package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGenerationContextAccessorsMatchLegacySemanticMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		wantValid bool
	}{
		{name: "absent", yaml: "type: Note\n"},
		{name: "malformed-family", yaml: "type: Note\ngenerated: wrong\n"},
		{name: "actor-only", yaml: "type: Note\ngenerated: {by: human:sergey}\n", wantValid: true},
		{name: "complete", yaml: "type: Note\ngenerated: {by: process:generator/v1, at: 2026-08-07T01:02:03Z}\n", wantValid: true},
		{name: "missing-actor", yaml: "type: Note\ngenerated: {at: 2026-08-07T01:02:03Z}\n"},
		{name: "invalid-actor", yaml: "type: Note\ngenerated: {by: invalid}\n"},
		{name: "malformed-time", yaml: "type: Note\ngenerated: {by: human:sergey, at: bad}\n"},
		{name: "nested-duplicate", yaml: "type: Note\ngenerated:\n  by: human:first\n  by: human:second\n"},
		{name: "terminal-alias", yaml: "type: Note\nevent: &event {by: human:sergey, at: 2026-08-07T01:02:03Z}\ngenerated: *event\n", wantValid: true},
		{name: "merge", yaml: "type: Note\nbase: &base {generated: {by: human:sergey, at: 2026-08-07T01:02:03Z}}\n<<: *base\n", wantValid: true},
		{name: "duplicate-family", yaml: "type: Note\ngenerated: {by: human:first}\ngenerated: {by: human:second}\n"},
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
			wantState := frontmatter.GenerationState()
			wantValue, wantValid := frontmatter.Generated()

			// Act.
			state, stateErr := frontmatter.GenerationStateContext(context.Background())
			value, valid, valueErr := frontmatter.GeneratedContext(context.Background())
			documentState, documentStateErr := document.GenerationStateContext(context.Background())
			documentValue, documentValid, documentValueErr := document.GeneratedContext(context.Background())

			// Assert.
			if stateErr != nil || valueErr != nil || documentStateErr != nil || documentValueErr != nil {
				t.Fatalf("context errors = %v, %v, %v, %v", stateErr, valueErr, documentStateErr, documentValueErr)
			}
			if !reflect.DeepEqual(state, wantState) || !reflect.DeepEqual(documentState, wantState) ||
				value != wantValue || documentValue != wantValue || valid != wantValid || documentValid != wantValid || valid != test.wantValid {
				t.Fatalf("generation parity failed: state=%#v/%#v value=%#v/%#v valid=%v/%v", state, wantState, value, wantValue, valid, wantValid)
			}
			if !reflect.DeepEqual(document.GenerationState(), wantState) {
				t.Fatalf("Document.GenerationState() = %#v, want %#v", document.GenerationState(), wantState)
			}
		})
	}
}

func TestGenerationContextPreservesActorAndRFC3339Parity(t *testing.T) {
	t.Parallel()

	actors := []string{
		"human:sergey",
		"process:generator/v1",
		"producer/version",
		"",
		"invalid",
		"human:",
		" producer/version",
		"producer/version ",
		"producer//version",
		"producer/\x00version",
		"human:\u2003",
	}
	for _, actor := range actors {
		got, err := validActorContext(context.Background(), actor)
		if err != nil || got != ValidActor(actor) {
			t.Errorf("validActorContext(%q) = (%v, %v), legacy=%v", actor, got, err, ValidActor(actor))
		}
	}

	times := []string{
		"2026-08-07T01:02:03Z",
		"2026-08-07T01:02:03.123456789+07:00",
		"2026-08-07T01:02:03," + strings.Repeat("1", 1024) + "Z",
		"2026-08-07T01:02:03." + strings.Repeat("1", 1024) + "+07:00",
		"2026-08-07T01:02:03." + strings.Repeat("x", 1024) + "Z",
		"bad",
	}
	for _, raw := range times {
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: raw}
		got, err := dateTimeValueFromNodeContext(context.Background(), node, true)
		want := parseDateTimeValue(raw, true)
		if err != nil || got.State != want.State || got.Raw != want.Raw || got.State == TemporalValid && !got.Time.Equal(want.Time) {
			t.Errorf("dateTimeValueFromNodeContext(%d bytes) = (%#v, %v), legacy=%#v", len(raw), got, err, want)
		}
	}
}

func TestGenerationContextCancelsArbitraryLengthFieldsWithoutPartialResults(t *testing.T) {
	t.Parallel()

	// Arrange.
	actor := "process:" + strings.Repeat("a", 1<<20)
	at := "2026-08-07T01:02:03." + strings.Repeat("1", 1<<20) + "Z"
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "generated"},
		{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "by"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: actor},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "at"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: at},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stateProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.GenerationStateContext(stateProbe); probeErr != nil || !got.Valid || got.By.Value != actor || got.At.Raw != at {
		t.Fatalf("state probe = (%#v, %v)", got, probeErr)
	}
	valueProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, valid, probeErr := frontmatter.GeneratedContext(valueProbe); probeErr != nil || !valid || got.By != actor || got.At != at {
		t.Fatalf("value probe = (%#v, %v, %v)", got, valid, probeErr)
	}

	// Act.
	state, stateErr := frontmatter.GenerationStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() / 2})
	value, valid, valueErr := frontmatter.GeneratedContext(&cancelAfterErrChecksContext{allowed: valueProbe.checks.Load() / 2})

	// Assert.
	if !reflect.DeepEqual(state, GenerationState{}) || !errors.Is(stateErr, context.Canceled) {
		t.Fatalf("mid-field state = (%#v, %v)", state, stateErr)
	}
	if value != (Generation{}) || valid || !errors.Is(valueErr, context.Canceled) {
		t.Fatalf("mid-field value = (%#v, %v, %v)", value, valid, valueErr)
	}
}

func TestGenerationContextCancelsLargeRootWithoutPartialResults(t *testing.T) {
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
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "by"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "human:sergey"},
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
	stateProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.GenerationStateContext(stateProbe); probeErr != nil || !got.Valid {
		t.Fatalf("state probe = (%#v, %v)", got, probeErr)
	}
	valueProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, valid, probeErr := frontmatter.GeneratedContext(valueProbe); probeErr != nil || !valid || got.By != "human:sergey" {
		t.Fatalf("value probe = (%#v, %v, %v)", got, valid, probeErr)
	}

	// Act.
	preState, preStateErr := frontmatter.GenerationStateContext(canceled)
	preValue, preValid, preValueErr := frontmatter.GeneratedContext(canceled)
	midState, midStateErr := frontmatter.GenerationStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() / 2})
	midValue, midValid, midValueErr := frontmatter.GeneratedContext(&cancelAfterErrChecksContext{allowed: valueProbe.checks.Load() / 2})
	nearState, nearStateErr := frontmatter.GenerationStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() - 1})
	nearValue, nearValid, nearValueErr := frontmatter.GeneratedContext(&cancelAfterErrChecksContext{allowed: valueProbe.checks.Load() - 1})

	// Assert.
	for _, result := range []struct {
		name  string
		state GenerationState
		err   error
	}{
		{name: "pre state", state: preState, err: preStateErr},
		{name: "mid state", state: midState, err: midStateErr},
		{name: "near state", state: nearState, err: nearStateErr},
	} {
		if !reflect.DeepEqual(result.state, GenerationState{}) || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%#v, %v)", result.name, result.state, result.err)
		}
	}
	for _, result := range []struct {
		name  string
		value Generation
		valid bool
		err   error
	}{
		{name: "pre value", value: preValue, valid: preValid, err: preValueErr},
		{name: "mid value", value: midValue, valid: midValid, err: midValueErr},
		{name: "near value", value: nearValue, valid: nearValid, err: nearValueErr},
	} {
		if result.value != (Generation{}) || result.valid || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%#v, %v, %v)", result.name, result.value, result.valid, result.err)
		}
	}
}

func TestGenerationContextStringsRemainStableAcrossCallerCopies(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\ngenerated: {by: human:sergey, at: 2026-08-07T01:02:03Z}\n")
	if err != nil {
		t.Fatal(err)
	}
	value, valid, err := frontmatter.GeneratedContext(context.Background())
	if err != nil || !valid {
		t.Fatalf("GeneratedContext() = (%#v, %v, %v)", value, valid, err)
	}
	actorCopy := []byte(value.By)
	timeCopy := []byte(value.At)

	// Act.
	actorCopy[0] = 'X'
	timeCopy[0] = 'X'
	again, againValid, againErr := frontmatter.GeneratedContext(context.Background())

	// Assert.
	if againErr != nil || !againValid || again.By != "human:sergey" || again.At != "2026-08-07T01:02:03Z" {
		t.Fatalf("caller copies affected projection: actor=%q time=%q again=(%#v, %v, %v)", actorCopy, timeCopy, again, againValid, againErr)
	}
}
