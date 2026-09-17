package mcpserver

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSharedVersionSchemasMatchInlineVersionEnvelopes(t *testing.T) {
	// Arrange.
	common := decodeSharedContractSchema(t, "common")
	standalone := decodeSharedContractSchema(t, "version")
	want := schemaObjectAt(t, common, "$defs", "version")
	delete(standalone, "$schema")
	delete(standalone, "$id")

	// Act and assert.
	if !reflect.DeepEqual(standalone, want) {
		t.Fatalf("version schema differs from common.$defs.version:\nstandalone=%#v\ncommon=%#v", standalone, want)
	}
	for _, name := range []string{"list_concepts.output", "read_concept.output", "validate_bundle.output"} {
		t.Run(name, func(t *testing.T) {
			inline := schemaObjectAt(t, decodeSharedContractSchema(t, name), "properties", "version")
			if !reflect.DeepEqual(inline, want) {
				t.Fatalf("%s version envelope differs from shared schema:\ninline=%#v\nshared=%#v", name, inline, want)
			}
		})
	}
}

func TestStandaloneSharedSchemasCompile(t *testing.T) {
	// Arrange, act, and assert.
	for _, name := range []string{"common", "version"} {
		t.Run(name, func(t *testing.T) {
			data := mustContractSchema(t, name)
			document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("UnmarshalJSON(%s) error = %v", name, err)
			}
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource(name, document); err != nil {
				t.Fatalf("AddResource(%s) error = %v", name, err)
			}
			if _, err := compiler.Compile(name); err != nil {
				t.Fatalf("Compile(%s) error = %v", name, err)
			}
		})
	}
}

func decodeSharedContractSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(mustContractSchema(t, name), &schema); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", name, err)
	}
	return schema
}

func schemaObjectAt(t *testing.T, schema map[string]any, path ...string) map[string]any {
	t.Helper()
	current := schema
	for _, segment := range path {
		value, ok := current[segment]
		if !ok {
			t.Fatalf("schema path %v is missing segment %q", path, segment)
		}
		current, ok = value.(map[string]any)
		if !ok {
			t.Fatalf("schema path %v segment %q is %T, want object", path, segment, value)
		}
	}
	return current
}
