package mutation

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCollectMarkdownMigrationOwnership_ClaimsEntriesAndOpaqueRegions(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\nx-extra: keep\n---\n\n" +
		"A proven claim [1].\n\n" +
		"`inline [2]`\n\n" +
		"```text\nfenced [3]\n```\n\n" +
		"<span>raw [4]</span>\n\n" +
		"# Citations\n\n" +
		"[1] [Specification](https://example.test/spec)\n")

	// Act.
	ownership, err := CollectMarkdownMigrationOwnership(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatalf("CollectMarkdownMigrationOwnership() error = %v", err)
	}
	if got, want := len(ownership.CitationEntries), 1; got != want {
		t.Fatalf("len(CitationEntries) = %d, want %d", got, want)
	}
	entry := ownership.CitationEntries[0]
	if entry.Number != 1 || entry.Title != "Specification" || entry.Resource != "https://example.test/spec" {
		t.Fatalf("CitationEntries[0] = %#v", entry)
	}
	if got, want := len(ownership.NumericMarkers), 2; got != want ||
		ownership.NumericMarkers[0].Number != 1 ||
		ownership.NumericMarkers[1].Number != 4 {
		t.Fatalf("NumericMarkers = %#v, want parser prose markers 1 and 4", ownership.NumericMarkers)
	}
}

func TestCollectMarkdownMigrationOwnership_OnlyClaimsSemanticNumericMarkers(t *testing.T) {
	// Arrange.
	source := []byte("# Heading claim [1]\n\n" +
		"[2][full] [3][] [4] [nested [5]](https://example.test) ![6](image.png)\n\n" +
		"`inline [7]`\n\n```text\nfenced [8]\n```\n\n" +
		"[full]: https://example.test/full\n" +
		"[3]: https://example.test/collapsed\n" +
		"[4]: https://example.test/shortcut\n")

	// Act.
	ownership, err := CollectMarkdownMigrationOwnership(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatalf("CollectMarkdownMigrationOwnership() error = %v", err)
	}
	if got, want := len(ownership.NumericMarkers), 1; got != want ||
		ownership.NumericMarkers[0].Number != 1 {
		t.Fatalf("NumericMarkers = %#v, want only heading marker 1", ownership.NumericMarkers)
	}
}

func TestCollectMarkdownMigrationOwnership_ExcludesFrontmatterFootnoteDecoys(t *testing.T) {
	for _, test := range []struct{ name, newline string }{
		{name: "LF", newline: "\n"},
		{name: "CRLF", newline: "\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			newline := test.newline
			source := []byte("---" + newline +
				"type: Knowledge" + newline +
				"decoy_ref: \"[^source]\"" + newline +
				"decoy_definition: \"[^source]: https://invalid.test\"" + newline +
				"---" + newline + newline +
				"Body [^source]." + newline + newline +
				"[^source]: https://example.test" + newline)
			wantReference := []byte("[^source]")
			wantDefinition := []byte("[^source]: https://example.test" + newline)

			// Act.
			ownership, err := CollectMarkdownMigrationOwnership(context.Background(), source)

			// Assert.
			if err != nil {
				t.Fatalf("CollectMarkdownMigrationOwnership() error = %v", err)
			}
			if len(ownership.FootnoteReferences) != 1 ||
				len(ownership.FootnoteDefinitions) != 1 {
				t.Fatalf("ownership = %#v", ownership)
			}
			reference := ownership.FootnoteReferences[0]
			definition := ownership.FootnoteDefinitions[0]
			if !bytes.Equal(source[reference.Span.Start:reference.Span.End], wantReference) {
				t.Fatalf("reference span = %#v, bytes %q", reference.Span, source[reference.Span.Start:reference.Span.End])
			}
			if !bytes.Equal(source[definition.Span.Start:definition.Span.End], wantDefinition) {
				t.Fatalf("definition span = %#v, bytes %q", definition.Span, source[definition.Span.Start:definition.Span.End])
			}
			if got := string(source[definition.ContentSpan.Start:definition.ContentSpan.End]); got != "https://example.test" {
				t.Fatalf("definition content = %q", got)
			}
			frontmatterEnd := bytes.Index(source, []byte("---"+newline+newline)) + len("---"+newline+newline)
			if reference.Span.Start < frontmatterEnd || definition.Span.Start < frontmatterEnd {
				t.Fatalf("frontmatter decoy was claimed: ref=%#v def=%#v", reference.Span, definition.Span)
			}
		})
	}
}

func TestRewriteLegacyCitations_RequiresExplicitMappingAndPreservesUnownedBytes(t *testing.T) {
	// Arrange.
	source := []byte("---\r\ntype: Knowledge\r\nx-extra: keep\r\n---\r\n\r\n" +
		"Claim [1].\r\n\r\n" +
		"<!-- untouched -->\r\n\r\n" +
		"# Citations\r\n\r\n" +
		"[1] [Specification](https://example.test/spec)\r\n")
	mappings := []legacyCitationMapping{{
		LegacyNumber: 1,
		LegacyEntry:  "[Specification](https://example.test/spec)",
		SourceID:     "spec",
		Title:        "Specification",
		Resource:     "https://example.test/spec",
	}}

	// Act.
	updated, err := rewriteLegacyCitationsContext(context.Background(), source, mappings)

	// Assert.
	if err != nil {
		t.Fatalf("rewriteLegacyCitationsContext() error = %v", err)
	}
	for _, want := range [][]byte{
		[]byte("x-extra: keep\r\n"),
		[]byte("Claim [^spec].\r\n"),
		[]byte("<!-- untouched -->\r\n"),
		[]byte("[^spec]: [Specification](https://example.test/spec)\r\n"),
	} {
		if !bytes.Contains(updated, want) {
			t.Fatalf("updated document does not contain %q:\n%s", want, updated)
		}
	}
	if bytes.Contains(updated, []byte("# Citations")) || bytes.Contains(updated, []byte("Claim [1]")) {
		t.Fatalf("legacy citation presentation remains:\n%s", updated)
	}
}

func TestRewriteLegacyCitations_BlocksNormalizedFootnoteLabelCollisions(t *testing.T) {
	// Arrange.
	source := []byte("Existing [^Spec].\n\n" +
		"[^spec]: existing content\n\n" +
		"Claim [1].\n\n" +
		"# Citations\n\n" +
		"[1] https://example.test/spec\n")
	mappings := []legacyCitationMapping{{
		LegacyNumber: 1,
		LegacyEntry:  "https://example.test/spec",
		SourceID:     "Spec",
		Resource:     "https://example.test/spec",
	}}

	// Act.
	updated, err := rewriteLegacyCitationsContext(context.Background(), source, mappings)

	// Assert.
	if updated != nil {
		t.Fatalf("updated = %q, want nil", updated)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) ||
		presentation.Code != "normalized_footnote_label_collision" ||
		presentation.Location.End <= presentation.Location.Start {
		t.Fatalf("error = %#v, want exact normalized_footnote_label_collision", err)
	}
}

func TestRewriteLegacyCitations_ComparesEntireExistingFootnoteDefinition(t *testing.T) {
	tests := []struct {
		name         string
		newline      string
		continuation string
	}{
		{name: "LF spaces", newline: "\n", continuation: "    continued provenance\n"},
		{name: "CRLF spaces", newline: "\r\n", continuation: "    continued provenance\r\n"},
		{name: "tab", newline: "\n", continuation: "\tcontinued provenance\n"},
		{name: "blank continuation", newline: "\n", continuation: "    first line\n\n    final line\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := []byte("Claim [1]." + test.newline + test.newline +
				"[^source]: [Spec](https://example.test/spec)" + test.newline +
				test.continuation + test.newline +
				"# Citations" + test.newline + test.newline +
				"[1] [Spec](https://example.test/spec)" + test.newline)
			mappings := []legacyCitationMapping{{
				LegacyNumber: 1,
				LegacyEntry:  "[Spec](https://example.test/spec)",
				SourceID:     "source",
			}}

			// Act.
			updated, err := rewriteLegacyCitationsContext(context.Background(), source, mappings)

			// Assert.
			if updated != nil {
				t.Fatalf("updated = %q, want nil", updated)
			}
			var presentation *PresentationError
			if !errors.As(err, &presentation) ||
				presentation.Code != "conflicting_footnote_definition" ||
				presentation.Location.End <= presentation.Location.Start {
				t.Fatalf("error = %#v, want exact conflicting_footnote_definition", err)
			}
		})
	}
}

func TestRewriteLegacyCitations_ReusesExactExistingFootnoteDefinitionWithoutDuplicate(t *testing.T) {
	// Arrange.
	const definition = "[^source]: [Spec](https://example.test/spec)\n"
	source := []byte("Claim [1].\n\n" +
		definition + "\n" +
		"# Citations\n\n" +
		"[1] [Spec](https://example.test/spec)\n")
	mappings := []legacyCitationMapping{{
		LegacyNumber: 1,
		LegacyEntry:  "[Spec](https://example.test/spec)",
		SourceID:     "source",
	}}

	// Act.
	updated, err := rewriteLegacyCitationsContext(context.Background(), source, mappings)

	// Assert.
	if err != nil {
		t.Fatalf("rewriteLegacyCitationsContext() error = %v", err)
	}
	if count := bytes.Count(updated, []byte(definition)); count != 1 {
		t.Fatalf("definition count = %d, want 1:\n%s", count, updated)
	}
	if !bytes.Contains(updated, []byte("Claim [^source].")) {
		t.Fatalf("numeric marker was not rewritten:\n%s", updated)
	}
}

func TestRewriteLegacyCitations_IgnoresFootnoteShapesOutsideTopLevelOwnership(t *testing.T) {
	// Arrange.
	opaque := "- [^source]: list-owned\n\n" +
		"> [^source]: quote-owned\n\n" +
		"    [^source]: code-owned\n\n" +
		"```markdown\n[^source]: fenced-owned\n```\n\n" +
		"<div>[^source]: html-owned</div>\n\n"
	source := []byte(opaque +
		"Claim [1].\n\n" +
		"# Citations\n\n" +
		"[1] [Spec](https://example.test/spec)\n")
	mappings := []legacyCitationMapping{{
		LegacyNumber: 1,
		LegacyEntry:  "[Spec](https://example.test/spec)",
		SourceID:     "source",
	}}

	// Act.
	updated, err := rewriteLegacyCitationsContext(context.Background(), source, mappings)

	// Assert.
	if err != nil {
		t.Fatalf("rewriteLegacyCitationsContext() error = %v", err)
	}
	if !bytes.Contains(updated, []byte(opaque)) {
		t.Fatalf("non-top-level footnote shapes changed:\n%s", updated)
	}
	if count := bytes.Count(updated, []byte("[^source]: [Spec](https://example.test/spec)\n")); count != 1 {
		t.Fatalf("generated top-level definition count = %d, want 1:\n%s", count, updated)
	}
}

func TestRewriteLegacyCitations_RewritesMarkerBeforeRawHTMLTag(t *testing.T) {
	// Arrange.
	source := []byte("Claim [1] <span>kept verbatim</span>.\n\n" +
		"# Citations\n\n" +
		"[1] https://example.test/one\n")
	mappings := []legacyCitationMapping{{
		LegacyNumber: 1,
		LegacyEntry:  "https://example.test/one",
		SourceID:     "one",
		Resource:     "https://example.test/one",
	}}

	// Act.
	updated, err := rewriteLegacyCitationsContext(context.Background(), source, mappings)

	// Assert.
	if err != nil {
		t.Fatalf("rewriteLegacyCitationsContext() error = %v", err)
	}
	if want := []byte("Claim [^one] <span>kept verbatim</span>."); !bytes.Contains(updated, want) {
		t.Fatalf("updated document does not contain %q:\n%s", want, updated)
	}
}

func TestRewriteLegacyCitations_PreservesParserBackedDestinationSemantics(t *testing.T) {
	tests := []struct {
		name          string
		legacyEntry   string
		wantTitle     string
		wantResource  string
		wantLinkTitle string
		wantFootnote  string
	}{
		{
			name:          "angle destination and title",
			legacyEntry:   `[Angle](<https://example.test/a b> "Angle title")`,
			wantTitle:     "Angle",
			wantResource:  "https://example.test/a b",
			wantLinkTitle: "Angle title",
			wantFootnote:  `[^source]: [Angle](<https://example.test/a b> "Angle title")`,
		},
		{
			name:          "nested parentheses and escaped title",
			legacyEntry:   `[Nested](https://example.test/a(b(c)) "A \"quote\"")`,
			wantTitle:     "Nested",
			wantResource:  "https://example.test/a(b(c))",
			wantLinkTitle: `A "quote"`,
			wantFootnote:  `[^source]: [Nested](https://example.test/a\(b\(c\)\) "A \"quote\"")`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := []byte("Claim [1].\n\n# Citations\n\n[1] " + test.legacyEntry + "\n")
			mappings := []legacyCitationMapping{{
				LegacyNumber: 1,
				LegacyEntry:  test.legacyEntry,
				SourceID:     "source",
			}}

			// Act.
			ownership, ownershipErr := CollectMarkdownMigrationOwnership(context.Background(), source)
			updated, rewriteErr := rewriteLegacyCitationsContext(context.Background(), source, mappings)

			// Assert.
			if ownershipErr != nil {
				t.Fatalf("CollectMarkdownMigrationOwnership() error = %v", ownershipErr)
			}
			if got, want := len(ownership.CitationEntries), 1; got != want {
				t.Fatalf("len(CitationEntries) = %d, want %d", got, want)
			}
			entry := ownership.CitationEntries[0]
			if entry.Title != test.wantTitle || entry.Resource != test.wantResource || entry.LinkTitle != test.wantLinkTitle {
				t.Fatalf("CitationEntries[0] = %#v", entry)
			}
			if rewriteErr != nil {
				t.Fatalf("rewriteLegacyCitationsContext() error = %v", rewriteErr)
			}
			if !bytes.Contains(updated, []byte(test.wantFootnote)) {
				t.Fatalf("updated document does not contain %q:\n%s", test.wantFootnote, updated)
			}
		})
	}
}

func TestRewriteLegacyCitations_UnresolvedMarkerFailsClosed(t *testing.T) {
	// Arrange.
	source := []byte("Claim [2].\n\n# Citations\n\n[1] https://example.test/one\n")
	mappings := []legacyCitationMapping{{
		LegacyNumber: 1,
		LegacyEntry:  "https://example.test/one",
		SourceID:     "one",
		Resource:     "https://example.test/one",
	}}

	// Act.
	updated, err := rewriteLegacyCitationsContext(context.Background(), source, mappings)

	// Assert.
	if updated != nil {
		t.Fatalf("updated = %q, want nil", updated)
	}
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("error = %v, want ErrUnsupportedPresentation", err)
	}
}

func TestRewriteLegacyCitations_BlocksBeforeTouchingOpaqueBytesInsideSection(t *testing.T) {
	// Arrange.
	source := []byte("Claim [1].\n\n# Citations\n\n" +
		"[1] https://example.test/one\n\n" +
		"```text\nkeep fenced bytes\n```\n")
	mappings := []legacyCitationMapping{{
		LegacyNumber: 1,
		LegacyEntry:  "https://example.test/one",
		SourceID:     "one",
		Resource:     "https://example.test/one",
	}}

	// Act.
	updated, err := rewriteLegacyCitationsContext(context.Background(), source, mappings)

	// Assert.
	if updated != nil {
		t.Fatalf("updated = %q, want nil", updated)
	}
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("error = %v, want ErrUnsupportedPresentation", err)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Code != "opaque_citations_content" {
		t.Fatalf("error = %#v, want opaque_citations_content", err)
	}
}

func TestRewriteLegacyCitations_BlocksOpaqueBytesIntersectingEntries(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		legacyEntry string
		number      uint64
	}{
		{
			name:        "numbered inline code",
			source:      "Claim [1].\n\n# Citations\n\n[1] https://example.test `opaque`\n",
			legacyEntry: "https://example.test `opaque`",
			number:      1,
		},
		{
			name:        "bullet raw html",
			source:      "# Citations\n\n- https://example.test <span>opaque</span>\n",
			legacyEntry: "https://example.test <span>opaque</span>",
		},
		{
			name:        "numbered nested fence",
			source:      "Claim [1].\n\n# Citations\n\n[1] https://example.test\n\n  ```text\n  opaque\n  ```\n",
			legacyEntry: "https://example.test\n\n  ```text\n  opaque\n  ```",
			number:      1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			mappings := []legacyCitationMapping{{
				LegacyNumber: test.number,
				LegacyEntry:  test.legacyEntry,
				SourceID:     "source",
				Resource:     "https://example.test",
			}}

			// Act.
			updated, err := rewriteLegacyCitationsContext(context.Background(), []byte(test.source), mappings)

			// Assert.
			if updated != nil {
				t.Fatalf("updated = %q, want nil", updated)
			}
			var presentation *PresentationError
			if !errors.As(err, &presentation) || presentation.Code != "opaque_citations_content" {
				t.Fatalf("error = %#v, want opaque_citations_content", err)
			}
		})
	}
}

func TestRewriteLegacyCitations_BlocksResidualUnknownLines(t *testing.T) {
	// Arrange.
	source := []byte("Claim [1].\n\n# Citations\n\n" +
		"[1] https://example.test/one\n\n" +
		"unknown future syntax\n")
	mappings := []legacyCitationMapping{{
		LegacyNumber: 1,
		LegacyEntry:  "https://example.test/one",
		SourceID:     "one",
		Resource:     "https://example.test/one",
	}}

	// Act.
	updated, err := rewriteLegacyCitationsContext(context.Background(), source, mappings)

	// Assert.
	if updated != nil {
		t.Fatalf("updated = %q, want nil", updated)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Code != "unowned_citations_content" {
		t.Fatalf("error = %#v, want unowned_citations_content", err)
	}
}

func TestResolveCitationMappings_RequiresEverySuppliedIdentityAndResolvesPairs(t *testing.T) {
	// Arrange.
	entries := []MarkdownCitationEntry{
		{Number: 1, Raw: "same", Span: SourceSpan{Start: 0, End: 4}, ContentSpan: SourceSpan{Start: 1, End: 4}},
		{Number: 2, Raw: "same", Span: SourceSpan{Start: 5, End: 9}, ContentSpan: SourceSpan{Start: 6, End: 9}},
	}
	pairs := []legacyCitationMapping{
		{LegacyNumber: 1, LegacyEntry: "same", SourceID: "one", Resource: "one.test"},
		{LegacyNumber: 2, LegacyEntry: "same", SourceID: "two", Resource: "two.test"},
	}
	wrongPair := []legacyCitationMapping{
		{LegacyNumber: 2, LegacyEntry: "same", SourceID: "one", Resource: "one.test"},
		{LegacyNumber: 1, LegacyEntry: "different", SourceID: "two", Resource: "two.test"},
	}

	// Act.
	resolved, pairErr := resolveCitationMappingsContext(context.Background(), entries, pairs)
	_, wrongErr := resolveCitationMappingsContext(context.Background(), entries, wrongPair)

	// Assert.
	if pairErr != nil {
		t.Fatalf("resolveCitationMappingsContext(pairs) error = %v", pairErr)
	}
	if len(resolved) != 2 || resolved[0].SourceID != "one" || resolved[1].SourceID != "two" {
		t.Fatalf("resolved pairs = %#v", resolved)
	}
	if !errors.Is(wrongErr, ErrUnsupportedPresentation) {
		t.Fatalf("wrong-pair error = %v, want ErrUnsupportedPresentation", wrongErr)
	}
}

func TestCollectMarkdownMigrationOwnership_MultipleCitationsSectionsFailClosed(t *testing.T) {
	// Arrange.
	source := []byte("# Citations\n\n[1] https://one.test\n\n# Body\n\ntext\n\n# Citations\n\n[2] https://two.test\n")

	// Act.
	ownership, err := CollectMarkdownMigrationOwnership(context.Background(), source)

	// Assert.
	if len(ownership.Sections) != 0 {
		t.Fatalf("ownership = %#v, want zero value", ownership)
	}
	if !errors.Is(err, ErrAmbiguousPresentation) {
		t.Fatalf("error = %v, want ErrAmbiguousPresentation", err)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) {
		t.Fatalf("error = %v, want *PresentationError", err)
	}
	wantLocation := SourceSpan{
		Start: 0,
		End:   strings.LastIndex(string(source), "# Citations") + len("# Citations\n"),
	}
	if presentation.Code != "multiple_citations_sections" || presentation.Location != wantLocation {
		t.Fatalf("PresentationError = %#v, want code multiple_citations_sections at %#v", presentation, wantLocation)
	}
}

func TestCollectMarkdownMigrationOwnership_ComputationBoundaryIsTopLevelAndExact(t *testing.T) {
	// Arrange.
	source := []byte("# Computation\n\n```sql\nSELECT 1;\n```\n\n## Explanation\n\nNarrative SQL is not owned.\n")

	// Act.
	ownership, err := CollectMarkdownMigrationOwnership(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatalf("CollectMarkdownMigrationOwnership() error = %v", err)
	}
	if got, want := len(ownership.Computation.Sections), 1; got != want {
		t.Fatalf("len(Computation.Sections) = %d, want %d", got, want)
	}
	if got, want := len(ownership.Computation.Fences), 1; got != want {
		t.Fatalf("len(Computation.Fences) = %d, want %d", got, want)
	}
	fence := ownership.Computation.Fences[0]
	if got := string(source[fence.Start:fence.End]); got != "```sql\nSELECT 1;\n```\n" {
		t.Fatalf("owned fence = %q", got)
	}
}
