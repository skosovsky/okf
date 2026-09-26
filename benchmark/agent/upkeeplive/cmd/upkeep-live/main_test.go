package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSyncedHashMatchesDurableBytes(t *testing.T) {
	// Arrange.
	path := filepath.Join(t.TempDir(), "rows.json")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	// Act.
	hash, err := writeSynced(f, map[string]int{"calls": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Assert.
	want := sha256.Sum256(b)
	if hash != hex.EncodeToString(want[:]) {
		t.Fatalf("saved hash %s differs from bytes", hash)
	}
}

func TestRunRejectsOuterDeadlineShorterThanBuiltInAdapter(t *testing.T) {
	// Arrange: all required paths are syntactically present; no files are read.
	args := []string{"-plan", "/tmp/frozen-plan.json", "-rows", "/tmp/new-rows.json", "-audit", "/tmp/new-audit.json", "-adapter", "/bin/true", "-call-seconds", "30"}
	// Act.
	err := run(args)
	// Assert.
	if err == nil || !strings.Contains(err.Error(), "at least 45") {
		t.Fatalf("error=%v", err)
	}
}
