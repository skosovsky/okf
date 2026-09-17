//go:build darwin || linux

package bundle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexRecoveryAllPublishedCommitBoundary(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	arrangeIndexBatchCrashBundle(t, root)
	runIndexBatchCrashHelper(t, root, "root-commit")
	manifest := readIndexBatchManifestFixture(t, root)
	before := captureIndexRecoveryCleanupPublicTargets(t, root, manifest)

	// Act.
	err := applyIndexRecoveryCommitBoundarySharedPass(root, indexPublishHooks{})

	// Assert.
	if err != nil {
		t.Fatalf("B4 commit pass error = %v", err)
	}
	assertIndexRecoveryCleanupPublicTargetsUnchanged(t, root, before)
	assertNoIndexRecoveryCommitBoundaryManifest(t, root)
	assertNoIndexTransactionArtifacts(t, root)
}

func TestIndexRecoveryMixedRollbackBoundary(t *testing.T) {
	// Arrange.
	root, original, manifest := arrangeIndexRecoveryCommitBoundaryMixedRollback(t)

	// Act.
	err := applyIndexRecoveryCommitBoundarySharedPass(root, indexPublishHooks{})

	// Assert.
	if err != nil {
		t.Fatalf("B4 rollback pass error = %v", err)
	}
	assertFileBytes(t, filepath.Join(root, "a", indexFilename), original["a/index.md"])
	if info, statErr := os.Lstat(filepath.Join(root, "a", indexFilename)); statErr != nil ||
		!info.Mode().IsRegular() ||
		indexPreservedMode(info.Mode()) != 0o640 {
		t.Fatalf("terminal a/index.md info=%v error=%v", info, statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "b", indexFilename)); !errors.Is(
		statErr,
		os.ErrNotExist,
	) {
		t.Fatalf("terminal b/index.md error=%v, want absent", statErr)
	}
	assertFileBytes(t, filepath.Join(root, indexFilename), original[indexFilename])
	assertNoIndexRecoveryCommitBoundaryManifest(t, root)
	assertNoIndexTransactionArtifacts(t, root)
	terminal := captureIndexRecoveryCleanupPublicTargets(t, root, manifest)
	if err := recoverIndexBatchFixture(root); err != nil {
		t.Fatalf("terminal no-op restart error = %v", err)
	}
	assertIndexRecoveryCleanupPublicTargetsUnchanged(t, root, terminal)
}

func TestIndexRecoveryManifestRemovalFailureRetainsProof(t *testing.T) {
	// Arrange.
	root, _, _ := arrangeIndexRecoveryCommitBoundaryMixedRollback(t)
	removeErr := errors.New("retain manifest")
	var atBoundary map[string]documentSessionTreeEntry
	hooks := indexPublishHooks{
		beforeCleanup: func(_ string, _ *os.Root, kind, _ string) error {
			if kind != string(indexArtifactManifest) {
				return nil
			}
			atBoundary = documentSessionCaptureTree(t, root)
			return removeErr
		},
	}

	// Act.
	err := applyIndexRecoveryCommitBoundarySharedPass(root, hooks)
	after := documentSessionCaptureTree(t, root)

	// Assert.
	if !errors.Is(err, removeErr) {
		t.Fatalf("B4 manifest failure error = %v, want %v", err, removeErr)
	}
	if atBoundary == nil {
		t.Fatal("manifest removal boundary was not reached")
	}
	documentSessionAssertTreeSnapshotEqual(t, atBoundary, after)
	manifestArtifactPathForFixture(t, root)
}

func TestIndexRecoveryDriftBeforeManifestBoundaryFailsHard(t *testing.T) {
	cases := []struct {
		name           string
		selectRelative func(indexBatchManifest) string
	}{
		{
			name: "target drift",
			selectRelative: func(_ indexBatchManifest) string {
				return "a/index.md"
			},
		},
		{
			name: "peer evidence drift",
			selectRelative: func(manifest indexBatchManifest) string {
				return indexBatchManifestEntryByPath(t, manifest, "a/index.md").Anchor
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			root, _, manifest := arrangeIndexRecoveryCommitBoundaryMixedRollback(t)
			drifted := testCase.selectRelative(manifest)
			hooks := indexPublishHooks{
				beforeCleanup: func(_ string, _ *os.Root, kind, _ string) error {
					if kind != string(indexArtifactManifest) {
						return nil
					}
					return os.WriteFile(
						filepath.Join(root, filepath.FromSlash(drifted)),
						[]byte("foreign drift\n"),
						0o640,
					)
				},
			}

			// Act.
			err := applyIndexRecoveryCommitBoundarySharedPass(root, hooks)

			// Assert.
			if err == nil {
				t.Fatalf("B4 accepted %s before manifest boundary", testCase.name)
			}
			manifestArtifactPathForFixture(t, root)
			got, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(drifted)))
			if readErr != nil || !bytes.Equal(got, []byte("foreign drift\n")) {
				t.Fatalf("drifted peer changed after hard failure: data=%q error=%v", got, readErr)
			}
		})
	}
}

func arrangeIndexRecoveryCommitBoundaryMixedRollback(
	t *testing.T,
) (string, map[string][]byte, indexBatchManifest) {
	t.Helper()
	root := t.TempDir()
	writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
	writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
	before := map[string][]byte{
		"a/index.md": []byte("# A original\n"),
		"index.md":   []byte(documentSessionIndex("0.2", "Root original")),
	}
	for relative, data := range before {
		if err := os.WriteFile(
			filepath.Join(root, filepath.FromSlash(relative)),
			data,
			0o640,
		); err != nil {
			t.Fatal(err)
		}
	}
	runIndexBatchCrashHelper(t, root, "before-root")
	return root, before, readIndexBatchManifestFixture(t, root)
}

func applyIndexRecoveryCommitBoundarySharedPass(rootPath string, hooks indexPublishHooks) error {
	root, err := openRootWithoutSymlinks(rootPath)
	if err != nil {
		return err
	}
	discoveries, discoveryErr := discoverIndexTransactionNamespace(root)
	if discoveryErr != nil {
		return errors.Join(discoveryErr, root.Close())
	}
	prepared, prepareErr := prepareIndexRecoveryWithSharedInventory(
		root,
		discoveries,
		hooks,
		true,
	)
	if prepareErr != nil {
		return errors.Join(prepareErr, root.Close())
	}
	applyErr := prepared.apply()
	prepared.close()
	return errors.Join(applyErr, root.Close())
}

func assertNoIndexRecoveryCommitBoundaryManifest(t *testing.T, root string) {
	t.Helper()
	for _, relative := range indexTransactionArtifacts(t, root) {
		if strings.HasPrefix(
			filepath.Base(filepath.FromSlash(relative)),
			indexBatchManifestPrefix,
		) {
			t.Fatalf("batch manifest survived completion boundary: %s", relative)
		}
	}
}
