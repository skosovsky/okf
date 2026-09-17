package markdownowner

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skosovsky/okf/internal/markdownlimit"
)

func TestMarkdownParserBoundary(t *testing.T) {
	t.Parallel()

	exact := strings.Repeat("a", markdownlimit.MaxDocumentBytes)
	over := exact + "a"
	invalidOver := []byte(over)
	invalidOver[len(invalidOver)-1] = 0xff

	tests := []struct {
		name string
		kind markdownlimit.Kind
		act  func(context.Context, []byte) (any, error)
	}{
		{name: "prose", kind: markdownlimit.ResourceBodyBytes, act: func(ctx context.Context, source []byte) (any, error) {
			return ProseLinesContext(ctx, string(source))
		}},
		{name: "computation", kind: markdownlimit.ResourceBodyBytes, act: func(ctx context.Context, source []byte) (any, error) {
			return InspectComputationContext(ctx, string(source))
		}},
		{name: "footnotes", kind: markdownlimit.ResourceBodyBytes, act: func(ctx context.Context, source []byte) (any, error) {
			return CollectFootnotes(ctx, source)
		}},
		{name: "citations", kind: markdownlimit.ResourceDocumentBytes, act: func(ctx context.Context, source []byte) (any, error) {
			return CollectCitationSectionProjection(ctx, source)
		}},
		{name: "migration", kind: markdownlimit.ResourceDocumentBytes, act: func(ctx context.Context, source []byte) (any, error) {
			return CollectMarkdownMigrationOwnership(ctx, source)
		}},
		{name: "reserved", kind: markdownlimit.ResourceBodyBytes, act: func(ctx context.Context, source []byte) (any, error) {
			return TopLevelStructureContext(ctx, string(source))
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange/Act: the exact public boundary remains accepted.
			baseline, err := test.act(context.Background(), []byte(exact))
			if err != nil {
				t.Fatalf("exact boundary error = %v", err)
			}
			second, err := test.act(context.Background(), []byte(exact))
			if err != nil || !reflect.DeepEqual(second, baseline) {
				t.Fatalf("background parity = (%#v, %v), want %#v", second, err, baseline)
			}

			// Act/Assert: one byte over is a typed, exact resource failure.
			result, err := test.act(context.Background(), []byte(over))
			if !reflect.ValueOf(result).IsZero() {
				t.Fatalf("over-boundary result = %#v", result)
			}
			var limitErr *markdownlimit.Error
			if !errors.Is(err, markdownlimit.ErrResourceLimit) || !errors.As(err, &limitErr) {
				t.Fatalf("over-boundary error = %#v", err)
			}
			if limitErr.Kind != test.kind || limitErr.Limit != markdownlimit.MaxDocumentBytes || limitErr.Observed != markdownlimit.MaxDocumentBytes+1 {
				t.Fatalf("limit error = %#v", limitErr)
			}

			// Arrange/Act/Assert: invalid UTF-8 precedes the size boundary.
			result, err = test.act(context.Background(), invalidOver)
			if !reflect.ValueOf(result).IsZero() {
				t.Fatalf("invalid-over result = %#v", result)
			}
			var ownershipErr *OwnershipError
			if !errors.As(err, &ownershipErr) || ownershipErr.Code != "invalid_utf8" {
				t.Fatalf("invalid-over error = %#v", err)
			}
			if errors.Is(err, markdownlimit.ErrResourceLimit) {
				t.Fatalf("invalid-over error also reports resource limit: %v", err)
			}

			// Arrange/Act/Assert: caller cancellation has highest precedence and
			// every surface publishes its exact zero value.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result, err = test.act(ctx, invalidOver)
			if !reflect.ValueOf(result).IsZero() || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled error = %#v", err)
			}

			scanCtx := &markdownValidationCheckpointContext{cancelAt: 2}
			result, err = test.act(scanCtx, []byte(strings.Repeat("a", 2*markdownContextChunk)))
			if !reflect.ValueOf(result).IsZero() || !errors.Is(err, context.Canceled) {
				t.Fatalf("UTF-8 scan checkpoint = (%#v, %v)", result, err)
			}
			if scanCtx.hits.Load() != 2 {
				t.Fatalf("UTF-8 scan checkpoint hits = %d, want 2", scanCtx.hits.Load())
			}

			for _, checkpoint := range []int64{1, 2} {
				checkpointCtx := &markdownParserCheckpointContext{cancelAt: checkpoint}
				result, err = test.act(checkpointCtx, []byte("# Heading\n\nbody\n"))
				if !reflect.ValueOf(result).IsZero() || !errors.Is(err, context.Canceled) {
					t.Fatalf("parser checkpoint %d = (%#v, %v)", checkpoint, result, err)
				}
				if checkpointCtx.hits.Load() != checkpoint {
					t.Fatalf("parser checkpoint hits = %d, want %d", checkpointCtx.hits.Load(), checkpoint)
				}
			}
		})
	}
}

func TestHeadingIDsContextUsesSameBoundedParserContract(t *testing.T) {
	t.Parallel()

	// Arrange.
	body := "# Hello, World!\n\n## Child\n"

	// Act.
	ids, err := HeadingIDsContext(context.Background(), body)

	// Assert.
	if err != nil || !reflect.DeepEqual(ids, []string{"hello-world", "child"}) {
		t.Fatalf("HeadingIDsContext() = (%#v, %v)", ids, err)
	}
	if ids, err := HeadingIDsContext(context.Background(), strings.Repeat("a", markdownlimit.MaxBodyBytes+1)); ids != nil || !errors.Is(err, markdownlimit.ErrResourceLimit) {
		t.Fatalf("oversize HeadingIDsContext() = (%#v, %v)", ids, err)
	}
}

type markdownValidationCheckpointContext struct {
	hits     atomic.Int64
	cancelAt int64
}

func (*markdownValidationCheckpointContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*markdownValidationCheckpointContext) Done() <-chan struct{}       { return nil }
func (*markdownValidationCheckpointContext) Value(any) any               { return nil }
func (c *markdownValidationCheckpointContext) Err() error {
	pcs := make([]uintptr, 32)
	count := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:count])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".validateMarkdownBytesContext") ||
			strings.HasSuffix(frame.Function, ".validateMarkdownStringContext") {
			if c.hits.Add(1) >= c.cancelAt {
				return context.Canceled
			}
			return nil
		}
		if !more {
			return nil
		}
	}
}

type markdownParserCheckpointContext struct {
	hits     atomic.Int64
	cancelAt int64
}

func (*markdownParserCheckpointContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*markdownParserCheckpointContext) Done() <-chan struct{}       { return nil }
func (*markdownParserCheckpointContext) Value(any) any               { return nil }
func (c *markdownParserCheckpointContext) Err() error {
	pcs := make([]uintptr, 32)
	count := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:count])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".parseValidatedMarkdownContext") {
			if c.hits.Add(1) >= c.cancelAt {
				return context.Canceled
			}
			return nil
		}
		if !more {
			return nil
		}
	}
}
