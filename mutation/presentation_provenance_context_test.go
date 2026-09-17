package mutation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func TestMergeProvenanceContextCancellation(t *testing.T) {
	t.Run("pre-cancel", func(t *testing.T) {
		// Arrange.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		graph := largeMergedRelationGraph(128, "target")

		// Act.
		matched, err := mergedRootContainsRelationTargetContext(ctx, graph, ref(t, "target").ID, "", make(map[*yaml.Node]bool))

		// Assert.
		if matched || !errors.Is(err, context.Canceled) {
			t.Fatalf("mergedRootContainsRelationTargetContext() = %v, %v", matched, err)
		}
	})

	t.Run("mid-edge traversal", func(t *testing.T) {
		// Arrange.
		id := ref(t, "target").ID
		graph := largeMergedRelationGraph(4_096, id.String())
		if err := bundle.ValidateYAMLNode(graph); err != nil {
			t.Fatalf("ValidateYAMLNode() error = %v", err)
		}
		control, controlErr := mergedRootContainsRelationTargetContext(context.Background(), graph, id, "", make(map[*yaml.Node]bool))
		ctx := &countdownContext{Context: context.Background(), remaining: 64}

		// Act.
		matched, err := mergedRootContainsRelationTargetContext(ctx, graph, id, "", make(map[*yaml.Node]bool))

		// Assert.
		if controlErr != nil || !control {
			t.Fatalf("control traversal = %v, %v", control, controlErr)
		}
		if matched || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled traversal = %v, %v", matched, err)
		}
	})

	t.Run("mid arbitrary key comparison", func(t *testing.T) {
		// Arrange.
		key := strings.Repeat("k", 4<<20)
		mapping := yamlTestMapping(key, yamlTestScalar("value"))
		ctx := &countdownContext{Context: context.Background(), remaining: 16}

		// Act.
		matched, err := mergedValueProvidesKeyContext(ctx, mapping, key, make(map[*yaml.Node]bool))

		// Assert.
		if matched || !errors.Is(err, context.Canceled) {
			t.Fatalf("mergedValueProvidesKeyContext() = %v, %v", matched, err)
		}
	})

	t.Run("mid canonical fragment validation", func(t *testing.T) {
		// Arrange.
		fragment := strings.Repeat("f", 4<<20)
		mapping := yamlTestMapping("id", yamlTestScalar(fragment))
		ctx := &countdownContext{Context: context.Background(), remaining: 16}

		// Act.
		matched, err := mergedCanonicalContainsFragmentContext(ctx, mapping, fragment, make(map[*yaml.Node]bool))

		// Assert.
		if matched || !errors.Is(err, context.Canceled) {
			t.Fatalf("mergedCanonicalContainsFragmentContext() = %v, %v", matched, err)
		}
	})
}

func TestMergeProvenanceContextSemanticParity(t *testing.T) {
	// Arrange.
	id := ref(t, "target").ID
	relationGraph := largeMergedRelationGraph(32, id.String())
	keyGraph := yamlTestMapping("wanted", yamlTestScalar("value"))
	canonicalGraph := yamlTestMapping("id", yamlTestScalar("fragment"))

	// Act.
	relationMatch, relationErr := mergedRootContainsRelationTargetContext(context.Background(), relationGraph, id, "", make(map[*yaml.Node]bool))
	relationMiss, relationMissErr := mergedRootContainsRelationTargetContext(context.Background(), relationGraph, ref(t, "other").ID, "", make(map[*yaml.Node]bool))
	keyMatch, keyErr := mergedValueProvidesKeyContext(context.Background(), keyGraph, "wanted", make(map[*yaml.Node]bool))
	keyMiss, keyMissErr := mergedValueProvidesKeyContext(context.Background(), keyGraph, "other", make(map[*yaml.Node]bool))
	fragmentMatch, fragmentErr := mergedCanonicalContainsFragmentContext(context.Background(), canonicalGraph, "fragment", make(map[*yaml.Node]bool))
	fragmentMiss, fragmentMissErr := mergedCanonicalContainsFragmentContext(context.Background(), canonicalGraph, "other", make(map[*yaml.Node]bool))

	// Assert.
	if relationErr != nil || relationMissErr != nil || !relationMatch || relationMiss {
		t.Fatalf("relation parity = (%v, %v), (%v, %v)", relationMatch, relationErr, relationMiss, relationMissErr)
	}
	if keyErr != nil || keyMissErr != nil || !keyMatch || keyMiss {
		t.Fatalf("key parity = (%v, %v), (%v, %v)", keyMatch, keyErr, keyMiss, keyMissErr)
	}
	if fragmentErr != nil || fragmentMissErr != nil || !fragmentMatch || fragmentMiss {
		t.Fatalf("fragment parity = (%v, %v), (%v, %v)", fragmentMatch, fragmentErr, fragmentMiss, fragmentMissErr)
	}
}

func largeMergedRelationGraph(count int, finalTarget string) *yaml.Node {
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for index := 0; index < count; index++ {
		target := "unrelated"
		if index == count-1 {
			target = finalTarget
		}
		item := yamlTestMapping("target", yamlTestScalar(target))
		items := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{item}}
		relations := yamlTestMapping("uses", items)
		sequence.Content = append(sequence.Content, yamlTestMapping("relations", relations))
	}
	return sequence
}

func yamlTestMapping(key string, value *yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{yamlTestScalar(key), value}}
}

func yamlTestScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}
