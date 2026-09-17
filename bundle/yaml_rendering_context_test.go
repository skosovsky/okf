package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStableYAMLRenderMatchesYAMLv3Bytes(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{name: "nil"},
		{name: "scalar style", yaml: "'quoted value'\n"},
		{name: "flow sequence", yaml: "[one, two, {nested: value}]\n"},
		{name: "block mapping order", yaml: "zeta: last\nalpha: first\nnested:\n  - one\n  - two\n"},
		{name: "literal style", yaml: "value: |\n  first\n  second\n"},
		{name: "anchor alias", yaml: "base: &base {value: one}\nselected: *base\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			var node *yaml.Node
			if test.yaml != "" {
				var document yaml.Node
				if err := yaml.Unmarshal([]byte(test.yaml), &document); err != nil {
					t.Fatal(err)
				}
				node = document.Content[0]
			}
			want := ""
			if node != nil {
				switch node.Kind {
				case yaml.ScalarNode:
					want = node.Value
				case yaml.AliasNode:
					want = "*" + node.Value
				default:
					encoded, err := yaml.Marshal(node)
					if err != nil {
						t.Fatal(err)
					}
					want = strings.TrimSpace(string(encoded))
				}
			}

			// Act.
			got, gotErr := stableYAMLValueContext(context.Background(), node)

			// Assert.
			if gotErr != nil || got != want {
				t.Fatalf("stable render = (%q,%v), want %q", got, gotErr, want)
			}
		})
	}
}

func TestStableYAMLRenderCancelsInsideScalarAndEncoding(t *testing.T) {
	large := strings.Repeat("x", 4<<20)
	tests := []struct {
		name string
		node *yaml.Node
	}{
		{
			name: "scalar copy",
			node: &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: large},
		},
		{
			name: "mapping encoding and writing",
			node: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "payload"},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: large},
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			baseline, baselineErr := stableYAMLValueContext(probe, test.node)
			checks := probe.checks.Load()
			if baselineErr != nil || baseline == "" || checks < 32 {
				t.Fatalf("probe = (%d bytes,%v), checks=%d", len(baseline), baselineErr, checks)
			}
			rows := []struct {
				name    string
				allowed int64
			}{
				{name: "middle", allowed: checks / 2},
				{name: "final", allowed: checks - 1},
			}
			for _, row := range rows {
				// Act.
				got, gotErr := stableYAMLValueContext(&cancelAfterErrChecksContext{allowed: row.allowed}, test.node)

				// Assert.
				if got != "" || !errors.Is(gotErr, context.Canceled) {
					t.Fatalf("%s cancellation = (%d bytes,%v), want zero/context.Canceled", row.name, len(got), gotErr)
				}
			}
		})
	}
}

func TestStableYAMLRenderCancelsInsideDeepBoundedGraph(t *testing.T) {
	// Arrange: depth stays strictly within the public physical-depth contract.
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	cursor := root
	for range MaxYAMLPhysicalDepth - 2 {
		next := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		cursor.Content = []*yaml.Node{next}
		cursor = next
	}
	for index := 0; index < 4096; index++ {
		cursor.Content = append(cursor.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"})
	}
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	baseline, baselineErr := stableYAMLValueContext(probe, root)
	checks := probe.checks.Load()
	if baselineErr != nil || baseline == "" || checks < 4096 {
		t.Fatalf("deep probe = (%d bytes,%v), checks=%d", len(baseline), baselineErr, checks)
	}

	// Act.
	got, gotErr := stableYAMLValueContext(&cancelAfterErrChecksContext{allowed: checks / 2}, root)

	// Assert.
	if got != "" || !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("deep cancellation = (%d bytes,%v), want zero/context.Canceled", len(got), gotErr)
	}
}

func TestStableYAMLRenderPreservesGraphFailurePrecedence(t *testing.T) {
	// Arrange.
	cycle := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	cycle.Content = []*yaml.Node{cycle}
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
		{name: "cycle", node: cycle, want: ErrInvalidYAMLGraph},
		{name: "budget", node: overDepth, want: ErrYAMLResourceLimit},
	}
	for _, test := range tests {
		// Act.
		got, gotErr := stableYAMLValueContext(context.Background(), test.node)

		// Assert.
		if got != "" || !errors.Is(gotErr, test.want) {
			t.Fatalf("%s = (%q,%v), want zero/%v", test.name, got, gotErr, test.want)
		}
	}
}

func TestStableYAMLSemanticObservationBackgroundParityAndOwnership(t *testing.T) {
	// Arrange.
	frontmatter, err := ParseFrontmatter("sources:\n  - resource: source.md\n    title: value\n")
	if err != nil {
		t.Fatal(err)
	}
	legacy, legacyErr := frontmatter.SemanticFamilyObservation("sources")
	if legacyErr != nil {
		t.Fatal(legacyErr)
	}

	// Act.
	got, gotErr := frontmatter.SemanticFamilyObservationContext(context.Background(), "sources")

	// Assert.
	if gotErr != nil || !reflect.DeepEqual(got, legacy) {
		t.Fatalf("Background parity = (%#v,%v), legacy=%#v", got, gotErr, legacy)
	}
	got.Raw = "mutated"
	got.ResolvedRaw = "mutated"
	again, againErr := frontmatter.SemanticFamilyObservationContext(context.Background(), "sources")
	if againErr != nil || !reflect.DeepEqual(again, legacy) {
		t.Fatalf("caller mutation leaked: (%#v,%v), legacy=%#v", again, againErr, legacy)
	}
}
