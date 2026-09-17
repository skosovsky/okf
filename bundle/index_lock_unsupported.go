//go:build (!darwin && !linux && !windows) || ios || android

package bundle

import (
	"fmt"
	"os"
	"runtime"
)

func physicalPublicationRootIdentity(*os.Root) (string, error) {
	return "", fmt.Errorf(
		"%w: %s/%s",
		ErrPublicationCapabilityUnsupported,
		runtime.GOOS,
		runtime.GOARCH,
	)
}

func tryAcquireCrossProcessPublicationLock(*os.Root, string) (func(), error) {
	return nil, fmt.Errorf(
		"%w: %s/%s",
		ErrPublicationCapabilityUnsupported,
		runtime.GOOS,
		runtime.GOARCH,
	)
}
