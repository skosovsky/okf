package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUsageWindowContextAccessorsMatchLegacySemanticMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
	}{
		{name: "absent", yaml: "type: Note\n"},
		{name: "valid", yaml: "type: Note\nusage_window: {from: 2026-08-01, to: 2026-08-07}\n"},
		{name: "malformed-family", yaml: "type: Note\nusage_window: wrong\n"},
		{name: "partial", yaml: "type: Note\nusage_window: {from: 2026-08-01}\n"},
		{name: "reverse-range", yaml: "type: Note\nusage_window: {from: 2026-08-07, to: 2026-08-01}\n"},
		{name: "terminal-alias", yaml: "type: Note\nwindow: &window {from: 2026-08-01, to: 2026-08-07}\nusage_window: *window\n"},
		{name: "merge", yaml: "type: Note\nbase: &base {usage_window: {from: 2026-08-01, to: 2026-08-07}}\n<<: *base\n"},
		{name: "duplicate-is-ambiguous", yaml: "type: Note\nusage_window: {from: 2026-08-01, to: 2026-08-07}\nusage_window: {from: 2026-08-02, to: 2026-08-08}\n"},
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
			wantState := frontmatter.UsageWindowState()
			wantValue, wantValid := frontmatter.UsageWindow()

			// Act.
			state, stateErr := frontmatter.UsageWindowStateContext(context.Background())
			value, valid, valueErr := frontmatter.UsageWindowContext(context.Background())
			documentState, documentStateErr := document.UsageWindowStateContext(context.Background())
			documentValue, documentValid, documentValueErr := document.UsageWindowContext(context.Background())

			// Assert.
			if stateErr != nil || valueErr != nil || documentStateErr != nil || documentValueErr != nil {
				t.Fatalf("context errors = %v, %v, %v, %v", stateErr, valueErr, documentStateErr, documentValueErr)
			}
			if !reflect.DeepEqual(state, wantState) || !reflect.DeepEqual(documentState, wantState) ||
				value != wantValue || documentValue != wantValue || valid != wantValid || documentValid != wantValid {
				t.Fatalf("usage parity failed: state=%#v/%#v value=%#v/%#v valid=%v/%v", state, wantState, value, wantValue, valid, wantValid)
			}
			if !reflect.DeepEqual(document.UsageWindowState(), wantState) {
				t.Fatalf("Document.UsageWindowState() = %#v, want %#v", document.UsageWindowState(), wantState)
			}
		})
	}
}

func TestEffectiveUsageWindowContextPreservesOverrideAndFallbackSemantics(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\nusage_window: {from: 2026-08-01, to: 2026-08-07}\n")
	if err != nil {
		t.Fatal(err)
	}
	document := NewDocument(frontmatter, "body")
	override := &UsageWindow{From: "custom-from", To: "custom-to"}
	typedSource := ProvenanceSource{UsageWindow: override}
	malformedOverride := ProvenanceSourceState{UsageWindow: UsageWindowState{
		Present: true,
		From:    DateValue{Raw: "bad", State: TemporalMalformed},
	}}
	absentOverride := ProvenanceSourceState{}

	// Act.
	typed, typedValid, typedErr := frontmatter.EffectiveUsageWindowContext(context.Background(), typedSource)
	documentTyped, documentTypedValid, documentTypedErr := document.EffectiveUsageWindowContext(context.Background(), typedSource)
	malformed, malformedErr := frontmatter.EffectiveUsageWindowStateContext(context.Background(), malformedOverride)
	fallback, fallbackErr := frontmatter.EffectiveUsageWindowStateContext(context.Background(), absentOverride)
	documentFallback, documentFallbackErr := document.EffectiveUsageWindowStateContext(context.Background(), absentOverride)

	// Assert.
	if typedErr != nil || documentTypedErr != nil || malformedErr != nil || fallbackErr != nil || documentFallbackErr != nil {
		t.Fatalf("context errors = %v, %v, %v, %v, %v", typedErr, documentTypedErr, malformedErr, fallbackErr, documentFallbackErr)
	}
	if !typedValid || !documentTypedValid || typed != *override || documentTyped != *override {
		t.Fatalf("typed override = %#v/%v document=%#v/%v", typed, typedValid, documentTyped, documentTypedValid)
	}
	if !reflect.DeepEqual(malformed, malformedOverride.UsageWindow) || !reflect.DeepEqual(fallback, frontmatter.UsageWindowState()) || !reflect.DeepEqual(documentFallback, fallback) {
		t.Fatalf("state override/fallback = malformed %#v fallback %#v document %#v", malformed, fallback, documentFallback)
	}
	if legacy, ok := frontmatter.EffectiveUsageWindow(typedSource); !ok || legacy != typed {
		t.Fatalf("legacy typed override = %#v/%v", legacy, ok)
	}
	if legacy := frontmatter.EffectiveUsageWindowState(malformedOverride); !reflect.DeepEqual(legacy, malformed) {
		t.Fatalf("legacy state override = %#v", legacy)
	}
	// The returned value is detached from the caller's pointer.
	typed.From = "mutated"
	if override.From != "custom-from" {
		t.Fatalf("result mutated caller override: %#v", override)
	}
}

func TestUsageWindowContextCancelsArbitraryLengthDateWithoutPartialState(t *testing.T) {
	t.Parallel()

	// Arrange.
	large := strings.Repeat("2", 1<<20)
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "usage_window"},
		{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "from"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: large},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "to"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "2026-08-07"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stateProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.UsageWindowStateContext(stateProbe); probeErr != nil || !got.Present || got.From.Raw != large {
		t.Fatalf("state probe = (%#v, %v)", got, probeErr)
	}
	valueProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if _, _, probeErr := frontmatter.UsageWindowContext(valueProbe); probeErr != nil {
		t.Fatal(probeErr)
	}

	// Act.
	state, stateErr := frontmatter.UsageWindowStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() / 2})
	value, valid, valueErr := frontmatter.UsageWindowContext(&cancelAfterErrChecksContext{allowed: valueProbe.checks.Load() / 2})

	// Assert.
	if !reflect.DeepEqual(state, UsageWindowState{}) || !errors.Is(stateErr, context.Canceled) {
		t.Fatalf("mid-date state = (%#v, %v)", state, stateErr)
	}
	if value != (UsageWindow{}) || valid || !errors.Is(valueErr, context.Canceled) {
		t.Fatalf("mid-date value = (%#v, %v, %v)", value, valid, valueErr)
	}
}

func TestUsageWindowContextCancelsLargeRootWithoutPartialResults(t *testing.T) {
	t.Parallel()

	// Arrange.
	content := make([]*yaml.Node, 0, 24_002)
	for index := 0; index < 12_000; index++ {
		content = append(content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "extension-" + zeroPaddedDecimal(index, 5)},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"},
		)
	}
	content = append(content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "usage_window"},
		&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "from"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "2026-08-01"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "to"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "2026-08-07"},
		}},
	)
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	stateProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.UsageWindowStateContext(stateProbe); probeErr != nil || !got.Valid {
		t.Fatalf("state probe = (%#v, %v)", got, probeErr)
	}
	valueProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, valid, probeErr := frontmatter.UsageWindowContext(valueProbe); probeErr != nil || !valid || got.From != "2026-08-01" {
		t.Fatalf("value probe = (%#v, %v, %v)", got, valid, probeErr)
	}

	// Act.
	preState, preStateErr := frontmatter.UsageWindowStateContext(canceled)
	preValue, preValid, preValueErr := frontmatter.UsageWindowContext(canceled)
	midState, midStateErr := frontmatter.UsageWindowStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() / 2})
	midValue, midValid, midValueErr := frontmatter.UsageWindowContext(&cancelAfterErrChecksContext{allowed: valueProbe.checks.Load() / 2})
	nearState, nearStateErr := frontmatter.UsageWindowStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() - 1})
	nearValue, nearValid, nearValueErr := frontmatter.UsageWindowContext(&cancelAfterErrChecksContext{allowed: valueProbe.checks.Load() - 1})

	// Assert.
	for _, result := range []struct {
		name  string
		state UsageWindowState
		err   error
	}{
		{name: "pre state", state: preState, err: preStateErr},
		{name: "mid state", state: midState, err: midStateErr},
		{name: "near state", state: nearState, err: nearStateErr},
	} {
		if !reflect.DeepEqual(result.state, UsageWindowState{}) || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%#v, %v)", result.name, result.state, result.err)
		}
	}
	for _, result := range []struct {
		name  string
		value UsageWindow
		valid bool
		err   error
	}{
		{name: "pre value", value: preValue, valid: preValid, err: preValueErr},
		{name: "mid value", value: midValue, valid: midValid, err: midValueErr},
		{name: "near value", value: nearValue, valid: nearValid, err: nearValueErr},
	} {
		if result.value != (UsageWindow{}) || result.valid || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%#v, %v, %v)", result.name, result.value, result.valid, result.err)
		}
	}
}
