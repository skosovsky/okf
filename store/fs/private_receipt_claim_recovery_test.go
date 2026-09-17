package fs

// Private receipt claims and crash-resumption behavior.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

var receiptPrunePrivatePhaseOwners = map[string]func(*testing.T){
	"artifact_binding_ready:receipt":     TestReceiptPruneClaimPhaseMatrix,
	"witness_after_link":                 TestReceiptPruneClaimPhaseMatrix,
	"artifact_witness_ready:receipt":     TestReceiptPruneClaimPhaseMatrix,
	"claim_after_rename":                 TestReceiptPruneClaimPhaseMatrix,
	"claim_before_unlink":                TestReceiptPruneClaimPhaseMatrix,
	"artifact_binding_removed":           TestReceiptPruneClaimPhaseMatrix,
	"receipt_prune_batch_synced":         TestReceiptPruneBatchesContainingDirectorySync,
	"receipt_remove_postfault_converges": TestReceiptPruneRemovePostFaultRecoveryUsesOneConvergenceBarrier,
}

func TestReceiptPrunePrivatePhaseOwnersAreClosed(t *testing.T) {
	want := []string{"artifact_binding_ready:receipt", "artifact_binding_removed", "artifact_witness_ready:receipt", "claim_after_rename", "claim_before_unlink", "receipt_prune_batch_synced", "receipt_remove_postfault_converges", "witness_after_link"}
	got := make([]string, 0, len(receiptPrunePrivatePhaseOwners))
	for phase, owner := range receiptPrunePrivatePhaseOwners {
		if owner == nil {
			t.Fatalf("nil owner for %s", phase)
		}
		got = append(got, phase)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("owners=%v want=%v", got, want)
	}
}

func TestRetainedCrashMarkerSurvivesComposition(t *testing.T) {
	injected := errors.New("crash")
	if got := retainArtifactSource(retainArtifactSourceCrash(injected)); !artifactSourceCrash(got) || !errors.Is(got, injected) {
		t.Fatalf("retained=%v crash=%t injected=%t", got, artifactSourceCrash(got), errors.Is(got, injected))
	}
}

func TestReceiptPruneClaimPhaseMatrix(t *testing.T) {
	for _, phase := range []string{"artifact_binding_ready:receipt", "witness_after_link", "artifact_witness_ready:receipt", "claim_after_rename", "claim_before_unlink", "artifact_binding_removed"} {
		t.Run(phase, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			s.config.MinimumReceipts = 0
			s.config.ReceiptRetention = time.Nanosecond
			writeExpiredReceipts(t, s, 1)
			injected := errors.New("receipt proof phase")
			s.descriptorBarrier = func(got string) error {
				if got == phase {
					return injected
				}
				return nil
			}

			// Act.
			firstErr := s.pruneReceipts()
			s.descriptorBarrier = nil
			secondErr := s.pruneReceipts()
			thirdErr := s.pruneReceipts()
			receipts := directoryNames(t, filepath.Join(root, filepath.FromSlash(internalDirectory), "receipts"))
			claims := claimInventory(t, root)

			// Assert.
			if !errors.Is(firstErr, injected) || secondErr != nil || thirdErr != nil || len(receipts) != 0 || len(claims) != 0 {
				t.Fatalf("first=%v second=%v third=%v receipts=%v claims=%v", firstErr, secondErr, thirdErr, receipts, claims)
			}
		})
	}
}

func TestReceiptPrunePostFaultResumesClosedBinding(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	s.config.MinimumReceipts = 0
	s.config.ReceiptRetention = time.Nanosecond
	writeExpiredReceipts(t, s, 1)
	injected := errors.New("receipt binding crash")
	claimSyncs := 0
	s.config.PostFault = func(step Step) error {
		if step == StepClaimDirectorySync {
			claimSyncs++
			if claimSyncs == 2 {
				return injected
			}
		}
		return nil
	}

	// Act.
	firstErr := s.pruneReceipts()
	evidence := claimInventory(t, root)
	s.config.PostFault = nil
	secondErr := s.pruneReceipts()
	thirdErr := s.pruneReceipts()
	receipts := directoryNames(t, filepath.Join(root, filepath.FromSlash(internalDirectory), "receipts"))
	claims := claimInventory(t, root)

	// Assert.
	if !errors.Is(firstErr, injected) || len(evidence) == 0 || secondErr != nil || thirdErr != nil || len(receipts) != 0 || len(claims) != 0 {
		t.Fatalf("first=%v evidence=%v second=%v third=%v receipts=%v claims=%v", firstErr, evidence, secondErr, thirdErr, receipts, claims)
	}
}

func TestReceiptPrunePreservesForeignProof(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	s.config.MinimumReceipts = 0
	s.config.ReceiptRetention = time.Nanosecond
	writeExpiredReceipts(t, s, 1)
	receiptDir := filepath.Join(root, filepath.FromSlash(internalDirectory), "receipts")
	names := directoryNames(t, receiptDir)
	if len(names) != 1 {
		t.Fatalf("receipts=%v", names)
	}
	receiptName := filepath.Join(receiptDir, names[0])
	raw, err := os.ReadFile(receiptName)
	if err != nil {
		t.Fatal(err)
	}
	relative := filepath.ToSlash(filepath.Join(internalDirectory, "receipts", names[0]))
	key, err := newArtifactClaimKey(receiptPruneOperation(relative, raw), relative, claimReceipt)
	if err != nil {
		t.Fatal(err)
	}
	record := directoryWitnessRecord{Version: 1, Operation: key.Operation, Path: key.Path, Role: key.Role, Identity: "foreign", Source: key.Path}
	bindingRaw, _ := json.Marshal(record)
	bindingName := filepath.Join(root, filepath.FromSlash(key.bindingPath()))
	if err := os.WriteFile(bindingName, bindingRaw, 0o640); err != nil {
		t.Fatal(err)
	}
	receiptBefore, _ := os.Stat(receiptName)
	bindingBefore, _ := os.Stat(bindingName)

	// Act.
	pruneErr := s.pruneReceipts()
	receiptAfter, receiptErr := os.Stat(receiptName)
	bindingAfter, bindingErr := os.Stat(bindingName)
	afterRaw, readErr := os.ReadFile(bindingName)

	// Assert.
	if !errors.Is(pruneErr, errArtifactClaimConflict) || receiptErr != nil || bindingErr != nil || !os.SameFile(receiptBefore, receiptAfter) || !os.SameFile(bindingBefore, bindingAfter) || readErr != nil || !reflect.DeepEqual(bindingRaw, afterRaw) {
		t.Fatalf("prune=%v receipt=%v same=%t binding=%v same=%t read=%v bytes=%t", pruneErr, receiptErr, receiptErr == nil && os.SameFile(receiptBefore, receiptAfter), bindingErr, bindingErr == nil && os.SameFile(bindingBefore, bindingAfter), readErr, reflect.DeepEqual(bindingRaw, afterRaw))
	}
}

func TestCapabilityForeignReplacementIsPreserved(t *testing.T) {
	// Arrange: crash after the probe target directory is physically synced.
	root, s := adversarialStore(t, Config{})
	injected := errors.New("capability crash")
	probe := filepath.Join(root, filepath.FromSlash(internalDirectory), "capabilities", "case-probe-a")
	s.config.PostFault = func(step Step) error {
		if step == StepCapabilityDirectorySync {
			if _, err := os.Stat(probe); err == nil {
				return injected
			}
		}
		return nil
	}
	_, firstErr := s.Snapshot(context.Background())
	if !errors.Is(firstErr, injected) {
		t.Fatalf("first=%v", firstErr)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	foreignBytes := []byte("foreign capability")
	if err := os.WriteFile(probe, foreignBytes, 0o604); err != nil {
		t.Fatal(err)
	}
	foreign, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	claimsBefore := claimInventory(t, root)
	s.config.PostFault = nil

	// Act.
	_, secondErr := s.Snapshot(context.Background())
	_, thirdErr := s.Snapshot(context.Background())
	after, statErr := os.Stat(probe)
	afterBytes, readErr := os.ReadFile(probe)
	claimsAfter := claimInventory(t, root)

	// Assert.
	if !errors.Is(secondErr, errArtifactClaimConflict) || !errors.Is(thirdErr, errArtifactClaimConflict) || statErr != nil || !os.SameFile(foreign, after) || foreign.Mode() != after.Mode() || readErr != nil || !reflect.DeepEqual(foreignBytes, afterBytes) || !reflect.DeepEqual(claimsBefore, claimsAfter) {
		t.Fatalf("second=%v third=%v stat=%v same=%t mode=%v/%v read=%v bytes=%q claims=%v/%v", secondErr, thirdErr, statErr, statErr == nil && os.SameFile(foreign, after), foreign.Mode(), modeOf(after), readErr, afterBytes, claimsBefore, claimsAfter)
	}
}

func directoryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
