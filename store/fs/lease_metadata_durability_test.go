package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateDirectoryModeRepairPostFaultIsTerminalAndIndependentStoreResyncs(t *testing.T) {
	// Arrange.
	root := precreatePrivateDirectory(t, 0o755)
	crashErr := errors.New("private directory chmod post-fault")
	var crashTrace []string
	config := Config{
		Fault: func(step Step) error {
			crashTrace = append(crashTrace, "pre:"+string(step))
			return nil
		},
		PostFault: func(step Step) error {
			crashTrace = append(crashTrace, "post:"+string(step))
			if step == StepPrivateDirectoryChmod {
				return crashErr
			}
			return nil
		},
	}

	// Act.
	failed, openErr := Open(root, config)
	if openErr != nil {
		t.Fatal(openErr)
	}
	_, snapshotErr := failed.Snapshot(context.Background())

	// Assert: chmod is visible, but its erroring PostFault is terminal for the
	// first operation that lazily initializes the private namespace.
	if !errors.Is(snapshotErr, crashErr) || len(crashTrace) == 0 || crashTrace[len(crashTrace)-1] != "post:"+string(StepPrivateDirectoryChmod) {
		t.Fatalf("Snapshot error=%v trace=%#v", snapshotErr, crashTrace)
	}
	privateInfo, err := os.Stat(filepath.Join(root, internalDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if privateInfo.Mode().Perm() != 0o700 {
		t.Fatalf("private directory mode after interrupted repair=%v, want 0700", privateInfo.Mode().Perm())
	}
	if err := failed.Close(); err != nil {
		t.Fatal(err)
	}

	var secondTrace []Step
	second, err := Open(root, Config{PostFault: func(step Step) error {
		secondTrace = append(secondTrace, step)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if privateSyncAt, capabilityAt := firstStepIndex(secondTrace, StepPrivateDirectorySync), firstStepIndex(secondTrace, StepCapabilityMkdir); privateSyncAt < 0 || capabilityAt < 0 || privateSyncAt >= capabilityAt {
		t.Fatalf("independent Store did not sync the private root before private setup: trace=%#v", secondTrace)
	}
	if !second.privateReady {
		t.Fatal("successful independent Store did not retain private namespace durability evidence")
	}
	secondTrace = nil
	if _, err := second.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if firstStepIndex(secondTrace, StepCapabilityMkdir) >= 0 {
		t.Fatalf("same Store repeated private namespace initialization: trace=%#v", secondTrace)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	var thirdTrace []Step
	third, err := Open(root, Config{PostFault: func(step Step) error {
		thirdTrace = append(thirdTrace, step)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, third)
	if _, err := third.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if privateSyncAt, capabilityAt := firstStepIndex(thirdTrace, StepPrivateDirectorySync), firstStepIndex(thirdTrace, StepCapabilityMkdir); privateSyncAt < 0 || capabilityAt < 0 || privateSyncAt >= capabilityAt {
		t.Fatalf("third independent Store did not establish its own private namespace durability: trace=%#v", thirdTrace)
	}
}

func TestModeCorrectPrivateNamespaceInitializationRunsOncePerStore(t *testing.T) {
	// Arrange.
	root := precreatePrivateDirectory(t, 0o700)
	var trace []Step

	// Act.
	s, err := Open(root, Config{PostFault: func(step Step) error {
		trace = append(trace, step)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Assert.
	if privateSyncAt, capabilityAt := firstStepIndex(trace, StepPrivateDirectorySync), firstStepIndex(trace, StepCapabilityMkdir); privateSyncAt < 0 || capabilityAt < 0 || privateSyncAt >= capabilityAt {
		t.Fatalf("mode-correct private root was not synced before first use: trace=%#v", trace)
	}
	if !s.privateReady {
		t.Fatal("Store did not retain successful private namespace durability evidence")
	}
	trace = nil
	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if firstStepIndex(trace, StepCapabilityMkdir) >= 0 {
		t.Fatalf("same Store repeated private namespace initialization: trace=%#v", trace)
	}
}

func precreatePrivateDirectory(t *testing.T, mode os.FileMode) string {
	t.Helper()
	root := t.TempDir()
	private := filepath.Join(root, internalDirectory)
	if err := os.MkdirAll(private, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(private, mode); err != nil {
		t.Fatal(err)
	}
	return root
}

func firstStepIndex(steps []Step, want Step) int {
	for i, step := range steps {
		if step == want {
			return i
		}
	}
	return -1
}
