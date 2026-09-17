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
	"sort"
)

const (
	indexBatchManifestProtocol = "okf:index-batch"
	indexBatchManifestVersion  = 3
	indexBatchManifestMode     = fs.FileMode(0o600)
)

type indexBatchManifest struct {
	Protocol string                    `json:"protocol"`
	Version  int                       `json:"version"`
	Entries  []indexBatchManifestEntry `json:"entries"`
}

type indexBatchManifestEntry struct {
	Path           string `json:"path"`
	OldPresent     bool   `json:"old_present"`
	OldDigest      string `json:"old_digest,omitempty"`
	OldMode        uint32 `json:"old_mode,omitempty"`
	NewDigest      string `json:"new_digest"`
	NewMode        uint32 `json:"new_mode"`
	Stage          string `json:"stage"`
	Backup         string `json:"backup,omitempty"`
	Claim          string `json:"claim,omitempty"`
	Anchor         string `json:"anchor,omitempty"`
	Witness        string `json:"witness,omitempty"`
	NewInstall     string `json:"new_install"`
	RestoreInstall string `json:"restore_install"`
	Discard        string `json:"discard"`
}

type indexBatchPublication struct {
	manifest     indexBatchManifest
	data         []byte
	name         string
	spec         publicationFileSpec
	root         *indexDestination
	destinations []*indexDestination
	complete     bool
	manifestLive bool
}

func orderIndexBatchDestinations(
	destinations []*indexDestination,
) ([]*indexDestination, *indexDestination, error) {
	ordered := append([]*indexDestination(nil), destinations...)
	sort.SliceStable(ordered, func(left, right int) bool {
		leftRoot := cleanIndexDestinationPath(ordered[left].output.relative) == indexFilename
		rightRoot := cleanIndexDestinationPath(ordered[right].output.relative) == indexFilename
		if leftRoot != rightRoot {
			return !leftRoot
		}
		return ordered[left].output.relative < ordered[right].output.relative
	})
	var root *indexDestination
	for _, destination := range ordered {
		if cleanIndexDestinationPath(destination.output.relative) != indexFilename {
			continue
		}
		if root != nil {
			return ordered, nil, invalidIndexDestination(indexFilename, "batch contains multiple root outputs")
		}
		root = destination
	}
	if root == nil {
		return ordered, nil, invalidIndexDestination(indexFilename, "batch must contain exactly one root output")
	}
	return ordered, root, nil
}

func prepareIndexBatchArtifacts(
	destinations []*indexDestination,
	hooks indexPublishHooks,
) error {
	for _, destination := range destinations {
		if err := stageIndexDestination(destination, hooks); err != nil {
			return err
		}
	}
	for _, destination := range destinations {
		if err := prepareIndexBatchV3InstallArtifacts(destination, hooks); err != nil {
			return fmt.Errorf(
				"prepare index batch %q v3 install evidence: %w",
				destination.output.relative,
				err,
			)
		}
	}
	for _, destination := range destinations {
		if !destination.existed {
			continue
		}
		if err := createIndexRecoveryBackup(destination, hooks); err != nil {
			return err
		}
		if hooks.afterBackupVerified != nil {
			if err := hooks.afterBackupVerified(
				destination.output.relative,
				destination.parent,
				destination.backup,
			); err != nil {
				return err
			}
		}
		if err := verifyOriginalIndexDestination(destination); err != nil {
			return err
		}
		if err := createIndexIdentityWitness(destination, hooks); err != nil {
			return err
		}
		if err := verifyOriginalIndexDestination(destination); err != nil {
			return err
		}
		claim, err := prepareIndexVacateClaimName(destination, hooks)
		if err != nil {
			return fmt.Errorf(
				"prepare index batch %q ownership claim: %w",
				destination.output.relative,
				err,
			)
		}
		destination.vacateClaim = claim
		anchor, err := selectIndexRollbackAnchorName(destination)
		if err != nil {
			return fmt.Errorf(
				"prepare index batch %q rollback anchor name: %w",
				destination.output.relative,
				err,
			)
		}
		destination.anchor = anchor
	}
	return nil
}

func selectIndexRollbackAnchorName(destination *indexDestination) (string, error) {
	template, err := newIndexArtifactNameTemplate(
		indexArtifactAnchor,
		destination.output.relative,
		indexPreservedMode(destination.original.Mode()),
		destination.originalData,
	)
	if err != nil {
		return "", err
	}
	for slot := 0; slot < 10_000; slot++ {
		name := template.Name(slot)
		if _, err := destination.parent.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			// This is only canonical name selection. The later rollback
			// acquires the manifest-bound path atomically with no replacement.
			return name, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("transaction artifact slots exhausted")
}

func createIndexIdentityWitness(
	destination *indexDestination,
	hooks indexPublishHooks,
) error {
	template, err := newIndexArtifactNameTemplate(
		indexArtifactWitness,
		destination.output.relative,
		indexPreservedMode(destination.original.Mode()),
		destination.originalData,
	)
	if err != nil {
		return err
	}
	source := publicationSpecFromBytes(destination.original, destination.originalData)
	for slot := 0; slot < 10_000; slot++ {
		name := template.Name(slot)
		if _, statErr := destination.parent.Lstat(name); statErr == nil {
			continue
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return statErr
		}
		spec, witnessErr := createDurablePublicationWitness(
			context.Background(),
			destination.parent,
			destination.leaf,
			source,
			name,
			indexArtifactCoreHooks(
				destination,
				indexArtifactWitness,
				name,
				hooks,
			),
		)
		if witnessErr == nil {
			destination.witness = name
			destination.witnessInfo = spec.info
			destination.witnessComplete = spec.complete
			return verifyIndexIdentityWitness(destination)
		}
		if spec.created {
			destination.witness = name
			destination.witnessInfo = spec.info
			destination.witnessComplete = spec.complete
			return indexPublicationStepError{
				destination: destination.output.relative,
				action:      "create durable original identity witness",
				err:         witnessErr,
			}
		}
		return indexPublicationStepError{
			destination: destination.output.relative,
			action:      "create durable original identity witness",
			err:         witnessErr,
		}
	}
	return errors.New("index identity witness slots exhausted")
}

func verifyIndexIdentityWitness(destination *indexDestination) error {
	if destination.witness == "" ||
		destination.witnessInfo == nil ||
		!destination.witnessComplete {
		return invalidIndexDestination(
			destination.output.relative,
			"original identity witness is incomplete",
		)
	}
	if err := verifyCreatedIndexArtifactForCleanup(
		destination,
		destination.witness,
		destination.witnessInfo,
		true,
		destination.originalData,
		indexPreservedMode(destination.original.Mode()),
	); err != nil {
		return err
	}
	if !os.SameFile(destination.original, destination.witnessInfo) {
		return invalidIndexDestination(
			destination.output.relative,
			"original identity witness does not retain the original inode",
		)
	}
	if destination.backupInfo != nil &&
		os.SameFile(destination.backupInfo, destination.witnessInfo) {
		return invalidIndexDestination(
			destination.output.relative,
			"independent backup aliases the original identity witness",
		)
	}
	return nil
}

func newIndexBatchManifest(
	destinations []*indexDestination,
) (indexBatchManifest, []byte, string, error) {
	manifest := indexBatchManifest{
		Protocol: indexBatchManifestProtocol,
		Version:  indexBatchManifestVersion,
		Entries:  make([]indexBatchManifestEntry, 0, len(destinations)),
	}
	for _, destination := range destinations {
		if destination.stage == "" || destination.stageInfo == nil || !destination.stageComplete {
			return indexBatchManifest{}, nil, "", invalidIndexDestination(
				destination.output.relative,
				"durable stage is unavailable before batch manifest",
			)
		}
		entry := indexBatchManifestEntry{
			Path:           cleanIndexDestinationPath(destination.output.relative),
			NewDigest:      indexBatchPayloadDigest(destination.output.data),
			NewMode:        uint32(indexPreservedMode(destination.stageInfo.Mode())),
			Stage:          pathJoin(destination.directory, destination.stage),
			NewInstall:     pathJoin(destination.directory, destination.newInstall),
			RestoreInstall: pathJoin(destination.directory, destination.restoreInstall),
			Discard:        pathJoin(destination.directory, destination.discard),
		}
		if destination.existed {
			if destination.backup == "" ||
				destination.backupInfo == nil ||
				!destination.backupComplete ||
				destination.vacateClaim == "" ||
				destination.anchor == "" ||
				destination.witness == "" ||
				destination.witnessInfo == nil ||
				!destination.witnessComplete {
				return indexBatchManifest{}, nil, "", invalidIndexDestination(
					destination.output.relative,
					"durable original evidence is unavailable before batch manifest",
				)
			}
			entry.OldPresent = true
			entry.OldDigest = indexBatchPayloadDigest(destination.originalData)
			entry.OldMode = uint32(indexPreservedMode(destination.original.Mode()))
			entry.Backup = pathJoin(destination.directory, destination.backup)
			entry.Claim = pathJoin(destination.directory, destination.vacateClaim)
			entry.Anchor = pathJoin(destination.directory, destination.anchor)
			entry.Witness = pathJoin(destination.directory, destination.witness)
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	sort.SliceStable(manifest.Entries, func(left, right int) bool {
		return indexBatchEntryLess(manifest.Entries[left].Path, manifest.Entries[right].Path)
	})
	if err := validateIndexBatchManifestShape(manifest); err != nil {
		return indexBatchManifest{}, nil, "", err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return indexBatchManifest{}, nil, "", err
	}
	data = append(data, '\n')
	return manifest, data, indexBatchManifestName(data), nil
}

func createDurableIndexBatchManifest(
	destinations []*indexDestination,
	root *indexDestination,
	hooks indexPublishHooks,
) (*indexBatchPublication, error) {
	manifest, data, name, err := newIndexBatchManifest(destinations)
	if err != nil {
		return nil, err
	}
	spec, createErr := createPublicationArtifact(
		context.Background(),
		root.parent,
		name,
		indexBatchManifestMode,
		data,
		indexArtifactCoreHooks(root, indexArtifactManifest, name, hooks),
	)
	batch := &indexBatchPublication{
		manifest:     manifest,
		data:         data,
		name:         name,
		spec:         spec,
		root:         root,
		destinations: append([]*indexDestination(nil), destinations...),
		complete:     spec.complete && createErr == nil,
		manifestLive: spec.created,
	}
	if createErr != nil {
		return batch, fmt.Errorf("create durable index batch manifest: %w", createErr)
	}
	if hooks.afterBatchManifestDurable != nil {
		if err := hooks.afterBatchManifestDurable(root.root, name); err != nil {
			return batch, err
		}
	}
	if err := revalidateIndexBatchBeforeMutation(destinations, batch); err != nil {
		return batch, err
	}
	return batch, nil
}

func revalidateIndexBatchBeforeMutation(
	destinations []*indexDestination,
	batch *indexBatchPublication,
) error {
	if batch == nil || batch.root == nil || !batch.manifestLive || !batch.complete {
		return errors.New("index batch manifest is not durable")
	}
	if err := verifyCreatedIndexArtifactForCleanup(
		batch.root,
		batch.name,
		batch.spec.info,
		true,
		batch.data,
		indexBatchManifestMode,
	); err != nil {
		return fmt.Errorf("index batch manifest changed before publication: %w", err)
	}
	expected := make(map[string]struct{}, 1+len(destinations)*3)
	expected[batch.name] = struct{}{}
	for _, destination := range destinations {
		if err := verifyIndexDestinationParent(destination); err != nil {
			return err
		}
		if err := verifyStagedIndexDestination(destination); err != nil {
			return err
		}
		expected[pathJoin(destination.directory, destination.stage)] = struct{}{}
		if err := verifyIndexNewInstall(destination); err != nil {
			return err
		}
		expected[pathJoin(destination.directory, destination.newInstall)] = struct{}{}
		if destination.existed {
			if err := verifyIndexBackup(destination); err != nil {
				return err
			}
			if err := verifyIndexIdentityWitness(destination); err != nil {
				return err
			}
			if err := verifyOriginalIndexDestination(destination); err != nil {
				return err
			}
			if _, err := destination.parent.Lstat(destination.vacateClaim); !errors.Is(err, fs.ErrNotExist) {
				return invalidIndexDestination(
					destination.output.relative,
					"ownership claim appeared before first batch mutation",
				)
			}
			if _, err := destination.parent.Lstat(destination.anchor); !errors.Is(err, fs.ErrNotExist) {
				return invalidIndexDestination(
					destination.output.relative,
					"rollback anchor appeared before first batch mutation",
				)
			}
			expected[pathJoin(destination.directory, destination.backup)] = struct{}{}
			expected[pathJoin(destination.directory, destination.witness)] = struct{}{}
		} else if err := verifyOriginalIndexDestination(destination); err != nil {
			return err
		}
	}
	discoveries, err := discoverIndexTransactionNamespace(batch.root.root)
	if err != nil {
		return err
	}
	if len(discoveries) != len(expected) {
		return errors.New("index transaction inventory changed before first batch mutation")
	}
	for _, discovery := range discoveries {
		if _, ok := expected[discovery.relative]; !ok || discovery.observation.reason != "" {
			return unrecoverableIndexRecoveryError{
				destination: pathJoin(discovery.directory, indexFilename),
				artifact:    discovery.relative,
				reason:      "index transaction inventory does not match the durable batch manifest",
			}
		}
	}
	return nil
}

func validateIndexBatchPublicationPrefix(
	destinations []*indexDestination,
	publishedCount int,
) error {
	if publishedCount < 0 || publishedCount > len(destinations) {
		return errors.New("invalid index batch publication prefix")
	}
	for index, destination := range destinations {
		if err := verifyIndexDestinationParent(destination); err != nil {
			return err
		}
		if err := verifyStagedIndexDestination(destination); err != nil {
			return err
		}
		if destination.existed {
			if err := verifyIndexBackup(destination); err != nil {
				return err
			}
			if err := verifyIndexIdentityWitness(destination); err != nil {
				return err
			}
			if _, err := destination.parent.Lstat(destination.anchor); !errors.Is(err, fs.ErrNotExist) {
				return invalidIndexDestination(
					destination.output.relative,
					"rollback anchor exists before rollback",
				)
			}
		}
		if index < publishedCount {
			if err := verifyInstalledIndexDestination(destination); err != nil {
				return err
			}
			if destination.existed {
				if err := verifyIndexBatchOriginalClaim(destination); err != nil {
					return err
				}
			}
			continue
		}
		if err := verifyOriginalIndexDestination(destination); err != nil {
			return err
		}
		if destination.existed {
			if _, err := destination.parent.Lstat(destination.vacateClaim); !errors.Is(err, fs.ErrNotExist) {
				return invalidIndexDestination(
					destination.output.relative,
					"ownership claim exists outside the published prefix",
				)
			}
		}
	}
	return nil
}

func rollbackIndexBatchDestinations(
	destinations []*indexDestination,
	hooks indexPublishHooks,
) error {
	for index := len(destinations) - 1; index >= 0; index-- {
		if err := rollbackIndexBatchDestination(destinations[index], hooks); err != nil {
			// Continuing after a partial target action could create a second
			// in-flight rollback boundary. Stop at the first failure so the
			// immutable manifest still describes one canonical progress state.
			return err
		}
	}
	for _, destination := range destinations {
		if err := verifyRolledBackIndexDestination(destination); err != nil {
			return err
		}
		if err := verifyIndexBatchV3RollbackProof(destination); err != nil {
			return err
		}
	}
	return nil
}

func verifyIndexBatchV3RollbackProof(destination *indexDestination) error {
	if destination.discardInfo == nil || !destination.discardComplete {
		return invalidIndexDestination(
			destination.output.relative,
			"completed rollback has no exact discard token",
		)
	}
	if destination.stageInfo == nil ||
		!os.SameFile(destination.stageInfo, destination.discardInfo) {
		return invalidIndexDestination(
			destination.output.relative,
			"completed rollback discard does not alias retained stage",
		)
	}
	if err := verifyCreatedIndexArtifactForCleanup(
		destination,
		destination.discard,
		destination.discardInfo,
		true,
		destination.output.data,
		indexPreservedMode(destination.discardInfo.Mode()),
	); err != nil {
		return err
	}
	if !destination.existed {
		return nil
	}
	if err := verifyIndexBatchRollbackAnchor(destination); err != nil {
		return err
	}
	if !destination.restoreInstallConsumed {
		return invalidIndexDestination(
			destination.output.relative,
			"completed rollback did not consume restore install token",
		)
	}
	if _, err := destination.parent.Lstat(destination.restoreInstall); !errors.Is(err, fs.ErrNotExist) {
		return invalidIndexDestination(
			destination.output.relative,
			"completed rollback restore install token is still present",
		)
	}
	return verifyRestoredIndexDestination(destination, destination.anchorInfo)
}

func captureIndexBatchRollbackBaseline(
	scope *indexBatchTransactionScope,
	prefix []*indexDestination,
) error {
	if scope == nil {
		return publicationConflict("index rollback transaction scope is unavailable", nil)
	}
	if scope.rollbackPeers != nil {
		return nil
	}
	mutable := make(map[*indexDestination]struct{}, len(prefix))
	for _, destination := range prefix {
		mutable[destination] = struct{}{}
	}
	for _, destination := range scope.destinations {
		if destination.published || destination.restoreStage != "" {
			mutable[destination] = struct{}{}
		}
	}
	scope.rollbackPeers = make(map[*indexDestination]struct{})
	scope.rollbackFrozenTargets = make(map[*indexDestination]indexRollbackFrozenPath)
	scope.rollbackFrozenPaths = make(map[string]indexRollbackFrozenPath)
	scope.rollbackFrozenManifest = nil
	scope.rollbackBaselineErr = nil

	for _, destination := range scope.destinations {
		if err := verifyIndexDestinationParent(destination); err != nil {
			return err
		}
		if _, isMutable := mutable[destination]; isMutable {
			if destination.published || destination.restoreStage == "" ||
				verifyIndexTransactionTargetState(
					destination,
					publicationScopeStable,
					false,
				) == nil {
				continue
			}
			frozen, err := captureIndexRollbackFrozenPath(
				destination.parent,
				destination.leaf,
			)
			if err != nil {
				return invalidIndexDestination(
					destination.output.relative,
					"foreign rollback blocker could not be captured",
				)
			}
			scope.rollbackFrozenTargets[destination] = frozen
			continue
		}

		scope.rollbackPeers[destination] = struct{}{}
		target, err := captureIndexRollbackFrozenPath(
			destination.parent,
			destination.leaf,
		)
		if err != nil {
			return invalidIndexDestination(
				destination.output.relative,
				"non-owned rollback peer target could not be captured",
			)
		}
		if verifyIndexTransactionTargetState(
			destination,
			publicationScopeStable,
			false,
		) != nil {
			scope.rollbackBaselineErr = errors.Join(
				scope.rollbackBaselineErr,
				publicationConflict(
					"non-owned index rollback peer target changed before rollback entry",
					nil,
				),
			)
		}
		scope.rollbackFrozenTargets[destination] = target

		for _, artifact := range indexRollbackPeerArtifacts(destination) {
			if artifact.name == "" {
				continue
			}
			frozen, err := captureIndexRollbackFrozenPath(
				destination.parent,
				artifact.name,
			)
			if err != nil {
				return invalidIndexDestination(
					destination.output.relative,
					"non-owned rollback peer evidence could not be captured",
				)
			}
			relative := pathJoin(destination.directory, artifact.name)
			scope.rollbackFrozenPaths[relative] = frozen
			if !indexRollbackFrozenPathMatchesExpected(
				frozen,
				artifact.info,
				artifact.complete,
				artifact.data,
				artifact.mode,
			) {
				scope.rollbackBaselineErr = errors.Join(
					scope.rollbackBaselineErr,
					publicationConflict(
						"non-owned index rollback peer evidence changed before rollback entry",
						nil,
					),
				)
			}
		}
	}

	if batch := scope.batch; batch != nil && batch.manifestLive {
		frozen, err := captureIndexRollbackFrozenPath(batch.root.parent, batch.name)
		if err != nil {
			return publicationConflict(
				"index batch manifest rollback baseline could not be captured",
				err,
			)
		}
		scope.rollbackFrozenManifest = &frozen
		if !indexRollbackFrozenPathMatchesExpected(
			frozen,
			batch.spec.info,
			batch.complete,
			batch.data,
			indexBatchManifestMode,
		) {
			scope.rollbackBaselineErr = errors.Join(
				scope.rollbackBaselineErr,
				publicationConflict(
					"index batch manifest changed before rollback entry",
					nil,
				),
			)
		}
	}
	return nil
}

func captureIndexRollbackFrozenPath(
	parent *os.Root,
	name string,
) (indexRollbackFrozenPath, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return indexRollbackFrozenPath{}, nil
	}
	if err != nil {
		return indexRollbackFrozenPath{}, err
	}
	frozen := indexRollbackFrozenPath{
		present: true,
		info:    info,
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return frozen, nil
	}
	spec, err := capturePublicationFile(context.Background(), parent, name)
	if err != nil {
		return indexRollbackFrozenPath{}, err
	}
	frozen.info = spec.info
	frozen.regular = &spec
	return frozen, nil
}

func verifyIndexRollbackFrozenPath(
	parent *os.Root,
	name string,
	frozen indexRollbackFrozenPath,
) error {
	if !frozen.present {
		if _, err := parent.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else {
			return publicationConflict("frozen rollback path appeared", err)
		}
	}
	if frozen.regular != nil {
		return verifyPublicationFile(
			context.Background(),
			parent,
			name,
			*frozen.regular,
			true,
		)
	}
	current, err := parent.Lstat(name)
	if err != nil || !samePublicationEntryObservation(frozen.info, current) {
		return publicationConflict("frozen rollback path observation changed", err)
	}
	return nil
}

func indexRollbackFrozenPathMatchesExpected(
	frozen indexRollbackFrozenPath,
	info os.FileInfo,
	complete bool,
	data []byte,
	mode fs.FileMode,
) bool {
	if info == nil || !complete {
		return !frozen.present
	}
	if frozen.regular == nil ||
		frozen.regular.info == nil ||
		!os.SameFile(info, frozen.regular.info) ||
		frozen.regular.mode != indexPreservedMode(mode) ||
		!bytes.Equal(frozen.regular.data, data) {
		return false
	}
	return true
}

func indexRollbackPeerArtifacts(
	destination *indexDestination,
) []struct {
	name     string
	info     os.FileInfo
	complete bool
	data     []byte
	mode     fs.FileMode
} {
	originalMode := fs.FileMode(0)
	if destination.original != nil {
		originalMode = destination.original.Mode()
	}
	rollbackData := destination.output.data
	if destination.rollbackKind == indexArtifactRestore {
		rollbackData = destination.originalData
	}
	rollbackMode := fs.FileMode(0)
	if destination.rollbackInfo != nil {
		rollbackMode = destination.rollbackInfo.Mode()
	}
	artifacts := make([]struct {
		name     string
		info     os.FileInfo
		complete bool
		data     []byte
		mode     fs.FileMode
	}, 0, 10)
	seen := make(map[string]struct{}, 10)
	add := func(
		name string,
		info os.FileInfo,
		complete bool,
		data []byte,
		mode fs.FileMode,
	) {
		if name == "" {
			return
		}
		if _, present := seen[name]; present {
			return
		}
		seen[name] = struct{}{}
		artifacts = append(artifacts, struct {
			name     string
			info     os.FileInfo
			complete bool
			data     []byte
			mode     fs.FileMode
		}{name, info, complete, data, mode})
	}

	add(
		destination.stage,
		destination.stageInfo,
		destination.stageComplete,
		destination.output.data,
		destination.stageInfoMode(),
	)
	if !destination.newInstallConsumed {
		add(
			destination.newInstall,
			destination.newInstallInfo,
			destination.newInstallComplete,
			destination.output.data,
			destination.stageInfoMode(),
		)
	}
	if !destination.restoreInstallConsumed {
		add(
			destination.restoreInstall,
			destination.restoreInstallInfo,
			destination.restoreInstallComplete,
			destination.originalData,
			originalMode,
		)
	}
	add(
		destination.discard,
		destination.discardInfo,
		destination.discardComplete,
		destination.output.data,
		destination.stageInfoMode(),
	)
	claimInfo := os.FileInfo(nil)
	claimComplete := false
	claimData := destination.originalData
	claimMode := originalMode
	switch {
	case destination.restoreStage == destination.vacateClaim:
		claimInfo = destination.restoreInfo
		claimComplete = destination.restoreComplete
	case destination.rollbackClaim == destination.vacateClaim:
		claimInfo = destination.rollbackInfo
		claimComplete = destination.rollbackComplete
		claimData = rollbackData
		claimMode = rollbackMode
	}
	add(
		destination.vacateClaim,
		claimInfo,
		claimComplete,
		claimData,
		claimMode,
	)
	add(
		destination.restoreStage,
		destination.restoreInfo,
		destination.restoreComplete,
		destination.originalData,
		originalMode,
	)
	add(
		destination.backup,
		destination.backupInfo,
		destination.backupComplete,
		destination.originalData,
		originalMode,
	)
	add(
		destination.witness,
		destination.witnessInfo,
		destination.witnessComplete,
		destination.originalData,
		originalMode,
	)
	add(
		destination.anchor,
		destination.anchorInfo,
		destination.anchorComplete,
		destination.originalData,
		originalMode,
	)
	add(
		destination.rollbackClaim,
		destination.rollbackInfo,
		destination.rollbackComplete,
		rollbackData,
		rollbackMode,
	)
	return artifacts
}

func verifyRolledBackIndexDestination(destination *indexDestination) error {
	if destination.existed && destination.anchorInfo != nil && destination.anchorComplete {
		return verifyRestoredIndexDestination(destination, destination.anchorInfo)
	}
	return verifyOriginalIndexDestination(destination)
}

func hasExactIndexBatchRollbackTarget(destination *indexDestination) bool {
	return destination.existed &&
		destination.anchorInfo != nil &&
		destination.anchorComplete &&
		verifyRestoredIndexDestination(destination, destination.anchorInfo) == nil
}

func rollbackIndexBatchDestination(
	destination *indexDestination,
	hooks indexPublishHooks,
) error {
	if err := verifyIndexDestinationParent(destination); err != nil {
		return err
	}
	needsAnchor := destination.existed &&
		(destination.published || destination.restoreStage != "")
	var evidenceErr error
	if needsAnchor {
		var err error
		evidenceErr, err = ensureIndexBatchRollbackAnchor(destination, hooks)
		if err != nil {
			return err
		}
		if err := ensureIndexBatchRestoreInstall(destination, hooks); err != nil {
			return err
		}
	}
	if hasExactIndexBatchRollbackTarget(destination) {
		destination.rollbackTargetInfo = destination.anchorInfo
		evidenceErr = errors.Join(evidenceErr, verifyIndexBatchSoftEvidence(destination))
		if evidenceErr != nil {
			destination.preserveBackup = true
			return recoverableIndexBackupError{
				destination: destination.output.relative,
				backup:      pathJoin(destination.directory, destination.anchor),
				reason:      "original target is already restored from the exact independent rollback anchor; changed source evidence preserved",
				err:         evidenceErr,
			}
		}
		return nil
	}
	if destination.existed {
		expectPublished := destination.published
		if !expectPublished {
			if err := verifyOriginalIndexDestination(destination); err == nil {
				return nil
			}
		}
		observedEvidenceErr, err := verifyIndexBatchRollbackBoundary(
			destination,
			expectPublished,
		)
		evidenceErr = errors.Join(evidenceErr, observedEvidenceErr)
		if err != nil {
			return err
		}
		if hooks.beforeRestore != nil {
			barrierHooks := publicationBarrierHooks{
				scopePhase: publicationScopeStable,
				validateScope: indexBatchRollbackScopeValidator(
					destination,
					expectPublished,
					&evidenceErr,
				),
			}
			if err := runPublicationBarrier(barrierHooks, func() error {
				return hooks.beforeRestore(
					destination.output.relative,
					destination.parent,
					destination.leaf,
					destination.backup,
				)
			}); err != nil {
				return err
			}
		}
		observedEvidenceErr, err = verifyIndexBatchRollbackBoundary(
			destination,
			expectPublished,
		)
		evidenceErr = errors.Join(evidenceErr, observedEvidenceErr)
		if err != nil {
			return err
		}
	}
	if destination.published {
		if err := vacateIndexBatchPublishedToDiscard(
			destination,
			hooks,
			&evidenceErr,
		); err != nil {
			return errors.Join(
				err,
				invalidIndexDestination(
					destination.output.relative,
					"exact staged target could not be retained in discard during batch rollback",
				),
			)
		}
	}
	if !destination.existed {
		if err := verifyIndexDestinationParent(destination); err != nil {
			return detachedIndexRecoveryError(destination, err)
		}
		if _, err := destination.parent.Lstat(destination.leaf); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return invalidIndexDestination(
			destination.output.relative,
			"create-only target is occupied during batch rollback",
		)
	}
	observedEvidenceErr, err := verifyIndexBatchRollbackBoundary(destination, false)
	evidenceErr = errors.Join(evidenceErr, observedEvidenceErr)
	if err != nil {
		return err
	}
	if err := installIndexBatchRestoreToken(
		destination,
		hooks,
		&evidenceErr,
	); err != nil {
		return err
	}
	if evidenceErr != nil {
		destination.preserveBackup = true
		return recoverableIndexBackupError{
			destination: destination.output.relative,
			backup:      pathJoin(destination.directory, destination.anchor),
			reason:      "original restored from the exact independent rollback anchor; changed source evidence preserved",
			err:         evidenceErr,
		}
	}
	return nil
}

func verifyIndexBatchRollbackBoundary(
	destination *indexDestination,
	expectPublished bool,
) (evidenceErr, hardErr error) {
	if err := verifyIndexDestinationParent(destination); err != nil {
		return nil, detachedIndexRecoveryError(destination, err)
	}
	if err := verifyIndexBatchRollbackAnchor(destination); err != nil {
		return nil, err
	}
	if destination.transactionScope != nil {
		if frozen, present := destination.transactionScope.
			rollbackFrozenTargets[destination]; present {
			if err := verifyIndexRollbackFrozenPath(
				destination.parent,
				destination.leaf,
				frozen,
			); err != nil {
				return nil, invalidIndexDestination(
					destination.output.relative,
					"frozen rollback blocker changed",
				)
			}
		}
	}
	evidenceErr = verifyIndexBatchSoftEvidence(destination)
	if expectPublished {
		return evidenceErr, verifyInstalledIndexDestination(destination)
	}
	if _, err := destination.parent.Lstat(destination.leaf); !errors.Is(err, fs.ErrNotExist) {
		blockedErr := invalidIndexDestination(
			destination.output.relative,
			"original target restoration is blocked during batch rollback",
		)
		destination.preserveBackup = true
		return evidenceErr, recoverableIndexBackupError{
			destination: destination.output.relative,
			backup:      pathJoin(destination.directory, destination.anchor),
			reason:      "foreign public target left unchanged; exact original retained in the independent rollback anchor with complete batch evidence",
			err:         errors.Join(evidenceErr, blockedErr),
		}
	}
	return evidenceErr, nil
}

func ensureIndexBatchRollbackAnchor(
	destination *indexDestination,
	hooks indexPublishHooks,
) (evidenceErr, err error) {
	if destination.anchorInfo != nil || destination.anchorComplete {
		return nil, verifyIndexBatchRollbackAnchor(destination)
	}
	sourceName, sourceInfo, err := selectIndexBatchOriginalSource(destination)
	if err != nil {
		return nil, err
	}
	source := publicationSpecFromBytes(sourceInfo, destination.originalData)
	destination.anchorSource = sourceName
	spec, createErr := createDurablePublicationCopy(
		context.Background(),
		destination.parent,
		sourceName,
		source,
		destination.anchor,
		indexArtifactCoreHooks(
			destination,
			indexArtifactAnchor,
			destination.anchor,
			hooks,
		),
	)
	destination.anchorSource = ""
	destination.anchorInfo = spec.info
	destination.anchorComplete = spec.complete
	if parentErr := verifyIndexDestinationParent(destination); parentErr != nil {
		return nil, errors.Join(createErr, detachedIndexRecoveryError(destination, parentErr))
	}
	if !spec.created {
		return nil, errors.Join(
			createErr,
			invalidIndexDestination(
				destination.output.relative,
				"manifest-bound rollback anchor path could not be acquired",
			),
		)
	}
	if verifyErr := verifyIndexBatchRollbackAnchor(destination); verifyErr != nil {
		return nil, errors.Join(createErr, verifyErr)
	}
	if createErr != nil {
		if errors.Is(createErr, errPublicationConflict) {
			return createErr, nil
		}
		return nil, createErr
	}
	return nil, nil
}

func ensureIndexBatchRestoreInstall(
	destination *indexDestination,
	hooks indexPublishHooks,
) error {
	if destination.restoreInstallConsumed {
		if hasExactIndexBatchRollbackTarget(destination) {
			return nil
		}
		return invalidIndexDestination(
			destination.output.relative,
			"consumed restore install token has no exact restored target",
		)
	}
	if destination.restoreInstallInfo != nil || destination.restoreInstallComplete {
		return verifyIndexBatchRestoreInstall(destination)
	}
	if err := verifyIndexBatchRollbackAnchor(destination); err != nil {
		return err
	}
	spec, createErr := createDurablePublicationWitness(
		context.Background(),
		destination.parent,
		destination.anchor,
		publicationSpecFromBytes(destination.anchorInfo, destination.originalData),
		destination.restoreInstall,
		indexArtifactCoreHooks(
			destination,
			indexArtifactRestoreInstall,
			destination.restoreInstall,
			hooks,
		),
	)
	destination.restoreInstallInfo = spec.info
	destination.restoreInstallComplete = spec.complete
	if !spec.created {
		return errors.Join(
			createErr,
			invalidIndexDestination(
				destination.output.relative,
				"manifest-bound restore install path could not be acquired",
			),
		)
	}
	return errors.Join(createErr, verifyIndexBatchRestoreInstall(destination))
}

func verifyIndexBatchRestoreInstall(destination *indexDestination) error {
	if destination.restoreInstall == "" ||
		destination.restoreInstallInfo == nil ||
		!destination.restoreInstallComplete ||
		destination.anchorInfo == nil ||
		!os.SameFile(destination.anchorInfo, destination.restoreInstallInfo) {
		return invalidIndexDestination(
			destination.output.relative,
			"restore install token does not retain rollback anchor identity",
		)
	}
	if err := verifyCreatedIndexArtifactForCleanup(
		destination,
		destination.restoreInstall,
		destination.restoreInstallInfo,
		true,
		destination.originalData,
		indexPreservedMode(destination.original.Mode()),
	); err != nil {
		return err
	}
	for _, forbidden := range []os.FileInfo{
		destination.original,
		destination.backupInfo,
		destination.restoreInfo,
		destination.stageInfo,
		destination.witnessInfo,
	} {
		if forbidden != nil && os.SameFile(destination.restoreInstallInfo, forbidden) {
			return invalidIndexDestination(
				destination.output.relative,
				"restore install token aliases forbidden evidence",
			)
		}
	}
	return nil
}

func vacateIndexBatchPublishedToDiscard(
	destination *indexDestination,
	hooks indexPublishHooks,
	evidenceErr *error,
) error {
	if destination.discardInfo != nil || destination.discardComplete {
		return invalidIndexDestination(
			destination.output.relative,
			"discard token already exists before published target vacate",
		)
	}
	expected := publicationSpecFromBytes(
		destination.publishedInfo,
		destination.output.data,
	)
	barrierHooks := indexArtifactCoreHooks(
		destination,
		indexArtifactDiscard,
		destination.discard,
		hooks,
	)
	barrierHooks.validateScope = indexBatchRollbackScopeValidator(
		destination,
		true,
		evidenceErr,
	)
	barrierHooks.beforeVacate = func() error {
		if hooks.beforeVacate == nil {
			return nil
		}
		return hooks.beforeVacate(
			destination.output.relative,
			destination.parent,
			destination.leaf,
		)
	}
	barrierHooks.afterVacateRename = func() error {
		if hooks.afterVacateRename == nil {
			return nil
		}
		return hooks.afterVacateRename(
			destination.output.relative,
			destination.parent,
			destination.leaf,
			destination.discard,
		)
	}
	barrierHooks.afterVacate = func() error {
		if hooks.afterVacate == nil {
			return nil
		}
		return hooks.afterVacate(
			destination.output.relative,
			destination.parent,
			destination.leaf,
			destination.discard,
		)
	}
	claimed, vacateErr := guardedVacatePublicationLeafWithCompensation(
		context.Background(),
		destination.parent,
		destination.leaf,
		expected,
		destination.discard,
		barrierHooks,
	)
	if claimed.created {
		destination.discardInfo = claimed.info
		destination.discardComplete = claimed.complete
	}
	if vacateErr != nil {
		return vacateErr
	}
	if !claimed.created ||
		claimed.info == nil ||
		destination.stageInfo == nil ||
		!os.SameFile(destination.stageInfo, claimed.info) {
		return invalidIndexDestination(
			destination.output.relative,
			"discard token does not retain staged target identity",
		)
	}
	if err := verifyCreatedIndexArtifactForCleanup(
		destination,
		destination.discard,
		destination.discardInfo,
		destination.discardComplete,
		destination.output.data,
		indexPreservedMode(destination.stageInfo.Mode()),
	); err != nil {
		return err
	}
	destination.published = false
	return nil
}

func installIndexBatchRestoreToken(
	destination *indexDestination,
	hooks indexPublishHooks,
	evidenceErr *error,
) error {
	if err := verifyIndexBatchRestoreInstall(destination); err != nil {
		return err
	}
	installHooks := indexRestoreInstallCoreHooks(
		destination,
		indexArtifactRestoreInstall,
		destination.restoreInstall,
		hooks,
	)
	installHooks.validateScope = indexBatchRollbackScopeValidator(
		destination,
		false,
		evidenceErr,
	)
	installed, installErr := guardedConsumePublicationInstallLeaf(
		context.Background(),
		destination.parent,
		destination.restoreInstall,
		publicationSpecFromBytes(
			destination.restoreInstallInfo,
			destination.originalData,
		),
		destination.anchor,
		publicationSpecFromBytes(destination.anchorInfo, destination.originalData),
		destination.leaf,
		installHooks,
	)
	if installed {
		destination.restoreInstallConsumed = true
		destination.rollbackTargetInfo = destination.anchorInfo
	}
	if installErr != nil || !installed {
		return errors.Join(
			installErr,
			invalidIndexDestination(
				destination.output.relative,
				"manifest-bound restore install token could not restore original",
			),
		)
	}
	return verifyRestoredIndexDestination(destination, destination.anchorInfo)
}

func verifyIndexBatchRollbackAnchor(destination *indexDestination) error {
	if destination.anchor == "" ||
		destination.anchorInfo == nil ||
		!destination.anchorComplete {
		return invalidIndexDestination(
			destination.output.relative,
			"manifest-bound rollback anchor is incomplete",
		)
	}
	if err := verifyCreatedIndexArtifactForCleanup(
		destination,
		destination.anchor,
		destination.anchorInfo,
		true,
		destination.originalData,
		indexPreservedMode(destination.original.Mode()),
	); err != nil {
		return err
	}
	for _, other := range []os.FileInfo{
		destination.original,
		destination.backupInfo,
		destination.restoreInfo,
		destination.stageInfo,
		destination.witnessInfo,
	} {
		if other != nil && os.SameFile(destination.anchorInfo, other) {
			return invalidIndexDestination(
				destination.output.relative,
				"rollback anchor is not an independent inode",
			)
		}
	}
	return nil
}

func verifyIndexBatchOriginalEvidence(destination *indexDestination) error {
	var evidenceErr error
	if err := verifyIndexBackup(destination); err != nil {
		evidenceErr = errors.Join(evidenceErr, err)
	}
	if err := verifyIndexIdentityWitness(destination); err != nil {
		evidenceErr = errors.Join(evidenceErr, err)
	}
	if destination.restoreStage != "" {
		if err := verifyIndexBatchOriginalClaim(destination); err != nil {
			evidenceErr = errors.Join(evidenceErr, err)
		}
	}
	return evidenceErr
}

func verifyIndexBatchSoftEvidence(destination *indexDestination) error {
	evidenceErr := verifyIndexBatchOriginalEvidence(destination)
	if destination.stage != "" {
		if err := verifyCreatedIndexArtifactForCleanup(
			destination,
			destination.stage,
			destination.stageInfo,
			destination.stageComplete,
			destination.output.data,
			indexPreservedMode(destination.stageInfoMode()),
		); err != nil {
			evidenceErr = errors.Join(
				evidenceErr,
				indexCleanupArtifactError(
					destination,
					"stage",
					destination.stage,
					err,
				),
			)
		}
	}
	return evidenceErr
}

func selectIndexBatchOriginalSource(
	destination *indexDestination,
) (string, os.FileInfo, error) {
	mode := indexPreservedMode(destination.original.Mode())
	for _, candidate := range []struct {
		name     string
		info     os.FileInfo
		complete bool
	}{
		{
			name:     destination.restoreStage,
			info:     destination.restoreInfo,
			complete: destination.restoreComplete,
		},
		{
			name:     destination.backup,
			info:     destination.backupInfo,
			complete: destination.backupComplete,
		},
	} {
		if candidate.name == "" || !candidate.complete {
			continue
		}
		if err := verifyCreatedIndexArtifactForCleanup(
			destination,
			candidate.name,
			candidate.info,
			true,
			destination.originalData,
			mode,
		); err == nil {
			return candidate.name, candidate.info, nil
		}
	}
	return "", nil, invalidIndexDestination(
		destination.output.relative,
		"no exact manifest-bound original source is available during batch rollback",
	)
}

func verifyIndexBatchOriginalClaim(destination *indexDestination) error {
	if destination.restoreStage == "" ||
		destination.restoreStage != destination.vacateClaim ||
		destination.restoreInfo == nil ||
		!destination.restoreComplete {
		return invalidIndexDestination(
			destination.output.relative,
			"published target has no complete original ownership claim",
		)
	}
	if err := verifyCreatedIndexArtifactForCleanup(
		destination,
		destination.restoreStage,
		destination.restoreInfo,
		true,
		destination.originalData,
		indexPreservedMode(destination.original.Mode()),
	); err != nil {
		return invalidIndexDestination(
			destination.output.relative,
			"original ownership claim changed during batch publication",
		)
	}
	if destination.witnessInfo == nil ||
		!os.SameFile(destination.restoreInfo, destination.witnessInfo) {
		return invalidIndexDestination(
			destination.output.relative,
			"original ownership claim does not match the manifest-bound identity witness",
		)
	}
	return nil
}

func indexBatchRootCommitBit(root *indexDestination) bool {
	if root == nil || root.parent == nil || root.stageInfo == nil {
		return false
	}
	info, err := root.parent.Lstat(root.leaf)
	return err == nil &&
		info.Mode()&os.ModeSymlink == 0 &&
		info.Mode().IsRegular() &&
		os.SameFile(root.stageInfo, info)
}

func indexBatchWrittenPaths(destinations []*indexDestination) []string {
	written := make([]string, len(destinations))
	for index, destination := range destinations {
		written[index] = destination.output.absolute
	}
	return written
}

// cleanupDurableIndexBatchCanonical routes an in-process durable batch through
// the same manifest-bound planner and proof-last executor used after restart.
// A failure before the manifest boundary deliberately leaves M and every
// remaining exact proof in place; a failure after it is handled by the
// manifest-free terminal-prefix classifier on the next pass.
func cleanupDurableIndexBatchCanonical(
	batch *indexBatchPublication,
	hooks indexPublishHooks,
) error {
	if batch == nil || !batch.manifestLive {
		return nil
	}
	if !batch.complete {
		return errors.New("durable index batch manifest is incomplete")
	}
	if batch.root == nil || batch.root.root == nil || batch.root.parent == nil {
		return errors.New("durable index batch root is unavailable")
	}
	discoveries, err := discoverIndexTransactionNamespace(batch.root.root)
	if err != nil {
		return err
	}
	prepared, err := prepareIndexRecoveryWithSharedInventory(
		batch.root.root,
		discoveries,
		hooks,
		true,
	)
	if err != nil {
		return err
	}
	defer prepared.close()
	if err := prepared.apply(); err != nil {
		return err
	}
	if _, err := batch.root.parent.Lstat(batch.name); errors.Is(err, fs.ErrNotExist) {
		batch.manifestLive = false
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("canonical index batch cleanup retained its manifest")
}

func cleanupIndexBatchManifest(
	batch *indexBatchPublication,
	hooks indexPublishHooks,
) error {
	if batch == nil || !batch.manifestLive {
		return nil
	}
	if batch.root == nil || batch.root.parent == nil {
		return errors.New("index batch manifest parent is unavailable")
	}
	manifestHooks := hooks
	manifestHooks.beforeCleanup = func(relative string, parent *os.Root, kind, name string) error {
		var hookErr error
		if hooks.beforeCleanup != nil {
			hookErr = hooks.beforeCleanup(relative, parent, kind, name)
		}
		for _, destination := range batch.destinations {
			if parentErr := verifyIndexDestinationParent(destination); parentErr != nil {
				return errors.Join(hookErr, detachedIndexRecoveryError(destination, parentErr))
			}
		}
		return hookErr
	}
	removed, err := removeCreatedIndexArtifact(
		batch.root,
		manifestHooks,
		"manifest",
		batch.name,
		batch.spec.info,
		batch.complete,
		batch.data,
		indexBatchManifestMode,
	)
	if removed {
		batch.manifestLive = false
	}
	return err
}

func indexBatchPayloadDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func indexBatchManifestName(data []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("okf:index-batch-manifest:v3\x00"))
	_, _ = digest.Write(data)
	return indexBatchManifestPrefix + hex.EncodeToString(digest.Sum(nil))
}

func parseIndexBatchManifestName(name string) bool {
	if !stringsHasCanonicalDigestPrefix(name, indexBatchManifestPrefix) {
		return false
	}
	return len(name) == len(indexBatchManifestPrefix)+sha256.Size*2
}

func stringsHasCanonicalDigestPrefix(name, prefix string) bool {
	if !bytes.HasPrefix([]byte(name), []byte(prefix)) {
		return false
	}
	for _, character := range name[len(prefix):] {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func validateExistingIndexBatchManifestArtifact(
	parent *os.Root,
	destination, storageName, protocolName string,
) string {
	if destination != indexFilename {
		return "batch manifest is not stored at the bundle root"
	}
	if !parseIndexBatchManifestName(protocolName) {
		return "malformed index batch manifest name"
	}
	before, err := parent.Lstat(storageName)
	if err != nil {
		return "index batch manifest cannot be inspected"
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return "index batch manifest is not a regular file"
	}
	if indexPreservedMode(before.Mode()) != indexBatchManifestMode {
		return "index batch manifest mode is not canonical"
	}
	data, err := readRegularIndexDestination(parent, storageName)
	if err != nil {
		return "index batch manifest cannot be read without following links"
	}
	after, err := parent.Lstat(storageName)
	if err != nil ||
		after.Mode()&os.ModeSymlink != 0 ||
		!after.Mode().IsRegular() ||
		!os.SameFile(before, after) ||
		indexPreservedMode(after.Mode()) != indexBatchManifestMode {
		return "index batch manifest changed while verifying"
	}
	if indexBatchManifestName(data) != protocolName {
		return "index batch manifest digest does not match its content"
	}
	var manifest indexBatchManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "index batch manifest is not canonical JSON"
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return "index batch manifest cannot be canonicalized"
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(canonical, data) {
		return "index batch manifest encoding is not canonical"
	}
	if err := validateIndexBatchManifestShape(manifest); err != nil {
		return err.Error()
	}
	return ""
}

// privateIndexBatchManifestCandidate recognizes a durable private-first
// creation residue without trusting its temporary pathname. The caller may
// normalize it to the returned canonical reserved manifest name only when the
// private claim digest also binds that name.
func privateIndexBatchManifestCandidate(
	spec publicationFileSpec,
) (indexBatchManifest, string, bool) {
	var manifest indexBatchManifest
	if !spec.complete ||
		spec.mode != indexBatchManifestMode ||
		json.Unmarshal(spec.data, &manifest) != nil ||
		validateIndexBatchManifestShape(manifest) != nil {
		return indexBatchManifest{}, "", false
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return indexBatchManifest{}, "", false
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(canonical, spec.data) {
		return indexBatchManifest{}, "", false
	}
	return manifest, indexBatchManifestName(spec.data), true
}

func validateIndexBatchManifestShape(manifest indexBatchManifest) error {
	if manifest.Protocol != indexBatchManifestProtocol ||
		manifest.Version != indexBatchManifestVersion {
		return errors.New("index batch manifest protocol or version is unsupported")
	}
	if len(manifest.Entries) == 0 {
		return errors.New("index batch manifest has no entries")
	}
	rootCount := 0
	seenPaths := make(map[string]struct{}, len(manifest.Entries))
	seenArtifacts := make(map[string]struct{}, len(manifest.Entries)*8)
	for index, entry := range manifest.Entries {
		if cleanIndexDestinationPath(entry.Path) != entry.Path ||
			ValidateRevisionPath(entry.Path) != nil ||
			path.Base(entry.Path) != indexFilename {
			return errors.New("index batch manifest contains an unsafe target path")
		}
		if index > 0 && !indexBatchEntryLess(manifest.Entries[index-1].Path, entry.Path) {
			return errors.New("index batch manifest entries are not canonically ordered")
		}
		if _, exists := seenPaths[entry.Path]; exists {
			return errors.New("index batch manifest contains a duplicate target")
		}
		seenPaths[entry.Path] = struct{}{}
		if entry.Path == indexFilename {
			rootCount++
		}
		if !validIndexBatchDigest(entry.NewDigest) || fs.FileMode(entry.NewMode) != indexPreservedMode(fs.FileMode(entry.NewMode)) {
			return errors.New("index batch manifest contains an invalid new payload contract")
		}
		if entry.OldPresent {
			if !validIndexBatchDigest(entry.OldDigest) ||
				fs.FileMode(entry.OldMode) != indexPreservedMode(fs.FileMode(entry.OldMode)) ||
				entry.Backup == "" ||
				entry.Claim == "" ||
				entry.Anchor == "" ||
				entry.Witness == "" {
				return errors.New("index batch manifest contains incomplete original evidence")
			}
		} else if entry.OldDigest != "" ||
			entry.OldMode != 0 ||
			entry.Backup != "" ||
			entry.Claim != "" ||
			entry.Anchor != "" ||
			entry.Witness != "" {
			return errors.New("index batch manifest binds original evidence for an absent target")
		}
		directory := path.Dir(entry.Path)
		if directory == "." {
			directory = ""
		}
		for _, bound := range []struct {
			kind     indexArtifactKind
			artifact string
		}{
			{kind: indexArtifactStage, artifact: entry.Stage},
			{kind: indexArtifactBackup, artifact: entry.Backup},
			{kind: indexArtifactRestore, artifact: entry.Claim},
			{kind: indexArtifactAnchor, artifact: entry.Anchor},
			{kind: indexArtifactWitness, artifact: entry.Witness},
			{kind: indexArtifactNewInstall, artifact: entry.NewInstall},
			{kind: indexArtifactRestoreInstall, artifact: entry.RestoreInstall},
			{kind: indexArtifactDiscard, artifact: entry.Discard},
		} {
			kind, artifact := bound.kind, bound.artifact
			if artifact == "" {
				switch kind {
				case indexArtifactStage, indexArtifactNewInstall, indexArtifactRestoreInstall, indexArtifactDiscard:
					return errors.New("index batch manifest entry has an incomplete v3 artifact contract")
				}
				continue
			}
			if path.Dir(artifact) != path.Clean(firstNonEmpty(directory, ".")) {
				return errors.New("index batch manifest artifact is outside its target directory")
			}
			if _, exists := seenArtifacts[artifact]; exists {
				return errors.New("index batch manifest contains a duplicate artifact path")
			}
			name := path.Base(artifact)
			if _, ok := parseIndexArtifactName(name, kind); !ok {
				return errors.New("index batch manifest contains a malformed artifact name")
			}
			seenArtifacts[artifact] = struct{}{}
		}
	}
	if rootCount != 1 {
		return errors.New("index batch manifest must contain exactly one root output")
	}
	return nil
}

func indexBatchEntryLess(left, right string) bool {
	leftRoot := left == indexFilename
	rightRoot := right == indexFilename
	if leftRoot != rightRoot {
		return !leftRoot
	}
	return left < right
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func validIndexBatchDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}
