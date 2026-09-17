//go:build darwin || linux

package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexV3RollbackManifestCleanupBoundary(t *testing.T) {
	t.Parallel()

	t.Run("manifest removal failure retains complete proof", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		arrangeIndexBatchCrashBundle(t, root)
		force := errors.New("force rollback")
		manifestFailure := errors.New("retain manifest")
		hooks := indexPublishHooks{
			beforeRootCommit: func(string, *os.Root, string) error {
				return force
			},
			beforeCleanup: func(_ string, _ *os.Root, kind, _ string) error {
				if kind == "manifest" {
					return manifestFailure
				}
				return nil
			},
		}

		// Act.
		_, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

		// Assert.
		if !errors.Is(err, force) || !errors.Is(err, manifestFailure) {
			t.Fatalf("regenerateIndexesWithHooks() error = %v, want manifest cleanup failure", err)
		}
		manifest := readIndexBatchManifestFixture(t, root)
		entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
		anchor := indexRollbackStat(t, root, entry.Anchor)
		assertIndexRollbackAliasState(t, root, entry.Stage, nil, false)
		assertIndexRollbackAliasState(t, root, entry.Discard, nil, false)
		assertIndexRollbackAliasState(t, root, entry.RestoreInstall, anchor, false)
		target := indexRollbackStat(t, root, entry.Path)
		if !os.SameFile(anchor, target) {
			t.Fatal("M failure did not retain exact target~A proof")
		}
	})

	t.Run("manifest follows non-proof cleanup and precedes exact proofs", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		arrangeIndexBatchCrashBundle(t, root)
		force := errors.New("force rollback")
		cut := errors.New("cut after discard cleanup")
		var entry indexBatchManifestEntry
		manifestRemoved := false
		hooks := indexPublishHooks{
			afterBatchManifestDurable: func(batchRoot *os.Root, name string) error {
				data, err := batchRoot.ReadFile(name)
				if err != nil {
					return err
				}
				var manifest indexBatchManifest
				if err := json.Unmarshal(data, &manifest); err != nil {
					return err
				}
				entry = indexBatchManifestEntryByPath(t, manifest, "a/index.md")
				return nil
			},
			beforeRootCommit: func(string, *os.Root, string) error {
				return force
			},
			afterCleanup: func(relative string, _ *os.Root, kind, _ string) error {
				if kind == "manifest" {
					manifestRemoved = true
					assertIndexRollbackAliasState(t, root, entry.Discard, nil, false)
					assertIndexRollbackAliasState(t, root, entry.Stage, nil, false)
					indexRollbackStat(t, root, entry.Anchor)
					return cut
				}
				return nil
			},
		}

		// Act.
		_, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

		// Assert.
		if !errors.Is(err, force) || !errors.Is(err, cut) {
			t.Fatalf("regenerateIndexesWithHooks() error = %v, want post-M proof cut", err)
		}
		if !manifestRemoved {
			t.Fatal("manifest boundary was not crossed")
		}
		indexRollbackStat(t, root, entry.Anchor)
		if names := indexBatchManifestFixtureNames(t, root); len(names) != 0 {
			t.Fatalf("manifest survived after-M cut: %#v", names)
		}
	})
}

func TestIndexV3ActiveCreateOnlyRollback(t *testing.T) {
	t.Parallel()

	t.Run("durable discard cut has no old install evidence", func(t *testing.T) {
		// Arrange.
		root := newIndexV3CreateOnlyRollbackBundle(t)
		force := errors.New("force create-only rollback")
		cut := errors.New("cut after create-only discard sync")
		hooks := indexPublishHooks{
			beforeRootCommit: func(string, *os.Root, string) error {
				return force
			},
			afterVacate: func(relative string, _ *os.Root, _, claim string) error {
				if relative == "a/index.md" &&
					filepath.Base(claim) != "" &&
					len(filepath.Base(claim)) >= len(indexDiscardPrefix) &&
					filepath.Base(claim)[:len(indexDiscardPrefix)] == indexDiscardPrefix {
					return cut
				}
				return nil
			},
		}

		// Act.
		written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

		// Assert.
		if written != nil || !errors.Is(err, force) || !errors.Is(err, cut) {
			t.Fatalf("regenerateIndexesWithHooks() = %#v, %v; want rollback cut", written, err)
		}
		manifest := readIndexBatchManifestFixture(t, root)
		entry := indexBatchManifestEntryByPath(t, manifest, "a/index.md")
		if entry.OldPresent || entry.Anchor != "" {
			t.Fatalf("create-only manifest binds old evidence: %+v", entry)
		}
		stage := indexRollbackStat(t, root, entry.Stage)
		assertIndexRollbackAliasState(t, root, entry.Discard, stage, true)
		assertIndexRollbackAliasState(t, root, entry.RestoreInstall, stage, false)
		if _, err := os.Lstat(filepath.Join(root, "a", indexFilename)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Lstat(create-only target) error = %v, want absent", err)
		}
	})

	t.Run("successful rollback proves absence and cleans after manifest", func(t *testing.T) {
		// Arrange.
		root := newIndexV3CreateOnlyRollbackBundle(t)
		force := errors.New("force create-only rollback")
		hooks := indexPublishHooks{
			beforeRootCommit: func(string, *os.Root, string) error {
				return force
			},
		}

		// Act.
		written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

		// Assert.
		if written != nil || !errors.Is(err, force) {
			t.Fatalf("regenerateIndexesWithHooks() = %#v, %v; want rollback", written, err)
		}
		if _, err := os.Lstat(filepath.Join(root, "a", indexFilename)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Lstat(create-only target) error = %v, want absent", err)
		}
		assertNoIndexTransactionArtifacts(t, root)
	})
}

func TestIndexV3DiscardMismatchCompensation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		blocked bool
	}{
		{name: "retains foreign discard when target is missing"},
		{name: "retains foreign discard and occupant", blocked: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			parent, stage, expected := newIndexV3DiscardFixture(t)
			var foreignDiscard os.FileInfo
			var occupant os.FileInfo
			hooks := publicationBarrierHooks{
				afterVacateRename: func() error {
					if err := parent.Remove("discard"); err != nil {
						return err
					}
					var err error
					foreignDiscard, err = createIndexRollbackForeign(
						parent,
						"discard",
						[]byte("foreign discard\n"),
					)
					if err != nil {
						return err
					}
					if testCase.blocked {
						occupant, err = createIndexRollbackForeign(
							parent,
							"index.md",
							[]byte("foreign occupant\n"),
						)
					}
					return err
				},
			}

			// Act.
			claimed, err := guardedVacatePublicationLeafWithCompensation(
				context.Background(),
				parent,
				"index.md",
				expected,
				"discard",
				hooks,
			)

			// Assert.
			if !errors.Is(err, errPublicationConflict) {
				t.Fatalf(
					"guardedVacatePublicationLeafWithCompensation() error = %v, want hard conflict",
					err,
				)
			}
			retainedStage, statErr := parent.Lstat("stage")
			if statErr != nil || !os.SameFile(stage.info, retainedStage) {
				t.Fatalf("retained S changed: info=%v error=%v", retainedStage, statErr)
			}
			target, targetErr := parent.Lstat("index.md")
			discard, discardErr := parent.Lstat("discard")
			if !claimed.created {
				t.Fatal("claimed.created = false, want retained drifted D evidence")
			}
			if discardErr != nil || !os.SameFile(foreignDiscard, discard) {
				t.Fatalf("foreign D changed: info=%v error=%v", discard, discardErr)
			}
			if testCase.blocked {
				if targetErr != nil || !os.SameFile(occupant, target) {
					t.Fatalf("compensation occupant changed: info=%v error=%v", target, targetErr)
				}
				return
			}
			if !errors.Is(targetErr, fs.ErrNotExist) {
				t.Fatalf("Lstat(index.md) error = %v, want missing fail-closed target", targetErr)
			}
		})
	}
}

func TestIndexV3ActiveExistingRollbackStateCuts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		point      string
		wantR      bool
		wantD      bool
		targetRole string
	}{
		{name: "anchor durable", point: "rollback-anchor-durable", targetRole: "new"},
		{name: "restore install durable", point: "rollback-restore-install-durable", wantR: true, targetRole: "new"},
		{name: "discard post rename", point: "rollback-discard-post-rename", wantR: true, wantD: true, targetRole: "missing"},
		{name: "discard post sync", point: "rollback-discard-post-sync", wantR: true, wantD: true, targetRole: "missing"},
		{name: "restore install post rename", point: "rollback-restore-install-post-rename", wantD: true, targetRole: "old"},
		{name: "restore install post sync", point: "rollback-restore-install-post-sync", wantD: true, targetRole: "old"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			arrangeIndexBatchCrashBundle(t, root)

			// Act.
			runIndexBatchCrashHelper(t, root, testCase.point)

			// Assert.
			manifest := readIndexBatchManifestFixture(t, root)
			entry := indexBatchManifestEntryByPath(t, manifest, "b/index.md")
			stage := indexRollbackStat(t, root, entry.Stage)
			anchor := indexRollbackStat(t, root, entry.Anchor)
			if os.SameFile(stage, anchor) {
				t.Fatal("independent A aliases retained S")
			}
			assertIndexRollbackAliasState(t, root, entry.RestoreInstall, anchor, testCase.wantR)
			assertIndexRollbackAliasState(t, root, entry.Discard, stage, testCase.wantD)
			targetPath := filepath.Join(root, "b", indexFilename)
			target, err := os.Lstat(targetPath)
			switch testCase.targetRole {
			case "missing":
				if !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("Lstat(target) error = %v, want absent", err)
				}
			case "new":
				if err != nil || !os.SameFile(stage, target) {
					t.Fatalf("target does not alias S: info=%v error=%v", target, err)
				}
			case "old":
				if err != nil || !os.SameFile(anchor, target) {
					t.Fatalf("target does not alias A: info=%v error=%v", target, err)
				}
			default:
				t.Fatalf("unknown target role %q", testCase.targetRole)
			}
		})
	}
}

func indexRollbackStat(t *testing.T, root, relative string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("Lstat(%q): %v", relative, err)
	}
	return info
}

func assertIndexRollbackAliasState(
	t *testing.T,
	root string,
	relative string,
	want os.FileInfo,
	present bool,
) {
	t.Helper()
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
	if !present {
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Lstat(%q) error = %v, want absent", relative, err)
		}
		return
	}
	if err != nil || !os.SameFile(want, info) {
		t.Fatalf("%q does not retain required identity: info=%v error=%v", relative, info, err)
	}
}

func newIndexV3DiscardFixture(
	t *testing.T,
) (*os.Root, publicationFileSpec, publicationFileSpec) {
	t.Helper()
	parent, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	stage, err := createPublicationArtifact(
		context.Background(),
		parent,
		"stage",
		0o644,
		[]byte("# generated\n"),
		publicationBarrierHooks{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Link("stage", "index.md"); err != nil {
		t.Fatal(err)
	}
	return parent, stage, publicationSpecFromBytes(stage.info, stage.data)
}

func createIndexRollbackForeign(
	parent *os.Root,
	name string,
	data []byte,
) (os.FileInfo, error) {
	file, err := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return parent.Lstat(name)
}

func newIndexV3CreateOnlyRollbackBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
	if err := os.WriteFile(
		filepath.Join(root, indexFilename),
		[]byte(documentSessionIndex("0.2", "Root original")),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	return root
}
