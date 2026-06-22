package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func TestMainFunctionUsesRunExitCode(t *testing.T) {
	// Arrange.
	oldArgs := os.Args
	oldExit := exit
	oldStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer func() {
		os.Args = oldArgs
		exit = oldExit
		os.Stdout = oldStdout
		_ = readEnd.Close()
	}()

	os.Args = []string{"okf", "help"}
	os.Stdout = writeEnd
	exit = func(code int) {
		panic(exitCode(code))
	}

	// Act.
	got := catchExit(main)
	_ = writeEnd.Close()
	output, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}

	// Assert.
	if got != 0 {
		t.Fatalf("main() exit code = %d, want 0", got)
	}
	if !strings.Contains(string(output), "USAGE:") {
		t.Fatalf("stdout = %q, want usage", output)
	}
}

func TestRunMetaCommands(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantOut    string
		wantErrOut string
	}{
		{name: "no args", wantCode: 1, wantErrOut: "USAGE:"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantOut: "COMMANDS:"},
		{name: "version", args: []string{"version"}, wantCode: 0, wantOut: "OKF spec v0.1"},
		{name: "unknown", args: []string{"wat"}, wantCode: 1, wantErrOut: "unknown subcommand: wat"},
		{name: "validate unexpected positional", args: []string{"validate", "bundle"}, wantCode: 1, wantErrOut: "unexpected validate argument"},
		{name: "info missing positional", args: []string{"info"}, wantCode: 1, wantErrOut: "missing <bundle>"},
		{name: "index missing positional", args: []string{"index"}, wantCode: 1, wantErrOut: "missing <bundle>"},
		{name: "graph missing positional", args: []string{"graph"}, wantCode: 1, wantErrOut: "missing <bundle>"},
		{name: "parse missing positional", args: []string{"parse"}, wantCode: 1, wantErrOut: "missing <file>"},
		{name: "fmt missing positional", args: []string{"fmt"}, wantCode: 1, wantErrOut: "missing <file>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			var stdout, stderr bytes.Buffer

			// Act.
			code := run(tt.args, &stdout, &stderr)

			// Assert.
			if code != tt.wantCode {
				t.Fatalf("run(%v) code = %d, want %d", tt.args, code, tt.wantCode)
			}
			if tt.wantOut != "" && !strings.Contains(stdout.String(), tt.wantOut) {
				t.Fatalf("stdout = %q, want fragment %q", stdout.String(), tt.wantOut)
			}
			if tt.wantErrOut != "" && !strings.Contains(stderr.String(), tt.wantErrOut) {
				t.Fatalf("stderr = %q, want fragment %q", stderr.String(), tt.wantErrOut)
			}
		})
	}
}

func TestRunValidateInfoAndGraph(t *testing.T) {
	// Arrange.
	t.Setenv("NO_COLOR", "")
	root := sampleBundle(t)

	// Act.
	validateCode, validateOut, validateErr := runCommand("validate", "-path", root, "-check-links")
	infoCode, infoOut, infoErr := runCommand("info", root)
	graphCode, graphOut, graphErr := runCommand("graph", root)
	dotCode, dotOut, dotErr := runCommand("graph", root, "--dot")
	dotFormatCode, dotFormatOut, dotFormatErr := runCommand("graph", root, "-format", "dot")
	dotEqualsCode, dotEqualsOut, dotEqualsErr := runCommand("graph", root, "-format=dot")
	textFormatCode, textFormatOut, textFormatErr := runCommand("graph", root, "-format", "text")
	textEqualsCode, textEqualsOut, textEqualsErr := runCommand("graph", root, "-format=text")
	mermaidCode, mermaidOut, mermaidErr := runCommand("graph", root, "-format", "mermaid")

	// Assert.
	if validateCode != 0 {
		t.Fatalf("validate code = %d, stderr = %q", validateCode, validateErr)
	}
	assertContains(t, validateOut, "Validating bundle: "+root)
	assertContains(t, validateOut, "[INFO] a.md: broken link")
	assertContains(t, validateOut, "Result: PASS (0 errors, 0 warnings, 1 info)")

	if infoCode != 0 {
		t.Fatalf("info code = %d, stderr = %q", infoCode, infoErr)
	}
	assertContains(t, infoOut, "concepts:   2")
	assertContains(t, infoOut, "okf_version: 0.1")
	assertContains(t, infoOut, "links:      2 internal (1 broken)")
	assertContains(t, infoOut, "     2  Note")

	if graphCode != 0 {
		t.Fatalf("graph code = %d, stderr = %q", graphCode, graphErr)
	}
	assertContains(t, graphOut, "a\n")
	assertContains(t, graphOut, "  -> b")
	assertContains(t, graphOut, "  -x missing")

	if dotCode != 0 {
		t.Fatalf("graph --dot code = %d, stderr = %q", dotCode, dotErr)
	}
	assertContains(t, dotOut, "digraph okf")
	assertContains(t, dotOut, `"a" -> "missing" [style=dashed, color=red];`)

	if dotFormatCode != 0 {
		t.Fatalf("graph -format dot code = %d, stderr = %q", dotFormatCode, dotFormatErr)
	}
	if dotEqualsCode != 0 {
		t.Fatalf("graph -format=dot code = %d, stderr = %q", dotEqualsCode, dotEqualsErr)
	}
	if dotFormatOut != dotOut || dotEqualsOut != dotOut {
		t.Fatalf("explicit dot output mismatch\n--dot:\n%s\n-format dot:\n%s\n-format=dot:\n%s", dotOut, dotFormatOut, dotEqualsOut)
	}

	if textFormatCode != 0 {
		t.Fatalf("graph -format text code = %d, stderr = %q", textFormatCode, textFormatErr)
	}
	if textEqualsCode != 0 {
		t.Fatalf("graph -format=text code = %d, stderr = %q", textEqualsCode, textEqualsErr)
	}
	if textFormatOut != graphOut || textEqualsOut != graphOut {
		t.Fatalf("explicit text output mismatch\ndefault:\n%s\n-format text:\n%s\n-format=text:\n%s", graphOut, textFormatOut, textEqualsOut)
	}

	if mermaidCode != 0 {
		t.Fatalf("graph -format mermaid code = %d, stderr = %q", mermaidCode, mermaidErr)
	}
	if mermaidErr != "" {
		t.Fatalf("graph -format mermaid stderr = %q, want empty", mermaidErr)
	}
	assertContains(t, mermaidOut, "graph LR\n")
	assertContains(t, mermaidOut, `n0["a"] --> n1["b"]`)
	assertContains(t, mermaidOut, `n0["a"] -.->|"404"| n2["missing"]`)
}

func TestRunGraphArgumentOrder(t *testing.T) {
	// Arrange.
	root := sampleBundle(t)

	// Act.
	suffixFlagCode, suffixFlagOut, suffixFlagErr := runCommand("graph", root, "-format", "mermaid")
	prefixFlagCode, prefixFlagOut, prefixFlagErr := runCommand("graph", "-format", "mermaid", root)
	suffixEqualsCode, suffixEqualsOut, suffixEqualsErr := runCommand("graph", root, "-format=mermaid")
	prefixEqualsCode, prefixEqualsOut, prefixEqualsErr := runCommand("graph", "-format=mermaid", root)

	// Assert.
	if suffixFlagCode != 0 {
		t.Fatalf("graph root -format mermaid code = %d, stderr = %q", suffixFlagCode, suffixFlagErr)
	}
	if prefixFlagCode != 0 {
		t.Fatalf("graph -format mermaid root code = %d, stderr = %q", prefixFlagCode, prefixFlagErr)
	}
	if suffixEqualsCode != 0 {
		t.Fatalf("graph root -format=mermaid code = %d, stderr = %q", suffixEqualsCode, suffixEqualsErr)
	}
	if prefixEqualsCode != 0 {
		t.Fatalf("graph -format=mermaid root code = %d, stderr = %q", prefixEqualsCode, prefixEqualsErr)
	}
	if prefixFlagOut != suffixFlagOut || suffixEqualsOut != suffixFlagOut || prefixEqualsOut != suffixFlagOut {
		t.Fatalf("mermaid output differs by argument order\nsuffix flag:\n%s\nprefix flag:\n%s\nsuffix equals:\n%s\nprefix equals:\n%s", suffixFlagOut, prefixFlagOut, suffixEqualsOut, prefixEqualsOut)
	}
}

func TestRunGraphDashPrefixedBundleAfterTerminator(t *testing.T) {
	// Arrange.
	parent := t.TempDir()
	root := filepath.Join(parent, "-bundle-dir")
	writeSampleBundle(t, root)
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("Chdir(%s) error = %v", parent, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("Chdir(%s) cleanup error = %v", oldWd, err)
		}
	})

	// Act.
	code, stdout, stderr := runCommand("graph", "--", "-bundle-dir")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -- -bundle-dir code = %d, stderr = %q", code, stderr)
	}
	assertContains(t, stdout, "a\n")
	assertContains(t, stdout, "  -> b")
}

func TestRunGraphFormatErrors(t *testing.T) {
	// Arrange.
	root := sampleBundle(t)

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "unknown format", args: []string{"graph", root, "-format", "unknown"}, wantErr: "unsupported graph format: unknown"},
		{name: "format conflict", args: []string{"graph", root, "--dot", "-format", "mermaid"}, wantErr: "cannot use --dot with -format=mermaid"},
		{name: "jsonld format conflict", args: []string{"graph", root, "--dot", "-format", "json-ld"}, wantErr: "cannot use --dot with -format=json-ld"},
		{name: "ntriples format conflict", args: []string{"graph", root, "--dot", "-format", "ntriples"}, wantErr: "cannot use --dot with -format=ntriples"},
		{name: "missing bundle", args: []string{"graph"}, wantErr: "missing <bundle>"},
		{name: "extra positional", args: []string{"graph", root, "extra"}, wantErr: "unexpected graph argument: extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			code, _, stderr := runCommand(tt.args...)

			// Assert.
			if code != 1 {
				t.Fatalf("runCommand(%v) code = %d, want 1", tt.args, code)
			}
			assertContains(t, stderr, tt.wantErr)
		})
	}

	// Act.
	dotFalseCode, dotFalseOut, dotFalseErr := runCommand("graph", root, "--dot=false", "-format=mermaid")

	// Assert.
	if dotFalseCode != 0 {
		t.Fatalf("graph --dot=false -format=mermaid code = %d, stderr = %q", dotFalseCode, dotFalseErr)
	}
	assertContains(t, dotFalseOut, "graph LR\n")
	if strings.Contains(dotFalseOut, "digraph okf") {
		t.Fatalf("--dot=false overrode -format=mermaid:\n%s", dotFalseOut)
	}

	// Act.
	jsonldDotFalseCode, jsonldDotFalseOut, jsonldDotFalseErr := runCommand("graph", root, "--dot=false", "-format=json-ld")

	// Assert.
	if jsonldDotFalseCode != 0 {
		t.Fatalf("graph --dot=false -format=json-ld code = %d, stderr = %q", jsonldDotFalseCode, jsonldDotFalseErr)
	}
	_ = decodeJSONLD(t, jsonldDotFalseOut)
	if strings.Contains(jsonldDotFalseOut, "digraph okf") {
		t.Fatalf("--dot=false overrode -format=json-ld:\n%s", jsonldDotFalseOut)
	}

	// Act.
	ntriplesDotFalseCode, ntriplesDotFalseOut, ntriplesDotFalseErr := runCommand("graph", root, "--dot=false", "-format=ntriples")

	// Assert.
	if ntriplesDotFalseCode != 0 {
		t.Fatalf("graph --dot=false -format=ntriples code = %d, stderr = %q", ntriplesDotFalseCode, ntriplesDotFalseErr)
	}
	_ = parseNTriples(t, ntriplesDotFalseOut)
	if strings.Contains(ntriplesDotFalseOut, "digraph okf") {
		t.Fatalf("--dot=false overrode -format=ntriples:\n%s", ntriplesDotFalseOut)
	}
}

func TestRunGraphMermaidEscapesLabels(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "complex & \"name]\t.md", "---\ntype: Note\n---\nSee [Target](/target.md) and [Missing](/missing&]\t.md).\n")
	writeTestFile(t, root, "target.md", "---\ntype: Note\n---\nBody.\n")

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "mermaid")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format mermaid code = %d, stderr = %q", code, stderr)
	}
	assertContains(t, stdout, "&amp;")
	assertContains(t, stdout, "&quot;")
	assertContains(t, stdout, "&#93;")
	assertContains(t, stdout, "missing&amp;&#93;")
	if strings.Contains(stdout, "\t") {
		t.Fatalf("mermaid output contains raw tab:\n%s", stdout)
	}
	if strings.Contains(stdout, `["complex & "name]`) {
		t.Fatalf("mermaid output contains raw unescaped label:\n%s", stdout)
	}
}

func TestRunGraphMermaidEmptyGraph(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "mermaid")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format mermaid code = %d, stderr = %q", code, stderr)
	}
	if stdout != "graph LR\n" {
		t.Fatalf("mermaid empty graph stdout = %q, want %q", stdout, "graph LR\n")
	}
}

func TestRunGraphMermaidDoesNotAllocateHiddenNoLinkNodes(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
	writeTestFile(t, root, "b.md", "---\ntype: Note\n---\nSee [C](/c.md).\n")
	writeTestFile(t, root, "c.md", "---\ntype: Note\n---\nBody.\n")

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "mermaid")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format mermaid code = %d, stderr = %q", code, stderr)
	}
	assertContains(t, stdout, `n0["b"] --> n1["c"]`)
	if strings.Contains(stdout, `n0["a"]`) {
		t.Fatalf("mermaid output allocated hidden no-link concept:\n%s", stdout)
	}
}

func TestRunGraphSemanticRelations(t *testing.T) {
	// Arrange.
	t.Setenv("NO_COLOR", "")
	root := semanticRelationsBundle(t)

	// Act.
	textCode, textOut, textErr := runCommand("graph", root, "-format", "text")
	dotCode, dotOut, dotErr := runCommand("graph", root, "-format", "dot")
	mermaidCode, mermaidOut, mermaidErr := runCommand("graph", root, "-format", "mermaid")
	jsonldCode, jsonldOut, jsonldErr := runCommand("graph", root, "-format", "json-ld")
	ntriplesCode, ntriplesOut, ntriplesErr := runCommand("graph", root, "-format", "ntriples")

	// Assert.
	if textCode != 0 {
		t.Fatalf("graph -format text code = %d, stderr = %q", textCode, textErr)
	}
	assertContains(t, textOut, "a\n  -> b\n")
	assertContains(t, textOut, "a#field1\n  => writes_to b#col-2\n  => writes_to b#col-2\n")
	assertContains(t, textOut, "a\n  => depends_on b#section-1\n  =x impacts missing#col\n")

	if dotCode != 0 {
		t.Fatalf("graph -format dot code = %d, stderr = %q", dotCode, dotErr)
	}
	assertContains(t, dotOut, `"a" -> "b";`)
	if strings.Count(dotOut, `"a#field1" -> "b#col-2" [label="writes_to"];`) != 2 {
		t.Fatalf("dot writes_to count = %d, want 2; stdout:\n%s", strings.Count(dotOut, `"a#field1" -> "b#col-2" [label="writes_to"];`), dotOut)
	}
	assertContains(t, dotOut, `"a" -> "b#section-1" [label="depends_on"];`)
	assertContains(t, dotOut, `"a" -> "missing#col" [label="impacts", style=dashed, color=red];`)

	if mermaidCode != 0 {
		t.Fatalf("graph -format mermaid code = %d, stderr = %q", mermaidCode, mermaidErr)
	}
	assertContains(t, mermaidOut, `n0["a"] --> n1["b"]`)
	if strings.Count(mermaidOut, `n2["a#field1"] -->|"writes_to"| n3["b#col-2"]`) != 2 {
		t.Fatalf("mermaid writes_to count = %d, want 2; stdout:\n%s", strings.Count(mermaidOut, `n2["a#field1"] -->|"writes_to"| n3["b#col-2"]`), mermaidOut)
	}
	assertContains(t, mermaidOut, `n0["a"] -->|"depends_on"| n4["b#section-1"]`)
	assertContains(t, mermaidOut, `n0["a"] -.->|"impacts 404"| n5["missing#col"]`)

	if jsonldCode != 0 {
		t.Fatalf("graph -format json-ld code = %d, stderr = %q", jsonldCode, jsonldErr)
	}
	document := decodeRawJSONLD(t, jsonldOut)
	for _, key := range []string{"depends_on", "writes_to", "impacts", "is_part_of"} {
		if _, ok := document.Context[key]; !ok {
			t.Fatalf("@context = %#v, want key %q", document.Context, key)
		}
	}
	a, ok := rawJSONLDNodeByID(document.Graph, "bundle:a")
	if !ok {
		t.Fatalf("@graph = %#v, want bundle:a", document.Graph)
	}
	field, ok := rawJSONLDNodeByID(document.Graph, "bundle:a#field1")
	if !ok {
		t.Fatalf("@graph = %#v, want bundle:a#field1", document.Graph)
	}
	if countRawJSONLDReference(t, a, "bundle:b", true) != 1 {
		t.Fatalf("bundle:a raw references = %#v, want Markdown reference bundle:b", a["references"])
	}
	if countRawJSONLDRelation(t, a, "depends_on", "bundle:b#section-1", true) != 1 {
		t.Fatalf("bundle:a depends_on = %#v, want bundle:b#section-1", a["depends_on"])
	}
	if countRawJSONLDRelation(t, a, "impacts", "bundle:missing#col", false) != 1 {
		t.Fatalf("bundle:a impacts = %#v, want missing target exists=false", a["impacts"])
	}
	if field["@type"] != "okf:SubResource" {
		t.Fatalf("bundle:a#field1 @type = %q, want okf:SubResource", field["@type"])
	}
	if !rawJSONLDPartOf(field, "bundle:a") {
		t.Fatalf("bundle:a#field1 is_part_of = %#v, want bundle:a", field["is_part_of"])
	}
	if countRawJSONLDRelation(t, field, "writes_to", "bundle:b#col-2", true) != 2 {
		t.Fatalf("bundle:a#field1 writes_to = %#v, want duplicate field relations", field["writes_to"])
	}

	if ntriplesCode != 0 {
		t.Fatalf("graph -format ntriples code = %d, stderr = %q", ntriplesCode, ntriplesErr)
	}
	_ = parseNTriples(t, ntriplesOut)
	assertContains(t, ntriplesOut, `<local:bundle:a> <https://okf.io/ontology/v0.1#references> <local:bundle:b> .`)
	assertContains(t, ntriplesOut, `<local:bundle:a#field1> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://okf.io/ontology/v0.1#SubResource> .`)
	if strings.Count(ntriplesOut, `<local:bundle:a#field1> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://okf.io/ontology/v0.1#SubResource> .`) != 1 {
		t.Fatalf("SubResource class facts count mismatch; stdout:\n%s", ntriplesOut)
	}
	assertContains(t, ntriplesOut, `<local:bundle:a#field1> <https://okf.io/ontology/v0.1#is_part_of> <local:bundle:a> .`)
	if strings.Count(ntriplesOut, `<local:bundle:a#field1> <https://okf.io/ontology/v0.1#writes_to> <local:bundle:b#col-2> .`) != 2 {
		t.Fatalf("ntriples writes_to count mismatch; stdout:\n%s", ntriplesOut)
	}
	assertContains(t, ntriplesOut, `<local:bundle:a> <https://okf.io/ontology/v0.1#depends_on> <local:bundle:b#section-1> .`)
	assertContains(t, ntriplesOut, `<local:bundle:a> <https://okf.io/ontology/v0.1#impacts> <local:bundle:missing#col> .`)
}

func TestRunGraphSemanticRelationsNTriplesIRIEncoding(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "api/checkout.md", "---\n"+
		"type: API Endpoint\n"+
		"schema:\n"+
		"  fields:\n"+
		"    - id: payload user\n"+
		"      relations:\n"+
		"        writes_to:\n"+
		"          - target: tables/orders#col customer\n"+
		"---\nBody.\n")
	writeTestFile(t, root, "tables/orders.md", "---\ntype: BigQuery Table\n---\nBody.\n")

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "ntriples")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format ntriples code = %d, stderr = %q", code, stderr)
	}
	_ = parseNTriples(t, stdout)
	assertContains(t, stdout, `<local:bundle:api%2Fcheckout#payload%20user> <https://okf.io/ontology/v0.1#writes_to> <local:bundle:tables%2Forders#col%20customer> .`)
	if strings.Contains(stdout, `<local:bundle:api/checkout#payload user>`) {
		t.Fatalf("ntriples output contains raw slash/space relation IRI:\n%s", stdout)
	}
}

func TestRunGraphSemanticRelationsRejectsControlFragments(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"schema:\n"+
		"  fields:\n"+
		"    - id: \"bad\\x7fsource\"\n"+
		"      relations:\n"+
		"        writes_to:\n"+
		"          - target: b#should-not-emit\n"+
		"relations:\n"+
		"  depends_on:\n"+
		"    - target: \"b#bad\\x7ffragment\"\n"+
		"  reads_from:\n"+
		"    - target: b#ok\n"+
		"---\nBody.\n")
	writeTestFile(t, root, "b.md", "---\ntype: Note\n---\nBody.\n")

	// Act.
	textCode, textOut, textErr := runCommand("graph", root, "-format", "text")
	dotCode, dotOut, dotErr := runCommand("graph", root, "-format", "dot")
	mermaidCode, mermaidOut, mermaidErr := runCommand("graph", root, "-format", "mermaid")
	jsonldCode, jsonldOut, jsonldErr := runCommand("graph", root, "-format", "json-ld")
	ntriplesCode, ntriplesOut, ntriplesErr := runCommand("graph", root, "-format", "ntriples")

	// Assert.
	outputs := []struct {
		name   string
		code   int
		stdout string
		stderr string
	}{
		{name: "text", code: textCode, stdout: textOut, stderr: textErr},
		{name: "dot", code: dotCode, stdout: dotOut, stderr: dotErr},
		{name: "mermaid", code: mermaidCode, stdout: mermaidOut, stderr: mermaidErr},
		{name: "json-ld", code: jsonldCode, stdout: jsonldOut, stderr: jsonldErr},
		{name: "ntriples", code: ntriplesCode, stdout: ntriplesOut, stderr: ntriplesErr},
	}
	for _, output := range outputs {
		if output.code != 0 {
			t.Fatalf("graph -format %s code = %d, stderr = %q", output.name, output.code, output.stderr)
		}
		if strings.Contains(output.stdout, "\x7f") ||
			strings.Contains(output.stdout, "bad") ||
			strings.Contains(output.stdout, "should-not-emit") {
			t.Fatalf("%s output leaked invalid control-fragment relation:\n%s", output.name, output.stdout)
		}
		assertContains(t, output.stdout, "b#ok")
	}
	_ = parseNTriples(t, ntriplesOut)
}

func TestRunGraphJSONLDContract(t *testing.T) {
	// Arrange.
	root := sampleBundle(t)
	calls := []struct {
		name string
		args []string
	}{
		{name: "suffix flag", args: []string{"graph", root, "-format", "json-ld"}},
		{name: "suffix equals", args: []string{"graph", root, "-format=json-ld"}},
		{name: "prefix flag", args: []string{"graph", "-format", "json-ld", root}},
		{name: "long flag", args: []string{"graph", root, "--format", "json-ld"}},
		{name: "long equals", args: []string{"graph", root, "--format=json-ld"}},
	}

	var baseOut string
	var baseNormalized string
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			// Act.
			code, stdout, stderr := runCommand(call.args...)

			// Assert.
			if code != 0 {
				t.Fatalf("runCommand(%v) code = %d, stderr = %q", call.args, code, stderr)
			}
			if stderr != "" {
				t.Fatalf("runCommand(%v) stderr = %q, want empty", call.args, stderr)
			}
			if !strings.HasSuffix(stdout, "\n") {
				t.Fatalf("json-ld stdout does not end with newline: %q", stdout)
			}

			normalized := normalizedJSON(t, stdout)
			if baseNormalized == "" {
				baseNormalized = normalized
				baseOut = stdout
				return
			}
			if normalized != baseNormalized {
				t.Fatalf("json-ld output mismatch\nbase:\n%s\n%s:\n%s", baseOut, call.name, stdout)
			}
		})
	}

	// Act.
	document := decodeJSONLD(t, baseOut)

	// Assert.
	for _, key := range []string{"okf", "bundle", "references", "target", "exists"} {
		if _, ok := document.Context[key]; !ok {
			t.Fatalf("@context = %#v, want key %q", document.Context, key)
		}
	}
	if len(document.Graph) != 2 {
		t.Fatalf("@graph length = %d, want 2", len(document.Graph))
	}

	a, ok := jsonldNodeByID(document, "bundle:a")
	if !ok {
		t.Fatalf("@graph = %#v, want bundle:a", document.Graph)
	}
	b, ok := jsonldNodeByID(document, "bundle:b")
	if !ok {
		t.Fatalf("@graph = %#v, want bundle:b", document.Graph)
	}

	if a.Kind != "okf:Concept" || a.OKFType != "Note" {
		t.Fatalf("bundle:a type fields = %q, %q; want okf:Concept, Note", a.Kind, a.OKFType)
	}
	if a.Title != "A" || a.Description != "Alpha" {
		t.Fatalf("bundle:a metadata = %#v, want title A and description Alpha", a)
	}
	if a.Resource != "https://example.com/a" || a.Timestamp != "2026-06-21T00:00:00Z" {
		t.Fatalf("bundle:a resource/timestamp = %q, %q", a.Resource, a.Timestamp)
	}
	if !reflect.DeepEqual(a.Tags, []string{"alpha"}) {
		t.Fatalf("bundle:a tags = %#v, want [alpha]", a.Tags)
	}
	if countJSONLDReference(a.References, "bundle:b", true) != 1 {
		t.Fatalf("bundle:a references = %#v, want existing bundle:b reference", a.References)
	}
	if countJSONLDReference(a.References, "bundle:missing", false) != 1 {
		t.Fatalf("bundle:a references = %#v, want dangling bundle:missing reference", a.References)
	}
	if len(b.References) != 0 {
		t.Fatalf("bundle:b references = %#v, want none", b.References)
	}
}

func TestRunGraphJSONLDOmitsSemanticContextWithoutRelations(t *testing.T) {
	// Arrange.
	root := sampleBundle(t)

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "json-ld")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format json-ld code = %d, stderr = %q", code, stderr)
	}
	document := decodeRawJSONLD(t, stdout)
	for _, key := range []string{"depends_on", "writes_to", "is_part_of"} {
		if _, ok := document.Context[key]; ok {
			t.Fatalf("@context contains semantic-only key %q without semantic relations: %#v", key, document.Context)
		}
	}
	for _, node := range document.Graph {
		if node["@type"] == "okf:SubResource" {
			t.Fatalf("@graph contains subresource without semantic relations: %#v", document.Graph)
		}
	}
}

func TestRunGraphJSONLDEmptyGraph(t *testing.T) {
	// Arrange.
	root := t.TempDir()

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "json-ld")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format json-ld code = %d, stderr = %q", code, stderr)
	}
	document := decodeJSONLD(t, stdout)
	if document.Graph == nil {
		t.Fatalf("@graph = nil, want non-nil empty slice; stdout:\n%s", stdout)
	}
	if len(document.Graph) != 0 {
		t.Fatalf("@graph length = %d, want 0", len(document.Graph))
	}
}

func TestRunGraphJSONLDOmitsAndSkipsParsedConcepts(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
	writeTestFile(t, root, "no-type.md", "---\ntitle: No Type\n---\nBody.\n")
	writeTestFile(t, root, "bad.md", "---\ntype: [\n---\nBody.\n")

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "json-ld")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format json-ld code = %d, stderr = %q", code, stderr)
	}

	nodes := decodeRawJSONLDNodes(t, stdout)
	a, ok := rawJSONLDNodeByID(nodes, "bundle:a")
	if !ok {
		t.Fatalf("raw nodes = %#v, want bundle:a", nodes)
	}
	if _, ok := a["references"]; ok {
		t.Fatalf("bundle:a raw node = %#v, want no references key", a)
	}
	if _, ok := a["tags"]; ok {
		t.Fatalf("bundle:a raw node = %#v, want no tags key", a)
	}

	noType, ok := rawJSONLDNodeByID(nodes, "bundle:no-type")
	if !ok {
		t.Fatalf("raw nodes = %#v, want bundle:no-type", nodes)
	}
	if noType["@type"] != "okf:Concept" {
		t.Fatalf("bundle:no-type @type = %q, want okf:Concept", noType["@type"])
	}
	if _, ok := noType["type"]; ok {
		t.Fatalf("bundle:no-type raw node = %#v, want no type key", noType)
	}
	if _, ok := rawJSONLDNodeByID(nodes, "bundle:bad"); ok {
		t.Fatalf("raw nodes = %#v, want unparseable bad.md omitted", nodes)
	}
}

func TestRunGraphJSONLDLinkFiltering(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "source.md", "---\ntype: Note\n---\n"+
		"See [Target](/target.md), [Target again](/target.md), [Missing](/missing.md), "+
		"[External](https://example.com), [Section](#section), and [Directory](subdir/).\n")
	writeTestFile(t, root, "target.md", "---\ntype: Note\n---\nBody.\n")
	if err := os.Mkdir(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatalf("Mkdir(subdir) error = %v", err)
	}

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "json-ld")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format json-ld code = %d, stderr = %q", code, stderr)
	}
	document := decodeJSONLD(t, stdout)
	source, ok := jsonldNodeByID(document, "bundle:source")
	if !ok {
		t.Fatalf("@graph = %#v, want bundle:source", document.Graph)
	}
	if len(source.References) != 3 {
		t.Fatalf("bundle:source references = %#v, want 3 internal concept references", source.References)
	}
	if countJSONLDReference(source.References, "bundle:target", true) != 2 {
		t.Fatalf("bundle:source references = %#v, want duplicate target references preserved", source.References)
	}
	if countJSONLDReference(source.References, "bundle:missing", false) != 1 {
		t.Fatalf("bundle:source references = %#v, want dangling missing reference", source.References)
	}
}

func TestRunGraphJSONLDEscapesViaJSON(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	rel := "complex & \"name]\t.md"
	writeTestFile(t, root, rel, "---\n"+
		"type: \"Type & \\\"quoted\\\"\"\n"+
		"title: \"Title & \\\"quote\\\" ]\"\n"+
		"description: \"Line one\\nLine two\\tTabbed\"\n"+
		"resource: \"https://example.com/path?x=1&y=2\"\n"+
		"tags: [\"тег\", \"a&b\", \"quote\\\"tag\", \"tab\\tvalue\"]\n"+
		"timestamp: \"2026-06-21T00:00:00Z\"\n"+
		"---\nBody.\n")

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "json-ld")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format json-ld code = %d, stderr = %q", code, stderr)
	}
	document := decodeJSONLD(t, stdout)
	node, ok := jsonldNodeByID(document, "bundle:complex & \"name]\t")
	if !ok {
		t.Fatalf("@graph = %#v, want escaped-name concept", document.Graph)
	}
	if node.OKFType != "Type & \"quoted\"" {
		t.Fatalf("type = %q, want decoded quoted type", node.OKFType)
	}
	if node.Title != "Title & \"quote\" ]" {
		t.Fatalf("title = %q, want decoded title", node.Title)
	}
	if node.Description != "Line one\nLine two\tTabbed" {
		t.Fatalf("description = %q, want decoded escaped whitespace", node.Description)
	}
	if !reflect.DeepEqual(node.Tags, []string{"тег", "a&b", "quote\"tag", "tab\tvalue"}) {
		t.Fatalf("tags = %#v, want decoded tags", node.Tags)
	}
}

func TestRunGraphNTriplesContract(t *testing.T) {
	// Arrange.
	root := sampleBundle(t)
	calls := []struct {
		name string
		args []string
	}{
		{name: "suffix flag", args: []string{"graph", root, "-format", "ntriples"}},
		{name: "suffix equals", args: []string{"graph", root, "-format=ntriples"}},
		{name: "prefix flag", args: []string{"graph", "-format", "ntriples", root}},
		{name: "long suffix flag", args: []string{"graph", root, "--format", "ntriples"}},
		{name: "long suffix equals", args: []string{"graph", root, "--format=ntriples"}},
		{name: "long prefix flag", args: []string{"graph", "--format", "ntriples", root}},
		{name: "long prefix equals", args: []string{"graph", "--format=ntriples", root}},
	}

	var baseOut string
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			// Act.
			code, stdout, stderr := runCommand(call.args...)

			// Assert.
			if code != 0 {
				t.Fatalf("runCommand(%v) code = %d, stderr = %q", call.args, code, stderr)
			}
			if stderr != "" {
				t.Fatalf("runCommand(%v) stderr = %q, want empty", call.args, stderr)
			}
			if stdout == "" {
				t.Fatalf("runCommand(%v) stdout is empty", call.args)
			}
			if !strings.HasSuffix(stdout, "\n") {
				t.Fatalf("ntriples stdout does not end with newline: %q", stdout)
			}
			_ = parseNTriples(t, stdout)

			if baseOut == "" {
				baseOut = stdout
				return
			}
			if stdout != baseOut {
				t.Fatalf("ntriples output mismatch\nbase:\n%s\n%s:\n%s", baseOut, call.name, stdout)
			}
		})
	}

	// Assert.
	assertContains(t, baseOut, `<local:bundle:a> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://okf.io/ontology/v0.1#Concept> .`)
	for _, predicate := range []string{
		"<https://okf.io/ontology/v0.1#type>",
		"<https://okf.io/ontology/v0.1#title>",
		"<https://okf.io/ontology/v0.1#description>",
		"<https://okf.io/ontology/v0.1#resource>",
		"<https://okf.io/ontology/v0.1#tags>",
		"<https://okf.io/ontology/v0.1#timestamp>",
	} {
		assertContains(t, baseOut, predicate)
	}
	assertContains(t, baseOut, `<local:bundle:a> <https://okf.io/ontology/v0.1#references> <local:bundle:b> .`)
	assertContains(t, baseOut, `<local:bundle:a> <https://okf.io/ontology/v0.1#references> <local:bundle:missing> .`)
}

func TestRunGraphNTriplesOmitsAndSkipsParsedConcepts(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
	writeTestFile(t, root, "no-type.md", "---\ntitle: No Type\n---\nBody.\n")
	writeTestFile(t, root, "bad.md", "---\ntype: [\n---\nBody.\n")

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "ntriples")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format ntriples code = %d, stderr = %q", code, stderr)
	}
	_ = parseNTriples(t, stdout)
	assertContains(t, stdout, `<local:bundle:a> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://okf.io/ontology/v0.1#Concept> .`)
	assertContains(t, stdout, `<local:bundle:no-type> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://okf.io/ontology/v0.1#Concept> .`)
	if strings.Contains(stdout, `<local:bundle:no-type> <https://okf.io/ontology/v0.1#type>`) {
		t.Fatalf("no-type concept emitted OKF type triple:\n%s", stdout)
	}
	if strings.Contains(stdout, "bad") {
		t.Fatalf("unparseable concept emitted in ntriples output:\n%s", stdout)
	}
}

func TestRunGraphNTriplesIRIEncoding(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "datasets/sales report.md", "---\ntype: Note\n---\nBody.\n")
	writeTestFile(t, root, "complex & \"name]?#.md", "---\ntype: Note\n---\nSee [Sales](/datasets/sales report.md).\n")
	writeTestFile(t, root, "unicode/тест.md", "---\ntype: Note\n---\nBody.\n")

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "ntriples")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format ntriples code = %d, stderr = %q", code, stderr)
	}
	_ = parseNTriples(t, stdout)
	assertContains(t, stdout, `<local:bundle:datasets%2Fsales%20report>`)
	assertContains(t, stdout, "<local:bundle:"+url.PathEscape("complex & \"name]?#")+">")
	assertContains(t, stdout, "<local:bundle:"+url.PathEscape("unicode/тест")+">")
	assertContains(t, stdout, `<https://okf.io/ontology/v0.1#references> <local:bundle:datasets%2Fsales%20report> .`)
	if strings.Contains(stdout, "<local:bundle:datasets/sales report>") {
		t.Fatalf("ntriples output contains raw slash/space concept IRI:\n%s", stdout)
	}
}

func TestRunGraphNTriplesEscapesLiterals(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\n"+
		"type: \"Type & \\\"quoted\\\"\"\n"+
		"title: \"Line one\\nLine two\\tTabbed\\rCarriage \\\"quoted\\\" \\\\slash\"\n"+
		"description: \"Unicode тег with controls \\a and \\v\"\n"+
		"tags: [\"alpha\", \"quote\\\"tag\", \"tab\\tvalue\"]\n"+
		"---\nBody.\n")

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "ntriples")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format ntriples code = %d, stderr = %q", code, stderr)
	}
	_ = parseNTriples(t, stdout)
	assertContains(t, stdout, `Line one\nLine two\tTabbed\rCarriage \"quoted\" \\slash`)
	assertContains(t, stdout, `Unicode тег with controls \u0007 and \u000B`)
	assertContains(t, stdout, `tab\tvalue`)
	if strings.Contains(stdout, `\a`) || strings.Contains(stdout, `\v`) {
		t.Fatalf("ntriples output contains invalid Go-style control escape:\n%s", stdout)
	}
	if strings.Contains(stdout, "\t") {
		t.Fatalf("ntriples output contains raw tab:\n%s", stdout)
	}
}

func TestRunGraphNTriplesLinkFilteringAndDuplicates(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "source.md", "---\ntype: Note\n---\n"+
		"See [Target](/target.md), [Target again](/target.md), [Missing](/missing.md), "+
		"[External](https://example.com), [Section](#section), and [Directory](subdir/).\n")
	writeTestFile(t, root, "target.md", "---\ntype: Note\n---\nBody.\n")
	if err := os.Mkdir(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatalf("Mkdir(subdir) error = %v", err)
	}

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "ntriples")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format ntriples code = %d, stderr = %q", code, stderr)
	}
	_ = parseNTriples(t, stdout)
	targetReference := `<local:bundle:source> <https://okf.io/ontology/v0.1#references> <local:bundle:target> .`
	missingReference := `<local:bundle:source> <https://okf.io/ontology/v0.1#references> <local:bundle:missing> .`
	if strings.Count(stdout, targetReference) != 2 {
		t.Fatalf("target reference count = %d, want 2; stdout:\n%s", strings.Count(stdout, targetReference), stdout)
	}
	if strings.Count(stdout, missingReference) != 1 {
		t.Fatalf("missing reference count = %d, want 1; stdout:\n%s", strings.Count(stdout, missingReference), stdout)
	}
	for _, fragment := range []string{"example.com", "section", "subdir"} {
		if strings.Contains(stdout, fragment) {
			t.Fatalf("ntriples output contains filtered link fragment %q:\n%s", fragment, stdout)
		}
	}
}

func TestRunGraphNTriplesEmptyBundle(t *testing.T) {
	// Arrange.
	root := t.TempDir()

	// Act.
	code, stdout, stderr := runCommand("graph", root, "-format", "ntriples")

	// Assert.
	if code != 0 {
		t.Fatalf("graph -format ntriples code = %d, stderr = %q", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("graph -format ntriples stderr = %q, want empty", stderr)
	}
	if stdout != "" {
		t.Fatalf("empty ntriples stdout = %q, want empty", stdout)
	}
}

func TestSkillsLockMatchesOpenKnowledgeFormatSkill(t *testing.T) {
	// Arrange.
	root := repoRoot(t)
	lockPath := filepath.Join(root, "skills-lock.json")
	content, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("ReadFile(skills-lock.json) error = %v", err)
	}
	var lock struct {
		Skills map[string]struct {
			ComputedHash string `json:"computedHash"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(content, &lock); err != nil {
		t.Fatalf("json.Unmarshal(skills-lock.json) error = %v", err)
	}
	entry, ok := lock.Skills["open-knowledge-format"]
	if !ok {
		t.Fatalf("skills-lock.json has no open-knowledge-format entry: %#v", lock.Skills)
	}

	// Act.
	got := computeSkillFolderHash(t, filepath.Join(root, "skills", "open-knowledge-format"))

	// Assert.
	if got != entry.ComputedHash {
		t.Fatalf("skills-lock computedHash = %q, want current skill folder hash %q", entry.ComputedHash, got)
	}
}

func TestRunValidateAndParseNonConformant(t *testing.T) {
	// Arrange.
	t.Setenv("NO_COLOR", "")
	root := t.TempDir()
	writeTestFile(t, root, "bad.md", "---\ntitle: Missing Type\n---\nbody\n")
	file := filepath.Join(root, "bad.md")

	// Act.
	validateCode, validateOut, validateErr := runCommand("validate", "-path", root)
	parseCode, parseOut, parseErr := runCommand("parse", file)

	// Assert.
	if validateCode != 1 {
		t.Fatalf("validate code = %d, want 1; stderr = %q", validateCode, validateErr)
	}
	assertContains(t, validateOut, "Result: FAIL")
	assertContains(t, validateOut, "missing or empty 'type' field")

	if parseCode != 1 {
		t.Fatalf("parse code = %d, want 1; stderr = %q", parseCode, parseErr)
	}
	assertContains(t, parseOut, "frontmatter (1 key(s)):")
	assertContains(t, parseOut, "has non-empty string `type`: false")
}

func TestRunValidateRejectsReservedFileStructure(t *testing.T) {
	// Arrange.
	t.Setenv("NO_COLOR", "1")
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nbody\n")
	writeTestFile(t, root, "nested/index.md", "---\ntype: Listing\n---\n\n# Listing\n")
	writeTestFile(t, root, "log.md", "# Log\n\n## May 22\n* bad date\n")

	// Act.
	code, stdout, stderr := runCommand("validate", "-path", root)

	// Assert.
	if code != 1 {
		t.Fatalf("validate code = %d, want 1; stderr = %q", code, stderr)
	}
	assertContains(t, stdout, "index.md should not contain frontmatter")
	assertContains(t, stdout, "log date heading is not ISO-8601")
	assertContains(t, stdout, "Result: FAIL")
}

func TestRunValidateRespectsNoColor(t *testing.T) {
	// Arrange.
	t.Setenv("NO_COLOR", "1")
	root := sampleBundle(t)

	// Act.
	code, stdout, stderr := runCommand("validate", "-path", root)

	// Assert.
	if code != 0 {
		t.Fatalf("validate code = %d, stderr = %q", code, stderr)
	}
	assertContains(t, stdout, "Result: PASS")
	if strings.Contains(stdout, "\x1b[32m") {
		t.Fatalf("stdout contains ANSI color despite NO_COLOR=1:\n%s", stdout)
	}
}

func TestRunParsePrintsDocumentStructure(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"title: A\n"+
		"tags: [one, two]\n"+
		"meta:\n"+
		"  owner: data\n"+
		"---\n"+
		"See [B](/b.md).\n\n"+
		"# Citations\n\n"+
		"[1] [Source](https://example.com)\n")

	// Act.
	code, stdout, stderr := runCommand("parse", filepath.Join(root, "a.md"))

	// Assert.
	if code != 0 {
		t.Fatalf("parse code = %d, stderr = %q", code, stderr)
	}
	assertContains(t, stdout, "frontmatter (4 key(s)):")
	assertContains(t, stdout, "  tags: [one, two]")
	assertContains(t, stdout, "  meta: {owner: data}")
	assertContains(t, stdout, "body:")
	assertContains(t, stdout, "links (2):")
	assertContains(t, stdout, "[Absolute] B -> /b.md")
	assertContains(t, stdout, "citations (1):")
	assertContains(t, stdout, "[1] [Source](https://example.com)")
}

func TestRunIndexAndFmt(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\ntitle: A\ndescription: Alpha\n---\nbody")
	file := filepath.Join(root, "a.md")

	// Act.
	indexCode, indexOut, indexErr := runCommand("index", root)
	formatCode, formatOut, formatErr := runCommand("fmt", file)
	writeCode, writeOut, writeErr := runCommand("fmt", "-w", file)
	updated, readErr := os.ReadFile(file)

	// Assert.
	if indexCode != 0 {
		t.Fatalf("index code = %d, stderr = %q", indexCode, indexErr)
	}
	assertContains(t, indexOut, "1 index file(s) regenerated.")
	indexText, err := os.ReadFile(filepath.Join(root, "index.md"))
	if err != nil {
		t.Fatalf("ReadFile(index.md) error = %v", err)
	}
	assertContains(t, string(indexText), "# Note")
	assertContains(t, string(indexText), "* [A](a.md) - Alpha")

	if formatCode != 0 {
		t.Fatalf("fmt code = %d, stderr = %q", formatCode, formatErr)
	}
	assertContains(t, formatOut, "---\ntype: Note\n")
	assertContains(t, formatOut, "body\n")

	if writeCode != 0 {
		t.Fatalf("fmt -w code = %d, stderr = %q", writeCode, writeErr)
	}
	assertContains(t, writeOut, "formatted "+file)
	if readErr != nil {
		t.Fatalf("ReadFile(formatted) error = %v", readErr)
	}
	assertContains(t, string(updated), "---\ntype: Note\n")
}

func TestRunErrorPaths(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	missing := filepath.Join(root, "missing.md")
	bad := filepath.Join(root, "bad.md")
	if err := os.WriteFile(bad, []byte("---\ntype: [\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(bad) error = %v", err)
	}

	tests := []struct {
		name     string
		args     []string
		fragment string
	}{
		{name: "info missing bundle", args: []string{"info", filepath.Join(root, "missing")}, fragment: "no such file"},
		{name: "graph missing bundle", args: []string{"graph", filepath.Join(root, "missing")}, fragment: "no such file"},
		{name: "parse missing file", args: []string{"parse", missing}, fragment: "no such file"},
		{name: "parse malformed file", args: []string{"parse", bad}, fragment: "invalid frontmatter"},
		{name: "fmt missing file", args: []string{"fmt", missing}, fragment: "no such file"},
		{name: "fmt malformed file", args: []string{"fmt", bad}, fragment: "invalid frontmatter"},
		{name: "index empty missing bundle", args: []string{"index", filepath.Join(root, "missing")}, fragment: "no index files written"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			var stdout, stderr bytes.Buffer

			// Act.
			code := run(tt.args, &stdout, &stderr)

			// Assert.
			if tt.name == "index empty missing bundle" {
				if code != 0 {
					t.Fatalf("run(%v) code = %d, want 0", tt.args, code)
				}
				assertContains(t, stdout.String(), tt.fragment)
				return
			}
			if code != 1 {
				t.Fatalf("run(%v) code = %d, want 1", tt.args, code)
			}
			assertContains(t, stderr.String(), tt.fragment)
		})
	}
}

func TestRunInfoReportsParseErrors(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "bad.md", "---\ntype: Note\n")

	// Act.
	code, stdout, stderr := runCommand("info", root)

	// Assert.
	if code != 0 {
		t.Fatalf("info code = %d, stderr = %q", code, stderr)
	}
	assertContains(t, stdout, "concepts:   0")
	assertContains(t, stdout, "unparseable files:")
	assertContains(t, stdout, "unterminated frontmatter")
}

func TestPositionalSupportsDashSeparatedPath(t *testing.T) {
	// Arrange.
	args := []string{"-w", "--", "-file.md"}

	// Act.
	got, err := positional(args, "<file>")

	// Assert.
	if err != nil {
		t.Fatalf("positional() error = %v", err)
	}
	if got != "-file.md" {
		t.Fatalf("positional() = %q, want -file.md", got)
	}
}

func TestCLIFormattingHelpers(t *testing.T) {
	tests := []struct {
		kind bundle.LinkKind
		want string
	}{
		{kind: bundle.LinkAbsolute, want: "Absolute"},
		{kind: bundle.LinkRelative, want: "Relative"},
		{kind: bundle.LinkExternal, want: "External"},
		{kind: bundle.LinkAnchor, want: "Anchor"},
		{kind: bundle.LinkOther, want: "Other"},
		{kind: bundle.LinkKind(99), want: "Other"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			// Act.
			got := cliLinkKind(tt.kind)

			// Assert.
			if got != tt.want {
				t.Fatalf("cliLinkKind(%v) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}

	// Arrange.
	unknown := &yaml.Node{Kind: yaml.AliasNode}

	// Act / Assert.
	if got := formatYAMLValue(nil); got != "" {
		t.Fatalf("formatYAMLValue(nil) = %q, want empty", got)
	}
	if got := formatYAMLValue(unknown); got != "" {
		t.Fatalf("formatYAMLValue(unknown) = %q, want empty", got)
	}
}

type exitCode int

func catchExit(fn func()) (code int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if exit, ok := recovered.(exitCode); ok {
				code = int(exit)
				return
			}
			panic(recovered)
		}
	}()
	fn()
	return -1
}

func runCommand(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func sampleBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSampleBundle(t, root)
	return root
}

func semanticRelationsBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"schema:\n"+
		"  fields:\n"+
		"    - id: field1\n"+
		"      relations:\n"+
		"        writes_to:\n"+
		"          - target: b#col-2\n"+
		"          - target: b#col-2\n"+
		"relations:\n"+
		"  depends_on:\n"+
		"    - target: b#section-1\n"+
		"  impacts:\n"+
		"    - target: missing#col\n"+
		"---\nSee [B](b.md).\n")
	writeTestFile(t, root, "b.md", "---\ntype: Note\n---\nBody.\n")
	return root
}

func writeSampleBundle(t *testing.T, root string) {
	t.Helper()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n\n# Note\n\n* [A](a.md) - Alpha\n* [B](b.md) - Beta\n")
	writeTestFile(t, root, "log.md", "# Log\n\n## 2026-06-21\n* **Update**: Created bundle.\n")
	writeTestFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"title: A\n"+
		"description: Alpha\n"+
		"resource: https://example.com/a\n"+
		"tags: [alpha]\n"+
		"timestamp: 2026-06-21T00:00:00Z\n"+
		"---\n"+
		"See [B](/b.md) and [Missing](/missing.md).\n")
	writeTestFile(t, root, "b.md", "---\n"+
		"type: Note\n"+
		"title: B\n"+
		"description: Beta\n"+
		"resource: https://example.com/b\n"+
		"tags: [beta]\n"+
		"timestamp: 2026-06-21T00:00:00Z\n"+
		"---\n"+
		"Body.\n")
}

func writeTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", rel, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller() failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func computeSkillFolderHash(t *testing.T, skillDir string) string {
	t.Helper()
	type skillFile struct {
		relativePath string
		content      []byte
	}
	var files []skillFile
	err := filepath.WalkDir(skillDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		files = append(files, skillFile{
			relativePath: filepath.ToSlash(relativePath),
			content:      content,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s) error = %v", skillDir, err)
	}
	sort.Slice(files, func(i, j int) bool {
		left := strings.ToLower(files[i].relativePath)
		right := strings.ToLower(files[j].relativePath)
		if left != right {
			return left < right
		}
		return files[i].relativePath < files[j].relativePath
	})
	hash := sha256.New()
	for _, file := range files {
		if _, err := hash.Write([]byte(file.relativePath)); err != nil {
			t.Fatalf("hash.Write(relativePath) error = %v", err)
		}
		if _, err := hash.Write(file.content); err != nil {
			t.Fatalf("hash.Write(content) error = %v", err)
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func assertContains(t *testing.T, got, fragment string) {
	t.Helper()
	if !strings.Contains(got, fragment) {
		t.Fatalf("got:\n%s\nwant fragment:\n%s", got, fragment)
	}
}

type jsonldTestDocument struct {
	Context map[string]any   `json:"@context"`
	Graph   []jsonldTestNode `json:"@graph"`
}

type rawJSONLDDocument struct {
	Context map[string]any   `json:"@context"`
	Graph   []map[string]any `json:"@graph"`
}

type jsonldTestNode struct {
	ID          string                `json:"@id"`
	Kind        string                `json:"@type"`
	OKFType     string                `json:"type,omitempty"`
	Title       string                `json:"title,omitempty"`
	Description string                `json:"description,omitempty"`
	Resource    string                `json:"resource,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Timestamp   string                `json:"timestamp,omitempty"`
	References  []jsonldTestReference `json:"references,omitempty"`
}

type jsonldTestReference struct {
	Kind   string `json:"@type"`
	Target string `json:"target"`
	Exists bool   `json:"exists"`
}

func normalizedJSON(t *testing.T, text string) string {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; text:\n%s", err, text)
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(normalized)
}

func decodeJSONLD(t *testing.T, text string) jsonldTestDocument {
	t.Helper()
	var document jsonldTestDocument
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		t.Fatalf("json.Unmarshal(jsonldDocument) error = %v; text:\n%s", err, text)
	}
	return document
}

func decodeRawJSONLDNodes(t *testing.T, text string) []map[string]any {
	t.Helper()
	document := decodeRawJSONLD(t, text)
	return document.Graph
}

func decodeRawJSONLD(t *testing.T, text string) rawJSONLDDocument {
	t.Helper()
	var document rawJSONLDDocument
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		t.Fatalf("json.Unmarshal(raw jsonldDocument) error = %v; text:\n%s", err, text)
	}
	return document
}

func jsonldNodeByID(document jsonldTestDocument, id string) (jsonldTestNode, bool) {
	for _, node := range document.Graph {
		if node.ID == id {
			return node, true
		}
	}
	return jsonldTestNode{}, false
}

func rawJSONLDNodeByID(nodes []map[string]any, id string) (map[string]any, bool) {
	for _, node := range nodes {
		if node["@id"] == id {
			return node, true
		}
	}
	return nil, false
}

func countJSONLDReference(references []jsonldTestReference, target string, exists bool) int {
	count := 0
	for _, reference := range references {
		if reference.Kind == "okf:Reference" && reference.Target == target && reference.Exists == exists {
			count++
		}
	}
	return count
}

func countRawJSONLDReference(t *testing.T, node map[string]any, target string, exists bool) int {
	t.Helper()
	return countRawJSONLDRelationEntries(t, node["references"], target, exists)
}

func countRawJSONLDRelation(t *testing.T, node map[string]any, relationType, target string, exists bool) int {
	t.Helper()
	return countRawJSONLDRelationEntries(t, node[relationType], target, exists)
}

func countRawJSONLDRelationEntries(t *testing.T, value any, target string, exists bool) int {
	t.Helper()
	entries, ok := value.([]any)
	if !ok {
		return 0
	}
	count := 0
	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("json-ld relation entry = %#v, want object", entry)
		}
		entryID, _ := object["@id"].(string)
		if entryID == "" {
			entryID, _ = object["target"].(string)
		}
		entryExists, _ := object["exists"].(bool)
		if entryID == target && entryExists == exists {
			count++
		}
	}
	return count
}

func rawJSONLDPartOf(node map[string]any, target string) bool {
	object, ok := node["is_part_of"].(map[string]any)
	if !ok {
		return false
	}
	id, _ := object["@id"].(string)
	return id == target
}

type ntripleTestTriple struct {
	Subject   string
	Predicate string
	Object    string
}

const (
	ntriplesTestOntologyPrefix = "https://okf.io/ontology/v0.1#"
	ntriplesTestBundlePrefix   = "local:bundle:"
	ntriplesTestRDFType        = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	ntriplesTestSubResource    = ntriplesTestOntologyPrefix + "SubResource"
)

func ntriplesTestIRI(value string) string {
	return "<" + value + ">"
}

func ntriplesTestPredicate(name string) string {
	return ntriplesTestOntologyPrefix + name
}

func parseNTriples(t *testing.T, stdout string) []ntripleTestTriple {
	t.Helper()
	if stdout == "" {
		return nil
	}

	lines := strings.SplitAfter(stdout, "\n")
	triples := make([]ntripleTestTriple, 0, len(lines))
	for i, line := range lines {
		if line == "" {
			if i == len(lines)-1 {
				continue
			}
			t.Fatalf("ntriples output contains empty line before EOF")
		}
		if line == "\n" {
			t.Fatalf("ntriples output contains blank line")
		}
		if !strings.HasSuffix(line, "\n") {
			t.Fatalf("ntriples line does not end with newline: %q", line)
		}
		triples = append(triples, parseNTripleLine(t, strings.TrimSuffix(line, "\n")))
	}
	return triples
}

func parseNTripleLine(t *testing.T, line string) ntripleTestTriple {
	t.Helper()
	if !strings.HasSuffix(line, " .") {
		t.Fatalf("ntriples line = %q, want final space-dot", line)
	}

	body := strings.TrimSuffix(line, " .")
	subject, rest := consumeNTriplesIRI(t, body, "subject")
	if !strings.HasPrefix(rest, " ") {
		t.Fatalf("ntriples line %q missing space after subject", line)
	}
	predicate, rest := consumeNTriplesIRI(t, rest[1:], "predicate")
	if !strings.HasPrefix(rest, " ") {
		t.Fatalf("ntriples line %q missing space after predicate", line)
	}
	predicateValue := strings.TrimSuffix(strings.TrimPrefix(predicate, "<"), ">")
	if predicateValue != ntriplesTestRDFType && !strings.HasPrefix(predicateValue, ntriplesTestOntologyPrefix) {
		t.Fatalf("ntriples line %q has unexpected predicate namespace %q", line, predicate)
	}
	object := rest[1:]
	switch {
	case object == "":
		t.Fatalf("ntriples line %q missing object", line)
	case strings.HasPrefix(object, "<"):
		validateNTriplesIRI(t, object, "object")
	case isValidNTriplesLiteral(object):
	default:
		t.Fatalf("ntriples line %q has invalid object %q", line, object)
	}
	validateNTriplesLocalBundleFragmentUse(t, subject, predicateValue, object)

	return ntripleTestTriple{
		Subject:   subject,
		Predicate: predicate,
		Object:    object,
	}
}

func consumeNTriplesIRI(t *testing.T, text, role string) (string, string) {
	t.Helper()
	if !strings.HasPrefix(text, "<") {
		t.Fatalf("ntriples %s in %q must start with '<'", role, text)
	}
	end := strings.Index(text, ">")
	if end < 0 {
		t.Fatalf("ntriples %s in %q missing closing '>'", role, text)
	}
	iri := text[:end+1]
	validateNTriplesIRI(t, iri, role)
	return iri, text[end+1:]
}

func validateNTriplesIRI(t *testing.T, iri, role string) {
	t.Helper()
	if !strings.HasPrefix(iri, "<") || !strings.HasSuffix(iri, ">") {
		t.Fatalf("ntriples %s IRI = %q, want <...>", role, iri)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(iri, "<"), ">")
	if inner == "" {
		t.Fatalf("ntriples %s IRI is empty", role)
	}
	for _, r := range inner {
		if r <= 0x20 || strings.ContainsRune("<>\"{}|^`\\", r) {
			t.Fatalf("ntriples %s IRI %q contains forbidden raw character %q", role, iri, r)
		}
	}
	if strings.HasPrefix(inner, ntriplesTestBundlePrefix) {
		local := strings.TrimPrefix(inner, ntriplesTestBundlePrefix)
		if strings.Contains(local, "?") {
			t.Fatalf("ntriples local bundle IRI %q contains raw ?", iri)
		}
		if strings.Count(local, "#") > 1 {
			t.Fatalf("ntriples local bundle IRI %q contains multiple raw # delimiters", iri)
		}
		if before, after, ok := strings.Cut(local, "#"); ok && (before == "" || after == "") {
			t.Fatalf("ntriples local bundle IRI %q has empty relation fragment boundary", iri)
		}
	}
}

func validateNTriplesLocalBundleFragmentUse(t *testing.T, subject, predicateValue, object string) {
	t.Helper()
	for _, iri := range []string{subject, object} {
		if !strings.HasPrefix(iri, "<"+ntriplesTestBundlePrefix) || !strings.Contains(iri, "#") {
			continue
		}
		if !allowsRelationFragment(predicateValue, object) {
			t.Fatalf("ntriples local bundle IRI %q contains raw # outside semantic relation context", iri)
		}
	}
}

func allowsRelationFragment(predicateValue, object string) bool {
	switch predicateValue {
	case ntriplesTestRDFType:
		return object == ntriplesTestIRI(ntriplesTestSubResource)
	case ntriplesTestPredicate("is_part_of"):
		return true
	default:
		if strings.HasPrefix(predicateValue, ntriplesTestOntologyPrefix) {
			name := strings.TrimPrefix(predicateValue, ntriplesTestOntologyPrefix)
			switch name {
			case "type", "title", "description", "resource", "tags", "timestamp", "references":
				return false
			default:
				return true
			}
		}
		return false
	}
}

func isValidNTriplesLiteral(literal string) bool {
	if len(literal) < 2 || literal[0] != '"' || literal[len(literal)-1] != '"' {
		return false
	}
	for i := 1; i < len(literal)-1; {
		switch c := literal[i]; {
		case c == '\\':
			if i+1 >= len(literal)-1 {
				return false
			}
			switch literal[i+1] {
			case 't', 'b', 'n', 'r', 'f', '"', '\'', '\\':
				i += 2
			case 'u':
				if !hasHexDigits(literal, i+2, 4) {
					return false
				}
				i += 6
			case 'U':
				if !hasHexDigits(literal, i+2, 8) {
					return false
				}
				i += 10
			default:
				return false
			}
		case c == '"':
			return false
		case c < 0x20:
			return false
		default:
			i++
		}
	}
	return true
}

func hasHexDigits(text string, start, count int) bool {
	if start+count > len(text)-1 {
		return false
	}
	for i := start; i < start+count; i++ {
		c := text[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}

func Example_run_help() {
	code, stdout, _ := runCommand("help")
	flagCode, flagStdout, _ := runCommand("--help")
	fmt.Println(code)
	fmt.Println(flagCode)
	fmt.Println(strings.Contains(stdout, "Open Knowledge Format toolkit"))
	fmt.Println(strings.Contains(stdout, "building, validating, analyzing, and exporting Open Knowledge Format (OKF) bundles"))
	fmt.Println(strings.Contains(stdout, "text|dot|mermaid|json-ld|ntriples"))
	fmt.Println(strings.Contains(flagStdout, "Open Knowledge Format toolkit"))
	fmt.Println(strings.Contains(flagStdout, "building, validating, analyzing, and exporting Open Knowledge Format (OKF) bundles"))
	fmt.Println(strings.Contains(flagStdout, "text|dot|mermaid|json-ld|ntriples"))
	// Output:
	// 0
	// 0
	// true
	// true
	// true
	// true
	// true
	// true
}
