package graph

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
)

func TestLegacyWrappersMatchFrozenGoldens(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := sampleGraphBundle(t)
	tests := []struct {
		name    string
		wrapper func(io.Writer) error
		golden  string
	}{
		{
			name:    "text",
			wrapper: func(w io.Writer) error { return RenderText(w, b) },
			golden:  "text.golden",
		},
		{
			name:    "dot",
			wrapper: func(w io.Writer) error { return RenderDOT(w, b) },
			golden:  "dot.golden",
		},
		{
			name:    "mermaid",
			wrapper: func(w io.Writer) error { return RenderMermaid(w, b) },
			golden:  "mermaid.golden",
		},
		{
			name:    "jsonld",
			wrapper: func(w io.Writer) error { return RenderJSONLD(w, b) },
			golden:  "jsonld.golden",
		},
		{
			name:    "ntriples",
			wrapper: func(w io.Writer) error { return RenderNTriples(w, b) },
			golden:  "ntriples.golden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			want, err := os.ReadFile(filepath.Join("testdata", "legacy", tt.golden))
			if err != nil {
				t.Fatalf("ReadFile(%q) error = %v", tt.golden, err)
			}
			var wrapper strings.Builder

			// Act.
			wrapperErr := tt.wrapper(&wrapper)

			// Assert.
			if wrapperErr != nil {
				t.Fatalf("render error = %v", wrapperErr)
			}
			if wrapper.String() != string(want) {
				t.Fatalf("wrapper output = %q, frozen golden = %q", wrapper.String(), string(want))
			}
		})
	}
}

func TestToolkitProjectionIsExplicitlyMarked(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := sampleGraphBundle(t)
	options := Options{Profile: ProjectionProfileToolkitV02}

	// Act.
	var jsonld, ntriples strings.Builder
	jsonldErr := RenderJSONLDWithOptions(&jsonld, b, options)
	ntriplesErr := RenderNTriplesWithOptions(&ntriples, b, options)

	// Assert.
	if jsonldErr != nil || ntriplesErr != nil {
		t.Fatalf("render errors = (%v, %v)", jsonldErr, ntriplesErr)
	}
	for _, want := range []string{
		`"profile": "skosovsky/okf-v0.2"`,
		`"projectionVersion": "0.2"`,
		toolkitProjectionNamespace,
	} {
		if !strings.Contains(jsonld.String(), want) {
			t.Fatalf("RenderJSONLDWithOptions() =\n%s\nwant %q", jsonld.String(), want)
		}
	}
	if !strings.Contains(ntriples.String(), toolkitProjectionNamespace+"profile") ||
		!strings.Contains(ntriples.String(), `"skosovsky/okf-v0.2"`) {
		t.Fatalf("RenderNTriplesWithOptions() =\n%s\nwant marked toolkit profile", ntriples.String())
	}
}

func TestToolkitExtensionRelationPolicy(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := sampleSemanticGraphBundle(t)

	// Act.
	var included, excluded strings.Builder
	includeErr := RenderNTriplesWithOptions(&included, b, Options{
		Profile:            ProjectionProfileToolkitV02,
		ExtensionRelations: ExtensionRelationsInclude,
	})
	excludeErr := RenderNTriplesWithOptions(&excluded, b, Options{
		Profile:            ProjectionProfileToolkitV02,
		ExtensionRelations: ExtensionRelationsExclude,
	})

	// Assert.
	if includeErr != nil || excludeErr != nil {
		t.Fatalf("render errors = (%v, %v)", includeErr, excludeErr)
	}
	if !strings.Contains(included.String(), "ToolkitExtensionRelation") ||
		!strings.Contains(included.String(), `"skosovsky/okf"`) ||
		!strings.Contains(included.String(), `<local:bundle:missing>`) ||
		!strings.Contains(included.String(), `<local:bundle:b#absent>`) ||
		!strings.Contains(included.String(), toolkitProjectionNamespace+`targetExists> "false"`) {
		t.Fatalf("included output =\n%s\nwant marked toolkit extension relation", included.String())
	}
	if strings.Contains(excluded.String(), "ToolkitExtensionRelation") {
		t.Fatalf("excluded output =\n%s\ncontains toolkit extension relation", excluded.String())
	}
}

func TestOptionsRejectUnknownValues(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := sampleGraphBundle(t)
	tests := []struct {
		name    string
		options Options
	}{
		{name: "profile", options: Options{Profile: "future"}},
		{name: "extension policy", options: Options{ExtensionRelations: "sometimes"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			err := RenderJSONLDWithOptions(io.Discard, b, tt.options)

			// Assert.
			if err == nil {
				t.Fatal("RenderJSONLDWithOptions() error = nil, want option validation error")
			}
		})
	}
}

func TestToolkitRenderersPropagateWriterErrors(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := sampleGraphBundle(t)
	writeErr := errors.New("write failed")
	options := Options{Profile: ProjectionProfileToolkitV02}
	renderers := []struct {
		name string
		run  func(io.Writer) error
	}{
		{name: "jsonld", run: func(w io.Writer) error { return RenderJSONLDWithOptions(w, b, options) }},
		{name: "ntriples", run: func(w io.Writer) error { return RenderNTriplesWithOptions(w, b, options) }},
	}

	for _, renderer := range renderers {
		t.Run(renderer.name+"/write-error", func(t *testing.T) {
			// Act.
			err := renderer.run(&failingWriter{err: writeErr})

			// Assert.
			if !errors.Is(err, writeErr) {
				t.Fatalf("%s error = %v, want %v", renderer.name, err, writeErr)
			}
		})
		t.Run(renderer.name+"/short-write", func(t *testing.T) {
			// Act.
			err := renderer.run(shortWriter{})

			// Assert.
			if !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("%s error = %v, want io.ErrShortWrite", renderer.name, err)
			}
		})
	}
}

func TestToolkitV02ProjectsTypedSignalFamilies(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := fullV02GraphBundle(t, false)
	asOf := time.Date(2026, 9, 23, 12, 0, 0, 0, time.FixedZone("test", 7*60*60))
	options := Options{
		Profile:            ProjectionProfileToolkitV02,
		AsOf:               &asOf,
		ExtensionRelations: ExtensionRelationsInclude,
	}

	// Act.
	var jsonld, ntriples strings.Builder
	jsonldErr := RenderJSONLDWithOptions(&jsonld, b, options)
	ntriplesErr := RenderNTriplesWithOptions(&ntriples, b, options)

	// Assert.
	if jsonldErr != nil || ntriplesErr != nil {
		t.Fatalf("render errors = (%v, %v)", jsonldErr, ntriplesErr)
	}
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	projection := toolkitNodeByID(t, document.Graph, toolkitSyntheticIRI("projection"))
	if projection["declaredOKFVersion"] != "0.2" ||
		projection["effectiveOKFVersion"] != "0.2" ||
		projection["compatibilityMode"] != "native" {
		t.Fatalf("version metadata = (%q, %q, %q), want (0.2, 0.2, native)",
			projection["declaredOKFVersion"], projection["effectiveOKFVersion"], projection["compatibilityMode"])
	}
	concept := toolkitNodeByID(t, document.Graph, "local:bundle:computations%2Frevenue")
	if concept["trustTier"] != "human-reviewed" || concept["effectiveStatus"] != "stable" {
		t.Fatalf("concept = %#v, want derived trust and effective status", concept)
	}
	if !typedJSONLDValueEquals(concept["stale"], true, "xsd:boolean") ||
		!typedJSONLDValueEquals(concept["stalenessAsOf"], "2026-09-23", "xsd:date") {
		t.Fatalf("concept staleness = %#v / %#v, want inclusive boundary", concept["stale"], concept["stalenessAsOf"])
	}
	generation := toolkitNodeByType(t, document.Graph, "proj:Generation")
	if generation["actor"] != "reference_agent/custom" ||
		!typedJSONLDValueEquals(generation["at"], "2026-06-28T14:00:00Z", "xsd:dateTime") {
		t.Fatalf("generation = %#v, want typed actor/time", generation)
	}
	unknownAttribution := toolkitNodeByID(t, document.Graph, toolkitSyntheticIRI("attribution", "computations/revenue", "unknown"))
	if !typedJSONLDValueEquals(unknownAttribution["resolved"], false, "xsd:boolean") {
		t.Fatalf("unknown attribution = %#v, want unresolved visibility", unknownAttribution)
	}
	localAsset := toolkitNodeByID(t, document.Graph, toolkitSyntheticIRI("asset", "references/policy.txt"))
	if !typedJSONLDValueEquals(localAsset["exists"], true, "xsd:boolean") {
		t.Fatalf("local asset = %#v, want exists=true", localAsset)
	}
	brokenAsset := toolkitNodeByID(t, document.Graph, toolkitSyntheticIRI("asset", "references/missing.py"))
	if !typedJSONLDValueEquals(brokenAsset["exists"], false, "xsd:boolean") {
		t.Fatalf("broken asset = %#v, want exists=false", brokenAsset)
	}
	brokenSourceAsset := toolkitNodeByID(t, document.Graph, toolkitSyntheticIRI("asset", "references/missing-source.txt"))
	if !typedJSONLDValueEquals(brokenSourceAsset["exists"], false, "xsd:boolean") {
		t.Fatalf("broken source asset = %#v, want exists=false", brokenSourceAsset)
	}
	if toolkitNodeWithIDFragment(document.Graph, "https%3A") != nil ||
		toolkitNodeWithIDFragment(document.Graph, "all%20queries") != nil {
		t.Fatalf("graph contains external/scope bundle resource node: %#v", document.Graph)
	}
	localSource := toolkitNodeByTypeAndProperty(t, document.Graph, "proj:ProvenanceSource", "sourceID", "local-policy")
	if localSource["resourceKind"] != "relative" ||
		!typedJSONLDValueEquals(localSource["usageFrom"], "2026-06-01", "xsd:date") ||
		!typedJSONLDValueEquals(localSource["sourceUsageWindowOverride"], false, "xsd:boolean") {
		t.Fatalf("local source = %#v, want shared usage window and local classification", localSource)
	}
	scopedSource := toolkitNodeByTypeAndProperty(t, document.Graph, "proj:ProvenanceSource", "sourceID", "scoped")
	if scopedSource["resourceKind"] != "scope" ||
		!typedJSONLDValueEquals(scopedSource["usageFrom"], "2026-05-01", "xsd:date") ||
		!typedJSONLDValueEquals(scopedSource["sourceUsageWindowOverride"], true, "xsd:boolean") {
		t.Fatalf("scoped source = %#v, want per-source usage window and scope classification", scopedSource)
	}
	if toolkitNodeWithIDFragment(document.Graph, "#local-policy") != nil {
		t.Fatalf("sources[].id became a fragment node: %#v", document.Graph)
	}
	ntriplesText := ntriples.String()
	for _, want := range []string{
		`"2026-09-23"^^<http://www.w3.org/2001/XMLSchema#date>`,
		`"2026-06-28T14:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime>`,
		`"9001"^^<http://www.w3.org/2001/XMLSchema#integer>`,
		`"orbital-db"`,
		`"opaque-kind"`,
		`"result_set"`,
	} {
		if !strings.Contains(ntriplesText, want) {
			t.Fatalf("N-Triples =\n%s\nwant %q", ntriplesText, want)
		}
	}
}

func TestToolkitV02NormalizedAttributionJoinIsManyToManyAndOrderIndependent(t *testing.T) {
	t.Parallel()

	// Arrange.
	load := func(t *testing.T, sources string) *bundle.Bundle {
		t.Helper()
		root := t.TempDir()
		writeGraphFile(t, root, "a.md", "---\n"+
			"type: Note\n"+
			"sources:\n"+sources+
			"---\n"+
			"Claim.[^SPEC]\n\n"+
			"[^spec]: Definition.\n")
		return loadGraphBundle(t, root)
	}
	first := load(t, ""+
		"  - {id: Spec, resource: https://example.test/upper}\n"+
		"  - {id: spec, resource: https://example.test/lower}\n")
	shuffled := load(t, ""+
		"  - {id: spec, resource: https://example.test/lower}\n"+
		"  - {id: Spec, resource: https://example.test/upper}\n")
	options := Options{Profile: ProjectionProfileToolkitV02}
	var firstJSONLD, shuffledJSONLD, firstNTriples, shuffledNTriples strings.Builder

	// Act.
	firstJSONErr := RenderJSONLDWithOptions(&firstJSONLD, first, options)
	shuffledJSONErr := RenderJSONLDWithOptions(&shuffledJSONLD, shuffled, options)
	firstRDFErr := RenderNTriplesWithOptions(&firstNTriples, first, options)
	shuffledRDFErr := RenderNTriplesWithOptions(&shuffledNTriples, shuffled, options)
	jsonTriples, jsonSemanticErr := standardsJSONLDTriples(firstJSONLD.String())
	rdfTriples, rdfSemanticErr := semanticNTriples(firstNTriples.String())

	// Assert.
	if firstJSONErr != nil || shuffledJSONErr != nil || firstRDFErr != nil ||
		shuffledRDFErr != nil || jsonSemanticErr != nil || rdfSemanticErr != nil {
		t.Fatalf("render/semantic errors = (%v, %v, %v, %v, %v, %v)",
			firstJSONErr, shuffledJSONErr, firstRDFErr, shuffledRDFErr,
			jsonSemanticErr, rdfSemanticErr)
	}
	if firstJSONLD.String() != shuffledJSONLD.String() {
		t.Fatalf("JSON-LD depends on source order:\nfirst:\n%s\nshuffled:\n%s",
			firstJSONLD.String(), shuffledJSONLD.String())
	}
	if firstNTriples.String() != shuffledNTriples.String() {
		t.Fatalf("N-Triples depends on source order:\nfirst:\n%s\nshuffled:\n%s",
			firstNTriples.String(), shuffledNTriples.String())
	}
	if strings.Join(jsonTriples, "\n") != strings.Join(rdfTriples, "\n") {
		t.Fatalf("semantic parity mismatch:\nJSON-LD:\n%s\nN-Triples:\n%s",
			strings.Join(jsonTriples, "\n"), strings.Join(rdfTriples, "\n"))
	}
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(firstJSONLD.String()), &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	attribution := toolkitNodeByID(
		t,
		document.Graph,
		toolkitChildIRI(mustConceptID(t, "a"), "attribution", "spec"),
	)
	if attribution["sourceID"] != "spec" ||
		!typedJSONLDValueEquals(attribution["resolved"], true, "xsd:boolean") {
		t.Fatalf("normalized attribution = %#v, want resolved sourceID=spec", attribution)
	}
	links, ok := attribution["attributedSource"].([]any)
	if !ok || len(links) != 2 {
		t.Fatalf("attributedSource = %#v, want both normalized matches", attribution["attributedSource"])
	}
	wantLinks := map[string]bool{
		toolkitChildIRI(mustConceptID(t, "a"), "source", "000000"): true,
		toolkitChildIRI(mustConceptID(t, "a"), "source", "000001"): true,
	}
	for _, raw := range links {
		link, ok := raw.(map[string]any)
		linkID, idOK := link["@id"].(string)
		if !ok || !idOK || !wantLinks[linkID] {
			t.Fatalf("attributedSource link = %#v, want both projected source nodes", raw)
		}
		delete(wantLinks, linkID)
	}
	if len(wantLinks) != 0 {
		t.Fatalf("missing attributed source links: %#v", wantLinks)
	}
	for _, rawID := range []string{"Spec", "spec"} {
		source := toolkitNodeByTypeAndProperty(
			t,
			document.Graph,
			"proj:ProvenanceSource",
			"sourceID",
			rawID,
		)
		if source["resource"] == nil {
			t.Fatalf("raw source %q was not preserved: %#v", rawID, source)
		}
	}
	if got := strings.Count(
		firstNTriples.String(),
		toolkitProjectionNamespace+`attributedSource>`,
	); got != 2 {
		t.Fatalf("attributedSource triple count = %d, want 2:\n%s", got, firstNTriples.String())
	}
}

func TestToolkitV02SourceOrdinalsAreStableAcrossEmbeddedNULBoundaryCollisions(t *testing.T) {
	t.Parallel()

	// Arrange.
	load := func(t *testing.T, sources string) *bundle.Bundle {
		t.Helper()
		root := t.TempDir()
		writeGraphFile(t, root, "a.md", "---\n"+
			"type: Note\n"+
			"sources:\n"+sources+
			"---\nBody.\n")
		return loadGraphBundle(t, root)
	}
	first := load(t, ""+
		`  - {id: "a\u0000b", resource: "c"}`+"\n"+
		`  - {id: "a", resource: "b\u0000c"}`+"\n"+
		`  - {id: "z", resource: "r", title: "m\u0000n", author: "o"}`+"\n"+
		`  - {id: "z", resource: "r", title: "m", author: "n\u0000o"}`+"\n")
	shuffled := load(t, ""+
		`  - {id: "z", resource: "r", title: "m", author: "n\u0000o"}`+"\n"+
		`  - {id: "z", resource: "r", title: "m\u0000n", author: "o"}`+"\n"+
		`  - {id: "a", resource: "b\u0000c"}`+"\n"+
		`  - {id: "a\u0000b", resource: "c"}`+"\n")
	options := Options{Profile: ProjectionProfileToolkitV02}
	var firstJSONLD, shuffledJSONLD, firstNTriples, shuffledNTriples strings.Builder

	// Act.
	firstJSONErr := RenderJSONLDWithOptions(&firstJSONLD, first, options)
	shuffledJSONErr := RenderJSONLDWithOptions(&shuffledJSONLD, shuffled, options)
	firstRDFErr := RenderNTriplesWithOptions(&firstNTriples, first, options)
	shuffledRDFErr := RenderNTriplesWithOptions(&shuffledNTriples, shuffled, options)
	jsonTriples, jsonSemanticErr := standardsJSONLDTriples(firstJSONLD.String())
	rdfTriples, rdfSemanticErr := semanticNTriples(firstNTriples.String())

	// Assert.
	if firstJSONErr != nil || shuffledJSONErr != nil || firstRDFErr != nil ||
		shuffledRDFErr != nil || jsonSemanticErr != nil || rdfSemanticErr != nil {
		t.Fatalf("render/semantic errors = (%v, %v, %v, %v, %v, %v)",
			firstJSONErr, shuffledJSONErr, firstRDFErr, shuffledRDFErr,
			jsonSemanticErr, rdfSemanticErr)
	}
	if firstJSONLD.String() != shuffledJSONLD.String() {
		t.Fatalf("JSON-LD depends on colliding source input order:\nfirst:\n%s\nshuffled:\n%s",
			firstJSONLD.String(), shuffledJSONLD.String())
	}
	if firstNTriples.String() != shuffledNTriples.String() {
		t.Fatalf("N-Triples depends on colliding source input order:\nfirst:\n%s\nshuffled:\n%s",
			firstNTriples.String(), shuffledNTriples.String())
	}
	if strings.Join(jsonTriples, "\n") != strings.Join(rdfTriples, "\n") {
		t.Fatalf("semantic parity mismatch:\nJSON-LD:\n%s\nN-Triples:\n%s",
			strings.Join(jsonTriples, "\n"), strings.Join(rdfTriples, "\n"))
	}
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(firstJSONLD.String()), &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	wantSources := []struct {
		id       string
		resource string
		title    string
		author   string
	}{
		{id: "a", resource: "b\x00c"},
		{id: "a\x00b", resource: "c"},
		{id: "z", resource: "r", title: "m", author: "n\x00o"},
		{id: "z", resource: "r", title: "m\x00n", author: "o"},
	}
	for ordinal, want := range wantSources {
		source := toolkitNodeByID(
			t,
			document.Graph,
			toolkitChildIRI(mustConceptID(t, "a"), "source", ordinalString(ordinal)),
		)
		if source["sourceID"] != want.id || source["resource"] != want.resource {
			t.Fatalf("source ordinal %d = %#v, want id=%q resource=%q",
				ordinal, source, want.id, want.resource)
		}
		if want.title != "" && source["title"] != want.title {
			t.Fatalf("source ordinal %d title = %#v, want %q", ordinal, source["title"], want.title)
		}
		if want.author != "" && source["author"] != want.author {
			t.Fatalf("source ordinal %d author = %#v, want %q", ordinal, source["author"], want.author)
		}
	}
}

func TestToolkitV02NormalizationIsDeterministic(t *testing.T) {
	t.Parallel()

	// Arrange.
	firstBundle := fullV02GraphBundle(t, false)
	shuffledBundle := fullV02GraphBundle(t, true)
	options := Options{Profile: ProjectionProfileToolkitV02}

	// Act.
	var firstJSONLD, shuffledJSONLD, firstNTriples, shuffledNTriples strings.Builder
	errs := []error{
		RenderJSONLDWithOptions(&firstJSONLD, firstBundle, options),
		RenderJSONLDWithOptions(&shuffledJSONLD, shuffledBundle, options),
		RenderNTriplesWithOptions(&firstNTriples, firstBundle, options),
		RenderNTriplesWithOptions(&shuffledNTriples, shuffledBundle, options),
	}

	// Assert.
	for _, err := range errs {
		if err != nil {
			t.Fatalf("render error = %v", err)
		}
	}
	if firstJSONLD.String() != shuffledJSONLD.String() {
		t.Fatalf("JSON-LD differs after semantic list shuffling:\nfirst:\n%s\nshuffled:\n%s",
			firstJSONLD.String(), shuffledJSONLD.String())
	}
	if firstNTriples.String() != shuffledNTriples.String() {
		t.Fatalf("N-Triples differs after semantic list shuffling:\nfirst:\n%s\nshuffled:\n%s",
			firstNTriples.String(), shuffledNTriples.String())
	}
}

func TestToolkitV02BareAndListVerificationProjectIdentically(t *testing.T) {
	t.Parallel()

	// Arrange.
	load := func(t *testing.T, verified string) *bundle.Bundle {
		t.Helper()
		root := t.TempDir()
		writeGraphFile(t, root, "a.md", "---\ntype: Note\nverified: "+verified+"\n---\nBody.\n")
		return loadGraphBundle(t, root)
	}
	bare := load(t, "{by: process:nightly, at: 2026-06-26T02:00:00Z}")
	list := load(t, "[{by: process:nightly, at: 2026-06-26T02:00:00Z}]")
	options := Options{Profile: ProjectionProfileToolkitV02}

	// Act.
	var bareOut, listOut strings.Builder
	bareErr := RenderNTriplesWithOptions(&bareOut, bare, options)
	listErr := RenderNTriplesWithOptions(&listOut, list, options)

	// Assert.
	if bareErr != nil || listErr != nil {
		t.Fatalf("render errors = (%v, %v)", bareErr, listErr)
	}
	if bareOut.String() != listOut.String() {
		t.Fatalf("bare output =\n%s\nlist output =\n%s", bareOut.String(), listOut.String())
	}
}

func TestToolkitV02DoesNotInventComputationForOtherConceptTypes(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"runtime: extension-runtime\n"+
		"parameters: [{name: extension, type: opaque, required: true}]\n"+
		"---\nBody.\n")
	b := loadGraphBundle(t, root)

	// Act.
	var out strings.Builder
	err := RenderNTriplesWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

	// Assert.
	if err != nil {
		t.Fatalf("RenderNTriplesWithOptions() error = %v", err)
	}
	if strings.Contains(out.String(), "AttestedComputationContract") ||
		strings.Contains(out.String(), "extension-runtime") {
		t.Fatalf("output =\n%s\ninvented computation predicates for Note", out.String())
	}
}

func TestToolkitV02ProjectsTypedComputationModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bundle.ComputationMode
	}{
		{
			name: "inline",
			body: "---\ntype: Attested Computation\nruntime: bigquery\n---\n" +
				"# Computation\n```sql\nSELECT 1;\n```\n",
			want: bundle.ComputationModeInline,
		},
		{
			name: "file",
			body: "---\ntype: Attested Computation\ncomputation: query with spaces.sql\n---\n",
			want: bundle.ComputationModeFile,
		},
		{
			name: "absent",
			body: "---\ntype: Attested Computation\nruntime: dbt\n---\n",
			want: bundle.ComputationModeAbsent,
		},
		{
			name: "ambiguous",
			body: "---\ntype: Attested Computation\ncomputation: query.sql\n---\n" +
				"# Computation\n```\nSELECT 1;\n```\n",
			want: bundle.ComputationModeAmbiguous,
		},
		{
			name: "malformed",
			body: "---\ntype: Attested Computation\ncomputation: 17\n---\n",
			want: bundle.ComputationModeMalformed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeGraphFile(t, root, "a.md", tt.body)
			b := loadGraphBundle(t, root)
			var out strings.Builder

			// Act.
			err := RenderJSONLDWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

			// Assert.
			if err != nil {
				t.Fatalf("RenderJSONLDWithOptions() error = %v", err)
			}
			var document struct {
				Graph []map[string]any `json:"@graph"`
			}
			if err := json.Unmarshal([]byte(out.String()), &document); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			contract := toolkitNodeByID(
				t,
				document.Graph,
				toolkitChildIRI(mustConceptID(t, "a"), "computation-contract", "000000"),
			)
			if contract["computationMode"] != string(tt.want) {
				t.Fatalf("computationMode = %#v, want %q", contract["computationMode"], tt.want)
			}
		})
	}
}

func TestToolkitV02DoesNotInventAssetsForAmbiguousPathValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "fragment only", value: "#fragment"},
		{name: "query only", value: "?query"},
		{name: "malformed URL escape", value: "https://host/%zz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			quotedValue := strconv.Quote(tt.value)
			writeGraphFile(t, root, "docs/a.md", "---\n"+
				"type: Attested Computation\n"+
				"resource: "+quotedValue+"\n"+
				"runtime: custom\n"+
				"computation: "+quotedValue+"\n"+
				"executor: {resource: "+quotedValue+"}\n"+
				"attester: {resource: "+quotedValue+"}\n"+
				"sources: [{id: adversarial, resource: "+quotedValue+"}]\n"+
				"---\nBody.\n")
			b := loadGraphBundle(t, root)
			options := Options{Profile: ProjectionProfileToolkitV02}
			var jsonld, ntriples strings.Builder

			// Act.
			jsonErr := RenderJSONLDWithOptions(&jsonld, b, options)
			ntriplesErr := RenderNTriplesWithOptions(&ntriples, b, options)

			// Assert.
			if jsonErr != nil || ntriplesErr != nil {
				t.Fatalf("render errors = (%v, %v)", jsonErr, ntriplesErr)
			}
			var document struct {
				Graph []map[string]any `json:"@graph"`
			}
			if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			concept := toolkitNodeByID(
				t,
				document.Graph,
				toolkitConceptIRI(mustConceptID(t, "docs/a")),
			)
			source := toolkitNodeByTypeAndProperty(
				t,
				document.Graph,
				"proj:ProvenanceSource",
				"sourceID",
				"adversarial",
			)
			contract := toolkitNodeByType(t, document.Graph, "proj:AttestedComputationContract")
			for _, observation := range []struct {
				name         string
				node         map[string]any
				resource     string
				kind         string
				resourceNode string
			}{
				{
					name:         "concept",
					node:         concept,
					resource:     "resource",
					kind:         "resourceKind",
					resourceNode: "resourceNode",
				},
				{
					name:         "source",
					node:         source,
					resource:     "resource",
					kind:         "resourceKind",
					resourceNode: "resourceNode",
				},
				{
					name:         "computation",
					node:         contract,
					resource:     "computationResource",
					kind:         "computationResourceKind",
					resourceNode: "computationNode",
				},
				{
					name:         "executor",
					node:         contract,
					resource:     "executorResource",
					kind:         "executorResourceKind",
					resourceNode: "executorNode",
				},
				{
					name:         "attester",
					node:         contract,
					resource:     "attesterResource",
					kind:         "attesterResourceKind",
					resourceNode: "attesterNode",
				},
			} {
				if observation.node[observation.resource] != tt.value ||
					observation.node[observation.kind] != string(bundle.PathValueAmbiguous) {
					t.Fatalf("%s path projection = %#v, want raw %q and ambiguous kind",
						observation.name, observation.node, tt.value)
				}
				if _, exists := observation.node[observation.resourceNode]; exists {
					t.Fatalf("%s path projection invented %q: %#v",
						observation.name, observation.resourceNode, observation.node)
				}
			}
			for _, node := range document.Graph {
				if node["@type"] == "proj:ReferencedAsset" {
					t.Fatalf("ambiguous path invented ReferencedAsset: %#v", node)
				}
				if _, exists := node["exists"]; exists {
					t.Fatalf("ambiguous path invented exists flag: %#v", node)
				}
			}
			ntriplesText := ntriples.String()
			if strings.Contains(ntriplesText, toolkitProjectionNamespace+"ReferencedAsset") ||
				strings.Contains(ntriplesText, toolkitProjectionNamespace+"exists>") ||
				strings.Contains(ntriplesText, toolkitSyntheticIRI("asset", "docs")) {
				t.Fatalf("ambiguous path invented graph semantics:\n%s", ntriplesText)
			}
		})
	}
}

func TestToolkitV02ProjectsCapturedLiteralPercentPathsAcrossAllFields(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	const value = "captured%zz.sql"
	const bundlePath = "docs/captured%zz.sql"
	writeGraphFile(t, root, "docs/a.md", "---\n"+
		"type: Attested Computation\n"+
		"resource: "+value+"\n"+
		"runtime: custom\n"+
		"computation: "+value+"\n"+
		"executor: {resource: "+value+"}\n"+
		"attester: {resource: "+value+"}\n"+
		"sources: [{id: captured, resource: "+value+"}]\n"+
		"---\nBody.\n")
	writeGraphFile(t, root, bundlePath, "captured\n")
	b := loadGraphBundle(t, root)
	var out strings.Builder

	// Act.
	err := RenderJSONLDWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

	// Assert.
	if err != nil {
		t.Fatalf("RenderJSONLDWithOptions() error = %v", err)
	}
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(out.String()), &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	concept := toolkitNodeByID(t, document.Graph, toolkitConceptIRI(mustConceptID(t, "docs/a")))
	source := toolkitNodeByTypeAndProperty(
		t,
		document.Graph,
		"proj:ProvenanceSource",
		"sourceID",
		"captured",
	)
	contract := toolkitNodeByType(t, document.Graph, "proj:AttestedComputationContract")
	assetID := toolkitSyntheticIRI("asset", bundlePath)
	for _, observation := range []struct {
		name, resource, kind, resourceNode string
		node                               map[string]any
	}{
		{name: "concept", node: concept, resource: "resource", kind: "resourceKind", resourceNode: "resourceNode"},
		{name: "source", node: source, resource: "resource", kind: "resourceKind", resourceNode: "resourceNode"},
		{name: "computation", node: contract, resource: "computationResource", kind: "computationResourceKind", resourceNode: "computationNode"},
		{name: "executor", node: contract, resource: "executorResource", kind: "executorResourceKind", resourceNode: "executorNode"},
		{name: "attester", node: contract, resource: "attesterResource", kind: "attesterResourceKind", resourceNode: "attesterNode"},
	} {
		if observation.node[observation.resource] != value ||
			observation.node[observation.kind] != string(bundle.PathValueRelative) {
			t.Fatalf("%s path projection = %#v, want captured relative %q", observation.name, observation.node, value)
		}
		reference, ok := observation.node[observation.resourceNode].(map[string]any)
		if !ok || reference["@id"] != assetID {
			t.Fatalf("%s resource node = %#v, want %q", observation.name, observation.node[observation.resourceNode], assetID)
		}
	}
	asset := toolkitNodeByID(t, document.Graph, assetID)
	exists, ok := asset["exists"].(map[string]any)
	if asset["bundlePath"] != bundlePath || !ok || exists["@value"] != true {
		t.Fatalf("captured asset = %#v, want bundlePath=%q exists=true", asset, bundlePath)
	}
}

func TestToolkitV02RejectsPartialMalformedComputationContractObservations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		frontmatter      string
		wantMode         bundle.ComputationMode
		forbidden        []string
		forbidParameters bool
		omitInline       bool
	}{
		{
			name:        "runtime",
			frontmatter: "runtime: 7\n",
			wantMode:    bundle.ComputationModeInline,
			forbidden:   []string{"runtime"},
		},
		{
			name:        "computation resource",
			frontmatter: "computation: 7\n",
			wantMode:    bundle.ComputationModeMalformed,
			forbidden:   []string{"computationResource", "computationResourceKind", "computationNode"},
			omitInline:  true,
		},
		{
			name:             "parameters shape",
			frontmatter:      "parameters: {name: year, type: integer, required: true}\n",
			wantMode:         bundle.ComputationModeInline,
			forbidParameters: true,
		},
		{
			name:             "parameter name",
			frontmatter:      "parameters: [{name: 7, type: integer, required: true}]\n",
			wantMode:         bundle.ComputationModeInline,
			forbidParameters: true,
		},
		{
			name:             "parameter type",
			frontmatter:      "parameters: [{name: year, type: 7, required: true}]\n",
			wantMode:         bundle.ComputationModeInline,
			forbidParameters: true,
		},
		{
			name:             "parameter required absent",
			frontmatter:      "parameters: [{name: year, type: integer}]\n",
			wantMode:         bundle.ComputationModeInline,
			forbidParameters: true,
		},
		{
			name:             "parameter required wrong tag",
			frontmatter:      "parameters: [{name: year, type: integer, required: \"false\"}]\n",
			wantMode:         bundle.ComputationModeInline,
			forbidParameters: true,
		},
		{
			name:        "executor shape",
			frontmatter: "executor: runner.md\n",
			wantMode:    bundle.ComputationModeInline,
			forbidden:   []string{"executorResource", "executorResourceKind", "executorNode", "receiptField"},
		},
		{
			name:        "executor resource",
			frontmatter: "executor: {resource: 7, receipt: [result]}\n",
			wantMode:    bundle.ComputationModeInline,
			forbidden:   []string{"executorResource", "executorResourceKind", "executorNode", "receiptField"},
		},
		{
			name:        "executor receipt shape",
			frontmatter: "executor: {resource: runner.md, receipt: result}\n",
			wantMode:    bundle.ComputationModeInline,
			forbidden:   []string{"executorResource", "executorResourceKind", "executorNode", "receiptField"},
		},
		{
			name:        "executor receipt item",
			frontmatter: "executor: {resource: runner.md, receipt: [result, 7]}\n",
			wantMode:    bundle.ComputationModeInline,
			forbidden:   []string{"executorResource", "executorResourceKind", "executorNode", "receiptField"},
		},
		{
			name:        "attester shape",
			frontmatter: "attester: attester.py\n",
			wantMode:    bundle.ComputationModeInline,
			forbidden:   []string{"attesterResource", "attesterResourceKind", "attesterNode"},
		},
		{
			name:        "attester resource",
			frontmatter: "attester: {resource: 7}\n",
			wantMode:    bundle.ComputationModeInline,
			forbidden:   []string{"attesterResource", "attesterResourceKind", "attesterNode"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			body := "# Computation\n```sql\nSELECT 1;\n```\n"
			if tt.omitInline {
				body = "Body.\n"
			}
			writeGraphFile(t, root, "a.md", "---\ntype: Attested Computation\n"+
				tt.frontmatter+"---\n"+body)
			b := loadGraphBundle(t, root)
			options := Options{Profile: ProjectionProfileToolkitV02}
			var jsonld, ntriples strings.Builder

			// Act.
			jsonErr := RenderJSONLDWithOptions(&jsonld, b, options)
			rdfErr := RenderNTriplesWithOptions(&ntriples, b, options)

			// Assert.
			if jsonErr != nil || rdfErr != nil {
				t.Fatalf("render errors = (%v, %v)", jsonErr, rdfErr)
			}
			var document struct {
				Graph []map[string]any `json:"@graph"`
			}
			if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			contract := toolkitNodeByType(t, document.Graph, "proj:AttestedComputationContract")
			if contract["computationMode"] != string(tt.wantMode) {
				t.Fatalf("contract = %#v, want computationMode %q", contract, tt.wantMode)
			}
			for _, predicate := range tt.forbidden {
				if _, exists := contract[predicate]; exists {
					t.Fatalf("partial malformed predicate %q projected: %#v", predicate, contract)
				}
				if strings.Contains(ntriples.String(), toolkitProjectionNamespace+predicate+">") {
					t.Fatalf("partial malformed predicate %q projected in N-Triples:\n%s",
						predicate, ntriples.String())
				}
			}
			if tt.forbidParameters {
				for _, node := range document.Graph {
					if node["@type"] == "proj:ComputationParameter" {
						t.Fatalf("partial malformed parameter projected: %#v", node)
					}
				}
				if strings.Contains(ntriples.String(), toolkitProjectionNamespace+"ComputationParameter>") {
					t.Fatalf("partial malformed parameter projected in N-Triples:\n%s", ntriples.String())
				}
			}
		})
	}
}

func TestToolkitV02PreservesExplicitFalseComputationParameter(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\n"+
		"type: Attested Computation\n"+
		"parameters: [{name: enabled, type: boolean, required: false}]\n"+
		"---\n# Computation\n```\nvalue\n```\n")
	b := loadGraphBundle(t, root)
	var out strings.Builder

	// Act.
	err := RenderJSONLDWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

	// Assert.
	if err != nil {
		t.Fatalf("RenderJSONLDWithOptions() error = %v", err)
	}
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(out.String()), &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	parameter := toolkitNodeByType(t, document.Graph, "proj:ComputationParameter")
	if parameter["name"] != "enabled" || parameter["parameterType"] != "boolean" ||
		!typedJSONLDValueEquals(parameter["required"], false, "xsd:boolean") {
		t.Fatalf("parameter = %#v, want explicit required=false", parameter)
	}
}

func TestToolkitV02UsesBundleOwnedComputationInspectionTaxonomy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		frontmatter string
		body        string
		want        bundle.ComputationMode
	}{
		{
			name: "duplicate section",
			body: "# Computation\n```\none\n```\n" +
				"# Computation\nExplanation only.\n",
			want: bundle.ComputationModeAmbiguous,
		},
		{
			name: "nested fence",
			body: "# Computation\n> ```sql\n> SELECT 1;\n> ```\n",
			want: bundle.ComputationModeMalformed,
		},
		{
			name:        "file and nested fence",
			frontmatter: "computation: query.sql\n",
			body:        "# Computation\n> ```sql\n> SELECT 1;\n> ```\n",
			want:        bundle.ComputationModeMalformed,
		},
		{
			name: "direct and nested fence",
			body: "# Computation\n```\ndirect\n```\n" +
				"> ```\n> nested\n> ```\n",
			want: bundle.ComputationModeAmbiguous,
		},
		{
			name: "unclosed direct fence",
			body: "# Computation\n```sql\nSELECT 1;\n",
			want: bundle.ComputationModeMalformed,
		},
		{
			name:        "file and unclosed fence",
			frontmatter: "computation: query.sql\n",
			body:        "# Computation\n```sql\nSELECT 1;\n",
			want:        bundle.ComputationModeMalformed,
		},
		{
			name: "multiple direct fences",
			body: "# Computation\n```\none\n```\n```\ntwo\n```\n",
			want: bundle.ComputationModeAmbiguous,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeGraphFile(t, root, "a.md", "---\ntype: Attested Computation\n"+
				tt.frontmatter+"---\n"+tt.body)
			b := loadGraphBundle(t, root)
			options := Options{Profile: ProjectionProfileToolkitV02}
			var jsonld, ntriples strings.Builder

			// Act.
			jsonErr := RenderJSONLDWithOptions(&jsonld, b, options)
			rdfErr := RenderNTriplesWithOptions(&ntriples, b, options)

			// Assert.
			if jsonErr != nil || rdfErr != nil {
				t.Fatalf("render errors = (%v, %v)", jsonErr, rdfErr)
			}
			var document struct {
				Graph []map[string]any `json:"@graph"`
			}
			if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			contract := toolkitNodeByType(t, document.Graph, "proj:AttestedComputationContract")
			if contract["computationMode"] != string(tt.want) {
				t.Fatalf("contract = %#v, want computationMode %q", contract, tt.want)
			}
			for _, predicate := range []string{"computationResource", "computationResourceKind", "computationNode"} {
				if _, exists := contract[predicate]; exists {
					t.Fatalf("conflicting computation projected %q: %#v", predicate, contract)
				}
				if strings.Contains(ntriples.String(), toolkitProjectionNamespace+predicate+">") {
					t.Fatalf("conflicting computation projected %q in N-Triples:\n%s",
						predicate, ntriples.String())
				}
			}
		})
	}
}

func TestToolkitV02MarksOnlyActualLegacyGenerationFallback(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "fallback.md", "---\ntype: Note\ntimestamp: 2026-05-28T22:53:05Z\n---\nBody.\n")
	writeGraphFile(t, root, "partial.md", "---\ntype: Note\ntimestamp: 2026-05-28T22:53:05Z\ngenerated: {by: process:new}\n---\nBody.\n")
	b := loadGraphBundle(t, root)

	// Act.
	var out strings.Builder
	err := RenderNTriplesWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

	// Assert.
	if err != nil {
		t.Fatalf("RenderNTriplesWithOptions() error = %v", err)
	}
	text := out.String()
	if strings.Count(text, toolkitProjectionNamespace+`legacyFallback> "true"`) != 1 {
		t.Fatalf("output =\n%s\nwant exactly one legacy fallback marker", text)
	}
}

func TestToolkitV02ProjectsLegacyCitationsOnlyWhenSourcesAbsent(t *testing.T) {
	t.Parallel()

	// Arrange.
	load := func(t *testing.T, frontmatter string) *bundle.Bundle {
		t.Helper()
		root := t.TempDir()
		writeGraphFile(t, root, "a.md", "---\ntype: Note\n"+frontmatter+"---\n"+
			"# Citations\n- https://legacy.example/source\n")
		return loadGraphBundle(t, root)
	}
	legacy := load(t, "")
	mixed := load(t, "sources: [{id: current, resource: https://current.example/source}]\n")
	options := Options{Profile: ProjectionProfileToolkitV02}

	// Act.
	var legacyOut, mixedOut strings.Builder
	legacyErr := RenderNTriplesWithOptions(&legacyOut, legacy, options)
	mixedErr := RenderNTriplesWithOptions(&mixedOut, mixed, options)

	// Assert.
	if legacyErr != nil || mixedErr != nil {
		t.Fatalf("render errors = (%v, %v)", legacyErr, mixedErr)
	}
	if !strings.Contains(legacyOut.String(), `"https://legacy.example/source"`) ||
		!strings.Contains(legacyOut.String(), toolkitProjectionNamespace+`legacyFallback> "true"`) ||
		!strings.Contains(legacyOut.String(), toolkitProjectionNamespace+`legacyCitationNumber> "1"`) {
		t.Fatalf("legacy output =\n%s\nwant marked Citations fallback source", legacyOut.String())
	}
	if strings.Contains(mixedOut.String(), "legacy.example") ||
		strings.Contains(mixedOut.String(), toolkitProjectionNamespace+"legacyCitationNumber") {
		t.Fatalf("mixed output =\n%s\nmust suppress legacy Citations when sources exists", mixedOut.String())
	}
}

func TestToolkitV02LegacyFallbackDependsOnlyOnSemanticFamilyAbsence(t *testing.T) {
	t.Parallel()

	versions := []struct {
		name        string
		declaration string
		selector    string
		wantSource  bundle.VersionSource
	}{
		{name: "default", wantSource: bundle.VersionSourceDefault},
		{
			name:        "declared v0.1",
			declaration: "okf_version: \"0.1\"\n",
			wantSource:  bundle.VersionSourceDeclared,
		},
		{
			name:        "declared v0.2",
			declaration: "okf_version: \"0.2\"\n",
			wantSource:  bundle.VersionSourceDeclared,
		},
		{
			name:       "explicit v0.1",
			selector:   "0.1",
			wantSource: bundle.VersionSourceExplicit,
		},
		{
			name:       "explicit v0.2",
			selector:   "0.2",
			wantSource: bundle.VersionSourceExplicit,
		},
		{
			name:        "future",
			declaration: "okf_version: \"9.0\"\n",
			wantSource:  bundle.VersionSourceFutureBestEffort,
		},
	}
	generatedStates := []struct {
		name               string
		frontmatter        string
		wantGeneration     bool
		wantLegacyFallback bool
		wantAt             string
		wantActor          string
	}{
		{
			name:               "generated absent",
			wantGeneration:     true,
			wantLegacyFallback: true,
			wantAt:             "2026-05-28T22:53:05Z",
		},
		{
			name:        "generated malformed",
			frontmatter: "generated: wrong\n",
		},
		{
			name:           "generated at without actor",
			frontmatter:    "generated: {at: 2026-07-29T00:00:00Z}\n",
			wantGeneration: true,
			wantAt:         "2026-07-29T00:00:00Z",
		},
		{
			name:           "generated at with malformed actor",
			frontmatter:    "generated: {by: 17, at: 2026-07-29T00:00:00Z}\n",
			wantGeneration: true,
			wantAt:         "2026-07-29T00:00:00Z",
		},
		{
			name:           "generated actor with malformed at",
			frontmatter:    "generated: {by: process:new, at: yesterday}\n",
			wantGeneration: true,
			wantActor:      "process:new",
		},
		{
			name:           "generated present",
			frontmatter:    "generated: {by: process:new, at: 2026-07-29T00:00:00Z}\n",
			wantGeneration: true,
			wantAt:         "2026-07-29T00:00:00Z",
			wantActor:      "process:new",
		},
	}
	sourceStates := []struct {
		name             string
		frontmatter      string
		citationsAllowed bool
	}{
		{name: "sources absent", citationsAllowed: true},
		{name: "sources malformed", frontmatter: "sources: wrong\n"},
		{
			name:        "sources present",
			frontmatter: "sources: [{id: current, resource: https://current.example/source}]\n",
		},
	}
	citationBodies := []struct {
		name     string
		body     string
		hasEntry bool
	}{
		{
			name:     "actual Citations",
			body:     "# Citations\n[1] https://legacy.example/source\n",
			hasEntry: true,
		},
		{
			name: "numeric only",
			body: "# Citations\n[1]\n",
		},
	}

	for _, version := range versions {
		for _, generated := range generatedStates {
			for _, sources := range sourceStates {
				for _, citations := range citationBodies {
					name := version.name + "/" + generated.name + "/" + sources.name + "/" + citations.name
					t.Run(name, func(t *testing.T) {
						// Arrange.
						root := t.TempDir()
						writeGraphFile(t, root, "index.md", "---\n"+
							version.declaration+"---\n# Index\n")
						writeGraphFile(t, root, "a.md", "---\n"+
							"type: Note\n"+
							"timestamp: 2026-05-28T22:53:05Z\n"+
							generated.frontmatter+
							sources.frontmatter+
							"---\n"+citations.body)
						b := loadGraphBundle(t, root)
						options := Options{
							Profile:         ProjectionProfileToolkitV02,
							VersionSelector: version.selector,
						}
						var jsonld, ntriples strings.Builder

						// Act.
						jsonErr := RenderJSONLDWithOptions(&jsonld, b, options)
						ntriplesErr := RenderNTriplesWithOptions(&ntriples, b, options)

						// Assert.
						if jsonErr != nil || ntriplesErr != nil {
							t.Fatalf("render errors = (%v, %v)", jsonErr, ntriplesErr)
						}
						var document struct {
							Graph []map[string]any `json:"@graph"`
						}
						if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
							t.Fatalf("json.Unmarshal() error = %v", err)
						}
						projection := toolkitNodeByID(
							t,
							document.Graph,
							toolkitSyntheticIRI("projection"),
						)
						if projection["versionSource"] != string(version.wantSource) {
							t.Fatalf("versionSource = %#v, want %q",
								projection["versionSource"], version.wantSource)
						}
						generationID := toolkitChildIRI(
							mustConceptID(t, "a"),
							"generation",
							"000000",
						)
						var generation map[string]any
						var legacySources []map[string]any
						for _, node := range document.Graph {
							if node["@id"] == generationID {
								generation = node
							}
							if node["@type"] == "proj:ProvenanceSource" &&
								typedJSONLDValueEquals(node["legacyFallback"], true, "xsd:boolean") {
								legacySources = append(legacySources, node)
							}
						}
						if generated.wantGeneration != (generation != nil) {
							t.Fatalf("generation = %#v, want present=%v",
								generation, generated.wantGeneration)
						}
						if generation != nil {
							if generated.wantAt != "" &&
								!typedJSONLDValueEquals(generation["at"], generated.wantAt, "xsd:dateTime") {
								t.Fatalf("generation at = %#v, want %q", generation["at"], generated.wantAt)
							}
							if generated.wantAt == "" {
								if _, exists := generation["at"]; exists {
									t.Fatalf("generation at = %#v, want absent", generation["at"])
								}
							}
							if generated.wantActor != "" && generation["actor"] != generated.wantActor {
								t.Fatalf("generation actor = %#v, want %q",
									generation["actor"], generated.wantActor)
							}
							if generated.wantActor == "" {
								if _, exists := generation["actor"]; exists {
									t.Fatalf("generation actor = %#v, want absent", generation["actor"])
								}
							}
							gotFallback := typedJSONLDValueEquals(
								generation["legacyFallback"],
								true,
								"xsd:boolean",
							)
							if gotFallback != generated.wantLegacyFallback {
								t.Fatalf("generation legacyFallback = %#v, want %v",
									generation["legacyFallback"], generated.wantLegacyFallback)
							}
						}
						wantLegacyCitation := sources.citationsAllowed && citations.hasEntry
						if (len(legacySources) == 1) != wantLegacyCitation {
							t.Fatalf("legacy citation nodes = %#v, want present=%v",
								legacySources, wantLegacyCitation)
						}
						if len(legacySources) > 1 {
							t.Fatalf("legacy citation node count = %d, want at most one", len(legacySources))
						}
						if wantLegacyCitation &&
							legacySources[0]["resource"] != "https://legacy.example/source" {
							t.Fatalf("legacy citation = %#v, want actual Citations resource", legacySources[0])
						}
						if !generated.wantLegacyFallback &&
							strings.Contains(ntriples.String(), `"2026-05-28T22:53:05Z"`) {
							t.Fatalf("legacy timestamp leaked into N-Triples:\n%s", ntriples.String())
						}
						if !wantLegacyCitation &&
							strings.Contains(ntriples.String(), "legacy.example/source") {
							t.Fatalf("legacy citation leaked into N-Triples:\n%s", ntriples.String())
						}
					})
				}
			}
		}
	}
}

func TestToolkitV02ProjectsOnlyCompleteValidSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "id", source: "{id: 7, resource: https://example.test/source}"},
		{name: "resource", source: "{id: source, resource: 7}"},
		{name: "title", source: "{resource: https://example.test/source, title: 7}"},
		{name: "author", source: "{resource: https://example.test/source, author: 7}"},
		{name: "usage count", source: "{resource: https://example.test/source, usage_count: -1}"},
		{name: "last modified", source: "{resource: https://example.test/source, last_modified: yesterday}"},
		{name: "usage window shape", source: "{resource: https://example.test/source, usage_window: nope}"},
		{name: "usage window from", source: "{resource: https://example.test/source, usage_window: {from: yesterday, to: 2026-06-30}}"},
		{name: "usage window to", source: "{resource: https://example.test/source, usage_window: {from: 2026-06-01, to: yesterday}}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeGraphFile(t, root, "a.md", "---\ntype: Note\nsources:\n  - "+tt.source+"\n---\nBody.\n")
			b := loadGraphBundle(t, root)
			options := Options{Profile: ProjectionProfileToolkitV02}
			var jsonld, ntriples strings.Builder

			// Act.
			jsonErr := RenderJSONLDWithOptions(&jsonld, b, options)
			rdfErr := RenderNTriplesWithOptions(&ntriples, b, options)

			// Assert.
			if jsonErr != nil || rdfErr != nil {
				t.Fatalf("render errors = (%v, %v)", jsonErr, rdfErr)
			}
			var document struct {
				Graph []map[string]any `json:"@graph"`
			}
			if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			for _, node := range document.Graph {
				if node["@type"] == "proj:ProvenanceSource" {
					t.Fatalf("partial malformed source projected: %#v", node)
				}
			}
			if strings.Contains(ntriples.String(), toolkitProjectionNamespace+"ProvenanceSource>") {
				t.Fatalf("partial malformed source projected in N-Triples:\n%s", ntriples.String())
			}
		})
	}
}

func TestToolkitV02ProjectsOnlyCompleteValidUsageWindows(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"usage_window: {from: yesterday, to: 2026-06-30}\n"+
		"sources:\n"+
		"  - {id: source, resource: https://example.test/source}\n"+
		"---\nBody.\n")
	b := loadGraphBundle(t, root)
	options := Options{Profile: ProjectionProfileToolkitV02}
	var jsonld, ntriples strings.Builder

	// Act.
	jsonErr := RenderJSONLDWithOptions(&jsonld, b, options)
	rdfErr := RenderNTriplesWithOptions(&ntriples, b, options)

	// Assert.
	if jsonErr != nil || rdfErr != nil {
		t.Fatalf("render errors = (%v, %v)", jsonErr, rdfErr)
	}
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	source := toolkitNodeByType(t, document.Graph, "proj:ProvenanceSource")
	if source["resource"] != "https://example.test/source" {
		t.Fatalf("valid source omitted: %#v", source)
	}
	for _, predicate := range []string{"usageFrom", "usageTo", "sourceUsageWindowOverride"} {
		if _, exists := source[predicate]; exists {
			t.Fatalf("source = %#v, partial usage window predicate %q projected", source, predicate)
		}
		if strings.Contains(ntriples.String(), toolkitProjectionNamespace+predicate+">") {
			t.Fatalf("partial usage window projected in N-Triples:\n%s", ntriples.String())
		}
	}
}

func TestToolkitV02VersionSelectorIsAnAssertion(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n")
	writeGraphFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
	b := loadGraphBundle(t, root)

	// Act.
	err := RenderJSONLDWithOptions(io.Discard, b, Options{
		Profile:         ProjectionProfileToolkitV02,
		VersionSelector: "0.1",
	})

	// Assert.
	if !errors.Is(err, bundle.ErrVersionConflict) {
		t.Fatalf("RenderJSONLDWithOptions() error = %v, want ErrVersionConflict", err)
	}
}

func TestAllGraphRenderersEnforceBundleVersionResolution(t *testing.T) {
	t.Parallel()

	type renderer struct {
		name            string
		supportsOptions bool
		render          func(io.Writer, *bundle.Bundle, Options) error
	}
	renderers := []renderer{
		{name: "wrapper/text", render: func(w io.Writer, b *bundle.Bundle, _ Options) error { return RenderText(w, b) }},
		{name: "wrapper/dot", render: func(w io.Writer, b *bundle.Bundle, _ Options) error { return RenderDOT(w, b) }},
		{name: "wrapper/mermaid", render: func(w io.Writer, b *bundle.Bundle, _ Options) error { return RenderMermaid(w, b) }},
		{name: "wrapper/jsonld", render: func(w io.Writer, b *bundle.Bundle, _ Options) error { return RenderJSONLD(w, b) }},
		{name: "wrapper/ntriples", render: func(w io.Writer, b *bundle.Bundle, _ Options) error { return RenderNTriples(w, b) }},
		{name: "legacy/text", supportsOptions: true, render: RenderTextWithOptions},
		{name: "legacy/dot", supportsOptions: true, render: RenderDOTWithOptions},
		{name: "legacy/mermaid", supportsOptions: true, render: RenderMermaidWithOptions},
		{name: "legacy/jsonld", supportsOptions: true, render: RenderJSONLDWithOptions},
		{name: "legacy/ntriples", supportsOptions: true, render: RenderNTriplesWithOptions},
		{
			name:            "toolkit/text",
			supportsOptions: true,
			render: func(w io.Writer, b *bundle.Bundle, options Options) error {
				options.Profile = ProjectionProfileToolkitV02
				return RenderTextWithOptions(w, b, options)
			},
		},
		{
			name:            "toolkit/dot",
			supportsOptions: true,
			render: func(w io.Writer, b *bundle.Bundle, options Options) error {
				options.Profile = ProjectionProfileToolkitV02
				return RenderDOTWithOptions(w, b, options)
			},
		},
		{
			name:            "toolkit/mermaid",
			supportsOptions: true,
			render: func(w io.Writer, b *bundle.Bundle, options Options) error {
				options.Profile = ProjectionProfileToolkitV02
				return RenderMermaidWithOptions(w, b, options)
			},
		},
		{
			name:            "toolkit/jsonld",
			supportsOptions: true,
			render: func(w io.Writer, b *bundle.Bundle, options Options) error {
				options.Profile = ProjectionProfileToolkitV02
				return RenderJSONLDWithOptions(w, b, options)
			},
		},
		{
			name:            "toolkit/ntriples",
			supportsOptions: true,
			render: func(w io.Writer, b *bundle.Bundle, options Options) error {
				options.Profile = ProjectionProfileToolkitV02
				return RenderNTriplesWithOptions(w, b, options)
			},
		},
	}
	load := func(t *testing.T, index string) *bundle.Bundle {
		t.Helper()
		root := t.TempDir()
		if index != "" {
			writeGraphFile(t, root, "index.md", index)
		}
		writeGraphFile(t, root, "a.md", "---\ntype: Note\n---\nSee [B](b.md).\n")
		writeGraphFile(t, root, "b.md", "---\ntype: Note\n---\nBody.\n")
		return loadGraphBundle(t, root)
	}

	malformed := []struct {
		name  string
		value string
	}{
		{name: "scalar", value: "17"},
		{name: "list", value: "[\"0.2\"]"},
		{name: "map", value: "{value: \"0.2\"}"},
		{name: "blank", value: `""`},
	}
	for _, declaration := range malformed {
		for _, renderer := range renderers {
			t.Run("malformed/"+declaration.name+"/"+renderer.name, func(t *testing.T) {
				// Arrange.
				b := load(t, "---\nokf_version: "+declaration.value+"\n---\n# Index\n")

				// Act.
				err := renderer.render(io.Discard, b, Options{})

				// Assert.
				if !errors.Is(err, bundle.ErrInvalidVersionDeclaration) {
					t.Fatalf("render error = %v, want ErrInvalidVersionDeclaration", err)
				}
			})
		}
	}

	valid := []struct {
		name  string
		index string
	}{
		{name: "absent index"},
		{name: "default", index: "---\ntitle: Root\n---\n# Index\n"},
		{name: "v0.1", index: "---\nokf_version: \"0.1\"\n---\n# Index\n"},
		{name: "v0.2", index: "---\nokf_version: \"0.2\"\n---\n# Index\n"},
		{name: "future best effort", index: "---\nokf_version: \"9.9\"\n---\n# Index\n"},
	}
	for _, declaration := range valid {
		for _, renderer := range renderers {
			t.Run("valid/"+declaration.name+"/"+renderer.name, func(t *testing.T) {
				// Arrange.
				b := load(t, declaration.index)

				// Act.
				err := renderer.render(io.Discard, b, Options{})

				// Assert.
				if err != nil {
					t.Fatalf("render error = %v", err)
				}
			})
		}
	}

	for _, renderer := range renderers {
		if !renderer.supportsOptions {
			continue
		}
		t.Run("conflict/"+renderer.name, func(t *testing.T) {
			// Arrange.
			b := load(t, "---\nokf_version: \"0.2\"\n---\n# Index\n")

			// Act.
			err := renderer.render(io.Discard, b, Options{VersionSelector: "0.1"})

			// Assert.
			if !errors.Is(err, bundle.ErrVersionConflict) {
				t.Fatalf("render error = %v, want ErrVersionConflict", err)
			}
		})
	}
}

func TestToolkitV02ProjectsAllTrustTiers(t *testing.T) {
	t.Parallel()

	// Arrange.
	tests := []struct {
		name     string
		verified string
		want     string
	}{
		{name: "unverified", want: "unverified"},
		{name: "machine", verified: "\nverified: {by: process:nightly, at: 2026-01-01T00:00:00Z}", want: "machine-confirmed"},
		{name: "human", verified: "\nverified: {by: human:reviewer, at: 2026-01-01T00:00:00Z}", want: "human-reviewed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeGraphFile(t, root, "a.md", "---\ntype: Note"+tt.verified+"\n---\nBody.\n")
			b := loadGraphBundle(t, root)

			// Act.
			var out strings.Builder
			err := RenderJSONLDWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

			// Assert.
			if err != nil {
				t.Fatalf("RenderJSONLDWithOptions() error = %v", err)
			}
			var document struct {
				Graph []map[string]any `json:"@graph"`
			}
			if err := json.Unmarshal([]byte(out.String()), &document); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			concept := toolkitNodeByID(t, document.Graph, "local:bundle:a")
			if concept["trustTier"] != tt.want {
				t.Fatalf("trustTier = %v, want %q", concept["trustTier"], tt.want)
			}
		})
	}
}

func TestToolkitV02MissingStatusDefaultsToStable(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
	b := loadGraphBundle(t, root)
	options := Options{Profile: ProjectionProfileToolkitV02}
	var jsonld, ntriples strings.Builder

	// Act.
	jsonErr := RenderJSONLDWithOptions(&jsonld, b, options)
	rdfErr := RenderNTriplesWithOptions(&ntriples, b, options)

	// Assert.
	if jsonErr != nil || rdfErr != nil {
		t.Fatalf("render errors = (%v, %v)", jsonErr, rdfErr)
	}
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	concept := toolkitNodeByID(t, document.Graph, toolkitConceptIRI(mustConceptID(t, "a")))
	if concept["effectiveStatus"] != bundle.StatusStable {
		t.Fatalf("effectiveStatus = %#v, want %q", concept["effectiveStatus"], bundle.StatusStable)
	}
	if _, exists := concept["declaredStatus"]; exists {
		t.Fatalf("missing status projected declaredStatus: %#v", concept)
	}
	wantTriple := `<local:bundle:a> <` + toolkitProjectionNamespace +
		`effectiveStatus> "stable" .`
	if !strings.Contains(ntriples.String(), wantTriple) {
		t.Fatalf("N-Triples =\n%s\nwant %q", ntriples.String(), wantTriple)
	}
	if strings.Contains(ntriples.String(), toolkitProjectionNamespace+"declaredStatus>") {
		t.Fatalf("N-Triples projected declaredStatus for missing status:\n%s", ntriples.String())
	}
}

func TestToolkitV02DuplicateStandardKeysFailClosed(t *testing.T) {
	t.Parallel()

	t.Run("concept families and type", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		writeGraphFile(t, root, "a.md", `---
type: Attested Computation
generated: {by: process:first, at: 2026-01-01T00:00:00Z}
generated: {by: human:second, at: 2026-02-02T00:00:00Z}
timestamp: 2025-12-31T00:00:00Z
verified: {by: human:first, at: 2026-01-01T00:00:00Z}
verified: {by: process:second, at: 2026-02-02T00:00:00Z}
status: draft
status: deprecated
stale_after: 2026-01-01
stale_after: 2026-12-31
sources: [{id: leaked-first, resource: first.txt}]
sources: [{id: leaked-second, resource: second.txt}]
runtime: first-runtime
runtime: second-runtime
computation: first.sql
computation: second.sql
---
# Citations
- https://legacy.example/source
`)
		writeGraphFile(t, root, "ambiguous-type.md", `---
type: Attested Computation
type: Note
runtime: leaked-runtime
computation: leaked.sql
---
Body.
`)
		b := loadGraphBundle(t, root)
		asOf := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
		options := Options{Profile: ProjectionProfileToolkitV02, AsOf: &asOf}
		var jsonld, ntriples strings.Builder

		// Act.
		jsonErr := RenderJSONLDWithOptions(&jsonld, b, options)
		rdfErr := RenderNTriplesWithOptions(&ntriples, b, options)

		// Assert.
		if jsonErr != nil || rdfErr != nil {
			t.Fatalf("render errors = (%v, %v)", jsonErr, rdfErr)
		}
		var document struct {
			Graph []map[string]any `json:"@graph"`
		}
		if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		concept := toolkitNodeByID(t, document.Graph, toolkitConceptIRI(mustConceptID(t, "a")))
		if concept["effectiveStatus"] != "unresolved" || concept["trustTier"] != string(bundle.TrustUnverified) {
			t.Fatalf("ambiguous lifecycle/trust projection = %#v", concept)
		}
		for _, predicate := range []string{"declaredStatus", "staleAfter", "stalenessAsOf", "stale"} {
			if _, exists := concept[predicate]; exists {
				t.Fatalf("ambiguous lifecycle projected %s=%#v", predicate, concept[predicate])
			}
		}
		contract := toolkitNodeByID(
			t,
			document.Graph,
			toolkitChildIRI(mustConceptID(t, "a"), "computation-contract", "000000"),
		)
		if contract["computationMode"] != string(bundle.ComputationModeMalformed) {
			t.Fatalf("computationMode = %#v, want malformed", contract["computationMode"])
		}
		for _, predicate := range []string{"runtime", "computationResource", "computationResourceKind", "computationNode"} {
			if _, exists := contract[predicate]; exists {
				t.Fatalf("ambiguous computation projected %s=%#v", predicate, contract[predicate])
			}
		}
		ambiguousType := toolkitNodeByID(
			t,
			document.Graph,
			toolkitConceptIRI(mustConceptID(t, "ambiguous-type")),
		)
		if _, exists := ambiguousType["conceptType"]; exists {
			t.Fatalf("duplicate type selected one conceptType: %#v", ambiguousType)
		}
		for _, node := range document.Graph {
			switch node["@type"] {
			case "proj:Generation", "proj:Verification", "proj:ProvenanceSource":
				t.Fatalf("duplicate family projected typed node: %#v", node)
			case "proj:AttestedComputationContract":
				reference, _ := node["contractOf"].(map[string]any)
				if reference["@id"] == toolkitConceptIRI(mustConceptID(t, "ambiguous-type")) {
					t.Fatalf("duplicate type invented Attested Computation contract: %#v", node)
				}
			}
		}
		for _, leaked := range []string{
			"process:first",
			"human:second",
			"human:first",
			"process:second",
			"leaked-first",
			"leaked-second",
			"first-runtime",
			"second-runtime",
			"first.sql",
			"second.sql",
			"https://legacy.example/source",
			"leaked-runtime",
			"leaked.sql",
		} {
			if strings.Contains(jsonld.String(), leaked) || strings.Contains(ntriples.String(), leaked) {
				t.Fatalf("duplicate standard key leaked first-wins value %q", leaked)
			}
		}
	})

	t.Run("root version", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		writeGraphFile(t, root, "index.md", "---\nokf_version: \"0.1\"\nokf_version: \"0.2\"\n---\n# Index\n")
		writeGraphFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
		b := loadGraphBundle(t, root)
		var out strings.Builder

		// Act.
		err := RenderJSONLDWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

		// Assert.
		if !errors.Is(err, bundle.ErrInvalidVersionDeclaration) {
			t.Fatalf("RenderJSONLDWithOptions() error = %v, want ErrInvalidVersionDeclaration", err)
		}
		if out.Len() != 0 {
			t.Fatalf("ambiguous version produced partial graph: %q", out.String())
		}
	})
}

func TestToolkitV02KeepsMalformedTypedSignalsOutOfProjection(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"status: 17\n"+
		"resource: 17\n"+
		"tags: [valid, 17]\n"+
		"generated: {by: 42, at: 2026-01-01T00:00:00Z}\n"+
		"verified: {by: \"human:\", at: yesterday}\n"+
		"---\nBody.\n")
	b := loadGraphBundle(t, root)

	// Act.
	var out strings.Builder
	err := RenderJSONLDWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

	// Assert.
	if err != nil {
		t.Fatalf("RenderJSONLDWithOptions() error = %v", err)
	}
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(out.String()), &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	concept := toolkitNodeByID(t, document.Graph, "local:bundle:a")
	if concept["trustTier"] != "unverified" {
		t.Fatalf("concept = %#v, malformed verification raised trust", concept)
	}
	if concept["effectiveStatus"] != "unresolved" || concept["declaredStatus"] != "17" {
		t.Fatalf("concept = %#v, malformed status was not surfaced as unresolved", concept)
	}
	for _, predicate := range []string{"resource", "resourceKind", "resourceNode", "tag"} {
		if _, exists := concept[predicate]; exists {
			t.Fatalf("concept = %#v, partial malformed %s projected", concept, predicate)
		}
	}
	generation := toolkitNodeByType(t, document.Graph, "proj:Generation")
	if _, exists := generation["actor"]; exists ||
		!typedJSONLDValueEquals(generation["at"], "2026-01-01T00:00:00Z", "xsd:dateTime") {
		t.Fatalf("generation = %#v, want independently valid timestamp only", generation)
	}
	for _, node := range document.Graph {
		if node["@type"] == "proj:Verification" {
			t.Fatalf("malformed verification projected as typed node: %#v", node)
		}
	}
}

func TestToolkitV02ProjectsStalenessOnlyFromValidStaleAfter(t *testing.T) {
	t.Parallel()

	boolPointer := func(value bool) *bool { return &value }
	tests := []struct {
		name       string
		staleAfter string
		wantStale  *bool
	}{
		{name: "absent"},
		{name: "malformed", staleAfter: "not-a-date"},
		{name: "before boundary", staleAfter: "2026-07-01", wantStale: boolPointer(false)},
		{name: "exact boundary", staleAfter: "2026-06-30", wantStale: boolPointer(true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			frontmatter := "---\ntype: Note\n"
			if tt.staleAfter != "" {
				frontmatter += "stale_after: " + tt.staleAfter + "\n"
			}
			writeGraphFile(t, root, "a.md", frontmatter+"---\nBody.\n")
			b := loadGraphBundle(t, root)
			asOf := time.Date(2026, 6, 30, 16, 0, 0, 0, time.FixedZone("test", 7*60*60))
			options := Options{Profile: ProjectionProfileToolkitV02, AsOf: &asOf}
			var jsonld, ntriples strings.Builder

			// Act.
			jsonErr := RenderJSONLDWithOptions(&jsonld, b, options)
			rdfErr := RenderNTriplesWithOptions(&ntriples, b, options)

			// Assert.
			if jsonErr != nil || rdfErr != nil {
				t.Fatalf("render errors = (%v, %v)", jsonErr, rdfErr)
			}
			var document struct {
				Graph []map[string]any `json:"@graph"`
			}
			if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			concept := toolkitNodeByID(t, document.Graph, toolkitConceptIRI(mustConceptID(t, "a")))
			if tt.wantStale == nil {
				for _, predicate := range []string{"staleAfter", "stalenessAsOf", "stale"} {
					if _, exists := concept[predicate]; exists {
						t.Fatalf("concept = %#v, unproven %s projected", concept, predicate)
					}
					if strings.Contains(ntriples.String(), toolkitProjectionNamespace+predicate+">") {
						t.Fatalf("N-Triples =\n%s\nunproven %s projected", ntriples.String(), predicate)
					}
				}
				return
			}
			if !typedJSONLDValueEquals(concept["staleAfter"], tt.staleAfter, "xsd:date") ||
				!typedJSONLDValueEquals(concept["stalenessAsOf"], "2026-06-30", "xsd:date") ||
				!typedJSONLDValueEquals(concept["stale"], *tt.wantStale, "xsd:boolean") {
				t.Fatalf("concept staleness = %#v, want stale_after=%s stale=%v",
					concept, tt.staleAfter, *tt.wantStale)
			}
			for _, fragment := range []string{
				toolkitProjectionNamespace + `staleAfter> "` + tt.staleAfter + `"^^<http://www.w3.org/2001/XMLSchema#date>`,
				toolkitProjectionNamespace + `stalenessAsOf> "2026-06-30"^^<http://www.w3.org/2001/XMLSchema#date>`,
				toolkitProjectionNamespace + `stale> "` + strconv.FormatBool(*tt.wantStale) + `"^^<http://www.w3.org/2001/XMLSchema#boolean>`,
			} {
				if !strings.Contains(ntriples.String(), fragment) {
					t.Fatalf("N-Triples =\n%s\nmissing %q", ntriples.String(), fragment)
				}
			}
		})
	}
}

func TestToolkitV02FutureVersionIsBestEffort(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "index.md", "---\nokf_version: \"9.7\"\n---\n# Index\n")
	writeGraphFile(t, root, "a.md", "---\ntype: Future\n---\nBody.\n")
	b := loadGraphBundle(t, root)

	// Act.
	var out strings.Builder
	err := RenderJSONLDWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

	// Assert.
	if err != nil {
		t.Fatalf("RenderJSONLDWithOptions() error = %v", err)
	}
	for _, want := range []string{
		`"declaredOKFVersion": "9.7"`,
		`"effectiveOKFVersion": "0.2"`,
		`"versionSource": "future-best-effort"`,
		`"compatibilityMode": "best-effort"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output =\n%s\nwant %q", out.String(), want)
		}
	}
}

func TestToolkitV02DoesNotProjectMalformedTypedDates(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"generated: {by: process:test, at: yesterday}\n"+
		"stale_after: someday\n"+
		"sources: [{id: s, resource: https://example.com, last_modified: recent}]\n"+
		"---\nBody.\n")
	b := loadGraphBundle(t, root)

	// Act.
	var out strings.Builder
	err := RenderNTriplesWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

	// Assert.
	if err != nil {
		t.Fatalf("RenderNTriplesWithOptions() error = %v", err)
	}
	text := out.String()
	for _, literal := range []string{`"yesterday"`, `"someday"`, `"recent"`} {
		if strings.Contains(text, literal) {
			t.Fatalf("output =\n%s\nmalformed typed value %s must remain raw-only", text, literal)
		}
	}
}

func TestToolkitV02ProjectsFullUint64UsageCount(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"sources:\n"+
		"  - id: maximum\n"+
		"    resource: https://example.com\n"+
		"    usage_count: 18446744073709551615\n"+
		"usage_window: {from: 2026-01-01, to: 2026-01-31}\n"+
		"---\nBody.\n")
	b := loadGraphBundle(t, root)

	// Act.
	var out strings.Builder
	err := RenderNTriplesWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02})

	// Assert.
	if err != nil {
		t.Fatalf("RenderNTriplesWithOptions() error = %v", err)
	}
	if !strings.Contains(out.String(), `"18446744073709551615"^^<http://www.w3.org/2001/XMLSchema#integer>`) {
		t.Fatalf("output =\n%s\nwant full uint64 usage_count", out.String())
	}
}

func TestToolkitV02DeduplicatesReusedAssetNodes(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\n"+
		"type: Attested Computation\n"+
		"resource: shared.txt\n"+
		"runtime: custom\n"+
		"computation: shared.txt\n"+
		"executor: {resource: shared.txt}\n"+
		"attester: {resource: shared.txt}\n"+
		"sources: [{id: shared, resource: shared.txt}]\n"+
		"---\nShared.[^shared]\n")
	writeGraphFile(t, root, "shared.txt", "shared\n")
	b := loadGraphBundle(t, root)
	options := Options{Profile: ProjectionProfileToolkitV02}

	// Act.
	var jsonld, ntriples strings.Builder
	jsonldErr := RenderJSONLDWithOptions(&jsonld, b, options)
	ntriplesErr := RenderNTriplesWithOptions(&ntriples, b, options)

	// Assert.
	if jsonldErr != nil || ntriplesErr != nil {
		t.Fatalf("render errors = (%v, %v)", jsonldErr, ntriplesErr)
	}
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(jsonld.String()), &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	assetID := toolkitSyntheticIRI("asset", "shared.txt")
	count := 0
	var asset map[string]any
	for _, node := range document.Graph {
		if node["@id"] == assetID {
			count++
			asset = node
		}
	}
	if count != 1 {
		t.Fatalf("asset node count = %d, want 1; graph = %#v", count, document.Graph)
	}
	roles, ok := asset["resourceRole"].([]any)
	if !ok || len(roles) != 5 {
		t.Fatalf("asset roles = %#v, want five deduplicated roles", asset["resourceRole"])
	}
	if got := strings.Count(ntriples.String(), `<`+assetID+`> <`+ntriplesRDFType+`> <`+toolkitProjectionNamespace+`ReferencedAsset> .`); got != 1 {
		t.Fatalf("ReferencedAsset type triple count = %d, want 1:\n%s", got, ntriples.String())
	}
}

func TestTopologyMetadataRequiresExplicitOption(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := sampleGraphBundle(t)
	renderers := []struct {
		name   string
		render func(io.Writer, Options) error
		marker string
	}{
		{name: "text", render: func(w io.Writer, options Options) error { return RenderTextWithOptions(w, b, options) }, marker: "@ a status=stable trust=unverified"},
		{name: "dot", render: func(w io.Writer, options Options) error { return RenderDOTWithOptions(w, b, options) }, marker: "// a status=stable trust=unverified"},
		{name: "mermaid", render: func(w io.Writer, options Options) error { return RenderMermaidWithOptions(w, b, options) }, marker: "%% a status=stable trust=unverified"},
	}

	for _, renderer := range renderers {
		t.Run(renderer.name, func(t *testing.T) {
			// Arrange.
			var topologyOnly, annotated strings.Builder

			// Act.
			topologyErr := renderer.render(&topologyOnly, Options{Profile: ProjectionProfileToolkitV02})
			annotatedErr := renderer.render(&annotated, Options{
				Profile:          ProjectionProfileToolkitV02,
				AnnotateTopology: true,
			})

			// Assert.
			if topologyErr != nil || annotatedErr != nil {
				t.Fatalf("render errors = (%v, %v)", topologyErr, annotatedErr)
			}
			if strings.Contains(topologyOnly.String(), renderer.marker) {
				t.Fatalf("topology-only output contains annotation:\n%s", topologyOnly.String())
			}
			if !strings.Contains(annotated.String(), renderer.marker) {
				t.Fatalf("annotated output =\n%s\nwant %q", annotated.String(), renderer.marker)
			}
		})
	}
}

func TestTopologyAnnotationProjectsStalenessOnlyFromValidStaleAfter(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, 6, 30, 14, 0, 0, 0, time.FixedZone("test", 7*60*60))
	states := []struct {
		name       string
		staleAfter string
		stale      *bool
	}{
		{name: "absent"},
		{name: "malformed", staleAfter: "not-a-date"},
		{name: "before boundary", staleAfter: "2026-07-01", stale: boolTestPointer(false)},
		{name: "equal boundary", staleAfter: "2026-06-30", stale: boolTestPointer(true)},
		{name: "after boundary", staleAfter: "2026-06-29", stale: boolTestPointer(true)},
	}
	renderers := []struct {
		name     string
		render   func(io.Writer, *bundle.Bundle, Options) error
		expected func(string) string
	}{
		{
			name:   "text",
			render: RenderTextWithOptions,
			expected: func(annotation string) string {
				return "@ a " + annotation + "\na\n  -> b\n"
			},
		},
		{
			name:   "dot",
			render: RenderDOTWithOptions,
			expected: func(annotation string) string {
				return "digraph okf {\n" +
					"  rankdir=LR; node [shape=box, fontsize=10];\n" +
					"  // a " + annotation + "\n" +
					"  \"a\" -> \"b\";\n" +
					"}\n"
			},
		},
		{
			name:   "mermaid",
			render: RenderMermaidWithOptions,
			expected: func(annotation string) string {
				return "graph LR\n" +
					"  %% a " + annotation + "\n" +
					"  n0[\"a\"] --> n1[\"b\"]\n"
			},
		},
	}

	for _, state := range states {
		for _, renderer := range renderers {
			t.Run(renderer.name+"/"+state.name, func(t *testing.T) {
				// Arrange.
				root := t.TempDir()
				frontmatter := "---\ntype: Note\n"
				if state.staleAfter != "" {
					frontmatter += "stale_after: " + state.staleAfter + "\n"
				}
				writeGraphFile(t, root, "a.md", frontmatter+"---\nSee [B](b.md).\n")
				writeGraphFile(t, root, "b.md", "---\ntype: Note\n---\nBody.\n")
				b := loadGraphBundle(t, root)
				options := Options{
					Profile:          ProjectionProfileToolkitV02,
					AsOf:             &asOf,
					AnnotateTopology: true,
				}
				annotation := "status=stable trust=unverified"
				if state.stale != nil {
					annotation += " stale=" + strconv.FormatBool(*state.stale)
				}
				want := renderer.expected(annotation)
				var first, second strings.Builder

				// Act.
				firstErr := renderer.render(&first, b, options)
				secondErr := renderer.render(&second, b, options)

				// Assert.
				if firstErr != nil || secondErr != nil {
					t.Fatalf("render errors = (%v, %v)", firstErr, secondErr)
				}
				if first.String() != want {
					t.Fatalf("output = %q, want %q", first.String(), want)
				}
				if second.String() != first.String() {
					t.Fatalf("non-deterministic output: first=%q second=%q", first.String(), second.String())
				}
			})
		}
	}
}

func boolTestPointer(value bool) *bool {
	return &value
}

func sampleSemanticGraphBundle(t *testing.T) *bundle.Bundle {
	t.Helper()
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"relations:\n"+
		"  depends_on: [{target: b}]\n"+
		"  blocks: [{target: missing}]\n"+
		"  points_to: [{target: b#absent}]\n"+
		"---\nBody.\n")
	writeGraphFile(t, root, "b.md", "---\ntype: Note\n---\nBody.\n")
	return loadGraphBundle(t, root)
}

func fullV02GraphBundle(t *testing.T, shuffled bool) *bundle.Bundle {
	t.Helper()
	root := t.TempDir()
	writeGraphFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Index\n")
	sources := "" +
		"  - id: local-policy\n" +
		"    resource: ../references/policy.txt\n" +
		"    author: team:finance\n" +
		"    usage_count: 9001\n" +
		"    last_modified: 2026-06-18\n" +
		"  - id: external\n" +
		"    resource: https://example.com/policy\n" +
		"  - id: scoped\n" +
		"    resource: all queries in BigQuery project X\n" +
		"    usage_window: {from: 2026-05-01, to: 2026-05-31}\n" +
		"  - id: broken\n" +
		"    resource: ../references/missing-source.txt\n"
	verified := "" +
		"  - {by: process:nightly, at: 2026-06-26T02:00:00Z}\n" +
		"  - {by: human:reviewer, at: 2026-06-25T09:00:00Z}\n"
	parameters := "" +
		"  - {name: year, type: integer, required: true}\n" +
		"  - {name: mode, type: opaque-kind, required: false}\n"
	receipt := "[job_id, executed_sql, result_set]"
	if shuffled {
		sources = "" +
			"  - id: broken\n" +
			"    resource: ../references/missing-source.txt\n" +
			"  - id: scoped\n" +
			"    resource: all queries in BigQuery project X\n" +
			"    usage_window: {from: 2026-05-01, to: 2026-05-31}\n" +
			"  - id: external\n" +
			"    resource: https://example.com/policy\n" +
			"  - id: local-policy\n" +
			"    resource: ../references/policy.txt\n" +
			"    author: team:finance\n" +
			"    usage_count: 9001\n" +
			"    last_modified: 2026-06-18\n"
		verified = "" +
			"  - {by: human:reviewer, at: 2026-06-25T09:00:00Z}\n" +
			"  - {by: process:nightly, at: 2026-06-26T02:00:00Z}\n"
		parameters = "" +
			"  - {name: mode, type: opaque-kind, required: false}\n" +
			"  - {name: year, type: integer, required: true}\n"
		receipt = "[result_set, job_id, executed_sql]"
	}
	writeGraphFile(t, root, "computations/revenue.md", "---\n"+
		"type: Attested Computation\n"+
		"runtime: orbital-db\n"+
		"parameters:\n"+parameters+
		"computation: ../references/revenue.sql\n"+
		"executor:\n"+
		"  resource: ../references/run.txt\n"+
		"  receipt: "+receipt+"\n"+
		"attester:\n"+
		"  resource: ../references/missing.py\n"+
		"generated: {by: reference_agent/custom, at: 2026-06-28T14:00:00Z}\n"+
		"verified:\n"+verified+
		"stale_after: 2026-09-23\n"+
		"sources:\n"+sources+
		"usage_window: {from: 2026-06-01, to: 2026-06-30}\n"+
		"relations:\n"+
		"  depends_on: [{target: missing}]\n"+
		"---\n"+
		"Local policy.[^local-policy] Unknown claim.[^unknown]\n\n"+
		"[^local-policy]: Policy\n"+
		"[^unknown]: Unknown\n")
	writeGraphFile(t, root, "references/policy.txt", "policy\n")
	writeGraphFile(t, root, "references/revenue.sql", "select 1\n")
	writeGraphFile(t, root, "references/run.txt", "run instructions\n")
	return loadGraphBundle(t, root)
}

func toolkitNodeByID(t *testing.T, graph []map[string]any, id string) map[string]any {
	t.Helper()
	for _, node := range graph {
		if node["@id"] == id {
			return node
		}
	}
	t.Fatalf("node %q not found in %#v", id, graph)
	return nil
}

func toolkitNodeByType(t *testing.T, graph []map[string]any, typ string) map[string]any {
	t.Helper()
	for _, node := range graph {
		if node["@type"] == typ {
			return node
		}
	}
	t.Fatalf("node type %q not found in %#v", typ, graph)
	return nil
}

func toolkitNodeByTypeAndProperty(
	t *testing.T,
	graph []map[string]any,
	typ string,
	property string,
	value any,
) map[string]any {
	t.Helper()
	for _, node := range graph {
		if node["@type"] == typ && node[property] == value {
			return node
		}
	}
	t.Fatalf("node type %q with %s=%#v not found in %#v", typ, property, value, graph)
	return nil
}

func toolkitNodeWithIDFragment(graph []map[string]any, fragment string) map[string]any {
	for _, node := range graph {
		id, _ := node["@id"].(string)
		if strings.Contains(id, fragment) {
			return node
		}
	}
	return nil
}

func typedJSONLDValueEquals(value any, wantValue any, wantType string) bool {
	object, ok := value.(map[string]any)
	return ok && object["@value"] == wantValue && object["@type"] == wantType
}
