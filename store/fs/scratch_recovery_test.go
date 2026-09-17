package fs

// Scratch proof compensation and recovery.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScratchAttemptCompensatesEveryProofPhase(t *testing.T) {
	for _, phase := range []string{
		"scratch_opened",
		"scratch_before_stat",
		"scratch_stat_ready",
		"artifact_binding_opened:scratch",
		"artifact_binding_stat_ready:scratch",
		"artifact_binding_written:scratch",
		"artifact_binding_synced:scratch",
		"artifact_binding_closed:scratch",
		"artifact_binding_ready:scratch",
		"witness_before_link",
		"witness_after_link",
		"artifact_witness_ready:scratch",
	} {
		t.Run(phase, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			injected := errors.New("scratch phase")
			s.descriptorBarrier = func(got string) error {
				if got == phase {
					return injected
				}
				return nil
			}

			// Act.
			writeErr := s.writeDurableAtObserved(context.Background(), "artifact.bin", []byte("payload"), 0o640, nil, nil)
			temps := scratchTempInventory(t, root)
			claims := claimInventory(t, root)
			_, targetErr := os.Stat(filepath.Join(root, "artifact.bin"))

			// Assert.
			if !errors.Is(writeErr, injected) || len(temps) != 0 || len(claims) != 0 || !errors.Is(targetErr, os.ErrNotExist) {
				t.Fatalf("write=%v injected=%t temps=%v claims=%v target=%v", writeErr, errors.Is(writeErr, injected), temps, claims, targetErr)
			}
		})
	}
}

func TestScratchPostFaultLeavesClosedEvidenceForRecovery(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	injected := errors.New("crash after scratch write")
	s.config.PostFault = func(step Step) error {
		if step == StepFileWrite {
			return injected
		}
		return nil
	}

	// Act.
	writeErr := s.writeDurableAtObserved(context.Background(), "artifact.bin", []byte("payload"), 0o640, nil, nil)
	beforeTemps := scratchTempInventory(t, root)
	beforeClaims := claimInventory(t, root)
	s.config.PostFault = nil
	_, firstErr := s.Snapshot(context.Background())
	_, secondErr := s.Snapshot(context.Background())
	afterTemps := scratchTempInventory(t, root)
	afterClaims := claimInventory(t, root)

	// Assert.
	if !errors.Is(writeErr, injected) || len(beforeTemps) == 0 || len(beforeClaims) == 0 || firstErr != nil || secondErr != nil || len(afterTemps) != 0 || len(afterClaims) != 0 {
		t.Fatalf("write=%v before=%v/%v first=%v second=%v after=%v/%v", writeErr, beforeTemps, beforeClaims, firstErr, secondErr, afterTemps, afterClaims)
	}
}

func TestScratchForeignReplacementIsPreserved(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	injected := errors.New("foreign scratch")
	var foreignName string
	var foreignInfo os.FileInfo
	foreignBytes := []byte("foreign")
	s.descriptorBarrier = func(phase string) error {
		if phase != "scratch_stat_ready" {
			return nil
		}
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(temporaryDirectory)))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".okf-tmp-") {
				foreignName = filepath.Join(root, filepath.FromSlash(temporaryDirectory), entry.Name())
				if err := os.Remove(foreignName); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(foreignName, foreignBytes, 0o604); err != nil {
					t.Fatal(err)
				}
				foreignInfo, err = os.Stat(foreignName)
				if err != nil {
					t.Fatal(err)
				}
				return injected
			}
		}
		t.Fatal("scratch temp not found")
		return nil
	}

	// Act.
	writeErr := s.writeDurableAtObserved(context.Background(), "artifact.bin", []byte("payload"), 0o640, nil, nil)
	after, statErr := os.Stat(foreignName)
	afterBytes, readErr := os.ReadFile(foreignName)

	// Assert.
	if !errors.Is(writeErr, injected) || !errors.Is(writeErr, errArtifactClaimConflict) || statErr != nil || !os.SameFile(foreignInfo, after) || foreignInfo.Mode() != after.Mode() || readErr != nil || !reflect.DeepEqual(foreignBytes, afterBytes) {
		t.Fatalf("write=%v conflict=%t stat=%v same=%t mode=%v/%v read=%v bytes=%q", writeErr, errors.Is(writeErr, errArtifactClaimConflict), statErr, statErr == nil && os.SameFile(foreignInfo, after), foreignInfo.Mode(), modeOf(after), readErr, afterBytes)
	}
}

func scratchTempInventory(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(temporaryDirectory)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".okf-tmp-") {
			result = append(result, entry.Name())
		}
	}
	return result
}
