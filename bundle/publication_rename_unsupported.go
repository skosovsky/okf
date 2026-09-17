//go:build !linux && !darwin && !windows

package bundle

import (
	"fmt"
	"os"
	"runtime"
)

func renamePublicationNoReplace(_ *os.Root, oldName, newName string) error {
	return &os.LinkError{
		Op:  "publication rename no-replace",
		Old: oldName,
		New: newName,
		Err: fmt.Errorf(
			"%w: %s",
			errPublicationNoReplaceUnsupported,
			runtime.GOOS,
		),
	}
}
