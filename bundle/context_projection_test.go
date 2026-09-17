package bundle

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBundleConceptIDsContextProjectsDefensivePathOrder(t *testing.T) {
	t.Parallel()

	// Arrange.
	alpha := mustParseConceptID(t, "alpha")
	nested := mustParseConceptID(t, "group/nested")
	loaded := &Bundle{concepts: []Concept{{ID: alpha}, {ID: nested}}}

	// Act.
	got, err := loaded.ConceptIDsContext(context.Background())

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if want := []ConceptID{alpha, nested}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ConceptIDsContext() = %#v, want %#v", got, want)
	}
	concepts := loaded.Concepts()
	if got[0].String() != concepts[0].ID.String() {
		t.Fatalf("ConceptIDsContext() order differs from Concepts(): %#v versus %#v", got, concepts)
	}
	got[0].segments[0] = "mutated"
	again, err := loaded.ConceptIDsContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again[0].String() != "alpha" {
		t.Fatalf("ConceptIDsContext() retained caller mutation: %#v", again)
	}
}

func TestFilesAndConceptIDSegmentsContextAreOwnedAndCancellable(t *testing.T) {
	// Arrange.
	large := strings.Repeat("x", 2<<20)
	id, err := NewConceptID([]string{large, "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &cancelAfterErrChecksContext{allowed: 16}

	// Act.
	segments, err := id.SegmentsContext(ctx)

	// Assert.
	if segments != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("SegmentsContext()=(%v,%v), want nil/canceled", segments, err)
	}

	// Arrange.
	loaded, err := Load(context.Background(), projectionBundleSource{"a.md": []byte("---\ntype: Note\n---\nBody\n"), "asset.bin": []byte("x")})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	files, err := loaded.FilesContext(context.Background())

	// Assert.
	if err != nil || len(files) != 2 {
		t.Fatalf("FilesContext()=(%v,%v), want 2", files, err)
	}
	files[0] = "mutated"
	again, err := loaded.FilesContext(context.Background())
	if err != nil || again[0] == "mutated" {
		t.Fatalf("FilesContext retained mutation: %v %v", again, err)
	}

	// Arrange.
	largeFiles := &Bundle{allFiles: []string{large, "asset.bin"}}
	cancelFiles := &cancelAfterErrChecksContext{allowed: 16}

	// Act.
	files, err = largeFiles.FilesContext(cancelFiles)

	// Assert.
	if files != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("FilesContext cancellation=(%v,%v), want nil/canceled", files, err)
	}
}

func TestNarrowContextAPIsLockInnerAndFinalCancellation(t *testing.T) {
	t.Parallel()
	large := strings.Repeat("x", 2<<20)
	tests := []struct {
		name   string
		target string
		run    func(context.Context) error
	}{
		{name: "concept validation inner", target: "validateConceptSegmentContext", run: func(ctx context.Context) error {
			_, err := NewConceptIDContext(ctx, []string{large})
			return err
		}},
		{name: "concept owned copy", target: "stringFromStringContext", run: func(ctx context.Context) error {
			_, err := NewConceptIDContext(ctx, []string{large})
			return err
		}},
		{name: "concept final", target: "newConceptIDFinalContext", run: func(ctx context.Context) error {
			_, err := NewConceptIDContext(ctx, []string{"valid"})
			return err
		}},
		{name: "segments final", target: "conceptIDSegmentsFinalContext", run: func(ctx context.Context) error {
			id, err := NewConceptID([]string{"valid"})
			if err != nil {
				return err
			}
			_, err = id.SegmentsContext(ctx)
			return err
		}},
		{name: "files final", target: "bundleFilesFinalContext", run: func(ctx context.Context) error {
			_, err := (&Bundle{allFiles: []string{"asset.bin"}}).FilesContext(ctx)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &stackFrameCancelContext{target: tt.target, cancelAt: 1}
			if err := tt.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("%s error=%v, want context.Canceled", tt.name, err)
			}
			if got := ctx.matches.Load(); got != 1 {
				t.Fatalf("%s matches=%d, want 1", tt.name, got)
			}
		})
	}
}

func TestNarrowContextAPIsPreserveEntryCancellationPrecedence(t *testing.T) {
	t.Parallel()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name string
		run  func() (any, error)
	}{
		{name: "nil bundle files", run: func() (any, error) {
			files, err := (*Bundle)(nil).FilesContext(canceled)
			return files, err
		}},
		{name: "zero id segments", run: func() (any, error) {
			segments, err := (ConceptID{}).SegmentsContext(canceled)
			return segments, err
		}},
		{name: "invalid empty concept", run: func() (any, error) {
			id, err := NewConceptIDContext(canceled, nil)
			return id, err
		}},
		{name: "invalid segment concept", run: func() (any, error) {
			id, err := NewConceptIDContext(canceled, []string{".."})
			return id, err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			got, err := tt.run()

			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v, want context.Canceled before input semantics", err)
			}
			value := reflect.ValueOf(got)
			if value.IsValid() && !value.IsZero() {
				t.Fatalf("result=%#v, want exact zero", got)
			}
		})
	}
}

func TestSegmentsContextEntryCancellationIsExactCallsite(t *testing.T) {
	t.Parallel()
	// Arrange.
	id, err := NewConceptID([]string{"valid"})
	if err != nil {
		t.Fatal(err)
	}
	trace := &callsiteTraceContext{}

	// Act: record the complete non-cancel checkpoint trace first.
	wantSegments, err := id.SegmentsContext(trace)

	// Assert.
	if err != nil || !reflect.DeepEqual(wantSegments, []string{"valid"}) {
		t.Fatalf("baseline SegmentsContext()=(%#v,%v)", wantSegments, err)
	}
	wantTrace := []struct {
		function string
		line     int
	}{
		{"ConceptID.SegmentsContext", 81},
		{"stringFromStringContext", 378},
		{"stringFromStringContext", 384},
		{"stringFromStringContext", 392},
		{"conceptIDSegmentsFinalContext", 99},
	}
	if len(trace.calls) != len(wantTrace) {
		t.Fatalf("checkpoint trace=%#v, want exactly %d calls", trace.calls, len(wantTrace))
	}
	for index, want := range wantTrace {
		got := trace.calls[index]
		if !strings.Contains(got.function, want.function) || !strings.HasSuffix(got.file, "bundle/"+map[bool]string{true: "conceptid.go", false: "frontmatter.go"}[strings.Contains(want.function, "ConceptID") || strings.Contains(want.function, "conceptID")]) || got.line != want.line {
			t.Fatalf("checkpoint[%d]=%#v, want %s:%d", index, got, want.function, want.line)
		}
	}

	// Arrange: cancel only at the exact frozen entry PC. A deletion, insertion,
	// or relocation changes the baseline trace or never reaches this PC.
	cancelCtx := &exactPCCancelContext{target: trace.calls[0].pc}

	// Act.
	segments, err := id.SegmentsContext(cancelCtx)

	// Assert.
	if segments != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("SegmentsContext()=(%#v,%v), want nil/context.Canceled at exact entry", segments, err)
	}
	if got := cancelCtx.hits.Load(); got != 1 {
		t.Fatalf("exact entry PC hits=%d, want 1", got)
	}
}

type errCallsite struct {
	pc       uintptr
	function string
	file     string
	line     int
}

type callsiteTraceContext struct{ calls []errCallsite }

func (*callsiteTraceContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*callsiteTraceContext) Done() <-chan struct{}       { return nil }
func (*callsiteTraceContext) Value(any) any               { return nil }
func (c *callsiteTraceContext) Err() error {
	pc, file, line, ok := runtime.Caller(1)
	if !ok {
		return nil
	}
	function := runtime.FuncForPC(pc)
	name := ""
	if function != nil {
		name = function.Name()
	}
	c.calls = append(c.calls, errCallsite{pc: pc, function: name, file: file, line: line})
	return nil
}

type exactPCCancelContext struct {
	target uintptr
	hits   atomic.Int32
}

func (*exactPCCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*exactPCCancelContext) Done() <-chan struct{}       { return nil }
func (*exactPCCancelContext) Value(any) any               { return nil }
func (c *exactPCCancelContext) Err() error {
	if c.hits.Load() != 0 {
		return context.Canceled
	}
	pc, _, _, ok := runtime.Caller(1)
	if ok && pc == c.target {
		c.hits.Add(1)
		return context.Canceled
	}
	return nil
}

func TestBundleContextProjectionsHonorCancellationAndNilParity(t *testing.T) {
	t.Parallel()

	// Arrange.
	first := mustParseConceptID(t, "first")
	second := mustParseConceptID(t, "second")
	unknown := mustParseConceptID(t, "unknown")
	relation := Relation{
		Source: RelationRef{ID: first},
		Type:   "depends_on",
		Target: RelationRef{ID: second},
	}
	loaded := &Bundle{
		concepts: []Concept{{ID: first}, {ID: second}},
		semantic: map[string][]Relation{first.String(): {relation, relation}},
		subresources: map[string]map[string]fragmentState{
			first.String(): {
				"zeta":  {count: 1},
				"alpha": {count: 1},
			},
		},
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// Act and assert: pre-cancellation.
	if _, err := loaded.ConceptIDsContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ConceptIDsContext() error = %v, want context.Canceled", err)
	}
	if _, err := loaded.SubresourcesContext(canceled, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("SubresourcesContext() error = %v, want context.Canceled", err)
	}
	if _, err := loaded.SemanticLinksFromContext(canceled, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("SemanticLinksFromContext() error = %v, want context.Canceled", err)
	}

	// Act and assert: deterministic per-item cancellation.
	if got, err := loaded.ConceptIDsContext(&cancelAfterErrChecksContext{allowed: 2}); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ConceptIDsContext() = (%#v, %v), want (nil, context.Canceled)", got, err)
	}
	if got, err := loaded.SubresourcesContext(&cancelAfterErrChecksContext{allowed: 1}, first); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("SubresourcesContext() = (%#v, %v), want (nil, context.Canceled)", got, err)
	}
	if got, err := loaded.SemanticLinksFromContext(&cancelAfterErrChecksContext{allowed: 2}, first); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("SemanticLinksFromContext() = (%#v, %v), want (nil, context.Canceled)", got, err)
	}

	// Act and assert: nil/unknown parity with legacy getters.
	var nilBundle *Bundle
	if got, err := nilBundle.ConceptIDsContext(context.Background()); err != nil || got != nil {
		t.Fatalf("nil ConceptIDsContext() = (%#v, %v), want (nil, nil)", got, err)
	}
	if got, err := nilBundle.SubresourcesContext(context.Background(), first); err != nil || got != nil {
		t.Fatalf("nil SubresourcesContext() = (%#v, %v), want (nil, nil)", got, err)
	}
	if got, err := nilBundle.SemanticLinksFromContext(context.Background(), first); err != nil || got != nil {
		t.Fatalf("nil SemanticLinksFromContext() = (%#v, %v), want (nil, nil)", got, err)
	}
	if got, err := loaded.SubresourcesContext(context.Background(), unknown); err != nil || got != nil {
		t.Fatalf("unknown SubresourcesContext() = (%#v, %v), want (nil, nil)", got, err)
	}
	if got, err := loaded.SemanticLinksFromContext(context.Background(), unknown); err != nil || got != nil {
		t.Fatalf("unknown SemanticLinksFromContext() = (%#v, %v), want (nil, nil)", got, err)
	}
	if got, err := loaded.SubresourcesContext(context.Background(), first); err != nil || !reflect.DeepEqual(got, loaded.Subresources(first)) {
		t.Fatalf("SubresourcesContext() = (%#v, %v), legacy = %#v", got, err, loaded.Subresources(first))
	}
	if got, err := loaded.SemanticLinksFromContext(context.Background(), first); err != nil || !reflect.DeepEqual(got, loaded.SemanticLinksFrom(first)) {
		t.Fatalf("SemanticLinksFromContext() = (%#v, %v), legacy = %#v", got, err, loaded.SemanticLinksFrom(first))
	}
}

func TestBundleContextProjectionResultsAreIndependentUnderConcurrency(t *testing.T) {
	t.Parallel()

	// Arrange.
	first := mustParseConceptID(t, "first")
	second := mustParseConceptID(t, "second")
	loaded := &Bundle{
		concepts: []Concept{{ID: first}, {ID: second}},
		semantic: map[string][]Relation{first.String(): {{
			Source: RelationRef{ID: first},
			Type:   "depends_on",
			Target: RelationRef{ID: second},
		}}},
		subresources: map[string]map[string]fragmentState{
			first.String(): {"fragment": {count: 1}},
		},
	}

	// Act.
	var group sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			ids, err := loaded.ConceptIDsContext(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			ids[0].segments[0] = "mutated"
			fragments, err := loaded.SubresourcesContext(context.Background(), first)
			if err != nil {
				t.Error(err)
				return
			}
			fragments[0] = "mutated"
			relations, err := loaded.SemanticLinksFromContext(context.Background(), first)
			if err != nil {
				t.Error(err)
				return
			}
			relations[0].Type = "mutated"
		}()
	}
	group.Wait()

	// Assert.
	ids, _ := loaded.ConceptIDsContext(context.Background())
	fragments, _ := loaded.SubresourcesContext(context.Background(), first)
	relations, _ := loaded.SemanticLinksFromContext(context.Background(), first)
	if ids[0].String() != "first" || !reflect.DeepEqual(fragments, []string{"fragment"}) || relations[0].Type != "depends_on" {
		t.Fatalf("bundle retained caller mutation: ids=%#v fragments=%#v relations=%#v", ids, fragments, relations)
	}
}

func TestSubresourcesContextCancelsDuringHighCardinalitySort(t *testing.T) {
	t.Parallel()

	// Arrange.
	const fragmentCount = 4096
	id := mustParseConceptID(t, "target")
	states := make(map[string]fragmentState, fragmentCount)
	for index := fragmentCount - 1; index >= 0; index-- {
		states["fragment-"+zeroPaddedDecimal(index, 4)] = fragmentState{count: 1}
	}
	loaded := &Bundle{subresources: map[string]map[string]fragmentState{id.String(): states}}
	// Allow the initial check, every map item, and the sort pre-check. The next
	// check is the first heap-sort comparison.
	ctx := &cancelAfterErrChecksContext{allowed: fragmentCount + 2}

	// Act.
	canceled, err := loaded.SubresourcesContext(ctx, id)
	first, firstErr := loaded.SubresourcesContext(context.Background(), id)
	second, secondErr := loaded.SubresourcesContext(context.Background(), id)
	legacy := loaded.Subresources(id)

	// Assert.
	if canceled != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-sort SubresourcesContext() = (%#v, %v), want (nil, context.Canceled)", canceled, err)
	}
	if firstErr != nil || secondErr != nil || !reflect.DeepEqual(first, second) || !reflect.DeepEqual(first, legacy) {
		t.Fatalf("background deterministic parity failed: firstErr=%v secondErr=%v equal=%v legacyEqual=%v", firstErr, secondErr, reflect.DeepEqual(first, second), reflect.DeepEqual(first, legacy))
	}
	if len(first) != fragmentCount || first[0] != "fragment-0000" || first[len(first)-1] != "fragment-4095" {
		t.Fatalf("sorted fragment boundary = len %d, first %q, last %q", len(first), first[0], first[len(first)-1])
	}
}

func TestBundleFileObservationContextsAreCancellableAndDefensive(t *testing.T) {
	t.Parallel()

	// Arrange.
	large := make([]byte, 128<<10)
	for index := range large {
		large[index] = byte(index)
	}
	loaded := &Bundle{
		files:    []string{"a.md", "b.md"},
		contents: map[string][]byte{"a.md": large},
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// Act and assert: cancellation.
	if _, err := loaded.MarkdownFilesContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("MarkdownFilesContext() error = %v, want context.Canceled", err)
	}
	if got, err := loaded.MarkdownFilesContext(&cancelAfterErrChecksContext{allowed: 2}); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("MarkdownFilesContext() = (%#v, %v), want canceled nil result", got, err)
	}
	if _, _, err := loaded.ReadFileContext(canceled, "a.md"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadFileContext() error = %v, want context.Canceled", err)
	}
	if got, ok, err := loaded.ReadFileContext(&cancelAfterErrChecksContext{allowed: 2}, "a.md"); got != nil || ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadFileContext() = (%d bytes, %v, %v), want canceled zero result", len(got), ok, err)
	}

	// Act and assert: legacy parity and defensive ownership.
	files, filesErr := loaded.MarkdownFilesContext(context.Background())
	data, ok, readErr := loaded.ReadFileContext(context.Background(), "a.md")
	if filesErr != nil || readErr != nil || !ok || !reflect.DeepEqual(files, loaded.MarkdownFiles()) {
		t.Fatalf("context file parity = files %#v/%v data ok=%v err=%v", files, filesErr, ok, readErr)
	}
	files[0] = "mutated.md"
	data[0] ^= 0xff
	filesAgain, _ := loaded.MarkdownFilesContext(context.Background())
	dataAgain, _, _ := loaded.ReadFileContext(context.Background(), "a.md")
	if filesAgain[0] != "a.md" || dataAgain[0] != large[0] {
		t.Fatalf("context output mutated retained state: files=%#v firstByte=%d", filesAgain, dataAgain[0])
	}

	// Act and assert: nil/unknown parity.
	var nilBundle *Bundle
	if got, err := nilBundle.MarkdownFilesContext(context.Background()); got != nil || err != nil {
		t.Fatalf("nil MarkdownFilesContext() = (%#v, %v)", got, err)
	}
	if got, ok, err := nilBundle.ReadFileContext(context.Background(), "a.md"); got != nil || ok || err != nil {
		t.Fatalf("nil ReadFileContext() = (%#v, %v, %v)", got, ok, err)
	}
	if got, ok, err := loaded.ReadFileContext(context.Background(), "missing.md"); got != nil || ok || err != nil {
		t.Fatalf("unknown ReadFileContext() = (%#v, %v, %v)", got, ok, err)
	}
}
