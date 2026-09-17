package fs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"sort"
	"strings"
)

func (s *Store) readScratchOwned(ctx context.Context, name string, limit int64, expected os.FileInfo) ([]byte, error) {
	if err := s.runDescriptorBarrier("scratch_ready_open"); err != nil {
		return nil, err
	}
	f, err := s.fdOpen(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	if err := s.runDescriptorBarrier("scratch_ready_stat"); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	actual, statErr := f.Stat()
	if statErr != nil || expected == nil || actual == nil || !actual.Mode().IsRegular() || !os.SameFile(expected, actual) || actual.Size() != limit {
		return nil, errors.Join(errArtifactClaimConflict, statErr, f.Close())
	}
	if err := s.runDescriptorBarrier("scratch_ready_read"); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	data, readErr := readAllContextLimit(ctx, f, limit)
	if readErr != nil {
		return nil, errors.Join(readErr, f.Close())
	}
	if err := s.runDescriptorBarrier("scratch_ready_restat"); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	current, pathErr := s.rootFD.Lstat(name)
	closeErr := f.Close()
	if pathErr != nil || closeErr != nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(actual, current) {
		return nil, errors.Join(errArtifactClaimConflict, pathErr, closeErr)
	}
	return data, nil
}

// cleanupScratchInventory consumes only regular scratch artifacts whose
// immutable binding and hard-link witness agree on the exact inode. A temp-like
// basename without that capability evidence is inert legacy/foreign state.
func (s *Store) cleanupScratchInventory(ctx context.Context) error {
	return s.cleanupScratchInventoryOperation(ctx, "")
}

// cleanupScratchInventoryOperation restricts convergence to one durable
// publication when operation is non-empty. Recovery uses this before removing
// that publication's journal, so a conflicting source token cannot lose the
// receipt-bearing evidence that owns it.
func (s *Store) cleanupScratchInventoryOperation(ctx context.Context, operation string) error {
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
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasSuffix(entry.Name(), ".binding.json") {
			continue
		}
		bindingName := path.Join(claimDirectory, entry.Name())
		raw, err := s.readMetadataLimitObserved(ctx, bindingName, maxJournalManifestRead)
		if err != nil {
			return err
		}
		var record directoryWitnessRecord
		if json.Unmarshal(raw, &record) != nil || record.Role != claimScratch {
			continue
		}
		if record.Version != 1 || record.Identity == "" {
			return errArtifactClaimConflict
		}
		if operation != "" && record.Operation != operation {
			continue
		}
		canonical, _ := json.Marshal(record)
		if string(canonical) != string(raw) {
			return errArtifactClaimConflict
		}
		key, err := validatePrivateArtifactRole(record)
		if err != nil || path.Base(key.bindingPath()) != entry.Name() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		current, currentErr := s.rootFD.Lstat(key.Path)
		claim, claimErr := s.rootFD.Lstat(key.claimPath())
		witness, witnessErr := s.rootFD.Lstat(key.witnessPath(false))
		if errors.Is(witnessErr, os.ErrNotExist) {
			if errors.Is(currentErr, os.ErrNotExist) && errors.Is(claimErr, os.ErrNotExist) {
				if err := s.removeArtifactBinding(key); err != nil {
					return err
				}
				continue
			}
			// Binding alone is not inode proof while an object still exists.
			return errArtifactClaimConflict
		}
		if witnessErr != nil || witness == nil || !witness.Mode().IsRegular() {
			return errors.Join(errArtifactClaimConflict, witnessErr)
		}
		identity, ok := fileIdentityKey(witness)
		if !ok || identity != record.Identity {
			return errArtifactClaimConflict
		}
		switch {
		case currentErr == nil:
			if claimErr == nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(current, witness) {
				return errArtifactClaimConflict
			}
			removed, err := s.consumeOwnedClaim(ctx, key, false)
			if err != nil || !removed {
				return errors.Join(errArtifactClaimConflict, err)
			}
		case claimErr == nil:
			if !errors.Is(currentErr, os.ErrNotExist) || claim == nil || !claim.Mode().IsRegular() || !os.SameFile(claim, witness) {
				return errArtifactClaimConflict
			}
			removed, err := s.consumeOwnedClaim(ctx, key, false)
			if err != nil || !removed {
				return errors.Join(errArtifactClaimConflict, err)
			}
		case errors.Is(currentErr, os.ErrNotExist) && errors.Is(claimErr, os.ErrNotExist):
			if err := s.removeArtifactWitness(key, witness); err != nil {
				return err
			}
		default:
			return errors.Join(currentErr, claimErr)
		}
	}
	return nil
}
