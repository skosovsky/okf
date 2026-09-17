package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
)

var errIndexBatchManifestTerminalNonPrefix = errors.New(
	"manifest-bound terminal cleanup is not a canonical prefix",
)

type indexBatchTargetRelation uint8

const (
	indexBatchTargetForeign indexBatchTargetRelation = iota
	indexBatchTargetAbsent
	indexBatchTargetOld
	indexBatchTargetNew
)

type indexBatchTargetSnapshot struct {
	entry    indexBatchManifestEntry
	item     *indexTransactionInventoryItem
	info     os.FileInfo
	data     []byte
	mode     fs.FileMode
	relation indexBatchTargetRelation
}

type indexBatchV3RecoveryPhase uint8

const (
	indexBatchV3PhaseInvalid indexBatchV3RecoveryPhase = iota
	indexBatchV3PhasePreNew
	indexBatchV3PhasePreNewVacated
	indexBatchV3PhasePreNewRollbackAnchored
	indexBatchV3PhasePreNewRollbackReady
	indexBatchV3PhasePreNewRestored
	indexBatchV3PhasePublished
	indexBatchV3PhaseRollbackAnchored
	indexBatchV3PhaseRollbackReady
	indexBatchV3PhasePreRestore
	indexBatchV3PhaseRestored
	indexBatchV3PhaseCreateRollbackVacated
	indexBatchV3PhaseBlockedAnchored
)

type preparedIndexBatchRecovery struct {
	inventory        []*indexTransactionInventoryItem
	manifest         indexBatchManifest
	manifestItem     *indexTransactionInventoryItem
	entries          []*indexBatchRecoveryEntry
	targets          []indexBatchTargetSnapshot
	committed        bool
	blockedRootErr   error
	blockedEntry     *indexBatchRecoveryEntry
	blockedEntryErr  error
	removed          map[string]struct{}
	extraParents     []*os.Root
	hooks            indexPublishHooks
	sharedInventory  bool
	terminalCommit   []*indexBatchTerminalCommitGroup
	manifestBoundary *indexTransactionInventoryItem
}

type indexBatchTerminalCommitGroup struct {
	destination    string
	stage          *indexTransactionInventoryItem
	backup         *indexTransactionInventoryItem
	claim          *indexTransactionInventoryItem
	anchor         *indexTransactionInventoryItem
	witness        *indexTransactionInventoryItem
	newInstall     *indexTransactionInventoryItem
	restoreInstall *indexTransactionInventoryItem
	discard        *indexTransactionInventoryItem
	proof          *indexTransactionInventoryItem
	targetInfo     os.FileInfo
	targetData     []byte
	targetMode     fs.FileMode
	targetAbsent   bool
}

type indexBatchTerminalCleanupBoundary uint8

const (
	indexBatchTerminalCleanupNonProof indexBatchTerminalCleanupBoundary = iota
	indexBatchTerminalCleanupManifest
	indexBatchTerminalCleanupProof
)

type indexBatchTerminalCleanupStep struct {
	boundary indexBatchTerminalCleanupBoundary
	item     *indexTransactionInventoryItem
}

type indexBatchRecoveryEntry struct {
	contract           indexBatchManifestEntry
	stage              *indexTransactionInventoryItem
	backup             *indexTransactionInventoryItem
	claim              *indexTransactionInventoryItem
	anchor             *indexTransactionInventoryItem
	anchorSource       *indexTransactionInventoryItem
	witness            *indexTransactionInventoryItem
	newInstall         *indexTransactionInventoryItem
	restoreInstall     *indexTransactionInventoryItem
	discard            *indexTransactionInventoryItem
	target             indexBatchTargetSnapshot
	v3Phase            indexBatchV3RecoveryPhase
	rollbackSource     *indexTransactionInventoryItem
	degradedStageErr   error
	degradedClaimErr   error
	degradedBackupErr  error
	degradedWitnessErr error
	runtimeEvidenceErr error
}

func prepareIndexBatchRecovery(
	inventory []*indexTransactionInventoryItem,
	hooks indexPublishHooks,
	sharedInventory bool,
) (*preparedIndexBatchRecovery, bool, error) {
	var manifests []*indexTransactionInventoryItem
	for _, item := range inventory {
		if item.discovery.kind == indexArtifactManifest {
			manifests = append(manifests, item)
		}
	}
	if len(manifests) == 0 {
		terminal, ok, err := prepareIndexBatchTerminalCommitCleanup(
			inventory,
			hooks,
			sharedInventory,
			nil,
		)
		if err == nil && ok && terminal != nil {
			if err = terminal.runIndexBatchTerminalTargetObservationHooks(); err != nil {
				terminal.close()
				return nil, true, err
			}
		}
		if ok || err != nil {
			return terminal, ok, err
		}
		return nil, false, nil
	}
	if len(manifests) != 1 {
		return nil, true, errors.New("index recovery contains multiple batch manifests")
	}
	manifestItem := manifests[0]
	if manifestItem.discovery.directory != "" || manifestItem.observation.reason != "" {
		return nil, true, unrecoverableIndexRecoveryError{
			destination: indexFilename,
			artifact:    manifestItem.discovery.relative,
			reason:      firstNonEmpty(manifestItem.observation.reason, "batch manifest is not at the bundle root"),
		}
	}
	var manifest indexBatchManifest
	if err := json.Unmarshal(manifestItem.observation.data, &manifest); err != nil {
		return nil, true, unrecoverableIndexRecoveryError{
			destination: indexFilename,
			artifact:    manifestItem.discovery.relative,
			reason:      "batch manifest cannot be decoded",
			err:         err,
		}
	}
	if err := validateIndexBatchManifestShape(manifest); err != nil {
		return nil, true, unrecoverableIndexRecoveryError{
			destination: indexFilename,
			artifact:    manifestItem.discovery.relative,
			reason:      err.Error(),
		}
	}
	byRelative := make(map[string]*indexTransactionInventoryItem, len(inventory))
	for _, item := range inventory {
		protocolRelative := indexBatchProtocolRelative(item)
		if _, exists := byRelative[protocolRelative]; exists {
			return nil, true, unrecoverableIndexRecoveryError{
				destination: item.destination.output.relative,
				artifact:    item.discovery.relative,
				reason:      "duplicate transaction artifact path",
			}
		}
		byRelative[protocolRelative] = item
	}
	allowed := map[string]struct{}{indexBatchProtocolRelative(manifestItem): {}}
	for _, contract := range manifest.Entries {
		for _, relative := range []string{
			contract.Stage,
			contract.Backup,
			contract.Claim,
			contract.Anchor,
			contract.Witness,
			contract.NewInstall,
			contract.RestoreInstall,
			contract.Discard,
		} {
			if relative != "" {
				allowed[relative] = struct{}{}
			}
		}
	}
	for _, item := range inventory {
		if _, ok := allowed[indexBatchProtocolRelative(item)]; !ok {
			return nil, true, unrecoverableIndexRecoveryError{
				destination: item.destination.output.relative,
				artifact:    item.discovery.relative,
				reason:      "transaction artifact is not bound by the batch manifest",
			}
		}
	}
	terminalInventory := make(
		[]*indexTransactionInventoryItem,
		0,
		len(inventory)-1,
	)
	for _, item := range inventory {
		if item != manifestItem {
			terminalInventory = append(terminalInventory, item)
		}
	}
	terminal, terminalHandled, terminalErr := prepareIndexBatchTerminalCommitCleanup(
		terminalInventory,
		hooks,
		sharedInventory,
		manifestItem,
	)
	if terminalHandled &&
		errors.Is(terminalErr, errIndexBatchManifestTerminalNonPrefix) {
		return nil, true, terminalErr
	}
	if terminalErr == nil &&
		terminalHandled &&
		indexBatchManifestTerminalCleanupOutcome(terminal) {
		if err := terminal.runIndexBatchTerminalTargetObservationHooks(); err != nil {
			terminal.close()
			return nil, true, err
		}
		return terminal, true, nil
	}
	prepared := &preparedIndexBatchRecovery{
		inventory:       inventory,
		manifest:        manifest,
		manifestItem:    manifestItem,
		removed:         make(map[string]struct{}),
		hooks:           hooks,
		sharedInventory: sharedInventory,
	}
	succeeded := false
	defer func() {
		if !succeeded {
			prepared.close()
		}
	}()
	for _, contract := range manifest.Entries {
		entry := &indexBatchRecoveryEntry{contract: contract}
		entry.stage = byRelative[contract.Stage]
		if contract.Backup != "" {
			entry.backup = byRelative[contract.Backup]
		}
		if contract.Claim != "" {
			entry.claim = byRelative[contract.Claim]
		}
		if contract.Anchor != "" {
			entry.anchor = byRelative[contract.Anchor]
		}
		if contract.Witness != "" {
			entry.witness = byRelative[contract.Witness]
		}
		entry.newInstall = byRelative[contract.NewInstall]
		entry.restoreInstall = byRelative[contract.RestoreInstall]
		entry.discard = byRelative[contract.Discard]
		item := entry.stage
		if item == nil {
			item = entry.backup
		}
		if item == nil {
			var err error
			item, err = prepared.openTargetItem(contract)
			if err != nil {
				prepared.close()
				return nil, true, err
			}
		}
		snapshot, err := observeIndexBatchTarget(
			item,
			contract,
			entry.stage,
			entry.backup,
			entry.claim,
			entry.anchor,
			entry.witness,
		)
		if err != nil {
			return nil, true, err
		}
		entry.target = snapshot
		if contract.OldPresent && entry.anchor == nil {
			entry.rollbackSource, err = prepared.selectRecoveryOriginalSource(entry)
			if err != nil {
				return nil, true, err
			}
		}
		entry.v3Phase, err = prepared.classifyIndexBatchV3Entry(entry)
		if err != nil {
			return nil, true, err
		}
		if hooks.afterBatchTargetObservation != nil {
			if err := hooks.afterBatchTargetObservation(
				contract.Path,
				item.destination.parent,
				item.destination.leaf,
			); err != nil {
				return nil, true, err
			}
			if err := prepared.captureStableRecoveryEvidenceDrift(entry); err != nil {
				return nil, true, err
			}
			if _, err := prepared.selectRecoveryOriginalSource(entry); err != nil {
				return nil, true, err
			}
		}
		prepared.targets = append(prepared.targets, snapshot)
		prepared.entries = append(prepared.entries, entry)
	}
	if err := prepared.validateIndexBatchV3PhaseMixture(); err != nil {
		return nil, true, err
	}
	if err := prepared.verifyCapturedTargetSnapshots(); err != nil {
		return nil, true, err
	}
	if hooks.beforeRecoveryClaim != nil {
		for _, entry := range prepared.entries {
			if entry.backup == nil {
				continue
			}
			if err := hooks.beforeRecoveryClaim(
				entry.contract.Path,
				entry.backup.destination.parent,
				entry.backup.discovery.name,
			); err != nil {
				return nil, true, err
			}
		}
		if err := prepared.revalidate(); err != nil {
			return nil, true, err
		}
	}
	root := prepared.rootEntry()
	if root == nil || root.stage == nil {
		return nil, true, errors.New("index batch root stage is missing while its manifest is present")
	}
	prepared.committed = root.target.relation == indexBatchTargetNew &&
		root.target.info != nil &&
		root.stage.observation.info != nil &&
		os.SameFile(root.target.info, root.stage.observation.info)
	if prepared.committed {
		if err := prepared.verifyIndexBatchV3CommitProof(); err != nil {
			return nil, true, err
		}
	} else if err := prepared.validatePrecommitState(); err != nil {
		return nil, true, err
	}
	if err := prepared.revalidate(); err != nil {
		return nil, true, err
	}
	succeeded = true
	return prepared, true, nil
}

func (prepared *preparedIndexBatchRecovery) validateIndexBatchV3PhaseMixture() error {
	root := prepared.rootEntry()
	if root == nil {
		return errors.New("index batch v3 has no root entry")
	}
	if root.v3Phase == indexBatchV3PhasePublished {
		for _, entry := range prepared.entries {
			if entry.v3Phase != indexBatchV3PhasePublished {
				return unrecoverableIndexRecoveryError{
					destination: entry.contract.Path,
					artifact:    entry.contract.Stage,
					reason:      "published root has a non-published v3 batch peer",
				}
			}
		}
		return nil
	}

	const (
		indexBatchV3MixturePublished = iota
		indexBatchV3MixtureActive
		indexBatchV3MixtureRestored
		indexBatchV3MixtureTail
	)
	segment := indexBatchV3MixturePublished
	activeCount := 0
	for _, entry := range prepared.entries {
		phase := entry.v3Phase
		switch phase {
		case indexBatchV3PhasePublished:
			if segment != indexBatchV3MixturePublished {
				return invalidIndexBatchV3Mixture(entry)
			}
		case indexBatchV3PhasePreNewVacated,
			indexBatchV3PhasePreNewRollbackAnchored,
			indexBatchV3PhasePreNewRollbackReady,
			indexBatchV3PhaseRollbackAnchored,
			indexBatchV3PhaseRollbackReady,
			indexBatchV3PhasePreRestore,
			indexBatchV3PhaseBlockedAnchored:
			if segment != indexBatchV3MixturePublished || activeCount != 0 {
				return invalidIndexBatchV3Mixture(entry)
			}
			activeCount++
			segment = indexBatchV3MixtureActive
		case indexBatchV3PhasePreNewRestored,
			indexBatchV3PhaseRestored,
			indexBatchV3PhaseCreateRollbackVacated:
			if segment == indexBatchV3MixtureTail {
				return invalidIndexBatchV3Mixture(entry)
			}
			segment = indexBatchV3MixtureRestored
		case indexBatchV3PhasePreNew:
			segment = indexBatchV3MixtureTail
		default:
			return invalidIndexBatchV3Mixture(entry)
		}
	}
	return nil
}

func invalidIndexBatchV3Mixture(entry *indexBatchRecoveryEntry) error {
	return unrecoverableIndexRecoveryError{
		destination: entry.contract.Path,
		artifact:    entry.contract.Stage,
		reason:      "v3 batch entry phases do not form a legal publication or rollback prefix",
	}
}

func (prepared *preparedIndexBatchRecovery) verifyCapturedTargetSnapshots() error {
	for _, entry := range prepared.entries {
		current, err := observeIndexBatchTarget(
			entry.target.item,
			entry.contract,
			entry.stage,
			entry.backup,
			entry.claim,
			entry.anchor,
			entry.witness,
		)
		if err != nil {
			return err
		}
		current = normalizeIndexBatchTargetSnapshot(entry, current)
		if !sameIndexBatchTargetSnapshot(current, entry.target) {
			return unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    entry.contract.Stage,
				reason:      "batch target changed after its recovery observation",
			}
		}
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) openTargetItem(
	entry indexBatchManifestEntry,
) (*indexTransactionInventoryItem, error) {
	if prepared.manifestItem == nil ||
		prepared.manifestItem.destination == nil ||
		prepared.manifestItem.destination.root == nil {
		return nil, errors.New("index batch root is unavailable while pinning a target")
	}
	directory := indexBatchArtifactDirectory(entry.Path)
	parent, err := openDirectory(prepared.manifestItem.destination.root, directory)
	if err != nil {
		return nil, unrecoverableIndexRecoveryError{
			destination: entry.Path,
			artifact:    entry.Stage,
			reason:      "batch target parent cannot be pinned",
			err:         err,
		}
	}
	parentInfo, err := parent.Lstat(".")
	if err != nil || !parentInfo.IsDir() {
		_ = parent.Close()
		return nil, unrecoverableIndexRecoveryError{
			destination: entry.Path,
			artifact:    entry.Stage,
			reason:      "batch target parent changed while being pinned",
			err:         err,
		}
	}
	prepared.extraParents = append(prepared.extraParents, parent)
	return &indexTransactionInventoryItem{
		discovery: indexTransactionDiscovery{
			directory: directory,
			relative:  entry.Stage,
		},
		destination: &indexDestination{
			output:     indexOutput{relative: entry.Path},
			root:       prepared.manifestItem.destination.root,
			parent:     parent,
			directory:  directory,
			parentInfo: parentInfo,
			leaf:       indexFilename,
		},
	}, nil
}

func (prepared *preparedIndexBatchRecovery) close() {
	if prepared == nil {
		return
	}
	for _, parent := range prepared.extraParents {
		_ = parent.Close()
	}
	prepared.extraParents = nil
}

func prepareIndexBatchTerminalCommitCleanup(
	inventory []*indexTransactionInventoryItem,
	hooks indexPublishHooks,
	sharedInventory bool,
	manifestBoundary *indexTransactionInventoryItem,
) (*preparedIndexBatchRecovery, bool, error) {
	rootMarker := false
	for _, item := range inventory {
		if item != nil &&
			item.destination != nil &&
			item.destination.output.relative == indexFilename &&
			item.discovery.directory == "" {
			switch item.discovery.kind {
			case indexArtifactStage, indexArtifactAnchor, indexArtifactWitness:
				rootMarker = true
			}
		}
	}
	if !rootMarker {
		return nil, false, nil
	}
	groupsByDestination := make(map[string]*indexBatchTerminalCommitGroup)
	destinations := make([]string, 0)
	for _, item := range inventory {
		if item == nil ||
			item.destination == nil ||
			item.observation.reason != "" ||
			item.observation.info == nil {
			return nil, true, unrecoverableIndexRecoveryError{
				destination: indexFilename,
				reason:      "no-manifest v3 cleanup contains an inexact artifact",
			}
		}
		destination := item.destination.output.relative
		group := groupsByDestination[destination]
		if group == nil {
			group = &indexBatchTerminalCommitGroup{destination: destination}
			groupsByDestination[destination] = group
			destinations = append(destinations, destination)
		}
		assign := func(slot **indexTransactionInventoryItem) error {
			if *slot != nil {
				return unrecoverableIndexRecoveryError{
					destination: destination,
					artifact:    item.discovery.relative,
					reason:      "no-manifest v3 cleanup contains duplicate artifact roles",
				}
			}
			*slot = item
			return nil
		}
		var err error
		switch item.discovery.kind {
		case indexArtifactStage:
			err = assign(&group.stage)
		case indexArtifactBackup:
			err = assign(&group.backup)
		case indexArtifactRestore:
			err = assign(&group.claim)
		case indexArtifactAnchor:
			err = assign(&group.anchor)
		case indexArtifactWitness:
			err = assign(&group.witness)
		case indexArtifactNewInstall:
			err = assign(&group.newInstall)
		case indexArtifactRestoreInstall:
			err = assign(&group.restoreInstall)
		case indexArtifactDiscard:
			err = assign(&group.discard)
		default:
			return nil, true, unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    item.discovery.relative,
				reason:      "no-manifest commit cleanup contains a non-terminal v3 role",
			}
		}
		if err != nil {
			return nil, true, err
		}
	}
	sort.Strings(destinations)
	groups := make([]*indexBatchTerminalCommitGroup, 0, len(destinations))
	same := func(left, right *indexTransactionInventoryItem) bool {
		return left != nil &&
			right != nil &&
			left.observation.info != nil &&
			right.observation.info != nil &&
			os.SameFile(left.observation.info, right.observation.info)
	}
	for _, destination := range destinations {
		group := groupsByDestination[destination]
		if group.claim != nil &&
			(group.witness == nil || !same(group.claim, group.witness)) {
			return nil, true, unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    group.claim.discovery.relative,
				reason:      "no-manifest commit claim does not alias retained witness",
			}
		}
		if group.newInstall != nil &&
			(group.stage == nil || !same(group.newInstall, group.stage)) {
			var prefixErr error
			if manifestBoundary != nil {
				prefixErr = errIndexBatchManifestTerminalNonPrefix
			}
			return nil, true, unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    group.newInstall.discovery.relative,
				reason:      "no-manifest N token does not alias S",
				err:         prefixErr,
			}
		}
		if group.discard != nil &&
			(group.stage == nil || !same(group.discard, group.stage)) {
			var prefixErr error
			if manifestBoundary != nil {
				prefixErr = errIndexBatchManifestTerminalNonPrefix
			}
			return nil, true, unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    group.discard.discovery.relative,
				reason:      "no-manifest D token does not alias S",
				err:         prefixErr,
			}
		}
		if group.restoreInstall != nil &&
			(group.anchor == nil || !same(group.restoreInstall, group.anchor)) {
			return nil, true, unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    group.restoreInstall.discovery.relative,
				reason:      "no-manifest R token does not alias A",
			}
		}
		if group.newInstall != nil && group.discard != nil {
			return nil, true, unrecoverableIndexRecoveryError{
				destination: destination,
				reason:      "no-manifest terminal group contains both N and D",
			}
		}
		if group.restoreInstall != nil {
			return nil, true, unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    group.restoreInstall.discovery.relative,
				reason:      "no-manifest terminal group contains impossible live R",
			}
		}
		if same(group.stage, group.backup) ||
			same(group.stage, group.claim) ||
			same(group.stage, group.anchor) ||
			same(group.stage, group.witness) ||
			same(group.backup, group.claim) ||
			same(group.backup, group.anchor) ||
			same(group.backup, group.witness) ||
			same(group.anchor, group.claim) ||
			same(group.anchor, group.witness) {
			return nil, true, unrecoverableIndexRecoveryError{
				destination: destination,
				reason:      "no-manifest terminal evidence aliases forbidden roles",
			}
		}
		targetItem := group.stage
		if targetItem == nil {
			targetItem = group.anchor
		}
		if targetItem == nil {
			targetItem = group.witness
		}
		if targetItem == nil {
			return nil, true, unrecoverableIndexRecoveryError{
				destination: destination,
				reason:      "no-manifest terminal group has no target proof",
			}
		}
		targetInfo, targetData, targetMode, targetPresent, err :=
			observeIndexBatchTerminalTarget(targetItem)
		if err != nil {
			return nil, true, err
		}
		group.targetInfo = targetInfo
		group.targetData = bytes.Clone(targetData)
		group.targetMode = targetMode
		matches := func(item *indexTransactionInventoryItem) bool {
			return item != nil &&
				item.observation.info != nil &&
				targetInfo != nil &&
				os.SameFile(item.observation.info, targetInfo) &&
				bytes.Equal(item.observation.data, targetData) &&
				indexPreservedMode(item.observation.info.Mode()) == targetMode
		}
		switch {
		case targetPresent && matches(group.stage):
			if group.anchor != nil ||
				group.newInstall != nil ||
				group.restoreInstall != nil ||
				group.discard != nil {
				return nil, true, unrecoverableIndexRecoveryError{
					destination: destination,
					reason:      "no-manifest commit group contains rollback tokens",
				}
			}
			group.proof = group.stage
		case targetPresent && matches(group.anchor):
			provenance := 0
			if group.newInstall != nil {
				provenance++
			}
			if group.discard != nil {
				provenance++
			}
			advancedCleanup := provenance == 0 &&
				group.backup == nil &&
				group.claim == nil &&
				group.witness == nil
			if group.anchor == nil ||
				provenance > 1 ||
				provenance == 0 && !advancedCleanup {
				return nil, true, unrecoverableIndexRecoveryError{
					destination: destination,
					reason:      "no-manifest restored group has ambiguous token state",
				}
			}
			group.proof = group.anchor
		case targetPresent && matches(group.witness):
			if group.anchor != nil ||
				group.restoreInstall != nil ||
				group.discard != nil ||
				group.claim != nil ||
				group.newInstall == nil && group.backup != nil {
				return nil, true, unrecoverableIndexRecoveryError{
					destination: destination,
					reason:      "no-manifest untouched rollback group is ambiguous",
				}
			}
			group.proof = group.witness
		case !targetPresent:
			if group.stage == nil ||
				group.anchor != nil ||
				group.backup != nil ||
				group.claim != nil ||
				group.witness != nil ||
				group.restoreInstall != nil {
				return nil, true, unrecoverableIndexRecoveryError{
					destination: destination,
					reason:      "no-manifest absent target has old or missing proof",
				}
			}
			group.proof = group.stage
			group.targetAbsent = true
		default:
			return nil, true, unrecoverableIndexRecoveryError{
				destination: destination,
				reason:      "no-manifest terminal target matches no retained proof",
			}
		}
		groups = append(groups, group)
	}
	if manifestBoundary != nil {
		if err := validateIndexBatchManifestTerminalCleanupPrefix(
			manifestBoundary,
			groups,
			inventory,
		); err != nil {
			return nil, true, err
		}
	}
	validationInventory := append(
		[]*indexTransactionInventoryItem(nil),
		inventory...,
	)
	if manifestBoundary != nil {
		validationInventory = append(validationInventory, manifestBoundary)
	}
	prepared := &preparedIndexBatchRecovery{
		inventory:        validationInventory,
		removed:          make(map[string]struct{}),
		hooks:            hooks,
		sharedInventory:  sharedInventory,
		terminalCommit:   groups,
		manifestBoundary: manifestBoundary,
	}
	if err := prepared.verifyIndexBatchTerminalCommitScope(); err != nil {
		return nil, true, err
	}
	return prepared, true, nil
}

func validateIndexBatchManifestTerminalCleanupPrefix(
	manifestItem *indexTransactionInventoryItem,
	groups []*indexBatchTerminalCommitGroup,
	inventory []*indexTransactionInventoryItem,
) error {
	if manifestItem == nil ||
		manifestItem.observation.info == nil ||
		manifestItem.observation.reason != "" {
		return unrecoverableIndexRecoveryError{
			destination: indexFilename,
			reason:      "terminal cleanup has no exact manifest boundary",
		}
	}
	var manifest indexBatchManifest
	if err := json.Unmarshal(manifestItem.observation.data, &manifest); err != nil {
		return unrecoverableIndexRecoveryError{
			destination: indexFilename,
			artifact:    manifestItem.discovery.relative,
			reason:      "terminal cleanup manifest cannot be decoded",
			err:         err,
		}
	}
	if err := validateIndexBatchManifestShape(manifest); err != nil {
		return unrecoverableIndexRecoveryError{
			destination: indexFilename,
			artifact:    manifestItem.discovery.relative,
			reason:      "terminal cleanup manifest is invalid",
			err:         err,
		}
	}
	contracts := make(map[string]indexBatchManifestEntry, len(manifest.Entries))
	for _, contract := range manifest.Entries {
		contracts[cleanIndexDestinationPath(contract.Path)] = contract
	}
	present := make(map[string]bool, len(inventory))
	for _, item := range inventory {
		if item != nil {
			present[indexBatchProtocolRelative(item)] = true
		}
	}
	type expectedGroup struct {
		group    *indexBatchTerminalCommitGroup
		contract indexBatchManifestEntry
		claim    bool
		backup   bool
		witness  bool
		newToken bool
		discard  bool
		stage    bool
	}
	expectedGroups := make([]expectedGroup, 0, len(groups))
	for _, group := range groups {
		if group == nil || group.proof == nil {
			return unrecoverableIndexRecoveryError{
				destination: indexFilename,
				reason:      "terminal cleanup lost an exact proof role",
			}
		}
		contract, ok := contracts[cleanIndexDestinationPath(group.destination)]
		if !ok {
			return unrecoverableIndexRecoveryError{
				destination: group.destination,
				reason:      "terminal cleanup destination is not bound by the manifest",
			}
		}
		proofKind := group.proof.discovery.kind
		expected := expectedGroup{
			group:    group,
			contract: contract,
			backup:   contract.OldPresent,
			witness:  contract.OldPresent && proofKind != indexArtifactWitness,
			stage:    proofKind != indexArtifactStage,
		}
		switch proofKind {
		case indexArtifactStage:
			if group.targetAbsent {
				expected.newToken = group.newInstall != nil || group.discard == nil
				expected.discard = !expected.newToken
			} else {
				expected.claim = contract.OldPresent
			}
		case indexArtifactAnchor:
			if !contract.OldPresent {
				return unrecoverableIndexRecoveryError{
					destination: group.destination,
					artifact:    group.proof.discovery.relative,
					reason:      "create-only terminal cleanup cannot use an anchor proof",
				}
			}
			expected.claim = true
			expected.newToken = group.newInstall != nil
			expected.discard = !expected.newToken
		case indexArtifactWitness:
			if !contract.OldPresent {
				return unrecoverableIndexRecoveryError{
					destination: group.destination,
					artifact:    group.proof.discovery.relative,
					reason:      "create-only terminal cleanup cannot use a witness proof",
				}
			}
			expected.newToken = true
		default:
			return unrecoverableIndexRecoveryError{
				destination: group.destination,
				artifact:    group.proof.discovery.relative,
				reason:      "terminal cleanup uses an unsupported proof role",
			}
		}
		expectedGroups = append(expectedGroups, expected)
	}
	var sequence []string
	appendExpected := func(enabled bool, relative string) {
		if enabled {
			sequence = append(sequence, relative)
		}
	}
	for _, expected := range expectedGroups {
		appendExpected(expected.claim, expected.contract.Claim)
	}
	for _, expected := range expectedGroups {
		appendExpected(expected.backup, expected.contract.Backup)
	}
	for _, expected := range expectedGroups {
		appendExpected(expected.witness, expected.contract.Witness)
	}
	for _, expected := range expectedGroups {
		appendExpected(expected.newToken, expected.contract.NewInstall)
	}
	for _, expected := range expectedGroups {
		appendExpected(expected.discard, expected.contract.Discard)
	}
	for _, expected := range expectedGroups {
		appendExpected(expected.stage, expected.contract.Stage)
	}
	presentSuffix := false
	for _, relative := range sequence {
		if relative == "" {
			return unrecoverableIndexRecoveryError{
				destination: indexFilename,
				reason:      "terminal cleanup manifest omits an expected non-proof role",
			}
		}
		if present[relative] {
			presentSuffix = true
			continue
		}
		if presentSuffix {
			return unrecoverableIndexRecoveryError{
				destination: indexFilename,
				artifact:    relative,
				reason:      "manifest-bound terminal cleanup contains a non-prefix evidence hole",
				err:         errIndexBatchManifestTerminalNonPrefix,
			}
		}
	}
	for _, expected := range expectedGroups {
		proof := expected.group.proof
		if proof == nil || !present[indexBatchProtocolRelative(proof)] {
			return unrecoverableIndexRecoveryError{
				destination: expected.group.destination,
				reason:      "manifest-bound terminal cleanup is missing an exact proof role",
			}
		}
	}
	return nil
}

func indexBatchManifestTerminalCleanupOutcome(
	prepared *preparedIndexBatchRecovery,
) bool {
	if prepared == nil || len(prepared.terminalCommit) == 0 {
		return false
	}
	allCommitted := true
	allRolledBack := true
	for _, group := range prepared.terminalCommit {
		if group == nil || group.proof == nil {
			return false
		}
		published := !group.targetAbsent &&
			group.proof.discovery.kind == indexArtifactStage
		allCommitted = allCommitted && published
		allRolledBack = allRolledBack && !published
	}
	return allCommitted || allRolledBack
}

func observeIndexBatchTerminalTarget(
	item *indexTransactionInventoryItem,
) (os.FileInfo, []byte, fs.FileMode, bool, error) {
	if item == nil || item.destination == nil || item.destination.parent == nil {
		return nil, nil, 0, false, errors.New("no-manifest terminal target parent is unavailable")
	}
	if err := verifyIndexDestinationParent(item.destination); err != nil {
		return nil, nil, 0, false, err
	}
	before, err := item.destination.parent.Lstat(item.destination.leaf)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, 0, false, nil
	}
	if err != nil ||
		before.Mode()&os.ModeSymlink != 0 ||
		!before.Mode().IsRegular() {
		return nil, nil, 0, false, unrecoverableIndexRecoveryError{
			destination: item.destination.output.relative,
			reason:      "no-manifest terminal target is not a regular file",
			err:         err,
		}
	}
	data, readErr := readRegularIndexDestination(
		item.destination.parent,
		item.destination.leaf,
	)
	after, statErr := item.destination.parent.Lstat(item.destination.leaf)
	if readErr != nil ||
		statErr != nil ||
		after.Mode()&os.ModeSymlink != 0 ||
		!after.Mode().IsRegular() ||
		!os.SameFile(before, after) ||
		indexPreservedMode(before.Mode()) != indexPreservedMode(after.Mode()) {
		return nil, nil, 0, false, unrecoverableIndexRecoveryError{
			destination: item.destination.output.relative,
			reason:      "no-manifest terminal target changed during observation",
			err:         errors.Join(readErr, statErr),
		}
	}
	return after, data, indexPreservedMode(after.Mode()), true, nil
}

func validatePreManifestIndexRecovery(
	inventory []*indexTransactionInventoryItem,
) error {
	anchorWitness := make(map[string]bool)
	for _, item := range inventory {
		if item.discovery.kind != indexArtifactAnchor {
			continue
		}
		state, err := observeIndexArtifactLeaf(item)
		if err != nil {
			return err
		}
		if !state.present || !state.matches || !state.sameFile {
			return unrecoverableIndexRecoveryError{
				destination: item.destination.output.relative,
				artifact:    item.discovery.relative,
				reason:      "rollback anchor has no exact all-old target without a batch manifest",
			}
		}
		anchorWitness[item.destination.output.relative] = true
	}
	for _, item := range inventory {
		state, err := observeIndexArtifactLeaf(item)
		if err != nil {
			return err
		}
		switch item.discovery.kind {
		case indexArtifactStage:
			if state.sameFile {
				return unrecoverableIndexRecoveryError{
					destination: item.destination.output.relative,
					artifact:    item.discovery.relative,
					reason:      "published stage has no durable batch manifest",
				}
			}
		case indexArtifactRestore:
			if !state.present ||
				!state.matches ||
				!state.sameFile && !anchorWitness[item.destination.output.relative] {
				return unrecoverableIndexRecoveryError{
					destination: item.destination.output.relative,
					artifact:    item.discovery.relative,
					reason:      "ownership claim has no exact all-old target without a batch manifest",
				}
			}
		case indexArtifactAnchor:
			// Verified above before other evidence is interpreted.
		case indexArtifactWitness:
			if !state.present ||
				!state.matches ||
				!state.sameFile && !anchorWitness[item.destination.output.relative] {
				return unrecoverableIndexRecoveryError{
					destination: item.destination.output.relative,
					artifact:    item.discovery.relative,
					reason:      "identity witness has no exact all-old target without a batch manifest",
				}
			}
		case indexArtifactBackup:
			if !state.present || !state.matches || state.sameFile {
				return unrecoverableIndexRecoveryError{
					destination: item.destination.output.relative,
					artifact:    item.discovery.relative,
					reason:      "independent backup has no exact all-old target without a batch manifest",
				}
			}
		case indexArtifactNewInstall, indexArtifactRestoreInstall, indexArtifactDiscard:
			return unrecoverableIndexRecoveryError{
				destination: item.destination.output.relative,
				artifact:    item.discovery.relative,
				reason:      "v3 install token has no durable batch manifest",
			}
		}
	}
	return nil
}

func observeIndexBatchTarget(
	item *indexTransactionInventoryItem,
	entry indexBatchManifestEntry,
	stage, backup, claim, anchor, witness *indexTransactionInventoryItem,
) (indexBatchTargetSnapshot, error) {
	snapshot := indexBatchTargetSnapshot{entry: entry, item: item}
	if item == nil || item.destination == nil || item.destination.parent == nil {
		return snapshot, errors.New("index batch target parent is unavailable")
	}
	if err := verifyIndexDestinationParent(item.destination); err != nil {
		return snapshot, unrecoverableIndexRecoveryError{
			destination: entry.Path,
			artifact:    item.discovery.relative,
			reason:      "batch target parent detached from the bundle root",
			err:         err,
		}
	}
	before, err := item.destination.parent.Lstat(item.destination.leaf)
	if errors.Is(err, fs.ErrNotExist) {
		if entry.OldPresent {
			snapshot.relation = indexBatchTargetForeign
		} else {
			snapshot.relation = indexBatchTargetAbsent
		}
		return snapshot, nil
	}
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return snapshot, unrecoverableIndexRecoveryError{
			destination: entry.Path,
			artifact:    item.discovery.relative,
			reason:      "batch target is not an inspectable regular file",
			err:         err,
		}
	}
	data, err := readRegularIndexDestination(item.destination.parent, item.destination.leaf)
	if err != nil {
		return snapshot, err
	}
	after, err := item.destination.parent.Lstat(item.destination.leaf)
	if err != nil ||
		after.Mode()&os.ModeSymlink != 0 ||
		!after.Mode().IsRegular() ||
		!os.SameFile(before, after) ||
		indexPreservedMode(before.Mode()) != indexPreservedMode(after.Mode()) {
		return snapshot, unrecoverableIndexRecoveryError{
			destination: entry.Path,
			artifact:    item.discovery.relative,
			reason:      "batch target changed while taking the recovery snapshot",
			err:         err,
		}
	}
	snapshot.info = after
	snapshot.data = data
	snapshot.mode = indexPreservedMode(after.Mode())
	switch {
	case entry.OldPresent &&
		anchor != nil &&
		anchor.observation.info != nil &&
		os.SameFile(after, anchor.observation.info) &&
		indexBatchTargetMatchesContract(data, snapshot.mode, entry.OldDigest, entry.OldMode):
		snapshot.relation = indexBatchTargetOld
	case entry.OldPresent &&
		witness != nil &&
		witness.observation.info != nil &&
		os.SameFile(after, witness.observation.info) &&
		indexBatchTargetMatchesContract(data, snapshot.mode, entry.OldDigest, entry.OldMode):
		snapshot.relation = indexBatchTargetOld
	case stage != nil &&
		stage.observation.info != nil &&
		os.SameFile(after, stage.observation.info) &&
		indexBatchTargetMatchesContract(data, snapshot.mode, entry.NewDigest, entry.NewMode):
		snapshot.relation = indexBatchTargetNew
	default:
		snapshot.relation = indexBatchTargetForeign
	}
	return snapshot, nil
}

func indexBatchTargetMatchesContract(data []byte, mode fs.FileMode, digest string, wantMode uint32) bool {
	return indexBatchPayloadDigest(data) == digest && uint32(mode) == wantMode
}

func (prepared *preparedIndexBatchRecovery) rootEntry() *indexBatchRecoveryEntry {
	for _, entry := range prepared.entries {
		if entry.contract.Path == indexFilename {
			return entry
		}
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) validateArtifact(
	item *indexTransactionInventoryItem,
	kind indexArtifactKind,
	digest string,
	mode uint32,
) error {
	if item == nil {
		return errors.New("batch manifest artifact is missing")
	}
	if item.discovery.kind != kind || item.observation.reason != "" || item.observation.info == nil {
		return unrecoverableIndexRecoveryError{
			destination: item.destination.output.relative,
			artifact:    item.discovery.relative,
			reason:      firstNonEmpty(item.observation.reason, "batch manifest artifact kind or identity does not match"),
		}
	}
	if !indexBatchTargetMatchesContract(
		item.observation.data,
		indexPreservedMode(item.observation.info.Mode()),
		digest,
		mode,
	) {
		return unrecoverableIndexRecoveryError{
			destination: item.destination.output.relative,
			artifact:    item.discovery.relative,
			reason:      "batch manifest artifact bytes or mode do not match the bound contract",
		}
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) classifyIndexBatchV3Entry(
	entry *indexBatchRecoveryEntry,
) (indexBatchV3RecoveryPhase, error) {
	fail := func(reason string) (indexBatchV3RecoveryPhase, error) {
		return indexBatchV3PhaseInvalid, unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.Stage,
			reason:      reason,
		}
	}
	if phase, blocked, err := prepared.classifyIndexBatchV3BlockedEntry(entry); blocked {
		return phase, err
	}
	if entry.stage == nil {
		return fail("v3 recovery lacks the manifest-bound stage proof")
	}
	stageErr := prepared.validateArtifact(
		entry.stage,
		indexArtifactStage,
		entry.contract.NewDigest,
		entry.contract.NewMode,
	)
	validateOptional := func(
		item *indexTransactionInventoryItem,
		kind indexArtifactKind,
		digest string,
		mode uint32,
	) error {
		if item == nil {
			return nil
		}
		return prepared.validateArtifact(item, kind, digest, mode)
	}
	if err := validateOptional(
		entry.newInstall,
		indexArtifactNewInstall,
		entry.contract.NewDigest,
		entry.contract.NewMode,
	); err != nil {
		return indexBatchV3PhaseInvalid, err
	}
	if err := validateOptional(
		entry.discard,
		indexArtifactDiscard,
		entry.contract.NewDigest,
		entry.contract.NewMode,
	); err != nil {
		return indexBatchV3PhaseInvalid, err
	}
	if entry.contract.OldPresent {
		for _, artifact := range []struct {
			item *indexTransactionInventoryItem
			kind indexArtifactKind
		}{
			{entry.backup, indexArtifactBackup},
			{entry.claim, indexArtifactRestore},
			{entry.witness, indexArtifactWitness},
			{entry.anchor, indexArtifactAnchor},
			{entry.restoreInstall, indexArtifactRestoreInstall},
		} {
			sourceBypassesOriginalRole := entry.anchor == nil &&
				entry.rollbackSource == entry.backup &&
				(artifact.kind == indexArtifactRestore ||
					artifact.kind == indexArtifactWitness)
			postAnchorSoftOriginalRole := entry.anchor != nil &&
				(artifact.kind == indexArtifactBackup ||
					artifact.kind == indexArtifactRestore ||
					artifact.kind == indexArtifactWitness)
			if sourceBypassesOriginalRole || postAnchorSoftOriginalRole {
				if artifact.kind == indexArtifactRestore &&
					artifact.item == nil &&
					entry.anchor == nil {
					continue
				}
				if !indexBatchV3StableRegularRole(artifact.item, artifact.kind) {
					return fail("v3 recovery lacks a regular manifest-bound original evidence role")
				}
				continue
			}
			if err := validateOptional(
				artifact.item,
				artifact.kind,
				entry.contract.OldDigest,
				entry.contract.OldMode,
			); err != nil {
				return indexBatchV3PhaseInvalid, err
			}
		}
	} else if entry.anchor != nil || entry.restoreInstall != nil {
		return fail("create-only batch entry contains old restore evidence")
	}

	same := func(left, right *indexTransactionInventoryItem) bool {
		return left != nil &&
			right != nil &&
			left.observation.info != nil &&
			right.observation.info != nil &&
			os.SameFile(left.observation.info, right.observation.info)
	}
	n := entry.newInstall != nil
	a := entry.anchor != nil
	r := entry.restoreInstall != nil
	d := entry.discard != nil
	missing := entry.target.info == nil
	targetNew := entry.target.relation == indexBatchTargetNew
	targetOld := entry.target.relation == indexBatchTargetOld
	exactAnchorTarget := targetOld &&
		entry.target.info != nil &&
		entry.anchor != nil &&
		entry.anchor.observation.info != nil &&
		os.SameFile(entry.target.info, entry.anchor.observation.info)
	detachedStageProof := indexBatchV3StableRegularRole(
		entry.stage,
		indexArtifactStage,
	) &&
		!same(entry.stage, entry.anchor) &&
		(n && a && !r && !d && exactAnchorTarget &&
			!same(entry.newInstall, entry.stage) ||
			!n && a && !r && d && exactAnchorTarget &&
				!same(entry.discard, entry.stage))
	if detachedStageProof {
		if stageErr == nil {
			stageErr = unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    entry.contract.Stage,
				reason:      "stage path is detached from its exact independent N/D proof",
			}
		}
		if entry.degradedStageErr == nil {
			entry.degradedStageErr = stageErr
		}
		stageErr = nil
	}
	if stageErr != nil {
		return indexBatchV3PhaseInvalid, stageErr
	}
	if entry.newInstall != nil &&
		!same(entry.newInstall, entry.stage) &&
		!detachedStageProof {
		return fail("new install token does not alias retained stage")
	}
	if entry.discard != nil &&
		!same(entry.discard, entry.stage) &&
		!detachedStageProof {
		return fail("discard token does not alias retained stage")
	}
	if entry.restoreInstall != nil &&
		(entry.anchor == nil || !same(entry.restoreInstall, entry.anchor)) {
		return fail("restore install token does not alias rollback anchor")
	}
	if entry.anchor != nil {
		for _, forbidden := range []*indexTransactionInventoryItem{
			entry.stage,
			entry.newInstall,
			entry.discard,
			entry.backup,
			entry.claim,
			entry.witness,
		} {
			if same(entry.anchor, forbidden) {
				return fail("rollback anchor aliases forbidden evidence")
			}
		}
	}
	if entry.backup != nil {
		for _, forbidden := range []*indexTransactionInventoryItem{
			entry.stage,
			entry.newInstall,
			entry.discard,
			entry.anchor,
			entry.restoreInstall,
			entry.witness,
		} {
			if same(entry.backup, forbidden) {
				return fail("independent backup aliases another evidence role")
			}
		}
	}
	if entry.newInstall != nil && entry.discard != nil {
		return fail("new install and discard tokens coexist")
	}
	allowDetachedClaimWitness := entry.rollbackSource == entry.backup ||
		entry.anchor != nil
	if entry.claim != nil &&
		(entry.witness == nil || !same(entry.claim, entry.witness)) &&
		!allowDetachedClaimWitness {
		return fail("original ownership claim does not alias identity witness")
	}

	if entry.contract.OldPresent && a && missing {
		preNewConsumer := n && !d && (!r || same(entry.restoreInstall, entry.anchor))
		publishedConsumer := !n && r && d &&
			same(entry.restoreInstall, entry.anchor) &&
			same(entry.discard, entry.stage)
		if !preNewConsumer && !publishedConsumer {
			return fail(
				"anchored missing target has neither exact N pre-new nor exact R+D published rollback tokens",
			)
		}
	}
	if entry.contract.OldPresent {
		switch {
		case n && !a && !r && !d && targetOld:
			return indexBatchV3PhasePreNew, nil
		case n && !a && !r && !d && missing &&
			entry.claim != nil &&
			entry.witness != nil &&
			same(entry.claim, entry.witness):
			return indexBatchV3PhasePreNewVacated, nil
		case n && a && !r && !d && missing:
			return indexBatchV3PhasePreNewRollbackAnchored, nil
		case n && a && r && !d && missing:
			return indexBatchV3PhasePreNewRollbackReady, nil
		case n && a && !r && !d && targetOld &&
			entry.target.info != nil &&
			entry.anchor.observation.info != nil &&
			os.SameFile(entry.target.info, entry.anchor.observation.info):
			return indexBatchV3PhasePreNewRestored, nil
		case !n && !a && !r && !d && targetNew:
			return indexBatchV3PhasePublished, nil
		case !n && a && !r && !d && targetNew:
			return indexBatchV3PhaseRollbackAnchored, nil
		case !n && a && r && !d && targetNew:
			return indexBatchV3PhaseRollbackReady, nil
		case !n && a && r && d && missing:
			return indexBatchV3PhasePreRestore, nil
		case !n && a && !r && d && targetOld &&
			entry.target.info != nil &&
			entry.anchor.observation.info != nil &&
			os.SameFile(entry.target.info, entry.anchor.observation.info):
			return indexBatchV3PhaseRestored, nil
		default:
			return fail("existing batch entry is not in a legal v3 recovery state")
		}
	}
	switch {
	case n && !a && !r && !d && missing:
		return indexBatchV3PhasePreNew, nil
	case !n && !a && !r && !d && targetNew:
		return indexBatchV3PhasePublished, nil
	case !n && !a && !r && d && missing:
		return indexBatchV3PhaseCreateRollbackVacated, nil
	default:
		return fail("create-only batch entry is not in a legal v3 recovery state")
	}
}

func (prepared *preparedIndexBatchRecovery) classifyIndexBatchV3BlockedEntry(
	entry *indexBatchRecoveryEntry,
) (indexBatchV3RecoveryPhase, bool, error) {
	if !entry.contract.OldPresent ||
		entry.anchor == nil ||
		entry.target.relation != indexBatchTargetForeign ||
		entry.target.info == nil {
		return indexBatchV3PhaseInvalid, false, nil
	}
	fail := func(reason string, err error) (indexBatchV3RecoveryPhase, bool, error) {
		return indexBatchV3PhaseInvalid, true, unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.Anchor,
			reason:      reason,
			err:         err,
		}
	}
	if err := prepared.validateArtifact(
		entry.anchor,
		indexArtifactAnchor,
		entry.contract.OldDigest,
		entry.contract.OldMode,
	); err != nil {
		return fail("foreign-blocked v3 entry lacks an exact manifest-bound rollback anchor", err)
	}
	for _, role := range []struct {
		item *indexTransactionInventoryItem
		kind indexArtifactKind
	}{
		{entry.stage, indexArtifactStage},
		{entry.backup, indexArtifactBackup},
		{entry.claim, indexArtifactRestore},
		{entry.witness, indexArtifactWitness},
	} {
		if !indexBatchV3StableRegularRole(role.item, role.kind) {
			return fail(
				"foreign-blocked v3 entry lacks a regular manifest-bound evidence role",
				nil,
			)
		}
	}
	if entry.restoreInstall != nil {
		if err := prepared.validateArtifact(
			entry.restoreInstall,
			indexArtifactRestoreInstall,
			entry.contract.OldDigest,
			entry.contract.OldMode,
		); err != nil {
			return fail("foreign-blocked v3 entry has an inexact restore-install token", err)
		}
	}
	same := func(left, right *indexTransactionInventoryItem) bool {
		return left != nil &&
			right != nil &&
			left.observation.info != nil &&
			right.observation.info != nil &&
			os.SameFile(left.observation.info, right.observation.info)
	}
	for _, token := range []struct {
		item  *indexTransactionInventoryItem
		kind  indexArtifactKind
		peer  *indexTransactionInventoryItem
		label string
	}{
		{entry.newInstall, indexArtifactNewInstall, entry.stage, "new-install"},
		{entry.discard, indexArtifactDiscard, entry.stage, "discard"},
	} {
		if token.item == nil {
			continue
		}
		if !indexBatchV3StableRegularRole(token.item, token.kind) {
			return fail(
				"foreign-blocked v3 entry has a non-regular "+token.label+" token",
				nil,
			)
		}
		var payloadErr error
		if token.kind == indexArtifactNewInstall {
			payloadErr = prepared.validateArtifact(
				token.item,
				token.kind,
				entry.contract.NewDigest,
				entry.contract.NewMode,
			)
		} else {
			payloadErr = prepared.validateArtifact(
				token.item,
				token.kind,
				entry.contract.NewDigest,
				entry.contract.NewMode,
			)
		}
		if !same(token.item, token.peer) {
			payloadErr = errors.Join(
				payloadErr,
				errors.New("foreign-blocked v3 "+token.label+" token detached from stage"),
			)
		}
		entry.runtimeEvidenceErr = errors.Join(entry.runtimeEvidenceErr, payloadErr)
	}
	if entry.restoreInstall != nil && !same(entry.restoreInstall, entry.anchor) {
		return fail("foreign-blocked v3 restore-install token does not alias anchor", nil)
	}
	if entry.newInstall != nil && entry.discard != nil {
		return fail("foreign-blocked v3 entry contains both new-install and discard tokens", nil)
	}
	for _, forbidden := range []*indexTransactionInventoryItem{
		entry.stage,
		entry.newInstall,
		entry.discard,
		entry.backup,
		entry.claim,
		entry.witness,
	} {
		if same(entry.anchor, forbidden) {
			return fail("foreign-blocked v3 rollback anchor aliases another evidence role", nil)
		}
	}
	return indexBatchV3PhaseBlockedAnchored, true, nil
}

func indexBatchV3StableRegularRole(
	item *indexTransactionInventoryItem,
	kind indexArtifactKind,
) bool {
	return item != nil &&
		item.discovery.kind == kind &&
		item.observation.info != nil &&
		item.observation.info.Mode().IsRegular()
}

func (prepared *preparedIndexBatchRecovery) validatePrecommitState() error {
	const (
		precommitPublished = iota
		precommitTail
	)
	phase := precommitPublished
	inFlightSeen := false
	untouchedOriginalSeen := false
	blockedDestinationSeen := false
	degradedClaims := 0
	for _, entry := range prepared.entries {
		if entry.contract.OldPresent && entry.anchor != nil {
			if err := prepared.verifyRecoveryAnchor(entry, entry.anchor); err != nil {
				return err
			}
		}
		exactAnchorBlockedTarget := entry.contract.OldPresent &&
			entry.anchor != nil &&
			entry.target.relation == indexBatchTargetForeign &&
			entry.target.info != nil
		var blockedEvidenceErr error
		var runtimeSource *indexTransactionInventoryItem
		if entry.runtimeEvidenceErr != nil &&
			entry.contract.OldPresent &&
			!exactAnchorBlockedTarget {
			var err error
			runtimeSource, err = prepared.selectRecoveryOriginalSource(entry)
			if err != nil {
				return err
			}
		}
		stageErr := prepared.validateArtifact(
			entry.stage,
			indexArtifactStage,
			entry.contract.NewDigest,
			entry.contract.NewMode,
		)
		if stageErr != nil &&
			exactAnchorBlockedTarget &&
			indexBatchV3StableRegularRole(entry.stage, indexArtifactStage) {
			blockedEvidenceErr = errors.Join(blockedEvidenceErr, stageErr)
			stageErr = nil
		}
		if stageErr != nil &&
			entry.runtimeEvidenceErr == nil &&
			entry.degradedStageErr == nil {
			return stageErr
		}
		if entry.contract.OldPresent {
			entryDegraded := false
			targetSupportsAnchorRollback := entry.anchor != nil &&
				(entry.target.relation == indexBatchTargetNew ||
					entry.target.info == nil ||
					entry.target.relation == indexBatchTargetOld &&
						entry.target.info != nil &&
						os.SameFile(entry.target.info, entry.anchor.observation.info) ||
					exactAnchorBlockedTarget)
			targetSupportsSafeRollback := entry.target.relation == indexBatchTargetNew ||
				entry.target.info == nil ||
				targetSupportsAnchorRollback
			witnessErr := prepared.validateArtifact(
				entry.witness,
				indexArtifactWitness,
				entry.contract.OldDigest,
				entry.contract.OldMode,
			)
			selectedBackupBeforeAnchor := entry.anchor == nil &&
				entry.rollbackSource == entry.backup
			if witnessErr != nil &&
				selectedBackupBeforeAnchor &&
				indexBatchV3StableRegularRole(entry.witness, indexArtifactWitness) {
				entry.runtimeEvidenceErr = errors.Join(entry.runtimeEvidenceErr, witnessErr)
				witnessErr = nil
			}
			if witnessErr != nil &&
				exactAnchorBlockedTarget &&
				indexBatchV3StableRegularRole(entry.witness, indexArtifactWitness) {
				blockedEvidenceErr = errors.Join(blockedEvidenceErr, witnessErr)
				witnessErr = nil
			}
			if witnessErr != nil {
				if entry.runtimeEvidenceErr != nil &&
					entry.witness != runtimeSource {
					witnessErr = nil
				}
			}
			if witnessErr != nil {
				rootWitnessBlocker := entry.contract.Path == indexFilename &&
					entry.claim == nil &&
					entry.anchor == nil &&
					entry.target.relation == indexBatchTargetForeign &&
					entry.target.info != nil &&
					entry.witness != nil &&
					entry.witness.observation.info != nil &&
					os.SameFile(entry.target.info, entry.witness.observation.info)
				if rootWitnessBlocker {
					prepared.blockedRootErr = errors.Join(
						publicationConflict(
							"foreign root prevented index batch commit; nested rollback completed while batch evidence remains preserved",
							nil,
						),
						witnessErr,
					)
				} else if !targetSupportsSafeRollback ||
					entry.witness == nil ||
					entry.witness.observation.info == nil {
					return witnessErr
				} else {
					entry.degradedWitnessErr = witnessErr
					entryDegraded = true
				}
			}
			backupErr := prepared.validateArtifact(
				entry.backup,
				indexArtifactBackup,
				entry.contract.OldDigest,
				entry.contract.OldMode,
			)
			if backupErr != nil &&
				exactAnchorBlockedTarget &&
				indexBatchV3StableRegularRole(entry.backup, indexArtifactBackup) {
				blockedEvidenceErr = errors.Join(blockedEvidenceErr, backupErr)
				backupErr = nil
			}
			if backupErr != nil {
				if entry.runtimeEvidenceErr != nil &&
					entry.backup != runtimeSource {
					backupErr = nil
				}
			}
			if backupErr != nil {
				backupIsStableForeign := entry.backup != nil &&
					entry.backup.observation.info != nil
				if !targetSupportsSafeRollback ||
					!backupIsStableForeign {
					return backupErr
				}
				entry.degradedBackupErr = backupErr
				entryDegraded = true
			}
			if !exactAnchorBlockedTarget &&
				entry.runtimeEvidenceErr == nil &&
				entry.backup != nil &&
				entry.witness != nil &&
				entry.backup.observation.info != nil &&
				entry.witness.observation.info != nil &&
				os.SameFile(entry.backup.observation.info, entry.witness.observation.info) {
				return errors.New("index batch backup must be independent from the original identity witness")
			}
			if entry.claim != nil || exactAnchorBlockedTarget {
				claimErr := prepared.validateArtifact(
					entry.claim,
					indexArtifactRestore,
					entry.contract.OldDigest,
					entry.contract.OldMode,
				)
				if claimErr != nil &&
					selectedBackupBeforeAnchor &&
					indexBatchV3StableRegularRole(entry.claim, indexArtifactRestore) {
					entry.runtimeEvidenceErr = errors.Join(entry.runtimeEvidenceErr, claimErr)
					claimErr = nil
				}
				if claimErr != nil &&
					exactAnchorBlockedTarget &&
					indexBatchV3StableRegularRole(entry.claim, indexArtifactRestore) {
					blockedEvidenceErr = errors.Join(blockedEvidenceErr, claimErr)
					claimErr = nil
				}
				if claimErr != nil {
					if entry.runtimeEvidenceErr != nil &&
						entry.claim != runtimeSource {
						claimErr = nil
					}
				}
				if claimErr != nil {
					targetIsMissing := entry.target.info == nil
					targetIsPriorRepair := entry.target.relation == indexBatchTargetOld &&
						entry.target.info != nil &&
						entry.anchor != nil &&
						os.SameFile(entry.target.info, entry.anchor.observation.info)
					targetIsOwnedNew := entry.target.relation == indexBatchTargetNew &&
						entry.target.info != nil
					// Inventory construction takes two exact Lstat-based
					// observations. A foreign claim may therefore be any
					// stable filesystem type, including a symlink or FIFO;
					// recovery must preserve rather than follow or read it.
					claimIsStableForeign := entry.claim.observation.info != nil
					if !claimIsStableForeign ||
						(!targetIsMissing &&
							!targetIsPriorRepair &&
							!targetIsOwnedNew) {
						return claimErr
					}
					entry.degradedClaimErr = claimErr
					entryDegraded = true
				}
				if !exactAnchorBlockedTarget &&
					entry.runtimeEvidenceErr == nil &&
					entry.backup != nil &&
					entry.backup.observation.info != nil &&
					entry.claim.observation.info != nil &&
					os.SameFile(entry.backup.observation.info, entry.claim.observation.info) {
					return errors.New("index batch backup must be independent from the original claim inode")
				}
				if !exactAnchorBlockedTarget &&
					entry.runtimeEvidenceErr == nil &&
					claimErr == nil &&
					witnessErr == nil &&
					entry.claim.observation.info != nil &&
					entry.witness != nil &&
					entry.witness.observation.info != nil &&
					!os.SameFile(entry.claim.observation.info, entry.witness.observation.info) {
					return errors.New("exact original claim must retain the manifest-bound witness inode")
				}
			}
			if !exactAnchorBlockedTarget &&
				entry.runtimeEvidenceErr == nil &&
				entry.claim == nil &&
				entry.target.info != nil &&
				entry.backup != nil &&
				entry.backup.observation.info != nil &&
				os.SameFile(entry.target.info, entry.backup.observation.info) {
				return errors.New("index batch backup must be independent from the untouched original target inode")
			}
			switch {
			case entry.target.relation == indexBatchTargetNew && entry.claim != nil:
				if phase != precommitPublished {
					return errors.New("precommit published targets are not a canonical prefix")
				}
			case entry.target.info == nil && entry.claim != nil:
				if phase != precommitPublished || inFlightSeen {
					return errors.New("precommit batch has a non-canonical in-flight target")
				}
				inFlightSeen = true
				phase = precommitTail
			case entry.target.relation == indexBatchTargetOld && entry.claim != nil:
				if blockedDestinationSeen {
					return errors.New("rollback-progress claim follows a foreign blocked target")
				}
				if entry.anchor == nil ||
					entry.target.info == nil ||
					!os.SameFile(entry.target.info, entry.anchor.observation.info) {
					return errors.New("rolled-back index target lacks its exact anchor identity witness")
				}
				if untouchedOriginalSeen {
					return errors.New("rollback-progress claim follows the untouched original suffix")
				}
				phase = precommitTail
			case entry.target.relation == indexBatchTargetOld && entry.claim == nil:
				if entry.anchor != nil ||
					entry.target.info == nil ||
					!os.SameFile(entry.target.info, entry.witness.observation.info) {
					return errors.New("untouched old target lacks its exact identity witness")
				}
				phase = precommitTail
				untouchedOriginalSeen = true
			case exactAnchorBlockedTarget:
				if blockedDestinationSeen || untouchedOriginalSeen {
					return errors.New("index batch contains a non-canonical foreign blocked target")
				}
				phase = precommitTail
				blockedDestinationSeen = true
				prepared.blockedEntry = entry
				prepared.blockedEntryErr = recoverableIndexBackupError{
					destination: entry.contract.Path,
					backup:      entry.contract.Anchor,
					reason:      "foreign public target left unchanged; exact original retained in the independent rollback anchor with complete batch evidence",
					err: errors.Join(
						publicationConflict(
							"foreign public target blocks index batch rollback",
							nil,
						),
						blockedEvidenceErr,
						entry.runtimeEvidenceErr,
					),
				}
			case entry.contract.Path == indexFilename &&
				entry.claim == nil &&
				entry.target.relation == indexBatchTargetForeign &&
				entry.target.info != nil:
				if blockedDestinationSeen {
					return errors.New("index batch contains multiple foreign blocked targets")
				}
				phase = precommitTail
				untouchedOriginalSeen = true
				prepared.blockedRootErr = errors.Join(
					prepared.blockedRootErr,
					publicationConflict(
						"foreign root prevented index batch commit; nested rollback completed while batch evidence remains preserved",
						nil,
					),
				)
			default:
				return unrecoverableIndexRecoveryError{
					destination: entry.contract.Path,
					artifact:    entry.contract.Stage,
					reason:      "precommit target is not in the exact publish-prefix state machine",
				}
			}
			if entryDegraded {
				degradedClaims++
				if degradedClaims > 1 {
					return errors.New("index batch contains degraded evidence for multiple targets")
				}
			}
		} else {
			switch entry.target.relation {
			case indexBatchTargetNew:
				if phase != precommitPublished {
					return errors.New("precommit create-only publications are not a canonical prefix")
				}
			case indexBatchTargetAbsent:
				// For create-only entries absence is both the untouched
				// preimage and the rollback postcondition. It therefore joins
				// the tail without proving where the untouched suffix starts.
				phase = precommitTail
			case indexBatchTargetForeign:
				if entry.contract.Path != indexFilename ||
					entry.target.info == nil ||
					blockedDestinationSeen {
					return unrecoverableIndexRecoveryError{
						destination: entry.contract.Path,
						artifact:    entry.contract.Stage,
						reason:      "create-only precommit target is neither absent nor the exact staged inode",
					}
				}
				phase = precommitTail
				prepared.blockedRootErr = publicationConflict(
					"foreign root prevented index batch commit; nested rollback completed while batch evidence remains preserved",
					nil,
				)
			default:
				return unrecoverableIndexRecoveryError{
					destination: entry.contract.Path,
					artifact:    entry.contract.Stage,
					reason:      "create-only precommit target is neither absent nor the exact staged inode",
				}
			}
		}
	}
	if prepared.blockedRootErr != nil && degradedClaims != 0 {
		return errors.New("index batch contains both a foreign root and a degraded ownership claim")
	}
	if prepared.blockedEntryErr != nil && degradedClaims != 0 {
		return errors.New("index batch contains both a foreign blocked target and degraded ownership evidence")
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) apply() error {
	if len(prepared.terminalCommit) != 0 {
		return prepared.applyIndexBatchTerminalCommitCleanup()
	}
	if prepared.hasDegradedClaim() {
		return prepared.applyDegradedClaimRepair()
	}
	if prepared.blockedRootErr != nil {
		return prepared.applyBlockedRootRollback()
	}
	if prepared.blockedEntryErr != nil {
		return prepared.applyBlockedEntryRollback()
	}
	if !prepared.sharedInventory {
		return prepared.applyIndexBatchV3SingleAction()
	}
	return prepared.applyIndexBatchV3Convergence()
}

func (prepared *preparedIndexBatchRecovery) applyIndexBatchTerminalCommitCleanup() error {
	steps, err := prepared.indexBatchTerminalCleanupPlan()
	if err != nil {
		return err
	}
	for _, step := range steps {
		if err := prepared.verifyIndexBatchTerminalCommitScope(); err != nil {
			return err
		}
		if err := prepared.removeArtifact(step.item); err != nil {
			return err
		}
		if err := prepared.verifyIndexBatchTerminalCommitScope(); err != nil {
			return err
		}
	}
	return prepared.verifyIndexBatchTerminalCommitScope()
}

func (prepared *preparedIndexBatchRecovery) indexBatchTerminalCleanupPlan() (
	[]indexBatchTerminalCleanupStep,
	error,
) {
	var steps []indexBatchTerminalCleanupStep
	appendStep := func(
		boundary indexBatchTerminalCleanupBoundary,
		item *indexTransactionInventoryItem,
	) {
		if item != nil {
			steps = append(steps, indexBatchTerminalCleanupStep{
				boundary: boundary,
				item:     item,
			})
		}
	}
	for _, group := range prepared.terminalCommit {
		appendStep(indexBatchTerminalCleanupNonProof, group.claim)
	}
	for _, group := range prepared.terminalCommit {
		appendStep(indexBatchTerminalCleanupNonProof, group.backup)
	}
	for _, group := range prepared.terminalCommit {
		if group.witness != nil && group.witness != group.proof {
			appendStep(indexBatchTerminalCleanupNonProof, group.witness)
		}
	}
	for _, group := range prepared.terminalCommit {
		appendStep(indexBatchTerminalCleanupNonProof, group.newInstall)
	}
	for _, group := range prepared.terminalCommit {
		appendStep(indexBatchTerminalCleanupNonProof, group.discard)
	}
	for _, group := range prepared.terminalCommit {
		if group.stage != nil && group.stage != group.proof {
			appendStep(indexBatchTerminalCleanupNonProof, group.stage)
		}
	}
	appendStep(indexBatchTerminalCleanupManifest, prepared.manifestBoundary)
	var rootProof *indexTransactionInventoryItem
	for _, group := range prepared.terminalCommit {
		if group.destination == indexFilename {
			rootProof = group.proof
			continue
		}
		appendStep(indexBatchTerminalCleanupProof, group.proof)
	}
	if rootProof == nil {
		return nil, errors.New("no-manifest commit cleanup lost its root proof")
	}
	appendStep(indexBatchTerminalCleanupProof, rootProof)
	return steps, nil
}

func (prepared *preparedIndexBatchRecovery) verifyIndexBatchTerminalCommitScope() error {
	if err := prepared.revalidate(); err != nil {
		return err
	}
	return prepared.verifyIndexBatchTerminalCommitTargets()
}

func (prepared *preparedIndexBatchRecovery) runIndexBatchTerminalTargetObservationHooks() error {
	if prepared == nil || prepared.hooks.afterBatchTargetObservation == nil {
		return nil
	}
	for _, group := range prepared.terminalCommit {
		if group == nil || group.proof == nil || group.proof.destination == nil {
			return errors.New("no-manifest commit cleanup has incomplete target proof")
		}
		if err := prepared.hooks.afterBatchTargetObservation(
			group.destination,
			group.proof.destination.parent,
			group.proof.destination.leaf,
		); err != nil {
			return err
		}
	}
	return prepared.verifyIndexBatchTerminalCommitScope()
}

func (prepared *preparedIndexBatchRecovery) verifyIndexBatchTerminalCommitTargets() error {
	for _, group := range prepared.terminalCommit {
		if group == nil ||
			group.proof == nil ||
			group.proof.destination == nil ||
			group.proof.observation.info == nil {
			return errors.New("no-manifest commit cleanup has incomplete target proof")
		}
		currentInfo, currentData, currentMode, present, err :=
			observeIndexBatchTerminalTarget(group.proof)
		if err != nil ||
			present == group.targetAbsent ||
			present &&
				(group.targetInfo == nil ||
					!os.SameFile(group.targetInfo, currentInfo) ||
					!bytes.Equal(group.targetData, currentData) ||
					group.targetMode != currentMode) {
			return unrecoverableIndexRecoveryError{
				destination: group.destination,
				artifact:    group.proof.discovery.relative,
				reason:      "public target changed after terminal classification",
				err:         err,
			}
		}
		if !present {
			continue
		}
		proof := group.proof.observation
		if proof.info == nil ||
			!os.SameFile(proof.info, currentInfo) ||
			!bytes.Equal(proof.data, currentData) ||
			indexPreservedMode(proof.info.Mode()) != currentMode {
			return unrecoverableIndexRecoveryError{
				destination: group.destination,
				artifact:    group.proof.discovery.relative,
				reason:      "public target changed during no-manifest terminal cleanup",
				err:         err,
			}
		}
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) applyIndexBatchV3SingleAction() error {
	switch {
	case prepared.hasIndexBatchV3ActivePreNewRollback():
		return prepared.applyIndexBatchV3PreNewRollback()
	case prepared.hasIndexBatchV3ActiveCreateRollback():
		return prepared.applyIndexBatchV3CreateRollback()
	case prepared.hasIndexBatchV3ActivePublishedRollback():
		return prepared.applyIndexBatchV3PublishedRollback()
	default:
		return prepared.reclassifyIndexBatchV3()
	}
}

func (prepared *preparedIndexBatchRecovery) applyIndexBatchV3Convergence() error {
	maxActions := len(prepared.entries)*2 + 1
	for action := 0; action < maxActions; action++ {
		if err := prepared.reclassifyIndexBatchV3(); err != nil {
			return err
		}
		if prepared.committed {
			if err := prepared.verifyIndexBatchV3CommitProof(); err != nil {
				return err
			}
			return prepared.removeIndexBatchV3ManifestAfterProof()
		}
		switch {
		case prepared.hasIndexBatchV3ActivePreNewRollback():
			if err := prepared.applyIndexBatchV3PreNewRollback(); err != nil {
				return err
			}
			continue
		case prepared.hasIndexBatchV3ActiveCreateRollback():
			if err := prepared.applyIndexBatchV3CreateRollback(); err != nil {
				return err
			}
			continue
		case prepared.hasIndexBatchV3ActivePublishedRollback():
			if err := prepared.applyIndexBatchV3PublishedRollback(); err != nil {
				return err
			}
			continue
		}
		if err := prepared.verifyIndexBatchV3RollbackProof(); err != nil {
			return err
		}
		if err := prepared.indexBatchV3EvidenceConflict(); err != nil {
			return err
		}
		return prepared.removeIndexBatchV3ManifestAfterProof()
	}
	return publicationConflict("v3 index batch recovery did not converge to a global proof", nil)
}

func (prepared *preparedIndexBatchRecovery) indexBatchV3EvidenceConflict() error {
	var conflict error
	for _, entry := range prepared.entries {
		conflict = errors.Join(conflict, indexBatchRecoveryEvidenceConflict(entry))
	}
	return conflict
}

func (prepared *preparedIndexBatchRecovery) verifyIndexBatchV3CommitProof() error {
	if err := prepared.reclassifyIndexBatchV3(); err != nil {
		return err
	}
	for _, entry := range prepared.entries {
		if entry.v3Phase != indexBatchV3PhasePublished ||
			entry.newInstall != nil ||
			entry.restoreInstall != nil ||
			entry.discard != nil ||
			entry.stage == nil ||
			entry.stage.observation.info == nil ||
			entry.target.info == nil ||
			!os.SameFile(entry.target.info, entry.stage.observation.info) {
			return unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    entry.contract.Stage,
				reason:      "v3 commit proof requires every target~S with consumed N",
			}
		}
	}
	return prepared.reclassifyIndexBatchV3()
}

func (prepared *preparedIndexBatchRecovery) verifyIndexBatchV3RollbackProof() error {
	if err := prepared.reclassifyIndexBatchV3(); err != nil {
		return err
	}
	for _, entry := range prepared.entries {
		if entry.contract.OldPresent {
			var witness *indexTransactionInventoryItem
			switch entry.v3Phase {
			case indexBatchV3PhasePreNew:
				witness = entry.witness
			case indexBatchV3PhasePreNewRestored, indexBatchV3PhaseRestored:
				witness = entry.anchor
			default:
				return unrecoverableIndexRecoveryError{
					destination: entry.contract.Path,
					artifact:    entry.contract.Stage,
					reason:      "existing v3 entry has no terminal rollback phase",
				}
			}
			if witness == nil ||
				witness.observation.info == nil ||
				entry.target.info == nil ||
				!os.SameFile(entry.target.info, witness.observation.info) ||
				!indexBatchTargetMatchesContract(
					entry.target.data,
					entry.target.mode,
					entry.contract.OldDigest,
					entry.contract.OldMode,
				) {
				return unrecoverableIndexRecoveryError{
					destination: entry.contract.Path,
					artifact:    entry.contract.Stage,
					reason:      "existing v3 target lacks exact old rollback proof",
				}
			}
			continue
		}
		switch entry.v3Phase {
		case indexBatchV3PhasePreNew:
			if entry.newInstall == nil ||
				entry.stage == nil ||
				entry.newInstall.observation.info == nil ||
				entry.stage.observation.info == nil ||
				!os.SameFile(
					entry.newInstall.observation.info,
					entry.stage.observation.info,
				) {
				return unrecoverableIndexRecoveryError{
					destination: entry.contract.Path,
					artifact:    entry.contract.NewInstall,
					reason:      "untouched create-only rollback lacks exact N~S absence proof",
				}
			}
		case indexBatchV3PhaseCreateRollbackVacated:
			if entry.discard == nil ||
				entry.stage == nil ||
				entry.discard.observation.info == nil ||
				entry.stage.observation.info == nil ||
				!os.SameFile(entry.discard.observation.info, entry.stage.observation.info) {
				return unrecoverableIndexRecoveryError{
					destination: entry.contract.Path,
					artifact:    entry.contract.Discard,
					reason:      "published create-only rollback lacks exact D~S absence proof",
				}
			}
		default:
			return unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    entry.contract.Stage,
				reason:      "create-only v3 entry has no terminal rollback phase",
			}
		}
		if entry.target.info != nil {
			return unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    entry.contract.Stage,
				reason:      "create-only v3 rollback target is not absent",
			}
		}
	}
	return prepared.reclassifyIndexBatchV3()
}

func (prepared *preparedIndexBatchRecovery) removeIndexBatchV3ManifestAfterProof() error {
	if prepared.manifestItem == nil {
		return errors.New("v3 global proof has no batch manifest")
	}
	if err := prepared.reclassifyIndexBatchV3(); err != nil {
		return err
	}
	terminalInventory := make(
		[]*indexTransactionInventoryItem,
		0,
		len(prepared.inventory)-1,
	)
	for _, item := range prepared.inventory {
		if item == nil || item == prepared.manifestItem {
			continue
		}
		if _, removed := prepared.removed[item.discovery.relative]; removed {
			continue
		}
		terminalInventory = append(terminalInventory, item)
	}
	cleanup, handled, err := prepareIndexBatchTerminalCommitCleanup(
		terminalInventory,
		prepared.hooks,
		true,
		prepared.manifestItem,
	)
	if err != nil {
		return err
	}
	if !handled || cleanup == nil || len(cleanup.terminalCommit) == 0 {
		return errors.New("v3 global proof has no complete terminal cleanup plan")
	}
	if err := cleanup.applyIndexBatchTerminalCommitCleanup(); err != nil {
		return err
	}
	if _, removed := cleanup.removed[prepared.manifestItem.discovery.relative]; !removed {
		return errors.New("v3 batch manifest removal did not cross the completion boundary")
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) hasIndexBatchV3ActivePreNewRollback() bool {
	for _, entry := range prepared.entries {
		switch entry.v3Phase {
		case indexBatchV3PhasePreNewVacated,
			indexBatchV3PhasePreNewRollbackAnchored,
			indexBatchV3PhasePreNewRollbackReady:
			return true
		}
	}
	return false
}

func (prepared *preparedIndexBatchRecovery) hasIndexBatchV3ActivePublishedRollback() bool {
	if prepared.committed {
		return false
	}
	for _, entry := range prepared.entries {
		if !entry.contract.OldPresent {
			continue
		}
		switch entry.v3Phase {
		case indexBatchV3PhasePublished,
			indexBatchV3PhaseRollbackAnchored,
			indexBatchV3PhaseRollbackReady,
			indexBatchV3PhasePreRestore:
			return true
		}
	}
	return false
}

func (prepared *preparedIndexBatchRecovery) hasIndexBatchV3ActiveCreateRollback() bool {
	if prepared.committed {
		return false
	}
	for _, entry := range prepared.entries {
		if !entry.contract.OldPresent &&
			entry.v3Phase == indexBatchV3PhasePublished {
			return true
		}
	}
	return false
}

func (prepared *preparedIndexBatchRecovery) applyIndexBatchV3PreNewRollback() error {
	if err := prepared.reclassifyIndexBatchV3(); err != nil {
		return err
	}
	var active *indexBatchRecoveryEntry
	for _, entry := range prepared.entries {
		switch entry.v3Phase {
		case indexBatchV3PhasePreNewVacated,
			indexBatchV3PhasePreNewRollbackAnchored,
			indexBatchV3PhasePreNewRollbackReady:
			if active != nil {
				return errors.New("v3 pre-new rollback contains multiple active entries")
			}
			active = entry
		}
	}
	if active == nil {
		return prepared.reclassifyIndexBatchV3()
	}
	if !active.contract.OldPresent ||
		active.newInstall == nil ||
		active.stage == nil ||
		active.claim == nil ||
		active.witness == nil {
		return unrecoverableIndexRecoveryError{
			destination: active.contract.Path,
			artifact:    active.contract.Stage,
			reason:      "v3 pre-new rollback lacks exact N~S or C~W evidence",
		}
	}

	if active.v3Phase == indexBatchV3PhasePreNewVacated {
		_, anchorErr := prepared.ensureRecoveryAnchor(active)
		reclassifyErr := prepared.reclassifyIndexBatchV3()
		if anchorErr != nil || reclassifyErr != nil {
			return errors.Join(anchorErr, reclassifyErr)
		}
		if active.v3Phase != indexBatchV3PhasePreNewRollbackAnchored {
			return errors.New("v3 pre-new rollback did not reach the anchored phase")
		}
	}
	if active.v3Phase == indexBatchV3PhasePreNewRollbackAnchored {
		_, restoreErr := prepared.ensureRecoveryRestoreInstall(active)
		reclassifyErr := prepared.reclassifyIndexBatchV3()
		if restoreErr != nil || reclassifyErr != nil {
			return errors.Join(restoreErr, reclassifyErr)
		}
		if active.v3Phase != indexBatchV3PhasePreNewRollbackReady {
			return errors.New("v3 pre-new rollback did not reach the restore-ready phase")
		}
	}
	if active.v3Phase == indexBatchV3PhasePreNewRollbackReady {
		installErr := prepared.consumeRecoveryRestoreInstall(active)
		reclassifyErr := prepared.reclassifyIndexBatchV3()
		if installErr != nil || reclassifyErr != nil {
			return errors.Join(installErr, reclassifyErr)
		}
		if active.v3Phase != indexBatchV3PhasePreNewRestored {
			return errors.New("v3 pre-new rollback did not reach the restored phase")
		}
	}
	return prepared.reclassifyIndexBatchV3()
}

func (prepared *preparedIndexBatchRecovery) applyIndexBatchV3CreateRollback() error {
	if err := prepared.reclassifyIndexBatchV3(); err != nil {
		return err
	}
	var active *indexBatchRecoveryEntry
	for index := len(prepared.entries) - 1; index >= 0; index-- {
		entry := prepared.entries[index]
		if !entry.contract.OldPresent &&
			entry.v3Phase == indexBatchV3PhasePublished {
			active = entry
			break
		}
	}
	if active == nil {
		return prepared.reclassifyIndexBatchV3()
	}
	if active.newInstall != nil ||
		active.anchor != nil ||
		active.restoreInstall != nil ||
		active.discard != nil ||
		active.stage == nil ||
		active.target.relation != indexBatchTargetNew ||
		active.target.info == nil ||
		active.stage.observation.info == nil ||
		!os.SameFile(active.target.info, active.stage.observation.info) {
		return unrecoverableIndexRecoveryError{
			destination: active.contract.Path,
			artifact:    active.contract.Stage,
			reason:      "create-only published rollback lacks exact target~S evidence",
		}
	}
	discardErr := prepared.vacateRecoveryPublishedToDiscard(active)
	reclassifyErr := prepared.reclassifyIndexBatchV3()
	if discardErr != nil || reclassifyErr != nil {
		return errors.Join(discardErr, reclassifyErr)
	}
	if active.v3Phase != indexBatchV3PhaseCreateRollbackVacated ||
		active.target.info != nil ||
		active.discard == nil ||
		active.discard.observation.info == nil ||
		!os.SameFile(active.discard.observation.info, active.stage.observation.info) {
		return unrecoverableIndexRecoveryError{
			destination: active.contract.Path,
			artifact:    active.contract.Discard,
			reason:      "create-only rollback did not reach exact D~S with absent target",
		}
	}
	return prepared.reclassifyIndexBatchV3()
}

func (prepared *preparedIndexBatchRecovery) applyIndexBatchV3PublishedRollback() error {
	if err := prepared.reclassifyIndexBatchV3(); err != nil {
		return err
	}
	var active *indexBatchRecoveryEntry
	for _, entry := range prepared.entries {
		switch entry.v3Phase {
		case indexBatchV3PhaseRollbackAnchored,
			indexBatchV3PhaseRollbackReady,
			indexBatchV3PhasePreRestore:
			if active != nil {
				return errors.New("v3 published rollback contains multiple active entries")
			}
			active = entry
		}
	}
	if active == nil {
		for index := len(prepared.entries) - 1; index >= 0; index-- {
			if prepared.entries[index].contract.OldPresent &&
				prepared.entries[index].v3Phase == indexBatchV3PhasePublished {
				active = prepared.entries[index]
				break
			}
		}
	}
	if active == nil {
		return prepared.reclassifyIndexBatchV3()
	}
	if !active.contract.OldPresent ||
		active.newInstall != nil ||
		active.stage == nil ||
		active.claim == nil ||
		active.witness == nil {
		return unrecoverableIndexRecoveryError{
			destination: active.contract.Path,
			artifact:    active.contract.Stage,
			reason:      "v3 published rollback lacks exact S or C~W evidence",
		}
	}

	if active.v3Phase == indexBatchV3PhasePublished {
		_, anchorErr := prepared.ensureRecoveryAnchor(active)
		reclassifyErr := prepared.reclassifyIndexBatchV3()
		if anchorErr != nil || reclassifyErr != nil {
			return errors.Join(anchorErr, reclassifyErr)
		}
		if active.v3Phase != indexBatchV3PhaseRollbackAnchored {
			return errors.New("v3 published rollback did not reach the anchored phase")
		}
	}
	if active.v3Phase == indexBatchV3PhaseRollbackAnchored {
		_, restoreErr := prepared.ensureRecoveryRestoreInstall(active)
		reclassifyErr := prepared.reclassifyIndexBatchV3()
		if restoreErr != nil || reclassifyErr != nil {
			return errors.Join(restoreErr, reclassifyErr)
		}
		if active.v3Phase != indexBatchV3PhaseRollbackReady {
			return errors.New("v3 published rollback did not reach the restore-ready phase")
		}
	}
	if active.v3Phase == indexBatchV3PhaseRollbackReady {
		discardErr := prepared.vacateRecoveryPublishedToDiscard(active)
		reclassifyErr := prepared.reclassifyIndexBatchV3()
		if discardErr != nil || reclassifyErr != nil {
			return errors.Join(discardErr, reclassifyErr)
		}
		if active.v3Phase != indexBatchV3PhasePreRestore {
			return errors.New("v3 published rollback did not reach the pre-restore phase")
		}
	}
	if active.v3Phase == indexBatchV3PhasePreRestore {
		installErr := prepared.consumeRecoveryRestoreInstall(active)
		reclassifyErr := prepared.reclassifyIndexBatchV3()
		if installErr != nil || reclassifyErr != nil {
			return errors.Join(installErr, reclassifyErr)
		}
		if active.v3Phase != indexBatchV3PhaseRestored {
			return errors.New("v3 published rollback did not reach the restored phase")
		}
	}
	return prepared.reclassifyIndexBatchV3()
}

func (prepared *preparedIndexBatchRecovery) reclassifyIndexBatchV3() error {
	if err := prepared.revalidate(); err != nil {
		return err
	}
	targets := make([]indexBatchTargetSnapshot, 0, len(prepared.entries))
	for _, entry := range prepared.entries {
		current, err := observeIndexBatchTarget(
			entry.target.item,
			entry.contract,
			entry.stage,
			entry.backup,
			entry.claim,
			entry.anchor,
			entry.witness,
		)
		if err != nil {
			return err
		}
		entry.target = normalizeIndexBatchTargetSnapshot(entry, current)
		entry.v3Phase, err = prepared.classifyIndexBatchV3Entry(entry)
		if err != nil {
			return err
		}
		targets = append(targets, entry.target)
	}
	prepared.targets = targets
	if err := prepared.validateIndexBatchV3PhaseMixture(); err != nil {
		return err
	}
	return prepared.verifyCapturedTargetSnapshots()
}

func (prepared *preparedIndexBatchRecovery) applyBlockedEntryRollback() error {
	if prepared.blockedEntry == nil {
		return errors.New("foreign-blocked index batch recovery has no blocked entry")
	}
	if err := prepared.revalidate(); err != nil {
		return errors.Join(prepared.blockedEntryErr, err)
	}
	if err := prepared.verifyCapturedTargetSnapshots(); err != nil {
		return errors.Join(prepared.blockedEntryErr, err)
	}
	return prepared.blockedEntryErr
}

func (prepared *preparedIndexBatchRecovery) applyBlockedRootRollback() error {
	root := prepared.rootEntry()
	if root == nil {
		return errors.New("foreign-root index batch recovery has no root entry")
	}
	lastNested := len(prepared.entries) - 2
	if err := prepared.verifyBlockedRootRollbackProgress(lastNested, root); err != nil {
		return errors.Join(prepared.blockedRootErr, err)
	}
	for index := lastNested; index >= 0; index-- {
		if err := prepared.rollbackTarget(prepared.entries[index]); err != nil {
			return errors.Join(prepared.blockedRootErr, err)
		}
		if err := prepared.verifyBlockedRootRollbackProgress(index-1, root); err != nil {
			return errors.Join(prepared.blockedRootErr, err)
		}
	}
	return prepared.blockedRootErr
}

func (prepared *preparedIndexBatchRecovery) verifyBlockedRootRollbackProgress(
	lastUntouched int,
	root *indexBatchRecoveryEntry,
) error {
	for index, entry := range prepared.entries {
		current, err := observeIndexBatchTarget(
			entry.target.item,
			entry.contract,
			entry.stage,
			entry.backup,
			entry.claim,
			entry.anchor,
			entry.witness,
		)
		if err != nil {
			return err
		}
		if entry == root {
			if !sameIndexBatchTargetSnapshot(current, root.target) {
				return errors.New("foreign index batch root changed during nested rollback")
			}
			continue
		}
		if index <= lastUntouched {
			if !sameIndexBatchTargetSnapshot(current, entry.target) {
				return errors.New("untouched nested index batch target changed")
			}
			continue
		}
		want := indexBatchTargetAbsent
		if entry.contract.OldPresent {
			want = indexBatchTargetOld
		}
		if current.relation != want {
			return errors.New("nested index batch target did not reach its rollback postcondition")
		}
	}
	return prepared.revalidate()
}

func (prepared *preparedIndexBatchRecovery) hasDegradedClaim() bool {
	for _, entry := range prepared.entries {
		if entry.degradedStageErr != nil ||
			entry.degradedClaimErr != nil ||
			entry.degradedBackupErr != nil ||
			entry.degradedWitnessErr != nil {
			return true
		}
	}
	return false
}

func (prepared *preparedIndexBatchRecovery) applyDegradedClaimRepair() error {
	if err := prepared.revalidate(); err != nil {
		return err
	}
	if err := prepared.verifyCapturedTargetSnapshots(); err != nil {
		return err
	}
	var degraded *indexBatchRecoveryEntry
	for _, entry := range prepared.entries {
		if entry.degradedStageErr == nil &&
			entry.degradedClaimErr == nil &&
			entry.degradedBackupErr == nil &&
			entry.degradedWitnessErr == nil {
			continue
		}
		if degraded != nil {
			return errors.New("index batch contains multiple degraded ownership claims")
		}
		degraded = entry
	}
	if degraded == nil {
		return errors.New("index batch degraded-claim repair has no degraded claim")
	}
	switch degraded.v3Phase {
	case indexBatchV3PhasePreNewVacated,
		indexBatchV3PhasePreNewRollbackAnchored,
		indexBatchV3PhasePreNewRollbackReady:
		if err := prepared.applyIndexBatchV3PreNewRollback(); err != nil {
			return err
		}
	case indexBatchV3PhasePublished,
		indexBatchV3PhaseRollbackAnchored,
		indexBatchV3PhaseRollbackReady,
		indexBatchV3PhasePreRestore:
		if err := prepared.applyIndexBatchV3PublishedRollback(); err != nil {
			return err
		}
	case indexBatchV3PhasePreNewRestored, indexBatchV3PhaseRestored:
		// The exact anchor target is already in a canonical terminal rollback
		// phase. Preserve the degraded regular evidence and stop unchanged.
	default:
		return unrecoverableIndexRecoveryError{
			destination: degraded.contract.Path,
			artifact:    degraded.contract.Stage,
			reason:      "degraded evidence repair has no canonical v3 rollback transition",
		}
	}
	if err := prepared.verifyDegradedClaimRepair(degraded); err != nil {
		return err
	}
	if err := prepared.revalidate(); err != nil {
		return err
	}
	return indexBatchRecoveryEvidenceConflict(degraded)
}

func (prepared *preparedIndexBatchRecovery) verifyDegradedClaimRepair(
	degraded *indexBatchRecoveryEntry,
) error {
	for _, entry := range prepared.entries {
		current, err := observeIndexBatchTarget(
			entry.target.item,
			entry.contract,
			entry.stage,
			entry.backup,
			entry.claim,
			entry.anchor,
			entry.witness,
		)
		if err != nil {
			return err
		}
		if entry != degraded {
			if !sameIndexBatchTargetSnapshot(current, entry.target) {
				return errors.New("unrelated index batch target changed during degraded-claim repair")
			}
			continue
		}
		if current.relation != indexBatchTargetOld ||
			current.info == nil ||
			entry.anchor == nil ||
			entry.anchor.observation.info == nil ||
			!os.SameFile(current.info, entry.anchor.observation.info) {
			return unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    entry.contract.Anchor,
				reason:      "degraded-claim repair did not install the exact independent anchor inode",
			}
		}
		entry.target = current
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) rollbackTarget(
	entry *indexBatchRecoveryEntry,
) error {
	current, err := observeIndexBatchTarget(
		entry.target.item,
		entry.contract,
		entry.stage,
		entry.backup,
		entry.claim,
		entry.anchor,
		entry.witness,
	)
	if err != nil {
		return err
	}
	current = normalizeIndexBatchTargetSnapshot(entry, current)
	if entry.contract.OldPresent &&
		current.relation == indexBatchTargetOld &&
		entry.anchor == nil &&
		entry.claim == nil {
		entry.target = current
		return nil
	}
	var anchor *indexTransactionInventoryItem
	if entry.contract.OldPresent {
		if current.relation == indexBatchTargetOld && entry.anchor != nil {
			if !os.SameFile(current.info, entry.anchor.observation.info) {
				return errors.New("index batch rollback target has old bytes without the anchor identity witness")
			}
			entry.target = current
			return indexBatchRecoveryEvidenceConflict(entry)
		}
		anchor, err = prepared.ensureRecoveryAnchor(entry)
		if err != nil {
			return err
		}
		if err := prepared.verifyCapturedTargetSnapshots(); err != nil {
			return err
		}
	}
	if current.relation == indexBatchTargetNew {
		spec := publicationSpecFromBytes(current.info, current.data)
		removeHooks := publicationBarrierHooks{
			validateScope: prepared.scopeValidator(entry, nil),
		}
		removed, err := guardedRemovePublicationFile(
			context.Background(),
			current.item.destination.parent,
			current.item.destination.leaf,
			spec,
			removeHooks,
		)
		if err != nil || !removed {
			return unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    entry.contract.Stage,
				reason:      "exact staged target could not be vacated during batch rollback",
				err:         err,
			}
		}
		current.info = nil
		current.data = nil
		current.mode = 0
		current.relation = indexBatchTargetAbsent
		if prepared.hooks.afterBatchRecoveryVacate != nil {
			if err := prepared.hooks.afterBatchRecoveryVacate(
				entry.contract.Path,
				current.item.destination.parent,
				current.item.destination.leaf,
			); err != nil {
				return err
			}
		}
		if err := prepared.captureStableRecoveryEvidenceDrift(entry); err != nil {
			return err
		}
		if err := prepared.revalidate(); err != nil {
			return err
		}
		if err := prepared.verifyRecoveryInFlightTargets(entry); err != nil {
			return err
		}
	}
	if entry.contract.OldPresent {
		if current.relation == indexBatchTargetOld {
			entry.target = current
			return nil
		}
		if current.relation != indexBatchTargetAbsent &&
			!(current.relation == indexBatchTargetForeign && current.info == nil) {
			return errors.New("index batch rollback target became foreign")
		}
		if err := prepared.verifyRecoveryAnchor(entry, anchor); err != nil {
			return err
		}
		installHooks := indexRestoreInstallCoreHooks(
			anchor.destination,
			anchor.discovery.kind,
			anchor.discovery.name,
			prepared.hooks,
		)
		installHooks.validateScope = prepared.scopeValidator(entry, nil)
		installed, err := guardedInstallPublicationLeaf(
			context.Background(),
			anchor.destination.parent,
			anchor.discovery.name,
			publicationSpecFromBytes(anchor.observation.info, anchor.observation.data),
			anchor.destination.leaf,
			installHooks,
		)
		if err != nil || !installed {
			return unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    anchor.discovery.relative,
				reason:      "exact independent rollback anchor could not restore the batch target",
				err:         err,
			}
		}
	}
	if prepared.hooks.afterBatchRecoveryAction != nil {
		if err := prepared.hooks.afterBatchRecoveryAction(
			entry.contract.Path,
			entry.target.item.destination.parent,
			entry.target.item.destination.leaf,
		); err != nil {
			return err
		}
	}
	refreshed, err := observeIndexBatchTarget(
		entry.target.item,
		entry.contract,
		entry.stage,
		entry.backup,
		entry.claim,
		entry.anchor,
		entry.witness,
	)
	if err != nil {
		return err
	}
	if entry.contract.OldPresent && refreshed.relation != indexBatchTargetOld ||
		!entry.contract.OldPresent && refreshed.relation != indexBatchTargetAbsent {
		return errors.New("index batch target did not reach its rollback postcondition")
	}
	entry.target = refreshed
	if err := prepared.captureStableRecoveryEvidenceDrift(entry); err != nil {
		return err
	}
	if err := prepared.revalidate(); err != nil {
		return err
	}
	if err := prepared.verifyCapturedTargetSnapshots(); err != nil {
		return err
	}
	return indexBatchRecoveryEvidenceConflict(entry)
}

func (prepared *preparedIndexBatchRecovery) verifyRecoveryInFlightTargets(
	inFlight *indexBatchRecoveryEntry,
) error {
	for _, entry := range prepared.entries {
		current, err := observeIndexBatchTarget(
			entry.target.item,
			entry.contract,
			entry.stage,
			entry.backup,
			entry.claim,
			entry.anchor,
			entry.witness,
		)
		if err != nil {
			return err
		}
		if entry == inFlight {
			if current.info != nil {
				return errors.New("vacated index batch target reappeared before anchor installation")
			}
			continue
		}
		if !sameIndexBatchTargetSnapshot(current, entry.target) {
			return errors.New("unrelated index batch target changed around rollback action")
		}
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) ensureRecoveryAnchor(
	entry *indexBatchRecoveryEntry,
) (*indexTransactionInventoryItem, error) {
	if entry.anchor != nil {
		if err := prepared.verifyRecoveryAnchor(entry, entry.anchor); err != nil {
			return nil, err
		}
		return entry.anchor, nil
	}
	source := entry.rollbackSource
	if source == nil {
		return nil, unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.Anchor,
			reason:      "rollback plan has no immutable pre-anchor original source",
		}
	}
	if err := prepared.revalidateRecoveryOriginalSource(entry, source); err != nil {
		return nil, err
	}
	name := path.Base(entry.contract.Anchor)
	anchorHooks := indexArtifactCoreHooks(
		source.destination,
		indexArtifactAnchor,
		name,
		prepared.hooks,
	)
	entry.anchorSource = source
	anchorHooks.validateScope = prepared.scopeValidator(entry, nil)
	spec, createErr := createDurablePublicationCopy(
		context.Background(),
		source.destination.parent,
		source.discovery.name,
		publicationSpecFromBytes(source.observation.info, source.observation.data),
		name,
		anchorHooks,
	)
	entry.anchorSource = nil
	if !spec.created {
		return nil, unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.Anchor,
			reason:      "manifest-bound rollback anchor path could not be acquired without replacement",
			err:         createErr,
		}
	}
	observation := observeIndexTransactionArtifact(
		source.destination.parent,
		source.destination.output.relative,
		source.discovery.directory,
		name,
		name,
		indexArtifactAnchor,
	)
	anchor := &indexTransactionInventoryItem{
		discovery: indexTransactionDiscovery{
			kind:         indexArtifactAnchor,
			directory:    source.discovery.directory,
			name:         name,
			protocolName: name,
			relative:     entry.contract.Anchor,
			observation:  observation,
		},
		destination: source.destination,
		observation: observation,
	}
	entry.anchor = anchor
	prepared.inventory = append(prepared.inventory, anchor)
	if err := prepared.verifyRecoveryAnchor(entry, anchor); err != nil {
		return nil, errors.Join(createErr, err)
	}
	if err := prepared.captureStableRecoveryEvidenceDrift(entry); err != nil {
		return nil, errors.Join(createErr, err)
	}
	if createErr != nil && !errors.Is(createErr, errPublicationConflict) {
		return nil, createErr
	}
	if createErr != nil {
		entry.runtimeEvidenceErr = errors.Join(entry.runtimeEvidenceErr, createErr)
	}
	if err := prepared.revalidate(); err != nil {
		return nil, err
	}
	return anchor, nil
}

func (prepared *preparedIndexBatchRecovery) ensureRecoveryRestoreInstall(
	entry *indexBatchRecoveryEntry,
) (*indexTransactionInventoryItem, error) {
	if entry.restoreInstall != nil {
		if err := prepared.verifyRecoveryRestoreInstall(entry, entry.restoreInstall); err != nil {
			return nil, err
		}
		return entry.restoreInstall, nil
	}
	if entry.anchor == nil {
		return nil, unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.RestoreInstall,
			reason:      "restore install token has no exact rollback anchor",
		}
	}
	if err := prepared.verifyRecoveryAnchor(entry, entry.anchor); err != nil {
		return nil, err
	}
	name := path.Base(entry.contract.RestoreInstall)
	restoreHooks := indexArtifactCoreHooks(
		entry.anchor.destination,
		indexArtifactRestoreInstall,
		name,
		prepared.hooks,
	)
	restoreHooks.validateScope = prepared.scopeValidator(entry, nil)
	spec, createErr := createDurablePublicationWitness(
		context.Background(),
		entry.anchor.destination.parent,
		entry.anchor.discovery.name,
		publicationSpecFromBytes(
			entry.anchor.observation.info,
			entry.anchor.observation.data,
		),
		name,
		restoreHooks,
	)
	if !spec.created {
		return nil, unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.RestoreInstall,
			reason:      "manifest-bound restore install path could not be acquired without replacement",
			err:         createErr,
		}
	}
	observation := observeIndexTransactionArtifact(
		entry.anchor.destination.parent,
		entry.anchor.destination.output.relative,
		entry.anchor.discovery.directory,
		name,
		name,
		indexArtifactRestoreInstall,
	)
	restoreInstall := &indexTransactionInventoryItem{
		discovery: indexTransactionDiscovery{
			kind:         indexArtifactRestoreInstall,
			directory:    entry.anchor.discovery.directory,
			name:         name,
			protocolName: name,
			relative:     entry.contract.RestoreInstall,
			observation:  observation,
		},
		destination: entry.anchor.destination,
		observation: observation,
	}
	entry.restoreInstall = restoreInstall
	prepared.inventory = append(prepared.inventory, restoreInstall)
	currentAnchor := observeIndexTransactionArtifact(
		entry.anchor.destination.parent,
		entry.anchor.destination.output.relative,
		entry.anchor.discovery.directory,
		entry.anchor.discovery.name,
		entry.anchor.discovery.protocolName,
		entry.anchor.discovery.kind,
	)
	if currentAnchor.reason != "" ||
		currentAnchor.info == nil ||
		!os.SameFile(currentAnchor.info, restoreInstall.observation.info) {
		return nil, errors.Join(
			createErr,
			unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    entry.contract.Anchor,
				reason:      "rollback anchor changed while materializing restore install token",
			},
		)
	}
	entry.anchor.observation = currentAnchor
	entry.anchor.discovery.observation = currentAnchor
	if err := prepared.verifyRecoveryRestoreInstall(entry, restoreInstall); err != nil {
		return nil, errors.Join(createErr, err)
	}
	if createErr != nil {
		return restoreInstall, createErr
	}
	if err := prepared.revalidate(); err != nil {
		return nil, err
	}
	return restoreInstall, nil
}

func (prepared *preparedIndexBatchRecovery) verifyRecoveryRestoreInstall(
	entry *indexBatchRecoveryEntry,
	restoreInstall *indexTransactionInventoryItem,
) error {
	if entry == nil || entry.anchor == nil {
		return errors.New("restore install verification has no rollback anchor")
	}
	if err := prepared.validateArtifact(
		restoreInstall,
		indexArtifactRestoreInstall,
		entry.contract.OldDigest,
		entry.contract.OldMode,
	); err != nil {
		return err
	}
	if restoreInstall.observation.info == nil ||
		entry.anchor.observation.info == nil ||
		!os.SameFile(restoreInstall.observation.info, entry.anchor.observation.info) {
		return unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.RestoreInstall,
			reason:      "restore install token does not retain rollback anchor identity",
		}
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) vacateRecoveryPublishedToDiscard(
	entry *indexBatchRecoveryEntry,
) error {
	if entry == nil ||
		entry.stage == nil ||
		entry.discard != nil ||
		entry.target.relation != indexBatchTargetNew ||
		entry.target.info == nil ||
		entry.stage.observation.info == nil ||
		!os.SameFile(entry.target.info, entry.stage.observation.info) {
		return unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.Discard,
			reason:      "published target lacks exact retained S identity before discard",
		}
	}
	name := path.Base(entry.contract.Discard)
	discardHooks := indexArtifactCoreHooks(
		entry.stage.destination,
		indexArtifactDiscard,
		name,
		prepared.hooks,
	)
	discardHooks.validateScope = prepared.scopeValidator(entry, nil)
	discardHooks.beforeVacate = func() error {
		if prepared.hooks.beforeVacate == nil {
			return nil
		}
		return prepared.hooks.beforeVacate(
			entry.contract.Path,
			entry.target.item.destination.parent,
			entry.target.item.destination.leaf,
		)
	}
	discardHooks.afterVacateRename = func() error {
		if prepared.hooks.afterVacateRename == nil {
			return nil
		}
		return prepared.hooks.afterVacateRename(
			entry.contract.Path,
			entry.target.item.destination.parent,
			entry.target.item.destination.leaf,
			name,
		)
	}
	discardHooks.afterVacate = func() error {
		if prepared.hooks.afterVacate == nil {
			return nil
		}
		return prepared.hooks.afterVacate(
			entry.contract.Path,
			entry.target.item.destination.parent,
			entry.target.item.destination.leaf,
			name,
		)
	}
	claimed, vacateErr := guardedVacatePublicationLeafWithCompensation(
		context.Background(),
		entry.target.item.destination.parent,
		entry.target.item.destination.leaf,
		publicationSpecFromBytes(entry.target.info, entry.target.data),
		name,
		discardHooks,
	)
	if claimed.created {
		observation := observeIndexTransactionArtifact(
			entry.stage.destination.parent,
			entry.stage.destination.output.relative,
			entry.stage.discovery.directory,
			name,
			name,
			indexArtifactDiscard,
		)
		discard := &indexTransactionInventoryItem{
			discovery: indexTransactionDiscovery{
				kind:         indexArtifactDiscard,
				directory:    entry.stage.discovery.directory,
				name:         name,
				protocolName: name,
				relative:     entry.contract.Discard,
				observation:  observation,
			},
			destination: entry.stage.destination,
			observation: observation,
		}
		entry.discard = discard
		prepared.inventory = append(prepared.inventory, discard)
	}
	reclassifyErr := prepared.reclassifyIndexBatchV3()
	if vacateErr != nil || reclassifyErr != nil {
		return errors.Join(vacateErr, reclassifyErr)
	}
	if !claimed.created ||
		entry.discard == nil ||
		entry.discard.observation.info == nil ||
		entry.stage.observation.info == nil ||
		!os.SameFile(entry.discard.observation.info, entry.stage.observation.info) {
		return unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.Discard,
			reason:      "discard token does not retain staged target identity",
		}
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) consumeRecoveryRestoreInstall(
	entry *indexBatchRecoveryEntry,
) error {
	restoreInstall := entry.restoreInstall
	if err := prepared.verifyRecoveryRestoreInstall(entry, restoreInstall); err != nil {
		return err
	}
	restoreHooks := indexRestoreInstallCoreHooks(
		restoreInstall.destination,
		indexArtifactRestoreInstall,
		restoreInstall.discovery.name,
		prepared.hooks,
	)
	restoreHooks.validateScope = prepared.scopeValidator(entry, restoreInstall)
	installed, installErr := guardedConsumePublicationInstallLeaf(
		context.Background(),
		restoreInstall.destination.parent,
		restoreInstall.discovery.name,
		publicationSpecFromBytes(
			restoreInstall.observation.info,
			restoreInstall.observation.data,
		),
		entry.anchor.discovery.name,
		publicationSpecFromBytes(
			entry.anchor.observation.info,
			entry.anchor.observation.data,
		),
		entry.target.item.destination.leaf,
		restoreHooks,
	)
	if installed {
		prepared.removed[restoreInstall.discovery.relative] = struct{}{}
		entry.restoreInstall = nil
	}
	var recoverableStop recoverableIndexBackupError
	if errors.As(installErr, &recoverableStop) {
		return installErr
	}
	if reclassifyErr := prepared.reclassifyIndexBatchV3(); reclassifyErr != nil {
		return errors.Join(installErr, reclassifyErr)
	}
	if installErr != nil || !installed {
		reason := "manifest-bound restore install token could not restore original"
		if installErr != nil {
			reason += ": " + installErr.Error()
		} else {
			reason += ": install primitive reported no mutation"
		}
		return unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.RestoreInstall,
			reason:      reason,
			err:         installErr,
		}
	}
	if entry.v3Phase != indexBatchV3PhasePreNewRestored &&
		entry.v3Phase != indexBatchV3PhaseRestored ||
		entry.target.info == nil ||
		entry.anchor.observation.info == nil ||
		!os.SameFile(entry.target.info, entry.anchor.observation.info) {
		return unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.RestoreInstall,
			reason:      "consumed restore install token has no exact restored target",
		}
	}
	if prepared.hooks.afterBatchRecoveryAction != nil {
		if err := prepared.hooks.afterBatchRecoveryAction(
			entry.contract.Path,
			entry.target.item.destination.parent,
			entry.target.item.destination.leaf,
		); err != nil {
			return err
		}
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) captureStableRecoveryEvidenceDrift(
	entry *indexBatchRecoveryEntry,
) error {
	for _, item := range []*indexTransactionInventoryItem{
		entry.stage,
		entry.claim,
		entry.backup,
		entry.witness,
	} {
		if item == nil {
			continue
		}
		current := observeIndexTransactionArtifact(
			item.destination.parent,
			item.destination.output.relative,
			item.discovery.directory,
			item.discovery.name,
			item.discovery.protocolName,
			item.discovery.kind,
		)
		driftErr := compareIndexRecoveryObservations(
			item.destination.output.relative,
			singleIndexRecoveryObservation(item.observation),
			singleIndexRecoveryObservation(current),
		)
		if driftErr == nil {
			continue
		}
		if eligibilityErr := prepared.postAnchorSoftEligibilityError(
			entry,
			item,
			current,
		); eligibilityErr != nil {
			return driftErr
		}
		item.observation = current
		item.discovery.observation = current
		entry.runtimeEvidenceErr = errors.Join(entry.runtimeEvidenceErr, driftErr)
		return indexBatchRecoveryEvidenceConflict(entry)
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) postAnchorSoftEligibilityError(
	entry *indexBatchRecoveryEntry,
	drift *indexTransactionInventoryItem,
	current indexRecoveryObservation,
) error {
	if prepared == nil || entry == nil || drift == nil {
		return errors.New("post-anchor soft evidence has no recovery context")
	}
	if current.info == nil ||
		current.info.Mode()&os.ModeSymlink != 0 ||
		!current.info.Mode().IsRegular() {
		return errors.New(
			"post-anchor soft evidence is not a present regular file: " +
				string(drift.discovery.kind) + " " + drift.discovery.relative,
		)
	}
	if drift != entry.stage &&
		drift != entry.backup &&
		drift != entry.claim &&
		drift != entry.witness {
		return errors.New("post-anchor drift role is a hard proof")
	}
	if entry.anchor == nil || entry.discard == nil || prepared.manifestItem == nil {
		return errors.New("post-anchor terminal proof set is incomplete")
	}
	exact := func(item *indexTransactionInventoryItem) bool {
		if item == nil {
			return false
		}
		observed := observeIndexTransactionArtifact(
			item.destination.parent,
			item.destination.output.relative,
			item.discovery.directory,
			item.discovery.name,
			item.discovery.protocolName,
			item.discovery.kind,
		)
		return compareIndexRecoveryObservations(
			item.destination.output.relative,
			singleIndexRecoveryObservation(item.observation),
			singleIndexRecoveryObservation(observed),
		) == nil
	}
	if !exact(entry.anchor) ||
		entry.anchor.observation.info == nil {
		return errors.New("post-anchor A proof is not exact")
	}
	if !exact(entry.discard) ||
		entry.discard.observation.info == nil ||
		entry.stage == nil ||
		entry.stage.observation.info == nil ||
		!os.SameFile(entry.discard.observation.info, entry.stage.observation.info) {
		return errors.New("post-anchor D~S proof is not exact")
	}
	if !exact(prepared.manifestItem) {
		return errors.New("post-anchor manifest boundary is not exact")
	}
	if entry.restoreInstall != nil {
		if _, err := entry.restoreInstall.destination.parent.Lstat(
			entry.restoreInstall.discovery.name,
		); !errors.Is(err, fs.ErrNotExist) {
			return errors.New("post-anchor R token is not consumed")
		}
	}
	target, err := observeIndexBatchTarget(
		entry.target.item,
		entry.contract,
		entry.stage,
		entry.backup,
		entry.claim,
		entry.anchor,
		entry.witness,
	)
	if err != nil ||
		target.relation != indexBatchTargetOld ||
		target.info == nil ||
		!os.SameFile(target.info, entry.anchor.observation.info) ||
		!indexBatchTargetMatchesContract(
			target.data,
			target.mode,
			entry.contract.OldDigest,
			entry.contract.OldMode,
		) {
		return errors.New("post-anchor public target is not exact A")
	}
	if err := verifyIndexDestinationParent(entry.target.item.destination); err != nil {
		return errors.Join(
			errors.New("post-anchor destination parent is not exact"),
			err,
		)
	}
	return nil
}

func indexBatchRecoveryEvidenceConflict(entry *indexBatchRecoveryEntry) error {
	if entry == nil {
		return nil
	}
	evidenceErr := errors.Join(
		entry.degradedStageErr,
		entry.degradedClaimErr,
		entry.degradedBackupErr,
		entry.degradedWitnessErr,
		entry.runtimeEvidenceErr,
	)
	if evidenceErr == nil {
		return nil
	}
	return recoverableIndexBackupError{
		destination: entry.contract.Path,
		backup:      entry.contract.Anchor,
		reason:      "target restored from the exact independent rollback anchor; foreign source evidence and batch artifacts preserved for manual inspection",
		err:         evidenceErr,
	}
}

func (prepared *preparedIndexBatchRecovery) verifyRecoveryAnchor(
	entry *indexBatchRecoveryEntry,
	anchor *indexTransactionInventoryItem,
) error {
	if err := prepared.validateArtifact(
		anchor,
		indexArtifactAnchor,
		entry.contract.OldDigest,
		entry.contract.OldMode,
	); err != nil {
		return err
	}
	for _, other := range []*indexTransactionInventoryItem{
		entry.backup,
		entry.claim,
		entry.stage,
		entry.witness,
	} {
		if other != nil &&
			other.observation.info != nil &&
			os.SameFile(anchor.observation.info, other.observation.info) {
			return unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    entry.contract.Anchor,
				reason:      "rollback anchor is not an independent inode",
			}
		}
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) selectRecoveryOriginalSource(
	entry *indexBatchRecoveryEntry,
) (*indexTransactionInventoryItem, error) {
	if err := prepared.validateCurrentRecoveryOriginal(
		entry,
		entry.backup,
		indexArtifactBackup,
	); err != nil {
		return nil, unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.Backup,
			reason:      "no exact independent manifest-bound backup remains before anchor durability",
			err:         err,
		}
	}
	for _, forbidden := range []*indexTransactionInventoryItem{
		entry.stage,
		entry.claim,
		entry.witness,
	} {
		if forbidden != nil &&
			forbidden.observation.info != nil &&
			os.SameFile(entry.backup.observation.info, forbidden.observation.info) {
			return nil, unrecoverableIndexRecoveryError{
				destination: entry.contract.Path,
				artifact:    entry.contract.Backup,
				reason:      "manifest-bound backup is not an independent original source",
			}
		}
	}
	claimErr := prepared.validateCurrentRecoveryOriginal(
		entry,
		entry.claim,
		indexArtifactRestore,
	)
	witnessErr := prepared.validateCurrentRecoveryOriginal(
		entry,
		entry.witness,
		indexArtifactWitness,
	)
	if claimErr == nil &&
		witnessErr == nil &&
		entry.claim.observation.info != nil &&
		entry.witness.observation.info != nil &&
		os.SameFile(entry.claim.observation.info, entry.witness.observation.info) {
		return entry.claim, nil
	}
	return entry.backup, nil
}

func (prepared *preparedIndexBatchRecovery) revalidateRecoveryOriginalSource(
	entry *indexBatchRecoveryEntry,
	source *indexTransactionInventoryItem,
) error {
	if err := prepared.validateCurrentRecoveryOriginal(
		entry,
		entry.backup,
		indexArtifactBackup,
	); err != nil {
		return unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.Backup,
			reason:      "independent backup changed before anchor durability",
			err:         err,
		}
	}
	if source == entry.backup {
		return nil
	}
	if source != entry.claim {
		return unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.Anchor,
			reason:      "immutable rollback source no longer belongs to the recovery plan",
		}
	}
	claimErr := prepared.validateCurrentRecoveryOriginal(
		entry,
		entry.claim,
		indexArtifactRestore,
	)
	witnessErr := prepared.validateCurrentRecoveryOriginal(
		entry,
		entry.witness,
		indexArtifactWitness,
	)
	if claimErr != nil ||
		witnessErr != nil ||
		entry.claim.observation.info == nil ||
		entry.witness.observation.info == nil ||
		!os.SameFile(entry.claim.observation.info, entry.witness.observation.info) {
		return unrecoverableIndexRecoveryError{
			destination: entry.contract.Path,
			artifact:    entry.contract.Claim,
			reason:      "selected C~W source changed before anchor durability",
			err:         errors.Join(claimErr, witnessErr),
		}
	}
	return nil
}

func (prepared *preparedIndexBatchRecovery) validateCurrentRecoveryOriginal(
	entry *indexBatchRecoveryEntry,
	item *indexTransactionInventoryItem,
	kind indexArtifactKind,
) error {
	if item == nil {
		return errors.New("manifest-bound original evidence role is missing")
	}
	current := observeIndexTransactionArtifact(
		item.destination.parent,
		item.destination.output.relative,
		item.discovery.directory,
		item.discovery.name,
		item.discovery.protocolName,
		item.discovery.kind,
	)
	if err := compareIndexRecoveryObservations(
		item.destination.output.relative,
		singleIndexRecoveryObservation(item.observation),
		singleIndexRecoveryObservation(current),
	); err != nil {
		return err
	}
	return prepared.validateArtifact(
		item,
		kind,
		entry.contract.OldDigest,
		entry.contract.OldMode,
	)
}

func sameIndexBatchTargetSnapshot(left, right indexBatchTargetSnapshot) bool {
	if left.relation != right.relation ||
		left.mode != right.mode ||
		!bytes.Equal(left.data, right.data) {
		return false
	}
	if left.info == nil || right.info == nil {
		return left.info == nil && right.info == nil
	}
	return os.SameFile(left.info, right.info)
}

func normalizeIndexBatchTargetSnapshot(
	entry *indexBatchRecoveryEntry,
	current indexBatchTargetSnapshot,
) indexBatchTargetSnapshot {
	if entry == nil ||
		entry.runtimeEvidenceErr == nil ||
		current.mode != entry.target.mode ||
		!bytes.Equal(current.data, entry.target.data) {
		return current
	}
	if current.info == nil || entry.target.info == nil {
		if current.info == nil && entry.target.info == nil {
			current.relation = entry.target.relation
		}
		return current
	}
	if os.SameFile(current.info, entry.target.info) {
		current.relation = entry.target.relation
	}
	return current
}

func (prepared *preparedIndexBatchRecovery) scopeValidator(
	inFlight *indexBatchRecoveryEntry,
	transition *indexTransactionInventoryItem,
) func(publicationScopePhase) error {
	return func(phase publicationScopePhase) error {
		if inFlight != nil && inFlight.anchorSource != nil {
			if err := prepared.revalidateRecoveryOriginalSource(
				inFlight,
				inFlight.anchorSource,
			); err != nil {
				return err
			}
		}
		for _, item := range prepared.inventory {
			if item == nil {
				continue
			}
			if err := verifyIndexDestinationParent(item.destination); err != nil {
				return detachedIndexRecoveryError(item.destination, err)
			}
			if _, removed := prepared.removed[item.discovery.relative]; removed {
				continue
			}
			current := observeIndexTransactionArtifact(
				item.destination.parent,
				item.destination.output.relative,
				item.discovery.directory,
				item.discovery.name,
				item.discovery.protocolName,
				item.discovery.kind,
			)
			if transition == item &&
				(phase == publicationScopeStable ||
					phase == publicationScopeRemove ||
					phase == publicationScopeInstall) &&
				current.info == nil {
				continue
			}
			if err := compareIndexRecoveryObservations(
				item.destination.output.relative,
				singleIndexRecoveryObservation(item.observation),
				singleIndexRecoveryObservation(current),
			); err != nil {
				if inFlight != nil &&
					(phase == publicationScopeStable ||
						phase == publicationScopeCreate ||
						phase == publicationScopeInstall) &&
					item != inFlight.anchor &&
					item != inFlight.anchorSource &&
					prepared.postAnchorSoftEligibilityError(
						inFlight,
						item,
						current,
					) == nil {
					item.observation = current
					item.discovery.observation = current
					inFlight.runtimeEvidenceErr = errors.Join(
						inFlight.runtimeEvidenceErr,
						err,
					)
					return indexBatchRecoveryEvidenceConflict(inFlight)
				}
				return err
			}
		}
		for _, entry := range prepared.entries {
			current, err := observeIndexBatchTarget(
				entry.target.item,
				entry.contract,
				entry.stage,
				entry.backup,
				entry.claim,
				entry.anchor,
				entry.witness,
			)
			if err != nil {
				return err
			}
			current = normalizeIndexBatchTargetSnapshot(entry, current)
			if entry == inFlight {
				switch phase {
				case publicationScopeRemove, publicationScopeVacate:
					if current.relation == indexBatchTargetNew ||
						current.info == nil {
						continue
					}
				case publicationScopeInstall:
					if current.info == nil {
						continue
					}
					if current.relation == indexBatchTargetOld &&
						entry.anchor != nil &&
						current.info != nil &&
						entry.anchor.observation.info != nil &&
						os.SameFile(current.info, entry.anchor.observation.info) {
						continue
					}
				}
			}
			if !sameIndexBatchTargetSnapshot(current, entry.target) {
				return unrecoverableIndexRecoveryError{
					destination: entry.contract.Path,
					artifact:    entry.contract.Stage,
					reason:      "batch target changed at publication barrier",
				}
			}
		}
		if len(prepared.terminalCommit) != 0 {
			return prepared.verifyIndexBatchTerminalCommitTargets()
		}
		return nil
	}
}

func (prepared *preparedIndexBatchRecovery) removeArtifact(
	item *indexTransactionInventoryItem,
) error {
	if item == nil {
		return nil
	}
	if _, removed := prepared.removed[item.discovery.relative]; removed {
		return nil
	}
	if err := removeDiscoveredIndexTransientArtifact(
		item.destination,
		item.discovery,
		item.observation,
		prepared.hooks,
		prepared.scopeValidator(nil, item),
	); err != nil {
		return err
	}
	prepared.removed[item.discovery.relative] = struct{}{}
	return prepared.revalidate()
}

func (prepared *preparedIndexBatchRecovery) revalidate() error {
	if len(prepared.inventory) == 0 {
		return nil
	}
	for _, item := range prepared.inventory {
		if _, removed := prepared.removed[item.discovery.relative]; removed {
			continue
		}
		if err := verifyIndexDestinationParent(item.destination); err != nil {
			return err
		}
		current := observeIndexTransactionArtifact(
			item.destination.parent,
			item.destination.output.relative,
			item.discovery.directory,
			item.discovery.name,
			item.discovery.protocolName,
			item.discovery.kind,
		)
		if err := compareIndexRecoveryObservations(
			item.destination.output.relative,
			singleIndexRecoveryObservation(item.observation),
			singleIndexRecoveryObservation(current),
		); err != nil {
			return err
		}
	}
	if prepared.sharedInventory {
		expected := make([]publicationNamespaceDiscovery, 0, len(prepared.inventory))
		for _, item := range prepared.inventory {
			if _, removed := prepared.removed[item.discovery.relative]; removed {
				continue
			}
			if item.observation.info == nil {
				continue
			}
			namespace := "index-v1"
			kind := string(item.discovery.kind)
			if hasASCIIFoldPrefix(item.discovery.name, publicationPrivateClaimPrefix) {
				namespace = "private-v2"
				kind = "claim"
			}
			expected = append(expected, publicationNamespaceDiscovery{
				namespace:    namespace,
				kind:         kind,
				directory:    item.discovery.directory,
				name:         item.discovery.name,
				protocolName: item.discovery.name,
				relative:     item.discovery.relative,
				parentInfo:   item.destination.parentInfo,
				info:         item.observation.info,
			})
		}
		sort.SliceStable(expected, func(left, right int) bool {
			return expected[left].relative < expected[right].relative
		})
		return revalidatePublicationNamespaceSnapshot(
			context.Background(),
			prepared.inventory[0].destination.root,
			[]publicationNamespaceRule{indexNamespaceRule(), privatePublicationNamespaceRule()},
			expected,
		)
	}
	current, err := discoverIndexTransactionNamespace(prepared.inventory[0].destination.root)
	if err != nil {
		return err
	}
	return compareIndexTransactionInventoryToDiscovery(
		prepared.inventory,
		current,
		prepared.removed,
	)
}

func (prepared *preparedIndexBatchRecovery) reconcileSharedRecoverySnapshot(
	snapshot []publicationNamespaceDiscovery,
) []publicationNamespaceDiscovery {
	soft := make(map[string]*indexTransactionInventoryItem)
	for _, entry := range prepared.entries {
		if entry == nil || entry.runtimeEvidenceErr == nil {
			continue
		}
		for _, item := range []*indexTransactionInventoryItem{
			entry.stage,
			entry.backup,
			entry.claim,
			entry.witness,
		} {
			if item != nil {
				soft[item.discovery.relative] = item
			}
		}
	}
	if len(soft) == 0 {
		return snapshot
	}
	expected := make([]publicationNamespaceDiscovery, 0, len(snapshot))
	for _, discovery := range snapshot {
		item, allowed := soft[discovery.relative]
		if !allowed {
			expected = append(expected, discovery)
			continue
		}
		if item.observation.info == nil {
			continue
		}
		discovery.parentInfo = item.destination.parentInfo
		discovery.info = item.observation.info
		expected = append(expected, discovery)
	}
	return expected
}

func indexBatchProtocolRelative(item *indexTransactionInventoryItem) string {
	if item == nil {
		return ""
	}
	return pathJoin(item.discovery.directory, item.discovery.protocolName)
}

func indexBatchArtifactDirectory(relative string) string {
	directory := path.Dir(relative)
	if directory == "." {
		return ""
	}
	return directory
}
