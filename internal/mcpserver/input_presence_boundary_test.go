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
	"github.com/mark3labs/mcp-go/server"
	"github.com/skosovsky/okf/mutation"
)

type inputPresenceInvalidCase struct {
	name     string
	build    func(string) map[string]any
	wantCode string
}

type inputPresenceSurface struct {
	tool    string
	handler server.ToolHandlerFunc
}

func TestMigrationInputPresenceBoundaryRejectsBeforeIO(t *testing.T) {
	surfaces := []inputPresenceSurface{
		{tool: "preview_v02_migration", handler: handlePreviewV02Migration},
		{tool: "apply_v02_migration", handler: handleApplyV02Migration},
	}

	for _, surface := range surfaces {
		surface := surface
		apply := surface.tool == "apply_v02_migration"
		for _, test := range migrationInputPresenceInvalidCases(apply) {
			test := test
			t.Run(surface.tool+"/"+test.name, func(t *testing.T) {
				assertInputPresenceRejectedBeforeIO(t, surface, test)
			})
		}
	}
}

func TestConceptPatchInputPresenceBoundaryRejectsBeforeIO(t *testing.T) {
	surfaces := []inputPresenceSurface{
		{tool: "preview_concept_patch", handler: handlePreviewConceptPatch},
		{tool: "apply_concept_patch", handler: handleApplyConceptPatch},
	}

	for _, surface := range surfaces {
		surface := surface
		apply := surface.tool == "apply_concept_patch"
		for _, test := range patchInputPresenceInvalidCases(apply) {
			test := test
			t.Run(surface.tool+"/"+test.name, func(t *testing.T) {
				assertInputPresenceRejectedBeforeIO(t, surface, test)
			})
		}
	}
}

func TestMutationInputPresenceExplicitZeroControlsPassBothBoundaries(t *testing.T) {
	root := t.TempDir()
	controls := []struct {
		name string
		tool string
		args func(string) map[string]any
	}{
		{
			name: "migration empty citation mappings",
			tool: "preview_v02_migration",
			args: func(root string) map[string]any {
				return migrationPresenceBase(root, false)
			},
		},
		{
			name: "migration empty computation parameters",
			tool: "preview_v02_migration",
			args: func(root string) map[string]any {
				arguments := migrationPresenceBase(root, false)
				arguments["computations"] = []any{migrationPresenceInlineComputation(
					[]any{},
				)}
				return arguments
			},
		},
		{
			name: "migration explicit false parameter",
			tool: "apply_v02_migration",
			args: func(root string) map[string]any {
				arguments := migrationPresenceBase(root, true)
				arguments["computations"] = []any{migrationPresenceInlineComputation(
					[]any{map[string]any{
						"name": "input", "type": "string", "required": false,
					}},
				)}
				return arguments
			},
		},
		{
			name: "migration empty asset content",
			tool: "apply_v02_migration",
			args: func(root string) map[string]any {
				arguments := migrationPresenceBase(root, true)
				arguments["computations"] = []any{migrationPresenceFileComputation()}
				return arguments
			},
		},
		{
			name: "migration explicit citation selector",
			tool: "preview_v02_migration",
			args: func(root string) map[string]any {
				arguments := migrationPresenceBase(root, false)
				arguments["citation_mappings"] = []any{map[string]any{
					"path": "alpha.md",
					"entries": []any{map[string]any{
						"legacy_number": 1,
						"source_id":     "source",
					}},
				}}
				return arguments
			},
		},
		{
			name: "expected source explicit zero shape",
			tool: "apply_v02_migration",
			args: func(root string) map[string]any {
				return migrationPresenceTransitionApply(root, migrationPresenceLegacySource())
			},
		},
		{
			name: "proof explicit empty arrays",
			tool: "apply_v02_migration",
			args: func(root string) map[string]any {
				return migrationPresenceTransitionApply(root, migrationPresenceLegacySource())
			},
		},
		{
			name: "proof changed file explicit empty from",
			tool: "apply_v02_migration",
			args: func(root string) map[string]any {
				arguments := migrationPresenceTransitionApply(root, migrationPresenceLegacySource())
				proof := arguments["proof"].(map[string]any)
				proof["changed_files"] = []any{map[string]any{
					"kind": "write", "path": "alpha.md", "from": "",
				}}
				return arguments
			},
		},
		{
			name: "target noop omits proof and digest",
			tool: "apply_v02_migration",
			args: func(root string) map[string]any {
				return migrationPresenceBase(root, true)
			},
		},
		{
			name: "patch empty computation parameters",
			tool: "preview_concept_patch",
			args: func(root string) map[string]any {
				return patchPresenceBase(root, false, []any{})
			},
		},
		{
			name: "patch explicit false parameter",
			tool: "apply_concept_patch",
			args: func(root string) map[string]any {
				return patchPresenceBase(root, true, []any{map[string]any{
					"name": "input", "type": "string", "required": false,
				}})
			},
		},
	}

	for _, test := range controls {
		test := test
		t.Run(test.name, func(t *testing.T) {
			arguments := test.args(root)
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{
				Name: test.tool, Arguments: arguments,
			}}

			if result := requireCanonicalToolInput(t.Context(), test.tool, request); result != nil {
				t.Fatalf("direct boundary rejected explicit zero control: %s", resultText(t, result))
			}

			handlerCalls := 0
			wrapped := contractToolHandler(test.tool, func(
				ctx context.Context,
				request mcp.CallToolRequest,
			) (*mcp.CallToolResult, error) {
				handlerCalls++
				if !hasValidatedToolInput(ctx, test.tool) {
					t.Fatal("wrapped handler lacks the exact validated-input marker")
				}
				if hasValidatedToolInput(ctx, test.tool+"-other") {
					t.Fatal("validated-input marker is not bound to the exact tool")
				}
				if result := requireCanonicalToolInput(ctx, test.tool, request); result != nil {
					t.Fatalf("marked handler repeated or failed validation: %s", resultText(t, result))
				}
				if result := requireCanonicalToolInput(
					ctx,
					pairedMutationTool(test.tool),
					request,
				); result == nil {
					t.Fatal("marker for one tool bypassed another tool's input contract")
				}
				return stableToolError("invalid_request", "test handler stop", false), nil
			})
			result, err := wrapped(t.Context(), request)
			if err != nil {
				t.Fatalf("wrapped boundary error = %v", err)
			}
			assertSchemaErrorEnvelope(t, result, "invalid_request")
			if handlerCalls != 1 {
				t.Fatalf("wrapped handler calls = %d, want 1", handlerCalls)
			}
		})
	}
}

func pairedMutationTool(tool string) string {
	switch tool {
	case "preview_v02_migration":
		return "apply_v02_migration"
	case "apply_v02_migration":
		return "preview_v02_migration"
	case "preview_concept_patch":
		return "apply_concept_patch"
	case "apply_concept_patch":
		return "preview_concept_patch"
	default:
		panic("unpaired mutation tool")
	}
}

func assertInputPresenceRejectedBeforeIO(
	t *testing.T,
	surface inputPresenceSurface,
	test inputPresenceInvalidCase,
) {
	t.Helper()
	root := t.TempDir()

	for _, boundary := range []string{"direct", "wrapped"} {
		t.Run(boundary, func(t *testing.T) {
			sourceOpens, sourceReads, storeOpens := 0, 0, 0
			ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
				sourceOpen: func() { sourceOpens++ },
				sourceRead: func() { sourceReads++ },
				storeOpen:  func() { storeOpens++ },
			})
			arguments := test.build(root)
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{
				Name: surface.tool, Arguments: arguments,
			}}
			before := protocolTreeSnapshot(t, root)
			handlerCalls := 0
			handler := surface.handler
			wantCode := "invalid_request"
			if boundary == "wrapped" {
				wantCode = "schema_validation"
				if test.wantCode == "resource_limit" {
					wantCode = "resource_limit"
				}
				handler = contractToolHandler(surface.tool, func(
					ctx context.Context,
					request mcp.CallToolRequest,
				) (*mcp.CallToolResult, error) {
					handlerCalls++
					return surface.handler(ctx, request)
				})
			} else if test.wantCode != "" {
				wantCode = test.wantCode
			}

			result, err := handler(ctx, request)
			if err != nil {
				t.Fatalf("handler error = %v", err)
			}
			assertSchemaErrorEnvelope(t, result, wantCode)
			if boundary == "wrapped" && handlerCalls != 0 {
				t.Fatalf("wrapped handler calls = %d, want 0", handlerCalls)
			}
			if sourceOpens != 0 || sourceReads != 0 || storeOpens != 0 {
				t.Fatalf(
					"I/O counts = source-open:%d source-read:%d store-open:%d, want all zero",
					sourceOpens,
					sourceReads,
					storeOpens,
				)
			}
			if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("bundle changed:\nbefore=%#v\nafter=%#v", before, after)
			}
			if _, err := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("transactional store was opened: %v", err)
			}
		})
	}
}

func migrationInputPresenceInvalidCases(apply bool) []inputPresenceInvalidCase {
	cases := []inputPresenceInvalidCase{
		migrationPresenceCase("citation_mappings missing", apply, func(arguments map[string]any) {
			delete(arguments, "citation_mappings")
		}),
		migrationPresenceCase("citation_mappings null", apply, func(arguments map[string]any) {
			arguments["citation_mappings"] = nil
		}),
		migrationInlineComputationPresenceCase("computation parameters missing", apply, func(contract map[string]any) {
			delete(contract, "parameters")
		}),
		migrationInlineComputationPresenceCase("computation parameters null", apply, func(contract map[string]any) {
			contract["parameters"] = nil
		}),
		migrationParameterPresenceCase("parameter required missing", apply, func(parameter map[string]any) {
			delete(parameter, "required")
		}),
		migrationParameterPresenceCase("parameter required null", apply, func(parameter map[string]any) {
			parameter["required"] = nil
		}),
		migrationFileComputationPresenceCase("asset content missing", apply, func(asset map[string]any) {
			delete(asset, "content")
		}),
		migrationFileComputationPresenceCase("asset content null", apply, func(asset map[string]any) {
			asset["content"] = nil
		}),
		migrationCitationPresenceCase("citation selector missing", apply, map[string]any{
			"source_id": "source",
		}),
		migrationCitationPresenceCase("citation selector null", apply, map[string]any{
			"legacy_number": nil,
			"source_id":     "source",
		}),
		migrationCitationPresenceCase("citation number explicit zero", apply, map[string]any{
			"legacy_number": 0,
			"source_id":     "source",
		}),
		migrationCitationPresenceCase("citation entry explicit empty", apply, map[string]any{
			"legacy_entry": "",
			"source_id":    "source",
		}),
		migrationPresenceCase("schema max length", apply, func(arguments map[string]any) {
			arguments["bundle_path"] = "/" + strings.Repeat("x", 4096)
		}).withCode("resource_limit"),
	}
	if !apply {
		return cases
	}

	for _, field := range []string{
		"declaration_present",
		"declaration_valid",
		"declaration_raw",
		"declared_version",
		"candidates",
		"blockers",
	} {
		field := field
		cases = append(cases, migrationExpectedSourcePresenceCase(
			"expected_source "+field+" missing",
			func(source map[string]any) { delete(source, field) },
		))
		if field != "declared_version" {
			cases = append(cases, migrationExpectedSourcePresenceCase(
				"expected_source "+field+" null",
				func(source map[string]any) { source[field] = nil },
			))
		}
	}
	for _, field := range []string{"start", "end"} {
		field := field
		cases = append(cases,
			migrationExpectedSourcePresenceCase(
				"expected_source candidate location "+field+" missing",
				func(source map[string]any) {
					location := migrationPresenceCandidateLocation(source)
					delete(location, field)
				},
			),
			migrationExpectedSourcePresenceCase(
				"expected_source candidate location "+field+" null",
				func(source map[string]any) {
					migrationPresenceCandidateLocation(source)[field] = nil
				},
			),
		)
	}

	for _, field := range []string{
		"reads",
		"writes",
		"deletes",
		"renames",
		"affected_refs",
		"reverse_impact",
		"changed_files",
		"changed_refs",
	} {
		field := field
		cases = append(cases,
			migrationProofPresenceCase("proof "+field+" missing", func(proof map[string]any) {
				delete(proof, field)
			}),
			migrationProofPresenceCase("proof "+field+" null", func(proof map[string]any) {
				proof[field] = nil
			}),
		)
	}
	cases = append(cases,
		migrationProofPresenceCase("proof changed_files.from missing", func(proof map[string]any) {
			proof["changed_files"] = []any{map[string]any{
				"kind": "write", "path": "alpha.md",
			}}
		}),
		migrationProofPresenceCase("proof changed_files.from null", func(proof map[string]any) {
			proof["changed_files"] = []any{map[string]any{
				"kind": "write", "path": "alpha.md", "from": nil,
			}}
		}),
		inputPresenceInvalidCase{
			name: "target noop explicit null proof",
			build: func(root string) map[string]any {
				arguments := migrationPresenceBase(root, true)
				arguments["proof"] = nil
				return arguments
			},
		},
		inputPresenceInvalidCase{
			name: "target noop explicit empty digest",
			build: func(root string) map[string]any {
				arguments := migrationPresenceBase(root, true)
				arguments["expected_plan_digest"] = ""
				return arguments
			},
		},
	)
	return cases
}

func patchInputPresenceInvalidCases(apply bool) []inputPresenceInvalidCase {
	return []inputPresenceInvalidCase{
		{
			name: "computation parameters missing",
			build: func(root string) map[string]any {
				arguments := patchPresenceBase(root, apply, []any{})
				contract := patchPresenceContract(arguments)
				delete(contract, "parameters")
				return arguments
			},
		},
		{
			name: "computation parameters null",
			build: func(root string) map[string]any {
				arguments := patchPresenceBase(root, apply, []any{})
				patchPresenceContract(arguments)["parameters"] = nil
				return arguments
			},
		},
		{
			name: "parameter required missing",
			build: func(root string) map[string]any {
				arguments := patchPresenceBase(root, apply, []any{map[string]any{
					"name": "input", "type": "string", "required": false,
				}})
				parameter := patchPresenceContract(arguments)["parameters"].([]any)[0].(map[string]any)
				delete(parameter, "required")
				return arguments
			},
		},
		{
			name: "parameter required null",
			build: func(root string) map[string]any {
				arguments := patchPresenceBase(root, apply, []any{map[string]any{
					"name": "input", "type": "string", "required": false,
				}})
				parameter := patchPresenceContract(arguments)["parameters"].([]any)[0].(map[string]any)
				parameter["required"] = nil
				return arguments
			},
		},
	}
}

func migrationPresenceCase(
	name string,
	apply bool,
	mutate func(map[string]any),
) inputPresenceInvalidCase {
	return inputPresenceInvalidCase{
		name: name,
		build: func(root string) map[string]any {
			arguments := migrationPresenceBase(root, apply)
			mutate(arguments)
			return arguments
		},
	}
}

func (test inputPresenceInvalidCase) withCode(code string) inputPresenceInvalidCase {
	test.wantCode = code
	return test
}

func migrationInlineComputationPresenceCase(
	name string,
	apply bool,
	mutate func(map[string]any),
) inputPresenceInvalidCase {
	return inputPresenceInvalidCase{
		name: name,
		build: func(root string) map[string]any {
			arguments := migrationPresenceBase(root, apply)
			computation := migrationPresenceInlineComputation([]any{})
			arguments["computations"] = []any{computation}
			mutate(computation["contract"].(map[string]any))
			return arguments
		},
	}
}

func migrationParameterPresenceCase(
	name string,
	apply bool,
	mutate func(map[string]any),
) inputPresenceInvalidCase {
	return inputPresenceInvalidCase{
		name: name,
		build: func(root string) map[string]any {
			arguments := migrationPresenceBase(root, apply)
			parameter := map[string]any{
				"name": "input", "type": "string", "required": false,
			}
			arguments["computations"] = []any{
				migrationPresenceInlineComputation([]any{parameter}),
			}
			mutate(parameter)
			return arguments
		},
	}
}

func migrationFileComputationPresenceCase(
	name string,
	apply bool,
	mutate func(map[string]any),
) inputPresenceInvalidCase {
	return inputPresenceInvalidCase{
		name: name,
		build: func(root string) map[string]any {
			arguments := migrationPresenceBase(root, apply)
			computation := migrationPresenceFileComputation()
			arguments["computations"] = []any{computation}
			mutate(computation["asset"].(map[string]any))
			return arguments
		},
	}
}

func migrationCitationPresenceCase(
	name string,
	apply bool,
	entry map[string]any,
) inputPresenceInvalidCase {
	return inputPresenceInvalidCase{
		name: name,
		build: func(root string) map[string]any {
			arguments := migrationPresenceBase(root, apply)
			arguments["citation_mappings"] = []any{map[string]any{
				"path": "alpha.md", "entries": []any{entry},
			}}
			return arguments
		},
	}
}

func migrationExpectedSourcePresenceCase(
	name string,
	mutate func(map[string]any),
) inputPresenceInvalidCase {
	return inputPresenceInvalidCase{
		name: name,
		build: func(root string) map[string]any {
			source := migrationPresenceLegacySource()
			arguments := migrationPresenceTransitionApply(root, source)
			mutate(source)
			return arguments
		},
	}
}

func migrationProofPresenceCase(
	name string,
	mutate func(map[string]any),
) inputPresenceInvalidCase {
	return inputPresenceInvalidCase{
		name: name,
		build: func(root string) map[string]any {
			arguments := migrationPresenceTransitionApply(root, migrationPresenceLegacySource())
			mutate(arguments["proof"].(map[string]any))
			return arguments
		},
	}
}

func migrationPresenceBase(root string, apply bool) map[string]any {
	arguments := map[string]any{
		"bundle_path":       root,
		"from":              "auto",
		"generated_by":      "process:migration",
		"timestamp_policy":  "preserve",
		"citation_mappings": []any{},
		"computations":      []any{},
	}
	if apply {
		arguments["expected_source"] = testMigrationSourceValue(
			mutation.MigrationVersionV02,
			string(mutation.MigrationTransitionTargetNoop),
		)
	}
	return arguments
}

func migrationPresenceTransitionApply(
	root string,
	source map[string]any,
) map[string]any {
	arguments := migrationPresenceBase(root, false)
	arguments["expected_source"] = source
	arguments["expected_plan_digest"] = presenceDigest("d")
	arguments["proof"] = migrationPresenceProof()
	return arguments
}

func migrationPresenceInlineComputation(parameters []any) map[string]any {
	return map[string]any{
		"concept_id": "alpha",
		"contract": map[string]any{
			"mode":               "inline",
			"runtime":            "go",
			"parameters":         parameters,
			"inline_computation": "",
			"executor": map[string]any{
				"resource": "urn:executor", "receipt": []any{"urn:receipt"},
			},
			"attester": map[string]any{"resource": "urn:attester"},
		},
	}
}

func migrationPresenceFileComputation() map[string]any {
	return map[string]any{
		"concept_id": "alpha",
		"contract": map[string]any{
			"mode":             "file",
			"runtime":          "go",
			"parameters":       []any{},
			"computation_path": "calc.go",
			"executor": map[string]any{
				"resource": "urn:executor", "receipt": []any{"urn:receipt"},
			},
			"attester": map[string]any{"resource": "urn:attester"},
		},
		"asset": map[string]any{"path": "calc.go", "content": ""},
	}
}

func migrationPresenceLegacySource() map[string]any {
	return map[string]any{
		"requested_selector":  "auto",
		"declaration_present": false,
		"declaration_valid":   true,
		"declaration_raw":     "",
		"declared_version":    nil,
		"resolved_source":     "0.1",
		"resolution_source":   "legacy-probe",
		"from_version":        "0.1",
		"to_version":          "0.2",
		"transition":          "v0.1-to-v0.2",
		"candidates": []any{map[string]any{
			"kind": "timestamp",
			"path": "alpha.md",
			"location": map[string]any{
				"start": 0,
				"end":   0,
			},
		}},
		"blockers": []any{},
	}
}

func migrationPresenceCandidateLocation(source map[string]any) map[string]any {
	candidates := source["candidates"].([]any)
	candidate := candidates[0].(map[string]any)
	return candidate["location"].(map[string]any)
}

func migrationPresenceProof() map[string]any {
	return map[string]any{
		"format_version":    2,
		"request_digest":    presenceDigest("a"),
		"resolution_digest": presenceDigest("b"),
		"base_revision":     presenceDigest("c"),
		"result_revision":   presenceDigest("e"),
		"reads":             []any{},
		"writes":            []any{},
		"deletes":           []any{},
		"renames":           []any{},
		"affected_refs":     []any{},
		"reverse_impact":    []any{},
		"changed_files":     []any{},
		"changed_refs":      []any{},
	}
}

func patchPresenceBase(root string, apply bool, parameters []any) map[string]any {
	arguments := map[string]any{
		"bundle_path": root,
		"actor":       "mcp:presence-boundary",
		"operations": []any{map[string]any{
			"kind":       "put_attested_computation",
			"concept_id": "alpha",
			"attested_computation": map[string]any{
				"mode":               "inline",
				"runtime":            "go",
				"parameters":         parameters,
				"inline_computation": "",
				"executor": map[string]any{
					"resource": "urn:executor", "receipt": []any{"urn:receipt"},
				},
				"attester": map[string]any{"resource": "urn:attester"},
			},
		}},
	}
	if apply {
		arguments["expected_revision"] = presenceDigest("f")
		arguments["expected_plan_digest"] = presenceDigest("9")
	}
	return arguments
}

func patchPresenceContract(arguments map[string]any) map[string]any {
	operations := arguments["operations"].([]any)
	operation := operations[0].(map[string]any)
	return operation["attested_computation"].(map[string]any)
}

func presenceDigest(hex string) string {
	return "sha256:" + strings.Repeat(hex, 64)
}
