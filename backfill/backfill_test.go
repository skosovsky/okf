package backfill

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
	"github.com/skosovsky/okf/validator"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
	c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z"}
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func TestExtractDeterministicBoundedDiffAndFileStats(t *testing.T) {
	// Arrange.
	dir := t.TempDir()
	gitTest(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-qm", "base")
	base := gitTest(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("changed\n"+strings.Repeat("x", 2048)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte("generated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-qm", "literal $(touch /tmp/not-executed) subject")
	head := gitTest(t, dir, "rev-parse", "HEAD")
	cfg := Config{Repository: dir, Base: base, Head: head, FirstParent: true, MaxDiffBytes: 64}
	// Act.
	one, err := Extract(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Extract(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(one)
	right, _ := json.Marshal(two)
	// Assert.
	if string(left) != string(right) || len(one.Events) != 1 {
		t.Fatalf("nondeterministic extraction: %d events", len(one.Events))
	}
	e := one.Events[0]
	if !e.DiffTruncated || len(e.Diff) != 64 || e.DiffBytes <= 64 || len(e.Files) != 2 {
		t.Fatalf("bounded event = %#v", e)
	}
	if e.Subject != "literal $(touch /tmp/not-executed) subject" {
		t.Fatalf("subject changed: %s", e.Subject)
	}
	if _, _, err := ValidateAnalysis(one, Analysis{SchemaVersion: SchemaVersion, EventsSHA256: one.EventsSHA256, PromptVersion: "test", ProducedAt: "2026-01-01T00:00:00Z", Results: []EventResult{{EventID: e.ID, Decision: DecisionRejected, Reason: "test"}}}); err != nil {
		t.Fatalf("manifest integrity: %v", err)
	}
}

func TestExtractRenameDeleteBinaryAndSkipReasons(t *testing.T) {
	// Arrange.
	dir := t.TempDir()
	gitTest(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "obsolete.txt"), []byte("obsolete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-qm", "base")
	base := gitTest(t, dir, "rev-parse", "HEAD")
	if err := os.Rename(filepath.Join(dir, "old.txt"), filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "obsolete.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "image.bin"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte("lock data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "weird $(touch nope).txt"), []byte("literal path\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", "-A")
	gitTest(t, dir, "commit", "-qm", "rename delete binary")
	head := gitTest(t, dir, "rev-parse", "HEAD")
	// Act.
	m, err := Extract(context.Background(), Config{Repository: dir, Base: base, Head: head, FirstParent: true, MaxDiffBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	// Assert.
	if len(m.Events) != 1 {
		t.Fatalf("events=%d", len(m.Events))
	}
	found := map[string]File{}
	for _, f := range m.Events[0].Files {
		found[f.Path] = f
	}
	if found["new.txt"].OldPath != "old.txt" || !strings.HasPrefix(found["new.txt"].Status, "R") || found["obsolete.txt"].Status != "D" || !found["image.bin"].Binary || found["go.sum"].SkippedReason == "" || strings.Contains(m.Events[0].Diff, "lock data") || found["weird $(touch nope).txt"].Path == "" {
		t.Fatalf("file stats=%#v", m.Events[0].Files)
	}
	analysis := Analysis{SchemaVersion: SchemaVersion, EventsSHA256: m.EventsSHA256, PromptVersion: "skip-test/v1", ProducedAt: "2026-01-03T00:00:00Z", Results: []EventResult{{EventID: m.Events[0].ID, Decision: DecisionRejected, Reason: "stat-only test"}}}
	coverage, _, err := ValidateAnalysis(m, analysis)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.SkippedFiles[found["go.sum"].SkippedReason] != 1 {
		t.Fatalf("skip counts=%#v", coverage.SkippedFiles)
	}
}

func TestExtractAllSkippedFilesKeepsStatsWithoutDiff(t *testing.T) {
	// Arrange.
	dir := t.TempDir()
	gitTest(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-qm", "base")
	base := gitTest(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte("secret lock material\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-qm", "lock only")
	head := gitTest(t, dir, "rev-parse", "HEAD")
	// Act.
	m, err := Extract(context.Background(), Config{Repository: dir, Base: base, Head: head, MaxDiffBytes: 10, FirstParent: true})
	if err != nil {
		t.Fatal(err)
	}
	// Assert.
	e := m.Events[0]
	if len(e.Files) != 1 || e.Files[0].SkippedReason == "" || e.Diff != "" || e.DiffBytes != 0 || e.DiffSHA256 != digest(nil) || e.DiffTruncated {
		t.Fatalf("all-skipped event=%#v", e)
	}
}

func testManifest(t *testing.T) (Manifest, Analysis) {
	t.Helper()
	shaA := strings.Repeat("a", 40)
	shaB := strings.Repeat("b", 40)
	events := []Event{{ID: "git:" + shaA, Commit: shaA, Parents: []string{}, Subject: "A", Author: "Test", AuthorTime: "2026-01-01T00:00:00Z", CommitterTime: "2026-01-01T00:00:00Z", Files: []File{{Path: "decision.go", Status: "M", Binary: false}}, Diff: "A", DiffSHA256: digest([]byte("A")), DiffBytes: 1}, {ID: "git:" + shaB, Commit: shaB, Parents: []string{shaA}, Subject: "B", Author: "Test", AuthorTime: "2026-01-02T00:00:00Z", CommitterTime: "2026-01-02T00:00:00Z", Files: []File{{Path: "decision.go", Status: "M", Binary: false}}, Diff: "B", DiffSHA256: digest([]byte("B")), DiffBytes: 1}}
	cfg := Config{Repository: "/tmp/repo", Base: shaA, Head: shaB, FirstParent: true, MaxDiffBytes: 100, SkipPaths: []string{}}
	cb, _ := json.Marshal(cfg)
	eb, _ := json.Marshal(events)
	m := Manifest{SchemaVersion: SchemaVersion, ExtractorVersion: ExtractorVersion, Config: cfg, Repository: cfg.Repository, Base: cfg.Base, Head: cfg.Head, ConfigSHA256: digest(cb), EventsSHA256: digest(eb), Events: events}
	a := Analysis{SchemaVersion: SchemaVersion, EventsSHA256: m.EventsSHA256, PromptVersion: "test/v1", ProducedAt: "2026-01-03T00:00:00Z", Results: []EventResult{{EventID: events[0].ID, Decision: DecisionConsidered, CandidateIDs: []string{"A"}}, {EventID: events[1].ID, Decision: DecisionConsidered, CandidateIDs: []string{"B"}}}, Candidates: []Candidate{{ID: "A", ConceptID: "getting-started", Title: "Decision", Description: "Current decision", Body: "Use A.", Evidence: []EvidenceRef{{EventID: events[0].ID, Path: "decision.go", DiffSHA256: events[0].DiffSHA256}}}, {ID: "B", ConceptID: "getting-started", Title: "Decision", Description: "Current decision", Body: "Use B.", History: []HistoryNote{{EventID: events[1].ID, Text: "A was replaced by B"}}, Evidence: []EvidenceRef{{EventID: events[1].ID, Path: "decision.go", DiffSHA256: events[1].DiffSHA256}}, Supersedes: []string{"A"}}}}
	return m, a
}

func TestReversalPlanAndStorePublication(t *testing.T) {
	// Arrange.
	m, a := testManifest(t)
	root := t.TempDir()
	index := "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n\n- [Decision](getting-started.md) - Current decision.\n"
	concept := "---\ntype: Note\ntitle: Decision\ndescription: Current decision\nstatus: draft\n---\n\n# Decision\n\nOld placeholder.\n"
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "getting-started.md"), []byte(concept), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := storefs.Open(root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := os.ReadFile(filepath.Join(root, "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Act.
	plan, err := Prepare(context.Background(), s, m, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Concepts) != 1 || !strings.Contains(plan.Concepts[0].Document, "Use B.") || strings.Contains(plan.Concepts[0].Document, "Use A.") {
		t.Fatalf("wrong active plan: %#v", plan.Concepts)
	}
	afterPreview, _ := os.ReadFile(filepath.Join(root, "getting-started.md"))
	if string(afterPreview) != string(before) {
		t.Fatal("preview mutated bundle")
	}
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	cp, err := Apply(context.Background(), s, plan, m, a, cpPath)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(root, "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Assert.
	if cp.NextCandidate != 1 || len(cp.Receipts) != 1 || !strings.Contains(string(after), "Use B.") || !strings.Contains(string(after), "status: draft") || strings.Contains(string(after), "verified:") {
		t.Fatalf("invalid published draft: %s / %#v", after, cp)
	}
	if _, err := Apply(context.Background(), s, plan, m, a, cpPath); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	tampered := a
	tampered.PromptVersion = "test/v2"
	if _, err := Apply(context.Background(), s, plan, m, tampered, cpPath); err == nil {
		t.Fatal("tampered analysis accepted")
	}
}

func TestAnalysisRejectsAmbiguousAndTruncatedInference(t *testing.T) {
	// Arrange.
	m, a := testManifest(t)
	a.Candidates[1].UnresolvedConflict = "A or B unresolved"
	a.Candidates[1].Supersedes = nil
	// Act.
	report, active, err := ValidateAnalysis(m, a)
	// Assert.
	if err != nil || len(report.UnresolvedConflicts) != 1 || len(active) != 0 {
		t.Fatalf("ambiguous outcome: report=%#v active=%#v err=%v", report, active, err)
	}
	m.Events[1].DiffTruncated = true
	m.Events[1].DiffBytes = 2
	m.Events[1].DiffSHA256 = digest([]byte("BC"))
	eb, _ := json.Marshal(m.Events)
	m.EventsSHA256 = digest(eb)
	a.EventsSHA256 = m.EventsSHA256
	a.Candidates[1].Evidence[0].DiffSHA256 = m.Events[1].DiffSHA256
	a.Candidates[1].EvidenceIncomplete = false
	if _, _, err := ValidateAnalysis(m, a); err == nil || !strings.Contains(err.Error(), "must acknowledge truncated evidence") {
		t.Fatalf("truncated evidence not rejected for missing acknowledgment: %v", err)
	}
}

func TestAnalysisRejectsCandidateWithoutConsideredEvent(t *testing.T) {
	// Arrange.
	m, a := testManifest(t)
	rejected := a
	rejected.Results = append([]EventResult(nil), a.Results...)
	rejected.Results[1] = EventResult{EventID: m.Events[1].ID, Decision: DecisionRejected, Reason: "no useful claim"}
	// Act and assert: rejected evidence cannot support a published candidate.
	if _, _, err := ValidateAnalysis(m, rejected); err == nil {
		t.Fatal("candidate cited rejected event")
	}
	orphan := a
	orphan.Candidates = append([]Candidate(nil), a.Candidates...)
	orphan.Candidates = append(orphan.Candidates, Candidate{ID: "orphan", ConceptID: "orphan", Title: "Orphan", Description: "Orphan", Body: "Unsupported.", Evidence: []EvidenceRef{{EventID: m.Events[0].ID, Path: "decision.go", DiffSHA256: m.Events[0].DiffSHA256}}})
	if _, _, err := ValidateAnalysis(m, orphan); err == nil {
		t.Fatal("orphan candidate accepted")
	}
}

type fixedAnalyzer struct{ analysis Analysis }

func (f fixedAnalyzer) Analyze(_ context.Context, _ AnalysisRequest) (Analysis, error) {
	return f.analysis, nil
}

func TestRunAnalyzerValidatesProviderProposal(t *testing.T) {
	// Arrange.
	m, a := testManifest(t)
	// Act.
	got, report, err := RunAnalyzer(context.Background(), fixedAnalyzer{a}, m)
	// Assert.
	if err != nil || got.PromptVersion != a.PromptVersion || report.ConsideredEvents != 2 {
		t.Fatalf("analysis=%#v report=%#v err=%v", got, report, err)
	}
	a.Results[0].Decision = DecisionRejected
	a.Results[0].Reason = "rejected"
	if _, _, err := RunAnalyzer(context.Background(), fixedAnalyzer{a}, m); err == nil {
		t.Fatal("invalid analyzer output accepted")
	}
}

func TestCancellationBeforeApplyKeepsBundleUnchanged(t *testing.T) {
	// Arrange.
	m, a := testManifest(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("---\nokf_version: \"0.2\"\n---\n\n# Empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := storefs.Open(root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	plan, err := Prepare(context.Background(), s, m, a)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Act.
	_, err = Apply(ctx, s, plan, m, a, filepath.Join(t.TempDir(), "checkpoint.json"))
	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "getting-started.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancel published concept: %v", err)
	}
}

func TestMultiConceptBatchPublishesIndexAndDocumentsTogether(t *testing.T) {
	// Arrange.
	m, a := testManifest(t)
	a.Candidates[1].ConceptID = "new-decision"
	a.Candidates[1].Supersedes = nil
	a.Candidates[1].History = nil
	root := t.TempDir()
	index := "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n\n- [Decision](getting-started.md) - Current decision.\n"
	concept := "---\ntype: Note\ntitle: Decision\ndescription: Current decision\nstatus: draft\n---\n\n# Decision\n\nOld placeholder.\n"
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "getting-started.md"), []byte(concept), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := storefs.Open(root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Act.
	plan, err := Prepare(context.Background(), s, m, a)
	if err != nil {
		t.Fatal(err)
	}
	beforeIndex, _ := os.ReadFile(filepath.Join(root, "index.md"))
	if string(beforeIndex) != index {
		t.Fatal("preview changed root index")
	}
	cp, err := Apply(context.Background(), s, plan, m, a, filepath.Join(t.TempDir(), "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	afterIndex, _ := os.ReadFile(filepath.Join(root, "index.md"))
	newDoc, err := os.ReadFile(filepath.Join(root, "new-decision.md"))
	// Assert.
	if err != nil || cp.NextCandidate != 1 || len(cp.Receipts) != 1 || !strings.Contains(string(afterIndex), "new-decision.md") || !strings.Contains(string(newDoc), "Use B.") {
		t.Fatalf("batch result index=%s doc=%s cp=%#v err=%v", afterIndex, newDoc, cp, err)
	}
}

func TestBatchStaleRevisionAndLostResponseReplay(t *testing.T) {
	// Arrange.
	m, a := testManifest(t)
	root := t.TempDir()
	index := "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n\n- [Decision](getting-started.md) - Current decision.\n"
	concept := "---\ntype: Note\ntitle: Decision\ndescription: Current decision\nstatus: draft\n---\n\n# Decision\n\nOld placeholder.\n"
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "getting-started.md"), []byte(concept), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := storefs.Open(root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	plan, err := Prepare(context.Background(), s, m, a)
	if err != nil {
		t.Fatal(err)
	}
	// Act: external edit invalidates the frozen base.
	if err := os.WriteFile(filepath.Join(root, "getting-started.md"), []byte(strings.Replace(concept, "Old placeholder.", "External edit.", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(context.Background(), s, plan, m, a, filepath.Join(t.TempDir(), "stale-checkpoint.json"))
	// Assert.
	var conflict *store.Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("stale revision error=%v", err)
	}
	current, _ := os.ReadFile(filepath.Join(root, "getting-started.md"))
	if !strings.Contains(string(current), "External edit.") {
		t.Fatal("stale apply overwrote external edit")
	}
	plan, err = Prepare(context.Background(), s, m, a)
	if err != nil {
		t.Fatal(err)
	}
	cpPath := filepath.Join(t.TempDir(), "resume-checkpoint.json")
	lost := &lostResponseWriter{Store: s}
	_, err = Apply(context.Background(), lost, plan, m, a, cpPath)
	if err == nil {
		t.Fatal("lost response was not simulated")
	}
	cp, err := Apply(context.Background(), s, plan, m, a, cpPath)
	if err != nil || cp.NextCandidate != 1 || len(cp.Receipts) != 1 {
		t.Fatalf("replay cp=%#v err=%v", cp, err)
	}
	if err := os.Remove(cpPath); err != nil {
		t.Fatal(err)
	}
	recovered, err := Apply(context.Background(), s, plan, m, a, cpPath)
	if err != nil || recovered.NextCandidate != 1 || len(recovered.Receipts) != 1 {
		t.Fatalf("missing-checkpoint recovery=%#v err=%v", recovered, err)
	}
}

func TestBatchJournalRecoveryShowsCompletePostState(t *testing.T) {
	// Arrange.
	m, a := testManifest(t)
	a.Candidates[1].ConceptID = "new-decision"
	a.Candidates[1].Supersedes = nil
	a.Candidates[1].History = nil
	root := t.TempDir()
	index := "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n\n- [Decision](getting-started.md) - Current decision.\n"
	concept := "---\ntype: Note\ntitle: Decision\ndescription: Current decision\nstatus: draft\n---\n\n# Decision\n\nOld placeholder.\n"
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "getting-started.md"), []byte(concept), 0o644); err != nil {
		t.Fatal(err)
	}
	journalRenamed := false
	crashed := false
	s, err := storefs.Open(root, storefs.Config{PostFault: func(step storefs.Step) error {
		if step == storefs.StepJournalRename {
			journalRenamed = true
		}
		if step == storefs.StepJournalDirectorySync && journalRenamed && !crashed {
			crashed = true
			return errors.New("simulated post-durable crash")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(context.Background(), s, m, a)
	if err != nil {
		t.Fatal(err)
	}
	// Act.
	_, applyErr := Apply(context.Background(), s, plan, m, a, filepath.Join(t.TempDir(), "checkpoint.json"))
	if !crashed || applyErr == nil {
		t.Fatalf("fault not exercised: crashed=%t err=%v", crashed, applyErr)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := storefs.Open(root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	snap, err := recovered.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	indexBytes, err := snap.ReadFile(context.Background(), "index.md")
	if err != nil {
		t.Fatal(err)
	}
	newDoc, err := snap.ReadFile(context.Background(), "new-decision.md")
	if err != nil {
		t.Fatal(err)
	}
	oldDoc, err := snap.ReadFile(context.Background(), "getting-started.md")
	if err != nil {
		t.Fatal(err)
	}
	// Assert.
	if !strings.Contains(string(indexBytes), "new-decision.md") || !strings.Contains(string(newDoc), "Use B.") || !strings.Contains(string(oldDoc), "Use A.") {
		t.Fatalf("recovery exposed incomplete batch: index=%s new=%s old=%s", indexBytes, newDoc, oldDoc)
	}
}

func TestCheckpointCannotClaimPublicationInAnotherStore(t *testing.T) {
	// Arrange.
	m, a := testManifest(t)
	base := t.TempDir()
	index := "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n\n- [Decision](getting-started.md) - Current decision.\n"
	concept := "---\ntype: Note\ntitle: Decision\ndescription: Current decision\nstatus: draft\n---\n\n# Decision\n\nOld placeholder.\n"
	for _, name := range []string{"one", "two"} {
		dir := filepath.Join(base, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(index), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "getting-started.md"), []byte(concept), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	one, err := storefs.Open(filepath.Join(base, "one"), storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := storefs.Open(filepath.Join(base, "two"), storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	plan, err := Prepare(context.Background(), one, m, a)
	if err != nil {
		t.Fatal(err)
	}
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	if _, err := Apply(context.Background(), one, plan, m, a, cpPath); err != nil {
		t.Fatal(err)
	}
	// Act.
	_, err = Apply(context.Background(), two, plan, m, a, cpPath)
	// Assert.
	if err == nil {
		t.Fatal("copied checkpoint accepted")
	}
	raw, _ := os.ReadFile(filepath.Join(base, "two", "getting-started.md"))
	if string(raw) != concept {
		t.Fatalf("copied checkpoint modified target bundle: %s", raw)
	}
	tampered := plan
	tampered.Coverage.ConsideredEvents = 0
	if _, err := Apply(context.Background(), one, tampered, m, a, filepath.Join(t.TempDir(), "tampered.json")); err == nil {
		t.Fatal("tampered coverage accepted")
	}
}

func TestFrozenFixtureReversalAndIncompleteEvidence(t *testing.T) {
	// Arrange.
	var m Manifest
	var a Analysis
	manifestRaw, err := os.ReadFile(filepath.Join("testdata", "reversal-events.json"))
	if err != nil {
		t.Fatal(err)
	}
	analysisRaw, err := os.ReadFile(filepath.Join("testdata", "reversal-analysis.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifestRaw, &m); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(analysisRaw, &a); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("---\nokf_version: \"0.2\"\n---\n\n# Concepts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := storefs.Open(root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Act.
	plan, err := Prepare(context.Background(), s, m, a)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := Apply(context.Background(), s, plan, m, a, filepath.Join(t.TempDir(), "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	decision, _ := os.ReadFile(filepath.Join(root, "decision.md"))
	handler, _ := os.ReadFile(filepath.Join(root, "handler-status.md"))
	index, _ := os.ReadFile(filepath.Join(root, "index.md"))
	// Assert.
	if cp.NextCandidate != 1 || len(plan.Concepts) != 2 || !strings.Contains(string(decision), "mode B") || strings.Contains(string(decision), "uses mode A") || !strings.Contains(string(handler), "unverified") || !strings.Contains(string(handler), "Evidence is incomplete") || !strings.Contains(string(index), "handler-status.md") {
		t.Fatalf("fixture publication decision=%s handler=%s index=%s", decision, handler, index)
	}
}

func TestNestedBackfillIndexesPassOrphanPolicy(t *testing.T) {
	// Arrange.
	m, a := testManifest(t)
	for i := range a.Candidates {
		a.Candidates[i].ConceptID = "architecture/decisions/mode"
	}
	root := t.TempDir()
	index := "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n"
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := storefs.Open(root, storefs.Config{ValidatorConfig: &validator.ValidatorConfig{TemporalProfile: bundle.TemporalProfileInstant}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Act.
	plan, err := Prepare(context.Background(), s, m, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Indexes) != 2 {
		t.Fatalf("indexes=%#v", plan.Indexes)
	}
	if _, err := os.Stat(filepath.Join(root, "architecture", "index.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview wrote index: %v", err)
	}
	if _, err := Apply(context.Background(), s, plan, m, a, filepath.Join(t.TempDir(), "checkpoint.json")); err != nil {
		t.Fatal(err)
	}
	report := validator.ValidatePath(root, &validator.ValidatorConfig{Strict: true, CheckLinks: true, CheckOrphans: true, TemporalProfile: bundle.TemporalProfileInstant})
	// Assert.
	if report.Count(validator.SeverityError) != 0 || report.Count(validator.SeverityWarning) != 0 {
		t.Fatalf("nested bundle diagnostics=%#v", report.Diagnostics)
	}
	for _, path := range []string{"architecture/index.md", "architecture/decisions/index.md", "architecture/decisions/mode.md"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
}

type lostResponseWriter struct {
	*storefs.Store
	lost bool
}

func (w *lostResponseWriter) ApplyBackfillBatch(ctx context.Context, req storefs.BackfillBatchRequest, o store.CommitOptions) (store.CommitReceipt, error) {
	r, err := w.Store.ApplyBackfillBatch(ctx, req, o)
	if err == nil && !w.lost {
		w.lost = true
		return store.CommitReceipt{}, errors.New("response lost after durable commit")
	}
	return r, err
}
