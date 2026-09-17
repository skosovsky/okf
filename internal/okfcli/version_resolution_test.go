package okfcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestVersionJSONContract(t *testing.T) {
	// Arrange.
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"version", "--json"}, &stdout, &stderr)

	// Assert.
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run(version --json) code/stderr = %d/%q, want 0/empty", code, stderr.String())
	}
	var got VersionInfo
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, stdout.String())
	}
	want := VersionInfo{
		CLIVersion:       cliVersion(),
		OKFSpecDefault:   "0.2",
		OKFSpecSupported: []string{"0.1", "0.2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("version JSON = %#v, want %#v", got, want)
	}
}

func TestStandaloneConceptLocalVersionIsPreservedButNeverDeclaresContract(t *testing.T) {
	declarations := []struct {
		name  string
		value string
	}{
		{name: "valid v0.1", value: "okf_version: \"0.1\"\n"},
		{name: "valid v0.2", value: "okf_version: \"0.2\"\n"},
		{name: "future", value: "okf_version: \"9.9\"\n"},
		{name: "numeric", value: "okf_version: 17\n"},
		{name: "sequence", value: "okf_version: [0.2]\n"},
		{name: "mapping", value: "okf_version: {major: 0, minor: 2}\n"},
		{name: "blank string", value: "okf_version: \"\"\n"},
		{name: "malformed string", value: "okf_version: \"v0.2\"\n"},
		{
			name:  "terminal alias",
			value: "declared: &declared [0.2]\nokf_version: *declared\n",
		},
		{
			name: "merged declaration",
			value: "defaults: &defaults\n" +
				"  okf_version: {major: 0, minor: 2}\n" +
				"<<: *defaults\n",
		},
	}
	selectors := []string{"auto", "0.1", "0.2"}

	for _, declaration := range declarations {
		for _, selector := range selectors {
			t.Run(declaration.name+"/"+selector, func(t *testing.T) {
				// Arrange.
				path := filepath.Join(t.TempDir(), "concept.md")
				original := "---\n" + declaration.value + "type: Knowledge\n---\n\nBody.\n"
				writeFixtureFile(t, path, original)
				var parseStdout, parseStderr bytes.Buffer
				var fmtStdout, fmtStderr bytes.Buffer
				var writeStdout, writeStderr bytes.Buffer

				// Act.
				parseCode := Run([]string{"parse", path, "--spec", selector, "--json"}, &parseStdout, &parseStderr)
				fmtCode := Run([]string{"fmt", path, "--spec", selector}, &fmtStdout, &fmtStderr)
				writeCode := Run([]string{"fmt", path, "--spec", selector, "--write"}, &writeStdout, &writeStderr)
				after, readErr := os.ReadFile(path)
				var response parseResponse
				decodeErr := json.Unmarshal(parseStdout.Bytes(), &response)

				// Assert.
				wantEffective := selector
				wantSource := "explicit"
				wantCompatibility := "native"
				if selector == "auto" {
					wantEffective = "0.2"
					wantSource = "default"
				} else if selector == "0.1" {
					wantCompatibility = "legacy"
				}
				if parseCode != 0 || parseStderr.Len() != 0 || decodeErr != nil ||
					response.Declared != "" || response.Effective != wantEffective ||
					response.Source != wantSource || response.Compatibility != wantCompatibility {
					t.Fatalf("parse code/stderr/decode/version = %d/%q/%v/%#v",
						parseCode, parseStderr.String(), decodeErr, response.VersionDTO)
				}
				if fmtCode != 0 || fmtStderr.Len() != 0 || fmtStdout.String() != original {
					t.Fatalf("fmt code/stdout/stderr = %d/%q/%q, want 0/exact/empty",
						fmtCode, fmtStdout.String(), fmtStderr.String())
				}
				if writeCode != 0 || writeStderr.Len() != 0 || writeStdout.Len() == 0 ||
					readErr != nil || !bytes.Equal(after, []byte(original)) {
					t.Fatalf("fmt --write code/stdout/stderr/read/unchanged = %d/%q/%q/%v/%t",
						writeCode, writeStdout.String(), writeStderr.String(), readErr,
						bytes.Equal(after, []byte(original)))
				}
			})
		}
	}
}

func TestParseAndFmtResolveVersionFromDeclaringBundleRoot(t *testing.T) {
	tests := []struct {
		name          string
		declaration   string
		nestedVersion string
		wantEffective string
		wantSource    string
	}{
		{
			name:          "declared v0.1 through undeclared nested index",
			declaration:   "okf_version: \"0.1\"\n",
			wantEffective: "0.1",
			wantSource:    "declared",
		},
		{
			name:          "declared v0.2 through undeclared nested index",
			declaration:   "okf_version: \"0.2\"\n",
			wantEffective: "0.2",
			wantSource:    "declared",
		},
		{
			name:          "undeclared root defaults",
			wantEffective: "0.2",
			wantSource:    "default",
		},
		{
			name:          "outer root dominates inert nested declaration",
			declaration:   "okf_version: \"0.2\"\n",
			nestedVersion: "okf_version: \"0.1\"\n",
			wantEffective: "0.2",
			wantSource:    "declared",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			nested := filepath.Join(root, "nested")
			if err := os.Mkdir(nested, 0o755); err != nil {
				t.Fatalf("os.Mkdir() error = %v", err)
			}
			indexFrontmatter := ""
			if test.declaration != "" {
				indexFrontmatter = "---\n" + test.declaration + "---\n\n"
			}
			writeFixtureFile(t, filepath.Join(root, "index.md"), indexFrontmatter+"# Root\n")
			nestedFrontmatter := ""
			if test.nestedVersion != "" {
				nestedFrontmatter = "---\n" + test.nestedVersion + "---\n\n"
			}
			writeFixtureFile(t, filepath.Join(nested, "index.md"), nestedFrontmatter+"# Nested\n")
			path := filepath.Join(nested, "concept.md")
			original := "---\ntype: Knowledge\n---\n\nBody.\n"
			writeFixtureFile(t, path, original)
			var parseStdout, parseStderr bytes.Buffer
			var fmtStdout, fmtStderr bytes.Buffer

			// Act.
			parseCode := Run([]string{"parse", path, "--json"}, &parseStdout, &parseStderr)
			fmtCode := Run([]string{"fmt", path}, &fmtStdout, &fmtStderr)
			var response parseResponse
			decodeErr := json.Unmarshal(parseStdout.Bytes(), &response)

			// Assert.
			if parseCode != 0 || fmtCode != 0 ||
				parseStderr.Len() != 0 || fmtStderr.Len() != 0 ||
				decodeErr != nil {
				t.Fatalf("parse/fmt code/stderr/decode = %d/%d/%q/%q/%v",
					parseCode, fmtCode, parseStderr.String(), fmtStderr.String(), decodeErr)
			}
			wantDeclared := strings.TrimSuffix(strings.TrimPrefix(test.declaration, "okf_version: \""), "\"\n")
			if test.declaration == "" {
				wantDeclared = ""
			}
			if response.Declared != wantDeclared ||
				response.Effective != test.wantEffective ||
				response.Source != test.wantSource {
				t.Fatalf("version = %#v, want declared/effective/source %q/%q/%q",
					response.VersionDTO, wantDeclared, test.wantEffective, test.wantSource)
			}
			if fmtStdout.String() != original {
				t.Fatalf("fmt output = %q, want exact original", fmtStdout.String())
			}
		})
	}
}

func TestParseAndFmtRejectRootVersionConflictForNestedConceptWithoutWrite(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	writeFixtureFile(t, filepath.Join(root, "index.md"),
		"---\nokf_version: \"0.1\"\n---\n\n# Root\n")
	writeFixtureFile(t, filepath.Join(nested, "index.md"), "# Nested\n")
	path := filepath.Join(nested, "concept.md")
	original := "---\ntype: Knowledge\n---\n\nBody.\n"
	writeFixtureFile(t, path, original)
	commands := [][]string{
		{"parse", path, "--spec", "0.2", "--json"},
		{"fmt", path, "--spec", "0.2", "--write"},
	}

	for _, command := range commands {
		// Act.
		var stdout, stderr bytes.Buffer
		code := Run(command, &stdout, &stderr)
		after, readErr := os.ReadFile(path)

		// Assert.
		if code != 1 || stdout.Len() != 0 ||
			!strings.Contains(stderr.String(), `declared \"0.1\", selected \"0.2\"`) {
			t.Fatalf("Run(%v) code/stdout/stderr = %d/%q/%q",
				command, code, stdout.String(), stderr.String())
		}
		if readErr != nil || string(after) != original {
			t.Fatalf("Run(%v) read/unchanged = %v/%t", command, readErr, string(after) == original)
		}
	}
}

func TestParseAndFmtRejectSymlinkDocumentScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions are platform-specific")
	}

	// Arrange.
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	writeFixtureFile(t, filepath.Join(real, "index.md"),
		"---\nokf_version: \"0.2\"\n---\n\n# Root\n")
	target := filepath.Join(real, "concept.md")
	writeFixtureFile(t, target, "---\ntype: Knowledge\n---\n\nBody.\n")
	fileLink := filepath.Join(real, "concept-link.md")
	if err := os.Symlink(target, fileLink); err != nil {
		t.Skipf("os.Symlink(file) error = %v", err)
	}
	directoryLink := filepath.Join(root, "bundle-link")
	if err := os.Symlink(real, directoryLink); err != nil {
		t.Skipf("os.Symlink(directory) error = %v", err)
	}
	paths := []string{fileLink, filepath.Join(directoryLink, "concept.md")}

	for _, path := range paths {
		for _, command := range []string{"parse", "fmt"} {
			// Act.
			var stdout, stderr bytes.Buffer
			code := Run([]string{command, path, "--spec", "0.2"}, &stdout, &stderr)

			// Assert.
			if code != 1 || stdout.Len() != 0 ||
				!strings.Contains(stderr.String(), "symlink") &&
					!strings.Contains(stderr.String(), bundle.ErrNotRegularFile.Error()) &&
					!strings.Contains(stderr.String(), bundle.ErrNotDirectory.Error()) {
				t.Fatalf("%s(%q) code/stdout/stderr = %d/%q/%q",
					command, path, code, stdout.String(), stderr.String())
			}
		}
	}
}

func TestMalformedRootIndexVersionFailsClosedAcrossStandaloneAndIndexCommands(t *testing.T) {
	declarations := []struct {
		name  string
		value string
	}{
		{name: "numeric", value: "okf_version: 17\n"},
		{name: "sequence", value: "okf_version: [0.2]\n"},
		{name: "mapping", value: "okf_version: {major: 0, minor: 2}\n"},
		{name: "blank string", value: "okf_version: \"\"\n"},
		{name: "malformed string", value: "okf_version: \"v0.2\"\n"},
	}
	for _, declaration := range declarations {
		t.Run(declaration.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			indexPath := filepath.Join(root, "index.md")
			conceptPath := filepath.Join(root, "nested", "concept.md")
			if err := os.Mkdir(filepath.Dir(conceptPath), 0o755); err != nil {
				t.Fatalf("os.Mkdir() error = %v", err)
			}
			writeFixtureFile(t, indexPath, "---\n"+declaration.value+"---\n# Root before.\n")
			writeFixtureFile(t, conceptPath, "---\ntype: Note\n---\n\nBody.\n")
			before, _ := os.ReadFile(indexPath)
			conceptBefore, _ := os.ReadFile(conceptPath)
			commands := [][]string{
				{"parse", indexPath, "--spec", "0.2", "--json"},
				{"fmt", indexPath, "--spec", "0.2", "--write"},
				{"parse", conceptPath, "--spec", "0.2", "--json"},
				{"fmt", conceptPath, "--spec", "0.2", "--write"},
				{"index", root, "--spec", "0.2"},
			}

			for _, command := range commands {
				// Act.
				var stdout, stderr bytes.Buffer
				code := Run(command, &stdout, &stderr)
				after, readErr := os.ReadFile(indexPath)
				conceptAfter, conceptReadErr := os.ReadFile(conceptPath)

				// Assert.
				if code != 1 || stdout.Len() != 0 ||
					!strings.Contains(stderr.String(), bundle.ErrInvalidVersionDeclaration.Error()) {
					t.Fatalf("Run(%v) code/stdout/stderr = %d/%q/%q",
						command, code, stdout.String(), stderr.String())
				}
				if readErr != nil || conceptReadErr != nil ||
					!bytes.Equal(before, after) || !bytes.Equal(conceptBefore, conceptAfter) {
					t.Fatalf("Run(%v) read/unchanged = %v/%v/%t/%t",
						command,
						readErr,
						conceptReadErr,
						bytes.Equal(before, after),
						bytes.Equal(conceptBefore, conceptAfter))
				}
			}
		})
	}
}

func TestInfoRejectsExplicitVersionConflictWithoutPayload(t *testing.T) {
	// Arrange.
	root := fixturePath(t, "compat", "declared-v01")
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"info", "--spec", "0.2", root, "--json"}, &stdout, &stderr)

	// Assert.
	if code != 1 {
		t.Fatalf("Run(info conflict) code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.HasPrefix(stderr.String(), "error: ") || !strings.Contains(stderr.String(), `declared \"0.1\", selected \"0.2\"`) {
		t.Fatalf("stderr = %q, want version assertion error", stderr.String())
	}
}

func TestValidateReportsSelectorMismatchAsPolicyFailure(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"), "---\nokf_version: \"0.1\"\n---\n# Concepts\n\n* [Note](note.md)\n")
	writeFixtureFile(t, filepath.Join(root, "note.md"), "---\ntype: Note\n---\nBody.\n")
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"validate", "--path", root, "--spec", "0.2", "--json"}, &stdout, &stderr)

	// Assert.
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("Run(validate selector mismatch) code/stderr = %d/%q, want 1/empty", code, stderr.String())
	}
	var response struct {
		Conformant     bool                       `json:"conformant"`
		PolicyFailures int                        `json:"policy_failures"`
		Errors         int                        `json:"errors"`
		Diagnostics    []ValidationJSONDiagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, stdout.String())
	}
	if !response.Conformant || response.Errors != 0 || response.PolicyFailures != 1 {
		t.Fatalf("validation outcome = conformant:%t errors:%d policy_failures:%d, want true/0/1",
			response.Conformant, response.Errors, response.PolicyFailures)
	}
	if len(response.Diagnostics) == 0 || !response.Diagnostics[0].PolicyFailure {
		t.Fatalf("diagnostics = %#v, want selector policy failure", response.Diagnostics)
	}
}

func TestInfoEvaluatesInclusiveStaleBoundary(t *testing.T) {
	// Arrange.
	root := fixturePath(t, "positive", "appendix-a")
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"info", root, "--as-of", "2026-06-15", "--json"}, &stdout, &stderr)

	// Assert.
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run(info) code/stderr = %d/%q", code, stderr.String())
	}
	var got infoResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, stdout.String())
	}
	if got.AsOf != "2026-06-15" || got.Stale != 1 {
		t.Fatalf("as_of/stale = %q/%d, want 2026-06-15/1", got.AsOf, got.Stale)
	}
}
