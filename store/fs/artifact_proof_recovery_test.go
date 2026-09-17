package fs

// Artifact proof recovery and private-directory durability.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	okfstore "github.com/skosovsky/okf/store"
)

func TestConsumeNeverSynthesizesUnboundProof(t *testing.T) {
	tests := []struct {
		name   string
		role   artifactRole
		target string
		dir    bool
	}{
		{name: "payload", role: claimPayload, target: ".okf/staging/txn-r5/payload/payload-00000"},
		{name: "journal", role: claimJournal, target: ".okf/transactions/txn-r5.json"},
		{name: "scratch", role: claimScratch, target: ".okf/temporary/r5.tmp"},
		{name: "stage directory", role: claimStageDir, target: ".okf/staging/txn-r5", dir: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			absolute := filepath.Join(root, filepath.FromSlash(test.target))
			if test.dir {
				if err := os.MkdirAll(absolute, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(absolute, []byte("unbound"), 0o640); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(absolute)
			if err != nil {
				t.Fatal(err)
			}
			key, err := newArtifactClaimKey("sha256:"+strings.Repeat("0", 64), test.target, test.role)
			if err != nil {
				t.Fatal(err)
			}
			claimsBefore := claimInventory(t, root)
			faultCalls := 0
			s.config.Fault = func(Step) error { faultCalls++; return nil }
			s.config.PostFault = func(Step) error { faultCalls++; return nil }

			// Act.
			consumed, consumeErr := s.consumeOwnedClaim(context.Background(), key, test.dir)
			after, statErr := os.Lstat(absolute)
			claimsAfter := claimInventory(t, root)

			// Assert.
			if consumed || consumeErr != nil || statErr != nil || !os.SameFile(before, after) || faultCalls != 0 || !reflect.DeepEqual(claimsBefore, claimsAfter) {
				t.Fatalf("consumed=%t err=%v stat=%v same=%t fault_calls=%d claims=%v/%v", consumed, consumeErr, statErr, os.SameFile(before, after), faultCalls, claimsBefore, claimsAfter)
			}
		})
	}
}

func TestCanceledStageBuildCompensatesAttemptProof(t *testing.T) {
	for _, phase := range []string{"stage_build_directory_synced", "stage_build_parent_synced"} {
		t.Run(phase, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := nextSnapshotForDurabilityTest(t, s)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "r5-stage-cancel-"+phase, "")
			fired := false
			s.descriptorBarrier = func(got string) error {
				if !fired && got == phase {
					fired = true
					return context.Canceled
				}
				return nil
			}

			// Act.
			err := s.publish(context.Background(), next, receipt)
			s.descriptorBarrier = nil
			visible, readErr := readVisibleRoot(context.Background(), s.rootFD)
			claims := claimInventory(t, root)
			stage, _ := journalStage(receipt.RequestDigest)
			journalName, _ := journalPath(receipt.RequestDigest)
			_, stageErr := os.Stat(filepath.Join(root, filepath.FromSlash(path.Dir(stage))))
			_, journalErr := os.Stat(filepath.Join(root, filepath.FromSlash(journalName)))

			// Assert.
			var committed *okfstore.CommittedError
			if !fired || !errors.Is(err, context.Canceled) || errors.As(err, &committed) || readErr != nil || !sameVisibleFiles(visible, base.(*snapshot).files) || len(claims) != 0 || !errors.Is(stageErr, os.ErrNotExist) || !errors.Is(journalErr, os.ErrNotExist) {
				t.Fatalf("fired=%t err=%v committed=%v read=%v visible=%v claims=%v stage=%v journal=%v", fired, err, committed, readErr, sortedFilePaths(visible), claims, stageErr, journalErr)
			}
		})
	}
}

func TestDirectoryProofMatrix(t *testing.T) {
	for _, test := range []struct {
		name string
		act  func(*testing.T, string, *Store, artifactClaimKey, fs.FileInfo) error
		ok   bool
	}{
		{
			name: "full clean proof",
			act: func(_ *testing.T, _ string, s *Store, key artifactClaimKey, owned fs.FileInfo) error {
				_, err := s.prepareOwnedClaim(context.Background(), key, owned, true)
				return err
			},
			ok: true,
		},
		{
			name: "forged sentinel without binding",
			act: func(t *testing.T, root string, _ *Store, key artifactClaimKey, owned fs.FileInfo) error {
				identity, _ := fileIdentityKey(owned)
				record := directoryWitnessRecord{Version: 1, Operation: key.Operation, Path: key.Path, Role: key.Role, Identity: identity}
				raw, _ := json.Marshal(record)
				return os.WriteFile(filepath.Join(root, filepath.FromSlash(key.witnessPath(true))), raw, 0o600)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			target := path.Join(internalDirectory, "staging", "txn-r5-directory")
			absolute := filepath.Join(root, filepath.FromSlash(target))
			if err := os.MkdirAll(absolute, 0o700); err != nil {
				t.Fatal(err)
			}
			owned, _ := os.Stat(absolute)
			key, _ := newArtifactClaimKey("r5-directory-proof", target, claimStageDir)
			if err := test.act(t, root, s, key, owned); err != nil {
				t.Fatal(err)
			}

			// Act.
			consumed, consumeErr := s.consumeOwnedClaim(context.Background(), key, true)

			// Assert.
			if test.ok {
				if !consumed || consumeErr != nil {
					t.Fatalf("consume clean proof=(%t,%v)", consumed, consumeErr)
				}
				return
			}
			if consumed || !errors.Is(consumeErr, errArtifactClaimConflict) {
				t.Fatalf("consume forged proof=(%t,%v)", consumed, consumeErr)
			}
			if after, err := os.Stat(absolute); err != nil || !os.SameFile(owned, after) {
				t.Fatalf("forged directory preservation=(%v,%v)", after, err)
			}
		})
	}
}

func TestDirectoryWrongSourcePreservesClaim(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	target := path.Join(internalDirectory, "staging", "txn-r5-wrong-source")
	absolute := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		t.Fatal(err)
	}
	owned, _ := os.Stat(absolute)
	key, _ := newArtifactClaimKey("r5-wrong-source", target, claimStageDir)
	claim, err := s.prepareOwnedClaim(context.Background(), key, owned, true)
	if err != nil {
		t.Fatal(err)
	}
	bindingName := filepath.Join(root, filepath.FromSlash(key.bindingPath()))
	raw, err := os.ReadFile(bindingName)
	if err != nil {
		t.Fatal(err)
	}
	var record directoryWitnessRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record.Source = target
	raw, _ = json.Marshal(record)
	if err := os.WriteFile(bindingName, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act.
	consumed, consumeErr := s.consumeOwnedClaim(context.Background(), key, true)
	after, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.claimPath())))

	// Assert.
	if consumed || !errors.Is(consumeErr, errArtifactClaimConflict) || statErr != nil || !os.SameFile(claim, after) {
		t.Fatalf("consume=(%t,%v) claim=(%v,%v) same=%t", consumed, consumeErr, after, statErr, statErr == nil && os.SameFile(claim, after))
	}
}

func TestDirectoryNoReplacePinsSourceAndTarget(t *testing.T) {
	pairs := []struct{ name, from, to string }{
		{name: "build to canonical", from: ".okf/claims/r5.build", to: ".okf/staging/r5-canonical"},
		{name: "canonical to claim", from: ".okf/staging/r5-source", to: ".okf/claims/r5.claim"},
		{name: "restore claim", from: ".okf/claims/r5-restore.claim", to: ".okf/staging/r5-restored"},
	}
	for _, pair := range pairs {
		for _, seam := range []string{"claim_after_source_stat", "claim_after_rename"} {
			t.Run(pair.name+"/"+seam, func(t *testing.T) {
				// Arrange.
				root, s := adversarialStore(t, Config{})
				absoluteFrom := filepath.Join(root, filepath.FromSlash(pair.from))
				absoluteTo := filepath.Join(root, filepath.FromSlash(pair.to))
				if err := os.MkdirAll(absoluteFrom, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(absoluteFrom, directorySentinelName), []byte("owned sentinel"), 0o600); err != nil {
					t.Fatal(err)
				}
				owned, _ := os.Stat(absoluteFrom)
				var saved string
				var foreign fs.FileInfo
				s.descriptorBarrier = func(got string) error {
					if got != seam {
						return nil
					}
					name := absoluteFrom
					if seam == "claim_after_rename" {
						name = absoluteTo
					}
					saved = name + ".owned"
					if err := os.Rename(name, saved); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(name, 0o750); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(name, directorySentinelName), []byte("foreign sentinel"), 0o640); err != nil {
						t.Fatal(err)
					}
					foreign, _ = os.Stat(name)
					s.descriptorBarrier = nil
					return nil
				}

				// Act.
				_, moveErr := s.fdRenameOwnedNoReplace(pair.from, pair.to, owned, true)
				restoredForeign, sourceErr := os.Stat(absoluteFrom)
				preservedOwned, savedErr := os.Stat(saved)
				_, targetErr := os.Stat(absoluteTo)

				// Assert.
				if !errors.Is(moveErr, errArtifactClaimConflict) || sourceErr != nil || savedErr != nil || foreign == nil || !os.SameFile(foreign, restoredForeign) || !os.SameFile(owned, preservedOwned) || !errors.Is(targetErr, os.ErrNotExist) {
					t.Fatalf("move=%v source=%v saved=%v target=%v foreign_same=%t owned_same=%t", moveErr, sourceErr, savedErr, targetErr, foreign != nil && os.SameFile(foreign, restoredForeign), os.SameFile(owned, preservedOwned))
				}
				foreignSentinel, _ := os.ReadFile(filepath.Join(absoluteFrom, directorySentinelName))
				ownedSentinel, _ := os.ReadFile(filepath.Join(saved, directorySentinelName))
				if string(foreignSentinel) != "foreign sentinel" || string(ownedSentinel) != "owned sentinel" || restoredForeign.Mode().Perm() != 0o750 || preservedOwned.Mode().Perm() != 0o700 {
					t.Fatalf("sentinels=%q/%q modes=%#o/%#o", foreignSentinel, ownedSentinel, restoredForeign.Mode().Perm(), preservedOwned.Mode().Perm())
				}
			})
		}
	}
}

func TestDirectoryNoReplaceCrashRetry(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	from, to := ".okf/claims/r5-crash.build", ".okf/staging/r5-crash"
	absoluteFrom := filepath.Join(root, filepath.FromSlash(from))
	if err := os.MkdirAll(absoluteFrom, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(absoluteFrom, directorySentinelName), []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	owned, _ := os.Stat(absoluteFrom)
	injected := errors.New("crash after directory rename")
	s.descriptorBarrier = func(got string) error {
		if got == "claim_after_rename" {
			return injected
		}
		return nil
	}

	// Act.
	_, firstErr := s.fdRenameOwnedNoReplace(from, to, owned, true)
	s.descriptorBarrier = nil
	installed, installedErr := os.Stat(filepath.Join(root, filepath.FromSlash(to)))
	_, retryErr := s.fdRenameOwnedNoReplace(to, from, installed, true)
	recovered, recoveredErr := os.Stat(absoluteFrom)

	// Assert.
	if !errors.Is(firstErr, injected) || installedErr != nil || retryErr != nil || recoveredErr != nil || !os.SameFile(owned, recovered) {
		t.Fatalf("first=%v installed=%v retry=%v recovered=%v same=%t", firstErr, installedErr, retryErr, recoveredErr, recoveredErr == nil && os.SameFile(owned, recovered))
	}
}

func TestOwnedStageForeignPayloadIsPreserved(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	operation := "sha256:" + strings.Repeat("3", 64)
	target := ".okf/staging/txn-r5-foreign/payload/payload-00000"
	source := ".okf/temporary/r5-payload-source"
	absoluteSource := filepath.Join(root, filepath.FromSlash(source))
	absoluteTarget := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(absoluteSource), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(absoluteTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absoluteSource, []byte("owned payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	owned, _ := os.Stat(absoluteSource)
	key, _ := newArtifactClaimKey(operation, target, claimPayload)
	if _, err := s.installOwnedClaimed(context.Background(), key, source, owned, false); err != nil {
		t.Fatal(err)
	}
	saved := absoluteTarget + ".owned"
	if err := os.Rename(absoluteTarget, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absoluteTarget, []byte("foreign payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	foreign, _ := os.Stat(absoluteTarget)
	claimsBefore := claimInventory(t, root)

	// Act.
	consumed, consumeErr := s.consumeOwnedClaim(context.Background(), key, false)
	after, statErr := os.Stat(absoluteTarget)
	ownedAfter, ownedErr := os.Stat(saved)
	claimsAfter := claimInventory(t, root)

	// Assert.
	if consumed || !errors.Is(consumeErr, errArtifactClaimConflict) || statErr != nil || ownedErr != nil || !os.SameFile(foreign, after) || !os.SameFile(owned, ownedAfter) || !reflect.DeepEqual(claimsBefore, claimsAfter) {
		t.Fatalf("consume=(%t,%v) foreign=%v owned=%v claims=%v/%v", consumed, consumeErr, statErr, ownedErr, claimsBefore, claimsAfter)
	}
	raw, _ := os.ReadFile(absoluteTarget)
	if string(raw) != "foreign payload" || after.Mode().Perm() != 0o640 {
		t.Fatalf("foreign payload=(%q,%#o)", raw, after.Mode().Perm())
	}
}

func TestScratchRecoveryPreservesUnboundTempExactly(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	target := path.Join(temporaryDirectory, ".okf-tmp-r5-unbound")
	absolute := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		t.Fatal(err)
	}
	want := []byte("foreign unbound scratch")
	if err := os.WriteFile(absolute, want, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(absolute)
	if err != nil {
		t.Fatal(err)
	}
	claimsBefore := claimInventory(t, root)

	// Act.
	cleanupErr := s.cleanupScratchInventory(context.Background())
	after, statErr := os.Stat(absolute)
	got, readErr := os.ReadFile(absolute)
	claimsAfter := claimInventory(t, root)

	// Assert.
	if cleanupErr != nil || statErr != nil || readErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !reflect.DeepEqual(want, got) || !reflect.DeepEqual(claimsBefore, claimsAfter) {
		t.Fatalf("cleanup=%v stat=%v read=%v same=%t mode=%v/%v bytes=%q claims=%v/%v", cleanupErr, statErr, readErr, statErr == nil && os.SameFile(before, after), before.Mode(), after.Mode(), got, claimsBefore, claimsAfter)
	}
}

func TestPrivateDirectoryFaultMatrixConvergesOnRetryAndReopen(t *testing.T) {
	for _, step := range []Step{StepPrivateDirectoryChmod, StepPrivateDirectorySync, StepPrivateDirectoryClose, StepPrivateDirectoryParentSync} {
		for _, phase := range []string{"fault", "postfault"} {
			t.Run(string(step)+"/"+phase, func(t *testing.T) {
				// Arrange.
				root, s := adversarialStore(t, Config{})
				absolute := filepath.Join(root, filepath.FromSlash(claimDirectory))
				if err := os.MkdirAll(absolute, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(absolute, 0o755); err != nil {
					t.Fatal(err)
				}
				injected := errors.New("private directory cut")
				fired := false
				hook := func(got Step) error {
					if !fired && got == step {
						fired = true
						return injected
					}
					return nil
				}
				if phase == "fault" {
					s.config.Fault = hook
				} else {
					s.config.PostFault = hook
				}

				// Act.
				firstErr := s.repairPrivateDirectory(context.Background(), claimDirectory)
				s.config.Fault, s.config.PostFault = nil, nil
				retryErr := s.repairPrivateDirectory(context.Background(), claimDirectory)
				after, statErr := os.Stat(absolute)
				reopened, openErr := Open(root, Config{})
				var reopenErr error
				if openErr == nil {
					_, reopenErr = reopened.Snapshot(context.Background())
					reopenErr = errors.Join(reopenErr, reopened.Close())
				}

				// Assert.
				if !fired || !errors.Is(firstErr, injected) || retryErr != nil || statErr != nil || after.Mode().Perm() != 0o700 || openErr != nil || reopenErr != nil {
					t.Fatalf("fired=%t first=%v retry=%v stat=%v mode=%#o open=%v reopen=%v", fired, firstErr, retryErr, statErr, after.Mode().Perm(), openErr, reopenErr)
				}
			})
		}
	}
}

func TestPrivateDirectorySwapIsStructuralAndForeignIsUntouched(t *testing.T) {
	for _, seam := range []string{"private_directory_after_open", "private_directory_after_chmod", "private_directory_after_sync"} {
		t.Run(seam, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			absolute := filepath.Join(root, filepath.FromSlash(claimDirectory))
			saved := absolute + ".owned"
			if err := os.MkdirAll(absolute, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(absolute, 0o755); err != nil {
				t.Fatal(err)
			}
			var foreign os.FileInfo
			parentSyncs := 0
			s.config.Fault = func(step Step) error {
				if step == StepPrivateDirectoryParentSync {
					parentSyncs++
				}
				return nil
			}
			s.descriptorBarrier = func(operation string) error {
				if operation != seam || foreign != nil {
					return nil
				}
				if err := os.Rename(absolute, saved); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(absolute, 0o750); err != nil {
					t.Fatal(err)
				}
				foreign, _ = os.Stat(absolute)
				return nil
			}

			// Act.
			repairErr := s.repairPrivateDirectory(context.Background(), claimDirectory)
			after, statErr := os.Stat(absolute)
			s.descriptorBarrier = nil
			removeErr := os.Remove(absolute)
			restoreErr := os.Rename(saved, absolute)
			retryErr := s.repairPrivateDirectory(context.Background(), claimDirectory)

			// Assert.
			if !errors.Is(repairErr, okfstore.ErrStorageCorrupt) || foreign == nil || statErr != nil || !os.SameFile(foreign, after) || after.Mode().Perm() != 0o750 || parentSyncs != 1 || removeErr != nil || restoreErr != nil || retryErr != nil {
				t.Fatalf("repair=%v foreign=%v stat=%v same=%t mode=%#o parent_syncs=%d remove=%v restore=%v retry=%v", repairErr, foreign, statErr, foreign != nil && statErr == nil && os.SameFile(foreign, after), after.Mode().Perm(), parentSyncs, removeErr, restoreErr, retryErr)
			}
		})
	}
}

func TestPrivateDirectoryAlreadyPrivateStillConvergesDurability(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	absolute := filepath.Join(root, filepath.FromSlash(claimDirectory))
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		t.Fatal(err)
	}
	counts := make(map[Step]int)
	s.config.Fault = func(step Step) error { counts[step]++; return nil }

	// Act.
	repairErr := s.repairPrivateDirectory(context.Background(), claimDirectory)
	after, statErr := os.Stat(absolute)

	// Assert.
	if repairErr != nil || statErr != nil || after.Mode().Perm() != 0o700 || counts[StepPrivateDirectoryChmod] != 0 || counts[StepPrivateDirectorySync] != 1 || counts[StepPrivateDirectoryClose] != 1 || counts[StepPrivateDirectoryParentSync] != 1 {
		t.Fatalf("repair=%v stat=%v mode=%#o counts=%v", repairErr, statErr, after.Mode().Perm(), counts)
	}
}

func TestPrivateDirectoryCancellationPreservesContextValue(t *testing.T) {
	for _, seam := range []string{"private_directory_after_open", "private_directory_after_chmod", "private_directory_after_sync"} {
		t.Run(seam, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			absolute := filepath.Join(root, filepath.FromSlash(claimDirectory))
			if err := os.MkdirAll(absolute, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(absolute, 0o755); err != nil {
				t.Fatal(err)
			}
			key := struct{ name string }{"r5-private-value"}
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "kept"))
			observed := false
			s.descriptorBarrier = func(operation string) error {
				if operation == seam {
					observed = ctx.Value(key) == "kept"
					cancel()
				}
				return nil
			}

			// Act.
			repairErr := s.repairPrivateDirectory(ctx, claimDirectory)
			after, statErr := os.Stat(absolute)

			// Assert.
			wantMode := os.FileMode(0o700)
			if seam == "private_directory_after_open" {
				wantMode = 0o755
			}
			if !errors.Is(repairErr, context.Canceled) || !observed || statErr != nil || after.Mode().Perm() != wantMode {
				t.Fatalf("repair=%v observed=%t stat=%v mode=%#o want=%#o", repairErr, observed, statErr, after.Mode().Perm(), wantMode)
			}
		})
	}
}
