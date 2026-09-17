package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/skosovsky/okf/bundle"
)

func TestMCPReadPreviewNoopAndRejectedCallsPreserveEntireBundleTree(t *testing.T) {
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	migrationArguments := func(root string) map[string]any {
		return map[string]any{
			"bundle_path": root, "from": "auto", "generated_by": "process:migration",
			"timestamp_policy": "preserve", "citation_mappings": []any{},
		}
	}
	assertPreserved := func(t *testing.T, root string, call func() *mcp.CallToolResult) *mcp.CallToolResult {
		t.Helper()
		before := protocolTreeSnapshot(t, root)
		result := call()
		after := protocolTreeSnapshot(t, root)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("bundle tree changed:\nbefore=%#v\nafter=%#v", before, after)
		}
		if _, statErr := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("read/noop/rejected call created .okf: %v", statErr)
		}
		return result
	}

	for _, withIndex := range []bool{false, true} {
		name := "rootless"
		if withIndex {
			name = "declared-v02"
		}
		t.Run("migration-target-noop/"+name, func(t *testing.T) {
			root := t.TempDir()
			if withIndex {
				writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n")
			}
			writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
			arguments := migrationArguments(root)
			preview := assertPreserved(t, root, func() *mcp.CallToolResult {
				return callMCPTool(t, srv, "preview_v02_migration", arguments)
			})
			if preview.IsError {
				t.Fatalf("target-noop preview returned error: %s", resultText(t, preview))
			}
			payload := preview.StructuredContent.(map[string]any)
			apply := cloneArguments(arguments)
			apply["expected_source"] = payload["source"]
			applied := assertPreserved(t, root, func() *mcp.CallToolResult {
				return callMCPTool(t, srv, "apply_v02_migration", apply)
			})
			if applied.IsError || applied.StructuredContent.(map[string]any)["status"] != "noop" {
				t.Fatalf("proofless target-noop apply = error %v, payload %#v", applied.IsError, applied.StructuredContent)
			}
		})
	}

	t.Run("undeclared-marker-only-is-native-proofless-target-noop", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA numbered claim [1] remains prose.\n")
		arguments := migrationArguments(root)
		preview := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "preview_v02_migration", arguments)
		})
		if preview.IsError {
			t.Fatalf("marker-only preview returned error: %s", resultText(t, preview))
		}
		payload := preview.StructuredContent.(map[string]any)
		source := payload["source"].(map[string]any)
		if payload["status"] != "noop" ||
			payload["proof"] != nil ||
			payload["plan_digest"] != "" ||
			source["resolved_source"] != "0.2" ||
			source["resolution_source"] != "default" ||
			source["transition"] != "target-noop" {
			t.Fatalf("marker-only preview = %#v", payload)
		}
		apply := cloneArguments(arguments)
		apply["expected_source"] = payload["source"]
		applied := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "apply_v02_migration", apply)
		})
		if applied.IsError || applied.StructuredContent.(map[string]any)["status"] != "noop" {
			t.Fatalf("marker-only apply = error %v, payload %#v", applied.IsError, applied.StructuredContent)
		}
	})

	t.Run("declared-target-active-citations-require-explicit-mapping", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n")
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nClaim [1].\n\n# Citations\n\n[1] https://example.test/source\n")
		arguments := migrationArguments(root)
		preview := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "preview_v02_migration", arguments)
		})
		if preview.IsError {
			t.Fatalf("active Citations preview returned generic error: %s", resultText(t, preview))
		}
		payload := preview.StructuredContent.(map[string]any)
		source := payload["source"].(map[string]any)
		blockers := payload["blockers"].([]any)
		actions := payload["manual_actions"].([]any)
		hasMissingMapping := false
		for _, raw := range blockers {
			blocker := raw.(map[string]any)
			if blocker["code"] == "missing_explicit_citation_mapping" && blocker["path"] == "a.md" {
				hasMissingMapping = true
			}
		}
		if payload["status"] != "blocked" ||
			source["transition"] != "target-noop" ||
			!hasMissingMapping ||
			len(actions) != 1 ||
			actions[0].(map[string]any)["code"] != "provide_citation_mapping" ||
			actions[0].(map[string]any)["path"] != "a.md" {
			t.Fatalf("active Citations preflight projection = %#v", payload)
		}
		apply := cloneArguments(arguments)
		apply["expected_source"] = payload["source"]
		rejected := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "apply_v02_migration", apply)
		})
		if !rejected.IsError ||
			rejected.StructuredContent.(map[string]any)["code"] != "migration_replay_mismatch" {
			t.Fatalf("active Citations apply = %#v", rejected.StructuredContent)
		}
	})

	t.Run("target-noop-number-only-citation-replay-is-document-aware", func(t *testing.T) {
		newBundle := func(t *testing.T, target string) string {
			t.Helper()
			root := t.TempDir()
			writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n- [Log](log.md)\n")
			writeTestFile(t, root, "log.md", "---\ntype: Note\n---\nClaim [^source].\n\n[^source]: "+target+"\n")
			return root
		}
		arguments := func(root string) map[string]any {
			args := migrationArguments(root)
			args["citation_mappings"] = []any{map[string]any{
				"path": "log.md",
				"entries": []any{map[string]any{
					"legacy_number": 1,
					"source_id":     "source",
					"resource":      "https://example.test/source",
				}},
			}}
			return args
		}

		matchingRoot := newBundle(t, "https://example.test/source")
		matchingArguments := arguments(matchingRoot)
		matchingPreview := assertPreserved(t, matchingRoot, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "preview_v02_migration", matchingArguments)
		})
		if matchingPreview.IsError ||
			matchingPreview.StructuredContent.(map[string]any)["status"] != "noop" {
			t.Fatalf("matching number-only replay = %#v", matchingPreview.StructuredContent)
		}
		matchingApply := cloneArguments(matchingArguments)
		matchingApply["expected_source"] = matchingPreview.StructuredContent.(map[string]any)["source"]
		applied := assertPreserved(t, matchingRoot, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "apply_v02_migration", matchingApply)
		})
		if applied.IsError || applied.StructuredContent.(map[string]any)["status"] != "noop" {
			t.Fatalf("matching number-only apply = %#v", applied.StructuredContent)
		}

		mismatchRoot := newBundle(t, "https://invalid.test")
		mismatchPreview := assertPreserved(t, mismatchRoot, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "preview_v02_migration", arguments(mismatchRoot))
		})
		if mismatchPreview.IsError {
			t.Fatalf("mismatched number-only replay returned generic error: %s", resultText(t, mismatchPreview))
		}
		mismatchPayload := mismatchPreview.StructuredContent.(map[string]any)
		blockers := mismatchPayload["blockers"].([]any)
		if mismatchPayload["status"] != "blocked" ||
			len(blockers) != 1 ||
			blockers[0].(map[string]any)["code"] != "migration_replay_mismatch" ||
			blockers[0].(map[string]any)["path"] != "log.md" ||
			blockers[0].(map[string]any)["location"].(map[string]any)["end"].(float64) <=
				blockers[0].(map[string]any)["location"].(map[string]any)["start"].(float64) {
			t.Fatalf("mismatched number-only preflight = %#v", mismatchPayload)
		}
	})

	t.Run("proof-bound-transition-noop-authenticates-before-store-open", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
		arguments := migrationArguments(root)
		arguments["from"] = "0.1"
		preview := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "preview_v02_migration", arguments)
		})
		if preview.IsError {
			t.Fatalf("transition-noop preview returned error: %s", resultText(t, preview))
		}
		payload := preview.StructuredContent.(map[string]any)
		source := payload["source"].(map[string]any)
		if payload["status"] != "noop" ||
			source["transition"] != "v0.1-to-v0.2" ||
			payload["proof"] == nil ||
			payload["plan_digest"] == "" {
			t.Fatalf("transition-noop preview = %#v", payload)
		}
		apply := cloneArguments(arguments)
		apply["expected_source"] = payload["source"]
		apply["proof"] = payload["proof"]
		apply["expected_plan_digest"] = payload["plan_digest"]

		wrongDigest := cloneArguments(apply)
		wrongDigest["expected_plan_digest"] = "sha256:" + strings.Repeat("a", 64)
		rejectedDigest := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "apply_v02_migration", wrongDigest)
		})
		if !rejectedDigest.IsError {
			t.Fatalf("transition-noop accepted wrong digest: %#v", rejectedDigest.StructuredContent)
		}

		tamperedProof := cloneArguments(apply)
		proof := deepCloneMap(t, payload["proof"].(map[string]any))
		proof["request_digest"] = "sha256:" + strings.Repeat("b", 64)
		tamperedProof["proof"] = proof
		rejectedProof := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "apply_v02_migration", tamperedProof)
		})
		if !rejectedProof.IsError {
			t.Fatalf("transition-noop accepted tampered proof: %#v", rejectedProof.StructuredContent)
		}

		applied := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "apply_v02_migration", apply)
		})
		if applied.IsError {
			t.Fatalf("transition-noop apply returned error: %s", resultText(t, applied))
		}
		appliedPayload := applied.StructuredContent.(map[string]any)
		if appliedPayload["status"] != "noop" ||
			appliedPayload["plan_digest"] != payload["plan_digest"] ||
			appliedPayload["base_revision"] != payload["base_revision"] ||
			appliedPayload["result_revision"] != payload["result_revision"] {
			t.Fatalf("transition-noop apply = %#v, preview = %#v", appliedPayload, payload)
		}
	})

	t.Run("rootless-target-noop-validates-complete-migration-input", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
		base := migrationArguments(root)
		validPreview := callMCPTool(t, srv, "preview_v02_migration", base)
		if validPreview.IsError {
			t.Fatalf("baseline target-noop preview returned error: %s", resultText(t, validPreview))
		}
		expectedSource := validPreview.StructuredContent.(map[string]any)["source"]
		validContract := func() map[string]any {
			return map[string]any{
				"mode": "file", "runtime": "sql", "parameters": []any{}, "computation_path": "query.sql",
				"executor": map[string]any{"resource": "runner", "receipt": []any{"digest"}},
				"attester": map[string]any{"resource": "reviewer"},
			}
		}
		tests := []struct {
			name   string
			mutate func(map[string]any)
		}{
			{name: "actor", mutate: func(args map[string]any) { args["generated_by"] = "not-an-actor" }},
			{name: "timestamp", mutate: func(args map[string]any) { args["timestamp_policy"] = "invalid" }},
			{name: "citation", mutate: func(args map[string]any) {
				args["citation_mappings"] = []any{map[string]any{
					"path":    "../escape.md",
					"entries": []any{map[string]any{"legacy_number": 1, "source_id": "one"}},
				}}
			}},
			{name: "generated", mutate: func(args map[string]any) {
				args["generated_at"] = []any{
					map[string]any{"concept_id": "a", "at": "2026-07-29T00:00:00Z"},
					map[string]any{"concept_id": "a", "at": "2026-07-30T00:00:00Z"},
				}
			}},
			{name: "computation", mutate: func(args map[string]any) {
				contract := validContract()
				contract["parameters"] = []any{
					map[string]any{"name": "duplicate", "type": "string", "required": true},
					map[string]any{"name": "duplicate", "type": "integer", "required": false},
				}
				args["computations"] = []any{map[string]any{"concept_id": "a", "contract": contract}}
			}},
			{name: "asset", mutate: func(args map[string]any) {
				args["computations"] = []any{map[string]any{
					"concept_id": "a", "contract": validContract(),
					"asset": map[string]any{"path": "other.sql", "content": "SELECT 1"},
				}}
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				arguments := cloneArguments(base)
				test.mutate(arguments)
				for _, call := range []struct {
					name string
					args map[string]any
				}{
					{name: "preview_v02_migration", args: arguments},
					{name: "apply_v02_migration", args: func() map[string]any {
						apply := cloneArguments(arguments)
						apply["expected_source"] = expectedSource
						return apply
					}()},
				} {
					result := assertPreserved(t, root, func() *mcp.CallToolResult {
						return callMCPTool(t, srv, call.name, call.args)
					})
					if test.name == "asset" && call.name == "preview_v02_migration" {
						if result.IsError {
							t.Fatalf(
								"%s returned generic error for invalid %s input: %s",
								call.name,
								test.name,
								resultText(t, result),
							)
						}
						payload := result.StructuredContent.(map[string]any)
						blockers := payload["blockers"].([]any)
						if payload["status"] != "blocked" ||
							payload["proof"] != nil ||
							payload["plan_digest"] != "" ||
							len(blockers) == 0 ||
							blockers[0].(map[string]any)["code"] !=
								"migration_computation_asset_path_mismatch" {
							t.Fatalf(
								"%s invalid %s projection = %#v",
								call.name,
								test.name,
								payload,
							)
						}
						continue
					}
					if !result.IsError {
						t.Fatalf("%s accepted invalid %s input: %#v", call.name, test.name, result.StructuredContent)
					}
					if test.name == "asset" {
						assertSchemaErrorEnvelope(
							t,
							result,
							"migration_computation_asset_path_mismatch",
						)
					}
				}
			})
		}
	})

	t.Run("target-noop-distinguishes-ambiguous-and-unsupported-citation-input", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n")
		base := migrationArguments(root)
		baseline := callMCPTool(t, srv, "preview_v02_migration", base)
		if baseline.IsError {
			t.Fatalf("baseline target-noop preview returned error: %s", resultText(t, baseline))
		}
		expectedSource := baseline.StructuredContent.(map[string]any)["source"]
		collision := cloneArguments(base)
		collision["citation_mappings"] = []any{map[string]any{
			"path": "a.md",
			"entries": []any{
				map[string]any{
					"legacy_number": 1,
					"source_id":     "Spec",
					"resource":      "https://example.test/spec",
				},
				map[string]any{
					"legacy_number": 2,
					"source_id":     "spec",
					"resource":      "https://example.test/spec",
				},
			},
		}}
		blocked := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "preview_v02_migration", collision)
		})
		if blocked.IsError {
			t.Fatalf("ambiguous input returned generic error: %s", resultText(t, blocked))
		}
		blockedPayload := blocked.StructuredContent.(map[string]any)
		blockers := blockedPayload["blockers"].([]any)
		actions := blockedPayload["manual_actions"].([]any)
		if blockedPayload["status"] != "blocked" ||
			blockedPayload["proof"] != nil ||
			blockedPayload["plan_digest"] != "" ||
			len(blockers) != 1 ||
			blockers[0].(map[string]any)["code"] != "normalized_footnote_label_collision" ||
			len(actions) != 1 ||
			actions[0].(map[string]any)["code"] != "disambiguate_citation_entry" {
			t.Fatalf("ambiguous target-noop projection = %#v", blockedPayload)
		}

		collisionApply := cloneArguments(collision)
		collisionApply["expected_source"] = expectedSource
		rejectedApply := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "apply_v02_migration", collisionApply)
		})
		if !rejectedApply.IsError {
			t.Fatalf("ambiguous target-noop apply returned success: %#v", rejectedApply.StructuredContent)
		}
		envelope := rejectedApply.StructuredContent.(map[string]any)
		if envelope["code"] != "normalized_footnote_label_collision" {
			t.Fatalf("ambiguous apply envelope = %#v", envelope)
		}

		unsupported := cloneArguments(base)
		unsupported["citation_mappings"] = []any{map[string]any{
			"path": "a.md",
			"entries": []any{map[string]any{
				"legacy_number": 1,
				"source_id":     "source",
				"title":         "[bad]",
				"resource":      "https://example.test/source",
			}},
		}}
		rejectedUnsupported := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "preview_v02_migration", unsupported)
		})
		if !rejectedUnsupported.IsError {
			t.Fatalf("unsupported individual entry returned blocked success: %#v", rejectedUnsupported.StructuredContent)
		}
		unsupportedEnvelope := rejectedUnsupported.StructuredContent.(map[string]any)
		if unsupportedEnvelope["code"] != "invalid_request" {
			t.Fatalf("unsupported entry envelope = %#v", unsupportedEnvelope)
		}
	})

	t.Run("read-and-preview-surfaces", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n")
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
		calls := []struct {
			name string
			args map[string]any
		}{
			{name: "list_concepts", args: map[string]any{"bundle_path": root}},
			{name: "read_concept", args: map[string]any{"bundle_path": root, "concept_id": "a"}},
			{name: "validate_bundle", args: map[string]any{"bundle_path": root}},
			{name: "get_semantic_graph", args: map[string]any{"bundle_path": root}},
			{name: "preview_concept_patch", args: map[string]any{
				"bundle_path": root, "actor": "mcp:test",
				"operations": []any{map[string]any{
					"kind": "set_lifecycle", "concept_id": "a",
					"lifecycle": map[string]any{"status": "stable", "stale_after": nil},
				}},
			}},
		}
		for _, call := range calls {
			result := assertPreserved(t, root, func() *mcp.CallToolResult {
				return callMCPTool(t, srv, call.name, call.args)
			})
			if result.IsError {
				t.Fatalf("%s returned error: %s", call.name, resultText(t, result))
			}
		}
	})

	t.Run("blocked-migration-preview", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n# Notes\n- [A](a.md)\n")
		writeTestFile(t, root, "a.md", "---\ntype: Knowledge\n---\nClaim [1].\n\n# Citations\n\n[1] https://example.test/spec\n")
		result := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "preview_v02_migration", migrationArguments(root))
		})
		if result.IsError || result.StructuredContent.(map[string]any)["status"] != "blocked" {
			t.Fatalf("blocked preview = error %v, payload %#v", result.IsError, result.StructuredContent)
		}
	})

	t.Run("invalid-migration-authorizations", func(t *testing.T) {
		newPreview := func(t *testing.T) (string, map[string]any, map[string]any) {
			t.Helper()
			root := t.TempDir()
			writeTestFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n# Knowledge\n- [A](a.md)\n")
			writeTestFile(t, root, "a.md", "---\ntype: Knowledge\n---\nA.\n")
			arguments := migrationArguments(root)
			preview := assertPreserved(t, root, func() *mcp.CallToolResult {
				return callMCPTool(t, srv, "preview_v02_migration", arguments)
			})
			if preview.IsError {
				t.Fatalf("preview returned error: %s", resultText(t, preview))
			}
			return root, arguments, preview.StructuredContent.(map[string]any)
		}
		tests := []struct {
			name     string
			wantCode string
			mutate   func(map[string]any, map[string]any)
		}{
			{name: "wrong-digest", wantCode: "plan_mismatch", mutate: func(apply, _ map[string]any) {
				apply["expected_plan_digest"] = "sha256:" + strings.Repeat("a", 64)
			}},
			{name: "proof-v1", wantCode: "schema_validation", mutate: func(apply, preview map[string]any) {
				proof := deepCloneMap(t, preview["proof"].(map[string]any))
				proof["format_version"] = float64(1)
				apply["proof"] = proof
			}},
			{name: "missing-resolution-digest", wantCode: "schema_validation", mutate: func(apply, preview map[string]any) {
				proof := deepCloneMap(t, preview["proof"].(map[string]any))
				delete(proof, "resolution_digest")
				apply["proof"] = proof
			}},
			{name: "tampered-resolution-digest", wantCode: "plan_mismatch", mutate: func(apply, preview map[string]any) {
				proof := deepCloneMap(t, preview["proof"].(map[string]any))
				proof["resolution_digest"] = "sha256:" + strings.Repeat("c", 64)
				apply["proof"] = proof
			}},
			{name: "tampered-proof", wantCode: "plan_mismatch", mutate: func(apply, preview map[string]any) {
				proof := deepCloneMap(t, preview["proof"].(map[string]any))
				proof["result_revision"] = proof["base_revision"]
				apply["proof"] = proof
			}},
			{name: "invalid-source", wantCode: "schema_validation", mutate: func(apply, preview map[string]any) {
				source := deepCloneMap(t, preview["source"].(map[string]any))
				source["declared_version"] = "0.2"
				apply["expected_source"] = source
			}},
			{name: "declaration-presence-tamper", wantCode: "schema_validation", mutate: func(apply, preview map[string]any) {
				source := deepCloneMap(t, preview["source"].(map[string]any))
				source["declaration_present"] = false
				apply["expected_source"] = source
			}},
			{name: "declaration-validity-tamper", wantCode: "schema_validation", mutate: func(apply, preview map[string]any) {
				source := deepCloneMap(t, preview["source"].(map[string]any))
				source["declaration_valid"] = false
				apply["expected_source"] = source
			}},
			{name: "declaration-raw-tamper", wantCode: "schema_validation", mutate: func(apply, preview map[string]any) {
				source := deepCloneMap(t, preview["source"].(map[string]any))
				source["declaration_raw"] = "0.1 "
				apply["expected_source"] = source
			}},
			{name: "resolution-source-tamper", wantCode: "schema_validation", mutate: func(apply, preview map[string]any) {
				source := deepCloneMap(t, preview["source"].(map[string]any))
				source["resolution_source"] = "explicit"
				apply["expected_source"] = source
			}},
			{name: "candidate-tamper", wantCode: "schema_validation", mutate: func(apply, preview map[string]any) {
				source := deepCloneMap(t, preview["source"].(map[string]any))
				source["candidates"] = []any{map[string]any{
					"kind": "timestamp", "path": "a.md",
					"location": map[string]any{"start": 0, "end": 1},
				}}
				apply["expected_source"] = source
			}},
			{name: "blocker-tamper", wantCode: "schema_validation", mutate: func(apply, preview map[string]any) {
				source := deepCloneMap(t, preview["source"].(map[string]any))
				source["blockers"] = []any{map[string]any{
					"code": "tampered", "path": "a.md",
					"location": map[string]any{"start": 0, "end": 1},
					"message":  "tampered",
				}}
				apply["expected_source"] = source
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				root, base, preview := newPreview(t)
				apply := cloneArguments(base)
				apply["proof"] = preview["proof"]
				apply["expected_plan_digest"] = preview["plan_digest"]
				apply["expected_source"] = preview["source"]
				test.mutate(apply, preview)
				result := assertPreserved(t, root, func() *mcp.CallToolResult {
					return callMCPTool(t, srv, "apply_v02_migration", apply)
				})
				if !result.IsError {
					t.Fatalf("invalid authorization returned success: %#v", result.StructuredContent)
				}
				if got := result.StructuredContent.(map[string]any)["code"]; got != test.wantCode {
					t.Fatalf("error code = %v, want %q: %#v", got, test.wantCode, result.StructuredContent)
				}
			})
		}
	})

	t.Run("wrong-patch-digest", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n")
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
		arguments := map[string]any{
			"bundle_path": root, "actor": "mcp:test",
			"operations": []any{map[string]any{
				"kind": "set_lifecycle", "concept_id": "a",
				"lifecycle": map[string]any{"status": "stable", "stale_after": nil},
			}},
		}
		preview := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "preview_concept_patch", arguments)
		})
		payload := preview.StructuredContent.(map[string]any)
		apply := cloneArguments(arguments)
		apply["expected_revision"] = payload["base_revision"]
		apply["expected_plan_digest"] = "sha256:" + strings.Repeat("a", 64)
		result := assertPreserved(t, root, func() *mcp.CallToolResult {
			return callMCPTool(t, srv, "apply_concept_patch", apply)
		})
		if !result.IsError {
			t.Fatalf("wrong patch digest returned success: %#v", result.StructuredContent)
		}
	})
}

func TestMCPTargetNoopPreflightReplaysResolvedRootlessDefaultInput(t *testing.T) {
	// Arrange.
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	baseArguments := func(root string) map[string]any {
		return map[string]any{
			"bundle_path": root, "from": "auto", "generated_by": "process:migration",
			"timestamp_policy": "preserve", "citation_mappings": []any{},
		}
	}
	callPreserved := func(
		t *testing.T,
		root string,
		name string,
		arguments map[string]any,
	) *mcp.CallToolResult {
		t.Helper()
		before := protocolTreeSnapshot(t, root)
		result := callMCPTool(t, srv, name, arguments)
		after := protocolTreeSnapshot(t, root)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("%s changed bundle tree:\nbefore=%#v\nafter=%#v", name, before, after)
		}
		if _, statErr := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s created transactional metadata: %v", name, statErr)
		}
		return result
	}
	assertProoflessPreview := func(t *testing.T, result *mcp.CallToolResult, wantStatus string) map[string]any {
		t.Helper()
		if result.IsError {
			t.Fatalf("preview returned error: %s", resultText(t, result))
		}
		payload := result.StructuredContent.(map[string]any)
		if payload["status"] != wantStatus || payload["proof"] != nil || payload["plan_digest"] != "" {
			t.Fatalf("preview authorization = %#v, want proofless %s", payload, wantStatus)
		}
		return payload
	}
	assertProoflessApply := func(t *testing.T, result *mcp.CallToolResult) map[string]any {
		t.Helper()
		if result.IsError {
			t.Fatalf("apply returned error: %s", resultText(t, result))
		}
		payload := result.StructuredContent.(map[string]any)
		if payload["status"] != "noop" || payload["plan_digest"] != "" {
			t.Fatalf("apply authorization = %#v, want proofless noop", payload)
		}
		return payload
	}
	assertBlockedReplay := func(
		t *testing.T,
		root string,
		arguments map[string]any,
		expectedSource any,
		wantCode string,
		wantPath string,
	) {
		t.Helper()
		preview := callPreserved(t, root, "preview_v02_migration", arguments)
		payload := assertProoflessPreview(t, preview, "blocked")
		blockers := payload["blockers"].([]any)
		if len(blockers) == 0 ||
			blockers[0].(map[string]any)["code"] != wantCode ||
			blockers[0].(map[string]any)["path"] != wantPath {
			t.Fatalf("blocked preview = %#v, want %s for %s", payload, wantCode, wantPath)
		}
		apply := cloneArguments(arguments)
		apply["expected_source"] = expectedSource
		rejected := callPreserved(t, root, "apply_v02_migration", apply)
		if !rejected.IsError {
			t.Fatalf("apply accepted blocked replay: %#v", rejected.StructuredContent)
		}
		envelope := rejected.StructuredContent.(map[string]any)
		if envelope["code"] != wantCode {
			t.Fatalf("apply error = %#v, want %s", envelope, wantCode)
		}
	}

	t.Run("citation-number-and-exact-entry-repeated-replay", func(t *testing.T) {
		tests := []struct {
			name         string
			legacyNumber uint64
			legacyEntry  string
		}{
			{name: "number-only", legacyNumber: 1},
			{name: "exact-entry", legacyEntry: "[1] https://example.test/spec"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				// Arrange.
				root := t.TempDir()
				writeTestFile(t, root, "a.md",
					"---\ntype: Knowledge\nsources:\n  - id: spec\n    resource: https://example.test/spec\n---\n\n"+
						"Claim [^spec].\n\n[^spec]: https://example.test/spec\n",
				)
				arguments := baseArguments(root)
				entry := map[string]any{
					"source_id": "spec",
					"resource":  "https://example.test/spec",
				}
				if test.legacyNumber != 0 {
					entry["legacy_number"] = test.legacyNumber
				} else {
					entry["legacy_entry"] = test.legacyEntry
				}
				arguments["citation_mappings"] = []any{map[string]any{
					"path": "a.md", "entries": []any{entry},
				}}

				// Act.
				firstPreview := callPreserved(t, root, "preview_v02_migration", arguments)
				secondPreview := callPreserved(t, root, "preview_v02_migration", arguments)
				firstPayload := assertProoflessPreview(t, firstPreview, "noop")
				secondPayload := assertProoflessPreview(t, secondPreview, "noop")
				apply := cloneArguments(arguments)
				apply["expected_source"] = firstPayload["source"]
				firstApply := callPreserved(t, root, "apply_v02_migration", apply)
				secondApply := callPreserved(t, root, "apply_v02_migration", apply)

				// Assert.
				source := firstPayload["source"].(map[string]any)
				if source["resolution_source"] != "default" ||
					source["transition"] != "target-noop" ||
					!reflect.DeepEqual(secondPayload, firstPayload) ||
					!reflect.DeepEqual(
						assertProoflessApply(t, secondApply),
						assertProoflessApply(t, firstApply),
					) {
					t.Fatalf("rootless replay drifted: preview=%#v/%#v apply=%#v/%#v",
						firstPayload, secondPayload, firstApply.StructuredContent, secondApply.StructuredContent)
				}
			})
		}
	})

	t.Run("citation-claim-replay-parser-ownership-matrix", func(t *testing.T) {
		const resource = "https://example.test/spec"
		mapping := func(selector map[string]any, sourceID string) map[string]any {
			entry := map[string]any{
				"source_id": sourceID,
				"resource":  resource,
			}
			for key, value := range selector {
				entry[key] = value
			}
			return entry
		}
		tests := []struct {
			name       string
			body       string
			entries    []any
			wantStatus string
			wantSpan   string
			wantEOF    bool
		}{
			{
				name: "leftover-parser-owned-number",
				body: "Claim [1] and [^spec].\n\n[^spec]: " + resource + "\n",
				entries: []any{
					mapping(map[string]any{"legacy_number": 1}, "spec"),
				},
				wantStatus: "blocked",
				wantSpan:   "[1]",
			},
			{
				name: "missing-keyed-definition",
				body: "Claim [^spec].\n",
				entries: []any{
					mapping(map[string]any{"legacy_number": 1}, "spec"),
				},
				wantStatus: "blocked",
				wantEOF:    true,
			},
			{
				name: "wrong-keyed-attribution",
				body: "Claim [^other].\n\n[^other]: other\n[^spec]: " + resource + "\n",
				entries: []any{
					mapping(map[string]any{"legacy_number": 1}, "spec"),
				},
				wantStatus: "blocked",
				wantSpan:   resource,
			},
			{
				name: "normalized-shared-source-id",
				body: "Shared claim [^spec].\n\n[^spec]: " + resource + "\n",
				entries: []any{
					mapping(map[string]any{"legacy_number": 1}, "spec"),
					mapping(map[string]any{"legacy_number": 2}, "spec"),
				},
				wantStatus: "noop",
			},
			{
				name: "entry-only-mapping-without-reference",
				body: "Narrative without a claim reference.\n\n[^spec]: " + resource + "\n",
				entries: []any{
					mapping(map[string]any{"legacy_entry": resource}, "spec"),
				},
				wantStatus: "noop",
			},
			{
				name: "code-and-raw-html-are-opaque",
				body: "`[1]`\n\n```text\n[1]\n```\n\n<div>\n[1]\n</div>\n\n" +
					"Claim [^spec].\n\n[^spec]: " + resource + "\n",
				entries: []any{
					mapping(map[string]any{"legacy_number": 1}, "spec"),
				},
				wantStatus: "noop",
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				// Arrange.
				root := t.TempDir()
				writeTestFile(t, root, "log.md", test.body)
				arguments := baseArguments(root)
				arguments["citation_mappings"] = []any{map[string]any{
					"path": "log.md", "entries": test.entries,
				}}

				// Act.
				preview := callPreserved(t, root, "preview_v02_migration", arguments)
				payload := assertProoflessPreview(t, preview, test.wantStatus)
				apply := cloneArguments(arguments)
				apply["expected_source"] = payload["source"]
				applied := callPreserved(t, root, "apply_v02_migration", apply)

				// Assert.
				if test.wantStatus == "noop" {
					assertProoflessApply(t, applied)
					return
				}
				blockers := payload["blockers"].([]any)
				if len(blockers) != 1 ||
					blockers[0].(map[string]any)["code"] != "migration_replay_mismatch" ||
					blockers[0].(map[string]any)["path"] != "log.md" {
					t.Fatalf("blocked replay = %#v", payload)
				}
				location := blockers[0].(map[string]any)["location"].(map[string]any)
				wantStart := len(test.body)
				wantEnd := wantStart
				if !test.wantEOF {
					wantStart = bytes.Index([]byte(test.body), []byte(test.wantSpan))
					wantEnd = wantStart + len(test.wantSpan)
				}
				if location["start"] != float64(wantStart) || location["end"] != float64(wantEnd) {
					t.Fatalf("ContentSpan = %#v, want [%d,%d)", location, wantStart, wantEnd)
				}
				if !applied.IsError ||
					applied.StructuredContent.(map[string]any)["code"] != "migration_replay_mismatch" {
					t.Fatalf("blocked apply = %#v", applied.StructuredContent)
				}
			})
		}
	})

	t.Run("generated-actor-and-equivalent-instant", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		writeTestFile(t, root, "a.md",
			"---\ntype: Knowledge\ngenerated:\n  by: process:migration\n  at: 2026-06-25T16:00:00+07:00\n---\n\nA.\n",
		)
		arguments := baseArguments(root)
		arguments["generated_at"] = []any{map[string]any{
			"concept_id": "a", "at": "2026-06-25T09:00:00.000Z",
		}}
		baseline := callPreserved(t, root, "preview_v02_migration", arguments)
		baselinePayload := assertProoflessPreview(t, baseline, "noop")
		apply := cloneArguments(arguments)
		apply["expected_source"] = baselinePayload["source"]

		// Act.
		applied := callPreserved(t, root, "apply_v02_migration", apply)

		// Assert.
		assertProoflessApply(t, applied)

		actorTamper := cloneArguments(arguments)
		actorTamper["generated_by"] = "process:tampered"
		actorPreview := callPreserved(t, root, "preview_v02_migration", actorTamper)
		actorPayload := assertProoflessPreview(t, actorPreview, "noop")
		actorApply := cloneArguments(actorTamper)
		actorApply["expected_source"] = actorPayload["source"]
		assertProoflessApply(t, callPreserved(t, root, "apply_v02_migration", actorApply))
		timeTamper := cloneArguments(arguments)
		timeTamper["generated_at"] = []any{map[string]any{
			"concept_id": "a", "at": "2026-06-26T09:00:00Z",
		}}
		assertBlockedReplay(
			t,
			root,
			timeTamper,
			baselinePayload["source"],
			"migration_replay_mismatch",
			"a.md",
		)
	})

	t.Run("computation-contract-and-asset-replay", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		writeTestFile(t, root, "query.md",
			"---\ntype: Attested Computation\nruntime: sql\nparameters: []\n"+
				"computation: references/query.sql\nexecutor:\n  resource: runner\n  receipt: [digest]\n"+
				"attester:\n  resource: reviewer\n---\n\nQuery.\n",
		)
		writeTestFile(t, root, "references/query.sql", "SELECT 1;\n")
		contract := map[string]any{
			"mode": "file", "runtime": "sql", "parameters": []any{}, "computation_path": "references/query.sql",
			"executor": map[string]any{"resource": "runner", "receipt": []any{"digest"}},
			"attester": map[string]any{"resource": "reviewer"},
		}
		arguments := baseArguments(root)
		arguments["computations"] = []any{map[string]any{
			"concept_id": "query",
			"contract":   contract,
			"asset":      map[string]any{"path": "references/query.sql", "content": "SELECT 1;\n"},
		}}
		baseline := callPreserved(t, root, "preview_v02_migration", arguments)
		baselinePayload := assertProoflessPreview(t, baseline, "noop")
		apply := cloneArguments(arguments)
		apply["expected_source"] = baselinePayload["source"]

		// Act.
		applied := callPreserved(t, root, "apply_v02_migration", apply)

		// Assert.
		assertProoflessApply(t, applied)

		contractTamper := cloneArguments(arguments)
		tamperedContract := deepCloneMap(t, contract)
		tamperedContract["runtime"] = "postgres"
		contractTamper["computations"] = []any{map[string]any{
			"concept_id": "query",
			"contract":   tamperedContract,
			"asset":      map[string]any{"path": "references/query.sql", "content": "SELECT 1;\n"},
		}}
		assertBlockedReplay(
			t,
			root,
			contractTamper,
			baselinePayload["source"],
			"migration_replay_mismatch",
			"query.md",
		)

		assetTamper := cloneArguments(arguments)
		assetTamper["computations"] = []any{map[string]any{
			"concept_id": "query",
			"contract":   contract,
			"asset":      map[string]any{"path": "references/query.sql", "content": "SELECT tampered;\n"},
		}}
		assertBlockedReplay(
			t,
			root,
			assetTamper,
			baselinePayload["source"],
			"migration_replay_mismatch",
			"references/query.sql",
		)
	})

	t.Run("missing-replay-document", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		writeTestFile(t, root, "a.md", "---\ntype: Knowledge\n---\n\nA.\n")
		baselineArguments := baseArguments(root)
		baseline := callPreserved(t, root, "preview_v02_migration", baselineArguments)
		baselinePayload := assertProoflessPreview(t, baseline, "noop")
		missing := cloneArguments(baselineArguments)
		missing["citation_mappings"] = []any{map[string]any{
			"path": "missing.md",
			"entries": []any{map[string]any{
				"legacy_number": 1, "source_id": "spec",
			}},
		}}

		// Act and assert.
		assertBlockedReplay(
			t,
			root,
			missing,
			baselinePayload["source"],
			"migration_document_missing",
			"missing.md",
		)
	})

	t.Run("source-free-validation-precedes-source-access", func(t *testing.T) {
		// Arrange.
		root := filepath.Join(t.TempDir(), "absent")
		arguments := baseArguments(root)
		arguments["generated_at"] = []any{
			map[string]any{"concept_id": "a", "at": "2026-06-25T09:00:00Z"},
			map[string]any{"concept_id": "a", "at": "2026-06-26T09:00:00Z"},
		}
		apply := cloneArguments(arguments)
		apply["expected_source"] = map[string]any{
			"requested_selector": "auto",
			"declared_version":   nil,
			"resolved_source":    "0.2",
			"resolution_source":  "default",
			"from_version":       "0.2",
			"to_version":         "0.2",
			"transition":         "target-noop",
		}

		// Act.
		preview := callMCPTool(t, srv, "preview_v02_migration", arguments)
		applied := callMCPTool(t, srv, "apply_v02_migration", apply)

		// Assert.
		for name, result := range map[string]*mcp.CallToolResult{
			"preview_v02_migration": preview,
			"apply_v02_migration":   applied,
		} {
			if !result.IsError {
				t.Fatalf("%s accepted duplicate generated_at input: %#v", name, result.StructuredContent)
			}
		}
		if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("source-free validation accessed or created bundle root: %v", statErr)
		}
	})
}

func protocolTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		switch {
		case entry.IsDir():
			snapshot["dir:"+relative] = ""
		case entry.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot["symlink:"+relative] = target
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot["file:"+relative] = string(data)
		}
		return nil
	}); err != nil {
		t.Fatalf("snapshot bundle tree: %v", err)
	}
	return snapshot
}

func deepCloneMap(t *testing.T, input map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

func TestMCPServerListsExpectedToolsAndSchemas(t *testing.T) {
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
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
		if tool.OutputSchema.Type != "object" {
			t.Fatalf("tool %s output schema type = %q, want object", tool.Name, tool.OutputSchema.Type)
		}
		if tool.OutputSchema.AdditionalProperties != false {
			t.Fatalf("tool %s output allows additionalProperties: %#v", tool.Name, tool.OutputSchema.AdditionalProperties)
		}
	}

	assertToolSchema(t, got, "list_concepts", []string{"bundle_path"}, true)
	assertToolProperties(t, got["list_concepts"], []string{"as_of", "bundle_path"})
	assertToolSchema(t, got, "read_concept", []string{"bundle_path", "concept_id"}, true)
	assertToolProperties(t, got["read_concept"], []string{"bundle_path", "concept_id"})
	assertToolSchema(t, got, "validate_bundle", []string{"bundle_path"}, true)
	assertToolProperties(t, got["validate_bundle"], []string{"as_of", "bundle_path", "check_links", "check_orphans", "strict", "target_version"})
	assertBooleanDefault(t, got["validate_bundle"], "strict", false)
	assertBooleanDefault(t, got["validate_bundle"], "check_links", false)
	assertBooleanDefault(t, got["validate_bundle"], "check_orphans", false)
	assertToolSchema(t, got, "get_semantic_graph", []string{"bundle_path"}, true)
	assertToolProperties(t, got["get_semantic_graph"], []string{"as_of", "bundle_path"})
	assertToolSchema(t, got, "write_concept", []string{"bundle_path", "concept_id", "frontmatter", "body"}, false)
	assertToolProperties(t, got["write_concept"], []string{"body", "bundle_path", "concept_id", "frontmatter"})
	assertToolSchema(t, got, "preview_concept_patch", []string{"bundle_path", "actor", "operations"}, true)
	assertToolProperties(t, got["preview_concept_patch"], []string{"actor", "bundle_path", "operations"})
	assertToolSchema(t, got, "apply_concept_patch", []string{"bundle_path", "actor", "operations", "expected_revision", "expected_plan_digest"}, false)
	assertToolProperties(t, got["apply_concept_patch"], []string{"actor", "bundle_path", "expected_plan_digest", "expected_revision", "operations"})
	assertToolSchema(t, got, "preview_v02_migration", []string{"bundle_path", "timestamp_policy", "citation_mappings"}, true)
	assertToolProperties(t, got["preview_v02_migration"], []string{"bundle_path", "citation_mappings", "computations", "from", "generated_at", "generated_by", "timestamp_conflict_policy", "timestamp_policy"})
	assertToolSchema(t, got, "apply_v02_migration", []string{"bundle_path", "timestamp_policy", "citation_mappings", "expected_source"}, false)
	assertToolProperties(t, got["apply_v02_migration"], []string{"bundle_path", "citation_mappings", "computations", "expected_plan_digest", "expected_source", "from", "generated_at", "generated_by", "proof", "timestamp_conflict_policy", "timestamp_policy"})

	for _, name := range []string{"preview_concept_patch", "apply_concept_patch"} {
		usage := schemaNode(t, got[name].InputSchema, "$defs", "source", "properties", "usage_count")
		if usage["type"] != "string" || usage["pattern"] != "^(0|[1-9][0-9]*)$" {
			t.Fatalf("%s usage_count schema = %#v, want canonical decimal string", name, usage)
		}
	}
	readUsage := schemaNode(
		t, got["read_concept"].OutputSchema,
		"properties", "projection", "properties", "sources", "items", "properties", "usage_count",
	)
	if readUsage["pattern"] != "^(0|[1-9][0-9]*)$" {
		t.Fatalf("read usage_count schema = %#v, want canonical decimal string/null", readUsage)
	}
	for _, name := range []string{"list_concepts", "read_concept", "validate_bundle"} {
		for _, path := range [][]string{
			{"properties", "version", "properties", "declared"},
			{"properties", "version", "properties", "effective"},
			{"properties", "version_declaration", "properties", "raw"},
			{"properties", "version_declaration", "properties", "value"},
		} {
			if node := schemaNode(t, got[name].OutputSchema, path...); node["maxLength"] != nil {
				t.Fatalf("%s version schema retained artificial maxLength at %v: %#v", name, path, node)
			}
		}
	}
	for _, field := range []string{"declaredOKFVersion", "effectiveOKFVersion"} {
		node := schemaNode(
			t, got["get_semantic_graph"].OutputSchema,
			"properties", "@graph", "items", "properties", field,
		)
		if node["maxLength"] != nil {
			t.Fatalf("semantic graph %s retained artificial maxLength: %#v", field, node)
		}
	}
	for _, name := range []string{"preview_v02_migration", "apply_v02_migration"} {
		for _, field := range []string{"declared_version", "resolved_source", "from_version", "to_version"} {
			node := schemaNode(
				t,
				got[name].OutputSchema,
				"$defs",
				"source_resolution",
				"properties",
				field,
			)
			if node["maxLength"] != nil {
				t.Fatalf("%s source.%s retained artificial maxLength: %#v", name, field, node)
			}
		}
	}
	for _, field := range []string{"declared_version", "resolved_source", "from_version", "to_version"} {
		node := schemaNode(
			t, got["apply_v02_migration"].InputSchema,
			"$defs", "source_resolution", "properties", field,
		)
		if node["maxLength"] != nil {
			t.Fatalf("apply expected_source.%s retained artificial maxLength: %#v", field, node)
		}
	}
}

func TestWriteConceptReplayPreservesDurableDiagnosticsAfterLaterCommit(t *testing.T) {
	root := t.TempDir()
	id, err := bundle.ParseConceptID("replayed")
	if err != nil {
		t.Fatal(err)
	}
	first, err := writeConcept(t.Context(), root, id, "type: Note\n", "Replay body.\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Diagnostics) == 0 {
		t.Fatal("write must exercise non-empty warning/info diagnostics")
	}
	other, err := bundle.ParseConceptID("later")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeConcept(t.Context(), root, other, "type: Note\n", "Later body.\n"); err != nil {
		t.Fatal(err)
	}
	replay, err := writeConcept(t.Context(), root, id, "type: Note\n", "Replay body.\n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replay, first) {
		t.Fatalf("replay response = %#v, want durable original %#v", replay, first)
	}
}

func TestMCPServerCallToolRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\ntitle: A\n---\nA.\n")

	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
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

	// An identical protocol retry must replay the existing store receipt rather
	// than publish a second write.
	replayResult := callMCPTool(t, srv, "write_concept", map[string]any{
		"bundle_path": root,
		"concept_id":  "b",
		"frontmatter": "type: Note\ntitle: B\n",
		"body":        "B.\n",
	})
	if replayResult.IsError {
		t.Fatalf("write_concept replay returned error: %s", resultText(t, replayResult))
	}
	receipts, err := os.ReadDir(filepath.Join(root, ".okf", "receipts"))
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipt files = %d, err=%v; want one publication", len(receipts), err)
	}
}

func TestMCPPatchUsageCountUsesLosslessCanonicalDecimalStrings(t *testing.T) {
	newBundle := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n\n- [A](a.md)\n")
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
		return root
	}
	operation := func(value any) map[string]any {
		return map[string]any{
			"kind": "put_source", "concept_id": "a",
			"source": map[string]any{
				"id": "source", "resource": "urn:test:source", "usage_count": value,
			},
		}
	}
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	for _, value := range []string{"0", "18446744073709551615"} {
		t.Run("accepted/"+value, func(t *testing.T) {
			root := newBundle(t)
			arguments := map[string]any{
				"bundle_path": root, "actor": "mcp-usage-count",
				"operations": []any{operation(value)},
			}
			preview := callMCPTool(t, srv, "preview_concept_patch", arguments)
			if preview.IsError {
				t.Fatalf("preview returned error: %s", resultText(t, preview))
			}
			raw, err := json.Marshal(preview.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var projected patchPreviewResponse
			if err := json.Unmarshal(raw, &projected); err != nil {
				t.Fatal(err)
			}
			applyArguments := cloneArguments(arguments)
			applyArguments["expected_revision"] = projected.BaseRevision
			applyArguments["expected_plan_digest"] = projected.PlanDigest
			applied := callMCPTool(t, srv, "apply_concept_patch", applyArguments)
			if applied.IsError {
				t.Fatalf("apply returned error: %s", resultText(t, applied))
			}
			if got := readTestFile(t, root, "a.md"); !strings.Contains(got, "usage_count: "+value+"\n") {
				t.Fatalf("published usage_count lost precision:\n%s", got)
			}
			read := callMCPTool(t, srv, "read_concept", map[string]any{
				"bundle_path": root, "concept_id": "a",
			})
			if read.IsError {
				t.Fatalf("read returned error: %s", resultText(t, read))
			}
			structured := read.StructuredContent.(map[string]any)
			projection := structured["projection"].(map[string]any)
			source := projection["sources"].([]any)[0].(map[string]any)
			if got := source["usage_count"]; got != value {
				t.Fatalf("structured usage_count = %#v, want canonical decimal string %q", got, value)
			}
		})
	}

	tests := []struct {
		name string
		wire any
		code string
	}{
		{name: "overflow", wire: "18446744073709551616", code: "invalid_request"},
		{name: "leading zero", wire: "01", code: "schema_validation"},
		{name: "fraction string", wire: "1.5", code: "schema_validation"},
		{name: "scientific string", wire: "1e3", code: "schema_validation"},
		{name: "numeric token", wire: uint64(7), code: "schema_validation"},
		{name: "fraction token", wire: 1.5, code: "schema_validation"},
		{name: "scientific token", wire: json.Number("1e3"), code: "schema_validation"},
	}
	for _, test := range tests {
		t.Run("rejected/"+test.name, func(t *testing.T) {
			result := callMCPTool(t, srv, "preview_concept_patch", map[string]any{
				"bundle_path": newBundle(t), "actor": "mcp-usage-count",
				"operations": []any{operation(test.wire)},
			})
			assertSchemaErrorEnvelope(t, result, test.code)
		})
	}
}

func TestMCPReadConceptAcceptsUncappedDomainScalars(t *testing.T) {
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	root := t.TempDir()
	actor := "human:" + strings.Repeat("a", 251)
	writeTestFile(t, root, "a.md", "---\n"+
		"type: "+strings.Repeat("T", 1025)+"\n"+
		"title: "+strings.Repeat("t", 8193)+"\n"+
		"description: "+strings.Repeat("d", 65537)+"\n"+
		"resource: "+strings.Repeat("r", 8193)+"\n"+
		"generated:\n  by: "+actor+"\n  at: 2026-07-29T00:00:00Z\n"+
		"---\nBody.\n")

	result := callMCPTool(t, srv, "read_concept", map[string]any{
		"bundle_path": root, "concept_id": "a",
	})
	if result.IsError {
		t.Fatalf("output-validated read rejected domain scalars: %s", resultText(t, result))
	}
	projection := result.StructuredContent.(map[string]any)["projection"].(map[string]any)
	if len(projection["type"].(string)) != 1025 ||
		projection["generated"].(map[string]any)["by"] != actor {
		t.Fatalf("large scalar projection drifted")
	}
}

func TestMCPSetUsageWindowSupportsClosedAnonymousExactSelector(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n")
	writeTestFile(t, root, "a.md", "---\ntype: Note\nsources:\n  - resource: urn:test:policy\n    usage_count: 18446744073709551615\n    usage_window: {from: 2026-01-01, to: 2026-01-31}\n---\nA.\n")
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	window := map[string]any{"from": "2026-02-01", "to": "2026-02-28"}
	source := map[string]any{
		"resource": "urn:test:policy", "usage_count": "18446744073709551615",
		"usage_window": map[string]any{"from": "2026-01-01", "to": "2026-01-31"},
	}
	arguments := map[string]any{
		"bundle_path": root, "actor": "mcp:usage-window",
		"operations": []any{map[string]any{
			"kind": "set_usage_window", "concept_id": "a", "source": source, "usage_window": window,
		}},
	}

	preview := callMCPTool(t, srv, "preview_concept_patch", arguments)
	if preview.IsError {
		t.Fatalf("exact-selector preview returned error: %s", resultText(t, preview))
	}
	projected := preview.StructuredContent.(map[string]any)
	if projected["status"] != "applicable" {
		t.Fatalf("exact-selector preview = %#v", projected)
	}
	apply := cloneArguments(arguments)
	apply["expected_revision"] = projected["base_revision"]
	apply["expected_plan_digest"] = projected["plan_digest"]
	applied := callMCPTool(t, srv, "apply_concept_patch", apply)
	replayed := callMCPTool(t, srv, "apply_concept_patch", apply)
	if applied.IsError || replayed.IsError {
		t.Fatalf("exact-selector apply/replay errors: apply=%s replay=%s", resultText(t, applied), resultText(t, replayed))
	}
	document := readTestFile(t, root, "a.md")
	if !strings.Contains(document, "usage_count: 18446744073709551615") ||
		!strings.Contains(document, "from: 2026-02-01") ||
		!strings.Contains(document, "to: 2026-02-28") {
		t.Fatalf("exact-selector publication lost source state:\n%s", document)
	}

	postSource := deepCloneMap(t, source)
	postSource["usage_window"] = window
	postArguments := cloneArguments(arguments)
	postArguments["operations"] = []any{map[string]any{
		"kind": "set_usage_window", "concept_id": "a", "source": postSource, "usage_window": window,
	}}
	postPreview := callMCPTool(t, srv, "preview_concept_patch", postArguments)
	if postPreview.IsError || postPreview.StructuredContent.(map[string]any)["status"] != "noop" {
		t.Fatalf("post-state selector preview = error %v, payload %#v", postPreview.IsError, postPreview.StructuredContent)
	}
	postPayload := postPreview.StructuredContent.(map[string]any)
	postApply := cloneArguments(postArguments)
	postApply["expected_revision"] = postPayload["base_revision"]
	postApply["expected_plan_digest"] = postPayload["plan_digest"]
	noop := callMCPTool(t, srv, "apply_concept_patch", postApply)
	if noop.IsError || noop.StructuredContent.(map[string]any)["status"] != "noop" {
		t.Fatalf("post-state selector noop = error %v, payload %#v", noop.IsError, noop.StructuredContent)
	}

	removeArguments := map[string]any{
		"bundle_path": root, "actor": "mcp:usage-window",
		"operations": []any{map[string]any{
			"kind": "remove_source", "concept_id": "a", "source": postSource,
		}},
	}
	removePreview := callMCPTool(t, srv, "preview_concept_patch", removeArguments)
	if removePreview.IsError {
		t.Fatalf("exact anonymous remove preview returned error: %s", resultText(t, removePreview))
	}
	removePayload := removePreview.StructuredContent.(map[string]any)
	removeApply := cloneArguments(removeArguments)
	removeApply["expected_revision"] = removePayload["base_revision"]
	removeApply["expected_plan_digest"] = removePayload["plan_digest"]
	removed := callMCPTool(t, srv, "apply_concept_patch", removeApply)
	if removed.IsError {
		t.Fatalf("exact anonymous remove apply returned error: %s", resultText(t, removed))
	}
	readResult := callMCPTool(t, srv, "read_concept", map[string]any{
		"bundle_path": root, "concept_id": "a",
	})
	if readResult.IsError ||
		len(readResult.StructuredContent.(map[string]any)["projection"].(map[string]any)["sources"].([]any)) != 0 {
		t.Fatalf("exact anonymous remove did not remove the source: %#v", readResult.StructuredContent)
	}
}

func TestMCPSetUsageWindowRejectsAmbiguousAndOpenSelectorShapes(t *testing.T) {
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	window := map[string]any{"from": "2026-02-01", "to": "2026-02-28"}

	t.Run("ambiguous-anonymous-pre-state", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n")
		writeTestFile(t, root, "a.md", "---\ntype: Note\nsources:\n  - resource: urn:test:same\n  - resource: urn:test:same\n---\nA.\n")
		result := callMCPTool(t, srv, "preview_concept_patch", map[string]any{
			"bundle_path": root, "actor": "mcp:usage-window",
			"operations": []any{map[string]any{
				"kind": "set_usage_window", "concept_id": "a",
				"source": map[string]any{"resource": "urn:test:same"}, "usage_window": window,
			}},
		})
		if result.IsError {
			t.Fatalf("rejected preview must satisfy its success output contract: %s", resultText(t, result))
		}
		if result.StructuredContent.(map[string]any)["status"] != "rejected" {
			t.Fatalf("ambiguous selector preview = %#v", result.StructuredContent)
		}
	})

	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
	invalidOperations := []map[string]any{
		{"kind": "set_usage_window", "concept_id": "a", "source_id": "", "usage_window": window},
		{
			"kind": "set_usage_window", "concept_id": "a", "source_id": "source",
			"source": map[string]any{"resource": "urn:test:source"}, "usage_window": window,
		},
		{
			"kind": "set_usage_window", "concept_id": "a",
			"source": map[string]any{"id": "identified", "resource": "urn:test:source"}, "usage_window": window,
		},
		{
			"kind": "set_usage_window", "concept_id": "a",
			"source": map[string]any{"resource": "urn:test:source", "unknown": true}, "usage_window": window,
		},
		{
			"kind": "set_usage_window", "concept_id": "a",
			"source_selector": map[string]any{"resource": "urn:test:source"}, "usage_window": window,
		},
		{"kind": "remove_source", "concept_id": "a", "source_id": ""},
		{
			"kind": "remove_source", "concept_id": "a", "source_id": "source",
			"source": map[string]any{"resource": "urn:test:source"},
		},
		{
			"kind": "remove_source", "concept_id": "a",
			"source": map[string]any{"id": "identified", "resource": "urn:test:source"},
		},
		{
			"kind": "remove_source", "concept_id": "a",
			"source": map[string]any{"resource": "urn:test:source", "unknown": true},
		},
	}
	for index, operation := range invalidOperations {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			preview := callMCPTool(t, srv, "preview_concept_patch", map[string]any{
				"bundle_path": root, "actor": "mcp:usage-window", "operations": []any{operation},
			})
			assertSchemaErrorEnvelope(t, preview, "schema_validation")
			apply := callMCPTool(t, srv, "apply_concept_patch", map[string]any{
				"bundle_path": root, "actor": "mcp:usage-window", "operations": []any{operation},
				"expected_revision":    "sha256:" + strings.Repeat("a", 64),
				"expected_plan_digest": "sha256:" + strings.Repeat("b", 64),
			})
			assertSchemaErrorEnvelope(t, apply, "schema_validation")
		})
	}
}

func TestMCPSetUsageWindowInvalidSelectorsRejectBeforeSourceOrStoreAccess(t *testing.T) {
	// Arrange.
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	window := map[string]any{"from": "2026-02-01", "to": "2026-02-28"}
	tests := []struct {
		name        string
		selector    map[string]any
		wantCode    string
		wantMessage string
	}{
		{
			name: "empty id", selector: map[string]any{"source_id": ""},
			wantCode: "schema_validation",
		},
		{
			name: "space id", selector: map[string]any{"source_id": " "},
			wantCode: "invalid_request", wantMessage: "operations[0]: invalid change set: invalid source id",
		},
		{
			name: "control id", selector: map[string]any{"source_id": "source\x00"},
			wantCode: "invalid_request", wantMessage: "operations[0]: invalid change set: invalid source id",
		},
		{
			name: "oversize id", selector: map[string]any{"source_id": strings.Repeat("s", 4097)},
			wantCode: "resource_limit",
		},
		{
			name: "empty exact resource", selector: map[string]any{
				"source": map[string]any{"resource": ""},
			},
			wantCode: "invalid_request", wantMessage: "operations[0]: invalid change set: invalid source resource",
		},
		{
			name: "space exact resource", selector: map[string]any{
				"source": map[string]any{"resource": " "},
			},
			wantCode: "invalid_request", wantMessage: "operations[0]: invalid change set: invalid source resource",
		},
		{
			name: "control exact resource", selector: map[string]any{
				"source": map[string]any{"resource": "urn:test:\x00"},
			},
			wantCode: "invalid_request", wantMessage: "operations[0]: invalid change set: invalid source resource",
		},
		{
			name: "oversize exact resource", selector: map[string]any{
				"source": map[string]any{"resource": strings.Repeat("r", 4097)},
			},
			wantCode: "resource_limit",
		},
		{
			name: "invalid exact date", selector: map[string]any{
				"source": map[string]any{
					"resource": "urn:test:source", "last_modified": "2026-02-30",
				},
			},
			wantCode: "schema_validation",
		},
		{
			name: "unknown exact field", selector: map[string]any{
				"source": map[string]any{
					"resource": "urn:test:source", "unknown": true,
				},
			},
			wantCode: "schema_validation",
		},
		{
			name: "mixed exact and id fields", selector: map[string]any{
				"source": map[string]any{
					"id": "source", "resource": "urn:test:source",
				},
			},
			wantCode: "schema_validation",
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "poison.md",
				"---\ntype: Note\n"+nestedYAMLValue(bundle.MaxYAMLPhysicalDepth+1)+"---\nA.\n",
			)
			operation := map[string]any{
				"kind": "set_usage_window", "concept_id": "a", "usage_window": window,
			}
			for key, value := range test.selector {
				operation[key] = value
			}
			before := protocolTreeSnapshot(t, root)
			for _, call := range []struct {
				name      string
				arguments map[string]any
			}{
				{
					name: "preview_concept_patch",
					arguments: map[string]any{
						"bundle_path": root, "actor": "mcp:usage-window",
						"operations": []any{operation},
					},
				},
				{
					name: "apply_concept_patch",
					arguments: map[string]any{
						"bundle_path": root, "actor": "mcp:usage-window",
						"operations":           []any{operation},
						"expected_revision":    "sha256:" + strings.Repeat("a", 64),
						"expected_plan_digest": "sha256:" + strings.Repeat("b", 64),
					},
				},
			} {
				result := callMCPTool(t, srv, call.name, call.arguments)
				assertSchemaErrorEnvelope(t, result, test.wantCode)
				envelope := result.StructuredContent.(map[string]any)
				if test.wantMessage != "" && envelope["message"] != test.wantMessage {
					t.Fatalf("%s message = %q, want %q", call.name, envelope["message"], test.wantMessage)
				}
			}
			after := protocolTreeSnapshot(t, root)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid selector changed bundle tree:\nbefore=%#v\nafter=%#v", before, after)
			}
			if _, statErr := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid selector opened transactional store: %v", statErr)
			}
		})
	}
}

func assertToolProperties(t *testing.T, tool mcp.Tool, want []string) {
	t.Helper()
	got := make([]string, 0, len(tool.InputSchema.Properties))
	for name := range tool.InputSchema.Properties {
		got = append(got, name)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("tool %s properties = %#v, want %#v", tool.Name, got, want)
	}
}

func TestMCPServerCallToolBusinessErrorsStayToolErrors(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "bad.md", "---\ntype: [\n---\nBad.\n")
	validRoot := t.TempDir()
	writeTestFile(t, validRoot, "a.md", "---\ntype: Note\n---\nA.\n")

	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
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

func schemaNode(t *testing.T, schema any, path ...string) map[string]any {
	t.Helper()
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("Marshal(schema) error = %v", err)
	}
	var current any
	if err := json.Unmarshal(data, &current); err != nil {
		t.Fatalf("Unmarshal(schema) error = %v", err)
	}
	for _, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("schema path %v reached %T before %q", path, current, segment)
		}
		current, ok = object[segment]
		if !ok {
			t.Fatalf("schema path %v missing %q in %#v", path, segment, object)
		}
	}
	node, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("schema path %v = %T, want object", path, current)
	}
	return node
}
