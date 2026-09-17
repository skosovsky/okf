package bundle

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

func TestDocumentV2RecoveryTransitionOracleCoversEveryLegalEntry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		phase    documentV2RecoveryPhase
		decision documentV2RecoveryDecision
		actions  []documentV2RecoveryAction
		final    documentV2RecoveryPhase
	}{
		{"abort pre-new", documentV2PhasePreNew, documentV2DecisionAbort, []documentV2RecoveryAction{documentV2ActionFinalizeAbort}, documentV2PhasePreNew},
		{"pre-new vacated", documentV2PhasePreNewVacated, documentV2DecisionRollback, []documentV2RecoveryAction{documentV2ActionCreateAnchor, documentV2ActionCreateRestoreInstall, documentV2ActionConsumeRestoreInstall, documentV2ActionFinalizeRollback}, documentV2PhasePreNewRestored},
		{"pre-new anchored", documentV2PhasePreNewRollbackAnchored, documentV2DecisionRollback, []documentV2RecoveryAction{documentV2ActionCreateRestoreInstall, documentV2ActionConsumeRestoreInstall, documentV2ActionFinalizeRollback}, documentV2PhasePreNewRestored},
		{"pre-new ready", documentV2PhasePreNewRollbackReady, documentV2DecisionRollback, []documentV2RecoveryAction{documentV2ActionConsumeRestoreInstall, documentV2ActionFinalizeRollback}, documentV2PhasePreNewRestored},
		{"pre-new restored", documentV2PhasePreNewRestored, documentV2DecisionRollback, []documentV2RecoveryAction{documentV2ActionFinalizeRollback}, documentV2PhasePreNewRestored},
		{"commit published", documentV2PhasePublished, documentV2DecisionCommit, []documentV2RecoveryAction{documentV2ActionFinalizeCommit}, documentV2PhasePublished},
		{"rollback published", documentV2PhasePublished, documentV2DecisionRollback, []documentV2RecoveryAction{documentV2ActionCreateAnchor, documentV2ActionCreateRestoreInstall, documentV2ActionVacatePublished, documentV2ActionConsumeRestoreInstall, documentV2ActionFinalizeRollback}, documentV2PhaseRestored},
		{"published anchored", documentV2PhaseRollbackAnchored, documentV2DecisionRollback, []documentV2RecoveryAction{documentV2ActionCreateRestoreInstall, documentV2ActionVacatePublished, documentV2ActionConsumeRestoreInstall, documentV2ActionFinalizeRollback}, documentV2PhaseRestored},
		{"published ready", documentV2PhaseRollbackReady, documentV2DecisionRollback, []documentV2RecoveryAction{documentV2ActionVacatePublished, documentV2ActionConsumeRestoreInstall, documentV2ActionFinalizeRollback}, documentV2PhaseRestored},
		{"pre-restore", documentV2PhasePreRestore, documentV2DecisionRollback, []documentV2RecoveryAction{documentV2ActionConsumeRestoreInstall, documentV2ActionFinalizeRollback}, documentV2PhaseRestored},
		{"restored", documentV2PhaseRestored, documentV2DecisionRollback, []documentV2RecoveryAction{documentV2ActionFinalizeRollback}, documentV2PhaseRestored},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			initial := documentV2StateForContractTest(t, testCase.phase)

			// Act.
			execution, err := executeDocumentV2RecoveryTransitions(
				context.Background(), initial, testCase.decision,
				func(_ context.Context, transition documentV2RecoveryTransition) (documentV2RecoveryObservation, error) {
					if transition.terminal {
						return documentV2RecoveryObservation{terminal: true, proofRemoved: true}, nil
					}
					return documentV2RecoveryObservation{
						state:             documentV2StateForContractTest(t, transition.to),
						canonicalMutation: transition.canonicalMutation,
					}, nil
				},
			)

			// Assert.
			if err != nil || execution.phase != testCase.final || !reflect.DeepEqual(execution.actions, testCase.actions) {
				t.Fatalf("execution=%+v error=%v, want phase=%v actions=%v", execution, err, testCase.final, testCase.actions)
			}
		})
	}
	if _, err := documentV2RecoveryTransitionFor(documentV2PhasePreNew, documentV2DecisionCommit); err == nil {
		t.Fatal("illegal pre-new commit transition accepted")
	}
}

func TestDocumentV2RecoveryOracleStructure(t *testing.T) {
	t.Parallel()

	// Arrange.
	if len(documentV2RecoveryStates) != 10 || len(documentV2RecoveryTransitions) != 11 {
		t.Fatalf("oracle rows states=%d transitions=%d, want 10/11", len(documentV2RecoveryStates), len(documentV2RecoveryTransitions))
	}
	phases := make(map[documentV2RecoveryPhase]struct{}, len(documentV2RecoveryStates))
	for _, row := range documentV2RecoveryStates {
		if row.phase == documentV2PhaseInvalid {
			t.Fatal("oracle contains invalid phase")
		}
		if _, duplicate := phases[row.phase]; duplicate {
			t.Fatalf("oracle contains duplicate phase %v", row.phase)
		}
		phases[row.phase] = struct{}{}
	}
	type key struct {
		phase    documentV2RecoveryPhase
		decision documentV2RecoveryDecision
	}
	pairs := make(map[key]struct{}, len(documentV2RecoveryTransitions))
	for _, transition := range documentV2RecoveryTransitions {
		pair := key{transition.from, transition.decision}
		if _, duplicate := pairs[pair]; duplicate {
			t.Fatalf("oracle contains duplicate transition %+v", pair)
		}
		pairs[pair] = struct{}{}
		if _, exists := phases[transition.from]; !exists {
			t.Fatalf("transition source %v is not a legal phase", transition.from)
		}
		if transition.terminal {
			if transition.to != documentV2PhaseInvalid || !transition.proofLast || transition.canonicalMutation {
				t.Fatalf("terminal transition violates proof-last invariants: %+v", transition)
			}
			continue
		}
		if _, exists := phases[transition.to]; !exists || transition.proofLast {
			t.Fatalf("nonterminal transition has invalid endpoint/invariants: %+v", transition)
		}
		wantMutation := transition.action == documentV2ActionVacatePublished ||
			transition.action == documentV2ActionConsumeRestoreInstall
		if transition.canonicalMutation != wantMutation {
			t.Fatalf("transition canonical mutation mismatch: %+v", transition)
		}
	}

	// Act / Assert.
	for phase := range phases {
		for decision := documentV2DecisionAbort; decision <= documentV2DecisionRollback; decision++ {
			_, present := pairs[key{phase, decision}]
			_, err := documentV2RecoveryTransitionFor(phase, decision)
			if present != (err == nil) {
				t.Fatalf("transition lookup phase=%v decision=%v present=%t error=%v", phase, decision, present, err)
			}
		}
	}
}

func TestDocumentV2RecoveryExecutorCancellationBoundary(t *testing.T) {
	t.Parallel()

	// Arrange.
	ctx, cancel := context.WithCancel(context.Background())
	steps := 0

	// Act.
	execution, err := executeDocumentV2RecoveryTransitions(
		ctx,
		documentV2StateForContractTest(t, documentV2PhaseRollbackReady),
		documentV2DecisionRollback,
		func(stepCtx context.Context, transition documentV2RecoveryTransition) (documentV2RecoveryObservation, error) {
			steps++
			switch steps {
			case 1:
				cancel()
				return documentV2RecoveryObservation{
					state: documentV2StateForContractTest(t, transition.to), canonicalMutation: true,
				}, context.Canceled
			case 2:
				if stepCtx.Err() != nil {
					t.Fatalf("post-mutation convergence context error = %v", stepCtx.Err())
				}
				return documentV2RecoveryObservation{
					state: documentV2StateForContractTest(t, transition.to), canonicalMutation: true,
				}, nil
			case 3:
				if stepCtx.Err() != nil || !transition.terminal || !transition.proofLast {
					t.Fatalf("terminal convergence context=%v transition=%+v", stepCtx.Err(), transition)
				}
				return documentV2RecoveryObservation{terminal: true, proofRemoved: true}, nil
			default:
				t.Fatalf("unexpected convergence step %d", steps)
				return documentV2RecoveryObservation{}, nil
			}
		},
	)

	// Assert.
	if !errors.Is(err, context.Canceled) || execution.phase != documentV2PhaseRestored || steps != 3 {
		t.Fatalf("execution=%+v error=%v steps=%d", execution, err, steps)
	}
	preCtx, preCancel := context.WithCancel(context.Background())
	preCancel()
	called := false
	_, preErr := executeDocumentV2RecoveryTransitions(
		preCtx,
		documentV2StateForContractTest(t, documentV2PhaseRollbackReady),
		documentV2DecisionRollback,
		func(context.Context, documentV2RecoveryTransition) (documentV2RecoveryObservation, error) {
			called = true
			return documentV2RecoveryObservation{}, nil
		},
	)
	if !errors.Is(preErr, context.Canceled) || called {
		t.Fatalf("pre-mutation cancellation error=%v called=%t", preErr, called)
	}
}

func TestDocumentV2RecoveryExecutorPreservesDoubleFaultOrder(t *testing.T) {
	t.Parallel()

	// Arrange.
	first := errors.New("first canonical fault")
	second := errors.New("second convergence fault")
	steps := 0

	// Act.
	execution, err := executeDocumentV2RecoveryTransitions(
		context.Background(),
		documentV2StateForContractTest(t, documentV2PhaseRollbackReady),
		documentV2DecisionRollback,
		func(_ context.Context, transition documentV2RecoveryTransition) (documentV2RecoveryObservation, error) {
			steps++
			if steps == 1 {
				return documentV2RecoveryObservation{
					state: documentV2StateForContractTest(t, transition.to), canonicalMutation: true,
				}, first
			}
			return documentV2RecoveryObservation{
				state: documentV2StateForContractTest(t, transition.from),
			}, second
		},
	)

	// Assert.
	if !errors.Is(err, first) || !errors.Is(err, second) || err.Error() != first.Error()+"\n"+second.Error() ||
		execution.phase != documentV2PhasePreRestore || steps != 2 {
		t.Fatalf("execution=%+v error=%q steps=%d", execution, err, steps)
	}

	cleanup := errors.New("terminal cleanup fault")
	cleanupExecution, cleanupErr := executeDocumentV2RecoveryTransitions(
		context.Background(),
		documentV2StateForContractTest(t, documentV2PhasePreRestore),
		documentV2DecisionRollback,
		func(_ context.Context, transition documentV2RecoveryTransition) (documentV2RecoveryObservation, error) {
			if transition.terminal {
				return documentV2RecoveryObservation{
					state: documentV2StateForContractTest(t, transition.from),
				}, cleanup
			}
			return documentV2RecoveryObservation{
				state: documentV2StateForContractTest(t, transition.to), canonicalMutation: true,
			}, nil
		},
	)
	if !errors.Is(cleanupErr, cleanup) || cleanupErr.Error() != cleanup.Error() ||
		cleanupExecution.phase != documentV2PhaseRestored ||
		!reflect.DeepEqual(cleanupExecution.actions, []documentV2RecoveryAction{
			documentV2ActionConsumeRestoreInstall,
			documentV2ActionFinalizeRollback,
		}) {
		t.Fatalf("cleanup execution=%+v error=%v", cleanupExecution, cleanupErr)
	}
}

func TestDocumentV2RecoveryExecutorReturnsExactTerminalCleanupCause(t *testing.T) {
	t.Parallel()

	// Arrange.
	cleanupErr := errors.New("terminal cleanup fault")
	steps := 0

	// Act.
	execution, err := executeDocumentV2RecoveryTransitions(
		context.Background(),
		documentV2StateForContractTest(t, documentV2PhasePublished),
		documentV2DecisionCommit,
		func(context.Context, documentV2RecoveryTransition) (documentV2RecoveryObservation, error) {
			steps++
			return documentV2RecoveryObservation{}, cleanupErr
		},
	)

	// Assert.
	if err != cleanupErr || execution.phase != documentV2PhasePublished || steps != 1 {
		t.Fatalf("execution=%+v error=%v steps=%d", execution, err, steps)
	}
}

func documentV2StateForContractTest(t *testing.T, phase documentV2RecoveryPhase) documentV2RecoveryState {
	t.Helper()
	for _, row := range documentV2RecoveryStates {
		if row.phase == phase {
			return row.state
		}
	}
	t.Fatalf("missing state for phase %v", phase)
	return documentV2RecoveryState{}
}

func TestDocumentV2RecoveryClassifierLegalStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		state documentV2RecoveryState
		want  documentV2RecoveryPhase
	}{
		{name: "pre new", state: documentV2State(true, false, false, false, false, documentV2TargetWitness), want: documentV2PhasePreNew},
		{name: "pre new vacated", state: documentV2State(true, false, false, false, true, documentV2TargetMissing), want: documentV2PhasePreNewVacated},
		{name: "pre new rollback anchored", state: documentV2State(true, true, false, false, true, documentV2TargetMissing), want: documentV2PhasePreNewRollbackAnchored},
		{name: "pre new rollback ready", state: documentV2State(true, true, true, false, true, documentV2TargetMissing), want: documentV2PhasePreNewRollbackReady},
		{name: "pre new restored", state: documentV2State(true, true, false, false, true, documentV2TargetAnchor), want: documentV2PhasePreNewRestored},
		{name: "published", state: documentV2State(false, false, false, false, true, documentV2TargetStage), want: documentV2PhasePublished},
		{name: "rollback anchored", state: documentV2State(false, true, false, false, true, documentV2TargetStage), want: documentV2PhaseRollbackAnchored},
		{name: "rollback ready", state: documentV2State(false, true, true, false, true, documentV2TargetStage), want: documentV2PhaseRollbackReady},
		{name: "pre restore", state: documentV2State(false, true, true, true, true, documentV2TargetMissing), want: documentV2PhasePreRestore},
		{name: "restored", state: documentV2State(false, true, false, true, true, documentV2TargetAnchor), want: documentV2PhaseRestored},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			before := testCase.state

			// Act.
			got, err := classifyDocumentV2RecoveryState(testCase.state)

			// Assert.
			if err != nil || got != testCase.want {
				t.Fatalf("classifyDocumentV2RecoveryState() = %v, %v; want %v", got, err, testCase.want)
			}
			if testCase.state != before {
				t.Fatal("classifyDocumentV2RecoveryState() mutated its input")
			}
		})
	}
}

func TestDocumentV2RecoveryClassifierRejectsIllegalStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		state documentV2RecoveryState
	}{
		{name: "missing retained stage", state: documentV2RecoveryState{witness: true, target: documentV2TargetWitness}},
		{name: "missing retained witness", state: documentV2RecoveryState{stage: true, backup: true, backupIndependent: true, target: documentV2TargetWitness}},
		{name: "missing retained backup", state: documentV2RecoveryState{stage: true, witness: true, target: documentV2TargetWitness}},
		{name: "backup aliases witness", state: func() documentV2RecoveryState {
			state := documentV2State(true, false, false, false, false, documentV2TargetWitness)
			state.backupIndependent = false
			return state
		}()},
		{name: "missing without ownership token", state: documentV2State(false, false, false, false, true, documentV2TargetMissing)},
		{name: "new token detached from stage", state: withDetachedNew(documentV2State(true, false, false, false, false, documentV2TargetWitness))},
		{name: "restore token without anchor", state: documentV2State(false, false, true, false, true, documentV2TargetStage)},
		{name: "restore token detached from anchor", state: withDetachedRestore(documentV2State(false, true, true, false, true, documentV2TargetStage))},
		{name: "discard detached from stage", state: withDetachedDiscard(documentV2State(false, true, true, true, true, documentV2TargetMissing))},
		{name: "claim detached from witness", state: withDetachedClaim(documentV2State(false, false, false, false, true, documentV2TargetStage))},
		{name: "new and discard coexist", state: documentV2State(true, true, false, true, true, documentV2TargetMissing)},
		{name: "foreign target", state: documentV2State(true, false, false, false, false, documentV2TargetForeign)},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			before := testCase.state

			// Act.
			got, err := classifyDocumentV2RecoveryState(testCase.state)

			// Assert.
			if err == nil || got != documentV2PhaseInvalid {
				t.Fatalf("classifyDocumentV2RecoveryState() = %v, %v; want hard rejection", got, err)
			}
			if testCase.state != before {
				t.Fatal("classifyDocumentV2RecoveryState() mutated its input")
			}
		})
	}
}

func TestDocumentV2MissingTargetRequiresExactInstallToken(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		state documentV2RecoveryState
		want  bool
	}{
		{name: "new install token", state: documentV2State(true, false, false, false, true, documentV2TargetMissing), want: true},
		{name: "restore install token", state: documentV2State(false, true, true, true, true, documentV2TargetMissing), want: true},
		{name: "missing without token", state: documentV2State(false, true, false, false, true, documentV2TargetMissing)},
		{name: "detached new token", state: withDetachedNew(documentV2State(true, false, false, false, true, documentV2TargetMissing))},
		{name: "detached restore token", state: withDetachedRestore(documentV2State(false, true, true, true, true, documentV2TargetMissing))},
		{name: "occupied target", state: documentV2State(true, false, false, false, false, documentV2TargetWitness)},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			before := testCase.state

			// Act.
			got := documentV2MissingTargetWritable(testCase.state)

			// Assert.
			if got != testCase.want {
				t.Fatalf("documentV2MissingTargetWritable() = %v; want %v", got, testCase.want)
			}
			if testCase.state != before {
				t.Fatal("documentV2MissingTargetWritable() mutated its input")
			}
		})
	}
}

func TestDocumentV2ManifestRequiresCanonicalBoundPaths(t *testing.T) {
	t.Parallel()

	valid := validDocumentV2ManifestForTest()
	cases := []struct {
		name   string
		mutate func(*documentTransactionManifest)
	}{
		{name: "missing stage", mutate: func(manifest *documentTransactionManifest) { manifest.Stage = "" }},
		{name: "missing new install", mutate: func(manifest *documentTransactionManifest) { manifest.NewInstall = "" }},
		{name: "missing anchor", mutate: func(manifest *documentTransactionManifest) { manifest.Anchor = "" }},
		{name: "missing restore install", mutate: func(manifest *documentTransactionManifest) { manifest.RestoreInstall = "" }},
		{name: "missing discard", mutate: func(manifest *documentTransactionManifest) { manifest.Discard = "" }},
		{name: "slot one anchor", mutate: func(manifest *documentTransactionManifest) {
			manifest.Anchor = documentRollbackAnchorNameForContract(
				manifest.Target,
				fs.FileMode(manifest.Mode),
				manifest.OriginalSize,
				manifest.OriginalSHA256,
				1,
			)
		}},
		{name: "artifact outside target directory", mutate: func(manifest *documentTransactionManifest) {
			manifest.NewInstall = "foreign/" + manifest.NewInstall
		}},
		{name: "wrong payload binding", mutate: func(manifest *documentTransactionManifest) {
			manifest.Discard = documentBoundArtifactNameForContract(
				"discard",
				manifest.Target,
				fs.FileMode(manifest.Mode),
				manifest.PublishedSize+1,
				manifest.PublishedSHA256,
			)
		}},
	}

	if err := validateDocumentV2ManifestPaths(valid, "docs"); err != nil {
		t.Fatalf("validateDocumentV2ManifestPaths(valid) error = %v", err)
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			manifest := validDocumentV2ManifestForTest()
			testCase.mutate(&manifest)

			// Act.
			err := validateDocumentV2ManifestPaths(manifest, "docs")

			// Assert.
			if err == nil {
				t.Fatal("validateDocumentV2ManifestPaths() error = nil, want non-canonical manifest")
			}
		})
	}
}

func TestDocumentV2NamespaceRejectsLegacyAndUnknownFamilies(t *testing.T) {
	t.Parallel()

	rule := documentNamespaceRule()
	cases := []struct {
		name     string
		artifact string
		wantKind string
	}{
		{name: "v1 manifest", artifact: ".okf-document-txn-v1-manifest-" + strings.Repeat("a", 64), wantKind: "unknown"},
		{name: "future manifest", artifact: ".okf-document-txn-v3-manifest-" + strings.Repeat("a", 64), wantKind: "unknown"},
		{name: "case folded v2", artifact: ".OKF-DOCUMENT-TXN-V2-MANIFEST-" + strings.Repeat("a", 64), wantKind: "unknown"},
		{name: "malformed v2", artifact: documentTransactionPrefix + "manifest-short", wantKind: "unknown"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			before := testCase.artifact

			// Act.
			kind, reserved := rule.classify(testCase.artifact)

			// Assert.
			if !reserved || kind != testCase.wantKind {
				t.Fatalf("classify(%q) = %q, %v; want %q, true", testCase.artifact, kind, reserved, testCase.wantKind)
			}
			if !isDocumentTransactionReservedPath("nested/" + testCase.artifact) {
				t.Fatalf("isDocumentTransactionReservedPath(%q) = false", testCase.artifact)
			}
			if testCase.artifact != before {
				t.Fatal("namespace classification mutated its input")
			}
		})
	}
}

func TestDocumentV2IdentityAliasContract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		left    documentV2IdentityRole
		right   documentV2IdentityRole
		wantErr bool
	}{
		{name: "new aliases stage", left: documentV2IdentityNewInstall, right: documentV2IdentityStage},
		{name: "restore aliases anchor", left: documentV2IdentityRestoreInstall, right: documentV2IdentityAnchor},
		{name: "discard aliases stage", left: documentV2IdentityDiscard, right: documentV2IdentityStage},
		{name: "claim aliases witness", left: documentV2IdentityClaim, right: documentV2IdentityWitness},
		{name: "target aliases stage", left: documentV2IdentityTarget, right: documentV2IdentityStage},
		{name: "new cannot alias anchor", left: documentV2IdentityNewInstall, right: documentV2IdentityAnchor, wantErr: true},
		{name: "restore cannot alias stage", left: documentV2IdentityRestoreInstall, right: documentV2IdentityStage, wantErr: true},
		{name: "discard cannot alias anchor", left: documentV2IdentityDiscard, right: documentV2IdentityAnchor, wantErr: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			relations := []documentV2IdentityRelation{{
				left:     testCase.left,
				right:    testCase.right,
				sameFile: true,
			}}

			// Act.
			err := validateDocumentV2IdentityRelations(relations)

			// Assert.
			if testCase.wantErr && err == nil {
				t.Fatal("validateDocumentV2IdentityRelations() error = nil, want forbidden alias")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("validateDocumentV2IdentityRelations() error = %v, want allowed alias", err)
			}
		})
	}
}

func documentV2State(
	n, a, r, d, claim bool,
	target documentV2TargetRelation,
) documentV2RecoveryState {
	return documentV2RecoveryState{
		stage: true, backup: true, witness: true, backupIndependent: true,
		newInstall: n, anchor: a, restoreInstall: r, discard: d, claim: claim,
		newAliasesStage: n, restoreAliasesAnchor: r, discardAliasesStage: d,
		claimAliasesWitness: claim,
		target:              target,
	}
}

func withDetachedNew(state documentV2RecoveryState) documentV2RecoveryState {
	state.newAliasesStage = false
	return state
}

func withDetachedRestore(state documentV2RecoveryState) documentV2RecoveryState {
	state.restoreAliasesAnchor = false
	return state
}

func withDetachedDiscard(state documentV2RecoveryState) documentV2RecoveryState {
	state.discardAliasesStage = false
	return state
}

func withDetachedClaim(state documentV2RecoveryState) documentV2RecoveryState {
	state.claimAliasesWitness = false
	return state
}

func validDocumentV2ManifestForTest() documentTransactionManifest {
	const target = "docs/concept.md"
	const directory = "docs"
	const originalDigest = "6f1c590b41eaac55ef533086e6d74c0f3110c0dec2b51e10b116633d8b56927d"
	const publishedDigest = "75d3b84f617a2d123c4d5b4d4ad6e77f0f191a1b31ae39166e0e9468f9d12055"
	mode := fs.FileMode(0o644)
	manifest := documentTransactionManifest{
		Format:          documentTransactionFormat,
		Target:          target,
		Mode:            uint32(mode),
		OriginalSize:    8,
		OriginalSHA256:  originalDigest,
		PublishedSize:   10,
		PublishedSHA256: publishedDigest,
	}
	manifest.Stage = pathJoin(directory, documentBoundArtifactNameForContract(
		"stage", target, mode, manifest.PublishedSize, manifest.PublishedSHA256,
	))
	manifest.NewInstall = pathJoin(directory, documentBoundArtifactNameForContract(
		"new-install", target, mode, manifest.PublishedSize, manifest.PublishedSHA256,
	))
	manifest.Anchor = pathJoin(directory, documentRollbackAnchorNameForContract(
		target, mode, manifest.OriginalSize, manifest.OriginalSHA256, 0,
	))
	manifest.RestoreInstall = pathJoin(directory, documentBoundArtifactNameForContract(
		"restore-install", target, mode, manifest.OriginalSize, manifest.OriginalSHA256,
	))
	manifest.Discard = pathJoin(directory, documentBoundArtifactNameForContract(
		"discard", target, mode, manifest.PublishedSize, manifest.PublishedSHA256,
	))
	return manifest
}
