package fs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

type visibleReplacement struct {
	key artifactClaimKey
	old fs.FileInfo
}

const absentVisibleIdentity = "absent"

func (s *Store) restoreVisibleClaimsForJournal(j journal) error {
	for _, name := range journalVisiblePaths(j) {
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, name, claimVisible)
		if err != nil {
			return err
		}
		claim, err := s.rootFD.Lstat(key.claimPath())
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || claim == nil || !claim.Mode().IsRegular() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if _, err := s.verifyVisibleArtifactProof(context.Background(), key, key.claimPath(), claim); err != nil {
			return err
		}
		target, targetErr := s.rootFD.Lstat(key.Path)
		if targetErr == nil {
			if target != nil && target.Mode().IsRegular() && os.SameFile(target, claim) {
				if err := s.cleanupVisibleReplacement(visibleReplacement{key: key, old: claim}); err != nil {
					return err
				}
			}
			continue
		}
		if !errors.Is(targetErr, os.ErrNotExist) {
			return targetErr
		}
		if err := s.restoreExpectedArtifactClaim(key, claim, false); err != nil {
			return err
		}
		witness, err := s.rootFD.Lstat(key.witnessPath(false))
		if err != nil {
			return err
		}
		if _, err := s.verifyBindingIdentity(context.Background(), key, witness); err != nil {
			return err
		}
		if err := s.removeVisibleProof(key, witness); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) cleanupVisibleClaims(j journal) error {
	paths := journalVisiblePaths(j)
	for _, target := range paths {
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, target, claimVisible)
		if err != nil {
			return err
		}
		claim, err := s.rootFD.Lstat(key.claimPath())
		if errors.Is(err, os.ErrNotExist) {
			witness, witnessErr := s.rootFD.Lstat(key.witnessPath(false))
			if witnessErr == nil {
				_, targetErr := s.rootFD.Lstat(target)
				if errors.Is(targetErr, os.ErrNotExist) && journalDeletesVisiblePath(j, target) {
					if _, err := s.verifyBindingIdentity(context.Background(), key, witness); err != nil {
						return err
					}
					if err := s.removeVisibleProof(key, witness); err != nil {
						return err
					}
				} else {
					if _, err := s.verifyBindingIdentity(context.Background(), key, witness); err != nil {
						return err
					}
					if err := s.verifyVisibleInstalledTargetProof(context.Background(), j, target); err != nil {
						return err
					}
					if err := s.removeVisibleProof(key, witness); err != nil {
						return err
					}
				}
			} else if !errors.Is(witnessErr, os.ErrNotExist) {
				return witnessErr
			} else if _, bindingErr := s.rootFD.Lstat(key.bindingPath()); bindingErr == nil {
				if journalDeletesVisiblePath(j, target) {
					if _, targetErr := s.rootFD.Lstat(target); !errors.Is(targetErr, os.ErrNotExist) {
						return errors.Join(errArtifactClaimConflict, targetErr)
					}
					if err := s.removeValidatedVisibleBindingTail(context.Background(), key); err != nil {
						return err
					}
				} else if err := s.consumeVisibleBindingTail(context.Background(), j, key, target); err != nil {
					return err
				}
			} else if !errors.Is(bindingErr, os.ErrNotExist) {
				return bindingErr
			}
			continue
		}
		if err != nil || claim == nil || !claim.Mode().IsRegular() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if _, err := s.rootFD.Lstat(target); err != nil {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if err := s.cleanupVisibleReplacement(visibleReplacement{key: key, old: claim}); err != nil {
			return err
		}
	}
	return nil
}

func journalDeletesVisiblePath(j journal, name string) bool {
	inBase := false
	for _, entry := range j.Base {
		inBase = inBase || entry.Path == name
	}
	for _, entry := range j.Files {
		if entry.Path == name {
			return false
		}
	}
	return inBase
}

func (s *Store) removeValidatedVisibleBindingTail(ctx context.Context, key artifactClaimKey) error {
	raw, err := s.readMetadataLimitObserved(ctx, key.bindingPath(), maxJournalManifestRead)
	if err != nil {
		return err
	}
	record, err := decodeDirectoryWitness(raw)
	if err != nil || record.Operation != key.Operation || record.Path != key.Path || record.Role != key.Role ||
		record.Identity == "" || record.Identity == absentVisibleIdentity || record.Source != key.Path || !safeClaimSourcePath(record.Source) {
		return errors.Join(errArtifactClaimConflict, err)
	}
	return s.removeVisibleBinding(key)
}

func journalVisiblePaths(j journal) []string {
	set := make(map[string]struct{}, len(j.Base)+len(j.Files))
	for _, entry := range j.Base {
		set[entry.Path] = struct{}{}
	}
	for _, entry := range j.Files {
		set[entry.Path] = struct{}{}
	}
	paths := make([]string, 0, len(set))
	for name := range set {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
}

func (s *Store) removeVisibleClaimed(ctx context.Context, name string, expected mutationTargetIdentity, step Step) error {
	if err := s.runDescriptorBarrier("visible_delete_begin:" + name); err != nil {
		return err
	}
	if expected.info == nil {
		if !expected.absent {
			return errMutationTargetChanged(name)
		}
		if _, err := s.rootFD.Lstat(name); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		return errMutationTargetChanged(name)
	}
	key, err := newArtifactClaimKey(expected.operation, name, claimVisible)
	if err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("visible_delete_before_target_witness"); err != nil {
		return err
	}
	if _, err := s.prepareRegularWitnessFrom(ctx, key, name, expected.info); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("visible_delete_after_target_witness"); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("visible_delete_after_target_witness:" + name); err != nil {
		return err
	}
	current, err := s.rootFD.Lstat(name)
	if err != nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(current, expected.info) {
		return errors.Join(errArtifactClaimConflict, err)
	}
	claim, err := s.fdRenameOwnedNoReplace(name, key.claimPath(), expected.info, false)
	if err != nil {
		return err
	}
	if err := s.postFault(step); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("visible_delete_claimed"); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("visible_delete_claimed:" + name); err != nil {
		return err
	}
	if _, err := s.rootFD.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.syncDirAt(path.Dir(name)); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("visible_delete_parent_synced"); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("visible_delete_parent_synced:" + name); err != nil {
		return err
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return err
	}
	return s.cleanupVisibleReplacement(visibleReplacement{key: key, old: claim})
}

func (s *Store) installVisibleSource(ctx context.Context, operation, target, source string, sourceInfo fs.FileInfo, expected mutationTargetIdentity) (visibleReplacement, fs.FileInfo, error) {
	if err := s.runDescriptorBarrier("visible_begin:" + target); err != nil {
		return visibleReplacement{}, nil, err
	}
	key, err := newArtifactClaimKey(operation, target, claimVisible)
	if err != nil {
		return visibleReplacement{}, nil, err
	}
	replacement := visibleReplacement{key: key}
	if expected.info != nil {
		if err := s.runDescriptorBarrier("visible_before_target_witness"); err != nil {
			return replacement, nil, err
		}
		if _, err := s.prepareRegularWitnessFrom(ctx, key, target, expected.info); err != nil {
			return replacement, nil, err
		}
		if err := s.runDescriptorBarrier("visible_after_target_witness"); err != nil {
			return replacement, nil, err
		}
		if err := s.runDescriptorBarrier("visible_after_target_witness:" + target); err != nil {
			return replacement, nil, err
		}
		current, err := s.rootFD.Lstat(target)
		if err != nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(expected.info, current) {
			return replacement, nil, errors.Join(errArtifactClaimConflict, err)
		}
		claim, err := s.fdRenameOwnedNoReplace(target, key.claimPath(), expected.info, false)
		if err != nil {
			return replacement, nil, err
		}
		replacement.old = claim
		if err := s.syncDirAt(path.Dir(target)); err != nil {
			return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
		}
		if err := s.syncDirAt(claimDirectory); err != nil {
			return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
		}
		if err := s.runDescriptorBarrier("visible_target_quarantined"); err != nil {
			return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
		}
		if err := s.runDescriptorBarrier("visible_target_quarantined:" + target); err != nil {
			return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
		}
	} else if !expected.absent {
		return replacement, nil, errMutationTargetChanged(target)
	} else {
		if _, err := s.rootFD.Lstat(target); err == nil {
			return replacement, nil, errMutationTargetChanged(target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return replacement, nil, err
		}
		if _, err := s.rootFD.Lstat(key.claimPath()); err == nil {
			return replacement, nil, errArtifactClaimConflict
		} else if !errors.Is(err, os.ErrNotExist) {
			return replacement, nil, err
		}
		vacancy := directoryWitnessRecord{Version: 1, Operation: key.Operation, Path: key.Path, Role: key.Role, Identity: absentVisibleIdentity}
		if err := s.ensureBindingRecord(ctx, key.bindingPath(), vacancy); err != nil {
			return replacement, nil, err
		}
	}
	if err := s.runDescriptorBarrier("visible_before_install"); err != nil {
		return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
	}
	if err := s.runDescriptorBarrier("visible_before_install:" + target); err != nil {
		return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
	}
	installedKey, err := newArtifactClaimKey(operation, target, claimVisibleInstall)
	if err != nil {
		return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
	}
	if err := s.runDescriptorBarrier("visible_before_installed_binding"); err != nil {
		return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
	}
	installedWitness, err := s.prepareRegularWitnessFrom(ctx, installedKey, source, sourceInfo)
	if err != nil {
		return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
	}
	if err := s.runDescriptorBarrier("visible_installed_binding_ready"); err != nil {
		return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
	}
	if err := s.runDescriptorBarrier("visible_installed_binding_ready:" + target); err != nil {
		return replacement, nil, errors.Join(err, s.rollbackVisibleReplacement(replacement))
	}
	installed, installErr := s.fdRenameOwnedNoReplace(source, target, sourceInfo, false)
	if installErr != nil {
		return replacement, installed, errors.Join(installErr, s.rollbackVisibleReplacement(replacement))
	}
	current, statErr := s.rootFD.Lstat(target)
	if statErr != nil || current == nil || installed == nil || installedWitness == nil || !current.Mode().IsRegular() || !os.SameFile(installed, current) || !os.SameFile(sourceInfo, current) || !os.SameFile(installedWitness, current) {
		return replacement, installed, errors.Join(errArtifactClaimConflict, statErr)
	}
	if err := s.runDescriptorBarrier("visible_source_installed"); err != nil {
		return replacement, installed, err
	}
	if err := s.runDescriptorBarrier("visible_source_installed:" + target); err != nil {
		return replacement, installed, err
	}
	current, statErr = s.rootFD.Lstat(target)
	if statErr != nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(installed, current) {
		return replacement, installed, errors.Join(errArtifactClaimConflict, statErr)
	}
	return replacement, installed, nil
}

func (s *Store) convergeVisibleVacancyClaims(ctx context.Context, j journal) error {
	for _, entry := range j.Files {
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, entry.Path, claimVisible)
		if err != nil {
			return err
		}
		raw, err := s.readMetadataLimitObserved(ctx, key.bindingPath(), maxJournalManifestRead)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		record, err := decodeDirectoryWitness(raw)
		if err != nil || record.Operation != key.Operation || record.Path != key.Path || record.Role != key.Role {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if record.Identity != absentVisibleIdentity {
			continue
		}
		target, targetErr := s.rootFD.Lstat(entry.Path)
		if errors.Is(targetErr, os.ErrNotExist) {
			continue
		}
		if targetErr != nil || target == nil || !target.Mode().IsRegular() {
			return errors.Join(errArtifactClaimConflict, targetErr)
		}
		installedKey, _ := newArtifactClaimKey(j.Receipt.RequestDigest, entry.Path, claimVisibleInstall)
		witness, witnessErr := s.rootFD.Lstat(installedKey.witnessPath(false))
		if witnessErr != nil || witness == nil || !os.SameFile(target, witness) {
			return errors.Join(errArtifactClaimConflict, witnessErr)
		}
	}
	return nil
}

func (s *Store) convergeVisibleInstalledClaims(ctx context.Context, j journal) error {
	base := make(map[string]journalBaseFile, len(j.Base))
	for _, entry := range j.Base {
		base[entry.Path] = entry
	}
	for _, entry := range j.Files {
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, entry.Path, claimVisibleInstall)
		if err != nil {
			return err
		}
		witness, err := s.rootFD.Lstat(key.witnessPath(false))
		if errors.Is(err, os.ErrNotExist) {
			if _, bindingErr := s.rootFD.Lstat(key.bindingPath()); bindingErr == nil {
				if err := s.verifyVisibleInstalledBindingTail(ctx, j, key, entry.Path); err != nil {
					return err
				}
			} else if !errors.Is(bindingErr, os.ErrNotExist) {
				return bindingErr
			}
			continue
		}
		if err != nil || witness == nil || !witness.Mode().IsRegular() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		target, targetErr := s.rootFD.Lstat(entry.Path)
		baseEntry, hadBase := base[entry.Path]
		preinstall := errors.Is(targetErr, os.ErrNotExist) && !hadBase
		if targetErr == nil && target != nil && target.Mode().IsRegular() && hadBase && target.Size() == baseEntry.Size {
			digest, digestErr := s.streamPinnedRegularDigest(ctx, entry.Path, target)
			preinstall = digestErr == nil && digest == baseEntry.Digest
			if digestErr != nil {
				return digestErr
			}
		}
		_, bindingErr := s.verifyBindingIdentity(ctx, key, witness)
		if bindingErr != nil {
			// A witness without its immutable binding is contradictory recovery
			// evidence. Recovery must never synthesize or discard proof here.
			return errors.Join(errArtifactClaimConflict, bindingErr)
		}
		if targetErr == nil && target != nil && target.Mode().IsRegular() && os.SameFile(target, witness) {
			continue
		}
		if !preinstall {
			return errors.Join(errArtifactClaimConflict, targetErr)
		}
		if err := s.removeVisibleInstalledProof(key, witness); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) cleanupVisibleInstalledClaims(ctx context.Context, j journal) error {
	for _, entry := range j.Files {
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, entry.Path, claimVisibleInstall)
		if err != nil {
			return err
		}
		witness, err := s.rootFD.Lstat(key.witnessPath(false))
		if errors.Is(err, os.ErrNotExist) {
			if _, bindingErr := s.rootFD.Lstat(key.bindingPath()); bindingErr == nil {
				if err := s.verifyVisibleInstalledBindingTail(ctx, j, key, entry.Path); err != nil {
					return err
				}
				if err := s.removeVisibleInstalledBinding(key); err != nil {
					return err
				}
			} else if !errors.Is(bindingErr, os.ErrNotExist) {
				return bindingErr
			}
			continue
		}
		if err != nil {
			return err
		}
		target, err := s.rootFD.Lstat(entry.Path)
		if err != nil || target == nil || witness == nil || !target.Mode().IsRegular() || !os.SameFile(target, witness) {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if _, err := s.verifyBindingIdentity(ctx, key, witness); err != nil {
			return err
		}
		if err := s.removeVisibleInstalledProof(key, witness); err != nil {
			return err
		}
	}
	return nil
}

// consumeVisibleBindingTail accepts only the proof-last state reached after the
// visible claim witness was removed.  A validated journal alone is not enough:
// the final target must still have the complete installed proof.
func (s *Store) consumeVisibleBindingTail(ctx context.Context, j journal, key artifactClaimKey, target string) error {
	raw, err := s.readMetadataLimitObserved(ctx, key.bindingPath(), maxJournalManifestRead)
	if err != nil {
		return err
	}
	record, err := decodeDirectoryWitness(raw)
	if err != nil || record.Operation != key.Operation || record.Path != key.Path || record.Role != key.Role {
		return errors.Join(errArtifactClaimConflict, err)
	}
	validVacancy := record.Identity == absentVisibleIdentity && record.Source == ""
	validReplacement := record.Identity != "" && record.Identity != absentVisibleIdentity && record.Source == key.Path && safeClaimSourcePath(record.Source)
	if !validVacancy && !validReplacement {
		return errArtifactClaimConflict
	}
	if err := s.verifyVisibleInstalledTargetProof(ctx, j, target); err != nil {
		return err
	}
	return s.removeVisibleBinding(key)
}

func (s *Store) verifyVisibleInstalledTargetProof(ctx context.Context, j journal, target string) error {
	installedKey, err := newArtifactClaimKey(j.Receipt.RequestDigest, target, claimVisibleInstall)
	if err != nil {
		return err
	}
	witness, err := s.rootFD.Lstat(installedKey.witnessPath(false))
	if err != nil || witness == nil || !witness.Mode().IsRegular() {
		return errors.Join(errArtifactClaimConflict, err)
	}
	current, err := s.rootFD.Lstat(target)
	if err != nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(current, witness) {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if _, err := s.verifyBindingIdentity(ctx, installedKey, witness); err != nil {
		return err
	}
	return nil
}

func (s *Store) verifyVisibleInstalledBindingTail(ctx context.Context, j journal, key artifactClaimKey, target string) error {
	claimKey, err := newArtifactClaimKey(j.Receipt.RequestDigest, target, claimVisible)
	if err != nil {
		return err
	}
	for _, name := range []string{claimKey.claimPath(), claimKey.witnessPath(false), claimKey.bindingPath()} {
		if _, statErr := s.rootFD.Lstat(name); !errors.Is(statErr, os.ErrNotExist) {
			return errors.Join(errArtifactClaimConflict, statErr)
		}
	}
	raw, err := s.readMetadataLimitObserved(ctx, key.bindingPath(), maxJournalManifestRead)
	if err != nil {
		return err
	}
	record, err := decodeDirectoryWitness(raw)
	current, statErr := s.rootFD.Lstat(target)
	identity, ok := fileIdentityKey(current)
	if err != nil || statErr != nil || current == nil || !current.Mode().IsRegular() || !ok ||
		record.Operation != key.Operation || record.Path != key.Path || record.Role != key.Role ||
		record.Identity != identity || !canonicalRegularArtifactSource(record.Source) {
		return errors.Join(errArtifactClaimConflict, err, statErr)
	}
	return nil
}

func (s *Store) verifyBindingIdentity(ctx context.Context, key artifactClaimKey, witness fs.FileInfo) (directoryWitnessRecord, error) {
	raw, err := s.readMetadataLimitObserved(ctx, key.bindingPath(), maxJournalManifestRead)
	if err != nil {
		return directoryWitnessRecord{}, errors.Join(errArtifactClaimConflict, err)
	}
	record, err := decodeDirectoryWitness(raw)
	identity, ok := fileIdentityKey(witness)
	validSource := canonicalRegularArtifactSource(record.Source)
	if key.Role == claimVisible {
		validSource = record.Source == key.Path && safeClaimSourcePath(record.Source)
	}
	if err != nil || !ok || record.Operation != key.Operation || record.Path != key.Path || record.Role != key.Role || record.Identity != identity || !validSource {
		return record, errors.Join(errArtifactClaimConflict, err)
	}
	return record, nil
}

func (s *Store) verifyVisibleArtifactProof(ctx context.Context, key artifactClaimKey, name string, expected fs.FileInfo) (fs.FileInfo, error) {
	verified, err := s.verifyArtifactProofAt(ctx, key, name, expected, false)
	if err != nil {
		return verified, err
	}
	witness, err := s.rootFD.Lstat(key.witnessPath(false))
	if err != nil || witness == nil || !os.SameFile(verified, witness) {
		return verified, errors.Join(errArtifactClaimConflict, err)
	}
	if _, err := s.verifyBindingIdentity(ctx, key, witness); err != nil {
		return verified, err
	}
	return verified, nil
}

func (s *Store) removeVisibleInstalledProof(key artifactClaimKey, witness fs.FileInfo) error {
	removed, err := s.fdRemoveClaimOwned(claimZonePath(key.witnessPath(false)), witness, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.runDescriptorBarrier("visible_installed_witness_removed"); err != nil {
		return err
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("visible_installed_witness_parent_synced"); err != nil {
		return err
	}
	return s.removeVisibleInstalledBinding(key)
}

func (s *Store) removeVisibleInstalledBinding(key artifactClaimKey) error {
	binding, err := s.rootFD.Lstat(key.bindingPath())
	if err != nil {
		return err
	}
	removed, err := s.fdRemoveClaimOwned(claimZonePath(key.bindingPath()), binding, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.runDescriptorBarrier("visible_installed_binding_removed"); err != nil {
		return err
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return err
	}
	return s.runDescriptorBarrier("visible_installed_binding_parent_synced")
}

func (s *Store) cleanupVisibleInstalledInventory(ctx context.Context) error {
	dir, err := openPinnedMetadataDir(s.rootFD, claimDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasSuffix(entry.Name(), ".binding.json") {
			continue
		}
		raw, err := s.readMetadataLimitObserved(ctx, path.Join(claimDirectory, entry.Name()), maxJournalManifestRead)
		if err != nil {
			return err
		}
		record, err := decodeDirectoryWitness(raw)
		if err != nil || record.Role != claimVisibleInstall {
			continue
		}
		key, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
		if err != nil || path.Base(key.bindingPath()) != entry.Name() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		witness, witnessErr := s.rootFD.Lstat(key.witnessPath(false))
		if errors.Is(witnessErr, os.ErrNotExist) {
			// Binding-only installed proof is a reachable proof-last state.  It is
			// ambiguous without its validated journal and is deliberately deferred.
			continue
		}
		if witnessErr != nil {
			return witnessErr
		}
		target, targetErr := s.rootFD.Lstat(key.Path)
		if targetErr != nil || target == nil || witness == nil || !target.Mode().IsRegular() || !os.SameFile(target, witness) {
			return errors.Join(errArtifactClaimConflict, targetErr)
		}
		if _, err := s.verifyBindingIdentity(ctx, key, witness); err != nil {
			return err
		}
		claimKey, claimKeyErr := newArtifactClaimKey(record.Operation, record.Path, claimVisible)
		if claimKeyErr != nil {
			return claimKeyErr
		}
		deferToJournal := false
		for _, name := range []string{claimKey.claimPath(), claimKey.witnessPath(false), claimKey.bindingPath()} {
			if _, statErr := s.rootFD.Lstat(name); statErr == nil {
				deferToJournal = true
				break
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
		}
		if deferToJournal {
			continue
		}
		if err := s.removeVisibleInstalledProof(key, witness); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) rollbackVisibleReplacement(replacement visibleReplacement) error {
	if replacement.old == nil {
		return nil
	}
	if err := s.restoreExpectedArtifactClaim(replacement.key, replacement.old, false); err != nil {
		return err
	}
	witness, err := s.rootFD.Lstat(replacement.key.witnessPath(false))
	if err != nil {
		return err
	}
	return s.removeVisibleProof(replacement.key, witness)
}

func (s *Store) removeVisibleProof(key artifactClaimKey, witness fs.FileInfo) error {
	removed, err := s.fdRemoveClaimOwned(claimZonePath(key.witnessPath(false)), witness, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.runDescriptorBarrier("visible_witness_removed"); err != nil {
		return err
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("visible_witness_parent_synced"); err != nil {
		return err
	}
	return s.removeVisibleBinding(key)
}

func (s *Store) removeVisibleBinding(key artifactClaimKey) error {
	binding, err := s.rootFD.Lstat(key.bindingPath())
	if err != nil {
		return err
	}
	removed, err := s.fdRemoveClaimOwned(claimZonePath(key.bindingPath()), binding, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.runDescriptorBarrier("visible_binding_removed"); err != nil {
		return err
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return err
	}
	return s.runDescriptorBarrier("visible_binding_parent_synced")
}

func (s *Store) cleanupVisibleReplacement(replacement visibleReplacement) error {
	if replacement.old == nil {
		return nil
	}
	verified, err := s.verifyVisibleArtifactProof(context.Background(), replacement.key, replacement.key.claimPath(), replacement.old)
	if err != nil {
		return err
	}
	removed, err := s.fdRemoveClaimOwned(claimZonePath(replacement.key.claimPath()), verified, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.runDescriptorBarrier("visible_claim_removed"); err != nil {
		return err
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("visible_claim_parent_synced"); err != nil {
		return err
	}
	witness, err := s.rootFD.Lstat(replacement.key.witnessPath(false))
	if err != nil {
		return err
	}
	return s.removeVisibleProof(replacement.key, witness)
}
