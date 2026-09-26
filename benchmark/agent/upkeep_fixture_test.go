package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/internal/okfcli"
	"github.com/skosovsky/okf/upkeep"
)

func TestBuildUpkeepStudyFromCommittedBackfill(t *testing.T) {
	// Arrange
	f, err := os.Open("corpus/backfill_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, hash, err := LoadCorpus(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	build := func() UpkeepStudy {
		p, err := BuildUpkeepStudyFromBackfill(cases, hash, "upkeep-fixture-v1", 1, "writer-v1", "consumer-v1", strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Act
	first, second := build(), build()
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	// Assert
	if !bytes.Equal(a, b) || first.SourceCorpusSHA256 != hash || len(first.Cases) != 2 {
		t.Fatalf("non-deterministic or incomplete plan: %s", a)
	}
	frozen, err := os.ReadFile("corpus/upkeep_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var frozenCases []UpkeepCase
	if err := json.Unmarshal(frozen, &frozenCases); err != nil {
		t.Fatal(err)
	}
	frozenBytes, _ := json.Marshal(frozenCases)
	generatedBytes, _ := json.Marshal(first.Cases)
	if !bytes.Equal(frozenBytes, generatedBytes) {
		t.Fatal("committed upkeep cases drifted from deterministic generator")
	}
	for _, c := range first.Cases {
		if c.Expected.Answer == "" || len(c.InitialCode) == 0 || len(c.TargetCode) == 0 {
			t.Fatalf("incomplete case %+v", c)
		}
		writerRequest, _ := json.Marshal(c.WriterRequest())
		consumerRequest, _ := json.Marshal(c.ConsumerRequest(c.InitialBundle))
		if bytes.Contains(writerRequest, []byte(`"expected"`)) || bytes.Contains(writerRequest, []byte(`"consumer_question"`)) || bytes.Contains(consumerRequest, []byte(`"expected"`)) || bytes.Contains(consumerRequest, []byte(`"target_code"`)) || bytes.Contains(consumerRequest, []byte(`"task"`)) {
			t.Fatalf("role projection leaked plan fields for %s", c.ID)
		}
		root := t.TempDir()
		for _, artifact := range c.InitialBundle {
			if err := os.WriteFile(filepath.Join(root, artifact.Path), []byte(artifact.Content), 0600); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		if code := okfcli.Run([]string{"validate", "--path", root, "--format", "json"}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s: invalid initial OKF bundle, code=%d stdout=%s stderr=%s", c.ID, code, stdout.String(), stderr.String())
		}
		var validation struct {
			Errors   int `json:"errors"`
			Warnings int `json:"warnings"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &validation); err != nil || validation.Errors != 0 || validation.Warnings != 0 {
			t.Fatalf("%s: initial bundle diagnostics=%+v err=%v", c.ID, validation, err)
		}
	}
}

func TestUpkeepFixtureCheckerDetectsBothRelevantPaths(t *testing.T) {
	// Arrange
	f, err := os.Open("corpus/backfill_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	source, hash, err := LoadCorpus(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildUpkeepStudyFromBackfill(source, hash, "upkeep-fixture-v1", 1, "writer-v1", "consumer-v1", strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range plan.Cases {
		t.Run(c.ID, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "knowledge"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, a := range c.InitialBundle {
				if err := os.WriteFile(filepath.Join(root, "knowledge", a.Path), []byte(a.Content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, a := range c.InitialCode {
				if err := os.WriteFile(filepath.Join(root, a.Path), []byte(a.Content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=OKF Test", "-c", "user.email=okf@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "baseline"}} {
				cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %s: %v", args, out, err)
				}
			}
			cfg := upkeep.Config{RepoRoot: root, BundleRoot: "knowledge", RelevantPaths: []string{"decision.go", "handler-capture.txt"}, Mode: "advisory", TimeoutMS: 5000, MaxSessionMinutes: 60}
			base, err := upkeep.Snapshot(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range c.TargetCode {
				if err := os.WriteFile(filepath.Join(root, a.Path), []byte(a.Content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			current, err := upkeep.Snapshot(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			// Act
			result, err := upkeep.Check(cfg, base, current, nil)
			// Assert
			if err != nil || result.Status != "needs_review" || !result.Advisory || len(result.Changes) != 1 || result.Changes[0].Path != c.TargetCode[0].Path {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}
