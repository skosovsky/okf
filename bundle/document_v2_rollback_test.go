package bundle

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDocumentV2ActiveRollbackConsumesRestoreToken(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	failForwardInstall := errors.New("fail after forward install")
	var anchorName, restoreName, discardName, stageName string
	var anchorInfo os.FileInfo
	anchorDurable := false
	restoreDurable := false
	discardDurable := false
	restored := false
	forwardFailed := false
	hooks := documentPublishHooks{
		afterAnchorSync: func(parent *os.Root, name string) error {
			anchorName = name
			var err error
			anchorInfo, err = parent.Lstat(name)
			if err != nil {
				return err
			}
			anchorDurable = true
			return nil
		},
		afterRestoreSync: func(parent *os.Root, name string) error {
			restoreName = name
			restore, err := parent.Lstat(name)
			if err != nil ||
				anchorInfo == nil ||
				!os.SameFile(restore, anchorInfo) {
				return errors.New("durable R is detached from retained A")
			}
			restoreDurable = true
			return nil
		},
		afterDiscardSync: func(parent *os.Root, name string) error {
			discardName = name
			var err error
			stageName, err = documentV2ForwardArtifactName(parent, "stage")
			if err != nil {
				return err
			}
			stage, err := parent.Lstat(stageName)
			if err != nil {
				return err
			}
			discard, err := parent.Lstat(discardName)
			if err != nil || !os.SameFile(stage, discard) {
				return errors.New("durable D is detached from retained S")
			}
			if _, err := parent.Lstat("concept.md"); !errors.Is(err, fs.ErrNotExist) {
				return errors.New("target is not missing after S to D transition")
			}
			discardDurable = true
			return nil
		},
		afterInstall: func(parent *os.Root, leaf string) error {
			if !forwardFailed {
				forwardFailed = true
				return failForwardInstall
			}
			targetInfo, err := parent.Lstat(leaf)
			if err != nil ||
				anchorInfo == nil ||
				!os.SameFile(targetInfo, anchorInfo) {
				return errors.New("restored target is detached from retained A")
			}
			if _, err := parent.Lstat(restoreName); !errors.Is(err, fs.ErrNotExist) {
				return errors.New("consumed R still exists")
			}
			stage, err := parent.Lstat(stageName)
			if err != nil {
				return err
			}
			discard, err := parent.Lstat(discardName)
			if err != nil || !os.SameFile(stage, discard) {
				return errors.New("restored state lost D to S proof")
			}
			restored = true
			return nil
		},
	}
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: hooks},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))

	// Act.
	err = session.RewriteContext(context.Background(), replacement)

	// Assert.
	if !errors.Is(err, failForwardInstall) {
		t.Fatalf("RewriteContext() error = %v, want injected failure", err)
	}
	if !anchorDurable || !restoreDurable || !discardDurable || !restored {
		t.Fatalf(
			"rollback checkpoints A=%t R=%t D=%t restored=%t",
			anchorDurable,
			restoreDurable,
			discardDurable,
			restored,
		)
	}
	targetInfo, statErr := os.Lstat(target)
	if statErr != nil ||
		anchorInfo == nil ||
		!os.SameFile(targetInfo, anchorInfo) ||
		documentSessionReadFile(t, target) != original {
		t.Fatalf("restored target info=%v error=%v anchor=%q", targetInfo, statErr, anchorName)
	}
	documentSessionAssertNoArtifacts(t, root)
}
