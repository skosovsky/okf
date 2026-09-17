//go:build (!darwin && !linux) || ios || android

package fs

import (
	"io/fs"
)

func (s *Store) fdRenameOwnedNoReplace(string, string, fs.FileInfo, bool) (fs.FileInfo, error) {
	return nil, platformOpenError()
}

func (s *Store) fdRenameOwnedNoReplaceMode(string, string, fs.FileInfo, bool, bool) (fs.FileInfo, error) {
	return nil, platformOpenError()
}

func (s *Store) fdLinkOwnedNoReplace(string, string, fs.FileInfo) error {
	return platformOpenError()
}

func (s *Store) fdLinkOwnedNoReplaceMode(string, string, fs.FileInfo, bool) error {
	return platformOpenError()
}

func fileIdentityKey(fs.FileInfo) (string, bool) { return "", false }
