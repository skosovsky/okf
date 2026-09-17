package mutation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

var (
	semanticRefsAllocationSink []bundle.RelationRef
	conceptsAllocationSink     []bundle.Concept
)

func TestAvailableSemanticRefsUsesDeterministicDefensiveProjection(t *testing.T) {
	// Arrange.
	loaded := loadSemanticRefsBundle(t, memorySource{
		"a.md": []byte("---\ntype: thing\nparts:\n  - id: z\n  - id: alpha\nrelations:\n  uses:\n    - target: b#beta\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\nparts:\n  - id: beta\n---\nB\n"),
	})
	want := []string{"a", "a#alpha", "a#z", "b", "b#beta"}

	// Act.
	first, err := availableSemanticRefsFromBundle(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	got := semanticRefStrings(first)
	first[0] = bundle.RelationRef{}
	first = append(first, bundle.RelationRef{})
	second, secondErr := availableSemanticRefsFromBundle(context.Background(), loaded)

	// Assert.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("availableSemanticRefsFromBundle() = %#v, want %#v", got, want)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	if gotSecond := semanticRefStrings(second); !reflect.DeepEqual(gotSecond, want) {
		t.Fatalf("projection retained caller mutation or changed order: %#v, want %#v", gotSecond, want)
	}
}

func TestRelationRefStructuralIdentitySeparatesHashConceptFromFragment(t *testing.T) {
	// Arrange. These refs identify a hash-bearing concept root and a fragment
	// respectively. Their identity must remain structural regardless of the
	// public wire grammar.
	hashConcept, fragmentRef := plannerHashCollisionRefs(t)
	inputs := [][]bundle.RelationRef{
		{hashConcept, fragmentRef, hashConcept, fragmentRef},
		{fragmentRef, hashConcept, fragmentRef, hashConcept},
	}
	want := []bundle.RelationRef{fragmentRef, hashConcept}

	for index, input := range inputs {
		// Act.
		got, err := refsForContext(context.Background(), input)

		// Assert.
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %d refsForContext() = %#v, want %#v", index, got, want)
		}
	}
	if relationRefIdentity(hashConcept) == relationRefIdentity(fragmentRef) {
		t.Fatal("structural identities collided")
	}
}

func TestAvailableSemanticRefsPreservesHashConceptAndFragmentNamespace(t *testing.T) {
	// Arrange.
	hashConcept, fragmentRef := plannerHashCollisionRefs(t)
	loaded := loadSemanticRefsBundle(t, memorySource{
		"source#part.md": []byte("---\ntype: thing\n---\nHash concept\n"),
		"source.md":      []byte("---\ntype: thing\nparts:\n  - id: part\n---\nSource\n"),
	})
	sourceRoot := ref(t, "source")
	want := []bundle.RelationRef{sourceRoot, fragmentRef, hashConcept}

	// Act.
	first, err := availableSemanticRefsFromBundle(context.Background(), loaded)
	second, secondErr := availableSemanticRefsFromBundle(context.Background(), loaded)

	// Assert.
	if err != nil || secondErr != nil {
		t.Fatalf("availableSemanticRefsFromBundle() errors = %v / %v", err, secondErr)
	}
	if !reflect.DeepEqual(first, want) || !reflect.DeepEqual(second, want) {
		t.Fatalf("semantic namespace = %#v / %#v, want %#v", first, second, want)
	}
}

func TestPlanConflictChangedRefsUsesStructuralRelationIdentity(t *testing.T) {
	// Arrange.
	hashConcept, fragmentRef := plannerHashCollisionRefs(t)
	source := memorySource{
		"source#part.md": []byte("---\ntype: thing\n---\nHash concept\n"),
		"source.md":      []byte("---\ntype: thing\nparts:\n  - id: part\n---\nSource\n"),
	}
	changeSet := store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           "structural-conflict-refs",
		Actor:        "tester",
		BaseRevision: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Operations:   []store.Operation{store.MoveConcept{From: ref(t, "source").ID, To: ref(t, "moved/source").ID}},
	}

	// Act.
	result, err := Plan(context.Background(), source, changeSet)

	// Assert.
	var conflict *store.Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("Plan() error = %v, want Conflict", err)
	}
	if result.Staged != nil {
		t.Fatalf("conflict exposed staged result: %#v", result)
	}
	seen := make(map[relationRefKey]struct{}, len(conflict.ChangedRefs))
	for _, relationRef := range conflict.ChangedRefs {
		seen[relationRefIdentity(relationRef)] = struct{}{}
	}
	for _, want := range []bundle.RelationRef{hashConcept, fragmentRef} {
		if _, ok := seen[relationRefIdentity(want)]; !ok {
			t.Fatalf("ChangedRefs lost structural ref %#v: %#v", want, conflict.ChangedRefs)
		}
	}
	if got, want := len(conflict.ChangedRefs), 3; got != want {
		t.Fatalf("ChangedRefs count = %d, want %d: %#v", got, want, conflict.ChangedRefs)
	}
}

func TestAvailableSemanticRefsPropagatesCancellationAndObservationErrors(t *testing.T) {
	// Arrange.
	loaded := loadSemanticRefsBundle(t, memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
		"c.md": []byte("---\ntype: thing\n---\nC\n"),
	})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	injected := errors.New("injected semantic projection failure")

	// Act.
	beforeRefs, beforeErr := availableSemanticRefsFromBundle(canceled, loaded)
	duringRefs, duringErr := availableSemanticRefsFromBundle(
		&semanticRefsErrorContext{Context: context.Background(), allowed: 2, err: injected},
		loaded,
	)
	traversalRefs, traversalErr := availableSemanticRefsFromBundle(
		&countdownContext{Context: context.Background(), remaining: 9},
		loaded,
	)

	// Assert.
	if beforeRefs != nil || !errors.Is(beforeErr, context.Canceled) {
		t.Fatalf("pre-canceled projection = (%#v, %v), want (nil, context.Canceled)", beforeRefs, beforeErr)
	}
	if duringRefs != nil || duringErr != injected {
		t.Fatalf("observation API failure = (%#v, %v), want exact injected error", duringRefs, duringErr)
	}
	if traversalRefs != nil || !errors.Is(traversalErr, context.Canceled) {
		t.Fatalf("mid-traversal cancellation = (%#v, %v), want (nil, context.Canceled)", traversalRefs, traversalErr)
	}
}

func TestAvailableSemanticRefsAvoidsDeepConceptCloneAllocations(t *testing.T) {
	// Arrange. Each document carries a large body and a broad YAML tree. The
	// legacy Concepts projection clones that whole tree; semantic observation
	// APIs project only IDs, fragments, and relations.
	files := make(memorySource, 12)
	for concept := 0; concept < 12; concept++ {
		var document strings.Builder
		document.WriteString("---\ntype: thing\nmetadata:\n")
		for field := 0; field < 192; field++ {
			fmt.Fprintf(&document, "  field_%03d: value_%03d\n", field, field)
		}
		document.WriteString("---\n")
		document.WriteString(strings.Repeat("large markdown payload\n", 2048))
		files[fmt.Sprintf("concept-%02d.md", concept)] = []byte(document.String())
	}
	loaded := loadSemanticRefsBundle(t, files)

	// Act.
	projectionAllocs := testing.AllocsPerRun(20, func() {
		var err error
		semanticRefsAllocationSink, err = availableSemanticRefsFromBundle(context.Background(), loaded)
		if err != nil {
			panic(err)
		}
	})
	deepCloneAllocs := testing.AllocsPerRun(20, func() {
		conceptsAllocationSink = loaded.Concepts()
	})

	// Assert. The gap is deliberately broad so this guards the architecture,
	// not incidental compiler allocation decisions.
	if projectionAllocs*8 >= deepCloneAllocs {
		t.Fatalf("semantic projection allocations = %.0f, deep clone = %.0f; want at least 8x fewer", projectionAllocs, deepCloneAllocs)
	}
	if got, want := len(semanticRefsAllocationSink), len(files); got != want {
		t.Fatalf("semantic ref count = %d, want %d", got, want)
	}
}

func TestAvailableSemanticRefsBoundsCancellationInsideLargeSemanticLists(t *testing.T) {
	tests := []struct {
		name     string
		document func() []byte
	}{
		{
			name: "fragments",
			document: func() []byte {
				var document strings.Builder
				document.WriteString("---\ntype: thing\nparts:\n")
				for index := 0; index < 4096; index++ {
					fmt.Fprintf(&document, "  - id: fragment-%04d\n", index)
				}
				document.WriteString("---\nA\n")
				return []byte(document.String())
			},
		},
		{
			name: "relations",
			document: func() []byte {
				var document strings.Builder
				document.WriteString("---\ntype: thing\nrelations:\n  uses:\n")
				for index := 0; index < 4096; index++ {
					document.WriteString("    - target: a\n")
				}
				document.WriteString("---\nA\n")
				return []byte(document.String())
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			loaded := loadSemanticRefsBundle(t, memorySource{"a.md": test.document()})
			injected := errors.New("stop large semantic list")
			ctx := &semanticRefsErrorContext{Context: context.Background(), allowed: 40, err: injected}

			// Act.
			refs, err := availableSemanticRefsFromBundle(ctx, loaded)

			// Assert. The exact call ceiling demonstrates that cancellation is
			// polled inside one concept's list, not only between concepts.
			if refs != nil || err != injected {
				t.Fatalf("large-list projection = (%#v, %v), want exact cancellation", refs, err)
			}
			if got, want := ctx.calls, 41; got != want {
				t.Fatalf("context checks = %d, want bounded %d", got, want)
			}
		})
	}
}

func TestRelationRefSortCancellationHasConstantAllocationCount(t *testing.T) {
	// Arrange.
	small := semanticRefRange(t, 64)
	large := semanticRefRange(t, 32<<10)
	canceledSortAllocs := func(refs []bundle.RelationRef) float64 {
		return testing.AllocsPerRun(20, func() {
			ctx := &semanticRefsErrorContext{
				Context: context.Background(),
				allowed: 32,
				err:     context.Canceled,
			}
			if err := sortRelationRefsContext(ctx, refs); !errors.Is(err, context.Canceled) {
				panic(fmt.Sprintf("sort error = %v", err))
			}
			if ctx.calls != 33 {
				panic(fmt.Sprintf("context checks = %d", ctx.calls))
			}
		})
	}

	// Act.
	smallAllocs := canceledSortAllocs(small)
	largeAllocs := canceledSortAllocs(large)

	// Assert. The in-place sorter does not allocate a result-sized scratch
	// buffer before it has a chance to observe cancellation.
	if largeAllocs > smallAllocs+1 {
		t.Fatalf("canceled sort allocations grow with input: small %.0f, large %.0f", smallAllocs, largeAllocs)
	}
}

func TestSemanticRefMaterializationCancelsDuringHighCardinalitySort(t *testing.T) {
	// Arrange. Allow every map entry to materialize, then exactly 32 sort
	// checks. This deterministically injects cancellation inside ordering,
	// independent of scheduler timing or map iteration order.
	const count = 32 << 10
	seen := make(map[relationRefKey]bundle.RelationRef, count)
	for _, relationRef := range semanticRefRange(t, count) {
		seen[relationRefIdentity(relationRef)] = relationRef
	}
	injected := errors.New("cancel semantic ref sort")
	allowed := 1 + count + 32
	ctx := &semanticRefsErrorContext{
		Context: context.Background(),
		allowed: allowed,
		err:     injected,
	}

	// Act.
	refs, err := materializeSortedRelationRefsContext(ctx, seen)

	// Assert.
	if refs != nil || err != injected {
		t.Fatalf("materialization = (%#v, %v), want exact mid-sort cancellation", refs, err)
	}
	if got, want := ctx.calls, allowed+1; got != want {
		t.Fatalf("context checks = %d, want exact bounded %d", got, want)
	}
}

func TestSemanticRefProjectionCancellationHasBoundedAllocationCount(t *testing.T) {
	// Arrange.
	small := semanticRefRange(t, 64)
	large := semanticRefRange(t, 32<<10)
	tests := []struct {
		name string
		call func(context.Context, []bundle.RelationRef) ([]bundle.RelationRef, error)
	}{
		{name: "deduplicate", call: refsForContext},
		{name: "defensive copy", call: copyRefsContext},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			measure := func(refs []bundle.RelationRef) float64 {
				return testing.AllocsPerRun(20, func() {
					ctx := &semanticRefsErrorContext{
						Context: context.Background(),
						allowed: 32,
						err:     context.Canceled,
					}
					out, err := test.call(ctx, refs)
					if out != nil || !errors.Is(err, context.Canceled) {
						panic(fmt.Sprintf("projection = (%#v, %v)", out, err))
					}
					if ctx.calls != 33 {
						panic(fmt.Sprintf("context checks = %d", ctx.calls))
					}
				})
			}

			// Act.
			smallAllocs := measure(small)
			largeAllocs := measure(large)

			// Assert. Initial capacity is deliberately bounded: work and
			// allocation count depend on checks completed, not total input size.
			if largeAllocs > smallAllocs+1 {
				t.Fatalf("canceled projection allocations grow with input: small %.0f, large %.0f", smallAllocs, largeAllocs)
			}
		})
	}
}

func TestYAMLImpactPathsPropagatesReverseImpactCancellation(t *testing.T) {
	// Arrange. A single target has a deliberately large incoming relation set,
	// so cancellation must be observed within the central reverse-impact
	// projection rather than only between requested refs.
	var source strings.Builder
	source.WriteString("---\ntype: thing\nrelations:\n  uses:\n")
	for index := 0; index < 4096; index++ {
		source.WriteString("    - target: b\n")
	}
	source.WriteString("---\nA\n")
	loaded := loadSemanticRefsBundle(t, memorySource{
		"a.md": []byte(source.String()),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
	})
	target := ref(t, "b")
	injected := errors.New("reverse impact canceled")
	ctx := &semanticRefsErrorContext{Context: context.Background(), allowed: 40, err: injected}
	state := &planState{bundle: loaded}

	// Act.
	paths, err := state.yamlImpactPathsContext(ctx, target)

	// Assert.
	if paths != nil || err != injected {
		t.Fatalf("yamlImpactPathsContext() = (%#v, %v), want exact reverse-impact cancellation", paths, err)
	}
	if got, want := ctx.calls, 41; got != want {
		t.Fatalf("context checks = %d, want bounded %d", got, want)
	}
}

type semanticRefsErrorContext struct {
	context.Context
	allowed int
	err     error
	calls   int
}

func (c *semanticRefsErrorContext) Err() error {
	c.calls++
	if c.allowed > 0 {
		c.allowed--
		return nil
	}
	return c.err
}

func loadSemanticRefsBundle(t *testing.T, source memorySource) *bundle.Bundle {
	t.Helper()
	loaded, err := bundle.Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func semanticRefStrings(refs []bundle.RelationRef) []string {
	out := make([]string, len(refs))
	for index, ref := range refs {
		out[index] = ref.String()
	}
	return out
}

func semanticRefRange(t *testing.T, count int) []bundle.RelationRef {
	t.Helper()
	out := make([]bundle.RelationRef, count)
	for index := range out {
		id, err := bundle.NewConceptID([]string{fmt.Sprintf("concept-%08d", count-index)})
		if err != nil {
			t.Fatal(err)
		}
		out[index] = bundle.RelationRef{ID: id}
	}
	return out
}

func plannerHashCollisionRefs(t *testing.T) (bundle.RelationRef, bundle.RelationRef) {
	t.Helper()
	hashID, err := bundle.NewConceptID([]string{"source#part"})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := bundle.NewConceptID([]string{"source"})
	if err != nil {
		t.Fatal(err)
	}
	return bundle.RelationRef{ID: hashID}, bundle.RelationRef{ID: sourceID, Fragment: "part"}
}
