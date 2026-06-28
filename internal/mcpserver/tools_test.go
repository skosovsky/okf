package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/graph"
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
	if !linkResult.IsError || !strings.Contains(resultText(t, linkResult), "path contains symlink: link.md") {
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
	if !parentResult.IsError || !strings.Contains(resultText(t, parentResult), "path contains symlink: linked-dir") {
		t.Fatalf("symlink parent result = error %v text %q", parentResult.IsError, resultText(t, parentResult))
	}
}

func TestAllToolsRejectInvalidBundlePaths(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
	filePath := filepath.Join(root, "a.md")

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

	for _, invalid := range invalidBundles {
		t.Run(invalid.name, func(t *testing.T) {
			for _, tool := range toolHandlerCases(invalid.bundlePath) {
				t.Run(tool.name, func(t *testing.T) {
					result := callHandler(t, tool.handler, tool.args)
					if !result.IsError || !strings.Contains(resultText(t, result), invalid.message) {
						t.Fatalf("%s result = error %v text %q, want %q", tool.name, result.IsError, resultText(t, result), invalid.message)
					}
				})
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
			if !result.IsError || !strings.Contains(resultText(t, result), "load bundle:") {
				t.Fatalf("%s result = error %v text %q, want load bundle tool error", tool.name, result.IsError, resultText(t, result))
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
	if advisoryReport.Warnings == 0 || advisoryReport.Info == 0 {
		t.Fatalf("advisory report = %#v, want warnings and info from enabled flags", advisoryReport)
	}

	for _, key := range []string{"strict", "check_links", "check_orphans"} {
		t.Run("invalid "+key, func(t *testing.T) {
			result := callHandler(t, handleValidateBundle, map[string]any{
				"bundle_path": root,
				key:           "true",
			})
			if !result.IsError || !strings.Contains(resultText(t, result), "argument "+fmt.Sprintf("%q", key)+" is not a boolean") {
				t.Fatalf("invalid %s result = error %v text %q", key, result.IsError, resultText(t, result))
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
	if result.IsError {
		t.Fatalf("write with unrelated symlink returned error: %s", resultText(t, result))
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
