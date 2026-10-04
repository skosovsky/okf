package mcpserver

import (
	"bytes"
	"encoding/json"
	"github.com/skosovsky/okf/internal/okfcli"
	"github.com/skosovsky/okf/retrieval"
	"strings"
	"testing"
)

func TestSectionSearchCLIAndMCPShareSnapshotAndSchema(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\n# Retry\nDeadline permits jitter.\n")
	// Act.
	r := callHandler(t, contractToolHandler("search_sections", handleSearchSections), map[string]any{"bundle_path": root, "query": "jitter deadline", "limit": float64(5)})
	var out, stderr bytes.Buffer
	code := okfcli.Run([]string{"search", root, "--query", "jitter deadline", "--limit", "5", "--json"}, &out, &stderr)
	// Assert.
	if r.IsError || code != 0 {
		t.Fatalf("MCP %#v CLI %d %s", r, code, stderr.String())
	}
	mcpBytes, e := json.Marshal(r.StructuredContent)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(bytes.TrimSpace(mcpBytes), bytes.TrimSpace(out.Bytes())) {
		t.Fatalf("parity %s != %s", mcpBytes, out.String())
	}
	if e = validateContract("search_sections.output", r.StructuredContent); e != nil {
		t.Fatal(e)
	}
	h := r.StructuredContent.(retrieval.Result).Hits[0]
	if h.Path != "a.md" || h.LineStart != 4 || h.LineEnd != 5 {
		t.Fatalf("source %#v", h)
	}
}
func TestSectionSearchRejectsBadInputsBeforeOpeningSource(t *testing.T) {
	// Arrange.
	for _, query := range []string{"!!!", ""} {
		// Act.
		r := callHandler(t, contractToolHandler("search_sections", handleSearchSections), map[string]any{"bundle_path": "/missing", "query": query})
		// Assert.
		if !r.IsError {
			t.Fatalf("invalid query success %#v", r)
		}
	}
}

func TestSectionSearchCaseFoldExpansionConformsToOutput(t *testing.T) {
	for _, runeText := range []string{"ß", "ﬃ"} {
		t.Run(runeText, func(t *testing.T) {
			// Arrange: a valid 512-character query expands during Unicode folding.
			query := strings.Repeat(runeText, 512)
			root := t.TempDir()
			writeTestFile(t, root, "a.md", "---\ntype: Note\n---\n"+query+"\n")
			// Act.
			r := callHandler(t, contractToolHandler("search_sections", handleSearchSections), map[string]any{"bundle_path": root, "query": query})
			// Assert.
			if r.IsError {
				t.Fatalf("expanded query: %#v", r)
			}
			if err := validateContract("search_sections.output", r.StructuredContent); err != nil {
				t.Fatal(err)
			}
			result := r.StructuredContent.(retrieval.Result)
			if len(result.Hits) != 1 || len([]rune(result.Terms[0])) <= 512 {
				t.Fatalf("expansion not tested: %#v", result)
			}
		})
	}
}
