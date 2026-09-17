package mcpserver

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/mutation"
)

func TestMigrationSuccessOutputSourceInvariantAcceptsRealStates(t *testing.T) {
	// Arrange.
	fixtures := outputStateContractFixtures(t)
	futureBlocked := migrationFutureBlockedOutput(t)
	tests := []struct {
		name string
		tool string
		base any
	}{
		{name: "preview applicable", tool: "preview_v02_migration", base: fixtures.migrationApplicable},
		{name: "preview planned noop", tool: "preview_v02_migration", base: fixtures.migrationPlannedNoop},
		{name: "preview target noop", tool: "preview_v02_migration", base: fixtures.migrationTargetNoop},
		{name: "preview blocked", tool: "preview_v02_migration", base: fixtures.migrationBlocked},
		{name: "preview future blocked", tool: "preview_v02_migration", base: futureBlocked},
		{name: "apply applied", tool: "apply_v02_migration", base: fixtures.migrationApplied},
		{name: "apply transition noop", tool: "apply_v02_migration", base: fixtures.migrationTransitionNoop},
		{name: "apply target noop", tool: "apply_v02_migration", base: fixtures.migrationApplyTargetNoop},
		{name: "apply rejected", tool: "apply_v02_migration", base: fixtures.migrationApplyRejected},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateMigrationOutputSourceState(test.tool, test.base); err != nil {
				t.Fatalf("real output source state rejected: %v", err)
			}
			result := mcp.NewToolResultStructured(test.base, "")
			if got := enforceContractResult(test.tool, result); got != result {
				t.Fatalf("real output rejected by contract boundary: %#v", got.StructuredContent)
			}
		})
	}
}

func TestMigrationSuccessOutputSourceInvariantRejectsNoncanonicalBlockedStates(t *testing.T) {
	// Arrange.
	base := migrationFutureBlockedOutput(t)
	mutations := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "selector",
			mutate: func(source map[string]any) {
				source["requested_selector"] = mutation.MigrationVersionV01
			},
		},
		{
			name: "declaration raw",
			mutate: func(source map[string]any) {
				source["declaration_raw"] = "8.0"
			},
		},
		{
			name: "declared version",
			mutate: func(source map[string]any) {
				source["declared_version"] = "8.0"
			},
		},
		{
			name: "resolved source",
			mutate: func(source map[string]any) {
				source["resolved_source"] = "8.0"
			},
		},
		{
			name: "from version",
			mutate: func(source map[string]any) {
				source["from_version"] = "8.0"
			},
		},
		{
			name: "to version",
			mutate: func(source map[string]any) {
				source["to_version"] = mutation.MigrationVersionV02
			},
		},
		{
			name: "provenance",
			mutate: func(source map[string]any) {
				source["resolution_source"] = string(mutation.MigrationResolutionDeclared)
			},
		},
		{
			name: "candidate",
			mutate: func(source map[string]any) {
				source["candidates"] = []any{map[string]any{
					"kind": "timestamp", "path": "a.md",
					"location": map[string]any{"start": 0, "end": 0},
				}}
			},
		},
		{
			name: "blocker code",
			mutate: func(source map[string]any) {
				source["blockers"].([]any)[0].(map[string]any)["code"] = "changed"
			},
		},
		{
			name: "blocker path",
			mutate: func(source map[string]any) {
				source["blockers"].([]any)[0].(map[string]any)["path"] = "a.md"
			},
		},
		{
			name: "blocker message",
			mutate: func(source map[string]any) {
				source["blockers"].([]any)[0].(map[string]any)["message"] = "changed"
			},
		},
		{
			name: "transition",
			mutate: func(source map[string]any) {
				source["transition"] = string(mutation.MigrationTransitionTargetNoop)
				source["to_version"] = "9.0"
				source["blockers"] = []any{}
			},
		},
	}

	// Act and assert.
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			value := outputStateMap(t, base)
			test.mutate(outputStateSource(value))

			if err := validateMigrationOutputSourceState(
				"preview_v02_migration",
				value,
			); err == nil {
				t.Fatalf("noncanonical blocked source accepted: %#v", value["source"])
			}
			result := enforceContractResult(
				"preview_v02_migration",
				mcp.NewToolResultStructured(value, ""),
			)
			if !result.IsError {
				t.Fatalf("contract boundary accepted blocked source: %#v", value["source"])
			}
		})
	}
}

func TestMigrationSuccessOutputSourceInvariantRejectsNoncanonicalStates(t *testing.T) {
	// Arrange.
	fixtures := outputStateContractFixtures(t)
	bases := []struct {
		name string
		tool string
		base any
	}{
		{name: "preview transition", tool: "preview_v02_migration", base: fixtures.migrationApplicable},
		{name: "preview target", tool: "preview_v02_migration", base: fixtures.migrationTargetNoop},
		{name: "apply transition", tool: "apply_v02_migration", base: fixtures.migrationApplied},
		{name: "apply target", tool: "apply_v02_migration", base: fixtures.migrationApplyTargetNoop},
	}
	validCandidate := func(path string) map[string]any {
		return map[string]any{
			"kind": "timestamp", "path": path,
			"location": map[string]any{"start": 0, "end": 0},
		}
	}
	mutations := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "declaration coherence",
			mutate: func(source map[string]any) {
				source["declaration_valid"] = false
			},
		},
		{
			name: "resolution provenance",
			mutate: func(source map[string]any) {
				source["resolution_source"] = string(mutation.MigrationResolutionFuture)
			},
		},
		{
			name: "candidate kind",
			mutate: func(source map[string]any) {
				candidate := validCandidate("a.md")
				candidate["kind"] = "unknown"
				source["candidates"] = []any{candidate}
			},
		},
		{
			name: "candidate path",
			mutate: func(source map[string]any) {
				source["candidates"] = []any{validCandidate("a.txt")}
			},
		},
		{
			name: "candidate span",
			mutate: func(source map[string]any) {
				candidate := validCandidate("a.md")
				candidate["location"].(map[string]any)["start"] = -1
				source["candidates"] = []any{candidate}
			},
		},
		{
			name: "candidate order",
			mutate: func(source map[string]any) {
				source["candidates"] = []any{
					validCandidate("b.md"),
					validCandidate("a.md"),
				}
			},
		},
		{
			name: "transition tuple",
			mutate: func(source map[string]any) {
				if source["transition"] == string(mutation.MigrationTransitionV01ToV02) {
					source["to_version"] = mutation.MigrationVersionV01
				} else {
					source["from_version"] = mutation.MigrationVersionV01
				}
			},
		},
	}

	// Act and assert.
	for _, base := range bases {
		for _, mutationCase := range mutations {
			t.Run(base.name+"/"+mutationCase.name, func(t *testing.T) {
				value := outputStateMap(t, base.base)
				mutationCase.mutate(outputStateSource(value))

				if err := validateMigrationOutputSourceState(base.tool, value); err == nil {
					t.Fatalf("noncanonical output source accepted: %#v", value["source"])
				}
			})
		}
	}
}

func migrationFutureBlockedOutput(t *testing.T) migrationPreviewResponse {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"9.0\"\n---\n")
	result := callHandler(t, handlePreviewV02Migration, map[string]any{
		"bundle_path": root, "timestamp_policy": "preserve",
		"citation_mappings": []any{},
	})
	if result.IsError {
		t.Fatalf("future preview returned error: %s", resultText(t, result))
	}
	response, ok := result.StructuredContent.(migrationPreviewResponse)
	if !ok ||
		response.Status != "blocked" ||
		response.Source.Transition != string(mutation.MigrationTransitionBlocked) {
		t.Fatalf("future preview = %#v", result.StructuredContent)
	}
	return response
}
