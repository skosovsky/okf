package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type indexPlanningErrorSource struct {
	fail  map[string]error
	reads []string
}

func indexBatchCanonicalCleanupTrace(t *testing.T, rootPath string) []string {
	t.Helper()
	root, err := openRootWithoutSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	discoveries, err := discoverIndexTransactionNamespace(root)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareIndexRecoveryWithSharedInventory(
		root,
		discoveries,
		indexPublishHooks{},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	if prepared.batch == nil {
		t.Fatal("canonical cleanup planner did not prepare a batch")
	}
	steps, err := prepared.batch.indexBatchTerminalCleanupPlan()
	if err != nil {
		t.Fatal(err)
	}
	trace := make([]string, 0, len(steps))
	lastBoundary := indexBatchTerminalCleanupNonProof
	manifestSteps := 0
	for _, step := range steps {
		if step.item == nil || step.boundary < lastBoundary {
			t.Fatalf("non-canonical cleanup step: %#v", step)
		}
		lastBoundary = step.boundary
		if step.boundary == indexBatchTerminalCleanupManifest {
			manifestSteps++
			if step.item.discovery.kind != indexArtifactManifest {
				t.Fatalf("manifest boundary step = %#v", step.item.discovery)
			}
		}
		trace = append(
			trace,
			step.item.destination.output.relative+":"+
				string(step.item.discovery.kind),
		)
	}
	if prepared.batch.manifestBoundary != nil && manifestSteps != 1 {
		t.Fatalf(
			"canonical cleanup manifest steps=%d, want one",
			manifestSteps,
		)
	}
	return trace
}

func (s *indexPlanningErrorSource) Paths(context.Context) ([]string, error) {
	return nil, nil
}

func (s *indexPlanningErrorSource) ReadFile(_ context.Context, name string) ([]byte, error) {
	s.reads = append(s.reads, name)
	if err := s.fail[name]; err != nil {
		return nil, err
	}
	return []byte("# valid\n"), nil
}

func TestRegenerateIndexesRejectsNestedSymlinkDestinationBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.md")
	externalBefore := []byte("external bytes must remain unchanged\n")
	if err := os.WriteFile(external, externalBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	writeIndexDoc(t, root, "other/b.md", "Note", "B", "Beta.")
	if err := os.Symlink(external, filepath.Join(root, "nested", indexFilename)); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert.
	const wantError = `invalid index destination "nested/index.md": destination is a symlink`
	if err == nil || !strings.Contains(err.Error(), wantError) {
		t.Fatalf("RegenerateIndexes() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	if after, readErr := os.ReadFile(external); readErr != nil || !bytes.Equal(after, externalBefore) {
		t.Fatalf("external bytes = %q, error = %v; want %q", after, readErr, externalBefore)
	}
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	assertPathDoesNotExist(t, filepath.Join(root, "other", indexFilename))
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesRejectsDanglingNestedSymlinkDestination(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination := filepath.Join(root, "nested", indexFilename)
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing.md"), destination); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert.
	const wantError = `invalid index destination "nested/index.md": destination is a symlink`
	if err == nil || !strings.Contains(err.Error(), wantError) {
		t.Fatalf("RegenerateIndexes() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesSelectorUsesOnlyRootDeclarationAndDoesNotTraverseNestedSymlink(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	rootBefore := []byte("---\nokf_version: \"0.2\"\n---\n\n# Root before\n")
	if err := os.WriteFile(filepath.Join(root, indexFilename), rootBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	external := filepath.Join(t.TempDir(), "external.md")
	externalBefore := []byte("---\nokf_version: [malformed\n---\n\n# External\n")
	if err := os.WriteFile(external, externalBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "nested", indexFilename)); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	// Act.
	written, err := regenerateIndexesWithSelectorForTest(root, "0.2")

	// Assert.
	const wantError = `invalid index destination "nested/index.md": destination is a symlink`
	if err == nil || err.Error() != wantError {
		t.Fatalf("RegenerateIndexesWithSelector() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	assertFileBytes(t, filepath.Join(root, indexFilename), rootBefore)
	assertFileBytes(t, external, externalBefore)
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesMalformedChildFailsClosedDeterministically(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	rootBefore := []byte("# Root before\n")
	nestedBefore := []byte("# Nested before\n")
	if err := os.WriteFile(filepath.Join(root, indexFilename), rootBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "nested/alpha.md", "---\ntype: Note\n")
	writeFile(t, root, "nested/zeta.md", "---\ntype: Note\n")
	if err := os.WriteFile(filepath.Join(root, "nested", indexFilename), nestedBefore, 0o640); err != nil {
		t.Fatal(err)
	}

	// Act.
	const repetitions = 16
	errorsSeen := make([]string, 0, repetitions)
	for run := 0; run < repetitions; run++ {
		written, err := regenerateIndexesForTest(root)
		if err == nil {
			t.Fatalf("run %d: RegenerateIndexes() error = nil", run)
		}
		if written != nil {
			t.Fatalf("run %d: written = %#v, want nil", run, written)
		}
		if !errors.Is(err, ErrUnterminatedFrontmatter) {
			t.Fatalf("run %d: error = %v, want ErrUnterminatedFrontmatter", run, err)
		}
		errorsSeen = append(errorsSeen, err.Error())
	}

	// Assert.
	const wantError = `parse index document "nested/alpha.md": unterminated frontmatter`
	for run, got := range errorsSeen {
		if got != wantError {
			t.Fatalf("run %d: error = %q, want %q", run, got, wantError)
		}
	}
	assertFileBytes(t, filepath.Join(root, indexFilename), rootBefore)
	assertFileBytes(t, filepath.Join(root, "nested", indexFilename), nestedBefore)
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesPlanningUsesGlobalLexicalFirstErrorAcrossDepths(t *testing.T) {
	t.Parallel()

	creationOrders := [][]string{
		{"z/deep/b.md", "a.md"},
		{"a.md", "z/deep/b.md"},
	}
	for orderIndex, creationOrder := range creationOrders {
		orderIndex, creationOrder := orderIndex, creationOrder
		t.Run(itoa(orderIndex), func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			rootBefore := []byte("# Root before\n")
			if err := os.WriteFile(filepath.Join(root, indexFilename), rootBefore, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, relative := range creationOrder {
				writeFile(t, root, relative, "---\ntype: Note\n")
			}

			// Act.
			const repetitions = 16
			errorsSeen := make([]string, 0, repetitions)
			for run := 0; run < repetitions; run++ {
				written, err := regenerateIndexesForTest(root)
				if err == nil {
					t.Fatalf("run %d: RegenerateIndexes() error = nil", run)
				}
				if written != nil {
					t.Fatalf("run %d: written = %#v, want nil", run, written)
				}
				errorsSeen = append(errorsSeen, err.Error())
			}

			// Assert.
			const wantError = `parse index document "a.md": unterminated frontmatter`
			for run, got := range errorsSeen {
				if got != wantError {
					t.Fatalf("run %d: error = %q, want %q", run, got, wantError)
				}
			}
			assertFileBytes(t, filepath.Join(root, indexFilename), rootBefore)
			assertPathDoesNotExist(t, filepath.Join(root, "z", indexFilename))
			assertPathDoesNotExist(t, filepath.Join(root, "z", "deep", indexFilename))
			assertNoIndexTransactionArtifacts(t, root)
		})
	}
}

func TestPreloadIndexDocumentsRejectsLexicalPathBeforeReads(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := &indexPlanningErrorSource{}
	files := []string{"z.md", "../a.md"}

	// Act.
	documents, err := preloadIndexDocuments(source, files)

	// Assert.
	const wantError = `invalid index planning path "../a.md": invalid revision path "../a.md"`
	if err == nil || err.Error() != wantError {
		t.Fatalf("preloadIndexDocuments() error = %v, want %q", err, wantError)
	}
	if documents != nil {
		t.Fatalf("documents = %#v, want nil", documents)
	}
	if len(source.reads) != 0 {
		t.Fatalf("reads = %#v, want none", source.reads)
	}
}

func TestIndexEntriesReadFailureUsesSortedStableFirstError(t *testing.T) {
	t.Parallel()

	permutations := [][]string{
		{"zeta.md", "alpha.md"},
		{"alpha.md", "zeta.md"},
	}
	for permutation, files := range permutations {
		// Arrange.
		injected := errors.New("provider-specific detail")
		source := &indexPlanningErrorSource{
			fail: map[string]error{
				"alpha.md": injected,
				"zeta.md":  injected,
			},
		}

		// Act.
		entries, err := indexEntriesForDirectory(source, "", files, nil)

		// Assert.
		const wantError = `read index document "alpha.md"`
		if err == nil || err.Error() != wantError || !errors.Is(err, injected) {
			t.Fatalf("permutation %d: error = %v, want %q wrapping injected", permutation, err, wantError)
		}
		if entries != nil {
			t.Fatalf("permutation %d: entries = %#v, want nil", permutation, entries)
		}
		if wantReads := []string{"alpha.md"}; !reflect.DeepEqual(source.reads, wantReads) {
			t.Fatalf("permutation %d: reads = %#v, want %#v", permutation, source.reads, wantReads)
		}
	}
}

func TestRegenerateIndexesPreflightUsesDeterministicDestinationOrder(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
	writeIndexDoc(t, root, "z/two.md", "Note", "Two", "Second.")
	if err := os.Mkdir(filepath.Join(root, "a", indexFilename), 0o755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.md")
	if err := os.WriteFile(external, []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "z", indexFilename)); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	// Act.
	const repetitions = 16
	errorsSeen := make([]string, 0, repetitions)
	for run := 0; run < repetitions; run++ {
		written, err := regenerateIndexesForTest(root)
		if err == nil {
			t.Fatalf("run %d: RegenerateIndexes() error = nil", run)
		}
		if written != nil {
			t.Fatalf("run %d: written = %#v, want nil", run, written)
		}
		errorsSeen = append(errorsSeen, err.Error())
	}

	// Assert.
	const wantError = `invalid index destination "a/index.md": destination is a directory`
	for run, got := range errorsSeen {
		if got != wantError {
			t.Fatalf("run %d: error = %q, want %q", run, got, wantError)
		}
	}
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesLaterConflictLeavesEveryEarlierDestinationUnchanged(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	deepBefore := []byte("# Deep before\n")
	nestedBefore := []byte("# Nested before\n")
	rootBefore := []byte("# Root before\n")
	writeIndexDoc(t, root, "a/deep/one.md", "Note", "One", "First.")
	writeIndexDoc(t, root, "z/two.md", "Note", "Two", "Second.")
	if err := os.WriteFile(filepath.Join(root, "a", "deep", indexFilename), deepBefore, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", indexFilename), nestedBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, indexFilename), rootBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "z", indexFilename), 0o755); err != nil {
		t.Fatal(err)
	}

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert.
	const wantError = `invalid index destination "z/index.md": destination is a directory`
	if err == nil || err.Error() != wantError {
		t.Fatalf("RegenerateIndexes() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	assertFileBytes(t, filepath.Join(root, "a", "deep", indexFilename), deepBefore)
	assertFileBytes(t, filepath.Join(root, "a", indexFilename), nestedBefore)
	assertFileBytes(t, filepath.Join(root, indexFilename), rootBefore)
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesRejectsConcurrentLeafSwapWithoutFollowingIt(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.md")
	externalBefore := []byte("external bytes\n")
	if err := os.WriteFile(external, externalBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	nestedIndex := filepath.Join(root, "nested", indexFilename)
	originalIndex := filepath.Join(root, "nested", "index.original")
	originalBefore := []byte("# Original nested index\n")
	if err := os.WriteFile(nestedIndex, originalBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	swapped := false
	hooks := indexPublishHooks{
		beforePublish: func(relative string, _ int) error {
			if relative != "nested/index.md" {
				return nil
			}
			if err := os.Rename(nestedIndex, originalIndex); err != nil {
				return err
			}
			if err := os.Symlink(external, nestedIndex); err != nil {
				return err
			}
			swapped = true
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	const wantError = `invalid index destination "nested/index.md": destination changed after preflight`
	if !swapped {
		t.Fatal("adversarial swap did not run")
	}
	if err == nil || !strings.Contains(err.Error(), wantError) {
		t.Fatalf("regenerateIndexesWithHooks() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	assertFileBytes(t, external, externalBefore)
	assertFileBytes(t, originalIndex, originalBefore)
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	if artifacts := indexTransactionArtifacts(t, root); len(artifacts) == 0 {
		t.Fatal("recovery evidence was removed after a concurrent leaf swap")
	}
}

func TestRegenerateIndexesRejectsConcurrentAncestorSwapAndCleansStages(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	external := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	nested := filepath.Join(root, "nested")
	parked := filepath.Join(root, "nested.parked")
	swapped := false
	hooks := indexPublishHooks{
		beforePublish: func(relative string, _ int) error {
			if relative != "nested/index.md" {
				return nil
			}
			if err := os.Rename(nested, parked); err != nil {
				return err
			}
			if err := os.Symlink(external, nested); err != nil {
				return err
			}
			swapped = true
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	const wantError = `invalid index destination "nested/index.md": destination ancestor changed after preflight`
	if !swapped {
		t.Fatal("adversarial ancestor swap did not run")
	}
	if err == nil || !strings.Contains(err.Error(), wantError) {
		t.Fatalf("regenerateIndexesWithHooks() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	assertPathDoesNotExist(t, filepath.Join(external, indexFilename))
	assertPathDoesNotExist(t, filepath.Join(parked, indexFilename))
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	if artifacts := indexTransactionArtifacts(t, parked); len(artifacts) == 0 {
		t.Fatal("recovery evidence was removed after a concurrent ancestor swap")
	}
}

func TestRegenerateIndexesRollsBackInjectedMidPublishFailure(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
	writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
	before := map[string][]byte{
		"a/index.md": []byte("# A before\n"),
		"b/index.md": []byte("# B before\n"),
		"index.md":   []byte("# Root before\n"),
	}
	for relative, data := range before {
		filename := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.WriteFile(filename, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	injected := errors.New("injected mid-publish failure")
	var order []string
	hooks := indexPublishHooks{
		beforePublish: func(relative string, ordinal int) error {
			order = append(order, relative)
			if ordinal == 1 {
				return injected
			}
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !errors.Is(err, injected) {
		t.Fatalf("regenerateIndexesWithHooks() error = %v, want injected error", err)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	if want := []string{"a/index.md", "b/index.md"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("publish order before failure = %#v, want %#v", order, want)
	}
	for relative, want := range before {
		assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), want)
	}
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesPublishesRootLastAndPreservesExistingModes(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
	writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
	for relative, mode := range map[string]fs.FileMode{
		"a/index.md": 0o600,
		"b/index.md": 0o640,
		"index.md":   0o604,
	} {
		filename := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.WriteFile(filename, []byte("# Before\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filename, mode); err != nil {
			t.Fatal(err)
		}
	}
	var order []string
	hooks := indexPublishHooks{
		beforePublish: func(relative string, _ int) error {
			order = append(order, relative)
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if err != nil {
		t.Fatalf("regenerateIndexesWithHooks() error = %v", err)
	}
	if want := []string{"a/index.md", "b/index.md", "index.md"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("publish order = %#v, want %#v", order, want)
	}
	if len(written) != len(order) || filepath.Base(written[len(written)-1]) != indexFilename ||
		written[len(written)-1] != filepath.Join(root, indexFilename) {
		t.Fatalf("written = %#v, want root index last", written)
	}
	for relative, want := range map[string]fs.FileMode{
		"a/index.md": 0o600,
		"b/index.md": 0o640,
		"index.md":   0o604,
	} {
		info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %04o, want %04o", relative, got, want)
		}
	}
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesRejectsStageSymlinkSwapBeforeInstall(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.md")
	externalBefore := []byte("external stage target\n")
	if err := os.WriteFile(external, externalBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	swapped := false
	var swappedStage string
	hooks := indexPublishHooks{
		beforeInstall: func(relative string, parent *os.Root, stage string) error {
			if relative != "nested/index.md" {
				return nil
			}
			if err := parent.Remove(stage); err != nil {
				return err
			}
			if err := os.Symlink(external, filepath.Join(parent.Name(), stage)); err != nil {
				return err
			}
			swappedStage = filepath.Join(parent.Name(), stage)
			swapped = true
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	const wantError = `invalid index destination "nested/index.md": staged file changed before publication`
	if !swapped {
		t.Fatal("stage swap did not run")
	}
	if err == nil ||
		!strings.HasPrefix(err.Error(), wantError+"\n") ||
		!errors.Is(err, errPublicationConflict) {
		t.Fatalf("regenerateIndexesWithHooks() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	assertFileBytes(t, external, externalBefore)
	assertPathDoesNotExist(t, filepath.Join(root, "nested", indexFilename))
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	artifacts := assertIndexPrecommitManifestBoundInstallProofs(t, root)
	swappedInfo, statErr := os.Lstat(swappedStage)
	if statErr != nil || swappedInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("swapped stage info=%v error=%v, want preserved symlink", swappedInfo, statErr)
	}
	if link, readlinkErr := os.Readlink(swappedStage); readlinkErr != nil || link != external {
		t.Fatalf("swapped stage link=%q error=%v, want exact external target %q", link, readlinkErr, external)
	}
	swappedPreserved := false
	for _, artifact := range artifacts {
		base := filepath.Base(artifact)
		if _, ok := parseIndexArtifactName(base, indexArtifactStage); ok {
			if base == filepath.Base(swappedStage) {
				swappedPreserved = true
			}
		}
	}
	if !swappedPreserved {
		t.Fatalf("transaction artifacts = %#v, want preserved swapped stage %q", artifacts, swappedStage)
	}
}

func TestRegenerateIndexesRejectsInPlaceStageContentChange(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	changed := false
	var changedStage string
	hooks := indexPublishHooks{
		beforeInstall: func(relative string, parent *os.Root, stage string) error {
			if relative != "nested/index.md" {
				return nil
			}
			if err := parent.WriteFile(stage, []byte("attacker stage bytes\n"), 0o644); err != nil {
				return err
			}
			changedStage = filepath.Join(parent.Name(), stage)
			changed = true
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	const wantError = `invalid index destination "nested/index.md": staged file content changed before publication`
	if !changed {
		t.Fatal("stage content change did not run")
	}
	if err == nil ||
		!strings.HasPrefix(err.Error(), wantError+"\n") ||
		!errors.Is(err, errPublicationConflict) {
		t.Fatalf("regenerateIndexesWithHooks() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	assertPathDoesNotExist(t, filepath.Join(root, "nested", indexFilename))
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	assertIndexPrecommitManifestBoundInstallProofs(t, root)
	assertFileBytes(t, changedStage, []byte("attacker stage bytes\n"))
}

func assertIndexPrecommitManifestBoundInstallProofs(
	t *testing.T,
	root string,
) []string {
	t.Helper()

	manifest := readIndexBatchManifestGenericTest(t, root)
	artifacts := indexTransactionArtifacts(t, root)
	expected := make(map[string]struct{}, 1+2*len(manifest.Entries))
	for _, artifact := range artifacts {
		if parseIndexBatchManifestName(filepath.Base(artifact)) {
			expected[artifact] = struct{}{}
		}
	}
	if len(expected) != 1 {
		t.Fatalf("manifest artifacts=%#v, want exactly one", artifacts)
	}
	for _, entry := range manifest.Entries {
		for _, relative := range []string{entry.Stage, entry.NewInstall} {
			if relative == "" {
				t.Fatalf("entry %q has an empty install proof", entry.Path)
			}
			expected[relative] = struct{}{}
			if _, err := os.Lstat(
				filepath.Join(root, filepath.FromSlash(relative)),
			); err != nil {
				t.Fatalf("entry %q proof %q is missing: %v", entry.Path, relative, err)
			}
		}
		newInstallInfo, err := os.Lstat(
			filepath.Join(root, filepath.FromSlash(entry.NewInstall)),
		)
		if err != nil ||
			newInstallInfo.Mode()&os.ModeSymlink != 0 ||
			!newInstallInfo.Mode().IsRegular() {
			t.Fatalf(
				"entry %q N proof is not regular: info=%v error=%v",
				entry.Path,
				newInstallInfo,
				err,
			)
		}
	}
	if len(artifacts) != len(expected) {
		t.Fatalf(
			"transaction artifacts=%#v, want exact M+S+N set %#v",
			artifacts,
			expected,
		)
	}
	for _, artifact := range artifacts {
		if _, present := expected[artifact]; !present {
			t.Fatalf(
				"unexpected transaction artifact %q in M+S+N state",
				artifact,
			)
		}
	}
	return artifacts
}

func TestRegenerateIndexesRejectsConsumedInstallTokenRepopulationWithoutMutation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination := filepath.Join(root, "nested", indexFilename)
	original := []byte("# Original nested index\n")
	if err := os.WriteFile(destination, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		t.Fatal(err)
	}
	repopulated := false
	var installedInfo os.FileInfo
	var attackerInfo os.FileInfo
	var attackerPath string
	var attackedTree map[string]documentSessionTreeEntry
	var attackedIdentities map[string]os.FileInfo
	hooks := indexPublishHooks{
		afterInstallLink: func(relative string, parent *os.Root, install, leaf string) error {
			if relative != "nested/index.md" {
				return nil
			}
			var err error
			installedInfo, err = parent.Lstat(leaf)
			if err != nil {
				return err
			}
			// v3 consumes N by renaming it onto the public target. Recreating
			// the now-absent N path must be treated as a distinct foreign
			// reserved entry, never as an alias of target~S.
			if err := parent.WriteFile(install, []byte("attacker bytes after link\n"), 0o600); err != nil {
				return err
			}
			attackerInfo, err = parent.Lstat(install)
			if err != nil {
				return err
			}
			attackerPath = filepath.Join(parent.Name(), install)
			repopulated = true
			attackedTree = documentSessionCaptureTree(t, root)
			attackedIdentities = documentPublicationIdentities(t, root)
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !repopulated {
		t.Fatal("post-link install-token repopulation did not run")
	}
	if err == nil || !errors.Is(err, errPublicationScopeInvalid) {
		t.Fatalf("regenerateIndexesWithHooks() error = %v, want scope-invalid conflict", err)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	generated := []byte(BuildIndexText([]IndexEntry{
		{Type: "Note", Title: "A", Link: "a.md", Description: "Alpha."},
	}))
	assertFileBytes(t, destination, generated)
	info, statErr := os.Lstat(destination)
	if statErr != nil || installedInfo == nil || !os.SameFile(installedInfo, info) {
		t.Fatalf("installed target inode changed: installed=%v target=%v error=%v", installedInfo, info, statErr)
	}
	afterAttacker, statErr := os.Lstat(attackerPath)
	if statErr != nil || attackerInfo == nil || !os.SameFile(attackerInfo, afterAttacker) {
		t.Fatalf("repopulated N inode changed: before=%v after=%v error=%v", attackerInfo, afterAttacker, statErr)
	}
	if os.SameFile(info, afterAttacker) {
		t.Fatal("repopulated N unexpectedly aliases target~S")
	}
	assertFileBytes(t, attackerPath, []byte("attacker bytes after link\n"))
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
	documentAssertPublicationIdentities(t, root, attackedIdentities)
	artifacts := indexTransactionArtifacts(t, root)
	attackerBytesPreserved := false
	for _, artifact := range artifacts {
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifact)))
		if readErr == nil && bytes.Equal(data, []byte("attacker bytes after link\n")) {
			attackerBytesPreserved = true
			break
		}
	}
	if !attackerBytesPreserved {
		t.Fatalf("recovery artifacts = %#v, want preserved post-link attacker bytes", artifacts)
	}
	beforeRetry := documentSessionCaptureTree(t, root)
	beforeRetryIdentities := documentPublicationIdentities(t, root)
	retryWritten, retryErr := regenerateIndexesForTest(root)
	afterRetry := documentSessionCaptureTree(t, root)
	if retryWritten != nil || retryErr == nil {
		t.Fatalf("retry = %#v, %v; want hard consumed-N conflict", retryWritten, retryErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, beforeRetry, afterRetry)
	documentAssertPublicationIdentities(t, root, beforeRetryIdentities)
}

func TestRegenerateIndexesReturnsStableTypedRecoveryWhenInstallRestorationIsOccupied(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination := filepath.Join(root, "nested", indexFilename)
	original := []byte("# Original nested index\n")
	replacement := []byte("# Concurrent replacement\n")
	if err := os.WriteFile(destination, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		t.Fatal(err)
	}
	occupied := false
	hooks := indexPublishHooks{
		afterVacate: func(relative string, parent *os.Root, leaf, _ string) error {
			if relative != "nested/index.md" {
				return nil
			}
			occupied = true
			return parent.WriteFile(leaf, replacement, 0o640)
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !occupied {
		t.Fatal("occupied-destination injection did not run")
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	var firstRecovery recoverableIndexBackupError
	if err == nil || !errors.As(err, &firstRecovery) ||
		firstRecovery.destination != "nested/index.md" ||
		firstRecovery.backup == "" {
		t.Fatalf("error = %v, want typed recoverable anchor for nested/index.md", err)
	}
	assertFileBytes(t, destination, replacement)
	if artifacts := indexTransactionArtifacts(t, root); len(artifacts) == 0 {
		t.Fatal("occupied rollback lost manifest-bound recovery evidence")
	}
	beforeRetry := documentSessionCaptureTree(t, root)

	// Act again: a repeated run must classify the same immutable anchor.
	repeatedWritten, repeatedErr := regenerateIndexesForTest(root)
	afterRetry := documentSessionCaptureTree(t, root)

	// Assert again.
	if repeatedWritten != nil {
		t.Fatalf("repeated written = %#v, want nil", repeatedWritten)
	}
	var repeatedRecovery recoverableIndexBackupError
	if repeatedErr == nil || !errors.As(repeatedErr, &repeatedRecovery) ||
		repeatedRecovery.destination != firstRecovery.destination ||
		repeatedRecovery.backup != firstRecovery.backup {
		t.Fatalf(
			"repeated error = %v, want same typed recovery classification %#v",
			repeatedErr,
			firstRecovery,
		)
	}
	assertFileBytes(t, destination, replacement)
	documentSessionAssertTreeSnapshotEqual(t, beforeRetry, afterRetry)
}

func TestRegenerateIndexesPreservesRecoveryBackupWhenPublishedRollbackIsBlocked(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination := filepath.Join(root, "nested", indexFilename)
	original := []byte("# Original nested index\n")
	replacement := []byte("# Replacement after publish\n")
	if err := os.WriteFile(destination, original, 0o604); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, 0o604); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected published-prefix failure")
	replaced := false
	hooks := indexPublishHooks{
		afterPublish: func(relative string, ordinal int) error {
			if relative != "nested/index.md" || ordinal != 0 {
				return nil
			}
			if err := os.Remove(destination); err != nil {
				return err
			}
			if err := os.WriteFile(destination, replacement, 0o640); err != nil {
				return err
			}
			replaced = true
			return injected
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !replaced || !errors.Is(err, injected) {
		t.Fatalf("replacement ran = %t, error = %v; want injected", replaced, err)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	backups := indexRecoveryBackups(t, root)
	if len(backups) != 1 {
		t.Fatalf("recovery backups = %#v, want one", backups)
	}
	if !strings.Contains(err.Error(), "installed destination does not match stage") {
		t.Fatalf("error = %v, want exact installed-target conflict", err)
	}
	assertFileBytes(t, destination, replacement)
	assertRecoveryBackup(t, root, backups[0], original, 0o604)
}

func TestRegenerateIndexesPreservesRecoveryBackupWhenRestoreFails(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination := filepath.Join(root, "nested", indexFilename)
	original := []byte("# Original nested index\n")
	if err := os.WriteFile(destination, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, 0o640); err != nil {
		t.Fatal(err)
	}
	injectedPublish := errors.New("injected after publish")
	injectedRestore := errors.New("injected restore failure")
	hooks := indexPublishHooks{
		afterPublish: func(relative string, ordinal int) error {
			if relative == "nested/index.md" && ordinal == 0 {
				return injectedPublish
			}
			return nil
		},
		beforeRestore: func(relative string, _ *os.Root, _, _ string) error {
			if relative == "nested/index.md" {
				return injectedRestore
			}
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !errors.Is(err, injectedPublish) || !errors.Is(err, injectedRestore) {
		t.Fatalf("error = %v, want publish and restore causes", err)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	backups := indexRecoveryBackups(t, root)
	if len(backups) != 1 {
		t.Fatalf("recovery backups = %#v, want one", backups)
	}
	manifest := readIndexBatchManifestGenericTest(t, root)
	entry := indexBatchManifestEntryByPathForGenericTest(t, manifest, "nested/index.md")
	targetInfo, targetErr := os.Lstat(destination)
	stageInfo, stageErr := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Stage)))
	if targetErr != nil || stageErr != nil || !os.SameFile(targetInfo, stageInfo) {
		t.Fatalf(
			"published stage was vacated after pre-vacate hook failure: target=%v stage=%v errors=%v/%v",
			targetInfo,
			stageInfo,
			targetErr,
			stageErr,
		)
	}
	anchorPath := filepath.Join(root, filepath.FromSlash(entry.Anchor))
	anchorInfo, anchorErr := os.Lstat(anchorPath)
	if anchorErr != nil || os.SameFile(targetInfo, anchorInfo) {
		t.Fatalf("rollback anchor is unavailable or aliases published S: A=%v S=%v error=%v", anchorInfo, targetInfo, anchorErr)
	}
	assertRecoveryBackup(t, root, entry.Anchor, original, 0o640)
	assertRecoveryBackup(t, root, backups[0], original, 0o640)
	for _, evidence := range []string{
		entry.Stage,
		entry.Backup,
		entry.Claim,
		entry.Witness,
		entry.Anchor,
	} {
		if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(evidence))); statErr != nil {
			t.Fatalf("manifest-bound evidence %q is missing: %v", evidence, statErr)
		}
	}

	// Act again.
	repeatedWritten, repeatedErr := regenerateIndexesForTest(root)

	// Assert again: the verified backup restores the absent leaf before normal
	// regeneration, then both aliases are cleaned.
	if repeatedErr != nil || len(repeatedWritten) == 0 {
		t.Fatalf("repeated result = %#v, %v; want automatic recovery", repeatedWritten, repeatedErr)
	}
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesFailsClosedOnManifestBoundBackupCorruption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(t *testing.T, filename string)
		wantReason string
	}{
		{
			name: "in-place content change",
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.WriteFile(filename, []byte("corrupt recovery bytes\n"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "recovery artifact digest does not match destination, bytes, and mode",
		},
		{
			name: "atomic regular-file replacement",
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				replacement := filepath.Join(filepath.Dir(filename), ".attacker-recovery-replacement")
				if err := os.WriteFile(replacement, []byte("replacement recovery bytes\n"), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, filename); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "recovery artifact digest does not match destination, bytes, and mode",
		},
		{
			name: "symlink replacement",
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(t.TempDir(), "external")
				if err := os.WriteFile(external, []byte("external bytes\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filename); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			},
			wantReason: "recovery artifact is a symlink",
		},
		{
			name: "directory replacement",
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filename, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "recovery artifact is not a regular file",
		},
		{
			name: "mode change",
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Chmod(filename, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "recovery artifact digest does not match destination, bytes, and mode",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
			destination := filepath.Join(root, "nested", indexFilename)
			original := []byte("# Original nested index\n")
			if err := os.WriteFile(destination, original, 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(destination, 0o640); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected after durable batch manifest")
			mutated := false
			mutatedPath := ""
			hooks := indexPublishHooks{
				afterBatchManifestDurable: func(batchRoot *os.Root, name string) error {
					data, err := batchRoot.ReadFile(name)
					if err != nil {
						return err
					}
					var manifest indexBatchManifest
					if err := json.Unmarshal(data, &manifest); err != nil {
						return err
					}
					entry := indexBatchManifestEntryByPathForGenericTest(
						t,
						manifest,
						"nested/index.md",
					)
					mutatedPath = filepath.Join(root, filepath.FromSlash(entry.Backup))
					test.mutate(t, mutatedPath)
					mutated = true
					return injected
				},
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if !mutated || !errors.Is(err, injected) {
				t.Fatalf("mutated = %t, error = %v; want injected manifest-bound corruption", mutated, err)
			}
			if written != nil {
				t.Fatalf("written = %#v, want nil", written)
			}
			assertFileBytes(t, destination, original)
			if _, statErr := os.Lstat(mutatedPath); statErr != nil {
				t.Fatalf("manifest-bound corrupt backup was not retained: %v", statErr)
			}
			manifest := readIndexBatchManifestGenericTest(t, root)
			entry := indexBatchManifestEntryByPathForGenericTest(
				t,
				manifest,
				"nested/index.md",
			)
			beforeRetry := documentSessionCaptureTree(t, root)

			// Act again.
			repeatedWritten, repeatedErr := regenerateIndexesForTest(root)
			afterRetry := documentSessionCaptureTree(t, root)

			// Assert again: immutable manifest corruption remains a stable
			// zero-mutation blocker. No replacement slot is invented.
			if repeatedWritten != nil {
				t.Fatalf("repeated written = %#v, want nil", repeatedWritten)
			}
			var hard unrecoverableIndexRecoveryError
			if repeatedErr == nil ||
				!errors.As(repeatedErr, &hard) ||
				hard.destination != entry.Path ||
				hard.artifact != entry.Backup ||
				hard.reason !=
					"no exact independent manifest-bound backup remains before anchor durability" ||
				strings.Contains(repeatedErr.Error(), "original preserved at") {
				t.Fatalf("repeated error = %v, want fail-closed corrupt inventory error", repeatedErr)
			}
			documentSessionAssertTreeSnapshotEqual(t, beforeRetry, afterRetry)
		})
	}
}

func TestRegenerateIndexesFailsClosedWhenBackupChangesBetweenVerificationAndClaim(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination := filepath.Join(root, "nested", indexFilename)
	original := []byte("# Original nested index\n")
	if err := os.WriteFile(destination, original, 0o604); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, 0o604); err != nil {
		t.Fatal(err)
	}
	forcedRollback := errors.New("injected rollback before restore")
	changedBeforeClaim := false
	hooks := indexPublishHooks{
		afterVacate: func(relative string, _ *os.Root, _, _ string) error {
			if relative != "nested/index.md" {
				return nil
			}
			return forcedRollback
		},
		beforeRestore: func(relative string, parent *os.Root, _, backup string) error {
			if relative != "nested/index.md" {
				return nil
			}
			replacementName := filepath.Join(parent.Name(), ".attacker-before-claim")
			if err := os.WriteFile(replacementName, []byte("attacker artifact\n"), 0o604); err != nil {
				return err
			}
			if err := os.Rename(replacementName, filepath.Join(parent.Name(), backup)); err != nil {
				return err
			}
			changedBeforeClaim = true
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !changedBeforeClaim {
		t.Fatal("between-verification-and-claim mutation did not run")
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	manifest := readIndexBatchManifestGenericTest(t, root)
	entry := indexBatchManifestEntryByPathForGenericTest(t, manifest, "nested/index.md")
	var firstRecovery recoverableIndexBackupError
	if err == nil ||
		!errors.Is(err, forcedRollback) ||
		!errors.As(err, &firstRecovery) ||
		firstRecovery.destination != entry.Path ||
		firstRecovery.backup != entry.Anchor {
		t.Fatalf("error = %v, want typed exact-A rollback for %s", err, entry.Path)
	}
	anchorPath := filepath.Join(root, filepath.FromSlash(entry.Anchor))
	anchorInfo, statErr := os.Lstat(anchorPath)
	targetInfo, targetErr := os.Lstat(destination)
	if statErr != nil || targetErr != nil || !os.SameFile(anchorInfo, targetInfo) {
		t.Fatalf("target is not exact A: target=%v A=%v targetErr=%v anchorErr=%v", targetInfo, anchorInfo, targetErr, statErr)
	}
	assertFileBytes(t, destination, original)
	changedBackup := filepath.Join(root, filepath.FromSlash(entry.Backup))
	assertFileBytes(t, changedBackup, []byte("attacker artifact\n"))
	changedBackupInfo, statErr := os.Lstat(changedBackup)
	if statErr != nil || os.SameFile(anchorInfo, changedBackupInfo) {
		t.Fatalf("changed B aliases A or disappeared: A=%v B=%v error=%v", anchorInfo, changedBackupInfo, statErr)
	}
	beforeRetry := documentSessionCaptureTree(t, root)
	beforeRetryIdentities := documentPublicationIdentities(t, root)

	// Act again.
	retryWritten, retryErr := regenerateIndexesForTest(root)
	afterRetry := documentSessionCaptureTree(t, root)

	// Assert again: no replacement backup slot is invented and neither the
	// foreign destination nor changed evidence is mutated.
	var repeatedRecovery recoverableIndexBackupError
	if retryWritten != nil || retryErr == nil ||
		!errors.As(retryErr, &repeatedRecovery) ||
		repeatedRecovery.destination != firstRecovery.destination ||
		repeatedRecovery.backup != firstRecovery.backup {
		t.Fatalf("retry = %#v, %v; want same typed exact-A blocker %#v", retryWritten, retryErr, firstRecovery)
	}
	documentSessionAssertTreeSnapshotEqual(t, beforeRetry, afterRetry)
	documentAssertPublicationIdentities(t, root, beforeRetryIdentities)
}

func TestRegenerateIndexesRejectsCorruptRecoveryArtifactWithoutSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		regularDrift bool
		mutate       func(t *testing.T, filename string)
		wantReason   string
	}{
		{
			name:         "content",
			regularDrift: true,
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.WriteFile(filename, []byte("corrupt recovery bytes\n"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "recovery artifact digest does not match destination, bytes, and mode",
		},
		{
			name:         "mode",
			regularDrift: true,
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Chmod(filename, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "recovery artifact digest does not match destination, bytes, and mode",
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(t.TempDir(), "external")
				if err := os.WriteFile(external, []byte("external bytes\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filename); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			},
			wantReason: "recovery artifact is a symlink",
		},
		{
			name: "directory",
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filename, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "recovery artifact is not a regular file",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange: first leave one verified artifact through a controlled rollback failure.
			root := t.TempDir()
			writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
			destination := filepath.Join(root, "nested", indexFilename)
			if err := os.WriteFile(destination, []byte("# Original nested index\n"), 0o640); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected restore failure")
			_, err := regenerateIndexesWithTestHooks(root, nil, "", indexPublishHooks{
				afterVacate: func(relative string, _ *os.Root, _, _ string) error {
					if relative == "nested/index.md" {
						return injected
					}
					return nil
				},
				beforeRestore: func(relative string, _ *os.Root, _, _ string) error {
					if relative == "nested/index.md" {
						return injected
					}
					return nil
				},
			})

			if !errors.Is(err, injected) {
				t.Fatalf("setup error = %v, want injected", err)
			}
			backups := indexRecoveryBackups(t, root)
			if len(backups) != 1 {
				t.Fatalf("setup backups = %#v, want one", backups)
			}
			artifact := backups[0]
			artifactPath := filepath.Join(root, filepath.FromSlash(artifact))
			test.mutate(t, artifactPath)
			corruptInfo, statErr := os.Lstat(artifactPath)
			if statErr != nil {
				t.Fatal(statErr)
			}
			manifest := readIndexBatchManifestGenericTest(t, root)
			entry := indexBatchManifestEntryByPathForGenericTest(t, manifest, "nested/index.md")
			beforeFirst := documentSessionCaptureTree(t, root)
			beforeFirstIdentities := documentPublicationIdentities(t, root)

			// Act.
			firstWritten, firstErr := regenerateIndexesForTest(root)
			afterFirst := documentSessionCaptureTree(t, root)
			afterFirstIdentities := documentPublicationIdentities(t, root)
			secondWritten, secondErr := regenerateIndexesForTest(root)
			afterSecond := documentSessionCaptureTree(t, root)

			// Assert.
			if firstWritten != nil || secondWritten != nil {
				t.Fatalf("written = %#v / %#v, want nil / nil", firstWritten, secondWritten)
			}
			anchorPath := filepath.Join(root, filepath.FromSlash(entry.Anchor))
			anchorInfo, anchorErr := os.Lstat(anchorPath)
			afterCorrupt, statErr := os.Lstat(artifactPath)
			if statErr != nil || !os.SameFile(corruptInfo, afterCorrupt) {
				t.Fatalf("corrupt B changed: before=%v after=%v error=%v", corruptInfo, afterCorrupt, statErr)
			}
			if test.regularDrift {
				var firstRecovery recoverableIndexBackupError
				var repeatedRecovery recoverableIndexBackupError
				if firstErr == nil || secondErr == nil ||
					!strings.Contains(firstErr.Error(), test.wantReason) ||
					!strings.Contains(secondErr.Error(), test.wantReason) ||
					!errors.As(firstErr, &firstRecovery) ||
					firstRecovery.destination != entry.Path ||
					firstRecovery.backup != entry.Anchor ||
					!errors.As(secondErr, &repeatedRecovery) ||
					repeatedRecovery.destination != firstRecovery.destination ||
					repeatedRecovery.backup != firstRecovery.backup {
					t.Fatalf(
						"errors = %v / %v; want stable typed exact-A soft conflict for %s",
						firstErr,
						secondErr,
						entry.Path,
					)
				}
				targetInfo, targetErr := os.Lstat(destination)
				if anchorErr != nil || targetErr != nil || !os.SameFile(anchorInfo, targetInfo) {
					t.Fatalf("target is not exact A: target=%v A=%v targetErr=%v anchorErr=%v", targetInfo, anchorInfo, targetErr, anchorErr)
				}
				assertFileBytes(t, destination, []byte("# Original nested index\n"))
				assertPathDoesNotExist(
					t,
					filepath.Join(root, filepath.FromSlash(entry.RestoreInstall)),
				)
				stageInfo, stageErr := os.Lstat(
					filepath.Join(root, filepath.FromSlash(entry.Stage)),
				)
				newInstallInfo, newInstallErr := os.Lstat(
					filepath.Join(root, filepath.FromSlash(entry.NewInstall)),
				)
				if stageErr != nil || newInstallErr != nil ||
					!os.SameFile(stageInfo, newInstallInfo) {
					t.Fatalf(
						"terminal pre-new rollback lacks exact N~S: S=%v N=%v errors=%v/%v",
						stageInfo,
						newInstallInfo,
						stageErr,
						newInstallErr,
					)
				}
				assertPathDoesNotExist(t, filepath.Join(root, filepath.FromSlash(entry.Discard)))
			} else {
				var firstRecovery unrecoverableIndexRecoveryError
				var repeatedRecovery unrecoverableIndexRecoveryError
				if firstErr == nil || secondErr == nil ||
					!errors.As(firstErr, &firstRecovery) ||
					!errors.As(secondErr, &repeatedRecovery) ||
					firstRecovery.destination != entry.Path ||
					firstRecovery.artifact != entry.Stage ||
					repeatedRecovery.destination != firstRecovery.destination ||
					repeatedRecovery.artifact != firstRecovery.artifact ||
					!strings.Contains(firstRecovery.reason, "lacks a regular manifest-bound original evidence role") ||
					repeatedRecovery.reason != firstRecovery.reason {
					t.Fatalf(
						"errors = %v / %v; want stable hard structural-B conflict for %s",
						firstErr,
						secondErr,
						entry.Path,
					)
				}
				assertPathDoesNotExist(t, destination)
				documentSessionAssertTreeSnapshotEqual(t, beforeFirst, afterFirst)
				documentAssertPublicationIdentities(t, root, beforeFirstIdentities)
			}
			for _, relative := range []string{
				entry.Witness,
				entry.Claim,
				entry.Stage,
				entry.Anchor,
			} {
				info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
				if statErr != nil {
					t.Fatalf("v3 proof %q disappeared: %v", relative, statErr)
				}
				if os.SameFile(anchorInfo, info) && relative != entry.Anchor {
					t.Fatalf("A aliases non-anchor proof %q", relative)
				}
			}
			documentSessionAssertTreeSnapshotEqual(t, afterFirst, afterSecond)
			documentAssertPublicationIdentities(t, root, afterFirstIdentities)
		})
	}
}

func TestRegenerateIndexesFindsOrphanRecoveryArtifactBeforeEmptyPlanning(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	destination, artifact, original := leaveVerifiedIndexRecoveryBackup(t, root, 0o640)
	if err := os.Remove(filepath.Join(root, "nested", "a.md")); err != nil {
		t.Fatal(err)
	}

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert: the absent leaf is restored from the exact backup before the
	// remaining bundle is planned.
	if err != nil || len(written) == 0 {
		t.Fatalf("RegenerateIndexes() = %#v, %v; want automatic recovery", written, err)
	}
	assertFileBytes(t, destination, original)
	assertPathDoesNotExist(t, filepath.Join(root, filepath.FromSlash(artifact)))
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesReservesRecoveryPrefixCaseInsensitively(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "orphan"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := strings.ToUpper(indexRecoveryBackupPrefix) + "0000-" + strings.Repeat("0", 64)
	relative := filepath.ToSlash(filepath.Join("orphan", name))
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), []byte("foreign\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act.
	firstWritten, firstErr := regenerateIndexesForTest(root)
	secondWritten, secondErr := regenerateIndexesForTest(root)

	// Assert.
	want := "index recovery for destination \"orphan/index.md\" is unrecoverable: artifact \"" +
		relative + "\": malformed recovery artifact name"
	if firstWritten != nil || secondWritten != nil ||
		firstErr == nil || firstErr.Error() != want ||
		secondErr == nil || secondErr.Error() != want {
		t.Fatalf("results = %#v, %v / %#v, %v; want stable %q", firstWritten, firstErr, secondWritten, secondErr, want)
	}
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), []byte("foreign\n"))
}

func TestRegenerateIndexesRevalidatesRecoveryArtifactBetweenScanAndClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(t *testing.T, filename string)
		wantReason string
	}{
		{
			name: "truncate",
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.WriteFile(filename, []byte("truncated\n"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "observed recovery artifact state changed before final verification",
		},
		{
			name: "chmod",
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Chmod(filename, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "observed recovery artifact state changed before final verification",
		},
		{
			name: "rename replacement",
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				replacement := filename + ".replacement"
				if err := os.WriteFile(replacement, []byte("replacement\n"), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, filename); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "observed recovery artifact was replaced before final verification",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			_, artifact, _ := leaveVerifiedIndexRecoveryBackup(t, root, 0o640)
			mutated := false
			hooks := indexPublishHooks{
				beforeRecoveryClaim: func(relative string, _ *os.Root, _ string) error {
					if relative != "nested/index.md" || mutated {
						return nil
					}
					test.mutate(t, filepath.Join(root, filepath.FromSlash(artifact)))
					mutated = true
					return nil
				},
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			want := "index recovery for destination \"nested/index.md\" is unrecoverable: artifact \"" +
				artifact + "\": " + test.wantReason
			if !mutated || written != nil || err == nil || err.Error() != want {
				t.Fatalf("mutated = %t, result = %#v, %v; want nil, %q", mutated, written, err, want)
			}
			if strings.Contains(err.Error(), "original preserved at") {
				t.Fatalf("error contains stale preservation claim: %v", err)
			}
		})
	}
}

func TestRegenerateIndexesFailsClosedWhenObservedRecoveryArtifactDisappears(t *testing.T) {
	t.Parallel()

	for _, window := range []string{"discovery", "initial scan"} {
		window := window
		for _, state := range []string{"verified", "corrupt"} {
			state := state
			for _, operation := range []string{"remove", "rename"} {
				operation := operation
				t.Run(window+"/"+state+"/"+operation, func(t *testing.T) {
					t.Parallel()

					// Arrange.
					root := t.TempDir()
					_, artifact, _ := leaveVerifiedIndexRecoveryBackup(t, root, 0o640)
					artifactFilename := filepath.Join(root, filepath.FromSlash(artifact))
					if state == "corrupt" {
						if err := os.WriteFile(artifactFilename, []byte("corrupt before observation\n"), 0o640); err != nil {
							t.Fatal(err)
						}
					}
					mutated := false
					mutatedArtifact := ""
					mutate := func(remove func() error, rename func() error) error {
						if operation == "remove" {
							return remove()
						}
						return rename()
					}
					hooks := indexPublishHooks{}
					if window == "discovery" {
						hooks.afterRecoveryDiscovery = func(
							relative string,
							pinnedRoot *os.Root,
							observed string,
						) error {
							if relative != "nested/index.md" ||
								observed != artifact ||
								mutated {
								return nil
							}
							mutated = true
							mutatedArtifact = observed
							return mutate(
								func() error { return pinnedRoot.Remove(observed) },
								func() error {
									return pinnedRoot.Rename(observed, pathJoin("nested", ".parked-recovery"))
								},
							)
						}
					} else {
						hooks.afterRecoveryObservation = func(
							relative string,
							parent *os.Root,
							name string,
						) error {
							if relative != "nested/index.md" ||
								name != filepath.Base(filepath.FromSlash(artifact)) ||
								mutated {
								return nil
							}
							mutated = true
							mutatedArtifact = pathJoin(path.Dir(relative), name)
							return mutate(
								func() error { return parent.Remove(name) },
								func() error { return parent.Rename(name, ".parked-recovery") },
							)
						}
					}

					// Act.
					written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

					// Assert.
					if !mutated || mutatedArtifact == "" || written != nil || err == nil ||
						!strings.Contains(err.Error(), `artifact "`+mutatedArtifact+`"`) ||
						!strings.Contains(err.Error(), "observed recovery artifact disappeared before final verification") {
						t.Fatalf("mutated = %t artifact=%q result = %#v, %v; want observed disappearance", mutated, mutatedArtifact, written, err)
					}
					if strings.Contains(err.Error(), "original preserved at") {
						t.Fatalf("disappearance error contains false preservation claim: %v", err)
					}

					// A fresh invocation must still honor the immutable manifest:
					// disappearance of a bound artifact is a permanent blocker,
					// not permission to infer a new transaction.
					beforeFresh := documentSessionCaptureTree(t, root)
					freshWritten, freshErr := regenerateIndexesForTest(root)
					afterFresh := documentSessionCaptureTree(t, root)
					var hard unrecoverableIndexRecoveryError
					if freshWritten != nil || freshErr == nil ||
						!errors.As(freshErr, &hard) ||
						hard.destination != "nested/index.md" ||
						hard.reason !=
							"v3 recovery lacks a regular manifest-bound original evidence role" ||
						strings.Contains(freshErr.Error(), "original preserved at") {
						t.Fatalf("fresh RegenerateIndexes() = %#v, %v; want immutable-manifest blocker", freshWritten, freshErr)
					}
					documentSessionAssertTreeSnapshotEqual(t, beforeFresh, afterFresh)
				})
			}
		}
	}
}

func TestRegenerateIndexesRejectsDetachedRecoveryParentBeforeClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(t *testing.T, root string) indexPublishHooks
		installCut func(hooks *indexPublishHooks, detach func() error)
	}{
		{
			name: "active transaction",
			setup: func(t *testing.T, root string) indexPublishHooks {
				t.Helper()
				writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
				destination := filepath.Join(root, "nested", indexFilename)
				if err := os.WriteFile(destination, []byte("# Original\n"), 0o640); err != nil {
					t.Fatal(err)
				}
				return indexPublishHooks{
					afterVacate: func(relative string, _ *os.Root, _, _ string) error {
						if relative != "nested/index.md" {
							return nil
						}
						return errors.New("injected active rollback before restore")
					},
				}
			},
			installCut: func(hooks *indexPublishHooks, detach func() error) {
				hooks.beforeRestore = func(relative string, _ *os.Root, _, _ string) error {
					if relative != "nested/index.md" {
						return nil
					}
					return detach()
				}
			},
		},
		{
			name: "preflight recovery claim",
			setup: func(t *testing.T, root string) indexPublishHooks {
				t.Helper()
				leaveVerifiedIndexRecoveryBackup(t, root, 0o640)
				return indexPublishHooks{}
			},
			installCut: func(hooks *indexPublishHooks, detach func() error) {
				hooks.beforeRecoveryClaim = func(relative string, _ *os.Root, _ string) error {
					if relative != "nested/index.md" {
						return nil
					}
					return detach()
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			hooks := test.setup(t, root)
			renamed := false
			var attackedTree map[string]documentSessionTreeEntry
			var attackedIdentities map[string]os.FileInfo
			detach := func() error {
				if renamed {
					return nil
				}
				if err := os.Rename(filepath.Join(root, "nested"), filepath.Join(root, "parked")); err != nil {
					return err
				}
				if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
					return err
				}
				renamed = true
				attackedTree = documentSessionCaptureTree(t, root)
				attackedIdentities = documentPublicationIdentities(t, root)
				return nil
			}
			test.installCut(&hooks, detach)

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if !renamed || written != nil || err == nil ||
				!strings.Contains(err.Error(), "recovery artifact is in a detached destination parent") &&
					!strings.Contains(err.Error(), "destination ancestor changed after preflight") {
				t.Fatalf("renamed = %t, result = %#v, %v; want detached recovery error", renamed, written, err)
			}
			parked := filepath.Join(root, "parked")
			if artifacts := indexTransactionArtifacts(t, parked); len(artifacts) == 0 {
				t.Fatal("detached parent lost immutable v2 transaction evidence")
			}
			documentSessionAssertTreeSnapshotEqual(t, attackedTree, documentSessionCaptureTree(t, root))
			documentAssertPublicationIdentities(t, root, attackedIdentities)
			beforeRetry := documentSessionCaptureTree(t, root)
			retryWritten, retryErr := regenerateIndexesForTest(root)
			afterRetry := documentSessionCaptureTree(t, root)
			if retryWritten != nil || retryErr == nil {
				t.Fatalf("retry = %#v, %v; want stable detached-parent conflict", retryWritten, retryErr)
			}
			documentSessionAssertTreeSnapshotEqual(t, beforeRetry, afterRetry)
		})
	}
}

func TestRegenerateIndexesGuardedStageUnlinkPreservesReplacement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		replace func(t *testing.T, parent *os.Root, stage string)
	}{
		{
			name: "regular sentinel",
			replace: func(t *testing.T, parent *os.Root, stage string) {
				t.Helper()
				if err := parent.Remove(stage); err != nil {
					t.Fatal(err)
				}
				if err := parent.WriteFile(stage, []byte("unrelated sentinel\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink sentinel",
			replace: func(t *testing.T, parent *os.Root, stage string) {
				t.Helper()
				if err := parent.Remove(stage); err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(t.TempDir(), "external")
				if err := os.WriteFile(external, []byte("external sentinel\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(parent.Name(), stage)); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			},
		},
		{
			name: "directory sentinel",
			replace: func(t *testing.T, parent *os.Root, stage string) {
				t.Helper()
				if err := parent.Remove(stage); err != nil {
					t.Fatal(err)
				}
				if err := parent.Mkdir(stage, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			arrange := func(t *testing.T, root string) {
				t.Helper()
				writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
				if err := os.WriteFile(
					filepath.Join(root, "nested", indexFilename),
					[]byte("# Original nested index\n"),
					0o640,
				); err != nil {
					t.Fatal(err)
				}
			}
			after := generatedIndexOracle(t, arrange, "nested/index.md", "index.md")
			root := t.TempDir()
			arrange(t, root)
			replaced := false
			var replacementPath string
			var replacementInfo os.FileInfo
			var attackedTree map[string]documentSessionTreeEntry
			var attackedIdentities map[string]os.FileInfo
			hooks := indexPublishHooks{
				beforeCleanup: func(relative string, parent *os.Root, kind, stage string) error {
					if relative != "nested/index.md" ||
						kind != string(indexArtifactStage) ||
						replaced {
						return nil
					}
					test.replace(t, parent, stage)
					var err error
					replacementInfo, err = parent.Lstat(stage)
					if err != nil {
						return err
					}
					replacementPath = filepath.Join(parent.Name(), stage)
					replaced = true
					attackedTree = documentSessionCaptureTree(t, root)
					attackedIdentities = documentPublicationIdentities(t, root)
					return nil
				},
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			var hard unrecoverableIndexRecoveryError
			if !replaced || len(written) != len(after) || err == nil ||
				!errors.Is(err, ErrPublicationCommitted) ||
				!errors.As(err, &hard) {
				t.Fatalf(
					"replaced = %t, result = %#v, %v; want committed canonical-S conflict",
					replaced,
					written,
					err,
				)
			}
			for relative, data := range after {
				assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), data)
			}
			afterReplacement, statErr := os.Lstat(replacementPath)
			if statErr != nil ||
				replacementInfo == nil ||
				!os.SameFile(replacementInfo, afterReplacement) {
				t.Fatalf(
					"foreign S inode changed: before=%v after=%v error=%v",
					replacementInfo,
					afterReplacement,
					statErr,
				)
			}
			documentSessionAssertTreeSnapshotEqual(
				t,
				attackedTree,
				documentSessionCaptureTree(t, root),
			)
			documentAssertPublicationIdentities(t, root, attackedIdentities)

			beforeRetry := documentSessionCaptureTree(t, root)
			beforeRetryIdentities := documentPublicationIdentities(t, root)
			retryWritten, retryErr := regenerateIndexesForTest(root)
			afterRetry := documentSessionCaptureTree(t, root)
			var repeatedRecovery unrecoverableIndexRecoveryError
			if retryWritten != nil || retryErr == nil ||
				!errors.As(retryErr, &repeatedRecovery) {
				t.Fatalf("retry = %#v, %v; want typed foreign-S conflict", retryWritten, retryErr)
			}
			documentSessionAssertTreeSnapshotEqual(t, beforeRetry, afterRetry)
			documentAssertPublicationIdentities(t, root, beforeRetryIdentities)
		})
	}

	t.Run("rollback retains target alias moved from exact A onto S", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
		destination := filepath.Join(root, "nested", indexFilename)
		original := []byte("# Original nested index\n")
		if err := os.WriteFile(destination, original, 0o640); err != nil {
			t.Fatal(err)
		}
		injected := errors.New("force canonical v3 rollback")
		var manifest indexBatchManifest
		var manifestPath string
		replaced := false
		var replacementInfo os.FileInfo
		var attackedTree map[string]documentSessionTreeEntry
		var attackedIdentities map[string]os.FileInfo
		hooks := indexPublishHooks{
			afterBatchManifestDurable: func(_ *os.Root, name string) error {
				manifestPath = filepath.Join(root, name)
				data, err := os.ReadFile(manifestPath)
				if err != nil {
					return err
				}
				return json.Unmarshal(data, &manifest)
			},
			beforeRootCommit: func(string, *os.Root, string) error {
				return injected
			},
			beforeCleanup: func(relative string, parent *os.Root, kind, stage string) error {
				if relative != "nested/index.md" ||
					kind != string(indexArtifactStage) ||
					replaced {
					return nil
				}
				entry := indexBatchManifestEntryByPathForGenericTest(
					t,
					manifest,
					relative,
				)
				if stage != filepath.Base(entry.Stage) {
					t.Fatalf("cleanup S=%q, want manifest S=%q", stage, entry.Stage)
				}
				if err := parent.Remove(stage); err != nil {
					return err
				}
				if err := parent.Rename(filepath.Base(entry.Anchor), stage); err != nil {
					return err
				}
				var err error
				replacementInfo, err = parent.Lstat(stage)
				if err != nil {
					return err
				}
				replaced = true
				attackedTree = documentSessionCaptureTree(t, root)
				attackedIdentities = documentPublicationIdentities(t, root)
				return nil
			},
		}

		// Act.
		written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

		// Assert.
		var hard unrecoverableIndexRecoveryError
		if !replaced || written != nil || !errors.Is(err, injected) ||
			!errors.As(err, &hard) {
			t.Fatalf(
				"replaced=%t result=%#v, %v; want hard rollback S-proof conflict",
				replaced,
				written,
				err,
			)
		}
		entry := indexBatchManifestEntryByPathForGenericTest(
			t,
			manifest,
			"nested/index.md",
		)
		stagePath := filepath.Join(root, filepath.FromSlash(entry.Stage))
		stageInfo, stageErr := os.Lstat(stagePath)
		targetInfo, targetErr := os.Lstat(destination)
		if stageErr != nil || targetErr != nil ||
			replacementInfo == nil ||
			!os.SameFile(replacementInfo, stageInfo) ||
			!os.SameFile(stageInfo, targetInfo) {
			t.Fatalf(
				"moved A→S proof changed: recorded=%v S=%v target=%v errors=%v/%v",
				replacementInfo,
				stageInfo,
				targetInfo,
				stageErr,
				targetErr,
			)
		}
		assertFileBytes(t, destination, original)
		assertFileBytes(t, stagePath, original)
		assertPathDoesNotExist(t, filepath.Join(root, filepath.FromSlash(entry.Anchor)))
		if _, manifestErr := os.Lstat(manifestPath); manifestErr != nil {
			t.Fatalf("M disappeared before nonproof-S conflict: %v", manifestErr)
		}
		documentSessionAssertTreeSnapshotEqual(
			t,
			attackedTree,
			documentSessionCaptureTree(t, root),
		)
		documentAssertPublicationIdentities(t, root, attackedIdentities)

		beforeRetry := documentSessionCaptureTree(t, root)
		beforeRetryIdentities := documentPublicationIdentities(t, root)
		retryWritten, retryErr := regenerateIndexesForTest(root)
		afterRetry := documentSessionCaptureTree(t, root)
		var repeatedRecovery unrecoverableIndexRecoveryError
		if retryWritten != nil || retryErr == nil ||
			!errors.As(retryErr, &repeatedRecovery) {
			t.Fatalf("retry = %#v, %v; want typed moved-A conflict", retryWritten, retryErr)
		}
		documentSessionAssertTreeSnapshotEqual(t, beforeRetry, afterRetry)
		documentAssertPublicationIdentities(t, root, beforeRetryIdentities)
	})
}

func TestRegenerateIndexesDetectsArtifactPathRepopulationAfterUnlink(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination := filepath.Join(root, "nested", indexFilename)
	original := []byte("# Original nested index\n")
	if err := os.WriteFile(destination, original, 0o640); err != nil {
		t.Fatal(err)
	}
	repopulated := false
	var sentinelInfo os.FileInfo
	var sentinelPath string
	hooks := indexPublishHooks{
		afterCleanup: func(relative string, parent *os.Root, kind, name string) error {
			if relative != "nested/index.md" ||
				kind != string(indexArtifactStage) ||
				repopulated {
				return nil
			}
			if err := parent.WriteFile(name, []byte("post-unlink sentinel\n"), 0o600); err != nil {
				return err
			}
			var err error
			sentinelInfo, err = parent.Lstat(name)
			if err != nil {
				return err
			}
			sentinelPath = filepath.Join(parent.Name(), name)
			repopulated = true
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	var hard unrecoverableIndexRecoveryError
	if !repopulated || len(written) != 2 || err == nil ||
		!errors.Is(err, ErrPublicationCommitted) ||
		!errors.Is(err, errPublicationScopeInvalid) ||
		!errors.As(err, &hard) {
		t.Fatalf("repopulated = %t, result = %#v, %v; want committed repopulation failure", repopulated, written, err)
	}
	assertFileBytes(t, destination, []byte(BuildIndexText([]IndexEntry{
		{Type: "Note", Title: "A", Link: "a.md", Description: "Alpha."},
	})))
	artifacts := indexTransactionArtifacts(t, root)
	if len(artifacts) == 0 {
		t.Fatal("repopulated foreign artifact and recovery evidence were both lost")
	}
	afterSentinel, statErr := os.Lstat(sentinelPath)
	if statErr != nil || sentinelInfo == nil || !os.SameFile(sentinelInfo, afterSentinel) {
		t.Fatalf("repopulated sentinel inode changed: before=%v after=%v error=%v", sentinelInfo, afterSentinel, statErr)
	}
	assertFileBytes(t, sentinelPath, []byte("post-unlink sentinel\n"))
}

func TestRegenerateIndexesGuardedRecoveryUnlinksPreserveReplacements(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{
		string(indexArtifactRestore),
		string(indexArtifactBackup),
	} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
			destination := filepath.Join(root, "nested", indexFilename)
			original := []byte("# Original nested index\n")
			if err := os.WriteFile(destination, original, 0o604); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected after vacate")
			replaced := false
			hooks := indexPublishHooks{
				afterVacate: func(relative string, _ *os.Root, _, _ string) error {
					if relative == "nested/index.md" {
						return injected
					}
					return nil
				},
				beforeCleanup: func(relative string, parent *os.Root, artifactKind, name string) error {
					if relative != "nested/index.md" || artifactKind != kind || replaced {
						return nil
					}
					if err := parent.Remove(name); err != nil {
						return err
					}
					if err := parent.WriteFile(name, []byte("unrelated recovery sentinel\n"), 0o600); err != nil {
						return err
					}
					replaced = true
					return nil
				},
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			var hard unrecoverableIndexRecoveryError
			if !replaced || written != nil || !errors.Is(err, injected) ||
				!errors.As(err, &hard) {
				t.Fatalf("replaced = %t, result = %#v, %v; want guarded recovery unlink failure", replaced, written, err)
			}
			assertFileBytes(t, destination, original)
			artifacts := indexTransactionArtifacts(t, root)
			if len(artifacts) == 0 {
				t.Fatal("recovery sentinel was deleted")
			}
			foundSentinel := false
			for _, artifact := range artifacts {
				filename := filepath.Join(root, filepath.FromSlash(artifact))
				if data, readErr := os.ReadFile(filename); readErr == nil &&
					bytes.Equal(data, []byte("unrelated recovery sentinel\n")) {
					foundSentinel = true
				}
			}
			if !foundSentinel {
				t.Fatalf("artifacts = %#v, want unrelated recovery sentinel", artifacts)
			}
		})
	}
}

func TestRegenerateIndexesReportsPostCommitCleanupFailureForEveryBackup(t *testing.T) {
	t.Parallel()

	for failureOrdinal := 0; failureOrdinal < 3; failureOrdinal++ {
		failureOrdinal := failureOrdinal
		t.Run(itoa(failureOrdinal), func(t *testing.T) {
			t.Parallel()

			// Arrange.
			before := map[string][]byte{
				"a/index.md": []byte("# A original\n"),
				"b/index.md": []byte("# B original\n"),
				"index.md":   []byte("# Root original\n"),
			}
			modes := map[string]fs.FileMode{
				"a/index.md": 0o600,
				"b/index.md": 0o640,
				"index.md":   0o604,
			}
			arrange := func(t *testing.T, root string) {
				t.Helper()
				writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
				writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
				for relative, data := range before {
					filename := filepath.Join(root, filepath.FromSlash(relative))
					if err := os.WriteFile(filename, data, modes[relative]); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(filename, modes[relative]); err != nil {
						t.Fatal(err)
					}
				}
			}
			after := generatedIndexOracle(t, arrange, "a/index.md", "b/index.md", "index.md")
			root := t.TempDir()
			arrange(t, root)
			injected := errors.New("injected pre-commit backup cleanup failure")
			backupOrdinal := 0
			failed := false
			hooks := indexPublishHooks{
				beforeCleanup: func(_ string, _ *os.Root, kind, _ string) error {
					if kind != string(indexArtifactBackup) || failed {
						return nil
					}
					current := backupOrdinal
					backupOrdinal++
					if current == failureOrdinal {
						failed = true
						return injected
					}
					return nil
				},
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if !failed || len(written) != len(after) || !errors.Is(err, injected) ||
				!errors.Is(err, ErrPublicationCommitted) {
				t.Fatalf("failed = %t, result = %#v, %v; want committed cleanup failure", failed, written, err)
			}
			for relative, data := range after {
				filename := filepath.Join(root, filepath.FromSlash(relative))
				assertFileBytes(t, filename, data)
				info, statErr := os.Lstat(filename)
				if statErr != nil {
					t.Fatal(statErr)
				}
				if got := indexPreservedMode(info.Mode()); got != modes[relative] {
					t.Fatalf("%s mode = %v, want %v", relative, got, modes[relative])
				}
			}
			if artifacts := indexTransactionArtifacts(t, root); len(artifacts) == 0 {
				t.Fatal("post-commit cleanup evidence was deleted")
			}
			recovered, recoveryErr := regenerateIndexesForTest(root)
			if recoveryErr != nil || len(recovered) != len(after) {
				t.Fatalf("next RegenerateIndexes() = %#v, %v; want convergence", recovered, recoveryErr)
			}
			for relative, data := range after {
				assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), data)
			}
			assertNoIndexTransactionArtifacts(t, root)
		})
	}
}

func TestRegenerateIndexesPostCommitCleanupHandlesExistingAndNewDestinations(t *testing.T) {
	t.Parallel()

	// Arrange.
	arrange := func(t *testing.T, root string) {
		t.Helper()
		writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
		writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
		if err := os.WriteFile(filepath.Join(root, "a", indexFilename), []byte("# A original\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, indexFilename), []byte("# Root original\n"), 0o604); err != nil {
			t.Fatal(err)
		}
	}
	after := generatedIndexOracle(t, arrange, "a/index.md", "b/index.md", "index.md")
	root := t.TempDir()
	arrange(t, root)
	aIndex := filepath.Join(root, "a", indexFilename)
	bIndex := filepath.Join(root, "b", indexFilename)
	rootIndex := filepath.Join(root, indexFilename)
	rootBefore := []byte("# Root original\n")
	injected := errors.New("injected second backup cleanup failure")
	backupOrdinal := 0
	failed := false
	hooks := indexPublishHooks{
		beforeCleanup: func(_ string, _ *os.Root, kind, _ string) error {
			if kind != string(indexArtifactBackup) || failed {
				return nil
			}
			current := backupOrdinal
			backupOrdinal++
			if current == 1 {
				failed = true
				return injected
			}
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !failed || len(written) != len(after) || !errors.Is(err, injected) ||
		!errors.Is(err, ErrPublicationCommitted) {
		t.Fatalf("failed = %t, result = %#v, %v; want committed cleanup failure", failed, written, err)
	}
	assertFileBytes(t, aIndex, after["a/index.md"])
	assertFileBytes(t, bIndex, after["b/index.md"])
	assertFileBytes(t, rootIndex, after["index.md"])
	artifacts := indexTransactionArtifacts(t, root)
	rootRecoveryPreserved := false
	for _, artifact := range artifacts {
		if !strings.HasPrefix(filepath.Base(artifact), indexRecoveryBackupPrefix) {
			continue
		}
		filename := filepath.Join(root, filepath.FromSlash(artifact))
		info, statErr := os.Lstat(filename)
		if statErr != nil || !info.Mode().IsRegular() || indexPreservedMode(info.Mode()) != 0o604 {
			continue
		}
		data, readErr := os.ReadFile(filename)
		if readErr == nil && bytes.Equal(data, rootBefore) {
			rootRecoveryPreserved = true
			break
		}
	}
	if !rootRecoveryPreserved {
		t.Fatalf("recovery artifacts = %#v, want verified root original bytes and mode", artifacts)
	}
	recovered, recoveryErr := regenerateIndexesForTest(root)
	if recoveryErr != nil || len(recovered) != len(after) {
		t.Fatalf("next RegenerateIndexes() = %#v, %v; want convergence", recovered, recoveryErr)
	}
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesPostCommitCleanupSwapPreservesForeignEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		replace func(t *testing.T, parent *os.Root, name string)
	}{
		{
			name: "regular",
			replace: func(t *testing.T, parent *os.Root, name string) {
				t.Helper()
				if err := parent.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := parent.WriteFile(name, []byte("foreign backup sentinel\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			replace: func(t *testing.T, parent *os.Root, name string) {
				t.Helper()
				if err := parent.Remove(name); err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(t.TempDir(), "external")
				if err := os.WriteFile(external, []byte("external backup sentinel\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(parent.Name(), name)); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			},
		},
		{
			name: "directory",
			replace: func(t *testing.T, parent *os.Root, name string) {
				t.Helper()
				if err := parent.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := parent.Mkdir(name, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			arrange := func(t *testing.T, root string) {
				t.Helper()
				writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
				if err := os.WriteFile(filepath.Join(root, "nested", indexFilename), []byte("# Nested original\n"), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, indexFilename), []byte("# Root original\n"), 0o604); err != nil {
					t.Fatal(err)
				}
			}
			after := generatedIndexOracle(t, arrange, "nested/index.md", "index.md")
			root := t.TempDir()
			arrange(t, root)
			nestedIndex := filepath.Join(root, "nested", indexFilename)
			rootIndex := filepath.Join(root, indexFilename)
			swapped := false
			foreign := ""
			hooks := indexPublishHooks{
				beforeCleanup: func(relative string, parent *os.Root, kind, name string) error {
					if relative != "nested/index.md" ||
						kind != string(indexArtifactBackup) ||
						swapped {
						return nil
					}
					foreign = filepath.Join(parent.Name(), name)
					test.replace(t, parent, name)
					swapped = true
					return nil
				},
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			var hard unrecoverableIndexRecoveryError
			if !swapped || len(written) != len(after) || err == nil ||
				!errors.Is(err, ErrPublicationCommitted) ||
				!errors.As(err, &hard) {
				t.Fatalf("swapped = %t, result = %#v, %v; want committed cleanup failure", swapped, written, err)
			}
			assertFileBytes(t, nestedIndex, after["nested/index.md"])
			assertFileBytes(t, rootIndex, after["index.md"])
			if _, statErr := os.Lstat(foreign); statErr != nil {
				t.Fatalf("foreign entry was removed: %v", statErr)
			}
			repeatedWritten, repeatedErr := regenerateIndexesForTest(root)
			if repeatedWritten != nil || repeatedErr == nil ||
				strings.Contains(repeatedErr.Error(), "original preserved at") {
				t.Fatalf("repeated result = %#v, %v; want honest orphan rejection", repeatedWritten, repeatedErr)
			}
		})
	}
}

func TestRegenerateIndexesReportsParentCloseErrorsAfterCommit(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	injected := errors.New("injected parent close failure")
	closeCalls := 0
	hooks := indexPublishHooks{
		beforeClose: func(string) error {
			closeCalls++
			return injected
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !errors.Is(err, injected) || !errors.Is(err, ErrPublicationCommitted) {
		t.Fatalf("regenerateIndexesWithHooks() error = %v, want committed close failure", err)
	}
	if closeCalls != len(written) || len(written) == 0 {
		t.Fatalf("close calls = %d, written = %#v; want one finalization per destination", closeCalls, written)
	}
	assertNoIndexTransactionArtifacts(t, root)
	assertFileBytes(t, filepath.Join(root, "nested", indexFilename), []byte(BuildIndexText([]IndexEntry{
		{Type: "Note", Title: "A", Link: "a.md", Description: "Alpha."},
	})))
}

func TestRegenerateIndexesFinalCommitValidationRunsInPublicationOrder(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
	writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
	var validated []string
	hooks := indexPublishHooks{
		beforeFinalValidation: func(relative string, _ int) error {
			validated = append(validated, relative)
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if err != nil {
		t.Fatalf("regenerateIndexesWithHooks() error = %v", err)
	}
	want := []string{"a/index.md", "b/index.md", "index.md"}
	if !reflect.DeepEqual(validated, want) {
		t.Fatalf("final validation order = %#v, want %#v", validated, want)
	}
	if len(written) != len(want) || written[len(written)-1] != filepath.Join(root, indexFilename) {
		t.Fatalf("written = %#v, want root last", written)
	}
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesPostCommitCleanupNeverRollsBackInPlaceTampering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		cleanupOrdinal int
		afterCleanup   bool
		mutate         func(t *testing.T, filename string)
		wantError      bool
	}{
		{
			name:           "first/content",
			cleanupOrdinal: 0,
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.WriteFile(filename, []byte("tampered published bytes\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantError: true,
		},
		{
			name:           "middle/chmod",
			cleanupOrdinal: 1,
			wantError:      true,
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Chmod(filename, 0o666); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:           "last/root-content-after-unlink",
			cleanupOrdinal: 2,
			afterCleanup:   true,
			mutate: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.WriteFile(filename, []byte("tampered root after backup unlink\n"), 0o604); err != nil {
					t.Fatal(err)
				}
			},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			before := map[string][]byte{
				"a/index.md": []byte("# A original\n"),
				"b/index.md": []byte("# B original\n"),
				"index.md":   []byte("# Root original\n"),
			}
			modes := map[string]fs.FileMode{
				"a/index.md": 0o600,
				"b/index.md": 0o640,
				"index.md":   0o604,
			}
			arrange := func(t *testing.T, root string) {
				t.Helper()
				writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
				writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
				for relative, data := range before {
					filename := filepath.Join(root, filepath.FromSlash(relative))
					if err := os.WriteFile(filename, data, modes[relative]); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(filename, modes[relative]); err != nil {
						t.Fatal(err)
					}
				}
			}
			after := generatedIndexOracle(t, arrange, "a/index.md", "b/index.md", "index.md")
			root := t.TempDir()
			arrange(t, root)
			cleanupOrdinal := 0
			mutated := false
			mutateAtBoundary := func(kind string) {
				if kind != string(indexArtifactBackup) || mutated {
					return
				}
				current := cleanupOrdinal
				cleanupOrdinal++
				if current != test.cleanupOrdinal {
					return
				}
				target := filepath.Join(root, "a", indexFilename)
				if test.cleanupOrdinal == 2 {
					target = filepath.Join(root, indexFilename)
				}
				test.mutate(t, target)
				mutated = true
			}
			hooks := indexPublishHooks{}
			if test.afterCleanup {
				hooks.afterCleanup = func(_ string, _ *os.Root, kind, _ string) error {
					mutateAtBoundary(kind)
					return nil
				}
			} else {
				hooks.beforeCleanup = func(_ string, _ *os.Root, kind, _ string) error {
					mutateAtBoundary(kind)
					return nil
				}
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if !mutated || len(written) != len(after) {
				t.Fatalf("mutated = %t, result = %#v, %v; want committed publication", mutated, written, err)
			}
			if test.wantError {
				if !errors.Is(err, ErrPublicationCommitted) {
					t.Fatalf("error = %v, want ErrPublicationCommitted", err)
				}
			} else if err != nil {
				t.Fatalf("error = %v, want committed success", err)
			}
			for relative, data := range after {
				if relative == "a/index.md" || relative == "index.md" {
					continue
				}
				assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), data)
			}
			aIndex := filepath.Join(root, "a", indexFilename)
			rootIndex := filepath.Join(root, indexFilename)
			switch test.name {
			case "first/content":
				assertFileBytes(t, aIndex, []byte("tampered published bytes\n"))
				assertFileBytes(t, rootIndex, after["index.md"])
			case "middle/chmod":
				assertFileBytes(t, aIndex, after["a/index.md"])
				info, statErr := os.Lstat(aIndex)
				if statErr != nil || indexPreservedMode(info.Mode()) != 0o666 {
					t.Fatalf("tampered mode = %v, error = %v; want 0666", info, statErr)
				}
				assertFileBytes(t, rootIndex, after["index.md"])
			case "last/root-content-after-unlink":
				assertFileBytes(t, aIndex, after["a/index.md"])
				assertFileBytes(t, rootIndex, []byte("tampered root after backup unlink\n"))
			}
		})
	}
}

func TestRegenerateIndexesLateCleanupBatchHasExplicitPreOrPostCommitOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		kind          string
		relative      string
		wantCommitted bool
	}{
		{name: "early stage removal", kind: "stage", relative: "a/index.md", wantCommitted: true},
		{name: "last stage removal", kind: "stage", relative: "index.md", wantCommitted: true},
		{name: "early backup removal", kind: string(indexArtifactBackup), relative: "a/index.md", wantCommitted: true},
		{name: "last backup removal", kind: string(indexArtifactBackup), relative: "index.md", wantCommitted: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			arrange := func(t *testing.T, root string) (map[string][]byte, map[string]fs.FileMode) {
				t.Helper()
				writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
				writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
				before := map[string][]byte{
					"a/index.md": []byte("# A original\n"),
					"b/index.md": []byte("# B original\n"),
					"index.md":   []byte("# Root original\n"),
				}
				modes := map[string]fs.FileMode{
					"a/index.md": 0o600,
					"b/index.md": 0o640,
					"index.md":   0o604,
				}
				for relative, data := range before {
					filename := filepath.Join(root, filepath.FromSlash(relative))
					if err := os.WriteFile(filename, data, modes[relative]); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(filename, modes[relative]); err != nil {
						t.Fatal(err)
					}
				}
				return before, modes
			}
			capture := func(t *testing.T, root string, modes map[string]fs.FileMode) map[string][]byte {
				t.Helper()
				result := make(map[string][]byte, len(modes))
				for relative := range modes {
					data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
					if err != nil {
						t.Fatal(err)
					}
					result[relative] = data
				}
				return result
			}

			// Arrange.
			root := t.TempDir()
			before, modes := arrange(t, root)
			oracle := t.TempDir()
			_, oracleModes := arrange(t, oracle)
			if _, err := regenerateIndexesForTest(oracle); err != nil {
				t.Fatalf("oracle RegenerateIndexes() error = %v", err)
			}
			after := capture(t, oracle, oracleModes)
			fault := errors.New("injected batch cleanup failure")
			fired := false
			hooks := indexPublishHooks{
				afterCleanup: func(relative string, _ *os.Root, kind, _ string) error {
					if !fired && relative == test.relative && kind == test.kind {
						fired = true
						return fault
					}
					return nil
				},
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if !fired || !errors.Is(err, fault) {
				t.Fatalf("fired=%t, result=%#v, error=%v; want cleanup fault", fired, written, err)
			}
			want := before
			if test.wantCommitted {
				want = after
				if !errors.Is(err, ErrPublicationCommitted) || len(written) != len(modes) {
					t.Fatalf("committed result=%#v, error=%v; want ErrPublicationCommitted and all paths", written, err)
				}
			} else if written != nil || errors.Is(err, ErrPublicationCommitted) {
				t.Fatalf("pre-commit result=%#v, error=%v; want rollback with nil written", written, err)
			}
			for relative, data := range want {
				filename := filepath.Join(root, filepath.FromSlash(relative))
				assertFileBytes(t, filename, data)
				info, statErr := os.Lstat(filename)
				if statErr != nil || indexPreservedMode(info.Mode()) != modes[relative] {
					t.Fatalf("%s mode=%v, error=%v; want %v", relative, info, statErr, modes[relative])
				}
			}
			recovered, recoveryErr := regenerateIndexesForTest(root)
			if recoveryErr != nil || len(recovered) != len(modes) {
				t.Fatalf("next RegenerateIndexes()=%#v, %v; want convergence", recovered, recoveryErr)
			}
			for relative, data := range after {
				assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), data)
			}
			assertNoIndexTransactionArtifacts(t, root)
		})
	}
}

func TestRegenerateIndexesPostCommitCleanupPreservesForeignLeaf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		replace func(t *testing.T, filename string)
	}{
		{
			name: "atomic regular replacement",
			replace: func(t *testing.T, filename string) {
				t.Helper()
				replacement := filename + ".replacement"
				if err := os.WriteFile(replacement, []byte("foreign published leaf\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, filename); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			replace: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(t.TempDir(), "external")
				if err := os.WriteFile(external, []byte("external foreign leaf\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filename); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			},
		},
		{
			name: "directory",
			replace: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filename, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			arrange := func(t *testing.T, root string) {
				t.Helper()
				writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
				if err := os.WriteFile(filepath.Join(root, "nested", indexFilename), []byte("# Nested original\n"), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, indexFilename), []byte("# Root original\n"), 0o604); err != nil {
					t.Fatal(err)
				}
			}
			after := generatedIndexOracle(t, arrange, "nested/index.md", "index.md")
			root := t.TempDir()
			arrange(t, root)
			nestedIndex := filepath.Join(root, "nested", indexFilename)
			rootIndex := filepath.Join(root, indexFilename)
			cleanupOrdinal := 0
			replaced := false
			hooks := indexPublishHooks{
				beforeCleanup: func(_ string, _ *os.Root, kind, _ string) error {
					if kind != string(indexArtifactBackup) || replaced {
						return nil
					}
					current := cleanupOrdinal
					cleanupOrdinal++
					if current != 1 {
						return nil
					}
					test.replace(t, nestedIndex)
					replaced = true
					return nil
				},
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if !replaced || len(written) != len(after) ||
				!errors.Is(err, ErrPublicationCommitted) {
				t.Fatalf("replaced = %t, result = %#v, %v; want committed cleanup conflict", replaced, written, err)
			}
			assertFileBytes(t, rootIndex, after["index.md"])
			if _, statErr := os.Lstat(nestedIndex); statErr != nil {
				t.Fatalf("foreign leaf was removed: %v", statErr)
			}
			if artifacts := indexTransactionArtifacts(t, root); len(artifacts) == 0 {
				t.Fatal("foreign leaf cleanup conflict lost recovery evidence")
			}
		})
	}
}

func TestRegenerateIndexesPostCommitCleanupPreservesForeignParent(t *testing.T) {
	t.Parallel()

	// Arrange.
	arrange := func(t *testing.T, root string) {
		t.Helper()
		writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
		if err := os.WriteFile(filepath.Join(root, "nested", indexFilename), []byte("# Nested original\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	after := generatedIndexOracle(t, arrange, "nested/index.md", "index.md")
	root := t.TempDir()
	arrange(t, root)
	renamed := false
	hooks := indexPublishHooks{
		afterCleanup: func(relative string, _ *os.Root, kind, _ string) error {
			if relative != "nested/index.md" ||
				kind != string(indexArtifactBackup) ||
				renamed {
				return nil
			}
			if err := os.Rename(filepath.Join(root, "nested"), filepath.Join(root, "parked")); err != nil {
				return err
			}
			if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(root, "nested", indexFilename), []byte("foreign parent leaf\n"), 0o600); err != nil {
				return err
			}
			renamed = true
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	var hard unrecoverableIndexRecoveryError
	if !renamed || len(written) != len(after) || err == nil ||
		!errors.Is(err, ErrPublicationCommitted) ||
		!errors.As(err, &hard) ||
		strings.Contains(err.Error(), "original preserved at \"nested/") {
		t.Fatalf("renamed = %t, result = %#v, %v; want honest committed detached cleanup", renamed, written, err)
	}
	assertFileBytes(t, filepath.Join(root, "nested", indexFilename), []byte("foreign parent leaf\n"))
	assertFileBytes(t, filepath.Join(root, "parked", indexFilename), after["nested/index.md"])
	assertFileBytes(t, filepath.Join(root, indexFilename), after["index.md"])
	if artifacts := indexTransactionArtifacts(t, root); len(artifacts) == 0 {
		t.Fatal("detached cleanup conflict lost all remaining recovery evidence")
	}
}

func TestRegenerateIndexesPostCommitCleanupPreservesForeignNewDestination(t *testing.T) {
	t.Parallel()

	// Arrange.
	arrange := func(t *testing.T, root string) {
		t.Helper()
		writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
		writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
		if err := os.WriteFile(filepath.Join(root, "a", indexFilename), []byte("# A original\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, indexFilename), []byte("# Root original\n"), 0o604); err != nil {
			t.Fatal(err)
		}
	}
	after := generatedIndexOracle(t, arrange, "a/index.md", "b/index.md", "index.md")
	root := t.TempDir()
	arrange(t, root)
	aIndex := filepath.Join(root, "a", indexFilename)
	bIndex := filepath.Join(root, "b", indexFilename)
	rootIndex := filepath.Join(root, indexFilename)
	replaced := false
	hooks := indexPublishHooks{
		afterCleanup: func(relative string, _ *os.Root, kind, _ string) error {
			if relative != indexFilename ||
				kind != string(indexArtifactBackup) ||
				replaced {
				return nil
			}
			replacement := bIndex + ".replacement"
			if err := os.WriteFile(replacement, []byte("foreign new destination\n"), 0o600); err != nil {
				return err
			}
			if err := os.Rename(replacement, bIndex); err != nil {
				return err
			}
			replaced = true
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !replaced || len(written) != len(after) ||
		!errors.Is(err, ErrPublicationCommitted) {
		t.Fatalf("replaced = %t, result = %#v, %v; want committed cleanup conflict", replaced, written, err)
	}
	assertFileBytes(t, aIndex, after["a/index.md"])
	assertFileBytes(t, rootIndex, after["index.md"])
	assertFileBytes(t, bIndex, []byte("foreign new destination\n"))
	if artifacts := indexTransactionArtifacts(t, root); len(artifacts) == 0 {
		t.Fatal("foreign destination cleanup conflict lost recovery evidence")
	}
}

func TestRegenerateIndexesSelectorRejectsBeforePublication(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		declared string
		selector string
		want     error
	}{
		{name: "declared legacy conflicts with native", declared: "0.1", selector: "0.2", want: ErrVersionConflict},
		{name: "future conflicts with explicit native", declared: "9.9", selector: "0.2", want: ErrVersionConflict},
		{name: "unsupported selector", declared: "0.2", selector: "9.0", want: ErrUnsupportedVersionSelector},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			rootBefore := []byte("---\nokf_version: \"" + test.declared + "\"\n---\n\n# Root before\n")
			if err := os.WriteFile(filepath.Join(root, indexFilename), rootBefore, 0o600); err != nil {
				t.Fatal(err)
			}
			writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
			nestedBefore := []byte("# Nested before\n")
			if err := os.WriteFile(filepath.Join(root, "nested", indexFilename), nestedBefore, 0o640); err != nil {
				t.Fatal(err)
			}

			// Act.
			written, err := regenerateIndexesWithSelectorForTest(root, test.selector)

			// Assert.
			if !errors.Is(err, test.want) {
				t.Fatalf("RegenerateIndexesWithSelector() error = %v, want %v", err, test.want)
			}
			if written != nil {
				t.Fatalf("written = %#v, want nil", written)
			}
			assertFileBytes(t, filepath.Join(root, indexFilename), rootBefore)
			assertFileBytes(t, filepath.Join(root, "nested", indexFilename), nestedBefore)
			assertNoIndexTransactionArtifacts(t, root)
		})
	}
}

func TestRegenerateIndexesSelectorAutoPreservesFutureDeclaration(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, indexFilename, "---\nokf_version: \"9.9\"\n---\n\n# Root before\n")
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")

	// Act.
	written, err := regenerateIndexesWithSelectorForTest(root, "")

	// Assert.
	if err != nil {
		t.Fatalf("RegenerateIndexesWithSelector() error = %v", err)
	}
	if len(written) == 0 {
		t.Fatal("written is empty")
	}
	document, err := ParseDocument(readFile(t, root, indexFilename))
	if err != nil {
		t.Fatal(err)
	}
	state := document.Frontmatter.VersionDeclarationState()
	if !state.Present || !state.Valid || state.Value != "9.9" {
		t.Fatalf("root version state = %#v, want preserved future 9.9", state)
	}
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesRootContentChangeAbortsAndRollsBackEarlierIndexes(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	rootBefore := []byte("---\nokf_version: \"0.2\"\n---\n\n# Root before\n")
	rootConcurrent := []byte("---\nokf_version: \"0.1\"\n---\n\n# Concurrent root\n")
	if err := os.WriteFile(filepath.Join(root, indexFilename), rootBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	nestedBefore := []byte("# Nested before\n")
	if err := os.WriteFile(filepath.Join(root, "nested", indexFilename), nestedBefore, 0o640); err != nil {
		t.Fatal(err)
	}
	changed := false
	hooks := indexPublishHooks{
		beforePublish: func(relative string, _ int) error {
			if relative != indexFilename {
				return nil
			}
			if err := os.WriteFile(filepath.Join(root, indexFilename), rootConcurrent, 0o600); err != nil {
				return err
			}
			changed = true
			return nil
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "0.2", hooks)

	// Assert.
	const wantError = `invalid index destination "index.md": destination content changed after preflight`
	if !changed {
		t.Fatal("root content change did not run")
	}
	if err == nil || !strings.Contains(err.Error(), wantError) {
		t.Fatalf("regenerateIndexesWithHooks() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	assertFileBytes(t, filepath.Join(root, indexFilename), rootConcurrent)
	assertFileBytes(t, filepath.Join(root, "nested", indexFilename), nestedBefore)
	if artifacts := indexTransactionArtifacts(t, root); len(artifacts) == 0 {
		t.Fatal("pre-root foreign mutation lost immutable batch recovery evidence")
	}
	beforeRetry := documentSessionCaptureTree(t, root)
	retryWritten, retryErr := regenerateIndexesForTest(root)
	afterRetry := documentSessionCaptureTree(t, root)
	if retryWritten != nil || retryErr == nil {
		t.Fatalf("retry = %#v, %v; want stable manual conflict", retryWritten, retryErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, beforeRetry, afterRetry)
}

func TestRegenerateIndexesRootChangeAfterResolutionFailsAtPreflight(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	rootBefore := []byte("---\nokf_version: \"0.2\"\n---\n\n# Root before\n")
	rootConcurrent := []byte("---\nokf_version: \"0.1\"\n---\n\n# Concurrent root\n")
	if err := os.WriteFile(filepath.Join(root, indexFilename), rootBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	nestedBefore := []byte("# Nested before\n")
	if err := os.WriteFile(filepath.Join(root, "nested", indexFilename), nestedBefore, 0o640); err != nil {
		t.Fatal(err)
	}
	changed := false
	hooks := indexPublishHooks{
		afterResolve: func() error {
			changed = true
			return os.WriteFile(filepath.Join(root, indexFilename), rootConcurrent, 0o600)
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "0.2", hooks)

	// Assert.
	const wantError = `invalid index destination "index.md": destination changed after version resolution`
	if !changed {
		t.Fatal("root change after resolution did not run")
	}
	if err == nil || err.Error() != wantError {
		t.Fatalf("regenerateIndexesWithHooks() error = %v, want %q", err, wantError)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	assertFileBytes(t, filepath.Join(root, indexFilename), rootConcurrent)
	assertFileBytes(t, filepath.Join(root, "nested", indexFilename), nestedBefore)
	assertNoIndexTransactionArtifacts(t, root)
}

func TestRegenerateIndexesRejectsUnsafeAncestorBeforeStaging(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		replace   func(t *testing.T, nested, parked, external string)
		wantError string
	}{
		{
			name: "symlink",
			replace: func(t *testing.T, nested, parked, external string) {
				t.Helper()
				if err := os.Rename(nested, parked); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, nested); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			},
			wantError: `invalid index destination "nested/index.md": ancestor "nested" is a symlink`,
		},
		{
			name: "non-directory",
			replace: func(t *testing.T, nested, parked, _ string) {
				t.Helper()
				if err := os.Rename(nested, parked); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(nested, []byte("not a directory"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantError: `invalid index destination "nested/index.md": ancestor "nested" is not a directory`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			external := t.TempDir()
			writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
			writeIndexDoc(t, root, "nested/b.md", "Note", "B", "Beta.")
			nested := filepath.Join(root, "nested")
			parked := filepath.Join(root, "nested.parked")
			replaced := false
			synthesize := func(relative string, _ []IndexChild) string {
				if relative == "nested" && !replaced {
					test.replace(t, nested, parked, external)
					replaced = true
				}
				return "nested"
			}

			// Act.
			written, err := RegenerateIndexesWith(root, synthesize)

			// Assert.
			if !replaced {
				t.Fatal("ancestor replacement did not run")
			}
			if err == nil || err.Error() != test.wantError {
				t.Fatalf("RegenerateIndexesWith() error = %v, want %q", err, test.wantError)
			}
			if written != nil {
				t.Fatalf("written = %#v, want nil", written)
			}
			assertPathDoesNotExist(t, filepath.Join(parked, indexFilename))
			assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
			assertNoIndexTransactionArtifacts(t, parked)
		})
	}
}

func TestRegenerateIndexesRollsBackEveryPublishedPrefix(t *testing.T) {
	t.Parallel()

	for failureOrdinal := 0; failureOrdinal < 3; failureOrdinal++ {
		failureOrdinal := failureOrdinal
		t.Run(itoa(failureOrdinal), func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeIndexDoc(t, root, "a/one.md", "Note", "One", "First.")
			writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Second.")
			before := map[string][]byte{
				"a/index.md": []byte("# A before\n"),
				"b/index.md": []byte("# B before\n"),
				"index.md":   []byte("# Root before\n"),
			}
			modes := map[string]fs.FileMode{
				"a/index.md": 0o600,
				"b/index.md": 0o640,
				"index.md":   0o604,
			}
			arrange := func(t *testing.T, targetRoot string) {
				t.Helper()
				writeIndexDoc(t, targetRoot, "a/one.md", "Note", "One", "First.")
				writeIndexDoc(t, targetRoot, "b/two.md", "Note", "Two", "Second.")
				for relative, data := range before {
					filename := filepath.Join(targetRoot, filepath.FromSlash(relative))
					if err := os.WriteFile(filename, data, modes[relative]); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(filename, modes[relative]); err != nil {
						t.Fatal(err)
					}
				}
			}
			arrange(t, root)
			after := generatedIndexOracle(t, arrange, "a/index.md", "b/index.md", "index.md")
			injected := errors.New("injected after publish")
			var published []string
			hooks := indexPublishHooks{
				afterPublish: func(relative string, ordinal int) error {
					published = append(published, relative)
					if ordinal == failureOrdinal {
						return injected
					}
					return nil
				},
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if !errors.Is(err, injected) {
				t.Fatalf("failure ordinal %d: error = %v, want injected", failureOrdinal, err)
			}
			if len(published) != failureOrdinal+1 {
				t.Fatalf("published trace = %#v, want %d items", published, failureOrdinal+1)
			}
			wantState := before
			if failureOrdinal == 2 {
				wantState = after
				if len(written) != len(after) || !errors.Is(err, ErrPublicationCommitted) {
					t.Fatalf("failure ordinal %d: written=%#v error=%v, want committed", failureOrdinal, written, err)
				}
			} else if written != nil || errors.Is(err, ErrPublicationCommitted) {
				t.Fatalf("failure ordinal %d: written=%#v error=%v, want precommit rollback", failureOrdinal, written, err)
			}
			for relative, want := range wantState {
				filename := filepath.Join(root, filepath.FromSlash(relative))
				assertFileBytes(t, filename, want)
				info, statErr := os.Stat(filename)
				if statErr != nil {
					t.Fatal(statErr)
				}
				if got := info.Mode().Perm(); got != modes[relative] {
					t.Fatalf("%s mode = %04o, want %04o", relative, got, modes[relative])
				}
			}
			if failureOrdinal == 2 {
				recovered, recoveryErr := regenerateIndexesForTest(root)
				if recoveryErr != nil || len(recovered) != len(after) {
					t.Fatalf("next RegenerateIndexes()=%#v, %v", recovered, recoveryErr)
				}
			}
			assertNoIndexTransactionArtifacts(t, root)
		})
	}
}

func TestConcurrentRegenerateIndexesCannotPublishMixedGeneration(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "x/a/one.md", "Note", "One", "")
	writeIndexDoc(t, root, "x/a/two.md", "Note", "Two", "")
	writeIndexDoc(t, root, "x/b/three.md", "Note", "Three", "")
	writeIndexDoc(t, root, "x/b/four.md", "Note", "Four", "")
	ready := make(chan struct{})
	release := make(chan struct{})
	type result struct {
		written []string
		err     error
	}
	const winnerTag = "generation-A"
	const contenderTag = "generation-B"
	winnerResult := make(chan result, 1)
	go func() {
		written, err := regenerateIndexesWithTestHooks(
			root,
			func(string, []IndexChild) string { return winnerTag },
			"",
			indexPublishHooks{
				afterIndexLock: func() error {
					close(ready)
					<-release
					return nil
				},
			})

		winnerResult <- result{written: written, err: err}
	}()
	<-ready

	// Act.
	contenderWritten, contenderErr := regenerateIndexesWithTestHooks(
		root,
		func(string, []IndexChild) string { return contenderTag },
		"",
		indexPublishHooks{})

	close(release)
	winner := <-winnerResult

	// Assert.
	if contenderWritten != nil || !errors.Is(contenderErr, ErrPublicationOwnershipConflict) {
		t.Fatalf("contender = %#v, %v; want stable ownership conflict", contenderWritten, contenderErr)
	}
	if winner.err != nil || len(winner.written) == 0 {
		t.Fatalf("winner = %#v, %v; want complete success", winner.written, winner.err)
	}
	xIndex := readFile(t, root, "x/index.md")
	rootIndex := readFile(t, root, indexFilename)
	if !strings.Contains(xIndex, winnerTag) || !strings.Contains(rootIndex, winnerTag) {
		t.Fatalf("winner generation is incomplete:\nx/index.md=%q\nindex.md=%q", xIndex, rootIndex)
	}
	if strings.Contains(xIndex, contenderTag) || strings.Contains(rootIndex, contenderTag) {
		t.Fatalf("loser generation leaked into published indexes")
	}
	assertNoIndexTransactionArtifacts(t, root)

	// A fresh invocation acquires ownership immediately after release.
	freshWritten, freshErr := regenerateIndexesWithTestHooks(
		root,
		func(string, []IndexChild) string { return contenderTag },
		"",
		indexPublishHooks{})

	if freshErr != nil || len(freshWritten) == 0 {
		t.Fatalf("fresh invocation = %#v, %v; want success", freshWritten, freshErr)
	}
}

func assertFileBytes(t *testing.T, filename string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", filename, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s bytes = %q, want %q", filename, got, want)
	}
}

func generatedIndexOracle(
	t *testing.T,
	arrange func(t *testing.T, root string),
	relatives ...string,
) map[string][]byte {
	t.Helper()
	root := t.TempDir()
	arrange(t, root)
	written, err := regenerateIndexesForTest(root)
	if err != nil || len(written) != len(relatives) {
		t.Fatalf("oracle RegenerateIndexes() = %#v, %v; want %d outputs", written, err, len(relatives))
	}
	result := make(map[string][]byte, len(relatives))
	for _, relative := range relatives {
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatalf("read oracle %q: %v", relative, readErr)
		}
		result[relative] = data
	}
	return result
}

func readIndexBatchManifestGenericTest(t *testing.T, root string) indexBatchManifest {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var manifestData []byte
	var manifestName string
	for _, entry := range entries {
		if !parseIndexBatchManifestName(entry.Name()) {
			continue
		}
		if manifestName != "" {
			t.Fatalf("multiple batch manifests: %q and %q", manifestName, entry.Name())
		}
		manifestName = entry.Name()
		manifestData, err = os.ReadFile(filepath.Join(root, manifestName))
		if err != nil {
			t.Fatal(err)
		}
	}
	if manifestName == "" {
		t.Fatal("batch manifest is missing")
	}
	var manifest indexBatchManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", manifestName, err)
	}
	return manifest
}

func indexBatchManifestEntryByPathForGenericTest(
	t *testing.T,
	manifest indexBatchManifest,
	relative string,
) indexBatchManifestEntry {
	t.Helper()
	for _, entry := range manifest.Entries {
		if entry.Path == relative {
			return entry
		}
	}
	t.Fatalf("manifest has no entry for %q: %+v", relative, manifest)
	return indexBatchManifestEntry{}
}

func assertPathDoesNotExist(t *testing.T, filename string) {
	t.Helper()
	if _, err := os.Lstat(filename); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(%q) error = %v, want not exist", filename, err)
	}
}

func assertNoIndexTransactionArtifacts(t *testing.T, root string) {
	t.Helper()
	if artifacts := indexTransactionArtifacts(t, root); len(artifacts) != 0 {
		t.Fatalf("transaction artifacts remain: %#v", artifacts)
	}
}

func indexRecoveryBackups(t *testing.T, root string) []string {
	t.Helper()
	var backups []string
	for _, artifact := range indexTransactionArtifacts(t, root) {
		if strings.Contains(filepath.Base(artifact), indexTransactionPrefix+"backup-") {
			backups = append(backups, artifact)
		}
	}
	return backups
}

func indexTransactionArtifacts(t *testing.T, root string) []string {
	t.Helper()
	var artifacts []string
	err := filepath.WalkDir(root, func(pathName string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if strings.HasPrefix(entry.Name(), indexTransactionPrefix) {
			relative, err := filepath.Rel(root, pathName)
			if err != nil {
				return err
			}
			artifacts = append(artifacts, filepath.ToSlash(relative))
		}
		if entry.Type()&os.ModeSymlink != 0 && entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q) error = %v", root, err)
	}
	sort.Strings(artifacts)
	return artifacts
}

func assertRecoveryBackup(
	t *testing.T,
	root string,
	relative string,
	want []byte,
	wantMode fs.FileMode,
) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	assertFileBytes(t, filename, want)
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != wantMode {
		t.Fatalf("recovery backup %s mode = %04o, want %04o", relative, got, wantMode)
	}
}

func leaveVerifiedIndexRecoveryBackup(
	t *testing.T,
	root string,
	mode fs.FileMode,
) (destination string, artifact string, original []byte) {
	t.Helper()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	destination = filepath.Join(root, "nested", indexFilename)
	original = []byte("# Original nested index\n")
	if err := os.WriteFile(destination, original, mode.Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, mode); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected setup restore failure")
	_, err := regenerateIndexesWithTestHooks(root, nil, "", indexPublishHooks{
		afterVacate: func(relative string, _ *os.Root, _, _ string) error {
			if relative == "nested/index.md" {
				return injected
			}
			return nil
		},
		beforeRestore: func(relative string, _ *os.Root, _, _ string) error {
			if relative == "nested/index.md" {
				return injected
			}
			return nil
		},
	})

	if !errors.Is(err, injected) {
		t.Fatalf("leave recovery backup error = %v, want injected", err)
	}
	backups := indexRecoveryBackups(t, root)
	if len(backups) != 1 {
		t.Fatalf("leave recovery backups = %#v, want one", backups)
	}
	return destination, backups[0], original
}

func TestRegenerateIndexesCleansVerifiedSelfAuthenticatingStageBeforeReadingBundle(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	stageData := []byte("# Interrupted stage\n")
	artifact := writeIndexTransactionArtifact(
		t,
		root,
		"nested/index.md",
		indexArtifactStage,
		stageData,
		0o640,
		0,
	)
	if loaded, err := LoadBundle(root); loaded != nil || err == nil {
		t.Fatalf("LoadBundle() before recovery = %#v, %v; want reserved-path rejection", loaded, err)
	}

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert.
	if err != nil || len(written) == 0 {
		t.Fatalf("RegenerateIndexes() = %#v, %v; want recovery and regeneration", written, err)
	}
	assertPathDoesNotExist(t, filepath.Join(root, filepath.FromSlash(artifact)))
	assertNoIndexTransactionArtifacts(t, root)
	loaded, loadErr := LoadBundle(root)
	if loadErr != nil || loaded == nil {
		t.Fatalf("LoadBundle() after recovery = %#v, %v", loaded, loadErr)
	}
	for _, filename := range loaded.Files() {
		if isIndexTransactionReservedPath(filepath.ToSlash(filename)) {
			t.Fatalf("Bundle captured transaction artifact %q", filename)
		}
	}
}

func TestIndexTransactionArtifactAllocationUsesDeterministicExclusiveSlots(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	pinned, err := openRootWithoutSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	parent, err := openDirectory(pinned, "nested")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parentInfo, err := parent.Lstat(".")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("# Stage\n")
	destination := &indexDestination{
		root:       pinned,
		parent:     parent,
		parentInfo: parentInfo,
		directory:  "nested",
		leaf:       indexFilename,
		output:     indexOutput{relative: "nested/index.md"},
	}

	// Act.
	firstName, first, firstComplete, firstErr := createDurableIndexArtifact(
		context.Background(),
		destination,
		indexArtifactStage,
		0o640,
		data,
		indexPublishHooks{},
	)
	secondName, second, secondComplete, secondErr := createDurableIndexArtifact(
		context.Background(),
		destination,
		indexArtifactStage,
		0o640,
		data,
		indexPublishHooks{},
	)

	// Assert.
	firstSlot, firstOK := parseIndexArtifactName(firstName, indexArtifactStage)
	secondSlot, secondOK := parseIndexArtifactName(secondName, indexArtifactStage)
	if firstErr != nil || secondErr != nil ||
		!firstComplete || !secondComplete ||
		first.info == nil || second.info == nil ||
		!firstOK || !secondOK ||
		firstSlot != 0 || secondSlot != 1 {
		t.Fatalf(
			"allocations = %q/%d/%t/%v, %q/%d/%t/%v; want canonical slots 0 and 1",
			firstName,
			firstSlot,
			firstOK,
			firstErr,
			secondName,
			secondSlot,
			secondOK,
			secondErr,
		)
	}
}

func TestIndexArtifactNameTemplatePrehashesPayloadForBoundedSlotEnumeration(t *testing.T) {
	t.Parallel()

	for _, kind := range []indexArtifactKind{
		indexArtifactStage,
		indexArtifactBackup,
		indexArtifactRestore,
	} {
		kind := kind
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			// Arrange.
			data := bytes.Repeat([]byte("bounded-payload-"), 1<<18)
			template, err := newIndexArtifactNameTemplate(
				kind,
				"nested/index.md",
				0o640,
				data,
			)
			if err != nil {
				t.Fatal(err)
			}
			wantDigest := indexArtifactDigest(kind, "nested/index.md", 0o640, data)
			for index := range data {
				data[index] ^= 0xff
			}

			// Act.
			var first, last string
			for slot := 0; slot < 10_000; slot++ {
				name := template.Name(slot)
				if slot == 0 {
					first = name
				}
				if slot == 9_999 {
					last = name
				}
			}

			// Assert.
			prefix, err := indexArtifactPrefix(kind)
			if err != nil {
				t.Fatal(err)
			}
			if first != fmt.Sprintf("%s%04d-%s", prefix, 0, wantDigest) ||
				last != fmt.Sprintf("%s%04d-%s", prefix, 9_999, wantDigest) {
				t.Fatalf("template names = %q .. %q, want payload-independent prehash %s", first, last, wantDigest)
			}
			if strings.Contains(first, indexArtifactDigest(kind, "nested/index.md", 0o640, data)) {
				t.Fatal("slot enumeration rehashed caller-mutated payload")
			}
		})
	}
}

func TestIndexDurableArtifactFileSyncFaultsNeverPublishUnsyncedBytes(t *testing.T) {
	t.Parallel()

	t.Run("stage", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
		destination := filepath.Join(root, "nested", indexFilename)
		original := []byte("# Original nested index\n")
		if err := os.WriteFile(destination, original, 0o640); err != nil {
			t.Fatal(err)
		}
		fault := errors.New("injected stage file sync failure")
		faulted := false
		var trace []string
		hooks := indexPublishHooks{
			fileSync: func(_ string, kind, _ string, file *os.File) error {
				trace = append(trace, kind+":file-sync")
				if kind == string(indexArtifactStage) && !faulted {
					faulted = true
					return fault
				}
				return file.Sync()
			},
			directorySync: func(_ string, kind, _ string, parent *os.Root) error {
				trace = append(trace, kind+":directory-sync")
				return syncPublicationDirectoryPlatform(parent)
			},
		}

		// Act.
		written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

		// Assert.
		if written != nil || !faulted || !errors.Is(err, fault) {
			t.Fatalf("result=%#v, faulted=%t, error=%v; want stage sync fault", written, faulted, err)
		}
		assertFileBytes(t, destination, original)
		for index, step := range trace {
			if step == string(indexArtifactStage)+":directory-sync" {
				t.Fatalf("stage namespace sync ran after failed file sync at trace[%d]: %#v", index, trace)
			}
		}
		assertNoIndexTransactionArtifacts(t, root)
	})

	t.Run("restore", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
		destination := filepath.Join(root, "nested", indexFilename)
		original := []byte("# Original nested index\n")
		if err := os.WriteFile(destination, original, 0o640); err != nil {
			t.Fatal(err)
		}
		originalInfo, err := os.Lstat(destination)
		if err != nil {
			t.Fatal(err)
		}
		syncFault := errors.New("injected restore file sync failure")
		restoreFaulted := false
		var trace []string
		hooks := indexPublishHooks{
			fileSync: func(_ string, kind, _ string, file *os.File) error {
				trace = append(trace, kind+":file-sync")
				if kind == string(indexArtifactRestore) && !restoreFaulted {
					restoreFaulted = true
					return syncFault
				}
				return file.Sync()
			},
			directorySync: func(_ string, kind string, _ string, parent *os.Root) error {
				trace = append(trace, kind+":directory-sync")
				return syncPublicationDirectoryPlatform(parent)
			},
		}

		// Act.
		written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

		// Assert.
		if written != nil || !restoreFaulted || !errors.Is(err, syncFault) {
			t.Fatalf("result=%#v, restoreFaulted=%t, error=%v", written, restoreFaulted, err)
		}
		currentInfo, statErr := os.Lstat(destination)
		if statErr != nil || !os.SameFile(originalInfo, currentInfo) {
			t.Fatalf("original destination identity changed: before=%v after=%v error=%v", originalInfo, currentInfo, statErr)
		}
		assertFileBytes(t, destination, original)
		for index, step := range trace {
			if step == string(indexArtifactRestore)+":directory-sync" {
				t.Fatalf("restore namespace sync ran after failed file sync at trace[%d]: %#v", index, trace)
			}
		}
		assertNoIndexTransactionArtifacts(t, root)
	})
}

func TestGlobalIndexRecoveryValidatesFullInventoryBeforeMutation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "a/note.md", "Note", "A", "Alpha.")
	writeIndexDoc(t, root, "z/note.md", "Note", "Z", "Zulu.")
	valid := writeIndexTransactionArtifact(
		t,
		root,
		"a/index.md",
		indexArtifactStage,
		[]byte("# Valid stage\n"),
		0o640,
		0,
	)
	corruptExpected := []byte("# Complete expected stage\n")
	corrupt := writeIndexTransactionArtifact(
		t,
		root,
		"z/index.md",
		indexArtifactStage,
		corruptExpected,
		0o640,
		0,
	)
	if err := os.WriteFile(
		filepath.Join(root, filepath.FromSlash(corrupt)),
		[]byte("# Partial\n"),
		0o640,
	); err != nil {
		t.Fatal(err)
	}

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert: the valid lexical predecessor is retained because the complete
	// inventory contained a later corrupt artifact.
	if written != nil || err == nil ||
		!strings.Contains(err.Error(), `artifact "`+corrupt+`"`) {
		t.Fatalf("RegenerateIndexes() = %#v, %v; want corrupt later artifact", written, err)
	}
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(valid)), []byte("# Valid stage\n"))
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(corrupt)), []byte("# Partial\n"))
	assertPathDoesNotExist(t, filepath.Join(root, "a", indexFilename))
	assertPathDoesNotExist(t, filepath.Join(root, "z", indexFilename))
	repeatedWritten, repeatedErr := regenerateIndexesForTest(root)
	if repeatedWritten != nil || repeatedErr == nil || repeatedErr.Error() != err.Error() {
		t.Fatalf(
			"repeated RegenerateIndexes() = %#v, %v; want stable %q",
			repeatedWritten,
			repeatedErr,
			err,
		)
	}
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(valid)), []byte("# Valid stage\n"))
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(corrupt)), []byte("# Partial\n"))

	// Manual removal of the corrupt evidence makes the next invocation safe.
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(corrupt))); err != nil {
		t.Fatal(err)
	}
	freshWritten, freshErr := regenerateIndexesForTest(root)
	if freshErr != nil || len(freshWritten) == 0 {
		t.Fatalf("fresh RegenerateIndexes() = %#v, %v", freshWritten, freshErr)
	}
	assertPathDoesNotExist(t, filepath.Join(root, filepath.FromSlash(valid)))
	assertNoIndexTransactionArtifacts(t, root)
}

func TestGlobalIndexRecoveryRevalidatesEveryPlannedArtifactBeforeFirstUnlink(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	data := []byte("# Stage\n")
	first := writeIndexTransactionArtifact(
		t,
		root,
		"a/index.md",
		indexArtifactStage,
		data,
		0o640,
		0,
	)
	later := writeIndexTransactionArtifact(
		t,
		root,
		"z/index.md",
		indexArtifactStage,
		data,
		0o640,
		0,
	)
	mutated := false
	hooks := indexPublishHooks{
		beforeCleanup: func(relative string, _ *os.Root, kind, _ string) error {
			if relative != "a/index.md" || kind != string(indexArtifactStage) || mutated {
				return nil
			}
			mutated = true
			return os.WriteFile(
				filepath.Join(root, filepath.FromSlash(later)),
				[]byte("# Replaced later stage\n"),
				0o640,
			)
		},
	}

	// Act.
	written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

	// Assert.
	if !mutated || written != nil || err == nil ||
		!strings.Contains(err.Error(), "observed recovery artifact state changed before final verification") {
		t.Fatalf("mutated=%t result=%#v, %v; want full-plan revalidation error", mutated, written, err)
	}
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(first)), data)
	assertFileBytes(
		t,
		filepath.Join(root, filepath.FromSlash(later)),
		[]byte("# Replaced later stage\n"),
	)
}

func TestGlobalIndexRecoveryRejectsAmbiguousDuplicateArtifactsWithoutMutation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	data := []byte("# Stage\n")
	first := writeIndexTransactionArtifact(
		t,
		root,
		"nested/index.md",
		indexArtifactStage,
		data,
		0o640,
		0,
	)
	second := writeIndexTransactionArtifact(
		t,
		root,
		"nested/index.md",
		indexArtifactStage,
		data,
		0o640,
		1,
	)

	// Act.
	written, err := regenerateIndexesForTest(root)

	// Assert.
	if written != nil || err == nil ||
		!strings.Contains(err.Error(), "multiple stage transaction artifacts make recovery ambiguous") {
		t.Fatalf("RegenerateIndexes() = %#v, %v; want ambiguity error", written, err)
	}
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(first)), data)
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(second)), data)
	assertPathDoesNotExist(t, filepath.Join(root, "nested", indexFilename))
}

func TestGlobalIndexRecoveryRejectsMalformedAndAmbiguousGroupsWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(t *testing.T, root string) []string
		wantReason string
	}{
		{
			name: "unknown kind",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				relative := "nested/.okf-index-txn-foreign"
				writeFile(t, root, relative, "foreign")
				return []string{relative}
			},
			wantReason: "unknown transaction artifact kind",
		},
		{
			name: "noncanonical case",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				canonical := writeIndexTransactionArtifact(
					t,
					root,
					"nested/index.md",
					indexArtifactStage,
					[]byte("# Stage\n"),
					0o640,
					0,
				)
				upper := strings.ToUpper(path.Base(canonical))
				noncanonical := pathJoin(path.Dir(canonical), upper)
				if err := os.Rename(
					filepath.Join(root, filepath.FromSlash(canonical)),
					filepath.Join(root, filepath.FromSlash(noncanonical)),
				); err != nil {
					t.Fatal(err)
				}
				return []string{noncanonical}
			},
			wantReason: "malformed transaction artifact name",
		},
		{
			name: "multiple backups",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				data := []byte("# Original\n")
				return []string{
					writeIndexTransactionArtifact(t, root, "nested/index.md", indexArtifactBackup, data, 0o640, 0),
					writeIndexTransactionArtifact(t, root, "nested/index.md", indexArtifactBackup, data, 0o640, 1),
				}
			},
			wantReason: "multiple backup transaction artifacts make recovery ambiguous",
		},
		{
			name: "mismatched restore and backup",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{
					writeIndexTransactionArtifact(
						t,
						root,
						"nested/index.md",
						indexArtifactRestore,
						[]byte("# Restore\n"),
						0o640,
						0,
					),
					writeIndexTransactionArtifact(
						t,
						root,
						"nested/index.md",
						indexArtifactBackup,
						[]byte("# Backup\n"),
						0o640,
						0,
					),
				}
			},
			wantReason: "restore and backup artifacts do not bind the same bytes and mode",
		},
		{
			name: "stage and restore without backup",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{
					writeIndexTransactionArtifact(
						t,
						root,
						"nested/index.md",
						indexArtifactStage,
						[]byte("# Stage\n"),
						0o640,
						0,
					),
					writeIndexTransactionArtifact(
						t,
						root,
						"nested/index.md",
						indexArtifactRestore,
						[]byte("# Restore\n"),
						0o640,
						0,
					),
				}
			},
			wantReason: "stage and restore artifacts without a matching backup are ambiguous",
		},
		{
			name: "restore only with absent leaf",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{
					writeIndexTransactionArtifact(
						t,
						root,
						"nested/index.md",
						indexArtifactRestore,
						[]byte("# Possible sole original\n"),
						0o640,
						0,
					),
				}
			},
			wantReason: "ownership claim has no exact all-old target without a batch manifest",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			artifacts := test.setup(t, root)

			// Act.
			written, err := regenerateIndexesForTest(root)

			// Assert.
			if written != nil || err == nil || !strings.Contains(err.Error(), test.wantReason) {
				t.Fatalf("RegenerateIndexes() = %#v, %v; want %q", written, err, test.wantReason)
			}
			for _, artifact := range artifacts {
				if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(artifact))); statErr != nil {
					t.Fatalf("artifact %q was mutated: %v", artifact, statErr)
				}
			}
			assertPathDoesNotExist(t, filepath.Join(root, "nested", indexFilename))
		})
	}
}

func TestGlobalIndexRecoveryReportsLexicalFirstReservedArtifactError(t *testing.T) {
	t.Parallel()

	for _, order := range [][]string{
		{"z/.okf-index-txn-unknown", "a/.okf-index-txn-unknown"},
		{"a/.okf-index-txn-unknown", "z/.okf-index-txn-unknown"},
	} {
		order := order
		t.Run(strings.Join(order, ","), func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			for _, relative := range order {
				writeFile(t, root, relative, "foreign")
			}

			// Act.
			written, err := regenerateIndexesForTest(root)

			// Assert.
			if written != nil || err == nil ||
				!strings.Contains(err.Error(), `artifact "a/.okf-index-txn-unknown"`) {
				t.Fatalf("RegenerateIndexes() = %#v, %v; want lexical a/ error", written, err)
			}
			for _, relative := range order {
				assertFileBytes(t, filepath.Join(root, filepath.FromSlash(relative)), []byte("foreign"))
			}
		})
	}
}

func TestGlobalIndexRecoveryRejectsInstalledStageWithoutManifest(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	data := []byte("# Installed stage\n")
	artifact := writeIndexTransactionArtifact(
		t,
		root,
		"nested/index.md",
		indexArtifactStage,
		data,
		0o640,
		0,
	)
	artifactFilename := filepath.Join(root, filepath.FromSlash(artifact))
	leaf := filepath.Join(root, "nested", indexFilename)
	if err := os.Link(artifactFilename, leaf); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(leaf)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := openRootWithoutSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	treeBefore := documentSessionCaptureTree(t, root)

	// Act.
	recoveryErr := inspectGlobalIndexRecoveryNamespace(pinned, indexPublishHooks{})

	// Assert.
	if recoveryErr == nil || !strings.Contains(recoveryErr.Error(), "published stage has no durable batch manifest") {
		t.Fatalf("recovery error = %v, want missing manifest blocker", recoveryErr)
	}
	assertFileBytes(t, artifactFilename, data)
	assertFileBytes(t, leaf, data)
	after, err := os.Lstat(leaf)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("installed leaf identity changed: before=%v after=%v err=%v", before, after, err)
	}
	documentSessionAssertTreeSnapshotEqual(t, treeBefore, documentSessionCaptureTree(t, root))
}

func TestGlobalIndexRecoveryRejectsAbsentLeafBackupWithoutManifest(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	data := []byte("# Original\n")
	artifact := writeIndexTransactionArtifact(
		t,
		root,
		"nested/index.md",
		indexArtifactBackup,
		data,
		0o640,
		0,
	)
	pinned, err := openRootWithoutSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	before := documentSessionCaptureTree(t, root)

	// Act.
	recoveryErr := inspectGlobalIndexRecoveryNamespace(pinned, indexPublishHooks{})

	// Assert.
	if recoveryErr == nil || !strings.Contains(recoveryErr.Error(), "has no exact all-old target without a batch manifest") {
		t.Fatalf("recovery error = %v, want missing manifest blocker", recoveryErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, documentSessionCaptureTree(t, root))
	assertPathDoesNotExist(t, filepath.Join(root, "nested", indexFilename))
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(artifact)), data)
}

func TestGlobalIndexRecoveryRejectsRestoreAndBackupWithoutManifest(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	data := []byte("# Original\n")
	restore := writeIndexTransactionArtifact(
		t,
		root,
		"nested/index.md",
		indexArtifactRestore,
		data,
		0o640,
		0,
	)
	backup := writeIndexTransactionArtifact(
		t,
		root,
		"nested/index.md",
		indexArtifactBackup,
		data,
		0o640,
		0,
	)
	pinned, err := openRootWithoutSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	before := documentSessionCaptureTree(t, root)

	// Act.
	recoveryErr := inspectGlobalIndexRecoveryNamespace(pinned, indexPublishHooks{})

	// Assert.
	if recoveryErr == nil || !strings.Contains(recoveryErr.Error(), "has no exact all-old target without a batch manifest") {
		t.Fatalf("recovery error = %v, want missing manifest blocker", recoveryErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, documentSessionCaptureTree(t, root))
	assertPathDoesNotExist(t, filepath.Join(root, "nested", indexFilename))
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(restore)), data)
	assertFileBytes(t, filepath.Join(root, filepath.FromSlash(backup)), data)
}

func writeIndexTransactionArtifact(
	t *testing.T,
	root string,
	destination string,
	kind indexArtifactKind,
	data []byte,
	mode fs.FileMode,
	slot int,
) string {
	t.Helper()
	directory := path.Dir(destination)
	if directory == "." {
		directory = ""
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
		t.Fatal(err)
	}
	name := indexArtifactNameFor(kind, destination, mode, data, slot)
	relative := pathJoin(directory, name)
	filename := filepath.Join(root, filepath.FromSlash(relative))
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return relative
}
