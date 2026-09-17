package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
)

func TestReadConceptProjectsOnlyCapturedBundleRevision(t *testing.T) {
	tests := []struct {
		name string
		swap func(*testing.T, string)
	}{
		{
			name: "concept inode swap",
			swap: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(root, "nested", "a.md")
				if err := os.Rename(path, path+".captured"); err != nil {
					t.Fatalf("rename captured concept: %v", err)
				}
				writeTestFile(t, root, "nested/a.md", "---\ntype: Evil\ntitle: Replacement\n---\nReplacement body.\n")
			},
		},
		{
			name: "bundle pathname swap",
			swap: func(t *testing.T, root string) {
				t.Helper()
				capturedRoot := root + ".captured"
				if err := os.Rename(root, capturedRoot); err != nil {
					t.Fatalf("rename captured root: %v", err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(capturedRoot) })
				if err := os.Mkdir(root, 0o755); err != nil {
					t.Fatalf("replace bundle root: %v", err)
				}
				writeTestFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n")
				writeTestFile(t, root, "nested/a.md", "---\ntype: Evil\ntitle: Replacement\n---\nReplacement body.\n")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			const raw = "---\ntype: Note\ntitle: Captured\n---\nCaptured body.\n"
			writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n")
			writeTestFile(t, root, "nested/a.md", raw)
			loaded, err := loadBundleContext(t.Context(), root)
			if err != nil {
				t.Fatalf("load captured bundle: %v", err)
			}
			id, err := bundle.ParseConceptID("nested/a")
			if err != nil {
				t.Fatalf("parse concept id: %v", err)
			}
			test.swap(t, root)

			// Act.
			result := readConceptFromBundleContext(t.Context(), root, loaded, id)

			// Assert.
			if result.IsError {
				t.Fatalf("read captured concept returned error: %s", resultText(t, result))
			}
			if got := resultText(t, result); got != raw {
				t.Fatalf("raw text = %q, want captured bytes %q", got, raw)
			}
			var structured readConceptStructured
			decodeStructuredContent(t, result.StructuredContent, &structured)
			if structured.ID != "nested/a" || structured.Path != "nested/a.md" ||
				structured.FrontmatterYAML != "type: Note\ntitle: Captured\n" ||
				structured.Body != "Captured body." {
				t.Fatalf("captured structure = %#v", structured)
			}
			if structured.Projection.Type == nil || *structured.Projection.Type != "Note" ||
				structured.Projection.Title == nil || *structured.Projection.Title != "Captured" {
				t.Fatalf("captured projection = %#v", structured.Projection)
			}
			if structured.Version.Effective == nil || *structured.Version.Effective != bundle.OKFVersion {
				t.Fatalf("captured version = %#v", structured.Version)
			}
		})
	}
}

func TestReadConceptCapturedLookupAndCopyCancellation(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\n"+strings.Repeat("x", 256<<10))
	loaded, err := loadBundleContext(t.Context(), root)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	id, err := bundle.ParseConceptID("a")
	if err != nil {
		t.Fatalf("parse concept id: %v", err)
	}
	missingID, err := bundle.ParseConceptID("missing")
	if err != nil {
		t.Fatalf("parse missing concept id: %v", err)
	}

	t.Run("missing captured path", func(t *testing.T) {
		// Act.
		result := readConceptFromBundleContext(t.Context(), root, loaded, missingID)

		// Assert.
		assertStableErrorCode(t, result, "concept_not_found")
		if got := resultText(t, result); got != "concept not found: missing" {
			t.Fatalf("missing text = %q", got)
		}
	})

	t.Run("pre-copy cancellation", func(t *testing.T) {
		// Arrange.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act.
		result := readConceptFromBundleContext(ctx, root, loaded, id)

		// Assert.
		assertStableErrorCode(t, result, "operation_cancelled")
	})

	t.Run("mid-copy cancellation", func(t *testing.T) {
		// Arrange. Two ConceptPath checks, one ReadFile preflight check, and
		// one 64 KiB copy chunk are allowed; the second chunk is cancelled.
		ctx := &readConceptCancelAfterContext{allowed: 4}

		// Act.
		result := readConceptFromBundleContext(ctx, root, loaded, id)

		// Assert.
		assertStableErrorCode(t, result, "operation_cancelled")
		if ctx.checks.Load() < 5 {
			t.Fatalf("context checks = %d, want cancellation during retained-byte copy", ctx.checks.Load())
		}
	})
}

func TestOwnedBundleSourceLifecycleIsFailClosed(t *testing.T) {
	closeFailure := errors.New("close snapshot failure")
	loadFailure := errors.New("load snapshot failure")

	t.Run("nil owner", func(t *testing.T) {
		// Act.
		loaded, err := loadOwnedBundleSourceContext(t.Context(), nil)

		// Assert.
		if loaded != nil || !errors.Is(err, errNilOwnedBundleSource) {
			t.Fatalf("load nil owner = (%#v, %v)", loaded, err)
		}
	})

	t.Run("close failure prevents publication", func(t *testing.T) {
		// Arrange.
		source := &ownedSnapshotTestSource{
			paths:    []string{"a.md"},
			files:    map[string][]byte{"a.md": []byte("---\ntype: Note\n---\nA.\n")},
			closeErr: closeFailure,
		}

		// Act.
		loaded, err := loadOwnedBundleSourceContext(t.Context(), source)

		// Assert.
		if loaded != nil {
			t.Fatalf("loaded = %#v, want nil after close failure", loaded)
		}
		if !errors.Is(err, closeFailure) {
			t.Fatalf("error = %v, want close identity", err)
		}
		if source.closeCalls.Load() != 1 {
			t.Fatalf("Close calls = %d, want exactly 1", source.closeCalls.Load())
		}
	})

	t.Run("load and close identities are joined", func(t *testing.T) {
		// Arrange.
		source := &ownedSnapshotTestSource{pathsErr: loadFailure, closeErr: closeFailure}

		// Act.
		loaded, err := loadOwnedBundleSourceContext(t.Context(), source)

		// Assert.
		if loaded != nil || !errors.Is(err, loadFailure) || !errors.Is(err, closeFailure) {
			t.Fatalf("load result = (%#v, %v), want both error identities", loaded, err)
		}
		if source.closeCalls.Load() != 1 {
			t.Fatalf("Close calls = %d, want exactly 1", source.closeCalls.Load())
		}
	})
}

func TestReadConceptProductionCallGraphUsesOnlyBundleSnapshot(t *testing.T) {
	// Arrange.
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	packageDirectory := filepath.Dir(currentFile)
	entries, err := os.ReadDir(packageDirectory)
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	wantSnapshotCalls := map[string]bool{
		"ConceptPathContext":             false,
		"Get":                            false,
		"ReadFileContext":                false,
		"VersionDeclarationStateContext": false,
		"VersionResolutionContext":       false,
	}
	forbiddenOSCalls := map[string]struct{}{
		"Open": {}, "OpenFile": {}, "OpenRoot": {}, "ReadFile": {}, "Stat": {}, "Lstat": {},
	}
	foundFunctions := map[string]bool{
		"handleReadConcept":            false,
		"readConceptFromBundleContext": false,
	}

	// Act / Assert.
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(packageDirectory, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			function, ok := node.(*ast.FuncDecl)
			if !ok || !foundFunctionsCandidate(function.Name.Name) {
				return true
			}
			foundFunctions[function.Name.Name] = true
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.Ident:
					if typed.Name == "readPinnedConcept" {
						t.Errorf("%s calls obsolete %s", function.Name.Name, typed.Name)
					}
				case *ast.CallExpr:
					selector, ok := typed.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "os" {
						if _, forbidden := forbiddenOSCalls[selector.Sel.Name]; forbidden {
							t.Errorf("%s directly calls os.%s", function.Name.Name, selector.Sel.Name)
						}
					}
					if _, wanted := wantSnapshotCalls[selector.Sel.Name]; wanted {
						wantSnapshotCalls[selector.Sel.Name] = true
					}
				}
				return true
			})
			return false
		})
	}
	for function, found := range foundFunctions {
		if !found {
			t.Errorf("production function %s not found", function)
		}
	}
	for method, found := range wantSnapshotCalls {
		if !found {
			t.Errorf("captured Bundle call %s not found", method)
		}
	}
}

func foundFunctionsCandidate(name string) bool {
	return name == "handleReadConcept" || name == "readConceptFromBundleContext"
}

func decodeStructuredContent(t *testing.T, value any, target any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
}

func assertStableErrorCode(t *testing.T, result *mcp.CallToolResult, want string) {
	t.Helper()
	var envelope errorEnvelope
	decodeStructuredContent(t, result.StructuredContent, &envelope)
	if envelope.Code != want {
		t.Fatalf("error code = %q, want %q", envelope.Code, want)
	}
}

type readConceptCancelAfterContext struct {
	checks  atomic.Int64
	allowed int64
}

func (c *readConceptCancelAfterContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *readConceptCancelAfterContext) Done() <-chan struct{}       { return nil }
func (c *readConceptCancelAfterContext) Value(any) any               { return nil }

func (c *readConceptCancelAfterContext) Err() error {
	if c.checks.Add(1) > c.allowed {
		return context.Canceled
	}
	return nil
}

type ownedSnapshotTestSource struct {
	paths      []string
	files      map[string][]byte
	pathsErr   error
	closeErr   error
	closeCalls atomic.Int64
}

func (s *ownedSnapshotTestSource) Paths(context.Context) ([]string, error) {
	return append([]string(nil), s.paths...), s.pathsErr
}

func (s *ownedSnapshotTestSource) ReadFile(_ context.Context, path string) ([]byte, error) {
	data, ok := s.files[path]
	if !ok {
		return nil, errors.New("missing owned snapshot test file")
	}
	return append([]byte(nil), data...), nil
}

func (s *ownedSnapshotTestSource) Close() error {
	s.closeCalls.Add(1)
	return s.closeErr
}
