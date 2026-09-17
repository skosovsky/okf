package markdownowner

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCollectMarkdownMigrationOwnershipUsesFullFileSpans(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := []byte("---\r\ntype: Note\r\n---\r\n" +
		"Claim [1].[^Spec]\r\n\r\n" +
		"# Citations\r\n[1] [Specification](https://example.test/spec)\r\n\r\n" +
		"# Notes\r\n" +
		"[^spec]: Existing\r\n\r\n" +
		"# Computation\r\n```sql\r\nSELECT 1;\r\n```\r\n")

	// Act.
	ownership, err := CollectMarkdownMigrationOwnership(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if ownership.CitationsSectionIndex < 0 || len(ownership.CitationEntries) != 1 || len(ownership.NumericMarkers) != 1 {
		t.Fatalf("citation ownership = %#v", ownership)
	}
	if len(ownership.FootnoteReferences) != 1 || len(ownership.FootnoteDefinitions) != 1 || len(ownership.LabelCollisions) != 1 {
		t.Fatalf("footnote ownership = %#v", ownership)
	}
	if len(ownership.Computation.Sections) != 1 || len(ownership.Computation.Fences) != 1 {
		t.Fatalf("computation ownership = %#v", ownership.Computation)
	}
	entry := ownership.CitationEntries[0]
	if string(source[entry.ContentSpan.Start:entry.ContentSpan.End]) != "[Specification](https://example.test/spec)" {
		t.Fatalf("ContentSpan = %q", source[entry.ContentSpan.Start:entry.ContentSpan.End])
	}
}

func TestCollectMarkdownMigrationOwnershipFenceExcludesFollowingBlankLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "LF following blank", source: "# Computation\n\n```sql\nSELECT 1;\n```\n\n## Explanation\n", want: "```sql\nSELECT 1;\n```\n"},
		{name: "CRLF following blank", source: "# Computation\r\n\r\n```sql\r\nSELECT 1;\r\n```\r\n\r\n## Explanation\r\n", want: "```sql\r\nSELECT 1;\r\n```\r\n"},
		{name: "no final newline", source: "# Computation\n\n```sql\nSELECT 1;\n```", want: "```sql\nSELECT 1;\n```"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := []byte(test.source)

			// Act.
			ownership, err := CollectMarkdownMigrationOwnership(context.Background(), source)

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if len(ownership.Computation.Fences) != 1 {
				t.Fatalf("Fences = %#v", ownership.Computation.Fences)
			}
			fence := ownership.Computation.Fences[0]
			if got := string(source[fence.Start:fence.End]); got != test.want {
				t.Fatalf("fence span = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNumericMarkersUseASTOwnedTextAndExcludeLinkLabels(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := []byte("# Heading [1]\n\n" +
		"Prose [2] and `<code [3]>` <span data-x=\"[4]\">outside [5]</span>.\n\n" +
		"[inline [6]](https://example.test/(nested))\n" +
		"[7][target] [8][] [9]\n" +
		"![image [10]](image.png) <https://example.test/[11]>\n\n" +
		"[target]: https://example.test \"Target\"\n" +
		"[8]: https://example.test/collapsed\n" +
		"[9]: https://example.test/shortcut\n")

	// Act.
	ownership, err := CollectMarkdownMigrationOwnership(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	var numbers []uint64
	for _, marker := range ownership.NumericMarkers {
		numbers = append(numbers, marker.Number)
	}
	want := []uint64{1, 2, 5}
	if !reflect.DeepEqual(numbers, want) {
		t.Fatalf("NumericMarkers() = %v, want %v: %#v", numbers, want, ownership.NumericMarkers)
	}
}

func TestCitationSectionProjectionOwnsEntriesResidualAndOpaque(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := []byte("---\r\ntype: Note\r\n---\r\n" +
		"# Citations\r\n" +
		"- [Angle](<https://example.test/a b> \"A \\\"quote\\\"\")\r\n" +
		"- [Nested](https://example.test/a_(b))\r\n\r\n" +
		"Unowned prose\r\n\r\n" +
		"```md\r\nopaque\r\n```\r\n")

	// Act.
	projection, err := CollectCitationSectionProjection(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Found || len(projection.Entries) != 2 || len(projection.Residual) != 1 || len(projection.Opaque) != 1 {
		t.Fatalf("projection = %#v", projection)
	}
	if projection.Entries[0].Resource != "https://example.test/a b" ||
		projection.Entries[0].LinkTitle != `A "quote"` ||
		projection.Entries[1].Resource != "https://example.test/a_(b)" {
		t.Fatalf("entries = %#v", projection.Entries)
	}
	if string(source[projection.Residual[0].Start:projection.Residual[0].End]) != "Unowned prose" {
		t.Fatalf("Residual = %q", source[projection.Residual[0].Start:projection.Residual[0].End])
	}
	if !strings.Contains(string(source[projection.Opaque[0].Start:projection.Opaque[0].End]), "opaque") {
		t.Fatalf("Opaque = %q", source[projection.Opaque[0].Start:projection.Opaque[0].End])
	}
}

func TestCitationSectionProjectionReportsOpaqueInsideEntriesBeforeResidual(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := []byte("# Citations\n" +
		"[1] `numbered opaque`\n\n" +
		"- `bullet opaque`\n" +
		"- <span>raw opaque</span>\n" +
		"- nested opaque\n\n" +
		"  ```text\n" +
		"  fenced opaque\n" +
		"  ```\n")

	// Act.
	projection, err := CollectCitationSectionProjection(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entries) != 4 {
		t.Fatalf("Entries = %#v", projection.Entries)
	}
	if len(projection.Residual) != 0 {
		t.Fatalf("Residual = %#v, want none", projection.Residual)
	}
	var opaqueText strings.Builder
	for _, opaque := range projection.Opaque {
		opaqueText.Write(source[opaque.Start:opaque.End])
		for _, residual := range projection.Residual {
			if spansOverlap(opaque, residual) {
				t.Fatalf("Opaque %#v overlaps Residual %#v", opaque, residual)
			}
		}
	}
	for _, want := range []string{"numbered opaque", "bullet opaque", "<span>", "fenced opaque"} {
		if !strings.Contains(opaqueText.String(), want) {
			t.Fatalf("Opaque text = %q, missing %q", opaqueText.String(), want)
		}
	}
	for _, opaque := range projection.Opaque {
		intersectsEntry := false
		for _, entry := range projection.Entries {
			intersectsEntry = intersectsEntry || spansOverlap(opaque, entry.Span)
		}
		if !intersectsEntry {
			t.Fatalf("Opaque %#v does not intersect an entry", opaque)
		}
	}
}

func TestCitationDestinationsAreEntryLocalAcrossParagraphAndListCorpus(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := []byte("# Citations\n" +
		"[1] [Angle](<https://example.test/a b> \"A \\\"quote\\\"\")\n" +
		"[2] <https://example.test/two>\n\n" +
		"- ![Image](image.png)\n" +
		"- [Nested](https://example.test/a_(b))\n")

	// Act.
	projection, err := CollectCitationSectionProjection(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entries) != 4 {
		t.Fatalf("Entries = %#v", projection.Entries)
	}
	if first := projection.Entries[0]; first.Resource != "https://example.test/a b" ||
		first.LinkTitle != `A "quote"` || first.Title != "Angle" {
		t.Fatalf("first entry = %#v", first)
	}
	if second := projection.Entries[1]; second.Resource != "https://example.test/two" ||
		second.Title != "https://example.test/two" {
		t.Fatalf("second entry = %#v", second)
	}
	if image := projection.Entries[2]; image.Resource != "" || image.Title != "" {
		t.Fatalf("image entry leaked a destination: %#v", image)
	}
	if nested := projection.Entries[3]; nested.Resource != "https://example.test/a_(b)" ||
		nested.Title != "Nested" {
		t.Fatalf("nested entry = %#v", nested)
	}
}

func TestInlineTokenOwnershipExcludesFullParserOwnedLinkSpans(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := []byte("outside [1] [^outside]\n\n" +
		"[label [2] [^label]](<https://x.test/[3]/[^destination]>) " +
		"![image [4] [^image]](<https://x.test/[5]/[^image-destination]>) " +
		"<https://example.test/auto>\n")

	// Act.
	ownership, err := CollectMarkdownMigrationOwnership(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if got := ownership.NumericMarkers; len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("NumericMarkers = %#v, want only outside marker", got)
	}
	if got := ownership.FootnoteReferences; len(got) != 1 || got[0].Label != "outside" {
		t.Fatalf("FootnoteReferences = %#v, want only outside reference", got)
	}

	document := parseMarkdown(source)
	paragraph := document.LastChild()
	excluded := inlineExcludedSpans(source, paragraph)
	var excludedText []string
	for _, span := range excluded {
		excludedText = append(excludedText, string(source[span.Start:span.End]))
	}
	want := []string{
		"[label [2] [^label]](<https://x.test/[3]/[^destination]>)",
		"![image [4] [^image]](<https://x.test/[5]/[^image-destination]>)",
		"<https://example.test/auto>",
	}
	if !reflect.DeepEqual(excludedText, want) {
		t.Fatalf("inlineExcludedSpans() = %#v, want %#v", excludedText, want)
	}
}

func TestCollectMarkdownMigrationOwnershipRejectsDuplicateCitations(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := []byte("# Citations\n- one\n# Citations\n- two\n")

	// Act.
	_, err := CollectMarkdownMigrationOwnership(context.Background(), source)

	// Assert.
	var ownershipErr *OwnershipError
	if !errors.As(err, &ownershipErr) || ownershipErr.Code != "multiple_citations_sections" || !ownershipErr.Ambiguous {
		t.Fatalf("CollectMarkdownMigrationOwnership() error = %#v", err)
	}
}

func TestCollectMarkdownMigrationOwnershipReportsExactTypedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    []byte
		code      string
		ambiguous bool
	}{
		{name: "invalid utf8", source: []byte{'a', 0xff, 'b'}, code: "invalid_utf8"},
		{name: "duplicate number", source: []byte("# Citations\n[1] one\n[1] two\n"), code: "duplicate_citation_number", ambiguous: true},
		{name: "unsupported entry", source: []byte("# Citations\nnot an entry\n"), code: "unsupported_citation_entry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Act.
			_, err := CollectMarkdownMigrationOwnership(context.Background(), tt.source)

			// Assert.
			var ownershipErr *OwnershipError
			if !errors.As(err, &ownershipErr) || ownershipErr.Code != tt.code || ownershipErr.Ambiguous != tt.ambiguous {
				t.Fatalf("error = %#v, want code=%q ambiguous=%v", err, tt.code, tt.ambiguous)
			}
			if ownershipErr.Span.End <= ownershipErr.Span.Start {
				t.Fatalf("error span = %#v, want non-empty", ownershipErr.Span)
			}
		})
	}
}
