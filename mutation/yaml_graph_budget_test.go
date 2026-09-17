package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

func TestMutationYAMLGraphResourceBoundaries(t *testing.T) {
	t.Run("physical depth exact and plus one", func(t *testing.T) {
		// Arrange.
		exact := nestedYAMLSequenceGraph(bundle.MaxYAMLPhysicalDepth)
		plusOne := nestedYAMLSequenceGraph(bundle.MaxYAMLPhysicalDepth + 1)

		// Act.
		exactErr := validateMutationYAMLGraphContext(context.Background(), exact)
		plusOneErr := validateMutationYAMLGraphForSourceContext(
			context.Background(),
			plusOne,
			[]byte("deep"),
			SourceSpan{Start: 0, End: 4},
		)

		// Assert.
		if exactErr != nil {
			t.Fatalf("exact physical depth: %v", exactErr)
		}
		assertMutationYAMLResourceError(
			t,
			plusOneErr,
			bundle.YAMLResourcePhysicalDepth,
			bundle.MaxYAMLPhysicalDepth,
			SourceSpan{Start: 0, End: 4},
		)
	})

	t.Run("semantic depth exact and plus one", func(t *testing.T) {
		// Arrange.
		exact := yamlAliasChainGraph(bundle.MaxYAMLSemanticDepth)
		plusOne := yamlAliasChainGraph(bundle.MaxYAMLSemanticDepth + 1)

		// Act.
		exactErr := validateMutationYAMLGraphContext(context.Background(), exact)
		plusOneErr := validateMutationYAMLGraphForSourceContext(
			context.Background(),
			plusOne,
			[]byte("aliases"),
			SourceSpan{Start: 0, End: 7},
		)

		// Assert.
		if exactErr != nil {
			t.Fatalf("exact semantic depth: %v", exactErr)
		}
		assertMutationYAMLResourceError(
			t,
			plusOneErr,
			bundle.YAMLResourceSemanticDepth,
			bundle.MaxYAMLSemanticDepth,
			SourceSpan{Start: 0, End: 7},
		)
	})

	t.Run("graph nodes exact and plus one", func(t *testing.T) {
		// Arrange.
		root := flatYAMLSequenceGraph(bundle.MaxYAMLGraphNodes)

		// Act.
		exactErr := validateMutationYAMLGraphContext(context.Background(), root)
		root.Content = append(root.Content, yamlStringNode("overflow"))
		plusOneErr := validateMutationYAMLGraphForSourceContext(
			context.Background(),
			root,
			[]byte("nodes"),
			SourceSpan{Start: 0, End: 5},
		)

		// Assert.
		if exactErr != nil {
			t.Fatalf("exact graph nodes: %v", exactErr)
		}
		assertMutationYAMLResourceError(
			t,
			plusOneErr,
			bundle.YAMLResourceGraphNodes,
			bundle.MaxYAMLGraphNodes,
			SourceSpan{Start: 0, End: 5},
		)
	})

	t.Run("semantic expansion exact and plus one", func(t *testing.T) {
		// Arrange. One anchored scalar plus 49,999 aliases expands to exactly
		// 100,000 semantic visits while remaining below the node/edge caps.
		root := yamlAliasFanoutGraph((bundle.MaxYAMLSemanticExpansion - 2) / 2)

		// Act.
		exactErr := validateMutationYAMLGraphContext(context.Background(), root)
		root.Content = append(root.Content, yamlStringNode("plus-one"))
		plusOneErr := validateMutationYAMLGraphForSourceContext(
			context.Background(),
			root,
			[]byte("expansion"),
			SourceSpan{Start: 0, End: 9},
		)

		// Assert.
		if exactErr != nil {
			t.Fatalf("exact semantic expansion: %v", exactErr)
		}
		assertMutationYAMLResourceError(
			t,
			plusOneErr,
			bundle.YAMLResourceSemanticExpansion,
			bundle.MaxYAMLSemanticExpansion,
			SourceSpan{Start: 0, End: 9},
		)
	})

	t.Run("graph edges plus one wins before recursive traversal", func(t *testing.T) {
		// Arrange. 50,000 aliases contribute 50,000 Content edges and 50,000
		// Alias edges in addition to the anchored target Content edge.
		root := yamlAliasFanoutGraph(bundle.MaxYAMLGraphEdges / 2)

		// Act.
		err := validateMutationYAMLGraphForSourceContext(
			context.Background(),
			root,
			[]byte("edges"),
			SourceSpan{Start: 0, End: 5},
		)

		// Assert.
		assertMutationYAMLResourceError(
			t,
			err,
			bundle.YAMLResourceGraphEdges,
			bundle.MaxYAMLGraphEdges,
			SourceSpan{Start: 0, End: 5},
		)
	})
}

func TestMutationYAMLGraphIntegrityMappingAtEveryDecodeBoundary(t *testing.T) {
	fixtures := []struct {
		name string
		code bundle.YAMLIntegrityCode
		root func() *yaml.Node
	}{
		{name: "content cycle", code: bundle.YAMLIntegrityContentCycle, root: yamlContentCycleGraph},
		{name: "alias cycle", code: bundle.YAMLIntegrityAliasCycle, root: yamlAliasCycleGraph},
		{name: "dangling alias", code: bundle.YAMLIntegrityAliasDangling, root: yamlDanglingAliasGraph},
		{name: "duplicate anchor", code: bundle.YAMLIntegrityAnchorDuplicate, root: yamlDuplicateAnchorGraph},
		{name: "content reuse", code: bundle.YAMLIntegrityContentReuse, root: yamlContentReuseGraph},
	}
	boundaries := []struct {
		name string
		run  func(context.Context, *yaml.Node, []byte) error
	}{
		{
			name: "main decoder",
			run: func(ctx context.Context, root *yaml.Node, source []byte) error {
				doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
				return mutationYAMLDocumentGraphError(
					validateMutationYAMLGraphContext(ctx, doc),
					source,
					0,
					len(source),
				)
			},
		},
		{
			name: "nested reparse",
			run: func(ctx context.Context, root *yaml.Node, source []byte) error {
				return validateMutationYAMLGraphForSourceContext(
					ctx,
					root,
					source,
					SourceSpan{Start: 1, End: len(source) - 1},
				)
			},
		},
	}

	for _, boundary := range boundaries {
		for _, fixture := range fixtures {
			t.Run(boundary.name+"/"+fixture.name, func(t *testing.T) {
				// Arrange.
				source := []byte("[graph]")
				root := fixture.root()

				// Act.
				err := boundary.run(context.Background(), root, source)

				// Assert.
				assertMutationYAMLIntegrityError(t, err, fixture.code, len(source))
			})
		}
	}
}

func TestMutationYAMLActualDecodersRejectAliasCycles(t *testing.T) {
	t.Run("main decoder", func(t *testing.T) {
		// Arrange.
		source := []byte("---\nloop: &loop [*loop]\n---\nbody\n")

		// Act.
		presentation, err := parsePresentationContext(context.Background(), source)

		// Assert.
		if presentation != nil {
			t.Fatalf("parsePresentationContext() = %#v", presentation)
		}
		assertMutationYAMLIntegrityError(t, err, bundle.YAMLIntegrityAliasCycle, len(source))
	})

	t.Run("nested collection reparse", func(t *testing.T) {
		// Arrange.
		raw := []byte("&loop [*loop]")
		resolver, err := newYAMLResolverContext(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		candidate := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}

		// Act.
		matched, matchErr := resolver.matchesCollection(SourceSpan{Start: 0, End: len(raw)}, candidate)

		// Assert.
		if matched {
			t.Fatal("alias-cycle collection matched")
		}
		assertMutationYAMLIntegrityError(t, matchErr, bundle.YAMLIntegrityAliasCycle, len(raw))
	})
}

func TestMutationYAMLNestedReparseGuardsBeforeSemanticComparison(t *testing.T) {
	t.Run("main decoder resource limit", func(t *testing.T) {
		// Arrange. The root mapping consumes one physical level, so 64 nested
		// sequences observe the public limit plus one.
		nested := strings.Repeat("[", bundle.MaxYAMLPhysicalDepth) + "x" + strings.Repeat("]", bundle.MaxYAMLPhysicalDepth)
		source := []byte("---\nvalue: " + nested + "\n---\nbody\n")

		// Act.
		presentation, err := parsePresentationContext(context.Background(), source)

		// Assert.
		if presentation != nil {
			t.Fatalf("parsePresentationContext() = %#v", presentation)
		}
		var typed *PresentationError
		var resource *bundle.YAMLResourceLimitError
		if !errors.As(err, &typed) || !errors.As(err, &resource) ||
			typed.Code != yamlCodeResourceLimit || typed.Format != "yaml" ||
			resource.Kind != bundle.YAMLResourcePhysicalDepth ||
			resource.Limit != bundle.MaxYAMLPhysicalDepth || resource.Observed != bundle.MaxYAMLPhysicalDepth+1 ||
			typed.Location.Start < 0 || typed.Location.Start >= typed.Location.End || typed.Location.End > len(source) {
			t.Fatalf("main decoder resource error = %#v / %#v / %#v", err, typed, resource)
		}
	})

	t.Run("collection resource limit", func(t *testing.T) {
		// Arrange.
		raw := strings.Repeat("[", bundle.MaxYAMLPhysicalDepth+1) + "x" + strings.Repeat("]", bundle.MaxYAMLPhysicalDepth+1)
		resolver, err := newYAMLResolverContext(context.Background(), []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		candidate := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}

		// Act.
		matched, matchErr := resolver.matchesCollection(SourceSpan{Start: 0, End: len(raw)}, candidate)

		// Assert.
		if matched {
			t.Fatal("resource-limited collection matched")
		}
		assertMutationYAMLResourceError(
			t,
			matchErr,
			bundle.YAMLResourcePhysicalDepth,
			bundle.MaxYAMLPhysicalDepth,
			SourceSpan{Start: 0, End: len(raw)},
		)
	})

	t.Run("scalar probe validates decoded graph", func(t *testing.T) {
		// Arrange.
		resolver, err := newYAMLResolverContext(context.Background(), []byte("value"))
		if err != nil {
			t.Fatal(err)
		}
		node := yamlStringNode("value")

		// Act.
		matched, matchErr := resolver.matchesNode(SourceSpan{Start: 0, End: 5}, node, 0)

		// Assert.
		if matchErr != nil || !matched {
			t.Fatalf("matchesNode() = %t, %v", matched, matchErr)
		}
	})
}

func TestMutationYAMLSyntheticScalarProbeBoundary(t *testing.T) {
	t.Run("block scalar 4096 and 4097", func(t *testing.T) {
		// Arrange.
		exactValue := strings.Repeat("x", maxSyntheticYAMLScalarProbeBytes-len("v: ")-len("\n"))
		plusOneValue := exactValue + "x"

		// Act.
		exactProbe, exactOK := boundedSyntheticYAMLScalarProbe("v: ", exactValue, "\n")
		plusOneProbe, plusOneOK := boundedSyntheticYAMLScalarProbe("v: ", plusOneValue, "\n")
		exactToken, exactTokenErr := yamlScalarTokenContext(context.Background(), exactValue, 0)
		plusOneToken, plusOneTokenErr := yamlScalarTokenContext(context.Background(), plusOneValue, 0)

		// Assert.
		if exactTokenErr != nil || plusOneTokenErr != nil {
			t.Fatalf("scalar token errors = %v, %v", exactTokenErr, plusOneTokenErr)
		}
		if !exactOK || len(exactProbe) != maxSyntheticYAMLScalarProbeBytes || exactToken != exactValue {
			t.Fatalf("exact probe/token = %d, %t, quoted=%t", len(exactProbe), exactOK, strings.HasPrefix(exactToken, "\""))
		}
		if plusOneOK || plusOneProbe != nil || plusOneToken != strconv.Quote(plusOneValue) {
			t.Fatalf("plus-one probe/token = %d, %t, quoted=%t", len(plusOneProbe), plusOneOK, strings.HasPrefix(plusOneToken, "\""))
		}
	})

	t.Run("flow scalar 4096 and 4097", func(t *testing.T) {
		// Arrange.
		exactValue := strings.Repeat("x", maxSyntheticYAMLScalarProbeBytes-len("{v: ")-len("}\n"))
		plusOneValue := exactValue + "x"

		// Act.
		exactProbe, exactOK := boundedSyntheticYAMLScalarProbe("{v: ", exactValue, "}\n")
		plusOneProbe, plusOneOK := boundedSyntheticYAMLScalarProbe("{v: ", plusOneValue, "}\n")
		exactSafe, exactErr := yamlFlowPlainSafeContext(context.Background(), exactValue)
		plusOneRendered, plusOneErr := yamlFlowScalarTokenContext(context.Background(), plusOneValue)

		// Assert.
		if exactErr != nil || plusOneErr != nil {
			t.Fatalf("flow scalar errors = %v, %v", exactErr, plusOneErr)
		}
		if !exactOK || len(exactProbe) != maxSyntheticYAMLScalarProbeBytes || !exactSafe {
			t.Fatalf("exact flow probe = %d, %t, safe=%t", len(exactProbe), exactOK, exactSafe)
		}
		if plusOneOK || plusOneProbe != nil || plusOneRendered != strconv.Quote(plusOneValue) {
			t.Fatalf("plus-one flow probe/render = %d, %t, quoted=%t", len(plusOneProbe), plusOneOK, strings.HasPrefix(plusOneRendered, "\""))
		}
	})
}

func TestMutationYAMLGraphValidationHonorsCancellation(t *testing.T) {
	// Arrange.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := flatYAMLSequenceGraph(1024)

	// Act.
	err := validateMutationYAMLGraphForSourceContext(
		ctx,
		root,
		[]byte("cancel"),
		SourceSpan{Start: 0, End: 6},
	)

	// Assert.
	var presentation *PresentationError
	if !errors.Is(err, context.Canceled) || errors.As(err, &presentation) {
		t.Fatalf("validation error = %#v, PresentationError=%#v", err, presentation)
	}
}

func TestPlannerYAMLGraphFailureReturnsZeroPlanAndStage(t *testing.T) {
	// Arrange. The root mapping plus 64 nested sequences crosses the public
	// physical-depth boundary before a mutation can be staged.
	nested := strings.Repeat("[", bundle.MaxYAMLPhysicalDepth) + "value" + strings.Repeat("]", bundle.MaxYAMLPhysicalDepth)
	data := []byte("---\nvalue: " + nested + "\n---\nbody\n")
	source := memorySource{"a.md": append([]byte(nil), data...)}
	before := append([]byte(nil), source["a.md"]...)
	changeSet := change(t, source, store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "moved/a").ID})

	// Act.
	result, err := Plan(context.Background(), source, changeSet)

	// Assert.
	if err == nil {
		t.Fatal("Plan() accepted a resource-limited YAML graph")
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
		t.Fatalf("rejected Plan() exposed mutation: %#v", result)
	}
	if !bytes.Equal(source["a.md"], before) {
		t.Fatal("rejected Plan() mutated caller source")
	}
}

func TestMutationYAMLDecodeSitesUseTheSharedGraphGuard(t *testing.T) {
	// Arrange. This is an executable same-class inventory: a newly introduced
	// production decoder/reparse site must remain inside the centralized
	// wrappers and validate a successful decode before returning it.
	type decodeSite struct {
		file, function, call, guard string
		line, column                int
	}
	expected := []decodeSite{
		{file: "yaml_decode.go", function: "newGuardedYAMLStreamDecoder", call: "NewDecoder"},
		{file: "yaml_decode.go", function: "decode", call: "Decode", guard: "validateMutationYAMLGraphContext"},
		{file: "yaml_decode.go", function: "decodeGuardedYAMLNodeContext", call: "Unmarshal", guard: "validateMutationYAMLGraphContext"},
	}
	var actual []decodeSite

	// Act.
	productionFiles, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range productionFiles {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fileset := token.NewFileSet()
		file, err := parser.ParseFile(fileset, name, content, 0)
		if err != nil {
			t.Fatal(err)
		}
		yamlAliases := make(map[string]struct{})
		for _, imported := range file.Imports {
			path, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatal(unquoteErr)
			}
			if path != "gopkg.in/yaml.v3" {
				continue
			}
			alias := "yaml"
			if imported.Name != nil {
				alias = imported.Name.Name
			}
			yamlAliases[alias] = struct{}{}
		}
		yamlDecoderFields := make(map[string]map[string]bool)
		isYAMLDecoderType := func(expression ast.Expr) bool {
			pointer, ok := expression.(*ast.StarExpr)
			if !ok {
				return false
			}
			selector, ok := pointer.X.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Decoder" {
				return false
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok {
				return false
			}
			_, imported := yamlAliases[identifier.Name]
			return imported
		}
		for _, declaration := range file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, specification := range generic.Specs {
				typed, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structure, ok := typed.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range structure.Fields.List {
					if !isYAMLDecoderType(field.Type) {
						continue
					}
					if yamlDecoderFields[typed.Name.Name] == nil {
						yamlDecoderFields[typed.Name.Name] = make(map[string]bool)
					}
					for _, fieldName := range field.Names {
						yamlDecoderFields[typed.Name.Name][fieldName.Name] = true
					}
				}
			}
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			body := string(content[fileset.Position(function.Body.Pos()).Offset:fileset.Position(function.Body.End()).Offset])
			decoderVariables := make(map[string]bool)
			for _, fieldList := range []*ast.FieldList{function.Recv, function.Type.Params} {
				if fieldList == nil {
					continue
				}
				for _, field := range fieldList.List {
					if isYAMLDecoderType(field.Type) {
						for _, fieldName := range field.Names {
							decoderVariables[fieldName.Name] = true
						}
					}
				}
			}
			methodReceiverName, methodReceiverType := "", ""
			if function.Recv != nil && len(function.Recv.List) == 1 && len(function.Recv.List[0].Names) == 1 {
				methodReceiverName = function.Recv.List[0].Names[0].Name
				receiverType := function.Recv.List[0].Type
				if pointer, ok := receiverType.(*ast.StarExpr); ok {
					receiverType = pointer.X
				}
				if identifier, ok := receiverType.(*ast.Ident); ok {
					methodReceiverType = identifier.Name
				}
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				declaration, ok := node.(*ast.DeclStmt)
				if !ok {
					return true
				}
				generic, ok := declaration.Decl.(*ast.GenDecl)
				if !ok {
					return true
				}
				for _, specification := range generic.Specs {
					value, ok := specification.(*ast.ValueSpec)
					if !ok || value.Type == nil || !isYAMLDecoderType(value.Type) {
						continue
					}
					for _, variable := range value.Names {
						decoderVariables[variable.Name] = true
					}
				}
				return true
			})
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				rawYAMLCall := false
				if identifier, ok := selector.X.(*ast.Ident); ok {
					_, rawYAMLCall = yamlAliases[identifier.Name]
					rawYAMLCall = rawYAMLCall && (selector.Sel.Name == "Unmarshal" || selector.Sel.Name == "NewDecoder")
				}
				// Resolve Decode only when its receiver is proven to have the
				// imported *yaml.Decoder type, directly or through a typed struct
				// field. Unrelated Decode methods are outside this inventory.
				if selector.Sel.Name == "Decode" {
					switch receiver := selector.X.(type) {
					case *ast.Ident:
						rawYAMLCall = decoderVariables[receiver.Name]
					case *ast.SelectorExpr:
						base, ok := receiver.X.(*ast.Ident)
						rawYAMLCall = ok && base.Name == methodReceiverName && yamlDecoderFields[methodReceiverType][receiver.Sel.Name]
					}
				}
				if !rawYAMLCall {
					return true
				}
				position := fileset.Position(call.Pos())
				guard := ""
				if selector.Sel.Name != "NewDecoder" {
					guard = "validateMutationYAMLGraphContext"
					if decodeAt, guardAt := strings.Index(body, selector.Sel.Name), strings.Index(body, guard); guardAt < decodeAt || decodeAt < 0 {
						t.Errorf("raw YAML call %s:%d:%d is not followed by %s", name, position.Line, position.Column, guard)
					}
				}
				actual = append(actual, decodeSite{
					file: filepath.Base(name), function: function.Name.Name, call: selector.Sel.Name,
					guard: guard, line: position.Line, column: position.Column,
				})
				return true
			})
		}
	}

	// Assert.
	if len(actual) != len(expected) {
		t.Fatalf("YAML decode CallExpr inventory = %#v, want %#v", actual, expected)
	}
	for index, want := range expected {
		got := actual[index]
		if got.file != want.file || got.function != want.function || got.call != want.call || got.guard != want.guard {
			t.Errorf("YAML decode site[%d] = %#v, want %#v", index, got, want)
		}
	}
}

func assertMutationYAMLResourceError(
	t *testing.T,
	err error,
	wantKind bundle.YAMLResourceLimitKind,
	wantLimit int,
	wantLocation SourceSpan,
) {
	t.Helper()
	var presentation *PresentationError
	var resource *bundle.YAMLResourceLimitError
	if !errors.Is(err, ErrUnsupportedPresentation) || !errors.Is(err, bundle.ErrYAMLResourceLimit) ||
		!errors.As(err, &presentation) || !errors.As(err, &resource) {
		t.Fatalf("resource error = %#v", err)
	}
	if presentation.Code != yamlCodeResourceLimit || presentation.Format != "yaml" ||
		presentation.Location != wantLocation || resource.Kind != wantKind ||
		resource.Limit != wantLimit || resource.Observed != wantLimit+1 {
		t.Fatalf("PresentationError/resource = %#v / %#v", presentation, resource)
	}
}

func assertMutationYAMLIntegrityError(t *testing.T, err error, wantCode bundle.YAMLIntegrityCode, sourceLength int) {
	t.Helper()
	var presentation *PresentationError
	var integrity *bundle.YAMLIntegrityError
	if !errors.Is(err, ErrUnsupportedPresentation) || !errors.Is(err, bundle.ErrInvalidYAMLGraph) ||
		!errors.As(err, &presentation) || !errors.As(err, &integrity) {
		t.Fatalf("integrity error = %#v", err)
	}
	if presentation.Code != yamlCodeGraphIntegrity || presentation.Format != "yaml" || integrity.Code != wantCode {
		t.Fatalf("PresentationError/integrity = %#v / %#v", presentation, integrity)
	}
	if presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End || presentation.Location.End > sourceLength {
		t.Fatalf("integrity location = %#v, source length %d", presentation.Location, sourceLength)
	}
}

func nestedYAMLSequenceGraph(depth int) *yaml.Node {
	root := yamlStringNode("leaf")
	for index := 0; index < depth; index++ {
		root = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{root}}
	}
	return root
}

func flatYAMLSequenceGraph(nodeCount int) *yaml.Node {
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	root.Content = make([]*yaml.Node, max(nodeCount-1, 0))
	for index := range root.Content {
		root.Content[index] = yamlStringNode("value")
	}
	return root
}

func yamlAliasChainGraph(depth int) *yaml.Node {
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	target := yamlStringNode("target")
	target.Anchor = fmt.Sprintf("anchor-%d", depth)
	nodes := make([]*yaml.Node, depth+1)
	nodes[depth] = target
	for index := depth - 1; index >= 0; index-- {
		nodes[index] = &yaml.Node{
			Kind:   yaml.AliasNode,
			Anchor: fmt.Sprintf("anchor-%d", index),
			Value:  nodes[index+1].Anchor,
			Alias:  nodes[index+1],
		}
	}
	root.Content = append(root.Content, nodes...)
	return root
}

func yamlAliasFanoutGraph(aliasCount int) *yaml.Node {
	target := yamlStringNode("target")
	target.Anchor = "target"
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{target}}
	for index := 0; index < aliasCount; index++ {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.AliasNode, Value: target.Anchor, Alias: target})
	}
	return root
}

func yamlContentCycleGraph() *yaml.Node {
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	root.Content = []*yaml.Node{root}
	return root
}

func yamlAliasCycleGraph() *yaml.Node {
	left := &yaml.Node{Kind: yaml.AliasNode, Anchor: "left", Value: "right"}
	right := &yaml.Node{Kind: yaml.AliasNode, Anchor: "right", Value: "left"}
	left.Alias = right
	right.Alias = left
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{left, right}}
}

func yamlDanglingAliasGraph() *yaml.Node {
	return &yaml.Node{
		Kind: yaml.SequenceNode,
		Tag:  "!!seq",
		Content: []*yaml.Node{
			{Kind: yaml.AliasNode, Value: "missing"},
		},
	}
}

func yamlDuplicateAnchorGraph() *yaml.Node {
	left := yamlStringNode("left")
	left.Anchor = "duplicate"
	right := yamlStringNode("right")
	right.Anchor = "duplicate"
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{left, right}}
}

func yamlContentReuseGraph() *yaml.Node {
	shared := yamlStringNode("shared")
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{shared, shared}}
}

func yamlStringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}
