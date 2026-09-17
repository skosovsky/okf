package bundle

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/markdownowner"
	"gopkg.in/yaml.v3"
)

// YAMLValueShape is the syntax-level shape of an observed YAML value.
// ResolvedShape on SemanticFamilyObservation records the effective shape after
// following aliases.
type YAMLValueShape string

const (
	YAMLShapeAbsent    YAMLValueShape = "absent"
	YAMLShapeAmbiguous YAMLValueShape = "ambiguous"
	YAMLShapeMapping   YAMLValueShape = "mapping"
	YAMLShapeSequence  YAMLValueShape = "sequence"
	YAMLShapeScalar    YAMLValueShape = "scalar"
	YAMLShapeAlias     YAMLValueShape = "alias"
	YAMLShapeDocument  YAMLValueShape = "document"
	YAMLShapeUnknown   YAMLValueShape = "unknown"
)

// SemanticFamilyObservation is a node-free, caller-owned observation of one
// standard YAML family. Raw is the stable syntax-level value representation;
// ResolvedRaw is the effective representation after following aliases.
// Ambiguous observations never expose a selected effective value.
type SemanticFamilyObservation struct {
	Present       bool
	Ambiguous     bool
	Shape         YAMLValueShape
	ResolvedShape YAMLValueShape
	ShapeValid    bool
	Raw           string
	ResolvedRaw   string
	ViaAlias      bool
	ViaMerge      bool
}

// AttributionEntryObservation preserves one ordered sources[] item and its
// deterministic keyed-attribution join. Value is populated only when the
// source item is a valid attribution input.
type AttributionEntryObservation struct {
	Index        int
	Family       SemanticFamilyObservation
	Ambiguous    bool
	Valid        bool
	NormalizedID string
	Source       ProvenanceSourceState
	Value        *Attribution
}

// AttributionFamilyObservation preserves sources-family presence and shape
// separately from its ordered physical/effective entries. Body-only orphan
// groups remain available through Document.Attributions.
type AttributionFamilyObservation struct {
	Family  SemanticFamilyObservation
	Entries []AttributionEntryObservation
}

// StaleAfterObservation preserves the stale_after family syntax alongside its
// temporal interpretation.
type StaleAfterObservation struct {
	Family SemanticFamilyObservation
	Value  DateValue
}

// ExecutorReceiptObservation is the strict, node-free observation of
// executor.resource and executor.receipt. Value is populated only when the
// effective typed-read contract is valid. As with ExecutorContractState,
// receipt is optional and an empty sequence is shape-valid; strict validation
// may apply the additional usability policy that a present receipt be nonempty.
type ExecutorReceiptObservation struct {
	ExecutorFamily SemanticFamilyObservation
	ResourceFamily SemanticFamilyObservation
	ReceiptFamily  SemanticFamilyObservation
	Resource       ScalarValueState
	ItemFamilies   []SemanticFamilyObservation
	Items          []ScalarValueState
	Values         []string
	Valid          bool
	HasValue       bool
	Value          *ExecutorContract
}

type semanticFamilySelection struct {
	observation SemanticFamilyObservation
	raw         *yaml.Node
	resolved    *yaml.Node
}

var standardSemanticFamilyShapes = map[string][]YAMLValueShape{
	"usage_window": {YAMLShapeMapping},
	"generated":    {YAMLShapeMapping},
	"verified":     {YAMLShapeMapping, YAMLShapeSequence},
	"sources":      {YAMLShapeSequence},
	"status":       {YAMLShapeScalar},
	"parameters":   {YAMLShapeSequence},
	"executor":     {YAMLShapeMapping},
	"attester":     {YAMLShapeMapping},
}

// SemanticFamilyObservation observes one supported standard family without
// exposing its YAML node. Arbitrary extension fields are intentionally not
// available through this API.
func (f Frontmatter) SemanticFamilyObservation(field string) (SemanticFamilyObservation, error) {
	return f.SemanticFamilyObservationContext(context.Background(), field)
}

// SemanticFamilyObservationContext is the cancellation-aware restricted
// standard-family observation surface. Shape validity is owned by the domain,
// not selected by the caller. Unknown fields are rejected before frontmatter
// traversal; cancellation returns no partial observation.
func (f Frontmatter) SemanticFamilyObservationContext(
	ctx context.Context,
	field string,
) (SemanticFamilyObservation, error) {
	accepted, supported := standardSemanticFamilyShapes[field]
	if !supported {
		return SemanticFamilyObservation{}, fmt.Errorf(
			"%w: %q",
			ErrUnknownSemanticFamily,
			field,
		)
	}
	if err := ctx.Err(); err != nil {
		return SemanticFamilyObservation{}, err
	}
	root := f.mappingNode()
	selection, err := observeSemanticFamilyContext(ctx, &root, field, accepted...)
	if err != nil {
		return SemanticFamilyObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return SemanticFamilyObservation{}, err
	}
	return selection.observation, nil
}

// AttributionObservation returns the lossless family observation using a
// background context.
func (d Document) AttributionObservation() AttributionFamilyObservation {
	observation, _ := d.AttributionObservationContext(context.Background())
	return observation
}

// AttributionObservationContext observes ordered sources[] evidence without
// requiring callers to traverse raw YAML. Cancellation returns no partial
// observation.
func (d Document) AttributionObservationContext(ctx context.Context) (AttributionFamilyObservation, error) {
	if err := ctx.Err(); err != nil {
		return AttributionFamilyObservation{}, err
	}
	root := d.Frontmatter.mappingNode()
	family, err := observeSemanticFamilyContext(ctx, &root, "sources", YAMLShapeSequence)
	if err != nil {
		return AttributionFamilyObservation{}, err
	}
	out := AttributionFamilyObservation{Family: family.observation}
	if family.observation.Ambiguous || family.resolved == nil ||
		family.resolved.Kind != yaml.SequenceNode {
		return out, nil
	}

	joined, err := d.AttributionsContext(ctx)
	if err != nil {
		return AttributionFamilyObservation{}, err
	}
	byNormalizedID := make(map[string]Attribution, len(joined))
	for _, value := range joined {
		if err := ctx.Err(); err != nil {
			return AttributionFamilyObservation{}, err
		}
		byNormalizedID[value.NormalizedID] = cloneAttribution(value)
	}

	out.Entries = make([]AttributionEntryObservation, 0, len(family.resolved.Content))
	resolver := newYAMLSemanticResolver(ctx)
	for index, rawItem := range family.resolved.Content {
		if err := ctx.Err(); err != nil {
			return AttributionFamilyObservation{}, err
		}
		itemFamily, resolvedItem, err := observeSequenceItemContext(ctx, rawItem, YAMLShapeMapping)
		if err != nil {
			return AttributionFamilyObservation{}, err
		}
		source, sourceErr := provenanceSourceStateContext(ctx, resolver, resolvedItem)
		if sourceErr != nil {
			return AttributionFamilyObservation{}, sourceErr
		}
		entry := AttributionEntryObservation{
			Index:  index,
			Family: itemFamily,
			Source: source,
		}
		if resolvedItem != nil && resolvedItem.Kind == yaml.MappingNode {
			for _, key := range []string{
				"id", "resource", "title", "author", "usage_count",
				"last_modified", "usage_window",
			} {
				member, memberErr := observeSemanticFamilyContext(ctx, resolvedItem, key)
				if memberErr != nil {
					return AttributionFamilyObservation{}, memberErr
				}
				if member.observation.Ambiguous {
					entry.Ambiguous = true
				}
				if key == "usage_window" && !member.observation.Ambiguous &&
					member.resolved != nil && member.resolved.Kind == yaml.MappingNode {
					for _, nestedKey := range []string{"from", "to"} {
						nested, nestedErr := observeSemanticFamilyContext(
							ctx,
							member.resolved,
							nestedKey,
						)
						if nestedErr != nil {
							return AttributionFamilyObservation{}, nestedErr
						}
						if nested.observation.Ambiguous {
							entry.Ambiguous = true
						}
					}
				}
			}
		}
		entry.Valid = !entry.Family.Ambiguous && !entry.Ambiguous &&
			entry.Family.ShapeValid && entry.Source.Valid && entry.Source.ID.Valid
		if entry.Valid {
			entry.NormalizedID = markdownowner.NormalizeFootnoteLabel(entry.Source.ID.Value)
			if value, ok := byNormalizedID[entry.NormalizedID]; ok && entry.NormalizedID != "" {
				cloned := cloneAttribution(value)
				entry.Value = &cloned
			} else {
				entry.Valid = false
				entry.NormalizedID = ""
			}
		}
		out.Entries = append(out.Entries, entry)
	}
	if err := ctx.Err(); err != nil {
		return AttributionFamilyObservation{}, err
	}
	return out, nil
}

// StaleAfterObservation returns the family and temporal observation using a
// background context.
func (f Frontmatter) StaleAfterObservation() StaleAfterObservation {
	observation, _ := f.StaleAfterObservationContext(context.Background())
	return observation
}

// StaleAfterObservationContext preserves absence, ambiguity, syntax shape,
// raw YAML, and the effective temporal value. Cancellation returns zero state.
func (f Frontmatter) StaleAfterObservationContext(ctx context.Context) (StaleAfterObservation, error) {
	if err := ctx.Err(); err != nil {
		return StaleAfterObservation{}, err
	}
	root := f.mappingNode()
	family, err := observeSemanticFamilyContext(ctx, &root, "stale_after", YAMLShapeScalar)
	if err != nil {
		return StaleAfterObservation{}, err
	}
	out := StaleAfterObservation{Family: family.observation}
	switch {
	case !family.observation.Present:
		out.Value.State = TemporalAbsent
	case family.observation.Ambiguous || family.resolved == nil:
		out.Value = DateValue{Raw: family.observation.ResolvedRaw, State: TemporalMalformed}
	default:
		value, valueErr := dateValueFromNodeContext(ctx, family.resolved)
		if valueErr != nil {
			return StaleAfterObservation{}, valueErr
		}
		if value.State == TemporalMalformed {
			out.Value = DateValue{Raw: family.observation.ResolvedRaw, State: TemporalMalformed}
		} else {
			out.Value = value
		}
	}
	if err := ctx.Err(); err != nil {
		return StaleAfterObservation{}, err
	}
	return out, nil
}

// ExecutorReceiptObservation returns strict executor/receipt evidence using a
// background context.
func (f Frontmatter) ExecutorReceiptObservation() ExecutorReceiptObservation {
	observation, _ := f.ExecutorReceiptObservationContext(context.Background())
	return observation
}

// ExecutorReceiptObservationContext resolves aliases and merges while failing
// closed on duplicate executor/resource/receipt keys. Cancellation returns no
// partial observation.
func (f Frontmatter) ExecutorReceiptObservationContext(ctx context.Context) (ExecutorReceiptObservation, error) {
	if err := ctx.Err(); err != nil {
		return ExecutorReceiptObservation{}, err
	}
	root := f.mappingNode()
	executor, err := observeSemanticFamilyContext(ctx, &root, "executor", YAMLShapeMapping)
	if err != nil {
		return ExecutorReceiptObservation{}, err
	}
	out := ExecutorReceiptObservation{ExecutorFamily: executor.observation}
	if executor.observation.Ambiguous || executor.resolved == nil ||
		executor.resolved.Kind != yaml.MappingNode {
		return out, nil
	}

	resource, err := observeSemanticFamilyContext(ctx, executor.resolved, "resource", YAMLShapeScalar)
	if err != nil {
		return ExecutorReceiptObservation{}, err
	}
	receipt, err := observeSemanticFamilyContext(ctx, executor.resolved, "receipt", YAMLShapeSequence)
	if err != nil {
		return ExecutorReceiptObservation{}, err
	}
	out.ResourceFamily = resource.observation
	out.ReceiptFamily = receipt.observation
	out.Resource = strictObservedStringState(resource)
	if out.Resource.Valid {
		out.HasValue = true
	}

	if receipt.observation.Present && !receipt.observation.Ambiguous &&
		receipt.resolved != nil && receipt.resolved.Kind == yaml.SequenceNode {
		out.ItemFamilies = make([]SemanticFamilyObservation, 0, len(receipt.resolved.Content))
		out.Items = make([]ScalarValueState, 0, len(receipt.resolved.Content))
		values := make([]string, 0, len(receipt.resolved.Content))
		for _, item := range receipt.resolved.Content {
			if err := ctx.Err(); err != nil {
				return ExecutorReceiptObservation{}, err
			}
			itemFamily, resolvedItem, itemErr := observeSequenceItemContext(ctx, item, YAMLShapeScalar)
			if itemErr != nil {
				return ExecutorReceiptObservation{}, itemErr
			}
			out.ItemFamilies = append(out.ItemFamilies, itemFamily)
			scalar := ScalarValueState{Present: true, Raw: itemFamily.Raw}
			if !itemFamily.Ambiguous {
				state, stateErr := strictStringNodeStateContext(ctx, resolvedItem, true)
				if stateErr != nil {
					return ExecutorReceiptObservation{}, stateErr
				}
				if state.Valid {
					scalar.Valid = true
					scalar.Value = state.Value
					values = append(values, state.Value)
				}
			}
			out.Items = append(out.Items, scalar)
		}
		if allScalarStatesValid(out.Items) {
			out.Values = values
		}
	}
	if len(out.Values) > 0 {
		out.HasValue = true
	}
	receiptValid := !receipt.observation.Present ||
		receipt.observation.ShapeValid && !receipt.observation.Ambiguous &&
			allScalarStatesValid(out.Items)
	out.Valid = out.Resource.Valid && receiptValid
	if out.Valid {
		out.Value = &ExecutorContract{
			Resource: out.Resource.Value,
			Receipt:  append([]string(nil), out.Values...),
		}
	}
	if err := ctx.Err(); err != nil {
		return ExecutorReceiptObservation{}, err
	}
	return out, nil
}

func observeSemanticFamilyContext(
	ctx context.Context,
	node *yaml.Node,
	key string,
	accepted ...YAMLValueShape,
) (semanticFamilySelection, error) {
	if err := ctx.Err(); err != nil {
		return semanticFamilySelection{}, err
	}
	selection, err := selectSemanticMappingValueContext(
		ctx,
		node,
		key,
		make(map[*yaml.Node]bool),
		false,
		false,
	)
	if err != nil {
		return semanticFamilySelection{}, err
	}
	if !selection.observation.Present {
		selection.observation.Shape = YAMLShapeAbsent
		selection.observation.ResolvedShape = YAMLShapeAbsent
		selection.observation.ShapeValid = true
		return selection, nil
	}
	if selection.observation.Ambiguous {
		if selection.observation.Shape == "" {
			selection.observation.Shape = YAMLShapeAmbiguous
		}
		selection.observation.ResolvedShape = YAMLShapeAmbiguous
		return selection, nil
	}
	selection.observation.Shape = yamlValueShape(selection.raw)
	selection.observation.ResolvedShape = yamlValueShape(selection.resolved)
	selection.observation.Raw, err = stableYAMLValueContext(ctx, selection.raw)
	if err != nil {
		return semanticFamilySelection{}, err
	}
	selection.observation.ResolvedRaw, err = stableYAMLValueContext(ctx, selection.resolved)
	if err != nil {
		return semanticFamilySelection{}, err
	}
	for _, shape := range accepted {
		if selection.observation.ResolvedShape == shape {
			selection.observation.ShapeValid = true
			break
		}
	}
	if len(accepted) == 0 {
		selection.observation.ShapeValid = true
	}
	return selection, nil
}

func selectSemanticMappingValueContext(
	ctx context.Context,
	node *yaml.Node,
	key string,
	active map[*yaml.Node]bool,
	viaAlias bool,
	viaMerge bool,
) (semanticFamilySelection, error) {
	_ = active
	selection, err := newYAMLSemanticResolver(ctx).mappingValue(node, key)
	if err != nil {
		return semanticFamilySelection{}, err
	}
	observation := SemanticFamilyObservation{
		Present:   selection.present,
		Ambiguous: selection.ambiguous,
		ViaAlias:  selection.present && (selection.viaAlias || viaAlias),
		ViaMerge:  selection.present && (selection.viaMerge || viaMerge),
	}
	if observation.Ambiguous {
		observation.Shape = YAMLShapeAmbiguous
		observation.ResolvedShape = YAMLShapeAmbiguous
	}
	return semanticFamilySelection{
		observation: observation,
		raw:         selection.raw,
		resolved:    selection.resolved,
	}, nil
}

func observeSelectedYAMLValueContext(
	ctx context.Context,
	raw *yaml.Node,
	viaAlias bool,
	viaMerge bool,
) (semanticFamilySelection, error) {
	if err := ctx.Err(); err != nil {
		return semanticFamilySelection{}, err
	}
	resolved, resolvedOK, sawAlias, err := resolveYAMLValueContext(
		ctx, raw, make(map[*yaml.Node]bool),
	)
	if err != nil {
		return semanticFamilySelection{}, err
	}
	observation := SemanticFamilyObservation{
		Present:  true,
		ViaAlias: viaAlias || sawAlias,
		ViaMerge: viaMerge,
	}
	if !resolvedOK {
		observation.Ambiguous = true
		observation.Shape = yamlValueShape(raw)
		observation.ResolvedShape = YAMLShapeAmbiguous
		return semanticFamilySelection{observation: observation, raw: raw}, nil
	}
	return semanticFamilySelection{observation: observation, raw: raw, resolved: resolved}, nil
}

func resolveYAMLValueContext(
	ctx context.Context,
	node *yaml.Node,
	active map[*yaml.Node]bool,
) (*yaml.Node, bool, bool, error) {
	_ = active
	value, err := newYAMLSemanticResolver(ctx).value(node)
	return value.node, value.resolved, value.viaAlias, err
}

func observeSequenceItemContext(
	ctx context.Context,
	raw *yaml.Node,
	accepted ...YAMLValueShape,
) (SemanticFamilyObservation, *yaml.Node, error) {
	selection, err := observeSelectedYAMLValueContext(ctx, raw, false, false)
	if err != nil {
		return SemanticFamilyObservation{}, nil, err
	}
	selection.observation.Shape = yamlValueShape(raw)
	if selection.observation.Ambiguous {
		selection.observation.Raw = ""
		selection.observation.ResolvedRaw = ""
		return selection.observation, nil, nil
	}
	selection.observation.Raw, err = stableYAMLValueContext(ctx, raw)
	if err != nil {
		return SemanticFamilyObservation{}, nil, err
	}
	selection.observation.ResolvedShape = yamlValueShape(selection.resolved)
	selection.observation.ResolvedRaw, err = stableYAMLValueContext(ctx, selection.resolved)
	if err != nil {
		return SemanticFamilyObservation{}, nil, err
	}
	for _, shape := range accepted {
		if selection.observation.ResolvedShape == shape {
			selection.observation.ShapeValid = true
			break
		}
	}
	return selection.observation, selection.resolved, nil
}

func yamlValueShape(node *yaml.Node) YAMLValueShape {
	if node == nil {
		return YAMLShapeAbsent
	}
	switch node.Kind {
	case yaml.MappingNode:
		return YAMLShapeMapping
	case yaml.SequenceNode:
		return YAMLShapeSequence
	case yaml.ScalarNode:
		return YAMLShapeScalar
	case yaml.AliasNode:
		return YAMLShapeAlias
	case yaml.DocumentNode:
		return YAMLShapeDocument
	default:
		return YAMLShapeUnknown
	}
}

func stableYAMLValueContext(ctx context.Context, node *yaml.Node) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if node == nil {
		return "", nil
	}
	if node.Kind == yaml.ScalarNode {
		return stringFromStringContext(ctx, node.Value)
	}
	if node.Kind == yaml.AliasNode {
		value, err := stringFromStringContext(ctx, node.Value)
		if err != nil {
			return "", err
		}
		var out strings.Builder
		out.Grow(len(value) + 1)
		out.WriteByte('*')
		for offset := 0; offset < len(value); offset += contextStringChunkSize {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			end := min(offset+contextStringChunkSize, len(value))
			out.WriteString(value[offset:end])
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return out.String(), nil
	}
	if err := validateYAMLSubgraphContext(ctx, node); err != nil {
		return "", err
	}
	cloned, err := cloneYAMLNodeContext(ctx, node)
	if err != nil {
		return "", err
	}
	encoded, err := encodeYAMLNodeStringContext(
		ctx,
		&cloned,
		func(writer io.Writer) yamlStringEncoder {
			encoder := yaml.NewEncoder(writer)
			encoder.SetIndent(4)
			return stableYAMLValueEncoder{Encoder: encoder}
		},
	)
	if err != nil {
		return "", fmt.Errorf("render semantic observation: %w", err)
	}
	return trimSpaceContext(ctx, encoded)
}

type stableYAMLValueEncoder struct{ *yaml.Encoder }

// yaml.Marshal uses four-space indentation. Ignore the shared streaming
// helper's two-space frontmatter setting to retain byte-for-byte compatibility.
func (stableYAMLValueEncoder) SetIndent(int) {}

func trimSpaceContext(ctx context.Context, value string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	start := 0
	for start < len(value) {
		if start%contextStringChunkSize == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		current, size := utf8.DecodeRuneInString(value[start:])
		if !unicode.IsSpace(current) {
			break
		}
		start += size
	}
	end := len(value)
	for end > start {
		if (len(value)-end)%contextStringChunkSize == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		current, size := utf8.DecodeLastRuneInString(value[:end])
		if !unicode.IsSpace(current) {
			break
		}
		end -= size
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return value[start:end], nil
}

func provenanceSourceStateFromNode(node *yaml.Node) ProvenanceSourceState {
	state, _ := provenanceSourceStateFromNodeContext(context.Background(), node)
	return state
}

func provenanceSourceStateFromNodeContext(ctx context.Context, node *yaml.Node) (ProvenanceSourceState, error) {
	if err := ctx.Err(); err != nil {
		return ProvenanceSourceState{}, err
	}
	if node == nil || node.Kind != yaml.MappingNode {
		return ProvenanceSourceState{Present: true}, nil
	}
	return provenanceSourceStateContext(ctx, newYAMLSemanticResolver(ctx), node)
}

func strictObservedStringState(selection semanticFamilySelection) ScalarValueState {
	if !selection.observation.Present {
		return ScalarValueState{}
	}
	state := ScalarValueState{Present: true, Raw: selection.observation.Raw}
	if selection.observation.Ambiguous {
		return state
	}
	if value, ok := nonBlankString(selection.resolved); ok {
		state.Valid = true
		state.Value = value
	}
	return state
}

func allScalarStatesValid(states []ScalarValueState) bool {
	for _, state := range states {
		if !state.Valid {
			return false
		}
	}
	return true
}

func cloneAttribution(value Attribution) Attribution {
	value.Sources = append([]ProvenanceSource(nil), value.Sources...)
	for index := range value.Sources {
		value.Sources[index] = cloneProvenanceSource(value.Sources[index])
	}
	value.References = append([]FootnoteReference(nil), value.References...)
	value.Definitions = append([]FootnoteDefinition(nil), value.Definitions...)
	return value
}
