package bundle

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
)

type activeDocumentRollback struct {
	session      *DocumentSession
	transaction  *documentTransaction
	hooks        documentPublishHooks
	original     publicationFileSpec
	leaf         string
	anchor       *documentRollbackAnchor
	anchorReady  bool
	restoreReady bool
}

func executeActiveDocumentRollback(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	published []byte,
	mutated, installed bool,
	hooks documentPublishHooks,
) error {
	if !mutated {
		return cleanupDocumentTransaction(ctx, session, transaction, nil, hooks)
	}
	_ = published
	driver := &activeDocumentRollback{
		session: session, transaction: transaction, hooks: hooks,
		original: publicationSpecFromDocumentGuard(session.state.target),
		leaf:     path.Base(session.state.relativePath),
	}
	initial := documentV2ContractState(true, false, false, false, true, documentV2TargetMissing)
	transaction.rollbackPhase = documentRollbackPreNewBeforeAnchor
	if installed {
		initial = documentV2ContractState(false, false, false, false, true, documentV2TargetStage)
		transaction.rollbackPhase = documentRollbackPublishedBeforeAnchor
	}
	_, err := executeDocumentV2RecoveryTransitions(
		ctx,
		initial,
		documentV2DecisionRollback,
		driver.step,
	)
	return err
}

func (driver *activeDocumentRollback) step(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	switch transition.action {
	case documentV2ActionCreateAnchor:
		return driver.createAnchor(ctx, transition)
	case documentV2ActionCreateRestoreInstall:
		return driver.createRestoreInstall(ctx, transition)
	case documentV2ActionVacatePublished:
		return driver.vacatePublished(ctx, transition)
	case documentV2ActionConsumeRestoreInstall:
		return driver.consumeRestoreInstall(ctx, transition)
	case documentV2ActionFinalizeRollback:
		return driver.finalize(ctx, transition)
	default:
		return driver.observation(transition.from, false),
			publicationConflict("active document rollback received an unsupported transition", nil)
	}
}

func (driver *activeDocumentRollback) createAnchor(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	transaction := driver.transaction
	if err := verifyDocumentRollbackSourceFile(
		ctx,
		driver.session.state.parent,
		transaction.backupName,
		transaction.backup,
		driver.original,
	); err != nil {
		return driver.observation(transition.from, false), documentConflict(
			"independent rollback backup changed before anchor selection",
			err,
		)
	}
	stepHooks := driver.hooks
	afterAnchorSync := stepHooks.afterAnchorSync
	stepHooks.afterAnchorSync = func(parent *os.Root, name string) error {
		driver.anchorReady = true
		if afterAnchorSync != nil {
			return afterAnchorSync(parent, name)
		}
		return nil
	}
	anchor, err := ensureDocumentRollbackAnchor(
		ctx,
		driver.session,
		transaction,
		driver.original,
		stepHooks,
	)
	if anchor != nil {
		driver.anchor = anchor
	} else if transaction.anchors[0].live {
		driver.anchor = &transaction.anchors[0]
	}
	if driver.anchor == nil || !driver.anchor.live || !driver.anchorReady {
		return driver.observation(transition.from, false), err
	}
	if transition.to == documentV2PhaseRollbackAnchored {
		transaction.rollbackPhase = documentRollbackPublishedBeforeDiscard
	} else {
		transaction.rollbackPhase = documentRollbackPreNewAfterAnchor
	}
	return driver.observation(transition.to, false), err
}

func (driver *activeDocumentRollback) createRestoreInstall(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	if driver.anchor == nil {
		driver.anchor = &driver.transaction.anchors[0]
	}
	stepHooks := driver.hooks
	afterRestoreSync := stepHooks.afterRestoreSync
	stepHooks.afterRestoreSync = func(parent *os.Root, name string) error {
		driver.restoreReady = true
		if afterRestoreSync != nil {
			return afterRestoreSync(parent, name)
		}
		return nil
	}
	err := ensureDocumentRestoreInstall(
		ctx,
		driver.session,
		driver.transaction,
		driver.anchor,
		stepHooks,
	)
	if !driver.transaction.restoreInstallLive || !driver.restoreReady {
		return driver.observation(transition.from, false), err
	}
	return driver.observation(transition.to, false), err
}

func (driver *activeDocumentRollback) vacatePublished(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	state := driver.session.state
	transaction := driver.transaction
	if err := verifyPublicationFile(
		ctx, state.parent, driver.anchor.name, driver.anchor.spec, true,
	); err != nil {
		return driver.observation(transition.from, false),
			documentConflict("rollback anchor changed before target vacate", err)
	}
	if err := verifyDocumentRollbackSelectedSource(
		ctx, state.parent, transaction, driver.original,
	); err != nil {
		return driver.observation(transition.from, false), err
	}
	outcome, vacateErr := guardedVacatePublicationLeafWithOutcome(
		ctx,
		state.parent,
		driver.leaf,
		transaction.stage,
		transaction.discardName,
		documentCoreHooks(ctx, driver.hooks, "discard", transaction.discardName, driver.session, transaction),
	)
	if !outcome.durableReceipt {
		return driver.observation(transition.from, false), vacateErr
	}
	transaction.discard = transaction.stage
	transaction.discard.created = true
	transaction.discardLive = true
	transaction.rollbackPhase = documentRollbackPublishedAfterDiscard
	observation := driver.observation(transition.to, true)
	if !outcome.renamed || outcome.claimed.info == nil || transaction.stage.info == nil ||
		!os.SameFile(outcome.claimed.info, transaction.stage.info) {
		return observation, publicationConflict("rollback discard is detached from retained stage", nil)
	}
	var err error
	if vacateErr != nil {
		err = documentConflict("published rollback vacate completed with retained drift", vacateErr)
	}
	if driver.hooks.afterDiscardSync != nil {
		if hookErr := driver.hooks.afterDiscardSync(state.parent, transaction.discardName); hookErr != nil {
			err = errors.Join(err, documentConflict("post-receipt discard barrier failed", hookErr))
		}
	}
	if scopeErr := validateDocumentRollbackStableScope(ctx, driver.session, transaction); scopeErr != nil {
		err = errors.Join(err, scopeErr)
	}
	return observation, err
}

func (driver *activeDocumentRollback) consumeRestoreInstall(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	state := driver.session.state
	transaction := driver.transaction
	if transition.from == documentV2PhasePreRestore {
		transaction.rollbackPhase = documentRollbackPublishedInstalling
	} else {
		transaction.rollbackPhase = documentRollbackPreNewInstalling
	}
	if err := verifyDocumentRollbackParentChain(ctx, driver.session); err != nil {
		return driver.observation(transition.from, false), err
	}
	current, statErr := state.parent.Lstat(driver.leaf)
	if !errors.Is(statErr, fs.ErrNotExist) {
		if statErr != nil {
			return driver.observation(transition.from, false),
				documentConflict("rollback destination cannot be inspected", statErr)
		}
		if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
			!os.SameFile(current, driver.anchor.spec.info) {
			return driver.observation(transition.from, false),
				documentConflict("rollback destination is occupied by foreign state", nil)
		}
		transaction.rollbackPhase = documentRollbackRestored
		return driver.verifyRestored(ctx, transition, false, nil)
	}
	installed, installErr := guardedConsumePublicationInstallLeaf(
		ctx,
		state.parent,
		transaction.restoreInstallName,
		transaction.restoreInstall,
		driver.anchor.name,
		driver.anchor.spec,
		driver.leaf,
		documentCoreHooks(
			ctx,
			driver.hooks,
			"restore-install",
			transaction.restoreInstallName,
			driver.session,
			transaction,
		),
	)
	if !installed {
		return driver.observation(transition.from, false), installErr
	}
	transaction.restoreInstallLive = false
	transaction.rollbackPhase = documentRollbackRestored
	if installErr != nil {
		return driver.observation(transition.to, true), installErr
	}
	if driver.hooks.afterRestoreConsumed != nil {
		if hookErr := driver.hooks.afterRestoreConsumed(state.parent, transaction.restoreInstallName); hookErr != nil {
			return driver.observation(transition.to, true),
				documentConflict("post-restore consumption barrier failed", hookErr)
		}
	}
	return driver.verifyRestored(ctx, transition, true, nil)
}

func (driver *activeDocumentRollback) verifyRestored(
	ctx context.Context,
	transition documentV2RecoveryTransition,
	mutated bool,
	cause error,
) (documentV2RecoveryObservation, error) {
	state := driver.session.state
	transaction := driver.transaction
	observation := driver.observation(transition.to, mutated)
	if _, err := state.parent.Lstat(transaction.restoreInstallName); !errors.Is(err, fs.ErrNotExist) {
		return observation, errors.Join(cause, publicationConflict("rollback restore-install token was not consumed", err))
	}
	if err := verifyPublicationFile(
		context.WithoutCancel(ctx), state.parent, driver.anchor.name, driver.anchor.spec, true,
	); err != nil {
		return observation, errors.Join(cause, documentConflict("rollback anchor was not retained", err))
	}
	if err := validateDocumentRollbackStableScope(ctx, driver.session, transaction); err != nil {
		return observation, errors.Join(cause, err)
	}
	if err := verifyDocumentRollbackTarget(
		ctx, state.parent, driver.leaf, driver.original, driver.anchor,
	); err != nil {
		return observation, errors.Join(cause, err)
	}
	if transaction.scopeEvidenceErr != nil {
		return observation, errors.Join(cause, documentConflict(
			"original restored from exact rollback anchor; changed evidence preserved",
			transaction.scopeEvidenceErr,
		))
	}
	return observation, cause
}

func (driver *activeDocumentRollback) finalize(
	ctx context.Context,
	transition documentV2RecoveryTransition,
) (documentV2RecoveryObservation, error) {
	driver.transaction.rollbackPhase = documentRollbackCleanup
	err := cleanupDocumentRollbackTransaction(
		ctx, driver.session, driver.transaction, driver.anchor, driver.hooks,
	)
	if err != nil {
		return driver.observation(transition.from, false), err
	}
	return documentV2RecoveryObservation{terminal: true, proofRemoved: true}, nil
}

func (driver *activeDocumentRollback) observation(
	phase documentV2RecoveryPhase,
	canonicalMutation bool,
) documentV2RecoveryObservation {
	for _, row := range documentV2RecoveryStates {
		if row.phase == phase {
			return documentV2RecoveryObservation{
				state: row.state, canonicalMutation: canonicalMutation,
			}
		}
	}
	return documentV2RecoveryObservation{state: documentV2RecoveryState{target: documentV2TargetForeign}}
}
