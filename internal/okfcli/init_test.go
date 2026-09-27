package okfcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/validator"
)

func TestInitCreatesValidDraftBundle(t *testing.T) {
	// Arrange.
	target := filepath.Join(t.TempDir(), "knowledge")
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"init", target, "--json"}, &stdout, &stderr)

	// Assert.
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("init code/stderr = %d/%q", code, stderr.String())
	}
	var result initResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Path != target || result.OKFVersion != bundle.OKFVersion ||
		len(result.Files) != 2 || result.Files[0] != "index.md" || result.Files[1] != "getting-started.md" {
		t.Fatalf("init result = %+v", result)
	}
	loaded, err := bundle.LoadBundle(target)
	if err != nil {
		t.Fatal(err)
	}
	report := validator.ValidateBundle(loaded, &validator.ValidatorConfig{Spec: "0.2", Strict: true})
	if !report.IsConformant() || report.ErrorCount() != 0 {
		t.Fatalf("new bundle fails base validation: %+v", report)
	}
	concept, err := os.ReadFile(filepath.Join(target, "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(concept), "status: draft") || strings.Contains(string(concept), "sources:") || strings.Contains(string(concept), "verified:") {
		t.Fatalf("unsafe placeholder metadata: %s", concept)
	}
}

func TestInitRejectsConflictsAndLeavesExistingDataIntact(t *testing.T) {
	for _, mode := range []string{"file", "directory", "symlink", "repeat"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange.
			parent := t.TempDir()
			target := filepath.Join(parent, "knowledge")
			switch mode {
			case "file":
				if err := os.WriteFile(target, []byte("owner data"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "owner.txt"), []byte("owner data"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(parent, "missing"), target); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "repeat":
				if err := initBundleContext(context.Background(), target, nil); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(target, "owner.txt"))

			// Act.
			var stdout, stderr bytes.Buffer
			code := Run([]string{"init", target}, &stdout, &stderr)

			// Assert.
			if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "already exists") {
				t.Fatalf("conflict result = %d/%q/%q", code, stdout.String(), stderr.String())
			}
			after, _ := os.ReadFile(filepath.Join(target, "owner.txt"))
			if !bytes.Equal(before, after) {
				t.Fatalf("owner data changed: %q => %q", before, after)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 1 {
				t.Fatalf("leftover stage: %v, %v", entries, err)
			}
		})
	}
}

func TestInitRejectsInvalidArgumentsAndMissingParent(t *testing.T) {
	parent := t.TempDir()
	for _, tc := range []struct {
		name, fragment string
		args           []string
	}{
		{"missing target", "missing bundle directory", []string{"init"}},
		{"extra target", "unexpected argument", []string{"init", "one", "two"}},
		{"unknown flag", "unknown flag", []string{"init", "--force", "one"}},
		{"missing parent", "open bundle parent", []string{"init", filepath.Join(parent, "absent", "knowledge")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run(tc.args, &stdout, &stderr)

			// Assert.
			if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.fragment) {
				t.Fatalf("invalid init result = %d/%q/%q", code, stdout.String(), stderr.String())
			}
		})
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid requests changed parent: %v, %v", entries, err)
	}
}

func TestInitCancellationAndPublicationFailureLeaveNoBundle(t *testing.T) {
	for _, tc := range []struct {
		name string
		hook func(context.CancelFunc) func(context.Context) error
	}{
		{"injected failure", func(context.CancelFunc) func(context.Context) error {
			return func(context.Context) error { return errors.New("publish failed") }
		}},
		{"cancelled", func(cancel context.CancelFunc) func(context.Context) error {
			return func(context.Context) error { cancel(); return nil }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			parent := t.TempDir()
			target := filepath.Join(parent, "knowledge")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Act.
			err := initBundleContext(ctx, target, tc.hook(cancel))

			// Assert.
			if err == nil {
				t.Fatal("init succeeded despite interrupted publication")
			}
			if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("target after rejection: %v", statErr)
			}
			entries, readErr := os.ReadDir(parent)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("stage after rejection: %v, %v", entries, readErr)
			}
		})
	}
}

func TestInitRejectsLateTargetWithoutReplacingIt(t *testing.T) {
	// Arrange.
	parent := t.TempDir()
	target := filepath.Join(parent, "knowledge")

	// Act.
	err := initBundleContext(context.Background(), target, func(context.Context) error {
		return os.WriteFile(target, []byte("winner"), 0o600)
	})

	// Assert.
	if err == nil {
		t.Fatal("late target replaced")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil || string(got) != "winner" {
		t.Fatalf("late target = %q, %v", got, readErr)
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil || len(entries) != 1 {
		t.Fatalf("stage after conflict: %v, %v", entries, readErr)
	}
}

func TestInitSignalCancelsCLIAndCleansStage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal subprocess test")
	}
	if os.Getenv("OKF_INIT_SIGNAL_HELPER") == "1" {
		target := os.Getenv("OKF_INIT_SIGNAL_TARGET")
		marker := os.Getenv("OKF_INIT_SIGNAL_MARKER")
		var stdout bytes.Buffer
		_, err := cmdInitWithHook([]string{target}, &stdout, func(ctx context.Context) error {
			if err := os.WriteFile(marker, []byte("ready"), 0o600); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		})
		if err == nil || stdout.Len() != 0 {
			fmt.Fprintf(os.Stderr, "unexpected helper result: %v/%q\n", err, stdout.String())
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Arrange.
	parent := t.TempDir()
	target := filepath.Join(parent, "knowledge")
	marker := filepath.Join(t.TempDir(), "ready")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestInitSignalCancelsCLIAndCleansStage$")
	cmd.Env = append(os.Environ(), "OKF_INIT_SIGNAL_HELPER=1", "OKF_INIT_SIGNAL_TARGET="+target, "OKF_INIT_SIGNAL_MARKER="+marker)
	var output bytes.Buffer
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("helper did not reach publish seam: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Act.
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err = <-waited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-waited
		t.Fatalf("helper did not exit after SIGINT: %s", output.String())
	}

	// Assert.
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(output.String(), "context canceled") {
		t.Fatalf("signal result = %v, stderr=%q", err, output.String())
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target after signal: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("stage after signal: %v, %v", entries, err)
	}
}
