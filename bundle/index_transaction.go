package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

const indexTransactionPrefix = ".okf-index-txn-"
const indexRecoveryBackupPrefix = indexTransactionPrefix + "backup-v1-"
const indexStagePrefix = indexTransactionPrefix + "stage-v1-"
const indexRestorePrefix = indexTransactionPrefix + "restore-v1-"
const indexAnchorPrefix = indexTransactionPrefix + "anchor-v2-"
const indexWitnessPrefix = indexTransactionPrefix + "witness-v2-"
const indexNewInstallPrefix = indexTransactionPrefix + "new-install-v3-"
const indexRestoreInstallPrefix = indexTransactionPrefix + "restore-install-v3-"
const indexDiscardPrefix = indexTransactionPrefix + "discard-v3-"
const indexBatchManifestPrefix = indexTransactionPrefix + "manifest-v3-"

type indexArtifactKind string

const (
	indexArtifactBackup         indexArtifactKind = "backup"
	indexArtifactStage          indexArtifactKind = "stage"
	indexArtifactRestore        indexArtifactKind = "restore"
	indexArtifactAnchor         indexArtifactKind = "anchor"
	indexArtifactWitness        indexArtifactKind = "witness"
	indexArtifactNewInstall     indexArtifactKind = "new-install"
	indexArtifactRestoreInstall indexArtifactKind = "restore-install"
	indexArtifactDiscard        indexArtifactKind = "discard"
	indexArtifactManifest       indexArtifactKind = "manifest"
	indexArtifactUnknown        indexArtifactKind = "unknown"
)

type indexOutput struct {
	relative         string
	absolute         string
	data             []byte
	expectedOriginal *indexExpectedOriginal
}

type indexExpectedOriginal struct {
	present bool
	data    []byte
}

type indexPublishHooks struct {
	afterIndexLock             func() error
	beforeSourceClose          func()
	afterResolve               func() error
	afterArtifactOpen          func(relative, kind, name string, file *os.File) error
	afterArtifactCreate        func(relative, kind, name string, file *os.File) error
	afterArtifactWrite         func(relative, kind, name string, file *os.File) error
	afterArtifactChmod         func(relative, kind, name string, file *os.File) error
	fileSync                   func(relative, kind, name string, file *os.File) error
	afterArtifactFileSync      func(relative, kind, name string, file *os.File) error
	afterArtifactClose         func(relative, kind, name string, parent *os.Root) error
	afterArtifactVerified      func(relative, kind, name string, parent *os.Root) error
	directorySync              func(relative, kind, name string, parent *os.Root) error
	afterArtifactDirectorySync func(
		relative, kind, name string,
		parent *os.Root,
	) error
	afterBatchManifestDurable   func(root *os.Root, name string) error
	beforeRootCommit            func(relative string, parent *os.Root, stage string) error
	afterRootCommit             func(relative string, parent *os.Root, stage, leaf string) error
	afterBatchTargetObservation func(relative string, parent *os.Root, leaf string) error
	afterBatchRecoveryVacate    func(relative string, parent *os.Root, leaf string) error
	afterBatchRecoveryAction    func(relative string, parent *os.Root, leaf string) error
	beforePublish               func(relative string, ordinal int) error
	beforeInstall               func(relative string, parent *os.Root, stage string) error
	beforeVacate                func(relative string, parent *os.Root, leaf string) error
	afterVacateRename           func(relative string, parent *os.Root, leaf, claim string) error
	afterVacate                 func(relative string, parent *os.Root, leaf, backup string) error
	afterInstallLink            func(relative string, parent *os.Root, stage, leaf string) error
	afterBackupVerified         func(relative string, parent *os.Root, backup string) error
	afterRestoreLink            func(relative string, parent *os.Root, stage, leaf string) error
	beforeRestore               func(relative string, parent *os.Root, leaf, backup string) error
	afterRecoveryDiscovery      func(relative string, root *os.Root, artifact string) error
	afterRecoveryObservation    func(relative string, parent *os.Root, name string) error
	beforeRecoveryClaim         func(relative string, parent *os.Root, backup string) error
	beforeCleanup               func(relative string, parent *os.Root, kind, name string) error
	afterCleanup                func(relative string, parent *os.Root, kind, name string) error
	beforeFinalValidation       func(relative string, ordinal int) error
	beforeClose                 func(relative string) error
	afterPublish                func(relative string, ordinal int) error
}

type indexDestination struct {
	output                 indexOutput
	root                   *os.Root
	parent                 *os.Root
	directory              string
	parentInfo             os.FileInfo
	leaf                   string
	existed                bool
	original               os.FileInfo
	originalData           []byte
	stage                  string
	stageInfo              os.FileInfo
	stageComplete          bool
	newInstall             string
	newInstallInfo         os.FileInfo
	newInstallComplete     bool
	newInstallConsumed     bool
	restoreInstall         string
	restoreInstallInfo     os.FileInfo
	restoreInstallComplete bool
	restoreInstallConsumed bool
	discard                string
	discardInfo            os.FileInfo
	discardComplete        bool
	publishedInfo          os.FileInfo
	backup                 string
	backupInfo             os.FileInfo
	backupComplete         bool
	restoreStage           string
	restoreInfo            os.FileInfo
	restoreComplete        bool
	rollbackClaim          string
	rollbackInfo           os.FileInfo
	rollbackComplete       bool
	rollbackKind           indexArtifactKind
	vacateClaim            string
	anchor                 string
	anchorSource           string
	anchorInfo             os.FileInfo
	anchorComplete         bool
	witness                string
	witnessInfo            os.FileInfo
	witnessComplete        bool
	preserveBackup         bool
	published              bool
	rollbackTargetInfo     os.FileInfo
	transactionScope       *indexBatchTransactionScope
}

type indexBatchTransactionScope struct {
	destinations           []*indexDestination
	batch                  *indexBatchPublication
	rollbackPeers          map[*indexDestination]struct{}
	rollbackFrozenTargets  map[*indexDestination]indexRollbackFrozenPath
	rollbackFrozenPaths    map[string]indexRollbackFrozenPath
	rollbackFrozenManifest *indexRollbackFrozenPath
	rollbackBaselineErr    error
}

type indexRollbackFrozenPath struct {
	present bool
	info    os.FileInfo
	regular *publicationFileSpec
}

func publishIndexOutputs(root *os.Root, outputs []indexOutput, hooks indexPublishHooks) (written []string, err error) {
	if len(outputs) == 0 {
		return nil, nil
	}

	destinations, err := preflightIndexDestinations(root, outputs, hooks)
	if err != nil {
		return nil, err
	}
	destinations, rootDestination, err := orderIndexBatchDestinations(destinations)
	if err != nil {
		_ = closeIndexDestinationParents(destinations, hooks)
		return nil, err
	}
	transactionScope := &indexBatchTransactionScope{
		destinations: destinations,
	}
	for _, destination := range destinations {
		destination.transactionScope = transactionScope
	}
	var batch *indexBatchPublication
	finalized := false
	committed := false
	defer func() {
		if !finalized {
			switch {
			case batch != nil && batch.manifestLive && batch.complete:
				batchCleanupErr := cleanupDurableIndexBatchCanonical(batch, hooks)
				err = errors.Join(err, batchCleanupErr)
				if batchCleanupErr == nil && !batch.manifestLive {
					transactionScope.rollbackPeers = nil
					transactionScope.rollbackFrozenTargets = nil
					transactionScope.rollbackFrozenPaths = nil
					transactionScope.rollbackFrozenManifest = nil
					transactionScope.rollbackBaselineErr = nil
				}
			default:
				if batch != nil {
					err = errors.Join(err, cleanupIndexBatchManifest(batch, hooks))
				}
				err = errors.Join(err, cleanupIndexTransaction(destinations, hooks))
			}
		}
		closeErr := closeIndexDestinationParents(destinations, hooks)
		if committed {
			if closeErr != nil {
				err = errors.Join(err, indexCommittedError("close destination parents", closeErr))
			}
		} else {
			err = errors.Join(err, closeErr)
			if err != nil {
				written = nil
			}
		}
	}()

	if err := prepareIndexBatchArtifacts(destinations, hooks); err != nil {
		return nil, err
	}
	batch, err = createDurableIndexBatchManifest(destinations, rootDestination, hooks)
	transactionScope.batch = batch
	if err != nil {
		return nil, err
	}
	rollback := func(prefix []*indexDestination) error {
		if err := captureIndexBatchRollbackBaseline(
			transactionScope,
			prefix,
		); err != nil {
			finalized = true
			return err
		}
		rollbackErr := errors.Join(
			rollbackIndexBatchDestinations(prefix, hooks),
			transactionScope.rollbackBaselineErr,
		)
		if rollbackErr != nil {
			// Preserve the complete manifest-bound evidence set. Recovery can
			// finish the rollback only while that immutable set remains intact.
			finalized = true
		}
		return rollbackErr
	}

	for ordinal, destination := range destinations[:len(destinations)-1] {
		if hooks.beforePublish != nil {
			if err := hooks.beforePublish(destination.output.relative, ordinal); err != nil {
				rollbackErr := rollback(destinations[:ordinal])
				return nil, errors.Join(err, rollbackErr)
			}
		}
		if err := publishIndexDestination(destination, hooks); err != nil {
			alreadyRolledBack := hasExactIndexBatchRollbackTarget(destination)
			destinationBlocked := !destination.published &&
				destination.restoreStage == "" &&
				(verifyIndexDestinationParent(destination) != nil ||
					verifyOriginalIndexDestination(destination) != nil)
			if destinationBlocked {
				// The transaction has no ownership evidence for this public
				// blocker. Preserve M and every bound artifact so recovery
				// remains a repeatable zero-mutation conflict.
				finalized = true
			}
			if destination.preserveBackup {
				// The exact anchor target is already restored, but one of the
				// other manifest-bound evidence paths changed. Preserve the
				// complete set for deterministic manual recovery.
				finalized = true
			}
			rollbackEnd := ordinal
			if (destination.published || destination.restoreStage != "") &&
				!alreadyRolledBack &&
				!destination.preserveBackup {
				rollbackEnd = ordinal + 1
			}
			rollbackErr := rollback(destinations[:rollbackEnd])
			return nil, errors.Join(err, rollbackErr)
		}
		if hooks.afterPublish != nil {
			if err := hooks.afterPublish(destination.output.relative, ordinal); err != nil {
				rollbackErr := rollback(destinations[:ordinal+1])
				return nil, errors.Join(err, rollbackErr)
			}
		}
	}

	rootOrdinal := len(destinations) - 1
	if err := validateIndexBatchPublicationPrefix(destinations, rootOrdinal); err != nil {
		rollbackErr := rollback(destinations[:rootOrdinal])
		return nil, errors.Join(err, rollbackErr)
	}
	if hooks.beforeRootCommit != nil {
		if err := hooks.beforeRootCommit(
			rootDestination.output.relative,
			rootDestination.parent,
			rootDestination.stage,
		); err != nil {
			rollbackErr := rollback(destinations[:rootOrdinal])
			return nil, errors.Join(err, rollbackErr)
		}
	}
	if hooks.beforePublish != nil {
		if err := hooks.beforePublish(rootDestination.output.relative, rootOrdinal); err != nil {
			rollbackErr := rollback(destinations[:rootOrdinal])
			return nil, errors.Join(err, rollbackErr)
		}
	}
	if err := publishIndexDestination(rootDestination, hooks); err != nil {
		if indexBatchRootCommitBit(rootDestination) {
			committed = true
			written = indexBatchWrittenPaths(destinations)
			finalized = true
			return written, indexCommittedError("root commit state requires recovery", err)
		}
		rootAlreadyRolledBack := hasExactIndexBatchRollbackTarget(rootDestination)
		rootBlocked := !rootAlreadyRolledBack &&
			(verifyIndexDestinationParent(rootDestination) != nil ||
				verifyOriginalIndexDestination(rootDestination) != nil)
		if rootBlocked {
			// The transaction did not mutate the root, so it must not replace
			// the foreign blocker. Keep the immutable manifest and every bound
			// artifact after rolling back the earlier published prefix; a
			// later recovery pass must make the same decision.
			finalized = true
		}
		if rootDestination.preserveBackup {
			finalized = true
		}
		rollbackSet := destinations[:rootOrdinal]
		if (rootDestination.published || rootDestination.restoreStage != "") &&
			!rootAlreadyRolledBack &&
			!rootDestination.preserveBackup {
			rollbackSet = destinations
		}
		rollbackErr := rollback(rollbackSet)
		return nil, errors.Join(err, rollbackErr)
	}

	committed = true
	written = indexBatchWrittenPaths(destinations)
	if hooks.afterRootCommit != nil {
		if err := hooks.afterRootCommit(
			rootDestination.output.relative,
			rootDestination.parent,
			rootDestination.stage,
			rootDestination.leaf,
		); err != nil {
			finalized = true
			return written, indexCommittedError("after root commit", err)
		}
	}
	if hooks.afterPublish != nil {
		if err := hooks.afterPublish(rootDestination.output.relative, rootOrdinal); err != nil {
			finalized = true
			return written, indexCommittedError("after root publication", err)
		}
	}
	if validationErr := validatePublishedIndexDestinations(destinations, hooks); validationErr != nil {
		finalized = true
		return written, indexCommittedError("final batch validation", validationErr)
	}
	if cleanupErr := cleanupDurableIndexBatchCanonical(batch, hooks); cleanupErr != nil {
		finalized = true
		return written, indexCommittedError("transaction cleanup is pending", cleanupErr)
	}
	finalized = true
	return written, nil
}

func indexCommittedError(reason string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrPublicationCommitted, reason, err)
}

func validatePublishedIndexDestinations(
	destinations []*indexDestination,
	hooks indexPublishHooks,
) error {
	for ordinal, destination := range destinations {
		if hooks.beforeFinalValidation != nil {
			if err := hooks.beforeFinalValidation(destination.output.relative, ordinal); err != nil {
				return err
			}
		}
		if err := verifyIndexDestinationParent(destination); err != nil {
			return invalidIndexDestination(
				destination.output.relative,
				"destination parent changed before commit",
			)
		}
		if err := verifyInstalledIndexDestination(destination); err != nil {
			return invalidIndexDestination(
				destination.output.relative,
				"published destination changed before commit",
			)
		}
	}
	return nil
}

func preflightIndexDestinations(
	root *os.Root,
	outputs []indexOutput,
	hooks indexPublishHooks,
) ([]*indexDestination, error) {
	ordered := append([]indexOutput(nil), outputs...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].relative < ordered[right].relative
	})

	byRelative := make(map[string]*indexDestination, len(ordered))
	for _, output := range ordered {
		relative := cleanIndexDestinationPath(output.relative)
		if err := ValidateRevisionPath(relative); err != nil {
			closeIndexDestinationMap(byRelative)
			return nil, invalidIndexDestination(relative, "path is not revision-safe")
		}
		if _, exists := byRelative[relative]; exists {
			closeIndexDestinationMap(byRelative)
			return nil, invalidIndexDestination(relative, "duplicate destination")
		}

		directory, leaf := path.Split(relative)
		directory = strings.TrimSuffix(directory, "/")
		if err := validateIndexDestinationAncestors(root, directory); err != nil {
			closeIndexDestinationMap(byRelative)
			return nil, invalidIndexDestination(relative, err.Error())
		}
		parent, err := openDirectory(root, directory)
		if err != nil {
			closeIndexDestinationMap(byRelative)
			return nil, invalidIndexDestination(relative, "destination ancestor changed or is unsafe")
		}
		parentInfo, err := parent.Lstat(".")
		if err != nil || !parentInfo.IsDir() {
			parent.Close()
			closeIndexDestinationMap(byRelative)
			return nil, invalidIndexDestination(relative, "cannot pin destination parent")
		}
		destination := &indexDestination{
			output:     output,
			root:       root,
			parent:     parent,
			directory:  directory,
			parentInfo: parentInfo,
			leaf:       leaf,
		}
		if err := inspectExistingIndexRecoveryBackups(destination, hooks, nil); err != nil {
			parent.Close()
			closeIndexDestinationMap(byRelative)
			return nil, err
		}
		info, statErr := parent.Lstat(leaf)
		switch {
		case statErr == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				parent.Close()
				closeIndexDestinationMap(byRelative)
				return nil, invalidIndexDestination(relative, "destination is a symlink")
			}
			if info.IsDir() {
				parent.Close()
				closeIndexDestinationMap(byRelative)
				return nil, invalidIndexDestination(relative, "destination is a directory")
			}
			if !info.Mode().IsRegular() {
				parent.Close()
				closeIndexDestinationMap(byRelative)
				return nil, invalidIndexDestination(relative, "destination is not a regular file")
			}
			destination.existed = true
			destination.original = info
			data, err := readRegularIndexDestination(parent, leaf)
			if err != nil {
				parent.Close()
				closeIndexDestinationMap(byRelative)
				return nil, invalidIndexDestination(relative, "destination changed while reading")
			}
			afterRead, err := parent.Lstat(leaf)
			if err != nil ||
				afterRead.Mode()&os.ModeSymlink != 0 ||
				!afterRead.Mode().IsRegular() ||
				!os.SameFile(info, afterRead) ||
				indexPreservedMode(afterRead.Mode()) != indexPreservedMode(info.Mode()) {
				parent.Close()
				closeIndexDestinationMap(byRelative)
				return nil, invalidIndexDestination(relative, "destination changed while reading")
			}
			destination.originalData = data
		case errors.Is(statErr, fs.ErrNotExist):
			// A missing leaf is a valid create destination.
		default:
			parent.Close()
			closeIndexDestinationMap(byRelative)
			return nil, invalidIndexDestination(relative, "cannot inspect destination")
		}
		if expected := output.expectedOriginal; expected != nil {
			if expected.present != destination.existed ||
				expected.present && !bytes.Equal(expected.data, destination.originalData) {
				parent.Close()
				closeIndexDestinationMap(byRelative)
				return nil, invalidIndexDestination(relative, "destination changed after version resolution")
			}
		}
		byRelative[relative] = destination
	}

	destinations := make([]*indexDestination, 0, len(outputs))
	for _, output := range outputs {
		destinations = append(destinations, byRelative[cleanIndexDestinationPath(output.relative)])
	}
	return destinations, nil
}

func cleanIndexDestinationPath(relative string) string {
	relative = strings.TrimPrefix(relative, "./")
	if relative == "" {
		return indexFilename
	}
	return relative
}

func validateIndexDestinationAncestors(root *os.Root, directory string) error {
	if directory == "" || directory == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(directory, "/") {
		current = pathJoin(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("ancestor %q does not exist", current)
		}
		if err != nil {
			return fmt.Errorf("cannot inspect ancestor %q", current)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ancestor %q is a symlink", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("ancestor %q is not a directory", current)
		}
	}
	return nil
}

func invalidIndexDestination(relative, reason string) error {
	return fmt.Errorf("invalid index destination %q: %s", relative, reason)
}

func stageIndexDestination(destination *indexDestination, hooks indexPublishHooks) error {
	if err := verifyIndexDestinationParent(destination); err != nil {
		return err
	}
	mode := fs.FileMode(0o644)
	if destination.existed {
		mode = indexPreservedMode(destination.original.Mode())
	}
	stage, spec, complete, err := createDurableIndexArtifact(
		context.Background(),
		destination,
		indexArtifactStage,
		mode,
		destination.output.data,
		hooks,
	)
	destination.stage = stage
	destination.stageInfo = spec.info
	destination.stageComplete = complete
	if err != nil {
		return fmt.Errorf("stage index destination %q: create durable artifact: %w", destination.output.relative, err)
	}
	if err := verifyIndexDestinationParent(destination); err != nil {
		return err
	}
	return nil
}

func prepareIndexBatchV3InstallArtifacts(
	destination *indexDestination,
	hooks indexPublishHooks,
) error {
	newMode := indexPreservedMode(destination.stageInfo.Mode())
	newData := destination.output.data
	var err error
	restoreMode, restoreData := newMode, newData
	if destination.existed {
		restoreMode = indexPreservedMode(destination.original.Mode())
		restoreData = destination.originalData
	}
	destination.restoreInstall, err = allocateIndexArtifactClaimName(
		destination.parent,
		indexArtifactRestoreInstall,
		destination.output.relative,
		restoreMode,
		restoreData,
	)
	if err != nil {
		return fmt.Errorf("reserve restore install token: %w", err)
	}
	destination.discard, err = allocateIndexArtifactClaimName(
		destination.parent,
		indexArtifactDiscard,
		destination.output.relative,
		newMode,
		newData,
	)
	if err != nil {
		return fmt.Errorf("reserve discard token: %w", err)
	}

	template, err := newIndexArtifactNameTemplate(
		indexArtifactNewInstall,
		destination.output.relative,
		newMode,
		newData,
	)
	if err != nil {
		return err
	}
	stage := publicationSpecFromBytes(destination.stageInfo, newData)
	for slot := 0; slot < 10_000; slot++ {
		name := template.Name(slot)
		spec, createErr := createDurablePublicationWitness(
			context.Background(),
			destination.parent,
			destination.stage,
			stage,
			name,
			indexArtifactCoreHooks(
				destination,
				indexArtifactNewInstall,
				name,
				hooks,
			),
		)
		if createErr == nil {
			destination.newInstall = name
			destination.newInstallInfo = spec.info
			destination.newInstallComplete = spec.complete
			return verifyIndexNewInstall(destination)
		}
		if spec.created {
			destination.newInstall = name
			destination.newInstallInfo = spec.info
			destination.newInstallComplete = spec.complete
			return createErr
		}
		if !errors.Is(createErr, fs.ErrExist) {
			return createErr
		}
	}
	return errors.New("new install token slots exhausted")
}

func verifyIndexNewInstall(destination *indexDestination) error {
	if destination == nil ||
		destination.newInstall == "" ||
		destination.newInstallInfo == nil ||
		!destination.newInstallComplete ||
		destination.stageInfo == nil ||
		!os.SameFile(destination.stageInfo, destination.newInstallInfo) {
		return invalidIndexDestination(
			destination.output.relative,
			"new install token does not retain staged identity",
		)
	}
	return verifyCreatedIndexArtifactForCleanup(
		destination,
		destination.newInstall,
		destination.newInstallInfo,
		destination.newInstallComplete,
		destination.output.data,
		indexPreservedMode(destination.stageInfo.Mode()),
	)
}

func publishIndexDestination(destination *indexDestination, hooks indexPublishHooks) error {
	if err := verifyIndexDestinationParent(destination); err != nil {
		return err
	}
	if err := verifyStagedIndexDestination(destination); err != nil {
		return err
	}
	if err := verifyOriginalIndexDestination(destination); err != nil {
		return err
	}
	if hooks.beforeInstall != nil {
		if err := hooks.beforeInstall(destination.output.relative, destination.parent, destination.stage); err != nil {
			return err
		}
		if err := verifyStagedIndexDestination(destination); err != nil {
			return err
		}
		if err := verifyOriginalIndexDestination(destination); err != nil {
			return err
		}
	}
	if destination.existed {
		if destination.backup == "" {
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
		} else if err := verifyIndexBackup(destination); err != nil {
			return err
		}
		if err := verifyOriginalIndexDestination(destination); err != nil {
			return claimVerifiedIndexRecovery(
				destination,
				hooks,
				"destination changed after backup",
				err,
			)
		}
		originalSpec := publicationSpecFromBytes(destination.original, destination.originalData)
		claimName := destination.vacateClaim
		if claimName == "" {
			var err error
			claimName, err = prepareIndexVacateClaimName(destination, hooks)
			if err != nil {
				return fmt.Errorf(
					"publish index destination %q: create ownership claim: %w",
					destination.output.relative,
					err,
				)
			}
			destination.vacateClaim = claimName
		}
		vacateHooks := publicationBarrierHooks{
			validateScope: indexTransactionArtifactScopeValidator(
				destination,
				claimName,
			),
			beforeVacate: func() error {
				if hooks.beforeVacate == nil {
					return nil
				}
				return hooks.beforeVacate(
					destination.output.relative,
					destination.parent,
					destination.leaf,
				)
			},
			afterVacateRename: func() error {
				if hooks.afterVacateRename == nil {
					return nil
				}
				return hooks.afterVacateRename(
					destination.output.relative,
					destination.parent,
					destination.leaf,
					claimName,
				)
			},
			afterVacate: func() error {
				if hooks.afterVacate == nil {
					return nil
				}
				return hooks.afterVacate(
					destination.output.relative,
					destination.parent,
					destination.leaf,
					destination.backup,
				)
			},
		}
		claimed, err := guardedVacatePublicationLeaf(
			context.Background(),
			destination.parent,
			destination.leaf,
			originalSpec,
			claimName,
			vacateHooks,
		)
		if claimed.created {
			destination.restoreStage = claimName
			destination.restoreInfo = claimed.info
			destination.restoreComplete = claimed.info != nil &&
				os.SameFile(destination.original, claimed.info) &&
				claimed.mode == originalSpec.mode &&
				bytes.Equal(claimed.data, originalSpec.data)
			if claimed.info == nil {
				recaptured, captureErr := capturePublicationFile(
					context.Background(),
					destination.parent,
					claimName,
				)
				if captureErr == nil && recaptured.info != nil {
					destination.restoreInfo = recaptured.info
					destination.restoreComplete =
						os.SameFile(destination.original, recaptured.info) &&
							recaptured.mode == originalSpec.mode &&
							bytes.Equal(recaptured.data, originalSpec.data)
				}
			}
		}
		if err != nil {
			if verifyErr := verifyOriginalIndexDestination(destination); verifyErr == nil {
				return indexPublicationStepError{
					destination: destination.output.relative,
					action:      "vacate destination",
					err:         err,
				}
			}
			restoreErr := restoreFailedIndexPublication(destination, hooks)
			return errors.Join(err, restoreErr)
		}
		if destination.witness != "" {
			if err := verifyIndexBatchOriginalClaim(destination); err != nil {
				return err
			}
		}
	}

	stageSpec := publicationSpecFromBytes(destination.stageInfo, destination.output.data)
	installSpec := publicationSpecFromBytes(destination.newInstallInfo, destination.output.data)
	installHooks := publicationBarrierHooks{
		validateScope: indexTransactionArtifactScopeValidator(
			destination,
			destination.newInstall,
		),
		afterInstall: func() error {
			if hooks.afterInstallLink == nil {
				return nil
			}
			return hooks.afterInstallLink(
				destination.output.relative,
				destination.parent,
				destination.newInstall,
				destination.leaf,
			)
		},
	}
	installed, installErr := guardedConsumePublicationInstallLeaf(
		context.Background(),
		destination.parent,
		destination.newInstall,
		installSpec,
		destination.stage,
		stageSpec,
		destination.leaf,
		installHooks,
	)
	if installed {
		destination.newInstallConsumed = true
		destination.published = true
		destination.publishedInfo = stageSpec.info
	}
	if installErr != nil {
		current, statErr := destination.parent.Lstat(destination.leaf)
		if statErr == nil &&
			current.Mode()&os.ModeSymlink == 0 &&
			current.Mode().IsRegular() &&
			os.SameFile(destination.stageInfo, current) {
			destination.published = true
			destination.publishedInfo = stageSpec.info
			verifyErr := verifyInstalledIndexDestination(destination)
			rollbackErr := rollbackUnexpectedInstalledIndex(destination, hooks)
			return errors.Join(installErr, verifyErr, rollbackErr)
		}
		restoreErr := restoreFailedIndexPublication(destination, hooks)
		return errors.Join(
			restoreErr,
			indexPublicationStepError{
				destination: destination.output.relative,
				action:      "install staged file",
				err:         installErr,
			},
		)
	}
	destination.published = true
	destination.publishedInfo = stageSpec.info
	if err := verifyInstalledIndexDestination(destination); err != nil {
		rollbackErr := rollbackUnexpectedInstalledIndex(destination, hooks)
		return errors.Join(
			err,
			rollbackErr,
		)
	}
	if err := verifyIndexDestinationParent(destination); err != nil {
		rollbackErr := rollbackIndexDestination(destination, hooks)
		return errors.Join(err, rollbackErr)
	}
	return nil
}

func verifyStagedIndexDestination(destination *indexDestination) error {
	info, err := destination.parent.Lstat(destination.stage)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		!os.SameFile(destination.stageInfo, info) ||
		indexPreservedMode(info.Mode()) != indexPreservedMode(destination.stageInfo.Mode()) {
		return invalidIndexDestination(destination.output.relative, "staged file changed before publication")
	}
	data, err := readRegularIndexDestination(destination.parent, destination.stage)
	if err != nil || !bytes.Equal(data, destination.output.data) {
		return invalidIndexDestination(destination.output.relative, "staged file content changed before publication")
	}
	afterRead, err := destination.parent.Lstat(destination.stage)
	if err != nil ||
		afterRead.Mode()&os.ModeSymlink != 0 ||
		!afterRead.Mode().IsRegular() ||
		!os.SameFile(destination.stageInfo, afterRead) ||
		indexPreservedMode(afterRead.Mode()) != indexPreservedMode(destination.stageInfo.Mode()) {
		return invalidIndexDestination(destination.output.relative, "staged file changed before publication")
	}
	return nil
}

func verifyOriginalIndexDestination(destination *indexDestination) error {
	current, err := destination.parent.Lstat(destination.leaf)
	if !destination.existed {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return invalidIndexDestination(destination.output.relative, "destination appeared after preflight")
	}
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(destination.original, current) ||
		indexPreservedMode(current.Mode()) != indexPreservedMode(destination.original.Mode()) {
		return invalidIndexDestination(destination.output.relative, "destination changed after preflight")
	}
	data, err := readRegularIndexDestination(destination.parent, destination.leaf)
	if err != nil || !bytes.Equal(data, destination.originalData) {
		return invalidIndexDestination(destination.output.relative, "destination content changed after preflight")
	}
	currentAfterRead, err := destination.parent.Lstat(destination.leaf)
	if err != nil ||
		currentAfterRead.Mode()&os.ModeSymlink != 0 ||
		!currentAfterRead.Mode().IsRegular() ||
		!os.SameFile(destination.original, currentAfterRead) ||
		indexPreservedMode(currentAfterRead.Mode()) != indexPreservedMode(destination.original.Mode()) {
		return invalidIndexDestination(destination.output.relative, "destination changed after preflight")
	}
	return nil
}

func verifyInstalledIndexDestination(destination *indexDestination) error {
	info, err := destination.parent.Lstat(destination.leaf)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		destination.publishedInfo == nil ||
		!os.SameFile(destination.publishedInfo, info) ||
		indexPreservedMode(info.Mode()) != indexPreservedMode(destination.publishedInfo.Mode()) {
		return invalidIndexDestination(destination.output.relative, "installed destination does not match stage")
	}
	data, err := readRegularIndexDestination(destination.parent, destination.leaf)
	if err != nil || !bytes.Equal(data, destination.output.data) {
		return invalidIndexDestination(destination.output.relative, "installed destination content does not match stage")
	}
	afterRead, err := destination.parent.Lstat(destination.leaf)
	if err != nil ||
		afterRead.Mode()&os.ModeSymlink != 0 ||
		!afterRead.Mode().IsRegular() ||
		destination.publishedInfo == nil ||
		!os.SameFile(destination.publishedInfo, afterRead) ||
		indexPreservedMode(afterRead.Mode()) != indexPreservedMode(destination.publishedInfo.Mode()) {
		return invalidIndexDestination(destination.output.relative, "installed destination changed while verifying")
	}
	return nil
}

func rollbackUnexpectedInstalledIndex(destination *indexDestination, hooks indexPublishHooks) error {
	if destination.vacateClaim != "" {
		if err := captureIndexBatchRollbackBaseline(
			destination.transactionScope,
			[]*indexDestination{destination},
		); err != nil {
			return err
		}
		return rollbackIndexBatchDestination(destination, hooks)
	}
	removed, removeErr := vacatePublishedIndexLeaf(destination, hooks)
	if !removed {
		if destination.backup != "" {
			return claimVerifiedIndexRecovery(
				destination,
				hooks,
				"installed destination cannot be safely removed",
				removeErr,
			)
		}
		return errors.Join(
			removeErr,
			fmt.Errorf(
				"rollback index destination %q: installed destination cannot be safely vacated",
				destination.output.relative,
			),
		)
	}
	return errors.Join(
		removeErr,
		restoreUnpublishedIndexDestination(destination, hooks),
	)
}

func verifyIndexDestinationParent(destination *indexDestination) error {
	current, err := openDirectory(destination.root, destination.directory)
	if err != nil {
		return invalidIndexDestination(destination.output.relative, "destination ancestor changed after preflight")
	}
	defer current.Close()
	info, err := current.Lstat(".")
	if err != nil || !info.IsDir() || !os.SameFile(destination.parentInfo, info) {
		return invalidIndexDestination(destination.output.relative, "destination ancestor changed after preflight")
	}
	return nil
}

func indexPublicationScopeValidator(
	destination *indexDestination,
) func(publicationScopePhase) error {
	return func(publicationScopePhase) error {
		if destination == nil ||
			destination.root == nil ||
			destination.parent == nil ||
			destination.parentInfo == nil {
			return publicationConflict("index publication scope is unavailable", nil)
		}
		if err := verifyIndexDestinationParent(destination); err != nil {
			return detachedIndexRecoveryError(destination, err)
		}
		return nil
	}
}

func indexTransactionArtifactScopeValidator(
	destination *indexDestination,
	transitionName string,
) func(publicationScopePhase) error {
	return func(phase publicationScopePhase) error {
		var transactionScope *indexBatchTransactionScope
		if destination != nil {
			transactionScope = destination.transactionScope
		}
		scope := []*indexDestination{destination}
		if transactionScope != nil && len(transactionScope.destinations) != 0 {
			scope = transactionScope.destinations
		}
		var softDestination *indexDestination
		for _, scoped := range scope {
			if err := indexPublicationScopeValidator(scoped)(phase); err != nil {
				return err
			}
			skipName := ""
			transition := false
			if scoped == destination {
				if phase != publicationScopeInstall ||
					transitionName == destination.newInstall ||
					transitionName == destination.restoreInstall {
					skipName = transitionName
				}
				transition = true
			}
			allowSoftEvidence := scoped == destination &&
				(verifyIndexBatchRollbackAnchor(destination) == nil ||
					destination.anchorSource != "" &&
						destination.anchorSource == destination.backup)
			if allowSoftEvidence {
				softDestination = destination
			}
			if err := verifyIndexTransactionArtifactsExcept(
				scoped,
				skipName,
				allowSoftEvidence,
			); err != nil {
				return err
			}
			if err := verifyIndexTransactionTargetState(
				scoped,
				phase,
				transition,
			); err != nil {
				return err
			}
		}
		if err := verifyIndexTransactionBatchManifest(
			transactionScope,
			destination,
			transitionName,
		); err != nil {
			return err
		}
		return validateIndexTransactionNamespace(
			transactionScope,
			destination,
			transitionName,
			phase,
			softDestination,
		)
	}
}

func verifyIndexTransactionBatchManifest(
	transactionScope *indexBatchTransactionScope,
	destination *indexDestination,
	transitionName string,
) error {
	if transactionScope == nil ||
		transactionScope.batch == nil ||
		!transactionScope.batch.manifestLive {
		return nil
	}
	batch := transactionScope.batch
	if destination == batch.root && transitionName == batch.name {
		return nil
	}
	if frozen := transactionScope.rollbackFrozenManifest; frozen != nil {
		if err := verifyIndexRollbackFrozenPath(
			batch.root.parent,
			batch.name,
			*frozen,
		); err != nil {
			return publicationConflict(
				"frozen index batch manifest changed during rollback",
				err,
			)
		}
		return nil
	}
	if batch.root == nil ||
		batch.root.parent == nil ||
		batch.spec.info == nil ||
		!batch.complete {
		return publicationConflict("index batch manifest scope is incomplete", nil)
	}
	if err := verifyCreatedIndexArtifactForCleanup(
		batch.root,
		batch.name,
		batch.spec.info,
		true,
		batch.data,
		indexBatchManifestMode,
	); err != nil {
		return publicationConflict("index batch manifest changed at publication barrier", err)
	}
	return nil
}

func validateIndexTransactionNamespace(
	transactionScope *indexBatchTransactionScope,
	destination *indexDestination,
	transitionName string,
	phase publicationScopePhase,
	softDestination *indexDestination,
) error {
	if transactionScope == nil || len(transactionScope.destinations) == 0 {
		return nil
	}
	root := transactionScope.destinations[0].root
	if root == nil {
		return publicationConflict("index transaction namespace root is unavailable", nil)
	}
	current, err := discoverPublicationNamespaces(
		context.Background(),
		root,
		[]publicationNamespaceRule{
			indexNamespaceRule(),
			privatePublicationNamespaceRule(),
		},
	)
	if err != nil {
		return err
	}
	expected := make(map[string]publicationNamespaceDiscovery)
	addExpected := func(scoped *indexDestination, name string, info os.FileInfo) {
		if scoped == nil || name == "" || info == nil {
			return
		}
		relative := pathJoin(scoped.directory, name)
		expected[relative] = publicationNamespaceDiscovery{
			directory:  scoped.directory,
			name:       name,
			relative:   relative,
			parentInfo: scoped.parentInfo,
			info:       info,
		}
	}
	for _, scoped := range transactionScope.destinations {
		if _, frozenPeer := transactionScope.rollbackPeers[scoped]; frozenPeer {
			for _, artifact := range indexRollbackPeerArtifacts(scoped) {
				frozen, present := transactionScope.rollbackFrozenPaths[pathJoin(scoped.directory, artifact.name)]
				if artifact.name == "" || !present || !frozen.present {
					continue
				}
				addExpected(scoped, artifact.name, frozen.info)
			}
			continue
		}
		addExpected(scoped, scoped.stage, scoped.stageInfo)
		if !scoped.newInstallConsumed {
			addExpected(scoped, scoped.newInstall, scoped.newInstallInfo)
		}
		if !scoped.restoreInstallConsumed {
			addExpected(scoped, scoped.restoreInstall, scoped.restoreInstallInfo)
		}
		addExpected(scoped, scoped.discard, scoped.discardInfo)
		addExpected(scoped, scoped.restoreStage, scoped.restoreInfo)
		addExpected(scoped, scoped.backup, scoped.backupInfo)
		addExpected(scoped, scoped.witness, scoped.witnessInfo)
		addExpected(scoped, scoped.anchor, scoped.anchorInfo)
		addExpected(scoped, scoped.rollbackClaim, scoped.rollbackInfo)
	}
	if batch := transactionScope.batch; batch != nil &&
		batch.manifestLive &&
		batch.root != nil {
		if frozen := transactionScope.rollbackFrozenManifest; frozen != nil {
			if frozen.present {
				addExpected(batch.root, batch.name, frozen.info)
			}
		} else if batch.spec.info != nil {
			addExpected(batch.root, batch.name, batch.spec.info)
		}
	}

	transitionRelative := ""
	if destination != nil && transitionName != "" {
		transitionRelative = pathJoin(destination.directory, transitionName)
	}
	seen := make(map[string]struct{}, len(expected))
	var transitionInfo os.FileInfo
	for _, discovery := range current {
		if observation, present := expected[discovery.relative]; present {
			if !os.SameFile(observation.parentInfo, discovery.parentInfo) ||
				!samePublicationEntryObservation(observation.info, discovery.info) {
				if isSoftIndexRollbackEvidencePath(
					softDestination,
					discovery.relative,
				) {
					seen[discovery.relative] = struct{}{}
					continue
				}
				if isCurrentIndexStagePath(destination, discovery.relative) {
					return invalidIndexDestination(
						destination.output.relative,
						"staged file changed before publication",
					)
				}
				return publicationConflict(
					"index transaction namespace observation changed at publication barrier",
					nil,
				)
			}
			seen[discovery.relative] = struct{}{}
			continue
		}
		allowTransition := phase == publicationScopeCreate ||
			phase == publicationScopeVacate ||
			phase == publicationScopeRemove
		if !allowTransition ||
			destination == nil ||
			discovery.directory != destination.directory ||
			transitionName == "" ||
			transitionInfo != nil {
			return publicationConflict(
				"index transaction namespace gained foreign evidence at publication barrier",
				nil,
			)
		}
		privateDigest, private := parsePrivatePublicationClaimName(discovery.name)
		if discovery.relative != transitionRelative &&
			(!private || !privateClaimBindsName(privateDigest, transitionName)) {
			return publicationConflict(
				"index transaction namespace gained an unrelated private claim",
				nil,
			)
		}
		if discovery.info == nil ||
			discovery.info.Mode()&os.ModeSymlink != 0 ||
			!discovery.info.Mode().IsRegular() {
			return publicationConflict(
				"index transaction namespace transition is not regular",
				nil,
			)
		}
		transitionInfo = discovery.info
	}
	for relative, observation := range expected {
		if _, present := seen[relative]; present {
			continue
		}
		if isSoftIndexRollbackEvidencePath(softDestination, relative) {
			continue
		}
		if (phase == publicationScopeRemove || phase == publicationScopeInstall) &&
			relative == transitionRelative {
			if transitionInfo != nil &&
				(observation.info == nil ||
					!os.SameFile(observation.info, transitionInfo)) {
				return publicationConflict(
					"index transaction cleanup quarantine detached from evidence",
					nil,
				)
			}
			continue
		}
		if isCurrentIndexStagePath(destination, relative) {
			return invalidIndexDestination(
				destination.output.relative,
				"staged file changed before publication",
			)
		}
		return publicationConflict(
			"index transaction namespace lost evidence at publication barrier",
			nil,
		)
	}
	if phase == publicationScopeVacate && transitionInfo != nil {
		var expectedTarget os.FileInfo
		if destination.published {
			expectedTarget = destination.publishedInfo
		} else {
			expectedTarget = destination.original
		}
		if expectedTarget == nil || !os.SameFile(expectedTarget, transitionInfo) {
			return publicationConflict(
				"index transaction vacate claim detached from target evidence",
				nil,
			)
		}
	}
	return nil
}

func isCurrentIndexStagePath(
	destination *indexDestination,
	relative string,
) bool {
	return destination != nil &&
		destination.stage != "" &&
		pathJoin(destination.directory, destination.stage) == relative
}

func isSoftIndexRollbackEvidencePath(
	destination *indexDestination,
	relative string,
) bool {
	if destination == nil {
		return false
	}
	names := []string{
		destination.stage,
		destination.backup,
		destination.witness,
		destination.restoreStage,
	}
	if destination.anchorSource != "" {
		if destination.anchorSource != destination.backup {
			return false
		}
		names = []string{
			destination.witness,
			destination.restoreStage,
		}
	}
	for _, name := range names {
		if name == "" ||
			name == destination.anchorSource ||
			pathJoin(destination.directory, name) != relative {
			continue
		}
		info, err := destination.parent.Lstat(name)
		return err == nil &&
			info.Mode()&os.ModeSymlink == 0 &&
			info.Mode().IsRegular()
	}
	return false
}

func indexBatchRollbackScopeValidator(
	destination *indexDestination,
	expectPublished bool,
	evidenceErr *error,
) func(publicationScopePhase) error {
	return func(phase publicationScopePhase) error {
		var transactionScope *indexBatchTransactionScope
		if destination != nil {
			transactionScope = destination.transactionScope
		}
		scope := []*indexDestination{destination}
		if transactionScope != nil && len(transactionScope.destinations) != 0 {
			scope = transactionScope.destinations
		}
		for _, scoped := range scope {
			if err := indexPublicationScopeValidator(scoped)(phase); err != nil {
				return err
			}
			if scoped != destination {
				if err := verifyIndexTransactionArtifactsExcept(scoped, "", false); err != nil {
					return err
				}
				if err := verifyIndexTransactionTargetState(
					scoped,
					publicationScopeStable,
					false,
				); err != nil {
					return err
				}
				continue
			}
			if scoped.existed {
				if err := verifyIndexBatchRollbackAnchor(scoped); err != nil {
					return err
				}
			}
			if observed := verifyIndexBatchSoftEvidence(scoped); observed != nil &&
				evidenceErr != nil {
				*evidenceErr = errors.Join(*evidenceErr, observed)
			}
			if err := verifyIndexBatchRollbackTargetAtBarrier(
				scoped,
				phase,
				expectPublished,
			); err != nil {
				return err
			}
		}
		if err := verifyIndexTransactionBatchManifest(transactionScope, nil, ""); err != nil {
			return err
		}
		var namespaceDestination *indexDestination
		namespaceTransition := ""
		switch phase {
		case publicationScopeVacate:
			namespaceDestination = destination
			namespaceTransition = destination.discard
		case publicationScopeInstall:
			namespaceDestination = destination
			namespaceTransition = destination.restoreInstall
		case publicationScopeRemove:
			namespaceDestination = destination
			namespaceTransition = destination.leaf
		}
		return validateIndexTransactionNamespace(
			transactionScope,
			namespaceDestination,
			namespaceTransition,
			phase,
			destination,
		)
	}
}

func verifyIndexBatchRollbackTargetAtBarrier(
	destination *indexDestination,
	phase publicationScopePhase,
	expectPublished bool,
) error {
	if phase == publicationScopeInstall {
		if _, err := destination.parent.Lstat(destination.leaf); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if hasExactIndexBatchRollbackTarget(destination) {
			return nil
		}
		return invalidIndexDestination(
			destination.output.relative,
			"rollback target changed at install barrier",
		)
	}
	if phase == publicationScopeVacate || phase == publicationScopeRemove {
		if _, err := destination.parent.Lstat(destination.leaf); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if expectPublished {
			return verifyInstalledIndexDestination(destination)
		}
	}
	if expectPublished {
		return verifyInstalledIndexDestination(destination)
	}
	if hasExactIndexBatchRollbackTarget(destination) {
		return nil
	}
	if _, err := destination.parent.Lstat(destination.leaf); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return invalidIndexDestination(
		destination.output.relative,
		"rollback target changed at publication barrier",
	)
}

func verifyIndexTransactionArtifactsExcept(
	destination *indexDestination,
	skipName string,
	allowSoftEvidence bool,
) error {
	if destination != nil && destination.transactionScope != nil {
		scope := destination.transactionScope
		if _, frozenPeer := scope.rollbackPeers[destination]; frozenPeer {
			for _, artifact := range indexRollbackPeerArtifacts(destination) {
				if artifact.name == "" {
					continue
				}
				frozen, present := scope.rollbackFrozenPaths[pathJoin(destination.directory, artifact.name)]
				if !present {
					return publicationConflict(
						"frozen index rollback peer evidence is unavailable",
						nil,
					)
				}
				if err := verifyIndexRollbackFrozenPath(
					destination.parent,
					artifact.name,
					frozen,
				); err != nil {
					return publicationConflict(
						"frozen index rollback peer evidence changed",
						err,
					)
				}
			}
			return nil
		}
	}
	verify := func(
		name string,
		info os.FileInfo,
		complete bool,
		data []byte,
		mode fs.FileMode,
	) error {
		if name == "" || name == skipName {
			return nil
		}
		if info == nil {
			if !complete {
				return nil
			}
			return invalidIndexDestination(
				destination.output.relative,
				"transaction artifact identity is unavailable at publication barrier",
			)
		}
		return verifyCreatedIndexArtifactForCleanup(
			destination,
			name,
			info,
			complete,
			data,
			mode,
		)
	}
	if err := verify(
		destination.stage,
		destination.stageInfo,
		destination.stageComplete,
		destination.output.data,
		indexPreservedMode(destination.stageInfoMode()),
	); err != nil {
		softRegular := allowSoftEvidence &&
			destination.stage != destination.anchorSource &&
			isSoftIndexRollbackEvidencePath(
				destination,
				pathJoin(destination.directory, destination.stage),
			)
		if !softRegular {
			return errors.Join(
				invalidIndexDestination(
					destination.output.relative,
					"staged file changed before publication",
				),
				err,
			)
		}
	}
	if !destination.newInstallConsumed {
		if err := verify(
			destination.newInstall,
			destination.newInstallInfo,
			destination.newInstallComplete,
			destination.output.data,
			indexPreservedMode(destination.stageInfoMode()),
		); err != nil {
			return errors.Join(
				invalidIndexDestination(
					destination.output.relative,
					"new install token changed before publication",
				),
				err,
			)
		}
	}
	originalMode := fs.FileMode(0)
	if destination.original != nil {
		originalMode = indexPreservedMode(destination.original.Mode())
	}
	if !destination.restoreInstallConsumed {
		if err := verify(
			destination.restoreInstall,
			destination.restoreInstallInfo,
			destination.restoreInstallComplete,
			destination.originalData,
			originalMode,
		); err != nil {
			return invalidIndexDestination(
				destination.output.relative,
				"restore install token changed at publication barrier",
			)
		}
	}
	if destination.discardInfo != nil || destination.discardComplete {
		if err := verify(
			destination.discard,
			destination.discardInfo,
			destination.discardComplete,
			destination.output.data,
			indexPreservedMode(destination.stageInfoMode()),
		); err != nil {
			return invalidIndexDestination(
				destination.output.relative,
				"discard token changed at publication barrier",
			)
		}
	}
	for _, artifact := range []struct {
		name     string
		info     os.FileInfo
		complete bool
		soft     bool
	}{
		{destination.restoreStage, destination.restoreInfo, destination.restoreComplete, true},
		{destination.backup, destination.backupInfo, destination.backupComplete, true},
		{destination.witness, destination.witnessInfo, destination.witnessComplete, true},
		{destination.anchor, destination.anchorInfo, destination.anchorComplete, false},
	} {
		if err := verify(
			artifact.name,
			artifact.info,
			artifact.complete,
			destination.originalData,
			originalMode,
		); err != nil {
			softRegular := allowSoftEvidence &&
				artifact.soft &&
				artifact.name != destination.anchorSource &&
				isSoftIndexRollbackEvidencePath(
					destination,
					pathJoin(destination.directory, artifact.name),
				)
			if !softRegular {
				return err
			}
		}
	}
	if destination.rollbackClaim != "" {
		data := destination.output.data
		if destination.rollbackKind == indexArtifactRestore {
			data = destination.originalData
		}
		mode := fs.FileMode(0)
		if destination.rollbackInfo != nil {
			mode = indexPreservedMode(destination.rollbackInfo.Mode())
		}
		if err := verify(
			destination.rollbackClaim,
			destination.rollbackInfo,
			destination.rollbackComplete,
			data,
			mode,
		); err != nil {
			return err
		}
	}
	return nil
}

func verifyIndexTransactionTargetState(
	destination *indexDestination,
	phase publicationScopePhase,
	transition bool,
) error {
	if destination != nil && destination.transactionScope != nil {
		if frozen, present := destination.transactionScope.
			rollbackFrozenTargets[destination]; present {
			if err := verifyIndexRollbackFrozenPath(
				destination.parent,
				destination.leaf,
				frozen,
			); err != nil {
				return invalidIndexDestination(
					destination.output.relative,
					"frozen rollback target changed",
				)
			}
			return nil
		}
	}
	if destination.published {
		return verifyInstalledIndexDestination(destination)
	}
	if hasExactIndexBatchRollbackTarget(destination) {
		return nil
	}
	if destination.rollbackTargetInfo != nil {
		current, err := capturePublicationFile(
			context.Background(),
			destination.parent,
			destination.leaf,
		)
		if err == nil &&
			current.info != nil &&
			os.SameFile(destination.rollbackTargetInfo, current.info) &&
			current.mode == indexPreservedMode(destination.original.Mode()) &&
			bytes.Equal(current.data, destination.originalData) {
			return nil
		}
	}
	if err := verifyOriginalIndexDestination(destination); err == nil {
		return nil
	}
	if destination.stageInfo != nil {
		current, err := capturePublicationFile(
			context.Background(),
			destination.parent,
			destination.leaf,
		)
		if err == nil &&
			current.info != nil &&
			os.SameFile(destination.stageInfo, current.info) &&
			current.mode == indexPreservedMode(destination.stageInfo.Mode()) &&
			bytes.Equal(current.data, destination.output.data) {
			return nil
		}
	}
	if transition &&
		(phase == publicationScopeVacate || phase == publicationScopeInstall) {
		if _, err := destination.parent.Lstat(destination.leaf); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
	}
	if destination.restoreStage != "" || !destination.existed {
		if _, err := destination.parent.Lstat(destination.leaf); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
	}
	return invalidIndexDestination(
		destination.output.relative,
		"transaction target changed at publication barrier",
	)
}

func readRegularIndexDestination(parent *os.Root, leaf string) ([]byte, error) {
	file, err := openRegularFile(parent, leaf)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return ioReadAllContext(context.Background(), file)
}

func restoreUnpublishedIndexDestination(destination *indexDestination, hooks indexPublishHooks) error {
	if destination.backup == "" && destination.existed {
		if err := ensureVerifiedIndexRecoveryBackup(destination, hooks); err != nil {
			return err
		}
	}
	if destination.backup == "" {
		if _, err := destination.parent.Lstat(destination.leaf); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err == nil {
			return fmt.Errorf("rollback index destination %q: destination is occupied", destination.output.relative)
		} else {
			return indexPublicationStepError{
				destination: destination.output.relative,
				action:      "inspect destination during rollback",
				err:         err,
			}
		}
	}
	if err := ensureVerifiedIndexRecoveryBackup(destination, hooks); err != nil {
		return err
	}
	if _, err := destination.parent.Lstat(destination.leaf); err == nil {
		return claimVerifiedIndexRecovery(
			destination,
			hooks,
			"restoration blocked by occupied destination",
			nil,
		)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return claimVerifiedIndexRecovery(
			destination,
			hooks,
			"destination cannot be inspected for restoration",
			err,
		)
	}
	if hooks.beforeRestore != nil {
		scopeValidator := indexPublicationScopeValidator(destination)
		if destination.transactionScope != nil {
			scopeValidator = indexTransactionArtifactScopeValidator(destination, "")
		}
		hookErr := runPublicationBarrier(
			publicationBarrierHooks{
				scopePhase:    publicationScopeStable,
				validateScope: scopeValidator,
			},
			func() error {
				return hooks.beforeRestore(
					destination.output.relative,
					destination.parent,
					destination.leaf,
					destination.backup,
				)
			},
		)
		if hookErr != nil {
			if errors.Is(hookErr, errPublicationScopeInvalid) {
				return hookErr
			}
			return claimVerifiedIndexRecovery(
				destination,
				hooks,
				"restoration was interrupted",
				hookErr,
			)
		}
	}
	if err := ensureVerifiedIndexRecoveryBackup(destination, hooks); err != nil {
		return err
	}
	if err := verifyIndexDestinationParent(destination); err != nil {
		return detachedIndexRecoveryError(destination, err)
	}
	if _, err := destination.parent.Lstat(destination.leaf); !errors.Is(err, fs.ErrNotExist) {
		return claimVerifiedIndexRecovery(
			destination,
			hooks,
			"restoration blocked after final parent verification",
			err,
		)
	}
	restoreStage := destination.restoreStage
	restoreInfo := destination.restoreInfo
	if restoreStage == "" || verifyCreatedIndexArtifactForCleanup(
		destination,
		restoreStage,
		restoreInfo,
		destination.restoreComplete,
		destination.originalData,
		indexPreservedMode(destination.original.Mode()),
	) != nil {
		if destination.vacateClaim != "" {
			restoreStage = destination.backup
			restoreInfo = destination.backupInfo
		} else {
			var err error
			restoreStage, restoreInfo, err = stageIndexRecoveryCopy(destination, hooks)
			if err != nil {
				return claimVerifiedIndexRecovery(
					destination,
					hooks,
					"recovery copy could not be staged",
					err,
				)
			}
			destination.restoreStage = restoreStage
		}
	}
	installed, err := guardedInstallPublicationLeaf(
		context.Background(),
		destination.parent,
		restoreStage,
		publicationSpecFromBytes(restoreInfo, destination.originalData),
		destination.leaf,
		indexRestoreInstallCoreHooks(
			destination,
			indexArtifactRestore,
			restoreStage,
			hooks,
		),
	)
	if err != nil {
		return claimVerifiedIndexRecovery(
			destination,
			hooks,
			"original could not be restored",
			err,
		)
	}
	if !installed {
		return claimVerifiedIndexRecovery(
			destination,
			hooks,
			"original recovery copy was not installed",
			nil,
		)
	}
	if err := verifyRestoredIndexDestination(destination, restoreInfo); err != nil {
		return claimVerifiedIndexRecovery(
			destination,
			hooks,
			"restored destination could not be verified",
			err,
		)
	}
	removed, err := removeCreatedIndexArtifact(
		destination,
		hooks,
		"recovery stage",
		restoreStage,
		destination.restoreInfo,
		destination.restoreComplete,
		destination.originalData,
		indexPreservedMode(destination.original.Mode()),
	)
	if removed {
		destination.restoreStage = ""
		destination.restoreInfo = nil
		destination.restoreComplete = false
	}
	if err != nil {
		return claimVerifiedIndexRecovery(
			destination,
			hooks,
			"recovery stage could not be removed",
			err,
		)
	}
	if err := verifyRestoredIndexDestination(destination, restoreInfo); err != nil {
		return claimVerifiedIndexRecovery(
			destination,
			hooks,
			"restored destination changed after staging",
			err,
		)
	}
	if destination.preserveBackup {
		return nil
	}
	removed, err = removeCreatedIndexArtifact(
		destination,
		hooks,
		"recovery backup",
		destination.backup,
		destination.backupInfo,
		destination.backupComplete,
		destination.originalData,
		indexPreservedMode(destination.original.Mode()),
	)
	if removed {
		destination.backup = ""
		destination.backupInfo = nil
		destination.backupComplete = false
	}
	if err != nil {
		return claimVerifiedIndexRecovery(
			destination,
			hooks,
			"verified backup could not be cleaned up",
			err,
		)
	}
	destination.preserveBackup = false
	return nil
}

func restoreFailedIndexPublication(
	destination *indexDestination,
	hooks indexPublishHooks,
) error {
	if destination.witness != "" {
		// A v3 manifest binds the witness, claim, backup, and rollback anchor.
		// Once the original has been vacated, restoration must install only the
		// independent anchor so the public target retains an exact durable
		// identity proof while C/B/W remain immutable recovery evidence.
		if err := captureIndexBatchRollbackBaseline(
			destination.transactionScope,
			[]*indexDestination{destination},
		); err != nil {
			return err
		}
		err := rollbackIndexBatchDestination(destination, hooks)
		if err != nil {
			// The current rollback boundary has already been attempted. The
			// outer batch loop may still roll back the preceding prefix, but
			// must not repeat this destination action or clean its evidence.
			destination.preserveBackup = true
		}
		return err
	}
	return restoreUnpublishedIndexDestination(destination, hooks)
}

func createIndexRecoveryBackup(
	destination *indexDestination,
	hooks indexPublishHooks,
) error {
	mode := indexPreservedMode(destination.original.Mode())
	template, err := newIndexArtifactNameTemplate(
		indexArtifactBackup,
		destination.output.relative,
		mode,
		destination.originalData,
	)
	if err != nil {
		return err
	}
	source := publicationSpecFromBytes(destination.original, destination.originalData)
	for slot := 0; slot < 10_000; slot++ {
		name := template.Name(slot)
		spec, copyErr := createDurablePublicationCopy(
			context.Background(),
			destination.parent,
			destination.leaf,
			source,
			name,
			indexArtifactCoreHooks(
				destination,
				indexArtifactBackup,
				name,
				hooks,
			),
		)
		if copyErr == nil {
			destination.backup = name
			destination.backupInfo = spec.info
			destination.backupComplete = spec.complete
			return verifyIndexBackup(destination)
		}
		if spec.created {
			destination.backup = name
			destination.backupInfo = spec.info
			destination.backupComplete = spec.complete
			return indexPublicationStepError{
				destination: destination.output.relative,
				action:      "create independent durable recovery backup",
				err:         copyErr,
			}
		}
		if !errors.Is(copyErr, fs.ErrExist) {
			return indexPublicationStepError{
				destination: destination.output.relative,
				action:      "create independent durable recovery backup",
				err:         copyErr,
			}
		}
	}
	return indexPublicationStepError{
		destination: destination.output.relative,
		action:      "create independent durable recovery backup",
		err:         errors.New("transaction artifact slots exhausted"),
	}
}

func verifyIndexBackup(destination *indexDestination) error {
	info, err := destination.parent.Lstat(destination.backup)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		!os.SameFile(destination.backupInfo, info) ||
		os.SameFile(destination.original, info) ||
		indexPreservedMode(info.Mode()) != indexPreservedMode(destination.original.Mode()) {
		return invalidIndexDestination(
			destination.output.relative,
			"recovery backup is not an independent copy of the original",
		)
	}
	data, err := readRegularIndexDestination(destination.parent, destination.backup)
	if err != nil || !bytes.Equal(data, destination.originalData) {
		return invalidIndexDestination(destination.output.relative, "recovery backup content does not match original")
	}
	afterRead, err := destination.parent.Lstat(destination.backup)
	if err != nil ||
		afterRead.Mode()&os.ModeSymlink != 0 ||
		!afterRead.Mode().IsRegular() ||
		!os.SameFile(destination.backupInfo, afterRead) ||
		indexPreservedMode(afterRead.Mode()) != indexPreservedMode(destination.original.Mode()) {
		return invalidIndexDestination(destination.output.relative, "recovery backup changed while verifying")
	}
	return nil
}

func stageIndexRecoveryCopy(
	destination *indexDestination,
	hooks indexPublishHooks,
) (string, os.FileInfo, error) {
	mode := indexPreservedMode(destination.original.Mode())
	name, spec, complete, err := createDurableIndexArtifact(
		context.Background(),
		destination,
		indexArtifactRestore,
		mode,
		destination.originalData,
		hooks,
	)
	destination.restoreStage = name
	destination.restoreInfo = spec.info
	destination.restoreComplete = complete
	if err != nil {
		return name, spec.info, err
	}
	return name, spec.info, nil
}

func verifyRestoredIndexDestination(destination *indexDestination, restoreInfo os.FileInfo) error {
	info, err := destination.parent.Lstat(destination.leaf)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		!os.SameFile(restoreInfo, info) ||
		indexPreservedMode(info.Mode()) != indexPreservedMode(destination.original.Mode()) {
		return invalidIndexDestination(destination.output.relative, "restored destination does not match original")
	}
	data, err := readRegularIndexDestination(destination.parent, destination.leaf)
	if err != nil || !bytes.Equal(data, destination.originalData) {
		return invalidIndexDestination(destination.output.relative, "restored destination content does not match original")
	}
	afterRead, err := destination.parent.Lstat(destination.leaf)
	if err != nil ||
		afterRead.Mode()&os.ModeSymlink != 0 ||
		!afterRead.Mode().IsRegular() ||
		!os.SameFile(restoreInfo, afterRead) ||
		indexPreservedMode(afterRead.Mode()) != indexPreservedMode(destination.original.Mode()) {
		return invalidIndexDestination(destination.output.relative, "restored destination changed while verifying")
	}
	return nil
}

func rollbackIndexDestinations(destinations []*indexDestination, hooks indexPublishHooks) error {
	var rollbackErr error
	for index := len(destinations) - 1; index >= 0; index-- {
		rollbackErr = errors.Join(rollbackErr, rollbackIndexDestination(destinations[index], hooks))
	}
	return rollbackErr
}

func rollbackIndexDestination(destination *indexDestination, hooks indexPublishHooks) error {
	if !destination.published {
		return restoreUnpublishedIndexDestination(destination, hooks)
	}
	if parentErr := verifyIndexDestinationParent(destination); parentErr != nil {
		removed, removeErr := removePublishedIndexLeafIfOwned(destination)
		if removed {
			destination.published = false
		}
		if destination.existed {
			recoveryErr := claimVerifiedIndexRecovery(
				destination,
				hooks,
				"destination parent detached before rollback",
				errors.Join(parentErr, removeErr),
			)
			return errors.Join(parentErr, removeErr, recoveryErr)
		}
		return errors.Join(parentErr, removeErr)
	}
	removed, removeErr := vacatePublishedIndexLeaf(destination, hooks)
	if !removed {
		if destination.existed {
			return claimVerifiedIndexRecovery(
				destination,
				hooks,
				"published destination could not be safely vacated",
				removeErr,
			)
		}
		return errors.Join(
			removeErr,
			fmt.Errorf("rollback index destination %q: published destination changed", destination.output.relative),
		)
	}
	return errors.Join(
		removeErr,
		restoreUnpublishedIndexDestination(destination, hooks),
	)
}

func removePublishedIndexLeafIfOwned(destination *indexDestination) (bool, error) {
	return vacatePublishedIndexLeaf(destination, indexPublishHooks{})
}

func vacatePublishedIndexLeaf(
	destination *indexDestination,
	hooks indexPublishHooks,
) (bool, error) {
	if _, err := destination.parent.Lstat(destination.leaf); errors.Is(err, fs.ErrNotExist) {
		destination.published = false
		return true, nil
	}
	if destination.publishedInfo == nil {
		return false, invalidIndexDestination(
			destination.output.relative,
			"published destination identity is unavailable during rollback",
		)
	}
	current, err := capturePublicationFile(
		context.Background(),
		destination.parent,
		destination.leaf,
	)
	if err != nil || current.info == nil || !os.SameFile(destination.publishedInfo, current.info) {
		return false, errors.Join(
			err,
			invalidIndexDestination(
				destination.output.relative,
				"published destination identity changed during rollback",
			),
		)
	}
	expected := publicationSpecFromBytes(destination.publishedInfo, destination.output.data)
	complete := current.mode == expected.mode && bytes.Equal(current.data, expected.data)
	claimKind := indexArtifactStage
	if !complete {
		claimKind = indexArtifactRestore
	}
	claimName, err := allocateIndexArtifactClaimName(
		destination.parent,
		claimKind,
		destination.output.relative,
		current.mode,
		current.data,
	)
	if err != nil {
		return false, err
	}
	claimed, vacateErr := guardedVacatePublicationLeafIdentity(
		context.Background(),
		destination.parent,
		destination.leaf,
		destination.publishedInfo,
		claimName,
		publicationBarrierHooks{
			validateScope: indexTransactionArtifactScopeValidator(
				destination,
				claimName,
			),
			beforeVacate: func() error {
				if hooks.beforeVacate == nil {
					return nil
				}
				return hooks.beforeVacate(
					destination.output.relative,
					destination.parent,
					destination.leaf,
				)
			},
			afterVacateRename: func() error {
				if hooks.afterVacateRename == nil {
					return nil
				}
				return hooks.afterVacateRename(
					destination.output.relative,
					destination.parent,
					destination.leaf,
					claimName,
				)
			},
			afterVacate: func() error {
				if hooks.afterVacate == nil {
					return nil
				}
				return hooks.afterVacate(
					destination.output.relative,
					destination.parent,
					destination.leaf,
					claimName,
				)
			},
		},
	)
	mutated := claimed.created
	if mutated {
		destination.published = false
		destination.rollbackClaim = claimName
		destination.rollbackInfo = claimed.info
		destination.rollbackComplete = claimed.info != nil &&
			complete &&
			claimed.mode == expected.mode &&
			bytes.Equal(claimed.data, expected.data)
		destination.rollbackKind = claimKind
		if claimed.info == nil {
			recaptured, captureErr := capturePublicationFile(
				context.Background(),
				destination.parent,
				claimName,
			)
			if captureErr == nil && recaptured.info != nil {
				destination.rollbackInfo = recaptured.info
				destination.rollbackComplete =
					complete &&
						os.SameFile(destination.publishedInfo, recaptured.info) &&
						recaptured.mode == expected.mode &&
						bytes.Equal(recaptured.data, expected.data)
			}
		}
	}
	if mutated && !complete {
		vacateErr = errors.Join(
			vacateErr,
			recoverableIndexBackupError{
				destination: destination.output.relative,
				backup:      pathJoin(destination.directory, claimName),
				reason:      "changed or unsafe; left in place: artifact content changed; same-inode published destination was tampered, original restored, and tampered recovery evidence retained",
			},
		)
	}
	return mutated, vacateErr
}

func allocateIndexArtifactClaimName(
	parent *os.Root,
	kind indexArtifactKind,
	relative string,
	mode fs.FileMode,
	data []byte,
) (string, error) {
	template, err := newIndexArtifactNameTemplate(kind, relative, mode, data)
	if err != nil {
		return "", err
	}
	for slot := 0; slot < 10_000; slot++ {
		name := template.Name(slot)
		if _, err := parent.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			return name, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("index ownership claim namespace exhausted")
}

type indexPublicationStepError struct {
	destination string
	action      string
	err         error
}

func (e indexPublicationStepError) Error() string {
	return fmt.Sprintf("publish index destination %q: %s", e.destination, e.action)
}

func (e indexPublicationStepError) Unwrap() error {
	return e.err
}

type recoverableIndexBackupError struct {
	destination string
	backup      string
	reason      string
	err         error
}

func (e recoverableIndexBackupError) Error() string {
	message := fmt.Sprintf(
		"rollback index destination %q: %s; original preserved at %q",
		e.destination,
		e.reason,
		e.backup,
	)
	if e.err != nil {
		message += ": " + e.err.Error()
	}
	return message
}

func (e recoverableIndexBackupError) Unwrap() error {
	return e.err
}

type unrecoverableIndexRecoveryError struct {
	destination string
	artifact    string
	reason      string
	err         error
}

func (e unrecoverableIndexRecoveryError) Error() string {
	if e.artifact == "" {
		return fmt.Sprintf(
			"index recovery for destination %q is unrecoverable: %s",
			e.destination,
			e.reason,
		)
	}
	return fmt.Sprintf(
		"index recovery for destination %q is unrecoverable: artifact %q: %s",
		e.destination,
		e.artifact,
		e.reason,
	)
}

func (e unrecoverableIndexRecoveryError) Unwrap() error {
	return e.err
}

func claimVerifiedIndexRecovery(
	destination *indexDestination,
	hooks indexPublishHooks,
	reason string,
	err error,
) error {
	if hooks.beforeRecoveryClaim != nil {
		err = errors.Join(
			err,
			hooks.beforeRecoveryClaim(
				destination.output.relative,
				destination.parent,
				destination.backup,
			),
		)
	}
	if verifyErr := ensureVerifiedIndexRecoveryBackup(destination, hooks); verifyErr != nil {
		return errors.Join(err, verifyErr)
	}
	destination.preserveBackup = true
	if parentErr := verifyIndexDestinationParent(destination); parentErr != nil {
		return errors.Join(err, detachedIndexRecoveryError(destination, parentErr))
	}
	if verifyErr := ensureVerifiedIndexRecoveryBackup(destination, hooks); verifyErr != nil {
		return errors.Join(err, verifyErr)
	}
	if parentErr := verifyIndexDestinationParent(destination); parentErr != nil {
		return errors.Join(err, detachedIndexRecoveryError(destination, parentErr))
	}
	return recoverableIndexBackupError{
		destination: destination.output.relative,
		backup:      indexBackupRelativePath(destination),
		reason:      reason,
		err:         err,
	}
}

func ensureVerifiedIndexRecoveryBackup(
	destination *indexDestination,
	hooks indexPublishHooks,
) error {
	if destination.backup == "" {
		if err := createIndexRecoveryBackup(destination, hooks); err != nil {
			return unrecoverableIndexRecoveryError{
				destination: destination.output.relative,
				reason:      "verified recovery artifact could not be created from the pinned original snapshot",
				err:         err,
			}
		}
		return nil
	}
	if err := verifyIndexBackup(destination); err == nil {
		return nil
	} else {
		corruptBackup := indexBackupRelativePath(destination)
		destination.backup = ""
		destination.backupInfo = nil
		destination.backupComplete = false
		destination.preserveBackup = false
		if createErr := createIndexRecoveryBackup(destination, hooks); createErr != nil {
			return unrecoverableIndexRecoveryError{
				destination: destination.output.relative,
				artifact:    corruptBackup,
				reason:      "corrupt recovery artifact could not be replaced from the pinned original snapshot",
				err:         errors.Join(err, createErr),
			}
		}
		if verifyErr := verifyIndexBackup(destination); verifyErr != nil {
			return unrecoverableIndexRecoveryError{
				destination: destination.output.relative,
				artifact:    corruptBackup,
				reason:      "replacement recovery artifact could not be verified",
				err:         errors.Join(err, verifyErr),
			}
		}
		return nil
	}
}

func indexBackupRelativePath(destination *indexDestination) string {
	return pathJoin(destination.directory, destination.backup)
}

func indexBackupNameFor(relative string, mode fs.FileMode, data []byte, slot int) string {
	return indexArtifactNameFor(indexArtifactBackup, relative, mode, data, slot)
}

func indexArtifactNameFor(
	kind indexArtifactKind,
	relative string,
	mode fs.FileMode,
	data []byte,
	slot int,
) string {
	name, _ := indexArtifactNameForChecked(kind, relative, mode, data, slot)
	return name
}

func indexArtifactNameForChecked(
	kind indexArtifactKind,
	relative string,
	mode fs.FileMode,
	data []byte,
	slot int,
) (string, error) {
	template, err := newIndexArtifactNameTemplate(kind, relative, mode, data)
	if err != nil {
		return "", err
	}
	return template.Name(slot), nil
}

type indexArtifactNameTemplate struct {
	prefix string
	digest string
}

func newIndexArtifactNameTemplate(
	kind indexArtifactKind,
	relative string,
	mode fs.FileMode,
	data []byte,
) (indexArtifactNameTemplate, error) {
	prefix, err := indexArtifactPrefix(kind)
	if err != nil {
		return indexArtifactNameTemplate{}, err
	}
	return indexArtifactNameTemplate{
		prefix: prefix,
		digest: indexArtifactDigest(kind, relative, mode, data),
	}, nil
}

func (template indexArtifactNameTemplate) Name(slot int) string {
	return fmt.Sprintf(
		"%s%04d-%s",
		template.prefix,
		slot,
		template.digest,
	)
}

func indexArtifactDigest(
	kind indexArtifactKind,
	relative string,
	mode fs.FileMode,
	data []byte,
) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("okf:index-artifact:v1\x00"))
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(cleanIndexDestinationPath(relative)))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(strconv.FormatUint(uint64(indexPreservedMode(mode)), 8)))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(data)
	return hex.EncodeToString(digest.Sum(nil))
}

type unknownIndexArtifactKindError struct{ kind indexArtifactKind }

func (err unknownIndexArtifactKindError) Error() string {
	return fmt.Sprintf("unknown index transaction artifact kind %q", err.kind)
}

func indexArtifactPrefix(kind indexArtifactKind) (string, error) {
	switch kind {
	case indexArtifactBackup:
		return indexRecoveryBackupPrefix, nil
	case indexArtifactStage:
		return indexStagePrefix, nil
	case indexArtifactRestore:
		return indexRestorePrefix, nil
	case indexArtifactAnchor:
		return indexAnchorPrefix, nil
	case indexArtifactWitness:
		return indexWitnessPrefix, nil
	case indexArtifactNewInstall:
		return indexNewInstallPrefix, nil
	case indexArtifactRestoreInstall:
		return indexRestoreInstallPrefix, nil
	case indexArtifactDiscard:
		return indexDiscardPrefix, nil
	case indexArtifactManifest:
		return indexBatchManifestPrefix, nil
	default:
		return "", unknownIndexArtifactKindError{kind: kind}
	}
}

func createDurableIndexArtifact(
	ctx context.Context,
	destination *indexDestination,
	kind indexArtifactKind,
	mode fs.FileMode,
	data []byte,
	hooks indexPublishHooks,
) (string, publicationFileSpec, bool, error) {
	template, err := newIndexArtifactNameTemplate(
		kind,
		destination.output.relative,
		mode,
		data,
	)
	if err != nil {
		return "", publicationFileSpec{}, false, err
	}
	for slot := 0; slot < 10_000; slot++ {
		name := template.Name(slot)
		coreHooks := indexArtifactCoreHooks(destination, kind, name, hooks)
		spec, err := createPublicationArtifact(
			ctx,
			destination.parent,
			name,
			mode,
			data,
			coreHooks,
		)
		if err == nil {
			return name, spec, true, nil
		}
		if spec.created {
			return name, spec, false, err
		}
		if !errors.Is(err, fs.ErrExist) {
			return name, spec, false, err
		}
	}
	return "", publicationFileSpec{}, false, errors.New("transaction artifact slots exhausted")
}

func indexArtifactCoreHooks(
	destination *indexDestination,
	kind indexArtifactKind,
	name string,
	hooks indexPublishHooks,
) publicationBarrierHooks {
	relative := destination.output.relative
	artifactKind := string(kind)
	return publicationBarrierHooks{
		validateScope: indexTransactionArtifactScopeValidator(destination, name),
		afterOpen: func(file *os.File) error {
			if hooks.afterArtifactOpen == nil {
				return nil
			}
			return hooks.afterArtifactOpen(relative, artifactKind, name, file)
		},
		afterCreate: func(file *os.File) error {
			if hooks.afterArtifactCreate == nil {
				return nil
			}
			return hooks.afterArtifactCreate(relative, artifactKind, name, file)
		},
		afterWrite: func(file *os.File) error {
			if hooks.afterArtifactWrite == nil {
				return nil
			}
			return hooks.afterArtifactWrite(relative, artifactKind, name, file)
		},
		afterChmod: func(file *os.File) error {
			if hooks.afterArtifactChmod == nil {
				return nil
			}
			return hooks.afterArtifactChmod(relative, artifactKind, name, file)
		},
		fileSync: func(file *os.File) error {
			if hooks.fileSync == nil {
				return file.Sync()
			}
			return hooks.fileSync(relative, artifactKind, name, file)
		},
		afterFileSync: func(file *os.File) error {
			if hooks.afterArtifactFileSync == nil {
				return nil
			}
			return hooks.afterArtifactFileSync(relative, artifactKind, name, file)
		},
		afterClose: func() error {
			if hooks.afterArtifactClose == nil {
				return nil
			}
			return hooks.afterArtifactClose(relative, artifactKind, name, destination.parent)
		},
		afterVerify: func() error {
			if hooks.afterArtifactVerified == nil {
				return nil
			}
			return hooks.afterArtifactVerified(relative, artifactKind, name, destination.parent)
		},
		directorySync: func(parent *os.Root) error {
			if hooks.directorySync == nil {
				return syncPublicationDirectoryPlatform(parent)
			}
			return hooks.directorySync(relative, artifactKind, name, parent)
		},
		afterDirectorySync: func() error {
			if hooks.afterArtifactDirectorySync == nil {
				return nil
			}
			return hooks.afterArtifactDirectorySync(relative, artifactKind, name, destination.parent)
		},
	}
}

func indexRestoreInstallCoreHooks(
	destination *indexDestination,
	kind indexArtifactKind,
	sourceName string,
	hooks indexPublishHooks,
) publicationBarrierHooks {
	scopeValidator := indexPublicationScopeValidator(destination)
	if destination != nil && destination.transactionScope != nil {
		scopeValidator = indexTransactionArtifactScopeValidator(
			destination,
			sourceName,
		)
	}
	return publicationBarrierHooks{
		validateScope: scopeValidator,
		fileSync: func(file *os.File) error {
			if hooks.fileSync == nil {
				return file.Sync()
			}
			return hooks.fileSync(
				destination.output.relative,
				string(kind),
				sourceName,
				file,
			)
		},
		directorySync: func(parent *os.Root) error {
			if hooks.directorySync == nil {
				return syncPublicationDirectoryPlatform(parent)
			}
			return hooks.directorySync(
				destination.output.relative,
				string(kind),
				sourceName,
				parent,
			)
		},
		afterInstall: func() error {
			if hooks.afterRestoreLink == nil {
				return nil
			}
			return hooks.afterRestoreLink(
				destination.output.relative,
				destination.parent,
				sourceName,
				destination.leaf,
			)
		},
		afterDirectorySync: func() error {
			if hooks.afterArtifactDirectorySync == nil {
				return nil
			}
			return hooks.afterArtifactDirectorySync(
				destination.output.relative,
				string(kind),
				sourceName,
				destination.parent,
			)
		},
	}
}

func prepareIndexVacateClaimName(
	destination *indexDestination,
	hooks indexPublishHooks,
) (string, error) {
	name, _, err := stageIndexRecoveryCopy(destination, hooks)
	if err != nil {
		return "", err
	}
	cleanupHooks := hooks
	cleanupHooks.beforeCleanup = nil
	cleanupHooks.afterCleanup = nil
	removed, err := removeCreatedIndexArtifact(
		destination,
		cleanupHooks,
		"recovery stage",
		name,
		destination.restoreInfo,
		destination.restoreComplete,
		destination.originalData,
		indexPreservedMode(destination.original.Mode()),
	)
	if removed {
		destination.restoreStage = ""
		destination.restoreInfo = nil
		destination.restoreComplete = false
	}
	if err != nil {
		return "", err
	}
	if !removed {
		return "", errors.New("ownership claim preparation did not remove placeholder")
	}
	if _, err := destination.parent.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		return "", errors.New("ownership claim path is not absent after preparation")
	}
	return name, nil
}

type existingIndexRecoveryScan struct {
	firstInvalidName   string
	firstInvalidPath   string
	firstInvalidReason string
	firstVerifiedName  string
	firstVerifiedPath  string
	observations       map[string]indexRecoveryObservation
}

type indexTransactionDiscovery struct {
	kind         indexArtifactKind
	directory    string
	name         string
	protocolName string
	relative     string
	observation  indexRecoveryObservation
}

type indexTransactionInventoryItem struct {
	discovery   indexTransactionDiscovery
	destination *indexDestination
	observation indexRecoveryObservation
}

type indexTransactionCleanupAction struct {
	item            *indexTransactionInventoryItem
	requireLeaf     bool
	requireSameFile bool
	restoreLeaf     bool
	checkLeaf       bool
	expectedLeaf    indexArtifactLeafState
}

type indexTransactionPlannedError struct {
	relative string
	err      error
}

type indexRecoveryObservation struct {
	name           string
	relative       string
	info           os.FileInfo
	reason         string
	data           []byte
	fingerprint    [sha256.Size]byte
	hasFingerprint bool
}

type preparedIndexRecovery struct {
	root            *os.Root
	inventory       []*indexTransactionInventoryItem
	actions         []indexTransactionCleanupAction
	batch           *preparedIndexBatchRecovery
	blocker         error
	hooks           indexPublishHooks
	sharedInventory bool
}

func inspectGlobalIndexRecoveryNamespace(root *os.Root, hooks indexPublishHooks) error {
	return inspectGlobalPublicationRecoveryNamespace(
		context.Background(),
		root,
		hooks,
		documentPublishHooks{},
	)
}

func indexNamespaceRule() publicationNamespaceRule {
	return publicationNamespaceRule{
		name: "index-v1",
		classify: func(name string) (string, bool) {
			kind, reserved := classifyIndexTransactionArtifact(name)
			return string(kind), reserved
		},
	}
}

func indexDiscoveriesFromPublication(
	root *os.Root,
	shared []publicationNamespaceDiscovery,
) ([]indexTransactionDiscovery, error) {
	discoveries := make([]indexTransactionDiscovery, 0, len(shared))
	for _, item := range shared {
		parent, err := openDirectory(root, item.directory)
		if err != nil {
			return nil, err
		}
		kind := indexArtifactKind(item.kind)
		observation := observeIndexTransactionArtifact(
			parent,
			pathJoin(item.directory, indexFilename),
			item.directory,
			item.name,
			item.protocolName,
			kind,
		)
		closeErr := parent.Close()
		if closeErr != nil {
			return nil, closeErr
		}
		discoveries = append(discoveries, indexTransactionDiscovery{
			kind:         kind,
			directory:    item.directory,
			name:         item.name,
			protocolName: item.protocolName,
			relative:     item.relative,
			observation:  observation,
		})
	}
	return discoveries, nil
}

func inspectIndexRecoveryDiscoveries(
	root *os.Root,
	discoveries []indexTransactionDiscovery,
	hooks indexPublishHooks,
) error {
	if len(discoveries) == 0 {
		return nil
	}
	prepared, err := prepareIndexRecovery(root, discoveries, hooks)
	if err != nil {
		return err
	}
	defer prepared.close()
	return prepared.apply()
}

func prepareIndexRecovery(
	root *os.Root,
	discoveries []indexTransactionDiscovery,
	hooks indexPublishHooks,
) (*preparedIndexRecovery, error) {
	return prepareIndexRecoveryWithSharedInventory(root, discoveries, hooks, false)
}

func prepareIndexRecoveryWithSharedInventory(
	root *os.Root,
	discoveries []indexTransactionDiscovery,
	hooks indexPublishHooks,
	sharedInventory bool,
) (*preparedIndexRecovery, error) {
	inventory, err := buildIndexTransactionInventory(root, discoveries, hooks)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*preparedIndexRecovery, error) {
		closeIndexTransactionInventory(inventory)
		return nil, err
	}
	batch, handled, err := prepareIndexBatchRecovery(inventory, hooks, sharedInventory)
	if err != nil {
		return fail(err)
	}
	if handled {
		return &preparedIndexRecovery{
			root:            root,
			inventory:       inventory,
			batch:           batch,
			hooks:           hooks,
			sharedInventory: sharedInventory,
		}, nil
	}
	if err := validateIndexTransactionInventoryShape(inventory); err != nil {
		return fail(err)
	}
	if err := validatePreManifestIndexRecovery(inventory); err != nil {
		return fail(err)
	}
	var claimHookErr error
	for _, item := range inventory {
		if item.discovery.kind != indexArtifactBackup || hooks.beforeRecoveryClaim == nil {
			continue
		}
		claimHookErr = errors.Join(
			claimHookErr,
			hooks.beforeRecoveryClaim(
				item.destination.output.relative,
				item.destination.parent,
				item.discovery.name,
			),
		)
	}
	revalidate := revalidateIndexTransactionInventory
	if sharedInventory {
		revalidate = func(_ *os.Root, inventory []*indexTransactionInventoryItem) error {
			return revalidateIndexTransactionInventoryObservations(inventory)
		}
	}
	if err := revalidate(root, inventory); err != nil {
		return fail(errors.Join(claimHookErr, err))
	}
	if claimHookErr != nil {
		return fail(claimHookErr)
	}

	actions, blocker, err := planIndexTransactionRecovery(inventory)
	if err != nil {
		return fail(err)
	}
	if blocker != nil {
		actions = nil
	}
	return &preparedIndexRecovery{
		root:            root,
		inventory:       inventory,
		actions:         actions,
		blocker:         blocker,
		hooks:           hooks,
		sharedInventory: sharedInventory,
	}, nil
}

func (prepared *preparedIndexRecovery) apply() error {
	if prepared == nil {
		return nil
	}
	if prepared.batch != nil {
		return prepared.batch.apply()
	}
	var err error
	if prepared.sharedInventory {
		err = revalidateIndexTransactionInventoryObservations(prepared.inventory)
	} else {
		err = revalidateIndexTransactionInventory(prepared.root, prepared.inventory)
	}
	if err != nil {
		return err
	}
	if prepared.blocker != nil {
		return prepared.blocker
	}
	if err := applyIndexTransactionCleanupPlan(
		prepared.inventory,
		prepared.actions,
		prepared.hooks,
		prepared.sharedInventory,
	); err != nil {
		return err
	}
	return nil
}

func (prepared *preparedIndexRecovery) close() {
	if prepared == nil {
		return
	}
	if prepared.batch != nil {
		prepared.batch.close()
	}
	closeIndexTransactionInventory(prepared.inventory)
	prepared.inventory = nil
}

func buildIndexTransactionInventory(
	root *os.Root,
	discoveries []indexTransactionDiscovery,
	hooks indexPublishHooks,
) ([]*indexTransactionInventoryItem, error) {
	inventory := make([]*indexTransactionInventoryItem, 0, len(discoveries))
	fail := func(err error) ([]*indexTransactionInventoryItem, error) {
		closeIndexTransactionInventory(inventory)
		return nil, err
	}
	for _, discovery := range discoveries {
		destinationRelative := pathJoin(discovery.directory, indexFilename)
		if hooks.afterRecoveryDiscovery != nil {
			if err := hooks.afterRecoveryDiscovery(
				destinationRelative,
				root,
				discovery.relative,
			); err != nil {
				return fail(err)
			}
		}
		parent, err := openDirectory(root, discovery.directory)
		if err != nil {
			return fail(unrecoverableIndexRecoveryError{
				destination: destinationRelative,
				artifact:    discovery.relative,
				reason:      "transaction artifact parent cannot be pinned",
				err:         err,
			})
		}
		parentInfo, err := parent.Lstat(".")
		if err != nil || !parentInfo.IsDir() {
			_ = parent.Close()
			return fail(unrecoverableIndexRecoveryError{
				destination: destinationRelative,
				artifact:    discovery.relative,
				reason:      "transaction artifact parent changed",
				err:         err,
			})
		}
		destination := &indexDestination{
			output:     indexOutput{relative: destinationRelative},
			root:       root,
			parent:     parent,
			directory:  discovery.directory,
			parentInfo: parentInfo,
			leaf:       indexFilename,
		}
		initial := observeIndexTransactionArtifact(
			parent,
			destinationRelative,
			discovery.directory,
			discovery.name,
			discovery.protocolName,
			discovery.kind,
		)
		if observationErr := compareIndexRecoveryObservations(
			destinationRelative,
			singleIndexRecoveryObservation(discovery.observation),
			singleIndexRecoveryObservation(initial),
		); observationErr != nil {
			_ = parent.Close()
			return fail(observationErr)
		}
		if hooks.afterRecoveryObservation != nil {
			if err := hooks.afterRecoveryObservation(
				destinationRelative,
				parent,
				discovery.name,
			); err != nil {
				_ = parent.Close()
				return fail(err)
			}
		}
		final := observeIndexTransactionArtifact(
			parent,
			destinationRelative,
			discovery.directory,
			discovery.name,
			discovery.protocolName,
			discovery.kind,
		)
		if observationErr := compareIndexRecoveryObservations(
			destinationRelative,
			singleIndexRecoveryObservation(initial),
			singleIndexRecoveryObservation(final),
		); observationErr != nil {
			_ = parent.Close()
			return fail(observationErr)
		}
		inventory = append(inventory, &indexTransactionInventoryItem{
			discovery:   discovery,
			destination: destination,
			observation: final,
		})
	}
	return inventory, nil
}

func closeIndexTransactionInventory(inventory []*indexTransactionInventoryItem) {
	for _, item := range inventory {
		if item.destination != nil && item.destination.parent != nil {
			_ = item.destination.parent.Close()
			item.destination.parent = nil
		}
	}
}

func validateIndexTransactionInventoryShape(
	inventory []*indexTransactionInventoryItem,
) error {
	groups := make(map[string]map[indexArtifactKind][]*indexTransactionInventoryItem)
	var plannedErrors []indexTransactionPlannedError
	for _, item := range inventory {
		if item.observation.reason != "" {
			plannedErrors = append(plannedErrors, indexTransactionPlannedError{
				relative: item.discovery.relative,
				err: unrecoverableIndexRecoveryError{
					destination: item.destination.output.relative,
					artifact:    item.discovery.relative,
					reason:      item.observation.reason,
				},
			})
		}
		destination := item.destination.output.relative
		if groups[destination] == nil {
			groups[destination] = make(map[indexArtifactKind][]*indexTransactionInventoryItem)
		}
		groups[destination][item.discovery.kind] = append(
			groups[destination][item.discovery.kind],
			item,
		)
	}
	for _, byKind := range groups {
		for kind, items := range byKind {
			if len(items) <= 1 {
				continue
			}
			plannedErrors = append(plannedErrors, indexTransactionPlannedError{
				relative: items[0].discovery.relative,
				err: unrecoverableIndexRecoveryError{
					destination: items[0].destination.output.relative,
					artifact:    items[0].discovery.relative,
					reason: fmt.Sprintf(
						"multiple %s transaction artifacts make recovery ambiguous",
						kind,
					),
				},
			})
		}
		restore := firstIndexTransactionInventoryItem(byKind[indexArtifactRestore])
		backup := firstIndexTransactionInventoryItem(byKind[indexArtifactBackup])
		stage := firstIndexTransactionInventoryItem(byKind[indexArtifactStage])
		if restore != nil && backup == nil && stage != nil {
			relative := restore.discovery.relative
			if stage.discovery.relative < relative {
				relative = stage.discovery.relative
			}
			plannedErrors = append(plannedErrors, indexTransactionPlannedError{
				relative: relative,
				err: unrecoverableIndexRecoveryError{
					destination: restore.destination.output.relative,
					artifact:    relative,
					reason:      "stage and restore artifacts without a matching backup are ambiguous",
				},
			})
		}
		if restore != nil && backup != nil &&
			restore.observation.info != nil &&
			backup.observation.info != nil &&
			restore.observation.reason == "" &&
			backup.observation.reason == "" &&
			(!bytes.Equal(restore.observation.data, backup.observation.data) ||
				indexPreservedMode(restore.observation.info.Mode()) !=
					indexPreservedMode(backup.observation.info.Mode())) {
			relative := restore.discovery.relative
			if backup.discovery.relative < relative {
				relative = backup.discovery.relative
			}
			plannedErrors = append(plannedErrors, indexTransactionPlannedError{
				relative: relative,
				err: unrecoverableIndexRecoveryError{
					destination: restore.destination.output.relative,
					artifact:    relative,
					reason:      "restore and backup artifacts do not bind the same bytes and mode",
				},
			})
		}
	}
	return firstIndexTransactionPlannedError(plannedErrors)
}

func firstIndexTransactionInventoryItem(
	items []*indexTransactionInventoryItem,
) *indexTransactionInventoryItem {
	if len(items) == 0 {
		return nil
	}
	return items[0]
}

func firstIndexTransactionPlannedError(errors []indexTransactionPlannedError) error {
	if len(errors) == 0 {
		return nil
	}
	sort.SliceStable(errors, func(left, right int) bool {
		return errors[left].relative < errors[right].relative
	})
	return errors[0].err
}

func revalidateIndexTransactionInventory(
	root *os.Root,
	inventory []*indexTransactionInventoryItem,
) error {
	if err := revalidateIndexTransactionInventoryObservations(inventory); err != nil {
		return err
	}
	current, err := discoverIndexTransactionNamespace(root)
	if err != nil {
		return err
	}
	return compareIndexTransactionInventoryToDiscovery(inventory, current, nil)
}

func revalidateIndexTransactionInventoryObservations(
	inventory []*indexTransactionInventoryItem,
) error {
	for _, item := range inventory {
		if err := verifyIndexDestinationParent(item.destination); err != nil {
			return unrecoverableIndexRecoveryError{
				destination: item.destination.output.relative,
				artifact:    item.discovery.relative,
				reason:      "recovery artifact is in a detached destination parent; no bundle-relative recovery path can be proven",
				err:         err,
			}
		}
	}
	for _, item := range inventory {
		currentObservation := observeIndexTransactionArtifact(
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
			singleIndexRecoveryObservation(currentObservation),
		); err != nil {
			return err
		}
		item.observation = currentObservation
	}
	return nil
}

func compareIndexTransactionInventoryToDiscovery(
	inventory []*indexTransactionInventoryItem,
	current []indexTransactionDiscovery,
	removed map[string]struct{},
) error {
	expected := make(map[string]*indexTransactionInventoryItem, len(inventory))
	for _, item := range inventory {
		if _, ok := removed[item.discovery.relative]; ok {
			continue
		}
		expected[item.discovery.relative] = item
	}
	actual := make(map[string]indexTransactionDiscovery, len(current))
	for _, discovery := range current {
		actual[discovery.relative] = discovery
	}
	names := make([]string, 0, len(expected)+len(actual))
	seen := make(map[string]struct{}, len(expected)+len(actual))
	for name := range expected {
		names = append(names, name)
		seen[name] = struct{}{}
	}
	for name := range actual {
		if _, ok := seen[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		before, existed := expected[name]
		after, exists := actual[name]
		switch {
		case existed && !exists:
			return unrecoverableIndexRecoveryError{
				destination: before.destination.output.relative,
				artifact:    name,
				reason:      "observed recovery artifact disappeared before final verification",
			}
		case !existed && exists:
			return unrecoverableIndexRecoveryError{
				destination: pathJoin(after.directory, indexFilename),
				artifact:    name,
				reason:      "recovery artifact appeared during final verification",
			}
		case before.discovery.kind != after.kind ||
			before.observation.info == nil ||
			after.observation.info == nil ||
			!os.SameFile(before.observation.info, after.observation.info):
			return unrecoverableIndexRecoveryError{
				destination: before.destination.output.relative,
				artifact:    name,
				reason:      "observed recovery artifact was replaced before final verification",
			}
		case before.observation.info.Mode() != after.observation.info.Mode() ||
			before.observation.reason != after.observation.reason ||
			before.observation.hasFingerprint != after.observation.hasFingerprint ||
			before.observation.hasFingerprint &&
				before.observation.fingerprint != after.observation.fingerprint:
			return unrecoverableIndexRecoveryError{
				destination: before.destination.output.relative,
				artifact:    name,
				reason:      "observed recovery artifact state changed before final verification",
			}
		}
	}
	return nil
}

type indexArtifactLeafState struct {
	present  bool
	matches  bool
	sameFile bool
}

func observeIndexArtifactLeaf(
	item *indexTransactionInventoryItem,
) (indexArtifactLeafState, error) {
	var state indexArtifactLeafState
	before, err := item.destination.parent.Lstat(item.destination.leaf)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	state.present = true
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return state, nil
	}
	data, err := readRegularIndexDestination(item.destination.parent, item.destination.leaf)
	if err != nil {
		return state, err
	}
	after, err := item.destination.parent.Lstat(item.destination.leaf)
	if err != nil ||
		after.Mode()&os.ModeSymlink != 0 ||
		!after.Mode().IsRegular() ||
		!os.SameFile(before, after) ||
		indexPreservedMode(before.Mode()) != indexPreservedMode(after.Mode()) {
		if err == nil {
			err = errors.New("destination changed while observing transaction recovery state")
		}
		return state, err
	}
	state.sameFile = os.SameFile(item.observation.info, after)
	state.matches = bytes.Equal(data, item.observation.data) &&
		indexPreservedMode(after.Mode()) == indexPreservedMode(item.observation.info.Mode())
	return state, nil
}

func planIndexTransactionRecovery(
	inventory []*indexTransactionInventoryItem,
) ([]indexTransactionCleanupAction, error, error) {
	groups := make(map[string]map[indexArtifactKind]*indexTransactionInventoryItem)
	destinations := make([]string, 0)
	for _, item := range inventory {
		destination := item.destination.output.relative
		if groups[destination] == nil {
			groups[destination] = make(map[indexArtifactKind]*indexTransactionInventoryItem)
			destinations = append(destinations, destination)
		}
		groups[destination][item.discovery.kind] = item
	}
	sort.Strings(destinations)

	var actions []indexTransactionCleanupAction
	var blockers []indexTransactionPlannedError
	for _, destination := range destinations {
		group := groups[destination]
		stage := group[indexArtifactStage]
		restore := group[indexArtifactRestore]
		backup := group[indexArtifactBackup]
		anchor := group[indexArtifactAnchor]
		witness := group[indexArtifactWitness]

		var stageLeaf indexArtifactLeafState
		if stage != nil {
			state, err := observeIndexArtifactLeaf(stage)
			if err != nil {
				return nil, nil, unrecoverableIndexRecoveryError{
					destination: destination,
					artifact:    stage.discovery.relative,
					reason:      "destination cannot be observed before stage cleanup",
					err:         err,
				}
			}
			stageLeaf = state
			if state.sameFile && !state.matches {
				return nil, nil, unrecoverableIndexRecoveryError{
					destination: destination,
					artifact:    stage.discovery.relative,
					reason:      "published stage alias does not match its self-authenticating contract",
				}
			}
			actions = append(actions, indexTransactionCleanupAction{
				item:            stage,
				requireLeaf:     state.sameFile,
				requireSameFile: state.sameFile,
				checkLeaf:       true,
				expectedLeaf:    state,
			})
		}

		if restore != nil {
			state, err := observeIndexArtifactLeaf(restore)
			if err != nil {
				return nil, nil, unrecoverableIndexRecoveryError{
					destination: destination,
					artifact:    restore.discovery.relative,
					reason:      "destination cannot be observed before restore recovery",
					err:         err,
				}
			}
			var backupLeaf indexArtifactLeafState
			if backup != nil {
				backupLeaf, err = observeIndexArtifactLeaf(backup)
				if err != nil {
					return nil, nil, unrecoverableIndexRecoveryError{
						destination: destination,
						artifact:    backup.discovery.relative,
						reason:      "destination cannot be identity-bound to recovery backup",
						err:         err,
					}
				}
			}
			var anchorLeaf indexArtifactLeafState
			if anchor != nil {
				anchorLeaf, err = observeIndexArtifactLeaf(anchor)
				if err != nil {
					return nil, nil, unrecoverableIndexRecoveryError{
						destination: destination,
						artifact:    anchor.discovery.relative,
						reason:      "destination cannot be identity-bound to rollback anchor",
						err:         err,
					}
				}
			}
			switch {
			case state.sameFile && state.matches:
				actions = append(actions, indexTransactionCleanupAction{
					item:            restore,
					requireLeaf:     true,
					requireSameFile: true,
				})
			case state.present && state.matches && backupLeaf.sameFile && backupLeaf.matches:
				actions = append(actions, indexTransactionCleanupAction{
					item:        restore,
					requireLeaf: true,
				})
			case state.present && state.matches && anchorLeaf.sameFile && anchorLeaf.matches:
				actions = append(actions, indexTransactionCleanupAction{
					item:        restore,
					requireLeaf: true,
				})
			case !state.present && backup != nil:
				actions = append(actions, indexTransactionCleanupAction{
					item:            restore,
					requireLeaf:     true,
					requireSameFile: true,
					restoreLeaf:     true,
				})
			default:
				reason := "verified restore artifact may be the only preserved original"
				if state.present {
					reason = "destination does not match the verified restore artifact"
				}
				blockers = append(blockers, indexTransactionPlannedError{
					relative: restore.discovery.relative,
					err: unrecoverableIndexRecoveryError{
						destination: destination,
						artifact:    restore.discovery.relative,
						reason:      reason,
					},
				})
			}
		}

		if backup != nil {
			state, err := observeIndexArtifactLeaf(backup)
			if err != nil {
				return nil, nil, unrecoverableIndexRecoveryError{
					destination: destination,
					artifact:    backup.discovery.relative,
					reason:      "destination cannot be observed before backup recovery",
					err:         err,
				}
			}
			switch {
			case state.matches:
				actions = append(actions, indexTransactionCleanupAction{
					item:        backup,
					requireLeaf: true,
				})
			case !state.present && restore == nil:
				actions = append(actions, indexTransactionCleanupAction{
					item:            backup,
					requireLeaf:     true,
					requireSameFile: true,
					restoreLeaf:     true,
				})
			case !state.present && restore != nil:
				actions = append(actions, indexTransactionCleanupAction{
					item:        backup,
					requireLeaf: true,
				})
			default:
				reason := "verified recovery backup requires manual restoration"
				if stage != nil && stageLeaf.sameFile {
					reason = "published stage alias has no transaction manifest; verified original backup requires manual restoration"
				}
				blockers = append(blockers, indexTransactionPlannedError{
					relative: backup.discovery.relative,
					err: recoverableIndexBackupError{
						destination: destination,
						backup:      backup.discovery.relative,
						reason:      reason,
					},
				})
			}
		}
		if witness != nil {
			state, err := observeIndexArtifactLeaf(witness)
			if err != nil || !state.present || !state.matches ||
				!state.sameFile && anchor == nil {
				return nil, nil, unrecoverableIndexRecoveryError{
					destination: destination,
					artifact:    witness.discovery.relative,
					reason:      "identity witness is not bound to the exact all-old target",
					err:         err,
				}
			}
			actions = append(actions, indexTransactionCleanupAction{
				item:            witness,
				requireLeaf:     true,
				requireSameFile: state.sameFile,
			})
		}
		if anchor != nil {
			state, err := observeIndexArtifactLeaf(anchor)
			if err != nil || !state.present || !state.matches || !state.sameFile {
				return nil, nil, unrecoverableIndexRecoveryError{
					destination: destination,
					artifact:    anchor.discovery.relative,
					reason:      "rollback anchor is not the exact all-old identity witness",
					err:         err,
				}
			}
			actions = append(actions, indexTransactionCleanupAction{
				item:            anchor,
				requireLeaf:     true,
				requireSameFile: true,
			})
		}
	}

	sort.SliceStable(actions, func(left, right int) bool {
		priority := func(kind indexArtifactKind) int {
			switch kind {
			case indexArtifactStage:
				return 0
			case indexArtifactRestore:
				return 1
			case indexArtifactBackup:
				return 2
			case indexArtifactAnchor:
				return 4
			case indexArtifactWitness:
				return 3
			default:
				return 5
			}
		}
		leftPriority := priority(actions[left].item.discovery.kind)
		rightPriority := priority(actions[right].item.discovery.kind)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return actions[left].item.discovery.relative < actions[right].item.discovery.relative
	})
	return actions, firstIndexTransactionPlannedError(blockers), nil
}

func applyIndexTransactionCleanupPlan(
	inventory []*indexTransactionInventoryItem,
	actions []indexTransactionCleanupAction,
	hooks indexPublishHooks,
	sharedInventory bool,
) error {
	if hooks.beforeCleanup != nil {
		for _, action := range actions {
			if err := hooks.beforeCleanup(
				action.item.destination.output.relative,
				action.item.destination.parent,
				string(action.item.discovery.kind),
				action.item.discovery.name,
			); err != nil {
				return err
			}
		}
	}
	if len(actions) > 0 {
		var err error
		if sharedInventory {
			err = revalidateIndexTransactionInventoryObservations(inventory)
		} else {
			err = revalidateIndexTransactionInventory(
				inventory[0].destination.root,
				inventory,
			)
		}
		if err != nil {
			return err
		}
	}
	cleanupHooks := hooks
	cleanupHooks.beforeCleanup = nil
	for _, action := range actions {
		if action.checkLeaf {
			state, err := observeIndexArtifactLeaf(action.item)
			if err != nil || state != action.expectedLeaf {
				if err == nil {
					err = errors.New("destination state changed after recovery planning")
				}
				return unrecoverableIndexRecoveryError{
					destination: action.item.destination.output.relative,
					artifact:    action.item.discovery.relative,
					reason:      "destination changed before planned transaction artifact cleanup",
					err:         err,
				}
			}
		}
		if action.restoreLeaf {
			if err := restoreIndexLeafFromTransactionArtifact(action.item, hooks); err != nil {
				return err
			}
		}
		if action.requireLeaf {
			state, err := observeIndexArtifactLeaf(action.item)
			if err != nil || !state.matches ||
				action.requireSameFile && !state.sameFile {
				if err == nil {
					err = errors.New("destination no longer satisfies the planned recovery postcondition")
				}
				return unrecoverableIndexRecoveryError{
					destination: action.item.destination.output.relative,
					artifact:    action.item.discovery.relative,
					reason:      "destination changed before guarded transaction artifact cleanup",
					err:         err,
				}
			}
		}
		if err := removeDiscoveredIndexTransientArtifact(
			action.item.destination,
			action.item.discovery,
			action.item.observation,
			cleanupHooks,
		); err != nil {
			return err
		}
		if action.requireLeaf {
			state, err := observeIndexArtifactLeaf(action.item)
			if err != nil || !state.matches {
				if err == nil {
					err = errors.New("destination changed after transaction artifact cleanup")
				}
				return unrecoverableIndexRecoveryError{
					destination: action.item.destination.output.relative,
					artifact:    action.item.discovery.relative,
					reason:      "recovered destination failed post-cleanup verification",
					err:         err,
				}
			}
		}
	}
	return nil
}

func restoreIndexLeafFromTransactionArtifact(
	item *indexTransactionInventoryItem,
	hooks indexPublishHooks,
) error {
	if err := verifyIndexDestinationParent(item.destination); err != nil {
		return unrecoverableIndexRecoveryError{
			destination: item.destination.output.relative,
			artifact:    item.discovery.relative,
			reason:      "transaction artifact parent changed before restoration",
			err:         err,
		}
	}
	if _, err := item.destination.parent.Lstat(item.destination.leaf); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			err = errors.New("destination became occupied")
		}
		return unrecoverableIndexRecoveryError{
			destination: item.destination.output.relative,
			artifact:    item.discovery.relative,
			reason:      "destination is not absent before restoration",
			err:         err,
		}
	}
	if err := verifyCreatedIndexArtifactForCleanup(
		item.destination,
		item.discovery.name,
		item.observation.info,
		true,
		item.observation.data,
		indexPreservedMode(item.observation.info.Mode()),
	); err != nil {
		return unrecoverableIndexRecoveryError{
			destination: item.destination.output.relative,
			artifact:    item.discovery.relative,
			reason:      "transaction artifact changed before restoration",
			err:         err,
		}
	}
	installed, err := guardedInstallPublicationLeaf(
		context.Background(),
		item.destination.parent,
		item.discovery.name,
		publicationSpecFromBytes(item.observation.info, item.observation.data),
		item.destination.leaf,
		indexRestoreInstallCoreHooks(
			item.destination,
			item.discovery.kind,
			item.discovery.name,
			hooks,
		),
	)
	if err != nil {
		return unrecoverableIndexRecoveryError{
			destination: item.destination.output.relative,
			artifact:    item.discovery.relative,
			reason:      "verified transaction artifact could not restore the destination",
			err:         err,
		}
	}
	if !installed {
		return unrecoverableIndexRecoveryError{
			destination: item.destination.output.relative,
			artifact:    item.discovery.relative,
			reason:      "verified transaction artifact was not installed",
		}
	}
	state, err := observeIndexArtifactLeaf(item)
	if err != nil || !state.matches || !state.sameFile {
		if err == nil {
			err = errors.New("restored destination does not match the verified transaction artifact")
		}
		return unrecoverableIndexRecoveryError{
			destination: item.destination.output.relative,
			artifact:    item.discovery.relative,
			reason:      "restored destination could not be verified",
			err:         err,
		}
	}
	return nil
}

func discoverIndexTransactionNamespace(
	root *os.Root,
) ([]indexTransactionDiscovery, error) {
	shared, err := discoverPublicationNamespaces(
		context.Background(),
		root,
		[]publicationNamespaceRule{indexNamespaceRule()},
	)
	if err != nil {
		return nil, err
	}
	return indexDiscoveriesFromPublication(root, shared)
}

func singleIndexRecoveryObservation(
	observation indexRecoveryObservation,
) map[string]indexRecoveryObservation {
	if observation.info == nil &&
		observation.reason == "recovery artifact cannot be inspected" {
		return map[string]indexRecoveryObservation{}
	}
	return map[string]indexRecoveryObservation{observation.name: observation}
}

func removeDiscoveredIndexTransientArtifact(
	destination *indexDestination,
	discovery indexTransactionDiscovery,
	observation indexRecoveryObservation,
	hooks indexPublishHooks,
	scopeValidators ...func(publicationScopePhase) error,
) error {
	cleanupFailure := func(reason string, err error) error {
		return unrecoverableIndexRecoveryError{
			destination: destination.output.relative,
			artifact:    discovery.relative,
			reason:      reason,
			err:         err,
		}
	}
	if err := verifyIndexDestinationParent(destination); err != nil {
		return cleanupFailure("verified transaction artifact parent changed before cleanup", err)
	}
	kind := string(discovery.kind)
	spec := publicationSpecFromBytes(observation.info, observation.data)
	scopeValidator := indexPublicationScopeValidator(destination)
	if len(scopeValidators) > 0 && scopeValidators[0] != nil {
		scopeValidator = scopeValidators[0]
	}
	_, removeErr := guardedRemovePublicationFileAs(
		context.Background(),
		destination.parent,
		discovery.name,
		discovery.protocolName,
		spec,
		publicationBarrierHooks{
			validateScope: scopeValidator,
			beforeRemove: func() error {
				if hooks.beforeCleanup == nil {
					return nil
				}
				return hooks.beforeCleanup(
					destination.output.relative,
					destination.parent,
					kind,
					discovery.name,
				)
			},
			afterRemove: func() error {
				if hooks.afterCleanup == nil {
					return nil
				}
				return hooks.afterCleanup(
					destination.output.relative,
					destination.parent,
					kind,
					discovery.name,
				)
			},
		},
	)
	if removeErr != nil {
		return cleanupFailure("guarded transaction artifact cleanup failed", removeErr)
	}
	if err := verifyIndexDestinationParent(destination); err != nil {
		return cleanupFailure("transaction artifact parent changed after guarded cleanup", err)
	}
	return nil
}

func inspectExistingIndexRecoveryBackups(
	destination *indexDestination,
	hooks indexPublishHooks,
	discovery map[string]indexRecoveryObservation,
) error {
	initial, err := scanExistingIndexRecoveryBackups(
		destination.parent,
		destination.directory,
		destination.output.relative,
	)
	if err != nil {
		return err
	}
	if observationErr := compareIndexRecoveryObservations(
		destination.output.relative,
		discovery,
		initial.observations,
	); observationErr != nil {
		return observationErr
	}
	var hookErr error
	if first := firstIndexRecoveryObservationName(initial.observations); first != "" &&
		hooks.afterRecoveryObservation != nil {
		hookErr = hooks.afterRecoveryObservation(
			destination.output.relative,
			destination.parent,
			first,
		)
	}
	if initial.firstVerifiedName != "" && hooks.beforeRecoveryClaim != nil {
		hookErr = errors.Join(
			hookErr,
			hooks.beforeRecoveryClaim(
				destination.output.relative,
				destination.parent,
				initial.firstVerifiedName,
			),
		)
	}
	for attempt := 0; attempt < 3; attempt++ {
		scan, err := scanExistingIndexRecoveryBackups(
			destination.parent,
			destination.directory,
			destination.output.relative,
		)
		if err != nil {
			return errors.Join(hookErr, err)
		}
		if observationErr := compareIndexRecoveryObservations(
			destination.output.relative,
			initial.observations,
			scan.observations,
		); observationErr != nil {
			return errors.Join(hookErr, observationErr)
		}
		if scan.firstVerifiedName == "" {
			if parentErr := verifyIndexDestinationParent(destination); parentErr != nil {
				return errors.Join(hookErr, detachedIndexRecoveryError(destination, parentErr))
			}
			return errors.Join(
				hookErr,
				indexRecoveryScanError(destination.output.relative, scan),
			)
		}
		if reason := validateExistingIndexRecoveryBackup(
			destination.parent,
			destination.output.relative,
			scan.firstVerifiedName,
		); reason == "" {
			if parentErr := verifyIndexDestinationParent(destination); parentErr != nil {
				return errors.Join(hookErr, detachedIndexRecoveryError(destination, parentErr))
			}
			if finalReason := validateExistingIndexRecoveryBackup(
				destination.parent,
				destination.output.relative,
				scan.firstVerifiedName,
			); finalReason != "" {
				if attempt == 2 {
					return errors.Join(hookErr, unrecoverableIndexRecoveryError{
						destination: destination.output.relative,
						artifact:    scan.firstVerifiedPath,
						reason:      "recovery artifact changed during final claim verification: " + finalReason,
					})
				}
				continue
			}
			if parentErr := verifyIndexDestinationParent(destination); parentErr != nil {
				return errors.Join(hookErr, detachedIndexRecoveryError(destination, parentErr))
			}
			return errors.Join(
				hookErr,
				indexRecoveryScanError(destination.output.relative, scan),
			)
		} else if attempt == 2 {
			if parentErr := verifyIndexDestinationParent(destination); parentErr != nil {
				return errors.Join(hookErr, detachedIndexRecoveryError(destination, parentErr))
			}
			return errors.Join(hookErr, unrecoverableIndexRecoveryError{
				destination: destination.output.relative,
				artifact:    scan.firstVerifiedPath,
				reason:      "recovery artifact changed during final claim verification: " + reason,
			})
		}
	}
	return errors.Join(hookErr, unrecoverableIndexRecoveryError{
		destination: destination.output.relative,
		reason:      "recovery verification attempts exhausted without a stable observation",
	})
}

func detachedIndexRecoveryError(destination *indexDestination, err error) error {
	return unrecoverableIndexRecoveryError{
		destination: destination.output.relative,
		reason:      "recovery artifact is in a detached destination parent; no bundle-relative recovery path can be proven",
		err:         publicationConflict("index recovery destination parent detached", err),
	}
}

func scanExistingIndexRecoveryBackups(
	parent *os.Root,
	directory string,
	destination string,
) (existingIndexRecoveryScan, error) {
	scan := existingIndexRecoveryScan{
		observations: make(map[string]indexRecoveryObservation),
	}
	file, err := parent.Open(".")
	if err != nil {
		return scan, invalidIndexDestination(destination, "cannot inspect recovery backups")
	}
	defer file.Close()
	entries, err := file.ReadDir(-1)
	if err != nil {
		return scan, invalidIndexDestination(destination, "cannot inspect recovery backups")
	}
	sort.SliceStable(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
	for _, entry := range entries {
		if !hasASCIIFoldPrefix(entry.Name(), indexTransactionPrefix+"backup-") {
			continue
		}
		observation := observeIndexRecoveryArtifact(
			parent,
			destination,
			directory,
			entry.Name(),
		)
		scan.observations[entry.Name()] = observation
		if observation.reason == "" {
			if scan.firstVerifiedPath == "" {
				scan.firstVerifiedName = entry.Name()
				scan.firstVerifiedPath = observation.relative
			}
			continue
		}
		if scan.firstInvalidPath == "" {
			scan.firstInvalidName = entry.Name()
			scan.firstInvalidPath = observation.relative
			scan.firstInvalidReason = observation.reason
		}
	}
	return scan, nil
}

func observeIndexRecoveryArtifact(
	parent *os.Root,
	destination string,
	directory string,
	name string,
) indexRecoveryObservation {
	return observeIndexTransactionArtifact(
		parent,
		destination,
		directory,
		name,
		name,
		indexArtifactBackup,
	)
}

func observeIndexTransactionArtifact(
	parent *os.Root,
	destination string,
	directory string,
	name, protocolName string,
	kind indexArtifactKind,
) indexRecoveryObservation {
	observation := indexRecoveryObservation{
		name:     name,
		relative: pathJoin(directory, name),
		reason: validateExistingIndexTransactionArtifactNamed(
			parent, destination, name, protocolName, kind,
		),
	}
	info, err := parent.Lstat(name)
	if err != nil {
		if observation.reason == "" {
			observation.reason = "recovery artifact disappeared while observing"
		}
		return observation
	}
	observation.info = info
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return observation
	}
	data, err := readRegularIndexDestination(parent, name)
	if err != nil {
		if observation.reason == "" {
			observation.reason = "recovery artifact cannot be read while observing"
		}
		return observation
	}
	after, err := parent.Lstat(name)
	if err != nil ||
		after.Mode()&os.ModeSymlink != 0 ||
		!after.Mode().IsRegular() ||
		!os.SameFile(info, after) ||
		indexPreservedMode(info.Mode()) != indexPreservedMode(after.Mode()) {
		observation.info = after
		observation.reason = "recovery artifact changed while observing"
		return observation
	}
	observation.info = after
	observation.data = append([]byte(nil), data...)
	observation.fingerprint = sha256.Sum256(data)
	observation.hasFingerprint = true
	return observation
}

func compareIndexRecoveryObservations(
	destination string,
	before map[string]indexRecoveryObservation,
	after map[string]indexRecoveryObservation,
) error {
	if before == nil {
		return nil
	}
	names := make([]string, 0, len(before)+len(after))
	seen := make(map[string]struct{}, len(before)+len(after))
	for name := range before {
		names = append(names, name)
		seen[name] = struct{}{}
	}
	for name := range after {
		if _, ok := seen[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		previous, existed := before[name]
		current, exists := after[name]
		switch {
		case existed && !exists:
			return unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    previous.relative,
				reason:      "observed recovery artifact disappeared before final verification",
			}
		case !existed && exists:
			return unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    current.relative,
				reason:      "recovery artifact appeared during final verification",
			}
		case previous.info == nil || current.info == nil:
			return unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    previous.relative,
				reason:      "observed recovery artifact identity became unavailable",
			}
		case !os.SameFile(previous.info, current.info):
			return unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    previous.relative,
				reason:      "observed recovery artifact was replaced before final verification",
			}
		case previous.info.Mode() != current.info.Mode() ||
			previous.reason != current.reason ||
			previous.hasFingerprint != current.hasFingerprint ||
			previous.hasFingerprint && previous.fingerprint != current.fingerprint:
			return unrecoverableIndexRecoveryError{
				destination: destination,
				artifact:    previous.relative,
				reason:      "observed recovery artifact state changed before final verification",
			}
		}
	}
	return nil
}

func firstIndexRecoveryObservationName(
	observations map[string]indexRecoveryObservation,
) string {
	if len(observations) == 0 {
		return ""
	}
	names := make([]string, 0, len(observations))
	for name := range observations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names[0]
}

func hasASCIIFoldPrefix(value, prefix string) bool {
	if len(value) < len(prefix) {
		return false
	}
	for index := range len(prefix) {
		left := value[index]
		right := prefix[index]
		if left >= 'A' && left <= 'Z' {
			left += 'a' - 'A'
		}
		if right >= 'A' && right <= 'Z' {
			right += 'a' - 'A'
		}
		if left != right {
			return false
		}
	}
	return true
}

func indexRecoveryScanError(destination string, scan existingIndexRecoveryScan) error {
	if scan.firstVerifiedPath != "" {
		reason := "verified recovery backup requires manual restoration"
		if scan.firstInvalidPath != "" {
			reason += fmt.Sprintf(
				"; invalid recovery artifact %q: %s",
				scan.firstInvalidPath,
				scan.firstInvalidReason,
			)
		}
		return recoverableIndexBackupError{
			destination: destination,
			backup:      scan.firstVerifiedPath,
			reason:      reason,
		}
	}
	if scan.firstInvalidPath != "" {
		return unrecoverableIndexRecoveryError{
			destination: destination,
			artifact:    scan.firstInvalidPath,
			reason:      scan.firstInvalidReason,
		}
	}
	return nil
}

func validateExistingIndexRecoveryBackup(parent *os.Root, destination, name string) string {
	return validateExistingIndexTransactionArtifact(
		parent,
		destination,
		name,
		indexArtifactBackup,
	)
}

func validateExistingIndexTransactionArtifact(
	parent *os.Root,
	destination string,
	name string,
	kind indexArtifactKind,
) string {
	return validateExistingIndexTransactionArtifactNamed(
		parent,
		destination,
		name,
		name,
		kind,
	)
}

func validateExistingIndexTransactionArtifactNamed(
	parent *os.Root,
	destination string,
	storageName string,
	protocolName string,
	kind indexArtifactKind,
) string {
	if kind == indexArtifactUnknown {
		return "unknown transaction artifact kind"
	}
	if kind == indexArtifactManifest {
		return validateExistingIndexBatchManifestArtifact(
			parent,
			destination,
			storageName,
			protocolName,
		)
	}
	slot, ok := parseIndexArtifactName(protocolName, kind)
	if !ok {
		if kind == indexArtifactBackup {
			return "malformed recovery artifact name"
		}
		return "malformed transaction artifact name"
	}
	before, err := parent.Lstat(storageName)
	if err != nil {
		return "recovery artifact cannot be inspected"
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return "recovery artifact is a symlink"
	}
	if !before.Mode().IsRegular() {
		return "recovery artifact is not a regular file"
	}
	data, err := readRegularIndexDestination(parent, storageName)
	if err != nil {
		return "recovery artifact cannot be read without following links"
	}
	after, err := parent.Lstat(storageName)
	if err != nil ||
		after.Mode()&os.ModeSymlink != 0 ||
		!after.Mode().IsRegular() ||
		!os.SameFile(before, after) ||
		indexPreservedMode(after.Mode()) != indexPreservedMode(before.Mode()) {
		return "recovery artifact changed while verifying"
	}
	want, err := indexArtifactNameForChecked(kind, destination, after.Mode(), data, slot)
	if err != nil {
		return err.Error()
	}
	if want != protocolName {
		return "recovery artifact digest does not match destination, bytes, and mode"
	}
	return ""
}

func indexPreservedMode(mode fs.FileMode) fs.FileMode {
	return publicationPreservedMode(mode)
}

func parseIndexArtifactName(name string, kind indexArtifactKind) (int, bool) {
	prefix, err := indexArtifactPrefix(kind)
	if err != nil {
		return 0, false
	}
	if !strings.HasPrefix(name, prefix) {
		return 0, false
	}
	remainder := strings.TrimPrefix(name, prefix)
	if len(remainder) != 4+1+sha256.Size*2 || remainder[4] != '-' {
		return 0, false
	}
	for _, character := range remainder[:4] {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	for _, character := range remainder[5:] {
		isDigit := character >= '0' && character <= '9'
		isLowerHex := character >= 'a' && character <= 'f'
		if !isDigit && !isLowerHex {
			return 0, false
		}
	}
	slot, parseErr := strconv.Atoi(remainder[:4])
	return slot, parseErr == nil
}

func classifyIndexTransactionArtifact(name string) (indexArtifactKind, bool) {
	if !hasASCIIFoldPrefix(name, indexTransactionPrefix) {
		return indexArtifactUnknown, false
	}
	kinds := []indexArtifactKind{
		indexArtifactRestoreInstall,
		indexArtifactNewInstall,
		indexArtifactManifest,
		indexArtifactDiscard,
		indexArtifactWitness,
		indexArtifactRestore,
		indexArtifactBackup,
		indexArtifactAnchor,
		indexArtifactStage,
	}
	for _, kind := range kinds {
		prefix, err := indexArtifactPrefix(kind)
		if err == nil && hasASCIIFoldPrefix(name, prefix) {
			return kind, true
		}
	}
	for _, kind := range kinds {
		familyPrefix := indexTransactionPrefix + string(kind) + "-"
		if hasASCIIFoldPrefix(name, familyPrefix) {
			return kind, true
		}
	}
	return indexArtifactUnknown, true
}

func cleanupIndexTransaction(destinations []*indexDestination, hooks indexPublishHooks) error {
	var cleanupErr error
	for _, destination := range destinations {
		batchLive := destination.transactionScope != nil &&
			destination.transactionScope.batch != nil &&
			destination.transactionScope.batch.manifestLive
		if destination.newInstall != "" && !destination.newInstallConsumed {
			if !batchLive {
				if err := verifyPublishedIndexBeforeCleanup(destination); err != nil {
					return errors.Join(cleanupErr, err)
				}
				removed, err := removeCreatedIndexArtifact(
					destination,
					hooks,
					"new install token",
					destination.newInstall,
					destination.newInstallInfo,
					destination.newInstallComplete,
					destination.output.data,
					indexPreservedMode(destination.stageInfoMode()),
				)
				cleanupErr = errors.Join(cleanupErr, err)
				if err != nil {
					return cleanupErr
				}
				if removed {
					destination.newInstall = ""
					destination.newInstallInfo = nil
					destination.newInstallComplete = false
				}
				if verifyErr := verifyPublishedIndexBeforeCleanup(destination); verifyErr != nil {
					return errors.Join(cleanupErr, verifyErr)
				}
			}
		}
		if !batchLive &&
			(destination.discardInfo != nil || destination.discardComplete) {
			if err := verifyPublishedIndexBeforeCleanup(destination); err != nil {
				return errors.Join(cleanupErr, err)
			}
			discardMode := fs.FileMode(0)
			if destination.discardInfo != nil {
				discardMode = indexPreservedMode(destination.discardInfo.Mode())
			}
			removed, err := removeCreatedIndexArtifact(
				destination,
				hooks,
				"discard token",
				destination.discard,
				destination.discardInfo,
				destination.discardComplete,
				destination.output.data,
				discardMode,
			)
			cleanupErr = errors.Join(cleanupErr, err)
			if err != nil {
				return cleanupErr
			}
			if removed {
				destination.discard = ""
				destination.discardInfo = nil
				destination.discardComplete = false
			}
		}
		if !batchLive &&
			!destination.restoreInstallConsumed &&
			(destination.restoreInstallInfo != nil ||
				destination.restoreInstallComplete) {
			if err := verifyPublishedIndexBeforeCleanup(destination); err != nil {
				return errors.Join(cleanupErr, err)
			}
			restoreMode := fs.FileMode(0)
			if destination.restoreInstallInfo != nil {
				restoreMode = indexPreservedMode(destination.restoreInstallInfo.Mode())
			}
			removed, err := removeCreatedIndexArtifact(
				destination,
				hooks,
				"restore install token",
				destination.restoreInstall,
				destination.restoreInstallInfo,
				destination.restoreInstallComplete,
				destination.originalData,
				restoreMode,
			)
			cleanupErr = errors.Join(cleanupErr, err)
			if err != nil {
				return cleanupErr
			}
			if removed {
				destination.restoreInstall = ""
				destination.restoreInstallInfo = nil
				destination.restoreInstallComplete = false
			}
		}
		if batchLive {
			continue
		}
		if destination.stage != "" &&
			destination.rollbackClaim != "" &&
			!destination.rollbackComplete {
			if err := verifyPublishedIndexBeforeCleanup(destination); err != nil {
				return errors.Join(cleanupErr, err)
			}
			removed, err := removeRetainedRollbackClaimAlias(destination)
			cleanupErr = errors.Join(cleanupErr, err)
			if err != nil {
				return cleanupErr
			}
			if removed {
				destination.stage = ""
				destination.stageInfo = nil
				destination.stageComplete = false
			}
			if verifyErr := verifyPublishedIndexBeforeCleanup(destination); verifyErr != nil {
				return errors.Join(cleanupErr, verifyErr)
			}
		}
		if destination.stage != "" {
			if err := verifyPublishedIndexBeforeCleanup(destination); err != nil {
				return errors.Join(cleanupErr, err)
			}
			removed, err := removeCreatedIndexArtifact(
				destination,
				hooks,
				"stage",
				destination.stage,
				destination.stageInfo,
				destination.stageComplete,
				destination.output.data,
				indexPreservedMode(destination.stageInfoMode()),
			)
			cleanupErr = errors.Join(cleanupErr, err)
			if err != nil {
				return cleanupErr
			}
			if removed {
				destination.stage = ""
				destination.stageInfo = nil
				destination.stageComplete = false
			}
			if verifyErr := verifyPublishedIndexBeforeCleanup(destination); verifyErr != nil {
				return errors.Join(cleanupErr, verifyErr)
			}
		}
		if destination.rollbackClaim != "" && destination.rollbackComplete {
			if err := verifyPublishedIndexBeforeCleanup(destination); err != nil {
				return errors.Join(cleanupErr, err)
			}
			mode := fs.FileMode(0)
			if destination.rollbackInfo != nil {
				mode = indexPreservedMode(destination.rollbackInfo.Mode())
			}
			removed, err := removeCreatedIndexArtifact(
				destination,
				hooks,
				"rollback claim",
				destination.rollbackClaim,
				destination.rollbackInfo,
				destination.rollbackComplete,
				destination.output.data,
				mode,
			)
			cleanupErr = errors.Join(cleanupErr, err)
			if err != nil {
				return cleanupErr
			}
			if removed {
				destination.rollbackClaim = ""
				destination.rollbackInfo = nil
				destination.rollbackComplete = false
				destination.rollbackKind = indexArtifactUnknown
			}
			if verifyErr := verifyPublishedIndexBeforeCleanup(destination); verifyErr != nil {
				return errors.Join(cleanupErr, verifyErr)
			}
		}
		if destination.restoreStage != "" {
			if err := verifyPublishedIndexBeforeCleanup(destination); err != nil {
				return errors.Join(cleanupErr, err)
			}
			removed, err := removeCreatedIndexArtifact(
				destination,
				hooks,
				"recovery stage",
				destination.restoreStage,
				destination.restoreInfo,
				destination.restoreComplete,
				destination.originalData,
				indexPreservedMode(destination.original.Mode()),
			)
			cleanupErr = errors.Join(cleanupErr, err)
			if err != nil {
				return cleanupErr
			}
			if removed {
				destination.restoreStage = ""
				destination.restoreInfo = nil
				destination.restoreComplete = false
			}
			if verifyErr := verifyPublishedIndexBeforeCleanup(destination); verifyErr != nil {
				return errors.Join(cleanupErr, verifyErr)
			}
		}
		if destination.backup != "" && !destination.preserveBackup {
			if err := verifyPublishedIndexBeforeCleanup(destination); err != nil {
				return errors.Join(cleanupErr, err)
			}
			removed, err := removeCreatedIndexArtifact(
				destination,
				hooks,
				"recovery backup",
				destination.backup,
				destination.backupInfo,
				destination.backupComplete,
				destination.originalData,
				indexPreservedMode(destination.original.Mode()),
			)
			cleanupErr = errors.Join(cleanupErr, err)
			if err != nil {
				destination.preserveBackup = true
				return cleanupErr
			}
			if removed {
				destination.backup = ""
				destination.backupInfo = nil
				destination.backupComplete = false
			}
			if verifyErr := verifyPublishedIndexBeforeCleanup(destination); verifyErr != nil {
				return errors.Join(cleanupErr, verifyErr)
			}
		}
		if destination.witnessInfo != nil || destination.witnessComplete {
			if err := verifyPublishedIndexBeforeCleanup(destination); err != nil {
				return errors.Join(cleanupErr, err)
			}
			removed, err := removeCreatedIndexArtifact(
				destination,
				hooks,
				"identity witness",
				destination.witness,
				destination.witnessInfo,
				destination.witnessComplete,
				destination.originalData,
				indexPreservedMode(destination.original.Mode()),
			)
			cleanupErr = errors.Join(cleanupErr, err)
			if err != nil {
				return cleanupErr
			}
			if removed {
				destination.witness = ""
				destination.witnessInfo = nil
				destination.witnessComplete = false
			}
			if verifyErr := verifyPublishedIndexBeforeCleanup(destination); verifyErr != nil {
				return errors.Join(cleanupErr, verifyErr)
			}
		}
		if destination.anchorInfo != nil || destination.anchorComplete {
			if err := verifyPublishedIndexBeforeCleanup(destination); err != nil {
				return errors.Join(cleanupErr, err)
			}
			removed, err := removeCreatedIndexArtifact(
				destination,
				hooks,
				"rollback anchor",
				destination.anchor,
				destination.anchorInfo,
				destination.anchorComplete,
				destination.originalData,
				indexPreservedMode(destination.original.Mode()),
			)
			cleanupErr = errors.Join(cleanupErr, err)
			if err != nil {
				return cleanupErr
			}
			if removed {
				destination.anchorInfo = nil
				destination.anchorComplete = false
			}
			if verifyErr := verifyPublishedIndexBeforeCleanup(destination); verifyErr != nil {
				return errors.Join(cleanupErr, verifyErr)
			}
		}
	}
	return cleanupErr
}

func verifyPublishedIndexBeforeCleanup(destination *indexDestination) error {
	if err := verifyIndexDestinationParent(destination); err != nil {
		return detachedIndexRecoveryError(destination, err)
	}
	if !destination.published {
		return nil
	}
	if err := verifyInstalledIndexDestination(destination); err != nil {
		return invalidIndexDestination(
			destination.output.relative,
			"published destination changed during transaction cleanup",
		)
	}
	return nil
}

func removeRetainedRollbackClaimAlias(destination *indexDestination) (bool, error) {
	stage, err := capturePublicationFile(
		context.Background(),
		destination.parent,
		destination.stage,
	)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		return false, indexCleanupArtifactError(
			destination,
			"stage alias",
			destination.stage,
			err,
		)
	}
	claim, err := capturePublicationFile(
		context.Background(),
		destination.parent,
		destination.rollbackClaim,
	)
	if err != nil ||
		stage.info == nil ||
		claim.info == nil ||
		destination.rollbackInfo == nil ||
		!os.SameFile(destination.rollbackInfo, claim.info) ||
		!os.SameFile(stage.info, claim.info) {
		return false, indexCleanupArtifactError(
			destination,
			"stage alias",
			destination.stage,
			errors.Join(err, errors.New("stage alias is not the retained rollback evidence inode")),
		)
	}
	removed, err := guardedRemovePublicationFile(
		context.Background(),
		destination.parent,
		destination.stage,
		stage,
		publicationBarrierHooks{
			validateScope: indexTransactionArtifactScopeValidator(
				destination,
				destination.stage,
			),
		},
	)
	if err != nil {
		return removed, indexCleanupArtifactError(
			destination,
			"stage alias",
			destination.stage,
			err,
		)
	}
	return removed, nil
}

func (destination *indexDestination) stageInfoMode() fs.FileMode {
	if destination.stageInfo == nil {
		return 0
	}
	return destination.stageInfo.Mode()
}

func removeCreatedIndexArtifact(
	destination *indexDestination,
	hooks indexPublishHooks,
	kind string,
	name string,
	expectedInfo os.FileInfo,
	complete bool,
	expectedData []byte,
	expectedMode fs.FileMode,
) (bool, error) {
	if _, err := destination.parent.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err := verifyCreatedIndexArtifactForCleanup(
		destination,
		name,
		expectedInfo,
		complete,
		expectedData,
		expectedMode,
	); err != nil {
		return false, indexCleanupArtifactError(destination, kind, name, err)
	}
	var spec publicationFileSpec
	if complete {
		spec = publicationSpecFromBytes(expectedInfo, expectedData)
	} else {
		captured, err := capturePublicationFile(context.Background(), destination.parent, name)
		if err != nil || captured.info == nil || !os.SameFile(expectedInfo, captured.info) {
			return false, indexCleanupArtifactError(
				destination,
				kind,
				name,
				errors.Join(err, errors.New("incomplete artifact identity cannot be recaptured")),
			)
		}
		spec = captured
	}
	removed, removeErr := guardedRemovePublicationFile(
		context.Background(),
		destination.parent,
		name,
		spec,
		publicationBarrierHooks{
			validateScope: indexTransactionArtifactScopeValidator(destination, name),
			beforeRemove: func() error {
				var hookErr error
				if hooks.beforeCleanup != nil {
					hookErr = hooks.beforeCleanup(
						destination.output.relative,
						destination.parent,
						kind,
						name,
					)
				}
				if parentErr := verifyIndexDestinationParent(destination); parentErr != nil {
					return errors.Join(hookErr, detachedIndexRecoveryError(destination, parentErr))
				}
				return hookErr
			},
			afterRemove: func() error {
				if hooks.afterCleanup == nil {
					return nil
				}
				return hooks.afterCleanup(
					destination.output.relative,
					destination.parent,
					kind,
					name,
				)
			},
		},
	)
	if removeErr != nil {
		return removed, indexCleanupArtifactError(destination, kind, name, removeErr)
	}
	return removed, nil
}

func verifyCreatedIndexArtifactForCleanup(
	destination *indexDestination,
	name string,
	expectedInfo os.FileInfo,
	complete bool,
	expectedData []byte,
	expectedMode fs.FileMode,
) error {
	if expectedInfo == nil {
		return errors.New("recorded artifact identity is unavailable")
	}
	before, err := destination.parent.Lstat(name)
	if err != nil {
		return errors.New("artifact cannot be inspected without following links")
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return errors.New("artifact path is now a symlink")
	}
	if !before.Mode().IsRegular() {
		return errors.New("artifact path is no longer a regular file")
	}
	if !os.SameFile(expectedInfo, before) {
		return errors.New("artifact identity changed")
	}
	if !complete {
		return nil
	}
	if indexPreservedMode(before.Mode()) != expectedMode {
		return errors.New("artifact mode changed")
	}
	data, err := readRegularIndexDestination(destination.parent, name)
	if err != nil {
		return errors.New("artifact cannot be read without following links")
	}
	after, err := destination.parent.Lstat(name)
	if err != nil ||
		after.Mode()&os.ModeSymlink != 0 ||
		!after.Mode().IsRegular() ||
		!os.SameFile(expectedInfo, after) {
		return errors.New("artifact identity or type changed while verifying")
	}
	if indexPreservedMode(after.Mode()) != expectedMode {
		return errors.New("artifact mode changed while verifying")
	}
	if !bytes.Equal(data, expectedData) {
		return errors.New("artifact content changed")
	}
	return nil
}

func indexCleanupArtifactError(
	destination *indexDestination,
	kind string,
	name string,
	err error,
) error {
	return fmt.Errorf(
		"cleanup index transaction %q %s artifact %q: changed or unsafe; left in place: %w",
		destination.output.relative,
		kind,
		pathJoin(destination.directory, name),
		err,
	)
}

func closeIndexDestinationParents(
	destinations []*indexDestination,
	hooks indexPublishHooks,
) error {
	var closeErr error
	for _, destination := range destinations {
		if destination.parent != nil {
			if hooks.beforeClose != nil {
				closeErr = errors.Join(
					closeErr,
					hooks.beforeClose(destination.output.relative),
				)
			}
			closeErr = errors.Join(closeErr, destination.parent.Close())
			destination.parent = nil
		}
	}
	return closeErr
}

func closeIndexDestinationMap(destinations map[string]*indexDestination) {
	for _, destination := range destinations {
		_ = destination.parent.Close()
	}
}
