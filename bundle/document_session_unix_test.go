//go:build darwin || linux

package bundle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestDocumentPrepareCrashSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_DOCUMENT_PREPARE_CRASH_ROOT")
	point := os.Getenv("OKF_DOCUMENT_PREPARE_CRASH_POINT")
	if root == "" || point == "" {
		return
	}
	crash := func() error {
		os.Exit(87)
		return nil
	}
	hooks := documentPublishHooks{
		afterWitnessSync: func(*os.Root, string) error {
			if point == "witness" {
				return crash()
			}
			return nil
		},
		afterBackupSync: func(*os.Root, string) error {
			if point == "backup" {
				return crash()
			}
			return nil
		},
		afterStageSync: func(*os.Root, string) error {
			if point == "stage" {
				return crash()
			}
			return nil
		},
		afterManifestSync: func(*os.Root, string) error {
			if point == "manifest" {
				return crash()
			}
			return nil
		},
		afterInstall: func(*os.Root, string) error {
			if point == "installed" {
				return crash()
			}
			return nil
		},
	}
	target := filepath.Join(root, "concept.md")
	if strings.HasPrefix(point, "recovery-cleanup-") {
		ordinal, err := strconv.Atoi(strings.TrimPrefix(point, "recovery-cleanup-"))
		if err != nil {
			t.Fatal(err)
		}
		cleanupOrdinal := 0
		hooks.afterCleanup = func(*os.Root, string, string) error {
			current := cleanupOrdinal
			cleanupOrdinal++
			if current == ordinal {
				return crash()
			}
			return nil
		}
		session, recoveryErr := openDocumentSessionContext(
			context.Background(),
			target,
			"",
			documentSessionHooks{publish: hooks},
		)
		if session != nil {
			_ = session.Close()
		}
		t.Fatalf(
			"OpenDocumentSessionContext() returned before cleanup crash %d: session=%#v error=%v",
			ordinal,
			session,
			recoveryErr,
		)
	}
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: hooks},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	err = session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)
	t.Fatalf("RewriteContext() returned at crash point %q: %v", point, err)
}

func TestDocumentProofLastCleanupOrdering(t *testing.T) {
	t.Run("committed manifest removed before terminal stage proof", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		target := filepath.Join(root, "concept.md")
		documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
		documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
		if err := os.Chmod(target, 0o640); err != nil {
			t.Fatal(err)
		}
		cleanupFault := errors.New("injected after committed manifest removal")
		var targetBeforeManifestRemoval os.FileInfo
		var stagePath string
		session, err := openDocumentSessionContext(
			context.Background(),
			target,
			"",
			documentSessionHooks{publish: documentPublishHooks{
				beforeCleanup: func(parent *os.Root, kind, _ string) error {
					if kind != "manifest" {
						return nil
					}
					stagePath = documentSessionSingleRecoveryArtifact(t, parent.Name(), "stage")
					stageInfo, err := os.Lstat(stagePath)
					if err != nil {
						return err
					}
					targetBeforeManifestRemoval, err = os.Lstat(target)
					if err != nil || !os.SameFile(targetBeforeManifestRemoval, stageInfo) {
						t.Fatalf("committed target is not exact S before M removal: target=%v S=%v error=%v", targetBeforeManifestRemoval, stageInfo, err)
					}
					return nil
				},
				afterCleanup: func(parent *os.Root, kind, name string) error {
					if kind != "manifest" {
						return nil
					}
					if _, err := parent.Lstat(name); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("manifest remains after removal hook: %v", err)
					}
					targetAfter, err := os.Lstat(target)
					if err != nil || !os.SameFile(targetBeforeManifestRemoval, targetAfter) {
						t.Fatalf("target inode changed across M removal: before=%v after=%v error=%v", targetBeforeManifestRemoval, targetAfter, err)
					}
					return cleanupFault
				},
			}},
		)
		if err != nil {
			t.Fatal(err)
		}

		// Act: commit and stop after proof-last M removal.
		rewriteErr := session.RewriteContext(
			context.Background(),
			documentSessionParsed(t, documentSessionConcept("concept", "after")),
		)
		_ = session.Close()

		// Assert the full no-M committed form is retained for terminal cleanup.
		if targetBeforeManifestRemoval == nil ||
			!errors.Is(rewriteErr, cleanupFault) ||
			!errors.Is(rewriteErr, ErrPublicationCommitted) {
			t.Fatalf("target=%v error=%v, want committed M-removal interruption", targetBeforeManifestRemoval, rewriteErr)
		}
		wantKinds := []string{"backup", "claim", "stage", "witness"}
		if got := documentPrepareCrashArtifactKinds(documentPrepareCrashArtifacts(t, root)); !reflect.DeepEqual(got, wantKinds) {
			t.Fatalf("committed terminal kinds = %#v, want %#v", got, wantKinds)
		}
		stageInfo, err := os.Lstat(stagePath)
		if err != nil {
			t.Fatal(err)
		}

		// Unified cleanup must preserve target~S and remove S last.
		var cleanupOrder []string
		recovered, recoveryErr := openDocumentSessionContext(
			context.Background(),
			target,
			"",
			documentSessionHooks{publish: documentPublishHooks{
				beforeCleanup: func(_ *os.Root, kind, _ string) error {
					targetInfo, targetErr := os.Lstat(target)
					if targetErr != nil ||
						!os.SameFile(stageInfo, targetInfo) {
						t.Fatalf("terminal S proof detached before %s: target=%v error=%v", kind, targetInfo, targetErr)
					}
					return nil
				},
				afterCleanup: func(parent *os.Root, kind, name string) error {
					if _, err := parent.Lstat(name); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("terminal %s remains after unlink hook: %v", kind, err)
					}
					targetInfo, err := os.Lstat(target)
					if err != nil || !os.SameFile(stageInfo, targetInfo) {
						t.Fatalf("target changed across terminal %s unlink: proof=%v target=%v error=%v", kind, stageInfo, targetInfo, err)
					}
					cleanupOrder = append(cleanupOrder, kind)
					return nil
				},
			}},
		)
		if recoveryErr != nil {
			t.Fatalf("terminal S recovery: %v", recoveryErr)
		}
		if err := recovered.Close(); err != nil {
			t.Fatal(err)
		}
		wantOrder := []string{"claim", "backup", "witness", "stage"}
		if !reflect.DeepEqual(cleanupOrder, wantOrder) {
			t.Fatalf("committed cleanup order = %#v, want %#v", cleanupOrder, wantOrder)
		}
		documentSessionAssertNoArtifacts(t, root)
	})

	t.Run("rollback manifest removed before terminal anchor proof", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		target := filepath.Join(root, "concept.md")
		original := documentSessionConcept("concept", "before")
		documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
		documentSessionWriteFile(t, target, original)
		if err := os.Chmod(target, 0o640); err != nil {
			t.Fatal(err)
		}
		rollbackFault := errors.New("injected post-install rollback")
		cleanupFault := errors.New("injected after rollback manifest removal")
		forwardFailed := false
		var targetBeforeManifestRemoval os.FileInfo
		var anchorPath string
		session, err := openDocumentSessionContext(
			context.Background(),
			target,
			"",
			documentSessionHooks{publish: documentPublishHooks{
				afterInstall: func(*os.Root, string) error {
					if forwardFailed {
						return nil
					}
					forwardFailed = true
					return rollbackFault
				},
				beforeCleanup: func(parent *os.Root, kind, _ string) error {
					if kind != "manifest" {
						return nil
					}
					anchorPath = documentSessionSingleRecoveryArtifact(
						t,
						parent.Name(),
						documentRollbackAnchorProtocolKind(0),
					)
					anchorInfo, err := os.Lstat(anchorPath)
					if err != nil {
						return err
					}
					targetBeforeManifestRemoval, err = os.Lstat(target)
					if err != nil || !os.SameFile(targetBeforeManifestRemoval, anchorInfo) {
						t.Fatalf("rollback target is not exact A before M removal: target=%v A=%v error=%v", targetBeforeManifestRemoval, anchorInfo, err)
					}
					return nil
				},
				afterCleanup: func(parent *os.Root, kind, name string) error {
					if kind != "manifest" {
						return nil
					}
					if _, err := parent.Lstat(name); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("rollback manifest remains after removal hook: %v", err)
					}
					targetAfter, err := os.Lstat(target)
					if err != nil || !os.SameFile(targetBeforeManifestRemoval, targetAfter) {
						t.Fatalf("rollback target changed across M removal: before=%v after=%v error=%v", targetBeforeManifestRemoval, targetAfter, err)
					}
					return cleanupFault
				},
			}},
		)
		if err != nil {
			t.Fatal(err)
		}

		// Act.
		rewriteErr := session.RewriteContext(
			context.Background(),
			documentSessionParsed(t, documentSessionConcept("concept", "after")),
		)
		_ = session.Close()

		// Assert the full no-M restored form is retained for terminal cleanup.
		if targetBeforeManifestRemoval == nil ||
			!errors.Is(rewriteErr, rollbackFault) ||
			!errors.Is(rewriteErr, cleanupFault) {
			t.Fatalf("target=%v error=%v, want rollback M-removal interruption", targetBeforeManifestRemoval, rewriteErr)
		}
		wantKinds := []string{"anchor-0", "backup", "claim", "discard", "stage", "witness"}
		if got := documentPrepareCrashArtifactKinds(documentPrepareCrashArtifacts(t, root)); !reflect.DeepEqual(got, wantKinds) {
			t.Fatalf("rollback terminal kinds = %#v, want %#v", got, wantKinds)
		}
		anchorInfo, err := os.Lstat(anchorPath)
		if err != nil {
			t.Fatal(err)
		}

		// Unified cleanup must preserve target~A and remove A last.
		var cleanupOrder []string
		recovered, recoveryErr := openDocumentSessionContext(
			context.Background(),
			target,
			"",
			documentSessionHooks{publish: documentPublishHooks{
				beforeCleanup: func(_ *os.Root, kind, _ string) error {
					targetInfo, targetErr := os.Lstat(target)
					if targetErr != nil ||
						!os.SameFile(anchorInfo, targetInfo) {
						t.Fatalf("terminal A proof detached before %s: target=%v error=%v", kind, targetInfo, targetErr)
					}
					return nil
				},
				afterCleanup: func(parent *os.Root, kind, name string) error {
					if _, err := parent.Lstat(name); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("terminal %s remains after unlink hook: %v", kind, err)
					}
					targetInfo, err := os.Lstat(target)
					if err != nil || !os.SameFile(anchorInfo, targetInfo) {
						t.Fatalf("target changed across terminal %s unlink: proof=%v target=%v error=%v", kind, anchorInfo, targetInfo, err)
					}
					cleanupOrder = append(cleanupOrder, kind)
					return nil
				},
			}},
		)
		if recoveryErr != nil {
			t.Fatalf("terminal A recovery: %v", recoveryErr)
		}
		if err := recovered.Close(); err != nil {
			t.Fatal(err)
		}
		wantOrder := []string{"discard", "claim", "backup", "witness", "stage", "anchor-0"}
		if !reflect.DeepEqual(cleanupOrder, wantOrder) {
			t.Fatalf("rollback cleanup order = %#v, want %#v", cleanupOrder, wantOrder)
		}
		afterInfo, err := os.Lstat(target)
		if err != nil || !os.SameFile(afterInfo, targetBeforeManifestRemoval) {
			t.Fatalf("terminal A recovery changed target: before=%v after=%v error=%v", targetBeforeManifestRemoval, afterInfo, err)
		}
		if got := documentSessionReadFile(t, target); got != original {
			t.Fatalf("terminal A target bytes = %q, want %q", got, original)
		}
		documentSessionAssertNoArtifacts(t, root)
	})

	t.Run("precommit witness-only final proof unlink", func(t *testing.T) {
		// Arrange a true W-only crash image.
		root := t.TempDir()
		target := filepath.Join(root, "concept.md")
		original := documentSessionConcept("concept", "before")
		documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
		documentSessionWriteFile(t, target, original)
		if err := os.Chmod(target, 0o640); err != nil {
			t.Fatal(err)
		}
		runDocumentPrepareCrashHelper(t, root, "witness")
		artifacts := documentPrepareCrashArtifacts(t, root)
		if got := documentPrepareCrashArtifactKinds(artifacts); !reflect.DeepEqual(got, []string{"witness"}) {
			t.Fatalf("precommit terminal kinds = %#v, want witness only", got)
		}
		witnessInfo, err := os.Lstat(artifacts["witness"])
		if err != nil {
			t.Fatal(err)
		}

		// Act and assert W is removed last without changing the target inode.
		var proofBefore os.FileInfo
		recovered, recoveryErr := openDocumentSessionContext(
			context.Background(),
			target,
			"",
			documentSessionHooks{publish: documentPublishHooks{
				beforeCleanup: func(parent *os.Root, kind, name string) error {
					if kind != "witness" {
						t.Fatalf("terminal precommit cleanup kind = %q, want witness", kind)
					}
					var err error
					proofBefore, err = parent.Lstat(name)
					targetInfo, targetErr := os.Lstat(target)
					if err != nil || targetErr != nil ||
						!os.SameFile(proofBefore, targetInfo) ||
						!os.SameFile(witnessInfo, proofBefore) {
						t.Fatalf("terminal W proof detached: proof=%v target=%v proofErr=%v targetErr=%v", proofBefore, targetInfo, err, targetErr)
					}
					return nil
				},
				afterCleanup: func(parent *os.Root, kind, name string) error {
					if kind != "witness" {
						t.Fatalf("terminal precommit cleanup kind = %q, want witness", kind)
					}
					if _, err := parent.Lstat(name); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("terminal W remains after unlink hook: %v", err)
					}
					targetInfo, err := os.Lstat(target)
					if err != nil || !os.SameFile(proofBefore, targetInfo) {
						t.Fatalf("target changed across terminal W unlink: proof=%v target=%v error=%v", proofBefore, targetInfo, err)
					}
					return nil
				},
			}},
		)
		if recoveryErr != nil {
			t.Fatalf("terminal W recovery: %v", recoveryErr)
		}
		if err := recovered.Close(); err != nil {
			t.Fatal(err)
		}
		afterInfo, err := os.Lstat(target)
		if err != nil || !os.SameFile(afterInfo, witnessInfo) {
			t.Fatalf("terminal W recovery changed target: before=%v after=%v error=%v", witnessInfo, afterInfo, err)
		}
		documentSessionAssertNoArtifacts(t, root)
	})
}

func TestDocumentActiveRollbackRejectsDetachedParentAtV2DestructiveBoundaries(t *testing.T) {
	t.Parallel()

	type configureCut func(
		hooks *documentPublishHooks,
		rollingBack *bool,
		attack func() error,
	)
	tests := []struct {
		name      string
		configure configureCut
	}{
		{
			name: "after anchor durable before published target vacate",
			configure: func(hooks *documentPublishHooks, rollingBack *bool, attack func() error) {
				rollbackDirectorySyncs := 0
				hooks.fileSync = func(file *os.File) error {
					return file.Sync()
				}
				hooks.directorySync = func(parent *os.Root) error {
					if err := syncPublicationDirectoryPlatform(parent); err != nil {
						return err
					}
					if !*rollingBack {
						return nil
					}
					rollbackDirectorySyncs++
					if rollbackDirectorySyncs == 2 {
						return attack()
					}
					return nil
				}
			},
		},
		{
			name: "before rollback anchor install",
			configure: func(hooks *documentPublishHooks, rollingBack *bool, attack func() error) {
				hooks.beforeInstall = func(*os.Root, string, string) error {
					if !*rollingBack {
						return nil
					}
					return attack()
				}
			},
		},
	}
	for _, cleanup := range []struct {
		name string
		kind string
	}{
		{name: "S", kind: "stage"},
		{name: "C", kind: "claim"},
		{name: "B", kind: "backup"},
		{name: "W", kind: "witness"},
		{name: "M", kind: "manifest"},
		{name: "proof A", kind: "anchor"},
	} {
		cleanup := cleanup
		tests = append(tests, struct {
			name      string
			configure configureCut
		}{
			name: "before cleanup " + cleanup.name,
			configure: func(hooks *documentPublishHooks, _ *bool, attack func() error) {
				hooks.beforeCleanup = func(_ *os.Root, kind, _ string) error {
					if kind != cleanup.kind {
						return nil
					}
					return attack()
				}
			},
		})
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "nested", "concept.md")
			original := documentSessionConcept("concept", "before")
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
			documentSessionWriteFile(t, target, original)
			if err := os.Chmod(target, 0o640); err != nil {
				t.Fatal(err)
			}
			rollbackFault := errors.New("injected rollback after document install")
			rollingBack := false
			attacked := false
			var attackedTree map[string]documentSessionTreeEntry
			var attackedIdentities map[string]os.FileInfo
			attack := func() error {
				if attacked {
					return nil
				}
				if err := os.Rename(filepath.Join(root, "nested"), filepath.Join(root, "parked")); err != nil {
					return err
				}
				if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
					return err
				}
				attacked = true
				attackedTree = documentSessionCaptureTree(t, root)
				attackedIdentities = documentPublicationIdentities(t, root)
				return nil
			}
			hooks := documentPublishHooks{
				afterInstall: func(*os.Root, string) error {
					if rollingBack {
						return nil
					}
					rollingBack = true
					return rollbackFault
				},
			}
			test.configure(&hooks, &rollingBack, attack)
			session, err := openDocumentSessionContext(
				context.Background(),
				target,
				"",
				documentSessionHooks{publish: hooks},
			)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			rewriteErr := session.RewriteContext(
				context.Background(),
				documentSessionParsed(t, documentSessionConcept("concept", "after")),
			)
			_ = session.Close()

			// Assert.
			if !attacked ||
				!errors.Is(rewriteErr, rollbackFault) ||
				!errors.Is(rewriteErr, ErrDocumentConflict) {
				t.Fatalf("attacked=%t error=%v, want typed detached-parent conflict at %s", attacked, rewriteErr, test.name)
			}
			documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
			documentAssertPublicationIdentities(t, root, attackedIdentities)
			if !documentSessionHasArtifacts(t, filepath.Join(root, "parked")) {
				t.Fatalf("detached v2 evidence disappeared at %s", test.name)
			}

			beforeRetry := documentSessionCaptureTree(t, root)
			beforeRetryIdentities := documentPublicationIdentities(t, root)
			restarted, restartErr := OpenDocumentSessionContext(context.Background(), target, "")
			if restarted != nil {
				_ = restarted.Close()
			}
			if restartErr == nil {
				t.Fatal("restart accepted detached document transaction")
			}
			documentSessionAssertTreeSnapshotEqual(t, beforeRetry, documentSessionCaptureTree(t, root))
			documentAssertPublicationIdentities(t, root, beforeRetryIdentities)
		})
	}
}

func TestDocumentRecoveryRejectsDetachedParentAtV2DestructiveBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("after anchor durable before recovery install", func(t *testing.T) {
		t.Parallel()
		testDocumentRecoveryDetachBeforeInstall(t, true)
	})
	t.Run("before recovery install", func(t *testing.T) {
		t.Parallel()
		testDocumentRecoveryDetachBeforeInstall(t, false)
	})
	for _, cleanup := range []struct {
		name string
		kind string
	}{
		{name: "S", kind: "stage"},
		{name: "C", kind: "claim"},
		{name: "B", kind: "backup"},
		{name: "W", kind: "witness"},
		{name: "M", kind: "manifest"},
		{name: "proof A", kind: documentRollbackAnchorProtocolKind(0)},
	} {
		cleanup := cleanup
		t.Run("before cleanup "+cleanup.name, func(t *testing.T) {
			t.Parallel()

			// Arrange a valid rollback group whose public target is exact A.
			root := t.TempDir()
			target := filepath.Join(root, "nested", "concept.md")
			original := documentSessionConcept("concept", "before")
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
			documentSessionCreateV2RecoveryState(
				t,
				root,
				"nested/concept.md",
				0o640,
				original,
				documentSessionConcept("concept", "after"),
				documentV2State(false, true, false, true, true, documentV2TargetAnchor),
			)
			attacked := false
			var attackedTree map[string]documentSessionTreeEntry
			var attackedIdentities map[string]os.FileInfo
			attack := func() error {
				if attacked {
					return nil
				}
				if err := os.Rename(filepath.Join(root, "nested"), filepath.Join(root, "parked")); err != nil {
					return err
				}
				if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
					return err
				}
				attacked = true
				attackedTree = documentSessionCaptureTree(t, root)
				attackedIdentities = documentPublicationIdentities(t, root)
				return nil
			}

			// Act.
			recovered, recoveryErr := openDocumentSessionContext(
				context.Background(),
				target,
				"",
				documentSessionHooks{publish: documentPublishHooks{
					beforeCleanup: func(_ *os.Root, kind, _ string) error {
						if kind != cleanup.kind {
							return nil
						}
						return attack()
					},
				}},
			)
			if recovered != nil {
				_ = recovered.Close()
			}

			// Assert.
			if !attacked || recovered != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
				t.Fatalf("attacked=%t session=%#v error=%v, want typed recovery detach conflict at %s", attacked, recovered, recoveryErr, cleanup.name)
			}
			documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
			documentAssertPublicationIdentities(t, root, attackedIdentities)
			assertDocumentDetachedRestartIsZeroMutation(t, root, target)
		})
	}
}

func testDocumentRecoveryDetachBeforeInstall(t *testing.T, afterAnchorDurable bool) {
	t.Helper()

	// Arrange a missing target with retained v2 recovery evidence.
	root := t.TempDir()
	target := filepath.Join(root, "nested", "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
	state := documentV2State(true, true, true, false, true, documentV2TargetMissing)
	if afterAnchorDurable {
		state = documentV2State(true, false, false, false, true, documentV2TargetMissing)
	}
	documentSessionCreateV2RecoveryState(
		t,
		root,
		"nested/concept.md",
		0o640,
		original,
		documentSessionConcept("concept", "after"),
		state,
	)
	attacked := false
	var attackedTree map[string]documentSessionTreeEntry
	var attackedIdentities map[string]os.FileInfo
	attack := func() error {
		if attacked {
			return nil
		}
		if err := os.Rename(filepath.Join(root, "nested"), filepath.Join(root, "parked")); err != nil {
			return err
		}
		if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
			return err
		}
		attacked = true
		attackedTree = documentSessionCaptureTree(t, root)
		attackedIdentities = documentPublicationIdentities(t, root)
		return nil
	}
	hooks := documentPublishHooks{}
	if afterAnchorDurable {
		hooks.fileSync = func(file *os.File) error {
			return file.Sync()
		}
		hooks.directorySync = func(parent *os.Root) error {
			if err := syncPublicationDirectoryPlatform(parent); err != nil {
				return err
			}
			entries, err := os.ReadDir(parent.Name())
			if err != nil {
				return err
			}
			hasAnchor := false
			for _, entry := range entries {
				kind, _, ok := parseDocumentArtifactName(entry.Name())
				if ok && kind == documentRollbackAnchorProtocolKind(0) {
					hasAnchor = true
					break
				}
			}
			if !hasAnchor {
				return nil
			}
			return attack()
		}
	} else {
		hooks.beforeInstall = func(*os.Root, string, string) error {
			return attack()
		}
	}

	// Act.
	recovered, recoveryErr := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: hooks},
	)
	if recovered != nil {
		_ = recovered.Close()
	}

	// Assert.
	if !attacked || recovered != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
		t.Fatalf("attacked=%t session=%#v error=%v, want typed detached recovery install conflict", attacked, recovered, recoveryErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
	documentAssertPublicationIdentities(t, root, attackedIdentities)
	assertDocumentDetachedRestartIsZeroMutation(t, root, target)
}

func assertDocumentDetachedRestartIsZeroMutation(t *testing.T, root, target string) {
	t.Helper()
	before := documentSessionCaptureTree(t, root)
	beforeIdentities := documentPublicationIdentities(t, root)
	restarted, restartErr := OpenDocumentSessionContext(context.Background(), target, "")
	if restarted != nil {
		_ = restarted.Close()
	}
	if restartErr == nil {
		t.Fatal("restart accepted detached document transaction")
	}
	documentSessionAssertTreeSnapshotEqual(t, before, documentSessionCaptureTree(t, root))
	documentAssertPublicationIdentities(t, root, beforeIdentities)
}

func TestDocumentActiveRollbackRevalidatesCrossEvidenceAfterV2Hooks(t *testing.T) {
	t.Parallel()

	for _, cut := range []struct {
		name       string
		current    string
		mutateKind string
		targetSwap bool
	}{
		{name: "before cleanup C mutate B", current: "claim", mutateKind: "backup"},
		{name: "before cleanup B mutate W", current: "backup", mutateKind: "witness"},
		{name: "before cleanup M mutate A", current: "manifest", mutateKind: "anchor"},
		{name: "before cleanup proof A swap target", current: "anchor", targetSwap: true},
	} {
		cut := cut
		t.Run(cut.name, func(t *testing.T) {
			t.Parallel()
			testDocumentActiveCrossEvidenceCleanupCut(t, cut.current, cut.mutateKind, cut.targetSwap)
		})
	}
	for _, mutation := range []string{"anchor", "backup", "target"} {
		mutation := mutation
		t.Run("before rollback install mutate "+mutation, func(t *testing.T) {
			t.Parallel()
			testDocumentActiveCrossEvidenceInstallCut(t, mutation)
		})
	}
}

func TestDocumentActiveRollbackPostDiscardOwnershipContract(t *testing.T) {
	t.Parallel()

	for _, mutation := range []struct {
		name string
		kind string
		soft bool
	}{
		{name: "soft S drift", kind: "stage", soft: true},
		{name: "soft B drift", kind: "backup", soft: true},
		{name: "soft C drift", kind: "claim", soft: true},
		{name: "soft W drift", kind: "witness", soft: true},
		{name: "soft D drift", kind: "discard", soft: true},
		{name: "hard A drift", kind: documentRollbackAnchorProtocolKind(0)},
		{name: "hard R drift", kind: "restore-install"},
		{name: "hard parent replacement", kind: "parent"},
		{name: "hard foreign target", kind: "target"},
	} {
		mutation := mutation
		t.Run(mutation.name, func(t *testing.T) {
			t.Parallel()
			testDocumentActivePostDiscardOwnershipMutation(t, mutation.kind, mutation.soft)
		})
	}
}

func testDocumentActivePostDiscardOwnershipMutation(
	t *testing.T,
	mutationKind string,
	soft bool,
) {
	t.Helper()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "nested", "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	rollbackFault := errors.New("injected rollback after install")
	rollingBack := false
	mutated := false
	var mutatedPath string
	var restorePath string
	var anchorPath string
	var attackedTree map[string]documentSessionTreeEntry
	var attackedIdentities map[string]os.FileInfo
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: documentPublishHooks{
			afterInstall: func(*os.Root, string) error {
				if rollingBack {
					return nil
				}
				rollingBack = true
				return rollbackFault
			},
			afterDiscardSync: func(*os.Root, string) error {
				if mutated {
					return nil
				}
				anchorPath = documentSessionSingleRecoveryArtifact(
					t,
					root,
					documentRollbackAnchorProtocolKind(0),
				)
				restorePath = documentSessionSingleRecoveryArtifact(
					t,
					root,
					"restore-install",
				)
				switch mutationKind {
				case "parent":
					if err := os.Rename(
						filepath.Join(root, "nested"),
						filepath.Join(root, "parked"),
					); err != nil {
						return err
					}
					if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
						return err
					}
				case "target":
					documentSessionWriteFile(t, target, "foreign\n")
					if err := os.Chmod(target, 0o600); err != nil {
						return err
					}
				default:
					mutatedPath = documentSessionSingleRecoveryArtifact(
						t,
						root,
						mutationKind,
					)
					file, openErr := os.OpenFile(mutatedPath, os.O_RDWR, 0)
					if openErr != nil {
						return openErr
					}
					_, writeErr := file.WriteAt([]byte("X"), 0)
					syncErr := file.Sync()
					closeErr := file.Close()
					if writeErr != nil || syncErr != nil || closeErr != nil {
						return errors.Join(writeErr, syncErr, closeErr)
					}
				}
				mutated = true
				attackedTree = documentSessionCaptureTree(t, root)
				attackedIdentities = documentPublicationIdentities(t, root)
				return nil
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	rewriteErr := session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !mutated ||
		!errors.Is(rewriteErr, rollbackFault) ||
		!errors.Is(rewriteErr, ErrDocumentConflict) {
		t.Fatalf(
			"mutated=%t error=%v, want typed post-D ownership conflict for %s",
			mutated,
			rewriteErr,
			mutationKind,
		)
	}
	if !soft {
		documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
		documentAssertPublicationIdentities(t, root, attackedIdentities)
		return
	}
	documentAssertSoftRestoreInstallConsumed(
		t,
		root,
		target,
		original,
		anchorPath,
		restorePath,
		mutatedPath,
		mutationKind,
		attackedIdentities,
		rewriteErr,
	)
}

func documentAssertSoftRestoreInstallConsumed(
	t *testing.T,
	root string,
	target string,
	original string,
	anchorPath string,
	restorePath string,
	mutatedPath string,
	mutationKind string,
	attackedIdentities map[string]os.FileInfo,
	operationErr error,
) {
	t.Helper()

	restoreRelative, err := filepath.Rel(root, restorePath)
	if err != nil {
		t.Fatal(err)
	}
	targetRelative, err := filepath.Rel(root, target)
	if err != nil {
		t.Fatal(err)
	}
	afterIdentities := documentPublicationIdentities(t, root)
	delete(attackedIdentities, filepath.ToSlash(restoreRelative))
	delete(afterIdentities, filepath.ToSlash(targetRelative))
	if len(afterIdentities) != len(attackedIdentities) {
		t.Fatalf(
			"soft restore convergence changed evidence cardinality: before=%v after=%v error=%v",
			documentSortedIdentityNames(attackedIdentities),
			documentSortedIdentityNames(afterIdentities),
			operationErr,
		)
	}
	for relative, before := range attackedIdentities {
		after, present := afterIdentities[relative]
		if !present || !os.SameFile(before, after) {
			t.Fatalf(
				"soft restore convergence changed %s identity: before=%v after=%v",
				relative,
				before,
				after,
			)
		}
	}
	if _, statErr := os.Lstat(restorePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("soft %s drift did not consume R: %v", mutationKind, statErr)
	}
	targetInfo, targetErr := os.Lstat(target)
	anchorInfo, anchorErr := os.Lstat(anchorPath)
	if targetErr != nil || anchorErr != nil || !os.SameFile(targetInfo, anchorInfo) {
		t.Fatalf(
			"soft %s drift did not consume R to exact old target: target=%v A=%v errors=%v/%v",
			mutationKind,
			targetInfo,
			anchorInfo,
			targetErr,
			anchorErr,
		)
	}
	if got := documentSessionReadFile(t, target); got != original {
		t.Fatalf("soft %s drift restored bytes = %q, want %q", mutationKind, got, original)
	}
	if got := documentSessionReadFile(t, mutatedPath); !strings.HasPrefix(got, "X") {
		t.Fatalf("soft %s drift was not preserved: %q", mutationKind, got)
	}
}

func documentSortedIdentityNames(identities map[string]os.FileInfo) []string {
	names := make([]string, 0, len(identities))
	for name := range identities {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestDocumentRecoveryRevalidatesCrossEvidenceAfterV2Hooks(t *testing.T) {
	t.Parallel()

	for _, cut := range []struct {
		name       string
		current    string
		mutateKind string
		targetSwap bool
	}{
		{name: "before cleanup C mutate B", current: "claim", mutateKind: "backup"},
		{name: "before cleanup B mutate W", current: "backup", mutateKind: "witness"},
		{name: "before cleanup M mutate A", current: "manifest", mutateKind: documentRollbackAnchorProtocolKind(0)},
		{name: "before cleanup proof A swap target", current: documentRollbackAnchorProtocolKind(0), targetSwap: true},
	} {
		cut := cut
		t.Run(cut.name, func(t *testing.T) {
			t.Parallel()
			testDocumentRecoveryCrossEvidenceCleanupCut(t, cut.current, cut.mutateKind, cut.targetSwap)
		})
	}
	for _, mutation := range []string{"anchor", "backup", "claim", "witness", "target"} {
		mutation := mutation
		t.Run("before recovery install mutate "+mutation, func(t *testing.T) {
			t.Parallel()
			testDocumentRecoveryCrossEvidenceInstallCut(t, mutation)
		})
	}
}

func testDocumentActiveCrossEvidenceCleanupCut(
	t *testing.T,
	currentKind string,
	mutateKind string,
	targetSwap bool,
) {
	t.Helper()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	rollbackFault := errors.New("injected rollback after install")
	rollingBack := false
	mutated := false
	held := make(map[string]*os.File)
	openHeld := func(parent *os.Root, kind, name string) error {
		if held[kind] != nil {
			return nil
		}
		file, err := parent.OpenFile(name, os.O_RDWR, 0)
		if err == nil {
			held[kind] = file
		}
		return err
	}
	var attackedTree map[string]documentSessionTreeEntry
	var attackedIdentities map[string]os.FileInfo
	hooks := documentPublishHooks{
		afterWitnessSync: func(parent *os.Root, name string) error {
			return openHeld(parent, "witness", name)
		},
		afterBackupSync: func(parent *os.Root, name string) error {
			return openHeld(parent, "backup", name)
		},
		afterStageSync: func(parent *os.Root, name string) error {
			return openHeld(parent, "stage", name)
		},
		afterManifestSync: func(parent *os.Root, name string) error {
			return openHeld(parent, "manifest", name)
		},
		afterVacateRename: func(parent *os.Root, _, claim string) error {
			if rollingBack {
				return nil
			}
			return openHeld(parent, "claim", claim)
		},
		beforeInstall: func(parent *os.Root, source, _ string) error {
			if !rollingBack {
				return nil
			}
			return openHeld(parent, "anchor", source)
		},
		afterInstall: func(*os.Root, string) error {
			if rollingBack {
				return nil
			}
			rollingBack = true
			return rollbackFault
		},
		beforeCleanup: func(_ *os.Root, kind, _ string) error {
			if kind != currentKind || mutated {
				return nil
			}
			if targetSwap {
				documentSessionAtomicReplace(t, target, original, 0o640)
			} else {
				file := held[mutateKind]
				if file == nil {
					return fmt.Errorf("held %s evidence is unavailable", mutateKind)
				}
				if _, err := file.WriteAt([]byte("X"), 0); err != nil {
					return err
				}
				if err := file.Sync(); err != nil {
					return err
				}
			}
			mutated = true
			attackedTree = documentSessionCaptureTree(t, root)
			attackedIdentities = documentPublicationIdentities(t, root)
			return nil
		},
	}
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: hooks},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, file := range held {
			_ = file.Close()
		}
		_ = session.Close()
	})

	// Act.
	rewriteErr := session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !mutated ||
		!errors.Is(rewriteErr, rollbackFault) ||
		!errors.Is(rewriteErr, ErrDocumentConflict) {
		t.Fatalf("mutated=%t error=%v, want typed cross-evidence conflict", mutated, rewriteErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
	documentAssertPublicationIdentities(t, root, attackedIdentities)
}

func testDocumentActiveCrossEvidenceInstallCut(t *testing.T, mutation string) {
	t.Helper()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	rollbackFault := errors.New("injected rollback after install")
	rollingBack := false
	mutated := false
	var heldBackup *os.File
	var attackedTree map[string]documentSessionTreeEntry
	var attackedIdentities map[string]os.FileInfo
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: documentPublishHooks{
			afterBackupSync: func(parent *os.Root, name string) error {
				var openErr error
				heldBackup, openErr = parent.OpenFile(name, os.O_RDWR, 0)
				return openErr
			},
			beforeInstall: func(parent *os.Root, source, _ string) error {
				if !rollingBack || mutated {
					return nil
				}
				switch mutation {
				case "anchor":
					file, openErr := parent.OpenFile(source, os.O_RDWR, 0)
					if openErr != nil {
						return openErr
					}
					_, writeErr := file.WriteAt([]byte("X"), 0)
					syncErr := file.Sync()
					closeErr := file.Close()
					if writeErr != nil || syncErr != nil || closeErr != nil {
						return errors.Join(writeErr, syncErr, closeErr)
					}
				case "backup":
					if _, writeErr := heldBackup.WriteAt([]byte("X"), 0); writeErr != nil {
						return writeErr
					}
					if syncErr := heldBackup.Sync(); syncErr != nil {
						return syncErr
					}
				case "target":
					documentSessionAtomicReplace(t, target, original, 0o640)
				default:
					return fmt.Errorf("unknown install mutation %q", mutation)
				}
				mutated = true
				attackedTree = documentSessionCaptureTree(t, root)
				attackedIdentities = documentPublicationIdentities(t, root)
				return nil
			},
			afterInstall: func(*os.Root, string) error {
				if rollingBack {
					return nil
				}
				rollingBack = true
				return rollbackFault
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if heldBackup != nil {
			_ = heldBackup.Close()
		}
		_ = session.Close()
	})

	// Act.
	rewriteErr := session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !mutated ||
		!errors.Is(rewriteErr, rollbackFault) ||
		!errors.Is(rewriteErr, ErrDocumentConflict) {
		t.Fatalf("mutated=%t error=%v, want typed install-boundary conflict", mutated, rewriteErr)
	}
	if mutation == "anchor" {
		documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
		documentAssertPublicationIdentities(t, root, attackedIdentities)
		return
	}
	if mutation == "backup" {
		anchor := documentSessionSingleRecoveryArtifact(
			t,
			root,
			documentRollbackAnchorProtocolKind(0),
		)
		anchorInfo, anchorErr := os.Lstat(anchor)
		targetInfo, targetErr := os.Lstat(target)
		backupInfo, backupErr := os.Lstat(documentSessionSingleRecoveryArtifact(t, root, "backup"))
		if anchorErr != nil || targetErr != nil || backupErr != nil ||
			!os.SameFile(targetInfo, anchorInfo) ||
			os.SameFile(targetInfo, backupInfo) {
			t.Fatalf("soft B drift did not restore exact A: A=%v B=%v target=%v errors=%v/%v/%v", anchorInfo, backupInfo, targetInfo, anchorErr, backupErr, targetErr)
		}
		return
	}
	documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
	documentAssertPublicationIdentities(t, root, attackedIdentities)
}

func testDocumentRecoveryCrossEvidenceCleanupCut(
	t *testing.T,
	currentKind string,
	mutateKind string,
	targetSwap bool,
) {
	t.Helper()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
	fixture := documentSessionCreateV2RecoveryState(
		t,
		root,
		"concept.md",
		0o640,
		original,
		documentSessionConcept("concept", "after"),
		documentV2State(false, true, false, true, true, documentV2TargetAnchor),
	)
	held := make(map[string]*os.File)
	for _, kind := range []string{
		"witness",
		"backup",
		"claim",
		"stage",
		"manifest",
		documentRollbackAnchorProtocolKind(0),
	} {
		file, err := os.OpenFile(filepath.Join(root, fixture.names[kind]), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		held[kind] = file
	}
	t.Cleanup(func() {
		for _, file := range held {
			_ = file.Close()
		}
	})
	mutated := false
	var attackedTree map[string]documentSessionTreeEntry
	var attackedIdentities map[string]os.FileInfo

	// Act.
	recovered, recoveryErr := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: documentPublishHooks{
			beforeCleanup: func(_ *os.Root, kind, _ string) error {
				if kind != currentKind || mutated {
					return nil
				}
				if targetSwap {
					documentSessionAtomicReplace(t, target, original, 0o640)
				} else {
					file := held[mutateKind]
					if _, err := file.WriteAt([]byte("X"), 0); err != nil {
						return err
					}
					if err := file.Sync(); err != nil {
						return err
					}
				}
				mutated = true
				attackedTree = documentSessionCaptureTree(t, root)
				attackedIdentities = documentPublicationIdentities(t, root)
				return nil
			},
		}},
	)
	if recovered != nil {
		_ = recovered.Close()
	}

	// Assert.
	if !mutated || recovered != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
		t.Fatalf("mutated=%t session=%#v error=%v, want typed recovery cross-evidence conflict", mutated, recovered, recoveryErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
	documentAssertPublicationIdentities(t, root, attackedIdentities)
}

func testDocumentRecoveryCrossEvidenceInstallCut(t *testing.T, mutation string) {
	t.Helper()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
	fixture := documentSessionCreateV2RecoveryState(
		t,
		root,
		"concept.md",
		0o640,
		original,
		documentSessionConcept("concept", "after"),
		documentV2State(true, true, true, false, true, documentV2TargetMissing),
	)
	anchorPath := filepath.Join(root, fixture.names[documentRollbackAnchorProtocolKind(0)])
	restorePath := filepath.Join(root, fixture.names["restore-install"])
	var mutatedPath string
	mutated := false
	var attackedTree map[string]documentSessionTreeEntry
	var attackedIdentities map[string]os.FileInfo

	// Act.
	recovered, recoveryErr := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: documentPublishHooks{
			beforeInstall: func(parent *os.Root, source, _ string) error {
				if mutated {
					return nil
				}
				switch mutation {
				case "anchor":
					file, openErr := parent.OpenFile(source, os.O_RDWR, 0)
					if openErr != nil {
						return openErr
					}
					_, writeErr := file.WriteAt([]byte("X"), 0)
					syncErr := file.Sync()
					closeErr := file.Close()
					if writeErr != nil || syncErr != nil || closeErr != nil {
						return errors.Join(writeErr, syncErr, closeErr)
					}
				case "backup", "claim", "witness":
					mutatedPath = filepath.Join(root, fixture.names[mutation])
					file, openErr := os.OpenFile(mutatedPath, os.O_RDWR, 0)
					if openErr != nil {
						return openErr
					}
					_, writeErr := file.WriteAt([]byte("X"), 0)
					syncErr := file.Sync()
					closeErr := file.Close()
					if writeErr != nil || syncErr != nil || closeErr != nil {
						return errors.Join(writeErr, syncErr, closeErr)
					}
				case "target":
					documentSessionAtomicReplace(t, target, original, 0o640)
				default:
					return fmt.Errorf("unknown recovery install mutation %q", mutation)
				}
				mutated = true
				attackedTree = documentSessionCaptureTree(t, root)
				attackedIdentities = documentPublicationIdentities(t, root)
				return nil
			},
		}},
	)
	if recovered != nil {
		_ = recovered.Close()
	}

	// Assert.
	if !mutated || recovered != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
		t.Fatalf("mutated=%t session=%#v error=%v, want typed recovery install conflict", mutated, recovered, recoveryErr)
	}
	if mutation == "backup" || mutation == "claim" || mutation == "witness" {
		documentAssertSoftRestoreInstallConsumed(
			t,
			root,
			target,
			original,
			anchorPath,
			restorePath,
			mutatedPath,
			mutation,
			attackedIdentities,
			recoveryErr,
		)
		return
	}
	documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
	documentAssertPublicationIdentities(t, root, attackedIdentities)
}

func TestDocumentPrepareDurabilityCrashCutsRecoverWithoutMutatingOriginal(t *testing.T) {
	tests := []struct {
		point string
		kinds []string
	}{
		{point: "witness", kinds: []string{"witness"}},
		{point: "backup", kinds: []string{"backup", "witness"}},
		{point: "stage", kinds: []string{"backup", "stage", "witness"}},
		{point: "manifest", kinds: []string{"backup", "manifest", "new-install", "stage", "witness"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.point, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
			documentSessionWriteFile(t, target, original)
			if err := os.Chmod(target, 0o640); err != nil {
				t.Fatal(err)
			}
			originalInfo, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			runDocumentPrepareCrashHelper(t, root, test.point)

			// Assert the prepare phase did not mutate the public target.
			targetInfo, err := os.Lstat(target)
			if err != nil || !os.SameFile(originalInfo, targetInfo) {
				t.Fatalf("target identity changed at %s cut: before=%v after=%v error=%v", test.point, originalInfo, targetInfo, err)
			}
			if got := documentSessionReadFile(t, target); got != original {
				t.Fatalf("target bytes at %s cut = %q, want %q", test.point, got, original)
			}
			artifacts := documentPrepareCrashArtifacts(t, root)
			if got := documentPrepareCrashArtifactKinds(artifacts); !reflect.DeepEqual(got, test.kinds) {
				t.Fatalf("artifact kinds at %s cut = %#v, want %#v", test.point, got, test.kinds)
			}
			assertDocumentPrepareArtifactIdentities(t, target, original, artifacts)

			recovered, recoveryErr := OpenDocumentSessionContext(context.Background(), target, "")
			if recoveryErr != nil {
				t.Fatalf(
					"OpenDocumentSessionContext() after %s cut: %v; remaining=%s",
					test.point,
					recoveryErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			if closeErr := recovered.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			afterInfo, err := os.Lstat(target)
			if err != nil || !os.SameFile(originalInfo, afterInfo) {
				t.Fatalf("recovery changed original identity at %s cut: before=%v after=%v error=%v", test.point, originalInfo, afterInfo, err)
			}
			if got := documentSessionReadFile(t, target); got != original {
				t.Fatalf("recovered bytes at %s cut = %q, want %q", test.point, got, original)
			}
			documentSessionAssertNoArtifacts(t, root)
		})
	}
}

func TestDocumentPreManifestWitnessRejectsSameBytesDifferentTargetInodeWithoutMutation(t *testing.T) {
	for _, point := range []string{"witness", "backup", "stage"} {
		point := point
		t.Run(point, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
			documentSessionWriteFile(t, target, original)
			if err := os.Chmod(target, 0o640); err != nil {
				t.Fatal(err)
			}
			runDocumentPrepareCrashHelper(t, root, point)
			beforeSwap, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			documentSessionAtomicReplace(t, target, original, 0o640)
			afterSwap, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			if os.SameFile(beforeSwap, afterSwap) {
				t.Fatal("same-byte replacement unexpectedly preserved target inode")
			}
			beforeRecovery := documentSessionCaptureTree(t, root)

			// Act.
			session, recoveryErr := OpenDocumentSessionContext(context.Background(), target, "")
			if session != nil {
				_ = session.Close()
			}
			afterRecovery := documentSessionCaptureTree(t, root)

			// Assert.
			if session != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
				t.Fatalf("session=%#v error=%v, want identity-bound ErrDocumentConflict", session, recoveryErr)
			}
			documentSessionAssertTreeSnapshotEqual(t, beforeRecovery, afterRecovery)
		})
	}
}

func TestDocumentManifestPreMutationRequiresCompleteExactEvidenceWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root, target, original string, artifacts map[string]string)
	}{
		{
			name: "missing witness",
			mutate: func(t *testing.T, _, _, _ string, artifacts map[string]string) {
				t.Helper()
				if err := os.Remove(artifacts["witness"]); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing backup",
			mutate: func(t *testing.T, _, _, _ string, artifacts map[string]string) {
				t.Helper()
				if err := os.Remove(artifacts["backup"]); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing stage",
			mutate: func(t *testing.T, _, _, _ string, artifacts map[string]string) {
				t.Helper()
				if err := os.Remove(artifacts["stage"]); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra claim",
			mutate: func(t *testing.T, root, target, _ string, artifacts map[string]string) {
				t.Helper()
				id := documentPrepareManifestID(t, artifacts)
				if err := os.Link(target, filepath.Join(root, documentArtifactName("claim", id))); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra rollback anchor",
			mutate: func(t *testing.T, root, target, original string, _ map[string]string) {
				t.Helper()
				name := documentRollbackAnchorName("concept.md", 0o640, []byte(original), 0)
				if err := os.Link(target, filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra discard",
			mutate: func(t *testing.T, root, _, _ string, artifacts map[string]string) {
				t.Helper()
				id := documentPrepareManifestID(t, artifacts)
				if err := os.Link(
					artifacts["stage"],
					filepath.Join(root, documentArtifactName("discard", id)),
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "same bytes under detached target inode",
			mutate: func(t *testing.T, _, target, original string, _ map[string]string) {
				t.Helper()
				documentSessionAtomicReplace(t, target, original, 0o640)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
			documentSessionWriteFile(t, target, original)
			if err := os.Chmod(target, 0o640); err != nil {
				t.Fatal(err)
			}
			runDocumentPrepareCrashHelper(t, root, "manifest")
			artifacts := documentPrepareCrashArtifacts(t, root)
			test.mutate(t, root, target, original, artifacts)
			before := documentSessionCaptureTree(t, root)
			beforeIdentities := documentPublicationIdentities(t, root)

			// Act.
			session, recoveryErr := OpenDocumentSessionContext(context.Background(), target, "")
			if session != nil {
				_ = session.Close()
			}
			after := documentSessionCaptureTree(t, root)

			// Assert.
			if session != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
				t.Fatalf("session=%#v error=%v, want manifest-shape ErrDocumentConflict", session, recoveryErr)
			}
			documentSessionAssertTreeSnapshotEqual(t, before, after)
			documentAssertPublicationIdentities(t, root, beforeIdentities)
		})
	}
}

func TestDocumentCommittedRecoveryAcceptsOnlyCanonicalCleanupPrefixes(t *testing.T) {
	tests := []struct {
		name           string
		cleanupOrdinal int
		wantKinds      []string
	}{
		{
			name:           "complete committed set",
			cleanupOrdinal: -1,
			wantKinds:      []string{"backup", "claim", "manifest", "stage", "witness"},
		},
		{
			name:           "manifest removed",
			cleanupOrdinal: 0,
			wantKinds:      []string{"backup", "claim", "stage", "witness"},
		},
		{
			name:           "claim removed",
			cleanupOrdinal: 1,
			wantKinds:      []string{"backup", "stage", "witness"},
		},
		{
			name:           "backup removed",
			cleanupOrdinal: 2,
			wantKinds:      []string{"stage", "witness"},
		},
		{
			name:           "witness removed terminal stage",
			cleanupOrdinal: 3,
			wantKinds:      []string{"stage"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
			if err := os.Chmod(target, 0o640); err != nil {
				t.Fatal(err)
			}
			runDocumentPrepareCrashHelper(t, root, "installed")
			if test.cleanupOrdinal >= 0 {
				runDocumentPrepareCrashHelper(
					t,
					root,
					"recovery-cleanup-"+strconv.Itoa(test.cleanupOrdinal),
				)
			}
			artifacts := documentPrepareCrashArtifacts(t, root)

			// Assert the only accepted committed states form the cleanup prefix.
			if got := documentPrepareCrashArtifactKinds(artifacts); !reflect.DeepEqual(got, test.wantKinds) {
				t.Fatalf("committed cleanup kinds = %#v, want %#v", got, test.wantKinds)
			}
			stageInfo, err := os.Lstat(artifacts["stage"])
			if err != nil {
				t.Fatal(err)
			}
			targetInfo, err := os.Lstat(target)
			if err != nil || !os.SameFile(targetInfo, stageInfo) {
				t.Fatalf("published target is not identity-bound to stage: target=%v stage=%v error=%v", targetInfo, stageInfo, err)
			}
			if got, want := documentSessionReadFile(t, target), documentSessionConcept("concept", "after"); got != want {
				t.Fatalf("published target bytes = %q, want %q", got, want)
			}
			if witnessPath := artifacts["witness"]; witnessPath != "" {
				witnessInfo, witnessErr := os.Lstat(witnessPath)
				if witnessErr != nil || os.SameFile(targetInfo, witnessInfo) {
					t.Fatalf("published target aliases original witness: target=%v witness=%v error=%v", targetInfo, witnessInfo, witnessErr)
				}
			}
			if backupPath := artifacts["backup"]; backupPath != "" {
				backupInfo, backupErr := os.Lstat(backupPath)
				if backupErr != nil || os.SameFile(targetInfo, backupInfo) {
					t.Fatalf("published target aliases independent backup: target=%v backup=%v error=%v", targetInfo, backupInfo, backupErr)
				}
			}

			recovered, recoveryErr := OpenDocumentSessionContext(context.Background(), target, "")
			if recoveryErr != nil {
				t.Fatalf("recovery from canonical prefix %q: %v", test.name, recoveryErr)
			}
			if closeErr := recovered.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			documentSessionAssertNoArtifacts(t, root)
		})
	}
}

func TestDocumentCommittedRecoveryRejectsNonPrefixInventoryWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root, target string, artifacts map[string]string)
	}{
		{
			name: "arbitrary missing backup",
			mutate: func(t *testing.T, _, _ string, artifacts map[string]string) {
				t.Helper()
				if err := os.Remove(artifacts["backup"]); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra discard",
			mutate: func(t *testing.T, root, _ string, artifacts map[string]string) {
				t.Helper()
				id := documentPrepareManifestID(t, artifacts)
				if err := os.Link(
					artifacts["stage"],
					filepath.Join(root, documentArtifactName("discard", id)),
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "duplicate private witness alias",
			mutate: func(t *testing.T, root, _ string, artifacts map[string]string) {
				t.Helper()
				parent, err := openRootWithoutSymlinks(root)
				if err != nil {
					t.Fatal(err)
				}
				witnessName := filepath.Base(artifacts["witness"])
				privateName, err := allocatePrivatePublicationClaim(parent, witnessName)
				if err == nil {
					err = parent.Link(witnessName, privateName)
				}
				if closeErr := parent.Close(); err == nil {
					err = closeErr
				}
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "same bytes under detached target inode",
			mutate: func(t *testing.T, _, target string, _ map[string]string) {
				t.Helper()
				documentSessionAtomicReplace(
					t,
					target,
					documentSessionConcept("concept", "after"),
					0o640,
				)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
			if err := os.Chmod(target, 0o640); err != nil {
				t.Fatal(err)
			}
			runDocumentPrepareCrashHelper(t, root, "installed")
			artifacts := documentPrepareCrashArtifacts(t, root)
			test.mutate(t, root, target, artifacts)
			before := documentSessionCaptureTree(t, root)
			beforeIdentities := documentPublicationIdentities(t, root)

			// Act.
			session, recoveryErr := OpenDocumentSessionContext(context.Background(), target, "")
			if session != nil {
				_ = session.Close()
			}
			after := documentSessionCaptureTree(t, root)

			// Assert.
			if session != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
				t.Fatalf("session=%#v error=%v, want committed inventory ErrDocumentConflict", session, recoveryErr)
			}
			documentSessionAssertTreeSnapshotEqual(t, before, after)
			documentAssertPublicationIdentities(t, root, beforeIdentities)
		})
	}
}

func TestOpenDocumentSessionRejectsFIFOWithoutOpeningIt(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "pipe.md")
	if err := syscall.Mkfifo(target, 0o600); err != nil {
		t.Fatalf("Mkfifo() error = %v", err)
	}

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if session != nil {
		_ = session.Close()
	}

	// Assert.
	if !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("OpenDocumentSessionContext(FIFO) error = %v, want ErrNotRegularFile", err)
	}
	if session != nil {
		t.Fatal("OpenDocumentSessionContext(FIFO) returned a session")
	}
}

func runDocumentPrepareCrashHelper(t *testing.T, root, point string) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestDocumentPrepareCrashSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_DOCUMENT_PREPARE_CRASH_ROOT="+root,
		"OKF_DOCUMENT_PREPARE_CRASH_POINT="+point,
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err == nil || command.ProcessState == nil || command.ProcessState.ExitCode() != 87 {
		t.Fatalf(
			"document prepare crash helper exit=%v error=%v at %s, want 87\n%s",
			command.ProcessState,
			err,
			point,
			output.String(),
		)
	}
}

func documentPrepareCrashArtifacts(t *testing.T, root string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := make(map[string]string)
	for _, entry := range entries {
		if !isDocumentTransactionReservedPath(entry.Name()) {
			continue
		}
		kind, _, ok := parseDocumentArtifactName(entry.Name())
		if !ok {
			t.Fatalf("reserved document artifact %q is not canonical", entry.Name())
		}
		if previous := artifacts[kind]; previous != "" {
			t.Fatalf("duplicate %s artifacts: %q and %q", kind, previous, entry.Name())
		}
		artifacts[kind] = filepath.Join(root, entry.Name())
	}
	return artifacts
}

func documentPrepareCrashArtifactKinds(artifacts map[string]string) []string {
	kinds := make([]string, 0, len(artifacts))
	for kind := range artifacts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func documentPrepareManifestID(t *testing.T, artifacts map[string]string) string {
	t.Helper()
	manifestPath := artifacts["manifest"]
	kind, id, ok := parseDocumentArtifactName(filepath.Base(manifestPath))
	if !ok || kind != "manifest" {
		t.Fatalf("manifest path %q is not canonical", manifestPath)
	}
	return id
}

func assertDocumentPrepareArtifactIdentities(
	t *testing.T,
	target string,
	original string,
	artifacts map[string]string,
) {
	t.Helper()
	targetInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	witnessInfo, err := os.Lstat(artifacts["witness"])
	if err != nil || !os.SameFile(targetInfo, witnessInfo) {
		t.Fatalf("witness does not alias original target: target=%v witness=%v error=%v", targetInfo, witnessInfo, err)
	}
	if got := documentSessionReadFile(t, artifacts["witness"]); got != original {
		t.Fatalf("witness bytes = %q, want %q", got, original)
	}
	if backupPath := artifacts["backup"]; backupPath != "" {
		backupInfo, statErr := os.Lstat(backupPath)
		if statErr != nil ||
			os.SameFile(targetInfo, backupInfo) ||
			os.SameFile(witnessInfo, backupInfo) {
			t.Fatalf("backup is not independent: target=%v witness=%v backup=%v error=%v", targetInfo, witnessInfo, backupInfo, statErr)
		}
		if got := documentSessionReadFile(t, backupPath); got != original {
			t.Fatalf("backup bytes = %q, want %q", got, original)
		}
	}
	if stagePath := artifacts["stage"]; stagePath != "" {
		stageInfo, statErr := os.Lstat(stagePath)
		if statErr != nil || os.SameFile(targetInfo, stageInfo) {
			t.Fatalf("stage aliases original target: target=%v stage=%v error=%v", targetInfo, stageInfo, statErr)
		}
		if backupPath := artifacts["backup"]; backupPath != "" {
			backupInfo, backupErr := os.Lstat(backupPath)
			if backupErr != nil || os.SameFile(backupInfo, stageInfo) {
				t.Fatalf("stage aliases backup: backup=%v stage=%v error=%v", backupInfo, stageInfo, backupErr)
			}
		}
		if got, want := documentSessionReadFile(t, stagePath), documentSessionConcept("concept", "after"); got != want {
			t.Fatalf("stage bytes = %q, want %q", got, want)
		}
	}
}

func TestOpenDocumentSessionRecoveryRejectsFIFOClaimWithoutMutation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
	fixture := documentSessionCreateV2RecoveryState(
		t,
		root,
		"concept.md",
		0o640,
		original,
		documentSessionConcept("concept", "after"),
		documentV2State(true, true, false, false, true, documentV2TargetMissing),
	)
	claimPath := filepath.Join(root, fixture.names["claim"])
	if err := os.Remove(claimPath); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(claimPath, 0o600); err != nil {
		t.Skipf("mkfifo is unavailable: %v", err)
	}
	claimInfo, err := os.Lstat(claimPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeTree := documentSessionCaptureTree(t, root)
	beforeIdentities := documentPublicationIdentities(t, root)

	// Act.
	session, recoveryErr := OpenDocumentSessionContext(context.Background(), target, "")
	if session != nil {
		_ = session.Close()
	}

	// Assert.
	if session != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
		t.Fatalf("session=%#v error=%v, want hard FIFO-claim conflict", session, recoveryErr)
	}
	afterClaim, statErr := os.Lstat(claimPath)
	if statErr != nil || !os.SameFile(claimInfo, afterClaim) || afterClaim.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO claim changed: before=%v after=%v error=%v", claimInfo, afterClaim, statErr)
	}
	if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("hard FIFO conflict changed missing target: %v", statErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, beforeTree, documentSessionCaptureTree(t, root))
	documentAssertPublicationIdentities(t, root, beforeIdentities)
}

func TestOpenDocumentSessionRecoveryRevalidatesFIFOAfterPreparedInventory(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	published := documentSessionConcept("concept", "after")
	documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
	fixture := documentSessionCreateV2RecoveryState(
		t,
		root,
		"concept.md",
		0o644,
		original,
		published,
		documentV2State(true, false, false, false, false, documentV2TargetWitness),
	)
	artifacts := documentSessionCaptureRecoveryArtifacts(t, root, fixture)
	mutated := false
	hooks := documentSessionHooks{
		publish: documentPublishHooks{
			afterRecoveryInventory: func() error {
				mutated = true
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(target, 0o600); err != nil {
					t.Fatal(err)
				}
				return nil
			},
		},
	}

	// Act.
	session, err := openDocumentSessionContext(context.Background(), target, "", hooks)
	if session != nil {
		_ = session.Close()
	}

	// Assert.
	if !mutated || err == nil || session != nil {
		t.Fatalf("mutated=%t, session=%#v, error=%v; want fail-closed recovery", mutated, session, err)
	}
	info, statErr := os.Lstat(target)
	if statErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO replacement = %v, %v", info, statErr)
	}
	documentSessionAssertRecoveryArtifactsUnchanged(t, root, artifacts)
}

func TestDocumentSessionRewritePreservesSpecialModeBits(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	wantMode := os.FileMode(0o751) | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	if err := os.Chmod(target, wantMode); err != nil {
		t.Fatalf("Chmod(target) error = %v", err)
	}
	before, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat(target) error = %v", err)
	}
	wantMode = publicationPreservedMode(before.Mode())
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if err != nil {
		t.Fatalf("OpenDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	err = session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if err != nil {
		t.Fatalf("RewriteContext() error = %v", err)
	}
	after, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat(published target) error = %v", err)
	}
	if got := publicationPreservedMode(after.Mode()); got != wantMode {
		t.Fatalf("published full mode = %v, want %v", got, wantMode)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestDocumentSessionCleanupNeverUnlinksForeignFIFOReplacement(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	var swapped atomic.Bool
	var foreignArtifact string
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				beforeCleanup: func(_ *os.Root, kind, name string) error {
					if kind != "stage" || !swapped.CompareAndSwap(false, true) {
						return nil
					}
					foreignArtifact = filepath.Join(root, name)
					if err := os.Remove(foreignArtifact); err != nil {
						t.Fatalf("Remove(stage artifact) error = %v", err)
					}
					if err := syscall.Mkfifo(foreignArtifact, 0o600); err != nil {
						t.Fatalf("Mkfifo(stage artifact) error = %v", err)
					}
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	err = session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if err == nil {
		t.Fatal("RewriteContext() succeeded after FIFO replaced stage artifact")
	}
	info, statErr := os.Lstat(foreignArtifact)
	if statErr != nil {
		t.Fatalf("foreign FIFO was unlinked: %v", statErr)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("foreign artifact mode = %v, want FIFO", info.Mode())
	}
}

func TestDocumentSessionCASRejectsFIFOReplacementWithoutOpeningIt(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				beforeCAS: func(*DocumentSession) error {
					if err := os.Remove(target); err != nil {
						t.Fatalf("Remove(target) error = %v", err)
					}
					if err := syscall.Mkfifo(target, 0o600); err != nil {
						t.Fatalf("Mkfifo(target) error = %v", err)
					}
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	err = session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("RewriteContext() error = %v, want ErrDocumentConflict", err)
	}
	info, statErr := os.Lstat(target)
	if statErr != nil {
		t.Fatalf("Lstat(FIFO target) error = %v", statErr)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("replacement mode = %v, want FIFO", info.Mode())
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestGuardedVacateNeverOverwritesForeignFIFOClaimDestination(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	leaf := "concept.md"
	target := filepath.Join(root, leaf)
	original := []byte(documentSessionConcept("concept", "before"))
	if err := os.WriteFile(target, original, 0o640); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := openRootWithoutSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	claim := documentArtifactName("claim", strings.Repeat("a", 64))
	injected := false

	// Act.
	claimed, err := guardedVacatePublicationLeaf(
		context.Background(),
		pinned,
		leaf,
		publicationSpecFromBytes(originalInfo, original),
		claim,
		publicationBarrierHooks{
			beforeVacate: func() error {
				injected = true
				return syscall.Mkfifo(filepath.Join(root, claim), 0o600)
			},
		},
	)

	// Assert.
	if !injected || err == nil || claimed.info != nil {
		t.Fatalf("injected=%t, claimed=%v, error=%v; want no-replace conflict", injected, claimed.info, err)
	}
	current, statErr := os.Lstat(target)
	if statErr != nil || !os.SameFile(originalInfo, current) {
		t.Fatalf("source identity changed: before=%v after=%v error=%v", originalInfo, current, statErr)
	}
	info, statErr := os.Lstat(filepath.Join(root, claim))
	if statErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("foreign FIFO claim = %v, %v", info, statErr)
	}
}

func TestDocumentSessionInstallNeverOverwritesForeignFIFO(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	var swapped atomic.Bool
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				beforeInstall: func(_ *os.Root, _, _ string) error {
					if !swapped.CompareAndSwap(false, true) {
						return nil
					}
					if err := syscall.Mkfifo(target, 0o600); err != nil {
						t.Fatalf("Mkfifo(foreign leaf) error = %v", err)
					}
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	err = session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if err == nil {
		t.Fatal("RewriteContext() overwrote a foreign install-boundary FIFO")
	}
	info, statErr := os.Lstat(target)
	if statErr != nil {
		t.Fatalf("foreign FIFO disappeared: %v", statErr)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("foreign leaf mode = %v, want FIFO", info.Mode())
	}
	if !documentSessionHasArtifacts(t, root) {
		t.Fatal("original recovery evidence was lost after foreign FIFO appeared")
	}
}

func TestPublicationRecoveryRejectsForeignPrivateFIFOWithoutMutation(t *testing.T) {
	t.Parallel()

	entrypoints := []struct {
		name string
		run  func(t *testing.T, root, target string) error
	}{
		{
			name: "session open",
			run: func(t *testing.T, _ string, target string) error {
				t.Helper()
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if session != nil {
					err = errors.Join(err, session.Close())
				}
				return err
			},
		},
		{
			name: "index regeneration",
			run: func(t *testing.T, root, _ string) error {
				t.Helper()
				_, err := regenerateIndexesWithSelectorForTest(root, "")
				return err
			},
		},
	}

	for _, entrypoint := range entrypoints {
		entrypoint := entrypoint
		t.Run(entrypoint.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "body"))
			pinned, err := openRootWithoutSymlinks(root)
			if err != nil {
				t.Fatalf("openRootWithoutSymlinks() error = %v", err)
			}
			privateName, err := allocatePrivatePublicationClaim(
				pinned,
				documentArtifactName("stage", strings.Repeat("a", 64)),
			)
			closeErr := pinned.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("allocate private name = %q, %v; close = %v", privateName, err, closeErr)
			}
			privatePath := filepath.Join(root, privateName)
			if err := syscall.Mkfifo(privatePath, 0o600); err != nil {
				t.Fatalf("Mkfifo(private entry) error = %v", err)
			}
			before, err := os.Lstat(privatePath)
			if err != nil {
				t.Fatalf("Lstat(private FIFO) error = %v", err)
			}

			// Act.
			err = entrypoint.run(t, root, target)

			// Assert.
			if err == nil {
				t.Fatal("publication entrypoint accepted foreign private FIFO")
			}
			after, statErr := os.Lstat(privatePath)
			if statErr != nil {
				t.Fatalf("foreign private FIFO was not retained: %v", statErr)
			}
			if !os.SameFile(before, after) || after.Mode()&os.ModeNamedPipe == 0 {
				t.Fatalf("foreign private FIFO changed: before=%v after=%v", before.Mode(), after.Mode())
			}
		})
	}
}

func TestPublicationRecoveryRejectsExcessPrivateClaimsBeforePayloadCapture(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "body"))
	pinned, err := openRootWithoutSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	var claims []string
	for _, kind := range []string{"stage", "backup"} {
		name, allocateErr := allocatePrivatePublicationClaim(
			pinned,
			documentArtifactName(kind, strings.Repeat("a", 64)),
		)
		if allocateErr != nil {
			_ = pinned.Close()
			t.Fatal(allocateErr)
		}
		claims = append(claims, name)
	}
	if err := pinned.Close(); err != nil {
		t.Fatal(err)
	}
	before := make(map[string]os.FileInfo, len(claims))
	for _, claim := range claims {
		filename := filepath.Join(root, claim)
		if err := syscall.Mkfifo(filename, 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(filename)
		if err != nil {
			t.Fatal(err)
		}
		before[claim] = info
	}

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if session != nil {
		_ = session.Close()
	}

	// Assert.
	if session != nil || err == nil ||
		!strings.Contains(err.Error(), "private publication claim inventory exceeds protocol limit") {
		t.Fatalf("session=%#v, error=%v; want bounded inventory rejection", session, err)
	}
	for claim, expected := range before {
		info, statErr := os.Lstat(filepath.Join(root, claim))
		if statErr != nil || !os.SameFile(expected, info) || info.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("private FIFO %q changed: before=%v after=%v error=%v", claim, expected, info, statErr)
		}
	}
}

func TestIndexRollbackPreservesForeignFIFOAtPostInstallBoundary(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
	documentSessionWriteFile(t, filepath.Join(root, "nested", "note.md"), documentSessionConcept("note", "body"))
	destination := filepath.Join(root, "nested", "index.md")
	documentSessionWriteFile(t, destination, "# Original nested index\n")
	injected := errors.New("injected post-install failure")
	replaced := false
	hooks := indexPublishHooks{
		afterInstallLink: func(relative string, _ *os.Root, _, _ string) error {
			if relative != "nested/index.md" || replaced {
				return nil
			}
			if err := os.Remove(destination); err != nil {
				t.Fatalf("Remove(installed index) error = %v", err)
			}
			if err := syscall.Mkfifo(destination, 0o600); err != nil {
				t.Fatalf("Mkfifo(foreign index leaf) error = %v", err)
			}
			replaced = true
			return injected
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !errors.Is(err, injected) || written != nil || !replaced {
		t.Fatalf("regenerateIndexesWithHooks() = %#v, %v; replaced=%v", written, err, replaced)
	}
	info, statErr := os.Lstat(destination)
	if statErr != nil {
		t.Fatalf("foreign FIFO disappeared: %v", statErr)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("foreign leaf mode = %v, want FIFO", info.Mode())
	}
	if backups := indexRecoveryBackups(t, root); len(backups) == 0 {
		t.Fatal("original recovery evidence was not retained")
	}
}
