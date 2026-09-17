package bundle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
)

type documentV2RecoveryPhase uint8

const (
	documentV2PhaseInvalid documentV2RecoveryPhase = iota
	documentV2PhasePreNew
	documentV2PhasePreNewVacated
	documentV2PhasePreNewRollbackAnchored
	documentV2PhasePreNewRollbackReady
	documentV2PhasePreNewRestored
	documentV2PhasePublished
	documentV2PhaseRollbackAnchored
	documentV2PhaseRollbackReady
	documentV2PhasePreRestore
	documentV2PhaseRestored
)

type documentV2TargetRelation uint8

const (
	documentV2TargetForeign documentV2TargetRelation = iota
	documentV2TargetMissing
	documentV2TargetWitness
	documentV2TargetStage
	documentV2TargetAnchor
)

type documentV2RecoveryState struct {
	stage          bool
	backup         bool
	newInstall     bool
	anchor         bool
	restoreInstall bool
	discard        bool
	witness        bool
	claim          bool

	newAliasesStage      bool
	restoreAliasesAnchor bool
	discardAliasesStage  bool
	claimAliasesWitness  bool
	backupIndependent    bool
	target               documentV2TargetRelation
}

type documentV2RecoveryDecision uint8

const (
	documentV2DecisionAbort documentV2RecoveryDecision = iota + 1
	documentV2DecisionCommit
	documentV2DecisionRollback
)

type documentV2RecoveryAction uint8

const (
	documentV2ActionCreateAnchor documentV2RecoveryAction = iota + 1
	documentV2ActionCreateRestoreInstall
	documentV2ActionVacatePublished
	documentV2ActionConsumeRestoreInstall
	documentV2ActionFinalizeAbort
	documentV2ActionFinalizeCommit
	documentV2ActionFinalizeRollback
)

type documentV2RecoveryTransition struct {
	from              documentV2RecoveryPhase
	decision          documentV2RecoveryDecision
	action            documentV2RecoveryAction
	to                documentV2RecoveryPhase
	terminal          bool
	canonicalMutation bool
	proofLast         bool
}

type documentV2RecoveryObservation struct {
	state             documentV2RecoveryState
	terminal          bool
	canonicalMutation bool
	proofRemoved      bool
}

type documentV2RecoveryExecution struct {
	phase   documentV2RecoveryPhase
	actions []documentV2RecoveryAction
}

type documentV2RecoveryStep func(
	context.Context,
	documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error)

type documentV2RecoveryStateRow struct {
	phase documentV2RecoveryPhase
	state documentV2RecoveryState
}

var documentV2RecoveryStates = [...]documentV2RecoveryStateRow{
	{documentV2PhasePreNew, documentV2ContractState(true, false, false, false, false, documentV2TargetWitness)},
	{documentV2PhasePreNewVacated, documentV2ContractState(true, false, false, false, true, documentV2TargetMissing)},
	{documentV2PhasePreNewRollbackAnchored, documentV2ContractState(true, true, false, false, true, documentV2TargetMissing)},
	{documentV2PhasePreNewRollbackReady, documentV2ContractState(true, true, true, false, true, documentV2TargetMissing)},
	{documentV2PhasePreNewRestored, documentV2ContractState(true, true, false, false, true, documentV2TargetAnchor)},
	{documentV2PhasePublished, documentV2ContractState(false, false, false, false, true, documentV2TargetStage)},
	{documentV2PhaseRollbackAnchored, documentV2ContractState(false, true, false, false, true, documentV2TargetStage)},
	{documentV2PhaseRollbackReady, documentV2ContractState(false, true, true, false, true, documentV2TargetStage)},
	{documentV2PhasePreRestore, documentV2ContractState(false, true, true, true, true, documentV2TargetMissing)},
	{documentV2PhaseRestored, documentV2ContractState(false, true, false, true, true, documentV2TargetAnchor)},
}

var documentV2RecoveryTransitions = [...]documentV2RecoveryTransition{
	{from: documentV2PhasePreNew, decision: documentV2DecisionAbort, action: documentV2ActionFinalizeAbort, terminal: true, proofLast: true},
	{from: documentV2PhasePreNewVacated, decision: documentV2DecisionRollback, action: documentV2ActionCreateAnchor, to: documentV2PhasePreNewRollbackAnchored},
	{from: documentV2PhasePreNewRollbackAnchored, decision: documentV2DecisionRollback, action: documentV2ActionCreateRestoreInstall, to: documentV2PhasePreNewRollbackReady},
	{from: documentV2PhasePreNewRollbackReady, decision: documentV2DecisionRollback, action: documentV2ActionConsumeRestoreInstall, to: documentV2PhasePreNewRestored, canonicalMutation: true},
	{from: documentV2PhasePreNewRestored, decision: documentV2DecisionRollback, action: documentV2ActionFinalizeRollback, terminal: true, proofLast: true},
	{from: documentV2PhasePublished, decision: documentV2DecisionCommit, action: documentV2ActionFinalizeCommit, terminal: true, proofLast: true},
	{from: documentV2PhasePublished, decision: documentV2DecisionRollback, action: documentV2ActionCreateAnchor, to: documentV2PhaseRollbackAnchored},
	{from: documentV2PhaseRollbackAnchored, decision: documentV2DecisionRollback, action: documentV2ActionCreateRestoreInstall, to: documentV2PhaseRollbackReady},
	{from: documentV2PhaseRollbackReady, decision: documentV2DecisionRollback, action: documentV2ActionVacatePublished, to: documentV2PhasePreRestore, canonicalMutation: true},
	{from: documentV2PhasePreRestore, decision: documentV2DecisionRollback, action: documentV2ActionConsumeRestoreInstall, to: documentV2PhaseRestored, canonicalMutation: true},
	{from: documentV2PhaseRestored, decision: documentV2DecisionRollback, action: documentV2ActionFinalizeRollback, terminal: true, proofLast: true},
}

func documentV2ContractState(
	newInstall, anchor, restoreInstall, discard, claim bool,
	target documentV2TargetRelation,
) documentV2RecoveryState {
	return documentV2RecoveryState{
		stage: true, backup: true, witness: true, backupIndependent: true,
		newInstall: newInstall, anchor: anchor, restoreInstall: restoreInstall,
		discard: discard, claim: claim,
		newAliasesStage: newInstall, restoreAliasesAnchor: restoreInstall,
		discardAliasesStage: discard, claimAliasesWitness: claim,
		target: target,
	}
}

func classifyDocumentV2RecoveryState(
	state documentV2RecoveryState,
) (documentV2RecoveryPhase, error) {
	fail := func(reason string) (documentV2RecoveryPhase, error) {
		return documentV2PhaseInvalid, errors.New(reason)
	}
	if !state.stage || !state.witness || !state.backup {
		return fail("document v2 retained stage, witness, and backup are required")
	}
	if !state.backupIndependent {
		return fail("document v2 backup must be an independent original copy")
	}
	if state.newInstall != state.newAliasesStage {
		return fail("document v2 new-install token must alias retained stage")
	}
	if state.restoreInstall != state.restoreAliasesAnchor ||
		state.restoreInstall && !state.anchor {
		return fail("document v2 restore-install token must alias retained anchor")
	}
	if state.discard != state.discardAliasesStage {
		return fail("document v2 discard token must alias retained stage")
	}
	if state.claim != state.claimAliasesWitness {
		return fail("document v2 claim must alias retained witness")
	}
	if state.newInstall && state.discard {
		return fail("document v2 new-install and discard tokens cannot coexist")
	}

	for _, row := range documentV2RecoveryStates {
		if state == row.state {
			return row.phase, nil
		}
	}
	return fail("document v2 recovery state is not a legal protocol phase")
}

func documentV2RecoveryTransitionFor(
	phase documentV2RecoveryPhase,
	decision documentV2RecoveryDecision,
) (documentV2RecoveryTransition, error) {
	for _, transition := range documentV2RecoveryTransitions {
		if transition.from == phase && transition.decision == decision {
			return transition, nil
		}
	}
	return documentV2RecoveryTransition{}, errors.New("document v2 recovery phase has no legal transition for the decision")
}

func executeDocumentV2RecoveryTransitions(
	ctx context.Context,
	initial documentV2RecoveryState,
	decision documentV2RecoveryDecision,
	step documentV2RecoveryStep,
) (documentV2RecoveryExecution, error) {
	phase, err := classifyDocumentV2RecoveryState(initial)
	execution := documentV2RecoveryExecution{phase: phase}
	if err != nil {
		return execution, err
	}
	if step == nil {
		return execution, errors.New("document v2 recovery transition step is nil")
	}
	runCtx := ctx
	var executionErr error
	converging := false
	for {
		if !converging && ctx.Err() != nil {
			return execution, ctx.Err()
		}
		transition, err := documentV2RecoveryTransitionFor(phase, decision)
		if err != nil {
			return execution, err
		}
		if transition.terminal && executionErr != nil && !converging {
			return execution, executionErr
		}
		execution.actions = append(execution.actions, transition.action)
		observation, stepErr := step(runCtx, transition)
		if observation.canonicalMutation {
			if !transition.canonicalMutation {
				return execution, errors.Join(stepErr, errors.New("document v2 transition reported an impossible canonical mutation"))
			}
			converging = true
			runCtx = context.WithoutCancel(ctx)
		}
		if stepErr != nil && !observation.canonicalMutation {
			if observed, classifyErr := classifyDocumentV2RecoveryState(observation.state); classifyErr == nil && (observed == phase || observed == transition.to) {
				execution.phase = observed
			}
			if !converging && executionErr == nil {
				return execution, stepErr
			}
			return execution, errors.Join(executionErr, stepErr)
		}
		if transition.terminal {
			if stepErr != nil {
				return execution, errors.Join(executionErr, stepErr)
			}
			if !transition.proofLast || !observation.terminal || !observation.proofRemoved {
				return execution, errors.New("document v2 terminal recovery did not remove proof last")
			}
			return execution, executionErr
		}
		if observation.terminal || observation.proofRemoved {
			return execution, errors.New("document v2 recovery transition removed proof before terminal action")
		}
		next, err := classifyDocumentV2RecoveryState(observation.state)
		if err != nil || next != transition.to && (stepErr == nil || next != phase) {
			return execution, errors.Join(
				executionErr,
				stepErr,
				err,
				errors.New("document v2 recovery transition reached an unexpected legal phase"),
			)
		}
		if next == transition.to && transition.canonicalMutation != observation.canonicalMutation {
			return execution, errors.Join(stepErr, errors.New("document v2 transition mutation receipt does not match its oracle"))
		}
		if stepErr != nil {
			if next == phase {
				return execution, errors.Join(executionErr, stepErr)
			}
			executionErr = errors.Join(executionErr, stepErr)
		}
		phase = next
		execution.phase = phase
	}
}

func documentV2MissingTargetWritable(state documentV2RecoveryState) bool {
	if state.target != documentV2TargetMissing {
		return false
	}
	return state.newInstall && state.newAliasesStage ||
		state.restoreInstall && state.anchor && state.restoreAliasesAnchor
}

type documentV2IdentityRole uint8

const (
	documentV2IdentityStage documentV2IdentityRole = iota + 1
	documentV2IdentityNewInstall
	documentV2IdentityAnchor
	documentV2IdentityRestoreInstall
	documentV2IdentityDiscard
	documentV2IdentityWitness
	documentV2IdentityClaim
	documentV2IdentityTarget
)

type documentV2IdentityRelation struct {
	left     documentV2IdentityRole
	right    documentV2IdentityRole
	sameFile bool
}

func validateDocumentV2IdentityRelations(relations []documentV2IdentityRelation) error {
	for _, relation := range relations {
		if !relation.sameFile ||
			documentV2IdentityAliasAllowed(relation.left, relation.right) {
			continue
		}
		return errors.New("document v2 contains a forbidden identity alias")
	}
	return nil
}

func documentV2IdentityAliasAllowed(left, right documentV2IdentityRole) bool {
	allowed := func(first, second documentV2IdentityRole) bool {
		return left == first && right == second || left == second && right == first
	}
	return allowed(documentV2IdentityStage, documentV2IdentityNewInstall) ||
		allowed(documentV2IdentityAnchor, documentV2IdentityRestoreInstall) ||
		allowed(documentV2IdentityStage, documentV2IdentityDiscard) ||
		allowed(documentV2IdentityWitness, documentV2IdentityClaim) ||
		allowed(documentV2IdentityTarget, documentV2IdentityWitness) ||
		allowed(documentV2IdentityTarget, documentV2IdentityStage) ||
		allowed(documentV2IdentityTarget, documentV2IdentityAnchor)
}

func validateDocumentV2ManifestPaths(
	manifest documentTransactionManifest,
	directory string,
) error {
	mode := fs.FileMode(manifest.Mode)
	expected := map[string]string{
		"stage": pathJoin(directory, documentBoundArtifactNameForContract(
			"stage",
			manifest.Target,
			mode,
			manifest.PublishedSize,
			manifest.PublishedSHA256,
		)),
		"new_install": pathJoin(directory, documentBoundArtifactNameForContract(
			"new-install",
			manifest.Target,
			mode,
			manifest.PublishedSize,
			manifest.PublishedSHA256,
		)),
		"anchor": pathJoin(directory, documentRollbackAnchorNameForContract(
			manifest.Target,
			mode,
			manifest.OriginalSize,
			manifest.OriginalSHA256,
			0,
		)),
		"restore_install": pathJoin(directory, documentBoundArtifactNameForContract(
			"restore-install",
			manifest.Target,
			mode,
			manifest.OriginalSize,
			manifest.OriginalSHA256,
		)),
		"discard": pathJoin(directory, documentBoundArtifactNameForContract(
			"discard",
			manifest.Target,
			mode,
			manifest.PublishedSize,
			manifest.PublishedSHA256,
		)),
	}
	actual := map[string]string{
		"stage":           manifest.Stage,
		"new_install":     manifest.NewInstall,
		"anchor":          manifest.Anchor,
		"restore_install": manifest.RestoreInstall,
		"discard":         manifest.Discard,
	}
	seen := make(map[string]string, len(actual))
	for field, value := range actual {
		if value == "" || value != expected[field] {
			return fmt.Errorf("document v2 manifest field %s is not canonical", field)
		}
		if earlier, duplicate := seen[value]; duplicate {
			return fmt.Errorf("document v2 manifest fields %s and %s alias one path", earlier, field)
		}
		seen[value] = field
	}
	targetDirectory, _ := path.Split(manifest.Target)
	if path.Clean(path.Dir(manifest.Stage)) != path.Clean(directory) ||
		path.Clean(path.Dir(manifest.NewInstall)) != path.Clean(directory) ||
		path.Clean(path.Dir(manifest.Anchor)) != path.Clean(directory) ||
		path.Clean(path.Dir(manifest.RestoreInstall)) != path.Clean(directory) ||
		path.Clean(path.Dir(manifest.Discard)) != path.Clean(directory) ||
		path.Clean(targetDirectory) != path.Clean(directory) {
		return errors.New("document v2 manifest artifacts must share the target directory")
	}
	return nil
}
