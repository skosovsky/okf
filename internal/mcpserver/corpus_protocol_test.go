package mcpserver

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

type mcpCorpusManifest struct {
	Cases map[string]string `yaml:"cases"`
}

type mcpCorpusCaseManifest struct {
	Run struct {
		Path         string `yaml:"path"`
		Strict       bool   `yaml:"strict"`
		CheckLinks   bool   `yaml:"check_links"`
		CheckOrphans bool   `yaml:"check_orphans"`
	} `yaml:"run"`
	Expected struct {
		ExitCode    int   `yaml:"exit_code"`
		Diagnostics []any `yaml:"diagnostics"`
	} `yaml:"expected"`
}

type mcpCorpusProtocolCase struct {
	Key                   string
	ExpectedDeclared      *string
	ExpectedEffective     string
	ExpectedResolution    string
	ExpectedCompatibility string
	PreviewPatch          bool
	PreviewMigration      bool
}

var mcpCorpusProtocolCases = []mcpCorpusProtocolCase{
	{
		Key: "appendix_a_v02", ExpectedDeclared: corpusString("0.2"),
		ExpectedEffective: "0.2", ExpectedResolution: "declared", ExpectedCompatibility: "native",
		PreviewPatch: true,
	},
	{
		Key: "v01_declared", ExpectedDeclared: corpusString("0.1"),
		ExpectedEffective: "0.1", ExpectedResolution: "declared", ExpectedCompatibility: "legacy",
		PreviewMigration: true,
	},
	{
		Key:               "v01_undeclared",
		ExpectedEffective: "0.2", ExpectedResolution: "default", ExpectedCompatibility: "native",
		PreviewMigration: true,
	},
	{
		Key: "mixed_legacy_v02", ExpectedDeclared: corpusString("0.2"),
		ExpectedEffective: "0.2", ExpectedResolution: "declared", ExpectedCompatibility: "native",
		PreviewMigration: true,
	},
	{
		Key: "unknown_future", ExpectedDeclared: corpusString("9.9"),
		ExpectedEffective: "0.2", ExpectedResolution: "future-best-effort", ExpectedCompatibility: "best-effort",
		PreviewMigration: true,
	},
}

func TestMCPCorpusManifestCoverageLock(t *testing.T) {
	fixtureRoot, manifest := loadMCPCorpusManifest(t)
	covered := make(map[string]struct{}, len(mcpCorpusProtocolCases))
	for _, test := range mcpCorpusProtocolCases {
		if _, duplicate := covered[test.Key]; duplicate {
			t.Fatalf("MCP corpus coverage contains duplicate key %q", test.Key)
		}
		covered[test.Key] = struct{}{}
		resolveMCPCorpusCase(t, fixtureRoot, manifest, test.Key)
	}

	for key, relative := range manifest.Cases {
		if key != "appendix_a_v02" && !strings.HasPrefix(relative, "compat/") {
			continue
		}
		if _, ok := covered[key]; !ok {
			t.Fatalf("corpus manifest case %q at %q is not referenced by MCP protocol coverage", key, relative)
		}
	}
}

func TestMCPCanonicalCorpusThroughProtocolSurfaces(t *testing.T) {
	fixtureRoot, manifest := loadMCPCorpusManifest(t)
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	for _, test := range mcpCorpusProtocolCases {
		t.Run(test.Key, func(t *testing.T) {
			root, fixture := resolveMCPCorpusCase(t, fixtureRoot, manifest, test.Key)
			before := protocolTreeSnapshot(t, root)

			listResult := callMCPTool(t, srv, "list_concepts", map[string]any{"bundle_path": root})
			assertCorpusToolSuccess(t, "list_concepts", listResult)
			var listed listConceptsStructured
			decodeCorpusStructured(t, listResult, &listed)
			assertCorpusVersion(t, test, listed.Version)
			if len(listed.Concepts) == 0 {
				t.Fatal("manifest-selected fixture produced no concepts")
			}

			for _, summary := range listed.Concepts {
				readResult := callMCPTool(t, srv, "read_concept", map[string]any{
					"bundle_path": root,
					"concept_id":  summary.ID,
				})
				assertCorpusToolSuccess(t, "read_concept", readResult)
				var read readConceptStructured
				decodeCorpusStructured(t, readResult, &read)
				assertCorpusVersion(t, test, read.Version)
				if read.ID != summary.ID || read.Path != summary.Path {
					t.Fatalf("read identity = (%q, %q), list identity = (%q, %q)",
						read.ID, read.Path, summary.ID, summary.Path)
				}
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(summary.Path)))
				if err != nil {
					t.Fatalf("ReadFile(%s) error = %v", summary.Path, err)
				}
				document, err := bundle.ParseDocument(string(data))
				if err != nil {
					t.Fatalf("ParseDocument(%s) error = %v", summary.Path, err)
				}
				fallback, err := document.LegacyFallbackObservationContext(t.Context())
				if err != nil {
					t.Fatalf("LegacyFallbackObservationContext(%s) error = %v", summary.Path, err)
				}
				if summary.LegacyDerived.Generated != fallback.TimestampActive ||
					summary.LegacyDerived.Citations != fallback.CitationsActive ||
					read.LegacyFallback.GeneratedFromTimestamp != fallback.TimestampActive ||
					read.LegacyFallback.SourcesFromCitations != fallback.CitationsActive {
					t.Fatalf("MCP fallback projection for %s = list %#v, read %#v; shared observation = %#v",
						summary.Path, summary.LegacyDerived, read.LegacyFallback, fallback)
				}
			}

			validateResult := callMCPTool(t, srv, "validate_bundle", map[string]any{
				"bundle_path":   root,
				"strict":        fixture.Run.Strict,
				"check_links":   fixture.Run.CheckLinks,
				"check_orphans": fixture.Run.CheckOrphans,
			})
			assertCorpusToolSuccess(t, "validate_bundle", validateResult)
			var validated validateBundleStructured
			decodeCorpusStructured(t, validateResult, &validated)
			assertCorpusVersion(t, test, validated.Version)
			if fixture.Expected.ExitCode != 0 || len(fixture.Expected.Diagnostics) != 0 {
				t.Fatalf("MCP corpus case %q is not locked as a positive fixture: %#v", test.Key, fixture.Expected)
			}
			if !validated.Conformance.Conformant || validated.Conformance.Errors != 0 ||
				len(validated.Diagnostics) != len(fixture.Expected.Diagnostics) {
				t.Fatalf("validate result = %#v, fixture expected = %#v", validated, fixture.Expected)
			}

			graphResult := callMCPTool(t, srv, "get_semantic_graph", map[string]any{"bundle_path": root})
			assertCorpusToolSuccess(t, "get_semantic_graph", graphResult)
			var graphed struct {
				Graph []map[string]any `json:"@graph"`
			}
			decodeCorpusStructured(t, graphResult, &graphed)
			graphIDs := make(map[string]struct{}, len(graphed.Graph))
			for _, node := range graphed.Graph {
				if id, ok := node["@id"].(string); ok {
					graphIDs[id] = struct{}{}
				}
			}
			for _, concept := range listed.Concepts {
				id := "local:bundle:" + url.PathEscape(concept.ID)
				if _, ok := graphIDs[id]; !ok {
					t.Fatalf("semantic graph does not contain listed concept %q as %q", concept.ID, id)
				}
			}

			if test.PreviewPatch {
				preview := callMCPTool(t, srv, "preview_concept_patch", map[string]any{
					"bundle_path": root,
					"actor":       "tool:mcp-corpus-e2e",
					"operations": []any{map[string]any{
						"kind":       "set_lifecycle",
						"concept_id": listed.Concepts[0].ID,
						"lifecycle":  map[string]any{"status": "draft", "stale_after": nil},
					}},
				})
				assertCorpusToolSuccess(t, "preview_concept_patch", preview)
			}
			if test.PreviewMigration {
				preview := callMCPTool(t, srv, "preview_v02_migration", map[string]any{
					"bundle_path":               root,
					"from":                      "auto",
					"generated_by":              "process:mcp-corpus-e2e",
					"timestamp_policy":          "preserve",
					"timestamp_conflict_policy": "reject",
					"citation_mappings":         []any{},
				})
				assertCorpusToolSuccess(t, "preview_v02_migration", preview)
			}

			after := protocolTreeSnapshot(t, root)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("MCP read/preview surfaces changed manifest fixture %q:\nbefore=%#v\nafter=%#v",
					test.Key, before, after)
			}
		})
	}
}

func loadMCPCorpusManifest(t *testing.T) (string, mcpCorpusManifest) {
	t.Helper()
	fixtureRoot, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "v02"))
	if err != nil {
		t.Fatalf("Abs(fixtures/v02) error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(fixtureRoot, "corpus.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(corpus.yaml) error = %v", err)
	}
	var manifest mcpCorpusManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal(corpus.yaml) error = %v", err)
	}
	if len(manifest.Cases) == 0 {
		t.Fatal("corpus.yaml has no cases")
	}
	return fixtureRoot, manifest
}

func resolveMCPCorpusCase(
	t *testing.T,
	fixtureRoot string,
	manifest mcpCorpusManifest,
	key string,
) (string, mcpCorpusCaseManifest) {
	t.Helper()
	relative, ok := manifest.Cases[key]
	if !ok || relative == "" {
		t.Fatalf("corpus.yaml is missing MCP case %q", key)
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		t.Fatalf("corpus case %q escapes fixture root: %q", key, relative)
	}
	caseRoot := filepath.Join(fixtureRoot, clean)
	info, err := os.Stat(caseRoot)
	if err != nil {
		t.Fatalf("Stat(corpus case %q at %q) error = %v", key, relative, err)
	}
	if !info.IsDir() {
		t.Fatalf("corpus case %q at %q is not a directory", key, relative)
	}

	data, err := os.ReadFile(filepath.Join(caseRoot, "manifest.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(%s manifest.yaml) error = %v", key, err)
	}
	var fixture mcpCorpusCaseManifest
	if err := yaml.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("Unmarshal(%s manifest.yaml) error = %v", key, err)
	}
	runRoot := filepath.Clean(filepath.Join(caseRoot, filepath.FromSlash(fixture.Run.Path)))
	relativeRunRoot, err := filepath.Rel(caseRoot, runRoot)
	if err != nil || relativeRunRoot == ".." ||
		strings.HasPrefix(relativeRunRoot, ".."+string(filepath.Separator)) {
		t.Fatalf("fixture run path for %q escapes its corpus case: %q", key, fixture.Run.Path)
	}
	return runRoot, fixture
}

func assertCorpusToolSuccess(t *testing.T, name string, result *mcp.CallToolResult) {
	t.Helper()
	if result.IsError {
		t.Fatalf("%s returned error: %s", name, resultText(t, result))
	}
}

func decodeCorpusStructured(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured result: %v", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode structured result: %v\n%s", err, data)
	}
}

func assertCorpusVersion(t *testing.T, test mcpCorpusProtocolCase, got versionDTO) {
	t.Helper()
	if !reflect.DeepEqual(got.Declared, test.ExpectedDeclared) ||
		stringPointerValue(got.Effective) != test.ExpectedEffective ||
		stringPointerValue(got.Resolution) != test.ExpectedResolution ||
		stringPointerValue(got.Compatibility) != test.ExpectedCompatibility {
		encoded, _ := json.Marshal(got)
		t.Fatalf("version for corpus case %q = %s", test.Key, encoded)
	}
}

func corpusString(value string) *string {
	return &value
}
