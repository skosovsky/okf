package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skosovsky/okf/benchmark/agent"
	"github.com/skosovsky/okf/internal/okfcli"
)

// exposeGoCLI materializes an isolated, valid OKF bundle and supplies actual
// Go CLI projections to the treatment arm. Any toolkit failure is a trial
// failure and never becomes a model factual error.
func exposeGoCLI(artifacts []agent.Artifact) ([]agent.Artifact, int, error) {
	root, err := os.MkdirTemp("", "okf-agent-bundle-*")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(root)
	for _, a := range artifacts {
		if a.Path == "" || filepath.IsAbs(a.Path) || filepath.Base(a.Path) != a.Path || a.Path == "." || a.Path == ".." {
			return nil, 0, fmt.Errorf("unsafe artifact path %q", a.Path)
		}
		if err := os.WriteFile(filepath.Join(root, a.Path), []byte(a.Content), 0600); err != nil {
			return nil, 0, err
		}
	}
	var stdout, stderr bytes.Buffer
	if code := okfcli.Run([]string{"validate", "--path", root, "--json", "--temporal-profile", "instant-0b87c52"}, &stdout, &stderr); code != 0 {
		return nil, 1, fmt.Errorf("Go CLI validate exit %d: %s %s", code, stdout.String(), stderr.String())
	}
	var summary struct {
		Conformant bool `json:"conformant"`
		Errors     int  `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil || !summary.Conformant || summary.Errors != 0 {
		return nil, 1, fmt.Errorf("Go CLI validate malformed/nonconformant report: %v", err)
	}
	result := append([]agent.Artifact(nil), artifacts...)
	result = append(result, agent.Artifact{ID: "cli-validation", Path: "tool/validate.json", Content: stdout.String()})
	calls := 1
	for _, a := range artifacts {
		if strings.EqualFold(a.Path, "index.md") {
			continue
		}
		stdout.Reset()
		stderr.Reset()
		if code := okfcli.Run([]string{"parse", filepath.Join(root, a.Path), "--json"}, &stdout, &stderr); code != 0 {
			return nil, calls + 1, fmt.Errorf("Go CLI parse %s exit %d: %s %s", a.ID, code, stdout.String(), stderr.String())
		}
		var projection map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &projection); err != nil {
			return nil, calls + 1, fmt.Errorf("Go CLI parse %s JSON: %w", a.ID, err)
		}
		projection["file"] = a.Path
		stable, err := json.Marshal(projection)
		if err != nil {
			return nil, calls + 1, err
		}
		result = append(result, agent.Artifact{ID: "cli-" + a.ID, Path: "tool/parse-" + a.ID + ".json", Content: string(stable)})
		calls++
	}
	return result, calls, nil
}
