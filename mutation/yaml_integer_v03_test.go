package mutation

import (
	"bytes"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestOKF03SourcePatchQuotesLargeExponentStringInFlowMapping(t *testing.T) {
	// Arrange.
	const before = "---\ntype: Note\nsources: {id: ref, resource: urn:test:original, x-audit: keep}\n---\n\nBody.\n"
	source := memorySource{"note.md": []byte(before)}
	op, err := store.NewPutSource(ref(t, "note").ID, store.ProvenanceSource{
		ID: "ref", Resource: "urn:test:original", Title: "1e9999",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, err := Plan(t.Context(), source, change(t, source, op))
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	raw, err := result.Staged.ReadFile(t.Context(), "note.md")
	want := strings.Replace(before, "x-audit: keep}", `x-audit: keep, title: "1e9999"}`, 1)
	if err != nil || string(raw) != want {
		t.Fatalf("published source = %q, %v, want %q", raw, err, want)
	}
}

func TestOKF03PutSourceUsesCoreIntegerAndPreservesSpelling(t *testing.T) {
	tests := []struct {
		name, sources, raw string
		count              uint64
	}{
		{"block decimal", "sources:\n  - id: ref\n    resource: scope\n    usage_count: 011 # retain\n    x-tag: !other preserve\n", "011", 11},
		{"flow decimal", "sources:\n  - {id: ref, resource: 'scope',   usage_count: 079  , x-tag: !other preserve, x-revision: 007}\n", "079", 79},
		{"singleton flow decimal", "sources: {id: ref, resource: 'scope',   usage_count: 079  , x-tag: !other preserve}\n", "079", 79},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := "---\ntype: Note\n" + tt.sources + "usage_window: {from: 2026-11-01, to: 2026-11-30}\n---\n\nBody.\n"
			source := memorySource{
				"index.md": []byte("---\nokf_version: '0.3'\n---\n# Notes\n\n- [Note](note.md)\n"),
				"note.md":  []byte(before),
			}
			plan := func(count uint64) Result {
				t.Helper()
				op, err := store.NewPutSourceForProfile(ref(t, "note").ID, store.ProvenanceSource{
					ID: "ref", Resource: "scope", UsageCount: &count,
				}, bundle.TemporalProfileV03)
				if err != nil {
					t.Fatal(err)
				}
				result, err := Plan(t.Context(), source, store.ChangeSet{
					Version: store.ChangeSetFormatVersion, ID: "core-count", Actor: "test",
					BaseRevision: revisionFor(t, source), Operations: []store.Operation{op},
				})
				if err != nil {
					t.Fatal(err)
				}
				return result
			}

			if unchanged := plan(tt.count); len(unchanged.Preview.Writes) != 0 {
				t.Fatalf("equivalent integer staged a write: %+v", unchanged.Preview.Writes)
			}
			updated := plan(80)
			written, err := updated.Staged.ReadFile(t.Context(), "note.md")
			want := strings.Replace(before, "usage_count: "+tt.raw, "usage_count: 80", 1)
			if err != nil || !bytes.Equal(written, []byte(want)) {
				t.Fatalf("written = %q, %v, want %q", written, err, want)
			}
		})
	}
}

func TestOKF03PlansSourceCollectionMutationsWithSingletonAndAbsentValues(t *testing.T) {
	id := ref(t, "note").ID
	add, err := store.NewPutSourceForProfile(id, store.ProvenanceSource{ID: "other", Resource: "second scope"}, bundle.TemporalProfileV03)
	if err != nil {
		t.Fatal(err)
	}
	selector, err := store.SourceByID("ref")
	if err != nil {
		t.Fatal(err)
	}
	window, err := store.NewSetUsageWindowForProfile(id, &selector, &store.UsageWindow{From: "2026-11-01", To: "2026-11-30"}, bundle.TemporalProfileV03)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := store.NewRemoveSource(id, selector)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, sourceYAML string
		op               store.Operation
		wantIDs          string
	}{
		{"promote", "{id: ref, resource: scope, x-audit: keep}", add, "ref,other"},
		{"set window", "{id: ref, resource: scope, x-audit: keep}", window, "ref"},
		{"remove singleton", "{id: ref, resource: scope}", remove, ""},
		{"insert into null", "null", add, "other"},
		{"insert into empty string", "''", add, "other"},
		{"remove empty", "[]", remove, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := memorySource{
				"index.md": []byte("---\nokf_version: '0.3'\n---\n# Notes\n\n- [Note](note.md)\n"),
				"note.md":  []byte("---\ntype: Note\nsources: " + tt.sourceYAML + "\n---\n\nBody.\n"),
			}
			result, err := Plan(t.Context(), source, store.ChangeSet{
				Version: store.ChangeSetFormatVersion, ID: "source-shapes", Actor: "test",
				BaseRevision: revisionFor(t, source), Operations: []store.Operation{tt.op},
			})
			if err != nil {
				t.Fatal(err)
			}
			written, err := result.Staged.ReadFile(t.Context(), "note.md")
			if err != nil {
				t.Fatal(err)
			}
			doc, err := bundle.ParseDocumentForVersionContext(t.Context(), string(written), bundle.VersionResolution{Effective: bundle.OKFVersion03})
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, item := range doc.Sources() {
				ids = append(ids, item.ID)
			}
			if strings.Join(ids, ",") != tt.wantIDs || strings.Contains(tt.sourceYAML, "x-audit: keep") && !bytes.Contains(written, []byte("x-audit: keep")) {
				t.Fatalf("sources = %v, written = %q", ids, written)
			}
			if tt.name == "set window" && doc.Sources()[0].UsageWindow == nil {
				t.Fatalf("source window absent: %q", written)
			}
		})
	}
}
