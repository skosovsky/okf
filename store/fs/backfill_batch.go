package fs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/receiptprojection"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

// BackfillDocument is one desired concept document in an atomic backfill batch.
type BackfillDocument struct {
	ConceptID bundle.ConceptID
	Document  bundle.Document
}

type BackfillIndex struct {
	Path    string
	Content string
}

// BackfillBatchRequest binds all concept replacements and the root index to a
// single revision. IndexDocument preserves the caller's chosen root contract.
type BackfillBatchRequest struct {
	ChangeSetID    store.ChangeSetID
	Actor          store.Actor
	BaseRevision   store.Revision
	IndexDocument  bundle.Document
	IndexDocuments []BackfillIndex
	Documents      []BackfillDocument
}

type BackfillBatchPreview struct {
	BaseRevision   store.Revision
	ResultRevision store.Revision
	Validation     validator.Report
	ChangedFiles   []store.FileChange
}

func backfillBatchDigest(req BackfillBatchRequest, index string, documents map[string]string) (string, error) {
	h := sha256.New()
	write := func(s string) error {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		if _, err := h.Write(n[:]); err != nil {
			return err
		}
		_, err := h.Write([]byte(s))
		return err
	}
	for _, s := range []string{"okf:backfill-batch:v1", string(req.ChangeSetID), string(req.Actor), index} {
		if err := write(s); err != nil {
			return "", err
		}
	}
	paths := make([]string, 0, len(documents))
	for p := range documents {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := write(p); err != nil {
			return "", err
		}
		if err := write(documents[p]); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func validateBackfillBatchRequest(req BackfillBatchRequest, options store.CommitOptions) (string, map[string]string, string, error) {
	if err := req.ChangeSetID.Validate(); err != nil {
		return "", nil, "", err
	}
	if err := req.Actor.Validate(); err != nil {
		return "", nil, "", err
	}
	if !req.BaseRevision.Valid() {
		return "", nil, "", fmt.Errorf("invalid base revision")
	}
	if err := options.Validate(); err != nil {
		return "", nil, "", err
	}
	if len(req.Documents) == 0 {
		return "", nil, "", fmt.Errorf("empty backfill batch")
	}
	index, err := req.IndexDocument.Serialize()
	if err != nil {
		return "", nil, "", err
	}
	if !req.IndexDocument.HasFrontmatter {
		return "", nil, "", fmt.Errorf("root index declaration required")
	}
	docs := make(map[string]string, len(req.Documents))
	for _, entry := range req.IndexDocuments {
		p := entry.Path
		if p == "index.md" || !strings.HasSuffix(p, "/index.md") || !safePath(p) {
			return "", nil, "", fmt.Errorf("invalid directory index path %q", p)
		}
		if _, ok := docs[p]; ok {
			return "", nil, "", fmt.Errorf("duplicate directory index %q", p)
		}
		if entry.Content == "" {
			return "", nil, "", fmt.Errorf("empty directory index %q", p)
		}
		parsed, err := bundle.ParseDocument(entry.Content)
		if err != nil {
			return "", nil, "", err
		}
		if parsed.HasFrontmatter {
			return "", nil, "", fmt.Errorf("directory index %q must not have frontmatter", p)
		}
		docs[p] = entry.Content
	}
	for _, d := range req.Documents {
		if err := bundle.ValidateConceptID(d.ConceptID); err != nil {
			return "", nil, "", err
		}
		p := d.ConceptID.String() + ".md"
		if !safePath(p) || strings.HasPrefix(filepath.Base(p), ".") {
			return "", nil, "", fmt.Errorf("invalid concept path")
		}
		if _, ok := docs[p]; ok {
			return "", nil, "", fmt.Errorf("duplicate concept %s", p)
		}
		raw, err := d.Document.Serialize()
		if err != nil {
			return "", nil, "", err
		}
		docs[p] = raw
	}
	digest, err := backfillBatchDigest(req, index, docs)
	return index, docs, digest, err
}

func (s *Store) stageBackfillBatch(ctx context.Context, base store.Snapshot, req BackfillBatchRequest, index string, docs map[string]string) (*snapshot, validator.Report, error) {
	o, err := mutation.NewOverlayContext(ctx, base)
	if err != nil {
		return nil, validator.Report{}, err
	}
	if err := o.PutContext(ctx, "index.md", []byte(index)); err != nil {
		return nil, validator.Report{}, err
	}
	paths := make([]string, 0, len(docs))
	for p := range docs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, validator.Report{}, err
		}
		if err := o.PutContext(ctx, p, []byte(docs[p])); err != nil {
			return nil, validator.Report{}, err
		}
	}
	report, blocked, stagedBundle, err := s.validateStagedSource(ctx, o)
	if err != nil {
		return nil, validator.Report{}, err
	}
	if report.ExitCode() != 0 || blocked {
		return nil, report, &store.InvalidChangeSet{Code: "staged_validation_failed", Diagnostics: relationStoreDiagnostics(stagedBundle.RelationDiagnostics())}
	}
	next, err := snapshotFromSource(ctx, o, s.config.HashAlgorithm)
	return next, report, err
}

// PreviewBackfillBatch validates the complete desired bundle without writing.
func (s *Store) PreviewBackfillBatch(ctx context.Context, req BackfillBatchRequest) (BackfillBatchPreview, error) {
	if err := ctx.Err(); err != nil {
		return BackfillBatchPreview{}, err
	}
	index, docs, _, err := validateBackfillBatchRequest(req, store.CommitOptions{})
	if err != nil {
		return BackfillBatchPreview{}, err
	}
	base, err := s.Snapshot(ctx)
	if err != nil {
		return BackfillBatchPreview{}, err
	}
	if base.Revision() != req.BaseRevision {
		return BackfillBatchPreview{}, &store.Conflict{Expected: req.BaseRevision, Actual: base.Revision(), Retryable: true}
	}
	next, report, err := s.stageBackfillBatch(ctx, base, req, index, docs)
	if err != nil {
		return BackfillBatchPreview{Validation: report}, err
	}
	projection, err := receiptprojection.Derive(ctx, base.(*snapshot).concepts, next.concepts)
	if err != nil {
		return BackfillBatchPreview{}, err
	}
	return BackfillBatchPreview{BaseRevision: base.Revision(), ResultRevision: next.Revision(), Validation: report, ChangedFiles: projection.ChangedFiles}, nil
}

// ApplyBackfillBatch publishes all candidate documents and index in one store
// journal transaction. Readers using Store see either old or new snapshot.
func (s *Store) ApplyBackfillBatch(ctx context.Context, req BackfillBatchRequest, options store.CommitOptions) (store.CommitReceipt, error) {
	if err := ctx.Err(); err != nil {
		return store.CommitReceipt{}, err
	}
	index, docs, digest, err := validateBackfillBatchRequest(req, options)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if err := s.recoverPending(ctx); err != nil {
		return projectCommitRecoveryError(err, options.IdempotencyKey, digest)
	}
	if options.IdempotencyKey != "" {
		if r, found, err := s.lookupReceiptContext(ctx, options.IdempotencyKey, digest); err != nil || found {
			return r, err
		}
	}
	base, err := s.Snapshot(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if options.IdempotencyKey != "" {
		if r, found, err := s.lookupReceiptContext(ctx, options.IdempotencyKey, digest); err != nil || found {
			return r, err
		}
	}
	if base.Revision() != req.BaseRevision {
		return store.CommitReceipt{}, &store.Conflict{Expected: req.BaseRevision, Actual: base.Revision(), Retryable: true}
	}
	next, _, err := s.stageBackfillBatch(ctx, base, req, index, docs)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return store.CommitReceipt{}, err
	}
	if s.beforeMutationLease != nil {
		s.beforeMutationLease()
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	defer unlock()
	if err := s.prepareLocked(ctx); err != nil {
		return projectCommitRecoveryError(err, options.IdempotencyKey, digest)
	}
	if options.IdempotencyKey != "" {
		if r, found, err := s.lookupReceiptContext(ctx, options.IdempotencyKey, digest); err != nil || found {
			return r, err
		}
	}
	current, err := s.snapshotUnlocked(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if current.Revision() != base.Revision() {
		projection, e := receiptprojection.Derive(ctx, base.(*snapshot).concepts, current.(*snapshot).concepts)
		if e != nil {
			return store.CommitReceipt{}, e
		}
		return store.CommitReceipt{}, &store.Conflict{Expected: base.Revision(), Actual: current.Revision(), ChangedRefs: projection.ChangedRefs, Retryable: true}
	}
	projection, err := receiptprojection.Derive(ctx, base.(*snapshot).concepts, next.concepts)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	receipt := store.CommitReceipt{FormatVersion: store.CommitReceiptFormatVersion, ChangeSetID: req.ChangeSetID, IdempotencyKey: options.IdempotencyKey, RequestDigest: digest, BaseRevision: base.Revision(), ResultRevision: next.Revision(), CommitTime: time.Now().UTC(), ChangedRefs: projection.ChangedRefs, ChangedFiles: projection.ChangedFiles}
	if err := s.publish(ctx, next, receipt); err != nil {
		var committed *store.CommittedError
		if errors.As(err, &committed) {
			return committed.Receipt(), err
		}
		return store.CommitReceipt{}, err
	}
	return receipt.Clone(), nil
}

// VerifyBackfillBatchReceipt reads the store's authenticated receipt for this
// exact request identity. It never creates a new publication.
func (s *Store) VerifyBackfillBatchReceipt(ctx context.Context, req BackfillBatchRequest, key store.IdempotencyKey, claimed store.CommitReceipt) error {
	actual, found, err := s.LookupBackfillBatchReceipt(ctx, req, key)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("backfill receipt absent from store")
	}
	left, err := json.Marshal(actual)
	if err != nil {
		return err
	}
	right, err := json.Marshal(claimed)
	if err != nil {
		return err
	}
	if !bytes.Equal(left, right) {
		return fmt.Errorf("backfill checkpoint receipt differs from store")
	}
	return nil
}

// LookupBackfillBatchReceipt returns a canonical store receipt without
// creating a new publication. It also completes any earlier durable journal.
func (s *Store) LookupBackfillBatchReceipt(ctx context.Context, req BackfillBatchRequest, key store.IdempotencyKey) (store.CommitReceipt, bool, error) {
	if key == "" {
		return store.CommitReceipt{}, false, fmt.Errorf("idempotency key required")
	}
	_, _, digest, err := validateBackfillBatchRequest(req, store.CommitOptions{IdempotencyKey: key})
	if err != nil {
		return store.CommitReceipt{}, false, err
	}
	if err := s.recoverPending(ctx); err != nil {
		return store.CommitReceipt{}, false, err
	}
	actual, found, err := s.lookupReceiptContext(ctx, key, digest)
	return actual, found, err
}
