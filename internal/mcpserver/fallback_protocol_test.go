package mcpserver

import (
	"context"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/skosovsky/okf/bundle"
)

func TestMCPLegacyFallbackObservationVersionSourceMatrix(t *testing.T) {
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	versions := []struct {
		name          string
		declaration   string
		declared      *string
		effective     string
		resolution    string
		compatibility string
	}{
		{name: "default", effective: "0.2", resolution: "default", compatibility: "native"},
		{
			name: "declared-v01", declaration: "0.1", declared: corpusString("0.1"),
			effective: "0.1", resolution: "declared", compatibility: "legacy",
		},
		{
			name: "declared-v02", declaration: "0.2", declared: corpusString("0.2"),
			effective: "0.2", resolution: "declared", compatibility: "native",
		},
		{
			name: "future", declaration: "9.9", declared: corpusString("9.9"),
			effective: "0.2", resolution: "future-best-effort", compatibility: "best-effort",
		},
	}
	documents := map[string]string{
		"active.md": "---\ntype: Note\ntimestamp: 2026-07-29T00:00:00Z\n---\n" +
			"Claim [1].\n\n# Citations\n\n[1] https://example.test/active\n",
		"malformed-active.md": "---\ntype: Note\ntimestamp: !!int 20260729\n---\n" +
			"# Citations\n\nnot a numbered citation\n",
		"suppressed.md": "---\ntype: Note\ngenerated: malformed\nsources: malformed\n" +
			"timestamp: 2026-07-29T00:00:00Z\n---\n" +
			"Claim [1].\n\n# Citations\n\n[1] https://example.test/suppressed\n",
		"marker-only.md": "---\ntype: Note\n---\nClaim [1] without a Citations heading.\n",
	}

	for _, version := range versions {
		t.Run(version.name, func(t *testing.T) {
			root := t.TempDir()
			if version.declaration != "" {
				writeTestFile(t, root, "index.md", "---\nokf_version: \""+version.declaration+"\"\n---\n# Fallback matrix\n")
			}
			for path, content := range documents {
				writeTestFile(t, root, path, content)
			}
			before := protocolTreeSnapshot(t, root)

			listResult := callMCPTool(t, srv, "list_concepts", map[string]any{"bundle_path": root})
			if listResult.IsError {
				t.Fatalf("list_concepts returned error: %s", resultText(t, listResult))
			}
			var listed listConceptsStructured
			decodeCorpusStructured(t, listResult, &listed)
			assertCorpusVersion(t, mcpCorpusProtocolCase{
				Key: version.name, ExpectedDeclared: version.declared,
				ExpectedEffective: version.effective, ExpectedResolution: version.resolution,
				ExpectedCompatibility: version.compatibility,
			}, listed.Version)
			summaries := make(map[string]conceptSummaryV02, len(listed.Concepts))
			for _, summary := range listed.Concepts {
				summaries[summary.ID] = summary
				readResult := callMCPTool(t, srv, "read_concept", map[string]any{
					"bundle_path": root,
					"concept_id":  summary.ID,
				})
				if readResult.IsError {
					t.Fatalf("read_concept(%s) returned error: %s", summary.ID, resultText(t, readResult))
				}
				var read readConceptStructured
				decodeCorpusStructured(t, readResult, &read)
				document, err := bundle.ParseDocument(documents[filepath.ToSlash(summary.Path)])
				if err != nil {
					t.Fatalf("ParseDocument(%s) error = %v", summary.Path, err)
				}
				observation, err := document.LegacyFallbackObservationContext(t.Context())
				if err != nil {
					t.Fatalf("LegacyFallbackObservationContext(%s) error = %v", summary.Path, err)
				}
				if summary.LegacyDerived.Generated != observation.TimestampActive ||
					summary.LegacyDerived.Citations != observation.CitationsActive ||
					read.LegacyFallback.GeneratedFromTimestamp != observation.TimestampActive ||
					read.LegacyFallback.SourcesFromCitations != observation.CitationsActive {
					t.Fatalf("%s fallback projection = list %#v read %#v, observation %#v",
						summary.ID, summary.LegacyDerived, read.LegacyFallback, observation)
				}
				if (summary.ID == "suppressed" || summary.ID == "marker-only") &&
					read.Projection.Generated != nil {
					t.Fatalf("%s projected absent/fully malformed generated family: %#v",
						summary.ID, read.Projection.Generated)
				}
			}
			if len(summaries) != len(documents) {
				t.Fatalf("listed concepts = %d, want %d", len(summaries), len(documents))
			}
			assertFallbackFlags(t, summaries["active"], true, true)
			assertFallbackFlags(t, summaries["malformed-active"], true, true)
			assertFallbackFlags(t, summaries["suppressed"], false, false)
			assertFallbackFlags(t, summaries["marker-only"], false, false)

			validateResult := callMCPTool(t, srv, "validate_bundle", map[string]any{
				"bundle_path": root,
				"strict":      true,
			})
			if validateResult.IsError {
				t.Fatalf("validate_bundle returned tool error: %s", resultText(t, validateResult))
			}

			graphResult := callMCPTool(t, srv, "get_semantic_graph", map[string]any{"bundle_path": root})
			if graphResult.IsError {
				t.Fatalf("get_semantic_graph returned error: %s", resultText(t, graphResult))
			}
			var graphed struct {
				Graph []map[string]any `json:"@graph"`
			}
			decodeCorpusStructured(t, graphResult, &graphed)
			activeID := "local:bundle:" + url.PathEscape("active")
			if !hasLegacyFallbackGraphLink(graphed.Graph, "generationOf", activeID) ||
				!hasLegacyFallbackGraphLink(graphed.Graph, "sourceOf", activeID) {
				t.Fatalf("graph did not project active fallbacks: %#v", graphed.Graph)
			}
			for _, id := range []string{"malformed-active", "suppressed", "marker-only"} {
				conceptID := "local:bundle:" + url.PathEscape(id)
				if hasLegacyFallbackGraphLink(graphed.Graph, "generationOf", conceptID) ||
					hasLegacyFallbackGraphLink(graphed.Graph, "sourceOf", conceptID) {
					t.Fatalf("graph invented a valid legacy fallback node for %q", id)
				}
			}

			if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("fallback protocol matrix changed bundle tree:\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}

	t.Run("explicit-v01-and-v02", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "marker.md", documents["marker-only.md"])
		before := protocolTreeSnapshot(t, root)
		for _, selector := range []string{"0.1", "0.2"} {
			validated := callMCPTool(t, srv, "validate_bundle", map[string]any{
				"bundle_path":    root,
				"target_version": selector,
				"strict":         true,
			})
			if validated.IsError {
				t.Fatalf("validate_bundle(%s) returned error: %s", selector, resultText(t, validated))
			}
			var projection validateBundleStructured
			decodeCorpusStructured(t, validated, &projection)
			if stringPointerValue(projection.Version.Effective) != selector ||
				stringPointerValue(projection.Version.Resolution) != "explicit" {
				t.Fatalf("explicit %s version projection = %#v", selector, projection.Version)
			}
			if selector == "0.1" {
				migrated := callMCPTool(t, srv, "preview_v02_migration", map[string]any{
					"bundle_path":               root,
					"from":                      selector,
					"generated_by":              "process:mcp-fallback-matrix",
					"timestamp_policy":          "preserve",
					"timestamp_conflict_policy": "reject",
					"citation_mappings":         []any{},
				})
				if migrated.IsError {
					t.Fatalf("preview_v02_migration(%s) returned error: %s", selector, resultText(t, migrated))
				}
			}
		}
		if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("explicit version matrix changed bundle tree:\nbefore=%#v\nafter=%#v", before, after)
		}
	})
}

func TestMCPGeneratedReplacementPresenceControlsEffectiveTimestampAndMigrationPreview(t *testing.T) {
	// Arrange.
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	const generatedAt = "2026-07-29T00:00:00Z"
	const legacyTimestamp = "2026-06-01T00:00:00Z"
	tests := []struct {
		name          string
		generatedYAML string
		wantEffective *string
		wantBy        *string
		wantAt        *string
		wantBlocker   string
	}{
		{
			name:          "missing generated.by with valid generated.at",
			generatedYAML: "generated:\n  at: " + generatedAt + "\n",
			wantEffective: corpusString(generatedAt),
			wantAt:        corpusString(generatedAt),
			wantBlocker:   "missing_generated_by",
		},
		{
			name:          "malformed generated.by with valid generated.at",
			generatedYAML: "generated:\n  by: [malformed]\n  at: " + generatedAt + "\n",
			wantEffective: corpusString(generatedAt),
			wantAt:        corpusString(generatedAt),
			wantBlocker:   "missing_generated_by",
		},
		{
			name:          "malformed generated.at suppresses timestamp",
			generatedYAML: "generated:\n  by: process:existing\n  at: not-a-time\n",
			wantBy:        corpusString("process:existing"),
			wantBlocker:   "invalid_generated_at",
		},
		{
			name:          "missing generated.at suppresses timestamp",
			generatedYAML: "generated:\n  by: process:existing\n",
			wantBy:        corpusString("process:existing"),
			wantBlocker:   "missing_generated_at",
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			document := "---\ntype: Knowledge\n" + test.generatedYAML +
				"timestamp: " + legacyTimestamp + "\n---\nA.\n"
			writeTestFile(t, root, "a.md", document)
			before := protocolTreeSnapshot(t, root)

			listedResult := callMCPTool(t, srv, "list_concepts", map[string]any{"bundle_path": root})
			if listedResult.IsError {
				t.Fatalf("list_concepts returned error: %s", resultText(t, listedResult))
			}
			var listed listConceptsStructured
			decodeCorpusStructured(t, listedResult, &listed)
			if len(listed.Concepts) != 1 ||
				!reflect.DeepEqual(listed.Concepts[0].GeneratedAt, test.wantEffective) ||
				listed.Concepts[0].LegacyDerived.Generated {
				t.Fatalf("list projection = %#v, want effective=%v and no timestamp fallback",
					listed.Concepts, test.wantEffective)
			}

			readResult := callMCPTool(t, srv, "read_concept", map[string]any{
				"bundle_path": root, "concept_id": "a",
			})
			if readResult.IsError {
				t.Fatalf("read_concept returned error: %s", resultText(t, readResult))
			}
			var read readConceptStructured
			decodeCorpusStructured(t, readResult, &read)
			if read.LegacyFallback.GeneratedFromTimestamp ||
				read.Projection.Generated == nil ||
				!reflect.DeepEqual(read.Projection.Generated.By, test.wantBy) ||
				!reflect.DeepEqual(read.Projection.Generated.At, test.wantAt) ||
				!strings.Contains(read.FrontmatterYAML, test.generatedYAML) {
				t.Fatalf("read projection = %#v, fallback=%#v",
					read.Projection.Generated, read.LegacyFallback)
			}

			previewResult := callMCPTool(t, srv, "preview_v02_migration", map[string]any{
				"bundle_path": root, "from": "0.1",
				"generated_by":     "process:mcp-fallback-matrix",
				"timestamp_policy": "preserve", "timestamp_conflict_policy": "reject",
				"citation_mappings": []any{},
			})
			if previewResult.IsError {
				t.Fatalf("preview_v02_migration returned error: %s", resultText(t, previewResult))
			}
			preview := previewResult.StructuredContent.(map[string]any)
			blockers := preview["blockers"].([]any)
			if preview["status"] != "blocked" ||
				len(preview["affected_paths"].([]any)) != 0 ||
				preview["plan_digest"] != "" ||
				preview["proof"] != nil ||
				len(blockers) != 1 ||
				blockers[0].(map[string]any)["code"] != test.wantBlocker ||
				strings.Contains(preview["diff"].(string), "generated:") {
				t.Fatalf("migration preview revived legacy timestamp fallback: %#v", preview)
			}

			after := protocolTreeSnapshot(t, root)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("read/preview matrix changed bundle tree:\nbefore=%#v\nafter=%#v", before, after)
			}
			matches, globErr := filepath.Glob(filepath.Join(root, ".okf"))
			if globErr != nil || len(matches) != 0 {
				t.Fatalf("read/preview matrix created .okf: matches=%#v err=%v", matches, globErr)
			}
		})
	}
}

func TestMCPLegacyFallbackObservationCancellationIsStable(t *testing.T) {
	document, err := bundle.ParseDocument("---\ntype: Note\n---\n" +
		strings.Repeat("Claim [1].\n\n# Section\n\n", 1024) +
		"# Citations\n\n[1] https://example.test\n")
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	ctx := &mcpCancelAfterErrContext{Context: t.Context(), remaining: 1}

	observation, result := legacyFallbackObservation(ctx, document)

	if observation != (bundle.LegacyFallbackObservation{}) || result == nil || !result.IsError {
		t.Fatalf("cancelled observation = (%#v, %#v), want stable tool error", observation, result)
	}
	envelope, ok := result.StructuredContent.(errorEnvelope)
	if !ok || envelope.Code != "operation_cancelled" || !envelope.Retryable ||
		len(envelope.Diagnostics) != 0 {
		t.Fatalf("cancelled observation envelope = %#v", result.StructuredContent)
	}
}

type mcpCancelAfterErrContext struct {
	context.Context
	remaining int
}

func (c *mcpCancelAfterErrContext) Err() error {
	if c.remaining == 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}

func assertFallbackFlags(t *testing.T, summary conceptSummaryV02, generated, citations bool) {
	t.Helper()
	if summary.LegacyDerived.Generated != generated || summary.LegacyDerived.Citations != citations {
		t.Fatalf("%s legacy fallback = %#v, want generated=%v citations=%v",
			summary.ID, summary.LegacyDerived, generated, citations)
	}
}

func hasLegacyFallbackGraphLink(nodes []map[string]any, property, conceptID string) bool {
	for _, node := range nodes {
		if !mcpTypedJSONLDBoolean(node["legacyFallback"]) {
			continue
		}
		if mcpJSONLDContainsIRI(node[property], conceptID) {
			return true
		}
	}
	return false
}

func mcpTypedJSONLDBoolean(value any) bool {
	object, ok := value.(map[string]any)
	return ok && object["@value"] == true && object["@type"] == "xsd:boolean"
}

func mcpJSONLDContainsIRI(value any, target string) bool {
	switch typed := value.(type) {
	case string:
		return typed == target
	case map[string]any:
		return typed["@id"] == target
	case []any:
		for _, item := range typed {
			if mcpJSONLDContainsIRI(item, target) {
				return true
			}
		}
	}
	return false
}
