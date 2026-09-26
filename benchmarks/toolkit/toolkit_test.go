package toolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/graph"
	"github.com/skosovsky/okf/validator"
)

var resultBundle *bundle.Bundle
var resultDocument bundle.Document
var resultLog bundle.Log
var resultAttributions []bundle.Attribution
var resultBytes []byte

func sizes() []int {
	if os.Getenv("OKF_BENCH_LARGE") == "1" {
		return []int{10, 100, 1000, 10000}
	}
	return []int{10, 100, 1000}
}

func load(t testing.TB, corpus Corpus) *bundle.Bundle {
	t.Helper()
	b, err := bundle.Load(context.Background(), corpus)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.ParseErrors()) != 0 {
		t.Fatalf("parse errors: %v", b.ParseErrors())
	}
	return b
}

func assertReport(t testing.TB, report validator.Report, n int) {
	t.Helper()
	if !report.IsConformant() || report.ScannedFiles != n+(n+99)/100+2 {
		t.Fatalf("validation: files=%d errors=%d diagnostics=%v", report.ScannedFiles, report.ErrorCount(), report.Diagnostics)
	}
}

// TestCorpusContracts is an AAA correctness gate for each benchmark input.
func TestCorpusContracts(t *testing.T) {
	for _, n := range sizes() {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			// Arrange.
			corpus, manifest := Generate(n)
			// Act.
			b := load(t, corpus)
			report, err := validator.ValidateBundleContext(context.Background(), b, &validator.ValidatorConfig{Strict: true, CheckLinks: true, CheckOrphans: true})
			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			assertReport(t, report, n)
			if len(b.Concepts()) != n || manifest.Edges != n || manifest.Sources != n {
				t.Fatalf("wrong corpus dimensions: %+v", manifest)
			}
			if report.WarningCount() != 0 {
				t.Fatalf("unexpected warnings: %v", report.Diagnostics)
			}
			if len(manifest.SHA256) != 64 {
				t.Fatal("missing corpus hash")
			}
		})
	}
}

func TestSpecialCorpusDeterminism(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func() (Corpus, Manifest)
	}{
		{"wrapped-and-code", InteropCorpus},
		{"legacy-date", func() (Corpus, Manifest) { return TemporalCorpus(false) }},
		{"offset-datetime", func() (Corpus, Manifest) { return TemporalCorpus(true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			first, one := tc.make()
			// Act.
			second, two := tc.make()
			b := load(t, first)
			// Assert.
			if one.SHA256 != two.SHA256 || one.Bytes != two.Bytes || len(b.Concepts()) != 10 || !bytes.Equal(first["g000/c00000.md"], second["g000/c00000.md"]) {
				t.Fatalf("special corpus drift: %+v / %+v", one, two)
			}
		})
	}
}

// This correctness gate is enabled only with the corrected binary. Baseline
// 004/005 behavior is unsupported and must not be counted as fast success.
func TestCorrectedInteropContract(t *testing.T) {
	if os.Getenv("OKF_BENCH_EXPECT_CORRECTED") != "1" {
		t.Skip("requires corrected 004/005 commit")
	}
	// Arrange.
	corpus, _ := InteropCorpus()
	b := load(t, corpus)
	// Act.
	parsed := bundle.ParseLog(string(corpus["log.md"]))
	report, err := validator.ValidateBundleContext(context.Background(), b, &validator.ValidatorConfig{Strict: true, CheckLinks: true, CheckOrphans: true})
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Days) != 1 || len(parsed.Days[0].Entries) != 1 || !strings.Contains(parsed.Days[0].Entries[0].Text, "and continues") {
		t.Fatalf("wrapped text lost: %+v", parsed)
	}
	assertReport(t, report, 10)
	if report.WarningCount() != 0 {
		t.Fatalf("phantom footnote or other warning: %+v", report.Diagnostics)
	}
	cli := os.Getenv("OKF_BENCH_CLI")
	if cli == "" {
		t.Fatal("OKF_BENCH_CLI is required for corrected correctness gate")
	}
	for _, instant := range []bool{false, true} {
		temporal, _ := TemporalCorpus(instant)
		root := t.TempDir()
		if err := temporal.WriteDir(root); err != nil {
			t.Fatal(err)
		}
		args := []string{"validate", "--path", root, "--json", "--strict", "--check-links", "--check-orphans"}
		if instant {
			args = append(args, "--temporal-profile", "instant-0b87c52")
		}
		out, err := exec.Command(cli, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("temporal CLI: %v: %s", err, out)
		}
		assertCLIJSON(t, out, 10)
	}
}

// BenchmarkToolkit times only the named library operation. Corpus creation,
// loading, expectation checks, and CLI compilation sit outside the timer.
func BenchmarkToolkit(b *testing.B) {
	for _, n := range sizes() {
		corpus, _ := Generate(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			documentText := string(corpus["g000/c00000.md"])
			b.Run("parse/document", func(b *testing.B) {
				if _, err := bundle.ParseDocument(documentText); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					parsed, err := bundle.ParseDocument(documentText)
					if err != nil {
						b.Fatal(err)
					}
					resultDocument = parsed
				}
			})
			b.Run("load", func(b *testing.B) {
				load(b, corpus)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var err error
					resultBundle, err = bundle.Load(context.Background(), corpus)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
			loaded := load(b, corpus)
			for _, mode := range []struct {
				name string
				cfg  validator.ValidatorConfig
			}{
				{"base", validator.ValidatorConfig{}},
				{"strict", validator.ValidatorConfig{Strict: true}},
				{"links", validator.ValidatorConfig{CheckLinks: true}},
				{"orphans", validator.ValidatorConfig{CheckOrphans: true}},
				{"all", validator.ValidatorConfig{Strict: true, CheckLinks: true, CheckOrphans: true}},
			} {
				b.Run("validate/"+mode.name, func(b *testing.B) {
					preflight, err := validator.ValidateBundleContext(context.Background(), loaded, &mode.cfg)
					if err != nil {
						b.Fatal(err)
					}
					assertReport(b, preflight, n)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						report, err := validator.ValidateBundleContext(context.Background(), loaded, &mode.cfg)
						if err != nil {
							b.Fatal(err)
						}
						resultBytes = []byte{byte(report.ErrorCount())}
					}
				})
			}
			b.Run("graph/jsonld", func(b *testing.B) {
				var out bytes.Buffer
				if err := graph.RenderJSONLDWithOptions(&out, loaded, graph.Options{Profile: graph.ProjectionProfileToolkitV02}); err != nil {
					b.Fatal(err)
				}
				if !bytes.Contains(out.Bytes(), []byte("Concept")) {
					b.Fatal("empty graph")
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					out.Reset()
					if err := graph.RenderJSONLDWithOptions(&out, loaded, graph.Options{Profile: graph.ProjectionProfileToolkitV02}); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				resultBytes = append(resultBytes[:0], out.Bytes()...)
			})
			if n <= 100 { // CLI runs remain an optional, separately labelled process workload.
				cli := os.Getenv("OKF_BENCH_CLI")
				if cli != "" {
					benchmarkCLI(b, cli, corpus, n)
				}
			}
		})
	}
	logText := "# Log\n\n## 2026-09-21\n* One entry\n"
	b.Run("log/typed", func(b *testing.B) {
		if got := bundle.ParseLog(logText); len(got.Days) != 1 || len(got.Days[0].Entries) != 1 {
			b.Fatal("invalid log fixture")
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resultLog = bundle.ParseLog(logText)
		}
	})
	// This case intentionally uses code-owned syntax; expected attribution is
	// one real source and no phantom source from the inline code span.
	text := "---\ntype: Note\nsources: [{id: real, resource: https://example.org/source}]\n---\nClaim [^real] and `code [^fake]`.\n\n[^real]: Evidence.\n"
	doc, err := bundle.ParseDocument(text)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("footnotes/ownership", func(b *testing.B) {
		preflight := doc.Attributions()
		if len(preflight) != 1 || preflight[0].ID != "real" || len(preflight[0].References) != 1 {
			b.Fatalf("wrong attribution: %+v", preflight)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resultAttributions = doc.Attributions()
		}
	})
	if os.Getenv("OKF_BENCH_EXPECT_CORRECTED") == "1" {
		benchmarkCorrectedOnly(b)
	}
}

func benchmarkCLI(b *testing.B, cli string, corpus Corpus, n int) {
	root := b.TempDir()
	if err := corpus.WriteDir(root); err != nil {
		b.Fatal(err)
	}
	args := []string{"validate", "--path", root, "--json", "--strict", "--check-links", "--check-orphans"}
	preflight := exec.Command(cli, args...)
	out, err := preflight.CombinedOutput()
	if err != nil {
		b.Fatalf("CLI preflight: %v: %s", err, out)
	}
	assertCLIJSON(b, out, n)
	b.Run("cli/validate", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			out, err := exec.Command(cli, args...).CombinedOutput()
			if err != nil {
				b.Fatalf("CLI failed: %v: %s", err, out)
			}
			resultBytes = out
		}
	})
}

func assertCLIJSON(t testing.TB, out []byte, n int) {
	t.Helper()
	var response struct {
		Conformant     bool              `json:"conformant"`
		ScannedFiles   int               `json:"scanned_files"`
		Errors         int               `json:"errors"`
		Warnings       int               `json:"warnings"`
		PolicyFailures int               `json:"policy_failures"`
		Diagnostics    []json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(out, &response); err != nil {
		t.Fatalf("CLI JSON preflight: %v: %s", err, out)
	}
	if !response.Conformant || response.ScannedFiles != n+(n+99)/100+2 || response.Errors != 0 || response.Warnings != 0 || response.PolicyFailures != 0 || len(response.Diagnostics) != 0 {
		t.Fatalf("CLI outcome is not the expected clean bundle: %+v", response)
	}
}

func benchmarkCorrectedOnly(b *testing.B) {
	interop, _ := InteropCorpus()
	wrappedText := string(interop["log.md"])
	b.Run("interop/wrapped-log/typed", func(b *testing.B) {
		parsed := bundle.ParseLog(wrappedText)
		if len(parsed.Days) != 1 || len(parsed.Days[0].Entries) != 1 || !strings.Contains(parsed.Days[0].Entries[0].Text, "and continues") {
			b.Fatal("wrapped log contract failed")
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resultLog = bundle.ParseLog(wrappedText)
		}
	})
	b.Run("interop/wrapped-log/validate", func(b *testing.B) {
		loaded := load(b, interop)
		cfg := &validator.ValidatorConfig{Strict: true, CheckLinks: true, CheckOrphans: true}
		preflight, err := validator.ValidateBundleContext(context.Background(), loaded, cfg)
		if err != nil {
			b.Fatal(err)
		}
		assertReport(b, preflight, 10)
		if preflight.WarningCount() != 0 {
			b.Fatal(preflight.Diagnostics)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			report, err := validator.ValidateBundleContext(context.Background(), loaded, cfg)
			if err != nil {
				b.Fatal(err)
			}
			resultBytes = []byte{byte(report.ErrorCount())}
		}
	})
	cli := os.Getenv("OKF_BENCH_CLI")
	if cli == "" {
		b.Fatal("OKF_BENCH_CLI is required for corrected temporal workloads")
	}
	for _, tc := range []struct {
		name    string
		instant bool
	}{{"legacy-date", false}, {"offset-datetime", true}} {
		corpus, _ := TemporalCorpus(tc.instant)
		b.Run("temporal/"+tc.name+"/parse", func(b *testing.B) {
			text := string(corpus["g000/c00000.md"])
			if _, err := bundle.ParseDocument(text); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				parsed, err := bundle.ParseDocument(text)
				if err != nil {
					b.Fatal(err)
				}
				resultDocument = parsed
			}
		})
		b.Run("temporal/"+tc.name+"/cli-validate", func(b *testing.B) {
			root := b.TempDir()
			if err := corpus.WriteDir(root); err != nil {
				b.Fatal(err)
			}
			args := []string{"validate", "--path", root, "--json", "--strict", "--check-links", "--check-orphans"}
			if tc.instant {
				args = append(args, "--temporal-profile", "instant-0b87c52")
			}
			out, err := exec.Command(cli, args...).CombinedOutput()
			if err != nil {
				b.Fatalf("temporal preflight: %v: %s", err, out)
			}
			assertCLIJSON(b, out, 10)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := exec.Command(cli, args...).CombinedOutput()
				if err != nil {
					b.Fatalf("temporal CLI: %v: %s", err, out)
				}
				resultBytes = out
			}
		})
	}
}

func TestCorpusDeterminism(t *testing.T) {
	// Arrange.
	first, one := Generate(10)
	// Act.
	second, two := Generate(10)
	// Assert.
	if one.SHA256 != two.SHA256 || one.Bytes != two.Bytes || !bytes.Equal(first["index.md"], second["index.md"]) {
		t.Fatal("corpus drift")
	}
	if !strings.Contains(string(first["g000/c00000.md"]), "[^s0]") {
		t.Fatal("missing footnote")
	}
	if filepath.IsAbs("g000/c00000.md") {
		t.Fatal("invalid path")
	}
}
