package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

type graphContextRenderer struct {
	name       string
	render     func(context.Context, io.Writer, *bundle.Bundle) error
	renderOld  func(io.Writer, *bundle.Bundle) error
	options    func(context.Context, io.Writer, *bundle.Bundle, Options) error
	optionsOld func(io.Writer, *bundle.Bundle, Options) error
}

func graphContextRenderers() []graphContextRenderer {
	return []graphContextRenderer{
		{name: "text", render: RenderTextContext, renderOld: RenderText, options: RenderTextWithOptionsContext, optionsOld: RenderTextWithOptions},
		{name: "dot", render: RenderDOTContext, renderOld: RenderDOT, options: RenderDOTWithOptionsContext, optionsOld: RenderDOTWithOptions},
		{name: "mermaid", render: RenderMermaidContext, renderOld: RenderMermaid, options: RenderMermaidWithOptionsContext, optionsOld: RenderMermaidWithOptions},
		{name: "jsonld", render: RenderJSONLDContext, renderOld: RenderJSONLD, options: RenderJSONLDWithOptionsContext, optionsOld: RenderJSONLDWithOptions},
		{name: "ntriples", render: RenderNTriplesContext, renderOld: RenderNTriples, options: RenderNTriplesWithOptionsContext, optionsOld: RenderNTriplesWithOptions},
	}
}

func TestGraphContextRenderersCancelBeforeAndDuringWork(t *testing.T) {
	// Arrange.
	b := largeContextGraphBundle(t, false)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, renderer := range graphContextRenderers() {
		renderer := renderer
		t.Run(renderer.name+"/pre-canceled", func(t *testing.T) {
			// Act.
			err := renderer.render(canceled, io.Discard, b)

			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s pre-canceled error = %v, want context.Canceled", renderer.name, err)
			}
		})
		t.Run(renderer.name+"/mid-phase-countdown", func(t *testing.T) {
			// Arrange.
			ctx := newGraphCountdownContext(24)
			var output strings.Builder

			// Act.
			err := renderer.options(ctx, &output, b, Options{
				Profile:            ProjectionProfileToolkitV02,
				ExtensionRelations: ExtensionRelationsInclude,
				AnnotateTopology:   true,
			})

			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s countdown error = %v, want context.Canceled", renderer.name, err)
			}
			if output.Len() != 0 {
				t.Fatalf("%s published %d bytes before cancellation, want exact zero", renderer.name, output.Len())
			}
		})
	}
}

func TestGraphContextWrappersPreserveExactBytes(t *testing.T) {
	// Arrange.
	b := sampleGraphBundle(t)
	options := Options{
		Profile:            ProjectionProfileToolkitV02,
		ExtensionRelations: ExtensionRelationsInclude,
		AnnotateTopology:   true,
	}

	for _, renderer := range graphContextRenderers() {
		renderer := renderer
		t.Run(renderer.name, func(t *testing.T) {
			// Arrange.
			var oldDefault, contextDefault, oldOptions, contextOptions strings.Builder

			// Act.
			errorsSeen := []error{
				renderer.renderOld(&oldDefault, b),
				renderer.render(context.Background(), &contextDefault, b),
				renderer.optionsOld(&oldOptions, b, options),
				renderer.options(context.Background(), &contextOptions, b, options),
			}

			// Assert.
			for _, err := range errorsSeen {
				if err != nil {
					t.Fatalf("%s parity render error = %v", renderer.name, err)
				}
			}
			if oldDefault.String() != contextDefault.String() {
				t.Fatalf("%s default wrapper bytes changed:\nold=%q\ncontext=%q", renderer.name, oldDefault.String(), contextDefault.String())
			}
			if oldOptions.String() != contextOptions.String() {
				t.Fatalf("%s options wrapper bytes changed:\nold=%q\ncontext=%q", renderer.name, oldOptions.String(), contextOptions.String())
			}
		})
	}
}

func TestGraphContextRenderersAreDeterministicForLargeShuffledInput(t *testing.T) {
	// Arrange.
	forward := largeContextGraphBundle(t, false)
	reverse := largeContextGraphBundle(t, true)
	options := Options{
		Profile:            ProjectionProfileToolkitV02,
		ExtensionRelations: ExtensionRelationsInclude,
		AnnotateTopology:   true,
	}

	for _, renderer := range graphContextRenderers() {
		renderer := renderer
		t.Run(renderer.name, func(t *testing.T) {
			// Arrange.
			var first, shuffled strings.Builder

			// Act.
			firstErr := renderer.options(context.Background(), &first, forward, options)
			shuffledErr := renderer.options(context.Background(), &shuffled, reverse, options)

			// Assert.
			if firstErr != nil || shuffledErr != nil {
				t.Fatalf("%s shuffled render errors = %v/%v", renderer.name, firstErr, shuffledErr)
			}
			if first.String() != shuffled.String() {
				t.Fatalf("%s output changed under shuffled source discovery", renderer.name)
			}
		})
	}
}

func TestGraphContextRenderersPreserveObservedWriterFailurePrecedence(t *testing.T) {
	// Arrange.
	b := sampleGraphBundle(t)
	writeErr := errors.New("write sentinel")

	for _, renderer := range graphContextRenderers() {
		renderer := renderer
		t.Run(renderer.name+"/writer-error", func(t *testing.T) {
			// Arrange.
			ctx, cancel := context.WithCancel(context.Background())
			writer := cancelingGraphWriter{cancel: cancel, err: writeErr}

			// Act.
			err := renderer.render(ctx, writer, b)

			// Assert.
			if !errors.Is(err, writeErr) {
				t.Fatalf("%s error = %v, want writer sentinel", renderer.name, err)
			}
		})
		t.Run(renderer.name+"/short-write", func(t *testing.T) {
			// Arrange.
			ctx, cancel := context.WithCancel(context.Background())
			writer := cancelingGraphWriter{cancel: cancel, short: true}

			// Act.
			err := renderer.render(ctx, writer, b)

			// Assert.
			if !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("%s error = %v, want io.ErrShortWrite", renderer.name, err)
			}
		})
		t.Run(renderer.name+"/cancel-after-successful-write", func(t *testing.T) {
			// Arrange.
			ctx, cancel := context.WithCancel(context.Background())
			writer := cancelingGraphWriter{cancel: cancel}

			// Act.
			err := renderer.render(ctx, writer, b)

			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s error = %v, want context.Canceled", renderer.name, err)
			}
		})
	}
}

func TestMermaidAndNTriplesBuildersOwnLargeDynamicValues(t *testing.T) {
	t.Parallel()
	// Arrange.
	large := strings.Repeat("relation&\"\n]", 1<<18)
	tests := []struct {
		name string
		act  func(context.Context) (string, error)
	}{
		{name: "mermaid label", act: func(ctx context.Context) (string, error) { return mermaidLabelContext(ctx, large) }},
		{name: "mermaid node quote", act: func(ctx context.Context) (string, error) {
			return (&mermaidNodeAllocator{}).nodeContext(ctx, large)
		}},
		{name: "ntriples predicate", act: func(ctx context.Context) (string, error) { return ntriplesPredicateContext(ctx, large) }},
		{name: "ntriples IRI", act: func(ctx context.Context) (string, error) { return ntriplesIRIContext(ctx, large) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			got, err := tt.act(newGraphCountdownContext(8))

			// Assert.
			if !errors.Is(err, context.Canceled) || got != "" {
				t.Fatalf("builder=(%d bytes,%v), want exact zero/context.Canceled", len(got), err)
			}
		})
	}

	// Arrange: force a %q chunk boundary through a multibyte rune.
	boundary := strings.Repeat("a", graphFormatChunkBytes-1) + "é\n\""
	var got strings.Builder
	// Act.
	err := writefContext(context.Background(), &got, "%q", boundary)
	// Assert.
	if err != nil || got.String() != fmt.Sprintf("%q", boundary) {
		t.Fatalf("chunked quote parity error=%v", err)
	}
}

func TestSortProjectionCancellationIsTransactionalAtEveryCheckpoint(t *testing.T) {
	t.Parallel()
	// Arrange.
	original := toolkitProjection{Nodes: []projectionNode{
		{ID: "same", Types: []string{"z", "a"}, Properties: map[string][]projectionValue{"p": {{Kind: projectionIRI, Value: "2"}}}},
		{ID: "same", Types: []string{"b"}, Properties: map[string][]projectionValue{"p": {{Kind: projectionLiteral, Value: "1"}}}},
		{ID: "other", Types: []string{"x"}, Properties: map[string][]projectionValue{"q": {{Kind: projectionLiteral, Value: "3"}}}},
	}}
	cancellations := 0
	for allowed := int64(1); allowed < 256; allowed++ {
		projection := toolkitProjection{Nodes: cloneProjectionNodesForTest(original.Nodes)}
		before := cloneProjectionNodesForTest(projection.Nodes)

		// Act.
		err := sortProjectionContext(newGraphCountdownContext(allowed), &projection)

		// Assert.
		if errors.Is(err, context.Canceled) {
			cancellations++
			if !reflect.DeepEqual(projection.Nodes, before) {
				t.Fatalf("allowed=%d mutated projection on cancellation", allowed)
			}
		}
	}
	if cancellations < 8 {
		t.Fatalf("observed %d cancellation checkpoints, want late-phase coverage", cancellations)
	}
}

func TestOrderedJSONObjectPreservesExactKeysAndDuplicateSemantics(t *testing.T) {
	t.Parallel()
	// Arrange: orderedJSONObject intentionally has no digest/hash seam; exact
	// bounded comparison is its collision-safe authority.
	prefix := strings.Repeat("k", 2<<20)
	keys := []string{prefix + "a", prefix + "b", "@id", "@type", "title", "references", "@context"}
	var object orderedJSONObject
	for index, key := range keys {
		if err := object.setContext(context.Background(), key, fmt.Sprintf("value-%d", index)); err != nil {
			t.Fatal(err)
		}
	}
	if err := object.setContext(context.Background(), prefix+"a", "replacement"); err != nil {
		t.Fatal(err)
	}

	// Act.
	var rendered strings.Builder
	err := writeOrderedJSONObjectContext(context.Background(), &rendered, object, 0)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if len(object) != len(keys) {
		t.Fatalf("duplicate exact key appended: len=%d want=%d", len(object), len(keys))
	}
	value, ok, err := object.getContext(context.Background(), prefix+"a")
	if err != nil || !ok || value != "replacement" {
		t.Fatalf("exact duplicate replacement=(%v,%t,%v)", value, ok, err)
	}
	for _, key := range keys[2:] {
		if !strings.Contains(rendered.String(), fmt.Sprintf("%q", key)) {
			t.Fatalf("serialized object lost reserved-like exact key %q", key)
		}
	}

	// Act: cancellation inside a near-collision comparison publishes nothing.
	var canceledOutput strings.Builder
	cancelContext := newGraphCountdownContext(8)
	err = renderTransactionalContext(cancelContext, &canceledOutput, func(spool io.Writer) error {
		return writeOrderedJSONObjectContext(cancelContext, spool, object, 0)
	})

	// Assert.
	if !errors.Is(err, context.Canceled) || canceledOutput.Len() != 0 {
		t.Fatalf("canceled ordered object=(%d bytes,%v), want exact zero/context.Canceled", canceledOutput.Len(), err)
	}
}

func TestRealNTriplesAndLegacyJSONLDOwnDynamicValues(t *testing.T) {
	t.Parallel()
	// Arrange.
	large := strings.Repeat("é", 2<<20)
	id, err := bundle.NewConceptID([]string{large})
	if err != nil {
		t.Fatal(err)
	}
	ref := bundle.RelationRef{ID: id, Fragment: large}
	tests := []struct {
		name string
		act  func(context.Context) (string, error)
	}{
		{name: "concept IRI", act: func(ctx context.Context) (string, error) { return ntriplesConceptIRIContext(ctx, id) }},
		{name: "relation IRI", act: func(ctx context.Context) (string, error) { return ntriplesRelationRefIRIContext(ctx, ref) }},
		{name: "toolkit IRI", act: func(ctx context.Context) (string, error) {
			return toolkitNTriplesValueContext(ctx, projectionValue{Kind: projectionIRI, Value: large})
		}},
		{name: "mixed UTF8 literal", act: func(ctx context.Context) (string, error) { return ntriplesLiteralContext(ctx, large) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.act(newGraphCountdownContext(20))
			if !errors.Is(err, context.Canceled) || got != "" {
				t.Fatalf("real NTriples path=(%d bytes,%v), want zero/context.Canceled", len(got), err)
			}
		})
	}

	// Arrange: BYOT relation defenses must never replace fixed JSON-LD fields,
	// even though Bundle extraction rejects these names earlier.
	for _, reserved := range []string{"@id", "@type", "title", "references", "@context"} {
		node := orderedJSONObject{{key: reserved, value: "fixed"}}
		relation := bundle.Relation{Type: reserved, Target: ref, TargetExists: true}
		if err := appendJSONLDRelationContext(context.Background(), &node, relation); err != nil {
			t.Fatal(err)
		}
		value, ok, err := node.getContext(context.Background(), reserved)
		preserved, preservedOK, preservedErr := node.getContext(context.Background(), "okf:_relations")
		if err != nil || !ok || value != "fixed" || preservedErr != nil || !preservedOK || len(preserved.([]map[string]any)) != 1 || len(node) != 2 {
			t.Fatalf("reserved %q was overwritten: %#v", reserved, node)
		}
	}
}

func TestTextDOTMermaidPreserveLateWriterFailures(t *testing.T) {
	t.Parallel()
	b := sampleGraphBundle(t)
	want := errors.New("late writer")
	for _, renderer := range []struct {
		name string
		run  func(io.Writer) error
	}{
		{name: "text", run: func(w io.Writer) error { return RenderText(w, b) }},
		{name: "dot", run: func(w io.Writer) error { return RenderDOT(w, b) }},
		{name: "mermaid", run: func(w io.Writer) error { return RenderMermaid(w, b) }},
	} {
		t.Run(renderer.name, func(t *testing.T) {
			writer := &failingWriter{failWrite: 3, err: want}
			err := renderer.run(writer)
			if !errors.Is(err, want) || writer.writes != 3 {
				t.Fatalf("late writer=(writes %d,error %v), want 3/sentinel", writer.writes, err)
			}
		})
	}
}

func TestToolkitBuildersAndPublicNTriplesAreContextOwned(t *testing.T) {
	t.Parallel()
	// Arrange.
	large := strings.Repeat("é", 2<<20)
	id, err := bundle.NewConceptID([]string{large})
	if err != nil {
		t.Fatal(err)
	}
	ref := bundle.RelationRef{ID: id, Fragment: large}
	for _, tt := range []struct {
		name string
		act  func(context.Context) (string, error)
	}{
		{name: "toolkit concept", act: func(ctx context.Context) (string, error) { return toolkitConceptIRIContext(ctx, id) }},
		{name: "toolkit ref", act: func(ctx context.Context) (string, error) { return toolkitRelationRefIRIContext(ctx, ref) }},
		{name: "toolkit synthetic", act: func(ctx context.Context) (string, error) { return toolkitSyntheticIRIContext(ctx, "kind", large) }},
		{name: "triple final", act: func(ctx context.Context) (string, error) {
			return ntriplesTripleContext(ctx, large, large, large, true)
		}},
		{name: "literal final", act: func(ctx context.Context) (string, error) { return ntriplesLiteralContext(ctx, large) }},
		{name: "mermaid final", act: func(ctx context.Context) (string, error) { return mermaidLabelContext(ctx, large) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.act(newGraphCountdownContext(8))
			if !errors.Is(err, context.Canceled) || got != "" {
				t.Fatalf("result=(%d,%v), want zero/canceled", len(got), err)
			}
		})
	}

	// Arrange: this is the real public toolkit N-Triples renderer. A synthetic
	// multi-MiB property reaches toolkit projection IRI construction before the
	// transactional spool can publish.
	b := largeContextGraphBundle(t, false)
	var output strings.Builder
	err = RenderNTriplesWithOptionsContext(newGraphCountdownContext(64), &output, b, Options{Profile: ProjectionProfileToolkitV02, ExtensionRelations: ExtensionRelationsInclude})
	if !errors.Is(err, context.Canceled) || output.Len() != 0 {
		t.Fatalf("public NTriples=(%d,%v), want zero/context.Canceled", output.Len(), err)
	}
}

func TestLegacyJSONLDProtectsEveryReservedKey(t *testing.T) {
	t.Parallel()
	reserved := []string{"@context", "@graph", "@id", "@type", "okf", "bundle", "type", "title", "description", "resource", "tags", "timestamp", "references", "target", "exists", "is_part_of"}
	for _, key := range reserved {
		node := orderedJSONObject{{key: key, value: "fixed"}}
		relation := bundle.Relation{Type: key, Target: bundle.RelationRef{}, TargetExists: true}
		if err := appendJSONLDRelationContext(context.Background(), &node, relation); err != nil {
			t.Fatal(err)
		}
		value, ok, err := node.getContext(context.Background(), key)
		preserved, preservedOK, preservedErr := node.getContext(context.Background(), "okf:_relations")
		if err != nil || !ok || value != "fixed" || preservedErr != nil || !preservedOK || len(preserved.([]map[string]any)) != 1 || len(node) != 2 {
			t.Fatalf("reserved %q overwritten: %#v", key, node)
		}
	}
}

func TestHelperFinalCheckpointsAndPublicNTriplesCallsite(t *testing.T) {
	t.Parallel()
	large := strings.Repeat("é", 2<<20)
	helpers := []struct {
		name string
		act  func(context.Context) (string, error)
	}{
		{name: "triple", act: func(ctx context.Context) (string, error) {
			return ntriplesTripleContext(ctx, large, large, large, true)
		}},
		{name: "literal", act: func(ctx context.Context) (string, error) { return ntriplesLiteralContext(ctx, large) }},
		{name: "mermaid", act: func(ctx context.Context) (string, error) { return mermaidLabelContext(ctx, large) }},
	}
	for _, tt := range helpers {
		t.Run(tt.name, func(t *testing.T) {
			trace := &countingGraphContext{}
			if _, err := tt.act(trace); err != nil {
				t.Fatal(err)
			}
			count := trace.calls.Load()
			got, err := tt.act(newGraphCountdownContext(count - 1))
			if !errors.Is(err, context.Canceled) || got != "" {
				t.Fatalf("true-final=(%d,%v), calls=%d", len(got), err, count)
			}
		})
	}

	fragment := strings.Repeat("f", 2<<20)
	source := graphMemorySource{
		"index.md":   []byte("---\nokf_version: \"0.2\"\n---\n# Index\n"),
		"concept.md": []byte("---\ntype: Note\nrelations:\n  follows:\n    - target: concept#" + fragment + "\n---\nBody\n"),
	}
	b, err := bundle.Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &graphStackCancelContext{required: []string{"renderToolkitNTriplesContext", "toolkitNTriplesValueContext", "ntriplesIRIContext"}}
	var output strings.Builder
	err = RenderNTriplesWithOptionsContext(ctx, &output, b, Options{Profile: ProjectionProfileToolkitV02, ExtensionRelations: ExtensionRelationsInclude})
	if !errors.Is(err, context.Canceled) || output.Len() != 0 || ctx.hits.Load() != 1 {
		t.Fatalf("public renderer=(bytes %d,error %v,hits %d), want zero/canceled/1", output.Len(), err, ctx.hits.Load())
	}
}

func TestTopologyUTF8BoundaryAndEscapedRelationBag(t *testing.T) {
	t.Parallel()
	// Arrange.
	status := strings.Repeat("a", 248) + "é\nnext"
	document, err := bundle.ParseDocument("---\ntype: Note\nstatus: \"" + status + "\"\n---\nBody\n")
	if err != nil {
		t.Fatal(err)
	}
	want := topologyAnnotation(document, Options{})
	got, err := topologyAnnotationContext(context.Background(), document, Options{})
	if err != nil || got != want || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("topology UTF8 parity=(%q,%v), want %q", got, err, want)
	}
	if canceled, err := topologyAnnotationContext(newGraphCountdownContext(4), document, Options{}); !errors.Is(err, context.Canceled) || canceled != "" {
		t.Fatalf("topology cancellation=(%q,%v), want zero/canceled", canceled, err)
	}

	// Arrange: mixed escaped keys, duplicates, and the bag-name collision retain
	// every original type in deterministic append order.
	node := orderedJSONObject{{key: "title", value: "fixed"}}
	types := []string{"title", "okf:_relations", "title", "@id"}
	for _, typ := range types {
		if err := appendJSONLDRelationContext(context.Background(), &node, bundle.Relation{Type: typ, Target: bundle.RelationRef{}, TargetExists: true}); err != nil {
			t.Fatal(err)
		}
	}
	bagValue, ok, err := node.getContext(context.Background(), "okf:_relations")
	if err != nil || !ok {
		t.Fatalf("escaped bag=(%#v,%t,%v)", bagValue, ok, err)
	}
	bag := bagValue.([]map[string]any)
	if len(bag) != len(types) {
		t.Fatalf("bag len=%d want=%d", len(bag), len(types))
	}
	for index, typ := range types {
		if bag[index]["type"] != typ {
			t.Fatalf("bag[%d] type=%v want=%q", index, bag[index]["type"], typ)
		}
	}
	fixed, _, _ := node.getContext(context.Background(), "title")
	if fixed != "fixed" {
		t.Fatalf("fixed title overwritten: %v", fixed)
	}
	var first, second strings.Builder
	if err := writeOrderedJSONObjectContext(context.Background(), &first, node, 0); err != nil {
		t.Fatal(err)
	}
	if err := writeOrderedJSONObjectContext(context.Background(), &second, node, 0); err != nil || first.String() != second.String() {
		t.Fatalf("escaped bag nondeterministic: %v", err)
	}
}

func TestTopologyPreservesBytesAndPublicRenderersRouteCancellation(t *testing.T) {
	// Arrange: BYOT frontmatter may contain malformed scalar bytes even though
	// parsed documents reject malformed UTF-8 at their input boundary.
	frontmatter, err := bundle.NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "type"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "Note"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "status"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.Repeat("a", 255) + string([]byte{0xff}) + "\nnext"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	document := bundle.NewDocument(frontmatter, "Body")

	// Act.
	annotation, err := topologyAnnotationContext(context.Background(), document, Options{})

	// Assert.
	if err != nil || !bytes.Contains([]byte(annotation), []byte{0xff}) || bytes.Contains([]byte(annotation), []byte("\uFFFD")) {
		t.Fatalf("malformed-byte parity=(%q,%v)", annotation, err)
	}
	trace := &countingGraphContext{}
	if _, err := topologyAnnotationContext(trace, document, Options{}); err != nil {
		t.Fatal(err)
	}
	if trace.calls.Load() < 2 {
		t.Fatalf("topology checkpoint trace=%d, want entry+true-final", trace.calls.Load())
	}
	if got, err := topologyAnnotationContext(newGraphCountdownContext(trace.calls.Load()-1), document, Options{}); !errors.Is(err, context.Canceled) || got != "" {
		t.Fatalf("true-final topology=(%q,%v), want zero/canceled", got, err)
	}

	root := t.TempDir()
	status := strings.Repeat("a", 255) + "é\nnext"
	writeGraphFile(t, root, "a.md", "---\ntype: Note\nstatus: \""+status+"\"\n---\n[B](b.md)\n")
	writeGraphFile(t, root, "b.md", "---\ntype: Note\n---\nBody\n")
	b := loadGraphBundle(t, root)
	public := []struct {
		name     string
		internal string
		render   func(context.Context, io.Writer, *bundle.Bundle, Options) error
	}{
		{"text", "renderTextWithOptionsContext", RenderTextWithOptionsContext},
		{"dot", "renderDOTWithOptionsContext", RenderDOTWithOptionsContext},
		{"mermaid", "renderMermaidWithOptionsContext", RenderMermaidWithOptionsContext},
	}
	for _, tt := range public {
		t.Run(tt.name, func(t *testing.T) {
			var baseline strings.Builder
			if err := tt.render(context.Background(), &baseline, b, Options{AnnotateTopology: true}); err != nil || !strings.Contains(baseline.String(), "é next") || strings.ContainsRune(baseline.String(), '\uFFFD') {
				t.Fatalf("public UTF8 parity=(%q,%v)", baseline.String(), err)
			}
			ctx := &graphStackCancelContext{required: []string{tt.internal, "topologyAnnotationContext"}}
			var output strings.Builder
			err := tt.render(ctx, &output, b, Options{AnnotateTopology: true})
			if !errors.Is(err, context.Canceled) || output.Len() != 0 || ctx.hits.Load() != 1 {
				t.Fatalf("public topology=(bytes %d,error %v,hits %d), want zero/canceled/1", output.Len(), err, ctx.hits.Load())
			}
		})
	}
}

func TestEscapedRelationBagContractIsExhaustive(t *testing.T) {
	// Arrange.
	reserved := []string{"@context", "@graph", "@id", "@type", "okf", "bundle", "type", "title", "description", "resource", "tags", "timestamp", "references", "target", "exists", "is_part_of"}
	types := append(append([]string{}, reserved...), "okf:_relations", "title", "okf:_relations", "ordinary")
	node := orderedJSONObject{{key: "title", value: "fixed"}}
	for _, typ := range types {
		if err := appendJSONLDRelationContext(context.Background(), &node, bundle.Relation{Type: typ, TargetExists: true}); err != nil {
			t.Fatal(err)
		}
	}

	// Act.
	bagValue, ok, err := node.getContext(context.Background(), "okf:_relations")
	var encoded strings.Builder
	encodeErr := writeOrderedJSONObjectContext(context.Background(), &encoded, node, 0)

	// Assert.
	if err != nil || !ok || encodeErr != nil || !legacyJSONLDReservedKey("okf:_relations") {
		t.Fatalf("bag contract=(ok %t,get %v,encode %v,reserved %t)", ok, err, encodeErr, legacyJSONLDReservedKey("okf:_relations"))
	}
	bag := bagValue.([]map[string]any)
	if len(bag) != len(types)-1 { // ordinary remains an ordinary dynamic property.
		t.Fatalf("bag len=%d want=%d", len(bag), len(types)-1)
	}
	for index, typ := range types[:len(types)-1] {
		if bag[index]["type"] != typ {
			t.Fatalf("bag[%d].type=%v want=%q", index, bag[index]["type"], typ)
		}
	}
	const exactDigest = "ba1025ca724838f7dbb66f96f9c3f724e6da066294f94e40be4ab859c23843a9"
	gotDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(encoded.String())))
	if gotDigest != exactDigest {
		t.Fatalf("escaped exact digest=%s bytes=%s", gotDigest, encoded.String())
	}
}

type graphMemorySource map[string][]byte

func (source graphMemorySource) Paths(context.Context) ([]string, error) {
	return []string{"concept.md", "index.md"}, nil
}
func (source graphMemorySource) ReadFile(_ context.Context, path string) ([]byte, error) {
	return append([]byte(nil), source[path]...), nil
}

type countingGraphContext struct{ calls atomic.Int64 }

func (*countingGraphContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*countingGraphContext) Done() <-chan struct{}       { return nil }
func (*countingGraphContext) Value(any) any               { return nil }
func (ctx *countingGraphContext) Err() error              { ctx.calls.Add(1); return nil }

type graphStackCancelContext struct {
	required []string
	hits     atomic.Int64
}

func (*graphStackCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*graphStackCancelContext) Done() <-chan struct{}       { return nil }
func (*graphStackCancelContext) Value(any) any               { return nil }
func (ctx *graphStackCancelContext) Err() error {
	pcs := make([]uintptr, 32)
	count := runtime.Callers(2, pcs)
	seen := make(map[string]bool)
	frames := runtime.CallersFrames(pcs[:count])
	for {
		frame, more := frames.Next()
		for _, name := range ctx.required {
			if strings.HasSuffix(frame.Function, "."+name) {
				seen[name] = true
			}
		}
		if !more {
			break
		}
	}
	for _, name := range ctx.required {
		if !seen[name] {
			return nil
		}
	}
	if ctx.hits.Add(1) == 1 {
		return context.Canceled
	}
	return context.Canceled
}

func TestRenderJSONLDContextStreamsBoundedChunks(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\ntype: Note\ntitle: \""+strings.Repeat("x", 32<<10)+"\"\n---\nBody.\n")
	b := loadGraphBundle(t, root)
	writer := &maxGraphWriteRecorder{}

	// Act.
	err := RenderJSONLDContext(context.Background(), writer, b)

	// Assert.
	if err != nil {
		t.Fatalf("RenderJSONLDContext() error = %v", err)
	}
	if writer.max > graphWriteChunkSize {
		t.Fatalf("maximum JSON-LD write = %d, want <= %d", writer.max, graphWriteChunkSize)
	}
	decodeGraphJSONLD(t, writer.output.String())
}

func TestLegacyIdentityAndIRIOwners(t *testing.T) {
	// Arrange.
	largeID := strings.Repeat("segment", 1<<18)
	largeFragment := strings.Repeat("fragment /%?é", 1<<17)
	id, err := bundle.NewConceptID([]string{largeID})
	if err != nil {
		t.Fatal(err)
	}
	ref := bundle.RelationRef{ID: id, Fragment: largeFragment}

	// Act.
	escaped, escapeErr := graphPathEscapeContext(context.Background(), largeFragment)
	iri, iriErr := ntriplesRelationRefIRIContext(context.Background(), ref)
	canceledIRI, canceledErr := ntriplesRelationRefIRIContext(newGraphCountdownContext(32), ref)

	// Assert.
	if escapeErr != nil || escaped != url.PathEscape(largeFragment) {
		t.Fatalf("PathEscape parity error=%v equal=%t", escapeErr, escaped == url.PathEscape(largeFragment))
	}
	wantIRI := ntriplesIRI(ntriplesBundlePrefix + url.PathEscape(id.String()) + "#" + url.PathEscape(largeFragment))
	if iriErr != nil || iri != wantIRI {
		t.Fatalf("relation IRI parity error=%v equal=%t", iriErr, iri == wantIRI)
	}
	if !errors.Is(canceledErr, context.Canceled) || canceledIRI != "" {
		t.Fatalf("canceled relation IRI=(%q,%v), want exact zero/context.Canceled", canceledIRI, canceledErr)
	}
}

func TestToolkitProjectionIsContextOwned(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	large := strings.Repeat("projection-value-", 1<<17)
	writeGraphFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n")
	writeGraphFile(t, root, "concept.md", "---\ntype: Note\ntitle: \""+large+"\"\nresource: \""+large+"\"\ngenerated: {by: \""+large+"\", at: 2026-08-12T00:00:00Z}\nstatus: stable\nstale_after: 2026-08-12\n---\nBody\n")
	b := loadGraphBundle(t, root)
	options := Options{Profile: ProjectionProfileToolkitV02, AsOf: graphDatePointer(2026, 8, 12)}

	// Act.
	projection, err := buildToolkitProjectionContext(newGraphCountdownContext(64), b, options)
	first, firstErr := buildToolkitProjectionContext(context.Background(), b, options)
	second, secondErr := buildToolkitProjectionContext(context.Background(), b, options)

	// Assert.
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(projection, toolkitProjection{}) {
		t.Fatalf("canceled projection=(%#v,%v), want exact zero/context.Canceled", projection, err)
	}
	if firstErr != nil || secondErr != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("Background projection parity errors=%v/%v equal=%t", firstErr, secondErr, reflect.DeepEqual(first, second))
	}
	first.Nodes[0].ID = "mutated"
	mutatedNested := false
	for nodeIndex := range first.Nodes {
		for property, values := range first.Nodes[nodeIndex].Properties {
			if len(values) == 0 {
				continue
			}
			values[0].Value = "mutated"
			first.Nodes[nodeIndex].Properties[property] = values
			mutatedNested = true
			break
		}
		if mutatedNested {
			break
		}
	}
	if !mutatedNested {
		t.Fatal("projection contained no nested property value to exercise ownership")
	}
	again, againErr := buildToolkitProjectionContext(context.Background(), b, options)
	if againErr != nil || !reflect.DeepEqual(again, second) {
		t.Fatal("toolkit projection retained caller mutation")
	}
}

func TestProjectionCoalescingUsesExactContextKeys(t *testing.T) {
	// Arrange.
	largePrefix := strings.Repeat("node-", 1<<18)
	projection := toolkitProjection{Nodes: []projectionNode{
		{ID: largePrefix + "a", Types: []string{"Z"}, Properties: map[string][]projectionValue{"title": {{Value: "left"}}}},
		{ID: largePrefix + "b", Types: []string{"B"}, Properties: map[string][]projectionValue{"title": {{Value: "other"}}}},
		{ID: largePrefix + "a", Types: []string{"A"}, Properties: map[string][]projectionValue{"title": {{Value: "right"}}}},
	}}
	canceled := projection
	canceled.Nodes = cloneProjectionNodesForTest(projection.Nodes)
	wantCanceled := cloneProjectionNodesForTest(canceled.Nodes)

	// Act.
	cancelErr := sortProjectionContext(newGraphCountdownContext(32), &canceled)
	err := sortProjectionContext(context.Background(), &projection)

	// Assert.
	if !errors.Is(cancelErr, context.Canceled) {
		t.Fatalf("canceled coalesce error=%v, want context.Canceled", cancelErr)
	}
	if !reflect.DeepEqual(canceled.Nodes, wantCanceled) {
		t.Fatal("canceled coalesce mutated caller-owned nested projection state")
	}
	if err != nil || len(projection.Nodes) != 2 {
		t.Fatalf("coalesced projection nodes=%d error=%v, want 2", len(projection.Nodes), err)
	}
	var merged *projectionNode
	for index := range projection.Nodes {
		if projection.Nodes[index].ID == largePrefix+"a" {
			merged = &projection.Nodes[index]
		}
	}
	if merged == nil || !reflect.DeepEqual(merged.Types, []string{"A", "Z"}) ||
		!reflect.DeepEqual(merged.Properties["title"], []projectionValue{{Value: "left"}, {Value: "right"}}) {
		t.Fatalf("exact-key coalesce=%#v", merged)
	}
}

func cloneProjectionNodesForTest(nodes []projectionNode) []projectionNode {
	out := make([]projectionNode, len(nodes))
	for index, node := range nodes {
		out[index] = projectionNode{ID: node.ID, Types: append([]string(nil), node.Types...), Properties: make(map[string][]projectionValue, len(node.Properties))}
		for property, values := range node.Properties {
			out[index].Properties[property] = append([]projectionValue(nil), values...)
		}
	}
	return out
}

func graphDatePointer(year int, month time.Month, day int) *time.Time {
	value := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return &value
}

type graphCountdownContext struct {
	remaining atomic.Int64
}

func newGraphCountdownContext(checks int64) *graphCountdownContext {
	ctx := &graphCountdownContext{}
	ctx.remaining.Store(checks)
	return ctx
}

func (*graphCountdownContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*graphCountdownContext) Done() <-chan struct{}       { return nil }
func (*graphCountdownContext) Value(any) any               { return nil }

func (c *graphCountdownContext) Err() error {
	if c.remaining.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

type cancelingGraphWriter struct {
	cancel context.CancelFunc
	err    error
	short  bool
}

func (w cancelingGraphWriter) Write(data []byte) (int, error) {
	w.cancel()
	if w.err != nil {
		return 0, w.err
	}
	if w.short && len(data) > 0 {
		return len(data) - 1, nil
	}
	return len(data), nil
}

type maxGraphWriteRecorder struct {
	max    int
	output strings.Builder
}

func (w *maxGraphWriteRecorder) Write(data []byte) (int, error) {
	if len(data) > w.max {
		w.max = len(data)
	}
	return w.output.Write(data)
}

func largeContextGraphBundle(t *testing.T, reverse bool) *bundle.Bundle {
	t.Helper()
	root := t.TempDir()
	const concepts = 96
	order := make([]int, concepts)
	for index := range order {
		if reverse {
			order[index] = concepts - 1 - index
		} else {
			order[index] = index
		}
	}
	writeGraphFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n")
	for _, index := range order {
		next := (index + 1) % concepts
		path := fmt.Sprintf("concept-%03d.md", index)
		contents := fmt.Sprintf("---\ntype: Note\ntitle: Concept %03d\ntags: [z, a, m]\nrelations:\n  follows:\n    - target: concept-%03d\n---\nSee [next](concept-%03d.md).\n", index, next, next)
		writeGraphFile(t, root, path, contents)
	}
	return loadGraphBundle(t, root)
}
