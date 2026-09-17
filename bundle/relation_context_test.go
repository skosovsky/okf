package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRelationOwnersCancelInsideAttackerStrings(t *testing.T) {
	large := strings.Repeat("a", 2<<20)
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
		zero any
	}{
		{name: "standard key", act: func(ctx context.Context) (any, error) { return isStandardFrontmatterKeyContext(ctx, large) }, zero: false},
		{name: "string key", act: func(ctx context.Context) (any, error) {
			return isStringKeyContext(ctx, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: large + "a"}, large+"b")
		}, zero: false},
		{name: "relation type", act: func(ctx context.Context) (any, error) { return validRelationTypeContext(ctx, large) }, zero: false},
		{name: "relation fragment", act: func(ctx context.Context) (any, error) { return validRelationFragmentContext(ctx, large) }, zero: false},
		{name: "relation ref parser", act: func(ctx context.Context) (any, error) { return parseRelationRefContext(ctx, "target#"+large) }, zero: RelationRef{}},
		{name: "relation ref comparator", act: func(ctx context.Context) (any, error) {
			left := RelationRef{ID: ConceptID{segments: []string{large}}, Fragment: large}
			right := RelationRef{ID: ConceptID{segments: []string{large + "b"}}, Fragment: large}
			return compareRelationRefsContext(ctx, left, right)
		}, zero: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, probeErr := test.act(probe)
			checks := probe.checks.Load()
			if probeErr != nil || checks < 16 {
				t.Fatalf("probe err=%v checks=%d", probeErr, checks)
			}
			got, gotErr := test.act(&cancelAfterErrChecksContext{allowed: checks / 2})
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, test.zero) {
				t.Fatalf("cancel=(zero=%v,err=%v), want zero/context.Canceled", reflect.DeepEqual(got, test.zero), gotErr)
			}
		})
	}
}

func TestRelationExtractRelationsContextCancelsInsideTargetParser(t *testing.T) {
	large := strings.Repeat("fragment", 256<<10)
	frontmatter, err := ParseFrontmatter("relations:\n  uses:\n    - target: target#" + large + "\n")
	if err != nil {
		t.Fatal(err)
	}
	source := ConceptID{segments: []string{"source"}}
	target := ConceptID{segments: []string{"target"}}
	loaded := &Bundle{byID: map[string]int{"source": 0, "target": 1}, subresources: map[string]map[string]fragmentState{}}
	concept := Concept{ID: source, Path: "source.md", Document: NewDocument(frontmatter, "")}
	ctx := &stackFrameCancelContext{target: "parseRelationRefContext", cancelAt: 3}

	relations, observations, gotErr := extractSemanticRelationsContext(ctx, concept, loaded)
	if relations != nil || observations != nil || !errors.Is(gotErr, context.Canceled) || ctx.matches.Load() != 3 {
		t.Fatalf("extract=(relations=%d,observations=%d,err=%v,matches=%d)", len(relations), len(observations), gotErr, ctx.matches.Load())
	}
	_ = target
}

func TestRelationContextBackgroundParityAndOwnership(t *testing.T) {
	inputs := []string{"target", `path\\#part#fragment`, "target#part"}
	for _, input := range inputs {
		legacy, legacyErr := ParseRelationRef(input)
		got, gotErr := parseRelationRefContext(context.Background(), input)
		if !reflect.DeepEqual(got, legacy) || !sameErrorText(gotErr, legacyErr) {
			t.Fatalf("parser parity %q: got=(%#v,%v), legacy=(%#v,%v)", input, got, gotErr, legacy, legacyErr)
		}
	}

	id := ConceptID{segments: []string{"known"}}
	relation := Relation{Source: RelationRef{ID: id}, Type: "uses", Target: RelationRef{ID: id, Fragment: "part"}, TargetExists: true}
	loaded := &Bundle{byID: map[string]int{"known": 0}, subresources: map[string]map[string]fragmentState{"known": {"part": {count: 1}}}, incoming: map[relationRefKey][]Relation{relationRefIdentity(relation.Target): {relation}}}
	got, err := loaded.ReverseImpactFragmentContext(context.Background(), id, "part")
	if err != nil || len(got) != 1 {
		t.Fatalf("projection=(%#v,%v)", got, err)
	}
	got[0].Target.ID.segments[0] = "mutated"
	again, err := loaded.ReverseImpactFragmentContext(context.Background(), id, "part")
	if err != nil || again[0].Target.ID.String() != "known" {
		t.Fatalf("ownership leaked: (%#v,%v)", again, err)
	}
}

func TestRelationTargetExistsAndProjectionSortCancelInsideIdentityAndComparator(t *testing.T) {
	large := strings.Repeat("x", 2<<20)
	id := ConceptID{segments: []string{large}}
	fragment := large + "-part"
	loaded := &Bundle{
		byID:         map[string]int{large: 0},
		subresources: map[string]map[string]fragmentState{large: {fragment: {count: 1}}},
		incoming:     make(map[relationRefKey][]Relation),
	}

	t.Run("target identity", func(t *testing.T) {
		// Arrange.
		probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
		got, err := loaded.targetExistsContext(probe, RelationRef{ID: id, Fragment: fragment})
		if err != nil || !got || probe.checks.Load() < 32 {
			t.Fatalf("baseline=(%v,%v), checks=%d", got, err, probe.checks.Load())
		}

		// Act.
		got, err = loaded.targetExistsContext(&cancelAfterErrChecksContext{allowed: probe.checks.Load() / 2}, RelationRef{ID: id, Fragment: fragment})

		// Assert.
		if got || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled target=(%v,%v), want false/context.Canceled", got, err)
		}
	})

	t.Run("reachable projection comparator", func(t *testing.T) {
		// Arrange.
		target := RelationRef{ID: id, Fragment: fragment}
		for index := 3; index >= 0; index-- {
			source := RelationRef{ID: ConceptID{segments: []string{large + string(rune('a'+index))}}}
			relation := Relation{Source: source, Type: large + "_type", Target: target, TargetExists: true}
			loaded.incoming[relationRefIdentity(target)] = append(loaded.incoming[relationRefIdentity(target)], relation)
		}
		probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
		baseline, err := loaded.ReverseImpactFragmentContext(probe, id, fragment)
		checks := probe.checks.Load()
		if err != nil || len(baseline) != 4 || checks < 64 {
			t.Fatalf("baseline=(%d,%v), checks=%d", len(baseline), err, checks)
		}

		// Act.
		got, gotErr := loaded.ReverseImpactFragmentContext(&cancelAfterErrChecksContext{allowed: checks - checks/3}, id, fragment)

		// Assert.
		if got != nil || !errors.Is(gotErr, context.Canceled) {
			t.Fatalf("canceled projection=(%#v,%v), want nil/context.Canceled", got, gotErr)
		}
	})
}
