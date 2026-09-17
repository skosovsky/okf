package mcpserver

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// contractFS contains the exact schemas advertised over MCP. Schemas are
// checked in so changes are reviewable independently from Go projections.
//
//go:embed contracts/*.schema.json
var contractFS embed.FS

var compiledContracts sync.Map

type contractSchemaLoader func(string) (json.RawMessage, error)

func contractSchema(name string) (json.RawMessage, error) {
	return contractSchemaFromFS(contractFS, name)
}

func contractSchemaFromFS(contractFiles fs.FS, name string) (json.RawMessage, error) {
	filename := path.Join("contracts", name+".schema.json")
	data, err := fs.ReadFile(contractFiles, filename)
	if err != nil {
		return nil, fmt.Errorf("read contract %s: %w", filename, err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("invalid contract %s: %w", filename, err)
	}
	return json.RawMessage(data), nil
}

func schemaTool(
	name string,
	description string,
	readOnly bool,
	load contractSchemaLoader,
) (mcp.Tool, error) {
	inputSchema, err := load(name + ".input")
	if err != nil {
		return mcp.Tool{}, fmt.Errorf("load input schema for %s: %w", name, err)
	}
	outputSchema, err := load(name + ".output")
	if err != nil {
		return mcp.Tool{}, fmt.Errorf("load output schema for %s: %w", name, err)
	}
	tool := mcp.NewTool(name)
	tool.Description = description
	// A raw checked-in schema and the mcp-go generated schema are mutually
	// exclusive. Clear the generated object before installing raw contracts.
	tool.InputSchema = mcp.ToolInputSchema{}
	tool.RawInputSchema = inputSchema
	tool.RawOutputSchema = outputSchema
	tool.Annotations.ReadOnlyHint = mcp.ToBoolPtr(readOnly)
	tool.Annotations.DestructiveHint = mcp.ToBoolPtr(!readOnly)
	tool.Annotations.OpenWorldHint = mcp.ToBoolPtr(false)
	return tool, nil
}

func validateContract(name string, value any) error {
	schema, err := compiledContract(name)
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal contract value: %w", err)
	}
	decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode contract value: %w", err)
	}
	return schema.Validate(decoded)
}

func compiledContract(name string) (*jsonschema.Schema, error) {
	if cached, ok := compiledContracts.Load(name); ok {
		return cached.(*jsonschema.Schema), nil
	}
	data, err := contractSchema(name)
	if err != nil {
		return nil, err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode %s contract: %w", name, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(name, document); err != nil {
		return nil, fmt.Errorf("add %s contract: %w", name, err)
	}
	schema, err := compiler.Compile(name)
	if err != nil {
		return nil, fmt.Errorf("compile %s contract: %w", name, err)
	}
	actual, _ := compiledContracts.LoadOrStore(name, schema)
	return actual.(*jsonschema.Schema), nil
}

func contractLimitViolation(err error) bool {
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return false
	}
	return validationHasLimit(validation)
}

func validationHasLimit(validation *jsonschema.ValidationError) bool {
	if validation == nil {
		return false
	}
	if validation.ErrorKind != nil {
		path := validation.ErrorKind.KeywordPath()
		if len(path) != 0 {
			switch path[len(path)-1] {
			case "maxItems", "maxLength", "maxProperties", "maximum":
				return true
			}
		}
	}
	for _, cause := range validation.Causes {
		if validationHasLimit(cause) {
			return true
		}
	}
	return false
}
