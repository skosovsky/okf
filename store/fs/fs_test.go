package fs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func TestDiskEnvelopeDecodersRejectDuplicateKeysRecursively(t *testing.T) {
	// Arrange.
	tests := []struct {
		name   string
		decode func([]byte) error
		raw    []byte
	}{
		{name: "journal version", decode: func(raw []byte) error { _, err := decodeJournal(raw); return err }, raw: []byte(`{"version":1,"version":1,"files":{},"receipt":{}}`)},
		{name: "journal nested receipt", decode: func(raw []byte) error { _, err := decodeJournal(raw); return err }, raw: []byte(`{"version":1,"files":{},"receipt":{"FormatVersion":1,"FormatVersion":1}}`)},
		{name: "receipt version", decode: func(raw []byte) error { _, err := decodeReceiptFile(raw); return err }, raw: []byte(`{"version":1,"version":1,"key":"retry","digest":"sha256:x","receipt":{}}`)},
		{name: "receipt key", decode: func(raw []byte) error { _, err := decodeReceiptFile(raw); return err }, raw: []byte(`{"version":1,"key":"retry","key":"retry","digest":"sha256:x","receipt":{}}`)},
		{name: "receipt digest", decode: func(raw []byte) error { _, err := decodeReceiptFile(raw); return err }, raw: []byte(`{"version":1,"key":"retry","digest":"sha256:x","digest":"sha256:x","receipt":{}}`)},
		{name: "receipt nested receipt", decode: func(raw []byte) error { _, err := decodeReceiptFile(raw); return err }, raw: []byte(`{"version":1,"key":"retry","digest":"sha256:x","receipt":{"ChangedFiles":[],"ChangedFiles":[]}}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			err := tt.decode(tt.raw)

			// Assert.
			if err == nil {
				t.Fatal("decoder accepted duplicate JSON key")
			}
		})
	}
}

func TestReplayEnvelopeRejectsTamperingAndNonCanonicalProjections(t *testing.T) {
	// Arrange. This is the smallest complete replay projection; keeping it
	// directly at the durable boundary catches changes that would otherwise
	// surface attacker-controlled diagnostics on an idempotent retry.
	receipt := testJournalReceipt("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "replay-contract", "replay-contract-key")
	replay, err := sealReplay(receipt, replaceReplay{
		Version: replayFormatVersion, Algorithm: replayAlgorithm, Domain: replayDomain,
		Validation: validationReplay{ScannedFiles: 1, Diagnostics: []validationDiagnosticReplay{{Severity: "WARN", File: ".", Message: "root warning", Refs: []string{}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(replay)
	if err != nil {
		t.Fatal(err)
	}

	// Act / Assert. Every malformed variation must fail before it is converted
	// back to validator diagnostics.
	tests := []struct {
		name string
		raw  []byte
	}{
		{"negative count", []byte(strings.Replace(string(raw), `"scanned_files":1`, `"scanned_files":-1`, 1))},
		{"null count", []byte(strings.Replace(string(raw), `"scanned_files":1`, `"scanned_files":null`, 1))},
		{"fractional count", []byte(strings.Replace(string(raw), `"scanned_files":1`, `"scanned_files":1.0`, 1))},
		{"exponent count", []byte(strings.Replace(string(raw), `"scanned_files":1`, `"scanned_files":1e0`, 1))},
		{"negative zero count", []byte(strings.Replace(string(raw), `"scanned_files":1`, `"scanned_files":-0`, 1))},
		{"string count", []byte(strings.Replace(string(raw), `"scanned_files":1`, `"scanned_files":"1"`, 1))},
		{"boolean count", []byte(strings.Replace(string(raw), `"scanned_files":1`, `"scanned_files":true`, 1))},
		{"null version", []byte(strings.Replace(string(raw), `"version":1`, `"version":null`, 1))},
		{"fractional version", []byte(strings.Replace(string(raw), `"version":1`, `"version":1.0`, 1))},
		{"non-string algorithm", []byte(strings.Replace(string(raw), `"algorithm":"sha256"`, `"algorithm":true`, 1))},
		{"null diagnostic message", []byte(strings.Replace(string(raw), `"message":"root warning"`, `"message":null`, 1))},
		{"non-string ref", []byte(strings.Replace(string(raw), `"refs":[]`, `"refs":[1]`, 1))},
		{"unknown field", []byte(strings.Replace(string(raw), `"validation":`, `"unexpected":true,"validation":`, 1))},
		{"duplicate key", []byte(strings.Replace(string(raw), `"version":1,`, `"version":1,"version":1,`, 1))},
		{"unknown severity", []byte(strings.Replace(string(raw), `"severity":"WARN"`, `"severity":"FATAL"`, 1))},
		{"oversized diagnostics", []byte(strings.Replace(string(raw), `"scanned_files":1`, `"scanned_files":1000001`, 1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var decoded replaceReplay
			if err := json.Unmarshal(tt.raw, &decoded); err == nil {
				t.Fatal("accepted invalid replay")
			}
		})
	}
}

func TestReceiptReplayBindingRejectsDeterministicTampering(t *testing.T) {
	// Arrange. A replay envelope can validate structurally on its own, but it
	// must never be accepted as receipt evidence until its binding is checked
	// against that exact receipt.
	receipt := testJournalReceipt("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "replay-binding", "replay-binding-key")
	replay, err := sealReplay(receipt, replaceReplay{
		Version: replayFormatVersion, Algorithm: replayAlgorithm, Domain: replayDomain,
		Validation: validationReplay{ScannedFiles: 1, Diagnostics: []validationDiagnosticReplay{{Severity: "WARN", File: ".", Message: "root warning", Refs: []string{}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tampered := replay
	last := len(tampered.Binding) - 1
	if tampered.Binding[last] == '0' {
		tampered.Binding = tampered.Binding[:last] + "1"
	} else {
		tampered.Binding = tampered.Binding[:last] + "0"
	}
	if tampered.Binding == replay.Binding {
		t.Fatal("test did not change binding")
	}

	// Act. The projection alone remains syntactically valid; the receipt
	// envelope is the mandatory semantic binding boundary.
	canonical, err := sealReplay(receipt, tampered)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Binding == tampered.Binding {
		t.Fatal("tampered binding equals expected binding")
	}
	raw, err := json.Marshal(receiptFile{Version: 2, Key: string(receipt.IdempotencyKey), Digest: receipt.RequestDigest, Receipt: receipt, Replay: &tampered})
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if _, err := decodeReceiptFile(raw); err == nil {
		t.Fatal("receipt decoder accepted tampered replay binding")
	}
	_, s := adversarialStore(t, Config{})
	if err := s.writePrivateDurableAt(s.receiptPath(receipt.IdempotencyKey), raw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.lookupReceiptFile(receipt.IdempotencyKey, receipt.RequestDigest); !errors.Is(err, store.ErrStorageCorrupt) {
		t.Fatalf("lookup error = %v, want storage corruption", err)
	}
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	journalRaw, err := json.Marshal(journal{Version: 4, HashAlgorithm: "sha256", Stage: stage, Files: []journalFile{}, Receipt: receipt, Replay: &tampered})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeJournal(journalRaw); err == nil {
		t.Fatal("journal decoder accepted tampered replay binding")
	}
}

func TestReceiptEnvelopeOptionalReplayIsOmittedOrStrictObject(t *testing.T) {
	// Arrange.
	receipt := testJournalReceipt("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "optional-replay", "optional-replay-key")
	base := receiptFile{Version: 2, Key: string(receipt.IdempotencyKey), Digest: receipt.RequestDigest, Receipt: receipt}
	omitted, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := sealReplay(receipt, replaceReplay{Version: replayFormatVersion, Algorithm: replayAlgorithm, Domain: replayDomain, Validation: validationReplay{ScannedFiles: 0, Diagnostics: []validationDiagnosticReplay{}}})
	if err != nil {
		t.Fatal(err)
	}
	withReplay, err := json.Marshal(receiptFile{Version: 2, Key: string(receipt.IdempotencyKey), Digest: receipt.RequestDigest, Receipt: receipt, Replay: &replay})
	if err != nil {
		t.Fatal(err)
	}

	// Act / Assert.
	for _, tt := range []struct {
		name    string
		raw     []byte
		wantErr bool
	}{
		{"omitted", omitted, false},
		{"object", withReplay, false},
		{"null", appendReplayEnvelopeField(omitted, "null"), true},
		{"string", appendReplayEnvelopeField(omitted, `"x"`), true},
		{"number", appendReplayEnvelopeField(omitted, "1"), true},
		{"array", appendReplayEnvelopeField(omitted, "[]"), true},
		{"boolean", appendReplayEnvelopeField(omitted, "true"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeReceiptFile(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("decode error=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}

	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	journalRaw, err := json.Marshal(journal{Version: 4, HashAlgorithm: "sha256", Stage: stage, Files: []journalFile{}, Receipt: receipt})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeJournal(appendReplayEnvelopeField(journalRaw, "null")); err == nil {
		t.Fatal("journal accepted explicit null replay")
	}
}

func TestJournalV5RejectsOversizedAndPermutedPayloadManifest(t *testing.T) {
	base := store.Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	result := store.Revision("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	receipt := testJournalReceipt(base, result, "limits-ordinal", "")
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := journalBaseBinding(receipt, nil)
	if err != nil {
		t.Fatal(err)
	}
	validDigest := "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
	tests := []journal{
		{Version: 5, HashAlgorithm: "sha256", Stage: stage, Base: []journalBaseFile{}, BaseBinding: binding, Receipt: receipt, Files: []journalFile{{Path: "a.md", Payload: "payload-00000", Size: DefaultMaxStagedPayloadBytes + 1, Digest: validDigest}}},
		{Version: 5, HashAlgorithm: "sha256", Stage: stage, Base: []journalBaseFile{}, BaseBinding: binding, Receipt: receipt, Files: []journalFile{{Path: "a.md", Payload: "payload-00001", Size: 1, Digest: validDigest}, {Path: "b.md", Payload: "payload-00000", Size: 1, Digest: validDigest}}},
	}
	for _, j := range tests {
		raw, err := json.Marshal(j)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeJournal(raw); err == nil {
			t.Fatal("accepted invalid v5 staged manifest")
		}
	}
}

func appendReplayEnvelopeField(raw []byte, value string) []byte {
	trimmed := bytes.TrimSuffix(raw, []byte("}"))
	out := append([]byte(nil), trimmed...)
	return append(out, []byte(`,"replay":`+value+`}`)...)
}

func TestDiskEnvelopeDecodersRejectMalformedUTF8BeforeDecoding(t *testing.T) {
	// Arrange. Cover each envelope's own strings as well as nested receipt data.
	receipt := testJournalReceipt("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "utf-receipt", "utf-key")
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	journalRaw, err := json.Marshal(journal{Version: 4, HashAlgorithm: "sha256", Stage: stage, Files: []journalFile{{Path: "a.md", Payload: "payload-00000", Size: 1, Digest: "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"}}, Receipt: receipt})
	if err != nil {
		t.Fatal(err)
	}
	receiptRaw, err := json.Marshal(receiptFile{Version: 2, Key: "utf-key", Digest: receipt.RequestDigest, Receipt: receipt})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		raw    []byte
		needle string
		decode func([]byte) error
	}{
		{name: "journal hash algorithm", raw: journalRaw, needle: "sha256", decode: func(raw []byte) error { _, err := decodeJournal(raw); return err }},
		{name: "journal file path", raw: journalRaw, needle: "a.md", decode: func(raw []byte) error { _, err := decodeJournal(raw); return err }},
		{name: "journal payload digest", raw: journalRaw, needle: "ca978112", decode: func(raw []byte) error { _, err := decodeJournal(raw); return err }},
		{name: "journal nested receipt", raw: journalRaw, needle: "utf-receipt", decode: func(raw []byte) error { _, err := decodeJournal(raw); return err }},
		{name: "receipt key", raw: receiptRaw, needle: "utf-key", decode: func(raw []byte) error { _, err := decodeReceiptFile(raw); return err }},
		{name: "receipt digest", raw: receiptRaw, needle: "sha256:0000", decode: func(raw []byte) error { _, err := decodeReceiptFile(raw); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := append([]byte(nil), tt.raw...)
			at := bytes.Index(raw, []byte(tt.needle))
			if at < 0 {
				t.Fatalf("fixture does not contain %q", tt.needle)
			}
			raw[at] = 0xff

			// Act.
			err := tt.decode(raw)

			// Assert.
			if err == nil {
				t.Fatal("decoder accepted malformed UTF-8")
			}
		})
	}
}

func TestDecodeJournalRequiresCompleteObjectEnvelope(t *testing.T) {
	// Arrange.
	tests := []string{
		`{}`,
		`{"version":3,"hash_algorithm":"sha256","stage":".okf/staging/sha256_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/payload","files":null,"receipt":{}}`,
		`{"version":3,"hash_algorithm":"sha256","stage":".okf/staging/sha256_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/payload","files":[],"receipt":null}`,
		`{"version":3,"hash_algorithm":"sha256","stage":".okf/staging/sha256_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/payload","files":{},"receipt":{}}`,
		`{"version":3,"hash_algorithm":"sha256","stage":".okf/staging/sha256_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/payload","files":[],"receipt":[],"extra":true}`,
	}

	for _, raw := range tests {
		// Act.
		_, err := decodeJournal([]byte(raw))

		// Assert.
		if err == nil {
			t.Fatalf("decodeJournal(%s) unexpectedly succeeded", raw)
		}
	}
}

func TestRecoveryRejectsDuplicateJournalKeysWithoutChangingBytes(t *testing.T) {
	// Arrange.
	root, _ := adversarialStore(t, Config{})
	raw := []byte(`{"version":1,"files":{},"receipt":{"FormatVersion":1,"FormatVersion":1}}`)
	journalPath := filepath.Join(root, internalDirectory, "transactions", "duplicate.json")
	if err := writeDurableFixture(journalPath, raw); err != nil {
		t.Fatal(err)
	}

	// Act.
	_, openErr := Open(root, Config{})
	after, readErr := os.ReadFile(journalPath)

	// Assert.
	if !errors.Is(openErr, store.ErrStorageCorrupt) {
		t.Fatalf("Open() error = %v, want storage corruption", openErr)
	}
	if readErr != nil || !bytes.Equal(after, raw) {
		t.Fatalf("malicious journal changed: read=%v got=%q want=%q", readErr, after, raw)
	}
}

func TestSnapshotCapturesAllVisibleFilesAndIsImmutable(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "# A\n")
	writeTestFile(t, root, "asset.bin", "first")
	writeTestFile(t, root, "assets/привет, world! (v2).bin", "unicode asset")
	writeTestFile(t, root, ".okf/ignored", "internal")
	writeTestFile(t, root, "nested/.okf/file.bin", "nested asset")
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	snapshot, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "asset.bin", "second")
	got, err := snapshot.ReadFile(context.Background(), "asset.bin")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := snapshot.Paths(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if string(got) != "first" {
		t.Fatalf("snapshot content = %q, want first", got)
	}
	if len(paths) != 4 || paths[0] != "a.md" || paths[1] != "asset.bin" || paths[2] != "assets/привет, world! (v2).bin" || paths[3] != "nested/.okf/file.bin" {
		t.Fatalf("paths = %#v", paths)
	}
	wantRevision, err := store.RevisionFromManifest([]store.ManifestEntry{
		{Path: "a.md", Content: []byte("# A\n")},
		{Path: "asset.bin", Content: []byte("first")},
		{Path: "assets/привет, world! (v2).bin", Content: []byte("unicode asset")},
		{Path: "nested/.okf/file.bin", Content: []byte("nested asset")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Revision(); got != wantRevision {
		t.Fatalf("snapshot revision = %q, want manifest revision %q", got, wantRevision)
	}
	if _, err := snapshot.ReadFile(context.Background(), ".okf/ignored"); err == nil {
		t.Fatal("internal file is revision-visible")
	}
}

func TestOpenRejectsVisibleSymlink(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "# A\n")
	if err := os.Symlink(filepath.Join(root, "a.md"), filepath.Join(root, "escape.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Act.
	s, err := Open(root, Config{})
	var snapErr error
	if err == nil {
		_, snapErr = s.Snapshot(context.Background())
	}

	// Assert.
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if snapErr == nil {
		t.Fatal("Snapshot() accepted a revision-visible symlink")
	}
}

func TestOpenRejectsRequestedRootAndAncestorSymlinks(t *testing.T) {
	// Arrange.
	parent := t.TempDir()
	realParent := filepath.Join(parent, "real")
	root := filepath.Join(realParent, "bundle")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "a.md", "# A\n")
	rootLink := filepath.Join(parent, "root-link")
	ancestorLink := filepath.Join(parent, "ancestor-link")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(realParent, ancestorLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Act.
	rootStore, rootErr := Open(rootLink, Config{})
	ancestorStore, ancestorErr := Open(filepath.Join(ancestorLink, "bundle"), Config{})

	// Assert.
	if rootStore != nil || rootErr == nil || ancestorStore != nil || ancestorErr == nil {
		t.Fatalf("Open symlink root=(%v,%v), ancestor=(%v,%v)", rootStore, rootErr, ancestorStore, ancestorErr)
	}
}

func TestLookupReceiptRejectsTamperedEnvelopeAsStorageCorruption(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	key := store.IdempotencyKey("receipt-key")
	if err := s.writePrivateDurableAt(s.receiptPath(key), []byte(`{"version":1,"key":"receipt-key","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","receipt":{},"extra":true}`)); err != nil {
		t.Fatal(err)
	}

	// Act.
	_, found, err := s.lookupReceipt(key, "sha256:0000000000000000000000000000000000000000000000000000000000000000")

	// Assert.
	if found || !errors.Is(err, store.ErrStorageCorrupt) {
		t.Fatalf("lookup found=%v err=%v", found, err)
	}
}

func TestReceiptDecoderRejectsInvalidUTF8WithoutNormalizingDurableBytes(t *testing.T) {
	// Arrange. The malformed byte is inside a nested ChangedFiles string: JSON
	// decoding would otherwise replace it with U+FFFD before receipt validation.
	raw := []byte(`{"version":1,"key":"receipt-key","digest":"sha256:x","receipt":{"ChangedFiles":[{"Kind":"write","Path":"`)
	raw = append(raw, 0xff)
	raw = append(raw, []byte(`"}]}}`)...)

	// Act.
	_, decodeErr := decodeReceiptFile(raw)

	// Assert.
	if decodeErr == nil {
		t.Fatal("decodeReceiptFile accepted invalid UTF-8")
	}

	// Arrange a durable replay path with exactly the same bytes.
	root := t.TempDir()
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := store.IdempotencyKey("receipt-key")
	if err := s.writePrivateDurableAt(s.receiptPath(key), raw); err != nil {
		t.Fatal(err)
	}

	// Act.
	_, found, replayErr := s.lookupReceipt(key, "sha256:x")
	after, readErr := os.ReadFile(filepath.Join(root, s.receiptPath(key)))

	// Assert.
	if found || !errors.Is(replayErr, store.ErrStorageCorrupt) {
		t.Fatalf("lookup found=%v err=%v, want storage corruption", found, replayErr)
	}
	if readErr != nil || !bytes.Equal(after, raw) {
		t.Fatalf("invalid receipt changed: read=%v got=%q want=%q", readErr, after, raw)
	}
}

func TestDurableProtocolRejectsNilErrorShortWrites(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	s.write = func(_ *os.File, data []byte) (int, error) { return len(data) - 1, nil }
	revision := store.Revision("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	receipt := store.CommitReceipt{FormatVersion: store.CommitReceiptFormatVersion, ChangeSetID: "short-write", IdempotencyKey: "short-write", RequestDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000", BaseRevision: revision, ResultRevision: revision, CommitTime: time.Now().UTC()}

	// Act.
	journalErr := s.writeJournal(context.Background(), path.Join(internalDirectory, "transactions", "short.json"), []byte("journal"))
	visibleErr := s.writeFile("visible.md", []byte("visible"))
	receiptErr := s.writeReceipt(receipt)

	// Assert.
	if !errors.Is(journalErr, io.ErrShortWrite) || !errors.Is(visibleErr, io.ErrShortWrite) || !errors.Is(receiptErr, io.ErrShortWrite) {
		t.Fatalf("short writes journal=%v visible=%v receipt=%v", journalErr, visibleErr, receiptErr)
	}
}

func TestStorePinsRootAcrossPathSwap(t *testing.T) {
	// Arrange.
	parent := t.TempDir()
	root := filepath.Join(parent, "bundle")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "a.md", "before")
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "bundle-moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "a.md", "attacker")

	// Act.
	snap, err := s.Snapshot(context.Background())
	got, readErr := snap.ReadFile(context.Background(), "a.md")

	// Assert.
	if err != nil || readErr != nil || string(got) != "before" {
		t.Fatalf("pinned snapshot err=%v read=%v content=%q", err, readErr, got)
	}
}

func TestPublishRecoveryCompletesPostStateAndReceipt(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("before"))
	_, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	next, err := newSnapshot(context.Background(), map[string][]byte{"b.md": []byte(adversarialDocument("after"))})
	if err != nil {
		t.Fatal(err)
	}
	base, err := newSnapshot(context.Background(), map[string][]byte{"a.md": []byte(adversarialDocument("before"))})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "change", "retry")
	journalData, err := encodeJournalFixture(t, root, next, receipt)
	if err != nil {
		t.Fatal(err)
	}
	journalName, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(root, journalName)
	if err := writeDurableFixture(journalPath, journalData); err != nil {
		t.Fatal(err)
	}

	// Act.
	reopened, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := os.ReadFile(filepath.Join(root, "b.md"))
	_, oldErr := os.Stat(filepath.Join(root, "a.md"))
	replayed, found, receiptErr := reopened.lookupReceipt("retry", receipt.RequestDigest)

	// Assert.
	if readErr != nil || string(got) != adversarialDocument("after") {
		t.Fatalf("recovered file = %q, err=%v", got, readErr)
	}
	if !errors.Is(oldErr, os.ErrNotExist) {
		t.Fatalf("old file stat error = %v, want not exist", oldErr)
	}
	if receiptErr != nil || !found || replayed.ChangeSetID != "change" {
		t.Fatalf("receipt = %#v, found=%v, err=%v", replayed, found, receiptErr)
	}
}

func TestRecoveryRejectsSelfConsistentJournalWithInvalidPostStateBeforeApply(t *testing.T) {
	// A forged v4 journal can have correct payload digests and result revision.
	// Recovery must still apply the same validator/relation arbitration as a
	// normal staged mutation, before replacing any visible file.
	tests := []struct {
		name  string
		files map[string][]byte
	}{
		{"malformed document", map[string][]byte{"broken.md": []byte("not an OKF document\n")}},
		{"missing semantic target", map[string][]byte{"a.md": []byte("---\ntype: Note\nrelations:\n  uses:\n    - target: absent\n---\nA\n")}},
		{"malformed semantic relation", map[string][]byte{"a.md": []byte("---\ntype: Note\nrelations:\n  uses: absent\n---\nA\n")}},
		{"ambiguous semantic target", map[string][]byte{
			"a.md":      []byte("---\ntype: Note\nrelations:\n  uses:\n    - target: target#part\n---\nA\n"),
			"target.md": []byte("---\ntype: Note\n---\n# Part\n\n# Part\n"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			before := adversarialDocument("before")
			writeTestFile(t, root, "old.md", before)
			if _, err := Open(root, Config{}); err != nil {
				t.Fatal(err)
			}
			base, err := newSnapshot(context.Background(), map[string][]byte{"old.md": []byte(before)})
			if err != nil {
				t.Fatal(err)
			}
			next, err := newSnapshot(context.Background(), tt.files)
			if err != nil {
				t.Fatal(err)
			}
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "forged-"+tt.name, "")
			raw, err := encodeJournalFixture(t, root, next, receipt)
			if err != nil {
				t.Fatal(err)
			}
			jp, err := journalPath(receipt.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeDurableFixture(filepath.Join(root, jp), raw); err != nil {
				t.Fatal(err)
			}

			// Act.
			_, err = Open(root, Config{})

			// Assert.
			if !errors.Is(err, store.ErrStorageCorrupt) {
				t.Fatalf("Open() error=%v, want storage corruption", err)
			}
			got, readErr := os.ReadFile(filepath.Join(root, "old.md"))
			if readErr != nil || string(got) != before {
				t.Fatalf("pre-state changed: data=%q err=%v", got, readErr)
			}
			for path := range tt.files {
				if _, statErr := os.Stat(filepath.Join(root, path)); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("invalid post-state %s published: %v", path, statErr)
				}
			}
		})
	}
}

func TestRecoveryJournalStateMachineRejectsEditorDriftAndCompletesAlreadyPostState(t *testing.T) {
	makeJournal := func(t *testing.T, root string) (store.CommitReceipt, string, *snapshot) {
		t.Helper()
		before := adversarialDocument("before")
		writeTestFile(t, root, "old.md", before)
		base, err := newSnapshot(context.Background(), map[string][]byte{"old.md": []byte(before)})
		if err != nil {
			t.Fatal(err)
		}
		next, err := newSnapshot(context.Background(), map[string][]byte{"new.md": []byte(adversarialDocument("after"))})
		if err != nil {
			t.Fatal(err)
		}
		receipt := testJournalReceipt(base.Revision(), next.Revision(), "state-machine", "state-machine-key")
		raw, err := encodeJournalFixture(t, root, next, receipt)
		if err != nil {
			t.Fatal(err)
		}
		jp, err := journalPath(receipt.RequestDigest)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeDurableFixture(filepath.Join(root, jp), raw); err != nil {
			t.Fatal(err)
		}
		return receipt, jp, next
	}

	t.Run("editor drift preserves evidence and visible bytes", func(t *testing.T) {
		root := t.TempDir()
		if _, err := Open(root, Config{}); err != nil {
			t.Fatal(err)
		}
		receipt, journal, _ := makeJournal(t, root)
		if err := os.Remove(filepath.Join(root, "old.md")); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, root, "editor.md", adversarialDocument("drift"))
		before, err := os.ReadFile(filepath.Join(root, "editor.md"))
		if err != nil {
			t.Fatal(err)
		}

		opened, err := Open(root, Config{})
		if opened != nil {
			_ = opened.Close()
		}
		if !errors.Is(err, store.ErrStorageCorrupt) {
			t.Fatalf("Open error=%v, want storage corruption", err)
		}
		after, readErr := os.ReadFile(filepath.Join(root, "editor.md"))
		if readErr != nil || !bytes.Equal(after, before) {
			t.Fatalf("editor bytes changed: %q / %v", after, readErr)
		}
		if _, statErr := os.Stat(filepath.Join(root, journal)); statErr != nil {
			t.Fatalf("journal evidence removed: %v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(root, "new.md")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("staged state published: %v", statErr)
		}
		_ = receipt
	})

	t.Run("already post state completes receipt without visible rewrite on same instance and reopen", func(t *testing.T) {
		root := t.TempDir()
		s, err := Open(root, Config{Fault: func(step Step) error {
			if step == StepFileWrite {
				return errors.New("visible write forbidden")
			}
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		receipt, journal, next := makeJournal(t, root)
		if err := os.Remove(filepath.Join(root, "old.md")); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, root, "new.md", string(next.files["new.md"]))

		if _, err := s.Snapshot(context.Background()); err != nil {
			t.Fatalf("same-instance recovery: %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(root, journal)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("journal not cleaned: %v", statErr)
		}
		if _, found, err := s.lookupReceipt(receipt.IdempotencyKey, receipt.RequestDigest); err != nil || !found {
			t.Fatalf("receipt after same-instance recovery: found=%v err=%v", found, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(root, Config{})
		if err != nil {
			t.Fatalf("reopen after recovery: %v", err)
		}
		defer reopened.Close()
		if _, found, err := reopened.lookupReceipt(receipt.IdempotencyKey, receipt.RequestDigest); err != nil || !found {
			t.Fatalf("receipt after reopen: found=%v err=%v", found, err)
		}
	})
}

func TestRecoveryJournalV5FinishesOnlyProvenanceSafePartialState(t *testing.T) {
	makeFixture := func(t *testing.T, corrupt bool) (string, store.CommitReceipt) {
		t.Helper()
		root := t.TempDir()
		writeTestFile(t, root, "a.md", adversarialDocument("old-a"))
		writeTestFile(t, root, "b.md", adversarialDocument("old-b"))
		if _, err := Open(root, Config{}); err != nil {
			t.Fatal(err)
		}
		base, err := newSnapshot(context.Background(), map[string][]byte{"a.md": []byte(adversarialDocument("old-a")), "b.md": []byte(adversarialDocument("old-b"))})
		if err != nil {
			t.Fatal(err)
		}
		next, err := newSnapshot(context.Background(), map[string][]byte{"a.md": []byte(adversarialDocument("new-a")), "c.md": []byte(adversarialDocument("new-c"))})
		if err != nil {
			t.Fatal(err)
		}
		receipt := testJournalReceipt(base.Revision(), next.Revision(), "partial-v5", "partial-v5-key")
		raw, err := encodeJournalFixture(t, root, next, receipt)
		if err != nil {
			t.Fatal(err)
		}
		jp, err := journalPath(receipt.RequestDigest)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeDurableFixture(filepath.Join(root, jp), raw); err != nil {
			t.Fatal(err)
		}
		// Simulate a crash after only the update became visible: b is still base,
		// c is not yet created. Both values are journal-provenance-safe.
		writeTestFile(t, root, "a.md", adversarialDocument("new-a"))
		if corrupt {
			writeTestFile(t, root, "unrelated.md", adversarialDocument("editor"))
		}
		return root, receipt
	}

	t.Run("mixed base/result state completes", func(t *testing.T) {
		root, receipt := makeFixture(t, false)
		s, err := Open(root, Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		snap := adversarialSnapshot(t, s)
		if snap.Revision() != receipt.ResultRevision {
			t.Fatalf("recovered revision=%s want=%s", snap.Revision(), receipt.ResultRevision)
		}
		if _, err := os.Stat(filepath.Join(root, "b.md")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("base delete not completed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "c.md")); err != nil {
			t.Fatalf("result create missing: %v", err)
		}
	})

	t.Run("unrelated path is corruption and evidence remains", func(t *testing.T) {
		root, receipt := makeFixture(t, true)
		jp, err := journalPath(receipt.RequestDigest)
		if err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(filepath.Join(root, "unrelated.md"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = Open(root, Config{})
		if !errors.Is(err, store.ErrStorageCorrupt) {
			t.Fatalf("Open error=%v, want storage corruption", err)
		}
		after, readErr := os.ReadFile(filepath.Join(root, "unrelated.md"))
		if readErr != nil || !bytes.Equal(before, after) {
			t.Fatalf("unrelated bytes changed: %q / %v", after, readErr)
		}
		if _, statErr := os.Stat(filepath.Join(root, jp)); statErr != nil {
			t.Fatalf("journal evidence removed: %v", statErr)
		}
	})
}

func TestReceiptReplayAndConflict(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	revision := store.Revision("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	receipt := store.CommitReceipt{FormatVersion: store.CommitReceiptFormatVersion, ChangeSetID: "same-change", IdempotencyKey: "same", RequestDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000", BaseRevision: revision, ResultRevision: revision, CommitTime: time.Now().UTC()}
	if err := s.writeReceipt(receipt); err != nil {
		t.Fatal(err)
	}

	// Act.
	replayed, found, replayErr := s.lookupReceipt("same", receipt.RequestDigest)
	_, _, conflictErr := s.lookupReceipt("same", "sha256:1111111111111111111111111111111111111111111111111111111111111111")

	// Assert.
	if replayErr != nil || !found || replayed.RequestDigest != receipt.RequestDigest {
		t.Fatalf("replay = %#v, found=%v, err=%v", replayed, found, replayErr)
	}
	var conflict *store.IdempotencyConflict
	if !errors.As(conflictErr, &conflict) {
		t.Fatalf("conflict error = %v", conflictErr)
	}
}

func TestRevisionChangesWithPathAndContent(t *testing.T) {
	// Arrange.
	one, err := newSnapshot(context.Background(), map[string][]byte{"a.md": []byte("same")})
	if err != nil {
		t.Fatal(err)
	}
	two, err := newSnapshot(context.Background(), map[string][]byte{"b.md": []byte("same")})
	if err != nil {
		t.Fatal(err)
	}
	three, err := newSnapshot(context.Background(), map[string][]byte{"a.md": []byte("different")})
	if err != nil {
		t.Fatal(err)
	}

	// Act / Assert.
	if one.Revision() == two.Revision() || one.Revision() == three.Revision() {
		t.Fatalf("revision does not bind both path and content")
	}
}

func TestDiffPairsEqualContentMovesDeterministically(t *testing.T) {
	// Arrange.
	base, err := newSnapshot(context.Background(), map[string][]byte{
		"a.md": []byte("same"), "b.md": []byte("same"),
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := newSnapshot(context.Background(), map[string][]byte{
		"x.md": []byte("same"), "y.md": []byte("same"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []store.FileChange{{Kind: store.FileRename, From: "a.md", Path: "x.md"}, {Kind: store.FileRename, From: "b.md", Path: "y.md"}}

	// Act / Assert. Repeat to exercise randomized map iteration.
	for i := 0; i < 100; i++ {
		got := diff(base, next)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("diff run %d = %#v, want %#v", i, got, want)
		}
	}
}

func TestRecoveryAfterInjectedPostJournalFaults(t *testing.T) {
	steps := []Step{StepFileWrite, StepFileSync, StepFileClose, StepRename, StepRemove, StepDirectorySync}
	for _, step := range steps {
		t.Run(string(step), func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeTestFile(t, root, "old.md", adversarialDocument("old"))
			calls := 0
			s, err := Open(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			s.config.Fault = func(got Step) error {
				if got == step {
					calls++
					// Journal file boundaries have dedicated steps, so these are
					// unambiguously post-journal apply boundaries.
					if calls >= 1 {
						return errors.New("injected " + string(step))
					}
				}
				return nil
			}
			next, err := newSnapshot(context.Background(), map[string][]byte{"new.md": []byte(adversarialDocument("new"))})
			if err != nil {
				t.Fatal(err)
			}
			base, baseErr := newSnapshot(context.Background(), map[string][]byte{"old.md": []byte(adversarialDocument("old"))})
			if baseErr != nil {
				t.Fatal(baseErr)
			}
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "recovery-"+string(step), "")

			// Act.
			err = s.publish(context.Background(), next, receipt)
			reopened, openErr := Open(root, Config{})
			_ = reopened
			got, readErr := os.ReadFile(filepath.Join(root, "new.md"))

			// Assert.
			if err == nil {
				t.Fatalf("publish did not hit %s", step)
			}
			if openErr != nil || readErr != nil || string(got) != adversarialDocument("new") {
				t.Fatalf("recovery: open=%v read=%v content=%q", openErr, readErr, got)
			}
		})
	}
}

func TestLargeStagedPayloadRecoversAfterDurableJournal(t *testing.T) {
	// Arrange. The payload is intentionally larger than the metadata ceiling:
	// only the compact manifest is metadata.
	root := t.TempDir()
	writeTestFile(t, root, "old.md", adversarialDocument("old"))
	fired := false
	armed := false
	s, err := Open(root, Config{PostFault: func(step Step) error {
		if armed && step == StepJournalDirectorySync && !fired {
			fired = true
			return errors.New("crash after journal")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	base := adversarialSnapshot(t, s)
	armed = true
	large := bytes.Repeat([]byte("x"), maxMetadataRead+1)
	next, err := newSnapshot(context.Background(), map[string][]byte{"new.md": []byte(adversarialDocument("new")), "asset.bin": large})
	if err != nil {
		t.Fatal(err)
	}
	err = s.publish(context.Background(), next, testJournalReceipt(base.Revision(), next.Revision(), "large-stage", "large-stage"))
	if err == nil || !fired {
		t.Fatalf("publish error=%v fired=%v", err, fired)
	}

	// Act. Reopening repeatedly must finish the same durable transaction.
	reopened, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := got.ReadFile(context.Background(), "asset.bin")
	if err != nil || !bytes.Equal(data, large) || got.Revision() != next.Revision() {
		t.Fatalf("large recovery: read=%v len=%d revision=%s", err, len(data), got.Revision())
	}
	entries, err := os.ReadDir(filepath.Join(root, internalDirectory, "staging"))
	if err != nil && !errors.Is(err, os.ErrNotExist) || len(entries) != 0 {
		t.Fatalf("staging leftovers=%v err=%v", entries, err)
	}
}

func TestRecoveryRejectsMissingCorruptAndSymlinkedV3Payload(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, payload string)
	}{
		{name: "missing", mutate: func(t *testing.T, payload string) {
			t.Helper()
			if err := os.Remove(payload); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "corrupt", mutate: func(t *testing.T, payload string) {
			t.Helper()
			if err := os.WriteFile(payload, []byte("corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", mutate: func(t *testing.T, payload string) {
			t.Helper()
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(payload); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, payload); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "old.md", adversarialDocument("old"))
			base, err := newSnapshot(context.Background(), map[string][]byte{"old.md": []byte(adversarialDocument("old"))})
			if err != nil {
				t.Fatal(err)
			}
			next, err := newSnapshot(context.Background(), map[string][]byte{"new.md": []byte(adversarialDocument("new"))})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := encodeJournalFixture(t, root, next, testJournalReceipt(base.Revision(), next.Revision(), "v3-payload", ""))
			if err != nil {
				t.Fatal(err)
			}
			jr := testJournalReceipt(base.Revision(), next.Revision(), "v3-payload", "")
			stage, err := journalStage(jr.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}
			payload := filepath.Join(root, stage, "payload-00000")
			tc.mutate(t, payload)
			jp, err := journalPath(jr.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeDurableFixture(filepath.Join(root, jp), raw); err != nil {
				t.Fatal(err)
			}
			_, err = Open(root, Config{})
			if !errors.Is(err, store.ErrStorageCorrupt) {
				t.Fatalf("Open() error=%v, want storage corruption", err)
			}
			if _, err := os.Stat(filepath.Join(root, "new.md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("published corrupted payload: %v", err)
			}
		})
	}
}

func TestRecoveryBindsV3JournalStageAndFilenameToRequestDigest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, j *journal, receipt store.CommitReceipt) string
	}{
		{name: "cross request stage", mutate: func(t *testing.T, j *journal, _ store.CommitReceipt) string {
			other, err := journalStage("sha256:1111111111111111111111111111111111111111111111111111111111111111")
			if err != nil {
				t.Fatal(err)
			}
			j.Stage = other
			return ""
		}},
		{name: "path variant", mutate: func(_ *testing.T, j *journal, _ store.CommitReceipt) string {
			j.Stage = ".okf/staging/txn-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/payload"
			return ""
		}},
		{name: "filename mismatch", mutate: func(_ *testing.T, _ *journal, _ store.CommitReceipt) string {
			return "txn-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "old.md", adversarialDocument("old"))
			base, err := newSnapshot(context.Background(), map[string][]byte{"old.md": []byte(adversarialDocument("old"))})
			if err != nil {
				t.Fatal(err)
			}
			next, err := newSnapshot(context.Background(), map[string][]byte{"new.md": []byte(adversarialDocument("new"))})
			if err != nil {
				t.Fatal(err)
			}
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "bind", "bind-key")
			raw, err := encodeJournalFixture(t, root, next, receipt)
			if err != nil {
				t.Fatal(err)
			}
			var j journal
			if err := json.Unmarshal(raw, &j); err != nil {
				t.Fatal(err)
			}
			name := tc.mutate(t, &j, receipt)
			raw, err = json.Marshal(j)
			if err != nil {
				t.Fatal(err)
			}
			if name == "" {
				jp, err := journalPath(receipt.RequestDigest)
				if err != nil {
					t.Fatal(err)
				}
				name = path.Base(jp)
			}
			if err := writeDurableFixture(filepath.Join(root, internalDirectory, "transactions", name), raw); err != nil {
				t.Fatal(err)
			}
			_, err = Open(root, Config{})
			if !errors.Is(err, store.ErrStorageCorrupt) {
				t.Fatalf("Open error=%v, want storage corruption", err)
			}
			if _, err := os.Stat(filepath.Join(root, "new.md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("bound journal published state: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, internalDirectory, "receipts", "bind-key.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("bound journal wrote receipt: %v", err)
			}
		})
	}
}

func TestOpenCleansOrphanStage(t *testing.T) {
	root := t.TempDir()
	id := "txn-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	payload := filepath.Join(root, internalDirectory, "staging", id, "payload", "payload-00000")
	writeTestFile(t, root, path.Join(internalDirectory, "staging", id, "payload", "payload-00000"), "orphan")
	if _, err := os.Stat(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, Config{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, internalDirectory, "staging", id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan stage remains: %v", err)
	}
}

func TestStageCleanupPostFaultsRecoverIdempotently(t *testing.T) {
	steps := []Step{
		StepStageCleanupPayloadRemove,
		StepStageCleanupPayloadDirectory,
		StepStageCleanupPayloadDirRemove,
		StepStageCleanupStageDirectory,
		StepStageCleanupStageDirRemove,
		StepStageCleanupRootDirectory,
	}
	for _, post := range []bool{false, true} {
		for _, step := range steps {
			t.Run(fmt.Sprintf("%t/%s", post, step), func(t *testing.T) {
				root := t.TempDir()
				writeTestFile(t, root, "old.md", adversarialDocument("old"))
				s, err := Open(root, Config{})
				if err != nil {
					t.Fatal(err)
				}
				base := adversarialSnapshot(t, s)
				next, err := newSnapshot(context.Background(), map[string][]byte{"new.md": []byte(adversarialDocument("new")), "asset.bin": []byte("asset")})
				if err != nil {
					t.Fatal(err)
				}
				fired := false
				inject := func(got Step) error {
					if got == step && !fired {
						fired = true
						return errors.New("cleanup crash")
					}
					return nil
				}
				if post {
					s.config.PostFault = inject
				} else {
					s.config.Fault = inject
				}
				receipt := testJournalReceipt(base.Revision(), next.Revision(), "cleanup-"+string(step), "cleanup-key")
				err = s.publish(context.Background(), next, receipt)
				if err == nil || !fired {
					t.Fatalf("publish error=%v fired=%v", err, fired)
				}
				first, err := Open(root, Config{})
				if err != nil {
					t.Fatal(err)
				}
				second, err := Open(root, Config{})
				if err != nil {
					t.Fatal(err)
				}
				for _, reopened := range []*Store{first, second} {
					snap, err := reopened.Snapshot(context.Background())
					if err != nil || snap.Revision() != next.Revision() {
						t.Fatalf("recovery snapshot=%v err=%v", snap, err)
					}
					got, found, err := reopened.lookupReceipt("cleanup-key", receipt.RequestDigest)
					if err != nil || !found || got.ResultRevision != next.Revision() {
						t.Fatalf("receipt=%#v found=%v err=%v", got, found, err)
					}
				}
				entries, err := os.ReadDir(filepath.Join(root, internalDirectory, "staging"))
				if err != nil && !errors.Is(err, os.ErrNotExist) || len(entries) != 0 {
					t.Fatalf("staging leftovers=%v err=%v", entries, err)
				}
			})
		}
	}
}

func TestStageAndReceiptFaultMatrixConverges(t *testing.T) {
	// Private metadata sync/close are first exercised while staging payloads;
	// their pre/post faults must abort before a journal or receipt is visible.
	stageSteps := []Step{StepStageFileWrite, StepStageFileSync, StepStageFileClose, StepStageRename, StepStageDirectorySync, StepPrivateMetadataSync, StepPrivateMetadataClose}
	receiptSteps := []Step{StepReceiptWrite, StepReceiptSync, StepReceiptClose, StepReceiptRename, StepReceiptDirectorySync, StepReceiptPrune}
	for _, post := range []bool{false, true} {
		for _, step := range append(append([]Step(nil), stageSteps...), receiptSteps...) {
			if post && step == StepReceiptPrune {
				continue // no durable prune boundary exists until an expired receipt is removed.
			}
			t.Run(fmt.Sprintf("%t/%s", post, step), func(t *testing.T) {
				root := t.TempDir()
				writeTestFile(t, root, "old.md", adversarialDocument("old"))
				s, err := Open(root, Config{})
				if err != nil {
					t.Fatal(err)
				}
				base := adversarialSnapshot(t, s)
				next, err := newSnapshot(context.Background(), map[string][]byte{"new.md": []byte(adversarialDocument("new"))})
				if err != nil {
					t.Fatal(err)
				}
				fired := false
				inject := func(got Step) error {
					if got == step && !fired {
						fired = true
						return errors.New("injected")
					}
					return nil
				}
				if post {
					s.config.PostFault = inject
				} else {
					s.config.Fault = inject
				}
				receipt := testJournalReceipt(base.Revision(), next.Revision(), "matrix-"+string(step), "matrix-key")
				err = s.publish(context.Background(), next, receipt)
				if err == nil || !fired {
					t.Fatalf("publish error=%v fired=%v", err, fired)
				}
				reopened, err := Open(root, Config{})
				if err != nil {
					t.Fatal(err)
				}
				_, err = Open(root, Config{})
				if err != nil {
					t.Fatal(err)
				}
				snap, err := reopened.Snapshot(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				isStage := false
				for _, candidate := range stageSteps {
					if step == candidate {
						isStage = true
					}
				}
				if isStage {
					if snap.Revision() != base.Revision() {
						t.Fatalf("stage fault published %s", snap.Revision())
					}
					if _, found, err := reopened.lookupReceipt("matrix-key", receipt.RequestDigest); err != nil || found {
						t.Fatalf("stage fault receipt found=%v err=%v", found, err)
					}
					entries, readErr := os.ReadDir(filepath.Join(root, internalDirectory, "staging"))
					if readErr != nil && !errors.Is(readErr, os.ErrNotExist) || len(entries) != 0 {
						t.Fatalf("stage fault leaked scratch=%v err=%v", entries, readErr)
					}
				} else {
					if snap.Revision() != next.Revision() {
						t.Fatalf("receipt fault did not recover post-state: %s", snap.Revision())
					}
					got, found, err := reopened.lookupReceipt("matrix-key", receipt.RequestDigest)
					if err != nil || !found || got.ResultRevision != next.Revision() {
						t.Fatalf("receipt replay=%#v found=%v err=%v", got, found, err)
					}
				}
			})
		}
	}
}

// TestPrivateMetadataSyncCloseFaultRecovery addresses each production use of
// the chmod-following sync/close: staged payload, journal, then receipt.
func TestPrivateMetadataSyncCloseFaultRecovery(t *testing.T) {
	for _, step := range []Step{StepPrivateMetadataSync, StepPrivateMetadataClose} {
		for _, post := range []bool{false, true} {
			for occurrence := 1; occurrence <= 3; occurrence++ {
				t.Run(fmt.Sprintf("%s/post=%t/occurrence=%d", step, post, occurrence), func(t *testing.T) {
					root := t.TempDir()
					writeTestFile(t, root, "old.md", adversarialDocument("old"))
					s, err := Open(root, Config{})
					if err != nil {
						t.Fatal(err)
					}
					base := adversarialSnapshot(t, s)
					next, err := newSnapshot(context.Background(), map[string][]byte{"new.md": []byte(adversarialDocument("new"))})
					if err != nil {
						t.Fatal(err)
					}
					seen := 0
					inject := func(got Step) error {
						if got == step {
							seen++
							if seen == occurrence {
								return errors.New("injected")
							}
						}
						return nil
					}
					if post {
						s.config.PostFault = inject
					} else {
						s.config.Fault = inject
					}
					r := testJournalReceipt(base.Revision(), next.Revision(), fmt.Sprintf("private-%s-%d", step, occurrence), "private-metadata-key")
					if err := s.publish(context.Background(), next, r); err == nil || seen < occurrence {
						t.Fatalf("publish error=%v occurrences=%d", err, seen)
					}
					reopened, err := Open(root, Config{})
					if err != nil {
						t.Fatal(err)
					}
					snap := adversarialSnapshot(t, reopened)
					if snap.Revision() != base.Revision() && snap.Revision() != next.Revision() {
						t.Fatalf("recovery=%s; neither pre nor post", snap.Revision())
					}
					got, found, err := reopened.lookupReceipt("private-metadata-key", r.RequestDigest)
					if snap.Revision() == next.Revision() && (err != nil || !found || got.ResultRevision != next.Revision()) {
						t.Fatalf("post-state receipt=%#v found=%v err=%v", got, found, err)
					}
					entries, readErr := os.ReadDir(filepath.Join(root, internalDirectory, "staging"))
					if readErr != nil && !errors.Is(readErr, os.ErrNotExist) || len(entries) != 0 {
						t.Fatalf("scratch=%v err=%v", entries, readErr)
					}
				})
			}
		}
	}
}

func TestReceiptPrunePostFaultAfterActualDeletionRecovers(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "old.md", adversarialDocument("old"))
	s, err := Open(root, Config{ReceiptRetention: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	base := adversarialSnapshot(t, s)
	old := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < MinReceiptCount; i++ {
		key := store.IdempotencyKey(fmt.Sprintf("old-%04d", i))
		r := testJournalReceipt(base.Revision(), base.Revision(), fmt.Sprintf("old-%04d", i), key)
		r.CommitTime = old
		if err := s.writeReceipt(r); err != nil {
			t.Fatal(err)
		}
	}
	next, err := newSnapshot(context.Background(), map[string][]byte{"new.md": []byte(adversarialDocument("new"))})
	if err != nil {
		t.Fatal(err)
	}
	fired := false
	s.config.PostFault = func(step Step) error {
		if step == StepReceiptPrune && !fired {
			fired = true
			return errors.New("prune crash")
		}
		return nil
	}
	r := testJournalReceipt(base.Revision(), next.Revision(), "prune-current", "prune-current")
	err = s.publish(context.Background(), next, r)
	if err == nil || !fired {
		t.Fatalf("publish error=%v fired=%v", err, fired)
	}
	reopened, err := Open(root, Config{ReceiptRetention: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Open(root, Config{ReceiptRetention: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := reopened.Snapshot(context.Background())
	if err != nil || snap.Revision() != next.Revision() {
		t.Fatalf("snapshot=%v err=%v", snap, err)
	}
	got, found, err := reopened.lookupReceipt("prune-current", r.RequestDigest)
	if err != nil || !found || got.ResultRevision != next.Revision() {
		t.Fatalf("receipt=%#v found=%v err=%v", got, found, err)
	}
}

// TestDurableFaultMatrixCoverage is deliberately a compile-time-adjacent
// inventory: adding a durable stage/receipt/cleanup boundary must update a
// matrix test, rather than silently relying on a nearby generic Step.
func TestDurableFaultMatrixCoverage(t *testing.T) {
	generic := []Step{StepMkdir, StepChmod, StepFileWrite, StepFileSync, StepFileClose, StepRename, StepRemove, StepDirectorySync}
	journal := []Step{StepJournalWrite, StepJournalFileWrite, StepJournalFileSync, StepJournalFileClose, StepJournalRename, StepJournalDirectorySync}
	stage := []Step{StepStageFileWrite, StepStageFileSync, StepStageFileClose, StepStageRename, StepStageDirectorySync}
	capability := []Step{StepCapabilityMkdir, StepCapabilityChmod, StepCapabilityFileWrite, StepCapabilityFileSync, StepCapabilityFileClose, StepCapabilityRename, StepCapabilityRemove, StepCapabilityDirectorySync}
	receipt := []Step{StepReceiptWrite, StepReceiptSync, StepReceiptClose, StepReceiptRename, StepReceiptDirectorySync, StepReceiptPrune, StepPrivateMetadataSync, StepPrivateMetadataClose, StepPrivateDirectoryChmod, StepPrivateDirectorySync, StepPrivateDirectoryClose, StepPrivateDirectoryParentSync}
	cleanup := []Step{StepStageCleanupPayloadRemove, StepStageCleanupPayloadDirectory, StepStageCleanupPayloadDirRemove, StepStageCleanupStageDirectory, StepStageCleanupStageDirRemove, StepStageCleanupRootDirectory}
	expected := append(append(append(append(append(append([]Step(nil), generic...), journal...), stage...), capability...), receipt...), cleanup...)
	expected = append(expected, StepTempCleanupRemove, StepTempCleanupDirectorySync)
	seen := map[Step]bool{}
	for _, step := range expected {
		if step == "" || seen[step] {
			t.Fatalf("invalid or duplicate durable matrix step %q", step)
		}
		seen[step] = true
	}
	// StageAndReceiptFaultMatrixConverges covers Fault+PostFault for stage and
	// receipt hooks, except ReceiptPrune PostFault. That hook is covered by
	// TestReceiptPrunePostFaultAfterActualDeletionRecovers because it fires only
	// after an actual retention deletion. Cleanup matrix covers both modes.
	actual := durableFaultInventory()
	if len(actual) != len(seen) {
		t.Fatalf("production inventory count=%d want=%d", len(actual), len(seen))
	}
	for _, step := range actual {
		if !seen[step] {
			t.Fatalf("production durable step %q lacks matrix applicability", step)
		}
	}
	for step := range seen {
		found := false
		for _, declared := range actual {
			if step == declared {
				found = true
			}
		}
		if !found {
			t.Fatalf("matrix step %q missing production inventory", step)
		}
	}
	if !seen[StepReceiptPrune] {
		t.Fatal("seeded ReceiptPrune exception missing")
	}
	for _, step := range []Step{StepTempCleanupRemove, StepTempCleanupDirectorySync} {
		if !seen[step] {
			t.Fatalf("temp cleanup step %q missing", step)
		}
	}
	if len(seen) == 0 {
		t.Fatal("empty durable inventory")
	}
}

func TestSameInstanceObservationRecoversDurableJournal(t *testing.T) {
	for _, observe := range []string{"snapshot", "preview"} {
		t.Run(observe, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeTestFile(t, root, "old.md", adversarialDocument("old"))
			armed := false
			fired := false
			s, err := Open(root, Config{PostFault: func(step Step) error {
				if armed && !fired && step == StepJournalDirectorySync {
					fired = true
					return errors.New("crash after durable journal")
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			base := adversarialSnapshot(t, s)
			next, err := newSnapshot(context.Background(), map[string][]byte{"new.md": []byte(adversarialDocument("new"))})
			if err != nil {
				t.Fatal(err)
			}
			armed = true
			if err := s.publish(context.Background(), next, testJournalReceipt(base.Revision(), next.Revision(), "same-instance-"+observe, "")); err == nil {
				t.Fatal("publish unexpectedly completed")
			}

			// Act.
			switch observe {
			case "snapshot":
				snap, err := s.Snapshot(context.Background())
				if err != nil || snap.Revision() != next.Revision() {
					t.Fatalf("Snapshot() revision=%v err=%v, want %s", snap, err, next.Revision())
				}
			case "preview":
				change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "same-instance-preview", Actor: "tester", BaseRevision: next.Revision(), Operations: []store.Operation{store.EnsureRelation{Source: adversarialRef(t, "new"), Type: "uses", Target: adversarialRef(t, "new")}}}
				preview, err := s.Preview(context.Background(), change)
				if err != nil || preview.BaseRevision != next.Revision() {
					t.Fatalf("Preview() base=%s err=%v, want %s", preview.BaseRevision, err, next.Revision())
				}
			}

			// Assert.
			if _, err := os.Stat(filepath.Join(root, "old.md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("mixed pre-state file remains: %v", err)
			}
		})
	}
}

func TestOpenCleansOnlyRegularOrphanTempsRecursively(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(root, "nested", "deep", ".okf-tmp-crashed")
	if err := os.WriteFile(regular, []byte("scratch"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "nested", ".okf-tmp-link")
	if err := os.Symlink(outside, symlink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	// Act.
	if _, err := Open(root, Config{}); err != nil {
		t.Fatal(err)
	}

	// Assert.
	if _, err := os.Lstat(regular); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("regular orphan=%v, want removed", err)
	}
	info, err := os.Lstat(symlink)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("special orphan protection info=%v err=%v", info, err)
	}
}

func TestTempCleanupFaultsLeaveNoScratchFiles(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, step := range []Step{StepTempCleanupRemove, StepTempCleanupDirectorySync} {
			t.Run(fmt.Sprintf("%t/%s", post, step), func(t *testing.T) {
				root := t.TempDir()
				s, err := Open(root, Config{})
				if err != nil {
					t.Fatal(err)
				}
				fired := false
				inject := func(got Step) error {
					if got == step && !fired {
						fired = true
						return errors.New("temp cleanup fault")
					}
					return nil
				}
				if post {
					s.config.PostFault = inject
				} else {
					s.config.Fault = inject
				}
				// Abort after temp creation, forcing deferred cleanup.
				baseFault := s.config.Fault
				s.config.Fault = func(got Step) error {
					if got == StepFileWrite {
						return errors.New("abort write")
					}
					if baseFault != nil {
						return baseFault(got)
					}
					return nil
				}
				err = s.writeDurableAt("target.md", []byte("data"), 0o644)
				if err == nil || !fired {
					t.Fatalf("write error=%v fired=%v", err, fired)
				}
				_, err = Open(root, Config{})
				if err != nil {
					t.Fatal(err)
				}
				_, err = Open(root, Config{})
				if err != nil {
					t.Fatal(err)
				}
				entries, err := os.ReadDir(root)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".okf-tmp-") {
						t.Fatalf("temp leak %s", entry.Name())
					}
				}
			})
		}
	}
}

func TestRecoveryRejectsTraversalJournal(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	_, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "must-not-write")
	data := []byte(`{"version":1,"files":{"../must-not-write":"eA=="},"receipt":{}}`)
	if err := writeDurableFixture(filepath.Join(root, internalDirectory, "transactions", "bad.json"), data); err != nil {
		t.Fatal(err)
	}

	// Act.
	_, openErr := Open(root, Config{})
	_, statErr := os.Stat(outside)

	// Assert.
	if openErr == nil {
		t.Fatal("tampered journal accepted")
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("traversal wrote outside root: %v", statErr)
	}
}

func encodeJournalFixture(t *testing.T, root string, next *snapshot, receipt store.CommitReceipt) ([]byte, error) {
	t.Helper()
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		return nil, err
	}
	baseFiles, err := readVisibleRoot(context.Background(), mustOpenRoot(t, root))
	if err != nil {
		return nil, err
	}
	j := journal{Version: 5, HashAlgorithm: "sha256", Stage: stage, Receipt: receipt}
	for _, p := range sortedFilePaths(baseFiles) {
		data := baseFiles[p]
		sum := sha256.Sum256(data)
		j.Base = append(j.Base, journalBaseFile{Path: p, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:])})
	}
	j.BaseBinding, err = journalBaseBinding(receipt, j.Base)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(next.files))
	for p := range next.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for i, p := range paths {
		data := next.files[p]
		sum := sha256.Sum256(data)
		payload := fmt.Sprintf("payload-%05d", i)
		j.Files = append(j.Files, journalFile{Path: p, Payload: payload, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:])})
		writeTestFile(t, root, path.Join(stage, payload), string(data))
	}
	return json.Marshal(j)
}

func mustOpenRoot(t *testing.T, root string) *os.Root {
	t.Helper()
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture construction is short-lived. readVisibleRoot owns no root close.
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func testJournalReceipt(base, result store.Revision, id string, key store.IdempotencyKey) store.CommitReceipt {
	return store.CommitReceipt{
		FormatVersion:  store.CommitReceiptFormatVersion,
		ChangeSetID:    store.ChangeSetID(id),
		IdempotencyKey: key,
		RequestDigest:  "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		BaseRevision:   base,
		ResultRevision: result,
		CommitTime:     time.Now().UTC(),
	}
}

func writeTestFile(t *testing.T, root, relative, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeDurableFixture creates a journal fixture through pathname APIs. It is
// deliberately test-only: the production store mutates exclusively via its
// pinned root descriptor.
func writeDurableFixture(target string, data []byte) (err error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".okf-test-tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	err = dir.Sync()
	return errors.Join(err, dir.Close())
}
