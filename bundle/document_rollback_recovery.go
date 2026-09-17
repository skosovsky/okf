package bundle

import (
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
)

func ensureDocumentV2PreNewRollbackAnchor(
	ctx context.Context,
	group *documentRecoveryGroup,
	hooks documentPublishHooks,
) (string, error) {
	anchorKind := documentRollbackAnchorProtocolKind(0)
	if _, present := group.names[anchorKind]; present {
		return "", publicationConflict("document v2 pre-new anchor slot is already occupied", nil)
	}
	sourceKind := group.rollbackSource
	source, present := group.artifacts[sourceKind]
	if !present ||
		!exactDocumentRecoveryOriginal(group, source) ||
		verifyPublicationFile(ctx, group.parent, group.names[sourceKind], source, true) != nil {
		return "", publicationConflict("document v2 selected pre-new source changed before anchor durability", nil)
	}
	name := path.Base(group.manifest.Anchor)
	anchor, err := createDurablePublicationCopy(
		ctx,
		group.parent,
		group.names[sourceKind],
		source,
		name,
		documentRecoveryBarrierHooks(hooks, group, anchorKind, name),
	)
	if anchor.created {
		group.artifacts[anchorKind] = anchor
		group.names[anchorKind] = name
		group.protocolNames[anchorKind] = name
		group.inventory.add(group, anchorKind)
	}
	if err != nil {
		return "", err
	}
	if !anchor.created || !exactDocumentRecoveryOriginal(group, anchor) {
		return "", publicationConflict("document v2 durable rollback anchor is not exact", nil)
	}
	return anchorKind, nil
}

func ensureDocumentRecoveryRestoreInstall(
	ctx context.Context,
	group *documentRecoveryGroup,
	hooks documentPublishHooks,
) error {
	anchorKind := documentRollbackAnchorProtocolKind(0)
	anchor, present := group.artifacts[anchorKind]
	if !present || !exactDocumentRecoveryOriginal(group, anchor) {
		return publicationConflict("document v2 restore token has no exact anchor", nil)
	}
	if restore, present := group.artifacts["restore-install"]; present {
		if restore.info == nil ||
			anchor.info == nil ||
			!os.SameFile(restore.info, anchor.info) ||
			!sameDocumentArtifactPayload(restore, anchor) {
			return publicationConflict("document v2 restore token is detached from anchor", nil)
		}
		return verifyPublicationFile(
			ctx,
			group.parent,
			group.names["restore-install"],
			restore,
			true,
		)
	}
	name := path.Base(group.manifest.RestoreInstall)
	restore, err := createDurablePublicationWitness(
		ctx,
		group.parent,
		group.names[anchorKind],
		anchor,
		name,
		documentRecoveryBarrierHooks(hooks, group, "restore-install", name),
	)
	if restore.created {
		group.artifacts["restore-install"] = restore
		group.names["restore-install"] = name
		group.protocolNames["restore-install"] = name
		group.inventory.add(group, "restore-install")
	}
	if err != nil {
		return err
	}
	if restore.info == nil ||
		anchor.info == nil ||
		!os.SameFile(restore.info, anchor.info) ||
		!sameDocumentArtifactPayload(restore, anchor) {
		return publicationConflict("created document v2 restore token is detached", nil)
	}
	return nil
}

func observeDocumentV2RecoveryGroup(
	ctx context.Context,
	group *documentRecoveryGroup,
	want documentV2RecoveryPhase,
) (documentV2RecoveryState, publicationFileSpec, error) {
	if err := verifyPublicationParent(group.root, group.directory, group.parentInfo); err != nil {
		return documentV2RecoveryState{}, publicationFileSpec{}, err
	}
	refreshDocumentV2SoftEvidence(ctx, group)
	if err := group.inventory.revalidate(ctx); err != nil {
		return documentV2RecoveryState{}, publicationFileSpec{}, err
	}
	for kind, expected := range group.artifacts {
		if _, soft := group.softKinds[kind]; soft {
			continue
		}
		if err := verifyPublicationFile(
			ctx,
			group.parent,
			group.names[kind],
			expected,
			true,
		); err != nil {
			return documentV2RecoveryState{}, publicationFileSpec{},
				publicationConflict("document v2 recovery evidence changed during transition", err)
		}
	}
	state, leafSpec, err := normalizeDocumentV2RecoveryState(ctx, group)
	if err != nil {
		return documentV2RecoveryState{}, publicationFileSpec{}, err
	}
	phase, err := classifyDocumentV2RecoveryState(state)
	if err != nil || phase != want {
		return documentV2RecoveryState{}, publicationFileSpec{}, errors.Join(
			err, publicationConflict("document v2 recovery transition reached an unexpected phase", nil),
		)
	}
	if err := validateDocumentV2RecoveryEvidence(ctx, group, want); err != nil {
		return documentV2RecoveryState{}, publicationFileSpec{}, err
	}
	return state, leafSpec, nil
}

func exactDocumentRecoveryOriginal(
	group *documentRecoveryGroup,
	spec publicationFileSpec,
) bool {
	return spec.info != nil &&
		spec.complete &&
		spec.mode == fs.FileMode(group.manifest.Mode) &&
		uint64(spec.size) == group.manifest.OriginalSize &&
		hex.EncodeToString(spec.digest[:]) == group.manifest.OriginalSHA256
}

func verifyDocumentRecoveryProofTarget(
	ctx context.Context,
	group *documentRecoveryGroup,
	proofKind string,
) error {
	proof, present := group.artifacts[proofKind]
	if !present || proof.info == nil || !proof.complete {
		return publicationConflict("document recovery proof is unavailable", nil)
	}
	if err := verifyPublicationFile(
		ctx,
		group.parent,
		group.names[proofKind],
		proof,
		true,
	); err != nil {
		return err
	}
	_, leaf := path.Split(group.manifest.Target)
	target, err := capturePublicationFile(ctx, group.parent, leaf)
	if err != nil ||
		target.info == nil ||
		!os.SameFile(target.info, proof.info) ||
		!sameDocumentArtifactPayload(target, proof) {
		return publicationConflict("document recovery target detached from proof", err)
	}
	group.leafSpec = target
	return nil
}

func documentRecoveryBarrierHooks(
	hooks documentPublishHooks,
	group *documentRecoveryGroup,
	kind string,
	name string,
) publicationBarrierHooks {
	return publicationBarrierHooks{
		fileSync:      hooks.fileSync,
		directorySync: hooks.directorySync,
		validateScope: documentRecoveryScopeValidator(group, kind, name, ""),
	}
}

func documentRecoveryScopeValidator(
	group *documentRecoveryGroup,
	kind string,
	name string,
	source string,
) func(publicationScopePhase) error {
	return documentRecoveryScopeValidatorAs(
		group,
		kind,
		name,
		name,
		source,
	)
}

func documentRecoveryScopeValidatorAs(
	group *documentRecoveryGroup,
	kind string,
	physicalName string,
	canonicalProtocolName string,
	source string,
) func(publicationScopePhase) error {
	return func(phase publicationScopePhase) error {
		return validateDocumentRecoveryScope(
			group,
			phase,
			kind,
			physicalName,
			canonicalProtocolName,
			source,
		)
	}
}

func validateDocumentRecoveryScope(
	group *documentRecoveryGroup,
	phase publicationScopePhase,
	kind string,
	physicalName string,
	canonicalProtocolName string,
	source string,
) error {
	ctx, contextErr := documentRecoveryTransitionContext(group)
	if contextErr != nil {
		return contextErr
	}
	if group == nil ||
		group.root == nil ||
		group.parent == nil ||
		group.parentInfo == nil ||
		group.inventory == nil {
		return publicationConflict("document recovery scope is unavailable", nil)
	}
	if err := verifyPublicationParent(
		group.root,
		group.directory,
		group.parentInfo,
	); err != nil {
		return publicationConflict("document recovery parent detached", err)
	}
	refreshDocumentV2SoftEvidence(ctx, group)
	if err := validateDocumentRecoveryNamespaceTransition(
		group,
		phase,
		kind,
		physicalName,
		canonicalProtocolName,
	); err != nil {
		return err
	}
	for foreignKind, expected := range group.foreign {
		if _, soft := group.softKinds[foreignKind]; soft {
			continue
		}
		current, err := group.parent.Lstat(group.names[foreignKind])
		if err != nil ||
			expected.info == nil ||
			current == nil ||
			!os.SameFile(expected.info, current) ||
			expected.info.Mode().Type() != current.Mode().Type() {
			return publicationConflict(
				"foreign document recovery evidence changed at publication barrier",
				err,
			)
		}
	}
	for artifactKind, expected := range group.artifacts {
		if _, soft := group.softKinds[artifactKind]; soft {
			continue
		}
		artifactName := group.names[artifactKind]
		if (phase == publicationScopeRemove || phase == publicationScopeInstall) &&
			artifactKind == kind {
			current, err := group.parent.Lstat(artifactName)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil ||
				expected.info == nil ||
				!os.SameFile(expected.info, current) {
				return publicationConflict(
					"document recovery cleanup evidence changed at publication barrier",
					err,
				)
			}
			continue
		}
		if err := verifyPublicationFile(
			ctx,
			group.parent,
			artifactName,
			expected,
			true,
		); err != nil {
			return publicationConflict(
				"document recovery evidence changed at publication barrier",
				err,
			)
		}
	}
	return validateDocumentRecoveryLeafTransition(group, phase, physicalName, source)
}

func validateDocumentRecoveryNamespaceTransition(
	group *documentRecoveryGroup,
	phase publicationScopePhase,
	kind string,
	physicalName string,
	canonicalProtocolName string,
) error {
	ctx, contextErr := documentRecoveryTransitionContext(group)
	if contextErr != nil {
		return contextErr
	}
	current, err := discoverPublicationNamespaces(
		ctx,
		group.inventory.root,
		[]publicationNamespaceRule{
			documentNamespaceRule(),
			privatePublicationNamespaceRule(),
		},
	)
	if err != nil {
		return err
	}
	expected := group.inventory.expected
	seen := make(map[string]struct{}, len(expected))
	transitionRelative := pathJoin(group.directory, physicalName)
	var transitionInfo os.FileInfo
	for _, discovery := range current {
		if observation, present := expected[discovery.relative]; present {
			if !os.SameFile(observation.parentInfo, discovery.parentInfo) ||
				!samePublicationEntryObservation(observation.info, discovery.info) {
				return publicationConflict(
					"document recovery namespace observation changed at publication barrier",
					nil,
				)
			}
			seen[discovery.relative] = struct{}{}
			continue
		}
		allowNew := phase == publicationScopeCreate ||
			phase == publicationScopeVacate ||
			phase == publicationScopeRemove
		if !allowNew || discovery.directory != group.directory || transitionInfo != nil {
			return publicationConflict(
				"document recovery namespace gained foreign evidence at publication barrier",
				nil,
			)
		}
		privateDigest, private := parsePrivatePublicationClaimName(discovery.name)
		if discovery.relative != transitionRelative &&
			(!private ||
				!privateClaimBindsName(privateDigest, canonicalProtocolName)) {
			return publicationConflict(
				"document recovery namespace gained an unrelated private claim",
				nil,
			)
		}
		if discovery.info == nil ||
			discovery.info.Mode()&os.ModeSymlink != 0 ||
			!discovery.info.Mode().IsRegular() {
			return publicationConflict(
				"document recovery transition artifact is not regular",
				nil,
			)
		}
		transitionInfo = discovery.info
	}
	for relative, observation := range expected {
		if _, present := seen[relative]; present {
			continue
		}
		if phase != publicationScopeRemove &&
			phase != publicationScopeInstall ||
			relative != transitionRelative {
			return publicationConflict(
				"document recovery namespace lost evidence at publication barrier",
				nil,
			)
		}
		if transitionInfo != nil &&
			(observation.info == nil || !os.SameFile(observation.info, transitionInfo)) {
			return publicationConflict(
				"document recovery cleanup quarantine detached from evidence",
				nil,
			)
		}
	}
	if phase == publicationScopeVacate && transitionInfo != nil {
		if group.leafSpec.info == nil || !os.SameFile(group.leafSpec.info, transitionInfo) {
			return publicationConflict(
				"document recovery vacate claim detached from target evidence",
				nil,
			)
		}
	}
	if phase == publicationScopeRemove {
		if expectedArtifact, present := group.artifacts[kind]; present &&
			transitionInfo != nil &&
			(expectedArtifact.info == nil ||
				!os.SameFile(expectedArtifact.info, transitionInfo)) {
			return publicationConflict(
				"document recovery private cleanup claim changed identity",
				nil,
			)
		}
	}
	return nil
}

func validateDocumentRecoveryLeafTransition(
	group *documentRecoveryGroup,
	phase publicationScopePhase,
	transitionName string,
	sourceName string,
) error {
	ctx, contextErr := documentRecoveryTransitionContext(group)
	if contextErr != nil {
		return contextErr
	}
	state, current, err := classifyDocumentRecoveryLeaf(ctx, group)
	if err == nil && state == group.leafState {
		if state == documentLeafMissing ||
			group.leafSpec.info != nil &&
				current.info != nil &&
				os.SameFile(group.leafSpec.info, current.info) {
			return nil
		}
	}
	_, leaf := path.Split(group.manifest.Target)
	switch phase {
	case publicationScopeInstall:
		var source publicationFileSpec
		for artifactKind, artifactName := range group.names {
			if artifactName == sourceName {
				source = group.artifacts[artifactKind]
				break
			}
		}
		target, captureErr := capturePublicationFile(ctx, group.parent, leaf)
		if captureErr == nil &&
			source.info != nil &&
			target.info != nil &&
			os.SameFile(source.info, target.info) &&
			sameDocumentArtifactPayload(source, target) {
			return nil
		}
		return publicationConflict("document recovery install target changed at barrier", captureErr)
	case publicationScopeVacate:
		if _, statErr := group.parent.Lstat(leaf); !errors.Is(statErr, fs.ErrNotExist) {
			return publicationConflict("document recovery vacated target reappeared at barrier", statErr)
		}
		claim, captureErr := capturePublicationFile(
			ctx,
			group.parent,
			transitionName,
		)
		if captureErr == nil &&
			group.leafSpec.info != nil &&
			claim.info != nil &&
			os.SameFile(group.leafSpec.info, claim.info) &&
			sameDocumentArtifactPayload(group.leafSpec, claim) {
			return nil
		}
		return publicationConflict("document recovery vacate evidence changed at barrier", captureErr)
	default:
		return errors.Join(
			err,
			publicationConflict("document recovery target changed at publication barrier", nil),
		)
	}
}

func documentRecoveryTransitionContext(group *documentRecoveryGroup) (context.Context, error) {
	if group != nil && group.transitionCtx != nil {
		return group.transitionCtx, nil
	}
	return nil, publicationConflict("document recovery transition context is unavailable", nil)
}

func documentRecoveryConsumeRestoreHooks(
	hooks documentPublishHooks,
	group *documentRecoveryGroup,
	restore string,
	leaf string,
) publicationBarrierHooks {
	barriers := documentRecoveryBarrierHooks(
		hooks,
		group,
		"restore-install",
		restore,
	)
	barriers.validateScope = documentRecoveryScopeValidator(
		group,
		"restore-install",
		restore,
		restore,
	)
	barriers.beforeInstall = func() error {
		if hooks.beforeInstall == nil {
			return nil
		}
		return hooks.beforeInstall(group.parent, restore, leaf)
	}
	barriers.afterInstall = func() error {
		if hooks.afterInstall == nil {
			return nil
		}
		return hooks.afterInstall(group.parent, leaf)
	}
	barriers.afterDirectorySync = func() error {
		transitionCtx, err := documentRecoveryTransitionContext(group)
		if err != nil {
			return err
		}
		group.transitionCtx = context.WithoutCancel(transitionCtx)
		group.canonicalReceipt = true
		if hooks.afterRestoreConsumed == nil {
			return nil
		}
		return hooks.afterRestoreConsumed(group.parent, restore)
	}
	return barriers
}

func documentRecoveryVacateHooks(
	hooks documentPublishHooks,
	group *documentRecoveryGroup,
	discard string,
) publicationBarrierHooks {
	barriers := documentRecoveryBarrierHooks(hooks, group, "discard", discard)
	barriers.validateCompensation = func() error {
		return verifyPublicationParent(
			group.root,
			group.directory,
			group.parentInfo,
		)
	}
	barriers.afterVacateRename = func() error {
		if hooks.afterVacateRename == nil {
			return nil
		}
		_, leaf := path.Split(group.manifest.Target)
		return hooks.afterVacateRename(group.parent, leaf, discard)
	}
	return barriers
}
