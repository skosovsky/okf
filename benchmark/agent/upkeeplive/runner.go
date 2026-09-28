// Package upkeeplive runs the task-013 paired writer/consumer protocol through
// a bring-your-own-model subprocess. Tests never invoke a model.
package upkeeplive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/skosovsky/okf/benchmark/agent"
	"github.com/skosovsky/okf/internal/okfcli"
	"github.com/skosovsky/okf/upkeep"
)

// Adapter receives one JSON request on stdin and emits one JSON response on
// stdout. It must start a fresh model session for every invocation. The runner
// verifies returned session IDs are distinct and never sends gold labels.
type Adapter interface {
	Call(context.Context, Request) (Response, error)
}

type Request struct {
	Role           string                       `json:"role"`
	Repeat         int                          `json:"repeat"`
	Model          string                       `json:"model"`
	PromptSHA256   string                       `json:"prompt_sha256"`
	Writer         *agent.UpkeepWriterRequest   `json:"writer,omitempty"`
	Consumer       *agent.UpkeepConsumerRequest `json:"consumer,omitempty"`
	Reminder       string                       `json:"reminder,omitempty"`
	Draft          *Draft                       `json:"draft,omitempty"`
	CheckerInitial *upkeep.Result               `json:"checker_initial,omitempty"`
}

type Draft struct {
	FinalCode   []agent.Artifact `json:"final_code"`
	FinalBundle []agent.Artifact `json:"final_bundle"`
}

type Response struct {
	SessionID    string             `json:"session_id"`
	Draft        *Draft             `json:"draft,omitempty"`
	Observation  *agent.Observation `json:"observation,omitempty"`
	InputTokens  int                `json:"input_tokens"`
	OutputTokens int                `json:"output_tokens"`
	Failure      string             `json:"failure,omitempty"`
}

type CommandAdapter struct{ Path string }

func (a CommandAdapter) Call(ctx context.Context, req Request) (Response, error) {
	if !filepath.IsAbs(a.Path) {
		return Response{}, errors.New("adapter path must be absolute")
	}
	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	cmd := exec.CommandContext(ctx, a.Path)
	cmd.Stdin = bytes.NewReader(b)
	cmd.WaitDelay = 100 * time.Millisecond
	out, stderr := &boundedBuffer{limit: 4 << 20}, &boundedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = out, stderr
	err = cmd.Run()
	if err != nil {
		return Response{}, fmt.Errorf("adapter: %w: %.300s", err, stderr.String())
	}
	if out.truncated {
		return Response{}, errors.New("adapter response exceeds 4 MiB")
	}
	var resp Response
	dec := json.NewDecoder(bytes.NewReader(out.data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&resp); err != nil {
		return Response{}, err
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Response{}, errors.New("adapter emitted trailing JSON")
	}
	return resp, nil
}

type boundedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

type unknownUsageError struct{ cause error }

func (e unknownUsageError) Error() string { return e.cause.Error() }
func (e unknownUsageError) Unwrap() error { return e.cause }
func usageUnknown(err error) bool         { var e unknownUsageError; return errors.As(err, &e) }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	available := b.limit - len(b.data)
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
func (b *boundedBuffer) String() string { return string(b.data) }

type Limits struct {
	MaxCalls    int `json:"max_calls"`
	MaxSeconds  int `json:"max_seconds"`
	MaxTokens   int `json:"max_tokens"` // post-call stop; one call may exceed this
	CallSeconds int `json:"call_seconds"`
}

type Event struct {
	CaseID        string `json:"case_id"`
	Arm           string `json:"arm"`
	Repeat        int    `json:"repeat"`
	Stage         string `json:"stage"`
	SessionID     string `json:"session_id,omitempty"`
	InputTokens   int    `json:"input_tokens,omitempty"`
	OutputTokens  int    `json:"output_tokens,omitempty"`
	CheckerStatus string `json:"checker_status,omitempty"`
	BundleSHA256  string `json:"bundle_sha256,omitempty"`
	CLIExit       int    `json:"cli_exit,omitempty"`
	Failure       string `json:"failure,omitempty"`
	UsageUnknown  bool   `json:"usage_unknown,omitempty"`
}

type Result struct {
	Rows              agent.UpkeepStudyRows `json:"rows"`
	Events            []Event               `json:"events"`
	Calls             int                   `json:"calls"`
	Tokens            int                   `json:"tokens"`
	UnknownUsageCalls int                   `json:"unknown_usage_calls"`
	Stopped           string                `json:"stopped,omitempty"`
}

const reminder = "After changing code, review the OKF concept docs. The advisory checker result will be supplied once; revise the bundle if needed."

func Run(ctx context.Context, plan agent.UpkeepStudy, adapter Adapter, limits Limits) (Result, error) {
	if adapter == nil || limits.MaxCalls < 1 || limits.MaxSeconds < 1 || limits.MaxTokens < 1 || limits.CallSeconds < 1 {
		return Result{}, errors.New("adapter and positive limits required")
	}
	if _, err := agent.AnalyzeUpkeepStudy(plan, agent.UpkeepStudyRows{}); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(limits.MaxSeconds)*time.Second)
	defer cancel()
	for _, c := range plan.Cases {
		if err := preflightChecker(ctx, c); err != nil {
			return Result{}, fmt.Errorf("%s checker preflight: %w", c.ID, err)
		}
	}
	result := Result{}
	sessions := map[string]bool{}
	call := func(req Request) (Response, error) {
		if result.Calls >= limits.MaxCalls {
			return Response{}, errors.New("max calls reached")
		}
		if result.Tokens >= limits.MaxTokens {
			return Response{}, errors.New("post-call token stop reached")
		}
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		one, done := context.WithTimeout(ctx, time.Duration(limits.CallSeconds)*time.Second)
		defer done()
		result.Calls++
		resp, err := adapter.Call(one, req)
		if err != nil {
			result.UnknownUsageCalls++
			return resp, unknownUsageError{err}
		}
		if resp.InputTokens < 0 || resp.OutputTokens < 0 || resp.InputTokens > math.MaxInt-resp.OutputTokens || resp.InputTokens+resp.OutputTokens == 0 || resp.SessionID == "" || sessions[resp.SessionID] {
			result.UnknownUsageCalls++
			return resp, unknownUsageError{errors.New("invalid or reused adapter session/usage")}
		}
		sessions[resp.SessionID] = true
		if result.Tokens > math.MaxInt-(resp.InputTokens+resp.OutputTokens) {
			result.UnknownUsageCalls++
			return resp, unknownUsageError{errors.New("usage counter overflow")}
		}
		result.Tokens += resp.InputTokens + resp.OutputTokens
		return resp, nil
	}
	for repeat := 1; repeat <= plan.Repeats; repeat++ {
		for _, c := range plan.Cases {
			// Frozen pseudo-random order, independent of model output.
			arms := []string{agent.UpkeepCheckerOff, agent.UpkeepCheckerOn}
			order := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", plan.ID, c.ID, repeat)))
			if order[0]&1 == 1 {
				arms[0], arms[1] = arms[1], arms[0]
			}
			for _, arm := range arms {
				if result.Calls >= limits.MaxCalls || result.Tokens >= limits.MaxTokens || ctx.Err() != nil {
					result.Stopped = "budget reached before all preregistered pairs"
					return result, nil
				}
				wr, cr, events := runPair(ctx, c, arm, repeat, plan, call)
				result.Rows.Writers = append(result.Rows.Writers, wr)
				if cr != nil {
					result.Rows.Consumers = append(result.Rows.Consumers, *cr)
				}
				result.Events = append(result.Events, events...)
				if result.UnknownUsageCalls > 0 {
					result.Stopped = "unknown token usage after adapter attempt"
					return result, nil
				}
			}
		}
	}
	return result, nil
}

// preflightChecker proves the pinned target change trips the checker before
// any adapter call. This catches a missing relevant path or inert treatment.
func preflightChecker(ctx context.Context, c agent.UpkeepCase) error {
	root, err := os.MkdirTemp("", "okf-upkeep-preflight-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	if err := prepareRepo(ctx, root, c); err != nil {
		return err
	}
	cfg := upkeep.Config{RepoRoot: root, BundleRoot: ".okf", RelevantPaths: []string{"decision.go", "handler-capture.txt"}, Mode: "advisory", FailurePolicy: "open", TimeoutMS: 5000, MaxSessionMinutes: 60}
	baseline, err := upkeep.Snapshot(ctx, cfg)
	if err != nil {
		return err
	}
	if err := writeDraft(root, c, Draft{FinalCode: c.TargetCode, FinalBundle: c.InitialBundle}); err != nil {
		return err
	}
	current, err := upkeep.Snapshot(ctx, cfg)
	if err != nil {
		return err
	}
	result, err := upkeep.Check(cfg, baseline, current, nil)
	if err != nil {
		return err
	}
	if result.Status != "needs_review" {
		return fmt.Errorf("target change gives %q", result.Status)
	}
	found := false
	for _, change := range result.Changes {
		for _, target := range c.TargetCode {
			if change.Path == target.Path || change.OldPath == target.Path {
				found = true
			}
		}
	}
	if !found {
		return errors.New("checker change does not include target code")
	}
	return nil
}

func runPair(ctx context.Context, c agent.UpkeepCase, arm string, repeat int, plan agent.UpkeepStudy, call func(Request) (Response, error)) (agent.UpkeepWriterRow, *agent.UpkeepConsumerRow, []Event) {
	start, _ := startingHash(c)
	w := agent.UpkeepWriterRow{CaseID: c.ID, Arm: arm, Repeat: repeat, StartingSHA256: start}
	events := []Event{}
	fail := func(stage string, err error) (agent.UpkeepWriterRow, *agent.UpkeepConsumerRow, []Event) {
		w.Failure = stage + ": " + err.Error()
		events = append(events, Event{CaseID: c.ID, Arm: arm, Repeat: repeat, Stage: stage, Failure: err.Error()})
		return w, nil, events
	}
	root, err := os.MkdirTemp("", "okf-upkeep-live-*")
	if err != nil {
		return fail("setup", err)
	}
	defer os.RemoveAll(root)
	if err = prepareRepo(ctx, root, c); err != nil {
		return fail("setup", err)
	}
	cfg := upkeep.Config{RepoRoot: root, BundleRoot: ".okf", RelevantPaths: []string{"decision.go", "handler-capture.txt"}, Mode: "advisory", FailurePolicy: "open", TimeoutMS: 5000, MaxSessionMinutes: 60}
	baseline, err := upkeep.Snapshot(ctx, cfg)
	if err != nil {
		return fail("snapshot_baseline", err)
	}
	req := Request{Role: "writer", Repeat: repeat, Model: plan.WriterModel, PromptSHA256: plan.WriterPrompt, Writer: ptr(c.WriterRequest())}
	if arm == agent.UpkeepCheckerOn {
		req.Reminder = reminder
	}
	first, err := call(req)
	if err != nil {
		events = append(events, Event{CaseID: c.ID, Arm: arm, Repeat: repeat, Stage: "writer_initial_call", UsageUnknown: usageUnknown(err), Failure: err.Error()})
		return fail("writer_initial", err)
	}
	w.SessionID = first.SessionID
	w.InputTokens += first.InputTokens
	w.OutputTokens += first.OutputTokens
	events = append(events, Event{CaseID: c.ID, Arm: arm, Repeat: repeat, Stage: "writer_initial", SessionID: first.SessionID, InputTokens: first.InputTokens, OutputTokens: first.OutputTokens, Failure: first.Failure})
	if first.Failure != "" {
		return fail("writer_initial", errors.New(first.Failure))
	}
	if first.Draft == nil {
		return fail("writer_initial", errors.New("missing structured draft"))
	}
	w.FinalCode, w.FinalBundle = first.Draft.FinalCode, first.Draft.FinalBundle
	if err = writeDraft(root, c, *first.Draft); err != nil {
		return fail("writer_initial", err)
	}
	final := *first.Draft
	if arm == agent.UpkeepCheckerOn {
		current, err := upkeep.Snapshot(ctx, cfg)
		if err != nil {
			return fail("checker_initial_snapshot", err)
		}
		initial, err := upkeep.Check(cfg, baseline, current, nil)
		if err != nil {
			return fail("checker_initial", err)
		}
		if initial.Status != "needs_review" {
			return fail("checker_initial", fmt.Errorf("expected needs_review, got %s", initial.Status))
		}
		initialJSON, _ := json.Marshal(initial)
		w.CheckerInitial = string(initialJSON)
		events = append(events, Event{CaseID: c.ID, Arm: arm, Repeat: repeat, Stage: "checker_initial", CheckerStatus: initial.Status})
		revision, err := call(Request{Role: "writer_revision", Repeat: repeat, Model: plan.WriterModel, PromptSHA256: plan.WriterPrompt, Writer: ptr(c.WriterRequest()), Reminder: reminder, Draft: &final, CheckerInitial: &initial})
		if err != nil {
			events = append(events, Event{CaseID: c.ID, Arm: arm, Repeat: repeat, Stage: "writer_revision_call", UsageUnknown: usageUnknown(err), Failure: err.Error()})
			return fail("writer_revision", err)
		}
		w.SessionID = revision.SessionID
		w.InputTokens += revision.InputTokens
		w.OutputTokens += revision.OutputTokens
		events = append(events, Event{CaseID: c.ID, Arm: arm, Repeat: repeat, Stage: "writer_revision", SessionID: revision.SessionID, InputTokens: revision.InputTokens, OutputTokens: revision.OutputTokens, Failure: revision.Failure})
		if revision.Failure != "" {
			return fail("writer_revision", errors.New(revision.Failure))
		}
		if revision.Draft == nil {
			return fail("writer_revision", errors.New("missing structured revision"))
		}
		final = *revision.Draft
		w.FinalCode, w.FinalBundle = final.FinalCode, final.FinalBundle
		if err = writeDraft(root, c, final); err != nil {
			return fail("writer_revision", err)
		}
		current, err = upkeep.Snapshot(ctx, cfg)
		if err != nil {
			return fail("checker_final_snapshot", err)
		}
		pre, err := upkeep.Check(cfg, baseline, current, nil)
		if err != nil {
			return fail("checker_final", err)
		}
		var decision *upkeep.Decision
		changed := changedConcepts(c.InitialBundle, final.FinalBundle)
		if len(changed) > 0 {
			decision = &upkeep.Decision{Fingerprint: pre.Fingerprint, Kind: "updated", UpdatedConcepts: changed}
		}
		checked, err := upkeep.Check(cfg, baseline, current, decision)
		if err != nil {
			return fail("checker_final", err)
		}
		checkedJSON, _ := json.Marshal(checked)
		w.CheckerResult = string(checkedJSON)
		events = append(events, Event{CaseID: c.ID, Arm: arm, Repeat: repeat, Stage: "checker_final", CheckerStatus: checked.Status})
	}
	codeHash, err := agent.UpkeepArtifactsSHA256(final.FinalCode)
	if err != nil {
		return fail("verify_code", err)
	}
	targetHash, _ := agent.UpkeepArtifactsSHA256(c.TargetCode)
	if codeHash != targetHash {
		return fail("verify_code", errors.New("final code differs from exact target"))
	}
	bundleHash, err := agent.UpkeepArtifactsSHA256(final.FinalBundle)
	if err != nil {
		return fail("verify_bundle", err)
	}
	var stdout, stderr bytes.Buffer
	exit := okfcli.Run([]string{"validate", "--path", filepath.Join(root, ".okf"), "--json", "--temporal-profile", "instant-0b87c52"}, &stdout, &stderr)
	events = append(events, Event{CaseID: c.ID, Arm: arm, Repeat: repeat, Stage: "go_cli_validate", CLIExit: exit, BundleSHA256: bundleHash})
	if exit != 0 {
		return fail("go_cli_validate", fmt.Errorf("exit %d: %.200s %.200s", exit, stdout.String(), stderr.String()))
	}
	var validation struct {
		Conformant bool `json:"conformant"`
		Errors     int  `json:"errors"`
	}
	if err = json.Unmarshal(stdout.Bytes(), &validation); err != nil || !validation.Conformant || validation.Errors != 0 {
		return fail("go_cli_validate", errors.New("bundle validation report is nonconformant"))
	}
	consumerReq := c.ConsumerRequest(final.FinalBundle)
	consumer, err := call(Request{Role: "consumer", Repeat: repeat, Model: plan.ConsumerModel, PromptSHA256: plan.ConsumerPrompt, Consumer: &consumerReq})
	if err != nil {
		events = append(events, Event{CaseID: c.ID, Arm: arm, Repeat: repeat, Stage: "consumer", BundleSHA256: bundleHash, UsageUnknown: usageUnknown(err), Failure: err.Error()})
		return w, &agent.UpkeepConsumerRow{CaseID: c.ID, Arm: arm, Repeat: repeat, VisibleSHA256: bundleHash, Failure: err.Error()}, events
	}
	cr := &agent.UpkeepConsumerRow{CaseID: c.ID, Arm: arm, Repeat: repeat, ConsumerSessionID: consumer.SessionID, VisibleSHA256: bundleHash}
	if consumer.Observation != nil {
		cr.Observation = *consumer.Observation
	}
	cr.Observation.InputTokens = consumer.InputTokens
	cr.Observation.OutputTokens = consumer.OutputTokens
	cr.Failure = consumer.Failure
	if consumer.Observation == nil && cr.Failure == "" {
		cr.Failure = "missing structured observation"
	}
	events = append(events, Event{CaseID: c.ID, Arm: arm, Repeat: repeat, Stage: "consumer", SessionID: consumer.SessionID, InputTokens: consumer.InputTokens, OutputTokens: consumer.OutputTokens, BundleSHA256: bundleHash, Failure: cr.Failure})
	return w, cr, events
}

func ptr[T any](v T) *T { return &v }

func startingHash(c agent.UpkeepCase) (string, error) {
	code, err := agent.UpkeepArtifactsSHA256(c.InitialCode)
	if err != nil {
		return "", err
	}
	bundle, err := agent.UpkeepArtifactsSHA256(c.InitialBundle)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(code + "\x00" + bundle))
	return hex.EncodeToString(h[:]), nil
}

func prepareRepo(ctx context.Context, root string, c agent.UpkeepCase) error {
	fixtureGit := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null"}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w: %s", args, err, out)
		}
		return nil
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "okf-study@example.invalid"}, {"config", "user.name", "OKF Study"}, {"config", "commit.gpgsign", "false"}} {
		if err := fixtureGit(args...); err != nil {
			return err
		}
	}
	if err := writeArtifacts(root, "", c.InitialCode); err != nil {
		return err
	}
	if err := writeArtifacts(root, ".okf", c.InitialBundle); err != nil {
		return err
	}
	for _, args := range [][]string{{"add", "--all"}, {"commit", "-q", "-m", "frozen baseline"}} {
		if err := fixtureGit(args...); err != nil {
			return err
		}
	}
	return nil
}

func writeDraft(root string, c agent.UpkeepCase, d Draft) error {
	if _, err := agent.UpkeepArtifactsSHA256(d.FinalCode); err != nil {
		return err
	}
	if _, err := agent.UpkeepArtifactsSHA256(d.FinalBundle); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".git" || entry.Name() == ".okf" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(root, ".okf")); err != nil {
		return err
	}
	if err := writeArtifacts(root, "", d.FinalCode); err != nil {
		return err
	}
	if err := writeArtifacts(root, ".okf", d.FinalBundle); err != nil {
		return err
	}
	return verifyCodeCheckout(root, d.FinalCode)
}

func verifyCodeCheckout(root string, expected []agent.Artifact) error {
	want := map[string]string{}
	for _, a := range expected {
		want[a.Path] = a.Content
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == ".git" || rel == ".okf" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		name := filepath.ToSlash(rel)
		content, ok := want[name]
		if !ok || !d.Type().IsRegular() {
			return fmt.Errorf("unexpected checkout file %q", name)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if string(b) != content {
			return fmt.Errorf("checkout bytes differ for %q", name)
		}
		seen[name] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(want) {
		return errors.New("checkout lacks declared code file")
	}
	return nil
}

func writeArtifacts(root, sub string, artifacts []agent.Artifact) error {
	for _, a := range artifacts {
		if a.Path == ".git" || strings.HasPrefix(a.Path, ".git/") || a.Path == ".okf" || strings.HasPrefix(a.Path, ".okf/") {
			return fmt.Errorf("reserved artifact path %q", a.Path)
		}
		p := filepath.Join(root, sub, filepath.FromSlash(a.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(a.Content), 0600); err != nil {
			return err
		}
	}
	return nil
}

func changedConcepts(before, after []agent.Artifact) []string {
	old := map[string]string{}
	for _, a := range before {
		old[a.Path] = a.Content
	}
	var paths []string
	for _, a := range after {
		if strings.HasSuffix(a.Path, ".md") && filepath.Base(a.Path) != "index.md" && filepath.Base(a.Path) != "log.md" && old[a.Path] != a.Content {
			paths = append(paths, ".okf/"+a.Path)
		}
	}
	sort.Strings(paths)
	return paths
}
