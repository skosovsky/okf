package setup

import (
	"context"
	"fmt"
	"github.com/skosovsky/okf/validator"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, files map[string]string) Options {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatal(err)
	}
	for name, b := range files {
		p := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(b), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return Options{Source: src, Target: filepath.Join(dir, "target"), Type: "Guide"}
}
func TestManagedCopyPreservesBytes(t *testing.T) {
	// Arrange.
	raw := "---\ntype: Guide\ncustom: [one, two] # keep comment\n---\n\n# Root\n[child](sub/child.md)\n"
	opts := fixture(t, map[string]string{"root.md": raw, "sub/child.md": "# Child\n[parent](../root.md)\n"})
	// Act.
	p, err := Preview(context.Background(), opts)
	if err != nil || !p.Applicable {
		t.Fatalf("preview: %v %+v", err, p)
	}
	same, _ := Preview(context.Background(), opts)
	if p.Digest != same.Digest {
		t.Fatal("nondeterministic plan")
	}
	applied, err := Apply(context.Background(), opts, p.Digest)
	// Assert.
	if err != nil || !applied.Published {
		t.Fatalf("apply: %v %+v", err, applied)
	}
	for name, want := range map[string]string{"root.md": raw, "sub/child.md": "---\ntype: Guide\n---\n# Child\n[parent](../root.md)\n"} {
		b, e := os.ReadFile(filepath.Join(opts.Target, name))
		if e != nil || string(b) != want {
			t.Fatalf("copied %s: %q %v", name, b, e)
		}
	}
	b, _ := os.ReadFile(filepath.Join(opts.Source, "root.md"))
	if string(b) != raw {
		t.Fatal("source changed")
	}
	if report := validator.ValidatePath(opts.Target, &validator.ValidatorConfig{CheckLinks: true}); report.ErrorCount() != 0 {
		t.Fatalf("nonconformant: %+v", report)
	}
	if _, e := Apply(context.Background(), opts, p.Digest); e == nil {
		t.Fatal("replay overwrites target")
	}
}
func TestBlockersNeverPublish(t *testing.T) {
	for _, tc := range []struct{ name, body, code string }{{"malformed", "---\ncustom: [\n---\n# Body", "frontmatter"}, {"unterminated", "---\ncustom: value", "frontmatter"}, {"duplicate", "---\ntype: A\ntype: B\n---\nbody", "frontmatter"}, {"invalid_type", "---\ntype: []\n---\nbody", "invalid_type"}, {"asset", "![asset](asset.png)", "unsupported_link"}, {"unselected", "[other](other.md)", "unsupported_link"}, {"html", "<img src=\"asset.png\">", "unsupported_html"}, {"reserved", "body", "reserved_path"}} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			name := "a.md"
			if tc.name == "reserved" {
				name = "index.md"
			}
			opts := fixture(t, map[string]string{name: tc.body})
			// Act.
			p, err := Preview(context.Background(), opts)
			_, applyErr := Apply(context.Background(), opts, p.Digest)
			// Assert.
			if err != nil || p.Applicable || applyErr == nil {
				t.Fatalf("blocker absent %v %+v %v", err, p, applyErr)
			}
			found := false
			for _, d := range p.Diagnostics {
				if d.Code == tc.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", tc.code, p)
			}
			if _, err := os.Stat(opts.Target); !os.IsNotExist(err) {
				t.Fatal("target published")
			}
		})
	}
}
func TestSourceDriftCancellationAndSymlinks(t *testing.T) {
	// Arrange.
	opts := fixture(t, map[string]string{"a.md": "# Original"})
	p, _ := Preview(context.Background(), opts)
	// Act.
	if err := os.WriteFile(filepath.Join(opts.Source, "a.md"), []byte("# Changed"), 0644); err != nil {
		t.Fatal(err)
	}
	_, driftErr := Apply(context.Background(), opts, p.Digest)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, cancelErr := Apply(ctx, opts, p.Digest)
	if err := os.Symlink("a.md", filepath.Join(opts.Source, "link.md")); err != nil {
		t.Fatal(err)
	}
	blocked, _ := Preview(context.Background(), opts)
	// Assert.
	if driftErr == nil || !strings.Contains(driftErr.Error(), "digest") || cancelErr != context.Canceled || blocked.Applicable {
		t.Fatalf("drift=%v cancel=%v plan=%+v", driftErr, cancelErr, blocked)
	}
	if _, err := os.Stat(opts.Target); !os.IsNotExist(err) {
		t.Fatal("partial target")
	}
	stages, _ := filepath.Glob(filepath.Join(filepath.Dir(opts.Target), ".okf-setup-*"))
	if len(stages) != 0 {
		t.Fatal("stages leaked")
	}
}
func TestSelectionAndMissingType(t *testing.T) {
	// Arrange.
	opts := fixture(t, map[string]string{"index.md": "ordinary index", "note.md": "# Note\n"})
	opts.Files = []string{"note.md"}
	opts.Type = ""
	// Act.
	missing, _ := Preview(context.Background(), opts)
	opts.Type = "Decision"
	p, err := Preview(context.Background(), opts)
	_, applyErr := Apply(context.Background(), opts, p.Digest)
	// Assert.
	if missing.Applicable || err != nil || !p.Applicable || applyErr != nil {
		t.Fatalf("missing=%+v selected=%+v err=%v %v", missing, p, err, applyErr)
	}
	b, _ := os.ReadFile(filepath.Join(opts.Source, "index.md"))
	if string(b) != "ordinary index" {
		t.Fatal("reserved source modified")
	}
}

func TestPublicationBoundaryFailures(t *testing.T) {
	for _, kind := range []string{"collision", "drift", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			// Arrange.
			opts := fixture(t, map[string]string{"sub/a.md": "# Body"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			plan, _ := Preview(ctx, opts)
			// Act.
			result, err := apply(ctx, opts, plan.Digest, func() error {
				switch kind {
				case "collision":
					return os.Mkdir(opts.Target, 0755)
				case "drift":
					return os.WriteFile(filepath.Join(opts.Source, "sub/a.md"), []byte("# Drift"), 0644)
				default:
					cancel()
					return nil
				}
			})
			// Assert.
			if err == nil || result.Published {
				t.Fatalf("unexpected publication: %+v %v", result, err)
			}
			if kind == "collision" {
				files, e := os.ReadDir(opts.Target)
				if e != nil || len(files) != 0 {
					t.Fatal("collision target changed")
				}
			} else if _, e := os.Stat(opts.Target); !os.IsNotExist(e) {
				t.Fatal("partial target")
			}
			stages, _ := filepath.Glob(filepath.Join(filepath.Dir(opts.Target), ".okf-setup-*"))
			if len(stages) != 0 {
				t.Fatal("stage leaked")
			}
		})
	}
}
func TestSymlinkEscapeAndSelectionEscapes(t *testing.T) {
	// Arrange.
	opts := fixture(t, map[string]string{"a.md": "# Body"})
	if err := os.Symlink("/etc/passwd", filepath.Join(opts.Source, "escape.md")); err != nil {
		t.Fatal(err)
	}
	// Act.
	p, err := Preview(context.Background(), opts)
	opts.Files = []string{"../a.md"}
	bad, selectionErr := Preview(context.Background(), opts)
	// Assert.
	if err != nil || p.Applicable || selectionErr != nil || bad.Applicable {
		t.Fatalf("escape accepted: %+v %v %+v %v", p, err, bad, selectionErr)
	}
}

func TestTypeInsertionRetainsUnknownYAMLAndBody(t *testing.T) {
	// Arrange.
	original := "---\r\ncustom:\r\n  flags: [alpha, beta] # retained\r\n---\r\n\r\n# Existing\r\n\r\nNo provenance is known.\r\n"
	opts := fixture(t, map[string]string{"a.md": original})
	opts.Type = "Custom: explicit"
	// Act.
	p, err := Preview(context.Background(), opts)
	if err != nil || !p.Applicable {
		t.Fatalf("preview %v %+v", err, p)
	}
	_, err = Apply(context.Background(), opts, p.Digest)
	b, readErr := os.ReadFile(filepath.Join(opts.Target, "a.md"))
	// Assert.
	expected := "---\r\ntype: 'Custom: explicit'\n" + strings.TrimPrefix(original, "---\r\n")
	if err != nil || readErr != nil || string(b) != expected {
		t.Fatalf("insertion %v %v %q", err, readErr, b)
	}
	if strings.Contains(string(b), "sources:") || strings.Contains(string(b), "verified:") {
		t.Fatal("invented metadata")
	}
}

func TestApplyPublishesAllNestedDocumentsAndIndexes(t *testing.T) {
	// Arrange.
	const documents = 501
	files := make(map[string]string, documents)
	for i := 0; i < documents; i++ {
		files[fmt.Sprintf("folder-%03d/note.md", i)] = fmt.Sprintf("# Note %d\nPreserved source %d.\n", i, i)
	}
	opts := fixture(t, files)
	plan, err := Preview(context.Background(), opts)
	if err != nil || !plan.Applicable || len(plan.Files) != documents {
		t.Fatalf("preview: %v %+v", err, plan)
	}
	// Act.
	result, err := Apply(context.Background(), opts, plan.Digest)
	// Assert.
	if err != nil || !result.Published {
		t.Fatalf("apply: %v %+v", err, result)
	}
	for name, original := range files {
		copied, err := os.ReadFile(filepath.Join(opts.Target, name))
		if err != nil || string(copied) != "---\ntype: Guide\n---\n"+original {
			t.Fatalf("missing/changed document %s: %v %q", name, err, copied)
		}
		source, err := os.ReadFile(filepath.Join(opts.Source, name))
		if err != nil || string(source) != original {
			t.Fatalf("source modified %s: %v", name, err)
		}
		index, err := os.ReadFile(filepath.Join(opts.Target, filepath.Dir(name), "index.md"))
		if err != nil || !strings.Contains(string(index), "(note.md)") {
			t.Fatalf("missing/invalid index for %s: %v %q", name, err, index)
		}
	}
	index, err := os.ReadFile(filepath.Join(opts.Target, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < documents; i++ {
		if !strings.Contains(string(index), fmt.Sprintf("(folder-%03d/index.md)", i)) {
			t.Fatalf("root index omits folder %d", i)
		}
	}
	count := 0
	if err := filepath.WalkDir(opts.Target, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != documents*2+1 {
		t.Fatalf("partial output: got %d files, want %d", count, documents*2+1)
	}
	report := validator.ValidatePath(opts.Target, &validator.ValidatorConfig{CheckLinks: true, CheckOrphans: true})
	if report.ErrorCount() != 0 {
		t.Fatalf("published bundle not conformant: %+v", report)
	}
}

func TestAutolinksFollowExplicitURIPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		supported  bool
	}{
		{"file", "<file:///etc/passwd>", false},
		{"ftp", "<ftp://example.test/asset.png>", false},
		{"javascript", "<javascript:alert(1)>", false},
		{"https", "<https://example.test/guide>", true},
		{"mailto", "<mailto:reader@example.test>", true},
		{"email", "<reader@example.test>", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			body := "# Existing note\n" + tc.body + "\n"
			opts := fixture(t, map[string]string{"note.md": body})
			// Act.
			plan, previewErr := Preview(context.Background(), opts)
			result, applyErr := Apply(context.Background(), opts, plan.Digest)
			// Assert.
			if previewErr != nil || plan.Applicable != tc.supported {
				t.Fatalf("URI policy mismatch: %v %+v", previewErr, plan)
			}
			if tc.supported {
				if applyErr != nil || !result.Published {
					t.Fatalf("supported autolink rejected: %v %+v", applyErr, result)
				}
				copied, err := os.ReadFile(filepath.Join(opts.Target, "note.md"))
				if err != nil || string(copied) != "---\ntype: Guide\n---\n"+body {
					t.Fatalf("autolink changed: %v %q", err, copied)
				}
			} else {
				if applyErr == nil || result.Published {
					t.Fatalf("unsupported autolink published: %v %+v", applyErr, result)
				}
				found := false
				for _, d := range plan.Diagnostics {
					if d.Code == "unsupported_link" {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing diagnostic: %+v", plan)
				}
				if _, err := os.Stat(opts.Target); !os.IsNotExist(err) {
					t.Fatal("target published")
				}
			}
		})
	}
}
