package viewer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
)

func fixture(t *testing.T, files map[string]string) *bundle.Bundle {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRenderEscapesScriptAndMarkdownHTML(t *testing.T) {
	// Arrange
	b := fixture(t, map[string]string{"index.md": "---\nokf_version: \"0.2\"\n---\n# Index\n", "a.md": "---\ntype: Note\ntitle: '</script><script>alert(1)</script>'\n---\n<script>alert(2)</script>\n\n<img src=\"https://example.test/a\" onerror=\"alert(4)\">\n\n![x](https://example.test/image.png)\n\n[jump](javascript:alert(3))\n\nCitation[^toolkit].\n\n[^toolkit]: Source details.\n\ncode"})
	// Act
	p, err := Build(context.Background(), b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	html := string(rendered)
	// Assert
	if strings.Contains(html, "</script><script>alert") {
		t.Fatal("unescaped script delimiter")
	}
	if strings.Contains(html, "<img") {
		t.Fatal("image can trigger network fetch")
	}
	if strings.Contains(html, "<script>alert(2)") {
		t.Fatal("raw Markdown HTML escaped incorrectly")
	}
	if strings.Contains(p.Concepts[0].HTML, "onerror=") {
		t.Fatal("raw Markdown HTML attribute survived")
	}
	if strings.Contains(html, "href=\"javascript:") {
		t.Fatal("unsafe URL retained")
	}
	if !strings.Contains(html, "script-src 'sha256-") {
		t.Fatal("script hash policy missing")
	}
	if !strings.Contains(html, "code") {
		t.Fatal("Markdown code missing")
	}
	if !strings.Contains(p.Concepts[0].HTML, "Source details.") || strings.Contains(p.Concepts[0].HTML, "[^toolkit]") {
		t.Fatal("footnote rendering missing")
	}
}

func TestBuildLinksLimitsAndReferenceTime(t *testing.T) {
	// Arrange
	b := fixture(t, map[string]string{"index.md": "---\nokf_version: \"0.2\"\n---\n# Index\n", "a.md": "---\ntype: Note\nstatus: draft\n---\nSee [B](b.md) and [missing](gone.md).", "b.md": "---\ntype: Note\n---\n# Русский"})
	asOf := time.Date(2026, 9, 26, 12, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	// Act
	p, err := Build(context.Background(), b, Options{AsOf: &asOf, MaxNodes: 2, TemporalProfile: bundle.TemporalProfileInstant})
	if err != nil {
		t.Fatal(err)
	}
	// Assert
	if len(p.Concepts) != 2 || len(p.Edges) != 2 {
		t.Fatalf("projection = %#v", p)
	}
	if !p.Edges[0].Exists || p.Edges[1].Exists {
		t.Fatalf("broken link flags = %#v", p.Edges)
	}
	if p.ReferenceBasis != "explicit-instant" || p.ReferenceTime != asOf.Format(time.RFC3339) {
		t.Fatal("reference time mismatch")
	}
	if p.Concepts[0].Status != "draft" {
		t.Fatal("status mismatch")
	}
	if _, err := Build(context.Background(), b, Options{MaxNodes: 1}); err == nil {
		t.Fatal("missing node limit error")
	}
}

func TestExportCollisionCancellationAndAtomicOverwrite(t *testing.T) {
	// Arrange
	b := fixture(t, map[string]string{"index.md": "---\nokf_version: \"0.2\"\n---\n# Index\n", "a.md": "---\ntype: Note\n---\nBody"})
	output := filepath.Join(b.Root(), "viewer.html")
	if err := os.WriteFile(output, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	// Act, assert collision and cancellation preserve old bytes
	if err := Export(context.Background(), b, output, Options{}); err == nil {
		t.Fatal("collision accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Export(ctx, b, output, Options{Overwrite: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	old, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "old" {
		t.Fatal("old file changed")
	}
	// Act, assert overwrite and protected paths
	if err := Export(context.Background(), b, output, Options{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "OKF knowledge viewer") {
		t.Fatal("output missing")
	}
	if err := Export(context.Background(), b, filepath.Join(b.Root(), "a.md"), Options{Overwrite: true}); err == nil {
		t.Fatal("concept overwrite allowed")
	}
	if err := Export(context.Background(), b, filepath.Join(b.Root(), ".okf", "viewer.html"), Options{Overwrite: true}); err == nil {
		t.Fatal("store overwrite allowed")
	}
	store := filepath.Join(b.Root(), ".okf")
	if err := os.Mkdir(store, 0755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(b.Root(), "alias")
	if err := os.Symlink(store, alias); err != nil {
		t.Fatal(err)
	}
	if err := Export(context.Background(), b, filepath.Join(alias, "viewer.html"), Options{}); err == nil {
		t.Fatal("symlink alias to store accepted")
	}
	if _, err := os.Stat(filepath.Join(store, "viewer.html")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("store artifact: %v", err)
	}
}

func TestEmptyBundleExportsSearchableShell(t *testing.T) {
	// Arrange
	b := fixture(t, map[string]string{"index.md": "---\nokf_version: \"0.2\"\n---\n# Index\n"})
	// Act
	p, err := Build(context.Background(), b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := Render(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	// Assert
	if len(p.Concepts) != 0 || len(p.Edges) != 0 {
		t.Fatalf("empty projection = %#v", p)
	}
	if !strings.Contains(string(h), "This bundle contains no concepts") || !strings.Contains(string(h), "Search") {
		t.Fatal("empty shell missing")
	}
}

func TestDefaultDateTemporalProfile(t *testing.T) {
	// Arrange
	b := fixture(t, map[string]string{"index.md": "---\nokf_version: \"0.2\"\n---\n# Index\n", "a.md": "---\ntype: Note\nstale_after: 2026-09-26\n---\nBody"})
	at := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	// Act
	p, err := Build(context.Background(), b, Options{AsOf: &at})
	if err != nil {
		t.Fatal(err)
	}
	// Assert
	if p.SpecRevision != string(bundle.TemporalProfileDate) || p.ReferenceTime != "2026-09-26" || p.ReferenceBasis != "explicit-civil-date" || p.Concepts[0].Staleness != "stale" {
		t.Fatalf("date projection = %#v", p)
	}
}

func TestLargeBundleUsesBoundedListLayout(t *testing.T) {
	// Arrange
	files := map[string]string{"index.md": "---\nokf_version: \"0.2\"\n---\n# Index\n"}
	for i := 0; i < 1001; i++ {
		files[fmt.Sprintf("c-%04d.md", i)] = "---\ntype: Note\n---\nBody"
	}
	b := fixture(t, files)
	// Act
	p, err := Build(context.Background(), b, Options{MaxNodes: 1001})
	if err != nil {
		t.Fatal(err)
	}
	html, err := Render(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	// Assert
	if len(p.Concepts) != 1001 || !strings.Contains(string(html), "Show graph") {
		t.Fatal("list-first large layout missing")
	}
	if _, err := Build(context.Background(), b, Options{MaxNodes: 1000}); err == nil {
		t.Fatal("limit not enforced")
	}
}

func TestUnicodeConceptIDAndMissingMetadata(t *testing.T) {
	// Arrange
	b := fixture(t, map[string]string{"index.md": "---\nokf_version: \"0.2\"\n---\n# Index\n", "папка/необычный.md": "---\ntype: Note\n---\nТекст"})
	// Act
	p, err := Build(context.Background(), b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := Render(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	// Assert
	if len(p.Concepts) != 1 || p.Concepts[0].ID != "папка/необычный" || p.Concepts[0].Title != "папка/необычный" {
		t.Fatalf("concept=%#v", p.Concepts)
	}
	if p.Concepts[0].Staleness != "unevaluated" || p.Concepts[0].Trust == "" {
		t.Fatal("missing metadata projection")
	}
	if !strings.Contains(string(h), "encodeURIComponent(id)") || !strings.Contains(string(h), "decodeURIComponent") {
		t.Fatal("canonical ID deep-link code missing")
	}
}
