package mutation

import (
	"context"
	"crypto/sha256"
	"errors"
	"hash"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

type adversarialOverlaySource struct {
	paths    []string
	pathsErr error
	files    map[string][]byte
	cancel   context.CancelFunc
}

type countdownContext struct {
	context.Context
	mu        sync.Mutex
	remaining int
}

func (c *countdownContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remaining > 0 {
		c.remaining--
	}
	if c.remaining == 0 {
		return context.Canceled
	}
	return nil
}

func (s *adversarialOverlaySource) Paths(context.Context) ([]string, error) {
	if s.cancel != nil {
		s.cancel()
	}
	return append([]string(nil), s.paths...), s.pathsErr
}

func TestOverlayContextCancellationDuringCachedTraversalAndDefensiveCopy(t *testing.T) {
	// Arrange.
	files := make(memorySource, 256)
	for i := 0; i < 256; i++ {
		files["file"+strconv.Itoa(i)+".md"] = []byte("content")
	}
	o := NewOverlay(files)
	if _, err := o.Paths(context.Background()); err != nil {
		t.Fatal(err)
	}
	large := make([]byte, 1<<20)
	if err := o.Put("large.bin", large); err != nil {
		t.Fatal(err)
	}

	// Act.
	paths, pathsErr := o.Paths(&countdownContext{Context: context.Background(), remaining: 8})
	data, readErr := o.ReadFile(&countdownContext{Context: context.Background(), remaining: 8}, "large.bin")

	// Assert.
	if !errors.Is(pathsErr, context.Canceled) || paths != nil {
		t.Fatalf("Paths() = %#v, %v", paths, pathsErr)
	}
	if !errors.Is(readErr, context.Canceled) || data != nil {
		t.Fatalf("ReadFile() = %d bytes, %v", len(data), readErr)
	}
}

func (s *adversarialOverlaySource) ReadFile(context.Context, string) ([]byte, error) {
	if s.cancel != nil {
		s.cancel()
	}
	return []byte("payload"), nil
}

type countingOverlaySource struct {
	mu         sync.Mutex
	pathsCalls int
	paths      []string
	files      map[string][]byte
}

func (s *countingOverlaySource) Paths(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pathsCalls++
	return append([]string(nil), s.paths...), nil
}

func (s *countingOverlaySource) ReadFile(_ context.Context, name string) ([]byte, error) {
	data, ok := s.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func cloneOverlayForTest(tb testing.TB, overlay *Overlay) *Overlay {
	tb.Helper()
	cloned, err := overlay.cloneContext(context.Background())
	if err != nil {
		tb.Fatal(err)
	}
	return cloned
}

func TestOverlayCloneSharesPrivatePayloadAndPathsCache(t *testing.T) {
	// Arrange.
	base := &countingOverlaySource{paths: []string{"base.md"}, files: map[string][]byte{"base.md": []byte("base")}}
	o := NewOverlay(base)
	if err := o.Put("changed.md", []byte("changed")); err != nil {
		t.Fatal(err)
	}
	clone := cloneOverlayForTest(t, o)

	// Act.
	first, err := o.Paths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := clone.Paths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	read, err := clone.ReadFile(context.Background(), "changed.md")
	if err != nil {
		t.Fatal(err)
	}
	read[0] = 'X'

	// Assert.
	if got, want := base.pathsCalls, 1; got != want {
		t.Fatalf("base Paths calls = %d, want %d", got, want)
	}
	if got, want := len(first), 2; got != want || len(second) != want {
		t.Fatalf("paths = %v / %v, want two paths", first, second)
	}
	if o.changed["changed.md"].data[0] != 'c' || clone.changed["changed.md"].data[0] != 'c' {
		t.Fatal("ReadFile exposed private payload")
	}
	if &o.changed["changed.md"].data[0] != &clone.changed["changed.md"].data[0] {
		t.Fatal("clone copied immutable payload")
	}
}

func TestOverlayStagedRenameReusesPayload(t *testing.T) {
	// Arrange.
	base := &countingOverlaySource{files: map[string][]byte{}}
	o := NewOverlay(base)
	if err := o.Put("from.md", []byte("payload")); err != nil {
		t.Fatal(err)
	}
	before := o.changed["from.md"].data

	// Act.
	if err := o.Rename(context.Background(), "from.md", "to.md"); err != nil {
		t.Fatal(err)
	}

	// Assert.
	after := o.changed["to.md"].data
	if &before[0] != &after[0] {
		t.Fatal("staged rename copied payload")
	}
}

func TestOverlayIdentityRenameStillProvesContextBaseAndExistence(t *testing.T) {
	tests := []struct {
		name    string
		overlay *Overlay
		ctx     context.Context
		want    error
		wantOK  bool
	}{
		{name: "existing", overlay: NewOverlay(&countingOverlaySource{paths: []string{"a.md"}, files: map[string][]byte{"a.md": []byte("a")}}), ctx: context.Background(), wantOK: true},
		{name: "missing", overlay: NewOverlay(&countingOverlaySource{files: map[string][]byte{}}), ctx: context.Background()},
		{name: "nil overlay", ctx: context.Background()},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests = append(tests, struct {
		name    string
		overlay *Overlay
		ctx     context.Context
		want    error
		wantOK  bool
	}{name: "cancelled", overlay: NewOverlay(&countingOverlaySource{paths: []string{"a.md"}, files: map[string][]byte{"a.md": []byte("a")}}), ctx: cancelled, want: context.Canceled})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			err := tt.overlay.Rename(tt.ctx, "a.md", "a.md")

			// Assert.
			if tt.wantOK && err != nil {
				t.Fatalf("Rename() error = %v", err)
			}
			if !tt.wantOK && err == nil {
				t.Fatal("Rename() unexpectedly succeeded")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("Rename() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestOverlayCreateAndRenameNeverTreatProbeFailureAsAbsence(t *testing.T) {
	probeErr := errors.New("path enumeration failed")

	t.Run("create", func(t *testing.T) {
		base := &adversarialOverlaySource{pathsErr: probeErr}
		o := NewOverlay(base)
		err := o.Create(context.Background(), "new.md", []byte("new"))
		if !errors.Is(err, probeErr) || len(o.changed) != 0 || len(o.deleted) != 0 || len(o.manifestChanged) != 0 {
			t.Fatalf("error/state = %v / %#v", err, o)
		}
	})

	t.Run("rename staged source", func(t *testing.T) {
		base := &adversarialOverlaySource{pathsErr: probeErr}
		o := NewOverlay(base)
		if err := o.Put("from.md", []byte("payload")); err != nil {
			t.Fatal(err)
		}
		err := o.Rename(context.Background(), "from.md", "to.md")
		if !errors.Is(err, probeErr) || len(o.changed) != 1 || string(o.changed["from.md"].data) != "payload" {
			t.Fatalf("error/state = %v / %#v", err, o.changed)
		}
	})
}

func TestOverlayCancellationNeverPublishesMutation(t *testing.T) {
	t.Run("create canceled during paths", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := &adversarialOverlaySource{cancel: cancel}
		o := NewOverlay(base)
		err := o.Create(ctx, "new.md", []byte("new"))
		if !errors.Is(err, context.Canceled) || len(o.changed) != 0 || len(o.deleted) != 0 {
			t.Fatalf("error/state = %v / %#v", err, o)
		}
	})

	t.Run("rename canceled after base read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := &adversarialOverlaySource{paths: []string{"from.md"}, files: map[string][]byte{"from.md": []byte("payload")}}
		o := NewOverlay(base)
		if _, err := o.Paths(ctx); err != nil {
			t.Fatal(err)
		}
		base.cancel = cancel
		err := o.Rename(ctx, "from.md", "to.md")
		if !errors.Is(err, context.Canceled) || len(o.changed) != 0 || len(o.deleted) != 0 {
			t.Fatalf("error/state = %v / changed=%#v deleted=%#v", err, o.changed, o.deleted)
		}
	})
}

func TestOverlayPerformanceGates_NoPayloadCopiesOrUnchangedHashes(t *testing.T) {
	// Arrange. Use a payload large enough that an accidental deep copy is
	// observable in both the backing-array identity and allocation profile.
	payload := make([]byte, 1<<20)
	for i := range payload {
		payload[i] = byte(i)
	}
	algorithm := &overlayPerformanceHash{}
	manifest, err := store.NewManifestWithAlgorithm([]store.ManifestEntry{{Path: "base.md", Content: []byte("base")}}, algorithm)
	if err != nil {
		t.Fatal(err)
	}
	base := manifestMemorySource{memorySource: memorySource{"base.md": []byte("base")}, manifest: manifest}
	o := NewOverlay(base)
	if err := o.Put("from.md", payload); err != nil {
		t.Fatal(err)
	}
	putHashes := algorithm.newCalls

	// Act.
	clone := cloneOverlayForTest(t, o)
	if err := clone.Rename(context.Background(), "from.md", "to.md"); err != nil {
		t.Fatal(err)
	}

	// Assert. Clone and staged rename are metadata operations: they must retain
	// the owned bytes and digest rather than re-copying or re-hashing them.
	if &o.changed["from.md"].data[0] != &clone.changed["to.md"].data[0] {
		t.Fatal("clone or staged rename copied payload bytes")
	}
	if got := algorithm.newCalls; got != putHashes {
		t.Fatalf("unchanged hash constructions = %d, want %d", got, putHashes)
	}
	if small, large := cloneAllocsForPayload(t, 64), cloneAllocsForPayload(t, 1<<20); small != large {
		t.Fatalf("clone allocations depend on payload size: small=%v large=%v", small, large)
	}
}

type overlayPerformanceHash struct{ newCalls int }

func (a *overlayPerformanceHash) Name() string { return "overlay-performance-sha256" }
func (a *overlayPerformanceHash) New() hash.Hash {
	a.newCalls++
	return sha256.New()
}

func cloneAllocsForPayload(tb testing.TB, size int) float64 {
	tb.Helper()
	payload := make([]byte, size)
	o := NewOverlay(&countingOverlaySource{files: map[string][]byte{}})
	if err := o.Put("payload.md", payload); err != nil {
		tb.Fatal(err)
	}
	return testing.AllocsPerRun(100, func() { cloneOverlayForTest(tb, o) })
}

func TestOverlayConcurrentPathsAndReadsAreSafe(t *testing.T) {
	// Arrange.
	base := &countingOverlaySource{paths: []string{"base.md"}, files: map[string][]byte{"base.md": []byte("base")}}
	o := NewOverlay(base)
	if err := o.Put("changed.md", []byte("changed")); err != nil {
		t.Fatal(err)
	}

	// Act.
	var group sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := o.Paths(context.Background()); err != nil {
				errs <- err
				return
			}
			_, err := o.ReadFile(context.Background(), "changed.md")
			errs <- err
		}()
	}
	group.Wait()
	close(errs)

	// Assert.
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := base.pathsCalls; got != 1 {
		t.Fatalf("base Paths calls = %d, want cached single enumeration", got)
	}
}

func BenchmarkOverlayClone(b *testing.B) {
	for _, files := range []int{0, 1, 100, 10_000} {
		b.Run(strconv.Itoa(files), func(b *testing.B) {
			base := &countingOverlaySource{files: map[string][]byte{}}
			o := NewOverlay(base)
			for i := 0; i < files; i++ {
				name := "file" + strconv.Itoa(i) + ".md"
				if err := o.Put(name, []byte("payload")); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				clone := cloneOverlayForTest(b, o)
				if len(clone.changed) != files {
					b.Fatalf("clone files = %d, want %d", len(clone.changed), files)
				}
			}
		})
	}
}

func BenchmarkOverlayRename(b *testing.B) {
	for _, files := range []int{1, 100, 10_000} {
		for _, kind := range []string{"base", "staged"} {
			b.Run(kind+"-"+strconv.Itoa(files), func(b *testing.B) {
				base := benchmarkOverlayBase(files)
				o := NewOverlay(base)
				if kind == "staged" {
					if err := o.Put("file0.md", []byte("payload")); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					candidate := cloneOverlayForTest(b, o)
					if err := candidate.Rename(context.Background(), "file0.md", "renamed.md"); err != nil {
						b.Fatal(err)
					}
					data, err := candidate.ReadFile(context.Background(), "renamed.md")
					if err != nil {
						b.Fatal(err)
					}
					if got, want := string(data), "payload"; got != want {
						b.Fatalf("renamed payload = %q, want %q", got, want)
					}
				}
			})
		}
	}
}

func BenchmarkOverlayPaths(b *testing.B) {
	for _, files := range []int{0, 1, 100, 10_000} {
		b.Run("base-"+strconv.Itoa(files), func(b *testing.B) {
			base := benchmarkOverlayBase(files)
			o := NewOverlay(base)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				paths, err := o.Paths(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				if got, want := len(paths), files; got != want {
					b.Fatalf("paths = %d, want %d", got, want)
				}
			}
		})
		b.Run("delta-"+strconv.Itoa(files), func(b *testing.B) {
			base := benchmarkOverlayBase(files)
			o := NewOverlay(base)
			if err := o.Put("changed.md", []byte("changed")); err != nil {
				b.Fatal(err)
			}
			if files > 0 {
				if err := o.Delete("file0.md"); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				paths, err := o.Paths(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				want := files
				if files == 0 {
					want = 1
				}
				if got := len(paths); got != want {
					b.Fatalf("paths = %d, want %d", got, want)
				}
			}
		})
	}
}

func BenchmarkOverlayManifestDeltaRevision(b *testing.B) {
	for _, filesCount := range []int{0, 1, 100, 10_000} {
		b.Run(strconv.Itoa(filesCount), func(b *testing.B) {
			entries := make([]store.ManifestEntry, 0, filesCount)
			files := make(memorySource, filesCount)
			for i := 0; i < filesCount; i++ {
				name := "file" + strconv.Itoa(i) + ".md"
				content := []byte("payload")
				entries = append(entries, store.ManifestEntry{Path: name, Content: content})
				files[name] = content
			}
			manifest, err := store.NewManifest(entries)
			if err != nil {
				b.Fatal(err)
			}
			o := NewOverlay(manifestMemorySource{memorySource: files, manifest: manifest})
			if err := o.Put("changed.md", []byte("changed")); err != nil {
				b.Fatal(err)
			}
			if filesCount > 0 {
				if err := o.Delete("file0.md"); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				revision, err := o.Manifest().Revision()
				if err != nil {
					b.Fatal(err)
				}
				if !revision.Valid() {
					b.Fatal("manifest delta revision is invalid")
				}
			}
		})
	}
}

func benchmarkOverlayBase(files int) *countingOverlaySource {
	base := &countingOverlaySource{files: make(map[string][]byte, files), paths: make([]string, 0, files)}
	for i := 0; i < files; i++ {
		name := "file" + strconv.Itoa(i) + ".md"
		base.paths = append(base.paths, name)
		base.files[name] = []byte("payload")
	}
	return base
}

var _ bundle.Source = (*Overlay)(nil)
