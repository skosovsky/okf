package documentlayout

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSplitUsesOneExactOuterDelimiterContract(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    Frontmatter
		wantOK  bool
		wantErr error
	}{
		{name: "lf", data: "---\ntype: thing\n---\nbody", want: Frontmatter{YAMLStart: 4, YAMLEnd: 16, BodyStart: 20}, wantOK: true},
		{name: "crlf", data: "---\r\ntype: thing\r\n---\r\nbody", want: Frontmatter{YAMLStart: 5, YAMLEnd: 18, BodyStart: 23}, wantOK: true},
		{name: "leading space", data: " ---\ntype: thing\n---\nbody"},
		{name: "trailing space", data: "--- \ntype: thing\n---\nbody"},
		{name: "tab", data: "\t---\ntype: thing\n---\nbody"},
		{name: "lone carriage return", data: "---\rtype: thing\r---\rbody"},
		{name: "unterminated", data: "---\ntype: thing\n", want: Frontmatter{YAMLStart: 4, YAMLEnd: 16, BodyStart: 16}, wantOK: true, wantErr: ErrUnterminatedFrontmatter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			got, ok, err := Split([]byte(tt.data))

			// Assert.
			if got != tt.want || ok != tt.wantOK || !errors.Is(err, tt.wantErr) {
				t.Fatalf("Split() = %#v, %t, %v; want %#v, %t, %v", got, ok, err, tt.want, tt.wantOK, tt.wantErr)
			}
		})
	}
}

func TestSplitContextMatchesSplit(t *testing.T) {
	tests := []string{
		"",
		"plain markdown",
		"---\n---\n",
		"---\r\ntype: thing\r\n---\r\nbody",
		"---\ntype: thing\n",
	}
	for _, source := range tests {
		source := source
		t.Run(strings.ReplaceAll(source, "\n", "_"), func(t *testing.T) {
			// Arrange.
			want, wantOK, wantErr := Split([]byte(source))

			// Act.
			got, gotOK, gotErr := SplitContext(context.Background(), []byte(source))

			// Assert.
			if got != want || gotOK != wantOK || !errors.Is(gotErr, wantErr) {
				t.Fatalf("SplitContext() = %#v, %t, %v; want %#v, %t, %v", got, gotOK, gotErr, want, wantOK, wantErr)
			}
		})
	}
}

func TestSplitContextCancellation(t *testing.T) {
	t.Run("pre-cancelled", func(t *testing.T) {
		// Arrange.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act.
		layout, ok, err := SplitContext(ctx, []byte("---\n---\n"))

		// Assert.
		if layout != (Frontmatter{}) || ok || !errors.Is(err, context.Canceled) {
			t.Fatalf("SplitContext() = %#v, %t, %v", layout, ok, err)
		}
	})

	t.Run("during physical-line scan", func(t *testing.T) {
		// Arrange. Cancellation occurs after the first 64 KiB chunk of the
		// unterminated second physical line has been inspected.
		ctx := &cancelAfterChecksContext{remaining: 3}
		source := []byte("---\n" + strings.Repeat("x", 256<<10))

		// Act.
		layout, ok, err := SplitContext(ctx, source)

		// Assert.
		if layout != (Frontmatter{}) || ok || !errors.Is(err, context.Canceled) {
			t.Fatalf("SplitContext() = %#v, %t, %v", layout, ok, err)
		}
	})
}

type cancelAfterChecksContext struct {
	remaining int
}

func (*cancelAfterChecksContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*cancelAfterChecksContext) Done() <-chan struct{}       { return nil }
func (*cancelAfterChecksContext) Value(any) any               { return nil }
func (ctx *cancelAfterChecksContext) Err() error {
	if ctx.remaining == 0 {
		return context.Canceled
	}
	ctx.remaining--
	return nil
}
