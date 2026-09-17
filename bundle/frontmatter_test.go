package bundle

import (
	"errors"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFrontmatterZeroValueIsUsable(t *testing.T) {
	t.Parallel()

	// Arrange.
	var frontmatter Frontmatter

	// Act.
	err := frontmatter.SetString("type", "Note")
	value, ok := frontmatter.Type()

	// Assert.
	if err != nil {
		t.Fatalf("SetString() error = %v", err)
	}
	if !ok || value != "Note" {
		t.Fatalf("Type() = %q, %v; want Note, true", value, ok)
	}
}

func TestParseFrontmatterRejectsInvalidUTF8BeforeYAML(t *testing.T) {
	t.Parallel()

	// Act.
	_, err := ParseFrontmatter("title: " + string([]byte{0xff}) + "\n")

	// Assert.
	if !errors.Is(err, ErrInvalidEncoding) {
		t.Fatalf("ParseFrontmatter() error = %v, want ErrInvalidEncoding", err)
	}
}

func TestFrontmatterPreservesKeyOrderAndExtensions(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter := NewFrontmatter()

	// Act.
	mustSetString(t, &frontmatter, "type", "BigQuery Table")
	mustSetString(t, &frontmatter, "title", "Orders")
	mustSetString(t, &frontmatter, "custom_key", "custom value")
	mustSetString(t, &frontmatter, "description", "One row per order.")

	// Assert.
	if got, want := frontmatter.Keys(), []string{"type", "title", "custom_key", "description"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys() = %#v, want %#v", got, want)
	}
	if got, want := frontmatter.ExtensionKeys(), []string{"custom_key"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtensionKeys() = %#v, want %#v", got, want)
	}
}

func TestFrontmatterSetReplacesWithoutMovingKey(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter := NewFrontmatter()
	mustSetString(t, &frontmatter, "type", "Old")
	mustSetString(t, &frontmatter, "title", "Title")

	// Act.
	mustSetString(t, &frontmatter, "type", "New")

	// Assert.
	if got, want := frontmatter.Keys(), []string{"type", "title"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys() = %#v, want %#v", got, want)
	}
	if got, ok := frontmatter.Type(); !ok || got != "New" {
		t.Fatalf("Type() = %q, %v; want New, true", got, ok)
	}
}

func TestFrontmatterTypedAccessors(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter := NewFrontmatter()

	// Act.
	mustSetString(t, &frontmatter, "type", "Playbook")
	mustSetString(t, &frontmatter, "title", "Incident Response")
	mustSetString(t, &frontmatter, "description", "Triage steps.")
	mustSetString(t, &frontmatter, "resource", "https://example.com/runbook")
	mustSetString(t, &frontmatter, "timestamp", "2026-05-28T00:00:00Z")
	if err := frontmatter.Set("tags", sequenceNode(
		scalarNode("!!str", "oncall"),
		scalarNode("!!int", "7"),
	)); err != nil {
		t.Fatalf("Set(tags) error = %v", err)
	}

	// Assert.
	assertAccessor(t, "Type", frontmatter.Type, "Playbook")
	assertAccessor(t, "Title", frontmatter.Title, "Incident Response")
	assertAccessor(t, "Description", frontmatter.Description, "Triage steps.")
	assertAccessor(t, "Resource", frontmatter.Resource, "https://example.com/runbook")
	assertAccessor(t, "Timestamp", frontmatter.Timestamp, "2026-05-28T00:00:00Z")
	if got, want := frontmatter.Tags(), []string{"oncall"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Tags() = %#v, want %#v", got, want)
	}
}

func TestFrontmatterStringFieldsRejectWhitespaceOnly(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: \"  \\t\"\nokf_version: \"  \"\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	typ, typeOK := frontmatter.Type()
	version := frontmatter.VersionDeclarationState()

	// Assert.
	if typeOK || typ != "" {
		t.Fatalf("Type() = %q, %v; want absent", typ, typeOK)
	}
	if !version.Present || version.Valid || version.Raw != "  " {
		t.Fatalf("VersionDeclarationState() = %#v, want malformed-present whitespace", version)
	}
}

func TestNewFrontmatterFromNodeClonesInput(t *testing.T) {
	t.Parallel()

	// Arrange.
	node := mappingNode(
		"type", scalarNode("!!str", "Note"),
		"title", scalarNode("!!str", "Original"),
	)

	// Act.
	frontmatter, err := NewFrontmatterFromNode(node)
	if err != nil {
		t.Fatalf("NewFrontmatterFromNode() error = %v", err)
	}
	node.Content[3].Value = "Mutated"
	gotNode, ok := frontmatter.Get("title")
	if !ok {
		t.Fatal("Get(title) ok = false, want true")
	}
	gotNode.Value = "Caller mutation"
	title, ok := frontmatter.Title()

	// Assert.
	if !ok || title != "Original" {
		t.Fatalf("Title() = %q, %v; want Original, true", title, ok)
	}
}

func TestNewFrontmatterFromNodeRejectsNonMapping(t *testing.T) {
	t.Parallel()

	// Arrange.
	node := sequenceNode(scalarNode("!!str", "not a mapping"))

	// Act.
	_, err := NewFrontmatterFromNode(node)

	// Assert.
	if !errors.Is(err, ErrInvalidFrontmatter) {
		t.Fatalf("NewFrontmatterFromNode() error = %v, want ErrInvalidFrontmatter", err)
	}
}

func TestIsEmptyYAMLValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		node *yaml.Node
		want bool
	}{
		{name: "nil", node: nil, want: true},
		{name: "null", node: scalarNode("!!null", ""), want: true},
		{name: "empty string", node: scalarNode("!!str", ""), want: true},
		{name: "false", node: scalarNode("!!bool", "false"), want: true},
		{name: "zero int", node: scalarNode("!!int", "0"), want: true},
		{name: "empty sequence", node: sequenceNode(), want: true},
		{name: "non-empty string", node: scalarNode("!!str", "x"), want: false},
		{name: "true", node: scalarNode("!!bool", "true"), want: false},
		{name: "non-zero int", node: scalarNode("!!int", "42"), want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			node := tt.node

			// Act.
			got := isEmptyYAMLValue(node)

			// Assert.
			if got != tt.want {
				t.Fatalf("isEmptyYAMLValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFrontmatterCloneRemapsYAMLAliasGraph(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("base: &base {nested: [one]}\nfirst: *base\nsecond: *base\n")
	if err != nil {
		t.Fatal(err)
	}
	original := frontmatter.mappingNode()
	originalBase := original.Content[1]

	// Act.
	cloned := frontmatter.YAMLNode()
	first, firstOK := frontmatter.Get("first")
	second, secondOK := frontmatter.Get("second")
	copyValue := frontmatter
	if err := copyValue.SetString("extra", "value"); err != nil {
		t.Fatal(err)
	}
	detached := copyValue.YAMLNode()

	// Assert.
	clonedBase := cloned.Content[1]
	if clonedBase == originalBase || cloned.Content[3].Alias != clonedBase || cloned.Content[5].Alias != clonedBase {
		t.Fatalf("YAMLNode alias graph not remapped: base=%p original=%p first=%p second=%p",
			clonedBase, originalBase, cloned.Content[3].Alias, cloned.Content[5].Alias)
	}
	if !firstOK || !secondOK || first.Alias == nil || second.Alias == nil ||
		first.Alias == originalBase || second.Alias == originalBase {
		t.Fatalf("Get aliases were not independently cloned: first=%#v second=%#v", first, second)
	}
	detachedBase := detached.Content[1]
	if detachedBase == originalBase || detached.Content[3].Alias != detachedBase || detached.Content[5].Alias != detachedBase {
		t.Fatalf("Set copy alias graph not detached/remapped: %#v", detached)
	}
}

func TestParseFrontmatterPreservesUnrelatedDuplicateExtensionKeysLosslessly(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := "type: Note\nx-producer:\n  duplicate: one\n  duplicate: two\n"

	// Act.
	frontmatter, err := ParseFrontmatter(source)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := frontmatter.YAMLString()
	if err != nil {
		t.Fatal(err)
	}
	node := frontmatter.YAMLNode()

	// Assert.
	if serialized != source {
		t.Fatalf("YAMLString() = %q, want byte-exact %q", serialized, source)
	}
	extension, ok := frontmatter.Get("x-producer")
	if !ok || extension.Kind != yaml.MappingNode || len(extension.Content) != 4 {
		t.Fatalf("Get(x-producer) = %#v, %v; want duplicate mapping entries", extension, ok)
	}
	if node.Kind != yaml.MappingNode || len(node.Content) != 4 {
		t.Fatalf("YAMLNode() = %#v, want complete root mapping", node)
	}
}

func TestSemanticValueStateFailsClosedOnDuplicateStandardKeys(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\ntype: Other\ngenerated: {by: process:one}\ngenerated: {by: process:two}\ntimestamp: 2026-07-29T00:00:00Z\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	typeState := frontmatter.SemanticValueState("type")
	generatedState := frontmatter.SemanticValueState("generated")
	_, typeOK := frontmatter.Type()
	effectiveTime, effectiveTimeOK := frontmatter.EffectiveContentChangeTime()
	fallback := frontmatter.LegacyFallbackObservation()

	// Assert.
	for name, state := range map[string]SemanticValueState{"type": typeState, "generated": generatedState} {
		if !state.Present || !state.Ambiguous || state.Value != nil {
			t.Fatalf("%s state = %#v, want present ambiguous unresolved", name, state)
		}
	}
	if typeOK {
		t.Fatal("Type() selected one duplicate value")
	}
	if effectiveTimeOK || effectiveTime != "" {
		t.Fatalf("EffectiveContentChangeTime() = %q, %v; want unresolved", effectiveTime, effectiveTimeOK)
	}
	if !fallback.GeneratedPresent || fallback.TimestampAllowed || fallback.TimestampActive {
		t.Fatalf("LegacyFallbackObservation() = %#v; duplicate generated must suppress timestamp", fallback)
	}
}

func TestSemanticMappingValueAppliesMergePrecedenceAndStopsCycles(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter(`
defaults: &defaults
  title: default
  description: inherited
later: &later
  title: later
  resource: inherited.md
<<: [*defaults, *later]
title: explicit
source_defaults: &source_defaults {id: inherited-id, resource: inherited.md}
sources:
  - <<: *source_defaults
    resource: explicit.md
`)
	if err != nil {
		t.Fatal(err)
	}
	cycle := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	alias := &yaml.Node{Kind: yaml.AliasNode, Value: "cycle", Alias: cycle}
	cycle.Content = []*yaml.Node{
		scalarNode("!!merge", "<<"),
		alias,
	}

	// Act.
	title, titleOK := frontmatter.Title()
	description, descriptionOK := frontmatter.Description()
	resource, resourceOK := frontmatter.Resource()
	_, rawDescription := frontmatter.Get("description")
	sources := frontmatter.Sources()
	_, cycleOK := SemanticMappingValue(cycle, "missing")

	// Assert.
	if title != "explicit" || !titleOK || description != "inherited" || !descriptionOK ||
		resource != "inherited.md" || !resourceOK || rawDescription {
		t.Fatalf("semantic root = title %q/%v description %q/%v resource %q/%v rawDescription=%v",
			title, titleOK, description, descriptionOK, resource, resourceOK, rawDescription)
	}
	if len(sources) != 1 || sources[0].ID != "inherited-id" || sources[0].Resource != "explicit.md" {
		t.Fatalf("Sources() merge projection = %#v", sources)
	}
	if cycleOK {
		t.Fatal("SemanticMappingValue() resolved a merge cycle")
	}
}

func TestSemanticMappingValueDereferencesTerminalAliases(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter(`
source: &source {resource: source.md}
sources: [*source]
title_value: &title_value Alias title
title: *title_value
`)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	title, titleOK := frontmatter.Title()
	sources := frontmatter.SourceStates()
	rawTitle, _ := frontmatter.Get("title")
	semanticTitle, semanticOK := frontmatter.SemanticGet("title")

	// Assert.
	if !titleOK || title != "Alias title" || !semanticOK ||
		semanticTitle.Kind != yaml.ScalarNode || semanticTitle.Value != "Alias title" {
		t.Fatalf("terminal alias title = %q/%v node=%#v", title, titleOK, semanticTitle)
	}
	if rawTitle.Kind != yaml.AliasNode {
		t.Fatalf("Get(title) changed raw alias: %#v", rawTitle)
	}
	if len(sources) != 1 || !sources[0].Valid || sources[0].Value.Resource != "source.md" {
		t.Fatalf("SourceStates() alias item = %#v", sources)
	}
}

func TestSemanticMappingValueFailsClosedOnDuplicateMergeKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		yaml          string
		key           string
		wantPresent   bool
		wantAmbiguous bool
		wantValue     string
	}{
		{
			name: "conflicting donors",
			yaml: "left: &left {stale_after: 2026-07-28}\n" +
				"right: &right {stale_after: 2026-07-29}\n" +
				"<<: *left\n<<: *right\n",
			key: "stale_after", wantPresent: true, wantAmbiguous: true,
		},
		{
			name: "only one donor contains affected family",
			yaml: "left: &left {stale_after: 2026-07-28}\n" +
				"right: &right {status: stable}\n" +
				"<<: *left\n<<: *right\n",
			key: "stale_after", wantPresent: true, wantAmbiguous: true,
		},
		{
			name: "same donor repeated",
			yaml: "left: &left {stale_after: 2026-07-28}\n" +
				"<<: *left\n<<: *left\n",
			key: "stale_after", wantPresent: true, wantAmbiguous: true,
		},
		{
			name: "nested donor duplicate direct key",
			yaml: "donor: &donor\n" +
				"  stale_after: 2026-07-28\n" +
				"  stale_after: 2026-07-29\n" +
				"<<: *donor\n",
			key: "stale_after", wantPresent: true, wantAmbiguous: true,
		},
		{
			name: "nested duplicate merge keys",
			yaml: "left: &left {stale_after: 2026-07-28}\n" +
				"right: &right {stale_after: 2026-07-29}\n" +
				"nested: &nested\n" +
				"  <<: *left\n" +
				"  <<: *right\n" +
				"<<: *nested\n",
			key: "stale_after", wantPresent: true, wantAmbiguous: true,
		},
		{
			name: "explicit value overrides duplicate merges",
			yaml: "left: &left {stale_after: 2026-07-28}\n" +
				"right: &right {stale_after: 2026-07-29}\n" +
				"<<: *left\n<<: *right\n" +
				"stale_after: 2026-07-30\n",
			key: "stale_after", wantPresent: true, wantValue: "2026-07-30",
		},
		{
			name: "one merge sequence retains precedence",
			yaml: "left: &left {stale_after: 2026-07-28}\n" +
				"right: &right {stale_after: 2026-07-29}\n" +
				"<<: [*left, *right]\n",
			key: "stale_after", wantPresent: true, wantValue: "2026-07-28",
		},
		{
			name: "unaffected family remains absent",
			yaml: "left: &left {status: stable}\n" +
				"right: &right {stale_after: 2026-07-29}\n" +
				"<<: *left\n<<: *right\n",
			key: "generated",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			state := frontmatter.SemanticValueState(test.key)
			value, present := frontmatter.SemanticGet(test.key)

			// Assert.
			if state.Present != test.wantPresent ||
				state.Ambiguous != test.wantAmbiguous ||
				present != test.wantPresent {
				t.Fatalf("semantic state = %#v, SemanticGet present=%t", state, present)
			}
			if test.wantAmbiguous {
				if state.Value != nil || value == nil || value.Kind != 0 {
					t.Fatalf("ambiguous value leaked: state=%#v value=%#v", state, value)
				}
				return
			}
			if !test.wantPresent {
				if state.Value != nil || value != nil {
					t.Fatalf("absent value leaked: state=%#v value=%#v", state, value)
				}
				return
			}
			if state.Value == nil || value == nil ||
				state.Value.Value != test.wantValue || value.Value != test.wantValue {
				t.Fatalf("semantic value = state:%#v get:%#v, want %q",
					state, value, test.wantValue)
			}
		})
	}
}

func TestFrontmatterSetFailsClosedOnDanglingAliases(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("base: &base {value: one}\nuse: *base\n")
	if err != nil {
		t.Fatal(err)
	}
	before, err := frontmatter.YAMLString()
	if err != nil {
		t.Fatal(err)
	}
	detachedTarget := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value", Anchor: "detached"}
	detachedAlias := &yaml.Node{Kind: yaml.AliasNode, Value: "detached", Alias: detachedTarget}

	// Act.
	replaceErr := frontmatter.SetString("base", "replacement")
	insertErr := frontmatter.Set("detached", detachedAlias)
	after, serializeErr := frontmatter.YAMLString()
	_, reparseErr := ParseFrontmatter(after)

	// Assert.
	if !errors.Is(replaceErr, ErrInvalidFrontmatter) || !errors.Is(insertErr, ErrInvalidFrontmatter) {
		t.Fatalf("Set errors = replace:%v insert:%v", replaceErr, insertErr)
	}
	if serializeErr != nil || reparseErr != nil || after != before {
		t.Fatalf("failed Set mutated frontmatter: after=%q serialize=%v reparse=%v", after, serializeErr, reparseErr)
	}
}

func mustSetString(t *testing.T, frontmatter *Frontmatter, key, value string) {
	t.Helper()
	if err := frontmatter.SetString(key, value); err != nil {
		t.Fatalf("SetString(%q) error = %v", key, err)
	}
}

func TestFrontmatterScalarKeyIdentityIsCollisionFree(t *testing.T) {
	t.Parallel()

	// Arrange: these tag/value tuples collided under NUL concatenation.
	root := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			scalarNode("a\x00b", "c"), scalarNode("!!str", "first"),
			scalarNode("a", "b\x00c"), scalarNode("!!str", "second"),
		},
	}

	// Act.
	_, err := NewFrontmatterFromNode(root)

	// Assert.
	if err != nil {
		t.Fatalf("distinct scalar key tuples rejected as duplicates: %v", err)
	}
}

func assertAccessor(t *testing.T, name string, accessor func() (string, bool), want string) {
	t.Helper()
	got, ok := accessor()
	if !ok || got != want {
		t.Fatalf("%s() = %q, %v; want %q, true", name, got, ok, want)
	}
}

func scalarNode(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}

func sequenceNode(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

func mappingNode(keyValues ...any) *yaml.Node {
	content := make([]*yaml.Node, 0, len(keyValues))
	for i := 0; i+1 < len(keyValues); i += 2 {
		content = append(content, scalarNode("!!str", keyValues[i].(string)), keyValues[i+1].(*yaml.Node))
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: content}
}
