package bundle

import (
	"errors"
	"io/fs"
	"os"
	"strings"
)

var errPublicationNoReplaceUnsupported = errors.New("atomic no-replace publication rename unsupported")

func validatePublicationRenameLeaf(oldName, newName string) error {
	if !validPublicationRenameLeaf(oldName) || !validPublicationRenameLeaf(newName) {
		return &os.LinkError{
			Op:  "publication rename no-replace",
			Old: oldName,
			New: newName,
			Err: fs.ErrInvalid,
		}
	}
	return nil
}

func validPublicationRenameLeaf(name string) bool {
	return name != "" &&
		name != "." &&
		name != ".." &&
		!strings.ContainsAny(name, `/\:`) &&
		!strings.ContainsRune(name, 0) &&
		!strings.HasSuffix(name, ".") &&
		!strings.HasSuffix(name, " ")
}
