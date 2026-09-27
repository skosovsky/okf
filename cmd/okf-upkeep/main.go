package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/skosovsky/okf/upkeep"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: okf-upkeep baseline|check --config FILE")
		return 1
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "upkeep config JSON")
	baselinePath := fs.String("baseline", "", "baseline JSON (check)")
	session := fs.String("session", "", "session ID printed by baseline (check)")
	outPath := fs.String("out", "", "baseline output JSON (baseline)")
	decisionPath := fs.String("decision", "", "decision JSON (check)")
	override := fs.String("override", "", "explicit reason to allow this invocation")
	attempt := fs.Int("attempt", 1, "host retry attempt; attempt 2+ breaks a stop loop")
	adapter := fs.String("adapter", "json", "json or claude-stop")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "--config is required")
		return 1
	}
	cfg, err := upkeep.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *adapter != "json" && *adapter != "claude-stop" {
		fmt.Fprintln(stderr, "adapter must be json or claude-stop")
		return 1
	}
	if *attempt < 1 {
		fmt.Fprintln(stderr, "attempt must be positive")
		return 1
	}
	if args[0] == "baseline" {
		if *outPath == "" {
			fmt.Fprintln(stderr, "--out is required")
			return 1
		}
		snap, err := upkeep.Snapshot(context.Background(), cfg)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := saveJSON(*outPath, snap); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, snap.SessionID)
		return 0
	}
	if args[0] != "check" {
		fmt.Fprintln(stderr, "expected baseline or check")
		return 1
	}
	if *baselinePath == "" {
		fmt.Fprintln(stderr, "--baseline is required")
		return 1
	}
	if *session == "" {
		fmt.Fprintln(stderr, "--session is required")
		return 1
	}
	if *adapter == "claude-stop" {
		var input struct {
			StopHookActive bool `json:"stop_hook_active"`
		}
		if err := json.NewDecoder(stdin).Decode(&input); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if input.StopHookActive {
			*attempt = 2
		}
	}
	var base upkeep.Baseline
	if err := readJSON(*baselinePath, &base); err != nil {
		return runtimeFailure(cfg, *adapter, *attempt, *override, stdout, stderr, err)
	}
	if base.SessionID != *session {
		return runtimeFailure(cfg, *adapter, *attempt, *override, stdout, stderr, errors.New("baseline session ID mismatch"))
	}
	var decision *upkeep.Decision
	if *decisionPath != "" {
		decision = new(upkeep.Decision)
		if err := readJSON(*decisionPath, decision); err != nil {
			return runtimeFailure(cfg, *adapter, *attempt, *override, stdout, stderr, err)
		}
	}
	current, err := upkeep.Snapshot(context.Background(), cfg)
	if err != nil {
		return runtimeFailure(cfg, *adapter, *attempt, *override, stdout, stderr, err)
	}
	result, err := upkeep.Check(cfg, base, current, decision)
	if err != nil {
		return runtimeFailure(cfg, *adapter, *attempt, *override, stdout, stderr, err)
	}
	block := result.Status == "needs_review" && cfg.Mode == "blocking"
	if strings.TrimSpace(*override) != "" && block {
		result.Status = "overridden"
		result.Reason = *override
		block = false
	}
	if *attempt >= 2 && block {
		result.Status = "loop_guard_allow"
		result.Reason = "host retry already occurred"
		block = false
	}
	return emit(*adapter, stdout, result, block)
}

func runtimeFailure(cfg upkeep.Config, adapter string, attempt int, override string, stdout, stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, err)
	block := cfg.Mode == "blocking" && cfg.FailurePolicy == "closed"
	r := upkeep.Result{Status: "unavailable", Reason: err.Error(), Changes: []upkeep.Entry{}, Advisory: !block}
	if block && strings.TrimSpace(override) != "" {
		r.Status, r.Reason, block = "overridden", override, false
	}
	if block && attempt >= 2 {
		r.Status, r.Reason, block = "loop_guard_allow", "host retry already occurred", false
	}
	return emit(adapter, stdout, r, block)
}

func emit(adapter string, stdout io.Writer, result upkeep.Result, block bool) int {
	var body any = result
	if adapter == "claude-stop" {
		if block {
			bodyJSON, _ := json.Marshal(result)
			body = struct {
				Decision string `json:"decision"`
				Reason   string `json:"reason"`
			}{"block", "Review repository knowledge for these changes: " + string(bodyJSON)}
		} else {
			body = struct{}{}
		}
	}
	if err := json.NewEncoder(stdout).Encode(body); err != nil {
		return 1
	}
	if block && adapter == "json" {
		return 2
	}
	return 0
}

func readJSON(path string, dst any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: trailing JSON data", path)
	}
	return nil
}

func saveJSON(path string, src any) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".okf-upkeep-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if err := json.NewEncoder(f).Encode(src); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
