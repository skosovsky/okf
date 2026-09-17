package mcpserver

import (
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/mutation"
)

func TestMigrationApplyRejectsNoncanonicalExpectedSourceProvenanceBeforeIO(t *testing.T) {
	// Arrange.
	declaredRoot := newMigrationAuthorizationPreflightRoot(t)
	declaredApply := migrationProvenanceApplyArguments(
		t,
		migrationAuthorizationPreflightArguments(declaredRoot),
	)

	explicitRoot := t.TempDir()
	writeTestFile(t, explicitRoot, "a.md", "---\ntype: Note\n---\n\nA.\n")
	explicitArguments := migrationAuthorizationPreflightArguments(explicitRoot)
	explicitArguments["from"] = mutation.MigrationVersionV01
	explicitArguments["timestamp_policy"] = "preserve"
	explicitApply := migrationProvenanceApplyArguments(t, explicitArguments)

	legacyRoot := t.TempDir()
	writeTestFile(
		t,
		legacyRoot,
		"index.md",
		"# Knowledge\n\n- [A](a.md)\n- [B](b.md)\n",
	)
	writeTestFile(
		t,
		legacyRoot,
		"a.md",
		"---\ntype: Note\ntimestamp: 2026-07-28T00:00:00Z\n---\n\nA.\n",
	)
	writeTestFile(
		t,
		legacyRoot,
		"b.md",
		"---\ntype: Note\ntimestamp: 2026-07-29T00:00:00Z\n---\n\nB.\n",
	)
	legacyArguments := migrationAuthorizationPreflightArguments(legacyRoot)
	legacyArguments["generated_at"] = []any{
		map[string]any{"concept_id": "a", "at": "2026-07-28T00:00:00Z"},
		map[string]any{"concept_id": "b", "at": "2026-07-29T00:00:00Z"},
	}
	legacyApply := migrationProvenanceApplyArguments(t, legacyArguments)
	legacySource := migrationSourceTupleMap(t, legacyApply["expected_source"])
	legacyCandidates := legacySource["candidates"].([]any)
	if len(legacyCandidates) < 2 {
		t.Fatalf("legacy-probe candidates = %#v, want at least two", legacyCandidates)
	}
	firstCandidate := deepCloneMap(t, legacyCandidates[0].(map[string]any))

	targetRoot := t.TempDir()
	writeTestFile(t, targetRoot, "a.md", "---\ntype: Note\n---\n\nA.\n")
	targetApply := migrationProvenanceApplyArguments(
		t,
		migrationAuthorizationPreflightArguments(targetRoot),
	)

	type provenanceCase struct {
		name   string
		root   string
		base   map[string]any
		code   string
		mutate func(map[string]any, map[string]any)
	}
	cases := []provenanceCase{
		{
			name: "declaration validity",
			root: declaredRoot,
			base: declaredApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["declaration_valid"] = false
			},
		},
		{
			name: "declaration presence",
			root: declaredRoot,
			base: declaredApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["declaration_present"] = false
			},
		},
		{
			name: "declaration missing version",
			root: declaredRoot,
			base: declaredApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["declared_version"] = nil
			},
		},
		{
			name: "declaration empty raw value",
			root: declaredRoot,
			base: declaredApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["declaration_raw"] = ""
			},
		},
		{
			name: "declaration raw value mismatch",
			root: declaredRoot,
			base: declaredApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["declaration_raw"] = mutation.MigrationVersionV02
			},
		},
		{
			name: "unknown resolution provenance",
			root: declaredRoot,
			base: declaredApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["resolution_source"] = "unknown"
			},
		},
		{
			name: "declared provenance requires auto selector",
			root: declaredRoot,
			base: declaredApply,
			mutate: func(arguments, source map[string]any) {
				arguments["from"] = mutation.MigrationVersionV01
				source["requested_selector"] = mutation.MigrationVersionV01
			},
		},
		{
			name: "declared provenance forbids candidates",
			root: declaredRoot,
			base: declaredApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["candidates"] = []any{deepCloneMap(t, firstCandidate)}
			},
		},
		{
			name: "explicit provenance requires explicit selector",
			root: explicitRoot,
			base: explicitApply,
			mutate: func(arguments, source map[string]any) {
				arguments["from"] = mutation.MigrationSelectorAuto
				source["requested_selector"] = mutation.MigrationSelectorAuto
			},
		},
		{
			name: "explicit provenance binds matching declaration",
			root: explicitRoot,
			base: explicitApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["declaration_present"] = true
				source["declaration_valid"] = true
				source["declaration_raw"] = mutation.MigrationVersionV02
				source["declared_version"] = mutation.MigrationVersionV02
			},
		},
		{
			name: "explicit provenance forbids candidates",
			root: explicitRoot,
			base: explicitApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["candidates"] = []any{deepCloneMap(t, firstCandidate)}
			},
		},
		{
			name: "legacy probe forbids declaration",
			root: legacyRoot,
			base: legacyApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["declaration_present"] = true
				source["declaration_valid"] = true
				source["declaration_raw"] = mutation.MigrationVersionV01
				source["declared_version"] = mutation.MigrationVersionV01
			},
		},
		{
			name: "legacy probe requires candidates",
			root: legacyRoot,
			base: legacyApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["candidates"] = []any{}
			},
		},
		{
			name: "legacy candidate kind",
			root: legacyRoot,
			base: legacyApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["candidates"].([]any)[0].(map[string]any)["kind"] = "unknown"
			},
		},
		{
			name: "legacy candidate canonical path",
			root: legacyRoot,
			base: legacyApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["candidates"].([]any)[0].(map[string]any)["path"] = "../a.md"
			},
		},
		{
			name: "legacy candidate Markdown path",
			root: legacyRoot,
			base: legacyApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["candidates"].([]any)[0].(map[string]any)["path"] = "a.txt"
			},
		},
		{
			name: "legacy candidate negative span",
			root: legacyRoot,
			base: legacyApply,
			mutate: func(_ map[string]any, source map[string]any) {
				location := source["candidates"].([]any)[0].(map[string]any)["location"].(map[string]any)
				location["start"] = -1
			},
		},
		{
			name: "legacy candidate inverted span",
			root: legacyRoot,
			base: legacyApply,
			mutate: func(_ map[string]any, source map[string]any) {
				location := source["candidates"].([]any)[0].(map[string]any)["location"].(map[string]any)
				location["start"] = 1
				location["end"] = 0
			},
		},
		{
			name: "legacy candidate order",
			root: legacyRoot,
			base: legacyApply,
			mutate: func(_ map[string]any, source map[string]any) {
				candidates := source["candidates"].([]any)
				candidates[0], candidates[1] = candidates[1], candidates[0]
			},
		},
		{
			name: "legacy candidate duplicate",
			root: legacyRoot,
			base: legacyApply,
			mutate: func(_ map[string]any, source map[string]any) {
				candidates := source["candidates"].([]any)
				candidates[1] = deepCloneMap(t, candidates[0].(map[string]any))
			},
		},
		{
			name: "default provenance forbids declaration",
			root: targetRoot,
			base: targetApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["declaration_present"] = true
				source["declaration_valid"] = true
				source["declaration_raw"] = mutation.MigrationVersionV02
				source["declared_version"] = mutation.MigrationVersionV02
			},
		},
		{
			name: "default provenance forbids candidates",
			root: targetRoot,
			base: targetApply,
			mutate: func(_ map[string]any, source map[string]any) {
				source["candidates"] = []any{deepCloneMap(t, firstCandidate)}
			},
		},
		{
			name: "future target noop is outside task20",
			root: targetRoot,
			base: targetApply,
			code: "plan_mismatch",
			mutate: func(_ map[string]any, source map[string]any) {
				source["declaration_present"] = true
				source["declaration_valid"] = true
				source["declaration_raw"] = "9.0"
				source["declared_version"] = "9.0"
				source["resolved_source"] = "9.0"
				source["resolution_source"] = string(mutation.MigrationResolutionFuture)
				source["from_version"] = "9.0"
				source["to_version"] = "9.0"
			},
		},
	}

	// Act and assert.
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := protocolTreeSnapshot(t, test.root)
			for _, surface := range []string{"wrapped", "direct"} {
				t.Run(surface, func(t *testing.T) {
					arguments := deepCloneMap(t, test.base)
					source := migrationSourceTupleMap(t, arguments["expected_source"])
					arguments["expected_source"] = source
					test.mutate(arguments, source)
					wantCode := test.code
					if wantCode == "" {
						wantCode = "invalid_request"
					}
					if err := validateContract("apply_v02_migration.input", arguments); err != nil {
						if surface == "wrapped" {
							wantCode = "schema_validation"
						} else {
							wantCode = "invalid_request"
						}
					}
					events := make([]string, 0, 3)
					ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
						sourceOpen: func() { events = append(events, "source_open") },
						sourceRead: func() { events = append(events, "source_read") },
						storeOpen:  func() { events = append(events, "store_open") },
					})

					var result *mcp.CallToolResult
					if surface == "wrapped" {
						result = callMigrationContract(
							t,
							ctx,
							"apply_v02_migration",
							arguments,
						)
					} else {
						var err error
						result, err = handleApplyV02Migration(
							ctx,
							mcp.CallToolRequest{Params: mcp.CallToolParams{
								Name:      "apply_v02_migration",
								Arguments: arguments,
							}},
						)
						if err != nil {
							t.Fatalf("handleApplyV02Migration() error = %v", err)
						}
					}

					assertSchemaErrorEnvelope(t, result, wantCode)
					if len(events) != 0 {
						t.Fatalf("I/O events = %#v, want none", events)
					}
					if after := protocolTreeSnapshot(t, test.root); !reflect.DeepEqual(after, before) {
						t.Fatalf(
							"invalid provenance changed tree:\nbefore=%#v\nafter=%#v",
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

func migrationProvenanceApplyArguments(
	t *testing.T,
	arguments map[string]any,
) map[string]any {
	t.Helper()
	plan := migrationSourceTuplePreview(
		t,
		arguments,
		"",
		"",
	)
	apply := cloneArguments(arguments)
	apply["expected_source"] = plan.Source
	if plan.Source.Transition == string(mutation.MigrationTransitionV01ToV02) {
		if plan.Proof == nil || plan.PlanDigest == "" {
			t.Fatalf("v0.1 preview authorization = %#v", plan)
		}
		apply["proof"] = plan.Proof
		apply["expected_plan_digest"] = plan.PlanDigest
	}
	return apply
}
