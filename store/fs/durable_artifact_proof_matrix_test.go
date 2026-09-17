package fs

// Proof completeness across durable artifact roles.

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
)

func TestRecoveryRequiresClosedProofForEveryDurableArtifact(t *testing.T) {
	operation := "sha256:" + strings.Repeat("0", 64)
	stage, err := journalStage(operation)
	if err != nil {
		t.Fatal(err)
	}
	journalName, err := journalPath(operation)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		role   artifactRole
		target string
		kind   string
	}{
		{name: "journal missing binding", role: claimJournal, target: journalName, kind: "binding"},
		{name: "journal missing witness", role: claimJournal, target: journalName, kind: "witness"},
		{name: "journal forged source", role: claimJournal, target: journalName, kind: "source"},
		{name: "stage parent missing binding", role: claimStageDir, target: path.Dir(stage), kind: "binding"},
		{name: "payload directory missing sentinel", role: claimStageDir, target: stage, kind: "sentinel"},
		{name: "payload missing binding", role: claimPayload, target: path.Join(stage, "payload-00000"), kind: "binding"},
		{name: "payload missing witness", role: claimPayload, target: path.Join(stage, "payload-00000"), kind: "witness"},
		{name: "payload forged source", role: claimPayload, target: path.Join(stage, "payload-00000"), kind: "source"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root, s, _, _ := stageRecoveryJournal(t, Config{})
			key, err := newArtifactClaimKey(operation, test.target, test.role)
			if err != nil {
				t.Fatal(err)
			}
			bindingName := filepath.Join(root, filepath.FromSlash(key.bindingPath()))
			witnessPath := key.witnessPath(test.role == claimStageDir)
			witnessName := filepath.Join(root, filepath.FromSlash(witnessPath))
			switch test.kind {
			case "binding":
				if err := os.Remove(bindingName); err != nil {
					t.Fatal(err)
				}
			case "witness", "sentinel":
				if err := os.Remove(witnessName); err != nil {
					t.Fatal(err)
				}
			case "source":
				raw, err := os.ReadFile(bindingName)
				if err != nil {
					t.Fatal(err)
				}
				record, err := decodeDirectoryWitness(raw)
				if err != nil {
					t.Fatal(err)
				}
				record.Source = path.Join(temporaryDirectory, "forged")
				raw, err = json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(bindingName, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			targetName := filepath.Join(root, filepath.FromSlash(test.target))
			before, err := os.Stat(targetName)
			if err != nil {
				t.Fatal(err)
			}
			beforeBytes, readErr := proofTargetBytes(targetName, before.IsDir())
			if readErr != nil {
				t.Fatal(readErr)
			}
			claimsBefore := claimInventory(t, root)

			// Act.
			_, recoveryErr := s.Snapshot(context.Background())
			after, statErr := os.Stat(targetName)
			afterBytes, readErr := proofTargetBytes(targetName, statErr == nil && after.IsDir())
			claimsAfter := claimInventory(t, root)

			// Assert.
			if !errors.Is(recoveryErr, errArtifactClaimConflict) || statErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || readErr != nil || !reflect.DeepEqual(beforeBytes, afterBytes) || !reflect.DeepEqual(claimsBefore, claimsAfter) {
				t.Fatalf("recovery=%v conflict=%t stat=%v same=%t mode=%v/%v read=%v bytes=%t claims=%v/%v", recoveryErr, errors.Is(recoveryErr, errArtifactClaimConflict), statErr, statErr == nil && os.SameFile(before, after), before.Mode(), modeOf(after), readErr, reflect.DeepEqual(beforeBytes, afterBytes), claimsBefore, claimsAfter)
			}
		})
	}
}

func TestRecoveryAcceptsExactFullProof(t *testing.T) {
	// Arrange.
	root, s, _, next := stageRecoveryJournal(t, Config{})

	// Act.
	snapshot, recoveryErr := s.Snapshot(context.Background())
	claims := claimInventory(t, root)

	// Assert.
	if recoveryErr != nil || snapshot.Revision() != next.Revision() || len(claims) != 0 {
		t.Fatalf("recovery=%v revision=%v/%v claims=%v", recoveryErr, snapshot.Revision(), next.Revision(), claims)
	}
}

func proofTargetBytes(name string, directory bool) ([]byte, error) {
	if directory {
		entries, err := os.ReadDir(name)
		if err != nil {
			return nil, err
		}
		var result []byte
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			result = append(result, entry.Name()...)
			result = append(result, 0)
			result = append(result, info.Mode().String()...)
			result = append(result, 0)
		}
		return result, nil
	}
	return os.ReadFile(name)
}
