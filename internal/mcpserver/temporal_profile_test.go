package mcpserver

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
)

func TestMCPInstantTemporalProfileAcrossReadValidateListAndGraph(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n")
	writeTestFile(t, root, "a.md", "---\ntype: Note\n"+
		"stale_after: 2026-09-23T18:00:00+07:00\n"+
		"sources:\n  - {id: s, resource: policy.md, last_modified: 2026-09-23T12:00:00Z}\n"+
		"usage_window: {from: 2026-09-23T18:00:00+07:00, to: 2026-09-23T12:00:00Z}\n"+
		"---\nBody.[^s]\n\n[^s]: Policy.\n")
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	profile := "instant-0b87c52"
	base := map[string]any{"bundle_path": root, "temporal_profile": profile}
	listArgs := map[string]any{"bundle_path": root, "temporal_profile": profile, "as_of": "2026-09-23T11:00:00Z"}
	readArgs := map[string]any{"bundle_path": root, "temporal_profile": profile, "concept_id": "a"}
	validateArgs := map[string]any{"bundle_path": root, "temporal_profile": profile, "strict": true, "as_of": "2026-09-23T11:00:00Z"}

	// Act.
	listed := callMCPTool(t, srv, "list_concepts", listArgs)
	read := callMCPTool(t, srv, "read_concept", readArgs)
	validated := callMCPTool(t, srv, "validate_bundle", validateArgs)
	graphed := callMCPTool(t, srv, "get_semantic_graph", base)

	// Assert.
	for name, result := range map[string]any{"list": listed, "read": read, "validate": validated, "graph": graphed} {
		if result == nil {
			t.Fatalf("%s nil", name)
		}
	}
	if listed.IsError || read.IsError || validated.IsError || graphed.IsError {
		t.Fatalf("tool errors: list=%s read=%s validate=%s graph=%s", resultText(t, listed), resultText(t, read), resultText(t, validated), resultText(t, graphed))
	}
	listMap := listed.StructuredContent.(map[string]any)
	if listMap["temporal_profile"] != profile {
		t.Fatalf("list profile: %#v", listMap)
	}
	concepts := listMap["concepts"].([]any)
	if len(concepts) != 1 || concepts[0].(map[string]any)["stale"] != true || concepts[0].(map[string]any)["source_count"] != float64(1) {
		t.Fatalf("list staleness: %#v", concepts)
	}
	readMap := read.StructuredContent.(map[string]any)
	projection := readMap["projection"].(map[string]any)
	if projection["stale_after"] != "2026-09-23T18:00:00+07:00" {
		t.Fatalf("read projection: %#v", projection)
	}
	sources := projection["sources"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["last_modified"] != "2026-09-23T12:00:00Z" {
		t.Fatalf("read sources: %#v", sources)
	}
	attributions := projection["attributions"].([]any)
	if len(attributions) != 1 || len(attributions[0].(map[string]any)["sources"].([]any)) != 1 {
		t.Fatalf("instant attribution join: %#v", attributions)
	}
	if validated.StructuredContent.(map[string]any)["temporal_profile"] != profile {
		t.Fatalf("validate profile: %#v", validated.StructuredContent)
	}
	graphMap := graphed.StructuredContent.(map[string]any)
	if graphMap["@graph"] == nil {
		t.Fatalf("graph projection: %#v", graphMap)
	}
}

func TestMCPInstantTemporalPatchPreviewApply(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n")
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	args := map[string]any{
		"bundle_path": root, "actor": "human:temporal", "temporal_profile": "instant-0b87c52",
		"operations": []any{
			map[string]any{"kind": "set_lifecycle", "concept_id": "a", "lifecycle": map[string]any{"status": nil, "stale_after": "2026-09-23T18:00:00+07:00"}},
			map[string]any{"kind": "set_usage_window", "concept_id": "a", "usage_window": map[string]any{"from": "2026-09-23T11:00:00Z", "to": "2026-09-23T12:00:00Z"}},
			map[string]any{"kind": "put_source", "concept_id": "a", "source": map[string]any{"resource": "urn:test:source", "last_modified": "2026-09-23T11:00:00Z"}},
		},
	}
	if err := validateContract("preview_concept_patch.input", args); err != nil {
		t.Fatal(err)
	}

	// Act.
	preview := callMCPTool(t, srv, "preview_concept_patch", args)
	if preview.IsError {
		t.Fatalf("preview: %s", resultText(t, preview))
	}
	projected := preview.StructuredContent.(map[string]any)
	if projected["status"] != "applicable" {
		t.Fatalf("preview: %#v", projected)
	}
	apply := cloneArguments(args)
	apply["expected_revision"] = projected["base_revision"]
	apply["expected_plan_digest"] = projected["plan_digest"]
	if err := validateContract("apply_concept_patch.input", apply); err != nil {
		t.Fatal(err)
	}
	applied := callMCPTool(t, srv, "apply_concept_patch", apply)

	// Assert.
	if applied.IsError || applied.StructuredContent.(map[string]any)["status"] != "applied" {
		t.Fatalf("apply: %s", resultText(t, applied))
	}
	document := readTestFile(t, root, "a.md")
	for _, expected := range []string{"2026-09-23T18:00:00+07:00", "2026-09-23T11:00:00Z", "2026-09-23T12:00:00Z"} {
		if !strings.Contains(document, expected) {
			t.Fatalf("missing %s in publication:\n%s", expected, document)
		}
	}
}

func TestMCPTemporalInputSchemasMatchReferenceParser(t *testing.T) {
	// Arrange.
	for _, tool := range []string{"list_concepts", "validate_bundle", "get_semantic_graph"} {
		for _, test := range []struct {
			raw   string
			valid bool
		}{
			{"2026-09-23T11:00:00Z", true},
			{"2026-09-23T18:00:00.123456789+07:00", true},
			{"2026-09-23T11:00:00-00:00", false},
			{"2026-09-23t11:00:00z", false},
			{"2026-09-23T11:00:00,123Z", false},
			{"2026-09-23T11:00:00+24:00", false},
			{"2026-09-23T11:00:00", false},
			{"2026-09-23", false},
		} {
			// Act.
			value := map[string]any{"bundle_path": "/tmp/bundle", "temporal_profile": "instant-0b87c52", "as_of": test.raw}
			err := validateContract(tool+".input", value)
			// Assert.
			if (err == nil) != test.valid {
				t.Errorf("%s as_of=%q schema err=%v, want valid=%t", tool, test.raw, err, test.valid)
			}
		}
	}
}

func TestMCPTemporalPatchSchemaAndConstructorsAgree(t *testing.T) {
	// Arrange.
	for _, profile := range []struct {
		name, temporal string
		valid          bool
	}{
		{"legacy-date", "2026-09-23", true},
		{"instant", "2026-09-23T18:00:00+07:00", true},
		{"wrong-profile", "2026-09-23", false},
		{"unknown-offset", "2026-09-23T18:00:00-00:00", false},
		{"lowercase", "2026-09-23t18:00:00z", false},
	} {
		selected := "instant-0b87c52"
		if profile.name == "legacy-date" {
			selected = "date-3fcbb9f"
		}
		operations := []any{map[string]any{"kind": "set_lifecycle", "concept_id": "a", "lifecycle": map[string]any{"status": nil, "stale_after": profile.temporal}}}
		request := map[string]any{"bundle_path": "/tmp/bundle", "actor": "human:test", "temporal_profile": selected, "operations": operations}
		// Act and assert.
		for _, name := range []string{"preview_concept_patch.input", "apply_concept_patch.input"} {
			input := map[string]any{}
			for key, value := range request {
				input[key] = value
			}
			if name == "apply_concept_patch.input" {
				input["expected_revision"] = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				input["expected_plan_digest"] = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			}
			err := validateContract(name, input)
			if (err == nil) != profile.valid {
				t.Errorf("%s/%s schema err=%v want valid=%t", name, profile.name, err, profile.valid)
			}
		}
	}
}
