package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/skosovsky/okf/mutation"
)

func TestMigrationComputationAssetModeCheckedInSchemaParity(t *testing.T) {
	// Arrange.
	fileComputation := migrationComputationContractItem(
		map[string]any{
			"mode":             "file",
			"computation_path": "assets/query.sql",
		},
		map[string]any{
			"path":    "assets/query.sql",
			"content": "",
		},
	)
	inlineComputation := migrationComputationContractItem(
		map[string]any{
			"mode":               "inline",
			"inline_computation": "",
			"inline_language":    "text",
		},
		nil,
	)
	tests := []struct {
		name        string
		computation map[string]any
		valid       bool
	}{
		{
			name:        "file requires asset",
			computation: migrationComputationWithoutAsset(fileComputation),
		},
		{
			name: "inline forbids asset",
			computation: migrationComputationWithAsset(
				inlineComputation,
				map[string]any{"path": "assets/query.sql", "content": ""},
			),
		},
		{
			name:        "zero-byte file asset is valid",
			computation: fileComputation,
			valid:       true,
		},
		{
			name: "asset path is required",
			computation: migrationComputationContractItem(
				map[string]any{
					"mode":             "file",
					"computation_path": "assets/query.sql",
				},
				map[string]any{"content": ""},
			),
		},
		{
			name: "asset content is required",
			computation: migrationComputationContractItem(
				map[string]any{
					"mode":             "file",
					"computation_path": "assets/query.sql",
				},
				map[string]any{"path": "assets/query.sql"},
			),
		},
		{
			name:        "empty inline computation is valid",
			computation: inlineComputation,
			valid:       true,
		},
		{
			name: "mode is required",
			computation: migrationComputationContractItem(
				map[string]any{"inline_computation": ""},
				nil,
			),
		},
		{
			name: "unknown mode is rejected",
			computation: migrationComputationContractItem(
				map[string]any{"mode": "stream", "inline_computation": ""},
				nil,
			),
		},
		{
			name: "mixed file and inline fields are rejected",
			computation: migrationComputationContractItem(
				map[string]any{
					"mode":               "file",
					"computation_path":   "assets/query.sql",
					"inline_computation": "",
				},
				map[string]any{"path": "assets/query.sql", "content": ""},
			),
		},
	}

	// Act and assert.
	for _, toolName := range []string{"preview_v02_migration", "apply_v02_migration"} {
		for _, test := range tests {
			t.Run(toolName+"/"+test.name, func(t *testing.T) {
				arguments := migrationComputationContractArguments(
					toolName,
					"/schema-only",
					test.computation,
				)
				err := validateContract(toolName+".input", arguments)
				if (err == nil) != test.valid {
					t.Fatalf(
						"validateContract() error = %v, valid=%t, computation=%#v",
						err,
						test.valid,
						test.computation,
					)
				}
			})
		}
	}
}

func TestMigrationComputationAssetModeRejectsBeforeHandlerAndIO(t *testing.T) {
	// Arrange.
	invalidComputations := []struct {
		name        string
		computation map[string]any
	}{
		{
			name: "file missing asset",
			computation: migrationComputationContractItem(
				map[string]any{
					"mode":             "file",
					"computation_path": "assets/query.sql",
				},
				nil,
			),
		},
		{
			name: "inline carrying asset",
			computation: migrationComputationContractItem(
				map[string]any{
					"mode":               "inline",
					"inline_computation": "",
				},
				map[string]any{"path": "assets/query.sql", "content": ""},
			),
		},
		{
			name: "absent mode",
			computation: migrationComputationContractItem(
				map[string]any{"inline_computation": ""},
				nil,
			),
		},
		{
			name: "unknown mode",
			computation: migrationComputationContractItem(
				map[string]any{"mode": "stream", "inline_computation": ""},
				nil,
			),
		},
		{
			name: "mixed mode fields",
			computation: migrationComputationContractItem(
				map[string]any{
					"mode":               "file",
					"computation_path":   "assets/query.sql",
					"inline_computation": "",
				},
				map[string]any{"path": "assets/query.sql", "content": ""},
			),
		},
	}
	handlers := []struct {
		toolName string
		handler  server.ToolHandlerFunc
	}{
		{toolName: "preview_v02_migration", handler: handlePreviewV02Migration},
		{toolName: "apply_v02_migration", handler: handleApplyV02Migration},
	}

	// Act and assert.
	for _, surface := range handlers {
		for _, test := range invalidComputations {
			t.Run(surface.toolName+"/"+test.name, func(t *testing.T) {
				root := t.TempDir()
				arguments := migrationComputationContractArguments(
					surface.toolName,
					root,
					test.computation,
				)
				request := mcp.CallToolRequest{Params: mcp.CallToolParams{
					Name:      surface.toolName,
					Arguments: arguments,
				}}
				sourceOpens, sourceReads, storeOpens := 0, 0, 0
				ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
					sourceOpen: func() { sourceOpens++ },
					sourceRead: func() { sourceReads++ },
					storeOpen:  func() { storeOpens++ },
				})

				handlerCalls := 0
				wrapped := contractToolHandler(
					surface.toolName,
					func(
						ctx context.Context,
						request mcp.CallToolRequest,
					) (*mcp.CallToolResult, error) {
						handlerCalls++
						return surface.handler(ctx, request)
					},
				)
				wrappedResult, err := wrapped(ctx, request)
				if err != nil {
					t.Fatalf("wrapped handler protocol error = %v", err)
				}
				assertSchemaErrorEnvelope(t, wrappedResult, "schema_validation")
				if handlerCalls != 0 {
					t.Fatalf("wrapped handler calls = %d, want 0", handlerCalls)
				}
				assertMigrationComputationModeNoIO(
					t,
					root,
					sourceOpens,
					sourceReads,
					storeOpens,
				)

				directResult, err := surface.handler(ctx, request)
				if err != nil {
					t.Fatalf("direct handler protocol error = %v", err)
				}
				assertSchemaErrorEnvelope(t, directResult, "invalid_request")
				assertMigrationComputationModeNoIO(
					t,
					root,
					sourceOpens,
					sourceReads,
					storeOpens,
				)
			})
		}
	}
}

func migrationComputationContractItem(
	contractOverrides map[string]any,
	asset map[string]any,
) map[string]any {
	item := map[string]any{
		"concept_id": "query",
		"contract":   mcpAttestedComputationContract(contractOverrides),
	}
	if asset != nil {
		item["asset"] = asset
	}
	return item
}

func migrationComputationWithoutAsset(computation map[string]any) map[string]any {
	cloned := cloneArguments(computation)
	delete(cloned, "asset")
	return cloned
}

func migrationComputationWithAsset(
	computation map[string]any,
	asset map[string]any,
) map[string]any {
	cloned := cloneArguments(computation)
	cloned["asset"] = asset
	return cloned
}

func migrationComputationContractArguments(
	toolName string,
	root string,
	computation map[string]any,
) map[string]any {
	arguments := map[string]any{
		"bundle_path":       root,
		"from":              "auto",
		"generated_by":      "process:migration",
		"timestamp_policy":  "preserve",
		"citation_mappings": []any{},
		"computations":      []any{computation},
	}
	if toolName == "apply_v02_migration" {
		arguments["expected_source"] = testMigrationSourceValue(
			mutation.MigrationVersionV02,
			string(mutation.MigrationTransitionTargetNoop),
		)
	}
	return arguments
}

func assertMigrationComputationModeNoIO(
	t *testing.T,
	root string,
	sourceOpens int,
	sourceReads int,
	storeOpens int,
) {
	t.Helper()
	if sourceOpens != 0 || sourceReads != 0 || storeOpens != 0 {
		t.Fatalf(
			"I/O counts = source-open:%d source-read:%d store-open:%d, want all zero",
			sourceOpens,
			sourceReads,
			storeOpens,
		)
	}
	if _, err := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transactional store was opened: %v", err)
	}
}
