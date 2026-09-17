package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestComputationContextCancelsInsideLargeTypeAndContractScalars(t *testing.T) {
	large := strings.Repeat("x", 2<<20)
	tests := []struct {
		name   string
		source string
		act    func(context.Context, Document) (any, error)
		zero   any
	}{
		{
			name:   "exact type comparison",
			source: "---\ntype: " + large + "\n---\n",
			act: func(ctx context.Context, document Document) (any, error) {
				return document.IsAttestedComputationContext(ctx)
			},
			zero: false,
		},
		{
			name: "contract scalar projection",
			source: "---\ntype: Attested Computation\nruntime: " + large +
				"\ncomputation: query.sql\n---\n",
			act: func(ctx context.Context, document Document) (any, error) {
				return document.Frontmatter.ComputationContractStateContext(ctx)
			},
			zero: ComputationContractState{},
		},
		{
			name: "type gated document projection",
			source: "---\ntype: Attested Computation\nexecutor:\n  resource: " + large +
				"\ncomputation: query.sql\n---\n",
			act: func(ctx context.Context, document Document) (any, error) {
				return document.AttestedComputationStateContext(ctx)
			},
			zero: AttestedComputationState{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			document, err := ParseDocument(test.source)
			if err != nil {
				t.Fatal(err)
			}
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			background, backgroundErr := test.act(probe, document)
			checks := probe.checks.Load()
			if backgroundErr != nil || checks < 8 {
				t.Fatalf("background probe = (%#v, %v), checks=%d", background, backgroundErr, checks)
			}
			cancelDuringScalar := &cancelAfterErrChecksContext{allowed: checks / 2}

			// Act.
			got, gotErr := test.act(cancelDuringScalar, document)

			// Assert.
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, test.zero) {
				t.Fatalf("canceled projection = (%#v, %v), want (%#v, context.Canceled)", got, gotErr, test.zero)
			}
			if gotChecks := cancelDuringScalar.checks.Load(); gotChecks <= 1 {
				t.Fatalf("cancellation occurred only at entry: checks=%d", gotChecks)
			}
		})
	}
}

func TestComputationContextBackgroundParityAndOwnership(t *testing.T) {
	// Arrange.
	document, err := ParseDocument("---\n" +
		"type: Attested Computation\n" +
		"runtime: go\n" +
		"parameters:\n  - {name: input, type: string, required: true}\n" +
		"computation: query.sql\n" +
		"executor: {resource: runner.md, receipt: [stdout, digest]}\n" +
		"attester: {resource: attester.md}\n---\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	legacy := document.AttestedComputationState()
	contextState, contextErr := document.AttestedComputationStateContext(context.Background())
	contractLegacy := document.Frontmatter.ComputationContractState()
	contractContext, contractErr := document.Frontmatter.ComputationContractStateContext(context.Background())

	// Assert.
	if contextErr != nil || contractErr != nil || !reflect.DeepEqual(contextState, legacy) ||
		!reflect.DeepEqual(contractContext, contractLegacy) {
		t.Fatalf("Background parity: state=(%#v, %v) legacy=%#v contract=(%#v, %v) legacy=%#v",
			contextState, contextErr, legacy, contractContext, contractErr, contractLegacy)
	}
	contextState.Contract.Executor.Receipt[0] = "mutated"
	contextState.ContractState.Value.Executor.Receipt[0] = "mutated"
	contextState.ContractState.Executor.Receipt.Values[0] = "mutated"
	contextState.ContractState.Executor.Receipt.Items[0].Value = "mutated"
	contextState.Contract.Parameters[0].Name = "mutated"
	contextState.ContractState.Parameters[0].Value.Name = "mutated"
	again, err := document.AttestedComputationStateContext(context.Background())
	if err != nil || !reflect.DeepEqual(again, legacy) {
		t.Fatalf("caller mutation leaked: again=(%#v, %v), want %#v", again, err, legacy)
	}
}

func TestComputationAttributionContextCancelsInsideLargeProvenanceSource(t *testing.T) {
	// Arrange.
	large := strings.Repeat("r", 2<<20)
	document, err := ParseDocument("---\ntype: Note\nsources:\n" +
		"  - id: spec\n    resource: " + large +
		"\n    title: title\n    author: author\n" +
		"    usage_count: 2\n    last_modified: 2026-01-01\n" +
		"    usage_window: {from: 2026-01-01, to: 2026-01-02}\n" +
		"---\nClaim.[^spec]\n\n[^spec]: evidence\n")
	if err != nil {
		t.Fatal(err)
	}
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	baseline, baselineErr := document.AttributionObservationContext(probe)
	checks := probe.checks.Load()
	if baselineErr != nil || checks < 8 {
		t.Fatalf("background probe = (%#v, %v), checks=%d", baseline, baselineErr, checks)
	}
	cancelDuringSource := &cancelAfterErrChecksContext{allowed: checks / 2}

	// Act.
	got, gotErr := document.AttributionObservationContext(cancelDuringSource)

	// Assert.
	if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, AttributionFamilyObservation{}) {
		t.Fatalf("canceled observation = (%#v, %v), want zero/context.Canceled", got, gotErr)
	}
	if gotChecks := cancelDuringSource.checks.Load(); gotChecks <= 1 {
		t.Fatalf("cancellation occurred only at entry: checks=%d", gotChecks)
	}
}

func TestProvenanceSourceOwnerCancelsInsideLargeScalar(t *testing.T) {
	// Arrange.
	large := strings.Repeat("r", 2<<20)
	frontmatter, err := ParseFrontmatter("sources:\n  - id: spec\n    resource: " + large + "\n")
	if err != nil {
		t.Fatal(err)
	}
	sources, present := frontmatter.SemanticGet("sources")
	if !present || sources.Kind != yaml.SequenceNode || len(sources.Content) != 1 {
		t.Fatalf("sources = (%#v, %v)", sources, present)
	}
	node := sources.Content[0]
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	baseline, baselineErr := provenanceSourceStateFromNodeContext(probe, node)
	checks := probe.checks.Load()
	if baselineErr != nil || checks < 8 || !reflect.DeepEqual(baseline, provenanceSourceStateFromNode(node)) {
		t.Fatalf("owner baseline = (%#v, %v), checks=%d", baseline, baselineErr, checks)
	}
	cancelDuringScalar := &cancelAfterErrChecksContext{allowed: checks / 2}

	// Act.
	got, gotErr := provenanceSourceStateFromNodeContext(cancelDuringScalar, node)

	// Assert.
	if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, ProvenanceSourceState{}) {
		t.Fatalf("canceled source = (%#v, %v), want zero/context.Canceled", got, gotErr)
	}
}

func TestComputationAttributionContextBackgroundParityAndOwnership(t *testing.T) {
	// Arrange.
	document, err := ParseDocument("---\ntype: Note\nsources:\n" +
		"  - id: spec\n    resource: source.md\n    title: Title\n    author: Author\n" +
		"    usage_count: 2\n    last_modified: 2026-01-01\n" +
		"    usage_window: {from: 2026-01-01, to: 2026-01-02}\n" +
		"---\nClaim.[^spec]\n\n[^spec]: evidence\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	legacy := document.AttributionObservation()
	got, gotErr := document.AttributionObservationContext(context.Background())

	// Assert.
	if gotErr != nil || !reflect.DeepEqual(got, legacy) {
		t.Fatalf("Background parity = (%#v, %v), want %#v", got, gotErr, legacy)
	}
	*got.Entries[0].Source.Value.UsageCount = 999
	got.Entries[0].Source.Value.UsageWindow.From = "mutated"
	got.Entries[0].Source.Resource.Value = "mutated"
	got.Entries[0].Value.Sources[0].Resource = "mutated"
	got.Entries[0].Value.References[0].ID = "mutated"
	again, err := document.AttributionObservationContext(context.Background())
	if err != nil || !reflect.DeepEqual(again, legacy) {
		t.Fatalf("caller mutation leaked: again=(%#v, %v), want %#v", again, err, legacy)
	}
}
