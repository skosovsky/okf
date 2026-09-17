package receiptprojection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestDerive_GoldenProjectionPairsRenamesAndSemanticRefs(t *testing.T) {
	// Arrange.
	base := loadProjectionBundle(t, projectionSource{
		"a.md":        []byte("---\ntype: Note\nparts:\n  - id: old\nrelations:\n  uses:\n    - target: b#target\n---\nOld\n"),
		"b.md":        []byte("---\ntype: Note\nparts:\n  - id: target\n---\nB\n"),
		"old-1.asset": []byte("same"),
		"old-2.asset": []byte("same"),
	})
	target := loadProjectionBundle(t, projectionSource{
		"a.md":    []byte("---\ntype: Note\nparts:\n  - id: new\nrelations:\n  uses:\n    - target: c#target\n---\nNew\n"),
		"b.md":    []byte("---\ntype: Note\nparts:\n  - id: target\n---\nB\n"),
		"c.md":    []byte("---\ntype: Note\nparts:\n  - id: target\n---\nC\n"),
		"x.asset": []byte("same"),
		"y.asset": []byte("same"),
	})
	wantFiles := []store.FileChange{
		{Kind: store.FileWrite, Path: "a.md"},
		{Kind: store.FileWrite, Path: "c.md"},
		{Kind: store.FileRename, Path: "x.asset", From: "old-1.asset"},
		{Kind: store.FileRename, Path: "y.asset", From: "old-2.asset"},
	}
	wantRefs := []string{"a", "a#new", "a#old", "b#target", "c", "c#target"}

	// Act.
	projection, err := Derive(context.Background(), base, target)

	// Assert.
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if !reflect.DeepEqual(projection.ChangedFiles, wantFiles) {
		t.Fatalf("ChangedFiles = %#v, want %#v", projection.ChangedFiles, wantFiles)
	}
	gotRefs := make([]string, len(projection.ChangedRefs))
	for i, ref := range projection.ChangedRefs {
		gotRefs[i] = ref.String()
	}
	if !reflect.DeepEqual(gotRefs, wantRefs) {
		t.Fatalf("ChangedRefs = %#v, want %#v", gotRefs, wantRefs)
	}
}

func TestCanonicalRefsKeepsStructuralRelationRefsDistinctAcrossPermutations(t *testing.T) {
	// Arrange.
	rootBang, err := bundle.ParseRelationRef("a!")
	if err != nil {
		t.Fatal(err)
	}
	fragmentBoundary, err := bundle.ParseRelationRef("a#z")
	if err != nil {
		t.Fatal(err)
	}
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
		{embedded, rootBang, fragmentBoundary, fragment, embedded},
		{fragmentBoundary, embedded, embedded, fragment, rootBang},
		{fragment, embedded, rootBang, embedded, fragmentBoundary},
	}
	want := []relationRefKey{
		{id: "a!"},
		{id: "a", fragment: "z"},
		{id: "source", fragment: "part"},
		{id: "source#part"},
	}

	for index, input := range inputs {
		seen := make(map[relationRefKey]bundle.RelationRef)

		// Act.
		for _, ref := range input {
			if err := addRef(context.Background(), seen, ref); err != nil {
				t.Fatalf("permutation %d addRef() error = %v", index, err)
			}
		}
		got, err := canonicalRefs(context.Background(), seen)

		// Assert.
		if err != nil {
			t.Fatalf("permutation %d canonicalRefs() error = %v", index, err)
		}
		if len(got) != len(want) {
			t.Fatalf("permutation %d canonicalRefs() count = %d, want %d", index, len(got), len(want))
		}
		for refIndex := range want {
			if key := relationRefKeyOf(got[refIndex]); key != want[refIndex] {
				t.Fatalf("permutation %d ref %d = %#v, want %#v", index, refIndex, key, want[refIndex])
			}
		}
	}
}

func TestRelationInventoryKeepsStructuralRelationEndpointsDistinct(t *testing.T) {
	// Arrange.
	embeddedID, err := bundle.NewConceptID([]string{"source#part"})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := bundle.ParseConceptID("source")
	if err != nil {
		t.Fatal(err)
	}
	targetID, err := bundle.ParseConceptID("target")
	if err != nil {
		t.Fatal(err)
	}
	conceptID, err := bundle.ParseConceptID("concept")
	if err != nil {
		t.Fatal(err)
	}
	relations := []bundle.Relation{
		{
			Source: bundle.RelationRef{ID: embeddedID},
			Type:   "uses",
			Target: bundle.RelationRef{ID: targetID},
		},
		{
			Source: bundle.RelationRef{ID: sourceID, Fragment: "part"},
			Type:   "uses",
			Target: bundle.RelationRef{ID: targetID},
		},
	}
	concepts := map[string]semanticConcept{
		conceptID.String(): {id: conceptID, relations: relations},
	}

	// Act.
	got, err := relationInventory(context.Background(), concepts, []string{conceptID.String()})

	// Assert.
	if err != nil {
		t.Fatalf("relationInventory() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("relationInventory() count = %d, want 2 distinct structural endpoint tuples", len(got))
	}
}

func TestDerive_IdenticalSourcesReturnCanonicalNonNilEmptyArrays(t *testing.T) {
	// Arrange.
	source := loadProjectionBundle(t, projectionSource{
		"a.md": []byte("---\ntype: Note\n---\nA\n"),
	})

	// Act.
	projection, err := Derive(context.Background(), source, source)

	// Assert.
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if projection.ChangedFiles == nil || len(projection.ChangedFiles) != 0 {
		t.Fatalf("ChangedFiles = %#v, want non-nil empty", projection.ChangedFiles)
	}
	if projection.ChangedRefs == nil || len(projection.ChangedRefs) != 0 {
		t.Fatalf("ChangedRefs = %#v, want non-nil empty", projection.ChangedRefs)
	}
}

func TestDerive_CanceledContextStopsProjection(t *testing.T) {
	// Arrange.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := loadProjectionBundle(t, projectionSource{
		"a.md": []byte("---\ntype: Note\n---\nA\n"),
	})

	// Act.
	projection, err := Derive(ctx, source, source)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Derive() error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(projection, Projection{}) {
		t.Fatalf("Derive() projection = %#v, want zero value on error", projection)
	}
}

func TestDeriveFiles_MetadataCollisionCannotBecomeWriteElisionOrRename(t *testing.T) {
	// Arrange.
	metadata := capturedMetadata{size: 4, sha256: sha256.Sum256([]byte("same"))}
	base := map[string]capturedMetadata{
		"same.asset":  metadata,
		"old-a.asset": metadata,
		"old-b.asset": metadata,
	}
	target := map[string]capturedMetadata{
		"same.asset": metadata,
		"new.asset":  metadata,
	}
	var comparisons [][2]string
	equal := func(
		_ context.Context,
		_ *bundle.Bundle,
		leftPath string,
		_ *bundle.Bundle,
		rightPath string,
	) (bool, error) {
		comparisons = append(comparisons, [2]string{leftPath, rightPath})
		switch {
		case leftPath == "old-b.asset" && rightPath == "new.asset":
			return true, nil
		default:
			return false, nil
		}
	}
	wantChanges := []store.FileChange{
		{Kind: store.FileRename, Path: "new.asset", From: "old-b.asset"},
		{Kind: store.FileDelete, Path: "old-a.asset"},
		{Kind: store.FileWrite, Path: "same.asset"},
	}
	wantComparisons := [][2]string{
		{"same.asset", "same.asset"},
		{"old-a.asset", "new.asset"},
		{"old-b.asset", "new.asset"},
	}

	// Act.
	changes, err := deriveFiles(
		context.Background(),
		nil,
		nil,
		base,
		target,
		equal,
	)

	// Assert.
	if err != nil {
		t.Fatalf("deriveFiles() error = %v", err)
	}
	if !reflect.DeepEqual(changes, wantChanges) {
		t.Fatalf("deriveFiles() = %#v, want %#v", changes, wantChanges)
	}
	if !reflect.DeepEqual(comparisons, wantComparisons) {
		t.Fatalf("exact comparisons = %#v, want stable exhaustive %#v", comparisons, wantComparisons)
	}
}

func TestDerive_LargeCapturedPayloadDoesNotAllocateCorpusCopies(t *testing.T) {
	// Arrange.
	const payloadSize = 8 << 20
	payload := bytes.Repeat([]byte{0x5a}, payloadSize)
	base := loadProjectionBundle(t, projectionSource{"large.asset": payload})
	target := loadProjectionBundle(t, projectionSource{"large.asset": payload})
	payload = nil
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	// Act.
	projection, err := Derive(context.Background(), base, target)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(base)
	runtime.KeepAlive(target)

	// Assert.
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if len(projection.ChangedFiles) != 0 || len(projection.ChangedRefs) != 0 {
		t.Fatalf("Derive() = %#v, want no changes", projection)
	}
	const allocationLimit = 2 << 20
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= allocationLimit {
		t.Fatalf("Derive() TotalAlloc = %d bytes, want < %d bytes", allocated, allocationLimit)
	}
}

func TestDerive_LargeMarkdownFrontmatterDoesNotAllocateDocumentCopies(t *testing.T) {
	// Arrange.
	const payloadSize = 8 << 20
	document := make([]byte, 0, payloadSize+64)
	document = append(document, "---\ntype: Note\npayload: "...)
	document = append(document, bytes.Repeat([]byte{'x'}, payloadSize)...)
	document = append(document, "\n---\nBody\n"...)
	base := loadProjectionBundle(t, projectionSource{"large.md": document})
	target := loadProjectionBundle(t, projectionSource{"large.md": document})
	document = nil
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	// Act.
	projection, err := Derive(context.Background(), base, target)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(base)
	runtime.KeepAlive(target)

	// Assert.
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if len(projection.ChangedFiles) != 0 || len(projection.ChangedRefs) != 0 {
		t.Fatalf("Derive() = %#v, want no changes", projection)
	}
	const allocationLimit = 4 << 20
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= allocationLimit {
		t.Fatalf("Derive() TotalAlloc = %d bytes, want < %d bytes", allocated, allocationLimit)
	}
}

func TestDeriveRefs_CancellationInterruptsSemanticRelationCopy(t *testing.T) {
	// Arrange.
	var source strings.Builder
	source.WriteString("---\ntype: Note\nrelations:\n  uses:\n")
	const relationCount = 512
	for range relationCount {
		source.WriteString("    - target: target\n")
	}
	source.WriteString("---\nSource\n")
	loaded := loadProjectionBundle(t, projectionSource{
		"source.md": []byte(source.String()),
		"target.md": []byte("---\ntype: Note\n---\nTarget\n"),
	})
	sourceID, err := bundle.ParseConceptID("source")
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}
	if got := len(loaded.SemanticLinksFrom(sourceID)); got != relationCount {
		t.Fatalf("SemanticLinksFrom() length = %d, want %d", got, relationCount)
	}
	ctx := newCancelAfterChecksContext(64)

	// Act.
	refs, err := deriveRefs(ctx, loaded, loaded)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("deriveRefs() error = %v, want context.Canceled", err)
	}
	if refs != nil {
		t.Fatalf("deriveRefs() = %#v, want nil on cancellation", refs)
	}
	if checks := ctx.checks.Load(); checks < 64 {
		t.Fatalf("context checks = %d, want at least 64", checks)
	}
}

func TestDerive_ConcurrentImmutableSemanticProjection(t *testing.T) {
	// Arrange.
	base := loadProjectionBundle(t, projectionSource{
		"a.md": []byte("---\ntype: Note\nparts:\n  - id: old\nrelations:\n  uses:\n    - target: b#part\n---\nA\n"),
		"b.md": []byte("---\ntype: Note\nparts:\n  - id: part\n---\nB\n"),
	})
	target := loadProjectionBundle(t, projectionSource{
		"a.md": []byte("---\ntype: Note\nparts:\n  - id: new\nrelations:\n  uses:\n    - target: b#part\n---\nA changed\n"),
		"b.md": []byte("---\ntype: Note\nparts:\n  - id: part\n---\nB\n"),
	})
	const goroutines = 8
	const iterations = 16
	errs := make(chan error, goroutines)
	var wait sync.WaitGroup

	// Act.
	for range goroutines {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range iterations {
				projection, err := Derive(context.Background(), base, target)
				if err != nil {
					errs <- err
					return
				}
				if len(projection.ChangedFiles) != 1 || len(projection.ChangedRefs) != 3 {
					errs <- errors.New("unexpected concurrent projection")
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errs)

	// Assert.
	for err := range errs {
		t.Fatal(err)
	}
}

type cancelAfterChecksContext struct {
	remaining atomic.Int64
	checks    atomic.Int64
	done      chan struct{}
	once      sync.Once
}

func newCancelAfterChecksContext(checks int64) *cancelAfterChecksContext {
	ctx := &cancelAfterChecksContext{done: make(chan struct{})}
	ctx.remaining.Store(checks)
	return ctx
}

func (c *cancelAfterChecksContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterChecksContext) Done() <-chan struct{}       { return c.done }
func (c *cancelAfterChecksContext) Value(any) any               { return nil }

func (c *cancelAfterChecksContext) Err() error {
	c.checks.Add(1)
	if c.remaining.Add(-1) <= 0 {
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

type projectionSource map[string][]byte

func (s projectionSource) Paths(context.Context) ([]string, error) {
	paths := make([]string, 0, len(s))
	for path := range s {
		paths = append(paths, path)
	}
	return paths, nil
}

func (s projectionSource) ReadFile(_ context.Context, path string) ([]byte, error) {
	data, ok := s[path]
	if !ok {
		return nil, errors.New("projection source: file not found")
	}
	return append([]byte(nil), data...), nil
}

func loadProjectionBundle(t *testing.T, source projectionSource) *bundle.Bundle {
	t.Helper()
	loaded, err := bundle.Load(context.Background(), source)
	if err != nil {
		t.Fatalf("bundle.Load() error = %v", err)
	}
	return loaded
}
