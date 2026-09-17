package fs

// Ownership validation and recovery at durability boundaries.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"hash"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestPreBoundaryCleanupOrdersJournalProofBeforeStage(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Store, string, string, error, error)
		wantPost  bool
	}{
		{
			name: "owned journal unlink fault",
			configure: func(s *Store, _, _ string, primary, cleanup error) {
				s.config.PostFault = func(step Step) error {
					if step == StepJournalRename {
						return primary
					}
					return nil
				}
				s.config.Fault = func(step Step) error {
					if step == StepRemove {
						return cleanup
					}
					return nil
				}
			},
			wantPost: true,
		},
		{
			name: "owned journal unlink postfault",
			configure: func(s *Store, _, _ string, primary, cleanup error) {
				s.config.PostFault = func(step Step) error {
					switch step {
					case StepJournalRename:
						return primary
					case StepRemove:
						return cleanup
					default:
						return nil
					}
				}
			},
		},
		{
			name: "transaction directory physical sync failure",
			configure: func(s *Store, _, _ string, primary, cleanup error) {
				s.config.PostFault = func(step Step) error {
					if step == StepJournalRename {
						return primary
					}
					return nil
				}
				physical := s.directorySync
				fired := false
				s.directorySync = func(store *Store, dir string) error {
					if !fired && dir == path.Join(internalDirectory, "transactions") {
						fired = true
						return cleanup
					}
					return physical(store, dir)
				}
			},
		},
		{
			name: "transaction directory sync fault",
			configure: func(s *Store, _, _ string, primary, cleanup error) {
				s.config.PostFault = func(step Step) error {
					if step == StepJournalRename {
						return primary
					}
					return nil
				}
				s.config.Fault = func(step Step) error {
					if step == StepJournalDirectorySync {
						return cleanup
					}
					return nil
				}
			},
		},
		{
			name: "transaction directory sync postfault",
			configure: func(s *Store, _, _ string, primary, cleanup error) {
				s.config.PostFault = func(step Step) error {
					switch step {
					case StepJournalRename:
						return primary
					case StepJournalDirectorySync:
						return cleanup
					default:
						return nil
					}
				}
			},
		},
		{
			name: "foreign journal replacement",
			configure: func(s *Store, root, journalName string, _, _ error) {
				fired := false
				s.config.DirectorySync = func(dir string) error {
					if fired || dir != path.Join(internalDirectory, "transactions") {
						return nil
					}
					fired = true
					replaceRegularWithSameBytes(t, root, journalName)
					return nil
				}
			},
			wantPost: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := nextSnapshotForDurabilityTest(t, s)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "cleanup-order-"+test.name, "")
			journalName, err := journalPath(receipt.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}
			stageName, err := journalStage(receipt.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}
			primary := errors.New("stop before journal durability")
			cleanup := errors.New("cleanup boundary failure")
			test.configure(s, root, journalName, primary, cleanup)

			// Act.
			publishErr := s.publish(context.Background(), next, receipt)
			var foreignRaw []byte
			var foreignInfo os.FileInfo
			if test.name == "foreign journal replacement" {
				foreignRaw, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(journalName)))
				if err != nil {
					t.Fatal(err)
				}
				foreignInfo, err = os.Stat(filepath.Join(root, filepath.FromSlash(journalName)))
				if err != nil {
					t.Fatal(err)
				}
			}
			_, stageErr := os.Stat(filepath.Join(root, filepath.FromSlash(stageName)))
			s.config.Fault = nil
			s.config.PostFault = nil
			s.config.DirectorySync = nil
			retryErr := s.recoverForTest(context.Background())
			if retryErr == nil && adversarialSnapshot(t, s).Revision() != next.Revision() {
				retryErr = s.publish(context.Background(), next, receipt)
			}
			var post store.Snapshot
			if test.name != "foreign journal replacement" {
				post = adversarialSnapshot(t, s)
			}

			// Assert.
			if publishErr == nil || stageErr != nil {
				t.Fatalf("publish=%v stage=%v, want failed cleanup with retained stage", publishErr, stageErr)
			}
			if test.name == "foreign journal replacement" {
				secondErr := s.recoverForTest(context.Background())
				afterRaw, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(journalName)))
				afterInfo, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(journalName)))
				_, stageAfterErr := os.Stat(filepath.Join(root, filepath.FromSlash(stageName)))
				var committed *store.CommittedError
				visible, visibleErr := readVisibleRoot(context.Background(), s.rootFD)
				if !errors.Is(publishErr, store.ErrStorageCorrupt) || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest || !errors.Is(retryErr, errArtifactClaimConflict) || !errors.Is(secondErr, errArtifactClaimConflict) || readErr != nil || statErr != nil || foreignInfo == nil || !os.SameFile(foreignInfo, afterInfo) || foreignInfo.Mode() != afterInfo.Mode() || !bytes.Equal(foreignRaw, afterRaw) || stageAfterErr != nil || visibleErr != nil || !sameVisibleFiles(visible, base.(*snapshot).files) {
					t.Fatalf("foreign conflict publish=%v committed=%v first=%v second=%v read=%v stat=%v same=%t mode=%v/%v bytes=%t stage=%v visible=%v", publishErr, committed, retryErr, secondErr, readErr, statErr, foreignInfo != nil && afterInfo != nil && os.SameFile(foreignInfo, afterInfo), modeOf(foreignInfo), modeOf(afterInfo), bytes.Equal(foreignRaw, afterRaw), stageAfterErr, visibleErr)
				}
				return
			}
			if test.name != "foreign journal replacement" && !errors.Is(publishErr, primary) {
				t.Fatalf("publish error=%v want primary", publishErr)
			}
			if test.name != "foreign journal replacement" && !errors.Is(publishErr, cleanup) {
				t.Fatalf("publish error=%v want cleanup cause", publishErr)
			}
			if retryErr != nil || (test.wantPost && post.Revision() != next.Revision()) {
				t.Fatalf("retry=%v revision=%s want post=%t (%s)", retryErr, post.Revision(), test.wantPost, next.Revision())
			}
		})
	}
}

func TestOwnedRemoveRevalidatesAfterDescriptorBarrier(t *testing.T) {
	tests := []struct {
		name string
		dir  bool
	}{
		{name: "journal"},
		{name: "payload"},
		{name: "scratch"},
		{name: "stage directory", dir: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			relative := path.Join(claimDirectory, "owned-remove", test.name)
			absolute := filepath.Join(root, filepath.FromSlash(relative))
			if test.dir {
				if err := os.MkdirAll(absolute, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(absolute, []byte("owned"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			owned, err := os.Stat(absolute)
			if err != nil {
				t.Fatal(err)
			}
			var foreign os.FileInfo
			s.descriptorBarrier = func(operation string) error {
				if operation != map[bool]string{false: "remove", true: "remove_dir"}[test.dir] || foreign != nil {
					return nil
				}
				pinned, err := os.Open(absolute)
				if err != nil {
					t.Fatal(err)
				}
				defer pinned.Close()
				if err := os.Remove(absolute); err != nil {
					t.Fatal(err)
				}
				if test.dir {
					if err := os.Mkdir(absolute, owned.Mode().Perm()); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(absolute, []byte("owned"), owned.Mode().Perm()); err != nil {
					t.Fatal(err)
				}
				foreign, err = os.Stat(absolute)
				return err
			}

			// Act.
			removed, removeErr := s.fdRemoveClaimOwned(claimZonePath(relative), owned, test.dir)
			after, statErr := os.Stat(absolute)

			// Assert.
			if removeErr != nil || removed || foreign == nil || statErr != nil || !os.SameFile(foreign, after) || after.Mode() != foreign.Mode() {
				t.Fatalf("removed=%t error=%v foreign=%v stat=%v same=%t", removed, removeErr, foreign, statErr, foreign != nil && after != nil && os.SameFile(foreign, after))
			}
		})
	}
}

func TestActiveOwnershipRejectsForeignJournalAndPayload(t *testing.T) {
	for _, artifact := range []string{"journal", "payload"} {
		t.Run(artifact, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := nextSnapshotForDurabilityTest(t, s)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "active-owned-"+artifact, "")
			journalName, _ := journalPath(receipt.RequestDigest)
			stageName, _ := journalStage(receipt.RequestDigest)
			fired := false
			s.config.PostFault = func(step Step) error {
				if fired || step != StepJournalWrite {
					return nil
				}
				fired = true
				target := journalName
				if artifact == "payload" {
					target = path.Join(stageName, payloadName(0))
				}
				replaceRegularWithSameBytes(t, root, target)
				return nil
			}

			// Act.
			publishErr := s.publish(context.Background(), next, receipt)
			raw, rawErr := readVisibleRoot(context.Background(), s.rootFD)

			// Assert.
			var committed *store.CommittedError
			if !fired || !errors.As(publishErr, &committed) || !reflect.DeepEqual(committed.Receipt(), receipt) {
				t.Fatalf("publish=%v fired=%t committed=%v", publishErr, fired, committed)
			}
			if rawErr != nil || !sameVisibleFiles(raw, base.(*snapshot).files) {
				t.Fatalf("visible changed: read=%v files=%v", rawErr, sortedFilePaths(raw))
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(journalName))); err != nil {
				t.Fatalf("journal evidence removed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(stageName))); err != nil {
				t.Fatalf("stage evidence removed: %v", err)
			}
		})
	}
}

type cancelOnProofHash struct {
	armed  *atomic.Bool
	seen   *atomic.Bool
	cancel context.CancelFunc
}

func (a cancelOnProofHash) Name() string   { return "sha256" }
func (a cancelOnProofHash) New() hash.Hash { return sha256.New() }
func (a cancelOnProofHash) NewContext(ctx context.Context) (hash.Hash, error) {
	if a.armed.Load() {
		if ctx.Value(durabilityContextKey("proof")) == "retained" {
			a.seen.Store(true)
		}
		a.cancel()
		return nil, ctx.Err()
	}
	return sha256.New(), nil
}

func TestPreDurableContextPropagation(t *testing.T) {
	t.Run("prepare hashing", func(t *testing.T) {
		// Arrange.
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), durabilityContextKey("proof"), "retained"))
		var armed, seen atomic.Bool
		_, s := adversarialStore(t, Config{HashAlgorithm: cancelOnProofHash{armed: &armed, seen: &seen, cancel: cancel}})
		base := adversarialSnapshot(t, s)
		next := nextSnapshotForDurabilityTest(t, s)
		receipt := testJournalReceipt(base.Revision(), next.Revision(), "ctx-prepare", "")
		armed.Store(true)

		// Act.
		_, _, err := s.prepareJournalContext(ctx, next, receipt, replaceReplay{}, productionJournalManifestByteLimits())

		// Assert.
		if !errors.Is(err, context.Canceled) || !seen.Load() {
			t.Fatalf("prepare error=%v value seen=%t", err, seen.Load())
		}
	})

	t.Run("chunked payload staging", func(t *testing.T) {
		// Arrange.
		root, s := adversarialStore(t, Config{})
		base := adversarialSnapshot(t, s)
		body := bytes.Repeat([]byte("x"), 200<<10)
		next, err := newSnapshot(context.Background(), map[string][]byte{"a.md": append([]byte("---\ntype: Note\n---\n\n"), body...)})
		if err != nil {
			t.Fatal(err)
		}
		receipt := testJournalReceipt(base.Revision(), next.Revision(), "ctx-chunk", "")
		ctx, cancel := context.WithCancel(context.Background())
		write := s.write
		calls := 0
		s.write = func(file *os.File, data []byte) (int, error) {
			n, err := write(file, data)
			calls++
			if calls == 1 {
				cancel()
			}
			return n, err
		}

		// Act.
		publishErr := s.publish(ctx, next, receipt)
		journalName, _ := journalPath(receipt.RequestDigest)
		stageName, _ := journalStage(receipt.RequestDigest)
		_, journalErr := os.Stat(filepath.Join(root, filepath.FromSlash(journalName)))
		_, stageErr := os.Stat(filepath.Join(root, filepath.FromSlash(stageName)))

		// Assert.
		var committed *store.CommittedError
		if !errors.Is(publishErr, context.Canceled) || errors.As(publishErr, &committed) || calls == 0 || !errors.Is(journalErr, os.ErrNotExist) || !errors.Is(stageErr, os.ErrNotExist) {
			t.Fatalf("publish=%v committed=%v calls=%d journal=%v stage=%v", publishErr, committed, calls, journalErr, stageErr)
		}
		entries, readErr := os.ReadDir(filepath.Join(root, filepath.FromSlash(claimDirectory)))
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("canceled staging claim residue: read=%v entries=%v", readErr, entries)
		}
	})
}

func TestRecoveryClassificationAndReceiptProjection(t *testing.T) {
	t.Run("operational recovery failure remains exact and receipt-bearing", func(t *testing.T) {
		root, s, _, next := stageRecoveryJournal(t, Config{})
		injected := errors.New("recovery write failed")
		fired := false
		s.config.Fault = func(step Step) error {
			if !fired && step == StepFileWrite {
				fired = true
				return injected
			}
			return nil
		}

		// Act.
		recoverErr := s.recoverForTest(context.Background())

		// Assert.
		var committed *store.CommittedError
		if !fired || !errors.Is(recoverErr, injected) || errors.Is(recoverErr, store.ErrStorageCorrupt) || !errors.As(recoverErr, &committed) || committed.Receipt().ResultRevision != next.Revision() {
			t.Fatalf("recover=%v fired=%t committed=%v", recoverErr, fired, committed)
		}
		if journals := pendingJournalNames(t, root); len(journals) != 1 {
			t.Fatalf("journals=%v want retained evidence", journals)
		}
	})

	t.Run("payload tamper is corruption with receipt", func(t *testing.T) {
		root, s, _, _ := stageRecoveryJournal(t, Config{})
		journal := readOnlyPendingJournal(t, root)
		payload := filepath.Join(root, filepath.FromSlash(path.Join(journal.Stage, journal.Files[0].Payload)))
		if err := os.WriteFile(payload, []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}

		// Act.
		recoverErr := s.recoverForTest(context.Background())
		var committed *store.CommittedError

		// Assert.
		if !errors.Is(recoverErr, store.ErrStorageCorrupt) || !errors.As(recoverErr, &committed) {
			t.Fatalf("recover=%v committed=%v, want receipt-bearing corruption", recoverErr, committed)
		}
		if journals := pendingJournalNames(t, root); len(journals) != 1 {
			t.Fatalf("journals=%v want retained evidence", journals)
		}
	})

	t.Run("Commit projects only matching recovery receipt", func(t *testing.T) {
		for _, matching := range []bool{true, false} {
			t.Run(map[bool]string{true: "matching", false: "unrelated"}[matching], func(t *testing.T) {
				// Arrange.
				_, s := adversarialStore(t, Config{})
				base := adversarialSnapshot(t, s)
				owner := adversarialChange(t, "recovery-owner", base.Revision(), "depends_on")
				ownerDigest, err := owner.RequestDigest()
				if err != nil {
					t.Fatal(err)
				}
				next := nextSnapshotForDurabilityTest(t, s)
				receipt := testJournalReceipt(base.Revision(), next.Revision(), "recovery-owner", "recovery-owner")
				receipt.RequestDigest = ownerDigest
				stageRecoveryJournalWithReceipt(t, s, next, receipt, replaceReplay{})
				injected := errors.New("recovery apply failed")
				s.config.Fault = func(step Step) error {
					if step == StepFileWrite {
						return injected
					}
					return nil
				}
				request := owner
				key := store.IdempotencyKey("recovery-owner")
				if !matching {
					request = adversarialChange(t, "unrelated", base.Revision(), "blocks")
					key = "unrelated"
				}

				// Act.
				got, commitErr := s.Commit(context.Background(), request, store.CommitOptions{IdempotencyKey: key})
				var committed *store.CommittedError

				// Assert.
				if !errors.Is(commitErr, injected) || !errors.As(commitErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest || committed.Receipt().IdempotencyKey != receipt.IdempotencyKey {
					t.Fatalf("commit=%v committed=%v receipt=%#v", commitErr, committed, committed.Receipt())
				}
				if reflect.DeepEqual(got, committed.Receipt()) != matching || (!matching && !got.ResultRevision.IsZero()) {
					t.Fatalf("public receipt=%#v matching=%t want committed=%#v", got, matching, committed.Receipt())
				}
			})
		}
	})

	t.Run("Replace projects matching receipt and validation", func(t *testing.T) {
		// Arrange.
		_, s := adversarialStore(t, Config{})
		base := adversarialSnapshot(t, s)
		document := policyGateDocument(t, adversarialDocument("replacement"))
		request := ReplaceConceptRequest{ChangeSetID: "replace-recovery", Actor: "tester", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: document}
		serialized, err := document.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		digest, err := replaceDigest(request, serialized)
		if err != nil {
			t.Fatal(err)
		}
		next := nextSnapshotForDurabilityTest(t, s)
		receipt := testJournalReceipt(base.Revision(), next.Revision(), "replace-recovery", "replace-recovery")
		receipt.RequestDigest = digest
		wantReport := validator.Report{Diagnostics: []validator.Diagnostic{}, ScannedFiles: 2}
		replay := freezeValidation(wantReport)
		stageRecoveryJournalWithReceipt(t, s, next, receipt, replay)
		injected := errors.New("replace recovery failed")
		s.config.Fault = func(step Step) error {
			if step == StepFileWrite {
				return injected
			}
			return nil
		}

		// Act.
		result, replaceErr := s.ReplaceConcept(context.Background(), request, store.CommitOptions{IdempotencyKey: "replace-recovery"})
		var committed *store.CommittedError

		// Assert.
		if !errors.Is(replaceErr, injected) || !errors.As(replaceErr, &committed) || !reflect.DeepEqual(result.Receipt, committed.Receipt()) || !reflect.DeepEqual(result.Validation, wantReport) {
			t.Fatalf("result=%#v error=%v want receipt=%#v report=%#v", result, replaceErr, receipt, wantReport)
		}
	})

	t.Run("Replace suppresses unrelated recovery projection", func(t *testing.T) {
		// Arrange.
		_, s := adversarialStore(t, Config{})
		base := adversarialSnapshot(t, s)
		ownerDocument := policyGateDocument(t, adversarialDocument("owner"))
		owner := ReplaceConceptRequest{ChangeSetID: "replace-owner", Actor: "tester", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: ownerDocument}
		serialized, err := ownerDocument.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		digest, err := replaceDigest(owner, serialized)
		if err != nil {
			t.Fatal(err)
		}
		next := nextSnapshotForDurabilityTest(t, s)
		receipt := testJournalReceipt(base.Revision(), next.Revision(), "replace-owner", "replace-owner")
		receipt.RequestDigest = digest
		stageRecoveryJournalWithReceipt(t, s, next, receipt, freezeValidation(validator.Report{Diagnostics: []validator.Diagnostic{}, ScannedFiles: 2}))
		injected := errors.New("unrelated replace recovery failed")
		s.config.Fault = func(step Step) error {
			if step == StepFileWrite {
				return injected
			}
			return nil
		}
		unrelated := ReplaceConceptRequest{ChangeSetID: "replace-unrelated", Actor: "tester", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: policyGateDocument(t, adversarialDocument("unrelated"))}

		// Act.
		result, replaceErr := s.ReplaceConcept(context.Background(), unrelated, store.CommitOptions{IdempotencyKey: "replace-unrelated"})
		var committed *store.CommittedError

		// Assert.
		if !errors.Is(replaceErr, injected) || !errors.As(replaceErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest || !result.Receipt.ResultRevision.IsZero() || result.Validation.Diagnostics != nil {
			t.Fatalf("result=%#v error=%v committed=%v", result, replaceErr, committed)
		}
	})
}

func TestAtomicRenameGuardsScratchSourceAfterBarrier(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	target := path.Join(internalDirectory, "transactions", "guarded-source.json")
	var foreignName string
	var foreignInfo os.FileInfo
	s.descriptorBarrier = func(operation string) error {
		if operation != "rename" || foreignInfo != nil {
			return nil
		}
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(temporaryDirectory)))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !bytes.HasPrefix([]byte(entry.Name()), []byte(".okf-tmp-")) {
				continue
			}
			foreignName, _, foreignInfo = replaceRegularWithSameBytes(t, root, path.Join(temporaryDirectory, entry.Name()))
			return nil
		}
		t.Fatal("scratch source not found")
		return nil
	}

	// Act.
	writeErr := s.writeDurableAtForTest(context.Background(), target, []byte("journal"), 0o600)
	after, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(foreignName)))
	_, targetErr := os.Stat(filepath.Join(root, filepath.FromSlash(target)))

	// Assert.
	if writeErr == nil || foreignInfo == nil || statErr != nil || !os.SameFile(foreignInfo, after) || !errors.Is(targetErr, os.ErrNotExist) {
		t.Fatalf("write=%v foreign=%v stat=%v target=%v", writeErr, foreignInfo, statErr, targetErr)
	}
}
