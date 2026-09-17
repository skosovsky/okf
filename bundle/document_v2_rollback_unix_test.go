//go:build darwin || linux

package bundle

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentV2ActiveRollbackCrashSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_DOCUMENT_V2_ROLLBACK_ROOT")
	point := os.Getenv("OKF_DOCUMENT_V2_ROLLBACK_POINT")
	if root == "" || point == "" {
		return
	}
	crash := func() error {
		os.Exit(88)
		return nil
	}
	forwardFailed := false
	discardRenamed := false
	restoreRenamed := false
	hooks := documentPublishHooks{
		afterAnchorSync: func(*os.Root, string) error {
			if point == "anchor-durable" {
				return crash()
			}
			return nil
		},
		afterRestoreSync: func(*os.Root, string) error {
			if point == "restore-durable" {
				return crash()
			}
			return nil
		},
		afterVacateRename: func(*os.Root, string, string) error {
			if !forwardFailed {
				return nil
			}
			discardRenamed = true
			if point == "discard-post-rename" {
				return crash()
			}
			return nil
		},
		afterDiscardSync: func(*os.Root, string) error {
			if point == "discard-post-sync" {
				return crash()
			}
			return nil
		},
		afterInstall: func(*os.Root, string) error {
			if !forwardFailed {
				forwardFailed = true
				return errors.New("enter active rollback")
			}
			restoreRenamed = true
			if point == "restore-post-rename" {
				return crash()
			}
			return nil
		},
		beforeInstall: func(*os.Root, string, string) error {
			if point == "cleanup-n" && !forwardFailed {
				forwardFailed = true
				return errors.New("enter active rollback before forward install")
			}
			return nil
		},
		afterRestoreConsumed: func(*os.Root, string) error {
			if point == "restore-post-sync" {
				return crash()
			}
			return nil
		},
		afterCleanup: func(_ *os.Root, kind, _ string) error {
			want := map[string]string{
				"cleanup-d": "discard",
				"cleanup-n": "new-install",
				"cleanup-s": "stage",
				"cleanup-a": "anchor",
			}[point]
			if want != "" && kind == want {
				return crash()
			}
			return nil
		},
		directorySync: func(parent *os.Root) error {
			if discardRenamed && point == "discard-pre-sync" {
				return crash()
			}
			if restoreRenamed && point == "restore-pre-sync" {
				return crash()
			}
			return syncPublicationDirectoryPlatform(parent)
		},
	}
	target := filepath.Join(root, "concept.md")
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
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))
	if err := session.RewriteContext(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentV2ActiveRollbackDurableCrashCuts(t *testing.T) {
	t.Parallel()

	points := []string{
		"anchor-durable",
		"restore-durable",
		"discard-post-rename",
		"discard-pre-sync",
		"discard-post-sync",
		"restore-post-rename",
		"restore-pre-sync",
		"restore-post-sync",
	}
	for _, point := range points {
		point := point
		t.Run(point, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))

			// Act.
			runDocumentV2RollbackCrashHelper(t, root, point)

			// Assert.
			artifacts := documentPrepareCrashArtifacts(t, root)
			for _, kind := range []string{"manifest", "witness", "backup", "stage", "claim", "anchor-0"} {
				if artifacts[kind] == "" {
					t.Fatalf("cut %s lacks %s: %#v", point, kind, artifacts)
				}
			}
			anchor := documentV2ForwardStat(t, artifacts["anchor-0"])
			stage := documentV2ForwardStat(t, artifacts["stage"])
			switch {
			case point == "anchor-durable":
				if artifacts["restore-install"] != "" || artifacts["discard"] != "" {
					t.Fatalf("anchor cut has future tokens: %#v", artifacts)
				}
				targetInfo := documentV2ForwardStat(t, target)
				if !os.SameFile(targetInfo, stage) {
					t.Fatal("anchor cut target is not retained S")
				}
			case point == "restore-durable":
				restore := documentV2ForwardStat(t, artifacts["restore-install"])
				if !os.SameFile(restore, anchor) || artifacts["discard"] != "" {
					t.Fatalf("restore cut lacks exact R~A: %#v", artifacts)
				}
				targetInfo := documentV2ForwardStat(t, target)
				if !os.SameFile(targetInfo, stage) {
					t.Fatal("restore cut target is not retained S")
				}
			case strings.HasPrefix(point, "discard-"):
				restore := documentV2ForwardStat(t, artifacts["restore-install"])
				discard := documentV2ForwardStat(t, artifacts["discard"])
				if !os.SameFile(restore, anchor) || !os.SameFile(discard, stage) {
					t.Fatal("discard cut lost R~A or D~S")
				}
				if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("discard cut target error = %v, want missing", err)
				}
			case strings.HasPrefix(point, "restore-post-") ||
				strings.HasPrefix(point, "restore-pre-"):
				if artifacts["restore-install"] != "" {
					t.Fatalf("restore consume cut retained R: %#v", artifacts)
				}
				discard := documentV2ForwardStat(t, artifacts["discard"])
				targetInfo := documentV2ForwardStat(t, target)
				if !os.SameFile(discard, stage) || !os.SameFile(targetInfo, anchor) {
					t.Fatal("restore consume cut lost target~A or D~S")
				}
			}
		})
	}
}

func TestDocumentV2ActiveRollbackCleanupProofLastCrashCuts(t *testing.T) {
	t.Parallel()

	points := []string{"cleanup-d", "cleanup-n", "cleanup-s", "cleanup-a"}
	for _, point := range points {
		point := point
		t.Run(point, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			documentSessionWriteFile(t, target, original)

			// Act.
			runDocumentV2RollbackCrashHelper(t, root, point)

			// Assert.
			artifacts := documentPrepareCrashArtifacts(t, root)
			if artifacts["manifest"] != "" || artifacts["restore-install"] != "" {
				t.Fatalf("post-M cleanup cut retained M or R: %#v", artifacts)
			}
			if point != "cleanup-a" {
				if artifacts["anchor-0"] == "" {
					t.Fatalf("%s removed proof A before cleanup completed: %#v", point, artifacts)
				}
				targetInfo := documentV2ForwardStat(t, target)
				anchorInfo := documentV2ForwardStat(t, artifacts["anchor-0"])
				if !os.SameFile(targetInfo, anchorInfo) {
					t.Fatalf("%s lost target~A proof", point)
				}
			}
			switch point {
			case "cleanup-d":
				if artifacts["discard"] != "" || artifacts["new-install"] != "" {
					t.Fatalf("D cut retained removed D or consumed N: %#v", artifacts)
				}
				if artifacts["stage"] == "" {
					t.Fatalf("D cut removed S before its cleanup boundary: %#v", artifacts)
				}
			case "cleanup-n":
				if artifacts["new-install"] != "" || artifacts["discard"] != "" {
					t.Fatalf("N cut retained removed N or invented D: %#v", artifacts)
				}
				if artifacts["stage"] == "" {
					t.Fatalf("N cut removed S before its cleanup boundary: %#v", artifacts)
				}
			case "cleanup-s":
				if artifacts["discard"] != "" ||
					artifacts["new-install"] != "" ||
					artifacts["stage"] != "" {
					t.Fatalf("S cut retained D/N/S after their cleanup boundaries: %#v", artifacts)
				}
			case "cleanup-a":
				if len(artifacts) != 0 {
					t.Fatalf("A cut retained transaction residue: %#v", artifacts)
				}
			}
			if documentSessionReadFile(t, target) != original {
				t.Fatalf("%s changed restored public bytes", point)
			}
		})
	}
}

func TestDocumentV2ActiveRollbackVacateMismatchCompensation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		inPlace bool
		blocked bool
	}{
		{name: "foreign D is hard"},
		{name: "in-place D payload drift is hard", inPlace: true},
		{name: "foreign D with target blocker", blocked: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
			forwardFault := errors.New("enter active rollback")
			foreign := []byte("foreign discard\n")
			blocker := []byte("foreign target blocker\n")
			forwardFailed := false
			injected := false
			var discardPath string
			var foreignInfo, blockerInfo os.FileInfo
			var attackedTree map[string]documentSessionTreeEntry
			var attackedIdentities map[string]os.FileInfo
			hooks := documentPublishHooks{
				afterInstall: func(*os.Root, string) error {
					if forwardFailed {
						return nil
					}
					forwardFailed = true
					return forwardFault
				},
				afterVacateRename: func(_ *os.Root, _, discard string) error {
					if !forwardFailed || injected {
						return nil
					}
					injected = true
					discardPath = filepath.Join(root, discard)
					if test.inPlace {
						file, err := os.OpenFile(discardPath, os.O_RDWR, 0)
						if err != nil {
							return err
						}
						_, writeErr := file.WriteAt([]byte("X"), 0)
						syncErr := file.Sync()
						closeErr := file.Close()
						if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
							return err
						}
					} else {
						if err := os.Remove(discardPath); err != nil {
							return err
						}
						if err := os.WriteFile(discardPath, foreign, 0o640); err != nil {
							return err
						}
					}
					var err error
					foreignInfo, err = os.Lstat(discardPath)
					if err != nil {
						return err
					}
					if test.blocked {
						if err := os.WriteFile(target, blocker, 0o600); err != nil {
							return err
						}
						blockerInfo, err = os.Lstat(target)
						if err != nil {
							return err
						}
					}
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
			t.Cleanup(func() { _ = session.Close() })

			// Act.
			err = session.RewriteContext(
				context.Background(),
				documentSessionParsed(t, documentSessionConcept("concept", "after")),
			)

			// Assert.
			if !injected ||
				!errors.Is(err, forwardFault) ||
				!errors.Is(err, ErrDocumentConflict) {
				t.Fatalf("injected=%t error=%v, want hard D rollback conflict", injected, err)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				attackedTree,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, attackedIdentities)
			if test.blocked {
				gotTarget := documentV2ForwardStat(t, target)
				gotDiscard := documentV2ForwardStat(t, discardPath)
				if blockerInfo == nil ||
					foreignInfo == nil ||
					!os.SameFile(gotTarget, blockerInfo) ||
					!os.SameFile(gotDiscard, foreignInfo) ||
					!bytes.Equal([]byte(documentSessionReadFile(t, target)), blocker) ||
					!bytes.Equal([]byte(documentSessionReadFile(t, discardPath)), foreign) {
					t.Fatal("blocked compensation changed foreign target or discard identity")
				}
			} else if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("hard D conflict changed missing target: %v", statErr)
			}
			gotDiscard := documentV2ForwardStat(t, discardPath)
			if foreignInfo == nil || !os.SameFile(gotDiscard, foreignInfo) {
				t.Fatal("hard D conflict changed attacked discard identity")
			}
			if !test.inPlace &&
				!bytes.Equal([]byte(documentSessionReadFile(t, discardPath)), foreign) {
				t.Fatal("hard D conflict changed foreign discard bytes")
			}
			if closeErr := session.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}

			// Restart must fail on the same hard D evidence without mutation.
			restarted, restartErr := OpenDocumentSessionContext(
				context.Background(),
				target,
				"",
			)
			if restarted != nil {
				_ = restarted.Close()
			}
			if restarted != nil || !errors.Is(restartErr, ErrDocumentConflict) {
				t.Fatalf(
					"restart session=%#v error=%v, want typed hard D conflict",
					restarted,
					restartErr,
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

func TestDocumentV2ActiveRollbackRejectsChangedTargetBeforeDiscard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		missing bool
	}{
		{name: "missing", missing: true},
		{name: "foreign"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
			forwardFault := errors.New("enter active rollback")
			foreign := documentSessionConcept("foreign", "preserve")
			injected := false
			var foreignInfo os.FileInfo
			var injectedTree map[string]documentSessionTreeEntry
			var injectedIdentities map[string]os.FileInfo
			hooks := documentPublishHooks{
				afterInstall: func(*os.Root, string) error {
					if injected {
						return nil
					}
					injected = true
					if test.missing {
						if err := os.Remove(target); err != nil {
							return err
						}
					} else {
						documentSessionAtomicReplace(t, target, foreign, 0o640)
						var err error
						foreignInfo, err = os.Lstat(target)
						if err != nil {
							return err
						}
						injectedTree = documentSessionCaptureTree(t, root)
						injectedIdentities = documentPublicationIdentities(t, root)
					}
					return forwardFault
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
			t.Cleanup(func() { _ = session.Close() })

			// Act.
			err = session.RewriteContext(
				context.Background(),
				documentSessionParsed(t, documentSessionConcept("concept", "after")),
			)

			// Assert.
			if !injected ||
				!errors.Is(err, forwardFault) ||
				!errors.Is(err, ErrDocumentConflict) {
				t.Fatalf("injected=%t error=%v, want pre-discard conflict", injected, err)
			}
			artifacts := documentPrepareCrashArtifacts(t, root)
			for _, kind := range []string{"manifest", "stage"} {
				if artifacts[kind] == "" {
					t.Fatalf("pre-discard conflict lacks %s: %#v", kind, artifacts)
				}
			}
			if artifacts["discard"] != "" {
				t.Fatalf("pre-discard conflict created D: %#v", artifacts)
			}
			if test.missing {
				if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("missing target error = %v, want missing", statErr)
				}
				return
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				injectedTree,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, injectedIdentities)
			got := documentV2ForwardStat(t, target)
			if foreignInfo == nil ||
				!os.SameFile(got, foreignInfo) ||
				documentSessionReadFile(t, target) != foreign {
				t.Fatal("pre-discard conflict changed exact foreign target")
			}
		})
	}
}

func TestDocumentV2ActiveRollbackManifestRemovalFailureRetainsProof(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	forwardFault := errors.New("enter active rollback")
	manifestFault := errors.New("retain rollback manifest")
	forwardFailed := false
	hooks := documentPublishHooks{
		afterInstall: func(*os.Root, string) error {
			if forwardFailed {
				return nil
			}
			forwardFailed = true
			return forwardFault
		},
		beforeCleanup: func(_ *os.Root, kind, _ string) error {
			if kind == "manifest" {
				return manifestFault
			}
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
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	err = session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !errors.Is(err, forwardFault) || !errors.Is(err, manifestFault) {
		t.Fatalf("RewriteContext() error = %v, want joined forward and manifest failures", err)
	}
	artifacts := documentPrepareCrashArtifacts(t, root)
	for _, kind := range []string{"manifest", "stage", "discard", "anchor-0"} {
		if artifacts[kind] == "" {
			t.Fatalf("M removal failure lacks %s: %#v", kind, artifacts)
		}
	}
	for _, kind := range []string{"new-install", "restore-install"} {
		if artifacts[kind] != "" {
			t.Fatalf("M removal failure retained consumed %s: %#v", kind, artifacts)
		}
	}
	targetInfo := documentV2ForwardStat(t, target)
	anchorInfo := documentV2ForwardStat(t, artifacts["anchor-0"])
	stageInfo := documentV2ForwardStat(t, artifacts["stage"])
	discardInfo := documentV2ForwardStat(t, artifacts["discard"])
	if !os.SameFile(targetInfo, anchorInfo) ||
		!os.SameFile(stageInfo, discardInfo) ||
		documentSessionReadFile(t, target) != original {
		t.Fatal("M removal failure lost target~A or D~S proof")
	}
}

func runDocumentV2RollbackCrashHelper(t *testing.T, root, point string) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestDocumentV2ActiveRollbackCrashSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_DOCUMENT_V2_ROLLBACK_ROOT="+root,
		"OKF_DOCUMENT_V2_ROLLBACK_POINT="+point,
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err == nil ||
		command.ProcessState == nil ||
		command.ProcessState.ExitCode() != 88 {
		t.Fatalf(
			"rollback helper exit=%v error=%v at %s, want 88\n%s",
			command.ProcessState,
			err,
			point,
			output.String(),
		)
	}
}
