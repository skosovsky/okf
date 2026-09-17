package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestRecoveryRejectsFilesystemEquivalentJournalBasePathsBeforeIO(t *testing.T) {
	aliases := []struct {
		name                 string
		actual               string
		phantom              string
		caseAliases          bool
		normalizationAliases bool
	}{
		{
			name:        "case-fold",
			actual:      "a.md",
			phantom:     "A.md",
			caseAliases: true,
		},
		{
			name:                 "NFC",
			actual:               "\u00e9.md",
			phantom:              "e\u0301.md",
			normalizationAliases: true,
		},
	}
	transitions := []struct {
		name   string
		retain bool
	}{
		{name: "delete-all"},
		{name: "retain-one", retain: true},
	}

	for _, alias := range aliases {
		for _, transition := range transitions {
			t.Run(alias.name+"/"+transition.name, func(t *testing.T) {
				// Arrange.
				root := t.TempDir()
				content := []byte(adversarialDocument("base"))
				writeTestFile(t, root, alias.actual, string(content))
				s, err := openObserved(root, Config{})
				if err != nil {
					t.Fatal(err)
				}
				registerStoreCleanup(t, s)
				s.caseAliases = alias.caseAliases
				s.normalizationAliases = alias.normalizationAliases

				base, err := s.Snapshot(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				nextFiles := map[string][]byte{}
				if transition.retain {
					nextFiles[alias.actual] = append([]byte(nil), content...)
				}
				next, err := newSnapshot(context.Background(), nextFiles)
				if err != nil {
					t.Fatal(err)
				}
				receipt := testJournalReceipt(base.Revision(), next.Revision(), alias.name+"-"+transition.name, "")
				journalName, raw := adversarialLiveDurableJournal(t, root, s, next, receipt)
				var durable journal
				if err := json.Unmarshal(raw, &durable); err != nil {
					t.Fatal(err)
				}
				if durable.Files == nil {
					durable.Files = []journalFile{}
				}
				durable.Base = append(durable.Base, journalBaseFile{
					Path:   alias.phantom,
					Size:   int64(len(content)),
					Digest: sha256Digest(content),
				})
				sort.Slice(durable.Base, func(i, j int) bool {
					return durable.Base[i].Path < durable.Base[j].Path
				})
				durable.BaseBinding, err = journalBaseBinding(receipt, durable.Base)
				if err != nil {
					t.Fatal(err)
				}
				raw, err = json.Marshal(durable)
				if err != nil {
					t.Fatal(err)
				}
				adversarialRewriteSameInode(t, filepath.Join(root, filepath.FromSlash(journalName)), raw)
				if transition.retain {
					payload := filepath.Join(root, filepath.FromSlash(path.Join(durable.Stage, durable.Files[0].Payload)))
					if err := os.Remove(payload); err != nil {
						t.Fatal(err)
					}
				}
				stagedSentinel := filepath.Join(
					root,
					filepath.FromSlash(path.Join(durable.Stage, "sentinel.bin")),
				)
				if err := os.MkdirAll(filepath.Dir(stagedSentinel), 0o700); err != nil {
					t.Fatal(err)
				}
				sentinelBytes := []byte("unreferenced staged evidence")
				if err := os.WriteFile(stagedSentinel, sentinelBytes, 0o600); err != nil {
					t.Fatal(err)
				}
				orphanTemp := filepath.Join(
					root,
					filepath.FromSlash(path.Join(temporaryDirectory, ".okf-tmp-adjacent")),
				)
				if err := os.WriteFile(orphanTemp, []byte("orphan"), 0o600); err != nil {
					t.Fatal(err)
				}
				orphanInfo, err := os.Stat(orphanTemp)
				if err != nil {
					t.Fatal(err)
				}
				orphanBytes, err := os.ReadFile(orphanTemp)
				if err != nil {
					t.Fatal(err)
				}
				publicTempLikeAssets := map[string][]byte{
					".okf-tmp-public.bin":        {0x00, 0xfe, 0xff},
					"nested/.okf-tmp-public.bin": []byte("opaque nested asset\n"),
				}
				for name, content := range publicTempLikeAssets {
					fullName := filepath.Join(root, filepath.FromSlash(name))
					if err := os.MkdirAll(filepath.Dir(fullName), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(fullName, content, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				before, err := readVisibleRoot(context.Background(), s.rootFD)
				if err != nil {
					t.Fatal(err)
				}
				provenanceReads := 0
				s.provenanceReadHook = func(string) {
					provenanceReads++
				}
				ordered := []string{alias.actual, alias.phantom}
				sort.Strings(ordered)
				wantCause := fmt.Sprintf(
					"fs store: metadata: storage corruption\n"+
						"invalid change set: filesystem-equivalent base paths %q and %q",
					ordered[0],
					ordered[1],
				)

				// Act.
				recoverErr := s.recoverForTest(context.Background())

				// Assert.
				var committed *store.CommittedError
				if !errors.Is(recoverErr, store.ErrStorageCorrupt) ||
					!errors.Is(recoverErr, store.ErrInvalidChangeSet) ||
					errors.As(recoverErr, &committed) ||
					recoverErr.Error() != wantCause {
					t.Fatalf("recover error=%v committed=%v, want pre-ownership cause %q", recoverErr, committed, wantCause)
				}
				if provenanceReads != 0 {
					t.Fatalf("provenance reads=%d, want zero before staged payload I/O", provenanceReads)
				}
				after, readErr := readVisibleRoot(context.Background(), s.rootFD)
				if readErr != nil || !reflect.DeepEqual(after, before) {
					t.Fatalf("visible state changed: before=%q after=%q read=%v", before, after, readErr)
				}
				persisted, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(journalName)))
				if readErr != nil || !reflect.DeepEqual(persisted, raw) {
					t.Fatalf("journal changed: read=%v bytes_equal=%t", readErr, reflect.DeepEqual(persisted, raw))
				}
				orphanAfter, statErr := os.Stat(orphanTemp)
				orphanAfterBytes, orphanReadErr := os.ReadFile(orphanTemp)
				if statErr != nil || orphanReadErr != nil || !os.SameFile(orphanInfo, orphanAfter) || orphanInfo.Mode() != orphanAfter.Mode() || !reflect.DeepEqual(orphanBytes, orphanAfterBytes) {
					t.Fatalf("unproven orphan changed: stat=%v read=%v same=%t mode=%v/%v bytes=%t", statErr, orphanReadErr, os.SameFile(orphanInfo, orphanAfter), orphanInfo.Mode(), orphanAfter.Mode(), reflect.DeepEqual(orphanBytes, orphanAfterBytes))
				}
				for name, want := range publicTempLikeAssets {
					got, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
					if readErr != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("public temp-like asset %q changed: read=%v bytes_equal=%t", name, readErr, reflect.DeepEqual(got, want))
					}
				}
				persistedSentinel, readErr := os.ReadFile(stagedSentinel)
				if readErr != nil || !reflect.DeepEqual(persistedSentinel, sentinelBytes) {
					t.Fatalf(
						"staged sentinel changed: read=%v bytes_equal=%t",
						readErr,
						reflect.DeepEqual(persistedSentinel, sentinelBytes),
					)
				}
			})
		}
	}
}
