package validator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type interopManifest struct {
	Source  string `json:"source"`
	Commit  string `json:"commit"`
	License string `json:"license"`
	Files   map[string]struct {
		SourcePath    string `json:"source_path"`
		SourceURL     string `json:"source_url"`
		SourceSHA256  string `json:"source_sha256"`
		FixtureSHA256 string `json:"fixture_sha256"`
		Transform     string `json:"transform"`
	} `json:"files"`
	Profiles map[string]struct {
		Strict       bool `json:"strict"`
		CheckOrphans bool `json:"check_orphans"`
		Diagnostics  []struct {
			Code      string `json:"code"`
			Severity  string `json:"severity"`
			File      string `json:"file"`
			FieldPath string `json:"field_path"`
		} `json:"diagnostics"`
	} `json:"profiles"`
}

func TestFrozenForeignInteroperabilityCorpus(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := filepath.Join("..", "fixtures", "interoperability")
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest interopManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Source == "" || len(manifest.Commit) != 40 || manifest.License == "" || len(manifest.Files) != 5 {
		t.Fatalf("incomplete source manifest: %#v", manifest)
	}
	for path, file := range manifest.Files {
		if file.SourcePath == "" || file.SourceURL != manifest.Source+"/blob/"+manifest.Commit+"/"+file.SourcePath || len(file.SourceSHA256) != 64 || file.Transform == "" {
			t.Fatalf("incomplete derivation for %s: %#v", path, file)
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(data)
		if hex.EncodeToString(got[:]) != file.FixtureSHA256 {
			t.Fatalf("fixture drift: %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "LICENSE")); err != nil {
		t.Fatal(err)
	}

	// Act and assert. Codes, severity, and field paths are the contract;
	// human-readable messages are intentionally not pinned.
	for name, profile := range manifest.Profiles {
		name, profile := name, profile
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			report := ValidatePath(filepath.Join(root, "foreign-68ce7a0"), &ValidatorConfig{
				Strict: profile.Strict, CheckOrphans: profile.CheckOrphans,
			})
			if report.ScannedFiles != 5 || !report.IsConformant() {
				t.Fatalf("report = %#v, want 5 files and base conformance", report)
			}
			var got, want []string
			for _, diagnostic := range report.Diagnostics {
				got = append(got, diagnostic.Code+"|"+diagnostic.Severity.String()+"|"+diagnostic.File+"|"+diagnostic.FieldPath)
			}
			for _, diagnostic := range profile.Diagnostics {
				want = append(want, diagnostic.Code+"|"+diagnostic.Severity+"|"+diagnostic.File+"|"+diagnostic.FieldPath)
			}
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("diagnostics = %#v, want %#v", got, want)
			}
		})
	}
}
