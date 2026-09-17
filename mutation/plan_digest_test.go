package mutation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestPlanDigest_BindsEveryAuthorizationFieldAndIsOrderStable(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
		"a.md":     []byte("---\ntype: Knowledge\ntimestamp: 2026-01-01T00:00:00Z\n---\n\nA.\n"),
	}
	request := MigrationRequest{
		ID:          "plan-digest-fields",
		Actor:       "operator",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	preview, err := NewMigrationPlanner().Preview(
		context.Background(),
		source,
		testMigrationResolution(t, source),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	change, err := migrationChangeSet(request, preview.Preview.BaseRevision)
	if err != nil {
		t.Fatal(err)
	}
	baseDigest, err := PlanDigest(change, preview.Preview)
	if err != nil {
		t.Fatal(err)
	}
	otherRevision := revisionFor(t, memorySource{"other.md": []byte("other")})
	changedRef := ref(t, "a#changed")

	changedRequest := change
	changedRequest.Actor = "different-operator"
	requestDigest, err := PlanDigest(changedRequest, preview.Preview)
	if err != nil {
		t.Fatal(err)
	}
	if requestDigest == baseDigest {
		t.Fatal("PlanDigest() did not bind the ChangeSet request")
	}

	mutations := map[string]func(*store.Preview){
		"base revision": func(value *store.Preview) { value.BaseRevision = otherRevision },
		"result revision": func(value *store.Preview) {
			value.ResultRevision = otherRevision
		},
		"read": func(value *store.Preview) {
			value.Reads = append(value.Reads, store.Read{Path: "extra.md"})
		},
		"write": func(value *store.Preview) {
			content := []byte("different")
			sum := sha256.Sum256(content)
			value.Writes = append(value.Writes, store.Write{
				Path: "extra.md", Digest: hex.EncodeToString(sum[:]), Content: content,
			})
		},
		"delete": func(value *store.Preview) {
			value.Deletes = append(value.Deletes, "deleted.md")
		},
		"rename": func(value *store.Preview) {
			value.Renames = append(value.Renames, store.Rename{From: "from.md", To: "to.md"})
		},
		"affected ref": func(value *store.Preview) {
			value.AffectedRefs = append(value.AffectedRefs, changedRef)
		},
		"reverse impact": func(value *store.Preview) {
			value.ReverseImpact = append(value.ReverseImpact, changedRef)
		},
	}

	// Act/Assert.
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := preview.Preview.Clone()
			mutate(&changed)
			got, digestErr := PlanDigest(change, changed)
			if name == "base revision" {
				if digestErr == nil {
					t.Fatal("PlanDigest() accepted preview/change base mismatch")
				}
				return
			}
			if digestErr != nil {
				t.Fatalf("PlanDigest() error = %v", digestErr)
			}
			if got == baseDigest {
				t.Fatalf("PlanDigest() = %q after %s mutation, want field sensitivity", got, name)
			}
		})
	}

	reordered := preview.Preview.Clone()
	reverseReads(reordered.Reads)
	reverseWrites(reordered.Writes)
	stable, err := PlanDigest(change, reordered)
	if err != nil || stable != baseDigest {
		t.Fatalf("reordered PlanDigest() = %q, %v; want %q", stable, err, baseDigest)
	}
}

func TestPlanDigestIdempotencyKey_IsFramedDomainSeparatedAndBounded(t *testing.T) {
	// Arrange.
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Act.
	base, baseErr := PlanDigestIdempotencyKey("migration", digest, "")
	repeat, repeatErr := PlanDigestIdempotencyKey("migration", digest, "")
	namespace, namespaceErr := PlanDigestIdempotencyKey("migration-extra", digest, "")
	caller, callerErr := PlanDigestIdempotencyKey("migration", digest, "extra")
	framedA, framedAErr := PlanDigestIdempotencyKey("a", digest, "bc")
	framedB, framedBErr := PlanDigestIdempotencyKey("ab", digest, "c")

	// Assert.
	for _, err := range []error{baseErr, repeatErr, namespaceErr, callerErr, framedAErr, framedBErr} {
		if err != nil {
			t.Fatalf("PlanDigestIdempotencyKey() error = %v", err)
		}
	}
	if base != repeat {
		t.Fatalf("keys are not deterministic: %q != %q", base, repeat)
	}
	if base == namespace || base == caller || framedA == framedB {
		t.Fatalf("keys lack domain/framing sensitivity: %q %q %q %q %q", base, namespace, caller, framedA, framedB)
	}
	if len(base) > 256 {
		t.Fatalf("derived key length = %d, want <= 256", len(base))
	}
}

func TestPlanDigestRelationRefs_UsesStructuralCanonicalOrder(t *testing.T) {
	// Arrange.
	escapedRoot := ref(t, `a\#x`)
	upper := ref(t, "aZ")
	fragment := ref(t, "a#x")
	first := []store.Ref{upper, fragment, escapedRoot}
	second := []store.Ref{escapedRoot, upper, fragment}
	base := revisionFor(t, memorySource{"a.md": []byte("base")})
	request := MigrationRequest{
		ID: "plan-digest-structural-refs", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	change, err := migrationChangeSet(request, base)
	if err != nil {
		t.Fatal(err)
	}
	firstPreview := store.Preview{
		BaseRevision: base, ResultRevision: base,
		AffectedRefs:  append([]store.Ref(nil), first...),
		ReverseImpact: append([]store.Ref(nil), second...),
	}
	secondPreview := store.Preview{
		BaseRevision: base, ResultRevision: base,
		AffectedRefs:  append([]store.Ref(nil), second...),
		ReverseImpact: append([]store.Ref(nil), first...),
	}

	// Act.
	firstWire, firstWireErr := canonicalPlanRelationRefsContext(context.Background(), first)
	secondWire, secondWireErr := canonicalPlanRelationRefsContext(context.Background(), second)
	firstDigest, firstErr := PlanDigest(change, firstPreview)
	secondDigest, secondErr := PlanDigest(change, secondPreview)

	// Assert.
	want := []string{fragment.String(), escapedRoot.String(), upper.String()}
	if firstWireErr != nil || secondWireErr != nil {
		t.Fatalf("canonicalPlanRelationRefsContext() errors = %v, %v", firstWireErr, secondWireErr)
	}
	if !slices.Equal(firstWire, want) || !slices.Equal(secondWire, want) {
		t.Fatalf("canonical refs = %#v / %#v, want structural wire projection %#v", firstWire, secondWire, want)
	}
	if firstErr != nil || secondErr != nil {
		t.Fatalf("PlanDigest() errors = %v, %v", firstErr, secondErr)
	}
	if firstDigest != secondDigest {
		t.Fatalf("permuted PlanDigest() = %q, %q", firstDigest, secondDigest)
	}
	if !equalMigrationRefs(first, []store.Ref{upper, fragment, escapedRoot}) {
		t.Fatalf("canonicalPlanRelationRefsContext() mutated caller refs: %#v", first)
	}
}

func reverseReads(values []store.Read) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseWrites(values []store.Write) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
