package store

import (
	"context"
	"errors"
	"testing"
)

func TestManifestWithDigestsContextReturnsLexicalFirstValidationError(t *testing.T) {
	// Arrange.
	manifest, err := NewManifest([]ManifestEntry{{Path: "seed.md", Content: []byte("seed")}})
	if err != nil {
		t.Fatal(err)
	}
	validDigest, ok := manifest.Digest("seed.md")
	if !ok {
		t.Fatal("seed digest is missing")
	}
	values := map[string]string{
		"../invalid.md": validDigest,
		"a.md":          "not-a-qualified-digest",
		"z.md":          "also-not-a-qualified-digest",
	}
	orders := [][]string{
		{"z.md", "a.md", "../invalid.md"},
		{"../invalid.md", "a.md", "z.md"},
	}
	const want = `invalid manifest: invalid path "../invalid.md"`

	for _, order := range orders {
		for run := 0; run < 64; run++ {
			input := orderedDigestMap(order, values)

			// Act.
			composed, composeErr := manifest.WithDigestsContext(context.Background(), input)

			// Assert.
			if !errors.Is(composeErr, ErrInvalidManifest) || composeErr.Error() != want {
				t.Fatalf("order=%q run=%d error=%v want %q", order, run, composeErr, want)
			}
			if composed.Valid() {
				t.Fatalf("order=%q run=%d returned valid manifest after error", order, run)
			}
		}
	}
}

func TestManifestPathsContextReturnsLexicalFirstInvalidDigest(t *testing.T) {
	// Arrange.
	manifest, err := NewManifest([]ManifestEntry{{Path: "seed.md", Content: []byte("seed")}})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"a.md": "not-a-qualified-digest",
		"m.md": "also-not-a-qualified-digest",
		"z.md": "still-not-a-qualified-digest",
	}
	orders := [][]string{
		{"z.md", "m.md", "a.md"},
		{"a.md", "m.md", "z.md"},
	}
	const want = `invalid manifest: invalid digest for "a.md"`

	for _, order := range orders {
		for run := 0; run < 64; run++ {
			forged := manifest.Clone()
			forged.digests = orderedDigestMap(order, values)

			// Act.
			paths, pathsErr := forged.PathsContext(context.Background())

			// Assert.
			if paths != nil {
				t.Fatalf("order=%q run=%d paths=%q want nil", order, run, paths)
			}
			if !errors.Is(pathsErr, ErrInvalidManifest) || pathsErr.Error() != want {
				t.Fatalf("order=%q run=%d error=%v want %q", order, run, pathsErr, want)
			}
		}
	}
}

func orderedDigestMap(order []string, values map[string]string) map[string]string {
	out := make(map[string]string, len(order))
	for _, path := range order {
		out[path] = values[path]
	}
	return out
}
