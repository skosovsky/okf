package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/internal/okfcli"
)

func actionEnv(t *testing.T) (map[string]string, func(string) string) {
	t.Helper()
	dir := t.TempDir()
	output := filepath.Join(dir, "output")
	if err := os.WriteFile(output, nil, 0600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"GITHUB_WORKSPACE":      dir,
		"GITHUB_OUTPUT":         output,
		"RUNNER_TEMP":           dir,
		"OKF_ACTION_INPUT_PATH": "bundle",
		"OKF_ACTION_INPUT_SPEC": "auto",
	}
	return values, func(key string) string { return values[key] }
}

func TestActionClassifiesOneRunAndPreservesReport(t *testing.T) {
	for _, test := range []struct {
		name, response, stderr, outcome string
		code, runs                      int
		wantError                       bool
	}{
		{name: "pass", response: `{"conformant":true,"policy_failures":0,"diagnostics":[]}` + "\n", outcome: "pass", runs: 1},
		{name: "base error", response: `{"conformant":false,"policy_failures":0,"diagnostics":[{}]}` + "\n", outcome: "validation_failure", code: 1, runs: 1, wantError: true},
		{name: "policy error", response: `{"conformant":true,"policy_failures":1,"diagnostics":[{}]}` + "\n", outcome: "validation_failure", code: 1, runs: 1, wantError: true},
		{name: "operational error", stderr: "error: unavailable\n", outcome: "operational_failure", code: 1, runs: 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			values, getenv := actionEnv(t)
			calls := 0
			var errOutput bytes.Buffer
			fake := func(args []string, stdout, stderr io.Writer) int {
				calls++
				_, _ = io.WriteString(stdout, test.response)
				_, _ = io.WriteString(stderr, test.stderr)
				return test.code
			}

			// Act.
			err := run(getenv, fake, &errOutput)

			// Assert.
			if (err != nil) != test.wantError || calls != test.runs {
				t.Fatalf("run error/calls = %v/%d, want error=%t/calls=%d", err, calls, test.wantError, test.runs)
			}
			data, readErr := os.ReadFile(values["GITHUB_OUTPUT"])
			if readErr != nil || !strings.Contains(string(data), "outcome="+test.outcome+"\n") {
				t.Fatalf("outputs = %q, err=%v", data, readErr)
			}
			if test.response != "" && !strings.Contains(string(data), "report="+strings.TrimSpace(test.response)+"\n") {
				t.Fatalf("inline report missing: %q", data)
			}
			if test.response == "" && !strings.Contains(string(data), "report_path=\n") {
				t.Fatalf("operational report path should be empty: %q", data)
			}
		})
	}
}

func TestActionPathAndInputsCannotBecomeShell(t *testing.T) {
	// Arrange.
	values, getenv := actionEnv(t)
	values["OKF_ACTION_INPUT_PATH"] = `bundle with spaces/quote' $(touch NEVER_EXECUTE)`
	values["OKF_ACTION_INPUT_MAX_WARNINGS"] = "0"
	values["OKF_ACTION_INPUT_STRICT"] = "true"
	var gotArgs []string
	fake := func(args []string, stdout, _ io.Writer) int {
		gotArgs = append([]string(nil), args...)
		_, _ = io.WriteString(stdout, `{"conformant":true,"policy_failures":0,"diagnostics":[]}`+"\n")
		return 0
	}

	// Act.
	err := run(getenv, fake, io.Discard)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(values["GITHUB_WORKSPACE"], values["OKF_ACTION_INPUT_PATH"])
	if gotArgs[2] != wantPath || !containsPair(gotArgs, "--max-warnings", "0") || !containsArg(gotArgs, "--strict") {
		t.Fatalf("args = %#v", gotArgs)
	}
	if _, err := os.Stat(filepath.Join(values["GITHUB_WORKSPACE"], "NEVER_EXECUTE")); !os.IsNotExist(err) {
		t.Fatalf("path input executed: %v", err)
	}
}

func TestActionRealCLIHandlesAdversarialPath(t *testing.T) {
	// Arrange.
	values, getenv := actionEnv(t)
	name := `bundle with spaces ' " ; $(touch NEVER_EXECUTE)`
	values["OKF_ACTION_INPUT_PATH"] = name
	root := filepath.Join(values["GITHUB_WORKSPACE"], name)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for filename, body := range map[string]string{
		"index.md":   "---\nokf_version: \"0.2\"\n---\n# Notes\n\n* [Concept](concept.md)\n",
		"concept.md": "---\ntype: Note\n---\nBody.\n",
	} {
		if err := os.WriteFile(filepath.Join(root, filename), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// Act.
	err := run(getenv, okfcli.Run, io.Discard)

	// Assert.
	if err != nil {
		t.Fatalf("adversarial path validation failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(values["GITHUB_WORKSPACE"], "NEVER_EXECUTE")); !os.IsNotExist(err) {
		t.Fatalf("path input executed: %v", err)
	}
}

func TestActionLargeReportUsesFile(t *testing.T) {
	// Arrange.
	values, getenv := actionEnv(t)
	response := `{"conformant":true,"policy_failures":0,"diagnostics":[{"message":"` + strings.Repeat("a", inlineReportLimit) + `"}]}` + "\n"
	fake := func(_ []string, stdout, _ io.Writer) int {
		_, _ = io.WriteString(stdout, response)
		return 0
	}

	// Act.
	err := run(getenv, fake, io.Discard)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(values["GITHUB_OUTPUT"])
	if err != nil || !strings.Contains(string(data), "report=\n") || strings.Contains(string(data), strings.Repeat("a", 100)) {
		t.Fatalf("oversized inline output = %q, err=%v", data, err)
	}
	var path string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "report_path=") {
			path = strings.TrimPrefix(line, "report_path=")
		}
	}
	full, err := os.ReadFile(path)
	if err != nil || string(full) != response {
		t.Fatalf("full report missing/mismatched: %v", err)
	}
}

func TestActionRealCLIIntegrationFixtures(t *testing.T) {
	for _, test := range []struct {
		name, fixture, spec, temporalProfile, asOf, strict, budget, outcome string
		wantError                                                           bool
	}{
		{name: "valid", fixture: "valid", outcome: "pass"},
		{name: "malformed", fixture: "malformed", outcome: "validation_failure", wantError: true},
		{name: "strict warning", fixture: "strict-warning", strict: "true", outcome: "pass"},
		{name: "warning budget", fixture: "strict-warning", strict: "true", budget: "0", outcome: "validation_failure", wantError: true},
		{name: "missing directory", fixture: "absent", outcome: "operational_failure", wantError: true},
		{name: "selector conflict", fixture: "legacy", spec: "0.2", outcome: "validation_failure", wantError: true},
		{name: "instant profile", fixture: "valid", temporalProfile: "instant-0b87c52", asOf: "2026-09-26T10:00:00+07:00", outcome: "pass"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			values, getenv := actionEnv(t)
			root, err := filepath.Abs(filepath.Join("..", "..", ".github", "fixtures", "okf-action", test.fixture))
			if err != nil {
				t.Fatal(err)
			}
			values["OKF_ACTION_INPUT_PATH"] = root
			values["OKF_ACTION_INPUT_STRICT"] = test.strict
			values["OKF_ACTION_INPUT_MAX_WARNINGS"] = test.budget
			values["OKF_ACTION_INPUT_TEMPORAL_PROFILE"] = test.temporalProfile
			values["OKF_ACTION_INPUT_AS_OF"] = test.asOf
			if test.spec != "" {
				values["OKF_ACTION_INPUT_SPEC"] = test.spec
			}

			// Act.
			err = run(getenv, okfcli.Run, io.Discard)
			args := []string{"validate", "--path", root, "--spec", values["OKF_ACTION_INPUT_SPEC"], "--format", "json"}
			if test.temporalProfile != "" {
				args = append(args, "--temporal-profile", test.temporalProfile)
			}
			if test.asOf != "" {
				args = append(args, "--as-of", test.asOf)
			}
			if test.strict == "true" {
				args = append(args, "--strict")
			}
			if test.budget != "" {
				args = append(args, "--max-warnings", test.budget)
			}
			var localReport, localErrors bytes.Buffer
			localCode := okfcli.Run(args, &localReport, &localErrors)

			// Assert.
			if (err != nil) != test.wantError {
				t.Fatalf("run error = %v", err)
			}
			data, err := os.ReadFile(values["GITHUB_OUTPUT"])
			if err != nil || !strings.Contains(string(data), "outcome="+test.outcome+"\n") {
				t.Fatalf("outputs = %q, err=%v", data, err)
			}
			if test.outcome == "operational_failure" {
				if localCode == 0 || localReport.Len() != 0 || !strings.Contains(string(data), "report_path=\n") {
					t.Fatalf("operational result: code=%d report=%q outputs=%q", localCode, localReport.String(), data)
				}
				return
			}
			if (localCode == 0) != (test.outcome == "pass") {
				t.Fatalf("local code = %d, outcome=%s", localCode, test.outcome)
			}
			var reportPath string
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "report_path=") {
					reportPath = strings.TrimPrefix(line, "report_path=")
				}
			}
			full, readErr := os.ReadFile(reportPath)
			if readErr != nil || !bytes.Equal(full, localReport.Bytes()) {
				t.Fatalf("Action/CLI report mismatch: err=%v action=%q local=%q", readErr, full, localReport.String())
			}
		})
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func containsPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
