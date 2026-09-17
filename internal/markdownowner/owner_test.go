package markdownowner

import (
	"reflect"
	"strings"
	"testing"
)

func TestProseLinesOwnsCommonMarkCodeForms(t *testing.T) {
	t.Parallel()

	// Arrange.
	markdown := "one `x [one](one.md)` [live](live.md)\r\n" +
		"two ``x ` [two](two.md)`` [live2](live2.md)\r\n" +
		"escaped \\` [escaped](escaped.md)\r\n" +
		"\r\n" +
		"    [indented](indented.md)\r\n" +
		"````lang\r\n[three](three.md)\r\n```\r\n[four](four.md)\r\n````\r\n"
	markdown += "> ```md\n> [quoted](quoted.md)\n> ```\n" +
		"- item\n\n  ~~~md\n  [listed](listed.md)\n  ~~~\n"

	// Act.
	lines := ProseLines(markdown)

	// Assert.
	joined := strings.Join(lines, "\n")
	for _, hidden := range []string{"one.md", "two.md", "indented.md", "three.md", "four.md", "quoted.md", "listed.md"} {
		if strings.Contains(joined, hidden) {
			t.Fatalf("ProseLines() retained code-owned %q:\n%s", hidden, joined)
		}
	}
	for _, visible := range []string{"live.md", "live2.md", "escaped.md"} {
		if !strings.Contains(joined, visible) {
			t.Fatalf("ProseLines() hid prose-owned %q:\n%s", visible, joined)
		}
	}
}

func TestInspectComputationRequiresOneClosedSectionOwnedFence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		markdown string
		want     bool
		content  string
	}{
		{name: "direct closed", markdown: "# Computation\n\n```sql\nSELECT 1;\n```\n", want: true, content: "SELECT 1;\n"},
		{name: "prose intervenes", markdown: "# Computation\n\nExplain.\n\n```sql\nSELECT 1;\n```\n", want: true, content: "SELECT 1;\n"},
		{name: "blockquote container excluded", markdown: "# Computation\n\n> ````sql\n> SELECT 1;\n> ```\n> ````\n"},
		{name: "list container excluded", markdown: "# Computation\n\n- explanation\n\n  ~~~~sql\n  SELECT 1;\n  ~~~~\n"},
		{name: "fence before section ignored", markdown: "```\noutside\n```\n# Computation\n\n```\ninside\n```\n", want: true, content: "inside\n"},
		{name: "next h1 ends section", markdown: "# Computation\n\n# Notes\n\n```\noutside\n```\n"},
		{name: "unclosed", markdown: "# Computation\n\n```sql\nSELECT 1;\n"},
		{name: "nested heading", markdown: "## Computation\n\n```sql\nSELECT 1;\n```\n"},
		{name: "lowercase heading", markdown: "# computation\n\n```sql\nSELECT 1;\n```\n"},
		{name: "uppercase heading", markdown: "# COMPUTATION\n\n```sql\nSELECT 1;\n```\n"},
		{name: "multiple", markdown: "# Computation\n\n```\none\n```\n# Computation\n\n```\ntwo\n```\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Act.
			inspection := InspectComputation(tt.markdown)
			ok := inspection.State == ComputationInline
			var fence Fence
			if ok {
				fence = inspection.DirectFences[0]
			}

			// Assert.
			if ok != tt.want {
				t.Fatalf("InspectComputation() state = %q, want inline=%v (%#v)", inspection.State, tt.want, inspection)
			}
			if ok && !reflect.DeepEqual(fence.Content, tt.content) {
				t.Fatalf("Content = %q, want %q", fence.Content, tt.content)
			}
		})
	}
}

func TestComputationSectionsRetainUnclosedAndMultipleFenceSpans(t *testing.T) {
	t.Parallel()

	// Arrange.
	markdown := "# Computation\n\n````sql\none\n````\n\n~~~sql\ntwo\n"

	// Act.
	sections := InspectComputation(markdown).Sections

	// Assert.
	if len(sections) != 1 || len(sections[0].Fences) != 2 {
		t.Fatalf("InspectComputation().Sections = %#v", sections)
	}
	if !sections[0].Fences[0].Closed {
		t.Fatalf("first fence is not closed: %#v", sections[0].Fences[0])
	}
	if sections[0].Fences[1].Closed || sections[0].Fences[1].End != len(markdown) {
		t.Fatalf("unclosed fence = %#v", sections[0].Fences[1])
	}
	if sections[0].Heading.Start != 0 || sections[0].Span.End != len(markdown) {
		t.Fatalf("section spans = %#v", sections[0])
	}
}
