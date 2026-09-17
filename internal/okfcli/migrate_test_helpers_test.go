package okfcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
)

func runMigrateJSON(t *testing.T, root string, write bool) (int, migrationReport, string) {
	t.Helper()
	return runMigrateJSONRequest(t, root, "auto", "human:test", write)
}

func runMigrateJSONRequest(t *testing.T, root, from, actor string, write bool) (int, migrationReport, string) {
	t.Helper()
	return runMigrateJSONRequestWithMappings(t, root, from, actor, "", write)
}

func runMigrateJSONRequestWithMappings(
	t *testing.T,
	root, from, actor, mappingsPath string,
	write bool,
) (int, migrationReport, string) {
	t.Helper()
	args := []string{"migrate", root, "--to", "0.2", "--from", from, "--format", "json"}
	if actor != "" {
		args = append(args, "--actor", actor)
	}
	if mappingsPath != "" {
		args = append(args, "--citation-mappings", mappingsPath)
	}
	if write {
		args = append(args, "--write")
	}
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	var report migrationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	return code, report, stderr.String()
}

func containsMigrationBlocker(blockers []migrationBlockerDTO, code string) bool {
	for _, blocker := range blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}

type migrationRecoveryRecorder struct {
	delegate mutation.MigrationPlanner
	previews []migrationRecoveryPreviewCall
	applies  []migrationRecoveryApplyCall
}

type migrationRecoveryPreviewCall struct {
	request mutation.MigrationRequest
	result  mutation.MigrationPreview
	err     error
}

type migrationRecoveryApplyCall struct {
	request mutation.MigrationApplyRequest
	receipt store.CommitReceipt
	err     error
}

func (recorder *migrationRecoveryRecorder) Preview(
	ctx context.Context,
	source bundle.Source,
	resolution mutation.MigrationSourceResolution,
	request mutation.MigrationRequest,
) (mutation.MigrationPreview, error) {
	preview, err := recorder.delegate.Preview(ctx, source, resolution, request)
	recorder.previews = append(recorder.previews, migrationRecoveryPreviewCall{
		request: request,
		result:  preview,
		err:     err,
	})
	return preview, err
}

func (recorder *migrationRecoveryRecorder) Apply(
	ctx context.Context,
	destination store.Store,
	request mutation.MigrationApplyRequest,
) (store.CommitReceipt, error) {
	receipt, err := recorder.delegate.Apply(ctx, destination, request)
	recorder.applies = append(recorder.applies, migrationRecoveryApplyCall{
		request: request,
		receipt: receipt,
		err:     err,
	})
	return receipt, err
}

func runMigrationRecoveryCommand(
	t *testing.T,
	root string,
	dependencies migrateDependencies,
) (int, migrationReport, []byte, error) {
	t.Helper()
	args := []string{
		root,
		"--to", "0.2",
		"--from", "auto",
		"--actor", "human:recovery-test",
		"--write",
		"--format", "json",
	}
	var stdout bytes.Buffer
	code, err := cmdMigrateWithDependencies(args, &stdout, dependencies)
	if err != nil {
		return code, migrationReport{}, stdout.Bytes(), err
	}
	var report migrationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; stdout=%q", err, stdout.String())
	}
	return code, report, stdout.Bytes(), nil
}

func writeMigrationRecoveryFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"index.md":       "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n- [Beta](nested/beta.md)\n",
		"alpha.md":       "---\ntype: Knowledge\ntimestamp: 2026-07-01T09:00:00Z\n---\n\nAlpha.\n",
		"nested/beta.md": "---\ntype: Knowledge\ntimestamp: 2026-07-02T09:00:00Z\n---\n\nBeta.\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func snapshotMigrationSource(t *testing.T, source bundle.Source) map[string]string {
	t.Helper()
	if source == nil {
		t.Fatal("migration preview has nil staged source")
	}
	paths, err := source.Paths(context.Background())
	if err != nil {
		t.Fatalf("staged source Paths() error = %v", err)
	}
	files := make(map[string]string, len(paths))
	for _, name := range paths {
		content, err := source.ReadFile(context.Background(), name)
		if err != nil {
			t.Fatalf("staged source ReadFile(%q) error = %v", name, err)
		}
		files[name] = string(content)
	}
	return files
}

type recordingMigrationPlanner struct {
	delegate mutation.MigrationPlanner
	previews []mutation.MigrationRequest
	applies  []mutation.MigrationApplyRequest
}

func (planner *recordingMigrationPlanner) Preview(
	ctx context.Context,
	source bundle.Source,
	resolution mutation.MigrationSourceResolution,
	request mutation.MigrationRequest,
) (mutation.MigrationPreview, error) {
	planner.previews = append(planner.previews, request)
	return planner.delegate.Preview(ctx, source, resolution, request)
}

func (planner *recordingMigrationPlanner) Apply(
	ctx context.Context,
	destination store.Store,
	request mutation.MigrationApplyRequest,
) (store.CommitReceipt, error) {
	planner.applies = append(planner.applies, request)
	return planner.delegate.Apply(ctx, destination, request)
}

func assertClosedMigrationOperationRequests(
	t *testing.T,
	planner *recordingMigrationPlanner,
	wantPreviews int,
	wantApplies int,
) {
	t.Helper()
	if len(planner.previews) != wantPreviews || len(planner.applies) != wantApplies {
		t.Fatalf("planner preview/apply calls = %d/%d, want %d/%d",
			len(planner.previews), len(planner.applies), wantPreviews, wantApplies)
	}
	for _, request := range planner.previews {
		if request.FromVersion != mutation.MigrationVersionV01 ||
			request.ToVersion != mutation.MigrationVersionV02 {
			t.Fatalf("preview operation tuple = %s -> %s, want 0.1 -> 0.2",
				request.FromVersion, request.ToVersion)
		}
	}
	for _, apply := range planner.applies {
		if apply.Request.FromVersion != mutation.MigrationVersionV01 ||
			apply.Request.ToVersion != mutation.MigrationVersionV02 {
			t.Fatalf("apply operation tuple = %s -> %s, want 0.1 -> 0.2",
				apply.Request.FromVersion, apply.Request.ToVersion)
		}
	}
}

func runMigrateWithDependenciesJSON(
	t *testing.T,
	root string,
	write bool,
	from string,
	dependencies migrateDependencies,
) (int, migrationReport, error) {
	t.Helper()
	args := []string{root, "--to", "0.2", "--from", from, "--format", "json"}
	if write {
		args = append(args, "--write")
	}
	var stdout bytes.Buffer
	code, err := cmdMigrateWithDependencies(args, &stdout, dependencies)
	if err != nil {
		return code, migrationReport{}, err
	}
	var report migrationReport
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("json.Unmarshal() error = %v; stdout=%q", decodeErr, stdout.String())
	}
	return code, report, nil
}

type lifecycleSource struct {
	bundle.Source
	closer     io.Closer
	closeErr   error
	closeCalls int
}

func (source *lifecycleSource) Close() error {
	source.closeCalls++
	var delegateErr error
	if source.closer != nil {
		delegateErr = source.closer.Close()
	}
	return errors.Join(delegateErr, source.closeErr)
}

type lifecycleStore struct {
	closeErr   error
	closeCalls int
}

func (*lifecycleStore) Snapshot(context.Context) (store.Snapshot, error) {
	return nil, errors.New("unexpected lifecycle store Snapshot call")
}

func (*lifecycleStore) Preview(context.Context, store.ChangeSet) (store.Preview, error) {
	return store.Preview{}, errors.New("unexpected lifecycle store Preview call")
}

func (*lifecycleStore) Commit(
	context.Context,
	store.ChangeSet,
	store.CommitOptions,
) (store.CommitReceipt, error) {
	return store.CommitReceipt{}, errors.New("unexpected lifecycle store Commit call")
}

func (backend *lifecycleStore) Close() error {
	backend.closeCalls++
	return backend.closeErr
}

type lifecyclePlanner struct {
	delegate   migrationPlanner
	previewErr error
	apply      func(mutation.MigrationApplyRequest) (store.CommitReceipt, error)
}

func (planner *lifecyclePlanner) Preview(
	ctx context.Context,
	source bundle.Source,
	resolution mutation.MigrationSourceResolution,
	request mutation.MigrationRequest,
) (mutation.MigrationPreview, error) {
	if planner.previewErr != nil {
		return mutation.MigrationPreview{}, planner.previewErr
	}
	return planner.delegate.Preview(ctx, source, resolution, request)
}

func (planner *lifecyclePlanner) Apply(
	_ context.Context,
	_ store.Store,
	request mutation.MigrationApplyRequest,
) (store.CommitReceipt, error) {
	return planner.apply(request)
}

func validLifecycleReceipt(request mutation.MigrationApplyRequest) store.CommitReceipt {
	return store.CommitReceipt{
		FormatVersion:  store.CommitReceiptFormatVersion,
		ChangeSetID:    request.Request.ID,
		IdempotencyKey: request.Options.IdempotencyKey,
		RequestDigest:  request.Proof.RequestDigest,
		BaseRevision:   request.Proof.BaseRevision,
		ResultRevision: request.Proof.ResultRevision,
		CommitTime:     time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC),
		ChangedRefs:    append([]bundle.RelationRef{}, request.Proof.ChangedRefs...),
		ChangedFiles:   append([]store.FileChange{}, request.Proof.ChangedFiles...),
	}
}

func lifecycleDependencies(
	t *testing.T,
	root string,
	planner *lifecyclePlanner,
	sourceCloseErr error,
	storeCloseErr error,
) (migrateDependencies, *lifecycleSource, *lifecycleStore) {
	t.Helper()
	dependencies := productionMigrateDependencies()
	dependencies.planner = planner
	physicalSource := &bundle.FileSystemSource{Root: root}
	source := &lifecycleSource{
		Source:   physicalSource,
		closer:   physicalSource,
		closeErr: sourceCloseErr,
	}
	backend := &lifecycleStore{closeErr: storeCloseErr}
	dependencies.openSource = func(context.Context, string) (migrationSource, error) {
		return source, nil
	}
	dependencies.openStore = func(context.Context, string) (migrationStore, error) {
		return backend, nil
	}
	return dependencies, source, backend
}

func changedMigrationFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"),
		"---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n")
	writeFixtureFile(t, filepath.Join(root, "alpha.md"),
		"---\ntype: Knowledge\ntimestamp: 2026-08-07T12:00:00Z\n---\n\nAlpha.\n")
	return root
}

func directClassifierReceipt(t *testing.T) store.CommitReceipt {
	t.Helper()
	ref, err := bundle.ParseRelationRef("alpha")
	if err != nil {
		t.Fatalf("parse relation ref: %v", err)
	}
	receipt := store.CommitReceipt{
		FormatVersion:  store.CommitReceiptFormatVersion,
		ChangeSetID:    "migration-direct",
		IdempotencyKey: "migration-direct-key",
		RequestDigest:  "sha256:" + strings.Repeat("2", 64),
		BaseRevision:   store.Revision("sha256:" + strings.Repeat("0", 64)),
		ResultRevision: store.Revision("sha256:" + strings.Repeat("1", 64)),
		CommitTime:     time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC),
		ChangedRefs:    []bundle.RelationRef{ref},
		ChangedFiles:   []store.FileChange{{Kind: store.FileWrite, Path: "alpha.md"}},
	}
	if err := store.ValidateCommitReceipt(receipt); err != nil {
		t.Fatalf("direct classifier receipt invalid: %v", err)
	}
	return receipt
}

type citationReadCloser struct {
	reader     *bytes.Reader
	readErr    error
	closeErr   error
	readCalls  int
	closeCalls int
}

func (reader *citationReadCloser) Read(buffer []byte) (int, error) {
	reader.readCalls++
	if reader.readErr != nil {
		return 0, reader.readErr
	}
	return reader.reader.Read(buffer)
}

func (reader *citationReadCloser) Close() error {
	reader.closeCalls++
	return reader.closeErr
}
