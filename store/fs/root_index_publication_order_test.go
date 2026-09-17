package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/internal/receiptprojection"
	"github.com/skosovsky/okf/store"
)

func TestRootIndexIsAppliedLastWithoutChangingCanonicalEvidenceOrder(t *testing.T) {
	for _, transition := range allRootIndexTransitions() {
		t.Run(string(transition), func(t *testing.T) {
			// Arrange.
			_, s, before, after, receipt := rootIndexPublicationFixtureForTransition(t, transition)
			var durable journal
			var applied []string
			visible := cloneVisibleFiles(before)
			applicationStarted := false
			journalRenamed := false
			s.config.Fault = func(step Step) error {
				if step != StepJournalDirectorySync || !journalRenamed || applicationStarted {
					return nil
				}
				journalName, err := journalPath(receipt.RequestDigest)
				if err != nil {
					return err
				}
				raw, err := readMetadataLimit(context.Background(), s.rootFD, journalName, maxJournalManifestRead)
				if err != nil {
					return err
				}
				durable, err = decodeJournalWithAlgorithm(raw, s.config.HashAlgorithm)
				return err
			}
			s.config.PostFault = func(step Step) error {
				if step == StepJournalRename {
					journalRenamed = true
				}
				if step == StepJournalDirectorySync && !applicationStarted {
					applicationStarted = true
					return nil
				}
				if !applicationStarted || step != StepRename && step != StepRemove {
					return nil
				}
				current, err := readVisibleRoot(context.Background(), s.rootFD)
				if err != nil {
					return err
				}
				changed := changedVisiblePath(visible, current)
				if changed != "" {
					applied = append(applied, changed)
				}
				visible = current
				return nil
			}

			// Act.
			err := s.publish(context.Background(), after, receipt)

			// Assert.
			if err != nil {
				t.Fatalf("publish() error = %v", err)
			}
			wantApplied := rootIndexExpectedApplicationOrder(transition)
			if !reflect.DeepEqual(applied, wantApplied) {
				t.Fatalf("physical application order = %v, want %v", applied, wantApplied)
			}
			gotJournalPaths := make([]string, len(durable.Files))
			for i, file := range durable.Files {
				gotJournalPaths[i] = file.Path
			}
			wantJournalPaths := sortedFilePaths(after.files)
			if !reflect.DeepEqual(gotJournalPaths, wantJournalPaths) {
				t.Fatalf("journal Files order = %v, want canonical %v", gotJournalPaths, wantJournalPaths)
			}
			if !reflect.DeepEqual(durable.Receipt.ChangedFiles, receipt.ChangedFiles) {
				t.Fatalf("journal receipt ChangedFiles = %#v, want unchanged %#v", durable.Receipt.ChangedFiles, receipt.ChangedFiles)
			}
			if got := visibleApplicationOrder(before, after.files); !reflect.DeepEqual(got, wantApplied) {
				t.Fatalf("derived application order = %v, want %v", got, wantApplied)
			}
		})
	}
}

func TestRecoveryAppliesRootIndexLast(t *testing.T) {
	for _, transition := range allRootIndexTransitions() {
		t.Run(string(transition), func(t *testing.T) {
			// Arrange. Stop immediately after the journal directory is durable,
			// before the normal publisher can touch a revision-visible target.
			root, s, before, after, receipt := rootIndexPublicationFixtureForTransition(t, transition)
			wantErr := errors.New("crash after durable journal")
			fired := false
			journalRenamed := false
			s.config.PostFault = func(step Step) error {
				if step == StepJournalRename {
					journalRenamed = true
				}
				if !fired && journalRenamed && step == StepJournalDirectorySync {
					fired = true
					return wantErr
				}
				return nil
			}
			if err := s.publish(context.Background(), after, receipt); !errors.Is(err, wantErr) || !fired {
				t.Fatalf("publish() error = %v, durable-journal fault fired = %t", err, fired)
			}
			assertRootIndexJournalEvidence(t, s, after, receipt)
			visible := cloneVisibleFiles(before)
			var recoveredOrder []string
			recoveryConfig := Config{PostFault: func(step Step) error {
				if step != StepRename && step != StepRemove {
					return nil
				}
				current, err := readVisibleRoot(context.Background(), s.rootFD)
				if err != nil {
					return err
				}
				changed := changedVisiblePath(visible, current)
				if changed != "" {
					recoveredOrder = append(recoveredOrder, changed)
				}
				visible = current
				return nil
			}}

			// Act.
			reopened, err := openObserved(root, recoveryConfig)
			if reopened != nil {
				registerStoreCleanup(t, reopened)
			}

			// Assert.
			if err != nil {
				t.Fatalf("Open() recovery error = %v", err)
			}
			wantOrder := rootIndexExpectedApplicationOrder(transition)
			if !reflect.DeepEqual(recoveredOrder, wantOrder) {
				t.Fatalf("recovery physical application order = %v, want %v", recoveredOrder, wantOrder)
			}
			recovered, err := readVisibleRoot(context.Background(), reopened.rootFD)
			if err != nil || !sameVisibleFiles(recovered, after.files) {
				t.Fatalf("recovered exact post-state: read=%v got=%v want=%v", err, sortedFilePaths(recovered), sortedFilePaths(after.files))
			}
		})
	}
}

func TestRootIndexClaimActionTraceHasNoVisibleTail(t *testing.T) {
	for _, transition := range changedRootIndexTransitions() {
		t.Run(string(transition), func(t *testing.T) {
			// Arrange.
			_, s, _, after, receipt := rootIndexPublicationFixtureForTransition(t, transition)
			var trace []string
			s.descriptorBarrier = func(operation string) error {
				for _, prefix := range []string{"visible_begin:", "visible_delete_begin:", "visible_parent_synced:", "visible_delete_parent_synced:"} {
					if strings.HasPrefix(operation, prefix) {
						trace = append(trace, operation)
						break
					}
				}
				return nil
			}

			// Act.
			err := s.publish(context.Background(), after, receipt)

			// Assert.
			if err != nil {
				t.Fatalf("publish: %v", err)
			}
			rootBegin, rootSync := -1, -1
			lastNonRootSync := -1
			for i, event := range trace {
				switch {
				case event == "visible_begin:index.md" || event == "visible_delete_begin:index.md":
					rootBegin = i
				case event == "visible_parent_synced:index.md" || event == "visible_delete_parent_synced:index.md":
					rootSync = i
				case strings.HasPrefix(event, "visible_parent_synced:") || strings.HasPrefix(event, "visible_delete_parent_synced:"):
					lastNonRootSync = i
				}
			}
			if rootBegin < 0 || rootSync <= rootBegin || lastNonRootSync >= rootBegin {
				t.Fatalf("invalid root marker trace: trace=%v lastNonRootSync=%d rootBegin=%d rootSync=%d", trace, lastNonRootSync, rootBegin, rootSync)
			}
			for _, event := range trace[rootSync+1:] {
				if strings.HasPrefix(event, "visible_begin:") || strings.HasPrefix(event, "visible_delete_begin:") || strings.HasPrefix(event, "visible_parent_synced:") || strings.HasPrefix(event, "visible_delete_parent_synced:") {
					t.Fatalf("visible operation after root commit marker: %s trace=%v", event, trace)
				}
			}
		})
	}
}

func TestRootIndexClaimForeignSwapMatrix(t *testing.T) {
	for _, seam := range []string{
		"visible_after_target_witness:index.md",
		"visible_target_quarantined:index.md",
		"visible_before_install:index.md",
		"visible_source_installed:index.md",
	} {
		t.Run(seam, func(t *testing.T) {
			// Arrange.
			root, s, before, after, receipt := rootIndexPublicationFixtureForTransition(t, rootIndexUpdate)
			oldIndex := append([]byte(nil), before["index.md"]...)
			oldInfo, _ := os.Stat(filepath.Join(root, "index.md"))
			var foreign os.FileInfo
			var foreignPath string
			var want []byte
			s.descriptorBarrier = func(operation string) error {
				if operation != seam || foreign != nil {
					return nil
				}
				switch seam {
				case "visible_target_quarantined:index.md":
					foreignPath = "index.md"
					want = append([]byte(nil), oldIndex...)
					if err := os.WriteFile(filepath.Join(root, foreignPath), want, 0o640); err != nil {
						t.Fatal(err)
					}
					foreign, _ = os.Stat(filepath.Join(root, foreignPath))
				case "visible_before_install:index.md":
					entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(temporaryDirectory)))
					if err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), ".okf-tmp-") {
							foreignPath, want, foreign = replaceRegularWithSameBytes(t, root, path.Join(temporaryDirectory, entry.Name()))
							break
						}
					}
				default:
					foreignPath, want, foreign = replaceRegularWithSameBytes(t, root, "index.md")
				}
				return nil
			}

			// Act.
			publishErr := s.publish(context.Background(), after, receipt)
			s.descriptorBarrier = nil
			foreignAfter, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(foreignPath)))
			got, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(foreignPath)))
			journalCount := len(pendingJournalNames(t, root))
			_, reopenErr := openObserved(root, Config{})
			var committed, recoveredCommitted *store.CommittedError

			// Assert.
			if foreign == nil || publishErr == nil || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("foreign=%v publish=%v committed=%v", foreign, publishErr, committed)
			}
			if statErr != nil || readErr != nil || !os.SameFile(foreign, foreignAfter) || foreign.Mode() != foreignAfter.Mode() || !bytes.Equal(want, got) {
				t.Fatalf("foreign changed: stat=%v read=%v same=%t mode=%v/%v bytes=%t", statErr, readErr, os.SameFile(foreign, foreignAfter), foreign.Mode(), foreignAfter.Mode(), bytes.Equal(want, got))
			}
			assertNonRootResultExact(t, root, after.files)
			if journalCount != 1 {
				t.Fatalf("durable root-index journal evidence lost: count=%d", journalCount)
			}
			if seam == "visible_target_quarantined:index.md" {
				key, _ := newArtifactClaimKey(receipt.RequestDigest, "index.md", claimVisible)
				claim, claimErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.claimPath())))
				if claimErr != nil || !os.SameFile(oldInfo, claim) {
					t.Fatalf("old root quarantine lost: claim=%v error=%v", claim, claimErr)
				}
			}
			if reopenErr == nil || !errors.As(reopenErr, &recoveredCommitted) || recoveredCommitted.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("reopen=%v committed=%v", reopenErr, recoveredCommitted)
			}
		})
	}
}

func TestRootIndexClaimForeignCreateAndDelete(t *testing.T) {
	for _, transition := range []rootIndexTransition{rootIndexCreate, rootIndexDelete} {
		t.Run(string(transition), func(t *testing.T) {
			// Arrange.
			root, s, before, after, receipt := rootIndexPublicationFixtureForTransition(t, transition)
			seam := "visible_before_install:index.md"
			want := append([]byte(nil), after.files["index.md"]...)
			if transition == rootIndexDelete {
				seam = "visible_delete_claimed:index.md"
				want = append([]byte(nil), before["index.md"]...)
			}
			var foreign os.FileInfo
			s.descriptorBarrier = func(operation string) error {
				if operation != seam || foreign != nil {
					return nil
				}
				if err := os.WriteFile(filepath.Join(root, "index.md"), want, 0o640); err != nil {
					t.Fatal(err)
				}
				foreign, _ = os.Stat(filepath.Join(root, "index.md"))
				return nil
			}

			// Act.
			publishErr := s.publish(context.Background(), after, receipt)
			s.descriptorBarrier = nil
			afterInfo, statErr := os.Stat(filepath.Join(root, "index.md"))
			got, readErr := os.ReadFile(filepath.Join(root, "index.md"))
			_, reopenErr := openObserved(root, Config{})
			var committed, recoveredCommitted *store.CommittedError

			// Assert.
			if foreign == nil || publishErr == nil || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("foreign=%v publish=%v committed=%v", foreign, publishErr, committed)
			}
			if statErr != nil || readErr != nil || !os.SameFile(foreign, afterInfo) || foreign.Mode() != afterInfo.Mode() || !bytes.Equal(want, got) {
				t.Fatalf("foreign changed: stat=%v read=%v same=%t mode=%v/%v bytes=%t", statErr, readErr, os.SameFile(foreign, afterInfo), foreign.Mode(), afterInfo.Mode(), bytes.Equal(want, got))
			}
			assertNonRootResultExact(t, root, after.files)
			if reopenErr == nil || !errors.As(reopenErr, &recoveredCommitted) || recoveredCommitted.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("reopen=%v committed=%v", reopenErr, recoveredCommitted)
			}
		})
	}
}

func TestRootIndexClaimDeleteAbsentExpectation(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	expected := mutationTargetIdentity{absent: true, operation: "root-delete-absent"}

	// Act/Assert.
	if err := s.removeVisibleGuarded(context.Background(), "index.md", expected); err != nil {
		t.Fatalf("absent root delete: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("foreign root"), 0o640); err != nil {
		t.Fatal(err)
	}
	foreign, _ := os.Stat(filepath.Join(root, "index.md"))
	deleteErr := s.removeVisibleGuarded(context.Background(), "index.md", expected)
	after, statErr := os.Stat(filepath.Join(root, "index.md"))
	if deleteErr == nil || statErr != nil || !os.SameFile(foreign, after) {
		t.Fatalf("changed absence delete=%v foreign=%v stat=%v", deleteErr, foreign, statErr)
	}
}

func assertNonRootResultExact(t *testing.T, root string, result map[string][]byte) {
	t.Helper()
	rootFD, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFD.Close()
	current, err := readVisibleRoot(context.Background(), rootFD)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range result {
		if name == "index.md" {
			continue
		}
		if !bytes.Equal(current[name], want) {
			t.Fatalf("non-root result mismatch at %s", name)
		}
	}
	for name := range current {
		if name == "index.md" {
			continue
		}
		if _, ok := result[name]; !ok {
			t.Fatalf("non-root obsolete path remains: %s", name)
		}
	}
}

func TestRootIndexLastCrashMatrixCoversEveryStepOccurrence(t *testing.T) {
	for _, transition := range changedRootIndexTransitions() {
		transition := transition
		t.Run(string(transition), func(t *testing.T) {
			t.Parallel()

			runRootIndexLastCrashMatrix(t, transition)
		})
	}
}

func runRootIndexLastCrashMatrix(t *testing.T, transition rootIndexTransition) {
	// Arrange. Capture the exact successful publication trace. Occurrence is
	// part of the identity because generic durability Steps repeat per file.
	_, baseline, _, baselineAfter, baselineReceipt := rootIndexCrashMatrixFixtureForTransition(t, transition)
	points := make([]rootIndexCrashPoint, 0)
	preOccurrences := map[Step]int{}
	postOccurrences := map[Step]int{}
	journalRenamed := false
	journalDurable := false
	visibleApplyStarted := false
	baseline.config.Fault = func(step Step) error {
		preOccurrences[step]++
		points = append(points, rootIndexCrashPoint{
			phase:               "pre",
			step:                step,
			occurrence:          preOccurrences[step],
			journalDurable:      journalDurable,
			visibleApplyStarted: visibleApplyStarted,
		})
		return nil
	}
	baseline.config.PostFault = func(step Step) error {
		postOccurrences[step]++
		if step == StepJournalRename {
			journalRenamed = true
		}
		if step == StepJournalDirectorySync && journalRenamed && !journalDurable {
			journalDurable = true
		}
		points = append(points, rootIndexCrashPoint{
			phase:               "post",
			step:                step,
			occurrence:          postOccurrences[step],
			journalDurable:      journalDurable,
			visibleApplyStarted: visibleApplyStarted,
		})
		if step == StepJournalDirectorySync && journalDurable && !visibleApplyStarted {
			visibleApplyStarted = true
		}
		return nil
	}
	if err := baseline.publish(context.Background(), baselineAfter, baselineReceipt); err != nil {
		t.Fatalf("baseline publish() error = %v", err)
	}
	if len(points) == 0 {
		t.Fatal("baseline publication exposed no crash points")
	}
	assertJournalDurabilityBoundary(t, points)
	points = selectRootIndexObservableCrashPoints(t, points, preOccurrences, postOccurrences)

	for _, point := range points {
		point := point
		t.Run(fmt.Sprintf("%s/%s/%d", point.phase, point.step, point.occurrence), func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root, s, before, after, receipt := rootIndexCrashMatrixFixtureForTransition(t, transition)
			wantErr := errors.New("injected root-index publication crash")
			preSeen := map[Step]int{}
			postSeen := map[Step]int{}
			fired := false
			inject := func(phase string, step Step, occurrences map[Step]int) error {
				occurrences[step]++
				if fired || phase != point.phase || step != point.step || occurrences[step] != point.occurrence {
					return nil
				}
				fired = true
				return wantErr
			}
			s.config.Fault = func(step Step) error { return inject("pre", step, preSeen) }
			s.config.PostFault = func(step Step) error { return inject("post", step, postSeen) }

			// Act.
			publishErr := s.publish(context.Background(), after, receipt)
			rawAfterFailure, rawErr := readVisibleRoot(context.Background(), s.rootFD)
			if !point.journalDurable {
				journalName, err := journalPath(receipt.RequestDigest)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.rootFD.Remove(journalName); err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("simulate loss of non-durable journal: %v", err)
				}
			}
			reopened, reopenErr := openRootIndexCrashMatrixStore(root)
			if reopened != nil {
				registerStoreCleanup(t, reopened)
			}
			var recovered map[string][]byte
			if reopenErr == nil {
				recovered, reopenErr = readVisibleRoot(context.Background(), reopened.rootFD)
			}

			// Assert.
			if !fired || !errors.Is(publishErr, wantErr) {
				t.Fatalf("publish() error = %v, fired = %t", publishErr, fired)
			}
			if rawErr != nil {
				t.Fatalf("read raw visible state after failure: %v", rawErr)
			}
			if !point.visibleApplyStarted && !sameVisibleFiles(rawAfterFailure, before) {
				t.Fatalf("pre-apply fault exposed revision-visible changes: got=%v want=%v", sortedFilePaths(rawAfterFailure), sortedFilePaths(before))
			}
			if !rootIndexPublicationStateIsForward(before, after.files, rawAfterFailure) {
				t.Fatalf("normal publication exposed reverse root-signpost state: got=%v", sortedFilePaths(rawAfterFailure))
			}
			if reopenErr != nil {
				t.Fatalf("Open() recovery error = %v", reopenErr)
			}
			want := before
			if point.journalDurable {
				want = after.files
			}
			if !sameVisibleFiles(recovered, want) {
				t.Fatalf("recovered state differs from exact expected state: got=%v want=%v journal_durable=%t", sortedFilePaths(recovered), sortedFilePaths(want), point.journalDurable)
			}
		})
	}
}

func TestRootIndexLastRecoveryCrashMatrixCoversEveryStepOccurrence(t *testing.T) {
	for _, transition := range changedRootIndexTransitions() {
		transition := transition
		t.Run(string(transition), func(t *testing.T) {
			t.Parallel()

			runRootIndexLastRecoveryCrashMatrix(t, transition)
		})
	}
}

func runRootIndexLastRecoveryCrashMatrix(t *testing.T, transition rootIndexTransition) {
	// Arrange. Capture the exact recovery trace from a journal whose containing
	// directory reached the durability boundary before the normal publisher ran.
	_, baseline, _, after, receipt := rootIndexCrashMatrixFixtureForTransition(t, transition)
	stageDurableRootIndexJournal(t, baseline, after, receipt)
	points := make([]rootIndexCrashPoint, 0)
	preOccurrences := map[Step]int{}
	postOccurrences := map[Step]int{}
	baseline.config.Fault = func(step Step) error {
		preOccurrences[step]++
		points = append(points, rootIndexCrashPoint{phase: "pre", step: step, occurrence: preOccurrences[step], journalDurable: true})
		return nil
	}
	baseline.config.PostFault = func(step Step) error {
		postOccurrences[step]++
		points = append(points, rootIndexCrashPoint{phase: "post", step: step, occurrence: postOccurrences[step], journalDurable: true})
		return nil
	}
	if err := baseline.recoverForTest(context.Background()); err != nil {
		t.Fatalf("baseline recover() error = %v", err)
	}
	if len(points) == 0 {
		t.Fatal("baseline recovery exposed no crash points")
	}
	points = selectRootIndexObservableCrashPoints(t, points, preOccurrences, postOccurrences)

	for _, point := range points {
		point := point
		t.Run(fmt.Sprintf("%s/%s/%d", point.phase, point.step, point.occurrence), func(t *testing.T) {
			t.Parallel()

			// Arrange.
			_, s, before, after, receipt := rootIndexCrashMatrixFixtureForTransition(t, transition)
			stageDurableRootIndexJournal(t, s, after, receipt)
			wantErr := errors.New("injected root-index recovery crash")
			preSeen := map[Step]int{}
			postSeen := map[Step]int{}
			fired := false
			inject := func(phase string, step Step, occurrences map[Step]int) error {
				occurrences[step]++
				if fired || phase != point.phase || step != point.step || occurrences[step] != point.occurrence {
					return nil
				}
				fired = true
				return wantErr
			}
			s.config.Fault = func(step Step) error { return inject("pre", step, preSeen) }
			s.config.PostFault = func(step Step) error { return inject("post", step, postSeen) }

			// Act.
			recoverErr := s.recoverForTest(context.Background())
			interrupted, readErr := readVisibleRoot(context.Background(), s.rootFD)
			s.config.Fault = nil
			s.config.PostFault = nil
			convergeErr := s.recoverForTest(context.Background())
			recovered, finalReadErr := readVisibleRoot(context.Background(), s.rootFD)

			// Assert.
			if !fired || !errors.Is(recoverErr, wantErr) {
				t.Fatalf("recover() error = %v, fired = %t", recoverErr, fired)
			}
			if readErr != nil {
				t.Fatalf("read interrupted visible state: %v", readErr)
			}
			if !rootIndexPublicationStateIsForward(before, after.files, interrupted) {
				t.Fatalf("recovery exposed reverse root-signpost state: got=%v", sortedFilePaths(interrupted))
			}
			if convergeErr != nil || finalReadErr != nil || !sameVisibleFiles(recovered, after.files) {
				t.Fatalf("recovery did not converge: recover=%v read=%v got=%v want=%v", convergeErr, finalReadErr, sortedFilePaths(recovered), sortedFilePaths(after.files))
			}
		})
	}
}

func TestRecoveryRejectsResultRootIndexWithIncompleteNonRootState(t *testing.T) {
	for _, transition := range changedRootIndexTransitions() {
		t.Run(string(transition), func(t *testing.T) {
			// Arrange. This reverse mix cannot be emitted by root-index-last
			// publication: the root signpost is result while every non-root
			// path is still base.
			root, s, before, after, receipt := rootIndexPublicationFixtureForTransition(t, transition)
			stageDurableRootIndexJournal(t, s, after, receipt)
			if resultIndex, exists := after.files["index.md"]; exists {
				writeTestFile(t, root, "index.md", string(resultIndex))
			} else if err := os.Remove(root + string(os.PathSeparator) + "index.md"); err != nil {
				t.Fatal(err)
			}
			journalName, err := journalPath(receipt.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			reopened, openErr := openObserved(root, Config{})
			if reopened != nil {
				registerStoreCleanup(t, reopened)
			}
			current, readErr := readVisibleRoot(context.Background(), s.rootFD)

			// Assert.
			if !errors.Is(openErr, store.ErrStorageCorrupt) {
				t.Fatalf("Open() error = %v, want storage corruption", openErr)
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			wantMixed := cloneVisibleFiles(before)
			if resultIndex, exists := after.files["index.md"]; exists {
				wantMixed["index.md"] = resultIndex
			} else {
				delete(wantMixed, "index.md")
			}
			if !sameVisibleFiles(current, wantMixed) {
				t.Fatalf("recovery changed reverse signpost state: got=%v want=%v", sortedFilePaths(current), sortedFilePaths(wantMixed))
			}
			if _, err := s.rootFD.Stat(journalName); err != nil {
				t.Fatalf("journal evidence removed: %v", err)
			}
		})
	}
}

type rootIndexCrashPoint struct {
	phase               string
	step                Step
	occurrence          int
	journalDurable      bool
	visibleApplyStarted bool
}

func selectRootIndexObservableCrashPoints(t *testing.T, points []rootIndexCrashPoint, pre, post map[Step]int) []rootIndexCrashPoint {
	t.Helper()
	selected := make([]rootIndexCrashPoint, 0, 32)
	selectedSet := make(map[string]struct{})
	for _, point := range points {
		last := pre[point.step]
		if point.phase == "post" {
			last = post[point.step]
		}
		keep := false
		switch point.step {
		case StepStageFileWrite, StepJournalRename:
			keep = point.occurrence == 1
		case StepJournalDirectorySync, StepFileWrite, StepFileSync, StepFileClose, StepRename, StepRemove, StepDirectorySync:
			keep = point.occurrence == 1 || point.occurrence == last
		}
		if keep {
			selected = append(selected, point)
			selectedSet[fmt.Sprintf("%s/%s/%d", point.phase, point.step, point.occurrence)] = struct{}{}
		}
	}
	if len(selected) == 0 || len(selected) > 40 {
		t.Fatalf("root-index observable crash rows=%d", len(selected))
	}
	t.Logf("retained %d serial root-index observable crash rows", len(selected))
	for _, point := range points {
		key := fmt.Sprintf("%s/%s/%d", point.phase, point.step, point.occurrence)
		if _, ok := selectedSet[key]; ok {
			continue
		}
		if owner, ok := stepOwnerRegistry[point.step]; !ok || owner == nil {
			t.Fatalf("unmapped private root-index crash point %s", key)
		}
	}
	return selected
}

func assertJournalDurabilityBoundary(t *testing.T, points []rootIndexCrashPoint) {
	t.Helper()
	var postRename, preDirectorySync, postDirectorySync *rootIndexCrashPoint
	for i := range points {
		point := &points[i]
		switch {
		case postRename == nil && point.phase == "post" && point.step == StepJournalRename:
			postRename = point
		case postRename != nil && preDirectorySync == nil && point.phase == "pre" && point.step == StepJournalDirectorySync:
			preDirectorySync = point
		case preDirectorySync != nil && postDirectorySync == nil && point.phase == "post" && point.step == StepJournalDirectorySync:
			postDirectorySync = point
		}
	}
	if postRename == nil || preDirectorySync == nil || postDirectorySync == nil {
		t.Fatalf("journal boundary trace incomplete: rename=%v pre-sync=%v post-sync=%v", postRename, preDirectorySync, postDirectorySync)
	}
	if postRename.journalDurable || preDirectorySync.journalDurable || !postDirectorySync.journalDurable {
		t.Fatalf("journal durability boundary must be post %s, got rename=%t pre-sync=%t post-sync=%t", StepJournalDirectorySync, postRename.journalDurable, preDirectorySync.journalDurable, postDirectorySync.journalDurable)
	}
}

func stageDurableRootIndexJournal(t *testing.T, s *Store, after *snapshot, receipt store.CommitReceipt) {
	t.Helper()
	wantErr := errors.New("stop after durable root-index journal")
	fired := false
	journalRenamed := false
	s.config.Fault = nil
	s.config.PostFault = func(step Step) error {
		if step == StepJournalRename {
			journalRenamed = true
		}
		if !fired && journalRenamed && step == StepJournalDirectorySync {
			fired = true
			return wantErr
		}
		return nil
	}
	if err := s.publish(context.Background(), after, receipt); !errors.Is(err, wantErr) || !fired {
		t.Fatalf("stage durable journal: publish error=%v fired=%t", err, fired)
	}
	s.config.PostFault = nil
}

func assertRootIndexJournalEvidence(t *testing.T, s *Store, after *snapshot, receipt store.CommitReceipt) {
	t.Helper()
	journalName, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := readMetadataLimit(context.Background(), s.rootFD, journalName, maxJournalManifestRead)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := decodeJournalWithAlgorithm(raw, s.config.HashAlgorithm)
	if err != nil {
		t.Fatal(err)
	}
	gotPaths := make([]string, len(durable.Files))
	for i, file := range durable.Files {
		gotPaths[i] = file.Path
	}
	if wantPaths := sortedFilePaths(after.files); !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("journal Files order = %v, want canonical %v", gotPaths, wantPaths)
	}
	if !reflect.DeepEqual(durable.Receipt.ChangedFiles, receipt.ChangedFiles) ||
		!reflect.DeepEqual(durable.Receipt.ChangedRefs, receipt.ChangedRefs) {
		t.Fatalf("journal receipt delta = %#v/%#v, want unchanged %#v/%#v", durable.Receipt.ChangedFiles, durable.Receipt.ChangedRefs, receipt.ChangedFiles, receipt.ChangedRefs)
	}
}

func rootIndexPublicationStateIsForward(base, result, current map[string][]byte) bool {
	rootAtResult := reflect.DeepEqual(current["index.md"], result["index.md"])
	rootChanged := !reflect.DeepEqual(base["index.md"], result["index.md"])
	if !rootChanged || !rootAtResult {
		return true
	}
	for path, data := range result {
		if path == "index.md" {
			continue
		}
		if !reflect.DeepEqual(current[path], data) {
			return false
		}
	}
	for path := range base {
		if path == "index.md" {
			continue
		}
		if _, remains := result[path]; !remains {
			if _, exists := current[path]; exists {
				return false
			}
		}
	}
	return true
}

type rootIndexTransition string

const (
	rootIndexCreate    rootIndexTransition = "create"
	rootIndexUpdate    rootIndexTransition = "update"
	rootIndexDelete    rootIndexTransition = "delete"
	rootIndexUnchanged rootIndexTransition = "unchanged"
)

func allRootIndexTransitions() []rootIndexTransition {
	return []rootIndexTransition{
		rootIndexCreate,
		rootIndexUpdate,
		rootIndexDelete,
		rootIndexUnchanged,
	}
}

func changedRootIndexTransitions() []rootIndexTransition {
	return []rootIndexTransition{
		rootIndexCreate,
		rootIndexUpdate,
		rootIndexDelete,
	}
}

func rootIndexExpectedApplicationOrder(transition rootIndexTransition) []string {
	paths := []string{"a.md", "created.md", "obsolete.md", "z.md"}
	if transition != rootIndexUnchanged {
		paths = append(paths, "index.md")
	}
	return paths
}

func rootIndexPublicationFixtureForTransition(t *testing.T, transition rootIndexTransition) (string, *Store, map[string][]byte, *snapshot, store.CommitReceipt) {
	t.Helper()
	return rootIndexPublicationFixtureForTransitionWithOpen(t, transition, func(root string) (*Store, error) {
		return openObserved(root, Config{})
	})
}

func rootIndexCrashMatrixFixtureForTransition(t *testing.T, transition rootIndexTransition) (string, *Store, map[string][]byte, *snapshot, store.CommitReceipt) {
	t.Helper()
	return rootIndexPublicationFixtureForTransitionWithOpen(t, transition, openRootIndexCrashMatrixStore)
}

func rootIndexPublicationFixtureForTransitionWithOpen(
	t *testing.T,
	transition rootIndexTransition,
	open func(string) (*Store, error),
) (string, *Store, map[string][]byte, *snapshot, store.CommitReceipt) {
	t.Helper()
	root := t.TempDir()
	before := rootIndexBaseFiles()
	if transition == rootIndexCreate {
		delete(before, "index.md")
	}
	for path, data := range before {
		writeTestFile(t, root, path, string(data))
	}
	s, err := open(root)
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	base, err := newSnapshot(context.Background(), before)
	if err != nil {
		t.Fatal(err)
	}
	after := rootIndexTargetSnapshotForTransition(t, transition, before)
	projection, err := receiptprojection.Derive(context.Background(), base.concepts, after.concepts)
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), after.Revision(), "root-index-last-"+string(transition), "")
	receipt.ChangedFiles = projection.ChangedFiles
	receipt.ChangedRefs = projection.ChangedRefs
	return root, s, before, after, receipt
}

func openRootIndexCrashMatrixStore(root string) (*Store, error) {
	return openObservedWithRecoveryLimitsAndSync(
		context.Background(),
		root,
		Config{},
		absoluteStagedManifestLimits(),
		durabilitySyncImplementation{
			file:      func(*os.File) error { return nil },
			directory: func(*Store, string) error { return nil },
		})

}

func rootIndexBaseFiles() map[string][]byte {
	return map[string][]byte{
		"a.md":        []byte(adversarialDocument("a-before")),
		"index.md":    []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n- [Obsolete](obsolete.md)\n- [Z](z.md)\n"),
		"obsolete.md": []byte(adversarialDocument("obsolete")),
		"z.md":        []byte(adversarialDocument("z-before")),
	}
}

func rootIndexTargetSnapshotForTransition(t *testing.T, transition rootIndexTransition, before map[string][]byte) *snapshot {
	t.Helper()
	files := map[string][]byte{
		"a.md":       []byte(adversarialDocument("a-after")),
		"created.md": []byte(adversarialDocument("created")),
		"index.md":   []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n- [Created](created.md)\n- [Z](z.md)\n"),
		"z.md":       []byte(adversarialDocument("z-after")),
	}
	switch transition {
	case rootIndexCreate, rootIndexUpdate:
	case rootIndexDelete:
		delete(files, "index.md")
	case rootIndexUnchanged:
		files["index.md"] = append([]byte(nil), before["index.md"]...)
	default:
		t.Fatalf("unknown root index transition %q", transition)
	}
	next, err := newSnapshot(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func cloneVisibleFiles(files map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(files))
	for path, data := range files {
		cloned[path] = append([]byte(nil), data...)
	}
	return cloned
}

func changedVisiblePath(before, after map[string][]byte) string {
	for _, path := range sortedFilePaths(before) {
		data, exists := after[path]
		if !exists || !reflect.DeepEqual(data, before[path]) {
			return path
		}
	}
	for _, path := range sortedFilePaths(after) {
		if _, exists := before[path]; !exists {
			return path
		}
	}
	return ""
}
