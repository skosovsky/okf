package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestResolvePathValueProjectionIsContextOwned(t *testing.T) {
	t.Parallel()
	// Arrange.
	large := strings.Repeat("scope value ", 1<<18)
	loaded := &Bundle{contents: map[string][]byte{"asset.bin": {1}}}

	// Act.
	resolved, ok, err := loaded.ResolvePathValueForContext(
		&cancelAfterErrChecksContext{allowed: 16},
		"concept.md",
		large,
		PathFieldSourceResource,
	)

	// Assert.
	if !errors.Is(err, context.Canceled) || ok || !reflect.DeepEqual(resolved, ResolvedPathValue{}) {
		t.Fatalf("ResolvePathValueForContext()=(%#v,%t,%v), want zero/false/context.Canceled", resolved, ok, err)
	}
	want, wantOK := loaded.ResolvePathValueFor("concept.md", "asset.bin", PathFieldSourceResource)
	got, gotOK, gotErr := loaded.ResolvePathValueForContext(context.Background(), "concept.md", "asset.bin", PathFieldSourceResource)
	if gotErr != nil || gotOK != wantOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("Background parity=(%#v,%t,%v), want (%#v,%t)", got, gotOK, gotErr, want, wantOK)
	}
	got.Raw = "mutated"
	again, _, _ := loaded.ResolvePathValueForContext(context.Background(), "concept.md", "asset.bin", PathFieldSourceResource)
	if again.Raw != "asset.bin" {
		t.Fatal("ResolvePathValueForContext retained caller mutation")
	}
}

func TestResolvePathValueOwnsUncapturedAndURLClassification(t *testing.T) {
	t.Parallel()
	// Arrange.
	loaded := &Bundle{contents: map[string][]byte{"tiny": {1}}}
	large := strings.Repeat("segment/", 1<<17) + "target.bin"
	tests := []struct {
		name  string
		value string
		field PathValueField
	}{
		{name: "uncaptured local", value: large, field: PathFieldComputation},
		{name: "valid URL", value: "https://example.test/" + large, field: PathFieldSourceResource},
		{name: "malformed URL", value: "https://example.test/%zz/" + large, field: PathFieldSourceResource},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			got, ok, err := loaded.ResolvePathValueForContext(&cancelAfterErrChecksContext{allowed: 20}, "concept.md", tt.value, tt.field)

			// Assert.
			if !errors.Is(err, context.Canceled) || ok || !reflect.DeepEqual(got, ResolvedPathValue{}) {
				t.Fatalf("result=(%#v,%t,%v), want exact zero/context.Canceled", got, ok, err)
			}
		})
	}

	// Act: tiny returned value isolates cancellation in authoritative lookup,
	// rather than in a later defensive clone.
	id := mustParseConceptID(t, strings.Repeat("k", 2<<20))
	loaded.byID = map[string]int{id.String(): 0}
	loaded.concepts = []Concept{{ID: id, Path: "x"}}
	got, ok, err := loaded.GetContext(&cancelAfterErrChecksContext{allowed: 48}, id)

	// Assert.
	if !errors.Is(err, context.Canceled) || ok || !reflect.DeepEqual(got, Concept{}) {
		t.Fatalf("tiny authoritative result=(%#v,%t,%v), want zero/context.Canceled", got, ok, err)
	}
}

func TestAuthoritativePathFamiliesAndSixteenMiBBoundary(t *testing.T) {
	t.Parallel()
	// Arrange.
	largeKey := strings.Repeat("i", 2<<20)
	id := mustParseConceptID(t, largeKey)
	loaded := &Bundle{byID: map[string]int{largeKey: 0}, concepts: []Concept{{ID: id, Path: "tiny.md"}}}

	// Act.
	path, ok, pathErr := loaded.ConceptPathContext(&cancelAfterErrChecksContext{allowed: 48}, id)
	observations, found, observationErr := loaded.ConceptPathValueObservationsContext(&cancelAfterErrChecksContext{allowed: 48}, id)

	// Assert.
	if !errors.Is(pathErr, context.Canceled) || ok || path != "" {
		t.Fatalf("ConceptPathContext=(%q,%t,%v), want zero/context.Canceled", path, ok, pathErr)
	}
	if !errors.Is(observationErr, context.Canceled) || found || !reflect.DeepEqual(observations, ConceptPathValueObservations{}) {
		t.Fatalf("ConceptPathValueObservations=(%#v,%t,%v), want zero/context.Canceled", observations, found, observationErr)
	}

	// Arrange: exercise the exact public Markdown scalar ceiling through URL
	// and local-path classification rather than an earlier small fixture.
	large := strings.Repeat("a", MaxMarkdownDocumentBytes-32)
	for _, value := range []string{"https://example.test/" + large, "segment/" + large} {
		// Act.
		got, gotOK, err := loaded.ResolvePathValueForContext(&cancelAfterErrChecksContext{allowed: 300}, "concept.md", value, PathFieldSourceResource)
		// Assert.
		if !errors.Is(err, context.Canceled) || gotOK || !reflect.DeepEqual(got, ResolvedPathValue{}) {
			t.Fatalf("16MiB result=(%#v,%t,%v), want zero/context.Canceled", got, gotOK, err)
		}
	}
}

func TestPathScannersOwnExactPublicLimitAndLookupPrecedesUse(t *testing.T) {
	t.Parallel()
	// Arrange: an out-of-range raw-map dereference would panic before the
	// authoritative Context lookup can cancel.
	key := strings.Repeat("lookup", 1<<18)
	id := mustParseConceptID(t, key)
	loaded := &Bundle{byID: map[string]int{key: 99}}
	for _, act := range []func(context.Context) error{
		func(ctx context.Context) error { _, _, err := loaded.ConceptPathContext(ctx, id); return err },
		func(ctx context.Context) error {
			_, _, err := loaded.ConceptPathValueObservationsContext(ctx, id)
			return err
		},
	} {
		if err := act(&cancelAfterErrChecksContext{allowed: 48}); !errors.Is(err, context.Canceled) {
			t.Fatalf("lookup-before-use error=%v, want context.Canceled", err)
		}
	}

	// Arrange/Act/Assert: direct scanner rows ensure cancellation occurs inside
	// normalization/classification, not during public input ownership.
	limit := strings.Repeat("a", MaxMarkdownDocumentBytes)
	if got, err := cleanSlashPathContext(&cancelAfterErrChecksContext{allowed: 32}, "root/../"+limit); !errors.Is(err, context.Canceled) || got != "" {
		t.Fatalf("clean scanner=(%d,%v), want zero/context.Canceled", len(got), err)
	}
	if got, err := joinAndCleanPathContext(&cancelAfterErrChecksContext{allowed: 32}, "dir", limit); !errors.Is(err, context.Canceled) || got != "" {
		t.Fatalf("join scanner=(%d,%v), want zero/context.Canceled", len(got), err)
	}
	if invalid, err := invalidURLCharactersContext(&cancelAfterErrChecksContext{allowed: 32}, "https://example.test/"+limit); !errors.Is(err, context.Canceled) || invalid {
		t.Fatalf("url scanner=(%t,%v), want false/context.Canceled", invalid, err)
	}
}
