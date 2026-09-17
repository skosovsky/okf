package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/mark3labs/mcp-go/server"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
)

func TestMCPAttestedComputationModeMapsExactlyAndPreservesEmptyInlinePresence(t *testing.T) {
	// Arrange.
	tests := []struct {
		name            string
		contract        map[string]any
		wantMode        store.AttestedComputationMode
		wantInline      string
		wantPath        string
		wantInlineField bool
	}{
		{
			name: "inline empty",
			contract: mcpAttestedComputationContract(map[string]any{
				"mode":               "inline",
				"inline_computation": "",
				"inline_language":    "text",
			}),
			wantMode:        store.AttestedComputationModeInline,
			wantInlineField: true,
		},
		{
			name: "file",
			contract: mcpAttestedComputationContract(map[string]any{
				"mode":             "file",
				"computation_path": "assets/query.sql",
			}),
			wantMode: store.AttestedComputationModeFile,
			wantPath: "assets/query.sql",
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments := map[string]any{
				"bundle_path": "/schema-only",
				"actor":       "mcp:mode-adapter",
				"operations": []any{map[string]any{
					"kind":                 "put_attested_computation",
					"concept_id":           "query",
					"attested_computation": test.contract,
				}},
			}
			if err := validateContract("preview_concept_patch.input", arguments); err != nil {
				t.Fatalf("valid input schema error = %v", err)
			}
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{
				Name: "preview_concept_patch", Arguments: arguments,
			}}
			decoded, result := decodePatchArguments(request)
			if result != nil {
				t.Fatalf("decodePatchArguments() error = %s", resultText(t, result))
			}
			input := decoded.Operations[0].AttestedComputation
			if input == nil {
				t.Fatal("attested computation input is nil")
			}
			if (input.InlineComputation != nil) != test.wantInlineField {
				t.Fatalf(
					"inline_computation presence = %v, want %v",
					input.InlineComputation != nil,
					test.wantInlineField,
				)
			}
			operations, result := buildPatchOperations(decoded.Operations)
			if result != nil {
				t.Fatalf("buildPatchOperations() error = %s", resultText(t, result))
			}
			operation, ok := operations[0].(store.PutAttestedComputation)
			if !ok {
				t.Fatalf("operation = %T, want store.PutAttestedComputation", operations[0])
			}
			contract := operation.Contract()
			if contract.Mode != test.wantMode ||
				contract.InlineComputation != test.wantInline ||
				contract.ComputationPath != test.wantPath {
				t.Fatalf("mapped contract = %#v", contract)
			}
		})
	}
}

func TestMCPAttestedComputationModeRejectsInvalidShapesBeforeHandlerAndIO(t *testing.T) {
	// Arrange.
	patchRoot := t.TempDir()
	writeTestFile(t, patchRoot, "index.md",
		"---\nokf_version: \"0.2\"\n---\n# Knowledge\n- [Query](query.md)\n",
	)
	writeTestFile(t, patchRoot, "query.md", "---\ntype: Knowledge\n---\nQuery.\n")
	migrationRoot := t.TempDir()
	writeTestFile(t, migrationRoot, "index.md",
		"---\nokf_version: \"0.1\"\n---\n# Knowledge\n- [Query](query.md)\n",
	)
	writeTestFile(t, migrationRoot, "query.md",
		"---\ntype: Knowledge\n---\nQuery.\n\n# Computation\n\n```text\n```\n",
	)
	patchArguments := func(root string, apply bool) map[string]any {
		arguments := map[string]any{
			"bundle_path": root,
			"actor":       "mcp:mode-adapter",
			"operations": []any{map[string]any{
				"kind":       "put_attested_computation",
				"concept_id": "query",
				"attested_computation": mcpAttestedComputationContract(map[string]any{
					"mode":               "inline",
					"inline_computation": "",
					"inline_language":    "text",
				}),
			}},
		}
		if apply {
			arguments["expected_revision"] = "sha256:" + strings.Repeat("a", 64)
			arguments["expected_plan_digest"] = "sha256:" + strings.Repeat("b", 64)
		}
		return arguments
	}
	migrationArguments := func(root string, apply bool) map[string]any {
		arguments := map[string]any{
			"bundle_path":       root,
			"from":              "auto",
			"generated_by":      "process:migration",
			"timestamp_policy":  "preserve",
			"citation_mappings": []any{},
			"computations": []any{map[string]any{
				"concept_id": "query",
				"contract": mcpAttestedComputationContract(map[string]any{
					"mode":               "inline",
					"inline_computation": "",
					"inline_language":    "text",
				}),
			}},
		}
		if apply {
			arguments["expected_source"] = testMigrationSourceValue(
				mutation.MigrationVersionV02,
				string(mutation.MigrationTransitionTargetNoop),
			)
		}
		return arguments
	}
	surfaces := []struct {
		name     string
		toolName string
		root     string
		handler  server.ToolHandlerFunc
		base     map[string]any
	}{
		{
			name:     "patch preview",
			toolName: "preview_concept_patch",
			root:     patchRoot,
			handler:  handlePreviewConceptPatch,
			base:     patchArguments(patchRoot, false),
		},
		{
			name:     "patch apply",
			toolName: "apply_concept_patch",
			root:     patchRoot,
			handler:  handleApplyConceptPatch,
			base:     patchArguments(patchRoot, true),
		},
		{
			name:     "migration preview",
			toolName: "preview_v02_migration",
			root:     migrationRoot,
			handler:  handlePreviewV02Migration,
			base:     migrationArguments(migrationRoot, false),
		},
		{
			name:     "migration apply target-noop",
			toolName: "apply_v02_migration",
			root:     migrationRoot,
			handler:  handleApplyV02Migration,
			base:     migrationArguments(migrationRoot, true),
		},
	}
	invalidShapes := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name:   "missing mode",
			mutate: func(contract map[string]any) { delete(contract, "mode") },
		},
		{
			name:   "unknown mode",
			mutate: func(contract map[string]any) { contract["mode"] = "stream" },
		},
		{
			name: "inline missing payload",
			mutate: func(contract map[string]any) {
				delete(contract, "inline_computation")
			},
		},
		{
			name: "inline mixed with path",
			mutate: func(contract map[string]any) {
				contract["computation_path"] = "assets/query.sql"
			},
		},
		{
			name: "file mixed with present empty inline payload",
			mutate: func(contract map[string]any) {
				contract["mode"] = "file"
				contract["computation_path"] = "assets/query.sql"
			},
		},
		{
			name: "file missing path",
			mutate: func(contract map[string]any) {
				contract["mode"] = "file"
				delete(contract, "inline_computation")
				delete(contract, "inline_language")
			},
		},
	}
	sourceOpens, sourceReads, storeOpens := 0, 0, 0
	ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
		sourceOpen: func() { sourceOpens++ },
		sourceRead: func() { sourceReads++ },
		storeOpen:  func() { storeOpens++ },
	})

	// Act and assert.
	for _, surface := range surfaces {
		if err := validateContract(surface.toolName+".input", surface.base); err != nil {
			t.Fatalf("%s valid baseline schema error = %v", surface.name, err)
		}
		for _, invalid := range invalidShapes {
			t.Run(surface.name+"/"+invalid.name, func(t *testing.T) {
				arguments := deepCloneMap(t, surface.base)
				contract := mcpModeContractFromArguments(t, surface.toolName, arguments)
				invalid.mutate(contract)
				if err := validateContract(surface.toolName+".input", arguments); err == nil {
					t.Fatalf(
						"invalid mode shape unexpectedly satisfies input schema: %#v",
						contract,
					)
				}
				before := protocolTreeSnapshot(t, surface.root)
				handlerCalls := 0
				handler := contractToolHandler(surface.toolName, func(
					ctx context.Context,
					request mcp.CallToolRequest,
				) (*mcp.CallToolResult, error) {
					handlerCalls++
					return surface.handler(ctx, request)
				})
				result, err := handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
					Name: surface.toolName, Arguments: arguments,
				}})
				if err != nil {
					t.Fatalf("handler protocol error = %v", err)
				}
				assertSchemaErrorEnvelope(t, result, "schema_validation")
				if handlerCalls != 0 {
					t.Fatalf("handler calls = %d, want 0", handlerCalls)
				}
				if sourceOpens != 0 || sourceReads != 0 || storeOpens != 0 {
					t.Fatalf(
						"I/O counts = source-open:%d source-read:%d store-open:%d, want all zero",
						sourceOpens,
						sourceReads,
						storeOpens,
					)
				}
				if after := protocolTreeSnapshot(t, surface.root); !reflect.DeepEqual(after, before) {
					t.Fatalf("bundle changed:\nbefore=%#v\nafter=%#v", before, after)
				}
				if _, statErr := os.Lstat(filepath.Join(surface.root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("transactional store was opened: %v", statErr)
				}
			})
		}
	}
}

func TestMCPAttestedComputationInlineEmptyPatchPreviewApplyAndDigest(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md",
		"---\nokf_version: \"0.2\"\n---\n# Knowledge\n- [Query](query.md)\n",
	)
	writeTestFile(t, root, "query.md", "---\ntype: Knowledge\n---\nQuery.\n")
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	arguments := map[string]any{
		"bundle_path": root,
		"actor":       "mcp:mode-adapter",
		"operations": []any{map[string]any{
			"kind":       "put_attested_computation",
			"concept_id": "query",
			"attested_computation": mcpAttestedComputationContract(map[string]any{
				"mode":               "inline",
				"inline_computation": "",
				"inline_language":    "text",
			}),
		}},
	}

	// Act.
	preview := callMCPTool(t, srv, "preview_concept_patch", arguments)

	// Assert.
	if preview.IsError {
		t.Fatalf("inline preview returned error: %s", resultText(t, preview))
	}
	inlinePlan := preview.StructuredContent.(map[string]any)
	if inlinePlan["status"] != "applicable" || inlinePlan["plan_digest"] == "" {
		t.Fatalf("inline preview = %#v", inlinePlan)
	}
	inlineBaseRevision := inlinePlan["base_revision"]
	inlinePlanDigest := inlinePlan["plan_digest"]
	fileArguments := deepCloneMap(t, arguments)
	fileRoot := t.TempDir()
	writeTestFile(t, fileRoot, "index.md",
		"---\nokf_version: \"0.2\"\n---\n# Knowledge\n- [Query](query.md)\n",
	)
	writeTestFile(t, fileRoot, "query.md", "---\ntype: Knowledge\n---\nQuery.\n")
	fileArguments["bundle_path"] = fileRoot
	fileContract := mcpModeContractFromArguments(t, "preview_concept_patch", fileArguments)
	fileContract["mode"] = "file"
	fileContract["computation_path"] = "assets/query.sql"
	delete(fileContract, "inline_computation")
	delete(fileContract, "inline_language")
	filePreview := callMCPTool(t, srv, "preview_concept_patch", fileArguments)
	if filePreview.IsError {
		t.Fatalf("file preview returned error: %s", resultText(t, filePreview))
	}
	filePlan := filePreview.StructuredContent.(map[string]any)
	if filePlan["base_revision"] != inlineBaseRevision {
		t.Fatalf(
			"identical source revisions differ: inline=%v file=%v",
			inlineBaseRevision,
			filePlan["base_revision"],
		)
	}
	if filePlan["plan_digest"] == inlinePlanDigest {
		t.Fatalf("explicit mode change retained plan digest %q", inlinePlanDigest)
	}

	applyArguments := cloneArguments(arguments)
	applyArguments["expected_revision"] = inlineBaseRevision
	applyArguments["expected_plan_digest"] = inlinePlanDigest
	applied := callMCPTool(t, srv, "apply_concept_patch", applyArguments)
	if applied.IsError {
		t.Fatalf("inline apply returned error: %s", resultText(t, applied))
	}
	applyPayload := applied.StructuredContent.(map[string]any)
	if applyPayload["status"] != "applied" ||
		applyPayload["plan_digest"] != inlinePlanDigest {
		t.Fatalf("inline apply = %#v", applyPayload)
	}
	document := readTestFile(t, root, "query.md")
	for _, want := range []string{"runtime: unknown-runtime", "# Computation", "```text"} {
		if !strings.Contains(document, want) {
			t.Fatalf("patched query.md lacks %q:\n%s", want, document)
		}
	}
}

func TestMCPAttestedComputationInlineEmptyMigrationPreviewApplyTargetNoopAndDigest(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md",
		"---\nokf_version: \"0.1\"\n---\n# Knowledge\n- [Query](query.md)\n",
	)
	writeTestFile(t, root, "query.md",
		"---\ntype: Knowledge\n---\nQuery.\n\n# Computation\n\n```text\n```\n",
	)
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	arguments := map[string]any{
		"bundle_path":       root,
		"from":              "auto",
		"generated_by":      "process:migration",
		"timestamp_policy":  "preserve",
		"citation_mappings": []any{},
		"computations": []any{map[string]any{
			"concept_id": "query",
			"contract": mcpAttestedComputationContract(map[string]any{
				"mode":               "inline",
				"inline_computation": "",
				"inline_language":    "text",
			}),
		}},
	}

	// Act.
	preview := callMCPTool(t, srv, "preview_v02_migration", arguments)

	// Assert.
	if preview.IsError {
		t.Fatalf("migration preview returned error: %s", resultText(t, preview))
	}
	plan := preview.StructuredContent.(map[string]any)
	proof, proofOK := plan["proof"].(map[string]any)
	if plan["status"] != "applicable" ||
		plan["plan_digest"] == "" ||
		!proofOK ||
		proof["request_digest"] == "" {
		t.Fatalf("migration preview = %#v", plan)
	}

	applyArguments := cloneArguments(arguments)
	applyArguments["expected_source"] = plan["source"]
	applyArguments["proof"] = plan["proof"]
	applyArguments["expected_plan_digest"] = plan["plan_digest"]
	applied := callMCPTool(t, srv, "apply_v02_migration", applyArguments)
	if applied.IsError {
		t.Fatalf("migration apply returned error: %s", resultText(t, applied))
	}
	appliedPayload := applied.StructuredContent.(map[string]any)
	if appliedPayload["status"] != "applied" ||
		appliedPayload["plan_digest"] != plan["plan_digest"] {
		t.Fatalf("migration apply = %#v", appliedPayload)
	}

	noopPreview := callMCPTool(t, srv, "preview_v02_migration", arguments)
	if noopPreview.IsError {
		t.Fatalf("target-noop preview returned error: %s", resultText(t, noopPreview))
	}
	noopPlan := noopPreview.StructuredContent.(map[string]any)
	if noopPlan["status"] != "noop" ||
		noopPlan["plan_digest"] != "" ||
		noopPlan["proof"] != nil {
		t.Fatalf("target-noop preview = %#v", noopPlan)
	}
	noopApplyArguments := cloneArguments(arguments)
	noopApplyArguments["expected_source"] = noopPlan["source"]
	noopApplied := callMCPTool(t, srv, "apply_v02_migration", noopApplyArguments)
	if noopApplied.IsError {
		t.Fatalf("target-noop apply returned error: %s", resultText(t, noopApplied))
	}
	if payload := noopApplied.StructuredContent.(map[string]any); payload["status"] != "noop" {
		t.Fatalf("target-noop apply = %#v", payload)
	}
	document := readTestFile(t, root, "query.md")
	for _, want := range []string{"runtime: unknown-runtime", "# Computation", "```text"} {
		if !strings.Contains(document, want) {
			t.Fatalf("migrated query.md lacks %q:\n%s", want, document)
		}
	}
}

func mcpAttestedComputationContract(overrides map[string]any) map[string]any {
	contract := map[string]any{
		"runtime":    "unknown-runtime",
		"parameters": []any{},
		"executor": map[string]any{
			"resource": "urn:test:executor",
			"receipt":  []any{"digest"},
		},
		"attester": map[string]any{"resource": "urn:test:attester"},
	}
	for key, value := range overrides {
		contract[key] = value
	}
	return contract
}

func mcpModeContractFromArguments(
	t *testing.T,
	toolName string,
	arguments map[string]any,
) map[string]any {
	t.Helper()
	switch toolName {
	case "preview_concept_patch", "apply_concept_patch":
		operations := arguments["operations"].([]any)
		return operations[0].(map[string]any)["attested_computation"].(map[string]any)
	case "preview_v02_migration", "apply_v02_migration":
		computations := arguments["computations"].([]any)
		return computations[0].(map[string]any)["contract"].(map[string]any)
	default:
		t.Fatalf("unsupported tool %q", toolName)
		return nil
	}
}
