package fs

import (
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const broadRegressionSelector = `^Test(CommitCancellationAfterJournalPersistsReceipt|PublishRecoveryCompletesPostStateAndReceipt|JournalDecoder.*|ConfigValidateStagedPayload.*|JournalPayload.*|RecoveryRejectsNegativeV5PayloadSizeBeforePayloadIO|StagedManifestEffectiveLimitsAreValidatedAsOneMetadataGate|RecoverySynthetic.*|ConfiguredStage.*|JournalManifestBytePreflightBeforeAnyStageSideEffect|RecoveryJournalManifestEffectiveByteGateBeforePayloadIO|PayloadOrdinalCeiling|AdversarialReceiptRetentionKeepsDeterministicMinimum|DescriptorBarrierRejectsSwappedRenameAndRemoveParents|ForcedFilesystemAliasesRejectStageAndRecovery|RecoveryRejectsDuplicateJournalKeysWithoutChangingBytes|RecoveryRejectsSelfConsistentJournalWithInvalidPostStateBeforeApply|RecoveryBindsV5JournalStageAndFilenameToRequestDigest|TempCleanupFaultsLeaveNoScratchFiles|RecoveryRejectsFilesystemEquivalentJournalBasePathsBeforeIO|(DurableBoundary|PostBoundary|PreBoundary|RecoveryCancellation|OwnedRemove|ActiveOwnership|PreDurable|RecoveryClassification|AtomicRename|JournalReadPins|AtomicInstall|RecoveryIO|RecoveryDescriptor|PayloadStructural|RecoveredReceipt|Claim|PreJournal|ValidatedJournal|TerminalClaim|DirectoryClaim|InstallProtocol|JournalHas|RoleClaim|StageNamespace|StageClaim|Scratch|ProofLast|ForeignBinding|PrivateSync|PrivateDirectory|Visible|MetadataIO|ProvenanceIO|OrphanDirectoryIO|TransactionReadDirIO|MetadataStructural|StructuralClose|CloseOnly|ConsumeNever|CanceledStage|DirectoryProof|DirectoryWrong|DirectoryNoReplace|OwnedStage|RecoveryRequiresClosed|RecoveryConsumesExact|DurableJournal|ClaimsZone|ActualPinned|ReceiptPrune|RetainedCrash|CapabilityForeign|RecoveryAcceptsExact|ForgedPrivate|ReceiptRecovery|Early|CanonicalTemp|InvalidClock|CompleteForged|JournalBefore|ReceiptBinding).*)$`

var broadRegressionOwners = map[string]func(*testing.T){
	"TestCommitCancellationAfterJournalPersistsReceipt":                       TestCommitCancellationAfterJournalPersistsReceipt,
	"TestAdversarialReceiptRetentionKeepsDeterministicMinimum":                TestAdversarialReceiptRetentionKeepsDeterministicMinimum,
	"TestDescriptorBarrierRejectsSwappedRenameAndRemoveParents":               TestDescriptorBarrierRejectsSwappedRenameAndRemoveParents,
	"TestForcedFilesystemAliasesRejectStageAndRecovery":                       TestForcedFilesystemAliasesRejectStageAndRecovery,
	"TestRecoveryRejectsDuplicateJournalKeysWithoutChangingBytes":             TestRecoveryRejectsDuplicateJournalKeysWithoutChangingBytes,
	"TestPublishRecoveryCompletesPostStateAndReceipt":                         TestPublishRecoveryCompletesPostStateAndReceipt,
	"TestRecoveryRejectsSelfConsistentJournalWithInvalidPostStateBeforeApply": TestRecoveryRejectsSelfConsistentJournalWithInvalidPostStateBeforeApply,
	"TestRecoveryBindsV5JournalStageAndFilenameToRequestDigest":               TestRecoveryBindsV5JournalStageAndFilenameToRequestDigest,
	"TestTempCleanupFaultsLeaveNoScratchFiles":                                TestTempCleanupFaultsLeaveNoScratchFiles,
	"TestRecoveryRejectsFilesystemEquivalentJournalBasePathsBeforeIO":         TestRecoveryRejectsFilesystemEquivalentJournalBasePathsBeforeIO,
	"TestPreBoundaryCleanupPreservesSameBytesForeignJournalInode":             TestPreBoundaryCleanupPreservesSameBytesForeignJournalInode,
	"TestPreBoundaryCleanupOrdersJournalProofBeforeStage":                     TestPreBoundaryCleanupOrdersJournalProofBeforeStage,
	"TestJournalDecoderAcceptsPayloadAboveDefaultsBelowAbsoluteCeiling":       TestJournalDecoderAcceptsPayloadAboveDefaultsBelowAbsoluteCeiling,
	"TestJournalDecoderAcceptsAggregateAboveDefaultBelowAbsoluteCeiling":      TestJournalDecoderAcceptsAggregateAboveDefaultBelowAbsoluteCeiling,
	"TestConfigValidateStagedPayloadAbsoluteCeilings":                         TestConfigValidateStagedPayloadAbsoluteCeilings,
	"TestJournalPayloadLimitsRejectAbsoluteOverflow":                          TestJournalPayloadLimitsRejectAbsoluteOverflow,
	"TestJournalDecoderSyntheticAbsoluteFileCountCeiling":                     TestJournalDecoderSyntheticAbsoluteFileCountCeiling,
	"TestJournalPayloadManifestIntegrityRegressions":                          TestJournalPayloadManifestIntegrityRegressions,
	"TestRecoveryRejectsNegativeV5PayloadSizeBeforePayloadIO":                 TestRecoveryRejectsNegativeV5PayloadSizeBeforePayloadIO,
	"TestStagedManifestEffectiveLimitsAreValidatedAsOneMetadataGate":          TestStagedManifestEffectiveLimitsAreValidatedAsOneMetadataGate,
	"TestRecoverySyntheticEnlargedCeilingCompletesDurableJournal":             TestRecoverySyntheticEnlargedCeilingCompletesDurableJournal,
	"TestRecoverySyntheticSmallerConfigRejectsBeforePayloadRead":              TestRecoverySyntheticSmallerConfigRejectsBeforePayloadRead,
	"TestRecoverySyntheticSmallerAggregateLimitRejectsBeforePayloadRead":      TestRecoverySyntheticSmallerAggregateLimitRejectsBeforePayloadRead,
	"TestRecoverySyntheticConfiguredFileCountRejectsBeforePayloadRead":        TestRecoverySyntheticConfiguredFileCountRejectsBeforePayloadRead,
	"TestConfiguredStagePayloadLimitsRejectBeforePayloadWrite":                TestConfiguredStagePayloadLimitsRejectBeforePayloadWrite,
	"TestConfiguredStageFileCountRejectsBeforeAnyStageWrite":                  TestConfiguredStageFileCountRejectsBeforeAnyStageWrite,
	"TestJournalManifestBytePreflightBeforeAnyStageSideEffect":                TestJournalManifestBytePreflightBeforeAnyStageSideEffect,
	"TestRecoveryJournalManifestEffectiveByteGateBeforePayloadIO":             TestRecoveryJournalManifestEffectiveByteGateBeforePayloadIO,
	"TestPayloadOrdinalCeiling":                                               TestPayloadOrdinalCeiling,
	"TestRecoveryClassificationAndReceiptProjection":                          TestRecoveryClassificationAndReceiptProjection,
	"TestClaimBindingIsClosed":                                                TestClaimBindingIsClosed,
	"TestPreJournalRegularProofCompensation":                                  TestPreJournalRegularProofCompensation,
	"TestPreJournalForeignSourceIsPreserved":                                  TestPreJournalForeignSourceIsPreserved,
	"TestPreJournalForeignWitnessIsPreserved":                                 TestPreJournalForeignWitnessIsPreserved,
	"TestPreJournalWrongBindingIsPreserved":                                   TestPreJournalWrongBindingIsPreserved,
	"TestPreJournalContradictoryClaimIsPreserved":                             TestPreJournalContradictoryClaimIsPreserved,
	"TestValidatedJournalCleansInstalledRegularProof":                         TestValidatedJournalCleansInstalledRegularProof,
	"TestPreJournalCompensationProofTail":                                     TestPreJournalCompensationProofTail,
	"TestClaimProtocolAdversarialMatrix":                                      TestClaimProtocolAdversarialMatrix,
	"TestTerminalClaimSwapIsPreserved":                                        TestTerminalClaimSwapIsPreserved,
	"TestDirectoryClaimUsesDurableSentinel":                                   TestDirectoryClaimUsesDurableSentinel,
	"TestInstallProtocolAdversarialMatrix":                                    TestInstallProtocolAdversarialMatrix,
	"TestJournalHasSinglePublicationBoundary":                                 TestJournalHasSinglePublicationBoundary,
	"TestRoleClaimTerminalSwapOutcome":                                        TestRoleClaimTerminalSwapOutcome,
	"TestStageNamespaceLifecycleMatrix":                                       TestStageNamespaceLifecycleMatrix,
	"TestStageNamespaceRejectsContradictoryStates":                            TestStageNamespaceRejectsContradictoryStates,
	"TestStageNamespaceClaimsRecoverDeepestFirst":                             TestStageNamespaceClaimsRecoverDeepestFirst,
	"TestStageNamespaceParentSyncCutsRetry":                                   TestStageNamespaceParentSyncCutsRetry,
	"TestStageClaimParentSyncCutIsIdempotent":                                 TestStageClaimParentSyncCutIsIdempotent,
	"TestClaimCleanupBatchesOwnedStageBoundaries":                             TestClaimCleanupBatchesOwnedStageBoundaries,
	"TestClaimCleanupPostFaultConvergesByNamedDirectoryPhase":                 TestClaimCleanupPostFaultConvergesByNamedDirectoryPhase,
	"TestScratchSourceForeignSwapMatrix":                                      TestScratchSourceForeignSwapMatrix,
	"TestProofLastTailCutsConvergeTwice":                                      TestProofLastTailCutsConvergeTwice,
	"TestForeignBindingIsPreservedExactly":                                    TestForeignBindingIsPreservedExactly,
	"TestPrivateSyncStepClassificationIsClosed":                               TestPrivateSyncStepClassificationIsClosed,
	"TestVisibleOverwriteCrashRetryMatrix":                                    TestVisibleOverwriteCrashRetryMatrix,
	"TestVisibleCreateCrashRetryMatrix":                                       TestVisibleCreateCrashRetryMatrix,
	"TestVisibleForeignSwapMatrix":                                            TestVisibleForeignSwapMatrix,
	"TestVisibleCreateForeignDestinationIsPreserved":                          TestVisibleCreateForeignDestinationIsPreserved,
	"TestVisibleRollbackTailRetryMatrix":                                      TestVisibleRollbackTailRetryMatrix,
	"TestVisibleDeleteCrashRetryMatrix":                                       TestVisibleDeleteCrashRetryMatrix,
	"TestVisibleDeleteForeignSwapMatrix":                                      TestVisibleDeleteForeignSwapMatrix,
	"TestVisibleDeleteAbsentExpectation":                                      TestVisibleDeleteAbsentExpectation,
	"TestMetadataIOWinsOverCancellation":                                      TestMetadataIOWinsOverCancellation,
	"TestProvenanceIOAndStructuralClassification":                             TestProvenanceIOAndStructuralClassification,
	"TestOrphanDirectoryIOIsOperational":                                      TestOrphanDirectoryIOIsOperational,
	"TestTransactionReadDirIOIsOperational":                                   TestTransactionReadDirIOIsOperational,
	"TestPrivateDirectorySyncIOIsOperational":                                 TestPrivateDirectorySyncIOIsOperational,
	"TestMetadataStructuralControlsRemainCorruption":                          TestMetadataStructuralControlsRemainCorruption,
	"TestConsumeNeverSynthesizesUnboundProof":                                 TestConsumeNeverSynthesizesUnboundProof,
	"TestCanceledStageBuildCompensatesAttemptProof":                           TestCanceledStageBuildCompensatesAttemptProof,
	"TestDirectoryProofMatrix":                                                TestDirectoryProofMatrix,
	"TestDirectoryWrongSourcePreservesClaim":                                  TestDirectoryWrongSourcePreservesClaim,
	"TestDirectoryNoReplacePinsSourceAndTarget":                               TestDirectoryNoReplacePinsSourceAndTarget,
	"TestDirectoryNoReplaceCrashRetry":                                        TestDirectoryNoReplaceCrashRetry,
	"TestOwnedStageForeignPayloadIsPreserved":                                 TestOwnedStageForeignPayloadIsPreserved,
	"TestScratchRecoveryPreservesUnboundTempExactly":                          TestScratchRecoveryPreservesUnboundTempExactly,
	"TestPrivateDirectoryFaultMatrixConvergesOnRetryAndReopen":                TestPrivateDirectoryFaultMatrixConvergesOnRetryAndReopen,
	"TestPrivateDirectorySwapIsStructuralAndForeignIsUntouched":               TestPrivateDirectorySwapIsStructuralAndForeignIsUntouched,
	"TestPrivateDirectoryAlreadyPrivateStillConvergesDurability":              TestPrivateDirectoryAlreadyPrivateStillConvergesDurability,
	"TestPrivateDirectoryCancellationPreservesContextValue":                   TestPrivateDirectoryCancellationPreservesContextValue,
	"TestStructuralCloseFailurePreservesOrderedFacts":                         TestStructuralCloseFailurePreservesOrderedFacts,
	"TestCloseOnlyAndCancellationRemainOperational":                           TestCloseOnlyAndCancellationRemainOperational,
	"TestRecoveryRequiresClosedDirectoryProof":                                TestRecoveryRequiresClosedDirectoryProof,
	"TestRecoveryConsumesExactDirectoryProof":                                 TestRecoveryConsumesExactDirectoryProof,
	"TestVisibleWitnessWithoutBindingIsPreserved":                             TestVisibleWitnessWithoutBindingIsPreserved,
	"TestDurableJournalFreshVisibleInstallIsBindingFirst":                     TestDurableJournalFreshVisibleInstallIsBindingFirst,
	"TestVisibleExactBindingConverges":                                        TestVisibleExactBindingConverges,
	"TestClaimsZonePathRejectsNonClaimsBeforeMutation":                        TestClaimsZonePathRejectsNonClaimsBeforeMutation,
	"TestClaimsZoneHandleRevalidatesModeAndRootBinding":                       TestClaimsZoneHandleRevalidatesModeAndRootBinding,
	"TestActualPinnedReadCloseAndRebindPrecedence":                            TestActualPinnedReadCloseAndRebindPrecedence,
	"TestReceiptPrunePrivatePhaseOwnersAreClosed":                             TestReceiptPrunePrivatePhaseOwnersAreClosed,
	"TestRetainedCrashMarkerSurvivesComposition":                              TestRetainedCrashMarkerSurvivesComposition,
	"TestReceiptPruneClaimPhaseMatrix":                                        TestReceiptPruneClaimPhaseMatrix,
	"TestReceiptPrunePostFaultResumesClosedBinding":                           TestReceiptPrunePostFaultResumesClosedBinding,
	"TestReceiptPrunePreservesForeignProof":                                   TestReceiptPrunePreservesForeignProof,
	"TestCapabilityForeignReplacementIsPreserved":                             TestCapabilityForeignReplacementIsPreserved,
	"TestRecoveryRequiresClosedProofForEveryDurableArtifact":                  TestRecoveryRequiresClosedProofForEveryDurableArtifact,
	"TestRecoveryAcceptsExactFullProof":                                       TestRecoveryAcceptsExactFullProof,
	"TestScratchAttemptCompensatesEveryProofPhase":                            TestScratchAttemptCompensatesEveryProofPhase,
	"TestScratchPostFaultLeavesClosedEvidenceForRecovery":                     TestScratchPostFaultLeavesClosedEvidenceForRecovery,
	"TestScratchForeignReplacementIsPreserved":                                TestScratchForeignReplacementIsPreserved,
	"TestVisibleRecoveryRequiresClosedSourceProof":                            TestVisibleRecoveryRequiresClosedSourceProof,
	"TestVisibleTailRequiresBothOldAndInstalledProof":                         TestVisibleTailRequiresBothOldAndInstalledProof,
	"TestForgedPrivateRoleProofIsInert":                                       TestForgedPrivateRoleProofIsInert,
	"TestReceiptRecoveryAuthenticatesBeforeRepair":                            TestReceiptRecoveryAuthenticatesBeforeRepair,
	"TestEarlyStructuralReadKeepsCloseOperationalFirst":                       TestEarlyStructuralReadKeepsCloseOperationalFirst,
	"TestEarlyReadCloseOnlyRemainsOperational":                                TestEarlyReadCloseOnlyRemainsOperational,
	"TestCanonicalTempArtifactNameIsExact":                                    TestCanonicalTempArtifactNameIsExact,
	"TestInvalidClockCannotCreateTempArtifact":                                TestInvalidClockCannotCreateTempArtifact,
	"TestCompleteForgedRoleProofCannotReachAlias":                             TestCompleteForgedRoleProofCannotReachAlias,
	"TestJournalBeforeReadGrowthPreservesErrorFacts":                          TestJournalBeforeReadGrowthPreservesErrorFacts,
	"TestReceiptBindingOnlyAuthenticatesBeforeRepair":                         TestReceiptBindingOnlyAuthenticatesBeforeRepair,
	"TestReceiptBindingFieldsAuthenticateIndependently":                       TestReceiptBindingFieldsAuthenticateIndependently,
}

func TestBroad100OwnerInventoryIsClosed(t *testing.T) {
	if len(broadRegressionOwners) != 114 {
		t.Fatalf("broad owner count=%d want=114", len(broadRegressionOwners))
	}
	selector := regexp.MustCompile(broadRegressionSelector)
	functions := make(map[uintptr]string, len(broadRegressionOwners))
	for name, owner := range broadRegressionOwners {
		if owner == nil {
			t.Fatalf("nil broad owner %q", name)
		}
		if !selector.MatchString(name) {
			t.Fatalf("unknown broad owner %q", name)
		}
		pointer := reflect.ValueOf(owner).Pointer()
		if previous, duplicate := functions[pointer]; duplicate {
			t.Fatalf("duplicate broad owner function %q and %q", previous, name)
		}
		functions[pointer] = name
		resolved := runtime.FuncForPC(pointer)
		if resolved == nil || name != resolved.Name()[strings.LastIndexByte(resolved.Name(), '.')+1:] {
			t.Fatalf("broad owner key=%q resolves to %v", name, resolved)
		}
	}
}
