package viewer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestSourcesPresenceOnlyLegacyFallback(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want []Source
	}{
		{"absent", "", []Source{{Title: "Legacy source", Resource: "https://example.test/legacy"}, {Title: "Offline interview transcript"}}},
		{"replacement", "sources:\n  - id: modern\n    title: Modern source\n    resource: https://example.test/modern\n", []Source{{ID: "modern", Title: "Modern source", Resource: "https://example.test/modern"}}},
		{"empty", "sources: []\n", []Source{}},
		{"null", "sources: null\n", []Source{}},
		{"malformed scalar", "sources: wrong-shape\n", []Source{}},
		{"malformed entry", "sources:\n  - resource: 42\n", []Source{}},
		{"duplicate replacement", "sources: []\nsources: wrong-shape\n", []Source{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: both modern and legacy representations are present in one body.
			b := fixture(t, map[string]string{
				"index.md": "---\nokf_version: \"0.2\"\n---\n# Index\n",
				"note.md":  "---\ntype: Note\n" + tc.yaml + "---\n# Note\n\n# Citations\n\n- [Legacy source](https://example.test/legacy)\n- Offline interview transcript\n",
			})
			// Act.
			p, err := Build(context.Background(), b, Options{})
			// Assert: mere presence suppresses fallback, even if no valid entry exists.
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(p.Concepts[0].Sources, tc.want) {
				t.Fatalf("sources = %#v, want %#v", p.Concepts[0].Sources, tc.want)
			}
		})
	}
}

func TestDeclaredV01LegacySourcesRegression(t *testing.T) {
	// Arrange: the original failing compatibility bundle.
	b, err := bundle.LoadBundle(filepath.Join("..", "fixtures", "v02", "compat", "declared-v01"))
	if err != nil {
		t.Fatal(err)
	}
	// Act.
	p, err := Build(context.Background(), b, Options{})
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range p.Concepts {
		if len(c.Sources) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("declared-v01 legacy Sources panel remains empty")
	}
}

func TestViewerRouteRegression(t *testing.T) {
	// Arrange: Node executes the shipped browser script with a small DOM harness.
	// Real-browser acceptance is recorded separately; this is a repeatable regression.
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable: run node testdata/regression-016/routes.cjs")
	}
	// Act.
	output, err := exec.Command(node, "testdata/regression-016/routes.cjs").CombinedOutput()
	// Assert.
	if err != nil {
		t.Fatalf("route regression: %v\n%s", err, output)
	}
}

func TestRouteFixturePreservesFootnotesAndUnusualIDs(t *testing.T) {
	// Arrange: Windows cannot represent colon filenames. Keep the repository
	// checkout portable and materialize the original names on supported hosts.
	if runtime.GOOS == "windows" {
		t.Skip("colon concept filenames are not representable on Windows")
	}
	root := "testdata/regression-016"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".md.fixture") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, ".md.fixture") {
			name = strings.Replace(strings.TrimSuffix(name, ".fixture"), "-example", ":example", 1)
		}
		files[name] = string(data)
	}
	b := fixture(t, files)
	// Act.
	p, err := Build(context.Background(), b, Options{})
	// Assert: Goldmark emits anchors used by the route regression, and all IDs survive.
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Concept)
	for _, c := range p.Concepts {
		byID[c.ID] = c
	}
	for _, id := range []string{"fn:example", "fnref:example", "fnref1:example", "тест", "ordinary"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("missing route %q", id)
		}
	}
	body := byID["ordinary"].HTML
	for _, fragment := range []string{`href="#fn:1"`, `href="#fnref:1"`, `id="fn:1"`, `id="fnref:1"`, `href="#fnref1:1"`, `id="fnref1:1"`} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("missing real Goldmark footnote markup %q in %s", fragment, body)
		}
	}
	if len(byID["ordinary"].Sources) != 2 || byID["ordinary"].Sources[1].Title != "Offline interview transcript" || byID["ordinary"].Sources[1].Resource != "" {
		t.Fatalf("legacy sources = %#v", byID["ordinary"].Sources)
	}
	for _, target := range []string{"fn:example", "fnref:example", "fnref1:example", "тест"} {
		found := false
		for _, edge := range p.Edges {
			if edge.FromConcept == "ordinary" && edge.ToConcept == target && edge.Exists {
				found = true
			}
		}
		if !found {
			t.Fatalf("no resolved Markdown edge to %q", target)
		}
	}
}
