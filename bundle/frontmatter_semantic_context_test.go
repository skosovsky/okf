package bundle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSemanticContextAccessorsMatchLegacyMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		yaml          string
		wantPresent   bool
		wantAmbiguous bool
		wantKind      yaml.Kind
		wantValue     string
	}{
		{name: "absent", yaml: "type: Note\n"},
		{name: "direct", yaml: "type: Note\nselected: direct\n", wantPresent: true, wantKind: yaml.ScalarNode, wantValue: "direct"},
		{name: "terminal-alias", yaml: "type: Note\nvalue: &value {nested: original}\nselected: *value\n", wantPresent: true, wantKind: yaml.MappingNode},
		{name: "merge", yaml: "type: Note\nbase: &base {selected: merged}\n<<: *base\n", wantPresent: true, wantKind: yaml.ScalarNode, wantValue: "merged"},
		{name: "explicit-overrides-merge", yaml: "type: Note\nbase: &base {selected: merged}\n<<: *base\nselected: direct\n", wantPresent: true, wantKind: yaml.ScalarNode, wantValue: "direct"},
		{name: "merge-sequence-earlier-wins", yaml: "type: Note\nfirst: &first {selected: first}\nsecond: &second {selected: second}\n<<: [*first, *second]\n", wantPresent: true, wantKind: yaml.ScalarNode, wantValue: "first"},
		{name: "duplicate-explicit", yaml: "type: Note\nselected: first\nselected: second\n", wantPresent: true, wantAmbiguous: true},
		{name: "multiple-merge-keys", yaml: "type: Note\nfirst: &first {selected: first}\nsecond: &second {selected: second}\n<<: *first\n<<: *second\n", wantPresent: true, wantAmbiguous: true},
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
			legacyState := frontmatter.SemanticValueState("selected")
			legacyValue, legacyPresent := frontmatter.SemanticGet("selected")

			// Act.
			state, stateErr := frontmatter.SemanticValueStateContext(context.Background(), "selected")
			value, present, getErr := frontmatter.SemanticGetContext(context.Background(), "selected")

			// Assert.
			if stateErr != nil || getErr != nil {
				t.Fatalf("context errors = %v, %v", stateErr, getErr)
			}
			if !reflect.DeepEqual(state, legacyState) || present != legacyPresent || !reflect.DeepEqual(value, legacyValue) {
				t.Fatalf("legacy parity failed: state=%#v/%#v value=%#v/%#v present=%v/%v", state, legacyState, value, legacyValue, present, legacyPresent)
			}
			if state.Present != test.wantPresent || state.Ambiguous != test.wantAmbiguous || present != test.wantPresent {
				t.Fatalf("semantic state = %#v present=%v", state, present)
			}
			if test.wantAmbiguous {
				if state.Value != nil || value == nil || value.Kind != 0 {
					t.Fatalf("ambiguous projection = state %#v value %#v", state, value)
				}
				return
			}
			if !test.wantPresent {
				if state.Value != nil || value != nil {
					t.Fatalf("absent projection = state %#v value %#v", state, value)
				}
				return
			}
			if state.Value == nil || value == nil || value.Kind != test.wantKind || value.Value != test.wantValue {
				t.Fatalf("resolved projection = state %#v value %#v", state, value)
			}
		})
	}
}

func TestSemanticContextAccessorsReturnDefensiveGraphs(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\nvalue: &value {nested: original}\nselected: *value\n")
	if err != nil {
		t.Fatal(err)
	}
	state, err := frontmatter.SemanticValueStateContext(context.Background(), "selected")
	if err != nil || state.Value == nil {
		t.Fatalf("SemanticValueStateContext() = (%#v, %v)", state, err)
	}
	value, present, err := frontmatter.SemanticGetContext(context.Background(), "selected")
	if err != nil || !present || value == nil {
		t.Fatalf("SemanticGetContext() = (%#v, %v, %v)", value, present, err)
	}

	// Act.
	testMappingValue(state.Value, "nested").Value = "mutated-state"
	testMappingValue(value, "nested").Value = "mutated-get"
	againState, stateErr := frontmatter.SemanticValueStateContext(context.Background(), "selected")
	againValue, againPresent, getErr := frontmatter.SemanticGetContext(context.Background(), "selected")

	// Assert.
	if stateErr != nil || getErr != nil || !againPresent {
		t.Fatalf("repeat context errors = %v, %v present=%v", stateErr, getErr, againPresent)
	}
	if testMappingValue(againState.Value, "nested").Value != "original" ||
		testMappingValue(againValue, "nested").Value != "original" {
		t.Fatalf("caller mutation escaped: state=%#v value=%#v", againState, againValue)
	}
	if state.Value == againState.Value || value == againValue || state.Value == value {
		t.Fatal("semantic context results share mutable graph roots")
	}
}

func TestSemanticContextAccessorsCancelDuringArbitraryLengthKey(t *testing.T) {
	t.Parallel()

	// Arrange.
	keyBytes := make([]byte, 1<<20)
	for index := range keyBytes {
		keyBytes[index] = 'k'
	}
	key := string(keyBytes)
	root := yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "found"},
	}}
	frontmatter, err := NewFrontmatterFromNode(&root)
	if err != nil {
		t.Fatal(err)
	}
	stateProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.SemanticValueStateContext(stateProbe, key); probeErr != nil || !got.Present || got.Value == nil {
		t.Fatalf("state probe = (%#v, %v)", got, probeErr)
	}
	getProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, present, probeErr := frontmatter.SemanticGetContext(getProbe, key); probeErr != nil || !present || got == nil {
		t.Fatalf("get probe = (%#v, %v, %v)", got, present, probeErr)
	}

	// Act.
	state, stateErr := frontmatter.SemanticValueStateContext(
		&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() / 2}, key,
	)
	value, present, getErr := frontmatter.SemanticGetContext(
		&cancelAfterErrChecksContext{allowed: getProbe.checks.Load() / 2}, key,
	)

	// Assert.
	if !reflect.DeepEqual(state, SemanticValueState{}) || !errors.Is(stateErr, context.Canceled) {
		t.Fatalf("mid-key state = (%#v, %v)", state, stateErr)
	}
	if value != nil || present || !errors.Is(getErr, context.Canceled) {
		t.Fatalf("mid-key get = (%#v, %v, %v)", value, present, getErr)
	}
}

func TestSemanticContextAccessorsCancelDuringLargeGraphTraversal(t *testing.T) {
	t.Parallel()

	// Arrange.
	content := make([]*yaml.Node, 0, 16_386)
	for index := 0; index < 8192; index++ {
		content = append(content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "key-" + zeroPaddedDecimal(index, 4)},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"},
		)
	}
	content = append(content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "selected"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "found"},
	)
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	stateProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.SemanticValueStateContext(stateProbe, "selected"); probeErr != nil || !got.Present {
		t.Fatalf("state probe = (%#v, %v)", got, probeErr)
	}
	getProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, present, probeErr := frontmatter.SemanticGetContext(getProbe, "selected"); probeErr != nil || !present || got == nil {
		t.Fatalf("get probe = (%#v, %v, %v)", got, present, probeErr)
	}

	// Act.
	preState, preStateErr := frontmatter.SemanticValueStateContext(canceled, "selected")
	preValue, prePresent, preGetErr := frontmatter.SemanticGetContext(canceled, "selected")
	midState, midStateErr := frontmatter.SemanticValueStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() / 2}, "selected")
	midValue, midPresent, midGetErr := frontmatter.SemanticGetContext(&cancelAfterErrChecksContext{allowed: getProbe.checks.Load() / 2}, "selected")
	nearState, nearStateErr := frontmatter.SemanticValueStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() - 1}, "selected")
	nearValue, nearPresent, nearGetErr := frontmatter.SemanticGetContext(&cancelAfterErrChecksContext{allowed: getProbe.checks.Load() - 1}, "selected")

	// Assert.
	for _, result := range []struct {
		name  string
		state SemanticValueState
		err   error
	}{
		{name: "pre state", state: preState, err: preStateErr},
		{name: "mid state", state: midState, err: midStateErr},
		{name: "near state", state: nearState, err: nearStateErr},
	} {
		if !reflect.DeepEqual(result.state, SemanticValueState{}) || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%#v, %v)", result.name, result.state, result.err)
		}
	}
	for _, result := range []struct {
		name    string
		value   *yaml.Node
		present bool
		err     error
	}{
		{name: "pre get", value: preValue, present: prePresent, err: preGetErr},
		{name: "mid get", value: midValue, present: midPresent, err: midGetErr},
		{name: "near get", value: nearValue, present: nearPresent, err: nearGetErr},
	} {
		if result.value != nil || result.present || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%#v, %v, %v)", result.name, result.value, result.present, result.err)
		}
	}
}
