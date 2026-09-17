package fs

// Artifact-role validation and receipt authentication.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/store"
)

func TestForgedPrivateRoleProofIsInert(t *testing.T) {
	operation := "sha256:" + strings.Repeat("8", 64)
	otherOperation := "sha256:" + strings.Repeat("9", 64)
	journalName, _ := journalPath(operation)
	payloadDir, _ := journalStage(operation)
	otherPayloadDir, _ := journalStage(otherOperation)
	tests := []struct {
		name   string
		role   artifactRole
		target string
		mutate func(*directoryWitnessRecord)
	}{
		{name: "journal path", role: claimJournal, target: journalName, mutate: func(r *directoryWitnessRecord) { r.Path, _ = journalPath(otherOperation) }},
		{name: "journal source", role: claimJournal, target: journalName, mutate: func(r *directoryWitnessRecord) { r.Source = path.Join(temporaryDirectory, ".okf-tmp-forged-0") }},
		{name: "payload directory", role: claimPayload, target: path.Join(payloadDir, "payload-00000"), mutate: func(r *directoryWitnessRecord) { r.Path = path.Join(otherPayloadDir, "payload-00000") }},
		{name: "payload basename", role: claimPayload, target: path.Join(payloadDir, "payload-00000"), mutate: func(r *directoryWitnessRecord) { r.Path = path.Join(payloadDir, "payload-99999-extra") }},
		{name: "payload source", role: claimPayload, target: path.Join(payloadDir, "payload-00000"), mutate: func(r *directoryWitnessRecord) { r.Source = path.Join(temporaryDirectory, ".okf-tmp-forged-0") }},
		{name: "scratch source", role: claimScratch, target: path.Join(temporaryDirectory, ".okf-tmp-800000006-6"), mutate: func(r *directoryWitnessRecord) { r.Source = path.Join(temporaryDirectory, ".okf-tmp-800000007-7") }},
		{name: "scratch path", role: claimScratch, target: path.Join(temporaryDirectory, ".okf-tmp-800000008-8"), mutate: func(r *directoryWitnessRecord) {
			r.Path, r.Source = path.Join(temporaryDirectory, "foreign"), path.Join(temporaryDirectory, "foreign")
		}},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			source := path.Join(temporaryDirectory, ".okf-tmp-80000000"+string(rune('0'+i))+"-0")
			if test.role == claimScratch {
				source = test.target
			}
			absoluteSource := filepath.Join(root, filepath.FromSlash(source))
			if err := os.MkdirAll(filepath.Dir(absoluteSource), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absoluteSource, []byte("owned role proof"), 0o640); err != nil {
				t.Fatal(err)
			}
			owned, err := os.Lstat(absoluteSource)
			if err != nil {
				t.Fatal(err)
			}
			key, err := newArtifactClaimKey(operation, test.target, test.role)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.prepareRegularWitnessFrom(context.Background(), key, source, owned); err != nil {
				t.Fatal(err)
			}
			if test.role != claimScratch {
				absoluteTarget := filepath.Join(root, filepath.FromSlash(test.target))
				if err := os.MkdirAll(filepath.Dir(absoluteTarget), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(absoluteSource, absoluteTarget); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(key.bindingPath())))
			if err != nil {
				t.Fatal(err)
			}
			var record directoryWitnessRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			test.mutate(&record)
			forged, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(key.bindingPath())), forged, 0o600); err != nil {
				t.Fatal(err)
			}
			paths := []string{source, test.target, key.witnessPath(false), key.bindingPath()}
			before := exactFileStates(t, root, paths)
			inventoryBefore := claimInventory(t, root)

			// Act.
			var cleanupErr error
			if test.role == claimScratch {
				cleanupErr = s.cleanupScratchInventory(context.Background())
			} else {
				cleanupErr = s.compensateUndurableRegularBindings(context.Background())
			}
			after := exactFileStates(t, root, paths)
			inventoryAfter := claimInventory(t, root)

			// Assert.
			if !errors.Is(cleanupErr, errArtifactClaimConflict) {
				t.Fatalf("cleanup error = %v, want artifact claim conflict", cleanupErr)
			}
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(inventoryBefore, inventoryAfter) {
				t.Fatalf("forged proof mutated state: files=%v/%v inventory=%v/%v", before, after, inventoryBefore, inventoryAfter)
			}
		})
	}
}

func TestReceiptRecoveryAuthenticatesBeforeRepair(t *testing.T) {
	for _, test := range []struct {
		name        string
		mutate      func(*testing.T, string, string, *directoryWitnessRecord, *[]byte, *artifactClaimKey)
		operational error
		cancel      bool
		corrupt     bool
	}{
		{name: "forged operation", mutate: func(t *testing.T, root, _ string, record *directoryWitnessRecord, _ *[]byte, key *artifactClaimKey) {
			record.Operation = "receipt:" + strings.Repeat("f", 64)
			rehomeReceiptProof(t, root, *key, record)
			*key, _ = newArtifactClaimKey(record.Operation, record.Path, record.Role)
		}},
		{name: "filename mismatch", mutate: func(t *testing.T, root, relative string, record *directoryWitnessRecord, raw *[]byte, key *artifactClaimKey) {
			receipt, err := decodeReceiptFile(*raw)
			if err != nil {
				t.Fatal(err)
			}
			receipt.Key = "r8-other-key"
			receipt.Receipt.IdempotencyKey = store.IdempotencyKey(receipt.Key)
			*raw, _ = json.Marshal(receipt)
			rewriteSameInode(t, root, relative, *raw)
			record.Operation = receiptPruneOperation(relative, *raw)
			rehomeReceiptProof(t, root, *key, record)
			*key, _ = newArtifactClaimKey(record.Operation, record.Path, record.Role)
		}},
		{name: "envelope mismatch", corrupt: true, mutate: func(t *testing.T, root, relative string, record *directoryWitnessRecord, raw *[]byte, key *artifactClaimKey) {
			receipt, err := decodeReceiptFile(*raw)
			if err != nil {
				t.Fatal(err)
			}
			receipt.Digest = "sha256:" + strings.Repeat("f", 64)
			*raw, _ = json.Marshal(receipt)
			rewriteSameInode(t, root, relative, *raw)
			record.Operation = receiptPruneOperation(relative, *raw)
			rehomeReceiptProof(t, root, *key, record)
			*key, _ = newArtifactClaimKey(record.Operation, record.Path, record.Role)
		}},
		{name: "binding source mismatch", mutate: func(t *testing.T, root, _ string, record *directoryWitnessRecord, _ *[]byte, key *artifactClaimKey) {
			record.Source = path.Join(internalDirectory, "receipts", "foreign.json")
			writeReceiptBinding(t, root, *key, *record)
		}},
		{name: "foreign inode", mutate: func(t *testing.T, root, relative string, _ *directoryWitnessRecord, _ *[]byte, _ *artifactClaimKey) {
			replaceRegularWithSameBytes(t, root, relative)
		}},
		{name: "missing byte-bearing source", mutate: func(t *testing.T, root, relative string, _ *directoryWitnessRecord, _ *[]byte, _ *artifactClaimKey) {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "raw EIO", operational: errors.New("receipt raw EIO")},
		{name: "cancellation", cancel: true, operational: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			s.config.MinimumReceipts = 0
			s.config.ReceiptRetention = time.Nanosecond
			writeExpiredReceipts(t, s, 1)
			receiptDir := filepath.Join(root, filepath.FromSlash(path.Join(internalDirectory, "receipts")))
			names := directoryNames(t, receiptDir)
			if len(names) != 1 {
				t.Fatalf("receipts = %v", names)
			}
			relative := path.Join(internalDirectory, "receipts", names[0])
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
			if err != nil {
				t.Fatal(err)
			}
			owned, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
			if err != nil {
				t.Fatal(err)
			}
			key, err := newArtifactClaimKey(receiptPruneOperation(relative, raw), relative, claimReceipt)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.prepareRegularWitnessFrom(context.Background(), key, relative, owned); err != nil {
				t.Fatal(err)
			}
			bindingRaw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(key.bindingPath())))
			if err != nil {
				t.Fatal(err)
			}
			var record directoryWitnessRecord
			if err := json.Unmarshal(bindingRaw, &record); err != nil {
				t.Fatal(err)
			}
			if test.mutate != nil {
				test.mutate(t, root, relative, &record, &raw, &key)
			}
			ctx := context.Background()
			if test.operational != nil {
				if test.cancel {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					s.descriptorBarrier = func(phase string) error {
						if phase == "payload_read" {
							cancel()
						}
						return nil
					}
				} else {
					s.descriptorBarrier = func(phase string) error {
						if phase == "payload_read" {
							return test.operational
						}
						return nil
					}
				}
			}
			before := receiptEvidence(t, root)

			// Act.
			cleanupErr := s.cleanupReceiptPruneInventory(ctx)
			after := receiptEvidence(t, root)

			// Assert.
			if test.operational != nil {
				if !errors.Is(cleanupErr, test.operational) || errors.Is(cleanupErr, store.ErrStorageCorrupt) {
					t.Fatalf("cleanup error = %v, want operational %v", cleanupErr, test.operational)
				}
			} else if !errors.Is(cleanupErr, errArtifactClaimConflict) || errors.Is(cleanupErr, store.ErrStorageCorrupt) != test.corrupt {
				t.Fatalf("cleanup error = %v, want conflict corrupt=%t", cleanupErr, test.corrupt)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("receipt authentication mutated evidence: before=%v after=%v", before, after)
			}
		})
	}
}
