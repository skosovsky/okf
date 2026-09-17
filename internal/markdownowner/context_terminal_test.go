package markdownowner

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestContextPipelinesCancelPreMidAndTerminalWithoutPartialResults(t *testing.T) {
	t.Parallel()

	var computation strings.Builder
	computation.WriteString("# Computation\n\n")
	for index := range 256 {
		fmt.Fprintf(&computation, "```sql\nselect %d;\n```\n", index)
	}
	var footnotes strings.Builder
	for index := range 512 {
		fmt.Fprintf(&footnotes, "claim [^n%d]\n\n[^n%d]: source %d\n", index, index, index)
	}
	var citations strings.Builder
	citations.WriteString("# Citations\n\n")
	for index := 1; index <= 512; index++ {
		fmt.Fprintf(&citations, "[%d] https://example.com/%d\n", index, index)
	}

	tests := []struct {
		name string
		run  func(context.Context) (any, error)
	}{
		{
			name: "computation",
			run: func(ctx context.Context) (any, error) {
				return InspectComputationContext(ctx, computation.String())
			},
		},
		{
			name: "footnotes",
			run: func(ctx context.Context) (any, error) {
				return CollectFootnotes(ctx, []byte(footnotes.String()))
			},
		},
		{
			name: "citations",
			run: func(ctx context.Context) (any, error) {
				return CollectCitationSectionProjection(ctx, []byte(citations.String()))
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if result, err := test.run(canceled); !isZero(result) || !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-cancel = (%#v, %v)", result, err)
			}

			probe := &countingCancelContext{allowed: 1 << 60}
			first, err := test.run(probe)
			if err != nil {
				t.Fatal(err)
			}
			total := probe.checks.Load()
			mid := &countingCancelContext{allowed: total / 2}
			if result, err := test.run(mid); !isZero(result) || !errors.Is(err, context.Canceled) {
				t.Fatalf("mid-cancel = (%#v, %v)", result, err)
			}
			terminal := &countingCancelContext{allowed: total - 1}
			if result, err := test.run(terminal); !isZero(result) || !errors.Is(err, context.Canceled) {
				t.Fatalf("terminal-cancel = (%#v, %v)", result, err)
			}
			second, err := test.run(context.Background())
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatalf("background parity = (%#v, %v), first=%#v", second, err, first)
			}
		})
	}
}

func isZero(value any) bool {
	switch typed := value.(type) {
	case ComputationInspection:
		return reflect.DeepEqual(typed, ComputationInspection{})
	case FootnoteProjection:
		return reflect.DeepEqual(typed, FootnoteProjection{})
	case CitationSectionProjection:
		return reflect.DeepEqual(typed, CitationSectionProjection{})
	default:
		return false
	}
}

type countingCancelContext struct {
	checks  atomic.Int64
	allowed int64
}

func (c *countingCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *countingCancelContext) Done() <-chan struct{}       { return nil }
func (c *countingCancelContext) Value(any) any               { return nil }
func (c *countingCancelContext) Err() error {
	if c.checks.Add(1) > c.allowed {
		return context.Canceled
	}
	return nil
}
