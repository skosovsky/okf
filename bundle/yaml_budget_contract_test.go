package bundle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestYAMLResourceLimitPublicContract(t *testing.T) {
	// Arrange.
	limits := []struct {
		name  string
		kind  YAMLResourceLimitKind
		value string
		limit int
	}{
		{name: "physical depth", kind: YAMLResourcePhysicalDepth, value: "physical_depth", limit: MaxYAMLPhysicalDepth},
		{name: "semantic depth", kind: YAMLResourceSemanticDepth, value: "semantic_depth", limit: MaxYAMLSemanticDepth},
		{name: "graph nodes", kind: YAMLResourceGraphNodes, value: "graph_nodes", limit: MaxYAMLGraphNodes},
		{name: "graph edges", kind: YAMLResourceGraphEdges, value: "graph_edges", limit: MaxYAMLGraphEdges},
		{name: "semantic expansion", kind: YAMLResourceSemanticExpansion, value: "semantic_expansion", limit: MaxYAMLSemanticExpansion},
	}

	// Act and assert.
	wantCaps := []int{64, 64, 100_000, 100_000, 100_000}
	for index, test := range limits {
		t.Run(test.name, func(t *testing.T) {
			if test.limit != wantCaps[index] {
				t.Fatalf("public cap = %d, want %d", test.limit, wantCaps[index])
			}
			if string(test.kind) != test.value {
				t.Fatalf("kind = %q, want %q", test.kind, test.value)
			}

			want := &YAMLResourceLimitError{
				Kind:     test.kind,
				Limit:    test.limit,
				Observed: test.limit + 1,
			}
			wrapped := fmt.Errorf("parse boundary: %w", want)
			if !errors.Is(wrapped, ErrYAMLResourceLimit) {
				t.Fatalf("errors.Is(error, ErrYAMLResourceLimit) = false: %v", wrapped)
			}
			if !errors.Is(wrapped, ErrInvalidFrontmatter) {
				t.Fatalf("errors.Is(error, ErrInvalidFrontmatter) = false: %v", wrapped)
			}
			if errors.Is(wrapped, ErrInvalidYAMLGraph) {
				t.Fatalf("resource error unexpectedly matches ErrInvalidYAMLGraph: %v", wrapped)
			}
			var got *YAMLResourceLimitError
			if !errors.As(wrapped, &got) {
				t.Fatalf("errors.As(*YAMLResourceLimitError) = false: %v", wrapped)
			}
			if *got != *want {
				t.Fatalf("typed error = %#v, want %#v", got, want)
			}
			if got.Observed != got.Limit+1 {
				t.Fatalf("Observed = %d, want Limit+1 = %d", got.Observed, got.Limit+1)
			}
		})
	}
}

func TestYAMLIntegrityPublicContract(t *testing.T) {
	// Arrange.
	codes := []struct {
		name  string
		code  YAMLIntegrityCode
		value string
	}{
		{name: "invalid node shape", code: YAMLIntegrityInvalidNodeShape, value: "yaml_invalid_node_shape"},
		{name: "content cycle", code: YAMLIntegrityContentCycle, value: "yaml_content_cycle"},
		{name: "content reuse", code: YAMLIntegrityContentReuse, value: "yaml_content_reuse"},
		{name: "alias cycle", code: YAMLIntegrityAliasCycle, value: "yaml_alias_cycle"},
		{name: "duplicate anchor", code: YAMLIntegrityAnchorDuplicate, value: "yaml_anchor_duplicate"},
		{name: "dangling alias", code: YAMLIntegrityAliasDangling, value: "yaml_alias_dangling"},
		{name: "alias name mismatch", code: YAMLIntegrityAliasNameMismatch, value: "yaml_alias_name_mismatch"},
	}

	// Act and assert.
	for _, test := range codes {
		t.Run(test.name, func(t *testing.T) {
			if string(test.code) != test.value {
				t.Fatalf("code = %q, want %q", test.code, test.value)
			}
			want := &YAMLIntegrityError{Code: test.code, Anchor: "contract-anchor"}
			wrapped := fmt.Errorf("boundary: %w", want)
			if !errors.Is(wrapped, ErrInvalidYAMLGraph) {
				t.Fatalf("errors.Is(error, ErrInvalidYAMLGraph) = false: %v", wrapped)
			}
			if !errors.Is(wrapped, ErrInvalidFrontmatter) {
				t.Fatalf("errors.Is(error, ErrInvalidFrontmatter) = false: %v", wrapped)
			}
			if errors.Is(wrapped, ErrYAMLResourceLimit) {
				t.Fatalf("integrity error unexpectedly matches ErrYAMLResourceLimit: %v", wrapped)
			}
			var got *YAMLIntegrityError
			if !errors.As(wrapped, &got) {
				t.Fatalf("errors.As(*YAMLIntegrityError) = false: %v", wrapped)
			}
			if got.Code != test.code || got.Anchor != want.Anchor {
				t.Fatalf("typed error = %#v, want %#v", got, want)
			}
		})
	}
}

func TestValidateYAMLNodeIntegrityMatrix(t *testing.T) {
	// Arrange.
	contentCycle := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	contentCycle.Content = []*yaml.Node{contentCycle}

	reused := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "shared"}
	contentReuse := &yaml.Node{
		Kind:    yaml.SequenceNode,
		Tag:     "!!seq",
		Content: []*yaml.Node{reused, reused},
	}

	aliasLeft := &yaml.Node{Kind: yaml.AliasNode, Anchor: "left", Value: "right"}
	aliasRight := &yaml.Node{Kind: yaml.AliasNode, Anchor: "right", Value: "left"}
	aliasLeft.Alias = aliasRight
	aliasRight.Alias = aliasLeft
	aliasCycle := &yaml.Node{
		Kind:    yaml.SequenceNode,
		Tag:     "!!seq",
		Content: []*yaml.Node{aliasLeft, aliasRight},
	}

	duplicateAnchor := &yaml.Node{
		Kind: yaml.SequenceNode,
		Tag:  "!!seq",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "one", Anchor: "same"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "two", Anchor: "same"},
		},
	}

	external := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "external", Anchor: "external"}
	danglingAlias := &yaml.Node{
		Kind: yaml.SequenceNode,
		Tag:  "!!seq",
		Content: []*yaml.Node{
			{Kind: yaml.AliasNode, Tag: "!!str", Value: "external", Alias: external},
		},
	}

	anchored := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "target", Anchor: "actual"}
	nameMismatch := &yaml.Node{
		Kind: yaml.SequenceNode,
		Tag:  "!!seq",
		Content: []*yaml.Node{
			anchored,
			{Kind: yaml.AliasNode, Value: "other", Alias: anchored},
		},
	}

	invalidMappingShape := &yaml.Node{
		Kind:    yaml.MappingNode,
		Tag:     "!!map",
		Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "key"}},
	}

	tests := []struct {
		name string
		root *yaml.Node
		code YAMLIntegrityCode
	}{
		{name: "mapping has odd content", root: invalidMappingShape, code: YAMLIntegrityInvalidNodeShape},
		{name: "document has two roots", root: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{yamlString("one"), yamlString("two")}}, code: YAMLIntegrityInvalidNodeShape},
		{name: "scalar owns content", root: &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "x", Content: []*yaml.Node{yamlString("child")}}, code: YAMLIntegrityInvalidNodeShape},
		{name: "content cycle", root: contentCycle, code: YAMLIntegrityContentCycle},
		{name: "content reuse", root: contentReuse, code: YAMLIntegrityContentReuse},
		{name: "alias cycle", root: aliasCycle, code: YAMLIntegrityAliasCycle},
		{name: "duplicate anchor", root: duplicateAnchor, code: YAMLIntegrityAnchorDuplicate},
		{name: "dangling alias", root: danglingAlias, code: YAMLIntegrityAliasDangling},
		{name: "alias name mismatch", root: nameMismatch, code: YAMLIntegrityAliasNameMismatch},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateYAMLNode(test.root)
			assertYAMLIntegrityCode(t, err, test.code)
		})
	}
}

func TestYAMLConstructorsAndSetRejectInvalidGraphsAtomically(t *testing.T) {
	// Arrange.
	cycle := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	cycle.Content = []*yaml.Node{cycle}
	invalidRoot := yamlMapping("value", cycle)

	frontmatter, err := ParseFrontmatter("type: Note\ntitle: before\n")
	if err != nil {
		t.Fatalf("ParseFrontmatter() arrange error = %v", err)
	}
	before, err := frontmatter.YAMLString()
	if err != nil {
		t.Fatalf("YAMLString() arrange error = %v", err)
	}

	// Act.
	_, validateErr := NewFrontmatterFromNode(invalidRoot)
	setErr := frontmatter.Set("payload", cycle)
	after, serializeErr := frontmatter.YAMLString()

	// Assert.
	assertYAMLIntegrityCode(t, validateErr, YAMLIntegrityContentCycle)
	assertYAMLIntegrityCode(t, setErr, YAMLIntegrityContentCycle)
	if serializeErr != nil {
		t.Fatalf("YAMLString() after rejected Set error = %v", serializeErr)
	}
	if after != before {
		t.Fatalf("frontmatter mutated after rejected Set:\n got %q\nwant %q", after, before)
	}
	if title, ok := frontmatter.Title(); !ok || title != "before" {
		t.Fatalf("Title() after rejected Set = (%q, %v), want (before, true)", title, ok)
	}
}

func TestYAMLOrdinaryAliasMergeSemanticsRemainDeterministic(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		key       string
		wantValue string
		ambiguous bool
	}{
		{
			name: "explicit overrides merge",
			yaml: "defaults: &defaults {title: merged}\n<<: *defaults\ntitle: explicit\n",
			key:  "title", wantValue: "explicit",
		},
		{
			name: "earlier merge donor wins",
			yaml: "first: &first {title: first}\nsecond: &second {title: second}\n<<: [*first, *second]\n",
			key:  "title", wantValue: "first",
		},
		{
			name: "duplicate explicit key is ambiguous",
			yaml: "title: first\ntitle: second\n",
			key:  "title", ambiguous: true,
		},
		{
			name: "multiple physical merge keys are ambiguous",
			yaml: "first: &first {title: first}\nsecond: &second {title: second}\n<<: *first\n<<: *second\n",
			key:  "title", ambiguous: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			frontmatter, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatalf("ParseFrontmatter() error = %v", err)
			}

			// Act.
			state := frontmatter.SemanticValueState(test.key)

			// Assert.
			if !state.Present || state.Ambiguous != test.ambiguous {
				t.Fatalf("state = %#v, want Present=true Ambiguous=%v", state, test.ambiguous)
			}
			if test.ambiguous {
				if state.Value != nil {
					t.Fatalf("ambiguous state Value = %#v, want nil", state.Value)
				}
				return
			}
			if state.Value == nil || state.Value.Kind != yaml.ScalarNode || state.Value.Value != test.wantValue {
				t.Fatalf("resolved value = %#v, want scalar %q", state.Value, test.wantValue)
			}
		})
	}
}

func TestYAMLAcyclicMalformedOptionalFamiliesDoNotBecomeGraphFailures(t *testing.T) {
	// Arrange.
	text := strings.Join([]string{
		"type: Note",
		"sources: [17, {usage_count: -1}]",
		"generated: wrong",
		"verified: [{by: 17}]",
		"parameters: wrong",
		"executor: [wrong]",
		"attester: 17",
		"relations: {bad: [17]}",
		"",
	}, "\n")

	// Act.
	frontmatter, err := ParseFrontmatter(text)

	// Assert.
	if err != nil {
		t.Fatalf("ParseFrontmatter() rejected acyclic malformed optional families: %v", err)
	}
	if got := frontmatter.Sources(); len(got) != 0 {
		t.Fatalf("Sources() = %#v, want no fabricated typed sources", got)
	}
	if got := frontmatter.ComputationContractState(); got.Valid {
		t.Fatalf("ComputationContractState() = %#v, want malformed/invalid", got)
	}
}

func TestBundleLoadCollectsConceptYAMLBudgetErrorsAndPropagatesRootIndexErrors(t *testing.T) {
	// Arrange.
	bomb := yamlBudgetDeepDocument(MaxYAMLPhysicalDepth + 1)
	source := yamlBudgetSource{
		"index.md": []byte(bomb),
		"valid.md": []byte("---\ntype: Note\ntitle: valid\n---\n\nbody\n"),
		"bomb.md":  []byte(bomb),
	}

	// Act.
	loaded, loadErr := Load(context.Background(), source)
	var versionErr error
	if loaded != nil {
		_, versionErr = loaded.VersionResolutionContext(context.Background(), "")
	}

	// Assert.
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if loaded == nil || loaded.Len() != 1 {
		t.Fatalf("Load() bundle = %#v, Len = %d, want one valid concept", loaded, loaded.Len())
	}
	parseErrors := loaded.ParseErrors()
	if len(parseErrors) != 1 || parseErrors[0].Path != "bomb.md" {
		t.Fatalf("ParseErrors() = %#v, want one bomb.md error", parseErrors)
	}
	assertYAMLResourceKind(t, parseErrors[0].Err, YAMLResourcePhysicalDepth, MaxYAMLPhysicalDepth)
	assertYAMLResourceKind(t, versionErr, YAMLResourcePhysicalDepth, MaxYAMLPhysicalDepth)
}

func TestYAMLContextEntryPointsReturnZeroValuesOnPreCancellation(t *testing.T) {
	// Arrange.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := yamlMapping("type", yamlString("Note"))

	// Act.
	validateErr := ValidateYAMLNodeContext(ctx, root)
	frontmatter, parseErr := ParseFrontmatterContext(ctx, "type: Note\n")
	fromNode, nodeErr := NewFrontmatterFromNodeContext(ctx, root)
	document, documentErr := ParseDocumentContext(ctx, "---\ntype: Note\n---\n")

	// Assert.
	for name, err := range map[string]error{
		"ValidateYAMLNodeContext":       validateErr,
		"ParseFrontmatterContext":       parseErr,
		"NewFrontmatterFromNodeContext": nodeErr,
		"ParseDocumentContext":          documentErr,
	} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%s error = %v, want context.Canceled", name, err)
		}
	}
	if !reflect.DeepEqual(frontmatter, Frontmatter{}) {
		t.Fatalf("ParseFrontmatterContext() value = %#v, want zero Frontmatter", frontmatter)
	}
	if !reflect.DeepEqual(fromNode, Frontmatter{}) {
		t.Fatalf("NewFrontmatterFromNodeContext() value = %#v, want zero Frontmatter", fromNode)
	}
	if !reflect.DeepEqual(document, Document{}) {
		t.Fatalf("ParseDocumentContext() value = %#v, want zero Document", document)
	}
}

func TestYAMLProductionPhysicalAndNodeBoundaries(t *testing.T) {
	t.Run("physical depth exact and plus one", func(t *testing.T) {
		// Arrange.
		exact := yamlBudgetPhysicalChain(MaxYAMLPhysicalDepth)
		over := yamlBudgetPhysicalChain(MaxYAMLPhysicalDepth + 1)

		// Act.
		exactErr := ValidateYAMLNode(exact)
		overErr := ValidateYAMLNode(over)

		// Assert.
		if exactErr != nil {
			t.Fatalf("ValidateYAMLNode(depth=%d) error = %v", MaxYAMLPhysicalDepth, exactErr)
		}
		assertYAMLResourceKind(t, overErr, YAMLResourcePhysicalDepth, MaxYAMLPhysicalDepth)
	})

	t.Run("graph nodes exact and plus one", func(t *testing.T) {
		// Arrange: one shallow sequence root plus unique scalar children.
		root := yamlBudgetWideSequence(MaxYAMLGraphNodes)

		// Act.
		exactErr := ValidateYAMLNode(root)
		root.Content = append(root.Content, yamlString("over"))
		overErr := ValidateYAMLNode(root)

		// Assert.
		if exactErr != nil {
			t.Fatalf("ValidateYAMLNode(nodes=%d) error = %v", MaxYAMLGraphNodes, exactErr)
		}
		assertYAMLResourceKind(t, overErr, YAMLResourceGraphNodes, MaxYAMLGraphNodes)
	})
}

func TestYAMLProductionGraphEdgeBoundary(t *testing.T) {
	// Arrange: root Content contributes 50,002 edges and 49,999 aliases
	// contribute 49,999 more. The graph has 100,001 edges; the analyzer must
	// report the structural edge cap before the coupled expansion cap. Inclusive
	// 100,000-edge acceptance is isolated below with a raised expansion limit.
	anchor := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "base", Anchor: "base"}
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	root.Content = append(root.Content, anchor)
	for index := 0; index < 49_999; index++ {
		root.Content = append(root.Content, &yaml.Node{
			Kind:  yaml.AliasNode,
			Value: "base",
			Alias: anchor,
		})
	}
	root.Content = append(root.Content, yamlString("parity"))
	root.Content = append(root.Content, yamlString("over"))

	// Act.
	overErr := ValidateYAMLNode(root)

	// Assert.
	assertYAMLResourceKind(t, overErr, YAMLResourceGraphEdges, MaxYAMLGraphEdges)
}

func TestYAMLResourceDimensionsAreIsolatedAtExactAndPlusOne(t *testing.T) {
	t.Run("physical depth", func(t *testing.T) {
		// Arrange.
		limits := yamlBudgetTestLimits()
		limits.physicalDepth = 4

		// Act.
		_, exactErr := validateYAMLNodeWithLimitsContext(
			context.Background(), yamlBudgetPhysicalChain(4), false, limits,
		)
		_, overErr := validateYAMLNodeWithLimitsContext(
			context.Background(), yamlBudgetPhysicalChain(5), false, limits,
		)

		// Assert.
		if exactErr != nil {
			t.Fatalf("exact physical depth error = %v", exactErr)
		}
		assertYAMLResourceKind(t, overErr, YAMLResourcePhysicalDepth, 4)
	})

	t.Run("graph nodes", func(t *testing.T) {
		// Arrange.
		limits := yamlBudgetTestLimits()
		limits.graphNodes = 8

		// Act.
		_, exactErr := validateYAMLNodeWithLimitsContext(
			context.Background(), yamlBudgetWideSequence(8), false, limits,
		)
		_, overErr := validateYAMLNodeWithLimitsContext(
			context.Background(), yamlBudgetWideSequence(9), false, limits,
		)

		// Assert.
		if exactErr != nil {
			t.Fatalf("exact graph nodes error = %v", exactErr)
		}
		assertYAMLResourceKind(t, overErr, YAMLResourceGraphNodes, 8)
	})

	t.Run("graph edges", func(t *testing.T) {
		// Arrange.
		limits := yamlBudgetTestLimits()
		limits.graphEdges = 6
		exact := yamlBudgetWideSequence(7)
		over := yamlBudgetWideSequence(8)

		// Act.
		_, exactErr := validateYAMLNodeWithLimitsContext(context.Background(), exact, false, limits)
		_, overErr := validateYAMLNodeWithLimitsContext(context.Background(), over, false, limits)

		// Assert.
		if exactErr != nil {
			t.Fatalf("exact graph edges error = %v", exactErr)
		}
		assertYAMLResourceKind(t, overErr, YAMLResourceGraphEdges, 6)
	})

	t.Run("semantic depth", func(t *testing.T) {
		// Arrange.
		limits := yamlBudgetTestLimits()
		limits.semanticDepth = 4

		// Act.
		_, exactErr := validateYAMLNodeWithLimitsContext(
			context.Background(), yamlBudgetAliasDepth(4), false, limits,
		)
		_, overErr := validateYAMLNodeWithLimitsContext(
			context.Background(), yamlBudgetAliasDepth(5), false, limits,
		)

		// Assert.
		if exactErr != nil {
			t.Fatalf("exact semantic depth error = %v", exactErr)
		}
		assertYAMLResourceKind(t, overErr, YAMLResourceSemanticDepth, 4)
	})

	t.Run("semantic expansion", func(t *testing.T) {
		// Arrange: E(root)=1+E(anchor)+N*(1+E(anchor))=2+2N.
		limits := yamlBudgetTestLimits()
		limits.semanticExpansion = 100
		exact := yamlBudgetAliasFanout(49)
		over := yamlBudgetAliasFanout(50)

		// Act.
		_, exactErr := validateYAMLNodeWithLimitsContext(context.Background(), exact, false, limits)
		_, overErr := validateYAMLNodeWithLimitsContext(context.Background(), over, false, limits)

		// Assert.
		if exactErr != nil {
			t.Fatalf("exact semantic expansion error = %v", exactErr)
		}
		assertYAMLResourceKind(t, overErr, YAMLResourceSemanticExpansion, 100)
	})
}

func TestYAMLSemanticDepthPublicBoundary64And65(t *testing.T) {
	// Arrange.
	exact := yamlBudgetAliasDepth(MaxYAMLSemanticDepth)
	over := yamlBudgetAliasDepth(MaxYAMLSemanticDepth + 1)

	// Act.
	exactErr := ValidateYAMLNode(exact)
	overErr := ValidateYAMLNode(over)

	// Assert.
	if exactErr != nil {
		t.Fatalf("semantic depth %d error = %v", MaxYAMLSemanticDepth, exactErr)
	}
	assertYAMLResourceKind(t, overErr, YAMLResourceSemanticDepth, MaxYAMLSemanticDepth)
}

func TestYAMLSharedAliasDAGAnalysisVisitsEachNodeAndEdgeOncePerPhase(t *testing.T) {
	// Arrange.
	root := yamlBudgetAliasFanout(512)
	limits := yamlBudgetTestLimits()

	// Act.
	analysis, err := validateYAMLNodeWithLimitsContext(context.Background(), root, false, limits)

	// Assert.
	if err != nil {
		t.Fatalf("validate shared alias DAG: %v", err)
	}
	if analysis.contentVisits != len(analysis.nodes) ||
		analysis.semanticVisits != len(analysis.nodes) ||
		analysis.expansionVisits != len(analysis.nodes) {
		t.Fatalf(
			"node visits = content:%d semantic:%d expansion:%d, want %d each",
			analysis.contentVisits,
			analysis.semanticVisits,
			analysis.expansionVisits,
			len(analysis.nodes),
		)
	}
	if analysis.semanticEdgeVisits != analysis.combinedEdgeCount ||
		analysis.expansionEdgeVisits != analysis.combinedEdgeCount {
		t.Fatalf(
			"edge visits = semantic:%d expansion:%d, want %d each",
			analysis.semanticEdgeVisits,
			analysis.expansionEdgeVisits,
			analysis.combinedEdgeCount,
		)
	}
}

func TestYAMLRawSemanticContextRejectsInvalidGraphsAndPreservesOrdinaryResults(t *testing.T) {
	// Arrange.
	valid, err := ParseFrontmatter("defaults: &defaults {title: inherited}\n<<: *defaults\n")
	if err != nil {
		t.Fatalf("ParseFrontmatter() arrange error = %v", err)
	}
	validRoot := valid.YAMLNode()
	cycle := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	cycle.Content = []*yaml.Node{cycle}
	invalidRoot := yamlMapping("title", cycle)

	// Act.
	value, present, valueErr := SemanticMappingValueContext(context.Background(), validRoot, "title")
	state, stateErr := ObserveSemanticMappingValueContext(context.Background(), validRoot, "title")
	semantic, semanticOK, semanticErr := SemanticNodeContext(context.Background(), validRoot.Content[1])
	invalidValue, invalidPresent, invalidErr := SemanticMappingValueContext(context.Background(), invalidRoot, "title")

	// Assert.
	if valueErr != nil || !present || value == nil || value.Value != "inherited" {
		t.Fatalf("SemanticMappingValueContext() = (%#v, %v, %v), want inherited", value, present, valueErr)
	}
	if stateErr != nil || !state.Present || state.Ambiguous || state.Value == nil || state.Value.Value != "inherited" {
		t.Fatalf("ObserveSemanticMappingValueContext() = (%#v, %v), want inherited", state, stateErr)
	}
	if semanticErr != nil || !semanticOK || semantic == nil {
		t.Fatalf("SemanticNodeContext() = (%#v, %v, %v), want valid owned node", semantic, semanticOK, semanticErr)
	}
	if invalidValue != nil || invalidPresent {
		t.Fatalf("invalid SemanticMappingValueContext() partial result = (%#v, %v)", invalidValue, invalidPresent)
	}
	assertYAMLIntegrityCode(t, invalidErr, YAMLIntegrityContentCycle)
}

func TestYAMLEncodeDecodeValidateGraphBoundaries(t *testing.T) {
	// Arrange.
	type model struct {
		Type  string   `yaml:"type"`
		Items []string `yaml:"items"`
	}
	want := model{Type: "Note", Items: []string{"one", "two"}}
	cycle := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	cycle.Content = []*yaml.Node{cycle}
	invalid := Frontmatter{node: *yamlMapping("self", cycle)}

	// Act.
	encoded, encodeErr := EncodeFrontmatter(want)
	decoded, decodeErr := DecodeFrontmatter[model](encoded)
	_, invalidDecodeErr := DecodeFrontmatter[map[string]any](invalid)

	// Assert.
	if encodeErr != nil || decodeErr != nil {
		t.Fatalf("round-trip errors = encode %v, decode %v", encodeErr, decodeErr)
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("decoded = %#v, want %#v", decoded, want)
	}
	assertYAMLIntegrityCode(t, invalidDecodeErr, YAMLIntegrityContentCycle)
}

func TestYAMLContextEntryPointsReturnZeroValuesOnMidCancellation(t *testing.T) {
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
	}{
		{
			name: "validate",
			act: func(ctx context.Context) (any, error) {
				return nil, ValidateYAMLNodeContext(ctx, yamlBudgetWideSequence(256))
			},
		},
		{
			name: "new from node",
			act: func(ctx context.Context) (any, error) {
				value, err := NewFrontmatterFromNodeContext(ctx, yamlMapping("items", yamlBudgetWideSequence(256)))
				return value, err
			},
		},
		{
			name: "raw semantic mapping",
			act: func(ctx context.Context) (any, error) {
				value, present, err := SemanticMappingValueContext(ctx, yamlMapping("items", yamlBudgetWideSequence(256)), "items")
				return struct {
					value   *yaml.Node
					present bool
				}{value: value, present: present}, err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			ctx := &yamlBudgetCancelContext{remaining: 12}

			// Act.
			value, err := test.act(ctx)

			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
			switch got := value.(type) {
			case Frontmatter:
				if !reflect.DeepEqual(got, Frontmatter{}) {
					t.Fatalf("partial Frontmatter = %#v, want zero", got)
				}
			case struct {
				value   *yaml.Node
				present bool
			}:
				if got.value != nil || got.present {
					t.Fatalf("partial raw semantic result = %#v", got)
				}
			}
		})
	}
}

func TestYAMLDeterministicCyclicAndDAGMatrixNeverPanics(t *testing.T) {
	for size := 1; size <= 16; size++ {
		t.Run(fmt.Sprintf("content-cycle-%02d", size), func(t *testing.T) {
			// Arrange.
			root := yamlBudgetContentCycle(size)

			// Act and assert.
			assertYAMLCallDoesNotPanic(t, func() error {
				return ValidateYAMLNodeContext(context.Background(), root)
			}, YAMLIntegrityContentCycle)
		})

		t.Run(fmt.Sprintf("alias-cycle-%02d", size), func(t *testing.T) {
			// Arrange.
			root := yamlBudgetAliasCycle(size)

			// Act and assert.
			assertYAMLCallDoesNotPanic(t, func() error {
				_, _, err := SemanticMappingValueContext(
					context.Background(),
					yamlMapping("value", root),
					"value",
				)
				return err
			}, YAMLIntegrityAliasCycle)
		})

		t.Run(fmt.Sprintf("acyclic-dag-%02d", size), func(t *testing.T) {
			// Arrange.
			root := yamlBudgetAliasDAG(size)

			// Act.
			var err error
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("valid DAG panicked: %v", recovered)
					}
				}()
				err = ValidateYAMLNodeContext(context.Background(), root)
			}()

			// Assert.
			if err != nil {
				t.Fatalf("ValidateYAMLNodeContext(valid DAG size %d) error = %v", size, err)
			}
		})
	}
}

func TestYAMLAliasGraphReturnedValuesAreDefensiveAndIndependent(t *testing.T) {
	// Arrange.
	frontmatter, err := ParseFrontmatter("base: &base {title: original}\nselected: *base\n")
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}
	root := frontmatter.YAMLNode()

	// Act.
	first, present, firstErr := SemanticMappingValueContext(context.Background(), root, "selected")
	second, secondPresent, secondErr := SemanticMappingValueContext(context.Background(), root, "selected")
	if first != nil && len(first.Content) >= 2 {
		first.Content[1].Value = "mutated"
	}

	// Assert.
	if firstErr != nil || secondErr != nil || !present || !secondPresent {
		t.Fatalf("semantic reads = first(%v,%v) second(%v,%v)", present, firstErr, secondPresent, secondErr)
	}
	if second == nil || len(second.Content) < 2 || second.Content[1].Value != "original" {
		t.Fatalf("second defensive value = %#v, want original", second)
	}
}

func assertYAMLResourceKind(t *testing.T, err error, kind YAMLResourceLimitKind, limit int) {
	t.Helper()
	if !errors.Is(err, ErrYAMLResourceLimit) || !errors.Is(err, ErrInvalidFrontmatter) {
		t.Fatalf("error = %v, want resource-limit and invalid-frontmatter identities", err)
	}
	var typed *YAMLResourceLimitError
	if !errors.As(err, &typed) {
		t.Fatalf("error = %v, want *YAMLResourceLimitError", err)
	}
	if typed.Kind != kind || typed.Limit != limit || typed.Observed != limit+1 {
		t.Fatalf("typed error = %#v, want Kind=%q Limit=%d Observed=%d", typed, kind, limit, limit+1)
	}
}

func assertYAMLIntegrityCode(t *testing.T, err error, code YAMLIntegrityCode) {
	t.Helper()
	if !errors.Is(err, ErrInvalidYAMLGraph) || !errors.Is(err, ErrInvalidFrontmatter) {
		t.Fatalf("error = %v, want invalid-YAML-graph and invalid-frontmatter identities", err)
	}
	var typed *YAMLIntegrityError
	if !errors.As(err, &typed) {
		t.Fatalf("error = %v, want *YAMLIntegrityError", err)
	}
	if typed.Code != code {
		t.Fatalf("integrity code = %q, want %q (error %v)", typed.Code, code, err)
	}
}

func yamlString(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func yamlMapping(key string, value *yaml.Node) *yaml.Node {
	return &yaml.Node{
		Kind:    yaml.MappingNode,
		Tag:     "!!map",
		Content: []*yaml.Node{yamlString(key), value},
	}
}

func yamlBudgetPhysicalChain(depth int) *yaml.Node {
	if depth < 0 {
		return nil
	}
	root := yamlString("leaf")
	for current := 0; current < depth; current++ {
		root = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{root}}
	}
	return root
}

func yamlBudgetWideSequence(nodes int) *yaml.Node {
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if nodes <= 1 {
		return root
	}
	root.Content = make([]*yaml.Node, nodes-1)
	for index := range root.Content {
		root.Content[index] = yamlString("node")
	}
	return root
}

func yamlBudgetTestLimits() yamlGraphLimits {
	return yamlGraphLimits{
		physicalDepth:     10_000,
		semanticDepth:     10_000,
		graphNodes:        10_000,
		graphEdges:        10_000,
		semanticExpansion: 10_000_000,
	}
}

func yamlBudgetAliasFanout(aliasCount int) *yaml.Node {
	anchor := &yaml.Node{
		Kind:   yaml.ScalarNode,
		Tag:    "!!str",
		Value:  "base",
		Anchor: "base",
	}
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{anchor}}
	for index := 0; index < aliasCount; index++ {
		root.Content = append(root.Content, &yaml.Node{
			Kind:  yaml.AliasNode,
			Value: anchor.Anchor,
			Alias: anchor,
		})
	}
	return root
}

func yamlBudgetAliasDepth(depth int) *yaml.Node {
	target := &yaml.Node{
		Kind:   yaml.ScalarNode,
		Tag:    "!!str",
		Value:  "leaf",
		Anchor: "depth-target",
	}
	aliases := make([]*yaml.Node, depth)
	for index := depth - 1; index >= 0; index-- {
		aliases[index] = &yaml.Node{
			Kind:   yaml.AliasNode,
			Anchor: fmt.Sprintf("depth-alias-%d", index),
			Value:  target.Anchor,
			Alias:  target,
		}
		target = aliases[index]
	}
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, alias := range aliases {
		root.Content = append(root.Content, alias)
	}
	if depth > 0 {
		root.Content = append(root.Content, aliases[depth-1].Alias)
	} else {
		root.Content = append(root.Content, target)
	}
	return root
}

func yamlBudgetContentCycle(size int) *yaml.Node {
	if size < 1 {
		size = 1
	}
	nodes := make([]*yaml.Node, size)
	for index := range nodes {
		nodes[index] = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	for index := range nodes {
		nodes[index].Content = []*yaml.Node{nodes[(index+1)%len(nodes)]}
	}
	return nodes[0]
}

func yamlBudgetAliasCycle(size int) *yaml.Node {
	if size < 1 {
		size = 1
	}
	aliases := make([]*yaml.Node, size)
	for index := range aliases {
		aliases[index] = &yaml.Node{
			Kind:   yaml.AliasNode,
			Anchor: fmt.Sprintf("alias-%d", index),
		}
	}
	for index := range aliases {
		next := aliases[(index+1)%len(aliases)]
		aliases[index].Alias = next
		aliases[index].Value = next.Anchor
	}
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: aliases}
}

func yamlBudgetAliasDAG(size int) *yaml.Node {
	if size < 1 {
		size = 1
	}
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	anchors := make([]*yaml.Node, 0, size)
	for index := 0; index < size; index++ {
		anchor := &yaml.Node{
			Kind:   yaml.ScalarNode,
			Tag:    "!!str",
			Value:  fmt.Sprintf("value-%d", index),
			Anchor: fmt.Sprintf("anchor-%d", index),
		}
		anchors = append(anchors, anchor)
		root.Content = append(root.Content, anchor)
		for donor := 0; donor <= index; donor++ {
			root.Content = append(root.Content, &yaml.Node{
				Kind:  yaml.AliasNode,
				Value: anchors[donor].Anchor,
				Alias: anchors[donor],
			})
		}
	}
	return root
}

func assertYAMLCallDoesNotPanic(t *testing.T, call func() error, code YAMLIntegrityCode) {
	t.Helper()
	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("YAML graph API panicked: %v", recovered)
			}
		}()
		err = call()
	}()
	assertYAMLIntegrityCode(t, err, code)
}

func yamlBudgetDeepDocument(depth int) string {
	return "---\ntype: Note\npayload: " + strings.Repeat("[", depth) + "value" + strings.Repeat("]", depth) + "\n---\n\nbody\n"
}

type yamlBudgetSource map[string][]byte

func (source yamlBudgetSource) Paths(context.Context) ([]string, error) {
	paths := make([]string, 0, len(source))
	for path := range source {
		paths = append(paths, path)
	}
	return paths, nil
}

func (source yamlBudgetSource) ReadFile(_ context.Context, path string) ([]byte, error) {
	data, ok := source[path]
	if !ok {
		return nil, fmt.Errorf("missing test path %q", path)
	}
	return append([]byte(nil), data...), nil
}

type yamlBudgetCancelContext struct {
	remaining int
}

func (*yamlBudgetCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*yamlBudgetCancelContext) Done() <-chan struct{}       { return nil }
func (*yamlBudgetCancelContext) Value(any) any               { return nil }

func (ctx *yamlBudgetCancelContext) Err() error {
	if ctx.remaining <= 0 {
		return context.Canceled
	}
	ctx.remaining--
	return nil
}
