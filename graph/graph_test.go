package graph

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestRenderGraphSemanticOutputs(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"schema:\n"+
		"  fields:\n"+
		"    - id: field1\n"+
		"      relations:\n"+
		"        writes_to:\n"+
		"          - target: b#col-2\n"+
		"          - target: b#col-2\n"+
		"relations:\n"+
		"  depends_on:\n"+
		"    - target: b#section-1\n"+
		"  impacts:\n"+
		"    - target: missing#col\n"+
		"---\nSee [B](b.md).\n")
	writeGraphFile(t, root, "b.md", "---\ntype: Note\n---\nBody.\n")
	b := loadGraphBundle(t, root)

	// Act.
	var textOut, dotOut, mermaidOut, jsonldOut, ntriplesOut strings.Builder
	if err := RenderText(&textOut, b); err != nil {
		t.Fatalf("RenderText() error = %v", err)
	}
	if err := RenderDOT(&dotOut, b); err != nil {
		t.Fatalf("RenderDOT() error = %v", err)
	}
	if err := RenderMermaid(&mermaidOut, b); err != nil {
		t.Fatalf("RenderMermaid() error = %v", err)
	}
	if err := RenderJSONLD(&jsonldOut, b); err != nil {
		t.Fatalf("RenderJSONLD() error = %v", err)
	}
	if err := RenderNTriples(&ntriplesOut, b); err != nil {
		t.Fatalf("RenderNTriples() error = %v", err)
	}

	// Assert.
	if !strings.Contains(textOut.String(), "a#field1\n  => writes_to b#col-2\n  => writes_to b#col-2\n") {
		t.Fatalf("RenderText() =\n%s\nwant duplicate subresource relations", textOut.String())
	}
	if strings.Count(dotOut.String(), `"a#field1" -> "b#col-2" [label="writes_to"];`) != 2 {
		t.Fatalf("RenderDOT() =\n%s\nwant duplicate writes_to edges", dotOut.String())
	}
	if !strings.Contains(dotOut.String(), `"a" -> "missing#col" [label="impacts", style=dashed, color=red];`) {
		t.Fatalf("RenderDOT() =\n%s\nwant missing semantic target edge", dotOut.String())
	}
	if strings.Count(mermaidOut.String(), `n2["a#field1"] -->|"writes_to"| n3["b#col-2"]`) != 2 {
		t.Fatalf("RenderMermaid() =\n%s\nwant duplicate writes_to edges", mermaidOut.String())
	}
	if !strings.Contains(mermaidOut.String(), `n0["a"] -.->|"impacts 404"| n5["missing#col"]`) {
		t.Fatalf("RenderMermaid() =\n%s\nwant missing semantic target edge", mermaidOut.String())
	}
	document := decodeGraphJSONLD(t, jsonldOut.String())
	if _, ok := document.Context["writes_to"]; !ok {
		t.Fatalf("JSON-LD context = %#v, want writes_to", document.Context)
	}
	if _, ok := document.Context["is_part_of"]; !ok {
		t.Fatalf("JSON-LD context = %#v, want is_part_of", document.Context)
	}
	field := jsonldNode(t, document, "bundle:a#field1")
	if field["@type"] != "okf:SubResource" {
		t.Fatalf("field node = %#v, want SubResource", field)
	}
	if countRawJSONLDRelation(t, field, "writes_to", "bundle:b#col-2", true) != 2 {
		t.Fatalf("field writes_to = %#v, want duplicate relations", field["writes_to"])
	}
	a := jsonldNode(t, document, "bundle:a")
	if countRawJSONLDRelation(t, a, "impacts", "bundle:missing#col", false) != 1 {
		t.Fatalf("a impacts = %#v, want missing semantic target", a["impacts"])
	}
	ntriples := ntriplesOut.String()
	if strings.Count(ntriples, `<local:bundle:a#field1> <https://okf.io/ontology/v0.1#writes_to> <local:bundle:b#col-2> .`) != 2 {
		t.Fatalf("RenderNTriples() =\n%s\nwant duplicate writes_to triples", ntriples)
	}
	if !strings.Contains(ntriples, `<local:bundle:a#field1> <https://okf.io/ontology/v0.1#is_part_of> <local:bundle:a> .`) {
		t.Fatalf("RenderNTriples() =\n%s\nwant subresource is_part_of triple", ntriples)
	}
}

func TestRenderNTriplesRelationIRIEncoding(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "api/checkout.md", "---\n"+
		"type: API Endpoint\n"+
		"schema:\n"+
		"  fields:\n"+
		"    - id: payload user\n"+
		"      relations:\n"+
		"        writes_to:\n"+
		"          - target: tables/orders#col customer\n"+
		"---\nBody.\n")
	writeGraphFile(t, root, "tables/orders.md", "---\ntype: BigQuery Table\n---\nBody.\n")
	b := loadGraphBundle(t, root)

	// Act.
	var out strings.Builder
	if err := RenderNTriples(&out, b); err != nil {
		t.Fatalf("RenderNTriples() error = %v", err)
	}

	// Assert.
	got := out.String()
	if !strings.Contains(got, `<local:bundle:api%2Fcheckout#payload%20user> <https://okf.io/ontology/v0.1#writes_to> <local:bundle:tables%2Forders#col%20customer> .`) {
		t.Fatalf("RenderNTriples() =\n%s\nwant encoded relation source and target IRIs", got)
	}
	if strings.Contains(got, "api/checkout#payload user") || strings.Contains(got, "tables/orders#col customer") {
		t.Fatalf("RenderNTriples() contains raw slash/space relation IRI:\n%s", got)
	}
}

func TestRenderEmptyBundleOutputs(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := loadGraphBundle(t, t.TempDir())

	tests := []struct {
		name string
		run  func(*strings.Builder) error
		want string
	}{
		{name: "text", run: func(out *strings.Builder) error { return RenderText(out, b) }, want: ""},
		{name: "dot", run: func(out *strings.Builder) error { return RenderDOT(out, b) }, want: "digraph okf {\n  rankdir=LR; node [shape=box, fontsize=10];\n}\n"},
		{name: "mermaid", run: func(out *strings.Builder) error { return RenderMermaid(out, b) }, want: "graph LR\n"},
		{name: "ntriples", run: func(out *strings.Builder) error { return RenderNTriples(out, b) }, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			var out strings.Builder

			// Act.
			err := tt.run(&out)

			// Assert.
			if err != nil {
				t.Fatalf("%s renderer error = %v", tt.name, err)
			}
			if out.String() != tt.want {
				t.Fatalf("%s renderer output = %q, want %q", tt.name, out.String(), tt.want)
			}
		})
	}

	var jsonldOut strings.Builder
	if err := RenderJSONLD(&jsonldOut, b); err != nil {
		t.Fatalf("RenderJSONLD(empty bundle) error = %v", err)
	}
	document := decodeGraphJSONLD(t, jsonldOut.String())
	if len(document.Graph) != 0 {
		t.Fatalf("RenderJSONLD(empty bundle) graph = %#v, want empty graph", document.Graph)
	}
}

func TestNTriplesLiteralEscapesControlCharacters(t *testing.T) {
	t.Parallel()

	// Act.
	got := ntriplesLiteral("bell:\a vertical:\v")

	// Assert.
	if !strings.Contains(got, `bell:\u0007 vertical:\u000B`) {
		t.Fatalf("ntriplesLiteral() = %q, want control escapes", got)
	}
	if strings.Contains(got, `\a`) || strings.Contains(got, `\v`) {
		t.Fatalf("ntriplesLiteral() = %q, contains invalid Go-style control escape", got)
	}
}

func TestRenderersPropagateWriterErrorsAndShortWrites(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\ntype: Note\n---\nSee [B](b.md).\n")
	writeGraphFile(t, root, "b.md", "---\ntype: Note\n---\nBody.\n")
	b := loadGraphBundle(t, root)
	writeErr := errors.New("write failed")
	renderers := []struct {
		name string
		run  func(io.Writer) error
	}{
		{name: "text", run: func(w io.Writer) error { return RenderText(w, b) }},
		{name: "dot", run: func(w io.Writer) error { return RenderDOT(w, b) }},
		{name: "mermaid", run: func(w io.Writer) error { return RenderMermaid(w, b) }},
		{name: "jsonld", run: func(w io.Writer) error { return RenderJSONLD(w, b) }},
		{name: "ntriples", run: func(w io.Writer) error { return RenderNTriples(w, b) }},
	}

	for _, renderer := range renderers {
		t.Run(renderer.name+"/write-error", func(t *testing.T) {
			// Act.
			err := renderer.run(&failingWriter{err: writeErr})

			// Assert.
			if !errors.Is(err, writeErr) {
				t.Fatalf("%s renderer error = %v, want %v", renderer.name, err, writeErr)
			}
		})

		t.Run(renderer.name+"/short-write", func(t *testing.T) {
			// Act.
			err := renderer.run(shortWriter{})

			// Assert.
			if !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("%s renderer error = %v, want io.ErrShortWrite", renderer.name, err)
			}
		})
	}
}

func TestRenderJSONLDWriteErrors(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := sampleGraphBundle(t)
	writeErr := errors.New("write failed")
	tests := []struct {
		name      string
		failWrite int
	}{
		{name: "json bytes", failWrite: 1},
		{name: "trailing newline", failWrite: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			writer := &failingWriter{failWrite: tt.failWrite, err: writeErr}

			// Act.
			err := RenderJSONLD(writer, b)

			// Assert.
			if !errors.Is(err, writeErr) {
				t.Fatalf("RenderJSONLD() error = %v, want %v", err, writeErr)
			}
		})
	}
}

func TestRenderNTriplesWriteErrors(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := sampleGraphBundle(t)
	writeErr := errors.New("write failed")
	tests := []struct {
		name      string
		failWrite int
	}{
		{name: "first triple", failWrite: 1},
		{name: "later metadata triple", failWrite: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			writer := &failingWriter{failWrite: tt.failWrite, err: writeErr}

			// Act.
			err := RenderNTriples(writer, b)

			// Assert.
			if !errors.Is(err, writeErr) {
				t.Fatalf("RenderNTriples() error = %v, want %v", err, writeErr)
			}
		})
	}
}

type graphJSONLDDocument struct {
	Context map[string]any   `json:"@context"`
	Graph   []map[string]any `json:"@graph"`
}

func decodeGraphJSONLD(t *testing.T, text string) graphJSONLDDocument {
	t.Helper()
	var document graphJSONLDDocument
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		t.Fatalf("json.Unmarshal(JSON-LD) error = %v; text:\n%s", err, text)
	}
	return document
}

func jsonldNode(t *testing.T, document graphJSONLDDocument, id string) map[string]any {
	t.Helper()
	for _, node := range document.Graph {
		if node["@id"] == id {
			return node
		}
	}
	t.Fatalf("JSON-LD node %q not found in %#v", id, document.Graph)
	return nil
}

func countRawJSONLDRelation(t *testing.T, node map[string]any, relationType, target string, exists bool) int {
	t.Helper()
	entries, _ := node[relationType].([]any)
	count := 0
	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("relation entry = %#v, want object", entry)
		}
		if object["@id"] == target && object["exists"] == exists {
			count++
		}
	}
	return count
}

type failingWriter struct {
	failWrite int
	writes    int
	err       error
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.failWrite == 0 || w.writes == w.failWrite {
		return 0, w.err
	}
	return len(p), nil
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

func loadGraphBundle(t *testing.T, root string) *bundle.Bundle {
	t.Helper()
	b, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	return b
}

func sampleGraphBundle(t *testing.T) *bundle.Bundle {
	t.Helper()
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"title: A\n"+
		"description: Alpha\n"+
		"tags: [a]\n"+
		"timestamp: 2026-06-21T00:00:00Z\n"+
		"---\nSee [B](b.md).\n")
	writeGraphFile(t, root, "b.md", "---\ntype: Note\n---\nBody.\n")
	return loadGraphBundle(t, root)
}

func writeGraphFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
