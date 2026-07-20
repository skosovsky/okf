//go:build (!darwin && !linux) || ios || android

package fs

import "os"

func testLockExclusive(*os.File) error { return platformOpenError() }

func testUnlockFile(*os.File) error { return platformOpenError() }
