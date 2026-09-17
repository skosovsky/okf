package mutation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func TestEqualValidatedYAMLNodeSemanticsContextParity(t *testing.T) {
	newGraph := func() *yaml.Node {
		aliasTarget := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "target", Anchor: "anchor"}
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "base"},
			aliasTarget,
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "values"},
			{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{
				{Kind: yaml.AliasNode, Value: "anchor", Alias: aliasTarget},
			}},
		}}
	}

	t.Run("equal content and alias presence", func(t *testing.T) {
		// Arrange.
		left, right := newGraph(), newGraph()
		if err := bundle.ValidateYAMLNode(left); err != nil {
			t.Fatalf("ValidateYAMLNode() error = %v", err)
		}

		// Act.
		equal, err := equalValidatedYAMLNodeSemanticsContext(context.Background(), left, right)

		// Assert.
		if err != nil || !equal {
			t.Fatalf("equalValidatedYAMLNodeSemanticsContext() = %v, %v", equal, err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*yaml.Node)
	}{
		{name: "kind", mutate: func(root *yaml.Node) { root.Kind = yaml.SequenceNode }},
		{name: "tag", mutate: func(root *yaml.Node) { root.Tag = "!!seq" }},
		{name: "value", mutate: func(root *yaml.Node) { root.Content[0].Value = "other" }},
		{name: "anchor", mutate: func(root *yaml.Node) { root.Anchor = "changed" }},
		{name: "alias presence", mutate: func(root *yaml.Node) { root.Content[3].Content[0].Alias = nil }},
		{name: "content length", mutate: func(root *yaml.Node) { root.Content = root.Content[:1] }},
	}
	for _, test := range tests {
		t.Run("mismatch "+test.name, func(t *testing.T) {
			// Arrange.
			left, right := newGraph(), newGraph()
			test.mutate(right)

			// Act.
			equal, err := equalValidatedYAMLNodeSemanticsContext(context.Background(), left, right)

			// Assert.
			if err != nil || equal {
				t.Fatalf("equalValidatedYAMLNodeSemanticsContext() = %v, %v", equal, err)
			}
		})
	}
}

func TestMappingTraversalDeepCancellationReturnsZero(t *testing.T) {
	root := &yaml.Node{Kind: yaml.MappingNode}
	cursor := root
	for range 256 {
		child := &yaml.Node{Kind: yaml.MappingNode}
		cursor.Content = []*yaml.Node{{Kind: yaml.ScalarNode, Value: "nested"}, child}
		cursor = child
	}
	ctx := &countdownContext{Context: context.Background(), remaining: 32}
	p := &presentation{ctx: ctx, root: root}

	patches, err := relationTargetPatchesContext(ctx, p, ref(t, "a").ID, ref(t, "b").ID, "", "")

	if patches != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("relationTargetPatchesContext() = (%#v, %v), want nil and context.Canceled", patches, err)
	}
}

func TestEqualValidatedYAMLNodeSemanticsContextCancellation(t *testing.T) {
	t.Run("pre-cancel", func(t *testing.T) {
		// Arrange.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"}

		// Act.
		equal, err := equalValidatedYAMLNodeSemanticsContext(ctx, node, node)

		// Assert.
		if equal || !errors.Is(err, context.Canceled) {
			t.Fatalf("equalValidatedYAMLNodeSemanticsContext() = %v, %v", equal, err)
		}
	})

	t.Run("mid large scalar", func(t *testing.T) {
		// Arrange.
		value := strings.Repeat("v", 4<<20)
		left := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
		right := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.Clone(value)}
		ctx := &countdownContext{Context: context.Background(), remaining: 8}

		// Act.
		equal, err := equalValidatedYAMLNodeSemanticsContext(ctx, left, right)

		// Assert.
		if equal || !errors.Is(err, context.Canceled) {
			t.Fatalf("equalValidatedYAMLNodeSemanticsContext() = %v, %v", equal, err)
		}
	})

	t.Run("mid wide graph", func(t *testing.T) {
		// Arrange.
		left, right := wideYAMLResolverSemanticGraph(8_192), wideYAMLResolverSemanticGraph(8_192)
		ctx := &countdownContext{Context: context.Background(), remaining: 64}

		// Act.
		equal, err := equalValidatedYAMLNodeSemanticsContext(ctx, left, right)

		// Assert.
		if equal || !errors.Is(err, context.Canceled) {
			t.Fatalf("equalValidatedYAMLNodeSemanticsContext() = %v, %v", equal, err)
		}
	})
}

func TestNodeSyntaxProvenanceContextCancellationReturnsZeroProvenance(t *testing.T) {
	// Arrange. Cancellation lands while scanning the 8,192-byte separation
	// prefix before a block collection token.
	source := []byte(strings.Repeat(" ", 8_192) + "- value")
	resolver := &yamlResolver{
		source: source,
		ctx:    &countdownContext{Context: context.Background(), remaining: 4},
	}
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}

	// Act.
	span, found, err := resolver.nodeSyntaxProvenanceContext(node, 8_192)

	// Assert.
	if span != (SourceSpan{}) || found || !errors.Is(err, context.Canceled) {
		t.Fatalf("nodeSyntaxProvenanceContext() = %#v, %v, %v", span, found, err)
	}
}

func wideYAMLResolverSemanticGraph(nodes int) *yaml.Node {
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	root.Content = make([]*yaml.Node, nodes)
	for index := range root.Content {
		root.Content[index] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"}
	}
	return root
}
