package mcpserver

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/skosovsky/okf/internal/okfcli"
	"github.com/skosovsky/okf/retrieval"
)

func TestOKF03SourceUsageCountDoesNotRankSearchHits(t *testing.T) {
	// Arrange. Use two otherwise identical hits and reverse the counters.
	root := t.TempDir()
	const aSource = "---\ntype: Note\nsources: {resource: 'urn:test:query-runs', usage_count: "
	const bSource = "---\ntype: Note\nsources: {resource: 'urn:test:dashboard-views', usage_count: "
	const body = "}\n---\n# Retry\nDeadline permits jitter.\n"
	args := map[string]any{"bundle_path": root, "query": "jitter"}
	concepts := contractToolHandler("search_concepts", handleSearchConcepts)
	sections := contractToolHandler("search_sections", handleSearchSections)

	// Act.
	var sectionScores []float64
	for _, counts := range [][2]string{{"1", "1000000"}, {"1000000", "1"}} {
		writeTestFile(t, root, "a.md", aSource+counts[0]+body)
		writeTestFile(t, root, "b.md", bSource+counts[1]+body)
		conceptResult := callHandler(t, concepts, args)
		sectionResult := callHandler(t, sections, args)

		// Assert. Section fingerprints may change when frontmatter changes.
		if conceptResult.IsError || sectionResult.IsError {
			t.Fatalf("counts %v: concept search %s, section search %s", counts, resultText(t, conceptResult), resultText(t, sectionResult))
		}
		conceptHits := conceptResult.StructuredContent.(conceptSearchResult).Hits
		sectionHits := sectionResult.StructuredContent.(retrieval.Result).Hits
		if len(conceptHits) != 2 || conceptHits[0].Concept.ID != "a" || conceptHits[1].Concept.ID != "b" {
			t.Fatalf("counts %v changed concept ordering: %+v", counts, conceptHits)
		}
		if len(sectionHits) != 2 || sectionHits[0].ConceptID != "a" || sectionHits[1].ConceptID != "b" {
			t.Fatalf("counts %v changed section ordering: %+v", counts, sectionHits)
		}
		scores := []float64{sectionHits[0].Score, sectionHits[1].Score}
		if sectionScores != nil && !slices.Equal(sectionScores, scores) {
			t.Fatalf("counts %v changed section scores from %v to %v", counts, sectionScores, scores)
		}
		sectionScores = scores
	}
}

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

func TestOKF03SectionSearchFollowsReplacementAndPreservesEvidence(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n# Concepts\n")
	writeTestFile(t, root, "old.md", "---\ntype: Note\nstatus: deprecated\nsuperseded_by: intermediate.md\n---\n# Procedure\nHistorical 24 hours.\n")
	writeTestFile(t, root, "intermediate.md", "---\ntype: Note\nstatus: deprecated\nsuperseded_by: current.md\n---\n# Previous change\n")
	writeTestFile(t, root, "current.md", "---\ntype: Note\nstatus: stable\n---\n# Procedure\nCurrent 48 hours.\n")
	writeTestFile(t, root, "removed.md", "---\ntype: Note\nstatus: deprecated\n---\n# Removed\nAlso 24 hours.\n")
	handler := contractToolHandler("search_sections", handleSearchSections)
	args := map[string]any{"bundle_path": root, "query": "24 hours", "current_only": true}
	result := callHandler(t, handler, args)
	if result.IsError {
		t.Fatalf("MCP result = %#v", result)
	}
	mcp := result.StructuredContent.(retrieval.Result)
	if mcp.Total != 1 || len(mcp.Hits) != 1 || mcp.Hits[0].Path != "old.md" ||
		mcp.Hits[0].ReplacementConceptID != "current" || mcp.Hits[0].LineStart != 6 ||
		!strings.Contains(mcp.Hits[0].Snippet, "24 hours") {
		t.Fatalf("current-only result = %+v", mcp)
	}
	var stdout, stderr bytes.Buffer
	if code := okfcli.Run([]string{"search", root, "--query", "24 hours", "--current-only", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("CLI exit = %d: %s", code, stderr.String())
	}
	wire, err := json.Marshal(mcp)
	if err != nil || !bytes.Equal(wire, bytes.TrimSpace(stdout.Bytes())) {
		t.Fatalf("CLI %s != MCP %s, %v", stdout.String(), wire, err)
	}
	if err := validateContract("search_sections.output", mcp); err != nil {
		t.Fatal(err)
	}
	delete(args, "current_only")
	ordinary := callHandler(t, handler, args)
	if ordinary.IsError || ordinary.StructuredContent.(retrieval.Result).Total != 2 {
		t.Fatalf("ordinary result = %#v", ordinary)
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
