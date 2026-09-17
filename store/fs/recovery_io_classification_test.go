package fs

// Recovery I/O and structural-error classification.

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestJournalReadPinsOneIdentityThroughOwnership(t *testing.T) {
	// Arrange.
	root, s, _, _ := stageRecoveryJournal(t, Config{})
	journalName := path.Join(internalDirectory, "transactions", pendingJournalNames(t, root)[0])
	var foreign os.FileInfo
	s.descriptorBarrier = func(operation string) error {
		if operation == "journal_ownership" && foreign == nil {
			_, _, foreign = replaceRegularWithSameBytes(t, root, journalName)
		}
		return nil
	}

	// Act.
	recoverErr := s.recoverForTest(context.Background())
	after, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(journalName)))
	var committed *store.CommittedError

	// Assert.
	if !errors.Is(recoverErr, store.ErrStorageCorrupt) || errors.As(recoverErr, &committed) || foreign == nil || statErr != nil || !os.SameFile(foreign, after) {
		t.Fatalf("recover=%v committed=%v foreign=%v stat=%v", recoverErr, committed, foreign, statErr)
	}
}

func TestAtomicInstallBindsRenamedSourceIdentity(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)
	next := nextSnapshotForDurabilityTest(t, s)
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "rename-capture", "")
	journalName, _ := journalPath(receipt.RequestDigest)
	stageName, _ := journalStage(receipt.RequestDigest)
	captures := 0
	var foreign os.FileInfo
	s.descriptorBarrier = func(operation string) error {
		if operation != "rename_capture" || foreign != nil {
			return nil
		}
		captures++
		if captures == len(next.files)+1 {
			_, _, foreign = replaceRegularWithSameBytes(t, root, journalName)
		}
		return nil
	}

	// Act.
	publishErr := s.publish(context.Background(), next, receipt)
	after, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(journalName)))
	_, stageErr := os.Stat(filepath.Join(root, filepath.FromSlash(stageName)))
	var committed *store.CommittedError

	// Assert.
	if publishErr == nil || errors.Is(publishErr, store.ErrStorageCorrupt) || errors.As(publishErr, &committed) || foreign == nil || statErr != nil || !os.SameFile(foreign, after) || stageErr != nil {
		t.Fatalf("publish=%v committed=%v captures=%d foreign=%v journal=%v stage=%v", publishErr, committed, captures, foreign, statErr, stageErr)
	}
	visible, readErr := readVisibleRoot(context.Background(), s.rootFD)
	if readErr != nil {
		t.Fatal(readErr)
	}
	visibleSnapshot, snapshotErr := newSnapshotWithAlgorithm(context.Background(), visible, s.config.HashAlgorithm)
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if got := visibleSnapshot.Revision(); got != base.Revision() {
		t.Fatalf("visible revision=%s want base=%s", got, base.Revision())
	}
}

func TestRecoveryIOWinsOverCancellation(t *testing.T) {
	for _, cut := range []string{"journal_ownership", "payload_read"} {
		t.Run(cut, func(t *testing.T) {
			// Arrange.
			_, s, _, next := stageRecoveryJournal(t, Config{})
			ctx, cancel := context.WithCancel(context.Background())
			injected := errors.New("raw recovery EIO")
			s.descriptorBarrier = func(operation string) error {
				if operation == cut {
					cancel()
					return injected
				}
				return nil
			}

			// Act.
			recoverErr := s.recoverForTest(ctx)
			var committed *store.CommittedError

			// Assert.
			if !errors.Is(recoverErr, injected) || errors.Is(recoverErr, context.Canceled) || errors.Is(recoverErr, store.ErrStorageCorrupt) {
				t.Fatalf("recover=%v, want exact operational EIO", recoverErr)
			}
			if cut == "journal_ownership" && errors.As(recoverErr, &committed) {
				t.Fatalf("pre-ownership error unexpectedly committed: %v", committed)
			}
			if cut == "payload_read" && (!errors.As(recoverErr, &committed) || committed.Receipt().ResultRevision != next.Revision()) {
				t.Fatalf("post-ownership error=%v committed=%v", recoverErr, committed)
			}
		})
	}
}

func TestRecoveryDescriptorIOClassification(t *testing.T) {
	for _, cut := range []string{
		"journal_open", "journal_stat", "journal_restat",
		"payload_open", "payload_stat", "payload_restat",
	} {
		t.Run(cut, func(t *testing.T) {
			// Arrange.
			_, s, _, next := stageRecoveryJournal(t, Config{})
			ctx, cancel := context.WithCancel(context.Background())
			injected := errors.New("descriptor EIO at " + cut)
			s.descriptorBarrier = func(operation string) error {
				if operation == cut {
					cancel()
					return injected
				}
				return nil
			}

			// Act.
			recoverErr := s.recoverForTest(ctx)
			var committed *store.CommittedError

			// Assert.
			if !errors.Is(recoverErr, injected) || errors.Is(recoverErr, context.Canceled) || errors.Is(recoverErr, store.ErrStorageCorrupt) {
				t.Fatalf("recover=%v, want operational descriptor error", recoverErr)
			}
			payloadCut := strings.HasPrefix(cut, "payload_")
			if payloadCut != errors.As(recoverErr, &committed) {
				t.Fatalf("recover=%v committed=%v payload cut=%t", recoverErr, committed, payloadCut)
			}
			if payloadCut && committed.Receipt().ResultRevision != next.Revision() {
				t.Fatalf("receipt=%#v want revision=%s", committed.Receipt(), next.Revision())
			}
		})
	}
}

func TestPayloadStructuralFailuresRemainCorruption(t *testing.T) {
	for _, kind := range []string{"type", "size", "hash", "identity"} {
		t.Run(kind, func(t *testing.T) {
			// Arrange.
			root, s, _, _ := stageRecoveryJournal(t, Config{})
			journal := readOnlyPendingJournal(t, root)
			payload := filepath.Join(root, filepath.FromSlash(path.Join(journal.Stage, journal.Files[0].Payload)))
			switch kind {
			case "type":
				if err := os.Remove(payload); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(payload, 0o700); err != nil {
					t.Fatal(err)
				}
			case "size":
				if err := os.WriteFile(payload, []byte("short"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "hash":
				raw, err := os.ReadFile(payload)
				if err != nil {
					t.Fatal(err)
				}
				raw[0] ^= 1
				if err := os.WriteFile(payload, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "identity":
				var foreign os.FileInfo
				s.descriptorBarrier = func(operation string) error {
					if operation == "payload_restat" && foreign == nil {
						relative, err := filepath.Rel(root, payload)
						if err != nil {
							t.Fatal(err)
						}
						_, _, foreign = replaceRegularWithSameBytes(t, root, filepath.ToSlash(relative))
					}
					return nil
				}
			}

			// Act.
			recoverErr := s.recoverForTest(context.Background())
			var committed *store.CommittedError

			// Assert.
			if !errors.Is(recoverErr, store.ErrStorageCorrupt) || !errors.As(recoverErr, &committed) {
				t.Fatalf("recover=%v committed=%v", recoverErr, committed)
			}
		})
	}
}

func TestRecoveredReceiptSurvivesOrphanTailFailure(t *testing.T) {
	t.Run("Commit matching and unrelated projection", func(t *testing.T) {
		for _, matching := range []bool{true, false} {
			t.Run(map[bool]string{true: "matching", false: "unrelated"}[matching], func(t *testing.T) {
				// Arrange.
				root, s := adversarialStore(t, Config{})
				base := adversarialSnapshot(t, s)
				owner := adversarialChange(t, "tail-owner", base.Revision(), "depends_on")
				digest, err := owner.RequestDigest()
				if err != nil {
					t.Fatal(err)
				}
				next := nextSnapshotForDurabilityTest(t, s)
				receipt := testJournalReceipt(base.Revision(), next.Revision(), "tail-owner", "tail-owner")
				receipt.RequestDigest = digest
				stageRecoveryJournalWithReceipt(t, s, next, receipt, replaceReplay{})
				injected := errors.New("orphan tail cleanup failed")
				armOrphanTailFailure(t, root, s, injected)
				request, key := owner, store.IdempotencyKey("tail-owner")
				if !matching {
					request = adversarialChange(t, "tail-unrelated", base.Revision(), "blocks")
					key = "tail-unrelated"
				}

				// Act.
				got, commitErr := s.Commit(context.Background(), request, store.CommitOptions{IdempotencyKey: key})
				var committed *store.CommittedError

				// Assert.
				if !errors.Is(commitErr, injected) || !errors.As(commitErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
					t.Fatalf("commit=%v committed=%v", commitErr, committed)
				}
				if matching != reflect.DeepEqual(got, committed.Receipt()) || (!matching && !got.ResultRevision.IsZero()) {
					t.Fatalf("matching=%t public=%#v committed=%#v", matching, got, committed.Receipt())
				}
			})
		}
	})

	t.Run("Replace matching and unrelated projection", func(t *testing.T) {
		for _, matching := range []bool{true, false} {
			t.Run(map[bool]string{true: "matching", false: "unrelated"}[matching], func(t *testing.T) {
				// Arrange.
				root, s := adversarialStore(t, Config{})
				base := adversarialSnapshot(t, s)
				document := policyGateDocument(t, adversarialDocument("tail replacement"))
				owner := ReplaceConceptRequest{ChangeSetID: "tail-replace", Actor: "tester", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: document}
				serialized, err := document.Serialize()
				if err != nil {
					t.Fatal(err)
				}
				digest, err := replaceDigest(owner, serialized)
				if err != nil {
					t.Fatal(err)
				}
				next := nextSnapshotForDurabilityTest(t, s)
				receipt := testJournalReceipt(base.Revision(), next.Revision(), "tail-replace", "tail-replace")
				receipt.RequestDigest = digest
				wantReport := validator.Report{Diagnostics: []validator.Diagnostic{}, ScannedFiles: 2}
				stageRecoveryJournalWithReceipt(t, s, next, receipt, freezeValidation(wantReport))
				injected := errors.New("replace orphan tail cleanup failed")
				armOrphanTailFailure(t, root, s, injected)
				request, key := owner, store.IdempotencyKey("tail-replace")
				if !matching {
					request = ReplaceConceptRequest{ChangeSetID: "tail-unrelated", Actor: "tester", BaseRevision: base.Revision(), ConceptID: owner.ConceptID, Document: policyGateDocument(t, adversarialDocument("unrelated"))}
					key = "tail-unrelated"
				}

				// Act.
				result, replaceErr := s.ReplaceConcept(context.Background(), request, store.CommitOptions{IdempotencyKey: key})
				var committed *store.CommittedError

				// Assert.
				if !errors.Is(replaceErr, injected) || !errors.As(replaceErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
					t.Fatalf("replace=%v committed=%v", replaceErr, committed)
				}
				if matching {
					if !reflect.DeepEqual(result.Receipt, committed.Receipt()) || !reflect.DeepEqual(result.Validation, wantReport) {
						t.Fatalf("matching result=%#v committed=%#v", result, committed.Receipt())
					}
				} else if !result.Receipt.ResultRevision.IsZero() || result.Validation.Diagnostics != nil {
					t.Fatalf("unrelated result=%#v", result)
				}
			})
		}
	})
}

func armOrphanTailFailure(t *testing.T, root string, s *Store, injected error) {
	t.Helper()
	orphan := path.Join(internalDirectory, "staging", "txn-"+strings.Repeat("f", 64))
	key, err := newArtifactClaimKey("recovered-orphan-tail", orphan, claimStageDir)
	if err != nil {
		t.Fatal(err)
	}
	makeStageNamespaceDirectory(t, root, s, key, orphan)
	removeDirs := 0
	s.descriptorBarrier = func(operation string) error {
		if operation != "remove_dir" {
			return nil
		}
		removeDirs++
		if removeDirs == 3 {
			return injected
		}
		return nil
	}
}
