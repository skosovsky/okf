//go:build darwin || linux

package bundle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexV3DegradedRegularEvidenceConvergesFromEveryPersistedExistingState(
	t *testing.T,
) {
	tests := []struct {
		name     string
		phase    indexBatchV3RecoveryPhase
		role     string
		mutation string
	}{
		{
			name:     "pre-new-vacated/C~W",
			phase:    indexBatchV3PhasePreNewVacated,
			role:     "C",
			mutation: "in-place",
		},
		{
			name:  "pre-new-rollback-anchored/W",
			phase: indexBatchV3PhasePreNewRollbackAnchored,
			role:  "W",
		},
		{
			name:  "pre-new-rollback-ready/B",
			phase: indexBatchV3PhasePreNewRollbackReady,
			role:  "B",
		},
		{
			name:  "pre-new-restored/C",
			phase: indexBatchV3PhasePreNewRestored,
			role:  "C",
		},
		{
			name:     "published/C~W",
			phase:    indexBatchV3PhasePublished,
			role:     "C",
			mutation: "in-place",
		},
		{
			name:  "rollback-anchored/W",
			phase: indexBatchV3PhaseRollbackAnchored,
			role:  "W",
		},
		{
			name:  "rollback-ready/C",
			phase: indexBatchV3PhaseRollbackReady,
			role:  "C",
		},
		{name: "pre-restore/B", phase: indexBatchV3PhasePreRestore, role: "B"},
		{name: "restored/W", phase: indexBatchV3PhaseRestored, role: "W"},
	}
	seen := make(map[indexBatchV3RecoveryPhase]bool, len(tests))
	seenRole := make(map[string]bool, 3)

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// Arrange a phase through the canonical crash/recovery protocol,
			// then replace exactly one payload role with a stable regular
			// foreign inode. C and W are separate directory entries here, so
			// replacing either leaves its peer as an independent exact source.
			root, entry, original := arrangeIndexV3DegradedRecoveryState(
				t,
				test.phase,
			)
			if got := inspectIndexRecoveryExistingRollbackEntry(t, root, entry.Path); got != test.phase {
				t.Fatalf("arranged phase=%v, want %v", got, test.phase)
			}
			seen[test.phase] = true
			seenRole[test.role] = true
			if test.mutation == "in-place" {
				seenRole["W"] = true
			}

			degradedRelative := indexV3DegradedRecoveryRolePath(t, entry, test.role)
			degradedPath := filepath.Join(root, filepath.FromSlash(degradedRelative))
			switch test.mutation {
			case "in-place":
				if err := mutateIndexCrossDestinationPath(degradedPath, false); err != nil {
					t.Fatal(err)
				}
			case "":
				if err := os.Remove(degradedPath); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					degradedPath,
					[]byte("foreign regular "+test.role+" evidence\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("unsupported role mutation %q", test.mutation)
			}
			degradedInfo, err := os.Lstat(degradedPath)
			if err != nil {
				t.Fatal(err)
			}
			degradedData, err := os.ReadFile(degradedPath)
			if err != nil {
				t.Fatal(err)
			}
			if !degradedInfo.Mode().IsRegular() {
				t.Fatalf("degraded %s mode=%v, want regular", test.role, degradedInfo.Mode())
			}

			// Act.
			firstErr := recoverIndexBatchFixture(root)

			// Assert recovery reaches the phase-family terminal state and
			// reports the retained foreign evidence through the soft type.
			firstSoft := assertIndexV3DegradedRecoverySoft(
				t,
				firstErr,
				entry,
			)
			wantTerminal := indexBatchV3PhaseRestored
			if test.phase == indexBatchV3PhasePreNewVacated ||
				test.phase == indexBatchV3PhasePreNewRollbackAnchored ||
				test.phase == indexBatchV3PhasePreNewRollbackReady ||
				test.phase == indexBatchV3PhasePreNewRestored {
				wantTerminal = indexBatchV3PhasePreNewRestored
			}
			if got := inspectIndexRecoveryExistingRollbackEntry(t, root, entry.Path); got != wantTerminal {
				t.Fatalf("terminal phase=%v, want %v", got, wantTerminal)
			}
			assertIndexV3DegradedRecoveryProofs(
				t,
				root,
				entry,
				original,
				degradedPath,
				degradedInfo,
				degradedData,
			)
			beforeRestart := documentSessionCaptureTree(t, root)
			beforeRestartIdentities := documentPublicationIdentities(t, root)

			// Act again from persisted terminal evidence.
			repeatedErr := recoverIndexBatchFixture(root)

			// Assert the restart returns the same typed soft stop and performs
			// no tree, byte, mode, link, or inode mutation.
			repeatedSoft := assertIndexV3DegradedRecoverySoft(
				t,
				repeatedErr,
				entry,
			)
			if repeatedSoft.destination != firstSoft.destination ||
				repeatedSoft.backup != firstSoft.backup ||
				repeatedSoft.reason != firstSoft.reason {
				t.Fatalf(
					"repeated soft=%#v, want stable classification %#v",
					repeatedSoft,
					firstSoft,
				)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				beforeRestart,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, beforeRestartIdentities)
		})
	}

	for _, phase := range []indexBatchV3RecoveryPhase{
		indexBatchV3PhasePreNewVacated,
		indexBatchV3PhasePreNewRollbackAnchored,
		indexBatchV3PhasePreNewRollbackReady,
		indexBatchV3PhasePreNewRestored,
		indexBatchV3PhasePublished,
		indexBatchV3PhaseRollbackAnchored,
		indexBatchV3PhaseRollbackReady,
		indexBatchV3PhasePreRestore,
		indexBatchV3PhaseRestored,
	} {
		if !seen[phase] {
			t.Errorf("persisted phase %v was not exercised", phase)
		}
	}
	for _, role := range []string{"B", "C", "W"} {
		if !seenRole[role] {
			t.Errorf("regular role %s was not exercised", role)
		}
	}
}

func TestIndexV3StageDriftPolicyByPersistedConsumerProof(t *testing.T) {
	states := []struct {
		name          string
		phase         indexBatchV3RecoveryPhase
		committed     bool
		tokenRelative func(indexBatchManifestEntry) string
	}{
		{
			name:  "pre-new-restored/N",
			phase: indexBatchV3PhasePreNewRestored,
			tokenRelative: func(entry indexBatchManifestEntry) string {
				return entry.NewInstall
			},
		},
		{
			name:  "published/no-token",
			phase: indexBatchV3PhasePublished,
		},
		{
			name:  "pre-restore/D",
			phase: indexBatchV3PhasePreRestore,
			tokenRelative: func(entry indexBatchManifestEntry) string {
				return entry.Discard
			},
		},
		{
			name:  "restored/D",
			phase: indexBatchV3PhaseRestored,
			tokenRelative: func(entry indexBatchManifestEntry) string {
				return entry.Discard
			},
		},
		{
			name:      "committed/no-token",
			phase:     indexBatchV3PhasePublished,
			committed: true,
		},
	}
	forms := []string{"replacement", "in-place", "missing", "type-swap"}
	caseCount := 0

	for _, state := range states {
		state := state
		for _, form := range forms {
			form := form
			caseCount++
			t.Run(state.name+"/"+form, func(t *testing.T) {
				// Arrange a persisted canonical state and record the executable
				// N/D consumer, when present, before drifting S.
				root, entry, original := arrangeIndexV3StagePolicyState(
					t,
					state.phase,
					state.committed,
				)
				if got := inspectIndexRecoveryExistingRollbackEntry(t, root, entry.Path); got != state.phase {
					t.Fatalf("arranged phase=%v, want %v", got, state.phase)
				}
				stagePath := filepath.Join(root, filepath.FromSlash(entry.Stage))
				var tokenPath string
				var tokenInfo os.FileInfo
				if state.tokenRelative != nil {
					tokenPath = filepath.Join(
						root,
						filepath.FromSlash(state.tokenRelative(entry)),
					)
					stageInfo, err := os.Lstat(stagePath)
					if err != nil {
						t.Fatal(err)
					}
					tokenInfo, err = os.Lstat(tokenPath)
					if err != nil || !os.SameFile(stageInfo, tokenInfo) {
						t.Fatalf(
							"canonical consumer is not bound to S: S=%v token=%v error=%v",
							stageInfo,
							tokenInfo,
							err,
						)
					}
				}
				mutateIndexV3StagePolicyForm(t, stagePath, form)
				attackedInfo, attackedInfoErr := os.Lstat(stagePath)
				var attackedData []byte
				if attackedInfoErr == nil && attackedInfo.Mode().IsRegular() {
					var err error
					attackedData, err = os.ReadFile(stagePath)
					if err != nil {
						t.Fatal(err)
					}
				}
				beforeRecovery := documentSessionCaptureTree(t, root)
				beforeRecoveryIdentities := documentPublicationIdentities(t, root)
				wantSoft := form == "replacement" &&
					(state.phase == indexBatchV3PhasePreNewRestored ||
						state.phase == indexBatchV3PhaseRestored) &&
					!state.committed

				// Act.
				recoveryErr := recoverIndexBatchFixture(root)

				if !wantSoft {
					// Assert a missing, non-regular, aliased-content drift, or a
					// state without N/D remains a hard zero-mutation conflict.
					var hard unrecoverableIndexRecoveryError
					var soft recoverableIndexBackupError
					if recoveryErr == nil ||
						errors.As(recoveryErr, &soft) {
						t.Fatalf(
							"recovery error=%v, want hard S conflict without soft classification",
							recoveryErr,
						)
					}
					if !errors.As(recoveryErr, &hard) {
						t.Fatalf(
							"recovery error=%v, want typed hard S conflict",
							recoveryErr,
						)
					}
					documentSessionAssertTreeSnapshotEqual(
						t,
						beforeRecovery,
						documentSessionCaptureTree(t, root),
					)
					documentAssertPublicationIdentities(
						t,
						root,
						beforeRecoveryIdentities,
					)
					manifestArtifactPathForFixture(t, root)
					return
				}

				// Assert a detached foreign regular S is soft only when an
				// independent exact N/D consumer remains. Recovery reaches the
				// exact-A terminal phase and preserves both M and foreign S.
				firstSoft := assertIndexV3DegradedRecoverySoft(
					t,
					recoveryErr,
					entry,
				)
				wantTerminal := state.phase
				if state.phase == indexBatchV3PhasePreRestore {
					wantTerminal = indexBatchV3PhaseRestored
				}
				if got := inspectIndexRecoveryExistingRollbackEntry(t, root, entry.Path); got != wantTerminal {
					t.Fatalf("terminal phase=%v, want %v", got, wantTerminal)
				}
				assertIndexV3DegradedRecoveryProofs(
					t,
					root,
					entry,
					original,
					stagePath,
					attackedInfo,
					attackedData,
				)
				if tokenPath == "" || tokenInfo == nil {
					t.Fatal("soft S drift has no independent consumer proof")
				}
				currentTokenInfo, err := os.Lstat(tokenPath)
				if err != nil || !os.SameFile(tokenInfo, currentTokenInfo) {
					t.Fatalf(
						"soft S recovery changed consumer: before=%v after=%v error=%v",
						tokenInfo,
						currentTokenInfo,
						err,
					)
				}
				beforeRestart := documentSessionCaptureTree(t, root)
				beforeRestartIdentities := documentPublicationIdentities(t, root)

				// Act again.
				repeatedErr := recoverIndexBatchFixture(root)

				// Assert the fresh restart reconstructs the same soft stop and
				// performs no tree or inode mutation.
				repeatedSoft := assertIndexV3DegradedRecoverySoft(
					t,
					repeatedErr,
					entry,
				)
				if repeatedSoft.destination != firstSoft.destination ||
					repeatedSoft.backup != firstSoft.backup ||
					repeatedSoft.reason != firstSoft.reason {
					t.Fatalf(
						"repeated soft=%#v, want stable classification %#v",
						repeatedSoft,
						firstSoft,
					)
				}
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
			})
		}
	}
	if caseCount != 20 {
		t.Fatalf("S policy cases=%d, want 20", caseCount)
	}
}

func TestIndexV3TerminalStageConflictStopsBeforeEveryPeerAndCleanup(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "lexical-first", path: "a/index.md"},
		{name: "lexical-last", path: "b/index.md"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// Arrange the canonical all-terminal rollback boundary immediately
			// before its first cleanup. A and B are the lexical endpoint peers.
			root := t.TempDir()
			before, _ := arrangeIndexBatchCrashBundle(t, root)
			runIndexBatchCrashHelper(t, root, "rollback-cleanup-before-first")
			manifest := readIndexBatchManifestFixture(t, root)
			entry := indexBatchManifestEntryByPath(t, manifest, test.path)
			if got := inspectIndexRecoveryExistingRollbackEntry(t, root, entry.Path); got != indexBatchV3PhaseRestored {
				t.Fatalf("terminal peer phase=%v, want RESTORED", got)
			}
			stagePath := filepath.Join(root, filepath.FromSlash(entry.Stage))
			mutateIndexV3StagePolicyForm(t, stagePath, "replacement")
			stageInfo, err := os.Lstat(stagePath)
			if err != nil {
				t.Fatal(err)
			}
			stageData, err := os.ReadFile(stagePath)
			if err != nil {
				t.Fatal(err)
			}
			beforeFirst := documentSessionCaptureTree(t, root)
			beforeFirstIdentities := documentPublicationIdentities(t, root)
			var firstTrace []string

			// Act.
			firstErr := recoverIndexBatchFixtureWithHooks(
				root,
				indexV3TerminalConflictTraceHooks(&firstTrace),
			)

			// Assert the terminal dispatcher returns the selected peer's soft
			// conflict before any peer transition or cleanup attempt.
			firstSoft := assertIndexV3DegradedRecoverySoft(t, firstErr, entry)
			if len(firstTrace) != 0 {
				t.Fatalf("first mutation trace=%#v, want none", firstTrace)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				beforeFirst,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, beforeFirstIdentities)
			assertIndexV3DegradedRecoveryProofs(
				t,
				root,
				entry,
				before[entry.Path],
				stagePath,
				stageInfo,
				stageData,
			)
			beforeRestart := documentSessionCaptureTree(t, root)
			beforeRestartIdentities := documentPublicationIdentities(t, root)
			var restartTrace []string

			// Act again.
			repeatedErr := recoverIndexBatchFixtureWithHooks(
				root,
				indexV3TerminalConflictTraceHooks(&restartTrace),
			)

			// Assert a fresh dispatcher makes the identical decision without
			// mutating either endpoint peer or entering cleanup.
			repeatedSoft := assertIndexV3DegradedRecoverySoft(t, repeatedErr, entry)
			if repeatedSoft.destination != firstSoft.destination ||
				repeatedSoft.backup != firstSoft.backup ||
				repeatedSoft.reason != firstSoft.reason {
				t.Fatalf(
					"repeated soft=%#v, want stable classification %#v",
					repeatedSoft,
					firstSoft,
				)
			}
			if len(restartTrace) != 0 {
				t.Fatalf("restart mutation trace=%#v, want none", restartTrace)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				beforeRestart,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, beforeRestartIdentities)
		})
	}
}

func indexV3TerminalConflictTraceHooks(trace *[]string) indexPublishHooks {
	record := func(event string) {
		*trace = append(*trace, event)
	}
	return indexPublishHooks{
		afterArtifactDirectorySync: func(_, _, _ string, _ *os.Root) error {
			record("artifact-directory-sync")
			return nil
		},
		afterVacateRename: func(string, *os.Root, string, string) error {
			record("vacate-rename")
			return nil
		},
		afterVacate: func(string, *os.Root, string, string) error {
			record("vacate-sync")
			return nil
		},
		afterRestoreLink: func(string, *os.Root, string, string) error {
			record("restore-link")
			return nil
		},
		afterBatchRecoveryAction: func(string, *os.Root, string) error {
			record("batch-recovery-action")
			return nil
		},
		beforeCleanup: func(string, *os.Root, string, string) error {
			record("before-cleanup")
			return nil
		},
		afterCleanup: func(string, *os.Root, string, string) error {
			record("after-cleanup")
			return nil
		},
	}
}

func arrangeIndexV3StagePolicyState(
	t *testing.T,
	phase indexBatchV3RecoveryPhase,
	committed bool,
) (string, indexBatchManifestEntry, []byte) {
	t.Helper()
	if !committed {
		return arrangeIndexV3DegradedRecoveryState(t, phase)
	}
	root := t.TempDir()
	before, _ := arrangeIndexBatchCrashBundle(t, root)
	runIndexBatchCrashHelper(t, root, "root-commit")
	manifest := readIndexBatchManifestFixture(t, root)
	entry := indexBatchManifestEntryByPath(t, manifest, "a/index.md")
	for role, relative := range map[string]string{
		"N": entry.NewInstall,
		"D": entry.Discard,
	} {
		if _, err := os.Lstat(
			filepath.Join(root, filepath.FromSlash(relative)),
		); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("committed state retained %s token %q: %v", role, relative, err)
		}
	}
	return root, entry, before[entry.Path]
}

func mutateIndexV3StagePolicyForm(t *testing.T, filename, form string) {
	t.Helper()
	switch form {
	case "replacement":
		if err := os.Remove(filename); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte("foreign regular S evidence\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "in-place":
		if err := mutateIndexCrossDestinationPath(filename, false); err != nil {
			t.Fatal(err)
		}
	case "missing":
		if err := os.Remove(filename); err != nil {
			t.Fatal(err)
		}
	case "type-swap":
		if err := os.Remove(filename); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(indexFilename, filename); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported S mutation form %q", form)
	}
}

func arrangeIndexV3DegradedRecoveryState(
	t *testing.T,
	phase indexBatchV3RecoveryPhase,
) (string, indexBatchManifestEntry, []byte) {
	t.Helper()

	arrangePreNew := func() (string, map[string][]byte, indexBatchManifestEntry) {
		root := t.TempDir()
		before, _ := arrangeIndexBatchCrashBundle(t, root)
		runIndexBatchCrashHelper(t, root, "mid-vacate")
		manifest := readIndexBatchManifestFixture(t, root)
		return root, before, indexBatchManifestEntryByPath(t, manifest, "b/index.md")
	}

	switch phase {
	case indexBatchV3PhasePreNewVacated:
		root, before, entry := arrangePreNew()
		return root, entry, before[entry.Path]
	case indexBatchV3PhasePreNewRollbackAnchored:
		root, before, entry := arrangePreNew()
		runIndexRecoveryPreNewRollbackCrash(t, root, "anchor-durable")
		return root, entry, before[entry.Path]
	case indexBatchV3PhasePreNewRollbackReady:
		root, before, entry := arrangePreNew()
		runIndexRecoveryPreNewRollbackCrash(t, root, "restore-install-durable")
		return root, entry, before[entry.Path]
	case indexBatchV3PhasePreNewRestored:
		root, before, entry := arrangePreNew()
		runIndexRecoveryPreNewRollbackCrash(t, root, "restore-install-post-rename")
		return root, entry, before[entry.Path]
	case indexBatchV3PhasePublished:
		root, before, entry := arrangeIndexRecoveryExistingRollbackPublished(t)
		return root, entry, before[entry.Path]
	case indexBatchV3PhaseRollbackAnchored:
		root, before, entry := arrangeIndexRecoveryExistingRollbackPublished(t)
		runIndexRecoveryExistingRollbackCrash(t, root, "anchor-durable")
		return root, entry, before[entry.Path]
	case indexBatchV3PhaseRollbackReady:
		root, before, entry := arrangeIndexRecoveryExistingRollbackPublished(t)
		runIndexRecoveryExistingRollbackCrash(t, root, "restore-install-durable")
		return root, entry, before[entry.Path]
	case indexBatchV3PhasePreRestore:
		root, before, entry := arrangeIndexRecoveryExistingRollbackPublished(t)
		runIndexRecoveryExistingRollbackCrash(t, root, "discard-post-sync")
		return root, entry, before[entry.Path]
	case indexBatchV3PhaseRestored:
		root, before, entry := arrangeIndexRecoveryExistingRollbackPublished(t)
		runIndexRecoveryExistingRollbackCrash(t, root, "restore-install-post-rename")
		return root, entry, before[entry.Path]
	default:
		t.Fatalf("unsupported degraded-recovery phase %v", phase)
		return "", indexBatchManifestEntry{}, nil
	}
}

func indexV3DegradedRecoveryRolePath(
	t *testing.T,
	entry indexBatchManifestEntry,
	role string,
) string {
	t.Helper()
	switch role {
	case "B":
		return entry.Backup
	case "C":
		return entry.Claim
	case "W":
		return entry.Witness
	default:
		t.Fatalf("unsupported degraded regular role %q", role)
		return ""
	}
}

func assertIndexV3DegradedRecoverySoft(
	t *testing.T,
	err error,
	entry indexBatchManifestEntry,
) recoverableIndexBackupError {
	t.Helper()
	var soft recoverableIndexBackupError
	if err == nil ||
		!errors.As(err, &soft) ||
		soft.destination != entry.Path ||
		soft.backup != entry.Anchor {
		t.Fatalf(
			"recovery error=%v, want only typed soft recovery for %q via %q",
			err,
			entry.Path,
			entry.Anchor,
		)
	}
	return soft
}

func assertIndexV3DegradedRecoveryProofs(
	t *testing.T,
	root string,
	entry indexBatchManifestEntry,
	original []byte,
	degradedPath string,
	degradedInfo os.FileInfo,
	degradedData []byte,
) {
	t.Helper()

	manifestArtifactPathForFixture(t, root)
	for role, relative := range map[string]string{
		"S": entry.Stage,
		"B": entry.Backup,
		"C": entry.Claim,
		"W": entry.Witness,
		"A": entry.Anchor,
	} {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s evidence %q info=%v error=%v, want retained regular file", role, relative, info, err)
		}
	}

	targetPath := filepath.Join(root, filepath.FromSlash(entry.Path))
	anchorPath := filepath.Join(root, filepath.FromSlash(entry.Anchor))
	targetInfo, targetErr := os.Lstat(targetPath)
	anchorInfo, anchorErr := os.Lstat(anchorPath)
	if targetErr != nil ||
		anchorErr != nil ||
		!os.SameFile(targetInfo, anchorInfo) {
		t.Fatalf(
			"terminal target is not exact A: target=%v A=%v targetErr=%v anchorErr=%v",
			targetInfo,
			anchorInfo,
			targetErr,
			anchorErr,
		)
	}
	assertFileBytes(t, targetPath, original)

	currentDegradedInfo, err := os.Lstat(degradedPath)
	if err != nil || !os.SameFile(degradedInfo, currentDegradedInfo) {
		t.Fatalf(
			"foreign evidence identity changed: before=%v after=%v error=%v",
			degradedInfo,
			currentDegradedInfo,
			err,
		)
	}
	got, err := os.ReadFile(degradedPath)
	if err != nil || !bytes.Equal(got, degradedData) {
		t.Fatalf("foreign evidence bytes=%q error=%v, want %q", got, err, degradedData)
	}
}
