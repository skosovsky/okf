package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
)

const documentRollbackAnchorPrefix = documentTransactionPrefix + "anchor-"
const documentOriginalWitnessPrefix = documentTransactionPrefix + "witness-"

func documentBoundArtifactName(
	kind string,
	target string,
	mode fs.FileMode,
	data []byte,
) string {
	sum := sha256.Sum256(data)
	return documentBoundArtifactNameForContract(
		kind,
		target,
		mode,
		uint64(len(data)),
		hex.EncodeToString(sum[:]),
	)
}

func documentBoundArtifactNameForContract(
	kind string,
	target string,
	mode fs.FileMode,
	size uint64,
	payloadDigest string,
) string {
	return documentTransactionPrefix +
		kind + "-" +
		documentBoundArtifactBindingForContract(
			kind,
			target,
			mode,
			size,
			payloadDigest,
		)
}

func documentBoundArtifactBinding(
	kind string,
	target string,
	mode fs.FileMode,
	data []byte,
) string {
	sum := sha256.Sum256(data)
	return documentBoundArtifactBindingForContract(
		kind,
		target,
		mode,
		uint64(len(data)),
		hex.EncodeToString(sum[:]),
	)
}

func documentBoundArtifactBindingForContract(
	kind string,
	target string,
	mode fs.FileMode,
	size uint64,
	payloadDigest string,
) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("okf:document-bound-artifact:v2\x00"))
	writeDocumentAnchorDigestField(digest, []byte(kind))
	writeDocumentAnchorDigestField(digest, []byte(target))
	var scalar [8]byte
	binary.BigEndian.PutUint64(scalar[:], uint64(publicationPreservedMode(mode)))
	_, _ = digest.Write(scalar[:])
	binary.BigEndian.PutUint64(scalar[:], size)
	_, _ = digest.Write(scalar[:])
	writeDocumentAnchorDigestField(digest, []byte(payloadDigest))
	return hex.EncodeToString(digest.Sum(nil))
}

func parseDocumentBoundArtifactName(name string) (kind, binding string, ok bool) {
	for _, kind = range []string{
		"witness",
		"backup",
		"stage",
		"new-install",
		"restore-install",
		"discard",
	} {
		prefix := documentTransactionPrefix + kind + "-"
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		binding = strings.TrimPrefix(name, prefix)
		return kind, binding, canonicalDocumentDigest(binding)
	}
	return "", "", false
}

func documentRollbackAnchorName(
	target string,
	mode fs.FileMode,
	data []byte,
	slot int,
) string {
	sum := sha256.Sum256(data)
	return documentRollbackAnchorNameForContract(
		target,
		mode,
		uint64(len(data)),
		hex.EncodeToString(sum[:]),
		slot,
	)
}

func documentRollbackAnchorNameForContract(
	target string,
	mode fs.FileMode,
	size uint64,
	payloadDigest string,
	slot int,
) string {
	return documentRollbackAnchorPrefix +
		strconv.Itoa(slot) + "-" +
		documentRollbackAnchorBindingForContract(
			target,
			mode,
			size,
			payloadDigest,
			slot,
		)
}

func documentRollbackAnchorBinding(
	target string,
	mode fs.FileMode,
	data []byte,
	slot int,
) string {
	sum := sha256.Sum256(data)
	return documentRollbackAnchorBindingForContract(
		target,
		mode,
		uint64(len(data)),
		hex.EncodeToString(sum[:]),
		slot,
	)
}

func documentRollbackAnchorBindingForContract(
	target string,
	mode fs.FileMode,
	size uint64,
	payloadDigest string,
	slot int,
) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("okf:document-rollback-anchor:v2\x00"))
	writeDocumentAnchorDigestField(digest, []byte(target))
	var scalar [8]byte
	binary.BigEndian.PutUint64(scalar[:], uint64(publicationPreservedMode(mode)))
	_, _ = digest.Write(scalar[:])
	binary.BigEndian.PutUint64(scalar[:], size)
	_, _ = digest.Write(scalar[:])
	writeDocumentAnchorDigestField(digest, []byte(payloadDigest))
	binary.BigEndian.PutUint64(scalar[:], uint64(slot))
	_, _ = digest.Write(scalar[:])
	return hex.EncodeToString(digest.Sum(nil))
}

func writeDocumentAnchorDigestField(digest interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write(value)
}

func parseDocumentRollbackAnchorName(name string) (slot int, binding string, ok bool) {
	if !strings.HasPrefix(name, documentRollbackAnchorPrefix) {
		return 0, "", false
	}
	rest := strings.TrimPrefix(name, documentRollbackAnchorPrefix)
	parts := strings.Split(rest, "-")
	if len(parts) != 2 {
		return 0, "", false
	}
	slot, err := strconv.Atoi(parts[0])
	if err != nil ||
		slot < 0 ||
		slot >= maxDocumentRollbackAnchors ||
		strconv.Itoa(slot) != parts[0] ||
		!canonicalDocumentDigest(parts[1]) {
		return 0, "", false
	}
	return slot, parts[1], true
}

func documentOriginalWitnessName(
	target string,
	mode fs.FileMode,
	data []byte,
) string {
	return documentBoundArtifactName("witness", target, mode, data)
}

func documentOriginalWitnessBinding(
	target string,
	mode fs.FileMode,
	data []byte,
) string {
	return documentBoundArtifactBinding("witness", target, mode, data)
}

func parseDocumentOriginalWitnessName(name string) (binding string, ok bool) {
	if !strings.HasPrefix(name, documentOriginalWitnessPrefix) {
		return "", false
	}
	kind, binding, ok := parseDocumentBoundArtifactName(name)
	return binding, ok && kind == "witness"
}

func exactDocumentRollbackAnchor(
	ctx context.Context,
	parent *os.Root,
	transaction *documentTransaction,
	original publicationFileSpec,
) (*documentRollbackAnchor, error) {
	for index := range transaction.anchors {
		anchor := &transaction.anchors[index]
		if !anchor.live ||
			anchor.spec.info == nil ||
			!anchor.spec.complete ||
			anchor.spec.mode != original.mode ||
			anchor.spec.size != original.size ||
			anchor.spec.digest != original.digest ||
			!bytes.Equal(anchor.spec.data, original.data) {
			continue
		}
		if err := verifyPublicationFile(
			context.WithoutCancel(ctx),
			parent,
			anchor.name,
			anchor.spec,
			true,
		); err == nil {
			return anchor, nil
		}
	}
	return nil, nil
}

func ensureDocumentRollbackAnchor(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	original publicationFileSpec,
	hooks documentPublishHooks,
) (*documentRollbackAnchor, error) {
	parent := session.state.parent
	source, err := selectDocumentRollbackSource(
		ctx,
		parent,
		transaction,
		original,
	)
	if err != nil {
		return nil, err
	}
	if anchor, _ := exactDocumentRollbackAnchor(ctx, parent, transaction, original); anchor != nil {
		return anchor, nil
	}
	anchor := &transaction.anchors[0]
	if anchor.live {
		return nil, publicationConflict("canonical rollback anchor is invalid", nil)
	}
	spec, err := createDurablePublicationCopy(
		context.WithoutCancel(ctx),
		parent,
		source.name,
		source.spec,
		anchor.name,
		documentCoreHooks(ctx, hooks, "anchor", anchor.name, session, transaction),
	)
	anchor.spec = spec
	anchor.live = spec.created
	if err != nil {
		return nil, err
	}
	if spec.info == nil ||
		os.SameFile(spec.info, transaction.backup.info) ||
		transaction.witness.info != nil && os.SameFile(spec.info, transaction.witness.info) ||
		transaction.claim.info != nil && os.SameFile(spec.info, transaction.claim.info) ||
		transaction.stage.info != nil && os.SameFile(spec.info, transaction.stage.info) {
		return nil, publicationConflict("rollback anchor aliases another evidence role", nil)
	}
	if hooks.afterAnchorSync != nil {
		if err := hooks.afterAnchorSync(parent, anchor.name); err != nil {
			return nil, err
		}
	}
	return anchor, nil
}

func selectDocumentRollbackSource(
	ctx context.Context,
	parent *os.Root,
	transaction *documentTransaction,
	original publicationFileSpec,
) (documentRollbackSource, error) {
	if transaction == nil {
		return documentRollbackSource{}, publicationConflict(
			"rollback source selection has no transaction",
			nil,
		)
	}
	if transaction.rollbackSourceSet {
		if err := verifyDocumentRollbackSelectedSource(
			ctx,
			parent,
			transaction,
			original,
		); err != nil {
			return documentRollbackSource{}, err
		}
		return transaction.rollbackSource, nil
	}

	claim, claimErr := captureDocumentRollbackClaim(
		ctx,
		parent,
		transaction,
		original,
	)
	witnessErr := verifyDocumentRollbackSourceFile(
		ctx,
		parent,
		transaction.witnessName,
		transaction.witness,
		original,
	)
	if claimErr == nil && witnessErr == nil {
		if claim.info == nil ||
			transaction.witness.info == nil ||
			!os.SameFile(claim.info, transaction.witness.info) {
			claimErr = publicationConflict(
				"rollback claim is detached from original identity witness",
				nil,
			)
		}
	}

	backupErr := verifyDocumentRollbackSourceFile(
		ctx,
		parent,
		transaction.backupName,
		transaction.backup,
		original,
	)
	if backupErr == nil {
		backupInfo := transaction.backup.info
		if backupInfo == nil ||
			transaction.witness.info != nil && os.SameFile(backupInfo, transaction.witness.info) ||
			claim.info != nil && os.SameFile(backupInfo, claim.info) ||
			transaction.stage.info != nil && os.SameFile(backupInfo, transaction.stage.info) ||
			transaction.newInstall.info != nil && os.SameFile(backupInfo, transaction.newInstall.info) {
			backupErr = publicationConflict(
				"rollback backup is not independent from another protocol role",
				nil,
			)
		}
	}
	if backupErr != nil {
		return documentRollbackSource{}, publicationConflict(
			"independent rollback backup changed before anchor durability",
			backupErr,
		)
	}

	if claimErr == nil && witnessErr == nil {
		transaction.rollbackSource = documentRollbackSource{
			kind: "claim",
			name: transaction.claimName,
			spec: claim,
		}
		transaction.rollbackSourceSet = true
		return transaction.rollbackSource, nil
	}
	if claimErr != nil {
		recordDocumentRollbackSoftEvidence(transaction, transaction.claimName, claimErr)
	}
	if witnessErr != nil {
		recordDocumentRollbackSoftEvidence(transaction, transaction.witnessName, witnessErr)
	}
	transaction.rollbackSource = documentRollbackSource{
		kind: "backup",
		name: transaction.backupName,
		spec: transaction.backup,
	}
	transaction.rollbackSourceSet = true
	return transaction.rollbackSource, nil
}

func captureDocumentRollbackClaim(
	ctx context.Context,
	parent *os.Root,
	transaction *documentTransaction,
	original publicationFileSpec,
) (publicationFileSpec, error) {
	if !transaction.claimLive {
		return publicationFileSpec{}, publicationConflict(
			"rollback claim is absent",
			nil,
		)
	}
	claim, err := capturePublicationFile(
		context.WithoutCancel(ctx),
		parent,
		transaction.claimName,
	)
	if err != nil {
		return publicationFileSpec{}, err
	}
	claim.created = true
	if transaction.claim.info != nil && !os.SameFile(transaction.claim.info, claim.info) {
		return claim, publicationConflict("rollback claim identity changed", nil)
	}
	if !sameDocumentArtifactPayload(claim, original) {
		return claim, publicationConflict("rollback claim payload changed", nil)
	}
	transaction.claim = claim
	return claim, nil
}

func verifyDocumentRollbackSourceFile(
	ctx context.Context,
	parent *os.Root,
	name string,
	source publicationFileSpec,
	original publicationFileSpec,
) error {
	if source.info == nil ||
		!source.complete ||
		!sameDocumentArtifactPayload(source, original) {
		return publicationConflict("rollback source contract is incomplete", nil)
	}
	if err := verifyPublicationFile(
		context.WithoutCancel(ctx),
		parent,
		name,
		source,
		true,
	); err != nil {
		return publicationConflict("rollback source changed", err)
	}
	return nil
}

func verifyDocumentRollbackSelectedSource(
	ctx context.Context,
	parent *os.Root,
	transaction *documentTransaction,
	original publicationFileSpec,
) error {
	if transaction == nil || !transaction.rollbackSourceSet {
		return publicationConflict("rollback source was not selected", nil)
	}
	if err := verifyDocumentRollbackSourceFile(
		ctx,
		parent,
		transaction.rollbackSource.name,
		transaction.rollbackSource.spec,
		original,
	); err != nil {
		return documentConflict("selected rollback source changed", err)
	}
	return nil
}

func ensureDocumentRestoreInstall(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	anchor *documentRollbackAnchor,
	hooks documentPublishHooks,
) error {
	parent := session.state.parent
	if transaction.restoreInstallLive {
		if transaction.restoreInstall.info == nil ||
			anchor == nil ||
			anchor.spec.info == nil ||
			!os.SameFile(transaction.restoreInstall.info, anchor.spec.info) {
			return publicationConflict("restore-install token is detached from rollback anchor", nil)
		}
		return verifyPublicationFile(
			context.WithoutCancel(ctx),
			parent,
			transaction.restoreInstallName,
			transaction.restoreInstall,
			true,
		)
	}
	spec, err := createDurablePublicationWitness(
		context.WithoutCancel(ctx),
		parent,
		anchor.name,
		anchor.spec,
		transaction.restoreInstallName,
		documentCoreHooks(
			ctx,
			hooks,
			"restore-install",
			transaction.restoreInstallName,
			session,
			transaction,
		),
	)
	transaction.restoreInstall = spec
	transaction.restoreInstallLive = spec.created
	if err != nil {
		return err
	}
	if spec.info == nil ||
		anchor.spec.info == nil ||
		!os.SameFile(spec.info, anchor.spec.info) ||
		!sameDocumentArtifactPayload(spec, anchor.spec) {
		return publicationConflict("restore-install token is detached from rollback anchor", nil)
	}
	if hooks.afterRestoreSync != nil {
		return hooks.afterRestoreSync(parent, transaction.restoreInstallName)
	}
	return nil
}

func cleanupDocumentRollbackTransaction(
	ctx context.Context,
	session *DocumentSession,
	transaction *documentTransaction,
	proof *documentRollbackAnchor,
	hooks documentPublishHooks,
) error {
	if transaction == nil || proof == nil || !proof.live {
		return publicationConflict("rollback cleanup has no retained identity proof", nil)
	}
	parent := session.state.parent
	leaf := path.Base(session.state.relativePath)
	original := publicationSpecFromDocumentGuard(session.state.target)
	verifyTargetAndProof := func() error {
		return verifyDocumentRollbackTarget(ctx, parent, leaf, original, proof)
	}
	if err := verifyTargetAndProof(); err != nil {
		return err
	}
	if transaction.restoreInstallLive {
		return publicationConflict("rollback cleanup started before restore token consumption", nil)
	}
	for _, evidence := range []struct {
		live bool
		name string
		spec publicationFileSpec
		want publicationFileSpec
	}{
		{transaction.backupLive, transaction.backupName, transaction.backup, original},
		{transaction.witnessLive, transaction.witnessName, transaction.witness, original},
		{transaction.claimLive, transaction.claimName, transaction.claim, original},
		{transaction.stageLive, transaction.stageName, transaction.stage, transaction.stage},
	} {
		if !evidence.live ||
			evidence.spec.info == nil ||
			!evidence.spec.complete ||
			evidence.spec.mode != evidence.want.mode ||
			evidence.spec.size != evidence.want.size ||
			evidence.spec.digest != evidence.want.digest ||
			!bytes.Equal(evidence.spec.data, evidence.want.data) ||
			verifyPublicationFile(
				context.WithoutCancel(ctx),
				parent,
				evidence.name,
				evidence.spec,
				true,
			) != nil {
			return publicationConflict("rollback evidence changed; retained for manual convergence", nil)
		}
	}
	if transaction.discardLive {
		if transaction.discard.info == nil ||
			!transaction.discard.complete ||
			transaction.stage.info == nil ||
			!os.SameFile(transaction.discard.info, transaction.stage.info) ||
			transaction.discard.mode != transaction.stage.mode ||
			transaction.discard.size != transaction.stage.size ||
			transaction.discard.digest != transaction.stage.digest ||
			!bytes.Equal(transaction.discard.data, transaction.stage.data) ||
			verifyPublicationFile(
				context.WithoutCancel(ctx),
				parent,
				transaction.discardName,
				transaction.discard,
				true,
			) != nil {
			return publicationConflict("rollback discard evidence changed; retained", nil)
		}
	}
	for index := range transaction.anchors {
		anchor := &transaction.anchors[index]
		if !anchor.live {
			continue
		}
		if anchor.spec.info == nil ||
			!anchor.spec.complete ||
			anchor.spec.mode != original.mode ||
			anchor.spec.size != original.size ||
			anchor.spec.digest != original.digest ||
			!bytes.Equal(anchor.spec.data, original.data) ||
			verifyPublicationFile(
				context.WithoutCancel(ctx),
				parent,
				anchor.name,
				anchor.spec,
				true,
			) != nil {
			return publicationConflict("rollback anchor changed; retained", nil)
		}
	}
	remove := func(kind, name string, spec *publicationFileSpec, live *bool) error {
		if !*live {
			return nil
		}
		if err := verifyTargetAndProof(); err != nil {
			return err
		}
		removed, err := guardedRemovePublicationFile(
			context.WithoutCancel(ctx),
			parent,
			name,
			*spec,
			documentCoreHooks(ctx, hooks, kind, name, session, transaction),
		)
		if removed {
			*live = false
		}
		if err != nil {
			return err
		}
		return verifyTargetAndProof()
	}
	if err := remove(
		"manifest",
		transaction.manifestName,
		&transaction.manifestSpec,
		&transaction.manifestLive,
	); err != nil {
		return err
	}
	if err := remove(
		"restore-install",
		transaction.restoreInstallName,
		&transaction.restoreInstall,
		&transaction.restoreInstallLive,
	); err != nil {
		return err
	}
	if err := remove(
		"new-install",
		transaction.newInstallName,
		&transaction.newInstall,
		&transaction.newInstallLive,
	); err != nil {
		return err
	}
	if err := remove("discard", transaction.discardName, &transaction.discard, &transaction.discardLive); err != nil {
		return err
	}
	if err := remove("claim", transaction.claimName, &transaction.claim, &transaction.claimLive); err != nil {
		return err
	}
	if err := remove("backup", transaction.backupName, &transaction.backup, &transaction.backupLive); err != nil {
		return err
	}
	if err := remove("witness", transaction.witnessName, &transaction.witness, &transaction.witnessLive); err != nil {
		return err
	}
	if err := remove("stage", transaction.stageName, &transaction.stage, &transaction.stageLive); err != nil {
		return err
	}
	// The remaining anchor is a self-authenticating, SameFile identity witness.
	// If this final removal is interrupted, recovery can validate it without
	// trusting content equality alone.
	if err := verifyTargetAndProof(); err != nil {
		return err
	}
	targetBefore, err := capturePublicationFile(
		context.WithoutCancel(ctx),
		parent,
		leaf,
	)
	if err != nil ||
		targetBefore.info == nil ||
		!os.SameFile(targetBefore.info, proof.spec.info) ||
		!sameDocumentArtifactPayload(targetBefore, original) {
		return publicationConflict("rollback target changed before final proof cleanup", err)
	}
	removed, removeErr := guardedRemovePublicationFile(
		context.WithoutCancel(ctx),
		parent,
		proof.name,
		proof.spec,
		documentCoreHooks(ctx, hooks, "anchor", proof.name, session, transaction),
	)
	if removed {
		proof.live = false
	}
	if removeErr != nil {
		return removeErr
	}
	targetAfter, err := capturePublicationFile(
		context.WithoutCancel(ctx),
		parent,
		leaf,
	)
	if err != nil ||
		targetAfter.info == nil ||
		!os.SameFile(targetAfter.info, targetBefore.info) ||
		!sameDocumentArtifactPayload(targetAfter, original) {
		return publicationConflict("rollback target changed across final proof cleanup", err)
	}
	return nil
}

func verifyDocumentRollbackTarget(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	original publicationFileSpec,
	proof *documentRollbackAnchor,
) error {
	if proof == nil || proof.spec.info == nil || !proof.spec.complete {
		return publicationConflict("rollback target has no complete identity proof", nil)
	}
	if err := verifyPublicationFile(
		context.WithoutCancel(ctx),
		parent,
		proof.name,
		proof.spec,
		true,
	); err != nil {
		return publicationConflict("rollback identity proof changed", err)
	}
	if err := verifyPublicationFile(
		context.WithoutCancel(ctx),
		parent,
		leaf,
		original,
		false,
	); err != nil {
		return publicationConflict("rollback target bytes or mode changed", err)
	}
	targetInfo, err := parent.Lstat(leaf)
	if err != nil || !os.SameFile(targetInfo, proof.spec.info) {
		return publicationConflict("rollback target detached from its identity proof", err)
	}
	return nil
}

func documentRollbackAnchorProtocolKind(slot int) string {
	return fmt.Sprintf("anchor-%d", slot)
}
