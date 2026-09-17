package mutation_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

func TestMigrationPlanProofStructuralRefs_ApplyReplayAndTamper(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	files := map[string]string{
		"index.md": "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Escaped](a%23x.md)\n- [Upper](aZ.md)\n",
		"a#x.md":   "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nEscaped.\n",
		"aZ.md":    "---\ntype: Knowledge\ntimestamp: 2026-06-26T09:00:00Z\n---\n\nUpper.\n",
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
		ID: "proof-structural-apply", Actor: "operator",
		FromVersion: mutation.MigrationVersionV01,
		ToVersion:   mutation.MigrationVersionV02,
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
	escapedRoot, err := bundle.ParseRelationRef(`a\#x`)
	if err != nil {
		t.Fatal(err)
	}
	upper, err := bundle.ParseRelationRef("aZ")
	if err != nil {
		t.Fatal(err)
	}
	escapedIndex := relationRefIndex(preview.Proof.ChangedRefs, escapedRoot)
	upperIndex := relationRefIndex(preview.Proof.ChangedRefs, upper)
	if escapedIndex < 0 || upperIndex < 0 || escapedIndex >= upperIndex {
		t.Fatalf("proof refs are not structurally ordered: %#v", preview.Proof.ChangedRefs)
	}
	apply := mutation.MigrationApplyRequest{
		Resolution: preview.Resolution,
		Request:    request, Proof: preview.Proof, PlanDigest: preview.PlanDigest,
		Options: store.CommitOptions{IdempotencyKey: "proof-structural-apply"},
	}
	tamperedProof := apply
	tamperedProof.Proof = preview.Proof.Clone()
	tamperedProof.Proof.ChangedRefs[escapedIndex], tamperedProof.Proof.ChangedRefs[upperIndex] =
		tamperedProof.Proof.ChangedRefs[upperIndex], tamperedProof.Proof.ChangedRefs[escapedIndex]

	// Act.
	_, proofTamperErr := planner.Apply(context.Background(), destination, tamperedProof)
	receipt, applyErr := planner.Apply(context.Background(), destination, apply)
	replayed, replayErr := planner.Apply(context.Background(), destination, apply)
	_, receiptTamperErr := planner.Apply(
		context.Background(),
		receiptMutatingStore{
			Store: destination,
			mutate: func(value *store.CommitReceipt) {
				value.ChangedRefs[escapedIndex], value.ChangedRefs[upperIndex] =
					value.ChangedRefs[upperIndex], value.ChangedRefs[escapedIndex]
			},
		},
		apply,
	)

	// Assert.
	if !errors.Is(proofTamperErr, mutation.ErrMigrationPlanMismatch) {
		t.Fatalf("tampered proof Apply() error = %v, want ErrMigrationPlanMismatch", proofTamperErr)
	}
	if applyErr != nil || replayErr != nil {
		t.Fatalf("Apply() errors = %v, %v", applyErr, replayErr)
	}
	if receipt.RequestDigest != replayed.RequestDigest ||
		receipt.BaseRevision != replayed.BaseRevision ||
		receipt.ResultRevision != replayed.ResultRevision ||
		!receipt.CommitTime.Equal(replayed.CommitTime) {
		t.Fatalf("replayed receipt = %#v, want %#v", replayed, receipt)
	}
	if !errors.Is(receiptTamperErr, mutation.ErrMigrationPlanMismatch) {
		t.Fatalf("tampered receipt Apply() error = %v, want ErrMigrationPlanMismatch", receiptTamperErr)
	}
}

func relationRefIndex(values []bundle.RelationRef, target bundle.RelationRef) int {
	for index, value := range values {
		if value.ID.String() == target.ID.String() && value.Fragment == target.Fragment {
			return index
		}
	}
	return -1
}
