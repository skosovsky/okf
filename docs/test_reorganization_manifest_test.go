package docs_test

import (
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testReorganizationManifest struct {
	SchemaVersion int `json:"schema_version"`
	Baseline      struct {
		FileCount             int  `json:"file_count"`
		TopLevelTestFuzzCount int  `json:"top_level_test_fuzz_count"`
		BodyEquivalence       bool `json:"body_equivalence_claimed"`
	} `json:"baseline"`
	Files []struct {
		BeforePath            string             `json:"before_path"`
		SHA256                string             `json:"sha256"`
		LOC                   *int               `json:"loc"`
		LOCStatus             string             `json:"loc_status"`
		PlatformClass         string             `json:"platform_class"`
		AfterPaths            []string           `json:"after_paths"`
		AfterBuildConstraints map[string]*string `json:"after_build_constraints"`
	} `json:"files"`
	Functions []struct {
		BeforeFile string `json:"before_file"`
		BeforeName string `json:"before_name"`
		AfterFile  string `json:"after_file"`
		AfterName  string `json:"after_name"`
	} `json:"functions"`
	PackageInventory []struct {
		Package            string `json:"package"`
		BaselineFunctions  int    `json:"baseline_functions"`
		SuccessorFunctions int    `json:"successor_functions"`
	} `json:"package_inventory"`
}

func TestV02TestReorganizationManifestMatchesCurrentInventory(t *testing.T) {
	// Arrange.
	root := repositoryRoot(t)
	manifestPath := filepath.Join(root, "docs", "development", "v0.2", "test-reorganization-v1.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest testReorganizationManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode %s: %v", manifestPath, err)
	}

	// Act and assert.
	if manifest.SchemaVersion != 1 || manifest.Baseline.FileCount != 64 ||
		manifest.Baseline.TopLevelTestFuzzCount != 401 || manifest.Baseline.BodyEquivalence {
		t.Fatalf("unexpected manifest baseline: %#v", manifest.Baseline)
	}
	if len(manifest.Files) != 64 || len(manifest.Functions) != 401 {
		t.Fatalf("manifest files/functions = %d/%d, want 64/401", len(manifest.Files), len(manifest.Functions))
	}

	beforeFiles := make(map[string]bool, len(manifest.Files))
	for _, file := range manifest.Files {
		if beforeFiles[file.BeforePath] {
			t.Fatalf("duplicate baseline file %q", file.BeforePath)
		}
		beforeFiles[file.BeforePath] = true
		digest, err := hex.DecodeString(file.SHA256)
		if err != nil || len(digest) != 32 {
			t.Fatalf("invalid SHA-256 for %s: %q", file.BeforePath, file.SHA256)
		}
		if file.LOC != nil || file.LOCStatus == "" {
			t.Fatalf("unavailable LOC provenance is not explicit for %s", file.BeforePath)
		}
		if len(file.AfterPaths) == 0 {
			t.Fatalf("baseline file %s has no successor", file.BeforePath)
		}
		for _, successor := range file.AfterPaths {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(successor))); err != nil {
				t.Fatalf("successor %s for %s: %v", successor, file.BeforePath, err)
			}
			constraint, recorded := file.AfterBuildConstraints[successor]
			if !recorded {
				t.Fatalf("successor %s has no recorded build constraint", successor)
			}
			if file.PlatformClass == "unix" && (constraint == nil || *constraint == "") {
				t.Fatalf("Unix baseline %s moved to unconstrained %s", file.BeforePath, successor)
			}
		}
	}

	beforeFunctions := make(map[string]bool, len(manifest.Functions))
	afterFunctions := make(map[string]bool, len(manifest.Functions))
	parsedFiles := map[string]map[string]bool{}
	for _, function := range manifest.Functions {
		beforeKey := function.BeforeFile + "\x00" + function.BeforeName
		afterKey := function.AfterFile + "\x00" + function.AfterName
		if beforeFunctions[beforeKey] || afterFunctions[afterKey] {
			t.Fatalf("function mapping is not one-to-one: %s -> %s", beforeKey, afterKey)
		}
		beforeFunctions[beforeKey], afterFunctions[afterKey] = true, true
		if !beforeFiles[function.BeforeFile] {
			t.Fatalf("function references unknown baseline file %s", function.BeforeFile)
		}
		inventory, ok := parsedFiles[function.AfterFile]
		if !ok {
			inventory = parseTopLevelTests(t, filepath.Join(root, filepath.FromSlash(function.AfterFile)))
			parsedFiles[function.AfterFile] = inventory
		}
		if !inventory[function.AfterName] {
			t.Fatalf("successor function %s is absent from %s", function.AfterName, function.AfterFile)
		}
	}

	baselineTotal, successorTotal := 0, 0
	for _, inventory := range manifest.PackageInventory {
		if inventory.BaselineFunctions != inventory.SuccessorFunctions {
			t.Fatalf("package %s inventory changed: %d -> %d", inventory.Package, inventory.BaselineFunctions, inventory.SuccessorFunctions)
		}
		baselineTotal += inventory.BaselineFunctions
		successorTotal += inventory.SuccessorFunctions
	}
	if baselineTotal != 401 || successorTotal != 401 {
		t.Fatalf("package inventory totals = %d/%d, want 401/401", baselineTotal, successorTotal)
	}
}

func TestV02TaskSpecificationsAreTrackedAndSelfContained(t *testing.T) {
	root := repositoryRoot(t)
	taskRoot := filepath.Join(root, "docs", "development", "v0.2", "tasks")
	for number := 9; number <= 21; number++ {
		name := "task" + twoDigits(number) + ".md"
		raw, err := os.ReadFile(filepath.Join(taskRoot, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(raw), ".cursor/task") {
			t.Fatalf("%s retains ignored .cursor dependency", name)
		}
	}
}

func TestV02CanonicalFixturesContainNoRuntimeState(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "fixtures", "v02")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".okf" {
			t.Errorf("canonical fixture contains runtime state: %s", path)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func parseTopLevelTests(t *testing.T, path string) map[string]bool {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	result := map[string]bool{}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil {
			continue
		}
		if strings.HasPrefix(function.Name.Name, "Test") || strings.HasPrefix(function.Name.Name, "Fuzz") {
			result[function.Name.Name] = true
		}
	}
	return result
}

func twoDigits(number int) string {
	return string([]byte{'0' + byte(number/10), '0' + byte(number%10)})
}
