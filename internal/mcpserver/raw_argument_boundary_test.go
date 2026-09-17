package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/skosovsky/okf/mutation"
)

type rawArgumentString string
type rawArgumentMap map[rawArgumentString]any
type rawArgumentSlice []rawArgumentMap

type rawArgumentBundlePath struct {
	BundlePath rawArgumentString `json:"bundle_path"`
}

type rawArgumentBytes []byte

type rawArgumentJSONMarshaler struct {
	calls *int
}

func (value rawArgumentJSONMarshaler) MarshalJSON() ([]byte, error) {
	*value.calls++
	return []byte(`"coerced"`), nil
}

type rawArgumentTextMarshaler struct {
	calls *int
}

func (value rawArgumentTextMarshaler) MarshalText() ([]byte, error) {
	*value.calls++
	return []byte("coerced"), nil
}

type rawArgumentTextMapKey string

func (rawArgumentTextMapKey) MarshalText() ([]byte, error) {
	return []byte("coerced-key"), nil
}

type rawArgumentEmbeddedValue struct {
	Value rawArgumentString `json:"value"`
}

type rawArgumentEmbeddedEnvelope struct {
	*rawArgumentEmbeddedValue
}

type rawArgumentPrivateEmbedded struct {
	Value rawArgumentString `json:"value"`
}

type rawArgumentPrivateEnvelope struct {
	rawArgumentPrivateEmbedded
}

type rawArgumentPrivatePointerEnvelope struct {
	*rawArgumentPrivateEmbedded
}

type rawArgumentStructCycle struct {
	Next *rawArgumentStructCycle `json:"next"`
}

type rawArgumentStructBounds struct {
	Next   *rawArgumentStructBounds `json:"next,omitempty"`
	Values []string                 `json:"values,omitempty"`
}

type rawArgumentUnexported struct {
	hidden  rawArgumentString
	Ignored rawArgumentString `json:"-"`
	Visible rawArgumentString `json:"visible"`
}

type rawPatchGeneratedArguments struct {
	By rawArgumentString `json:"by"`
}

type rawPatchOperationArguments struct {
	Kind      string                      `json:"kind"`
	ConceptID string                      `json:"concept_id"`
	Generated *rawPatchGeneratedArguments `json:"generated"`
}

type rawMigrationCitationEntryArguments struct {
	LegacyNumber int               `json:"legacy_number"`
	SourceID     string            `json:"source_id"`
	Resource     rawArgumentString `json:"resource"`
}

type rawMigrationCitationArguments struct {
	Path    string                               `json:"path"`
	Entries []rawMigrationCitationEntryArguments `json:"entries"`
}

type rawComputationExecutorArguments struct {
	Resource string   `json:"resource"`
	Receipt  []string `json:"receipt"`
}

type rawComputationAttesterArguments struct {
	Resource string `json:"resource"`
}

type rawComputationParameterArguments struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type rawComputationContractArguments struct {
	Mode              string                             `json:"mode"`
	Runtime           string                             `json:"runtime"`
	Parameters        []rawComputationParameterArguments `json:"parameters"`
	InlineComputation rawArgumentString                  `json:"inline_computation"`
	Executor          rawComputationExecutorArguments    `json:"executor"`
	Attester          rawComputationAttesterArguments    `json:"attester"`
}

type rawPatchComputationOperationArguments struct {
	Kind                string                          `json:"kind"`
	ConceptID           string                          `json:"concept_id"`
	AttestedComputation rawComputationContractArguments `json:"attested_computation"`
}

type rawMigrationComputationArguments struct {
	ConceptID string                          `json:"concept_id"`
	Contract  rawComputationContractArguments `json:"contract"`
}

type rawJSONParityString string
type rawJSONParityBool bool
type rawJSONParityInt int64
type rawJSONParityUint uint64
type rawJSONParityFloat32 float32
type rawJSONParityFloat64 float64

type rawJSONParityLeft struct {
	Conflict string `json:"conflict"`
	Left     string `json:"left"`
}

type rawJSONParityRight struct {
	Conflict string `json:"conflict"`
	Right    string `json:"right"`
}

type rawJSONParityNested struct {
	Value string `json:"value"`
}

type rawJSONParityStruct struct {
	rawJSONParityLeft
	*rawJSONParityRight
	Nested  rawJSONParityNested `json:"nested"`
	Empty   string              `json:"empty,omitempty"`
	Visible string              `json:"visible"`
}

type rawJSONParityDominant struct {
	rawJSONParityNested
	Value string `json:"value"`
}

type rawJSONParityTaggedEmbedded struct {
	*rawJSONParityNested `json:"wrapped"`
}

func TestContractToolHandlerRejectsRawInvalidUTF8BeforeSchemaAndHandler(t *testing.T) {
	// Arrange.
	toolNames := []string{
		"list_concepts",
		"read_concept",
		"validate_bundle",
		"get_semantic_graph",
		"write_concept",
		"preview_concept_patch",
		"apply_concept_patch",
		"preview_v02_migration",
		"apply_v02_migration",
	}

	// Act and assert.
	for _, name := range toolNames {
		t.Run(name, func(t *testing.T) {
			nextCalls := 0
			next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				nextCalls++
				return mcp.NewToolResultStructured(map[string]any{}, ""), nil
			}
			result := callHandler(t, contractToolHandler(name, next), map[string]any{
				"bundle_path": t.TempDir(),
				"nested": rawArgumentSlice{{
					"value": rawArgumentString(string([]byte{0xff})),
				}},
			})

			assertSchemaErrorEnvelope(t, result, "invalid_request")
			envelope := result.StructuredContent.(errorEnvelope)
			if envelope.Message != "MCP arguments contain invalid UTF-8" {
				t.Fatalf("error message = %q", envelope.Message)
			}
			if nextCalls != 0 {
				t.Fatalf("handler calls = %d, want 0", nextCalls)
			}
		})
	}
}

func TestContractToolHandlerUsesRawArgumentsForTypedTopLevelStructs(t *testing.T) {
	// Arrange.
	toolNames := []string{
		"list_concepts",
		"read_concept",
		"validate_bundle",
		"get_semantic_graph",
		"write_concept",
		"preview_concept_patch",
		"apply_concept_patch",
		"preview_v02_migration",
		"apply_v02_migration",
	}
	invalid := rawArgumentString(string([]byte{0xff}))
	sourceOpens, sourceReads, storeOpens := 0, 0, 0
	ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
		sourceOpen: func() { sourceOpens++ },
		sourceRead: func() { sourceReads++ },
		storeOpen:  func() { storeOpens++ },
	})

	// Act and assert.
	for _, name := range toolNames {
		t.Run(name, func(t *testing.T) {
			nextCalls := 0
			next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				nextCalls++
				return mcp.NewToolResultStructured(map[string]any{}, ""), nil
			}
			result := callRawArgumentsHandlerContext(
				t,
				ctx,
				contractToolHandler(name, next),
				&rawArgumentBundlePath{BundlePath: invalid},
			)

			assertSchemaErrorEnvelope(t, result, "invalid_request")
			envelope := result.StructuredContent.(errorEnvelope)
			if envelope.Message != errMCPArgumentUTF8.Error() {
				t.Fatalf("error message = %q", envelope.Message)
			}
			if nextCalls != 0 {
				t.Fatalf("handler calls = %d, want 0", nextCalls)
			}
		})
	}
	if sourceOpens != 0 || sourceReads != 0 || storeOpens != 0 {
		t.Fatalf(
			"I/O counts = source-open:%d source-read:%d store-open:%d, want all zero",
			sourceOpens,
			sourceReads,
			storeOpens,
		)
	}
}

func TestContractToolHandlerKeepsTypedTopLevelStructOutsideMapSchemaBoundary(t *testing.T) {
	// Arrange. mcp-go intentionally exposes only map[string]any through
	// GetArguments, while GetRawArguments retains this typed value.
	nextCalls := 0
	next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		nextCalls++
		return mcp.NewToolResultStructured(map[string]any{}, ""), nil
	}

	// Act.
	result := callRawArgumentsHandler(
		t,
		contractToolHandler("list_concepts", next),
		rawArgumentBundlePath{BundlePath: rawArgumentString(t.TempDir())},
	)

	// Assert. Raw validation accepts the valid struct, then the public map-only
	// schema boundary rejects it without dispatching the tool handler.
	assertSchemaErrorEnvelope(t, result, "schema_validation")
	if nextCalls != 0 {
		t.Fatalf("handler calls = %d, want 0", nextCalls)
	}
}

func TestContractToolHandlerRejectsRawByteBudgetBeforeAllToolIO(t *testing.T) {
	// Arrange.
	toolNames := []string{
		"list_concepts",
		"read_concept",
		"validate_bundle",
		"get_semantic_graph",
		"write_concept",
		"preview_concept_patch",
		"apply_concept_patch",
		"preview_v02_migration",
		"apply_v02_migration",
	}
	oversize := rawArgumentStringWithJSONContentSize(t, maxBundleTotalBytes-1)
	sourceOpens, sourceReads, storeOpens := 0, 0, 0
	ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
		sourceOpen: func() { sourceOpens++ },
		sourceRead: func() { sourceReads++ },
		storeOpen:  func() { storeOpens++ },
	})

	// Act and assert. The escaped value alone serializes one byte beyond the
	// budget once quoted, before the enclosing struct overhead is considered.
	for _, name := range toolNames {
		t.Run(name, func(t *testing.T) {
			nextCalls := 0
			next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				nextCalls++
				return mcp.NewToolResultStructured(map[string]any{}, ""), nil
			}
			result := callRawArgumentsHandlerContext(
				t,
				ctx,
				contractToolHandler(name, next),
				&rawArgumentBundlePath{BundlePath: oversize},
			)

			assertSchemaErrorEnvelope(t, result, "resource_limit")
			if nextCalls != 0 {
				t.Fatalf("handler calls = %d, want 0", nextCalls)
			}
		})
	}
	if sourceOpens != 0 || sourceReads != 0 || storeOpens != 0 {
		t.Fatalf(
			"I/O counts = source-open:%d source-read:%d store-open:%d, want all zero",
			sourceOpens,
			sourceReads,
			storeOpens,
		)
	}
}

func callRawArgumentsHandler(
	t *testing.T,
	handler server.ToolHandlerFunc,
	arguments any,
) *mcp.CallToolResult {
	t.Helper()
	return callRawArgumentsHandlerContext(t, t.Context(), handler, arguments)
}

func callRawArgumentsHandlerContext(
	t *testing.T,
	ctx context.Context,
	handler server.ToolHandlerFunc,
	arguments any,
) *mcp.CallToolResult {
	t.Helper()
	result, err := handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: arguments},
	})
	if err != nil {
		t.Fatalf("handler returned protocol error: %v", err)
	}
	return result
}

func TestValidateMCPInputCoversTypedContainers(t *testing.T) {
	// Arrange.
	invalid := rawArgumentString(string([]byte{0xff}))
	pointer := &invalid
	tests := []struct {
		name        string
		input       any
		wantMessage string
	}{
		{
			name: "typed map value",
			input: map[rawArgumentString]rawArgumentString{
				"value": invalid,
			},
			wantMessage: errMCPArgumentUTF8.Error(),
		},
		{
			name:        "typed slice",
			input:       []rawArgumentString{invalid},
			wantMessage: errMCPArgumentUTF8.Error(),
		},
		{
			name:        "typed array",
			input:       [1]rawArgumentString{invalid},
			wantMessage: errMCPArgumentUTF8.Error(),
		},
		{
			name:        "interface and pointer",
			input:       []any{any(pointer)},
			wantMessage: errMCPArgumentUTF8.Error(),
		},
		{
			name: "typed struct",
			input: struct {
				Value rawArgumentString `json:"value"`
			}{Value: invalid},
			wantMessage: errMCPArgumentUTF8.Error(),
		},
		{
			name: "embedded pointer and alias",
			input: rawArgumentEmbeddedEnvelope{
				rawArgumentEmbeddedValue: &rawArgumentEmbeddedValue{Value: invalid},
			},
			wantMessage: errMCPArgumentUTF8.Error(),
		},
		{
			name: "private embedded struct",
			input: rawArgumentPrivateEnvelope{
				rawArgumentPrivateEmbedded: rawArgumentPrivateEmbedded{Value: invalid},
			},
			wantMessage: errMCPArgumentUTF8.Error(),
		},
		{
			name: "private embedded pointer",
			input: rawArgumentPrivatePointerEnvelope{
				rawArgumentPrivateEmbedded: &rawArgumentPrivateEmbedded{Value: invalid},
			},
			wantMessage: errMCPArgumentUTF8.Error(),
		},
		{
			name: "typed invalid string key",
			input: map[rawArgumentString]string{
				invalid: "valid",
			},
			wantMessage: errMCPArgumentKeyUTF8.Error(),
		},
		{
			name: "non-string map key",
			input: map[int]string{
				1: "valid",
			},
			wantMessage: errMCPArgumentMapKey.Error(),
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validateRawMCPArgumentsContext(t.Context(), test.input)
			assertSchemaErrorEnvelope(t, result, "invalid_request")
			envelope := result.StructuredContent.(errorEnvelope)
			if envelope.Message != test.wantMessage {
				t.Fatalf("error message = %q, want %q", envelope.Message, test.wantMessage)
			}
		})
	}

	if result := validateRawMCPArgumentsContext(t.Context(), rawArgumentUnexported{
		hidden:  invalid,
		Ignored: invalid,
		Visible: "valid",
	}); result != nil {
		t.Fatalf("ignored or unexported field was inspected: %#v", result)
	}

	invalidTagType := reflect.StructOf([]reflect.StructField{{
		Name: "Value",
		Type: reflect.TypeFor[string](),
		Tag:  reflect.StructTag("json:\"" + string([]byte{0xff}) + "\""),
	}})
	escapedInvalidTagType := reflect.StructOf([]reflect.StructField{{
		Name: "Value",
		Type: reflect.TypeFor[string](),
		Tag:  reflect.StructTag(`json:"\xff"`),
	}})
	for name, typ := range map[string]reflect.Type{
		"raw invalid JSON name":     invalidTagType,
		"escaped invalid JSON name": escapedInvalidTagType,
	} {
		t.Run(name, func(t *testing.T) {
			result := validateRawMCPArgumentsContext(t.Context(), reflect.New(typ).Elem().Interface())
			if result == nil {
				t.Fatal("invalid UTF-8 JSON field name accepted")
			}
			assertSchemaErrorEnvelope(t, result, "invalid_request")
			envelope := result.StructuredContent.(errorEnvelope)
			if envelope.Message != errMCPArgumentKeyUTF8.Error() {
				t.Fatalf("invalid field name message = %q", envelope.Message)
			}
		})
	}

	unrelatedInvalidTagType := reflect.StructOf([]reflect.StructField{{
		Name: "Value",
		Type: reflect.TypeFor[string](),
		Tag: reflect.StructTag(
			"xml:\"" + string([]byte{0xff}) + "\" json:\"value\"",
		),
	}})
	unrelatedInvalidTagValue := reflect.New(unrelatedInvalidTagType).Elem()
	unrelatedInvalidTagValue.Field(0).SetString("valid")
	if result := validateRawMCPArgumentsContext(t.Context(), unrelatedInvalidTagValue.Interface()); result != nil {
		t.Fatalf("unrelated non-JSON tag rejected: %#v", result)
	}
}

func TestValidateMCPInputBoundsCyclesDepthAndItems(t *testing.T) {
	// Arrange.
	mapCycle := map[string]any{}
	mapCycle["self"] = mapCycle
	sliceCycle := make([]any, 1)
	sliceCycle[0] = sliceCycle
	var pointerCycle any
	pointerCycle = &pointerCycle
	structCycle := &rawArgumentStructCycle{}
	structCycle.Next = structCycle
	deepStruct := &rawArgumentStructBounds{}
	deepStructTail := deepStruct
	for range maxBundlePathDepth + 2 {
		deepStructTail.Next = &rawArgumentStructBounds{}
		deepStructTail = deepStructTail.Next
	}

	deep := any("valid")
	for range maxBundlePathDepth + 2 {
		previous := deep
		deep = &previous
	}
	oversize := make([]string, maxMCPArgumentItems)

	tests := []struct {
		name        string
		input       any
		wantCode    string
		wantMessage string
	}{
		{
			name:        "map cycle",
			input:       mapCycle,
			wantCode:    "invalid_request",
			wantMessage: errMCPArgumentCycle.Error(),
		},
		{
			name:        "slice cycle",
			input:       sliceCycle,
			wantCode:    "invalid_request",
			wantMessage: errMCPArgumentCycle.Error(),
		},
		{
			name:        "pointer interface cycle",
			input:       pointerCycle,
			wantCode:    "invalid_request",
			wantMessage: errMCPArgumentCycle.Error(),
		},
		{
			name:        "struct pointer cycle",
			input:       structCycle,
			wantCode:    "invalid_request",
			wantMessage: errMCPArgumentCycle.Error(),
		},
		{
			name:        "depth",
			input:       deep,
			wantCode:    "resource_limit",
			wantMessage: errMCPResourceLimit.Error(),
		},
		{
			name:        "struct depth",
			input:       deepStruct,
			wantCode:    "resource_limit",
			wantMessage: errMCPResourceLimit.Error(),
		},
		{
			name:        "items",
			input:       oversize,
			wantCode:    "resource_limit",
			wantMessage: errMCPResourceLimit.Error(),
		},
		{
			name: "struct items",
			input: rawArgumentStructBounds{
				Values: oversize,
			},
			wantCode:    "resource_limit",
			wantMessage: errMCPResourceLimit.Error(),
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validateRawMCPArgumentsContext(t.Context(), test.input)
			assertSchemaErrorEnvelope(t, result, test.wantCode)
			envelope := result.StructuredContent.(errorEnvelope)
			if envelope.Message != test.wantMessage {
				t.Fatalf("error message = %q, want %q", envelope.Message, test.wantMessage)
			}
		})
	}

	shared := map[rawArgumentString]string{"value": "valid"}
	if result := validateRawMCPArgumentsContext(t.Context(), []any{shared, shared}); result != nil {
		t.Fatalf("repeated non-cyclic alias rejected: %#v", result)
	}
}

func TestValidateMCPInputBoundsExactJSONOutputBytes(t *testing.T) {
	// Arrange. NUL forces the longest C0 escape form, so the raw strings are
	// much smaller than their exact encoding/json representation.
	exactString := rawArgumentStringWithJSONContentSize(t, maxBundleTotalBytes-2)
	halfContent := (maxBundleTotalBytes - 7) / 2
	tests := []struct {
		name     string
		input    any
		wantCode string
	}{
		{
			name:  "escape-heavy string exact boundary",
			input: exactString,
		},
		{
			name: "container repeated occurrences exact boundary",
			input: []rawArgumentString{
				rawArgumentStringWithJSONContentSize(t, halfContent),
				rawArgumentStringWithJSONContentSize(
					t,
					maxBundleTotalBytes-7-halfContent,
				),
			},
		},
		{
			name: "map structural tokens exact boundary",
			input: map[rawArgumentString]rawArgumentString{
				"k": rawArgumentStringWithJSONContentSize(t, maxBundleTotalBytes-8),
			},
		},
		{
			name: "promoted embedded field and name exact boundary",
			input: rawArgumentEmbeddedEnvelope{
				rawArgumentEmbeddedValue: &rawArgumentEmbeddedValue{
					Value: rawArgumentStringWithJSONContentSize(
						t,
						maxBundleTotalBytes-2-len(`"value":`)-2,
					),
				},
			},
		},
		{
			name: "omitted empty field exact boundary",
			input: struct {
				Ignored rawArgumentString `json:"ignored,omitempty"`
				Value   rawArgumentString `json:"v"`
			}{
				Value: rawArgumentStringWithJSONContentSize(
					t,
					maxBundleTotalBytes-2-len(`"v":`)-2,
				),
			},
		},
		{
			name: "escape-heavy string boundary plus one",
			input: rawArgumentStringWithJSONContentSize(
				t,
				maxBundleTotalBytes-1,
			),
			wantCode: "resource_limit",
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validateRawMCPArgumentsContext(t.Context(), test.input)
			if test.wantCode == "" {
				if result != nil {
					t.Fatalf("exact-boundary input rejected: %#v", result)
				}
				return
			}
			assertSchemaErrorEnvelope(t, result, test.wantCode)
		})
	}
}

func rawArgumentStringWithJSONContentSize(t *testing.T, size int) rawArgumentString {
	t.Helper()
	if size < 0 {
		t.Fatalf("negative JSON content size: %d", size)
	}
	return rawArgumentString(
		strings.Repeat("\x00", size/6) +
			strings.Repeat("x", size%6),
	)
}

func TestMCPJSONOutputSizeEstimatorMatchesEncodingJSON(t *testing.T) {
	// Arrange.
	stringValue := rawJSONParityString("ascii\x00\x01\b\f\n\r\t\"\\<>&\u007f\u2028\u2029🙂")
	pointerValue := rawJSONParityString("pointer")
	tests := []struct {
		name  string
		input any
	}{
		{name: "null interface", input: nil},
		{name: "typed nil pointer", input: (*rawJSONParityString)(nil)},
		{name: "typed nil map", input: map[rawJSONParityString]any(nil)},
		{name: "typed nil slice", input: []rawJSONParityString(nil)},
		{name: "empty map", input: map[rawJSONParityString]any{}},
		{name: "empty slice", input: []rawJSONParityString{}},
		{name: "byte array stays numeric", input: [3]byte{1, 2, 3}},
		{name: "empty struct", input: struct{}{}},
		{name: "escaped string alias", input: stringValue},
		{name: "bool alias true", input: rawJSONParityBool(true)},
		{name: "bool alias false", input: rawJSONParityBool(false)},
		{name: "signed integer alias", input: rawJSONParityInt(math.MinInt64)},
		{name: "unsigned integer alias", input: rawJSONParityUint(math.MaxUint64)},
		{name: "negative zero", input: rawJSONParityFloat64(math.Copysign(0, -1))},
		{name: "float64 fixed cutoff", input: rawJSONParityFloat64(1e-6)},
		{name: "float64 exponent", input: rawJSONParityFloat64(1e-7)},
		{name: "float64 large exponent", input: rawJSONParityFloat64(1e21)},
		{name: "float32 exponent", input: rawJSONParityFloat32(1e-7)},
		{name: "pointer", input: &pointerValue},
		{
			name: "array and nested interfaces",
			input: [4]any{
				rawJSONParityInt(-7),
				rawJSONParityUint(9),
				[]any{true, nil, rawJSONParityFloat64(1.25)},
				&pointerValue,
			},
		},
		{
			name: "map ordering and escaped keys",
			input: map[rawJSONParityString]any{
				"<html>":     stringValue,
				"quote\"":    false,
				"line\u2028": rawJSONParityInt(3),
			},
		},
		{
			name: "embedded conflict promotion and omitempty",
			input: rawJSONParityStruct{
				rawJSONParityLeft: rawJSONParityLeft{
					Conflict: "discard-left",
					Left:     "left",
				},
				rawJSONParityRight: &rawJSONParityRight{
					Conflict: "discard-right",
					Right:    "right",
				},
				Nested:  rawJSONParityNested{Value: "nested"},
				Visible: "visible",
			},
		},
		{
			name: "shallow field dominance",
			input: rawJSONParityDominant{
				rawJSONParityNested: rawJSONParityNested{Value: "embedded"},
				Value:               "shallow",
			},
		},
		{
			name: "nil embedded pointer after conflict selection",
			input: rawJSONParityStruct{
				rawJSONParityLeft: rawJSONParityLeft{
					Conflict: "still-discarded",
					Left:     "left",
				},
				Visible: "visible",
			},
		},
		{
			name: "tagged anonymous pointer",
			input: rawJSONParityTaggedEmbedded{
				rawJSONParityNested: &rawJSONParityNested{Value: "wrapped"},
			},
		},
		{
			name: "omitempty scalar and collection matrix",
			input: struct {
				String  string         `json:"string,omitempty"`
				Bool    bool           `json:"bool,omitempty"`
				Int     int            `json:"int,omitempty"`
				Float   float64        `json:"float,omitempty"`
				Pointer *string        `json:"pointer,omitempty"`
				Slice   []string       `json:"slice,omitempty"`
				Map     map[string]any `json:"map,omitempty"`
				Array   [0]string      `json:"array,omitempty"`
				Struct  struct{}       `json:"struct,omitempty"`
			}{},
		},
		{
			name: "invalid tag name falls back to Go field",
			input: struct {
				Bad string `json:"bad\\name"`
			}{Bad: "fallback"},
		},
		{
			name: "HTML escaped field name",
			input: struct {
				Value string `json:"<tag>"`
			}{Value: "value"},
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.input)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			size, err := estimateMCPJSONOutputSizeContext(t.Context(), test.input, ^uint64(0))
			if err != nil {
				t.Fatalf("estimate JSON size: %v", err)
			}
			if size != uint64(len(encoded)) {
				t.Fatalf(
					"estimated size = %d, marshal size = %d, JSON = %s",
					size,
					len(encoded),
					encoded,
				)
			}
			if bounded, boundedErr := estimateMCPJSONOutputSizeContext(
				t.Context(),
				test.input,
				uint64(len(encoded)),
			); boundedErr != nil || bounded != uint64(len(encoded)) {
				t.Fatalf("exact bounded estimate = %d, error = %v", bounded, boundedErr)
			}
			if _, boundedErr := estimateMCPJSONOutputSizeContext(
				t.Context(),
				test.input,
				uint64(len(encoded)-1),
			); !errors.Is(boundedErr, errMCPResourceLimit) {
				t.Fatalf("boundary - 1 error = %v", boundedErr)
			}
			if result := validateRawMCPArgumentsContext(t.Context(), test.input); result != nil {
				t.Fatalf("supported value rejected: %#v", result)
			}
		})
	}
}

func TestMCPInputBudgetCountersDoNotOverflow(t *testing.T) {
	// Arrange.
	maxInt := int(^uint(0) >> 1)
	maxUint64 := ^uint64(0)

	// Act and assert.
	for _, test := range []struct {
		name  string
		used  int
		add   int
		valid bool
	}{
		{name: "items exact boundary", used: maxMCPArgumentItems, add: 0, valid: true},
		{name: "items boundary plus one", used: maxMCPArgumentItems, add: 1},
		{name: "items integer overflow", used: maxInt, add: 1},
		{name: "items negative amount", used: 0, add: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			used := test.used
			err := consumeMCPInputItems(&used, test.add)
			if test.valid && err != nil {
				t.Fatalf("consume items: %v", err)
			}
			if !test.valid && !errors.Is(err, errMCPResourceLimit) {
				t.Fatalf("consume items error = %v", err)
			}
			want := test.used
			if test.valid {
				want += test.add
			}
			if used != want {
				t.Fatalf("items = %d, want %d", used, want)
			}
		})
	}

	for _, test := range []struct {
		name  string
		size  uint64
		limit uint64
		add   uint64
		valid bool
	}{
		{
			name:  "JSON bytes exact boundary",
			size:  maxBundleTotalBytes,
			limit: maxBundleTotalBytes,
			valid: true,
		},
		{
			name:  "JSON bytes boundary plus one",
			size:  maxBundleTotalBytes,
			limit: maxBundleTotalBytes,
			add:   1,
		},
		{
			name:  "JSON byte counter integer overflow",
			size:  maxUint64,
			limit: maxUint64,
			add:   1,
		},
		{
			name:  "JSON byte counter already beyond limit",
			size:  maxBundleTotalBytes + 1,
			limit: maxBundleTotalBytes,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			estimator := mcpJSONSizeEstimator{
				size:  test.size,
				limit: test.limit,
			}
			err := estimator.consume(test.add)
			if test.valid && err != nil {
				t.Fatalf("consume JSON bytes: %v", err)
			}
			if !test.valid && !errors.Is(err, errMCPResourceLimit) {
				t.Fatalf("consume JSON bytes error = %v", err)
			}
			want := test.size
			if test.valid {
				want += test.add
			}
			if estimator.size != want {
				t.Fatalf("JSON bytes = %d, want %d", estimator.size, want)
			}
		})
	}
}

func TestValidateMCPInputRejectsJSONCoercionsBeforeMarshal(t *testing.T) {
	// Arrange.
	jsonCalls, textCalls := 0, 0
	tests := []struct {
		name  string
		input any
	}{
		{name: "byte slice", input: []byte("opaque")},
		{name: "named byte slice", input: rawArgumentBytes("opaque")},
		{name: "json marshaler", input: rawArgumentJSONMarshaler{calls: &jsonCalls}},
		{name: "text marshaler", input: rawArgumentTextMarshaler{calls: &textCalls}},
		{
			name: "text marshaler string map key",
			input: map[rawArgumentTextMapKey]string{
				"key": "value",
			},
		},
		{name: "json number", input: json.Number("1")},
		{
			name: "json string tag",
			input: struct {
				Value int `json:"value,string"`
			}{Value: 1},
		},
		{
			name: "json omitzero tag",
			input: struct {
				Value string `json:"value,omitzero"`
			}{},
		},
		{
			name: "omitted byte slice remains outside supported raw domain",
			input: struct {
				Value []byte `json:"value,omitempty"`
			}{},
		},
		{name: "complex scalar", input: complex128(1)},
		{name: "channel", input: make(chan int)},
		{name: "function", input: func() {}},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validateRawMCPArgumentsContext(t.Context(), test.input)
			assertSchemaErrorEnvelope(t, result, "invalid_request")
			envelope := result.StructuredContent.(errorEnvelope)
			if envelope.Message != errMCPArgumentCoercion.Error() {
				t.Fatalf("message = %q", envelope.Message)
			}
		})
	}
	if jsonCalls != 0 || textCalls != 0 {
		t.Fatalf("coercion calls = json:%d text:%d, want zero", jsonCalls, textCalls)
	}

	nextCalls := 0
	result := callHandler(
		t,
		contractToolHandler(
			"list_concepts",
			func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				nextCalls++
				return mcp.NewToolResultStructured(map[string]any{}, ""), nil
			},
		),
		map[string]any{
			"bundle_path": t.TempDir(),
			"nested":      rawArgumentJSONMarshaler{calls: &jsonCalls},
		},
	)
	assertSchemaErrorEnvelope(t, result, "invalid_request")
	if jsonCalls != 0 || nextCalls != 0 {
		t.Fatalf("boundary calls = marshaler:%d handler:%d, want zero", jsonCalls, nextCalls)
	}

	if result := validateRawMCPArgumentsContext(t.Context(), map[string]any{
		"string": "wire",
		"number": float64(1),
		"array":  []any{true, nil},
	}); result != nil {
		t.Fatalf("wire-decoded JSON map rejected: %#v", result)
	}
	if result := validateRawMCPArgumentsContext(t.Context(), struct {
		Ignored []byte `json:"-"`
	}{
		Ignored: []byte("not serialized"),
	}); result != nil {
		t.Fatalf("explicitly ignored unsupported field rejected: %#v", result)
	}

	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		result := validateRawMCPArgumentsContext(t.Context(), value)
		assertSchemaErrorEnvelope(t, result, "invalid_request")
		envelope := result.StructuredContent.(errorEnvelope)
		if envelope.Message != errMCPArgumentValue.Error() {
			t.Fatalf("non-finite float message = %q", envelope.Message)
		}
	}
}

func TestValidateMCPInputMapTraversalIsDeterministic(t *testing.T) {
	// Arrange.
	invalidKey := rawArgumentString(string([]byte{0xff}))
	values := rawArgumentMap{
		"a": rawArgumentSlice{{
			"value": rawArgumentString(string([]byte{0xfe})),
		}},
		"b": rawArgumentMap{
			rawArgumentString(string([]byte{0xfd})): "nested invalid key",
		},
		invalidKey: "invalid root key",
	}
	orders := [][]rawArgumentString{
		{"a", "b", invalidKey},
		{invalidKey, "b", "a"},
		{"b", "a", invalidKey},
		{invalidKey, "a", "b"},
	}

	// Act and assert.
	for repetition := 0; repetition < 100; repetition++ {
		for _, order := range orders {
			arguments := make(rawArgumentMap, len(order))
			for _, key := range order {
				arguments[key] = values[key]
			}
			err := validateMCPInputContext(t.Context(), arguments, 0, new(int))
			if err == nil || err.Error() != "MCP arguments contain invalid UTF-8" {
				t.Fatalf("repetition %d order %#v error = %v", repetition, order, err)
			}
			result := validateRawMCPArgumentsContext(t.Context(), arguments)
			assertSchemaErrorEnvelope(t, result, "invalid_request")
			envelope := result.StructuredContent.(errorEnvelope)
			if envelope.Message != "MCP arguments contain invalid UTF-8" {
				t.Fatalf(
					"repetition %d order %#v message = %q",
					repetition,
					order,
					envelope.Message,
				)
			}
		}
	}
}

func TestValidateMCPInputStructTraversalIsDeterministic(t *testing.T) {
	// Arrange. Declaration order intentionally differs from JSON field order.
	input := struct {
		Later rawArgumentString            `json:"b"`
		First map[rawArgumentString]string `json:"a"`
	}{
		Later: rawArgumentString(string([]byte{0xfe})),
		First: map[rawArgumentString]string{
			rawArgumentString(string([]byte{0xff})): "invalid key",
		},
	}

	// Act and assert.
	for repetition := 0; repetition < 100; repetition++ {
		result := validateRawMCPArgumentsContext(t.Context(), input)
		assertSchemaErrorEnvelope(t, result, "invalid_request")
		envelope := result.StructuredContent.(errorEnvelope)
		if envelope.Message != errMCPArgumentKeyUTF8.Error() {
			t.Fatalf("repetition %d message = %q", repetition, envelope.Message)
		}
	}
}

func TestRawInvalidUTF8RejectsPatchAndMigrationBeforeHandlerSourceAndStoreIO(t *testing.T) {
	// Arrange.
	patchRoot := t.TempDir()
	writeTestFile(t, patchRoot, "index.md",
		"---\nokf_version: \"0.2\"\n---\n# Knowledge\n- [Alpha](alpha.md)\n",
	)
	writeTestFile(t, patchRoot, "alpha.md",
		"---\ntype: Knowledge\n---\nClaim.\n",
	)
	migrationRoot := t.TempDir()
	writeTestFile(t, migrationRoot, "index.md",
		"---\nokf_version: \"0.1\"\n---\n# Knowledge\n- [Alpha](alpha.md)\n",
	)
	writeTestFile(t, migrationRoot, "alpha.md",
		"---\ntype: Knowledge\n---\nClaim [1].\n\n# Citations\n\n[1] https://example.test/spec\n",
	)
	sourceOpens, sourceReads, storeOpens := 0, 0, 0
	ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
		sourceOpen: func() { sourceOpens++ },
		sourceRead: func() { sourceReads++ },
		storeOpen:  func() { storeOpens++ },
	})
	invalid := rawArgumentString(string([]byte{0xff}))
	patchArguments := func(root string, apply bool) map[string]any {
		arguments := map[string]any{
			"bundle_path": root,
			"actor":       "mcp:raw-boundary",
			"operations": []rawPatchOperationArguments{{
				Kind:      "set_generated",
				ConceptID: "alpha",
				Generated: &rawPatchGeneratedArguments{
					By: invalid,
				},
			}},
		}
		if apply {
			arguments["expected_revision"] = "sha256:" + strings.Repeat("a", 64)
			arguments["expected_plan_digest"] = "sha256:" + strings.Repeat("b", 64)
		}
		return arguments
	}
	migrationArguments := func(root string, apply bool) map[string]any {
		arguments := map[string]any{
			"bundle_path":      root,
			"from":             "auto",
			"generated_by":     "process:migration",
			"timestamp_policy": "preserve",
			"citation_mappings": []rawMigrationCitationArguments{{
				Path: "alpha.md",
				Entries: []rawMigrationCitationEntryArguments{{
					LegacyNumber: 1,
					SourceID:     "source",
					Resource:     invalid,
				}},
			}},
		}
		if apply {
			arguments["expected_source"] = testMigrationSourceValue(
				mutation.MigrationVersionV02,
				string(mutation.MigrationTransitionTargetNoop),
			)
		}
		return arguments
	}
	tests := []struct {
		name     string
		toolName string
		root     string
		handler  server.ToolHandlerFunc
		args     map[string]any
	}{
		{
			name:     "patch preview",
			toolName: "preview_concept_patch",
			root:     patchRoot,
			handler:  handlePreviewConceptPatch,
			args:     patchArguments(patchRoot, false),
		},
		{
			name:     "patch apply",
			toolName: "apply_concept_patch",
			root:     patchRoot,
			handler:  handleApplyConceptPatch,
			args:     patchArguments(patchRoot, true),
		},
		{
			name:     "migration preview",
			toolName: "preview_v02_migration",
			root:     migrationRoot,
			handler:  handlePreviewV02Migration,
			args:     migrationArguments(migrationRoot, false),
		},
		{
			name:     "migration apply",
			toolName: "apply_v02_migration",
			root:     migrationRoot,
			handler:  handleApplyV02Migration,
			args:     migrationArguments(migrationRoot, true),
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := protocolTreeSnapshot(t, test.root)
			if err := validateContract(test.toolName+".input", test.args); err != nil {
				t.Fatalf("test input must be schema-valid after JSON marshaling: %v", err)
			}
			handlerCalls := 0
			guarded := contractToolHandler(test.toolName, func(
				ctx context.Context,
				request mcp.CallToolRequest,
			) (*mcp.CallToolResult, error) {
				handlerCalls++
				return test.handler(ctx, request)
			})
			result, err := guarded(ctx, mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name:      test.toolName,
					Arguments: test.args,
				},
			})
			if err != nil {
				t.Fatalf("handler protocol error = %v", err)
			}
			assertSchemaErrorEnvelope(t, result, "invalid_request")
			envelope := result.StructuredContent.(errorEnvelope)
			if envelope.Message != errMCPArgumentUTF8.Error() {
				t.Fatalf("error message = %q", envelope.Message)
			}
			if handlerCalls != 0 {
				t.Fatalf("handler calls = %d, want 0", handlerCalls)
			}
			if sourceOpens != 0 || sourceReads != 0 || storeOpens != 0 {
				t.Fatalf(
					"I/O counts = source-open:%d source-read:%d store-open:%d, want all zero",
					sourceOpens,
					sourceReads,
					storeOpens,
				)
			}
			if after := protocolTreeSnapshot(t, test.root); !reflect.DeepEqual(after, before) {
				t.Fatalf("bundle changed:\nbefore=%#v\nafter=%#v", before, after)
			}
			if _, statErr := os.Lstat(filepath.Join(test.root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("transactional store was opened: %v", statErr)
			}
		})
	}
}

func TestRawByteBudgetRejectsSchemaValidTypedPatchAndMigrationBeforeIO(t *testing.T) {
	// Arrange.
	patchRoot := t.TempDir()
	writeTestFile(t, patchRoot, "index.md",
		"---\nokf_version: \"0.2\"\n---\n# Knowledge\n- [Alpha](alpha.md)\n",
	)
	writeTestFile(t, patchRoot, "alpha.md",
		"---\ntype: Knowledge\n---\nClaim.\n",
	)
	migrationRoot := t.TempDir()
	writeTestFile(t, migrationRoot, "index.md",
		"---\nokf_version: \"0.2\"\n---\n# Knowledge\n- [Alpha](alpha.md)\n",
	)
	writeTestFile(t, migrationRoot, "alpha.md",
		"---\ntype: Knowledge\n---\nClaim.\n",
	)

	payload := rawArgumentString(strings.Repeat("x", 1<<20))
	computation := rawComputationContractArguments{
		Mode:              "inline",
		Runtime:           "go",
		Parameters:        []rawComputationParameterArguments{},
		InlineComputation: payload,
		Executor: rawComputationExecutorArguments{
			Resource: "urn:executor",
			Receipt:  []string{"urn:receipt"},
		},
		Attester: rawComputationAttesterArguments{Resource: "urn:attester"},
	}
	patchOperations := make([]rawPatchComputationOperationArguments, 64)
	migrationComputations := make([]rawMigrationComputationArguments, 64)
	for index := range patchOperations {
		patchOperations[index] = rawPatchComputationOperationArguments{
			Kind:                "put_attested_computation",
			ConceptID:           "alpha",
			AttestedComputation: computation,
		}
		migrationComputations[index] = rawMigrationComputationArguments{
			ConceptID: "alpha",
			Contract:  computation,
		}
	}
	patchArguments := func(apply bool) map[string]any {
		arguments := map[string]any{
			"bundle_path": patchRoot,
			"actor":       "mcp:raw-byte-budget",
			"operations":  patchOperations,
		}
		if apply {
			arguments["expected_revision"] = "sha256:" + strings.Repeat("a", 64)
			arguments["expected_plan_digest"] = "sha256:" + strings.Repeat("b", 64)
		}
		return arguments
	}
	migrationArguments := func(apply bool) map[string]any {
		arguments := map[string]any{
			"bundle_path":       migrationRoot,
			"from":              "auto",
			"timestamp_policy":  "preserve",
			"citation_mappings": []rawMigrationCitationArguments{},
			"computations":      migrationComputations,
		}
		if apply {
			arguments["expected_source"] = testMigrationSourceValue(
				mutation.MigrationVersionV02,
				string(mutation.MigrationTransitionTargetNoop),
			)
		}
		return arguments
	}
	tests := []struct {
		name     string
		toolName string
		root     string
		handler  server.ToolHandlerFunc
		args     map[string]any
	}{
		{
			name:     "patch preview",
			toolName: "preview_concept_patch",
			root:     patchRoot,
			handler:  handlePreviewConceptPatch,
			args:     patchArguments(false),
		},
		{
			name:     "patch apply",
			toolName: "apply_concept_patch",
			root:     patchRoot,
			handler:  handleApplyConceptPatch,
			args:     patchArguments(true),
		},
		{
			name:     "migration preview",
			toolName: "preview_v02_migration",
			root:     migrationRoot,
			handler:  handlePreviewV02Migration,
			args:     migrationArguments(false),
		},
		{
			name:     "migration apply",
			toolName: "apply_v02_migration",
			root:     migrationRoot,
			handler:  handleApplyV02Migration,
			args:     migrationArguments(true),
		},
	}
	sourceOpens, sourceReads, storeOpens := 0, 0, 0
	ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
		sourceOpen: func() { sourceOpens++ },
		sourceRead: func() { sourceReads++ },
		storeOpen:  func() { storeOpens++ },
	})

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateContract(test.toolName+".input", test.args); err != nil {
				t.Fatalf("test input must be schema-valid: %v", err)
			}
			before := protocolTreeSnapshot(t, test.root)
			handlerCalls := 0
			guarded := contractToolHandler(test.toolName, func(
				ctx context.Context,
				request mcp.CallToolRequest,
			) (*mcp.CallToolResult, error) {
				handlerCalls++
				return test.handler(ctx, request)
			})
			result, err := guarded(ctx, mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name:      test.toolName,
					Arguments: test.args,
				},
			})
			if err != nil {
				t.Fatalf("handler protocol error = %v", err)
			}
			assertSchemaErrorEnvelope(t, result, "resource_limit")
			if handlerCalls != 0 {
				t.Fatalf("handler calls = %d, want 0", handlerCalls)
			}
			if sourceOpens != 0 || sourceReads != 0 || storeOpens != 0 {
				t.Fatalf(
					"I/O counts = source-open:%d source-read:%d store-open:%d, want all zero",
					sourceOpens,
					sourceReads,
					storeOpens,
				)
			}
			if after := protocolTreeSnapshot(t, test.root); !reflect.DeepEqual(after, before) {
				t.Fatalf("bundle changed:\nbefore=%#v\nafter=%#v", before, after)
			}
			if _, statErr := os.Lstat(filepath.Join(test.root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("transactional store was opened: %v", statErr)
			}
		})
	}
}

func TestMigrationValidUnicodeAndExactLegacyEntryApplyWithoutReplacement(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	const legacyEntry = "https://example.test/источник\r\n\tпродолжение — №1"
	const document = "---\r\n" +
		"type: Knowledge\r\n" +
		"timestamp: 2026-06-25T09:00:00Z\r\n" +
		"---\r\n" +
		"Claim [1].\r\n\r\n" +
		"# Citations\r\n\r\n" +
		"[1] " + legacyEntry + "\r\n"
	writeTestFile(t, root, "index.md",
		"---\nokf_version: \"0.1\"\n---\n# Knowledge\n- [Alpha](alpha.md)\n",
	)
	writeTestFile(t, root, "alpha.md", document)
	arguments := map[string]any{
		"bundle_path":               root,
		"from":                      "auto",
		"generated_by":              "process:migration",
		"timestamp_policy":          "remove_after_copy",
		"timestamp_conflict_policy": "reject",
		"citation_mappings": []any{map[string]any{
			"path": "alpha.md",
			"entries": []any{map[string]any{
				"legacy_number": 1,
				"legacy_entry":  legacyEntry,
				"source_id":     "source-1",
				"title":         "Спецификация — редакция №1",
				"resource":      "urn:пример:источник?редакция=№1",
			}},
		}},
	}

	// Act.
	preview := callHandler(
		t,
		contractToolHandler("preview_v02_migration", handlePreviewV02Migration),
		arguments,
	)

	// Assert.
	if preview.IsError {
		t.Fatalf("preview returned error: %s", resultText(t, preview))
	}
	plan := preview.StructuredContent.(migrationPreviewResponse)
	if plan.Status != "applicable" || plan.Proof == nil || plan.PlanDigest == "" {
		t.Fatalf("preview = %#v", plan)
	}
	if got := arguments["citation_mappings"].([]any)[0].(map[string]any)["entries"].([]any)[0].(map[string]any)["legacy_entry"]; got != legacyEntry {
		t.Fatalf("legacy_entry changed during preview: %q", got)
	}

	applyArguments := cloneArguments(arguments)
	applyArguments["proof"] = plan.Proof
	applyArguments["expected_plan_digest"] = plan.PlanDigest
	applyArguments["expected_source"] = plan.Source
	applied := callHandler(
		t,
		contractToolHandler("apply_v02_migration", handleApplyV02Migration),
		applyArguments,
	)
	if applied.IsError {
		t.Fatalf("apply returned error: %s", resultText(t, applied))
	}

	for path, content := range protocolTreeSnapshot(t, root) {
		if strings.Contains(content, "\uFFFD") || bytes.Contains([]byte(content), []byte{0xef, 0xbf, 0xbd}) {
			t.Fatalf("%s contains Unicode replacement character: %q", path, content)
		}
	}
	updated := readTestFile(t, root, "alpha.md")
	for _, exact := range []string{
		"Спецификация — редакция №1",
		"urn:пример:источник?редакция=№1",
		"Claim [^source-1]",
	} {
		if !strings.Contains(updated, exact) {
			t.Fatalf("migrated alpha.md omitted %q:\n%s", exact, updated)
		}
	}
	if got := applyArguments["citation_mappings"].([]any)[0].(map[string]any)["entries"].([]any)[0].(map[string]any)["legacy_entry"]; got != legacyEntry {
		t.Fatalf("legacy_entry changed during apply: %q", got)
	}
}
