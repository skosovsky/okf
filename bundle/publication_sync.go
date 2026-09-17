//go:build darwin || linux || windows

package bundle

import (
	"errors"
	"os"
)

func syncPublicationDirectoryPlatform(parent *os.Root) error {
	directory, err := parent.Open(".")
	if err != nil {
		return err
	}
	info, statErr := directory.Stat()
	if statErr != nil || !info.IsDir() {
		return errors.Join(statErr, directory.Close())
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}
