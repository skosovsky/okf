package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFrozenCorpusAndGold(t *testing.T) {
	// Arrange.
	repo, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(repo, "benchmarks/retrieval/testdata/freeze.json")
	queries := filepath.Join(repo, "benchmarks/retrieval/testdata/queries.json")
	root := filepath.Join(repo, "benchmarks/retrieval/testdata/corpus")
	// Act.
	files, digest, err := verifyFreeze(manifest, queries, root)
	raw, readErr := os.ReadFile(queries)
	var corpus Corpus
	decodeErr := json.Unmarshal(raw, &corpus)
	goldErr := validateGold(corpus, files)
	// Assert.
	if err != nil || readErr != nil || decodeErr != nil || goldErr != nil || digest == "" {
		t.Fatalf("freeze=%v read=%v decode=%v gold=%v", err, readErr, decodeErr, goldErr)
	}
	corpus.Cases[0].Relevant[0].Line++
	if validateGold(corpus, files) == nil {
		t.Fatal("wrong gold line accepted")
	}
	corpus.Cases[0].Relevant[0].Line--
	corpus.Cases[0].Relevant[0].Evidence = "invented"
	if validateGold(corpus, files) == nil {
		t.Fatal("wrong gold evidence accepted")
	}
	// Confirm modified sources are rejected before a server can start.
	changed := t.TempDir()
	for name, b := range files {
		if err = os.WriteFile(filepath.Join(changed, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(changed, "outbox.md"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = verifyFreeze(manifest, queries, changed); err == nil {
		t.Fatal("source drift accepted")
	}
}
func TestDocumentProjectionAndScoring(t *testing.T) {
	// Arrange.
	hits := []hit{{ID: "other"}, {ID: "other"}, {ID: "correct"}, {ID: "last"}}
	gold := []Gold{{ID: "correct"}}
	// Act.
	ids, err := documentIDs(hits, "search_sections")
	found, rr := score(ids, gold)
	noAnswer, noAnswerRR := score(ids, nil)
	// Assert.
	if err != nil || len(ids) != 3 || ids[0] != "other" || ids[1] != "correct" || !found || rr != 0.5 || noAnswer || noAnswerRR != 0 {
		t.Fatalf("ids=%v err=%v found=%t rr=%v", ids, err, found, rr)
	}
	if _, err = documentIDs(make([]hit, 6), "search_sections"); err == nil {
		t.Fatal("overbudget accepted")
	}
	found, rr = score([]string{"1", "2", "3", "4", "5", "correct"}, gold)
	if found || rr != 0 {
		t.Fatal("rank six counted")
	}
}
func TestSectionValidationRejectsFalseAttribution(t *testing.T) {
	// Arrange.
	data := []byte("# One\nEvidence here.\n\n# Two\nOther.\n")
	files := map[string][]byte{"doc.md": data}
	valid := hit{ID: "doc", Path: "doc.md", Heading: "One", Locator: "doc.md#L1-L3", Start: 1, End: 3, Snippet: "Evidence here.", Digest: contentHash(data)}
	// Act.
	err := validateHit(valid, files)
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*hit){func(h *hit) { h.Path = "../doc.md" }, func(h *hit) { h.ID = "wrong" }, func(h *hit) { h.Digest = "bad" }, func(h *hit) { h.Locator = "doc.md#wrong" }, func(h *hit) { h.Snippet = "invented" }, func(h *hit) { h.Heading = "Two" }, func(h *hit) { h.End = 5 }} {
		bad := valid
		change(&bad)
		if validateHit(bad, files) == nil {
			t.Fatalf("false attribution accepted %+v", bad)
		}
	}
}
