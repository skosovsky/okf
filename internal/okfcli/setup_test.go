package okfcli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/skosovsky/okf/setup"
	"os"
	"path/filepath"
	"testing"
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
