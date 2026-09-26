package okf_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/validator"
)

func TestPinnedTemporalSpecificationsAndProfileCorpus(t *testing.T) {
	// Arrange.
	cases := []struct {
		name, specFile, lockFile, digest string
		profile                          bundle.TemporalProfile
	}{
		{"date", "spec-v02.md", "spec-lock.json", "5a3311d270bebb16d558010e75064f5b75323f284992641732b1c8097511f948", bundle.TemporalProfileDate},
		{"instant", "spec-v02-instant.md", "spec-lock-instant.json", "26aa5da029278939f914e578107242d9607d4f2dc5fe153272b82f9ed1030101", bundle.TemporalProfileInstant},
	}
	manifestBytes, err := os.ReadFile(filepath.Join("fixtures", "v02", "temporal-profiles", "corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Path            string `json:"path"`
			TemporalProfile string `json:"temporal_profile"`
			SpecLock        string `json:"spec_lock"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Cases) != len(cases) {
		t.Fatalf("temporal corpus manifest cases = %d, want %d", len(manifest.Cases), len(cases))
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			var bindingFound bool
			for _, binding := range manifest.Cases {
				if binding.Path == test.name && binding.TemporalProfile == string(test.profile) && binding.SpecLock == "../"+test.lockFile {
					bindingFound = true
				}
			}
			if !bindingFound {
				t.Fatalf("manifest does not bind %s to %s and %s", test.name, test.profile, test.lockFile)
			}
			spec, err := os.ReadFile(filepath.Join("skills", "open-knowledge-format", "references", test.specFile))
			if err != nil {
				t.Fatal(err)
			}
			lock, err := os.ReadFile(filepath.Join("fixtures", "v02", test.lockFile))
			if err != nil {
				t.Fatal(err)
			}
			var metadata struct {
				SHA256 string `json:"sha256"`
			}
			if err := json.Unmarshal(lock, &metadata); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join("fixtures", "v02", "temporal-profiles", test.name)
			loaded, err := bundle.LoadBundle(root)
			if err != nil {
				t.Fatal(err)
			}
			reference := time.Date(2026, 9, 23, 11, 0, 0, 123456789, time.UTC)

			// Act.
			actualDigest := sha256.Sum256(spec)
			report := validator.ValidateBundle(loaded, &validator.ValidatorConfig{Strict: true, TemporalProfile: test.profile, ReferenceDate: reference})

			// Assert.
			if got := hex.EncodeToString(actualDigest[:]); got != test.digest || metadata.SHA256 != test.digest {
				t.Fatalf("pin drift: local=%s lock=%s expected=%s", got, metadata.SHA256, test.digest)
			}
			if report.TemporalProfile != test.profile || report.ErrorCount() != 0 {
				t.Fatalf("profile report = %#v", report)
			}
			for _, diag := range report.Diagnostics {
				switch diag.Code {
				case "source_field_invalid", "usage_window_invalid", "usage_window_order", "stale_after_invalid":
					t.Fatalf("temporal fixture rejected: %#v", diag)
				}
			}
		})
	}
}

func TestNewPinnedAppendixATemporalExamplesUseInstantProfile(t *testing.T) {
	// Arrange: extract the three exact Markdown examples from the pinned
	// Appendix A, rather than a hand-transcribed approximation.
	spec, err := os.ReadFile(filepath.Join("skills", "open-knowledge-format", "references", "spec-v02-instant.md"))
	if err != nil {
		t.Fatal(err)
	}
	appendix := string(spec)
	start := strings.Index(appendix, "### v0.2 form")
	if start < 0 {
		t.Fatal("pinned Appendix A v0.2 form missing")
	}
	appendix = appendix[start:]
	const open = "```markdown\n"
	var examples []string
	for len(examples) < 3 {
		begin := strings.Index(appendix, open)
		if begin < 0 {
			break
		}
		appendix = appendix[begin+len(open):]
		end := strings.Index(appendix, "\n```")
		if end < 0 {
			t.Fatal("unterminated Appendix A Markdown example")
		}
		examples = append(examples, appendix[:end]+"\n")
		appendix = appendix[end+len("\n```"):]
	}
	if len(examples) != 3 {
		t.Fatalf("Appendix A examples = %d, want 3", len(examples))
	}

	for index, raw := range examples {
		// Act.
		document, err := bundle.ParseDocument(raw)
		if err != nil {
			t.Fatalf("example %d parse: %v", index, err)
		}
		stale, err := document.Frontmatter.StaleAfterForProfileContext(t.Context(), bundle.TemporalProfileInstant)
		if err != nil {
			t.Fatal(err)
		}
		sources, err := document.Frontmatter.SourceStatesForProfileContext(t.Context(), bundle.TemporalProfileInstant)
		if err != nil {
			t.Fatal(err)
		}
		window, err := document.Frontmatter.UsageWindowForProfileContext(t.Context(), bundle.TemporalProfileInstant)
		if err != nil {
			t.Fatal(err)
		}

		// Assert.
		if stale.Value.State != bundle.TemporalValid {
			t.Fatalf("Appendix A example %d stale_after=%#v", index, stale)
		}
		for sourceIndex, source := range sources {
			if !source.Valid {
				t.Fatalf("Appendix A example %d source %d = %#v", index, sourceIndex, source)
			}
		}
		if index == 1 {
			if len(sources) != 2 || !window.Valid || sources[0].LastModified.State != bundle.TemporalValid || sources[1].LastModified.State != bundle.TemporalValid {
				t.Fatalf("Appendix A revenue temporal values: sources=%#v window=%#v", sources, window)
			}
		}
	}
}
