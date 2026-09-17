package mcpserver

import (
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/mutation"
)

func TestMigrationApplyAuthorizationPreflightRejectsBeforeSourceAndStoreIO(t *testing.T) {
	// Arrange.
	root := newMigrationAuthorizationPreflightRoot(t)
	arguments := migrationAuthorizationPreflightArguments(root)
	preview := callMigrationContract(
		t,
		t.Context(),
		"preview_v02_migration",
		arguments,
	)
	if preview.IsError {
		t.Fatalf("preview returned error: %s", resultText(t, preview))
	}
	plan := preview.StructuredContent.(migrationPreviewResponse)
	if plan.Status != "applicable" || plan.Proof == nil || plan.PlanDigest == "" {
		t.Fatalf("preview authorization = %#v", plan)
	}
	validApply := cloneArguments(arguments)
	validApply["expected_source"] = plan.Source
	validApply["proof"] = plan.Proof
	validApply["expected_plan_digest"] = plan.PlanDigest
	before := protocolTreeSnapshot(t, root)
	alternateDigest := "sha256:" + strings.Repeat("a", 64)
	if alternateDigest == plan.PlanDigest {
		alternateDigest = "sha256:" + strings.Repeat("b", 64)
	}
	tests := []struct {
		name string
		code string
		edit func(map[string]any)
	}{
		{
			name: "canonical but mismatched expected plan digest",
			code: "plan_mismatch",
			edit: func(apply map[string]any) {
				apply["expected_plan_digest"] = alternateDigest
			},
		},
		{
			name: "proof request digest does not bind migration request",
			code: "plan_mismatch",
			edit: func(apply map[string]any) {
				proof := apply["proof"].(map[string]any)
				proof["request_digest"] = alternateDigest
			},
		},
		{
			name: "proof relation ref is noncanonical",
			code: "invalid_request",
			edit: func(apply map[string]any) {
				proof := apply["proof"].(map[string]any)
				proof["changed_refs"] = []any{`source\\#part`}
			},
		},
		{
			name: "proof relation ref order is noncanonical",
			code: "plan_mismatch",
			edit: func(apply map[string]any) {
				proof := apply["proof"].(map[string]any)
				proof["changed_refs"] = []any{"z", "a"}
			},
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			apply := deepCloneMap(t, validApply)
			test.edit(apply)
			if err := validateContract("apply_v02_migration.input", apply); err != nil {
				t.Fatalf("adversarial input is not schema-valid: %v", err)
			}
			events := make([]string, 0, 4)
			ctx := withMigrationIOObserver(t.Context(), migrationIOObserver{
				sourceOpen: func() { events = append(events, "source_open") },
				sourceRead: func() { events = append(events, "source_read") },
				storeOpen:  func() { events = append(events, "store_open") },
			})

			result := callMigrationContract(
				t,
				ctx,
				"apply_v02_migration",
				apply,
			)

			assertSchemaErrorEnvelope(t, result, test.code)
			if len(events) != 0 {
				t.Fatalf("I/O events = %#v, want none", events)
			}
			if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid authorization changed tree:\nbefore=%#v\nafter=%#v", before, after)
			}
			assertNoMigrationMetadata(t, root)
		})
	}
}

func TestMigrationAuthorizationPreflightPreservesValidIOOrderAndProoflessTargetNoop(t *testing.T) {
	// Arrange.
	root := newMigrationAuthorizationPreflightRoot(t)
	arguments := migrationAuthorizationPreflightArguments(root)
	previewEvents := make([]string, 0, 3)
	previewContext := withMigrationIOObserver(t.Context(), migrationIOObserver{
		sourceOpen: func() { previewEvents = append(previewEvents, "source_open") },
		sourceRead: func() { previewEvents = append(previewEvents, "source_read") },
		storeOpen:  func() { previewEvents = append(previewEvents, "store_open") },
	})

	// Act.
	preview := callMigrationContract(
		t,
		previewContext,
		"preview_v02_migration",
		arguments,
	)

	// Assert.
	if preview.IsError {
		t.Fatalf("preview returned error: %s", resultText(t, preview))
	}
	plan := preview.StructuredContent.(migrationPreviewResponse)
	if plan.Status != "applicable" || plan.Proof == nil || plan.PlanDigest == "" {
		t.Fatalf("preview authorization = %#v", plan)
	}
	wantPreviewEvents := []string{"source_open", "source_read", "source_read"}
	if !reflect.DeepEqual(previewEvents, wantPreviewEvents) {
		t.Fatalf("preview I/O events = %#v, want %#v", previewEvents, wantPreviewEvents)
	}

	// Arrange.
	applyArguments := cloneArguments(arguments)
	applyArguments["expected_source"] = plan.Source
	applyArguments["proof"] = plan.Proof
	applyArguments["expected_plan_digest"] = plan.PlanDigest
	applyEvents := make([]string, 0, 4)
	applyContext := withMigrationIOObserver(t.Context(), migrationIOObserver{
		sourceOpen: func() { applyEvents = append(applyEvents, "source_open") },
		sourceRead: func() { applyEvents = append(applyEvents, "source_read") },
		storeOpen:  func() { applyEvents = append(applyEvents, "store_open") },
	})

	// Act.
	applied := callMigrationContract(
		t,
		applyContext,
		"apply_v02_migration",
		applyArguments,
	)

	// Assert.
	if applied.IsError {
		t.Fatalf("apply returned error: %s", resultText(t, applied))
	}
	applyResponse := applied.StructuredContent.(migrationApplyResponse)
	if applyResponse.Status != "applied" {
		t.Fatalf("apply response = %#v", applyResponse)
	}
	wantApplyEvents := []string{"source_open", "source_read", "source_read", "store_open"}
	if !reflect.DeepEqual(applyEvents, wantApplyEvents) {
		t.Fatalf("apply I/O events = %#v, want %#v", applyEvents, wantApplyEvents)
	}

	// Arrange.
	targetPreview := callMigrationContract(
		t,
		t.Context(),
		"preview_v02_migration",
		arguments,
	)
	if targetPreview.IsError {
		t.Fatalf("target preview returned error: %s", resultText(t, targetPreview))
	}
	targetPlan := targetPreview.StructuredContent.(migrationPreviewResponse)
	if targetPlan.Status != "noop" ||
		targetPlan.Source.Transition != string(mutation.MigrationTransitionTargetNoop) ||
		targetPlan.Proof != nil ||
		targetPlan.PlanDigest != "" {
		t.Fatalf("target preview = %#v", targetPlan)
	}
	targetApplyArguments := cloneArguments(arguments)
	targetApplyArguments["expected_source"] = targetPlan.Source
	targetEvents := make([]string, 0, 3)
	targetContext := withMigrationIOObserver(t.Context(), migrationIOObserver{
		sourceOpen: func() { targetEvents = append(targetEvents, "source_open") },
		sourceRead: func() { targetEvents = append(targetEvents, "source_read") },
		storeOpen:  func() { targetEvents = append(targetEvents, "store_open") },
	})

	// Act.
	targetApplied := callMigrationContract(
		t,
		targetContext,
		"apply_v02_migration",
		targetApplyArguments,
	)

	// Assert.
	if targetApplied.IsError {
		t.Fatalf("proofless target apply returned error: %s", resultText(t, targetApplied))
	}
	targetResponse := targetApplied.StructuredContent.(migrationApplyResponse)
	if targetResponse.Status != "noop" ||
		targetResponse.PlanDigest != "" ||
		len(targetResponse.ChangedPaths) != 0 {
		t.Fatalf("proofless target apply response = %#v", targetResponse)
	}
	wantTargetEvents := []string{"source_open", "source_read", "source_read"}
	if !reflect.DeepEqual(targetEvents, wantTargetEvents) {
		t.Fatalf("target apply I/O events = %#v, want %#v", targetEvents, wantTargetEvents)
	}
}

func newMigrationAuthorizationPreflightRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "index.md",
		"---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n",
	)
	writeTestFile(t, root, "alpha.md",
		"---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nAlpha.\n",
	)
	return root
}

func migrationAuthorizationPreflightArguments(root string) map[string]any {
	return map[string]any{
		"bundle_path":               root,
		"from":                      "auto",
		"generated_by":              "process:migration",
		"timestamp_policy":          "remove_after_copy",
		"timestamp_conflict_policy": "reject",
		"citation_mappings":         []any{},
		"computations":              []any{},
	}
}
