package mutation

import (
	"context"
	"fmt"
	"sort"
	"testing"
)

// comparisonOverlaySource is deliberately self-contained so the same benchmark
// can run against baseline commit 43f7214, before the parser-backed mutation
// implementation, and the result tree.
type comparisonOverlaySource map[string][]byte

func (s comparisonOverlaySource) Paths(context.Context) ([]string, error) {
	paths := make([]string, 0, len(s))
	for name := range s {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths, nil
}

func (s comparisonOverlaySource) ReadFile(_ context.Context, name string) ([]byte, error) {
	data, ok := s[name]
	if !ok {
		return nil, fmt.Errorf("comparison source: file not found %q", name)
	}
	return append([]byte(nil), data...), nil
}

func BenchmarkParserBackedOverlayComparison(b *testing.B) {
	const (
		files        = 10_000
		payloadBytes = 256
	)
	payload := make([]byte, payloadBytes)

	b.Run("clone-changed-10000", func(b *testing.B) {
		o := NewOverlay(comparisonOverlaySource{})
		for i := 0; i < files; i++ {
			if err := o.Put(fmt.Sprintf("file-%05d.md", i), payload); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
		b.ReportMetric(files, "files/op")
		b.ReportMetric(payloadBytes, "payload-bytes/file")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			clone := cloneOverlayForTest(b, o)
			if len(clone.changed) != files {
				b.Fatalf("clone files = %d, want %d", len(clone.changed), files)
			}
		}
	})

	b.Run("paths-base-10000", func(b *testing.B) {
		base := make(comparisonOverlaySource, files)
		for i := 0; i < files; i++ {
			base[fmt.Sprintf("file-%05d.md", i)] = payload
		}
		o := NewOverlay(base)
		if _, err := o.Paths(context.Background()); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ReportMetric(files, "files/op")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			paths, err := o.Paths(context.Background())
			if err != nil {
				b.Fatal(err)
			}
			if len(paths) != files {
				b.Fatalf("paths = %d, want %d", len(paths), files)
			}
		}
	})

	b.Run("rename-staged-10000", func(b *testing.B) {
		o := NewOverlay(comparisonOverlaySource{})
		for i := 0; i < files; i++ {
			if err := o.Put(fmt.Sprintf("file-%05d.md", i), payload); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
		b.ReportMetric(files, "files/op")
		b.ReportMetric(payloadBytes, "payload-bytes/file")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			candidate := cloneOverlayForTest(b, o)
			if err := candidate.Rename(context.Background(), "file-00000.md", "renamed.md"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
