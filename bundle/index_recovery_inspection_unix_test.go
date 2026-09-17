//go:build unix

package bundle

import "testing"

func inspectIndexRecoveryExistingRollbackEntry(
	t *testing.T,
	rootPath, relative string,
) indexBatchV3RecoveryPhase {
	t.Helper()
	root, err := openRootWithoutSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	discoveries, err := discoverIndexTransactionNamespace(root)
	if err != nil {
		_ = root.Close()
		t.Fatal(err)
	}
	prepared, err := prepareIndexRecovery(root, discoveries, indexPublishHooks{})
	if err != nil {
		_ = root.Close()
		t.Fatal(err)
	}
	defer prepared.close()
	defer root.Close()
	if prepared.batch == nil {
		t.Fatal("v3 batch recovery was not prepared")
	}
	for _, entry := range prepared.batch.entries {
		if entry.contract.Path == relative {
			return entry.v3Phase
		}
	}
	for _, group := range prepared.batch.terminalCommit {
		if group == nil || group.destination != relative || group.proof == nil {
			continue
		}
		switch group.proof.discovery.kind {
		case indexArtifactStage:
			if group.targetAbsent {
				return indexBatchV3PhaseCreateRollbackVacated
			}
			return indexBatchV3PhasePublished
		case indexArtifactAnchor:
			return indexBatchV3PhaseRestored
		case indexArtifactWitness:
			return indexBatchV3PhasePreNewRestored
		default:
			t.Fatalf(
				"terminal batch entry %q has unsupported proof role %q",
				relative,
				group.proof.discovery.kind,
			)
		}
	}
	t.Fatalf("batch entry %q is missing", relative)
	return indexBatchV3PhaseInvalid
}
