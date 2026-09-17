package store

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
)

func TestPreviewClone_PreservesTopLevelSliceState(t *testing.T) {
	ref := testRef(t, "concept", "fragment")
	tests := []struct {
		name    string
		preview Preview
	}{
		{name: "nil"},
		{
			name: "empty",
			preview: Preview{
				Reads:         make([]Read, 0),
				Writes:        make([]Write, 0),
				Deletes:       make([]string, 0),
				Renames:       make([]Rename, 0),
				AffectedRefs:  make([]bundle.RelationRef, 0),
				ReverseImpact: make([]bundle.RelationRef, 0),
				Plan:          make([]OperationPlan, 0),
				Diagnostics:   make([]Diagnostic, 0),
			},
		},
		{
			name: "nonempty",
			preview: Preview{
				Reads:         []Read{{Path: "read.md"}},
				Writes:        []Write{{Path: "write.md", Content: []byte("content")}},
				Deletes:       []string{"delete.md"},
				Renames:       []Rename{{From: "old.md", To: "new.md"}},
				AffectedRefs:  []bundle.RelationRef{ref},
				ReverseImpact: []bundle.RelationRef{ref},
				Plan:          []OperationPlan{{AffectedRefs: []bundle.RelationRef{ref}, Details: []string{"detail"}}},
				Diagnostics:   []Diagnostic{{Code: "diagnostic", Refs: []bundle.RelationRef{ref}}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			beforeJSON, err := json.Marshal(test.preview)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			cloned := test.preview.Clone()
			afterJSON, err := json.Marshal(cloned)
			if err != nil {
				t.Fatal(err)
			}

			// Assert.
			assertSameSliceState(t, "Reads", test.preview.Reads, cloned.Reads)
			assertSameSliceState(t, "Writes", test.preview.Writes, cloned.Writes)
			assertSameSliceState(t, "Deletes", test.preview.Deletes, cloned.Deletes)
			assertSameSliceState(t, "Renames", test.preview.Renames, cloned.Renames)
			assertSameSliceState(t, "AffectedRefs", test.preview.AffectedRefs, cloned.AffectedRefs)
			assertSameSliceState(t, "ReverseImpact", test.preview.ReverseImpact, cloned.ReverseImpact)
			assertSameSliceState(t, "Plan", test.preview.Plan, cloned.Plan)
			assertSameSliceState(t, "Diagnostics", test.preview.Diagnostics, cloned.Diagnostics)
			if !bytes.Equal(beforeJSON, afterJSON) {
				t.Fatalf("clone JSON changed:\nbefore: %s\nafter:  %s", beforeJSON, afterJSON)
			}

			if test.name != "nonempty" {
				return
			}
			cloned.Reads[0].Path = "mutated-read.md"
			cloned.Writes[0].Path = "mutated-write.md"
			cloned.Deletes[0] = "mutated-delete.md"
			cloned.Renames[0].To = "mutated-new.md"
			cloned.AffectedRefs[0].Fragment = "mutated-affected"
			cloned.ReverseImpact[0].Fragment = "mutated-reverse"
			cloned.Plan[0].Details[0] = "mutated-detail"
			cloned.Diagnostics[0].Code = "mutated-diagnostic"
			if test.preview.Reads[0].Path != "read.md" ||
				test.preview.Writes[0].Path != "write.md" ||
				test.preview.Deletes[0] != "delete.md" ||
				test.preview.Renames[0].To != "new.md" ||
				test.preview.AffectedRefs[0].Fragment != "fragment" ||
				test.preview.ReverseImpact[0].Fragment != "fragment" ||
				test.preview.Plan[0].Details[0] != "detail" ||
				test.preview.Diagnostics[0].Code != "diagnostic" {
				t.Fatal("clone aliases a top-level caller-owned slice")
			}
		})
	}
}

func TestPreviewClone_PreservesNestedSliceStateAndOwnership(t *testing.T) {
	ref := testRef(t, "concept", "fragment")
	tests := []struct {
		name    string
		content []byte
		refs    []bundle.RelationRef
		details []string
	}{
		{name: "nil"},
		{
			name:    "empty",
			content: make([]byte, 0),
			refs:    make([]bundle.RelationRef, 0),
			details: make([]string, 0),
		},
		{
			name:    "nonempty",
			content: []byte("content"),
			refs:    []bundle.RelationRef{ref},
			details: []string{"detail"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			preview := Preview{
				Writes:      []Write{{Path: "asset.bin", Content: test.content}},
				Diagnostics: []Diagnostic{{Refs: test.refs}},
				Plan:        []OperationPlan{{AffectedRefs: test.refs, Details: test.details}},
			}

			// Act.
			cloned := preview.Clone()

			// Assert.
			assertSameSliceState(t, "Write.Content", preview.Writes[0].Content, cloned.Writes[0].Content)
			assertSameSliceState(t, "Diagnostic.Refs", preview.Diagnostics[0].Refs, cloned.Diagnostics[0].Refs)
			assertSameSliceState(t, "OperationPlan.AffectedRefs", preview.Plan[0].AffectedRefs, cloned.Plan[0].AffectedRefs)
			assertSameSliceState(t, "OperationPlan.Details", preview.Plan[0].Details, cloned.Plan[0].Details)
			if test.name != "nonempty" {
				return
			}
			cloned.Writes[0].Content[0] = 'X'
			cloned.Diagnostics[0].Refs[0].Fragment = "mutated-diagnostic"
			cloned.Plan[0].AffectedRefs[0].Fragment = "mutated-plan"
			cloned.Plan[0].Details[0] = "mutated-detail"
			if string(preview.Writes[0].Content) != "content" ||
				preview.Diagnostics[0].Refs[0].Fragment != "fragment" ||
				preview.Plan[0].AffectedRefs[0].Fragment != "fragment" ||
				preview.Plan[0].Details[0] != "detail" {
				t.Fatal("clone aliases nested caller-owned state")
			}
		})
	}
}

func TestCommitReceiptClone_PreservesSliceStateAndOwnership(t *testing.T) {
	ref := testRef(t, "concept", "fragment")
	tests := []struct {
		name  string
		refs  []bundle.RelationRef
		files []FileChange
	}{
		{name: "nil"},
		{name: "empty", refs: make([]bundle.RelationRef, 0), files: make([]FileChange, 0)},
		{
			name:  "nonempty",
			refs:  []bundle.RelationRef{ref},
			files: []FileChange{{Kind: FileWrite, Path: "asset.bin"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			receipt := CommitReceipt{ChangedRefs: test.refs, ChangedFiles: test.files}

			// Act.
			cloned := receipt.Clone()

			// Assert.
			assertSameSliceState(t, "ChangedRefs", receipt.ChangedRefs, cloned.ChangedRefs)
			assertSameSliceState(t, "ChangedFiles", receipt.ChangedFiles, cloned.ChangedFiles)
			if test.name != "nonempty" {
				return
			}
			cloned.ChangedRefs[0].Fragment = "mutated"
			cloned.ChangedFiles[0].Path = "mutated.bin"
			if receipt.ChangedRefs[0].Fragment != "fragment" || receipt.ChangedFiles[0].Path != "asset.bin" {
				t.Fatal("clone aliases receipt slices")
			}
		})
	}
}

func TestCommitReceiptClone_JSONReplayKeepsNonNullArrayPolicy(t *testing.T) {
	revision := testRevision(t)
	tests := []struct {
		name      string
		files     []FileChange
		wantFiles string
	}{
		{name: "no-op", files: make([]FileChange, 0), wantFiles: `[]`},
		{
			name:      "asset-only",
			files:     []FileChange{{Kind: FileWrite, Path: "references/computations/revenue.sql"}},
			wantFiles: `[{"Kind":"write","Path":"references/computations/revenue.sql","From":""}]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			receipt := CommitReceipt{
				FormatVersion:  CommitReceiptFormatVersion,
				ChangeSetID:    "change",
				IdempotencyKey: "retry",
				RequestDigest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				BaseRevision:   revision,
				ResultRevision: revision,
				CommitTime:     time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
				ChangedRefs:    make([]bundle.RelationRef, 0),
				ChangedFiles:   test.files,
			}
			want := `{"FormatVersion":1,"ChangeSetID":"change","IdempotencyKey":"retry","RequestDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","BaseRevision":"` +
				revision.String() + `","ResultRevision":"` + revision.String() +
				`","CommitTime":"2026-07-29T00:00:00Z","ChangedRefs":[],"ChangedFiles":` + test.wantFiles + `}`

			// Act.
			cloned := receipt.Clone()
			raw, err := json.Marshal(cloned)
			if err != nil {
				t.Fatal(err)
			}
			var replayed CommitReceipt
			if err := json.Unmarshal(raw, &replayed); err != nil {
				t.Fatal(err)
			}
			replayedRaw, err := json.Marshal(replayed)
			if err != nil {
				t.Fatal(err)
			}

			// Assert.
			if string(raw) != want || !bytes.Equal(replayedRaw, raw) {
				t.Fatalf("receipt wire is not exact and replay-stable:\nwant:     %s\nclone:    %s\nreplayed: %s", want, raw, replayedRaw)
			}
			if cloned.ChangedRefs == nil || replayed.ChangedRefs == nil {
				t.Fatalf("ChangedRefs lost non-nil empty policy: clone=%#v replay=%#v", cloned.ChangedRefs, replayed.ChangedRefs)
			}
			if cloned.ChangedFiles == nil || replayed.ChangedFiles == nil {
				t.Fatalf("ChangedFiles lost non-nil policy: clone=%#v replay=%#v", cloned.ChangedFiles, replayed.ChangedFiles)
			}
		})
	}
}

func TestManifestClone_PreservesMapStateAndOwnership(t *testing.T) {
	empty, err := NewManifest(nil)
	if err != nil {
		t.Fatal(err)
	}
	nonempty, err := NewManifest([]ManifestEntry{{Path: "asset.bin", Content: []byte("content")}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		manifest Manifest
	}{
		{name: "nil"},
		{name: "empty", manifest: empty},
		{name: "nonempty", manifest: nonempty},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			wantDigests := test.manifest.digests

			// Act.
			cloned := test.manifest.Clone()
			gotDigests := cloned.digests
			exposedDigests := cloned.Digests()

			// Assert.
			if (wantDigests == nil) != (gotDigests == nil) || !reflect.DeepEqual(wantDigests, gotDigests) {
				t.Fatalf("digest map state/value = %#v, want %#v", gotDigests, wantDigests)
			}
			if (gotDigests == nil) != (exposedDigests == nil) || !reflect.DeepEqual(gotDigests, exposedDigests) {
				t.Fatalf("exposed digest map state/value = %#v, want %#v", exposedDigests, gotDigests)
			}
			if test.name != "nonempty" {
				return
			}
			exposedDigests["asset.bin"] = "mutated-accessor"
			if got, _ := cloned.Digest("asset.bin"); got == "mutated-accessor" {
				t.Fatal("Digests aliases manifest digest map")
			}
			cloned.digests["asset.bin"] = "mutated"
			if got, _ := test.manifest.Digest("asset.bin"); got == "mutated" {
				t.Fatal("clone aliases manifest digest map")
			}
		})
	}
}

func assertSameSliceState[S ~[]E, E any](t *testing.T, field string, want, got S) {
	t.Helper()
	if (want == nil) != (got == nil) || !reflect.DeepEqual(want, got) {
		t.Fatalf("%s state/value = %#v, want %#v", field, got, want)
	}
}
