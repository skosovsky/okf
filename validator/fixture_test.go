package validator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

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
}

type manifestExpected struct {
	ExitCode    *int                         `yaml:"exit_code"`
	Diagnostics []manifestExpectedDiagnostic `yaml:"diagnostics"`
}

type manifestExpectedDiagnostic struct {
	Severity        string `yaml:"severity"`
	MessageContains string `yaml:"message_contains"`
}

type expectedReport struct {
	ExitCode     int                  `json:"exit_code"`
	ScannedFiles int                  `json:"scanned_files"`
	Diagnostics  []expectedDiagnostic `json:"diagnostics"`
	Counts       expectedCounts       `json:"counts"`
}

type expectedDiagnostic struct {
	File     string `json:"file"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type expectedCounts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
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

			// Act.
			actual := normalizeReport(ValidatePath(bundlePath, &cfg))

			// Assert.
			if !reflect.DeepEqual(actual, expected) {
				actualJSON, _ := json.MarshalIndent(actual, "", "  ")
				expectedJSON, _ := json.MarshalIndent(expected, "", "  ")
				t.Fatalf("%s\n%s", manifest.Description, jsonLineDiff(string(expectedJSON), string(actualJSON)))
			}
		})
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

func normalizeReport(report Report) expectedReport {
	diagnostics := make([]expectedDiagnostic, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		diagnostics = append(diagnostics, expectedDiagnostic{
			File:     diagnostic.File,
			Severity: diagnostic.Severity.String(),
			Message:  diagnostic.Message,
		})
	}
	return expectedReport{
		ExitCode:     report.ExitCode(),
		ScannedFiles: report.ScannedFiles,
		Diagnostics:  diagnostics,
		Counts: expectedCounts{
			Errors:   report.ErrorCount(),
			Warnings: report.WarningCount(),
			Info:     report.InfoCount(),
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
