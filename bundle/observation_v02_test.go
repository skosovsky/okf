package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAttributionObservationPreservesFamilyAndOrderedEntryEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		frontmatter     string
		wantFamily      SemanticFamilyObservation
		wantEntryShapes []YAMLValueShape
		wantValid       []bool
		wantAmbiguous   []bool
	}{
		{
			name: "absent",
			wantFamily: SemanticFamilyObservation{
				Shape: YAMLShapeAbsent, ResolvedShape: YAMLShapeAbsent, ShapeValid: true,
			},
		},
		{
			name:        "present empty",
			frontmatter: "sources: []\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeSequence, ResolvedShape: YAMLShapeSequence,
				ShapeValid: true, Raw: "[]", ResolvedRaw: "[]",
			},
		},
		{
			name:        "present invalid",
			frontmatter: "sources: wrong\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				Raw: "wrong", ResolvedRaw: "wrong",
			},
		},
		{
			name: "ambiguous duplicate family",
			frontmatter: "sources: [{id: first, resource: one}]\n" +
				"sources: [{id: second, resource: two}]\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Ambiguous: true, Shape: YAMLShapeAmbiguous,
				ResolvedShape: YAMLShapeAmbiguous,
			},
		},
		{
			name: "alias family and ordered entry shapes",
			frontmatter: "source: &source {id: Spec, resource: one}\n" +
				"source_list: &source_list [*source, wrong, {id: missing-resource}]\n" +
				"sources: *source_list\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeAlias, ResolvedShape: YAMLShapeSequence,
				ShapeValid: true, Raw: "*source_list",
				ResolvedRaw: "&source_list [*source, wrong, {id: missing-resource}]",
				ViaAlias:    true,
			},
			wantEntryShapes: []YAMLValueShape{YAMLShapeAlias, YAMLShapeScalar, YAMLShapeMapping},
			wantValid:       []bool{true, false, false},
			wantAmbiguous:   []bool{false, false, false},
		},
		{
			name: "merged family and ambiguous member",
			frontmatter: "defaults: &defaults\n" +
				"  sources:\n" +
				"    - {id: duplicate, resource: one, resource: two}\n" +
				"<<: *defaults\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeSequence, ResolvedShape: YAMLShapeSequence,
				ShapeValid: true, Raw: "- {id: duplicate, resource: one, resource: two}",
				ResolvedRaw: "- {id: duplicate, resource: one, resource: two}",
				ViaAlias:    true, ViaMerge: true,
			},
			wantEntryShapes: []YAMLValueShape{YAMLShapeMapping},
			wantValid:       []bool{false},
			wantAmbiguous:   []bool{true},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			document, err := ParseDocument("---\ntype: Note\n" + test.frontmatter +
				"---\nClaim.[^spec]\n\n[^spec]: evidence\n")
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got, err := document.AttributionObservationContext(context.Background())

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Family, test.wantFamily) {
				t.Fatalf("Family = %#v, want %#v", got.Family, test.wantFamily)
			}
			if len(got.Entries) != len(test.wantEntryShapes) {
				t.Fatalf("Entries = %#v, want %d", got.Entries, len(test.wantEntryShapes))
			}
			for index, entry := range got.Entries {
				if entry.Index != index || entry.Family.Shape != test.wantEntryShapes[index] ||
					entry.Valid != test.wantValid[index] ||
					entry.Ambiguous != test.wantAmbiguous[index] {
					t.Fatalf("Entries[%d] = %#v", index, entry)
				}
			}
		})
	}
}

func TestAttributionObservationProjectsNormalizedTypedJoinAndOwnsReturnedData(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := ParseDocument(`---
type: Note
sources:
  - id: Spec
    resource: one
    usage_count: 2
  - id: spec
    resource: two
---
Claim.[^spec]

[^spec]: evidence
`)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	first := document.AttributionObservation()
	contextValue, contextErr := document.AttributionObservationContext(context.Background())

	// Assert.
	if contextErr != nil || !reflect.DeepEqual(first, contextValue) {
		t.Fatalf("context parity = (%#v, %v), want %#v", contextValue, contextErr, first)
	}
	if len(first.Entries) != 2 {
		t.Fatalf("Entries = %#v", first.Entries)
	}
	for index, entry := range first.Entries {
		if !entry.Valid || entry.NormalizedID != "spec" || entry.Value == nil ||
			entry.Value.NormalizedID != "spec" || len(entry.Value.Sources) != 2 ||
			len(entry.Value.References) != 1 || len(entry.Value.Definitions) != 1 {
			t.Fatalf("Entries[%d] = %#v", index, entry)
		}
	}
	first.Entries[0].NormalizedID = "mutated"
	first.Entries[0].Source.Value.ID = "mutated"
	*first.Entries[0].Source.Value.UsageCount = 999
	first.Entries[0].Value.Sources[0].ID = "mutated"
	first.Entries[0].Value.References[0].ID = "mutated"
	first.Entries = append(first.Entries, AttributionEntryObservation{})

	again := document.AttributionObservation()
	if !reflect.DeepEqual(again, contextValue) {
		t.Fatalf("caller mutation leaked:\nagain=%#v\nwant=%#v", again, contextValue)
	}
}

func TestAttributionObservationPropagatesNestedUsageWindowAmbiguity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		window string
	}{
		{
			name: "duplicate from",
			window: "from: 2026-01-01\n" +
				"      from: 2026-01-02\n" +
				"      to: 2026-01-31\n",
		},
		{
			name: "duplicate to through alias",
			window: "<<: &window_defaults\n" +
				"        from: 2026-01-01\n" +
				"        to: 2026-01-30\n" +
				"        to: 2026-01-31\n",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			document, err := ParseDocument("---\ntype: Note\nsources:\n" +
				"  - id: spec\n" +
				"    resource: source.md\n" +
				"    usage_window:\n" +
				"      " + test.window +
				"---\nClaim.[^spec]\n")
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got := document.AttributionObservation()

			// Assert.
			if len(got.Entries) != 1 || !got.Entries[0].Ambiguous ||
				got.Entries[0].Valid || got.Entries[0].Value != nil ||
				got.Entries[0].NormalizedID != "" ||
				got.Entries[0].Source.Valid {
				t.Fatalf("AttributionObservation() = %#v", got)
			}
		})
	}
}

func TestAttributionObservationContextCancellationReturnsNoPartialState(t *testing.T) {
	t.Parallel()

	var frontmatter strings.Builder
	frontmatter.WriteString("type: Note\nsources:\n")
	for index := 0; index < 512; index++ {
		frontmatter.WriteString("  - {id: source-")
		frontmatter.WriteString(zeroPaddedDecimal(index, 4))
		frontmatter.WriteString(", resource: scope}\n")
	}
	document, err := ParseDocument("---\n" + frontmatter.String() + "---\n")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	preCanceled, preErr := document.AttributionObservationContext(canceled)
	midCanceled, midErr := document.AttributionObservationContext(
		&cancelAfterErrChecksContext{allowed: 32},
	)

	// Assert.
	if !errors.Is(preErr, context.Canceled) ||
		!reflect.DeepEqual(preCanceled, AttributionFamilyObservation{}) {
		t.Fatalf("pre-canceled = (%#v, %v)", preCanceled, preErr)
	}
	if !errors.Is(midErr, context.Canceled) ||
		!reflect.DeepEqual(midCanceled, AttributionFamilyObservation{}) {
		t.Fatalf("mid-canceled = (%#v, %v)", midCanceled, midErr)
	}
}

func TestStaleAfterObservationPreservesSemanticAndTemporalStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		yaml       string
		wantFamily SemanticFamilyObservation
		wantState  TemporalValueState
		wantRaw    string
	}{
		{
			name: "absent",
			wantFamily: SemanticFamilyObservation{
				Shape: YAMLShapeAbsent, ResolvedShape: YAMLShapeAbsent, ShapeValid: true,
			},
			wantState: TemporalAbsent,
		},
		{
			name: "valid",
			yaml: "stale_after: 2026-07-29\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true, Raw: "2026-07-29", ResolvedRaw: "2026-07-29",
			},
			wantState: TemporalValid,
			wantRaw:   "2026-07-29",
		},
		{
			name: "malformed scalar",
			yaml: "stale_after: tomorrow\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true, Raw: "tomorrow", ResolvedRaw: "tomorrow",
			},
			wantState: TemporalMalformed,
			wantRaw:   "tomorrow",
		},
		{
			name: "present empty scalar",
			yaml: "stale_after: ''\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true,
			},
			wantState: TemporalMalformed,
		},
		{
			name: "malformed mapping retains raw",
			yaml: "stale_after: {date: tomorrow}\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeMapping, ResolvedShape: YAMLShapeMapping,
				Raw: "{date: tomorrow}", ResolvedRaw: "{date: tomorrow}",
			},
			wantState: TemporalMalformed,
			wantRaw:   "{date: tomorrow}",
		},
		{
			name: "alias",
			yaml: "deadline: &deadline 2026-07-29\nstale_after: *deadline\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeAlias, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true, Raw: "*deadline", ResolvedRaw: "2026-07-29",
				ViaAlias: true,
			},
			wantState: TemporalValid,
			wantRaw:   "2026-07-29",
		},
		{
			name: "merge",
			yaml: "defaults: &defaults {stale_after: 2026-07-29}\n<<: *defaults\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true, Raw: "2026-07-29", ResolvedRaw: "2026-07-29",
				ViaAlias: true, ViaMerge: true,
			},
			wantState: TemporalValid,
			wantRaw:   "2026-07-29",
		},
		{
			name: "duplicate",
			yaml: "stale_after: 2026-07-28\nstale_after: 2026-07-29\n",
			wantFamily: SemanticFamilyObservation{
				Present: true, Ambiguous: true, Shape: YAMLShapeAmbiguous,
				ResolvedShape: YAMLShapeAmbiguous,
			},
			wantState: TemporalMalformed,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got, err := frontmatter.StaleAfterObservationContext(context.Background())

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Family, test.wantFamily) ||
				got.Value.State != test.wantState || got.Value.Raw != test.wantRaw {
				t.Fatalf("StaleAfterObservationContext() = %#v, want family=%#v state=%q raw=%q",
					got, test.wantFamily, test.wantState, test.wantRaw)
			}
			if legacy := frontmatter.StaleAfterObservation(); !reflect.DeepEqual(legacy, got) {
				t.Fatalf("non-context = %#v, context = %#v", legacy, got)
			}
		})
	}
}

func TestStaleAfterObservationContextCancellationIsFailClosed(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("stale_after: 2026-07-29\n")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	got, err := frontmatter.StaleAfterObservationContext(ctx)

	// Assert.
	if !errors.Is(err, context.Canceled) || got != (StaleAfterObservation{}) {
		t.Fatalf("StaleAfterObservationContext() = (%#v, %v)", got, err)
	}
}

func TestDuplicateMergeKeyObservationsFailClosedAcrossTypedSurfaces(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := ParseDocument(`---
type: Attested Computation
runtime: sql
computation: query.sql
left: &left
  stale_after: 2026-07-28
  sources: [{id: left, resource: left.md}]
  executor: {resource: left.md, receipt: [left]}
right: &right
  stale_after: 2026-07-29
  sources: [{id: right, resource: right.md}]
  executor: {resource: right.md, receipt: [right]}
<<: *left
<<: *right
---
Claim.[^left]
`)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	stale := document.Frontmatter.StaleAfterObservation()
	staleValue := document.StaleAfterValue()
	attribution := document.AttributionObservation()
	executor := document.Frontmatter.ExecutorReceiptObservation()
	contract := document.Frontmatter.ComputationContractState()
	typed, typedOK := document.AttestedComputation()

	// Assert.
	for name, family := range map[string]SemanticFamilyObservation{
		"stale_after": stale.Family,
		"sources":     attribution.Family,
		"executor":    executor.ExecutorFamily,
	} {
		if !family.Present || !family.Ambiguous ||
			family.Shape != YAMLShapeAmbiguous ||
			family.ResolvedShape != YAMLShapeAmbiguous ||
			family.Raw != "" || family.ResolvedRaw != "" ||
			!family.ViaAlias || !family.ViaMerge || family.ShapeValid {
			t.Fatalf("%s family = %#v", name, family)
		}
	}
	if stale.Value.State != TemporalMalformed || stale.Value.Raw != "" ||
		staleValue.State != TemporalMalformed || staleValue.Raw != "" {
		t.Fatalf("stale values = observation:%#v legacy:%#v", stale.Value, staleValue)
	}
	if attribution.Entries != nil {
		t.Fatalf("ambiguous attribution entries leaked: %#v", attribution.Entries)
	}
	if executor.ResourceFamily != (SemanticFamilyObservation{}) ||
		executor.ReceiptFamily != (SemanticFamilyObservation{}) ||
		executor.Items != nil || executor.Values != nil || executor.Value != nil ||
		executor.Valid || executor.HasValue {
		t.Fatalf("ambiguous executor leaked: %#v", executor)
	}
	if !contract.Present || contract.Valid || !contract.Executor.Present ||
		contract.Executor.Valid || contract.Value.Executor != nil ||
		typedOK || !reflect.DeepEqual(typed, AttestedComputationContract{}) {
		t.Fatalf("typed contract leaked: contract=%#v typed=(%#v,%t)",
			contract, typed, typedOK)
	}
}

func TestExplicitFamiliesOverrideDuplicateMergeKeysInObservations(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter(`left: &left
  stale_after: 2026-07-28
  executor: {resource: left.md, receipt: [left]}
right: &right
  stale_after: 2026-07-29
  executor: {resource: right.md, receipt: [right]}
<<: *left
<<: *right
stale_after: 2026-07-30
executor: {resource: explicit.md, receipt: [explicit]}
`)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	stale := frontmatter.StaleAfterObservation()
	executor := frontmatter.ExecutorReceiptObservation()

	// Assert.
	if stale.Family.Ambiguous || stale.Family.ViaMerge || stale.Family.ViaAlias ||
		!stale.Family.ShapeValid || stale.Value.State != TemporalValid ||
		stale.Value.Raw != "2026-07-30" {
		t.Fatalf("explicit stale_after = %#v", stale)
	}
	if executor.ExecutorFamily.Ambiguous || executor.ExecutorFamily.ViaMerge ||
		executor.ExecutorFamily.ViaAlias || !executor.Valid ||
		executor.Value == nil || executor.Value.Resource != "explicit.md" ||
		!reflect.DeepEqual(executor.Value.Receipt, []string{"explicit"}) {
		t.Fatalf("explicit executor = %#v", executor)
	}
}

func TestExecutorReceiptObservationPreservesFamiliesEntriesAndEffectiveValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		yaml             string
		wantExecutor     SemanticFamilyObservation
		wantResource     SemanticFamilyObservation
		wantReceipt      SemanticFamilyObservation
		wantItemValidity []bool
		wantValues       []string
		wantValid        bool
		wantValue        bool
	}{
		{
			name: "absent",
			wantExecutor: SemanticFamilyObservation{
				Shape: YAMLShapeAbsent, ResolvedShape: YAMLShapeAbsent, ShapeValid: true,
			},
		},
		{
			name: "wrong executor shape",
			yaml: "executor: wrong\n",
			wantExecutor: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				Raw: "wrong", ResolvedRaw: "wrong",
			},
		},
		{
			name: "duplicate executor",
			yaml: "executor: {resource: one}\nexecutor: {resource: two}\n",
			wantExecutor: SemanticFamilyObservation{
				Present: true, Ambiguous: true, Shape: YAMLShapeAmbiguous,
				ResolvedShape: YAMLShapeAmbiguous,
			},
		},
		{
			name: "duplicate receipt",
			yaml: "executor:\n  resource: run.md\n  receipt: [one]\n  receipt: [two]\n",
			wantExecutor: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeMapping, ResolvedShape: YAMLShapeMapping,
				ShapeValid:  true,
				Raw:         "resource: run.md\nreceipt: [one]\nreceipt: [two]",
				ResolvedRaw: "resource: run.md\nreceipt: [one]\nreceipt: [two]",
			},
			wantResource: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true, Raw: "run.md", ResolvedRaw: "run.md",
			},
			wantReceipt: SemanticFamilyObservation{
				Present: true, Ambiguous: true, Shape: YAMLShapeAmbiguous,
				ResolvedShape: YAMLShapeAmbiguous,
			},
		},
		{
			name: "receipt absent",
			yaml: "executor: {resource: run.md}\n",
			wantExecutor: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeMapping, ResolvedShape: YAMLShapeMapping,
				ShapeValid: true, Raw: "{resource: run.md}", ResolvedRaw: "{resource: run.md}",
			},
			wantResource: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true, Raw: "run.md", ResolvedRaw: "run.md",
			},
			wantReceipt: SemanticFamilyObservation{
				Shape: YAMLShapeAbsent, ResolvedShape: YAMLShapeAbsent, ShapeValid: true,
			},
			wantValid: true,
			wantValue: true,
		},
		{
			name: "receipt present empty",
			yaml: "executor: {resource: run.md, receipt: []}\n",
			wantExecutor: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeMapping, ResolvedShape: YAMLShapeMapping,
				ShapeValid: true, Raw: "{resource: run.md, receipt: []}",
				ResolvedRaw: "{resource: run.md, receipt: []}",
			},
			wantResource: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true, Raw: "run.md", ResolvedRaw: "run.md",
			},
			wantReceipt: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeSequence, ResolvedShape: YAMLShapeSequence,
				ShapeValid: true, Raw: "[]", ResolvedRaw: "[]",
			},
			wantValues: []string{},
			wantValid:  true,
			wantValue:  true,
		},
		{
			name: "receipt wrong shape",
			yaml: "executor: {resource: run.md, receipt: wrong}\n",
			wantExecutor: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeMapping, ResolvedShape: YAMLShapeMapping,
				ShapeValid: true, Raw: "{resource: run.md, receipt: wrong}",
				ResolvedRaw: "{resource: run.md, receipt: wrong}",
			},
			wantResource: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true, Raw: "run.md", ResolvedRaw: "run.md",
			},
			wantReceipt: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				Raw: "wrong", ResolvedRaw: "wrong",
			},
		},
		{
			name: "duplicate resource",
			yaml: "executor: {resource: one, resource: two, receipt: [result]}\n",
			wantExecutor: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeMapping, ResolvedShape: YAMLShapeMapping,
				ShapeValid: true, Raw: "{resource: one, resource: two, receipt: [result]}",
				ResolvedRaw: "{resource: one, resource: two, receipt: [result]}",
			},
			wantResource: SemanticFamilyObservation{
				Present: true, Ambiguous: true, Shape: YAMLShapeAmbiguous,
				ResolvedShape: YAMLShapeAmbiguous,
			},
			wantReceipt: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeSequence, ResolvedShape: YAMLShapeSequence,
				ShapeValid: true, Raw: "[result]", ResolvedRaw: "[result]",
			},
			wantItemValidity: []bool{true},
			wantValues:       []string{"result"},
		},
		{
			name: "merged aliased executor members",
			yaml: "field: &field result\n" +
				"receipt_values: &receipt_values [*field, rows]\n" +
				"executor_defaults: &executor_defaults\n" +
				"  resource: run.md\n" +
				"  receipt: *receipt_values\n" +
				"root_defaults: &root_defaults\n" +
				"  executor: *executor_defaults\n" +
				"<<: *root_defaults\n",
			wantExecutor: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeAlias, ResolvedShape: YAMLShapeMapping,
				ShapeValid: true, Raw: "*executor_defaults",
				ResolvedRaw: "&executor_defaults\nresource: run.md\nreceipt: *receipt_values",
				ViaAlias:    true, ViaMerge: true,
			},
			wantResource: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true, Raw: "run.md", ResolvedRaw: "run.md",
			},
			wantReceipt: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeAlias, ResolvedShape: YAMLShapeSequence,
				ShapeValid: true, Raw: "*receipt_values",
				ResolvedRaw: "&receipt_values [*field, rows]", ViaAlias: true,
			},
			wantItemValidity: []bool{true, true},
			wantValues:       []string{"result", "rows"},
			wantValid:        true,
			wantValue:        true,
		},
		{
			name: "mixed invalid receipt entries",
			yaml: "field: &field one\nexecutor:\n  resource: run.md\n" +
				"  receipt: [*field, 2, '', {name: wrong}]\n",
			wantExecutor: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeMapping, ResolvedShape: YAMLShapeMapping,
				ShapeValid:  true,
				Raw:         "resource: run.md\nreceipt: [*field, 2, '', {name: wrong}]",
				ResolvedRaw: "resource: run.md\nreceipt: [*field, 2, '', {name: wrong}]",
			},
			wantResource: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeScalar, ResolvedShape: YAMLShapeScalar,
				ShapeValid: true, Raw: "run.md", ResolvedRaw: "run.md",
			},
			wantReceipt: SemanticFamilyObservation{
				Present: true, Shape: YAMLShapeSequence, ResolvedShape: YAMLShapeSequence,
				ShapeValid: true, Raw: "[*field, 2, '', {name: wrong}]",
				ResolvedRaw: "[*field, 2, '', {name: wrong}]",
			},
			wantItemValidity: []bool{true, false, false, false},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got, err := frontmatter.ExecutorReceiptObservationContext(context.Background())

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.ExecutorFamily, test.wantExecutor) ||
				!reflect.DeepEqual(got.ResourceFamily, test.wantResource) ||
				!reflect.DeepEqual(got.ReceiptFamily, test.wantReceipt) {
				t.Fatalf("family mismatch:\ngot=%#v\nwant executor=%#v resource=%#v receipt=%#v",
					got, test.wantExecutor, test.wantResource, test.wantReceipt)
			}
			if got.Valid != test.wantValid || (got.Value != nil) != test.wantValue ||
				!reflect.DeepEqual(got.Values, test.wantValues) {
				t.Fatalf("value state = %#v", got)
			}
			if len(got.Items) != len(test.wantItemValidity) {
				t.Fatalf("Items = %#v", got.Items)
			}
			if len(got.ItemFamilies) != len(test.wantItemValidity) {
				t.Fatalf("ItemFamilies = %#v", got.ItemFamilies)
			}
			for index, valid := range test.wantItemValidity {
				if got.Items[index].Valid != valid {
					t.Fatalf("Items[%d] = %#v, want valid=%t", index, got.Items[index], valid)
				}
			}
			if legacy := frontmatter.ExecutorReceiptObservation(); !reflect.DeepEqual(legacy, got) {
				t.Fatalf("non-context = %#v, context = %#v", legacy, got)
			}
		})
	}
}

func TestExecutorReceiptTerminalAliasParityAcrossBundleReadSurfaces(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter(`type: Attested Computation
field: &field result
receipt: &receipt [*field]
executor:
  resource: run.md
  receipt: *receipt
computation: query.sql
`)
	if err != nil {
		t.Fatal(err)
	}
	document := NewDocument(frontmatter, "")

	// Act.
	contract := frontmatter.ComputationContractState()
	typed, typedOK := frontmatter.AttestedComputation()
	observation := frontmatter.ExecutorReceiptObservation()
	documentState := document.AttestedComputationState()

	// Assert.
	if !contract.Valid || !contract.Executor.Valid || !contract.Executor.Receipt.Valid ||
		len(contract.Executor.Receipt.Items) != 1 ||
		!contract.Executor.Receipt.Items[0].Valid ||
		!reflect.DeepEqual(contract.Executor.Receipt.Values, []string{"result"}) ||
		!typedOK || typed.Executor == nil ||
		!reflect.DeepEqual(typed.Executor.Receipt, []string{"result"}) ||
		!observation.Valid || observation.Value == nil ||
		!reflect.DeepEqual(observation.Values, []string{"result"}) ||
		!documentState.ContractState.Valid ||
		!documentState.ContractState.Executor.Receipt.Valid ||
		documentState.Contract.Executor == nil ||
		!reflect.DeepEqual(documentState.Contract.Executor.Receipt, []string{"result"}) {
		t.Fatalf("alias parity failed: contract=%#v typed=(%#v,%t) observation=%#v document=%#v",
			contract, typed, typedOK, observation, documentState)
	}
}

func TestExecutorReceiptObservationOwnsReturnedSlicesAndCancelsWithoutPartialState(t *testing.T) {
	t.Parallel()

	// Arrange.
	var receipt strings.Builder
	receipt.WriteString("executor:\n  resource: run.md\n  receipt:\n")
	for index := 0; index < 512; index++ {
		receipt.WriteString("    - field_")
		receipt.WriteString(zeroPaddedDecimal(index, 4))
		receipt.WriteByte('\n')
	}
	frontmatter, err := ParseFrontmatter(receipt.String())
	if err != nil {
		t.Fatal(err)
	}
	want := frontmatter.ExecutorReceiptObservation()
	first := frontmatter.ExecutorReceiptObservation()
	first.Items[0].Value = "mutated"
	first.Values[0] = "mutated"
	first.Value.Receipt[0] = "mutated"
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	again := frontmatter.ExecutorReceiptObservation()
	preCanceled, preErr := frontmatter.ExecutorReceiptObservationContext(canceled)
	midCanceled, midErr := frontmatter.ExecutorReceiptObservationContext(
		&cancelAfterErrChecksContext{allowed: 32},
	)

	// Assert.
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("caller mutation leaked:\nagain=%#v\nwant=%#v", again, want)
	}
	if !errors.Is(preErr, context.Canceled) ||
		!reflect.DeepEqual(preCanceled, ExecutorReceiptObservation{}) {
		t.Fatalf("pre-canceled = (%#v, %v)", preCanceled, preErr)
	}
	if !errors.Is(midErr, context.Canceled) ||
		!reflect.DeepEqual(midCanceled, ExecutorReceiptObservation{}) {
		t.Fatalf("mid-canceled = (%#v, %v)", midCanceled, midErr)
	}
}

func TestStaleAfterObservationValidTimeIsIndependentAcrossCalls(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("stale_after: 2026-07-29\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	first := frontmatter.StaleAfterObservation()
	second := frontmatter.StaleAfterObservation()

	// Assert.
	want := time.Date(2026, time.July, 29, 0, 0, 0, 0, time.UTC)
	if !first.Value.Time.Equal(want) || !reflect.DeepEqual(first, second) {
		t.Fatalf("observations = %#v / %#v, want time %v", first, second, want)
	}
}
