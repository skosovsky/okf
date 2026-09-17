package bundle

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

func sameErrorText(left, right error) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Error() == right.Error()
}

type stackFrameCancelContext struct {
	target   string
	cancelAt int64
	matches  atomic.Int64
	canceled atomic.Bool
}

func (*stackFrameCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*stackFrameCancelContext) Done() <-chan struct{}       { return nil }
func (*stackFrameCancelContext) Value(any) any               { return nil }

func (c *stackFrameCancelContext) Err() error {
	if c.canceled.Load() {
		return context.Canceled
	}
	pcs := make([]uintptr, 32)
	count := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:count])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, c.target) {
			if c.matches.Add(1) >= c.cancelAt {
				c.canceled.Store(true)
				return context.Canceled
			}
			break
		}
		if !more {
			break
		}
	}
	return nil
}

type recordingSource struct {
	paths []string
	files map[string][]byte
	reads atomic.Int64
}

func (s *recordingSource) Paths(context.Context) ([]string, error) {
	return append([]string(nil), s.paths...), nil
}

func (s *recordingSource) ReadFile(_ context.Context, name string) ([]byte, error) {
	s.reads.Add(1)
	return append([]byte(nil), s.files[name]...), nil
}
