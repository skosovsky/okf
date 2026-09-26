package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/benchmark/agent"
)

func TestModelVisiblePromptOmitsHarnessLabels(t *testing.T) {
	// Arrange: case ID and arm are deliberately revealing, but remain wire-only.
	req := agent.Request{
		CaseID:       "reversal-treatment-case",
		Arm:          agent.Treatment,
		Question:     "What is the current mode?",
		Artifacts:    []agent.Artifact{{ID: "decision", Path: "decision.md", Content: "Current mode: B."}},
		Instructions: "Answer from evidence.",
	}

	// Act.
	prompt, err := modelVisiblePrompt(req)
	if err != nil {
		t.Fatal(err)
	}
	var visible map[string]json.RawMessage
	if err := json.Unmarshal(prompt, &visible); err != nil {
		t.Fatal(err)
	}

	// Assert: the source evidence and answer instruction survive projection.
	if len(visible) != 3 || visible["question"] == nil || visible["artifacts"] == nil || visible["instructions"] == nil {
		t.Fatalf("unexpected model-visible fields: %s", prompt)
	}
	if strings.Contains(string(prompt), req.CaseID) || strings.Contains(string(prompt), `"arm"`) || strings.Contains(string(prompt), req.Arm) {
		t.Fatalf("harness labels leaked to model: %s", prompt)
	}
	var artifacts []agent.Artifact
	if err := json.Unmarshal(visible["artifacts"], &artifacts); err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0] != req.Artifacts[0] {
		t.Fatalf("evidence changed: %+v", artifacts)
	}
}

func TestVerifyRuntime(t *testing.T) {
	// Arrange.
	path := filepath.Join(t.TempDir(), "codex")
	content := []byte("#!/bin/sh\nprintf 'codex-cli 0.155.0-alpha.16.3\\n'\n")
	if err := os.WriteFile(path, content, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	// Act and assert.
	if err := verifyRuntime(path, "codex-cli 0.155.0-alpha.16.3", hash); err != nil {
		t.Fatal(err)
	}
	if err := verifyRuntime(path, "codex-cli 0.137.0", hash); err == nil {
		t.Fatal("accepted wrong version")
	}
	if err := verifyRuntime(path, "codex-cli 0.155.0-alpha.16.3", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("accepted wrong SHA-256")
	}
	if err := verifyRuntime("codex", "codex-cli 0.155.0-alpha.16.3", hash); err == nil {
		t.Fatal("accepted implicit PATH selection")
	}
}

func TestSuccessfulGoCLICommand(t *testing.T) {
	// Arrange: only a completed command event with a successful direct binary
	// invocation can prove toolkit use. Textual mentions and shell compounds cannot.
	const binary = "/private/tmp/okf-eval-cli"
	conceptPaths := map[string]bool{"current.md": true}
	tests := []struct {
		name string
		item string
		want bool
	}{
		{"validate", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate --path . --json --temporal-profile instant-0b87c52","exit_code":0}`, true},
		{"parse", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli parse current.md --json","exit_code":0}`, true},
		{"quoted shell wrapper", `{"type":"command_execution","command":"bash -lc '/private/tmp/okf-eval-cli parse current.md --json'","exit_code":0}`, true},
		{"zsh validate", `{"type":"command_execution","command":"/bin/zsh -lc '/private/tmp/okf-eval-cli validate --path . --json --temporal-profile instant-0b87c52'","exit_code":0}`, true},
		{"zsh parse", `{"type":"command_execution","command":"/bin/zsh -lc '/private/tmp/okf-eval-cli parse current.md --json'","exit_code":0}`, true},
		{"zsh help", `{"type":"command_execution","command":"/bin/zsh -lc '/private/tmp/okf-eval-cli validate --help'","exit_code":0}`, false},
		{"zsh foreign path", `{"type":"command_execution","command":"/bin/zsh -lc '/private/tmp/okf-eval-cli parse other.md --json'","exit_code":0}`, false},
		{"zsh pipeline", `{"type":"command_execution","command":"/bin/zsh -lc '/private/tmp/okf-eval-cli parse current.md --json | head'","exit_code":0}`, false},
		{"zsh comment", `{"type":"command_execution","command":"/bin/zsh -lc '/private/tmp/okf-eval-cli parse current.md --json # pretend'","exit_code":0}`, false},
		{"zsh failed", `{"type":"command_execution","command":"/bin/zsh -lc '/private/tmp/okf-eval-cli parse current.md --json'","exit_code":1}`, false},
		{"validate help", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate --help","exit_code":0}`, false},
		{"parse help", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli parse --help","exit_code":0}`, false},
		{"validate wrong path", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate --path /tmp/empty --json --temporal-profile instant-0b87c52","exit_code":0}`, false},
		{"parse unrelated file", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli parse other.md --json","exit_code":0}`, false},
		{"echo path", `{"type":"command_execution","command":"echo /private/tmp/okf-eval-cli validate","exit_code":0}`, false},
		{"echo words", `{"type":"command_execution","command":"echo okf validate","exit_code":0}`, false},
		{"comment", `{"type":"command_execution","command":"# /private/tmp/okf-eval-cli validate","exit_code":0}`, false},
		{"compound", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate --path .; echo done","exit_code":0}`, false},
		{"failed", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate","exit_code":1}`, false},
		{"missing exit", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate"}`, false},
		{"wrong type", `{"type":"mcp_tool_call","command":"/private/tmp/okf-eval-cli validate","exit_code":0}`, false},
		{"other subcommand", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli help","exit_code":0}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			got := successfulGoCLICommand(json.RawMessage(tt.item), binary, conceptPaths)

			// Assert.
			if got != tt.want {
				t.Fatalf("successfulGoCLICommand() = %t, want %t", got, tt.want)
			}
		})
	}
}
