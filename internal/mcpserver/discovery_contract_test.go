package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/mark3labs/mcp-go/server"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestTwoMCPServerCatalogsRequireServerQualifiedSelection(t *testing.T) {
	// Arrange. Both live MCP clients expose the same unqualified basename.
	okfServer, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	otherServer := server.NewMCPServer("other-mcp", "1.0.0", server.WithToolCapabilities(false))
	otherCalls := 0
	otherServer.AddTool(mcp.NewTool("list_concepts", mcp.WithDescription("Unrelated catalog")), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		otherCalls++
		return mcp.NewToolResultText("other server"), nil
	})
	type catalog struct {
		client *client.Client
		tools  map[string]bool
	}
	catalogs := map[string]catalog{}
	for _, srv := range []*server.MCPServer{okfServer, otherServer} {
		c, err := client.NewInProcessClient(srv)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		request := mcp.InitializeRequest{}
		request.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		request.Params.ClientInfo = mcp.Implementation{Name: "discovery-test", Version: "1.0.0"}
		initialized, err := c.Initialize(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		listed, err := c.ListTools(t.Context(), mcp.ListToolsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		tools := make(map[string]bool, len(listed.Tools))
		for _, tool := range listed.Tools {
			tools[tool.Name] = true
		}
		catalogs[initialized.ServerInfo.Name] = catalog{client: c, tools: tools}
	}
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n\n- [Alpha](alpha.md)\n")
	writeTestFile(t, root, "alpha.md", "---\ntype: Note\n---\nAlpha.\n")

	// Act. The host adapter resolves a (server name, declared tool name) pair;
	// neither server requires or advertises a universal client-side prefix.
	okfCatalog, ok := catalogs["okf-mcp"]
	if !ok || !okfCatalog.tools["list_concepts"] || !catalogs["other-mcp"].tools["list_concepts"] {
		t.Fatalf("expected two catalogs with duplicate basename: %#v", catalogs)
	}
	result, err := okfCatalog.client.CallTool(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "list_concepts", Arguments: map[string]any{"bundle_path": root},
	}})

	// Assert. The selected OKF endpoint runs; the same-named unrelated tool does not.
	if err != nil || result == nil || result.IsError || otherCalls != 0 {
		t.Fatalf("qualified selection: result=%#v, err=%v, unrelated calls=%d", result, err, otherCalls)
	}
	if result.StructuredContent == nil {
		t.Fatal("OKF list_concepts returned no structured content")
	}
}

func TestToolDiscoveryAnnotationsAndStableWireList(t *testing.T) {
	// Arrange.
	tools := mustServerTools(t)
	serverOne, err := mcptest.NewServer(t, tools...)
	if err != nil {
		t.Fatalf("first MCP server: %v", err)
	}
	defer serverOne.Close()
	serverTwo, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("second MCP server: %v", err)
	}
	defer serverTwo.Close()
	list := func(t *testing.T, srv *mcptest.Server) []mcp.Tool {
		t.Helper()
		result, err := srv.Client().ListTools(t.Context(), mcp.ListToolsRequest{})
		if err != nil {
			t.Fatalf("tools/list: %v", err)
		}
		return result.Tools
	}

	// Act.
	first := list(t, serverOne)
	repeated := list(t, serverOne)
	restarted := list(t, serverTwo)
	firstWire, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	repeatedWire, err := json.Marshal(repeated)
	if err != nil {
		t.Fatal(err)
	}
	restartedWire, err := json.Marshal(restarted)
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if !bytes.Equal(firstWire, repeatedWire) || !bytes.Equal(firstWire, restartedWire) {
		t.Fatal("tools/list changed across repeated requests or server restart")
	}
	if len(first) != len(advertisedContractRows) {
		t.Fatalf("tools/list count = %d, want %d", len(first), len(advertisedContractRows))
	}
	rows := append([]advertisedContractRow(nil), advertisedContractRows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	for i, row := range rows {
		tool := first[i]
		if tool.Name != row.name {
			t.Fatalf("tool order at %d = %q, want %q", i, tool.Name, row.name)
		}
		annotations := tool.Annotations
		if annotations.ReadOnlyHint == nil || *annotations.ReadOnlyHint != row.readOnly ||
			annotations.DestructiveHint == nil || *annotations.DestructiveHint == row.readOnly ||
			annotations.IdempotentHint == nil || *annotations.IdempotentHint != row.readOnly ||
			annotations.OpenWorldHint == nil || *annotations.OpenWorldHint {
			t.Fatalf("%s annotations = %#v", tool.Name, annotations)
		}
	}
}

func TestAdvertisedInputExamplesValidateTheirPropertySchemas(t *testing.T) {
	// Arrange, act, assert. Each example is checked against its own advertised
	// property, including local $defs, so prose cannot silently drift from wire.
	for _, row := range advertisedContractRows {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n\n- [Alpha](alpha.md)\n")
			writeTestFile(t, root, "alpha.md", "---\ntype: Note\n---\nAlpha.\n")
			base := row.input(t, root)
			data, err := contractSchema(row.name + ".input")
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			properties := document["properties"].(map[string]any)
			for name, raw := range properties {
				property := raw.(map[string]any)
				examples, ok := property["examples"].([]any)
				if !ok {
					continue
				}
				if len(examples) == 0 {
					t.Fatalf("%s has empty examples", name)
				}
				propertyDoc := map[string]any{"$schema": document["$schema"]}
				if defs, ok := document["$defs"]; ok {
					propertyDoc["$defs"] = defs
				}
				for key, value := range property {
					propertyDoc[key] = value
				}
				propertyData, err := json.Marshal(propertyDoc)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(propertyData))
				if err != nil {
					t.Fatal(err)
				}
				compiler := jsonschema.NewCompiler()
				compiler.AssertFormat()
				resource := fmt.Sprintf("https://okf.dev/mcp/discovery-test/%s-%s", row.name, name)
				if err := compiler.AddResource(resource, decoded); err != nil {
					t.Fatal(err)
				}
				compiled, err := compiler.Compile(resource)
				if err != nil {
					t.Fatal(err)
				}
				for _, example := range examples {
					if err := compiled.Validate(example); err != nil {
						t.Errorf("%s example %v violates property schema: %v", name, example, err)
					}
					candidate := cloneArguments(base)
					if row.name == "apply_v02_migration" && name == "expected_plan_digest" {
						transitionRoot := t.TempDir()
						writeTestFile(t, transitionRoot, "alpha.md", "---\ntype: Note\n---\nAlpha.\n")
						candidate = migrationContractArguments(transitionRoot, false)
						candidate["from"] = "0.1"
						preview := callHandler(t, contractToolHandler("preview_v02_migration", handlePreviewV02Migration), candidate)
						if preview.IsError {
							t.Fatalf("transition preview: %s", resultText(t, preview))
						}
						plan := preview.StructuredContent.(migrationPreviewResponse)
						candidate["expected_source"] = plan.Source
						candidate["proof"] = plan.Proof
					}
					candidate[name] = example
					if err := validateContract(row.name+".input", candidate); err != nil {
						t.Errorf("%s example %v violates full input schema: %v", name, example, err)
					}
				}
			}
		})
	}
}

func TestToolDiscoveryKeepsExistingWireShape(t *testing.T) {
	// Arrange.
	tools := mustServerTools(t)
	wantNames := make([]string, len(advertisedContractRows))
	for i, row := range advertisedContractRows {
		wantNames[i] = row.name
	}

	// Act.
	gotNames := make([]string, len(tools))
	for i, tool := range tools {
		gotNames[i] = tool.Tool.Name
	}

	// Assert.
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("tool names/order changed: got %v, want %v", gotNames, wantNames)
	}
}
