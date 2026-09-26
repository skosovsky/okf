package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
)

func TestSearchConceptsRankingUnicodeAndBounds(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n\n- [Cafe](cafe.md)\n")
	writeTestFile(t, root, "cafe.md", "---\ntype: Note\ntitle: Café\n---\nFirst.\n")
	writeTestFile(t, root, "other.md", "---\ntype: Note\ntitle: Other\n---\nCAFE\u0301 in body.\n")
	writeTestFile(t, root, "third.md", "---\ntype: Note\ntitle: café\n---\nThird.\n")

	// Act.
	result := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), map[string]any{"bundle_path": root, "query": "cafe\u0301", "limit": float64(2)})

	// Assert.
	if result.IsError {
		t.Fatalf("search error: %s", resultText(t, result))
	}
	response := result.StructuredContent.(conceptSearchResult)
	if response.Total != 3 || !response.Truncated || len(response.Hits) != 2 {
		t.Fatalf("response = %#v", response)
	}
	if response.Hits[0].Concept.ID != "cafe" || response.Hits[0].Match != "title" || response.Hits[1].Concept.ID != "third" {
		t.Fatalf("ranked hits = %#v", response.Hits)
	}
	// The composed/decomposed title/body matches still contribute to total.
	if response.Hits[0].Concept.Path != "cafe.md" {
		t.Fatalf("path = %q", response.Hits[0].Concept.Path)
	}
}

func TestSearchConceptsFieldPriorityAndCanonicalTies(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n\n- [Needle](needle.md)\n")
	writeTestFile(t, root, "needle.md", "---\ntype: Note\ntitle: ID hit\n---\nA.\n")
	writeTestFile(t, root, "title-b.md", "---\ntype: Note\ntitle: Needle\n---\nB.\n")
	writeTestFile(t, root, "title-a.md", "---\ntype: Note\ntitle: NEEDLE\n---\nC.\n")
	writeTestFile(t, root, "description.md", "---\ntype: Note\ntitle: D\ndescription: needle description\n---\nD.\n")
	writeTestFile(t, root, "tag.md", "---\ntype: Note\ntitle: T\ntags: [needle]\n---\nT.\n")
	writeTestFile(t, root, "body.md", "---\ntype: Note\ntitle: B\n---\nneedle in body.\n")

	// Act.
	result := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), map[string]any{"bundle_path": root, "query": "needle"})

	// Assert.
	if result.IsError {
		t.Fatalf("search error: %s", resultText(t, result))
	}
	got := result.StructuredContent.(conceptSearchResult)
	wantIDs := []string{"needle", "title-a", "title-b", "description", "tag", "body"}
	wantFields := []string{"id", "title", "title", "description", "tags", "body"}
	if len(got.Hits) != len(wantIDs) || got.Total != len(wantIDs) || got.Truncated {
		t.Fatalf("search = %#v", got)
	}
	for i, hit := range got.Hits {
		if hit.Concept.ID != wantIDs[i] || hit.Match != wantFields[i] {
			t.Fatalf("hit %d = %#v, want %q/%q", i, hit, wantIDs[i], wantFields[i])
		}
	}
}

func TestSearchConceptsDoesNotMatchAcrossTagBoundary(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n")
	writeTestFile(t, root, "tags.md", "---\ntype: Note\ntags: [foo, bar]\n---\nNo match.\n")

	// Act.
	result := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), map[string]any{"bundle_path": root, "query": "foo\nbar"})

	// Assert.
	if result.IsError {
		t.Fatalf("search error: %s", resultText(t, result))
	}
	if got := result.StructuredContent.(conceptSearchResult); got.Total != 0 || len(got.Hits) != 0 {
		t.Fatalf("cross-tag false match: %#v", got)
	}
}

func TestSearchConceptsRejectsBlankQueryAndCancellation(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "# Index\n")
	blank := map[string]any{"bundle_path": root, "query": "  "}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	result := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), blank)
	tooMany := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), map[string]any{"bundle_path": root, "query": "a", "limit": float64(101)})
	_, err := searchConceptSnapshot(cancelled, &bundle.Bundle{}, "a", 20, nil, bundle.TemporalProfileDate)

	// Assert.
	if !result.IsError {
		t.Fatal("blank query accepted")
	}
	if !tooMany.IsError {
		t.Fatal("limit above 100 accepted")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled search error = %v", err)
	}
}

func TestGetNeighborsCancelledSnapshotReturnsNoPartialSuccess(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "# Index\n")
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	id, err := bundle.ParseConceptID("a")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	_, err = neighborConceptSnapshot(ctx, loaded, id, "both", []string{"navigation"}, 20, nil, bundle.TemporalProfileDate)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled neighborhood error = %v", err)
	}
}

func TestGetNeighborsSeparatesNavigationRelationsAndProvenance(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n\n- [A](a.md)\n")
	writeTestFile(t, root, "a.md", "---\ntype: Note\ntitle: A\nparts:\n  - id: section\nsources:\n  - id: doc\n    resource: https://example.com/doc\n    title: Document\n---\n# A\n\n[To B](b.md#part)\n[Broken](missing.md)\n`[fake](ghost.md)`\n")
	writeTestFile(t, root, "b.md", "---\ntype: Note\ntitle: B\nparts:\n  - id: part\nrelations:\n  uses:\n    - target: a#section\n---\n# B\n\n[To A](/a.md#section)\n")

	// Act.
	result := callHandler(t, contractToolHandler("get_neighbors", handleGetNeighbors), map[string]any{"bundle_path": root, "concept_id": "a", "direction": "both", "limit": float64(20)})
	incoming := callHandler(t, contractToolHandler("get_neighbors", handleGetNeighbors), map[string]any{"bundle_path": root, "concept_id": "a", "direction": "in", "limit": float64(20)})

	// Assert.
	if result.IsError {
		t.Fatalf("neighbors error: %s", resultText(t, result))
	}
	response := result.StructuredContent.(conceptNeighborsResult)
	if len(response.Sources) != 1 || response.Sources[0].Resource != "https://example.com/doc" {
		t.Fatalf("sources = %#v", response.Sources)
	}
	kinds := map[string]int{}
	for _, edge := range response.Edges {
		kinds[edge.Kind]++
		if strings.Contains(edge.To, "ghost") {
			t.Fatalf("code-owned pseudolink became edge: %#v", edge)
		}
	}
	if kinds["navigation"] != 2 || kinds["relation"] != 1 {
		t.Fatalf("edges = %#v", response.Edges)
	}
	if response.Total != 3 || response.Truncated {
		t.Fatalf("bounds = %#v", response)
	}
	if response.Edges[2].To != "a#section" {
		t.Fatalf("fragment relation = %#v", response.Edges[2])
	}
	if incoming.IsError {
		t.Fatalf("incoming error: %s", resultText(t, incoming))
	}
	if got := incoming.StructuredContent.(conceptNeighborsResult); len(got.Sources) != 0 || got.Total != 2 {
		t.Fatalf("incoming direction = %#v", got)
	}
}

func TestGetNeighborsPreservesEscapedRelationIdentity(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n\n- [Hash](hash%23name.md)\n")
	writeTestFile(t, root, "hash#name.md", "---\ntype: Note\ntitle: Hash\n---\nHash.\n")
	writeTestFile(t, root, "source.md", "---\ntype: Note\ntitle: Source\nrelations:\n  uses:\n    - target: 'hash\\#name'\n---\nSource.\n")

	// Act.
	result := callHandler(t, contractToolHandler("get_neighbors", handleGetNeighbors), map[string]any{"bundle_path": root, "concept_id": "hash#name", "kinds": []any{"relation"}, "direction": "in"})

	// Assert.
	if result.IsError {
		t.Fatalf("neighbors error: %s", resultText(t, result))
	}
	response := result.StructuredContent.(conceptNeighborsResult)
	if response.Total != 1 || len(response.Edges) != 1 || response.Edges[0].To != "hash\\#name" || response.Edges[0].Target.ID != "hash#name" {
		t.Fatalf("escaped edge = %#v", response)
	}
}

func TestGetNeighborsMissingAndBoundedOnGeneratedFixture(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n\n- [Hub](hub.md)\n")
	writeTestFile(t, root, "hub.md", "---\ntype: Note\ntitle: Hub\n---\nHub.\n")
	for i := 0; i < 1000; i++ {
		writeTestFile(t, root, fmt.Sprintf("node-%04d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: Node %d\n---\n[Hub](hub.md)\n", i))
	}
	start := time.Now()

	// Act.
	response := callHandler(t, contractToolHandler("get_neighbors", handleGetNeighbors), map[string]any{"bundle_path": root, "concept_id": "hub", "direction": "in", "kinds": []any{"navigation"}, "limit": float64(5)})
	elapsed := time.Since(start)
	searchStart := time.Now()
	search := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), map[string]any{"bundle_path": root, "query": "Node", "limit": float64(5)})
	searchElapsed := time.Since(searchStart)
	missing := callHandler(t, contractToolHandler("get_neighbors", handleGetNeighbors), map[string]any{"bundle_path": root, "concept_id": "missing"})

	// Assert.
	if response.IsError {
		t.Fatalf("neighbors error: %s", resultText(t, response))
	}
	parsed := response.StructuredContent.(conceptNeighborsResult)
	if parsed.Total != 1000 || len(parsed.Edges) != 5 || !parsed.Truncated {
		t.Fatalf("bounded output = %#v", parsed)
	}
	if parsed.Edges[0].From != "node-0000" || parsed.Edges[4].From != "node-0004" {
		t.Fatalf("order = %#v", parsed.Edges)
	}
	if !missing.IsError {
		t.Fatal("missing concept accepted")
	}
	if search.IsError {
		t.Fatalf("search error: %s", resultText(t, search))
	}
	searchResult := search.StructuredContent.(conceptSearchResult)
	if searchResult.Total != 1000 || len(searchResult.Hits) != 5 || !searchResult.Truncated || searchResult.Hits[0].Concept.ID != "node-0000" {
		t.Fatalf("bounded search output = %#v", searchResult)
	}
	t.Logf("generated 1000-concept neighborhood: %s", elapsed)
	t.Logf("generated 1000-concept search: %s", searchElapsed)
}

func TestGetNeighborsCanonicalIDAndRootBoundary(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "# Index\n")
	// Act.
	traversal := callHandler(t, contractToolHandler("get_neighbors", handleGetNeighbors), map[string]any{"bundle_path": root, "concept_id": "../escape"})
	// Assert.
	if !traversal.IsError {
		t.Fatal("traversal ID accepted")
	}
}

func TestConceptQueriesRejectSymlinkEscape(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n")
	writeTestFile(t, outside, "secret.md", "---\ntype: Note\ntitle: Secret\n---\nOutside.\n")
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "secret.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	// Act.
	search := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), map[string]any{"bundle_path": root, "query": "Secret"})
	neighbors := callHandler(t, contractToolHandler("get_neighbors", handleGetNeighbors), map[string]any{"bundle_path": root, "concept_id": "secret"})

	// Assert.
	if search.IsError {
		t.Fatalf("search error: %s", resultText(t, search))
	}
	if got := search.StructuredContent.(conceptSearchResult).Total; got != 0 {
		t.Fatalf("symlink content found: %d", got)
	}
	if !neighbors.IsError {
		t.Fatal("symlink target exposed as concept")
	}
}

func TestConceptQueryTemporalProfileIsExplicit(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n\n- [A](a.md)\n")
	writeTestFile(t, root, "a.md", "---\ntype: Note\ntitle: A\nstale_after: 2026-09-26T12:00:00+07:00\n---\nA.\n")
	instant := map[string]any{"bundle_path": root, "query": "A", "temporal_profile": "instant-0b87c52", "as_of": "2026-09-26T12:00:01+07:00"}
	dateWithInstant := map[string]any{"bundle_path": root, "query": "A", "as_of": "2026-09-26T12:00:01+07:00"}

	// Act.
	result := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), instant)
	rejected := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), dateWithInstant)

	// Assert.
	if result.IsError {
		t.Fatalf("instant search error: %s", resultText(t, result))
	}
	got := result.StructuredContent.(conceptSearchResult)
	if got.TemporalProfile != "instant-0b87c52" || got.AsOf == nil || *got.AsOf != "2026-09-26T12:00:01+07:00" || len(got.Hits) != 1 || got.Hits[0].Concept.Stale == nil || !*got.Hits[0].Concept.Stale {
		t.Fatalf("temporal observation = %#v", got)
	}
	if !rejected.IsError {
		t.Fatal("datetime without explicit instant profile accepted")
	}
}

var _ = mcp.CallToolRequest{}
