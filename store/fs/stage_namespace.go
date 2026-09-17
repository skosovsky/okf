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

func (k artifactClaimKey) buildPath() string {
	return path.Join(claimDirectory, k.stem()+".build")
}

// createOwnedStageDirectory encodes the stage lifecycle in names, not in a
// second journal: private build -> canonical stage -> private claim -> absence.
// The immutable sentinel is complete and durable before canonical exposure.
func (s *Store) createOwnedStageDirectory(ctx context.Context, key artifactClaimKey) (fs.FileInfo, error) {
	if err := s.fail(s.durableStep(key.Path, StepMkdir)); err != nil {
		return nil, err
	}
	if err := s.ensureClaimNamespace(ctx); err != nil {
		return nil, err
	}
	if _, err := s.rootFD.Lstat(key.claimPath()); err == nil {
		return nil, errArtifactClaimConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if current, err := s.rootFD.Lstat(key.Path); err == nil {
		if _, verifyErr := s.verifyDirectoryWitnessAt(ctx, key, key.Path, current); verifyErr != nil {
			return nil, verifyErr
		}
		return current, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	buildName := key.buildPath()
	build, err := s.rootFD.Lstat(buildName)
	if errors.Is(err, os.ErrNotExist) {
		created, mkdirErr := s.fdMkdirAll(buildName, 0o700)
		if mkdirErr != nil {
			return nil, mkdirErr
		}
		if len(created) != 1 || created[0] != buildName {
			return nil, errArtifactClaimConflict
		}
		build, err = s.rootFD.Lstat(buildName)
		if err != nil || build == nil || !build.IsDir() {
			return nil, errors.Join(errArtifactClaimConflict, err)
		}
		if _, err := s.prepareDirectoryWitnessAt(ctx, key, buildName, build); err != nil {
			return build, err
		}
	} else if err != nil || build == nil || !build.IsDir() {
		return nil, errors.Join(errArtifactClaimConflict, err)
	} else if _, err := s.verifyDirectoryWitnessAt(ctx, key, buildName, build); err != nil {
		// A precreated or torn private build is evidence, never sweepable state.
		return nil, err
	}
	if err := s.syncDirAt(buildName); err != nil {
		return build, err
	}
	if err := s.runDescriptorBarrier("stage_build_directory_synced"); err != nil {
		return build, err
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return build, err
	}
	if err := s.runDescriptorBarrier("stage_build_parent_synced"); err != nil {
		return build, err
	}
	installed, err := s.fdRenameOwnedNoReplace(buildName, key.Path, build, true)
	if err != nil {
		return installed, err
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return installed, err
	}
	if err := s.runDescriptorBarrier("stage_build_source_parent_synced"); err != nil {
		return installed, err
	}
	if err := s.syncStageDirAt(path.Dir(key.Path)); err != nil {
		return installed, err
	}
	if err := s.runDescriptorBarrier("stage_canonical_parent_synced"); err != nil {
		return installed, err
	}
	if err := s.postFault(s.durableStep(key.Path, StepMkdir)); err != nil {
		return installed, err
	}
	return installed, nil
}

// cleanupAttemptStageBuild compensates only the build inode pinned by the
// current live attempt. The immutable external binding remains proof-last;
// recovery never calls this helper and never derives ownership from a name.
func (s *Store) cleanupAttemptStageBuild(ctx context.Context, key artifactClaimKey, owned fs.FileInfo) error {
	current, err := s.rootFD.Lstat(key.buildPath())
	if err != nil || current == nil || owned == nil || !current.IsDir() || !os.SameFile(current, owned) {
		return errors.Join(errArtifactClaimConflict, err)
	}
	binding, err := s.rootFD.Lstat(key.bindingPath())
	if err != nil || binding == nil || !binding.Mode().IsRegular() {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.verifyArtifactBindingRecord(context.WithoutCancel(ctx), key, binding); err != nil {
		return err
	}
	raw, err := s.readMetadataLimitObserved(context.WithoutCancel(ctx), key.bindingPath(), maxJournalManifestRead)
	if err != nil {
		return err
	}
	record, err := decodeDirectoryWitness(raw)
	identity, ok := fileIdentityKey(owned)
	if err != nil || !ok || record.Identity != identity || record.Source != "" {
		return errors.Join(errArtifactClaimConflict, err)
	}
	sentinelName := path.Join(key.buildPath(), directorySentinelName)
	if sentinel, sentinelErr := s.rootFD.Lstat(sentinelName); sentinelErr == nil {
		if _, verifyErr := s.verifyDirectoryWitnessAt(context.WithoutCancel(ctx), key, key.buildPath(), owned); verifyErr != nil {
			return verifyErr
		}
		removed, removeErr := s.fdRemoveClaimOwned(claimZonePath(sentinelName), sentinel, false)
		if removeErr != nil || !removed {
			return errors.Join(errArtifactClaimConflict, removeErr)
		}
		if err := s.syncDirAt(key.buildPath()); err != nil {
			return err
		}
	} else if !errors.Is(sentinelErr, os.ErrNotExist) {
		return sentinelErr
	}
	removed, err := s.fdRemoveClaimOwned(claimZonePath(key.buildPath()), owned, true)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return err
	}
	return s.removeArtifactBinding(key)
}

func (s *Store) removeTrustedStageBuild(ctx context.Context, key artifactClaimKey, build fs.FileInfo) error {
	if _, err := s.verifyDirectoryWitnessAt(ctx, key, key.buildPath(), build); err != nil {
		return err
	}
	sentinelName := path.Join(key.buildPath(), directorySentinelName)
	sentinel, err := s.rootFD.Lstat(sentinelName)
	if err != nil || sentinel == nil || !sentinel.Mode().IsRegular() {
		return errors.Join(errArtifactClaimConflict, err)
	}
	removed, err := s.fdRemoveClaimOwned(claimZonePath(sentinelName), sentinel, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.syncDirAt(key.buildPath()); err != nil {
		return err
	}
	removed, err = s.fdRemoveClaimOwned(claimZonePath(key.buildPath()), build, true)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	return s.syncDirAt(claimDirectory)
}

func (s *Store) syncStageClaimSourceParent(key artifactClaimKey) error {
	parent := path.Dir(key.Path)
	if err := s.syncDirAt(parent); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for safeStageDirectory(path.Join(parent, "payload")) || strings.HasPrefix(parent, path.Join(internalDirectory, "staging")+"/") {
		ancestor, err := newArtifactClaimKey(key.Operation, parent, claimStageDir)
		if err != nil {
			return err
		}
		if info, err := s.rootFD.Lstat(ancestor.claimPath()); err == nil && info != nil && info.IsDir() {
			if err := s.syncDirAt(ancestor.claimPath()); err != nil {
				return err
			}
			return s.runDescriptorBarrier("stage_claim_ancestor_parent_synced")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if parent == path.Join(internalDirectory, "staging") {
			break
		}
		parent = path.Dir(parent)
	}
	return errArtifactClaimConflict
}

// cleanupStageNamespaceClaims converges only closed, sentinel-bound namespace
// states. Duplicate build/canonical/claim names are preserved as conflicts.
func (s *Store) cleanupStageNamespaceClaims(ctx context.Context) error {
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
	// External bindings are the proof-last tail. They let recovery finish a
	// claim whose in-directory sentinel and directory were removed in separate
	// crashable steps, without trusting the claim basename alone.
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".binding.json") {
			continue
		}
		raw, err := s.readMetadataLimitObserved(ctx, path.Join(claimDirectory, entry.Name()), maxJournalManifestRead)
		if err != nil {
			return err
		}
		record, err := decodeDirectoryWitness(raw)
		if err != nil || record.Role != claimStageDir {
			continue
		}
		key, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
		if err != nil || path.Base(key.bindingPath()) != entry.Name() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		public, publicErr := s.rootFD.Lstat(key.Path)
		build, buildErr := s.rootFD.Lstat(key.buildPath())
		claim, claimErr := s.rootFD.Lstat(key.claimPath())
		if publicErr == nil || buildErr == nil {
			_ = public
			_ = build
			continue
		}
		if claimErr == nil {
			if claim == nil || !claim.IsDir() {
				return errArtifactClaimConflict
			}
			if _, sentinelErr := s.rootFD.Lstat(key.claimedDirectoryWitnessPath()); sentinelErr == nil {
				continue
			} else if !errors.Is(sentinelErr, os.ErrNotExist) {
				return sentinelErr
			}
			identity, ok := fileIdentityKey(claim)
			if !ok || identity != record.Identity {
				return errArtifactClaimConflict
			}
			removed, err := s.fdRemoveClaimOwned(claimZonePath(key.claimPath()), claim, true)
			if err != nil || !removed {
				return errors.Join(errArtifactClaimConflict, err)
			}
			if err := s.syncDirAt(claimDirectory); err != nil {
				return err
			}
			if err := s.removeArtifactBinding(key); err != nil {
				return err
			}
			continue
		}
		if errors.Is(publicErr, os.ErrNotExist) && errors.Is(buildErr, os.ErrNotExist) && errors.Is(claimErr, os.ErrNotExist) {
			if err := s.removeArtifactBinding(key); err != nil {
				return err
			}
		}
	}
	type namespaceItem struct {
		name string
		key  artifactClaimKey
		info fs.FileInfo
	}
	items := make([]namespaceItem, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".build") || strings.HasSuffix(entry.Name(), ".claim")) {
			continue
		}
		name := path.Join(claimDirectory, entry.Name())
		info, err := s.rootFD.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info == nil || !info.IsDir() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		raw, err := s.readMetadataLimitObserved(ctx, path.Join(name, directorySentinelName), maxJournalManifestRead)
		if err != nil {
			return errors.Join(errArtifactClaimConflict, err)
		}
		record, err := decodeDirectoryWitness(raw)
		if err != nil || record.Role != claimStageDir {
			if err != nil {
				return err
			}
			continue
		}
		key, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
		if err != nil {
			return err
		}
		wantName := key.claimPath()
		if strings.HasSuffix(entry.Name(), ".build") {
			wantName = key.buildPath()
		}
		if name != wantName {
			return errArtifactClaimConflict
		}
		items = append(items, namespaceItem{name: name, key: key, info: info})
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := strings.Count(items[i].key.Path, "/"), strings.Count(items[j].key.Path, "/")
		if left != right {
			return left > right
		}
		return items[i].name < items[j].name
	})
	for _, item := range items {
		name, key, info := item.name, item.key, item.info
		if _, err := s.rootFD.Lstat(key.Path); err == nil {
			return errArtifactClaimConflict
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		other := key.claimPath()
		if name == other {
			other = key.buildPath()
		}
		if _, err := s.rootFD.Lstat(other); err == nil {
			return errArtifactClaimConflict
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if name == key.buildPath() {
			if err := s.removeTrustedStageBuild(ctx, key, info); err != nil {
				return err
			}
			continue
		}
		removed, err := s.consumeOwnedClaim(ctx, key, true)
		if err != nil || !removed {
			return errors.Join(errArtifactClaimConflict, err)
		}
	}
	return nil
}
