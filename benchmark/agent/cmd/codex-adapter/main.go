// codex-adapter is an optional bounded model adapter. It runs each benchmark
// trial in a fresh, read-only Codex session outside the repository.
package main

import (
	"bufio"
	"bytes"
	"context"
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
	if model == "" || modelVersion == "" || settingsRaw == "" {
		return fmt.Errorf("model, model version and settings required")
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
	goCLI := os.Getenv("OKF_EVAL_CLI_BINARY")
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
		toolInstruction := "Read the listed files with read-only shell commands before answering."
		if req.Arm == agent.Treatment {
			toolInstruction = "Use the Go OKF CLI at " + goCLI + " to run `validate --path . --json --temporal-profile instant-0b87c52` and `parse <concept-file> --json` on concept files, then read the concept bodies. Do not answer before invoking the CLI."
		}
		prompt = []byte(req.Instructions + "\n" + toolInstruction + "\nQuestion: " + req.Question + "\nEvidence files (artifact ID=path):\n" + strings.Join(paths, "\n"))
	} else {
		prompt, err = json.Marshal(req)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "exec", "--json", "--ephemeral", "--ignore-user-config", "--skip-git-repo-check", "-s", "read-only", "-C", working, "-m", model, "-c", "model_reasoning_effort=\""+settings.ReasoningEffort+"\"", "--output-schema", schemaPath, "-o", lastPath, "-")
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
				trace := string(ev.Item)
				if len(trace) > 1000 {
					trace = trace[:1000]
				}
				obs.ToolTrace = append(obs.ToolTrace, trace)
			}
		}
		if ev.Type == "item.completed" && mode == "direct" && req.Arm == agent.Treatment && successfulGoCLICommand(ev.Item, goCLI, conceptPaths) {
			obs.ToolkitCalls++
			trace := string(ev.Item)
			if len(trace) > 1000 {
				trace = trace[:1000]
			}
			obs.ToolTrace = append(obs.ToolTrace, trace)
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	if runErr != nil {
		obs.AdapterError = fmt.Sprintf("codex exec: %v: %.600s", runErr, stderr.String())
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
	// This deliberately recognizes only simple commands. If Codex changes its
	// event format or uses shell compound commands, the trial fails closed.
	if strings.ContainsAny(item.Command, "\n\r;&|><`$()\\\"'") {
		return false
	}
	argv := strings.Fields(item.Command)
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
