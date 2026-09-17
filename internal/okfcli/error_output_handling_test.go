package okfcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/validator"
)

func TestRunRendersEveryUsageErrorAsOneCanonicalEscapedStderrLine(t *testing.T) {
	// Arrange.
	const hostile = "Ω\n\r\x00\x01\t\x1f\x7f\u2028\u2029end"
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{name: "missing command", message: "missing command"},
		{name: "unknown command", args: []string{hostile}, message: "unknown subcommand: " + hostile},
		{name: "unknown flag", args: []string{"help", "--bad" + hostile}, message: "unknown flag: --bad" + hostile},
		{name: "help argument", args: []string{"help", hostile}, message: "unexpected help argument: " + hostile},
		{name: "version argument", args: []string{"version", hostile}, message: "unexpected version argument: " + hostile},
		{name: "validate argument", args: []string{"validate", hostile}, message: "unexpected validate argument: " + hostile},
		{name: "info argument", args: []string{"info", "bundle", hostile}, message: "unexpected info argument: " + hostile},
		{name: "index argument", args: []string{"index", "bundle", hostile}, message: "unexpected index argument: " + hostile},
		{name: "graph argument", args: []string{"graph", "bundle", hostile}, message: "unexpected graph argument: " + hostile},
		{name: "parse argument", args: []string{"parse", "file", hostile}, message: "unexpected parse argument: " + hostile},
		{name: "fmt argument", args: []string{"fmt", "file", hostile}, message: "unexpected fmt argument: " + hostile},
		{
			name:    "migrate argument",
			args:    []string{"migrate", "bundle", hostile, "--to", "0.2"},
			message: "unexpected migrate argument: " + hostile,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run(test.args, &stdout, &stderr)

			// Assert.
			if code != 1 || stdout.Len() != 0 {
				t.Fatalf("Run(%q) code/stdout = %d/%q, want 1/empty", test.args, code, stdout.String())
			}
			assertCanonicalCLIError(t, stderr.String(), test.message)
		})
	}
}

func TestRunRendersOperationalPathErrorAsOneCanonicalEscapedStderrLine(t *testing.T) {
	// Arrange.
	const hostile = "Ω\n\r\x00\x01\t\x1f\x7f\u2028\u2029end"
	path := filepath.Join(t.TempDir(), hostile)
	_, wantErr := cmdParse([]string{path}, io.Discard)
	if wantErr == nil {
		t.Fatal("cmdParse(missing hostile path) error = nil")
	}
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"parse", path}, &stdout, &stderr)

	// Assert.
	if code != 1 || stdout.Len() != 0 {
		t.Fatalf("Run(parse missing hostile path) code/stdout = %d/%q, want 1/empty",
			code, stdout.String())
	}
	assertCanonicalCLIError(t, stderr.String(), wantErr.Error())
}

func TestRunRendersStdoutWriterErrorAsOneCanonicalEscapedStderrLine(t *testing.T) {
	// Arrange.
	const hostile = "Ω\n\r\x00\x01\t\x1f\x7f\u2028\u2029end"
	writer := &scriptedWriter{err: errors.New(hostile)}
	var stderr bytes.Buffer

	// Act.
	code := Run([]string{"version"}, writer, &stderr)

	// Assert.
	if code != 1 {
		t.Fatalf("Run(version with failing stdout) code = %d, want 1", code)
	}
	assertCanonicalCLIError(t, stderr.String(), "write stdout: "+hostile)
}

func TestRunIndexPublicationConflictIsSingleLineAndDoesNotRetry(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"),
		"---\nokf_version: \"0.2\"\n---\n\n# Before\n")
	before := snapshotFilesystemTree(t, root)
	conflict := fmt.Errorf(
		"regenerate %q:\r\nowner\tchanged: %w",
		root,
		bundle.ErrPublicationOwnershipConflict,
	)
	if !errors.Is(conflict, bundle.ErrPublicationOwnershipConflict) {
		t.Fatalf("conflict does not wrap ErrPublicationOwnershipConflict: %v", conflict)
	}
	calls := 0
	regenerate := func(gotRoot, gotSelector string) ([]string, error) {
		calls++
		if gotRoot != root || gotSelector != "0.2" {
			t.Fatalf("regenerator root/selector = %q/%q, want %q/0.2",
				gotRoot, gotSelector, root)
		}
		return nil, conflict
	}
	var stdout, stderr bytes.Buffer

	// Act.
	code := runWithIndexRegenerator(
		[]string{"index", root, "--spec", "0.2"},
		&stdout,
		&stderr,
		regenerate,
	)
	after := snapshotFilesystemTree(t, root)

	// Assert.
	if code != 1 || stdout.Len() != 0 || calls != 1 {
		t.Fatalf("run conflict code/stdout/calls = %d/%q/%d, want 1/empty/1",
			code, stdout.String(), calls)
	}
	assertCanonicalCLIError(t, stderr.String(), conflict.Error())
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("conflict changed bundle tree: before=%#v after=%#v", before, after)
	}
	for path := range after {
		if strings.HasPrefix(filepath.Base(path), ".okf-index-txn-") {
			t.Fatalf("conflict left index transaction artifact: %s", path)
		}
	}
}

func TestRunValidateUsageErrorsPrecedeBundleDependencies(t *testing.T) {
	rootParent := t.TempDir()
	missingRoot := filepath.Join(rootParent, "missing")
	unreadableRoot := filepath.Join(rootParent, "unreadable")
	if err := os.Mkdir(unreadableRoot, 0o700); err != nil {
		t.Fatalf("os.Mkdir(unreadable root) error = %v", err)
	}
	writeFixtureFile(t, filepath.Join(unreadableRoot, "secret.md"), "secret\n")
	if err := os.Chmod(unreadableRoot, 0); err != nil {
		t.Fatalf("os.Chmod(unreadable root) error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(unreadableRoot, 0o700); err != nil {
			t.Errorf("restore unreadable root mode: %v", err)
		}
	})
	expensiveRoot := filepath.Join(rootParent, "expensive")
	if err := os.Mkdir(expensiveRoot, 0o700); err != nil {
		t.Fatalf("os.Mkdir(expensive root) error = %v", err)
	}
	for index := range 64 {
		writeFixtureFile(
			t,
			filepath.Join(expensiveRoot, fmt.Sprintf("%03d.md", index)),
			"---\ntype: Note\n---\nBody.\n",
		)
	}

	tests := []struct {
		name      string
		root      string
		formatArg []string
		wantError string
		loadError error
	}{
		{
			name:      "invalid format precedes missing root",
			root:      missingRoot,
			formatArg: []string{"--format", "yaml"},
			wantError: "unsupported validate format: yaml",
			loadError: errors.New("missing root"),
		},
		{
			name:      "json conflict precedes unreadable root",
			root:      unreadableRoot,
			formatArg: []string{"--json", "--format", "text"},
			wantError: "cannot use --json with --format=text",
			loadError: errors.New("unreadable root"),
		},
		{
			name:      "invalid format bypasses expensive root",
			root:      expensiveRoot,
			formatArg: []string{"--format=xml"},
			wantError: "unsupported validate format: xml",
			loadError: errors.New("expensive root load should not run"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			loadCalls := 0
			validateCalls := 0
			dependencies := productionRunDependencies()
			dependencies.loadBundle = func(gotRoot string) (*bundle.Bundle, error) {
				loadCalls++
				if gotRoot != test.root {
					t.Fatalf("load root = %q, want %q", gotRoot, test.root)
				}
				return nil, test.loadError
			}
			dependencies.validateBundle = func(
				context.Context,
				*bundle.Bundle,
				*validator.ValidatorConfig,
			) (validator.Report, error) {
				validateCalls++
				return validator.Report{}, nil
			}
			args := append([]string{"validate", "--path", test.root}, test.formatArg...)
			var stdout, stderr bytes.Buffer

			// Act.
			code := runWithDependencies(args, &stdout, &stderr, dependencies)

			// Assert.
			if code != 1 || stdout.Len() != 0 ||
				loadCalls != 0 || validateCalls != 0 {
				t.Fatalf(
					"validate usage error code/stdout/load/validate = %d/%q/%d/%d",
					code,
					stdout.String(),
					loadCalls,
					validateCalls,
				)
			}
			assertCanonicalCLIError(t, stderr.String(), test.wantError)
		})
	}
}

func TestSeparatedStringFlagsRejectDashLeadingValuesBeforeIO(t *testing.T) {
	// Arrange.
	parent := t.TempDir()
	unavailableRoot := filepath.Join(parent, "unavailable-bundle")
	unavailableFile := filepath.Join(parent, "unavailable.md")
	tests := []struct {
		name   string
		prefix []string
		flag   string
	}{
		{name: "validate path", prefix: []string{"validate"}, flag: "--path"},
		{name: "validate path alias", prefix: []string{"validate"}, flag: "-path"},
		{name: "validate format", prefix: []string{"validate", "--path=" + unavailableRoot}, flag: "--format"},
		{name: "validate format alias", prefix: []string{"validate", "-path=" + unavailableRoot}, flag: "-format"},
		{name: "validate spec", prefix: []string{"validate", "--path=" + unavailableRoot}, flag: "--spec"},
		{name: "validate as-of", prefix: []string{"validate", "--path=" + unavailableRoot}, flag: "--as-of"},
		{name: "info format", prefix: []string{"info", unavailableRoot}, flag: "--format"},
		{name: "info spec", prefix: []string{"info", unavailableRoot}, flag: "--spec"},
		{name: "info as-of", prefix: []string{"info", unavailableRoot}, flag: "--as-of"},
		{name: "index spec", prefix: []string{"index", unavailableRoot}, flag: "--spec"},
		{name: "graph format", prefix: []string{"graph", unavailableRoot}, flag: "--format"},
		{name: "graph profile", prefix: []string{"graph", unavailableRoot}, flag: "--profile"},
		{name: "graph spec", prefix: []string{"graph", unavailableRoot}, flag: "--spec"},
		{name: "graph as-of", prefix: []string{"graph", unavailableRoot}, flag: "--as-of"},
		{name: "graph extension-relations", prefix: []string{"graph", unavailableRoot}, flag: "--extension-relations"},
		{name: "parse format", prefix: []string{"parse", unavailableFile}, flag: "--format"},
		{name: "parse spec", prefix: []string{"parse", unavailableFile}, flag: "--spec"},
		{name: "parse as-of", prefix: []string{"parse", unavailableFile}, flag: "--as-of"},
		{name: "fmt spec", prefix: []string{"fmt", unavailableFile}, flag: "--spec"},
		{name: "migrate from", prefix: []string{"migrate", unavailableRoot, "--to=0.2"}, flag: "--from"},
		{name: "migrate to", prefix: []string{"migrate", unavailableRoot}, flag: "--to"},
		{name: "migrate actor", prefix: []string{"migrate", unavailableRoot, "--to=0.2"}, flag: "--actor"},
		{
			name:   "migrate citation-mappings",
			prefix: []string{"migrate", unavailableRoot, "--to=0.2"},
			flag:   "--citation-mappings",
		},
		{name: "migrate format", prefix: []string{"migrate", unavailableRoot, "--to=0.2"}, flag: "--format"},
	}

	for _, test := range tests {
		for _, next := range []string{"--", "-literal"} {
			name := "dash-leading"
			if next == "--" {
				name = "terminator"
			}
			t.Run(test.name+"/"+name, func(t *testing.T) {
				// Arrange.
				loadCalls := 0
				validateCalls := 0
				indexCalls := 0
				dependencies := productionRunDependencies()
				dependencies.loadBundle = func(string) (*bundle.Bundle, error) {
					loadCalls++
					return nil, errors.New("bundle loader must not run")
				}
				dependencies.validateBundle = func(
					context.Context,
					*bundle.Bundle,
					*validator.ValidatorConfig,
				) (validator.Report, error) {
					validateCalls++
					return validator.Report{}, nil
				}
				dependencies.regenerateIndexes = func(string, string) ([]string, error) {
					indexCalls++
					return nil, errors.New("index regenerator must not run")
				}
				args := append(append([]string{}, test.prefix...), test.flag, next)
				before := snapshotFilesystemTree(t, parent)
				var stdout, stderr bytes.Buffer

				// Act.
				code := runWithDependencies(args, &stdout, &stderr, dependencies)
				after := snapshotFilesystemTree(t, parent)

				// Assert.
				if code != 1 || stdout.Len() != 0 ||
					loadCalls != 0 || validateCalls != 0 || indexCalls != 0 {
					t.Fatalf(
						"Run(%v) code/stdout/load/validate/index = %d/%q/%d/%d/%d",
						args,
						code,
						stdout.String(),
						loadCalls,
						validateCalls,
						indexCalls,
					)
				}
				assertCanonicalCLIError(
					t,
					stderr.String(),
					"flag needs an argument: "+test.flag,
				)
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("Run(%v) changed filesystem: before=%#v after=%#v",
						args, before, after)
				}
			})
		}
	}
}

func TestInlineStringFlagsPermitDashLeadingLiterals(t *testing.T) {
	tests := []struct {
		command string
		flag    string
	}{
		{command: "validate", flag: "--path"},
		{command: "validate", flag: "--format"},
		{command: "validate", flag: "--spec"},
		{command: "validate", flag: "--as-of"},
		{command: "info", flag: "--format"},
		{command: "info", flag: "--spec"},
		{command: "info", flag: "--as-of"},
		{command: "index", flag: "--spec"},
		{command: "graph", flag: "--format"},
		{command: "graph", flag: "--profile"},
		{command: "graph", flag: "--spec"},
		{command: "graph", flag: "--as-of"},
		{command: "graph", flag: "--extension-relations"},
		{command: "parse", flag: "--format"},
		{command: "parse", flag: "--spec"},
		{command: "parse", flag: "--as-of"},
		{command: "fmt", flag: "--spec"},
		{command: "migrate", flag: "--from"},
		{command: "migrate", flag: "--to"},
		{command: "migrate", flag: "--actor"},
		{command: "migrate", flag: "--citation-mappings"},
		{command: "migrate", flag: "--format"},
	}

	for _, test := range tests {
		t.Run(test.command+"/"+test.flag, func(t *testing.T) {
			// Arrange.
			specs := []flagSpec{{Name: test.flag, Kind: stringFlag}}

			// Act.
			parsed, err := parseArgs([]string{test.flag + "=-literal"}, specs)

			// Assert.
			if err != nil || parsed.value(test.flag, "") != "-literal" {
				t.Fatalf("parseArgs(%s=-literal) value/error = %q/%v",
					test.flag, parsed.value(test.flag, ""), err)
			}
		})
	}

	t.Run("validate compatibility aliases", func(t *testing.T) {
		// Arrange.
		tests := []struct {
			alias     string
			canonical string
		}{
			{alias: "-path", canonical: "--path"},
			{alias: "-format", canonical: "--format"},
		}
		specs := []flagSpec{
			{Name: "--path", Kind: stringFlag},
			{Name: "--strict", Kind: boolFlag},
			{Name: "--check-links", Kind: boolFlag},
			{Name: "--check-orphans", Kind: boolFlag},
			{Name: "--format", Kind: stringFlag},
			{Name: "--json", Kind: boolFlag},
		}
		for _, test := range tests {
			// Act.
			parsed, err := parseArgs([]string{test.alias + "=-literal"}, specs)

			// Assert.
			if err != nil || parsed.value(test.canonical, "") != "-literal" {
				t.Fatalf("parseArgs(%s=-literal) canonical value/error = %q/%v",
					test.alias, parsed.value(test.canonical, ""), err)
			}
		}
	})
}

func TestValidateCompatibilityAliasesShareCanonicalIdentityBeforeDependencies(t *testing.T) {
	root := filepath.Join(t.TempDir(), "unavailable")
	tests := []struct {
		name       string
		args       []string
		secondName string
	}{
		{
			name:       "path alias then canonical",
			args:       []string{"validate", "-path", root, "--path=other"},
			secondName: "--path",
		},
		{
			name:       "path alias twice",
			args:       []string{"validate", "-path", root, "-path=other"},
			secondName: "-path",
		},
		{
			name:       "strict alias then canonical",
			args:       []string{"validate", "-path=" + root, "-strict", "--strict"},
			secondName: "--strict",
		},
		{
			name:       "strict alias twice",
			args:       []string{"validate", "-path=" + root, "-strict", "-strict=false"},
			secondName: "-strict",
		},
		{
			name:       "check-links alias then canonical",
			args:       []string{"validate", "-path=" + root, "-check-links", "--check-links"},
			secondName: "--check-links",
		},
		{
			name:       "check-links alias twice",
			args:       []string{"validate", "-path=" + root, "-check-links", "-check-links=false"},
			secondName: "-check-links",
		},
		{
			name:       "check-orphans alias then canonical",
			args:       []string{"validate", "-path=" + root, "-check-orphans", "--check-orphans"},
			secondName: "--check-orphans",
		},
		{
			name:       "check-orphans alias twice",
			args:       []string{"validate", "-path=" + root, "-check-orphans", "-check-orphans=false"},
			secondName: "-check-orphans",
		},
		{
			name:       "format alias then canonical",
			args:       []string{"validate", "-path=" + root, "-format", "json", "--format=json"},
			secondName: "--format",
		},
		{
			name:       "format alias twice",
			args:       []string{"validate", "-path=" + root, "-format", "json", "-format=json"},
			secondName: "-format",
		},
		{
			name:       "json alias then canonical",
			args:       []string{"validate", "-path=" + root, "-json", "--json"},
			secondName: "--json",
		},
		{
			name:       "json alias twice",
			args:       []string{"validate", "-path=" + root, "-json", "-json=false"},
			secondName: "-json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			loadCalls := 0
			validateCalls := 0
			dependencies := productionRunDependencies()
			dependencies.loadBundle = func(string) (*bundle.Bundle, error) {
				loadCalls++
				return nil, errors.New("bundle loader must not run")
			}
			dependencies.validateBundle = func(
				context.Context,
				*bundle.Bundle,
				*validator.ValidatorConfig,
			) (validator.Report, error) {
				validateCalls++
				return validator.Report{}, nil
			}
			var stdout, stderr bytes.Buffer

			// Act.
			code := runWithDependencies(test.args, &stdout, &stderr, dependencies)

			// Assert.
			if code != 1 || stdout.Len() != 0 ||
				loadCalls != 0 || validateCalls != 0 {
				t.Fatalf(
					"Run(%v) code/stdout/load/validate = %d/%q/%d/%d",
					test.args,
					code,
					stdout.String(),
					loadCalls,
					validateCalls,
				)
			}
			assertCanonicalCLIError(
				t,
				stderr.String(),
				"duplicate flag: "+test.secondName,
			)
		})
	}
}

func TestHelpDocumentsReservedCitationReplayEvidenceContract(t *testing.T) {
	// Arrange.
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"help"}, &stdout, &stderr)

	// Assert.
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run(help) code/stderr = %d/%q, want 0/empty", code, stderr.String())
	}
	for _, fragment := range []string{
		"Reserved index/log replay requires exact legacy_entry",
		"or complete title/resource evidence",
	} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("help output missing %q:\n%s", fragment, stdout.String())
		}
	}
}

func assertCanonicalCLIError(t *testing.T, got, message string) {
	t.Helper()
	want := "error: " + renderTextString(message) + "\n"
	if got != want {
		t.Fatalf("stderr = %q, want exact %q", got, want)
	}
	if strings.Count(got, "\n") != 1 ||
		!strings.HasSuffix(got, "\n") ||
		strings.ContainsAny(got, "\r\x00\x01\t\x1f\x7f\u2028\u2029") ||
		strings.Contains(got, "Ω\n") {
		t.Fatalf("stderr is not one canonical escaped line: %q", got)
	}
}
