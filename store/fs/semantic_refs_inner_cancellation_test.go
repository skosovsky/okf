package fs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestSemanticRefsPreCanceledHighCardinalityDoesNotAllocateProjection(t *testing.T) {
	// Arrange.
	const fragmentCount = 4096
	var document strings.Builder
	document.WriteString("---\ntype: Note\nparts:\n")
	for index := range fragmentCount {
		fmt.Fprintf(&document, "  - id: part-%04d\n", index)
	}
	document.WriteString("---\nSource\n")
	snapshot, err := newSnapshot(context.Background(), map[string][]byte{
		"source.md": []byte(document.String()),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var gotErr error

	// Act.
	allocations := testing.AllocsPerRun(20, func() {
		_, gotErr = semanticRefs(ctx, snapshot)
	})

	// Assert.
	if !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("semanticRefs() error = %v, want context.Canceled", gotErr)
	}
	if allocations != 0 {
		t.Fatalf("semanticRefs() allocations = %.2f, want 0 for pre-canceled projection", allocations)
	}
}

func TestSemanticRefsCancellationInterruptsFragmentInsertion(t *testing.T) {
	// Arrange.
	const fragmentCount = 512
	var document strings.Builder
	document.WriteString("---\ntype: Note\nparts:\n")
	for index := range fragmentCount {
		fmt.Fprintf(&document, "  - id: part-%04d\n", index)
	}
	document.WriteString("---\nSource\n")
	snapshot, err := newSnapshot(context.Background(), map[string][]byte{
		"source.md": []byte(document.String()),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Bundle projection consumes roughly one check per fragment first. This
	// threshold deterministically lands in fs's second, insertion-side pass.
	ctx := newSemanticRefsCancelAfterContext(fragmentCount + 256)

	// Act.
	_, err = semanticRefs(ctx, snapshot)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("semanticRefs() error = %v, want context.Canceled", err)
	}
	if got := ctx.checks.Load(); got != fragmentCount+256 {
		t.Fatalf("context checks = %d, want deterministic cancellation at %d", got, fragmentCount+256)
	}
}

func TestSemanticRefsCancellationInterruptsRelationInsertion(t *testing.T) {
	// Arrange.
	const relationCount = 512
	var source strings.Builder
	source.WriteString("---\ntype: Note\nrelations:\n  uses:\n")
	for range relationCount {
		source.WriteString("    - target: target\n")
	}
	source.WriteString("---\nSource\n")
	snapshot, err := newSnapshot(context.Background(), map[string][]byte{
		"source.md": []byte(source.String()),
		"target.md": []byte(adversarialDocument("target")),
	})
	if err != nil {
		t.Fatal(err)
	}
	// SemanticLinksFromContext consumes roughly one check per relation first.
	// This threshold lands in fs's per-source/target insertion pass.
	ctx := newSemanticRefsCancelAfterContext(relationCount + 256)

	// Act.
	_, err = semanticRefs(ctx, snapshot)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("semanticRefs() error = %v, want context.Canceled", err)
	}
	if got := ctx.checks.Load(); got != relationCount+256 {
		t.Fatalf("context checks = %d, want deterministic cancellation at %d", got, relationCount+256)
	}
}

func TestCanonicalSemanticRefsCancellationInterruptsMaterialization(t *testing.T) {
	// Arrange.
	const refCount = 1024
	seen := semanticProjectionRefs(t, refCount)
	ctx := newSemanticRefsCancelAfterContext(257)

	// Act.
	refs, err := canonicalSemanticRefs(ctx, seen)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canonicalSemanticRefs() error = %v, want context.Canceled", err)
	}
	if refs != nil {
		t.Fatalf("canonicalSemanticRefs() = %#v, want nil on cancellation", refs)
	}
	if got := ctx.checks.Load(); got != 257 {
		t.Fatalf("context checks = %d, want deterministic cancellation at 257", got)
	}
}

func TestCanonicalSemanticRefsCancellationInterruptsSort(t *testing.T) {
	// Arrange.
	const refCount = 512
	seen := semanticProjectionRefs(t, refCount)
	// One initial check and one check per map entry precede sorting. The
	// remaining budget deterministically expires during merge transfers.
	const cancelAt = refCount + 128
	ctx := newSemanticRefsCancelAfterContext(cancelAt)

	// Act.
	refs, err := canonicalSemanticRefs(ctx, seen)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canonicalSemanticRefs() error = %v, want context.Canceled", err)
	}
	if refs != nil {
		t.Fatalf("canonicalSemanticRefs() = %#v, want nil on cancellation", refs)
	}
	if got := ctx.checks.Load(); got != cancelAt {
		t.Fatalf("context checks = %d, want deterministic cancellation at %d", got, cancelAt)
	}
}

func semanticProjectionRefs(t *testing.T, count int) map[relationRefKey]bundle.RelationRef {
	t.Helper()
	refs := make(map[relationRefKey]bundle.RelationRef, count)
	for index := range count {
		ref := adversarialRef(t, fmt.Sprintf("concept-%04d", index))
		refs[relationRefKeyOf(ref)] = ref
	}
	return refs
}
