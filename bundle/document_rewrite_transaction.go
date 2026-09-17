package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const documentTransactionPrefix = ".okf-document-txn-v2-"
const documentTransactionFamilyPrefix = ".okf-document-txn-"
const documentTransactionFormat = "okf-document-transaction-v2"
const maxDocumentRollbackAnchors = 1

type documentPublishHooks struct {
	afterRecoveryInventory func() error
	afterManifestSync      func(parent *os.Root, name string) error
	afterWitnessSync       func(parent *os.Root, name string) error
	afterBackupSync        func(parent *os.Root, name string) error
	afterStageSync         func(parent *os.Root, name string) error
	afterNewInstallSync    func(parent *os.Root, name string) error
	afterAnchorSync        func(parent *os.Root, name string) error
	afterRestoreSync       func(parent *os.Root, name string) error
	afterRestoreConsumed   func(parent *os.Root, name string) error
	afterDiscardSync       func(parent *os.Root, name string) error
	beforeCAS              func(session *DocumentSession) error
	beforeInstall          func(parent *os.Root, stage, leaf string) error
	afterInstall           func(parent *os.Root, leaf string) error
	afterVacateRename      func(parent *os.Root, leaf, claim string) error
	afterVacate            func(parent *os.Root, leaf, claim string) error
	beforeCleanup          func(parent *os.Root, kind, name string) error
	afterCleanup           func(parent *os.Root, kind, name string) error
	fileSync               func(*os.File) error
	directorySync          func(*os.Root) error
	validateContext        func(context.Context) error
}

type documentTransactionManifest struct {
	Format          string `json:"format"`
	Target          string `json:"target"`
	Mode            uint32 `json:"mode"`
	OriginalSize    uint64 `json:"original_size"`
	OriginalSHA256  string `json:"original_sha256"`
	PublishedSize   uint64 `json:"published_size"`
	PublishedSHA256 string `json:"published_sha256"`
	Stage           string `json:"stage"`
	NewInstall      string `json:"new_install"`
	Anchor          string `json:"anchor"`
	RestoreInstall  string `json:"restore_install"`
	Discard         string `json:"discard"`
}

type documentRollbackAnchor struct {
	name string
	spec publicationFileSpec
	live bool
}

type documentRollbackPhase uint8

const (
	documentRollbackInactive documentRollbackPhase = iota
	documentRollbackPreNewBeforeAnchor
	documentRollbackPreNewAfterAnchor
	documentRollbackPreNewInstalling
	documentRollbackPublishedBeforeAnchor
	documentRollbackPublishedBeforeDiscard
	documentRollbackPublishedAfterDiscard
	documentRollbackPublishedInstalling
	documentRollbackRestored
	documentRollbackCleanup
)

type documentTransaction struct {
	manifest           documentTransactionManifest
	manifestData       []byte
	id                 string
	manifestName       string
	backupName         string
	witnessName        string
	stageName          string
	newInstallName     string
	restoreInstallName string
	claimName          string
	manifestSpec       publicationFileSpec
	backup             publicationFileSpec
	witness            publicationFileSpec
	stage              publicationFileSpec
	newInstall         publicationFileSpec
	restoreInstall     publicationFileSpec
	claim              publicationFileSpec
	discardName        string
	discard            publicationFileSpec
	anchors            [maxDocumentRollbackAnchors]documentRollbackAnchor
	manifestLive       bool
	backupLive         bool
	witnessLive        bool
	stageLive          bool
	newInstallLive     bool
	restoreInstallLive bool
	claimLive          bool
	discardLive        bool
	rollbackPhase      documentRollbackPhase
	rollbackSource     documentRollbackSource
	rollbackSourceSet  bool
	scopeEvidenceErr   error
	scopeEvidenceNames map[string]struct{}
}

func rewriteCapturedDocument(
	ctx context.Context,
	session *DocumentSession,
	published []byte,
	hooks documentPublishHooks,
) error {
	return rewriteCapturedDocumentWithRollback(
		ctx, session, published, hooks, convergeDocumentRollback,
	)
}

func rewriteCapturedDocumentWithRollback(
	ctx context.Context,
	session *DocumentSession,
	published []byte,
	hooks documentPublishHooks,
	rollback func(context.Context, *DocumentSession, *documentTransaction, []byte, bool, bool, documentPublishHooks) error,
) (err error) {
	if hooks.beforeCAS != nil {
		if err := hooks.beforeCAS(session); err != nil {
			return err
		}
	}
	state := session.state
	if err := verifyDocumentSessionPreimage(ctx, session); err != nil {
		return err
	}
	if bytes.Equal(state.target.data, published) {
		return verifyDocumentSessionPreimage(ctx, session)
	}
	transaction, err := prepareDocumentTransaction(ctx, session, published, hooks)
	if err != nil {
		return err
	}
	mutated := false
	installed := false
	committed := false
	defer func() {
		if err == nil || committed {
			return
		}
		err = errors.Join(
			err,
			rollback(
				context.WithoutCancel(ctx),
				session,
				transaction,
				published,
				mutated,
				installed,
				hooks,
			),
		)
	}()
	if err := verifyDocumentSessionPreimage(ctx, session); err != nil {
		return err
	}
	claim, err := guardedVacatePublicationLeaf(
		ctx,
		state.parent,
		path.Base(state.relativePath),
		publicationSpecFromDocumentGuard(state.target),
		transaction.claimName,
		documentCoreHooks(ctx, hooks, "claim", transaction.claimName, session, transaction),
	)
	mutated = claim.created
	transaction.claim = claim
	transaction.claimLive = claim.created
	if err != nil {
		return documentConflict("vacate captured document", err)
	}
	if err := verifyDocumentDependencyGuards(ctx, session); err != nil {
		return err
	}
	installHooks := documentCoreHooks(
		context.WithoutCancel(ctx),
		hooks,
		"new-install",
		transaction.newInstallName,
		session,
		transaction,
	)
	installHooks.beforeInstall = func() error {
		if hooks.beforeInstall != nil {
			if err := hooks.beforeInstall(
				state.parent,
				transaction.newInstallName,
				path.Base(state.relativePath),
			); err != nil {
				return err
			}
		}
		return verifyDocumentOriginalEvidence(
			context.WithoutCancel(ctx),
			state.parent,
			transaction,
			publicationSpecFromDocumentGuard(state.target),
		)
	}
	installedNow, installErr := guardedConsumePublicationInstallLeaf(
		context.WithoutCancel(ctx),
		state.parent,
		transaction.newInstallName,
		transaction.newInstall,
		transaction.stageName,
		transaction.stage,
		path.Base(state.relativePath),
		installHooks,
	)
	installed = installedNow
	if installedNow {
		transaction.newInstallLive = false
	}
	if installErr != nil {
		return documentConflict("install rewritten document", installErr)
	}
	if err := verifyDocumentSessionPostimage(
		context.WithoutCancel(ctx),
		session,
		transaction.stage.info,
		published,
	); err != nil {
		return err
	}
	// The installed target and its parent namespace are durable and have passed
	// the full postimage CAS. Cleanup is monotonic post-commit convergence:
	// any later error retains the published leaf and recoverable residue rather
	// than attempting a precommit rollback after original evidence may be gone.
	committed = true
	if err := cleanupDocumentTransaction(
		context.WithoutCancel(ctx),
		session,
		transaction,
		published,
		hooks,
	); err != nil {
		return documentCommittedError("transaction cleanup is pending", err)
	}
	if err := verifyDocumentSessionPostimage(
		context.WithoutCancel(ctx),
		session,
		transaction.stage.info,
		published,
	); err != nil {
		return documentCommittedError("final postimage validation failed", err)
	}
	return nil
}

func documentPreparationStep(
	name string,
	revalidate func(context.Context, []byte) error,
	primitive func(context.Context, []byte) error,
	receipt func() error,
	postverify func(context.Context, []byte) error,
) publicationEngineStep[[]byte, struct{}] {
	return publicationEngineStep[[]byte, struct{}]{
		name:       name,
		revalidate: revalidate,
		primitive: func(ctx context.Context, snapshot []byte) (publicationEngineOutcome[struct{}], error) {
			return publicationEngineOutcome[struct{}]{}, primitive(ctx, snapshot)
		},
		receipt: func(context.Context, []byte, struct{}) error { return receipt() },
		postverify: func(ctx context.Context, snapshot []byte, _ struct{}) error {
			return postverify(ctx, snapshot)
		},
	}
}

func newDocumentPreparationPlan(
	session *DocumentSession,
	transaction *documentTransaction,
	published []byte,
	hooks documentPublishHooks,
) (publicationEnginePlan[[]byte, struct{}], error) {
	state := session.state
	noop := func(context.Context, []byte) error { return nil }
	after := func(hook func(*os.Root, string) error, name string) func() error {
		return func() error {
			if hook == nil {
				return nil
			}
			return hook(state.parent, name)
		}
	}
	verifyBackup := func(ctx context.Context, _ []byte) error {
		if transaction.backup.info == nil || transaction.witness.info == nil ||
			os.SameFile(transaction.backup.info, transaction.witness.info) {
			return publicationConflict("document independent backup aliases original identity witness", nil)
		}
		return verifyDocumentSessionPreimage(ctx, session)
	}
	steps := []publicationEngineStep[[]byte, struct{}]{
		documentPreparationStep("witness", noop, func(ctx context.Context, _ []byte) error {
			spec, err := createDurablePublicationWitness(ctx, state.parent, path.Base(state.relativePath),
				publicationSpecFromDocumentGuard(state.target), transaction.witnessName,
				documentCoreHooks(ctx, hooks, "witness", transaction.witnessName, session, transaction))
			transaction.witness, transaction.witnessLive = spec, spec.created
			return err
		}, after(hooks.afterWitnessSync, transaction.witnessName),
			func(ctx context.Context, _ []byte) error { return verifyDocumentSessionPreimage(ctx, session) }),
		documentPreparationStep("backup", noop, func(ctx context.Context, _ []byte) error {
			spec, err := createDurablePublicationCopy(ctx, state.parent, path.Base(state.relativePath),
				publicationSpecFromDocumentGuard(state.target), transaction.backupName,
				documentCoreHooks(ctx, hooks, "backup", transaction.backupName, session, transaction))
			transaction.backup, transaction.backupLive = spec, spec.created
			return err
		}, func() error { return nil }, noop),
		documentPreparationStep("backup-directory", noop, func(ctx context.Context, _ []byte) error {
			return syncPublicationDirectory(state.parent,
				documentCoreHooks(ctx, hooks, "backup", transaction.backupName, session, transaction))
		}, after(hooks.afterBackupSync, transaction.backupName), verifyBackup),
		documentPreparationStep("stage", noop, func(ctx context.Context, snapshot []byte) error {
			spec, err := createPublicationArtifact(ctx, state.parent, transaction.stageName, state.target.mode, snapshot,
				documentCoreHooks(ctx, hooks, "stage", transaction.stageName, session, transaction))
			transaction.stage, transaction.stageLive = spec, spec.created
			return err
		}, after(hooks.afterStageSync, transaction.stageName), noop),
		documentPreparationStep("new-install", noop, func(ctx context.Context, _ []byte) error {
			spec, err := createDurablePublicationWitness(ctx, state.parent, transaction.stageName, transaction.stage,
				transaction.newInstallName,
				documentCoreHooks(ctx, hooks, "new-install", transaction.newInstallName, session, transaction))
			transaction.newInstall, transaction.newInstallLive = spec, spec.created
			return err
		}, after(hooks.afterNewInstallSync, transaction.newInstallName),
			func(ctx context.Context, snapshot []byte) error {
				return verifyDocumentPreparedArtifacts(ctx, session, transaction, snapshot)
			}),
		documentPreparationStep("manifest", noop, func(ctx context.Context, _ []byte) error {
			spec, err := createPublicationArtifact(ctx, state.parent, transaction.manifestName, 0o600,
				transaction.manifestData,
				documentCoreHooks(ctx, hooks, "manifest", transaction.manifestName, session, transaction))
			transaction.manifestSpec, transaction.manifestLive = spec, spec.created
			return err
		}, after(hooks.afterManifestSync, transaction.manifestName),
			func(ctx context.Context, snapshot []byte) error {
				return verifyDocumentPreparedArtifacts(ctx, session, transaction, snapshot)
			}),
	}
	clone := func(data []byte) []byte { return bytes.Clone(data) }
	return newPublicationEnginePlan(published, clone, func(struct{}) struct{} { return struct{}{} }, steps)
}

func prepareDocumentTransaction(
	ctx context.Context,
	session *DocumentSession,
	published []byte,
	hooks documentPublishHooks,
) (_ *documentTransaction, err error) {
	state := session.state
	directory, _ := path.Split(state.relativePath)
	directory = strings.TrimSuffix(directory, "/")
	stageName := documentBoundArtifactName(
		"stage",
		state.relativePath,
		state.target.mode,
		published,
	)
	newInstallName := documentBoundArtifactName(
		"new-install",
		state.relativePath,
		state.target.mode,
		published,
	)
	anchorName := documentRollbackAnchorName(
		state.relativePath,
		state.target.mode,
		state.target.data,
		0,
	)
	restoreInstallName := documentBoundArtifactName(
		"restore-install",
		state.relativePath,
		state.target.mode,
		state.target.data,
	)
	discardName := documentBoundArtifactName(
		"discard",
		state.relativePath,
		state.target.mode,
		published,
	)
	artifactPath := func(name string) string {
		return pathJoin(directory, name)
	}
	manifest := documentTransactionManifest{
		Format: documentTransactionFormat, Target: state.relativePath,
		Mode:         uint32(state.target.mode),
		OriginalSize: uint64(len(state.target.data)), OriginalSHA256: documentDigest(state.target.data),
		PublishedSize: uint64(len(published)), PublishedSHA256: documentDigest(published),
		Stage:          artifactPath(stageName),
		NewInstall:     artifactPath(newInstallName),
		Anchor:         artifactPath(anchorName),
		RestoreInstall: artifactPath(restoreInstallName),
		Discard:        artifactPath(discardName),
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	id := documentManifestDigest(manifestData)
	transaction := &documentTransaction{
		manifest: manifest, manifestData: manifestData, id: id,
		manifestName: documentArtifactName("manifest", id),
		backupName: documentBoundArtifactName(
			"backup",
			state.relativePath,
			state.target.mode,
			state.target.data,
		),
		witnessName: documentOriginalWitnessName(
			state.relativePath,
			state.target.mode,
			state.target.data,
		),
		stageName:          stageName,
		newInstallName:     newInstallName,
		restoreInstallName: restoreInstallName,
		claimName:          documentArtifactName("claim", id),
		discardName:        discardName,
	}
	for slot := range transaction.anchors {
		transaction.anchors[slot].name = documentRollbackAnchorName(
			state.relativePath,
			state.target.mode,
			state.target.data,
			slot,
		)
	}
	defer func() {
		if err != nil {
			err = errors.Join(
				err,
				cleanupDocumentTransaction(context.WithoutCancel(ctx), session, transaction, nil, hooks),
			)
		}
	}()
	plan, err := newDocumentPreparationPlan(session, transaction, published, hooks)
	if err != nil {
		return nil, err
	}
	if _, err := runPublicationEngine(ctx, plan); err != nil {
		return nil, err
	}
	return transaction, nil
}

func verifyDocumentPreparedArtifacts(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	published []byte,
) error {
	if err := verifyDocumentSessionPreimage(ctx, session); err != nil {
		return err
	}
	if transaction == nil ||
		!transaction.witnessLive ||
		!transaction.backupLive ||
		!transaction.stageLive ||
		!transaction.newInstallLive ||
		transaction.witness.info == nil ||
		transaction.backup.info == nil ||
		transaction.stage.info == nil ||
		transaction.newInstall.info == nil ||
		!transaction.witness.complete ||
		!transaction.backup.complete ||
		!transaction.stage.complete ||
		!transaction.newInstall.complete {
		return publicationConflict("document prepared artifact set is incomplete", nil)
	}
	original := publicationSpecFromDocumentGuard(session.state.target)
	if !os.SameFile(original.info, transaction.witness.info) ||
		os.SameFile(transaction.backup.info, transaction.witness.info) ||
		os.SameFile(transaction.stage.info, transaction.witness.info) ||
		os.SameFile(transaction.stage.info, transaction.backup.info) ||
		!os.SameFile(transaction.stage.info, transaction.newInstall.info) {
		return publicationConflict("document prepared artifact identities violate protocol", nil)
	}
	for _, evidence := range []struct {
		kind string
		name string
		spec publicationFileSpec
		want publicationFileSpec
	}{
		{"witness", transaction.witnessName, transaction.witness, original},
		{"backup", transaction.backupName, transaction.backup, original},
		{
			"stage",
			transaction.stageName,
			transaction.stage,
			publicationSpecFromBytes(transaction.stage.info, published),
		},
		{
			"new-install",
			transaction.newInstallName,
			transaction.newInstall,
			publicationSpecFromBytes(transaction.stage.info, published),
		},
	} {
		if evidence.spec.mode != evidence.want.mode ||
			evidence.spec.size != evidence.want.size ||
			evidence.spec.digest != evidence.want.digest ||
			!bytes.Equal(evidence.spec.data, evidence.want.data) {
			return publicationConflict("document "+evidence.kind+" contract diverged during prepare", nil)
		}
		if err := verifyPublicationFile(
			context.WithoutCancel(ctx),
			session.state.parent,
			evidence.name,
			evidence.spec,
			true,
		); err != nil {
			return publicationConflict("document "+evidence.kind+" changed during prepare", err)
		}
	}
	return nil
}

func cleanupDocumentTransaction(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	published []byte,
	hooks documentPublishHooks,
) error {
	state := session.state
	type cleanupArtifact struct {
		kind string
		name *string
		spec *publicationFileSpec
		live *bool
	}
	precommit := []cleanupArtifact{
		{"manifest", &transaction.manifestName, &transaction.manifestSpec, &transaction.manifestLive},
		{"new-install", &transaction.newInstallName, &transaction.newInstall, &transaction.newInstallLive},
		{"stage", &transaction.stageName, &transaction.stage, &transaction.stageLive},
		{"backup", &transaction.backupName, &transaction.backup, &transaction.backupLive},
		{"witness", &transaction.witnessName, &transaction.witness, &transaction.witnessLive},
	}
	committed := []cleanupArtifact{
		{"manifest", &transaction.manifestName, &transaction.manifestSpec, &transaction.manifestLive},
		{"claim", &transaction.claimName, &transaction.claim, &transaction.claimLive},
		{"backup", &transaction.backupName, &transaction.backup, &transaction.backupLive},
		{"witness", &transaction.witnessName, &transaction.witness, &transaction.witnessLive},
		{"stage", &transaction.stageName, &transaction.stage, &transaction.stageLive},
	}
	artifacts := precommit
	if published != nil {
		if err := verifyPublishedDocument(
			ctx,
			session,
			transaction.stage.info,
			published,
		); err != nil {
			return err
		}
		artifacts = committed
	}
	for _, artifact := range artifacts {
		if !*artifact.live {
			continue
		}
		if _, err := state.parent.Lstat(*artifact.name); errors.Is(err, fs.ErrNotExist) {
			*artifact.live = false
			continue
		} else if err != nil {
			return documentConflict("inspect transaction artifact during cleanup", err)
		}
		if artifact.spec.info == nil {
			return documentConflict(
				"created transaction artifact identity is unavailable; retained for recovery",
				nil,
			)
		}
		var precommitTarget publicationFileSpec
		if published == nil && transaction.witnessLive {
			if err := verifyDocumentPrecommitTargetProof(ctx, session, transaction); err != nil {
				return err
			}
			if artifact.kind == "witness" {
				var captureErr error
				precommitTarget, captureErr = capturePublicationFile(
					context.WithoutCancel(ctx),
					state.parent,
					path.Base(state.relativePath),
				)
				if captureErr != nil {
					return captureErr
				}
			}
		}
		var removed bool
		var removeErr error
		if artifact.spec.complete {
			removed, removeErr = guardedRemovePublicationFile(
				ctx,
				state.parent,
				*artifact.name,
				*artifact.spec,
				documentCoreHooks(ctx, hooks, artifact.kind, *artifact.name, session, transaction),
			)
		} else {
			removed, removeErr = guardedRemovePublicationFileIdentity(
				ctx,
				state.parent,
				*artifact.name,
				artifact.spec.info,
				documentCoreHooks(ctx, hooks, artifact.kind, *artifact.name, session, transaction),
			)
		}
		if removed {
			*artifact.live = false
		}
		if published == nil && artifact.kind == "witness" && removed {
			current, captureErr := capturePublicationFile(
				context.WithoutCancel(ctx),
				state.parent,
				path.Base(state.relativePath),
			)
			original := publicationSpecFromDocumentGuard(state.target)
			if captureErr != nil ||
				current.info == nil ||
				precommitTarget.info == nil ||
				!os.SameFile(current.info, precommitTarget.info) ||
				!os.SameFile(current.info, artifact.spec.info) ||
				!sameDocumentArtifactPayload(current, original) {
				return errors.Join(
					removeErr,
					documentConflict(
						"precommit target changed across final witness cleanup",
						captureErr,
					),
				)
			}
		} else if published == nil && transaction.witnessLive {
			removeErr = errors.Join(
				removeErr,
				verifyDocumentPrecommitTargetProof(ctx, session, transaction),
			)
		}
		if removeErr != nil {
			return removeErr
		}
		if published != nil {
			if err := verifyPublishedDocument(
				ctx,
				session,
				transaction.stage.info,
				published,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifyDocumentPrecommitTargetProof(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
) error {
	if transaction == nil ||
		!transaction.witnessLive ||
		transaction.witness.info == nil ||
		!transaction.witness.complete {
		return publicationConflict("precommit original identity witness is unavailable", nil)
	}
	state := session.state
	if err := verifyPublicationFile(
		context.WithoutCancel(ctx),
		state.parent,
		transaction.witnessName,
		transaction.witness,
		true,
	); err != nil {
		return publicationConflict("precommit original identity witness changed", err)
	}
	current, err := capturePublicationFile(
		context.WithoutCancel(ctx),
		state.parent,
		path.Base(state.relativePath),
	)
	original := publicationSpecFromDocumentGuard(state.target)
	if err != nil ||
		current.info == nil ||
		!os.SameFile(current.info, transaction.witness.info) ||
		!sameDocumentArtifactPayload(current, original) {
		return publicationConflict("precommit target detached from original identity witness", err)
	}
	return nil
}

func convergeDocumentRollback(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	published []byte,
	mutated, installed bool,
	hooks documentPublishHooks,
) error {
	return executeActiveDocumentRollback(
		ctx, session, transaction, published, mutated, installed, hooks,
	)
}

func validateDocumentRollbackStableScope(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
) error {
	return validateDocumentTransactionScope(
		ctx,
		session,
		transaction,
		publicationScopeStable,
		"",
		"",
	)
}

func verifyDocumentRollbackParentChain(
	ctx context.Context,
	session *DocumentSession,
) error {
	if err := verifyDocumentSessionDirectories(ctx, session); err != nil {
		return err
	}
	for _, guard := range session.state.discovery.directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(filepath.Clean(guard.path))
		if err != nil ||
			info.Mode()&os.ModeSymlink != 0 ||
			!info.IsDir() ||
			guard.info == nil ||
			!os.SameFile(guard.info, info) {
			return documentConflict("rollback parent chain identity changed", err)
		}
	}
	return nil
}

type documentRollbackSource struct {
	kind string
	name string
	spec publicationFileSpec
}

func verifyDocumentOriginalEvidence(
	ctx context.Context,
	parent *os.Root,
	transaction *documentTransaction,
	original publicationFileSpec,
) error {
	if transaction == nil ||
		!transaction.backupLive ||
		!transaction.witnessLive ||
		!transaction.claimLive ||
		transaction.backup.info == nil ||
		transaction.witness.info == nil ||
		transaction.claim.info == nil ||
		!transaction.backup.complete ||
		!transaction.witness.complete ||
		!transaction.claim.complete {
		return publicationConflict("document original evidence is incomplete before install", nil)
	}
	if os.SameFile(transaction.backup.info, transaction.claim.info) ||
		os.SameFile(transaction.backup.info, transaction.witness.info) {
		return publicationConflict("document backup aliases mutable original identity evidence", nil)
	}
	if !os.SameFile(transaction.claim.info, transaction.witness.info) {
		return publicationConflict("document vacate claim detached from original identity witness", nil)
	}
	for _, evidence := range []struct {
		kind string
		name string
		spec publicationFileSpec
	}{
		{"backup", transaction.backupName, transaction.backup},
		{"witness", transaction.witnessName, transaction.witness},
		{"claim", transaction.claimName, transaction.claim},
	} {
		if evidence.spec.mode != original.mode ||
			evidence.spec.size != original.size ||
			evidence.spec.digest != original.digest ||
			!bytes.Equal(evidence.spec.data, original.data) {
			return publicationConflict("document "+evidence.kind+" contract diverged from original", nil)
		}
		if err := verifyPublicationFile(
			context.WithoutCancel(ctx),
			parent,
			evidence.name,
			evidence.spec,
			true,
		); err != nil {
			return publicationConflict("document "+evidence.kind+" changed before install", err)
		}
	}
	return nil
}

func documentCommittedError(reason string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrPublicationCommitted, reason, err)
}

func validateDocumentTransactionScope(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	phase publicationScopePhase,
	kind string,
	name string,
) error {
	if session == nil || session.state == nil || transaction == nil {
		return publicationConflict("document transaction scope is unavailable", nil)
	}
	if err := verifyDocumentRollbackParentChain(ctx, session); err != nil {
		return err
	}
	softEvidenceCount := len(transaction.scopeEvidenceNames)
	artifacts := documentTransactionLiveArtifacts(transaction)
	if err := validateDocumentTransactionNamespace(
		ctx,
		session,
		transaction,
		artifacts,
		phase,
		name,
	); err != nil {
		return err
	}
	parent := session.state.parent
	for artifactName, expected := range artifacts {
		if phase == publicationScopeRemove && artifactName == name {
			current, err := parent.Lstat(artifactName)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil ||
				expected.info == nil ||
				!os.SameFile(expected.info, current) {
				return publicationConflict(
					"document cleanup evidence changed at publication barrier",
					err,
				)
			}
			continue
		}
		if phase == publicationScopeInstall && artifactName == name {
			if _, statErr := parent.Lstat(artifactName); errors.Is(statErr, fs.ErrNotExist) {
				current, captureErr := capturePublicationFile(
					ctx,
					parent,
					path.Base(session.state.relativePath),
				)
				if captureErr == nil &&
					expected.info != nil &&
					current.info != nil &&
					os.SameFile(expected.info, current.info) &&
					sameDocumentArtifactPayload(expected, current) {
					continue
				}
				return publicationConflict(
					"consumed document install token is not identity-bound to target",
					captureErr,
				)
			}
		}
		if err := verifyPublicationFile(
			ctx,
			parent,
			artifactName,
			expected,
			true,
		); err != nil {
			if artifactName != name &&
				isDocumentRollbackSoftEvidence(transaction, artifactName) {
				recordDocumentRollbackSoftEvidence(transaction, artifactName, err)
				continue
			}
			if phase == publicationScopeInstall &&
				artifactName != name &&
				isKnownInvalidDocumentRollbackAnchor(transaction, artifactName) {
				continue
			}
			if phase == publicationScopeCreate &&
				isDocumentRollbackAnchorName(transaction, artifactName) {
				continue
			}
			return publicationConflict(
				"document transaction evidence changed at publication barrier",
				err,
			)
		}
	}
	if err := validateDocumentRollbackPhaseState(
		ctx,
		session,
		transaction,
		phase,
		name,
	); err != nil {
		return err
	}
	if err := validateDocumentTransactionTarget(
		ctx,
		session,
		transaction,
		phase,
	); err != nil {
		return err
	}
	if transaction.rollbackPhase == documentRollbackCleanup &&
		len(transaction.scopeEvidenceNames) > softEvidenceCount {
		return documentConflict(
			"soft rollback evidence changed before cleanup; retained",
			transaction.scopeEvidenceErr,
		)
	}
	_ = kind
	return nil
}

func documentTransactionLiveArtifacts(
	transaction *documentTransaction,
) map[string]publicationFileSpec {
	artifacts := make(map[string]publicationFileSpec)
	add := func(live bool, name string, spec publicationFileSpec) {
		if live {
			artifacts[name] = spec
		}
	}
	add(transaction.manifestLive, transaction.manifestName, transaction.manifestSpec)
	add(transaction.backupLive, transaction.backupName, transaction.backup)
	add(transaction.witnessLive, transaction.witnessName, transaction.witness)
	add(transaction.stageLive, transaction.stageName, transaction.stage)
	add(
		transaction.newInstallLive,
		transaction.newInstallName,
		transaction.newInstall,
	)
	add(
		transaction.restoreInstallLive,
		transaction.restoreInstallName,
		transaction.restoreInstall,
	)
	add(transaction.claimLive, transaction.claimName, transaction.claim)
	add(transaction.discardLive, transaction.discardName, transaction.discard)
	for index := range transaction.anchors {
		anchor := transaction.anchors[index]
		add(anchor.live, anchor.name, anchor.spec)
	}
	return artifacts
}

func validateDocumentTransactionNamespace(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	artifacts map[string]publicationFileSpec,
	phase publicationScopePhase,
	transitionName string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent := session.state.parent
	file, err := parent.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := file.ReadDir(-1)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(artifacts))
	var transitionInfo os.FileInfo
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entryName := entry.Name()
		_, _, documentReserved := parseDocumentArtifactName(entryName)
		privateDigest, private := parsePrivatePublicationClaimName(entryName)
		if !documentReserved && !private {
			continue
		}
		info, err := parent.Lstat(entryName)
		if err != nil {
			return err
		}
		if expected, present := artifacts[entryName]; present {
			if expected.info != nil && samePublicationEntryObservation(expected.info, info) {
				seen[entryName] = struct{}{}
				continue
			}
			if entryName != transitionName &&
				isDocumentRollbackSoftEvidence(transaction, entryName) {
				recordDocumentRollbackSoftEvidence(
					transaction,
					entryName,
					publicationConflict("soft rollback evidence changed", nil),
				)
				seen[entryName] = struct{}{}
				continue
			}
			sameIdentity := expected.info != nil && os.SameFile(expected.info, info)
			if (phase == publicationScopeCreate || phase == publicationScopeInstall) &&
				entryName != transitionName &&
				isKnownInvalidDocumentRollbackAnchor(transaction, entryName) &&
				info.Mode()&os.ModeSymlink == 0 &&
				info.Mode().IsRegular() {
				seen[entryName] = struct{}{}
				continue
			}
			if sameIdentity &&
				phase == publicationScopeInstall &&
				entryName != transitionName &&
				isDocumentRollbackSoftEvidence(transaction, entryName) {
				recordDocumentRollbackSoftEvidence(
					transaction,
					entryName,
					publicationConflict("soft rollback evidence changed", nil),
				)
				seen[entryName] = struct{}{}
				continue
			}
			if sameIdentity &&
				phase == publicationScopeInstall &&
				entryName != transitionName &&
				isKnownInvalidDocumentRollbackAnchor(transaction, entryName) {
				seen[entryName] = struct{}{}
				continue
			}
			if sameIdentity &&
				phase == publicationScopeCreate &&
				isDocumentRollbackAnchorName(transaction, entryName) {
				seen[entryName] = struct{}{}
				continue
			}
			{
				return publicationConflict(
					"document transaction namespace observation changed",
					nil,
				)
			}
		}
		allowTransition := phase == publicationScopeCreate ||
			phase == publicationScopeVacate ||
			phase == publicationScopeRemove
		if !allowTransition ||
			transitionName == "" ||
			transitionInfo != nil ||
			entryName != transitionName &&
				(!private || !privateClaimBindsName(privateDigest, transitionName)) {
			return publicationConflict(
				fmt.Sprintf(
					"document transaction namespace gained foreign evidence %q while transitioning %q",
					entryName,
					transitionName,
				),
				nil,
			)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return publicationConflict(
				"document transaction transition evidence is not regular",
				nil,
			)
		}
		transitionInfo = info
	}
	for artifactName, expected := range artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, present := seen[artifactName]; present {
			continue
		}
		if phase == publicationScopeInstall && artifactName == transitionName {
			continue
		}
		if artifactName != transitionName &&
			isDocumentRollbackSoftEvidence(transaction, artifactName) {
			recordDocumentRollbackSoftEvidence(
				transaction,
				artifactName,
				publicationConflict("soft rollback evidence disappeared", nil),
			)
			continue
		}
		if phase != publicationScopeRemove || artifactName != transitionName {
			return publicationConflict(
				"document transaction namespace lost evidence",
				nil,
			)
		}
		if transitionInfo != nil &&
			(expected.info == nil || !os.SameFile(expected.info, transitionInfo)) {
			return publicationConflict(
				"document cleanup private claim detached from evidence",
				nil,
			)
		}
	}
	return nil
}

func isDocumentRollbackSoftEvidence(
	transaction *documentTransaction,
	name string,
) bool {
	if transaction == nil || name == "" {
		return false
	}
	originalEvidence := name == transaction.backupName ||
		name == transaction.witnessName ||
		name == transaction.claimName
	switch transaction.rollbackPhase {
	case documentRollbackPreNewBeforeAnchor:
		return transaction.rollbackSourceSet &&
			transaction.rollbackSource.kind == "backup" &&
			originalEvidence &&
			name != transaction.rollbackSource.name
	case documentRollbackPreNewAfterAnchor,
		documentRollbackPreNewInstalling:
		return originalEvidence
	case documentRollbackPublishedBeforeAnchor:
		return transaction.rollbackSourceSet &&
			transaction.rollbackSource.kind == "backup" &&
			originalEvidence &&
			name != transaction.rollbackSource.name
	case documentRollbackPublishedBeforeDiscard:
		return false
	case documentRollbackPublishedAfterDiscard,
		documentRollbackPublishedInstalling,
		documentRollbackRestored,
		documentRollbackCleanup:
		return originalEvidence ||
			name == transaction.stageName ||
			name == transaction.discardName
	default:
		return false
	}
}

func isDocumentRollbackAnchorName(
	transaction *documentTransaction,
	name string,
) bool {
	for index := range transaction.anchors {
		if transaction.anchors[index].name == name {
			return true
		}
	}
	return false
}

func isKnownInvalidDocumentRollbackAnchor(
	transaction *documentTransaction,
	name string,
) bool {
	for index := range transaction.anchors {
		anchor := transaction.anchors[index]
		if anchor.name == name {
			return anchor.live && !anchor.spec.complete
		}
	}
	return false
}

func recordDocumentRollbackSoftEvidence(
	transaction *documentTransaction,
	name string,
	err error,
) {
	if transaction.scopeEvidenceNames == nil {
		transaction.scopeEvidenceNames = make(map[string]struct{})
	}
	if _, recorded := transaction.scopeEvidenceNames[name]; recorded {
		return
	}
	transaction.scopeEvidenceNames[name] = struct{}{}
	transaction.scopeEvidenceErr = errors.Join(
		transaction.scopeEvidenceErr,
		publicationConflict("rollback evidence changed and was preserved: "+name, err),
	)
}

func validateDocumentRollbackPhaseState(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	scopePhase publicationScopePhase,
	transitionName string,
) error {
	if transaction == nil || transaction.rollbackPhase == documentRollbackInactive {
		return nil
	}
	parent := session.state.parent
	original := publicationSpecFromDocumentGuard(session.state.target)

	selectedHard := transaction.rollbackPhase == documentRollbackPreNewBeforeAnchor ||
		transaction.rollbackPhase == documentRollbackPublishedBeforeAnchor ||
		transaction.rollbackPhase == documentRollbackPublishedBeforeDiscard
	if selectedHard {
		if err := verifyDocumentRollbackSelectedSource(
			ctx,
			parent,
			transaction,
			original,
		); err != nil {
			return err
		}
	}

	preNew := transaction.rollbackPhase == documentRollbackPreNewBeforeAnchor ||
		transaction.rollbackPhase == documentRollbackPreNewAfterAnchor ||
		transaction.rollbackPhase == documentRollbackPreNewInstalling
	publishedBeforeReceipt := transaction.rollbackPhase == documentRollbackPublishedBeforeAnchor ||
		transaction.rollbackPhase == documentRollbackPublishedBeforeDiscard
	if preNew {
		if err := verifyDocumentRollbackExactArtifact(
			ctx,
			parent,
			transaction.stageName,
			transaction.stage,
			"rollback stage",
		); err != nil {
			return err
		}
		if err := verifyDocumentRollbackExactArtifact(
			ctx,
			parent,
			transaction.newInstallName,
			transaction.newInstall,
			"rollback new-install token",
		); err != nil {
			return err
		}
		if transaction.stage.info == nil ||
			transaction.newInstall.info == nil ||
			!os.SameFile(transaction.stage.info, transaction.newInstall.info) ||
			!sameDocumentArtifactPayload(transaction.stage, transaction.newInstall) {
			return publicationConflict(
				"rollback new-install token is detached from stage",
				nil,
			)
		}
	} else if publishedBeforeReceipt {
		if err := verifyDocumentRollbackExactArtifact(
			ctx,
			parent,
			transaction.stageName,
			transaction.stage,
			"published rollback stage",
		); err != nil {
			return err
		}
	}

	anchorRequired := transaction.rollbackPhase != documentRollbackPreNewBeforeAnchor &&
		transaction.rollbackPhase != documentRollbackPublishedBeforeAnchor
	if anchorRequired {
		anchor := &transaction.anchors[0]
		if !anchor.live ||
			anchor.spec.info == nil ||
			!anchor.spec.complete ||
			!sameDocumentArtifactPayload(anchor.spec, original) {
			return publicationConflict("rollback anchor contract is incomplete", nil)
		}
		anchorRemovalTransition := scopePhase == publicationScopeRemove &&
			transitionName == anchor.name
		if !anchorRemovalTransition {
			if err := verifyDocumentRollbackExactArtifact(
				ctx,
				parent,
				anchor.name,
				anchor.spec,
				"rollback anchor",
			); err != nil {
				return err
			}
		}
		restoreConsumedTransition := scopePhase == publicationScopeInstall &&
			transitionName == transaction.restoreInstallName
		if transaction.restoreInstallLive && !restoreConsumedTransition {
			if err := verifyDocumentRollbackExactArtifact(
				ctx,
				parent,
				transaction.restoreInstallName,
				transaction.restoreInstall,
				"rollback restore-install token",
			); err != nil {
				return err
			}
			if transaction.restoreInstall.info == nil ||
				!os.SameFile(transaction.restoreInstall.info, anchor.spec.info) ||
				!sameDocumentArtifactPayload(transaction.restoreInstall, anchor.spec) {
				return publicationConflict(
					"restore-install token is detached from rollback anchor",
					nil,
				)
			}
		}
	}
	return nil
}

func verifyDocumentRollbackExactArtifact(
	ctx context.Context,
	parent *os.Root,
	name string,
	spec publicationFileSpec,
	role string,
) error {
	if spec.info == nil || !spec.complete {
		return publicationConflict(role+" contract is incomplete", nil)
	}
	if err := verifyPublicationFile(
		context.WithoutCancel(ctx),
		parent,
		name,
		spec,
		true,
	); err != nil {
		return publicationConflict(role+" changed", err)
	}
	return nil
}

func validateDocumentTransactionTarget(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	phase publicationScopePhase,
) error {
	parent := session.state.parent
	leaf := path.Base(session.state.relativePath)
	current, err := capturePublicationFile(ctx, parent, leaf)
	if transaction.rollbackPhase != documentRollbackInactive {
		return validateDocumentRollbackTargetForPhase(
			transaction,
			phase,
			current,
			err,
		)
	}
	if errors.Is(err, fs.ErrNotExist) {
		if phase == publicationScopeVacate ||
			phase == publicationScopeInstall ||
			phase == publicationScopeCreate && transaction.claimLive {
			return nil
		}
		return publicationConflict("document transaction target disappeared at barrier", err)
	}
	if err != nil {
		return publicationConflict("document transaction target cannot be observed at barrier", err)
	}
	candidates := []publicationFileSpec{
		publicationSpecFromDocumentGuard(session.state.target),
	}
	if transaction.stageLive {
		candidates = append(candidates, transaction.stage)
	}
	for index := range transaction.anchors {
		if transaction.anchors[index].live {
			candidates = append(candidates, transaction.anchors[index].spec)
		}
	}
	for _, candidate := range candidates {
		if candidate.info != nil &&
			current.info != nil &&
			os.SameFile(candidate.info, current.info) &&
			sameDocumentArtifactPayload(candidate, current) {
			return nil
		}
	}
	return publicationConflict("document transaction target changed at publication barrier", nil)
}

func validateDocumentRollbackTargetForPhase(
	transaction *documentTransaction,
	scopePhase publicationScopePhase,
	current publicationFileSpec,
	captureErr error,
) error {
	missing := errors.Is(captureErr, fs.ErrNotExist)
	matches := func(expected publicationFileSpec) bool {
		return captureErr == nil &&
			current.info != nil &&
			expected.info != nil &&
			os.SameFile(current.info, expected.info) &&
			sameDocumentArtifactPayload(current, expected)
	}
	missingOrAnchorDuringInstall := func() bool {
		return missing ||
			scopePhase == publicationScopeInstall &&
				matches(transaction.anchors[0].spec)
	}

	switch transaction.rollbackPhase {
	case documentRollbackPreNewBeforeAnchor,
		documentRollbackPreNewAfterAnchor:
		if missing {
			return nil
		}
	case documentRollbackPreNewInstalling:
		if missingOrAnchorDuringInstall() {
			return nil
		}
	case documentRollbackPublishedBeforeAnchor:
		if matches(transaction.stage) {
			return nil
		}
	case documentRollbackPublishedBeforeDiscard:
		if matches(transaction.stage) ||
			scopePhase == publicationScopeVacate && missing {
			return nil
		}
	case documentRollbackPublishedAfterDiscard:
		if missing {
			return nil
		}
	case documentRollbackPublishedInstalling:
		if missingOrAnchorDuringInstall() {
			return nil
		}
	case documentRollbackRestored,
		documentRollbackCleanup:
		if matches(transaction.anchors[0].spec) {
			return nil
		}
	default:
		return publicationConflict("unknown active rollback target phase", nil)
	}
	return publicationConflict("rollback target violates phase ownership", captureErr)
}

func documentCoreHooks(
	ctx context.Context,
	hooks documentPublishHooks,
	kind, name string,
	session *DocumentSession,
	transaction *documentTransaction,
) publicationBarrierHooks {
	state := session.state
	validateContext := func(validationCtx context.Context) error {
		if hooks.validateContext == nil {
			return nil
		}
		return hooks.validateContext(validationCtx)
	}
	return publicationBarrierHooks{
		fileSync: hooks.fileSync, directorySync: hooks.directorySync,
		validateScope: func(phase publicationScopePhase) error {
			if err := validateContext(ctx); err != nil {
				return err
			}
			return validateDocumentTransactionScope(
				ctx,
				session,
				transaction,
				phase,
				kind,
				name,
			)
		},
		validateCompensation: func() error {
			convergence := context.WithoutCancel(ctx)
			if err := validateContext(convergence); err != nil {
				return err
			}
			return verifyDocumentRollbackParentChain(convergence, session)
		},
		beforeInstall: func() error {
			if hooks.beforeInstall == nil {
				return nil
			}
			return hooks.beforeInstall(state.parent, name, path.Base(state.relativePath))
		},
		afterVacateRename: func() error {
			if hooks.afterVacateRename == nil {
				return nil
			}
			return hooks.afterVacateRename(
				state.parent,
				path.Base(state.relativePath),
				name,
			)
		},
		afterVacate: func() error {
			if hooks.afterVacate == nil {
				return nil
			}
			return hooks.afterVacate(
				state.parent,
				path.Base(state.relativePath),
				name,
			)
		},
		afterInstall: func() error {
			if hooks.afterInstall == nil {
				return nil
			}
			return hooks.afterInstall(state.parent, path.Base(state.relativePath))
		},
		beforeRemove: func() error {
			if hooks.beforeCleanup == nil {
				return nil
			}
			return hooks.beforeCleanup(state.parent, kind, name)
		},
		afterRemove: func() error {
			if hooks.afterCleanup == nil {
				return nil
			}
			return hooks.afterCleanup(state.parent, kind, name)
		},
	}
}

func publicationSpecFromDocumentGuard(guard documentFileGuard) publicationFileSpec {
	return publicationSpecFromBytes(guard.info, guard.data)
}

func documentArtifactName(kind, id string) string {
	return documentTransactionPrefix + kind + "-" + id
}

func documentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func documentManifestDigest(data []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("okf:document-transaction-manifest:v2\x00"))
	_, _ = digest.Write(data)
	return hex.EncodeToString(digest.Sum(nil))
}
