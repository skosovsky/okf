package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/skosovsky/okf/bundle"
)

func TestMCPInputErrorsGiveSafeRecoveryFields(t *testing.T) {
	root := t.TempDir()
	secret := "secret-value-never-echo"
	missingActor := patchContractArguments(root, false)
	delete(missingActor, "actor")
	missingOperations := patchContractArguments(root, false)
	delete(missingOperations, "operations")
	tests := []struct {
		name, tool, field, code string
		args                    map[string]any
		guidance                string
	}{
		{
			name: "profile enum", tool: "search_concepts", field: "temporal_profile", code: "schema_validation",
			args:     map[string]any{"bundle_path": root, "query": "cache", "temporal_profile": secret},
			guidance: "date-3fcbb9f",
		},
		{
			name: "temporal instant", tool: "search_concepts", field: "as_of", code: "schema_validation",
			args:     map[string]any{"bundle_path": root, "query": "cache", "temporal_profile": "instant-0b87c52", "as_of": "2026-09-27"},
			guidance: "explicit UTC offset",
		},
		{
			name: "wrong limit type", tool: "search_concepts", field: "limit", code: "schema_validation",
			args:     map[string]any{"bundle_path": root, "query": "cache", "limit": "10"},
			guidance: "integer",
		},
		{
			name: "missing query", tool: "search_concepts", field: "query", code: "schema_validation",
			args:     map[string]any{"bundle_path": root},
			guidance: "literal search text",
		},
		{
			name: "generic enum", tool: "get_neighbors", field: "direction", code: "schema_validation",
			args:     map[string]any{"bundle_path": root, "concept_id": "a", "direction": "sideways"},
			guidance: "\"in\"",
		},
		{
			name: "generic type", tool: "write_concept", field: "body", code: "schema_validation",
			args:     map[string]any{"bundle_path": root, "concept_id": "a", "frontmatter": "type: Note", "body": true},
			guidance: "Expected JSON string",
		},
		{
			name: "generic format", tool: "preview_v02_migration", field: "generated_at.0.at", code: "schema_validation",
			args: map[string]any{"bundle_path": root, "timestamp_policy": "preserve", "citation_mappings": []any{},
				"generated_at": []any{map[string]any{"concept_id": "a", "at": "not-a-date"}}},
			guidance: "2026-01-02T03:04:05Z",
		},
		{
			name: "required actor", tool: "preview_concept_patch", field: "actor", code: "schema_validation",
			args: missingActor, guidance: "JSON string; for example \"human:reviewer\"",
		},
		{
			name: "required operations", tool: "preview_concept_patch", field: "operations", code: "schema_validation",
			args: missingOperations, guidance: "JSON array",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: the next handler records whether invalid input passed validation.
			called := false
			handler := contractToolHandler(tc.tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				called = true
				return mcp.NewToolResultStructured(map[string]any{}, ""), nil
			})
			// Act.
			result := callHandler(t, handler, tc.args)
			// Assert.
			assertSchemaErrorEnvelope(t, result, tc.code)
			if called {
				t.Fatal("invalid input reached tool handler")
			}
			envelope := result.StructuredContent.(errorEnvelope)
			found := false
			for _, d := range envelope.Diagnostics {
				if d.Field == tc.field && strings.Contains(d.Message, tc.guidance) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s recovery diagnostic: %#v", tc.field, envelope.Diagnostics)
			}
			wire, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(wire), secret) || strings.Contains(string(wire), root) {
				t.Fatalf("error leaked request content: %s", wire)
			}
			if len(wire) > 4096 || len(envelope.Diagnostics) > maxInputErrorDiagnostics {
				t.Fatalf("error too large: %d bytes, %d diagnostics", len(wire), len(envelope.Diagnostics))
			}
		})
	}
}

func TestMCPRequiredOperationsExampleComesFromValidContract(t *testing.T) {
	for _, tc := range []struct {
		tool  string
		apply bool
	}{
		{tool: "preview_concept_patch"},
		{tool: "apply_concept_patch", apply: true},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			// Arrange: read the advertised example and omit the required field.
			data, err := contractSchema(tc.tool + ".input")
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Properties struct {
					Operations struct {
						Examples []any `json:"examples"`
					} `json:"operations"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatal(err)
			}
			if len(schema.Properties.Operations.Examples) != 1 {
				t.Fatalf("expected one operations example, got %#v", schema.Properties.Operations.Examples)
			}
			operations, ok := schema.Properties.Operations.Examples[0].([]any)
			if !ok || len(operations) != 1 {
				t.Fatalf("operations example must be a nonempty JSON array: %#v", schema.Properties.Operations.Examples[0])
			}
			encoded, err := json.Marshal(operations)
			if err != nil {
				t.Fatal(err)
			}
			arguments := patchContractArguments(t.TempDir(), tc.apply)
			delete(arguments, "operations")

			// Act: request guidance, then validate that exact advertised example.
			result := callHandler(t, contractToolHandler(tc.tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				t.Fatal("missing operations reached tool handler")
				return nil, nil
			}), arguments)
			arguments["operations"] = operations
			validationErr := validateContract(tc.tool+".input", arguments)

			// Assert: the hint is concrete, typed, and accepted by the same schema.
			assertSchemaErrorEnvelope(t, result, "schema_validation")
			if !hasInputHint(result, "operations", "JSON array; for example "+string(encoded)) {
				t.Fatalf("missing trusted operations example: %#v", result.StructuredContent)
			}
			if !strings.Contains(string(encoded), `"kind":"set_lifecycle"`) ||
				!strings.Contains(string(encoded), `"concept_id":"example"`) {
				t.Fatalf("expected typed operation with a replaceable concept ID: %s", encoded)
			}
			if validationErr != nil {
				t.Fatalf("advertised example violates input schema: %v", validationErr)
			}
		})
	}
}

func TestMCPAmbiguousSchemaPropertyWithholdsWrongExample(t *testing.T) {
	// Arrange: root from advertises "auto", while proof.renames[].from is a path.
	validation := &jsonschema.ValidationError{
		InstanceLocation: []string{"proof", "renames", "0", "from"},
		ErrorKind:        &kind.Type{Want: []string{"string"}},
	}
	// Act and assert across repeated schema walks (map iteration order varies).
	for i := 0; i < 20; i++ {
		result := inputContractError("apply_v02_migration.input", "schema_validation", "tool input does not match the advertised schema", validation)
		assertSchemaErrorEnvelope(t, result, "schema_validation")
		if !hasInputHint(result, "proof.renames.0.from", "Expected JSON string") {
			t.Fatalf("nested field guidance missing: %#v", result.StructuredContent)
		}
		required := &jsonschema.ValidationError{
			InstanceLocation: []string{"proof", "renames", "0"},
			ErrorKind:        &kind.Required{Missing: []string{"from"}},
		}
		requiredResult := inputContractError("apply_v02_migration.input", "schema_validation", "tool input does not match the advertised schema", required)
		if !hasInputHint(requiredResult, "proof.renames.0.from", "advertised input schema") {
			t.Fatalf("ambiguous required field borrowed an example: %#v", requiredResult.StructuredContent)
		}
		wire, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(wire), "auto") {
			t.Fatalf("root from example contaminated nested field: %s", wire)
		}
		requiredWire, err := json.Marshal(requiredResult)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(requiredWire), "auto") {
			t.Fatalf("root from example contaminated nested required field: %s", requiredWire)
		}
	}
}

func TestMCPRequiredDateTimeUsesTrustedFormat(t *testing.T) {
	// Arrange: generated_at[].at has a declared date-time format.
	validation := &jsonschema.ValidationError{
		InstanceLocation: []string{"generated_at", "0"},
		ErrorKind:        &kind.Required{Missing: []string{"at"}},
	}
	// Act.
	result := inputContractError("preview_v02_migration.input", "schema_validation", "tool input does not match the advertised schema", validation)
	// Assert.
	assertSchemaErrorEnvelope(t, result, "schema_validation")
	if !hasInputHint(result, "generated_at.0.at", "2026-01-02T03:04:05Z") {
		t.Fatalf("required date-time guidance is missing: %#v", result.StructuredContent)
	}
}

func TestMCPInputErrorRecoverySequences(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nCache invalidation.\n")
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	base := map[string]any{"bundle_path": root, "query": "cache"}
	// Act and assert: invalid enum identifies a trusted valid choice; that choice succeeds.
	wrongProfile := cloneArguments(base)
	wrongProfile["temporal_profile"] = "invalid-profile"
	invalidProfile := callMCPTool(t, srv, "search_concepts", wrongProfile)
	assertSchemaErrorEnvelope(t, invalidProfile, "schema_validation")
	if !hasInputHint(invalidProfile, "temporal_profile", "date-3fcbb9f") {
		t.Fatalf("missing profile recovery hint: %#v", invalidProfile.StructuredContent)
	}
	correctProfile := cloneArguments(base)
	correctProfile["temporal_profile"] = "date-3fcbb9f"
	profileResult := callMCPTool(t, srv, "search_concepts", correctProfile)
	if profileResult.IsError || profileResult.StructuredContent.(map[string]any)["total"] != float64(1) {
		t.Fatalf("corrected profile did not succeed: %#v", profileResult)
	}
	// Act and assert: an instant needs explicit offset; corrected input succeeds.
	wrongTime := cloneArguments(base)
	wrongTime["temporal_profile"] = "instant-0b87c52"
	wrongTime["as_of"] = "2026-09-27"
	invalidTime := callMCPTool(t, srv, "search_concepts", wrongTime)
	assertSchemaErrorEnvelope(t, invalidTime, "schema_validation")
	if !hasInputHint(invalidTime, "as_of", "explicit UTC offset") {
		t.Fatalf("missing temporal recovery hint: %#v", invalidTime.StructuredContent)
	}
	correctTime := cloneArguments(wrongTime)
	correctTime["as_of"] = "2026-09-27T10:00:00Z"
	timeResult := callMCPTool(t, srv, "search_concepts", correctTime)
	if timeResult.IsError || timeResult.StructuredContent.(map[string]any)["total"] != float64(1) {
		t.Fatalf("corrected temporal input did not succeed: %#v", timeResult)
	}
}

func hasInputHint(result *mcp.CallToolResult, field, fragment string) bool {
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return false
	}
	var envelope errorEnvelope
	if json.Unmarshal(data, &envelope) != nil {
		return false
	}
	for _, diagnostic := range envelope.Diagnostics {
		if diagnostic.Field == field && strings.Contains(diagnostic.Message, fragment) {
			return true
		}
	}
	return false
}

func TestInvalidMCPMutationIsZeroWriteWithActionableError(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nOriginal.\n")
	before := protocolTreeSnapshot(t, root)
	arguments := map[string]any{
		"bundle_path":      root,
		"actor":            "human:reviewer",
		"operations":       []any{map[string]any{"kind": "remove_concept", "concept_id": "a"}},
		"temporal_profile": "invalid-profile",
	}
	// Act.
	result := callHandler(t, contractToolHandler("apply_concept_patch", handleApplyConceptPatch), arguments)
	after := protocolTreeSnapshot(t, root)
	// Assert.
	assertSchemaErrorEnvelope(t, result, "schema_validation")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid mutation changed bundle tree: before=%#v after=%#v", before, after)
	}
	var found bool
	for _, diagnostic := range result.StructuredContent.(errorEnvelope).Diagnostics {
		if diagnostic.Field == "temporal_profile" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing temporal_profile diagnostic: %#v", result.StructuredContent)
	}
}

func TestMCPRecoveryDiagnosticsPreserveStableEnvelopeAndText(t *testing.T) {
	tests := []struct {
		code, message, field, advice string
		retryable                    bool
	}{
		{"revision_conflict", "bundle revision changed after preview", "expected_revision", "preview again", true},
		{"plan_mismatch", "expected_plan_digest does not match the rebuilt plan", "expected_plan_digest", "preview again", false},
		{"concept_not_found", "concept not found: missing", "concept_id", "search_concepts", false},
		{"resource_limit", "search concepts exceeds MCP resource limits", "query", "narrower literal query", false},
	}
	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			// Arrange/Act.
			result := stableToolErrorWithRecovery(tc.code, tc.message, tc.retryable)
			// Assert.
			if !result.IsError || resultText(t, result) != tc.message {
				t.Fatalf("tool error/text changed: %#v", result)
			}
			envelope := result.StructuredContent.(errorEnvelope)
			if envelope.Code != tc.code || envelope.Message != tc.message || envelope.Retryable != tc.retryable ||
				len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Field != tc.field ||
				!strings.Contains(envelope.Diagnostics[0].Message, tc.advice) {
				t.Fatalf("recovery envelope = %#v", envelope)
			}
			assertSchemaErrorEnvelope(t, result, tc.code)
		})
	}
}

func TestMCPHandlerExecutionErrorRemainsProtocolError(t *testing.T) {
	// Arrange: a valid request reaches the handler, which reports a transport failure.
	root := t.TempDir()
	want := errors.New("transport failed")
	handler := contractToolHandler("list_concepts", func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, want
	})
	// Act.
	result, err := handler(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Arguments: map[string]any{"bundle_path": root},
	}})
	// Assert.
	if result != nil || !errors.Is(err, want) {
		t.Fatalf("execution failure became tool result: result=%#v err=%v", result, err)
	}
}

func TestMCPErrorMessageBoundAndUnadvertisedFieldRedaction(t *testing.T) {
	// Arrange: malformed input uses an unadvertised key that resembles a secret.
	root := t.TempDir()
	secret := "private_key_never_project"
	args := map[string]any{"bundle_path": root, "query": "cache", secret: "hidden"}
	// Act.
	result := callHandler(t, contractToolHandler("search_concepts", handleSearchConcepts), args)
	bounded := stableToolError("operation_rejected", strings.Repeat("x", 4096), false)
	// Assert.
	assertSchemaErrorEnvelope(t, result, "schema_validation")
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), secret) || strings.Contains(string(wire), "hidden") {
		t.Fatalf("unadvertised field or value leaked: %s", wire)
	}
	if len(resultText(t, bounded)) > 512 {
		t.Fatalf("tool error message exceeded bound: %d", len(resultText(t, bounded)))
	}
	// A runtime canonical-ID rejection must not echo the rejected ID either.
	_, idErr := parseCanonicalConceptID("private://secret-token")
	if idErr == nil || strings.Contains(idErr.Error(), "secret-token") {
		t.Fatalf("canonical-ID error disclosed invalid value: %v", idErr)
	}
}

func TestMCPMalformedYAMLAliasDoesNotLeakOrWrite(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nOriginal.\n")
	before := protocolTreeSnapshot(t, root)
	secret := "private_anchor_token"
	arguments := map[string]any{
		"bundle_path": root, "concept_id": "new",
		"frontmatter": "type: Note\nx: *" + secret + "\n",
		"body":        "New.\n",
	}
	// Act.
	result := callHandler(t, contractToolHandler("write_concept", handleWriteConcept), arguments)
	after := protocolTreeSnapshot(t, root)
	// Assert.
	assertSchemaErrorEnvelope(t, result, "invalid_request")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("malformed YAML changed bundle tree: before=%#v after=%#v", before, after)
	}
	if strings.Contains(resultText(t, result), secret) || !hasInputHint(result, "frontmatter", "Check YAML syntax") {
		t.Fatalf("unsafe or unactionable YAML error: %#v", result)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), secret) || strings.Contains(string(wire), root) {
		t.Fatalf("YAML error leaked secret or absolute path: %s", wire)
	}
}

func TestMCPParseAndJSONErrorResultsAreBoundedAndRedacted(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	secret := "private_parse_token"
	parseError := bundle.ParseError{Path: root + "/a.md", Err: errors.New(secret)}
	oversized := make([]bundle.ParseError, maxMCPErrorDiagnostics+1)
	for i := range oversized {
		oversized[i] = parseError
	}
	// Act.
	ordinary := parseErrorsResult(root, []bundle.ParseError{parseError})
	tooMany := parseErrorsResult(root, oversized)
	largeDiagnostic := jsonErrorResult(parseErrorResponse{Status: "error", Diagnostics: []diagnosticDTO{{
		Code: "invalid_frontmatter", Severity: "error", File: "a.md", Message: strings.Repeat(secret, 100),
	}}})
	largeResult := jsonErrorResult(parseErrorResponse{Status: strings.Repeat("x", maxMCPErrorResultBytes+1)})
	// Assert.
	for _, result := range []*mcp.CallToolResult{ordinary, largeDiagnostic} {
		if result == nil || !result.IsError {
			t.Fatalf("expected bounded tool error: %#v", result)
		}
		wire, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(wire), secret) || strings.Contains(string(wire), root) || len(wire) > maxMCPErrorResultBytes {
			t.Fatalf("unsafe or unbounded error result: %d bytes: %s", len(wire), wire)
		}
	}
	assertSchemaErrorEnvelope(t, tooMany, "resource_limit")
	assertSchemaErrorEnvelope(t, largeResult, "resource_limit")
}

func TestMCPJSONErrorFinalEnvelopeBound(t *testing.T) {
	// Arrange: one copy of 64 diagnostics fits; MCP text plus structured
	// diagnostics exceeds the final wire budget.
	diagnostics := make([]diagnosticDTO, maxMCPErrorDiagnostics)
	for i := range diagnostics {
		diagnostics[i] = diagnosticDTO{
			Code: "invalid_frontmatter", Severity: "error",
			File: strings.Repeat("f", 150) + ".md", Message: strings.Repeat("m", 400),
		}
	}
	value := parseErrorResponse{Status: "error", Diagnostics: diagnostics}
	valueWire, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(valueWire) >= maxMCPErrorResultBytes {
		t.Fatalf("test setup exceeded pre-envelope budget: %d", len(valueWire))
	}
	// Act.
	result := jsonErrorResult(value)
	// Assert.
	assertSchemaErrorEnvelope(t, result, "resource_limit")
	finalWire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(finalWire) > maxMCPErrorResultBytes {
		t.Fatalf("fallback exceeded final wire budget: %d", len(finalWire))
	}
}

type manyConceptSource struct{ paths []string }

func (source manyConceptSource) Paths(context.Context) ([]string, error) {
	return append([]string(nil), source.paths...), nil
}

func (manyConceptSource) ReadFile(context.Context, string) ([]byte, error) {
	return []byte("---\ntype: Note\n---\nBody.\n"), nil
}

func TestMCPConceptCountLimitRequiresSmallerBundle(t *testing.T) {
	// Arrange: both queries run against the same bundle above the concept count limit.
	paths := make([]string, maxConceptItems+1)
	for i := range paths {
		paths[i] = fmt.Sprintf("concept-%05d.md", i)
	}
	loaded, err := bundle.Load(t.Context(), manyConceptSource{paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	// Act.
	_, broadErr := searchConceptSnapshot(t.Context(), loaded, "body", 20, nil, bundle.TemporalProfileDate)
	_, narrowErr := searchConceptSnapshot(t.Context(), loaded, "never-matches", 20, nil, bundle.TemporalProfileDate)
	result := conceptQueryError("search concepts", narrowErr)
	// Assert.
	if !errors.Is(broadErr, errMCPConceptCountLimit) || !errors.Is(narrowErr, errMCPConceptCountLimit) {
		t.Fatalf("concept count errors = broad:%v narrow:%v", broadErr, narrowErr)
	}
	assertSchemaErrorEnvelope(t, result, "resource_limit")
	if !hasInputHint(result, "bundle_path", "smaller bundle") {
		t.Fatalf("concept count advice did not identify the bundle: %#v", result.StructuredContent)
	}
	if strings.Contains(resultText(t, result), "narrower query") {
		t.Fatalf("concept count error suggested ineffective retry: %s", resultText(t, result))
	}
}
