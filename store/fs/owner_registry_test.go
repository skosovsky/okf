package fs

import (
	"reflect"
	"sort"
	"testing"
)

// stepOwnerRegistry is the single executable ownership registry for
// Store-private fault phases delegated by adapter/root observable matrices.
// Function references make stale owner names a compile failure.
var stepOwnerRegistry = map[Step]func(*testing.T){
	StepPrivateDirectoryChmod:        TestPrivateDirectoryFaultMatrixConvergesOnRetryAndReopen,
	StepPrivateDirectorySync:         TestPrivateDirectoryFaultMatrixConvergesOnRetryAndReopen,
	StepPrivateDirectoryClose:        TestPrivateDirectoryFaultMatrixConvergesOnRetryAndReopen,
	StepPrivateDirectoryParentSync:   TestPrivateDirectoryFaultMatrixConvergesOnRetryAndReopen,
	StepCapabilityMkdir:              TestCapabilityProbeFaultHooksRecoverAndClean,
	StepCapabilityChmod:              TestCapabilityProbeFaultHooksRecoverAndClean,
	StepCapabilityFileWrite:          TestCapabilityProbeFaultHooksRecoverAndClean,
	StepCapabilityFileSync:           TestCapabilityProbeFaultHooksRecoverAndClean,
	StepCapabilityFileClose:          TestCapabilityProbeFaultHooksRecoverAndClean,
	StepCapabilityRename:             TestCapabilityProbeFaultHooksRecoverAndClean,
	StepCapabilityRemove:             TestCapabilityProbeFaultHooksRecoverAndClean,
	StepCapabilityDirectorySync:      TestCapabilityProbeFaultHooksRecoverAndClean,
	StepClaimDirectorySync:           TestRoleClaimTerminalSwapOutcome,
	StepMkdir:                        TestStageNamespaceLifecycleMatrix,
	StepChmod:                        TestPrivateSyncStepClassificationIsClosed,
	StepPrivateMetadataSync:          TestPrivateSyncStepClassificationIsClosed,
	StepPrivateMetadataClose:         TestPrivateSyncStepClassificationIsClosed,
	StepStageFileWrite:               TestPreJournalRegularProofCompensation,
	StepStageFileSync:                TestPreJournalCompensationProofTail,
	StepStageFileClose:               TestPreJournalCompensationProofTail,
	StepStageRename:                  TestStageNamespaceParentSyncCutsRetry,
	StepStageDirectorySync:           TestStageNamespaceParentSyncCutsRetry,
	StepStageCleanupPayloadRemove:    TestClaimCleanupBatchesOwnedStageBoundaries,
	StepStageCleanupPayloadDirectory: TestClaimCleanupPostFaultConvergesByNamedDirectoryPhase,
	StepStageCleanupPayloadDirRemove: TestClaimCleanupBatchesOwnedStageBoundaries,
	StepStageCleanupStageDirectory:   TestClaimCleanupPostFaultConvergesByNamedDirectoryPhase,
	StepStageCleanupStageDirRemove:   TestClaimCleanupBatchesOwnedStageBoundaries,
	StepStageCleanupRootDirectory:    TestClaimCleanupPostFaultConvergesByNamedDirectoryPhase,
	StepTempCleanupRemove:            TestScratchSourceForeignSwapMatrix,
	StepTempCleanupDirectorySync:     TestProofLastTailCutsConvergeTwice,
	StepJournalWrite:                 TestJournalHasSinglePublicationBoundary,
	StepJournalFileWrite:             TestJournalHasSinglePublicationBoundary,
	StepJournalFileSync:              TestJournalHasSinglePublicationBoundary,
	StepJournalFileClose:             TestJournalHasSinglePublicationBoundary,
	StepJournalRename:                TestJournalHasSinglePublicationBoundary,
	StepJournalDirectorySync:         TestDurableBoundaryOutcomeMatrix,
	StepFileWrite:                    TestVisibleOverwriteCrashRetryMatrix,
	StepFileSync:                     TestVisibleOverwriteCrashRetryMatrix,
	StepFileClose:                    TestVisibleOverwriteCrashRetryMatrix,
	StepRename:                       TestVisibleCreateCrashRetryMatrix,
	StepRemove:                       TestVisibleDeleteCrashRetryMatrix,
	StepDirectorySync:                TestRootIndexClaimActionTraceHasNoVisibleTail,
	StepReceiptWrite:                 TestStageAndReceiptFaultMatrixConverges,
	StepReceiptSync:                  TestStageAndReceiptFaultMatrixConverges,
	StepReceiptClose:                 TestReceiptPruneRemovePostFaultRecoveryUsesOneConvergenceBarrier,
	StepReceiptRename:                TestStageAndReceiptFaultMatrixConverges,
	StepReceiptDirectorySync:         TestStageAndReceiptFaultMatrixConverges,
	StepReceiptPrune:                 TestReceiptPruneRemovalOrderIsLexical,
}

func TestStepOwnerRegistryIsClosed(t *testing.T) {
	// Arrange.
	required := []Step{
		StepPrivateDirectoryChmod, StepPrivateDirectorySync, StepPrivateDirectoryClose, StepPrivateDirectoryParentSync,
		StepCapabilityMkdir, StepCapabilityChmod, StepCapabilityFileWrite, StepCapabilityFileSync,
		StepCapabilityFileClose, StepCapabilityRename, StepCapabilityRemove, StepCapabilityDirectorySync,
		StepClaimDirectorySync, StepMkdir, StepChmod, StepPrivateMetadataSync, StepPrivateMetadataClose,
		StepStageFileWrite, StepStageFileSync, StepStageFileClose, StepStageRename, StepStageDirectorySync,
		StepStageCleanupPayloadRemove, StepStageCleanupPayloadDirectory, StepStageCleanupPayloadDirRemove,
		StepStageCleanupStageDirectory, StepStageCleanupStageDirRemove, StepStageCleanupRootDirectory,
		StepTempCleanupRemove, StepTempCleanupDirectorySync,
		StepJournalWrite, StepJournalFileWrite, StepJournalFileSync, StepJournalFileClose, StepJournalRename, StepJournalDirectorySync,
		StepFileWrite, StepFileSync, StepFileClose, StepRename, StepRemove, StepDirectorySync,
		StepReceiptWrite, StepReceiptSync, StepReceiptClose, StepReceiptRename, StepReceiptDirectorySync, StepReceiptPrune,
	}
	sort.Slice(required, func(i, j int) bool { return required[i] < required[j] })
	got := make([]Step, 0, len(stepOwnerRegistry))
	for step, owner := range stepOwnerRegistry {
		if owner == nil || reflect.ValueOf(owner).Pointer() == 0 {
			t.Fatalf("step %s has nil canonical owner", step)
		}
		got = append(got, step)
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })

	// Act / Assert.
	if !reflect.DeepEqual(got, required) {
		t.Fatalf("owner inventory=%v want=%v", got, required)
	}
}
