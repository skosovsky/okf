package bundle

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"
)

func TestGuardedConsumeIndexNewInstallStateCuts(t *testing.T) {
	t.Parallel()

	cutErr := errors.New("phase2 install cut")
	cases := []struct {
		name          string
		hooks         func() publicationBarrierHooks
		wantInstalled bool
		wantTarget    bool
		wantNew       bool
	}{
		{
			name: "before rename",
			hooks: func() publicationBarrierHooks {
				return publicationBarrierHooks{
					beforeInstall: func() error { return cutErr },
				}
			},
			wantNew: true,
		},
		{
			name: "after rename before directory sync",
			hooks: func() publicationBarrierHooks {
				return publicationBarrierHooks{
					afterInstall: func() error { return cutErr },
				}
			},
			wantInstalled: true,
			wantTarget:    true,
		},
		{
			name: "after directory sync",
			hooks: func() publicationBarrierHooks {
				return publicationBarrierHooks{
					afterDirectorySync: func() error { return cutErr },
				}
			},
			wantInstalled: true,
			wantTarget:    true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			parent, stage, install := newIndexV3InstallFixture(t)

			// Act.
			installed, err := guardedConsumePublicationInstallLeaf(
				context.Background(),
				parent,
				"new-install",
				install,
				"stage",
				stage,
				"index.md",
				testCase.hooks(),
			)

			// Assert.
			if errors.Is(err, errPublicationNoReplaceUnsupported) {
				t.Skip("atomic no-replace rename is unsupported on this platform")
			}
			if !errors.Is(err, cutErr) {
				t.Fatalf("guardedConsumePublicationInstallLeaf() error = %v, want cut", err)
			}
			if installed != testCase.wantInstalled {
				t.Fatalf("installed = %v, want %v", installed, testCase.wantInstalled)
			}
			assertIndexV3InstallState(
				t,
				parent,
				stage.info,
				testCase.wantNew,
				testCase.wantTarget,
			)
		})
	}
}

func TestGuardedConsumeIndexNewInstallNeverOverwritesForeignTarget(t *testing.T) {
	t.Parallel()

	// Arrange.
	parent, stage, install := newIndexV3InstallFixture(t)
	foreignFile, err := parent.OpenFile("index.md", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreignFile.Write([]byte("foreign\n")); err != nil {
		_ = foreignFile.Close()
		t.Fatal(err)
	}
	if err := foreignFile.Close(); err != nil {
		t.Fatal(err)
	}
	foreign, err := parent.Lstat("index.md")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	installed, installErr := guardedConsumePublicationInstallLeaf(
		context.Background(),
		parent,
		"new-install",
		install,
		"stage",
		stage,
		"index.md",
		publicationBarrierHooks{},
	)

	// Assert.
	if installed {
		t.Fatal("installed = true, want no mutation")
	}
	if installErr == nil {
		t.Fatal("guardedConsumePublicationInstallLeaf() error = nil, want occupied target")
	}
	after, err := parent.Lstat("index.md")
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(foreign, after) {
		t.Fatal("foreign target identity changed")
	}
	retainedStage, err := parent.Lstat("stage")
	if err != nil || !os.SameFile(stage.info, retainedStage) {
		t.Fatalf("retained S changed: info=%v error=%v", retainedStage, err)
	}
	retainedNew, err := parent.Lstat("new-install")
	if err != nil || !os.SameFile(stage.info, retainedNew) {
		t.Fatalf("retained N changed: info=%v error=%v", retainedNew, err)
	}
}

func newIndexV3InstallFixture(
	t *testing.T,
) (*os.Root, publicationFileSpec, publicationFileSpec) {
	t.Helper()
	parent, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	stage, err := createPublicationArtifact(
		context.Background(),
		parent,
		"stage",
		0o644,
		[]byte("# generated\n"),
		publicationBarrierHooks{},
	)
	if err != nil {
		t.Fatal(err)
	}
	install, err := createDurablePublicationWitness(
		context.Background(),
		parent,
		"stage",
		stage,
		"new-install",
		publicationBarrierHooks{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stage.info == nil || install.info == nil || !os.SameFile(stage.info, install.info) {
		t.Fatal("fixture N does not alias retained S")
	}
	return parent, stage, install
}

func assertIndexV3InstallState(
	t *testing.T,
	parent *os.Root,
	stageIdentity os.FileInfo,
	wantNew bool,
	wantTarget bool,
) {
	t.Helper()
	stage, err := parent.Lstat("stage")
	if err != nil || !os.SameFile(stageIdentity, stage) {
		t.Fatalf("retained S changed: info=%v error=%v", stage, err)
	}
	assertAlias := func(name string, want bool) {
		t.Helper()
		info, err := parent.Lstat(name)
		if !want {
			if !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Lstat(%q) error = %v, want absent", name, err)
			}
			return
		}
		if err != nil || !os.SameFile(stageIdentity, info) {
			t.Fatalf("%q does not alias retained S: info=%v error=%v", name, info, err)
		}
	}
	assertAlias("new-install", wantNew)
	assertAlias("index.md", wantTarget)
}
