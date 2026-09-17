package okfcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/skosovsky/okf/mutation"
)

func TestMigrateTargetNoopDryRunBuildsProofWithoutOpeningStore(t *testing.T) {
	// Arrange.
	root := targetNoopMigrationFixture(t)
	before := snapshotRevisionVisibleMigrationFiles(t, root)
	recorder := &recordingMigrationPlanner{delegate: mutation.NewMigrationPlanner()}
	dependencies := productionMigrateDependencies()
	dependencies.planner = recorder
	storeOpens := 0
	dependencies.openStore = func(context.Context, string) (migrationStore, error) {
		storeOpens++
		return nil, errors.New("unexpected durable store open")
	}

	// Act.
	code, report, err := runMigrateWithDependenciesJSON(t, root, false, "auto", dependencies)
	after := snapshotRevisionVisibleMigrationFiles(t, root)
	_, metadataErr := os.Stat(filepath.Join(root, ".okf"))

	// Assert.
	if err != nil || code != 0 || storeOpens != 0 {
		t.Fatalf("dry-run error/code/store opens = %v/%d/%d, want nil/0/0", err, code, storeOpens)
	}
	if report.From != mutation.MigrationVersionV02 ||
		report.To != mutation.MigrationVersionV02 ||
		report.Transition != string(mutation.MigrationTransitionTargetNoop) ||
		!report.Noop || report.Applied || report.Receipt != nil ||
		report.Outcome != "noop" || report.PlanDigest == "" ||
		report.ProofFormatVersion != mutation.MigrationPlanProofFormatVersion ||
		report.ResolutionDigest == "" || report.Base == "" || report.Result != report.Base {
		t.Fatalf("target-noop dry-run report = %#v", report)
	}
	assertClosedMigrationOperationRequests(t, recorder, 1, 0)
	if !reflect.DeepEqual(before, after) || !os.IsNotExist(metadataErr) {
		t.Fatalf("dry-run changed files/opened store: unchanged=%t metadataErr=%v",
			reflect.DeepEqual(before, after), metadataErr)
	}
}

func TestMigrateTargetNoopApplyCommitsCASAndReplaysReceipt(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("durable filesystem store is unsupported on this platform")
	}

	// Arrange.
	root := targetNoopMigrationFixture(t)
	before := snapshotRevisionVisibleMigrationFiles(t, root)
	firstRecorder := &recordingMigrationPlanner{delegate: mutation.NewMigrationPlanner()}
	firstDependencies := productionMigrateDependencies()
	firstDependencies.planner = firstRecorder

	// Act.
	firstCode, first, firstErr := runMigrateWithDependenciesJSON(t, root, true, "auto", firstDependencies)
	afterFirst := snapshotRevisionVisibleMigrationFiles(t, root)
	secondCode, second, secondErr := runMigrateWithDependenciesJSON(
		t,
		root,
		true,
		"auto",
		productionMigrateDependencies(),
	)
	afterSecond := snapshotRevisionVisibleMigrationFiles(t, root)
	metadata, metadataErr := os.Stat(filepath.Join(root, ".okf"))

	// Assert.
	if firstErr != nil || secondErr != nil || firstCode != 0 || secondCode != 0 {
		t.Fatalf("target-noop apply/replay error/code = %v/%d, %v/%d",
			firstErr, firstCode, secondErr, secondCode)
	}
	for name, report := range map[string]migrationReport{"apply": first, "replay": second} {
		if report.From != mutation.MigrationVersionV02 ||
			report.To != mutation.MigrationVersionV02 ||
			report.Transition != string(mutation.MigrationTransitionTargetNoop) ||
			!report.Noop || !report.Applied || report.Receipt == nil ||
			report.Outcome != "applied" || report.PlanDigest == "" ||
			report.ProofFormatVersion != mutation.MigrationPlanProofFormatVersion ||
			report.ResolutionDigest == "" || report.Base == "" || report.Result != report.Base ||
			len(report.Receipt.ChangedFiles) != 0 || len(report.Receipt.ChangedRefs) != 0 ||
			report.Receipt.BaseRevision != report.Base || report.Receipt.ResultRevision != report.Result {
			t.Fatalf("%s target-noop report = %#v", name, report)
		}
	}
	assertClosedMigrationOperationRequests(t, firstRecorder, 1, 1)
	if first.PlanDigest != second.PlanDigest ||
		first.ResolutionDigest != second.ResolutionDigest ||
		!reflect.DeepEqual(first.Receipt, second.Receipt) {
		t.Fatalf("target-noop replay identity differs:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if !reflect.DeepEqual(before, afterFirst) || !reflect.DeepEqual(before, afterSecond) {
		t.Fatalf("target-noop apply changed revision-visible files:\nbefore=%#v\nafter first=%#v\nafter second=%#v",
			before, afterFirst, afterSecond)
	}
	if metadataErr != nil || !metadata.IsDir() {
		t.Fatalf("target-noop apply metadata = %#v, %v; want durable store directory", metadata, metadataErr)
	}
}

func TestMigratePlannedNoopDryRunAndApplyUseSameProof(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("durable filesystem store is unsupported on this platform")
	}

	// Arrange.
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "note.md"), "---\ntype: Knowledge\nx-keep: true\n---\nBody.\n")
	before := snapshotRevisionVisibleMigrationFiles(t, root)
	dryDependencies := productionMigrateDependencies()
	dryStoreOpens := 0
	dryDependencies.openStore = func(context.Context, string) (migrationStore, error) {
		dryStoreOpens++
		return nil, errors.New("unexpected durable store open")
	}

	// Act.
	dryCode, dry, dryErr := runMigrateWithDependenciesJSON(t, root, false, "0.1", dryDependencies)
	afterDry := snapshotRevisionVisibleMigrationFiles(t, root)
	applyCode, applied, applyErr := runMigrateWithDependenciesJSON(
		t,
		root,
		true,
		"0.1",
		productionMigrateDependencies(),
	)
	afterApply := snapshotRevisionVisibleMigrationFiles(t, root)

	// Assert.
	if dryErr != nil || applyErr != nil || dryCode != 0 || applyCode != 0 || dryStoreOpens != 0 {
		t.Fatalf("planned-noop dry/apply error/code/store opens = %v/%d/%d, %v/%d",
			dryErr, dryCode, dryStoreOpens, applyErr, applyCode)
	}
	if !dry.Noop || dry.Applied || dry.Receipt != nil || dry.Outcome != "noop" ||
		dry.Transition != string(mutation.MigrationTransitionV01ToV02) ||
		dry.PlanDigest == "" || dry.ResolutionDigest == "" || dry.Base == "" || dry.Result != dry.Base {
		t.Fatalf("planned-noop dry-run report = %#v", dry)
	}
	if !applied.Noop || !applied.Applied || applied.Receipt == nil || applied.Outcome != "applied" ||
		applied.PlanDigest != dry.PlanDigest || applied.ResolutionDigest != dry.ResolutionDigest ||
		applied.Base != dry.Base || applied.Result != dry.Result ||
		len(applied.Receipt.ChangedFiles) != 0 || len(applied.Receipt.ChangedRefs) != 0 {
		t.Fatalf("planned-noop apply report = %#v", applied)
	}
	if !reflect.DeepEqual(before, afterDry) || !reflect.DeepEqual(before, afterApply) {
		t.Fatalf("planned-noop changed revision-visible files:\nbefore=%#v\nafter dry=%#v\nafter apply=%#v",
			before, afterDry, afterApply)
	}
}

func TestMigrateApplyRejectsStalePreview(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("durable filesystem store is unsupported on this platform")
	}

	// Arrange.
	root := t.TempDir()
	indexPath := filepath.Join(root, "index.md")
	notePath := filepath.Join(root, "note.md")
	writeFixtureFile(t, indexPath,
		"---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Note](note.md)\n")
	writeFixtureFile(t, notePath,
		"---\ntype: Knowledge\n---\nBody.\n")
	indexBefore, _ := os.ReadFile(indexPath)
	noteBefore, _ := os.ReadFile(notePath)
	dependencies := productionMigrateDependencies()
	productionOpenStore := dependencies.openStore
	openCalls := 0
	dependencies.openStore = func(ctx context.Context, path string) (migrationStore, error) {
		openCalls++
		writeFixtureFile(t, filepath.Join(root, "concurrent.md"), "concurrent R2 bytes\n")
		return productionOpenStore(ctx, path)
	}

	// Act.
	code, report, err := runMigrateWithDependenciesJSON(t, root, true, "auto", dependencies)
	indexAfter, indexErr := os.ReadFile(indexPath)
	noteAfter, noteErr := os.ReadFile(notePath)
	concurrentAfter, concurrentErr := os.ReadFile(filepath.Join(root, "concurrent.md"))

	// Assert.
	if err != nil || code != 1 || openCalls != 1 {
		t.Fatalf("stale apply error/code/store opens = %v/%d/%d, want nil/1/1", err, code, openCalls)
	}
	if report.Outcome != "blocked" || report.Noop || report.Applied || report.Receipt != nil ||
		report.PlanDigest == "" || report.ResolutionDigest == "" ||
		len(report.Blockers) != 1 || report.Blockers[0].Code != "migration_apply_conflict" {
		t.Fatalf("stale apply report = %#v", report)
	}
	if indexErr != nil || noteErr != nil || concurrentErr != nil ||
		!bytes.Equal(indexBefore, indexAfter) || !bytes.Equal(noteBefore, noteAfter) ||
		string(concurrentAfter) != "concurrent R2 bytes\n" {
		t.Fatalf("stale apply publication = index %v/%t note %v/%t concurrent %v/%q",
			indexErr, bytes.Equal(indexBefore, indexAfter),
			noteErr, bytes.Equal(noteBefore, noteAfter), concurrentErr, concurrentAfter)
	}
}

func targetNoopMigrationFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"),
		"---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [Note](note.md)\n")
	writeFixtureFile(t, filepath.Join(root, "note.md"),
		"---\ntype: Knowledge\nx-keep: true\n---\nBody.\n")
	return root
}

func snapshotRevisionVisibleMigrationFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".okf" || filepath.Dir(relative) == ".okf" ||
			len(relative) > len(".okf") && relative[:len(".okf")+1] == ".okf"+string(filepath.Separator) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
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
		t.Fatalf("snapshot revision-visible files: %v", err)
	}
	return files
}
