package fs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/receiptprojection"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestV02SnapshotPreservesMetadataAndAssetsByteExact(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	want := loadV02DurabilityFixture(t)
	writeV02DurabilityFixture(t, root, want)
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	trackV02Store(t, s)

	// Act.
	snapshot, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	assertV02SourceFiles(t, snapshot, want)
	assertV02StoredAttesterContract(t, snapshot)
}

func TestV02RevisionChangesForEveryMetadataAndAssetByteChange(t *testing.T) {
	fixture := loadV02DurabilityFixture(t)
	paths := sortedV02FixturePaths(fixture)
	for _, changedPath := range paths {
		changedPath := changedPath
		t.Run(strings.ReplaceAll(changedPath, "/", "_"), func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeV02DurabilityFixture(t, root, fixture)
			s, err := openObserved(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			trackV02Store(t, s)
			base, err := s.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			changed := append([]byte(nil), fixture[changedPath]...)
			changed[len(changed)/2] ^= 0x01

			// Act.
			writeTestFile(t, root, changedPath, string(changed))
			next, err := s.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			// Assert.
			if next.Revision() == base.Revision() {
				t.Fatalf("revision did not change after byte mutation of %q", changedPath)
			}
		})
	}
}

func TestV02RevisionChangesForVersionOnlyIndexMigration(t *testing.T) {
	// Arrange.
	v02Files := loadV02DurabilityFixture(t)
	v01IndexFiles := cloneV02Fixture(v02Files)
	v01IndexFiles["index.md"] = bytes.Replace(v01IndexFiles["index.md"], []byte(`okf_version: "0.2"`), []byte(`okf_version: "0.1"`), 1)
	root := t.TempDir()
	writeV02DurabilityFixture(t, root, v01IndexFiles)
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	trackV02Store(t, s)
	v01, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	writeTestFile(t, root, "index.md", string(v02Files["index.md"]))
	v02, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if bytes.Equal(v01IndexFiles["index.md"], v02Files["index.md"]) {
		t.Fatal("fixture did not perform a version-only index migration")
	}
	for path, want := range v02Files {
		if path == "index.md" {
			continue
		}
		if !bytes.Equal(v01IndexFiles[path], want) {
			t.Fatalf("version migration changed non-index file %q", path)
		}
	}
	if v01.Revision() == v02.Revision() {
		t.Fatal("revision did not change for version-only index migration")
	}
}

func TestV01FallbackAndV02FinalStatePassStagedValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string][]byte
	}{
		{name: "v0.1 fallback", files: v01DurabilityFixture()},
		{name: "v0.2 final", files: loadV02DurabilityFixture(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			s, err := openObserved(root, Config{ValidatorConfig: &validator.ValidatorConfig{Strict: true}})
			if err != nil {
				t.Fatal(err)
			}
			trackV02Store(t, s)
			staged, err := newSnapshot(context.Background(), tc.files)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			report, blocked, _, err := s.validateStagedSource(context.Background(), staged)

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if blocked || report.ErrorCount() != 0 {
				t.Fatalf("staged validation blocked=%v errors=%d diagnostics=%#v", blocked, report.ErrorCount(), report.Diagnostics)
			}
		})
	}
}

func TestV02PublicPreviewPreservesRealValidatorDiagnostics(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	files := loadV02DurabilityFixture(t)
	const conceptPath = "computations/revenue.md"
	concept := string(files[conceptPath])
	concept = strings.Replace(concept,
		"generated: { by: reference_agent/gemini-2.5-pro, at:",
		"generated: { by: team:finance-writer, at:", 1)
	concept = strings.Replace(concept,
		"receipt: [query_id, executed_sql, result]",
		"receipt: [query_id, executed_sql, executed_sql, result]", 1)
	if !strings.Contains(concept, "team:finance-writer") ||
		!strings.Contains(concept, "executed_sql, executed_sql") {
		t.Fatal("diagnostic fixture mutations were not applied")
	}
	files[conceptPath] = []byte(concept)
	writeV02DurabilityFixture(t, root, files)
	s, err := openObserved(root, Config{ValidatorConfig: &validator.ValidatorConfig{Strict: true}})
	if err != nil {
		t.Fatal(err)
	}
	trackV02Store(t, s)
	base, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	change := v02DurabilityChange(t, "v02-real-preview-diagnostics", base.Revision())

	// Act.
	preview, err := s.Preview(context.Background(), change)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]store.Diagnostic{
		"actor_invalid": {
			Kind:     store.DiagnosticValidation,
			Severity: store.DiagnosticWarning,
			Code:     "actor_invalid",
			File:     conceptPath,
			Message:  "'generated.by' should follow the OKF actor convention",
		},
		"receipt_field_duplicate": {
			Kind:     store.DiagnosticValidation,
			Severity: store.DiagnosticWarning,
			Code:     "receipt_field_duplicate",
			File:     conceptPath,
			Message:  `receipt field "executed_sql" is duplicated`,
		},
	}
	if len(preview.Diagnostics) != len(want) {
		t.Fatalf("Preview.Diagnostics=%#v want exactly %d real validator findings", preview.Diagnostics, len(want))
	}
	for _, got := range preview.Diagnostics {
		expected, ok := want[got.Code]
		if !ok {
			t.Fatalf("unexpected Preview diagnostic %#v", got)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("Preview diagnostic for %q=%#v want %#v", got.Code, got, expected)
		}
	}
}

func TestV01AndV02UseSamePublicDurableTransactionProtocol(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string][]byte
	}{
		{name: "v0.1", files: v01DurabilityFixture()},
		{name: "v0.2", files: loadV02DurabilityFixture(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeV02DurabilityFixture(t, root, tc.files)
			armed := false
			fired := false
			journalRenamed := false
			s, err := openObserved(root, Config{PostFault: func(step Step) error {
				if step == StepJournalRename {
					journalRenamed = true
				}
				if armed && journalRenamed && step == StepJournalDirectorySync && !fired {
					fired = true
					return errors.New("injected crash after durable journal sync")
				}
				return nil
			}})

			if err != nil {
				t.Fatal(err)
			}
			closeSource := trackV02Store(t, s)
			base, err := s.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			change := v02DurabilityChange(t, "durable-protocol-"+tc.name, base.Revision())
			preview, err := s.Preview(context.Background(), change)
			if err != nil {
				t.Fatal(err)
			}
			expected := applyV02Preview(t, tc.files, preview)
			options := store.CommitOptions{IdempotencyKey: store.IdempotencyKey("durable-protocol-" + tc.name)}
			// Act.
			armed = true
			failedReceipt, commitErr := s.Commit(context.Background(), change, options)
			closeSource()
			reopened, reopenErr := openObserved(root, Config{})
			if reopenErr != nil {
				t.Fatal(reopenErr)
			}
			trackV02Store(t, reopened)
			firstReplay, firstReplayErr := reopened.Commit(context.Background(), change, options)
			secondReplay, secondReplayErr := reopened.Commit(context.Background(), change, options)
			recovered, snapshotErr := reopened.Snapshot(context.Background())

			// Assert.
			var committed *store.CommittedError
			if commitErr == nil || !fired || !errors.As(commitErr, &committed) || !reflect.DeepEqual(failedReceipt, committed.Receipt()) {
				t.Fatalf("commit error=%v durable-journal fault fired=%v", commitErr, fired)
			}
			if firstReplayErr != nil || secondReplayErr != nil {
				t.Fatalf("first replay=%v second replay=%v", firstReplayErr, secondReplayErr)
			}
			if !reflect.DeepEqual(firstReplay, secondReplay) {
				t.Fatalf("idempotent receipts differ:\nfirst:  %#v\nsecond: %#v", firstReplay, secondReplay)
			}
			if !reflect.DeepEqual(firstReplay, failedReceipt) {
				t.Fatalf("replayed receipt differs from committed result:\ncommit: %#v\nreplay: %#v", failedReceipt, firstReplay)
			}
			if firstReplay.FormatVersion != 1 {
				t.Fatalf("receipt format version=%d want literal 1", firstReplay.FormatVersion)
			}
			if firstReplay.BaseRevision != base.Revision() || firstReplay.ResultRevision != preview.ResultRevision {
				t.Fatalf("receipt revisions base=%s result=%s want base=%s result=%s", firstReplay.BaseRevision, firstReplay.ResultRevision, base.Revision(), preview.ResultRevision)
			}
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			if recovered.Revision() != preview.ResultRevision {
				t.Fatalf("recovered revision=%s want %s", recovered.Revision(), preview.ResultRevision)
			}
			assertV02SourceFiles(t, recovered, expected)
		})
	}
}

func TestV02CommitReceiptReplayIsIdempotentAndPreservesAssets(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	fixture := loadV02DurabilityFixture(t)
	writeV02DurabilityFixture(t, root, fixture)
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	trackV02Store(t, s)
	base, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	change := v02DurabilityChange(t, "v02-receipt-replay", base.Revision())
	options := store.CommitOptions{IdempotencyKey: "v02-receipt-replay"}
	preview, err := s.Preview(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	expected := applyV02Preview(t, fixture, preview)

	// Act.
	first, firstErr := s.Commit(context.Background(), change, options)
	afterFirst, snapshotErr := s.Snapshot(context.Background())
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	replayed, replayErr := s.Commit(context.Background(), change, options)
	afterReplay, replaySnapshotErr := s.Snapshot(context.Background())
	if replaySnapshotErr != nil {
		t.Fatal(replaySnapshotErr)
	}

	// Assert.
	if firstErr != nil || replayErr != nil {
		t.Fatalf("first commit=%v replay=%v", firstErr, replayErr)
	}
	if !reflect.DeepEqual(replayed, first) {
		t.Fatalf("replayed receipt differs:\nfirst:  %#v\nreplay: %#v", first, replayed)
	}
	if afterFirst.Revision() != first.ResultRevision || afterReplay.Revision() != first.ResultRevision {
		t.Fatalf("snapshot revisions first=%s replay=%s receipt=%s", afterFirst.Revision(), afterReplay.Revision(), first.ResultRevision)
	}
	if first.ResultRevision != preview.ResultRevision {
		t.Fatalf("receipt result revision=%s preview=%s", first.ResultRevision, preview.ResultRevision)
	}
	if len(first.ChangedFiles) != 1 || first.ChangedFiles[0].Kind != store.FileWrite || first.ChangedFiles[0].Path != "computations/revenue.md" {
		t.Fatalf("changed files=%#v, want only computation metadata write", first.ChangedFiles)
	}
	assertV02SourceFiles(t, afterFirst, expected)
	assertV02SourceFiles(t, afterReplay, expected)
}

func TestV02AssetOnlyCommitReceiptIsCanonicalDurableAndReplayExact(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "sql", path: "references/computations/revenue.sql"},
		{name: "attester", path: "references/attesters/sql-equality.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			ctx := context.Background()
			root := t.TempDir()
			fixture := loadV02DurabilityFixture(t)
			writeV02DurabilityFixture(t, root, fixture)
			s, err := openObserved(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			closeSource := trackV02Store(t, s)
			baseSource, err := s.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			base := baseSource.(*snapshot)
			expectedFiles := cloneV02Fixture(fixture)
			changed := append([]byte(nil), expectedFiles[tc.path]...)
			changed[len(changed)/2] ^= 0x01
			expectedFiles[tc.path] = changed
			assertV02ExactlyOneChangedByte(t, fixture, expectedFiles, tc.path)
			next, err := newSnapshot(ctx, expectedFiles)
			if err != nil {
				t.Fatal(err)
			}
			changeID := "v02-asset-only-" + tc.name
			change := v02DurabilityChange(t, changeID, base.Revision())
			requestDigest, err := change.RequestDigest()
			if err != nil {
				t.Fatal(err)
			}
			options := store.CommitOptions{IdempotencyKey: store.IdempotencyKey(changeID)}
			wantFiles := []store.FileChange{{Kind: store.FileWrite, Path: tc.path}}
			wantRefs := make([]bundle.RelationRef, 0)
			wantReceipt := store.CommitReceipt{
				FormatVersion:  1,
				ChangeSetID:    store.ChangeSetID(changeID),
				IdempotencyKey: options.IdempotencyKey,
				RequestDigest:  requestDigest,
				BaseRevision:   base.Revision(),
				ResultRevision: next.Revision(),
				CommitTime:     time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
				ChangedRefs:    wantRefs,
				ChangedFiles:   wantFiles,
			}
			projection, err := receiptprojection.Derive(ctx, base.concepts, next.concepts)
			if err != nil {
				t.Fatal(err)
			}
			receiptEnvelopePath := filepath.Join(root, filepath.FromSlash(s.receiptPath(options.IdempotencyKey)))

			// Act.
			publishErr := s.publish(ctx, next, wantReceipt)
			rawEnvelope, envelopeReadErr := os.ReadFile(receiptEnvelopePath)
			var envelope receiptFile
			var envelopeDecodeErr error
			if envelopeReadErr == nil {
				envelope, envelopeDecodeErr = decodeReceiptFile(rawEnvelope)
			}
			afterPublish, publishSnapshotErr := s.Snapshot(ctx)
			closeSource()
			reopened, reopenErr := openObserved(root, Config{})
			if reopenErr != nil {
				t.Fatal(reopenErr)
			}
			trackV02Store(t, reopened)
			firstReplay, firstReplayErr := reopened.Commit(ctx, change, options)
			secondReplay, secondReplayErr := reopened.Commit(ctx, change, options)
			afterReplay, replaySnapshotErr := reopened.Snapshot(ctx)

			// Assert.
			if publishErr != nil || publishSnapshotErr != nil {
				t.Fatalf("publish error=%v snapshot error=%v", publishErr, publishSnapshotErr)
			}
			if envelopeReadErr != nil || envelopeDecodeErr != nil {
				t.Fatalf("durable receipt envelope read=%v decode=%v", envelopeReadErr, envelopeDecodeErr)
			}
			if envelope.Version != 2 {
				t.Fatalf("durable receipt envelope version=%d want literal 2", envelope.Version)
			}
			if envelope.Key != string(options.IdempotencyKey) || envelope.Digest != requestDigest {
				t.Fatalf(
					"durable receipt binding key=%q digest=%q want key=%q digest=%q",
					envelope.Key,
					envelope.Digest,
					options.IdempotencyKey,
					requestDigest,
				)
			}
			if !reflect.DeepEqual(envelope.Receipt, wantReceipt) {
				t.Fatalf("durable envelope receipt=%#v want exact %#v", envelope.Receipt, wantReceipt)
			}
			if base.Revision() == next.Revision() {
				t.Fatal("asset-only one-byte mutation did not change result revision")
			}
			if !reflect.DeepEqual(projection.ChangedFiles, wantFiles) {
				t.Fatalf("projection ChangedFiles=%#v want exact asset-only write %#v", projection.ChangedFiles, wantFiles)
			}
			if projection.ChangedRefs == nil || len(projection.ChangedRefs) != 0 {
				t.Fatalf("projection ChangedRefs=%#v want non-nil empty semantic delta", projection.ChangedRefs)
			}
			if firstReplayErr != nil || secondReplayErr != nil {
				t.Fatalf("first replay=%v second replay=%v", firstReplayErr, secondReplayErr)
			}
			if firstReplay.FormatVersion != 1 {
				t.Fatalf("durable receipt format=%d want literal v1", firstReplay.FormatVersion)
			}
			if firstReplay.ChangedRefs == nil || secondReplay.ChangedRefs == nil {
				t.Fatalf("replayed ChangedRefs must remain non-nil empty: first=%#v second=%#v", firstReplay.ChangedRefs, secondReplay.ChangedRefs)
			}
			if !reflect.DeepEqual(firstReplay, wantReceipt) || !reflect.DeepEqual(secondReplay, wantReceipt) {
				t.Fatalf("durable replay escaped exact canonical receipt:\nwant:   %#v\nfirst:  %#v\nsecond: %#v", wantReceipt, firstReplay, secondReplay)
			}
			if afterPublish.Revision() != wantReceipt.ResultRevision || replaySnapshotErr != nil || afterReplay.Revision() != wantReceipt.ResultRevision {
				t.Fatalf("snapshot revisions publish=%s replay=%s want=%s replay error=%v", afterPublish.Revision(), afterReplay.Revision(), wantReceipt.ResultRevision, replaySnapshotErr)
			}
			assertV02SourceFiles(t, afterPublish, expectedFiles)
			assertV02SourceFiles(t, afterReplay, expectedFiles)
		})
	}
}

func TestV02RecoveryRejectsInvalidStagedAssetWithoutPartialVisibleWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, payloadPath string)
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, payloadPath string) {
				t.Helper()
				if err := os.Remove(payloadPath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tampered",
			mutate: func(t *testing.T, payloadPath string) {
				t.Helper()
				data, err := os.ReadFile(payloadPath)
				if err != nil {
					t.Fatal(err)
				}
				data[len(data)/2] ^= 0x01
				if err := os.WriteFile(payloadPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, payloadPath string) {
				t.Helper()
				outside := filepath.Join(t.TempDir(), "outside.sql")
				if err := os.WriteFile(outside, []byte("SELECT 'outside';\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(payloadPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, payloadPath); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			baseFiles := v01DurabilityFixture()
			writeV02DurabilityFixture(t, root, baseFiles)
			base, err := newSnapshot(context.Background(), baseFiles)
			if err != nil {
				t.Fatal(err)
			}
			final, err := newSnapshot(context.Background(), loadV02DurabilityFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			receipt := testJournalReceipt(base.Revision(), final.Revision(), "v02-invalid-staged-asset-"+tc.name, "")
			fixtureStore, err := openObserved(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			staged := stageDurableJournalFixture(t, fixtureStore, final, receipt)
			payloadPath := ""
			for _, file := range staged.Files {
				if file.Path == "references/computations/revenue.sql" {
					payloadPath = filepath.Join(root, filepath.FromSlash(staged.Stage), file.Payload)
					break
				}
			}
			if payloadPath == "" {
				t.Fatal("staged SQL asset payload not found")
			}
			tc.mutate(t, payloadPath)
			if err := fixtureStore.Close(); err != nil {
				t.Fatal(err)
			}

			// Act.
			_, openErr := openObserved(root, Config{})
			visible, readErr := readVisibleRoot(context.Background(), mustOpenRoot(t, root))

			// Assert.
			if !errors.Is(openErr, store.ErrStorageCorrupt) {
				t.Fatalf("Open() error=%v, want storage corruption", openErr)
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !reflect.DeepEqual(visible, baseFiles) {
				t.Fatalf("visible state partially changed:\ngot:  %#v\nwant: %#v", visible, baseFiles)
			}
		})
	}
}

func v01DurabilityFixture() map[string][]byte {
	return map[string][]byte{
		"index.md": []byte(`---
okf_version: "0.1"
---

# Finance

* [Income statement](metrics/income-statement.md) - Fiscal-year headline figures.
* [Revenue computation](computations/revenue.md) - Legacy revenue computation note.
`),
		"metrics/income-statement.md": []byte(`---
type: Metric
title: Income statement (fiscal year)
description: Headline income-statement figures for a fiscal year.
tags: [finance, income-statement]
timestamp: '2026-05-28T22:53:05+00:00'
---

# Definition

The income statement reports revenue for a fiscal year.

# Revenue

    SELECT SUM(amount) AS revenue
    FROM finance.recognized_revenue
    WHERE fiscal_year = <year>

# Citations

- https://wiki.example/finance/revenue-recognition
`),
		"computations/revenue.md": []byte(`---
type: Note
title: Revenue computation
description: Legacy prose describing the revenue computation.
tags: [finance, revenue]
timestamp: '2026-05-28T22:53:05+00:00'
---

# Definition

Revenue is computed from recognized finance entries.
`),
	}
}

func assertV02StoredAttesterContract(t *testing.T, source bundle.Source) {
	t.Helper()
	ctx := context.Background()
	computationMetadata, err := source.ReadFile(ctx, "computations/revenue.md")
	if err != nil {
		t.Fatal(err)
	}
	attester, err := source.ReadFile(ctx, "references/attesters/sql-equality.py")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := source.ReadFile(ctx, "references/computations/revenue.sql")
	if err != nil {
		t.Fatal(err)
	}
	document, err := bundle.ParseDocument(string(computationMetadata))
	if err != nil {
		t.Fatal(err)
	}
	contract, ok := document.AttestedComputation()
	if !ok || contract.Executor == nil {
		t.Fatalf("stored Attested Computation executor contract=%#v present=%v", contract.Executor, ok)
	}
	executedSQLDeclared := false
	for _, field := range contract.Executor.Receipt {
		if field == "executed_sql" {
			executedSQLDeclared = true
			break
		}
	}
	if !executedSQLDeclared {
		t.Fatalf("stored attester's executed_sql input is undeclared; receipt=%q", contract.Executor.Receipt)
	}
	sum := sha256.Sum256(sql)
	digest := hex.EncodeToString(sum[:])
	wantAttester := []byte(`# Inert reference example: the sanctioned SQL digest is pinned to the captured
# computation bytes. This file documents the attestation rule; the OKF toolkit
# does not execute attester resources.
import hashlib

SANCTIONED_SQL_SHA256 = "` + digest + `"


def attest(receipt: dict) -> bool:
    executed_sql = receipt.get("executed_sql")
    if not isinstance(executed_sql, str):
        return False
    return hashlib.sha256(executed_sql.encode()).hexdigest() == SANCTIONED_SQL_SHA256
`)
	if !bytes.Equal(attester, wantAttester) {
		t.Fatalf("stored attester bytes do not match canonical inert digest contract:\ngot:  %q\nwant: %q", attester, wantAttester)
	}
}

func loadV02DurabilityFixture(t *testing.T) map[string][]byte {
	t.Helper()
	root := filepath.Join("..", "..", "fixtures", "v02", "positive", "durability")
	files := make(map[string][]byte)
	var walk func(string)
	walk = func(dir string) {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			fullPath := filepath.Join(dir, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				t.Fatalf("canonical durability fixture contains symlink %q", fullPath)
			}
			if entry.IsDir() {
				walk(fullPath)
				continue
			}
			info, err := entry.Info()
			if err != nil {
				t.Fatal(err)
			}
			if !info.Mode().IsRegular() {
				t.Fatalf("canonical durability fixture contains non-regular file %q", fullPath)
			}
			data, err := os.ReadFile(fullPath)
			if err != nil {
				t.Fatal(err)
			}
			relative, err := filepath.Rel(root, fullPath)
			if err != nil {
				t.Fatal(err)
			}
			files[filepath.ToSlash(relative)] = data
		}
	}
	walk(root)
	for _, required := range []string{
		"index.md",
		"metrics/income-statement.md",
		"computations/revenue.md",
		"references/computations/revenue.sql",
		"references/attesters/sql-equality.py",
		"references/executors/run-postgres.md",
	} {
		if _, ok := files[required]; !ok {
			t.Fatalf("canonical durability fixture missing %q", required)
		}
	}
	return files
}

func v02DurabilityChange(t *testing.T, id string, base store.Revision) store.ChangeSet {
	t.Helper()
	source, err := bundle.ParseRelationRef("computations/revenue")
	if err != nil {
		t.Fatal(err)
	}
	target, err := bundle.ParseRelationRef("metrics/income-statement")
	if err != nil {
		t.Fatal(err)
	}
	return store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           store.ChangeSetID(id),
		Actor:        "process:store-fs-contract",
		BaseRevision: base,
		Operations: []store.Operation{store.EnsureRelation{
			Source: source,
			Type:   "depends_on",
			Target: target,
		}},
	}
}

func applyV02Preview(t *testing.T, base map[string][]byte, preview store.Preview) map[string][]byte {
	t.Helper()
	files := cloneV02Fixture(base)
	for _, rename := range preview.Renames {
		content, ok := files[rename.From]
		if !ok {
			t.Fatalf("preview renames missing path %q", rename.From)
		}
		delete(files, rename.From)
		files[rename.To] = append([]byte(nil), content...)
	}
	for _, deleted := range preview.Deletes {
		if _, ok := files[deleted]; !ok {
			t.Fatalf("preview deletes missing path %q", deleted)
		}
		delete(files, deleted)
	}
	for _, write := range preview.Writes {
		files[write.Path] = append([]byte(nil), write.Content...)
	}
	return files
}

func trackV02Store(t *testing.T, s *Store) func() {
	t.Helper()
	closed := false
	closeStore := func() {
		if closed {
			return
		}
		closed = true
		if err := s.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}
	t.Cleanup(closeStore)
	return closeStore
}

func writeV02DurabilityFixture(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for path, data := range files {
		writeTestFile(t, root, path, string(data))
	}
}

func assertV02SourceFiles(t *testing.T, source bundle.Source, want map[string][]byte) {
	t.Helper()
	gotPaths, err := source.Paths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := sortedV02FixturePaths(want)
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("Paths()=%q want %q", gotPaths, wantPaths)
	}
	for _, path := range wantPaths {
		got, err := source.ReadFile(context.Background(), path)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", path, err)
		}
		if !bytes.Equal(got, want[path]) {
			t.Fatalf("ReadFile(%q) bytes differ:\ngot:  %q\nwant: %q", path, got, want[path])
		}
	}
}

func sortedV02FixturePaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func cloneV02Fixture(files map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(files))
	for path, data := range files {
		cloned[path] = append([]byte(nil), data...)
	}
	return cloned
}

func assertV02ExactlyOneChangedByte(t *testing.T, before, after map[string][]byte, changedPath string) {
	t.Helper()
	if !reflect.DeepEqual(sortedV02FixturePaths(before), sortedV02FixturePaths(after)) {
		t.Fatalf("asset mutation changed fixture paths")
	}
	differences := 0
	for path, beforeBytes := range before {
		afterBytes := after[path]
		if len(afterBytes) != len(beforeBytes) {
			t.Fatalf("asset mutation changed size of %q from %d to %d", path, len(beforeBytes), len(afterBytes))
		}
		for index := range beforeBytes {
			if beforeBytes[index] != afterBytes[index] {
				if path != changedPath {
					t.Fatalf("asset mutation changed unexpected file %q at byte %d", path, index)
				}
				differences++
			}
		}
	}
	if differences != 1 {
		t.Fatalf("asset mutation changed %d bytes in %q, want exactly 1", differences, changedPath)
	}
}
