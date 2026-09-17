package fs

// Binding-only receipt authentication before repair.

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

	"github.com/skosovsky/okf/store"
)

func TestReceiptBindingOnlyAuthenticatesBeforeRepair(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutate  func(*testing.T, string, *Store, string, []byte) (string, []byte)
		corrupt bool
	}{
		{name: "fully rekeyed filename mismatch", mutate: func(t *testing.T, root string, s *Store, oldPath string, raw []byte) (string, []byte) {
			newPath := s.receiptPath(store.IdempotencyKey("r10-forged-key"))
			if err := os.Rename(filepath.Join(root, filepath.FromSlash(oldPath)), filepath.Join(root, filepath.FromSlash(newPath))); err != nil {
				t.Fatal(err)
			}
			return newPath, raw
		}},
		{name: "fully rekeyed envelope mismatch", corrupt: true, mutate: func(t *testing.T, root string, _ *Store, oldPath string, raw []byte) (string, []byte) {
			receipt, err := decodeReceiptFile(raw)
			if err != nil {
				t.Fatal(err)
			}
			receipt.Digest = "sha256:" + strings.Repeat("f", 64)
			forged, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			rewriteSameInode(t, root, oldPath, forged)
			return oldPath, forged
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: construct a binding-only state directly under the final
			// forged key. No witness or earlier legitimate key is ever created.
			root, s := adversarialStore(t, Config{})
			writeExpiredReceipts(t, s, 1)
			directory := filepath.Join(root, filepath.FromSlash(path.Join(internalDirectory, "receipts")))
			names := directoryNames(t, directory)
			if len(names) != 1 {
				t.Fatalf("receipts=%v", names)
			}
			oldPath := path.Join(internalDirectory, "receipts", names[0])
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(oldPath)))
			if err != nil {
				t.Fatal(err)
			}
			forgedPath, forgedRaw := test.mutate(t, root, s, oldPath, raw)
			identityInfo, err := os.Lstat(filepath.Join(root, filepath.FromSlash(forgedPath)))
			if err != nil {
				t.Fatal(err)
			}
			identity, ok := fileIdentityKey(identityInfo)
			if !ok {
				t.Fatal("receipt identity unavailable")
			}
			operation := receiptPruneOperation(forgedPath, forgedRaw)
			key, err := newArtifactClaimKey(operation, forgedPath, claimReceipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(claimDirectory)), 0o700); err != nil {
				t.Fatal(err)
			}
			record := directoryWitnessRecord{Version: 1, Operation: operation, Path: forgedPath, Role: claimReceipt, Identity: identity, Source: forgedPath}
			writeReceiptBinding(t, root, key, record)
			if path.Base(key.bindingPath()) != key.stem()+".binding.json" {
				t.Fatalf("binding basename=%q stem=%q", path.Base(key.bindingPath()), key.stem())
			}
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(key.witnessPath(false)))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected witness before authentication: %v", err)
			}
			before := receiptEvidence(t, root)
			var phases []string
			faultCalls := 0
			s.descriptorBarrier = func(phase string) error { phases = append(phases, phase); return nil }
			s.config.Fault = func(Step) error { faultCalls++; return nil }
			s.config.PostFault = func(Step) error { faultCalls++; return nil }

			// Act.
			cleanupErr := s.cleanupReceiptPruneInventory(context.Background())
			after := receiptEvidence(t, root)
			_, witnessErr := os.Lstat(filepath.Join(root, filepath.FromSlash(key.witnessPath(false))))

			// Assert.
			for _, phase := range phases {
				if !strings.HasPrefix(phase, "metadata_") && !strings.HasPrefix(phase, "payload_") {
					t.Fatalf("authentication reached repair/terminal phase %q", phase)
				}
			}
			if !errors.Is(cleanupErr, errArtifactClaimConflict) || errors.Is(cleanupErr, store.ErrStorageCorrupt) != test.corrupt || !errors.Is(witnessErr, os.ErrNotExist) || faultCalls != 0 || !reflect.DeepEqual(before, after) {
				t.Fatalf("cleanup=%v witness=%v faults=%d phases=%v state=%v/%v", cleanupErr, witnessErr, faultCalls, phases, before, after)
			}
		})
	}
}
