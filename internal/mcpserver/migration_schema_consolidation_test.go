package mcpserver

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMigrationSchemasAreSelfContainedCanonicalMaterializations(t *testing.T) {
	// Arrange.
	shared := decodeSharedContractSchema(t, "migration_v02.shared")

	// Act and assert.
	compileContractSchema(
		t,
		"migration_v02.shared.schema.json",
		mustContractSchema(t, "migration_v02.shared"),
	)
	for _, materialization := range migrationSchemaMaterializations {
		t.Run(materialization.contract, func(t *testing.T) {
			got := decodeSharedContractSchema(t, materialization.contract)
			want := materializeMigrationSchema(t, shared, materialization)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf(
					"%s differs from its root template plus canonical shared definitions",
					materialization.contract,
				)
			}
			assertOnlyLocalSchemaReferences(t, got)
			compileContractSchema(
				t,
				materialization.contract+".advertised.json",
				mustContractSchema(t, materialization.contract),
			)
		})
	}
}

func TestMigrationSharedDefinitionDriftInvalidatesEveryConsumer(t *testing.T) {
	tests := []struct {
		name       string
		definition string
		mutate     func(map[string]any)
	}{
		{
			name: "computation contract", definition: "computation_contract",
			mutate: func(definition map[string]any) {
				schemaObjectAt(t, definition, "properties", "runtime")["maxLength"] = float64(4095)
			},
		},
		{
			name: "source resolution", definition: "source_resolution",
			mutate: func(definition map[string]any) {
				schemaObjectAt(t, definition, "properties", "requested_selector")["enum"] = []any{"auto"}
			},
		},
		{
			name: "plan proof", definition: "plan_proof",
			mutate: func(definition map[string]any) {
				schemaObjectAt(t, definition, "properties", "reads", "items")["maxLength"] = float64(4095)
			},
		},
		{
			name: "output path", definition: "output_path",
			mutate: func(definition map[string]any) {
				definition["minLength"] = float64(2)
			},
		},
		{
			name: "wire diagnostic", definition: "wire_diagnostic",
			mutate: func(definition map[string]any) {
				schemaObjectAt(t, definition, "properties", "severity")["enum"] = []any{"ERROR"}
			},
		},
		{
			name: "wire diagnostics", definition: "wire_diagnostics",
			mutate: func(definition map[string]any) {
				definition["maxItems"] = float64(9999)
			},
		},
		{
			name: "non-error wire diagnostics", definition: "non_error_wire_diagnostics",
			mutate: func(definition map[string]any) {
				delete(definition, "allOf")
				definition["type"] = "array"
			},
		},
		{
			name: "error wire diagnostics", definition: "error_wire_diagnostics",
			mutate: func(definition map[string]any) {
				delete(definition, "allOf")
				definition["type"] = "array"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shared := deepCloneSchemaObject(
				t,
				decodeSharedContractSchema(t, "migration_v02.shared"),
			)
			test.mutate(schemaObjectAt(t, shared, "$defs", test.definition))
			for _, materialization := range migrationSchemaMaterializations {
				t.Run(materialization.contract, func(t *testing.T) {
					mutated := materializeMigrationSchema(t, shared, materialization)
					checkedIn := decodeSharedContractSchema(t, materialization.contract)
					drifted := !reflect.DeepEqual(mutated, checkedIn)
					wantDrift := slices.Contains(materialization.definitions, test.definition)
					if drifted != wantDrift {
						t.Fatalf(
							"%s drift=%t, want %t for canonical %s mutation",
							materialization.contract,
							drifted,
							wantDrift,
							test.definition,
						)
					}
				})
			}
		})
	}
}

func assertOnlyLocalSchemaReferences(t *testing.T, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "$ref" {
				reference, ok := child.(string)
				if !ok || !strings.HasPrefix(reference, "#/$defs/") {
					t.Errorf("$ref = %#v, want a self-contained local definition", child)
				}
			}
			assertOnlyLocalSchemaReferences(t, child)
		}
	case []any:
		for _, child := range typed {
			assertOnlyLocalSchemaReferences(t, child)
		}
	}
}
