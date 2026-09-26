// codex-adapter is an optional bounded model adapter. It runs each benchmark
// trial in a fresh, read-only Codex session outside the repository.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/skosovsky/okf/benchmark/agent"
)

const responseSchema = `{"type":"object","additionalProperties":false,"required":["answer","evidence","refused"],"properties":{"answer":{"type":"string"},"evidence":{"type":"array","items":{"type":"string"}},"refused":{"type":"boolean"}}}`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	model := os.Getenv("OKF_EVAL_MODEL")
	modelVersion := os.Getenv("OKF_EVAL_MODEL_VERSION")
	settingsRaw := os.Getenv("OKF_EVAL_SETTINGS")
	modelRuntime := os.Getenv("OKF_EVAL_MODEL_RUNTIME")
	runtimeVersion := os.Getenv("OKF_EVAL_MODEL_RUNTIME_VERSION")
	runtimeSHA256 := os.Getenv("OKF_EVAL_MODEL_RUNTIME_SHA256")
	if model == "" || modelVersion == "" || settingsRaw == "" {
		return fmt.Errorf("model, model version and settings required")
	}
	if err := verifyRuntime(modelRuntime, runtimeVersion, runtimeSHA256); err != nil {
		return err
	}
	var settings struct {
		ReasoningEffort string `json:"reasoning_effort"`
	}
	settingsDecoder := json.NewDecoder(bytes.NewReader([]byte(settingsRaw)))
	settingsDecoder.DisallowUnknownFields()
	if err := settingsDecoder.Decode(&settings); err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	if settings.ReasoningEffort != "low" && settings.ReasoningEffort != "medium" && settings.ReasoningEffort != "high" {
		return fmt.Errorf("unsupported reasoning_effort")
	}
	if modelVersion != model {
		return fmt.Errorf("model-version must equal pinned model ID; this adapter cannot verify a backend snapshot")
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20+1))
	if err != nil {
		return err
	}
	if len(input) > 1<<20 {
		return fmt.Errorf("request exceeds 1 MiB")
	}
	var req agent.Request
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return err
	}
	if req.CaseID == "" || req.Question == "" || len(req.Artifacts) == 0 {
		return fmt.Errorf("incomplete request")
	}
	working, err := os.MkdirTemp("", "okf-agent-codex-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(working)
	schemaPath := filepath.Join(working, "response-schema.json")
	if err := os.WriteFile(schemaPath, []byte(responseSchema), 0600); err != nil {
		return err
	}
	lastPath := filepath.Join(working, "last-message.json")
	mode := os.Getenv("OKF_EVAL_TOOLKIT_MODE")
	diagnosticRead := os.Getenv("OKF_EVAL_DIAGNOSTIC_READ") == "true"
	goCLI := os.Getenv("OKF_EVAL_CLI_BINARY")
	if diagnosticRead {
		controlInstruction := directToolInstruction(agent.Control, goCLI, true)
		treatmentInstruction := directToolInstruction(agent.Treatment, goCLI, true)
		if mode != "direct" || goCLI == "" || !strings.HasPrefix(treatmentInstruction, controlInstruction) {
			return fmt.Errorf("diagnostic prompt/tool parity preflight failed")
		}
	}
	conceptPaths := make(map[string]bool)
	for _, artifact := range req.Artifacts {
		if artifact.Path != "index.md" && strings.HasSuffix(artifact.Path, ".md") && strings.HasPrefix(artifact.Content, "---\n") {
			conceptPaths[artifact.Path] = true
		}
	}
	var prompt []byte
	if mode == "direct" {
		if req.Arm == agent.Treatment && goCLI == "" {
			return fmt.Errorf("direct treatment requires Go CLI binary")
		}
		var paths []string
		for _, artifact := range req.Artifacts {
			clean := filepath.Clean(artifact.Path)
			if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("unsafe artifact path")
			}
			path := filepath.Join(working, clean)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(artifact.Content), 0600); err != nil {
				return err
			}
			paths = append(paths, artifact.ID+"="+clean)
		}
		toolInstruction := directToolInstruction(req.Arm, goCLI, diagnosticRead)
		prompt = []byte(req.Instructions + "\n" + toolInstruction + "\nQuestion: " + req.Question + "\nEvidence files (artifact ID=path):\n" + strings.Join(paths, "\n"))
	} else {
		prompt, err = json.Marshal(req)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, modelRuntime, "exec", "--json", "--ephemeral", "--ignore-user-config", "--skip-git-repo-check", "-s", "read-only", "-C", working, "-m", model, "-c", "model_reasoning_effort=\""+settings.ReasoningEffort+"\"", "--output-schema", schemaPath, "-o", lastPath, "-")
	cmd.Stdin = bytes.NewReader(prompt)
	var stdout, stderr cappedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	runErr := cmd.Run()
	obs := agent.Observation{ElapsedMS: time.Since(start).Milliseconds()}
	s := bufio.NewScanner(bytes.NewReader(stdout.data))
	s.Buffer(make([]byte, 64<<10), 2<<20)
	for s.Scan() {
		var ev struct {
			Type  string          `json:"type"`
			Item  json.RawMessage `json:"item"`
			Usage struct {
				InputTokens       int `json:"input_tokens"`
				OutputTokens      int `json:"output_tokens"`
				CachedInputTokens int `json:"cached_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(s.Bytes(), &ev) != nil {
			if diagnosticRead {
				if len(obs.EventTrace) < 64 {
					obs.EventTrace = append(obs.EventTrace, agent.EventMetadata{Event: "invalid_json"})
				} else {
					obs.EventTraceTruncated = true
				}
			}
			continue
		}
		if ev.Type == "turn.completed" {
			obs.InputTokens += ev.Usage.InputTokens
			obs.OutputTokens += ev.Usage.OutputTokens
			obs.CacheTokens += ev.Usage.CachedInputTokens
		}
		if ev.Type == "item.started" {
			var item struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(ev.Item, &item) == nil && (item.Type == "command_execution" || item.Type == "mcp_tool_call") {
				obs.ToolCalls++
				if !diagnosticRead {
					trace := string(ev.Item)
					if len(trace) > 1000 {
						trace = trace[:1000]
					}
					obs.ToolTrace = append(obs.ToolTrace, trace)
				}
			}
		}
		if diagnosticRead {
			if len(obs.EventTrace) < 64 {
				obs.EventTrace = append(obs.EventTrace, eventMetadata(ev.Type, ev.Item, req.Artifacts, goCLI, conceptPaths))
			} else {
				obs.EventTraceTruncated = true
			}
			if ev.Type == "item.completed" {
				for _, id := range confirmedReadArtifacts(ev.Item, req.Artifacts) {
					if !containsString(obs.ReadArtifacts, id) {
						obs.ReadArtifacts = append(obs.ReadArtifacts, id)
					}
				}
			}
		}
		if ev.Type == "item.completed" && mode == "direct" && req.Arm == agent.Treatment && successfulGoCLICommand(ev.Item, goCLI, conceptPaths) {
			obs.ToolkitCalls++
			if !diagnosticRead {
				trace := string(ev.Item)
				if len(trace) > 1000 {
					trace = trace[:1000]
				}
				obs.ToolTrace = append(obs.ToolTrace, trace)
			}
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	if runErr != nil {
		if diagnosticRead {
			hash := sha256.Sum256(stderr.data)
			obs.AdapterError = fmt.Sprintf("codex exec: %v; stderr_sha256=%x; stderr_bytes=%d", runErr, hash, len(stderr.data))
		} else {
			obs.AdapterError = fmt.Sprintf("codex exec: %v: %.600s", runErr, stderr.String())
		}
		return json.NewEncoder(os.Stdout).Encode(obs)
	}
	if ctx.Err() != nil {
		obs.AdapterError = ctx.Err().Error()
		return json.NewEncoder(os.Stdout).Encode(obs)
	}
	if stdout.truncated {
		obs.AdapterError = "codex event stream exceeds 2 MiB"
		return json.NewEncoder(os.Stdout).Encode(obs)
	}
	last, err := os.ReadFile(lastPath)
	if err != nil {
		obs.AdapterError = fmt.Sprintf("last message: %v", err)
		return json.NewEncoder(os.Stdout).Encode(obs)
	}
	var answer struct {
		Answer   string   `json:"answer"`
		Evidence []string `json:"evidence"`
		Refused  bool     `json:"refused"`
	}
	answerDecoder := json.NewDecoder(bytes.NewReader(last))
	answerDecoder.DisallowUnknownFields()
	if err := answerDecoder.Decode(&answer); err != nil {
		obs.AdapterError = fmt.Sprintf("last message: %v", err)
		return json.NewEncoder(os.Stdout).Encode(obs)
	}
	obs.Answer = answer.Answer
	obs.Evidence = answer.Evidence
	obs.Refused = answer.Refused
	if obs.InputTokens+obs.OutputTokens == 0 {
		obs.AdapterError = "Codex event stream omitted usage"
	}
	return json.NewEncoder(os.Stdout).Encode(obs)
}

func verifyRuntime(path, version, expectedSHA256 string) error {
	if !filepath.IsAbs(path) || version == "" || len(expectedSHA256) != 64 {
		return fmt.Errorf("absolute Codex runtime path, version and SHA-256 are required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("Codex runtime is not an executable regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != expectedSHA256 {
		return fmt.Errorf("Codex runtime SHA-256 mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return fmt.Errorf("Codex runtime version probe: %w", err)
	}
	if strings.TrimSpace(string(out)) != version {
		return fmt.Errorf("Codex runtime version mismatch")
	}
	return nil
}

// successfulGoCLICommand only accepts a completed, successful, direct invocation
// of the supplied binary. A mention of the binary in a shell command, comment,
// pipeline or failed command is not evidence of toolkit use.
func successfulGoCLICommand(raw json.RawMessage, goCLI string, conceptPaths map[string]bool) bool {
	if goCLI == "" {
		return false
	}
	var item struct {
		Type     string `json:"type"`
		Command  string `json:"command"`
		ExitCode *int   `json:"exit_code"`
	}
	if json.Unmarshal(raw, &item) != nil || item.Type != "command_execution" || item.ExitCode == nil || *item.ExitCode != 0 {
		return false
	}
	// Accept only a simple direct command or a safely quoted shell wrapper.
	// Compound commands and unknown event formats fail closed.
	argv := simpleCommandArgv(item.Command)
	if len(argv) < 2 || argv[0] != goCLI {
		return false
	}
	switch argv[1] {
	case "validate":
		// Exactly the benchmark's JSON validation of the materialized bundle.
		return len(argv) == 7 && argv[2] == "--path" && argv[3] == "." && argv[4] == "--json" && argv[5] == "--temporal-profile" && argv[6] == "instant-0b87c52"
	case "parse":
		// The parsed file must be an actual concept supplied in this trial.
		return len(argv) == 4 && conceptPaths[argv[2]] && argv[3] == "--json"
	default:
		return false
	}
}

type cappedBuffer struct {
	data      []byte
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	available := 2<<20 - len(b.data)
	if available < 0 {
		available = 0
	}
	if len(p) > available {
		b.truncated = true
		p = p[:available]
	}
	b.data = append(b.data, p...)
	return n, nil
}
func (b *cappedBuffer) String() string { return string(b.data) }
