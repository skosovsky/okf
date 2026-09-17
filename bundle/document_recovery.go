package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

type preparedDocumentRecovery struct {
	root            *os.Root
	snapshot        []publicationNamespaceDiscovery
	groups          []*documentRecoveryGroup
	actions         []documentRecoveryAction
	hooks           documentPublishHooks
	sharedInventory bool
}

type documentRecoveryGroup struct {
	root             *os.Root
	directory        string
	parent           *os.Root
	parentInfo       os.FileInfo
	id               string
	manifest         documentTransactionManifest
	artifacts        map[string]publicationFileSpec
	names            map[string]string
	protocolNames    map[string]string
	invalid          map[string]error
	foreign          map[string]publicationNamespaceDiscovery
	v2State          documentV2RecoveryState
	phase            documentV2RecoveryPhase
	leafState        documentRecoveryLeafState
	leafSpec         publicationFileSpec
	proofKind        string
	terminalOnly     bool
	terminalPlan     documentV2TerminalCleanupPlan
	completed        bool
	inventory        *documentRecoveryInventory
	softKinds        map[string]struct{}
	softConflict     error
	softConflicted   map[string]struct{}
	rollbackSource   string
	transitionCtx    context.Context
	canonicalReceipt bool
}

type documentRecoveryLeafState uint8

const (
	documentLeafForeign documentRecoveryLeafState = iota
	documentLeafOriginal
	documentLeafPublished
	documentLeafMissing
)

type documentRecoveryAction struct {
	group *documentRecoveryGroup
	phase documentV2RecoveryPhase
}

func isDocumentTransactionReservedPath(name string) bool {
	for _, component := range strings.Split(name, "/") {
		if hasASCIIFoldPrefix(component, documentTransactionFamilyPrefix) {
			return true
		}
	}
	return false
}

func documentNamespaceRule() publicationNamespaceRule {
	return publicationNamespaceRule{
		name: "document-v2",
		classify: func(name string) (string, bool) {
			if !hasASCIIFoldPrefix(name, documentTransactionFamilyPrefix) {
				return "unknown", false
			}
			if !strings.HasPrefix(name, documentTransactionPrefix) {
				return "unknown", true
			}
			if slot, _, ok := parseDocumentRollbackAnchorName(name); ok {
				return documentRollbackAnchorProtocolKind(slot), true
			}
			if kind, _, ok := parseDocumentBoundArtifactName(name); ok {
				return kind, true
			}
			for _, kind := range []string{
				"manifest", "claim",
			} {
				prefix := documentTransactionPrefix + kind + "-"
				id := strings.TrimPrefix(name, prefix)
				if strings.HasPrefix(name, prefix) && canonicalDocumentDigest(id) {
					return kind, true
				}
			}
			return "unknown", true
		},
	}
}

func inspectGlobalDocumentRecoveryNamespace(
	ctx context.Context,
	root *os.Root,
	hooks documentPublishHooks,
) (map[string]publicationFileSpec, error) {
	recoveredTargets := make(map[string]publicationFileSpec)
	err := inspectGlobalPublicationRecoveryNamespaceWithDocumentTargets(
		ctx,
		root,
		indexPublishHooks{},
		hooks,
		recoveredTargets,
	)
	return recoveredTargets, err
}

func prepareDocumentRecovery(
	ctx context.Context,
	root *os.Root,
	snapshot []publicationNamespaceDiscovery,
	hooks documentPublishHooks,
) (*preparedDocumentRecovery, error) {
	byID := make(map[string]*documentRecoveryGroup)
	var groups []*documentRecoveryGroup
	var detached []*detachedDocumentArtifact
	fail := func(err error) (*preparedDocumentRecovery, error) {
		for _, group := range groups {
			_ = group.parent.Close()
		}
		return nil, err
	}
	for _, discovery := range snapshot {
		kind, id, ok := parseDocumentArtifactName(discovery.protocolName)
		if !ok || kind == "unknown" {
			return fail(publicationConflict("non-canonical document artifact", nil))
		}
		if isDetachedDocumentArtifact(kind) {
			parent, err := openDirectory(root, discovery.directory)
			if err != nil {
				return fail(err)
			}
			spec, captureErr := capturePublicationFile(ctx, parent, discovery.name)
			closeErr := parent.Close()
			if closeErr != nil {
				return fail(closeErr)
			}
			if captureErr != nil && kind != "backup" && kind != "witness" {
				return fail(captureErr)
			}
			detached = append(detached, &detachedDocumentArtifact{
				discovery: discovery,
				kind:      kind,
				spec:      spec,
				err:       captureErr,
			})
			continue
		}
		key := discovery.directory + "\x00" + id
		group := byID[key]
		if group == nil {
			parent, err := openDirectory(root, discovery.directory)
			if err != nil {
				return fail(err)
			}
			group = &documentRecoveryGroup{
				root: root, directory: discovery.directory, parent: parent,
				parentInfo: discovery.parentInfo, id: id,
				artifacts:     make(map[string]publicationFileSpec),
				names:         make(map[string]string),
				protocolNames: make(map[string]string),
				invalid:       make(map[string]error),
				foreign:       make(map[string]publicationNamespaceDiscovery),
			}
			byID[key] = group
			groups = append(groups, group)
		}
		if _, duplicate := group.names[kind]; duplicate {
			return fail(publicationConflict("duplicate document artifact kind", nil))
		}
		spec, err := capturePublicationFile(ctx, group.parent, discovery.name)
		if err != nil {
			if kind != "claim" && kind != "backup" && kind != "witness" {
				return fail(err)
			}
			group.names[kind], group.protocolNames[kind] =
				discovery.name, discovery.protocolName
			group.invalid[kind] = publicationConflict(
				"document "+kind+" artifact is not an inspectable regular file",
				err,
			)
			group.foreign[kind] = discovery
			continue
		}
		group.artifacts[kind], group.names[kind], group.protocolNames[kind] =
			spec, discovery.name, discovery.protocolName
	}
	targets := make(map[string]struct{})
	manifestGroups := make([]*documentRecoveryGroup, 0, len(groups))
	for _, group := range groups {
		if _, hasManifest := group.artifacts["manifest"]; !hasManifest {
			claim, hasClaim := group.artifacts["claim"]
			if !hasClaim ||
				len(group.names) != 1 ||
				len(group.invalid) != 0 ||
				len(group.foreign) != 0 {
				return fail(publicationConflict(
					"document recovery group has no canonical manifest",
					nil,
				))
			}
			detached = append(detached, &detachedDocumentArtifact{
				discovery: publicationNamespaceDiscovery{
					namespace:    "document-v2",
					kind:         "claim",
					directory:    group.directory,
					name:         group.names["claim"],
					protocolName: group.protocolNames["claim"],
					relative:     pathJoin(group.directory, group.names["claim"]),
					parentInfo:   group.parentInfo,
					info:         claim.info,
				},
				kind: "claim",
				spec: claim,
			})
			_ = group.parent.Close()
			continue
		}
		if err := prepareDocumentManifestRecoveryGroup(ctx, group, detached); err != nil {
			return fail(err)
		}
		key, err := publicationPathPhysicalKey(root, group.manifest.Target)
		if err != nil {
			return fail(err)
		}
		if _, duplicate := targets[key]; duplicate {
			return fail(publicationConflict("duplicate physical document recovery target", nil))
		}
		targets[key] = struct{}{}
		manifestGroups = append(manifestGroups, group)
	}
	groups = manifestGroups
	terminal, err := prepareTerminalDocumentRecoveryGroups(ctx, root, detached)
	if err != nil {
		return fail(err)
	}
	groups = append(groups, terminal...)
	for _, group := range terminal {
		key, keyErr := publicationPathPhysicalKey(root, group.manifest.Target)
		if keyErr != nil {
			return fail(keyErr)
		}
		if _, duplicate := targets[key]; duplicate {
			return fail(publicationConflict("duplicate physical document recovery target", nil))
		}
		targets[key] = struct{}{}
	}
	sort.Slice(groups, func(left, right int) bool {
		return groups[left].manifest.Target < groups[right].manifest.Target
	})
	actions := make([]documentRecoveryAction, len(groups))
	inventory := newDocumentRecoveryInventory(root, snapshot)
	for index, group := range groups {
		group.inventory = inventory
		actions[index] = documentRecoveryAction{group: group, phase: group.phase}
	}
	return &preparedDocumentRecovery{root: root, snapshot: snapshot, groups: groups, actions: actions, hooks: hooks}, nil
}

func prepareDocumentManifestRecoveryGroup(
	ctx context.Context,
	group *documentRecoveryGroup,
	detached []*detachedDocumentArtifact,
) error {
	manifestSpec, ok := group.artifacts["manifest"]
	if !ok || documentManifestDigest(manifestSpec.data) != group.id ||
		group.protocolNames["manifest"] != documentArtifactName("manifest", group.id) ||
		manifestSpec.mode != 0o600 {
		return publicationConflict("invalid document manifest identity", nil)
	}
	if err := json.Unmarshal(manifestSpec.data, &group.manifest); err != nil {
		return err
	}
	canonical, _ := json.Marshal(group.manifest)
	if !bytes.Equal(canonical, manifestSpec.data) ||
		group.manifest.Format != documentTransactionFormat ||
		!validDocumentRecoveryManifest(group) {
		return publicationConflict("invalid document manifest", nil)
	}
	if err := attachDocumentDetachedArtifacts(group, detached); err != nil {
		return err
	}
	if len(group.invalid) != 0 || len(group.foreign) != 0 {
		return publicationConflict("document v2 recovery evidence is not inspectable", nil)
	}
	state, leafSpec, err := normalizeDocumentV2RecoveryState(ctx, group)
	if err != nil {
		return err
	}
	phase, err := classifyDocumentV2RecoveryState(state)
	if err != nil {
		return publicationConflict("invalid document v2 recovery phase", err)
	}
	setDocumentV2RecoveryClassification(group, state, leafSpec, phase)
	if err := configureDocumentV2RecoveryEvidence(ctx, group); err != nil {
		return err
	}
	return nil
}

func setDocumentV2RecoveryClassification(
	group *documentRecoveryGroup,
	state documentV2RecoveryState,
	leafSpec publicationFileSpec,
	phase documentV2RecoveryPhase,
) {
	group.v2State = state
	group.phase = phase
	group.leafSpec = leafSpec
	group.proofKind = ""
	switch state.target {
	case documentV2TargetMissing:
		group.leafState = documentLeafMissing
	case documentV2TargetStage:
		group.leafState = documentLeafPublished
		group.proofKind = "stage"
	case documentV2TargetAnchor:
		group.leafState = documentLeafOriginal
		group.proofKind = documentRollbackAnchorProtocolKind(0)
	case documentV2TargetWitness:
		group.leafState = documentLeafOriginal
		group.proofKind = "witness"
	default:
		group.leafState = documentLeafForeign
	}
}

func documentV2RecoveryArtifactMatchesManifest(
	group *documentRecoveryGroup,
	kind string,
	spec publicationFileSpec,
) bool {
	if spec.info == nil || !spec.complete || spec.mode != fs.FileMode(group.manifest.Mode) {
		return false
	}
	size, digest := group.manifest.OriginalSize, group.manifest.OriginalSHA256
	switch kind {
	case "stage", "new-install", "discard":
		size, digest = group.manifest.PublishedSize, group.manifest.PublishedSHA256
	case "witness", "backup", "claim",
		documentRollbackAnchorProtocolKind(0), "restore-install":
	default:
		return false
	}
	return uint64(spec.size) == size && hex.EncodeToString(spec.digest[:]) == digest
}

func normalizeDocumentV2RecoveryState(
	ctx context.Context,
	group *documentRecoveryGroup,
) (documentV2RecoveryState, publicationFileSpec, error) {
	spec := func(kind string) (publicationFileSpec, bool) {
		value, present := group.artifacts[kind]
		return value, present
	}
	witness, _ := spec("witness")
	stage, _ := spec("stage")
	newInstall, hasNewSpec := spec("new-install")
	anchorKind := documentRollbackAnchorProtocolKind(0)
	anchor, hasAnchorSpec := spec(anchorKind)
	restore, hasRestoreSpec := spec("restore-install")
	discard, hasDiscardSpec := spec("discard")
	_, hasClaimSpec := spec("claim")
	_, hasNewName := group.names["new-install"]
	_, hasAnchorName := group.names[anchorKind]
	_, hasRestoreName := group.names["restore-install"]
	_, hasDiscardName := group.names["discard"]
	_, hasClaimName := group.names["claim"]
	hasNew := hasNewSpec || hasNewName
	hasAnchor := hasAnchorSpec || hasAnchorName
	hasRestore := hasRestoreSpec || hasRestoreName
	hasDiscard := hasDiscardSpec || hasDiscardName
	_, newSoft := group.softKinds["new-install"]
	_, restoreSoft := group.softKinds["restore-install"]
	_, discardSoft := group.softKinds["discard"]
	_, stageSoft := group.softKinds["stage"]
	same := func(left, right publicationFileSpec) bool {
		return left.info != nil && right.info != nil && os.SameFile(left.info, right.info)
	}
	if !stageSoft && same(group.artifacts["manifest"], stage) {
		return documentV2RecoveryState{}, publicationFileSpec{},
			publicationConflict("document v2 retained stage aliases manifest", nil)
	}
	if hasNew && !newSoft && (!hasNewSpec || !same(newInstall, stage)) ||
		hasRestore && !restoreSoft && (!hasRestoreSpec || !hasAnchorSpec || !same(restore, anchor)) ||
		hasDiscard && !discardSoft && (!hasDiscardSpec || !same(discard, stage)) {
		return documentV2RecoveryState{}, publicationFileSpec{},
			publicationConflict("document v2 transition token is detached", nil)
	}
	if hasAnchor && (!hasAnchorSpec ||
		same(anchor, stage) ||
		same(anchor, group.artifacts["manifest"])) {
		return documentV2RecoveryState{}, publicationFileSpec{},
			publicationConflict("document v2 rollback anchor is unavailable or aliases a hard role", nil)
	}
	_, leaf := path.Split(group.manifest.Target)
	leafSpec, err := capturePublicationFile(ctx, group.parent, leaf)
	target := documentV2TargetForeign
	switch {
	case errors.Is(err, fs.ErrNotExist):
		leafSpec = publicationFileSpec{}
		target = documentV2TargetMissing
	case err != nil:
		return documentV2RecoveryState{}, publicationFileSpec{}, err
	case leafSpec.mode != fs.FileMode(group.manifest.Mode):
	case same(leafSpec, witness) && sameDocumentArtifactPayload(leafSpec, witness):
		target = documentV2TargetWitness
	case same(leafSpec, stage) && sameDocumentArtifactPayload(leafSpec, stage):
		target = documentV2TargetStage
	case hasAnchor && same(leafSpec, anchor) && sameDocumentArtifactPayload(leafSpec, anchor):
		target = documentV2TargetAnchor
	}
	hasClaim := hasClaimSpec || hasClaimName ||
		hasAnchor ||
		hasRestore ||
		hasDiscard ||
		hasNew && target == documentV2TargetMissing
	state := documentV2RecoveryState{
		stage:                true,
		backup:               true,
		witness:              true,
		newInstall:           hasNew,
		anchor:               hasAnchor,
		restoreInstall:       hasRestore,
		discard:              hasDiscard,
		claim:                hasClaim,
		newAliasesStage:      hasNew && (newSoft || same(newInstall, stage)),
		restoreAliasesAnchor: hasRestore && (restoreSoft || same(restore, anchor)),
		discardAliasesStage:  hasDiscard && (discardSoft || same(discard, stage)),
		claimAliasesWitness:  hasClaim,
		backupIndependent:    true,
		target:               target,
	}
	return state, leafSpec, nil
}

func configureDocumentV2RecoveryEvidence(
	ctx context.Context,
	group *documentRecoveryGroup,
) error {
	group.softKinds = make(map[string]struct{})
	switch group.phase {
	case documentV2PhasePreNewVacated:
		if err := requireDocumentV2HardEvidence(ctx, group, "stage", "new-install"); err != nil {
			return err
		}
		if !documentV2ExactRecoverySource(ctx, group, "backup", true) {
			return publicationConflict(
				"document v2 pre-new vacated recovery lacks an exact independent backup before anchor durability",
				nil,
			)
		}
		source, err := selectDocumentV2PreNewRecoverySource(ctx, group)
		if err != nil {
			return err
		}
		group.rollbackSource = source
		for _, kind := range []string{"claim", "backup", "witness"} {
			if kind != source && kind != "backup" {
				enableDocumentV2SoftEvidence(group, kind)
			}
		}
	case documentV2PhasePreNewRollbackAnchored,
		documentV2PhasePreNewRollbackReady:
		hard := []string{"stage", "new-install", documentRollbackAnchorProtocolKind(0)}
		if group.phase == documentV2PhasePreNewRollbackReady {
			hard = append(hard, "restore-install")
		}
		if err := requireDocumentV2HardEvidence(ctx, group, hard...); err != nil {
			return err
		}
		enableDocumentV2SoftOriginalEvidence(group)
	case documentV2PhaseRollbackAnchored,
		documentV2PhaseRollbackReady:
		hard := []string{"stage", documentRollbackAnchorProtocolKind(0)}
		if group.phase == documentV2PhaseRollbackReady {
			hard = append(hard, "restore-install")
		}
		if err := requireDocumentV2HardEvidence(ctx, group, hard...); err != nil {
			return err
		}
		enableDocumentV2SoftOriginalEvidence(group)
	case documentV2PhasePreRestore:
		if err := requireDocumentV2HardEvidence(
			ctx,
			group,
			"stage",
			documentRollbackAnchorProtocolKind(0),
			"restore-install",
			"discard",
		); err != nil {
			return err
		}
		enableDocumentV2SoftOriginalEvidence(group)
	default:
		if err := requireDocumentV2HardEvidence(ctx, group, "witness", "backup", "stage"); err != nil {
			return err
		}
		for _, kind := range []string{
			"claim",
			"new-install",
			documentRollbackAnchorProtocolKind(0),
			"restore-install",
			"discard",
		} {
			if _, present := group.names[kind]; present {
				if err := requireDocumentV2HardEvidence(ctx, group, kind); err != nil {
					return err
				}
			}
		}
	}
	return validateDocumentV2RecoveryEvidence(ctx, group, group.phase)
}

func validateDocumentV2RecoveryEvidence(
	ctx context.Context,
	group *documentRecoveryGroup,
	phase documentV2RecoveryPhase,
) error {
	if err := verifyPublicationParent(group.root, group.directory, group.parentInfo); err != nil {
		return err
	}
	switch phase {
	case documentV2PhasePreNewVacated:
		if err := requireDocumentV2HardEvidence(
			ctx,
			group,
			"stage",
			"new-install",
			"backup",
			group.rollbackSource,
		); err != nil {
			return err
		}
		if !documentV2ExactRecoverySource(ctx, group, "backup", true) {
			return publicationConflict(
				"document v2 pre-new vacated backup changed before anchor durability",
				nil,
			)
		}
	case documentV2PhasePreNewRollbackAnchored:
		if err := requireDocumentV2HardEvidence(
			ctx, group, "stage", "new-install", documentRollbackAnchorProtocolKind(0),
		); err != nil {
			return err
		}
	case documentV2PhasePreNewRollbackReady:
		if err := requireDocumentV2HardEvidence(
			ctx, group, "stage", "new-install",
			documentRollbackAnchorProtocolKind(0), "restore-install",
		); err != nil {
			return err
		}
	case documentV2PhaseRollbackAnchored:
		if err := requireDocumentV2HardEvidence(
			ctx, group, "stage", documentRollbackAnchorProtocolKind(0),
		); err != nil {
			return err
		}
	case documentV2PhaseRollbackReady:
		if err := requireDocumentV2HardEvidence(
			ctx, group, "stage",
			documentRollbackAnchorProtocolKind(0), "restore-install",
		); err != nil {
			return err
		}
	case documentV2PhasePreRestore:
		if err := requireDocumentV2HardEvidence(
			ctx, group, "stage",
			documentRollbackAnchorProtocolKind(0), "restore-install", "discard",
		); err != nil {
			return err
		}
	}
	return nil
}

func requireDocumentV2HardEvidence(
	ctx context.Context,
	group *documentRecoveryGroup,
	kinds ...string,
) error {
	for _, kind := range kinds {
		if kind == "" {
			return publicationConflict("document v2 recovery has no selected hard source", nil)
		}
		spec, present := group.artifacts[kind]
		if !present ||
			!documentV2RecoveryArtifactMatchesManifest(group, kind, spec) {
			return publicationConflict("document v2 recovery lacks exact hard "+kind, nil)
		}
		if err := verifyPublicationFile(
			ctx,
			group.parent,
			group.names[kind],
			spec,
			true,
		); err != nil {
			return publicationConflict("document v2 hard "+kind+" changed", err)
		}
	}
	if err := validateDocumentV2HardAliases(group); err != nil {
		return err
	}
	return nil
}

func validateDocumentV2HardAliases(group *documentRecoveryGroup) error {
	same := func(left, right string) bool {
		leftSpec, leftOK := group.artifacts[left]
		rightSpec, rightOK := group.artifacts[right]
		return leftOK && rightOK &&
			leftSpec.info != nil && rightSpec.info != nil &&
			os.SameFile(leftSpec.info, rightSpec.info) &&
			sameDocumentArtifactPayload(leftSpec, rightSpec)
	}
	if _, present := group.names["new-install"]; present && !same("new-install", "stage") {
		return publicationConflict("document v2 new-install is detached from stage", nil)
	}
	anchorKind := documentRollbackAnchorProtocolKind(0)
	if _, present := group.names["restore-install"]; present && !same("restore-install", anchorKind) {
		return publicationConflict("document v2 restore-install is detached from anchor", nil)
	}
	if _, present := group.names["discard"]; present && !same("discard", "stage") {
		return publicationConflict("document v2 discard is detached from stage", nil)
	}
	if _, claimSoft := group.softKinds["claim"]; !claimSoft {
		if _, witnessSoft := group.softKinds["witness"]; !witnessSoft {
			if _, present := group.names["claim"]; present && !same("claim", "witness") {
				return publicationConflict("document v2 claim is detached from witness", nil)
			}
		}
	}
	if _, backupSoft := group.softKinds["backup"]; !backupSoft {
		if _, witnessSoft := group.softKinds["witness"]; !witnessSoft &&
			same("backup", "witness") {
			return publicationConflict("document v2 backup aliases witness", nil)
		}
		if _, stageSoft := group.softKinds["stage"]; !stageSoft &&
			same("backup", "stage") {
			return publicationConflict("document v2 backup aliases stage", nil)
		}
	}
	return nil
}

func selectDocumentV2PreNewRecoverySource(
	ctx context.Context,
	group *documentRecoveryGroup,
) (string, error) {
	if documentV2ExactRecoverySource(ctx, group, "claim", false) {
		return "claim", nil
	}
	if documentV2ExactRecoverySource(ctx, group, "backup", true) {
		return "backup", nil
	}
	return "", publicationConflict(
		"document v2 pre-new vacated recovery has neither exact claim nor independent backup",
		nil,
	)
}

func documentV2ExactRecoverySource(
	ctx context.Context,
	group *documentRecoveryGroup,
	kind string,
	requireIndependent bool,
) bool {
	spec, present := group.artifacts[kind]
	if !present ||
		!exactDocumentRecoveryOriginal(group, spec) ||
		verifyPublicationFile(ctx, group.parent, group.names[kind], spec, true) != nil {
		return false
	}
	if !requireIndependent {
		return true
	}
	for otherKind, other := range group.artifacts {
		if otherKind == kind || other.info == nil || spec.info == nil {
			continue
		}
		if os.SameFile(spec.info, other.info) {
			return false
		}
	}
	return true
}

func enableDocumentV2SoftOriginalEvidence(group *documentRecoveryGroup) {
	if group.softKinds == nil {
		group.softKinds = make(map[string]struct{})
	}
	for _, kind := range []string{"claim", "backup", "witness"} {
		enableDocumentV2SoftEvidence(group, kind)
	}
}

func enableDocumentV2SoftEvidence(group *documentRecoveryGroup, kind string) {
	if group.softKinds == nil {
		group.softKinds = make(map[string]struct{})
	}
	group.softKinds[kind] = struct{}{}
	ensureDocumentV2SoftEvidenceName(group, kind)
	if err := documentV2SoftEvidenceConflict(group, kind); err != nil {
		recordDocumentV2SoftConflict(group, kind, err)
	}
}

func recordDocumentV2SoftConflict(
	group *documentRecoveryGroup,
	kind string,
	err error,
) {
	if err == nil {
		return
	}
	if group.softConflicted == nil {
		group.softConflicted = make(map[string]struct{})
	}
	if _, recorded := group.softConflicted[kind]; recorded {
		return
	}
	group.softConflicted[kind] = struct{}{}
	group.softConflict = errors.Join(group.softConflict, err)
}

func ensureDocumentV2SoftEvidenceName(group *documentRecoveryGroup, kind string) {
	if group.names[kind] != "" {
		return
	}
	var name string
	switch kind {
	case "claim":
		name = documentArtifactName("claim", group.id)
	case "backup", "witness":
		name = documentBoundArtifactNameForContract(
			kind,
			group.manifest.Target,
			fs.FileMode(group.manifest.Mode),
			group.manifest.OriginalSize,
			group.manifest.OriginalSHA256,
		)
	}
	group.names[kind] = name
	group.protocolNames[kind] = name
}

func documentV2SoftEvidenceConflict(
	group *documentRecoveryGroup,
	kind string,
) error {
	spec, present := group.artifacts[kind]
	if !present {
		return publicationConflict("document v2 soft "+kind+" is missing or not exact", nil)
	}
	exact := exactDocumentRecoveryOriginal(group, spec)
	if kind == "stage" || kind == "discard" {
		exact = documentV2RecoveryArtifactMatchesManifest(group, kind, spec)
	}
	if !exact {
		return publicationConflict("document v2 soft "+kind+" is missing or not exact", nil)
	}
	for otherKind, other := range group.artifacts {
		if otherKind == kind ||
			other.info == nil ||
			spec.info == nil ||
			!os.SameFile(spec.info, other.info) {
			continue
		}
		allowed := kind == "claim" && otherKind == "witness" ||
			kind == "witness" && otherKind == "claim" ||
			kind == "stage" && otherKind == "discard" ||
			kind == "discard" && otherKind == "stage"
		if !allowed {
			return publicationConflict("document v2 soft "+kind+" aliases another evidence role", nil)
		}
	}
	return nil
}

func validateDocumentV2RecoveryPlanReadOnly(action documentRecoveryAction) error {
	if action.phase == documentV2PhaseInvalid || action.group == nil {
		return nil
	}
	if action.group.phase != action.phase {
		return publicationConflict("document v2 recovery plan phase changed", nil)
	}
	switch action.phase {
	case documentV2PhasePreNew,
		documentV2PhasePreNewRestored,
		documentV2PhasePublished,
		documentV2PhaseRestored:
		return nil
	case documentV2PhasePreNewVacated,
		documentV2PhasePreNewRollbackAnchored,
		documentV2PhasePreNewRollbackReady,
		documentV2PhaseRollbackAnchored,
		documentV2PhaseRollbackReady,
		documentV2PhasePreRestore:
		return nil
	}
	return publicationConflict(
		"document v2 recovery actions are intentionally disabled during phase planning",
		nil,
	)
}

func (prepared *preparedDocumentRecovery) apply(ctx context.Context) error {
	runCtx := prepared.planContext(ctx)
	durableReceipt := false
	var convergenceErr error
	if !prepared.sharedInventory {
		if err := revalidatePublicationNamespaceSnapshot(
			runCtx, prepared.root, []publicationNamespaceRule{documentNamespaceRule()}, prepared.snapshot,
		); err != nil {
			return err
		}
	}
	if prepared.hooks.afterRecoveryInventory != nil {
		if err := prepared.hooks.afterRecoveryInventory(); err != nil {
			return err
		}
		if err := revalidatePublicationNamespaceSnapshot(
			runCtx, prepared.root, []publicationNamespaceRule{documentNamespaceRule()}, prepared.snapshot,
		); err != nil {
			return err
		}
	}
	for _, action := range prepared.actions {
		if err := revalidateDocumentRecoveryGroup(
			documentRecoveryGroupContext(runCtx, action.group),
			action.group,
		); err != nil {
			return err
		}
	}
	for _, action := range prepared.actions {
		if err := validateDocumentV2RecoveryPlanReadOnly(action); err != nil {
			return err
		}
	}
	for _, action := range prepared.actions {
		actionErr := applyDocumentRecovery(
			documentRecoveryGroupContext(runCtx, action.group),
			action.group,
			prepared.hooks,
		)
		if action.group.canonicalReceipt {
			durableReceipt = true
			runCtx = context.WithoutCancel(ctx)
		}
		var revalidateErr error
		for _, revalidate := range prepared.actions {
			if err := revalidateDocumentRecoveryGroup(
				documentRecoveryGroupContext(runCtx, revalidate.group),
				revalidate.group,
			); err != nil {
				revalidateErr = errors.Join(revalidateErr, err)
			}
		}
		if err := errors.Join(actionErr, revalidateErr); err != nil {
			if !durableReceipt {
				return err
			}
			convergenceErr = errors.Join(convergenceErr, err)
		}
	}
	return convergenceErr
}

func (prepared *preparedDocumentRecovery) planContext(ctx context.Context) context.Context {
	if len(prepared.actions) == 0 {
		return ctx
	}
	for _, action := range prepared.actions {
		if !action.group.terminalOnly {
			return ctx
		}
	}
	return context.WithoutCancel(ctx)
}

func documentRecoveryGroupContext(
	ctx context.Context,
	group *documentRecoveryGroup,
) context.Context {
	if group.terminalOnly {
		return context.WithoutCancel(ctx)
	}
	return ctx
}

func (prepared *preparedDocumentRecovery) close() {
	for _, group := range prepared.groups {
		_ = group.parent.Close()
	}
}

func applyDocumentRecovery(
	ctx context.Context,
	group *documentRecoveryGroup,
	hooks documentPublishHooks,
) error {
	return applyDocumentV2RestartRecovery(ctx, group, hooks)
}

func applyDocumentV2TerminalDecision(
	ctx context.Context,
	group *documentRecoveryGroup,
	hooks documentPublishHooks,
) error {
	convergence := ctx
	if group.phase != documentV2PhasePreNew {
		convergence = context.WithoutCancel(ctx)
	}
	state, leafSpec, err := observeDocumentV2RecoveryGroup(
		convergence,
		group,
		group.phase,
	)
	if err != nil {
		return err
	}
	switch group.phase {
	case documentV2PhasePreNew:
		if !state.newInstall ||
			state.claim ||
			state.anchor ||
			state.restoreInstall ||
			state.discard ||
			state.target != documentV2TargetWitness {
			return publicationConflict("document v2 abort decision is not untouched pre-new", nil)
		}
	case documentV2PhasePublished:
		if state.newInstall ||
			!state.claim ||
			state.anchor ||
			state.restoreInstall ||
			state.discard ||
			state.target != documentV2TargetStage {
			return publicationConflict("document v2 commit decision is not published-only", nil)
		}
	case documentV2PhasePreNewRestored:
		if !state.newInstall ||
			!state.claim ||
			!state.anchor ||
			state.restoreInstall ||
			state.discard ||
			state.target != documentV2TargetAnchor {
			return publicationConflict("document v2 rollback decision is not restored pre-new", nil)
		}
	case documentV2PhaseRestored:
		if state.newInstall ||
			!state.claim ||
			!state.anchor ||
			state.restoreInstall ||
			!state.discard ||
			state.target != documentV2TargetAnchor {
			return publicationConflict("document v2 rollback decision is not restored published state", nil)
		}
	default:
		return publicationConflict("document v2 phase is not a terminal decision", nil)
	}
	setDocumentV2RecoveryClassification(group, state, leafSpec, group.phase)
	if group.softConflict != nil {
		return publicationConflict(
			"document v2 target converged; soft evidence conflict was retained",
			group.softConflict,
		)
	}
	if err := verifyDocumentRecoveryProofTarget(
		convergence,
		group,
		group.proofKind,
	); err != nil {
		return err
	}
	_, leaf := path.Split(group.manifest.Target)
	barriers := documentRecoveryBarrierHooks(hooks, group, "", "")
	if err := syncPublicationPath(
		convergence,
		group.parent,
		leaf,
		group.leafSpec,
		false,
		barriers,
	); err != nil {
		return err
	}
	if err := syncPublicationDirectory(group.parent, barriers); err != nil {
		return err
	}
	if group.phase == documentV2PhasePreNewRestored ||
		group.phase == documentV2PhaseRestored {
		group.canonicalReceipt = true
	}
	state, leafSpec, err = observeDocumentV2RecoveryGroup(
		convergence,
		group,
		group.phase,
	)
	if err != nil {
		return err
	}
	setDocumentV2RecoveryClassification(group, state, leafSpec, group.phase)
	manifest, present := group.artifacts["manifest"]
	if !present {
		return publicationConflict("document v2 terminal decision has no manifest", nil)
	}
	removed, removeErr := guardedRemovePublicationFileAs(
		convergence,
		group.parent,
		group.names["manifest"],
		group.protocolNames["manifest"],
		manifest,
		documentRecoveryRemovalHooks(hooks, group, "manifest"),
	)
	if removed {
		group.inventory.remove(group, "manifest")
		delete(group.artifacts, "manifest")
		delete(group.names, "manifest")
		delete(group.protocolNames, "manifest")
	}
	if removeErr != nil {
		return removeErr
	}
	if !removed {
		return publicationConflict("document v2 terminal manifest was not removed", nil)
	}
	state, leafSpec, err = observeDocumentV2RecoveryGroup(
		convergence,
		group,
		group.phase,
	)
	if err != nil {
		return err
	}
	setDocumentV2RecoveryClassification(group, state, leafSpec, group.phase)
	if err := validateTerminalDocumentRecoveryGroup(group); err != nil {
		return publicationConflict(
			"document v2 terminal decision did not produce a cleanup plan",
			err,
		)
	}
	return applyTerminalDocumentRecovery(convergence, group, hooks)
}

func classifyDocumentRecoveryLeaf(
	ctx context.Context,
	group *documentRecoveryGroup,
) (documentRecoveryLeafState, publicationFileSpec, error) {
	_, leaf := path.Split(group.manifest.Target)
	spec, err := capturePublicationFile(ctx, group.parent, leaf)
	if errors.Is(err, fs.ErrNotExist) {
		return documentLeafMissing, publicationFileSpec{}, nil
	}
	if err != nil || spec.mode != fs.FileMode(group.manifest.Mode) {
		return documentLeafForeign, publicationFileSpec{}, err
	}
	if group.terminalOnly {
		proof, present := group.artifacts[group.proofKind]
		if !present ||
			proof.info == nil ||
			spec.info == nil ||
			!os.SameFile(spec.info, proof.info) ||
			!sameDocumentArtifactPayload(spec, proof) {
			return documentLeafForeign, spec, publicationConflict(
				"terminal document target detached from identity proof",
				nil,
			)
		}
		return group.leafState, spec, nil
	}
	sum := hex.EncodeToString(spec.digest[:])
	if uint64(spec.size) == group.manifest.OriginalSize && sum == group.manifest.OriginalSHA256 {
		if witness, present := group.artifacts["witness"]; present &&
			witness.info != nil &&
			spec.info != nil &&
			os.SameFile(spec.info, witness.info) {
			return documentLeafOriginal, spec, nil
		}
		for slot := 0; slot < maxDocumentRollbackAnchors; slot++ {
			kind := documentRollbackAnchorProtocolKind(slot)
			if anchor, present := group.artifacts[kind]; present &&
				anchor.info != nil &&
				spec.info != nil &&
				os.SameFile(spec.info, anchor.info) {
				return documentLeafOriginal, spec, nil
			}
		}
		return documentLeafForeign, spec, publicationConflict(
			"original recovery leaf is not identity-bound to witness or anchor",
			nil,
		)
	}
	if uint64(spec.size) == group.manifest.PublishedSize && sum == group.manifest.PublishedSHA256 {
		if stage, present := group.artifacts["stage"]; !present ||
			stage.info == nil ||
			spec.info == nil ||
			!os.SameFile(spec.info, stage.info) {
			return documentLeafForeign, spec, publicationConflict(
				"published recovery leaf is not identity-bound to stage",
				nil,
			)
		}
		return documentLeafPublished, spec, nil
	}
	return documentLeafForeign, spec, nil
}

func revalidateDocumentRecoveryGroup(
	ctx context.Context,
	group *documentRecoveryGroup,
) error {
	if err := verifyPublicationParent(group.root, group.directory, group.parentInfo); err != nil {
		return err
	}
	refreshDocumentV2SoftEvidence(context.WithoutCancel(ctx), group)
	if err := group.inventory.revalidate(ctx); err != nil {
		return err
	}
	if group.completed {
		_, leaf := path.Split(group.manifest.Target)
		current, err := capturePublicationFile(context.WithoutCancel(ctx), group.parent, leaf)
		if err != nil ||
			current.info == nil ||
			group.leafSpec.info == nil ||
			!os.SameFile(current.info, group.leafSpec.info) ||
			!sameDocumentArtifactPayload(current, group.leafSpec) {
			return publicationConflict("completed document recovery target changed", err)
		}
		return nil
	}
	for kind, expected := range group.foreign {
		if _, soft := group.softKinds[kind]; soft {
			continue
		}
		name := group.names[kind]
		current, err := group.parent.Lstat(name)
		if err != nil ||
			expected.info == nil ||
			current == nil ||
			!os.SameFile(expected.info, current) ||
			expected.info.Mode().Type() != current.Mode().Type() {
			return publicationConflict(
				"foreign document recovery evidence changed after inventory",
				err,
			)
		}
	}
	for kind, expected := range group.artifacts {
		if _, soft := group.softKinds[kind]; soft {
			continue
		}
		name := group.names[kind]
		if name == "" || expected.info == nil {
			return publicationConflict("document recovery evidence identity is unavailable", nil)
		}
		if err := verifyPublicationFile(
			context.WithoutCancel(ctx),
			group.parent,
			name,
			expected,
			true,
		); err != nil {
			return publicationConflict("document recovery evidence changed after inventory", err)
		}
	}
	if group.phase != documentV2PhaseInvalid {
		state, current, err := normalizeDocumentV2RecoveryState(
			context.WithoutCancel(ctx),
			group,
		)
		if err != nil {
			return err
		}
		phase, classifyErr := classifyDocumentV2RecoveryState(state)
		if classifyErr != nil || phase != group.phase || state != group.v2State {
			return errors.Join(
				classifyErr,
				publicationConflict("document v2 recovery phase changed after planning", nil),
			)
		}
		if state.target == documentV2TargetMissing {
			if current.info != nil {
				return publicationConflict("missing document v2 target appeared", nil)
			}
			return nil
		}
		if group.leafSpec.info == nil ||
			current.info == nil ||
			!os.SameFile(group.leafSpec.info, current.info) ||
			!sameDocumentArtifactPayload(group.leafSpec, current) {
			return publicationConflict("document v2 recovery target changed after planning", nil)
		}
		return nil
	}
	state, current, err := classifyDocumentRecoveryLeaf(context.WithoutCancel(ctx), group)
	if err != nil || state != group.leafState {
		return errors.Join(
			err,
			publicationConflict("document recovery leaf state changed after inventory", nil),
		)
	}
	switch state {
	case documentLeafMissing:
		if current.info != nil {
			return publicationConflict("missing document recovery leaf appeared", nil)
		}
	case documentLeafOriginal, documentLeafPublished:
		if group.leafSpec.info == nil ||
			current.info == nil ||
			!os.SameFile(group.leafSpec.info, current.info) {
			return publicationConflict("document recovery leaf identity changed after inventory", nil)
		}
	default:
		return publicationConflict("document recovery leaf is foreign", nil)
	}
	return nil
}

func refreshDocumentV2SoftEvidence(
	ctx context.Context,
	group *documentRecoveryGroup,
) {
	for kind := range group.softKinds {
		name := group.names[kind]
		if name == "" {
			continue
		}
		previous, hadPrevious := group.artifacts[kind]
		spec, captureErr := capturePublicationFile(ctx, group.parent, name)
		relative := pathJoin(group.directory, name)
		if captureErr == nil {
			if !hadPrevious ||
				previous.info == nil ||
				spec.info == nil ||
				!os.SameFile(previous.info, spec.info) ||
				!sameDocumentArtifactPayload(previous, spec) {
				recordDocumentV2SoftConflict(
					group,
					kind,
					publicationConflict("document v2 soft "+kind+" changed", nil),
				)
			}
			group.artifacts[kind] = spec
			delete(group.invalid, kind)
			delete(group.foreign, kind)
			if group.inventory != nil {
				group.inventory.expected[relative] = publicationNamespaceDiscovery{
					namespace:    "document-v2",
					kind:         kind,
					directory:    group.directory,
					name:         name,
					protocolName: group.protocolNames[kind],
					relative:     relative,
					parentInfo:   group.parentInfo,
					info:         spec.info,
				}
			}
		} else {
			if hadPrevious {
				recordDocumentV2SoftConflict(
					group,
					kind,
					publicationConflict("document v2 soft "+kind+" disappeared or changed type", captureErr),
				)
			}
			delete(group.artifacts, kind)
			if errors.Is(captureErr, fs.ErrNotExist) {
				delete(group.invalid, kind)
				delete(group.foreign, kind)
				if group.inventory != nil {
					delete(group.inventory.expected, relative)
				}
			} else {
				info, statErr := group.parent.Lstat(name)
				group.invalid[kind] = publicationConflict(
					"document v2 soft "+kind+" is not inspectable",
					errors.Join(captureErr, statErr),
				)
				group.foreign[kind] = publicationNamespaceDiscovery{
					namespace:    "document-v2",
					kind:         kind,
					directory:    group.directory,
					name:         name,
					protocolName: group.protocolNames[kind],
					relative:     relative,
					parentInfo:   group.parentInfo,
					info:         info,
				}
				if group.inventory != nil {
					group.inventory.expected[relative] = group.foreign[kind]
				}
			}
		}
		if err := documentV2SoftEvidenceConflict(group, kind); err != nil {
			recordDocumentV2SoftConflict(group, kind, err)
		}
	}
}

func validDocumentRecoveryManifest(group *documentRecoveryGroup) bool {
	manifest := group.manifest
	if ValidateRevisionPath(manifest.Target) != nil ||
		publicationPreservedMode(fs.FileMode(manifest.Mode)) != fs.FileMode(manifest.Mode) ||
		!canonicalDocumentDigest(manifest.OriginalSHA256) ||
		!canonicalDocumentDigest(manifest.PublishedSHA256) ||
		manifest.OriginalSize == manifest.PublishedSize &&
			manifest.OriginalSHA256 == manifest.PublishedSHA256 {
		return false
	}
	directory, leaf := path.Split(manifest.Target)
	return strings.TrimSuffix(directory, "/") == group.directory &&
		leaf != "" &&
		!isReservedTransactionPath(leaf) &&
		validateDocumentV2ManifestPaths(manifest, group.directory) == nil
}

func parseDocumentArtifactName(name string) (kind, id string, ok bool) {
	if slot, binding, anchorOK := parseDocumentRollbackAnchorName(name); anchorOK {
		return documentRollbackAnchorProtocolKind(slot), binding, true
	}
	if kind, binding, boundOK := parseDocumentBoundArtifactName(name); boundOK {
		return kind, binding, true
	}
	for _, kind = range []string{
		"manifest", "claim",
	} {
		prefix := documentTransactionPrefix + kind + "-"
		if strings.HasPrefix(name, prefix) {
			id = strings.TrimPrefix(name, prefix)
			return kind, id, canonicalDocumentDigest(id)
		}
	}
	return "unknown", "", false
}

func canonicalDocumentDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
