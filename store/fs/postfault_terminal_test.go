package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRemovePostFaultIsTerminalAndRetrySyncsExactlyOnce(t *testing.T) {
	// Arrange.
	root, s := openPostFaultStore(t)
	if err := os.WriteFile(filepath.Join(root, "victim.bin"), []byte("victim"), 0o600); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("remove post-fault")
	trace := armPostFaultTrace(s, StepRemove, injected)

	// Act.
	expected, captureErr := s.captureMutationTarget("victim.bin")
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	expected.operation = internalArtifactOperation("postfault-remove-victim")
	err := s.removeVisibleGuarded(context.Background(), "victim.bin", expected)

	// Assert.
	if err != injected {
		t.Fatalf("remove error=%v, want injected", err)
	}
	if len(*trace) < 2 || (*trace)[0] != "pre:remove" || (*trace)[len(*trace)-1] != "post:remove" {
		t.Fatalf("crash trace=%q, want public remove boundaries; private claim phases have named owners", *trace)
	}
	if _, statErr := os.Stat(filepath.Join(root, "victim.bin")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("removed file stat error=%v, want not exist", statErr)
	}

	retryTrace := armPostFaultTrace(s, "", nil)
	retryExpected := mutationTargetIdentity{absent: true, operation: expected.operation}
	if err := s.removeVisibleGuarded(context.Background(), "victim.bin", retryExpected); err != nil {
		t.Fatal(err)
	}
	wantRetryTrace := []string{"pre:remove"}
	if !reflect.DeepEqual(*retryTrace, wantRetryTrace) {
		t.Fatalf("retry trace=%q want=%q", *retryTrace, wantRetryTrace)
	}
}

func TestCleanupTempPreservesUnprovenScratchWithoutHooks(t *testing.T) {
	// Arrange.
	root, s := openPostFaultStore(t)
	const scratch = temporaryDirectory + "/.okf-tmp-retained"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(scratch)), []byte("scratch"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(root, filepath.FromSlash(scratch)))
	if err != nil {
		t.Fatal(err)
	}
	trace := armPostFaultTrace(s, StepTempCleanupRemove, errors.New("must not fire"))

	// Act.
	err = s.cleanupScratchInventory(t.Context())

	// Assert.
	after, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(scratch)))
	got, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(scratch)))
	if err != nil || len(*trace) != 0 || statErr != nil || readErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || string(got) != "scratch" {
		t.Fatalf("cleanup=%v trace=%q before=%v after=%v stat=%v read=%v bytes=%q", err, *trace, before, after, statErr, readErr, got)
	}
}

func TestWriteDurableAtPostFaultRunsNoLaterDurabilityHooks(t *testing.T) {
	steps := []Step{
		StepChmod,
		StepFileWrite,
		StepFileSync,
		StepFileClose,
		StepRename,
		StepTempCleanupDirectorySync,
		StepDirectorySync,
	}
	fullTrace := []string{
		"pre:chmod", "post:chmod",
		"pre:file_write", "post:file_write",
		"pre:file_sync", "post:file_sync",
		"pre:file_close", "post:file_close",
		"pre:rename", "post:rename",
		"pre:temp_cleanup_directory_sync", "post:temp_cleanup_directory_sync",
		"pre:directory_sync", "post:directory_sync",
	}

	for index, step := range steps {
		t.Run(string(step), func(t *testing.T) {
			// Arrange.
			root, s := openPostFaultStore(t)
			injected := errors.New("write post-fault")
			trace := armPostFaultTrace(s, step, injected)

			// Act.
			err := s.writeDurableAtForTest(context.Background(), "asset.bin", []byte("payload"), 0o600)

			// Assert.
			if err != injected {
				t.Fatalf("durable write error=%v, want injected", err)
			}
			filtered := (*trace)[:0]
			for _, event := range *trace {
				if strings.Contains(event, "claim_directory_sync") || strings.Contains(event, "private_directory_") || event == "pre:mkdir" || event == "post:mkdir" {
					continue
				}
				filtered = append(filtered, event)
				if event == "post:"+string(step) {
					break // Named R5 proof compensation owns the remaining trace.
				}
			}
			want := fullTrace[:2*(index+1)]
			if !reflect.DeepEqual(filtered, want) {
				t.Fatalf("trace=%q filtered=%q want=%q", *trace, filtered, want)
			}
			scratch := postFaultScratchFiles(t, root)
			if index >= 4 {
				if len(scratch) != 0 {
					t.Fatalf("post-rename scratch=%q, want none", scratch)
				}
				if data, readErr := os.ReadFile(filepath.Join(root, "asset.bin")); readErr != nil || string(data) != "payload" {
					t.Fatalf("post-rename target=%q read=%v", data, readErr)
				}
			} else {
				if len(scratch) != 1 {
					t.Fatalf("pre-rename scratch=%q, want one retained orphan", scratch)
				}
				if _, statErr := os.Stat(filepath.Join(root, "asset.bin")); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("pre-rename target stat=%v, want not exist", statErr)
				}
			}

			s.config.Fault = nil
			s.config.PostFault = nil
			reopened, reopenErr := openObserved(root, Config{})
			if reopenErr != nil {
				t.Fatal(reopenErr)
			}
			registerStoreCleanup(t, reopened)
			if remaining := postFaultScratchFiles(t, root); len(remaining) != 0 {
				t.Fatalf("recovery scratch=%q, want none", remaining)
			}
		})
	}
}

func TestOrphanTempRecoverySyncsEmptySourceDirectoryExactlyOnce(t *testing.T) {
	// Arrange.
	root, s := openPostFaultStore(t)
	if scratch := postFaultScratchFiles(t, root); len(scratch) != 0 {
		t.Fatalf("initial scratch=%q, want none", scratch)
	}
	trace := armPostFaultTrace(s, "", nil)

	// Act.
	err := s.cleanupOrphanTemps(t.Context())

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pre:temp_cleanup_directory_sync",
		"post:temp_cleanup_directory_sync",
	}
	if !reflect.DeepEqual(*trace, want) {
		t.Fatalf("empty recovery trace=%q want=%q", *trace, want)
	}
}

func TestCapabilityProbeRemovalSyncsParentExactlyOnce(t *testing.T) {
	// Arrange.
	_, s := openPostFaultStore(t)
	trace := armPostFaultTrace(s, "", nil)

	// Act.
	_, err := s.detectPathAliasProbe("case-probe-b", "CASE-PROBE-B")

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	removeAt := -1
	for index, event := range *trace {
		if event == "post:capability_remove" {
			removeAt = index
		}
	}
	if removeAt < 0 {
		t.Fatalf("trace lacks capability removal: %q", *trace)
	}
	wantTail := []string{
		"post:capability_remove",
		"pre:capability_directory_sync",
		"post:capability_directory_sync",
	}
	if got := (*trace)[removeAt:]; !reflect.DeepEqual(got, wantTail) {
		t.Fatalf("capability removal tail=%q want=%q", got, wantTail)
	}
}

func openPostFaultStore(t *testing.T) (string, *Store) {
	t.Helper()
	root := t.TempDir()
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	return root, s
}

func armPostFaultTrace(s *Store, failAt Step, injected error) *[]string {
	trace := make([]string, 0)
	fired := false
	s.config.Fault = func(step Step) error {
		trace = append(trace, "pre:"+string(step))
		return nil
	}
	s.config.PostFault = func(step Step) error {
		trace = append(trace, "post:"+string(step))
		if !fired && step == failAt {
			fired = true
			return injected
		}
		return nil
	}
	return &trace
}

func postFaultScratchFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(temporaryDirectory)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".okf-tmp-") {
			names = append(names, entry.Name())
		}
	}
	return names
}
