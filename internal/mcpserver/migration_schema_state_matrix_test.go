package mcpserver

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestMigrationOutputApplicableStatesRejectEverySourceSemanticMutation(t *testing.T) {
	// Arrange.
	fixtures := outputStateContractFixtures(t)
	states := []struct {
		name     string
		contract string
		value    any
	}{
		{name: "preview applicable", contract: "preview_v02_migration.output", value: fixtures.migrationApplicable},
		{name: "preview planned noop", contract: "preview_v02_migration.output", value: fixtures.migrationPlannedNoop},
		{name: "preview target noop", contract: "preview_v02_migration.output", value: fixtures.migrationTargetNoop},
		{name: "apply applied", contract: "apply_v02_migration.output", value: fixtures.migrationApplied},
		{name: "apply transition noop", contract: "apply_v02_migration.output", value: fixtures.migrationTransitionNoop},
		{name: "apply target noop", contract: "apply_v02_migration.output", value: fixtures.migrationApplyTargetNoop},
		{name: "apply rejected", contract: "apply_v02_migration.output", value: fixtures.migrationApplyRejected},
	}
	candidate := map[string]any{
		"kind": "timestamp",
		"path": "a.md",
		"location": map[string]any{
			"start": float64(0),
			"end":   float64(1),
		},
	}
	blocker := map[string]any{
		"code": "blocked",
		"path": "a.md",
		"location": map[string]any{
			"start": float64(0),
			"end":   float64(1),
		},
		"message": "blocked",
	}
	mutations := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "requested selector",
			mutate: func(source map[string]any) {
				if source["resolved_source"] == "0.1" {
					source["requested_selector"] = "0.1"
				} else {
					source["requested_selector"] = "0.2"
				}
			},
		},
		{
			name: "declaration present",
			mutate: func(source map[string]any) {
				source["declaration_present"] = !source["declaration_present"].(bool)
			},
		},
		{
			name: "declaration valid",
			mutate: func(source map[string]any) {
				source["declaration_valid"] = false
			},
		},
		{
			name: "declaration raw",
			mutate: func(source map[string]any) {
				if source["declaration_raw"] == "" {
					source["declaration_raw"] = "0.2"
				} else {
					source["declaration_raw"] = ""
				}
			},
		},
		{
			name: "declared version",
			mutate: func(source map[string]any) {
				if source["declared_version"] == nil {
					source["declared_version"] = "0.2"
				} else {
					source["declared_version"] = nil
				}
			},
		},
		{
			name: "resolved source",
			mutate: func(source map[string]any) {
				if source["resolved_source"] == "0.1" {
					source["resolved_source"] = "0.2"
				} else {
					source["resolved_source"] = "0.1"
				}
			},
		},
		{
			name: "resolution provenance",
			mutate: func(source map[string]any) {
				source["resolution_source"] = "future"
			},
		},
		{
			name: "from version",
			mutate: func(source map[string]any) {
				if source["from_version"] == "0.1" {
					source["from_version"] = "0.2"
				} else {
					source["from_version"] = "0.1"
				}
			},
		},
		{
			name: "to version",
			mutate: func(source map[string]any) {
				if source["to_version"] == "0.1" {
					source["to_version"] = "0.2"
				} else {
					source["to_version"] = "0.1"
				}
			},
		},
		{
			name: "transition",
			mutate: func(source map[string]any) {
				source["transition"] = "blocked"
			},
		},
		{
			name: "candidates",
			mutate: func(source map[string]any) {
				source["candidates"] = []any{deepCloneMap(t, candidate)}
			},
		},
		{
			name: "source blockers",
			mutate: func(source map[string]any) {
				source["blockers"] = []any{deepCloneMap(t, blocker)}
			},
		},
	}

	// Act and assert.
	for _, state := range states {
		for _, mutation := range mutations {
			t.Run(state.name+"/"+mutation.name, func(t *testing.T) {
				value := outputStateMap(t, state.value)
				mutation.mutate(outputStateSource(value))
				if err := validateContract(state.contract, value); err == nil {
					t.Fatalf("%s accepted mutated source: %#v", state.contract, value["source"])
				}
			})
		}
	}
}

func TestMigrationPreviewBlockedStateAcceptsOnlyApplicableOrCoherentBlockedSource(t *testing.T) {
	// Arrange.
	fixtures := outputStateContractFixtures(t)
	applicableV01 := outputStateMap(t, fixtures.migrationBlocked)
	applicableTarget := outputStateMap(t, fixtures.migrationBlocked)
	applicableTarget["source"] = outputStateMap(t, fixtures.migrationTargetNoop)["source"]
	blocked := outputStateMap(t, fixtures.migrationBlocked)
	blocked["source"] = map[string]any{
		"requested_selector":  "auto",
		"declaration_present": true,
		"declaration_valid":   true,
		"declaration_raw":     "9.0",
		"declared_version":    "9.0",
		"resolved_source":     "9.0",
		"resolution_source":   "future",
		"from_version":        "9.0",
		"to_version":          "",
		"transition":          "blocked",
		"candidates":          []any{},
		"blockers": []any{map[string]any{
			"code": "unsupported_migration_source",
			"path": "index.md",
			"location": map[string]any{
				"start": float64(0),
				"end":   float64(0),
			},
			"message": `future declaration "9.0" cannot be rewritten as v0.2`,
		}},
	}

	// Act and assert.
	for name, value := range map[string]map[string]any{
		"applicable v0.1":    applicableV01,
		"applicable target":  applicableTarget,
		"resolution blocked": blocked,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateContract("preview_v02_migration.output", value); err != nil {
				t.Fatalf("blocked preview rejected valid source state: %v", err)
			}
		})
	}
	invalidBlockedSources := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "without source blocker", mutate: func(source map[string]any) {
			source["blockers"] = []any{}
		}},
		{name: "extra source blocker", mutate: func(source map[string]any) {
			blockers := source["blockers"].([]any)
			source["blockers"] = append(blockers, deepCloneMap(t, blockers[0].(map[string]any)))
		}},
		{name: "source blocker code", mutate: func(source map[string]any) {
			source["blockers"].([]any)[0].(map[string]any)["code"] = "blocked"
		}},
		{name: "source blocker path", mutate: func(source map[string]any) {
			source["blockers"].([]any)[0].(map[string]any)["path"] = "a.md"
		}},
		{name: "source blocker start", mutate: func(source map[string]any) {
			location := source["blockers"].([]any)[0].(map[string]any)["location"].(map[string]any)
			location["start"] = float64(1)
		}},
		{name: "source blocker end", mutate: func(source map[string]any) {
			location := source["blockers"].([]any)[0].(map[string]any)["location"].(map[string]any)
			location["end"] = float64(1)
		}},
		{name: "source blocker message", mutate: func(source map[string]any) {
			source["blockers"].([]any)[0].(map[string]any)["message"] = "blocked"
		}},
	}
	for _, invalid := range invalidBlockedSources {
		t.Run(invalid.name, func(t *testing.T) {
			value := deepCloneMap(t, blocked)
			invalid.mutate(outputStateSource(value))
			if err := validateContract("preview_v02_migration.output", value); err == nil {
				t.Fatal("blocked preview accepted incoherent future source blocker")
			}
		})
	}
}

func TestMigrationApplicableSourceUnionAcceptsEveryRuntimeProvenance(t *testing.T) {
	// Arrange.
	v01Declared := testMigrationSourceValue("0.1", "v0.1-to-v0.2")
	v01ExplicitAbsent := deepCloneMap(t, v01Declared)
	v01ExplicitAbsent["requested_selector"] = "0.1"
	v01ExplicitAbsent["declaration_present"] = false
	v01ExplicitAbsent["declaration_raw"] = ""
	v01ExplicitAbsent["declared_version"] = nil
	v01ExplicitAbsent["resolution_source"] = "explicit"
	v01ExplicitPresent := deepCloneMap(t, v01Declared)
	v01ExplicitPresent["requested_selector"] = "0.1"
	v01ExplicitPresent["resolution_source"] = "explicit"
	v01LegacyProbe := deepCloneMap(t, v01Declared)
	v01LegacyProbe["declaration_present"] = false
	v01LegacyProbe["declaration_raw"] = ""
	v01LegacyProbe["declared_version"] = nil
	v01LegacyProbe["resolution_source"] = "legacy-probe"
	v01LegacyProbe["candidates"] = []any{map[string]any{
		"kind": "timestamp",
		"path": "a.md",
		"location": map[string]any{
			"start": float64(0),
			"end":   float64(1),
		},
	}}

	targetDeclared := testMigrationSourceValue("0.2", "target-noop")
	targetExplicitAbsent := deepCloneMap(t, targetDeclared)
	targetExplicitAbsent["requested_selector"] = "0.2"
	targetExplicitAbsent["declaration_present"] = false
	targetExplicitAbsent["declaration_raw"] = ""
	targetExplicitAbsent["declared_version"] = nil
	targetExplicitAbsent["resolution_source"] = "explicit"
	targetExplicitPresent := deepCloneMap(t, targetDeclared)
	targetExplicitPresent["requested_selector"] = "0.2"
	targetExplicitPresent["resolution_source"] = "explicit"
	targetDefault := deepCloneMap(t, targetDeclared)
	targetDefault["declaration_present"] = false
	targetDefault["declaration_raw"] = ""
	targetDefault["declared_version"] = nil
	targetDefault["resolution_source"] = "default"

	fixtures := outputStateContractFixtures(t)
	tests := []struct {
		name       string
		source     map[string]any
		preview    any
		transition bool
	}{
		{name: "v0.1 declared", source: v01Declared, preview: fixtures.migrationApplicable, transition: true},
		{name: "v0.1 explicit absent", source: v01ExplicitAbsent, preview: fixtures.migrationApplicable, transition: true},
		{name: "v0.1 explicit present", source: v01ExplicitPresent, preview: fixtures.migrationApplicable, transition: true},
		{name: "v0.1 legacy probe", source: v01LegacyProbe, preview: fixtures.migrationApplicable, transition: true},
		{name: "target declared", source: targetDeclared, preview: fixtures.migrationTargetNoop},
		{name: "target explicit absent", source: targetExplicitAbsent, preview: fixtures.migrationTargetNoop},
		{name: "target explicit present", source: targetExplicitPresent, preview: fixtures.migrationTargetNoop},
		{name: "target default", source: targetDefault, preview: fixtures.migrationTargetNoop},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preview := outputStateMap(t, test.preview)
			preview["source"] = test.source
			if err := validateContract("preview_v02_migration.output", preview); err != nil {
				t.Fatalf("preview output rejected runtime provenance: %v", err)
			}
			apply := map[string]any{
				"bundle_path":       "/schema-only",
				"from":              test.source["requested_selector"],
				"timestamp_policy":  "preserve",
				"citation_mappings": []any{},
				"expected_source":   test.source,
			}
			if test.transition {
				apply["proof"] = preview["proof"]
				apply["expected_plan_digest"] = preview["plan_digest"]
			}
			if err := validateContract("apply_v02_migration.input", apply); err != nil {
				t.Fatalf("apply input rejected runtime provenance: %v", err)
			}
		})
	}
}

func TestMigrationPreviewProofIsDirectlyConsumableByApplyInput(t *testing.T) {
	// Arrange.
	preview := outputStateMap(t, outputStateContractFixtures(t).migrationApplicable)
	apply := map[string]any{
		"bundle_path":          "/schema-only",
		"from":                 "auto",
		"timestamp_policy":     "preserve",
		"citation_mappings":    []any{},
		"expected_source":      preview["source"],
		"proof":                preview["proof"],
		"expected_plan_digest": preview["plan_digest"],
	}

	// Act and assert.
	if err := validateContract("preview_v02_migration.output", preview); err != nil {
		t.Fatalf("preview output rejected canonical proof: %v", err)
	}
	if err := validateContract("apply_v02_migration.input", apply); err != nil {
		t.Fatalf("apply input rejected preview proof: %v", err)
	}
}

func TestMigrationPlanProofParityRejectsEveryMissingAndOverlongDimension(t *testing.T) {
	// Arrange.
	preview := outputStateMap(t, outputStateContractFixtures(t).migrationApplicable)
	apply := map[string]any{
		"bundle_path":          "/schema-only",
		"from":                 "auto",
		"timestamp_policy":     "preserve",
		"citation_mappings":    []any{},
		"expected_source":      preview["source"],
		"proof":                preview["proof"],
		"expected_plan_digest": preview["plan_digest"],
	}
	required := []string{
		"format_version",
		"request_digest",
		"resolution_digest",
		"base_revision",
		"result_revision",
		"reads",
		"writes",
		"deletes",
		"renames",
		"affected_refs",
		"reverse_impact",
		"changed_files",
		"changed_refs",
	}
	overlong := strings.Repeat("p", 4097)
	caps := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "reads", mutate: func(proof map[string]any) { proof["reads"] = []any{overlong} }},
		{name: "writes path", mutate: func(proof map[string]any) {
			proof["writes"] = []any{map[string]any{
				"path": overlong, "digest": strings.Repeat("a", 64),
			}}
		}},
		{name: "deletes", mutate: func(proof map[string]any) { proof["deletes"] = []any{overlong} }},
		{name: "rename from", mutate: func(proof map[string]any) {
			proof["renames"] = []any{map[string]any{"from": overlong, "to": "a.md"}}
		}},
		{name: "rename to", mutate: func(proof map[string]any) {
			proof["renames"] = []any{map[string]any{"from": "a.md", "to": overlong}}
		}},
		{name: "affected refs", mutate: func(proof map[string]any) {
			proof["affected_refs"] = []any{overlong}
		}},
		{name: "reverse impact", mutate: func(proof map[string]any) {
			proof["reverse_impact"] = []any{overlong}
		}},
		{name: "changed file path", mutate: func(proof map[string]any) {
			proof["changed_files"] = []any{map[string]any{
				"kind": "write", "path": overlong, "from": "",
			}}
		}},
		{name: "changed file from", mutate: func(proof map[string]any) {
			proof["changed_files"] = []any{map[string]any{
				"kind": "rename", "path": "b.md", "from": overlong,
			}}
		}},
		{name: "changed refs", mutate: func(proof map[string]any) {
			proof["changed_refs"] = []any{overlong}
		}},
	}
	assertBothReject := func(t *testing.T, mutate func(map[string]any)) {
		t.Helper()
		invalidPreview := deepCloneMap(t, preview)
		mutate(invalidPreview["proof"].(map[string]any))
		if err := validateContract("preview_v02_migration.output", invalidPreview); err == nil {
			t.Fatal("preview output accepted invalid proof")
		}
		invalidApply := deepCloneMap(t, apply)
		mutate(invalidApply["proof"].(map[string]any))
		if err := validateContract("apply_v02_migration.input", invalidApply); err == nil {
			t.Fatal("apply input accepted invalid proof")
		}
	}

	// Act and assert.
	for _, field := range required {
		t.Run("missing/"+field, func(t *testing.T) {
			assertBothReject(t, func(proof map[string]any) {
				delete(proof, field)
			})
		})
	}
	for _, cap := range caps {
		t.Run("overlong/"+cap.name, func(t *testing.T) {
			assertBothReject(t, cap.mutate)
		})
	}
}

func TestMigrationSourceSchemaAndRuntimeInvariantSplitIsExplicit(t *testing.T) {
	// Arrange.
	fixture := outputStateMap(t, outputStateContractFixtures(t).migrationApplicable)
	source := outputStateSource(fixture)
	source["declaration_present"] = false
	source["declaration_raw"] = ""
	source["declared_version"] = nil
	source["resolution_source"] = "legacy-probe"
	source["candidates"] = []any{map[string]any{
		"kind": "timestamp",
		"path": "../a.md",
		"location": map[string]any{
			"start": float64(1),
			"end":   float64(0),
		},
	}}

	// Act.
	schemaErr := validateContract("preview_v02_migration.output", fixture)
	result := mcp.NewToolResultStructured(fixture, "")
	enforced := enforceContractResult("preview_v02_migration", result)

	// Assert.
	if schemaErr != nil {
		t.Fatalf("representational source constraint unexpectedly moved into schema: %v", schemaErr)
	}
	if enforced == result {
		t.Fatal("runtime invariant accepted noncanonical candidate path/span")
	}
}
