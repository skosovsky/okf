package bundle

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIndexBatchManifestCanonicallyBindsCompleteOrderedMembership(t *testing.T) {
	t.Parallel()

	// Arrange.
	before := map[string][]byte{
		"a/index.md": []byte("# A original\n"),
		"index.md":   []byte("# Root original\n"),
	}
	oldModes := map[string]fs.FileMode{
		"a/index.md": 0o600,
		"index.md":   0o604,
	}
	arrange := func(t *testing.T, root string) {
		t.Helper()
		writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
		writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
		for relative, data := range before {
			filename := filepath.Join(root, filepath.FromSlash(relative))
			if err := os.WriteFile(filename, data, oldModes[relative]); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filename, oldModes[relative]); err != nil {
				t.Fatal(err)
			}
		}
	}
	after := generatedIndexOracle(t, arrange, "a/index.md", "b/index.md", "index.md")
	rootPath := t.TempDir()
	arrange(t, rootPath)
	var manifest indexBatchManifest
	var manifestData []byte
	manifestName := ""
	hooks := indexPublishHooks{
		afterBatchManifestDurable: func(root *os.Root, name string) error {
			data, err := root.ReadFile(name)
			if err != nil {
				return err
			}
			manifestName = name
			manifestData = append([]byte(nil), data...)
			return json.Unmarshal(data, &manifest)
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(rootPath, nil, "", hooks)

	// Assert.
	if err != nil || len(written) != len(after) {
		t.Fatalf("regenerateIndexesWithHooks() = %#v, %v", written, err)
	}
	if manifestName == "" || manifestName != indexBatchManifestName(manifestData) {
		t.Fatalf("manifest name=%q, want self-auth name %q", manifestName, indexBatchManifestName(manifestData))
	}
	canonical, marshalErr := json.Marshal(manifest)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	canonical = append(canonical, '\n')
	if !reflect.DeepEqual(manifestData, canonical) {
		t.Fatalf("manifest bytes=%q, want canonical %q", manifestData, canonical)
	}
	wantPaths := []string{"a/index.md", "b/index.md", "index.md"}
	if manifest.Protocol != indexBatchManifestProtocol ||
		manifest.Version != indexBatchManifestVersion ||
		len(manifest.Entries) != len(wantPaths) {
		t.Fatalf("manifest=%+v, want protocol/version and %d entries", manifest, len(wantPaths))
	}
	rootEntries := 0
	for ordinal, entry := range manifest.Entries {
		if entry.Path != wantPaths[ordinal] {
			t.Fatalf("manifest entry[%d].Path=%q, want %q", ordinal, entry.Path, wantPaths[ordinal])
		}
		if entry.Path == indexFilename {
			rootEntries++
		}
		if entry.NewDigest != indexBatchPayloadDigest(after[entry.Path]) {
			t.Fatalf("%s new digest=%q, want exact payload digest", entry.Path, entry.NewDigest)
		}
		info, statErr := os.Lstat(filepath.Join(rootPath, filepath.FromSlash(entry.Path)))
		if statErr != nil {
			t.Fatal(statErr)
		}
		if entry.NewMode != uint32(indexPreservedMode(info.Mode())) {
			t.Fatalf("%s new mode=%04o, want %04o", entry.Path, entry.NewMode, indexPreservedMode(info.Mode()))
		}
		if _, ok := parseIndexArtifactName(filepath.Base(entry.Stage), indexArtifactStage); !ok {
			t.Fatalf("%s stage=%q is not an exact canonical stage name", entry.Path, entry.Stage)
		}
		if filepath.ToSlash(filepath.Dir(entry.Stage)) != filepath.ToSlash(filepath.Dir(entry.Path)) {
			t.Fatalf("%s stage %q escapes target directory", entry.Path, entry.Stage)
		}
		old, existed := before[entry.Path]
		if entry.OldPresent != existed {
			t.Fatalf("%s old presence=%t, want %t", entry.Path, entry.OldPresent, existed)
		}
		if !existed {
			if entry.OldDigest != "" || entry.OldMode != 0 ||
				entry.Backup != "" || entry.Claim != "" ||
				entry.Anchor != "" || entry.Witness != "" {
				t.Fatalf("%s absent-old evidence is not empty: %+v", entry.Path, entry)
			}
			continue
		}
		if entry.OldDigest != indexBatchPayloadDigest(old) ||
			entry.OldMode != uint32(oldModes[entry.Path]) {
			t.Fatalf("%s old contract=%+v, want digest/mode for exact preimage", entry.Path, entry)
		}
		if _, ok := parseIndexArtifactName(filepath.Base(entry.Backup), indexArtifactBackup); !ok {
			t.Fatalf("%s backup=%q is not canonical", entry.Path, entry.Backup)
		}
		if _, ok := parseIndexArtifactName(filepath.Base(entry.Claim), indexArtifactRestore); !ok {
			t.Fatalf("%s claim=%q is not canonical", entry.Path, entry.Claim)
		}
		if _, ok := parseIndexArtifactName(filepath.Base(entry.Anchor), indexArtifactAnchor); !ok {
			t.Fatalf("%s anchor=%q is not canonical", entry.Path, entry.Anchor)
		}
		if _, ok := parseIndexArtifactName(filepath.Base(entry.Witness), indexArtifactWitness); !ok {
			t.Fatalf("%s witness=%q is not canonical", entry.Path, entry.Witness)
		}
		for _, artifact := range []string{entry.Backup, entry.Claim, entry.Anchor, entry.Witness} {
			if filepath.ToSlash(filepath.Dir(artifact)) != filepath.ToSlash(filepath.Dir(entry.Path)) {
				t.Fatalf("%s artifact %q escapes target directory", entry.Path, artifact)
			}
		}
	}
	if rootEntries != 1 {
		t.Fatalf("root manifest entries=%d, want exactly one", rootEntries)
	}
	missingWitness := manifest
	missingWitness.Entries = append([]indexBatchManifestEntry(nil), manifest.Entries...)
	missingWitness.Entries[0].Witness = ""
	if err := validateIndexBatchManifestShape(missingWitness); err == nil {
		t.Fatal("manifest without an original identity witness was accepted")
	}
	wrongKindWitness := manifest
	wrongKindWitness.Entries = append([]indexBatchManifestEntry(nil), manifest.Entries...)
	wrongKindWitness.Entries[0].Witness = wrongKindWitness.Entries[0].Backup
	if err := validateIndexBatchManifestShape(wrongKindWitness); err == nil {
		t.Fatal("manifest whose witness path names another artifact kind was accepted")
	}
	assertNoIndexTransactionArtifacts(t, rootPath)
}

func TestIndexBatchPublishesRootLastAndUsesCanonicalProofLastCleanup(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
	writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
	var published []string
	var cleanup []string
	var canonicalPlan []string
	hooks := indexPublishHooks{
		afterInstallLink: func(relative string, _ *os.Root, _, _ string) error {
			published = append(published, relative)
			return nil
		},
		afterRootCommit: func(relative string, _ *os.Root, _, _ string) error {
			if relative == indexFilename {
				canonicalPlan = indexBatchCanonicalCleanupTrace(t, root)
			}
			return nil
		},
		afterCleanup: func(relative string, _ *os.Root, kind, _ string) error {
			cleanup = append(cleanup, relative+":"+kind)
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	wantPublished := []string{"a/index.md", "b/index.md", "index.md"}
	if err != nil || !reflect.DeepEqual(published, wantPublished) ||
		len(written) != len(wantPublished) {
		t.Fatalf("published=%#v written=%#v error=%v, want root last", published, written, err)
	}
	if len(canonicalPlan) == 0 ||
		!reflect.DeepEqual(cleanup, canonicalPlan) {
		t.Fatalf(
			"cleanup order=%#v, want shared canonical plan %#v",
			cleanup,
			canonicalPlan,
		)
	}
	manifestOrdinal := -1
	for ordinal, step := range cleanup {
		if strings.HasSuffix(step, ":"+string(indexArtifactManifest)) {
			manifestOrdinal = ordinal
			break
		}
	}
	if manifestOrdinal < 0 ||
		manifestOrdinal == len(cleanup)-1 ||
		cleanup[len(cleanup)-1] !=
			indexFilename+":"+string(indexArtifactStage) {
		t.Fatalf(
			"cleanup order=%#v, want M boundary followed by proof suffix with root S last",
			cleanup,
		)
	}
	assertNoIndexTransactionArtifacts(t, root)
}

func TestIndexBatchRollbackNeverVacatesMutatedPublishedTargetOrContinues(t *testing.T) {
	t.Parallel()

	// Arrange.
	before := map[string][]byte{
		"a/index.md": []byte("# A original\n"),
		"b/index.md": []byte("# B original\n"),
		"index.md":   []byte(documentSessionIndex("0.2", "Root original")),
	}
	arrange := func(t *testing.T, root string) {
		t.Helper()
		writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
		writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
		for relative, data := range before {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), data, 0o640); err != nil {
				t.Fatal(err)
			}
		}
	}
	after := generatedIndexOracle(t, arrange, "a/index.md", "b/index.md", "index.md")
	root := t.TempDir()
	arrange(t, root)
	injected := errors.New("force rollback after foreign mutation")
	mutated := false
	hooks := indexPublishHooks{
		afterPublish: func(relative string, _ int) error {
			if relative != "b/index.md" || mutated {
				return nil
			}
			mutated = true
			if err := os.WriteFile(
				filepath.Join(root, "b", indexFilename),
				[]byte("foreign mutation of published target\n"),
				0o640,
			); err != nil {
				return err
			}
			return injected
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !mutated || written != nil || !errors.Is(err, injected) {
		t.Fatalf("mutated=%t written=%#v error=%v, want failed precommit rollback", mutated, written, err)
	}
	assertFileBytes(t, filepath.Join(root, "b", indexFilename), []byte("foreign mutation of published target\n"))
	assertFileBytes(t, filepath.Join(root, "a", indexFilename), after["a/index.md"])
	assertFileBytes(t, filepath.Join(root, indexFilename), before["index.md"])
	if artifacts := indexTransactionArtifacts(t, root); len(artifacts) == 0 {
		t.Fatal("failed exact-identity rollback lost manifest-bound evidence")
	}
}
