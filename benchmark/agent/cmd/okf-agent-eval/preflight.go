package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// BYOT adapters may have no local model runtime. The Codex adapter requires
// all three pins in every toolkit mode; whenever supplied, the runner checks
// them before it writes a plan or invokes an adapter.
func runtimePinForMode(mode, path, version, expectedSHA256 string) (string, error) {
	if path == "" && version == "" && expectedSHA256 == "" {
		if mode == "direct" {
			return "", fmt.Errorf("direct mode requires a pinned model runtime")
		}
		return "", nil
	}
	return pinnedModelRuntime(path, version, expectedSHA256)
}

func pinnedModelRuntime(path, version, expectedSHA256 string) (string, error) {
	if path == "" || version == "" || !sha256Pattern.MatchString(expectedSHA256) {
		return "", fmt.Errorf("model runtime pin requires an absolute path, exact version, and SHA-256")
	}
	hash, err := hashExecutable(path)
	if err != nil {
		return "", err
	}
	if hash != expectedSHA256 {
		return "", fmt.Errorf("model runtime SHA-256 mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("model runtime version probe: %w", err)
	}
	if strings.TrimSpace(string(out)) != version {
		return "", fmt.Errorf("model runtime version mismatch: got %q", strings.TrimSpace(string(out)))
	}
	return hash, nil
}

func pinnedInputs(commit, specRevision, adapter string, strict bool) (specHash, adapterHash string, err error) {
	if !revisionPattern.MatchString(commit) || !revisionPattern.MatchString(specRevision) {
		return "", "", fmt.Errorf("commit and SPEC revision must be full 40-digit Git IDs")
	}
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", err
	}
	if strict && strings.TrimSpace(string(head)) != commit {
		return "", "", fmt.Errorf("pinned commit differs from HEAD")
	}
	if strict {
		status, err := exec.Command("git", "status", "--porcelain", "--untracked-files=all").Output()
		if err != nil {
			return "", "", err
		}
		if len(status) > 0 {
			return "", "", fmt.Errorf("working tree is dirty; benchmark requires committed code and corpus")
		}
	}
	rootBytes, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", "", err
	}
	root := strings.TrimSpace(string(rootBytes))
	specFile, expectedHash := "", ""
	switch specRevision {
	case "3fcbb9f828c2f23d109c855ee403c3a4c81f3a96":
		specFile = "spec-v02.md"
		expectedHash = "5a3311d270bebb16d558010e75064f5b75323f284992641732b1c8097511f948"
	case "0b87c52c6ef999286c745e19998fdfcd03d5dbee":
		specFile = "spec-v02-instant.md"
		expectedHash = "26aa5da029278939f914e578107242d9607d4f2dc5fe153272b82f9ed1030101"
	default:
		return "", "", fmt.Errorf("SPEC revision is not one of the two pinned OKF 0.2 contracts")
	}
	specData, err := os.ReadFile(filepath.Join(root, "skills/open-knowledge-format/references", specFile))
	if err != nil {
		return "", "", err
	}
	h := sha256.Sum256(specData)
	specHash = hex.EncodeToString(h[:])
	if specHash != expectedHash {
		return "", "", fmt.Errorf("pinned SPEC file hash mismatch")
	}
	skillData, err := os.ReadFile(filepath.Join(root, "skills/open-knowledge-format/SKILL.md"))
	if err != nil {
		return "", "", err
	}
	if strict && (!strings.Contains(string(skillData), specRevision) || !strings.Contains(string(skillData), specHash)) {
		return "", "", fmt.Errorf("SPEC revision/hash do not match normative skill lock")
	}
	adapterHash, err = hashExecutable(adapter)
	if err != nil {
		return "", "", err
	}
	return specHash, adapterHash, nil
}

func hashExecutable(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("binary must be an absolute executable regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}
