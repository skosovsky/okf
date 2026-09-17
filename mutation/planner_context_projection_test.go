package mutation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestReadBundleFileContextCancelsDuringHugeCapturedFileCopy(t *testing.T) {
	// Arrange.
	loaded := loadSemanticRefsBundle(t, memorySource{
		"large.md": []byte("---\ntype: thing\n---\n" + strings.Repeat("payload", 1<<20)),
	})
	injected := errors.New("cancel captured file copy")
	ctx := &semanticRefsErrorContext{Context: context.Background(), allowed: 4, err: injected}

	// Act.
	data, ok, err := readBundleFileContext(ctx, loaded, "large.md")

	// Assert.
	if data != nil || ok || err != injected {
		t.Fatalf("readBundleFileContext() = (%d bytes, %t, %v), want exact canceled zero result", len(data), ok, err)
	}
	if got, want := ctx.calls, 5; got != want {
		t.Fatalf("context checks = %d, want bounded %d", got, want)
	}
}

func TestPlannerRequestAndDiagnosticProjectionCancellationAllocationsAreBounded(t *testing.T) {
	// Arrange.
	makeTracked := func(count int) *trackedSource {
		paths := make(map[string]struct{}, count)
		for index := 0; index < count; index++ {
			paths[fmt.Sprintf("nested/%08d.md", index)] = struct{}{}
		}
		return &trackedSource{paths: paths}
	}
	makeRelations := func(count int) []bundle.RelationDiagnostic {
		out := make([]bundle.RelationDiagnostic, count)
		for index := range out {
			out[index] = bundle.RelationDiagnostic{
				Code: "diagnostic",
				File: fmt.Sprintf("nested/%08d.md", index),
			}
		}
		return out
	}
	tests := []struct {
		name    string
		measure func(int) float64
	}{
		{
			name: "request reads",
			measure: func(count int) float64 {
				tracked := makeTracked(count)
				return testing.AllocsPerRun(20, func() {
					ctx := &semanticRefsErrorContext{Context: context.Background(), allowed: 32, err: context.Canceled}
					reads, err := tracked.readsContext(ctx)
					if reads != nil || !errors.Is(err, context.Canceled) || ctx.calls != 33 {
						panic(fmt.Sprintf("reads = (%#v, %v), checks=%d", reads, err, ctx.calls))
					}
				})
			},
		},
		{
			name: "diagnostics",
			measure: func(count int) float64 {
				relations := makeRelations(count)
				return testing.AllocsPerRun(20, func() {
					ctx := &semanticRefsErrorContext{Context: context.Background(), allowed: 32, err: context.Canceled}
					diagnostics, err := previewDiagnosticsContext(ctx, validator.Report{}, relations)
					if diagnostics != nil || !errors.Is(err, context.Canceled) || ctx.calls != 33 {
						panic(fmt.Sprintf("diagnostics = (%#v, %v), checks=%d", diagnostics, err, ctx.calls))
					}
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			smallAllocs := test.measure(64)
			largeAllocs := test.measure(32 << 10)

			// Assert.
			if largeAllocs > smallAllocs+2 {
				t.Fatalf("canceled allocations grow with cardinality: small %.0f, large %.0f", smallAllocs, largeAllocs)
			}
		})
	}
}

func TestMoveConceptFailsClosedForStreamTerminatedPathFamilies(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
	}{
		{name: "source resource", frontmatter: "sources:\n  - id: source-a\n    resource: b.md\n"},
		{name: "computation", frontmatter: "computation: b.md\n"},
		{name: "executor resource", frontmatter: "executor:\n  resource: b.md\n"},
		{name: "attester resource", frontmatter: "attester:\n  resource: b.md\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange. Bundle semantic parsing accepts the YAML document-end
			// marker, while the lossless mutation presentation rejects it.
			source := memorySource{
				"a.md": []byte("---\ntype: thing\n" + test.frontmatter + "...\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			}
			operation := store.MoveConcept{From: ref(t, "b").ID, To: ref(t, "nested/b").ID}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))

			// Assert.
			if !errors.Is(err, ErrUnsupportedPresentation) {
				t.Fatalf("Plan() error = %v, want ErrUnsupportedPresentation", err)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
				t.Fatalf("failed move exposed staged state: %#v", result)
			}
		})
	}
}

func TestMoveConceptFailsClosedForStreamTerminatedSelfAndOutgoingDocument(t *testing.T) {
	// Arrange. The moved document has an outgoing semantic relation and a local
	// computation path that does not target itself. It is still always impacted
	// because moving the document changes relative path resolution.
	source := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a\ncomputation: helper.sql\n...\n---\nB\n"),
	}
	operation := store.MoveConcept{From: ref(t, "b").ID, To: ref(t, "nested/b").ID}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))

	// Assert.
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("Plan() error = %v, want ErrUnsupportedPresentation", err)
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
		t.Fatalf("failed move exposed staged state: %#v", result)
	}
}

func TestMoveDocumentPathImpactPropagatesObservationCancellation(t *testing.T) {
	// Arrange.
	var document strings.Builder
	document.WriteString("---\ntype: thing\nsources:\n")
	for index := 0; index < 4096; index++ {
		fmt.Fprintf(&document, "  - id: source-%04d\n    resource: asset-%04d.sql\n", index, index)
	}
	document.WriteString("---\nA\n")
	loaded := loadSemanticRefsBundle(t, memorySource{
		"a.md": []byte(document.String()),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
	})
	injected := errors.New("cancel retained path observations")
	ctx := &semanticRefsErrorContext{Context: context.Background(), allowed: 40, err: injected}

	// Act.
	impacted, err := moveDocumentPathImpactContext(ctx, loaded, "a.md", "b.md")

	// Assert.
	if impacted || err != injected {
		t.Fatalf("moveDocumentPathImpactContext() = (%t, %v), want exact cancellation", impacted, err)
	}
	if got, want := ctx.calls, 41; got != want {
		t.Fatalf("context checks = %d, want bounded %d", got, want)
	}
}
