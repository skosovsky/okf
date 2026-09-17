//go:build windows

package bundle

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

const (
	indexWindowsMutexHelperIdentity = "OKF_INDEX_MUTEX_HELPER_IDENTITY"
	indexWindowsMutexHelperRoot     = "OKF_INDEX_MUTEX_HELPER_ROOT"
	indexWindowsMutexHelperWant     = "OKF_INDEX_MUTEX_HELPER_WANT"
)

func TestIndexPhysicalRootIdentityWindows(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	firstRoot, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatalf("open first root: %v", err)
	}
	defer firstRoot.Close()
	secondRoot, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatalf("open second root: %v", err)
	}
	defer secondRoot.Close()

	first, err := physicalPublicationRootIdentity(firstRoot)
	if err != nil {
		t.Fatalf("identify first root: %v", err)
	}
	second, err := physicalPublicationRootIdentity(secondRoot)
	if err != nil {
		t.Fatalf("identify second root: %v", err)
	}

	if first == "" {
		t.Fatal("physical root identity is empty")
	}
	if first != second {
		t.Fatalf("physical root identity is unstable: first %q, second %q", first, second)
	}
}

func TestPublicationLockCapabilityProbeWindowsDoesNotMutateNamespace(t *testing.T) {
	t.Parallel()

	// Arrange.
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Act.
	release, err := acquirePublicationLock(root)
	if release != nil {
		release()
	}

	// Assert.
	if err != nil && !errors.Is(err, ErrPublicationCapabilityUnsupported) {
		t.Fatalf("acquirePublicationLock() error = %v, want success or capability sentinel", err)
	}
	entries, readErr := os.ReadDir(directory)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("publication lock probe mutated namespace: %#v", entries)
	}
}

func TestIndexCrossProcessLockWindows(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()
	identity, err := physicalPublicationRootIdentity(root)
	if err != nil {
		t.Fatalf("identify root: %v", err)
	}

	release, err := tryAcquireCrossProcessPublicationLock(root, identity)
	if err != nil {
		t.Fatalf("acquire parent lock: %v", err)
	}
	defer release()

	runIndexWindowsMutexHelper(t, directory, identity, "conflict")
	release()
	runIndexWindowsMutexHelper(t, directory, identity, "acquire")
}

func TestIndexWindowsCrossProcessLockHelper(t *testing.T) {
	identity := os.Getenv(indexWindowsMutexHelperIdentity)
	if identity == "" {
		t.Skip("helper process only")
	}

	root, err := os.OpenRoot(os.Getenv(indexWindowsMutexHelperRoot))
	if err != nil {
		t.Fatalf("open helper root: %v", err)
	}
	defer root.Close()

	release, err := tryAcquireCrossProcessPublicationLock(root, identity)
	switch os.Getenv(indexWindowsMutexHelperWant) {
	case "conflict":
		if !errors.Is(err, ErrPublicationOwnershipConflict) {
			t.Fatalf("expected regeneration conflict, got %v", err)
		}
		if release != nil {
			t.Fatal("conflicting acquisition returned a release function")
		}
	case "acquire":
		if err != nil {
			t.Fatalf("acquire released mutex: %v", err)
		}
		if release == nil {
			t.Fatal("successful acquisition returned nil release")
		}
		release()
	default:
		t.Fatalf("unknown helper expectation %q", os.Getenv(indexWindowsMutexHelperWant))
	}
}

func runIndexWindowsMutexHelper(t *testing.T, root, identity, want string) {
	t.Helper()

	command := exec.Command(
		os.Args[0],
		"-test.run=^TestIndexWindowsCrossProcessLockHelper$",
	)
	command.Env = append(
		os.Environ(),
		indexWindowsMutexHelperIdentity+"="+identity,
		indexWindowsMutexHelperRoot+"="+root,
		indexWindowsMutexHelperWant+"="+want,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run mutex helper (%s): %v\n%s", want, err, output)
	}
}
