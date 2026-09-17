package bundle

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestYAMLStringContextMatchesLegacyEncoderBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
	}{
		{name: "empty", yaml: ""},
		{name: "scalar", yaml: "type: Note\nvalue: text\n"},
		{name: "mapping", yaml: "type: Note\nvalue:\n  nested: text\n"},
		{name: "alias", yaml: "type: Note\nbase: &base {nested: text}\nvalue: *base\n"},
		{name: "merge", yaml: "type: Note\nbase: &base {nested: text}\nvalue:\n  <<: *base\n  own: field\n"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			parsed, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}
			frontmatter, err := NewFrontmatterFromNode(parsed.YAMLNode())
			if err != nil {
				t.Fatal(err)
			}
			want := legacyYAMLStringForTest(t, frontmatter.YAMLNode())

			// Act.
			got, contextErr := frontmatter.YAMLStringContext(context.Background())
			legacy, legacyErr := frontmatter.YAMLString()

			// Assert.
			if contextErr != nil || legacyErr != nil {
				t.Fatalf("YAML string errors = %v, %v", contextErr, legacyErr)
			}
			if got != want || legacy != want {
				t.Fatalf("YAML bytes differ:\ncontext=%q\nlegacy=%q\nwant=%q", got, legacy, want)
			}
		})
	}
}

func TestYAMLStringContextPreservesRawBytesExactly(t *testing.T) {
	t.Parallel()

	// Arrange.
	raw := "type: Note\r\nvalue: 'quoted' # comment\r\n"
	frontmatter, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	got, contextErr := frontmatter.YAMLStringContext(context.Background())
	legacy, legacyErr := frontmatter.YAMLString()

	// Assert.
	if contextErr != nil || legacyErr != nil || got != raw || legacy != raw {
		t.Fatalf("raw parity = context %q/%v legacy %q/%v want %q", got, contextErr, legacy, legacyErr, raw)
	}
}

func TestYAMLStringContextCancelsDuringRawByteCopy(t *testing.T) {
	t.Parallel()

	// Arrange.
	raw := "type: Note\npayload: " + strings.Repeat("x", 4<<20) + "\n"
	frontmatter, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.YAMLStringContext(probe); probeErr != nil || got != raw {
		t.Fatalf("raw probe = (%d bytes, %v)", len(got), probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	pre, preErr := frontmatter.YAMLStringContext(canceled)
	mid, midErr := frontmatter.YAMLStringContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2})
	nearComplete, nearCompleteErr := frontmatter.YAMLStringContext(&cancelAfterErrChecksContext{allowed: totalChecks - 1})

	// Assert.
	for _, result := range []struct {
		name  string
		value string
		err   error
	}{
		{name: "pre", value: pre, err: preErr},
		{name: "mid", value: mid, err: midErr},
		{name: "near-complete", value: nearComplete, err: nearCompleteErr},
	} {
		if result.value != "" || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%d bytes, %v), want empty/context.Canceled", result.name, len(result.value), result.err)
		}
	}
}

func TestYAMLStringContextCancelsDuringEncodedScalarStreaming(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "type"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "Note"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "payload"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.Repeat("x", 4<<20)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.YAMLStringContext(probe); probeErr != nil || len(got) < 4<<20 {
		t.Fatalf("encoded probe = (%d bytes, %v)", len(got), probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	mid, midErr := frontmatter.YAMLStringContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2})
	nearComplete, nearCompleteErr := frontmatter.YAMLStringContext(&cancelAfterErrChecksContext{allowed: totalChecks - 1})

	// Assert.
	if mid != "" || !errors.Is(midErr, context.Canceled) ||
		nearComplete != "" || !errors.Is(nearCompleteErr, context.Canceled) {
		t.Fatalf("encoded cancellation = mid %d/%v near %d/%v", len(mid), midErr, len(nearComplete), nearCompleteErr)
	}
}

func TestYAMLStringContextCancelsDuringLargeGraphWork(t *testing.T) {
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
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.YAMLStringContext(probe); probeErr != nil || got == "" {
		t.Fatalf("large graph probe = (%d bytes, %v)", len(got), probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	mid, midErr := frontmatter.YAMLStringContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2})

	// Assert.
	if mid != "" || !errors.Is(midErr, context.Canceled) {
		t.Fatalf("large graph cancellation = (%d bytes, %v)", len(mid), midErr)
	}
}

func TestYAMLStringContextRejectsMalformedRetainedGraphsWithoutPartialOutput(t *testing.T) {
	t.Parallel()

	// Arrange.
	oddMapping := Frontmatter{node: yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "key"},
	}}}
	cycle := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	cycle.Content = []*yaml.Node{cycle}
	cyclicFrontmatter := Frontmatter{node: yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"}, cycle,
	}}}

	// Act and assert.
	for name, frontmatter := range map[string]Frontmatter{"odd": oddMapping, "cycle": cyclicFrontmatter} {
		got, err := frontmatter.YAMLStringContext(context.Background())
		if got != "" || err == nil || !errors.Is(err, ErrInvalidYAMLGraph) {
			t.Errorf("%s malformed graph = (%q, %v)", name, got, err)
		}
	}
}

func TestYAMLStringContextEncoderErrorPrecedence(t *testing.T) {
	t.Parallel()

	operationErr := errors.New("encode failure")
	closeErr := errors.New("close failure")
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	tests := []struct {
		name       string
		ctx        context.Context
		encoder    scriptedYAMLStringEncoder
		want       error
		wantText   string
		rejectText string
	}{
		{
			name:       "encode-precedes-close",
			ctx:        context.Background(),
			encoder:    scriptedYAMLStringEncoder{encodeErr: operationErr, closeErr: closeErr},
			want:       ErrInvalidFrontmatter,
			wantText:   operationErr.Error(),
			rejectText: closeErr.Error(),
		},
		{
			name:     "close-after-success",
			ctx:      context.Background(),
			encoder:  scriptedYAMLStringEncoder{encodeData: []byte("ok"), closeErr: closeErr},
			want:     ErrInvalidFrontmatter,
			wantText: closeErr.Error(),
		},
		{
			name:     "encode-writer-cancel-precedes-encoder-error",
			ctx:      &cancelAfterErrChecksContext{allowed: 2},
			encoder:  scriptedYAMLStringEncoder{encodeData: []byte("ok"), encodeErr: operationErr},
			want:     context.Canceled,
			wantText: context.Canceled.Error(),
		},
		{
			name:       "encode-error-precedes-close-writer-cancel",
			ctx:        &cancelAfterErrChecksContext{allowed: 1},
			encoder:    scriptedYAMLStringEncoder{encodeErr: operationErr, closeData: []byte("close"), closeErr: closeErr},
			want:       ErrInvalidFrontmatter,
			wantText:   operationErr.Error(),
			rejectText: closeErr.Error(),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			factory := func(writer io.Writer) yamlStringEncoder {
				encoder := test.encoder
				encoder.writer = writer
				return &encoder
			}

			// Act.
			got, err := encodeYAMLNodeStringContext(test.ctx, node, factory)

			// Assert.
			if got != "" || !errors.Is(err, test.want) || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("encode result = (%q, %v), want %v containing %q", got, err, test.want, test.wantText)
			}
			if test.rejectText != "" && strings.Contains(err.Error(), test.rejectText) {
				t.Fatalf("lower-precedence error leaked: %v", err)
			}
		})
	}
}

type scriptedYAMLStringEncoder struct {
	writer     io.Writer
	encodeData []byte
	closeData  []byte
	encodeErr  error
	closeErr   error
}

func (*scriptedYAMLStringEncoder) SetIndent(int) {}

func (e *scriptedYAMLStringEncoder) Encode(any) error {
	if len(e.encodeData) > 0 {
		_, _ = e.writer.Write(e.encodeData)
	}
	return e.encodeErr
}

func (e *scriptedYAMLStringEncoder) Close() error {
	if len(e.closeData) > 0 {
		_, _ = e.writer.Write(e.closeData)
	}
	return e.closeErr
}

func legacyYAMLStringForTest(t *testing.T, node *yaml.Node) string {
	t.Helper()
	if node == nil || len(node.Content) == 0 {
		return ""
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(node); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(out.String(), "...\n")
}
