package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestContentChangeContextAccessorsMatchLegacyPrecedenceMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		wantState TemporalValueState
		wantRaw   string
	}{
		{name: "absent", yaml: "type: Note\n", wantState: TemporalAbsent},
		{name: "legacy-timestamp", yaml: "type: Note\ntimestamp: 2026-08-01T01:02:03Z\n", wantState: TemporalValid, wantRaw: "2026-08-01T01:02:03Z"},
		{name: "generated-wins", yaml: "type: Note\ntimestamp: 2026-08-01T01:02:03Z\ngenerated: {by: human:sergey, at: 2026-08-07T01:02:03Z}\n", wantState: TemporalValid, wantRaw: "2026-08-07T01:02:03Z"},
		{name: "actorless-generated-at-wins", yaml: "type: Note\ntimestamp: 2026-08-01T01:02:03Z\ngenerated: {at: 2026-08-07T01:02:03Z}\n", wantState: TemporalValid, wantRaw: "2026-08-07T01:02:03Z"},
		{name: "generated-without-at-suppresses-timestamp", yaml: "type: Note\ntimestamp: 2026-08-01T01:02:03Z\ngenerated: {by: human:sergey}\n", wantState: TemporalAbsent},
		{name: "malformed-generated-suppresses-timestamp", yaml: "type: Note\ntimestamp: 2026-08-01T01:02:03Z\ngenerated: wrong\n", wantState: TemporalMalformed, wantRaw: "wrong"},
		{name: "malformed-generated-at", yaml: "type: Note\ngenerated: {by: human:sergey, at: bad}\n", wantState: TemporalMalformed, wantRaw: "bad"},
		{name: "generated-alias", yaml: "type: Note\nevent: &event {at: 2026-08-07T01:02:03Z}\ngenerated: *event\n", wantState: TemporalValid, wantRaw: "2026-08-07T01:02:03Z"},
		{name: "generated-merge", yaml: "type: Note\nbase: &base {generated: {at: 2026-08-07T01:02:03Z}}\n<<: *base\n", wantState: TemporalValid, wantRaw: "2026-08-07T01:02:03Z"},
		{name: "duplicate-generated-is-malformed", yaml: "type: Note\ngenerated: {at: 2026-08-07T01:02:03Z}\ngenerated: {at: 2026-08-08T01:02:03Z}\n", wantState: TemporalMalformed},
		{name: "timestamp-alias", yaml: "type: Note\ntime: &time 2026-08-01T01:02:03Z\ntimestamp: *time\n", wantState: TemporalValid, wantRaw: "2026-08-01T01:02:03Z"},
		{name: "timestamp-merge", yaml: "type: Note\nbase: &base {timestamp: 2026-08-01T01:02:03Z}\n<<: *base\n", wantState: TemporalValid, wantRaw: "2026-08-01T01:02:03Z"},
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
			wantGenerated := frontmatter.GeneratedAtValue()
			wantEffective := frontmatter.EffectiveContentChangeTimeValue()
			wantString, wantValid := frontmatter.EffectiveContentChangeTime()

			// Act.
			generated, generatedErr := frontmatter.GeneratedAtValueContext(context.Background())
			effective, effectiveErr := frontmatter.EffectiveContentChangeTimeValueContext(context.Background())
			value, valid, valueErr := frontmatter.EffectiveContentChangeTimeContext(context.Background())
			documentGenerated, documentGeneratedErr := document.GeneratedAtValueContext(context.Background())
			documentEffective, documentEffectiveErr := document.EffectiveContentChangeTimeValueContext(context.Background())
			documentValue, documentValid, documentValueErr := document.EffectiveContentChangeTimeContext(context.Background())

			// Assert.
			if generatedErr != nil || effectiveErr != nil || valueErr != nil || documentGeneratedErr != nil || documentEffectiveErr != nil || documentValueErr != nil {
				t.Fatalf("context errors = %v, %v, %v, %v, %v, %v", generatedErr, effectiveErr, valueErr, documentGeneratedErr, documentEffectiveErr, documentValueErr)
			}
			if !reflect.DeepEqual(generated, wantGenerated) || !reflect.DeepEqual(documentGenerated, wantGenerated) ||
				!reflect.DeepEqual(effective, wantEffective) || !reflect.DeepEqual(documentEffective, wantEffective) ||
				value != wantString || documentValue != wantString || valid != wantValid || documentValid != wantValid {
				t.Fatalf("content time parity failed: generated=%#v/%#v effective=%#v/%#v value=%q/%q valid=%v/%v", generated, wantGenerated, effective, wantEffective, value, wantString, valid, wantValid)
			}
			if effective.State != test.wantState || effective.Raw != test.wantRaw {
				t.Fatalf("effective observation = %#v, want state=%q raw=%q", effective, test.wantState, test.wantRaw)
			}
		})
	}
}

func TestContentChangeContextCancelsArbitraryFractionWithoutPartialResults(t *testing.T) {
	t.Parallel()

	// Arrange.
	at := "2026-08-07T01:02:03." + strings.Repeat("1", 1<<20) + "Z"
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "generated"},
		{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "at"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: at},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	generatedProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.GeneratedAtValueContext(generatedProbe); probeErr != nil || got.State != TemporalValid || got.Raw != at {
		t.Fatalf("generated probe = (%#v, %v)", got, probeErr)
	}
	effectiveProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.EffectiveContentChangeTimeValueContext(effectiveProbe); probeErr != nil || got.State != TemporalValid || got.Raw != at {
		t.Fatalf("effective probe = (%#v, %v)", got, probeErr)
	}
	stringProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, valid, probeErr := frontmatter.EffectiveContentChangeTimeContext(stringProbe); probeErr != nil || !valid || got != at {
		t.Fatalf("string probe = (%d bytes, %v, %v)", len(got), valid, probeErr)
	}

	// Act.
	generated, generatedErr := frontmatter.GeneratedAtValueContext(&cancelAfterErrChecksContext{allowed: generatedProbe.checks.Load() / 2})
	effective, effectiveErr := frontmatter.EffectiveContentChangeTimeValueContext(&cancelAfterErrChecksContext{allowed: effectiveProbe.checks.Load() / 2})
	value, valid, valueErr := frontmatter.EffectiveContentChangeTimeContext(&cancelAfterErrChecksContext{allowed: stringProbe.checks.Load() / 2})

	// Assert.
	if !reflect.DeepEqual(generated, DateTimeValue{}) || !errors.Is(generatedErr, context.Canceled) ||
		!reflect.DeepEqual(effective, DateTimeValue{}) || !errors.Is(effectiveErr, context.Canceled) ||
		value != "" || valid || !errors.Is(valueErr, context.Canceled) {
		t.Fatalf("mid-fraction cancellation = generated %#v/%v effective %#v/%v value %d/%v/%v", generated, generatedErr, effective, effectiveErr, len(value), valid, valueErr)
	}
}

func TestContentChangeContextCancelsLargeRootWithoutPartialResults(t *testing.T) {
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
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "generated"},
		&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "at"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "2026-08-07T01:02:03Z"},
		}},
	)
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.EffectiveContentChangeTimeValueContext(probe); probeErr != nil || got.State != TemporalValid {
		t.Fatalf("effective probe = (%#v, %v)", got, probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	pre, preErr := frontmatter.EffectiveContentChangeTimeValueContext(canceled)
	mid, midErr := frontmatter.EffectiveContentChangeTimeValueContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2})
	nearComplete, nearCompleteErr := frontmatter.EffectiveContentChangeTimeValueContext(&cancelAfterErrChecksContext{allowed: totalChecks - 1})

	// Assert.
	for _, result := range []struct {
		name  string
		value DateTimeValue
		err   error
	}{
		{name: "pre", value: pre, err: preErr},
		{name: "mid", value: mid, err: midErr},
		{name: "near-complete", value: nearComplete, err: nearCompleteErr},
	} {
		if !reflect.DeepEqual(result.value, DateTimeValue{}) || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%#v, %v)", result.name, result.value, result.err)
		}
	}
}

func TestContentChangeContextRawStringsRemainStableAcrossCallerCopies(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\ngenerated: {at: 2026-08-07T01:02:03Z}\n")
	if err != nil {
		t.Fatal(err)
	}
	value, err := frontmatter.EffectiveContentChangeTimeValueContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	copyBytes := []byte(value.Raw)

	// Act.
	copyBytes[0] = 'X'
	again, againErr := frontmatter.EffectiveContentChangeTimeValueContext(context.Background())

	// Assert.
	if againErr != nil || again.Raw != "2026-08-07T01:02:03Z" || string(copyBytes) == again.Raw {
		t.Fatalf("caller copy affected raw time: copy=%q again=(%#v, %v)", copyBytes, again, againErr)
	}
}
