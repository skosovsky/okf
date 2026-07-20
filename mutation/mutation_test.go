package mutation

import (
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

type manifestMemorySource struct {
	memorySource
	manifest store.Manifest
}

func (s manifestMemorySource) Manifest() store.Manifest { return s.manifest.Clone() }

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
	fresh, _ := o.ReadFile(context.Background(), "b.md")
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
			var got map[string]string
			err := yaml.Unmarshal([]byte("value: "+yamlScalarToken(tt.value, tt.preferred)+"\n"), &got)

			// Assert.
			if err != nil || got["value"] != tt.value {
				t.Fatalf("token %q parsed as %#v, %v; want %q", yamlScalarToken(tt.value, tt.preferred), got, err, tt.value)
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

func TestPresentationPatches_PreserveExactBytes(t *testing.T) {
	// Arrange. The fields and body deliberately exercise presentation that the
	// YAML encoder cannot round-trip: CRLF, comments, quotes, flow values, and
	// no final newline.
	s := memorySource{
		"a.md": []byte("---\r\ntype: 'thing' # keep\r\nunknown: {future: [one, two]}\r\nparts: [{id: \"old\", note: keep}]\r\n---\r\n\r\nBody  "),
		"b.md": []byte("---\r\ntype: thing\r\nrelations:\r\n  uses:\r\n    - target: 'a#old' # keep\r\n---\r\n\r\nB"),
	}
	op := store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, op))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := result.Staged.ReadFile(context.Background(), "a.md")
	b, _ := result.Staged.ReadFile(context.Background(), "b.md")

	// Assert.
	if want := "---\r\ntype: 'thing' # keep\r\nunknown: {future: [one, two]}\r\nparts: [{id: \"new\", note: keep}]\r\n---\r\n\r\nBody  "; string(a) != want {
		t.Fatalf("a bytes changed:\n%q\nwant:\n%q", a, want)
	}
	if want := "---\r\ntype: thing\r\nrelations:\r\n  uses:\r\n    - target: 'a#new' # keep\r\n---\r\n\r\nB"; string(b) != want {
		t.Fatalf("b bytes changed:\n%q\nwant:\n%q", b, want)
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
			name:  "invalid id before valid id",
			input: "---\r\nparts:\r\n  - id: 17\r\n    id: 'old' # keep\r\n---\r\nBody",
			from:  "old",
			want:  "---\r\nparts:\r\n  - id: 17\r\n    id: 'new' # keep\r\n---\r\nBody",
		},
		{
			name:  "valid id before invalid id",
			input: "---\nparts:\n  - id: \"old\"\n    id: 17\n---\nBody\n",
			from:  "old",
			want:  "---\nparts:\n  - id: \"new\"\n    id: 17\n---\nBody\n",
		},
		{
			name:  "invalid id falls back to valid anchor",
			input: "---\nparts:\n  - id: 17\n    anchor: old\n---\nBody\n",
			from:  "old",
			want:  "---\nparts:\n  - id: 17\n    anchor: new\n---\nBody\n",
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
			p, err := parsePresentation([]byte(tt.input))
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			patch, found, err := fragmentPatch(p, tt.from, "new")

			// Assert.
			if tt.wantErr {
				if err == nil {
					t.Fatal("fragmentPatch() error = nil, want ambiguity error")
				}
				return
			}
			if err != nil || !found {
				t.Fatalf("fragmentPatch() = (%#v, %t, %v), want patch", patch, found, err)
			}
			got, err := p.patchYAML([]bytePatch{patch})
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

func TestEnsureRelation_NormalizesDuplicateTargets(t *testing.T) {
	// Arrange.
	s := memorySource{"a.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: b\n    - target: b\n---\n\nA\n"), "b.md": []byte("---\ntype: thing\n---\n\nB\n")}
	op := store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")}

	// Act.
	result, err := Plan(context.Background(), s, change(t, s, op))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := bundle.Load(context.Background(), result.Staged)
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if got := len(loaded.SemanticLinksFrom(ref(t, "a").ID)); got != 1 {
		t.Fatalf("resolved relation count = %d", got)
	}
	data, _ := result.Staged.ReadFile(context.Background(), "a.md")
	if got := strings.Count(string(data), "target: b"); got != 1 {
		t.Fatalf("target count = %d: %s", got, data)
	}
}

func TestEnsureRelation_RejectsLossyDuplicateTargetNormalization(t *testing.T) {
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
			if !errors.As(err, &invalidChange) || invalidChange.Code != "lossy_relation_deduplication" {
				t.Fatalf("error = %#v", err)
			}
			if len(invalidChange.Diagnostics) != 1 || invalidChange.Diagnostics[0].Code != "lossy_relation_deduplication" {
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
			if !errors.As(err, &invalidChange) || invalidChange.Code != "invalid_presentation" {
				t.Fatalf("error = %#v", err)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 {
				t.Fatalf("Plan() returned partial stage: %#v", result)
			}
		})
	}
}

func TestApplyBytePatches_RejectsOverlap(t *testing.T) {
	// Arrange / Act.
	_, err := applyBytePatches([]byte("abcd"), []bytePatch{{Start: 1, End: 3}, {Start: 2, End: 4}})

	// Assert.
	if err == nil {
		t.Fatal("overlapping patches accepted")
	}
}

func TestRelationTargetPatchesPropagatesInvalidSourceRange(t *testing.T) {
	// Arrange.
	target := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "b", Line: 99, Column: 1}
	p := &presentation{
		yaml: []byte("relations:\n  uses:\n    - target: b\n"),
		root: &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "relations"},
			{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "uses"},
				{Kind: yaml.SequenceNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "target"}, target,
				}}}},
			}},
		}},
	}

	// Act.
	_, err := relationTargetPatches(p, ref(t, "b").ID, ref(t, "moved").ID, "", "")

	// Assert.
	if err == nil || !strings.Contains(err.Error(), "line outside source") {
		t.Fatalf("relationTargetPatches() error = %v, want source-range error", err)
	}
}

func TestPlanMoveConceptPresentationFailureReturnsNoStage(t *testing.T) {
	// Arrange.
	s := memorySource{
		"a.md": []byte("---\ntype: Note\n---\nA\n"),
		"b.md": []byte("---\ntype: Note\nrelations:\n"),
	}
	changeSet := change(t, s, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved").ID})

	// Act.
	result, err := Plan(context.Background(), s, changeSet)

	// Assert.
	var invalidChange *store.InvalidChangeSet
	if !errors.As(err, &invalidChange) || invalidChange.Code != "invalid_presentation" {
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
	data, _ := result.Staged.ReadFile(context.Background(), "a.md")
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
	index, _ := result.Staged.ReadFile(context.Background(), "index.md")
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
		"c.md":            []byte("---\ntype: thing\n---\n\n[A](a.md) ![image](a.md#cover) [titled](a.md#part \"A title\") [angle](<a.md?view=1#part>)\n[ref]: a.md#fragment \"Reference title\"\n[external](https://example.test/a.md) [protocol](//example.test/a.md) [absolute](/a.md) [suffix](other-a.md)\n`[inline](a.md)`\n```md\n[fenced](a.md)\n```\n    [indented](a.md)\n"),
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
		"[external](https://example.test/a.md) [protocol](//example.test/a.md) [absolute](/a.md) [suffix](other-a.md)",
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
	moved, _ := result.Staged.ReadFile(context.Background(), "a.md")
	if !contains(string(moved), "[self](a.md)") {
		t.Fatalf("moved file link not recalculated: %s", moved)
	}
	nested, _ := result.Staged.ReadFile(context.Background(), "nested/c.md")
	if !contains(string(nested), "[target](../a.md)") {
		t.Fatalf("nested link not rewritten: %s", nested)
	}
	root, _ := result.Staged.ReadFile(context.Background(), "index.md")
	if !contains(string(root), "[target](a.md)") {
		t.Fatalf("root link not rewritten: %s", root)
	}
}

func TestPlanMoveConcept_MarkdownRewriteIsDeterministic(t *testing.T) {
	// Arrange
	s := memorySource{
		"a.md":       []byte("---\ntype: thing\n---\n\nA\n"),
		"notes/c.md": []byte("---\ntype: thing\n---\n\n[inline](../a.md#part)\n[reference]: ../a.md \"Title\"\n"),
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
	_, err := parsePresentation([]byte("---\ntitle: " + string([]byte{0xff}) + "\n---\nbody\n"))

	// Assert.
	if !errors.Is(err, bundle.ErrInvalidEncoding) {
		t.Fatalf("parsePresentation() error = %v, want ErrInvalidEncoding", err)
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
