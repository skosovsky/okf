package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGraphBundleProjectionsAreContextOwned(t *testing.T) {
	t.Parallel()
	// Arrange.
	id := mustParseConceptID(t, "concept")
	large := strings.Repeat("x", 2<<20)
	largeID, err := NewConceptID([]string{large})
	if err != nil {
		t.Fatal(err)
	}
	document, err := ParseDocument("---\ntype: Note\ntitle: " + large + "\n---\nBody\n")
	if err != nil {
		t.Fatal(err)
	}
	loaded := &Bundle{
		concepts: []Concept{{ID: id, Path: "concept.md", Document: document}},
		byID:     map[string]int{"concept": 0},
		outbound: map[string][]ResolvedLink{"concept": {{Target: id, Exists: true, Text: large, Raw: large}}},
		observations: map[string][]RelationObservation{"concept": {{
			Source: RelationRef{ID: id, Fragment: large}, Type: large,
			Target: RelationRef{ID: id, Fragment: large}, TargetExists: true, RawTarget: large,
		}}},
	}
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
	}{
		{name: "concept", act: func(ctx context.Context) (any, error) { value, _, err := loaded.GetContext(ctx, id); return value, err }},
		{name: "links", act: func(ctx context.Context) (any, error) { return loaded.LinksFromContext(ctx, id) }},
		{name: "declared relations", act: func(ctx context.Context) (any, error) { return loaded.DeclaredSemanticLinksFromContext(ctx, id) }},
		{name: "concept identity", act: func(ctx context.Context) (any, error) { return largeID.StringContext(ctx) }},
		{name: "relation identity", act: func(ctx context.Context) (any, error) {
			return (RelationRef{ID: largeID, Fragment: large}).StringContext(ctx)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			got, err := tt.act(&cancelAfterErrChecksContext{allowed: 16})

			// Assert.
			if !errors.Is(err, context.Canceled) || !reflect.ValueOf(got).IsZero() {
				t.Fatalf("projection=(%#v,%v), want exact zero/context.Canceled", got, err)
			}
		})
	}

	// Act.
	concept, ok, err := loaded.GetContext(context.Background(), id)
	links, linksErr := loaded.LinksFromContext(context.Background(), id)
	observations, observationsErr := loaded.DeclaredSemanticLinksFromContext(context.Background(), id)

	// Assert.
	if err != nil || !ok || linksErr != nil || observationsErr != nil {
		t.Fatalf("background projections errors=%v/%v/%v ok=%t", err, linksErr, observationsErr, ok)
	}
	concept.Path = "mutated"
	links[0].Text = "mutated"
	observations[0].Source.Fragment = "mutated"
	againConcept, _, _ := loaded.GetContext(context.Background(), id)
	againLinks, _ := loaded.LinksFromContext(context.Background(), id)
	againObservations, _ := loaded.DeclaredSemanticLinksFromContext(context.Background(), id)
	if againConcept.Path != "concept.md" || againLinks[0].Text != large || againObservations[0].Source.Fragment != large {
		t.Fatal("Context projection retained caller mutation")
	}
}

func TestTopologyLifecycleAccessorsAreContextOwned(t *testing.T) {
	t.Parallel()
	// Arrange.
	large := strings.Repeat("s", 2<<20)
	document, err := ParseDocument("---\ntype: Note\nstatus: " + large + "\nstale_after: 2026-08-12\n---\nBody\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	status, statusErr := document.StatusStateContext(&cancelAfterErrChecksContext{allowed: 16})
	stale, staleErr := document.IsStaleContext(&cancelAfterErrChecksContext{allowed: 2}, time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC))

	// Assert.
	if !errors.Is(statusErr, context.Canceled) || !reflect.DeepEqual(status, StatusState{}) {
		t.Fatalf("StatusStateContext()=(%#v,%v), want zero/context.Canceled", status, statusErr)
	}
	if !errors.Is(staleErr, context.Canceled) || stale {
		t.Fatalf("IsStaleContext()=(%t,%v), want false/context.Canceled", stale, staleErr)
	}
	backgroundStatus, err := document.StatusStateContext(context.Background())
	if err != nil || !reflect.DeepEqual(backgroundStatus, document.StatusState()) {
		t.Fatalf("StatusState Background parity=(%#v,%v), wrapper=%#v", backgroundStatus, err, document.StatusState())
	}
	backgroundStale, err := document.IsStaleContext(context.Background(), time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC))
	if err != nil || backgroundStale != document.IsStale(time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("IsStale Background parity=(%t,%v)", backgroundStale, err)
	}
}

func TestGraphBundleProjectionPrecancelPrecedence(t *testing.T) {
	t.Parallel()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	invalid := ConceptID{}
	var nilBundle *Bundle

	if value, ok, err := nilBundle.GetContext(canceled, invalid); !errors.Is(err, context.Canceled) || ok || !reflect.DeepEqual(value, Concept{}) {
		t.Fatalf("GetContext precancel=(%#v,%t,%v)", value, ok, err)
	}
	if value, err := nilBundle.LinksFromContext(canceled, invalid); !errors.Is(err, context.Canceled) || value != nil {
		t.Fatalf("LinksFromContext precancel=(%#v,%v)", value, err)
	}
	if value, err := nilBundle.DeclaredSemanticLinksFromContext(canceled, invalid); !errors.Is(err, context.Canceled) || value != nil {
		t.Fatalf("DeclaredSemanticLinksFromContext precancel=(%#v,%v)", value, err)
	}
}
