package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSourceContextAccessorsMatchLegacySemanticProjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
	}{
		{name: "absent", yaml: "type: Note\n"},
		{name: "empty", yaml: "type: Note\nsources: []\n"},
		{name: "malformed", yaml: "type: Note\nsources: wrong\n"},
		{
			name: "direct",
			yaml: "type: Note\nsources:\n" +
				"  - id: source-a\n" +
				"    resource: scope/a\n" +
				"    usage_count: 3\n" +
				"    last_modified: 2026-08-07\n" +
				"    usage_window: {from: 2026-08-01, to: 2026-08-07}\n" +
				"  - {id: malformed-without-resource}\n",
		},
		{
			name: "merged",
			yaml: "type: Note\nbase: &base\n" +
				"  sources:\n" +
				"    - {id: source-a, resource: scope/a}\n" +
				"<<: *base\n",
		},
		{
			name: "duplicate-is-ambiguous",
			yaml: "type: Note\nsources: []\nsources:\n" +
				"  - {id: source-a, resource: scope/a}\n",
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
			document := NewDocument(frontmatter, "body")
			wantStates := frontmatter.SourceStates()
			wantSources := frontmatter.Sources()

			// Act.
			states, statesErr := frontmatter.SourceStatesContext(context.Background())
			sources, sourcesErr := frontmatter.SourcesContext(context.Background())
			documentStates, documentStatesErr := document.SourceStatesContext(context.Background())
			documentSources, documentSourcesErr := document.SourcesContext(context.Background())

			// Assert.
			if statesErr != nil || sourcesErr != nil || documentStatesErr != nil || documentSourcesErr != nil {
				t.Fatalf("context errors = %v, %v, %v, %v", statesErr, sourcesErr, documentStatesErr, documentSourcesErr)
			}
			if !reflect.DeepEqual(states, wantStates) || !reflect.DeepEqual(documentStates, wantStates) ||
				!reflect.DeepEqual(sources, wantSources) || !reflect.DeepEqual(documentSources, wantSources) {
				t.Fatalf("source context parity failed: states=%#v want=%#v sources=%#v want=%#v", states, wantStates, sources, wantSources)
			}
			if !reflect.DeepEqual(document.SourceStates(), wantStates) || !reflect.DeepEqual(document.Sources(), wantSources) {
				t.Fatalf("document legacy delegates differ: states=%#v sources=%#v", document.SourceStates(), document.Sources())
			}
		})
	}
}

func TestSourceContextAccessorsReturnDefensivePointers(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\nsources:\n" +
		"  - id: source-a\n" +
		"    resource: scope/a\n" +
		"    usage_count: 3\n" +
		"    usage_window: {from: 2026-08-01, to: 2026-08-07}\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	states, err := frontmatter.SourceStatesContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sources, err := frontmatter.SourcesContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	*states[0].Value.UsageCount = 99
	states[0].Value.UsageWindow.From = "mutated"
	*sources[0].UsageCount = 100
	sources[0].UsageWindow.To = "mutated"
	againStates, statesErr := frontmatter.SourceStatesContext(context.Background())
	againSources, sourcesErr := frontmatter.SourcesContext(context.Background())

	// Assert.
	if statesErr != nil || sourcesErr != nil {
		t.Fatalf("repeat errors = %v, %v", statesErr, sourcesErr)
	}
	if *againStates[0].Value.UsageCount != 3 || againStates[0].Value.UsageWindow.From != "2026-08-01" ||
		*againSources[0].UsageCount != 3 || againSources[0].UsageWindow.To != "2026-08-07" {
		t.Fatalf("caller mutation escaped: states=%#v sources=%#v", againStates, againSources)
	}
}

func TestSourceContextAccessorsCancelWithoutPartialResults(t *testing.T) {
	t.Parallel()

	// Arrange.
	var yaml strings.Builder
	yaml.WriteString("type: Note\nsources:\n")
	for index := 0; index < 512; index++ {
		yaml.WriteString("  - {id: source-")
		yaml.WriteString(zeroPaddedDecimal(index, 4))
		yaml.WriteString(", resource: scope}\n")
	}
	frontmatter, err := ParseFrontmatter(yaml.String())
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	statesProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.SourceStatesContext(statesProbe); probeErr != nil || len(got) != 512 {
		t.Fatalf("state probe = (%d, %v)", len(got), probeErr)
	}
	sourcesProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.SourcesContext(sourcesProbe); probeErr != nil || len(got) != 512 {
		t.Fatalf("source probe = (%d, %v)", len(got), probeErr)
	}

	// Act.
	preStates, preStatesErr := frontmatter.SourceStatesContext(canceled)
	preSources, preSourcesErr := frontmatter.SourcesContext(canceled)
	midStates, midStatesErr := frontmatter.SourceStatesContext(&cancelAfterErrChecksContext{allowed: statesProbe.checks.Load() / 2})
	midSources, midSourcesErr := frontmatter.SourcesContext(&cancelAfterErrChecksContext{allowed: sourcesProbe.checks.Load() - 1})

	// Assert.
	for _, result := range []struct {
		name  string
		value any
		err   error
	}{
		{name: "pre states", value: preStates, err: preStatesErr},
		{name: "pre sources", value: preSources, err: preSourcesErr},
		{name: "mid states", value: midStates, err: midStatesErr},
		{name: "mid sources", value: midSources, err: midSourcesErr},
	} {
		if !reflect.ValueOf(result.value).IsNil() || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%#v, %v), want nil/context.Canceled", result.name, result.value, result.err)
		}
	}
}

func TestVerificationContextAccessorsMatchLegacyAndTrustTier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
	}{
		{name: "absent", yaml: "type: Note\n"},
		{name: "bare", yaml: "type: Note\nverified: {by: process:runner/v1, at: 2026-08-07T01:02:03Z}\n"},
		{name: "list", yaml: "type: Note\nverified:\n  - {by: process:runner/v1, at: 2026-08-07T01:02:03Z}\n  - {by: human:sergey, at: 2026-08-07T02:03:04Z}\n"},
		{name: "malformed", yaml: "type: Note\nverified: [wrong, {by: '', at: bad}]\n"},
		{name: "duplicate-is-ambiguous", yaml: "type: Note\nverified: {by: human:a, at: 2026-08-07T01:02:03Z}\nverified: {by: human:b, at: 2026-08-07T01:02:03Z}\n"},
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
			document := NewDocument(frontmatter, "body")
			wantStates := frontmatter.VerificationStates()
			wantValues := frontmatter.Verifications()
			wantTier := frontmatter.TrustTier()

			// Act.
			states, statesErr := frontmatter.VerificationStatesContext(context.Background())
			values, valuesErr := frontmatter.VerificationsContext(context.Background())
			tier, tierErr := frontmatter.TrustTierContext(context.Background())
			documentStates, documentStatesErr := document.VerificationStatesContext(context.Background())
			documentValues, documentValuesErr := document.VerificationsContext(context.Background())
			documentTier, documentTierErr := document.TrustTierContext(context.Background())

			// Assert.
			if statesErr != nil || valuesErr != nil || tierErr != nil || documentStatesErr != nil || documentValuesErr != nil || documentTierErr != nil {
				t.Fatalf("context errors = %v, %v, %v, %v, %v, %v", statesErr, valuesErr, tierErr, documentStatesErr, documentValuesErr, documentTierErr)
			}
			if !reflect.DeepEqual(states, wantStates) || !reflect.DeepEqual(documentStates, wantStates) ||
				!reflect.DeepEqual(values, wantValues) || !reflect.DeepEqual(documentValues, wantValues) ||
				tier != wantTier || documentTier != wantTier {
				t.Fatalf("verification context parity failed: states=%#v/%#v values=%#v/%#v tier=%q/%q", states, wantStates, values, wantValues, tier, wantTier)
			}
			if !reflect.DeepEqual(document.VerificationStates(), wantStates) ||
				!reflect.DeepEqual(document.Verifications(), wantValues) || document.TrustTier() != wantTier {
				t.Fatalf("document legacy delegates differ")
			}
		})
	}
}

func TestVerificationContextAccessorsCancelWithoutPartialResults(t *testing.T) {
	t.Parallel()

	// Arrange.
	var yaml strings.Builder
	yaml.WriteString("type: Note\nverified:\n")
	for index := 0; index < 512; index++ {
		yaml.WriteString("  - {by: process:runner/v1, at: 2026-08-07T01:02:03Z}\n")
	}
	frontmatter, err := ParseFrontmatter(yaml.String())
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	statesProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.VerificationStatesContext(statesProbe); probeErr != nil || len(got) != 512 {
		t.Fatalf("state probe = (%d, %v)", len(got), probeErr)
	}
	valuesProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.VerificationsContext(valuesProbe); probeErr != nil || len(got) != 512 {
		t.Fatalf("value probe = (%d, %v)", len(got), probeErr)
	}
	tierProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.TrustTierContext(tierProbe); probeErr != nil || got != TrustMachineConfirmed {
		t.Fatalf("tier probe = (%q, %v)", got, probeErr)
	}

	// Act.
	preStates, preStatesErr := frontmatter.VerificationStatesContext(canceled)
	preValues, preValuesErr := frontmatter.VerificationsContext(canceled)
	preTier, preTierErr := frontmatter.TrustTierContext(canceled)
	midStates, midStatesErr := frontmatter.VerificationStatesContext(&cancelAfterErrChecksContext{allowed: statesProbe.checks.Load() / 2})
	midValues, midValuesErr := frontmatter.VerificationsContext(&cancelAfterErrChecksContext{allowed: valuesProbe.checks.Load() - 1})
	midTier, midTierErr := frontmatter.TrustTierContext(&cancelAfterErrChecksContext{allowed: tierProbe.checks.Load() - 1})

	// Assert.
	if preStates != nil || !errors.Is(preStatesErr, context.Canceled) ||
		preValues != nil || !errors.Is(preValuesErr, context.Canceled) ||
		preTier != "" || !errors.Is(preTierErr, context.Canceled) ||
		midStates != nil || !errors.Is(midStatesErr, context.Canceled) ||
		midValues != nil || !errors.Is(midValuesErr, context.Canceled) ||
		midTier != "" || !errors.Is(midTierErr, context.Canceled) {
		t.Fatalf("cancellation leaked partial state: pre=(%#v,%v,%#v,%v,%q,%v) mid=(%#v,%v,%#v,%v,%q,%v)", preStates, preStatesErr, preValues, preValuesErr, preTier, preTierErr, midStates, midStatesErr, midValues, midValuesErr, midTier, midTierErr)
	}
}
