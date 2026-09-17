package bundle

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBundleCapturedFileMetadata_ReturnsImmutableValues(t *testing.T) {
	// Arrange.
	data := []byte("captured payload")
	loaded, err := Load(context.Background(), projectionBundleSource{"asset.bin": data})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	wantDigest := sha256.Sum256(data)
	data[0] ^= 0xff

	// Act.
	size, digest, ok := loaded.CapturedFileMetadata("asset.bin")
	digest[0] ^= 0xff
	againSize, againDigest, againOK := loaded.CapturedFileMetadata("asset.bin")

	// Assert.
	if !ok || size != uint64(len(data)) {
		t.Fatalf("CapturedFileMetadata() = (%d, _, %v), want (%d, _, true)", size, ok, len(data))
	}
	if !againOK || againSize != size || againDigest != wantDigest {
		t.Fatalf("second CapturedFileMetadata() = (%d, %x, %v), want (%d, %x, true)", againSize, againDigest, againOK, size, wantDigest)
	}
	if _, _, ok := (*Bundle)(nil).CapturedFileMetadata("asset.bin"); ok {
		t.Fatal("nil Bundle CapturedFileMetadata() ok = true, want false")
	}
	if _, _, ok := loaded.CapturedFileMetadata("missing.bin"); ok {
		t.Fatal("missing CapturedFileMetadata() ok = true, want false")
	}
}

func TestCapturedFilesEqualContext_ExactComparisonRejectsMetadataCollision(t *testing.T) {
	// Arrange.
	metadata := capturedFile{size: 4, sha256: sha256.Sum256([]byte("left"))}
	left := &Bundle{
		contents: map[string][]byte{"asset.bin": []byte("left")},
		captured: map[string]capturedFile{"asset.bin": metadata},
	}
	right := &Bundle{
		contents: map[string][]byte{"asset.bin": []byte("rift")},
		captured: map[string]capturedFile{"asset.bin": metadata},
	}

	// Act.
	equal, err := CapturedFilesEqualContext(context.Background(), left, "asset.bin", right, "asset.bin")

	// Assert.
	if err != nil {
		t.Fatalf("CapturedFilesEqualContext() error = %v", err)
	}
	if equal {
		t.Fatal("CapturedFilesEqualContext() = true for exact-byte mismatch under colliding metadata")
	}
}

func TestCapturedFilesEqualContext_IsCancellableBetweenChunks(t *testing.T) {
	// Arrange.
	data := make([]byte, 3*(64<<10))
	metadata := capturedFile{size: uint64(len(data)), sha256: sha256.Sum256(data)}
	left := &Bundle{
		contents: map[string][]byte{"asset.bin": data},
		captured: map[string]capturedFile{"asset.bin": metadata},
	}
	right := &Bundle{
		contents: map[string][]byte{"asset.bin": append([]byte(nil), data...)},
		captured: map[string]capturedFile{"asset.bin": metadata},
	}
	ctx := &cancelAfterErrChecksContext{allowed: 2}

	// Act.
	equal, err := CapturedFilesEqualContext(ctx, left, "asset.bin", right, "asset.bin")

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CapturedFilesEqualContext() error = %v, want context.Canceled", err)
	}
	if equal {
		t.Fatal("CapturedFilesEqualContext() = true after cancellation")
	}
}

func TestCapturedFileMetadataContext_IsCancellableBetweenChunks(t *testing.T) {
	// Arrange.
	data := make([]byte, 3*(64<<10))
	ctx := &cancelAfterErrChecksContext{allowed: 2}

	// Act.
	metadata, err := capturedFileMetadataContext(ctx, data)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("capturedFileMetadataContext() error = %v, want context.Canceled", err)
	}
	if metadata != (capturedFile{}) {
		t.Fatalf("capturedFileMetadataContext() = %#v, want zero value", metadata)
	}
}

func TestLoad_CancellationDuringCapturedInventoryHashReturnsNoBundle(t *testing.T) {
	// Arrange.
	ctx := &armedCancelAfterErrChecksContext{allowed: 6}
	source := armingBundleSource{
		ctx:  ctx,
		data: make([]byte, 3*(64<<10)),
	}

	// Act.
	loaded, err := Load(ctx, source)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Load() error = %v, want context.Canceled", err)
	}
	if loaded != nil {
		t.Fatalf("Load() bundle = %#v, want nil", loaded)
	}
}

func TestCapturedFilesAccessors_ConcurrentReadsAreRaceSafe(t *testing.T) {
	// Arrange.
	loaded, err := Load(context.Background(), projectionBundleSource{
		"asset.bin": make([]byte, 256<<10),
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	const readers = 16
	var group sync.WaitGroup
	group.Add(readers)
	errs := make(chan error, readers)

	// Act.
	for range readers {
		go func() {
			defer group.Done()
			for range 32 {
				if _, _, ok := loaded.CapturedFileMetadata("asset.bin"); !ok {
					errs <- errors.New("captured metadata missing")
					return
				}
				equal, err := CapturedFilesEqualContext(
					context.Background(),
					loaded,
					"asset.bin",
					loaded,
					"asset.bin",
				)
				if err != nil {
					errs <- err
					return
				}
				if !equal {
					errs <- errors.New("identical captured file compared unequal")
					return
				}
			}
		}()
	}
	group.Wait()
	close(errs)

	// Assert.
	for err := range errs {
		t.Error(err)
	}
}

type projectionBundleSource map[string][]byte

func (s projectionBundleSource) Paths(context.Context) ([]string, error) {
	paths := make([]string, 0, len(s))
	for path := range s {
		paths = append(paths, path)
	}
	return paths, nil
}

func (s projectionBundleSource) ReadFile(_ context.Context, path string) ([]byte, error) {
	data, ok := s[path]
	if !ok {
		return nil, errors.New("file not found")
	}
	return append([]byte(nil), data...), nil
}

type armingBundleSource struct {
	ctx  *armedCancelAfterErrChecksContext
	data []byte
}

func (s armingBundleSource) Paths(context.Context) ([]string, error) {
	return []string{"asset.bin"}, nil
}

func (s armingBundleSource) ReadFile(context.Context, string) ([]byte, error) {
	s.ctx.armed.Store(true)
	return s.data, nil
}

type cancelAfterErrChecksContext struct {
	checks  atomic.Int64
	allowed int64
}

func (c *cancelAfterErrChecksContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterErrChecksContext) Done() <-chan struct{}       { return nil }
func (c *cancelAfterErrChecksContext) Value(any) any               { return nil }

func (c *cancelAfterErrChecksContext) Err() error {
	if c.checks.Add(1) > c.allowed {
		return context.Canceled
	}
	return nil
}

type armedCancelAfterErrChecksContext struct {
	armed   atomic.Bool
	checks  atomic.Int64
	allowed int64
}

func (c *armedCancelAfterErrChecksContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *armedCancelAfterErrChecksContext) Done() <-chan struct{}       { return nil }
func (c *armedCancelAfterErrChecksContext) Value(any) any               { return nil }

func (c *armedCancelAfterErrChecksContext) Err() error {
	if !c.armed.Load() {
		return nil
	}
	if c.checks.Add(1) > c.allowed {
		return context.Canceled
	}
	return nil
}
