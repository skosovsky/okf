package fs

// Independent receipt-binding field authentication.

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
)

func TestReceiptBindingFieldsAuthenticateIndependently(t *testing.T) {
	for _, test := range []struct {
		name            string
		forge           func(*testing.T, string, string, []byte, os.FileInfo, *directoryWitnessRecord)
		wantPayloadRead bool
		preserveAlias   bool
	}{
		{name: "Path", preserveAlias: true, forge: func(t *testing.T, root, _ string, _ []byte, _ os.FileInfo, record *directoryWitnessRecord) {
			alias := "visible-receipt-alias.json"
			if err := os.Link(filepath.Join(root, filepath.FromSlash(record.Path)), filepath.Join(root, alias)); err != nil {
				t.Fatal(err)
			}
			record.Path = alias
		}},
		{name: "PathCoherentRekey", preserveAlias: true, forge: func(t *testing.T, root, _ string, raw []byte, info os.FileInfo, record *directoryWitnessRecord) {
			alias := "visible-receipt-alias.json"
			if err := os.Link(filepath.Join(root, filepath.FromSlash(record.Path)), filepath.Join(root, alias)); err != nil {
				t.Fatal(err)
			}
			record.Path = alias
			record.Source = alias
			record.Operation = receiptPruneOperation(alias, raw)
			identity, _ := fileIdentityKey(info)
			record.Identity = identity
		}},
		{name: "Source", forge: func(_ *testing.T, _ string, _ string, _ []byte, _ os.FileInfo, record *directoryWitnessRecord) {
			record.Source = path.Join(internalDirectory, "receipts", "forged-source.json")
		}},
		{name: "Operation", wantPayloadRead: true, forge: func(_ *testing.T, _ string, _ string, _ []byte, _ os.FileInfo, record *directoryWitnessRecord) {
			record.Operation = "receipt:" + strings.Repeat("e", 64)
		}},
		{name: "Identity", forge: func(_ *testing.T, _ string, _ string, _ []byte, _ os.FileInfo, record *directoryWitnessRecord) {
			record.Identity = "forged-identity"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: every row creates only its final forged key and binding.
			// There is no witness and no earlier legitimate proof inventory.
			root, s := adversarialStore(t, Config{})
			writeExpiredReceipts(t, s, 1)
			receiptDir := filepath.Join(root, filepath.FromSlash(path.Join(internalDirectory, "receipts")))
			names := directoryNames(t, receiptDir)
			if len(names) != 1 {
				t.Fatalf("receipts=%v", names)
			}
			receiptPath := path.Join(internalDirectory, "receipts", names[0])
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(receiptPath)))
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(receiptPath)))
			if err != nil {
				t.Fatal(err)
			}
			identity, _ := fileIdentityKey(info)
			record := directoryWitnessRecord{
				Version:   1,
				Operation: receiptPruneOperation(receiptPath, raw),
				Path:      receiptPath,
				Role:      claimReceipt,
				Identity:  identity,
				Source:    receiptPath,
			}
			test.forge(t, root, receiptPath, raw, info, &record)
			key, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(claimDirectory)), 0o700); err != nil {
				t.Fatal(err)
			}
			writeReceiptBinding(t, root, key, record)
			if path.Base(key.bindingPath()) != key.stem()+".binding.json" {
				t.Fatalf("binding basename=%q stem=%q", path.Base(key.bindingPath()), key.stem())
			}
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(key.witnessPath(false)))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected witness before authentication: %v", err)
			}
			before := receiptEvidence(t, root)
			var aliasBefore map[string]exactFileState
			if test.preserveAlias {
				aliasBefore = exactFileStates(t, root, []string{record.Path})
			}
			var phases []string
			faultCalls := 0
			mutationCallbacks := 0
			s.descriptorBarrier = func(phase string) error {
				phases = append(phases, phase)
				if !strings.HasPrefix(phase, "metadata_") && !strings.HasPrefix(phase, "payload_") {
					mutationCallbacks++
				}
				return nil
			}
			s.config.Fault = func(Step) error { faultCalls++; return nil }
			s.config.PostFault = func(Step) error { faultCalls++; return nil }

			// Act.
			cleanupErr := s.cleanupReceiptPruneInventory(context.Background())
			after := receiptEvidence(t, root)
			_, witnessErr := os.Lstat(filepath.Join(root, filepath.FromSlash(key.witnessPath(false))))
			var aliasAfter map[string]exactFileState
			if test.preserveAlias {
				aliasAfter = exactFileStates(t, root, []string{record.Path})
			}

			// Assert: metadata phases read the binding; only Operation reaches the
			// authenticated byte read. No row may enter repair or terminal phases.
			payloadReads := 0
			for _, phase := range phases {
				switch {
				case strings.HasPrefix(phase, "metadata_"):
				case strings.HasPrefix(phase, "payload_"):
					payloadReads++
				default:
					t.Fatalf("field %s reached repair/consume phase %q", test.name, phase)
				}
			}
			if cleanupErr != errArtifactClaimConflict || errors.Is(cleanupErr, store.ErrStorageCorrupt) || !errors.Is(witnessErr, os.ErrNotExist) || faultCalls != 0 || mutationCallbacks != 0 || (payloadReads > 0) != test.wantPayloadRead || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(aliasBefore, aliasAfter) {
				t.Fatalf("cleanup=%v witness=%v faults=%d mutations=%d phases=%v state=%v/%v alias=%v/%v", cleanupErr, witnessErr, faultCalls, mutationCallbacks, phases, before, after, aliasBefore, aliasAfter)
			}
		})
	}
}
