package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/graph"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
	"github.com/skosovsky/okf/validator"
)

func TestListConceptsReturnsDeterministicSummaries(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "zeta.md", "---\ntype: Note\n---\nZeta.\n")
	writeTestFile(t, root, "alpha.md", "---\ntype: Dataset\ntitle: Alpha\n---\nAlpha.\n")

	result := callHandler(t, handleListConcepts, map[string]any{"bundle_path": root})
	if result.IsError {
		t.Fatalf("handleListConcepts() returned error result: %s", resultText(t, result))
	}

	var got listConceptsResponse
	decodeResult(t, result, &got)
	want := []conceptSummary{
		{ID: "alpha", Type: "Dataset", Title: "Alpha", Path: "alpha.md"},
		{ID: "zeta", Type: "Note", Title: "", Path: "zeta.md"},
	}
	if !equalConceptSummaries(got.Concepts, want) {
		t.Fatalf("concepts = %#v, want %#v", got.Concepts, want)
	}
}

func TestLegacyFiveToolTextPayloadsRemainByteExact(t *testing.T) {
	// Frozen from the pre-v0.2 MCP fallback contract. StructuredContent may
	// evolve; these text payload bytes are the backward-compatible surface.
	newReadRoot := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
		return root
	}
	graphGolden := "{\n" +
		"  \"@context\": {\n" +
		"    \"bundle\": \"local:bundle:\",\n" +
		"    \"description\": \"okf:description\",\n" +
		"    \"exists\": \"okf:exists\",\n" +
		"    \"okf\": \"https://okf.io/ontology/v0.1#\",\n" +
		"    \"references\": \"okf:references\",\n" +
		"    \"resource\": \"okf:resource\",\n" +
		"    \"tags\": \"okf:tags\",\n" +
		"    \"target\": {\n" +
		"      \"@id\": \"okf:target\",\n" +
		"      \"@type\": \"@id\"\n" +
		"    },\n" +
		"    \"timestamp\": \"okf:timestamp\",\n" +
		"    \"title\": \"okf:title\",\n" +
		"    \"type\": \"okf:type\"\n" +
		"  },\n" +
		"  \"@graph\": [\n" +
		"    {\n" +
		"      \"@id\": \"bundle:a\",\n" +
		"      \"@type\": \"okf:Concept\",\n" +
		"      \"type\": \"Note\"\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	tests := []struct {
		name string
		run  func(*testing.T) *mcp.CallToolResult
		want string
	}{
		{
			name: "list_concepts",
			run: func(t *testing.T) *mcp.CallToolResult {
				return callHandler(t, handleListConcepts, map[string]any{"bundle_path": newReadRoot(t)})
			},
			want: `{"concepts":[{"id":"a","type":"Note","title":"","path":"a.md"}]}`,
		},
		{
			name: "read_concept",
			run: func(t *testing.T) *mcp.CallToolResult {
				return callHandler(t, handleReadConcept, map[string]any{
					"bundle_path": newReadRoot(t), "concept_id": "a",
				})
			},
			want: "---\ntype: Note\n---\nA.\n",
		},
		{
			name: "validate_bundle",
			run: func(t *testing.T) *mcp.CallToolResult {
				return callHandler(t, handleValidateBundle, map[string]any{"bundle_path": newReadRoot(t)})
			},
			want: `{"scanned_files":1,"conformant":true,"errors":0,"warnings":0,"info":0,"diagnostics":[]}`,
		},
		{
			name: "get_semantic_graph",
			run: func(t *testing.T) *mcp.CallToolResult {
				return callHandler(t, handleSemanticGraph, map[string]any{"bundle_path": newReadRoot(t)})
			},
			want: graphGolden,
		},
		{
			name: "write_concept",
			run: func(t *testing.T) *mcp.CallToolResult {
				root := t.TempDir()
				writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n\n- [A](a.md)\n")
				writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nOld.\n")
				return callHandler(t, handleWriteConcept, map[string]any{
					"bundle_path": root, "concept_id": "a",
					"frontmatter": "type: Note\n", "body": "Updated.\n",
				})
			},
			want: `{"status":"success","path":"a.md","diagnostics":[]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.run(t)
			if result.IsError {
				t.Fatalf("legacy call returned error: %s", resultText(t, result))
			}
			if got := resultText(t, result); got != test.want {
				t.Fatalf("legacy text mismatch\ngot:  %q\nwant: %q", got, test.want)
			}
		})
	}
}

func TestReadOnlyToolsRejectParseErrorsAsToolErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{name: "list", handler: handleListConcepts},
		{name: "graph", handler: handleSemanticGraph},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "bad.md", "---\ntype: [\n---\nBody.\n")

			result := callHandler(t, tt.handler, map[string]any{"bundle_path": root})
			if !result.IsError {
				t.Fatalf("handler returned success, want tool error: %s", resultText(t, result))
			}
			var got struct {
				Status      string          `json:"status"`
				Diagnostics []diagnosticDTO `json:"diagnostics"`
			}
			decodeResult(t, result, &got)
			if got.Status != "error" || len(got.Diagnostics) != 1 {
				t.Fatalf("error response = %#v, want one diagnostic", got)
			}
			if got.Diagnostics[0].File != "bad.md" || got.Diagnostics[0].Severity != "ERROR" {
				t.Fatalf("diagnostic = %#v, want bad.md error", got.Diagnostics[0])
			}
		})
	}
}

func TestReadConceptValidatesPathIDAndSymlinks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")

	result := callHandler(t, handleReadConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "a",
	})
	if result.IsError {
		t.Fatalf("handleReadConcept() returned error result: %s", resultText(t, result))
	}
	if got := resultText(t, result); got != "---\ntype: Note\n---\nA.\n" {
		t.Fatalf("read content = %q", got)
	}

	invalidIDs := []string{
		"",
		" a",
		"/a",
		"a/",
		"a.md",
		"a//b",
		"a\\b",
		"file:///a",
		"http://example.com/a",
		".hidden",
		"nested/index",
		"log",
		"../outside",
	}
	for _, conceptID := range invalidIDs {
		t.Run("invalid "+conceptID, func(t *testing.T) {
			result := callHandler(t, handleReadConcept, map[string]any{
				"bundle_path": root,
				"concept_id":  conceptID,
			})
			if !result.IsError {
				t.Fatalf("concept_id %q returned success", conceptID)
			}
		})
	}

	missing := callHandler(t, handleReadConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "missing",
	})
	if !missing.IsError || !strings.Contains(resultText(t, missing), "concept not found: missing") {
		t.Fatalf("missing result = error %v text %q", missing.IsError, resultText(t, missing))
	}

	skipIfSymlinkUnsupported(t)
	writeTestFile(t, root, "target.md", "---\ntype: Note\n---\nTarget.\n")
	if err := os.Symlink(filepath.Join(root, "target.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	linkResult := callHandler(t, handleReadConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "link",
	})
	if !linkResult.IsError || !strings.Contains(resultText(t, linkResult), "concept not found: link") {
		t.Fatalf("symlink target result = error %v text %q", linkResult.IsError, resultText(t, linkResult))
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked-dir")); err != nil {
		t.Skipf("create symlink dir: %v", err)
	}
	parentResult := callHandler(t, handleReadConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "linked-dir/a",
	})
	if !parentResult.IsError || !strings.Contains(resultText(t, parentResult), "concept not found: linked-dir/a") {
		t.Fatalf("symlink parent result = error %v text %q", parentResult.IsError, resultText(t, parentResult))
	}
}

func TestAllToolsRejectInvalidBundlePaths(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
	filePath := filepath.Join(root, "a.md")
	canonicalTools := map[string]struct{}{
		"list_concepts":         {},
		"read_concept":          {},
		"validate_bundle":       {},
		"get_semantic_graph":    {},
		"write_concept":         {},
		"preview_concept_patch": {},
		"apply_concept_patch":   {},
		"preview_v02_migration": {},
		"apply_v02_migration":   {},
	}

	invalidBundles := []struct {
		name       string
		bundlePath any
		message    string
	}{
		{name: "missing arg", bundlePath: nil, message: "required argument \"bundle_path\" not found"},
		{name: "relative path", bundlePath: "relative", message: "bundle_path must be absolute"},
		{name: "missing path", bundlePath: filepath.Join(root, "missing"), message: "bundle_path is not accessible"},
		{name: "file path", bundlePath: filePath, message: "bundle_path must be a directory"},
	}

	if runtime.GOOS != "windows" {
		linkPath := filepath.Join(t.TempDir(), "bundle-link")
		if err := os.Symlink(root, linkPath); err != nil {
			t.Fatalf("create bundle symlink: %v", err)
		}
		invalidBundles = append(invalidBundles, struct {
			name       string
			bundlePath any
			message    string
		}{name: "symlink root", bundlePath: linkPath, message: "bundle_path must not be a symlink"})
	}

	// Act and assert.
	for _, invalid := range invalidBundles {
		t.Run(invalid.name, func(t *testing.T) {
			for _, tool := range toolHandlerCases(invalid.bundlePath) {
				t.Run(tool.name, func(t *testing.T) {
					result := callHandler(t, tool.handler, tool.args)
					if invalid.bundlePath == nil {
						if _, canonical := canonicalTools[tool.name]; canonical {
							envelope, ok := result.StructuredContent.(errorEnvelope)
							if !result.IsError || !ok ||
								envelope.Code != "invalid_request" ||
								envelope.Message != "tool input does not match the advertised schema" {
								t.Fatalf(
									"%s result = error %v envelope %#v, want invalid_request schema message",
									tool.name,
									result.IsError,
									result.StructuredContent,
								)
							}
							return
						}
					}
					if !result.IsError || !strings.Contains(resultText(t, result), invalid.message) {
						t.Fatalf("%s result = error %v text %q, want %q", tool.name, result.IsError, resultText(t, result), invalid.message)
					}
				})
			}
		})
	}
}

func TestBundlePathRejectsAncestorSymlinkForEachMCPTool(t *testing.T) {
	skipIfSymlinkUnsupported(t)

	// Arrange: the final bundle directory is ordinary, but one requested
	// ancestor is a link. Resolving it would let callers choose an unrelated
	// filesystem location behind a path accepted as a bundle_path.
	container := t.TempDir()
	realAncestor := filepath.Join(container, "real")
	realRoot := filepath.Join(realAncestor, "bundle")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatalf("mkdir real bundle: %v", err)
	}
	writeTestFile(t, realRoot, "a.md", "---\ntype: Note\n---\nA.\n")
	linkedAncestor := filepath.Join(container, "linked")
	if err := os.Symlink(realAncestor, linkedAncestor); err != nil {
		t.Fatalf("create ancestor symlink: %v", err)
	}
	requestedRoot := filepath.Join(linkedAncestor, "bundle")

	tools := []toolHandlerCase{
		{name: "read_concept", handler: handleReadConcept, args: map[string]any{"bundle_path": requestedRoot, "concept_id": "a"}},
		{name: "list_concepts", handler: handleListConcepts, args: map[string]any{"bundle_path": requestedRoot}},
		{name: "validate_bundle", handler: handleValidateBundle, args: map[string]any{"bundle_path": requestedRoot}},
		{name: "write_concept", handler: handleWriteConcept, args: map[string]any{
			"bundle_path": requestedRoot, "concept_id": "b", "frontmatter": "type: Note\n", "body": "B.\n",
		}},
	}
	for _, tool := range tools {
		t.Run(tool.name, func(t *testing.T) {
			result := callHandler(t, tool.handler, tool.args)
			if !result.IsError || !strings.Contains(resultText(t, result), "bundle_path must not contain a symlink") {
				t.Fatalf("%s result = error %v text %q, want ancestor symlink rejection", tool.name, result.IsError, resultText(t, result))
			}
		})
	}
}

func TestBundlePathRejectsUncleanAbsolutePath(t *testing.T) {
	root := t.TempDir()
	unclean := root + string(filepath.Separator) + "."
	for _, tool := range toolHandlerCases(unclean) {
		t.Run(tool.name, func(t *testing.T) {
			result := callHandler(t, tool.handler, tool.args)
			if !result.IsError || !strings.Contains(resultText(t, result), "bundle_path must be clean") {
				t.Fatalf("%s result = error %v text %q, want clean path rejection", tool.name, result.IsError, resultText(t, result))
			}
		})
	}
}

func TestAllToolsRejectUnloadableBundlePaths(t *testing.T) {
	root, ok := makeUnloadableBundleRoot(t)
	if !ok {
		t.Skip("could not create a bundle path that bundle.LoadBundle rejects on this platform")
	}

	for _, tool := range toolHandlerCases(root) {
		t.Run(tool.name, func(t *testing.T) {
			result := callHandler(t, tool.handler, tool.args)
			if !result.IsError || resultText(t, result) == "" {
				t.Fatalf("%s result = error %v text %q, want non-empty tool error", tool.name, result.IsError, resultText(t, result))
			}
		})
	}
}

func TestLoadableEmptyAndMissingRootIndexBundlesAreNotPreflightErrors(t *testing.T) {
	emptyRoot := t.TempDir()
	listEmpty := callHandler(t, handleListConcepts, map[string]any{"bundle_path": emptyRoot})
	if listEmpty.IsError {
		t.Fatalf("empty list_concepts returned tool error: %s", resultText(t, listEmpty))
	}
	var emptyList listConceptsResponse
	decodeResult(t, listEmpty, &emptyList)
	if len(emptyList.Concepts) != 0 {
		t.Fatalf("empty concepts = %#v, want none", emptyList.Concepts)
	}

	validateEmpty := callHandler(t, handleValidateBundle, map[string]any{"bundle_path": emptyRoot})
	if validateEmpty.IsError {
		t.Fatalf("empty validate_bundle returned tool error: %s", resultText(t, validateEmpty))
	}
	graphEmpty := callHandler(t, handleSemanticGraph, map[string]any{"bundle_path": emptyRoot})
	if graphEmpty.IsError {
		t.Fatalf("empty get_semantic_graph returned tool error: %s", resultText(t, graphEmpty))
	}

	writeEmpty := callHandler(t, handleWriteConcept, map[string]any{
		"bundle_path": emptyRoot,
		"concept_id":  "created",
		"frontmatter": "type: Note\n",
		"body":        "Created.\n",
	})
	if writeEmpty.IsError {
		t.Fatalf("empty write_concept returned tool error: %s", resultText(t, writeEmpty))
	}

	noIndexRoot := t.TempDir()
	writeTestFile(t, noIndexRoot, "a.md", "---\ntype: Note\n---\nA.\n")
	for _, tool := range toolHandlerCases(noIndexRoot) {
		t.Run(tool.name, func(t *testing.T) {
			result := callHandler(t, tool.handler, tool.args)
			if tool.name == "apply_concept_patch" || tool.name == "apply_v02_migration" {
				if !result.IsError {
					t.Fatalf("%s accepted fake revision/digest credentials", tool.name)
				}
				if text := resultText(t, result); strings.Contains(text, "index.md") {
					t.Fatalf("%s returned root-index preflight error: %s", tool.name, text)
				}
				return
			}
			if result.IsError {
				t.Fatalf("%s returned tool error for missing root index: %s", tool.name, resultText(t, result))
			}
		})
	}
}

func TestValidateBundleReturnsNormalReportForConformanceErrors(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "bad.md", "---\ntitle: Missing Type\n---\nBody.\n")

	result := callHandler(t, handleValidateBundle, map[string]any{"bundle_path": root})
	if result.IsError {
		t.Fatalf("handleValidateBundle() returned tool error: %s", resultText(t, result))
	}

	var got validateBundleResponse
	decodeResult(t, result, &got)
	if got.Conformant || got.Errors == 0 || len(got.Diagnostics) == 0 {
		t.Fatalf("validation response = %#v, want non-conformant report", got)
	}
	if got.Diagnostics[0].File != "bad.md" {
		t.Fatalf("diagnostic file = %q, want bad.md", got.Diagnostics[0].File)
	}
	assertValidateBundleWireContract(t, []byte(resultText(t, result)))
}

func TestValidateBundleKeepsRelationDiagnosticsOutOfBaselineReport(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\nrelations:\n  uses:\n    - target: missing#part\n---\nBody.\n")

	result := callHandler(t, handleValidateBundle, map[string]any{"bundle_path": root})
	if result.IsError {
		t.Fatalf("handleValidateBundle() returned tool error: %s", resultText(t, result))
	}
	var got validateBundleResponse
	decodeResult(t, result, &got)
	if !got.Conformant || got.Errors != 0 || len(got.Diagnostics) != 0 {
		t.Fatalf("validation response = %#v, want conformant base-only report", got)
	}
}

func TestValidateBundleAppliesOptionalFlags(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nSee [missing](/missing.md).\n")

	base := callHandler(t, handleValidateBundle, map[string]any{"bundle_path": root})
	if base.IsError {
		t.Fatalf("base validate returned tool error: %s", resultText(t, base))
	}
	var baseReport validateBundleResponse
	decodeResult(t, base, &baseReport)
	if !baseReport.Conformant || baseReport.Errors != 0 || baseReport.Warnings != 0 || baseReport.Info != 0 {
		t.Fatalf("base report = %#v, want conformant with no advisory diagnostics", baseReport)
	}

	advisory := callHandler(t, handleValidateBundle, map[string]any{
		"bundle_path":   root,
		"strict":        true,
		"check_links":   true,
		"check_orphans": true,
	})
	if advisory.IsError {
		t.Fatalf("advisory validate returned tool error: %s", resultText(t, advisory))
	}
	var advisoryReport validateBundleResponse
	decodeResult(t, advisory, &advisoryReport)
	if !advisoryReport.Conformant || advisoryReport.Errors != 0 {
		t.Fatalf("advisory report = %#v, want conformant report without hard errors", advisoryReport)
	}
	if advisoryReport.Info == 0 {
		t.Fatalf("advisory report = %#v, want info from enabled link/orphan flags", advisoryReport)
	}

	for _, key := range []string{"strict", "check_links", "check_orphans"} {
		t.Run("invalid "+key, func(t *testing.T) {
			result := callHandler(t, handleValidateBundle, map[string]any{
				"bundle_path": root,
				key:           "true",
			})
			envelope, ok := result.StructuredContent.(errorEnvelope)
			if !result.IsError || !ok ||
				envelope.Code != "invalid_request" ||
				envelope.Message != "tool input does not match the advertised schema" {
				t.Fatalf("invalid %s result = error %v envelope %#v", key, result.IsError, result.StructuredContent)
			}
		})
	}
}

func TestSemanticGraphMatchesRenderer(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nSee [B](b.md).\n")
	writeTestFile(t, root, "b.md", "---\ntype: Note\n---\nB.\n")

	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	var expected bytes.Buffer
	if err := graph.RenderJSONLD(&expected, loaded); err != nil {
		t.Fatalf("RenderJSONLD() error = %v", err)
	}

	result := callHandler(t, handleSemanticGraph, map[string]any{"bundle_path": root})
	if result.IsError {
		t.Fatalf("handleSemanticGraph() returned tool error: %s", resultText(t, result))
	}
	if got := resultText(t, result); got != expected.String() {
		t.Fatalf("graph JSON-LD mismatch\ngot:\n%s\nwant:\n%s", got, expected.String())
	}
}

func TestWriteConceptStagesValidatesAndWritesAtomically(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "existing.md", "---\ntype: Note\ntitle: Old\n---\nOld.\n")
	if err := os.Chmod(filepath.Join(root, "existing.md"), 0o600); err != nil {
		t.Fatalf("chmod existing: %v", err)
	}

	created := callHandler(t, handleWriteConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "nested/created",
		"frontmatter": "type: Note\ntitle: Created\n",
		"body":        "Created body.\n",
	})
	if created.IsError {
		t.Fatalf("create returned error result: %s", resultText(t, created))
	}
	var createdResponse writeConceptResponse
	decodeResult(t, created, &createdResponse)
	if createdResponse.Status != "success" || createdResponse.Path != "nested/created.md" {
		t.Fatalf("create response = %#v", createdResponse)
	}
	assertWriteConceptWireContract(t, []byte(resultText(t, created)))
	if got := readTestFile(t, root, "nested/created.md"); got != "---\ntype: Note\ntitle: Created\n---\n\nCreated body.\n" {
		t.Fatalf("created file = %q", got)
	}

	updated := callHandler(t, handleWriteConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "existing",
		"frontmatter": "type: Note\ntitle: Updated\n",
		"body":        "Updated body.\n",
	})
	if updated.IsError {
		t.Fatalf("update returned error result: %s", resultText(t, updated))
	}
	info, err := os.Lstat(filepath.Join(root, "existing.md"))
	if err != nil {
		t.Fatalf("stat updated file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("updated mode = %v, want 0600", got)
	}
	if got := readTestFile(t, root, "existing.md"); got != "---\ntype: Note\ntitle: Updated\n---\n\nUpdated body.\n" {
		t.Fatalf("updated file = %q", got)
	}
}

func TestWriteConceptRejectsInvalidInputWithoutChangingBundle(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "existing.md", "---\ntype: Note\n---\nExisting.\n")
	before := readTestFile(t, root, "existing.md")

	for i, conceptID := range []string{
		"",
		" new",
		"/new",
		"new/",
		"new.md",
		"new//nested",
		"new\\nested",
		"file:///new",
		"http://example.com/new",
		".hidden",
		"nested/index",
		"log",
		"../outside",
	} {
		t.Run(fmt.Sprintf("invalid id %02d", i), func(t *testing.T) {
			result := callHandler(t, handleWriteConcept, map[string]any{
				"bundle_path": root,
				"concept_id":  conceptID,
				"frontmatter": "type: Note\n",
				"body":        "Body.\n",
			})
			if !result.IsError {
				t.Fatalf("concept_id %q returned success", conceptID)
			}
			if got := readTestFile(t, root, "existing.md"); got != before {
				t.Fatalf("existing file changed: %q", got)
			}
		})
	}

	tests := []struct {
		name         string
		args         map[string]any
		wantRejected bool
	}{
		{
			name: "invalid id",
			args: map[string]any{
				"bundle_path": root, "concept_id": ".hidden",
				"frontmatter": "type: Note\n", "body": "Body.\n",
			},
		},
		{
			name: "invalid yaml",
			args: map[string]any{
				"bundle_path": root, "concept_id": "new",
				"frontmatter": "type: [\n", "body": "Body.\n",
			},
		},
		{
			name: "validation rejection",
			args: map[string]any{
				"bundle_path": root, "concept_id": "new",
				"frontmatter": "title: Missing Type\n", "body": "Body.\n",
			},
			wantRejected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callHandler(t, handleWriteConcept, tt.args)
			if !result.IsError {
				t.Fatalf("write returned success: %s", resultText(t, result))
			}
			if tt.wantRejected {
				var rejection writeConceptResponse
				decodeResult(t, result, &rejection)
				if rejection.Status != "rejected" || rejection.Path != "new.md" || len(rejection.Diagnostics) == 0 {
					t.Fatalf("rejection response = %#v, want rejected new.md with diagnostics", rejection)
				}
			}
			if got := readTestFile(t, root, "existing.md"); got != before {
				t.Fatalf("existing file changed: %q", got)
			}
			if _, err := os.Lstat(filepath.Join(root, "new.md")); !os.IsNotExist(err) {
				t.Fatalf("new.md exists after rejected write: %v", err)
			}
		})
	}
}

func TestWriteConceptRejectsInvalidUTF8BodyBeforeOpeningStore(t *testing.T) {
	t.Parallel()

	// Arrange.
	id, err := bundle.ParseConceptID("note")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	_, err = writeConcept(t.Context(), t.TempDir(), id, "type: Note\n", string([]byte{0xff}))

	// Assert.
	if !errors.Is(err, bundle.ErrInvalidEncoding) {
		t.Fatalf("writeConcept() error = %v, want ErrInvalidEncoding", err)
	}
}

func TestWriteConceptDoesNotFollowSymlinks(t *testing.T) {
	skipIfSymlinkUnsupported(t)

	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
	outside := t.TempDir()
	writeTestFile(t, outside, "bad.md", "---\ntype: [\n---\nOutside.\n")
	if err := os.Symlink(filepath.Join(outside, "bad.md"), filepath.Join(root, "linked.md")); err != nil {
		t.Skipf("create symlink target: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked-dir")); err != nil {
		t.Skipf("create symlink dir: %v", err)
	}

	result := callHandler(t, handleWriteConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "new",
		"frontmatter": "type: Note\n",
		"body":        "New.\n",
	})
	if !result.IsError || !strings.Contains(resultText(t, result), "symlink revision-visible path linked-dir") {
		t.Fatalf("visible symlink write result = error %v text %q", result.IsError, resultText(t, result))
	}

	targetResult := callHandler(t, handleWriteConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "linked",
		"frontmatter": "type: Note\n",
		"body":        "Replacement.\n",
	})
	if !targetResult.IsError || !strings.Contains(resultText(t, targetResult), "path contains symlink: linked.md") {
		t.Fatalf("symlink target write result = error %v text %q", targetResult.IsError, resultText(t, targetResult))
	}

	parentResult := callHandler(t, handleWriteConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "linked-dir/new",
		"frontmatter": "type: Note\n",
		"body":        "Replacement.\n",
	})
	if !parentResult.IsError || !strings.Contains(resultText(t, parentResult), "path contains symlink: linked-dir") {
		t.Fatalf("symlink parent write result = error %v text %q", parentResult.IsError, resultText(t, parentResult))
	}
}

func TestWriteConceptRelationDiagnosticsPreserveCodesAndPublication(t *testing.T) {
	for _, tc := range []struct {
		name        string
		frontmatter string
	}{
		{
			name:        "missing target",
			frontmatter: "type: Note\nrelations:\n  depends_on:\n    - {}\n",
		},
		{
			name:        "malformed relation block",
			frontmatter: "type: Note\nrelations: invalid\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			original := "---\ntype: Note\n---\nOriginal.\n"
			writeTestFile(t, root, "a.md", original)

			// Act.
			result := callHandler(t, handleWriteConcept, map[string]any{
				"bundle_path": root,
				"concept_id":  "a",
				"frontmatter": tc.frontmatter,
				"body":        "Rejected.\n",
			})
			var response writeConceptResponse
			decodeResult(t, result, &response)

			// Assert.
			if !result.IsError || response.Status != "rejected" || response.Path != "a.md" {
				t.Fatalf("response = %#v, isError=%t; want rejected a.md", response, result.IsError)
			}
			if len(response.Diagnostics) == 0 || response.Diagnostics[0].File != "a.md" || response.Diagnostics[0].Message == "" {
				t.Fatalf("diagnostics = %#v, want a fixed wire diagnostic for a.md", response.Diagnostics)
			}
			assertWriteConceptWireContract(t, []byte(resultText(t, result)))
			if got := readTestFile(t, root, "a.md"); got != original {
				t.Fatalf("rejected write changed bytes = %q, want %q", got, original)
			}
		})
	}

	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nOriginal.\n")

	// Act.
	result := callHandler(t, handleWriteConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "a",
		"frontmatter": "type: Note\nparts:\n  - id: canonical\n    anchor: legacy\n",
		"body":        "Published.\n",
	})
	var response writeConceptResponse
	decodeResult(t, result, &response)

	// Assert.
	if result.IsError || response.Status != "success" || readTestFile(t, root, "a.md") == "---\ntype: Note\n---\nOriginal.\n" {
		t.Fatalf("anchor alias response = %#v, isError=%t; want published success", response, result.IsError)
	}
}

func TestWriteConceptRejectedPreservesDistinctStructuredRelationDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name        string
		frontmatter string
		wantCode    string
		wantMessage string
		wantType    string
		wantTarget  string
	}{
		{
			name:        "missing fragments from separate files",
			frontmatter: "type: Note\nrelations:\n  uses:\n    - target: target#missing\n",
			wantCode:    "missing_or_ambiguous_target_fragment",
			wantMessage: "target fragment does not exist or is ambiguous",
			wantType:    "uses",
			wantTarget:  "target#missing",
		},
		{
			name:        "malformed relation shape from separate files",
			frontmatter: "type: Note\nrelations:\n  depends_on: malformed\n",
			wantCode:    "relation_not_sequence",
			wantMessage: "relation value must be a sequence",
			wantType:    "depends_on",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeTestFile(t, root, "target.md", "---\ntype: Note\nparts:\n  - id: canonical\n---\nTarget.\n")
			writeTestFile(t, root, "b.md", "---\n"+tc.frontmatter+"---\nExisting.\n")

			// Act.
			result := callHandler(t, handleWriteConcept, map[string]any{
				"bundle_path": root,
				"concept_id":  "a",
				"frontmatter": tc.frontmatter,
				"body":        "Rejected.\n",
			})
			var response writeConceptResponse
			decodeResult(t, result, &response)

			// Assert.
			if !result.IsError || response.Status != "rejected" {
				t.Fatalf("response = %#v, isError=%t; want rejected response", response, result.IsError)
			}
			projected := make([]diagnosticDTO, 0, 2)
			for _, diagnostic := range response.Diagnostics {
				if diagnostic.Severity == string(store.DiagnosticError) && diagnostic.Message == tc.wantMessage {
					projected = append(projected, diagnostic)
				}
			}
			if got, want := len(projected), 2; got != want {
				t.Fatalf("relation diagnostics count = %d, want %d: %#v", got, want, response.Diagnostics)
			}
			for i, file := range []string{"a.md", "b.md"} {
				diagnostic := projected[i]
				if diagnostic.Severity != string(store.DiagnosticError) || diagnostic.File != file || diagnostic.Message != tc.wantMessage {
					t.Fatalf("diagnostic[%d] = %#v, want fixed wire projection for file=%q", i, diagnostic, file)
				}
			}
			assertWriteConceptWireContract(t, []byte(resultText(t, result)))
		})
	}
}

func TestWriteConceptRejectedDeduplicatesLogicalRelationDiagnostics(t *testing.T) {
	makeRelation := func(file, source string) (validator.Diagnostic, store.Diagnostic) {
		ref, err := bundle.ParseRelationRef(source)
		if err != nil {
			t.Fatalf("ParseRelationRef(%q): %v", source, err)
		}
		validatorDiagnostic := validator.Diagnostic{
			Code:         "missing_target",
			Severity:     validator.SeverityError,
			File:         file,
			Message:      "relation item requires target",
			RelationType: "depends_on",
			Source:       ref,
			Refs:         []bundle.RelationRef{ref},
		}
		storeDiagnostic := store.Diagnostic{
			Kind:         store.DiagnosticRelation,
			Severity:     store.DiagnosticError,
			Code:         validatorDiagnostic.Code,
			File:         file,
			Message:      validatorDiagnostic.Message,
			RelationType: validatorDiagnostic.RelationType,
			Refs:         []bundle.RelationRef{ref},
		}
		return validatorDiagnostic, storeDiagnostic
	}

	for _, tc := range []struct {
		name    string
		sources []string
	}{
		{name: "single finding", sources: []string{"a"}},
		{name: "same code different sources", sources: []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			report := validator.Report{}
			rejected := make([]store.Diagnostic, 0, len(tc.sources))
			for _, source := range tc.sources {
				validatorDiagnostic, storeDiagnostic := makeRelation(source+".md", source)
				report.Diagnostics = append(report.Diagnostics, validatorDiagnostic)
				rejected = append(rejected, storeDiagnostic)
			}

			// Act.
			response, err := writeConceptRejectedContext(t.Context(), "", "a.md", report, rejected)
			if err != nil {
				t.Fatalf("writeConceptRejectedContext() error = %v", err)
			}

			// Assert.
			if got, want := len(response.Diagnostics), len(tc.sources); got != want {
				t.Fatalf("diagnostics count = %d, want %d: %#v", got, want, response.Diagnostics)
			}
			for i, source := range tc.sources {
				diagnostic := response.Diagnostics[i]
				if diagnostic.Severity != string(store.DiagnosticError) || diagnostic.File != source+".md" || diagnostic.Message != "relation item requires target" {
					t.Fatalf("diagnostic[%d] = %#v, want fixed wire projection for %q", i, diagnostic, source)
				}
			}
			data, err := json.Marshal(response)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			assertWriteConceptWireContract(t, data)
		})
	}
}

func TestWriteConceptResponseWireContract(t *testing.T) {
	// Arrange. These source diagnostics intentionally carry fields that belong
	// to the internal graph/store contract, not to MCP.
	ref, err := bundle.ParseRelationRef("a")
	if err != nil {
		t.Fatal(err)
	}
	report := validator.Report{Diagnostics: []validator.Diagnostic{{
		Code: "relation_not_sequence", Severity: validator.SeverityError,
		File: "a.md", Message: "relation value must be a sequence",
		RelationType: "depends_on", Source: ref, Refs: []bundle.RelationRef{ref},
	}}}
	rejected := []store.Diagnostic{{
		Kind: store.DiagnosticRelation, Severity: store.DiagnosticError,
		Code: "relation_not_sequence", File: "a.md", Message: "relation value must be a sequence",
		RelationType: "depends_on", RawTarget: "not-a-sequence", Refs: []bundle.RelationRef{ref},
	}}

	// Act.
	success, err := writeConceptSuccessContext(t.Context(), "", "a.md", report)
	if err != nil {
		t.Fatalf("writeConceptSuccessContext() error = %v", err)
	}
	successData, err := json.Marshal(success)
	if err != nil {
		t.Fatalf("marshal success: %v", err)
	}
	rejectedResponse, err := writeConceptRejectedContext(t.Context(), "", "a.md", report, rejected)
	if err != nil {
		t.Fatalf("writeConceptRejectedContext() error = %v", err)
	}
	rejectedData, err := json.Marshal(rejectedResponse)
	if err != nil {
		t.Fatalf("marshal rejected: %v", err)
	}

	// Assert.
	assertWriteConceptWireContract(t, successData)
	assertWriteConceptWireContract(t, rejectedData)
}

func assertWriteConceptWireContract(t *testing.T, data []byte) {
	t.Helper()
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(data, &topLevel); err != nil {
		t.Fatalf("decode write response: %v\n%s", err, data)
	}
	if len(topLevel) != 3 {
		t.Fatalf("write response = %#v, want only status/path/diagnostics: %s", topLevel, data)
	}
	for _, key := range []string{"status", "path", "diagnostics"} {
		if _, ok := topLevel[key]; !ok {
			t.Fatalf("write response lacks %q: %s", key, data)
		}
	}
	assertDiagnosticWireContract(t, data)
}

func assertValidateBundleWireContract(t *testing.T, data []byte) {
	t.Helper()
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(data, &topLevel); err != nil {
		t.Fatalf("decode validation response: %v\n%s", err, data)
	}
	for _, key := range []string{"scanned_files", "conformant", "errors", "warnings", "info", "diagnostics"} {
		if _, ok := topLevel[key]; !ok {
			t.Fatalf("validation response lacks %q: %s", key, data)
		}
	}
	if len(topLevel) != 6 {
		t.Fatalf("validation response = %#v, want fixed response keys: %s", topLevel, data)
	}
	assertDiagnosticWireContract(t, data)
}

// assertDiagnosticWireContract guards the established MCP diagnostic schema:
// diagnostics expose only severity, optional file, and message. Full relation
// diagnostics remain internal to bundle/store/validator APIs.
func assertDiagnosticWireContract(t *testing.T, data []byte) {
	t.Helper()
	var response struct {
		Diagnostics []map[string]json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("decode diagnostic wire response: %v\n%s", err, data)
	}
	for i, diagnostic := range response.Diagnostics {
		for key := range diagnostic {
			if key != "severity" && key != "file" && key != "message" {
				t.Fatalf("diagnostic[%d] leaked non-contract key %q: %s", i, key, data)
			}
		}
		if _, ok := diagnostic["severity"]; !ok {
			t.Fatalf("diagnostic[%d] lacks severity: %s", i, data)
		}
		if _, ok := diagnostic["message"]; !ok {
			t.Fatalf("diagnostic[%d] lacks message: %s", i, data)
		}
	}
}

func TestWriteConceptConcurrentCreatesDoNotLoseEitherUpdate(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	start := make(chan struct{})
	type outcome struct {
		id       string
		response writeConceptResponse
		err      error
	}
	results := make(chan outcome, 2)
	var writers sync.WaitGroup
	for _, id := range []string{"first", "second"} {
		writers.Add(1)
		go func(id string) {
			defer writers.Done()
			<-start
			conceptID, err := parseCanonicalConceptID(id)
			if err != nil {
				results <- outcome{id: id, err: err}
				return
			}
			response, err := writeConcept(context.Background(), root, conceptID, "type: Note\n", id+" body.\n")
			results <- outcome{id: id, response: response, err: err}
		}(id)
	}

	// Act.
	close(start)
	writers.Wait()
	close(results)
	gotResults := make([]outcome, 0, 2)
	for result := range results {
		gotResults = append(gotResults, result)
	}

	// Assert.
	for _, result := range gotResults {
		if result.err != nil || result.response.Status != "success" {
			t.Fatalf("concurrent write %s = %#v, err=%v", result.id, result.response, result.err)
		}
		if got := readTestFile(t, root, result.id+".md"); !strings.Contains(got, result.id+" body.") {
			t.Fatalf("%s content = %q", result.id, got)
		}
	}
}

func TestWriteConceptReplaysDeterministicRequestIdentity(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	args := map[string]any{
		"bundle_path": root, "concept_id": "entry", "frontmatter": "type: Note\n", "body": "First.\n",
	}

	// Act.
	first := callHandler(t, handleWriteConcept, args)
	replay := callHandler(t, handleWriteConcept, args)
	receipts, err := os.ReadDir(filepath.Join(root, ".okf", "receipts"))

	// Assert.
	if first.IsError || replay.IsError {
		t.Fatalf("first error=%v replay error=%v; first=%s replay=%s", first.IsError, replay.IsError, resultText(t, first), resultText(t, replay))
	}
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipt files = %d, err=%v; want one publication", len(receipts), err)
	}
	if got := readTestFile(t, root, "entry.md"); !strings.Contains(got, "First.") {
		t.Fatalf("entry content = %q, want first payload", got)
	}
}

func TestWriteConceptChangeSetIDUsesUnambiguousCanonicalEncoding(t *testing.T) {
	// Arrange. These inputs produce the same bytes with the former NUL-delimited
	// encoding because a field value can contain the delimiter.
	id, err := parseCanonicalConceptID("entry")
	if err != nil {
		t.Fatal(err)
	}
	firstFrontmatter := "type: Note\nlabel: café\x00next"
	firstBody := "body\n"
	secondFrontmatter := "type: Note\nlabel: café"
	secondBody := "next\x00body\n"
	resolution, err := bundle.ResolveVersion("", "")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	first := writeConceptChangeSetIDWithPolicy(id, firstFrontmatter, firstBody, resolution)
	firstReplay := writeConceptChangeSetIDWithPolicy(id, firstFrontmatter, firstBody, resolution)
	second := writeConceptChangeSetIDWithPolicy(id, secondFrontmatter, secondBody, resolution)
	boundaryA := writeConceptChangeSetIDWithPolicy(id, "type: Note\nlabel: ab\n", "c", resolution)
	boundaryB := writeConceptChangeSetIDWithPolicy(id, "type: Note\nlabel: a\n", "bc", resolution)

	// Assert.
	if first != firstReplay {
		t.Fatalf("deterministic identity = %q then %q", first, firstReplay)
	}
	if first == second {
		t.Fatalf("NUL-containing distinct requests share identity %q", first)
	}
	if boundaryA == boundaryB {
		t.Fatalf("distinct field boundaries share identity %q", boundaryA)
	}
}

func TestWriteConceptChangeSetIDBindsVersionPolicy(t *testing.T) {
	// Arrange.
	id, err := bundle.ParseConceptID("entry")
	if err != nil {
		t.Fatal(err)
	}
	v01, err := bundle.ResolveVersion("0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	v02, err := bundle.ResolveVersion("0.2", "")
	if err != nil {
		t.Fatal(err)
	}
	future, err := bundle.ResolveVersion("9.9", "")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	legacy := writeConceptChangeSetIDWithPolicy(id, "type: Note\n", "Body.\n", v01)
	native := writeConceptChangeSetIDWithPolicy(id, "type: Note\n", "Body.\n", v02)
	bestEffort := writeConceptChangeSetIDWithPolicy(id, "type: Note\n", "Body.\n", future)

	// Assert.
	if legacy == native || legacy == bestEffort || native == bestEffort {
		t.Fatalf("version-bound identities collide: %q %q %q", legacy, native, bestEffort)
	}
}

func TestWriteConceptClosesStoreWhenSnapshotFails(t *testing.T) {
	// Arrange.
	opened := &closeTrackingStore{snapshotErr: errors.New("snapshot failed"), closeErr: errors.New("close failed")}
	id, err := parseCanonicalConceptID("entry")
	if err != nil {
		t.Fatal(err)
	}
	opener := func(context.Context, string, storefs.Config) (transactionalStore, error) {
		return opened, nil
	}

	// Act.
	response, writeErr := writeConceptCommitWithStoreOpener(
		t.Context(),
		t.TempDir(),
		id,
		"type: Note\n",
		"Body.\n",
		1,
		opener,
	)

	// Assert.
	if response.Status != "" || response.Path != "" || response.Diagnostics != nil {
		t.Fatalf("response = %#v, want zero response on close failure", response)
	}
	if !opened.closed {
		t.Fatal("store Close was not called")
	}
	if !errors.Is(writeErr, opened.snapshotErr) || !errors.Is(writeErr, opened.closeErr) {
		t.Fatalf("write error = %v, want joined snapshot and close errors", writeErr)
	}
}

type closeTrackingStore struct {
	snapshotErr error
	closeErr    error
	closed      bool
}

func (s *closeTrackingStore) Snapshot(context.Context) (store.Snapshot, error) {
	return nil, s.snapshotErr
}
func (s *closeTrackingStore) ReplaceConcept(context.Context, storefs.ReplaceConceptRequest, store.CommitOptions) (storefs.ReplaceConceptResult, error) {
	return storefs.ReplaceConceptResult{}, errors.New("ReplaceConcept must not be called")
}
func (s *closeTrackingStore) Close() error { s.closed = true; return s.closeErr }

type toolHandlerCase struct {
	name    string
	handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	args    map[string]any
}

func toolHandlerCases(bundlePath any) []toolHandlerCase {
	args := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		if bundlePath != nil {
			out["bundle_path"] = bundlePath
		}
		for key, value := range extra {
			out[key] = value
		}
		return out
	}
	return []toolHandlerCase{
		{name: "list_concepts", handler: handleListConcepts, args: args(nil)},
		{name: "read_concept", handler: handleReadConcept, args: args(map[string]any{"concept_id": "a"})},
		{name: "validate_bundle", handler: handleValidateBundle, args: args(nil)},
		{name: "get_semantic_graph", handler: handleSemanticGraph, args: args(nil)},
		{name: "write_concept", handler: handleWriteConcept, args: args(map[string]any{
			"concept_id":  "b",
			"frontmatter": "type: Note\n",
			"body":        "B.\n",
		})},
		{name: "preview_concept_patch", handler: handlePreviewConceptPatch, args: args(map[string]any{
			"actor": "test", "operations": []any{map[string]any{
				"kind": "set_lifecycle", "concept_id": "a",
				"lifecycle": map[string]any{"status": "stable", "stale_after": nil},
			}},
		})},
		{name: "apply_concept_patch", handler: handleApplyConceptPatch, args: args(map[string]any{
			"actor": "test", "operations": []any{map[string]any{
				"kind": "set_lifecycle", "concept_id": "a",
				"lifecycle": map[string]any{"status": "stable", "stale_after": nil},
			}},
			"expected_revision":    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"expected_plan_digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		})},
		{name: "preview_v02_migration", handler: handlePreviewV02Migration, args: args(map[string]any{
			"generated_by": "process:test", "timestamp_policy": "preserve",
			"citation_mappings": []any{},
		})},
		{name: "apply_v02_migration", handler: handleApplyV02Migration, args: args(map[string]any{
			"generated_by": "process:test", "timestamp_policy": "preserve",
			"citation_mappings": []any{},
			"proof": testMigrationPlanProofDTO(
				"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			),
			"expected_plan_digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"expected_source":      testMigrationSourceValue("0.1", "v0.1-to-v0.2"),
		})},
	}
}

func makeUnloadableBundleRoot(t *testing.T) (string, bool) {
	t.Helper()

	if runtime.GOOS == "windows" {
		return "", false
	}
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatalf("mkdir blocked: %v", err)
	}
	writeTestFile(t, blocked, "b.md", "---\ntype: Note\n---\nB.\n")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatalf("chmod blocked: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(blocked, 0o755)
	})
	if _, err := bundle.LoadBundle(root); err == nil {
		return "", false
	}
	return root, true
}

func callHandler(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()

	result, err := handler(t.Context(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	})
	if err != nil {
		t.Fatalf("handler returned protocol error: %v", err)
	}
	return result
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()

	var builder strings.Builder
	for _, content := range result.Content {
		text, ok := content.(mcp.TextContent)
		if !ok {
			t.Fatalf("content type = %T, want mcp.TextContent", content)
		}
		builder.WriteString(text.Text)
	}
	return builder.String()
}

func decodeResult(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()

	if err := json.Unmarshal([]byte(resultText(t, result)), target); err != nil {
		t.Fatalf("decode result JSON: %v\n%s", err, resultText(t, result))
	}
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTestFile(t *testing.T, root, rel string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func skipIfSymlinkUnsupported(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("symlink assertions require symlink support")
	}
}

func equalConceptSummaries(a, b []conceptSummary) bool {
	if len(a) != len(b) {
		return false
	}
	aCopy := append([]conceptSummary(nil), a...)
	bCopy := append([]conceptSummary(nil), b...)
	sort.Slice(aCopy, func(i, j int) bool { return aCopy[i].ID < aCopy[j].ID })
	sort.Slice(bCopy, func(i, j int) bool { return bCopy[i].ID < bCopy[j].ID })
	for i := range aCopy {
		if aCopy[i] != bCopy[i] {
			return false
		}
	}
	return true
}
