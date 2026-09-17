package mcpserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
)

type inertSecurityProbe struct {
	marker   string
	requests atomic.Int64
	server   *httptest.Server
}

func newInertSecurityProbe(t *testing.T) *inertSecurityProbe {
	t.Helper()

	probe := &inertSecurityProbe{
		marker: filepath.Join(t.TempDir(), "unexpected-execution.marker"),
	}
	probe.server = httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		probe.requests.Add(1)
	}))
	t.Cleanup(probe.server.Close)
	return probe
}

func (p *inertSecurityProbe) resource(kind string) string {
	command := `$(touch "` + p.marker + `")`
	return p.server.URL + "/" + kind + "?payload=" + url.QueryEscape(command)
}

func (p *inertSecurityProbe) computation() string {
	return "touch " + strconv.Quote(p.marker) + "\n" +
		"curl " + strconv.Quote(p.resource("computation")) + "\n"
}

func (p *inertSecurityProbe) assertInert(t *testing.T, stage string) {
	t.Helper()

	if got := p.requests.Load(); got != 0 {
		t.Fatalf("%s fetched an inert resource: request count = %d", stage, got)
	}
	if _, err := os.Lstat(p.marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s executed command-like data: marker stat error = %v", stage, err)
	}
}

func TestMCPAttestedComputationValuesRemainInert(t *testing.T) {
	t.Run("patch-preview-apply-read-list-graph", func(t *testing.T) {
		// Arrange.
		probe := newInertSecurityProbe(t)
		root := t.TempDir()
		writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n")
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
		srv, err := mcptest.NewServer(t, mustServerTools(t)...)
		if err != nil {
			t.Fatalf("NewServer() error = %v", err)
		}
		defer srv.Close()
		arguments := map[string]any{
			"bundle_path": root,
			"actor":       "mcp:inert-security-test",
			"operations": []any{
				map[string]any{
					"kind": "put_source", "concept_id": "a",
					"source": map[string]any{
						"id": "malicious-resource", "resource": probe.resource("source"),
					},
				},
				map[string]any{
					"kind": "put_attested_computation", "concept_id": "a",
					"attested_computation": map[string]any{
						"mode": "inline", "runtime": "shell", "parameters": []any{},
						"inline_computation": probe.computation(), "inline_language": "sh",
						"executor": map[string]any{
							"resource": probe.resource("executor"), "receipt": []any{"result"},
						},
						"attester": map[string]any{"resource": probe.resource("attester")},
					},
				},
			},
		}

		// Act.
		preview := callMCPTool(t, srv, "preview_concept_patch", arguments)

		// Assert.
		if preview.IsError {
			t.Fatalf("patch preview returned error: %s", resultText(t, preview))
		}
		previewPayload := preview.StructuredContent.(map[string]any)
		if previewPayload["status"] != "applicable" {
			t.Fatalf("patch preview = %#v, want applicable", previewPayload)
		}
		probe.assertInert(t, "patch preview")

		// Act.
		applyArguments := cloneArguments(arguments)
		applyArguments["expected_revision"] = previewPayload["base_revision"]
		applyArguments["expected_plan_digest"] = previewPayload["plan_digest"]
		applied := callMCPTool(t, srv, "apply_concept_patch", applyArguments)

		// Assert.
		if applied.IsError {
			t.Fatalf("patch apply returned error: %s", resultText(t, applied))
		}
		probe.assertInert(t, "patch apply")
		document := readTestFile(t, root, "a.md")
		for _, value := range []string{
			probe.resource("source"),
			probe.resource("executor"),
			probe.resource("attester"),
			probe.marker,
		} {
			if !strings.Contains(document, value) {
				t.Fatalf("applied patch did not preserve inert value %q:\n%s", value, document)
			}
		}

		for _, call := range []struct {
			name string
			args map[string]any
		}{
			{name: "read_concept", args: map[string]any{"bundle_path": root, "concept_id": "a"}},
			{name: "list_concepts", args: map[string]any{"bundle_path": root}},
			{name: "get_semantic_graph", args: map[string]any{"bundle_path": root}},
		} {
			// Act.
			result := callMCPTool(t, srv, call.name, call.args)

			// Assert.
			if result.IsError {
				t.Fatalf("%s returned error: %s", call.name, resultText(t, result))
			}
			probe.assertInert(t, "patch "+call.name)
		}
	})

	t.Run("migration-preview-apply-read-list-graph", func(t *testing.T) {
		// Arrange.
		probe := newInertSecurityProbe(t)
		root := t.TempDir()
		legacyResource := probe.resource("migrated-source")
		writeTestFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n# Knowledge\n- [A](a.md)\n")
		writeTestFile(t, root, "a.md",
			"---\ntype: Knowledge\n---\nClaim [1].\n\n# Citations\n\n[1] [Remote]("+legacyResource+")\n\n"+
				"# Computation\n\n```sh\n"+probe.computation()+"```\n",
		)
		srv, err := mcptest.NewServer(t, mustServerTools(t)...)
		if err != nil {
			t.Fatalf("NewServer() error = %v", err)
		}
		defer srv.Close()
		arguments := map[string]any{
			"bundle_path": root, "from": "auto", "generated_by": "process:migration",
			"timestamp_policy": "preserve",
			"citation_mappings": []any{map[string]any{
				"path": "a.md",
				"entries": []any{map[string]any{
					"legacy_number": 1, "source_id": "remote",
					"resource": legacyResource,
				}},
			}},
			"computations": []any{map[string]any{
				"concept_id": "a",
				"contract": map[string]any{
					"mode": "inline", "runtime": "shell", "parameters": []any{},
					"inline_computation": probe.computation(), "inline_language": "sh",
					"executor": map[string]any{
						"resource": probe.resource("executor"), "receipt": []any{"result"},
					},
					"attester": map[string]any{"resource": probe.resource("attester")},
				},
			}},
		}

		// Act.
		preview := callMCPTool(t, srv, "preview_v02_migration", arguments)

		// Assert.
		if preview.IsError {
			t.Fatalf("migration preview returned error: %s", resultText(t, preview))
		}
		previewPayload := preview.StructuredContent.(map[string]any)
		if previewPayload["status"] != "applicable" {
			t.Fatalf("migration preview = %#v, want applicable", previewPayload)
		}
		probe.assertInert(t, "migration preview")

		// Act.
		applyArguments := cloneArguments(arguments)
		applyArguments["expected_source"] = previewPayload["source"]
		applyArguments["proof"] = previewPayload["proof"]
		applyArguments["expected_plan_digest"] = previewPayload["plan_digest"]
		applied := callMCPTool(t, srv, "apply_v02_migration", applyArguments)

		// Assert.
		if applied.IsError {
			t.Fatalf("migration apply returned error: %s", resultText(t, applied))
		}
		probe.assertInert(t, "migration apply")
		document := readTestFile(t, root, "a.md")
		for _, value := range []string{
			probe.resource("migrated-source"),
			probe.resource("executor"),
			probe.resource("attester"),
			probe.marker,
		} {
			if !strings.Contains(document, value) {
				t.Fatalf("migration did not preserve inert value %q:\n%s", value, document)
			}
		}

		for _, call := range []struct {
			name string
			args map[string]any
		}{
			{name: "read_concept", args: map[string]any{"bundle_path": root, "concept_id": "a"}},
			{name: "list_concepts", args: map[string]any{"bundle_path": root}},
			{name: "get_semantic_graph", args: map[string]any{"bundle_path": root}},
		} {
			// Act.
			result := callMCPTool(t, srv, call.name, call.args)

			// Assert.
			if result.IsError {
				t.Fatalf("%s returned error: %s", call.name, resultText(t, result))
			}
			probe.assertInert(t, "migration "+call.name)
		}
	})
}
