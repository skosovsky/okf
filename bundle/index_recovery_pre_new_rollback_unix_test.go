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

func TestIndexRecoveryPreNewVacatedRollback(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	before, _ := arrangeIndexBatchCrashBundle(t, root)
	runIndexBatchCrashHelper(t, root, "mid-vacate")
	manifest := readIndexBatchManifestFixture(t, root)
	entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")

	// Act.
	err := applyIndexRecoveryPreNewRollbackPass(root, indexPublishHooks{})

	// Assert.
	if err != nil {
		t.Fatalf("recoverIndexBatchFixture() error = %v", err)
	}
	assertIndexRecoveryPreNewRollbackRestoredEvidence(t, root, entry, before[entry.Path])
}

func TestIndexRecoveryPreNewRollbackCrashCuts(t *testing.T) {
	cases := []string{
		"anchor-durable",
		"restore-install-durable",
		"restore-install-post-rename",
		"restore-install-post-sync",
	}
	for _, point := range cases {
		t.Run(point, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			before, _ := arrangeIndexBatchCrashBundle(t, root)
			runIndexBatchCrashHelper(t, root, "mid-vacate")
			manifest := readIndexBatchManifestFixture(t, root)
			entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")

			// Act.
			runIndexRecoveryPreNewRollbackCrash(t, root, point)
			err := applyIndexRecoveryPreNewRollbackPass(root, indexPublishHooks{})

			// Assert.
			if err != nil {
				t.Fatalf("recoverIndexBatchFixture() after %s error = %v", point, err)
			}
			assertIndexRecoveryPreNewRollbackRestoredEvidence(t, root, entry, before[entry.Path])
		})
	}
}

func TestIndexRecoveryPreNewRollbackSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_INDEX_V3_B1_RECOVERY_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	point := os.Getenv("OKF_INDEX_V3_B1_RECOVERY_POINT")
	crash := func() {
		os.Exit(87)
	}
	restoreInstallDirectorySyncs := 0
	hooks := indexPublishHooks{
		afterArtifactDirectorySync: func(relative, kind, _ string, _ *os.Root) error {
			if relative != "b/index.md" {
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
		afterRestoreLink: func(relative string, _ *os.Root, _, _ string) error {
			if point == "restore-install-post-rename" && relative == "b/index.md" {
				crash()
			}
			return nil
		},
	}
	if err := applyIndexRecoveryPreNewRollbackPass(root, hooks); err != nil {
		t.Fatal(err)
	}
	t.Fatal("B1 recovery crash point was not reached")
}

func applyIndexRecoveryPreNewRollbackPass(rootPath string, hooks indexPublishHooks) error {
	root, err := openRootWithoutSymlinks(rootPath)
	if err != nil {
		return err
	}
	discoveries, discoveryErr := discoverIndexTransactionNamespace(root)
	if discoveryErr != nil {
		return errors.Join(discoveryErr, root.Close())
	}
	recoveryErr := inspectIndexRecoveryDiscoveries(root, discoveries, hooks)
	return errors.Join(recoveryErr, root.Close())
}

func runIndexRecoveryPreNewRollbackCrash(t *testing.T, root, point string) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestIndexRecoveryPreNewRollbackSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_INDEX_V3_B1_RECOVERY_ROOT="+root,
		"OKF_INDEX_V3_B1_RECOVERY_POINT="+point,
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 87 {
		t.Fatalf("B1 recovery helper %s error = %v, output:\n%s", point, err, output.String())
	}
}

func assertIndexRecoveryPreNewRollbackRestoredEvidence(
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
	newInstall := stat(entry.NewInstall)
	if !os.SameFile(stage, newInstall) {
		t.Fatal("retained new install token does not alias stage")
	}
	anchor := stat(entry.Anchor)
	target := stat(entry.Path)
	if !os.SameFile(anchor, target) {
		t.Fatal("restored target does not alias independent rollback anchor")
	}
	if os.SameFile(anchor, stage) {
		t.Fatal("rollback anchor aliases staged new payload")
	}
	claim := stat(entry.Claim)
	witness := stat(entry.Witness)
	if !os.SameFile(claim, witness) {
		t.Fatal("retained original claim does not alias identity witness")
	}
	if _, err := os.Lstat(
		filepath.Join(root, filepath.FromSlash(entry.RestoreInstall)),
	); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(%s) error = %v, want consumed", entry.RestoreInstall, err)
	}
	stat(filepath.Base(manifestArtifactPathForFixture(t, root)))
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, old) {
		t.Fatalf("restored target = %q, want %q", got, old)
	}
}

func manifestArtifactPathForFixture(t *testing.T, root string) string {
	t.Helper()
	for _, relative := range indexTransactionArtifacts(t, root) {
		if strings.HasPrefix(
			filepath.Base(filepath.FromSlash(relative)),
			indexBatchManifestPrefix,
		) {
			return relative
		}
	}
	t.Fatal("manifest artifact is missing")
	return ""
}
