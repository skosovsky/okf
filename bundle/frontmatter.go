package bundle

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Frontmatter stores OKF YAML frontmatter as an order-preserving YAML mapping.
type Frontmatter struct {
	node yaml.Node
	raw  []byte
}

// ScalarValueState preserves presence and raw scalar text while exposing a
// typed string only for a correctly tagged, non-empty YAML string.
type ScalarValueState struct {
	Present bool
	Valid   bool
	Raw     string
	Value   string
}

// StringListState preserves the shape and per-item validity of an optional
// sequence of YAML strings.
type StringListState struct {
	Present bool
	Valid   bool
	Items   []ScalarValueState
}

// SemanticValueState preserves semantic key presence separately from a
// resolvable value. Duplicate explicit keys and values inherited through
// multiple physical merge keys are ambiguous: they remain in the raw YAML
// graph, but typed consumers must not silently select one.
type SemanticValueState struct {
	Present   bool
	Ambiguous bool
	Value     *yaml.Node
}

// NewFrontmatter creates an empty frontmatter mapping.
func NewFrontmatter() Frontmatter {
	return Frontmatter{node: newMappingNode()}
}

// NewFrontmatterFromNode creates frontmatter from a YAML node.
//
// A document node is unwrapped. A null or zero node becomes an empty mapping.
// A non-mapping value is rejected because OKF frontmatter is always a mapping.
func NewFrontmatterFromNode(node *yaml.Node) (Frontmatter, error) {
	return NewFrontmatterFromNodeContext(context.Background(), node)
}

// NewFrontmatterFromNodeContext is the cancellation-aware form of
// NewFrontmatterFromNode. It validates the complete caller-owned YAML graph
// before cloning or retaining any part of it.
func NewFrontmatterFromNodeContext(ctx context.Context, node *yaml.Node) (Frontmatter, error) {
	if err := ctx.Err(); err != nil {
		return Frontmatter{}, err
	}
	if node == nil || node.Kind == 0 || isNullNode(node) {
		return NewFrontmatter(), nil
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return NewFrontmatter(), nil
		}
		if len(node.Content) != 1 {
			return Frontmatter{}, yamlIntegrityError(YAMLIntegrityInvalidNodeShape, node.Anchor)
		}
		if node.Content[0] == node {
			return Frontmatter{}, yamlIntegrityError(YAMLIntegrityContentCycle, node.Anchor)
		}
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return Frontmatter{}, fmt.Errorf("%w: expected YAML mapping", ErrInvalidFrontmatter)
	}
	if err := ValidateYAMLNodeContext(ctx, node); err != nil {
		return Frontmatter{}, err
	}
	cloned := cloneYAMLNode(node)
	if err := ctx.Err(); err != nil {
		return Frontmatter{}, err
	}
	return Frontmatter{node: cloned}, nil
}

// ParseFrontmatter parses a YAML frontmatter mapping.
func ParseFrontmatter(text string) (Frontmatter, error) {
	return ParseFrontmatterContext(context.Background(), text)
}

// ParseFrontmatterContext is the cancellation-aware form of ParseFrontmatter.
func ParseFrontmatterContext(ctx context.Context, text string) (Frontmatter, error) {
	if err := ctx.Err(); err != nil {
		return Frontmatter{}, err
	}
	// yaml.v3 accepts malformed UTF-8 after normalizing it. Frontmatter is a
	// public text format, therefore parsing must preserve the encoding boundary.
	valid, err := validUTF8StringContext(ctx, text)
	if err != nil {
		return Frontmatter{}, err
	}
	if !valid {
		return Frontmatter{}, fmt.Errorf("%w: invalid UTF-8", ErrInvalidEncoding)
	}

	blank, err := blankStringContext(ctx, text)
	if err != nil {
		return Frontmatter{}, err
	}
	if blank {
		return NewFrontmatter(), nil
	}

	var node yaml.Node
	decoder := yaml.NewDecoder(&contextStringReader{ctx: ctx, value: text})
	if err := decoder.Decode(&node); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Frontmatter{}, ctxErr
		}
		return Frontmatter{}, fmt.Errorf("%w: %v", ErrInvalidFrontmatter, err)
	}
	if err := ctx.Err(); err != nil {
		return Frontmatter{}, err
	}

	frontmatter, err := NewFrontmatterFromNodeContext(ctx, &node)
	if err != nil {
		return Frontmatter{}, err
	}
	frontmatter.raw, err = bytesFromStringContext(ctx, text)
	if err != nil {
		return Frontmatter{}, err
	}
	return frontmatter, nil
}

func validUTF8StringContext(ctx context.Context, value string) (bool, error) {
	nextCheck := 0
	for offset := 0; offset < len(value); {
		if offset >= nextCheck {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			nextCheck = offset + contextStringChunkSize
		}
		_, size := utf8.DecodeRuneInString(value[offset:])
		if size == 1 && value[offset] >= utf8.RuneSelf {
			return false, nil
		}
		offset += size
	}
	return true, ctx.Err()
}

func blankStringContext(ctx context.Context, value string) (bool, error) {
	nextCheck := 0
	for offset := 0; offset < len(value); {
		if offset >= nextCheck {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			nextCheck = offset + contextStringChunkSize
		}
		current, size := utf8.DecodeRuneInString(value[offset:])
		if !unicode.IsSpace(current) {
			return false, nil
		}
		offset += size
	}
	return true, ctx.Err()
}

type contextStringReader struct {
	ctx    context.Context
	value  string
	offset int
}

func (r *contextStringReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.offset >= len(r.value) {
		return 0, io.EOF
	}
	limit := min(len(data), contextStringChunkSize, len(r.value)-r.offset)
	written := copy(data[:limit], r.value[r.offset:r.offset+limit])
	r.offset += written
	if err := r.ctx.Err(); err != nil {
		return written, err
	}
	return written, nil
}

func bytesFromStringContext(ctx context.Context, value string) ([]byte, error) {
	out := make([]byte, 0, len(value))
	for offset := 0; offset < len(value); offset += contextStringChunkSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(offset+contextStringChunkSize, len(value))
		out = append(out, value[offset:end]...)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// IsEmpty reports whether the frontmatter has no keys.
func (f Frontmatter) IsEmpty() bool {
	node := f.mappingNode()
	return len(node.Content) == 0
}

// YAMLString serializes frontmatter as a YAML mapping.
func (f Frontmatter) YAMLString() (string, error) {
	return f.YAMLStringContext(context.Background())
}

// YAMLStringContext is the cancellation-aware form of YAMLString. Retained
// raw bytes and encoder output cross a chunked context writer; cancellation or
// graph/encoder failure returns no partial string.
func (f Frontmatter) YAMLStringContext(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	node := f.mappingNode()
	if err := ValidateYAMLNodeContext(ctx, &node); err != nil {
		return "", err
	}
	if f.raw != nil {
		return stringFromBytesContext(ctx, f.raw)
	}
	cloned, err := cloneYAMLNodeContext(ctx, &node)
	if err != nil {
		return "", err
	}
	if len(cloned.Content) == 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", nil
	}
	return encodeYAMLNodeStringContext(
		ctx,
		&cloned,
		func(writer io.Writer) yamlStringEncoder { return yaml.NewEncoder(writer) },
	)
}

type yamlStringEncoder interface {
	SetIndent(int)
	Encode(any) error
	Close() error
}

type yamlStringEncoderFactory func(io.Writer) yamlStringEncoder

func encodeYAMLNodeStringContext(
	ctx context.Context,
	node *yaml.Node,
	newEncoder yamlStringEncoderFactory,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	writer := &contextChunkWriter{ctx: ctx}
	encoder := newEncoder(writer)
	encoder.SetIndent(2)
	encodeErr := encoder.Encode(node)
	encodeWriterErr := writer.err
	closeErr := encoder.Close()

	// A writer error is the concrete streaming failure and takes precedence
	// over an encoder wrapper error from the same phase. Otherwise the encode
	// operation precedes close, and close precedes trailing cancellation.
	if encodeWriterErr != nil {
		return "", encodeWriterErr
	}
	if encodeErr != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidFrontmatter, encodeErr)
	}
	if writer.err != nil {
		return "", writer.err
	}
	if closeErr != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidFrontmatter, closeErr)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	encoded, err := writer.StringContext(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(encoded, "...\n"), nil
}

const contextStringChunkSize = 64 << 10

type contextChunkWriter struct {
	ctx    context.Context
	chunks [][]byte
	total  int
	err    error
}

func (w *contextChunkWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return 0, err
	}
	written := 0
	for offset := 0; offset < len(data); offset += contextStringChunkSize {
		if err := w.ctx.Err(); err != nil {
			w.err = err
			return written, err
		}
		end := min(offset+contextStringChunkSize, len(data))
		chunk := append([]byte(nil), data[offset:end]...)
		w.chunks = append(w.chunks, chunk)
		w.total += len(chunk)
		written += len(chunk)
	}
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return written, err
	}
	return written, nil
}

func (w *contextChunkWriter) StringContext(ctx context.Context) (string, error) {
	if w.err != nil {
		return "", w.err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(w.total)
	for _, chunk := range w.chunks {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if _, err := out.Write(chunk); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func stringFromBytesContext(ctx context.Context, data []byte) (string, error) {
	writer := &contextChunkWriter{ctx: ctx}
	if _, err := writer.Write(data); err != nil {
		return "", err
	}
	return writer.StringContext(ctx)
}

func stringFromStringContext(ctx context.Context, value string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(len(value))
	for offset := 0; offset < len(value); offset += contextStringChunkSize {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := min(offset+contextStringChunkSize, len(value))
		if _, err := out.WriteString(value[offset:end]); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// YAMLNode returns a copy of the underlying YAML mapping node.
func (f Frontmatter) YAMLNode() *yaml.Node {
	node, _ := f.YAMLNodeContext(context.Background())
	return node
}

// YAMLNodeContext is the cancellation-aware form of YAMLNode. Cancellation
// returns no partial graph and the successful result shares no mutable YAML
// nodes or Content slices with the retained frontmatter.
func (f Frontmatter) YAMLNodeContext(ctx context.Context) (*yaml.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	node := f.mappingNode()
	cloned, err := cloneYAMLNodeContext(ctx, &node)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &cloned, nil
}

// Get returns the YAML value node for a string key.
func (f Frontmatter) Get(key string) (*yaml.Node, bool) {
	value, ok, _ := f.GetContext(context.Background(), key)
	return value, ok
}

// GetContext is the cancellation-aware direct-key lookup. It preserves Get's
// physical first-key semantics: duplicate keys select the first occurrence and
// merge donors are not consulted. Cancellation returns no partial node.
func (f Frontmatter) GetContext(ctx context.Context, key string) (*yaml.Node, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	node := f.mappingNode()
	value, present, err := directMappingValueContext(ctx, &node, key)
	if err != nil {
		return nil, false, err
	}
	if !present {
		return nil, false, nil
	}
	cloned, err := cloneYAMLNodeContext(ctx, value)
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return &cloned, true, nil
}

func directMappingValueContext(
	ctx context.Context,
	node *yaml.Node,
	key string,
) (*yaml.Node, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, false, nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		candidate := node.Content[index]
		if candidate == nil || candidate.Kind != yaml.ScalarNode {
			continue
		}
		equal, err := equalStringContext(ctx, candidate.Value, key)
		if err != nil {
			return nil, false, err
		}
		if equal {
			return node.Content[index+1], true, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

func equalStringContext(ctx context.Context, left, right string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(left) != len(right) {
		return false, nil
	}
	const chunkSize = 64 << 10
	for offset := 0; offset < len(left); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		end := min(offset+chunkSize, len(left))
		if left[offset:end] != right[offset:end] {
			return false, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}

// SemanticGet resolves a key using YAML mapping/alias/merge semantics while
// returning an owned node graph. Explicit keys override merged keys; for a
// merge sequence, earlier mappings take precedence. Cycles fail closed.
func (f Frontmatter) SemanticGet(key string) (*yaml.Node, bool) {
	value, present, _ := f.SemanticGetContext(context.Background(), key)
	return value, present
}

// SemanticGetContext is the cancellation-aware semantic lookup. It preserves
// alias, merge, precedence, and ambiguity semantics while returning an owned
// resolved graph. Cancellation returns no partial node.
func (f Frontmatter) SemanticGetContext(ctx context.Context, key string) (*yaml.Node, bool, error) {
	state, err := f.SemanticValueStateContext(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if !state.Present {
		return nil, false, nil
	}
	if state.Value == nil {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		return &yaml.Node{}, true, nil
	}
	return state.Value, true, nil
}

// SemanticValueState resolves a key using YAML mapping/alias/merge semantics
// while preserving duplicate-key ambiguity.
func (f Frontmatter) SemanticValueState(key string) SemanticValueState {
	state, _ := f.SemanticValueStateContext(context.Background(), key)
	return state
}

// SemanticValueStateContext is the cancellation-aware retained-root semantic
// observation. The Frontmatter construction boundary has already validated
// the full graph, so lookup reuses the shared semantic resolver without a
// duplicate raw-subgraph validation pass.
func (f Frontmatter) SemanticValueStateContext(ctx context.Context, key string) (SemanticValueState, error) {
	if err := ctx.Err(); err != nil {
		return SemanticValueState{}, err
	}
	node := f.mappingNode()
	selection, err := newYAMLSemanticResolver(ctx).mappingValue(&node, key)
	if err != nil {
		return SemanticValueState{}, err
	}
	state := SemanticValueState{
		Present:   selection.present,
		Ambiguous: selection.ambiguous,
	}
	if selection.resolved != nil {
		cloned, cloneErr := cloneYAMLNodeContext(ctx, selection.resolved)
		if cloneErr != nil {
			return SemanticValueState{}, cloneErr
		}
		state.Value = &cloned
	}
	if err := ctx.Err(); err != nil {
		return SemanticValueState{}, err
	}
	return state, nil
}

// SemanticMappingValue resolves one mapping key through aliases and YAML merge
// keys and returns a defensive graph copy. It is cycle-safe and leaves the raw
// caller-owned graph untouched.
func SemanticMappingValue(node *yaml.Node, key string) (*yaml.Node, bool) {
	value, present, _ := SemanticMappingValueContext(context.Background(), node, key)
	return value, present
}

// SemanticMappingValueContext is the cancellation-aware raw-node semantic
// lookup. Arbitrary caller graphs are validated as closed subgraphs before
// traversal; errors return no partial value.
func SemanticMappingValueContext(ctx context.Context, node *yaml.Node, key string) (*yaml.Node, bool, error) {
	state, err := ObserveSemanticMappingValueContext(ctx, node, key)
	if err != nil {
		return nil, false, err
	}
	if !state.Present {
		return nil, false, nil
	}
	if state.Value == nil {
		return &yaml.Node{}, true, nil
	}
	return state.Value, true, nil
}

// ObserveSemanticMappingValue resolves one mapping key while distinguishing
// absence from duplicate explicit keys and other unresolved present values.
// Value, when non-nil, is a defensive graph copy.
func ObserveSemanticMappingValue(node *yaml.Node, key string) SemanticValueState {
	state, _ := ObserveSemanticMappingValueContext(context.Background(), node, key)
	return state
}

// ObserveSemanticMappingValueContext is the cancellation-aware raw-node
// observation surface. Validation and cancellation errors return zero state.
func ObserveSemanticMappingValueContext(ctx context.Context, node *yaml.Node, key string) (SemanticValueState, error) {
	if err := ctx.Err(); err != nil {
		return SemanticValueState{}, err
	}
	if err := validateYAMLSubgraphContext(ctx, node); err != nil {
		return SemanticValueState{}, err
	}
	state := semanticMappingValueState(node, key, make(map[*yaml.Node]bool))
	if err := ctx.Err(); err != nil {
		return SemanticValueState{}, err
	}
	if state.Value != nil {
		cloned := cloneYAMLNode(state.Value)
		state.Value = &cloned
	}
	if err := ctx.Err(); err != nil {
		return SemanticValueState{}, err
	}
	return state, nil
}

// SemanticNode dereferences a terminal YAML alias chain and returns an owned
// graph. Alias cycles fail closed.
func SemanticNode(node *yaml.Node) (*yaml.Node, bool) {
	value, ok, _ := SemanticNodeContext(context.Background(), node)
	return value, ok
}

// SemanticNodeContext validates and dereferences a raw terminal alias chain.
// Errors and unresolved values return no partial graph.
func SemanticNodeContext(ctx context.Context, node *yaml.Node) (*yaml.Node, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := validateYAMLSubgraphContext(ctx, node); err != nil {
		return nil, false, err
	}
	return semanticNodeValidatedContext(ctx, node)
}

func semanticNodeValidatedContext(ctx context.Context, node *yaml.Node) (*yaml.Node, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	resolved, err := newYAMLSemanticResolver(ctx).value(node)
	if err != nil {
		return nil, false, err
	}
	if !resolved.resolved || resolved.node == nil {
		return nil, false, ctx.Err()
	}
	cloned, err := cloneYAMLNodeContext(ctx, resolved.node)
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return &cloned, true, nil
}

// Set stores a raw YAML value for a string key.
//
// If the key already exists, its value is replaced without moving the key.
func (f *Frontmatter) Set(key string, value *yaml.Node) error {
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidFrontmatter)
	}
	// Frontmatter values are intentionally cheap to copy. Detach the complete
	// YAML graph before mutation so a Concept or Document returned from an
	// immutable Bundle cannot mutate the bundle's retained model through shared
	// node pointers or Content slices.
	candidate := cloneYAMLNode(&f.node)
	if candidate.Kind != yaml.MappingNode {
		candidate = newMappingNode()
	}

	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
	if value != nil {
		valueNode = value
	}

	replaced := false
	for i := 0; i+1 < len(candidate.Content); i += 2 {
		if candidate.Content[i].Kind == yaml.ScalarNode && candidate.Content[i].Value == key {
			candidate.Content[i+1] = valueNode
			replaced = true
			break
		}
	}
	if !replaced {
		candidate.Content = append(candidate.Content, keyNode, valueNode)
	}
	if err := ValidateYAMLNode(&candidate); err != nil {
		return err
	}
	f.node = cloneYAMLNode(&candidate)
	f.raw = nil
	return nil
}

// SetString stores a string scalar for a frontmatter key.
func (f *Frontmatter) SetString(key, value string) error {
	return f.Set(key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// Keys returns string keys in source/insertion order.
func (f Frontmatter) Keys() []string {
	keys, _ := f.KeysContext(context.Background())
	return keys
}

// KeysContext is the cancellation-aware physical key projection. It preserves
// source order and duplicates, includes every scalar key regardless of tag,
// and skips complex/non-scalar keys exactly like Keys. Cancellation returns no
// partial slice.
func (f Frontmatter) KeysContext(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	node := f.mappingNode()
	var keys []string
	for i := 0; i+1 < len(node.Content); i += 2 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := node.Content[i]
		if key == nil || key.Kind != yaml.ScalarNode {
			continue
		}
		owned, err := stringFromStringContext(ctx, key.Value)
		if err != nil {
			return nil, err
		}
		keys = append(keys, owned)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

// Type returns the required OKF type field.
func (f Frontmatter) Type() (string, bool) {
	value, valid, _ := f.TypeContext(context.Background())
	return value, valid
}

// TypeContext is the cancellation-aware required type projection.
func (f Frontmatter) TypeContext(ctx context.Context) (string, bool, error) {
	state, err := f.semanticStringStateContext(ctx, "type")
	if err != nil {
		return "", false, err
	}
	return state.Value, state.Valid, nil
}

// Title returns the optional display title field.
func (f Frontmatter) Title() (string, bool) {
	value, valid, _ := f.TitleContext(context.Background())
	return value, valid
}

// TitleContext is the cancellation-aware optional title projection.
func (f Frontmatter) TitleContext(ctx context.Context) (string, bool, error) {
	state, err := f.TitleStateContext(ctx)
	if err != nil {
		return "", false, err
	}
	return state.Value, state.Valid, nil
}

// TitleState returns strict optional title state without scalar coercion.
func (f Frontmatter) TitleState() ScalarValueState {
	state, _ := f.TitleStateContext(context.Background())
	return state
}

// TitleStateContext is the cancellation-aware strict title observation.
func (f Frontmatter) TitleStateContext(ctx context.Context) (ScalarValueState, error) {
	return f.semanticStringStateContext(ctx, "title")
}

// Description returns the optional one-line description field.
func (f Frontmatter) Description() (string, bool) {
	value, valid, _ := f.DescriptionContext(context.Background())
	return value, valid
}

// DescriptionContext is the cancellation-aware description projection.
func (f Frontmatter) DescriptionContext(ctx context.Context) (string, bool, error) {
	state, err := f.DescriptionStateContext(ctx)
	if err != nil {
		return "", false, err
	}
	return state.Value, state.Valid, nil
}

// DescriptionState returns strict optional description state without coercion.
func (f Frontmatter) DescriptionState() ScalarValueState {
	state, _ := f.DescriptionStateContext(context.Background())
	return state
}

// DescriptionStateContext is the cancellation-aware strict description
// observation.
func (f Frontmatter) DescriptionStateContext(ctx context.Context) (ScalarValueState, error) {
	return f.semanticStringStateContext(ctx, "description")
}

// Resource returns the optional resource URI field.
func (f Frontmatter) Resource() (string, bool) {
	value, valid, _ := f.ResourceContext(context.Background())
	return value, valid
}

// ResourceContext is the cancellation-aware resource projection.
func (f Frontmatter) ResourceContext(ctx context.Context) (string, bool, error) {
	state, err := f.ResourceStateContext(ctx)
	if err != nil {
		return "", false, err
	}
	return state.Value, state.Valid, nil
}

// ResourceState returns strict optional resource state without coercion.
func (f Frontmatter) ResourceState() ScalarValueState {
	state, _ := f.ResourceStateContext(context.Background())
	return state
}

// ResourceStateContext is the cancellation-aware strict resource observation.
func (f Frontmatter) ResourceStateContext(ctx context.Context) (ScalarValueState, error) {
	return f.semanticStringStateContext(ctx, "resource")
}

// Timestamp returns the optional last-modified timestamp field.
//
// Deprecated: use TimestampState to preserve semantic presence and malformed
// observations instead of collapsing them into the bool result.
func (f Frontmatter) Timestamp() (string, bool) {
	value, valid, _ := f.TimestampContext(context.Background())
	return value, valid
}

// TimestampContext is the cancellation-aware legacy timestamp projection.
func (f Frontmatter) TimestampContext(ctx context.Context) (string, bool, error) {
	state, err := f.TimestampStateContext(ctx)
	if err != nil {
		return "", false, err
	}
	return state.Value, state.Valid, nil
}

// TimestampState returns strict legacy timestamp state. Correctly tagged
// string/timestamp scalars must also contain an RFC3339 datetime.
func (f Frontmatter) TimestampState() ScalarValueState {
	state, _ := f.TimestampStateContext(context.Background())
	return state
}

// TimestampStateContext is the cancellation-aware strict legacy timestamp
// observation. Correct string/timestamp tags must contain RFC3339 data.
func (f Frontmatter) TimestampStateContext(ctx context.Context) (ScalarValueState, error) {
	node, present, err := f.SemanticGetContext(ctx, "timestamp")
	if err != nil {
		return ScalarValueState{}, err
	}
	if !present {
		return ScalarValueState{}, nil
	}
	state := ScalarValueState{Present: true}
	if node == nil || node.Kind != yaml.ScalarNode {
		return state, nil
	}
	owned, copyErr := stringFromStringContext(ctx, node.Value)
	if copyErr != nil {
		return ScalarValueState{}, copyErr
	}
	state.Raw = owned
	if node.Tag != "!!str" && node.Tag != "!!timestamp" {
		return state, nil
	}
	nonBlank, blankErr := nonBlankStringValueContext(ctx, node.Value)
	if blankErr != nil {
		return ScalarValueState{}, blankErr
	}
	if !nonBlank || parseDateTimeValue(owned, true).State != TemporalValid {
		if err := ctx.Err(); err != nil {
			return ScalarValueState{}, err
		}
		return state, nil
	}
	state.Valid, state.Value = true, owned
	if err := ctx.Err(); err != nil {
		return ScalarValueState{}, err
	}
	return state, nil
}

// Tags returns correctly tagged, non-empty string observations from the
// optional tags list. Malformed items remain visible through TagsState/Get.
func (f Frontmatter) Tags() []string {
	tags, _ := f.TagsContext(context.Background())
	return tags
}

// TagsContext is the cancellation-aware typed tags projection. Cancellation
// returns no partial slice.
func (f Frontmatter) TagsContext(ctx context.Context) ([]string, error) {
	state, err := f.TagsStateContext(ctx)
	if err != nil {
		return nil, err
	}
	if !state.Present || !state.Valid && state.Items == nil {
		return nil, nil
	}
	tags := make([]string, 0, len(state.Items))
	for _, item := range state.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if item.Valid {
			owned, copyErr := stringFromStringContext(ctx, item.Value)
			if copyErr != nil {
				return nil, copyErr
			}
			tags = append(tags, owned)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return tags, nil
}

// TagsState returns sequence shape and per-item strict string observations.
func (f Frontmatter) TagsState() StringListState {
	state, _ := f.TagsStateContext(context.Background())
	return state
}

// TagsStateContext is the cancellation-aware strict tags observation. It uses
// the shared resolver over the retained, already-validated root and preserves
// absent, ambiguous, alias, merge, sequence, and per-item shape semantics.
func (f Frontmatter) TagsStateContext(ctx context.Context) (StringListState, error) {
	if err := ctx.Err(); err != nil {
		return StringListState{}, err
	}
	root := f.mappingNode()
	selection, err := newYAMLSemanticResolver(ctx).mappingValue(&root, "tags")
	if err != nil {
		return StringListState{}, err
	}
	node, present := semanticSelectionNode(selection)
	if !present {
		return StringListState{}, nil
	}
	state := StringListState{Present: true}
	if node.Kind != yaml.SequenceNode {
		return state, nil
	}
	state.Valid = true
	state.Items = make([]ScalarValueState, 0, len(node.Content))
	for _, item := range node.Content {
		if err := ctx.Err(); err != nil {
			return StringListState{}, err
		}
		observation, observationErr := strictStringNodeStateContext(ctx, item, true)
		if observationErr != nil {
			return StringListState{}, observationErr
		}
		state.Items = append(state.Items, observation)
		if !observation.Valid {
			state.Valid = false
		}
	}
	if err := ctx.Err(); err != nil {
		return StringListState{}, err
	}
	return state, nil
}

// ExtensionKeys returns keys that are not well-known OKF frontmatter fields.
func (f Frontmatter) ExtensionKeys() []string {
	extensions, _ := f.ExtensionKeysContext(context.Background())
	return extensions
}

// ExtensionKeysContext is the cancellation-aware physical extension-key
// projection. It preserves KeysContext order/duplicates/complex-key behavior
// and applies the standard-key exclusion without sorting or hashing caller
// strings. Cancellation returns no partial slice.
func (f Frontmatter) ExtensionKeysContext(ctx context.Context) ([]string, error) {
	keys, err := f.KeysContext(ctx)
	if err != nil {
		return nil, err
	}
	var extensions []string
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		standard, standardErr := isStandardFrontmatterKeyContext(ctx, key)
		if standardErr != nil {
			return nil, standardErr
		}
		if !standard {
			extensions = append(extensions, key)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return extensions, nil
}

func isStandardFrontmatterKeyContext(ctx context.Context, key string) (bool, error) {
	for _, standard := range []string{
		"type", "title", "description", "resource", "tags", "okf_version",
		"timestamp", "sources", "usage_window", "generated", "verified",
		"status", "stale_after", "runtime", "parameters", "computation",
		"executor", "attester",
	} {
		equal, err := equalStringContext(ctx, key, standard)
		if err != nil {
			return false, err
		}
		if equal {
			return true, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func isStandardFrontmatterKey(key string) bool {
	switch key {
	case "type", "title", "description", "resource", "tags", "okf_version",
		"timestamp", "sources", "usage_window", "generated", "verified",
		"status", "stale_after", "runtime", "parameters", "computation",
		"executor", "attester":
		return true
	default:
		return false
	}
}

func standardSemanticFamilyNodes(root *yaml.Node) map[*yaml.Node]struct{} {
	excluded, _ := standardSemanticFamilyNodesContext(context.Background(), root)
	return excluded
}

func standardSemanticFamilyNodesContext(
	ctx context.Context,
	root *yaml.Node,
) (map[*yaml.Node]struct{}, error) {
	excluded := make(map[*yaml.Node]struct{})
	if ctx == nil {
		ctx = context.Background()
	}

	type visitMode uint8
	const (
		visitMapping visitMode = iota
		visitMerge
		markSubgraph
	)
	type visitFrame struct {
		node *yaml.Node
		mode visitMode
	}
	seenMappings := make(map[*yaml.Node]struct{})
	stack := []visitFrame{{node: root, mode: visitMapping}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		last := len(stack) - 1
		frame := stack[last]
		stack = stack[:last]
		node := frame.node
		if node == nil {
			continue
		}

		switch frame.mode {
		case markSubgraph:
			if _, seen := excluded[node]; seen {
				continue
			}
			excluded[node] = struct{}{}
			if node.Alias != nil {
				stack = append(stack, visitFrame{node: node.Alias, mode: markSubgraph})
			}
			for index := len(node.Content) - 1; index >= 0; index-- {
				stack = append(stack, visitFrame{node: node.Content[index], mode: markSubgraph})
			}
		case visitMerge:
			if node.Kind == yaml.SequenceNode {
				for index := len(node.Content) - 1; index >= 0; index-- {
					stack = append(stack, visitFrame{node: node.Content[index], mode: visitMapping})
				}
				continue
			}
			stack = append(stack, visitFrame{node: node, mode: visitMapping})
		case visitMapping:
			if node.Kind == yaml.AliasNode {
				stack = append(stack, visitFrame{node: node.Alias, mode: visitMapping})
				continue
			}
			if node.Kind != yaml.MappingNode {
				continue
			}
			if _, seen := seenMappings[node]; seen {
				continue
			}
			seenMappings[node] = struct{}{}
			for index := len(node.Content) - 2; index >= 0; index -= 2 {
				key, value := node.Content[index], node.Content[index+1]
				if isMergeKey(key) {
					stack = append(stack, visitFrame{node: value, mode: visitMerge})
					continue
				}
				if key != nil && key.Kind == yaml.ScalarNode && key.Tag == "!!str" &&
					isStandardFrontmatterKey(key.Value) {
					stack = append(stack, visitFrame{node: value, mode: markSubgraph})
				}
			}
		}
	}
	return excluded, nil
}

func (f Frontmatter) stringState(key string) ScalarValueState {
	node, present := f.SemanticGet(key)
	return strictStringNodeState(node, present)
}

func (f Frontmatter) semanticStringStateContext(
	ctx context.Context,
	key string,
) (ScalarValueState, error) {
	node, present, err := f.SemanticGetContext(ctx, key)
	if err != nil {
		return ScalarValueState{}, err
	}
	return strictStringNodeStateContext(ctx, node, present)
}

func strictStringNodeState(node *yaml.Node, present bool) ScalarValueState {
	if !present {
		return ScalarValueState{}
	}
	state := ScalarValueState{Present: true, Raw: rawScalar(node)}
	value, valid := nonBlankString(node)
	if valid {
		state.Valid, state.Value = true, value
	}
	return state
}

func strictStringNodeStateContext(
	ctx context.Context,
	node *yaml.Node,
	present bool,
) (ScalarValueState, error) {
	if err := ctx.Err(); err != nil {
		return ScalarValueState{}, err
	}
	if !present {
		return ScalarValueState{}, nil
	}
	state := ScalarValueState{Present: true}
	if node == nil || node.Kind != yaml.ScalarNode {
		return state, nil
	}
	owned, err := stringFromStringContext(ctx, node.Value)
	if err != nil {
		return ScalarValueState{}, err
	}
	state.Raw = owned
	if node.Tag != "!!str" {
		return state, nil
	}
	nonBlank, err := nonBlankStringValueContext(ctx, node.Value)
	if err != nil {
		return ScalarValueState{}, err
	}
	if nonBlank {
		state.Valid, state.Value = true, owned
	}
	return state, nil
}

func nonBlankStringValueContext(ctx context.Context, value string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	nextCheck := 0
	for offset, current := range value {
		if offset >= nextCheck {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			nextCheck = offset + contextStringChunkSize
		}
		if !unicode.IsSpace(current) {
			return true, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func (f Frontmatter) stringScalar(key string) (string, bool) {
	node, ok := f.SemanticGet(key)
	if !ok || node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", false
	}
	if strings.TrimSpace(node.Value) == "" {
		return "", false
	}
	return node.Value, true
}

func (f Frontmatter) mappingNode() yaml.Node {
	if f.node.Kind == yaml.MappingNode {
		return f.node
	}
	return newMappingNode()
}

func newMappingNode() yaml.Node {
	return yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func isNullNode(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Value == "" && node.Tag == "")
}

func displayString(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode || isNullNode(node) {
		return "", false
	}

	switch node.Tag {
	case "!!str", "!!bool", "!!int", "!!float", "!!timestamp", "":
		return node.Value, true
	default:
		return "", false
	}
}

func isEmptyYAMLValue(node *yaml.Node) bool {
	if node == nil || isNullNode(node) {
		return true
	}

	switch node.Kind {
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str":
			return node.Value == ""
		case "!!bool":
			value, err := strconv.ParseBool(node.Value)
			return err == nil && !value
		case "!!int":
			value, err := strconv.ParseInt(node.Value, 10, 64)
			return err == nil && value == 0
		default:
			return false
		}
	case yaml.SequenceNode, yaml.MappingNode:
		return len(node.Content) == 0
	default:
		return false
	}
}

func cloneYAMLNode(node *yaml.Node) yaml.Node {
	cloned, _ := cloneYAMLNodeContext(context.Background(), node)
	return cloned
}

func cloneYAMLNodeContext(ctx context.Context, node *yaml.Node) (yaml.Node, error) {
	if err := ctx.Err(); err != nil {
		return yaml.Node{}, err
	}
	if node == nil {
		return yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}, nil
	}

	root := &yaml.Node{}
	memo := map[*yaml.Node]*yaml.Node{node: root}
	queue := []*yaml.Node{node}
	for cursor := 0; cursor < len(queue); cursor++ {
		if err := ctx.Err(); err != nil {
			return yaml.Node{}, err
		}
		source := queue[cursor]
		target := memo[source]
		*target = *source
		var err error
		target.Tag, err = stringFromStringContext(ctx, source.Tag)
		if err != nil {
			return yaml.Node{}, err
		}
		target.Value, err = stringFromStringContext(ctx, source.Value)
		if err != nil {
			return yaml.Node{}, err
		}
		target.Anchor, err = stringFromStringContext(ctx, source.Anchor)
		if err != nil {
			return yaml.Node{}, err
		}
		target.HeadComment, err = stringFromStringContext(ctx, source.HeadComment)
		if err != nil {
			return yaml.Node{}, err
		}
		target.LineComment, err = stringFromStringContext(ctx, source.LineComment)
		if err != nil {
			return yaml.Node{}, err
		}
		target.FootComment, err = stringFromStringContext(ctx, source.FootComment)
		if err != nil {
			return yaml.Node{}, err
		}
		target.Content = nil
		target.Alias = nil

		if len(source.Content) > 0 {
			target.Content = make([]*yaml.Node, len(source.Content))
			for index, child := range source.Content {
				if err := ctx.Err(); err != nil {
					return yaml.Node{}, err
				}
				if child == nil {
					continue
				}
				cloned := memo[child]
				if cloned == nil {
					cloned = &yaml.Node{}
					memo[child] = cloned
					queue = append(queue, child)
				}
				target.Content[index] = cloned
			}
		}
		if source.Alias != nil {
			cloned := memo[source.Alias]
			if cloned == nil {
				cloned = &yaml.Node{}
				memo[source.Alias] = cloned
				queue = append(queue, source.Alias)
			}
			target.Alias = cloned
		}
	}
	if err := ctx.Err(); err != nil {
		return yaml.Node{}, err
	}
	if err := ctx.Err(); err != nil {
		return yaml.Node{}, err
	}
	return *root, nil
}

func semanticMappingValue(node *yaml.Node, key string, active map[*yaml.Node]bool) (*yaml.Node, bool) {
	state := semanticMappingValueState(node, key, active)
	if !state.Present {
		return nil, false
	}
	if state.Value == nil {
		return &yaml.Node{}, true
	}
	return state.Value, true
}

func semanticMappingValueState(node *yaml.Node, key string, active map[*yaml.Node]bool) SemanticValueState {
	_ = active
	selection, err := newYAMLSemanticResolver(context.Background()).mappingValue(node, key)
	if err != nil {
		return SemanticValueState{}
	}
	return SemanticValueState{
		Present:   selection.present,
		Value:     selection.resolved,
		Ambiguous: selection.ambiguous,
	}
}

func semanticValueNode(node *yaml.Node, active map[*yaml.Node]bool) (*yaml.Node, bool) {
	_ = active
	value, err := newYAMLSemanticResolver(context.Background()).value(node)
	return value.node, err == nil && value.resolved
}

func semanticMergedValue(node *yaml.Node, key string, active map[*yaml.Node]bool) (*yaml.Node, bool) {
	state := semanticMergedValueState(node, key, active)
	if !state.Present {
		return nil, false
	}
	if state.Value == nil {
		return &yaml.Node{}, true
	}
	return state.Value, true
}

func semanticMergedValueState(node *yaml.Node, key string, active map[*yaml.Node]bool) SemanticValueState {
	_ = active
	selection, err := newYAMLSemanticResolver(context.Background()).mergedValue(node, key)
	if err != nil {
		return SemanticValueState{}
	}
	return SemanticValueState{
		Present:   selection.present,
		Value:     selection.resolved,
		Ambiguous: selection.ambiguous,
	}
}

func isMergeKey(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode &&
		(node.Tag == "!!merge" || node.Value == "<<")
}

func validateYAMLAliasIntegrity(root *yaml.Node) error {
	reachable := make(map[*yaml.Node]bool)
	anchors := make(map[string]*yaml.Node)
	var aliases []*yaml.Node
	var visit func(*yaml.Node) error
	visit = func(node *yaml.Node) error {
		if node == nil || reachable[node] {
			return nil
		}
		reachable[node] = true
		if node.Anchor != "" {
			if previous := anchors[node.Anchor]; previous != nil && previous != node {
				return fmt.Errorf("%w: duplicate YAML anchor %q", ErrInvalidFrontmatter, node.Anchor)
			}
			anchors[node.Anchor] = node
		}
		if node.Kind == yaml.AliasNode {
			aliases = append(aliases, node)
		}
		for _, child := range node.Content {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return err
	}
	for _, alias := range aliases {
		if alias.Alias == nil || !reachable[alias.Alias] || alias.Alias.Anchor == "" ||
			alias.Value != "" && alias.Value != alias.Alias.Anchor {
			return fmt.Errorf("%w: dangling YAML alias %q", ErrInvalidFrontmatter, alias.Value)
		}
	}
	return nil
}
