package bundle

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestBundleContextObservationAPIsAreDefensiveAndMatchLegacyOrder(t *testing.T) {
	t.Parallel()

	// Arrange.
	sourceA := mustParseConceptID(t, "source-a")
	sourceB := mustParseConceptID(t, "source-b")
	target := mustParseConceptID(t, "target")
	rootRelation := Relation{Source: RelationRef{ID: sourceB}, Type: "depends_on", Target: RelationRef{ID: target}}
	fragmentRelation := Relation{Source: RelationRef{ID: sourceA}, Type: "depends_on", Target: RelationRef{ID: target, Fragment: "part"}}
	loaded := &Bundle{
		concepts: []Concept{
			{ID: sourceA, Path: "source-a.md"},
			{ID: sourceB, Path: "source-b.md"},
			{ID: target, Path: "target.md"},
		},
		byID: map[string]int{sourceA.String(): 0, sourceB.String(): 1, target.String(): 2},
		subresources: map[string]map[string]fragmentState{
			target.String(): {"part": {count: 1}},
		},
		incoming: map[relationRefKey][]Relation{
			relationRefIdentity(RelationRef{ID: target}):                   {rootRelation},
			relationRefIdentity(RelationRef{ID: target, Fragment: "part"}): {fragmentRelation},
		},
	}

	// Act.
	path, ok, err := loaded.ConceptPathContext(context.Background(), target)
	conceptImpact, conceptErr := loaded.ReverseImpactConceptContext(context.Background(), target)
	fragmentImpact, fragmentErr := loaded.ReverseImpactFragmentContext(context.Background(), target, "part")

	// Assert.
	if err != nil || !ok || path != "target.md" {
		t.Fatalf("ConceptPathContext() = (%q, %v, %v)", path, ok, err)
	}
	if conceptErr != nil || !reflect.DeepEqual(conceptImpact, loaded.ReverseImpactConcept(target)) {
		t.Fatalf("ReverseImpactConceptContext() = (%#v, %v), legacy=%#v", conceptImpact, conceptErr, loaded.ReverseImpactConcept(target))
	}
	if fragmentErr != nil || !reflect.DeepEqual(fragmentImpact, loaded.ReverseImpactFragment(target, "part")) {
		t.Fatalf("ReverseImpactFragmentContext() = (%#v, %v), legacy=%#v", fragmentImpact, fragmentErr, loaded.ReverseImpactFragment(target, "part"))
	}
	if len(conceptImpact) != 2 || conceptImpact[0].Source.String() != "source-a" || conceptImpact[1].Source.String() != "source-b" {
		t.Fatalf("ReverseImpactConceptContext() order = %#v", conceptImpact)
	}
	conceptImpact[0].Source.ID.segments[0] = "mutated"
	conceptImpact[0].Target.ID.segments[0] = "mutated"
	fragmentImpact[0].Type = "mutated"
	again, _ := loaded.ReverseImpactConceptContext(context.Background(), target)
	fragmentAgain, _ := loaded.ReverseImpactFragmentContext(context.Background(), target, "part")
	if again[0].Source.String() != "source-a" || again[0].Target.ID.String() != "target" || fragmentAgain[0].Type != "depends_on" {
		t.Fatalf("context result mutated retained relations: concept=%#v fragment=%#v", again, fragmentAgain)
	}
}

func TestBundleContextObservationAPIsHonorCancellationAndUnknownParity(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := mustParseConceptID(t, "source")
	target := mustParseConceptID(t, "target")
	unknown := mustParseConceptID(t, "unknown")
	relation := Relation{Source: RelationRef{ID: source}, Type: "depends_on", Target: RelationRef{ID: target, Fragment: "part"}}
	loaded := &Bundle{
		concepts: []Concept{{ID: source, Path: "source.md"}, {ID: target, Path: "target.md"}},
		byID:     map[string]int{source.String(): 0, target.String(): 1},
		subresources: map[string]map[string]fragmentState{
			target.String(): {"part": {count: 1}},
		},
		incoming: map[relationRefKey][]Relation{
			relationRefIdentity(RelationRef{ID: target, Fragment: "part"}): {relation, relation},
		},
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// Act and assert: pre-call cancellation.
	if _, _, err := loaded.ConceptPathContext(canceled, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("ConceptPathContext() error = %v, want context.Canceled", err)
	}
	if _, err := loaded.ReverseImpactConceptContext(canceled, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReverseImpactConceptContext() error = %v, want context.Canceled", err)
	}
	if _, err := loaded.ReverseImpactFragmentContext(canceled, target, "part"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReverseImpactFragmentContext() error = %v, want context.Canceled", err)
	}

	// Act and assert: deterministic mid-projection cancellation.
	if path, ok, err := loaded.ConceptPathContext(&cancelAfterErrChecksContext{allowed: 1}, target); path != "" || ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("ConceptPathContext() = (%q, %v, %v), want canceled zero result", path, ok, err)
	}
	if got, err := loaded.ReverseImpactConceptContext(&cancelAfterErrChecksContext{allowed: 2}, target); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ReverseImpactConceptContext() = (%#v, %v), want canceled nil result", got, err)
	}
	if got, err := loaded.ReverseImpactFragmentContext(&cancelAfterErrChecksContext{allowed: 2}, target, "part"); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ReverseImpactFragmentContext() = (%#v, %v), want canceled nil result", got, err)
	}

	// Act and assert: nil/unknown parity.
	var nilBundle *Bundle
	if path, ok, err := nilBundle.ConceptPathContext(context.Background(), target); path != "" || ok || err != nil {
		t.Fatalf("nil ConceptPathContext() = (%q, %v, %v)", path, ok, err)
	}
	if got, err := nilBundle.ReverseImpactConceptContext(context.Background(), target); got != nil || err != nil {
		t.Fatalf("nil ReverseImpactConceptContext() = (%#v, %v)", got, err)
	}
	if got, err := nilBundle.ReverseImpactFragmentContext(context.Background(), target, "part"); got != nil || err != nil {
		t.Fatalf("nil ReverseImpactFragmentContext() = (%#v, %v)", got, err)
	}
	if path, ok, err := loaded.ConceptPathContext(context.Background(), unknown); path != "" || ok || err != nil {
		t.Fatalf("unknown ConceptPathContext() = (%q, %v, %v)", path, ok, err)
	}
	if got, err := loaded.ReverseImpactConceptContext(context.Background(), unknown); got != nil || err != nil {
		t.Fatalf("unknown ReverseImpactConceptContext() = (%#v, %v)", got, err)
	}
	if got, err := loaded.ReverseImpactFragmentContext(context.Background(), target, "unknown"); got != nil || err != nil {
		t.Fatalf("unknown ReverseImpactFragmentContext() = (%#v, %v)", got, err)
	}
}

func TestReverseImpactConceptContextCancelsDuringHighCardinalitySort(t *testing.T) {
	t.Parallel()

	// Arrange.
	const relationCount = 4096
	target := mustParseConceptID(t, "target")
	relations := make([]Relation, 0, relationCount)
	for index := relationCount - 1; index >= 0; index-- {
		source := mustParseConceptID(t, "source-"+zeroPaddedDecimal(index, 4))
		relations = append(relations, Relation{
			Source: sourceRef(source),
			Type:   "depends_on",
			Target: RelationRef{ID: target},
		})
	}
	loaded := &Bundle{
		concepts: []Concept{{ID: target, Path: "target.md"}},
		byID:     map[string]int{target.String(): 0},
		incoming: map[relationRefKey][]Relation{relationRefIdentity(RelationRef{ID: target}): relations},
	}
	// One initial check, one per incoming relation, one SubresourcesContext
	// check, one per map materialization, and the sort pre-check are allowed.
	// The next check is the first heap-sort comparison.
	ctx := &cancelAfterErrChecksContext{allowed: 2*relationCount + 3}

	// Act.
	canceled, err := loaded.ReverseImpactConceptContext(ctx, target)
	first, firstErr := loaded.ReverseImpactConceptContext(context.Background(), target)
	second, secondErr := loaded.ReverseImpactConceptContext(context.Background(), target)
	legacy := loaded.ReverseImpactConcept(target)

	// Assert.
	if canceled != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-sort ReverseImpactConceptContext() = (%#v, %v), want (nil, context.Canceled)", canceled, err)
	}
	if firstErr != nil || secondErr != nil || !reflect.DeepEqual(first, second) || !reflect.DeepEqual(first, legacy) {
		t.Fatalf("background deterministic parity failed: firstErr=%v secondErr=%v equal=%v legacyEqual=%v", firstErr, secondErr, reflect.DeepEqual(first, second), reflect.DeepEqual(first, legacy))
	}
	if len(first) != relationCount || first[0].Source.String() != "source-0000" ||
		first[len(first)-1].Source.String() != "source-4095" {
		t.Fatalf("sorted impact boundary = len %d, first %q, last %q", len(first), first[0].Source.String(), first[len(first)-1].Source.String())
	}
}

func TestReverseImpactConceptContextUsesCollisionFreeRelationIdentity(t *testing.T) {
	t.Parallel()

	// Arrange: these tuples collided under NUL-delimited concatenation.
	target := mustParseConceptID(t, "target")
	source := mustParseConceptID(t, "source")
	first := Relation{
		Source: RelationRef{ID: source}, Type: "a",
		Target: RelationRef{ID: ConceptID{segments: []string{"b\x00c"}}},
	}
	second := Relation{
		Source: RelationRef{ID: source}, Type: "a\x00b",
		Target: RelationRef{ID: ConceptID{segments: []string{"c"}}},
	}
	loaded := &Bundle{
		concepts: []Concept{{ID: target}},
		byID:     map[string]int{target.String(): 0},
		incoming: map[relationRefKey][]Relation{relationRefIdentity(RelationRef{ID: target}): {second, first}},
	}

	// Act.
	got, err := loaded.ReverseImpactConceptContext(context.Background(), target)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Type != "a" || got[1].Type != "a\x00b" {
		t.Fatalf("collision-free relations = %#v", got)
	}
}

func sourceRef(id ConceptID) RelationRef { return RelationRef{ID: id} }

func zeroPaddedDecimal(value, width int) string {
	digits := "0"
	if value > 0 {
		digits = ""
		for current := value; current > 0; current /= 10 {
			digits = string(rune('0'+current%10)) + digits
		}
	}
	for len(digits) < width {
		digits = "0" + digits
	}
	return digits
}
