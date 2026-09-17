package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestYAMLNodeContextMatchesLegacyAndPreservesAliasTopology(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\nbase: &base\n  name: original\nalias: *base\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	legacy := frontmatter.YAMLNode()
	contextNode, contextErr := frontmatter.YAMLNodeContext(context.Background())
	base := testMappingValue(contextNode, "base")
	alias := testMappingValue(contextNode, "alias")

	// Assert.
	if contextErr != nil {
		t.Fatal(contextErr)
	}
	if !reflect.DeepEqual(contextNode, legacy) {
		t.Fatalf("YAMLNodeContext() differs from YAMLNode(): context=%#v legacy=%#v", contextNode, legacy)
	}
	if base == nil || alias == nil || alias.Kind != yaml.AliasNode || alias.Alias != base {
		t.Fatalf("alias topology not preserved: base=%p alias=%#v target=%p", base, alias, alias.Alias)
	}
	if base == frontmatter.node.Content[3] {
		t.Fatal("YAMLNodeContext() shares retained child node")
	}
}

func TestYAMLNodeContextResultIsDefensive(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\nbase: &base\n  name: original\nalias: *base\n")
	if err != nil {
		t.Fatal(err)
	}
	first, err := frontmatter.YAMLNodeContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	firstBase := testMappingValue(first, "base")
	firstName := testMappingValue(firstBase, "name")

	// Act.
	firstName.Value = "mutated"
	first.Content[0].Value = "mutated-key"
	again, againErr := frontmatter.YAMLNodeContext(context.Background())
	againBase := testMappingValue(again, "base")
	againName := testMappingValue(againBase, "name")

	// Assert.
	if againErr != nil {
		t.Fatal(againErr)
	}
	if again.Content[0].Value != "type" || againName == nil || againName.Value != "original" {
		t.Fatalf("caller mutation escaped into retained graph: %#v", again)
	}
	if firstBase == againBase || firstName == againName {
		t.Fatal("independent calls share mutable YAML nodes")
	}
}

func TestYAMLNodeContextCancelsWithoutPartialGraph(t *testing.T) {
	t.Parallel()

	// Arrange.
	var source strings.Builder
	source.WriteString("type: Note\nvalues:\n")
	for index := 0; index < 4096; index++ {
		source.WriteString("  - value-")
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
	if got, probeErr := frontmatter.YAMLNodeContext(probe); probeErr != nil || got == nil {
		t.Fatalf("YAMLNodeContext() probe = (%#v, %v)", got, probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	pre, preErr := frontmatter.YAMLNodeContext(canceled)
	mid, midErr := frontmatter.YAMLNodeContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2})
	nearComplete, nearCompleteErr := frontmatter.YAMLNodeContext(&cancelAfterErrChecksContext{allowed: totalChecks - 1})

	// Assert.
	for _, result := range []struct {
		name string
		node *yaml.Node
		err  error
	}{
		{name: "pre", node: pre, err: preErr},
		{name: "mid", node: mid, err: midErr},
		{name: "near-complete", node: nearComplete, err: nearCompleteErr},
	} {
		if result.node != nil || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s cancellation = (%#v, %v), want nil/context.Canceled", result.name, result.node, result.err)
		}
	}
}

func TestCloneYAMLNodeContextTerminatesOnCyclicCallerGraph(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	root.Content = []*yaml.Node{root}

	// Act.
	cloned, err := cloneYAMLNodeContext(context.Background(), root)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if cloned.Kind != yaml.SequenceNode || len(cloned.Content) != 1 || cloned.Content[0] == nil {
		t.Fatalf("cyclic clone shape = %#v", cloned)
	}
}

func testMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index] != nil && node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}
