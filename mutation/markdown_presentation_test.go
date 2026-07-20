package mutation

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"sort"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"github.com/yuin/goldmark/ast"
)

func TestCollectMarkdownDestinations_GoldmarkBackedSpans(t *testing.T) {
	// Arrange
	source := []byte("---\r\ntype: thing\r\n---\r\n[link](<one(a).md#x> \"title\") ![image](two.md)\r\n[full][r] [collapsed][] [shortcut]\r\n\r\n[r]: <three.md> \"title\"\r\n[collapsed]: four.md\r\n[shortcut]: five.md\r\n`[code](six.md)`\r\n```md\r\n[fenced](seven.md)\r\n```\r\n")

	// Act
	destinations, err := collectMarkdownDestinations(source)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		got = append(got, string(source[destination.Span.Start:destination.Span.End]))
	}
	want := []string{"<one(a).md#x>", "two.md", "<three.md>", "four.md", "five.md"}
	if !equalStrings(got, want) {
		t.Fatalf("spans = %q, want %q", got, want)
	}
}

func TestRewriteMarkdownDestinations_PreservesEverythingOutsideSpans(t *testing.T) {
	// Arrange
	source := []byte("---\ntype: thing\n---\n[one](old.md) [ref]: <old.md#part>\n")
	before, err := collectMarkdownDestinations(source)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	updated, err := rewriteMarkdownDestinations(source, func(value string) (string, bool) {
		if value == "old.md" {
			return "new.md", true
		}
		return value, false
	})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("[one](new.md) [ref]: <old.md#part>")) {
		t.Fatalf("updated = %q", updated)
	}
	patches := []bytePatch{{Start: before[0].Span.Start, End: before[0].Span.End, Text: []byte("new.md")}}
	if !bytesOutsidePatchesEqual(source, updated, patches) {
		t.Fatal("bytes outside destination span changed")
	}
}

func TestRewriteMarkdownDestinations_EmptyInlineDestination(t *testing.T) {
	for _, source := range []string{"[link]()\n", "![image](  )\n"} {
		t.Run(source, func(t *testing.T) {
			before, err := collectMarkdownDestinations([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			if len(before) != 1 || before[0].Value != "" || before[0].Span.Start != before[0].Span.End {
				t.Fatalf("destination = %#v", before)
			}
			updated, err := rewriteMarkdownDestinations([]byte(source), func(value string) (string, bool) {
				return "target.md", value == ""
			})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(updated, []byte("target.md)")) {
				t.Fatalf("updated = %q", updated)
			}
		})
	}
}

func TestRewriteMarkdownDestinations_NestedImageAndLinkOwnDistinctSpans(t *testing.T) {
	source := []byte("[![image](a.md)](a.md)\n")
	destinations, err := collectMarkdownDestinations(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(destinations) != 2 || destinations[0].Kind != MarkdownImageDestination || destinations[1].Kind != MarkdownLinkDestination {
		t.Fatalf("destinations = %#v", destinations)
	}
	first := bytes.Index(source, []byte("a.md"))
	second := bytes.LastIndex(source, []byte("a.md"))
	wantOwners := []MarkdownDestination{{Kind: MarkdownImageDestination, Span: SourceSpan{Start: first, End: first + len("a.md")}, Value: "a.md"}, {Kind: MarkdownLinkDestination, Span: SourceSpan{Start: second, End: second + len("a.md")}, Value: "a.md"}}
	assertExactMarkdownDestinations(t, source, wantOwners, destinations)
	identityUpdated, err := applyBytePatches(source, []bytePatch{{Start: wantOwners[0].Span.Start, End: wantOwners[0].Span.End, Text: []byte("image.md")}, {Start: wantOwners[1].Span.Start, End: wantOwners[1].Span.End, Text: []byte("link.md")}})
	if err != nil {
		t.Fatal(err)
	}
	identityOwners, err := collectMarkdownDestinations(identityUpdated)
	if err != nil || len(identityOwners) != 2 || identityOwners[0].Kind != MarkdownImageDestination || identityOwners[0].Value != "image.md" || identityOwners[1].Kind != MarkdownLinkDestination || identityOwners[1].Value != "link.md" {
		t.Fatalf("distinct nested owners = %#v, err=%v", identityOwners, err)
	}
	updated, err := rewriteMarkdownDestinations(source, func(value string) (string, bool) {
		return "new.md", value == "a.md"
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "[![image](new.md)](new.md)\n"; string(updated) != want {
		t.Fatalf("updated = %q, want %q", updated, want)
	}
}

func TestCollectMarkdownDestinations_LeavesRawHTMLUntouched(t *testing.T) {
	// Arrange
	source := []byte("<div>[not-a-link](old.md)</div>\n")

	// Act
	destinations, err := collectMarkdownDestinations(source)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(destinations) != 0 {
		t.Fatalf("destinations = %#v", destinations)
	}
}

func TestRewriteMarkdownDestinations_AnchorsEachGoldmarkNode(t *testing.T) {
	// Arrange
	source := []byte("<span>[decoy](old.md)</span> [link [nested]](old.md) ![image](old.md) ` [code](old.md) ` <https://old.md>\n\n[full][ref] [collapsed][] [shortcut]\n\n[ref]: <old.md> \"title\"\n[collapsed]: old.md\n[shortcut]: old.md\n\n```md\n[decoy](old.md)\n```\n<a href=\"old.md\">raw</a>\n")

	// Act
	updated, err := rewriteMarkdownDestinations(source, func(value string) (string, bool) {
		if value == "old.md" {
			return "new.md", true
		}
		return value, false
	})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<span>[decoy](new.md)</span>", "[link [nested]](new.md)", "![image](new.md)",
		"` [code](old.md) `", "<https://old.md>", "[ref]: <new.md> \"title\"",
		"[collapsed]: new.md", "[shortcut]: new.md", "[decoy](old.md)", "href=\"old.md\"",
	} {
		if !bytes.Contains(updated, []byte(want)) {
			t.Fatalf("updated missing %q:\n%s", want, updated)
		}
	}
}

func TestRewriteMarkdownDestinations_MultilineReferenceDefinition(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{
			name:   "plain continuation",
			source: "---\ntype: thing\n---\n[use][r]\n\n[r]:\n  old.md\n",
			want:   "[r]:\n  new.md\n",
		},
		{
			name:   "angle continuation",
			source: "---\ntype: thing\n---\n[use][r]\n\n[r]:\n  <old.md>\n",
			want:   "[r]:\n  <new.md>\n",
		},
		{
			name:   "tab-indented continuation",
			source: "---\ntype: thing\n---\n[use][r]\n\n[r]:\n\told.md\n",
			want:   "[r]:\n\tnew.md\n",
		},
		{
			name:   "crlf plain continuation",
			source: "---\r\ntype: thing\r\n---\r\n[use][r]\r\n\r\n[r]:\r\n  old.md\r\n",
			want:   "[r]:\r\n  new.md\r\n",
		},
		{
			name:   "crlf angle continuation",
			source: "---\r\ntype: thing\r\n---\r\n[use][r]\r\n\r\n[r]:\r\n  <old.md>\r\n",
			want:   "[r]:\r\n  <new.md>\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated, err := rewriteMarkdownDestinations([]byte(tt.source), func(value string) (string, bool) {
				if value == "old.md" {
					return "new.md", true
				}
				return value, false
			})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(updated, []byte(tt.want)) {
				t.Fatalf("updated = %q, want substring %q", updated, tt.want)
			}
		})
	}
}

func TestMoveConcept_RewritesMultilineReferenceDefinition(t *testing.T) {
	s := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\n---\n[use][r]\n\n[r]:\n  a.md\n"),
	}
	result, err := Plan(context.Background(), s, change(t, s, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := result.Staged.ReadFile(context.Background(), "b.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("[r]:\n  moved/a.md\n")) {
		t.Fatalf("b.md = %q", data)
	}
}

func TestRewriteMarkdownDestinations_UsesOpaqueASTDescendantOwnership(t *testing.T) {
	// Goldmark proves the first link-looking token belongs to a CodeSpan child;
	// only the final destination is owned by the enclosing Link.
	tests := []struct {
		name   string
		source []byte
	}{
		{name: "body-only", source: []byte("[`](0 `](0)")},
		{name: "with-frontmatter", source: []byte("---\ntype: thing\n---\n[`](0 `](0)")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			destinations, collectErr := collectMarkdownDestinations(tt.source)
			updated, rewriteErr := rewriteMarkdownDestinations(tt.source, func(string) (string, bool) {
				return "new", true
			})

			// Assert.
			if collectErr != nil || rewriteErr != nil || len(destinations) != 1 {
				t.Fatalf("destinations/errors = %#v / %v / %v", destinations, collectErr, rewriteErr)
			}
			if !bytes.Contains(updated, []byte("[`](0 `](new)")) {
				t.Fatalf("updated = %q", updated)
			}
		})
	}
}

func TestMarkdownParserFailureFailsClosed(t *testing.T) {
	// Arrange. Goldmark v1.8.2 panics in fenced-code parsing for this valid
	// UTF-8 blockquote fixture. Frontmatter also proves the reported span is
	// remapped to the original file rather than the parsed Markdown body.
	source := []byte("---\ntype: thing\n---\n> \t~")
	before := append([]byte(nil), source...)

	// Act.
	_, collectErr := collectMarkdownDestinations(source)
	updated, rewriteErr := rewriteMarkdownDestinations(source, func(string) (string, bool) {
		return "rewritten", true
	})

	// Assert.
	for _, err := range []error{collectErr, rewriteErr} {
		if !errors.Is(err, ErrUnsupportedPresentation) {
			t.Fatalf("error = %v, want ErrUnsupportedPresentation", err)
		}
		var presentation *PresentationError
		if !errors.As(err, &presentation) || presentation.Code != "markdown_parser_failure" || presentation.Format != "markdown" {
			t.Fatalf("PresentationError = %#v", presentation)
		}
		if !presentation.Location.valid(len(source)) || presentation.Location.Start != len("---\ntype: thing\n---\n") || presentation.Location.End != len(source) {
			t.Fatalf("Location = %#v, want bounded Markdown body", presentation.Location)
		}
	}
	if updated != nil || !bytes.Equal(source, before) {
		t.Fatalf("parser failure mutated source or returned output: source=%q updated=%q", source, updated)
	}
}

func TestMoveConceptMarkdownParserFailureDoesNotStage(t *testing.T) {
	// Arrange.
	source := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\n---\n> \t~"),
	}
	before := append([]byte(nil), source["b.md"]...)

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}))

	// Assert.
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("Plan() error = %v, want ErrUnsupportedPresentation", err)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Code != "markdown_parser_failure" || presentation.Format != "markdown" || presentation.Path != "b.md" || presentation.Operation != "move_concept" {
		t.Fatalf("PresentationError = %#v", presentation)
	}
	if !presentation.Location.valid(len(before)) || presentation.Location.Start != len("---\ntype: thing\n---\n") || presentation.Location.End != len(before) {
		t.Fatalf("Location = %#v", presentation.Location)
	}
	if result.Staged != nil || !bytes.Equal(source["b.md"], before) {
		t.Fatalf("failed MoveConcept staged or mutated input: result=%#v source=%q", result, source["b.md"])
	}
}

func TestRewriteMarkdownDestinations_DoesNotTreatParagraphContinuationAsReferenceDefinition(t *testing.T) {
	// Arrange
	source := []byte("[paragraph](keep.md)\n[ref]: old.md\n")

	// Act
	updated, err := rewriteMarkdownDestinations(source, func(value string) (string, bool) {
		if value == "old.md" {
			return "new.md", true
		}
		return value, false
	})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(updated, source) {
		t.Fatalf("updated = %q, want byte-identical %q", updated, source)
	}
}

func TestRewriteMarkdownDestinations_ReferenceDefinitionASTAnchoring(t *testing.T) {
	t.Run("invalid lexical prefix anchors later true definition", func(t *testing.T) {
		// Arrange
		source := []byte("[0]:0 0\n\n[0]:0\n")

		// Act
		updated, err := rewriteMarkdownDestinations(source, func(value string) (string, bool) {
			return "new.md", value == "0"
		})

		// Assert
		if err != nil {
			t.Fatal(err)
		}
		want := []byte("[0]:0 0\n\n[0]:new.md\n")
		if !bytes.Equal(updated, want) {
			t.Fatalf("updated = %q, want %q", updated, want)
		}
	})

	for _, tt := range []struct {
		name, source string
	}{
		{"equal definitions", "[same]: old.md\n[same]: old.md\n\n[use][same]\n"},
		{"different definitions", "[same]: old.md\n[same]: ignored.md\n\n[use][same]\n"},
		{"normalized label definitions", "[one][FOO bar] [two][foo   bar]\n\n[ Foo BAR ]: old.md\n[foo bar]: ignored.md\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			source := []byte(tt.source)

			// Act
			updated, err := rewriteMarkdownDestinations(source, func(value string) (string, bool) {
				return "new.md", value == "old.md"
			})

			// Assert
			if !errors.Is(err, ErrAmbiguousPresentation) || updated != nil {
				t.Fatalf("updated/error = %q / %v, want fail-closed ambiguity", updated, err)
			}
			var presentation *PresentationError
			if !errors.As(err, &presentation) || presentation.Code != "duplicate_reference_definition" || presentation.Location.Start >= presentation.Location.End {
				t.Fatalf("presentation = %#v", presentation)
			}
		})
	}
}

func TestMoveConcept_RewritesSemanticEscapesAndEncodesDestination(t *testing.T) {
	for _, tt := range []struct {
		name, to, want string
	}{
		{name: "space switches to angle", to: "new space", want: "[move](<new space.md>)"},
		{name: "angle delimiter is escaped", to: "new>place", want: "[move](new\\>place.md)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := memorySource{
				"old(x).md": []byte("---\ntype: thing\n---\nA\n"),
				"b.md":      []byte("---\ntype: thing\n---\n[move](old\\(x\\).md)\n"),
			}
			result, err := Plan(context.Background(), source, change(t, source, store.MoveConcept{From: ref(t, "old(x)").ID, To: ref(t, tt.to).ID}))
			if err != nil {
				t.Fatal(err)
			}
			data, err := result.Staged.ReadFile(context.Background(), "b.md")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte(tt.want)) {
				t.Fatalf("b.md = %q, want %q", data, tt.want)
			}
		})
	}
}

func FuzzRewriteMarkdownDestinations(f *testing.F) {
	for _, seed := range []string{
		"[link](old.md)\n",
		"`[code](old.md)`\n",
		"[ref]: <old.md>\n[use][ref]\n",
		"[one](a.md) ![two](b.md) [ref][r]\n\n[r]: c.md\n",
		"[`](0 `](0)",
		"> \t~",
	} {
		f.Add(seed)
	}
	for variant := 0; variant < markdownSupportedVariantCount; variant++ {
		f.Add(strings.Repeat("v", variant))
	}
	f.Fuzz(func(t *testing.T, source string) {
		// Arrange
		data := []byte(source)
		original := append([]byte(nil), data...)
		fuzzMarkdownSupportedSubset(t, data)
		independentBefore, independentErr := markdownGoldmarkASTProjection(data)
		before, beforeErr := collectMarkdownDestinations(data)
		if beforeErr != nil {
			assertMarkdownFuzzError(t, beforeErr, len(data))
			if !bytes.Equal(data, original) {
				t.Fatal("pre-collection rejection mutated source input")
			}
			return
		}
		if independentErr != nil {
			t.Fatalf("collector accepted input rejected by independent Goldmark oracle: %v", independentErr)
		}
		assertMarkdownProjection(t, data, before)
		assertMarkdownMatchesGoldmarkProjection(t, independentBefore, before)
		selected := markdownFuzzSelection(data, before)
		rewrites := markdownFuzzRewrites(before, selected)
		expected := append([]MarkdownDestination(nil), before...)
		patches := make([]bytePatch, 0, len(before))
		for i, destination := range before {
			value, rewrite := rewrites[destination.Value]
			if !rewrite {
				continue
			}
			expected[i].Value = value
			replacement := []byte(value)
			if data[destination.Span.Start] == '<' {
				replacement = append(append([]byte{'<'}, replacement...), '>')
			}
			patches = append(patches, bytePatch{Start: destination.Span.Start, End: destination.Span.End, Text: replacement})
		}

		// Act
		updated, err := rewriteMarkdownDestinations(data, func(value string) (string, bool) {
			replacement, rewrite := rewrites[value]
			return replacement, rewrite
		})

		// Assert
		if err != nil {
			t.Fatalf("supported markdown projection rejected: %v", err)
		}
		if !bytes.Equal(data, original) {
			t.Fatal("rewrite mutated source input")
		}
		after, afterErr := collectMarkdownDestinations(updated)
		if afterErr != nil {
			t.Fatalf("updated source does not reparse: %v", afterErr)
		}
		assertMarkdownProjection(t, updated, after)
		assertMarkdownMatchesGoldmarkAST(t, updated, after)
		if !equalMarkdownProjection(expected, after) {
			t.Fatalf("ordered AST projection = %#v, want %#v", after, expected)
		}
		if !bytesOutsidePatchesEqual(data, updated, patches) {
			t.Fatal("bytes outside actual destination patches changed")
		}
	})
}

const markdownSupportedVariantCount = 9

func TestMarkdownSupportedFuzzOracleMatrix(t *testing.T) {
	for variant := 0; variant < markdownSupportedVariantCount; variant++ {
		t.Run(fmt.Sprintf("variant-%d", variant), func(t *testing.T) {
			fuzzMarkdownSupportedSubsetVariant(t, []byte("matrix"), variant)
		})
	}
}

// fuzzMarkdownSupportedSubset selects from a deterministic acceptance matrix
// whose expected semantics are stored while constructing the fixture. It does
// not infer expected values from the collector under test.
func fuzzMarkdownSupportedSubset(t *testing.T, seed []byte) {
	t.Helper()
	fuzzMarkdownSupportedSubsetVariant(t, seed, len(seed)%markdownSupportedVariantCount)
}

func fuzzMarkdownSupportedSubsetVariant(t *testing.T, seed []byte, variant int) {
	t.Helper()
	source, expectedOwners := markdownSupportedFixture(seed, variant)
	expected := markdownSemanticsFromDestinations(expectedOwners)
	independent, err := markdownGoldmarkASTProjection(source)
	if err != nil {
		t.Fatalf("generated supported Markdown did not parse independently: %v", err)
	}
	assertMarkdownSemanticProjectionEqual(t, expected, independent, "constructed fixture", "Goldmark AST")
	collected, err := collectMarkdownDestinations(source)
	if err != nil {
		t.Fatalf("generated supported Markdown variant %d was rejected: %v", variant, err)
	}
	assertMarkdownProjection(t, source, collected)
	assertExactMarkdownDestinations(t, source, expectedOwners, collected)
	assertMarkdownMatchesGoldmarkProjection(t, expected, collected)
	patches := make([]bytePatch, 0, len(expectedOwners))
	for _, destination := range expectedOwners {
		replacement := []byte("supported-rewrite.md")
		if source[destination.Span.Start] == '<' {
			replacement = []byte("<supported-rewrite.md>")
		}
		patches = append(patches, bytePatch{Start: destination.Span.Start, End: destination.Span.End, Text: replacement})
	}
	updated, err := rewriteMarkdownDestinations(source, func(value string) (string, bool) {
		return "supported-rewrite.md", true
	})
	if err != nil {
		t.Fatalf("generated supported Markdown variant %d rewrite was rejected: %v", variant, err)
	}
	if !bytesOutsidePatchesEqual(source, updated, patches) {
		t.Fatalf("generated supported Markdown variant %d changed bytes outside destinations", variant)
	}
	after, err := collectMarkdownDestinations(updated)
	if err != nil {
		t.Fatalf("generated supported Markdown variant %d rewrite did not reparse: %v", variant, err)
	}
	wantAfter := make([]markdownASTSemantic, len(expected))
	for index := range wantAfter {
		wantAfter[index] = markdownASTSemantic{kind: expected[index].kind, value: "supported-rewrite.md"}
	}
	assertMarkdownMatchesGoldmarkProjection(t, wantAfter, after)
}

func markdownSupportedFixture(seed []byte, variant int) ([]byte, []MarkdownDestination) {
	sample := seed
	if len(sample) > 32 {
		sample = sample[:32]
	}
	target := "fuzz-" + hex.EncodeToString(sample) + ".md"
	type spec struct {
		kind       MarkdownDestinationKind
		raw, value string
	}
	build := func(source []byte, specs ...spec) ([]byte, []MarkdownDestination) {
		owners := make([]MarkdownDestination, 0, len(specs))
		cursor := 0
		for _, expected := range specs {
			relative := bytes.Index(source[cursor:], []byte(expected.raw))
			if relative < 0 {
				panic("constructed Markdown fixture lost expected raw destination")
			}
			start := cursor + relative
			owners = append(owners, MarkdownDestination{Kind: expected.kind, Span: SourceSpan{Start: start, End: start + len(expected.raw)}, Value: expected.value})
			cursor = start + len(expected.raw)
		}
		return source, owners
	}
	switch variant % markdownSupportedVariantCount {
	case 0:
		return build([]byte(fmt.Sprintf("[link](%s)\n", target)), spec{MarkdownLinkDestination, target, target})
	case 1:
		return build([]byte(fmt.Sprintf("![image](%s \"title\")\r\n", target)), spec{MarkdownImageDestination, target, target})
	case 2:
		source := []byte("[empty]() ![empty](  )\n")
		first := bytes.Index(source, []byte("()")) + 1
		second := bytes.Index(source, []byte("(  )")) + 3
		return source, []MarkdownDestination{{Kind: MarkdownLinkDestination, Span: SourceSpan{Start: first, End: first}, Value: ""}, {Kind: MarkdownImageDestination, Span: SourceSpan{Start: second, End: second}, Value: ""}}
	case 3:
		return build([]byte(fmt.Sprintf("[one](%s) [two](%s)\n", target, target)), spec{MarkdownLinkDestination, target, target}, spec{MarkdownLinkDestination, target, target})
	case 4:
		return build([]byte(fmt.Sprintf("[![image](%s)](%s)\n", target, target)), spec{MarkdownImageDestination, target, target}, spec{MarkdownLinkDestination, target, target})
	case 5:
		return build([]byte(fmt.Sprintf("[full][r] [collapsed][] [shortcut]\n\n[r]: %s\n[collapsed]: %s-c\n[shortcut]: %s-s\n", target, target, target)), spec{MarkdownReferenceDefinitionDestination, target, target}, spec{MarkdownReferenceDefinitionDestination, target + "-c", target + "-c"}, spec{MarkdownReferenceDefinitionDestination, target + "-s", target + "-s"})
	case 6:
		return build([]byte(fmt.Sprintf("[use][r]\n\n[r]:\r\n  <%s>\r\n", target)), spec{MarkdownReferenceDefinitionDestination, "<" + target + ">", target})
	case 7:
		return build([]byte("[escaped](fuzz\\(escaped\\).md)\n"), spec{MarkdownLinkDestination, "fuzz\\(escaped\\).md", "fuzz(escaped).md"})
	default:
		value := "fuzz path " + hex.EncodeToString(sample) + ".md"
		return build([]byte(fmt.Sprintf("[angle](<%s> 'title')\n", value)), spec{MarkdownLinkDestination, "<" + value + ">", value})
	}
}

func markdownSemanticsFromDestinations(destinations []MarkdownDestination) []markdownASTSemantic {
	semantics := make([]markdownASTSemantic, len(destinations))
	for index, destination := range destinations {
		semantics[index] = markdownASTSemantic{kind: destination.Kind, value: destination.Value}
	}
	return semantics
}

func assertExactMarkdownDestinations(t *testing.T, source []byte, expected, actual []MarkdownDestination) {
	t.Helper()
	if len(expected) != len(actual) {
		t.Fatalf("exact owners = %#v, collected = %#v", expected, actual)
	}
	for index := range expected {
		if expected[index].Kind != actual[index].Kind || expected[index].Span != actual[index].Span || expected[index].Value != actual[index].Value {
			t.Fatalf("exact owner %d = %#v raw %q, collected = %#v raw %q", index, expected[index], source[expected[index].Span.Start:expected[index].Span.End], actual[index], source[actual[index].Span.Start:actual[index].Span.End])
		}
	}
}

type markdownASTSemantic struct {
	kind  MarkdownDestinationKind
	value string
}

func assertMarkdownMatchesGoldmarkAST(t *testing.T, source []byte, collected []MarkdownDestination) {
	t.Helper()
	expected, err := markdownGoldmarkASTProjection(source)
	if err != nil {
		t.Fatalf("independent Goldmark projection = %v", err)
	}
	assertMarkdownMatchesGoldmarkProjection(t, expected, collected)
}

func markdownGoldmarkASTProjection(source []byte) ([]markdownASTSemantic, error) {
	bodyStart, err := markdownBodyStart(source)
	if err != nil {
		return nil, err
	}
	doc, _, err := protectedGoldmarkParse(context.Background(), source[bodyStart:])
	if err != nil {
		return nil, err
	}
	expected := make([]markdownASTSemantic, 0)
	err = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := node.(type) {
		case *ast.Link:
			if node.Reference == nil {
				expected = append(expected, markdownASTSemantic{kind: MarkdownLinkDestination, value: independentMarkdownDestination(node.Destination)})
			}
		case *ast.Image:
			if node.Reference == nil {
				expected = append(expected, markdownASTSemantic{kind: MarkdownImageDestination, value: independentMarkdownDestination(node.Destination)})
			}
		case *ast.LinkReferenceDefinition:
			expected = append(expected, markdownASTSemantic{kind: MarkdownReferenceDefinitionDestination, value: independentMarkdownDestination(node.Destination)})
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	sortMarkdownASTSemantics(expected)
	return expected, nil
}

func independentMarkdownDestination(raw []byte) string {
	unescaped := make([]byte, 0, len(raw))
	for index := 0; index < len(raw); index++ {
		if raw[index] == '\\' && index+1 < len(raw) && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(raw[index+1])) {
			index++
		}
		unescaped = append(unescaped, raw[index])
	}
	return html.UnescapeString(string(unescaped))
}

func assertMarkdownMatchesGoldmarkProjection(t *testing.T, expected []markdownASTSemantic, collected []MarkdownDestination) {
	t.Helper()
	actual := make([]markdownASTSemantic, len(collected))
	for index, destination := range collected {
		actual[index] = markdownASTSemantic{kind: destination.Kind, value: destination.Value}
	}
	assertMarkdownSemanticProjectionEqual(t, expected, actual, "Goldmark AST", "collected")
}

func assertMarkdownSemanticProjectionEqual(t *testing.T, expected, actual []markdownASTSemantic, expectedName, actualName string) {
	t.Helper()
	expected = append([]markdownASTSemantic(nil), expected...)
	actual = append([]markdownASTSemantic(nil), actual...)
	sortMarkdownASTSemantics(expected)
	sortMarkdownASTSemantics(actual)
	if len(expected) != len(actual) {
		t.Fatalf("%s destinations = %#v, %s = %#v", expectedName, expected, actualName, actual)
	}
	for index := range expected {
		if expected[index] != actual[index] {
			t.Fatalf("%s destinations = %#v, %s = %#v", expectedName, expected, actualName, actual)
		}
	}
}

func sortMarkdownASTSemantics(values []markdownASTSemantic) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].kind != values[right].kind {
			return values[left].kind < values[right].kind
		}
		return values[left].value < values[right].value
	})
}

func assertMarkdownFuzzError(t *testing.T, err error, sourceLength int) {
	t.Helper()
	if !errors.Is(err, ErrAmbiguousPresentation) && !errors.Is(err, ErrUnsupportedPresentation) && !errors.Is(err, bundle.ErrInvalidEncoding) {
		t.Fatalf("untyped markdown rejection: %v", err)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Format != "markdown" {
		t.Fatalf("markdown rejection lacks PresentationError context: %#v", err)
	}
	if sourceLength > 0 && (presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End || presentation.Location.End > sourceLength) {
		t.Fatalf("markdown rejection location = %#v for %d-byte source", presentation.Location, sourceLength)
	}
}

func assertMarkdownProjection(t *testing.T, source []byte, projection []MarkdownDestination) {
	t.Helper()
	for i, item := range projection {
		if !item.Span.valid(len(source)) {
			t.Fatalf("invalid destination span: %#v", item)
		}
		if i > 0 && projection[i-1].Span.End > item.Span.Start {
			t.Fatalf("overlapping destination spans: %#v and %#v", projection[i-1], item)
		}
	}
}

func equalMarkdownProjection(want, got []MarkdownDestination) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		// Spans identify concrete AST-owned source nodes before and after the
		// rewrite; kind/value/order are the stable semantic projection.
		if want[i].Kind != got[i].Kind || want[i].Value != got[i].Value {
			return false
		}
	}
	return true
}

func markdownFuzzSelection(source []byte, destinations []MarkdownDestination) []bool {
	selected := make([]bool, len(destinations))
	if len(destinations) == 0 {
		return selected
	}
	// Deterministically select a non-empty, input-dependent subset.  This
	// covers every eligible position rather than a legacy fixed "old.md" case.
	for i, destination := range destinations {
		selected[i] = (len(source)+destination.Span.Start+i)%2 == 0
	}
	for _, ok := range selected {
		if ok {
			return selected
		}
	}
	selected[len(selected)-1] = true
	return selected
}

func markdownFuzzRewrites(destinations []MarkdownDestination, selected []bool) map[string]string {
	rewrites := make(map[string]string, len(destinations))
	for i, destination := range destinations {
		if selected[i] {
			// The callback has no node identity.  Its first selected occurrence
			// chooses a value-class rewrite, which applies to every equal value.
			if _, exists := rewrites[destination.Value]; !exists {
				rewrites[destination.Value] = fmt.Sprintf("fuzz-rewrite-%d.md", i)
			}
		}
	}
	return rewrites
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
