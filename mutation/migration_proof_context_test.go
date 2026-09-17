package mutation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestCanonicalizeMigrationPlanProofContext_AllCollectionsAreBoundedAndOwned(t *testing.T) {
	longA := strings.Repeat("a", 1<<20)
	longB := strings.Repeat("b", 1<<20)
	id, err := bundle.ParseConceptID("proof")
	if err != nil {
		t.Fatal(err)
	}
	refs := []bundle.RelationRef{{ID: id, Fragment: longB}, {ID: id, Fragment: longA}}
	tests := []struct {
		name  string
		proof MigrationPlanProof
	}{
		{name: "Reads", proof: MigrationPlanProof{Reads: []store.Read{{Path: longB}, {Path: longA}}}},
		{name: "Writes", proof: MigrationPlanProof{Writes: []MigrationPlanWrite{{Path: longB, Digest: longB}, {Path: longA, Digest: longA}}}},
		{name: "Deletes", proof: MigrationPlanProof{Deletes: []string{longB, longA}}},
		{name: "Renames", proof: MigrationPlanProof{Renames: []store.Rename{{From: longB, To: longA}, {From: longA, To: longB}}}},
		{name: "AffectedRefs", proof: MigrationPlanProof{AffectedRefs: refs}},
		{name: "ReverseImpact", proof: MigrationPlanProof{ReverseImpact: refs}},
		{name: "ChangedFiles", proof: MigrationPlanProof{ChangedFiles: []store.FileChange{{Path: longB, Kind: store.FileWrite}, {Path: longA, Kind: store.FileWrite}}}},
		{name: "ChangedRefs", proof: MigrationPlanProof{ChangedRefs: refs}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			before := test.proof.Clone()
			want := test.proof.Clone()
			canonicalizeMigrationPlanProof(&want)
			ctx := &migrationResolutionCancelAtFunctionContext{
				Context:  context.Background(),
				function: "compareStringsContext",
			}

			// Act.
			cancelled, cancelErr := canonicalizeMigrationPlanProofContext(ctx, test.proof)
			got, gotErr := canonicalizeMigrationPlanProofContext(context.Background(), test.proof)

			// Assert.
			if cancelErr != context.Canceled || !reflect.DeepEqual(cancelled, MigrationPlanProof{}) {
				t.Fatalf("cancelled canonical proof = %#v, %v", cancelled, cancelErr)
			}
			if gotErr != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("canonical proof = %#v, %v; want %#v", got, gotErr, want)
			}
			if !reflect.DeepEqual(test.proof, before) {
				t.Fatal("canonicalization mutated caller proof")
			}
		})
	}
}

func TestMigrationProofValidationComparatorsKeepCallerContext(t *testing.T) {
	id, err := bundle.ParseConceptID("proof")
	if err != nil {
		t.Fatal(err)
	}
	refs := []bundle.RelationRef{{ID: id, Fragment: "a"}, {ID: id, Fragment: "b"}}
	tests := []struct {
		name     string
		validate func(context.Context) error
	}{
		{name: "Reads", validate: func(ctx context.Context) error {
			return validateProofReadsContext(ctx, []store.Read{{Path: "a.md"}, {Path: "b.md"}})
		}},
		{name: "Writes", validate: func(ctx context.Context) error {
			return validateProofWritesContext(ctx, []MigrationPlanWrite{{Path: "a.md", Digest: strings.Repeat("a", 64)}, {Path: "b.md", Digest: strings.Repeat("b", 64)}})
		}},
		{name: "Deletes", validate: func(ctx context.Context) error { return validateProofPathsContext(ctx, []string{"a.md", "b.md"}) }},
		{name: "Renames", validate: func(ctx context.Context) error {
			return validateProofRenamesContext(ctx, []store.Rename{{From: "a.md", To: "b.md"}, {From: "b.md", To: "c.md"}})
		}},
		{name: "AffectedRefs", validate: func(ctx context.Context) error { return validateProofRefsContext(ctx, refs) }},
		{name: "ReverseImpact", validate: func(ctx context.Context) error { return validateProofRefsContext(ctx, refs) }},
		{name: "ChangedFiles", validate: func(ctx context.Context) error {
			return validateProofFileChangesContext(ctx, []store.FileChange{{Path: "a.md", Kind: store.FileWrite}, {Path: "b.md", Kind: store.FileWrite}})
		}},
		{name: "ChangedRefs", validate: func(ctx context.Context) error { return validateProofRefsContext(ctx, refs) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			ctx := &migrationResolutionCancelAtFunctionContext{
				Context:  context.Background(),
				function: "compareStringsContext",
			}

			// Act.
			gotErr := test.validate(ctx)

			// Assert.
			if gotErr != context.Canceled {
				t.Fatalf("validation error = %v, want context.Canceled", gotErr)
			}
		})
	}
}

func TestValidateProofWritesContext_RejectsDuplicatePathWithDistinctDigests(t *testing.T) {
	// Arrange.
	writes := []MigrationPlanWrite{
		{Path: "a.md", Digest: strings.Repeat("a", 64)},
		{Path: "a.md", Digest: strings.Repeat("b", 64)},
	}

	// Act.
	err := validateProofWritesContext(context.Background(), writes)

	// Assert.
	if !errors.Is(err, ErrMigrationPlanMismatch) {
		t.Fatalf("validateProofWritesContext() error = %v", err)
	}
}

func TestEqualMigrationPlanProofContext_LongFieldsAreBounded(t *testing.T) {
	longValue := strings.Repeat("a", 2<<20)
	id, err := bundle.ParseConceptID("proof")
	if err != nil {
		t.Fatal(err)
	}
	ref := bundle.RelationRef{ID: id, Fragment: longValue}
	tests := []struct {
		name     string
		proof    MigrationPlanProof
		function string
	}{
		{name: "Reads", proof: MigrationPlanProof{Reads: []store.Read{{Path: longValue}}}, function: "compareMigrationReadContext"},
		{name: "Writes", proof: MigrationPlanProof{Writes: []MigrationPlanWrite{{Path: longValue, Digest: longValue}}}, function: "compareMigrationPlanWriteContext"},
		{name: "Deletes", proof: MigrationPlanProof{Deletes: []string{longValue}}, function: "compareMigrationDeleteContext"},
		{name: "Renames", proof: MigrationPlanProof{Renames: []store.Rename{{From: longValue, To: longValue}}}, function: "compareMigrationRenameContext"},
		{name: "AffectedRefs", proof: MigrationPlanProof{AffectedRefs: []bundle.RelationRef{ref}}, function: "compareMigrationRelationRefContext"},
		{name: "ReverseImpact", proof: MigrationPlanProof{ReverseImpact: []bundle.RelationRef{ref}}, function: "compareMigrationRelationRefContext"},
		{name: "ChangedFiles", proof: MigrationPlanProof{ChangedFiles: []store.FileChange{{Path: longValue, Kind: store.FileWrite}}}, function: "compareMigrationFileChangeContext"},
		{name: "ChangedRefs", proof: MigrationPlanProof{ChangedRefs: []bundle.RelationRef{ref}}, function: "compareMigrationRelationRefContext"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			ctx := &migrationResolutionCancelAtFunctionContext{
				Context:  context.Background(),
				function: test.function,
			}

			// Act.
			equal, err := equalMigrationPlanProofContext(ctx, test.proof, test.proof.Clone())

			// Assert.
			if err != context.Canceled || equal {
				t.Fatalf("equalMigrationPlanProofContext() = %t, %v", equal, err)
			}
		})
	}
}

func TestEqualMigrationPlanProofContext_BindsEveryFieldAndCollection(t *testing.T) {
	// Arrange.
	id, err := bundle.ParseConceptID("proof")
	if err != nil {
		t.Fatal(err)
	}
	base := MigrationPlanProof{
		FormatVersion:    MigrationPlanProofFormatVersion,
		RequestDigest:    "request",
		ResolutionDigest: "resolution",
		BaseRevision:     "base",
		ResultRevision:   "result",
		Reads:            []store.Read{{Path: "a.md"}},
		Writes:           []MigrationPlanWrite{{Path: "a.md", Digest: "digest"}},
		Deletes:          []string{"b.md"},
		Renames:          []store.Rename{{From: "c.md", To: "d.md"}},
		AffectedRefs:     []bundle.RelationRef{{ID: id, Fragment: "affected"}},
		ReverseImpact:    []bundle.RelationRef{{ID: id, Fragment: "reverse"}},
		ChangedFiles:     []store.FileChange{{Path: "a.md", Kind: store.FileWrite}},
		ChangedRefs:      []bundle.RelationRef{{ID: id, Fragment: "changed"}},
	}
	mutations := []struct {
		name   string
		mutate func(*MigrationPlanProof)
	}{
		{name: "FormatVersion", mutate: func(value *MigrationPlanProof) { value.FormatVersion++ }},
		{name: "RequestDigest", mutate: func(value *MigrationPlanProof) { value.RequestDigest += "x" }},
		{name: "ResolutionDigest", mutate: func(value *MigrationPlanProof) { value.ResolutionDigest += "x" }},
		{name: "BaseRevision", mutate: func(value *MigrationPlanProof) { value.BaseRevision += "x" }},
		{name: "ResultRevision", mutate: func(value *MigrationPlanProof) { value.ResultRevision += "x" }},
		{name: "Reads", mutate: func(value *MigrationPlanProof) { value.Reads[0].Path += "x" }},
		{name: "Writes", mutate: func(value *MigrationPlanProof) { value.Writes[0].Digest += "x" }},
		{name: "Deletes", mutate: func(value *MigrationPlanProof) { value.Deletes[0] += "x" }},
		{name: "Renames", mutate: func(value *MigrationPlanProof) { value.Renames[0].To += "x" }},
		{name: "AffectedRefs", mutate: func(value *MigrationPlanProof) { value.AffectedRefs[0].Fragment += "x" }},
		{name: "ReverseImpact", mutate: func(value *MigrationPlanProof) { value.ReverseImpact[0].Fragment += "x" }},
		{name: "ChangedFiles", mutate: func(value *MigrationPlanProof) { value.ChangedFiles[0].Kind = store.FileDelete }},
		{name: "ChangedRefs", mutate: func(value *MigrationPlanProof) { value.ChangedRefs[0].Fragment += "x" }},
	}

	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			// Arrange.
			changed := base.Clone()
			mutation.mutate(&changed)

			// Act.
			equal, err := equalMigrationPlanProofContext(context.Background(), base, changed)

			// Assert.
			if err != nil || equal {
				t.Fatalf("equalMigrationPlanProofContext() = %t, %v", equal, err)
			}
		})
	}
}

func TestMigrationPlanProofDigestContext_ValidationCancellationReturnsZero(t *testing.T) {
	// Arrange.
	fixture := newMigrationCommitOutcomeFixture(t)
	change, err := migrationChangeSet(fixture.apply.Request, fixture.apply.Proof.BaseRevision)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &migrationResolutionCancelAtFunctionContext{
		Context:  context.Background(),
		function: "compareMigrationReadContext",
	}

	// Act.
	digest, err := migrationPlanProofDigestContext(ctx, change, fixture.apply.Proof, fixture.apply.Resolution)

	// Assert.
	if err != context.Canceled || digest != "" {
		t.Fatalf("migrationPlanProofDigestContext() = %q, %v", digest, err)
	}
}

func TestMigrationPlannerApply_FreshProofEqualityCancellationIsPrecommit(t *testing.T) {
	// Arrange.
	fixture := newMigrationCommitOutcomeFixture(t)
	destination := &scriptedMigrationStore{snapshot: fixture.snapshot}
	destination.commit = func(context.Context, store.ChangeSet, store.CommitOptions) (store.CommitReceipt, error) {
		t.Fatal("Commit called after proof equality cancellation")
		return store.CommitReceipt{}, nil
	}
	ctx := &migrationResolutionCancelAtFunctionContext{
		Context:  context.Background(),
		function: "equalMigrationPlanProofContext",
	}

	// Act.
	receipt, err := fixture.planner.Apply(ctx, destination, fixture.apply)

	// Assert.
	if err != context.Canceled || !reflect.DeepEqual(receipt, store.CommitReceipt{}) ||
		destination.snapshotCalls != 1 || destination.commitCalls != 0 {
		t.Fatalf("Apply() = (%#v, %v); calls snapshot=%d commit=%d",
			receipt, err, destination.snapshotCalls, destination.commitCalls)
	}
}

func TestEqualMigrationPlanProofContext_PreservesNilShapeAndFieldIdentity(t *testing.T) {
	// Arrange.
	base := MigrationPlanProof{Reads: []store.Read{}, Writes: []MigrationPlanWrite{}}
	nilShape := base
	nilShape.Reads = nil
	changed := base.Clone()
	changed.Writes = append(changed.Writes, MigrationPlanWrite{Path: "a.md", Digest: strings.Repeat("a", 64)})

	// Act.
	equalBase, baseErr := equalMigrationPlanProofContext(context.Background(), base, base.Clone())
	equalNil, nilErr := equalMigrationPlanProofContext(context.Background(), base, nilShape)
	equalChanged, changedErr := equalMigrationPlanProofContext(context.Background(), base, changed)

	// Assert.
	if baseErr != nil || nilErr != nil || changedErr != nil || !equalBase || equalNil || equalChanged {
		t.Fatalf("equality results = base:%t/%v nil:%t/%v changed:%t/%v",
			equalBase, baseErr, equalNil, nilErr, equalChanged, changedErr)
	}
}
