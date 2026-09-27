package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/skosovsky/okf/benchmark/agent"
)

func TestPinnedModelRuntime(t *testing.T) {
	// Arrange.
	path := filepath.Join(t.TempDir(), "codex")
	content := []byte("#!/bin/sh\nprintf 'codex-cli 0.155.0-alpha.16.3\\n'\n")
	if err := os.WriteFile(path, content, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	// Act and assert.
	if got, err := pinnedModelRuntime(path, "codex-cli 0.155.0-alpha.16.3", hash); err != nil || got != hash {
		t.Fatalf("valid runtime: hash %q, error %v", got, err)
	}
	if _, err := pinnedModelRuntime(path, "codex-cli 0.137.0", hash); err == nil {
		t.Fatal("accepted wrong version")
	}
	if _, err := pinnedModelRuntime(path, "codex-cli 0.155.0-alpha.16.3", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("accepted wrong SHA-256")
	}
	if _, err := pinnedModelRuntime("codex", "codex-cli 0.155.0-alpha.16.3", hash); err == nil {
		t.Fatal("accepted implicit PATH selection")
	}
}

func TestRuntimePinAcrossToolkitModes(t *testing.T) {
	// Arrange: the same Codex executable must be pinnable for metadata-only
	// and precomputed toolkit modes, not just direct mode.
	path := filepath.Join(t.TempDir(), "codex")
	content := []byte("#!/bin/sh\nprintf 'codex-cli test\\n'\n")
	if err := os.WriteFile(path, content, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	for _, mode := range []string{"none", "treatment", "both", "direct"} {
		t.Run(mode, func(t *testing.T) {
			// Act.
			got, err := runtimePinForMode(mode, path, "codex-cli test", hash)

			// Assert.
			if err != nil || got != hash {
				t.Fatalf("runtime pin in %s: hash %q, error %v", mode, got, err)
			}
		})
	}
	if _, err := runtimePinForMode("none", path, "", hash); err == nil {
		t.Fatal("accepted incomplete non-direct Codex pin")
	}
	if _, err := runtimePinForMode("direct", "", "", ""); err == nil {
		t.Fatal("accepted unpinned direct runtime")
	}
}

func TestNonDirectCallPassesRuntimePin(t *testing.T) {
	// Arrange: a local fake adapter checks the runner's environment contract.
	adapter := filepath.Join(t.TempDir(), "adapter")
	script := "#!/bin/sh\n" +
		"test \"$OKF_EVAL_TOOLKIT_MODE\" = none || exit 2\n" +
		"test \"$OKF_EVAL_MODEL_RUNTIME\" = /absolute/codex || exit 3\n" +
		"test \"$OKF_EVAL_MODEL_RUNTIME_VERSION\" = 'codex-cli test' || exit 4\n" +
		"test \"$OKF_EVAL_MODEL_RUNTIME_SHA256\" = abc || exit 5\n" +
		"printf '{\"tool_calls\":0,\"toolkit_calls\":0,\"input_tokens\":1,\"output_tokens\":1,\"cache_tokens\":0,\"elapsed_ms\":1}\\n'\n"
	if err := os.WriteFile(adapter, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	// Act.
	obs, err := call(context.Background(), adapter, "model", "model", `{}`, "none", "", "/absolute/codex", "codex-cli test", "abc", false, agent.Request{CaseID: "case", Arm: agent.Control, Question: "q"})

	// Assert.
	if err != nil || obs.InputTokens != 1 || obs.OutputTokens != 1 {
		t.Fatalf("non-direct adapter call: observation %+v, error %v", obs, err)
	}
}

func TestDiagnosticRequiresAllArtifactReads(t *testing.T) {
	// Arrange.
	arts := []agent.Artifact{{ID: "historical"}, {ID: "current"}}

	// Act and assert.
	if allArtifactsRead(arts, []string{"current"}) {
		t.Fatal("accepted partial control read")
	}
	if !allArtifactsRead(arts, []string{"historical", "current"}) {
		t.Fatal("rejected complete control read")
	}
}

func TestValidDiagnosticV2BudgetAndID(t *testing.T) {
	// Arrange.
	data, err := os.ReadFile("../../corpus/diagnostic_primary.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, hash, err := agent.LoadCorpus(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	check := func(id string, tokens int) bool {
		return validDiagnosticV2(id, hash, cases, 1, 2, 120, tokens, true, 0, "direct", false, "Codex read-only ephemeral temp with shell; Go CLI binary supplied")
	}

	// Act and assert.
	if !check(agent.DiagnosticV2RunID, agent.DiagnosticV2MaxTokens) {
		t.Fatal("rejected exact v2 diagnostic bounds")
	}
	if check(agent.DiagnosticV2RunID, agent.DiagnosticV1MaxTokens) {
		t.Fatal("accepted old 60k budget for new v2 run")
	}
	if check(agent.DiagnosticV1RunID, agent.DiagnosticV2MaxTokens) {
		t.Fatal("accepted old run ID for new v2 run")
	}
}
