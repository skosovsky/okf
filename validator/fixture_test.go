package validator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type fixtureManifest struct {
	Description string           `yaml:"description"`
	Run         fixtureRun       `yaml:"run"`
	Expected    manifestExpected `yaml:"expected"`
}

type fixtureRun struct {
	Path         *string `yaml:"path"`
	Strict       *bool   `yaml:"strict"`
	CheckLinks   *bool   `yaml:"check_links"`
	CheckOrphans *bool   `yaml:"check_orphans"`
	Spec         *string `yaml:"spec"`
	AsOf         *string `yaml:"as_of"`
}

type manifestExpected struct {
	ExitCode    *int                         `yaml:"exit_code"`
	Diagnostics []manifestExpectedDiagnostic `yaml:"diagnostics"`
}

type manifestExpectedDiagnostic struct {
	Severity        string `yaml:"severity"`
	MessageContains string `yaml:"message_contains"`
	Code            string `yaml:"code"`
	SpecRef         string `yaml:"spec_ref"`
	FieldPath       string `yaml:"field_path"`
	PolicyFailure   *bool  `yaml:"policy_failure"`
}

type expectedReport struct {
	ExitCode     int                  `json:"exit_code"`
	ScannedFiles int                  `json:"scanned_files"`
	Diagnostics  []expectedDiagnostic `json:"diagnostics"`
	Counts       expectedCounts       `json:"counts"`
}

type expectedDiagnostic struct {
	Code          string `json:"code,omitempty"`
	SpecRef       string `json:"spec_ref,omitempty"`
	File          string `json:"file"`
	FieldPath     string `json:"field_path,omitempty"`
	Severity      string `json:"severity"`
	Message       string `json:"message"`
	PolicyFailure bool   `json:"policy_failure,omitempty"`
}

type expectedCounts struct {
	Errors         int `json:"errors"`
	Warnings       int `json:"warnings"`
	Info           int `json:"info"`
	PolicyFailures int `json:"policy_failures,omitempty"`
}

func TestValidationFixtures(t *testing.T) {
	root := filepath.Join("..", "fixtures")
	var manifests []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() == "manifest.yaml" {
			manifests = append(manifests, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(fixtures) error = %v", err)
	}
	if len(manifests) == 0 {
		t.Fatal("no validation fixtures found")
	}
	sort.Strings(manifests)

	for _, manifestPath := range manifests {
		manifestPath := manifestPath
		name, _ := filepath.Rel(root, filepath.Dir(manifestPath))
		t.Run(filepath.ToSlash(name), func(t *testing.T) {
			t.Parallel()

			// Arrange.
			manifest := readFixtureManifest(t, manifestPath)
			expected := readExpectedReport(t, filepath.Join(filepath.Dir(manifestPath), "expected.json"))
			assertManifestExpected(t, manifestPath, manifest, expected)
			bundlePath := filepath.Join(filepath.Dir(manifestPath), *manifest.Run.Path)
			cfg := ValidatorConfig{
				Strict:       *manifest.Run.Strict,
				CheckLinks:   *manifest.Run.CheckLinks,
				CheckOrphans: *manifest.Run.CheckOrphans,
			}
			if manifest.Run.Spec != nil {
				cfg.Spec = *manifest.Run.Spec
			} else if !strings.Contains(filepath.ToSlash(manifestPath), "/v02/") &&
				!strings.Contains(filepath.ToSlash(manifestPath), "/versioning/root-0.2/") {
				// The pre-v0.2 fixture corpus is the explicit legacy regression
				// profile. New fixtures declare their contract or use v0.2 auto.
				cfg.Spec = "0.1"
			}
			if manifest.Run.AsOf != nil {
				referenceDate, err := time.Parse(time.DateOnly, *manifest.Run.AsOf)
				if err != nil {
					t.Fatalf("run.as_of = %q: %v", *manifest.Run.AsOf, err)
				}
				cfg.ReferenceDate = referenceDate
			}

			// Act.
			structured := strings.Contains(filepath.ToSlash(manifestPath), "/v02/")
			report := ValidatePath(bundlePath, &cfg)
			actual := normalizeReport(report, structured)

			// Assert.
			assertDiagnosticMachineContract(t, report)
			if !reflect.DeepEqual(actual, expected) {
				actualJSON, _ := json.MarshalIndent(actual, "", "  ")
				expectedJSON, _ := json.MarshalIndent(expected, "", "  ")
				t.Fatalf("%s\n%s", manifest.Description, jsonLineDiff(string(expectedJSON), string(actualJSON)))
			}
		})
	}
}

func assertDiagnosticMachineContract(t *testing.T, report Report) {
	t.Helper()
	for i, diagnostic := range report.Diagnostics {
		if strings.TrimSpace(diagnostic.Code) == "" ||
			strings.TrimSpace(diagnostic.SpecRef) == "" ||
			strings.TrimSpace(diagnostic.FieldPath) == "" {
			t.Fatalf("diagnostic %d = %#v, want non-empty Code, SpecRef, and FieldPath", i, diagnostic)
		}
	}
}

func readFixtureManifest(t *testing.T, path string) fixtureManifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var manifest fixtureManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	assertManifestRun(t, path, manifest.Run)
	return manifest
}

func assertManifestRun(t *testing.T, path string, run fixtureRun) {
	t.Helper()
	if run.Path == nil {
		t.Fatalf("%s: manifest run.path is required", path)
	}
	if *run.Path == "" {
		t.Fatalf("%s: manifest run.path should not be empty", path)
	}
	if run.Strict == nil {
		t.Fatalf("%s: manifest run.strict is required", path)
	}
	if run.CheckLinks == nil {
		t.Fatalf("%s: manifest run.check_links is required", path)
	}
	if run.CheckOrphans == nil {
		t.Fatalf("%s: manifest run.check_orphans is required", path)
	}
}

func assertManifestExpected(t *testing.T, path string, manifest fixtureManifest, expected expectedReport) {
	t.Helper()
	if manifest.Expected.ExitCode == nil {
		t.Fatalf("%s: manifest expected.exit_code is required", path)
	}
	if got, want := *manifest.Expected.ExitCode, expected.ExitCode; got != want {
		t.Fatalf("%s: manifest expected.exit_code = %d, want %d", path, got, want)
	}
	if got, want := len(manifest.Expected.Diagnostics), len(expected.Diagnostics); got != want {
		t.Fatalf("%s: manifest expected diagnostics = %d, want %d", path, got, want)
	}
	for i, diagnostic := range manifest.Expected.Diagnostics {
		want := expected.Diagnostics[i]
		if diagnostic.Severity != want.Severity {
			t.Fatalf("%s: manifest diagnostic %d severity = %q, want %q", path, i, diagnostic.Severity, want.Severity)
		}
		if diagnostic.MessageContains == "" {
			t.Fatalf("%s: manifest diagnostic %d message_contains is required", path, i)
		}
		if !strings.Contains(want.Message, diagnostic.MessageContains) {
			t.Fatalf("%s: manifest diagnostic %d message_contains = %q does not match expected message %q", path, i, diagnostic.MessageContains, want.Message)
		}
		if diagnostic.Code != "" && diagnostic.Code != want.Code {
			t.Fatalf("%s: manifest diagnostic %d code = %q, want %q", path, i, diagnostic.Code, want.Code)
		}
		if diagnostic.FieldPath != "" && diagnostic.FieldPath != want.FieldPath {
			t.Fatalf("%s: manifest diagnostic %d field_path = %q, want %q", path, i, diagnostic.FieldPath, want.FieldPath)
		}
		if diagnostic.SpecRef != "" && diagnostic.SpecRef != want.SpecRef {
			t.Fatalf("%s: manifest diagnostic %d spec_ref = %q, want %q", path, i, diagnostic.SpecRef, want.SpecRef)
		}
		if diagnostic.PolicyFailure != nil && *diagnostic.PolicyFailure != want.PolicyFailure {
			t.Fatalf("%s: manifest diagnostic %d policy_failure = %v, want %v", path, i, *diagnostic.PolicyFailure, want.PolicyFailure)
		}
	}
}

func readExpectedReport(t *testing.T, path string) expectedReport {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var report expectedReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	return report
}

func normalizeReport(report Report, structured bool) expectedReport {
	diagnostics := make([]expectedDiagnostic, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		expected := expectedDiagnostic{
			File:     diagnostic.File,
			Severity: diagnostic.Severity.String(),
			Message:  diagnostic.Message,
		}
		if structured {
			expected.Code = diagnostic.Code
			expected.SpecRef = diagnostic.SpecRef
			expected.FieldPath = diagnostic.FieldPath
			expected.PolicyFailure = diagnostic.PolicyFailure
		}
		diagnostics = append(diagnostics, expected)
	}
	return expectedReport{
		ExitCode:     report.ExitCode(),
		ScannedFiles: report.ScannedFiles,
		Diagnostics:  diagnostics,
		Counts: expectedCounts{
			Errors:         report.ErrorCount(),
			Warnings:       report.WarningCount(),
			Info:           report.InfoCount(),
			PolicyFailures: report.PolicyFailureCount(),
		},
	}
}

func jsonLineDiff(expected, actual string) string {
	if expected == actual {
		return "diff (-expected +actual):\n"
	}
	expectedLines := strings.Split(expected, "\n")
	actualLines := strings.Split(actual, "\n")
	maxLen := len(expectedLines)
	if len(actualLines) > maxLen {
		maxLen = len(actualLines)
	}

	var out strings.Builder
	out.WriteString("diff (-expected +actual):\n")
	for i := 0; i < maxLen; i++ {
		var expectedLine, actualLine string
		if i < len(expectedLines) {
			expectedLine = expectedLines[i]
		}
		if i < len(actualLines) {
			actualLine = actualLines[i]
		}
		if expectedLine == actualLine {
			continue
		}
		if i < len(expectedLines) {
			out.WriteString("- ")
			out.WriteString(expectedLine)
			out.WriteByte('\n')
		}
		if i < len(actualLines) {
			out.WriteString("+ ")
			out.WriteString(actualLine)
			out.WriteByte('\n')
		}
	}
	return out.String()
}
