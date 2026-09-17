package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCrashMatrixSyncHarnessRejectsIncompleteImplementationBeforeMetadataSetup(t *testing.T) {
	// Arrange.
	root := t.TempDir()

	// Act.
	store, err := openObservedWithRecoveryLimitsAndSync(
		context.Background(),
		root,
		Config{},
		absoluteStagedManifestLimits(),
		durabilitySyncImplementation{})

	_, metadataErr := os.Stat(filepath.Join(root, internalDirectory))

	// Assert.
	if store != nil {
		t.Fatalf("open returned store %#v with incomplete durability sync implementation", store)
	}
	if err == nil || err.Error() != "fs store: incomplete durability sync implementation" {
		t.Fatalf("open error=%v, want incomplete durability sync implementation", err)
	}
	if !errors.Is(metadataErr, os.ErrNotExist) {
		t.Fatalf("incomplete durability sync implementation touched private metadata: %v", metadataErr)
	}
}

func TestCrashMatrixSyncHarnessPreservesProductionFaultTraceAndState(t *testing.T) {
	// Arrange.
	_, production, _, productionAfter, productionReceipt := rootIndexPublicationFixtureForTransitionWithOpen(
		t,
		rootIndexCreate,
		func(root string) (*Store, error) {
			return observeOpened(Open(root, Config{}))
		},
	)
	_, harness, _, harnessAfter, harnessReceipt := rootIndexCrashMatrixFixtureForTransition(t, rootIndexCreate)
	productionTrace := captureDurabilityTrace(production)
	harnessTrace := captureDurabilityTrace(harness)
	closedFile, err := os.CreateTemp(t.TempDir(), "closed-sync-target")
	if err != nil {
		t.Fatal(err)
	}
	if err := closedFile.Close(); err != nil {
		t.Fatal(err)
	}

	// Act.
	productionFileSyncErr := production.syncFile(closedFile)
	harnessFileSyncErr := harness.syncFile(closedFile)
	productionDirectorySyncErr := production.syncDirectory("missing-directory")
	harnessDirectorySyncErr := harness.syncDirectory("missing-directory")
	productionPublishErr := production.publish(context.Background(), productionAfter, productionReceipt)
	harnessPublishErr := harness.publish(context.Background(), harnessAfter, harnessReceipt)
	productionVisible, productionReadErr := readVisibleRoot(context.Background(), production.rootFD)
	harnessVisible, harnessReadErr := readVisibleRoot(context.Background(), harness.rootFD)

	// Assert.
	if productionFileSyncErr == nil || productionDirectorySyncErr == nil {
		t.Fatalf(
			"production Open did not bind real sync syscalls: file=%v directory=%v",
			productionFileSyncErr,
			productionDirectorySyncErr,
		)
	}
	if harnessFileSyncErr != nil || harnessDirectorySyncErr != nil {
		t.Fatalf("crash harness physical sync replacement failed: file=%v directory=%v", harnessFileSyncErr, harnessDirectorySyncErr)
	}
	if productionPublishErr != nil || harnessPublishErr != nil {
		t.Fatalf("publish errors: production=%v harness=%v", productionPublishErr, harnessPublishErr)
	}
	if !reflect.DeepEqual(*harnessTrace, *productionTrace) {
		t.Fatalf("fault traces differ:\nproduction: %v\nharness:    %v", *productionTrace, *harnessTrace)
	}
	if productionReadErr != nil || harnessReadErr != nil {
		t.Fatalf("visible reads: production=%v harness=%v", productionReadErr, harnessReadErr)
	}
	if !sameVisibleFiles(productionVisible, productionAfter.files) ||
		!sameVisibleFiles(harnessVisible, harnessAfter.files) ||
		!sameVisibleFiles(harnessVisible, productionVisible) {
		t.Fatal("crash harness escaped the exact production publication state")
	}
}

func captureDurabilityTrace(store *Store) *[]string {
	trace := make([]string, 0)
	store.config.Fault = func(step Step) error {
		trace = append(trace, "pre:"+string(step))
		return nil
	}
	store.config.PostFault = func(step Step) error {
		trace = append(trace, "post:"+string(step))
		return nil
	}
	return &trace
}
