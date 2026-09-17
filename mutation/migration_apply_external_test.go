package mutation_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

type receiptMutatingStore struct {
	store.Store
	mutate func(*store.CommitReceipt)
}

func testMigrationResolution(t *testing.T, source bundle.Source) mutation.MigrationSourceResolution {
	t.Helper()
	resolution, err := mutation.ResolveMigrationSource(context.Background(), source, mutation.MigrationSourceOptions{
		RequestedSelector: mutation.MigrationSelectorAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolution
}

func (s receiptMutatingStore) Commit(ctx context.Context, change store.ChangeSet, options store.CommitOptions) (store.CommitReceipt, error) {
	receipt, err := s.Store.Commit(ctx, change, options)
	if err == nil && s.mutate != nil {
		s.mutate(&receipt)
	}
	return receipt, err
}

func TestMigrationPlannerApply_MarkerOnlyDocumentIsNotLegacyCitationEvidence(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	index := "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"
	markerOnly := "---\ntype: Knowledge\n---\n\nA numbered claim [1] remains prose.\n"
	for name, content := range map[string]string{"index.md": index, "a.md": markerOnly} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	destination, err := storefs.Open(root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destination.Close() })
	snapshot, err := destination.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := mutation.MigrationRequest{
		ID: "marker-only-apply", Actor: "operator",
		FromVersion: mutation.MigrationVersionV01,
		ToVersion:   mutation.MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	planner := mutation.NewMigrationPlanner()
	preview, err := planner.Preview(context.Background(), snapshot, testMigrationResolution(t, snapshot), request)
	if err != nil {
		t.Fatal(err)
	}
	apply := mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
		Options:    store.CommitOptions{IdempotencyKey: "marker-only-apply"},
	}

	// Act.
	receipt, applyErr := planner.Apply(context.Background(), destination, apply)
	replayed, replayErr := planner.Apply(context.Background(), destination, apply)

	// Assert.
	if applyErr != nil || replayErr != nil {
		t.Fatalf("Apply() errors = %v, %v", applyErr, replayErr)
	}
	if receipt.RequestDigest != replayed.RequestDigest ||
		receipt.ResultRevision != replayed.ResultRevision {
		t.Fatalf("replayed receipt = %#v, want %#v", replayed, receipt)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != markerOnly {
		t.Fatalf("marker-only document changed:\n%s", got)
	}
}

func TestMigrationPlannerApply_BindsExactRevisionAndPlanDigest(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	files := map[string]string{
		"index.md": "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n",
		"alpha.md": "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nAlpha.\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	destination, err := storefs.Open(root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destination.Close() })
	snapshot, err := destination.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := mutation.MigrationRequest{
		ID:          "migration-apply",
		Actor:       "operator",
		FromVersion: mutation.MigrationVersionV01,
		ToVersion:   mutation.MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	planner := mutation.NewMigrationPlanner()
	preview, err := planner.Preview(context.Background(), snapshot, testMigrationResolution(t, snapshot), request)
	if err != nil {
		t.Fatal(err)
	}
	badDigest := preview.PlanDigest[:len(preview.PlanDigest)-1] + "0"
	if badDigest == preview.PlanDigest {
		badDigest = preview.PlanDigest[:len(preview.PlanDigest)-1] + "1"
	}

	// Act.
	_, mismatchErr := planner.Apply(context.Background(), destination, mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: badDigest,
		Options:    store.CommitOptions{IdempotencyKey: "migration-apply-mismatch"},
	})
	_, hostileFreshErr := planner.Apply(context.Background(), receiptMutatingStore{
		Store: destination,
		mutate: func(receipt *store.CommitReceipt) {
			receipt.ChangedFiles = nil
		},
	}, mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
		Options:    store.CommitOptions{IdempotencyKey: "migration-apply-exact"},
	})
	receipt, applyErr := planner.Apply(context.Background(), destination, mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
		Options:    store.CommitOptions{IdempotencyKey: "migration-apply-exact"},
	})
	replayed, replayErr := planner.Apply(context.Background(), destination, mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
		Options:    store.CommitOptions{IdempotencyKey: "migration-apply-exact"},
	})
	_, hostileReplayErr := planner.Apply(context.Background(), receiptMutatingStore{
		Store: destination,
		mutate: func(receipt *store.CommitReceipt) {
			receipt.FormatVersion = 0
		},
	}, mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
		Options:    store.CommitOptions{IdempotencyKey: "migration-apply-exact"},
	})
	_, replayTamperErr := planner.Apply(context.Background(), destination, mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: badDigest,
		Options:    store.CommitOptions{IdempotencyKey: "migration-apply-exact"},
	})
	_, staleErr := planner.Apply(context.Background(), destination, mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
	})
	_, mismatchedKeyErr := planner.Apply(context.Background(), destination, mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
		Options:    store.CommitOptions{IdempotencyKey: "migration-apply-different"},
	})

	// Assert.
	if !errors.Is(mismatchErr, mutation.ErrMigrationPlanMismatch) {
		t.Fatalf("mismatched Apply() error = %v, want ErrMigrationPlanMismatch", mismatchErr)
	}
	if !errors.Is(hostileFreshErr, mutation.ErrMigrationPlanMismatch) {
		t.Fatalf("hostile fresh Apply() error = %v, want ErrMigrationPlanMismatch", hostileFreshErr)
	}
	if applyErr != nil {
		t.Fatalf("exact Apply() error = %v", applyErr)
	}
	if replayErr != nil {
		t.Fatalf("idempotent Apply() replay error = %v", replayErr)
	}
	if !errors.Is(hostileReplayErr, mutation.ErrMigrationPlanMismatch) {
		t.Fatalf("hostile replay Apply() error = %v, want ErrMigrationPlanMismatch", hostileReplayErr)
	}
	if !errors.Is(replayTamperErr, mutation.ErrMigrationPlanMismatch) {
		t.Fatalf("tampered replay Apply() error = %v, want ErrMigrationPlanMismatch", replayTamperErr)
	}
	if replayed.RequestDigest != receipt.RequestDigest ||
		replayed.BaseRevision != receipt.BaseRevision ||
		replayed.ResultRevision != receipt.ResultRevision ||
		!replayed.CommitTime.Equal(receipt.CommitTime) {
		t.Fatalf("replayed receipt = %#v, want %#v", replayed, receipt)
	}
	var staleConflict *store.Conflict
	if !errors.As(staleErr, &staleConflict) {
		t.Fatalf("stale Apply() error = %v, want *store.Conflict", staleErr)
	}
	var keyConflict *store.Conflict
	if !errors.As(mismatchedKeyErr, &keyConflict) {
		t.Fatalf("mismatched-key Apply() error = %v, want *store.Conflict", mismatchedKeyErr)
	}
	if receipt.BaseRevision != preview.Preview.BaseRevision || receipt.ResultRevision != preview.Preview.ResultRevision {
		t.Fatalf("receipt revisions = %s -> %s, preview = %s -> %s",
			receipt.BaseRevision, receipt.ResultRevision, preview.Preview.BaseRevision, preview.Preview.ResultRevision)
	}
	committed, err := os.ReadFile(filepath.Join(root, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) == files["index.md"] {
		t.Fatal("exact Apply did not publish the migration")
	}
}

func TestMigrationPlannerApply_ConcurrentWriterWinsWithoutPartialMigration(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	files := map[string]string{
		"index.md": "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n",
		"alpha.md": "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nAlpha.\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	destination, err := storefs.Open(root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destination.Close() })
	snapshot, err := destination.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := mutation.MigrationRequest{
		ID:          "migration-concurrent-writer",
		Actor:       "operator",
		FromVersion: mutation.MigrationVersionV01,
		ToVersion:   mutation.MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	planner := mutation.NewMigrationPlanner()
	preview, err := planner.Preview(context.Background(), snapshot, testMigrationResolution(t, snapshot), request)
	if err != nil {
		t.Fatal(err)
	}
	writerPath := filepath.Join(root, "writer.md")
	const writerContent = "concurrent writer state\n"
	if err := os.WriteFile(writerPath, []byte(writerContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act.
	_, applyErr := planner.Apply(context.Background(), destination, mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
	})

	// Assert.
	var conflict *store.Conflict
	if !errors.As(applyErr, &conflict) {
		t.Fatalf("Apply() error = %v, want *store.Conflict", applyErr)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s changed after concurrent conflict:\ngot:\n%s\nwant:\n%s", name, got, want)
		}
	}
	gotWriter, err := os.ReadFile(writerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotWriter) != writerContent {
		t.Fatalf("concurrent writer state = %q, want %q", gotWriter, writerContent)
	}
}

func TestMigrationPlannerApply_NoRootIndexKeepsPreviewRevisionStable(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	alphaPath := filepath.Join(root, "alpha.md")
	if err := os.WriteFile(alphaPath, []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nAlpha.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination, err := storefs.Open(root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destination.Close() })
	snapshot, err := destination.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := mutation.MigrationRequest{
		ID:          "migration-no-index",
		Actor:       "operator",
		FromVersion: mutation.MigrationVersionV01,
		ToVersion:   mutation.MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	planner := mutation.NewMigrationPlanner()

	// Act.
	preview, previewErr := planner.Preview(context.Background(), snapshot, testMigrationResolution(t, snapshot), request)
	if previewErr != nil {
		t.Fatalf("Preview() error = %v", previewErr)
	}
	receipt, applyErr := planner.Apply(context.Background(), destination, mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
		Options:    store.CommitOptions{IdempotencyKey: "migration-no-index"},
	})

	// Assert.
	if applyErr != nil {
		t.Fatalf("Apply() error = %v", applyErr)
	}
	if receipt.ResultRevision != preview.Preview.ResultRevision {
		t.Fatalf("receipt result = %s, preview result = %s", receipt.ResultRevision, preview.Preview.ResultRevision)
	}
	if _, err := os.Stat(filepath.Join(root, "index.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("migration invented index.md: %v", err)
	}
	updated, err := os.ReadFile(alphaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) == "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nAlpha.\n" {
		t.Fatal("no-index migration did not update concept")
	}
	postSnapshot, err := destination.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	replayPreview, err := planner.Preview(context.Background(), postSnapshot, testMigrationResolution(t, postSnapshot), request)
	if err != nil {
		t.Fatalf("no-index replay Preview() error = %v", err)
	}
	if len(replayPreview.Preview.Writes) != 0 ||
		replayPreview.Preview.BaseRevision != replayPreview.Preview.ResultRevision {
		t.Fatalf("no-index identical replay is not a noop: %#v", replayPreview.Preview)
	}
}

func TestMigrationPlannerApply_RejectsFreshAndStaleReceiptProjectionTampering(t *testing.T) {
	extraRef, err := bundle.ParseRelationRef("z")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*store.CommitReceipt){
		"result revision": func(value *store.CommitReceipt) {
			value.ResultRevision = value.BaseRevision
		},
		"changed files extra": func(value *store.CommitReceipt) {
			value.ChangedFiles = append(value.ChangedFiles, store.FileChange{Kind: store.FileWrite, Path: "z.md"})
		},
		"changed files missing": func(value *store.CommitReceipt) {
			value.ChangedFiles = append([]store.FileChange{}, value.ChangedFiles[:len(value.ChangedFiles)-1]...)
		},
		"changed files duplicate": func(value *store.CommitReceipt) {
			value.ChangedFiles = append(value.ChangedFiles, value.ChangedFiles[len(value.ChangedFiles)-1])
		},
		"changed files order": func(value *store.CommitReceipt) {
			value.ChangedFiles[0], value.ChangedFiles[1] = value.ChangedFiles[1], value.ChangedFiles[0]
		},
		"changed refs extra": func(value *store.CommitReceipt) {
			value.ChangedRefs = append(value.ChangedRefs, extraRef)
		},
		"changed refs missing": func(value *store.CommitReceipt) {
			value.ChangedRefs = append([]bundle.RelationRef{}, value.ChangedRefs[:len(value.ChangedRefs)-1]...)
		},
		"changed refs duplicate": func(value *store.CommitReceipt) {
			value.ChangedRefs = append(value.ChangedRefs, value.ChangedRefs[len(value.ChangedRefs)-1])
		},
		"changed refs order": func(value *store.CommitReceipt) {
			value.ChangedRefs[0], value.ChangedRefs[1] = value.ChangedRefs[1], value.ChangedRefs[0]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			files := map[string]string{
				"index.md": "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n- [B](b.md)\n",
				"a.md":     "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n",
				"b.md":     "---\ntype: Knowledge\ntimestamp: 2026-06-26T09:00:00Z\n---\n",
			}
			for path, content := range files {
				if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			destination, err := storefs.Open(root, storefs.Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = destination.Close() })
			snapshot, err := destination.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			request := mutation.MigrationRequest{
				ID:    "receipt-tamper-" + store.ChangeSetID(strings.ReplaceAll(name, " ", "-")),
				Actor: "operator", FromVersion: mutation.MigrationVersionV01, ToVersion: mutation.MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					GeneratedBy:       "process:migration",
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
				},
			}
			planner := mutation.NewMigrationPlanner()
			preview, err := planner.Preview(context.Background(), snapshot, testMigrationResolution(t, snapshot), request)
			if err != nil {
				t.Fatal(err)
			}
			apply := mutation.MigrationApplyRequest{
				Resolution: preview.Resolution,
				Request:    request, Proof: preview.Proof, PlanDigest: preview.PlanDigest,
				Options: store.CommitOptions{IdempotencyKey: "tamper-" + store.IdempotencyKey(strings.ReplaceAll(name, " ", "-"))},
			}

			// Act.
			_, freshErr := planner.Apply(context.Background(), receiptMutatingStore{Store: destination, mutate: mutate}, apply)
			_, replayErr := planner.Apply(context.Background(), destination, apply)
			_, staleErr := planner.Apply(context.Background(), receiptMutatingStore{Store: destination, mutate: mutate}, apply)

			// Assert.
			if !errors.Is(freshErr, mutation.ErrMigrationPlanMismatch) {
				t.Fatalf("fresh tamper error = %v, want ErrMigrationPlanMismatch", freshErr)
			}
			if replayErr != nil {
				t.Fatalf("valid replay error = %v", replayErr)
			}
			if !errors.Is(staleErr, mutation.ErrMigrationPlanMismatch) {
				t.Fatalf("stale tamper error = %v, want ErrMigrationPlanMismatch", staleErr)
			}
		})
	}
}
