package bundle

import "os"

// PublishNewDirectory installs a fully staged directory without replacing an
// existing target. Both names must be direct children of parent. The caller
// owns staging, validation, cleanup, and directory durability barriers.
func PublishNewDirectory(parent *os.Root, stagedName, targetName string) error {
	return renamePublicationNoReplace(parent, stagedName, targetName)
}

// SyncPublicationDirectory makes a completed directory-name change durable on
// platforms supported by the bundle publication protocol.
func SyncPublicationDirectory(parent *os.Root) error {
	return syncPublicationDirectoryPlatform(parent)
}
