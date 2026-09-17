package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExtensionKeysContextMatchesLegacyPhysicalSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{name: "empty", yaml: "", want: nil},
		{name: "standards-only", yaml: "type: Note\ntitle: Title\ntags: []\n", want: nil},
		{name: "ordered-extensions", yaml: "type: Note\nfirst: 1\nsecond: 2\n", want: []string{"first", "second"}},
		{name: "duplicate-extensions", yaml: "type: Note\next: first\next: second\n", want: []string{"ext", "ext"}},
		{
			name: "complex-key-skipped",
			yaml: "type: Note\n? [complex, key]\n: skipped\next: value\n",
			want: []string{"ext"},
		},
		{
			name: "physical-merge-key-is-extension",
			yaml: "type: Note\nbase: &base {title: inherited}\n<<: *base\next: value\n",
			want: []string{"base", "<<", "ext"},
		},
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
			legacy := frontmatter.ExtensionKeys()

			// Act.
			got, contextErr := frontmatter.ExtensionKeysContext(context.Background())

			// Assert.
			if contextErr != nil {
				t.Fatal(contextErr)
			}
			if !reflect.DeepEqual(got, legacy) || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ExtensionKeysContext() = %#v, legacy=%#v want=%#v", got, legacy, test.want)
			}
		})
	}
}

func TestExtensionKeysContextPreservesScalarTagAgnosticExclusion(t *testing.T) {
	t.Parallel()

	// Arrange: legacy Keys and ExtensionKeys classify scalar keys by Value,
	// regardless of their YAML tag.
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: "type"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "Note"},
		{Kind: yaml.ScalarNode, Tag: "!!int", Value: "123"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	got, contextErr := frontmatter.ExtensionKeysContext(context.Background())

	// Assert.
	if contextErr != nil {
		t.Fatal(contextErr)
	}
	if !reflect.DeepEqual(got, []string{"123"}) || !reflect.DeepEqual(got, frontmatter.ExtensionKeys()) {
		t.Fatalf("tagged scalar extensions = %#v, legacy=%#v", got, frontmatter.ExtensionKeys())
	}
}

func TestExtensionKeysContextCancelsArbitraryLengthKeyWithoutPartialSlice(t *testing.T) {
	t.Parallel()

	// Arrange.
	key := strings.Repeat("x", 1<<20)
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.ExtensionKeysContext(probe); probeErr != nil || !reflect.DeepEqual(got, []string{key}) {
		t.Fatalf("ExtensionKeysContext() probe = (%d, %v)", len(got), probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	got, contextErr := frontmatter.ExtensionKeysContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2})

	// Assert.
	if got != nil || !errors.Is(contextErr, context.Canceled) {
		t.Fatalf("mid-key ExtensionKeysContext() = (%#v, %v)", got, contextErr)
	}
}

func TestExtensionKeysContextCancelsLargeMappingWithoutPartialSlice(t *testing.T) {
	t.Parallel()

	// Arrange.
	content := make([]*yaml.Node, 0, 24_000)
	for index := 0; index < 12_000; index++ {
		content = append(content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "extension-" + zeroPaddedDecimal(index, 5)},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"},
		)
	}
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.ExtensionKeysContext(probe); probeErr != nil || len(got) != 12_000 {
		t.Fatalf("ExtensionKeysContext() probe = (%d, %v)", len(got), probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	pre, preErr := frontmatter.ExtensionKeysContext(canceled)
	mid, midErr := frontmatter.ExtensionKeysContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2})
	nearComplete, nearCompleteErr := frontmatter.ExtensionKeysContext(&cancelAfterErrChecksContext{allowed: totalChecks - 1})

	// Assert.
	for _, result := range []struct {
		name string
		keys []string
		err  error
	}{
		{name: "pre", keys: pre, err: preErr},
		{name: "mid", keys: mid, err: midErr},
		{name: "near-complete", keys: nearComplete, err: nearCompleteErr},
	} {
		if result.keys != nil || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%d keys, %v)", result.name, len(result.keys), result.err)
		}
	}
}

func TestExtensionKeysContextReturnsIndependentSlices(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\nfirst: 1\nsecond: 2\n")
	if err != nil {
		t.Fatal(err)
	}
	first, err := frontmatter.ExtensionKeysContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	first[0] = "mutated"
	again, againErr := frontmatter.ExtensionKeysContext(context.Background())

	// Assert.
	if againErr != nil {
		t.Fatal(againErr)
	}
	if !reflect.DeepEqual(again, []string{"first", "second"}) || !reflect.DeepEqual(again, frontmatter.ExtensionKeys()) {
		t.Fatalf("caller mutation escaped: again=%#v legacy=%#v", again, frontmatter.ExtensionKeys())
	}
}
