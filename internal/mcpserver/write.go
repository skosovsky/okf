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

var errWriteConceptConflictRetriesExhausted = errors.New("write concept conflict retries exhausted")

// transactionalStore is deliberately the small part of the filesystem store
// used by this transport adapter. It lets lifecycle tests prove that every
// successful Open is paired with Close without putting test hooks in fs.
type transactionalStore interface {
	Snapshot(context.Context) (store.Snapshot, error)
	ReplaceConcept(context.Context, storefs.ReplaceConceptRequest, store.CommitOptions) (storefs.ReplaceConceptResult, error)
	Close() error
}

type transactionalStoreOpener func(
	context.Context,
	string,
	storefs.Config,
) (transactionalStore, error)

func openTransactionalStoreContext(
	ctx context.Context,
	root string,
	cfg storefs.Config,
) (transactionalStore, error) {
	return storefs.OpenContext(ctx, root, cfg)
}

// writeConcept is a cooperating writer: validation and publication both run
// through the filesystem store's snapshot, lease, journal and CAS pipeline.
func writeConcept(ctx context.Context, root string, id bundle.ConceptID, frontmatterText, body string) (writeConceptResponse, error) {
	return writeConceptCommit(ctx, root, id, frontmatterText, body)
}

func writeConceptCommit(ctx context.Context, root string, id bundle.ConceptID, frontmatterText, body string) (response writeConceptResponse, err error) {
	return writeConceptCommitWithMaxAttempts(
		ctx,
		root,
		id,
		frontmatterText,
		body,
		maxWriteConceptConflictRetries+1,
	)
}

func writeConceptCommitWithMaxAttempts(
	ctx context.Context,
	root string,
	id bundle.ConceptID,
	frontmatterText string,
	body string,
	maxAttempts int,
) (response writeConceptResponse, err error) {
	return writeConceptCommitWithStoreOpener(
		ctx,
		root,
		id,
		frontmatterText,
		body,
		maxAttempts,
		openTransactionalStoreContext,
	)
}

func writeConceptCommitWithStoreOpener(
	ctx context.Context,
	root string,
	id bundle.ConceptID,
	frontmatterText string,
	body string,
	maxAttempts int,
	open transactionalStoreOpener,
) (response writeConceptResponse, err error) {
	if ctx == nil {
		return writeConceptResponse{}, errNilRequestContext
	}
	if err := ctx.Err(); err != nil {
		return writeConceptResponse{}, err
	}
	if open == nil {
		return writeConceptResponse{}, errors.New("nil transactional store opener")
	}
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
	if err := rejectWriteSymlinksContext(ctx, root, targetPath); err != nil {
		return writeConceptResponse{}, err
	}

	frontmatter, err := bundle.ParseFrontmatterContext(ctx, frontmatterText)
	if err != nil {
		return writeConceptResponse{}, err
	}
	document := bundle.NewDocument(frontmatter, body)
	if _, err := document.Serialize(); err != nil {
		return writeConceptResponse{}, err
	}

	cfg := validator.ValidatorConfig{Strict: true, CheckLinks: true, CheckOrphans: true}
	transactionalStore, err := open(ctx, root, withMCPStoreLimits(storefs.Config{ValidatorConfig: &cfg}))
	if err != nil {
		return writeConceptResponse{}, err
	}
	if transactionalStore == nil {
		return writeConceptResponse{}, errors.New("transactional store opener returned nil store")
	}
	durable := false
	defer func() {
		if closeErr := transactionalStore.Close(); closeErr != nil {
			if durable {
				return
			}
			if err != nil {
				err = errors.Join(err, closeErr)
				return
			}
			response = writeConceptResponse{}
			err = closeErr
		}
	}()

	request := storefs.ReplaceConceptRequest{
		Actor:     store.Actor("okf-mcp"),
		ConceptID: id,
		Document:  document,
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return writeConceptResponse{}, err
		}
		snapshot, err := transactionalStore.Snapshot(ctx)
		if err != nil {
			return writeConceptResponse{}, err
		}
		loaded, err := loadBoundedSource(ctx, snapshot)
		if err != nil {
			return writeConceptResponse{}, err
		}
		resolution, err := loaded.VersionResolutionContext(ctx, "")
		if err != nil {
			return writeConceptResponse{}, err
		}
		changeSetID := writeConceptChangeSetIDWithPolicy(id, frontmatterText, body, resolution)
		// MCP does not accept a caller-supplied idempotency key. The canonical
		// server identity binds both content and the resolved OKF policy.
		request.ChangeSetID = changeSetID
		request.BaseRevision = snapshot.Revision()

		options := store.CommitOptions{IdempotencyKey: store.IdempotencyKey(changeSetID)}
		result, replaceErr := transactionalStore.ReplaceConcept(ctx, request, options)
		result, outcomeErr := classifyMCPWriteCommitOutcome(request, options, result, replaceErr)
		if outcomeErr == nil {
			durable = true
			return writeConceptSuccessContext(mcpPostCommitContext(ctx), root, targetPath, result.Validation)
		}
		var invalid *store.InvalidChangeSet
		if errors.As(outcomeErr, &invalid) {
			return writeConceptRejectedContext(ctx, root, targetPath, result.Validation, invalid.Diagnostics)
		}
		var conflict *store.Conflict
		if errors.As(outcomeErr, &conflict) && attempt+1 < maxAttempts {
			continue
		}
		return writeConceptResponse{}, outcomeErr
	}
	return writeConceptResponse{}, errWriteConceptConflictRetriesExhausted
}

func writeConceptSuccessContext(
	ctx context.Context,
	root string,
	targetPath string,
	report validator.Report,
) (writeConceptResponse, error) {
	projected, err := reportResponseContext(ctx, root, report)
	if err != nil {
		return writeConceptResponse{}, err
	}
	return writeConceptResponse{
		Status:      "success",
		Path:        relativeSlashPath(root, targetPath),
		Diagnostics: projected.Diagnostics,
	}, ctx.Err()
}

func writeConceptRejectedContext(
	ctx context.Context,
	root string,
	targetPath string,
	report validator.Report,
	rejected []store.Diagnostic,
) (writeConceptResponse, error) {
	if ctx == nil {
		return writeConceptResponse{}, errNilRequestContext
	}
	structured := make([]diagnosticProjection, 0, len(rejected))
	for _, diagnostic := range rejected {
		if err := ctx.Err(); err != nil {
			return writeConceptResponse{}, err
		}
		structured = append(structured, diagnosticDTOFromStore(root, diagnostic))
	}
	validated := make([]diagnosticProjection, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		if err := ctx.Err(); err != nil {
			return writeConceptResponse{}, err
		}
		validated = append(validated, diagnosticDTOFromValidator(root, diagnostic))
	}
	diagnostics, err := uniqueDiagnosticDTOsContext(ctx, structured, validated)
	if err != nil {
		return writeConceptResponse{}, err
	}
	return writeConceptResponse{
		Status:      "rejected",
		Path:        relativeSlashPath(root, targetPath),
		Diagnostics: diagnostics,
	}, ctx.Err()
}

func writeConceptChangeSetIDWithPolicy(id bundle.ConceptID, frontmatterText, body string, resolution bundle.VersionResolution) store.ChangeSetID {
	h := sha256.New()
	for _, field := range []string{
		"okf-mcp:write-concept:v3",
		id.String(),
		frontmatterText,
		body,
		resolution.Declared,
		resolution.Effective,
		string(resolution.Source),
		string(resolution.Compatibility),
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(field))
	}
	return store.ChangeSetID("mcp-replace-" + hex.EncodeToString(h.Sum(nil)))
}

func rejectWriteSymlinksContext(ctx context.Context, root, targetPath string) error {
	if ctx == nil {
		return errNilRequestContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return rejectExistingSymlinksContext(ctx, root, targetPath)
}

func rejectExistingSymlinksContext(ctx context.Context, root, targetPath string) error {
	if ctx == nil {
		return errNilRequestContext
	}
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
	for _, part := range parts {
		if err := ctx.Err(); err != nil {
			return err
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path contains symlink: %s", relativeSlashPath(cleanRoot, current))
		}
	}
	return ctx.Err()
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
