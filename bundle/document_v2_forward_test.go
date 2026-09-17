package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDocumentV2ForwardConsumesDurableNewInstallToken(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	var newInstallName string
	var stageName string
	var stageInfo os.FileInfo
	newInstallDurable := false
	manifestDurable := false
	installed := false
	hooks := documentPublishHooks{
		afterNewInstallSync: func(parent *os.Root, name string) error {
			newInstallName = name
			var err error
			stageName, err = documentV2ForwardArtifactName(parent, "stage")
			if err != nil {
				return err
			}
			stageInfo, err = parent.Lstat(stageName)
			if err != nil {
				return err
			}
			newInstallInfo, err := parent.Lstat(newInstallName)
			if err != nil || !os.SameFile(stageInfo, newInstallInfo) {
				return errors.New("durable N does not alias retained S")
			}
			if _, err := documentV2ForwardArtifactName(parent, "manifest"); err == nil {
				return errors.New("manifest appeared before durable N")
			}
			newInstallDurable = true
			return nil
		},
		afterManifestSync: func(parent *os.Root, name string) error {
			data, err := parent.ReadFile(name)
			if err != nil {
				return err
			}
			var manifest documentTransactionManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				return err
			}
			if filepath.Base(filepath.FromSlash(manifest.Stage)) != stageName ||
				filepath.Base(filepath.FromSlash(manifest.NewInstall)) != newInstallName {
				return errors.New("manifest does not bind durable S/N paths")
			}
			manifestDurable = true
			return nil
		},
		afterInstall: func(parent *os.Root, leaf string) error {
			current, err := parent.Lstat(leaf)
			if err != nil || stageInfo == nil || !os.SameFile(current, stageInfo) {
				return errors.New("published target does not alias retained S")
			}
			if _, err := parent.Lstat(newInstallName); !errors.Is(err, fs.ErrNotExist) {
				return errors.New("consumed N still exists")
			}
			if _, err := parent.Lstat(stageName); err != nil {
				return errors.New("retained S disappeared during N consumption")
			}
			installed = true
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
	if err != nil {
		t.Fatalf("RewriteContext() error = %v", err)
	}
	if !newInstallDurable || !manifestDurable || !installed {
		t.Fatalf(
			"forward checkpoints N=%t M=%t install=%t",
			newInstallDurable,
			manifestDurable,
			installed,
		)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func documentV2ForwardArtifactName(parent *os.Root, wantKind string) (string, error) {
	directory, err := parent.Open(".")
	if err != nil {
		return "", err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", err
	}
	for _, entry := range entries {
		kind, _, ok := parseDocumentArtifactName(entry.Name())
		if ok && kind == wantKind {
			return entry.Name(), nil
		}
	}
	return "", fs.ErrNotExist
}
