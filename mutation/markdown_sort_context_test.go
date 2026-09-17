package mutation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestMarkdownInlineProjectionSortContext(t *testing.T) {
	t.Run("stable parity", func(t *testing.T) {
		// Arrange.
		input := []markdownInlineDestinationNode{
			{depth: 1, ordinal: 0},
			{depth: 2, ordinal: 1},
			{depth: 2, ordinal: 2},
			{depth: 1, ordinal: 3},
		}

		// Act.
		ordered, err := sortedMarkdownInlineDestinationNodesContext(context.Background(), input)

		// Assert.
		want := []int{1, 2, 0, 3}
		if err != nil || len(ordered) != len(want) {
			t.Fatalf("sortedMarkdownInlineDestinationNodesContext() = %#v, %v", ordered, err)
		}
		for index, ordinal := range want {
			if ordered[index].ordinal != ordinal {
				t.Fatalf("ordered[%d].ordinal = %d, want %d", index, ordered[index].ordinal, ordinal)
			}
		}
	})

	t.Run("pre and mid cancellation publish no order", func(t *testing.T) {
		// Arrange.
		input := make([]markdownInlineDestinationNode, 8_192)
		for index := range input {
			input[index] = markdownInlineDestinationNode{depth: index % 17, ordinal: index}
		}
		before := append([]markdownInlineDestinationNode(nil), input...)
		pre, cancel := context.WithCancel(context.Background())
		cancel()

		// Act.
		preOrdered, preErr := sortedMarkdownInlineDestinationNodesContext(pre, input)
		midOrdered, midErr := sortedMarkdownInlineDestinationNodesContext(
			&countdownContext{Context: context.Background(), remaining: 4}, input,
		)

		// Assert.
		if preOrdered != nil || !errors.Is(preErr, context.Canceled) {
			t.Fatalf("pre-canceled sort = %d nodes, %v", len(preOrdered), preErr)
		}
		if midOrdered != nil || !errors.Is(midErr, context.Canceled) {
			t.Fatalf("mid-canceled sort = %d nodes, %v", len(midOrdered), midErr)
		}
		if !reflect.DeepEqual(input, before) {
			t.Fatal("canceled inline sort mutated caller projection")
		}
	})
}

func TestMarkdownDestinationSpanSortContext(t *testing.T) {
	t.Run("stable parity", func(t *testing.T) {
		// Arrange.
		source := []byte("0123456789")
		input := []MarkdownDestination{
			{Kind: MarkdownImageDestination, Span: SourceSpan{Start: 4, End: 5}, Value: "last"},
			{Kind: MarkdownImageDestination, Span: SourceSpan{Start: 0, End: 1}, Value: "first-image"},
			{Kind: MarkdownLinkDestination, Span: SourceSpan{Start: 0, End: 1}, Value: "first-link"},
		}

		// Act.
		ordered, err := validateMarkdownDestinationSpansContext(context.Background(), source, input)

		// Assert. Exact-span duplicates retain kind precedence, then caller order;
		// they are still rejected as overlapping before any order is published.
		if ordered != nil {
			t.Fatalf("overlapping validation returned projection: %#v", ordered)
		}
		assertMarkdownInvalidSpan(t, err, SourceSpan{Start: 0, End: 1})
		if input[0].Value != "last" || input[1].Value != "first-image" {
			t.Fatalf("validation mutated input order: %#v", input)
		}
	})

	t.Run("non-overlap ordering parity", func(t *testing.T) {
		// Arrange.
		source := []byte("0123456789")
		input := []MarkdownDestination{
			{Span: SourceSpan{Start: 6, End: 7}, Value: "third"},
			{Span: SourceSpan{Start: 0, End: 1}, Value: "first"},
			{Span: SourceSpan{Start: 3, End: 4}, Value: "second"},
		}

		// Act.
		ordered, err := validateMarkdownDestinationSpansContext(context.Background(), source, input)

		// Assert.
		if err != nil || len(ordered) != 3 || ordered[0].Value != "first" || ordered[1].Value != "second" || ordered[2].Value != "third" {
			t.Fatalf("span ordering = %#v, %v", ordered, err)
		}
	})

	t.Run("overlap precedence and union", func(t *testing.T) {
		// Arrange.
		source := []byte("0123456789")
		input := []MarkdownDestination{
			{Span: SourceSpan{Start: 2, End: 6}},
			{Span: SourceSpan{Start: 1, End: 4}},
		}

		// Act.
		ordered, err := validateMarkdownDestinationSpansContext(context.Background(), source, input)

		// Assert.
		if ordered != nil {
			t.Fatalf("overlap returned projection: %#v", ordered)
		}
		assertMarkdownInvalidSpan(t, err, SourceSpan{Start: 1, End: 6})
	})

	t.Run("pre and mid cancellation publish no spans", func(t *testing.T) {
		// Arrange.
		const count = 8_192
		source := make([]byte, count*2)
		input := make([]MarkdownDestination, count)
		for index := range input {
			start := (count - 1 - index) * 2
			input[index] = MarkdownDestination{Span: SourceSpan{Start: start, End: start + 1}}
		}
		before := append([]MarkdownDestination(nil), input...)
		pre, cancel := context.WithCancel(context.Background())
		cancel()

		// Act.
		preOrdered, preErr := validateMarkdownDestinationSpansContext(pre, source, input)
		midOrdered, midErr := validateMarkdownDestinationSpansContext(
			&countdownContext{Context: context.Background(), remaining: 4}, source, input,
		)

		// Assert.
		if preOrdered != nil || !errors.Is(preErr, context.Canceled) {
			t.Fatalf("pre-canceled spans = %d, %v", len(preOrdered), preErr)
		}
		if midOrdered != nil || !errors.Is(midErr, context.Canceled) {
			t.Fatalf("mid-canceled spans = %d, %v", len(midOrdered), midErr)
		}
		if !reflect.DeepEqual(input, before) {
			t.Fatal("canceled span validation mutated caller projection")
		}
	})
}

func TestRewriteMarkdownDestinationsContextCancellationReturnsNoOutput(t *testing.T) {
	// Arrange.
	source := []byte(strings.Repeat("[label](target) ", 512))
	ctx := &countdownContext{Context: context.Background(), remaining: 64}

	// Act.
	out, err := rewriteMarkdownDestinationsContext(ctx, source, func(value string) (string, bool) {
		return "updated", true
	})

	// Assert.
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("rewriteMarkdownDestinationsContext() = %d bytes, %v", len(out), err)
	}
}

func assertMarkdownInvalidSpan(t *testing.T, err error, want SourceSpan) {
	t.Helper()
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Code != "invalid_source_span" ||
		presentation.Format != "markdown" || presentation.Location != want ||
		!errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("span error = %#v, want invalid_source_span at %#v", err, want)
	}
}
