package bundle

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProjectionHashProjectionMapOwnersCancelInsidePreparedKeys(t *testing.T) {
	large := strings.Repeat("k", 2<<20)
	id := ConceptID{segments: []string{large}}
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
	}{
		{name: "string lookup", act: func(ctx context.Context) (any, error) {
			value, ok, err := lookupStringMapContext(ctx, map[string]int{large: 1}, large)
			return struct {
				Value int
				OK    bool
			}{value, ok}, err
		}},
		{name: "relation key lookup", act: func(ctx context.Context) (any, error) {
			key := relationRefKey{id: large, fragment: large}
			value, ok, err := lookupRelationRefMapContext(ctx, map[relationRefKey][]Relation{key: {}}, key)
			return struct {
				Len int
				OK  bool
			}{len(value), ok}, err
		}},
		{name: "dedupe key", act: func(ctx context.Context) (any, error) {
			return migrationSafeRelationProjectionKeyContext(ctx, Relation{Source: RelationRef{ID: id, Fragment: large}, Type: large, Target: RelationRef{ID: id, Fragment: large}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, err := test.act(probe)
			checks := probe.checks.Load()
			if err != nil || checks < 32 {
				t.Fatalf("probe=%v/%d", err, checks)
			}
			got, gotErr := test.act(&cancelAfterErrChecksContext{allowed: checks / 2})
			if got == nil || !errors.Is(gotErr, context.Canceled) {
				t.Fatalf("cancel=(%#v,%v)", got, gotErr)
			}
		})
	}
}

func TestProjectionHashReachableProjectionsCancelWithAttackerKeys(t *testing.T) {
	large := strings.Repeat("r", 1<<20)
	id := ConceptID{segments: []string{large}}
	target := RelationRef{ID: id, Fragment: large}
	relation := Relation{Source: target, Type: large, Target: target, TargetExists: true}
	loaded := &Bundle{byID: map[string]int{large: 0}, subresources: map[string]map[string]fragmentState{large: {large: {count: 1}}}, incoming: map[relationRefKey][]Relation{relationRefIdentity(target): {relation, relation}}}
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
	}{{"subresources", func(ctx context.Context) (any, error) { return loaded.SubresourcesContext(ctx, id) }}, {"reverse impact", func(ctx context.Context) (any, error) { return loaded.ReverseImpactConceptContext(ctx, id) }}, {"fragment impact", func(ctx context.Context) (any, error) { return loaded.ReverseImpactFragmentContext(ctx, id, large) }}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, err := test.act(probe)
			checks := probe.checks.Load()
			if err != nil || checks < 32 {
				t.Fatalf("probe=%v/%d", err, checks)
			}
			got, gotErr := test.act(&cancelAfterErrChecksContext{allowed: checks - checks/3})
			if got != nil && !reflectValueNil(got) || !errors.Is(gotErr, context.Canceled) {
				t.Fatalf("projection=(%#v,%v)", got, gotErr)
			}
		})
	}
}

func reflectValueNil(value any) bool {
	switch value.(type) {
	case []string, []Relation:
		return true
	}
	return false
}

func TestProjectionHashBackgroundParity(t *testing.T) {
	id := ConceptID{segments: []string{"known"}}
	loaded := &Bundle{byID: map[string]int{"known": 0}, subresources: map[string]map[string]fragmentState{"known": {"part": {count: 1}}}}
	got, err := loaded.SubresourcesContext(context.Background(), id)
	if err != nil || len(got) != 1 || got[0] != "part" {
		t.Fatalf("subresources=(%#v,%v)", got, err)
	}
}
