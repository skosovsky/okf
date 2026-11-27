package fs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestOKF03JournalPreparationAllowsExistingCompanionAndRejectsUntypedNewMarkdown(t *testing.T) {
	root := t.TempDir()
	const index = "---\nokf_version: '0.3'\n---\n# Notes\n\n- [Policy](policy.md)\n"
	const concept = "---\ntype: Note\n---\n\nPolicy.\n"
	files := map[string][]byte{
		"index.md":  []byte(index),
		"policy.md": []byte(concept),
		"README.md": []byte("See [Policy](policy.md).\n"),
	}
	for path, data := range files {
		if err := os.WriteFile(filepath.Join(root, path), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	prepare := func() error {
		t.Helper()
		next, err := newSnapshotWithAlgorithm(t.Context(), files, s.config.HashAlgorithm)
		if err != nil {
			return err
		}
		receipt := testJournalReceipt(base.Revision(), next.Revision(), "companion-publication", "")
		_, _, err = s.prepareJournalContext(t.Context(), next, receipt, replaceReplay{}, productionJournalManifestByteLimits())
		return err
	}
	files["README.md"] = []byte("Read the [Policy](policy.md).\n")
	if err := prepare(); err != nil {
		t.Fatalf("existing companion update: %v", err)
	}
	files["new.md"] = []byte("Unexpected untyped document.\n")
	var invalid *store.InvalidChangeSet
	if err := prepare(); !errors.As(err, &invalid) || invalid.Code != "concept_type_required" {
		t.Fatalf("new companion: %v", err)
	}
}
