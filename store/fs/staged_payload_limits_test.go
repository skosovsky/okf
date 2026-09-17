package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestJournalDecoderAcceptsPayloadAboveDefaultsBelowAbsoluteCeiling(t *testing.T) {
	// Arrange.
	raw := stagedLimitJournal(t, []journalFile{{
		Path:    "asset.bin",
		Payload: "payload-00000",
		Size:    DefaultMaxStagedPayloadBytes + 1,
		Digest:  "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
	}}, "above-default")

	// Act.
	j, err := decodeJournal(raw)

	// Assert.
	if err != nil {
		t.Fatalf("decode above-default manifest: %v", err)
	}
	if got := j.Files[0].Size; got != DefaultMaxStagedPayloadBytes+1 {
		t.Fatalf("decoded size=%d", got)
	}
}

func TestJournalDecoderAcceptsAggregateAboveDefaultBelowAbsoluteCeiling(t *testing.T) {
	// Arrange. Declared metadata crosses the default 1 GiB transaction policy
	// by one byte without allocating any payload bytes.
	digest := "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
	files := []journalFile{
		{Path: "asset-0.bin", Payload: "payload-00000", Size: DefaultMaxStagedPayloadBytes, Digest: digest},
		{Path: "asset-1.bin", Payload: "payload-00001", Size: DefaultMaxStagedPayloadBytes, Digest: digest},
		{Path: "asset-2.bin", Payload: "payload-00002", Size: DefaultMaxStagedPayloadBytes, Digest: digest},
		{Path: "asset-3.bin", Payload: "payload-00003", Size: DefaultMaxStagedPayloadBytes, Digest: digest},
		{Path: "asset-4.bin", Payload: "payload-00004", Size: 1, Digest: digest},
	}
	raw := stagedLimitJournal(t, files, "aggregate-above-default")

	// Act.
	j, err := decodeJournal(raw)

	// Assert.
	if err != nil {
		t.Fatalf("decode above-default aggregate manifest: %v", err)
	}
	var aggregate int64
	for _, file := range j.Files {
		aggregate += file.Size
	}
	if aggregate != DefaultMaxStagedTransactionBytes+1 {
		t.Fatalf("decoded aggregate=%d want=%d", aggregate, DefaultMaxStagedTransactionBytes+1)
	}
}

func TestConfigValidateStagedPayloadAbsoluteCeilings(t *testing.T) {
	// Arrange.
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "exact ceilings",
			config: Config{
				MaxStagedPayloadBytes:     maxStagedPayloadBytes,
				MaxStagedTransactionBytes: maxStagedTransactionBytes,
			},
		},
		{
			name: "payload ceiling plus one",
			config: Config{
				MaxStagedPayloadBytes:     maxStagedPayloadBytes + 1,
				MaxStagedTransactionBytes: maxStagedTransactionBytes,
			},
			wantErr: true,
		},
		{
			name: "transaction ceiling plus one",
			config: Config{
				MaxStagedPayloadBytes:     maxStagedPayloadBytes,
				MaxStagedTransactionBytes: maxStagedTransactionBytes + 1,
			},
			wantErr: true,
		},
		{
			name: "transaction below payload",
			config: Config{
				MaxStagedPayloadBytes:     2,
				MaxStagedTransactionBytes: 1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			err := tt.config.Validate()

			// Assert.
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestJournalPayloadLimitsRejectAbsoluteOverflow(t *testing.T) {
	// Arrange.
	digest := "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
	tests := []struct {
		name  string
		files []journalFile
	}{
		{"size", []journalFile{{Path: "a.md", Payload: "payload-00000", Size: maxStagedPayloadBytes + 1, Digest: digest}}},
		{"integer", []journalFile{{Path: "a.md", Payload: "payload-00000", Size: math.MaxInt64, Digest: digest}}},
		{"aggregate", []journalFile{
			{Path: "a.md", Payload: "payload-00000", Size: maxStagedTransactionBytes, Digest: digest},
			{Path: "b.md", Payload: "payload-00001", Size: 1, Digest: digest},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := stagedLimitJournal(t, tt.files, "absolute-"+tt.name)

			// Act.
			_, err := decodeJournal(raw)

			// Assert.
			if err == nil || !strings.Contains(err.Error(), "journal file manifest") {
				t.Fatalf("decode error=%v, want absolute manifest-limit rejection", err)
			}
		})
	}
}

func TestJournalDecoderSyntheticAbsoluteFileCountCeiling(t *testing.T) {
	// Arrange. Two zero-byte manifest entries exercise the immutable decoder
	// cardinality gate without production-sized allocation. Production still
	// binds the same seam to the public v5 payload ordinal ceiling.
	const digest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	files := []journalFile{
		{Path: "a.md", Payload: "payload-00000", Size: 0, Digest: digest},
		{Path: "b.md", Payload: "payload-00001", Size: 0, Digest: digest},
	}
	raw := stagedLimitJournal(t, files, "synthetic-absolute-file-count")
	smaller := stagedManifestLimits{maxFiles: 1, maxPayloadBytes: 1, maxTransactionBytes: 1}
	exact := stagedManifestLimits{maxFiles: 2, maxPayloadBytes: 1, maxTransactionBytes: 1}

	// Act.
	_, smallerErr := decodeJournalWithAlgorithmNameAndLimits(raw, nil, "sha256", smaller)
	decoded, exactErr := decodeJournalWithAlgorithmNameAndLimits(raw, nil, "sha256", exact)

	// Assert.
	if smallerErr == nil || smallerErr.Error() != "invalid journal file manifest: staged file count exceeds limit" {
		t.Fatalf("synthetic one-file ceiling error = %v", smallerErr)
	}
	if exactErr != nil || len(decoded.Files) != 2 {
		t.Fatalf("synthetic exact ceiling decode = files:%d error:%v", len(decoded.Files), exactErr)
	}
	absolute := absoluteStagedManifestLimits()
	if DefaultMaxStagedFiles != 100_000 || absolute.maxFiles != DefaultMaxStagedFiles {
		t.Fatalf("production file-count ceiling changed: default=%d absolute=%d", DefaultMaxStagedFiles, absolute.maxFiles)
	}
}

func TestJournalPayloadManifestIntegrityRegressions(t *testing.T) {
	// Arrange.
	digest := "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
	tests := []struct {
		name  string
		files []journalFile
	}{
		{"ordinal", []journalFile{{Path: "a.md", Payload: "payload-00001", Size: 1, Digest: digest}}},
		{"duplicate", []journalFile{
			{Path: "a.md", Payload: "payload-00000", Size: 1, Digest: digest},
			{Path: "b.md", Payload: "payload-00000", Size: 1, Digest: digest},
		}},
		{"tamper", []journalFile{{Path: "a.md", Payload: "payload-00000", Size: 1, Digest: strings.Repeat("0", 64)}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := stagedLimitJournal(t, tt.files, "integrity-"+tt.name)

			// Act.
			_, err := decodeJournal(raw)

			// Assert.
			if err == nil || !strings.Contains(err.Error(), "journal file manifest") {
				t.Fatalf("decode error=%v, want payload manifest integrity rejection", err)
			}
		})
	}
}

func TestRecoveryRejectsNegativeV5PayloadSizeBeforePayloadIO(t *testing.T) {
	// Arrange. First prove that the current v5 envelope is otherwise accepted
	// by the decoder. Recovery then receives the same manifest with only Size
	// changed to -1 and no staged payload present.
	const digest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	validFile := journalFile{
		Path:    "a.md",
		Payload: "payload-00000",
		Size:    0,
		Digest:  digest,
	}
	validRaw := stagedLimitJournal(t, []journalFile{validFile}, "negative-size")
	if _, err := decodeJournal(validRaw); err != nil {
		t.Fatalf("otherwise-valid v5 journal decode error = %v", err)
	}
	invalidFile := validFile
	invalidFile.Size = -1
	invalidRaw := stagedLimitJournal(t, []journalFile{invalidFile}, "negative-size")
	root := t.TempDir()
	receipt := testJournalReceipt(
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"negative-size",
		"",
	)
	journalName, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	source := path.Join(temporaryDirectory, ".okf-tmp-1-0")
	absoluteSource := filepath.Join(root, filepath.FromSlash(source))
	if err := writeDurableFixture(absoluteSource, validRaw); err != nil {
		t.Fatal(err)
	}
	owned, err := os.Stat(absoluteSource)
	if err != nil {
		t.Fatal(err)
	}
	key, err := newArtifactClaimKey(receipt.RequestDigest, journalName, claimJournal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.prepareRegularWitnessFrom(context.Background(), key, source, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := s.fdRenameOwnedNoReplace(source, journalName, owned, false); err != nil {
		t.Fatal(err)
	}
	if err := s.syncDirAt(path.Dir(journalName)); err != nil {
		t.Fatal(err)
	}
	journalFile, err := os.OpenFile(filepath.Join(root, filepath.FromSlash(journalName)), os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journalFile.Write(invalidRaw); err != nil {
		t.Fatal(err)
	}
	if err := journalFile.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := journalFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(root, filepath.FromSlash(path.Join(stage, invalidFile.Payload)))
	if _, err := os.Stat(payloadPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("adversarial payload precondition stat error = %v, want not exist", err)
	}

	// Act.
	_, openErr := openObserved(root, Config{})

	// Assert. The metadata-only negative-size gate must win over any attempt to
	// open the deliberately absent payload.
	if !errors.Is(openErr, store.ErrStorageCorrupt) {
		t.Fatalf("Open() error = %v, want ErrStorageCorrupt", openErr)
	}
	if !strings.Contains(openErr.Error(), "invalid journal file manifest: staged payload exceeds limit") {
		t.Fatalf("Open() error = %v, want negative-size manifest rejection", openErr)
	}
	if strings.Contains(openErr.Error(), "no such file") {
		t.Fatalf("Open() reached absent payload before metadata rejection: %v", openErr)
	}
}

func TestStagedManifestEffectiveLimitsAreValidatedAsOneMetadataGate(t *testing.T) {
	// Arrange. Small values exercise the same pure gate used for real limits.
	files := []journalFile{
		{Path: "a.md", Payload: "payload-00000", Size: 8},
		{Path: "b.md", Payload: "payload-00001", Size: 5},
	}

	// Act.
	smallerErr := validateStagedManifestLimits(files, 2, 8, 12)
	enlargedErr := validateStagedManifestLimits(files, 2, 8, 13)

	// Assert.
	if smallerErr == nil {
		t.Fatal("smaller aggregate policy accepted manifest")
	}
	if enlargedErr != nil {
		t.Fatalf("enlarged aggregate policy rejected manifest: %v", enlargedErr)
	}
}

func TestRecoverySyntheticEnlargedCeilingCompletesDurableJournal(t *testing.T) {
	// Arrange. Seventeen real bytes cross the synthetic 16-byte default while
	// staying below the explicitly enlarged 32-byte decoder/config ceiling.
	// This bounded immutable/effective-seam fixture is the normative analogue
	// for large revision assets; recovery must not allocate production-scale
	// payloads merely to prove that payload and metadata ceilings are distinct.
	const syntheticDefault int64 = 16
	const syntheticEnlarged int64 = 32
	payload := bytes.Repeat([]byte("x"), int(syntheticDefault+1))
	if int64(len(payload)) <= syntheticDefault || int64(len(payload)) >= syntheticEnlarged {
		t.Fatalf("payload size=%d, want (%d,%d)", len(payload), syntheticDefault, syntheticEnlarged)
	}
	root := t.TempDir()
	config := Config{
		MaxStagedPayloadBytes:     syntheticEnlarged,
		MaxStagedTransactionBytes: syntheticEnlarged,
	}
	next, _ := stageCrashedPayload(t, root, config, payload, "synthetic-enlarged")
	absoluteLimits := stagedManifestLimits{
		maxFiles:            1,
		maxPayloadBytes:     syntheticEnlarged,
		maxTransactionBytes: syntheticEnlarged,
	}

	// Act.
	reopened, err := openObservedWithRecoveryLimits(context.Background(), root, config, absoluteLimits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := got.ReadFile(context.Background(), "asset.bin")

	// Assert.
	if readErr != nil || !bytes.Equal(data, payload) || got.Revision() != next.Revision() {
		t.Fatalf("recovery read=%v size=%d revision=%s want=%s", readErr, len(data), got.Revision(), next.Revision())
	}
}

func TestRecoverySyntheticSmallerConfigRejectsBeforePayloadRead(t *testing.T) {
	// Arrange. The decoder ceiling accepts the durable 17-byte manifest, but
	// the reopened store's 16-byte effective policy must reject it before I/O.
	const syntheticDefault int64 = 16
	const syntheticEnlarged int64 = 32
	payload := bytes.Repeat([]byte("x"), int(syntheticDefault+1))
	root := t.TempDir()
	enlargedConfig := Config{
		MaxStagedPayloadBytes:     syntheticEnlarged,
		MaxStagedTransactionBytes: syntheticEnlarged,
	}
	_, stage := stageCrashedPayload(t, root, enlargedConfig, payload, "synthetic-smaller")
	payloadPath := filepath.Join(root, filepath.FromSlash(path.Join(stage, payloadName(0))))
	if err := os.Remove(payloadPath); err != nil {
		t.Fatal(err)
	}
	smallerConfig := Config{
		MaxStagedPayloadBytes:     syntheticDefault,
		MaxStagedTransactionBytes: syntheticDefault,
	}
	absoluteLimits := stagedManifestLimits{
		maxFiles:            1,
		maxPayloadBytes:     syntheticEnlarged,
		maxTransactionBytes: syntheticEnlarged,
	}

	// Act. The staged file is deliberately missing: only a pre-I/O policy gate
	// can return the configured-limit diagnostic.
	_, openErr := openObservedWithRecoveryLimits(context.Background(), root, smallerConfig, absoluteLimits)

	// Assert.
	if !errors.Is(openErr, store.ErrStorageCorrupt) || !strings.Contains(openErr.Error(), "configured limit") {
		t.Fatalf("open error=%v, want configured-limit corruption before missing payload", openErr)
	}
}

func TestRecoverySyntheticSmallerAggregateLimitRejectsBeforePayloadRead(t *testing.T) {
	const (
		perFileLimit       int64 = 8
		writerAggregate    int64 = 16
		reopenedAggregate  int64 = 12
		syntheticFileCount       = 2
	)
	for _, missingOrdinal := range []int{0, 1} {
		t.Run(payloadName(missingOrdinal), func(t *testing.T) {
			// Arrange. Both eight-byte payloads fit the writer and reopened
			// per-file policy. Their sixteen-byte aggregate fits the immutable
			// ceiling but exceeds the reopened transaction policy.
			root := t.TempDir()
			writerConfig := Config{
				MaxStagedFiles:            syntheticFileCount,
				MaxStagedPayloadBytes:     perFileLimit,
				MaxStagedTransactionBytes: writerAggregate,
			}
			_, stage := stageCrashedFiles(t, root, writerConfig, map[string][]byte{
				"asset-a.bin": bytes.Repeat([]byte("a"), int(perFileLimit)),
				"asset-b.bin": bytes.Repeat([]byte("b"), int(perFileLimit)),
			}, "synthetic-smaller-aggregate-"+payloadName(missingOrdinal))
			payloadDirectory := filepath.Join(root, filepath.FromSlash(stage))
			if err := os.Remove(filepath.Join(payloadDirectory, payloadName(missingOrdinal))); err != nil {
				t.Fatal(err)
			}
			otherOrdinal := 1 - missingOrdinal
			if err := os.WriteFile(
				filepath.Join(payloadDirectory, payloadName(otherOrdinal)),
				bytes.Repeat([]byte("x"), int(perFileLimit)),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			transaction := path.Base(path.Dir(stage))
			journalName := filepath.Join(root, internalDirectory, "transactions", transaction+".json")
			smallerConfig := Config{
				MaxStagedFiles:            syntheticFileCount,
				MaxStagedPayloadBytes:     perFileLimit,
				MaxStagedTransactionBytes: reopenedAggregate,
			}
			absoluteLimits := stagedManifestLimits{
				maxFiles:            syntheticFileCount,
				maxPayloadBytes:     perFileLimit,
				maxTransactionBytes: writerAggregate,
			}

			// Act. One payload is absent and the other has a bad digest. Any
			// payload read would therefore win error precedence over the
			// complete-manifest aggregate policy.
			reopened, openErr := openObservedWithRecoveryLimits(context.Background(), root, smallerConfig, absoluteLimits)
			if reopened != nil {
				t.Cleanup(func() { _ = reopened.Close() })
			}

			// Assert.
			if reopened != nil {
				t.Fatal("recovery returned a store after aggregate policy rejection")
			}
			if !errors.Is(openErr, store.ErrStorageCorrupt) {
				t.Fatalf("open error=%v, want ErrStorageCorrupt", openErr)
			}
			const want = "staged manifest exceeds configured limit: staged payload exceeds limit"
			if !strings.Contains(openErr.Error(), want) {
				t.Fatalf("open error=%v, want %q", openErr, want)
			}
			for _, payloadError := range []string{"no such file", "digest mismatch"} {
				if strings.Contains(openErr.Error(), payloadError) {
					t.Fatalf("recovery read staged payload before aggregate gate: %v", openErr)
				}
			}
			if got, err := os.ReadFile(filepath.Join(root, "old.md")); err != nil || string(got) != adversarialDocument("old") {
				t.Fatalf("visible base changed: read=%v bytes=%q", err, got)
			}
			for _, name := range []string{"asset-a.bin", "asset-b.bin"} {
				if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("partial visible asset %q stat=%v, want absent", name, err)
				}
			}
			if _, err := os.Stat(journalName); err != nil {
				t.Fatalf("rejected journal was not retained: %v", err)
			}
		})
	}
}

func TestRecoverySyntheticConfiguredFileCountRejectsBeforePayloadRead(t *testing.T) {
	// Arrange. A valid durable two-file journal fits the synthetic immutable
	// decoder ceiling but exceeds the reopened store's one-file policy. Remove
	// the first payload so only the complete-manifest policy gate can win.
	root := t.TempDir()
	writerConfig := Config{MaxStagedFiles: 2}
	_, stage := stageCrashedFiles(t, root, writerConfig, map[string][]byte{
		"asset-a.bin": []byte("a"),
		"asset-b.bin": []byte("b"),
	}, "synthetic-configured-file-count")
	payloadPath := filepath.Join(root, filepath.FromSlash(path.Join(stage, payloadName(0))))
	if err := os.Remove(payloadPath); err != nil {
		t.Fatal(err)
	}
	smallerConfig := Config{MaxStagedFiles: 1}
	absoluteLimits := stagedManifestLimits{
		maxFiles:            2,
		maxPayloadBytes:     DefaultMaxStagedPayloadBytes,
		maxTransactionBytes: DefaultMaxStagedTransactionBytes,
	}

	// Act.
	_, openErr := openObservedWithRecoveryLimits(context.Background(), root, smallerConfig, absoluteLimits)

	// Assert.
	if !errors.Is(openErr, store.ErrStorageCorrupt) {
		t.Fatalf("open error = %v, want ErrStorageCorrupt", openErr)
	}
	const want = "staged manifest exceeds configured limit: staged file count exceeds limit"
	if !strings.Contains(openErr.Error(), want) {
		t.Fatalf("open error = %v, want %q", openErr, want)
	}
	if strings.Contains(openErr.Error(), "no such file") {
		t.Fatalf("recovery opened absent payload before configured count gate: %v", openErr)
	}
}

func stageCrashedPayload(t *testing.T, root string, config Config, payload []byte, id string) (*snapshot, string) {
	t.Helper()
	return stageCrashedFiles(t, root, config, map[string][]byte{"asset.bin": payload}, id)
}

func stageCrashedFiles(t *testing.T, root string, config Config, files map[string][]byte, id string) (*snapshot, string) {
	t.Helper()
	writeTestFile(t, root, "old.md", adversarialDocument("old"))
	fired := false
	armed := false
	journalRenamed := false
	writerConfig := config
	writerConfig.PostFault = func(step Step) error {
		if step == StepJournalRename {
			journalRenamed = true
		}
		if armed && journalRenamed && step == StepJournalDirectorySync && !fired {
			fired = true
			return errors.New("crash after durable journal")
		}
		return nil
	}
	s, err := openObserved(root, writerConfig)
	if err != nil {
		t.Fatal(err)
	}
	base := adversarialSnapshot(t, s)
	next, err := newSnapshot(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), id, "")
	armed = true
	if err := s.publish(context.Background(), next, receipt); err == nil || !fired {
		t.Fatalf("publish error=%v fired=%v", err, fired)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	return next, stage
}

func stagedLimitJournal(t *testing.T, files []journalFile, id string) []byte {
	t.Helper()
	base := store.Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	result := store.Revision("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	receipt := testJournalReceipt(base, result, id, "")
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	baseManifest := []journalBaseFile{}
	binding, err := journalBaseBinding(receipt, baseManifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(journal{Version: 5, HashAlgorithm: "sha256", Stage: stage, Base: baseManifest, BaseBinding: binding, Files: files, Receipt: receipt})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestConfiguredStagePayloadLimitsRejectBeforePayloadWrite(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("base"))
	s, err := openObserved(root, Config{MaxStagedPayloadBytes: 8, MaxStagedTransactionBytes: 12})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := adversarialSnapshot(t, s)
	for _, files := range []map[string][]byte{
		{"a.md": bytes.Repeat([]byte("a"), 9)},
		{"a.md": bytes.Repeat([]byte("a"), 8), "b.md": bytes.Repeat([]byte("b"), 5)},
	} {
		next, snapshotErr := newSnapshot(context.Background(), files)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		receipt := testJournalReceipt(base.Revision(), next.Revision(), "configured-stage-limit", "")
		_, stageErr := s.stageJournal(next, receipt, replaceReplay{})
		if !errors.Is(stageErr, store.ErrInvalidChangeSet) {
			t.Fatalf("stage error = %v, want InvalidChangeSet", stageErr)
		}
		stage, pathErr := journalStage(receipt.RequestDigest)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(stage))); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("limit rejection wrote staged payload: %v", statErr)
		}
	}
}

func TestConfiguredStageFileCountRejectsBeforeAnyStageWrite(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("base"))
	armed := false
	stageWrites := 0
	s, err := openObserved(root, Config{
		MaxStagedFiles: 1,
		PostFault: func(step Step) error {
			if armed {
				switch step {
				case StepStageFileWrite, StepStageFileSync, StepStageFileClose, StepStageRename, StepStageDirectorySync:
					stageWrites++
				}
			}
			return nil
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := adversarialSnapshot(t, s)
	next, err := newSnapshot(context.Background(), map[string][]byte{
		"a.md": []byte(adversarialDocument("next")),
		"b.md": []byte(adversarialDocument("created")),
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "configured-stage-file-count", "")
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	armed = true
	_, stageErr := s.stageJournal(next, receipt, replaceReplay{})
	armed = false

	// Assert.
	if !errors.Is(stageErr, store.ErrInvalidChangeSet) || !strings.Contains(stageErr.Error(), "staged file limit exceeded") {
		t.Fatalf("stage error = %v, want InvalidChangeSet file-count rejection", stageErr)
	}
	if stageWrites != 0 {
		t.Fatalf("configured count rejection reached %d staged durability boundaries", stageWrites)
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(stage))); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("configured count rejection created stage: %v", statErr)
	}
}

func TestJournalManifestBytePreflightBeforeAnyStageSideEffect(t *testing.T) {
	// Arrange. The long revision-visible paths make metadata, rather than
	// payload bytes or file count, the only failing dimension.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("base"))
	armed := false
	durabilityCallbacks := 0
	s, err := openObserved(root, Config{
		Fault: func(Step) error {
			if armed {
				durabilityCallbacks++
			}
			return nil
		},
		PostFault: func(Step) error {
			if armed {
				durabilityCallbacks++
			}
			return nil
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := adversarialSnapshot(t, s)
	longPrefix := strings.Repeat("safe-segment/", 256)
	next, err := newSnapshot(context.Background(), map[string][]byte{
		longPrefix + "asset-a.bin": []byte("a"),
		longPrefix + "asset-b.bin": []byte("b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "journal-byte-preflight", "")
	_, raw, err := s.prepareJournalContext(context.Background(), next, receipt, replaceReplay{}, productionJournalManifestByteLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= maxJournalManifestRead {
		t.Fatalf("synthetic journal size=%d reached production ceiling", len(raw))
	}
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	stageDirectory := filepath.Join(root, filepath.FromSlash(path.Dir(stage)))
	journalName, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		limits journalManifestByteLimits
	}{
		{
			name: "absolute",
			limits: journalManifestByteLimits{
				absolute:  len(raw) - 1,
				effective: len(raw),
			},
		},
		{
			name: "effective",
			limits: journalManifestByteLimits{
				absolute:  maxJournalManifestRead,
				effective: len(raw) - 1,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Act.
			armed = true
			publishErr := s.publishWithReplayAndManifestLimits(context.Background(), next, receipt, replaceReplay{}, tc.limits)
			armed = false

			// Assert. Preserve the writer's established untyped error class and
			// exact diagnostic while proving no staging boundary was reached.
			want := fmt.Sprintf("fs store: journal manifest exceeds %d byte limit", len(raw)-1)
			if publishErr == nil || publishErr.Error() != want {
				t.Fatalf("publish error=%v, want %q", publishErr, want)
			}
			if errors.Is(publishErr, store.ErrInvalidChangeSet) || errors.Is(publishErr, store.ErrStorageCorrupt) {
				t.Fatalf("manifest-size rejection changed error class: %v", publishErr)
			}
			if durabilityCallbacks != 0 {
				t.Fatalf("manifest-size rejection reached %d durability callbacks", durabilityCallbacks)
			}
			if _, statErr := os.Stat(stageDirectory); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("manifest-size rejection created stage directory: %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(journalName))); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("manifest-size rejection created journal: %v", statErr)
			}
		})
	}

	// Act. Equality is accepted: the same exact envelope may now begin staging.
	armed = true
	j, owned, stageErr := s.stageJournalWithManifestLimitsObserved(context.Background(), next, receipt, replaceReplay{}, journalManifestByteLimits{
		absolute:  len(raw),
		effective: len(raw),
	})
	armed = false

	// Assert.
	if stageErr != nil {
		t.Fatalf("exact byte boundary rejected: %v", stageErr)
	}
	if durabilityCallbacks == 0 {
		t.Fatal("exact byte boundary did not begin staging")
	}
	canonical, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, raw) {
		t.Fatal("staged journal wire differs from preflight envelope")
	}
	if _, statErr := os.Stat(stageDirectory); statErr != nil {
		t.Fatalf("exact byte boundary did not create stage directory: %v", statErr)
	}
	if err := s.cleanupOwnedStage(j, owned); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryJournalManifestEffectiveByteGateBeforePayloadIO(t *testing.T) {
	// Arrange. Persist a canonical journal and then remove its first payload.
	// The synthetic effective metadata ceiling must win before recovery can
	// observe that missing file.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("base"))
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := adversarialSnapshot(t, s)
	longPath := strings.Repeat("safe-segment/", 256) + "asset.bin"
	next, err := newSnapshot(context.Background(), map[string][]byte{longPath: []byte("payload")})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "recovery-journal-byte-preflight", "")
	j, raw, err := s.prepareJournalContext(context.Background(), next, receipt, replaceReplay{}, productionJournalManifestByteLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.stageJournalPayloadsObserved(context.Background(), next, j); err != nil {
		t.Fatal(err)
	}
	journalName, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.writeJournalObserved(context.Background(), journalName, raw); err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(root, filepath.FromSlash(path.Join(j.Stage, j.Files[0].Payload)))
	if err := os.Remove(payloadPath); err != nil {
		t.Fatal(err)
	}

	// Act.
	recoveryErr := s.recoverContextWithAllLimits(
		context.Background(),
		absoluteStagedManifestLimits(),
		configuredStagedManifestLimits(s.config),
		journalManifestByteLimits{absolute: maxJournalManifestRead, effective: len(raw) - 1},
	)

	// Assert.
	if !errors.Is(recoveryErr, store.ErrStorageCorrupt) {
		t.Fatalf("recovery error=%v, want ErrStorageCorrupt", recoveryErr)
	}
	want := fmt.Sprintf("journal manifest exceeds configured limit: journal manifest exceeds %d byte limit", len(raw)-1)
	if !strings.Contains(recoveryErr.Error(), want) {
		t.Fatalf("recovery error=%v, want %q", recoveryErr, want)
	}
	if strings.Contains(recoveryErr.Error(), "no such file") {
		t.Fatalf("recovery opened payload before manifest byte gate: %v", recoveryErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(journalName))); statErr != nil {
		t.Fatalf("recovery removed rejected journal: %v", statErr)
	}
}

func TestPayloadOrdinalCeiling(t *testing.T) {
	if got := payloadName(DefaultMaxStagedFiles - 1); got != "payload-99999" || !safePayloadName(got) {
		t.Fatalf("last ordinal=%q", got)
	}
	if got := payloadName(DefaultMaxStagedFiles); got != "" || safePayloadName("payload-100000") {
		t.Fatalf("ordinal ceiling not enforced: %q", got)
	}
	if err := (Config{MaxStagedFiles: DefaultMaxStagedFiles + 1}).Validate(); err == nil {
		t.Fatal("accepted oversized staged file limit")
	}
}
