package fs_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

func TestMigrationApplyCrashMatrixRecoversExactPreOrPostAndReplaysReceipt(t *testing.T) {
	// Arrange. Derive the supported boundary/occurrence set from a complete
	// successful apply. This keeps the matrix synchronized with the real
	// durability harness and includes repeated per-file boundaries.
	ctx := context.Background()
	request := migrationDurabilityRequest("occurrence-matrix")
	planner := mutation.NewMigrationPlanner()
	baselineRoot := t.TempDir()
	writeMigrationFixture(t, baselineRoot)
	var points []migrationFaultPoint
	preOccurrences := map[storefs.Step]int{}
	postOccurrences := map[storefs.Step]int{}
	armed := false
	journalRenamed := false
	journalDurable := false
	baseline, err := storefs.OpenWithoutPhysicalSyncForTesting(baselineRoot, storefs.Config{
		Fault: func(step storefs.Step) error {
			if armed {
				preOccurrences[step]++
				points = append(points, migrationFaultPoint{
					Phase:          "pre",
					Step:           step,
					Occurrence:     preOccurrences[step],
					JournalDurable: journalDurable,
				})
			}
			return nil
		},
		PostFault: func(step storefs.Step) error {
			if armed {
				postOccurrences[step]++
				if step == storefs.StepJournalRename {
					journalRenamed = true
				}
				if step == storefs.StepJournalDirectorySync && journalRenamed {
					journalDurable = true
				}
				points = append(points, migrationFaultPoint{
					Phase:          "post",
					Step:           step,
					Occurrence:     postOccurrences[step],
					JournalDurable: journalDurable,
				})
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = baseline.Close() })
	base, err := baseline.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := planner.ResolveSource(ctx, base, mutation.MigrationSourceOptions{
		RequestedSelector: request.FromVersion,
	})
	if err != nil {
		t.Fatalf("ResolveSource() error = %v", err)
	}
	preview, err := planner.Preview(ctx, base, resolution, request)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Proof.FormatVersion != mutation.MigrationPlanProofFormatVersion ||
		preview.Proof.ResolutionDigest == "" ||
		!reflect.DeepEqual(preview.Resolution, resolution) {
		t.Fatalf("preview did not preserve the frozen v2 resolution proof: resolution=%#v proof=%#v", preview.Resolution, preview.Proof)
	}
	before := readMigrationSource(t, base)
	after := readMigrationSource(t, preview.Staged)
	apply := mutation.MigrationApplyRequest{
		Request:    request,
		Resolution: resolution,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
		Options: store.CommitOptions{
			IdempotencyKey: "migration-crash-occurrence-matrix",
		},
	}
	armed = true
	baselineReceipt, err := planner.Apply(ctx, baseline, apply)
	armed = false
	if err != nil {
		t.Fatalf("successful trace Apply() error = %v", err)
	}
	wantIdempotencyKey, err := mutation.PlanDigestIdempotencyKey(
		"migration:v0.1-to-v0.2",
		preview.PlanDigest,
		apply.Options.IdempotencyKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := readMigrationFilesystem(t, baselineRoot); !reflect.DeepEqual(got, after) {
		t.Fatalf("successful trace final files differ from exact preview post-state")
	}
	assertMigrationFaultTraceCoverage(t, points, preOccurrences, postOccurrences)
	t.Logf("derived %d pre/post durability fault occurrences", len(points))
	points = selectMigrationAdapterFaultPoints(t, points, preOccurrences, postOccurrences)
	t.Logf("retained %d migration-adapter durability rows", len(points))

	for _, point := range points {
		point := point
		t.Run(fmt.Sprintf("%s/%s/%d", point.Phase, point.Step, point.Occurrence), func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeMigrationFixture(t, root)
			armed, fired := false, false
			preSeen := map[storefs.Step]int{}
			postSeen := map[storefs.Step]int{}
			wantErr := errors.New("injected migration crash at " + point.String())
			inject := func(phase string, step storefs.Step, seen map[storefs.Step]int) error {
				if !armed {
					return nil
				}
				seen[step]++
				if fired || phase != point.Phase || step != point.Step || seen[step] != point.Occurrence {
					return nil
				}
				fired = true
				return wantErr
			}
			destination, err := storefs.OpenWithoutPhysicalSyncForTesting(root, storefs.Config{
				Fault: func(got storefs.Step) error {
					return inject("pre", got, preSeen)
				},
				PostFault: func(got storefs.Step) error {
					return inject("post", got, postSeen)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = destination.Close() })
			pointBase, err := destination.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if pointBase.Revision() != preview.Proof.BaseRevision {
				t.Fatalf("fresh fixture revision = %s, want proof base %s", pointBase.Revision(), preview.Proof.BaseRevision)
			}

			// Act.
			armed = true
			_, applyErr := planner.Apply(ctx, destination, apply)
			armed = false
			interrupted := readMigrationFilesystem(t, root)
			if closeErr := destination.Close(); closeErr != nil {
				t.Fatalf("Close() after injected crash error = %v", closeErr)
			}
			if !point.JournalDurable {
				removeMigrationNondurableJournals(t, root)
			}
			reopened, reopenErr := storefs.OpenWithoutPhysicalSyncForTesting(root, storefs.Config{})
			if reopenErr != nil {
				t.Fatalf("Open() recovery error = %v", reopenErr)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			recovered, snapshotErr := reopened.Snapshot(ctx)
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			recoveredFiles := readMigrationSource(t, recovered)
			firstReceipt, firstReplayErr := planner.Apply(ctx, reopened, apply)
			secondReceipt, secondReplayErr := planner.Apply(ctx, reopened, apply)
			final, finalErr := reopened.Snapshot(ctx)

			// Assert.
			if !errors.Is(applyErr, wantErr) || !fired {
				t.Fatalf("Apply() error = %v, fault %s fired = %t", applyErr, point, fired)
			}
			if !migrationRootIndexStateIsForward(before, after, interrupted) {
				t.Fatalf("interrupted state at %s published root index before non-root post-state", point)
			}
			wantRecovered := before
			if point.JournalDurable {
				wantRecovered = after
			}
			if !reflect.DeepEqual(recoveredFiles, wantRecovered) {
				t.Fatalf("recovered files at %s differ from exact expected state (journal_durable=%t)", point, point.JournalDurable)
			}
			if firstReplayErr != nil || secondReplayErr != nil {
				t.Fatalf("receipt replay at %s errors = first:%v second:%v", point, firstReplayErr, secondReplayErr)
			}
			if !reflect.DeepEqual(firstReceipt, secondReceipt) {
				t.Fatalf("receipt replay at %s differs:\nfirst:  %#v\nsecond: %#v", point, firstReceipt, secondReceipt)
			}
			if firstReceipt.ChangeSetID != request.ID ||
				firstReceipt.IdempotencyKey != wantIdempotencyKey ||
				firstReceipt.RequestDigest != baselineReceipt.RequestDigest ||
				firstReceipt.BaseRevision != preview.Proof.BaseRevision ||
				firstReceipt.ResultRevision != preview.Proof.ResultRevision ||
				!reflect.DeepEqual(firstReceipt.ChangedFiles, preview.Proof.ChangedFiles) ||
				!reflect.DeepEqual(firstReceipt.ChangedRefs, preview.Proof.ChangedRefs) {
				t.Fatalf("receipt at %s escaped exact successful/proof envelope:\nreceipt: %#v\nbaseline: %#v\nproof: %#v", point, firstReceipt, baselineReceipt, preview.Proof)
			}
			if finalErr != nil {
				t.Fatal(finalErr)
			}
			if got := readMigrationSource(t, final); !reflect.DeepEqual(got, after) {
				t.Fatalf("final files at %s differ from exact preview post-state", point)
			}
		})
	}
}

func TestMigrationApply_ZeroByteAssetRecoversAfterDurableJournalCrash(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	root := t.TempDir()
	writeMigrationFixture(t, root)
	request := migrationDurabilityRequest("zero-byte-recovery")
	request.Migration.Computations[0].Asset.Content = []byte{}
	planner := mutation.NewMigrationPlanner()
	crashErr := errors.New("injected crash after durable zero-byte journal")
	armed := false
	journalRenamed := false
	destination, err := storefs.OpenWithoutPhysicalSyncForTesting(root, storefs.Config{
		PostFault: func(step storefs.Step) error {
			if !armed {
				return nil
			}
			if step == storefs.StepJournalRename {
				journalRenamed = true
				return nil
			}
			if step == storefs.StepJournalDirectorySync && journalRenamed {
				return crashErr
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := destination.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := planner.ResolveSource(ctx, base, mutation.MigrationSourceOptions{
		RequestedSelector: request.FromVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := planner.Preview(ctx, base, resolution, request)
	if err != nil {
		t.Fatalf("Preview() error = %v; blockers = %#v", err, preview.Blockers)
	}
	apply := mutation.MigrationApplyRequest{
		Request:    request,
		Resolution: resolution,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
		Options: store.CommitOptions{
			IdempotencyKey: "migration-zero-byte-recovery",
		},
	}

	// Act.
	armed = true
	_, applyErr := planner.Apply(ctx, destination, apply)
	armed = false
	closeErr := destination.Close()
	reopened, reopenErr := storefs.OpenWithoutPhysicalSyncForTesting(root, storefs.Config{})
	if reopenErr == nil {
		t.Cleanup(func() { _ = reopened.Close() })
	}
	var recovered store.Snapshot
	var snapshotErr error
	var asset []byte
	var assetErr error
	var paths []string
	var pathsErr error
	var targetResolution mutation.MigrationSourceResolution
	var resolutionErr error
	var replay mutation.MigrationPreview
	var replayErr error
	if reopenErr == nil {
		recovered, snapshotErr = reopened.Snapshot(ctx)
	}
	if snapshotErr == nil && recovered != nil {
		asset, assetErr = recovered.ReadFile(ctx, "references/query.sql")
		paths, pathsErr = recovered.Paths(ctx)
		targetResolution, resolutionErr = planner.ResolveSource(
			ctx,
			recovered,
			mutation.MigrationSourceOptions{RequestedSelector: mutation.MigrationSelectorAuto},
		)
	}
	if resolutionErr == nil && recovered != nil {
		replay, replayErr = planner.Preview(ctx, recovered, targetResolution, request)
	}

	// Assert.
	if !errors.Is(applyErr, crashErr) || !journalRenamed {
		t.Fatalf("Apply() error = %v, journal renamed = %t", applyErr, journalRenamed)
	}
	if closeErr != nil || reopenErr != nil || snapshotErr != nil ||
		assetErr != nil || pathsErr != nil || resolutionErr != nil || replayErr != nil {
		t.Fatalf(
			"close/reopen/snapshot/asset/paths/resolve/replay errors = %v/%v/%v/%v/%v/%v/%v",
			closeErr,
			reopenErr,
			snapshotErr,
			assetErr,
			pathsErr,
			resolutionErr,
			replayErr,
		)
	}
	if len(asset) != 0 {
		t.Fatalf("recovered zero-byte asset = %q", asset)
	}
	present := false
	for _, path := range paths {
		if path == "references/query.sql" {
			present = true
			break
		}
	}
	if !present {
		t.Fatalf("recovered zero-byte asset is absent from snapshot paths: %#v", paths)
	}
	if recovered.Revision() != preview.Proof.ResultRevision {
		t.Fatalf("recovered revision = %s, want %s", recovered.Revision(), preview.Proof.ResultRevision)
	}
	if targetResolution.Transition != mutation.MigrationTransitionTargetNoop ||
		len(replay.Preview.Writes) != 0 ||
		replay.Preview.BaseRevision != replay.Preview.ResultRevision {
		t.Fatalf("recovered zero-byte migration is not target-noop: resolution=%#v preview=%#v", targetResolution, replay.Preview)
	}
}

type migrationFaultPoint struct {
	Phase          string
	Step           storefs.Step
	Occurrence     int
	JournalDurable bool
}

// selectMigrationAdapterFaultPoints keeps only behavior observable through the
// MigrationPlanner adapter. Store R4 named matrices own private claim,
// witness, binding, sentinel, staging and scratch crash permutations.
func selectMigrationAdapterFaultPoints(t *testing.T, points []migrationFaultPoint, pre, post map[storefs.Step]int) []migrationFaultPoint {
	t.Helper()
	var selected []migrationFaultPoint
	seenPreJournal := false
	seenDurable := false
	seenNonRoot := false
	seenRootLast := false
	seenReceipt := false
	for _, point := range points {
		last := pre[point.Step]
		if point.Phase == "post" {
			last = post[point.Step]
		}
		keep := false
		switch point.Step {
		case storefs.StepStageFileWrite:
			keep = point.Occurrence == 1
			seenPreJournal = seenPreJournal || keep
		case storefs.StepJournalRename:
			keep = point.Occurrence == 1
		case storefs.StepJournalDirectorySync:
			keep = point.Occurrence == 1 || point.Occurrence == last
			seenDurable = seenDurable || keep && point.JournalDurable
		case storefs.StepFileWrite, storefs.StepFileSync, storefs.StepFileClose, storefs.StepRename, storefs.StepDirectorySync:
			keep = point.Occurrence == 1 || point.Occurrence == last
			seenNonRoot = seenNonRoot || keep && point.Occurrence == 1
			seenRootLast = seenRootLast || keep && point.Occurrence == last
		case storefs.StepReceiptWrite, storefs.StepReceiptSync, storefs.StepReceiptRename, storefs.StepReceiptDirectorySync:
			keep = true
			seenReceipt = true
		}
		if keep {
			selected = append(selected, point)
		}
	}
	if len(selected) < 30 || len(selected) > 45 || !seenPreJournal || !seenDurable || !seenNonRoot || !seenRootLast || !seenReceipt {
		t.Fatalf("migration adapter selection rows=%d prejournal=%t durable=%t nonroot=%t rootlast=%t receipt=%t", len(selected), seenPreJournal, seenDurable, seenNonRoot, seenRootLast, seenReceipt)
	}
	return selected
}

func (p migrationFaultPoint) String() string {
	return fmt.Sprintf("%s/%s/%d", p.Phase, p.Step, p.Occurrence)
}

func assertMigrationFaultTraceCoverage(t *testing.T, points []migrationFaultPoint, pre, post map[storefs.Step]int) {
	t.Helper()
	if len(points) == 0 {
		t.Fatal("successful migration exposed no durability fault boundaries")
	}
	total := 0
	for _, count := range pre {
		total += count
	}
	for _, count := range post {
		total += count
	}
	if len(points) != total {
		t.Fatalf("successful trace points = %d, want every supported pre/post occurrence = %d", len(points), total)
	}
	for _, step := range []storefs.Step{
		storefs.StepStageFileWrite,
		storefs.StepStageFileSync,
		storefs.StepStageFileClose,
		storefs.StepStageRename,
		storefs.StepStageDirectorySync,
		storefs.StepFileWrite,
		storefs.StepFileSync,
		storefs.StepFileClose,
		storefs.StepRename,
		storefs.StepDirectorySync,
	} {
		if pre[step] < 2 || post[step] < 2 {
			t.Fatalf("successful trace does not cover repeated %s boundaries: pre:%d post:%d", step, pre[step], post[step])
		}
	}
}

func migrationDurabilityRequest(label string) mutation.MigrationRequest {
	suffix := strings.ReplaceAll(label, "_", "-")
	return mutation.MigrationRequest{
		ID:          store.ChangeSetID("migration-crash-" + suffix),
		Actor:       "operator",
		FromVersion: mutation.MigrationVersionV01,
		ToVersion:   mutation.MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy: "process:migration",
			GeneratedAt: []store.MigrationGeneratedAt{{
				Path: "query.md",
				At:   "2026-06-25T10:00:00Z",
			}},
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
			Computations: []store.ComputationMigration{{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:            store.AttestedComputationModeFile,
					Runtime:         "sql",
					ComputationPath: "references/query.sql",
					Executor: store.ExecutorContract{
						Resource: "executors/sql.md",
						Receipt:  []string{"rows"},
					},
					Attester: store.AttesterContract{Resource: "attesters/sql.md"},
				},
				Asset: &store.MigrationAsset{
					Path:    "references/query.sql",
					Content: []byte("SELECT 1;\n"),
				},
			}},
		},
	}
}

func writeMigrationFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"index.md":       "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n- [Beta](nested/beta.md)\n- [Query](query.md)\n",
		"alpha.md":       "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nAlpha.\n",
		"nested/beta.md": "---\ntype: Knowledge\ntimestamp: 2026-06-26T09:00:00Z\n---\n\nBeta.\n",
		"query.md":       "---\ntype: Knowledge\n---\n\nQuery.\n",
	}
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func readMigrationFilesystem(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".okf" && entry.IsDir() {
			return filepath.SkipDir
		}
		if relative == "." || entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func removeMigrationNondurableJournals(t *testing.T, root string) {
	t.Helper()
	transactions := filepath.Join(root, ".okf", "transactions")
	entries, err := os.ReadDir(transactions)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if err := os.Remove(filepath.Join(transactions, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
}

func migrationRootIndexStateIsForward(base, result, current map[string]string) bool {
	resultRoot, resultHasRoot := result["index.md"]
	currentRoot, currentHasRoot := current["index.md"]
	baseRoot, baseHasRoot := base["index.md"]
	rootChanged := baseHasRoot != resultHasRoot || baseRoot != resultRoot
	rootAtResult := currentHasRoot == resultHasRoot && currentRoot == resultRoot
	if !rootChanged || !rootAtResult {
		return true
	}
	for path, content := range result {
		if path != "index.md" && current[path] != content {
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

func readMigrationSource(t *testing.T, source bundle.Source) map[string]string {
	t.Helper()
	paths, err := source.Paths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(paths))
	for _, path := range paths {
		content, err := source.ReadFile(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = string(content)
	}
	return files
}
