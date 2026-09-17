package fs

import (
	"errors"
	"io/fs"
)

// scratchAttemptCompensation is the sole capability for callback-free
// compensation. Its identities originate from the current O_EXCL attempt.
type scratchAttemptCompensation struct {
	store  *Store
	key    artifactClaimKey
	source string
	owned  fs.FileInfo
}

func (c scratchAttemptCompensation) removePartialBinding(binding fs.FileInfo) error {
	removed, err := c.store.fdRemoveClaimOwnedMode(claimZonePath(c.key.bindingPath()), binding, false, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	return c.store.syncDirectory(claimDirectory)
}
