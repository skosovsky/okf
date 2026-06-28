package mcpserver

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
)

func TestMCPServerListsExpectedToolsAndSchemas(t *testing.T) {
	srv, err := mcptest.NewServer(t, ServerTools()...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	if !srv.Client().IsInitialized() {
		t.Fatal("MCP client is not initialized after mcptest server start")
	}

	result, err := srv.Client().ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	got := map[string]mcp.Tool{}
	for _, tool := range result.Tools {
		got[tool.Name] = tool
		if tool.Description == "" {
			t.Fatalf("tool %s has empty description", tool.Name)
		}
		if tool.InputSchema.Type != "object" {
			t.Fatalf("tool %s schema type = %q, want object", tool.Name, tool.InputSchema.Type)
		}
		if tool.InputSchema.AdditionalProperties != false {
			t.Fatalf("tool %s allows additionalProperties: %#v", tool.Name, tool.InputSchema.AdditionalProperties)
		}
	}

	assertToolSchema(t, got, "list_concepts", []string{"bundle_path"}, true)
	assertToolSchema(t, got, "read_concept", []string{"bundle_path", "concept_id"}, true)
	assertToolSchema(t, got, "validate_bundle", []string{"bundle_path"}, true)
	assertBooleanDefault(t, got["validate_bundle"], "strict", false)
	assertBooleanDefault(t, got["validate_bundle"], "check_links", false)
	assertBooleanDefault(t, got["validate_bundle"], "check_orphans", false)
	assertToolSchema(t, got, "get_semantic_graph", []string{"bundle_path"}, true)
	assertToolSchema(t, got, "write_concept", []string{"bundle_path", "concept_id", "frontmatter", "body"}, false)
}

func TestMCPServerCallToolRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\ntitle: A\n---\nA.\n")

	srv, err := mcptest.NewServer(t, ServerTools()...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	listResult := callMCPTool(t, srv, "list_concepts", map[string]any{"bundle_path": root})
	if listResult.IsError {
		t.Fatalf("list_concepts returned error: %s", resultText(t, listResult))
	}
	var listed listConceptsResponse
	decodeResult(t, listResult, &listed)
	if len(listed.Concepts) != 1 || listed.Concepts[0].ID != "a" {
		t.Fatalf("list response = %#v", listed)
	}

	writeResult := callMCPTool(t, srv, "write_concept", map[string]any{
		"bundle_path": root,
		"concept_id":  "b",
		"frontmatter": "type: Note\ntitle: B\n",
		"body":        "B.\n",
	})
	if writeResult.IsError {
		t.Fatalf("write_concept returned error: %s", resultText(t, writeResult))
	}
	if got := readTestFile(t, root, "b.md"); got != "---\ntype: Note\ntitle: B\n---\n\nB.\n" {
		t.Fatalf("written file = %q", got)
	}
}

func TestMCPServerCallToolBusinessErrorsStayToolErrors(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "bad.md", "---\ntype: [\n---\nBad.\n")
	validRoot := t.TempDir()
	writeTestFile(t, validRoot, "a.md", "---\ntype: Note\n---\nA.\n")

	srv, err := mcptest.NewServer(t, ServerTools()...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{
			name: "relative bundle path",
			tool: "list_concepts",
			args: map[string]any{"bundle_path": "relative"},
		},
		{
			name: "invalid validate flag type",
			tool: "validate_bundle",
			args: map[string]any{"bundle_path": validRoot, "strict": "true"},
		},
		{
			name: "parse error list",
			tool: "list_concepts",
			args: map[string]any{"bundle_path": root},
		},
		{
			name: "parse error graph",
			tool: "get_semantic_graph",
			args: map[string]any{"bundle_path": root},
		},
		{
			name: "missing concept",
			tool: "read_concept",
			args: map[string]any{"bundle_path": root, "concept_id": "missing"},
		},
		{
			name: "write validation rejection",
			tool: "write_concept",
			args: map[string]any{
				"bundle_path": root, "concept_id": "new",
				"frontmatter": "title: Missing Type\n", "body": "Body.\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callMCPTool(t, srv, tt.tool, tt.args)
			if !result.IsError {
				t.Fatalf("%s returned success: %s", tt.tool, resultText(t, result))
			}
			if !json.Valid([]byte(resultText(t, result))) && resultText(t, result) == "" {
				t.Fatalf("%s returned empty error text", tt.tool)
			}
		})
	}
}

func callMCPTool(t *testing.T, srv *mcptest.Server, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	result, err := srv.Client().CallTool(t.Context(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
	if err != nil {
		t.Fatalf("CallTool(%s) protocol error: %v", name, err)
	}
	return result
}

func assertToolSchema(t *testing.T, tools map[string]mcp.Tool, name string, required []string, readOnly bool) {
	t.Helper()

	tool, ok := tools[name]
	if !ok {
		t.Fatalf("missing tool %s", name)
	}
	if !slices.Equal(tool.InputSchema.Required, required) {
		t.Fatalf("tool %s required = %#v, want %#v", name, tool.InputSchema.Required, required)
	}
	for _, prop := range required {
		if _, ok := tool.InputSchema.Properties[prop]; !ok {
			t.Fatalf("tool %s missing required property %s", name, prop)
		}
	}
	if tool.Annotations.ReadOnlyHint == nil || *tool.Annotations.ReadOnlyHint != readOnly {
		t.Fatalf("tool %s readOnlyHint = %#v, want %v", name, tool.Annotations.ReadOnlyHint, readOnly)
	}
	if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
		t.Fatalf("tool %s openWorldHint = %#v, want false", name, tool.Annotations.OpenWorldHint)
	}
}

func assertBooleanDefault(t *testing.T, tool mcp.Tool, name string, want bool) {
	t.Helper()

	raw, ok := tool.InputSchema.Properties[name]
	if !ok {
		t.Fatalf("tool %s missing property %s", tool.Name, name)
	}
	prop, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("tool %s property %s type = %T, want map[string]any", tool.Name, name, raw)
	}
	if prop["type"] != "boolean" {
		t.Fatalf("tool %s property %s schema type = %#v, want boolean", tool.Name, name, prop["type"])
	}
	if prop["default"] != want {
		t.Fatalf("tool %s property %s default = %#v, want %v", tool.Name, name, prop["default"], want)
	}
}
