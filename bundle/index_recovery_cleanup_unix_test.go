//go:build darwin || linux

package bundle

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

type indexRecoveryCleanupPublicSnapshot struct {
	info    os.FileInfo
	data    []byte
	present bool
}

func TestIndexRecoveryCleanupNoManifestCommitCleanup(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	arrangeIndexBatchCrashBundle(t, root)
	runIndexBatchCrashHelper(t, root, "root-commit")
	manifest := readIndexBatchManifestFixture(t, root)
	cutIndexRecoveryCleanupAfterManifestBoundary(t, root)
	before := captureIndexRecoveryCleanupPublicTargets(t, root, manifest)

	// Act.
	err := recoverIndexBatchFixture(root)

	// Assert.
	if err != nil {
		t.Fatalf("no-manifest commit cleanup error = %v", err)
	}
	assertIndexRecoveryCleanupPublicTargetsUnchanged(t, root, before)
	assertNoIndexTransactionArtifacts(t, root)
}

func TestIndexRecoveryCleanupNoManifestMixedRollbackCleanup(t *testing.T) {
	// Arrange.
	root, _, manifest := arrangeIndexRecoveryCommitBoundaryMixedRollback(t)
	cutIndexRecoveryCleanupAfterManifestBoundary(t, root)
	before := captureIndexRecoveryCleanupPublicTargets(t, root, manifest)

	// Act.
	err := recoverIndexBatchFixture(root)

	// Assert.
	if err != nil {
		t.Fatalf("no-manifest rollback cleanup error = %v", err)
	}
	assertIndexRecoveryCleanupPublicTargetsUnchanged(t, root, before)
	assertNoIndexTransactionArtifacts(t, root)
}

func TestIndexRecoveryCleanupNoManifestPreNewRestoredCleanup(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	arrangeIndexBatchCrashBundle(t, root)
	runIndexBatchCrashHelper(t, root, "mid-vacate-first")
	manifest := readIndexBatchManifestFixture(t, root)
	cutIndexRecoveryCleanupAfterManifestBoundary(t, root)
	before := captureIndexRecoveryCleanupPublicTargets(t, root, manifest)

	// Act.
	err := recoverIndexBatchFixture(root)

	// Assert.
	if err != nil {
		t.Fatalf("no-manifest pre-new restored cleanup error = %v", err)
	}
	assertIndexRecoveryCleanupPublicTargetsUnchanged(t, root, before)
	assertNoIndexTransactionArtifacts(t, root)
}

func TestIndexRecoveryCleanupCrashRestartEveryBoundary(t *testing.T) {
	// Arrange the count once from the same terminal mixed state used per case.
	countRoot, _, _ := arrangeIndexRecoveryCleanupMixedNoManifest(t)
	actionCount := len(indexBatchCanonicalCleanupTrace(t, countRoot))
	if actionCount == 0 {
		t.Fatal("mixed no-manifest fixture has no cleanup actions")
	}

	for ordinal := 0; ordinal < actionCount; ordinal++ {
		t.Run(strconv.Itoa(ordinal), func(t *testing.T) {
			// Arrange.
			root, manifest, before := arrangeIndexRecoveryCleanupMixedNoManifest(t)

			// Act.
			runIndexRecoveryCleanupCrash(t, root, ordinal)
			err := recoverIndexBatchFixture(root)

			// Assert.
			if err != nil {
				t.Fatalf("cleanup restart after action %d error = %v", ordinal, err)
			}
			assertIndexRecoveryCleanupPublicTargetsUnchanged(t, root, before)
			assertNoIndexTransactionArtifacts(t, root)
			_ = manifest
		})
	}
}

func TestIndexRecoveryCleanupDriftFailsHard(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, root string, manifest indexBatchManifest) error
	}{
		{
			name: "target",
			mutate: func(t *testing.T, root string, _ indexBatchManifest) error {
				return os.WriteFile(
					filepath.Join(root, "a", indexFilename),
					[]byte("foreign target\n"),
					0o640,
				)
			},
		},
		{
			name: "parent",
			mutate: func(t *testing.T, root string, _ indexBatchManifest) error {
				return os.Rename(
					filepath.Join(root, "a"),
					filepath.Join(root, "a-detached"),
				)
			},
		},
		{
			name: "artifact",
			mutate: func(t *testing.T, root string, manifest indexBatchManifest) error {
				entry := indexBatchManifestEntryByPath(t, manifest, "a/index.md")
				anchor := filepath.Join(root, filepath.FromSlash(entry.Anchor))
				data, err := os.ReadFile(anchor)
				if err != nil {
					return err
				}
				info, err := os.Lstat(anchor)
				if err != nil {
					return err
				}
				documentSessionAtomicReplace(
					t,
					anchor,
					string(data),
					indexPreservedMode(info.Mode()),
				)
				return nil
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			root, manifest, _ := arrangeIndexRecoveryCleanupMixedNoManifest(t)
			mutated := false
			var atDrift map[string]documentSessionTreeEntry
			hooks := indexPublishHooks{
				beforeCleanup: func(string, *os.Root, string, string) error {
					if mutated {
						return nil
					}
					mutated = true
					if err := testCase.mutate(t, root, manifest); err != nil {
						return err
					}
					atDrift = documentSessionCaptureTree(t, root)
					return nil
				},
			}

			// Act.
			err := recoverIndexBatchFixtureWithHooks(root, hooks)

			// Assert.
			if err == nil {
				t.Fatalf("no-manifest cleanup accepted %s drift", testCase.name)
			}
			if !mutated {
				t.Fatalf("%s drift hook was not reached", testCase.name)
			}
			if atDrift == nil {
				t.Fatalf("%s drift state was not captured", testCase.name)
			}
			afterFirst := documentSessionCaptureTree(t, root)
			documentSessionAssertTreeSnapshotEqual(t, atDrift, afterFirst)
			if manifests := indexBatchManifestFixtureNames(t, root); len(manifests) != 0 {
				t.Fatalf("%s drift recreated manifest %#v", testCase.name, manifests)
			}

			restartErr := recoverIndexBatchFixture(root)
			if restartErr == nil {
				t.Fatalf("no-manifest restart accepted persistent %s drift", testCase.name)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				afterFirst,
				documentSessionCaptureTree(t, root),
			)
		})
	}
}

func TestIndexRecoveryCleanupUnknownAndV2FailClosed(t *testing.T) {
	cases := []string{
		indexTransactionPrefix + "unknown-v9-0000-deadbeef",
		indexTransactionPrefix + "manifest-v2-0000-deadbeef",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			// Arrange.
			root, _, _ := arrangeIndexRecoveryCleanupMixedNoManifest(t)
			if err := os.WriteFile(filepath.Join(root, name), []byte("foreign\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := documentSessionCaptureTree(t, root)

			// Act.
			err := recoverIndexBatchFixture(root)
			after := documentSessionCaptureTree(t, root)

			// Assert.
			if err == nil {
				t.Fatalf("no-manifest cleanup accepted reserved artifact %q", name)
			}
			documentSessionAssertTreeSnapshotEqual(t, before, after)
		})
	}
}

func TestIndexRecoveryCleanupProofArtifactsAreLast(t *testing.T) {
	// Arrange.
	root, manifest, before := arrangeIndexRecoveryCleanupMixedNoManifest(t)
	cleanupPlan := indexBatchCanonicalCleanupTrace(t, root)
	if len(cleanupPlan) == 0 {
		t.Fatal("proof-last cleanup plan is empty")
	}
	proofs := map[string]struct{}{
		indexBatchManifestEntryByPath(t, manifest, "a/index.md").Anchor:   {},
		indexBatchManifestEntryByPath(t, manifest, "b/index.md").Stage:    {},
		indexBatchManifestEntryByPath(t, manifest, indexFilename).Witness: {},
	}
	var removed []string
	var removedTrace []string
	hooks := indexPublishHooks{
		afterCleanup: func(relative string, _ *os.Root, kind, name string) error {
			directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(relative)))
			if directory == "." {
				directory = ""
			}
			artifact := pathJoin(directory, name)
			removed = append(removed, artifact)
			removedTrace = append(removedTrace, relative+":"+kind)
			if _, proof := proofs[artifact]; !proof {
				return nil
			}
			for _, remaining := range indexTransactionArtifacts(t, root) {
				if _, isProof := proofs[remaining]; !isProof {
					return errors.New("proof cleanup started before non-proof artifacts were gone")
				}
			}
			return nil
		},
	}

	// Act.
	err := recoverIndexBatchFixtureWithHooks(root, hooks)

	// Assert.
	if err != nil {
		t.Fatalf("proof-last cleanup error = %v", err)
	}
	if len(removed) == 0 {
		t.Fatal("proof-last cleanup removed no artifacts")
	}
	if !reflect.DeepEqual(removedTrace, cleanupPlan) {
		t.Fatalf("cleanup trace=%#v, want canonical plan %#v", removedTrace, cleanupPlan)
	}
	assertIndexRecoveryCleanupPublicTargetsUnchanged(t, root, before)
	assertNoIndexTransactionArtifacts(t, root)
}

func TestIndexRecoveryCleanupSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_INDEX_V3_4C_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	ordinal, err := strconv.Atoi(os.Getenv("OKF_INDEX_V3_4C_ORDINAL"))
	if err != nil {
		t.Fatal(err)
	}
	current := 0
	hooks := indexPublishHooks{
		afterCleanup: func(string, *os.Root, string, string) error {
			if current == ordinal {
				os.Exit(87)
			}
			current++
			return nil
		},
	}
	if err := recoverIndexBatchFixtureWithHooks(root, hooks); err != nil {
		t.Fatal(err)
	}
	t.Fatal("4c cleanup crash boundary was not reached")
}

func arrangeIndexRecoveryCleanupMixedNoManifest(
	t *testing.T,
) (string, indexBatchManifest, map[string]indexRecoveryCleanupPublicSnapshot) {
	t.Helper()
	root, _, manifest := arrangeIndexRecoveryCommitBoundaryMixedRollback(t)
	cutIndexRecoveryCleanupAfterManifestBoundary(t, root)
	return root, manifest, captureIndexRecoveryCleanupPublicTargets(t, root, manifest)
}

func cutIndexRecoveryCleanupAfterManifestBoundary(t *testing.T, root string) {
	t.Helper()
	boundaryErr := errors.New("stop immediately after durable manifest removal")
	reached := false
	hooks := indexPublishHooks{
		afterCleanup: func(_ string, _ *os.Root, kind, _ string) error {
			if kind != string(indexArtifactManifest) {
				return nil
			}
			reached = true
			return boundaryErr
		},
	}
	err := applyIndexRecoveryCommitBoundarySharedPass(root, hooks)
	if !reached || !errors.Is(err, boundaryErr) {
		t.Fatalf("manifest boundary reached=%t error=%v, want injected stop", reached, err)
	}
	assertNoIndexRecoveryCommitBoundaryManifest(t, root)
	if plan := indexBatchCanonicalCleanupTrace(t, root); len(plan) == 0 {
		t.Fatal("manifest boundary retained no exact proof cleanup plan")
	}
}

func runIndexRecoveryCleanupCrash(t *testing.T, root string, ordinal int) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestIndexRecoveryCleanupSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_INDEX_V3_4C_ROOT="+root,
		"OKF_INDEX_V3_4C_ORDINAL="+strconv.Itoa(ordinal),
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 87 {
		t.Fatalf("4c cleanup helper %d error = %v, output:\n%s", ordinal, err, output.String())
	}
}

func captureIndexRecoveryCleanupPublicTargets(
	t *testing.T,
	root string,
	manifest indexBatchManifest,
) map[string]indexRecoveryCleanupPublicSnapshot {
	t.Helper()
	captured := make(map[string]indexRecoveryCleanupPublicSnapshot, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		filename := filepath.Join(root, filepath.FromSlash(entry.Path))
		info, err := os.Lstat(filename)
		if errors.Is(err, fs.ErrNotExist) {
			captured[entry.Path] = indexRecoveryCleanupPublicSnapshot{}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		captured[entry.Path] = indexRecoveryCleanupPublicSnapshot{
			info:    info,
			data:    data,
			present: true,
		}
	}
	return captured
}

func assertIndexRecoveryCleanupPublicTargetsUnchanged(
	t *testing.T,
	root string,
	before map[string]indexRecoveryCleanupPublicSnapshot,
) {
	t.Helper()
	for relative, snapshot := range before {
		filename := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(filename)
		if !snapshot.present {
			if !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("public target %q appeared: %v", relative, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(snapshot.info, info) ||
			indexPreservedMode(snapshot.info.Mode()) != indexPreservedMode(info.Mode()) ||
			!bytes.Equal(snapshot.data, data) {
			t.Fatalf("public target %q changed during no-manifest cleanup", relative)
		}
	}
}
