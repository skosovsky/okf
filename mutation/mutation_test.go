package mutation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

type memorySource map[string][]byte

type noReadSource struct{}

func (noReadSource) Paths(context.Context) ([]string, error) {
	return nil, errors.New("source must not be read")
}

func (noReadSource) ReadFile(context.Context, string) ([]byte, error) {
	return nil, errors.New("source must not be read")
}

type countedAlgorithm struct{ calls int }

func (a *countedAlgorithm) Name() string { return "counted-sha256" }
func (a *countedAlgorithm) New() hash.Hash {
	a.calls++
	return sha256.New()
}

type cancellingContextHashAlgorithm struct {
	calls  int
	cancel context.CancelFunc
}

func (*cancellingContextHashAlgorithm) Name() string   { return "cancel-sha256" }
func (*cancellingContextHashAlgorithm) New() hash.Hash { return sha256.New() }
func (a *cancellingContextHashAlgorithm) NewContext(context.Context) (hash.Hash, error) {
	a.calls++
	a.cancel()
	return sha256.New(), nil
}

type manifestMemorySource struct {
	memorySource
	manifest store.Manifest
}

type unverifiedManifestSource struct {
	memorySource
	manifest store.Manifest
}

func (s unverifiedManifestSource) Manifest() store.Manifest { return s.manifest.Clone() }

func (s manifestMemorySource) Manifest() store.Manifest { return s.manifest.Clone() }
func (s manifestMemorySource) verifiedManifestContext(ctx context.Context) (store.Manifest, []string, bool, error) {
	manifest, err := s.manifest.CloneContext(ctx)
	if err != nil {
		return store.Manifest{}, nil, false, err
	}
	paths, err := s.Paths(ctx)
	return manifest, paths, true, err
}

func (m memorySource) Paths(context.Context) ([]string, error) {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	return out, nil
}
func (m memorySource) ReadFile(_ context.Context, p string) ([]byte, error) {
	v, ok := m[p]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), v...), nil
}
func ref(t *testing.T, raw string) bundle.RelationRef {
	t.Helper()
	r, err := bundle.ParseRelationRef(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func revisionFor(t *testing.T, s bundle.Source) store.Revision {
	t.Helper()
	r, err := revision(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func change(t *testing.T, s bundle.Source, op store.Operation) store.ChangeSet {
	return store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "change-1", Actor: "tester", BaseRevision: revisionFor(t, s), Operations: []store.Operation{op}}
}

func TestPlanStaleRevisionReportsCompleteAvailableSemanticRefs(t *testing.T) {
	// Arrange. The expected snapshot is unavailable, but the current source
	// still exposes content, a fragment, and a semantic relation.
	source := memorySource{
		"a.md": []byte("---\ntype: Note\nparts:\n  - id: part\nrelations:\n  uses:\n    - target: b\n---\nA\n"),
		"b.md": []byte("---\ntype: Note\n---\nB\n"),
	}
	change := store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           "stale-semantic-refs",
		Actor:        "tester",
		BaseRevision: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Operations:   []store.Operation{store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}},
	}

	// Act.
	_, err := NewPlanner(nil).Plan(context.Background(), source, change)

	// Assert.
	var conflict *store.Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("Plan() error = %v, want conflict", err)
	}
	got := make([]string, len(conflict.ChangedRefs))
	for i, value := range conflict.ChangedRefs {
		got[i] = value.String()
	}
	if want := []string{"a", "a#part", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedRefs = %v, want %v", got, want)
	}
}

func TestOverlay_RenameTombstoneAndCopies(t *testing.T) {
	// Arrange
	base := memorySource{"a.md": []byte("old")}
	o := NewOverlay(base)
	// Act
	if err := o.Rename(context.Background(), "a.md", "b.md"); err != nil {
		t.Fatal(err)
	}
	data, err := o.ReadFile(context.Background(), "b.md")
	data[0] = 'X'
	// Assert
	if err != nil || string(data) != "Xld" {
		t.Fatalf("got %q, %v", data, err)
	}
	if _, err := o.ReadFile(context.Background(), "a.md"); err == nil {
		t.Fatal("old path fell back to base")
	}
	fresh := readSourceFileForTest(t, o, "b.md")
	if string(fresh) != "old" {
		t.Fatalf("overlay leaked caller mutation: %q", fresh)
	}
}

func TestYAMLScalarToken_RoundTripsExactValue(t *testing.T) {
	// Arrange.
	tests := []struct {
		name      string
		value     string
		preferred yaml.Style
	}{
		{name: "colon space", value: "fragment: one"},
		{name: "single quote", value: "it's exact", preferred: yaml.SingleQuotedStyle},
		{name: "double quote", value: `say "hello"`, preferred: yaml.DoubleQuotedStyle},
		{name: "backslash", value: `path\\name`},
		{name: "hash", value: "part # one"},
		{name: "colon", value: "part:one"},
		{name: "unicode", value: "раздел/東京"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			token, tokenErr := yamlScalarTokenContext(context.Background(), tt.value, tt.preferred)
			var got map[string]string
			err := yaml.Unmarshal([]byte("value: "+token+"\n"), &got)

			// Assert.
			if tokenErr != nil || err != nil || got["value"] != tt.value {
				t.Fatalf("token %q parsed as %#v, errors %v/%v; want %q", token, got, tokenErr, err, tt.value)
			}
		})
	}
}

func TestPlanMoveConceptToReservedLoaderFile_StopsBeforeStaging(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\n---\n\nA\n")}
	changeSet := change(t, source, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/index").ID})

	// Act.
	result, err := Plan(context.Background(), source, changeSet)

	// Assert.
	if !errors.Is(err, store.ErrInvalidChangeSet) {
		t.Fatalf("Plan() error = %v, want ErrInvalidChangeSet", err)
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
		t.Fatalf("Plan() staged invalid move: %#v", result.Preview)
	}
}

func TestPlannerPlanCancelledBeforeStagedValidationDoesNotReturnStage(t *testing.T) {
	// Arrange.
	source := memorySource{
		"a.md": []byte("---\ntype: Note\n---\n\nA\n"),
		"b.md": []byte("---\ntype: Note\n---\n\nB\n"),
	}
	changeSet := change(t, source, store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	result, err := Plan(ctx, source, changeSet)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Plan() error = %v, want context.Canceled", err)
	}
	if result.Staged != nil {
		t.Fatal("Plan() returned staged source after cancellation")
	}
}

func TestOverlay_ManifestReusesUnchangedDigest(t *testing.T) {
	// Arrange.
	algorithm := &countedAlgorithm{}
	base := memorySource{"a.md": []byte("old"), "b.md": []byte("unchanged")}
	manifest, err := store.NewManifestWithAlgorithm([]store.ManifestEntry{{Path: "a.md", Content: base["a.md"]}, {Path: "b.md", Content: base["b.md"]}}, algorithm)
	if err != nil {
		t.Fatal(err)
	}
	o := NewOverlay(manifestMemorySource{memorySource: base, manifest: manifest})
	before := algorithm.calls

	// Act.
	if err := o.Put("a.md", []byte("new")); err != nil {
		t.Fatal(err)
	}
	_, err = o.Manifest().Revision()
	if err != nil {
		t.Fatal(err)
	}

	// Assert. One content hash and one manifest hash; b.md was not read or
	// rehashed while computing the staged revision.
	if got := algorithm.calls - before; got != 2 {
		t.Fatalf("hash constructions = %d, want changed content plus manifest", got)
	}
}

func TestPlannerDoesNotTrustArbitraryManifestSource(t *testing.T) {
	// Arrange. ManifestSource is an optimization hint, not proof that its path
	// set or digests describe the immutable Source snapshot.
	files := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\n---\nB\n"),
	}
	actual := revisionFor(t, files)
	tests := []struct {
		name    string
		entries []store.ManifestEntry
	}{
		{name: "omitted path", entries: []store.ManifestEntry{{Path: "a.md", Content: files["a.md"]}}},
		{name: "extra path", entries: []store.ManifestEntry{{Path: "a.md", Content: files["a.md"]}, {Path: "b.md", Content: files["b.md"]}, {Path: "ghost.md", Content: []byte("ghost")}}},
		{name: "wrong digest", entries: []store.ManifestEntry{{Path: "a.md", Content: []byte("forged")}, {Path: "b.md", Content: files["b.md"]}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest, err := store.NewManifest(tt.entries)
			if err != nil {
				t.Fatal(err)
			}
			source := unverifiedManifestSource{memorySource: files, manifest: manifest}

			// Act.
			overlay, overlayErr := NewOverlayContext(context.Background(), source)
			gotRevision, revisionErr := revision(context.Background(), source)
			result, planErr := Plan(context.Background(), source, store.ChangeSet{
				Version: store.ChangeSetFormatVersion, ID: "unverified-manifest", Actor: "tester", BaseRevision: actual,
				Operations: []store.Operation{store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}},
			})

			// Assert. Both revision and CAS use actual Source bytes. The overlay
			// may stage normally, but cannot carry the forged manifest fast path.
			if overlayErr != nil || overlay.hasManifest || revisionErr != nil || gotRevision != actual || planErr != nil || result.Staged == nil || result.Preview.BaseRevision != actual {
				t.Fatalf("overlay=%#v revision=%q/%v plan=%#v/%v", overlay, gotRevision, revisionErr, result, planErr)
			}
		})
	}
}

func TestOverlay_RenameMovesCachedDigestWithoutContentHash(t *testing.T) {
	// Arrange.
	algorithm := &countedAlgorithm{}
	base := memorySource{"a.md": []byte("old"), "b.md": []byte("unchanged")}
	manifest, err := store.NewManifestWithAlgorithm([]store.ManifestEntry{{Path: "a.md", Content: base["a.md"]}, {Path: "b.md", Content: base["b.md"]}}, algorithm)
	if err != nil {
		t.Fatal(err)
	}
	o := NewOverlay(manifestMemorySource{memorySource: base, manifest: manifest})
	before := algorithm.calls

	// Act.
	if err := o.Rename(context.Background(), "a.md", "nested/a.md"); err != nil {
		t.Fatal(err)
	}
	_, err = o.Manifest().Revision()
	if err != nil {
		t.Fatal(err)
	}

	// Assert. Rename does no content hashing; only Revision constructs its
	// single manifest hash.
	if got := algorithm.calls - before; got != 1 {
		t.Fatalf("hash constructions = %d, want manifest only", got)
	}
}

func TestOverlayPutContextCancelsCustomHashWithoutStagingPayload(t *testing.T) {
	// Arrange.
	ctx, cancel := context.WithCancel(context.Background())
	algorithm := &cancellingContextHashAlgorithm{cancel: cancel}
	manifest, err := store.NewManifestWithAlgorithm([]store.ManifestEntry{{Path: "a.md", Content: []byte("old")}}, algorithm)
	if err != nil {
		t.Fatal(err)
	}
	o := NewOverlay(manifestMemorySource{memorySource: memorySource{"a.md": []byte("old")}, manifest: manifest})

	// Act.
	err = o.PutContext(ctx, "a.md", []byte("new"))

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PutContext() error = %v, want context.Canceled", err)
	}
	if algorithm.calls != 1 {
		t.Fatalf("context hash calls = %d, want 1", algorithm.calls)
	}
	if _, staged := o.changed["a.md"]; staged {
		t.Fatal("PutContext() staged payload after cancelled hash")
	}
}

func TestOverlay_RejectsReservedMetadataPathsWithoutChangingState(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*Overlay) error
	}{
		{name: "read metadata root", apply: func(o *Overlay) error { _, err := o.ReadFile(context.Background(), ".okf"); return err }},
		{name: "read metadata child", apply: func(o *Overlay) error { _, err := o.ReadFile(context.Background(), ".okf/x"); return err }},
		{name: "put normalized metadata child", apply: func(o *Overlay) error { return o.Put("x/../.okf/y", []byte("new")) }},
		{name: "create mixed separators", apply: func(o *Overlay) error { return o.Create(context.Background(), `x\..\.okf\y`, []byte("new")) }},
		{name: "delete metadata root", apply: func(o *Overlay) error { return o.Delete(".okf") }},
		{name: "rename from metadata", apply: func(o *Overlay) error { return o.Rename(context.Background(), ".okf/x", "renamed.md") }},
		{name: "rename to metadata", apply: func(o *Overlay) error { return o.Rename(context.Background(), "note.md", ".okf/x") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			base := memorySource{"note.md": []byte("old"), ".okf-name.md": []byte("ordinary")}
			manifest, err := store.NewManifest([]store.ManifestEntry{{Path: "note.md", Content: base["note.md"]}, {Path: ".okf-name.md", Content: base[".okf-name.md"]}})
			if err != nil {
				t.Fatal(err)
			}
			o := NewOverlay(manifestMemorySource{memorySource: base, manifest: manifest})
			before, err := o.Manifest().Revision()
			if err != nil {
				t.Fatal(err)
			}
			baseBefore := cloneMemorySource(base)

			// Act.
			err = tt.apply(o)

			// Assert.
			if !errors.Is(err, store.ErrInvalidChangeSet) {
				t.Fatalf("operation error = %v, want ErrInvalidChangeSet", err)
			}
			after, err := o.Manifest().Revision()
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("manifest revision = %q, want unchanged %q", after, before)
			}
			if !equalMemorySource(base, baseBefore) {
				t.Fatalf("base changed: got %#v, want %#v", base, baseBefore)
			}
		})
	}
}

func TestOverlay_RejectsReservedMetadataFromPathsAndManifest(t *testing.T) {
	// Arrange.
	base := memorySource{"note.md": []byte("note"), ".okf/revision": []byte("private")}
	_, manifestErr := store.NewManifest([]store.ManifestEntry{{Path: "note.md", Content: base["note.md"]}, {Path: ".okf/revision", Content: base[".okf/revision"]}})
	o := NewOverlay(base)
	baseBefore := cloneMemorySource(base)

	// Act.
	_, pathsErr := o.Paths(context.Background())
	_, stagedRevisionErr := revision(context.Background(), o)

	// Assert.
	if !errors.Is(pathsErr, store.ErrInvalidChangeSet) {
		t.Fatalf("Paths() error = %v, want ErrInvalidChangeSet", pathsErr)
	}
	if !errors.Is(manifestErr, store.ErrInvalidManifest) {
		t.Fatalf("NewManifest() error = %v, want ErrInvalidManifest", manifestErr)
	}
	if !errors.Is(stagedRevisionErr, store.ErrInvalidChangeSet) {
		t.Fatalf("revision() error = %v, want ErrInvalidChangeSet", stagedRevisionErr)
	}
	if !equalMemorySource(base, baseBefore) {
		t.Fatalf("base changed: got %#v, want %#v", base, baseBefore)
	}

	// Only the root metadata directory is reserved.
	clean := NewOverlay(memorySource{
		".okf-name.md":         []byte("ordinary"),
		"nested/.okf/file.bin": []byte("nested ordinary"),
	})
	paths, err := clean.Paths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(paths, ","), ".okf-name.md,nested/.okf/file.bin"; got != want {
		t.Fatalf("Paths() = %q, want %q", got, want)
	}
	wantManifest, err := store.NewManifest([]store.ManifestEntry{
		{Path: ".okf-name.md", Content: []byte("ordinary")},
		{Path: "nested/.okf/file.bin", Content: []byte("nested ordinary")},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRevision, err := wantManifest.Revision()
	if err != nil {
		t.Fatal(err)
	}
	if got := revisionFor(t, clean); got != wantRevision {
		t.Fatalf("overlay revision = %q, want manifest revision %q", got, wantRevision)
	}
}

func cloneMemorySource(in memorySource) memorySource {
	out := make(memorySource, len(in))
	for name, content := range in {
		out[name] = append([]byte(nil), content...)
	}
	return out
}

func equalMemorySource(a, b memorySource) bool {
	if len(a) != len(b) {
		return false
	}
	for name, content := range a {
		other, ok := b[name]
		if !ok || string(content) != string(other) {
			return false
		}
	}
	return true
}

func TestPlanEnsureRelation_IsIdempotentAndPreservesUnknownField(t *testing.T) {
	// Arrange
	s := memorySource{"a.md": []byte("---\ntype: thing\nunknown: [keep]\n---\n\nA\n"), "b.md": []byte("---\ntype: thing\n---\n\nB\n")}
	op := store.EnsureRelation{Source: ref(t, "a"), Type: "depends_on", Target: ref(t, "b")}
	// Act
	first, err := Plan(context.Background(), s, change(t, s, op))
	if err != nil {
		t.Fatal(err)
	}
	secondChange := change(t, first.Staged, op)
	second, err := Plan(context.Background(), first.Staged, secondChange)
	// Assert
	if err != nil {
		t.Fatal(err)
	}
	data, err := second.Staged.ReadFile(context.Background(), "a.md")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundle.Load(context.Background(), second.Staged)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(b.SemanticLinksFrom(ref(t, "a").ID)); got != 1 {
		t.Fatalf("relations = %d", got)
	}
	if string(data) == "" || !contains(string(data), "unknown:") {
		t.Fatalf("unknown field lost: %s", data)
	}
}

func TestPresentationPatches_RejectsTouchedFlowCanonicalFragmentWithoutStage(t *testing.T) {
	// Arrange. A canonical fragment inside a flow mapping has no supported
	// lossless structural proof, even though unrelated flow extension bytes are
	// otherwise allowed in the document.
	s := memorySource{
		"a.md": []byte("---\r\ntype: 'thing' # keep\r\nunknown: {future: [one, two]}\r\nparts: [{id: \"old\", note: keep}]\r\n---\r\n\r\nBody  "),
		"b.md": []byte("---\r\ntype: thing\r\nrelations:\r\n  uses:\r\n    - target: 'a#old' # keep\r\n---\r\n\r\nB"),
	}
	op := store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, op))

	// Assert.
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("Plan() error = %v, want ErrUnsupportedPresentation", err)
	}
	var presentationErr *PresentationError
	if !errors.As(err, &presentationErr) || presentationErr.Format != "yaml" || presentationErr.Code != "canonical_fragment_flow_mapping" {
		t.Fatalf("presentation error = %#v", presentationErr)
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
		t.Fatalf("Plan() returned partial stage: %#v", result)
	}
}

func TestFragmentPatch_UsesSemanticCanonicalIdentity(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		from    string
		want    string
		wantErr bool
	}{
		{
			name:    "invalid id before valid id",
			input:   "---\r\nparts:\r\n  - id: 17\r\n    id: 'old' # keep\r\n---\r\nBody",
			from:    "old",
			wantErr: true,
		},
		{
			name:    "valid id before invalid id",
			input:   "---\nparts:\n  - id: \"old\"\n    id: 17\n---\nBody\n",
			from:    "old",
			wantErr: true,
		},
		{
			name:    "invalid id next to valid anchor is unsupported",
			input:   "---\nparts:\n  - id: 17\n    anchor: old\n---\nBody\n",
			from:    "old",
			wantErr: true,
		},
		{
			name:    "duplicate valid ids with anchor are ambiguous",
			input:   "---\nparts:\n  - id: old\n    id: other\n    anchor: alias\n---\nBody\n",
			from:    "old",
			wantErr: true,
		},
		{
			name:    "duplicate valid anchors are ambiguous",
			input:   "---\nparts:\n  - anchor: old\n    anchor: other\n---\nBody\n",
			from:    "old",
			wantErr: true,
		},
		{
			name:  "id anchor alias patches canonical id only",
			input: "---\nparts:\n  - id: old\n    anchor: legacy\n---\nBody\n",
			from:  "old",
			want:  "---\nparts:\n  - id: new\n    anchor: legacy\n---\nBody\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			p, err := parsePresentationContext(context.Background(), []byte(tt.input))
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			patch, found, err := fragmentPatchContext(context.Background(), p, tt.from, "new")

			// Assert.
			if tt.wantErr {
				if err == nil {
					t.Fatal("fragmentPatchContext(context.Background(), ) error = nil, want typed presentation error")
				}
				if !errors.Is(err, ErrUnsupportedPresentation) && !errors.Is(err, ErrAmbiguousPresentation) {
					t.Fatalf("fragmentPatchContext(context.Background(), ) error = %v, want Unsupported or Ambiguous", err)
				}
				return
			}
			if err != nil || !found {
				t.Fatalf("fragmentPatchContext(context.Background(), ) = (%#v, %t, %v), want patch", patch, found, err)
			}
			got, err := p.patchYAMLContext(context.Background(), []bytePatch{patch})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("patched bytes = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEnsureRelation_RejectsDuplicateMappingKeys(t *testing.T) {
	// Arrange. A successful desired-state operation may not leave a second YAML
	// mapping key behind, so the ambiguous source is rejected before staging.
	s := memorySource{"a.md": []byte("---\ntype: thing\nrelations: {uses: []}\nrelations: {uses: []}\n---\n\nA\n"), "b.md": []byte("---\ntype: thing\n---\n\nB\n")}
	op := store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}

	// Act.
	_, err := Plan(context.Background(), s, change(t, s, op))

	// Assert.
	var invalidChange *store.InvalidChangeSet
	if !errors.As(err, &invalidChange) || invalidChange.Code != "ambiguous_relation_presentation" {
		t.Fatalf("error = %#v", err)
	}
}

func TestRenameFragment_PreservesDuplicateUnrelatedKeys(t *testing.T) {
	// Arrange.
	s := memorySource{"a.md": []byte("---\ntype: thing\nparts:\n  - anchor: old\n    label: one\n    label: two\n---\nA\n")}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, store.RenameFragment{
		Concept: ref(t, "a").ID,
		From:    "old",
		To:      "new",
	}))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "a.md")
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("---\ntype: thing\nparts:\n  - anchor: new\n    label: one\n    label: two\n---\nA\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("unrelated duplicate keys changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMoveConcept_PreservesDuplicateUnrelatedRelationExtensions(t *testing.T) {
	// Arrange.
	s := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a\n      note: one\n      note: two\n---\nB\n"),
	}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "b.md")
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: moved/a\n      note: one\n      note: two\n---\nB\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("unrelated relation extensions changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRelationUpdatesRejectDuplicateRelationTypesAsAmbiguous(t *testing.T) {
	for _, tt := range []struct {
		name   string
		source memorySource
		op     store.Operation
	}{
		{
			name: "move concept",
			source: memorySource{
				"a.md": []byte("---\ntype: thing\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a\n  uses:\n    - target: c\n---\nB\n"),
				"c.md": []byte("---\ntype: thing\n---\nC\n"),
			},
			op: store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID},
		},
		{
			name: "rename fragment",
			source: memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a#old\n  uses:\n    - target: c\n---\nB\n"),
				"c.md": []byte("---\ntype: thing\n---\nC\n"),
			},
			op: store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := make(memorySource, len(tt.source))
			for path, data := range tt.source {
				before[path] = append([]byte(nil), data...)
			}
			result, err := Plan(context.Background(), tt.source, change(t, tt.source, tt.op))
			if !errors.Is(err, ErrAmbiguousPresentation) || result.Staged != nil {
				t.Fatalf("result/error = %#v / %v", result, err)
			}
			var presentation *PresentationError
			if !errors.As(err, &presentation) || presentation.Code != "duplicate_relation_type" {
				t.Fatalf("presentation = %#v", presentation)
			}
			if !reflect.DeepEqual(tt.source, before) {
				t.Fatalf("source mutated: got=%#v want=%#v", tt.source, before)
			}
		})
	}
}

func TestPresentationInvalid_DoesNotNestPresentationError(t *testing.T) {
	s := memorySource{"a.md": []byte("---\ntype: thing\nparts: [{id: old}]\n---\nA\n")}
	_, err := Plan(context.Background(), s, change(t, s, store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}))
	var presentation *PresentationError
	if !errors.As(err, &presentation) {
		t.Fatalf("error = %v", err)
	}
	var nested *PresentationError
	if errors.As(presentation.Err, &nested) {
		t.Fatalf("PresentationError.Err nested another PresentationError: %#v", presentation.Err)
	}
	if !errors.Is(presentation.Err, ErrUnsupportedPresentation) {
		t.Fatalf("Err = %v, want sentinel", presentation.Err)
	}
}

func TestEnsureRelation_RejectsDuplicateTargetsAsAmbiguous(t *testing.T) {
	// Arrange.
	s := memorySource{"a.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: b\n    - target: b\n---\n\nA\n"), "b.md": []byte("---\ntype: thing\n---\n\nB\n")}
	op := store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, op))

	// Assert.
	if !errors.Is(err, ErrAmbiguousPresentation) || result.Staged != nil {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
}

func TestUpdateRoutesRejectDuplicateSemanticCandidatesWithoutStage(t *testing.T) {
	tests := []struct {
		name, code string
		files      memorySource
		op         store.Operation
	}{
		{
			name: "move duplicate relation targets", code: "duplicate_target",
			files: memorySource{
				"a.md": []byte("---\ntype: thing\n---\nA\n"),
				"c.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a\n    - target: 'a'\n---\nC\n"),
			},
			op: store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID},
		},
		{
			name: "rename duplicate relation targets", code: "duplicate_target",
			files: memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n---\nA\n"),
				"c.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a#old\n    - target: \"a#old\"\n---\nC\n"),
			},
			op: store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"},
		},
		{
			name: "rename duplicate source fragments", code: "duplicate_canonical_fragment",
			files: memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n  - anchor: old\n---\nA\n"),
			},
			op: store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"},
		},
		{
			name: "rename ambiguous destination fragment", code: "duplicate_canonical_fragment",
			files: memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n  - id: new\n  - anchor: new\n---\nA\n"),
			},
			op: store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange / Act.
			result, err := Plan(context.Background(), test.files, change(t, test.files, test.op))

			// Assert.
			var presentation *PresentationError
			if !errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) || presentation.Code != test.code {
				t.Fatalf("Plan() error = %#v, want %s ambiguity", err, test.code)
			}
			if presentation.Location.Start >= presentation.Location.End {
				t.Fatalf("PresentationError.Location = %#v", presentation.Location)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
				t.Fatalf("rejected update exposed stage: %#v", result)
			}
		})
	}
}

func TestUnrelatedMergeProvenanceDoesNotBlockMutationRoutes(t *testing.T) {
	unrelated := []byte("---\ntype: thing\nbase: &base\n  cites:\n    - target: b\nrelations:\n  <<: *base\n---\nC\n")
	tests := []struct {
		name  string
		files memorySource
		op    store.Operation
	}{
		{
			name: "move ignores unrelated merged relation",
			files: memorySource{
				"a.md": []byte("---\ntype: thing\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
				"c.md": unrelated,
			},
			op: store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID},
		},
		{
			name: "rename ignores unrelated merged relation",
			files: memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
				"c.md": unrelated,
			},
			op: store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"},
		},
		{
			name: "ensure selector ignores unrelated merged identity",
			files: memorySource{
				"a.md": []byte("---\ntype: thing\nbase: &base\n  id: other\nparts:\n  - id: source\n  - <<: *base\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			},
			op: store.EnsureRelation{Source: ref(t, "a#source"), Type: "uses", Target: ref(t, "b")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange / Act.
			result, err := Plan(context.Background(), test.files, change(t, test.files, test.op))

			// Assert.
			if err != nil || result.Staged == nil {
				t.Fatalf("Plan() = %#v, %v", result, err)
			}
			if before, ok := test.files["c.md"]; ok {
				after, readErr := result.Staged.ReadFile(context.Background(), "c.md")
				if readErr != nil || !bytes.Equal(after, before) {
					t.Fatalf("unrelated merge changed: %q, %v", after, readErr)
				}
			}
		})
	}
}

func TestEnsureRelation_ClassifiesCanonicalEndpointAmbiguityBeforeMissing(t *testing.T) {
	tests := []struct {
		name   string
		source memorySource
		op     store.EnsureRelation
		path   string
	}{
		{
			name: "duplicate source ids in one mapping",
			source: memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: part\n    id: part\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			},
			op: store.EnsureRelation{Source: ref(t, "a#part"), Type: "uses", Target: ref(t, "b")}, path: "a.md",
		},
		{
			name: "duplicate source fragment across mappings",
			source: memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: part\n  - id: part\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\n---\nB\n"),
			},
			op: store.EnsureRelation{Source: ref(t, "a#part"), Type: "uses", Target: ref(t, "b")}, path: "a.md",
		},
		{
			name: "duplicate target anchors in one mapping",
			source: memorySource{
				"a.md": []byte("---\ntype: thing\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\nparts:\n  - anchor: part\n    anchor: part\n---\nB\n"),
			},
			op: store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b#part")}, path: "b.md",
		},
		{
			name: "duplicate target fragment across mappings",
			source: memorySource{
				"a.md": []byte("---\ntype: thing\n---\nA\n"),
				"b.md": []byte("---\ntype: thing\nparts:\n  - anchor: part\n  - anchor: part\n---\nB\n"),
			},
			op: store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b#part")}, path: "b.md",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			before := make(memorySource, len(tt.source))
			for path, data := range tt.source {
				before[path] = append([]byte(nil), data...)
			}

			// Act.
			result, err := Plan(context.Background(), tt.source, change(t, tt.source, tt.op))

			// Assert.
			if !errors.Is(err, ErrAmbiguousPresentation) || result.Staged != nil {
				t.Fatalf("result/error = %#v / %v", result, err)
			}
			var presentation *PresentationError
			if !errors.As(err, &presentation) || presentation.Code != "duplicate_canonical_fragment" || presentation.Path != tt.path || presentation.Operation != "ensure_relation" {
				t.Fatalf("presentation = %#v", presentation)
			}
			if !reflect.DeepEqual(tt.source, before) {
				t.Fatalf("source mutated: got=%#v want=%#v", tt.source, before)
			}
		})
	}
}

func TestEnsureRelation_IDWinsOverAnchorForEndpointLookup(t *testing.T) {
	// Arrange.
	s := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\nparts:\n  - id: canonical\n    anchor: alias\n---\nB\n"),
	}
	op := store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b#alias")}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, op))

	// Assert.
	if err == nil || errors.Is(err, ErrAmbiguousPresentation) || result.Staged != nil {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
	var invalidChange *store.InvalidChangeSet
	if !errors.As(err, &invalidChange) || invalidChange.Code != "missing_relation_endpoint" {
		t.Fatalf("invalid change = %#v", invalidChange)
	}
}

func TestPlanNeverPublishesNonCanonicalFrontmatterDelimiterRewrite(t *testing.T) {
	for _, tt := range []struct {
		name, open, newline string
	}{
		{name: "leading space lf", open: " ---", newline: "\n"},
		{name: "trailing space lf", open: "--- ", newline: "\n"},
		{name: "leading tab crlf", open: "\t---", newline: "\r\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			data := []byte(tt.open + tt.newline + "type: thing" + tt.newline + "unknown: \"[A](a.md)\"" + tt.newline + "---" + tt.newline + "A" + tt.newline)
			s := memorySource{"a.md": data}
			before := append([]byte(nil), data...)

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}))

			// Assert.
			if err == nil || result.Staged != nil {
				t.Fatalf("result/error = %#v / %v", result, err)
			}
			if !bytes.Equal(s["a.md"], before) {
				t.Fatalf("source changed: %q", s["a.md"])
			}
		})
	}
}

func TestEnsureRelation_RejectsPresentedDuplicateTargets(t *testing.T) {
	tests := []struct {
		name  string
		items string
	}{
		{
			name: "distinct extension fields",
			items: "    - target: b\n" +
				"      note: keep-first\n" +
				"    - target: b\n" +
				"      audit: keep-second\n",
		},
		{
			name: "overlapping extension fields",
			items: "    - target: b\n" +
				"      note: shared\n" +
				"    - target: b\n" +
				"      note: shared\n",
		},
		{
			name: "conflicting extension fields",
			items: "    - target: b\n" +
				"      note: first\n" +
				"    - target: b\n" +
				"      note: second\n",
		},
		{
			name: "comment on duplicate",
			items: "    - target: b # audit trail\n" +
				"    - target: b\n",
		},
		{
			name: "anchor on duplicate",
			items: "    - &first {target: b}\n" +
				"    - target: b\n",
		},
		{
			name: "tagged target",
			items: "    - target: !!str b\n" +
				"    - target: b\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			raw := "---\ntype: thing\nrelations:\n  uses:\n" + tt.items + "---\n\nA\n"
			s := memorySource{
				"a.md": []byte(raw),
				"b.md": []byte("---\ntype: thing\n---\n\nB\n"),
			}
			op := store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, op))

			// Assert.
			var invalidChange *store.InvalidChangeSet
			var presentation *PresentationError
			if !errors.As(err, &invalidChange) || invalidChange.Code != "ambiguous_relation_presentation" || !errors.As(err, &presentation) || !errors.Is(err, ErrAmbiguousPresentation) {
				t.Fatalf("error = %#v", err)
			}
			if presentation.Code == "" || presentation.Format != "yaml" || presentation.Path != "a.md" || presentation.Operation != "ensure_relation" {
				t.Fatalf("PresentationError = %#v", presentation)
			}
			if len(invalidChange.Diagnostics) != 1 || invalidChange.Diagnostics[0].Code != "ambiguous_relation_presentation" {
				t.Fatalf("diagnostics = %#v", invalidChange.Diagnostics)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Plan) != 0 {
				t.Fatalf("Plan() returned partial stage: %#v", result)
			}
			got, readErr := s.ReadFile(context.Background(), "a.md")
			if readErr != nil || string(got) != raw {
				t.Fatalf("source mutated: %q, %v", got, readErr)
			}
		})
	}
}

func TestEnsureRelation_RejectsDuplicateTargetKeysWithoutStaging(t *testing.T) {
	for _, targets := range []string{"b\n      target: b", "b\n      target: other"} {
		t.Run(strings.ReplaceAll(targets, "\n", "_"), func(t *testing.T) {
			// Arrange. These are duplicate YAML mapping keys in one item, not two
			// independently-addressable relation items. They must never be
			// normalized by deleting the item selected by a presentation patch.
			raw := "---\ntype: thing\nrelations:\n  uses:\n    - target: " + targets + "\n      note: preserve\n---\n\nA\n"
			s := memorySource{
				"a.md":     []byte(raw),
				"b.md":     []byte("---\ntype: thing\n---\n\nB\n"),
				"other.md": []byte("---\ntype: thing\n---\n\nOther\n"),
			}
			op := store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, op))

			// Assert.
			var invalidChange *store.InvalidChangeSet
			if !errors.As(err, &invalidChange) || invalidChange.Code != "ambiguous_relation_presentation" {
				t.Fatalf("error = %#v", err)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 {
				t.Fatalf("Plan() returned partial stage: %#v", result)
			}
			got, readErr := s.ReadFile(context.Background(), "a.md")
			if readErr != nil || string(got) != raw {
				t.Fatalf("source mutated: %q, %v", got, readErr)
			}
		})
	}
}

func TestRelationTargetPatches_RejectDuplicateSemanticKeysBeforeMutation(t *testing.T) {
	// Arrange. Move and rename rewrite relation targets through a different
	// presentation path; both must reject the same ambiguous item before an
	// overlay can contain a partial patch.
	for _, op := range []store.Operation{
		store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID},
		store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"},
	} {
		t.Run(fmt.Sprintf("%T", op), func(t *testing.T) {
			s := memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n---\n\nA\n"),
				"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a#old\n      target: a#old\n      note: preserve\n---\n\nB\n"),
			}

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, op))

			// Assert.
			var invalidChange *store.InvalidChangeSet
			if !errors.As(err, &invalidChange) || invalidChange.Code != "ambiguous_presentation" || !errors.Is(err, ErrAmbiguousPresentation) {
				t.Fatalf("error = %#v", err)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 {
				t.Fatalf("Plan() returned partial stage: %#v", result)
			}
		})
	}
}

func TestPlanYAML_TouchedFlowRelationNeverStages(t *testing.T) {
	for _, op := range []store.Operation{
		store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID},
		store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"},
	} {
		t.Run(fmt.Sprintf("%T", op), func(t *testing.T) {
			// Arrange.
			relation := "a"
			if _, ok := op.(store.RenameFragment); ok {
				relation = "a#old"
			}
			raw := []byte("---\ntype: thing\nrelations: {uses: [{target: " + relation + "}]}\n---\nB\n")
			s := memorySource{
				"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n---\nA\n"),
				"b.md": raw,
			}

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, op))

			// Assert.
			var invalidChange *store.InvalidChangeSet
			if !errors.As(err, &invalidChange) || invalidChange.Code != "invalid_presentation" || result.Staged != nil {
				t.Fatalf("result/error = %#v / %v", result, err)
			}
			got := readSourceFileForTest(t, s, "b.md")
			if !bytes.Equal(got, raw) {
				t.Fatalf("source changed: %q", got)
			}
		})
	}
}

func TestPlanYAML_UnrelatedFlowRelationDoesNotVetoMove(t *testing.T) {
	// Arrange.
	raw := []byte("---\ntype: thing\nrelations: {uses: [{target: c}]}\n---\nB\n")
	p, err := parsePresentationContext(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	patches, err := relationTargetPatchesContext(context.Background(), p, ref(t, "a").ID, ref(t, "moved/a").ID, "", "")
	updated, patchErr := p.patchYAMLContext(context.Background(), patches)

	// Assert.
	if err != nil || patchErr != nil || len(patches) != 0 || !bytes.Equal(updated, raw) {
		t.Fatalf("patches/bytes = %#v / %q / %v / %v", patches, updated, err, patchErr)
	}
}

func TestMoveConcept_UnrelatedUnsupportedYAMLDoesNotVeto(t *testing.T) {
	s := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\n...\nextra: 1\n---\nB\n"),
	}
	result, err := Plan(context.Background(), s, change(t, s, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Staged == nil {
		t.Fatal("expected staged move")
	}
	got, err := result.Staged.ReadFile(context.Background(), "b.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "---\ntype: thing\n...\nextra: 1\n---\nB\n" {
		t.Fatalf("unrelated file changed: %q", got)
	}
}

func TestMoveConcept_RelatedUnsupportedYAMLStillFailsClosed(t *testing.T) {
	s := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a\n...\nextra: 1\n---\nB\n"),
	}
	result, err := Plan(context.Background(), s, change(t, s, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}))
	if !errors.Is(err, ErrUnsupportedPresentation) || result.Staged != nil {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
}

func TestRenameFragment_UnrelatedUnsupportedYAMLDoesNotVeto(t *testing.T) {
	s := memorySource{
		"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\n...\nextra: 1\n---\nB\n"),
	}
	result, err := Plan(context.Background(), s, change(t, s, store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Staged == nil {
		t.Fatal("expected staged rename")
	}
	got, err := result.Staged.ReadFile(context.Background(), "b.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "---\ntype: thing\n...\nextra: 1\n---\nB\n" {
		t.Fatalf("unrelated file changed: %q", got)
	}
}

func TestRenameFragment_RelatedUnsupportedYAMLStillFailsClosed(t *testing.T) {
	s := memorySource{
		"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a#old\n...\nextra: 1\n---\nB\n"),
	}
	result, err := Plan(context.Background(), s, change(t, s, store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}))
	if !errors.Is(err, ErrUnsupportedPresentation) || result.Staged != nil {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
}

func TestPlanMoveConcept_AmbiguousMarkdownLocationIsFullFile(t *testing.T) {
	s := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\n---\n[use][r]\n\n[r]: a.md\n[r]: a.md\n"),
	}
	result, err := Plan(context.Background(), s, change(t, s, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}))
	if !errors.Is(err, ErrAmbiguousPresentation) || result.Staged != nil {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Code != "duplicate_reference_definition" || presentation.Path != "b.md" {
		t.Fatalf("presentation = %#v", presentation)
	}
	data := s["b.md"]
	bodyStart := bytes.Index(data, []byte("[use]"))
	if presentation.Location.Start < bodyStart || presentation.Location.End > len(data) || presentation.Location.Start >= presentation.Location.End {
		t.Fatalf("Location %#v bodyStart=%d len=%d", presentation.Location, bodyStart, len(data))
	}
	if !bytes.Contains(data[presentation.Location.Start:presentation.Location.End], []byte("a.md")) {
		t.Fatalf("Location %#v does not cover candidates in %q", presentation.Location, data)
	}
}

func TestApplyBytePatches_RejectsOverlap(t *testing.T) {
	// Arrange / Act.
	_, err := applyBytePatchesContext(context.Background(), []byte("abcd"), []bytePatch{{Start: 1, End: 3}, {Start: 2, End: 4}})

	// Assert.
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("error = %v, want ErrUnsupportedPresentation", err)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Code != yamlCodeOverlappingPatch {
		t.Fatalf("presentation = %#v", presentation)
	}
}

func TestRelationTargetPatchesPropagatesInvalidSourceRange(t *testing.T) {
	// Arrange.
	p, err := parsePresentationContext(context.Background(), []byte("---\nrelations:\n  uses:\n    - target: b\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	target := mappingValuesForTest(t, mappingValuesForTest(t, mappingValuesForTest(t, p.root, "relations")[0], "uses")[0].Content[0], "target")[0]
	target.Line = 99

	// Act.
	_, err = relationTargetPatchesContext(context.Background(), p, ref(t, "b").ID, ref(t, "moved").ID, "", "")

	// Assert.
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("relationTargetPatchesContext(context.Background(), ) error = %v, want ErrUnsupportedPresentation", err)
	}
	var presentationErr *PresentationError
	if !errors.As(err, &presentationErr) || presentationErr.Code != "invalid_coordinate" || presentationErr.Location.Start >= presentationErr.Location.End {
		t.Fatalf("presentation error = %#v", presentationErr)
	}
}

func TestPlanMoveConceptPresentationFailureReturnsNoStage(t *testing.T) {
	// Arrange. Impacted file carries a semantic relation to the moved concept and
	// unsupported presentation that the lossless YAML path must reject before stage.
	s := memorySource{
		"a.md": []byte("---\ntype: Note\n---\nA\n"),
		"b.md": []byte("---\ntype: Note\nrelations:\n  uses:\n    - target: a\n...\nextra: 1\n---\nB\n"),
	}
	changeSet := change(t, s, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved").ID})

	// Act.
	result, err := Plan(context.Background(), s, changeSet)

	// Assert.
	var invalidChange *store.InvalidChangeSet
	if !errors.As(err, &invalidChange) || invalidChange.Code != "invalid_presentation" || !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("Plan() error = %#v, want invalid_presentation", err)
	}
	if result.Staged != nil || len(result.Preview.Renames) != 0 {
		t.Fatalf("Plan() returned partial stage: %#v", result)
	}
}

func TestPlanEnsureRelation_AllowsInformationalAnchorAliasAndUsesCanonicalID(t *testing.T) {
	// Arrange. `id` is the canonical fragment; the differing anchor is only an
	// informational alias and must neither become a target nor block planning.
	s := memorySource{
		"a.md": []byte("---\ntype: thing\nparts:\n  - id: canonical\n    anchor: legacy-anchor\n---\n\nA\n"),
		"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a#canonical\n---\n\nB\n"),
	}
	op := store.EnsureRelation{Source: ref(t, "a#canonical"), Type: "uses", Target: ref(t, "b")}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, op))

	// Assert.
	if err != nil {
		t.Fatalf("Plan() error = %v; diagnostics=%#v", err, result.RelationDiagnostics)
	}
	loaded, err := bundle.Load(context.Background(), result.Staged)
	if err != nil {
		t.Fatal(err)
	}
	links := loaded.SemanticLinksFrom(ref(t, "a").ID)
	if len(links) != 1 || links[0].Source.String() != "a#canonical" || links[0].Target.String() != "b" {
		t.Fatalf("canonical source relation = %#v", links)
	}
	incoming := loaded.SemanticLinksTo(ref(t, "a#canonical"))
	if len(incoming) != 1 || incoming[0].Target.String() != "a#canonical" {
		t.Fatalf("canonical target relation = %#v", incoming)
	}
	for _, diagnostic := range result.RelationDiagnostics {
		if diagnostic.Code == "anchor_alias" && diagnostic.BlocksMutation() {
			t.Fatalf("anchor alias unexpectedly blocks mutation: %#v", diagnostic)
		}
	}
}

func TestPlanRenameFragment_RewritesIncomingReference(t *testing.T) {
	// Arrange
	s := memorySource{
		"a.md": []byte("---\ntype: thing\nid: root-metadata\nanchor: root-anchor\nparts:\n  - id: old\n    label: preserve\n---\n\nA\n"),
		"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a#old\n---\n\nB\n"),
		"c.md": []byte("---\ntype: thing\nparts:\n  - id: source\n    relations:\n      uses:\n        - target: a#old\n---\n\nC\n"),
	}
	op := store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}
	// Act
	result, err := Plan(context.Background(), s, change(t, s, op))
	// Assert
	if err != nil {
		t.Fatalf("%v; validation=%#v relation=%#v", err, result.Validation, result.RelationDiagnostics)
	}
	b, err := bundle.Load(context.Background(), result.Staged)
	if err != nil {
		t.Fatal(err)
	}
	links := b.SemanticLinksFrom(ref(t, "b").ID)
	if len(links) != 1 || links[0].Target.String() != "a#new" {
		t.Fatalf("links = %#v", links)
	}
	data := readSourceFileForTest(t, result.Staged, "a.md")
	if !contains(string(data), "label: preserve") || !contains(string(data), "id: root-metadata") || !contains(string(data), "anchor: root-anchor") {
		t.Fatalf("unknown nested field lost: %s", data)
	}
	if got := relationRefStrings(result.Preview.AffectedRefs); !sameStrings(got, []string{"a#new", "a#old"}) {
		t.Fatalf("affected refs = %v", got)
	}
	if got := relationRefStrings(result.Preview.ReverseImpact); !sameStrings(got, []string{"b", "c#source"}) {
		t.Fatalf("reverse impact = %v", got)
	}
}

func TestPlanMoveConcept_RewritesSemanticAndIndexLinks(t *testing.T) {
	// Arrange
	s := memorySource{
		"a.md":     []byte("---\ntype: thing\nzeta: preserve-first\nalpha: preserve-second\nparts:\n  - id: fragment\n---\n\nA\n"),
		"b.md":     []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a\n    - target: a#fragment\n---\n\nB\n"),
		"c.md":     []byte("---\ntype: thing\nparts:\n  - id: source\n    relations:\n      uses:\n        - target: a#fragment\n---\n\nC\n"),
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Bundle\n\n- [A](a.md)\n"),
	}
	op := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}
	// Act
	result, err := Plan(context.Background(), s, change(t, s, op))
	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, err := result.Staged.ReadFile(context.Background(), "a.md"); err == nil {
		t.Fatal("old concept remains visible")
	}
	b, err := bundle.Load(context.Background(), result.Staged)
	if err != nil {
		t.Fatal(err)
	}
	links := b.SemanticLinksFrom(ref(t, "b").ID)
	if len(links) != 2 || links[0].Target.String() != "nested/a" || links[1].Target.String() != "nested/a#fragment" {
		t.Fatalf("links = %#v", links)
	}
	index := readSourceFileForTest(t, result.Staged, "index.md")
	if !contains(string(index), "(nested/a.md)") {
		t.Fatalf("index not rewritten: %s", index)
	}
	moved, err := result.Staged.ReadFile(context.Background(), "nested/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(moved), "zeta: preserve-first\nalpha: preserve-second") {
		t.Fatalf("unknown YAML fields or ordering changed: %s", moved)
	}
	if got := relationRefStrings(result.Preview.AffectedRefs); !sameStrings(got, []string{"a", "nested/a"}) {
		t.Fatalf("affected refs = %v", got)
	}
	if got := relationRefStrings(result.Preview.ReverseImpact); !sameStrings(got, []string{"b", "c#source"}) {
		t.Fatalf("reverse impact = %v", got)
	}
}

func TestPlanMoveConcept_RewritesMarkdownDestinationsWithoutTouchingCode(t *testing.T) {
	// Arrange
	s := memorySource{
		"a.md":            []byte("---\ntype: thing\n---\n\nA\n"),
		"c.md":            []byte("---\ntype: thing\n---\n\n[A](a.md) ![image](a.md#cover) [titled](a.md#part \"A title\") [angle](<a.md?view=1#part>)\n\n[ref]: a.md#fragment \"Reference title\"\n\n[external](https://example.test/a.md) [protocol](//example.test/a.md) [absolute](/a.md) [suffix](other-a.md)\n`[inline](a.md)`\n```md\n[fenced](a.md)\n```\n    [indented](a.md)\n"),
		"index.md":        []byte("---\nokf_version: \"0.1\"\n---\n\n# Root\n\n- [A](a.md)\n- [C](c.md)\n- [Nested](nested/index.md)\n"),
		"nested/index.md": []byte("# Nested\n\n- [up](../a.md)\n"),
	}
	op := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}

	// Act
	result, err := Plan(context.Background(), s, change(t, s, op))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	c, err := result.Staged.ReadFile(context.Background(), "c.md")
	if err != nil {
		t.Fatal(err)
	}
	got := string(c)
	for _, want := range []string{
		"[A](nested/a.md)",
		"![image](nested/a.md#cover)",
		"[titled](nested/a.md#part \"A title\")",
		"[angle](<nested/a.md?view=1#part>)",
		"[ref]: nested/a.md#fragment \"Reference title\"",
		"[external](https://example.test/a.md) [protocol](//example.test/a.md) [absolute](/nested/a.md) [suffix](other-a.md)",
		"`[inline](a.md)`",
		"[fenced](a.md)",
		"    [indented](a.md)",
	} {
		if !contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	nested, err := result.Staged.ReadFile(context.Background(), "nested/index.md")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(nested), "[up](a.md)") {
		t.Fatalf("nested link not rewritten: %s", nested)
	}
}

func TestPlanMoveConcept_RewritesLinksWhenMovingOutOfNestedDirectory(t *testing.T) {
	// Arrange
	s := memorySource{
		"nested/a.md": []byte("---\ntype: thing\n---\n\n[self](a.md)\n"),
		"nested/c.md": []byte("---\ntype: thing\n---\n\n[target](a.md)\n"),
		"index.md":    []byte("---\nokf_version: \"0.1\"\n---\n\n# Root\n\n- [target](nested/a.md)\n"),
	}
	op := store.MoveConcept{From: ref(t, "nested/a").ID, To: ref(t, "a").ID}

	// Act
	result, err := Plan(context.Background(), s, change(t, s, op))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	moved := readSourceFileForTest(t, result.Staged, "a.md")
	if !contains(string(moved), "[self](a.md)") {
		t.Fatalf("moved file link not recalculated: %s", moved)
	}
	nested := readSourceFileForTest(t, result.Staged, "nested/c.md")
	if !contains(string(nested), "[target](../a.md)") {
		t.Fatalf("nested link not rewritten: %s", nested)
	}
	root := readSourceFileForTest(t, result.Staged, "index.md")
	if !contains(string(root), "[target](a.md)") {
		t.Fatalf("root link not rewritten: %s", root)
	}
}

func TestPlanMoveConcept_PreservesOutgoingTargetsOfMovedDocument(t *testing.T) {
	// Arrange. Every relative destination in a moved document is interpreted
	// from its old path, then emitted relative to its new path. This applies to
	// non-self targets as well as links, images, and reference definitions.
	s := memorySource{
		"a.md": []byte("---\ntype: thing\n---\n\n[B](b.md) ![B](b.md?raw=1#image) [self](a.md#part) [via-ref][b]\n\n[b]: b.md#section \"B\"\n"),
		"b.md": []byte("---\ntype: thing\n---\n\nB\n"),
	}
	op := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, op))

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	moved, err := result.Staged.ReadFile(context.Background(), "nested/a.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[B](../b.md)",
		"![B](../b.md?raw=1#image)",
		"[self](a.md#part)",
		"[b]: ../b.md#section \"B\"",
	} {
		if !bytes.Contains(moved, []byte(want)) {
			t.Fatalf("moved document = %q, want %q", moved, want)
		}
	}
}

func TestPlanMoveConcept_ReportsInvalidUTF8MarkdownBodyLocation(t *testing.T) {
	// Arrange.
	invalid := []byte("---\ntype: thing\n---\nbody ")
	invalid = append(invalid, 0xff)
	s := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": invalid,
	}
	op := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, op))

	// Assert.
	var presentation *PresentationError
	if !errors.Is(err, bundle.ErrInvalidEncoding) || !errors.Is(err, ErrUnsupportedPresentation) || !errors.As(err, &presentation) {
		t.Fatalf("Plan() error = %#v, want typed invalid encoding presentation error", err)
	}
	if presentation.Format != "markdown" || presentation.Code != "invalid_encoding" || presentation.Path != "b.md" || presentation.Operation != "move_concept" {
		t.Fatalf("PresentationError = %#v", presentation)
	}
	if presentation.Location != (SourceSpan{Start: len(invalid) - 1, End: len(invalid)}) {
		t.Fatalf("Location = %#v, want invalid body byte", presentation.Location)
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
		t.Fatalf("rejected plan exposed staged mutation: %#v", result)
	}
}

func TestPlanMoveConcept_RewritesEveryPubliclyResolvableInternalDestination(t *testing.T) {
	tests := []struct {
		name, oldID, newID, sourcePath, destination, want string
	}{
		{name: "absolute", oldID: "a", newID: "moved/a", sourcePath: "c.md", destination: "/a.md", want: "/moved/a.md"},
		{name: "colon segment", oldID: "a:b", newID: "moved/a:b", sourcePath: "c.md", destination: "a:b.md", want: "moved/a:b.md"},
		{name: "nested colon", oldID: "dir:x/a", newID: "moved/a", sourcePath: "c.md", destination: "dir:x/a.md", want: "moved/a.md"},
		{name: "query fragment", oldID: "a", newID: "moved/a", sourcePath: "c.md", destination: "a.md?view=1#part", want: "moved/a.md?view=1#part"},
		{name: "excess parent", oldID: "a", newID: "moved/a", sourcePath: "nested/c.md", destination: "../../../a.md?view=1", want: "../moved/a.md?view=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			old, err := bundle.ParseConceptID(tt.oldID)
			if err != nil {
				t.Fatal(err)
			}
			moved, err := bundle.ParseConceptID(tt.newID)
			if err != nil {
				t.Fatal(err)
			}
			s := memorySource{
				conceptPath(old): []byte("---\ntype: thing\n---\nOld\n"),
				tt.sourcePath:    []byte("---\ntype: thing\n---\n[x](" + tt.destination + ")\n"),
			}

			// Act.
			result, err := Plan(context.Background(), s, change(t, s, store.MoveConcept{From: old, To: moved}))

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			updated, err := result.Staged.ReadFile(context.Background(), tt.sourcePath)
			if err != nil || !bytes.Contains(updated, []byte("[x]("+tt.want+")")) {
				t.Fatalf("updated = %q, error=%v; want destination %q", updated, err, tt.want)
			}
		})
	}
}

func TestPlanMoveConcept_MarkdownRewriteIsDeterministic(t *testing.T) {
	// Arrange
	s := memorySource{
		"a.md":       []byte("---\ntype: thing\n---\n\nA\n"),
		"notes/c.md": []byte("---\ntype: thing\n---\n\n[inline](../a.md#part)\n\n[reference]: ../a.md \"Title\"\n"),
		"index.md":   []byte("---\nokf_version: \"0.1\"\n---\n\n# Root\n\n- [A](a.md)\n- [Notes](notes/c.md)\n"),
	}
	op := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}

	// Act
	var first string
	for range 3 {
		result, err := Plan(context.Background(), s, change(t, s, op))
		if err != nil {
			t.Fatal(err)
		}
		data, err := result.Staged.ReadFile(context.Background(), "notes/c.md")
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = string(data)
			continue
		}
		if string(data) != first {
			t.Fatalf("non-deterministic Markdown rewrite:\nfirst: %q\n got: %q", first, data)
		}
	}

	// Assert
	if !contains(first, "[inline](../nested/a.md#part)") || !contains(first, "[reference]: ../nested/a.md \"Title\"") {
		t.Fatalf("unexpected rewritten Markdown: %s", first)
	}
}

func TestPlanPreconditionFailure(t *testing.T) {
	// Arrange
	s := memorySource{"a.md": []byte("---\ntype: thing\n---\n\nA\n"), "b.md": []byte("---\ntype: thing\n---\n\nB\n")}
	op := store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}
	c := change(t, s, op)
	c.Preconditions = []store.Precondition{store.RefAbsent{Ref: ref(t, "a")}}
	// Act
	_, err := Plan(context.Background(), s, c)
	// Assert
	if !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("error = %v", err)
	}
}

func TestPlanRejectsInvalidUTF8FragmentBeforeReadingSource(t *testing.T) {
	// Arrange.
	s := noReadSource{}
	valid := ref(t, "a")
	invalidTarget := bundle.RelationRef{ID: valid.ID, Fragment: "bad\xe2\x28\xa1fragment"}
	change := store.ChangeSet{
		Version:       store.ChangeSetFormatVersion,
		ID:            "change-1",
		Actor:         "tester",
		BaseRevision:  "not-read",
		Operations:    []store.Operation{store.EnsureRelation{Source: valid, Type: "uses", Target: invalidTarget}},
		Preconditions: []store.Precondition{store.RelationExists{Source: valid, Type: "uses", Target: invalidTarget}},
	}

	// Act.
	_, err := Plan(context.Background(), s, change)

	// Assert.
	if !errors.Is(err, store.ErrInvalidChangeSet) {
		t.Fatalf("Plan() error = %v, want ErrInvalidChangeSet", err)
	}
	var structured *store.InvalidChangeSet
	if !errors.As(err, &structured) || structured.Code != "invalid_change_set" {
		t.Fatalf("Plan() error = %#v, want structured invalid_change_set", err)
	}
}

func TestParsePresentationRejectsInvalidUTF8BeforeYAMLParsing(t *testing.T) {
	t.Parallel()

	// Act.
	_, err := parsePresentationContext(context.Background(), []byte("---\ntitle: "+string([]byte{0xff})+"\n---\nbody\n"))

	// Assert.
	if !errors.Is(err, bundle.ErrInvalidEncoding) {
		t.Fatalf("parsePresentationContext(context.Background(), ) error = %v, want ErrInvalidEncoding", err)
	}
}

func contains(s, part string) bool {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

func relationRefStrings(refs []bundle.RelationRef) []string {
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = ref.String()
	}
	return out
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
