package bundle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKeysContextMatchesLegacyPhysicalOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{name: "empty", yaml: "", want: nil},
		{name: "ordered", yaml: "first: 1\nsecond: 2\n", want: []string{"first", "second"}},
		{name: "duplicates", yaml: "key: first\nkey: second\n", want: []string{"key", "key"}},
		{
			name: "complex-and-alias-keys-are-skipped",
			yaml: "? [complex, key]\n: skipped\n" +
				"anchor_value: &key anchored\n" +
				"? *key\n: skipped-alias\n" +
				"simple: value\n",
			want: []string{"anchor_value", "simple"},
		},
		{
			name: "physical-merge-key-is-visible",
			yaml: "base: &base {merged: value}\n<<: *base\nown: value\n",
			want: []string{"base", "<<", "own"},
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
			legacy := frontmatter.Keys()

			// Act.
			got, contextErr := frontmatter.KeysContext(context.Background())

			// Assert.
			if contextErr != nil {
				t.Fatal(contextErr)
			}
			if !reflect.DeepEqual(got, legacy) || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("KeysContext() = %#v, legacy=%#v want=%#v", got, legacy, test.want)
			}
		})
	}
}

func TestKeysContextCopiesArbitraryLengthKeysWithCancellation(t *testing.T) {
	t.Parallel()

	// Arrange.
	keyBytes := make([]byte, 1<<20)
	for index := range keyBytes {
		keyBytes[index] = 'k'
	}
	key := string(keyBytes)
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.KeysContext(probe); probeErr != nil || !reflect.DeepEqual(got, []string{key}) {
		t.Fatalf("KeysContext() probe = (%d keys, %v)", len(got), probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	got, contextErr := frontmatter.KeysContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2})

	// Assert.
	if got != nil || !errors.Is(contextErr, context.Canceled) {
		t.Fatalf("mid-key KeysContext() = (%#v, %v), want nil/context.Canceled", got, contextErr)
	}
}

func TestKeysContextCancelsLargeMappingWithoutPartialSlice(t *testing.T) {
	t.Parallel()

	// Arrange.
	content := make([]*yaml.Node, 0, 24_000)
	for index := 0; index < 12_000; index++ {
		content = append(content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "key-" + zeroPaddedDecimal(index, 5)},
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
	want, probeErr := frontmatter.KeysContext(probe)
	if probeErr != nil || len(want) != 12_000 {
		t.Fatalf("KeysContext() probe = (%d, %v)", len(want), probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	pre, preErr := frontmatter.KeysContext(canceled)
	mid, midErr := frontmatter.KeysContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2})
	nearComplete, nearCompleteErr := frontmatter.KeysContext(&cancelAfterErrChecksContext{allowed: totalChecks - 1})

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
			t.Errorf("%s = (%d keys, %v), want nil/context.Canceled", result.name, len(result.keys), result.err)
		}
	}
}

func TestKeysContextReturnsIndependentSlices(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("first: 1\nsecond: 2\n")
	if err != nil {
		t.Fatal(err)
	}
	first, err := frontmatter.KeysContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	first[0] = "mutated"
	again, againErr := frontmatter.KeysContext(context.Background())
	legacy := frontmatter.Keys()

	// Assert.
	if againErr != nil {
		t.Fatal(againErr)
	}
	if !reflect.DeepEqual(again, []string{"first", "second"}) || !reflect.DeepEqual(legacy, again) {
		t.Fatalf("caller mutation escaped: again=%#v legacy=%#v", again, legacy)
	}
}
