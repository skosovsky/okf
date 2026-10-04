package viewer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRenderLanguageKeepsProjectionAndSecurity(t *testing.T) {
	// Arrange
	p := Projection{Concepts: []Concept{{ID: "тест", Title: "</script><script>unsafe</script>", HTML: "<p>Authored English</p>"}}, Edges: []Edge{}}
	original, _ := json.Marshal(p)
	// Act
	legacy, err := Render(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := RenderWithOptions(context.Background(), p, RenderOptions{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	russian, err := RenderWithOptions(context.Background(), p, RenderOptions{Language: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	// Assert
	if !bytes.Equal(legacy, explicit) {
		t.Fatal("existing Render must remain the English default")
	}
	for language, data := range map[string][]byte{"en": explicit, "ru": russian} {
		if !bytes.Contains(data, []byte(`<html lang="`+language+`">`)) {
			t.Fatalf("missing initial language %s", language)
		}
		match := regexp.MustCompile(`(?s)<script id="okf-data" type="application/json">(.*?)</script>`).FindSubmatch(data)
		if len(match) != 2 || !bytes.Equal(match[1], original) {
			t.Fatal("language changed semantic JSON")
		}
		script := regexp.MustCompile(`(?s)</script><script>(.*?)</script></body>`).FindSubmatch(data)
		if len(script) != 2 {
			t.Fatal("missing embedded script")
		}
		sum := sha256.Sum256(script[1])
		if !bytes.Contains(data, []byte("'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")) {
			t.Fatal("CSP does not match production script")
		}
		if !bytes.Contains(data, []byte("connect-src 'none'")) || bytes.Contains(data, []byte("</script><script>unsafe")) {
			t.Fatal("unsafe export")
		}
	}
}

func TestLanguageRejectsBeforePublication(t *testing.T) {
	// Arrange
	b := fixture(t, map[string]string{"index.md": "# Index\n"})
	for _, language := range []string{"RU", "fr", `ru"><script>`, " en"} {
		t.Run(language, func(t *testing.T) {
			dir := t.TempDir()
			existing := filepath.Join(dir, "existing.html")
			if err := os.WriteFile(existing, []byte("previous"), 0600); err != nil {
				t.Fatal(err)
			}
			// Act
			_, renderErr := RenderWithOptions(context.Background(), Projection{}, RenderOptions{Language: language})
			replaceErr := Export(context.Background(), b, existing, Options{Language: language, Overwrite: true})
			createErr := Export(context.Background(), b, filepath.Join(dir, "new.html"), Options{Language: language})
			// Assert
			if renderErr == nil || replaceErr == nil || createErr == nil {
				t.Fatal("unsupported language accepted")
			}
			content, err := os.ReadFile(existing)
			if err != nil || string(content) != "previous" {
				t.Fatal("previous export changed")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatal("invalid language created output or temporary file")
			}
		})
	}
}

func TestBuildLanguageIsPresentationOnly(t *testing.T) {
	// Arrange
	b := fixture(t, map[string]string{"index.md": "# Index\n", "note.md": "---\ntype: Note\n---\n# Note\n"})
	// Act
	english, err := Build(context.Background(), b, Options{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	russian, err := Build(context.Background(), b, Options{Language: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	en, _ := json.Marshal(english)
	ru, _ := json.Marshal(russian)
	// Assert
	if !bytes.Equal(en, ru) {
		t.Fatal("presentation locale affects projection")
	}
	rendered, err := RenderWithOptions(context.Background(), russian, RenderOptions{Language: "ru"})
	if err != nil || !strings.Contains(string(rendered), `<html lang="ru">`) {
		t.Fatal("Russian export unavailable")
	}
}
