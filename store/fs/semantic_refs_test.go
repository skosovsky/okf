package fs

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestSemanticRefsLargeMarkdownFrontmatterDoesNotAllocateDocumentCopies(t *testing.T) {
	// Arrange. The captured document is deliberately dominated by frontmatter:
	// the legacy Concepts projection cloned both raw YAML buffers at this callsite.
	const payloadSize = 8 << 20
	document := make([]byte, 0, payloadSize+(8<<20)+128)
	document = append(document, "---\ntype: Note\npayload: "...)
	document = append(document, bytes.Repeat([]byte{'x'}, payloadSize)...)
	document = append(document, "\n---\n# Large\n\n"...)
	document = append(document, bytes.Repeat([]byte{'b'}, 8<<20)...)
	snapshot, err := newSnapshot(context.Background(), map[string][]byte{"large.md": document})
	if err != nil {
		t.Fatal(err)
	}
	document = nil
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	// Act.
	refs, err := semanticRefs(context.Background(), snapshot)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(snapshot)

	// Assert.
	if err != nil {
		t.Fatalf("semanticRefs() error = %v", err)
	}
	if len(refs) != 1 || refs[0].String() != "large" {
		t.Fatalf("semanticRefs() = %#v, want root ref", refs)
	}
	const allocationLimit = 4 << 20
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= allocationLimit {
		t.Fatalf("semanticRefs() TotalAlloc = %d bytes, want < %d bytes", allocated, allocationLimit)
	}
}

func TestCanonicalSemanticRefsKeepsStructuralRelationRefsDistinctAcrossPermutations(t *testing.T) {
	// Arrange.
	embeddedID, err := bundle.NewConceptID([]string{"source#part"})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := bundle.ParseConceptID("source")
	if err != nil {
		t.Fatal(err)
	}
	embedded := bundle.RelationRef{ID: embeddedID}
	fragment := bundle.RelationRef{ID: sourceID, Fragment: "part"}
	inputs := [][]bundle.RelationRef{
		{embedded, fragment, embedded},
		{embedded, embedded, fragment},
		{fragment, embedded, embedded},
	}
	want := []relationRefKey{
		{id: "source", fragment: "part"},
		{id: "source#part"},
	}

	for index, input := range inputs {
		seen := make(map[relationRefKey]bundle.RelationRef)

		// Act.
		for _, ref := range input {
			if err := addSemanticRef(context.Background(), seen, ref); err != nil {
				t.Fatalf("permutation %d addSemanticRef() error = %v", index, err)
			}
		}
		got, err := canonicalSemanticRefs(context.Background(), seen)

		// Assert.
		if err != nil {
			t.Fatalf("permutation %d canonicalSemanticRefs() error = %v", index, err)
		}
		if len(got) != len(want) {
			t.Fatalf("permutation %d canonicalSemanticRefs() count = %d, want %d", index, len(got), len(want))
		}
		for refIndex := range want {
			if key := relationRefKeyOf(got[refIndex]); key != want[refIndex] {
				t.Fatalf("permutation %d ref %d = %#v, want %#v", index, refIndex, key, want[refIndex])
			}
		}
	}
}

func TestSemanticRefsCancellationInterruptsContextAwareProjection(t *testing.T) {
	// Arrange.
	var source strings.Builder
	source.WriteString("---\ntype: Note\nrelations:\n  uses:\n")
	const relationCount = 512
	for range relationCount {
		source.WriteString("    - target: target\n")
	}
	source.WriteString("---\nSource\n")
	snapshot, err := newSnapshot(context.Background(), map[string][]byte{
		"source.md": []byte(source.String()),
		"target.md": []byte(adversarialDocument("target")),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := newSemanticRefsCancelAfterContext(10)

	// Act.
	_, err = semanticRefs(ctx, snapshot)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("semanticRefs() error = %v, want context.Canceled", err)
	}
	if got := ctx.checks.Load(); got < 10 {
		t.Fatalf("semanticRefs() cancellation checks = %d, want at least 10", got)
	}
}

func TestStaleConflictPathsPropagateSemanticRefsCancellation(t *testing.T) {
	// Arrange.
	_, s := adversarialStore(t, Config{})
	_ = adversarialSnapshot(t, s)
	stale := store.Revision("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	change := adversarialChange(t, "semantic-refs-cancel", stale, "uses")
	document, err := bundle.ParseDocument(adversarialDocument("replacement"))
	if err != nil {
		t.Fatal(err)
	}
	replace := ReplaceConceptRequest{
		ChangeSetID:  "semantic-refs-cancel-replace",
		Actor:        "adversary-test",
		BaseRevision: stale,
		ConceptID:    adversarialRef(t, "a").ID,
		Document:     document,
	}
	tests := []struct {
		name string
		call func(context.Context) error
	}{
		{name: "commit", call: func(ctx context.Context) error {
			_, err := s.Commit(ctx, change, store.CommitOptions{})
			return err
		}},
		{name: "replace", call: func(ctx context.Context) error {
			_, err := s.ReplaceConcept(ctx, replace, store.CommitOptions{})
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange. Cancel only while the conservative conflict projection is
			// on stack. This remains stable when earlier recovery/snapshot Context
			// owners add checkpoints.
			cancelled := &semanticRefsStackCancelContext{}

			// Act.
			err := tc.call(cancelled)

			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("stale %s error = %v, want semanticRefs cancellation", tc.name, err)
			}
			if cancelled.hits.Load() != 1 {
				t.Fatalf("stale %s semanticRefs cancellation hits = %d, want 1", tc.name, cancelled.hits.Load())
			}
		})
	}
}

type semanticRefsStackCancelContext struct{ hits atomic.Int64 }

func (*semanticRefsStackCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*semanticRefsStackCancelContext) Done() <-chan struct{}       { return nil }
func (*semanticRefsStackCancelContext) Value(any) any               { return nil }
func (c *semanticRefsStackCancelContext) Err() error {
	callers := make([]uintptr, 24)
	count := runtime.Callers(2, callers)
	frames := runtime.CallersFrames(callers[:count])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".semanticRefs") {
			c.hits.Add(1)
			return context.Canceled
		}
		if !more {
			return nil
		}
	}
}

type semanticRefsCancelAfterContext struct {
	cancelAt int64
	checks   atomic.Int64
	done     chan struct{}
	once     sync.Once
}

func newSemanticRefsCancelAfterContext(cancelAt int64) *semanticRefsCancelAfterContext {
	return &semanticRefsCancelAfterContext{cancelAt: cancelAt, done: make(chan struct{})}
}

func (c *semanticRefsCancelAfterContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *semanticRefsCancelAfterContext) Done() <-chan struct{}       { return c.done }
func (c *semanticRefsCancelAfterContext) Value(any) any               { return nil }

func (c *semanticRefsCancelAfterContext) Err() error {
	check := c.checks.Add(1)
	if c.cancelAt > 0 && check >= c.cancelAt {
		c.once.Do(func() { close(c.done) })
		return context.Canceled
	}
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}
