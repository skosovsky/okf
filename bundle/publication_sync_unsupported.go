//go:build !darwin && !linux && !windows

package bundle

import (
	"fmt"
	"os"
	"runtime"
)

func syncPublicationDirectoryPlatform(*os.Root) error {
	return fmt.Errorf(
		"%w: durable directory publication on %s/%s",
		ErrPublicationCapabilityUnsupported,
		runtime.GOOS,
		runtime.GOARCH,
	)
}
