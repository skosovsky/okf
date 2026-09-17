package okfcli

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"

	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

func TestMigrateApplyFaultReopenRetryRecoversAtomicState(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("durable filesystem store is unsupported on this platform")
	}

	tests := []struct {
		name               string
		config             func(*bool, *bool, *bool, error) storefs.Config
		wantRecoveredState string
		wantCommitted      bool
	}{
		{
			name: "fault before journal leaves pre-state and retry commits",
			config: func(armed, fired, _ *bool, injected error) storefs.Config {
				return storefs.Config{Fault: func(step storefs.Step) error {
					if *armed && !*fired && step == storefs.StepJournalWrite {
						*fired = true
						return injected
					}
					return nil
				}}
			},
			wantRecoveredState: "pre",
		},
		{
			name: "post fault after durable journal returns receipt and replay completes",
			config: func(armed, fired, journalRenamed *bool, injected error) storefs.Config {
				return storefs.Config{PostFault: func(step storefs.Step) error {
					if !*armed {
						return nil
					}
					if step == storefs.StepJournalRename {
						*journalRenamed = true
						return nil
					}
					if !*fired && *journalRenamed && step == storefs.StepJournalDirectorySync {
						*fired = true
						return injected
					}
					return nil
				}}
			},
			wantRecoveredState: "pre",
			wantCommitted:      true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeMigrationRecoveryFixture(t, root)
			preState := snapshotRevisionVisibleMigrationFiles(t, root)
			injected := errors.New("injected CLI migration durability fault")
			armed := false
			fired := false
			journalRenamed := false
			firstRecorder := &migrationRecoveryRecorder{delegate: mutation.NewMigrationPlanner()}
			firstDependencies := productionMigrateDependencies()
			firstDependencies.planner = firstRecorder
			firstDependencies.openStore = func(ctx context.Context, path string) (migrationStore, error) {
				opened, err := storefs.OpenContext(
					ctx,
					path,
					test.config(&armed, &fired, &journalRenamed, injected),
				)
				if err == nil {
					armed = true
				}
				return opened, err
			}

			// Act: the first invocation stops at the selected durability boundary.
			firstCode, firstReport, firstStdout, firstErr := runMigrationRecoveryCommand(
				t,
				root,
				firstDependencies,
			)
			interruptedState := snapshotRevisionVisibleMigrationFiles(t, root)
			if len(firstRecorder.previews) != 1 || len(firstRecorder.applies) != 1 {
				t.Fatalf("first planner preview/apply calls = %d/%d, want 1/1",
					len(firstRecorder.previews), len(firstRecorder.applies))
			}
			firstPreview := firstRecorder.previews[0]
			firstApply := firstRecorder.applies[0]
			postState := snapshotMigrationSource(t, firstPreview.result.Staged)

			// Assert. A pre-journal fault has no durable outcome. Once the journal
			// is durable, the valid receipt is authoritative even though visible
			// publication may still require normal reopen recovery.
			if !fired || firstPreview.err != nil || !errors.Is(firstApply.err, injected) {
				t.Fatalf("first fired/preview/apply error = %t/%v/%v", fired, firstPreview.err, firstApply.err)
			}
			if test.wantCommitted {
				var committed *store.CommittedError
				if firstErr != nil || firstCode != 0 || len(firstStdout) == 0 ||
					!errors.As(firstApply.err, &committed) ||
					store.ValidateCommitReceipt(firstApply.receipt) != nil ||
					!reflect.DeepEqual(firstApply.receipt, committed.Receipt()) ||
					!firstReport.Applied || firstReport.Noop || firstReport.Outcome != "applied" ||
					firstReport.Receipt == nil ||
					!reflect.DeepEqual(firstReport.Receipt, projectMigrationReceipt(firstApply.receipt)) {
					t.Fatalf(
						"durable first invocation code/error/stdout/report/apply = %d/%v/%q/%#v/(%#v,%v)",
						firstCode, firstErr, firstStdout, firstReport, firstApply.receipt, firstApply.err,
					)
				}
			} else if !errors.Is(firstErr, injected) || firstCode != 0 || len(firstStdout) != 0 ||
				!reflect.DeepEqual(firstReport, migrationReport{}) ||
				store.ValidateCommitReceipt(firstApply.receipt) == nil {
				t.Fatalf(
					"precommit first invocation code/error/stdout/report/apply = %d/%v/%q/%#v/(%#v,%v)",
					firstCode, firstErr, firstStdout, firstReport, firstApply.receipt, firstApply.err,
				)
			}
			if reflect.DeepEqual(preState, postState) {
				t.Fatal("migration preview post-state equals its pre-state")
			}
			if !reflect.DeepEqual(interruptedState, preState) {
				t.Fatalf("fault exposed a partial revision-visible migration:\npre=%#v\ninterrupted=%#v\npost=%#v",
					preState, interruptedState, postState)
			}

			// Arrange retry. Resolution and preview intentionally happen before
			// openStore. A durable-journal retry therefore rebuilds the original
			// proof from the still-pre publication, then normal Open recovers the
			// journal and Store.Commit replays the exact original receipt.
			secondRecorder := &migrationRecoveryRecorder{delegate: mutation.NewMigrationPlanner()}
			secondDependencies := productionMigrateDependencies()
			secondDependencies.planner = secondRecorder
			productionOpenStore := secondDependencies.openStore
			openCalls := 0
			var recoveredAtOpen map[string]string
			secondDependencies.openStore = func(ctx context.Context, path string) (migrationStore, error) {
				openCalls++
				opened, err := productionOpenStore(ctx, path)
				if err != nil {
					return nil, err
				}
				recoveredAtOpen = snapshotRevisionVisibleMigrationFiles(t, root)
				return opened, nil
			}

			// Act: retry the same deterministic CLI request through a normal
			// Store open, then inspect the exact converged tree.
			secondCode, secondReport, secondStdout, secondErr := runMigrationRecoveryCommand(
				t,
				root,
				secondDependencies,
			)
			finalState := snapshotRevisionVisibleMigrationFiles(t, root)

			// Assert.
			if secondErr != nil || secondCode != 0 || len(secondStdout) == 0 || openCalls != 1 ||
				len(secondRecorder.previews) != 1 || len(secondRecorder.applies) != 1 {
				t.Fatalf(
					"retry error/code/stdout/store opens/preview/apply = %v/%d/%q/%d/%d/%d",
					secondErr,
					secondCode,
					secondStdout,
					openCalls,
					len(secondRecorder.previews),
					len(secondRecorder.applies),
				)
			}
			wantRecovered := preState
			if test.wantRecoveredState == "post" {
				wantRecovered = postState
			}
			if !reflect.DeepEqual(recoveredAtOpen, wantRecovered) {
				t.Fatalf("normal Store reopen recovered %s state incorrectly:\nwant=%#v\ngot=%#v",
					test.wantRecoveredState, wantRecovered, recoveredAtOpen)
			}
			if !reflect.DeepEqual(finalState, postState) {
				t.Fatalf("retry did not converge to the exact preview post-state:\nwant=%#v\ngot=%#v",
					postState, finalState)
			}

			secondPreview := secondRecorder.previews[0]
			secondApply := secondRecorder.applies[0]
			if secondPreview.err != nil || secondApply.err != nil ||
				secondPreview.result.PlanDigest != firstPreview.result.PlanDigest ||
				!reflect.DeepEqual(secondApply.request, firstApply.request) {
				t.Fatalf(
					"retry changed deterministic request: preview errors=%v/%v digests=%q/%q apply equal=%t",
					firstPreview.err,
					secondPreview.err,
					firstPreview.result.PlanDigest,
					secondPreview.result.PlanDigest,
					reflect.DeepEqual(secondApply.request, firstApply.request),
				)
			}
			if err := store.ValidateCommitReceipt(secondApply.receipt); err != nil {
				t.Fatalf("retry Commit receipt is invalid: %v", err)
			}
			wantKey, err := mutation.PlanDigestIdempotencyKey(
				"migration:v0.1-to-v0.2",
				firstPreview.result.PlanDigest,
				firstApply.request.Options.IdempotencyKey,
			)
			if err != nil {
				t.Fatal(err)
			}
			if secondApply.receipt.IdempotencyKey != wantKey ||
				secondApply.receipt.BaseRevision != firstPreview.result.Proof.BaseRevision ||
				secondApply.receipt.ResultRevision != firstPreview.result.Proof.ResultRevision ||
				!reflect.DeepEqual(secondApply.receipt.ChangedRefs, firstPreview.result.Proof.ChangedRefs) ||
				!reflect.DeepEqual(secondApply.receipt.ChangedFiles, firstPreview.result.Proof.ChangedFiles) {
				t.Fatalf("retry receipt does not match the original durable proof:\nreceipt=%#v\nproof=%#v",
					secondApply.receipt, firstPreview.result.Proof)
			}
			if !secondReport.Applied || secondReport.Noop || secondReport.Outcome != "applied" ||
				secondReport.Receipt == nil || len(secondReport.Blockers) != 0 ||
				len(secondReport.CleanupDiagnostics) != 0 ||
				secondReport.PlanDigest != firstPreview.result.PlanDigest ||
				!reflect.DeepEqual(secondReport.Receipt, projectMigrationReceipt(secondApply.receipt)) {
				t.Fatalf("retry CLI receipt projection = %#v, receipt=%#v", secondReport, secondApply.receipt)
			}
			if test.wantCommitted && (!reflect.DeepEqual(secondApply.receipt, firstApply.receipt) ||
				!reflect.DeepEqual(secondReport.Receipt, firstReport.Receipt)) {
				t.Fatalf("durable replay receipt/result drifted:\nfirst=%#v/%#v\nsecond=%#v/%#v",
					firstApply.receipt, firstReport.Receipt, secondApply.receipt, secondReport.Receipt)
			}
		})
	}
}
