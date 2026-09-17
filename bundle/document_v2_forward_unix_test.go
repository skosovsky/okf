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

func TestDocumentV2ForwardCrashSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_DOCUMENT_V2_FORWARD_ROOT")
	point := os.Getenv("OKF_DOCUMENT_V2_FORWARD_POINT")
	if root == "" || point == "" {
		return
	}
	crash := func() error {
		os.Exit(87)
		return nil
	}
	hooks := documentPublishHooks{
		afterStageSync: func(*os.Root, string) error {
			if point == "prepare-pre-n" {
				return crash()
			}
			return nil
		},
		afterNewInstallSync: func(*os.Root, string) error {
			if point == "prepare-post-n" {
				return crash()
			}
			return nil
		},
		beforeInstall: func(*os.Root, string, string) error {
			if point == "forward-pre-rename" {
				return crash()
			}
			return nil
		},
		afterInstall: func(*os.Root, string) error {
			if point == "forward-post-rename" {
				return crash()
			}
			return nil
		},
	}
	if point == "forward-post-sync" {
		hooks.directorySync = func(parent *os.Root) error {
			if err := syncPublicationDirectoryPlatform(parent); err != nil {
				return err
			}
			if documentV2ForwardInstalledState(parent, "concept.md") {
				return crash()
			}
			return nil
		}
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

func TestDocumentV2ForwardCrashCutsRetainExactOwnershipState(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		point         string
		targetMissing bool
		newPresent    bool
	}{
		{point: "forward-pre-rename", targetMissing: true, newPresent: true},
		{point: "forward-post-rename"},
		{point: "forward-post-sync"},
	} {
		testCase := testCase
		t.Run(testCase.point, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			documentSessionWriteFile(t, target, original)

			// Act.
			runDocumentV2ForwardCrashHelper(t, root, testCase.point)

			// Assert.
			artifacts := documentPrepareCrashArtifacts(t, root)
			for _, kind := range []string{"manifest", "witness", "backup", "stage", "claim"} {
				if artifacts[kind] == "" {
					t.Fatalf("crash cut %s lacks %s: %#v", testCase.point, kind, artifacts)
				}
			}
			_, newErr := os.Lstat(artifacts["new-install"])
			if testCase.newPresent && newErr != nil {
				t.Fatalf("Lstat(N) error = %v, want retained", newErr)
			}
			if !testCase.newPresent && !errors.Is(newErr, fs.ErrNotExist) {
				t.Fatalf("Lstat(N) error = %v, want consumed", newErr)
			}
			stageInfo := documentV2ForwardStat(t, artifacts["stage"])
			if testCase.newPresent {
				newInfo := documentV2ForwardStat(t, artifacts["new-install"])
				if !os.SameFile(stageInfo, newInfo) {
					t.Fatal("pre-rename N is detached from S")
				}
			}
			witnessInfo := documentV2ForwardStat(t, artifacts["witness"])
			claimInfo := documentV2ForwardStat(t, artifacts["claim"])
			if !os.SameFile(witnessInfo, claimInfo) {
				t.Fatal("forward C is detached from W")
			}
			targetInfo, targetErr := os.Lstat(target)
			if testCase.targetMissing {
				if !errors.Is(targetErr, fs.ErrNotExist) {
					t.Fatalf("Lstat(target) error = %v, want missing", targetErr)
				}
			} else if targetErr != nil || !os.SameFile(targetInfo, stageInfo) {
				t.Fatalf("target is not exact S: info=%v error=%v", targetInfo, targetErr)
			}
		})
	}
}

func TestDocumentV2PreManifestNewTokenCleanupNeverMutatesTarget(t *testing.T) {
	t.Parallel()

	for _, point := range []string{"prepare-pre-n", "prepare-post-n"} {
		point := point
		t.Run(point, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			documentSessionWriteFile(t, target, original)
			before := documentV2ForwardStat(t, target)
			runDocumentV2ForwardCrashHelper(t, root, point)

			// Act.
			session, err := OpenDocumentSessionContext(context.Background(), target, "")
			if session != nil {
				_ = session.Close()
			}

			// Assert.
			if err != nil || session == nil {
				t.Fatalf("OpenDocumentSessionContext() session=%#v error=%v", session, err)
			}
			after := documentV2ForwardStat(t, target)
			if !os.SameFile(before, after) ||
				documentSessionReadFile(t, target) != original {
				t.Fatal("M-absent cleanup mutated the exact original target")
			}
			documentSessionAssertNoArtifacts(t, root)
		})
	}
}

func TestDocumentV2ForwardForeignTargetIsNeverOverwritten(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	foreign := []byte("foreign target\n")
	var foreignInfo os.FileInfo
	injected := false
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: documentPublishHooks{
			beforeInstall: func(*os.Root, string, string) error {
				if injected {
					return nil
				}
				injected = true
				file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
				if err != nil {
					return err
				}
				if _, err := file.Write(foreign); err != nil {
					_ = file.Close()
					return err
				}
				if err := file.Sync(); err != nil {
					_ = file.Close()
					return err
				}
				if err := file.Close(); err != nil {
					return err
				}
				foreignInfo, err = os.Lstat(target)
				return err
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))

	// Act.
	err = session.RewriteContext(context.Background(), replacement)

	// Assert.
	if !errors.Is(err, ErrDocumentConflict) || !injected {
		t.Fatalf("RewriteContext() error = %v injected=%t, want conflict", err, injected)
	}
	after := documentV2ForwardStat(t, target)
	if foreignInfo == nil ||
		!os.SameFile(foreignInfo, after) ||
		!bytes.Equal([]byte(documentSessionReadFile(t, target)), foreign) {
		t.Fatal("atomic no-replace changed the exact foreign target")
	}
	artifacts := documentPrepareCrashArtifacts(t, root)
	stage := documentV2ForwardStat(t, artifacts["stage"])
	newInstall := documentV2ForwardStat(t, artifacts["new-install"])
	if !os.SameFile(stage, newInstall) {
		t.Fatal("foreign conflict did not retain exact N~S evidence")
	}
}

func TestDocumentV2ManifestRemovalFailureRetainsPublishedProof(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	retainManifest := errors.New("retain manifest")
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: documentPublishHooks{
			beforeCleanup: func(*os.Root, string, string) error {
				return retainManifest
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))

	// Act.
	err = session.RewriteContext(context.Background(), replacement)

	// Assert.
	if !errors.Is(err, ErrPublicationCommitted) ||
		!errors.Is(err, retainManifest) {
		t.Fatalf("RewriteContext() error = %v, want committed manifest failure", err)
	}
	artifacts := documentPrepareCrashArtifacts(t, root)
	for _, kind := range []string{"manifest", "witness", "backup", "stage", "claim"} {
		if artifacts[kind] == "" {
			t.Fatalf("M failure lacks %s proof: %#v", kind, artifacts)
		}
	}
	if artifacts["new-install"] != "" {
		t.Fatal("M failure resurrected consumed N")
	}
	targetInfo := documentV2ForwardStat(t, target)
	stageInfo := documentV2ForwardStat(t, artifacts["stage"])
	if !os.SameFile(targetInfo, stageInfo) {
		t.Fatal("M failure did not retain target~S proof")
	}
}

func TestDocumentV2DetachedNewCleanupRejectsAmbiguousStateWithoutMutation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*testing.T, string, string, map[string]string)
	}{
		{
			name: "target missing",
			mutate: func(t *testing.T, _, target string, _ map[string]string) {
				t.Helper()
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "target foreign different inode",
			mutate: func(t *testing.T, _, target string, _ map[string]string) {
				t.Helper()
				documentSessionAtomicReplace(
					t,
					target,
					documentSessionConcept("concept", "before"),
					0o644,
				)
			},
		},
		{
			name: "target new generation",
			mutate: func(t *testing.T, _, target string, artifacts map[string]string) {
				t.Helper()
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(artifacts["stage"], target); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "new token swapped inode",
			mutate: func(t *testing.T, _, _ string, artifacts map[string]string) {
				t.Helper()
				data, err := os.ReadFile(artifacts["stage"])
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(artifacts["new-install"]); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(artifacts["new-install"], data, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "new token non regular",
			mutate: func(t *testing.T, root, _ string, artifacts map[string]string) {
				t.Helper()
				if err := os.Remove(artifacts["new-install"]); err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(root, "external")
				if err := os.WriteFile(external, []byte("external\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, artifacts["new-install"]); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "new token mode drift",
			mutate: func(t *testing.T, _, _ string, artifacts map[string]string) {
				t.Helper()
				if err := os.Chmod(artifacts["new-install"], 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "new token content drift",
			mutate: func(t *testing.T, _, _ string, artifacts map[string]string) {
				t.Helper()
				if err := os.WriteFile(
					artifacts["new-install"],
					[]byte("tampered new generation\n"),
					0o644,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unresolved claim without manifest",
			mutate: func(t *testing.T, root, target string, _ map[string]string) {
				t.Helper()
				claim := filepath.Join(
					root,
					documentArtifactName("claim", strings.Repeat("a", 64)),
				)
				if err := os.Link(target, claim); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
			runDocumentV2ForwardCrashHelper(t, root, "prepare-post-n")
			artifacts := documentPrepareCrashArtifacts(t, root)
			testCase.mutate(t, root, target, artifacts)
			beforeTree := documentSessionCaptureTree(t, root)
			beforeIdentities := documentPublicationIdentities(t, root)

			// Act.
			session, err := OpenDocumentSessionContext(context.Background(), target, "")
			if session != nil {
				_ = session.Close()
			}

			// Assert.
			if session != nil || err == nil {
				t.Fatalf(
					"OpenDocumentSessionContext() session=%#v error=%v, want hard error",
					session,
					err,
				)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				beforeTree,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, beforeIdentities)
		})
	}
}

func TestDocumentV2DetachedNewCleanupCrashOrderAndRestartConvergence(t *testing.T) {
	t.Parallel()

	expectedAfterCut := [][]string{
		{"stage", "backup", "witness"},
		{"backup", "witness"},
		{"witness"},
		nil,
	}
	for ordinal, expectedKinds := range expectedAfterCut {
		ordinal, expectedKinds := ordinal, expectedKinds
		t.Run(string(rune('0'+ordinal)), func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			documentSessionWriteFile(t, target, original)
			targetBefore := documentV2ForwardStat(t, target)
			runDocumentV2ForwardCrashHelper(t, root, "prepare-post-n")

			// Act.
			runDocumentPrepareCrashHelper(
				t,
				root,
				"recovery-cleanup-"+string(rune('0'+ordinal)),
			)

			// Assert crash prefix.
			artifacts := documentPrepareCrashArtifacts(t, root)
			if len(artifacts) != len(expectedKinds) {
				t.Fatalf(
					"cleanup cut %d artifacts=%#v, want %v",
					ordinal,
					artifacts,
					expectedKinds,
				)
			}
			for _, kind := range expectedKinds {
				if artifacts[kind] == "" {
					t.Fatalf("cleanup cut %d lacks %s: %#v", ordinal, kind, artifacts)
				}
			}
			if witness := artifacts["witness"]; witness != "" {
				if !os.SameFile(
					documentV2ForwardStat(t, target),
					documentV2ForwardStat(t, witness),
				) {
					t.Fatalf("cleanup cut %d lost proof-last target~W", ordinal)
				}
			}

			// Act restart.
			session, err := OpenDocumentSessionContext(context.Background(), target, "")
			if session != nil {
				_ = session.Close()
			}

			// Assert convergence.
			if err != nil || session == nil {
				t.Fatalf("restart session=%#v error=%v", session, err)
			}
			targetAfter := documentV2ForwardStat(t, target)
			if !os.SameFile(targetBefore, targetAfter) ||
				documentSessionReadFile(t, target) != original {
				t.Fatalf("cleanup cut %d restart mutated target", ordinal)
			}
			documentSessionAssertNoArtifacts(t, root)
		})
	}
}

func runDocumentV2ForwardCrashHelper(t *testing.T, root, point string) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestDocumentV2ForwardCrashSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_DOCUMENT_V2_FORWARD_ROOT="+root,
		"OKF_DOCUMENT_V2_FORWARD_POINT="+point,
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err == nil ||
		command.ProcessState == nil ||
		command.ProcessState.ExitCode() != 87 {
		t.Fatalf(
			"document v2 forward helper exit=%v error=%v at %s, want 87\n%s",
			command.ProcessState,
			err,
			point,
			output.String(),
		)
	}
}

func documentV2ForwardInstalledState(parent *os.Root, leaf string) bool {
	stageName, err := documentV2ForwardArtifactName(parent, "stage")
	if err != nil {
		return false
	}
	if _, err := documentV2ForwardArtifactName(parent, "claim"); err != nil {
		return false
	}
	if _, err := documentV2ForwardArtifactName(parent, "manifest"); err != nil {
		return false
	}
	if _, err := documentV2ForwardArtifactName(parent, "new-install"); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	stage, err := parent.Lstat(stageName)
	if err != nil {
		return false
	}
	target, err := parent.Lstat(leaf)
	return err == nil && os.SameFile(stage, target)
}

func documentV2ForwardStat(t *testing.T, name string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(name)
	if err != nil {
		t.Fatalf("Lstat(%q) error = %v", name, err)
	}
	return info
}
