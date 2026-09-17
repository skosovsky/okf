//go:build (darwin && !ios) || (linux && !android)

package bundle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDocumentV2ActiveRollbackPreservesContextValuesAfterCancellation(t *testing.T) {
	// Arrange.
	type contextKey struct{}
	const wantValue = "active-rollback-value"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, wantValue))
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	trigger := errors.New("cancel after forward install")
	forward := true
	cancelled := false
	validatorObserved := false
	hooks := documentPublishHooks{
		validateContext: func(validationCtx context.Context) error {
			if validationCtx.Value(contextKey{}) != wantValue {
				return errors.New("scope validator lost request context value")
			}
			if cancelled {
				if validationCtx.Err() != nil {
					return errors.New("post-mutation scope validator retained cancellation")
				}
				validatorObserved = true
			}
			return nil
		},
		afterInstall: func(*os.Root, string) error {
			if forward {
				forward = false
				cancelled = true
				cancel()
				return trigger
			}
			return nil
		},
	}
	session, err := openDocumentSessionContext(ctx, target, "", documentSessionHooks{publish: hooks})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))
	serialized, err := replacement.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	observed := false
	rollback := func(
		rollbackCtx context.Context,
		session *DocumentSession,
		transaction *documentTransaction,
		published []byte,
		mutated, installed bool,
		hooks documentPublishHooks,
	) error {
		observed = true
		if rollbackCtx.Err() != nil || rollbackCtx.Value(contextKey{}) != wantValue {
			return errors.New("rollback context lost cancellation suppression or values")
		}
		return convergeDocumentRollback(
			rollbackCtx, session, transaction, published, mutated, installed, hooks,
		)
	}

	// Act.
	err = rewriteCapturedDocumentWithRollback(ctx, session, []byte(serialized), hooks, rollback)

	// Assert.
	if !observed || !validatorObserved || !errors.Is(err, trigger) {
		t.Fatalf("rollback observed=%t validator=%t error=%v", observed, validatorObserved, err)
	}
	if got := documentSessionReadFile(t, target); got != documentSessionConcept("concept", "before") {
		t.Fatalf("rollback target = %q", got)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestDocumentV2ActiveCancellationAfterDurableVacateConvergesWithError(t *testing.T) {
	// Arrange.
	ctx, cancel := context.WithCancel(context.Background())
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	vacated := false
	hooks := documentPublishHooks{
		afterVacate: func(parent *os.Root, leaf, claim string) error {
			if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
				return errors.New("durably vacated target is not missing")
			}
			if _, err := parent.Lstat(claim); err != nil {
				return err
			}
			vacated = true
			cancel()
			return nil
		},
	}
	session, err := openDocumentSessionContext(
		ctx, target, "", documentSessionHooks{publish: hooks},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))

	// Act.
	err = session.RewriteContext(ctx, replacement)

	// Assert.
	if !vacated || err == nil || !errors.Is(err, context.Canceled) ||
		!errors.Is(err, ErrDocumentConflict) || errors.Is(err, ErrPublicationCommitted) {
		t.Fatalf("vacated=%t error=%v", vacated, err)
	}
	info, statErr := os.Lstat(target)
	if statErr != nil || info.Mode().Perm() != 0o640 || documentSessionReadFile(t, target) != original {
		t.Fatalf("converged target info=%v error=%v", info, statErr)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestDocumentV2ActiveRollbackMatchesIndependentOracle(t *testing.T) {
	original := documentSessionConcept("concept", "before")
	published := documentSessionConcept("concept", "after")
	tests := []struct {
		cut       string
		errors    []string
		bytes     string
		missing   bool
		role      string
		artifacts []string
		residue   bool
	}{
		{"normal", []string{"document-conflict", "trigger"}, original, false, "restored-independent", nil, false},
		{"anchor", []string{"document-conflict", "trigger", "cut"}, published, false, "stage", []string{"anchor-0", "backup", "claim", "manifest", "stage", "witness"}, true},
		{"restore", []string{"document-conflict", "trigger", "cut"}, published, false, "stage", []string{"anchor-0", "backup", "claim", "manifest", "restore-install", "stage", "witness"}, true},
		{"discard", []string{"document-conflict", "trigger", "document-conflict", "cut"}, original, false, "restored-independent", nil, false},
		{"consume", []string{"document-conflict", "trigger", "document-conflict", "cut"}, original, false, "restored-independent", nil, false},
		{"cleanup", []string{"document-conflict", "trigger", "cut"}, original, false, "anchor-0", []string{"anchor-0", "backup", "claim", "discard", "manifest", "stage", "witness"}, true},
		{"cleanup-3", []string{"document-conflict", "trigger", "cut"}, original, false, "anchor-0", []string{"anchor-0", "backup", "claim", "stage", "witness"}, true},
		{"rollback-file-sync", []string{"document-conflict", "trigger", "cut"}, published, false, "stage", []string{"backup", "claim", "manifest", "stage", "witness"}, true},
		{"rollback-directory-sync", []string{"document-conflict", "trigger", "cut"}, published, false, "stage", []string{"anchor-0", "backup", "claim", "manifest", "restore-install", "stage", "witness"}, true},
		{"vacate-rename", []string{"document-conflict", "trigger", "cut"}, published, false, "stage", []string{"anchor-0", "backup", "claim", "manifest", "restore-install", "stage", "witness"}, true},
		{"source-conflict", []string{"document-conflict", "trigger", "document-conflict"}, published, false, "stage", []string{"backup", "claim", "manifest", "stage", "witness"}, true},
		{"scope-conflict", []string{"document-conflict", "trigger"}, original, false, "restored-independent", nil, true},
		{"parent-conflict", []string{"document-conflict", "trigger", "document-conflict", "document-conflict"}, published, false, "stage", []string{"backup", "claim", "manifest", "stage", "witness"}, true},
		{"double", []string{"document-conflict", "trigger", "document-conflict", "cut", "second"}, "", true, "missing", []string{"anchor-0", "backup", "claim", "discard", "manifest", "restore-install", "stage", "witness"}, true},
	}
	for _, test := range tests {
		t.Run(test.cut, func(t *testing.T) {
			// Arrange / Act.
			canonical := runDocumentV2ActiveRollbackForTest(t, test.cut, convergeDocumentRollback)

			// Assert.
			wantMode := fs.FileMode(0o640)
			if test.missing {
				wantMode = 0
			}
			if !reflect.DeepEqual(canonical.errorIdentities, test.errors) ||
				canonical.committed || canonical.targetMissing != test.missing ||
				canonical.targetBytes != test.bytes || canonical.targetMode != wantMode ||
				canonical.targetIdentityRole != test.role ||
				!reflect.DeepEqual(canonical.artifactKinds, test.artifacts) ||
				(canonical.residue != "") != test.residue || !canonical.targetStable ||
				len(canonical.errorOrder) == 0 {
				t.Fatalf("canonical violates independent contract: %+v", canonical)
			}
		})
	}
}

func runDocumentV2ActiveRollbackForTest(
	t *testing.T,
	cut string,
	rollback func(context.Context, *DocumentSession, *documentTransaction, []byte, bool, bool, documentPublishHooks) error,
) documentFaultParity {
	t.Helper()
	temp := t.TempDir()
	root := filepath.Join(temp, "bundle")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "concept.md")
	inspectRoot, inspectTarget := root, target
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	trigger := errors.New("active parity forward trigger")
	cutErr := errors.New("active parity " + cut + " cut")
	secondErr := errors.New("active parity double convergence cut")
	forward := true
	installCalls := 0
	rollbackArmed := false
	vacateCalls := 0
	cleanupCalls := 0
	hooks := documentPublishHooks{
		beforeInstall: func(*os.Root, string, string) error {
			installCalls++
			if cut == "double" && installCalls == 2 {
				return secondErr
			}
			return nil
		},
		afterInstall: func(parent *os.Root, _ string) error {
			if forward {
				forward = false
				rollbackArmed = true
				switch cut {
				case "source-conflict":
					name, nameErr := documentV2ForwardArtifactName(parent, "backup")
					if nameErr != nil {
						t.Fatal(nameErr)
					}
					file, openErr := parent.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 0)
					if openErr != nil {
						t.Fatal(openErr)
					}
					_, writeErr := file.Write([]byte("changed backup\n"))
					if err := errors.Join(writeErr, file.Close()); err != nil {
						t.Fatal(err)
					}
				case "scope-conflict":
					name := documentTransactionPrefix + "foreign"
					file, openErr := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
					if openErr != nil {
						t.Fatal(openErr)
					}
					if closeErr := file.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
				case "parent-conflict":
					detached := filepath.Join(temp, "detached")
					if renameErr := os.Rename(root, detached); renameErr != nil {
						t.Fatal(renameErr)
					}
					if mkdirErr := os.Mkdir(root, 0o755); mkdirErr != nil {
						t.Fatal(mkdirErr)
					}
					inspectRoot = detached
					inspectTarget = filepath.Join(detached, "concept.md")
				}
				return trigger
			}
			return nil
		},
	}
	if cut == "rollback-file-sync" {
		hooks.fileSync = func(*os.File) error {
			if rollbackArmed {
				rollbackArmed = false
				return cutErr
			}
			return nil
		}
	}
	if cut == "rollback-directory-sync" {
		hooks.directorySync = func(*os.Root) error {
			if rollbackArmed {
				rollbackArmed = false
				return cutErr
			}
			return nil
		}
	}
	if cut == "vacate-rename" {
		hooks.afterVacateRename = func(*os.Root, string, string) error {
			vacateCalls++
			if vacateCalls == 2 {
				return cutErr
			}
			return nil
		}
	}
	if cut == "anchor" {
		hooks.afterAnchorSync = func(*os.Root, string) error { return cutErr }
	}
	if cut == "restore" {
		hooks.afterRestoreSync = func(*os.Root, string) error { return cutErr }
	}
	if cut == "discard" {
		hooks.afterDiscardSync = func(*os.Root, string) error { return cutErr }
	}
	if cut == "double" {
		hooks.afterDiscardSync = func(*os.Root, string) error { return cutErr }
	}
	if cut == "consume" {
		hooks.afterRestoreConsumed = func(*os.Root, string) error { return cutErr }
	}
	if cut == "cleanup" {
		hooks.beforeCleanup = func(*os.Root, string, string) error { return cutErr }
	}
	if cut == "cleanup-3" {
		hooks.beforeCleanup = func(*os.Root, string, string) error {
			cleanupCalls++
			if cleanupCalls == 3 {
				return cutErr
			}
			return nil
		}
	}
	session, err := openDocumentSessionContext(
		context.Background(), target, "", documentSessionHooks{publish: hooks},
	)
	if err != nil {
		t.Fatal(err)
	}
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))
	serialized, err := replacement.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	err = rewriteCapturedDocumentWithRollback(
		context.Background(), session, []byte(serialized), hooks, rollback,
	)
	result := documentFaultParity{
		errorOrder: documentFaultErrorOrder(err, trigger),
		errorIdentities: documentFaultIdentityOrder(err,
			documentFaultIdentity{"trigger", trigger},
			documentFaultIdentity{"cut", cutErr},
			documentFaultIdentity{"second", secondErr},
			documentFaultIdentity{"document-conflict", ErrDocumentConflict},
			documentFaultIdentity{"publication-committed", ErrPublicationCommitted},
		),
		committed: errors.Is(err, ErrPublicationCommitted),
	}
	if !errors.Is(err, trigger) {
		t.Fatalf("active error = %v, want trigger", err)
	}
	if cut == "double" && !errors.Is(err, secondErr) {
		t.Fatalf("active error = %v, want second convergence error", err)
	}
	conflictCut := cut == "source-conflict" || cut == "scope-conflict" || cut == "parent-conflict"
	if cut != "normal" && cut != "double" && !conflictCut && !errors.Is(err, cutErr) {
		t.Fatalf("active error = %v, want cut error", err)
	}
	if conflictCut && !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("active error = %v, want document conflict", err)
	}
	targetBeforeClose := observeDocumentFaultParity(
		t, &result, inspectRoot, inspectTarget, originalInfo, original,
		documentSessionConcept("concept", "after"),
	)
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	targetAfterClose, statErr := os.Lstat(inspectTarget)
	if result.targetMissing && errors.Is(statErr, fs.ErrNotExist) {
		result.targetStable = true
	} else if statErr == nil && targetBeforeClose != nil {
		result.targetStable = os.SameFile(targetBeforeClose, targetAfterClose)
	} else if statErr != nil {
		t.Fatal(statErr)
	}
	if !result.targetStable {
		t.Fatal("session close changed target identity")
	}
	return result
}

func TestDocumentV2ActiveAndRestartRollbackActionTrace(t *testing.T) {
	t.Run("active", testDocumentV2ActivePublishedRollbackActionTrace)
	t.Run("restart", testDocumentV2RestartRollbackActionTrace)
}

func testDocumentV2ActivePublishedRollbackActionTrace(t *testing.T) {

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	trigger := errors.New("active rollback trigger")
	var actual []documentV2RecoveryAction
	var anchorName string
	var anchorInfo os.FileInfo
	forwardInstall := true
	finalizing := false
	hooks := documentPublishHooks{
		afterAnchorSync: func(parent *os.Root, name string) error {
			actual = append(actual, documentV2ActionCreateAnchor)
			anchorName = name
			var err error
			anchorInfo, err = parent.Lstat(name)
			return err
		},
		afterRestoreSync: func(*os.Root, string) error {
			actual = append(actual, documentV2ActionCreateRestoreInstall)
			return nil
		},
		afterDiscardSync: func(*os.Root, string) error {
			actual = append(actual, documentV2ActionVacatePublished)
			return context.Canceled
		},
		afterInstall: func(*os.Root, string) error {
			if forwardInstall {
				forwardInstall = false
				return trigger
			}
			return nil
		},
		afterRestoreConsumed: func(parent *os.Root, _ string) error {
			actual = append(actual, documentV2ActionConsumeRestoreInstall)
			current, err := parent.Lstat("concept.md")
			if err != nil || anchorInfo == nil || !os.SameFile(current, anchorInfo) {
				return errors.New("active rollback target is detached from its anchor")
			}
			return nil
		},
		beforeCleanup: func(*os.Root, string, string) error {
			if !finalizing {
				actual = append(actual, documentV2ActionFinalizeRollback)
				finalizing = true
			}
			return nil
		},
	}
	session, err := openDocumentSessionContext(
		context.Background(), target, "", documentSessionHooks{publish: hooks},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))

	// Act.
	activeErr := session.RewriteContext(context.Background(), replacement)

	// Assert.
	wantActions := []documentV2RecoveryAction{
		documentV2ActionCreateAnchor,
		documentV2ActionCreateRestoreInstall,
		documentV2ActionVacatePublished,
		documentV2ActionConsumeRestoreInstall,
		documentV2ActionFinalizeRollback,
	}
	if !errors.Is(activeErr, trigger) || !errors.Is(activeErr, context.Canceled) {
		t.Fatalf("active error = %v", activeErr)
	}
	if !reflect.DeepEqual(actual, wantActions) {
		t.Fatalf("active actions = %v, want %v", actual, wantActions)
	}
	if documentSessionReadFile(t, target) != original {
		t.Fatal("active rollback target payload differs from the original")
	}
	if _, err := os.Lstat(filepath.Join(root, anchorName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("active rollback retained proof after terminal convergence: %v", err)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func testDocumentV2RestartRollbackActionTrace(t *testing.T) {

	// Arrange.
	fixture := newDocumentV2PlanFixture(
		t,
		documentV2State(false, true, false, false, true, documentV2TargetStage),
	)
	anchorInfo, err := os.Lstat(fixture.paths["anchor-0"])
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareDocumentV2PlanFixture(t, fixture)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(prepared.close)
	var actual []documentV2RecoveryAction
	finalizing := false
	prepared.hooks = documentPublishHooks{
		afterRestoreSync: func(*os.Root, string) error {
			actual = append(actual, documentV2ActionCreateRestoreInstall)
			return nil
		},
		afterDiscardSync: func(*os.Root, string) error {
			actual = append(actual, documentV2ActionVacatePublished)
			return nil
		},
		afterRestoreConsumed: func(parent *os.Root, _ string) error {
			actual = append(actual, documentV2ActionConsumeRestoreInstall)
			current, err := parent.Lstat(filepath.Base(fixture.target))
			if err != nil || !os.SameFile(current, anchorInfo) {
				return errors.New("restart rollback target is detached from its anchor")
			}
			return nil
		},
		beforeCleanup: func(*os.Root, string, string) error {
			if !finalizing {
				actual = append(actual, documentV2ActionFinalizeRollback)
				finalizing = true
			}
			return nil
		},
	}

	// Act.
	restartErr := prepared.apply(context.Background())

	// Assert.
	wantActions := []documentV2RecoveryAction{
		documentV2ActionCreateRestoreInstall,
		documentV2ActionVacatePublished,
		documentV2ActionConsumeRestoreInstall,
		documentV2ActionFinalizeRollback,
	}
	if restartErr != nil {
		t.Fatalf("restart error = %v", restartErr)
	}
	if !reflect.DeepEqual(actual, wantActions) {
		t.Fatalf("restart actions = %v, want %v", actual, wantActions)
	}
	if documentSessionReadFile(t, fixture.target) != fixture.original {
		t.Fatal("restart rollback target payload differs from the original")
	}
	if current, err := os.Lstat(fixture.target); err != nil || !os.SameFile(current, anchorInfo) {
		t.Fatalf("restart target identity changed: info=%v err=%v", current, err)
	}
	documentSessionAssertNoArtifacts(t, fixture.root)
}

func TestDocumentV2RestartMatchesIndependentOracleForEveryLegalPhase(t *testing.T) {
	for _, row := range documentV2RecoveryStates {
		row := row
		t.Run(fmt.Sprintf("phase-%d", row.phase), func(t *testing.T) {
			// Arrange.
			fixture := newDocumentV2PlanFixture(t, row.state)
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatal(err)
			}
			initial, _ := os.Lstat(fixture.target)
			wantActions, wantBytes, wantRole := documentV2RestartOracle(row.phase, fixture)
			var actions []documentV2RecoveryAction
			finalizing := false
			prepared.hooks = documentPublishHooks{
				afterAnchorSync: func(*os.Root, string) error {
					actions = append(actions, documentV2ActionCreateAnchor)
					return nil
				},
				afterRestoreSync: func(*os.Root, string) error {
					actions = append(actions, documentV2ActionCreateRestoreInstall)
					return nil
				},
				afterDiscardSync: func(*os.Root, string) error {
					actions = append(actions, documentV2ActionVacatePublished)
					return nil
				},
				afterRestoreConsumed: func(*os.Root, string) error {
					actions = append(actions, documentV2ActionConsumeRestoreInstall)
					return nil
				},
				beforeCleanup: func(*os.Root, string, string) error {
					if !finalizing {
						decision, decisionErr := documentV2RestartDecision(row.phase)
						if decisionErr != nil {
							return decisionErr
						}
						actions = append(actions, documentV2TerminalActionForDecision(decision))
						finalizing = true
					}
					return nil
				},
			}

			// Act.
			err = prepared.apply(context.Background())
			prepared.close()

			// Assert.
			if err != nil || !reflect.DeepEqual(actions, wantActions) {
				t.Fatalf("error=%v actions=%v want=%v", err, actions, wantActions)
			}
			result := observeDocumentRestartParity(t, fixture, initial)
			if result.bytes != wantBytes || result.mode != 0o644 ||
				result.identityRole != wantRole || result.residue != "" {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func documentV2RestartOracle(
	phase documentV2RecoveryPhase,
	fixture documentV2PlanFixture,
) ([]documentV2RecoveryAction, string, string) {
	finalizeRollback := documentV2ActionFinalizeRollback
	switch phase {
	case documentV2PhasePreNew:
		return []documentV2RecoveryAction{documentV2ActionFinalizeAbort}, fixture.original, "initial-inode"
	case documentV2PhasePreNewVacated:
		return []documentV2RecoveryAction{documentV2ActionCreateAnchor, documentV2ActionCreateRestoreInstall, documentV2ActionConsumeRestoreInstall, finalizeRollback}, fixture.original, "recovered-inode"
	case documentV2PhasePreNewRollbackAnchored:
		return []documentV2RecoveryAction{documentV2ActionCreateRestoreInstall, documentV2ActionConsumeRestoreInstall, finalizeRollback}, fixture.original, "recovered-inode"
	case documentV2PhasePreNewRollbackReady:
		return []documentV2RecoveryAction{documentV2ActionConsumeRestoreInstall, finalizeRollback}, fixture.original, "recovered-inode"
	case documentV2PhasePreNewRestored:
		return []documentV2RecoveryAction{finalizeRollback}, fixture.original, "initial-inode"
	case documentV2PhasePublished:
		return []documentV2RecoveryAction{documentV2ActionFinalizeCommit}, fixture.published, "initial-inode"
	case documentV2PhaseRollbackAnchored:
		return []documentV2RecoveryAction{documentV2ActionCreateRestoreInstall, documentV2ActionVacatePublished, documentV2ActionConsumeRestoreInstall, finalizeRollback}, fixture.original, "recovered-inode"
	case documentV2PhaseRollbackReady:
		return []documentV2RecoveryAction{documentV2ActionVacatePublished, documentV2ActionConsumeRestoreInstall, finalizeRollback}, fixture.original, "recovered-inode"
	case documentV2PhasePreRestore:
		return []documentV2RecoveryAction{documentV2ActionConsumeRestoreInstall, finalizeRollback}, fixture.original, "recovered-inode"
	case documentV2PhaseRestored:
		return []documentV2RecoveryAction{finalizeRollback}, fixture.original, "initial-inode"
	default:
		return nil, "", ""
	}
}

func documentV2TerminalActionForDecision(decision documentV2RecoveryDecision) documentV2RecoveryAction {
	switch decision {
	case documentV2DecisionAbort:
		return documentV2ActionFinalizeAbort
	case documentV2DecisionCommit:
		return documentV2ActionFinalizeCommit
	default:
		return documentV2ActionFinalizeRollback
	}
}

func TestDocumentV2RestartCancellationAfterDurableVacateConvergesWithValues(t *testing.T) {
	// Arrange.
	type contextKey struct{}
	const wantValue = "restart-convergence-value"
	fixture := newDocumentV2PlanFixture(
		t,
		documentV2State(false, true, true, false, true, documentV2TargetStage),
	)
	anchor, err := os.Lstat(fixture.paths["anchor-0"])
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareDocumentV2PlanFixture(t, fixture)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(prepared.close)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, wantValue))
	consumed := false
	prepared.hooks.afterDiscardSync = func(*os.Root, string) error {
		cancel()
		return context.Canceled
	}
	prepared.hooks.afterRestoreConsumed = func(*os.Root, string) error {
		transitionCtx := prepared.groups[0].transitionCtx
		if transitionCtx == nil || transitionCtx.Err() != nil || transitionCtx.Value(contextKey{}) != wantValue {
			return errors.New("restart convergence lost caller context values")
		}
		consumed = true
		return nil
	}

	// Act.
	err = prepared.apply(ctx)

	// Assert.
	if !errors.Is(err, context.Canceled) || !consumed {
		t.Fatalf("apply error=%v consumed=%t", err, consumed)
	}
	current, statErr := os.Lstat(fixture.target)
	if statErr != nil || !os.SameFile(anchor, current) || current.Mode() != anchor.Mode() ||
		documentSessionReadFile(t, fixture.target) != fixture.original {
		t.Fatalf("target did not converge: info=%v error=%v", current, statErr)
	}
	prepared.close()
	residue, prepareErr := prepareDocumentV2PlanFixture(t, fixture)
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	t.Cleanup(residue.close)
	if len(residue.actions) != 0 {
		t.Fatalf("terminal convergence retained recovery actions=%+v", residue.actions)
	}
	documentSessionAssertNoArtifacts(t, fixture.root)
}

type documentRestartParity struct {
	bytes        string
	mode         fs.FileMode
	identityRole string
	residue      string
}

func observeDocumentRestartParity(
	t *testing.T,
	fixture documentV2PlanFixture,
	initial os.FileInfo,
) documentRestartParity {
	t.Helper()
	current, err := os.Lstat(fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	role := "recovered-inode"
	if initial != nil && os.SameFile(initial, current) {
		role = "initial-inode"
	}
	result := documentRestartParity{
		bytes:        documentSessionReadFile(t, fixture.target),
		mode:         current.Mode().Perm(),
		identityRole: role,
		residue:      documentSessionPublicationArtifactDebug(t, fixture.root),
	}
	if result.residue != "" {
		t.Fatalf("restart retained residue: %s", result.residue)
	}
	return result
}

func TestDocumentV2RestartPrivateEvidenceCutsPreserveResidue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		state      documentV2RecoveryState
		residue    documentV2RecoveryPhase
		installCut bool
	}{
		{"anchor", documentV2State(true, false, false, false, true, documentV2TargetMissing), documentV2PhasePreNewRollbackAnchored, false},
		{"restore install", documentV2State(false, true, false, false, true, documentV2TargetStage), documentV2PhaseRollbackReady, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			fixture := newDocumentV2PlanFixture(t, testCase.state)
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatal(err)
			}
			sentinel := errors.New("private evidence cut: " + testCase.name)
			if testCase.installCut {
				prepared.hooks.afterRestoreSync = func(*os.Root, string) error { return sentinel }
			} else {
				prepared.hooks.afterAnchorSync = func(*os.Root, string) error { return sentinel }
			}
			var beforeInfo os.FileInfo
			var beforeBytes []byte
			if testCase.state.target != documentV2TargetMissing {
				beforeInfo, err = os.Lstat(fixture.target)
				if err != nil {
					t.Fatal(err)
				}
				beforeBytes, err = os.ReadFile(fixture.target)
				if err != nil {
					t.Fatal(err)
				}
			}

			// Act.
			recoveryErr := prepared.apply(context.Background())
			prepared.close()
			residue, prepareErr := prepareDocumentV2PlanFixture(t, fixture)
			if prepareErr != nil {
				t.Fatal(prepareErr)
			}
			t.Cleanup(residue.close)

			// Assert.
			if !errors.Is(recoveryErr, sentinel) {
				t.Fatalf("recovery error=%v", recoveryErr)
			}
			if len(residue.actions) != 1 || residue.actions[0].phase != testCase.residue {
				t.Fatalf("recovery residue=%v, want phase=%v", residue.actions, testCase.residue)
			}
			if testCase.state.target == documentV2TargetMissing {
				if _, err := os.Lstat(fixture.target); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("private anchor cut changed missing target: %v", err)
				}
				return
			}
			afterInfo, err := os.Lstat(fixture.target)
			if err != nil || !os.SameFile(beforeInfo, afterInfo) || beforeInfo.Mode() != afterInfo.Mode() {
				t.Fatalf("private restore cut changed target identity/mode: before=%v after=%v err=%v", beforeInfo, afterInfo, err)
			}
			afterBytes, err := os.ReadFile(fixture.target)
			if err != nil || !reflect.DeepEqual(beforeBytes, afterBytes) {
				t.Fatalf("private restore cut changed target bytes: %v", err)
			}
		})
	}
}

func documentV2StateForPhaseForTest(
	t *testing.T,
	phase documentV2RecoveryPhase,
) documentV2RecoveryState {
	if t != nil {
		t.Helper()
	}
	for _, row := range documentV2RecoveryStates {
		if row.phase == phase {
			return row.state
		}
	}
	if t != nil {
		t.Fatalf("no document v2 contract state for phase %v", phase)
	}
	return documentV2RecoveryState{target: documentV2TargetForeign}
}
