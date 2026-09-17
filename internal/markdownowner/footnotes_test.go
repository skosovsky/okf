package markdownowner

import (
	"context"
	"reflect"
	"testing"
)

func TestCollectFootnotesUsesParserOwnedInlineBoundaries(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := []byte("Outside [^outside]. Exact shortcut [^exact].\n\n" +
		"[inline [^inline]](https://example.test/inline)\n" +
		"[full [^full]][target]\n" +
		"[^collapsed][]\n" +
		"![image [^image]](image.png)\n" +
		"<https://example.test/auto>\n" +
		"`inline code [^code]`\n\n" +
		"```text\n[^fenced]\n[^fenced]: hidden\n```\n\n" +
		"[target]: https://example.test/[^definition]\n" +
		"[^collapsed]: collapsed link definition\n" +
		"[^outside]: outside definition\n" +
		"[^exact]: exact definition\n")

	// Act.
	projection, err := CollectFootnotes(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	var references []string
	for _, reference := range projection.References {
		references = append(references, reference.Label)
		if got := string(source[reference.Span.Start:reference.Span.End]); got != "[^"+reference.Label+"]" {
			t.Fatalf("reference span = %q for %#v", got, reference)
		}
	}
	if want := []string{"outside", "exact"}; !reflect.DeepEqual(references, want) {
		t.Fatalf("References = %#v, want %#v", references, want)
	}
	var definitions []string
	for _, definition := range projection.Definitions {
		definitions = append(definitions, definition.Label)
	}
	if want := []string{"collapsed", "outside", "exact"}; !reflect.DeepEqual(definitions, want) {
		t.Fatalf("Definitions = %#v, want %#v", definitions, want)
	}
}

func TestCollectFootnotesOwnsCompleteMultilineDefinitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      string
		wantSpan    string
		wantContent string
	}{
		{
			name:        "LF spaces",
			source:      "[^lf]: first\n    second\n    third\noutside [^outside]\n",
			wantSpan:    "[^lf]: first\n    second\n    third\n",
			wantContent: "first\n    second\n    third",
		},
		{
			name:        "CRLF spaces",
			source:      "[^crlf]: first\r\n    second\r\noutside [^outside]\r\n",
			wantSpan:    "[^crlf]: first\r\n    second\r\n",
			wantContent: "first\r\n    second",
		},
		{
			name:        "tab continuation",
			source:      "[^tab]: first\n\tsecond\noutside [^outside]\n",
			wantSpan:    "[^tab]: first\n\tsecond\n",
			wantContent: "first\n\tsecond",
		},
		{
			name:        "blank before continuation",
			source:      "[^blank]: first\n\n    second\noutside [^outside]\n",
			wantSpan:    "[^blank]: first\n\n    second\n",
			wantContent: "first\n\n    second",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			source := []byte(tt.source)

			// Act.
			projection, err := CollectFootnotes(context.Background(), source)

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if len(projection.Definitions) != 1 {
				t.Fatalf("Definitions = %#v, want one", projection.Definitions)
			}
			definition := projection.Definitions[0]
			if got := string(source[definition.Span.Start:definition.Span.End]); got != tt.wantSpan {
				t.Fatalf("definition span = %q, want %q", got, tt.wantSpan)
			}
			if got := string(source[definition.ContentSpan.Start:definition.ContentSpan.End]); got != tt.wantContent {
				t.Fatalf("content span = %q, want %q", got, tt.wantContent)
			}
			if definition.Content != tt.wantContent {
				t.Fatalf("Content = %q, want %q", definition.Content, tt.wantContent)
			}
			if got := []string{projection.References[0].Label}; !reflect.DeepEqual(got, []string{"outside"}) {
				t.Fatalf("References = %#v, want outside only", projection.References)
			}
		})
	}
}

func TestCollectFootnotesRejectsDefinitionsOwnedByOtherBlocks(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := []byte("- [^list]: list-owned\n" +
		"> [^quote]: quote-owned\n" +
		"    [^code]: indented-code-owned\n" +
		"\t[^tab-code]: tab-code-owned\n" +
		"[^plain]: plain\n" +
		"outside [^outside]\n")

	// Act.
	projection, err := CollectFootnotes(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	var definitions []string
	for _, definition := range projection.Definitions {
		definitions = append(definitions, definition.Label)
	}
	if want := []string{"plain"}; !reflect.DeepEqual(definitions, want) {
		t.Fatalf("Definitions = %#v, want %#v", definitions, want)
	}
}
