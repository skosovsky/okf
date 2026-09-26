package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

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
	specFile,expectedHash:="",""
	switch specRevision {
	case "3fcbb9f828c2f23d109c855ee403c3a4c81f3a96": specFile="spec-v02.md";expectedHash="5a3311d270bebb16d558010e75064f5b75323f284992641732b1c8097511f948"
	case "0b87c52c6ef999286c745e19998fdfcd03d5dbee": specFile="spec-v02-instant.md";expectedHash="26aa5da029278939f914e578107242d9607d4f2dc5fe153272b82f9ed1030101"
	default: return "","",fmt.Errorf("SPEC revision is not one of the two pinned OKF 0.2 contracts")
	}
	specData, err := os.ReadFile(filepath.Join(root, "skills/open-knowledge-format/references",specFile))
	if err != nil {
		return "", "", err
	}
	h := sha256.Sum256(specData)
	specHash = hex.EncodeToString(h[:])
	if specHash!=expectedHash{return "","",fmt.Errorf("pinned SPEC file hash mismatch")}
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
