package fs

// Visible-artifact proof requirements during recovery.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestVisibleRecoveryRequiresClosedSourceProof(t *testing.T) {
	for _, role := range []artifactRole{claimVisible, claimVisibleInstall} {
		for _, test := range []struct {
			damage        string
			postOwnership bool
		}{
			{damage: "wrong-source", postOwnership: true},
			{damage: "unsafe-source"},
			{damage: "binding-only", postOwnership: true},
			{damage: "witness-only", postOwnership: role == claimVisibleInstall},
			{damage: "identity-mismatch", postOwnership: true},
		} {
			t.Run(string(role)+"/"+test.damage, func(t *testing.T) {
				t.Parallel()

				// Arrange: stop a real publication after the new target is installed.
				root, s := adversarialStore(t, Config{})
				base := adversarialSnapshot(t, s)
				next := nextSnapshotForDurabilityTest(t, s)
				receipt := testJournalReceipt(base.Revision(), next.Revision(), "r7-visible-proof", "")
				stop := errors.New("retain live visible proof")
				s.descriptorBarrier = func(phase string) error {
					if phase == "visible_source_installed:a.md" {
						return stop
					}
					return nil
				}
				if err := s.publish(context.Background(), next, receipt); !errors.Is(err, stop) {
					t.Fatalf("publish=%v", err)
				}
				s.descriptorBarrier = nil
				key, err := newArtifactClaimKey(receipt.RequestDigest, "a.md", role)
				if err != nil {
					t.Fatal(err)
				}
				bindingName := filepath.Join(root, filepath.FromSlash(key.bindingPath()))
				witnessName := filepath.Join(root, filepath.FromSlash(key.witnessPath(false)))
				switch test.damage {
				case "binding-only":
					if err := os.Remove(witnessName); err != nil {
						t.Fatal(err)
					}
				case "witness-only":
					if err := os.Remove(bindingName); err != nil {
						t.Fatal(err)
					}
				default:
					raw, err := os.ReadFile(bindingName)
					if err != nil {
						t.Fatal(err)
					}
					record, err := decodeDirectoryWitness(raw)
					if err != nil {
						t.Fatal(err)
					}
					switch test.damage {
					case "wrong-source":
						record.Source = "wrong.md"
					case "unsafe-source":
						record.Source = "../outside"
					case "identity-mismatch":
						record.Identity = "different-inode"
					}
					raw, _ = json.Marshal(record)
					if err := os.WriteFile(bindingName, raw, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				inventoryBefore := claimInventory(t, root)
				targetName := filepath.Join(root, "a.md")
				targetBefore, err := os.Stat(targetName)
				if err != nil {
					t.Fatal(err)
				}
				targetBytes, _ := os.ReadFile(targetName)

				// Act.
				_, recoveryErr := s.Snapshot(context.Background())
				targetAfter, statErr := os.Stat(targetName)
				afterBytes, readErr := os.ReadFile(targetName)
				inventoryAfter := claimInventory(t, root)

				// Assert: contradictory proof authorizes no cleanup or adoption.
				var committed *store.CommittedError
				isCommitted := errors.As(recoveryErr, &committed)
				wantCommitted := test.postOwnership
				if !errors.Is(recoveryErr, errArtifactClaimConflict) || isCommitted != wantCommitted || statErr != nil || readErr != nil || !os.SameFile(targetBefore, targetAfter) || targetBefore.Mode() != targetAfter.Mode() || !reflect.DeepEqual(targetBytes, afterBytes) || !reflect.DeepEqual(inventoryBefore, inventoryAfter) {
					t.Fatalf("recovery=%v stat=%v read=%v same=%t mode=%v/%v bytes=%t inventory=%v/%v", recoveryErr, statErr, readErr, statErr == nil && os.SameFile(targetBefore, targetAfter), targetBefore.Mode(), modeOf(targetAfter), reflect.DeepEqual(targetBytes, afterBytes), inventoryBefore, inventoryAfter)
				}
			})
		}
	}
}

func TestVisibleTailRequiresBothOldAndInstalledProof(t *testing.T) {
	for _, damage := range []string{"old-witness-swap", "installed-target-swap"} {
		t.Run(damage, func(t *testing.T) {
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := nextSnapshotForDurabilityTest(t, s)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "r7-visible-tail-"+damage, "")
			stop := errors.New("retain old proof tail")
			s.descriptorBarrier = func(phase string) error {
				if phase == "visible_claim_removed" {
					return stop
				}
				return nil
			}
			if err := s.publish(context.Background(), next, receipt); !errors.Is(err, stop) {
				t.Fatalf("publish=%v", err)
			}
			s.descriptorBarrier = nil
			oldKey, _ := newArtifactClaimKey(receipt.RequestDigest, "a.md", claimVisible)
			name := "a.md"
			if damage == "old-witness-swap" {
				name = oldKey.witnessPath(false)
			}
			_, _, foreign := replaceRegularWithSameBytes(t, root, name)
			beforeInventory := claimInventory(t, root)

			_, recoveryErr := s.Snapshot(context.Background())
			after, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
			afterInventory := claimInventory(t, root)

			var committed *store.CommittedError
			if !errors.Is(recoveryErr, errArtifactClaimConflict) || !errors.As(recoveryErr, &committed) || statErr != nil || !os.SameFile(foreign, after) || !reflect.DeepEqual(beforeInventory, afterInventory) {
				t.Fatalf("recovery=%v committed=%v stat=%v same=%t inventory=%v/%v", recoveryErr, committed, statErr, statErr == nil && os.SameFile(foreign, after), beforeInventory, afterInventory)
			}
		})
	}
}
