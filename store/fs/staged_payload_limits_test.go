package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestJournalPayloadLimitsRejectOverflowBeforePayloadRead(t *testing.T) {
	base := store.Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	result := store.Revision("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	receipt := testJournalReceipt(base, result, "payload-overflow", "")
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := journalBaseBinding(receipt, nil)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
	for _, files := range [][]journalFile{
		{{Path: "a.md", Payload: "payload-00000", Size: math.MaxInt64, Digest: digest}},
		{{Path: "a.md", Payload: "payload-00000", Size: DefaultMaxStagedPayloadBytes, Digest: digest}, {Path: "b.md", Payload: "payload-00001", Size: 1, Digest: digest}},
	} {
		raw, marshalErr := json.Marshal(journal{Version: 5, HashAlgorithm: "sha256", Stage: stage, Base: []journalBaseFile{}, BaseBinding: binding, Files: files, Receipt: receipt})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, decodeErr := decodeJournal(raw); decodeErr == nil {
			t.Fatal("accepted oversized or aggregate-overflow manifest")
		}
	}
}

func TestConfiguredStagePayloadLimitsRejectBeforePayloadWrite(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("base"))
	s, err := Open(root, Config{MaxStagedPayloadBytes: 8, MaxStagedTransactionBytes: 12})
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
