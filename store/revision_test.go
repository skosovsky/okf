package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"testing"
)

type countingHashAlgorithm struct{ newCalls int }

func (a *countingHashAlgorithm) Name() string { return "counting-sha256" }
func (a *countingHashAlgorithm) New() hash.Hash {
	a.newCalls++
	return sha256.New()
}

func TestRevisionFromManifest_IsOrderIndependentAndPathSensitive(t *testing.T) {
	// Arrange.
	forward := []ManifestEntry{{Path: "a.md", Content: []byte("one")}, {Path: "nested/b.md", Content: []byte("two")}}
	reverse := []ManifestEntry{{Path: "nested/b.md", Content: []byte("two")}, {Path: "a.md", Content: []byte("one")}}

	// Act.
	first, err := RevisionFromManifest(forward)
	second, secondErr := RevisionFromManifest(reverse)
	changed, changedErr := RevisionFromManifest([]ManifestEntry{{Path: "b.md", Content: []byte("one")}, {Path: "nested/b.md", Content: []byte("two")}})

	// Assert.
	if err != nil || secondErr != nil || changedErr != nil {
		t.Fatalf("RevisionFromManifest errors: %v, %v, %v", err, secondErr, changedErr)
	}
	if first != second {
		t.Fatalf("same manifest in different order: %s != %s", first, second)
	}
	if first == changed {
		t.Fatal("path change must change revision")
	}
	if !first.Valid() {
		t.Fatalf("revision must validate: %s", first)
	}
}

func TestRevisionFromManifest_RejectsAmbiguousPaths(t *testing.T) {
	// Arrange.
	tests := [][]ManifestEntry{
		{{Path: "a.md"}, {Path: "a.md"}},
		{{Path: "../a.md"}},
		{{Path: "a\\b.md"}},
		{{Path: "/a.md"}},
		{{Path: ".okf/revision"}},
		{{Path: "bad\x00.bin"}},
		{{Path: string([]byte{'b', 'a', 'd', 0xff})}},
	}

	for _, entries := range tests {
		// Act.
		_, err := RevisionFromManifest(entries)

		// Assert.
		if !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("entries %#v: expected invalid manifest, got %v", entries, err)
		}
	}
}

func TestManifestPath_ReservesOnlyRootMetadataDirectory(t *testing.T) {
	// Arrange.
	tests := []struct {
		path  string
		valid bool
	}{
		{path: ".okf"},
		{path: ".okf/revision"},
		{path: ".okf-name", valid: true},
		{path: "nested/.okf/file.bin", valid: true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			// Act.
			_, err := NewManifest([]ManifestEntry{{Path: tt.path, Content: []byte("asset")}})

			// Assert.
			if (err == nil) != tt.valid {
				t.Fatalf("NewManifest(%q) error = %v, valid = %t", tt.path, err, tt.valid)
			}
		})
	}
}

func TestRevisionFromManifest_AcceptsUnicodeAndPunctuationAssetPaths(t *testing.T) {
	// Arrange.
	entries := []ManifestEntry{{Path: "assets/привет, world! (v2).bin", Content: []byte("asset")}}

	// Act.
	revision, err := RevisionFromManifest(entries)

	// Assert.
	if err != nil || !revision.Valid() {
		t.Fatalf("RevisionFromManifest() = %q, %v", revision, err)
	}
}

func TestParseRevision_RejectsNonCanonicalValue(t *testing.T) {
	// Arrange.
	value := "sha256:" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	// Act.
	_, err := ParseRevision(value)

	// Assert.
	if !errors.Is(err, ErrInvalidRevision) {
		t.Fatalf("expected invalid revision, got %v", err)
	}
}

func TestRevisionIsZero(t *testing.T) {
	// Arrange.
	var zero Revision

	// Act.
	isZero := zero.IsZero()

	// Assert.
	if !isZero || zero.Valid() {
		t.Fatal("zero revision must be distinguishable and invalid")
	}
}

func TestManifest_ReusesDigestAcrossRenameAndDefensiveCopies(t *testing.T) {
	// Arrange.
	algorithm := &countingHashAlgorithm{}
	manifest, err := NewManifestWithAlgorithm([]ManifestEntry{{Path: "a.md", Content: []byte("one")}, {Path: "b.md", Content: []byte("two")}}, algorithm)
	if err != nil {
		t.Fatal(err)
	}
	beforeRename := algorithm.newCalls

	// Act.
	renamed, err := manifest.Rename("a.md", "nested/a.md")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := renamed.Revision()
	if err != nil {
		t.Fatal(err)
	}
	digests := renamed.Digests()
	digests["b.md"] = "tampered"

	// Assert. Revision allocates one manifest hash; rename does not hash either
	// unchanged content value. The map returned to callers is also detached.
	if algorithm.newCalls-beforeRename != 1 {
		t.Fatalf("hash constructions after rename = %d, want manifest only", algorithm.newCalls-beforeRename)
	}
	if revision.String()[:len("counting-sha256:")] != "counting-sha256:" {
		t.Fatalf("algorithm qualifier lost: %s", revision)
	}
	if got, _ := renamed.Digest("b.md"); got == "tampered" {
		t.Fatal("manifest leaked digest map ownership")
	}
}

type hostileHash struct {
	hash.Hash
	write func([]byte) (int, error)
}

func (h hostileHash) Write(p []byte) (int, error) { return h.write(p) }

type panickingHash struct {
	hash.Hash
	panicWrite bool
	panicSum   bool
}

func (h panickingHash) Write(p []byte) (int, error) {
	if h.panicWrite {
		panic("Write")
	}
	return h.Hash.Write(p)
}

func (h panickingHash) Sum(b []byte) []byte {
	if h.panicSum {
		panic("Sum")
	}
	return h.Hash.Sum(b)
}

type hostileHashAlgorithm struct {
	new func() hash.Hash
}

func (a hostileHashAlgorithm) Name() string   { return "hostile-v1" }
func (a hostileHashAlgorithm) New() hash.Hash { return a.new() }

type hostileContextHashAlgorithm struct {
	hostileHashAlgorithm
	newContext func() (hash.Hash, error)
}

type panickingHashAlgorithm struct {
	panicName, panicContext bool
}

func (a panickingHashAlgorithm) Name() string {
	if a.panicName {
		panic("Name")
	}
	return "panic-safe-v1"
}
func (a panickingHashAlgorithm) New() hash.Hash {
	return sha256.New()
}
func (a panickingHashAlgorithm) NewContext(ctx context.Context) (hash.Hash, error) {
	if a.panicContext {
		panic("NewContext")
	}
	return sha256.New(), ctx.Err()
}

type panickingNewOnlyHashAlgorithm struct{}

func (panickingNewOnlyHashAlgorithm) Name() string { return "panic-new-v1" }
func (panickingNewOnlyHashAlgorithm) New() hash.Hash {
	panic("New")
}

func TestHashAlgorithmPanicsReturnStableErrors(t *testing.T) {
	// Arrange.
	tests := []HashAlgorithm{
		panickingHashAlgorithm{panicName: true},
		panickingNewOnlyHashAlgorithm{},
		panickingHashAlgorithm{panicContext: true},
	}

	for _, algorithm := range tests {
		// Act.
		validateErr := ValidateHashAlgorithm(algorithm)
		_, manifestErr := NewManifestWithAlgorithm([]ManifestEntry{{Path: "a.md", Content: []byte("one")}}, algorithm)

		// Assert.
		if !errors.Is(validateErr, ErrInvalidHashAlgorithm) || !errors.Is(manifestErr, ErrInvalidHashAlgorithm) || !errors.Is(manifestErr, ErrInvalidManifest) {
			t.Fatalf("algorithm %#v: Validate=%v NewManifest=%v", algorithm, validateErr, manifestErr)
		}
	}
}

func (a hostileContextHashAlgorithm) NewContext(context.Context) (hash.Hash, error) {
	return a.newContext()
}

func TestManifestHashWritesRejectShortWritesAndErrors(t *testing.T) {
	// Arrange.
	tests := []struct {
		name  string
		write func([]byte) (int, error)
		want  error
	}{
		{name: "zero nil", write: func([]byte) (int, error) { return 0, nil }, want: io.ErrShortWrite},
		{name: "partial nil", write: func(p []byte) (int, error) { return len(p) - 1, nil }, want: io.ErrShortWrite},
		{name: "error", write: func([]byte) (int, error) { return 0, errors.New("hostile write") }, want: io.ErrShortWrite},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			algorithm := hostileHashAlgorithm{new: func() hash.Hash { return hostileHash{Hash: sha256.New(), write: tt.write} }}

			// Act.
			_, err := RevisionFromManifestWithAlgorithm([]ManifestEntry{{Path: "a.md", Content: []byte("one")}}, algorithm)

			// Assert.
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestManifestRevisionRejectsLengthPrefixedShortWrite(t *testing.T) {
	// Arrange. Content hashing succeeds; only the revision framing writer fails.
	calls := 0
	algorithm := hostileHashAlgorithm{new: func() hash.Hash {
		calls++
		if calls == 1 {
			return sha256.New()
		}
		return hostileHash{Hash: sha256.New(), write: func([]byte) (int, error) { return 0, nil }}
	}}
	manifest, err := NewManifestWithAlgorithm([]ManifestEntry{{Path: "a.md", Content: []byte("one")}}, algorithm)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	_, err = manifest.Revision()

	// Assert.
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v, want io.ErrShortWrite", err)
	}
}

func TestManifestHashConstructorNilReturnsStableError(t *testing.T) {
	// Arrange.
	algorithm := hostileHashAlgorithm{new: func() hash.Hash { return nil }}
	contextual := hostileContextHashAlgorithm{hostileHashAlgorithm: hostileHashAlgorithm{new: func() hash.Hash { return sha256.New() }}, newContext: func() (hash.Hash, error) { return nil, nil }}

	// Act.
	_, newErr := NewManifestWithAlgorithm([]ManifestEntry{{Path: "a.md", Content: []byte("one")}}, algorithm)
	_, contextErr := NewManifestContext(context.Background(), []ManifestEntry{{Path: "a.md", Content: []byte("one")}}, contextual)

	// Assert.
	if !errors.Is(newErr, ErrInvalidHashAlgorithm) || !errors.Is(contextErr, ErrInvalidHashAlgorithm) {
		t.Fatalf("nil constructors must return stable errors: New=%v NewContext=%v", newErr, contextErr)
	}
}

func TestManifestHashPanicsReturnStableErrors(t *testing.T) {
	// Arrange.
	tests := []struct {
		name        string
		panicWrite  bool
		panicSum    bool
		framingOnly bool
	}{
		{name: "content write", panicWrite: true},
		{name: "content sum", panicSum: true},
		{name: "framing write", panicWrite: true, framingOnly: true},
		{name: "framing sum", panicSum: true, framingOnly: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			calls := 0
			algorithm := hostileHashAlgorithm{new: func() hash.Hash {
				calls++
				if tt.framingOnly && calls == 1 {
					return sha256.New()
				}
				return panickingHash{Hash: sha256.New(), panicWrite: tt.panicWrite, panicSum: tt.panicSum}
			}}

			// Act.
			_, err := RevisionFromManifestWithAlgorithm([]ManifestEntry{{Path: "a.md", Content: []byte("one")}}, algorithm)

			// Assert.
			if !errors.Is(err, ErrInvalidHashAlgorithm) || !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("RevisionFromManifestWithAlgorithm() error = %v", err)
			}
		})
	}
}

func TestManifestPutContextHashPanicReturnsStableErrors(t *testing.T) {
	// Arrange.
	manifest, err := NewManifestWithAlgorithm([]ManifestEntry{{Path: "a.md", Content: []byte("one")}}, hostileHashAlgorithm{new: func() hash.Hash { return sha256.New() }})
	if err != nil {
		t.Fatal(err)
	}
	manifest.algorithm = hostileHashAlgorithm{new: func() hash.Hash { return panickingHash{Hash: sha256.New(), panicWrite: true} }}

	// Act.
	_, err = manifest.PutContext(context.Background(), "b.md", []byte("two"))

	// Assert.
	if !errors.Is(err, ErrInvalidHashAlgorithm) || !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("PutContext() error = %v", err)
	}
}
