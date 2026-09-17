package mutation

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

func TestYAMLResolver_ScalarSpanUsesRuneColumnsForMultibyteKeys(t *testing.T) {
	// Arrange. yaml.v3 Column counts Unicode characters, not bytes.
	source := []byte("café: old\n")
	resolver, err := newYAMLResolverContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(source, &doc); err != nil {
		t.Fatal(err)
	}
	value := doc.Content[0].Content[1]

	// Act.
	span, err := resolver.scalar(value)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if got := string(source[span.Span.Start:span.Span.End]); got != "old" {
		t.Fatalf("raw span = %q, want %q (span=%v)", got, "old", span.Span)
	}
}

func TestYAMLResolver_ScalarSpanPreservesCRLFCommentAndQuote(t *testing.T) {
	// Arrange.
	data := []byte("---\r\ntype: thing\r\nrelations:\r\n  uses:\r\n    - target: 'a#old' # keep\r\n---\r\nbody")
	p, err := parsePresentationContext(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	target := mappingValuesForTest(t, mappingValuesForTest(t, p.root, "relations")[0], "uses")[0].Content[0].Content[1]

	// Act.
	patch, err := p.resolvedScalarPatch(target, "a#new")
	updated, patchErr := p.patchYAMLContext(context.Background(), []bytePatch{patch})

	// Assert.
	if err != nil || patchErr != nil {
		t.Fatalf("patch errors = %v, %v", err, patchErr)
	}
	if got, want := string(data[patch.Start:patch.End]), "'a#old'"; got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}
	if !bytes.Contains(updated, []byte("target: 'a#new' # keep\r\n")) {
		t.Fatalf("updated = %q", updated)
	}
	if !bytesOutsidePatchesEqualForTest(t, data, updated, []bytePatch{patch}) {
		t.Fatal("bytes outside scalar span changed")
	}
}

func TestYAMLResolver_InvalidUTF8FailsBeforeCoordinates(t *testing.T) {
	// Arrange.
	data := []byte("---\ntarget: \xff\n---\n")

	// Act.
	_, err := parsePresentationContext(context.Background(), data)

	// Assert.
	if !errors.Is(err, bundle.ErrInvalidEncoding) {
		t.Fatalf("error = %v, want ErrInvalidEncoding", err)
	}
}

func TestYAMLResolver_VerifyPatchedRejectsSyntacticallyValidWrongScalar(t *testing.T) {
	// Arrange.
	data := []byte("---\nparts:\n  - id: old\n---\nbody\n")
	p, err := parsePresentationContext(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	patch, found, err := fragmentPatchContext(context.Background(), p, "old", "new")
	if err != nil || !found {
		t.Fatalf("fragment patch = %#v, %t, %v", patch, found, err)
	}
	patch.Text = []byte("wrong") // valid YAML, but not the declared edit.

	// Act.
	_, err = p.patchYAMLContext(context.Background(), []bytePatch{patch})

	// Assert.
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("error = %v, want semantic rejection as Unsupported", err)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Code != "semantic_edit_mismatch" {
		t.Fatalf("presentation = %#v", presentation)
	}
}

func TestParsePresentation_InvalidYAMLUsesTypedContract(t *testing.T) {
	// Arrange.
	data := []byte("---\nparts: [\n---\nbody\n")

	// Act.
	_, err := parsePresentationContext(context.Background(), data)

	// Assert.
	var presentation *PresentationError
	if !errors.Is(err, ErrUnsupportedPresentation) || !errors.As(err, &presentation) {
		t.Fatalf("parsePresentationContext(context.Background(), ) error = %#v", err)
	}
	if presentation.Code != "yaml_syntax" || presentation.Format != "yaml" || presentation.Location != (SourceSpan{Start: 0, End: len("parts: [\n")}) {
		t.Fatalf("PresentationError = %#v", presentation)
	}
}

func TestEnsureRelationPreservesUnrelatedMultilineScalar(t *testing.T) {
	// Arrange. AST sibling ownership proves the root insertion boundary without
	// claiming the unrelated multiline extension scalar as a patch owner.
	data := []byte("---\nA: \"\n\"\n---\nbody\n")

	// Act.
	updated, err := ensureRelationPresentationContext(context.Background(), data, ref(t, "a"), "uses", "b")

	// Assert.
	if err != nil || !bytes.Contains(updated, []byte("A: \"\n\"\nrelations:\n  uses:\n    - target: b\n")) {
		t.Fatalf("ensureRelationPresentationContext(context.Background(), ) = %q, %v", updated, err)
	}
}

func TestEnsureRelationInsertionBoundaryIgnoresOpaqueUnrelatedSubtrees(t *testing.T) {
	tests := []struct {
		name, yaml, want string
	}{
		{
			name: "root complex merge block and bracket scalars",
			yaml: "base: &base\n  label: keep\n? [opaque, key]\n: {nested: [one, two]}\nmerged:\n  <<: *base\nunknown: |\n  keep\nplain: abc[\n",
			want: "plain: abc[\nrelations:\n  uses:\n    - target: b\n",
		},
		{
			name: "relation mapping block extension",
			yaml: "relations:\n  note: |\n    keep\n",
			want: "    keep\n  uses:\n    - target: b\n",
		},
		{
			name: "relation item opaque extensions",
			yaml: "relations:\n  uses:\n    - target: a\n      note: |\n        keep\n      duplicate: one\n      duplicate: two\n",
			want: "      duplicate: two\n    - target: b\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			data := []byte("---\n" + tt.yaml + "---\nbody\n")

			// Act.
			updated, err := ensureRelationPresentationContext(context.Background(), data, ref(t, "a"), "uses", "b")

			// Assert.
			if err != nil || !bytes.Contains(updated, []byte(tt.want)) {
				t.Fatalf("updated = %q, error=%v", updated, err)
			}
			if _, err := parsePresentationContext(context.Background(), updated); err != nil {
				t.Fatalf("updated presentation does not reparse: %v", err)
			}
		})
	}
}

func TestRenameFragmentPreservesUnrelatedMergeProvenance(t *testing.T) {
	// Arrange. The merge contributes only an opaque label; the explicit id is
	// still the single owned identity route.
	data := []byte("---\nbase: &base\n  label: keep\nparts:\n  - <<: *base\n    id: old\n---\nbody\n")
	p, err := parsePresentationContext(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	patch, found, err := fragmentPatchContext(context.Background(), p, "old", "new")
	updated, patchErr := p.patchYAMLContext(context.Background(), []bytePatch{patch})

	// Assert.
	if err != nil || patchErr != nil || !found || !bytes.Contains(updated, []byte("  - <<: *base\n    id: new\n")) {
		t.Fatalf("updated = %q, found=%t, errors=%v/%v", updated, found, err, patchErr)
	}
}

func TestEnsureRelationAppendsAfterEntireSupportedSequence(t *testing.T) {
	for _, tt := range []struct {
		name, newline string
	}{
		{name: "LF", newline: "\n"},
		{name: "CRLF", newline: "\r\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			nl := tt.newline
			data := []byte("---" + nl + "type: thing" + nl + "relations:" + nl + "  uses:" + nl + "    - target: a # retain" + nl + "      note: keep-a" + nl + "    - target: c" + nl + "      note: keep-c" + nl + "---" + nl + "body")
			updated, err := ensureRelationPresentationContext(context.Background(), data, ref(t, "a"), "uses", "b")
			if err != nil {
				t.Fatal(err)
			}
			want := "    - target: a # retain" + nl + "      note: keep-a" + nl + "    - target: c" + nl + "      note: keep-c" + nl + "    - target: b" + nl
			if !bytes.Contains(updated, []byte(want)) {
				t.Fatalf("sequence order/comment/newline = %q", updated)
			}
			after, err := parsePresentationContext(context.Background(), updated)
			if err != nil || !equalStrings(yamlRootRelationTargetsForType(t, after.root, "uses"), []string{"a", "c", "b"}) {
				t.Fatalf("targets = %q, err=%v", yamlRootRelationTargetsForType(t, after.root, "uses"), err)
			}
		})
	}
}

func TestEnsureRelationRejectsAmbiguousInsertionTriviaWithoutStage(t *testing.T) {
	for _, tt := range []struct {
		name, trivia string
	}{
		{name: "comment before next sibling", trivia: "      # ownership is ambiguous\n"},
		{name: "blank line before next sibling", trivia: "\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			s := memorySource{
				"a.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a\n" + tt.trivia + "other: keep\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			}
			before := append([]byte(nil), s["a.md"]...)

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}))

			// Assert.
			var presentation *PresentationError
			if !errors.Is(err, ErrUnsupportedPresentation) || !errors.As(err, &presentation) || presentation.Code != "ambiguous_insertion_trivia" || result.Staged != nil || !bytes.Equal(before, s["a.md"]) {
				t.Fatalf("Plan() = %#v, %#v", result, err)
			}
		})
	}
}

func TestEnsureRelationAllowsQuotedExtensionMarkerData(t *testing.T) {
	for _, quote := range []string{"'", `"`} {
		for _, marker := range []string{"!!not-a-tag", " &not-an-anchor", " *not-an-alias", "<<", "---", "%TAG"} {
			t.Run(quote+marker, func(t *testing.T) {
				// Arrange. These tokens are YAML syntax only outside the quoted
				// extension scalar. The existing relation item remains byte-for-byte
				// intact when the new item is appended.
				value := marker
				if quote == "'" {
					value = strings.ReplaceAll(value, "'", "''")
				}
				data := []byte("---\r\nrelations:\r\n  uses:\r\n    - target: a # keep\r\n      note: " + quote + value + quote + "\r\n---\r\nbody\r\n")

				// Act.
				updated, err := ensureRelationPresentationContext(context.Background(), data, ref(t, "a"), "uses", "b")

				// Assert.
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(updated, []byte("    - target: a # keep\r\n      note: "+quote+value+quote+"\r\n    - target: b\r\n")) {
					t.Fatalf("relation order or extension bytes changed: %q", updated)
				}
			})
		}
	}
}

func TestEnsureRelationRejectsSyntaxProvenanceWithoutStage(t *testing.T) {
	for _, tt := range []struct {
		name, relations string
		want            error
	}{
		{"tagged-item", "    - !!map\n      target: a\n", ErrUnsupportedPresentation},
		{"non-specific-tagged-item", "    - !\n      target: a\n", ErrUnsupportedPresentation},
		{"tagged-target", "    - target: !!str a\n", ErrUnsupportedPresentation},
		{"non-specific-tagged-target", "    - target: ! a\n", ErrUnsupportedPresentation},
		{"anchored-item", "    - &item\n      target: a\n", ErrUnsupportedPresentation},
		{"aliased-item", "    - &item {target: a}\n    - *item\n", ErrAmbiguousPresentation},
		{"merge", "    - <<: {target: a}\n", ErrAmbiguousPresentation},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			s := memorySource{"a.md": []byte("---\ntype: thing\nrelations:\n  uses:\n" + tt.relations + "---\nbody\n"), "b.md": []byte("---\ntype: thing\n---\nB\n")}
			before := append([]byte(nil), s["a.md"]...)

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}))

			// Assert.
			if !errors.Is(err, tt.want) || result.Staged != nil || !bytes.Equal(before, s["a.md"]) {
				t.Fatalf("Plan() = %#v, %v", result, err)
			}
		})
	}
}

func TestPlannerRejectsMergeDerivedTouchedKeysWithoutStage(t *testing.T) {
	tests := []struct {
		name, frontmatter, marker, startMarker string
		source                                 bundle.RelationRef
	}{
		{
			name:        "relations",
			frontmatter: "type: thing\nbase: &base\n  relations:\n    uses:\n      - target: b\n<<: *base\n",
			marker:      "<<: *base",
			source:      ref(t, "a"),
		},
		{
			name:        "relation type",
			frontmatter: "type: thing\nbase: &base\n  uses:\n    - target: b\nrelations:\n  <<: *base\n",
			marker:      "<<: *base",
			source:      ref(t, "a"),
		},
		{
			name:        "target",
			frontmatter: "type: thing\nbase: &base\n  target: b\nrelations:\n  uses:\n    - <<: *base\n",
			marker:      "<<: *base",
			source:      ref(t, "a"),
		},
		{
			name:        "canonical fragment",
			frontmatter: "type: thing\nbase: &base\n  id: part\nparts:\n  - <<: *base\n",
			marker:      "<<: *base",
			startMarker: "part\n",
			source:      ref(t, "a#part"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			data := []byte("---\n" + tt.frontmatter + "---\nbody\n")
			source := memorySource{
				"a.md": data,
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			}
			before := append([]byte(nil), data...)
			op := store.EnsureRelation{Source: tt.source, Type: "uses", Target: ref(t, "b")}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, op))

			// Assert.
			var presentation *PresentationError
			startMarker := tt.marker
			if tt.startMarker != "" {
				startMarker = tt.startMarker
			}
			start := bytes.Index(data, []byte(startMarker))
			want := SourceSpan{Start: start, End: bytes.Index(data, []byte(tt.marker)) + len(tt.marker)}
			if !errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) || presentation.Code != "alias_provenance" || presentation.Location != want {
				t.Fatalf("Plan() error = %#v, location=%#v, want alias_provenance at %#v", err, presentation, want)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || !bytes.Equal(source["a.md"], before) {
				t.Fatalf("Plan() exposed merge-derived mutation: %#v", result)
			}
		})
	}
}

func TestPlannerRejectsMergeDerivedUpdateLookupsWithoutStage(t *testing.T) {
	tests := []struct {
		name, frontmatter, startMarker string
		op                             func(*testing.T) store.Operation
	}{
		{
			name:        "move relation block",
			frontmatter: "type: thing\nbase: &base\n  relations:\n    uses:\n      - target: b\n<<: *base\n",
			op: func(t *testing.T) store.Operation {
				return store.MoveConcept{From: ref(t, "b").ID, To: ref(t, "moved/b").ID}
			},
		},
		{
			name:        "move relation type",
			frontmatter: "type: thing\nbase: &base\n  uses:\n    - target: b\nrelations:\n  <<: *base\n",
			op: func(t *testing.T) store.Operation {
				return store.MoveConcept{From: ref(t, "b").ID, To: ref(t, "moved/b").ID}
			},
		},
		{
			name:        "move target",
			frontmatter: "type: thing\nbase: &base\n  target: b\nrelations:\n  uses:\n    - <<: *base\n",
			op: func(t *testing.T) store.Operation {
				return store.MoveConcept{From: ref(t, "b").ID, To: ref(t, "moved/b").ID}
			},
		},
		{
			name:        "rename canonical fragment",
			frontmatter: "type: thing\nbase: &base\n  id: part\nparts:\n  - <<: *base\n",
			startMarker: "part\n",
			op: func(t *testing.T) store.Operation {
				return store.RenameFragment{Concept: ref(t, "a").ID, From: "part", To: "renamed"}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			data := []byte("---\n" + tt.frontmatter + "---\nbody\n")
			source := memorySource{
				"a.md": data,
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			}
			before := append([]byte(nil), data...)

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, tt.op(t)))

			// Assert.
			var presentation *PresentationError
			marker := "<<: *base"
			startMarker := marker
			if tt.startMarker != "" {
				startMarker = tt.startMarker
			}
			start := bytes.Index(data, []byte(startMarker))
			want := SourceSpan{Start: start, End: bytes.Index(data, []byte(marker)) + len(marker)}
			if !errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) || presentation.Code != "alias_provenance" || presentation.Location != want {
				t.Fatalf("Plan() error = %#v, location=%#v, want alias_provenance at %#v", err, presentation, want)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || !bytes.Equal(source["a.md"], before) {
				t.Fatalf("Plan() exposed merge-derived update: %#v", result)
			}
		})
	}
}

func TestPlannerYAMLAmbiguityLocationsCoverConcreteCandidates(t *testing.T) {
	tests := []struct {
		name, frontmatter, code, token string
		op                             func(*testing.T) store.Operation
	}{
		{
			name:        "duplicate relations",
			frontmatter: "type: thing\nrelations:\n  uses: []\nrelations:\n  uses: []\n",
			code:        "duplicate_relations",
			token:       "relations",
			op: func(t *testing.T) store.Operation {
				return store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}
			},
		},
		{
			name:        "duplicate relation type",
			frontmatter: "type: thing\nrelations:\n  uses: []\n  uses: []\n",
			code:        "duplicate_relation_type",
			token:       "uses",
			op: func(t *testing.T) store.Operation {
				return store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}
			},
		},
		{
			name:        "duplicate target",
			frontmatter: "type: thing\nrelations:\n  uses:\n    - target: c\n      target: c\n",
			code:        "duplicate_target",
			token:       "target",
			op: func(t *testing.T) store.Operation {
				return store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}
			},
		},
		{
			name:        "duplicate canonical fragment",
			frontmatter: "type: thing\nparts:\n  - id: part\n    id: part\n",
			code:        "duplicate_canonical_fragment",
			token:       "id",
			op: func(t *testing.T) store.Operation {
				return store.EnsureRelation{Source: ref(t, "a#part"), Type: "uses", Target: ref(t, "b")}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			data := []byte("---\n" + tt.frontmatter + "---\nbody\n")
			source := memorySource{
				"a.md": data,
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
				"c.md": []byte("---\ntype: thing\n---\nC\n"),
			}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, tt.op(t)))

			// Assert.
			var presentation *PresentationError
			start := bytes.Index(data, []byte(tt.token))
			end := bytes.LastIndex(data, []byte(tt.token)) + len(tt.token)
			want := SourceSpan{Start: start, End: end}
			if !errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) || presentation.Code != tt.code || presentation.Location != want {
				t.Fatalf("Plan() error = %#v, location=%#v, want %s at %#v", err, presentation, tt.code, want)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
				t.Fatalf("Plan() exposed ambiguous mutation: %#v", result)
			}
		})
	}
}

func TestEnsureRelationRejectsUnsupportedCanonicalSelectorProvenance(t *testing.T) {
	tests := []struct {
		name, canonical, code string
		wantKind              error
		targetSelector        bool
		idempotent            bool
	}{
		{name: "source id anchor", canonical: "id: &sel part", code: "touched_anchor", wantKind: ErrUnsupportedPresentation},
		{name: "source anchor anchor", canonical: "anchor: &sel part", code: "touched_anchor", wantKind: ErrUnsupportedPresentation},
		{name: "source explicit tag", canonical: "id: !!str part", code: "explicit_tag", wantKind: ErrUnsupportedPresentation},
		{name: "source non-specific tag", canonical: "id: ! part", code: "explicit_tag", wantKind: ErrUnsupportedPresentation},
		{name: "source idempotent tag", canonical: "id: !!str part", code: "explicit_tag", wantKind: ErrUnsupportedPresentation, idempotent: true},
		{name: "target id anchor", canonical: "id: &sel part", code: "touched_anchor", wantKind: ErrUnsupportedPresentation, targetSelector: true},
		{name: "target explicit tag", canonical: "anchor: !!str part", code: "explicit_tag", wantKind: ErrUnsupportedPresentation, targetSelector: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			selected := []byte("---\ntype: thing\nparts:\n  - " + tt.canonical + "\n---\nselected\n")
			a := []byte("---\ntype: thing\n---\nA\n")
			b := []byte("---\ntype: thing\n---\nB\n")
			sourceRef, targetRef := ref(t, "a#part"), ref(t, "b")
			selectedPath := "a.md"
			if tt.targetSelector {
				sourceRef, targetRef = ref(t, "a"), ref(t, "b#part")
				selectedPath = "b.md"
				b = selected
			} else {
				a = selected
			}
			if tt.idempotent {
				a = []byte("---\ntype: thing\nparts:\n  - " + tt.canonical + "\n    relations:\n      uses:\n        - target: b\n---\nselected\n")
			}
			source := memorySource{"a.md": a, "b.md": b}
			before := append([]byte(nil), source[selectedPath]...)

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, store.EnsureRelation{Source: sourceRef, Type: "uses", Target: targetRef}))

			// Assert.
			var presentation *PresentationError
			if !errors.Is(err, tt.wantKind) || !errors.As(err, &presentation) || presentation.Code != tt.code || presentation.Path != selectedPath || presentation.Location.Start >= presentation.Location.End {
				t.Fatalf("Plan() error = %#v, presentation=%#v", err, presentation)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 || !bytes.Equal(source[selectedPath], before) {
				t.Fatalf("Plan() exposed selector mutation: %#v", result)
			}
		})
	}
}

func TestPlannerRejectsCanonicalIdentityThroughDirectAlias(t *testing.T) {
	presentations := []struct {
		name, frontmatter, alias string
	}{
		{name: "mapping alias", frontmatter: "type: thing\nbase: &part\n  id: old\nparts:\n  - *part\n", alias: "*part"},
		{name: "sequence alias", frontmatter: "type: thing\nbase: &parts\n  - id: old\nparts:\n  - *parts\n", alias: "*parts"},
	}
	routes := []string{"rename", "ensure source", "ensure target"}
	for _, presentationCase := range presentations {
		for _, route := range routes {
			t.Run(presentationCase.name+"/"+route, func(t *testing.T) {
				// Arrange.
				selected := []byte("---\n" + presentationCase.frontmatter + "---\nselected\n")
				plain := []byte("---\ntype: thing\n---\nplain\n")
				source := memorySource{"a.md": selected, "b.md": plain}
				selectedPath := "a.md"
				var operation store.Operation
				switch route {
				case "rename":
					operation = store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}
				case "ensure source":
					operation = store.EnsureRelation{Source: ref(t, "a#old"), Type: "uses", Target: ref(t, "b")}
				case "ensure target":
					source["a.md"], source["b.md"] = plain, selected
					selectedPath = "b.md"
					operation = store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b#old")}
				}
				before := append([]byte(nil), source[selectedPath]...)

				// Act.
				result, err := Plan(context.Background(), source, change(t, source, operation))

				// Assert.
				var presentation *PresentationError
				aliasAt := bytes.LastIndex(source[selectedPath], []byte(presentationCase.alias))
				if !errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) || presentation.Code != "alias_provenance" || presentation.Location.Start >= aliasAt || presentation.Location.End != aliasAt+len(presentationCase.alias) {
					t.Fatalf("Plan() error = %#v, presentation=%#v, alias=%d", err, presentation, aliasAt)
				}
				if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 || !bytes.Equal(source[selectedPath], before) {
					t.Fatalf("Plan() exposed alias-derived identity mutation: %#v", result)
				}
			})
		}
	}
}

func TestEnsureRelationPreservesTaggedUnrelatedExtensions(t *testing.T) {
	for _, tagged := range []string{"!!str keep", "! keep"} {
		t.Run(tagged, func(t *testing.T) {
			// Arrange.
			s := memorySource{
				"a.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a\n      note: " + tagged + "\n---\nbody\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			}

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}))

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			updated, readErr := result.Staged.ReadFile(context.Background(), "a.md")
			if readErr != nil || !bytes.Contains(updated, []byte("      note: "+tagged+"\n    - target: b\n")) {
				t.Fatalf("updated = %q, error=%v", updated, readErr)
			}
		})
	}
}

func TestEnsureRelationRejectsNonSpecificTagsOnStructuralRoute(t *testing.T) {
	frontmatters := []struct {
		name, yaml string
	}{
		{name: "tagged relations mapping", yaml: "type: thing\nrelations: !\n  uses:\n    - target: a\n"},
		{name: "tagged relation sequence", yaml: "type: thing\nrelations:\n  uses: !\n    - target: a\n"},
		{name: "tagged relation key", yaml: "type: thing\nrelations:\n  ! uses:\n    - target: a\n"},
		{name: "tagged structural key", yaml: "type: thing\n! relations:\n  uses:\n    - target: a\n"},
	}
	for _, tt := range frontmatters {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			s := memorySource{
				"a.md": []byte("---\n" + tt.yaml + "---\nA\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			}
			before := append([]byte(nil), s["a.md"]...)

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}))

			// Assert.
			var presentation *PresentationError
			if !errors.Is(err, ErrUnsupportedPresentation) || !errors.As(err, &presentation) || presentation.Code != "explicit_tag" || presentation.Path != "a.md" || presentation.Operation != "ensure_relation" || presentation.Location.Start < 4 || result.Staged != nil || !bytes.Equal(before, s["a.md"]) {
				t.Fatalf("Plan() = %#v, %#v", result, err)
			}
		})
	}
}

func TestBlockPlainScalarResolverPreservesPunctuationAcrossOperations(t *testing.T) {
	t.Run("move relation targets", func(t *testing.T) {
		for _, raw := range []string{"b,c", "b[c]", "b{c}"} {
			t.Run(raw, func(t *testing.T) {
				// Arrange.
				old, err := bundle.ParseConceptID(raw)
				if err != nil {
					t.Fatal(err)
				}
				moved, err := bundle.ParseConceptID("moved/" + raw)
				if err != nil {
					t.Fatal(err)
				}
				files := memorySource{
					"a.md":           []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: " + raw + " # retain\n---\nA\n"),
					conceptPath(old): []byte("---\ntype: thing\n---\nB\n"),
				}

				// Act.
				result, err := Plan(context.Background(), files, change(t, files, store.MoveConcept{From: old, To: moved}))

				// Assert.
				if err != nil {
					t.Fatal(err)
				}
				updated := readSourceFileForTest(t, result.Staged, "a.md")
				if !bytes.Contains(updated, []byte("target: moved/"+raw+" # retain")) {
					t.Fatalf("updated = %q", updated)
				}
			})
		}
	})

	t.Run("rename canonical fragment", func(t *testing.T) {
		files := memorySource{"a.md": []byte("---\ntype: thing\nparts:\n  - id: part,one # retain\n---\nA\n")}
		result, err := Plan(context.Background(), files, change(t, files, store.RenameFragment{Concept: ref(t, "a").ID, From: "part,one", To: "renamed"}))
		if err != nil {
			t.Fatal(err)
		}
		updated := readSourceFileForTest(t, result.Staged, "a.md")
		if !bytes.Contains(updated, []byte("id: renamed # retain")) {
			t.Fatalf("updated = %q", updated)
		}
	})

	t.Run("ensure from punctuation fragment", func(t *testing.T) {
		files := memorySource{
			"a.md": []byte("---\ntype: thing\nparts:\n  - id: part,one\n---\nA\n"),
			"b.md": []byte("---\ntype: thing\n---\nB\n"),
		}
		result, err := Plan(context.Background(), files, change(t, files, store.EnsureRelation{Source: ref(t, "a#part,one"), Type: "uses", Target: ref(t, "b")}))
		if err != nil {
			t.Fatal(err)
		}
		updated := readSourceFileForTest(t, result.Staged, "a.md")
		if !bytes.Contains(updated, []byte("id: part,one\n    relations:\n      uses:\n        - target: b")) {
			t.Fatalf("updated = %q", updated)
		}
	})

}

func TestBlockPlainScalarResolverSupportsMultilineSemanticValue(t *testing.T) {
	// Arrange. yaml.v3 folds this physical scalar to "part, one".
	files := memorySource{"a.md": []byte("---\ntype: thing\nparts:\n  - id: part,\n      one\n---\nA\n")}

	// Act.
	result, err := Plan(context.Background(), files, change(t, files, store.RenameFragment{Concept: ref(t, "a").ID, From: "part, one", To: "renamed"}))

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	updated := readSourceFileForTest(t, result.Staged, "a.md")
	if !bytes.Contains(updated, []byte("- id: renamed\n")) || bytes.Contains(updated, []byte("      one\n")) {
		t.Fatalf("updated = %q", updated)
	}
}

func TestEnsureRelationRejectsMixedSequenceBeforeInsertion(t *testing.T) {
	// This native fuzz seed used to append after the first item and reorder the
	// supported peer. A mixed sequence has no lossless ownership grammar.
	data := []byte("---\nrelations:\n uses:\n    - 0\n    - target: 0\n---\nbody\n")

	updated, err := ensureRelationPresentationContext(context.Background(), data, ref(t, "a"), "uses", "b")

	var presentation *PresentationError
	if updated != nil || !errors.Is(err, ErrUnsupportedPresentation) || !errors.As(err, &presentation) || presentation.Code != "relation_item_presentation" {
		t.Fatalf("ensureRelationPresentationContext(context.Background(), ) = %q, %#v", updated, err)
	}
}

func TestPlan_InvalidYAMLReturnsPresentationErrorWithoutStage(t *testing.T) {
	// Arrange.
	s := memorySource{"a.md": []byte("---\nparts: [\n---\nbody\n")}
	op := store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, op))

	// Assert.
	var presentation *PresentationError
	if !errors.Is(err, ErrUnsupportedPresentation) || !errors.As(err, &presentation) {
		t.Fatalf("Plan() error = %#v", err)
	}
	if presentation.Code != "yaml_syntax" || presentation.Format != "yaml" || presentation.Path != "a.md" || presentation.Operation != "rename_fragment" || presentation.Location.Start != 4 {
		t.Fatalf("PresentationError = %#v", presentation)
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 {
		t.Fatalf("Plan() staged invalid YAML: %#v", result)
	}
}

func TestPlanRejectsNonMappingFrontmatterWithTypedConcreteLocation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{name: "scalar", yaml: "value\n"},
		{name: "sequence", yaml: "- value\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			data := []byte("---\n" + test.yaml + "---\n[A](a.md)\n")
			source := memorySource{
				"a.md":      []byte("---\ntype: thing\n---\nA\n"),
				"broken.md": append([]byte(nil), data...),
			}
			before := append([]byte(nil), source["broken.md"]...)

			// Act.
			presentation, directErr := parsePresentationContext(context.Background(), data)
			result, planErr := Plan(
				context.Background(),
				source,
				change(t, source, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}),
			)

			// Assert.
			var direct *PresentationError
			if presentation != nil || !errors.Is(directErr, ErrUnsupportedPresentation) ||
				!errors.As(directErr, &direct) || direct.Code != "invalid_frontmatter" ||
				direct.Format != "yaml" || direct.Location.Start < 0 ||
				direct.Location.Start >= direct.Location.End || direct.Location.End > len(test.yaml) {
				t.Fatalf("direct presentation/error = %#v / %#v", presentation, directErr)
			}
			var planned *PresentationError
			if !errors.Is(planErr, ErrUnsupportedPresentation) || !errors.As(planErr, &planned) ||
				planned.Code != "invalid_frontmatter" || planned.Format != "yaml" ||
				planned.Path != "broken.md" || planned.Operation != "move_concept" ||
				planned.Location.Start < 0 || planned.Location.Start >= planned.Location.End ||
				planned.Location.End > len(data) {
				t.Fatalf("Plan() error = %#v", planErr)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 ||
				len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
				t.Fatalf("rejected Plan() exposed mutation: %#v", result)
			}
			if !bytes.Equal(source["broken.md"], before) {
				t.Fatal("rejected Plan() mutated source")
			}
		})
	}
}

func TestPlanEmptyYAMLDocumentMapsAbsentLocalSpanToCompleteSource(t *testing.T) {
	// Arrange.
	data := []byte("---\n---\nbody\n")
	source := memorySource{
		"a.md": append([]byte(nil), data...),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
	}

	// Act.
	result, err := Plan(
		context.Background(),
		source,
		change(t, source, store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}),
	)

	// Assert.
	if err == nil {
		t.Fatal("Plan() accepted an empty YAML document")
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
		t.Fatalf("rejected Plan() exposed mutation: %#v, err=%v", result, err)
	}
	var presentation *PresentationError
	if !errors.Is(err, ErrUnsupportedPresentation) || !errors.As(err, &presentation) ||
		presentation.Code != "yaml_syntax" || presentation.Format != "yaml" {
		t.Fatalf("Plan() error = %#v, want YAML yaml_syntax", err)
	}
	if presentation.Location != (SourceSpan{Start: 4, End: 5}) {
		t.Fatalf("public empty-document location = %#v, want closing delimiter [4,5)", presentation.Location)
	}
	if !bytes.Equal(source["a.md"], data) {
		t.Fatal("rejected Plan() mutated source")
	}
}

func TestPlannerTreatsPostFrontmatterYAMLLikeTextAsMarkdownBody(t *testing.T) {
	body := "foo: bar\n---\n[body link](b.md)\n"
	tests := []struct {
		name  string
		files memorySource
		op    store.Operation
		path  string
	}{
		{
			name: "ensure relation",
			files: memorySource{
				"a.md": []byte("---\ntype: thing\n---\n" + body),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			},
			op: store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}, path: "a.md",
		},
		{
			name: "move concept",
			files: memorySource{
				"a.md": []byte("---\ntype: thing\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a\n---\n" + body),
			},
			op: store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}, path: "b.md",
		},
		{
			name: "rename fragment",
			files: memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n---\n" + body),
				"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a#old\n---\nB\n"),
			},
			op: store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}, path: "a.md",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			changeSet := change(t, tt.files, tt.op)

			// Act.
			result, err := Plan(context.Background(), tt.files, changeSet)

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			updated, err := result.Staged.ReadFile(context.Background(), tt.path)
			if err != nil || !bytes.Contains(updated, []byte(body)) {
				t.Fatalf("Markdown body was not byte-preserved: %q, err=%v", updated, err)
			}
		})
	}
}

func TestPlannerMapsGeneratedYAMLSyntaxFailureToOriginalInsertionRange(t *testing.T) {
	t.Parallel()

	// Arrange. yaml.v3 accepts the source, but inserting a relation at its raw
	// sibling boundary produces invalid YAML because the CR remains inside the
	// preceding comment line. The generated buffer is longer than the source.
	source := memorySource{
		"a.md": []byte("---\nrelations: \n 00000: #000000000000000000000000000000000000000\r0: 0\n\n---\nbody\n"),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
	}
	op := store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, op))

	// Assert.
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Code != "yaml_syntax" {
		t.Fatalf("Plan() error = %#v, want yaml_syntax PresentationError", err)
	}
	if !presentation.Location.valid(len(source["a.md"])) || presentation.Location.Start >= presentation.Location.End {
		t.Fatalf("Location = %#v for %d-byte original", presentation.Location, len(source["a.md"]))
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
		t.Fatalf("rejected plan exposed staged mutation: %#v", result)
	}
}

func FuzzYAMLResolver(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("\r0\n"),
		[]byte("relations:\n  uses:\n    - target: plain\nparts:\n  - id: old\n"),
		[]byte("relations:\r\n  uses:\r\n    - target: 'single''quote' # comment\r\nparts:\r\n  - anchor: old\r\n"),
		[]byte("relations:\n  uses:\n    - target: \"double\\\\quote\"\nparts:\n  - id: old\n"),
		[]byte("relations: {uses: [b]}\n"),
		[]byte("relations:\n  uses:\n    - target: old\n    - target: old\n"),
		[]byte("value: |\n  block\n"),
		[]byte("target: !!str b\n"),
		[]byte("value: &x b\nother: *x\n"),
		[]byte("base: &base\n  relations:\n    uses:\n      - target: b\n<<: *base\n"),
		[]byte("base: &base\n  uses:\n    - target: b\nrelations:\n  <<: *base\n"),
		[]byte("base: &base\n  target: b\nrelations:\n  uses:\n    - <<: *base\n"),
		[]byte("base: &base\n  id: old\nparts:\n  - <<: *base\n"),
		[]byte("%YAML 1.2\n---\ntarget: b\n"),
		[]byte("target: \xff\n"),
		[]byte("---\n\xff"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, frontmatter []byte) {
		// Arrange.
		fuzzYAMLSupportedSubset(t, frontmatter)
		fuzzYAMLPublicPositiveSubset(t, frontmatter)
		fuzzYAMLKnownRejectionSubset(t, frontmatter)
		data := append([]byte("---\n"), frontmatter...)
		data = append(data, []byte("\n---\nbody\n")...)
		before := append([]byte(nil), data...)

		// Act.
		p, err := parsePresentationContext(context.Background(), data)
		fuzzYAMLPlannerAtomicRejection(t, data)

		// Assert. Arbitrary valid UTF-8 must either be rejected through the
		// presentation contract or exercise every selector route below.
		if !utf8.Valid(data) {
			invalidAt := independentInvalidUTF8Offset(data)
			expectedFormat := "yaml"
			if invalidAt >= independentWrappedMarkdownBodyStart(data) {
				expectedFormat = "markdown"
			}
			assertInvalidEncodingFuzzError(t, err, len(data), expectedFormat, invalidAt)
			return
		}
		if err != nil {
			assertYAMLFuzzError(t, err, len(data))
			return
		}
		fuzzYAMLFragmentRoute(t, p)
		fuzzYAMLRelationTargetRoute(t, p)
		fuzzYAMLEnsureRoute(t, data)
		if !bytes.Equal(data, before) {
			t.Fatal("resolver mutated source input")
		}
	})
}

// fuzzYAMLSupportedSubset maps every arbitrary input to one of four bounded,
// independently known supported presentations. Arbitrary YAML may fail closed,
// but broad rejection of block/flow, LF/CRLF, id/anchor, or unrelated merge
// forms is therefore observable on every fuzz run.
func fuzzYAMLSupportedSubset(t *testing.T, seed []byte) {
	t.Helper()
	sample := seed
	if len(sample) > 32 {
		sample = sample[:32]
	}
	note := hex.EncodeToString(sample)
	variant := byte(0)
	if len(seed) > 0 {
		variant = seed[0] % 4
	}
	type fixture struct {
		data                          []byte
		oldFragment, newFragment      []byte
		oldTarget, newTarget, ensured []byte
	}
	var generated fixture
	switch variant {
	case 0:
		generated = fixture{data: []byte(fmt.Sprintf("---\ntype: thing\nnote: %s\nrelations:\n  uses:\n    - target: old\nparts:\n  - id: old\n---\nbody\n", note)), oldFragment: []byte("id: old"), newFragment: []byte("id: fuzz-fragment"), oldTarget: []byte("target: old"), newTarget: []byte("target: fuzz-target"), ensured: []byte("    - target: b\n")}
	case 1:
		generated = fixture{data: []byte(fmt.Sprintf("---\r\ntype: thing\r\nnote: '%s'\r\nrelations:\r\n  uses:\r\n    - target: 'old'\r\nparts:\r\n  - anchor: 'old'\r\n---\r\nbody\r\n", note)), oldFragment: []byte("anchor: 'old'"), newFragment: []byte("anchor: 'fuzz-fragment'"), oldTarget: []byte("target: 'old'"), newTarget: []byte("target: 'fuzz-target'"), ensured: []byte("    - target: b\r\n")}
	case 2:
		generated = fixture{data: []byte(fmt.Sprintf("---\ntype: thing\nnote: \"%s\"\nopaque: {keep: [one, two]}\nrelations:\n  uses:\n    - target: \"old\"\nparts:\n  - id: \"old\"\n---\nbody\n", note)), oldFragment: []byte("id: \"old\""), newFragment: []byte("id: \"fuzz-fragment\""), oldTarget: []byte("target: \"old\""), newTarget: []byte("target: \"fuzz-target\""), ensured: []byte("    - target: b\n")}
	default:
		generated = fixture{data: []byte(fmt.Sprintf("---\ntype: thing\ndefaults: &defaults {note: \"%s\"}\nrelations:\n  uses:\n    - target: old\n      <<: *defaults\nparts:\n  - id: old\n    <<: *defaults\n---\nbody\n", note)), oldFragment: []byte("id: old"), newFragment: []byte("id: fuzz-fragment"), oldTarget: []byte("target: old"), newTarget: []byte("target: fuzz-target"), ensured: []byte("    - target: b\n")}
	}
	data := generated.data
	if bytes.Count(data, generated.oldFragment) != 1 || bytes.Count(data, generated.oldTarget) != 1 {
		t.Fatal("constructed YAML fixture lost its independently known raw projection")
	}
	p, err := parsePresentationContext(context.Background(), data)
	if err != nil {
		t.Fatalf("generated supported YAML was rejected: %v", err)
	}
	fragment, found, err := fragmentPatchContext(context.Background(), p, "old", "fuzz-fragment")
	if err != nil || !found {
		t.Fatalf("generated supported fragment route = found %t, err %v", found, err)
	}
	fragmentUpdated, err := p.patchYAMLContext(context.Background(), []bytePatch{fragment})
	if err != nil || p.VerifyPatchedContext(context.Background(), fragmentUpdated, []bytePatch{fragment}) != nil {
		t.Fatalf("generated supported fragment patch failed: %v", err)
	}
	if _, err := parsePresentationContext(context.Background(), fragmentUpdated); err != nil || bytes.Count(fragmentUpdated, generated.newFragment) != 1 || bytes.Contains(fragmentUpdated, generated.oldFragment) {
		t.Fatalf("generated supported fragment raw projection = %q, err %v", fragmentUpdated, err)
	}
	if !bytesOutsidePatchesEqualForTest(t, data, fragmentUpdated, []bytePatch{fragment}) {
		t.Fatal("generated supported fragment changed bytes outside its patch")
	}
	targets, err := relationTargetPatchesContext(context.Background(), p, ref(t, "old").ID, ref(t, "fuzz-target").ID, "", "")
	if err != nil || len(targets) != 1 {
		t.Fatalf("generated supported target route = %d patches, err %v", len(targets), err)
	}
	targetUpdated, err := p.patchYAMLContext(context.Background(), targets)
	if err != nil || p.VerifyPatchedContext(context.Background(), targetUpdated, targets) != nil {
		t.Fatalf("generated supported target patch failed: %v", err)
	}
	if _, err := parsePresentationContext(context.Background(), targetUpdated); err != nil || bytes.Count(targetUpdated, generated.newTarget) != 1 || bytes.Contains(targetUpdated, generated.oldTarget) {
		t.Fatalf("generated supported target raw projection = %q, err %v", targetUpdated, err)
	}
	if !bytesOutsidePatchesEqualForTest(t, data, targetUpdated, targets) {
		t.Fatal("generated supported target changed bytes outside its patch")
	}
	ensured, err := ensureRelationPresentationContext(context.Background(), data, ref(t, "a"), "uses", "b")
	if err != nil {
		t.Fatalf("generated supported ensure route was rejected: %v", err)
	}
	if _, err := parsePresentationContext(context.Background(), ensured); err != nil || bytes.Count(ensured, generated.oldTarget) != 1 || bytes.Count(ensured, generated.ensured) != 1 {
		t.Fatalf("generated supported ensure raw projection = %q, err %v", ensured, err)
	}
}

type yamlKnownRejection struct {
	name                 string
	data                 []byte
	operation            store.Operation
	operationName, code  string
	kind                 error
	spanStart, spanEnd   []byte
	spanStartValueOffset int
	expectedRange        SourceSpan
}

func yamlKnownRejectionMatrix(t *testing.T) []yamlKnownRejection {
	t.Helper()
	a, b := ref(t, "a"), ref(t, "b")
	duplicateTarget := []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: b\n    - target: b\n---\nA\n")
	duplicateFragment := []byte("---\ntype: thing\nparts:\n  - id: old\n  - id: old\n---\nA\n")
	mergedTarget := []byte("---\ntype: thing\nbase: &base\n  target: b\nrelations:\n  uses:\n    - <<: *base\n---\nA\n")
	mergedFragment := []byte("---\ntype: thing\nbase: &base\n  id: old\nparts:\n  - <<: *base\n---\nA\n")
	return []yamlKnownRejection{
		{name: "move duplicate target", data: duplicateTarget, operation: store.MoveConcept{From: b.ID, To: ref(t, "moved/b").ID}, operationName: "move_concept", code: "duplicate_target", kind: ErrAmbiguousPresentation, spanStart: []byte("target: b"), spanEnd: []byte("target: b"), spanStartValueOffset: len("target: ")},
		{name: "ensure duplicate target", data: duplicateTarget, operation: store.EnsureRelation{Source: a, Type: "uses", Target: b}, operationName: "ensure_relation", code: "duplicate_target", kind: ErrAmbiguousPresentation, expectedRange: SourceSpan{Start: 41, End: 64}},
		{name: "rename duplicate fragment", data: duplicateFragment, operation: store.RenameFragment{Concept: a.ID, From: "old", To: "new"}, operationName: "rename_fragment", code: "duplicate_canonical_fragment", kind: ErrAmbiguousPresentation, spanStart: []byte("id: old"), spanEnd: []byte("id: old"), spanStartValueOffset: len("id: ")},
		{name: "move merged target", data: mergedTarget, operation: store.MoveConcept{From: b.ID, To: ref(t, "moved/b").ID}, operationName: "move_concept", code: "alias_provenance", kind: ErrAmbiguousPresentation, spanStart: []byte("<<: *base"), spanEnd: []byte("<<: *base")},
		{name: "ensure merged target", data: mergedTarget, operation: store.EnsureRelation{Source: a, Type: "uses", Target: b}, operationName: "ensure_relation", code: "alias_provenance", kind: ErrAmbiguousPresentation, spanStart: []byte("<<: *base"), spanEnd: []byte("<<: *base")},
		{name: "rename merged fragment", data: mergedFragment, operation: store.RenameFragment{Concept: a.ID, From: "old", To: "new"}, operationName: "rename_fragment", code: "alias_provenance", kind: ErrAmbiguousPresentation, expectedRange: SourceSpan{Start: 34, End: 58}},
	}
}

func TestYAMLKnownRejectionOracleMatrix(t *testing.T) {
	for _, fixture := range yamlKnownRejectionMatrix(t) {
		t.Run(fixture.name, func(t *testing.T) {
			assertYAMLKnownRejection(t, fixture)
		})
	}
}

func fuzzYAMLKnownRejectionSubset(t *testing.T, seed []byte) {
	t.Helper()
	fixtures := yamlKnownRejectionMatrix(t)
	assertYAMLKnownRejection(t, fixtures[len(seed)%len(fixtures)])
}

func assertYAMLKnownRejection(t *testing.T, fixture yamlKnownRejection) {
	t.Helper()
	source := memorySource{
		"a.md": append([]byte(nil), fixture.data...),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
	}
	result, err := Plan(context.Background(), source, change(t, source, fixture.operation))
	var presentation *PresentationError
	if !errors.Is(err, fixture.kind) || !errors.As(err, &presentation) || presentation.Code != fixture.code || presentation.Operation != fixture.operationName || presentation.Path != "a.md" || presentation.Format != "yaml" {
		t.Fatalf("Plan() error = %#v, want %s/%s", err, fixture.operationName, fixture.code)
	}
	want := fixture.expectedRange
	if want == (SourceSpan{}) {
		start := bytes.Index(fixture.data, fixture.spanStart)
		endStart := bytes.LastIndex(fixture.data, fixture.spanEnd)
		if start < 0 || endStart < 0 {
			t.Fatal("known rejection fixture lost its declared raw range")
		}
		start += fixture.spanStartValueOffset
		want = SourceSpan{Start: start, End: endStart + len(fixture.spanEnd)}
	}
	if presentation.Location != want {
		t.Fatalf("Location = %#v, want independently constructed %#v", presentation.Location, want)
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
		t.Fatalf("rejected known fixture exposed staged mutation: %#v", result)
	}
	if !bytes.Equal(source["a.md"], fixture.data) {
		t.Fatal("known rejection mutated source bytes")
	}
}

type yamlPublicPositive struct {
	name        string
	source      memorySource
	operation   store.Operation
	wantFiles   map[string][]byte
	wantWrites  map[string][]byte
	wantDeletes []string
	wantRenames []store.Rename
}

func yamlPublicPositiveMatrix(t *testing.T) []yamlPublicPositive {
	t.Helper()
	b := ref(t, "b")
	validB := []byte("---\ntype: thing\n---\nB\n")
	renameBefore := []byte("---\ntype: thing\nparts:\n  - id: old\n---\nA\n")
	renameAfter := []byte("---\ntype: thing\nparts:\n  - id: new\n---\nA\n")
	moveBefore := []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: b\n---\nA\n")
	moveAfter := []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: moved/b\n---\nA\n")
	ensureBefore := []byte("---\ntype: thing\n---\nA\n")
	ensureAfter := []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: b\n---\nA\n")
	return []yamlPublicPositive{
		{name: "rename", source: memorySource{"a.md": renameBefore, "b.md": validB}, operation: store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}, wantFiles: map[string][]byte{"a.md": renameAfter, "b.md": validB}, wantWrites: map[string][]byte{"a.md": renameAfter}},
		{name: "move", source: memorySource{"a.md": moveBefore, "b.md": validB}, operation: store.MoveConcept{From: b.ID, To: ref(t, "moved/b").ID}, wantFiles: map[string][]byte{"a.md": moveAfter, "moved/b.md": validB}, wantWrites: map[string][]byte{"a.md": moveAfter, "moved/b.md": validB}, wantDeletes: []string{"b.md"}, wantRenames: []store.Rename{{From: "b.md", To: "moved/b.md"}}},
		{name: "ensure", source: memorySource{"a.md": ensureBefore, "b.md": validB}, operation: store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: b}, wantFiles: map[string][]byte{"a.md": ensureAfter, "b.md": validB}, wantWrites: map[string][]byte{"a.md": ensureAfter}},
	}
}

func TestYAMLPublicPositiveOracleMatrix(t *testing.T) {
	for _, fixture := range yamlPublicPositiveMatrix(t) {
		t.Run(fixture.name, func(t *testing.T) {
			assertYAMLPublicPositive(t, fixture)
		})
	}
}

func fuzzYAMLPublicPositiveSubset(t *testing.T, seed []byte) {
	t.Helper()
	fixtures := yamlPublicPositiveMatrix(t)
	assertYAMLPublicPositive(t, fixtures[len(seed)%len(fixtures)])
}

func assertYAMLPublicPositive(t *testing.T, fixture yamlPublicPositive) {
	t.Helper()
	source := make(memorySource, len(fixture.source))
	for path, data := range fixture.source {
		source[path] = append([]byte(nil), data...)
	}
	result, err := Plan(context.Background(), source, change(t, source, fixture.operation))
	if err != nil || result.Staged == nil {
		t.Fatalf("Plan() = %#v, %v", result, err)
	}
	if len(result.Preview.Plan) != 1 || !equalYAMLFuzzOperation(result.Preview.Plan[0].Operation, fixture.operation) || !equalStrings(result.Preview.Deletes, fixture.wantDeletes) || !equalYAMLFuzzRenames(result.Preview.Renames, fixture.wantRenames) {
		t.Fatalf("preview plan = %#v, deletes = %#v, renames = %#v", result.Preview.Plan, result.Preview.Deletes, result.Preview.Renames)
	}
	if len(result.Preview.Writes) != len(fixture.wantWrites) {
		t.Fatalf("preview writes = %#v", result.Preview.Writes)
	}
	for _, write := range result.Preview.Writes {
		want, ok := fixture.wantWrites[write.Path]
		if !ok || !bytes.Equal(write.Content, want) || write.Digest == "" {
			t.Fatalf("preview write = %#v, want %q", write, want)
		}
	}
	paths, err := result.Staged.Paths(context.Background())
	if err != nil || len(paths) != len(fixture.wantFiles) {
		t.Fatalf("staged paths = %q, err=%v", paths, err)
	}
	for path, want := range fixture.wantFiles {
		got, readErr := result.Staged.ReadFile(context.Background(), path)
		if readErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("staged %s = %q, want %q, err=%v", path, got, want, readErr)
		}
	}
	for path, before := range fixture.source {
		if !bytes.Equal(source[path], before) {
			t.Fatalf("public positive plan mutated source %s", path)
		}
	}
}

func equalYAMLFuzzRenames(left, right []store.Rename) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalYAMLFuzzOperation(left, right store.Operation) bool {
	switch left := left.(type) {
	case store.RenameFragment:
		right, ok := right.(store.RenameFragment)
		return ok && left.Concept.String() == right.Concept.String() && left.From == right.From && left.To == right.To
	case store.MoveConcept:
		right, ok := right.(store.MoveConcept)
		return ok && left.From.String() == right.From.String() && left.To.String() == right.To.String()
	case store.EnsureRelation:
		right, ok := right.(store.EnsureRelation)
		return ok && left.Source.String() == right.Source.String() && left.Type == right.Type && left.Target.String() == right.Target.String()
	default:
		return false
	}
}

func fuzzYAMLPlannerAtomicRejection(t *testing.T, data []byte) {
	t.Helper()
	sourceBytes := append([]byte(nil), data...)
	source := memorySource{
		"a.md": sourceBytes,
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
	}
	a := ref(t, "a")
	b := ref(t, "b")
	operations := []store.Operation{
		store.RenameFragment{Concept: a.ID, From: "old", To: "fuzz-fragment"},
		store.MoveConcept{From: b.ID, To: ref(t, "moved/b").ID},
		store.EnsureRelation{Source: a, Type: "uses", Target: b},
	}
	for _, operation := range operations {
		result, err := Plan(context.Background(), source, change(t, source, operation))
		if err != nil {
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
				t.Fatalf("rejected fuzz plan exposed staged mutation: %#v, err=%v", result, err)
			}
			var presentation *PresentationError
			if errors.As(err, &presentation) && (presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End || presentation.Location.End > len(data)) {
				t.Fatalf("planner rejection location = %#v for %d-byte source: %v", presentation.Location, len(data), err)
			}
			if (errors.Is(err, ErrUnsupportedPresentation) || errors.Is(err, ErrAmbiguousPresentation) || errors.Is(err, bundle.ErrInvalidEncoding)) && presentation == nil {
				t.Fatalf("presentation rejection lacks exact typed context: %#v", err)
			}
		} else if result.Staged == nil {
			t.Fatal("accepted fuzz plan returned no staged source")
		} else {
			assertYAMLFuzzAcceptedPlan(t, result, data, operation)
		}
		if !bytes.Equal(source["a.md"], sourceBytes) || !bytes.Equal(source["a.md"], data) {
			t.Fatal("planner mutated rejected source bytes")
		}
	}
}

func assertYAMLFuzzAcceptedPlan(t *testing.T, result Result, before []byte, operation store.Operation) {
	t.Helper()
	if len(result.Preview.Plan) != 1 || !equalYAMLFuzzOperation(result.Preview.Plan[0].Operation, operation) {
		t.Fatalf("accepted fuzz plan = %#v, want one exact operation", result.Preview.Plan)
	}
	for _, write := range result.Preview.Writes {
		staged, err := result.Staged.ReadFile(context.Background(), write.Path)
		if err != nil || !bytes.Equal(staged, write.Content) || write.Digest == "" {
			t.Fatalf("accepted fuzz write = %#v, staged=%q, err=%v", write, staged, err)
		}
	}
	after, err := result.Staged.ReadFile(context.Background(), "a.md")
	if err != nil {
		t.Fatalf("accepted fuzz plan lost a.md: %v", err)
	}
	switch operation := operation.(type) {
	case store.RenameFragment:
		if len(result.Preview.Renames) != 0 || len(result.Preview.Writes) != 1 || bytes.Equal(after, before) || independentCanonicalFragmentCount(after, operation.To) != 1 || independentCanonicalFragmentCount(after, operation.From) != 0 {
			t.Fatalf("accepted rename result = writes %#v, renames %#v, a.md=%q", result.Preview.Writes, result.Preview.Renames, after)
		}
	case store.MoveConcept:
		wantRename := store.Rename{From: operation.From.String() + ".md", To: operation.To.String() + ".md"}
		if len(result.Preview.Renames) != 1 || result.Preview.Renames[0] != wantRename {
			t.Fatalf("accepted move renames = %#v, want %#v", result.Preview.Renames, wantRename)
		}
		oldCount := independentYAMLTargetCount(before, operation.From.String())
		if independentYAMLTargetCount(after, operation.From.String()) != 0 || independentYAMLTargetCount(after, operation.To.String()) < oldCount {
			t.Fatalf("accepted move target projection: before=%q after=%q", before, after)
		}
		moved, readErr := result.Staged.ReadFile(context.Background(), operation.To.String()+".md")
		if readErr != nil || !bytes.Equal(moved, []byte("---\ntype: thing\n---\nB\n")) {
			t.Fatalf("accepted move destination = %q, err=%v", moved, readErr)
		}
	case store.EnsureRelation:
		if len(result.Preview.Renames) != 0 || independentRootRelationTargetCount(after, operation.Type, operation.Target.String()) != 1 {
			t.Fatalf("accepted ensure projection: renames=%#v a.md=%q", result.Preview.Renames, after)
		}
	}
}

func independentPresentationYAML(data []byte) (*yaml.Node, bool) {
	first := bytes.IndexByte(data, '\n')
	if first < 0 {
		return nil, false
	}
	bodyStart := independentWrappedMarkdownBodyStart(data)
	if bodyStart <= first+1 {
		return nil, false
	}
	closingEnd := bodyStart - 1
	closingStart := bytes.LastIndex(data[:closingEnd], []byte("\n")) + 1
	if closingStart < first+1 {
		return nil, false
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data[first+1:closingStart], &doc); err != nil || len(doc.Content) != 1 {
		return nil, false
	}
	return doc.Content[0], true
}

func independentCanonicalFragmentCount(data []byte, wanted string) int {
	root, ok := independentPresentationYAML(data)
	if !ok {
		return 0
	}
	count := 0
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node == nil {
			return
		}
		if node.Kind == yaml.MappingNode {
			for index := 0; index+1 < len(node.Content); index += 2 {
				key, value := node.Content[index], node.Content[index+1]
				if (key.Value == "id" || key.Value == "anchor") && value.Kind == yaml.ScalarNode && value.Value == wanted {
					count++
				}
			}
		}
		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(root)
	return count
}

func independentYAMLTargetCount(data []byte, wanted string) int {
	root, ok := independentPresentationYAML(data)
	if !ok {
		return 0
	}
	count := 0
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node == nil {
			return
		}
		if node.Kind == yaml.MappingNode {
			for index := 0; index+1 < len(node.Content); index += 2 {
				if node.Content[index].Value == "target" && node.Content[index+1].Value == wanted {
					count++
				}
			}
		}
		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(root)
	return count
}

func independentRootRelationTargetCount(data []byte, relationType, wanted string) int {
	root, ok := independentPresentationYAML(data)
	if !ok || root.Kind != yaml.MappingNode {
		return 0
	}
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value != "relations" || root.Content[index+1].Kind != yaml.MappingNode {
			continue
		}
		relations := root.Content[index+1]
		for relationIndex := 0; relationIndex+1 < len(relations.Content); relationIndex += 2 {
			if relations.Content[relationIndex].Value != relationType || relations.Content[relationIndex+1].Kind != yaml.SequenceNode {
				continue
			}
			count := 0
			for _, item := range relations.Content[relationIndex+1].Content {
				if item.Kind != yaml.MappingNode {
					continue
				}
				for itemIndex := 0; itemIndex+1 < len(item.Content); itemIndex += 2 {
					if item.Content[itemIndex].Value == "target" && item.Content[itemIndex+1].Value == wanted {
						count++
					}
				}
			}
			return count
		}
	}
	return 0
}

func assertYAMLFuzzError(t *testing.T, err error, sourceLength int) {
	t.Helper()
	var presentation *PresentationError
	if (!errors.Is(err, ErrUnsupportedPresentation) && !errors.Is(err, ErrAmbiguousPresentation)) || !errors.As(err, &presentation) || presentation.Format != "yaml" {
		t.Fatalf("untyped YAML rejection = %#v", err)
	}
	assertYAMLFuzzLocation(t, presentation, sourceLength, err)
}

func assertInvalidEncodingFuzzError(t *testing.T, err error, sourceLength int, expectedFormat string, invalidAt int) {
	t.Helper()
	var presentation *PresentationError
	if !errors.Is(err, bundle.ErrInvalidEncoding) || !errors.As(err, &presentation) || presentation.Format != expectedFormat || presentation.Location != (SourceSpan{Start: invalidAt, End: invalidAt + 1}) {
		t.Fatalf("invalid UTF-8 YAML rejection = %#v", err)
	}
	assertYAMLFuzzLocation(t, presentation, sourceLength, err)
}

func independentWrappedMarkdownBodyStart(data []byte) int {
	firstLineEnd := bytes.IndexByte(data, '\n')
	if firstLineEnd < 0 {
		return len(data)
	}
	for start := firstLineEnd + 1; start <= len(data); {
		relativeEnd := bytes.IndexByte(data[start:], '\n')
		end := len(data)
		if relativeEnd >= 0 {
			end = start + relativeEnd
		}
		line := bytes.TrimSuffix(data[start:end], []byte("\r"))
		if bytes.Equal(line, []byte("---")) {
			if end < len(data) {
				return end + 1
			}
			return end
		}
		if relativeEnd < 0 {
			break
		}
		start = end + 1
	}
	return len(data)
}

func independentInvalidUTF8Offset(data []byte) int {
	for offset := 0; offset < len(data); {
		_, size := utf8.DecodeRune(data[offset:])
		if size == 1 && data[offset] >= utf8.RuneSelf {
			return offset
		}
		offset += size
	}
	return -1
}

func assertYAMLFuzzLocation(t *testing.T, presentation *PresentationError, sourceLength int, err error) {
	t.Helper()
	if sourceLength > 0 && (presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End || presentation.Location.End > sourceLength) {
		t.Fatalf("YAML rejection location = %#v for %d-byte source: %v", presentation.Location, sourceLength, err)
	}
}

func fuzzYAMLFragmentRoute(t *testing.T, p *presentation) {
	t.Helper()
	patch, found, err := fragmentPatchContext(context.Background(), p, "old", "fuzz-fragment")
	if err != nil {
		assertYAMLFuzzError(t, err, len(p.data))
		return
	}
	if !found {
		return
	}
	updated, err := p.patchYAMLContext(context.Background(), []bytePatch{patch})
	if err != nil {
		t.Fatalf("accepted fragment patch rejected: %v", err)
	}
	if err := p.VerifyPatchedContext(context.Background(), updated, []bytePatch{patch}); err != nil {
		t.Fatalf("VerifyPatched(fragment) = %v", err)
	}
	if !bytesOutsidePatchesEqualForTest(t, p.data, updated, []bytePatch{patch}) {
		t.Fatal("fragment patch changed bytes outside its actual span")
	}
	after, err := parsePresentationContext(context.Background(), updated)
	if err != nil || canonicalFragmentCount(t, after.root, "fuzz-fragment") != 1 {
		t.Fatalf("fragment semantic projection = %#v, err=%v", after, err)
	}
}

func fuzzYAMLRelationTargetRoute(t *testing.T, p *presentation) {
	t.Helper()
	before := yamlRelationTargets(t, p.root)
	hasDuplicate := yamlHasDuplicateUpdateTarget(t, p.root, ref(t, "old").ID, "")
	patches, err := relationTargetPatchesContext(context.Background(), p, ref(t, "old").ID, ref(t, "fuzz-target").ID, "", "")
	if err != nil {
		assertYAMLFuzzError(t, err, len(p.data))
		return
	}
	if hasDuplicate {
		t.Fatal("duplicate relation target update was accepted")
	}
	if len(patches) == 0 {
		return
	}
	updated, err := p.patchYAMLContext(context.Background(), patches)
	if err != nil {
		t.Fatalf("accepted target patches rejected: %v", err)
	}
	if err := p.VerifyPatchedContext(context.Background(), updated, patches); err != nil {
		t.Fatalf("VerifyPatched(targets) = %v", err)
	}
	if !bytesOutsidePatchesEqualForTest(t, p.data, updated, patches) {
		t.Fatal("relation target patch changed bytes outside actual spans")
	}
	after, err := parsePresentationContext(context.Background(), updated)
	if err != nil {
		t.Fatalf("target output did not reparse: %v", err)
	}
	want := append([]string(nil), before...)
	for i, value := range want {
		if value == "old" {
			want[i] = "fuzz-target"
		}
	}
	if !equalStrings(yamlRelationTargets(t, after.root), want) {
		t.Fatalf("ordered relation targets = %q, want %q", yamlRelationTargets(t, after.root), want)
	}
}

func yamlHasDuplicateUpdateTarget(t *testing.T, root *yaml.Node, id bundle.ConceptID, fragment string) bool {
	t.Helper()
	duplicate := false
	err := walkMappingsContext(context.Background(), root, includeRootMapping, func(mapping *yaml.Node) error {
		for _, relations := range mappingValuesForTest(t, mapping, "relations") {
			relations = yamlAliasTarget(relations)
			if relations == nil || relations.Kind != yaml.MappingNode {
				continue
			}
			for index := 1; index < len(relations.Content); index += 2 {
				duplicates, err := duplicateRelationTargetsInSequenceContext(context.Background(), relations.Content[index], id, fragment)
				if err != nil {
					t.Fatalf("duplicateRelationTargetsInSequenceContext() error = %v", err)
				}
				if len(duplicates) > 1 {
					duplicate = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walkMappingsContext() error = %v", err)
	}
	return duplicate
}

func fuzzYAMLEnsureRoute(t *testing.T, data []byte) {
	t.Helper()
	p, err := parsePresentationContext(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	beforeUses := yamlRootRelationTargetsForType(t, p.root, "uses")
	beforeOther := yamlRootRelationTargetsExceptType(t, p.root, "uses")
	updated, err := ensureRelationPresentationContext(context.Background(), data, ref(t, "a"), "uses", "b")
	if err != nil {
		assertYAMLFuzzError(t, err, len(data))
		return
	}
	after, err := parsePresentationContext(context.Background(), updated)
	if err != nil {
		t.Fatalf("ensure output did not reparse: %v", err)
	}
	gotUses := yamlRootRelationTargetsForType(t, after.root, "uses")
	wantUses := intendedEnsureTargets(beforeUses, "b")
	if !equalStrings(gotUses, wantUses) {
		t.Fatalf("ensure uses projection = %q, want %q", gotUses, wantUses)
	}
	if gotOther := yamlRootRelationTargetsExceptType(t, after.root, "uses"); !equalStrings(gotOther, beforeOther) {
		t.Fatalf("ensure changed untouched relation targets: got %q, want %q", gotOther, beforeOther)
	}
}

func yamlRelationTargets(t *testing.T, n *yaml.Node) []string {
	var out []string
	err := walkMappingsContext(context.Background(), n, includeRootMapping, func(mapping *yaml.Node) error {
		for _, relations := range mappingValuesForTest(t, mapping, "relations") {
			if relations.Kind != yaml.MappingNode {
				continue
			}
			for i := 1; i < len(relations.Content); i += 2 {
				for _, item := range relations.Content[i].Content {
					for _, target := range mappingValuesForTest(t, item, "target") {
						out = append(out, target.Value)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walkMappingsContext() error = %v", err)
	}
	return out
}

func yamlRootRelationTargetsForType(t *testing.T, root *yaml.Node, wantedType string) []string {
	var out []string
	for _, relations := range mappingValuesForTest(t, root, "relations") {
		if relations.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(relations.Content); i += 2 {
			if relations.Content[i].Value != wantedType {
				continue
			}
			for _, item := range relations.Content[i+1].Content {
				for _, target := range mappingValuesForTest(t, item, "target") {
					out = append(out, target.Value)
				}
			}
		}
	}
	return out
}

func yamlRootRelationTargetsExceptType(t *testing.T, root *yaml.Node, excludedType string) []string {
	var out []string
	for _, relations := range mappingValuesForTest(t, root, "relations") {
		if relations.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(relations.Content); i += 2 {
			if relations.Content[i].Value == excludedType {
				continue
			}
			for _, item := range relations.Content[i+1].Content {
				for _, target := range mappingValuesForTest(t, item, "target") {
					out = append(out, target.Value)
				}
			}
		}
	}
	return out
}

func canonicalFragmentCount(t *testing.T, root *yaml.Node, want string) int {
	t.Helper()
	count := 0
	err := walkMappingsContext(context.Background(), root, excludeRootMapping, func(mapping *yaml.Node) error {
		identity := bundle.ResolveMappingIdentity(mapping)
		if identity.State == bundle.MappingIdentityValid && identity.Fragment == want {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walkMappingsContext() error = %v", err)
	}
	return count
}

func intendedEnsureTargets(values []string, target string) []string {
	out := make([]string, 0, len(values))
	found := false
	for _, value := range values {
		if value != target || !found {
			out = append(out, value)
			if value == target {
				found = true
			}
		}
	}
	if !found {
		out = append(out, target)
	}
	return out
}
