package mutation

import (
	"context"
	"fmt"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

// BenchmarkPlannerRepresentativeTransaction measures the whole planner path
// over a realistic visible bundle. Each transaction exercises copy-on-write
// overlay cloning, YAML presentation changes, a staged rename, manifest delta
// revisions, Paths, and Goldmark-backed Markdown destination rewrites.
func BenchmarkPlannerRepresentativeTransaction(b *testing.B) {
	for _, concepts := range []int{100, 1000, 10_000} {
		b.Run(fmt.Sprintf("concepts=%d", concepts), func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(concepts+1), "files/op")
			b.ReportMetric(3, "ops/tx")
			b.StopTimer()

			for i := 0; i < b.N; i++ {
				source, changeSet := benchmarkPlannerFixture(b, concepts, i)
				b.StartTimer()

				result, err := Plan(context.Background(), source, changeSet)
				if err != nil {
					b.Fatal(err)
				}

				b.StopTimer()
				assertBenchmarkPlannerResult(b, result, concepts)
			}
		})
	}
}

func TestPlannerRepeatedPreviewAndValidationSemanticMatrix(t *testing.T) {
	for _, concepts := range []int{100, 1000, 10_000} {
		t.Run(fmt.Sprintf("concepts=%d", concepts), func(t *testing.T) {
			if raceEnabled && concepts == 10_000 {
				t.Skip("10,000-concept semantic performance case runs without -race; race covers the 100/1,000 correctness matrix")
			}
			for iteration := 0; iteration < 2; iteration++ {
				source, changeSet := benchmarkPlannerFixture(t, concepts, iteration)

				result, err := Plan(context.Background(), source, changeSet)
				if err != nil {
					t.Fatal(err)
				}
				if !result.Validation.IsConformant() {
					t.Fatalf("validation = %#v, want conformant staged result", result.Validation)
				}
				assertBenchmarkPlannerResult(t, result, concepts)
			}
		})
	}
}

func benchmarkPlannerFixture(t testing.TB, concepts, iteration int) (bundle.Source, store.ChangeSet) {
	t.Helper()
	files := make(memorySource, concepts+1)
	files["a.md"] = []byte("---\ntype: thing\nparts:\n  - id: old\nrelations:\n  uses:\n    - target: c\n---\n\n[A to C](c.md)\n")
	files["b.md"] = []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a#old\n    - target: c\n---\n\n[B to C](c.md)\n")
	files["c.md"] = []byte("---\ntype: thing\n---\n\n[C self](c.md)\n")
	for i := 3; i < concepts; i++ {
		name := fmt.Sprintf("concept-%04d.md", i)
		files[name] = []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: c\n---\n\n[C link](c.md)\n")
	}
	files["index.md"] = []byte("---\nokf_version: \"0.1\"\n---\n\n# Bundle\n\n- [C](c.md)\n")

	entries := make([]store.ManifestEntry, 0, len(files))
	for name, data := range files {
		entries = append(entries, store.ManifestEntry{Path: name, Content: data})
	}
	manifest, err := store.NewManifest(entries)
	if err != nil {
		t.Fatal(err)
	}
	source := manifestMemorySource{memorySource: files, manifest: manifest}
	baseRevision, err := manifest.Revision()
	if err != nil {
		t.Fatal(err)
	}
	a, err := bundle.ParseRelationRef("a")
	if err != nil {
		t.Fatal(err)
	}
	bRef, err := bundle.ParseRelationRef("b")
	if err != nil {
		t.Fatal(err)
	}
	c, err := bundle.ParseRelationRef("c")
	if err != nil {
		t.Fatal(err)
	}
	movedC, err := bundle.ParseRelationRef("moved/c")
	if err != nil {
		t.Fatal(err)
	}
	changeSet := store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           store.ChangeSetID(fmt.Sprintf("planner-benchmark-%d", iteration)),
		Actor:        "benchmark",
		BaseRevision: baseRevision,
		Operations: []store.Operation{
			store.EnsureRelation{Source: a, Type: "uses", Target: bRef},
			store.RenameFragment{Concept: a.ID, From: "old", To: "new"},
			store.MoveConcept{From: c.ID, To: movedC.ID},
		},
	}
	return source, changeSet
}

func assertBenchmarkPlannerResult(t testing.TB, result Result, concepts int) {
	t.Helper()
	if got, want := len(result.Preview.Plan), 3; got != want {
		t.Fatalf("operation plans = %d, want %d", got, want)
	}
	if got, want := len(result.Preview.Renames), 1; got != want || result.Preview.Renames[0] != (store.Rename{From: "c.md", To: "moved/c.md"}) {
		t.Fatalf("renames = %#v, want c.md -> moved/c.md", result.Preview.Renames)
	}
	paths, err := result.Staged.Paths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(paths), concepts+1; got != want {
		t.Fatalf("visible paths = %d, want %d", got, want)
	}
	if _, err := result.Staged.ReadFile(context.Background(), "c.md"); err == nil {
		t.Fatal("old concept path remains visible")
	}
	for _, name := range []string{"a.md", "b.md", "moved/c.md", "index.md"} {
		if _, err := result.Staged.ReadFile(context.Background(), name); err != nil {
			t.Fatalf("staged %s: %v", name, err)
		}
	}
	updatedA, _ := result.Staged.ReadFile(context.Background(), "a.md")
	updatedB, _ := result.Staged.ReadFile(context.Background(), "b.md")
	updatedIndex, _ := result.Staged.ReadFile(context.Background(), "index.md")
	if !contains(string(updatedA), "- target: b") || !contains(string(updatedB), "target: a#new") || !contains(string(updatedIndex), "(moved/c.md)") {
		t.Fatal("planner result did not retain the expected YAML and Markdown semantics")
	}
	stagedBundle, err := bundle.Load(context.Background(), result.Staged)
	if err != nil {
		t.Fatalf("load staged bundle: %v", err)
	}
	a, _ := bundle.ParseRelationRef("a")
	bRef, _ := bundle.ParseRelationRef("b")
	if !benchmarkHasSemanticTarget(stagedBundle.SemanticLinksFrom(a.ID), "b") || !benchmarkHasSemanticTarget(stagedBundle.SemanticLinksFrom(bRef.ID), "a#new") {
		t.Fatal("staged graph does not contain the ensured and renamed semantic links")
	}
	if _, err := result.Staged.(store.ManifestSource).Manifest().Revision(); err != nil {
		t.Fatalf("staged manifest delta revision: %v", err)
	}
}

func benchmarkHasSemanticTarget(links []bundle.Relation, want string) bool {
	for _, link := range links {
		if link.Target.String() == want {
			return true
		}
	}
	return false
}
