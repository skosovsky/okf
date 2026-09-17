package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func TestMCPYAMLResourceDimensionsPreserveDomainCauseAndWireProjection(t *testing.T) {
	tests := []struct {
		name  string
		kind  bundle.YAMLResourceLimitKind
		exact *yaml.Node
		over  *yaml.Node
	}{
		{
			name:  "physical depth",
			kind:  bundle.YAMLResourcePhysicalDepth,
			exact: a2YAMLPhysicalChain(bundle.MaxYAMLPhysicalDepth),
			over:  a2YAMLPhysicalChain(bundle.MaxYAMLPhysicalDepth + 1),
		},
		{
			name:  "semantic depth",
			kind:  bundle.YAMLResourceSemanticDepth,
			exact: a2YAMLAliasDepth(bundle.MaxYAMLSemanticDepth),
			over:  a2YAMLAliasDepth(bundle.MaxYAMLSemanticDepth + 1),
		},
		{
			name:  "graph nodes",
			kind:  bundle.YAMLResourceGraphNodes,
			exact: a2YAMLWideSequence(bundle.MaxYAMLGraphNodes),
			over:  a2YAMLWideSequence(bundle.MaxYAMLGraphNodes + 1),
		},
		{
			name: "graph edges",
			kind: bundle.YAMLResourceGraphEdges,
			// An exact-edge alias graph necessarily crosses the equal-sized
			// semantic-expansion cap. The assertion below proves it crossed
			// no graph-edge boundary; +1 is classified as graph_edges first.
			exact: a2YAMLGraphEdges(bundle.MaxYAMLGraphEdges),
			over:  a2YAMLGraphEdges(bundle.MaxYAMLGraphEdges + 1),
		},
		{
			name:  "semantic expansion",
			kind:  bundle.YAMLResourceSemanticExpansion,
			exact: a2YAMLSemanticExpansion(false),
			over:  a2YAMLSemanticExpansion(true),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange / Act.
			exactErr := bundle.ValidateYAMLNodeContext(t.Context(), test.exact)
			overErr := bundle.ValidateYAMLNodeContext(t.Context(), test.over)

			// Assert. Graph-edge exactness is feasible at the edge phase but
			// cannot also satisfy the independent expansion cap of equal size.
			if test.kind == bundle.YAMLResourceGraphEdges {
				var exactLimit *bundle.YAMLResourceLimitError
				if !errors.As(exactErr, &exactLimit) || exactLimit.Kind != bundle.YAMLResourceSemanticExpansion {
					t.Fatalf("exact graph-edge error = %v, want later semantic_expansion boundary", exactErr)
				}
			} else if exactErr != nil {
				t.Fatalf("exact public boundary returned error: %v", exactErr)
			}
			assertA2YAMLResourceCause(t, overErr, test.kind)
			assertA2ErrorEnvelope(
				t,
				bundleDomainError("capture bundle", "bundle exceeds MCP resource limits", overErr),
				"resource_limit",
				"bundle exceeds MCP resource limits",
				false,
			)
		})
	}
}

func TestMCPYAMLIntegrityClassesCollapseToGenericWireError(t *testing.T) {
	tests := []struct {
		name string
		code bundle.YAMLIntegrityCode
		root *yaml.Node
	}{
		{
			name: "invalid node shape", code: bundle.YAMLIntegrityInvalidNodeShape,
			root: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{a2YAMLString("odd")}},
		},
		{
			name: "content cycle", code: bundle.YAMLIntegrityContentCycle,
			root: a2YAMLContentCycle(),
		},
		{
			name: "content reuse", code: bundle.YAMLIntegrityContentReuse,
			root: a2YAMLContentReuse(),
		},
		{
			name: "alias cycle", code: bundle.YAMLIntegrityAliasCycle,
			root: a2YAMLAliasCycle(),
		},
		{
			name: "dangling alias", code: bundle.YAMLIntegrityAliasDangling,
			root: &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{{
				Kind: yaml.AliasNode, Value: "missing",
			}}},
		},
		{
			name: "alias name mismatch", code: bundle.YAMLIntegrityAliasNameMismatch,
			root: a2YAMLAliasNameMismatch(),
		},
		{
			name: "duplicate anchor", code: bundle.YAMLIntegrityAnchorDuplicate,
			root: a2YAMLDuplicateAnchor(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			err := bundle.ValidateYAMLNodeContext(t.Context(), test.root)

			// Assert.
			if !errors.Is(err, bundle.ErrInvalidYAMLGraph) {
				t.Fatalf("domain error = %v, want ErrInvalidYAMLGraph", err)
			}
			var integrity *bundle.YAMLIntegrityError
			if !errors.As(err, &integrity) || integrity.Code != test.code {
				t.Fatalf("domain error = %v, want integrity code %s", err, test.code)
			}
			result := bundleDomainError("capture bundle", "unused resource message", err)
			assertA2ErrorEnvelope(
				t,
				result,
				"invalid_yaml_graph",
				"bundle contains an invalid YAML graph",
				false,
			)
			if strings.Contains(resultText(t, result), string(test.code)) {
				t.Fatalf("wire message exposed internal integrity code %q", test.code)
			}
		})
	}
}

func TestMCPBoundedCaptureUsesDomainYAMLBoundary(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		kind bundle.YAMLResourceLimitKind
		code bundle.YAMLIntegrityCode
	}{
		{
			name: "physical resource",
			raw:  a2PhysicalDepthDocument(bundle.MaxYAMLPhysicalDepth + 1),
			kind: bundle.YAMLResourcePhysicalDepth,
		},
		{
			name: "duplicate anchor integrity",
			raw:  "---\ntype: Note\nleft: &same one\nright: &same two\n---\nBody.\n",
			code: bundle.YAMLIntegrityAnchorDuplicate,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			err := validateMCPDocumentYAMLContext(t.Context(), []byte(test.raw))

			// Assert.
			if test.kind != "" {
				assertA2YAMLResourceCause(t, err, test.kind)
				return
			}
			var integrity *bundle.YAMLIntegrityError
			if !errors.Is(err, bundle.ErrInvalidYAMLGraph) ||
				!errors.As(err, &integrity) || integrity.Code != test.code {
				t.Fatalf("bounded YAML error = %v, want integrity code %s", err, test.code)
			}
		})
	}

	t.Run("ordinary malformed YAML remains a parse error", func(t *testing.T) {
		// Arrange.
		const malformed = "---\ntype: [unterminated\n---\nBody.\n"
		source := staticBundleSource{paths: []string{"bad.md"}, data: []byte(malformed)}

		// Act.
		loaded, err := loadBoundedSource(t.Context(), source)

		// Assert.
		if err != nil {
			t.Fatalf("ordinary malformed YAML aborted capture: %v", err)
		}
		if len(loaded.ParseErrors()) != 1 {
			t.Fatalf("parse errors = %#v, want one established per-file error", loaded.ParseErrors())
		}
		if errors.Is(loaded.ParseErrors()[0].Err, bundle.ErrYAMLResourceLimit) ||
			errors.Is(loaded.ParseErrors()[0].Err, bundle.ErrInvalidYAMLGraph) {
			t.Fatalf("ordinary parse error misclassified: %v", loaded.ParseErrors()[0].Err)
		}
	})
}

func TestAllNineToolsRejectDomainYAMLBeforeStoreIO(t *testing.T) {
	tests := []struct {
		name             string
		raw              string
		code             string
		coreMessage      string
		migrationMessage string
	}{
		{
			name:             "resource",
			raw:              a2PhysicalDepthDocument(bundle.MaxYAMLPhysicalDepth + 1),
			code:             "resource_limit",
			coreMessage:      "bundle exceeds MCP resource limits",
			migrationMessage: "migration source exceeds MCP resource limits",
		},
		{
			name:             "integrity",
			raw:              "---\ntype: Note\nleft: &same one\nright: &same two\n---\nBody.\n",
			code:             "invalid_yaml_graph",
			coreMessage:      "bundle contains an invalid YAML graph",
			migrationMessage: "bundle contains an invalid YAML graph",
		},
	}

	for _, test := range tests {
		for _, toolName := range []string{
			"list_concepts", "read_concept", "validate_bundle", "get_semantic_graph",
			"write_concept", "preview_concept_patch", "apply_concept_patch",
			"preview_v02_migration", "apply_v02_migration",
		} {
			t.Run(test.name+"/"+toolName, func(t *testing.T) {
				// Arrange.
				root := t.TempDir()
				writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n")
				writeTestFile(t, root, "a.md", test.raw)
				before := protocolTreeSnapshot(t, root)
				var selected toolHandlerCase
				for _, candidate := range toolHandlerCases(root) {
					if candidate.name == toolName {
						selected = candidate
						break
					}
				}
				if selected.handler == nil {
					t.Fatalf("tool handler %q not found", toolName)
				}
				if toolName == "apply_v02_migration" {
					selected.args = migrationPresenceBase(root, true)
				}
				storeOpens := 0
				ctx := withLegacyToolIOObserver(t.Context(), legacyToolIOObserver{
					storeOpen: func() { storeOpens++ },
				})
				ctx = withMigrationIOObserver(ctx, migrationIOObserver{
					storeOpen: func() { storeOpens++ },
				})

				// Act.
				result, err := selected.handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
					Arguments: selected.args,
				}})

				// Assert.
				if err != nil {
					t.Fatalf("handler protocol error: %v", err)
				}
				message := test.coreMessage
				if strings.Contains(toolName, "patch") || strings.Contains(toolName, "migration") {
					message = test.migrationMessage
				}
				assertA2ErrorEnvelope(t, result, test.code, message, false)
				if storeOpens != 0 {
					t.Fatalf("store opens = %d, want zero", storeOpens)
				}
				if _, statErr := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("pre-durable rejection created .okf: %v", statErr)
				}
				after := protocolTreeSnapshot(t, root)
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("bundle tree changed\nbefore: %#v\nafter:  %#v", before, after)
				}
			})
		}
	}
}

func TestBundleDomainErrorSentinelPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		code      string
		message   string
		retryable bool
	}{
		{
			name: "cancellation before all",
			err:  errors.Join(context.Canceled, bundle.ErrYAMLResourceLimit, bundle.ErrInvalidYAMLGraph),
			code: "operation_cancelled", message: "capture was cancelled", retryable: true,
		},
		{
			name: "resource before integrity",
			err:  errors.Join(bundle.ErrYAMLResourceLimit, bundle.ErrInvalidYAMLGraph),
			code: "resource_limit", message: "bounded resource", retryable: false,
		},
		{
			name: "integrity before generic",
			err:  errors.Join(bundle.ErrInvalidYAMLGraph, errors.New("generic")),
			code: "invalid_yaml_graph", message: "bundle contains an invalid YAML graph", retryable: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			result := bundleDomainError("capture", "bounded resource", test.err)

			// Assert.
			assertA2ErrorEnvelope(t, result, test.code, test.message, test.retryable)
		})
	}
}

func TestMCPProductionHasNoLocalYAMLWalker(t *testing.T) {
	// Arrange.
	production, err := os.ReadFile("tools.go")
	if err != nil {
		t.Fatalf("read tools.go: %v", err)
	}
	text := string(production)

	// Act / Assert.
	for _, forbidden := range []string{
		"validateMarkdownYAMLDepth", "validateYAMLNodeDepth",
		"internal/documentlayout", "gopkg.in/yaml.v3", "yaml.Node",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("tools.go retains local YAML walker token %q", forbidden)
		}
	}
	for _, required := range []string{"bundle.ParseDocumentContext", "bundle.ParseFrontmatterContext"} {
		if !strings.Contains(text, required) {
			t.Errorf("tools.go lacks domain YAML boundary %q", required)
		}
	}
}

func assertA2YAMLResourceCause(
	t *testing.T,
	err error,
	want bundle.YAMLResourceLimitKind,
) {
	t.Helper()
	if !errors.Is(err, bundle.ErrYAMLResourceLimit) {
		t.Fatalf("error = %v, want ErrYAMLResourceLimit", err)
	}
	var typed *bundle.YAMLResourceLimitError
	if !errors.As(err, &typed) || typed.Kind != want || typed.Observed != typed.Limit+1 {
		t.Fatalf("resource cause = %#v, want kind %s and saturated observed", typed, want)
	}
}

func assertA2ErrorEnvelope(
	t *testing.T,
	result *mcp.CallToolResult,
	wantCode string,
	wantMessage string,
	wantRetryable bool,
) {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("result = %#v, want error", result)
	}
	envelope, ok := result.StructuredContent.(errorEnvelope)
	if !ok {
		t.Fatalf("structured content type = %T, want errorEnvelope only", result.StructuredContent)
	}
	if envelope.Code != wantCode || envelope.Message != wantMessage ||
		envelope.Retryable != wantRetryable || len(envelope.Diagnostics) != 0 {
		t.Fatalf("error envelope = %#v", envelope)
	}
	if got := resultText(t, result); got != wantMessage {
		t.Fatalf("error text = %q, want %q", got, wantMessage)
	}
}

func a2PhysicalDepthDocument(depth int) string {
	sequenceDepth := max(0, depth-1)
	return "---\ntype: Note\npayload: " + strings.Repeat("[", sequenceDepth) + "value" +
		strings.Repeat("]", sequenceDepth) + "\n---\nBody.\n"
}

func a2YAMLPhysicalChain(depth int) *yaml.Node {
	root := a2YAMLString("leaf")
	for range depth {
		root = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{root}}
	}
	return root
}

func a2YAMLWideSequence(nodes int) *yaml.Node {
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for index := 1; index < nodes; index++ {
		root.Content = append(root.Content, a2YAMLString("node"))
	}
	return root
}

func a2YAMLAliasDepth(depth int) *yaml.Node {
	target := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "leaf", Anchor: "depth-target"}
	aliases := make([]*yaml.Node, depth)
	for index := depth - 1; index >= 0; index-- {
		aliases[index] = &yaml.Node{
			Kind: yaml.AliasNode, Anchor: fmt.Sprintf("depth-alias-%d", index),
			Value: target.Anchor, Alias: target,
		}
		target = aliases[index]
	}
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: aliases}
	if depth > 0 {
		root.Content = append(root.Content, aliases[depth-1].Alias)
	} else {
		root.Content = append(root.Content, target)
	}
	return root
}

func a2YAMLGraphEdges(edges int) *yaml.Node {
	anchor := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "base", Anchor: "base"}
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{anchor}}
	aliasCount := (edges - 2) / 2
	for range aliasCount {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.AliasNode, Value: anchor.Anchor, Alias: anchor})
	}
	for current := 1 + 2*aliasCount; current < edges; current++ {
		root.Content = append(root.Content, a2YAMLString("edge padding"))
	}
	return root
}

func a2YAMLSemanticExpansion(over bool) *yaml.Node {
	anchor := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Anchor: "wide"}
	for range 98 {
		anchor.Content = append(anchor.Content, a2YAMLString("node"))
	}
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{anchor}}
	aliasCount := 999
	if over {
		aliasCount++
	}
	for range aliasCount {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.AliasNode, Value: anchor.Anchor, Alias: anchor})
	}
	return root
}

func a2YAMLContentCycle() *yaml.Node {
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	root.Content = []*yaml.Node{root}
	return root
}

func a2YAMLContentReuse() *yaml.Node {
	shared := a2YAMLString("shared")
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{shared, shared}}
}

func a2YAMLAliasCycle() *yaml.Node {
	left := &yaml.Node{Kind: yaml.AliasNode, Anchor: "left", Value: "right"}
	right := &yaml.Node{Kind: yaml.AliasNode, Anchor: "right", Value: "left"}
	left.Alias = right
	right.Alias = left
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{left, right}}
}

func a2YAMLAliasNameMismatch() *yaml.Node {
	target := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value", Anchor: "actual"}
	alias := &yaml.Node{Kind: yaml.AliasNode, Value: "different", Alias: target}
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{target, alias}}
}

func a2YAMLDuplicateAnchor() *yaml.Node {
	left := a2YAMLString("left")
	left.Anchor = "same"
	right := a2YAMLString("right")
	right.Anchor = "same"
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{left, right}}
}

func a2YAMLString(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}
