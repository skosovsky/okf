//go:build darwin

package bundle

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func renamePublicationNoReplace(parent *os.Root, oldName, newName string) error {
	if err := validatePublicationRenameLeaf(oldName, newName); err != nil {
		return err
	}
	if parent == nil {
		return &os.LinkError{
			Op:  "renameatx_np",
			Old: oldName,
			New: newName,
			Err: fmt.Errorf("%w: nil publication root", os.ErrInvalid),
		}
	}
	root, err := parent.Open(".")
	if err != nil {
		return &os.LinkError{Op: "renameatx_np", Old: oldName, New: newName, Err: err}
	}
	defer root.Close()

	err = unix.RenameatxNp(
		int(root.Fd()),
		oldName,
		int(root.Fd()),
		newName,
		unix.RENAME_EXCL,
	)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.ENOTSUP) {
		err = fmt.Errorf("%w: %w", errPublicationNoReplaceUnsupported, err)
	}
	return &os.LinkError{Op: "renameatx_np", Old: oldName, New: newName, Err: err}
}
