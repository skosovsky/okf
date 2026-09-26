// Command upkeep-live runs the task-013 writer/consumer experiment.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/skosovsky/okf/benchmark/agent"
	"github.com/skosovsky/okf/benchmark/agent/upkeeplive"
)

type manifest struct {
	PlanSHA256        string             `json:"plan_sha256"`
	AdapterSHA256     string             `json:"adapter_sha256"`
	RunnerSHA256      string             `json:"runner_sha256"`
	GoVersion         string             `json:"go_version"`
	StartedAt         time.Time          `json:"started_at"`
	Limits            upkeeplive.Limits  `json:"limits"`
	Calls             int                `json:"calls"`
	Tokens            int                `json:"tokens"`
	UnknownUsageCalls int                `json:"unknown_usage_calls"`
	Stopped           string             `json:"stopped,omitempty"`
	Events            []upkeeplive.Event `json:"events"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("upkeep-live", flag.ContinueOnError)
	planPath := fs.String("plan", "", "frozen UpkeepStudy plan JSON")
	rowsPath := fs.String("rows", "", "exclusive output UpkeepStudyRows JSON")
	auditPath := fs.String("audit", "", "exclusive audit manifest JSON")
	adapterPath := fs.String("adapter", "", "absolute model adapter executable")
	maxCalls := fs.Int("max-calls", 12, "hard adapter invocation cap")
	maxSeconds := fs.Int("max-seconds", 600, "hard total wall-clock cap")
	maxTokens := fs.Int("max-tokens", 150000, "post-call token stop")
	callSeconds := fs.Int("call-seconds", 45, "hard per-call cap")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *planPath == "" || *rowsPath == "" || *auditPath == "" || !filepath.IsAbs(*adapterPath) || fs.NArg() != 0 {
		return errors.New("usage: upkeep-live -plan PLAN -rows NEW_ROWS -audit NEW_AUDIT -adapter /absolute/adapter [caps]")
	}
	if *callSeconds < 45 {
		return errors.New("call-seconds must be at least 45: built-in Codex adapter needs a 38-second internal deadline plus cleanup reserve")
	}
	if *rowsPath == *auditPath || *rowsPath == *planPath || *auditPath == *planPath {
		return errors.New("plan, rows and audit paths must differ")
	}
	var plan agent.UpkeepStudy
	planBytes, err := readJSON(*planPath, &plan)
	if err != nil {
		return err
	}
	if _, err = agent.AnalyzeUpkeepStudy(plan, agent.UpkeepStudyRows{}); err != nil {
		return err
	}
	adapterBytes, err := os.ReadFile(*adapterPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(*adapterPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("adapter is not an executable regular file")
	}
	adapterHash := sha256.Sum256(adapterBytes)
	runnerPath, err := os.Executable()
	if err != nil {
		return err
	}
	runnerBytes, err := os.ReadFile(runnerPath)
	if err != nil {
		return err
	}
	runnerHash := sha256.Sum256(runnerBytes)
	if plan.CheckerSHA256 != hex.EncodeToString(runnerHash[:]) {
		return errors.New("plan checker_sha256 must match this runner binary: upkeep is linked in-process")
	}
	rowsFile, err := os.OpenFile(*rowsPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer rowsFile.Close()
	auditFile, err := os.OpenFile(*auditPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer auditFile.Close()
	limits := upkeeplive.Limits{MaxCalls: *maxCalls, MaxSeconds: *maxSeconds, MaxTokens: *maxTokens, CallSeconds: *callSeconds}
	started := time.Now().UTC()
	result, err := upkeeplive.Run(context.Background(), plan, upkeeplive.CommandAdapter{Path: *adapterPath}, limits)
	if err != nil {
		return err
	}
	rowsHash, err := writeSynced(rowsFile, result.Rows)
	if err != nil {
		return err
	}
	planHash := sha256.Sum256(planBytes)
	audit := manifest{PlanSHA256: hex.EncodeToString(planHash[:]), AdapterSHA256: hex.EncodeToString(adapterHash[:]), RunnerSHA256: hex.EncodeToString(runnerHash[:]), GoVersion: runtime.Version(), StartedAt: started, Limits: limits, Calls: result.Calls, Tokens: result.Tokens, UnknownUsageCalls: result.UnknownUsageCalls, Stopped: result.Stopped, Events: result.Events}
	auditHash, err := writeSynced(auditFile, audit)
	if err != nil {
		return err
	}
	if err := rowsFile.Close(); err != nil {
		return err
	}
	if err := auditFile.Close(); err != nil {
		return err
	}
	marker, err := os.OpenFile(*auditPath+".complete", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := writeSynced(marker, map[string]string{"rows_sha256": rowsHash, "audit_sha256": auditHash}); err != nil {
		marker.Close()
		return err
	}
	return marker.Close()
}

func readJSON(path string, dst any) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 16<<20 {
		return nil, errors.New("plan exceeds 16 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("plan has trailing JSON")
	}
	return b, nil
}

func writeSynced(f *os.File, v any) (string, error) {
	h := sha256.New()
	e := json.NewEncoder(io.MultiWriter(f, h))
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
