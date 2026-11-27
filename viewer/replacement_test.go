package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
)

func TestOKF03ViewerShowsReplacementAndPathEdge(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	for name, content := range map[string]string{
		"index.md": "---\nokf_version: '0.3'\n---\n",
		"old.md":   "---\ntype: Policy\nstatus: deprecated\nsuperseded_by: new.md\n---\n",
		"new.md":   "---\ntype: Policy\nstale_after: 2026-11-18\n---\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	projected, err := Build(t.Context(), b, Options{AsOf: &at})
	if err != nil {
		t.Fatal(err)
	}
	html, err := RenderWithOptions(t.Context(), projected, RenderOptions{Language: "ru"})
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if len(projected.Concepts) != 2 || projected.Concepts[0].Staleness != "stale" || projected.Concepts[1].Replacement != "new" ||
		len(projected.Edges) != 1 || projected.Edges[0].Kind != "superseded_by" ||
		!strings.Contains(string(html), "Открыть замену") {
		t.Fatalf("unexpected replacement projection: %#v", projected)
	}
}
