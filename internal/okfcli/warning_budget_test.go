package okfcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type warningBudgetJSON struct {
	Conformant     bool `json:"conformant"`
	PolicyFailures int  `json:"policy_failures"`
	Errors         int  `json:"errors"`
	Warnings       int  `json:"warnings"`
	Diagnostics    []struct {
		Code          string `json:"code"`
		PolicyFailure bool   `json:"policy_failure"`
	} `json:"diagnostics"`
}

func warningBundle(t *testing.T, concept string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"index.md":   "---\nokf_version: \"0.2\"\n---\n# Notes\n\n* [Concept](concept.md)\n",
		"concept.md": concept,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestValidateWarningBudgetBoundary(t *testing.T) {
	// Arrange.
	root := warningBundle(t, "---\ntype: Note\nstatus: unexpected\ntags: nope\n---\nBody.\n")
	for _, test := range []struct {
		name, budget                       string
		wantCode, wantWarnings, wantPolicy int
	}{
		{name: "zero", budget: "0", wantCode: 1, wantWarnings: 2, wantPolicy: 1},
		{name: "N minus one", budget: "1", wantCode: 1, wantWarnings: 2, wantPolicy: 1},
		{name: "N", budget: "2", wantCode: 0, wantWarnings: 2},
		{name: "N plus one", budget: "3", wantCode: 0, wantWarnings: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			var stdout, stderr bytes.Buffer
			code := Run([]string{"validate", "--path", root, "--strict", "--max-warnings", test.budget, "--format", "json"}, &stdout, &stderr)
			var report warningBudgetJSON
			decodeErr := json.Unmarshal(stdout.Bytes(), &report)

			// Assert.
			if code != test.wantCode || stderr.Len() != 0 || decodeErr != nil {
				t.Fatalf("code/stderr/decode = %d/%q/%v, report=%q", code, stderr.String(), decodeErr, stdout.String())
			}
			if !report.Conformant || report.Errors != 0 || report.Warnings != test.wantWarnings || report.PolicyFailures != test.wantPolicy {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

func TestValidateWarningBudgetBaseAndPolicyRemainDistinct(t *testing.T) {
	// Arrange.
	root := warningBundle(t, "---\ntype: [\n---\nBody.\n")

	// Act.
	var stdout, stderr bytes.Buffer
	code := Run([]string{"validate", "--path", root, "--max-warnings", "0", "--format", "json"}, &stdout, &stderr)
	var report warningBudgetJSON
	decodeErr := json.Unmarshal(stdout.Bytes(), &report)

	// Assert.
	if code != 1 || stderr.Len() != 0 || decodeErr != nil || report.Conformant || report.Errors == 0 || report.PolicyFailures != 0 {
		t.Fatalf("code/stderr/decode/report = %d/%q/%v/%+v", code, stderr.String(), decodeErr, report)
	}
}

func TestValidateWarningBudgetCoexistsWithBaseError(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	for name, body := range map[string]string{
		"index.md":   "---\nokf_version: \"0.2\"\n---\n# Notes\n\n* [Broken](broken.md)\n* [Warning](warning.md)\n",
		"broken.md":  "---\ntype: [\n---\nBody.\n",
		"warning.md": "---\ntype: Note\nstatus: unexpected\n---\nBody.\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// Act.
	var stdout, stderr bytes.Buffer
	code := Run([]string{"validate", "--path", root, "--strict", "--max-warnings", "0", "--format", "json"}, &stdout, &stderr)
	var report warningBudgetJSON
	decodeErr := json.Unmarshal(stdout.Bytes(), &report)

	// Assert.
	if code != 1 || stderr.Len() != 0 || decodeErr != nil || report.Conformant || report.Errors == 0 || report.Warnings != 1 || report.PolicyFailures != 1 {
		t.Fatalf("code/stderr/decode/report = %d/%q/%v/%+v", code, stderr.String(), decodeErr, report)
	}
	policyCode := false
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "warning_budget_exceeded" && diagnostic.PolicyFailure {
			policyCode = true
		}
	}
	if !policyCode {
		t.Fatalf("missing separate policy finding: %+v", report.Diagnostics)
	}
}

func TestValidateWarningBudgetRejectsMalformedInputBeforeLoading(t *testing.T) {
	// Arrange.
	for _, raw := range []string{"-1", "nope", "1.5", "18446744073709551616"} {
		// Act.
		var stdout, stderr bytes.Buffer
		code := Run([]string{"validate", "--path", "missing", "--max-warnings=" + raw, "--format", "json"}, &stdout, &stderr)

		// Assert.
		if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "--max-warnings") {
			t.Fatalf("input %q: code/stdout/stderr = %d/%q/%q", raw, code, stdout.String(), stderr.String())
		}
	}
}
