package mcpserver

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
)

type advertisedContractRow struct {
	name     string
	readOnly bool
	handler  server.ToolHandlerFunc
	input    func(*testing.T, string) map[string]any
}

var advertisedContractRows = []advertisedContractRow{
	{name: "list_concepts", readOnly: true, handler: handleListConcepts, input: func(_ *testing.T, root string) map[string]any {
		return map[string]any{"bundle_path": root}
	}},
	{name: "read_concept", readOnly: true, handler: handleReadConcept, input: func(_ *testing.T, root string) map[string]any {
		return map[string]any{"bundle_path": root, "concept_id": "alpha"}
	}},
	{name: "validate_bundle", readOnly: true, handler: handleValidateBundle, input: func(_ *testing.T, root string) map[string]any {
		return map[string]any{"bundle_path": root}
	}},
	{name: "get_semantic_graph", readOnly: true, handler: handleSemanticGraph, input: func(_ *testing.T, root string) map[string]any {
		return map[string]any{"bundle_path": root}
	}},
	{name: "write_concept", handler: handleWriteConcept, input: func(_ *testing.T, root string) map[string]any {
		return map[string]any{"bundle_path": root, "concept_id": "written", "frontmatter": "type: Note\n", "body": "Written.\n"}
	}},
	{name: "preview_concept_patch", readOnly: true, handler: handlePreviewConceptPatch, input: func(_ *testing.T, root string) map[string]any {
		return patchContractArguments(root, false)
	}},
	{name: "apply_concept_patch", handler: handleApplyConceptPatch, input: func(t *testing.T, root string) map[string]any {
		arguments := patchContractArguments(root, false)
		preview := callHandler(t, contractToolHandler("preview_concept_patch", handlePreviewConceptPatch), arguments)
		if preview.IsError {
			t.Fatalf("patch preview error: %s", resultText(t, preview))
		}
		plan := preview.StructuredContent.(patchPreviewResponse)
		arguments["expected_revision"] = plan.BaseRevision
		arguments["expected_plan_digest"] = plan.PlanDigest
		return arguments
	}},
	{name: "preview_v02_migration", readOnly: true, handler: handlePreviewV02Migration, input: func(_ *testing.T, root string) map[string]any {
		return migrationContractArguments(root, false)
	}},
	{name: "apply_v02_migration", handler: handleApplyV02Migration, input: func(t *testing.T, root string) map[string]any {
		arguments := migrationContractArguments(root, false)
		preview := callHandler(t, contractToolHandler("preview_v02_migration", handlePreviewV02Migration), arguments)
		if preview.IsError {
			t.Fatalf("migration preview error: %s", resultText(t, preview))
		}
		plan := preview.StructuredContent.(migrationPreviewResponse)
		arguments["expected_source"] = plan.Source
		if plan.Proof != nil {
			arguments["proof"] = plan.Proof
			arguments["expected_plan_digest"] = plan.PlanDigest
		}
		return arguments
	}},
}

func TestAdvertisedContractMatrix(t *testing.T) {
	// Arrange.
	if len(advertisedContractRows) != 9 {
		t.Fatalf("advertised rows = %d, want 9", len(advertisedContractRows))
	}
	tools := mustServerTools(t)
	if len(tools) != len(advertisedContractRows) {
		t.Fatalf("registered tools = %d, want %d", len(tools), len(advertisedContractRows))
	}
	registered := make(map[string]server.ServerTool, len(tools))
	for _, tool := range tools {
		registered[tool.Tool.Name] = tool
	}
	// Act and assert.
	seen := map[string]bool{}
	for _, row := range advertisedContractRows {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n\n- [Alpha](alpha.md)\n")
			writeTestFile(t, root, "alpha.md", "---\ntype: Note\n---\nAlpha.\n")
			if seen[row.name] {
				t.Fatalf("duplicate descriptor %q", row.name)
			}
			seen[row.name] = true
			tool, ok := registered[row.name]
			if !ok {
				t.Fatalf("descriptor has no registered tool")
			}
			input := mustContractSchema(t, row.name+".input")
			output := mustContractSchema(t, row.name+".output")
			if !bytes.Equal(tool.Tool.RawInputSchema, input) || !bytes.Equal(tool.Tool.RawOutputSchema, output) {
				t.Fatal("advertised raw schema differs from checked-in bytes")
			}
			if tool.Tool.Annotations.ReadOnlyHint == nil || *tool.Tool.Annotations.ReadOnlyHint != row.readOnly ||
				tool.Tool.Annotations.DestructiveHint == nil || *tool.Tool.Annotations.DestructiveHint == row.readOnly ||
				tool.Tool.Annotations.OpenWorldHint == nil || *tool.Tool.Annotations.OpenWorldHint {
				t.Fatalf("annotations = %#v", tool.Tool.Annotations)
			}
			arguments := row.input(t, root)
			validateContractValue(t, row.name+".input", arguments)
			result := callHandler(t, contractToolHandler(row.name, row.handler), arguments)
			if result.IsError || result.StructuredContent == nil {
				t.Fatalf("real handler result = %#v; text=%s", result.StructuredContent, resultText(t, result))
			}
			validateContractValue(t, row.name+".output", result.StructuredContent)
			if row.name == "write_concept" || strings.Contains(row.name, "patch") || strings.Contains(row.name, "migration") {
				var fallback any
				if err := json.Unmarshal([]byte(resultText(t, result)), &fallback); err != nil {
					t.Fatalf("decode text fallback: %v", err)
				}
				var structured any
				data, err := json.Marshal(result.StructuredContent)
				if err != nil || json.Unmarshal(data, &structured) != nil || !reflect.DeepEqual(fallback, structured) {
					t.Fatalf("structured/text parity failed: marshal=%v", err)
				}
			}
			invalid := cloneArguments(arguments)
			invalid["unexpected"] = true
			handlerCalls := 0
			errorResult := callHandler(t, contractToolHandler(row.name, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				handlerCalls++
				return mcp.NewToolResultText("unreachable"), nil
			}), invalid)
			assertSchemaErrorEnvelope(t, errorResult, "schema_validation")
			if handlerCalls != 0 {
				t.Fatalf("invalid input reached handler %d times", handlerCalls)
			}
		})
	}
}

func TestCheckedInSchemaClosureMatrix(t *testing.T) {
	// Arrange, act, and assert.
	contracts := []string{"common", "error", "version", "migration_v02.shared"}
	for _, row := range advertisedContractRows {
		contracts = append(contracts, row.name+".input", row.name+".output")
	}
	if len(contracts) != 22 {
		t.Fatalf("schema descriptors = %d, want 22", len(contracts))
	}
	for _, name := range contracts {
		t.Run(name, func(t *testing.T) {
			data := mustContractSchema(t, name)
			compileContractSchema(t, name, data)
			var schema any
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatalf("Unmarshal schema: %v", err)
			}
			assertSchemaTree(t, schema, schema, "#")
		})
	}
	shared := decodeSharedContractSchema(t, "common")
	version := decodeSharedContractSchema(t, "version")
	delete(version, "$schema")
	delete(version, "$id")
	if !reflect.DeepEqual(schemaObjectAt(t, shared, "$defs", "version"), version) {
		t.Fatal("common version definition drifted from version.schema.json")
	}
}

func TestConceptIDDefinitionMaterializationDoesNotDrift(t *testing.T) {
	t.Parallel()

	common := decodeSharedContractSchema(t, "common")
	commonDefinition := schemaObjectAt(t, common, "$defs", "concept_id")

	for _, contract := range []string{"read_concept", "write_concept"} {
		contract := contract
		t.Run(contract, func(t *testing.T) {
			t.Parallel()

			root := decodeSharedContractSchema(t, contract+".input")
			materialized := schemaObjectAt(t, root, "$defs", "concept_id")
			if !reflect.DeepEqual(materialized, commonDefinition) {
				t.Fatalf("%s concept_id definition drifted from common", contract)
			}
			property := schemaObjectAt(t, root, "properties", "concept_id")
			allOf, ok := property["allOf"].([]any)
			if !ok || len(allOf) != 2 {
				t.Fatalf("%s concept_id allOf = %#v, want source ref plus transport cap", contract, property["allOf"])
			}
			reference, ok := allOf[0].(map[string]any)
			if !ok || reference["$ref"] != "#/$defs/concept_id" {
				t.Fatalf("%s concept_id source ref = %#v", contract, allOf[0])
			}
			transport, ok := allOf[1].(map[string]any)
			if !ok || transport["maxLength"] != float64(4096) {
				t.Fatalf("%s concept_id transport cap = %#v", contract, allOf[1])
			}
		})
	}
}

func TestLegacyInputContractsCompileAndConceptIDSchemaIsClosed(t *testing.T) {
	for _, contract := range []string{
		"list_concepts.input",
		"read_concept.input",
		"validate_bundle.input",
		"get_semantic_graph.input",
		"write_concept.input",
	} {
		if _, err := compiledContract(contract); err != nil {
			t.Fatalf("compiledContract(%q) error = %v", contract, err)
		}
	}

	root := t.TempDir()
	valid := []string{
		"alpha",
		"groups/customer orders",
		"東京/данные/100%+ready=да",
	}
	for _, conceptID := range valid {
		for _, contract := range []string{"read_concept.input", "write_concept.input"} {
			arguments := map[string]any{
				"bundle_path": root,
				"concept_id":  conceptID,
			}
			if contract == "write_concept.input" {
				arguments["frontmatter"] = "type: Knowledge\n"
				arguments["body"] = "Claim.\n"
			}
			if err := validateContract(contract, arguments); err != nil {
				t.Fatalf("%s rejected valid concept_id %q: %v", contract, conceptID, err)
			}
		}
	}

	invalid := []string{
		"",
		" alpha",
		"alpha ",
		"/alpha",
		"alpha/",
		"alpha//beta",
		"alpha.md",
		"index",
		"group/log",
		".hidden",
		"group/.hidden",
		"file:alpha",
		"urn:alpha",
		`alpha\beta`,
		"alpha\x00beta",
	}
	for _, conceptID := range invalid {
		arguments := map[string]any{
			"bundle_path": root,
			"concept_id":  conceptID,
		}
		if err := validateContract("read_concept.input", arguments); err == nil {
			t.Fatalf("read_concept.input accepted invalid concept_id %q", conceptID)
		}
		arguments["frontmatter"] = ""
		arguments["body"] = ""
		if err := validateContract("write_concept.input", arguments); err == nil {
			t.Fatalf("write_concept.input accepted invalid concept_id %q", conceptID)
		}
	}
}

func assertSchemaTree(t *testing.T, root, value any, pointer string) {
	t.Helper()
	switch node := value.(type) {
	case map[string]any:
		if schemaContainsObject(node) {
			if _, ok := node["additionalProperties"]; !ok {
				t.Errorf("%s object lacks explicit additionalProperties policy", pointer)
			}
		}
		if reference, ok := node["$ref"].(string); ok {
			if !strings.HasPrefix(reference, "#/") || resolveLocalReference(root, reference) == nil {
				t.Errorf("%s unresolved/non-local $ref %q", pointer, reference)
			}
		}
		for key, child := range node {
			assertSchemaTree(t, root, child, pointer+"/"+key)
		}
	case []any:
		for index, child := range node {
			assertSchemaTree(t, root, child, pointer+"/"+string(rune('0'+index)))
		}
	}
}

func schemaContainsObject(value map[string]any) bool {
	typeValue := value["type"]
	if typeValue == "object" {
		return true
	}
	types, _ := typeValue.([]any)
	return slices.Contains(types, any("object"))
}

func resolveLocalReference(root any, reference string) any {
	current := root
	for _, part := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = object[part]
		if !ok {
			return nil
		}
	}
	return current
}

type inputUnionRow struct {
	name     string
	contract string
	base     func() map[string]any
	mutate   func(map[string]any)
	valid    bool
}

func TestInputUnionMatrix(t *testing.T) {
	revision := presenceDigest("a")
	patch := func(apply bool) func() map[string]any {
		return func() map[string]any { return patchPresenceBase("/schema-only", apply, []any{}) }
	}
	migration := func(apply bool) func() map[string]any {
		return func() map[string]any { return migrationPresenceBase("/schema-only", apply) }
	}
	rows := []inputUnionRow{
		{name: "patch/missing actor", contract: "preview_concept_patch.input", base: patch(false), mutate: func(v map[string]any) { delete(v, "actor") }},
		{name: "patch/null actor", contract: "preview_concept_patch.input", base: patch(false), mutate: func(v map[string]any) { v["actor"] = nil }},
		{name: "patch/empty actor", contract: "preview_concept_patch.input", base: patch(false), mutate: func(v map[string]any) { v["actor"] = "" }},
		{name: "patch/actor plus one", contract: "preview_concept_patch.input", base: patch(false), mutate: func(v map[string]any) { v["actor"] = strings.Repeat("a", 257) }},
		{name: "patch/empty parameters", contract: "preview_concept_patch.input", base: patch(false), valid: true},
		{name: "patch/explicit false", contract: "preview_concept_patch.input", base: patch(false), valid: true, mutate: func(v map[string]any) {
			patchPresenceContract(v)["parameters"] = []any{map[string]any{"name": "p", "type": "string", "required": false}}
		}},
		{name: "patch/apply zero digest invalid", contract: "apply_concept_patch.input", base: patch(true), mutate: func(v map[string]any) { v["expected_plan_digest"] = "" }},
		{name: "patch/apply canonical digest", contract: "apply_concept_patch.input", base: patch(true), valid: true},
		{name: "migration/missing citations", contract: "preview_v02_migration.input", base: migration(false), mutate: func(v map[string]any) { delete(v, "citation_mappings") }},
		{name: "migration/null citations", contract: "preview_v02_migration.input", base: migration(false), mutate: func(v map[string]any) { v["citation_mappings"] = nil }},
		{name: "migration/empty citations", contract: "preview_v02_migration.input", base: migration(false), valid: true},
		{name: "migration/inline empty", contract: "preview_v02_migration.input", base: migration(false), valid: true, mutate: func(v map[string]any) { v["computations"] = []any{migrationPresenceInlineComputation([]any{})} }},
		{name: "migration/file empty asset", contract: "preview_v02_migration.input", base: migration(false), valid: true, mutate: func(v map[string]any) { v["computations"] = []any{migrationPresenceFileComputation()} }},
		{name: "migration/citation legacy selector", contract: "preview_v02_migration.input", base: migration(false), valid: true, mutate: func(v map[string]any) {
			v["citation_mappings"] = []any{map[string]any{"path": "alpha.md", "entries": []any{map[string]any{"legacy_number": 1, "source_id": "source"}}}}
		}},
		{name: "migration/citation dual selector", contract: "preview_v02_migration.input", base: migration(false), valid: true, mutate: func(v map[string]any) {
			v["citation_mappings"] = []any{map[string]any{"path": "alpha.md", "entries": []any{map[string]any{"legacy_number": 1, "legacy_entry": "x", "source_id": "source"}}}}
		}},
		{name: "migration/apply missing source", contract: "apply_v02_migration.input", base: migration(true), mutate: func(v map[string]any) { delete(v, "expected_source") }},
		{name: "migration/apply target zero proof", contract: "apply_v02_migration.input", base: migration(true), valid: true},
		{name: "migration/apply transition proof", contract: "apply_v02_migration.input", valid: true, base: func() map[string]any {
			return migrationPresenceTransitionApply("/schema-only", migrationPresenceLegacySource())
		}},
		{name: "migration/proof missing reads", contract: "apply_v02_migration.input", base: func() map[string]any {
			return migrationPresenceTransitionApply("/schema-only", migrationPresenceLegacySource())
		}, mutate: func(v map[string]any) { delete(v["proof"].(map[string]any), "reads") }},
		{name: "migration/proof path plus one", contract: "apply_v02_migration.input", base: func() map[string]any {
			return migrationPresenceTransitionApply("/schema-only", migrationPresenceLegacySource())
		}, mutate: func(v map[string]any) { v["proof"].(map[string]any)["reads"] = []any{strings.Repeat("p", 4097)} }},
		{name: "migration/proof explicit arrays", contract: "apply_v02_migration.input", valid: true, base: func() map[string]any {
			return migrationPresenceTransitionApply("/schema-only", migrationPresenceLegacySource())
		}},
		{name: "migration/source impossible tuple", contract: "apply_v02_migration.input", base: migration(true), mutate: func(v map[string]any) { v["expected_source"].(map[string]any)["to_version"] = "0.1" }},
		{name: "digest control", contract: "apply_concept_patch.input", base: patch(true), valid: true, mutate: func(v map[string]any) { v["expected_revision"] = revision }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			value := row.base()
			if row.mutate != nil {
				row.mutate(value)
			}
			err := validateContract(row.contract, value)
			if (err == nil) != row.valid {
				t.Fatalf("valid=%t, want %t: %v; value=%#v", err == nil, row.valid, err, value)
			}
			if !row.valid {
				tool := strings.TrimSuffix(row.contract, ".input")
				calls := 0
				result := callHandler(t, contractToolHandler(tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					calls++
					return mcp.NewToolResultText("unreachable"), nil
				}), value)
				assertSchemaErrorEnvelope(t, result, map[bool]string{true: "resource_limit", false: "schema_validation"}[contractLimitViolation(err)])
				if calls != 0 {
					t.Fatalf("invalid row reached handler")
				}
			}
		})
	}
}

type outputStateRow struct {
	name     string
	tool     string
	value    any
	mutate   func(map[string]any)
	accepted bool
}

func TestOutputStateMatrix(t *testing.T) {
	fixtures := outputStateContractFixtures(t)
	baseRows := []outputStateRow{
		{name: "patch preview applicable", tool: "preview_concept_patch", value: fixtures.patchApplicable, accepted: true},
		{name: "patch preview noop", tool: "preview_concept_patch", value: fixtures.patchNoop, accepted: true},
		{name: "patch preview rejected", tool: "preview_concept_patch", value: fixtures.patchRejected, accepted: true},
		{name: "patch apply applied", tool: "apply_concept_patch", value: fixtures.patchApplied, accepted: true},
		{name: "patch apply noop", tool: "apply_concept_patch", value: fixtures.patchApplyNoop, accepted: true},
		{name: "patch apply rejected", tool: "apply_concept_patch", value: fixtures.patchApplyRejected, accepted: true},
		{name: "migration preview applicable", tool: "preview_v02_migration", value: fixtures.migrationApplicable, accepted: true},
		{name: "migration preview planned noop", tool: "preview_v02_migration", value: fixtures.migrationPlannedNoop, accepted: true},
		{name: "migration preview target noop", tool: "preview_v02_migration", value: fixtures.migrationTargetNoop, accepted: true},
		{name: "migration preview blocked", tool: "preview_v02_migration", value: fixtures.migrationBlocked, accepted: true},
		{name: "migration apply applied", tool: "apply_v02_migration", value: fixtures.migrationApplied, accepted: true},
		{name: "migration apply transition noop", tool: "apply_v02_migration", value: fixtures.migrationTransitionNoop, accepted: true},
		{name: "migration apply target noop", tool: "apply_v02_migration", value: fixtures.migrationApplyTargetNoop, accepted: true},
		{name: "migration apply rejected", tool: "apply_v02_migration", value: fixtures.migrationApplyRejected, accepted: true},
		{name: "write success", tool: "write_concept", value: fixtures.writeSuccess, accepted: true},
	}
	mutations := []struct {
		name    string
		forTool func(string) bool
		mutate  func(map[string]any)
	}{
		{name: "unknown status", forTool: func(string) bool { return true }, mutate: func(v map[string]any) { v["status"] = "impossible" }},
		{name: "unexpected receipt", forTool: func(tool string) bool { return tool != "write_concept" }, mutate: func(v map[string]any) { v["receipt"] = map[string]any{} }},
		{name: "missing changed paths", forTool: func(tool string) bool { return strings.HasPrefix(tool, "apply_") }, mutate: func(v map[string]any) { delete(v, "changed_paths") }},
		{name: "duplicate affected path", forTool: func(tool string) bool { return strings.HasPrefix(tool, "preview_") }, mutate: func(v map[string]any) {
			if _, ok := v["affected_paths"]; ok {
				v["affected_paths"] = []any{"a.md", "a.md"}
			} else {
				v["status"] = "impossible"
			}
		}},
		{name: "source tuple drift", forTool: func(tool string) bool { return strings.Contains(tool, "migration") }, mutate: func(v map[string]any) { outputStateSource(v)["to_version"] = "0.1" }},
		{name: "proof required shape", forTool: func(tool string) bool { return tool == "preview_v02_migration" }, mutate: func(v map[string]any) {
			if proof, ok := v["proof"].(map[string]any); ok {
				delete(proof, "reads")
			} else {
				v["proof"] = map[string]any{}
			}
		}},
	}
	rows := append([]outputStateRow(nil), baseRows...)
	for _, base := range baseRows {
		for _, mutation := range mutations {
			if mutation.forTool(base.tool) {
				rows = append(rows, outputStateRow{name: base.name + "/" + mutation.name, tool: base.tool, value: base.value, mutate: mutation.mutate})
			}
		}
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			value := outputStateMap(t, row.value)
			if row.mutate != nil {
				row.mutate(value)
			}
			result := mcp.NewToolResultStructured(value, "")
			accepted := enforceContractResult(row.tool, result) == result
			if accepted != row.accepted {
				t.Fatalf("accepted=%t, want %t: %#v", accepted, row.accepted, value)
			}
		})
	}
	errorResult := stableToolError("schema_validation", "rejected", false)
	assertSchemaErrorEnvelope(t, errorResult, "schema_validation")
}

func TestStableErrorCatalogMatrix(t *testing.T) {
	// The wire schema intentionally stays extensible; this table pins the
	// adapter's currently published codes. Diagnostic-code completeness remains
	// owned by TestDiagnosticCodesAreNonEmptyAcrossSchemasAndProjections.
	codes := []string{
		"concept_not_found", "invalid_projection", "invalid_request",
		"invalid_revision", "invalid_yaml_graph", "migration_blocked",
		"operation_cancelled", "operation_rejected", "plan_mismatch",
		"resource_limit", "revision_conflict", "schema_validation",
		"source_conflict", "unsupported_migration_source",
	}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			result := stableToolError(code, "catalog contract", false)
			assertSchemaErrorEnvelope(t, result, code)
			envelope := result.StructuredContent.(errorEnvelope)
			if envelope.Message != "catalog contract" || envelope.Retryable {
				t.Fatalf("stable envelope = %#v", envelope)
			}
		})
	}
}

// migrationSchemaTemplateFS is the root-only side of the canonical migration
// schema materializer. Production continues to advertise only checked-in files.
//
//go:embed contracts/*v02_migration.*.template.json
var migrationSchemaTemplateFS embed.FS

type migrationSchemaMaterialization struct {
	contract     string
	templateFile string
	definitions  []string
}

var migrationSchemaMaterializations = []migrationSchemaMaterialization{
	{contract: "preview_v02_migration.input", templateFile: "contracts/preview_v02_migration.input.template.json", definitions: []string{"computation_contract", "citation_mappings", "computations"}},
	{contract: "apply_v02_migration.input", templateFile: "contracts/apply_v02_migration.input.template.json", definitions: []string{"computation_contract", "citation_mappings", "computations", "plan_proof", "source_resolution", "applicable_v01_source_resolution", "applicable_target_source_resolution", "applicable_source_resolution"}},
	{contract: "preview_v02_migration.output", templateFile: "contracts/preview_v02_migration.output.template.json", definitions: []string{"output_path", "wire_diagnostic", "wire_diagnostics", "non_error_wire_diagnostics", "plan_proof", "source_resolution", "applicable_v01_source_resolution", "applicable_target_source_resolution", "applicable_source_resolution", "blocked_source_resolution", "preview_blocked_source_resolution"}},
	{contract: "apply_v02_migration.output", templateFile: "contracts/apply_v02_migration.output.template.json", definitions: []string{"output_path", "wire_diagnostic", "wire_diagnostics", "non_error_wire_diagnostics", "error_wire_diagnostics", "source_resolution", "applicable_v01_source_resolution", "applicable_target_source_resolution"}},
}

func TestMigrationSchemaMaterializationMatrix(t *testing.T) {
	shared := decodeSharedContractSchema(t, "migration_v02.shared")
	for _, materialization := range migrationSchemaMaterializations {
		t.Run(materialization.contract, func(t *testing.T) {
			got := decodeSharedContractSchema(t, materialization.contract)
			want := materializeMigrationSchema(t, shared, materialization)
			if !bytes.Equal(canonicalContractBytes(t, got), canonicalContractBytes(t, want)) {
				t.Fatalf("materialized contract drift")
			}
		})
	}
	for _, definition := range []string{"computation_contract", "source_resolution", "plan_proof", "output_path", "wire_diagnostic", "wire_diagnostics", "non_error_wire_diagnostics", "error_wire_diagnostics"} {
		t.Run("drift/"+definition, func(t *testing.T) {
			mutated := deepCloneSchemaObject(t, shared)
			schemaObjectAt(t, mutated, "$defs", definition)["description"] = "drift"
			for _, materialization := range migrationSchemaMaterializations {
				got := decodeSharedContractSchema(t, materialization.contract)
				drift := !reflect.DeepEqual(got, materializeMigrationSchema(t, mutated, materialization))
				if drift != slices.Contains(materialization.definitions, definition) {
					t.Fatalf("%s drift=%t", materialization.contract, drift)
				}
			}
		})
	}
}

func canonicalContractBytes(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal canonical contract: %v", err)
	}
	return data
}

func materializeMigrationSchema(t *testing.T, shared map[string]any, materialization migrationSchemaMaterialization) map[string]any {
	t.Helper()
	data, err := migrationSchemaTemplateFS.ReadFile(materialization.templateFile)
	if err != nil {
		t.Fatalf("ReadFile template: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Unmarshal template: %v", err)
	}
	sharedDefinitions := schemaObjectAt(t, shared, "$defs")
	definitions := make(map[string]any, len(materialization.definitions))
	for _, name := range materialization.definitions {
		definition, ok := sharedDefinitions[name]
		if !ok {
			t.Fatalf("shared definition %q missing", name)
		}
		definitions[name] = definition
	}
	root["$defs"] = definitions
	return root
}

func deepCloneSchemaObject(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func compileContractSchema(t *testing.T, name string, data []byte) {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(name, document); err != nil {
		t.Fatalf("add %s: %v", name, err)
	}
	if _, err := compiler.Compile(name); err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
}

func validateContractValue(t *testing.T, name string, value any) {
	t.Helper()
	if err := validateContract(name, value); err != nil {
		t.Fatalf("%s rejected value: %v", name, err)
	}
}

func assertSchemaErrorEnvelope(t *testing.T, result *mcp.CallToolResult, code string) {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("schema failure returned success: %#v", result)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != code {
		t.Fatalf("error code = %q, want %q", envelope.Code, code)
	}
	validateContractValue(t, "error", envelope)
}

func cloneArguments(input map[string]any) map[string]any {
	data, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		panic(err)
	}
	return clone
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type staticBundleSource struct {
	paths []string
	data  []byte
}

func (s staticBundleSource) Paths(context.Context) ([]string, error) {
	return append([]string(nil), s.paths...), nil
}
func (s staticBundleSource) ReadFile(context.Context, string) ([]byte, error) { return s.data, nil }

var _ bundle.Source = staticBundleSource{}

func testMigrationPlanProofDTO(base, result, requestDigest string) *migrationPlanProofDTO {
	return &migrationPlanProofDTO{
		FormatVersion: mutation.MigrationPlanProofFormatVersion, RequestDigest: requestDigest,
		ResolutionDigest: requestDigest, BaseRevision: base, ResultRevision: result,
		Reads: []string{}, Writes: []migrationPlanWriteDTO{}, Deletes: []string{},
		Renames: []migrationRenameDTO{}, AffectedRefs: []string{}, ReverseImpact: []string{},
		ChangedFiles: []migrationFileChangeDTO{}, ChangedRefs: []string{},
	}
}

func testMigrationSourceValue(resolved, transition string) map[string]any {
	from, to := resolved, resolved
	if transition == string(mutation.MigrationTransitionV01ToV02) {
		from, to = mutation.MigrationVersionV01, mutation.MigrationVersionV02
	}
	return map[string]any{
		"requested_selector": mutation.MigrationSelectorAuto, "declaration_present": true,
		"declaration_valid": true, "declaration_raw": resolved, "declared_version": resolved,
		"resolved_source": resolved, "resolution_source": string(mutation.MigrationResolutionDeclared),
		"from_version": from, "to_version": to, "transition": transition,
		"candidates": []any{}, "blockers": []any{},
	}
}

func nestedYAMLValue(depth int) string {
	var builder strings.Builder
	for index := range depth {
		builder.WriteString(strings.Repeat("  ", index))
		builder.WriteString("node:\n")
	}
	return builder.String()
}

func patchContractArguments(root string, apply bool) map[string]any {
	arguments := map[string]any{
		"bundle_path": root, "actor": "mcp:contract-matrix",
		"operations": []any{map[string]any{"kind": "set_generated", "concept_id": "alpha", "generated": map[string]any{"by": "human:contract"}}},
	}
	if apply {
		arguments["expected_revision"] = presenceDigest("f")
		arguments["expected_plan_digest"] = presenceDigest("9")
	}
	return arguments
}

func migrationContractArguments(root string, apply bool) map[string]any {
	arguments := map[string]any{"bundle_path": root, "from": "auto", "timestamp_policy": "preserve", "citation_mappings": []any{}, "computations": []any{}}
	if apply {
		arguments["expected_source"] = testMigrationSourceValue(mutation.MigrationVersionV02, string(mutation.MigrationTransitionTargetNoop))
	}
	return arguments
}
