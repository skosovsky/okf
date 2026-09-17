//go:build darwin || linux

package bundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCreatePublicationArtifactAfterOpenSwapPreservesForeignFIFO(t *testing.T) {
	t.Parallel()

	// Arrange.
	rootPath := t.TempDir()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	injected := errors.New("injected after-open FIFO swap")
	privateName := ""

	// Act.
	spec, err := createPublicationArtifact(
		context.Background(),
		root,
		"artifact",
		0o640,
		[]byte("publication payload\n"),
		publicationBarrierHooks{
			afterOpen: func(*os.File) error {
				privateName = publicationCoreSinglePrivateName(t, rootPath)
				if removeErr := root.Remove(privateName); removeErr != nil {
					t.Fatal(removeErr)
				}
				if fifoErr := unix.Mkfifo(filepath.Join(rootPath, privateName), 0o600); fifoErr != nil {
					t.Skipf("FIFO is unavailable: %v", fifoErr)
				}
				return injected
			},
		},
	)

	// Assert.
	if privateName == "" || !spec.created || !errors.Is(err, injected) ||
		!errors.Is(err, errPublicationConflict) {
		t.Fatalf("private=%q spec=%+v error=%v; want retained ownership conflict", privateName, spec, err)
	}
	if _, statErr := os.Lstat(filepath.Join(rootPath, "artifact")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("canonical artifact Lstat error=%v, want not exist", statErr)
	}
	info, statErr := os.Lstat(filepath.Join(rootPath, privateName))
	if statErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("foreign FIFO info=%v error=%v", info, statErr)
	}
}

func TestPublicationDescriptorUnixD9AndBlockedPhaseOracleBindings(t *testing.T) {
	t.Parallel()

	// Arrange. The full Unix bundle gate executes these existing behavioral
	// oracles; bindings prevent the descriptor row from becoming free-standing.
	oracles := []func(*testing.T){
		TestCreatePublicationArtifactAfterOpenSwapPreservesForeignFIFO,
		TestRegenerateIndexesRejectsNamedPipeDestinationWithoutOpeningIt,
		TestIndexBlockedAnchoredRestartRejectsMissingTargetWithoutMutation,
	}

	// Act.
	phase, mapped := publicationIndexPhaseForTest(indexBatchV3PhaseBlockedAnchored)

	// Assert.
	for index, oracle := range oracles {
		if oracle == nil {
			t.Fatalf("Unix publication oracle %d is nil", index)
		}
	}
	if phase != publicationTestBlockedAnchored || !mapped {
		t.Fatalf("blocked phase = %q, %v; want exact descriptor mapping", phase, mapped)
	}
	if publicationD9PlatformCapability&publicationD1D9 == 0 {
		t.Fatal("D9 platform capability is absent from the shared invariant vocabulary")
	}
}
