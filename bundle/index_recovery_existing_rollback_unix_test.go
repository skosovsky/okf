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

func TestIndexRecoveryExistingPublishedRollback(t *testing.T) {
	// Arrange.
	root, before, entry := arrangeIndexRecoveryExistingRollbackPublished(t)
	if phase := inspectIndexRecoveryExistingRollbackEntry(t, root, entry.Path); phase != indexBatchV3PhasePublished {
		t.Fatalf("initial phase = %v, want PUBLISHED", phase)
	}

	// Act.
	err := applyIndexRecoveryPreNewRollbackPass(root, indexPublishHooks{})

	// Assert.
	if err != nil {
		t.Fatalf("B2 recovery pass error = %v", err)
	}
	assertIndexRecoveryExistingRollbackRestoredEvidence(t, root, entry, before[entry.Path])
}

func TestIndexRecoveryExistingResumeStatesAndCrashCuts(t *testing.T) {
	cases := []struct {
		point string
		phase indexBatchV3RecoveryPhase
	}{
		{point: "anchor-durable", phase: indexBatchV3PhaseRollbackAnchored},
		{point: "restore-install-durable", phase: indexBatchV3PhaseRollbackReady},
		{point: "discard-post-rename", phase: indexBatchV3PhasePreRestore},
		{point: "discard-post-sync", phase: indexBatchV3PhasePreRestore},
		{point: "restore-install-post-rename", phase: indexBatchV3PhaseRestored},
		{point: "restore-install-post-sync", phase: indexBatchV3PhaseRestored},
	}
	seen := make(map[indexBatchV3RecoveryPhase]bool)
	for _, testCase := range cases {
		t.Run(testCase.point, func(t *testing.T) {
			// Arrange.
			root, before, entry := arrangeIndexRecoveryExistingRollbackPublished(t)

			// Act.
			runIndexRecoveryExistingRollbackCrash(t, root, testCase.point)
			phase := inspectIndexRecoveryExistingRollbackEntry(t, root, entry.Path)
			seen[phase] = true
			err := applyIndexRecoveryPreNewRollbackPass(root, indexPublishHooks{})

			// Assert.
			if phase != testCase.phase {
				t.Fatalf("phase after %s = %v, want %v", testCase.point, phase, testCase.phase)
			}
			if err != nil {
				t.Fatalf("B2 resume after %s error = %v", testCase.point, err)
			}
			if testCase.phase == indexBatchV3PhaseRestored {
				assertFileBytes(
					t,
					filepath.Join(root, filepath.FromSlash(entry.Path)),
					before[entry.Path],
				)
				assertNoIndexTransactionArtifacts(t, root)
			} else {
				assertIndexRecoveryExistingRollbackRestoredEvidence(t, root, entry, before[entry.Path])
			}
		})
	}
	for _, phase := range []indexBatchV3RecoveryPhase{
		indexBatchV3PhaseRollbackAnchored,
		indexBatchV3PhaseRollbackReady,
		indexBatchV3PhasePreRestore,
		indexBatchV3PhaseRestored,
	} {
		if !seen[phase] {
			t.Errorf("resume phase %v was not exercised", phase)
		}
	}
}

func TestIndexRecoveryExistingRollbackSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_INDEX_V3_B2_RECOVERY_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	point := os.Getenv("OKF_INDEX_V3_B2_RECOVERY_POINT")
	crash := func() {
		os.Exit(87)
	}
	restoreInstallDirectorySyncs := 0
	hooks := indexPublishHooks{
		afterArtifactDirectorySync: func(relative, kind, _ string, _ *os.Root) error {
			if relative != "a/index.md" {
				return nil
			}
			if point == "anchor-durable" && kind == string(indexArtifactAnchor) {
				crash()
			}
			if kind != string(indexArtifactRestoreInstall) {
				return nil
			}
			current := restoreInstallDirectorySyncs
			restoreInstallDirectorySyncs++
			if point == "restore-install-durable" && current == 0 {
				crash()
			}
			if point == "restore-install-post-sync" && current == 1 {
				crash()
			}
			return nil
		},
		afterVacateRename: func(relative string, _ *os.Root, _, claim string) error {
			if point == "discard-post-rename" &&
				relative == "a/index.md" &&
				strings.HasPrefix(claim, indexDiscardPrefix) {
				crash()
			}
			return nil
		},
		afterVacate: func(relative string, _ *os.Root, _, claim string) error {
			if point == "discard-post-sync" &&
				relative == "a/index.md" &&
				strings.HasPrefix(claim, indexDiscardPrefix) {
				crash()
			}
			return nil
		},
		afterRestoreLink: func(relative string, _ *os.Root, source, _ string) error {
			if point == "restore-install-post-rename" &&
				relative == "a/index.md" &&
				strings.HasPrefix(source, indexRestoreInstallPrefix) {
				crash()
			}
			return nil
		},
	}
	if err := applyIndexRecoveryPreNewRollbackPass(root, hooks); err != nil {
		t.Fatal(err)
	}
	t.Fatal("B2 recovery crash point was not reached")
}

func arrangeIndexRecoveryExistingRollbackPublished(
	t *testing.T,
) (string, map[string][]byte, indexBatchManifestEntry) {
	t.Helper()
	root := t.TempDir()
	before, _ := arrangeIndexBatchCrashBundle(t, root)
	runIndexBatchCrashHelper(t, root, "mid-vacate")
	if err := applyIndexRecoveryPreNewRollbackPass(root, indexPublishHooks{}); err != nil {
		t.Fatalf("B1 setup pass error = %v", err)
	}
	manifest := readIndexBatchManifestFixture(t, root)
	return root, before, indexBatchManifestEntryByPath(t, manifest, "a/index.md")
}

func runIndexRecoveryExistingRollbackCrash(t *testing.T, root, point string) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestIndexRecoveryExistingRollbackSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_INDEX_V3_B2_RECOVERY_ROOT="+root,
		"OKF_INDEX_V3_B2_RECOVERY_POINT="+point,
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 87 {
		t.Fatalf("B2 recovery helper %s error = %v, output:\n%s", point, err, output.String())
	}
}

func assertIndexRecoveryExistingRollbackRestoredEvidence(
	t *testing.T,
	root string,
	entry indexBatchManifestEntry,
	old []byte,
) {
	t.Helper()
	stat := func(relative string) os.FileInfo {
		t.Helper()
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("Lstat(%s) error = %v", relative, err)
		}
		return info
	}
	stage := stat(entry.Stage)
	discard := stat(entry.Discard)
	if !os.SameFile(stage, discard) {
		t.Fatal("retained discard token does not alias stage")
	}
	anchor := stat(entry.Anchor)
	target := stat(entry.Path)
	if !os.SameFile(anchor, target) {
		t.Fatal("restored target does not alias independent rollback anchor")
	}
	if os.SameFile(anchor, stage) {
		t.Fatal("rollback anchor aliases staged new payload")
	}
	if _, err := os.Lstat(
		filepath.Join(root, filepath.FromSlash(entry.NewInstall)),
	); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(%s) error = %v, want consumed", entry.NewInstall, err)
	}
	if _, err := os.Lstat(
		filepath.Join(root, filepath.FromSlash(entry.RestoreInstall)),
	); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(%s) error = %v, want consumed", entry.RestoreInstall, err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, old) {
		t.Fatalf("restored target = %q, want %q", got, old)
	}
	manifestArtifactPathForFixture(t, root)
}
