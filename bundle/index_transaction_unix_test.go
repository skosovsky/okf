//go:build unix

package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRegenerateIndexesRejectsNamedPipeDestinationWithoutOpeningIt(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination := filepath.Join(root, "nested", indexFilename)
	if err := unix.Mkfifo(destination, 0o600); err != nil {
		t.Skipf("mkfifo is unavailable: %v", err)
	}

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert.
	const wantError = `invalid index destination "nested/index.md": destination is not a regular file`
	if err == nil || err.Error() != wantError {
		t.Fatalf("RegenerateIndexes() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	info, statErr := os.Lstat(destination)
	if statErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("destination mode = %v, error = %v; want named pipe unchanged", info, statErr)
	}
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesRejectsSocketDestinationWithoutOpeningIt(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination := filepath.Join(root, "nested", indexFilename)
	listener, err := net.Listen("unix", destination)
	if err != nil {
		t.Skipf("Unix sockets are unavailable: %v", err)
	}
	defer listener.Close()

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert.
	const wantError = `invalid index destination "nested/index.md": destination is not a regular file`
	if err == nil || err.Error() != wantError {
		t.Fatalf("RegenerateIndexes() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	info, statErr := os.Lstat(destination)
	if statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("destination mode = %v, error = %v; want socket unchanged", info, statErr)
	}
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesRejectsOrphanSpecialRecoveryArtifactsWithoutOpeningThem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		create func(t *testing.T, filename string) func()
	}{
		{
			name: "named pipe",
			create: func(t *testing.T, filename string) func() {
				t.Helper()
				if err := unix.Mkfifo(filename, 0o600); err != nil {
					t.Skipf("mkfifo is unavailable: %v", err)
				}
				return func() {}
			},
		},
		{
			name: "socket",
			create: func(t *testing.T, filename string) func() {
				t.Helper()
				listener, err := net.Listen("unix", filename)
				if err != nil {
					t.Skipf("Unix sockets are unavailable: %v", err)
				}
				return func() { _ = listener.Close() }
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "orphan"), 0o755); err != nil {
				t.Fatal(err)
			}
			name := indexRecoveryBackupPrefix + "0000-" + strings.Repeat("0", 64)
			filename := filepath.Join(root, "orphan", name)
			cleanup := test.create(t, filename)
			defer cleanup()
			relative := filepath.ToSlash(filepath.Join("orphan", name))

			// Act.
			written, err := regenerateIndexesForTest(root)

			// Assert.
			want := "index recovery for destination \"orphan/index.md\" is unrecoverable: artifact \"" +
				relative + "\": recovery artifact is not a regular file"
			if written != nil || err == nil || err.Error() != want {
				t.Fatalf("RegenerateIndexes() = %#v, %v; want nil, %q", written, err, want)
			}
			if strings.Contains(err.Error(), "original preserved at") {
				t.Fatalf("special artifact error contains false claim: %v", err)
			}
			if _, statErr := os.Lstat(filename); statErr != nil {
				t.Fatalf("special recovery artifact was removed: %v", statErr)
			}
		})
	}
}

func TestRegenerateIndexesRejectsSpecialSelfAuthenticatingStageWithoutOpeningIt(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := indexArtifactNameFor(
		indexArtifactStage,
		"nested/index.md",
		0o600,
		[]byte("# Expected stage\n"),
		0,
	)
	relative := pathJoin("nested", name)
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := unix.Mkfifo(filename, 0o600); err != nil {
		t.Skipf("mkfifo is unavailable: %v", err)
	}

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert.
	want := "index recovery for destination \"nested/index.md\" is unrecoverable: artifact \"" +
		relative + "\": recovery artifact is not a regular file"
	if written != nil || err == nil || err.Error() != want {
		t.Fatalf("RegenerateIndexes() = %#v, %v; want nil, %q", written, err, want)
	}
	info, statErr := os.Lstat(filename)
	if statErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("stage evidence changed: info=%v err=%v", info, statErr)
	}
}

func TestRegenerateIndexesRecoveryArtifactPreservesSpecialModeBits(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	specialMode := fs.FileMode(0o640) | fs.ModeSetuid
	_, artifact, _ := leaveVerifiedIndexRecoveryBackup(t, root, specialMode)
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(artifact)))
	if err != nil {
		t.Fatal(err)
	}
	if got := indexPreservedMode(info.Mode()); got != specialMode {
		t.Skipf("filesystem does not preserve setuid on owned regular files: got %v", got)
	}

	// Act.
	written, recoveryErr := regenerateIndexesForTest(root)

	// Assert.
	if recoveryErr != nil || len(written) == 0 {
		t.Fatalf("RegenerateIndexes() = %#v, %v; want automatic recovery", written, recoveryErr)
	}
	assertPathDoesNotExist(t, filepath.Join(root, filepath.FromSlash(artifact)))
	assertNoIndexTransactionArtifacts(t, root)
	destination := filepath.Join(root, "nested", indexFilename)
	assertFileBytes(t, destination, []byte(BuildIndexText([]IndexEntry{
		{Type: "Note", Title: "A", Link: "a.md", Description: "Alpha."},
	})))
	after, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got := indexPreservedMode(after.Mode()); got != specialMode {
		t.Fatalf("regenerated destination mode = %v, want %v", got, specialMode)
	}
}

func TestRegenerateIndexesRootCommitBarrierRejectsSpecialModeMutation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	probe := filepath.Join(root, "mode-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
		t.Fatal(err)
	}
	specialMode := fs.FileMode(0o600) | fs.ModeSetuid
	if err := os.Chmod(probe, specialMode); err != nil {
		t.Skipf("special chmod bits are unavailable: %v", err)
	}
	probeInfo, err := os.Lstat(probe)
	if err != nil {
		t.Fatal(err)
	}
	if indexPreservedMode(probeInfo.Mode()) != specialMode {
		t.Skipf("filesystem did not retain special mode bits: %v", probeInfo.Mode())
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}

	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination := filepath.Join(root, "nested", indexFilename)
	original := []byte("# Original nested index\n")
	published := []byte(BuildIndexText([]IndexEntry{
		{Type: "Note", Title: "A", Link: "a.md", Description: "Alpha."},
	}))
	if err := os.WriteFile(destination, original, 0o640); err != nil {
		t.Fatal(err)
	}
	mutated := false
	hooks := indexPublishHooks{
		beforeRootCommit: func(relative string, _ *os.Root, _ string) error {
			if relative != indexFilename || mutated {
				return nil
			}
			if err := os.Chmod(destination, fs.FileMode(0o640)|fs.ModeSetuid); err != nil {
				return err
			}
			mutated = true
			return nil
		},
	}

	// Act.
	written, regenerationErr := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !mutated || written != nil || regenerationErr == nil ||
		!errors.Is(regenerationErr, errPublicationScopeInvalid) ||
		!strings.Contains(regenerationErr.Error(), "artifact mode changed") {
		t.Fatalf("mutated = %t, result = %#v, %v; want precommit mode rejection", mutated, written, regenerationErr)
	}
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	assertFileBytes(t, destination, published)
	info, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got := indexPreservedMode(info.Mode()); got != fs.FileMode(0o640)|fs.ModeSetuid {
		t.Fatalf("externally mutated mode = %v, want 0640+setuid", got)
	}
	manifest := readIndexBatchManifestFixture(t, root)
	entry := indexBatchManifestEntryByPath(t, manifest, "nested/index.md")
	stageInfo, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Stage)))
	if err != nil || !os.SameFile(info, stageInfo) {
		t.Fatalf("retained stage identity = %v, target = %v, error = %v", stageInfo, info, err)
	}
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(entry.Backup)), original)
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Witness))); err != nil {
		t.Fatalf("retained witness Lstat error = %v", err)
	}
}

func TestIndexPublicationLockSerializesSubprocessRegeneration(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	control := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	ready := filepath.Join(control, "ready")
	release := filepath.Join(control, "release")
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestIndexPublicationLockSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_INDEX_LOCK_HELPER=hold",
		"OKF_INDEX_LOCK_ROOT="+root,
		"OKF_INDEX_LOCK_READY="+ready,
		"OKF_INDEX_LOCK_RELEASE="+release,
	)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForIndexLockTestPath(t, ready)

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert while the subprocess owns the physical directory.
	if written != nil || !errors.Is(err, ErrPublicationOwnershipConflict) {
		t.Fatalf("subprocess contender = %#v, %v; want ownership conflict", written, err)
	}
	assertPathDoesNotExist(t, filepath.Join(root, "nested", indexFilename))
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	assertNoIndexTransactionArtifacts(t, root)

	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("holder subprocess error = %v\n%s", err, output.String())
	}
	assertFileBytes(t, filepath.Join(root, "nested", indexFilename), []byte(BuildIndexText([]IndexEntry{
		{Type: "Note", Title: "A", Link: "a.md", Description: "Alpha."},
	})))
	assertNoIndexTransactionArtifacts(t, root)
}

func TestIndexPublicationLockIsReleasedWhenSubprocessExits(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	control := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	ready := filepath.Join(control, "ready")
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestIndexPublicationLockSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_INDEX_LOCK_HELPER=exit",
		"OKF_INDEX_LOCK_ROOT="+root,
		"OKF_INDEX_LOCK_READY="+ready,
	)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("exiting holder error = %v\n%s", err, output.String())
	}
	waitForIndexLockTestPath(t, ready)

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert.
	if err != nil || len(written) == 0 {
		t.Fatalf("post-exit regeneration = %#v, %v; want success", written, err)
	}
	assertNoIndexTransactionArtifacts(t, root)
}

func TestIndexBatchRollbackAnchorIsolatedFromHeldEvidenceMutation(t *testing.T) {
	for _, mutatedKind := range []string{"claim", "backup"} {
		mutatedKind := mutatedKind
		t.Run(mutatedKind, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			before, _ := arrangeIndexBatchCrashBundle(t, root)
			targetPath := filepath.Join(root, "b", indexFilename)
			var held *os.File
			var err error
			if mutatedKind == "claim" {
				held, err = os.OpenFile(targetPath, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				if held != nil {
					_ = held.Close()
				}
			})
			forcedRollback := errors.New("force rollback after nested prefix")
			mutated := false
			hooks := indexPublishHooks{
				afterBackupVerified: func(relative string, parent *os.Root, backup string) error {
					if mutatedKind != "backup" || relative != "b/index.md" {
						return nil
					}
					var openErr error
					held, openErr = parent.OpenFile(backup, os.O_RDWR, 0)
					return openErr
				},
				beforeRootCommit: func(string, *os.Root, string) error {
					return forcedRollback
				},
				beforeRestore: func(relative string, _ *os.Root, _, _ string) error {
					if relative != "b/index.md" || mutated {
						return nil
					}
					if held == nil {
						return errors.New("held evidence descriptor is unavailable")
					}
					if _, writeErr := held.WriteAt([]byte("X"), 0); writeErr != nil {
						return writeErr
					}
					if syncErr := held.Sync(); syncErr != nil {
						return syncErr
					}
					mutated = true
					return nil
				},
			}

			// Act.
			written, publishErr := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if written != nil || !mutated || !errors.Is(publishErr, forcedRollback) {
				t.Fatalf("written=%#v mutated=%t error=%v, want rollback with held mutation", written, mutated, publishErr)
			}
			manifest := readIndexBatchManifestFixture(t, root)
			entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
			anchorInfo, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Anchor)))
			if statErr != nil {
				t.Fatal(statErr)
			}
			targetInfo, statErr := os.Lstat(targetPath)
			if statErr != nil || !os.SameFile(targetInfo, anchorInfo) {
				t.Fatalf("target does not alias durable anchor: target=%v anchor=%v error=%v", targetInfo, anchorInfo, statErr)
			}
			assertFileBytes(t, targetPath, before["b/index.md"])
			evidence := entry.Claim
			if mutatedKind == "backup" {
				evidence = entry.Backup
			}
			if got := documentSessionReadFile(t, filepath.Join(root, filepath.FromSlash(evidence))); got == string(before["b/index.md"]) {
				t.Fatalf("held %s mutation was not retained", mutatedKind)
			}
		})
	}
}

func TestIndexBatchRollbackRejectsSameBytesDifferentInodeAfterAnchorInstall(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	before, _ := arrangeIndexBatchCrashBundle(t, root)
	targetPath := filepath.Join(root, "b", indexFilename)
	forcedRollback := errors.New("force rollback after nested prefix")
	replaced := false
	hooks := indexPublishHooks{
		beforeRootCommit: func(string, *os.Root, string) error {
			return forcedRollback
		},
		afterRestoreLink: func(relative string, _ *os.Root, _, _ string) error {
			if relative != "b/index.md" || replaced {
				return nil
			}
			documentSessionAtomicReplace(t, targetPath, string(before["b/index.md"]), 0o640)
			replaced = true
			return nil
		},
	}

	// Act.
	written, publishErr := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if written != nil || !replaced || !errors.Is(publishErr, forcedRollback) {
		t.Fatalf("written=%#v replaced=%t error=%v, want identity-proof rejection", written, replaced, publishErr)
	}
	manifest := readIndexBatchManifestFixture(t, root)
	entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
	anchorInfo, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Anchor)))
	if statErr != nil {
		t.Fatal(statErr)
	}
	targetInfo, statErr := os.Lstat(targetPath)
	if statErr != nil || os.SameFile(targetInfo, anchorInfo) {
		t.Fatalf("same-byte replacement unexpectedly aliases anchor: target=%v anchor=%v error=%v", targetInfo, anchorInfo, statErr)
	}
	assertFileBytes(t, targetPath, before["b/index.md"])
	beforeRetry := documentSessionCaptureTree(t, root)

	// Act again.
	recoveryErr := recoverIndexBatchFixture(root)
	afterRetry := documentSessionCaptureTree(t, root)

	// Assert again.
	if recoveryErr == nil {
		t.Fatal("recovery accepted same-byte target without anchor identity")
	}
	documentSessionAssertTreeSnapshotEqual(t, beforeRetry, afterRetry)
}

func TestIndexBatchWitnessInventoryRejectsMissingCorruptExtraAndDuplicateWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string, entry indexBatchManifestEntry)
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, root string, entry indexBatchManifestEntry) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(entry.Witness))); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt",
			mutate: func(t *testing.T, root string, entry indexBatchManifestEntry) {
				t.Helper()
				if err := os.WriteFile(
					filepath.Join(root, filepath.FromSlash(entry.Witness)),
					[]byte("# Corrupt witness\n"),
					fs.FileMode(entry.OldMode),
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra",
			mutate: func(t *testing.T, root string, entry indexBatchManifestEntry) {
				t.Helper()
				writeIndexTransactionArtifact(
					t,
					root,
					entry.Path,
					indexArtifactWitness,
					[]byte("# Extra witness\n"),
					fs.FileMode(entry.OldMode),
					9,
				)
			},
		},
		{
			name: "duplicate private alias",
			mutate: func(t *testing.T, root string, entry indexBatchManifestEntry) {
				t.Helper()
				pinned, err := openRootWithoutSymlinks(root)
				if err != nil {
					t.Fatal(err)
				}
				directory := filepath.ToSlash(filepath.Dir(entry.Path))
				if directory == "." {
					directory = ""
				}
				parent, err := openDirectory(pinned, directory)
				if err != nil {
					_ = pinned.Close()
					t.Fatal(err)
				}
				witnessName := filepath.Base(filepath.FromSlash(entry.Witness))
				privateName, err := allocatePrivatePublicationClaim(parent, witnessName)
				if err == nil {
					err = parent.Link(witnessName, privateName)
				}
				closeErr := errors.Join(parent.Close(), pinned.Close())
				if err := errors.Join(err, closeErr); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			arrangeIndexBatchCrashBundle(t, root)
			runIndexBatchCrashHelper(t, root, "manifest")
			manifest := readIndexBatchManifestFixture(t, root)
			entry := indexBatchManifestEntryByPath(t, manifest, "a/index.md")
			test.mutate(t, root, entry)
			before := documentSessionCaptureTree(t, root)

			// Act.
			recoveryErr := recoverIndexBatchFixture(root)
			after := documentSessionCaptureTree(t, root)

			// Assert.
			if recoveryErr == nil {
				t.Fatal("recovery accepted invalid witness inventory")
			}
			documentSessionAssertTreeSnapshotEqual(t, before, after)
		})
	}
}

func TestIndexBatchDegradedClaimRepairOccupiedTargetIsZeroMutation(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	arrangeIndexBatchCrashBundle(t, root)
	runIndexBatchCrashHelper(t, root, "mid-vacate")
	manifest := readIndexBatchManifestFixture(t, root)
	entry := missingIndexBatchManifestEntry(t, root, manifest)
	claimPath := filepath.Join(root, filepath.FromSlash(entry.Claim))
	if err := os.WriteFile(claimPath, []byte("# Foreign claim\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(root, filepath.FromSlash(entry.Path))
	if err := os.WriteFile(targetPath, []byte("# Occupied foreign target\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	before := documentSessionCaptureTree(t, root)

	// Act.
	recoveryErr := recoverIndexBatchFixture(root)
	after := documentSessionCaptureTree(t, root)

	// Assert.
	if recoveryErr == nil {
		t.Fatal("recovery accepted occupied foreign target")
	}
	documentSessionAssertTreeSnapshotEqual(t, before, after)
}

func TestIndexForwardRollbackConvergesPublishedPrefixWithFrozenNonOwnedPeerDrift(t *testing.T) {
	t.Parallel()

	peers := []struct {
		name string
		path string
	}{
		{name: "a", path: "a/index.md"},
		{name: "b", path: "b/index.md"},
		{name: "root", path: indexFilename},
	}
	roles := []struct {
		name string
		kind string
	}{
		{name: "target", kind: "target"},
		{name: "S", kind: "stage"},
		{name: "B", kind: "backup"},
		{name: "C", kind: "claim"},
		{name: "W", kind: "witness"},
		{name: "A", kind: "anchor"},
		{name: "M", kind: "manifest"},
	}
	caseCount := 0
	for prefix := 0; prefix < len(peers); prefix++ {
		for peerOrdinal := prefix; peerOrdinal < len(peers); peerOrdinal++ {
			for _, role := range roles {
				test := struct {
					name        string
					prefix      int
					peer        string
					driftedRole string
				}{
					name: "prefix " + strconv.Itoa(prefix) +
						"/peer " + peers[peerOrdinal].name +
						"/" + role.name,
					prefix:      prefix,
					peer:        peers[peerOrdinal].path,
					driftedRole: role.kind,
				}
				caseCount++
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()

					// Arrange.
					root := t.TempDir()
					before, _ := arrangeIndexBatchCrashBundle(t, root)
					forcedRollback := errors.New("injected forward publication failure")
					var manifest indexBatchManifest
					var manifestPath string
					var frozenPeers map[string]indexForwardRollbackFrozenPath
					var frozenEvidence map[string]indexForwardRollbackFrozenPath
					var afterDriftTree map[string]documentSessionTreeEntry
					var afterDriftIdentities map[string]os.FileInfo
					drifted := false
					hooks := indexPublishHooks{
						afterBatchManifestDurable: func(_ *os.Root, name string) error {
							manifestPath = filepath.Join(root, name)
							data, err := os.ReadFile(manifestPath)
							if err != nil {
								return err
							}
							return json.Unmarshal(data, &manifest)
						},
						beforePublish: func(_ string, ordinal int) error {
							if ordinal != test.prefix {
								return nil
							}
							entry := indexBatchManifestEntryByPath(t, manifest, test.peer)
							driftedPath, err := indexForwardRollbackRolePath(
								root,
								entry,
								manifestPath,
								test.driftedRole,
							)
							if err != nil {
								return err
							}
							if err := mutateIndexForwardRollbackRole(
								driftedPath,
								test.driftedRole,
							); err != nil {
								return err
							}
							frozenPeers = captureIndexForwardRollbackNonOwnedPeers(
								t,
								root,
								manifest,
								test.prefix,
							)
							frozenEvidence = captureIndexForwardRollbackReservedPaths(t, root)
							afterDriftTree = documentSessionCaptureTree(t, root)
							afterDriftIdentities = documentPublicationIdentities(t, root)
							drifted = true
							return forcedRollback
						},
					}

					// Act.
					written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

					// Assert.
					if !drifted || written != nil || !errors.Is(err, forcedRollback) ||
						!errors.Is(err, errPublicationConflict) {
						t.Fatalf(
							"drifted=%t written=%#v error=%v, want typed forward rollback conflict",
							drifted,
							written,
							err,
						)
					}
					if test.prefix == 0 {
						documentSessionAssertTreeSnapshotEqual(
							t,
							afterDriftTree,
							documentSessionCaptureTree(t, root),
						)
						documentAssertPublicationIdentities(t, root, afterDriftIdentities)
					}
					assertIndexForwardRollbackFrozenPaths(t, root, frozenPeers)
					assertIndexForwardRollbackFrozenPaths(t, root, frozenEvidence)
					assertIndexForwardRollbackPublishedPrefix(
						t,
						root,
						manifest,
						before,
						test.prefix,
					)
				})
			}
		}
	}
	if caseCount != 42 {
		t.Fatalf("forward rollback matrix cases=%d, want 42", caseCount)
	}
}

type indexForwardRollbackFrozenPath struct {
	present bool
	entry   documentSessionTreeEntry
}

func indexForwardRollbackRolePath(
	root string,
	entry indexBatchManifestEntry,
	manifestPath string,
	role string,
) (string, error) {
	var relative string
	switch role {
	case "target":
		relative = entry.Path
	case "stage":
		relative = entry.Stage
	case "backup":
		relative = entry.Backup
	case "claim":
		relative = entry.Claim
	case "witness":
		relative = entry.Witness
	case "anchor":
		relative = entry.Anchor
	case "manifest":
		return manifestPath, nil
	default:
		return "", errors.New("unknown forward rollback drift role " + role)
	}
	if relative == "" {
		return "", errors.New("forward rollback drift role is unavailable: " + role)
	}
	return filepath.Join(root, filepath.FromSlash(relative)), nil
}

func mutateIndexForwardRollbackRole(filename, role string) error {
	switch role {
	case "claim", "anchor":
		return os.WriteFile(filename, []byte("foreign forward rollback evidence\n"), 0o604)
	default:
		return mutateIndexCrossDestinationPath(filename, true)
	}
}

func captureIndexForwardRollbackNonOwnedPeers(
	t *testing.T,
	root string,
	manifest indexBatchManifest,
	prefix int,
) map[string]indexForwardRollbackFrozenPath {
	t.Helper()
	var relatives []string
	for ordinal, entry := range manifest.Entries {
		if ordinal < prefix {
			continue
		}
		relatives = append(relatives,
			entry.Path,
			entry.Stage,
			entry.Backup,
			entry.Claim,
			entry.Witness,
			entry.Anchor,
		)
	}
	return captureIndexForwardRollbackPaths(t, root, relatives)
}

func captureIndexForwardRollbackReservedPaths(
	t *testing.T,
	root string,
) map[string]indexForwardRollbackFrozenPath {
	t.Helper()
	reserved := captureReservedPublicationTree(t, root)
	frozen := make(map[string]indexForwardRollbackFrozenPath, len(reserved))
	for relative, entry := range reserved {
		frozen[relative] = indexForwardRollbackFrozenPath{
			present: true,
			entry:   entry,
		}
	}
	return frozen
}

func captureIndexForwardRollbackPaths(
	t *testing.T,
	root string,
	relatives []string,
) map[string]indexForwardRollbackFrozenPath {
	t.Helper()
	tree := documentSessionCaptureTree(t, root)
	frozen := make(map[string]indexForwardRollbackFrozenPath, len(relatives))
	for _, relative := range relatives {
		if relative == "" {
			continue
		}
		entry, present := tree[relative]
		frozen[relative] = indexForwardRollbackFrozenPath{
			present: present,
			entry:   entry,
		}
	}
	return frozen
}

func assertIndexForwardRollbackFrozenPaths(
	t *testing.T,
	root string,
	want map[string]indexForwardRollbackFrozenPath,
) {
	t.Helper()
	tree := documentSessionCaptureTree(t, root)
	for relative, expected := range want {
		actual, present := tree[relative]
		if present != expected.present {
			t.Fatalf(
				"frozen rollback path %q presence=%t, want %t",
				relative,
				present,
				expected.present,
			)
		}
		if !present {
			continue
		}
		if expected.entry.info == nil || actual.info == nil ||
			!os.SameFile(expected.entry.info, actual.info) ||
			expected.entry.info.Mode() != actual.info.Mode() ||
			expected.entry.info.Size() != actual.info.Size() ||
			!expected.entry.info.ModTime().Equal(actual.info.ModTime()) ||
			expected.entry.data != actual.data ||
			expected.entry.link != actual.link {
			t.Fatalf(
				"frozen rollback path %q changed:\nbefore=%#v\nafter=%#v",
				relative,
				expected.entry,
				actual,
			)
		}
	}
}

func assertIndexForwardRollbackPublishedPrefix(
	t *testing.T,
	root string,
	manifest indexBatchManifest,
	before map[string][]byte,
	prefix int,
) {
	t.Helper()
	if len(manifest.Entries) != 3 {
		t.Fatalf("manifest entries=%d, want 3", len(manifest.Entries))
	}
	for ordinal, entry := range manifest.Entries {
		for _, evidence := range []string{entry.Stage, entry.Backup, entry.Witness} {
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(evidence))); err != nil {
				t.Fatalf("retained evidence %q: %v", evidence, err)
			}
		}
		if ordinal >= prefix {
			continue
		}
		targetPath := filepath.Join(root, filepath.FromSlash(entry.Path))
		anchorPath := filepath.Join(root, filepath.FromSlash(entry.Anchor))
		targetInfo, targetErr := os.Lstat(targetPath)
		anchorInfo, anchorErr := os.Lstat(anchorPath)
		if targetErr != nil || anchorErr != nil || !os.SameFile(targetInfo, anchorInfo) {
			t.Fatalf(
				"rolled-back prefix target %q is not exact A: target=%v A=%v errors=%v/%v",
				entry.Path,
				targetInfo,
				anchorInfo,
				targetErr,
				anchorErr,
			)
		}
		assertFileBytes(t, targetPath, before[entry.Path])
		if got := indexPreservedMode(targetInfo.Mode()); got != fs.FileMode(entry.OldMode) {
			t.Fatalf(
				"rolled-back prefix target %q mode=%v, want %v",
				entry.Path,
				got,
				fs.FileMode(entry.OldMode),
			)
		}
		for _, evidence := range []string{entry.Claim, entry.Anchor} {
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(evidence))); err != nil {
				t.Fatalf("retained rollback evidence %q: %v", evidence, err)
			}
		}
	}
}

func TestIndexActivePublicationRevalidatesCrossDestinationInventoryAtEveryHook(t *testing.T) {
	t.Parallel()

	for _, phase := range []struct {
		name          string
		cuts          []string
		mutationKinds []string
		kindsForCut   func(string) []string
		attackEntry   string
		configure     func(hooks *indexPublishHooks, cut string, attack func() error)
	}{
		{
			name:          "create rollback anchor",
			cuts:          indexCreateCrossDestinationCuts(),
			mutationKinds: []string{"target", "stage", "backup", "claim", "witness", "manifest"},
			attackEntry:   "a/index.md",
			configure: func(hooks *indexPublishHooks, cut string, attack func() error) {
				configureIndexCreateCrossDestinationCut(hooks, cut, "b/index.md", attack)
			},
		},
		{
			name:          "install rollback anchor",
			cuts:          []string{"before vacate", "after link"},
			mutationKinds: []string{"target", "stage", "backup", "claim", "witness", "manifest"},
			attackEntry:   "a/index.md",
			configure: func(hooks *indexPublishHooks, cut string, attack func() error) {
				configureIndexActiveInstallCrossDestinationCut(hooks, cut, "b/index.md", attack)
			},
		},
	} {
		phase := phase
		for _, cut := range phase.cuts {
			cut := cut
			mutationKinds := phase.mutationKinds
			if phase.kindsForCut != nil {
				mutationKinds = phase.kindsForCut(cut)
			}
			for _, mutationKind := range mutationKinds {
				mutationKind := mutationKind
				t.Run(phase.name+"/"+cut+"/mutate cross-destination "+mutationKind, func(t *testing.T) {
					t.Parallel()

					// Arrange a two-destination published prefix. Rollback starts
					// with b, while a remains a complete immutable peer group.
					root := t.TempDir()
					_, _ = arrangeIndexBatchCrashBundle(t, root)
					forcedRollback := errors.New("injected rollback after b publication")
					var manifest indexBatchManifest
					var manifestPath string
					attacked := false
					var attackedTree map[string]documentSessionTreeEntry
					var attackedIdentities map[string]os.FileInfo
					attack := func() error {
						if attacked {
							return nil
						}
						entry := indexBatchManifestEntryByPath(t, manifest, phase.attackEntry)
						path, err := indexCrossDestinationMutationPath(
							root,
							entry,
							manifestPath,
							mutationKind,
						)
						if err != nil {
							return err
						}
						if err := mutateIndexCrossDestinationPath(path, mutationKind == "target"); err != nil {
							return err
						}
						attacked = true
						attackedTree = documentSessionCaptureTree(t, root)
						attackedIdentities = documentPublicationIdentities(t, root)
						return nil
					}
					hooks := indexPublishHooks{
						afterBatchManifestDurable: func(_ *os.Root, name string) error {
							manifestPath = filepath.Join(root, name)
							data, err := os.ReadFile(manifestPath)
							if err != nil {
								return err
							}
							return json.Unmarshal(data, &manifest)
						},
						afterPublish: func(relative string, ordinal int) error {
							if relative == "b/index.md" && ordinal == 1 {
								return forcedRollback
							}
							return nil
						},
					}
					phase.configure(&hooks, cut, attack)

					// Act.
					written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

					// Assert. The snapshot is captured after any syscall that
					// defines this boundary (for example the A link or current
					// cleanup removal). No later action is authorized.
					if !attacked || written != nil || err == nil {
						t.Fatalf(
							"attacked=%t written=%#v error=%v, want transaction-wide conflict",
							attacked,
							written,
							err,
						)
					}
					documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
					documentAssertPublicationIdentities(t, root, attackedIdentities)
				})
			}
		}
	}
}

func TestIndexRecoveryRevalidatesCrossDestinationInventoryAtEveryHook(t *testing.T) {
	t.Parallel()

	for _, phase := range []struct {
		name          string
		crashPoint    string
		cuts          []string
		mutationKinds []string
		kindsForCut   func(string) []string
		attackEntry   string
		configure     func(hooks *indexPublishHooks, cut string, attack func() error)
	}{
		{
			name:          "create rollback anchor",
			crashPoint:    "before-root",
			cuts:          indexCreateCrossDestinationCuts(),
			mutationKinds: []string{"target", "stage", "backup", "claim", "witness", "manifest"},
			attackEntry:   "a/index.md",
			configure: func(hooks *indexPublishHooks, cut string, attack func() error) {
				configureIndexCreateCrossDestinationCut(hooks, cut, "b/index.md", attack)
			},
		},
		{
			name:          "install rollback anchor",
			crashPoint:    "before-root",
			cuts:          []string{"before vacate", "after link"},
			mutationKinds: []string{"target", "stage", "backup", "claim", "witness", "manifest"},
			attackEntry:   "a/index.md",
			configure: func(hooks *indexPublishHooks, cut string, attack func() error) {
				configureIndexRecoveryInstallCrossDestinationCut(hooks, cut, "b/index.md", attack)
			},
		},
	} {
		phase := phase
		for _, cut := range phase.cuts {
			cut := cut
			for _, mutationKind := range phase.mutationKinds {
				mutationKind := mutationKind
				t.Run(phase.name+"/"+cut+"/mutate cross-destination "+mutationKind, func(t *testing.T) {
					t.Parallel()

					// Arrange a real crash state so recovery must create/install
					// anchors and then execute the manifest-bound cleanup plan.
					root := t.TempDir()
					_, _ = arrangeIndexBatchCrashBundle(t, root)
					runIndexBatchCrashHelper(t, root, phase.crashPoint)
					manifest := readIndexBatchManifestFixture(t, root)
					manifestNames := indexBatchManifestFixtureNames(t, root)
					if len(manifestNames) != 1 {
						t.Fatalf("batch manifests=%#v, want one", manifestNames)
					}
					manifestPath := filepath.Join(root, manifestNames[0])
					attacked := false
					var attackedTree map[string]documentSessionTreeEntry
					var attackedIdentities map[string]os.FileInfo
					attack := func() error {
						if attacked {
							return nil
						}
						entry := indexBatchManifestEntryByPath(t, manifest, phase.attackEntry)
						path, err := indexCrossDestinationMutationPath(
							root,
							entry,
							manifestPath,
							mutationKind,
						)
						if err != nil {
							return err
						}
						if err := mutateIndexCrossDestinationPath(path, mutationKind == "target"); err != nil {
							return err
						}
						attacked = true
						attackedTree = documentSessionCaptureTree(t, root)
						attackedIdentities = documentPublicationIdentities(t, root)
						return nil
					}
					hooks := indexPublishHooks{}
					phase.configure(&hooks, cut, attack)

					// Act.
					err := recoverIndexBatchFixtureWithHooks(root, hooks)

					// Assert.
					if !attacked || err == nil {
						t.Fatalf(
							"attacked=%t recovery error=%v, want transaction-wide conflict",
							attacked,
							err,
						)
					}
					documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
					documentAssertPublicationIdentities(t, root, attackedIdentities)
				})
			}
		}
	}
}

func TestIndexActiveRollbackRepairsCurrentDestinationEvidenceDriftAtEveryAnchorHook(t *testing.T) {
	t.Parallel()

	for _, phase := range []struct {
		name      string
		cuts      []string
		kinds     []string
		configure func(hooks *indexPublishHooks, cut string, attack func() error)
	}{
		{
			name:  "create rollback anchor",
			cuts:  indexCreateCrossDestinationCuts(),
			kinds: []string{"stage", "backup", "witness"},
			configure: func(hooks *indexPublishHooks, cut string, attack func() error) {
				configureIndexCreateCrossDestinationCut(hooks, cut, "b/index.md", attack)
			},
		},
		{
			name:  "install rollback anchor",
			cuts:  []string{"before vacate", "after link"},
			kinds: []string{"stage", "backup", "claim", "witness"},
			configure: func(hooks *indexPublishHooks, cut string, attack func() error) {
				configureIndexActiveInstallCrossDestinationCut(hooks, cut, "b/index.md", attack)
			},
		},
	} {
		phase := phase
		for _, cut := range phase.cuts {
			cut := cut
			for _, mutationKind := range phase.kinds {
				mutationKind := mutationKind
				for _, mutationForm := range indexCurrentEvidenceMutationFormsForCut(
					phase.name,
					cut,
					mutationKind,
				) {
					mutationForm := mutationForm
					t.Run(
						phase.name+"/"+cut+"/mutate current "+mutationKind+"/"+mutationForm,
						func(t *testing.T) {
							t.Parallel()

							// Arrange.
							root := t.TempDir()
							before, _ := arrangeIndexBatchCrashBundle(t, root)
							forcedRollback := errors.New("injected rollback after b publication")
							var manifest indexBatchManifest
							var manifestPath string
							var evidenceSnapshots map[string]indexEvidenceMutationSnapshot
							var attackedTree map[string]documentSessionTreeEntry
							var attackedIdentities map[string]os.FileInfo
							attacked := false
							hooks := indexPublishHooks{
								afterBatchManifestDurable: func(_ *os.Root, name string) error {
									manifestPath = filepath.Join(root, name)
									data, err := os.ReadFile(manifestPath)
									if err != nil {
										return err
									}
									return json.Unmarshal(data, &manifest)
								},
								afterPublish: func(relative string, ordinal int) error {
									if relative == "b/index.md" && ordinal == 1 {
										return forcedRollback
									}
									return nil
								},
							}
							attack := func() error {
								if attacked {
									return nil
								}
								entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
								filename, err := indexCrossDestinationMutationPath(
									root,
									entry,
									manifestPath,
									mutationKind,
								)
								if err != nil {
									return err
								}
								if err := mutateIndexCurrentEvidencePath(filename, mutationForm); err != nil {
									return err
								}
								evidenceSnapshots = captureIndexEvidenceMutationSnapshots(t, root, entry)
								attackedTree = documentSessionCaptureTree(t, root)
								attackedIdentities = documentPublicationIdentities(t, root)
								attacked = true
								return nil
							}
							phase.configure(&hooks, cut, attack)

							// Act.
							written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

							// Assert. S/B are non-selected evidence while A is
							// being created from C. Once A is durable, S/B/C/W
							// are all soft. Exact-A repair is mandatory and the
							// attacked evidence state must remain byte/identity exact.
							if !attacked || written != nil || err == nil {
								t.Fatalf(
									"attacked=%t written=%#v error=%v, want classified current-evidence rollback",
									attacked,
									written,
									err,
								)
							}
							entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
							if indexCurrentEvidenceExpectedSoft(
								false,
								phase.name,
								cut,
								mutationKind,
								mutationForm,
							) {
								if !errors.Is(err, forcedRollback) {
									t.Fatalf("soft error=%v, want forced rollback", err)
								}
								assertIndexCurrentEvidenceSoftRepair(
									t,
									root,
									manifestPath,
									entry,
									before["b/index.md"],
									evidenceSnapshots,
									err,
								)
							} else {
								assertIndexCurrentEvidenceHardConflict(
									t,
									root,
									attackedTree,
									attackedIdentities,
									err,
								)
							}
						},
					)
				}
			}
		}
	}
}

func TestIndexRecoveryRepairsCurrentDestinationEvidenceDriftAtEveryAnchorHook(t *testing.T) {
	t.Parallel()

	for _, phase := range []struct {
		name      string
		cuts      []string
		kinds     []string
		configure func(hooks *indexPublishHooks, cut string, attack func() error)
	}{
		{
			name:  "create rollback anchor",
			cuts:  indexCreateCrossDestinationCuts(),
			kinds: []string{"stage", "backup", "witness"},
			configure: func(hooks *indexPublishHooks, cut string, attack func() error) {
				configureIndexCreateCrossDestinationCut(hooks, cut, "b/index.md", attack)
			},
		},
		{
			name:  "install rollback anchor",
			cuts:  []string{"before vacate", "after link"},
			kinds: []string{"stage", "backup", "claim", "witness"},
			configure: func(hooks *indexPublishHooks, cut string, attack func() error) {
				configureIndexRecoveryInstallCrossDestinationCut(hooks, cut, "b/index.md", attack)
			},
		},
	} {
		phase := phase
		for _, cut := range phase.cuts {
			cut := cut
			for _, mutationKind := range phase.kinds {
				mutationKind := mutationKind
				for _, mutationForm := range indexCurrentEvidenceMutationFormsForCut(
					phase.name,
					cut,
					mutationKind,
				) {
					mutationForm := mutationForm
					t.Run(
						phase.name+"/"+cut+"/mutate current "+mutationKind+"/"+mutationForm,
						func(t *testing.T) {
							t.Parallel()

							// Arrange.
							root := t.TempDir()
							before, _ := arrangeIndexBatchCrashBundle(t, root)
							runIndexBatchCrashHelper(t, root, "before-root")
							manifest := readIndexBatchManifestFixture(t, root)
							manifestNames := indexBatchManifestFixtureNames(t, root)
							if len(manifestNames) != 1 {
								t.Fatalf("batch manifests=%#v, want one", manifestNames)
							}
							manifestPath := filepath.Join(root, manifestNames[0])
							var evidenceSnapshots map[string]indexEvidenceMutationSnapshot
							var attackedTree map[string]documentSessionTreeEntry
							var attackedIdentities map[string]os.FileInfo
							attacked := false
							attack := func() error {
								if attacked {
									return nil
								}
								entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
								filename, err := indexCrossDestinationMutationPath(
									root,
									entry,
									manifestPath,
									mutationKind,
								)
								if err != nil {
									return err
								}
								if err := mutateIndexCurrentEvidencePath(filename, mutationForm); err != nil {
									return err
								}
								evidenceSnapshots = captureIndexEvidenceMutationSnapshots(t, root, entry)
								attackedTree = documentSessionCaptureTree(t, root)
								attackedIdentities = documentPublicationIdentities(t, root)
								attacked = true
								return nil
							}
							hooks := indexPublishHooks{}
							phase.configure(&hooks, cut, attack)

							// Act.
							err := recoverIndexBatchFixtureWithHooks(root, hooks)

							// Assert.
							if !attacked || err == nil {
								t.Fatalf("attacked=%t recovery error=%v, want current-evidence repair conflict", attacked, err)
							}
							entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
							if indexCurrentEvidenceExpectedSoft(
								true,
								phase.name,
								cut,
								mutationKind,
								mutationForm,
							) {
								assertIndexCurrentEvidenceSoftRepair(
									t,
									root,
									manifestPath,
									entry,
									before["b/index.md"],
									evidenceSnapshots,
									err,
								)
								if phase.name == "install rollback anchor" &&
									cut == "after link" &&
									mutationKind == "stage" &&
									mutationForm == "replacement" {
									assertPathDoesNotExist(
										t,
										filepath.Join(
											root,
											filepath.FromSlash(entry.RestoreInstall),
										),
									)
									discardInfo, discardErr := os.Lstat(
										filepath.Join(
											root,
											filepath.FromSlash(entry.Discard),
										),
									)
									if discardErr != nil ||
										discardInfo.Mode()&os.ModeSymlink != 0 ||
										!discardInfo.Mode().IsRegular() {
										t.Fatalf(
											"terminal exact-D proof is unavailable: info=%v error=%v",
											discardInfo,
											discardErr,
										)
									}
									beforeRestart := documentSessionCaptureTree(t, root)
									beforeRestartIdentities := documentPublicationIdentities(t, root)
									restartErr := recoverIndexBatchFixture(root)
									assertIndexCurrentEvidenceSoftRepair(
										t,
										root,
										manifestPath,
										entry,
										before["b/index.md"],
										evidenceSnapshots,
										restartErr,
									)
									documentSessionAssertTreeSnapshotEqual(
										t,
										beforeRestart,
										documentSessionCaptureTree(t, root),
									)
									documentAssertPublicationIdentities(
										t,
										root,
										beforeRestartIdentities,
									)
								}
							} else {
								assertIndexCurrentEvidenceHardConflict(
									t,
									root,
									attackedTree,
									attackedIdentities,
									err,
								)
							}
						},
					)
				}
			}
		}
	}
}

func TestIndexActiveRollbackRejectsHardCurrentDestinationAliasDriftAtEveryAnchorHook(t *testing.T) {
	t.Parallel()
	testIndexRollbackRejectsHardCurrentDestinationAliasDrift(t, false)
}

func TestIndexRecoveryRejectsHardCurrentDestinationAliasDriftAtEveryAnchorHook(t *testing.T) {
	t.Parallel()
	testIndexRollbackRejectsHardCurrentDestinationAliasDrift(t, true)
}

func TestIndexActiveRollbackRejectsSelectedAlternateSourceDriftAtEveryAnchorHook(t *testing.T) {
	t.Parallel()
	testIndexRollbackRejectsSelectedAlternateSourceDrift(t, false)
}

func TestIndexRecoveryRejectsSelectedAlternateSourceDriftAtEveryAnchorHook(t *testing.T) {
	t.Parallel()
	testIndexRollbackRejectsSelectedAlternateSourceDrift(t, true)
}

func testIndexRollbackRejectsSelectedAlternateSourceDrift(
	t *testing.T,
	recovery bool,
) {
	t.Helper()

	for _, cut := range indexCreateCrossDestinationCuts() {
		cut := cut
		for _, mutationForm := range indexCurrentEvidenceMutationForms() {
			mutationForm := mutationForm
			t.Run(cut+"/mutate selected B/"+mutationForm, func(t *testing.T) {
				t.Parallel()

				// Arrange a complete rollback group, then corrupt C through
				// its held inode. W aliases C, so both preferred-source proofs
				// drift while independent B remains exact and must be selected.
				root := t.TempDir()
				_, _ = arrangeIndexBatchCrashBundle(t, root)
				forcedRollback := errors.New("injected rollback after b publication")
				var manifest indexBatchManifest
				var manifestPath string
				preDrifted := false
				preDriftClaim := func() error {
					if preDrifted {
						return nil
					}
					entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
					claimPath := filepath.Join(root, filepath.FromSlash(entry.Claim))
					if err := mutateIndexCurrentEvidencePath(claimPath, "in place"); err != nil {
						return err
					}
					preDrifted = true
					return nil
				}
				if recovery {
					runIndexBatchCrashHelper(t, root, "before-root")
					manifest = readIndexBatchManifestFixture(t, root)
					names := indexBatchManifestFixtureNames(t, root)
					if len(names) != 1 {
						t.Fatalf("batch manifests=%#v, want one", names)
					}
					manifestPath = filepath.Join(root, names[0])
					if err := preDriftClaim(); err != nil {
						t.Fatal(err)
					}
				}
				attacked := false
				var attackedTree map[string]documentSessionTreeEntry
				var attackedIdentities map[string]os.FileInfo
				attackSelectedBackup := func() error {
					if attacked {
						return nil
					}
					entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
					backupPath := filepath.Join(root, filepath.FromSlash(entry.Backup))
					if err := mutateIndexCurrentEvidencePath(backupPath, mutationForm); err != nil {
						return err
					}
					attacked = true
					attackedTree = documentSessionCaptureTree(t, root)
					attackedIdentities = documentPublicationIdentities(t, root)
					return nil
				}
				hooks := indexPublishHooks{}
				if !recovery {
					hooks.afterBatchManifestDurable = func(_ *os.Root, name string) error {
						manifestPath = filepath.Join(root, name)
						data, err := os.ReadFile(manifestPath)
						if err != nil {
							return err
						}
						return json.Unmarshal(data, &manifest)
					}
					hooks.afterPublish = func(relative string, ordinal int) error {
						if relative != "b/index.md" || ordinal != 1 {
							return nil
						}
						return errors.Join(preDriftClaim(), forcedRollback)
					}
				}
				configureIndexCreateCrossDestinationCut(
					&hooks,
					cut,
					"b/index.md",
					attackSelectedBackup,
				)

				// Act.
				var err error
				if recovery {
					err = recoverIndexBatchFixtureWithHooks(root, hooks)
				} else {
					var written []string
					written, err = regenerateIndexesWithTestHooks(root, nil, "", hooks)
					if written != nil || !errors.Is(err, forcedRollback) {
						t.Fatalf("written=%#v error=%v, want forced alternate-source rollback", written, err)
					}
				}

				// Assert. B became the selected capture source after C/W
				// drift. Any B drift during the copy is hard even though the
				// payload was already read into memory.
				var unrecoverable unrecoverableIndexRecoveryError
				typedConflict := errors.Is(err, errPublicationScopeInvalid) ||
					errors.As(err, &unrecoverable)
				if !preDrifted || !attacked || err == nil || !typedConflict {
					t.Fatalf(
						"preDrifted=%t attacked=%t error=%v, want typed selected-B conflict",
						preDrifted,
						attacked,
						err,
					)
				}
				documentSessionAssertTreeSnapshotEqual(
					t,
					attackedTree,
					documentSessionCaptureTree(t, root),
				)
				documentAssertPublicationIdentities(t, root, attackedIdentities)
			})
		}
	}
}

func testIndexRollbackRejectsHardCurrentDestinationAliasDrift(
	t *testing.T,
	recovery bool,
) {
	t.Helper()

	for _, phase := range []struct {
		name      string
		cuts      []string
		mutations map[string][]string
	}{
		{
			name: "create rollback anchor",
			cuts: indexCreateCrossDestinationCuts(),
			mutations: map[string][]string{
				"stage":   {"in place", "chmod"},
				"claim":   indexCurrentEvidenceMutationForms(),
				"witness": {"in place", "chmod"},
			},
		},
		{
			name: "install rollback anchor",
			cuts: []string{"before vacate"},
			mutations: map[string][]string{
				"stage": {"in place", "chmod"},
			},
		},
	} {
		phase := phase
		for _, cut := range phase.cuts {
			cut := cut
			for mutationKind, forms := range phase.mutations {
				mutationKind := mutationKind
				for _, mutationForm := range forms {
					mutationForm := mutationForm
					t.Run(
						phase.name+"/"+cut+"/hard current "+mutationKind+"/"+mutationForm,
						func(t *testing.T) {
							t.Parallel()

							// Arrange.
							root := t.TempDir()
							_, _ = arrangeIndexBatchCrashBundle(t, root)
							forcedRollback := errors.New("injected rollback after b publication")
							var manifest indexBatchManifest
							var manifestPath string
							if recovery {
								runIndexBatchCrashHelper(t, root, "before-root")
								manifest = readIndexBatchManifestFixture(t, root)
								names := indexBatchManifestFixtureNames(t, root)
								if len(names) != 1 {
									t.Fatalf("batch manifests=%#v, want one", names)
								}
								manifestPath = filepath.Join(root, names[0])
							}
							attacked := false
							var attackedTree map[string]documentSessionTreeEntry
							var attackedIdentities map[string]os.FileInfo
							attack := func() error {
								if attacked {
									return nil
								}
								entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
								filename, err := indexCrossDestinationMutationPath(
									root,
									entry,
									manifestPath,
									mutationKind,
								)
								if err != nil {
									return err
								}
								if err := mutateIndexCurrentEvidencePath(filename, mutationForm); err != nil {
									return err
								}
								attacked = true
								attackedTree = documentSessionCaptureTree(t, root)
								attackedIdentities = documentPublicationIdentities(t, root)
								return nil
							}
							hooks := indexPublishHooks{}
							if !recovery {
								hooks.afterBatchManifestDurable = func(_ *os.Root, name string) error {
									manifestPath = filepath.Join(root, name)
									data, err := os.ReadFile(manifestPath)
									if err != nil {
										return err
									}
									return json.Unmarshal(data, &manifest)
								}
								hooks.afterPublish = func(relative string, ordinal int) error {
									if relative == "b/index.md" && ordinal == 1 {
										return forcedRollback
									}
									return nil
								}
							}
							switch phase.name {
							case "create rollback anchor":
								configureIndexCreateCrossDestinationCut(
									&hooks,
									cut,
									"b/index.md",
									attack,
								)
							case "install rollback anchor":
								if recovery {
									configureIndexRecoveryInstallCrossDestinationCut(
										&hooks,
										cut,
										"b/index.md",
										attack,
									)
								} else {
									configureIndexActiveInstallCrossDestinationCut(
										&hooks,
										cut,
										"b/index.md",
										attack,
									)
								}
							default:
								t.Fatalf("unknown hard-current phase %q", phase.name)
							}

							// Act.
							var err error
							if recovery {
								err = recoverIndexBatchFixtureWithHooks(root, hooks)
							} else {
								var written []string
								written, err = regenerateIndexesWithTestHooks(root, nil, "", hooks)
								if written != nil || !errors.Is(err, forcedRollback) {
									t.Fatalf("written=%#v error=%v, want forced hard conflict", written, err)
								}
							}

							// Assert. In-place/mode S drift mutates public P
							// through SameFile(S,P). C-path drift, and W
							// in-place/mode drift through SameFile(W,C), changes
							// the selected source while A is being captured.
							var unrecoverable unrecoverableIndexRecoveryError
							typedConflict := errors.Is(err, errPublicationScopeInvalid) ||
								errors.As(err, &unrecoverable)
							if !attacked || err == nil || !typedConflict {
								t.Fatalf(
									"attacked=%t error=%v, want typed hard alias/source conflict",
									attacked,
									err,
								)
							}
							documentSessionAssertTreeSnapshotEqual(
								t,
								attackedTree,
								documentSessionCaptureTree(t, root),
							)
							documentAssertPublicationIdentities(t, root, attackedIdentities)
						},
					)
				}
			}
		}
	}
}

func assertIndexCurrentEvidenceSoftRepair(
	t *testing.T,
	root string,
	manifestPath string,
	entry indexBatchManifestEntry,
	original []byte,
	evidenceSnapshots map[string]indexEvidenceMutationSnapshot,
	err error,
) {
	t.Helper()

	var recoverable recoverableIndexBackupError
	if !errors.As(err, &recoverable) || recoverable.backup != entry.Anchor {
		t.Fatalf(
			"error=%v, want typed exact-A recovery at %q",
			err,
			entry.Anchor,
		)
	}
	targetPath := filepath.Join(root, filepath.FromSlash(entry.Path))
	anchorPath := filepath.Join(root, filepath.FromSlash(entry.Anchor))
	targetInfo, targetErr := os.Lstat(targetPath)
	anchorInfo, anchorErr := os.Lstat(anchorPath)
	if targetErr != nil || anchorErr != nil || !os.SameFile(targetInfo, anchorInfo) {
		t.Fatalf(
			"current target was not repaired from exact A: target=%v A=%v errors=%v/%v",
			targetInfo,
			anchorInfo,
			targetErr,
			anchorErr,
		)
	}
	assertFileBytes(t, targetPath, original)
	assertIndexEvidenceMutationSnapshots(t, root, evidenceSnapshots)
	for _, evidence := range []string{entry.Stage, entry.Backup, entry.Claim, entry.Witness} {
		info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(evidence)))
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if statErr != nil {
			t.Fatalf("inspect current-destination evidence %q: %v", evidence, statErr)
		}
		if os.SameFile(anchorInfo, info) {
			t.Fatalf("independent rollback anchor aliases evidence %q", evidence)
		}
	}
	if _, statErr := os.Lstat(manifestPath); statErr != nil {
		t.Fatalf("batch manifest was not retained after soft repair: %v", statErr)
	}
}

func assertIndexCurrentEvidenceHardConflict(
	t *testing.T,
	root string,
	attackedTree map[string]documentSessionTreeEntry,
	attackedIdentities map[string]os.FileInfo,
	err error,
) {
	t.Helper()
	var hard unrecoverableIndexRecoveryError
	if err == nil ||
		(!errors.Is(err, errPublicationScopeInvalid) &&
			!errors.As(err, &hard)) {
		t.Fatalf("error=%v, want typed hard current-evidence conflict", err)
	}
	documentSessionAssertTreeSnapshotEqual(
		t,
		attackedTree,
		documentSessionCaptureTree(t, root),
	)
	documentAssertPublicationIdentities(t, root, attackedIdentities)
}

type indexEvidenceMutationSnapshot struct {
	present bool
	entry   documentSessionTreeEntry
	info    os.FileInfo
}

func indexCurrentEvidenceMutationForms() []string {
	return []string{"in place", "chmod", "replacement", "missing", "type swap"}
}

func indexCurrentEvidenceMutationFormsForCut(phase, cut, kind string) []string {
	forms := indexCurrentEvidenceMutationForms()
	if phase == "create rollback anchor" && kind == "witness" {
		return []string{"replacement", "missing", "type swap"}
	}
	if kind != "stage" ||
		phase != "create rollback anchor" && cut != "before vacate" {
		return forms
	}
	return []string{"replacement", "missing", "type swap"}
}

func indexCurrentEvidenceExpectedSoft(
	recovery bool,
	phase, cut, kind, form string,
) bool {
	if form == "missing" || form == "type swap" {
		return false
	}
	if phase != "install rollback anchor" ||
		(cut != "before vacate" && cut != "after link") {
		return false
	}
	if recovery && cut == "before vacate" {
		return false
	}
	// At this cut A is durable and target~A. S replacement leaves the exact
	// D~S proof inode in place, while in-place/mode drift also mutates D and
	// therefore remains hard.
	return cut != "after link" || kind != "stage" || form == "replacement"
}

func TestIndexCurrentEvidencePolicyGeneratedCounts(t *testing.T) {
	phases := []struct {
		name  string
		cuts  []string
		kinds []string
	}{
		{
			name:  "create rollback anchor",
			cuts:  indexCreateCrossDestinationCuts(),
			kinds: []string{"stage", "backup", "witness"},
		},
		{
			name:  "install rollback anchor",
			cuts:  []string{"before vacate", "after link"},
			kinds: []string{"stage", "backup", "claim", "witness"},
		},
	}
	for _, execution := range []struct {
		name     string
		recovery bool
		wantHard int
		wantSoft int
	}{
		{name: "active", wantHard: 106, wantSoft: 20},
		{name: "recovery", recovery: true, wantHard: 116, wantSoft: 10},
	} {
		hard, soft := 0, 0
		for _, phase := range phases {
			for _, cut := range phase.cuts {
				for _, kind := range phase.kinds {
					for _, form := range indexCurrentEvidenceMutationFormsForCut(
						phase.name,
						cut,
						kind,
					) {
						if indexCurrentEvidenceExpectedSoft(
							execution.recovery,
							phase.name,
							cut,
							kind,
							form,
						) {
							soft++
						} else {
							hard++
						}
					}
				}
			}
		}
		if hard != execution.wantHard || soft != execution.wantSoft {
			t.Fatalf(
				"%s generated policy hard=%d soft=%d, want %d/%d",
				execution.name,
				hard,
				soft,
				execution.wantHard,
				execution.wantSoft,
			)
		}
	}
}

func TestIndexRecoveryPostAnchorBackupDriftStopsBeforeCleanupAndRepeatsOnRestart(
	t *testing.T,
) {
	tests := []struct {
		name         string
		soft         bool
		restartProof bool
		mutate       func(string) error
	}{
		{
			name: "same bytes new inode runtime snapshot only",
			soft: true,
			mutate: func(filename string) error {
				return mutateIndexCrossDestinationPath(filename, true)
			},
		},
		{
			name:         "wrong mode",
			soft:         true,
			restartProof: true,
			mutate: func(filename string) error {
				info, err := os.Lstat(filename)
				if err != nil {
					return err
				}
				return os.Chmod(filename, info.Mode().Perm()^0o100)
			},
		},
		{
			name:         "foreign regular",
			soft:         true,
			restartProof: true,
			mutate: func(filename string) error {
				if err := os.Remove(filename); err != nil {
					return err
				}
				return os.WriteFile(
					filename,
					[]byte("foreign current evidence\n"),
					0o600,
				)
			},
		},
		{
			name: "missing",
			mutate: func(filename string) error {
				return os.Remove(filename)
			},
		},
		{
			name: "symlink",
			mutate: func(filename string) error {
				if err := os.Remove(filename); err != nil {
					return err
				}
				return os.Symlink(indexFilename, filename)
			},
		},
		{
			name: "named pipe",
			mutate: func(filename string) error {
				if err := os.Remove(filename); err != nil {
					return err
				}
				return unix.Mkfifo(filename, 0o600)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			before, _ := arrangeIndexBatchCrashBundle(t, root)
			runIndexBatchCrashHelper(t, root, "before-root")
			manifest := readIndexBatchManifestFixture(t, root)
			entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
			manifestNames := indexBatchManifestFixtureNames(t, root)
			if len(manifestNames) != 1 {
				t.Fatalf("batch manifests=%#v, want one", manifestNames)
			}
			manifestPath := filepath.Join(root, manifestNames[0])
			backupPath := filepath.Join(root, filepath.FromSlash(entry.Backup))
			attacked := false
			var attackedTree map[string]documentSessionTreeEntry
			var attackedIdentities map[string]os.FileInfo
			var postAttackTrace []string
			trace := func(event string) {
				if attacked {
					postAttackTrace = append(postAttackTrace, event)
				}
			}
			hooks := indexPublishHooks{
				afterRestoreLink: func(
					relative string,
					_ *os.Root,
					_, _ string,
				) error {
					if attacked || relative != entry.Path {
						return nil
					}
					if err := test.mutate(backupPath); err != nil {
						return err
					}
					attacked = true
					postAttackTrace = append(postAttackTrace, "drift barrier")
					attackedTree = documentSessionCaptureTree(t, root)
					attackedIdentities = documentPublicationIdentities(t, root)
					return nil
				},
				beforeCleanup: func(_ string, _ *os.Root, _, _ string) error {
					trace("before cleanup")
					return nil
				},
				afterCleanup: func(_ string, _ *os.Root, _, _ string) error {
					trace("after cleanup")
					return nil
				},
				afterBatchRecoveryAction: func(
					_ string,
					_ *os.Root,
					_ string,
				) error {
					trace("after recovery action")
					return nil
				},
				afterArtifactDirectorySync: func(
					_, _, _ string,
					_ *os.Root,
				) error {
					trace("after artifact directory sync")
					return nil
				},
				beforeFinalValidation: func(_ string, _ int) error {
					trace("before final validation")
					return nil
				},
			}

			// Act.
			err := recoverIndexBatchFixtureWithHooks(root, hooks)

			// Assert.
			if !attacked || err == nil {
				t.Fatalf("attacked=%t error=%v, want post-anchor conflict", attacked, err)
			}
			if len(postAttackTrace) != 1 ||
				postAttackTrace[0] != "drift barrier" {
				t.Fatalf(
					"post-anchor trace=%#v, want immediate stop at drift barrier",
					postAttackTrace,
				)
			}
			if !test.soft {
				assertIndexCurrentEvidenceHardConflict(
					t,
					root,
					attackedTree,
					attackedIdentities,
					err,
				)
				return
			}
			assertIndexPostAnchorSoftStop(
				t,
				root,
				manifestPath,
				entry,
				before["b/index.md"],
				attackedTree,
				attackedIdentities,
				err,
			)
			// A same-payload, same-mode replacement is observable only inside
			// the running process: v3 deliberately binds independent B by
			// path, bytes, and mode, not by a persistent inode number.
			if !test.restartProof {
				return
			}

			var restartTrace []string
			restartHooks := indexPublishHooks{
				beforeCleanup: func(_ string, _ *os.Root, _, _ string) error {
					restartTrace = append(restartTrace, "before cleanup")
					return nil
				},
				afterCleanup: func(_ string, _ *os.Root, _, _ string) error {
					restartTrace = append(restartTrace, "after cleanup")
					return nil
				},
				afterBatchRecoveryAction: func(
					_ string,
					_ *os.Root,
					_ string,
				) error {
					restartTrace = append(restartTrace, "after recovery action")
					return nil
				},
				beforeFinalValidation: func(_ string, _ int) error {
					restartTrace = append(restartTrace, "before final validation")
					return nil
				},
			}

			// Act: simulate a fresh process with no normalized runtime state.
			restartErr := recoverIndexBatchFixtureWithHooks(root, restartHooks)

			// Assert: discovery reconstructs the same typed stop without a
			// cleanup or proof mutation.
			if len(restartTrace) != 0 {
				t.Fatalf(
					"restart trace=%#v, want zero convergence hooks",
					restartTrace,
				)
			}
			assertIndexPostAnchorSoftStop(
				t,
				root,
				manifestPath,
				entry,
				before["b/index.md"],
				attackedTree,
				attackedIdentities,
				restartErr,
			)
		})
	}
}

func assertIndexPostAnchorSoftStop(
	t *testing.T,
	root string,
	manifestPath string,
	entry indexBatchManifestEntry,
	original []byte,
	wantTree map[string]documentSessionTreeEntry,
	wantIdentities map[string]os.FileInfo,
	err error,
) {
	t.Helper()

	var recoverable recoverableIndexBackupError
	if err == nil ||
		!errors.As(err, &recoverable) ||
		recoverable.destination != entry.Path ||
		recoverable.backup != entry.Anchor {
		t.Fatalf(
			"error=%v, want typed post-anchor stop for %q at %q",
			err,
			entry.Path,
			entry.Anchor,
		)
	}
	documentSessionAssertTreeSnapshotEqual(
		t,
		wantTree,
		documentSessionCaptureTree(t, root),
	)
	documentAssertPublicationIdentities(t, root, wantIdentities)
	for kind, relative := range map[string]string{
		"M": filepath.ToSlash(strings.TrimPrefix(manifestPath, root+string(os.PathSeparator))),
		"B": entry.Backup,
		"D": entry.Discard,
		"A": entry.Anchor,
	} {
		if relative == "" {
			t.Fatalf("%s proof path is empty", kind)
		}
		if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(relative))); statErr != nil {
			t.Fatalf("%s proof %q was not retained: %v", kind, relative, statErr)
		}
	}
	targetInfo, targetErr := os.Lstat(
		filepath.Join(root, filepath.FromSlash(entry.Path)),
	)
	anchorInfo, anchorErr := os.Lstat(
		filepath.Join(root, filepath.FromSlash(entry.Anchor)),
	)
	discardInfo, discardErr := os.Lstat(
		filepath.Join(root, filepath.FromSlash(entry.Discard)),
	)
	stageInfo, stageErr := os.Lstat(
		filepath.Join(root, filepath.FromSlash(entry.Stage)),
	)
	if targetErr != nil ||
		anchorErr != nil ||
		!os.SameFile(targetInfo, anchorInfo) {
		t.Fatalf(
			"target~A proof is not exact: target=%v A=%v errors=%v/%v",
			targetInfo,
			anchorInfo,
			targetErr,
			anchorErr,
		)
	}
	if discardErr != nil ||
		stageErr != nil ||
		!os.SameFile(discardInfo, stageInfo) {
		t.Fatalf(
			"D~S proof is not exact: D=%v S=%v errors=%v/%v",
			discardInfo,
			stageInfo,
			discardErr,
			stageErr,
		)
	}
	assertFileBytes(
		t,
		filepath.Join(root, filepath.FromSlash(entry.Path)),
		original,
	)
}

func mutateIndexCurrentEvidencePath(filename, form string) error {
	switch form {
	case "in place":
		return mutateIndexCrossDestinationPath(filename, false)
	case "chmod":
		info, err := os.Lstat(filename)
		if err != nil {
			return err
		}
		return os.Chmod(filename, info.Mode().Perm()^0o100)
	case "replacement":
		if err := os.Remove(filename); err != nil {
			return err
		}
		return os.WriteFile(filename, []byte("foreign current evidence\n"), 0o600)
	case "missing":
		return os.Remove(filename)
	case "type swap":
		if err := os.Remove(filename); err != nil {
			return err
		}
		return os.Symlink(indexFilename, filename)
	default:
		return errors.New("unknown current-evidence mutation form " + form)
	}
}

func captureIndexEvidenceMutationSnapshots(
	t *testing.T,
	root string,
	entry indexBatchManifestEntry,
) map[string]indexEvidenceMutationSnapshot {
	t.Helper()
	tree := documentSessionCaptureTree(t, root)
	identities := documentPublicationIdentities(t, root)
	snapshots := make(map[string]indexEvidenceMutationSnapshot)
	for _, relative := range []string{entry.Stage, entry.Backup, entry.Claim, entry.Witness} {
		snapshot := indexEvidenceMutationSnapshot{}
		snapshot.entry, snapshot.present = tree[relative]
		snapshot.info = identities[relative]
		snapshots[relative] = snapshot
	}
	return snapshots
}

func assertIndexEvidenceMutationSnapshots(
	t *testing.T,
	root string,
	want map[string]indexEvidenceMutationSnapshot,
) {
	t.Helper()
	tree := documentSessionCaptureTree(t, root)
	identities := documentPublicationIdentities(t, root)
	for relative, expected := range want {
		actual, present := tree[relative]
		if present != expected.present {
			t.Fatalf(
				"current evidence %q presence=%t, want %t",
				relative,
				present,
				expected.present,
			)
		}
		if !expected.present {
			continue
		}
		if actual.info.Mode() != expected.entry.info.Mode() ||
			actual.info.Size() != expected.entry.info.Size() ||
			!actual.info.ModTime().Equal(expected.entry.info.ModTime()) ||
			actual.data != expected.entry.data ||
			actual.link != expected.entry.link {
			t.Fatalf(
				"current evidence %q changed after soft repair:\nbefore=%#v\nafter=%#v",
				relative,
				expected.entry,
				actual,
			)
		}
		actualInfo := identities[relative]
		if expected.info == nil || actualInfo == nil || !os.SameFile(expected.info, actualInfo) {
			t.Fatalf(
				"current evidence %q identity changed: before=%v after=%v",
				relative,
				expected.info,
				actualInfo,
			)
		}
	}
}

func indexCreateCrossDestinationCuts() []string {
	return []string{
		"after open",
		"after create",
		"after write",
		"after chmod",
		"after file sync",
		"after close",
		"after verify",
		"after directory sync",
	}
}

func indexCleanupCrossDestinationCuts(kinds []string) []string {
	cuts := make([]string, 0, len(kinds)*2)
	for _, cut := range []string{"before remove", "after remove"} {
		for _, kind := range kinds {
			cuts = append(cuts, cut+" on "+kind)
		}
	}
	return cuts
}

func indexRecoveryCleanupCrossDestinationMutationKinds(cut string) []string {
	removeCut, currentKind, ok := strings.Cut(cut, " on ")
	if !ok {
		panic("invalid recovery cleanup cross-destination cut " + cut)
	}
	allPeerKinds := []string{"target", "stage", "backup", "claim", "witness", "anchor", "manifest"}
	if removeCut == "before remove" {
		if currentKind == "manifest" {
			return []string{"target", "stage", "backup", "claim", "witness", "anchor"}
		}
		return allPeerKinds
	}
	if removeCut != "after remove" {
		panic("unknown recovery cleanup cross-destination cut " + cut)
	}
	switch currentKind {
	case "manifest":
		return []string{"target", "stage", "backup", "claim", "witness", "anchor"}
	case "restore":
		return []string{"target", "stage", "backup", "claim", "witness", "anchor"}
	case "backup":
		return []string{"target", "stage", "backup", "witness", "anchor"}
	case "stage":
		return []string{"target", "stage", "witness", "anchor"}
	case "witness":
		return []string{"target", "witness", "anchor"}
	case "anchor":
		return []string{"target", "anchor"}
	default:
		panic("unknown recovery cleanup current kind " + currentKind)
	}
}

func configureIndexCreateCrossDestinationCut(
	hooks *indexPublishHooks,
	cut string,
	relative string,
	attack func() error,
) {
	matches := func(gotRelative, kind string) bool {
		return gotRelative == relative && kind == string(indexArtifactAnchor)
	}
	switch cut {
	case "after open":
		hooks.afterArtifactOpen = func(gotRelative, kind, _ string, _ *os.File) error {
			if !matches(gotRelative, kind) {
				return nil
			}
			return attack()
		}
	case "after create":
		hooks.afterArtifactCreate = func(gotRelative, kind, _ string, _ *os.File) error {
			if !matches(gotRelative, kind) {
				return nil
			}
			return attack()
		}
	case "after write":
		hooks.afterArtifactWrite = func(gotRelative, kind, _ string, _ *os.File) error {
			if !matches(gotRelative, kind) {
				return nil
			}
			return attack()
		}
	case "after chmod":
		hooks.afterArtifactChmod = func(gotRelative, kind, _ string, _ *os.File) error {
			if !matches(gotRelative, kind) {
				return nil
			}
			return attack()
		}
	case "after file sync":
		hooks.afterArtifactFileSync = func(gotRelative, kind, _ string, _ *os.File) error {
			if !matches(gotRelative, kind) {
				return nil
			}
			return attack()
		}
	case "after close":
		hooks.afterArtifactClose = func(gotRelative, kind, _ string, _ *os.Root) error {
			if !matches(gotRelative, kind) {
				return nil
			}
			return attack()
		}
	case "after verify":
		hooks.afterArtifactVerified = func(gotRelative, kind, _ string, _ *os.Root) error {
			if !matches(gotRelative, kind) {
				return nil
			}
			return attack()
		}
	case "after directory sync":
		hooks.afterArtifactDirectorySync = func(gotRelative, kind, _ string, _ *os.Root) error {
			if !matches(gotRelative, kind) {
				return nil
			}
			return attack()
		}
	default:
		panic("unknown create cross-destination cut " + cut)
	}
}

func configureIndexActiveInstallCrossDestinationCut(
	hooks *indexPublishHooks,
	cut string,
	relative string,
	attack func() error,
) {
	armed := false
	hooks.beforeRestore = func(gotRelative string, _ *os.Root, _, _ string) error {
		if gotRelative != relative {
			return nil
		}
		armed = true
		if cut == "before vacate" {
			return attack()
		}
		return nil
	}
	switch cut {
	case "before vacate":
	case "before install":
		hooks.fileSync = func(gotRelative, kind, _ string, file *os.File) error {
			if !armed || gotRelative != relative || kind != string(indexArtifactAnchor) {
				return file.Sync()
			}
			return errors.Join(attack(), file.Sync())
		}
	case "after link":
		hooks.afterRestoreLink = func(gotRelative string, _ *os.Root, _, _ string) error {
			if gotRelative != relative {
				return nil
			}
			return attack()
		}
	default:
		panic("unknown active install cross-destination cut " + cut)
	}
}

func configureIndexRecoveryInstallCrossDestinationCut(
	hooks *indexPublishHooks,
	cut string,
	relative string,
	attack func() error,
) {
	switch cut {
	case "before vacate":
		hooks.afterBatchTargetObservation = func(gotRelative string, _ *os.Root, _ string) error {
			if gotRelative != relative {
				return nil
			}
			return attack()
		}
	case "before install":
		hooks.afterBatchRecoveryVacate = func(gotRelative string, _ *os.Root, _ string) error {
			if gotRelative != relative {
				return nil
			}
			return attack()
		}
	case "after link":
		hooks.afterRestoreLink = func(gotRelative string, _ *os.Root, _, _ string) error {
			if gotRelative != relative {
				return nil
			}
			return attack()
		}
	default:
		panic("unknown recovery install cross-destination cut " + cut)
	}
}

func configureIndexCleanupCrossDestinationCut(
	hooks *indexPublishHooks,
	cut string,
	relative string,
	attack func() error,
) {
	removeCut, currentKind, ok := strings.Cut(cut, " on ")
	if !ok {
		panic("invalid cleanup cross-destination cut " + cut)
	}
	currentRelative := relative
	if currentKind == "manifest" {
		currentRelative = indexFilename
	}
	match := func(gotRelative, kind string) bool {
		return gotRelative == currentRelative && kind == currentKind
	}
	switch removeCut {
	case "before remove":
		hooks.beforeCleanup = func(gotRelative string, _ *os.Root, kind, _ string) error {
			if !match(gotRelative, kind) {
				return nil
			}
			return attack()
		}
	case "after remove":
		hooks.afterCleanup = func(gotRelative string, _ *os.Root, kind, _ string) error {
			if !match(gotRelative, kind) {
				return nil
			}
			return attack()
		}
	default:
		panic("unknown cleanup cross-destination cut " + cut)
	}
}

func indexCrossDestinationMutationPath(
	root string,
	entry indexBatchManifestEntry,
	manifestPath string,
	kind string,
) (string, error) {
	var relative string
	switch kind {
	case "target":
		relative = entry.Path
	case "stage":
		relative = entry.Stage
	case "backup":
		relative = entry.Backup
	case "claim":
		relative = entry.Claim
	case "witness":
		relative = entry.Witness
	case "anchor":
		relative = entry.Anchor
	case "manifest":
		if manifestPath == "" {
			return "", errors.New("batch manifest path is unavailable")
		}
		return manifestPath, nil
	default:
		return "", errors.New("unknown cross-destination mutation kind " + kind)
	}
	if relative == "" {
		return "", errors.New(kind + " is not present in the selected manifest entry")
	}
	return filepath.Join(root, filepath.FromSlash(relative)), nil
}

func mutateIndexCrossDestinationPath(filename string, replace bool) error {
	if replace {
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		info, err := os.Lstat(filename)
		if err != nil {
			return err
		}
		replacement := filepath.Join(
			filepath.Dir(filename),
			".okf-cross-destination-"+filepath.Base(filename),
		)
		if err := os.WriteFile(replacement, data, info.Mode().Perm()); err != nil {
			return err
		}
		if err := os.Chmod(replacement, info.Mode()); err != nil {
			return err
		}
		return os.Rename(replacement, filename)
	}
	file, err := os.OpenFile(filename, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteAt([]byte("X"), 0)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func TestIndexActiveOccupiedRollbackCreatesIndependentAnchorAndRestartIsIdempotent(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	targetPath := filepath.Join(root, "nested", indexFilename)
	original := []byte("# Original nested index\n")
	foreign := []byte("# Concurrent foreign index\n")
	if err := os.WriteFile(targetPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	occupied := false
	hooks := indexPublishHooks{
		afterVacate: func(relative string, parent *os.Root, leaf, _ string) error {
			if relative != "nested/index.md" {
				return nil
			}
			occupied = true
			return parent.WriteFile(leaf, foreign, 0o640)
		},
	}

	// Act.
	written, publicationErr := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert the active path never touches the foreign occupant and creates A.
	if !occupied || written != nil {
		t.Fatalf("occupied=%t written=%#v error=%v", occupied, written, publicationErr)
	}
	var firstRecovery recoverableIndexBackupError
	if publicationErr == nil || !errors.As(publicationErr, &firstRecovery) {
		t.Fatalf("publication error=%v, want typed recoverableIndexBackupError", publicationErr)
	}
	manifest := readIndexBatchManifestFixture(t, root)
	entry := indexBatchManifestEntryByPath(t, manifest, "nested/index.md")
	if firstRecovery.backup != entry.Anchor {
		t.Fatalf("typed recovery artifact=%q, want exact anchor %q", firstRecovery.backup, entry.Anchor)
	}
	anchorPath := filepath.Join(root, filepath.FromSlash(entry.Anchor))
	anchorInfo, err := os.Lstat(anchorPath)
	if err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, anchorPath, original)
	if got := indexPreservedMode(anchorInfo.Mode()); got != 0o600 {
		t.Fatalf("anchor mode=%v, want 0600", got)
	}
	for _, relative := range []string{
		entry.Backup,
		entry.Claim,
		entry.Witness,
		entry.Stage,
	} {
		info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
		if statErr != nil || os.SameFile(anchorInfo, info) {
			t.Fatalf("anchor aliases %s: anchor=%v other=%v error=%v", relative, anchorInfo, info, statErr)
		}
	}
	foreignInfo, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, targetPath, foreign)
	beforeRetry := documentSessionCaptureTree(t, root)
	beforeIdentities := documentPublicationIdentities(t, root)

	// Act again.
	repeatedWritten, repeatedErr := regenerateIndexesForTest(root)
	afterRetry := documentSessionCaptureTree(t, root)

	// Assert restart validates the existing A without allocating or mutating.
	if repeatedWritten != nil {
		t.Fatalf("repeated written=%#v, want nil", repeatedWritten)
	}
	var repeatedRecovery recoverableIndexBackupError
	if repeatedErr == nil || !errors.As(repeatedErr, &repeatedRecovery) ||
		repeatedRecovery.destination != firstRecovery.destination ||
		repeatedRecovery.backup != firstRecovery.backup {
		t.Fatalf("repeated error=%v, want same typed classification %#v", repeatedErr, firstRecovery)
	}
	afterForeignInfo, err := os.Lstat(targetPath)
	if err != nil || !os.SameFile(foreignInfo, afterForeignInfo) {
		t.Fatalf("foreign target identity changed: before=%v after=%v error=%v", foreignInfo, afterForeignInfo, err)
	}
	assertFileBytes(t, targetPath, foreign)
	documentSessionAssertTreeSnapshotEqual(t, beforeRetry, afterRetry)
	documentAssertPublicationIdentities(t, root, beforeIdentities)
}

func TestIndexBlockedAnchoredRestartRejectsMissingTargetWithoutMutation(t *testing.T) {
	// Arrange a blocked target with an independent A, then prove a regular S
	// replacement remains a soft blocker while the foreign target is present.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	targetRelative := "nested/index.md"
	targetPath := filepath.Join(root, filepath.FromSlash(targetRelative))
	original := []byte("# Original nested index\n")
	foreign := []byte("# Concurrent foreign index\n")
	if err := os.WriteFile(targetPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	hooks := indexPublishHooks{
		afterVacate: func(relative string, parent *os.Root, leaf, _ string) error {
			if relative != targetRelative {
				return nil
			}
			return parent.WriteFile(leaf, foreign, 0o640)
		},
	}
	written, publicationErr := regenerateIndexesWithTestHooks(root, nil, "", hooks)
	var firstSoft recoverableIndexBackupError
	if written != nil ||
		publicationErr == nil ||
		!errors.As(publicationErr, &firstSoft) {
		t.Fatalf(
			"blocked publication = %#v, %v; want typed soft blocker",
			written,
			publicationErr,
		)
	}
	manifest := readIndexBatchManifestFixture(t, root)
	entry := indexBatchManifestEntryByPath(t, manifest, targetRelative)
	if err := mutateIndexCurrentEvidencePath(
		filepath.Join(root, filepath.FromSlash(entry.Stage)),
		"replacement",
	); err != nil {
		t.Fatal(err)
	}
	beforeSoft := documentSessionCaptureTree(t, root)
	softWritten, softErr := regenerateIndexesForTest(root)
	var repeatedSoft recoverableIndexBackupError
	if softWritten != nil ||
		softErr == nil ||
		!errors.As(softErr, &repeatedSoft) ||
		repeatedSoft.destination != firstSoft.destination ||
		repeatedSoft.backup != firstSoft.backup {
		t.Fatalf(
			"regular-drift restart = %#v, %v; want stable soft blocker %#v",
			softWritten,
			softErr,
			firstSoft,
		)
	}
	documentSessionAssertTreeSnapshotEqual(
		t,
		beforeSoft,
		documentSessionCaptureTree(t, root),
	)

	// Act: the foreign target disappears between restarts.
	if err := os.Remove(targetPath); err != nil {
		t.Fatal(err)
	}
	tokenInfo := func(relative string) os.FileInfo {
		t.Helper()
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
		if err == nil {
			return info
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		return nil
	}
	stageInfo := tokenInfo(entry.Stage)
	anchorInfo := tokenInfo(entry.Anchor)
	newInstallInfo := tokenInfo(entry.NewInstall)
	restoreInfo := tokenInfo(entry.RestoreInstall)
	discardInfo := tokenInfo(entry.Discard)
	exactPreNewConsumer := newInstallInfo != nil &&
		stageInfo != nil &&
		os.SameFile(newInstallInfo, stageInfo)
	exactPublishedConsumer := restoreInfo != nil &&
		anchorInfo != nil &&
		os.SameFile(restoreInfo, anchorInfo) &&
		discardInfo != nil &&
		stageInfo != nil &&
		os.SameFile(discardInfo, stageInfo)
	if exactPreNewConsumer || exactPublishedConsumer {
		t.Fatalf(
			"blocked missing target unexpectedly has an exact reachable consumer: N~S=%t R~A+D~S=%t",
			exactPreNewConsumer,
			exactPublishedConsumer,
		)
	}
	beforeHard := documentSessionCaptureTree(t, root)
	hardWritten, hardErr := regenerateIndexesForTest(root)
	afterHard := documentSessionCaptureTree(t, root)

	// Assert missing target is a hard structural transition. Recovery neither
	// restores A nor consumes any evidence on this call.
	var hard unrecoverableIndexRecoveryError
	var unexpectedSoft recoverableIndexBackupError
	if hardWritten != nil ||
		hardErr == nil ||
		!errors.As(hardErr, &hard) ||
		errors.As(hardErr, &unexpectedSoft) {
		t.Fatalf(
			"missing-target restart = %#v, %v; want only hard structural error",
			hardWritten,
			hardErr,
		)
	}
	if _, err := os.Lstat(targetPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing foreign target was recreated: %v", err)
	}
	documentSessionAssertTreeSnapshotEqual(t, beforeHard, afterHard)
}

func TestIndexRecoveryTreatsPostAnchorEvidenceDriftAsSoftWithOccupiedTarget(t *testing.T) {
	mutationForms := indexCurrentEvidenceMutationForms()
	if len(mutationForms) != 5 {
		t.Fatalf("post-anchor evidence mutation forms=%d, want 5", len(mutationForms))
	}
	roles := []string{"S", "W", "B", "C"}
	caseCount := 0
	for _, role := range roles {
		role := role
		for _, mutationForm := range mutationForms {
			mutationForm := mutationForm
			caseCount++
			t.Run(role+"/"+mutationForm, func(t *testing.T) {
				// Arrange: leave a durable independent A and a foreign target occupant.
				root := t.TempDir()
				writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
				targetRelative := "nested/index.md"
				targetPath := filepath.Join(root, filepath.FromSlash(targetRelative))
				original := []byte("# Original nested index\n")
				foreign := []byte("# Concurrent foreign index\n")
				if err := os.WriteFile(targetPath, original, 0o600); err != nil {
					t.Fatal(err)
				}
				occupied := false
				hooks := indexPublishHooks{
					afterVacate: func(relative string, parent *os.Root, leaf, _ string) error {
						if relative != targetRelative {
							return nil
						}
						occupied = true
						return parent.WriteFile(leaf, foreign, 0o640)
					},
				}

				written, publicationErr := regenerateIndexesWithTestHooks(root, nil, "", hooks)
				if !occupied || written != nil {
					t.Fatalf(
						"occupied=%t written=%#v publication error=%v",
						occupied,
						written,
						publicationErr,
					)
				}
				var firstRecovery recoverableIndexBackupError
				if publicationErr == nil || !errors.As(publicationErr, &firstRecovery) {
					t.Fatalf("publication error=%v, want typed recoverableIndexBackupError", publicationErr)
				}
				manifest := readIndexBatchManifestFixture(t, root)
				entry := indexBatchManifestEntryByPath(t, manifest, targetRelative)
				if firstRecovery.destination != targetRelative ||
					firstRecovery.backup != entry.Anchor {
					t.Fatalf(
						"typed recovery=%#v, want destination=%q anchor=%q",
						firstRecovery,
						targetRelative,
						entry.Anchor,
					)
				}
				manifestNames := indexBatchManifestFixtureNames(t, root)
				if len(manifestNames) != 1 {
					t.Fatalf("batch manifests=%#v, want one", manifestNames)
				}
				retained := map[string]string{
					"M": manifestNames[0],
					"S": entry.Stage,
					"B": entry.Backup,
					"C": entry.Claim,
					"W": entry.Witness,
					"A": entry.Anchor,
				}
				for kind, relative := range retained {
					if relative == "" {
						t.Fatalf("%s evidence path is empty", kind)
					}
					if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
						t.Fatalf("%s evidence %q was not retained: %v", kind, relative, err)
					}
				}

				targetInfo, err := os.Lstat(targetPath)
				if err != nil {
					t.Fatal(err)
				}
				evidencePath := filepath.Join(root, filepath.FromSlash(retained[role]))
				evidenceInfoBeforeDrift, err := os.Lstat(evidencePath)
				if err != nil {
					t.Fatal(err)
				}
				if err := mutateIndexCurrentEvidencePath(evidencePath, mutationForm); err != nil {
					t.Fatal(err)
				}
				anchorPath := filepath.Join(root, filepath.FromSlash(entry.Anchor))
				anchorInfo, err := os.Lstat(anchorPath)
				if err != nil {
					t.Fatal(err)
				}
				if os.SameFile(targetInfo, anchorInfo) ||
					os.SameFile(evidenceInfoBeforeDrift, anchorInfo) {
					t.Fatalf(
						"independent A aliases target or original %s: target=%v evidence=%v A=%v",
						role,
						targetInfo,
						evidenceInfoBeforeDrift,
						anchorInfo,
					)
				}
				assertFileBytes(t, targetPath, foreign)
				assertFileBytes(t, anchorPath, original)
				if got := indexPreservedMode(anchorInfo.Mode()); got != 0o600 {
					t.Fatalf("A mode=%v, want 0600", got)
				}
				beforeRecovery := documentSessionCaptureTree(t, root)
				beforeIdentities := documentPublicationIdentities(t, root)

				// Act: ordinary recovery sees post-A S drift while A and target stay exact.
				recovered, recoveryErr := regenerateIndexesForTest(root)

				// Assert: stable regular content/identity drift is soft after
				// exact A. Removing the role or swapping its filesystem type is
				// a structural inventory violation and remains hard.
				if recovered != nil {
					t.Fatalf("recovered=%#v, want nil", recovered)
				}
				structural := mutationForm == "missing" || mutationForm == "type swap"
				if structural {
					var hard unrecoverableIndexRecoveryError
					var soft recoverableIndexBackupError
					if recoveryErr == nil ||
						!errors.As(recoveryErr, &hard) ||
						errors.As(recoveryErr, &soft) {
						t.Fatalf(
							"recovery error=%v, want only hard structural classification",
							recoveryErr,
						)
					}
				} else {
					var repeatedRecovery recoverableIndexBackupError
					if recoveryErr == nil || !errors.As(recoveryErr, &repeatedRecovery) ||
						repeatedRecovery.destination != firstRecovery.destination ||
						repeatedRecovery.backup != firstRecovery.backup {
						t.Fatalf(
							"recovery error=%v, want same typed classification %#v",
							recoveryErr,
							firstRecovery,
						)
					}
				}
				documentSessionAssertTreeSnapshotEqual(
					t,
					beforeRecovery,
					documentSessionCaptureTree(t, root),
				)
				documentAssertPublicationIdentities(t, root, beforeIdentities)
				afterTargetInfo, err := os.Lstat(targetPath)
				if err != nil || !os.SameFile(targetInfo, afterTargetInfo) {
					t.Fatalf(
						"foreign target identity changed: before=%v after=%v error=%v",
						targetInfo,
						afterTargetInfo,
						err,
					)
				}
				assertFileBytes(t, targetPath, foreign)
				afterAnchorInfo, err := os.Lstat(anchorPath)
				if err != nil || !os.SameFile(anchorInfo, afterAnchorInfo) {
					t.Fatalf(
						"A identity changed: before=%v after=%v error=%v",
						anchorInfo,
						afterAnchorInfo,
						err,
					)
				}
				assertFileBytes(t, anchorPath, original)
			})
		}
	}
	if caseCount != 20 {
		t.Fatalf("post-anchor evidence recovery cases=%d, want 20", caseCount)
	}
}

func TestIndexRecoveryRejectsOtherEntryDegradationAlongsideBlockedExactAnchor(t *testing.T) {
	// Arrange a blocked nested destination with an exact independent A.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	targetRelative := "nested/index.md"
	targetPath := filepath.Join(root, filepath.FromSlash(targetRelative))
	original := []byte("# Original nested index\n")
	foreign := []byte("# Concurrent foreign index\n")
	if err := os.WriteFile(targetPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	hooks := indexPublishHooks{
		afterVacate: func(relative string, parent *os.Root, leaf, _ string) error {
			if relative != targetRelative {
				return nil
			}
			return parent.WriteFile(leaf, foreign, 0o640)
		},
	}
	written, publicationErr := regenerateIndexesWithTestHooks(root, nil, "", hooks)
	var blocked recoverableIndexBackupError
	if written != nil || publicationErr == nil || !errors.As(publicationErr, &blocked) {
		t.Fatalf(
			"written=%#v publication error=%v, want blocked exact-A recovery",
			written,
			publicationErr,
		)
	}
	manifest := readIndexBatchManifestFixture(t, root)
	blockedEntry := indexBatchManifestEntryByPath(t, manifest, targetRelative)
	otherEntry := indexBatchManifestEntryByPath(t, manifest, indexFilename)
	if otherEntry.Path == blockedEntry.Path || otherEntry.Stage == "" {
		t.Fatalf(
			"other entry is not independent: blocked=%#v other=%#v",
			blockedEntry,
			otherEntry,
		)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	anchorPath := filepath.Join(root, filepath.FromSlash(blockedEntry.Anchor))
	anchorInfo, err := os.Lstat(anchorPath)
	if err != nil || os.SameFile(targetInfo, anchorInfo) {
		t.Fatalf(
			"blocked target and exact A are not independent: target=%v A=%v error=%v",
			targetInfo,
			anchorInfo,
			err,
		)
	}
	assertFileBytes(t, targetPath, foreign)
	assertFileBytes(t, anchorPath, original)

	otherStagePath := filepath.Join(root, filepath.FromSlash(otherEntry.Stage))
	otherStageInfo, err := os.Lstat(otherStagePath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(targetInfo, otherStageInfo) ||
		os.SameFile(anchorInfo, otherStageInfo) {
		t.Fatalf(
			"other-entry S aliases blocked target or A: target=%v A=%v otherS=%v",
			targetInfo,
			anchorInfo,
			otherStageInfo,
		)
	}
	if err := mutateIndexCurrentEvidencePath(otherStagePath, "replacement"); err != nil {
		t.Fatal(err)
	}
	beforeRecovery := documentSessionCaptureTree(t, root)
	beforeIdentities := documentPublicationIdentities(t, root)

	// Act.
	recovered, recoveryErr := regenerateIndexesForTest(root)

	// Assert other-entry degradation remains hard and recovery is zero mutation.
	if recovered != nil || recoveryErr == nil {
		t.Fatalf("recovered=%#v recovery error=%v, want hard conflict", recovered, recoveryErr)
	}
	var hard unrecoverableIndexRecoveryError
	if !errors.As(recoveryErr, &hard) {
		t.Fatalf("recovery error=%v, want unrecoverableIndexRecoveryError", recoveryErr)
	}
	var soft recoverableIndexBackupError
	if errors.As(recoveryErr, &soft) {
		t.Fatalf("recovery error=%v also contains unexpected soft classification %#v", recoveryErr, soft)
	}
	documentSessionAssertTreeSnapshotEqual(
		t,
		beforeRecovery,
		documentSessionCaptureTree(t, root),
	)
	documentAssertPublicationIdentities(t, root, beforeIdentities)
	afterTargetInfo, err := os.Lstat(targetPath)
	if err != nil || !os.SameFile(targetInfo, afterTargetInfo) {
		t.Fatalf(
			"foreign target changed: before=%v after=%v error=%v",
			targetInfo,
			afterTargetInfo,
			err,
		)
	}
	afterAnchorInfo, err := os.Lstat(anchorPath)
	if err != nil || !os.SameFile(anchorInfo, afterAnchorInfo) {
		t.Fatalf(
			"exact A changed: before=%v after=%v error=%v",
			anchorInfo,
			afterAnchorInfo,
			err,
		)
	}
}

func TestIndexRecoveryOccupiedRollbackAtAnchorCreateBoundaryIsRestartIdempotent(t *testing.T) {
	tests := []struct {
		name   string
		source string
		cut    string
	}{}
	for _, source := range []string{"C", "B"} {
		for _, cut := range indexCreateCrossDestinationCuts() {
			tests = append(tests, struct {
				name   string
				source string
				cut    string
			}{
				name:   "canonical " + source + " " + cut,
				source: source,
				cut:    cut,
			})
		}
	}
	if len(tests) != 16 {
		t.Fatalf("recovery occupancy cases=%d, want 16", len(tests))
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// Arrange a published precommit target whose rollback must create A.
			root := t.TempDir()
			before, _ := arrangeIndexBatchCrashBundle(t, root)
			runIndexBatchCrashHelper(t, root, "before-root")
			manifest := readIndexBatchManifestFixture(t, root)
			entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
			var driftedCData []byte
			if test.source == "B" {
				claimPath := filepath.Join(root, filepath.FromSlash(entry.Claim))
				if err := mutateIndexCurrentEvidencePath(claimPath, "in place"); err != nil {
					t.Fatal(err)
				}
				var err error
				driftedCData, err = os.ReadFile(claimPath)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Equal(driftedCData, before[entry.Path]) {
					t.Fatal("canonical C was not invalidated before recovery B selection")
				}
			}
			targetPath := filepath.Join(root, "b", indexFilename)
			foreign := []byte("# Recovery-time foreign occupant\n")
			boundaryReached := false
			occupied := false
			var foreignInfo os.FileInfo
			hooks := indexPublishHooks{
				afterVacate: func(
					relative string,
					parent *os.Root,
					leaf string,
					_ string,
				) error {
					if relative != entry.Path {
						return nil
					}
					occupied = true
					if err := parent.WriteFile(leaf, foreign, 0o640); err != nil {
						return err
					}
					var err error
					foreignInfo, err = parent.Lstat(leaf)
					return err
				},
			}
			configureIndexCreateCrossDestinationCut(
				&hooks,
				test.cut,
				entry.Path,
				func() error {
					boundaryReached = true
					claimInfo, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Claim)))
					if err != nil {
						return err
					}
					witnessInfo, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Witness)))
					if err != nil {
						return err
					}
					backupInfo, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Backup)))
					if err != nil {
						return err
					}
					if !os.SameFile(claimInfo, witnessInfo) ||
						os.SameFile(claimInfo, backupInfo) {
						t.Fatalf(
							"recovery C source is not C/W with independent B: C=%v W=%v B=%v",
							claimInfo,
							witnessInfo,
							backupInfo,
						)
					}
					wantClaim := before[entry.Path]
					if test.source == "B" {
						wantClaim = driftedCData
					}
					assertFileBytes(
						t,
						filepath.Join(root, filepath.FromSlash(entry.Claim)),
						wantClaim,
					)
					assertFileBytes(
						t,
						filepath.Join(root, filepath.FromSlash(entry.Backup)),
						before[entry.Path],
					)
					return nil
				},
			)

			// Act.
			recoveryErr := recoverIndexBatchFixtureWithHooks(root, hooks)

			// Assert A is durable and the recovery-time occupant is untouched.
			if !boundaryReached || !occupied || recoveryErr == nil {
				t.Fatalf(
					"boundary=%t occupied=%t recovery error=%v, want occupied rollback conflict; artifacts:\n%s",
					boundaryReached,
					occupied,
					recoveryErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			targetInfo, err := os.Lstat(targetPath)
			if err != nil || foreignInfo == nil || !os.SameFile(foreignInfo, targetInfo) {
				t.Fatalf(
					"foreign target changed: before=%v after=%v error=%v",
					foreignInfo,
					targetInfo,
					err,
				)
			}
			assertFileBytes(t, targetPath, foreign)
			anchorPath := filepath.Join(root, filepath.FromSlash(entry.Anchor))
			anchorInfo, err := os.Lstat(anchorPath)
			if err != nil || os.SameFile(targetInfo, anchorInfo) {
				t.Fatalf(
					"independent A is unavailable: target=%v A=%v error=%v",
					targetInfo,
					anchorInfo,
					err,
				)
			}
			assertFileBytes(t, anchorPath, before[entry.Path])
			for _, relative := range []string{
				entry.Stage,
				entry.Backup,
				entry.Claim,
				entry.Witness,
				entry.Anchor,
			} {
				if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(relative))); statErr != nil {
					t.Fatalf("recovery evidence %q was not retained: %v", relative, statErr)
				}
			}
			if manifests := indexBatchManifestFixtureNames(t, root); len(manifests) != 1 {
				t.Fatalf("batch manifests=%#v, want retained M", manifests)
			}
			beforeRetry := documentSessionCaptureTree(t, root)
			beforeIdentities := documentPublicationIdentities(t, root)

			// Act again.
			repeatedWritten, repeatedErr := regenerateIndexesForTest(root)

			// Assert restart is the stable typed conflict and exact zero mutation.
			if repeatedWritten != nil {
				t.Fatalf("repeated written=%#v, want nil", repeatedWritten)
			}
			var repeatedRecovery recoverableIndexBackupError
			if repeatedErr == nil || !errors.As(repeatedErr, &repeatedRecovery) ||
				repeatedRecovery.destination != entry.Path ||
				repeatedRecovery.backup != entry.Anchor {
				t.Fatalf(
					"repeated error=%v, want typed recovery for %q via %q",
					repeatedErr,
					entry.Path,
					entry.Anchor,
				)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				beforeRetry,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, beforeIdentities)
		})
	}
}

func TestIndexActiveRejectsOccupiedTargetDriftAtAnchorCreateBoundary(t *testing.T) {
	tests := []struct {
		name string
		cut  string
		form string
	}{}
	for _, cut := range indexCreateCrossDestinationCuts() {
		for _, form := range indexCurrentEvidenceMutationForms() {
			tests = append(tests, struct {
				name string
				cut  string
				form string
			}{
				name: cut + "/" + form,
				cut:  cut,
				form: form,
			})
		}
	}
	if len(tests) != 40 {
		t.Fatalf("active hard target drift cases=%d, want 40", len(tests))
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
			targetRelative := "nested/index.md"
			targetPath := filepath.Join(root, filepath.FromSlash(targetRelative))
			if err := os.WriteFile(targetPath, []byte("# Original nested index\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			foreign := []byte("# Concurrent foreign index\n")
			occupied := false
			attacked := false
			var cutTree map[string]documentSessionTreeEntry
			var cutIdentities map[string]os.FileInfo
			hooks := indexPublishHooks{
				afterVacate: func(relative string, parent *os.Root, leaf, _ string) error {
					if relative != targetRelative {
						return nil
					}
					occupied = true
					return parent.WriteFile(leaf, foreign, 0o640)
				},
			}
			configureIndexCreateCrossDestinationCut(
				&hooks,
				test.cut,
				targetRelative,
				func() error {
					if err := mutateIndexCurrentEvidencePath(targetPath, test.form); err != nil {
						return err
					}
					attacked = true
					cutTree = documentSessionCaptureTree(t, root)
					cutIdentities = documentPublicationIdentities(t, root)
					return nil
				},
			)

			// Act.
			written, publicationErr := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert no action after the hard target drift changed the cut.
			if !occupied || !attacked || written != nil ||
				publicationErr == nil ||
				!errors.Is(publicationErr, errPublicationScopeInvalid) {
				t.Fatalf(
					"occupied=%t attacked=%t written=%#v error=%v, want hard target conflict",
					occupied,
					attacked,
					written,
					publicationErr,
				)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				cutTree,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, cutIdentities)
			if manifests := indexBatchManifestFixtureNames(t, root); len(manifests) != 1 {
				t.Fatalf("batch manifests=%#v, want retained M", manifests)
			}
			legalPreNewResume := test.cut == "after directory sync" &&
				test.form == "missing"
			if legalPreNewResume {
				manifest := readIndexBatchManifestFixture(t, root)
				entry := indexBatchManifestEntryByPath(t, manifest, targetRelative)
				newInstall, newErr := os.Lstat(filepath.Join(
					root,
					filepath.FromSlash(entry.NewInstall),
				))
				stage, stageErr := os.Lstat(filepath.Join(
					root,
					filepath.FromSlash(entry.Stage),
				))
				if newErr != nil ||
					stageErr != nil ||
					!os.SameFile(newInstall, stage) ||
					inspectIndexRecoveryExistingRollbackEntry(t, root, targetRelative) !=
						indexBatchV3PhasePreNewRollbackAnchored {
					t.Fatalf(
						"late missing target lacks exact legal N~S pre-new phase: N=%v/%v S=%v/%v",
						newInstall,
						newErr,
						stage,
						stageErr,
					)
				}
			}
			beforeRetry := documentSessionCaptureTree(t, root)
			beforeRetryIdentities := documentPublicationIdentities(t, root)

			// Act again.
			repeatedWritten, repeatedErr := regenerateIndexesForTest(root)

			if legalPreNewResume {
				if repeatedErr != nil || len(repeatedWritten) != 2 {
					t.Fatalf(
						"legal pre-new restart = %#v, %v; want two published indexes",
						repeatedWritten,
						repeatedErr,
					)
				}
				documentSessionAssertNoPublicationArtifacts(t, root)
				return
			}

			// Assert every non-reachable restart is also hard and exactly zero mutation.
			if repeatedWritten != nil || repeatedErr == nil {
				t.Fatalf(
					"repeated written=%#v error=%v, want hard zero-mutation conflict",
					repeatedWritten,
					repeatedErr,
				)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				beforeRetry,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, beforeRetryIdentities)
		})
	}
}

func TestIndexOccupiedRollbackAtEveryAnchorCreateBoundaryIsRestartIdempotent(t *testing.T) {
	tests := []struct {
		name   string
		source string
		cut    string
	}{}
	for _, source := range []string{"C", "B"} {
		for _, cut := range indexCreateCrossDestinationCuts() {
			tests = append(tests, struct {
				name   string
				source string
				cut    string
			}{
				name:   "active canonical " + source + " " + cut,
				source: source,
				cut:    cut,
			})
		}
	}
	if len(tests) != 16 {
		t.Fatalf("active occupancy cases=%d, want 16", len(tests))
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
			targetRelative := "nested/index.md"
			targetPath := filepath.Join(root, filepath.FromSlash(targetRelative))
			original := []byte("# Original nested index\n")
			foreign := []byte("# Concurrent foreign index\n")
			if err := os.WriteFile(targetPath, original, 0o600); err != nil {
				t.Fatal(err)
			}

			var manifest indexBatchManifest
			var manifestPath string
			var foreignInfo os.FileInfo
			var driftedCInfo os.FileInfo
			var driftedCData []byte
			occupied := false
			boundaryReached := false
			hooks := indexPublishHooks{
				afterBatchManifestDurable: func(_ *os.Root, name string) error {
					manifestPath = filepath.Join(root, name)
					data, err := os.ReadFile(manifestPath)
					if err != nil {
						return err
					}
					return json.Unmarshal(data, &manifest)
				},
				afterVacate: func(relative string, parent *os.Root, leaf, _ string) error {
					if relative != targetRelative {
						return nil
					}
					if test.source == "B" {
						entry := indexBatchManifestEntryByPath(t, manifest, targetRelative)
						claimPath := filepath.Join(root, filepath.FromSlash(entry.Claim))
						if err := mutateIndexCrossDestinationPath(claimPath, false); err != nil {
							return err
						}
						var err error
						driftedCInfo, err = os.Lstat(claimPath)
						if err != nil {
							return err
						}
						driftedCData, err = os.ReadFile(claimPath)
						if err != nil {
							return err
						}
						if bytes.Equal(driftedCData, original) {
							return errors.New("canonical C was not invalidated before B selection")
						}
					}
					occupied = true
					if err := parent.WriteFile(leaf, foreign, 0o640); err != nil {
						return err
					}
					var err error
					foreignInfo, err = parent.Lstat(leaf)
					return err
				},
			}
			configureIndexCreateCrossDestinationCut(
				&hooks,
				test.cut,
				targetRelative,
				func() error {
					boundaryReached = true
					entry := indexBatchManifestEntryByPath(t, manifest, targetRelative)

					targetInfo, err := os.Lstat(targetPath)
					if err != nil || foreignInfo == nil || !os.SameFile(foreignInfo, targetInfo) {
						t.Fatalf(
							"foreign target at first A-create boundary: before=%v after=%v error=%v",
							foreignInfo,
							targetInfo,
							err,
						)
					}
					assertFileBytes(t, targetPath, foreign)

					paths := map[string]string{
						"S": entry.Stage,
						"B": entry.Backup,
						"C": entry.Claim,
						"W": entry.Witness,
						"A": entry.Anchor,
					}
					seenNames := map[string]string{entry.Path: "target"}
					infos := make(map[string]os.FileInfo, len(paths))
					for kind, relative := range paths {
						if relative == "" || relative == entry.Path {
							t.Fatalf("%s path=%q is not disjoint from target %q", kind, relative, entry.Path)
						}
						if previous, duplicate := seenNames[relative]; duplicate {
							t.Fatalf("%s path=%q aliases %s by name", kind, relative, previous)
						}
						seenNames[relative] = kind
						if kind == "A" {
							continue
						}
						info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
						if statErr != nil {
							t.Fatalf("inspect %s %q: %v", kind, relative, statErr)
						}
						infos[kind] = info
						if os.SameFile(targetInfo, info) {
							t.Fatalf("foreign target aliases %s inode: target=%v %s=%v", kind, targetInfo, kind, info)
						}
					}
					if !os.SameFile(infos["C"], infos["W"]) ||
						os.SameFile(infos["C"], infos["B"]) {
						t.Fatalf(
							"canonical source identity is not C/W with independent B: C=%v W=%v B=%v",
							infos["C"],
							infos["W"],
							infos["B"],
						)
					}
					assertFileBytes(
						t,
						filepath.Join(root, filepath.FromSlash(paths["B"])),
						original,
					)
					if got := indexPreservedMode(infos["B"].Mode()); got != 0o600 {
						t.Fatalf("B mode=%v, want 0600", got)
					}
					for _, kind := range []string{"C", "W"} {
						want := original
						if test.source == "B" {
							want = driftedCData
						}
						assertFileBytes(
							t,
							filepath.Join(root, filepath.FromSlash(paths[kind])),
							want,
						)
						if got := indexPreservedMode(infos[kind].Mode()); got != 0o600 {
							t.Fatalf("%s mode=%v, want 0600", kind, got)
						}
					}
					if test.source == "B" {
						if driftedCInfo == nil || !os.SameFile(driftedCInfo, infos["C"]) {
							t.Fatalf(
								"invalid canonical C identity changed before B selection: before=%v after=%v",
								driftedCInfo,
								infos["C"],
							)
						}
					}
					return nil
				},
			)

			// Act.
			written, publicationErr := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if !occupied || !boundaryReached || written != nil {
				t.Fatalf(
					"occupied=%t boundaryReached=%t written=%#v error=%v",
					occupied,
					boundaryReached,
					written,
					publicationErr,
				)
			}
			var firstRecovery recoverableIndexBackupError
			if publicationErr == nil || !errors.As(publicationErr, &firstRecovery) {
				t.Fatalf("publication error=%v, want typed recoverableIndexBackupError", publicationErr)
			}
			entry := indexBatchManifestEntryByPath(t, manifest, targetRelative)
			if firstRecovery.destination != targetRelative ||
				firstRecovery.backup != entry.Anchor {
				t.Fatalf(
					"typed recovery=%#v, want destination=%q anchor=%q",
					firstRecovery,
					targetRelative,
					entry.Anchor,
				)
			}
			if manifestPath == "" {
				t.Fatal("batch manifest path was not captured")
			}

			targetInfo, err := os.Lstat(targetPath)
			if err != nil || foreignInfo == nil || !os.SameFile(foreignInfo, targetInfo) {
				t.Fatalf(
					"foreign target identity changed: before=%v after=%v error=%v",
					foreignInfo,
					targetInfo,
					err,
				)
			}
			if !targetInfo.Mode().IsRegular() ||
				indexPreservedMode(targetInfo.Mode()) != 0o640 {
				t.Fatalf("foreign target mode/type=%v, want regular 0640", targetInfo.Mode())
			}
			assertFileBytes(t, targetPath, foreign)
			if test.source == "B" {
				claimPath := filepath.Join(root, filepath.FromSlash(entry.Claim))
				claimInfo, statErr := os.Lstat(claimPath)
				if statErr != nil || driftedCInfo == nil || !os.SameFile(driftedCInfo, claimInfo) {
					t.Fatalf(
						"invalid canonical C identity changed: before=%v after=%v error=%v",
						driftedCInfo,
						claimInfo,
						statErr,
					)
				}
				assertFileBytes(t, claimPath, driftedCData)
			}

			retained := map[string]string{
				"M": filepath.ToSlash(strings.TrimPrefix(manifestPath, root+string(filepath.Separator))),
				"S": entry.Stage,
				"B": entry.Backup,
				"C": entry.Claim,
				"W": entry.Witness,
				"A": entry.Anchor,
			}
			seenNames := map[string]string{entry.Path: "target"}
			for kind, relative := range retained {
				if relative == "" {
					t.Fatalf("%s evidence path is empty", kind)
				}
				if previous, duplicate := seenNames[relative]; duplicate {
					t.Fatalf("%s evidence path=%q aliases %s by name", kind, relative, previous)
				}
				seenNames[relative] = kind
				info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
				if statErr != nil {
					t.Fatalf("%s evidence %q was not retained: %v", kind, relative, statErr)
				}
				if os.SameFile(targetInfo, info) {
					t.Fatalf("foreign target aliases retained %s inode: target=%v %s=%v", kind, targetInfo, kind, info)
				}
			}
			anchorPath := filepath.Join(root, filepath.FromSlash(entry.Anchor))
			anchorInfo, err := os.Lstat(anchorPath)
			if err != nil {
				t.Fatal(err)
			}
			assertFileBytes(t, anchorPath, original)
			if got := indexPreservedMode(anchorInfo.Mode()); got != 0o600 {
				t.Fatalf("A mode=%v, want 0600", got)
			}
			for _, relative := range []string{
				entry.Stage,
				entry.Backup,
				entry.Claim,
				entry.Witness,
			} {
				info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
				if statErr != nil || os.SameFile(anchorInfo, info) {
					t.Fatalf("A aliases %q: A=%v other=%v error=%v", relative, anchorInfo, info, statErr)
				}
			}

			beforeRetry := documentSessionCaptureTree(t, root)
			beforeIdentities := documentPublicationIdentities(t, root)

			// Act again.
			repeatedWritten, repeatedErr := regenerateIndexesForTest(root)

			// Assert restart is an exact zero-mutation typed conflict.
			if repeatedWritten != nil {
				t.Fatalf("repeated written=%#v, want nil", repeatedWritten)
			}
			var repeatedRecovery recoverableIndexBackupError
			if repeatedErr == nil || !errors.As(repeatedErr, &repeatedRecovery) ||
				repeatedRecovery.destination != firstRecovery.destination ||
				repeatedRecovery.backup != firstRecovery.backup {
				t.Fatalf("repeated error=%v, want same typed classification %#v", repeatedErr, firstRecovery)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				beforeRetry,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, beforeIdentities)
		})
	}
}

func TestIndexActiveRollbackRestoresOldTargetOnlyFromIndependentAnchor(t *testing.T) {
	tests := []struct {
		name  string
		fault func(relative string, hooks *indexPublishHooks, injected error)
	}{
		{
			name: "hook error after durable vacate",
			fault: func(relative string, hooks *indexPublishHooks, injected error) {
				hooks.afterVacate = func(got string, _ *os.Root, _, _ string) error {
					if got == relative {
						return injected
					}
					return nil
				}
			},
		},
		{
			name: "post-install-link failure after vacate",
			fault: func(relative string, hooks *indexPublishHooks, injected error) {
				hooks.afterInstallLink = func(got string, _ *os.Root, _, _ string) error {
					if got == relative {
						return injected
					}
					return nil
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
			targetPath := filepath.Join(root, "nested", indexFilename)
			original := []byte("# Original nested index\n")
			if err := os.WriteFile(targetPath, original, 0o604); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected active rollback boundary")
			var anchorInfo os.FileInfo
			var nonAnchorInfos []os.FileInfo
			captured := false
			hooks := indexPublishHooks{}
			test.fault("nested/index.md", &hooks, injected)
			hooks.afterRestoreLink = func(
				relative string,
				_ *os.Root,
				_, _ string,
			) error {
				if captured || relative != "nested/index.md" {
					return nil
				}
				manifest := readIndexBatchManifestFixture(t, root)
				entry := indexBatchManifestEntryByPath(t, manifest, "nested/index.md")
				var err error
				anchorInfo, err = os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Anchor)))
				if err != nil {
					t.Fatal(err)
				}
				for _, evidence := range []string{
					entry.Witness,
					entry.Backup,
					entry.Claim,
					entry.Stage,
				} {
					info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(evidence)))
					if statErr != nil {
						t.Fatal(statErr)
					}
					nonAnchorInfos = append(nonAnchorInfos, info)
				}
				targetInfo, statErr := os.Lstat(targetPath)
				if statErr != nil || !os.SameFile(targetInfo, anchorInfo) {
					t.Fatalf("rollback target is not exact A: target=%v A=%v error=%v", targetInfo, anchorInfo, statErr)
				}
				captured = true
				return nil
			}

			// Act.
			written, publicationErr := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if !captured || written != nil || !errors.Is(publicationErr, injected) {
				t.Fatalf("captured=%t written=%#v error=%v, want injected rollback", captured, written, publicationErr)
			}
			targetInfo, err := os.Lstat(targetPath)
			if err != nil || !os.SameFile(targetInfo, anchorInfo) {
				t.Fatalf("final rollback target is not unlinked A inode: target=%v A=%v error=%v", targetInfo, anchorInfo, err)
			}
			for _, info := range nonAnchorInfos {
				if os.SameFile(targetInfo, info) {
					t.Fatalf("rollback target aliases W/B/C/S evidence: target=%v evidence=%v", targetInfo, info)
				}
			}
			assertFileBytes(t, targetPath, original)
			if got := indexPreservedMode(targetInfo.Mode()); got != 0o604 {
				t.Fatalf("restored target mode=%v, want 0604", got)
			}
			documentSessionAssertNoPublicationArtifacts(t, root)
		})
	}
}

func TestIndexBatchDegradedClaimRepairRejectsSameBytesDifferentInodeTarget(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	arrangeIndexBatchCrashBundle(t, root)
	runIndexBatchCrashHelper(t, root, "mid-vacate")
	manifest := readIndexBatchManifestFixture(t, root)
	entry := missingIndexBatchManifestEntry(t, root, manifest)
	claimPath := filepath.Join(root, filepath.FromSlash(entry.Claim))
	if err := os.WriteFile(claimPath, []byte("# Foreign claim\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(root, filepath.FromSlash(entry.Backup))
	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	backupInfo, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(root, filepath.FromSlash(entry.Path))
	if err := os.WriteFile(targetPath, backupData, fs.FileMode(entry.OldMode)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(targetPath, fs.FileMode(entry.OldMode)); err != nil {
		t.Fatal(err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(targetInfo, backupInfo) {
		t.Fatal("same-byte target unexpectedly aliases manifest-bound backup")
	}
	before := documentSessionCaptureTree(t, root)

	// Act.
	recoveryErr := recoverIndexBatchFixture(root)
	after := documentSessionCaptureTree(t, root)

	// Assert.
	if recoveryErr == nil {
		t.Fatal("recovery accepted same-byte target without anchor identity witness")
	}
	documentSessionAssertTreeSnapshotEqual(t, before, after)
}

func TestIndexBatchDegradedClaimRepairIsStableAcrossRestart(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	arrangeIndexBatchCrashBundle(t, root)
	runIndexBatchCrashHelper(t, root, "mid-vacate")
	manifest := readIndexBatchManifestFixture(t, root)
	entry := missingIndexBatchManifestEntry(t, root, manifest)
	claimPath := filepath.Join(root, filepath.FromSlash(entry.Claim))
	if err := os.WriteFile(claimPath, []byte("# Foreign claim\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := recoverIndexBatchFixture(root); err == nil ||
		!strings.Contains(err.Error(), "target restored from the exact independent rollback anchor") {
		t.Fatalf("first recovery error = %v", err)
	}
	if manifests := indexBatchManifestFixtureNames(t, root); len(manifests) != 1 {
		t.Fatalf("first recovery retained manifests=%#v, want one", manifests)
	}
	before := documentSessionCaptureTree(t, root)

	// Act.
	recoveryErr := recoverIndexBatchFixture(root)
	after := documentSessionCaptureTree(t, root)

	// Assert.
	if recoveryErr == nil ||
		!strings.Contains(recoveryErr.Error(), "target restored from the exact independent rollback anchor") {
		t.Fatalf("second recovery error = %v", recoveryErr)
	}
	if manifests := indexBatchManifestFixtureNames(t, root); len(manifests) != 1 {
		t.Fatalf("second recovery retained manifests=%#v, want one", manifests)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, after)
}

func TestIndexBatchManifestRejectsAmbiguousInFlightClaimsWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string, manifest indexBatchManifest)
	}{
		{
			name: "two simultaneous missing targets",
			mutate: func(t *testing.T, root string, manifest indexBatchManifest) {
				t.Helper()
				entry := indexBatchManifestEntryByPath(t, manifest, "a/index.md")
				if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Claim))); err != nil {
					t.Fatalf("expected first published claim: %v", err)
				}
				if err := os.Remove(filepath.Join(root, "a", indexFilename)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "claim exists for non-prefix target",
			mutate: func(t *testing.T, root string, manifest indexBatchManifest) {
				t.Helper()
				entry := indexBatchManifestEntryByPath(t, manifest, indexFilename)
				if err := os.Link(
					filepath.Join(root, indexFilename),
					filepath.Join(root, filepath.FromSlash(entry.Claim)),
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			arrangeIndexBatchCrashBundle(t, root)
			point := "mid-vacate"
			if test.name == "claim exists for non-prefix target" {
				point = "mid-vacate-first"
			}
			runIndexBatchCrashHelper(t, root, point)
			manifest := readIndexBatchManifestFixture(t, root)
			test.mutate(t, root, manifest)
			before := documentSessionCaptureTree(t, root)

			// Act.
			err := recoverIndexBatchFixture(root)
			after := documentSessionCaptureTree(t, root)

			// Assert.
			if err == nil {
				t.Fatal("global publication recovery accepted ambiguous batch")
			}
			documentSessionAssertTreeSnapshotEqual(t, before, after)
		})
	}
}

func TestIndexBatchManifestRejectsMissingExtraMismatchAndMultipleWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		point  string
		mutate func(t *testing.T, root string)
	}{
		{
			name:  "missing after publication started",
			point: "mid-vacate",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				names := indexBatchManifestFixtureNames(t, root)
				if len(names) != 1 {
					t.Fatalf("batch manifests=%#v, want one", names)
				}
				if err := os.Remove(filepath.Join(root, names[0])); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "extra transaction artifact",
			point: "manifest",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				writeIndexTransactionArtifact(
					t,
					root,
					"a/index.md",
					indexArtifactStage,
					[]byte("# Unbound extra stage\n"),
					0o640,
					9,
				)
			},
		},
		{
			name:  "manifest content mismatch",
			point: "manifest",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				names := indexBatchManifestFixtureNames(t, root)
				if len(names) != 1 {
					t.Fatalf("batch manifests=%#v, want one", names)
				}
				if err := os.WriteFile(filepath.Join(root, names[0]), []byte("{}\n"), indexBatchManifestMode); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "multiple manifests",
			point: "manifest",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				manifest := readIndexBatchManifestFixture(t, root)
				manifest.Entries[0].NewMode ^= 0o020
				data, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				data = append(data, '\n')
				if err := os.WriteFile(
					filepath.Join(root, indexBatchManifestName(data)),
					data,
					indexBatchManifestMode,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			arrangeIndexBatchCrashBundle(t, root)
			runIndexBatchCrashHelper(t, root, test.point)
			test.mutate(t, root)
			before := documentSessionCaptureTree(t, root)

			// Act.
			err := recoverIndexBatchFixture(root)
			after := documentSessionCaptureTree(t, root)

			// Assert.
			if err == nil {
				t.Fatal("global publication recovery accepted invalid manifest inventory")
			}
			documentSessionAssertTreeSnapshotEqual(t, before, after)
		})
	}
}

func TestIndexBatchManifestCrashAfterEveryCommittedCleanupBoundary(t *testing.T) {
	// Arrange an oracle trace so the test covers every actual protocol action,
	// including manifest removal and the terminal root-stage removal.
	traceRoot := t.TempDir()
	_, arrange := arrangeIndexBatchCrashBundle(t, traceRoot)
	var trace []string
	written, err := regenerateIndexesWithTestHooks(traceRoot, nil, "", indexPublishHooks{
		afterCleanup: func(relative string, _ *os.Root, kind, _ string) error {
			trace = append(trace, relative+":"+kind)
			return nil
		},
	})

	if err != nil || len(written) != 3 || len(trace) == 0 {
		t.Fatalf("trace publication=%#v, %v cleanup=%#v", written, err, trace)
	}
	planRoot := t.TempDir()
	arrange(t, planRoot)
	runIndexBatchCrashHelper(t, planRoot, "root-commit")
	wantTrace := indexBatchCanonicalCleanupTrace(t, planRoot)
	if !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("committed cleanup trace=%#v, want exact durable-evidence order %#v", trace, wantTrace)
	}
	after := make(map[string][]byte, len(written))
	for _, relative := range []string{"a/index.md", "b/index.md", "index.md"} {
		data, readErr := os.ReadFile(filepath.Join(traceRoot, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		after[relative] = data
	}

	for ordinal, action := range trace {
		ordinal, action := ordinal, action
		t.Run(strconv.Itoa(ordinal)+"/"+action, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			arrange(t, root)
			runIndexBatchCrashHelperAtOrdinal(t, root, "commit-cleanup", ordinal)

			// Act.
			err := recoverIndexBatchFixture(root)

			// Assert.
			if err != nil {
				t.Fatalf("recovery after committed cleanup[%d] %s: %v", ordinal, action, err)
			}
			for relative, data := range after {
				assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), data)
			}
			documentSessionAssertNoPublicationArtifacts(t, root)
		})
	}
}

func TestIndexBatchManifestCrashAfterEveryRollbackTargetAndCleanupBoundary(t *testing.T) {
	// Discover the exact rollback cleanup sequence without relying on an
	// implementation-specific count.
	traceRoot := t.TempDir()
	before, arrange := arrangeIndexBatchCrashBundle(t, traceRoot)
	injected := errors.New("force precommit rollback")
	var cleanupTrace []string
	written, err := regenerateIndexesWithTestHooks(traceRoot, nil, "", indexPublishHooks{
		beforeRootCommit: func(string, *os.Root, string) error {
			return injected
		},
		afterCleanup: func(relative string, _ *os.Root, kind, _ string) error {
			cleanupTrace = append(cleanupTrace, relative+":"+kind)
			return nil
		},
	})

	if written != nil || !errors.Is(err, injected) || len(cleanupTrace) == 0 {
		t.Fatalf("rollback trace=%#v, %v cleanup=%#v", written, err, cleanupTrace)
	}
	planRoot := t.TempDir()
	arrange(t, planRoot)
	runIndexBatchCrashHelper(t, planRoot, "rollback-cleanup-before-first")
	wantCleanupTrace := indexBatchCanonicalCleanupTrace(t, planRoot)
	if !reflect.DeepEqual(cleanupTrace, wantCleanupTrace) {
		t.Fatalf(
			"rollback cleanup trace=%#v, want proof-last canonical order %#v",
			cleanupTrace,
			wantCleanupTrace,
		)
	}
	for relative, data := range before {
		assertFileBytes(t, filepath.Join(traceRoot, filepath.FromSlash(relative)), data)
	}
	documentSessionAssertNoPublicationArtifacts(t, traceRoot)

	t.Run("active and restarted recovery share the canonical cleanup order", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		arrange(t, root)
		runIndexBatchCrashHelper(t, root, "before-root")
		var recoveryTrace []string
		err := recoverIndexBatchFixtureWithHooks(root, indexPublishHooks{
			afterCleanup: func(relative string, _ *os.Root, kind, _ string) error {
				recoveryTrace = append(recoveryTrace, relative+":"+kind)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("restart recovery error = %v", err)
		}
		if !reflect.DeepEqual(recoveryTrace, cleanupTrace) {
			t.Fatalf(
				"restart cleanup trace=%#v, want active canonical trace %#v",
				recoveryTrace,
				cleanupTrace,
			)
		}
	})

	// Both non-root existing destinations were published before the root
	// commit boundary, so reverse recovery has exactly two vacate→restore
	// actions. Crash after vacating New but before installing Old.
	t.Run("cleanup/before-first", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		arrange(t, root)
		runIndexBatchCrashHelper(t, root, "rollback-cleanup-before-first")
		if err := recoverIndexBatchFixture(root); err != nil {
			t.Fatalf("recovery before first cleanup: %v", err)
		}
		for relative, data := range before {
			assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), data)
		}
		documentSessionAssertNoPublicationArtifacts(t, root)
	})

	for ordinal := 0; ordinal < 2; ordinal++ {
		ordinal := ordinal
		t.Run("target-vacate/"+strconv.Itoa(ordinal), func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			arrange(t, root)
			runIndexBatchCrashHelper(t, root, "before-root")
			runIndexBatchCrashHelperAtOrdinal(t, root, "rollback-vacate", ordinal)
			if err := recoverIndexBatchFixture(root); err != nil {
				t.Fatalf("recovery after rollback target vacate[%d]: %v", ordinal, err)
			}
			for relative, data := range before {
				assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), data)
			}
			documentSessionAssertNoPublicationArtifacts(t, root)
		})
	}
	for ordinal, action := range cleanupTrace {
		ordinal, action := ordinal, action
		t.Run("cleanup/"+strconv.Itoa(ordinal)+"/"+action, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			arrange(t, root)
			runIndexBatchCrashHelperAtOrdinal(t, root, "rollback-cleanup", ordinal)
			if err := recoverIndexBatchFixture(root); err != nil {
				t.Fatalf("recovery after rollback cleanup[%d] %s: %v", ordinal, action, err)
			}
			for relative, data := range before {
				assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), data)
			}
			documentSessionAssertNoPublicationArtifacts(t, root)
		})
	}
}

func TestIndexBatchManifestCommittedRecoveryAcceptsOnlyCanonicalCleanupPrefix(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(t *testing.T, root string)
	}{
		{
			name: "non-prefix missing backup",
			arrange: func(t *testing.T, root string) {
				t.Helper()
				runIndexBatchCrashHelper(t, root, "root-commit")
				manifest := readIndexBatchManifestFixture(t, root)
				entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(entry.Backup))); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unbound extra artifact",
			arrange: func(t *testing.T, root string) {
				t.Helper()
				runIndexBatchCrashHelper(t, root, "root-commit")
				writeIndexTransactionArtifact(
					t,
					root,
					"a/index.md",
					indexArtifactStage,
					[]byte("# Unbound committed extra\n"),
					0o640,
					9,
				)
			},
		},
		{
			name: "terminal proof suffix plus extra artifact",
			arrange: func(t *testing.T, root string) {
				t.Helper()
				runIndexBatchCrashHelper(t, root, "terminal-proof-suffix")
				if names := indexBatchManifestFixtureNames(t, root); len(names) != 0 {
					t.Fatalf("manifest after terminal-prefix crash=%#v, want absent", names)
				}
				writeIndexTransactionArtifact(
					t,
					root,
					"a/index.md",
					indexArtifactStage,
					[]byte("# Unbound terminal extra\n"),
					0o640,
					9,
				)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			arrangeIndexBatchCrashBundle(t, root)
			test.arrange(t, root)
			before := documentSessionCaptureTree(t, root)

			// Act.
			err := recoverIndexBatchFixture(root)
			after := documentSessionCaptureTree(t, root)

			// Assert.
			if err == nil {
				t.Fatal("committed recovery accepted non-canonical remaining inventory")
			}
			documentSessionAssertTreeSnapshotEqual(t, before, after)
		})
	}
}

func TestIndexBatchManifestRollbackCleanupRejectsEveryNonPrefixHole(t *testing.T) {
	// Arrange one exact terminal rollback state before its first cleanup
	// removal, then clone it with hard-link identities preserved.
	base := t.TempDir()
	arrangeIndexBatchCrashBundle(t, base)
	runIndexBatchCrashHelper(t, base, "rollback-cleanup-before-first")
	manifest := readIndexBatchManifestFixture(t, base)
	a := indexBatchManifestEntryByPath(t, manifest, "a/index.md")
	b := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
	rootEntry := indexBatchManifestEntryByPath(t, manifest, indexFilename)
	nonProof := []string{
		a.Claim,
		b.Claim,
		a.Backup,
		b.Backup,
		rootEntry.Backup,
		a.Witness,
		b.Witness,
		rootEntry.NewInstall,
		a.Discard,
		b.Discard,
		a.Stage,
		b.Stage,
		rootEntry.Stage,
	}
	for _, relative := range nonProof {
		if _, err := os.Lstat(filepath.Join(base, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("full terminal fixture lacks %q: %v", relative, err)
		}
	}

	for earlier := 0; earlier < len(nonProof)-1; earlier++ {
		for missing := earlier + 1; missing < len(nonProof); missing++ {
			earlier, missing := earlier, missing
			name := strconv.Itoa(earlier) + "-present/" +
				strconv.Itoa(missing) + "-missing"
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				cloneIndexTransactionTreePreservingLinks(t, base, root)
				for index := 0; index < earlier; index++ {
					if err := os.Remove(filepath.Join(
						root,
						filepath.FromSlash(nonProof[index]),
					)); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Remove(filepath.Join(
					root,
					filepath.FromSlash(nonProof[missing]),
				)); err != nil {
					t.Fatal(err)
				}
				before := documentSessionCaptureTree(t, root)

				// Act.
				err := recoverIndexBatchFixture(root)
				after := documentSessionCaptureTree(t, root)

				// Assert.
				var hard unrecoverableIndexRecoveryError
				if !errors.As(err, &hard) {
					t.Fatalf("non-prefix hole error = %v, want hard recovery error", err)
				}
				documentSessionAssertTreeSnapshotEqual(t, before, after)
			})
		}
	}
	t.Run("tagged terminal fast-path error propagates", func(t *testing.T) {
		root := t.TempDir()
		cloneIndexTransactionTreePreservingLinks(t, base, root)
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(a.Stage))); err != nil {
			t.Fatal(err)
		}
		before := documentSessionCaptureTree(t, root)
		err := recoverIndexBatchFixture(root)
		var hard unrecoverableIndexRecoveryError
		if !errors.As(err, &hard) ||
			!errors.Is(err, errIndexBatchManifestTerminalNonPrefix) {
			t.Fatalf("missing anchored S error = %v, want tagged terminal hard error", err)
		}
		documentSessionAssertTreeSnapshotEqual(
			t,
			before,
			documentSessionCaptureTree(t, root),
		)
	})
	t.Run("anchored absent D is never reclassified as N lineage", func(t *testing.T) {
		root := t.TempDir()
		cloneIndexTransactionTreePreservingLinks(t, base, root)
		for index := 0; index < 7; index++ {
			if err := os.Remove(filepath.Join(
				root,
				filepath.FromSlash(nonProof[index]),
			)); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(a.Discard))); err != nil {
			t.Fatal(err)
		}
		before := documentSessionCaptureTree(t, root)
		err := recoverIndexBatchFixture(root)
		var hard unrecoverableIndexRecoveryError
		if !errors.As(err, &hard) ||
			!errors.Is(err, errIndexBatchManifestTerminalNonPrefix) {
			t.Fatalf("anchored missing-D error = %v, want tagged terminal hard error", err)
		}
		documentSessionAssertTreeSnapshotEqual(
			t,
			before,
			documentSessionCaptureTree(t, root),
		)
	})
}

func TestIndexBatchManifestTerminalProofSuffixRecoveryConverges(t *testing.T) {
	// Arrange the proof suffix from the same canonical planner used by active
	// cleanup and recovery.
	root := t.TempDir()
	_, arrange := arrangeIndexBatchCrashBundle(t, root)
	after := generatedIndexOracle(t, arrange, "a/index.md", "b/index.md", "index.md")
	planRoot := t.TempDir()
	arrange(t, planRoot)
	runIndexBatchCrashHelper(t, planRoot, "root-commit")
	plan := indexBatchCanonicalCleanupTrace(t, planRoot)
	manifestOrdinal := -1
	for ordinal, step := range plan {
		if strings.HasSuffix(step, ":"+string(indexArtifactManifest)) {
			manifestOrdinal = ordinal
			break
		}
	}
	if manifestOrdinal < 0 || manifestOrdinal == len(plan)-1 {
		t.Fatalf("canonical cleanup plan=%#v, want non-empty proof suffix", plan)
	}
	wantProofs := append([]string(nil), plan[manifestOrdinal+1:]...)
	sort.Strings(wantProofs)
	runIndexBatchCrashHelper(t, root, "terminal-proof-suffix")
	if names := indexBatchManifestFixtureNames(t, root); len(names) != 0 {
		t.Fatalf("manifest after terminal-prefix crash=%#v, want absent", names)
	}
	artifacts := indexTransactionArtifacts(t, root)
	gotProofs := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		directory := filepath.ToSlash(filepath.Dir(artifact))
		if directory == "." {
			directory = ""
		}
		if !strings.HasPrefix(filepath.Base(artifact), indexStagePrefix) {
			t.Fatalf("terminal artifact %q is not a stage proof", artifact)
		}
		gotProofs = append(
			gotProofs,
			pathJoin(directory, indexFilename)+":"+
				string(indexArtifactStage),
		)
	}
	sort.Strings(gotProofs)
	if !reflect.DeepEqual(gotProofs, wantProofs) {
		t.Fatalf(
			"terminal proof suffix=%#v, want shared-plan suffix %#v",
			gotProofs,
			wantProofs,
		)
	}

	// Act.
	err := recoverIndexBatchFixture(root)

	// Assert.
	if err != nil {
		t.Fatalf("terminal root-stage recovery error = %v", err)
	}
	for relative, data := range after {
		assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), data)
	}
	documentSessionAssertNoPublicationArtifacts(t, root)
}

func TestIndexBatchRecoveryObservesEachTargetOnceAndRejectsOscillationWithoutMutation(t *testing.T) {
	tests := []struct {
		name     string
		relative string
		mutate   func(t *testing.T, target string, published []byte, mode fs.FileMode)
	}{
		{
			name:     "earlier peer same bytes different inode",
			relative: "a/index.md",
			mutate: func(t *testing.T, target string, published []byte, mode fs.FileMode) {
				t.Helper()
				documentSessionAtomicReplace(t, target, string(published), mode)
			},
		},
		{
			name:     "later peer same bytes different inode",
			relative: indexFilename,
			mutate: func(t *testing.T, target string, published []byte, mode fs.FileMode) {
				t.Helper()
				documentSessionAtomicReplace(t, target, string(published), mode)
			},
		},
		{
			name:     "in-place bytes",
			relative: "a/index.md",
			mutate: func(t *testing.T, target string, _ []byte, mode fs.FileMode) {
				t.Helper()
				if err := os.WriteFile(target, []byte("# foreign\n"), mode); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "mode",
			relative: "a/index.md",
			mutate: func(t *testing.T, target string, _ []byte, _ fs.FileMode) {
				t.Helper()
				if err := os.Chmod(target, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "removed",
			relative: "a/index.md",
			mutate: func(t *testing.T, target string, _ []byte, _ fs.FileMode) {
				t.Helper()
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "type swap",
			relative: "a/index.md",
			mutate: func(t *testing.T, target string, _ []byte, _ fs.FileMode) {
				t.Helper()
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(target, 0o750); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			arrangeIndexBatchCrashBundle(t, root)
			runIndexBatchCrashHelper(t, root, "root-commit")
			manifest := readIndexBatchManifestFixture(t, root)
			beforeArtifacts := indexTransactionArtifacts(t, root)
			target := filepath.Join(root, filepath.FromSlash(tt.relative))
			published, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			observations := make(map[string]int, len(manifest.Entries))
			hooks := indexPublishHooks{
				afterBatchTargetObservation: func(relative string, _ *os.Root, _ string) error {
					observations[relative]++
					if relative == tt.relative {
						tt.mutate(t, target, published, indexPreservedMode(info.Mode()))
					}
					return nil
				},
			}

			// Act.
			recoveryErr := recoverIndexBatchFixtureWithHooks(root, hooks)

			// Assert.
			if recoveryErr == nil {
				t.Fatal("recovery accepted target drift after terminal classification")
			}
			for _, entry := range manifest.Entries {
				if got := observations[entry.Path]; got != 1 {
					t.Fatalf("target observations[%q]=%d, want one", entry.Path, got)
				}
			}
			if len(observations) != len(manifest.Entries) {
				t.Fatalf("target observations=%#v, want exactly one per manifest entry", observations)
			}
			if afterArtifacts := indexTransactionArtifacts(t, root); !reflect.DeepEqual(afterArtifacts, beforeArtifacts) {
				t.Fatalf("recovery artifacts changed before plan: before=%#v after=%#v", beforeArtifacts, afterArtifacts)
			}
			if manifests := indexBatchManifestFixtureNames(t, root); len(manifests) != 1 {
				t.Fatalf("recovery retained manifests=%#v, want one", manifests)
			}
		})
	}
}

func TestIndexBatchRecoveryStopsAfterFirstRollbackActionOnCrossTargetDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
	}{
		{
			name: "next target bytes",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "a", indexFilename), []byte("foreign bytes\n"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "next target mode",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Chmod(filepath.Join(root, "a", indexFilename), 0o666); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "next target deleted",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, "a", indexFilename)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "next target parent detached",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "parked-a")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "a", indexFilename), []byte("foreign parent\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "completed target bytes",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "b", indexFilename), []byte("completed target drift\n"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			arrangeIndexBatchCrashBundle(t, root)
			runIndexBatchCrashHelper(t, root, "before-root")
			actions := 0
			hooks := indexPublishHooks{
				afterBatchRecoveryAction: func(relative string, _ *os.Root, _ string) error {
					actions++
					if actions == 1 {
						if relative != "b/index.md" {
							t.Fatalf("first rollback action=%q, want b/index.md", relative)
						}
						test.mutate(t, root)
					}
					return nil
				},
			}

			// Act.
			err := recoverIndexBatchFixtureWithHooks(root, hooks)

			// Assert.
			if err == nil || actions != 1 {
				t.Fatalf("recovery error=%v actions=%d, want stop after first action", err, actions)
			}
			if artifacts := indexTransactionArtifacts(t, root); len(artifacts) == 0 {
				t.Fatal("cross-target drift lost manifest-bound recovery evidence")
			}
			if test.name != "completed target bytes" {
				assertFileBytes(t, filepath.Join(root, "b", indexFilename), []byte("# B original\n"))
			}
		})
	}
}

func TestIndexBatchCanonicalCleanupRevalidatesCrossDestinationAtEverySharedPlanBoundary(
	t *testing.T,
) {
	// Arrange the shared cleanup plan from a real durable terminal state. Both
	// the active transaction and restarted recovery must execute this exact
	// enum-derived order.
	base := t.TempDir()
	_, arrange := arrangeIndexBatchCrashBundle(t, base)
	runIndexBatchCrashHelper(t, base, "root-commit")
	plan := indexBatchCanonicalCleanupTrace(t, base)
	if len(plan) == 0 {
		t.Fatal("canonical cleanup plan is empty")
	}
	caseCount := 0
	for _, execution := range []string{"active", "recovery"} {
		execution := execution
		for _, cut := range []string{"before", "after"} {
			cut := cut
			for stepOrdinal, wantStep := range plan {
				stepOrdinal, wantStep := stepOrdinal, wantStep
				caseCount++
				name := execution + "/" + cut + "/" +
					strconv.Itoa(stepOrdinal) + "/" +
					strings.ReplaceAll(wantStep, "/", "_")
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					// Arrange.
					root := t.TempDir()
					if execution == "active" {
						arrange(t, root)
					} else {
						cloneIndexTransactionTreePreservingLinks(t, base, root)
					}
					stepDestination, _, ok := strings.Cut(wantStep, ":")
					if !ok {
						t.Fatalf("canonical cleanup step %q has no artifact kind", wantStep)
					}
					attackTarget := "b/index.md"
					if stepDestination == attackTarget {
						attackTarget = "a/index.md"
					}
					attackPath := filepath.Join(
						root,
						filepath.FromSlash(attackTarget),
					)
					attacked := false
					var attackedTree map[string]documentSessionTreeEntry
					var attackedIdentities map[string]os.FileInfo
					var beforeTrace, afterTrace []string
					attack := func() error {
						if attacked {
							return nil
						}
						if err := mutateIndexCrossDestinationPath(
							attackPath,
							true,
						); err != nil {
							return err
						}
						attacked = true
						attackedTree = documentSessionCaptureTree(t, root)
						attackedIdentities = documentPublicationIdentities(t, root)
						return nil
					}
					hooks := indexPublishHooks{
						beforeCleanup: func(
							relative string,
							_ *os.Root,
							kind, _ string,
						) error {
							current := relative + ":" + kind
							beforeTrace = append(beforeTrace, current)
							if cut == "before" &&
								len(beforeTrace)-1 == stepOrdinal {
								return attack()
							}
							return nil
						},
						afterCleanup: func(
							relative string,
							_ *os.Root,
							kind, _ string,
						) error {
							current := relative + ":" + kind
							afterTrace = append(afterTrace, current)
							if cut == "after" &&
								len(afterTrace)-1 == stepOrdinal {
								return attack()
							}
							return nil
						},
					}

					// Act.
					var err error
					var written []string
					if execution == "active" {
						written, err = regenerateIndexesWithTestHooks(
							root,
							nil,
							"",
							hooks)

					} else {
						err = recoverIndexBatchFixtureWithHooks(root, hooks)
					}

					// Assert.
					if !attacked || err == nil {
						t.Fatalf(
							"attacked=%t error=%v, want cross-destination cleanup conflict",
							attacked,
							err,
						)
					}
					if execution == "active" &&
						(len(written) != 3 ||
							!errors.Is(err, ErrPublicationCommitted)) {
						t.Fatalf(
							"active written=%#v error=%v, want three durable outputs and ErrPublicationCommitted",
							written,
							err,
						)
					}
					wantBefore := plan[:stepOrdinal+1]
					wantAfter := plan[:stepOrdinal]
					if cut == "after" {
						wantAfter = plan[:stepOrdinal+1]
					}
					beforeMatches := len(beforeTrace) == len(wantBefore) &&
						(len(wantBefore) == 0 ||
							reflect.DeepEqual(beforeTrace, wantBefore))
					afterMatches := len(afterTrace) == len(wantAfter) &&
						(len(wantAfter) == 0 ||
							reflect.DeepEqual(afterTrace, wantAfter))
					if !beforeMatches || !afterMatches {
						t.Fatalf(
							"cleanup trace before=%#v after=%#v, want %#v/%#v",
							beforeTrace,
							afterTrace,
							wantBefore,
							wantAfter,
						)
					}
					documentSessionAssertTreeSnapshotEqual(
						t,
						attackedTree,
						documentSessionCaptureTree(t, root),
					)
					documentAssertPublicationIdentities(
						t,
						root,
						attackedIdentities,
					)
				})
			}
		}
	}
	if want := 4 * len(plan); caseCount != want {
		t.Fatalf("cleanup boundary cases=%d, want %d", caseCount, want)
	}
}

func TestIndexBatchV3RejectsDetachedParentAtCanonicalDestructiveBoundaries(
	t *testing.T,
) {
	base := t.TempDir()
	_, arrange := arrangeIndexBatchCrashBundle(t, base)
	runIndexBatchCrashHelper(t, base, "rollback-cleanup-before-first")
	plan := indexBatchCanonicalCleanupTrace(t, base)
	if len(plan) == 0 {
		t.Fatal("canonical v3 cleanup plan is empty")
	}

	t.Run("anchor durable before target vacate", func(t *testing.T) {
		for _, execution := range []string{"active", "recovery"} {
			execution := execution
			t.Run(execution, func(t *testing.T) {
				root := t.TempDir()
				arrange(t, root)
				if execution == "recovery" {
					runIndexBatchCrashHelper(t, root, "before-root")
				}
				attacked := false
				var attackedTree map[string]documentSessionTreeEntry
				var attackedIdentities map[string]os.FileInfo
				attack := func() error {
					if attacked {
						return nil
					}
					if err := detachIndexFixtureParent(root, "b"); err != nil {
						return err
					}
					attacked = true
					attackedTree = documentSessionCaptureTree(t, root)
					attackedIdentities = documentPublicationIdentities(t, root)
					return nil
				}
				hooks := indexPublishHooks{
					afterArtifactDirectorySync: func(
						relative, kind, _ string,
						_ *os.Root,
					) error {
						if relative != "b/index.md" ||
							kind != string(indexArtifactAnchor) {
							return nil
						}
						return attack()
					},
				}
				var written []string
				var err error
				if execution == "active" {
					hooks.beforeRootCommit = func(string, *os.Root, string) error {
						return errors.New("force canonical v3 rollback")
					}
					written, err = regenerateIndexesWithTestHooks(root, nil, "", hooks)
				} else {
					err = recoverIndexBatchFixtureWithHooks(root, hooks)
				}
				assertIndexV3DetachedParentStop(
					t,
					root,
					attacked,
					attackedTree,
					attackedIdentities,
					written,
					err,
				)
				assertIndexV3DetachedParentRestart(t, root)
			})
		}
	})

	noManifestCases := 0
	for _, execution := range []string{"active", "recovery"} {
		execution := execution
		for stepOrdinal, wantStep := range plan {
			stepOrdinal, wantStep := stepOrdinal, wantStep
			t.Run(
				execution+"/"+strconv.Itoa(stepOrdinal)+"/"+
					strings.ReplaceAll(wantStep, "/", "_"),
				func(t *testing.T) {
					root := t.TempDir()
					if execution == "active" {
						arrange(t, root)
					} else {
						cloneIndexTransactionTreePreservingLinks(t, base, root)
					}
					stepDestination, _, ok := strings.Cut(wantStep, ":")
					if !ok {
						t.Fatalf("canonical cleanup step %q has no kind", wantStep)
					}
					directory := "a"
					if strings.HasPrefix(stepDestination, "b/") {
						directory = "b"
					}
					attacked := false
					var attackedTree map[string]documentSessionTreeEntry
					var attackedIdentities map[string]os.FileInfo
					var beforeTrace, afterTrace []string
					attack := func() error {
						if attacked {
							return nil
						}
						if err := detachIndexFixtureParent(root, directory); err != nil {
							return err
						}
						attacked = true
						attackedTree = documentSessionCaptureTree(t, root)
						attackedIdentities = documentPublicationIdentities(t, root)
						return nil
					}
					hooks := indexPublishHooks{
						beforeCleanup: func(
							relative string,
							_ *os.Root,
							kind, _ string,
						) error {
							beforeTrace = append(
								beforeTrace,
								relative+":"+kind,
							)
							if len(beforeTrace)-1 == stepOrdinal {
								return attack()
							}
							return nil
						},
						afterCleanup: func(
							relative string,
							_ *os.Root,
							kind, _ string,
						) error {
							afterTrace = append(
								afterTrace,
								relative+":"+kind,
							)
							return nil
						},
					}
					var written []string
					var err error
					if execution == "active" {
						hooks.beforeRootCommit = func(
							string,
							*os.Root,
							string,
						) error {
							return errors.New("force canonical v3 rollback")
						}
						written, err = regenerateIndexesWithTestHooks(
							root,
							nil,
							"",
							hooks)

					} else {
						err = recoverIndexBatchFixtureWithHooks(root, hooks)
					}
					assertIndexV3DetachedParentStop(
						t,
						root,
						attacked,
						attackedTree,
						attackedIdentities,
						written,
						err,
					)
					wantBefore := plan[:stepOrdinal+1]
					wantAfter := plan[:stepOrdinal]
					if len(beforeTrace) != len(wantBefore) ||
						!reflect.DeepEqual(beforeTrace, wantBefore) ||
						len(afterTrace) != len(wantAfter) ||
						len(wantAfter) > 0 &&
							!reflect.DeepEqual(afterTrace, wantAfter) {
						t.Fatalf(
							"detached cleanup trace before=%#v after=%#v, want %#v/%#v",
							beforeTrace,
							afterTrace,
							wantBefore,
							wantAfter,
						)
					}
					if len(indexBatchManifestFixtureNames(t, root)) == 1 {
						assertIndexV3DetachedParentRestart(t, root)
						return
					}
					// After M, the running recovery retains exact parent
					// handles and must still stop. A fresh process has no
					// durable parent-inode receipt by design, so no restart
					// assertion is fabricated for this suffix.
					noManifestCases++
				},
			)
		}
	}
	if noManifestCases == 0 {
		t.Fatal("canonical v3 plan did not exercise the no-manifest boundary")
	}
}

func detachIndexFixtureParent(root, directory string) error {
	if err := os.Rename(
		filepath.Join(root, directory),
		filepath.Join(root, "parked-"+directory),
	); err != nil {
		return err
	}
	return os.Mkdir(filepath.Join(root, directory), 0o755)
}

func assertIndexV3DetachedParentStop(
	t *testing.T,
	root string,
	attacked bool,
	attackedTree map[string]documentSessionTreeEntry,
	attackedIdentities map[string]os.FileInfo,
	written []string,
	err error,
) {
	t.Helper()

	if !attacked ||
		written != nil ||
		err == nil ||
		!isIndexV3DetachedParentConflict(err) {
		t.Fatalf(
			"attacked=%t written=%#v error=%v, want canonical v3 detached-parent blocker",
			attacked,
			written,
			err,
		)
	}
	documentSessionAssertTreeSnapshotEqual(
		t,
		attackedTree,
		documentSessionCaptureTree(t, root),
	)
	documentAssertPublicationIdentities(t, root, attackedIdentities)
}

func assertIndexV3DetachedParentRestart(t *testing.T, root string) {
	t.Helper()

	before := documentSessionCaptureTree(t, root)
	identities := documentPublicationIdentities(t, root)
	written, err := regenerateIndexesForTest(root)
	if written != nil || err == nil || !isIndexV3DetachedParentConflict(err) {
		t.Fatalf(
			"restart written=%#v error=%v, want manifest-bound detached-parent blocker",
			written,
			err,
		)
	}
	documentSessionAssertTreeSnapshotEqual(
		t,
		before,
		documentSessionCaptureTree(t, root),
	)
	documentAssertPublicationIdentities(t, root, identities)
}

func isIndexV3DetachedParentConflict(err error) bool {
	var hard unrecoverableIndexRecoveryError
	return errors.As(err, &hard) ||
		errors.Is(err, errPublicationScopeInvalid) ||
		strings.Contains(err.Error(), "destination ancestor changed after preflight") ||
		strings.Contains(err.Error(), "detached destination parent")
}

func TestIndexBatchCommittedCleanupRevalidatesAllTargetsAndParentsAroundEachAction(t *testing.T) {
	t.Run("before first removal same bytes different inode", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		arrangeIndexBatchCrashBundle(t, root)
		runIndexBatchCrashHelper(t, root, "root-commit")
		beforeArtifacts := indexTransactionArtifacts(t, root)
		cleanupPlan := indexBatchCanonicalCleanupTrace(t, root)
		if len(cleanupPlan) == 0 {
			t.Fatal("canonical cleanup plan is empty")
		}
		target := filepath.Join(root, "b", indexFilename)
		published, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		beforeCalls := 0
		afterCalls := 0
		var firstStep string
		hooks := indexPublishHooks{
			beforeCleanup: func(relative string, _ *os.Root, kind, _ string) error {
				beforeCalls++
				firstStep = relative + ":" + kind
				if beforeCalls == 1 {
					documentSessionAtomicReplace(t, target, string(published), 0o640)
				}
				return nil
			},
			afterCleanup: func(string, *os.Root, string, string) error {
				afterCalls++
				return nil
			},
		}

		// Act.
		err = recoverIndexBatchFixtureWithHooks(root, hooks)

		// Assert.
		if err == nil || beforeCalls != 1 || afterCalls != 0 ||
			firstStep != cleanupPlan[0] {
			t.Fatalf(
				"recovery error=%v before=%d after=%d first=%q, want one pre-remove hook for %q then rejection",
				err,
				beforeCalls,
				afterCalls,
				firstStep,
				cleanupPlan[0],
			)
		}
		if afterArtifacts := indexTransactionArtifacts(t, root); !reflect.DeepEqual(afterArtifacts, beforeArtifacts) {
			t.Fatalf("artifacts changed before first remove: before=%#v after=%#v", beforeArtifacts, afterArtifacts)
		}
	})

	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
	}{
		{
			name: "target bytes",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "b", indexFilename), []byte("cleanup drift\n"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "target mode",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Chmod(filepath.Join(root, "b", indexFilename), 0o666); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "target deleted",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, "b", indexFilename)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "cleaned-prefix parent detached",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "parked-a")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "a", indexFilename), []byte("foreign parent\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run("after first removal/"+test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			arrangeIndexBatchCrashBundle(t, root)
			runIndexBatchCrashHelper(t, root, "root-commit")
			cleanupCalls := 0
			hooks := indexPublishHooks{
				afterCleanup: func(string, *os.Root, string, string) error {
					cleanupCalls++
					if cleanupCalls == 1 {
						test.mutate(t, root)
					}
					return nil
				},
			}

			// Act.
			err := recoverIndexBatchFixtureWithHooks(root, hooks)

			// Assert.
			if err == nil || cleanupCalls != 1 {
				t.Fatalf("recovery error=%v cleanupCalls=%d, want stop after first removal", err, cleanupCalls)
			}
			if artifacts := indexTransactionArtifacts(t, root); len(artifacts) == 0 {
				t.Fatal("cleanup drift lost all recovery evidence")
			}
		})
	}
}

func TestIndexBatchManifestCrashSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_INDEX_BATCH_CRASH_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	point := os.Getenv("OKF_INDEX_BATCH_CRASH_POINT")
	crashOrdinal, ordinalErr := strconv.Atoi(os.Getenv("OKF_INDEX_BATCH_CRASH_ORDINAL"))
	if ordinalErr != nil {
		crashOrdinal = -1
	}
	crash := func() {
		os.Exit(87)
	}
	cleanupOrdinal := 0
	recoveryVacateOrdinal := 0
	restoreInstallDirectorySyncs := 0
	hooks := indexPublishHooks{
		afterArtifactDirectorySync: func(relative, kind, _ string, _ *os.Root) error {
			if point == "pre-manifest" &&
				relative == "a/index.md" &&
				kind == string(indexArtifactStage) {
				crash()
			}
			if point == "witness-pre-manifest" &&
				relative == "a/index.md" &&
				kind == string(indexArtifactWitness) {
				crash()
			}
			if point == "rollback-anchor-durable" &&
				relative == "b/index.md" &&
				kind == string(indexArtifactAnchor) {
				crash()
			}
			if relative == "b/index.md" &&
				kind == string(indexArtifactRestoreInstall) {
				current := restoreInstallDirectorySyncs
				restoreInstallDirectorySyncs++
				if point == "rollback-restore-install-durable" && current == 0 {
					crash()
				}
				if point == "rollback-restore-install-post-sync" && current == 1 {
					crash()
				}
			}
			return nil
		},
		afterBatchManifestDurable: func(*os.Root, string) error {
			if point == "manifest" {
				crash()
			}
			return nil
		},
		afterVacateRename: func(relative string, _ *os.Root, _, claim string) error {
			if point == "mid-vacate" && relative == "b/index.md" {
				crash()
			}
			if point == "mid-vacate-first" && relative == "a/index.md" {
				crash()
			}
			if point == "rollback-discard-post-rename" &&
				relative == "b/index.md" &&
				strings.HasPrefix(claim, indexDiscardPrefix) {
				crash()
			}
			return nil
		},
		afterRootCommit: func(string, *os.Root, string, string) error {
			if point == "root-commit" {
				crash()
			}
			return nil
		},
		beforeRootCommit: func(string, *os.Root, string) error {
			if point == "before-root" {
				crash()
			}
			if point == "rollback-cleanup" ||
				point == "rollback-cleanup-before-first" ||
				point == "rollback-anchor-durable" ||
				point == "rollback-restore-install-durable" ||
				point == "rollback-discard-post-rename" ||
				point == "rollback-discard-post-sync" ||
				point == "rollback-restore-install-post-rename" ||
				point == "rollback-restore-install-post-sync" ||
				point == "rollback-after-vacate" ||
				point == "rollback-after-link" {
				return errors.New("force precommit rollback")
			}
			return nil
		},
		beforeRestore: func(relative string, _ *os.Root, _, _ string) error {
			if point == "rollback-after-vacate" && relative == "b/index.md" {
				crash()
			}
			return nil
		},
		afterVacate: func(relative string, _ *os.Root, _, claim string) error {
			if point == "rollback-vacate" {
				current := recoveryVacateOrdinal
				recoveryVacateOrdinal++
				if current == crashOrdinal {
					crash()
				}
			}
			if point == "rollback-discard-post-sync" &&
				relative == "b/index.md" &&
				strings.HasPrefix(claim, indexDiscardPrefix) {
				crash()
			}
			return nil
		},
		afterRestoreLink: func(relative string, _ *os.Root, source, _ string) error {
			if point == "rollback-after-link" && relative == "b/index.md" {
				crash()
			}
			if point == "rollback-restore-install-post-rename" &&
				relative == "b/index.md" &&
				strings.HasPrefix(source, indexRestoreInstallPrefix) {
				crash()
			}
			return nil
		},
		beforeCleanup: func(_ string, _ *os.Root, _, _ string) error {
			if point == "rollback-cleanup-before-first" {
				crash()
			}
			return nil
		},
		afterCleanup: func(_ string, _ *os.Root, kind, _ string) error {
			current := cleanupOrdinal
			cleanupOrdinal++
			if point == "terminal-proof-suffix" &&
				kind == string(indexArtifactManifest) {
				crash()
			}
			if (point == "commit-cleanup" || point == "rollback-cleanup") &&
				current == crashOrdinal {
				crash()
			}
			return nil
		},
	}
	hooks = withoutPhysicalIndexDurability(hooks)
	if _, err := regenerateIndexesWithTestHooks(root, nil, "", hooks); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash point was not reached")
}

func cloneIndexTransactionTreePreservingLinks(t *testing.T, source, target string) {
	t.Helper()
	type copiedRegular struct {
		info os.FileInfo
		path string
	}
	var copied []copiedRegular
	err := filepath.WalkDir(source, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, filename)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		destination := filepath.Join(target, relative)
		info, err := os.Lstat(filename)
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(destination, info.Mode().Perm())
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(filename)
			if err != nil {
				return err
			}
			return os.Symlink(link, destination)
		case info.Mode().IsRegular():
			for _, prior := range copied {
				if os.SameFile(prior.info, info) {
					if err := os.Link(prior.path, destination); err != nil {
						return err
					}
					copied = append(copied, copiedRegular{info: info, path: destination})
					return nil
				}
			}
			data, err := os.ReadFile(filename)
			if err != nil {
				return err
			}
			if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
				return err
			}
			copied = append(copied, copiedRegular{info: info, path: destination})
			return nil
		default:
			return errors.New("terminal fixture contains a non-file entry")
		}
	})
	if err != nil {
		t.Fatalf("clone terminal index transaction tree: %v", err)
	}
}

func arrangeIndexBatchCrashBundle(
	t *testing.T,
	root string,
) (map[string][]byte, func(t *testing.T, root string)) {
	t.Helper()
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
			if err := os.WriteFile(
				filepath.Join(root, filepath.FromSlash(relative)),
				data,
				0o640,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	arrange(t, root)
	return before, arrange
}

func runIndexBatchCrashHelper(t *testing.T, root, point string) {
	t.Helper()
	runIndexBatchCrashHelperAtOrdinal(t, root, point, -1)
}

func runIndexBatchCrashHelperAtOrdinal(t *testing.T, root, point string, ordinal int) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestIndexBatchManifestCrashSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_INDEX_BATCH_CRASH_ROOT="+root,
		"OKF_INDEX_BATCH_CRASH_POINT="+point,
		"OKF_INDEX_BATCH_CRASH_ORDINAL="+strconv.Itoa(ordinal),
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err == nil || command.ProcessState == nil || command.ProcessState.ExitCode() != 87 {
		t.Fatalf(
			"crash helper exit=%v error=%v at %s, want 87\n%s",
			command.ProcessState,
			err,
			point,
			output.String(),
		)
	}
}

func readIndexBatchManifestFixture(t *testing.T, root string) indexBatchManifest {
	t.Helper()
	names := indexBatchManifestFixtureNames(t, root)
	if len(names) != 1 {
		t.Fatalf("batch manifests=%#v, want one", names)
	}
	data, err := os.ReadFile(filepath.Join(root, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	var manifest indexBatchManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func indexBatchManifestFixtureNames(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if parseIndexBatchManifestName(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	return names
}

func indexBatchManifestEntryByPath(
	t *testing.T,
	manifest indexBatchManifest,
	path string,
) indexBatchManifestEntry {
	t.Helper()
	for _, entry := range manifest.Entries {
		if entry.Path == path {
			return entry
		}
	}
	t.Fatalf("manifest has no entry for %q: %+v", path, manifest)
	return indexBatchManifestEntry{}
}

func missingIndexBatchManifestEntry(
	t *testing.T,
	root string,
	manifest indexBatchManifest,
) indexBatchManifestEntry {
	t.Helper()
	for _, entry := range manifest.Entries {
		if entry.Claim == "" || entry.Backup == "" ||
			entry.Anchor == "" || entry.Witness == "" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Path))); errors.Is(err, os.ErrNotExist) {
			return entry
		} else if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatalf("manifest has no missing target with claim and backup: %+v", manifest)
	return indexBatchManifestEntry{}
}

func captureReservedPublicationTree(
	t *testing.T,
	root string,
) map[string]documentSessionTreeEntry {
	t.Helper()
	all := documentSessionCaptureTree(t, root)
	reserved := make(map[string]documentSessionTreeEntry)
	for relative, entry := range all {
		if isReservedTransactionPath(filepath.Base(filepath.FromSlash(relative))) {
			reserved[relative] = entry
		}
	}
	return reserved
}

func soleIndexArtifactOfKind(
	t *testing.T,
	root string,
	kind indexArtifactKind,
) string {
	t.Helper()
	var matches []string
	for _, relative := range indexTransactionArtifacts(t, root) {
		if _, ok := parseIndexArtifactName(
			filepath.Base(filepath.FromSlash(relative)),
			kind,
		); ok {
			matches = append(matches, relative)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("%s artifacts=%#v, want one", kind, matches)
	}
	return matches[0]
}

func recoverIndexBatchFixture(rootPath string) error {
	return recoverIndexBatchFixtureWithHooks(rootPath, indexPublishHooks{})
}

func recoverIndexBatchFixtureWithHooks(rootPath string, hooks indexPublishHooks) error {
	hooks = withoutPhysicalIndexDurability(hooks)
	root, err := openRootWithoutSymlinks(rootPath)
	if err != nil {
		return err
	}
	recoveryErr := inspectGlobalPublicationRecoveryNamespace(
		context.Background(),
		root,
		hooks,
		documentPublishHooks{},
	)
	return errors.Join(recoveryErr, root.Close())
}

func TestIndexPublicationLockSubprocessHelper(t *testing.T) {
	mode := os.Getenv("OKF_INDEX_LOCK_HELPER")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	root := os.Getenv("OKF_INDEX_LOCK_ROOT")
	ready := os.Getenv("OKF_INDEX_LOCK_READY")
	release := os.Getenv("OKF_INDEX_LOCK_RELEASE")
	_, err := regenerateIndexesWithTestHooks(root, nil, "", indexPublishHooks{
		afterIndexLock: func() error {
			if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
				return err
			}
			if mode == "exit" {
				os.Exit(0)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(release); err == nil {
					return nil
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if time.Now().After(deadline) {
					return errors.New("timed out waiting for subprocess release")
				}
				time.Sleep(5 * time.Millisecond)
			}
		},
	})

	if err != nil {
		t.Fatal(err)
	}
}

func waitForIndexLockTestPath(t *testing.T, filename string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filename); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", filename)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
