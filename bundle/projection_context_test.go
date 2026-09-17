package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestProjectionConceptIdentityAndCloneOwnersCancelInsideSegments(t *testing.T) {
	large := strings.Repeat("s", 2<<20)
	id := ConceptID{segments: []string{large, large}}
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
		zero any
	}{
		{name: "concept clone", act: func(ctx context.Context) (any, error) { return cloneConceptIDContext(ctx, id) }, zero: ConceptID{}},
		{name: "relation clone", act: func(ctx context.Context) (any, error) {
			return cloneRelationContext(ctx, Relation{Source: RelationRef{ID: id, Fragment: large}, Type: large, Target: RelationRef{ID: id, Fragment: large}, RawTarget: large})
		}, zero: Relation{}},
		{name: "diagnostic clone", act: func(ctx context.Context) (any, error) {
			return cloneRelationDiagnosticContext(ctx, RelationDiagnostic{Source: id, SourceFragment: large, RelationType: large, RawTarget: large, File: large, Message: large, Code: large})
		}, zero: RelationDiagnostic{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, probeErr := test.act(probe)
			checks := probe.checks.Load()
			if probeErr != nil || checks < 32 {
				t.Fatalf("probe err=%v checks=%d", probeErr, checks)
			}
			got, gotErr := test.act(&cancelAfterErrChecksContext{allowed: checks / 2})
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, test.zero) {
				t.Fatalf("cancel=(%#v,%v)", got, gotErr)
			}
		})
	}
}

func TestProjectionPublicLookupsCancelInsideConceptIdentity(t *testing.T) {
	large := strings.Repeat("i", 2<<20)
	id := ConceptID{segments: []string{large}}
	loaded := &Bundle{byID: map[string]int{large: 0}, concepts: []Concept{{ID: id, Path: "concept.md"}}}
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
		zero any
	}{
		{name: "concept path", act: func(ctx context.Context) (any, error) {
			path, found, err := loaded.ConceptPathContext(ctx, id)
			return struct {
				Path  string
				Found bool
			}{path, found}, err
		}, zero: struct {
			Path  string
			Found bool
		}{}},
		{name: "path observations", act: func(ctx context.Context) (any, error) {
			value, found, err := loaded.ConceptPathValueObservationsContext(ctx, id)
			return struct {
				Value ConceptPathValueObservations
				Found bool
			}{value, found}, err
		}, zero: struct {
			Value ConceptPathValueObservations
			Found bool
		}{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, err := test.act(probe)
			checks := probe.checks.Load()
			if err != nil || checks < 16 {
				t.Fatalf("probe=%v/%d", err, checks)
			}
			got, gotErr := test.act(&cancelAfterErrChecksContext{allowed: checks / 2})
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, test.zero) {
				t.Fatalf("cancel=(%#v,%v)", got, gotErr)
			}
		})
	}
}

func TestProjectionPublicProjectionsCancelWithoutPartialPublication(t *testing.T) {
	large := strings.Repeat("p", 1<<20)
	id := ConceptID{segments: []string{large}}
	relation := Relation{Source: RelationRef{ID: id, Fragment: large}, Type: large, Target: RelationRef{ID: id, Fragment: large}, RawTarget: large, TargetExists: true}
	loaded := &Bundle{concepts: []Concept{{ID: id}, {ID: id}}, semantic: map[string][]Relation{large: {relation, relation}}, diagnostics: []RelationDiagnostic{{Source: id, SourceFragment: large, Message: large}, {Source: id, SourceFragment: large, Message: large}}}
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
	}{
		{name: "concept ids", act: func(ctx context.Context) (any, error) { return loaded.ConceptIDsContext(ctx) }},
		{name: "semantic relations", act: func(ctx context.Context) (any, error) { return loaded.SemanticLinksFromContext(ctx, id) }},
		{name: "diagnostics", act: func(ctx context.Context) (any, error) { return loaded.RelationDiagnosticsContext(ctx) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, err := test.act(probe)
			checks := probe.checks.Load()
			if err != nil || checks < 32 {
				t.Fatalf("probe=%v/%d", err, checks)
			}
			got, gotErr := test.act(&cancelAfterErrChecksContext{allowed: checks - checks/3})
			if (got != nil && !reflect.ValueOf(got).IsNil()) || !errors.Is(gotErr, context.Canceled) {
				t.Fatalf("projection=(%#v,%v)", got, gotErr)
			}
		})
	}
}

func TestProjectionBackgroundParityAndDeepOwnership(t *testing.T) {
	id := ConceptID{segments: []string{"known"}}
	relation := Relation{Source: RelationRef{ID: id}, Target: RelationRef{ID: id}, Type: "uses", TargetExists: true}
	loaded := &Bundle{concepts: []Concept{{ID: id}}, semantic: map[string][]Relation{"known": {relation}}, diagnostics: []RelationDiagnostic{{Source: id, Code: "code"}}}
	ids, err := loaded.ConceptIDsContext(context.Background())
	if err != nil || !reflect.DeepEqual(ids, []ConceptID{id}) {
		t.Fatalf("ids=(%#v,%v)", ids, err)
	}
	relations, err := loaded.SemanticLinksFromContext(context.Background(), id)
	if err != nil || !reflect.DeepEqual(relations, loaded.SemanticLinksFrom(id)) {
		t.Fatalf("relations=(%#v,%v)", relations, err)
	}
	diagnostics, err := loaded.RelationDiagnosticsContext(context.Background())
	if err != nil || !reflect.DeepEqual(diagnostics, loaded.RelationDiagnostics()) {
		t.Fatalf("diagnostics=(%#v,%v)", diagnostics, err)
	}
	ids[0].segments[0] = "mutated"
	relations[0].Source.ID.segments[0] = "mutated"
	diagnostics[0].Source.segments[0] = "mutated"
	again, _ := loaded.ConceptIDsContext(context.Background())
	againRelations, _ := loaded.SemanticLinksFromContext(context.Background(), id)
	againDiagnostics, _ := loaded.RelationDiagnosticsContext(context.Background())
	if again[0].String() != "known" || againRelations[0].Source.ID.String() != "known" || againDiagnostics[0].Source.String() != "known" {
		t.Fatalf("ownership leaked")
	}
}
