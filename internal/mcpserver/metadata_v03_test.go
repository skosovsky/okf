package mcpserver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/okfcli"
	"github.com/skosovsky/okf/validator"
	"github.com/skosovsky/okf/viewer"
)

func TestOKF03DeadlineStatesAgreeAcrossPresentations(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	cases := []struct {
		id, value string
		state     bundle.TemporalValueState
		viewState string
	}{
		{"absent", "[]", bundle.TemporalAbsent, "unevaluated"},
		{"malformed", "sometimes", bundle.TemporalMalformed, "invalid"},
		{"valid", "2026-11-27", bundle.TemporalValid, "stale"},
	}
	for _, test := range cases {
		writeTestFile(t, root, test.id+".md", "---\ntype: Note\nstale_after: "+test.value+"\n---\n[Deadline](valid.md).\n")
	}
	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	const date = "2026-11-27"
	asOf, err := time.Parse(time.DateOnly, date)
	if err != nil {
		t.Fatal(err)
	}
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		args["bundle_path"] = root
		result := callMCPTool(t, server, tool, args)
		if result.IsError {
			t.Fatalf("%s: %s", tool, resultText(t, result))
		}
		validateContractValue(t, tool+".output", result.StructuredContent)
		return result.StructuredContent.(map[string]any)
	}
	run := func(args ...string) []byte {
		t.Helper()
		var out, stderr bytes.Buffer
		if exit := okfcli.Run(args, &out, &stderr); exit != 0 {
			t.Fatalf("%v exited %d: %s", args, exit, stderr.String())
		}
		return out.Bytes()
	}

	// Act.
	list := call("list_concepts", map[string]any{"as_of": date})["concepts"].([]any)
	search := call("search_concepts", map[string]any{"query": "Deadline", "as_of": date})["hits"].([]any)
	graphJSON := run("graph", root, "--profile", "skosovsky/okf-v0.2", "--format", "json-ld", "--as-of", date)
	var graph struct {
		Nodes []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(graphJSON, &graph); err != nil {
		t.Fatal(err)
	}
	view, err := viewer.Build(t.Context(), loaded, viewer.Options{AsOf: &asOf})
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	for _, test := range cases {
		read := call("read_concept", map[string]any{"concept_id": test.id})["projection"].(map[string]any)
		neighbors := call("get_neighbors", map[string]any{"concept_id": test.id})["concept"].(map[string]any)
		if read["stale_after_state"] != string(test.state) || neighbors["stale_after_state"] != string(test.state) || neighbors["stale"] != nil {
			t.Errorf("%s: MCP read or neighbors has unexpected deadline state", test.id)
		}
		var listed, found map[string]any
		for _, item := range list {
			if concept := item.(map[string]any); concept["id"] == test.id {
				listed = concept
			}
		}
		for _, item := range search {
			if concept := item.(map[string]any)["concept"].(map[string]any); concept["id"] == test.id {
				found = concept
			}
		}
		var wantStale any
		if test.state == bundle.TemporalValid {
			wantStale = true
		}
		for _, card := range []map[string]any{listed, found} {
			if card["stale_after_state"] != string(test.state) || card["stale"] != wantStale {
				t.Errorf("%s: unexpected MCP card: %#v", test.id, card)
			}
		}
		var parsed struct {
			Stale          any
			LifecycleAudit struct {
				StaleAfter struct{ State bundle.TemporalValueState } `json:"stale_after"`
			} `json:"lifecycle_audit"`
		}
		if err := json.Unmarshal(run("parse", filepath.Join(root, test.id+".md"), "--json", "--as-of", date), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.LifecycleAudit.StaleAfter.State != test.state || parsed.Stale != wantStale {
			t.Errorf("%s: unexpected CLI parse deadline: %+v", test.id, parsed)
		}
	}
	var malformedNode map[string]any
	for _, node := range graph.Nodes {
		if node["@id"] == "local:bundle:malformed" {
			malformedNode = node
			break
		}
	}
	if malformedNode["staleAfterState"] != "malformed" || malformedNode["stale"] != nil || malformedNode["staleAfter"] != nil {
		t.Fatalf("unexpected graph deadline: %#v", malformedNode)
	}
	if !strings.Contains(string(run("graph", root, "--format", "text", "--annotate-topology")), "stale_after=malformed") {
		t.Fatal("annotated graph did not expose the malformed deadline")
	}
	if len(view.Concepts) != len(cases) {
		t.Fatalf("viewer projected %d concepts, want %d", len(view.Concepts), len(cases))
	}
	for i, concept := range view.Concepts {
		if concept.ID != cases[i].id || concept.Staleness != cases[i].viewState {
			t.Errorf("viewer concept %s has staleness %s, want %s", concept.ID, concept.Staleness, cases[i].viewState)
		}
	}
}

func TestOKF03UnknownStatusAndBrokenLinkStayVisible(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	const body = "---\n\n# Review\n\n[Review deadline](future%20policy.md).\n"
	writeTestFile(t, root, "unknown.md", "---\ntype: Note\nstatus: invalid\nsuperseded_by: current.md\n"+body)
	writeTestFile(t, root, "numeric.md", "---\ntype: Note\nstatus: 17\n"+body)
	writeTestFile(t, root, "current.md", "---\ntype: Note\nstatus: null\n---\n\n# Review\n\nCurrent deadline.\n")
	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	run := func(args ...string) []byte {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := okfcli.Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v exited %d: %s", args, code, stderr.String())
		}
		return stdout.Bytes()
	}

	// Act.
	read := callMCPTool(t, server, "read_concept", map[string]any{"bundle_path": root, "concept_id": "unknown"})
	search := callMCPTool(t, server, "search_concepts", map[string]any{"bundle_path": root, "query": "deadline", "current_only": true})
	sections := run("search", root, "--query", "deadline", "--current-only", "--json")
	graph := run("graph", root, "--format", "ntriples")
	topology := run("graph", root, "--format", "text", "--annotate-topology")
	presentation, err := viewer.Build(t.Context(), loaded, viewer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	report := validator.ValidateBundle(loaded, &validator.ValidatorConfig{Strict: true, CheckLinks: true})

	// Assert.
	if read.IsError || search.IsError {
		t.Fatalf("read=%s, search=%s", resultText(t, read), resultText(t, search))
	}
	validateContractValue(t, "read_concept.output", read.StructuredContent)
	validateContractValue(t, "search_concepts.output", search.StructuredContent)
	status := read.StructuredContent.(map[string]any)["projection"].(map[string]any)["status_state"].(map[string]any)
	if status["raw"] != "invalid" || status["effective"] != "invalid" {
		t.Fatalf("MCP status = %#v", status)
	}
	var parsed struct {
		Status    string
		StatusRaw string `json:"status_raw"`
	}
	if err := json.Unmarshal(run("parse", filepath.Join(root, "unknown.md"), "--json"), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Status != "invalid" || parsed.StatusRaw != "invalid" {
		t.Fatalf("CLI status = %+v", parsed)
	}
	hits := search.StructuredContent.(map[string]any)["hits"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["concept"].(map[string]any)["id"] != "current" {
		t.Fatalf("MCP current hits = %#v", hits)
	}
	var sectionResult struct {
		Hits []struct {
			ID string `json:"concept_id"`
		}
	}
	if err := json.Unmarshal(sections, &sectionResult); err != nil {
		t.Fatal(err)
	}
	if len(sectionResult.Hits) != 1 || sectionResult.Hits[0].ID != "current" {
		t.Fatalf("CLI current hits = %+v", sectionResult.Hits)
	}
	for _, text := range []string{`#declaredStatus> "invalid" .`, `#declaredStatus> "17" .`, `#references> <local:bundle:future%20policy> .`} {
		if !bytes.Contains(graph, []byte(text)) {
			t.Errorf("graph missing %s: %s", text, graph)
		}
	}
	if !bytes.Contains(topology, []byte("status_raw=17")) {
		t.Errorf("annotated graph missing raw numeric status: %s", topology)
	}
	rawStatuses := make(map[string]string)
	for _, concept := range presentation.Concepts {
		rawStatuses[concept.ID] = concept.StatusRaw
	}
	if rawStatuses["unknown"] != "invalid" || rawStatuses["numeric"] != "17" || rawStatuses["current"] != "" {
		t.Errorf("viewer raw statuses = %v", rawStatuses)
	}
	if len(presentation.Edges) != 2 || !slices.ContainsFunc(presentation.Edges, func(edge viewer.Edge) bool {
		return edge.From == "unknown" && edge.To == "future policy" && edge.Raw == "future%20policy.md" && !edge.Exists
	}) {
		t.Errorf("viewer broken edges = %+v", presentation.Edges)
	}
	if !report.IsConformant() || !slices.ContainsFunc(report.Of(validator.SeverityInfo), func(d validator.Diagnostic) bool {
		return d.Code == "link_target_missing" && d.File == "unknown.md"
	}) {
		t.Errorf("validation = %+v", report.Diagnostics)
	}
}

func TestOKF03IgnoresIncompleteFamilyMembers(t *testing.T) {
	// Arrange. Each incomplete member has useful-looking data that must
	// remain in the document without contributing to the typed projection.
	root := t.TempDir()
	const markdown = `---
type: Attested Computation
runtime: postgres
sources:
  - {id: incomplete}
  - {id: policy, resource: 'urn:test:policy'}
generated: {at: 2026-11-27}
verified:
  - {by: 'human:without-time'}
  - {at: 2026-11-26}
  - {by: 'human:reviewer', at: 2026-11-26}
parameters:
  - {type: string}
  - {name: without_type}
  - {name: year, type: integer}
---

## Computation

` + "```sql\nSELECT 1\n```\n"
	writeTestFile(t, root, "metric.md", markdown)
	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	// Act.
	goReport := validator.ValidateBundle(loaded, &validator.ValidatorConfig{Strict: true})
	read := callMCPTool(t, server, "read_concept", map[string]any{"bundle_path": root, "concept_id": "metric"})
	validated := callMCPTool(t, server, "validate_bundle", map[string]any{"bundle_path": root, "strict": true})
	var out, stderr bytes.Buffer
	exit := okfcli.Run([]string{"validate", "--path", root, "--strict", "--json"}, &out, &stderr)
	var parseOut, parseErr bytes.Buffer
	parseExit := okfcli.Run([]string{"parse", filepath.Join(root, "metric.md"), "--json"}, &parseOut, &parseErr)
	var graphOut, graphErr bytes.Buffer
	graphExit := okfcli.Run([]string{"graph", root, "--profile", "skosovsky/okf-v0.2", "--format", "json-ld"}, &graphOut, &graphErr)

	// Assert.
	if exit != 0 || read.IsError || validated.IsError {
		t.Fatalf("CLI %d %s, MCP read %s, MCP validate %s", exit, stderr.String(), resultText(t, read), resultText(t, validated))
	}
	validateContractValue(t, "read_concept.output", read.StructuredContent)
	validateContractValue(t, "validate_bundle.output", validated.StructuredContent)
	projection := read.StructuredContent.(map[string]any)["projection"].(map[string]any)
	if projection["generated"] != nil || projection["trust"] != "human-reviewed" {
		t.Fatalf("incomplete generation contributed to typed provenance: %#v", projection)
	}
	var parsed struct {
		Generated                  json.RawMessage
		EffectiveContentChangeTime string `json:"effective_content_change_time"`
		TrustTier                  string `json:"trust_tier"`
	}
	if parseExit != 0 {
		t.Fatalf("CLI parse exit %d: %s", parseExit, parseErr.String())
	}
	if err := json.Unmarshal(parseOut.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Generated) != 0 || parsed.EffectiveContentChangeTime != "" || parsed.TrustTier != "human-reviewed" {
		t.Fatalf("CLI parse used incomplete generation: %+v", parsed)
	}
	if graphExit != 0 {
		t.Fatalf("CLI graph exit %d: %s", graphExit, graphErr.String())
	}
	var graph struct {
		Nodes []struct {
			Type string `json:"@type"`
		} `json:"@graph"`
	}
	if err := json.Unmarshal(graphOut.Bytes(), &graph); err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.Type == "proj:Generation" {
			t.Fatalf("graph emitted an incomplete generation: %s", graphOut.String())
		}
	}
	for _, family := range []struct {
		items      any
		key, value string
	}{
		{projection["sources"], "id", "policy"},
		{projection["attributions"], "id", "policy"},
		{projection["verified"], "by", "human:reviewer"},
		{projection["computation"].(map[string]any)["parameters"], "name", "year"},
	} {
		items := family.items.([]any)
		if len(items) != 1 || items[0].(map[string]any)[family.key] != family.value {
			t.Fatalf("want only %s=%s, got %#v", family.key, family.value, items)
		}
	}
	type finding struct{ code, field string }
	var gotGo, gotCLI, gotMCP []finding
	want := []finding{
		{"generated_by_missing", "generated.by"},
		{"parameter_name_missing", "parameters[0].name"},
		{"parameter_type_missing", "parameters[1].type"},
		{"source_resource_missing", "sources[0].resource"},
		{"verification_at_missing", "verified[0].at"},
		{"verification_by_missing", "verified[1].by"},
	}
	for _, diagnostic := range goReport.Of(validator.SeverityWarning) {
		gotGo = append(gotGo, finding{diagnostic.Code, diagnostic.FieldPath})
	}
	var cli struct {
		Conformant  bool
		Diagnostics []struct {
			Code      string
			FieldPath string `json:"field_path"`
		}
	}
	if err := json.Unmarshal(out.Bytes(), &cli); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range cli.Diagnostics {
		gotCLI = append(gotCLI, finding{diagnostic.Code, diagnostic.FieldPath})
	}
	mcp := validated.StructuredContent.(map[string]any)
	for _, item := range mcp["diagnostics"].([]any) {
		diagnostic := item.(map[string]any)
		gotMCP = append(gotMCP, finding{diagnostic["code"].(string), diagnostic["field"].(string)})
	}
	if !goReport.IsConformant() || !cli.Conformant || mcp["conformance"].(map[string]any)["conformant"] != true {
		t.Fatalf("incomplete optional data broke conformance: %+v, %s, %v", goReport, out.String(), mcp)
	}
	for name, got := range map[string][]finding{"Go": gotGo, "CLI": gotCLI, "MCP": gotMCP} {
		for _, expected := range want {
			if !slices.Contains(got, expected) {
				t.Errorf("%s missing %v: %v", name, expected, got)
			}
		}
	}
	if got := readTestFile(t, root, "metric.md"); got != markdown {
		t.Fatal("reading or validating changed raw Markdown")
	}
}

func TestOKF03TagShapesAgreeAcrossReadValidateAndGraph(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name, yaml string
		want       []string
	}{
		{"single", "tags: 'red,blue'\n", []string{"red,blue"}},
		{"mixed", "tags: ['green', 17]\n", []string{"green"}},
		{"null", "tags: null\nresource: null\n", nil},
		{"empty-string", "tags: ''\nresource: ''\n", nil},
		{"empty-list", "tags: []\nresource: []\n", nil},
	}
	for _, test := range cases {
		writeTestFile(t, root, test.name+".md", "---\ntype: Note\n"+test.yaml+"---\nBody.\n")
	}
	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	run := func(args ...string) []byte {
		t.Helper()
		var output, stderr bytes.Buffer
		if code := okfcli.Run(args, &output, &stderr); code != 0 {
			t.Fatalf("%v: %d %s", args, code, stderr.String())
		}
		return output.Bytes()
	}
	graphJSON := run("graph", root, "--profile", "skosovsky/okf-v0.2", "--format", "json-ld")
	var graph struct {
		Nodes []struct {
			ID  string          `json:"@id"`
			Tag json.RawMessage `json:"tag"`
		} `json:"@graph"`
	}
	if err := json.Unmarshal(graphJSON, &graph); err != nil {
		t.Fatal(err)
	}
	graphTags := make(map[string][]string)
	for _, node := range graph.Nodes {
		if len(node.Tag) == 0 {
			continue
		}
		var values []string
		if err := json.Unmarshal(node.Tag, &values); err != nil {
			var value string
			if err := json.Unmarshal(node.Tag, &value); err != nil {
				t.Fatal(err)
			}
			values = []string{value}
		}
		graphTags[node.ID] = values
	}

	for _, test := range cases {
		name, want := test.name, test.want
		id, err := bundle.ParseConceptID(name)
		if err != nil {
			t.Fatal(err)
		}
		concept, exists := loaded.Get(id)
		var cli struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(run("parse", filepath.Join(root, name+".md"), "--json"), &cli); err != nil {
			t.Fatal(err)
		}
		result := callMCPTool(t, server, "read_concept", map[string]any{"bundle_path": root, "concept_id": name})
		if result.IsError {
			t.Fatalf("MCP read %s: %s", name, resultText(t, result))
		}
		projection := result.StructuredContent.(map[string]any)["projection"].(map[string]any)
		var mcpTags []string
		for _, tag := range projection["tags"].([]any) {
			mcpTags = append(mcpTags, tag.(string))
		}
		seen := graphTags["local:bundle:"+name]
		if !exists || !slices.Equal(concept.Document.Frontmatter.Tags(), want) ||
			!slices.Equal(cli.Tags, want) || !slices.Equal(mcpTags, want) || !slices.Equal(seen, want) {
			t.Fatalf("%s: want %v, Go %v, CLI %v, MCP %v, graph %v", name, want, concept.Document.Frontmatter.Tags(), cli.Tags, mcpTags, seen)
		}
	}
	var validated struct {
		Conformant  bool `json:"conformant"`
		Diagnostics []struct {
			Code, File string
			Field      string `json:"field_path"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(run("validate", "--path", root, "--strict", "--json"), &validated); err != nil {
		t.Fatal(err)
	}
	if !validated.Conformant || len(validated.Diagnostics) != 1 || validated.Diagnostics[0].Code != "tags_invalid" ||
		validated.Diagnostics[0].File != "mixed.md" || validated.Diagnostics[0].Field != "tags[1]" {
		t.Fatalf("unexpected diagnostics: %+v", validated)
	}
}

func TestOKF03PatchAddsMetadataToPlainRootIndex(t *testing.T) {
	root := t.TempDir()
	const original = "# Rules\r\n\r\n- [Policy](policy.md)"
	writeTestFile(t, root, "index.md", original)
	writeTestFile(t, root, "policy.md", "---\ntype: Policy\n---\n\nPolicy.\n")
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	args := map[string]any{"bundle_path": root, "actor": "human:reviewer"}
	operation := map[string]any{"kind": "set_bundle_metadata", "bundle_metadata": map[string]any{"title": ""}}
	args["operations"] = []any{operation}
	if preview := callMCPTool(t, server, "preview_concept_patch", args); preview.IsError ||
		preview.StructuredContent.(map[string]any)["status"] != "noop" {
		t.Fatalf("remove absent metadata: %s", resultText(t, preview))
	}
	operation["bundle_metadata"] = map[string]any{"title": "2026-11-27", "description": "1e9999"}
	preview := callMCPTool(t, server, "preview_concept_patch", args)
	if preview.IsError {
		t.Fatalf("preview: %s", resultText(t, preview))
	}
	plan := preview.StructuredContent.(map[string]any)
	if plan["status"] != "applicable" {
		t.Fatalf("plan: %s", resultText(t, preview))
	}
	path := filepath.Join(root, "index.md")
	if current, err := os.ReadFile(path); err != nil || string(current) != original {
		t.Fatalf("preview changed index: %q, %v", current, err)
	}
	args["expected_revision"] = plan["base_revision"]
	args["expected_plan_digest"] = plan["plan_digest"]
	applied := callMCPTool(t, server, "apply_concept_patch", args)
	if applied.IsError || applied.StructuredContent.(map[string]any)["status"] != "applied" {
		t.Fatalf("apply: %s", resultText(t, applied))
	}

	current, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(current), "---\r\n") ||
		!strings.HasSuffix(string(current), "---\r\n"+original) || strings.Contains(string(current), "okf_version:") {
		t.Fatalf("patched index: %q, %v", current, err)
	}
	listed := callMCPTool(t, server, "list_concepts", map[string]any{"bundle_path": root})
	if listed.IsError {
		t.Fatalf("list: %s", resultText(t, listed))
	}
	result := listed.StructuredContent.(map[string]any)
	metadata := result["bundle_metadata"].(map[string]any)
	version := result["version"].(map[string]any)
	if metadata["title"] != "2026-11-27" || metadata["description"] != "1e9999" ||
		version["effective"] != "0.3" || version["resolution"] != "default" {
		t.Fatalf("metadata=%v, version=%v", metadata, version)
	}
}

func TestOKF03PatchPreservesMetadataAndSetsReplacement(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	const directives = "---\n%ROOT keep\n%YAML 1.3\n--- # YAML mapping\n"
	writeTestFile(t, root, "index.md", directives+"okf_version: '0.3'\ntitle: Original\ndescription: Keep this\n"+
		"x-project: {team: ops} # preserve\n---\n\n# Index\n\n- [Old](old.md)\n- [Current](current.md)\n")
	writeTestFile(t, root, "LICENSE.txt", "Sample license")
	writeTestFile(t, root, "old.md", "---\ntype: Policy\nstatus: deprecated\n"+
		"generated: {by: bot/v1, at: '2026-11-15T10:00:00Z', x-run: keep}\n---\n\nHistorical.\n")
	writeTestFile(t, root, "current.md", "---\ntype: Policy\n---\n\nCurrent.\n")
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	args := map[string]any{
		"bundle_path": root, "actor": "human:reviewer", "temporal_profile": "date-or-instant-0.3",
		"operations": []any{
			map[string]any{"kind": "set_bundle_metadata", "bundle_metadata": map[string]any{"title": "Delivery", "license": "/LICENSE.txt"}},
			map[string]any{"kind": "set_superseded_by", "concept_id": "old", "superseded_by": "/current.md"},
			map[string]any{"kind": "set_lifecycle", "concept_id": "old", "lifecycle": map[string]any{"status": "deprecated", "stale_after": "2026-11-18T10:00:00Z"}},
			map[string]any{"kind": "set_generated", "concept_id": "old", "generated": map[string]any{"by": "bot/v2", "at": "2026-11-17T10:00:00Z"}},
		},
	}

	// Act.
	preview := callMCPTool(t, server, "preview_concept_patch", args)
	if preview.IsError {
		t.Fatalf("preview: %s", resultText(t, preview))
	}
	plan := preview.StructuredContent.(map[string]any)
	args["expected_revision"] = plan["base_revision"]
	args["expected_plan_digest"] = plan["plan_digest"]
	applied := callMCPTool(t, server, "apply_concept_patch", args)
	replayed := callMCPTool(t, server, "apply_concept_patch", args)
	listed := callMCPTool(t, server, "list_concepts", map[string]any{"bundle_path": root})
	searched := callMCPTool(t, server, "search_concepts", map[string]any{"bundle_path": root, "query": "old", "current_only": true})

	// Assert.
	if applied.IsError || replayed.IsError || listed.IsError || searched.IsError {
		t.Fatalf("preview=%s apply=%s replay=%s list=%s search=%s", resultText(t, preview), resultText(t, applied),
			resultText(t, replayed), resultText(t, listed), resultText(t, searched))
	}
	receipt := applied.StructuredContent.(map[string]any)
	if receipt["status"] != "applied" || receipt["result_revision"] != plan["result_revision"] || resultText(t, applied) != resultText(t, replayed) {
		t.Fatalf("unexpected patch result: %v", receipt)
	}
	metadata := listed.StructuredContent.(map[string]any)["bundle_metadata"].(map[string]any)
	if metadata["title"] != "Delivery" || metadata["description"] != "Keep this" || metadata["license_path"] != "LICENSE.txt" {
		t.Fatalf("metadata: %#v", metadata)
	}
	hits := searched.StructuredContent.(map[string]any)["hits"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["concept"].(map[string]any)["id"] != "current" {
		t.Fatalf("replacement: %#v", hits)
	}
	for path, want := range map[string]string{"index.md": "x-project: {team: ops} # preserve", "old.md": "x-run: keep", "current.md": "\nCurrent.\n"} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || !strings.Contains(string(data), want) || path == "index.md" && !strings.HasPrefix(string(data), directives) {
			t.Fatalf("%s lost %q: %q (%v)", path, want, data, err)
		}
	}
}

func TestOKF03PatchWritesDateOnlyProvenanceAndPreservesHistory(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n# Notes\n\n- [Rule](rule.md)\n")
	const directives = "---\n%YAML 1.2\n%TAG !ok! tag:example.org,2026:\n--- # YAML mapping\n"
	writeTestFile(t, root, "rule.md", directives+"type: Guide\nexternal: !ok!code preserve\n"+
		"generated: {by: process:old, at: '2026-11-14', audit: keep}\n"+
		"verified:\n  - {by: 'human:previous', at: '2026-11-15', receipt: keep}\n---\n\nRule.\n")
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	review := map[string]any{"by": "human:reviewer", "at": "2026-11-17"}
	edits := map[string]any{
		"bundle_path": root, "actor": "human:reviewer",
		"operations": []any{
			map[string]any{"kind": "set_generated", "concept_id": "rule", "generated": map[string]any{"by": "urn:okf:publisher", "at": "2026-11-17"}},
			map[string]any{"kind": "ensure_verification", "concept_id": "rule", "verification": review},
		},
	}
	if legacy := callMCPTool(t, server, "preview_concept_patch", edits); !legacy.IsError {
		t.Fatalf("legacy profile accepted a calendar date: %s", resultText(t, legacy))
	}
	edits["temporal_profile"] = "date-or-instant-0.3"

	apply := func() {
		t.Helper()
		preview := callMCPTool(t, server, "preview_concept_patch", edits)
		if preview.IsError {
			t.Fatalf("preview: %s", resultText(t, preview))
		}
		plan := preview.StructuredContent.(map[string]any)
		if plan["status"] != "applicable" {
			t.Fatalf("unexpected preview: %s", resultText(t, preview))
		}
		edits["expected_revision"] = plan["base_revision"]
		edits["expected_plan_digest"] = plan["plan_digest"]
		applied := callMCPTool(t, server, "apply_concept_patch", edits)
		replayed := callMCPTool(t, server, "apply_concept_patch", edits)
		if applied.IsError || replayed.IsError || resultText(t, applied) != resultText(t, replayed) {
			t.Fatalf("apply=%s replay=%s", resultText(t, applied), resultText(t, replayed))
		}
		delete(edits, "expected_revision")
		delete(edits, "expected_plan_digest")
	}
	read := func(wantTrust string) {
		t.Helper()
		result := callMCPTool(t, server, "read_concept", map[string]any{"bundle_path": root, "concept_id": "rule"})
		if result.IsError {
			t.Fatalf("read: %s", resultText(t, result))
		}
		projection := result.StructuredContent.(map[string]any)["projection"].(map[string]any)
		data, err := os.ReadFile(filepath.Join(root, "rule.md"))
		if projection["trust"] != wantTrust {
			t.Fatalf("trust=%v, want %s, document=%s", projection["trust"], wantTrust, data)
		}
		if err != nil || !strings.HasPrefix(string(data), directives) || !strings.Contains(string(data), "external: !ok!code preserve") ||
			!strings.Contains(string(data), "audit: keep") || !strings.Contains(string(data), "receipt: keep") ||
			!strings.Contains(string(data), "2026-11-17") || strings.Contains(string(data), "2026-11-17T") {
			t.Fatalf("date or opaque metadata changed: %q, %v", data, err)
		}
	}

	apply()
	read("human-reviewed")
	edits["operations"] = []any{map[string]any{"kind": "remove_verification", "concept_id": "rule", "verification": review}}
	apply()
	read("unverified")
}

func TestOKF03WriteConceptPreservesExtensionOwnership(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n\n# Notes\n\n- [Policy](policy.md)\n")
	oldFrontmatter := "type: Policy\nowner: ops\nsources:\n  - {id: policy, resource: 'urn:test:policy', x-audit: {owner: ops}}\n"
	before := "---\n" + oldFrontmatter + "---\n\nOld policy.\n"
	writeTestFile(t, root, "policy.md", before)
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	args := map[string]any{"bundle_path": root, "concept_id": "policy", "body": "Updated policy."}
	read := func() string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(root, "policy.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}

	// Act.
	args["frontmatter"] = "type: Policy\nowner: ops\nsources: [{id: policy, resource: 'urn:test:policy'}]\n"
	lost := callMCPTool(t, server, "write_concept", args)
	afterRejection := read()
	args["frontmatter"] = oldFrontmatter + "generated: {by: 'human:editor', at: '2026-11-18T10:00:00Z'}\n"
	written := callMCPTool(t, server, "write_concept", args)

	// Assert.
	if !lost.IsError || !strings.Contains(resultText(t, lost), "sources[0].x-audit") || afterRejection != before {
		t.Fatalf("loss rejection: %s, file=%q", resultText(t, lost), afterRejection)
	}
	if written.IsError || !strings.Contains(resultText(t, written), "\"status\":\"success\"") {
		t.Fatalf("write: %s", resultText(t, written))
	}
	for _, want := range []string{"owner: ops", "x-audit", "human:editor", "Updated policy."} {
		if !strings.Contains(read(), want) {
			t.Fatalf("saved document is missing %q: %s", want, read())
		}
	}
}

func TestOKF03WriteReordersSourcesWithoutMovingClaimAttribution(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	const metadata = "type: Policy\ntitle: &primary dashboard\nusage_window: {from: 2026-10-01, to: 2026-10-31}\n"
	const dashboard = "  - id: *primary\n    resource: 'urn:test:dashboard'\n    usage_count: 40\n" +
		"    usage_window: {from: 2026-11-01, to: 2026-11-30, x-training-calendar: UTC}\n    x-training-owner: analytics\n"
	const policy = "  - {id: policy, resource: 'urn:test:policy', usage_count: 7, x-training-owner: compliance}\n"
	const body = "\nObserved usage.[^dashboard] Governing rule.[^policy]\n\n" +
		"[^dashboard]: Dashboard views.\n[^policy]: Policy reads.\n"
	original := "---\n" + metadata + "sources:\n" + dashboard + policy + "---\n" + body
	writeTestFile(t, root, "rule.md", original)
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	args := map[string]any{"bundle_path": root, "concept_id": "rule", "body": body}

	// Act. A new target behind the same alias must not inherit the old
	// source's extensions. A different representation of the same ID can.
	args["frontmatter"] = strings.Replace(metadata, "&primary dashboard", "&primary impostor", 1) + "sources:\n" + dashboard + policy
	reassigned := callMCPTool(t, server, "write_concept", args)
	afterRejection := readTestFile(t, root, "rule.md")
	knownEdit := strings.NewReplacer("id: *primary", "id: dashboard", "urn:test:dashboard", "urn:test:dashboard-v2").Replace(dashboard)
	args["frontmatter"] = metadata + "sources:\n" + policy + knownEdit
	written := callMCPTool(t, server, "write_concept", args)
	read := callMCPTool(t, server, "read_concept", map[string]any{"bundle_path": root, "concept_id": "rule"})

	// Assert.
	if !reassigned.IsError || !strings.Contains(resultText(t, reassigned), "sources[0].usage_window.x-training-calendar") || afterRejection != original {
		t.Fatalf("alias reassignment=%s, published=%q", resultText(t, reassigned), afterRejection)
	}
	if written.IsError || read.IsError {
		t.Fatalf("write=%s read=%s", resultText(t, written), resultText(t, read))
	}
	validateContractValue(t, "read_concept.output", read.StructuredContent)
	projection := read.StructuredContent.(map[string]any)["projection"].(map[string]any)
	sources := projection["sources"].([]any)
	if len(sources) != 2 || sources[0].(map[string]any)["id"] != "policy" || sources[1].(map[string]any)["id"] != "dashboard" {
		t.Fatalf("reordered sources=%#v", sources)
	}
	attributions := projection["attributions"].([]any)
	matches := make(map[string]string)
	for _, value := range attributions {
		attribution := value.(map[string]any)
		id := attribution["id"].(string)
		references := attribution["references"].([]any)
		joined := attribution["sources"].([]any)
		if len(references) != 1 || len(joined) != 1 || references[0].(map[string]any)["id"] != id {
			t.Fatalf("attribution=%#v", attribution)
		}
		matches[id] = joined[0].(map[string]any)["resource"].(string)
	}
	if len(matches) != 2 || matches["dashboard"] != "urn:test:dashboard-v2" || matches["policy"] != "urn:test:policy" {
		t.Fatalf("read footnotes point to the wrong sources: %v", matches)
	}
	shared := projection["usage_window"].(map[string]any)
	local := sources[1].(map[string]any)["usage_window"].(map[string]any)
	if shared["from"] != "2026-10-01" || shared["to"] != "2026-10-31" ||
		local["from"] != "2026-11-01" || local["to"] != "2026-11-30" ||
		sources[0].(map[string]any)["usage_window"] != nil ||
		sources[0].(map[string]any)["usage_count"] != "7" || sources[1].(map[string]any)["usage_count"] != "40" {
		t.Fatalf("windows: shared=%#v, sources=%#v", shared, sources)
	}
	published := readTestFile(t, root, "rule.md")
	for _, mustKeep := range []string{policy, knownEdit, "[^dashboard]: Dashboard views.", "[^policy]: Policy reads."} {
		if !strings.Contains(published, mustKeep) {
			t.Fatalf("missing published bytes %q: %s", mustKeep, published)
		}
	}
}

func TestOKF03WriteConceptRejectsEquivalentYAMLKeys(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n\n# Notes\n")
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	result := callMCPTool(t, server, "write_concept", map[string]any{
		"bundle_path": root,
		"concept_id":  "policy",
		"body":        "Policy.",
		"frontmatter": "type: Policy\nexternal: {11: one, 0xB: two}\n",
	})

	if !result.IsError || !strings.Contains(resultText(t, result), "duplicate YAML key") {
		t.Fatalf("want duplicate YAML key rejection, got %s", resultText(t, result))
	}
	if _, err := os.Stat(filepath.Join(root, "policy.md")); !os.IsNotExist(err) {
		t.Fatalf("rejected concept should not exist, stat error: %v", err)
	}
}

func TestOKF03ReadConceptProjectsCoreYAMLAndSingletonSource(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n\n# Notes\n\n- [Policy](policy.md)\n")
	const frontmatter = "%OTHER preserve\n--- # YAML mapping\ntype: 2026-11-27\nsources: {id: policy, resource: 'urn:test:policy', usage_count: 011}\n"
	writeTestFile(t, root, "policy.md", "---\n"+frontmatter+"---\n\nRule.[^policy]\n\n[^policy]: Policy source.\n")
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	result := callMCPTool(t, server, "read_concept", map[string]any{
		"bundle_path": root, "concept_id": "policy",
	})
	encoded, err := json.Marshal(result.StructuredContent)
	projection := string(encoded)
	if result.IsError || err != nil || !strings.Contains(projection, `"type":"2026-11-27"`) ||
		!strings.Contains(projection, `"usage_count":"11"`) || !strings.Contains(resultText(t, result), "usage_count: 011") ||
		!strings.Contains(projection, `"normalized_id":"policy"`) {
		t.Fatalf("unexpected read projection: %s (%v), %s", projection, err, resultText(t, result))
	}
	written := callMCPTool(t, server, "write_concept", map[string]any{
		"bundle_path": root, "concept_id": "policy",
		"frontmatter": frontmatter,
		"body":        "Updated rule.[^policy]\n\n[^policy]: Policy source.\n",
	})
	if written.IsError || !strings.Contains(resultText(t, written), `"status":"success"`) {
		t.Fatalf("write: %s", resultText(t, written))
	}
	data, err := os.ReadFile(filepath.Join(root, "policy.md"))
	if err != nil || !strings.HasPrefix(string(data), "---\n"+frontmatter+"---\n") {
		t.Fatalf("written bytes = %q, %v", data, err)
	}
}

func TestOKF03SourcePatchReplacesOnlyTheChangedCoreInteger(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n\n# Notes\n\n- [Policy](policy.md)\n")
	const original = "---\ntype: Policy\nsources: {id: ref, resource: 'scope',  usage_count: 079 , x-extra: !other keep}\n" +
		"usage_window: {from: 2026-11-01, to: 2026-11-30}\n---\n\nPolicy.\n"
	writeTestFile(t, root, "policy.md", original)
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	source := map[string]any{"id": "ref", "resource": "scope", "usage_count": "79"}
	args := map[string]any{
		"bundle_path": root, "actor": "human:auditor", "temporal_profile": "date-or-instant-0.3",
		"operations": []any{map[string]any{"kind": "put_source", "concept_id": "policy", "source": source}},
	}
	unchanged := callMCPTool(t, server, "preview_concept_patch", args)
	if unchanged.IsError || unchanged.StructuredContent.(map[string]any)["status"] != "noop" {
		t.Fatalf("equivalent count preview: %s", resultText(t, unchanged))
	}
	source["usage_count"] = "80"
	preview := callMCPTool(t, server, "preview_concept_patch", args)
	if preview.IsError {
		t.Fatalf("preview: %s", resultText(t, preview))
	}
	plan := preview.StructuredContent.(map[string]any)
	args["expected_revision"] = plan["base_revision"]
	args["expected_plan_digest"] = plan["plan_digest"]
	applied := callMCPTool(t, server, "apply_concept_patch", args)
	if applied.IsError || applied.StructuredContent.(map[string]any)["status"] != "applied" {
		t.Fatalf("apply: %s", resultText(t, applied))
	}
	data, err := os.ReadFile(filepath.Join(root, "policy.md"))
	want := strings.Replace(original, "usage_count: 079", "usage_count: 80", 1)
	if err != nil || string(data) != want {
		t.Fatalf("written bytes = %q, %v, want %q", data, err, want)
	}
	read := callMCPTool(t, server, "read_concept", map[string]any{"bundle_path": root, "concept_id": "policy"})
	projection, err := json.Marshal(read.StructuredContent)
	if read.IsError || err != nil || !strings.Contains(string(projection), `"usage_count":"80"`) {
		t.Fatalf("read: %s (%v)", projection, err)
	}
}

func TestOKF03MCPComputationShowsUnavailableFileWithoutInlineFallback(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n\n# Notes\n\n- [Metric](metric.md)\n")
	writeTestFile(t, root, "metric.md", "---\ntype: Attested Computation\nruntime: postgres\n"+
		"computation: /references/missing.sql\nparameters: {name: year, type: integer}\n---\n\n"+
		"## Computation\n\n```sql\nSELECT 1\n```\n")
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	// Act.
	read := callMCPTool(t, server, "read_concept", map[string]any{"bundle_path": root, "concept_id": "metric"})
	list := callMCPTool(t, server, "list_concepts", map[string]any{"bundle_path": root})

	// Assert.
	if read.IsError || list.IsError {
		t.Fatalf("read=%s list=%s", resultText(t, read), resultText(t, list))
	}
	typed := read.StructuredContent.(map[string]any)["projection"].(map[string]any)["computation"].(map[string]any)
	summary := list.StructuredContent.(map[string]any)["concepts"].([]any)[0].(map[string]any)["computation"].(map[string]any)
	for _, state := range []map[string]any{typed, summary} {
		if state["attestable"] != false || state["mode"] != "file" || state["unavailable_reason"] != "missing_computation_file" {
			t.Fatalf("computation state: %#v", state)
		}
	}
	if params := typed["parameters"].([]any); len(params) != 1 || params[0].(map[string]any)["required"] != false {
		t.Fatalf("parameters: %#v", typed["parameters"])
	}
	validateContractValue(t, "read_concept.output", read.StructuredContent)
	validateContractValue(t, "list_concepts.output", list.StructuredContent)
}

func TestOKF03ComputationAvailabilityAgreesAcrossReaders(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n# Metrics\n\n- [Metric](metrics/amount.md)\n")
	writeTestFile(t, root, "refs/check.py", "Attester fixture")
	writeTestFile(t, root, "refs/amount.sql", "SELECT 3\n")
	const first = "SELECT 1\n"
	contract := "---\ntype: Attested Computation\nruntime: postgres\n"
	body := "---\n\n## Computation\n\n```sql\n" + first + "```\n\n```sql\nSELECT 2\n```\n"
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	for _, test := range []struct {
		name, fields, mode, resolved, reason, inline string
		valid                                        bool
	}{
		{"first fence", "attester: {resource: /refs/check.py}\n", "inline", "", "", first, true},
		{"present file", "attester: {resource: /refs/check.py}\ncomputation: ../refs/amount.sql\n", "file", "refs/amount.sql", "", "", true},
		{"missing file", "attester: {resource: /refs/check.py}\ncomputation: ../refs/missing.sql\n", "file", "", "missing_computation_file", "", false},
		{"missing attester file", "attester: {resource: /refs/missing.py}\n", "inline", "", "missing_attester_file", first, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeTestFile(t, root, "metrics/amount.md", contract+test.fields+body)

			// Act.
			var out, stderr bytes.Buffer
			target := filepath.Join(root, "metrics", "amount.md")
			exit := okfcli.Run([]string{"parse", target, "--json"}, &out, &stderr)
			var graphOut, graphErr bytes.Buffer
			graphExit := okfcli.Run([]string{"graph", root, "--format", "json-ld"}, &graphOut, &graphErr)
			read := callMCPTool(t, server, "read_concept", map[string]any{"bundle_path": root, "concept_id": "metrics/amount"})
			loaded, err := bundle.LoadBundle(root)
			if err != nil {
				t.Fatal(err)
			}
			view, err := viewer.Build(t.Context(), loaded, viewer.Options{})
			if err != nil {
				t.Fatal(err)
			}
			report := validator.ValidateBundle(loaded, &validator.ValidatorConfig{Strict: true})

			// Assert.
			if exit != 0 || read.IsError || !report.IsConformant() {
				t.Fatalf("parse=%d %s, MCP=%s, validation=%+v", exit, stderr.String(), resultText(t, read), report)
			}
			var parse struct {
				Computation struct {
					Mode, Inline                string
					Valid, Attestable, Multiple bool
					ResolvedPath                string `json:"resolved_path"`
					Reason                      string `json:"unavailable_reason"`
				} `json:"attested_computation_audit"`
			}
			if err := json.Unmarshal(out.Bytes(), &parse); err != nil {
				t.Fatal(err)
			}
			computation := parse.Computation
			attestable := test.reason == ""
			if computation.Mode != test.mode || computation.Inline != test.inline || computation.ResolvedPath != test.resolved ||
				computation.Attestable != attestable || computation.Valid != test.valid || computation.Reason != test.reason {
				t.Fatalf("CLI computation = %+v", computation)
			}
			typed := read.StructuredContent.(map[string]any)["projection"].(map[string]any)["computation"].(map[string]any)
			reason, _ := typed["unavailable_reason"].(string)
			if typed["mode"] != test.mode || typed["attestable"] != attestable || reason != test.reason {
				t.Fatalf("MCP computation = %#v", typed)
			}
			if len(view.Concepts) != 1 {
				t.Fatalf("viewer = %+v", view.Concepts)
			}
			metric := view.Concepts[0]
			if metric.Attestable == nil || *metric.Attestable != attestable ||
				metric.ComputationUnavailableReason != test.reason {
				t.Fatalf("viewer = %+v", view.Concepts)
			}
			if graphExit != 0 {
				t.Fatalf("graph exit %d: %s", graphExit, graphErr.String())
			}
			var graph struct {
				Nodes []map[string]any `json:"@graph"`
			}
			if err := json.Unmarshal(graphOut.Bytes(), &graph); err != nil {
				t.Fatal(err)
			}
			contractIndex := slices.IndexFunc(graph.Nodes, func(node map[string]any) bool {
				return node["@type"] == "proj:AttestedComputationContract"
			})
			if contractIndex < 0 {
				t.Fatalf("graph contract missing: %s", graphOut.String())
			}
			node := graph.Nodes[contractIndex]
			graphReason, _ := node["computationUnavailableReason"].(string)
			if node["computationMode"] != test.mode || (node["attestable"] != nil) != attestable || graphReason != test.reason {
				t.Fatalf("graph computation = %#v", node)
			}
			hasMultiple := slices.ContainsFunc(report.Of(validator.SeverityWarning), func(d validator.Diagnostic) bool {
				return d.Code == "computation_inline_multiple" && d.FieldPath == "body.Computation"
			})
			if hasMultiple != (test.mode == "inline") || computation.Multiple != hasMultiple {
				t.Fatalf("multiple fences: CLI=%t, warnings=%+v", computation.Multiple, report.Diagnostics)
			}
		})
	}
}
