package agent

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.json
var schemaBytes []byte

var wireSchemas struct {
	sync.Once
	items map[string]*jsonschema.Schema
	err   error
}

func ValidateWire(kind string, raw []byte) error {
	wireSchemas.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
		if err != nil {
			wireSchemas.err = err
			return
		}
		compiler := jsonschema.NewCompiler()
		const uri = "https://github.com/skosovsky/okf/benchmark/agent/schema.json"
		if err := compiler.AddResource(uri, doc); err != nil {
			wireSchemas.err = err
			return
		}
		wireSchemas.items = map[string]*jsonschema.Schema{}
		for _, k := range []string{"row", "plan", "request", "observation", "report"} {
			s, err := compiler.Compile(uri + "#/$defs/" + k)
			if err != nil {
				wireSchemas.err = err
				return
			}
			wireSchemas.items[k] = s
		}
	})
	if wireSchemas.err != nil {
		return wireSchemas.err
	}
	s, ok := wireSchemas.items[kind]
	if !ok {
		return fmt.Errorf("unknown wire kind %q", kind)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	return s.Validate(value)
}
