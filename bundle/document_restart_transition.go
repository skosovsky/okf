package bundle

import (
	"context"
	"errors"
	"path"
)

type restartDocumentRecovery struct {
	group *documentRecoveryGroup
	hooks documentPublishHooks
}

func applyDocumentV2RestartRecovery(
	ctx context.Context,
	group *documentRecoveryGroup,
	hooks documentPublishHooks,
) error {
	if group.terminalOnly {
		group.transitionCtx = context.WithoutCancel(ctx)
		if err := revalidateDocumentRecoveryGroup(group.transitionCtx, group); err != nil {
			return err
		}
		return applyTerminalDocumentRecovery(group.transitionCtx, group, hooks)
	}
	if err := revalidateDocumentRecoveryGroup(ctx, group); err != nil {
		return err
	}
	if group.phase == documentV2PhaseInvalid {
		return publicationConflict("unclassified document recovery state is unsupported", nil)
	}
	decision, err := documentV2RestartDecision(group.phase)
	if err != nil {
		return err
	}
	driver := &restartDocumentRecovery{group: group, hooks: hooks}
	_, err = executeDocumentV2RecoveryTransitions(ctx, group.v2State, decision, driver.step)
	return err
}

func documentV2RestartDecision(phase documentV2RecoveryPhase) (documentV2RecoveryDecision, error) {
	switch phase {
	case documentV2PhasePreNew:
		return documentV2DecisionAbort, nil
	case documentV2PhasePublished:
		return documentV2DecisionCommit, nil
	case documentV2PhasePreNewVacated,
		documentV2PhasePreNewRollbackAnchored,
		documentV2PhasePreNewRollbackReady,
		documentV2PhasePreNewRestored,
		documentV2PhaseRollbackAnchored,
		documentV2PhaseRollbackReady,
		documentV2PhasePreRestore,
		documentV2PhaseRestored:
		return documentV2DecisionRollback, nil
	default:
		return 0, publicationConflict("document v2 restart phase has no recovery decision", nil)
	}
}

func (driver *restartDocumentRecovery) step(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	driver.group.transitionCtx = ctx
	switch transition.action {
	case documentV2ActionCreateAnchor:
		return driver.createAnchor(ctx, transition)
	case documentV2ActionCreateRestoreInstall:
		return driver.createRestoreInstall(ctx, transition)
	case documentV2ActionVacatePublished:
		return driver.vacatePublished(ctx, transition)
	case documentV2ActionConsumeRestoreInstall:
		return driver.consumeRestoreInstall(ctx, transition)
	case documentV2ActionFinalizeAbort,
		documentV2ActionFinalizeCommit,
		documentV2ActionFinalizeRollback:
		return driver.finalize(ctx)
	default:
		return driver.observation(transition.from, false),
			publicationConflict("document v2 restart received an unsupported transition", nil)
	}
}

func (driver *restartDocumentRecovery) createAnchor(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	if transition.from != documentV2PhasePreNewVacated {
		return driver.observation(transition.from, false),
			publicationConflict("document v2 restart cannot create an anchor in this phase", nil)
	}
	kind, err := ensureDocumentV2PreNewRollbackAnchor(ctx, driver.group, driver.hooks)
	if err != nil {
		return driver.observation(transition.from, false), err
	}
	enableDocumentV2SoftOriginalEvidence(driver.group)
	if driver.hooks.afterAnchorSync != nil {
		if err := driver.hooks.afterAnchorSync(driver.group.parent, driver.group.names[kind]); err != nil {
			return driver.observation(transition.to, false), err
		}
	}
	return driver.observe(ctx, transition.to, false)
}

func (driver *restartDocumentRecovery) createRestoreInstall(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	if err := ensureDocumentRecoveryRestoreInstall(ctx, driver.group, driver.hooks); err != nil {
		return driver.observation(transition.from, false), err
	}
	if driver.hooks.afterRestoreSync != nil {
		if err := driver.hooks.afterRestoreSync(
			driver.group.parent,
			driver.group.names["restore-install"],
		); err != nil {
			return driver.observation(transition.to, false), err
		}
	}
	return driver.observe(ctx, transition.to, false)
}

func (driver *restartDocumentRecovery) vacatePublished(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	group := driver.group
	if err := verifyDocumentRecoveryProofTarget(ctx, group, "stage"); err != nil {
		return driver.observation(transition.from, false), err
	}
	stage := group.artifacts["stage"]
	_, leaf := path.Split(group.manifest.Target)
	discardName := path.Base(group.manifest.Discard)
	outcome, vacateErr := guardedVacatePublicationLeafWithOutcome(
		ctx,
		group.parent,
		leaf,
		stage,
		discardName,
		documentRecoveryVacateHooks(driver.hooks, group, discardName),
	)
	if !outcome.renamed ||
		!publicationVacateClaimMatches(stage, outcome.claimed, false) ||
		!outcome.durableReceipt && !outcome.claimed.created {
		return driver.observation(transition.from, false), vacateErr
	}
	convergence := context.WithoutCancel(ctx)
	group.transitionCtx = convergence
	group.canonicalReceipt = outcome.durableReceipt
	discard := outcome.claimed
	if discard.info == nil {
		var captureErr error
		discard, captureErr = capturePublicationFile(convergence, group.parent, discardName)
		vacateErr = errors.Join(vacateErr, captureErr)
	}
	discard.created = true
	group.artifacts["discard"] = discard
	group.names["discard"] = discardName
	group.protocolNames["discard"] = discardName
	group.inventory.add(group, "discard")
	group.leafState = documentLeafMissing
	group.leafSpec = publicationFileSpec{}
	if outcome.durableReceipt && driver.hooks.afterDiscardSync != nil {
		vacateErr = errors.Join(vacateErr, driver.hooks.afterDiscardSync(group.parent, discardName))
	}
	observation, observeErr := driver.observe(convergence, transition.to, true)
	return observation, errors.Join(vacateErr, observeErr)
}

func (driver *restartDocumentRecovery) consumeRestoreInstall(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	group := driver.group
	if transition.from == documentV2PhasePreRestore {
		enableDocumentV2SoftEvidence(group, "stage")
		enableDocumentV2SoftEvidence(group, "discard")
	}
	anchorKind := documentRollbackAnchorProtocolKind(0)
	restoreName := group.names["restore-install"]
	_, leaf := path.Split(group.manifest.Target)
	installed, installErr := guardedConsumePublicationInstallLeaf(
		ctx,
		group.parent,
		restoreName,
		group.artifacts["restore-install"],
		group.names[anchorKind],
		group.artifacts[anchorKind],
		leaf,
		documentRecoveryConsumeRestoreHooks(driver.hooks, group, restoreName, leaf),
	)
	if !installed {
		return driver.observation(transition.from, false), installErr
	}
	convergence := context.WithoutCancel(ctx)
	group.transitionCtx = convergence
	group.inventory.remove(group, "restore-install")
	delete(group.artifacts, "restore-install")
	delete(group.names, "restore-install")
	delete(group.protocolNames, "restore-install")
	observation, observeErr := driver.observe(convergence, transition.to, true)
	return observation, errors.Join(installErr, observeErr)
}

func (driver *restartDocumentRecovery) finalize(
	ctx context.Context,
) (documentV2RecoveryObservation, error) {
	err := applyDocumentV2TerminalDecision(ctx, driver.group, driver.hooks)
	if err != nil {
		return documentV2RecoveryObservation{}, err
	}
	return documentV2RecoveryObservation{terminal: true, proofRemoved: true}, nil
}

func (driver *restartDocumentRecovery) observe(
	ctx context.Context,
	phase documentV2RecoveryPhase,
	canonicalMutation bool,
) (documentV2RecoveryObservation, error) {
	state, leaf, err := observeDocumentV2RecoveryGroup(ctx, driver.group, phase)
	if err != nil {
		return driver.observation(phase, canonicalMutation), err
	}
	setDocumentV2RecoveryClassification(driver.group, state, leaf, phase)
	return documentV2RecoveryObservation{state: state, canonicalMutation: canonicalMutation}, nil
}

func (driver *restartDocumentRecovery) observation(
	phase documentV2RecoveryPhase,
	canonicalMutation bool,
) documentV2RecoveryObservation {
	for _, row := range documentV2RecoveryStates {
		if row.phase == phase {
			return documentV2RecoveryObservation{state: row.state, canonicalMutation: canonicalMutation}
		}
	}
	return documentV2RecoveryObservation{state: documentV2RecoveryState{target: documentV2TargetForeign}}
}
