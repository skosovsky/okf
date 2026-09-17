package fs

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/store"
)

func TestVisiblePostFaultRecoveryReplaysSkippedNamespaceBarriers(t *testing.T) {
	tests := []struct {
		name            string
		result          map[string][]byte
		crashStep       Step
		wantBarrier     []string
		wantResultPath  string
		wantResultBytes []byte
	}{
		{
			name: "rename write",
			result: map[string][]byte{
				"new/z/concept.md": []byte(adversarialDocument("result")),
				"keep.md":          []byte(adversarialDocument("keep")),
			},
			crashStep:       StepRename,
			wantBarrier:     []string{"new/z", "old/deep", "new", "old", "."},
			wantResultPath:  "new/z/concept.md",
			wantResultBytes: []byte(adversarialDocument("result")),
		},
		{
			name: "delete",
			result: map[string][]byte{
				"keep.md": []byte(adversarialDocument("keep")),
			},
			crashStep:   StepRemove,
			wantBarrier: []string{"old/deep", "old", "."},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			oldPath := "old/deep/concept.md"
			writeTestFile(t, root, oldPath, adversarialDocument("base"))
			writeTestFile(t, root, "keep.md", adversarialDocument("keep"))
			s, err := openObserved(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			base := adversarialSnapshot(t, s)
			next, err := newSnapshot(context.Background(), test.result)
			if err != nil {
				t.Fatal(err)
			}
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "visible-postfault-"+test.name, "")
			crashErr := errors.New("visible namespace post-fault")
			journalRenamed := false
			journalDurable := false
			crashed := false
			var crashTrace []string
			s.config.Fault = func(step Step) error {
				crashTrace = append(crashTrace, "pre:"+string(step))
				return nil
			}
			s.config.PostFault = func(step Step) error {
				crashTrace = append(crashTrace, "post:"+string(step))
				if step == StepJournalRename {
					journalRenamed = true
				}
				if journalRenamed && step == StepJournalDirectorySync {
					journalDurable = true
				}
				if journalDurable && !crashed && step == test.crashStep {
					crashed = true
					return crashErr
				}
				return nil
			}
			// Act: publish through the real durable journal and visible apply path.
			publishErr := s.publish(context.Background(), next, receipt)

			// Assert: an erroring PostFault is terminal in the crashing invocation.
			if !errors.Is(publishErr, crashErr) || !crashed {
				t.Fatalf("publish() error=%v crashed=%t", publishErr, crashed)
			}
			wantLast := "post:" + string(test.crashStep)
			if len(crashTrace) == 0 || crashTrace[len(crashTrace)-1] != wantLast {
				t.Fatalf("hooks continued after erroring PostFault: trace tail=%#v want last=%q", crashTrace, wantLast)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}

			var recoveryTrace []string
			recovered, err := openObserved(root, Config{
				DirectorySync: func(dir string) error {
					recoveryTrace = append(recoveryTrace, "dir:"+dir)
					return nil
				},
				PostFault: func(step Step) error {
					recoveryTrace = append(recoveryTrace, "post:"+string(step))
					return nil
				},
			})

			if err != nil {
				t.Fatal(err)
			}

			barrierEvents := prefixedDirectories(test.wantBarrier)
			barrierAt, barrierEnd := lastFilteredSubsequenceRange(recoveryTrace, barrierEvents, "dir:")
			tempAt := lastIndexBefore(recoveryTrace, "dir:"+temporaryDirectory, barrierAt)
			journalRemoveAt := indexFrom(recoveryTrace, "post:"+string(StepRemove), barrierEnd+1)
			journalCleanupAt := indexFrom(recoveryTrace, "dir:"+internalDirectory+"/transactions", journalRemoveAt+1)
			if barrierAt < 0 || tempAt < 0 || journalRemoveAt < 0 || journalCleanupAt < 0 {
				t.Fatalf(
					"recovery durability order incomplete: temp=%d barrier=%d journal-remove=%d journal-dir=%d trace=%#v",
					tempAt,
					barrierAt,
					journalRemoveAt,
					journalCleanupAt,
					recoveryTrace,
				)
			}
			if len(pendingJournalNames(t, root)) != 0 {
				t.Fatalf("first recovery left a pending journal: %v", pendingJournalNames(t, root))
			}
			if err := recovered.Close(); err != nil {
				t.Fatal(err)
			}

			// A second independent observation is converged and needs no
			// journal evidence to reconstruct the promised result.
			second, err := openObserved(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			registerStoreCleanup(t, second)
			snapshot := adversarialSnapshot(t, second)
			oldBytes, oldErr := snapshot.ReadFile(context.Background(), oldPath)
			if !errors.Is(oldErr, os.ErrNotExist) || oldBytes != nil {
				t.Fatalf("old path after recovery = %q, %v; want absent", oldBytes, oldErr)
			}
			if test.wantResultPath != "" {
				got, readErr := snapshot.ReadFile(context.Background(), test.wantResultPath)
				if readErr != nil || !reflect.DeepEqual(got, test.wantResultBytes) {
					t.Fatalf("result path after recovery = %q, %v; want %q", got, readErr, test.wantResultBytes)
				}
			}
			if len(pendingJournalNames(t, root)) != 0 {
				t.Fatalf("second recovery left a pending journal: %v", pendingJournalNames(t, root))
			}
		})
	}
}

func TestJournalNamespacePostFaultRecoverySyncsTransactionsBeforeEvidenceUse(t *testing.T) {
	tests := []struct {
		name      string
		crashStep Step
	}{
		{name: "journal rename remains listed", crashStep: StepJournalRename},
		{name: "journal remove is absent from listing", crashStep: StepRemove},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeTestFile(t, root, "concept.md", adversarialDocument("unchanged"))
			s, err := openObserved(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			base := adversarialSnapshot(t, s)
			next := base.(*snapshot)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "journal-namespace-"+test.name, "")
			journalRenamed := false
			crashed := false
			crashErr := errors.New("journal namespace post-fault")
			s.config.PostFault = func(step Step) error {
				if step == StepJournalRename {
					journalRenamed = true
					if test.crashStep == StepJournalRename {
						crashed = true
						return crashErr
					}
				}
				if journalRenamed && !crashed && step == test.crashStep {
					crashed = true
					return crashErr
				}
				return nil
			}
			if err := s.publish(context.Background(), next, receipt); !errors.Is(err, crashErr) || !crashed {
				t.Fatalf("publish() error=%v crashed=%t", err, crashed)
			}
			s.config.PostFault = nil
			var synced []string
			var recoverySteps []Step
			s.config.DirectorySync = func(dir string) error {
				synced = append(synced, dir)
				return nil
			}
			s.config.PostFault = func(step Step) error {
				recoverySteps = append(recoverySteps, step)
				return nil
			}

			// Act.
			recoveryErr := s.recoverForTest(context.Background())

			// Assert.
			temporaryAt := indexFrom(synced, temporaryDirectory, 0)
			transactionAt := indexFrom(synced, internalDirectory+"/transactions", temporaryAt+1)
			temporaryStepAt, transactionStepAt := -1, -1
			for i, step := range recoverySteps {
				if temporaryStepAt < 0 && step == StepTempCleanupDirectorySync {
					temporaryStepAt = i
				}
				if temporaryStepAt >= 0 && step == StepJournalDirectorySync {
					transactionStepAt = i
					break
				}
			}
			if recoveryErr != nil || temporaryAt < 0 || transactionAt <= temporaryAt || temporaryStepAt < 0 || transactionStepAt <= temporaryStepAt {
				t.Fatalf("recovery error=%v syncs=%#v steps=%#v, want stable temp -> transaction evidence subsequence", recoveryErr, synced, recoverySteps)
			}
			if len(pendingJournalNames(t, root)) != 0 {
				t.Fatalf("recovery left pending journal: %v", pendingJournalNames(t, root))
			}
			got := adversarialSnapshot(t, s)
			if got.Revision() != base.Revision() {
				t.Fatalf("recovered revision=%s want=%s", got.Revision(), base.Revision())
			}
			stage, stageErr := journalStage(receipt.RequestDigest)
			if stageErr != nil {
				t.Fatal(stageErr)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path.Dir(stage)))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stage residue=%v", err)
			}
			for _, dir := range []string{claimDirectory, temporaryDirectory, path.Join(internalDirectory, "receipts")} {
				entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
				if err != nil || len(entries) != 0 {
					t.Fatalf("private residue dir=%s entries=%v err=%v", dir, entries, err)
				}
			}
			registerStoreCleanup(t, s)
		})
	}
}

func prefixedDirectories(directories []string) []string {
	out := make([]string, len(directories))
	for i, dir := range directories {
		out[i] = "dir:" + dir
	}
	return out
}

func lastFilteredSubsequenceRange(trace, sequence []string, prefix string) (int, int) {
	var filtered []string
	var indices []int
	for i, event := range trace {
		if strings.HasPrefix(event, prefix) {
			filtered = append(filtered, event)
			indices = append(indices, i)
		}
	}
	for at := len(filtered) - len(sequence); at >= 0; at-- {
		if reflect.DeepEqual(filtered[at:at+len(sequence)], sequence) {
			return indices[at], indices[at+len(sequence)-1]
		}
	}
	return -1, -1
}

func lastIndexBefore(trace []string, value string, before int) int {
	if before > len(trace) {
		before = len(trace)
	}
	for i := before - 1; i >= 0; i-- {
		if trace[i] == value {
			return i
		}
	}
	return -1
}

func indexFrom(trace []string, value string, from int) int {
	if from < 0 {
		from = 0
	}
	for i := from; i < len(trace); i++ {
		if trace[i] == value {
			return i
		}
	}
	return -1
}

func TestApplyResultExactSyncsRenameAndRemoveNamespacesBottomUp(t *testing.T) {
	tests := []struct {
		name       string
		result     map[string][]byte
		wantSynced []string
	}{
		{
			name: "rename",
			result: map[string][]byte{
				"new/z/concept.md": []byte(adversarialDocument("base")),
			},
			wantSynced: []string{"new/z", "old/deep", "new", "old", "."},
		},
		{
			name:       "remove",
			result:     map[string][]byte{},
			wantSynced: []string{"old/deep", "old", "."},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			basePath := "old/deep/concept.md"
			writeTestFile(t, root, basePath, adversarialDocument("base"))
			s, err := openObserved(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			registerStoreCleanup(t, s)
			base := adversarialSnapshot(t, s)
			next, err := newSnapshot(context.Background(), test.result)
			if err != nil {
				t.Fatal(err)
			}
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "result-exact-"+test.name, "")
			j, _, err := s.prepareJournalContext(context.Background(), next, receipt, replaceReplay{}, productionJournalManifestByteLimits())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.stageJournalPayloadsObserved(context.Background(), next, j); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(basePath))); err != nil {
				t.Fatal(err)
			}
			for name, data := range test.result {
				target := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var synced []string
			s.config.DirectorySync = func(dir string) error {
				synced = append(synced, dir)
				return nil
			}

			// Act.
			applied, applyErr := s.applyForTest(context.Background(), j)

			// Assert.
			if applyErr != nil || applied {
				t.Fatalf("apply() = (%t, %v), want result-exact no-op", applied, applyErr)
			}
			if !reflect.DeepEqual(synced, test.wantSynced) {
				t.Fatalf("namespace syncs = %#v, want %#v", synced, test.wantSynced)
			}
		})
	}
}

func TestRecoveryEmptyPrivateNamespacesStillSyncsDurabilityBarriers(t *testing.T) {
	// Arrange.
	_, s := adversarialStore(t, Config{})
	_ = adversarialSnapshot(t, s)
	var synced []string
	s.config.DirectorySync = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}

	// Act.
	err := s.recoverForTest(context.Background())

	// Assert.
	want := []string{temporaryDirectory, internalDirectory + "/transactions", internalDirectory + "/staging"}
	filtered := synced[:0]
	for _, dir := range synced {
		if dir == "." || dir == internalDirectory {
			continue // Named R5 private-directory repair owns these barriers.
		}
		filtered = append(filtered, dir)
	}
	if err != nil || !reflect.DeepEqual(filtered, want) {
		t.Fatalf("recovery error=%v syncs=%#v, want %#v", err, synced, want)
	}
}

func TestKnownPrivateNamespaceSyncIsDeterministicBottomUp(t *testing.T) {
	// Arrange.
	_, s := adversarialStore(t, Config{})
	_ = adversarialSnapshot(t, s)
	var synced []string
	s.config.DirectorySync = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}

	// Act.
	err := s.syncKnownPrivateNamespace(context.Background())

	// Assert.
	want := []string{
		internalDirectory + "/capabilities",
		claimDirectory,
		internalDirectory + "/receipts",
		internalDirectory + "/staging",
		temporaryDirectory,
		internalDirectory + "/transactions",
		internalDirectory,
		".",
	}
	if err != nil || !reflect.DeepEqual(synced, want) {
		t.Fatalf("syncKnownPrivateNamespace() error=%v syncs=%#v, want %#v", err, synced, want)
	}
}

func TestUnprovenOrphanStageCleanupPreservesNamespace(t *testing.T) {
	tests := []struct {
		name       string
		makeEntry  func(t *testing.T, root, stageDir string)
		wantSynced func(stageDir string) []string
	}{
		{
			name:      "payload already missing",
			makeEntry: func(*testing.T, string, string) {},
			wantSynced: func(string) []string {
				return []string{internalDirectory + "/staging"}
			},
		},
		{
			name: "special payload preserved",
			makeEntry: func(t *testing.T, root, stageDir string) {
				t.Helper()
				special := filepath.Join(root, filepath.FromSlash(stageDir), "payload", "preserved-directory")
				if err := os.MkdirAll(special, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantSynced: func(string) []string {
				return []string{internalDirectory + "/staging"}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			stageDir := internalDirectory + "/staging/txn-" + strings.Repeat("a", 64)
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(stageDir)), 0o700); err != nil {
				t.Fatal(err)
			}
			test.makeEntry(t, root, stageDir)
			var synced []string
			s.config.DirectorySync = func(dir string) error {
				synced = append(synced, dir)
				return nil
			}

			// Act.
			err := s.cleanupOrphanStages(context.Background())

			// Assert.
			want := test.wantSynced(stageDir)
			if err != nil || !reflect.DeepEqual(synced, want) {
				t.Fatalf("cleanupOrphanStages() error=%v syncs=%#v, want %#v", err, synced, want)
			}
			if info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(stageDir))); statErr != nil || !info.IsDir() {
				t.Fatalf("unproven stage changed: %v/%v", info, statErr)
			}
		})
	}
}

func TestReceiptPruneSyncsDirectoryWhenListingIsAlreadyConverged(t *testing.T) {
	// Arrange.
	_, s := adversarialStore(t, Config{})
	_ = adversarialSnapshot(t, s)
	var synced []string
	s.config.DirectorySync = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}

	// Act.
	err := s.pruneReceipts()

	// Assert.
	want := []string{internalDirectory + "/receipts"}
	if err != nil || !reflect.DeepEqual(synced, want) {
		t.Fatalf("pruneReceipts() error=%v syncs=%#v, want %#v", err, synced, want)
	}
}

func TestReceiptPruneBatchesContainingDirectorySync(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		t.Run(formatReceiptNumber(count), func(t *testing.T) {
			// Arrange.
			_, s := adversarialStore(t, Config{})
			_ = adversarialSnapshot(t, s)
			s.config.MinimumReceipts = 0
			s.config.ReceiptRetention = time.Nanosecond
			writeExpiredReceipts(t, s, count)
			var synced []string
			var posts []Step
			s.config.DirectorySync = func(dir string) error {
				synced = append(synced, dir)
				return nil
			}
			s.config.PostFault = func(step Step) error {
				posts = append(posts, step)
				return nil
			}
			batchBarriers := 0
			s.descriptorBarrier = func(phase string) error {
				if phase == "receipt_prune_batch_synced" {
					batchBarriers++
				}
				return nil
			}

			// Act.
			err := s.pruneReceipts()

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if countStrings(synced, internalDirectory+"/receipts") == 0 || batchBarriers != 1 {
				t.Fatalf("receipt syncs=%#v final_batch_barriers=%d, want a durable receipt namespace and one final batch boundary", synced, batchBarriers)
			}
			if got := countSteps(posts, StepRemove); got != count {
				t.Fatalf("receipt removals=%d posts=%#v, want %d", got, posts, count)
			}
			wantPruneBoundary := 0
			if count > 0 {
				wantPruneBoundary = 1
			}
			if got := countSteps(posts, StepReceiptPrune); got != wantPruneBoundary {
				t.Fatalf("prune boundaries=%d posts=%#v, want %d", got, posts, wantPruneBoundary)
			}
		})
	}
}

func TestReceiptPruneRemovePostFaultRecoveryUsesOneConvergenceBarrier(t *testing.T) {
	// Arrange.
	_, s := adversarialStore(t, Config{})
	s.config.MinimumReceipts = 0
	s.config.ReceiptRetention = time.Nanosecond
	writeExpiredReceipts(t, s, 1)
	crashErr := errors.New("receipt remove post-fault")
	var crashPosts []Step
	var crashSyncs []string
	s.config.DirectorySync = func(dir string) error {
		crashSyncs = append(crashSyncs, dir)
		return nil
	}
	s.config.PostFault = func(step Step) error {
		crashPosts = append(crashPosts, step)
		if step == StepRemove {
			return crashErr
		}
		return nil
	}

	// Act: the closed claim is consumed and synced before the crash-like hook.
	firstErr := s.pruneReceipts()

	// Assert: the crashing phase is terminal, then an absent-entry retry
	// supplies exactly one receipts-directory convergence barrier.
	if !errors.Is(firstErr, crashErr) || len(crashPosts) == 0 || crashPosts[len(crashPosts)-1] != StepRemove || countStrings(crashSyncs, internalDirectory+"/receipts") != 2 {
		t.Fatalf("first prune error=%v posts=%#v syncs=%#v", firstErr, crashPosts, crashSyncs)
	}
	var recoveryPosts []Step
	var recoverySyncs []string
	s.config.DirectorySync = func(dir string) error {
		recoverySyncs = append(recoverySyncs, dir)
		return nil
	}
	s.config.PostFault = func(step Step) error {
		recoveryPosts = append(recoveryPosts, step)
		return nil
	}
	if err := s.pruneReceipts(); err != nil {
		t.Fatal(err)
	}
	wantSyncs := []string{internalDirectory + "/receipts"}
	if !reflect.DeepEqual(recoverySyncs, wantSyncs) || countSteps(recoveryPosts, StepRemove) != 0 || countSteps(recoveryPosts, StepReceiptPrune) != 0 {
		t.Fatalf("recovery posts=%#v syncs=%#v, want one absent-entry barrier", recoveryPosts, recoverySyncs)
	}
}

func TestReceiptPruneRemovalOrderIsLexical(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	s.config.MinimumReceipts = 0
	s.config.ReceiptRetention = time.Nanosecond
	writeExpiredReceipts(t, s, 3)
	names := make([]string, 3)
	for i := range names {
		key := store.IdempotencyKey("exact-once-" + formatReceiptNumber(i))
		names[i] = filepath.Base(filepath.FromSlash(s.receiptPath(key)))
	}
	sort.Strings(names)
	crashErr := errors.New("stop after lexical first receipt")
	s.config.PostFault = func(step Step) error {
		if step == StepRemove {
			return crashErr
		}
		return nil
	}

	// Act.
	err := s.pruneReceipts()

	// Assert.
	if !errors.Is(err, crashErr) {
		t.Fatalf("pruneReceipts() error=%v", err)
	}
	for i, name := range names {
		_, statErr := os.Stat(filepath.Join(root, internalDirectory, "receipts", name))
		if i == 0 && !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("lexical first receipt %q was not removed: %v", name, statErr)
		}
		if i > 0 && statErr != nil {
			t.Fatalf("later receipt %q changed after terminal PostFault: %v", name, statErr)
		}
	}
}

func writeExpiredReceipts(t *testing.T, s *Store, count int) {
	t.Helper()
	base := time.Unix(0, 0).UTC()
	revision := store.Revision("sha256:" + strings.Repeat("0", 64))
	digest := "sha256:" + strings.Repeat("0", 64)
	for i := 0; i < count; i++ {
		suffix := formatReceiptNumber(i)
		receipt := store.CommitReceipt{
			FormatVersion:  store.CommitReceiptFormatVersion,
			ChangeSetID:    store.ChangeSetID("change-" + suffix),
			IdempotencyKey: store.IdempotencyKey("exact-once-" + suffix),
			RequestDigest:  digest,
			BaseRevision:   revision,
			ResultRevision: revision,
			CommitTime:     base.Add(time.Duration(i) * time.Second),
		}
		if err := s.writeReceipt(receipt); err != nil {
			t.Fatal(err)
		}
	}
}

func countStrings(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

func countSteps(values []Step, want Step) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}
