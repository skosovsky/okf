// Command okf-action adapts the Go validator CLI to GitHub Actions outputs.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/skosovsky/okf/internal/okfcli"
)

const inlineReportLimit = 60000

type runner func([]string, io.Writer, io.Writer) int

type inputs struct {
	path, spec, temporalProfile, asOf, maxWarnings string
	strict, checkLinks, checkOrphans               bool
}

func main() {
	if err := run(os.Getenv, okfcli.Run, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "okf-action:", err)
		os.Exit(1)
	}
}

func run(getenv func(string) string, validate runner, stderr io.Writer) error {
	values, err := readInputs(getenv)
	if err != nil {
		return recordOperationalFailure(getenv, err)
	}
	args := []string{"validate", "--path", values.path, "--spec", values.spec, "--format", "json"}
	if values.temporalProfile != "" {
		args = append(args, "--temporal-profile", values.temporalProfile)
	}
	if values.strict {
		args = append(args, "--strict")
	}
	if values.checkLinks {
		args = append(args, "--check-links")
	}
	if values.checkOrphans {
		args = append(args, "--check-orphans")
	}
	if values.asOf != "" {
		args = append(args, "--as-of", values.asOf)
	}
	if values.maxWarnings != "" {
		args = append(args, "--max-warnings", values.maxWarnings)
	}

	var output, errorsOutput bytes.Buffer
	code := validate(args, &output, &errorsOutput)
	if errorsOutput.Len() != 0 {
		_, _ = io.Copy(stderr, &errorsOutput)
	}
	var report struct {
		Conformant     *bool             `json:"conformant"`
		PolicyFailures *int              `json:"policy_failures"`
		Diagnostics    []json.RawMessage `json:"diagnostics"`
	}
	validReport := json.Unmarshal(output.Bytes(), &report) == nil && report.Conformant != nil && report.PolicyFailures != nil && report.Diagnostics != nil
	if !validReport || code != 0 && code != 1 {
		return recordOperationalFailure(getenv, fmt.Errorf("validator did not produce a complete JSON report (exit %d)", code))
	}
	if (code == 0) != (*report.Conformant && *report.PolicyFailures == 0) {
		return recordOperationalFailure(getenv, errors.New("validator exit code and JSON outcome disagree"))
	}
	outcome := "pass"
	if code != 0 {
		outcome = "validation_failure"
	}
	if err := writeOutputs(getenv, outcome, output.Bytes()); err != nil {
		return err
	}
	if code != 0 {
		return errors.New("bundle validation failed; see report and report-path outputs")
	}
	return nil
}

func readInputs(getenv func(string) string) (inputs, error) {
	values := inputs{
		path: getenv("OKF_ACTION_INPUT_PATH"), spec: getenv("OKF_ACTION_INPUT_SPEC"), temporalProfile: getenv("OKF_ACTION_INPUT_TEMPORAL_PROFILE"),
		asOf: getenv("OKF_ACTION_INPUT_AS_OF"), maxWarnings: getenv("OKF_ACTION_INPUT_MAX_WARNINGS"),
	}
	if values.path == "" {
		values.path = "."
	}
	if values.spec == "" {
		values.spec = "auto"
	}
	workspace := getenv("GITHUB_WORKSPACE")
	if workspace == "" {
		return inputs{}, errors.New("GITHUB_WORKSPACE is empty")
	}
	if !filepath.IsAbs(values.path) {
		values.path = filepath.Join(workspace, values.path)
	}
	for _, item := range []struct {
		key    string
		target *bool
	}{
		{"OKF_ACTION_INPUT_STRICT", &values.strict},
		{"OKF_ACTION_INPUT_CHECK_LINKS", &values.checkLinks},
		{"OKF_ACTION_INPUT_CHECK_ORPHANS", &values.checkOrphans},
	} {
		raw := getenv(item.key)
		if raw == "" || raw == "false" {
			continue
		}
		if raw != "true" {
			return inputs{}, fmt.Errorf("%s must be true or false", item.key)
		}
		*item.target = true
	}
	if values.maxWarnings != "" {
		budget, err := strconv.ParseUint(values.maxWarnings, 10, 64)
		if err != nil || budget > uint64(int(^uint(0)>>1)) {
			return inputs{}, errors.New("max-warnings must be a nonnegative integer")
		}
	}
	return values, nil
}

func recordOperationalFailure(getenv func(string) string, cause error) error {
	if err := writeOutputs(getenv, "operational_failure", nil); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func writeOutputs(getenv func(string) string, outcome string, report []byte) error {
	outputPath := getenv("GITHUB_OUTPUT")
	tempDir := getenv("RUNNER_TEMP")
	if outputPath == "" || tempDir == "" {
		return errors.New("GITHUB_OUTPUT and RUNNER_TEMP are required")
	}
	reportPath := ""
	if len(report) != 0 {
		file, err := os.CreateTemp(tempDir, "okf-validation-*.json")
		if err != nil {
			return fmt.Errorf("create report file: %w", err)
		}
		reportPath = file.Name()
		if _, err := file.Write(report); err != nil {
			file.Close()
			return fmt.Errorf("write report file: %w", err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close report file: %w", err)
		}
	}
	var lines strings.Builder
	fmt.Fprintf(&lines, "outcome=%s\nreport_path=%s\n", outcome, reportPath)
	if len(report) <= inlineReportLimit && len(report) != 0 {
		// JSON is one line from the CLI encoder. Validate it again above, so it
		// cannot forge a new GitHub output line or delimiter.
		fmt.Fprintf(&lines, "report=%s\n", strings.TrimSuffix(string(report), "\n"))
	} else {
		lines.WriteString("report=\n")
	}
	file, err := os.OpenFile(outputPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}
	if _, err := io.WriteString(file, lines.String()); err != nil {
		file.Close()
		return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
	}
	return file.Close()
}
