package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSemanticNodeContextCancelsInsideResolutionCloneAndScalarCopy(t *testing.T) {
	large := strings.Repeat("x", 4<<20)
	target := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Anchor: "target", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "payload"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: large},
	}}
	alias := &yaml.Node{Kind: yaml.AliasNode, Value: "target", Alias: target}
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{target, alias}}
	tests := []struct {
		name     string
		phase    string
		cancelAt int64
	}{
		{name: "semantic resolution final", phase: "yamlSemanticResolver).value", cancelAt: 3},
		{name: "clone per node", phase: "cloneYAMLNodeContext", cancelAt: 4},
		{name: "scalar chunk", phase: "stringFromStringContext", cancelAt: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			ctx := &stackFrameCancelContext{target: test.phase, cancelAt: test.cancelAt}

			// Act.
			got, ok, gotErr := semanticNodeInValidatedRootContext(ctx, root, alias)

			// Assert.
			if got != nil || ok || !errors.Is(gotErr, context.Canceled) || ctx.matches.Load() != test.cancelAt {
				t.Fatalf("phase %q=(node=%v,ok=%v,err=%v,matches=%d), want nil/false/canceled/%d", test.phase, got != nil, ok, gotErr, ctx.matches.Load(), test.cancelAt)
			}
		})
	}
}

func TestSemanticNodeContextCancelsInsideDeepBroadBoundedGraph(t *testing.T) {
	// Arrange.
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle, Line: 4, Column: 2}
	cursor := root
	for range MaxYAMLPhysicalDepth - 3 {
		next := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		cursor.Content = []*yaml.Node{next}
		cursor = next
	}
	for index := 0; index < 4096; index++ {
		cursor.Content = append(cursor.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"})
	}
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	baseline, baselineOK, baselineErr := SemanticNodeContext(probe, root)
	checks := probe.checks.Load()
	if baselineErr != nil || !baselineOK || baseline == nil || checks < 4096 {
		t.Fatalf("probe=(node=%v,ok=%v,err=%v), checks=%d", baseline != nil, baselineOK, baselineErr, checks)
	}

	// Act.
	got, ok, gotErr := SemanticNodeContext(&cancelAfterErrChecksContext{allowed: checks / 2}, root)

	// Assert.
	if got != nil || ok || !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("deep cancel=(node=%v,ok=%v,err=%v), want nil/false/canceled", got != nil, ok, gotErr)
	}
}

func TestSemanticNodeBackgroundExactParityAndDeepOwnership(t *testing.T) {
	// Arrange.
	target := &yaml.Node{
		Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle,
		Anchor: "target", HeadComment: "head", LineComment: "line", FootComment: "foot", Line: 7, Column: 3,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "key", Style: yaml.SingleQuotedStyle, Line: 8, Column: 4},
			{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "original"}}},
		},
	}
	alias := &yaml.Node{Kind: yaml.AliasNode, Value: "target", Alias: target}
	legacy, legacyOK := SemanticNode(alias)

	// Act.
	got, gotOK, gotErr := SemanticNodeContext(context.Background(), alias)

	// Assert.
	if gotErr != nil || !gotOK || got == nil || !legacyOK || !reflect.DeepEqual(got, legacy) {
		t.Fatalf("Background parity=(%#v,%v,%v), legacy=(%#v,%v)", got, gotOK, gotErr, legacy, legacyOK)
	}
	got.Value = "mutated"
	got.Content[0].Value = "mutated"
	got.Content[1].Content[0].Value = "mutated"
	again, againOK, againErr := SemanticNodeContext(context.Background(), alias)
	if againErr != nil || !againOK || again.Value != target.Value || again.Content[0].Value != "key" ||
		again.Content[1].Content[0].Value != "original" {
		t.Fatalf("caller mutation leaked: (%#v,%v,%v)", again, againOK, againErr)
	}

	nilNode, nilOK, nilErr := SemanticNodeContext(context.Background(), nil)
	if nilErr == nil || nilOK || nilNode != nil || !errors.Is(nilErr, ErrInvalidYAMLGraph) {
		t.Fatalf("nil shape=(%#v,%v,%v)", nilNode, nilOK, nilErr)
	}
}

func semanticNodeInValidatedRootContext(ctx context.Context, root, node *yaml.Node) (*yaml.Node, bool, error) {
	if err := ValidateYAMLNodeContext(ctx, root); err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return semanticNodeValidatedContext(ctx, node)
}

func TestSemanticNodeGraphErrorPrecedence(t *testing.T) {
	cycle := &yaml.Node{Kind: yaml.AliasNode, Value: "cycle"}
	cycle.Alias = cycle
	overDepth := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	cursor := overDepth
	for range MaxYAMLPhysicalDepth + 1 {
		next := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		cursor.Content = []*yaml.Node{next}
		cursor = next
	}
	tests := []struct {
		name string
		node *yaml.Node
		want error
	}{
		{name: "alias cycle", node: cycle, want: ErrInvalidYAMLGraph},
		{name: "resource limit", node: overDepth, want: ErrYAMLResourceLimit},
	}
	for _, test := range tests {
		got, ok, err := SemanticNodeContext(context.Background(), test.node)
		if got != nil || ok || !errors.Is(err, test.want) {
			t.Fatalf("%s=(node=%v,ok=%v,err=%v), want nil/false/%v", test.name, got != nil, ok, err, test.want)
		}
	}
}
