package setup

import (
	"context"
	"encoding/json"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"os"
	"testing"
)

func TestJSONContracts(t *testing.T) {
	// Arrange.
	opts := fixture(t, map[string]string{"a.md": "# Body"})
	plan, err := Preview(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plan", "request"} {
		t.Run(name, func(t *testing.T) {
			bytes, err := os.ReadFile("contracts/" + name + ".v1.schema.json")
			if err != nil {
				t.Fatal(err)
			}
			var document any
			if err = json.Unmarshal(bytes, &document); err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			if err = compiler.AddResource("schema.json", document); err != nil {
				t.Fatal(err)
			}
			schema, err := compiler.Compile("schema.json")
			if err != nil {
				t.Fatal(err)
			}
			var value any
			var input any = plan
			if name == "request" {
				input = map[string]any{"options": opts, "apply": true, "plan_digest": plan.Digest}
			}
			data, _ := json.Marshal(input)
			if err = json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			// Act.
			validationErr := schema.Validate(value)
			// Assert.
			if validationErr != nil {
				t.Fatal(validationErr)
			}
			if name == "request" {
				delete(value.(map[string]any), "plan_digest")
				if err = schema.Validate(value); err == nil {
					t.Fatal("apply without digest accepted")
				}
			}
		})
	}
}
