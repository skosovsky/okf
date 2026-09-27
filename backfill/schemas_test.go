package backfill

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestBackfillWireSchemasCompileAndValidateFixtures(t *testing.T) {
	// Arrange.
	compiler := jsonschema.NewCompiler()
	const prefix = "https://github.com/skosovsky/okf/backfill/schemas/"
	names := []string{"event", "manifest", "candidate", "analysis", "checkpoint", "coverage"}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("schemas", name+".schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s decode: %v", name, err)
		}
		if err := compiler.AddResource(prefix+name+".schema.json", doc); err != nil {
			t.Fatalf("%s resource: %v", name, err)
		}
	}
	compiled := map[string]*jsonschema.Schema{}
	for _, name := range names {
		s, err := compiler.Compile(prefix + name + ".schema.json")
		if err != nil {
			t.Fatalf("%s compile: %v", name, err)
		}
		compiled[name] = s
	}
	m, a := testManifest(t)
	coverage, _, err := ValidateAnalysis(m, a)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{"event": m.Events[0], "manifest": m, "candidate": a.Candidates[0], "analysis": a, "coverage": coverage}
	// Act and assert.
	for name, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var wire any
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if err := compiled[name].Validate(wire); err != nil {
			t.Fatalf("%s validation: %v", name, err)
		}
	}
}
