package mutation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

type plannerCountingSource struct {
	files     memorySource
	manifest  store.Manifest
	readCalls int
}

func TestLinearMutationHelpersHonorCancellation(t *testing.T) {
	large := make([]byte, 256<<10)

	t.Run("apply byte patches", func(t *testing.T) {
		ctx := &countdownContext{Context: context.Background(), remaining: 4}
		out, err := applyBytePatchesContext(ctx, large, nil)
		if !errors.Is(err, context.Canceled) || out != nil {
			t.Fatalf("applyBytePatchesContext() = %d bytes, %v", len(out), err)
		}
	})

	t.Run("outside patch comparison", func(t *testing.T) {
		ctx := &countdownContext{Context: context.Background(), remaining: 4}
		equal, err := bytesOutsidePatchesEqualContext(ctx, large, large, nil)
		if !errors.Is(err, context.Canceled) || equal {
			t.Fatalf("bytesOutsidePatchesEqualContext() = %t, %v", equal, err)
		}
	})

	t.Run("semantic YAML projection", func(t *testing.T) {
		var yaml strings.Builder
		yaml.WriteString("---\n")
		for index := 0; index < 128; index++ {
			fmt.Fprintf(&yaml, "key%d: value%d\n", index, index)
		}
		yaml.WriteString("---\nbody\n")
		presentation, err := parsePresentationContext(context.Background(), []byte(yaml.String()))
		if err != nil {
			t.Fatal(err)
		}
		ctx := &countdownContext{Context: context.Background(), remaining: 16}
		semantic, err := semanticYAMLContext(ctx, presentation.root)
		if !errors.Is(err, context.Canceled) || semantic != nil {
			t.Fatalf("semanticYAMLContext() = %#v, %v", semantic, err)
		}
	})

	t.Run("YAML resolver line table", func(t *testing.T) {
		ctx := &countdownContext{Context: context.Background(), remaining: 4}
		resolver, err := newYAMLResolverContext(ctx, large)
		if !errors.Is(err, context.Canceled) || resolver != nil {
			t.Fatalf("newYAMLResolverContext() = %#v, %v", resolver, err)
		}
	})

	t.Run("UTF-8 validation", func(t *testing.T) {
		ctx := &countdownContext{Context: context.Background(), remaining: 4}
		span, valid, err := validateUTF8Context(ctx, large)
		if !errors.Is(err, context.Canceled) || valid || span != (SourceSpan{}) {
			t.Fatalf("validateUTF8Context() = %#v, %t, %v", span, valid, err)
		}
	})

	t.Run("YAML stream scan", func(t *testing.T) {
		ctx := &countdownContext{Context: context.Background(), remaining: 4}
		span, found, err := leadingYAMLStreamTokenContext(ctx, bytesOf(' ', 256<<10))
		if !errors.Is(err, context.Canceled) || found || span != (SourceSpan{}) {
			t.Fatalf("leadingYAMLStreamTokenContext() = %#v, %t, %v", span, found, err)
		}
	})

	t.Run("YAML scalar token scan", func(t *testing.T) {
		source := append([]byte{'"'}, bytesOf('x', 256<<10)...)
		resolver, err := newYAMLResolverContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		resolver.ctx = &countdownContext{Context: context.Background(), remaining: 4}
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.Repeat("x", 256<<10), Line: 1, Column: 1, Style: yaml.DoubleQuotedStyle}
		span, err := resolver.scalar(node)
		if !errors.Is(err, context.Canceled) || span != (yamlScalarSpan{}) {
			t.Fatalf("scalar() = %#v, %v", span, err)
		}
	})

	t.Run("quoted YAML node traversal", func(t *testing.T) {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for index := 0; index < 128; index++ {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprintf("key%d", index)})
		}
		resolver, err := newYAMLResolverContext(context.Background(), []byte("key: value\n"))
		if err != nil {
			t.Fatal(err)
		}
		ctx := &countdownContext{Context: context.Background(), remaining: 16}
		spans, err := quotedYAMLScalarSpansContext(ctx, root, resolver)
		if !errors.Is(err, context.Canceled) || spans != nil {
			t.Fatalf("quotedYAMLScalarSpansContext() = %#v, %v", spans, err)
		}
	})

	t.Run("Markdown node window scan", func(t *testing.T) {
		source := append([]byte{'['}, bytesOf('x', 256<<10)...)
		source = append(source, []byte("](target.md)")...)
		ctx := &countdownContext{Context: context.Background(), remaining: 4}
		span, value, found, err := markdownInlineTokenContext(ctx, source, 0, len(source), []byte("target.md"), nil)
		if !errors.Is(err, context.Canceled) || found || span != (SourceSpan{}) || value != nil {
			t.Fatalf("markdownInlineTokenContext() = %#v, %q, %t, %v", span, value, found, err)
		}
	})

	t.Run("Markdown destination token scan", func(t *testing.T) {
		source := append([]byte{'<'}, bytesOf('x', 256<<10)...)
		ctx := &countdownContext{Context: context.Background(), remaining: 4}
		span, value, found, err := markdownDestinationTokenContext(ctx, source, 0, len(source))
		if !errors.Is(err, context.Canceled) || found || span != (SourceSpan{}) || value != nil {
			t.Fatalf("markdownDestinationTokenContext() = %#v, %q, %t, %v", span, value, found, err)
		}
	})

	t.Run("preview hashing and copy", func(t *testing.T) {
		overlay := NewOverlay(memorySource{})
		if err := overlay.Put("large.bin", large); err != nil {
			t.Fatal(err)
		}
		state := &planState{overlay: overlay, readPaths: newTrackedSource(memorySource{})}
		ctx := &countdownContext{Context: context.Background(), remaining: 4}
		preview, err := state.previewContext(ctx, "base", "result")
		if !errors.Is(err, context.Canceled) || len(preview.Writes) != 0 {
			t.Fatalf("previewContext() = %#v, %v", preview, err)
		}
	})
}

func bytesOf(value byte, count int) []byte {
	out := make([]byte, count)
	for index := range out {
		out[index] = value
	}
	return out
}

type cancelAfterReadSource struct {
	*plannerCountingSource
	cancel context.CancelFunc
	after  int
}

func (s *cancelAfterReadSource) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := s.plannerCountingSource.ReadFile(ctx, path)
	if s.readCalls == s.after {
		s.cancel()
	}
	return data, err
}

func newPlannerCountingSource(t *testing.T, files memorySource) *plannerCountingSource {
	t.Helper()
	entries := make([]store.ManifestEntry, 0, len(files))
	for path, content := range files {
		entries = append(entries, store.ManifestEntry{Path: path, Content: content})
	}
	manifest, err := store.NewManifest(entries)
	if err != nil {
		t.Fatal(err)
	}
	return &plannerCountingSource{files: files, manifest: manifest}
}

func (s *plannerCountingSource) Paths(context.Context) ([]string, error) {
	paths := make([]string, 0, len(s.files))
	for path := range s.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *plannerCountingSource) ReadFile(_ context.Context, path string) ([]byte, error) {
	s.readCalls++
	content, ok := s.files[path]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), content...), nil
}

func (s *plannerCountingSource) Manifest() store.Manifest { return s.manifest.Clone() }
func (s *plannerCountingSource) ManifestContext(ctx context.Context) (store.Manifest, error) {
	return s.manifest.CloneContext(ctx)
}
func (s *plannerCountingSource) verifiedManifestContext(ctx context.Context) (store.Manifest, []string, bool, error) {
	manifest, err := s.ManifestContext(ctx)
	if err != nil {
		return store.Manifest{}, nil, false, err
	}
	paths, err := s.Paths(ctx)
	return manifest, paths, true, err
}

func TestPlanCancelsDuringManifestFastPathTraversal(t *testing.T) {
	// Arrange.
	files := make(memorySource, 257)
	files["a.md"] = []byte("---\ntype: thing\n---\nA\n")
	for i := 0; i < 256; i++ {
		files[fmt.Sprintf("asset%03d.bin", i)] = []byte("content")
	}
	source := newPlannerCountingSource(t, files)
	base, err := source.manifest.Revision()
	if err != nil {
		t.Fatal(err)
	}
	changeSet := store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           "cancel-manifest-traversal",
		Actor:        "tester",
		BaseRevision: base,
		Operations:   []store.Operation{store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}},
	}
	ctx := &countdownContext{Context: context.Background(), remaining: 16}

	// Act.
	result, err := Plan(ctx, source, changeSet)

	// Assert.
	if !errors.Is(err, context.Canceled) || result.Staged != nil || source.readCalls != 0 {
		t.Fatalf("Plan() = %#v, %v; reads=%d", result, err, source.readCalls)
	}
}

func TestPlanValidatesFinalLoadedBundleWithoutExtraSourceRead(t *testing.T) {
	// Arrange. The manifest makes revision calculation read-free. Plan still
	// captures each immutable base file once. Presentation preflight and bundle
	// loading share that snapshot; operation loads consume the overlay.
	source := newPlannerCountingSource(t, memorySource{
		"a.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: b\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
	})
	base, err := source.manifest.Revision()
	if err != nil {
		t.Fatal(err)
	}
	changeSet := store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           "final-loaded-bundle",
		Actor:        "tester",
		BaseRevision: base,
		Operations:   []store.Operation{store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}},
	}

	// Act.
	result, err := Plan(context.Background(), source, changeSet)

	// Assert. Each immutable base file is read once; preflight must not add a
	// second source pass.
	if err != nil || result.Staged == nil {
		t.Fatalf("Plan() result/error = %#v / %v", result, err)
	}
	if got, want := source.readCalls, 2; got != want {
		t.Fatalf("source ReadFile calls = %d, want %d (one captured base snapshot)", got, want)
	}
}

func TestPlanPresentationErrorsCarryFullFileContext(t *testing.T) {
	tests := []struct {
		name          string
		source        memorySource
		op            store.Operation
		wantKind      error
		wantCode      string
		wantPath      string
		wantOp        string
		wantStart     int
		wantEnd       int
		wantExactSpan bool
	}{
		{
			name: "ensure relation ambiguous YAML",
			source: memorySource{
				"a.md": []byte("---\ntype: thing\nrelations:\n  uses: []\nrelations:\n  uses: []\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			},
			op: store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}, wantKind: ErrAmbiguousPresentation, wantCode: "duplicate_relations", wantPath: "a.md", wantOp: "ensure_relation", wantStart: 16, wantEnd: 47, wantExactSpan: true,
		},
		{
			name: "move unsupported YAML on impacted file",
			source: memorySource{
				"a.md": []byte("---\ntype: thing\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\nrelations: {uses: [{target: a}]}\n---\nB\n"),
			},
			op: store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}, wantKind: ErrUnsupportedPresentation, wantCode: "relations_not_block_mapping", wantPath: "b.md", wantOp: "move_concept", wantStart: 27, wantEnd: 33, wantExactSpan: true,
		},
		{
			name: "rename unsupported YAML",
			source: memorySource{
				"a.md": []byte("---\ntype: thing\nparts: [{id: old}]\n---\nA\n"),
			},
			op: store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}, wantKind: ErrUnsupportedPresentation, wantCode: "canonical_fragment_flow_mapping", wantPath: "a.md", wantOp: "rename_fragment", wantStart: 24, wantEnd: 28, wantExactSpan: true,
		},
		{
			name: "unterminated frontmatter stays in full-file bounds",
			source: memorySource{
				"a.md": []byte("---\ntype: thing\n"),
			},
			op: store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}, wantKind: ErrUnsupportedPresentation, wantCode: "unterminated_frontmatter", wantPath: "a.md", wantOp: "rename_fragment", wantStart: 4, wantEnd: 16, wantExactSpan: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange / Act.
			result, err := Plan(context.Background(), test.source, change(t, test.source, test.op))

			// Assert.
			if !errors.Is(err, test.wantKind) {
				t.Fatalf("Plan() error = %v, want %v", err, test.wantKind)
			}
			var presentation *PresentationError
			if !errors.As(err, &presentation) {
				t.Fatalf("Plan() error = %v, want PresentationError", err)
			}
			if presentation.Code != test.wantCode || presentation.Format != "yaml" || presentation.Path != test.wantPath || presentation.Operation != test.wantOp {
				t.Fatalf("PresentationError = %#v", presentation)
			}
			data := readSourceFileForTest(t, test.source, test.wantPath)
			if presentation.Location.Start < 0 || presentation.Location.End < presentation.Location.Start || presentation.Location.End > len(data) {
				t.Fatalf("Location out of bounds: %#v len=%d", presentation.Location, len(data))
			}
			if test.wantExactSpan {
				if presentation.Location.Start != test.wantStart || presentation.Location.End != test.wantEnd {
					t.Fatalf("PresentationError.Location = %#v, want [%d,%d)", presentation.Location, test.wantStart, test.wantEnd)
				}
			} else if presentation.Location.Start != test.wantStart || presentation.Location.End != test.wantStart {
				t.Fatalf("PresentationError.Location = %#v, want full-file marker %d", presentation.Location, test.wantStart)
			}
			if result.Staged != nil {
				t.Fatalf("Plan() staged failed presentation: %#v", result)
			}
		})
	}
}

func TestPlanTreatsClosingFrontmatterDelimiterAsMarkdownBoundary(t *testing.T) {
	// Arrange. These bytes look like YAML stream syntax only if the parser
	// incorrectly scans past the recognized closing frontmatter delimiter.
	source := memorySource{
		"a.md": []byte("---\ntype: thing\n---\n[go](b.md)\n---\nTitle\n---\n%YAML 1.2\n"),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
	}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, store.MoveConcept{From: ref(t, "b").ID, To: ref(t, "nested/b").ID}))

	// Assert.
	if err != nil || result.Staged == nil {
		t.Fatalf("Plan() result/error = %#v / %v", result, err)
	}
	got, readErr := result.Staged.ReadFile(context.Background(), "a.md")
	if readErr != nil {
		t.Fatal(readErr)
	}
	want := "---\ntype: thing\n---\n[go](nested/b.md)\n---\nTitle\n---\n%YAML 1.2\n"
	if string(got) != want {
		t.Fatalf("rewritten document = %q, want %q", got, want)
	}
}

func TestPlanCancellationStopsBeforeLaterFiles(t *testing.T) {
	// Arrange. The manifest makes revision calculation read-free. The first
	// operation-time read cancels the context; no later Markdown file may be
	// parsed or read.
	files := memorySource{
		"a.md": []byte("---\ntype: thing\n---\n[a](b.md)\n"),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
		"c.md": []byte("---\ntype: thing\n---\n[c](b.md)\n"),
	}
	base := newPlannerCountingSource(t, files)
	ctx, cancel := context.WithCancel(context.Background())
	source := &cancelAfterReadSource{plannerCountingSource: base, cancel: cancel, after: 2}

	// Act.
	result, err := Plan(ctx, source, change(t, base, store.MoveConcept{From: ref(t, "b").ID, To: ref(t, "nested/b").ID}))

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Plan() error = %v, want context.Canceled", err)
	}
	if result.Staged != nil {
		t.Fatalf("Plan() staged = %#v, want nil", result.Staged)
	}
	if got, want := base.readCalls, 2; got != want {
		t.Fatalf("ReadFile calls = %d, want %d; later files were touched", got, want)
	}
}

func TestPlanConflictChangedRefsPropagatesCancellation(t *testing.T) {
	files := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
	}
	base := newPlannerCountingSource(t, files)
	ctx, cancel := context.WithCancel(context.Background())
	source := &cancelAfterReadSource{plannerCountingSource: base, cancel: cancel, after: 1}
	changeSet := store.ChangeSet{
		Version: store.ChangeSetFormatVersion,
		ID:      "conflict-cancellation", Actor: "tester",
		BaseRevision: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Operations:   []store.Operation{store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}},
	}

	result, err := Plan(ctx, source, changeSet)

	if !errors.Is(err, context.Canceled) || result.Staged != nil {
		t.Fatalf("result/error = %#v / %v, want cancellation without conflict result", result, err)
	}
	var conflict *store.Conflict
	if errors.As(err, &conflict) {
		t.Fatalf("cancellation was converted to conflict: %#v", conflict)
	}
}

func TestPlanCancellationBubblesWithoutPresentationError(t *testing.T) {
	tests := []struct {
		name string
		op   store.Operation
	}{
		{"rename fragment", store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}},
		{"ensure relation", store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange. Cancellation from the first source read forces bundle
			// collection to stop before a second file can reach any parser.
			base := newPlannerCountingSource(t, memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
				"c.md": []byte("---\ntype: thing\n---\nC\n"),
			})
			ctx, cancel := context.WithCancel(context.Background())
			source := &cancelAfterReadSource{plannerCountingSource: base, cancel: cancel, after: 1}

			// Act.
			result, err := Plan(ctx, source, change(t, base, test.op))

			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Plan() error = %v, want context.Canceled", err)
			}
			var presentation *PresentationError
			if errors.As(err, &presentation) {
				t.Fatalf("Plan() error = %#v, must not wrap cancellation", presentation)
			}
			if result.Staged != nil || base.readCalls != 1 {
				t.Fatalf("result/read calls = %#v / %d, want nil / 1", result.Staged, base.readCalls)
			}
		})
	}
}
