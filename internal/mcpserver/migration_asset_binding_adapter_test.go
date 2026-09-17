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
	"github.com/skosovsky/okf/mutation"
)

func TestMCPMigrationInheritsResolvedComputationAssetBinding(t *testing.T) {
	// Arrange.
	tests := []struct {
		name            string
		computationPath string
		zeroByteAsset   bool
	}{
		{
			name:            "document relative",
			computationPath: "../references/query.sql",
		},
		{
			name:            "bundle relative",
			computationPath: "/references/query.sql",
		},
		{
			name:            "normalized path with suffix",
			computationPath: "../references/../references/query.sql?mode=preview#fragment",
		},
		{
			name:            "zero-byte asset",
			computationPath: "../references/query.sql",
			zeroByteAsset:   true,
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newNestedComputationMigrationRoot(t)
			arguments := nestedComputationMigrationArguments(
				root,
				test.computationPath,
				"references/query.sql",
			)
			if test.zeroByteAsset {
				computations := arguments["computations"].([]any)
				computation := computations[0].(map[string]any)
				asset := computation["asset"].(map[string]any)
				asset["content"] = ""
			}
			preview := callMigrationContract(
				t,
				t.Context(),
				"preview_v02_migration",
				arguments,
			)
			if preview.IsError {
				t.Fatalf("preview returned error: %s", resultText(t, preview))
			}
			plan := preview.StructuredContent.(migrationPreviewResponse)
			if plan.Status != "applicable" || plan.Proof == nil || plan.PlanDigest == "" {
				t.Fatalf("preview = %#v", plan)
			}

			applyArguments := cloneArguments(arguments)
			applyArguments["proof"] = plan.Proof
			applyArguments["expected_plan_digest"] = plan.PlanDigest
			applyArguments["expected_source"] = plan.Source
			applied := callMigrationContract(
				t,
				t.Context(),
				"apply_v02_migration",
				applyArguments,
			)
			if applied.IsError {
				t.Fatalf("apply returned error: %s", resultText(t, applied))
			}
			if response := applied.StructuredContent.(migrationApplyResponse); response.Status != "applied" {
				t.Fatalf("apply = %#v", response)
			}
			document := readTestFile(t, root, "nested/query.md")
			if !strings.Contains(document, test.computationPath) {
				t.Fatalf(
					"published document did not preserve computation path %q:\n%s",
					test.computationPath,
					document,
				)
			}
			wantAsset := "SELECT 1;\n"
			if test.zeroByteAsset {
				wantAsset = ""
			}
			if asset := readTestFile(t, root, "references/query.sql"); asset != wantAsset {
				t.Fatalf("published asset = %q, want %q", asset, wantAsset)
			}

			beforeNoop := protocolTreeSnapshot(t, root)
			replay := callMigrationContract(
				t,
				t.Context(),
				"preview_v02_migration",
				arguments,
			)
			if replay.IsError {
				t.Fatalf("target-noop preview returned error: %s", resultText(t, replay))
			}
			replayPlan := replay.StructuredContent.(migrationPreviewResponse)
			if replayPlan.Status != "noop" ||
				replayPlan.Proof != nil ||
				replayPlan.PlanDigest != "" ||
				replayPlan.Source.Transition != string(mutation.MigrationTransitionTargetNoop) {
				t.Fatalf("target-noop preview = %#v", replayPlan)
			}
			if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, beforeNoop) {
				t.Fatalf("target-noop preview changed tree:\nbefore=%#v\nafter=%#v", beforeNoop, after)
			}

			replayApplyArguments := cloneArguments(arguments)
			replayApplyArguments["expected_source"] = replayPlan.Source
			replayed := callMigrationContract(
				t,
				t.Context(),
				"apply_v02_migration",
				replayApplyArguments,
			)
			if replayed.IsError {
				t.Fatalf("target-noop apply returned error: %s", resultText(t, replayed))
			}
			if response := replayed.StructuredContent.(migrationApplyResponse); response.Status != "noop" {
				t.Fatalf("target-noop apply = %#v", response)
			}
			if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, beforeNoop) {
				t.Fatalf("target-noop apply changed tree:\nbefore=%#v\nafter=%#v", beforeNoop, after)
			}
		})
	}
}

func TestMCPMigrationComputationAssetMismatchIsStableAndWriteFree(t *testing.T) {
	// Arrange.
	tests := []struct {
		name            string
		computationPath string
		assetPath       string
	}{
		{
			name:            "raw equal resolves below nested document",
			computationPath: "references/query.sql",
			assetPath:       "references/query.sql",
		},
		{
			name:            "different canonical asset",
			computationPath: "../references/other.sql",
			assetPath:       "references/query.sql",
		},
		{
			name:            "external URL",
			computationPath: "https://example.test/query.sql",
			assetPath:       "references/query.sql",
		},
		{
			name:            "ambiguous Windows path",
			computationPath: `C:\work\query.sql`,
			assetPath:       "references/query.sql",
		},
		{
			name:            "bundle escape",
			computationPath: "../../references/query.sql",
			assetPath:       "references/query.sql",
		},
		{
			name:            "suffix only",
			computationPath: "#fragment",
			assetPath:       "references/query.sql",
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Run("transition", func(t *testing.T) {
				root := newNestedComputationMigrationRoot(t)
				arguments := nestedComputationMigrationArguments(
					root,
					test.computationPath,
					test.assetPath,
				)
				assertMCPMigrationAssetMismatchTransition(t, root, arguments)
			})

			t.Run("target noop", func(t *testing.T) {
				root := newNestedComputationMigrationRoot(t)
				applyNestedComputationMigration(
					t,
					root,
					"../references/query.sql",
				)
				arguments := nestedComputationMigrationArguments(
					root,
					test.computationPath,
					test.assetPath,
				)
				assertMCPMigrationAssetMismatchTargetNoop(t, root, arguments)
			})
		})
	}
}

func assertMCPMigrationAssetMismatchTransition(
	t *testing.T,
	root string,
	arguments map[string]any,
) {
	t.Helper()
	before := protocolTreeSnapshot(t, root)
	storeOpens := 0
	ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
		storeOpen: func() { storeOpens++ },
	})
	preview := callMigrationContract(t, ctx, "preview_v02_migration", arguments)
	assertBlockedAssetBindingPreview(t, preview)
	plan := preview.StructuredContent.(migrationPreviewResponse)

	digest := "sha256:" + strings.Repeat("a", 64)
	applyArguments := cloneArguments(arguments)
	applyArguments["proof"] = testMigrationPlanProofDTO(
		plan.BaseRevision,
		plan.BaseRevision,
		digest,
	)
	applyArguments["expected_plan_digest"] = digest
	applyArguments["expected_source"] = plan.Source
	applied := callMigrationContract(t, ctx, "apply_v02_migration", applyArguments)
	assertSchemaErrorEnvelope(t, applied, "plan_mismatch")
	assertMigrationAssetMismatchDidNotWrite(t, root, before, storeOpens)
	assertNoMigrationMetadata(t, root)
}

func assertMCPMigrationAssetMismatchTargetNoop(
	t *testing.T,
	root string,
	arguments map[string]any,
) {
	t.Helper()
	before := protocolTreeSnapshot(t, root)
	storeOpens := 0
	ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
		storeOpen: func() { storeOpens++ },
	})
	preview := callMigrationContract(t, ctx, "preview_v02_migration", arguments)
	assertBlockedAssetBindingPreview(t, preview)
	plan := preview.StructuredContent.(migrationPreviewResponse)
	if plan.Source.Transition != string(mutation.MigrationTransitionTargetNoop) {
		t.Fatalf("blocked target source = %#v", plan.Source)
	}

	applyArguments := cloneArguments(arguments)
	applyArguments["expected_source"] = plan.Source
	applied := callMigrationContract(t, ctx, "apply_v02_migration", applyArguments)
	assertSchemaErrorEnvelope(t, applied, "migration_computation_asset_path_mismatch")
	assertMigrationAssetMismatchDidNotWrite(t, root, before, storeOpens)
}

func assertBlockedAssetBindingPreview(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	if result.IsError {
		t.Fatalf("blocked preview returned transport error: %s", resultText(t, result))
	}
	preview := result.StructuredContent.(migrationPreviewResponse)
	if preview.Status != "blocked" ||
		preview.Proof != nil ||
		preview.PlanDigest != "" ||
		preview.ResultRevision != nil ||
		len(preview.Blockers) != 1 ||
		preview.Blockers[0].Code != "migration_computation_asset_path_mismatch" ||
		preview.Blockers[0].Path != "nested/query.md" ||
		len(preview.AffectedPaths) != 0 {
		t.Fatalf("blocked preview = %#v", preview)
	}
}

func assertMigrationAssetMismatchDidNotWrite(
	t *testing.T,
	root string,
	before map[string]string,
	storeOpens int,
) {
	t.Helper()
	if storeOpens != 0 {
		t.Fatalf("transactional store open count = %d, want 0", storeOpens)
	}
	if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("blocked migration changed tree:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func applyNestedComputationMigration(t *testing.T, root, computationPath string) {
	t.Helper()
	arguments := nestedComputationMigrationArguments(
		root,
		computationPath,
		"references/query.sql",
	)
	preview := callMigrationContract(
		t,
		t.Context(),
		"preview_v02_migration",
		arguments,
	)
	if preview.IsError {
		t.Fatalf("setup preview returned error: %s", resultText(t, preview))
	}
	plan := preview.StructuredContent.(migrationPreviewResponse)
	if plan.Proof == nil || plan.PlanDigest == "" {
		t.Fatalf("setup preview did not return authorization: %#v", plan)
	}
	applyArguments := cloneArguments(arguments)
	applyArguments["proof"] = plan.Proof
	applyArguments["expected_plan_digest"] = plan.PlanDigest
	applyArguments["expected_source"] = plan.Source
	applied := callMigrationContract(
		t,
		t.Context(),
		"apply_v02_migration",
		applyArguments,
	)
	if applied.IsError {
		t.Fatalf("setup apply returned error: %s", resultText(t, applied))
	}
}

func newNestedComputationMigrationRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "index.md",
		"---\nokf_version: \"0.1\"\n---\n\n# Computations\n\n- [Query](nested/query.md)\n",
	)
	writeTestFile(t, root, "nested/query.md",
		"---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nBody.\n",
	)
	return root
}

func nestedComputationMigrationArguments(
	root string,
	computationPath string,
	assetPath string,
) map[string]any {
	return map[string]any{
		"bundle_path":               root,
		"from":                      "auto",
		"generated_by":              "process:migration",
		"timestamp_policy":          "remove_after_copy",
		"timestamp_conflict_policy": "reject",
		"citation_mappings":         []any{},
		"computations": []any{map[string]any{
			"concept_id": "nested/query",
			"contract": map[string]any{
				"mode":             "file",
				"runtime":          "sql",
				"parameters":       []any{},
				"computation_path": computationPath,
				"executor": map[string]any{
					"resource": "executors/sql.md",
					"receipt":  []any{"rows"},
				},
				"attester": map[string]any{"resource": "attesters/sql.md"},
			},
			"asset": map[string]any{
				"path":    assetPath,
				"content": "SELECT 1;\n",
			},
		}},
	}
}

func callMigrationContract(
	t *testing.T,
	ctx context.Context,
	name string,
	arguments map[string]any,
) *mcp.CallToolResult {
	t.Helper()
	var handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	switch name {
	case "preview_v02_migration":
		handler = contractToolHandler(name, handlePreviewV02Migration)
	case "apply_v02_migration":
		handler = contractToolHandler(name, handleApplyV02Migration)
	default:
		t.Fatalf("unknown migration tool %q", name)
	}
	result, err := handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: arguments,
		},
	})
	if err != nil {
		t.Fatalf("%s protocol error = %v", name, err)
	}
	return result
}

func assertNoMigrationMetadata(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transactional metadata exists: %v", err)
	}
}
