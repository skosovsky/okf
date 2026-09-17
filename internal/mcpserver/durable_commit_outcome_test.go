package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

func durableCommitReceipt(t *testing.T, id store.ChangeSetID, key store.IdempotencyKey, base store.Revision) store.CommitReceipt {
	t.Helper()
	return store.CommitReceipt{
		FormatVersion:  store.CommitReceiptFormatVersion,
		ChangeSetID:    id,
		IdempotencyKey: key,
		RequestDigest:  "sha256:" + strings.Repeat("3", 64),
		BaseRevision:   base,
		ResultRevision: store.Revision("sha256:" + strings.Repeat("4", 64)),
		CommitTime:     time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC),
		ChangedRefs:    []bundle.RelationRef{},
		ChangedFiles:   []store.FileChange{{Kind: store.FileWrite, Path: "entry.md"}},
	}
}

func TestClassifyMCPPatchCommitOutcomeTreatsAuthenticatedCommittedErrorAsSuccess(t *testing.T) {
	// Arrange.
	base := store.Revision("sha256:" + strings.Repeat("1", 64))
	change := durableCommitChangeSet(t, base)
	options := store.CommitOptions{IdempotencyKey: "patch:key"}
	receipt := durableCommitReceipt(t, change.ID, options.IdempotencyKey, base)
	digest, err := change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.RequestDigest = digest
	cause := &store.Conflict{Retryable: true}
	committedErr := store.NewCommittedError(receipt, cause)

	// Act.
	got, gotErr := classifyMCPPatchCommitOutcome(change, options, receipt, committedErr)

	// Assert.
	if gotErr != nil || !reflect.DeepEqual(got, receipt) {
		t.Fatalf("outcome = (%#v, %v), want authenticated durable receipt", got, gotErr)
	}
	got.ChangedFiles[0].Path = "mutated.md"
	if receipt.ChangedFiles[0].Path != "entry.md" {
		t.Fatal("classified patch receipt aliases the store receipt")
	}
}

func TestClassifyMCPPatchCommitOutcomeRejectsForeignCommittedReceiptBeforeConflictMapping(t *testing.T) {
	// Arrange.
	base := store.Revision("sha256:" + strings.Repeat("1", 64))
	change := durableCommitChangeSet(t, base)
	options := store.CommitOptions{IdempotencyKey: "patch:key"}
	receipt := durableCommitReceipt(t, change.ID, options.IdempotencyKey, base)
	digest, err := change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.RequestDigest = digest
	foreign := receipt.Clone()
	foreign.CommitTime = foreign.CommitTime.Add(time.Second)
	committedErr := store.NewCommittedError(foreign, &store.Conflict{Retryable: true})

	// Act.
	got, gotErr := classifyMCPPatchCommitOutcome(change, options, receipt, committedErr)

	// Assert.
	if !reflect.DeepEqual(got, store.CommitReceipt{}) || !errors.Is(gotErr, committedErr) ||
		!errors.Is(gotErr, mutation.ErrMigrationPlanMismatch) {
		t.Fatalf("outcome = (%#v, %v), want zero + original + plan mismatch", got, gotErr)
	}
}

func TestApplyPatchCommitWithStoreProjectsCommittedConflictAsSuccess(t *testing.T) {
	// Arrange.
	base := store.Revision("sha256:" + strings.Repeat("1", 64))
	change := durableCommitChangeSet(t, base)
	options := store.CommitOptions{IdempotencyKey: "patch:key"}
	receipt := durableCommitReceipt(t, change.ID, options.IdempotencyKey, base)
	digest, err := change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.RequestDigest = digest
	committer := durablePatchCommitter(func(context.Context, store.ChangeSet, store.CommitOptions) (store.CommitReceipt, error) {
		return receipt, store.NewCommittedError(receipt, &store.Conflict{Retryable: true})
	})

	// Act.
	result, applyErr := applyPatchCommitWithStore(t.Context(), committer, change, options, "sha256:"+strings.Repeat("5", 64))

	// Assert.
	if applyErr != nil || result == nil || result.IsError {
		t.Fatalf("result = %#v, err = %v; want durable success", result, applyErr)
	}
	text := resultText(t, result)
	if !strings.Contains(text, `"status":"applied"`) || !strings.Contains(text, `"changed_paths":["entry.md"]`) {
		t.Fatalf("durable patch response = %s", text)
	}
}

func TestApplyPatchCommitWithStoreMapsOrdinaryCancellation(t *testing.T) {
	// Arrange.
	base := store.Revision("sha256:" + strings.Repeat("1", 64))
	change := durableCommitChangeSet(t, base)
	committer := durablePatchCommitter(func(context.Context, store.ChangeSet, store.CommitOptions) (store.CommitReceipt, error) {
		return store.CommitReceipt{}, context.Canceled
	})

	// Act.
	result, applyErr := applyPatchCommitWithStore(t.Context(), committer, change, store.CommitOptions{}, "plan")

	// Assert.
	if applyErr != nil {
		t.Fatal(applyErr)
	}
	envelope, ok := result.StructuredContent.(errorEnvelope)
	if !ok || envelope.Code != "operation_cancelled" || !envelope.Retryable {
		t.Fatalf("result = %#v, want retryable operation_cancelled", result.StructuredContent)
	}
}

type durablePatchCommitter func(context.Context, store.ChangeSet, store.CommitOptions) (store.CommitReceipt, error)

func (commit durablePatchCommitter) Commit(
	ctx context.Context,
	change store.ChangeSet,
	options store.CommitOptions,
) (store.CommitReceipt, error) {
	return commit(ctx, change, options)
}

func durableCommitChangeSet(t *testing.T, base store.Revision) store.ChangeSet {
	t.Helper()
	operation, err := store.NewSetBundleVersion("0.2")
	if err != nil {
		t.Fatal(err)
	}
	return store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           "patch:durable",
		Actor:        "actor:test",
		BaseRevision: base,
		Operations:   []store.Operation{operation},
	}
}

func TestClassifyMCPWriteCommitOutcomeAndPostCommitContextPreserveDurableSuccess(t *testing.T) {
	// Arrange.
	base := store.Revision("sha256:" + strings.Repeat("1", 64))
	request := storefs.ReplaceConceptRequest{ChangeSetID: "write:durable", BaseRevision: base}
	options := store.CommitOptions{IdempotencyKey: "write:durable"}
	receipt := durableCommitReceipt(t, request.ChangeSetID, options.IdempotencyKey, base)
	result := storefs.ReplaceConceptResult{Receipt: receipt}
	cause := context.Canceled
	committedErr := store.NewCommittedError(receipt, cause)
	valueKey := struct{}{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), valueKey, "retained"))
	cancel()

	// Act.
	got, gotErr := classifyMCPWriteCommitOutcome(request, options, result, committedErr)
	postCtx := mcpPostCommitContext(ctx)

	// Assert.
	if gotErr != nil || !reflect.DeepEqual(got.Receipt, receipt) {
		t.Fatalf("outcome = (%#v, %v), want durable write result", got, gotErr)
	}
	if postCtx.Err() != nil || postCtx.Value(valueKey) != "retained" {
		t.Fatalf("post context = err %v value %#v", postCtx.Err(), postCtx.Value(valueKey))
	}
}

func TestClassifyMCPWriteCommitOutcomePreservesOrdinaryErrorIdentity(t *testing.T) {
	// Arrange.
	conflict := &store.Conflict{Retryable: true}

	// Act.
	got, gotErr := classifyMCPWriteCommitOutcome(
		storefs.ReplaceConceptRequest{}, store.CommitOptions{}, storefs.ReplaceConceptResult{}, conflict,
	)

	// Assert.
	if !reflect.DeepEqual(got, storefs.ReplaceConceptResult{}) || gotErr != conflict {
		t.Fatalf("outcome = (%#v, %v), want zero + exact ordinary error", got, gotErr)
	}
}

func TestClassifyMCPWriteCommitOutcomeAcceptsAuthenticatedReplayFromOriginalBase(t *testing.T) {
	// Arrange. ReplaceConcept checks idempotency before the fresh base, so a
	// replay returns the original receipt even when this invocation observed a
	// later snapshot revision.
	originalBase := store.Revision("sha256:" + strings.Repeat("1", 64))
	currentBase := store.Revision("sha256:" + strings.Repeat("2", 64))
	request := storefs.ReplaceConceptRequest{ChangeSetID: "write:replay", BaseRevision: currentBase}
	options := store.CommitOptions{IdempotencyKey: "write:replay"}
	receipt := durableCommitReceipt(t, request.ChangeSetID, options.IdempotencyKey, originalBase)

	// Act.
	got, gotErr := classifyMCPWriteCommitOutcome(
		request, options, storefs.ReplaceConceptResult{Receipt: receipt}, nil,
	)

	// Assert.
	if gotErr != nil || !reflect.DeepEqual(got.Receipt, receipt) {
		t.Fatalf("replay outcome = (%#v, %v), want original authenticated receipt", got, gotErr)
	}
}

func TestDurableWriteOutcomeAdversarialReceiptMatrix(t *testing.T) {
	base := store.Revision("sha256:" + strings.Repeat("1", 64))
	patch := durableCommitChangeSet(t, base)
	patchOptions := store.CommitOptions{IdempotencyKey: "patch:key"}
	patchReceipt := durableCommitReceipt(t, patch.ID, patchOptions.IdempotencyKey, base)
	digest, err := patch.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	patchReceipt.RequestDigest = digest
	writeRequest := storefs.ReplaceConceptRequest{ChangeSetID: "write:durable", BaseRevision: base}
	writeOptions := store.CommitOptions{IdempotencyKey: "write:durable"}
	writeReceipt := durableCommitReceipt(t, writeRequest.ChangeSetID, writeOptions.IdempotencyKey, base)

	tests := []struct {
		name string
		act  func() error
		want error
	}{
		{
			name: "patch invalid returned receipt",
			act: func() error {
				_, gotErr := classifyMCPPatchCommitOutcome(patch, patchOptions, store.CommitReceipt{}, nil)
				return gotErr
			},
			want: store.ErrStorageCorrupt,
		},
		{
			name: "patch ordinary exact error",
			act: func() error {
				ordinary := errors.New("ordinary patch")
				_, gotErr := classifyMCPPatchCommitOutcome(patch, patchOptions, store.CommitReceipt{}, ordinary)
				if gotErr != ordinary {
					t.Fatalf("ordinary patch identity = %v", gotErr)
				}
				return nil
			},
		},
		{
			name: "write invalid returned receipt masks cancellation",
			act: func() error {
				_, gotErr := classifyMCPWriteCommitOutcome(writeRequest, writeOptions,
					storefs.ReplaceConceptResult{}, errors.Join(&store.CommittedError{}, context.Canceled))
				return gotErr
			},
			want: store.ErrStorageCorrupt,
		},
		{
			name: "write mismatched committed receipt",
			act: func() error {
				foreign := writeReceipt.Clone()
				foreign.CommitTime = foreign.CommitTime.Add(time.Second)
				_, gotErr := classifyMCPWriteCommitOutcome(writeRequest, writeOptions,
					storefs.ReplaceConceptResult{Receipt: writeReceipt},
					store.NewCommittedError(foreign, &store.Conflict{Retryable: true}))
				return gotErr
			},
			want: mutation.ErrMigrationPlanMismatch,
		},
		{
			name: "write foreign request identity",
			act: func() error {
				foreign := writeReceipt.Clone()
				foreign.ChangeSetID = "write:foreign"
				_, gotErr := classifyMCPWriteCommitOutcome(writeRequest, writeOptions,
					storefs.ReplaceConceptResult{Receipt: foreign},
					store.NewCommittedError(foreign, context.Canceled))
				return gotErr
			},
			want: mutation.ErrMigrationPlanMismatch,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotErr := test.act()
			if test.want != nil && !errors.Is(gotErr, test.want) {
				t.Fatalf("error = %v, want %v", gotErr, test.want)
			}
		})
	}
}

func TestWriteConceptErrorPrioritizesReceiptAuthentication(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{"corruption over cancellation", errors.Join(errMCPWriteReceiptInvalid, context.Canceled), "operation_rejected"},
		{"plan mismatch over conflict", errors.Join(mutation.ErrMigrationPlanMismatch, &store.Conflict{Retryable: true}), "plan_mismatch"},
		{"ordinary storage corruption", fmt.Errorf("unsafe symlink: %w", store.ErrStorageCorrupt), "operation_rejected"},
		{"ordinary cancellation", context.Canceled, "operation_cancelled"},
		{"ordinary conflict", &store.Conflict{Retryable: true}, "revision_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := writeConceptError(test.err)
			envelope, ok := result.StructuredContent.(errorEnvelope)
			if !ok || envelope.Code != test.code {
				t.Fatalf("result = %#v, want code %s", result.StructuredContent, test.code)
			}
		})
	}
}

func TestWriteConceptCommitTreatsCommittedCancellationAndCloseFailureAsDurableSuccess(t *testing.T) {
	// Arrange.
	base := store.Revision("sha256:" + strings.Repeat("1", 64))
	snapshot := &durableWriteSnapshot{
		revision: base,
		files: map[string][]byte{
			"index.md": []byte("---\nokf_version: \"0.2\"\n---\n# Index\n"),
			"entry.md": []byte("---\ntype: Note\n---\nOld.\n"),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	opened := &durableWriteStore{snapshot: snapshot, closeErr: errors.New("late close")}
	opened.replace = func(request storefs.ReplaceConceptRequest, options store.CommitOptions) (storefs.ReplaceConceptResult, error) {
		receipt := durableCommitReceipt(t, request.ChangeSetID, options.IdempotencyKey, request.BaseRevision)
		cancel()
		return storefs.ReplaceConceptResult{Receipt: receipt}, store.NewCommittedError(receipt, context.Canceled)
	}
	id, err := bundle.ParseConceptID("entry")
	if err != nil {
		t.Fatal(err)
	}
	opener := func(context.Context, string, storefs.Config) (transactionalStore, error) { return opened, nil }

	// Act.
	response, writeErr := writeConceptCommitWithStoreOpener(
		ctx, t.TempDir(), id, "type: Note\n", "New.\n", 1, opener,
	)

	// Assert.
	if writeErr != nil || response.Status != "success" || response.Path != "entry.md" || response.Diagnostics == nil {
		t.Fatalf("response = %#v, err = %v; want durable success", response, writeErr)
	}
	if !opened.closed {
		t.Fatal("transactional store was not closed")
	}
}

type durableWriteSnapshot struct {
	revision store.Revision
	files    map[string][]byte
}

func (s *durableWriteSnapshot) Paths(context.Context) ([]string, error) {
	return []string{"entry.md", "index.md"}, nil
}

func (s *durableWriteSnapshot) ReadFile(_ context.Context, path string) ([]byte, error) {
	content, ok := s.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), content...), nil
}

func (s *durableWriteSnapshot) Revision() store.Revision { return s.revision }

func (*durableWriteSnapshot) OpenConcept(bundle.ConceptID) (bundle.Concept, error) {
	return bundle.Concept{}, fs.ErrNotExist
}

func (*durableWriteSnapshot) ListConcepts() ([]bundle.ConceptID, error) { return nil, nil }

type durableWriteStore struct {
	snapshot *durableWriteSnapshot
	replace  func(storefs.ReplaceConceptRequest, store.CommitOptions) (storefs.ReplaceConceptResult, error)
	closeErr error
	closed   bool
}

func (s *durableWriteStore) Snapshot(context.Context) (store.Snapshot, error) { return s.snapshot, nil }

func (s *durableWriteStore) ReplaceConcept(
	_ context.Context,
	request storefs.ReplaceConceptRequest,
	options store.CommitOptions,
) (storefs.ReplaceConceptResult, error) {
	return s.replace(request, options)
}

func (s *durableWriteStore) Close() error {
	s.closed = true
	return s.closeErr
}
