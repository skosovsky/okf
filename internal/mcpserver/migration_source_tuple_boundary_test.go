package mcpserver

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/mutation"
)

func TestMigrationApplyRejectsImpossibleExpectedSourceTuplesBeforeIO(t *testing.T) {
	// Arrange.
	transitionRoot := newMigrationAuthorizationPreflightRoot(t)
	transitionArguments := migrationAuthorizationPreflightArguments(transitionRoot)
	transitionPlan := migrationSourceTuplePreview(
		t,
		transitionArguments,
		"applicable",
		mutation.MigrationTransitionV01ToV02,
	)
	transitionApply := cloneArguments(transitionArguments)
	transitionApply["expected_source"] = transitionPlan.Source
	transitionApply["proof"] = transitionPlan.Proof
	transitionApply["expected_plan_digest"] = transitionPlan.PlanDigest

	targetRoot := t.TempDir()
	writeTestFile(
		t,
		targetRoot,
		"index.md",
		"---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n",
	)
	writeTestFile(t, targetRoot, "alpha.md", "---\ntype: Knowledge\n---\n\nAlpha.\n")
	targetArguments := migrationAuthorizationPreflightArguments(targetRoot)
	targetPlan := migrationSourceTuplePreview(
		t,
		targetArguments,
		"noop",
		mutation.MigrationTransitionTargetNoop,
	)
	targetApply := cloneArguments(targetArguments)
	targetApply["expected_source"] = targetPlan.Source

	futureRoot := t.TempDir()
	writeTestFile(t, futureRoot, "index.md", "---\nokf_version: \"9.0\"\n---\n")
	futureArguments := migrationAuthorizationPreflightArguments(futureRoot)
	futurePlan := migrationSourceTuplePreview(
		t,
		futureArguments,
		"blocked",
		mutation.MigrationTransitionBlocked,
	)
	futureApply := cloneArguments(futureArguments)
	futureApply["expected_source"] = futurePlan.Source

	tests := []struct {
		name       string
		root       string
		base       map[string]any
		directCode string
		mutate     func(map[string]any, map[string]any)
	}{
		{
			name:       "canonical blocked transition",
			root:       futureRoot,
			base:       futureApply,
			directCode: "invalid_request",
			mutate:     func(_ map[string]any, _ map[string]any) {},
		},
		{
			name:       "malformed blocked transition",
			root:       transitionRoot,
			base:       transitionApply,
			directCode: "invalid_request",
			mutate: func(_ map[string]any, source map[string]any) {
				source["transition"] = string(mutation.MigrationTransitionBlocked)
			},
		},
		{
			name:       "applicable transition contains blocker",
			root:       transitionRoot,
			base:       transitionApply,
			directCode: "invalid_request",
			mutate: func(_ map[string]any, source map[string]any) {
				source["blockers"] = []any{map[string]any{
					"code": "tampered", "path": "alpha.md",
					"location": map[string]any{"start": 0, "end": 0},
					"message":  "tampered",
				}}
			},
		},
		{
			name:       "v0.1 transition selector is target",
			root:       transitionRoot,
			base:       transitionApply,
			directCode: "invalid_request",
			mutate: func(arguments, source map[string]any) {
				arguments["from"] = mutation.MigrationVersionV02
				source["requested_selector"] = mutation.MigrationVersionV02
			},
		},
		{
			name:       "v0.1 transition resolved source is target",
			root:       transitionRoot,
			base:       transitionApply,
			directCode: "invalid_request",
			mutate: func(_ map[string]any, source map[string]any) {
				source["resolved_source"] = mutation.MigrationVersionV02
			},
		},
		{
			name:       "v0.1 transition from version is target",
			root:       transitionRoot,
			base:       transitionApply,
			directCode: "invalid_request",
			mutate: func(_ map[string]any, source map[string]any) {
				source["from_version"] = mutation.MigrationVersionV02
			},
		},
		{
			name:       "v0.1 transition to version is legacy",
			root:       transitionRoot,
			base:       transitionApply,
			directCode: "invalid_request",
			mutate: func(_ map[string]any, source map[string]any) {
				source["to_version"] = mutation.MigrationVersionV01
			},
		},
		{
			name:       "target noop selector is legacy",
			root:       targetRoot,
			base:       targetApply,
			directCode: "invalid_request",
			mutate: func(arguments, source map[string]any) {
				arguments["from"] = mutation.MigrationVersionV01
				source["requested_selector"] = mutation.MigrationVersionV01
			},
		},
		{
			name:       "target noop resolved source is legacy",
			root:       targetRoot,
			base:       targetApply,
			directCode: "invalid_request",
			mutate: func(_ map[string]any, source map[string]any) {
				source["resolved_source"] = mutation.MigrationVersionV01
			},
		},
		{
			name:       "target noop from version is legacy",
			root:       targetRoot,
			base:       targetApply,
			directCode: "invalid_request",
			mutate: func(_ map[string]any, source map[string]any) {
				source["from_version"] = mutation.MigrationVersionV01
			},
		},
		{
			name:       "target noop to version is legacy",
			root:       targetRoot,
			base:       targetApply,
			directCode: "invalid_request",
			mutate: func(_ map[string]any, source map[string]any) {
				source["to_version"] = mutation.MigrationVersionV01
			},
		},
		{
			name:       "target noop carries proof",
			root:       targetRoot,
			base:       targetApply,
			directCode: "invalid_request",
			mutate: func(arguments, _ map[string]any) {
				arguments["proof"] = transitionPlan.Proof
			},
		},
		{
			name:       "target noop carries plan digest",
			root:       targetRoot,
			base:       targetApply,
			directCode: "invalid_request",
			mutate: func(arguments, _ map[string]any) {
				arguments["expected_plan_digest"] = transitionPlan.PlanDigest
			},
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := protocolTreeSnapshot(t, test.root)
			for _, surface := range []struct {
				name string
				code string
				call func(context.Context, map[string]any) *mcp.CallToolResult
			}{
				{
					name: "wrapped",
					code: "schema_validation",
					call: func(ctx context.Context, arguments map[string]any) *mcp.CallToolResult {
						return callMigrationContract(
							t,
							ctx,
							"apply_v02_migration",
							arguments,
						)
					},
				},
				{
					name: "direct",
					code: test.directCode,
					call: func(ctx context.Context, arguments map[string]any) *mcp.CallToolResult {
						result, err := handleApplyV02Migration(
							ctx,
							mcp.CallToolRequest{Params: mcp.CallToolParams{
								Name:      "apply_v02_migration",
								Arguments: arguments,
							}},
						)
						if err != nil {
							t.Fatalf("handleApplyV02Migration() error = %v", err)
						}
						return result
					},
				},
			} {
				t.Run(surface.name, func(t *testing.T) {
					arguments := deepCloneMap(t, test.base)
					source := migrationSourceTupleMap(t, arguments["expected_source"])
					arguments["expected_source"] = source
					test.mutate(arguments, source)
					events := make([]string, 0, 3)
					ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
						sourceOpen: func() { events = append(events, "source_open") },
						sourceRead: func() { events = append(events, "source_read") },
						storeOpen:  func() { events = append(events, "store_open") },
					})

					result := surface.call(ctx, arguments)

					assertSchemaErrorEnvelope(t, result, surface.code)
					if len(events) != 0 {
						t.Fatalf("I/O events = %#v, want none", events)
					}
					if after := protocolTreeSnapshot(t, test.root); !reflect.DeepEqual(after, before) {
						t.Fatalf(
							"invalid expected_source changed tree:\nbefore=%#v\nafter=%#v",
							before,
							after,
						)
					}
					assertNoMigrationMetadata(t, test.root)
				})
			}
		})
	}
}

func TestMigrationPreviewProducesCanonicalApplicableSourceTuples(t *testing.T) {
	// Arrange.
	transitionRoot := newMigrationAuthorizationPreflightRoot(t)
	targetRoot := t.TempDir()
	writeTestFile(t, targetRoot, "index.md", "---\nokf_version: \"0.2\"\n---\n")
	tests := []struct {
		name       string
		arguments  map[string]any
		status     string
		transition mutation.MigrationTransition
	}{
		{
			name:       "v0.1 transition",
			arguments:  migrationAuthorizationPreflightArguments(transitionRoot),
			status:     "applicable",
			transition: mutation.MigrationTransitionV01ToV02,
		},
		{
			name:       "target noop",
			arguments:  migrationAuthorizationPreflightArguments(targetRoot),
			status:     "noop",
			transition: mutation.MigrationTransitionTargetNoop,
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := migrationSourceTuplePreview(
				t,
				test.arguments,
				test.status,
				test.transition,
			)
			arguments := migrationArguments{
				From:           test.arguments["from"].(string),
				ExpectedSource: &plan.Source,
			}
			source, result := expectedMigrationSource(arguments)
			if result != nil {
				t.Fatalf("preview source rejected by apply boundary: %s", resultText(t, result))
			}
			if err := validateApplicableMigrationSourceTuple(source); err != nil {
				t.Fatalf("preview source tuple = %#v: %v", source, err)
			}
		})
	}
}

func migrationSourceTuplePreview(
	t *testing.T,
	arguments map[string]any,
	status string,
	transition mutation.MigrationTransition,
) migrationPreviewResponse {
	t.Helper()
	result := callHandler(t, handlePreviewV02Migration, arguments)
	if result.IsError {
		t.Fatalf("preview returned error: %s", resultText(t, result))
	}
	plan, ok := result.StructuredContent.(migrationPreviewResponse)
	if !ok {
		t.Fatalf("preview structuredContent = %T", result.StructuredContent)
	}
	if status != "" && plan.Status != status ||
		transition != "" && plan.Source.Transition != string(transition) {
		t.Fatalf("preview = %#v", plan)
	}
	return plan
}

func migrationSourceTupleMap(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(expected_source) error = %v", err)
	}
	var output map[string]any
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("Unmarshal(expected_source) error = %v", err)
	}
	return output
}
