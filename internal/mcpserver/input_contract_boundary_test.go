package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/skosovsky/okf/bundle"
)

type inputContractSurface struct {
	name    string
	handler server.ToolHandlerFunc
	base    func(string) map[string]any
}

func TestAllNineToolsRejectCanonicalInputFailuresBeforeIO(t *testing.T) {
	// Arrange.
	rawOverBudget := strings.Repeat("x", maxBundleTotalBytes+1)
	rawItemsOverBudget := make([]any, maxMCPArgumentItems+1)
	failures := []struct {
		name        string
		mutate      func(map[string]any)
		directCode  string
		wrappedCode string
	}{
		{
			name: "missing",
			mutate: func(arguments map[string]any) {
				delete(arguments, "bundle_path")
			},
			directCode: "invalid_request", wrappedCode: "schema_validation",
		},
		{
			name: "null",
			mutate: func(arguments map[string]any) {
				arguments["bundle_path"] = nil
			},
			directCode: "invalid_request", wrappedCode: "schema_validation",
		},
		{
			name: "extra",
			mutate: func(arguments map[string]any) {
				arguments["unexpected"] = true
			},
			directCode: "invalid_request", wrappedCode: "schema_validation",
		},
		{
			name: "wrong type",
			mutate: func(arguments map[string]any) {
				arguments["bundle_path"] = 7
			},
			directCode: "invalid_request", wrappedCode: "schema_validation",
		},
		{
			name: "raw invalid UTF-8",
			mutate: func(arguments map[string]any) {
				arguments["bundle_path"] = string([]byte{0xff})
			},
			directCode: "invalid_request", wrappedCode: "invalid_request",
		},
		{
			name: "schema string cap",
			mutate: func(arguments map[string]any) {
				arguments["bundle_path"] = strings.Repeat("x", 4097)
			},
			directCode: "resource_limit", wrappedCode: "resource_limit",
		},
		{
			name: "raw byte cap",
			mutate: func(arguments map[string]any) {
				arguments["unexpected"] = rawOverBudget
			},
			directCode: "resource_limit", wrappedCode: "resource_limit",
		},
		{
			name: "raw item cap",
			mutate: func(arguments map[string]any) {
				arguments["unexpected"] = rawItemsOverBudget
			},
			directCode: "resource_limit", wrappedCode: "resource_limit",
		},
		{
			name: "raw nesting depth",
			mutate: func(arguments map[string]any) {
				var nested any = "leaf"
				for range maxBundlePathDepth + 8 {
					nested = map[string]any{"next": nested}
				}
				arguments["unexpected"] = nested
			},
			directCode: "resource_limit", wrappedCode: "resource_limit",
		},
	}

	// Act and assert.
	for _, surface := range inputContractSurfaces() {
		surface := surface
		for _, failure := range failures {
			failure := failure
			t.Run(surface.name+"/"+failure.name, func(t *testing.T) {
				root := t.TempDir()
				arguments := surface.base(root)
				failure.mutate(arguments)
				assertInputContractRejectedBeforeIO(
					t,
					surface,
					root,
					arguments,
					failure.directCode,
					failure.wrappedCode,
					0,
				)
			})
		}
	}
}

func inputContractSurfaces() []inputContractSurface {
	return []inputContractSurface{
		{
			name: "list_concepts", handler: handleListConcepts,
			base: func(root string) map[string]any { return map[string]any{"bundle_path": root} },
		},
		{
			name: "read_concept", handler: handleReadConcept,
			base: func(root string) map[string]any {
				return map[string]any{"bundle_path": root, "concept_id": "alpha"}
			},
		},
		{
			name: "validate_bundle", handler: handleValidateBundle,
			base: func(root string) map[string]any { return map[string]any{"bundle_path": root} },
		},
		{
			name: "get_semantic_graph", handler: handleSemanticGraph,
			base: func(root string) map[string]any { return map[string]any{"bundle_path": root} },
		},
		{
			name: "write_concept", handler: handleWriteConcept,
			base: func(root string) map[string]any {
				return map[string]any{
					"bundle_path": root,
					"concept_id":  "alpha",
					"frontmatter": "type: Knowledge\n",
					"body":        "Claim.\n",
				}
			},
		},
		{
			name: "preview_concept_patch", handler: handlePreviewConceptPatch,
			base: func(root string) map[string]any {
				return patchPresenceBase(root, false, []any{})
			},
		},
		{
			name: "apply_concept_patch", handler: handleApplyConceptPatch,
			base: func(root string) map[string]any {
				return patchPresenceBase(root, true, []any{})
			},
		},
		{
			name: "preview_v02_migration", handler: handlePreviewV02Migration,
			base: func(root string) map[string]any {
				return migrationPresenceBase(root, false)
			},
		},
		{
			name: "apply_v02_migration", handler: handleApplyV02Migration,
			base: func(root string) map[string]any {
				return migrationPresenceBase(root, true)
			},
		},
	}
}

func TestAdvertisedStringAndCollectionCapsRejectBeforeIO(t *testing.T) {
	// Arrange, act, and assert.
	for _, surface := range inputContractSurfaces() {
		surface := surface
		capCases := advertisedInputCapCases(surface.name)
		if len(capCases) == 0 {
			t.Fatalf("input surface %q lacks advertised cap cases", surface.name)
		}
		for _, capCase := range capCases {
			capCase := capCase
			t.Run(surface.name+"/"+capCase.name, func(t *testing.T) {
				root := t.TempDir()
				arguments := surface.base(root)
				capCase.mutate(arguments)
				assertInputContractRejectedBeforeIO(
					t,
					surface,
					root,
					arguments,
					"resource_limit",
					"resource_limit",
					0,
				)
			})
		}
	}
}

type advertisedInputCapCase struct {
	name   string
	mutate func(map[string]any)
}

func advertisedInputCapCases(tool string) []advertisedInputCapCase {
	bundlePath := advertisedInputCapCase{
		name: "bundle path string",
		mutate: func(arguments map[string]any) {
			arguments["bundle_path"] = strings.Repeat("x", 4097)
		},
	}
	switch tool {
	case "list_concepts", "validate_bundle", "get_semantic_graph":
		return []advertisedInputCapCase{bundlePath}
	case "read_concept":
		return []advertisedInputCapCase{
			bundlePath,
			{
				name: "concept id string",
				mutate: func(arguments map[string]any) {
					arguments["concept_id"] = strings.Repeat("x", 4097)
				},
			},
		}
	case "write_concept":
		return []advertisedInputCapCase{
			bundlePath,
			{
				name: "concept id string",
				mutate: func(arguments map[string]any) {
					arguments["concept_id"] = strings.Repeat("x", 4097)
				},
			},
			{
				name: "frontmatter string",
				mutate: func(arguments map[string]any) {
					arguments["frontmatter"] = strings.Repeat("x", maxFrontmatterBytes+1)
				},
			},
			{
				name: "body string",
				mutate: func(arguments map[string]any) {
					arguments["body"] = strings.Repeat("x", maxConceptReadBytes+1)
				},
			},
		}
	case "preview_concept_patch", "apply_concept_patch":
		return []advertisedInputCapCase{
			bundlePath,
			{
				name: "actor string",
				mutate: func(arguments map[string]any) {
					arguments["actor"] = strings.Repeat("x", 257)
				},
			},
			{
				name: "operations collection",
				mutate: func(arguments map[string]any) {
					operation := arguments["operations"].([]any)[0]
					arguments["operations"] = repeatedInputItems(operation, 257)
				},
			},
			{
				name: "computation runtime string",
				mutate: func(arguments map[string]any) {
					patchPresenceContract(arguments)["runtime"] = strings.Repeat("x", 4097)
				},
			},
			{
				name: "computation parameters collection",
				mutate: func(arguments map[string]any) {
					parameter := map[string]any{
						"name": "input", "type": "string", "required": false,
					}
					patchPresenceContract(arguments)["parameters"] =
						repeatedInputItems(parameter, 257)
				},
			},
		}
	case "preview_v02_migration", "apply_v02_migration":
		return []advertisedInputCapCase{
			bundlePath,
			{
				name: "generated by string",
				mutate: func(arguments map[string]any) {
					arguments["generated_by"] = strings.Repeat("x", 257)
				},
			},
			{
				name: "generated at collection",
				mutate: func(arguments map[string]any) {
					timestamp := map[string]any{
						"concept_id": "alpha",
						"at":         "2026-01-01T00:00:00Z",
					}
					arguments["generated_at"] = repeatedInputItems(timestamp, 10_001)
				},
			},
			{
				name: "computations collection",
				mutate: func(arguments map[string]any) {
					computation := migrationPresenceInlineComputation([]any{})
					arguments["computations"] = repeatedInputItems(computation, 257)
				},
			},
			{
				name: "computation runtime string",
				mutate: func(arguments map[string]any) {
					computation := migrationPresenceInlineComputation([]any{})
					computation["contract"].(map[string]any)["runtime"] =
						strings.Repeat("x", 4097)
					arguments["computations"] = []any{computation}
				},
			},
			{
				name: "computation parameters collection",
				mutate: func(arguments map[string]any) {
					parameter := map[string]any{
						"name": "input", "type": "string", "required": false,
					}
					arguments["computations"] = []any{
						migrationPresenceInlineComputation(
							repeatedInputItems(parameter, 257),
						),
					}
				},
			},
			{
				name: "asset content string",
				mutate: func(arguments map[string]any) {
					computation := migrationPresenceFileComputation()
					computation["asset"].(map[string]any)["content"] =
						strings.Repeat("x", 8_388_609)
					arguments["computations"] = []any{computation}
				},
			},
		}
	}
	return nil
}

func repeatedInputItems(value any, count int) []any {
	items := make([]any, count)
	for index := range items {
		items[index] = value
	}
	return items
}

func TestLegacyDomainAndWritePayloadFailuresPrecedeIO(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name         string
		surface      inputContractSurface
		arguments    map[string]any
		directCode   string
		wrappedCode  string
		wrappedCalls int
	}{
		{
			name: "read reserved concept id",
			surface: inputContractSurface{
				name: "read_concept", handler: handleReadConcept,
			},
			arguments: map[string]any{
				"bundle_path": root, "concept_id": "group/index",
			},
			directCode: "invalid_request", wrappedCode: "schema_validation",
		},
		{
			name: "write reserved concept id",
			surface: inputContractSurface{
				name: "write_concept", handler: handleWriteConcept,
			},
			arguments: map[string]any{
				"bundle_path": root, "concept_id": "log",
				"frontmatter": "", "body": "",
			},
			directCode: "invalid_request", wrappedCode: "schema_validation",
		},
		{
			name: "write malformed frontmatter",
			surface: inputContractSurface{
				name: "write_concept", handler: handleWriteConcept,
			},
			arguments: map[string]any{
				"bundle_path": root, "concept_id": "alpha",
				"frontmatter": "type: [unterminated\n", "body": "Claim.\n",
			},
			directCode: "invalid_request", wrappedCode: "invalid_request", wrappedCalls: 1,
		},
		{
			name: "write frontmatter byte cap",
			surface: inputContractSurface{
				name: "write_concept", handler: handleWriteConcept,
			},
			arguments: map[string]any{
				"bundle_path": root, "concept_id": "alpha",
				"frontmatter": strings.Repeat("é", maxFrontmatterBytes/2+1), "body": "",
			},
			directCode: "resource_limit", wrappedCode: "resource_limit", wrappedCalls: 1,
		},
		{
			name: "write body byte cap",
			surface: inputContractSurface{
				name: "write_concept", handler: handleWriteConcept,
			},
			arguments: map[string]any{
				"bundle_path": root, "concept_id": "alpha",
				"frontmatter": "", "body": strings.Repeat("é", maxConceptReadBytes/2+1),
			},
			directCode: "resource_limit", wrappedCode: "resource_limit", wrappedCalls: 1,
		},
		{
			name: "write frontmatter YAML depth",
			surface: inputContractSurface{
				name: "write_concept", handler: handleWriteConcept,
			},
			arguments: map[string]any{
				"bundle_path": root, "concept_id": "alpha",
				"frontmatter": "type: Note\n" + nestedYAMLValue(bundle.MaxYAMLPhysicalDepth+1),
				"body":        "",
			},
			directCode: "resource_limit", wrappedCode: "resource_limit", wrappedCalls: 1,
		},
	}

	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			assertInputContractRejectedBeforeIO(
				t,
				test.surface,
				root,
				test.arguments,
				test.directCode,
				test.wrappedCode,
				test.wrappedCalls,
			)
		})
	}
}

func assertInputContractRejectedBeforeIO(
	t *testing.T,
	surface inputContractSurface,
	trustedRoot string,
	arguments map[string]any,
	directCode string,
	wrappedCode string,
	wantWrappedCalls int,
) {
	t.Helper()

	for _, boundary := range []string{"direct", "wrapped"} {
		t.Run(boundary, func(t *testing.T) {
			sourceOpens, sourceReads, storeOpens := 0, 0, 0
			ctx := withLegacyToolIOObserver(t.Context(), legacyToolIOObserver{
				sourceOpen: func() { sourceOpens++ },
				sourceRead: func() { sourceReads++ },
				storeOpen:  func() { storeOpens++ },
			})
			ctx = withMigrationIOObserver(ctx, migrationIOObserver{
				sourceOpen: func() { sourceOpens++ },
				sourceRead: func() { sourceReads++ },
				storeOpen:  func() { storeOpens++ },
			})
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{
				Name: surface.name, Arguments: arguments,
			}}
			handler := surface.handler
			handlerCalls := 0
			wantCode := directCode
			if boundary == "wrapped" {
				wantCode = wrappedCode
				handler = contractToolHandler(surface.name, func(
					ctx context.Context,
					request mcp.CallToolRequest,
				) (*mcp.CallToolResult, error) {
					handlerCalls++
					return surface.handler(ctx, request)
				})
			}

			result, err := handler(ctx, request)
			if err != nil {
				t.Fatalf("handler error = %v", err)
			}
			assertSchemaErrorEnvelope(t, result, wantCode)
			if boundary == "wrapped" && handlerCalls != wantWrappedCalls {
				t.Fatalf("wrapped handler calls = %d, want %d", handlerCalls, wantWrappedCalls)
			}
			if sourceOpens != 0 || sourceReads != 0 || storeOpens != 0 {
				t.Fatalf(
					"I/O counts = source-open:%d source-read:%d store-open:%d, want all zero",
					sourceOpens,
					sourceReads,
					storeOpens,
				)
			}
			if trustedRoot != "" {
				if _, err := os.Lstat(filepath.Join(trustedRoot, ".okf")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("transactional store was opened: %v", err)
				}
			}
		})
	}
}
