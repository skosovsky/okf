package mutation

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestMigrationPlanProofClone_PreservesEverySliceShapeAndOwnership(t *testing.T) {
	// Arrange.
	firstRef := ref(t, "first#fragment")
	secondRef := ref(t, "second#fragment")

	// Act and assert.
	assertMigrationProofSliceClone(
		t,
		"Reads",
		[]store.Read{{Path: "first.md"}},
		store.Read{Path: "second.md"},
		func(proof *MigrationPlanProof, values []store.Read) { proof.Reads = values },
		func(proof MigrationPlanProof) []store.Read { return proof.Reads },
	)
	assertMigrationProofSliceClone(
		t,
		"Writes",
		[]MigrationPlanWrite{{Path: "first.md", Digest: "first"}},
		MigrationPlanWrite{Path: "second.md", Digest: "second"},
		func(proof *MigrationPlanProof, values []MigrationPlanWrite) { proof.Writes = values },
		func(proof MigrationPlanProof) []MigrationPlanWrite { return proof.Writes },
	)
	assertMigrationProofSliceClone(
		t,
		"Deletes",
		[]string{"first.md"},
		"second.md",
		func(proof *MigrationPlanProof, values []string) { proof.Deletes = values },
		func(proof MigrationPlanProof) []string { return proof.Deletes },
	)
	assertMigrationProofSliceClone(
		t,
		"Renames",
		[]store.Rename{{From: "first.md", To: "first-new.md"}},
		store.Rename{From: "second.md", To: "second-new.md"},
		func(proof *MigrationPlanProof, values []store.Rename) { proof.Renames = values },
		func(proof MigrationPlanProof) []store.Rename { return proof.Renames },
	)
	assertMigrationProofSliceClone(
		t,
		"AffectedRefs",
		[]bundle.RelationRef{firstRef},
		secondRef,
		func(proof *MigrationPlanProof, values []bundle.RelationRef) { proof.AffectedRefs = values },
		func(proof MigrationPlanProof) []bundle.RelationRef { return proof.AffectedRefs },
	)
	assertMigrationProofSliceClone(
		t,
		"ReverseImpact",
		[]bundle.RelationRef{firstRef},
		secondRef,
		func(proof *MigrationPlanProof, values []bundle.RelationRef) { proof.ReverseImpact = values },
		func(proof MigrationPlanProof) []bundle.RelationRef { return proof.ReverseImpact },
	)
	assertMigrationProofSliceClone(
		t,
		"ChangedFiles",
		[]store.FileChange{{Kind: store.FileWrite, Path: "first.md"}},
		store.FileChange{Kind: store.FileDelete, Path: "second.md"},
		func(proof *MigrationPlanProof, values []store.FileChange) { proof.ChangedFiles = values },
		func(proof MigrationPlanProof) []store.FileChange { return proof.ChangedFiles },
	)
	assertMigrationProofSliceClone(
		t,
		"ChangedRefs",
		[]bundle.RelationRef{firstRef},
		secondRef,
		func(proof *MigrationPlanProof, values []bundle.RelationRef) { proof.ChangedRefs = values },
		func(proof MigrationPlanProof) []bundle.RelationRef { return proof.ChangedRefs },
	)
}

func assertMigrationProofSliceClone[T any](
	t *testing.T,
	name string,
	nonempty []T,
	replacement T,
	set func(*MigrationPlanProof, []T),
	get func(MigrationPlanProof) []T,
) {
	t.Helper()
	states := []struct {
		name   string
		values []T
	}{
		{name: "nil", values: nil},
		{name: "non-nil empty", values: make([]T, 0)},
		{name: "nonempty", values: nonempty},
	}
	for _, state := range states {
		t.Run(name+"/"+state.name, func(t *testing.T) {
			// Arrange.
			proof := MigrationPlanProof{}
			set(&proof, state.values)
			original, err := cloneMigrationProofSliceContext(context.Background(), state.values)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			cloned := proof.Clone()
			got := get(cloned)
			source := get(proof)

			// Assert.
			if (got == nil) != (state.values == nil) {
				t.Fatalf("Clone() nil shape = %t, want %t", got == nil, state.values == nil)
			}
			if !reflect.DeepEqual(got, state.values) {
				t.Fatalf("Clone() value = %#v, want %#v", got, state.values)
			}
			if len(got) == 0 {
				return
			}
			if &got[0] == &source[0] {
				t.Fatal("Clone() retained caller-owned backing array")
			}

			var zero T
			got[0] = zero
			if !reflect.DeepEqual(source[0], original[0]) {
				t.Fatalf("mutating clone changed source to %#v", source[0])
			}
			source[0] = replacement
			if !reflect.DeepEqual(got[0], zero) {
				t.Fatalf("mutating source changed clone to %#v", got[0])
			}
		})
	}
}

func TestMigrationPlanProofClone_RepeatedConcurrentCopiesAreIndependent(t *testing.T) {
	// Arrange.
	original := migrationProofCloneFixture(t)
	const goroutines = 32
	const iterations = 128

	// Act.
	var wait sync.WaitGroup
	wait.Add(goroutines)
	for worker := 0; worker < goroutines; worker++ {
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				cloned := original.Clone()
				cloned.Reads[0].Path = "mutated-read.md"
				cloned.Writes[0].Path = "mutated-write.md"
				cloned.Deletes[0] = "mutated-delete.md"
				cloned.Renames[0].From = "mutated-rename.md"
				cloned.AffectedRefs[0].Fragment = "mutated-affected"
				cloned.ReverseImpact[0].Fragment = "mutated-reverse"
				cloned.ChangedFiles[0].Path = "mutated-change.md"
				cloned.ChangedRefs[0].Fragment = "mutated-changed"
			}
		}()
	}
	wait.Wait()

	// Assert.
	want := migrationProofCloneFixture(t)
	if !reflect.DeepEqual(original, want) {
		t.Fatalf("concurrent Clone() mutated source:\ngot:  %#v\nwant: %#v", original, want)
	}
	first := original.Clone()
	second := original.Clone()
	first.Reads[0].Path = "first-only.md"
	first.AffectedRefs[0].Fragment = "first-only"
	if reflect.DeepEqual(first, second) {
		t.Fatal("repeated Clone() results unexpectedly share mutations")
	}
	if second.Reads[0].Path != original.Reads[0].Path ||
		second.AffectedRefs[0].Fragment != original.AffectedRefs[0].Fragment {
		t.Fatal("one repeated Clone() mutated another clone")
	}
}

func TestMigrationPlanProofClone_PreservesCanonicalDigest(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
		"a.md":     []byte("---\ntype: Knowledge\ntimestamp: 2026-01-01T00:00:00Z\n---\n"),
	}
	request := MigrationRequest{
		ID: "proof-clone-digest", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)
	if err != nil {
		t.Fatal(err)
	}
	change, err := migrationChangeSet(request, preview.Proof.BaseRevision)
	if err != nil {
		t.Fatal(err)
	}
	before, err := migrationPlanProofDigestContext(context.Background(), change, preview.Proof, preview.Resolution)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	cloned := preview.Proof.Clone()
	after, err := migrationPlanProofDigestContext(context.Background(), change, cloned, preview.Resolution)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("Clone() changed canonical digest: %q != %q", before, after)
	}
	if !equalMigrationPlanProof(preview.Proof, cloned) {
		t.Fatal("Clone() changed ordinary canonical proof value")
	}
}

func migrationProofCloneFixture(t *testing.T) MigrationPlanProof {
	t.Helper()
	return MigrationPlanProof{
		Reads:         []store.Read{{Path: "read.md"}},
		Writes:        []MigrationPlanWrite{{Path: "write.md", Digest: "digest"}},
		Deletes:       []string{"delete.md"},
		Renames:       []store.Rename{{From: "old.md", To: "new.md"}},
		AffectedRefs:  []bundle.RelationRef{ref(t, "affected#fragment")},
		ReverseImpact: []bundle.RelationRef{ref(t, "reverse#fragment")},
		ChangedFiles:  []store.FileChange{{Kind: store.FileWrite, Path: "write.md"}},
		ChangedRefs:   []bundle.RelationRef{ref(t, "changed#fragment")},
	}
}
