package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestTask004Measurement(t *testing.T) {
	if os.Getenv("OKF_TASK004_MEASURE") != "1" {
		t.Skip("opt-in large-bundle MCP measurement; set OKF_TASK004_MEASURE=1")
	}
	tools := []struct {
		name    string
		handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		args    func(string, string) map[string]any
	}{
		{"search_broad", handleSearchConcepts, func(root, _ string) map[string]any {
			return map[string]any{"bundle_path": root, "query": "policy shared"}
		}},
		{"search_precise", handleSearchConcepts, func(root, target string) map[string]any { return map[string]any{"bundle_path": root, "query": target} }},
		{"list", handleListConcepts, func(root, _ string) map[string]any { return map[string]any{"bundle_path": root} }},
		{"read", handleReadConcept, func(root, target string) map[string]any {
			return map[string]any{"bundle_path": root, "concept_id": target}
		}},
		{"neighbors", handleGetNeighbors, func(root, target string) map[string]any {
			return map[string]any{"bundle_path": root, "concept_id": target}
		}},
		{"graph", handleSemanticGraph, func(root, _ string) map[string]any { return map[string]any{"bundle_path": root} }},
		{"validate", handleValidateBundle, func(root, _ string) map[string]any { return map[string]any{"bundle_path": root} }},
		{"preview_patch", handlePreviewConceptPatch, func(root, target string) map[string]any {
			return map[string]any{"bundle_path": root, "actor": "human:measure", "operations": []any{map[string]any{"kind": "set_lifecycle", "concept_id": target, "lifecycle": map[string]any{"status": "draft", "stale_after": nil}}}}
		}},
		{"preview_temporal", handlePreviewTemporalUpgrade, func(root, _ string) map[string]any {
			return map[string]any{"bundle_path": root, "id": "measure-004", "actor": "human:measure"}
		}},
	}
	for _, count := range []int{19, 999, 9999} {
		// Arrange.
		root := t.TempDir()
		writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n\n* [First](concept-00000.md) - Entry point.\n")
		target := fmt.Sprintf("concept-%05d", count-1)
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("concept-%05d", i)
			body := "policy shared.\n"
			if i == count-1 {
				body += "[Neighbor](concept-00000.md)\n"
			}
			writeTestFile(t, root, id+".md", "---\ntype: Note\ntitle: Topic "+id+"\n---\n"+body)
		}
		for _, tool := range tools {
			// Act.
			start := time.Now()
			result := callHandler(t, contractToolHandler(map[string]string{"search_broad": "search_concepts", "search_precise": "search_concepts", "list": "list_concepts", "read": "read_concept", "neighbors": "get_neighbors", "graph": "get_semantic_graph", "validate": "validate_bundle", "preview_patch": "preview_concept_patch", "preview_temporal": "preview_temporal_upgrade"}[tool.name], tool.handler), tool.args(root, target))
			structured, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			textBytes := len(resultText(t, result))
			summary := map[string]any{"concepts": count, "bundle_files": count + 1, "tool": tool.name, "error": result.IsError, "structured_bytes": len(structured), "text_bytes": textBytes, "ms": time.Since(start).Milliseconds()}
			// Assert.
			if result.IsError {
				t.Fatalf("%s failed on %d concepts: %s", tool.name, count, resultText(t, result))
			}
			if search, ok := result.StructuredContent.(conceptSearchResult); ok {
				summary["total"] = search.Total
				summary["returned"] = len(search.Hits)
				summary["truncated"] = search.Truncated
				if tool.name == "search_precise" {
					if search.Total != 1 || search.Truncated || len(search.Hits) != 1 || search.Hits[0].Concept.ID != target {
						t.Fatalf("target not found exactly: %#v", search)
					}
				} else if tool.name == "search_broad" {
					wantReturned := min(count, 20)
					if search.Total != count || len(search.Hits) != wantReturned || search.Truncated != (count > 20) {
						t.Fatalf("broad search completeness flags: %#v", search)
					}
				} else {
					t.Fatalf("unexpected search result for %s", tool.name)
				}
			}
			if neighbors, ok := result.StructuredContent.(conceptNeighborsResult); ok {
				summary["total"] = neighbors.Total
				summary["returned"] = len(neighbors.Edges)
				summary["truncated"] = neighbors.Truncated
				if neighbors.Concept.ID != target || neighbors.Total != 1 || len(neighbors.Edges) != 1 || neighbors.Truncated {
					t.Fatalf("neighbor route failed: %#v", neighbors)
				}
			}
			if read, ok := result.StructuredContent.(readConceptStructured); ok {
				if read.ID != target || !strings.Contains(read.Body, "policy shared") {
					t.Fatalf("wrong concept read: %q/%q", read.ID, read.Body)
				}
			}
			if list, ok := result.StructuredContent.(listConceptsStructured); ok {
				summary["returned"] = len(list.Concepts)
				if len(list.Concepts) != count {
					t.Fatalf("list returned %d concepts, want %d", len(list.Concepts), count)
				}
			}
			if validation, ok := result.StructuredContent.(validateBundleStructured); ok {
				summary["conformant"] = validation.Conformance.Conformant
				summary["diagnostics"] = len(validation.Diagnostics)
				codes := make([]string, 0, len(validation.Diagnostics))
				for _, diagnostic := range validation.Diagnostics {
					codes = append(codes, diagnostic.Code)
				}
				summary["diagnostic_codes"] = codes
				if !validation.Conformance.Conformant || validation.Conformance.Errors != 0 || len(validation.Diagnostics) != 0 {
					t.Fatalf("fixture not conformant: %#v", validation)
				}
			}
			if strings.HasPrefix(tool.name, "preview_") {
				var preview map[string]any
				if err := json.Unmarshal(structured, &preview); err != nil {
					t.Fatal(err)
				}
				summary["status"] = preview["status"]
				wantStatus := "applicable"
				if tool.name == "preview_temporal" {
					wantStatus = "noop"
				}
				if preview["status"] != wantStatus {
					t.Fatalf("%s preview status = %v, want %s", tool.name, preview["status"], wantStatus)
				}
			}
			switch tool.name {
			case "search_broad", "search_precise":
				if _, ok := result.StructuredContent.(conceptSearchResult); !ok {
					t.Fatalf("%s structured type = %T", tool.name, result.StructuredContent)
				}
			case "list":
				if _, ok := result.StructuredContent.(listConceptsStructured); !ok {
					t.Fatalf("list structured type = %T", result.StructuredContent)
				}
			case "read":
				if _, ok := result.StructuredContent.(readConceptStructured); !ok {
					t.Fatalf("read structured type = %T", result.StructuredContent)
				}
			case "neighbors":
				if _, ok := result.StructuredContent.(conceptNeighborsResult); !ok {
					t.Fatalf("neighbors structured type = %T", result.StructuredContent)
				}
			case "graph":
				if _, ok := result.StructuredContent.(map[string]any); !ok {
					t.Fatalf("graph structured type = %T", result.StructuredContent)
				}
			case "validate":
				if _, ok := result.StructuredContent.(validateBundleStructured); !ok {
					t.Fatalf("validate structured type = %T", result.StructuredContent)
				}
			}
			if tool.name == "search_broad" && count > 20 {
				search := result.StructuredContent.(conceptSearchResult)
				for _, hit := range search.Hits {
					if hit.Concept.ID == target {
						t.Fatal("target unexpectedly in first page")
					}
				}
			}
			line, _ := json.Marshal(summary)
			t.Logf("MEASURE %s", line)
		}
		if count == 9999 {
			writeTestFile(t, root, "overflow.md", "---\ntype: Note\n---\npolicy shared.\n")
			start := time.Now()
			result := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), map[string]any{"bundle_path": root, "query": "policy shared"})
			structured, _ := json.Marshal(result.StructuredContent)
			var payload map[string]any
			_ = json.Unmarshal(structured, &payload)
			if !result.IsError || payload["code"] != "resource_limit" {
				t.Fatalf("overflow result = %#v", result)
			}
			line, _ := json.Marshal(map[string]any{"concepts": 10000, "bundle_files": 10001, "tool": "search_over_limit", "error": result.IsError, "code": payload["code"], "structured_bytes": len(structured), "text_bytes": len(resultText(t, result)), "ms": time.Since(start).Milliseconds()})
			t.Logf("MEASURE %s", line)
		}
	}
}

func TestTask004NeighborOverflowMeasurement(t *testing.T) {
	if os.Getenv("OKF_TASK004_MEASURE") != "1" {
		t.Skip("opt-in MCP neighbor overflow measurement; set OKF_TASK004_MEASURE=1")
	}
	// Arrange: 125 same-kind outbound edges cannot be narrowed by direction/kind.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n\n* [Hub](hub.md) - Entry point.\n")
	var hub strings.Builder
	hub.WriteString("---\ntype: Note\n---\n# Hub\n")
	for i := 0; i < 125; i++ {
		id := fmt.Sprintf("node-%03d", i)
		hub.WriteString("\n* [")
		hub.WriteString(id)
		hub.WriteString("](")
		hub.WriteString(id)
		hub.WriteString(".md)")
		writeTestFile(t, root, id+".md", "---\ntype: Note\n---\nNode body.\n")
	}
	writeTestFile(t, root, "hub.md", hub.String()+"\n")
	for _, limit := range []int{20, 100} {
		// Act.
		start := time.Now()
		result := callHandler(t, contractToolHandler("get_neighbors", handleGetNeighbors), map[string]any{"bundle_path": root, "concept_id": "hub", "direction": "out", "kinds": []any{"navigation"}, "limit": float64(limit)})
		structured, _ := json.Marshal(result.StructuredContent)
		// Assert.
		if result.IsError {
			t.Fatalf("neighbors limit %d: %s", limit, resultText(t, result))
		}
		neighbors := result.StructuredContent.(conceptNeighborsResult)
		if neighbors.Total != 125 || len(neighbors.Edges) != limit || !neighbors.Truncated {
			t.Fatalf("neighbors limit %d: %#v", limit, neighbors)
		}
		line, _ := json.Marshal(map[string]any{"case": "same-kind-125", "tool": "get_neighbors", "limit": limit, "total": neighbors.Total, "returned": len(neighbors.Edges), "truncated": neighbors.Truncated, "structured_bytes": len(structured), "text_bytes": len(resultText(t, result)), "ms": time.Since(start).Milliseconds()})
		t.Logf("MEASURE %s", line)
	}
	// Act: v1's whole graph is the documented traversal fallback.
	start := time.Now()
	graph := callHandler(t, contractToolHandler("get_semantic_graph", handleSemanticGraph), map[string]any{"bundle_path": root})
	structured, _ := json.Marshal(graph.StructuredContent)
	// Assert: the edge beyond the 100-edge neighbor cap remains reachable.
	if graph.IsError {
		t.Fatalf("graph fallback failed: %s", resultText(t, graph))
	}
	document, ok := graph.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("graph structured content type = %T", graph.StructuredContent)
	}
	nodes, ok := document["@graph"].([]any)
	if !ok {
		t.Fatalf("graph nodes type = %T", document["@graph"])
	}
	var hubNode map[string]any
	for _, rawNode := range nodes {
		node, ok := rawNode.(map[string]any)
		if ok && node["@id"] == "local:bundle:hub" {
			hubNode = node
			break
		}
	}
	if hubNode == nil {
		t.Fatal("hub node absent from graph")
	}
	references, ok := hubNode["references"].([]any)
	if !ok || len(references) != 125 {
		t.Fatalf("hub references = %#v", hubNode["references"])
	}
	foundLastEdge := false
	for _, rawRef := range references {
		ref, ok := rawRef.(map[string]any)
		if ok && ref["@id"] == "local:bundle:node-124" {
			foundLastEdge = true
			break
		}
	}
	if !foundLastEdge {
		t.Fatal("hub→node-124 edge absent from graph")
	}
	line, _ := json.Marshal(map[string]any{"case": "same-kind-125", "tool": "get_semantic_graph", "complete": true, "structured_bytes": len(structured), "text_bytes": len(resultText(t, graph)), "ms": time.Since(start).Milliseconds()})
	t.Logf("MEASURE %s", line)
}

// Frozen workflow-cases.json read-01 asks for the active retries policy and
// its source. The refinement token comes from that question, not a known ID.
func TestTask004Read01QuestionDrivenRefinement(t *testing.T) {
	runTask004Read01QuestionDrivenRefinement(t, 30, 20)
}

func TestTask004Read01NearLimitQuestionDrivenRefinement(t *testing.T) {
	if os.Getenv("OKF_TASK004_MEASURE") != "1" {
		t.Skip("opt-in near-limit read-01 workflow; set OKF_TASK004_MEASURE=1")
	}
	runTask004Read01QuestionDrivenRefinement(t, 9998, 100)
}

func runTask004Read01QuestionDrivenRefinement(t *testing.T, genericCount, broadLimit int) {
	t.Helper()
	// Arrange.
	request := "Найди действующую политику retries и покажи источник."
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n\n* [Policies](policy-00000.md) - Entry point.\n")
	for i := 0; i < genericCount; i++ {
		id := fmt.Sprintf("policy-%05d", i)
		writeTestFile(t, root, id+".md", "---\ntype: Note\ntitle: Generic policy\n---\nGeneral policy.\n")
	}
	writeTestFile(t, root, "z-retries.md", "---\ntype: Note\ntitle: Active retries policy\nstatus: stable\nstale_after: 2099-01-01\nsources:\n  - id: retry-rfc\n    resource: https://example.invalid/retries-rfc\n    title: Synthetic retries specification\n---\n# Retries\n\nThe current retries policy permits three attempts.[^retry-rfc]\n\n[^retry-rfc]: Synthetic retries specification.\n")
	validated := callHandler(t, contractToolHandler("validate_bundle", handleValidateBundle), map[string]any{"bundle_path": root})
	if validated.IsError || !validated.StructuredContent.(validateBundleStructured).Conformance.Conformant {
		t.Fatalf("read-01 fixture is not conformant: %s", resultText(t, validated))
	}

	// Act: broad intent query is truncated; the request itself supplies retries.
	start := time.Now()
	broad := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), map[string]any{"bundle_path": root, "query": "policy", "limit": float64(broadLimit), "as_of": "2026-09-27"})
	if broad.IsError {
		t.Fatalf("broad search: %s", resultText(t, broad))
	}
	broadResult := broad.StructuredContent.(conceptSearchResult)
	refinedQuery := "retries"
	if !strings.Contains(request, refinedQuery) {
		t.Fatal("refinement was not available from the question")
	}
	refined := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), map[string]any{"bundle_path": root, "query": refinedQuery, "as_of": "2026-09-27"})
	if refined.IsError {
		t.Fatalf("refined search: %s", resultText(t, refined))
	}
	refinedResult := refined.StructuredContent.(conceptSearchResult)
	if len(refinedResult.Hits) != 1 {
		t.Fatalf("refined search = %#v", refinedResult)
	}
	selectedID := refinedResult.Hits[0].Concept.ID
	read := callHandler(t, contractToolHandler("read_concept", handleReadConcept), map[string]any{"bundle_path": root, "concept_id": selectedID})
	if read.IsError {
		t.Fatalf("read: %s", resultText(t, read))
	}
	concept := read.StructuredContent.(readConceptStructured)
	neighbors := callHandler(t, contractToolHandler("get_neighbors", handleGetNeighbors), map[string]any{"bundle_path": root, "concept_id": selectedID, "as_of": "2026-09-27"})
	if neighbors.IsError {
		t.Fatalf("neighbors: %s", resultText(t, neighbors))
	}
	related := neighbors.StructuredContent.(conceptNeighborsResult)

	// Assert: target and source were discovered without querying a known ID.
	if broadResult.Total != genericCount+1 || len(broadResult.Hits) != broadLimit || !broadResult.Truncated {
		t.Fatalf("broad search flags = %#v", broadResult)
	}
	for _, hit := range broadResult.Hits {
		if hit.Concept.ID == selectedID {
			t.Fatal("selected policy unexpectedly in first broad result")
		}
	}
	if refinedResult.Total != 1 || refinedResult.Truncated || selectedID != "z-retries" ||
		refinedResult.Hits[0].Concept.Title != "Active retries policy" {
		t.Fatalf("refined search = %#v", refinedResult)
	}
	if concept.ID != selectedID || !strings.Contains(concept.Body, "three attempts") ||
		len(concept.Projection.Sources) != 1 || concept.Projection.Sources[0].Resource != "https://example.invalid/retries-rfc" {
		t.Fatalf("selected read/source = %#v", concept)
	}
	if related.Concept.ID != selectedID || related.Concept.Status == nil || *related.Concept.Status != "stable" ||
		related.Concept.Stale == nil || *related.Concept.Stale || related.Total != 0 || related.Truncated ||
		len(related.Sources) != 1 || related.Sources[0].Resource != "https://example.invalid/retries-rfc" {
		t.Fatalf("selected neighbors/source = %#v", related)
	}
	for _, step := range []struct {
		name   string
		result *mcp.CallToolResult
	}{{"search_broad", broad}, {"search_refined", refined}, {"read", read}, {"neighbors", neighbors}} {
		structured, err := json.Marshal(step.result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		line, _ := json.Marshal(map[string]any{"case": "read-01", "concepts": genericCount + 1, "bundle_files": genericCount + 2, "tool": step.name, "structured_bytes": len(structured), "text_bytes": len(resultText(t, step.result))})
		t.Logf("MEASURE %s", line)
	}
	chain, _ := json.Marshal(map[string]any{"case": "read-01", "concepts": genericCount + 1, "bundle_files": genericCount + 2, "broad_limit": broadLimit, "broad_total": broadResult.Total, "broad_returned": len(broadResult.Hits), "broad_truncated": broadResult.Truncated, "tool": "chain", "calls": 4, "ms": time.Since(start).Milliseconds()})
	t.Logf("MEASURE %s", chain)
}
