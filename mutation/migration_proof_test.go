package mutation

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestMigrationPlanProof_IsCanonicalDeepCopiedAndPartitionFramed(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md":  []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
		"a.md":      []byte("---\ntype: Knowledge\ntimestamp: 2026-01-01T00:00:00Z\n---\n"),
		"asset.bin": []byte("inert"),
	}
	request := MigrationRequest{
		ID: "proof-canonical", Actor: "operator",
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

	// Act.
	clone := preview.Proof.Clone()
	movedPartition := preview.Proof.Clone()
	movedPartition.ReverseImpact = append([]store.Ref{}, movedPartition.AffectedRefs...)
	movedPartition.AffectedRefs = []store.Ref{}
	movedDigest, movedErr := migrationPlanProofDigestContext(context.Background(), change, movedPartition, preview.Resolution)
	originalDigest, originalErr := migrationPlanProofDigestContext(context.Background(), change, preview.Proof, preview.Resolution)
	escapedRoot := ref(t, `a\#x`)
	upper := ref(t, "aZ")
	structural := preview.Proof.Clone()
	structural.AffectedRefs = []store.Ref{upper, escapedRoot}
	structural.ReverseImpact = []store.Ref{upper, escapedRoot}
	structural.ChangedRefs = []store.Ref{upper, escapedRoot}
	canonicalizeMigrationPlanProof(&structural)
	structuralDigest, structuralErr := migrationPlanProofDigestContext(context.Background(), change, structural, preview.Resolution)
	structuralShuffled := structural.Clone()
	slices.Reverse(structuralShuffled.AffectedRefs)
	slices.Reverse(structuralShuffled.ReverseImpact)
	slices.Reverse(structuralShuffled.ChangedRefs)
	canonicalizeMigrationPlanProof(&structuralShuffled)
	structuralShuffledDigest, structuralShuffledErr := migrationPlanProofDigestContext(context.Background(), change, structuralShuffled, preview.Resolution)
	shuffled := preview.Proof.Clone()
	slices.Reverse(shuffled.Reads)
	slices.Reverse(shuffled.Writes)
	slices.Reverse(shuffled.AffectedRefs)
	slices.Reverse(shuffled.ReverseImpact)
	slices.Reverse(shuffled.ChangedFiles)
	slices.Reverse(shuffled.ChangedRefs)
	canonicalizeMigrationPlanProof(&shuffled)
	shuffledDigest, shuffledErr := migrationPlanProofDigestContext(context.Background(), change, shuffled, preview.Resolution)
	clone.Writes[0].Path = "mutated.md"

	// Assert.
	if originalErr != nil ||
		movedErr != nil ||
		shuffledErr != nil ||
		structuralErr != nil ||
		structuralShuffledErr != nil {
		t.Fatalf(
			"proof digest errors = %v, %v, %v, %v, %v",
			originalErr,
			movedErr,
			shuffledErr,
			structuralErr,
			structuralShuffledErr,
		)
	}
	if originalDigest == movedDigest {
		t.Fatal("moving refs across adjacent proof partitions did not change digest")
	}
	if shuffledDigest != originalDigest || !equalMigrationPlanProof(shuffled, preview.Proof) {
		t.Fatal("canonical proof or digest depends on input order")
	}
	if structuralDigest != structuralShuffledDigest ||
		!equalMigrationPlanProof(structural, structuralShuffled) {
		t.Fatal("escaped ConceptID proof or digest depends on relation ref input order")
	}
	if preview.Proof.Writes[0].Path == clone.Writes[0].Path {
		t.Fatal("MigrationPlanProof.Clone() retained write slice")
	}
	for name, mutate := range map[string]func(*MigrationPlanProof){
		"null reads":         func(value *MigrationPlanProof) { value.Reads = nil },
		"null writes":        func(value *MigrationPlanProof) { value.Writes = nil },
		"null deletes":       func(value *MigrationPlanProof) { value.Deletes = nil },
		"null renames":       func(value *MigrationPlanProof) { value.Renames = nil },
		"null affected":      func(value *MigrationPlanProof) { value.AffectedRefs = nil },
		"null reverse":       func(value *MigrationPlanProof) { value.ReverseImpact = nil },
		"null changed files": func(value *MigrationPlanProof) { value.ChangedFiles = nil },
		"null changed refs":  func(value *MigrationPlanProof) { value.ChangedRefs = nil },
		"resolution digest": func(value *MigrationPlanProof) {
			value.ResolutionDigest = strings.Repeat("0", len(value.ResolutionDigest))
		},
		"wrong version": func(value *MigrationPlanProof) { value.FormatVersion++ },
	} {
		t.Run(name, func(t *testing.T) {
			hostile := preview.Proof.Clone()
			mutate(&hostile)
			if err := validateMigrationPlanProofContext(context.Background(), change, hostile, preview.Resolution); !errors.Is(err, ErrMigrationPlanMismatch) {
				t.Fatalf("validateMigrationPlanProof() error = %v", err)
			}
		})
	}
	for name, mutate := range map[string]func(*MigrationPlanProof){
		"existing non-markdown write": func(value *MigrationPlanProof) {
			value.Writes[0].Path = "asset.bin"
			value.ChangedFiles[0].Path = "asset.bin"
		},
		"unrequested created write": func(value *MigrationPlanProof) {
			last := len(value.Writes) - 1
			value.Writes[last].Path = "z.md"
			value.ChangedFiles[last].Path = "z.md"
		},
		"delete": func(value *MigrationPlanProof) {
			value.Deletes = append(value.Deletes, "z.md")
		},
		"receipt file mismatch": func(value *MigrationPlanProof) {
			value.ChangedFiles[0].Path = "b.md"
		},
	} {
		t.Run("scope/"+name, func(t *testing.T) {
			hostile := preview.Proof.Clone()
			mutate(&hostile)
			if err := validateMigrationPlanProofContext(context.Background(), change, hostile, preview.Resolution); !errors.Is(err, ErrMigrationPlanMismatch) {
				t.Fatalf("validateMigrationPlanProof() error = %v", err)
			}
		})
	}
}

func TestMigrationProofTupleOrdering_IsDelimiterSafeAndCanonical(t *testing.T) {
	renames := []store.Rename{
		{From: "a\x00b", To: "c"},
		{From: "a", To: "b\x00c"},
	}
	files := []store.FileChange{
		{Path: "a\x00b", Kind: store.FileWrite, From: "c"},
		{Path: "a", Kind: store.FileWrite, From: "b\x00c"},
	}

	// Act.
	renameOrder := compareRename(renames[0], renames[1])
	fileOrder := compareFileChange(files[0], files[1])
	slices.SortFunc(renames, compareRename)
	slices.SortFunc(files, compareFileChange)

	// Assert.
	if renameOrder == 0 || fileOrder == 0 {
		t.Fatal("escaped-NUL tuples collapsed to the same identity")
	}
	if renames[0].From != "a" || files[0].Path != "a" {
		t.Fatalf("canonical tuple order = %#v, %#v", renames, files)
	}
}

func TestMigrationProofRelationRefs_UseStructuralIdentityAndOrder(t *testing.T) {
	// Arrange. Wire order differs because the root ConceptID '#' is escaped as
	// a backslash, while structural order compares the unescaped ConceptID.
	escapedRoot := ref(t, `a\#x`)
	upper := ref(t, "aZ")
	fragment := ref(t, "a#x")
	values := []store.Ref{upper, fragment, escapedRoot}
	proof := MigrationPlanProof{
		AffectedRefs:  append([]store.Ref(nil), values...),
		ReverseImpact: append([]store.Ref(nil), values...),
		ChangedRefs:   append([]store.Ref(nil), values...),
	}

	// Act.
	canonicalizeMigrationPlanProof(&proof)

	// Assert.
	want := []store.Ref{fragment, escapedRoot, upper}
	for name, got := range map[string][]store.Ref{
		"affected": proof.AffectedRefs,
		"reverse":  proof.ReverseImpact,
		"changed":  proof.ChangedRefs,
	} {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s refs = %#v, want structural order %#v", name, got, want)
		}
		if err := validateProofRefsContext(context.Background(), got); err != nil {
			t.Fatalf("validateProofRefs(%s) error = %v", name, err)
		}
	}
	if escapedRoot.String() < upper.String() {
		t.Fatalf("adversarial wire order unexpectedly matches structural order: %q < %q", escapedRoot, upper)
	}
	if !equalMigrationRefs([]store.Ref{escapedRoot, upper}, []store.Ref{upper, escapedRoot}) {
		t.Fatal("receipt/proof set equality rejected distinct canonical v1/v2 orders")
	}
	if equalMigrationRefs([]store.Ref{escapedRoot, escapedRoot}, []store.Ref{escapedRoot, upper}) ||
		equalMigrationRefs([]store.Ref{escapedRoot, upper}, []store.Ref{upper, upper}) {
		t.Fatal("receipt/proof set equality accepted duplicate or missing structural refs")
	}

	tampered := append([]store.Ref(nil), want...)
	tampered[0], tampered[2] = tampered[2], tampered[0]
	if err := validateProofRefsContext(context.Background(), tampered); !errors.Is(err, ErrMigrationPlanMismatch) {
		t.Fatalf("tampered validateProofRefs() error = %v", err)
	}
	duplicate := append(append([]store.Ref(nil), want...), upper)
	if err := validateProofRefsContext(context.Background(), duplicate); !errors.Is(err, ErrMigrationPlanMismatch) {
		t.Fatalf("duplicate validateProofRefs() error = %v", err)
	}
	if err := validateProofRefsContext(context.Background(), []store.Ref{{}}); !errors.Is(err, ErrMigrationPlanMismatch) {
		t.Fatalf("invalid validateProofRefs() error = %v", err)
	}
}

func TestMigrationPlanProofRelationRefs_PermutationDigestAndTamper(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
		"a.md":     []byte("---\ntype: Knowledge\ntimestamp: 2026-01-01T00:00:00Z\n---\n"),
	}
	request := MigrationRequest{
		ID: "proof-structural-refs", Actor: "operator",
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
	escapedRoot := ref(t, `a\#x`)
	fragment := ref(t, "a#x")
	upper := ref(t, "aZ")
	want := []store.Ref{fragment, escapedRoot, upper}
	left := preview.Proof.Clone()
	left.AffectedRefs = []store.Ref{upper, escapedRoot, fragment}
	left.ReverseImpact = []store.Ref{fragment, upper, escapedRoot}
	left.ChangedRefs = []store.Ref{upper, fragment, escapedRoot}
	right := preview.Proof.Clone()
	right.AffectedRefs = []store.Ref{fragment, upper, escapedRoot}
	right.ReverseImpact = []store.Ref{escapedRoot, fragment, upper}
	right.ChangedRefs = []store.Ref{fragment, escapedRoot, upper}

	// Act.
	canonicalizeMigrationPlanProof(&left)
	canonicalizeMigrationPlanProof(&right)
	leftDigest, leftErr := migrationPlanProofDigestContext(context.Background(), change, left, preview.Resolution)
	rightDigest, rightErr := migrationPlanProofDigestContext(context.Background(), change, right, preview.Resolution)
	tampered := left.Clone()
	tampered.AffectedRefs[0], tampered.AffectedRefs[2] =
		tampered.AffectedRefs[2], tampered.AffectedRefs[0]
	_, tamperErr := migrationPlanProofDigestContext(context.Background(), change, tampered, preview.Resolution)

	// Assert.
	if leftErr != nil || rightErr != nil {
		t.Fatalf("migrationPlanProofDigest() errors = %v, %v", leftErr, rightErr)
	}
	if leftDigest != rightDigest {
		t.Fatalf("permuted proof digests = %q, %q", leftDigest, rightDigest)
	}
	for name, got := range map[string][]store.Ref{
		"left affected":  left.AffectedRefs,
		"left reverse":   left.ReverseImpact,
		"left changed":   left.ChangedRefs,
		"right affected": right.AffectedRefs,
		"right reverse":  right.ReverseImpact,
		"right changed":  right.ChangedRefs,
	} {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %#v, want %#v", name, got, want)
		}
	}
	if !errors.Is(tamperErr, ErrMigrationPlanMismatch) {
		t.Fatalf("tampered migrationPlanProofDigest() error = %v", tamperErr)
	}
	if !equalMigrationRefs(left.ChangedRefs, tampered.AffectedRefs) {
		t.Fatal("receipt/proof set equality rejected a structurally identical ref set")
	}
}

func TestMigrationPlanProof_CanonicalOrderAndDigestIgnoreInputOrder(t *testing.T) {
	left := MigrationPlanProof{
		FormatVersion: MigrationPlanProofFormatVersion,
		Renames: []store.Rename{
			{From: "z", To: "a"},
			{From: "a", To: "z"},
		},
		ChangedFiles: []store.FileChange{
			{Path: "z", Kind: store.FileWrite},
			{Path: "a", Kind: store.FileDelete},
		},
	}
	right := left.Clone()
	slices.Reverse(right.Renames)
	slices.Reverse(right.ChangedFiles)

	// Act.
	canonicalizeMigrationPlanProof(&left)
	canonicalizeMigrationPlanProof(&right)
	leftDigest := digestMigrationProofProjection(left)
	rightDigest := digestMigrationProofProjection(right)

	// Assert.
	if !slices.Equal(left.Renames, right.Renames) ||
		!slices.Equal(left.ChangedFiles, right.ChangedFiles) {
		t.Fatalf("canonical tuple projections differ:\nleft=%#v\nright=%#v", left, right)
	}
	if leftDigest != rightDigest {
		t.Fatalf("canonical proof digests differ: %q != %q", leftDigest, rightDigest)
	}
}

func digestMigrationProofProjection(proof MigrationPlanProof) string {
	e := planDigestEncoder{}
	for _, rename := range proof.Renames {
		e.string(rename.From)
		e.string(rename.To)
	}
	for _, file := range proof.ChangedFiles {
		e.string(file.Path)
		e.string(string(file.Kind))
		e.string(file.From)
	}
	return digestPlanEncoder(e)
}
