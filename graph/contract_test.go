package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/piprate/json-gold/ld"
	"github.com/skosovsky/okf/bundle"
)

type executableProjectionContract struct {
	Profile           string                             `json:"profile"`
	ProjectionVersion string                             `json:"projection_version"`
	Namespace         string                             `json:"namespace"`
	Identity          contractIdentity                   `json:"identity"`
	Collections       map[string]string                  `json:"collections"`
	Deduplication     contractDeduplication              `json:"deduplication"`
	Parity            contractParity                     `json:"parity"`
	Joins             map[string]contractJoin            `json:"joins"`
	Datatypes         map[string]string                  `json:"datatypes"`
	Classes           map[string]projectionClassContract `json:"classes"`
}

type contractIdentity struct {
	ConceptIRI         contractIRIIdentity `json:"concept_iri"`
	SyntheticIRI       contractIRIIdentity `json:"synthetic_iri"`
	NamespacesDisjoint bool                `json:"namespaces_disjoint"`
}

type contractIRIIdentity struct {
	Prefix             string `json:"prefix"`
	ComponentEncoding  string `json:"component_encoding"`
	ComponentSeparator string `json:"component_separator,omitempty"`
}

type contractDeduplication struct {
	NodeKey             string   `json:"node_key"`
	CoalesceSameNodeKey bool     `json:"coalesce_same_node_key"`
	PropertyValues      string   `json:"property_values"`
	NodeOrder           []string `json:"node_order"`
	ValueOrder          []string `json:"value_order"`
}

type contractParity struct {
	Formats           []string `json:"formats"`
	Graph             string   `json:"graph"`
	SemanticTripleSet string   `json:"semantic_triple_set"`
}

type contractJoin struct {
	SourceKey      string `json:"source_key"`
	AttributionKey string `json:"attribution_key"`
	Normalization  string `json:"normalization"`
	Cardinality    string `json:"cardinality"`
	Collision      string `json:"collision"`
}

type projectionClassContract struct {
	Closed     bool                                   `json:"closed"`
	Predicates map[string]projectionPredicateContract `json:"predicates"`
}

type projectionPredicateContract struct {
	Min      int    `json:"min"`
	Max      *int   `json:"max"`
	Kind     string `json:"kind"`
	Datatype string `json:"datatype"`
}

func TestToolkitV02ExecutableContract(t *testing.T) {
	t.Parallel()

	// Arrange.
	contract := loadExecutableProjectionContract(t)
	b := fullV02GraphBundle(t, false)
	asOf := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)

	// Act.
	projection, err := buildToolkitProjection(b, Options{
		Profile:            ProjectionProfileToolkitV02,
		AsOf:               &asOf,
		ExtensionRelations: ExtensionRelationsInclude,
	})

	// Assert.
	if err != nil {
		t.Fatalf("buildToolkitProjection() error = %v", err)
	}
	if contract.Profile != string(ProjectionProfileToolkitV02) ||
		contract.ProjectionVersion != toolkitProjectionVersion ||
		contract.Namespace != toolkitProjectionNamespace {
		t.Fatalf("contract identity = (%q, %q, %q), want runtime constants",
			contract.Profile, contract.ProjectionVersion, contract.Namespace)
	}
	if !contract.Identity.NamespacesDisjoint ||
		contract.Identity.ConceptIRI.Prefix != toolkitBundlePrefix ||
		contract.Identity.ConceptIRI.ComponentEncoding != "url-path-escape" ||
		contract.Identity.SyntheticIRI.Prefix != toolkitSyntheticPrefix ||
		contract.Identity.SyntheticIRI.ComponentEncoding != "base64url-unpadded" ||
		contract.Identity.SyntheticIRI.ComponentSeparator != ":" ||
		contract.Collections["empty_predicate"] != "omit" ||
		contract.Deduplication.NodeKey != "@id" ||
		!contract.Deduplication.CoalesceSameNodeKey ||
		contract.Deduplication.PropertyValues != "set" ||
		strings.Join(contract.Deduplication.NodeOrder, ",") != "iri" ||
		strings.Join(contract.Deduplication.ValueOrder, ",") != "predicate,kind,value" ||
		strings.Join(contract.Parity.Formats, ",") != "json-ld,n-triples" ||
		contract.Parity.Graph != "default" ||
		contract.Parity.SemanticTripleSet != "equal" {
		t.Fatalf("contract behavioral rules are incomplete: %#v", contract)
	}
	attributionJoin := contract.Joins["claim_attribution_sources"]
	if attributionJoin.SourceKey != "ProvenanceSource.sourceID" ||
		attributionJoin.AttributionKey != "ClaimAttribution.sourceID" ||
		attributionJoin.Normalization != "commonmark-reference-label" ||
		attributionJoin.Cardinality != "many-to-many" ||
		attributionJoin.Collision != "all-matches" {
		t.Fatalf("attribution join contract is incomplete: %#v", attributionJoin)
	}
	wantClasses := []string{
		"AttestedComputationContract",
		"ClaimAttribution",
		"ComputationParameter",
		"Concept",
		"Generation",
		"Projection",
		"ProvenanceSource",
		"ReferencedAsset",
		"ToolkitExtensionRelation",
		"Verification",
	}
	var gotClasses []string
	for class := range contract.Classes {
		gotClasses = append(gotClasses, class)
	}
	sort.Strings(gotClasses)
	if strings.Join(gotClasses, "\n") != strings.Join(wantClasses, "\n") {
		t.Fatalf("contract classes = %v, want %v", gotClasses, wantClasses)
	}
	for datatype, iri := range map[string]string{
		"string":   "http://www.w3.org/2001/XMLSchema#string",
		"boolean":  "http://www.w3.org/2001/XMLSchema#boolean",
		"integer":  "http://www.w3.org/2001/XMLSchema#integer",
		"date":     "http://www.w3.org/2001/XMLSchema#date",
		"dateTime": "http://www.w3.org/2001/XMLSchema#dateTime",
		"iri":      "@id",
	} {
		if contract.Datatypes[datatype] != iri {
			t.Fatalf("contract datatype %q = %q, want %q", datatype, contract.Datatypes[datatype], iri)
		}
	}
	if err := validateContractDefinition(contract); err != nil {
		t.Fatalf("contract definition is invalid: %v", err)
	}
	if err := validateProjectionAgainstContract(projection, contract); err != nil {
		t.Fatalf("projection violates executable contract: %v", err)
	}
	seenClasses := make(map[string]bool)
	for _, node := range projection.Nodes {
		for _, class := range node.Types {
			seenClasses[class] = true
		}
	}
	for class := range contract.Classes {
		if !seenClasses[class] {
			t.Fatalf("contract class %q is not exercised by full projection fixture", class)
		}
	}
}

func validateContractDefinition(contract executableProjectionContract) error {
	validLiteralDatatypes := map[string]bool{
		contract.Datatypes["string"]:   true,
		contract.Datatypes["boolean"]:  true,
		contract.Datatypes["integer"]:  true,
		contract.Datatypes["date"]:     true,
		contract.Datatypes["dateTime"]: true,
	}
	for className, class := range contract.Classes {
		if !class.Closed {
			return fmt.Errorf("class %q must be closed", className)
		}
		if len(class.Predicates) == 0 {
			return fmt.Errorf("class %q has no predicate rules", className)
		}
		for predicate, rule := range class.Predicates {
			if predicate == "" {
				return fmt.Errorf("class %q has empty predicate name", className)
			}
			if rule.Min < 0 {
				return fmt.Errorf("class %q predicate %q has negative minimum", className, predicate)
			}
			if rule.Max != nil && *rule.Max < rule.Min {
				return fmt.Errorf("class %q predicate %q has invalid cardinality %d..%d",
					className, predicate, rule.Min, *rule.Max)
			}
			switch rule.Kind {
			case "iri":
				if rule.Datatype != contract.Datatypes["iri"] {
					return fmt.Errorf("class %q predicate %q IRI datatype = %q",
						className, predicate, rule.Datatype)
				}
			case "literal":
				if !validLiteralDatatypes[rule.Datatype] {
					return fmt.Errorf("class %q predicate %q unknown literal datatype %q",
						className, predicate, rule.Datatype)
				}
			default:
				return fmt.Errorf("class %q predicate %q unknown kind %q",
					className, predicate, rule.Kind)
			}
		}
	}
	return nil
}

func TestExecutableContractRejectsInvalidProjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*toolkitProjection)
	}{
		{
			name: "unknown class",
			mutate: func(projection *toolkitProjection) {
				projection.Nodes[0].Types = []string{"Unknown"}
			},
		},
		{
			name: "unknown predicate",
			mutate: func(projection *toolkitProjection) {
				projection.Nodes[0].Properties["unknown"] = []projectionValue{{
					Kind: projectionLiteral, Value: "value",
				}}
			},
		},
		{
			name: "missing required predicate",
			mutate: func(projection *toolkitProjection) {
				delete(projection.Nodes[0].Properties, "profile")
			},
		},
		{
			name: "empty collection retained",
			mutate: func(projection *toolkitProjection) {
				projection.Nodes[0].Properties["profile"] = []projectionValue{}
			},
		},
		{
			name: "duplicate value",
			mutate: func(projection *toolkitProjection) {
				value := projection.Nodes[0].Properties["profile"][0]
				projection.Nodes[0].Properties["profile"] = append(
					projection.Nodes[0].Properties["profile"],
					value,
				)
			},
		},
		{
			name: "maximum cardinality",
			mutate: func(projection *toolkitProjection) {
				projection.Nodes[0].Properties["profile"] = append(
					projection.Nodes[0].Properties["profile"],
					projectionValue{Kind: projectionLiteral, Value: "second"},
				)
			},
		},
		{
			name: "wrong kind and datatype",
			mutate: func(projection *toolkitProjection) {
				projection.Nodes[0].Properties["profile"] = []projectionValue{{
					Kind: projectionIRI, Value: "urn:wrong",
				}}
			},
		},
		{
			name: "duplicate node identity",
			mutate: func(projection *toolkitProjection) {
				projection.Nodes = append(projection.Nodes, projection.Nodes[0])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			contract := loadExecutableProjectionContract(t)
			b := sampleGraphBundle(t)
			projection, err := buildToolkitProjection(b, Options{Profile: ProjectionProfileToolkitV02})
			if err != nil {
				t.Fatalf("buildToolkitProjection() error = %v", err)
			}
			projectionIndex := projectionNodeIndex(t, projection.Nodes, toolkitSyntheticIRI("projection"))
			projection.Nodes[0], projection.Nodes[projectionIndex] =
				projection.Nodes[projectionIndex], projection.Nodes[0]
			tt.mutate(&projection)

			// Act.
			err = validateProjectionAgainstContract(projection, contract)

			// Assert.
			if err == nil {
				t.Fatal("validateProjectionAgainstContract() error = nil")
			}
		})
	}
}

func loadExecutableProjectionContract(t *testing.T) executableProjectionContract {
	t.Helper()
	raw, err := os.ReadFile("contracts/v0.2.json")
	if err != nil {
		t.Fatalf("ReadFile(contract) error = %v", err)
	}
	var contract executableProjectionContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("json.Unmarshal(contract) error = %v", err)
	}
	return contract
}

func validateProjectionAgainstContract(
	projection toolkitProjection,
	contract executableProjectionContract,
) error {
	seenNodeIDs := make(map[string]struct{}, len(projection.Nodes))
	for _, node := range projection.Nodes {
		if node.ID == "" {
			return fmt.Errorf("node has empty identity")
		}
		if _, exists := seenNodeIDs[node.ID]; exists {
			return fmt.Errorf("duplicate node identity %q", node.ID)
		}
		seenNodeIDs[node.ID] = struct{}{}
		if len(node.Types) != 1 {
			return fmt.Errorf("node %q has %d classes, want exactly one", node.ID, len(node.Types))
		}
		className := node.Types[0]
		class, exists := contract.Classes[className]
		if !exists {
			return fmt.Errorf("node %q has unknown class %q", node.ID, className)
		}
		if !class.Closed {
			return fmt.Errorf("class %q is not closed", className)
		}
		for predicate, values := range node.Properties {
			rule, allowed := class.Predicates[predicate]
			if !allowed {
				return fmt.Errorf("class %q has unknown predicate %q", className, predicate)
			}
			if len(values) == 0 {
				return fmt.Errorf("node %q retains empty predicate %q", node.ID, predicate)
			}
			if len(values) < rule.Min {
				return fmt.Errorf("node %q predicate %q cardinality %d < %d",
					node.ID, predicate, len(values), rule.Min)
			}
			if rule.Max != nil && len(values) > *rule.Max {
				return fmt.Errorf("node %q predicate %q cardinality %d > %d",
					node.ID, predicate, len(values), *rule.Max)
			}
			seenValues := make(map[string]struct{}, len(values))
			for _, value := range values {
				kind, datatype := projectionContractValueType(value)
				if kind != rule.Kind || datatype != rule.Datatype {
					return fmt.Errorf(
						"node %q predicate %q value (%q, %q), want (%q, %q)",
						node.ID, predicate, kind, datatype, rule.Kind, rule.Datatype,
					)
				}
				key := fmt.Sprintf("%d\x00%s", value.Kind, value.Value)
				if _, duplicate := seenValues[key]; duplicate {
					return fmt.Errorf("node %q predicate %q has duplicate value %#v", node.ID, predicate, value)
				}
				seenValues[key] = struct{}{}
			}
		}
		for predicate, rule := range class.Predicates {
			count := len(node.Properties[predicate])
			if count < rule.Min {
				return fmt.Errorf("node %q missing required predicate %q", node.ID, predicate)
			}
			if rule.Max != nil && *rule.Max < rule.Min {
				return fmt.Errorf("class %q predicate %q has invalid cardinality %d..%d",
					className, predicate, rule.Min, *rule.Max)
			}
		}
	}
	return nil
}

func projectionContractValueType(value projectionValue) (kind, datatype string) {
	switch value.Kind {
	case projectionIRI:
		return "iri", "@id"
	case projectionBoolean:
		return "literal", "http://www.w3.org/2001/XMLSchema#boolean"
	case projectionInteger:
		return "literal", "http://www.w3.org/2001/XMLSchema#integer"
	case projectionDate:
		return "literal", "http://www.w3.org/2001/XMLSchema#date"
	case projectionDateTime:
		return "literal", "http://www.w3.org/2001/XMLSchema#dateTime"
	default:
		return "literal", "http://www.w3.org/2001/XMLSchema#string"
	}
}

func projectionNodeIndex(t *testing.T, nodes []projectionNode, id string) int {
	t.Helper()
	for index, node := range nodes {
		if node.ID == id {
			return index
		}
	}
	t.Fatalf("projection node %q not found", id)
	return -1
}

func TestToolkitJSONLDAndNTriplesHaveSemanticParity(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := fullV02GraphBundle(t, false)
	options := Options{Profile: ProjectionProfileToolkitV02}
	var jsonld, ntriples strings.Builder

	// Act.
	jsonldErr := RenderJSONLDWithOptions(&jsonld, b, options)
	ntriplesErr := RenderNTriplesWithOptions(&ntriples, b, options)
	jsonTriples, jsonErr := standardsJSONLDTriples(jsonld.String())
	rdfTriples, rdfErr := semanticNTriples(ntriples.String())
	var root map[string]any
	rootErr := json.Unmarshal([]byte(jsonld.String()), &root)

	// Assert.
	if jsonldErr != nil || ntriplesErr != nil || jsonErr != nil || rdfErr != nil || rootErr != nil {
		t.Fatalf("render/parse errors = (%v, %v, %v, %v, %v)",
			jsonldErr, ntriplesErr, jsonErr, rdfErr, rootErr)
	}
	if len(root) != 2 || root["@context"] == nil || root["@graph"] == nil {
		t.Fatalf("JSON-LD root = %#v, want only @context and @graph", root)
	}
	if strings.Join(jsonTriples, "\n") != strings.Join(rdfTriples, "\n") {
		t.Fatalf("semantic projection mismatch:\nJSON-LD:\n%s\nN-Triples:\n%s",
			strings.Join(jsonTriples, "\n"), strings.Join(rdfTriples, "\n"))
	}
}

func TestCanonicalAppendixAFrozenSemanticGolden(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := loadGraphBundle(t, "../fixtures/v02/positive/appendix-a")
	asOf := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	options := Options{Profile: ProjectionProfileToolkitV02, AsOf: &asOf}
	golden, err := os.ReadFile("testdata/v02/appendix-a.semantic.nt")
	if err != nil {
		t.Fatalf("ReadFile(Appendix golden) error = %v", err)
	}
	var jsonld, ntriples strings.Builder

	// Act.
	jsonldErr := RenderJSONLDWithOptions(&jsonld, b, options)
	ntriplesErr := RenderNTriplesWithOptions(&ntriples, b, options)
	jsonTriples, jsonErr := standardsJSONLDTriples(jsonld.String())
	goldenTriples, goldenErr := semanticNTriples(string(golden))

	// Assert.
	if jsonldErr != nil || ntriplesErr != nil || jsonErr != nil || goldenErr != nil {
		t.Fatalf("render/parse errors = (%v, %v, %v, %v)",
			jsonldErr, ntriplesErr, jsonErr, goldenErr)
	}
	if ntriples.String() != string(golden) {
		t.Fatalf("Appendix N-Triples changed from frozen semantic golden:\ngot:\n%s\nwant:\n%s",
			ntriples.String(), string(golden))
	}
	if strings.Join(jsonTriples, "\n") != strings.Join(goldenTriples, "\n") {
		t.Fatalf("Appendix JSON-LD RDF differs from frozen semantic golden:\ngot:\n%s\nwant:\n%s",
			strings.Join(jsonTriples, "\n"), strings.Join(goldenTriples, "\n"))
	}
}

func TestCanonicalAppendixAProjection(t *testing.T) {
	t.Parallel()

	// Arrange.
	b := loadGraphBundle(t, "../fixtures/v02/positive/appendix-a")
	asOf := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	options := Options{Profile: ProjectionProfileToolkitV02, AsOf: &asOf}
	var out strings.Builder

	// Act.
	err := RenderJSONLDWithOptions(&out, b, options)

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
	projection := toolkitNodeByID(t, document.Graph, toolkitSyntheticIRI("projection"))
	if projection["declaredOKFVersion"] != "0.2" || projection["effectiveOKFVersion"] != "0.2" {
		t.Fatalf("version projection = (%q, %q), want (0.2, 0.2)",
			projection["declaredOKFVersion"], projection["effectiveOKFVersion"])
	}
	revenue := toolkitNodeByID(t, document.Graph, toolkitConceptIRI(mustConceptID(t, "computations/revenue")))
	profit := toolkitNodeByID(t, document.Graph, toolkitConceptIRI(mustConceptID(t, "computations/profit")))
	narrative := toolkitNodeByID(t, document.Graph, toolkitConceptIRI(mustConceptID(t, "metrics/income-statement")))
	if revenue["trustTier"] != "human-reviewed" ||
		!typedJSONLDValueEquals(revenue["stale"], false, "xsd:boolean") {
		t.Fatalf("Appendix revenue = %#v, want human-reviewed and fresh", revenue)
	}
	if profit["trustTier"] != "machine-confirmed" ||
		!typedJSONLDValueEquals(profit["stale"], true, "xsd:boolean") {
		t.Fatalf("Appendix profit = %#v, want machine-confirmed and stale on boundary", profit)
	}
	references, ok := narrative["references"].([]any)
	if !ok || len(references) != 2 {
		t.Fatalf("Appendix narrative references = %#v, want revenue and profit", narrative["references"])
	}
	revenueContract := toolkitNodeByID(
		t,
		document.Graph,
		toolkitChildIRI(mustConceptID(t, "computations/revenue"), "computation-contract", "000000"),
	)
	profitContract := toolkitNodeByID(
		t,
		document.Graph,
		toolkitChildIRI(mustConceptID(t, "computations/profit"), "computation-contract", "000000"),
	)
	if revenueContract["runtime"] != "bigquery" || profitContract["runtime"] != "dbt" {
		t.Fatalf("Appendix runtimes = (%#v, %#v), want bigquery/dbt", revenueContract, profitContract)
	}
	if revenueContract["computationMode"] != "inline" || profitContract["computationMode"] != "inline" {
		t.Fatalf("Appendix computation modes = (%#v, %#v), want inline/inline",
			revenueContract["computationMode"], profitContract["computationMode"])
	}
	if toolkitNodeByID(
		t,
		document.Graph,
		toolkitSyntheticIRI("asset", "references/attesters/sql-equality.py"),
	)["@type"] != "proj:ReferencedAsset" {
		t.Fatal("Appendix attester asset is not projected")
	}
	attribution := toolkitNodeByID(
		t,
		document.Graph,
		toolkitChildIRI(mustConceptID(t, "computations/revenue"), "attribution", "rev-policy"),
	)
	if !typedJSONLDValueEquals(attribution["resolved"], true, "xsd:boolean") {
		t.Fatalf("Appendix attribution = %#v, want keyed source resolution", attribution)
	}
}

func TestSyntheticIRIsAreCollisionProof(t *testing.T) {
	t.Parallel()

	// Arrange.
	adversarialIDs := []string{
		"projection",
		"asset:shared.txt",
		"generation:a:000000",
		"urn:skosovsky:okf:graph:v0.2:projection",
	}
	kinds := []string{
		"projection",
		"asset",
		"unresolved-asset",
		"generation",
		"verification",
		"source",
		"attribution",
		"computation-contract",
		"parameter",
		"extension-relation",
	}

	// Act.
	seen := make(map[string]string)
	for _, raw := range adversarialIDs {
		id, err := bundle.ParseConceptID(raw)
		if err != nil {
			t.Fatalf("ParseConceptID(%q) error = %v", raw, err)
		}
		iri := toolkitConceptIRI(id)
		if previous := seen[iri]; previous != "" {
			t.Fatalf("concept IRI collision %q between %q and %q", iri, previous, raw)
		}
		seen[iri] = "concept:" + raw
	}
	for _, kind := range kinds {
		iri := toolkitSyntheticIRI(kind, "a:b", "c/d")
		if previous := seen[iri]; previous != "" {
			t.Fatalf("synthetic IRI collision %q between %q and %q", iri, previous, kind)
		}
		if !strings.HasPrefix(iri, toolkitSyntheticPrefix) ||
			strings.HasPrefix(iri, toolkitBundlePrefix) {
			t.Fatalf("synthetic IRI %q is not in reserved disjoint namespace", iri)
		}
		seen[iri] = "synthetic:" + kind
	}

	// Assert.
	left := toolkitSyntheticIRI("source", "a:b", "c")
	right := toolkitSyntheticIRI("source", "a", "b:c")
	if left == right {
		t.Fatalf("component delimiter collision: %q", left)
	}
	if toolkitSyntheticIRI("projection") == toolkitBundlePrefix+"projection" {
		t.Fatal("projection subject collides with concept ID projection")
	}
}

func TestBoundedNTriplesParserRejectsInvalidGrammar(t *testing.T) {
	t.Parallel()

	// Arrange.
	tests := []struct {
		name string
		line string
	}{
		{name: "space in IRI", line: `<subject bad> <predicate> "value" .`},
		{name: "bad literal escape", line: `<subject> <predicate> "bad\q" .`},
		{name: "missing terminator", line: `<subject> <predicate> <object>`},
		{name: "bare object", line: `<subject> <predicate> object .`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			err := validateNTripleLine(tt.line)

			// Assert.
			if err == nil {
				t.Fatalf("validateNTripleLine(%q) error = nil", tt.line)
			}
		})
	}
}

func TestProjectionNodeIDsStayUniqueForReservedLookingConcepts(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "projection.md", "---\n"+
		"type: Attested Computation\n"+
		"resource: projection.md\n"+
		"runtime: custom\n"+
		"---\n# Computation\n\n```text\nvalue\n```\n")
	writeGraphFile(t, root, "asset.md", "---\ntype: Note\n---\nSee [projection](projection.md).\n")
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
	seen := make(map[string]string)
	for _, node := range document.Graph {
		id, _ := node["@id"].(string)
		if id == "" {
			t.Fatalf("node lacks @id: %#v", node)
		}
		if previous := seen[id]; previous != "" {
			t.Fatalf("node @id collision %q between %s and %#v", id, previous, node)
		}
		seen[id] = fmt.Sprintf("%v", node["@type"])
	}
	projectionIRI := toolkitSyntheticIRI("projection")
	conceptIRI := toolkitConceptIRI(mustConceptID(t, "projection"))
	assetIRI := toolkitSyntheticIRI("asset", "projection.md")
	if seen[projectionIRI] == "" || seen[conceptIRI] == "" || seen[assetIRI] == "" ||
		conceptIRI == assetIRI || projectionIRI == conceptIRI {
		t.Fatalf("reserved identities = projection %q, concept %q, asset %q; seen=%#v",
			projectionIRI, conceptIRI, assetIRI, seen)
	}
}

func standardsJSONLDTriples(text string) ([]string, error) {
	var document any
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		return nil, err
	}
	options := ld.NewJsonLdOptions("")
	options.Format = "application/n-quads"
	output, err := ld.NewJsonLdProcessor().ToRDF(document, options)
	if err != nil {
		return nil, err
	}
	serialized, ok := output.(string)
	if !ok {
		return nil, fmt.Errorf("JSON-LD ToRDF output type = %T, want string", output)
	}
	// json-gold preserves an embedded NUL from a JSON string as a raw byte in
	// its N-Quads output. Canonicalize that byte to the required RDF escape
	// before validating and comparing the processor's semantic output.
	serialized = strings.ReplaceAll(serialized, "\x00", `\u0000`)
	var triples []string
	for _, line := range strings.Split(strings.TrimSuffix(serialized, "\n"), "\n") {
		if line == "" {
			continue
		}
		if err := validateNTripleLine(line); err != nil {
			return nil, fmt.Errorf("JSON-LD processor emitted non-default or invalid N-Triples line %q: %w", line, err)
		}
		triples = append(triples, line)
	}
	sort.Strings(triples)
	return triples, nil
}

func semanticNTriples(text string) ([]string, error) {
	var triples []string
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if line == "" {
			continue
		}
		if err := validateNTripleLine(line); err != nil {
			return nil, err
		}
		triples = append(triples, line)
	}
	sort.Strings(triples)
	return triples, nil
}

func validateNTripleLine(line string) error {
	offset, err := consumeNTriplesIRI(line, 0)
	if err != nil {
		return fmt.Errorf("invalid N-Triples subject in %q: %w", line, err)
	}
	if offset >= len(line) || line[offset] != ' ' {
		return fmt.Errorf("missing subject separator in %q", line)
	}
	offset, err = consumeNTriplesIRI(line, offset+1)
	if err != nil {
		return fmt.Errorf("invalid N-Triples predicate in %q: %w", line, err)
	}
	if offset >= len(line) || line[offset] != ' ' {
		return fmt.Errorf("missing predicate separator in %q", line)
	}
	offset++
	switch {
	case offset < len(line) && line[offset] == '<':
		offset, err = consumeNTriplesIRI(line, offset)
	case offset < len(line) && line[offset] == '"':
		offset, err = consumeNTriplesLiteral(line, offset)
	default:
		err = fmt.Errorf("object is neither IRI nor literal")
	}
	if err != nil {
		return fmt.Errorf("invalid N-Triples object in %q: %w", line, err)
	}
	if line[offset:] != " ." {
		return fmt.Errorf("invalid N-Triples terminator in %q", line)
	}
	return nil
}

func consumeNTriplesIRI(text string, offset int) (int, error) {
	if offset >= len(text) || text[offset] != '<' {
		return offset, fmt.Errorf("IRI must begin with '<'")
	}
	for offset++; offset < len(text); offset++ {
		switch value := text[offset]; {
		case value == '>':
			return offset + 1, nil
		case value <= 0x20 || strings.ContainsRune("<>\"{}|^`\\", rune(value)):
			return offset, fmt.Errorf("forbidden IRI byte 0x%02X", value)
		}
	}
	return offset, fmt.Errorf("unterminated IRI")
}

func consumeNTriplesLiteral(text string, offset int) (int, error) {
	if offset >= len(text) || text[offset] != '"' {
		return offset, fmt.Errorf("literal must begin with quote")
	}
	for offset++; offset < len(text); offset++ {
		value := text[offset]
		switch {
		case value == '"':
			offset++
			if strings.HasPrefix(text[offset:], "^^") {
				return consumeNTriplesIRI(text, offset+2)
			}
			return offset, nil
		case value == '\\':
			offset++
			if offset >= len(text) {
				return offset, fmt.Errorf("unterminated escape")
			}
			switch text[offset] {
			case '"', '\\', 't', 'n', 'r', 'b', 'f':
			case 'u':
				if !validHexEscape(text, offset+1, 4) {
					return offset, fmt.Errorf("invalid unicode escape")
				}
				offset += 4
			case 'U':
				if !validHexEscape(text, offset+1, 8) {
					return offset, fmt.Errorf("invalid unicode escape")
				}
				offset += 8
			default:
				return offset, fmt.Errorf("unsupported escape \\%c", text[offset])
			}
		case value < 0x20:
			return offset, fmt.Errorf("unescaped control byte 0x%02X", value)
		}
	}
	return offset, fmt.Errorf("unterminated literal")
}

func validHexEscape(text string, offset, count int) bool {
	if offset+count > len(text) {
		return false
	}
	for _, value := range text[offset : offset+count] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", value) {
			return false
		}
	}
	return true
}

func mustConceptID(t *testing.T, raw string) bundle.ConceptID {
	t.Helper()
	id, err := bundle.ParseConceptID(raw)
	if err != nil {
		t.Fatalf("ParseConceptID(%q) error = %v", raw, err)
	}
	return id
}
