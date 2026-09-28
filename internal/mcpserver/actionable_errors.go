package mcpserver

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

const maxInputErrorDiagnostics = 8

// inputContractError reports only the schema location and trusted guidance.
// ValidationError.Error() contains instance values and must never reach the wire.
func inputContractError(contractName, code, message string, err error) *mcp.CallToolResult {
	result := stableToolError(code, message, false)
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return result
	}
	diagnostics := make([]wireDiagnostic, 0, maxInputErrorDiagnostics)
	advertised := advertisedInputFields(contractName)
	seen := make(map[string]bool)
	var visit func(*jsonschema.ValidationError)
	visit = func(node *jsonschema.ValidationError) {
		if node == nil || len(diagnostics) >= maxInputErrorDiagnostics {
			return
		}
		if node.ErrorKind != nil {
			field := safeInputField(node.InstanceLocation, advertised.all)
			if required, ok := node.ErrorKind.(*kind.Required); ok {
				for _, missing := range required.Missing {
					if len(diagnostics) >= maxInputErrorDiagnostics {
						break
					}
					if _, ok := advertised.all[missing]; ok {
						path := joinInputField(field, missing)
						appendInputDiagnostic(&diagnostics, seen, contractName, path, "required", node.ErrorKind, advertisedFieldSchema(path, advertised))
					}
				}
			} else {
				keyword := "schema"
				if parts := node.ErrorKind.KeywordPath(); len(parts) > 0 {
					keyword = parts[len(parts)-1]
				}
				if keyword != "schema" && keyword != "group" &&
					keyword != "allOf" && keyword != "anyOf" && keyword != "oneOf" {
					appendInputDiagnostic(&diagnostics, seen, contractName, field, keyword, node.ErrorKind, advertisedFieldSchema(field, advertised))
				}
			}
		}
		for _, cause := range node.Causes {
			visit(cause)
		}
	}
	visit(validation)
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Field == diagnostics[j].Field {
			return diagnostics[i].Code < diagnostics[j].Code
		}
		return diagnostics[i].Field < diagnostics[j].Field
	})
	envelope := result.StructuredContent.(errorEnvelope)
	envelope.Diagnostics = diagnostics
	result.StructuredContent = envelope
	return result
}

func appendInputDiagnostic(out *[]wireDiagnostic, seen map[string]bool, contractName, field, keyword string, problem jsonschema.ErrorKind, schema map[string]any) {
	key := field + ":" + keyword
	if seen[key] {
		return
	}
	seen[key] = true
	*out = append(*out, wireDiagnostic{
		Code: "invalid_input", Severity: "ERROR", Field: field,
		Message: inputGuidance(contractName, field, keyword, problem, schema),
	})
}

func safeInputField(parts []string, allowed map[string]map[string]any) string {
	if len(parts) == 0 {
		return "arguments"
	}
	if len(parts) > 8 {
		return "arguments"
	}
	for _, part := range parts {
		if _, ok := allowed[part]; !ok && !decimalIndex(part) {
			return "arguments"
		}
	}
	return strings.Join(parts, ".")
}

func decimalIndex(part string) bool {
	if len(part) == 0 || len(part) > 6 {
		return false
	}
	for _, ch := range part {
		if ch >= '0' && ch <= '9' {
			continue
		}
		return false
	}
	return true
}

type advertisedInputSchema struct {
	all  map[string]map[string]any
	root map[string]map[string]any
}

func advertisedInputFields(name string) advertisedInputSchema {
	advertised := advertisedInputSchema{
		all:  make(map[string]map[string]any),
		root: make(map[string]map[string]any),
	}
	data, err := contractSchema(name)
	if err != nil {
		return advertised
	}
	var document any
	if json.Unmarshal(data, &document) != nil {
		return advertised
	}
	if top, ok := document.(map[string]any); ok {
		if properties, ok := top["properties"].(map[string]any); ok {
			for key, property := range properties {
				if entry, ok := property.(map[string]any); ok {
					advertised.root[key] = entry
				}
			}
		}
	}
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if properties, ok := node["properties"].(map[string]any); ok {
				for key, property := range properties {
					if _, duplicate := advertised.all[key]; duplicate {
						// A flat lookup cannot identify which branch supplied a
						// repeated name. Keep the name for path redaction, but
						// withhold examples so another field cannot contaminate it.
						advertised.all[key] = nil
					} else if entry, ok := property.(map[string]any); ok {
						advertised.all[key] = entry
					}
				}
			}
			for _, child := range node {
				visit(child)
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(document)
	return advertised
}

func advertisedFieldSchema(field string, advertised advertisedInputSchema) map[string]any {
	if !strings.Contains(field, ".") {
		return advertised.root[field]
	}
	parts := strings.Split(field, ".")
	return advertised.all[parts[len(parts)-1]]
}

func joinInputField(parent, child string) string {
	if parent == "arguments" {
		return child
	}
	return parent + "." + child
}

func inputGuidance(contractName, field, keyword string, problem jsonschema.ErrorKind, schema map[string]any) string {
	switch {
	case field == "temporal_profile":
		return "Use date-3fcbb9f or instant-0b87c52; match as_of to that profile."
	case field == "as_of" || strings.HasSuffix(field, ".as_of"):
		return "Use YYYY-MM-DD with date-3fcbb9f, or RFC3339 with an explicit UTC offset with instant-0b87c52; do not guess a timezone."
	case field == "limit":
		return "Use an integer from 1 to 100, for example 20; narrow query if total exceeds the limit."
	case field == "query":
		return "Use nonblank literal search text up to 512 characters, for example cache invalidation."
	case field == "bundle_path":
		return "Use the absolute path of an accessible OKF bundle root."
	case field == "expected_revision" || field == "base_revision" || field == "plan_digest" || field == "expected_plan_digest":
		return "Use the exact revision or digest returned by a fresh preview."
	case keyword == "required":
		return requiredInputGuidance(contractName, field, schema)
	case keyword == "additionalProperties":
		return "Remove unsupported fields and use only advertised input properties."
	case keyword == "type":
		if typed, ok := problem.(*kind.Type); ok && len(typed.Want) > 0 {
			return "Expected JSON " + strings.Join(typed.Want, " or ") + trustedExample(schema) + "; strings are not coerced to numbers or booleans."
		}
		return "Use the JSON type declared for this field; strings are not coerced to numbers or booleans."
	case keyword == "enum" || keyword == "const":
		if enumerated, ok := problem.(*kind.Enum); ok {
			if options := trustedChoices(enumerated.Want); options != "" {
				return "Use one of the advertised values: " + options + "."
			}
		}
		if constant, ok := problem.(*kind.Const); ok {
			if option := trustedChoices([]any{constant.Want}); option != "" {
				return "Use the advertised value " + option + "."
			}
		}
		return "Use one of the values declared for this field in the advertised input schema."
	case keyword == "maxLength" || keyword == "maxItems" || keyword == "maximum" || keyword == "maxProperties":
		return "Reduce this field to the advertised limit; narrow the request before retrying."
	case keyword == "format" || keyword == "pattern":
		if format, ok := problem.(*kind.Format); ok {
			switch format.Want {
			case "date":
				return "Use a date in YYYY-MM-DD format, for example 2026-01-02."
			case "date-time":
				return "Use RFC3339 date-time with an explicit UTC offset, for example 2026-01-02T03:04:05Z."
			default:
				return "Use the advertised " + format.Want + " format" + trustedExample(schema) + "."
			}
		}
		return "Use the format declared for this field in the advertised input schema."
	default:
		return "Check this field against the advertised input schema, then retry with corrected arguments."
	}
}

func requiredInputGuidance(contractName, field string, schema map[string]any) string {
	// A nil schema means this property name appears in multiple branches.
	// Keep the field path, but never borrow an example from another branch.
	if schema == nil {
		return "Supply this required field using the advertised input schema."
	}
	if values, ok := schema["enum"].([]any); ok {
		if choices := trustedChoices(values); choices != "" {
			return "Supply this required field using an advertised value: " + choices + "."
		}
	}
	if value, ok := schema["const"]; ok {
		if choice := trustedChoices([]any{value}); choice != "" {
			return "Supply this required field using the advertised value " + choice + "."
		}
	}
	typeName, _ := schema["type"].(string)
	if typeName == "" {
		return "Supply this required field using the advertised input schema."
	}
	message := "Supply this required field as JSON " + typeName
	if field == "operations" && typeName == "array" {
		if example := trustedOperationsExample(contractName, schema); example != "" {
			return message + "; for example " + example + ". Replace concept_id with an existing concept ID."
		}
	}
	formatExample := ""
	if format, ok := schema["format"].(string); ok {
		switch format {
		case "date":
			message += " in YYYY-MM-DD format"
			formatExample = "2026-01-02"
		case "date-time":
			message += " in RFC3339 date-time format with an explicit UTC offset"
			formatExample = "2026-01-02T03:04:05Z"
		}
	}
	if example := trustedExample(schema); example != "" {
		return message + example + "."
	}
	if formatExample != "" {
		return message + "; for example " + formatExample + "."
	}
	return message + "."
}

// A structured example is shown only for the root operations field and only
// after the complete request validates against the checked-in input contract.
func trustedOperationsExample(contractName string, schema map[string]any) string {
	if contractName != "preview_concept_patch.input" && contractName != "apply_concept_patch.input" {
		return ""
	}
	examples, ok := schema["examples"].([]any)
	if !ok || len(examples) == 0 {
		return ""
	}
	operations, ok := examples[0].([]any)
	if !ok || len(operations) == 0 {
		return ""
	}
	encoded, err := json.Marshal(operations)
	if err != nil || len(encoded) > 256 {
		return ""
	}
	request := map[string]any{
		"bundle_path": "/workspace/knowledge",
		"actor":       "human:reviewer",
		"operations":  operations,
	}
	if contractName == "apply_concept_patch.input" {
		request["expected_revision"] = "sha256:" + strings.Repeat("a", 64)
		request["expected_plan_digest"] = "sha256:" + strings.Repeat("b", 64)
	}
	if validateContract(contractName, request) != nil {
		return ""
	}
	return string(encoded)
}

func trustedExample(schema map[string]any) string {
	if examples, ok := schema["examples"].([]any); ok && len(examples) > 0 {
		if encoded := trustedChoices(examples[:1]); encoded != "" {
			return "; for example " + encoded
		}
	}
	return ""
}

func trustedChoices(values []any) string {
	if len(values) == 0 || len(values) > 8 {
		return ""
	}
	options := make([]string, 0, len(values))
	for _, value := range values {
		switch value.(type) {
		case string, bool, json.Number, float64:
		default:
			return ""
		}
		encoded, err := json.Marshal(value)
		if err != nil || len(encoded) > 64 {
			return ""
		}
		options = append(options, string(encoded))
	}
	joined := strings.Join(options, ", ")
	if len(joined) > 240 {
		return ""
	}
	return joined
}

func stableToolErrorWithRecovery(code, message string, retryable bool) *mcp.CallToolResult {
	result := stableToolError(code, message, retryable)
	envelope := result.StructuredContent.(errorEnvelope)
	envelope.Diagnostics = recoveryDiagnostics(code, message)
	result.StructuredContent = envelope
	return result
}

func withRecoveryStep(result *mcp.CallToolResult, field, advice string) *mcp.CallToolResult {
	envelope := result.StructuredContent.(errorEnvelope)
	envelope.Diagnostics = []wireDiagnostic{{
		Code: "recovery_step", Severity: "INFO", Field: field, Message: advice,
	}}
	result.StructuredContent = envelope
	return result
}

func recoveryDiagnostics(code, message string) []wireDiagnostic {
	field, advice := "", ""
	switch code {
	case "revision_conflict":
		field, advice = "expected_revision", "Bundle revision changed. Run the matching preview again against current bundle data; review its new revision and plan before apply."
	case "plan_mismatch":
		field, advice = "expected_plan_digest", "Run the matching preview again against current bundle data; review its new plan digest before apply."
	case "concept_not_found":
		field, advice = "concept_id", "Use list_concepts or search_concepts to find a current concept ID."
	case "unsupported_migration_source", "migration_blocked":
		field, advice = "source", "Inspect preview blockers and manual actions, resolve them explicitly, then preview again."
	case "resource_limit":
		if strings.Contains(message, "search concepts") {
			field, advice = "query", "Use a narrower literal query; repeating the same broad query will hit the same limit."
		} else {
			advice = "Reduce the requested scope or inspect a smaller bundle subset before retrying."
		}
	}
	if advice == "" {
		return []wireDiagnostic{}
	}
	return []wireDiagnostic{{Code: "recovery_step", Severity: "INFO", Field: field, Message: advice}}
}
