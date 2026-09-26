package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/skosovsky/okf/benchmark/agent"
	"github.com/skosovsky/okf/benchmark/agent/beforeafter"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: okf-before-after run|analyze [flags]")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	root := f.String("repo-root", ".", "clean corrected checkout root")
	corpus := f.String("corpus", "benchmark/agent/beforeafter/corpus.json", "frozen question corpus")
	planPath := f.String("plan", "", "output/input plan JSON")
	rowsPath := f.String("rows", "", "output/input rows JSONL")
	oldBin := f.String("old-bin", "", "absolute historical Go CLI binary")
	newBin := f.String("new-bin", "", "absolute corrected Go CLI binary")
	adapter := f.String("adapter", "", "absolute model adapter binary")
	runtime := f.String("model-runtime", "", "absolute pinned Codex runtime")
	runtimeVersion := f.String("model-runtime-version", "", "pinned runtime --version output")
	runtimeSHA := f.String("model-runtime-sha256", "", "preregistered model runtime SHA-256")
	oldSHA := f.String("old-bin-sha256", "", "preregistered historical Go CLI SHA-256")
	newSHA := f.String("new-bin-sha256", "", "preregistered corrected Go CLI SHA-256")
	adapterSHA := f.String("adapter-sha256", "", "preregistered model adapter SHA-256")
	newCommit := f.String("new-commit", "", "full corrected commit SHA")
	runID := f.String("run-id", "", "unique preregistered run ID")
	model := f.String("model", "", "model ID")
	modelVersion := f.String("model-version", "", "model version/snapshot")
	settings := f.String("settings", "", "model settings JSON")
	repeats := f.Int("repeats", 2, "repetitions per arm/case")
	maxCalls := f.Int("max-model-calls", 18, "hard model-call cap")
	maxSeconds := f.Int("max-seconds", 900, "hard total wall-clock cap")
	maxTokens := f.Int("max-tokens", 150000, "observed input+output token stop")
	maxCost := f.Float64("max-cost-usd", 0, "observed USD stop, if pricing known")
	unpriced := f.Bool("unpriced", false, "explicitly acknowledge unavailable per-call pricing")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if *planPath == "" || *rowsPath == "" {
		return errors.New("-plan and -rows required")
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	if args[0] == "analyze" {
		cases, corpusSHA, fixtureSHA, err := beforeafter.LoadCorpus(filepath.Join(absRoot, *corpus), absRoot)
		if err != nil {
			return err
		}
		var plan beforeafter.Plan
		raw, err := os.ReadFile(*planPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &plan); err != nil {
			return err
		}
		if plan.CorpusSHA256 != corpusSHA || plan.FixtureSHA256 != fixtureSHA {
			return errors.New("corpus or fixture changed since run")
		}
		b, err := os.ReadFile(*rowsPath)
		if err != nil {
			return err
		}
		var rows []beforeafter.Row
		malformed := 0
		for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var row beforeafter.Row
			if err := json.Unmarshal(line, &row); err != nil {
				malformed++
				continue
			}
			rows = append(rows, row)
		}
		report, err := beforeafter.Analyze(plan, cases, rows)
		if err != nil {
			return err
		}
		report.Malformed = malformed
		if malformed > 0 {
			report.Valid = false
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	if args[0] != "run" {
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
	if *oldBin == "" || *newBin == "" || *adapter == "" || *runtime == "" {
		return errors.New("run requires old/new Go CLI, adapter and model runtime paths")
	}
	for _, p := range []string{*oldBin, *newBin, *adapter, *runtime} {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("binary must be absolute: %s", p)
		}
	}
	if err := checkCheckout(absRoot, *newCommit); err != nil {
		return err
	}
	version, err := exec.Command(*runtime, "--version").Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(version)) != *runtimeVersion {
		return errors.New("model runtime version changed")
	}
	cfg := beforeafter.Config{RepoRoot: absRoot, CorpusPath: filepath.Join(absRoot, *corpus), OldBinary: *oldBin, NewBinary: *newBin, AdapterBinary: *adapter,
		ExpectedOldSHA256: *oldSHA, ExpectedNewSHA256: *newSHA, ExpectedAdapterSHA256: *adapterSHA, ModelRuntime: *runtime, ModelRuntimeVersion: *runtimeVersion, ExpectedModelRuntimeSHA256: *runtimeSHA,
		RowsPath: *rowsPath, PlanPath: *planPath, RunID: *runID, NewCommit: *newCommit, Model: *model, ModelVersion: *modelVersion, Settings: *settings, Repeats: *repeats,
		MaxModelCalls: *maxCalls, MaxSeconds: *maxSeconds, MaxTokens: *maxTokens, MaxCostUSD: *maxCost, Unpriced: *unpriced,
		Runner: &adapterRunner{binary: *adapter, model: *model, modelVersion: *modelVersion, settings: *settings, runtime: *runtime, runtimeVersion: *runtimeVersion, runtimeSHA: *runtimeSHA}}
	_, err = beforeafter.Run(context.Background(), cfg)
	return err
}

func checkCheckout(root, commit string) error {
	if len(commit) != 40 {
		return errors.New("new commit must be a full SHA")
	}
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	b, err := cmd.Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != commit {
		return errors.New("new commit does not match checkout HEAD")
	}
	cmd = exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=all")
	b, err = cmd.Output()
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(b)) != 0 {
		return errors.New("checkout must be clean before live run")
	}
	cmd = exec.Command("git", "-C", root, "cat-file", "-t", beforeafter.OldCommit)
	b, err = cmd.Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != "commit" {
		return errors.New("historical baseline commit missing")
	}
	return nil
}

type adapterRunner struct{ binary, model, modelVersion, settings, runtime, runtimeVersion, runtimeSHA string }

func (a *adapterRunner) Run(ctx context.Context, req agent.Request) (agent.Observation, error) {
	var obs agent.Observation
	input, err := json.Marshal(req)
	if err != nil {
		return obs, err
	}
	trial, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(trial, a.binary)
	cmd.Env = append(os.Environ(), "OKF_EVAL_MODEL="+a.model, "OKF_EVAL_MODEL_VERSION="+a.modelVersion, "OKF_EVAL_SETTINGS="+a.settings,
		"OKF_EVAL_TOOLKIT_MODE=both", "OKF_EVAL_MODEL_RUNTIME="+a.runtime, "OKF_EVAL_MODEL_RUNTIME_VERSION="+a.runtimeVersion, "OKF_EVAL_MODEL_RUNTIME_SHA256="+a.runtimeSHA)
	cmd.Stdin = bytes.NewReader(input)
	cmd.WaitDelay = time.Second
	var out, stderr boundedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err = cmd.Run()
	if out.exceeded || stderr.exceeded {
		return obs, errors.New("model adapter output exceeded 2 MiB cap")
	}
	if err != nil {
		digest := sha256.Sum256(stderr.b)
		return obs, fmt.Errorf("model adapter: %w; stderr_sha256=%x; stderr_bytes=%d", err, digest, len(stderr.b))
	}
	d := json.NewDecoder(bytes.NewReader(out.b))
	d.DisallowUnknownFields()
	if err := d.Decode(&obs); err != nil {
		return obs, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return obs, errors.New("trailing adapter output")
	}
	return obs, nil
}

type boundedBuffer struct {
	b        []byte
	exceeded bool
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	if len(w.b)+len(p) > 2<<20 {
		w.exceeded = true
		return len(p), nil
	}
	w.b = append(w.b, p...)
	return len(p), nil
}
