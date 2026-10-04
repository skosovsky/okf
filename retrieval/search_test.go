package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/skosovsky/okf/bundle"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func loadFixture(t *testing.T, files map[string]string) *bundle.Bundle {
	t.Helper()
	fs := fstest.MapFS{}
	for p, v := range files {
		fs[p] = &fstest.MapFile{Data: []byte(v)}
	}
	b, e := bundle.Load(t.Context(), bundle.SourceFromFS(fs))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestSectionsUseOriginalLinesAndIgnoreFencedHeadings(t *testing.T) {
	// Arrange.
	raw := "---\r\ntype: Note\r\n---\r\n\r\n# Guide\r\nIntro\r\n\r\nRetry policy\r\n------------\r\nDeadline with jitter.\r\n```md\r\n# fake heading\r\n```\r\n\r\n## Кэш\r\nКэш использует ревизию.\r\n"
	b := loadFixture(t, map[string]string{"fn:example.md": raw})
	// Act.
	result, e := Search(t.Context(), b, "jitter deadline", 5)
	// Assert.
	if e != nil || len(result.Hits) != 1 {
		t.Fatalf("%#v %v", result, e)
	}
	h := result.Hits[0]
	if h.Heading != "Retry policy" || h.LineStart != 8 || h.LineEnd != 14 || h.ConceptID != "fn:example" {
		t.Fatalf("range %#v", h)
	}
	sum := sha256.Sum256([]byte(raw))
	if h.Digest != hex.EncodeToString(sum[:]) || !strings.Contains(raw, h.Snippet) {
		t.Fatalf("source mismatch %#v", h)
	}
	ru, e := Search(t.Context(), b, "КЭШ ревизию", 5)
	if e != nil || len(ru.Hits) != 1 || ru.Hits[0].LineStart != 15 {
		t.Fatalf("RU %#v %v", ru, e)
	}
}
func TestANDUnicodeTiesTruncationAndSnapshot(t *testing.T) {
	// Arrange.
	raw := "---\ntype: Note\n---\n# Café\nAlpha beta.\n"
	b := loadFixture(t, map[string]string{"b.md": raw, "a.md": raw})
	before := b.ReadFile
	// Act.
	one, e := Search(t.Context(), b, "CAFE\u0301 beta", 1)
	two, e2 := Search(t.Context(), b, "CAFE\u0301 beta", 1)
	none, e3 := Search(t.Context(), b, "alpha missing", 5)
	// Assert.
	if e != nil || e2 != nil || e3 != nil || !reflect.DeepEqual(one, two) || one.Total != 2 || !one.Truncated || one.Hits[0].ConceptID != "a" || len(none.Hits) != 0 {
		t.Fatalf("%#v %#v %#v %v %v %v", one, two, none, e, e2, e3)
	}
	captured, _ := before("a.md")
	captured[0] = 'x'
	again, _ := Search(t.Context(), b, "CAFE\u0301 beta", 1)
	if again.Revision != one.Revision {
		t.Fatal("caller changed snapshot")
	}
	changed := loadFixture(t, map[string]string{"b.md": raw, "a.md": raw + "Changed\n"})
	after, _ := Search(t.Context(), changed, "beta", 1)
	if after.Revision == one.Revision {
		t.Fatal("changed source has same revision")
	}
}
func TestInvalidCancellationAndResourceCaps(t *testing.T) {
	// Arrange.
	b := loadFixture(t, map[string]string{"a.md": "---\ntype: Note\n---\nhello"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Act and Assert.
	for _, q := range []string{"", "!!!", strings.Repeat("x", 513), string([]byte{255})} {
		if _, e := Search(t.Context(), b, q, 5); !errors.Is(e, ErrQuery) {
			t.Fatalf("query %q: %v", q, e)
		}
	}
	if _, e := Search(ctx, b, "hello", 5); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := Search(t.Context(), b, "hello", 0); !errors.Is(e, ErrQuery) {
		t.Fatal(e)
	}
	bad := loadFixture(t, map[string]string{"bad.md": "---\ntype: [\n---\nhello"})
	if _, e := Search(t.Context(), bad, "hello", 5); !errors.Is(e, ErrParse) {
		t.Fatal(e)
	}
	huge := loadFixture(t, map[string]string{"a.md": "---\ntype: Note\n---\n# " + strings.Repeat("a", 1025)})
	if _, e := Search(t.Context(), huge, "a", 5); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
}
func TestFilesystemNoFollowAndRelativeRoot(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.md"), []byte("---\ntype: Note\n---\nsecret"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	os.WriteFile(filepath.Join(root, "a.md"), []byte("---\ntype: Note\n---\npublic"), 0600)
	// Act.
	b, e := Load(t.Context(), root)
	if e != nil {
		t.Fatal(e)
	}
	r, e := Search(t.Context(), b, "secret", 5)
	// Assert.
	if e != nil || len(r.Hits) > 0 {
		t.Fatalf("escaped %#v %v", r, e)
	}
	alias := filepath.Join(t.TempDir(), "link")
	os.Symlink(root, alias)
	if _, e = Load(t.Context(), alias); e == nil {
		t.Fatal("symlink root accepted")
	}
	// A bundle loaded by the old public loader can have a relative Root.
	cwd, _ := os.Getwd()
	rel, _ := filepath.Rel(cwd, root)
	old, e := bundle.LoadBundle(rel)
	if e != nil {
		t.Fatal(e)
	}
	hit, e := Search(t.Context(), old, "public", 5)
	if e != nil || hit.Hits[0].Path != "a.md" {
		t.Fatalf("relative root %#v %v", hit, e)
	}
}

func TestSnippetFinalTokenAndCancellation(t *testing.T) {
	// Arrange.
	raw := strings.Repeat("other ", 200) + "needle"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// Act.
	got, err := snippet(t.Context(), raw, []string{"needle"})
	_, canceled := snippet(ctx, raw, []string{"needle"})
	// Assert.
	if err != nil || !strings.Contains(got, "needle") || !strings.Contains(raw, got) {
		t.Fatalf("snippet %q: %v", got, err)
	}
	if !errors.Is(canceled, context.Canceled) {
		t.Fatal(canceled)
	}
}
func TestAggregateTokenSectionAndOutputCaps(t *testing.T) {
	// Arrange: each fixture violates a distinct published resource cap.
	fixtures := []map[string]string{
		{"a.md": "---\ntype: Note\n---\n" + strings.Repeat("term ", MaxTokens+1)},
		{"a.md": "---\ntype: Note\n---\n" + strings.Repeat("# heading\n", MaxSections+1)},
		{strings.Repeat("a", 4000) + ".md": "---\ntype: Note\n---\n" + strings.Repeat("# "+strings.Repeat("b", 1000)+"\nneedle\n", 100)},
	}
	for _, files := range fixtures {
		b := loadFixture(t, files)
		// Act.
		_, err := Search(t.Context(), b, "needle", 100)
		// Assert.
		if !errors.Is(err, ErrLimit) {
			t.Fatalf("cap: %v", err)
		}
	}
}
