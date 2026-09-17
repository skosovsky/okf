//go:build darwin || linux

package bundle

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexRecoveryCreateOnlyPublishedRollback(t *testing.T) {
	// Arrange.
	root, entry := arrangeIndexRecoveryCreateOnlyRollbackPublished(t)
	if phase := inspectIndexRecoveryExistingRollbackEntry(t, root, entry.Path); phase != indexBatchV3PhasePublished {
		t.Fatalf("initial phase = %v, want PUBLISHED", phase)
	}

	// Act.
	err := applyIndexRecoveryPreNewRollbackPass(root, indexPublishHooks{})

	// Assert.
	if err != nil {
		t.Fatalf("B3 recovery pass error = %v", err)
	}
	assertIndexRecoveryCreateOnlyRollbackVacatedEvidence(t, root, entry)
}

func TestIndexRecoveryCreateOnlyRollbackDiscardCrashCuts(t *testing.T) {
	cases := []string{
		"discard-post-rename",
		"discard-pre-sync",
		"discard-post-sync",
	}
	for _, point := range cases {
		t.Run(point, func(t *testing.T) {
			// Arrange.
			root, entry := arrangeIndexRecoveryCreateOnlyRollbackPublished(t)

			// Act.
			runIndexRecoveryCreateOnlyRollbackCrash(t, root, point)
			phase := inspectIndexRecoveryExistingRollbackEntry(t, root, entry.Path)
			err := applyIndexRecoveryPreNewRollbackPass(root, indexPublishHooks{})

			// Assert.
			if phase != indexBatchV3PhaseCreateRollbackVacated {
				t.Fatalf("phase after %s = %v, want ROLLBACK_VACATED", point, phase)
			}
			if err != nil {
				t.Fatalf("B3 resume after %s error = %v", point, err)
			}
			if _, statErr := os.Lstat(
				filepath.Join(root, filepath.FromSlash(entry.Path)),
			); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("Lstat(%s) error = %v, want absent", entry.Path, statErr)
			}
			assertNoIndexTransactionArtifacts(t, root)
		})
	}
}

func TestIndexRecoveryCreateOnlyRollbackMissingTargetInstallAndDiscardFailsClosed(t *testing.T) {
	// Arrange.
	root, entry := arrangeIndexRecoveryCreateOnlyRollbackPublished(t)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(entry.Path))); err != nil {
		t.Fatal(err)
	}
	before := documentSessionCaptureTree(t, root)

	// Act.
	err := applyIndexRecoveryPreNewRollbackPass(root, indexPublishHooks{})
	after := documentSessionCaptureTree(t, root)

	// Assert.
	if err == nil {
		t.Fatal("B3 recovery accepted missing target/N/D")
	}
	documentSessionAssertTreeSnapshotEqual(t, before, after)
}

func TestIndexRecoveryCreateOnlyRollbackSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_INDEX_V3_B3_RECOVERY_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	point := os.Getenv("OKF_INDEX_V3_B3_RECOVERY_POINT")
	crash := func() {
		os.Exit(87)
	}
	hooks := indexPublishHooks{
		afterVacateRename: func(relative string, _ *os.Root, _, claim string) error {
			if point == "discard-post-rename" &&
				relative == "a/index.md" &&
				strings.HasPrefix(claim, indexDiscardPrefix) {
				crash()
			}
			return nil
		},
		directorySync: func(relative, kind, _ string, parent *os.Root) error {
			if point == "discard-pre-sync" &&
				relative == "a/index.md" &&
				kind == string(indexArtifactDiscard) {
				crash()
			}
			return syncPublicationDirectoryPlatform(parent)
		},
		afterVacate: func(relative string, _ *os.Root, _, claim string) error {
			if point == "discard-post-sync" &&
				relative == "a/index.md" &&
				strings.HasPrefix(claim, indexDiscardPrefix) {
				crash()
			}
			return nil
		},
	}
	if err := applyIndexRecoveryPreNewRollbackPass(root, hooks); err != nil {
		t.Fatal(err)
	}
	t.Fatal("B3 recovery crash point was not reached")
}

func arrangeIndexRecoveryCreateOnlyRollbackPublished(t *testing.T) (string, indexBatchManifestEntry) {
	t.Helper()
	root := newIndexV3CreateOnlyRollbackBundle(t)
	runIndexBatchCrashHelper(t, root, "before-root")
	manifest := readIndexBatchManifestFixture(t, root)
	entry := indexBatchManifestEntryByPath(t, manifest, "a/index.md")
	if entry.OldPresent || entry.Anchor != "" {
		t.Fatalf("create-only manifest binds old evidence: %+v", entry)
	}
	return root, entry
}

func runIndexRecoveryCreateOnlyRollbackCrash(t *testing.T, root, point string) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestIndexRecoveryCreateOnlyRollbackSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_INDEX_V3_B3_RECOVERY_ROOT="+root,
		"OKF_INDEX_V3_B3_RECOVERY_POINT="+point,
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 87 {
		t.Fatalf("B3 recovery helper %s error = %v, output:\n%s", point, err, output.String())
	}
}

func assertIndexRecoveryCreateOnlyRollbackVacatedEvidence(
	t *testing.T,
	root string,
	entry indexBatchManifestEntry,
) {
	t.Helper()
	stage, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Stage)))
	if err != nil {
		t.Fatal(err)
	}
	discard, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Discard)))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(stage, discard) {
		t.Fatal("create-only discard token does not alias stage")
	}
	if _, err := os.Lstat(
		filepath.Join(root, filepath.FromSlash(entry.Path)),
	); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(%s) error = %v, want absent", entry.Path, err)
	}
	if _, err := os.Lstat(
		filepath.Join(root, filepath.FromSlash(entry.NewInstall)),
	); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(%s) error = %v, want consumed", entry.NewInstall, err)
	}
	if entry.Anchor != "" {
		t.Fatalf("create-only manifest unexpectedly binds anchor %q", entry.Anchor)
	}
	if _, err := os.Lstat(
		filepath.Join(root, filepath.FromSlash(entry.RestoreInstall)),
	); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(%s) error = %v, want no restore install", entry.RestoreInstall, err)
	}
	manifestArtifactPathForFixture(t, root)
	rootData, err := os.ReadFile(filepath.Join(root, indexFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rootData, []byte(documentSessionIndex("0.2", "Root original"))) {
		t.Fatal("unrelated root target changed during create-only rollback")
	}
}
