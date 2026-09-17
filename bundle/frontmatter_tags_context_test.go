package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTagsContextAccessorsMatchLegacySemanticMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
	}{
		{name: "absent", yaml: "type: Note\n"},
		{name: "bare-is-malformed", yaml: "type: Note\ntags: one\n"},
		{name: "empty-list", yaml: "type: Note\ntags: []\n"},
		{name: "ordered-list", yaml: "type: Note\ntags: [first, second, third]\n"},
		{name: "malformed-items-remain-visible", yaml: "type: Note\ntags: [valid, 3, '', null]\n"},
		{name: "family-alias", yaml: "type: Note\nvalues: &values [first, second]\ntags: *values\n"},
		{name: "item-alias-stays-invalid", yaml: "type: Note\nvalue: &value first\ntags: [*value, second]\n"},
		{name: "merged-family", yaml: "type: Note\nbase: &base {tags: [first, second]}\n<<: *base\n"},
		{name: "duplicate-family-is-ambiguous", yaml: "type: Note\ntags: [first]\ntags: [second]\n"},
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
			wantState := frontmatter.TagsState()
			wantTags := frontmatter.Tags()

			// Act.
			state, stateErr := frontmatter.TagsStateContext(context.Background())
			tags, tagsErr := frontmatter.TagsContext(context.Background())

			// Assert.
			if stateErr != nil || tagsErr != nil {
				t.Fatalf("context errors = %v, %v", stateErr, tagsErr)
			}
			if !reflect.DeepEqual(state, wantState) || !reflect.DeepEqual(tags, wantTags) {
				t.Fatalf("tags parity failed: state=%#v/%#v tags=%#v/%#v", state, wantState, tags, wantTags)
			}
			switch test.name {
			case "ordered-list", "family-alias", "merged-family":
				if !state.Valid || len(tags) < 2 || tags[0] != "first" || tags[1] != "second" {
					t.Fatalf("ordered tags = state %#v tags %#v", state, tags)
				}
			case "item-alias-stays-invalid":
				if state.Valid || len(state.Items) != 2 || state.Items[0].Valid || !reflect.DeepEqual(tags, []string{"second"}) {
					t.Fatalf("item alias projection = state %#v tags %#v", state, tags)
				}
			case "duplicate-family-is-ambiguous":
				if !state.Present || state.Valid || state.Items != nil || tags != nil {
					t.Fatalf("duplicate projection = state %#v tags %#v", state, tags)
				}
			}
		})
	}
}

func TestTagsContextAccessorsCancelDuringArbitraryLengthScalar(t *testing.T) {
	t.Parallel()

	// Arrange.
	value := strings.Repeat("x", 1<<20)
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "tags"},
		{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stateProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.TagsStateContext(stateProbe); probeErr != nil || len(got.Items) != 1 || got.Items[0].Value != value {
		t.Fatalf("state probe = (%#v, %v)", got, probeErr)
	}
	tagsProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.TagsContext(tagsProbe); probeErr != nil || !reflect.DeepEqual(got, []string{value}) {
		t.Fatalf("tags probe = (%d, %v)", len(got), probeErr)
	}

	// Act.
	state, stateErr := frontmatter.TagsStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() / 2})
	tags, tagsErr := frontmatter.TagsContext(&cancelAfterErrChecksContext{allowed: tagsProbe.checks.Load() / 2})

	// Assert.
	if !reflect.DeepEqual(state, StringListState{}) || !errors.Is(stateErr, context.Canceled) {
		t.Fatalf("mid-scalar state = (%#v, %v)", state, stateErr)
	}
	if tags != nil || !errors.Is(tagsErr, context.Canceled) {
		t.Fatalf("mid-scalar tags = (%#v, %v)", tags, tagsErr)
	}
}

func TestTagsContextAccessorsCancelLargeListWithoutPartialResults(t *testing.T) {
	t.Parallel()

	// Arrange.
	items := make([]*yaml.Node, 12_000)
	for index := range items {
		items[index] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "tag-" + zeroPaddedDecimal(index, 5)}
	}
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "tags"},
		{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items},
	}})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	stateProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.TagsStateContext(stateProbe); probeErr != nil || len(got.Items) != 12_000 {
		t.Fatalf("state probe = (%d, %v)", len(got.Items), probeErr)
	}
	tagsProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.TagsContext(tagsProbe); probeErr != nil || len(got) != 12_000 {
		t.Fatalf("tags probe = (%d, %v)", len(got), probeErr)
	}

	// Act.
	preState, preStateErr := frontmatter.TagsStateContext(canceled)
	preTags, preTagsErr := frontmatter.TagsContext(canceled)
	midState, midStateErr := frontmatter.TagsStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() / 2})
	midTags, midTagsErr := frontmatter.TagsContext(&cancelAfterErrChecksContext{allowed: tagsProbe.checks.Load() / 2})
	nearState, nearStateErr := frontmatter.TagsStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() - 1})
	nearTags, nearTagsErr := frontmatter.TagsContext(&cancelAfterErrChecksContext{allowed: tagsProbe.checks.Load() - 1})

	// Assert.
	for _, result := range []struct {
		name  string
		state StringListState
		err   error
	}{
		{name: "pre state", state: preState, err: preStateErr},
		{name: "mid state", state: midState, err: midStateErr},
		{name: "near state", state: nearState, err: nearStateErr},
	} {
		if !reflect.DeepEqual(result.state, StringListState{}) || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%#v, %v)", result.name, result.state, result.err)
		}
	}
	for _, result := range []struct {
		name string
		tags []string
		err  error
	}{
		{name: "pre tags", tags: preTags, err: preTagsErr},
		{name: "mid tags", tags: midTags, err: midTagsErr},
		{name: "near tags", tags: nearTags, err: nearTagsErr},
	} {
		if result.tags != nil || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%d tags, %v)", result.name, len(result.tags), result.err)
		}
	}
}

func TestTagsContextResultsAreIndependentAcrossCalls(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\ntags: [first, second]\n")
	if err != nil {
		t.Fatal(err)
	}
	state, err := frontmatter.TagsStateContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tags, err := frontmatter.TagsContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	state.Items[0].Raw = "mutated"
	state.Items[0].Value = "mutated"
	tags[0] = "mutated"
	againState, stateErr := frontmatter.TagsStateContext(context.Background())
	againTags, tagsErr := frontmatter.TagsContext(context.Background())

	// Assert.
	if stateErr != nil || tagsErr != nil {
		t.Fatalf("repeat errors = %v, %v", stateErr, tagsErr)
	}
	if againState.Items[0].Raw != "first" || againState.Items[0].Value != "first" ||
		!reflect.DeepEqual(againTags, []string{"first", "second"}) {
		t.Fatalf("caller mutation escaped: state=%#v tags=%#v", againState, againTags)
	}
}
