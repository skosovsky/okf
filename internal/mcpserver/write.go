package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
	"github.com/skosovsky/okf/validator"
)

const maxWriteConceptConflictRetries = 2

// transactionalStore is deliberately the small part of the filesystem store
// used by this transport adapter. It lets lifecycle tests prove that every
// successful Open is paired with Close without putting test hooks in fs.
type transactionalStore interface {
	Snapshot(context.Context) (store.Snapshot, error)
	ReplaceConcept(context.Context, storefs.ReplaceConceptRequest, store.CommitOptions) (storefs.ReplaceConceptResult, error)
	Close() error
}

var openTransactionalStore = func(root string, cfg storefs.Config) (transactionalStore, error) {
	return storefs.Open(root, cfg)
}

// writeConcept is a cooperating writer: validation and publication both run
// through the filesystem store's snapshot, lease, journal and CAS pipeline.
func writeConcept(ctx context.Context, root string, id bundle.ConceptID, frontmatterText, body string) (writeConceptResponse, error) {
	return writeConceptCommit(ctx, root, id, frontmatterText, body)
}

func writeConceptCommit(ctx context.Context, root string, id bundle.ConceptID, frontmatterText, body string) (response writeConceptResponse, err error) {
	// MCP strings usually originate in JSON, whose decoder replaces malformed
	// UTF-8. Keep the programmatic transport entry point equally strict before
	// serializing a document to the filesystem store.
	if !utf8.ValidString(body) {
		return writeConceptResponse{}, fmt.Errorf("%w: invalid UTF-8", bundle.ErrInvalidEncoding)
	}

	targetPath := id.ToPath(root)
	if !isInside(root, targetPath) {
		return writeConceptResponse{}, fmt.Errorf("concept path escapes bundle root")
	}
	// Keep the transport-level error contract for a directly addressed symlink.
	// The store independently rejects every revision-visible symlink while
	// capturing its snapshot, closing the time-of-check/time-of-use gap here.
	if err := rejectWriteSymlinks(root, targetPath); err != nil {
		return writeConceptResponse{}, err
	}

	frontmatter, err := bundle.ParseFrontmatter(frontmatterText)
	if err != nil {
		return writeConceptResponse{}, err
	}
	document := bundle.NewDocument(frontmatter, body)
	if _, err := document.Serialize(); err != nil {
		return writeConceptResponse{}, err
	}

	cfg := validator.ValidatorConfig{Strict: true, CheckLinks: true, CheckOrphans: true}
	transactionalStore, err := openTransactionalStore(root, storefs.Config{ValidatorConfig: &cfg})
	if err != nil {
		return writeConceptResponse{}, err
	}
	defer func() {
		if closeErr := transactionalStore.Close(); closeErr != nil {
			if err != nil {
				err = errors.Join(err, closeErr)
				return
			}
			response = writeConceptResponse{}
			err = closeErr
		}
	}()

	changeSetID := writeConceptChangeSetID(id, frontmatterText, body)
	// MCP does not accept a caller-supplied idempotency key. The canonical
	// ChangeSetID is stable for an identical write request and is therefore the
	// server-generated identity used by the store receipt protocol.
	idempotencyKey := store.IdempotencyKey(changeSetID)
	request := storefs.ReplaceConceptRequest{
		ChangeSetID: changeSetID,
		Actor:       store.Actor("okf-mcp"),
		ConceptID:   id,
		Document:    document,
	}
	for attempt := 0; attempt <= maxWriteConceptConflictRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return writeConceptResponse{}, err
		}
		snapshot, err := transactionalStore.Snapshot(ctx)
		if err != nil {
			return writeConceptResponse{}, err
		}
		request.BaseRevision = snapshot.Revision()

		result, err := transactionalStore.ReplaceConcept(ctx, request, store.CommitOptions{IdempotencyKey: idempotencyKey})
		if err == nil {
			return writeConceptSuccess(root, targetPath, result.Validation), nil
		}
		var invalid *store.InvalidChangeSet
		if errors.As(err, &invalid) {
			return writeConceptRejected(root, targetPath, result.Validation, invalid.Diagnostics), nil
		}
		var conflict *store.Conflict
		if errors.As(err, &conflict) && attempt < maxWriteConceptConflictRetries {
			continue
		}
		return writeConceptResponse{}, err
	}
	panic("unreachable")
}

func writeConceptSuccess(root, targetPath string, report validator.Report) writeConceptResponse {
	return writeConceptResponse{
		Status:      "success",
		Path:        relativeSlashPath(root, targetPath),
		Diagnostics: reportResponse(root, report).Diagnostics,
	}
}

func writeConceptRejected(root, targetPath string, report validator.Report, rejected []store.Diagnostic) writeConceptResponse {
	structured := make([]diagnosticProjection, 0, len(rejected))
	for _, diagnostic := range rejected {
		structured = append(structured, diagnosticDTOFromStore(root, diagnostic))
	}
	validated := make([]diagnosticProjection, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		validated = append(validated, diagnosticDTOFromValidator(root, diagnostic))
	}
	return writeConceptResponse{
		Status:      "rejected",
		Path:        relativeSlashPath(root, targetPath),
		Diagnostics: uniqueDiagnosticDTOs(structured, validated),
	}
}

// writeConceptChangeSetID binds a retried MCP request to its semantic input
// with an unambiguous, ordered, length-prefixed encoding. The v2 domain
// separates this corrected representation from the previous delimiter-based
// identity format.
func writeConceptChangeSetID(id bundle.ConceptID, frontmatterText, body string) store.ChangeSetID {
	h := sha256.New()
	for _, field := range []string{"okf-mcp:write-concept:v2", id.String(), frontmatterText, body} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(field))
	}
	return store.ChangeSetID("mcp-replace-" + hex.EncodeToString(h.Sum(nil)))
}

func rejectReadSymlinks(root, targetPath string) error {
	return rejectExistingSymlinks(root, targetPath, true)
}

func rejectWriteSymlinks(root, targetPath string) error {
	return rejectExistingSymlinks(root, targetPath, false)
}

func rejectExistingSymlinks(root, targetPath string, requireTarget bool) error {
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(targetPath)
	if !isInside(cleanRoot, cleanTarget) {
		return fmt.Errorf("path escapes bundle root")
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return err
	}
	parts := splitPath(rel)
	current := cleanRoot
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				if requireTarget && i < len(parts)-1 {
					return err
				}
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path contains symlink: %s", relativeSlashPath(cleanRoot, current))
		}
	}
	return nil
}

func splitPath(path string) []string {
	if path == "." || path == "" {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return filtered
}
