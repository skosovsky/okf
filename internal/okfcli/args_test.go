package okfcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalFlagFormsAndTrueShortInventory(t *testing.T) {
	canonicalFixture := fixturePath(t, "positive", "minimal")
	root := t.TempDir()
	for _, name := range []string{"index.md", "minimal.md"} {
		contents, err := os.ReadFile(filepath.Join(canonicalFixture, name))
		if err != nil {
			t.Fatalf("read canonical fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(root, name), contents, 0o644); err != nil {
			t.Fatalf("copy canonical fixture %s: %v", name, err)
		}
	}
	note := filepath.Join(root, "minimal.md")
	canonical := []struct {
		name string
		args []string
	}{
		{name: "top-level help short", args: []string{"-h"}},
		{name: "top-level help long", args: []string{"--help"}},
		{name: "top-level version short", args: []string{"-V"}},
		{name: "top-level version long", args: []string{"--version"}},
		{name: "version json bare", args: []string{"version", "--json"}},
		{name: "version json true", args: []string{"version", "--json=true"}},
		{name: "version json false", args: []string{"version", "--json=false"}},
		{name: "validate path value", args: []string{"validate", "--path", root}},
		{name: "validate path equals", args: []string{"validate", "--path=" + root}},
		{name: "validate path alias value", args: []string{"validate", "-path", root}},
		{name: "validate path alias equals", args: []string{"validate", "-path=" + root}},
		{name: "validate format value", args: []string{"validate", "--path", root, "--format", "json"}},
		{name: "validate format equals", args: []string{"validate", "--path", root, "--format=json"}},
		{name: "validate format alias value", args: []string{"validate", "-path", root, "-format", "json"}},
		{name: "validate format alias equals", args: []string{"validate", "-path=" + root, "-format=json"}},
		{name: "validate strict bare", args: []string{"validate", "--path", root, "--strict"}},
		{name: "validate strict true", args: []string{"validate", "--path", root, "--strict=true"}},
		{name: "validate strict false", args: []string{"validate", "--path", root, "--strict=false"}},
		{name: "validate strict alias bare", args: []string{"validate", "-path", root, "-strict"}},
		{name: "validate strict alias true", args: []string{"validate", "-path", root, "-strict=true"}},
		{name: "validate strict alias false", args: []string{"validate", "-path", root, "-strict=false"}},
		{name: "validate check-links bare", args: []string{"validate", "--path", root, "--check-links"}},
		{name: "validate check-links true", args: []string{"validate", "--path", root, "--check-links=true"}},
		{name: "validate check-links false", args: []string{"validate", "--path", root, "--check-links=false"}},
		{name: "validate check-links alias bare", args: []string{"validate", "-path", root, "-check-links"}},
		{name: "validate check-links alias true", args: []string{"validate", "-path", root, "-check-links=true"}},
		{name: "validate check-links alias false", args: []string{"validate", "-path", root, "-check-links=false"}},
		{name: "validate check-orphans bare", args: []string{"validate", "--path", root, "--check-orphans"}},
		{name: "validate check-orphans true", args: []string{"validate", "--path", root, "--check-orphans=true"}},
		{name: "validate check-orphans false", args: []string{"validate", "--path", root, "--check-orphans=false"}},
		{name: "validate check-orphans alias bare", args: []string{"validate", "-path", root, "-check-orphans"}},
		{name: "validate check-orphans alias true", args: []string{"validate", "-path", root, "-check-orphans=true"}},
		{name: "validate check-orphans alias false", args: []string{"validate", "-path", root, "-check-orphans=false"}},
		{name: "validate json bare", args: []string{"validate", "--path", root, "--json"}},
		{name: "validate json true", args: []string{"validate", "--path", root, "--json=true"}},
		{name: "validate json false", args: []string{"validate", "--path", root, "--json=false"}},
		{name: "validate json alias bare", args: []string{"validate", "-path", root, "-json"}},
		{name: "validate json alias true", args: []string{"validate", "-path", root, "-json=true"}},
		{name: "validate json alias false", args: []string{"validate", "-path", root, "-json=false"}},
		{name: "info json bare", args: []string{"info", root, "--json"}},
		{name: "info json true", args: []string{"info", root, "--json=true"}},
		{name: "info json false", args: []string{"info", root, "--json=false"}},
		{name: "graph format value", args: []string{"graph", root, "--format", "dot"}},
		{name: "graph format equals", args: []string{"graph", root, "--format=dot"}},
		{name: "graph dot bare", args: []string{"graph", root, "--dot"}},
		{name: "graph dot true", args: []string{"graph", root, "--dot=true"}},
		{name: "graph dot false", args: []string{"graph", root, "--dot=false"}},
		{name: "graph annotate topology bare", args: []string{"graph", root, "--annotate-topology"}},
		{name: "graph annotate topology true", args: []string{"graph", root, "--annotate-topology=true"}},
		{name: "graph annotate topology false", args: []string{"graph", root, "--annotate-topology=false"}},
		{name: "parse json bare", args: []string{"parse", note, "--json"}},
		{name: "parse json true", args: []string{"parse", note, "--json=true"}},
		{name: "parse json false", args: []string{"parse", note, "--json=false"}},
		{name: "migrate write bare", args: []string{"migrate", root, "--to", "0.2", "--write"}},
		{name: "migrate write true", args: []string{"migrate", root, "--to", "0.2", "--write=true"}},
		{name: "migrate write false", args: []string{"migrate", root, "--to", "0.2", "--write=false"}},
	}
	for _, test := range canonical {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run(test.args, &stdout, &stderr)

			// Assert.
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("Run(%v) code/stdout/stderr = %d/%q/%q, want 0/any/empty",
					test.args, code, stdout.String(), stderr.String())
			}
		})
	}

	t.Run("fmt true short write flag", func(t *testing.T) {
		// Arrange.
		shortPath := filepath.Join(t.TempDir(), "short.md")
		longPath := filepath.Join(t.TempDir(), "long.md")
		truePath := filepath.Join(t.TempDir(), "true.md")
		falsePath := filepath.Join(t.TempDir(), "false.md")
		original := "---\ntype:  Note\n---\nBody.\n"
		writeFixtureFile(t, shortPath, original)
		writeFixtureFile(t, longPath, original)
		writeFixtureFile(t, truePath, original)
		writeFixtureFile(t, falsePath, original)
		var shortStdout, shortStderr, longStdout, longStderr bytes.Buffer
		var trueStdout, trueStderr, falseStdout, falseStderr bytes.Buffer

		// Act.
		shortCode := Run([]string{"fmt", "-w", shortPath}, &shortStdout, &shortStderr)
		longCode := Run([]string{"fmt", "--write", longPath}, &longStdout, &longStderr)
		trueCode := Run([]string{"fmt", "--write=true", truePath}, &trueStdout, &trueStderr)
		falseCode := Run([]string{"fmt", "--write=false", falsePath}, &falseStdout, &falseStderr)
		shortAfter, shortErr := os.ReadFile(shortPath)
		longAfter, longErr := os.ReadFile(longPath)
		trueAfter, trueErr := os.ReadFile(truePath)
		falseAfter, falseErr := os.ReadFile(falsePath)

		// Assert.
		if shortCode != 0 || longCode != 0 || trueCode != 0 || falseCode != 0 ||
			shortStderr.Len() != 0 || longStderr.Len() != 0 ||
			trueStderr.Len() != 0 || falseStderr.Len() != 0 ||
			shortErr != nil || longErr != nil || trueErr != nil || falseErr != nil ||
			!bytes.Equal(shortAfter, longAfter) ||
			!bytes.Equal(shortAfter, trueAfter) ||
			!bytes.Equal(falseAfter, []byte(original)) ||
			falseStdout.Len() == 0 {
			t.Fatalf("fmt write forms = codes %d/%d/%d/%d, stderr %q/%q/%q/%q, reads %v/%v/%v/%v",
				shortCode, longCode, trueCode, falseCode,
				shortStderr.String(), longStderr.String(), trueStderr.String(), falseStderr.String(),
				shortErr, longErr, trueErr, falseErr)
		}
	})
}

func TestCompatibilityOnlyFlagsAndProfileSynonymsAreRejected(t *testing.T) {
	root := fixturePath(t, "positive", "minimal")
	note := filepath.Join(root, "minimal.md")
	tests := []struct {
		name     string
		args     []string
		fragment string
	}{
		{name: "help single-dash long", args: []string{"help", "-help"}, fragment: "unknown flag: -help"},
		{name: "version json", args: []string{"version", "-json"}, fragment: "unknown flag: -json"},
		{name: "validate spec", args: []string{"validate", "--path", root, "-spec=0.2"}, fragment: "unknown flag: -spec"},
		{name: "validate as-of", args: []string{"validate", "--path", root, "-as-of=2099-01-01"}, fragment: "unknown flag: -as-of"},
		{name: "validate attached long", args: []string{"validate", "--path", root, "--jsontrue"}, fragment: "unknown flag: --jsontrue"},
		{name: "info format value", args: []string{"info", root, "-format", "json"}, fragment: "unknown flag: -format"},
		{name: "info format equals", args: []string{"info", root, "-format=json"}, fragment: "unknown flag: -format"},
		{name: "index spec", args: []string{"index", root, "-spec=0.2"}, fragment: "unknown flag: -spec"},
		{name: "graph format value", args: []string{"graph", root, "-format", "dot"}, fragment: "unknown graph flag: -format"},
		{name: "graph format equals", args: []string{"graph", root, "-format=dot"}, fragment: "unknown graph flag: -format"},
		{name: "graph dot bare", args: []string{"graph", root, "-dot"}, fragment: "unknown graph flag: -dot"},
		{name: "graph dot true", args: []string{"graph", root, "-dot=true"}, fragment: "unknown graph flag: -dot"},
		{name: "graph dot false", args: []string{"graph", root, "-dot=false"}, fragment: "unknown graph flag: -dot"},
		{name: "graph attached long", args: []string{"graph", root, "--formatdot"}, fragment: "unknown graph flag: --formatdot"},
		{name: "parse format value", args: []string{"parse", note, "-format", "json"}, fragment: "unknown flag: -format"},
		{name: "parse format equals", args: []string{"parse", note, "-format=json"}, fragment: "unknown flag: -format"},
		{name: "fmt long disguised as short", args: []string{"fmt", note, "-write"}, fragment: "unknown flag: -write"},
		{name: "migrate format", args: []string{"migrate", root, "--to", "0.2", "-format=json"}, fragment: "unknown flag: -format"},
	}
	for _, profile := range []string{"legacy", "0.1", "v0.1", "toolkit", "0.2", "v0.2"} {
		tests = append(tests, struct {
			name     string
			args     []string
			fragment string
		}{
			name:     "graph profile synonym " + profile,
			args:     []string{"graph", root, "--profile", profile},
			fragment: "unsupported graph profile: " + profile,
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run(test.args, &stdout, &stderr)

			// Assert.
			if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.fragment) {
				t.Fatalf("Run(%v) code/stdout/stderr = %d/%q/%q, want 1/empty/%q",
					test.args, code, stdout.String(), stderr.String(), test.fragment)
			}
		})
	}
}

func TestArgumentParserSupportsOrderTerminatorAndRejectsDuplicates(t *testing.T) {
	// Arrange.
	root := fixturePath(t, "positive", "minimal")
	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{name: "flags before path", args: []string{"info", "--json", "--spec=0.2", root}, wantCode: 0},
		{name: "flags after path", args: []string{"info", root, "--spec", "0.2", "--json"}, wantCode: 0},
		{name: "duplicate true short", args: []string{"fmt", "-w", "--write", filepath.Join(root, "minimal.md")}, wantCode: 1, wantStderr: "duplicate flag"},
		{name: "extra argument", args: []string{"info", root, "extra"}, wantCode: 1, wantStderr: "unexpected info argument"},
		{name: "help extra argument", args: []string{"help", "extra"}, wantCode: 1, wantStderr: "unexpected help argument"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run(test.args, &stdout, &stderr)

			// Assert.
			if code != test.wantCode {
				t.Fatalf("Run(%v) code = %d, want %d; stderr=%q", test.args, code, test.wantCode, stderr.String())
			}
			if test.wantStderr != "" && !strings.Contains(stderr.String(), test.wantStderr) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

func TestAllPositionalCommandsAcceptDashPathAfterTerminator(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir("-bundle", 0o755); err != nil {
		t.Fatalf("os.Mkdir(-bundle) error = %v", err)
	}
	writeFixtureFile(t, filepath.Join("-bundle", "index.md"), "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n\n* [Note](note.md)\n")
	writeFixtureFile(t, filepath.Join("-bundle", "note.md"), "---\ntype: Note\n---\nBody.\n")
	writeFixtureFile(t, "-file.md", "---\ntype: Note\n---\nBody.\n")
	if err := os.Mkdir("-legacy", 0o755); err != nil {
		t.Fatalf("os.Mkdir(-legacy) error = %v", err)
	}
	writeFixtureFile(t, filepath.Join("-legacy", "index.md"), "---\nokf_version: \"0.1\"\n---\n\n# Concepts\n\n* [Note](note.md)\n")
	writeFixtureFile(t, filepath.Join("-legacy", "note.md"), "---\ntype: Note\ntimestamp: 2026-06-01T10:00:00Z\n---\nBody.\n")
	cases := []struct {
		name string
		args []string
	}{
		{name: "info", args: []string{"info", "--json", "--", "-bundle"}},
		{name: "index", args: []string{"index", "--", "-bundle"}},
		{name: "graph", args: []string{"graph", "--format", "json-ld", "--", "-bundle"}},
		{name: "parse", args: []string{"parse", "--json", "--", "-file.md"}},
		{name: "fmt", args: []string{"fmt", "--", "-file.md"}},
		{name: "migrate", args: []string{"migrate", "--to", "0.2", "--actor", "human:test", "--format", "json", "--", "-legacy"}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run(test.args, &stdout, &stderr)

			// Assert.
			if code != 0 || stderr.Len() != 0 || stdout.Len() == 0 {
				t.Fatalf("Run(%v) code/stdout/stderr = %d/%q/%q, want 0/non-empty/empty",
					test.args, code, stdout.String(), stderr.String())
			}
		})
	}
}
