package fs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"hash"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/skosovsky/okf/store"
)

type durabilityContextKey string

type contextValueHash struct {
	canceled *atomic.Bool
	seen     *atomic.Bool
}

func (a contextValueHash) Name() string   { return "sha256" }
func (a contextValueHash) New() hash.Hash { return sha256.New() }
func (a contextValueHash) NewContext(ctx context.Context) (hash.Hash, error) {
	if a.canceled.Load() && ctx.Err() == nil && ctx.Value(durabilityContextKey("proof")) == "retained" {
		a.seen.Store(true)
	}
	return sha256.New(), nil
}

func stageRecoveryJournal(t *testing.T, config Config) (string, *Store, store.Snapshot, *snapshot) {
	t.Helper()
	root, s := adversarialStore(t, config)
	base := adversarialSnapshot(t, s)
	next, err := newSnapshotWithAlgorithm(context.Background(), map[string][]byte{
		"a.md": []byte(adversarialDocument("changed")),
		"b.md": []byte(adversarialDocument("B")),
	}, s.config.HashAlgorithm)
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "task17-recovery", "")
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
	return root, s, base, next
}

func stageRecoveryJournalWithReceipt(t *testing.T, s *Store, next *snapshot, receipt store.CommitReceipt, replay replaceReplay) {
	t.Helper()
	replay, err := sealReplay(receipt, replay)
	if err != nil {
		t.Fatal(err)
	}
	j, raw, err := s.prepareJournalContext(context.Background(), next, receipt, replay, productionJournalManifestByteLimits())
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
}

func nextSnapshotForDurabilityTest(t *testing.T, s *Store) *snapshot {
	t.Helper()
	next, err := newSnapshotWithAlgorithm(context.Background(), map[string][]byte{
		"a.md": []byte(adversarialDocument("changed")),
		"b.md": []byte(adversarialDocument("B")),
	}, s.config.HashAlgorithm)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func replaceRegularWithSameBytes(t *testing.T, root, name string) (string, []byte, os.FileInfo) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	raw, err := os.ReadFile(absolute)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(absolute)
	if err != nil {
		t.Fatal(err)
	}
	placeholder, err := os.CreateTemp(filepath.Dir(absolute), ".okf-replaced-*")
	if err != nil {
		t.Fatal(err)
	}
	quarantine := placeholder.Name()
	if err := placeholder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(quarantine); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(absolute, quarantine); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(quarantine) })
	if err := os.WriteFile(absolute, raw, before.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(absolute)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("replacement unexpectedly retained inode")
	}
	if err := os.Remove(quarantine); err != nil {
		t.Fatal(err)
	}
	return name, raw, after
}

func modeOf(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}

func makeStageNamespaceDirectory(t *testing.T, root string, s *Store, key artifactClaimKey, name string) os.FileInfo {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := s.rootFD.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.prepareDirectoryWitnessAt(context.Background(), key, name, info); err != nil {
		t.Fatal(err)
	}
	return info
}

func claimInventory(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(claimDirectory)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, entry.Name()+":"+info.Mode().String())
	}
	sort.Strings(result)
	return result
}

type exactFileState struct {
	Identity string
	Mode     os.FileMode
	Bytes    string
}

func exactFileStates(t *testing.T, root string, names []string) map[string]exactFileState {
	t.Helper()
	got := make(map[string]exactFileState, len(names))
	for _, name := range names {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(absolute)
		if err != nil {
			t.Fatalf("Lstat(%s): %v", name, err)
		}
		identity, ok := fileIdentityKey(info)
		if !ok {
			t.Fatalf("identity(%s) unavailable", name)
		}
		raw, err := os.ReadFile(absolute)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		got[name] = exactFileState{Identity: identity, Mode: info.Mode(), Bytes: string(raw)}
	}
	return got
}

func rewriteSameInode(t *testing.T, root, name string, raw []byte) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(root, filepath.FromSlash(name)), os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeReceiptBinding(t *testing.T, root string, key artifactClaimKey, record directoryWitnessRecord) {
	t.Helper()
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(key.bindingPath())), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func rehomeReceiptProof(t *testing.T, root string, oldKey artifactClaimKey, record *directoryWitnessRecord) {
	t.Helper()
	newKey, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{oldKey.bindingPath(), newKey.bindingPath()}, {oldKey.witnessPath(false), newKey.witnessPath(false)}} {
		if pair[0] == pair[1] {
			continue
		}
		if err := os.Rename(filepath.Join(root, filepath.FromSlash(pair[0])), filepath.Join(root, filepath.FromSlash(pair[1]))); err != nil {
			t.Fatal(err)
		}
	}
	writeReceiptBinding(t, root, newKey, *record)
}

func receiptEvidence(t *testing.T, root string) map[string]exactFileState {
	t.Helper()
	got := map[string]exactFileState{}
	for _, directory := range []string{claimDirectory, path.Join(internalDirectory, "receipts")} {
		absoluteDir := filepath.Join(root, filepath.FromSlash(directory))
		entries, err := os.ReadDir(absoluteDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := path.Join(directory, entry.Name())
			absolute := filepath.Join(root, filepath.FromSlash(name))
			info, err := os.Lstat(absolute)
			if err != nil {
				t.Fatal(err)
			}
			identity, ok := fileIdentityKey(info)
			if !ok {
				t.Fatalf("identity unavailable for %s", name)
			}
			raw, err := os.ReadFile(absolute)
			if err != nil {
				t.Fatal(err)
			}
			got[name] = exactFileState{Identity: identity, Mode: info.Mode(), Bytes: string(raw)}
		}
	}
	return got
}

func writeRecoveryFile(t *testing.T, root, name string, raw []byte) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func injectReadCloseError(t *testing.T, s *Store, injected error, suffix string) {
	t.Helper()
	if injected == nil {
		return
	}
	s.closeRead = func(file *os.File) error {
		actual := file.Close()
		if suffix != "" && !strings.HasSuffix(filepath.ToSlash(file.Name()), suffix) {
			return actual
		}
		return errors.Join(injected, actual)
	}
}
