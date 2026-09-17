package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDocumentAttributionsKeyedAndCodeAware(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := ParseDocument(`---
type: Note
sources:
  - {id: b, resource: second}
  - {id: a, resource: first}
  - {id: a, resource: duplicate}
---
Claim one.[^a] Claim repeated.[^a] Unknown.[^missing] Escaped \[^ignored].
[marker-shaped [^ignored-link]](https://example.test)

` + "`inline [^ignored]`" + `

` + "```text\n[^fenced]\n[^fenced]: no\n```\n" + `
[^a]: prose cannot replace structured source
[^missing]: still visible
`)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	attributions := document.Attributions()

	// Assert.
	if len(attributions) != 3 {
		t.Fatalf("len(Attributions()) = %d, want 3: %#v", len(attributions), attributions)
	}
	a := attributions[0]
	if a.ID != "a" || len(a.Sources) != 2 || len(a.References) != 2 || len(a.Definitions) != 1 {
		t.Fatalf("attribution a = %#v", a)
	}
	if attributions[1].ID != "b" || len(attributions[1].References) != 0 {
		t.Fatalf("attribution b = %#v", attributions[1])
	}
	if attributions[2].ID != "missing" || len(attributions[2].Sources) != 0 || len(attributions[2].References) != 1 {
		t.Fatalf("unknown attribution = %#v", attributions[2])
	}
}

func TestDocumentCitationsFallbackOnlyWithoutSources(t *testing.T) {
	t.Parallel()

	// Arrange.
	legacy, err := ParseDocument("---\ntype: Note\n---\n# Citations\n- https://legacy\n")
	if err != nil {
		t.Fatal(err)
	}
	modern, err := ParseDocument("---\ntype: Note\nsources: wrong-shape\n---\n# Citations\n- https://legacy\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	legacyCitations := legacy.Citations()
	modernCitations := modern.Citations()

	// Assert.
	if len(legacyCitations) != 1 {
		t.Fatalf("legacy Citations() = %#v", legacyCitations)
	}
	if modernCitations != nil {
		t.Fatalf("modern Citations() = %#v, want nil", modernCitations)
	}
}

func TestDocumentDuplicateSourcesSuppressLegacyCitationsFallback(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := ParseDocument(`---
type: Note
sources: [{id: first, resource: one}]
sources: [{id: second, resource: two}]
---
# Citations
- https://legacy.example
`)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	state := document.Frontmatter.SemanticValueState("sources")
	observation := document.LegacyFallbackObservation()
	citations := document.Citations()

	// Assert.
	if !state.Present || !state.Ambiguous || state.Value != nil {
		t.Fatalf("sources state = %#v, want present ambiguous unresolved", state)
	}
	if !observation.SourcesPresent || observation.CitationsAllowed || observation.CitationsActive {
		t.Fatalf("LegacyFallbackObservation() = %#v; duplicate sources must suppress fallback", observation)
	}
	if citations != nil {
		t.Fatalf("Citations() = %#v, want suppressed legacy citations", citations)
	}
}

func TestDocumentAttributionsRetainValidSourceIdentityWithMalformedSibling(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := ParseDocument(`---
type: Note
sources:
  - id: source-a
    resource: https://example.test/a
    title: 42
---
Claim.[^source-a]

[^source-a]: explanatory prose
`)
	if err != nil {
		t.Fatal(err)
	}
	if sources := document.Sources(); len(sources) != 0 {
		t.Fatalf("Sources() = %#v, want no complete-valid sources", sources)
	}

	// Act.
	attributions := document.Attributions()

	// Assert.
	if len(attributions) != 1 {
		t.Fatalf("Attributions() = %#v", attributions)
	}
	attribution := attributions[0]
	if attribution.ID != "source-a" || len(attribution.Sources) != 1 ||
		attribution.Sources[0].ID != "source-a" ||
		attribution.Sources[0].Resource != "https://example.test/a" ||
		len(attribution.References) != 1 || len(attribution.Definitions) != 1 {
		t.Fatalf("Attribution = %#v", attribution)
	}
}

func TestDocumentAttributionsJoinNormalizedFootnoteIdentityWithoutChangingRawIDs(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := ParseDocument(`---
type: Note
sources:
  - id: Spec
    resource: https://example.test/spec
---
Claim.[^spec]

[^spec]: definition
`)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	attributions := document.Attributions()

	// Assert.
	if len(attributions) != 1 {
		t.Fatalf("Attributions() = %#v", attributions)
	}
	attribution := attributions[0]
	if attribution.ID != "Spec" || len(attribution.Sources) != 1 ||
		attribution.Sources[0].ID != "Spec" || len(attribution.References) != 1 ||
		attribution.References[0].ID != "spec" || len(attribution.Definitions) != 1 ||
		attribution.Definitions[0].ID != "spec" {
		t.Fatalf("Attribution raw identities changed: %#v", attribution)
	}
}

func TestDocumentAttributionsAreIndependentOfRawObservationOrder(t *testing.T) {
	t.Parallel()

	parse := func(t *testing.T, sources, body string) Document {
		t.Helper()
		document, err := ParseDocument("---\ntype: Note\nsources:\n" + sources + "---\n" + body)
		if err != nil {
			t.Fatal(err)
		}
		return document
	}

	// Arrange.
	first := parse(t,
		"  - {id: spec, resource: z-resource}\n  - {id: Spec, resource: a-resource}\n",
		"One.[^spec] Two.[^Spec]\n\n[^spec]: z definition\n[^Spec]: a definition\n",
	)
	second := parse(t,
		"  - {id: Spec, resource: a-resource}\n  - {id: spec, resource: z-resource}\n",
		"Two.[^Spec] One.[^spec]\n\n[^Spec]: a definition\n[^spec]: z definition\n",
	)

	// Act.
	firstAttributions := first.Attributions()
	secondAttributions := second.Attributions()

	// Assert.
	if !reflect.DeepEqual(firstAttributions, secondAttributions) {
		t.Fatalf("Attributions() depend on observation order:\nfirst=%#v\nsecond=%#v", firstAttributions, secondAttributions)
	}
	contextAttributions, contextErr := first.AttributionsContext(context.Background())
	if contextErr != nil || !reflect.DeepEqual(contextAttributions, firstAttributions) {
		t.Fatalf("AttributionsContext() = (%#v, %v), legacy=%#v", contextAttributions, contextErr, firstAttributions)
	}
	if len(firstAttributions) != 1 || firstAttributions[0].ID != "Spec" ||
		firstAttributions[0].NormalizedID != "spec" {
		t.Fatalf("stable attribution identity = %#v, want ID=Spec NormalizedID=spec", firstAttributions)
	}
	if got := []string{
		firstAttributions[0].Sources[0].ID,
		firstAttributions[0].Sources[1].ID,
		firstAttributions[0].References[0].ID,
		firstAttributions[0].References[1].ID,
		firstAttributions[0].Definitions[0].ID,
		firstAttributions[0].Definitions[1].ID,
	}; !reflect.DeepEqual(got, []string{"Spec", "spec", "Spec", "spec", "Spec", "spec"}) {
		t.Fatalf("raw observation ids = %#v", got)
	}
}

func TestDocumentAttributionsContextCancellationSeamsReturnNoPartialResult(t *testing.T) {
	t.Parallel()

	// Arrange.
	document := NewDocument(NewFrontmatter(), "Claim.[^source]\n\n[^source]: definition\n")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// Act and assert: pre-cancel.
	if got, err := document.AttributionsContext(canceled); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled AttributionsContext() = (%#v, %v)", got, err)
	}

	// Act and assert: cancel at the post-Goldmark parser seam.
	// Checks: attribution preflight, source-state preflight, post-source,
	// CollectFootnotes preflight; the fifth check follows parseMarkdown.
	if got, err := document.AttributionsContext(&cancelAfterErrChecksContext{allowed: 4}); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("parser-seam AttributionsContext() = (%#v, %v)", got, err)
	}
}

func TestDocumentAttributionsContextHighCardinalityFamiliesAreInterruptible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		frontmatter string
		body        string
	}{
		{
			name: "sources",
			frontmatter: func() string {
				var yaml strings.Builder
				yaml.WriteString("sources:\n")
				for index := 0; index < 2048; index++ {
					yaml.WriteString("  - {id: source-")
					yaml.WriteString(zeroPaddedDecimal(index, 4))
					yaml.WriteString(", resource: scope}\n")
				}
				return yaml.String()
			}(),
		},
		{
			name: "references and groups",
			body: func() string {
				var body strings.Builder
				for index := 0; index < 2048; index++ {
					body.WriteString(" [^ref-")
					body.WriteString(zeroPaddedDecimal(index, 4))
					body.WriteString("]")
				}
				return body.String()
			}(),
		},
		{
			name: "definitions",
			body: func() string {
				var body strings.Builder
				for index := 0; index < 2048; index++ {
					body.WriteString("[^def-")
					body.WriteString(zeroPaddedDecimal(index, 4))
					body.WriteString("]: value\n")
				}
				return body.String()
			}(),
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter := NewFrontmatter()
			if tt.frontmatter != "" {
				var err error
				frontmatter, err = ParseFrontmatter(tt.frontmatter)
				if err != nil {
					t.Fatal(err)
				}
			}
			document := NewDocument(frontmatter, tt.body)
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			want, err := document.AttributionsContext(probe)
			if err != nil || len(want) != 2048 {
				t.Fatalf("probe AttributionsContext() = (%d groups, %v)", len(want), err)
			}
			totalChecks := probe.checks.Load()
			cancelNearMaterialization := &cancelAfterErrChecksContext{allowed: totalChecks - 256}

			// Act.
			got, err := document.AttributionsContext(cancelNearMaterialization)

			// Assert.
			if got != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("high-cardinality AttributionsContext() = (%#v, %v), want no partial canceled result", got, err)
			}
			again, againErr := document.AttributionsContext(context.Background())
			if againErr != nil || !reflect.DeepEqual(again, want) || !reflect.DeepEqual(document.Attributions(), want) {
				t.Fatalf("background/legacy parity failed: againErr=%v contextEqual=%v legacyEqual=%v", againErr, reflect.DeepEqual(again, want), reflect.DeepEqual(document.Attributions(), want))
			}
		})
	}
}
