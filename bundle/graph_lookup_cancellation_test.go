package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAuthoritativeLookupsCancelAfterIdentityPreparation(t *testing.T) {
	t.Parallel()
	// Arrange.
	large := strings.Repeat("identifier", 1<<18)
	id, err := NewConceptID([]string{large})
	if err != nil {
		t.Fatal(err)
	}
	loaded := &Bundle{
		concepts:     []Concept{{ID: id, Path: large + ".md"}},
		byID:         map[string]int{large: 0},
		outbound:     map[string][]ResolvedLink{large: {{Target: id, Exists: true}}},
		semantic:     map[string][]Relation{large: {{Source: RelationRef{ID: id}, Target: RelationRef{ID: id}}}},
		observations: map[string][]RelationObservation{large: {{Source: RelationRef{ID: id}, Target: RelationRef{ID: id}}}},
		contents:     map[string][]byte{large + ".bin": {1}},
	}
	tests := []struct {
		name    string
		allowed int64
		act     func(context.Context) (any, error)
	}{
		{name: "get", allowed: 48, act: func(ctx context.Context) (any, error) { value, _, err := loaded.GetContext(ctx, id); return value, err }},
		{name: "links", allowed: 48, act: func(ctx context.Context) (any, error) { return loaded.LinksFromContext(ctx, id) }},
		{name: "semantic", allowed: 48, act: func(ctx context.Context) (any, error) { return loaded.SemanticLinksFromContext(ctx, id) }},
		{name: "declared", allowed: 48, act: func(ctx context.Context) (any, error) { return loaded.DeclaredSemanticLinksFromContext(ctx, id) }},
		{name: "path contents", allowed: 96, act: func(ctx context.Context) (any, error) {
			value, _, err := loaded.ResolvePathValueForContext(ctx, "concept.md", large+".bin", PathFieldSourceResource)
			return value, err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			value, err := tt.act(&cancelAfterErrChecksContext{allowed: tt.allowed})

			// Assert.
			if !errors.Is(err, context.Canceled) || !reflect.ValueOf(value).IsZero() {
				t.Fatalf("zero=%t error=%v, want true/context.Canceled", reflect.ValueOf(value).IsZero(), err)
			}
		})
	}
}
