package okfcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

func TestMigrateStoreOpenTypedNilErrorDoesNotPanicOrPublishOutput(t *testing.T) {
	// Arrange.
	root := targetNoopMigrationFixture(t)
	openErr := errors.New("store open failed")
	migrateDependencies := productionMigrateDependencies()
	migrateDependencies.openStore = func(context.Context, string) (migrationStore, error) {
		var backend *storefs.Store
		return backend, openErr
	}
	runDependencies := productionRunDependencies()
	runDependencies.migrate = func(args []string, stdout io.Writer) (int, error) {
		return cmdMigrateWithDependencies(args, stdout, migrateDependencies)
	}
	var stdout, stderr bytes.Buffer

	// Act.
	code := runWithDependencies(
		[]string{"migrate", root, "--to", "0.2", "--write", "--format", "json"},
		&stdout,
		&stderr,
		runDependencies,
	)

	// Assert.
	if code != 1 || stdout.Len() != 0 {
		t.Fatalf("code/stdout = %d/%q, want 1/empty", code, stdout.String())
	}
	assertCanonicalCLIError(t, stderr.String(), openErr.Error())
}

func TestBuildMigrationReportClassifiesCommitOutcomes(t *testing.T) {
	ordinary := errors.New("ordinary apply error identity")
	postCommitIO := errors.New("post-commit IO identity")
	tests := []struct {
		name         string
		fixture      func(*testing.T) string
		outcome      func(store.CommitReceipt) (store.CommitReceipt, error)
		wantCode     int
		wantApplied  bool
		wantNoop     bool
		wantOutcome  string
		wantReceipt  bool
		wantBlocker  string
		wantError    error
		wantExactErr bool
		wantNoOutput bool
	}{
		{
			name:    "changed receipt and nil",
			fixture: changedMigrationFixture,
			outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
				return receipt, nil
			},
			wantApplied: true, wantOutcome: "applied", wantReceipt: true,
		},
		{
			name:    "changed matching committed IO",
			fixture: changedMigrationFixture,
			outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
				return receipt, store.NewCommittedError(receipt, postCommitIO)
			},
			wantApplied: true, wantOutcome: "applied", wantReceipt: true,
		},
		{
			name:    "noop matching committed cancellation",
			fixture: targetNoopMigrationFixture,
			outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
				return receipt, store.NewCommittedError(receipt, context.Canceled)
			},
			wantApplied: true, wantNoop: true, wantOutcome: "applied", wantReceipt: true,
		},
		{
			name:    "ordinary error cannot borrow valid receipt",
			fixture: changedMigrationFixture,
			outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
				return receipt, ordinary
			},
			wantError: ordinary, wantExactErr: true, wantNoOutput: true,
		},
		{
			name:    "mismatched committed receipt is blocked",
			fixture: changedMigrationFixture,
			outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
				other := receipt.Clone()
				other.CommitTime = receipt.CommitTime.Add(time.Second).UTC()
				return receipt, store.NewCommittedError(other, postCommitIO)
			},
			wantCode: 1, wantOutcome: "blocked", wantBlocker: "migration_apply_conflict",
		},
		{
			name:    "invalid receipt is rejected",
			fixture: changedMigrationFixture,
			outcome: func(store.CommitReceipt) (store.CommitReceipt, error) {
				return store.CommitReceipt{}, nil
			},
			wantError: store.ErrStorageCorrupt, wantNoOutput: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := test.fixture(t)
			planner := &lifecyclePlanner{
				delegate: mutation.NewMigrationPlanner(),
				apply: func(request mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
					return test.outcome(validLifecycleReceipt(request))
				},
			}
			dependencies, source, backend := lifecycleDependencies(t, root, planner, nil, nil)
			var stdout bytes.Buffer

			// Act.
			code, err := cmdMigrateWithDependencies(
				[]string{root, "--to", "0.2", "--from", "auto", "--actor", "process:migration", "--write", "--format", "json"},
				&stdout,
				dependencies,
			)

			// Assert.
			if code != test.wantCode || source.closeCalls != 1 || backend.closeCalls != 1 {
				t.Fatalf("code/source closes/store closes = %d/%d/%d, want %d/1/1",
					code, source.closeCalls, backend.closeCalls, test.wantCode)
			}
			if test.wantError != nil {
				if !errors.Is(err, test.wantError) || test.wantExactErr && err != test.wantError ||
					test.wantNoOutput && stdout.Len() != 0 {
					t.Fatalf("error/stdout = %v/%q, want error %v and no output", err, stdout.String(), test.wantError)
				}
				return
			}
			if err != nil || stdout.Len() == 0 {
				t.Fatalf("error/stdout = %v/%q, want rendered report", err, stdout.String())
			}
			var report migrationReport
			if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
				t.Fatalf("decode migration report: %v; stdout=%q", decodeErr, stdout.String())
			}
			if report.Applied != test.wantApplied || report.Noop != test.wantNoop ||
				report.Outcome != test.wantOutcome || (report.Receipt != nil) != test.wantReceipt {
				t.Fatalf("report = %#v, want applied=%t noop=%t outcome=%q receipt=%t",
					report, test.wantApplied, test.wantNoop, test.wantOutcome, test.wantReceipt)
			}
			if test.wantBlocker != "" &&
				(len(report.Blockers) != 1 || report.Blockers[0].Code != test.wantBlocker) {
				t.Fatalf("blockers = %#v, want %q", report.Blockers, test.wantBlocker)
			}
		})
	}
}

func TestClassifyMigrationCommitOutcomeValidatesCommittedErrorReceipt(t *testing.T) {
	// Arrange.
	receipt := directClassifierReceipt(t)
	invalidCommitted := &store.CommittedError{}

	// Act.
	got, gotErr := classifyMigrationCommitOutcome(receipt, invalidCommitted)

	// Assert.
	if !reflect.DeepEqual(got, store.CommitReceipt{}) ||
		!errors.Is(gotErr, store.ErrStorageCorrupt) ||
		!errors.Is(gotErr, invalidCommitted) {
		t.Fatalf("outcome = %#v/%v, want zero receipt with storage corruption and original committed error", got, gotErr)
	}
}

func TestClassifyMigrationCommitOutcomePreservesExactErrors(t *testing.T) {
	ordinary := errors.New("ordinary exact identity")
	postCommit := errors.New("post-commit exact identity")
	tests := []struct {
		name      string
		outcome   func(store.CommitReceipt) error
		wantExact error
		wantIs    error
		wantPlan  bool
	}{
		{
			name:      "ordinary error exact identity",
			outcome:   func(store.CommitReceipt) error { return ordinary },
			wantExact: ordinary,
		},
		{
			name: "valid receipt mismatch preserves committed error",
			outcome: func(receipt store.CommitReceipt) error {
				other := receipt.Clone()
				other.CommitTime = other.CommitTime.Add(time.Second).UTC()
				return store.NewCommittedError(other, postCommit)
			},
			wantIs: postCommit, wantPlan: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			receipt := directClassifierReceipt(t)
			applyErr := test.outcome(receipt)

			// Act.
			got, gotErr := classifyMigrationCommitOutcome(receipt, applyErr)

			// Assert.
			if !reflect.DeepEqual(got, store.CommitReceipt{}) {
				t.Fatalf("receipt = %#v, want zero", got)
			}
			if test.wantExact != nil && gotErr != test.wantExact {
				t.Fatalf("error identity = %p/%v, want %p/%v", gotErr, gotErr, test.wantExact, test.wantExact)
			}
			if test.wantIs != nil && !errors.Is(gotErr, test.wantIs) {
				t.Fatalf("error = %v, want original cause %v", gotErr, test.wantIs)
			}
			if test.wantPlan && !errors.Is(gotErr, applyErr) {
				t.Fatalf("error = %v, want original committed error %v", gotErr, applyErr)
			}
			if errors.Is(gotErr, mutation.ErrMigrationPlanMismatch) != test.wantPlan {
				t.Fatalf("plan mismatch = %t, want %t; error=%v",
					errors.Is(gotErr, mutation.ErrMigrationPlanMismatch), test.wantPlan, gotErr)
			}
		})
	}
}

func TestClassifyMigrationCommitOutcomeClonesAcceptedReceipts(t *testing.T) {
	tests := []struct {
		name     string
		applyErr func(store.CommitReceipt) error
	}{
		{name: "nil error", applyErr: func(store.CommitReceipt) error { return nil }},
		{name: "committed error", applyErr: func(receipt store.CommitReceipt) error {
			return store.NewCommittedError(receipt, errors.New("post-commit identity"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := directClassifierReceipt(t)
			sourceSnapshot := source.Clone()
			applyErr := test.applyErr(source)
			var committed *store.CommittedError
			var committedSnapshot store.CommitReceipt
			if errors.As(applyErr, &committed) {
				committedSnapshot = committed.Receipt()
			}

			// Act.
			got, gotErr := classifyMigrationCommitOutcome(source, applyErr)
			got.ChangedFiles[0].Path = "mutated.md"
			got.ChangedRefs[0].Fragment = "mutated"
			if committed != nil {
				getter := committed.Receipt()
				getter.ChangedFiles[0].Path = "getter-mutated.md"
				getter.ChangedRefs[0].Fragment = "getter-mutated"
			}

			// Assert.
			if gotErr != nil || !reflect.DeepEqual(source, sourceSnapshot) {
				t.Fatalf("error/source = %v/%#v, want nil/unchanged %#v", gotErr, source, sourceSnapshot)
			}
			if committed != nil && !reflect.DeepEqual(committed.Receipt(), committedSnapshot) {
				t.Fatalf("committed receipt changed = %#v, want %#v", committed.Receipt(), committedSnapshot)
			}
		})
	}
}

func TestRunMigrateMatchingCommittedErrorIsSuccessfulAndSilent(t *testing.T) {
	for _, cause := range []error{errors.New("post-commit IO identity"), context.Canceled} {
		cause := cause
		t.Run(cause.Error(), func(t *testing.T) {
			// Arrange.
			root := changedMigrationFixture(t)
			var expectedReceipt store.CommitReceipt
			planner := &lifecyclePlanner{
				delegate: mutation.NewMigrationPlanner(),
				apply: func(request mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
					receipt := validLifecycleReceipt(request)
					expectedReceipt = receipt.Clone()
					return receipt, store.NewCommittedError(receipt, cause)
				},
			}
			migrateDependencies, _, _ := lifecycleDependencies(t, root, planner, nil, nil)
			runDependencies := productionRunDependencies()
			runDependencies.migrate = func(args []string, stdout io.Writer) (int, error) {
				return cmdMigrateWithDependencies(args, stdout, migrateDependencies)
			}
			var stdout, stderr bytes.Buffer

			// Act.
			code := runWithDependencies(
				[]string{"migrate", root, "--to", "0.2", "--from", "auto", "--actor", "process:migration", "--write", "--format", "json"},
				&stdout,
				&stderr,
				runDependencies,
			)

			// Assert.
			var report migrationReport
			decodeErr := json.Unmarshal(stdout.Bytes(), &report)
			if code != 0 || stderr.Len() != 0 || decodeErr != nil ||
				report.Outcome != "applied" || !report.Applied || report.Noop ||
				len(report.Blockers) != 0 || len(report.CleanupDiagnostics) != 0 ||
				!reflect.DeepEqual(report.Receipt, projectMigrationReceipt(expectedReceipt)) {
				t.Fatalf("Run() code/stdout/stderr = %d/%q/%q, want successful applied receipt",
					code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestMigrateCommittedErrorStillProjectsDeferredCloseDiagnostics(t *testing.T) {
	// Arrange.
	root := changedMigrationFixture(t)
	planner := &lifecyclePlanner{
		delegate: mutation.NewMigrationPlanner(),
		apply: func(request mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
			receipt := validLifecycleReceipt(request)
			return receipt, store.NewCommittedError(receipt, errors.New("post-commit IO identity"))
		},
	}
	dependencies, _, _ := lifecycleDependencies(
		t,
		root,
		planner,
		errors.New("source close identity"),
		errors.New("store close identity"),
	)
	var stdout bytes.Buffer

	// Act.
	code, err := cmdMigrateWithDependencies(
		[]string{root, "--to", "0.2", "--from", "auto", "--actor", "process:migration", "--write", "--format", "json"},
		&stdout,
		dependencies,
	)

	// Assert.
	var report migrationReport
	decodeErr := json.Unmarshal(stdout.Bytes(), &report)
	if err != nil || decodeErr != nil || code != 0 || report.Outcome != "applied_with_cleanup_diagnostics" ||
		report.Receipt == nil || len(report.CleanupDiagnostics) != 2 {
		t.Fatalf("result = code:%d error:%v decode:%v report:%#v", code, err, decodeErr, report)
	}
}

func TestMigrateLifecyclePostReceiptCleanupIsReportedAndNonRetryable(t *testing.T) {
	// Arrange.
	root := targetNoopMigrationFixture(t)
	storeCloseErr := errors.New("store close /private/secret\nretry me")
	sourceCloseErr := errors.New("source close /private/secret\x00retry me")
	planner := &lifecyclePlanner{
		delegate: mutation.NewMigrationPlanner(),
		apply: func(request mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
			return validLifecycleReceipt(request), nil
		},
	}
	dependencies, source, backend := lifecycleDependencies(
		t,
		root,
		planner,
		sourceCloseErr,
		storeCloseErr,
	)
	var stdout bytes.Buffer

	// Act.
	code, err := cmdMigrateWithDependencies(
		[]string{root, "--to", "0.2", "--from", "auto", "--write", "--format", "json"},
		&stdout,
		dependencies,
	)

	// Assert.
	if err != nil || code != 0 || source.closeCalls != 1 || backend.closeCalls != 1 {
		t.Fatalf("error/code/source closes/store closes = %v/%d/%d/%d, want nil/0/1/1",
			err, code, source.closeCalls, backend.closeCalls)
	}
	var report migrationReport
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode migration report: %v; stdout=%q", decodeErr, stdout.String())
	}
	if !report.Applied || report.Receipt == nil ||
		report.Outcome != "applied_with_cleanup_diagnostics" ||
		len(report.CleanupDiagnostics) != 2 {
		t.Fatalf("post-receipt report = %#v", report)
	}
	wantCodes := []string{"migration_store_close_failed", "migration_source_close_failed"}
	wantPhases := []string{"store_close", "source_close"}
	for index, diagnostic := range report.CleanupDiagnostics {
		if diagnostic.Code != wantCodes[index] || diagnostic.Phase != wantPhases[index] ||
			diagnostic.Retryable || diagnostic.Message == "" ||
			strings.Contains(diagnostic.Message, "/private/secret") ||
			strings.ContainsAny(diagnostic.Message, "\n\r\x00") {
			t.Fatalf("cleanup diagnostic[%d] = %#v", index, diagnostic)
		}
	}
	if strings.Contains(stdout.String(), "/private/secret") ||
		strings.Contains(stdout.String(), "retry me") {
		t.Fatalf("hostile Close error leaked to JSON output: %q", stdout.String())
	}
}

func TestMigrateLifecyclePreReceiptCloseFailureSuppressesOutputAndPreservesErrors(t *testing.T) {
	applyErr := errors.New("apply failed identity")
	previewErr := errors.New("preview failed identity")
	blockedErr := errors.New("blocked resolution identity")
	conflictErr := &store.Conflict{Retryable: true}

	tests := []struct {
		name            string
		write           bool
		previewErr      error
		resolve         migrationSourceResolver
		apply           func(mutation.MigrationApplyRequest) (store.CommitReceipt, error)
		sourceCloseErr  error
		storeCloseErr   error
		wantStoreCloses int
		wantErrors      []error
	}{
		{
			name:           "dry run source close",
			sourceCloseErr: errors.New("dry source close identity"),
		},
		{
			name: "blocker source close",
			resolve: func(
				context.Context,
				bundle.Source,
				mutation.MigrationSourceOptions,
			) (mutation.MigrationSourceResolution, error) {
				return mutation.MigrationSourceResolution{
					RequestedSelector: "auto",
					Transition:        mutation.MigrationTransitionBlocked,
					Blockers: []mutation.MigrationBlocker{{
						Code: "unsupported_migration_source", Message: "blocked",
					}},
				}, blockedErr
			},
			sourceCloseErr: errors.New("blocked source close identity"),
			wantErrors:     []error{blockedErr},
		},
		{
			name:           "preview blocker source close",
			previewErr:     previewErr,
			sourceCloseErr: errors.New("preview source close identity"),
			wantErrors:     []error{previewErr},
		},
		{
			name:  "apply conflict both close failures",
			write: true,
			apply: func(mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
				return store.CommitReceipt{}, conflictErr
			},
			sourceCloseErr:  errors.New("conflict source close identity"),
			storeCloseErr:   errors.New("conflict store close identity"),
			wantStoreCloses: 1,
			wantErrors:      []error{conflictErr},
		},
		{
			name:  "apply error both close failures",
			write: true,
			apply: func(mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
				return store.CommitReceipt{}, applyErr
			},
			sourceCloseErr:  errors.New("apply source close identity"),
			storeCloseErr:   errors.New("apply store close identity"),
			wantStoreCloses: 1,
			wantErrors:      []error{applyErr},
		},
		{
			name:  "zero receipt is not committed",
			write: true,
			apply: func(mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
				return store.CommitReceipt{}, nil
			},
			sourceCloseErr:  errors.New("invalid receipt source close identity"),
			storeCloseErr:   errors.New("invalid receipt store close identity"),
			wantStoreCloses: 1,
			wantErrors:      []error{store.ErrStorageCorrupt},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := targetNoopMigrationFixture(t)
			planner := &lifecyclePlanner{
				delegate:   mutation.NewMigrationPlanner(),
				previewErr: test.previewErr,
				apply:      test.apply,
			}
			if planner.apply == nil {
				planner.apply = func(mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
					return store.CommitReceipt{}, errors.New("unexpected apply")
				}
			}
			dependencies, source, backend := lifecycleDependencies(
				t,
				root,
				planner,
				test.sourceCloseErr,
				test.storeCloseErr,
			)
			if test.resolve != nil {
				dependencies.resolve = test.resolve
			}
			args := []string{root, "--to", "0.2", "--from", "auto", "--format", "json"}
			if test.write {
				args = append(args, "--write")
			}
			var stdout bytes.Buffer

			// Act.
			code, err := cmdMigrateWithDependencies(args, &stdout, dependencies)

			// Assert.
			if err == nil || code != 0 || stdout.Len() != 0 || source.closeCalls != 1 ||
				backend.closeCalls != test.wantStoreCloses {
				t.Fatalf("error/code/stdout/source closes/store closes = %v/%d/%q/%d/%d",
					err, code, stdout.String(), source.closeCalls, backend.closeCalls)
			}
			if !errors.Is(err, test.sourceCloseErr) {
				t.Fatalf("error = %v, missing source Close identity", err)
			}
			if test.storeCloseErr != nil && !errors.Is(err, test.storeCloseErr) {
				t.Fatalf("error = %v, missing store Close identity", err)
			}
			for _, wantErr := range test.wantErrors {
				if !errors.Is(err, wantErr) {
					t.Fatalf("error = %v, missing original identity %v", err, wantErr)
				}
			}
		})
	}
}

func TestMigrateLifecyclePlainAppliedReportHasEmptyCleanupArrayAndTextSection(t *testing.T) {
	// Arrange.
	root := targetNoopMigrationFixture(t)
	planner := &lifecyclePlanner{
		delegate: mutation.NewMigrationPlanner(),
		apply: func(request mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
			return validLifecycleReceipt(request), nil
		},
	}
	dependencies, source, backend := lifecycleDependencies(t, root, planner, nil, nil)
	var stdout bytes.Buffer

	// Act.
	code, err := cmdMigrateWithDependencies(
		[]string{root, "--to", "0.2", "--from", "auto", "--write", "--format", "json"},
		&stdout,
		dependencies,
	)

	// Assert.
	if err != nil || code != 0 || source.closeCalls != 1 || backend.closeCalls != 1 {
		t.Fatalf("error/code/source closes/store closes = %v/%d/%d/%d",
			err, code, source.closeCalls, backend.closeCalls)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"cleanup_diagnostics":[]`)) {
		t.Fatalf("JSON cleanup_diagnostics is not a non-null empty array: %q", stdout.String())
	}
	var report migrationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "applied" || len(report.CleanupDiagnostics) != 0 || report.Receipt == nil {
		t.Fatalf("plain applied report = %#v", report)
	}
	var textOutput bytes.Buffer
	if _, err := renderMigrationReport(&textOutput, "text", report, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOutput.String(), "Cleanup diagnostics (0):\n") ||
		!strings.Contains(textOutput.String(), "Receipt:\n") {
		t.Fatalf("text renderer omitted cleanup/receipt section:\n%s", textOutput.String())
	}
}

func TestMigrateLifecycleAppliedModesCrossStoreAndSourceCleanupFailures(t *testing.T) {
	modes := []struct {
		name     string
		fixture  func(*testing.T) string
		from     string
		wantNoop bool
	}{
		{
			name: "changed migration",
			fixture: func(t *testing.T) string {
				root := t.TempDir()
				writeFixtureFile(t, filepath.Join(root, "concept.md"),
					"---\ntype: Note\ntimestamp: 2026-06-01T10:00:00Z\nx-keep: true\n---\n\nBody.\n")
				return root
			},
			from: "auto",
		},
		{
			name: "target noop",
			fixture: func(t *testing.T) string {
				return targetNoopMigrationFixture(t)
			},
			from:     "auto",
			wantNoop: true,
		},
		{
			name: "planned noop",
			fixture: func(t *testing.T) string {
				root := t.TempDir()
				writeFixtureFile(t, filepath.Join(root, "note.md"),
					"---\ntype: Knowledge\nx-keep: true\n---\nBody.\n")
				return root
			},
			from:     "0.1",
			wantNoop: true,
		},
	}
	closeModes := []struct {
		name            string
		storeErr        error
		sourceErr       error
		wantDiagnostics []string
	}{
		{
			name:            "store close",
			storeErr:        errors.New("hostile store /secret"),
			wantDiagnostics: []string{"migration_store_close_failed"},
		},
		{
			name:            "source close",
			sourceErr:       errors.New("hostile source /secret"),
			wantDiagnostics: []string{"migration_source_close_failed"},
		},
		{
			name:            "store then source close",
			storeErr:        errors.New("hostile store /secret"),
			sourceErr:       errors.New("hostile source /secret"),
			wantDiagnostics: []string{"migration_store_close_failed", "migration_source_close_failed"},
		},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			for _, closeMode := range closeModes {
				t.Run(closeMode.name, func(t *testing.T) {
					// Arrange.
					root := mode.fixture(t)
					planner := &lifecyclePlanner{
						delegate: mutation.NewMigrationPlanner(),
						apply: func(request mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
							return validLifecycleReceipt(request), nil
						},
					}
					dependencies, source, backend := lifecycleDependencies(
						t, root, planner, closeMode.sourceErr, closeMode.storeErr,
					)
					var stdout bytes.Buffer

					// Act.
					code, err := cmdMigrateWithDependencies(
						[]string{root, "--to", "0.2", "--from", mode.from, "--actor", "human:test", "--write", "--format", "json"},
						&stdout,
						dependencies,
					)

					// Assert.
					if err != nil || code != 0 || source.closeCalls != 1 || backend.closeCalls != 1 {
						t.Fatalf("error/code/source closes/store closes = %v/%d/%d/%d",
							err, code, source.closeCalls, backend.closeCalls)
					}
					var report migrationReport
					if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
						t.Fatal(err)
					}
					if !report.Applied || report.Receipt == nil || report.Noop != mode.wantNoop ||
						report.Outcome != "applied_with_cleanup_diagnostics" ||
						len(report.CleanupDiagnostics) != len(closeMode.wantDiagnostics) {
						t.Fatalf("applied lifecycle report = %#v", report)
					}
					for index, wantCode := range closeMode.wantDiagnostics {
						if report.CleanupDiagnostics[index].Code != wantCode {
							t.Fatalf("diagnostic[%d] = %#v, want code %q",
								index, report.CleanupDiagnostics[index], wantCode)
						}
					}
					if strings.Contains(stdout.String(), "/secret") {
						t.Fatalf("hostile cleanup detail leaked: %q", stdout.String())
					}
				})
			}
		})
	}
}

func TestRunMigratePostReceiptCleanupReturnsSuccessWithoutStderr(t *testing.T) {
	// Arrange.
	root := targetNoopMigrationFixture(t)
	planner := &lifecyclePlanner{
		delegate: mutation.NewMigrationPlanner(),
		apply: func(request mutation.MigrationApplyRequest) (store.CommitReceipt, error) {
			return validLifecycleReceipt(request), nil
		},
	}
	migrateDependencies, _, _ := lifecycleDependencies(
		t,
		root,
		planner,
		errors.New("hostile source close /secret"),
		errors.New("hostile store close /secret"),
	)
	runDependencies := productionRunDependencies()
	runDependencies.migrate = func(args []string, stdout io.Writer) (int, error) {
		return cmdMigrateWithDependencies(args, stdout, migrateDependencies)
	}
	var stdout, stderr bytes.Buffer

	// Act.
	code := runWithDependencies(
		[]string{"migrate", root, "--to", "0.2", "--from", "auto", "--write", "--format", "json"},
		&stdout,
		&stderr,
		runDependencies,
	)

	// Assert.
	if code != 0 || stderr.Len() != 0 ||
		!strings.Contains(stdout.String(), `"outcome":"applied_with_cleanup_diagnostics"`) ||
		!strings.Contains(stdout.String(), `"receipt":{`) ||
		strings.Contains(stdout.String(), "/secret") {
		t.Fatalf("outer Run code/stdout/stderr = %d/%q/%q", code, stdout.String(), stderr.String())
	}
}

func TestCitationMappingsReaderClosesExactlyOnceAndJoinsFailures(t *testing.T) {
	readErr := errors.New("citation read identity")
	closeErr := errors.New("citation close identity")
	tests := []struct {
		name       string
		data       []byte
		readErr    error
		wantErrors []error
		wantText   string
	}{
		{name: "read and close", readErr: readErr, wantErrors: []error{readErr, closeErr}},
		{name: "parse and close", data: []byte(`{"not":"an array"}`), wantErrors: []error{closeErr}, wantText: "decode citation mappings"},
		{name: "limit and close", data: bytes.Repeat([]byte("x"), maxCitationMappingsFileBytes+1), wantErrors: []error{closeErr}, wantText: "file size limit exceeded"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			reader := &citationReadCloser{
				reader:   bytes.NewReader(test.data),
				readErr:  test.readErr,
				closeErr: closeErr,
			}

			// Act.
			mappings, err := loadCitationMappingsFromReadCloser("mappings.json", reader)

			// Assert.
			if mappings != nil || err == nil || reader.closeCalls != 1 {
				t.Fatalf("mappings/error/close calls = %#v/%v/%d", mappings, err, reader.closeCalls)
			}
			for _, wantErr := range test.wantErrors {
				if !errors.Is(err, wantErr) {
					t.Fatalf("error = %v, missing identity %v", err, wantErr)
				}
			}
			if test.wantText != "" && !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("error = %v, want text %q", err, test.wantText)
			}
		})
	}
}

func TestCitationMappingsHandleClosesBeforeMigrationSourceOpens(t *testing.T) {
	// Arrange.
	root := targetNoopMigrationFixture(t)
	reader := &citationReadCloser{reader: bytes.NewReader([]byte("[]"))}
	dependencies := productionMigrateDependencies()
	dependencies.openCitationMappings = func(string) (io.ReadCloser, error) { return reader, nil }
	physicalSource := &bundle.FileSystemSource{Root: root}
	dependencies.openSource = func(context.Context, string) (migrationSource, error) {
		if reader.closeCalls != 1 {
			return nil, errors.New("citation handle survived into migration")
		}
		return physicalSource, nil
	}
	var stdout bytes.Buffer

	// Act.
	code, err := cmdMigrateWithDependencies(
		[]string{root, "--to", "0.2", "--from", "auto", "--citation-mappings", "mappings.json", "--format", "json"},
		&stdout,
		dependencies,
	)

	// Assert.
	if err != nil || code != 0 || stdout.Len() == 0 || reader.closeCalls != 1 {
		t.Fatalf("error/code/stdout/close calls = %v/%d/%q/%d", err, code, stdout.String(), reader.closeCalls)
	}
}
