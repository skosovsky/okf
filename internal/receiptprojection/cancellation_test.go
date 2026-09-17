package receiptprojection

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestReadFileInventoryPreCanceledHighCardinalityDoesNotAllocateProjection(t *testing.T) {
	// Arrange.
	const fileCount = 4096
	source := make(projectionSource, fileCount)
	for index := range fileCount {
		source[fmt.Sprintf("assets/%04d.bin", index)] = nil
	}
	loaded := loadProjectionBundle(t, source)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var gotErr error

	// Act.
	allocations := testing.AllocsPerRun(20, func() {
		_, gotErr = readFileInventory(ctx, loaded)
	})

	// Assert.
	if !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("readFileInventory() error = %v, want context.Canceled", gotErr)
	}
	if allocations != 0 {
		t.Fatalf("readFileInventory() allocations = %.2f, want 0 before Files projection", allocations)
	}
}

func TestAddConceptRefsCancellationInterruptsFragmentInsertion(t *testing.T) {
	// Arrange.
	const fragmentCount = 1024
	id, err := bundle.ParseConceptID("source")
	if err != nil {
		t.Fatal(err)
	}
	fragments := make([]string, fragmentCount)
	for index := range fragments {
		fragments[index] = fmt.Sprintf("part-%04d", index)
	}
	concepts := map[string]semanticConcept{
		id.String(): {id: id, subresources: fragments},
	}
	seen := make(map[relationRefKey]bundle.RelationRef)
	ctx := newCancelAfterChecksContext(129)

	// Act.
	err = addConceptRefs(ctx, seen, concepts, id.String())

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("addConceptRefs() error = %v, want context.Canceled", err)
	}
	if len(seen) == 0 || len(seen) >= fragmentCount {
		t.Fatalf("addConceptRefs() inserted %d refs, want a bounded partial projection", len(seen))
	}
	if got := ctx.checks.Load(); got != 129 {
		t.Fatalf("context checks = %d, want deterministic cancellation at 129", got)
	}
}

func TestRelationInventoryCancellationInterruptsRelationInsertion(t *testing.T) {
	// Arrange.
	const relationCount = 1024
	sourceID, err := bundle.ParseConceptID("source")
	if err != nil {
		t.Fatal(err)
	}
	targetID, err := bundle.ParseConceptID("target")
	if err != nil {
		t.Fatal(err)
	}
	relations := make([]bundle.Relation, relationCount)
	for index := range relations {
		relations[index] = bundle.Relation{
			Source: bundle.RelationRef{ID: sourceID, Fragment: fmt.Sprintf("part-%04d", index)},
			Type:   "uses",
			Target: bundle.RelationRef{ID: targetID},
		}
	}
	concepts := map[string]semanticConcept{
		sourceID.String(): {id: sourceID, relations: relations},
	}
	ctx := newCancelAfterChecksContext(129)

	// Act.
	inventory, err := relationInventory(ctx, concepts, []string{sourceID.String()})

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("relationInventory() error = %v, want context.Canceled", err)
	}
	if inventory != nil {
		t.Fatalf("relationInventory() = %#v, want nil on cancellation", inventory)
	}
	if got := ctx.checks.Load(); got != 129 {
		t.Fatalf("context checks = %d, want deterministic cancellation at 129", got)
	}
}

func TestCanonicalRefsCancellationInterruptsMaterialization(t *testing.T) {
	// Arrange.
	const refCount = 1024
	seen := projectionRefs(t, refCount)
	ctx := newCancelAfterChecksContext(257)

	// Act.
	refs, err := canonicalRefs(ctx, seen)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canonicalRefs() error = %v, want context.Canceled", err)
	}
	if refs != nil {
		t.Fatalf("canonicalRefs() = %#v, want nil on cancellation", refs)
	}
	if got := ctx.checks.Load(); got != 257 {
		t.Fatalf("context checks = %d, want deterministic cancellation at 257", got)
	}
}

func TestCanonicalRefsCancellationInterruptsSort(t *testing.T) {
	// Arrange.
	const refCount = 512
	seen := projectionRefs(t, refCount)
	// One initial check and one check per map entry precede sorting. The
	// remaining budget deterministically expires during merge transfers.
	const cancelAt = refCount + 128
	ctx := newCancelAfterChecksContext(cancelAt)

	// Act.
	refs, err := canonicalRefs(ctx, seen)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canonicalRefs() error = %v, want context.Canceled", err)
	}
	if refs != nil {
		t.Fatalf("canonicalRefs() = %#v, want nil on cancellation", refs)
	}
	if got := ctx.checks.Load(); got != cancelAt {
		t.Fatalf("context checks = %d, want deterministic cancellation at %d", got, cancelAt)
	}
}

func projectionRefs(t *testing.T, count int) map[relationRefKey]bundle.RelationRef {
	t.Helper()
	refs := make(map[relationRefKey]bundle.RelationRef, count)
	for index := range count {
		id, err := bundle.ParseConceptID(fmt.Sprintf("concept-%04d", index))
		if err != nil {
			t.Fatal(err)
		}
		ref := bundle.RelationRef{ID: id}
		refs[relationRefKeyOf(ref)] = ref
	}
	return refs
}
