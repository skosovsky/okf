package mutation

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestMigrationTerminalAssemblyCancellationReturnsZero(t *testing.T) {
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n"),
	}
	request := MigrationRequest{ID: "terminal-cancel", Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02, Migration: store.V01ToV02Migration{GeneratedBy: "process:migration", TimestampPolicy: store.LegacyTimestampPreserve, TimestampConflict: store.TimestampConflictReject}}
	resolution := testMigrationResolution(t, source)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	preview, err := NewMigrationPlanner().Preview(ctx, source, resolution, request)

	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(preview, MigrationPreview{}) {
		t.Fatalf("Preview() = (%#v, %v), want zero and context.Canceled", preview, err)
	}
}

func TestMigrationTerminalCloneCancellationReturnsZero(t *testing.T) {
	ctx := &countdownContext{Context: context.Background(), remaining: 2}
	preview := MigrationPreview{Blockers: make([]MigrationBlocker, 4096)}

	cloned, err := cloneMigrationPreviewContext(ctx, preview)

	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(cloned, MigrationPreview{}) {
		t.Fatalf("cloneMigrationPreviewContext() = (%#v, %v), want zero and context.Canceled", cloned, err)
	}
}
