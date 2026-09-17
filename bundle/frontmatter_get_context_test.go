package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGetContextMatchesLegacyDirectKeySemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		key  string
	}{
		{name: "absent", yaml: "type: Note\n", key: "missing"},
		{name: "direct", yaml: "type: Note\nvalue: direct\n", key: "value"},
		{name: "duplicate-selects-first", yaml: "type: Note\nvalue: first\nvalue: second\n", key: "value"},
		{name: "merge-only-is-absent", yaml: "type: Note\nbase: &base {value: merged}\n<<: *base\n", key: "value"},
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
			want, wantPresent := frontmatter.Get(test.key)

			// Act.
			got, present, getErr := frontmatter.GetContext(context.Background(), test.key)

			// Assert.
			if getErr != nil {
				t.Fatal(getErr)
			}
			if present != wantPresent || !reflect.DeepEqual(got, want) {
				t.Fatalf("GetContext(%q) = (%#v, %v), legacy = (%#v, %v)", test.key, got, present, want, wantPresent)
			}
			if test.name == "duplicate-selects-first" && (got == nil || got.Value != "first") {
				t.Fatalf("duplicate direct-key selection = %#v, want first", got)
			}
		})
	}
}

func TestGetContextReturnsDefensiveAliasGraph(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\nbase: &base {name: original}\nvalue: *base\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	first, present, getErr := frontmatter.GetContext(context.Background(), "value")
	if getErr != nil || !present {
		t.Fatalf("GetContext() = (%#v, %v, %v)", first, present, getErr)
	}
	first.Alias.Content[1].Value = "mutated"
	first.Value = "mutated-anchor-name"
	again, againPresent, againErr := frontmatter.GetContext(context.Background(), "value")

	// Assert.
	if againErr != nil || !againPresent {
		t.Fatalf("repeat GetContext() = (%#v, %v, %v)", again, againPresent, againErr)
	}
	if again.Value != "base" || again.Alias == nil || again.Alias.Content[1].Value != "original" {
		t.Fatalf("caller mutation escaped into retained graph: %#v", again)
	}
	if first == again || first.Alias == again.Alias {
		t.Fatal("independent GetContext calls share mutable nodes")
	}
}

func TestGetContextCancelsDuringArbitraryLengthKeyComparison(t *testing.T) {
	t.Parallel()

	// Arrange.
	key := strings.Repeat("k", 1<<20)
	root := yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "found"},
	}}
	frontmatter, err := NewFrontmatterFromNode(&root)
	if err != nil {
		t.Fatal(err)
	}
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, present, probeErr := frontmatter.GetContext(probe, key); probeErr != nil || !present || got.Value != "found" {
		t.Fatalf("GetContext() probe = (%#v, %v, %v)", got, present, probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	got, present, getErr := frontmatter.GetContext(
		&cancelAfterErrChecksContext{allowed: totalChecks / 2},
		key,
	)

	// Assert.
	if got != nil || present || !errors.Is(getErr, context.Canceled) {
		t.Fatalf("mid-key GetContext() = (%#v, %v, %v), want nil/false/context.Canceled", got, present, getErr)
	}
}

func TestGetContextCancelsDuringValueCloneWithoutPartialNode(t *testing.T) {
	t.Parallel()

	// Arrange.
	var source strings.Builder
	source.WriteString("type: Note\nvalue:\n")
	for index := 0; index < 4096; index++ {
		source.WriteString("  - item-")
		source.WriteString(zeroPaddedDecimal(index, 4))
		source.WriteByte('\n')
	}
	frontmatter, err := ParseFrontmatter(source.String())
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, present, probeErr := frontmatter.GetContext(probe, "value"); probeErr != nil || !present || got == nil {
		t.Fatalf("GetContext() probe = (%#v, %v, %v)", got, present, probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	pre, prePresent, preErr := frontmatter.GetContext(canceled, "value")
	mid, midPresent, midErr := frontmatter.GetContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2}, "value")
	nearComplete, nearCompletePresent, nearCompleteErr := frontmatter.GetContext(&cancelAfterErrChecksContext{allowed: totalChecks - 1}, "value")

	// Assert.
	for _, result := range []struct {
		name    string
		node    *yaml.Node
		present bool
		err     error
	}{
		{name: "pre", node: pre, present: prePresent, err: preErr},
		{name: "mid", node: mid, present: midPresent, err: midErr},
		{name: "near-complete", node: nearComplete, present: nearCompletePresent, err: nearCompleteErr},
	} {
		if result.node != nil || result.present || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%#v, %v, %v), want nil/false/context.Canceled", result.name, result.node, result.present, result.err)
		}
	}
}
