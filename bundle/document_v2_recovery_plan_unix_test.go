//go:build darwin || linux

package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type documentV2PlanFixture struct {
	root      string
	target    string
	paths     map[string]string
	original  string
	published string
}

func TestDocumentV2RecoveryPreNewCrashSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_DOCUMENT_V2_RECOVERY_PRE_NEW_ROOT")
	point := os.Getenv("OKF_DOCUMENT_V2_RECOVERY_PRE_NEW_POINT")
	if root == "" || point == "" {
		return
	}
	crash := func() error {
		os.Exit(90)
		return nil
	}
	fixture := newDocumentV2PlanFixtureAt(
		t,
		root,
		documentV2State(true, false, false, false, true, documentV2TargetMissing),
	)
	prepared, err := prepareDocumentV2PlanFixture(t, fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	consumeRenamed := false
	prepared.hooks = documentPublishHooks{
		afterAnchorSync: func(*os.Root, string) error {
			if point == "anchor-durable" {
				return crash()
			}
			return nil
		},
		afterRestoreSync: func(*os.Root, string) error {
			if point == "restore-durable" {
				return crash()
			}
			return nil
		},
		afterInstall: func(*os.Root, string) error {
			consumeRenamed = true
			if point == "consume-post-rename" {
				return crash()
			}
			return nil
		},
		directorySync: func(parent *os.Root) error {
			if consumeRenamed && point == "consume-pre-sync" {
				return crash()
			}
			return syncPublicationDirectoryPlatform(parent)
		},
		afterRestoreConsumed: func(*os.Root, string) error {
			if point == "consume-post-sync" {
				return crash()
			}
			return nil
		},
	}
	if err := prepared.apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("recovery returned without crashing at %s", point)
}

func TestDocumentV2RecoveryTerminalDecisionCrashSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_DOCUMENT_V2_TERMINAL_ROOT")
	if root == "" {
		return
	}
	state := documentV2State(true, true, false, false, true, documentV2TargetAnchor)
	if os.Getenv("OKF_DOCUMENT_V2_TERMINAL_STATE") == "restored" {
		state = documentV2State(false, true, false, true, true, documentV2TargetAnchor)
	}
	fixture := newDocumentV2PlanFixtureAt(
		t,
		root,
		state,
	)
	prepared, err := prepareDocumentV2PlanFixture(t, fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	prepared.hooks = documentPublishHooks{
		afterCleanup: func(_ *os.Root, kind, _ string) error {
			if kind == "manifest" {
				os.Exit(91)
			}
			return nil
		},
	}
	if err := prepared.apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Fatal("terminal decision returned without crashing after M")
}

func TestDocumentV2RecoveryPublishedRollbackCrashSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_DOCUMENT_V2_PUBLISHED_ROLLBACK_ROOT")
	point := os.Getenv("OKF_DOCUMENT_V2_PUBLISHED_ROLLBACK_POINT")
	if root == "" || point == "" {
		return
	}
	crash := func() error {
		os.Exit(92)
		return nil
	}
	state := documentV2State(false, true, true, false, true, documentV2TargetStage)
	switch point {
	case "restore-token-durable":
		state = documentV2State(false, true, false, false, true, documentV2TargetStage)
	case "discard-post-rename", "discard-pre-sync", "discard-post-sync":
	case "restore-post-rename", "restore-pre-sync", "restore-post-sync":
		state = documentV2State(false, true, true, true, true, documentV2TargetMissing)
	default:
		t.Fatalf("unknown published rollback crash point %q", point)
	}
	fixture := newDocumentV2PlanFixtureAt(t, root, state)
	prepared, err := prepareDocumentV2PlanFixture(t, fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	discardRenamed := false
	restoreRenamed := false
	prepared.hooks = documentPublishHooks{
		afterRestoreSync: func(*os.Root, string) error {
			if point == "restore-token-durable" {
				return crash()
			}
			return nil
		},
		afterVacateRename: func(*os.Root, string, string) error {
			discardRenamed = true
			if point == "discard-post-rename" {
				return crash()
			}
			return nil
		},
		afterDiscardSync: func(*os.Root, string) error {
			if point == "discard-post-sync" {
				return crash()
			}
			return nil
		},
		afterInstall: func(*os.Root, string) error {
			restoreRenamed = true
			if point == "restore-post-rename" {
				return crash()
			}
			return nil
		},
		afterRestoreConsumed: func(*os.Root, string) error {
			if point == "restore-post-sync" {
				return crash()
			}
			return nil
		},
		directorySync: func(parent *os.Root) error {
			if discardRenamed && point == "discard-pre-sync" {
				return crash()
			}
			if restoreRenamed && point == "restore-pre-sync" {
				return crash()
			}
			return syncPublicationDirectoryPlatform(parent)
		},
	}
	if err := prepared.apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("published rollback returned without crashing at %s", point)
}

func TestDocumentV2RecoveryNoManifestCleanupCrashSubprocessHelper(t *testing.T) {
	root := os.Getenv("OKF_DOCUMENT_V2_NO_MANIFEST_CLEANUP_ROOT")
	form := os.Getenv("OKF_DOCUMENT_V2_NO_MANIFEST_CLEANUP_FORM")
	ordinalText := os.Getenv("OKF_DOCUMENT_V2_NO_MANIFEST_CLEANUP_ORDINAL")
	if root == "" || form == "" || ordinalText == "" {
		return
	}
	ordinal, err := strconv.Atoi(ordinalText)
	if err != nil {
		t.Fatal(err)
	}
	var state documentV2RecoveryState
	switch form {
	case "commit":
		state = documentV2State(false, false, false, false, true, documentV2TargetStage)
	case "untouched":
		state = documentV2State(true, false, false, false, false, documentV2TargetWitness)
	case "pre-new-restored":
		state = documentV2State(true, true, false, false, true, documentV2TargetAnchor)
	case "published-restored":
		state = documentV2State(false, true, false, true, true, documentV2TargetAnchor)
	default:
		t.Fatalf("unknown no-M cleanup form %q", form)
	}
	fixture := newDocumentV2PlanFixtureAt(t, root, state)
	documentV2RemoveManifestForTest(t, fixture)
	cleanupOrdinal := 0
	session, recoveryErr := openDocumentSessionContext(
		context.Background(),
		fixture.target,
		"",
		documentSessionHooks{publish: documentPublishHooks{
			afterCleanup: func(*os.Root, string, string) error {
				current := cleanupOrdinal
				cleanupOrdinal++
				if current == ordinal {
					os.Exit(93)
				}
				return nil
			},
		}},
	)
	if session != nil {
		_ = session.Close()
	}
	t.Fatalf(
		"no-M cleanup returned before crash form=%s ordinal=%d: session=%#v error=%v",
		form,
		ordinal,
		session,
		recoveryErr,
	)
}

func TestDocumentV2RecoveryPlannerClassifiesTenLegalPhasesWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state documentV2RecoveryState
		phase documentV2RecoveryPhase
	}{
		{name: "pre-new", state: documentV2State(true, false, false, false, false, documentV2TargetWitness), phase: documentV2PhasePreNew},
		{name: "pre-new-vacated", state: documentV2State(true, false, false, false, true, documentV2TargetMissing), phase: documentV2PhasePreNewVacated},
		{name: "pre-new-rollback-anchored", state: documentV2State(true, true, false, false, true, documentV2TargetMissing), phase: documentV2PhasePreNewRollbackAnchored},
		{name: "pre-new-rollback-ready", state: documentV2State(true, true, true, false, true, documentV2TargetMissing), phase: documentV2PhasePreNewRollbackReady},
		{name: "pre-new-restored", state: documentV2State(true, true, false, false, true, documentV2TargetAnchor), phase: documentV2PhasePreNewRestored},
		{name: "published", state: documentV2State(false, false, false, false, true, documentV2TargetStage), phase: documentV2PhasePublished},
		{name: "rollback-anchored", state: documentV2State(false, true, false, false, true, documentV2TargetStage), phase: documentV2PhaseRollbackAnchored},
		{name: "rollback-ready", state: documentV2State(false, true, true, false, true, documentV2TargetStage), phase: documentV2PhaseRollbackReady},
		{name: "pre-restore", state: documentV2State(false, true, true, true, true, documentV2TargetMissing), phase: documentV2PhasePreRestore},
		{name: "restored", state: documentV2State(false, true, false, true, true, documentV2TargetAnchor), phase: documentV2PhaseRestored},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(t, test.state)
			before := documentSessionCaptureTree(t, fixture.root)
			beforeIdentities := documentPublicationIdentities(t, fixture.root)

			// Act.
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)

			// Assert.
			if err != nil {
				t.Fatalf("prepareDocumentRecovery() error = %v", err)
			}
			defer prepared.close()
			if len(prepared.actions) != 1 ||
				prepared.actions[0].phase != test.phase ||
				prepared.actions[0].group.phase != test.phase ||
				prepared.actions[0].group.v2State != test.state {
				t.Fatalf(
					"planned actions = %#v state=%#v, want one immutable %v plan",
					prepared.actions,
					prepared.actions[0].group.v2State,
					test.phase,
				)
			}
			documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)

			switch test.phase {
			case documentV2PhasePreNew,
				documentV2PhasePreNewRestored,
				documentV2PhasePublished,
				documentV2PhasePreNewVacated,
				documentV2PhasePreNewRollbackAnchored,
				documentV2PhasePreNewRollbackReady,
				documentV2PhaseRollbackAnchored,
				documentV2PhaseRollbackReady,
				documentV2PhasePreRestore,
				documentV2PhaseRestored:
				return
			}
			applyErr := prepared.apply(context.Background())
			if !errors.Is(applyErr, errPublicationConflict) {
				t.Fatalf("read-only apply error = %v, want fail-closed conflict", applyErr)
			}
			documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
		})
	}
}

func TestDocumentV2RecoveryPreNewRollbackConvergesFromEveryEnabledPhase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state documentV2RecoveryState
	}{
		{name: "vacated", state: documentV2State(true, false, false, false, true, documentV2TargetMissing)},
		{name: "anchored", state: documentV2State(true, true, false, false, true, documentV2TargetMissing)},
		{name: "ready", state: documentV2State(true, true, true, false, true, documentV2TargetMissing)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(t, test.state)
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			var anchorInfo os.FileInfo
			if test.state.anchor {
				anchorInfo = documentV2ForwardStat(t, fixture.paths["anchor-0"])
			} else {
				prepared.hooks.afterAnchorSync = func(parent *os.Root, name string) error {
					info, statErr := parent.Lstat(name)
					if statErr != nil {
						return statErr
					}
					anchorInfo = info
					return nil
				}
			}

			// Act.
			err = prepared.apply(context.Background())

			// Assert.
			if err != nil {
				t.Fatalf("apply() error = %v", err)
			}
			group := prepared.actions[0].group
			if group.phase != documentV2PhasePreNewRestored ||
				group.v2State.target != documentV2TargetAnchor ||
				!group.v2State.newInstall ||
				group.v2State.restoreInstall ||
				group.v2State.discard ||
				!group.completed {
				t.Fatalf("final phase=%v state=%#v, want restored with retained N and no R/D", group.phase, group.v2State)
			}
			targetInfo := documentV2ForwardStat(t, fixture.target)
			if anchorInfo == nil ||
				!os.SameFile(targetInfo, anchorInfo) ||
				documentSessionReadFile(t, fixture.target) != fixture.original {
				t.Fatal("pre-new recovery cleanup lost target~A")
			}
			documentSessionAssertNoArtifacts(t, filepath.Dir(fixture.target))
		})
	}
}

func TestDocumentV2RecoveryPreNewRollbackDurableCrashCuts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		point string
		phase documentV2RecoveryPhase
	}{
		{point: "anchor-durable", phase: documentV2PhasePreNewRollbackAnchored},
		{point: "restore-durable", phase: documentV2PhasePreNewRollbackReady},
		{point: "consume-post-rename", phase: documentV2PhasePreNewRestored},
		{point: "consume-pre-sync", phase: documentV2PhasePreNewRestored},
		{point: "consume-post-sync", phase: documentV2PhasePreNewRestored},
	}
	for _, test := range tests {
		test := test
		t.Run(test.point, func(t *testing.T) {
			t.Parallel()

			// Arrange and Act.
			root := t.TempDir()
			runDocumentV2RecoveryPreNewCrashHelper(t, root, test.point)
			fixture := documentV2PlanFixture{
				root:      root,
				target:    filepath.Join(root, "docs", "concept.md"),
				original:  documentSessionConcept("concept", "before"),
				published: documentSessionConcept("concept", "after"),
			}
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatalf("prepare crash image error = %v", err)
			}
			defer prepared.close()

			// Assert.
			group := prepared.actions[0].group
			if prepared.actions[0].phase != test.phase ||
				group.phase != test.phase ||
				!group.v2State.newInstall ||
				group.v2State.discard {
				t.Fatalf("cut %s phase=%v state=%#v, want %v with N and no D", test.point, group.phase, group.v2State, test.phase)
			}
			artifacts := documentPrepareCrashArtifacts(t, filepath.Join(root, "docs"))
			stageInfo := documentV2ForwardStat(t, artifacts["stage"])
			newInfo := documentV2ForwardStat(t, artifacts["new-install"])
			if !os.SameFile(stageInfo, newInfo) {
				t.Fatalf("cut %s lost N~S", test.point)
			}
			if artifacts["discard"] != "" {
				t.Fatalf("cut %s created D: %#v", test.point, artifacts)
			}
			if test.phase != documentV2PhasePreNewRestored {
				if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("cut %s target error = %v, want missing", test.point, statErr)
				}
			}
			anchorInfo := documentV2ForwardStat(t, artifacts["anchor-0"])
			if err := prepared.apply(context.Background()); err != nil {
				t.Fatalf("resume after %s error = %v", test.point, err)
			}
			if group.phase != documentV2PhasePreNewRestored || !group.completed {
				t.Fatalf("resume after %s phase=%v completed=%t, want restored completion", test.point, group.phase, group.completed)
			}
			targetInfo := documentV2ForwardStat(t, fixture.target)
			if !os.SameFile(targetInfo, anchorInfo) ||
				documentSessionReadFile(t, fixture.target) != fixture.original {
				t.Fatalf("cut %s restart lost target~A exact Old", test.point)
			}
			documentSessionAssertNoArtifacts(t, filepath.Join(root, "docs"))
		})
	}
}

func TestDocumentV2RecoveryPublishedRollbackConvergesFromEveryEnabledPhase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state documentV2RecoveryState
	}{
		{
			name:  "published anchored",
			state: documentV2State(false, true, false, false, true, documentV2TargetStage),
		},
		{
			name:  "published restore ready",
			state: documentV2State(false, true, true, false, true, documentV2TargetStage),
		},
		{
			name:  "pre-restore",
			state: documentV2State(false, true, true, true, true, documentV2TargetMissing),
		},
		{
			name:  "restored terminal",
			state: documentV2State(false, true, false, true, true, documentV2TargetAnchor),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(t, test.state)
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			anchorInfo := documentV2ForwardStat(t, fixture.paths["anchor-0"])

			// Act.
			err = prepared.apply(context.Background())

			// Assert.
			if err != nil {
				t.Fatalf("apply() error = %v", err)
			}
			group := prepared.actions[0].group
			if group.phase != documentV2PhaseRestored ||
				group.v2State.newInstall ||
				!group.v2State.anchor ||
				group.v2State.restoreInstall ||
				!group.v2State.discard ||
				group.v2State.target != documentV2TargetAnchor ||
				!group.completed {
				t.Fatalf("final phase=%v state=%#v, want restored with A/D and no N/R", group.phase, group.v2State)
			}
			targetInfo := documentV2ForwardStat(t, fixture.target)
			if !os.SameFile(targetInfo, anchorInfo) ||
				documentSessionReadFile(t, fixture.target) != fixture.original {
				t.Fatal("published rollback cleanup lost target~A")
			}
			documentSessionAssertNoArtifacts(t, filepath.Dir(fixture.target))
		})
	}
}

func TestDocumentV2RecoveryPublishedRollbackDurableCrashCuts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		point string
		phase documentV2RecoveryPhase
	}{
		{point: "restore-token-durable", phase: documentV2PhaseRollbackReady},
		{point: "discard-post-rename", phase: documentV2PhasePreRestore},
		{point: "discard-pre-sync", phase: documentV2PhasePreRestore},
		{point: "discard-post-sync", phase: documentV2PhasePreRestore},
		{point: "restore-post-rename", phase: documentV2PhaseRestored},
		{point: "restore-pre-sync", phase: documentV2PhaseRestored},
		{point: "restore-post-sync", phase: documentV2PhaseRestored},
	}
	for _, test := range tests {
		test := test
		t.Run(test.point, func(t *testing.T) {
			t.Parallel()

			// Arrange and Act.
			root := t.TempDir()
			runDocumentV2PublishedRollbackCrashHelper(t, root, test.point)
			fixture := documentV2PlanFixture{
				root:      root,
				target:    filepath.Join(root, "docs", "concept.md"),
				original:  documentSessionConcept("concept", "before"),
				published: documentSessionConcept("concept", "after"),
			}
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatalf("prepare crash image error = %v", err)
			}
			defer prepared.close()

			// Assert.
			group := prepared.actions[0].group
			if prepared.actions[0].phase != test.phase ||
				group.phase != test.phase ||
				group.v2State.newInstall ||
				!group.v2State.anchor ||
				!group.v2State.claim {
				t.Fatalf("cut %s phase=%v state=%#v, want %v with A/C and no N", test.point, group.phase, group.v2State, test.phase)
			}
			artifacts := documentPrepareCrashArtifacts(t, filepath.Join(root, "docs"))
			for _, kind := range []string{"manifest", "witness", "backup", "stage", "claim", "anchor-0"} {
				if artifacts[kind] == "" {
					t.Fatalf("cut %s lacks %s: %#v", test.point, kind, artifacts)
				}
			}
			anchorInfo := documentV2ForwardStat(t, artifacts["anchor-0"])
			stageInfo := documentV2ForwardStat(t, artifacts["stage"])
			switch test.phase {
			case documentV2PhaseRollbackReady:
				restoreInfo := documentV2ForwardStat(t, artifacts["restore-install"])
				targetInfo := documentV2ForwardStat(t, fixture.target)
				if artifacts["discard"] != "" ||
					!os.SameFile(restoreInfo, anchorInfo) ||
					!os.SameFile(targetInfo, stageInfo) {
					t.Fatalf("cut %s lost target~S or R~A: %#v", test.point, artifacts)
				}
			case documentV2PhasePreRestore:
				restoreInfo := documentV2ForwardStat(t, artifacts["restore-install"])
				discardInfo := documentV2ForwardStat(t, artifacts["discard"])
				if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("cut %s target error = %v, want missing", test.point, statErr)
				}
				if !os.SameFile(restoreInfo, anchorInfo) ||
					!os.SameFile(discardInfo, stageInfo) {
					t.Fatalf("cut %s lost R~A or D~S: %#v", test.point, artifacts)
				}
			case documentV2PhaseRestored:
				if artifacts["restore-install"] != "" {
					t.Fatalf("cut %s retained R: %#v", test.point, artifacts)
				}
				targetInfo := documentV2ForwardStat(t, fixture.target)
				discardInfo := documentV2ForwardStat(t, artifacts["discard"])
				if !os.SameFile(targetInfo, anchorInfo) ||
					!os.SameFile(discardInfo, stageInfo) {
					t.Fatalf("cut %s lost target~A or D~S: %#v", test.point, artifacts)
				}
			default:
				t.Fatalf("unexpected crash phase %v", test.phase)
			}

			session, reopenErr := OpenDocumentSessionContext(
				context.Background(),
				fixture.target,
				"",
			)
			if reopenErr != nil || session == nil {
				t.Fatalf(
					"public restart after %s session=%#v error=%v, want recovery success",
					test.point,
					session,
					reopenErr,
				)
			}
			if closeErr := session.Close(); closeErr != nil {
				t.Fatalf("close public restart after %s: %v", test.point, closeErr)
			}
			targetInfo := documentV2ForwardStat(t, fixture.target)
			if !os.SameFile(targetInfo, anchorInfo) ||
				documentSessionReadFile(t, fixture.target) != fixture.original {
				t.Fatalf("public restart after %s lost target~A or original bytes", test.point)
			}
			documentSessionAssertNoArtifacts(t, filepath.Join(root, "docs"))
		})
	}
}

func TestDocumentV2RecoveryPublishedRollbackAnchoredPublicRestartReachesManifestBoundary(t *testing.T) {
	t.Parallel()

	// Arrange.
	fixture := newDocumentV2PlanFixture(
		t,
		documentV2State(false, true, false, false, true, documentV2TargetStage),
	)
	anchorInfo := documentV2ForwardStat(t, fixture.paths["anchor-0"])

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), fixture.target, "")

	// Assert.
	if err != nil || session == nil {
		t.Fatalf("public restart session=%#v error=%v, want recovery success", session, err)
	}
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	targetInfo := documentV2ForwardStat(t, fixture.target)
	if !os.SameFile(targetInfo, anchorInfo) ||
		documentSessionReadFile(t, fixture.target) != fixture.original {
		t.Fatal("anchored public restart lost target~A or original bytes")
	}
	documentSessionAssertNoArtifacts(t, filepath.Dir(fixture.target))
}

func TestDocumentV2RecoveryPublishedRejectsNonPhaseInventoryWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*testing.T, documentV2PlanFixture)
	}{
		{
			name: "extra exact new-install",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				t.Helper()
				if err := os.Link(fixture.paths["stage"], fixture.paths["new-install"]); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra exact restore-install without anchor",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				t.Helper()
				if err := os.Link(fixture.paths["witness"], fixture.paths["restore-install"]); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra exact discard before vacate",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				t.Helper()
				if err := os.Link(fixture.paths["stage"], fixture.paths["discard"]); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "foreign same-class stage",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				t.Helper()
				name := documentBoundArtifactName(
					"stage",
					"docs/foreign.md",
					0o644,
					[]byte(fixture.published),
				)
				documentSessionWriteFile(
					t,
					filepath.Join(filepath.Dir(fixture.target), name),
					fixture.published,
				)
			},
		},
		{
			name: "malformed rollback anchor",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				t.Helper()
				documentSessionWriteFile(
					t,
					filepath.Join(
						filepath.Dir(fixture.target),
						documentTransactionPrefix+"anchor-not-canonical",
					),
					fixture.original,
				)
			},
		},
		{
			name: "foreign canonical rollback anchor",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				t.Helper()
				foreign := []byte("foreign rollback anchor\n")
				name := documentRollbackAnchorName(
					"docs/concept.md",
					0o644,
					foreign,
					0,
				)
				documentSessionWriteFile(
					t,
					filepath.Join(filepath.Dir(fixture.target), name),
					string(foreign),
				)
			},
		},
		{
			name: "second exact rollback anchor",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				t.Helper()
				documentSessionWriteFile(
					t,
					fixture.paths["anchor-0"],
					fixture.original,
				)
				second := documentRollbackAnchorName(
					"docs/concept.md",
					0o644,
					[]byte(fixture.original),
					1,
				)
				if err := os.Link(
					fixture.paths["anchor-0"],
					filepath.Join(filepath.Dir(fixture.target), second),
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "foreign reserved-prefix inventory",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				t.Helper()
				documentSessionWriteFile(
					t,
					filepath.Join(
						filepath.Dir(fixture.target),
						documentTransactionPrefix+"foreign-evidence",
					),
					"foreign\n",
				)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(
				t,
				documentV2State(false, false, false, false, true, documentV2TargetStage),
			)
			test.mutate(t, fixture)
			before := documentSessionCaptureTree(t, fixture.root)
			beforeIdentities := documentPublicationIdentities(t, fixture.root)

			// Act.
			session, err := OpenDocumentSessionContext(
				context.Background(),
				fixture.target,
				"",
			)
			if session != nil {
				_ = session.Close()
			}

			// Assert.
			if session != nil || !errors.Is(err, ErrDocumentConflict) {
				t.Fatalf(
					"session=%#v error=%v, want fail-closed ErrDocumentConflict",
					session,
					err,
				)
			}
			documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
		})
	}
}

func TestDocumentV2RecoveryPublishedRollbackRejectsUnsafeEvidenceWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		state         documentV2RecoveryState
		publicRestart bool
		mutate        func(*testing.T, documentV2PlanFixture)
	}{
		{
			name:  "missing published target",
			state: documentV2State(false, true, true, false, true, documentV2TargetStage),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				if err := os.Remove(fixture.target); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "foreign published target",
			state: documentV2State(false, true, true, false, true, documentV2TargetStage),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.target, "foreign\n", 0o640)
			},
		},
		{
			name:  "missing discard",
			state: documentV2State(false, true, true, true, true, documentV2TargetMissing),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				if err := os.Remove(fixture.paths["discard"]); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:          "foreign discard",
			state:         documentV2State(false, true, true, true, true, documentV2TargetMissing),
			publicRestart: true,
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.paths["discard"], "foreign\n", 0o644)
			},
		},
		{
			name:          "in-place discard payload drift",
			state:         documentV2State(false, true, true, true, true, documentV2TargetMissing),
			publicRestart: true,
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				file, err := os.OpenFile(fixture.paths["discard"], os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.WriteAt([]byte("X"), 0)
				syncErr := file.Sync()
				closeErr := file.Close()
				if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "invalid discard mode",
			state: documentV2State(false, true, true, true, true, documentV2TargetMissing),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				if err := os.Chmod(fixture.paths["discard"], 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(t, test.state)
			test.mutate(t, fixture)
			before := documentSessionCaptureTree(t, fixture.root)
			beforeIdentities := documentPublicationIdentities(t, fixture.root)

			// Act.
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if prepared != nil {
				prepared.close()
			}

			// Assert.
			if err == nil {
				t.Fatal("prepareDocumentRecovery() error = nil, want hard rejection")
			}
			documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
			if !test.publicRestart {
				return
			}
			session, restartErr := OpenDocumentSessionContext(
				context.Background(),
				fixture.target,
				"",
			)
			if session != nil {
				_ = session.Close()
			}
			if session != nil || !errors.Is(restartErr, ErrDocumentConflict) {
				t.Fatalf(
					"public restart session=%#v error=%v, want typed hard D conflict",
					session,
					restartErr,
				)
			}
			documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
		})
	}
}

func TestDocumentV2RecoveryPublishedRollbackVacateMismatchCompensation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		inPlace bool
		blocked bool
	}{
		{name: "foreign D is hard"},
		{name: "in-place D payload drift is hard", inPlace: true},
		{name: "foreign D with target blocker", blocked: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(
				t,
				documentV2State(false, true, true, false, true, documentV2TargetStage),
			)
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			foreign := []byte("foreign discard\n")
			blocker := []byte("foreign target blocker\n")
			injected := false
			var discardPath string
			var foreignInfo, blockerInfo os.FileInfo
			var attackedTree map[string]documentSessionTreeEntry
			var attackedIdentities map[string]os.FileInfo
			prepared.hooks = documentPublishHooks{
				afterVacateRename: func(_ *os.Root, _, discard string) error {
					if injected {
						return nil
					}
					injected = true
					discardPath = filepath.Join(filepath.Dir(fixture.target), discard)
					if test.inPlace {
						file, err := os.OpenFile(discardPath, os.O_RDWR, 0)
						if err != nil {
							return err
						}
						_, writeErr := file.WriteAt([]byte("X"), 0)
						syncErr := file.Sync()
						closeErr := file.Close()
						if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
							return err
						}
					} else {
						if err := os.Remove(discardPath); err != nil {
							return err
						}
						if err := os.WriteFile(discardPath, foreign, 0o640); err != nil {
							return err
						}
					}
					var err error
					foreignInfo, err = os.Lstat(discardPath)
					if err != nil {
						return err
					}
					if test.blocked {
						if err := os.WriteFile(fixture.target, blocker, 0o600); err != nil {
							return err
						}
						blockerInfo, err = os.Lstat(fixture.target)
						if err != nil {
							return err
						}
					}
					attackedTree = documentSessionCaptureTree(t, fixture.root)
					attackedIdentities = documentPublicationIdentities(t, fixture.root)
					return nil
				},
			}

			// Act.
			err = prepared.apply(context.Background())

			// Assert.
			if !injected || !errors.Is(err, errPublicationConflict) {
				t.Fatalf("injected=%t apply error=%v, want hard D conflict", injected, err)
			}
			artifacts := documentPrepareCrashArtifacts(t, filepath.Dir(fixture.target))
			for _, kind := range []string{"manifest", "witness", "backup", "stage", "claim", "anchor-0", "restore-install", "discard"} {
				if artifacts[kind] == "" {
					t.Fatalf("hard D conflict lost %s: %#v", kind, artifacts)
				}
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				attackedTree,
				documentSessionCaptureTree(t, fixture.root),
			)
			documentAssertPublicationIdentities(t, fixture.root, attackedIdentities)
			if test.blocked {
				targetInfo := documentV2ForwardStat(t, fixture.target)
				discardInfo := documentV2ForwardStat(t, discardPath)
				if blockerInfo == nil ||
					foreignInfo == nil ||
					!os.SameFile(targetInfo, blockerInfo) ||
					!os.SameFile(discardInfo, foreignInfo) ||
					!bytes.Equal([]byte(documentSessionReadFile(t, fixture.target)), blocker) ||
					!bytes.Equal([]byte(documentSessionReadFile(t, discardPath)), foreign) {
					t.Fatal("blocked compensation changed foreign target or discard identity")
				}
			} else if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("hard D conflict changed missing target: %v", statErr)
			}
			discardInfo := documentV2ForwardStat(t, discardPath)
			if foreignInfo == nil || !os.SameFile(discardInfo, foreignInfo) {
				t.Fatal("hard D conflict changed attacked discard identity")
			}
			if !test.inPlace &&
				!bytes.Equal([]byte(documentSessionReadFile(t, discardPath)), foreign) {
				t.Fatal("hard D conflict changed foreign discard bytes")
			}

			// A public restart must repeat the typed conflict without touching
			// the attacked missing-target ownership state.
			session, restartErr := OpenDocumentSessionContext(
				context.Background(),
				fixture.target,
				"",
			)
			if session != nil {
				_ = session.Close()
			}
			if session != nil || !errors.Is(restartErr, ErrDocumentConflict) {
				t.Fatalf(
					"restart session=%#v error=%v, want typed hard D conflict",
					session,
					restartErr,
				)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				attackedTree,
				documentSessionCaptureTree(t, fixture.root),
			)
			documentAssertPublicationIdentities(t, fixture.root, attackedIdentities)
		})
	}
}

func TestDocumentV2RecoveryPublishedRollbackExactDHookErrorCompensates(t *testing.T) {
	t.Parallel()

	// Arrange.
	fixture := newDocumentV2PlanFixture(
		t,
		documentV2State(false, true, true, false, true, documentV2TargetStage),
	)
	prepared, err := prepareDocumentV2PlanFixture(t, fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	targetBefore := documentV2ForwardStat(t, fixture.target)
	stageBefore := documentV2ForwardStat(t, fixture.paths["stage"])
	beforeIdentities := documentPublicationIdentities(t, fixture.root)
	hookFault := errors.New("injected exact D post-rename fault")
	injected := false
	var discardPath string
	prepared.hooks = documentPublishHooks{
		afterVacateRename: func(_ *os.Root, _, discard string) error {
			injected = true
			discardPath = filepath.Join(filepath.Dir(fixture.target), discard)
			discardInfo := documentV2ForwardStat(t, discardPath)
			stageInfo := documentV2ForwardStat(t, fixture.paths["stage"])
			if !os.SameFile(discardInfo, stageInfo) ||
				documentSessionReadFile(t, discardPath) != fixture.published {
				return errors.New("post-rename D is not exact S")
			}
			return hookFault
		},
	}

	// Act.
	err = prepared.apply(context.Background())

	// Assert.
	if !injected || !errors.Is(err, hookFault) {
		t.Fatalf("injected=%t error=%v, want exact-D hook fault", injected, err)
	}
	targetAfter := documentV2ForwardStat(t, fixture.target)
	if !os.SameFile(targetBefore, targetAfter) ||
		!os.SameFile(stageBefore, targetAfter) ||
		documentSessionReadFile(t, fixture.target) != fixture.published {
		t.Fatal("exact-D compensation did not restore the published target")
	}
	if _, statErr := os.Lstat(discardPath); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("exact-D compensation retained D: %v", statErr)
	}
	afterIdentities := documentPublicationIdentities(t, fixture.root)
	if len(afterIdentities) != len(beforeIdentities) {
		t.Fatalf(
			"exact-D compensation file count changed: before=%d after=%d",
			len(beforeIdentities),
			len(afterIdentities),
		)
	}
	documentAssertPublicationIdentities(t, fixture.root, beforeIdentities)
}

func TestDocumentV2RecoveryPublishedRollbackRejectsForeignTargetBeforeRestoreConsume(t *testing.T) {
	t.Parallel()

	// Arrange.
	fixture := newDocumentV2PlanFixture(
		t,
		documentV2State(false, true, true, true, true, documentV2TargetMissing),
	)
	prepared, err := prepareDocumentV2PlanFixture(t, fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	foreign := "foreign restore blocker\n"
	var foreignInfo os.FileInfo
	prepared.hooks = documentPublishHooks{
		beforeInstall: func(*os.Root, string, string) error {
			documentSessionWriteFile(t, fixture.target, foreign)
			var err error
			foreignInfo, err = os.Lstat(fixture.target)
			return err
		},
	}

	// Act.
	err = prepared.apply(context.Background())

	// Assert.
	if !errors.Is(err, errPublicationConflict) {
		t.Fatalf("apply() error = %v, want occupied-target conflict", err)
	}
	artifacts := documentPrepareCrashArtifacts(t, filepath.Dir(fixture.target))
	for _, kind := range []string{"manifest", "witness", "backup", "stage", "claim", "anchor-0", "restore-install", "discard"} {
		if artifacts[kind] == "" {
			t.Fatalf("foreign target race lost %s: %#v", kind, artifacts)
		}
	}
	targetInfo := documentV2ForwardStat(t, fixture.target)
	anchorInfo := documentV2ForwardStat(t, artifacts["anchor-0"])
	restoreInfo := documentV2ForwardStat(t, artifacts["restore-install"])
	stageInfo := documentV2ForwardStat(t, artifacts["stage"])
	discardInfo := documentV2ForwardStat(t, artifacts["discard"])
	if foreignInfo == nil ||
		!os.SameFile(targetInfo, foreignInfo) ||
		os.SameFile(targetInfo, anchorInfo) ||
		!os.SameFile(restoreInfo, anchorInfo) ||
		!os.SameFile(discardInfo, stageInfo) ||
		documentSessionReadFile(t, fixture.target) != foreign {
		t.Fatal("restore consume overwrote foreign target or lost D/A/R proofs")
	}
}

func TestDocumentV2RecoveryRestoredManifestRemovalFailureRetainsProofs(t *testing.T) {
	t.Parallel()

	// Arrange.
	fixture := newDocumentV2PlanFixture(
		t,
		documentV2State(false, true, false, true, true, documentV2TargetAnchor),
	)
	prepared, err := prepareDocumentV2PlanFixture(t, fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	before := documentSessionCaptureTree(t, fixture.root)
	beforeIdentities := documentPublicationIdentities(t, fixture.root)
	manifestFault := errors.New("retain restored manifest")
	prepared.hooks = documentPublishHooks{
		beforeCleanup: func(_ *os.Root, kind, _ string) error {
			if kind == "manifest" {
				return manifestFault
			}
			return nil
		},
	}

	// Act.
	err = prepared.apply(context.Background())

	// Assert.
	if !errors.Is(err, manifestFault) {
		t.Fatalf("apply() error = %v, want manifest removal fault", err)
	}
	documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
	artifacts := documentPrepareCrashArtifacts(t, filepath.Dir(fixture.target))
	targetInfo := documentV2ForwardStat(t, fixture.target)
	anchorInfo := documentV2ForwardStat(t, artifacts["anchor-0"])
	stageInfo := documentV2ForwardStat(t, artifacts["stage"])
	discardInfo := documentV2ForwardStat(t, artifacts["discard"])
	if artifacts["manifest"] == "" ||
		!os.SameFile(targetInfo, anchorInfo) ||
		!os.SameFile(discardInfo, stageInfo) {
		t.Fatal("manifest removal failure lost M, target~A, or D~S proof")
	}
}

func TestDocumentV2RecoveryPlannerRejectsIllegalInventoryWithoutMutation(t *testing.T) {
	t.Parallel()

	type mutation func(*testing.T, documentV2PlanFixture)
	var tests []struct {
		name   string
		state  documentV2RecoveryState
		mutate mutation
	}
	remove := func(kind string) mutation {
		return func(t *testing.T, fixture documentV2PlanFixture) {
			t.Helper()
			if err := os.Remove(fixture.paths[kind]); err != nil {
				t.Fatal(err)
			}
		}
	}
	tests = append(tests,
		struct {
			name   string
			state  documentV2RecoveryState
			mutate mutation
		}{name: "missing witness", state: documentV2State(true, false, false, false, false, documentV2TargetWitness), mutate: remove("witness")},
		struct {
			name   string
			state  documentV2RecoveryState
			mutate mutation
		}{name: "missing backup", state: documentV2State(true, false, false, false, false, documentV2TargetWitness), mutate: remove("backup")},
		struct {
			name   string
			state  documentV2RecoveryState
			mutate mutation
		}{name: "missing stage", state: documentV2State(true, false, false, false, false, documentV2TargetWitness), mutate: remove("stage")},
		struct {
			name   string
			state  documentV2RecoveryState
			mutate mutation
		}{name: "missing consumed new token", state: documentV2State(true, false, false, false, true, documentV2TargetMissing), mutate: remove("new-install")},
		struct {
			name   string
			state  documentV2RecoveryState
			mutate mutation
		}{name: "missing consumed restore token", state: documentV2State(false, true, true, true, true, documentV2TargetMissing), mutate: remove("restore-install")},
		struct {
			name   string
			state  documentV2RecoveryState
			mutate mutation
		}{name: "missing target without token", state: documentV2State(false, false, false, false, true, documentV2TargetStage), mutate: func(t *testing.T, fixture documentV2PlanFixture) {
			t.Helper()
			if err := os.Remove(fixture.target); err != nil {
				t.Fatal(err)
			}
		}},
		struct {
			name   string
			state  documentV2RecoveryState
			mutate mutation
		}{name: "foreign target", state: documentV2State(false, false, false, false, true, documentV2TargetStage), mutate: func(t *testing.T, fixture documentV2PlanFixture) {
			t.Helper()
			documentSessionAtomicReplace(t, fixture.target, "foreign\n", 0o640)
		}},
		struct {
			name   string
			state  documentV2RecoveryState
			mutate mutation
		}{name: "backup aliases witness", state: documentV2State(true, false, false, false, false, documentV2TargetWitness), mutate: func(t *testing.T, fixture documentV2PlanFixture) {
			t.Helper()
			if err := os.Remove(fixture.paths["backup"]); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(fixture.paths["witness"], fixture.paths["backup"]); err != nil {
				t.Fatal(err)
			}
		}},
	)
	for _, role := range []string{"new-install", "restore-install", "discard"} {
		role := role
		state := documentV2State(true, false, false, false, true, documentV2TargetMissing)
		payload := "published"
		if role == "restore-install" {
			state = documentV2State(false, true, true, false, true, documentV2TargetStage)
			payload = "original"
		} else if role == "discard" {
			state = documentV2State(false, true, true, true, true, documentV2TargetMissing)
		}
		for _, corruption := range []string{"swap", "type", "mode", "content"} {
			corruption := corruption
			tests = append(tests, struct {
				name   string
				state  documentV2RecoveryState
				mutate mutation
			}{
				name:  role + " " + corruption,
				state: state,
				mutate: func(t *testing.T, fixture documentV2PlanFixture) {
					t.Helper()
					path := fixture.paths[role]
					if corruption == "mode" {
						if err := os.Chmod(path, 0o600); err != nil {
							t.Fatal(err)
						}
						return
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					switch corruption {
					case "swap":
						data := fixture.published
						if payload == "original" {
							data = fixture.original
						}
						documentSessionWriteFile(t, path, data)
					case "type":
						if err := os.Mkdir(path, 0o755); err != nil {
							t.Fatal(err)
						}
					case "content":
						documentSessionWriteFile(t, path, "tampered\n")
					}
				},
			})
		}
	}
	for _, artifact := range []string{
		".okf-document-txn-v1-manifest-" + strings.Repeat("a", 64),
		".okf-document-txn-v3-manifest-" + strings.Repeat("a", 64),
		documentTransactionPrefix + "rollback-" + strings.Repeat("a", 64),
	} {
		artifact := artifact
		tests = append(tests, struct {
			name   string
			state  documentV2RecoveryState
			mutate mutation
		}{
			name:  "reserved unknown " + artifact[:min(len(artifact), 32)],
			state: documentV2State(true, false, false, false, false, documentV2TargetWitness),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				t.Helper()
				documentSessionWriteFile(t, filepath.Join(filepath.Dir(fixture.target), artifact), "{}")
			},
		})
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(t, test.state)
			test.mutate(t, fixture)
			before := documentSessionCaptureTree(t, fixture.root)
			beforeIdentities := documentPublicationIdentities(t, fixture.root)

			// Act.
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if prepared != nil {
				prepared.close()
			}

			// Assert.
			if err == nil {
				t.Fatal("prepareDocumentRecovery() error = nil, want hard rejection")
			}
			documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
		})
	}
}

func TestDocumentV2RecoveryRollbackAnchoredTreatsWitnessAnchorAliasAsSoftConflict(t *testing.T) {
	t.Parallel()

	// Arrange a legal RollbackAnchored phase, then retain exact original W
	// under A. A remains an exact hard source; the W/A role alias is soft
	// after A is durable.
	fixture := newDocumentV2PlanFixture(
		t,
		documentV2State(false, true, false, false, true, documentV2TargetStage),
	)
	if err := os.Remove(fixture.paths["anchor-0"]); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(fixture.paths["witness"], fixture.paths["anchor-0"]); err != nil {
		t.Fatal(err)
	}
	manifestBefore := documentV2ForwardStat(t, fixture.paths["manifest"])
	witnessBefore := documentV2ForwardStat(t, fixture.paths["witness"])
	backupBefore := documentV2ForwardStat(t, fixture.paths["backup"])
	stageBefore := documentV2ForwardStat(t, fixture.paths["stage"])
	claimBefore := documentV2ForwardStat(t, fixture.paths["claim"])
	anchorBefore := documentV2ForwardStat(t, fixture.paths["anchor-0"])
	if !os.SameFile(witnessBefore, claimBefore) ||
		!os.SameFile(witnessBefore, anchorBefore) ||
		os.SameFile(backupBefore, witnessBefore) {
		t.Fatal("fixture does not encode exact soft W/C/A alias with independent B")
	}

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), fixture.target, "")
	if session != nil {
		_ = session.Close()
	}

	// Assert: recovery converged through durable R/A, retained the soft
	// alias conflict, and stopped before terminal cleanup.
	if session != nil || !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("session=%#v error=%v, want typed soft-evidence conflict", session, err)
	}
	artifacts := documentPrepareCrashArtifacts(t, filepath.Dir(fixture.target))
	wantKinds := []string{
		"anchor-0",
		"backup",
		"claim",
		"discard",
		"manifest",
		"stage",
		"witness",
	}
	if got := documentPrepareCrashArtifactKinds(artifacts); !reflect.DeepEqual(got, wantKinds) {
		t.Fatalf("retained artifact kinds = %#v, want %#v", got, wantKinds)
	}
	targetInfo := documentV2ForwardStat(t, fixture.target)
	manifestAfter := documentV2ForwardStat(t, artifacts["manifest"])
	witnessAfter := documentV2ForwardStat(t, artifacts["witness"])
	backupAfter := documentV2ForwardStat(t, artifacts["backup"])
	stageAfter := documentV2ForwardStat(t, artifacts["stage"])
	claimAfter := documentV2ForwardStat(t, artifacts["claim"])
	anchorAfter := documentV2ForwardStat(t, artifacts["anchor-0"])
	discardAfter := documentV2ForwardStat(t, artifacts["discard"])
	if !os.SameFile(targetInfo, anchorAfter) ||
		!os.SameFile(anchorAfter, witnessAfter) ||
		!os.SameFile(witnessAfter, claimAfter) ||
		os.SameFile(backupAfter, anchorAfter) ||
		!os.SameFile(discardAfter, stageAfter) {
		t.Fatal("soft W/C/A alias did not converge to target~A with retained D~S proof")
	}
	for name, pair := range map[string][2]os.FileInfo{
		"manifest": {manifestBefore, manifestAfter},
		"witness":  {witnessBefore, witnessAfter},
		"backup":   {backupBefore, backupAfter},
		"stage":    {stageBefore, stageAfter},
		"claim":    {claimBefore, claimAfter},
		"anchor":   {anchorBefore, anchorAfter},
	} {
		if !os.SameFile(pair[0], pair[1]) {
			t.Fatalf("soft convergence changed retained %s identity", name)
		}
	}
	if got := documentSessionReadFile(t, fixture.target); got != fixture.original {
		t.Fatalf("converged target bytes = %q, want %q", got, fixture.original)
	}
	if _, statErr := os.Lstat(fixture.paths["restore-install"]); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("consumed R error = %v, want missing", statErr)
	}
}

func TestDocumentV2RecoveryPlanRevalidationRejectsDriftWithoutFurtherMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*testing.T, documentV2PlanFixture)
	}{
		{name: "target", mutate: func(t *testing.T, fixture documentV2PlanFixture) {
			documentSessionAtomicReplace(t, fixture.target, "foreign\n", 0o640)
		}},
		{name: "artifact", mutate: func(t *testing.T, fixture documentV2PlanFixture) {
			documentSessionAtomicReplace(t, fixture.paths["backup"], fixture.original, 0o644)
		}},
		{name: "parent", mutate: func(t *testing.T, fixture documentV2PlanFixture) {
			directory := filepath.Dir(fixture.target)
			detached := directory + ".detached"
			if err := os.Rename(directory, detached); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(directory, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(
				t,
				documentV2State(false, false, false, false, true, documentV2TargetStage),
			)
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			test.mutate(t, fixture)
			before := documentSessionCaptureTree(t, fixture.root)
			beforeIdentities := documentPublicationIdentities(t, fixture.root)

			// Act.
			err = prepared.apply(context.Background())

			// Assert.
			if err == nil {
				t.Fatal("apply() error = nil, want drift rejection")
			}
			documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
		})
	}
}

func TestDocumentV2RecoveryPublishedRollbackPlanRejectsPhaseDriftWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		state  documentV2RecoveryState
		mutate func(*testing.T, documentV2PlanFixture)
	}{
		{
			name:  "rollback-ready target",
			state: documentV2State(false, true, true, false, true, documentV2TargetStage),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.target, "foreign\n", 0o640)
			},
		},
		{
			name:  "rollback-ready restore",
			state: documentV2State(false, true, true, false, true, documentV2TargetStage),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.paths["restore-install"], fixture.original, 0o644)
			},
		},
		{
			name:  "pre-restore restore",
			state: documentV2State(false, true, true, true, true, documentV2TargetMissing),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.paths["restore-install"], fixture.original, 0o644)
			},
		},
		{
			name:  "pre-restore discard",
			state: documentV2State(false, true, true, true, true, documentV2TargetMissing),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.paths["discard"], fixture.published, 0o644)
			},
		},
		{
			name:  "restored target",
			state: documentV2State(false, true, false, true, true, documentV2TargetAnchor),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.target, "foreign\n", 0o640)
			},
		},
		{
			name:  "restored anchor",
			state: documentV2State(false, true, false, true, true, documentV2TargetAnchor),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.paths["anchor-0"], fixture.original, 0o644)
			},
		},
		{
			name:  "restored discard",
			state: documentV2State(false, true, false, true, true, documentV2TargetAnchor),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.paths["discard"], fixture.published, 0o644)
			},
		},
		{
			name:  "restored parent",
			state: documentV2State(false, true, false, true, true, documentV2TargetAnchor),
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				directory := filepath.Dir(fixture.target)
				detached := directory + ".detached"
				if err := os.Rename(directory, detached); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(t, test.state)
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			test.mutate(t, fixture)
			before := documentSessionCaptureTree(t, fixture.root)
			beforeIdentities := documentPublicationIdentities(t, fixture.root)

			// Act.
			err = prepared.apply(context.Background())

			// Assert.
			if err == nil {
				t.Fatal("apply() error = nil, want phase drift rejection")
			}
			documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
		})
	}
}

func TestDocumentV2RecoveryPublicOpenMapsPlannerConflictWithoutMutation(t *testing.T) {
	t.Parallel()

	// Arrange.
	fixture := newDocumentV2PlanFixture(
		t,
		documentV2State(false, true, true, false, true, documentV2TargetStage),
	)
	documentSessionAtomicReplace(t, fixture.target, "foreign\n", 0o640)
	before := documentSessionCaptureTree(t, fixture.root)
	beforeIdentities := documentPublicationIdentities(t, fixture.root)

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), fixture.target, "")
	if session != nil {
		_ = session.Close()
	}

	// Assert.
	if session != nil || !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("OpenDocumentSessionContext() session=%#v error=%v, want exported conflict", session, err)
	}
	documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
}

func TestDocumentV2RecoveryTerminalDecisionConvergesThroughCleanup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state documentV2RecoveryState
		phase documentV2RecoveryPhase
	}{
		{name: "abort untouched pre-new", state: documentV2State(true, false, false, false, false, documentV2TargetWitness), phase: documentV2PhasePreNew},
		{name: "commit published", state: documentV2State(false, false, false, false, true, documentV2TargetStage), phase: documentV2PhasePublished},
		{name: "finish pre-new rollback", state: documentV2State(true, true, false, false, true, documentV2TargetAnchor), phase: documentV2PhasePreNewRestored},
		{name: "finish published rollback", state: documentV2State(false, true, false, true, true, documentV2TargetAnchor), phase: documentV2PhaseRestored},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(t, test.state)
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			beforeTarget := documentV2ForwardStat(t, fixture.target)
			beforeBytes := documentSessionReadFile(t, fixture.target)

			// Act.
			err = prepared.apply(context.Background())

			// Assert.
			if err != nil {
				t.Fatalf("apply() error = %v", err)
			}
			group := prepared.actions[0].group
			if group.phase != test.phase || group.v2State != test.state || !group.completed {
				t.Fatalf("terminal decision phase=%v state=%#v, want %v %#v", group.phase, group.v2State, test.phase, test.state)
			}
			documentSessionAssertNoArtifacts(t, filepath.Dir(fixture.target))
			afterTarget := documentV2ForwardStat(t, fixture.target)
			if !os.SameFile(beforeTarget, afterTarget) ||
				documentSessionReadFile(t, fixture.target) != beforeBytes {
				t.Fatal("terminal cleanup changed public target")
			}
		})
	}
}

func TestDocumentV2RecoveryRestoredCleansNormalizedPrivateAnchorClaim(t *testing.T) {
	t.Parallel()

	// Arrange a canonical Restored state, then model a crash after guarded
	// removal renamed physical A to a private claim. The private name binds
	// canonical A; target remains the exact A inode.
	fixture := newDocumentV2PlanFixtureAtContract(
		t,
		t.TempDir(),
		"docs/nested/concept.md",
		documentV2State(false, true, false, true, true, documentV2TargetAnchor),
	)
	targetBefore := documentV2ForwardStat(t, fixture.target)
	anchorBefore := documentV2ForwardStat(t, fixture.paths["anchor-0"])
	if !os.SameFile(targetBefore, anchorBefore) {
		t.Fatal("Restored fixture target is detached from canonical A")
	}
	directory := filepath.Dir(fixture.target)
	parent, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	canonicalAnchor := filepath.Base(fixture.paths["anchor-0"])
	privateAnchor, err := allocatePrivatePublicationClaim(parent, canonicalAnchor)
	if err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	if err := parent.Rename(canonicalAnchor, privateAnchor); err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	privateAnchorPath := filepath.Join(directory, privateAnchor)
	privateBefore := documentV2ForwardStat(t, privateAnchorPath)
	if !os.SameFile(targetBefore, privateBefore) {
		t.Fatal("normalized private A claim detached from public target")
	}

	// Prove shared discovery normalized the physical private name while
	// retaining canonical A as the logical protocol name.
	root, err := os.OpenRoot(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := discoverPublicationNamespaces(
		context.Background(),
		root,
		[]publicationNamespaceRule{
			documentNamespaceRule(),
			privatePublicationNamespaceRule(),
		},
	)
	if err != nil {
		_ = root.Close()
		t.Fatal(err)
	}
	normalized, err := normalizePrivatePublicationClaims(
		context.Background(),
		root,
		snapshot,
	)
	closeErr := root.Close()
	if err := errors.Join(err, closeErr); err != nil {
		t.Fatal(err)
	}
	anchorKind := documentRollbackAnchorProtocolKind(0)
	var normalizedAnchor *publicationNamespaceDiscovery
	for index := range normalized {
		discovery := &normalized[index]
		if discovery.name == privateAnchor {
			normalizedAnchor = discovery
			break
		}
	}
	if normalizedAnchor == nil ||
		normalizedAnchor.namespace != "document-v2" ||
		normalizedAnchor.kind != anchorKind ||
		normalizedAnchor.protocolName != canonicalAnchor {
		t.Fatalf(
			"normalized A = %#v, want document-v2 %q physical %q protocol %q",
			normalizedAnchor,
			anchorKind,
			privateAnchor,
			canonicalAnchor,
		)
	}

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), fixture.target, "")
	if err != nil {
		t.Fatalf("OpenDocumentSessionContext() error = %v", err)
	}
	if session == nil {
		t.Fatal("OpenDocumentSessionContext() session = nil")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	// Assert physical-as-canonical cleanup preserved target and removed every
	// document/private residue, including any accidental nested private name.
	targetAfter := documentV2ForwardStat(t, fixture.target)
	if !os.SameFile(targetBefore, targetAfter) ||
		documentSessionReadFile(t, fixture.target) != fixture.original {
		t.Fatal("normalized private A cleanup changed restored target")
	}
	if _, statErr := os.Lstat(privateAnchorPath); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("physical private A remains: %v", statErr)
	}
	if _, statErr := os.Lstat(fixture.paths["anchor-0"]); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("canonical A unexpectedly remains: %v", statErr)
	}
	err = filepath.WalkDir(fixture.root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(fixture.root, filename)
		if err != nil {
			return err
		}
		if isReservedTransactionPath(filepath.ToSlash(relative)) {
			t.Fatalf("recovery retained reserved/private residue %q", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDocumentV2RecoveryPreNewRestoredCrashAfterManifestRemovalRetainsProofs(t *testing.T) {
	t.Parallel()

	// Arrange and Act.
	root := t.TempDir()
	runDocumentV2TerminalDecisionCrashHelper(t, root)
	target := filepath.Join(root, "docs", "concept.md")

	// Assert.
	artifacts := documentPrepareCrashArtifacts(t, filepath.Dir(target))
	if artifacts["manifest"] != "" {
		t.Fatalf("post-M crash retained M: %#v", artifacts)
	}
	for _, kind := range []string{
		"witness",
		"backup",
		"stage",
		"new-install",
		"claim",
		"anchor-0",
	} {
		if artifacts[kind] == "" {
			t.Fatalf("post-M crash lost %s: %#v", kind, artifacts)
		}
	}
	if artifacts["restore-install"] != "" || artifacts["discard"] != "" {
		t.Fatalf("post-M crash retained R or created D: %#v", artifacts)
	}
	targetInfo := documentV2ForwardStat(t, target)
	anchorInfo := documentV2ForwardStat(t, artifacts["anchor-0"])
	stageInfo := documentV2ForwardStat(t, artifacts["stage"])
	newInfo := documentV2ForwardStat(t, artifacts["new-install"])
	if !os.SameFile(targetInfo, anchorInfo) ||
		!os.SameFile(stageInfo, newInfo) {
		t.Fatal("post-M crash lost target~A or N~S proofs")
	}
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if err != nil || session == nil {
		t.Fatalf("post-M restart session=%#v error=%v", session, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	targetAfter := documentV2ForwardStat(t, target)
	if !os.SameFile(targetInfo, targetAfter) ||
		documentSessionReadFile(t, target) != documentSessionConcept("concept", "before") {
		t.Fatal("post-M cleanup changed restored public target")
	}
	documentSessionAssertNoArtifacts(t, filepath.Dir(target))
}

func TestDocumentV2RecoveryRestoredCrashAfterManifestRemovalRetainsProofs(t *testing.T) {
	t.Parallel()

	// Arrange and Act.
	root := t.TempDir()
	runDocumentV2RestoredTerminalDecisionCrashHelper(t, root)
	target := filepath.Join(root, "docs", "concept.md")

	// Assert.
	artifacts := documentPrepareCrashArtifacts(t, filepath.Dir(target))
	if artifacts["manifest"] != "" ||
		artifacts["new-install"] != "" ||
		artifacts["restore-install"] != "" {
		t.Fatalf("restored post-M crash retained M/N/R: %#v", artifacts)
	}
	for _, kind := range []string{
		"witness",
		"backup",
		"stage",
		"claim",
		"anchor-0",
		"discard",
	} {
		if artifacts[kind] == "" {
			t.Fatalf("restored post-M crash lost %s: %#v", kind, artifacts)
		}
	}
	targetInfo := documentV2ForwardStat(t, target)
	anchorInfo := documentV2ForwardStat(t, artifacts["anchor-0"])
	stageInfo := documentV2ForwardStat(t, artifacts["stage"])
	discardInfo := documentV2ForwardStat(t, artifacts["discard"])
	if !os.SameFile(targetInfo, anchorInfo) ||
		!os.SameFile(stageInfo, discardInfo) {
		t.Fatal("restored post-M crash lost target~A or D~S proofs")
	}
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if err != nil || session == nil {
		t.Fatalf("restored post-M restart session=%#v error=%v", session, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	targetAfter := documentV2ForwardStat(t, target)
	if !os.SameFile(targetInfo, targetAfter) ||
		documentSessionReadFile(t, target) != documentSessionConcept("concept", "before") {
		t.Fatal("restored post-M cleanup changed public target")
	}
	documentSessionAssertNoArtifacts(t, filepath.Dir(target))
}

func TestDocumentV2RecoveryNoManifestCommitCleansWithoutMutatingTarget(t *testing.T) {
	t.Parallel()

	// Arrange a full committed no-M terminal form.
	fixture := newDocumentV2PlanFixture(
		t,
		documentV2State(false, false, false, false, true, documentV2TargetStage),
	)
	documentV2RemoveManifestForTest(t, fixture)
	artifacts := documentPrepareCrashArtifacts(t, filepath.Dir(fixture.target))
	if artifacts["manifest"] != "" {
		t.Fatalf("commit vertical retained M: %#v", artifacts)
	}
	for _, kind := range []string{"witness", "backup", "stage", "claim"} {
		if artifacts[kind] == "" {
			t.Fatalf("commit vertical lacks %s: %#v", kind, artifacts)
		}
	}
	targetBefore := documentV2ForwardStat(t, fixture.target)
	var cleanupOrder []string

	// Act.
	session, err := openDocumentSessionContext(
		context.Background(),
		fixture.target,
		"",
		documentSessionHooks{publish: documentPublishHooks{
			beforeCleanup: func(_ *os.Root, kind, _ string) error {
				current := documentV2ForwardStat(t, fixture.target)
				if !os.SameFile(targetBefore, current) {
					t.Fatalf("target changed before %s cleanup", kind)
				}
				return nil
			},
			afterCleanup: func(parent *os.Root, kind, name string) error {
				if _, statErr := parent.Lstat(name); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("%s remains after cleanup: %v", kind, statErr)
				}
				current := documentV2ForwardStat(t, fixture.target)
				if !os.SameFile(targetBefore, current) {
					t.Fatalf("target changed after %s cleanup", kind)
				}
				cleanupOrder = append(cleanupOrder, kind)
				return nil
			},
		}},
	)

	// Assert.
	if err != nil {
		t.Fatalf("OpenDocumentSessionContext() error = %v", err)
	}
	if session == nil {
		t.Fatal("OpenDocumentSessionContext() session = nil")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"claim", "backup", "witness", "stage"}
	if !reflect.DeepEqual(cleanupOrder, wantOrder) {
		t.Fatalf("cleanup order = %#v, want %#v", cleanupOrder, wantOrder)
	}
	targetAfter := documentV2ForwardStat(t, fixture.target)
	if !os.SameFile(targetBefore, targetAfter) ||
		documentSessionReadFile(t, fixture.target) != fixture.published {
		t.Fatal("no-M commit cleanup mutated public target")
	}
	documentSessionAssertNoArtifacts(t, filepath.Dir(fixture.target))
}

func TestDocumentV2RecoveryNoManifestCleanupPreservesValuesAfterCancellation(t *testing.T) {
	// Arrange.
	type contextKey struct{}
	const wantValue = "restart-terminal-value"
	fixture := newDocumentV2PlanFixture(
		t,
		documentV2State(false, false, false, false, true, documentV2TargetStage),
	)
	documentV2RemoveManifestForTest(t, fixture)
	prepared, err := prepareDocumentV2PlanFixture(t, fixture)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(prepared.close)
	if len(prepared.groups) != 1 || !prepared.groups[0].terminalOnly {
		t.Fatal("fixture is not a terminal-only restart group")
	}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, wantValue))
	observed := false
	prepared.hooks.beforeCleanup = func(*os.Root, string, string) error {
		transitionCtx := prepared.groups[0].transitionCtx
		if transitionCtx == nil || transitionCtx.Err() != nil || transitionCtx.Value(contextKey{}) != wantValue {
			return errors.New("terminal cleanup lost value-preserving cancellation suppression")
		}
		observed = true
		return nil
	}
	cancel()

	// Act.
	err = prepared.apply(ctx)

	// Assert.
	if err != nil || !observed {
		t.Fatalf("apply error=%v observed=%t", err, observed)
	}
	if got := documentSessionReadFile(t, fixture.target); got != fixture.published {
		t.Fatalf("target bytes = %q", got)
	}
	documentSessionAssertNoArtifacts(t, fixture.root)
}

func TestDocumentV2RecoveryMixedTerminalAndPreNewHonorPreCancellation(t *testing.T) {
	t.Parallel()

	// Arrange a terminal-only group followed by a manifest-backed PreNew group.
	root := t.TempDir()
	terminal := newDocumentV2PlanFixtureAtContract(
		t,
		root,
		"a-terminal/concept.md",
		documentV2State(false, false, false, false, true, documentV2TargetStage),
	)
	pending := newDocumentV2PlanFixtureAtContract(
		t,
		root,
		"z-pending/concept.md",
		documentV2State(true, false, false, false, false, documentV2TargetWitness),
	)
	documentV2RemoveManifestForTest(t, terminal)
	prepared, err := prepareDocumentV2PlanFixture(t, terminal)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	before := documentSessionCaptureTree(t, root)
	beforeIdentities := documentPublicationIdentities(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	err = prepared.apply(ctx)

	// Assert cancellation remains pre-mutation because the nonterminal group
	// has no receipt that could authorize convergence.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("apply() error = %v, want context.Canceled", err)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, documentSessionCaptureTree(t, root))
	documentAssertPublicationIdentities(t, root, beforeIdentities)
	if got := documentSessionReadFile(t, pending.target); got != pending.original {
		t.Fatalf("pending target = %q, want %q", got, pending.original)
	}
}

func TestDocumentV2RecoveryMixedTerminalCancellationPreservesValuesWithoutPromotingContext(t *testing.T) {
	// Arrange a terminal-only group followed by a PreNew group. Cancellation
	// occurs inside terminal cleanup, whose local context must retain values.
	type contextKey struct{}
	const wantValue = "mixed-terminal-value"
	root := t.TempDir()
	terminal := newDocumentV2PlanFixtureAtContract(
		t,
		root,
		"a-terminal/concept.md",
		documentV2State(false, false, false, false, true, documentV2TargetStage),
	)
	pending := newDocumentV2PlanFixtureAtContract(
		t,
		root,
		"z-pending/concept.md",
		documentV2State(true, false, false, false, false, documentV2TargetWitness),
	)
	documentV2RemoveManifestForTest(t, terminal)
	prepared, err := prepareDocumentV2PlanFixture(t, terminal)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	if len(prepared.groups) != 2 || !prepared.groups[0].terminalOnly || prepared.groups[1].terminalOnly {
		t.Fatalf("mixed groups = %#v", prepared.groups)
	}
	pendingBefore := documentSessionCaptureTree(t, filepath.Dir(pending.target))
	pendingIdentities := documentPublicationIdentities(t, filepath.Dir(pending.target))
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, wantValue))
	observedValue := false
	prepared.hooks.beforeCleanup = func(*os.Root, string, string) error {
		transitionCtx := prepared.groups[0].transitionCtx
		if transitionCtx == nil || transitionCtx.Err() != nil || transitionCtx.Value(contextKey{}) != wantValue {
			return errors.New("terminal cleanup lost value-preserving cancellation suppression")
		}
		observedValue = true
		cancel()
		return nil
	}

	// Act.
	err = prepared.apply(ctx)

	// Assert the terminal group converged, while cancellation still stopped
	// the unrelated PreNew group because no canonical receipt was produced.
	if !errors.Is(err, context.Canceled) || !observedValue {
		t.Fatalf("apply() error=%v observedValue=%t", err, observedValue)
	}
	if got := documentSessionReadFile(t, terminal.target); got != terminal.published {
		t.Fatalf("terminal target = %q, want %q", got, terminal.published)
	}
	documentSessionAssertNoArtifacts(t, filepath.Dir(terminal.target))
	documentSessionAssertTreeSnapshotEqual(
		t,
		pendingBefore,
		documentSessionCaptureTree(t, filepath.Dir(pending.target)),
	)
	documentAssertPublicationIdentities(t, filepath.Dir(pending.target), pendingIdentities)
}

func TestDocumentV2RecoveryMultiGroupConvergesAfterReceiptDespiteCancellation(t *testing.T) {
	// Arrange a rollback that will produce a canonical restore receipt before
	// a terminal-only group, with both groups sharing one recovery inventory.
	type contextKey struct{}
	const wantValue = "post-receipt-value"
	root := t.TempDir()
	receipted := newDocumentV2PlanFixtureAtContract(
		t,
		root,
		"a-receipted/concept.md",
		documentV2State(true, true, true, false, true, documentV2TargetMissing),
	)
	terminal := newDocumentV2PlanFixtureAtContract(
		t,
		root,
		"z-terminal/concept.md",
		documentV2State(false, false, false, false, true, documentV2TargetStage),
	)
	documentV2RemoveManifestForTest(t, terminal)
	prepared, err := prepareDocumentV2PlanFixture(t, receipted)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	if len(prepared.groups) != 2 || prepared.groups[0].terminalOnly || !prepared.groups[1].terminalOnly {
		t.Fatalf("mixed groups = %#v", prepared.groups)
	}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, wantValue))
	hookErr := errors.New("post-receipt hook failure")
	receiptObserved := false
	terminalObserved := false
	prepared.hooks.afterRestoreConsumed = func(*os.Root, string) error {
		receiptObserved = prepared.groups[0].canonicalReceipt
		cancel()
		return hookErr
	}
	prepared.hooks.beforeCleanup = func(*os.Root, string, string) error {
		transitionCtx := prepared.groups[1].transitionCtx
		if transitionCtx == nil {
			return nil
		}
		if transitionCtx.Err() != nil || transitionCtx.Value(contextKey{}) != wantValue {
			return errors.New("post-receipt convergence lost cancellation suppression or context value")
		}
		terminalObserved = true
		return nil
	}

	// Act.
	err = prepared.apply(ctx)

	// Assert the receipt converts cancellation into mandatory convergence for
	// the receipted group and all later groups in the same prepared plan.
	if !errors.Is(err, hookErr) || !receiptObserved || !terminalObserved {
		t.Fatalf(
			"apply() error=%v receiptObserved=%t terminalObserved=%t",
			err,
			receiptObserved,
			terminalObserved,
		)
	}
	if got := documentSessionReadFile(t, receipted.target); got != receipted.original {
		t.Fatalf("receipted target = %q, want restored %q", got, receipted.original)
	}
	if got := documentSessionReadFile(t, terminal.target); got != terminal.published {
		t.Fatalf("terminal target = %q, want %q", got, terminal.published)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestDocumentV2RecoveryPostRenameFaultsConvergeBeforeDurableReceipt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		state         documentV2RecoveryState
		hooks         func(error) documentPublishHooks
		wantConverged bool
	}{
		{
			name:  "consume restore before directory sync",
			state: documentV2State(true, true, true, false, true, documentV2TargetMissing),
			hooks: func(fault error) documentPublishHooks {
				return documentPublishHooks{
					afterInstall: func(*os.Root, string) error { return fault },
				}
			},
			wantConverged: true,
		},
		{
			name:  "vacate published before directory sync",
			state: documentV2State(false, true, true, false, true, documentV2TargetStage),
			hooks: func(fault error) documentPublishHooks {
				return documentPublishHooks{
					afterVacateRename: func(*os.Root, string, string) error { return fault },
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange a fault after the public rename but before its directory
			// sync. The mutation mandates local convergence but is not yet a
			// cross-group durable receipt.
			fixture := newDocumentV2PlanFixture(t, test.state)
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			fault := errors.New("post-rename pre-sync fault")
			prepared.hooks = test.hooks(fault)

			// Act.
			err = prepared.apply(context.Background())

			// Assert the original cause is retained. Consume is an uncompensated
			// canonical mutation and must converge; vacate is compensated by the
			// generic primitive before its receipt and remains retryable.
			if !errors.Is(err, fault) {
				t.Fatalf("apply() error = %v, want %v", err, fault)
			}
			if prepared.groups[0].canonicalReceipt != test.wantConverged {
				t.Fatalf(
					"canonical receipt=%t, want %t: error=%v phase=%v",
					prepared.groups[0].canonicalReceipt,
					test.wantConverged,
					err,
					prepared.groups[0].phase,
				)
			}
			if !test.wantConverged {
				if got := documentSessionReadFile(t, fixture.target); got != fixture.published {
					t.Fatalf("compensated target = %q, want %q", got, fixture.published)
				}
				prepared.hooks = documentPublishHooks{}
				if retryErr := prepared.apply(context.Background()); retryErr != nil {
					t.Fatalf("retry after compensated vacate error = %v", retryErr)
				}
			}
			if got := documentSessionReadFile(t, fixture.target); got != fixture.original {
				t.Fatalf("target = %q, want restored %q", got, fixture.original)
			}
			documentSessionAssertNoArtifacts(t, fixture.root)
		})
	}
}

func TestDocumentV2RecoveryLocalMutationWithoutReceiptDoesNotPromoteLaterGroup(t *testing.T) {
	// Arrange a consumed restore rename followed by a failure at the terminal
	// directory sync. The first group must converge locally, but it has no
	// durable authority to run the later PreNew group.
	root := t.TempDir()
	local := newDocumentV2PlanFixtureAtContract(
		t,
		root,
		"a-local/concept.md",
		documentV2State(true, true, true, false, true, documentV2TargetMissing),
	)
	pending := newDocumentV2PlanFixtureAtContract(
		t,
		root,
		"z-pending/concept.md",
		documentV2State(true, false, false, false, false, documentV2TargetWitness),
	)
	prepared, err := prepareDocumentV2PlanFixture(t, local)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	pendingBefore := documentSessionCaptureTree(t, filepath.Dir(pending.target))
	pendingIdentities := documentPublicationIdentities(t, filepath.Dir(pending.target))
	mutationErr := errors.New("post-consume rename fault")
	syncErr := errors.New("terminal parent sync fault")
	prepared.hooks.afterInstall = func(*os.Root, string) error { return mutationErr }
	prepared.hooks.directorySync = func(*os.Root) error { return syncErr }

	// Act.
	err = prepared.apply(context.Background())

	// Assert local canonical convergence restored the target, but the failed
	// parent sync produced no cross-group receipt and the later group is exact.
	if !errors.Is(err, mutationErr) || !errors.Is(err, syncErr) {
		t.Fatalf("apply() error = %v, want mutation and sync causes", err)
	}
	if prepared.groups[0].canonicalReceipt {
		t.Fatal("pre-sync local convergence was promoted to a durable receipt")
	}
	if got := documentSessionReadFile(t, local.target); got != local.original {
		t.Fatalf("local target = %q, want restored %q", got, local.original)
	}
	documentSessionAssertTreeSnapshotEqual(
		t,
		pendingBefore,
		documentSessionCaptureTree(t, filepath.Dir(pending.target)),
	)
	documentAssertPublicationIdentities(t, filepath.Dir(pending.target), pendingIdentities)
}

func TestDocumentV2RecoveryZeroPlanPreservesCancellationAndValues(t *testing.T) {
	t.Parallel()

	// Arrange.
	type contextKey struct{}
	const wantValue = "zero-plan-value"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, wantValue))
	cancel()
	prepared := &preparedDocumentRecovery{}

	// Act.
	planCtx := prepared.planContext(ctx)

	// Assert an empty plan cannot manufacture convergence authority.
	if !errors.Is(planCtx.Err(), context.Canceled) || planCtx.Value(contextKey{}) != wantValue {
		t.Fatalf("plan context error=%v value=%v", planCtx.Err(), planCtx.Value(contextKey{}))
	}
}

func TestDocumentV2RecoveryNoManifestOtherTerminalFormsCleanWithoutMutatingTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		state         documentV2RecoveryState
		initialKinds  []string
		cleanupOrder  []string
		expectedBytes string
	}{
		{
			name:          "untouched",
			state:         documentV2State(true, false, false, false, false, documentV2TargetWitness),
			initialKinds:  []string{"backup", "new-install", "stage", "witness"},
			cleanupOrder:  []string{"new-install", "stage", "backup", "witness"},
			expectedBytes: documentSessionConcept("concept", "before"),
		},
		{
			name:          "pre-new restored",
			state:         documentV2State(true, true, false, false, true, documentV2TargetAnchor),
			initialKinds:  []string{"anchor-0", "backup", "claim", "new-install", "stage", "witness"},
			cleanupOrder:  []string{"new-install", "claim", "backup", "witness", "stage", "anchor-0"},
			expectedBytes: documentSessionConcept("concept", "before"),
		},
		{
			name:          "published restored",
			state:         documentV2State(false, true, false, true, true, documentV2TargetAnchor),
			initialKinds:  []string{"anchor-0", "backup", "claim", "discard", "stage", "witness"},
			cleanupOrder:  []string{"discard", "claim", "backup", "witness", "stage", "anchor-0"},
			expectedBytes: documentSessionConcept("concept", "before"),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange a full no-M terminal form.
			fixture := newDocumentV2PlanFixture(t, test.state)
			documentV2RemoveManifestForTest(t, fixture)
			artifacts := documentPrepareCrashArtifacts(t, filepath.Dir(fixture.target))
			if got := documentPrepareCrashArtifactKinds(artifacts); !reflect.DeepEqual(got, test.initialKinds) {
				t.Fatalf("initial no-M kinds = %#v, want %#v", got, test.initialKinds)
			}
			targetBefore := documentV2ForwardStat(t, fixture.target)
			var cleanupOrder []string

			// Act.
			session, err := openDocumentSessionContext(
				context.Background(),
				fixture.target,
				"",
				documentSessionHooks{publish: documentPublishHooks{
					beforeCleanup: func(_ *os.Root, kind, _ string) error {
						current := documentV2ForwardStat(t, fixture.target)
						if !os.SameFile(targetBefore, current) {
							t.Fatalf("target changed before %s cleanup", kind)
						}
						return nil
					},
					afterCleanup: func(parent *os.Root, kind, name string) error {
						if _, statErr := parent.Lstat(name); !errors.Is(statErr, fs.ErrNotExist) {
							t.Fatalf("%s remains after cleanup: %v", kind, statErr)
						}
						current := documentV2ForwardStat(t, fixture.target)
						if !os.SameFile(targetBefore, current) {
							t.Fatalf("target changed after %s cleanup", kind)
						}
						cleanupOrder = append(cleanupOrder, kind)
						return nil
					},
				}},
			)

			// Assert.
			if err != nil {
				t.Fatalf("OpenDocumentSessionContext() error = %v", err)
			}
			if session == nil {
				t.Fatal("OpenDocumentSessionContext() session = nil")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cleanupOrder, test.cleanupOrder) {
				t.Fatalf("cleanup order = %#v, want %#v", cleanupOrder, test.cleanupOrder)
			}
			targetAfter := documentV2ForwardStat(t, fixture.target)
			if !os.SameFile(targetBefore, targetAfter) ||
				documentSessionReadFile(t, fixture.target) != test.expectedBytes {
				t.Fatal("no-M terminal cleanup mutated public target")
			}
			documentSessionAssertNoArtifacts(t, filepath.Dir(fixture.target))
		})
	}
}

func TestDocumentV2RecoveryNoManifestCleanupCrashPrefixesConvergeIdempotently(t *testing.T) {
	t.Parallel()

	tests := []struct {
		form          string
		order         []string
		expectedBytes string
	}{
		{
			form:          "commit",
			order:         []string{"claim", "backup", "witness", "stage"},
			expectedBytes: documentSessionConcept("concept", "after"),
		},
		{
			form:          "untouched",
			order:         []string{"new-install", "stage", "backup", "witness"},
			expectedBytes: documentSessionConcept("concept", "before"),
		},
		{
			form:          "pre-new-restored",
			order:         []string{"new-install", "claim", "backup", "witness", "stage", "anchor-0"},
			expectedBytes: documentSessionConcept("concept", "before"),
		},
		{
			form:          "published-restored",
			order:         []string{"discard", "claim", "backup", "witness", "stage", "anchor-0"},
			expectedBytes: documentSessionConcept("concept", "before"),
		},
	}
	for _, test := range tests {
		test := test
		for ordinal := range test.order {
			ordinal := ordinal
			t.Run(fmt.Sprintf("%s/%02d-%s", test.form, ordinal, test.order[ordinal]), func(t *testing.T) {
				t.Parallel()

				// Arrange and crash after one exact cleanup action.
				root := t.TempDir()
				runDocumentV2NoManifestCleanupCrashHelper(t, root, test.form, ordinal)
				target := filepath.Join(root, "docs", "concept.md")
				targetBefore := documentV2ForwardStat(t, target)
				gotKinds := documentPrepareCrashArtifactKinds(
					documentPrepareCrashArtifacts(t, filepath.Dir(target)),
				)
				wantKinds := append([]string{}, test.order[ordinal+1:]...)
				sort.Strings(wantKinds)
				if !reflect.DeepEqual(gotKinds, wantKinds) {
					t.Fatalf(
						"post-crash kinds = %#v, want reachable suffix %#v",
						gotKinds,
						wantKinds,
					)
				}

				// Act: restart from the actual crash image, then reopen again.
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if err != nil || session == nil {
					t.Fatalf("first restart session=%#v error=%v", session, err)
				}
				if err := session.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, reopenErr := OpenDocumentSessionContext(context.Background(), target, "")
				if reopenErr != nil || reopened == nil {
					t.Fatalf("idempotent reopen session=%#v error=%v", reopened, reopenErr)
				}
				if err := reopened.Close(); err != nil {
					t.Fatal(err)
				}

				// Assert.
				targetAfter := documentV2ForwardStat(t, target)
				if !os.SameFile(targetBefore, targetAfter) ||
					documentSessionReadFile(t, target) != test.expectedBytes {
					t.Fatal("crash/restart cleanup mutated public target")
				}
				documentSessionAssertNoArtifacts(t, filepath.Dir(target))
			})
		}
	}
}

func TestDocumentV2RecoveryNoManifestPlanRejectsDriftWithoutMutation(t *testing.T) {
	t.Parallel()

	forms := []struct {
		name        string
		state       documentV2RecoveryState
		nextKind    string
		nextPayload func(documentV2PlanFixture) string
	}{
		{
			name:     "commit",
			state:    documentV2State(false, false, false, false, true, documentV2TargetStage),
			nextKind: "claim",
			nextPayload: func(fixture documentV2PlanFixture) string {
				return fixture.original
			},
		},
		{
			name:     "untouched",
			state:    documentV2State(true, false, false, false, false, documentV2TargetWitness),
			nextKind: "new-install",
			nextPayload: func(fixture documentV2PlanFixture) string {
				return fixture.published
			},
		},
		{
			name:     "pre-new-restored",
			state:    documentV2State(true, true, false, false, true, documentV2TargetAnchor),
			nextKind: "new-install",
			nextPayload: func(fixture documentV2PlanFixture) string {
				return fixture.published
			},
		},
		{
			name:     "published-restored",
			state:    documentV2State(false, true, false, true, true, documentV2TargetAnchor),
			nextKind: "discard",
			nextPayload: func(fixture documentV2PlanFixture) string {
				return fixture.published
			},
		},
	}
	for _, form := range forms {
		form := form
		tests := []struct {
			name   string
			mutate func(*testing.T, documentV2PlanFixture)
		}{
			{
				name: "target",
				mutate: func(t *testing.T, fixture documentV2PlanFixture) {
					documentSessionAtomicReplace(t, fixture.target, "foreign\n", 0o640)
				},
			},
			{
				name: "parent",
				mutate: func(t *testing.T, fixture documentV2PlanFixture) {
					directory := filepath.Dir(fixture.target)
					detached := directory + ".detached"
					if err := os.Rename(directory, detached); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(directory, 0o755); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "next-owned-artifact",
				mutate: func(t *testing.T, fixture documentV2PlanFixture) {
					documentSessionAtomicReplace(
						t,
						fixture.paths[form.nextKind],
						form.nextPayload(fixture),
						0o644,
					)
				},
			},
		}
		for _, test := range tests {
			test := test
			t.Run(form.name+"/"+test.name, func(t *testing.T) {
				t.Parallel()

				// Arrange a no-M plan, then drift after planning.
				fixture := newDocumentV2PlanFixture(t, form.state)
				documentV2RemoveManifestForTest(t, fixture)
				prepared, err := prepareDocumentV2PlanFixture(t, fixture)
				if err != nil {
					t.Fatal(err)
				}
				defer prepared.close()
				test.mutate(t, fixture)
				before := documentSessionCaptureTree(t, fixture.root)
				beforeIdentities := documentPublicationIdentities(t, fixture.root)

				// Act.
				err = prepared.apply(context.Background())

				// Assert.
				if err == nil {
					t.Fatal("apply() error = nil, want no-M plan drift rejection")
				}
				documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
			})
		}
	}
}

func TestDocumentV2RecoveryNoManifestRejectsArtifactCorruptionWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*testing.T, documentV2PlanFixture)
	}{
		{
			name: "replacement",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.paths["discard"], fixture.published, 0o644)
			},
		},
		{
			name: "type",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				if err := os.Remove(fixture.paths["discard"]); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(fixture.paths["discard"], 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mode",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				if err := os.Chmod(fixture.paths["discard"], 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "content",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.paths["discard"], "foreign\n", 0o644)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(
				t,
				documentV2State(false, true, false, true, true, documentV2TargetAnchor),
			)
			documentV2RemoveManifestForTest(t, fixture)
			test.mutate(t, fixture)
			before := documentSessionCaptureTree(t, fixture.root)
			beforeIdentities := documentPublicationIdentities(t, fixture.root)

			// Act.
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if prepared != nil {
				prepared.close()
			}

			// Assert.
			if err == nil {
				t.Fatal("prepareDocumentRecovery() error = nil, want artifact corruption rejection")
			}
			documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
		})
	}
}

func TestDocumentV2RecoveryNoManifestRejectsReservedAndOrphanEvidenceWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*testing.T, documentV2PlanFixture)
	}{
		{
			name: "unknown generation",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionWriteFile(
					t,
					filepath.Join(
						filepath.Dir(fixture.target),
						".okf-document-txn-v3-manifest-"+strings.Repeat("a", 64),
					),
					"{}",
				)
			},
		},
		{
			name: "v1 generation",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionWriteFile(
					t,
					filepath.Join(
						filepath.Dir(fixture.target),
						".okf-document-txn-v1-manifest-"+strings.Repeat("a", 64),
					),
					"{}",
				)
			},
		},
		{
			name: "ambiguous orphan claim",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				if err := os.Remove(fixture.paths["claim"]); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(fixture.paths["backup"], fixture.paths["claim"]); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			fixture := newDocumentV2PlanFixture(
				t,
				documentV2State(false, false, false, false, true, documentV2TargetStage),
			)
			documentV2RemoveManifestForTest(t, fixture)
			test.mutate(t, fixture)
			before := documentSessionCaptureTree(t, fixture.root)
			beforeIdentities := documentPublicationIdentities(t, fixture.root)

			// Act.
			prepared, err := prepareDocumentV2PlanFixture(t, fixture)
			if prepared != nil {
				prepared.close()
			}

			// Assert.
			if err == nil {
				t.Fatal("prepareDocumentRecovery() error = nil, want reserved/orphan rejection")
			}
			documentV2AssertFixtureUnchanged(t, fixture, before, beforeIdentities)
		})
	}
}

func TestDocumentV2RecoveryNoManifestRejectsTwoGroupCrossDriftWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*testing.T, documentV2PlanFixture)
	}{
		{
			name: "cross-target",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(t, fixture.target, "foreign\n", 0o640)
			},
		},
		{
			name: "cross-artifact",
			mutate: func(t *testing.T, fixture documentV2PlanFixture) {
				documentSessionAtomicReplace(
					t,
					fixture.paths["backup"],
					fixture.original,
					0o644,
				)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange two full committed groups and remove both manifests.
			root := t.TempDir()
			first := newDocumentV2PlanFixtureAtContract(
				t,
				root,
				"docs/a.md",
				documentV2State(false, false, false, false, true, documentV2TargetStage),
			)
			second := newDocumentV2PlanFixtureAtContract(
				t,
				root,
				"docs/b.md",
				documentV2State(false, false, false, false, true, documentV2TargetStage),
			)
			documentV2RemoveManifestForTest(t, first)
			documentV2RemoveManifestForTest(t, second)
			prepared, err := prepareDocumentV2PlanFixture(t, first)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			if len(prepared.actions) != 2 {
				t.Fatalf("terminal actions = %d, want 2", len(prepared.actions))
			}
			test.mutate(t, second)
			before := documentSessionCaptureTree(t, root)
			beforeIdentities := documentPublicationIdentities(t, root)

			// Act.
			err = prepared.apply(context.Background())

			// Assert: every group is revalidated before the first cleanup.
			if err == nil {
				t.Fatal("apply() error = nil, want two-group cross-drift rejection")
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				before,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, beforeIdentities)
		})
	}
}

func newDocumentV2PlanFixture(
	t *testing.T,
	state documentV2RecoveryState,
) documentV2PlanFixture {
	t.Helper()
	return newDocumentV2PlanFixtureAt(t, t.TempDir(), state)
}

func documentV2RemoveManifestForTest(
	t *testing.T,
	fixture documentV2PlanFixture,
) {
	t.Helper()
	if err := os.Remove(fixture.paths["manifest"]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(fixture.paths["manifest"]); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("manifest removal error = %v, want missing", err)
	}
}

func newDocumentV2PlanFixtureAt(
	t *testing.T,
	root string,
	state documentV2RecoveryState,
) documentV2PlanFixture {
	t.Helper()
	return newDocumentV2PlanFixtureAtContract(
		t,
		root,
		"docs/concept.md",
		state,
	)
}

func newDocumentV2PlanFixtureAtContract(
	t *testing.T,
	root string,
	targetContract string,
	state documentV2RecoveryState,
) documentV2PlanFixture {
	t.Helper()
	indexPath := filepath.Join(root, indexFilename)
	if _, err := os.Lstat(indexPath); errors.Is(err, fs.ErrNotExist) {
		documentSessionWriteFile(
			t,
			indexPath,
			documentSessionIndex("0.2", "root"),
		)
	} else if err != nil {
		t.Fatal(err)
	}
	targetDirectory := path.Dir(targetContract)
	if targetDirectory == "." {
		targetDirectory = ""
	}
	directory := filepath.Join(root, filepath.FromSlash(targetDirectory))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	conceptID := strings.TrimSuffix(path.Base(targetContract), path.Ext(targetContract))
	original := documentSessionConcept(conceptID, "before")
	published := documentSessionConcept(conceptID, "after")
	mode := fs.FileMode(0o644)
	manifest := documentTransactionManifest{
		Format:          documentTransactionFormat,
		Target:          targetContract,
		Mode:            uint32(mode),
		OriginalSize:    uint64(len(original)),
		OriginalSHA256:  documentDigest([]byte(original)),
		PublishedSize:   uint64(len(published)),
		PublishedSHA256: documentDigest([]byte(published)),
	}
	manifest.Stage = pathJoin(targetDirectory, documentBoundArtifactName("stage", targetContract, mode, []byte(published)))
	manifest.NewInstall = pathJoin(targetDirectory, documentBoundArtifactName("new-install", targetContract, mode, []byte(published)))
	manifest.Anchor = pathJoin(targetDirectory, documentRollbackAnchorName(targetContract, mode, []byte(original), 0))
	manifest.RestoreInstall = pathJoin(targetDirectory, documentBoundArtifactName("restore-install", targetContract, mode, []byte(original)))
	manifest.Discard = pathJoin(targetDirectory, documentBoundArtifactName("discard", targetContract, mode, []byte(published)))
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	id := documentManifestDigest(manifestData)
	paths := map[string]string{
		"manifest":        filepath.Join(directory, documentArtifactName("manifest", id)),
		"witness":         filepath.Join(directory, documentOriginalWitnessName(targetContract, mode, []byte(original))),
		"backup":          filepath.Join(directory, documentBoundArtifactName("backup", targetContract, mode, []byte(original))),
		"stage":           filepath.Join(root, filepath.FromSlash(manifest.Stage)),
		"new-install":     filepath.Join(root, filepath.FromSlash(manifest.NewInstall)),
		"anchor-0":        filepath.Join(root, filepath.FromSlash(manifest.Anchor)),
		"restore-install": filepath.Join(root, filepath.FromSlash(manifest.RestoreInstall)),
		"discard":         filepath.Join(root, filepath.FromSlash(manifest.Discard)),
		"claim":           filepath.Join(directory, documentArtifactName("claim", id)),
	}
	documentSessionWriteFile(t, paths["manifest"], string(manifestData))
	if err := os.Chmod(paths["manifest"], 0o600); err != nil {
		t.Fatal(err)
	}
	documentSessionWriteFile(t, paths["witness"], original)
	documentSessionWriteFile(t, paths["backup"], original)
	documentSessionWriteFile(t, paths["stage"], published)
	link := func(source, target string) {
		t.Helper()
		if err := os.Link(source, target); err != nil {
			t.Fatal(err)
		}
	}
	if state.newInstall {
		link(paths["stage"], paths["new-install"])
	}
	if state.anchor {
		documentSessionWriteFile(t, paths["anchor-0"], original)
	}
	if state.restoreInstall {
		link(paths["anchor-0"], paths["restore-install"])
	}
	if state.discard {
		link(paths["stage"], paths["discard"])
	}
	if state.claim {
		link(paths["witness"], paths["claim"])
	}
	target := filepath.Join(root, filepath.FromSlash(targetContract))
	switch state.target {
	case documentV2TargetWitness:
		link(paths["witness"], target)
	case documentV2TargetStage:
		link(paths["stage"], target)
	case documentV2TargetAnchor:
		link(paths["anchor-0"], target)
	case documentV2TargetMissing:
	default:
		documentSessionWriteFile(t, target, "foreign\n")
	}
	return documentV2PlanFixture{
		root: root, target: target, paths: paths,
		original: original, published: published,
	}
}

func prepareDocumentV2PlanFixture(
	t *testing.T,
	fixture documentV2PlanFixture,
) (*preparedDocumentRecovery, error) {
	t.Helper()
	root, err := os.OpenRoot(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	snapshot, err := discoverPublicationNamespaces(
		context.Background(),
		root,
		[]publicationNamespaceRule{documentNamespaceRule()},
	)
	if err != nil {
		return nil, err
	}
	return prepareDocumentRecovery(
		context.Background(),
		root,
		snapshot,
		documentPublishHooks{},
	)
}

func documentV2AssertFixtureUnchanged(
	t *testing.T,
	fixture documentV2PlanFixture,
	before map[string]documentSessionTreeEntry,
	beforeIdentities map[string]os.FileInfo,
) {
	t.Helper()
	documentSessionAssertTreeSnapshotEqual(
		t,
		before,
		documentSessionCaptureTree(t, fixture.root),
	)
	documentAssertPublicationIdentities(t, fixture.root, beforeIdentities)
}

func runDocumentV2RecoveryPreNewCrashHelper(
	t *testing.T,
	root string,
	point string,
) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestDocumentV2RecoveryPreNewCrashSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_DOCUMENT_V2_RECOVERY_PRE_NEW_ROOT="+root,
		"OKF_DOCUMENT_V2_RECOVERY_PRE_NEW_POINT="+point,
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err == nil ||
		command.ProcessState == nil ||
		command.ProcessState.ExitCode() != 90 {
		t.Fatalf(
			"pre-new recovery helper exit=%v error=%v at %s, want 90\n%s",
			command.ProcessState,
			err,
			point,
			output.String(),
		)
	}
}

func runDocumentV2TerminalDecisionCrashHelper(t *testing.T, root string) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestDocumentV2RecoveryTerminalDecisionCrashSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_DOCUMENT_V2_TERMINAL_ROOT="+root,
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err == nil ||
		command.ProcessState == nil ||
		command.ProcessState.ExitCode() != 91 {
		t.Fatalf(
			"terminal decision helper exit=%v error=%v, want 91\n%s",
			command.ProcessState,
			err,
			output.String(),
		)
	}
}

func runDocumentV2RestoredTerminalDecisionCrashHelper(t *testing.T, root string) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestDocumentV2RecoveryTerminalDecisionCrashSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_DOCUMENT_V2_TERMINAL_ROOT="+root,
		"OKF_DOCUMENT_V2_TERMINAL_STATE=restored",
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err == nil ||
		command.ProcessState == nil ||
		command.ProcessState.ExitCode() != 91 {
		t.Fatalf(
			"restored terminal decision helper exit=%v error=%v, want 91\n%s",
			command.ProcessState,
			err,
			output.String(),
		)
	}
}

func runDocumentV2PublishedRollbackCrashHelper(
	t *testing.T,
	root string,
	point string,
) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestDocumentV2RecoveryPublishedRollbackCrashSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_DOCUMENT_V2_PUBLISHED_ROLLBACK_ROOT="+root,
		"OKF_DOCUMENT_V2_PUBLISHED_ROLLBACK_POINT="+point,
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err == nil ||
		command.ProcessState == nil ||
		command.ProcessState.ExitCode() != 92 {
		t.Fatalf(
			"published rollback helper exit=%v error=%v at %s, want 92\n%s",
			command.ProcessState,
			err,
			point,
			output.String(),
		)
	}
}

func runDocumentV2NoManifestCleanupCrashHelper(
	t *testing.T,
	root string,
	form string,
	ordinal int,
) {
	t.Helper()
	var output bytes.Buffer
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestDocumentV2RecoveryNoManifestCleanupCrashSubprocessHelper$",
	)
	command.Env = append(
		os.Environ(),
		"OKF_DOCUMENT_V2_NO_MANIFEST_CLEANUP_ROOT="+root,
		"OKF_DOCUMENT_V2_NO_MANIFEST_CLEANUP_FORM="+form,
		"OKF_DOCUMENT_V2_NO_MANIFEST_CLEANUP_ORDINAL="+strconv.Itoa(ordinal),
	)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err == nil ||
		command.ProcessState == nil ||
		command.ProcessState.ExitCode() != 93 {
		t.Fatalf(
			"no-M cleanup helper exit=%v error=%v form=%s ordinal=%d, want 93\n%s",
			command.ProcessState,
			err,
			form,
			ordinal,
			output.String(),
		)
	}
}
