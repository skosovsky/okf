package okfcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/setup"
)

func TestSetupCLIJSONPreviewAndApply(t *testing.T) {
	// Arrange.
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	target := filepath.Join(dir, "bundle")
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.md"), []byte("# Existing note\n"), 0644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--source", src, "--target", target, "--type", "Guide", "--json"}
	var stdout bytes.Buffer
	// Act.
	code, err := cmdSetup(args, &stdout)
	var plan setup.Plan
	decodeErr := json.Unmarshal(stdout.Bytes(), &plan)
	stdout.Reset()
	applyArgs := append(append([]string{}, args...), "--apply", "--plan-digest", plan.Digest)
	appliedCode, applyErr := cmdSetup(applyArgs, &stdout)
	// Assert.
	if code != 0 || err != nil || decodeErr != nil || !plan.Applicable || plan.Published || appliedCode != 0 || applyErr != nil {
		t.Fatalf("preview=%d %v %+v apply=%d %v", code, err, plan, appliedCode, applyErr)
	}
	p, e := setup.Preview(context.Background(), setup.Options{Source: src, Target: target, Type: "Guide"})
	if e != nil || p.Applicable {
		t.Fatal("existing target accepted")
	}
}

func TestOKF03SetupBindsVersionAndPreservesMarkdown(t *testing.T) {
	dir := t.TempDir()
	src, target := filepath.Join(dir, "src"), filepath.Join(dir, "bundle")
	const raw = "---\ntype: Guide\nowner: {team: data} # preserve\n---\n# Existing note\n"
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.md"), []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--source", src, "--target", target, "--json"}
	versionArgs := append(append([]string{}, args...), "--spec", "0.3")
	var stdout bytes.Buffer
	if code, err := cmdSetup(args, &stdout); code != 0 || err != nil {
		t.Fatalf("preview = %d, %v", code, err)
	}
	var plan setup.Plan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil || plan.Options.OKFVersion != "0.3" || !plan.Applicable {
		t.Fatalf("preview = %+v, %v", plan, err)
	}
	applyFlags := []string{"--apply", "--plan-digest", plan.Digest}
	stdout.Reset()
	legacyArgs := append(append([]string{}, args...), "--spec", "0.2")
	if _, err := cmdSetup(append(legacyArgs, applyFlags...), &stdout); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("apply with a different version = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("mismatched plan published: %v", err)
	}
	stdout.Reset()
	if code, err := cmdSetup(append(versionArgs, applyFlags...), &stdout); code != 0 || err != nil {
		t.Fatalf("apply = %d, %v", code, err)
	}
	index, err := os.ReadFile(filepath.Join(target, "index.md"))
	if err != nil || !strings.Contains(string(index), `okf_version: "0.3"`) {
		t.Fatalf("root index = %q, %v", index, err)
	}
	for _, root := range []string{src, target} {
		data, err := os.ReadFile(filepath.Join(root, "a.md"))
		if err != nil || string(data) != raw {
			t.Fatalf("%s/a.md = %q, %v", root, data, err)
		}
	}
}
