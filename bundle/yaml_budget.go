package bundle

import (
	"context"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	// MaxYAMLPhysicalDepth is the maximum number of nested mapping and sequence
	// containers accepted by the YAML graph boundary.
	MaxYAMLPhysicalDepth = 64
	// MaxYAMLSemanticDepth is the maximum number of alias dereferences and
	// direct merge-donor transitions on a semantic path.
	MaxYAMLSemanticDepth = 64
	// MaxYAMLGraphNodes is the maximum number of Content-reachable YAML nodes.
	MaxYAMLGraphNodes = 100_000
	// MaxYAMLGraphEdges is the maximum combined number of Content and Alias
	// edges in a YAML graph.
	MaxYAMLGraphEdges = 100_000
	// MaxYAMLSemanticExpansion caps the multiplicity-aware size of the graph
	// obtained by following both Content and Alias edges.
	MaxYAMLSemanticExpansion = 100_000
)

// ErrYAMLResourceLimit marks a YAML graph that exceeds a public resource cap.
var ErrYAMLResourceLimit = errors.New("YAML resource limit exceeded")

// ErrInvalidYAMLGraph marks a structurally invalid YAML node graph.
var ErrInvalidYAMLGraph = errors.New("invalid YAML graph")

// YAMLResourceLimitKind identifies a bounded YAML graph resource.
type YAMLResourceLimitKind string

const (
	YAMLResourcePhysicalDepth     YAMLResourceLimitKind = "physical_depth"
	YAMLResourceSemanticDepth     YAMLResourceLimitKind = "semantic_depth"
	YAMLResourceGraphNodes        YAMLResourceLimitKind = "graph_nodes"
	YAMLResourceGraphEdges        YAMLResourceLimitKind = "graph_edges"
	YAMLResourceSemanticExpansion YAMLResourceLimitKind = "semantic_expansion"
)

// YAMLResourceLimitError reports the first public YAML resource boundary that
// was crossed. Observed is deliberately saturated at Limit+1.
type YAMLResourceLimitError struct {
	Kind     YAMLResourceLimitKind
	Limit    int
	Observed int
}

func (e *YAMLResourceLimitError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s limit %d (observed %d)", ErrYAMLResourceLimit, e.Kind, e.Limit, e.Observed)
}

// Unwrap preserves both the specific resource identity and the public
// frontmatter parse boundary.
func (e *YAMLResourceLimitError) Unwrap() []error {
	return []error{ErrYAMLResourceLimit, ErrInvalidFrontmatter}
}

// YAMLIntegrityCode is a stable machine-readable YAML graph diagnostic code.
type YAMLIntegrityCode string

const (
	YAMLIntegrityInvalidNodeShape  YAMLIntegrityCode = "yaml_invalid_node_shape"
	YAMLIntegrityContentCycle      YAMLIntegrityCode = "yaml_content_cycle"
	YAMLIntegrityContentReuse      YAMLIntegrityCode = "yaml_content_reuse"
	YAMLIntegrityAliasCycle        YAMLIntegrityCode = "yaml_alias_cycle"
	YAMLIntegrityAliasDangling     YAMLIntegrityCode = "yaml_alias_dangling"
	YAMLIntegrityAliasNameMismatch YAMLIntegrityCode = "yaml_alias_name_mismatch"
	YAMLIntegrityAnchorDuplicate   YAMLIntegrityCode = "yaml_anchor_duplicate"
)

// YAMLIntegrityError reports a malformed YAML node graph. Anchor is populated
// when an anchor or alias identifies the failing edge.
type YAMLIntegrityError struct {
	Code   YAMLIntegrityCode
	Anchor string
}

func (e *YAMLIntegrityError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Anchor == "" {
		return fmt.Sprintf("%s: %s", ErrInvalidYAMLGraph, e.Code)
	}
	return fmt.Sprintf("%s: %s (%q)", ErrInvalidYAMLGraph, e.Code, e.Anchor)
}

// Unwrap preserves both the graph-integrity identity and the public
// frontmatter parse boundary.
func (e *YAMLIntegrityError) Unwrap() []error {
	return []error{ErrInvalidYAMLGraph, ErrInvalidFrontmatter}
}

type yamlVisitColor uint8

const (
	yamlVisitGray yamlVisitColor = iota + 1
	yamlVisitBlack
)

type yamlContentFrame struct {
	node          *yaml.Node
	physicalDepth int
	exit          bool
}

type yamlGraphAnalysis struct {
	root                *yaml.Node
	nodes               []*yaml.Node
	topological         []*yaml.Node
	aliases             []*yaml.Node
	directMergeDonor    map[*yaml.Node]bool
	contentEdgeCount    int
	combinedEdgeCount   int
	contentVisits       int
	semanticVisits      int
	expansionVisits     int
	semanticEdgeVisits  int
	expansionEdgeVisits int
}

type yamlGraphLimits struct {
	physicalDepth     int
	semanticDepth     int
	graphNodes        int
	graphEdges        int
	semanticExpansion int
}

var publicYAMLGraphLimits = yamlGraphLimits{
	physicalDepth:     MaxYAMLPhysicalDepth,
	semanticDepth:     MaxYAMLSemanticDepth,
	graphNodes:        MaxYAMLGraphNodes,
	graphEdges:        MaxYAMLGraphEdges,
	semanticExpansion: MaxYAMLSemanticExpansion,
}

// ValidateYAMLNode validates the complete Content-reachable YAML graph using
// the public integrity and resource limits.
func ValidateYAMLNode(root *yaml.Node) error {
	return ValidateYAMLNodeContext(context.Background(), root)
}

// ValidateYAMLNodeContext is the cancellable form of ValidateYAMLNode.
func ValidateYAMLNodeContext(ctx context.Context, root *yaml.Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	analysis, err := analyzeYAMLGraphContext(ctx, root, false, publicYAMLGraphLimits)
	if err != nil {
		return err
	}
	return validateYAMLAnalysisContext(ctx, analysis, publicYAMLGraphLimits)
}

// validateYAMLSubgraphContext validates a raw semantic subgraph. Unlike the
// full-document boundary, an alias target may be owned outside root's Content
// tree; the target and its Content tree are therefore included through the
// transitive Alias closure.
func validateYAMLSubgraphContext(ctx context.Context, root *yaml.Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	analysis, err := analyzeYAMLGraphContext(ctx, root, true, publicYAMLGraphLimits)
	if err != nil {
		return err
	}
	return validateYAMLAnalysisContext(ctx, analysis, publicYAMLGraphLimits)
}

func validateYAMLNodeWithLimitsContext(
	ctx context.Context,
	root *yaml.Node,
	includeAliasClosure bool,
	limits yamlGraphLimits,
) (*yamlGraphAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	analysis, err := analyzeYAMLGraphContext(ctx, root, includeAliasClosure, limits)
	if err != nil {
		return nil, err
	}
	if err := validateYAMLAnalysisContext(ctx, analysis, limits); err != nil {
		return analysis, err
	}
	return analysis, nil
}

func validateYAMLAnalysisContext(ctx context.Context, analysis *yamlGraphAnalysis, limits yamlGraphLimits) error {
	if err := validateYAMLSemanticDepthContext(ctx, analysis, limits); err != nil {
		return err
	}
	if err := validateYAMLSemanticExpansionContext(ctx, analysis, limits); err != nil {
		return err
	}
	return ctx.Err()
}

func analyzeYAMLGraphContext(
	ctx context.Context,
	root *yaml.Node,
	includeAliasClosure bool,
	limits yamlGraphLimits,
) (*yamlGraphAnalysis, error) {
	analysisRoot, err := yamlBudgetRoot(root)
	if err != nil {
		return nil, err
	}
	analysis := &yamlGraphAnalysis{
		root:             analysisRoot,
		directMergeDonor: make(map[*yaml.Node]bool),
	}
	colors := make(map[*yaml.Node]yamlVisitColor)
	anchors := make(map[string]*yaml.Node)
	contentReuse := false
	contentReuseAnchor := ""
	contentRoots := []*yaml.Node{analysisRoot}
	aliasCursor := 0
	for len(contentRoots) != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lastRoot := len(contentRoots) - 1
		contentRoot := contentRoots[lastRoot]
		contentRoots = contentRoots[:lastRoot]
		if colors[contentRoot] == yamlVisitBlack {
			continue
		}

		stack := []yamlContentFrame{{node: contentRoot}}
		for len(stack) != 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			last := len(stack) - 1
			frame := stack[last]
			stack = stack[:last]
			if frame.exit {
				colors[frame.node] = yamlVisitBlack
				continue
			}
			analysis.contentVisits++

			if frame.node == nil {
				return nil, yamlIntegrityError(YAMLIntegrityInvalidNodeShape, "")
			}
			switch colors[frame.node] {
			case yamlVisitGray:
				return nil, yamlIntegrityError(YAMLIntegrityContentCycle, frame.node.Anchor)
			case yamlVisitBlack:
				contentReuse = true
				if contentReuseAnchor == "" {
					contentReuseAnchor = frame.node.Anchor
				}
				continue
			}
			if err := validateYAMLNodeShape(frame.node); err != nil {
				return nil, err
			}

			depth := frame.physicalDepth
			if frame.node.Kind == yaml.MappingNode || frame.node.Kind == yaml.SequenceNode {
				depth++
				if depth > limits.physicalDepth {
					return nil, yamlResourceLimitError(YAMLResourcePhysicalDepth, limits.physicalDepth)
				}
			}

			colors[frame.node] = yamlVisitGray
			analysis.nodes = append(analysis.nodes, frame.node)
			if len(analysis.nodes) > limits.graphNodes {
				return nil, yamlResourceLimitError(YAMLResourceGraphNodes, limits.graphNodes)
			}
			if frame.node.Anchor != "" {
				if previous := anchors[frame.node.Anchor]; previous != nil && previous != frame.node {
					return nil, yamlIntegrityError(YAMLIntegrityAnchorDuplicate, frame.node.Anchor)
				}
				anchors[frame.node.Anchor] = frame.node
			}
			if frame.node.Kind == yaml.AliasNode {
				analysis.aliases = append(analysis.aliases, frame.node)
			}
			if err := markYAMLDirectMergeDonorsContext(ctx, frame.node, analysis.directMergeDonor); err != nil {
				return nil, err
			}

			analysis.contentEdgeCount = saturatingYAMLAdd(analysis.contentEdgeCount, len(frame.node.Content), limits.graphEdges)
			if analysis.contentEdgeCount > limits.graphEdges {
				return nil, yamlResourceLimitError(YAMLResourceGraphEdges, limits.graphEdges)
			}

			stack = append(stack, yamlContentFrame{node: frame.node, exit: true})
			for index := len(frame.node.Content) - 1; index >= 0; index-- {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				stack = append(stack, yamlContentFrame{
					node:          frame.node.Content[index],
					physicalDepth: depth,
				})
			}
		}

		if includeAliasClosure {
			for aliasCursor < len(analysis.aliases) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				alias := analysis.aliases[aliasCursor]
				aliasCursor++
				if alias.Alias == nil || alias.Alias.Anchor == "" {
					return nil, yamlIntegrityError(YAMLIntegrityAliasDangling, yamlAliasDiagnosticAnchor(alias))
				}
				if colors[alias.Alias] == 0 {
					contentRoots = append(contentRoots, alias.Alias)
				}
			}
		}
	}
	if contentReuse {
		return nil, yamlIntegrityError(YAMLIntegrityContentReuse, contentReuseAnchor)
	}

	for _, alias := range analysis.aliases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		target := alias.Alias
		if target == nil || colors[target] != yamlVisitBlack || target.Anchor == "" {
			return nil, yamlIntegrityError(YAMLIntegrityAliasDangling, yamlAliasDiagnosticAnchor(alias))
		}
		if alias.Value != "" && alias.Value != target.Anchor {
			return nil, yamlIntegrityError(YAMLIntegrityAliasNameMismatch, alias.Value)
		}
		if anchors[target.Anchor] != target {
			return nil, yamlIntegrityError(YAMLIntegrityAliasDangling, target.Anchor)
		}
	}

	analysis.combinedEdgeCount = saturatingYAMLAdd(analysis.contentEdgeCount, len(analysis.aliases), limits.graphEdges)
	if analysis.combinedEdgeCount > limits.graphEdges {
		return nil, yamlResourceLimitError(YAMLResourceGraphEdges, limits.graphEdges)
	}

	topological, err := yamlTopologicalOrderContext(ctx, analysis.nodes)
	if err != nil {
		return nil, err
	}
	analysis.topological = topological
	return analysis, nil
}

func yamlBudgetRoot(root *yaml.Node) (*yaml.Node, error) {
	if root == nil {
		return nil, yamlIntegrityError(YAMLIntegrityInvalidNodeShape, "")
	}
	if root.Kind != yaml.DocumentNode {
		return root, nil
	}
	if err := validateYAMLNodeShape(root); err != nil {
		return nil, err
	}
	return root.Content[0], nil
}

func validateYAMLNodeShape(node *yaml.Node) error {
	valid := false
	switch node.Kind {
	case yaml.DocumentNode:
		valid = len(node.Content) == 1 && node.Alias == nil
	case yaml.MappingNode:
		valid = len(node.Content)%2 == 0 && node.Alias == nil
	case yaml.SequenceNode:
		valid = node.Alias == nil
	case yaml.ScalarNode:
		valid = len(node.Content) == 0 && node.Alias == nil
	case yaml.AliasNode:
		valid = len(node.Content) == 0
	}
	if !valid {
		return yamlIntegrityError(YAMLIntegrityInvalidNodeShape, node.Anchor)
	}
	return nil
}

func markYAMLDirectMergeDonorsContext(ctx context.Context, node *yaml.Node, direct map[*yaml.Node]bool) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !isMergeKey(node.Content[index]) {
			continue
		}
		value := node.Content[index+1]
		if value == nil {
			continue
		}
		switch value.Kind {
		case yaml.MappingNode:
			direct[value] = true
		case yaml.SequenceNode:
			for _, donor := range value.Content {
				if err := ctx.Err(); err != nil {
					return err
				}
				if donor != nil && donor.Kind == yaml.MappingNode {
					direct[donor] = true
				}
			}
		}
	}
	return nil
}

func yamlAliasDiagnosticAnchor(alias *yaml.Node) string {
	if alias == nil {
		return ""
	}
	if alias.Value != "" {
		return alias.Value
	}
	if alias.Alias != nil {
		return alias.Alias.Anchor
	}
	return ""
}

func yamlTopologicalOrderContext(ctx context.Context, nodes []*yaml.Node) ([]*yaml.Node, error) {
	indegree := make(map[*yaml.Node]int, len(nodes))
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, child := range node.Content {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			indegree[child]++
		}
		if node.Kind == yaml.AliasNode {
			indegree[node.Alias]++
		}
	}

	queue := make([]*yaml.Node, 0, len(nodes))
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if indegree[node] == 0 {
			queue = append(queue, node)
		}
	}

	order := make([]*yaml.Node, 0, len(nodes))
	for head := 0; head < len(queue); head++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		node := queue[head]
		order = append(order, node)
		for _, child := range node.Content {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
			}
		}
		if node.Kind == yaml.AliasNode {
			child := node.Alias
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	if len(order) != len(nodes) {
		return nil, yamlIntegrityError(YAMLIntegrityAliasCycle, "")
	}
	return order, nil
}

func validateYAMLSemanticDepthContext(
	ctx context.Context,
	analysis *yamlGraphAnalysis,
	limits yamlGraphLimits,
) error {
	depth := make(map[*yaml.Node]int, len(analysis.nodes))
	for _, node := range analysis.topological {
		if err := ctx.Err(); err != nil {
			return err
		}
		analysis.semanticVisits++
		base := depth[node]
		for _, child := range node.Content {
			if err := ctx.Err(); err != nil {
				return err
			}
			analysis.semanticEdgeVisits++
			weight := 0
			if analysis.directMergeDonor[child] {
				weight = 1
			}
			if err := updateYAMLSemanticDepth(depth, child, base+weight, limits.semanticDepth); err != nil {
				return err
			}
		}
		if node.Kind == yaml.AliasNode {
			analysis.semanticEdgeVisits++
			if err := updateYAMLSemanticDepth(depth, node.Alias, base+1, limits.semanticDepth); err != nil {
				return err
			}
		}
	}
	return nil
}

func updateYAMLSemanticDepth(depth map[*yaml.Node]int, node *yaml.Node, candidate, limit int) error {
	if candidate > limit {
		return yamlResourceLimitError(YAMLResourceSemanticDepth, limit)
	}
	if candidate > depth[node] {
		depth[node] = candidate
	}
	return nil
}

func validateYAMLSemanticExpansionContext(
	ctx context.Context,
	analysis *yamlGraphAnalysis,
	limits yamlGraphLimits,
) error {
	expansion := make(map[*yaml.Node]int, len(analysis.nodes))
	for index := len(analysis.topological) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		node := analysis.topological[index]
		analysis.expansionVisits++
		total := 1
		for _, child := range node.Content {
			if err := ctx.Err(); err != nil {
				return err
			}
			analysis.expansionEdgeVisits++
			total = saturatingYAMLAdd(total, expansion[child], limits.semanticExpansion)
		}
		if node.Kind == yaml.AliasNode {
			analysis.expansionEdgeVisits++
			total = saturatingYAMLAdd(total, expansion[node.Alias], limits.semanticExpansion)
		}
		expansion[node] = total
	}
	if expansion[analysis.root] > limits.semanticExpansion {
		return yamlResourceLimitError(YAMLResourceSemanticExpansion, limits.semanticExpansion)
	}
	return nil
}

func saturatingYAMLAdd(current, increment, limit int) int {
	if current > limit || increment > limit-current {
		return limit + 1
	}
	return current + increment
}

func yamlResourceLimitError(kind YAMLResourceLimitKind, limit int) error {
	return &YAMLResourceLimitError{Kind: kind, Limit: limit, Observed: limit + 1}
}

func yamlIntegrityError(code YAMLIntegrityCode, anchor string) error {
	return &YAMLIntegrityError{Code: code, Anchor: anchor}
}
