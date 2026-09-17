package mutation

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

// TestParserBackedMarkdownCorpus is a deterministic, source-level complement to
// FuzzRewriteMarkdownDestinations.  The expected tokens are deliberately raw:
// they prove the exact span, including angle brackets and escaping.
func TestParserBackedMarkdownCorpus(t *testing.T) {
	tests := []struct {
		name, source string
		want         []string
		excluded     []string
	}{
		{"inline-image-angle-nested-escaped-crlf", "---\r\ntype: thing\r\n---\r\n[a [b]](<old(one).md> \"title\") ![i](old-image.md) [e](old\\(x\\).md)\r\n", []string{"<old(one).md>", "old-image.md", "old\\(x\\).md"}, nil},
		{"references-full-collapsed-shortcut-repeated", "[one][r] [two][r] [collapsed][] [shortcut]\n\n[r]: <old.md> 'title'\n[collapsed]: old-two.md\n[shortcut]: old-three.md\n", []string{"<old.md>", "old-two.md", "old-three.md"}, nil},
		{"invalid-prefix-reference-anchor-corpus", "[0]:0 0\n\n[0]:0\n", []string{"0"}, nil},
		{"adjacent-blockquote-list", "[a](old.md)[b](old-two.md)\n\n> [q](old-three.md)\n\n- [l](old-four.md)\n", []string{"old.md", "old-two.md", "old-three.md", "old-four.md"}, nil},
		{"html-container-keeps-markdown-inline", "<span>[inside](old.md)</span> <a href=\"old.md\">raw</a>\n", []string{"old.md"}, []string{"href=\"new.md\""}},
		{"excluded-code-autolink-malformed", "`[code](old.md)`\n\n    [indent](old.md)\n\n```md\n[fence](old.md)\n```\n\n<https://old.md> [bad](<old.md)\n", nil, []string{"new.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			source := []byte(tt.source)

			// Act.
			before, err := collectMarkdownDestinationsContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			updated, err := rewriteMarkdownDestinationsContext(context.Background(), source, func(value string) (string, bool) {
				if strings.HasPrefix(value, "old") {
					return "new" + strings.TrimPrefix(value, "old"), true
				}
				return value, false
			})

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(before))
			patches := make([]bytePatch, 0, len(before))
			for i, destination := range before {
				if !destination.Span.valid(len(source)) || (i > 0 && before[i-1].Span.End > destination.Span.Start) {
					t.Fatalf("invalid or overlapping span %#v", destination.Span)
				}
				got = append(got, string(source[destination.Span.Start:destination.Span.End]))
				if strings.HasPrefix(destination.Value, "old") {
					replacement, encodeErr := encodeMarkdownDestinationContext(context.Background(), "new"+strings.TrimPrefix(destination.Value, "old"), destination.angle)
					if encodeErr != nil {
						t.Fatal(encodeErr)
					}
					patches = append(patches, bytePatch{Start: destination.Span.Start, End: destination.Span.End, Text: replacement})
				}
			}
			if !equalStrings(got, tt.want) {
				t.Fatalf("raw destination spans = %q, want %q", got, tt.want)
			}
			if !bytesOutsidePatchesEqualForTest(t, source, updated, patches) {
				t.Fatal("bytes outside the union of destination spans changed")
			}
			after, err := collectMarkdownDestinationsContext(context.Background(), updated)
			if err != nil || len(after) != len(before) {
				t.Fatalf("rewritten document did not reparse equivalently: count=%d/%d err=%v", len(after), len(before), err)
			}
			for _, excluded := range tt.excluded {
				if bytes.Contains(updated, []byte(excluded)) {
					t.Fatalf("excluded presentation changed: %q in %q", excluded, updated)
				}
			}
		})
	}
}

// TestParserBackedYAMLPresentationCorpus covers parser -> resolver -> patch and
// planner staging.  The rejection rows are intentionally planner-level so a
// failure proves no partial Result escaped.
func TestParserBackedYAMLPresentationCorpus(t *testing.T) {
	t.Run("scalar-styles-crlf-utf8-comments-order-unknown", func(t *testing.T) {
		for _, item := range []struct{ name, token string }{
			{"plain", "old"}, {"single", "'old'"}, {"double-escaped", "\"old\""},
		} {
			t.Run(item.name, func(t *testing.T) {
				// Arrange.
				data := []byte("---\r\ntype: thing\r\nunknown: café # retain\r\nparts:\r\n  - id: " + item.token + " # retain\r\n    anchor: legacy\r\n---\r\nbody\r\n")
				p, err := parsePresentationContext(context.Background(), data)
				if err != nil {
					t.Fatal(err)
				}

				// Act.
				patch, found, err := fragmentPatchContext(context.Background(), p, "old", "new")
				updated, patchErr := p.patchYAMLContext(context.Background(), []bytePatch{patch})

				// Assert.
				if err != nil || !found || patchErr != nil {
					t.Fatalf("patch = %#v found=%t errors=%v/%v", patch, found, err, patchErr)
				}
				if !bytesOutsidePatchesEqualForTest(t, data, updated, []bytePatch{patch}) || !bytes.Contains(updated, []byte("unknown: café # retain\r\n")) {
					t.Fatalf("lossless patch = %q", updated)
				}
			})
		}
	})

	t.Run("target-insertion-idempotence-and-duplicate-ambiguity", func(t *testing.T) {
		// Arrange.
		data := []byte("---\ntype: thing\nparts:\n  - id: a\nrelations:\n  uses:\n    - target: b\n    - target: b\n---\nA\n")
		// Act.
		updated, err := ensureRelationPresentationContext(context.Background(), data, ref(t, "a"), "uses", "b")
		// Assert.
		if !errors.Is(err, ErrAmbiguousPresentation) || updated != nil {
			t.Fatalf("duplicate result = %q, %v", updated, err)
		}
		inserted, err := ensureRelationPresentationContext(context.Background(), []byte("---\ntype: thing\nparts:\n  - id: a\n---\nA\n"), ref(t, "a"), "uses", "b")
		if err != nil || !bytes.Contains(inserted, []byte("relations:\n  uses:\n    - target: b\n")) {
			t.Fatalf("insertion = %q, %v", inserted, err)
		}
	})

	t.Run("quoted-structural-keys", func(t *testing.T) {
		data := []byte("---\n\"type\": thing\n\"<<\": ordinary-extension\n'parts':\n  - \"id\": old\n\"relations\":\n  'uses':\n    - \"target\": old#frag # retain\n---\nA\n")
		p, err := parsePresentationContext(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		fragment, found, err := fragmentPatchContext(context.Background(), p, "old", "new")
		patches, relationErr := relationTargetPatchesContext(context.Background(), p, ref(t, "old").ID, ref(t, "new").ID, "frag", "frag")
		patches = append(patches, fragment)
		updated, patchErr := p.patchYAMLContext(context.Background(), patches)
		if err != nil || !found || relationErr != nil || patchErr != nil {
			t.Fatalf("quoted key patch errors = %v/%v/%v", err, relationErr, patchErr)
		}
		if !bytes.Contains(updated, []byte("\"id\": new")) || !bytes.Contains(updated, []byte("\"target\": new#frag # retain")) {
			t.Fatalf("updated = %q", updated)
		}
	})

	t.Run("stream-marker-text-inside-scalars", func(t *testing.T) {
		for _, tt := range []struct{ name, unknown string }{
			{"block", "unknown: |\n  %YAML 1.2\n  ---\n  ...\n"},
			{"quoted", "unknown: \"line\n  %TAG ! tag:example.test,2026:\n  ---\n  ...\"\n"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				data := []byte("---\n" + tt.unknown + "parts:\n  - id: old\n---\nA\n")
				p, err := parsePresentationContext(context.Background(), data)
				if err != nil {
					t.Fatal(err)
				}
				patch, found, err := fragmentPatchContext(context.Background(), p, "old", "new")
				if err != nil || !found {
					t.Fatalf("fragment patch found=%t err=%v", found, err)
				}
				updated, patchErr := p.patchYAMLContext(context.Background(), []bytePatch{patch})
				if patchErr != nil || !bytes.Contains(updated, []byte("- id: new")) {
					t.Fatalf("scalar marker patch = %q, error=%v", updated, patchErr)
				}
			})
		}
	})

	t.Run("insertion-verification-uses-independent-intent", func(t *testing.T) {
		data := []byte("---\ntype: thing\n---\nA\n")
		p, err := parsePresentationContext(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := p.expectedEnsureRelationContext(context.Background(), p.root, "uses", "b")
		if err != nil {
			t.Fatal(err)
		}
		updated, err := p.insertAfterContext(context.Background(), p.root, "relations:\n  uses:\n    - target: wrong\n", expected)
		if updated != nil || !errors.Is(err, ErrUnsupportedPresentation) {
			t.Fatalf("wrong insertion result/error = %q / %v", updated, err)
		}
		var presentation *PresentationError
		if !errors.As(err, &presentation) || presentation.Code != "semantic_edit_mismatch" {
			t.Fatalf("presentation = %#v", presentation)
		}
	})

	t.Run("target-update-and-id-wins-over-anchor", func(t *testing.T) {
		// Arrange.
		data := []byte("---\ntype: thing\nparts:\n  - id: old\n    anchor: legacy\nrelations:\n  uses:\n    - target: old#frag # retain\n---\nA\n")
		p, err := parsePresentationContext(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}

		// Act.
		fragment, found, err := fragmentPatchContext(context.Background(), p, "old", "new")
		patches, relationErr := relationTargetPatchesContext(context.Background(), p, ref(t, "old").ID, ref(t, "new").ID, "frag", "frag")
		patches = append(patches, fragment)
		updated, patchErr := p.patchYAMLContext(context.Background(), patches)

		// Assert.
		if err != nil || !found || relationErr != nil || patchErr != nil {
			t.Fatalf("patch errors = %v/%v/%v", err, relationErr, patchErr)
		}
		if !bytes.Contains(updated, []byte("- id: new\n    anchor: legacy")) || !bytes.Contains(updated, []byte("target: new#frag # retain")) {
			t.Fatalf("updated = %q", updated)
		}
	})

	for _, tt := range []struct {
		name, frontmatter string
		want              error
		wantCode          string
	}{
		{"tag", "parts:\n  - id: !!str old\n", ErrUnsupportedPresentation, "explicit_tag"},
		{"non-specific-tag-id", "parts:\n  - id: ! old\n", ErrUnsupportedPresentation, "explicit_tag"},
		{"non-specific-tag-anchor", "parts:\n  - anchor: ! old\n", ErrUnsupportedPresentation, "explicit_tag"},
		{"anchor", "parts:\n  - id: &x old\n", ErrUnsupportedPresentation, "touched_anchor"},
		{"alias", "parts:\n  - id: &x old\n  - id: *x\n", ErrAmbiguousPresentation, "alias_provenance"},
		{"flow", "parts: [{id: old}]\n", ErrUnsupportedPresentation, "canonical_fragment_flow_mapping"},
		{"block-scalar", "parts:\n  - id: |\n      old\n", ErrUnsupportedPresentation, "unsupported_scalar"},
		{"block-scalar-strip", "parts:\n  - id: |-\n      old\n", ErrUnsupportedPresentation, "unsupported_scalar"},
		{"folded-scalar", "parts:\n  - id: >\n      old\n", ErrUnsupportedPresentation, "unsupported_scalar"},
		{"quoted-complex-key", "? [parts]\n: - id: old\n", ErrUnsupportedPresentation, "unsupported_key"},
		{"directives", "%YAML 1.2\nparts:\n  - id: old\n", ErrUnsupportedPresentation, "yaml_stream_syntax"},
		{"document-marker", "parts:\n  - id: old\n...\nextra: 1\n", ErrUnsupportedPresentation, "yaml_stream_syntax"},
		{"document-start-marker-comment", "parts:\n  - id: old\n--- # nested document\nextra: 1\n", ErrUnsupportedPresentation, "yaml_stream_syntax"},
		{"document-end-marker-comment", "parts:\n  - id: old\n... # end\n", ErrUnsupportedPresentation, "yaml_stream_syntax"},
		{"document-end-marker-spaces", "parts:\n  - id: old\n...   \n", ErrUnsupportedPresentation, "yaml_stream_syntax"},
		{"document-end-marker-comment-crlf", "parts:\r\n  - id: old\r\n... # end\r\n", ErrUnsupportedPresentation, "yaml_stream_syntax"},
		{"duplicate-mapping", "parts:\n  - id: old\n    id: old\n", ErrAmbiguousPresentation, "duplicate_canonical_fragment"},
		{"quoted-newline-fragment", "parts:\n  - id: \"old\\n\"\n", ErrUnsupportedPresentation, "invalid_identity_fragment"},
		{"quoted-newline-with-valid-anchor", "parts:\n  - id: \"old\\n\"\n    anchor: old\n", ErrUnsupportedPresentation, "invalid_identity_fragment"},
		{"invalid-sibling-id-with-valid-anchor", "parts:\n  - id: \"other\\n\"\n    anchor: old\n", ErrUnsupportedPresentation, "invalid_identity_fragment"},
		{"nonscalar-id-with-valid-anchor", "parts:\n  - id: [foo]\n    anchor: old\n", ErrUnsupportedPresentation, "unsupported_identity_scalar"},
		{"integer-id-with-valid-anchor", "parts:\n  - id: 123\n    anchor: old\n", ErrUnsupportedPresentation, "explicit_tag"},
	} {
		t.Run("reject-"+tt.name, func(t *testing.T) {
			// Arrange.
			s := memorySource{"a.md": []byte("---\n" + tt.frontmatter + "---\nA\n"), "b.md": []byte("---\ntype: thing\n---\nB\n")}
			before := append([]byte(nil), s["a.md"]...)

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}))

			// Assert.
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			var presentation *PresentationError
			if !errors.As(err, &presentation) || presentation.Format != "yaml" || presentation.Path != "a.md" {
				t.Fatalf("metadata = %#v", presentation)
			}
			if tt.wantCode != "" && presentation.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q (%v)", presentation.Code, tt.wantCode, err)
			}
			if result.Staged != nil || !bytes.Equal(before, s["a.md"]) {
				t.Fatalf("rejection staged or mutated input: %#v", result)
			}
		})
	}

	t.Run("invalid-utf8", func(t *testing.T) {
		data := []byte("---\nparts:\n  - id: \xff\n---\nA\n")
		_, err := parsePresentationContext(context.Background(), data)
		if !errors.Is(err, bundle.ErrInvalidEncoding) {
			t.Fatalf("error = %v", err)
		}
	})
}

// TestParserBackedAllocationBaseline makes the payload-size allocation contract a
// stable gate in addition to the benchmark. Eight allocations is deliberately
// a metadata-only ceiling: the measured clone baseline is five allocations,
// and three allocations of slack permits harmless runtime/map variation while
// still rejecting a payload-proportional copy.
func TestParserBackedAllocationBaseline(t *testing.T) {
	// Arrange.
	small := cloneAllocsForPayload(t, 64)
	large := cloneAllocsForPayload(t, 1<<20)

	// Act.
	// AllocsPerRun invokes the same clone operation under a stable allocation
	// measurement harness; no benchmark timing is involved.

	// Assert.
	if small != large {
		t.Fatalf("clone allocation baseline changes with payload: 64B=%v 1MiB=%v", small, large)
	}
	if small > 8 {
		t.Fatalf("clone allocations = %v, want <= 8 metadata allocations (baseline 5 + slack 3)", small)
	}
}
