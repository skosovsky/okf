package okfcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/validator"
)

func TestValidateFatalDependencyErrorsPreserveIdentityAndProduceNoReport(t *testing.T) {
	// Arrange.
	fatalSentinel := errors.New("fatal validator sentinel")
	tests := []struct {
		name    string
		wantErr error
	}{
		{name: "fatal sentinel", wantErr: fatalSentinel},
		{name: "cancellation", wantErr: context.Canceled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := productionRunDependencies()
			dependencies.loadBundle = func(string) (*bundle.Bundle, error) {
				return &bundle.Bundle{}, nil
			}
			dependencies.validateBundle = func(
				ctx context.Context,
				_ *bundle.Bundle,
				_ *validator.ValidatorConfig,
			) (validator.Report, error) {
				if ctx == nil {
					t.Fatal("validator context = nil")
				}
				return validator.Report{ScannedFiles: 1}, fmt.Errorf("validation stopped: %w", test.wantErr)
			}

			var commandStdout bytes.Buffer
			_, commandErr := cmdValidate(
				[]string{"--path", "captured-root", "--json"},
				&commandStdout,
				dependencies.loadBundle,
				dependencies.validateBundle,
			)
			var stdout, stderr bytes.Buffer

			// Act.
			code := runWithDependencies(
				[]string{"validate", "--path", "captured-root", "--json"},
				&stdout,
				&stderr,
				dependencies,
			)

			// Assert.
			if !errors.Is(commandErr, test.wantErr) {
				t.Fatalf("cmdValidate() error = %v, want identity %v", commandErr, test.wantErr)
			}
			if commandStdout.Len() != 0 {
				t.Fatalf("cmdValidate() stdout = %q, want empty", commandStdout.String())
			}
			if code != 1 || stdout.Len() != 0 {
				t.Fatalf(
					"runWithDependencies(validate) code/stdout = %d/%q, want 1/empty",
					code,
					stdout.String(),
				)
			}
			assertCanonicalCLIError(t, stderr.String(), commandErr.Error())
			assertNoValidationSuccessOutput(t, stdout.String(), stderr.String())
		})
	}
}

func TestValidateRealCapturedRootRejectsFatalYAMLGraphsWithoutPartialReport(t *testing.T) {
	// Arrange.
	resourceValue := strings.Repeat("[", bundle.MaxYAMLPhysicalDepth+1) +
		"leaf" +
		strings.Repeat("]", bundle.MaxYAMLPhysicalDepth+1)
	tests := []struct {
		name        string
		frontmatter string
		wantErr     error
	}{
		{
			name: "invalid graph",
			frontmatter: "okf_version: 0.2\n" +
				"title: Root\n" +
				"first: &duplicate one\n" +
				"second: &duplicate two\n",
			wantErr: bundle.ErrInvalidYAMLGraph,
		},
		{
			name: "resource limit",
			frontmatter: "okf_version: 0.2\n" +
				"title: Root\n" +
				"payload: " + resourceValue + "\n",
			wantErr: bundle.ErrYAMLResourceLimit,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			content := "---\n" + test.frontmatter + "---\n\n# Root\n"
			if err := os.WriteFile(filepath.Join(root, "index.md"), []byte(content), 0o600); err != nil {
				t.Fatalf("write captured root: %v", err)
			}
			loaded, err := bundle.LoadBundle(root)
			if err != nil {
				t.Fatalf("LoadBundle(captured root): %v", err)
			}
			_, validationErr := validator.ValidateBundleContext(context.Background(), loaded, nil)
			if !errors.Is(validationErr, test.wantErr) {
				t.Fatalf("ValidateBundleContext() error = %v, want identity %v", validationErr, test.wantErr)
			}
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run(
				[]string{"validate", "--path", root, "--spec", "0.2", "--json"},
				&stdout,
				&stderr,
			)

			// Assert.
			if code != 1 || stdout.Len() != 0 {
				t.Fatalf("Run(validate) code/stdout = %d/%q, want 1/empty", code, stdout.String())
			}
			if !strings.Contains(stderr.String(), test.wantErr.Error()) {
				t.Fatalf("Run(validate) stderr = %q, want identity text %q", stderr.String(), test.wantErr.Error())
			}
			if strings.Count(stderr.String(), "error: ") != 1 || strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("Run(validate) stderr = %q, want one canonical error line", stderr.String())
			}
			assertNoValidationSuccessOutput(t, stdout.String(), stderr.String())
		})
	}
}

func assertNoValidationSuccessOutput(t *testing.T, stdout, stderr string) {
	t.Helper()
	combined := stdout + stderr
	for _, falseSuccess := range []string{"Result: PASS", `"conformant":true`, `"conformant": true`} {
		if strings.Contains(combined, falseSuccess) {
			t.Fatalf("validation output contains false success marker %q: %q", falseSuccess, combined)
		}
	}
}
