package mutation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

type yamlRenderValue struct {
	kind     yamlRenderKind
	text     string
	lexical  string
	boolean  bool
	integer  int64
	unsigned uint64
	mapping  []yamlRenderEntry
	sequence []yamlRenderValue
}

type yamlRenderKind uint8

const (
	yamlRenderInvalid yamlRenderKind = iota
	yamlRenderString
	yamlRenderBool
	yamlRenderInt
	yamlRenderUint
	yamlRenderDate
	yamlRenderDateTime
	yamlRenderMapping
	yamlRenderSequence
)

type yamlRenderEntry struct {
	Key   string
	Value yamlRenderValue
}

func yamlString(value string) yamlRenderValue {
	return yamlRenderValue{kind: yamlRenderString, text: value}
}
func yamlBool(value bool) yamlRenderValue {
	return yamlRenderValue{kind: yamlRenderBool, boolean: value}
}
func yamlInt(value int64) yamlRenderValue {
	return yamlRenderValue{kind: yamlRenderInt, integer: value}
}
func yamlUint(value uint64) yamlRenderValue {
	return yamlRenderValue{kind: yamlRenderUint, unsigned: value}
}

func yamlDate(value string) (yamlRenderValue, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return yamlRenderValue{}, fmt.Errorf("invalid YAML date %q", value)
	}
	return yamlRenderValue{kind: yamlRenderDate, text: value, lexical: value}, nil
}

func yamlDateTime(value string) (yamlRenderValue, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return yamlRenderValue{}, fmt.Errorf("invalid YAML datetime %q", value)
	}
	normalized := parsed.Format(time.RFC3339Nano)
	return yamlRenderValue{kind: yamlRenderDateTime, text: normalized, lexical: normalized}, nil
}

func yamlEntry(key string, value yamlRenderValue) yamlRenderEntry {
	return yamlRenderEntry{Key: key, Value: value}
}

func yamlMapping(entries ...yamlRenderEntry) yamlRenderValue {
	return yamlRenderValue{kind: yamlRenderMapping, mapping: append([]yamlRenderEntry(nil), entries...)}
}

func yamlSequence(values ...yamlRenderValue) yamlRenderValue {
	return yamlRenderValue{kind: yamlRenderSequence, sequence: append([]yamlRenderValue(nil), values...)}
}

func cloneYAMLRenderValueContext(ctx context.Context, value yamlRenderValue) (yamlRenderValue, error) {
	var cloned yamlRenderValue
	if err := walkYAMLRenderValueContext(ctx, value, &cloned); err != nil {
		return yamlRenderValue{}, err
	}
	return cloned, nil
}

func yamlRenderFromSemanticContext(ctx context.Context, node *yamlSemanticNode) (yamlRenderValue, error) {
	validated, err := cloneSemanticNodeContext(ctx, node)
	if err != nil {
		return yamlRenderValue{}, err
	}
	return yamlRenderFromValidatedSemanticContext(ctx, validated)
}

func yamlRenderFromValidatedSemanticContext(ctx context.Context, node *yamlSemanticNode) (yamlRenderValue, error) {
	if err := ctx.Err(); err != nil {
		return yamlRenderValue{}, err
	}
	if node == nil {
		return yamlRenderValue{}, fmt.Errorf("%w: nil YAML semantic node", ErrUnsupportedPresentation)
	}
	switch node.Kind {
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str":
			return yamlString(node.Value), nil
		case "!!bool":
			if len(node.Value) > len("false") {
				return yamlRenderValue{}, fmt.Errorf("%w: invalid bool", ErrUnsupportedPresentation)
			}
			value, err := strconv.ParseBool(node.Value)
			if err != nil {
				return yamlRenderValue{}, fmt.Errorf("%w: invalid bool", ErrUnsupportedPresentation)
			}
			return yamlBool(value), nil
		case "!!int":
			if len(node.Value) > 66 {
				return yamlRenderValue{}, fmt.Errorf("%w: invalid int", ErrUnsupportedPresentation)
			}
			value, err := strconv.ParseInt(node.Value, 0, 64)
			if err == nil {
				return yamlInt(value), nil
			}
			unsigned, unsignedErr := strconv.ParseUint(node.Value, 0, 64)
			if unsignedErr != nil {
				return yamlRenderValue{}, fmt.Errorf("%w: invalid int", ErrUnsupportedPresentation)
			}
			return yamlUint(unsigned), nil
		case "!!timestamp":
			if len(node.Value) == len("2006-01-02") {
				value, err := yamlDate(node.Value)
				if err == nil {
					value.lexical = node.Value
				}
				return value, err
			}
			value, err := yamlDateTime(node.Value)
			if err == nil {
				value.lexical = node.Value
			}
			return value, err
		default:
			return yamlRenderValue{}, fmt.Errorf("%w: scalar tag %q", ErrUnsupportedPresentation, node.Tag)
		}
	case yaml.MappingNode:
		if node.Tag != "!!map" || len(node.Content)%2 != 0 {
			return yamlRenderValue{}, fmt.Errorf("%w: mapping", ErrUnsupportedPresentation)
		}
		entries := make([]yamlRenderEntry, 0, len(node.Content)/2)
		seen := make(map[[sha256.Size]byte][]string, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			if err := ctx.Err(); err != nil {
				return yamlRenderValue{}, err
			}
			key := node.Content[i]
			if key == nil || key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return yamlRenderValue{}, fmt.Errorf("%w: semantic mapping key", ErrUnsupportedPresentation)
			}
			digest, err := stringFingerprintContext(ctx, key.Value)
			if err != nil {
				return yamlRenderValue{}, err
			}
			for _, existing := range seen[digest] {
				equal, equalErr := stringsEqualContext(ctx, existing, key.Value)
				if equalErr != nil {
					return yamlRenderValue{}, equalErr
				}
				if equal {
					return yamlRenderValue{}, fmt.Errorf("%w: semantic mapping key", ErrUnsupportedPresentation)
				}
			}
			seen[digest] = append(seen[digest], key.Value)
			value, err := yamlRenderFromValidatedSemanticContext(ctx, node.Content[i+1])
			if err != nil {
				return yamlRenderValue{}, err
			}
			entries = append(entries, yamlEntry(key.Value, value))
		}
		return yamlMapping(entries...), nil
	case yaml.SequenceNode:
		if node.Tag != "!!seq" {
			return yamlRenderValue{}, fmt.Errorf("%w: sequence", ErrUnsupportedPresentation)
		}
		items := make([]yamlRenderValue, len(node.Content))
		for i, child := range node.Content {
			value, err := yamlRenderFromValidatedSemanticContext(ctx, child)
			if err != nil {
				return yamlRenderValue{}, err
			}
			items[i] = value
		}
		return yamlSequence(items...), nil
	default:
		return yamlRenderValue{}, fmt.Errorf("%w: YAML semantic node kind %d", ErrUnsupportedPresentation, node.Kind)
	}
}

type yamlRenderContainerIdentity struct {
	kind           yamlRenderKind
	mapping        *yamlRenderEntry
	mappingLen     int
	mappingNonNil  bool
	sequence       *yamlRenderValue
	sequenceLen    int
	sequenceNonNil bool
}

type yamlRenderWalkMemo struct {
	clone          yamlRenderValue
	containerDepth int
	expansion      int
}

// walkYAMLRenderValueContext is the single render-graph budget boundary. When
// target is non-nil it also produces a defensive deep copy; errors never
// expose a partial result.
func walkYAMLRenderValueContext(ctx context.Context, value yamlRenderValue, target *yamlRenderValue) error {
	memo := make(map[yamlRenderContainerIdentity]yamlRenderWalkMemo)
	active := make(map[yamlRenderContainerIdentity]bool)
	nodes, edges := 0, 0
	expansionCap := bundle.MaxYAMLSemanticExpansion + 1
	limitError := func(kind bundle.YAMLResourceLimitKind, limit int) error {
		return &bundle.YAMLResourceLimitError{Kind: kind, Limit: limit, Observed: limit + 1}
	}
	identity := func(value *yamlRenderValue) yamlRenderContainerIdentity {
		id := yamlRenderContainerIdentity{
			kind:           value.kind,
			mappingLen:     len(value.mapping),
			mappingNonNil:  value.mapping != nil,
			sequenceLen:    len(value.sequence),
			sequenceNonNil: value.sequence != nil,
		}
		if len(value.mapping) != 0 {
			id.mapping = &value.mapping[0]
		}
		if len(value.sequence) != 0 {
			id.sequence = &value.sequence[0]
		}
		return id
	}
	var visit func(*yamlRenderValue, int) (yamlRenderWalkMemo, error)
	visit = func(source *yamlRenderValue, parentDepth int) (yamlRenderWalkMemo, error) {
		if err := ctx.Err(); err != nil {
			return yamlRenderWalkMemo{}, err
		}
		id := identity(source)
		hasIdentity := id.mapping != nil || id.sequence != nil
		if hasIdentity && active[id] {
			return yamlRenderWalkMemo{}, &bundle.YAMLIntegrityError{Code: bundle.YAMLIntegrityContentCycle}
		}
		if hasIdentity {
			if completed, ok := memo[id]; ok {
				if completed.containerDepth > bundle.MaxYAMLPhysicalDepth-parentDepth {
					return yamlRenderWalkMemo{}, limitError(bundle.YAMLResourcePhysicalDepth, bundle.MaxYAMLPhysicalDepth)
				}
				return completed, nil
			}
			active[id] = true
			defer delete(active, id)
		}
		nodes++
		if nodes > bundle.MaxYAMLGraphNodes {
			return yamlRenderWalkMemo{}, limitError(bundle.YAMLResourceGraphNodes, bundle.MaxYAMLGraphNodes)
		}
		children := len(source.mapping) + len(source.sequence)
		if children > bundle.MaxYAMLGraphEdges-edges {
			return yamlRenderWalkMemo{}, limitError(bundle.YAMLResourceGraphEdges, bundle.MaxYAMLGraphEdges)
		}
		edges += children
		containerDepth := 0
		if source.kind == yamlRenderMapping || source.kind == yamlRenderSequence {
			containerDepth = 1
			if parentDepth == bundle.MaxYAMLPhysicalDepth {
				return yamlRenderWalkMemo{}, limitError(bundle.YAMLResourcePhysicalDepth, bundle.MaxYAMLPhysicalDepth)
			}
		}
		currentDepth := parentDepth + containerDepth
		result := yamlRenderWalkMemo{clone: *source, containerDepth: containerDepth, expansion: 1}
		if source.mapping != nil {
			result.clone.mapping = make([]yamlRenderEntry, len(source.mapping))
			for i := range source.mapping {
				result.clone.mapping[i].Key = source.mapping[i].Key
				child, err := visit(&source.mapping[i].Value, currentDepth)
				if err != nil {
					return yamlRenderWalkMemo{}, err
				}
				result.clone.mapping[i].Value = child.clone
				result.containerDepth = max(result.containerDepth, containerDepth+child.containerDepth)
				if child.expansion > expansionCap-result.expansion {
					result.expansion = expansionCap
				} else {
					result.expansion += child.expansion
				}
			}
		}
		if source.sequence != nil {
			result.clone.sequence = make([]yamlRenderValue, len(source.sequence))
			for i := range source.sequence {
				child, err := visit(&source.sequence[i], currentDepth)
				if err != nil {
					return yamlRenderWalkMemo{}, err
				}
				result.clone.sequence[i] = child.clone
				result.containerDepth = max(result.containerDepth, containerDepth+child.containerDepth)
				if child.expansion > expansionCap-result.expansion {
					result.expansion = expansionCap
				} else {
					result.expansion += child.expansion
				}
			}
		}
		if hasIdentity {
			memo[id] = result
		}
		return result, ctx.Err()
	}
	result, err := visit(&value, 0)
	if err != nil {
		return err
	}
	if result.expansion > bundle.MaxYAMLSemanticExpansion {
		return limitError(bundle.YAMLResourceSemanticExpansion, bundle.MaxYAMLSemanticExpansion)
	}
	if target != nil {
		*target = result.clone
	}
	return ctx.Err()
}

var (
	errYAMLDesiredConflict           = errors.New("conflicting YAML desired values")
	errYAMLDuplicateSequenceIdentity = errors.New("duplicate YAML sequence identity")
	errInvalidSemanticEdit           = errors.New("invalid semantic edit")
)

func validateYAMLDesiredUniquenessContext(ctx context.Context, value yamlRenderValue, route string) error {
	if err := walkYAMLRenderValueContext(ctx, value, nil); err != nil {
		return err
	}
	return validateYAMLDesiredUniquenessUncheckedContext(ctx, value, route)
}

func validateYAMLDesiredUniquenessUncheckedContext(ctx context.Context, value yamlRenderValue, route string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch value.kind {
	case yamlRenderMapping:
		for _, entry := range value.mapping {
			if err := validateYAMLDesiredUniquenessUncheckedContext(ctx, entry.Value, entry.Key); err != nil {
				return err
			}
		}
	case yamlRenderSequence:
		if isSequenceIdentityRoute(route) {
			if _, err := sequenceIdentitiesContext(ctx, value, route); err != nil {
				return err
			}
		}
		for _, item := range value.sequence {
			if err := validateYAMLDesiredUniquenessUncheckedContext(ctx, item, ""); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func isSequenceIdentityRoute(route string) bool {
	switch route {
	case "sources", "verified", "parameters":
		return true
	default:
		return false
	}
}

func unchangedYAMLDesiredContext(ctx context.Context, value yamlRenderValue, route string) (yamlRenderValue, error) {
	cloned, err := cloneYAMLRenderValueContext(ctx, value)
	if err != nil {
		return yamlRenderValue{}, err
	}
	if err := validateYAMLDesiredUniquenessContext(ctx, cloned, route); err != nil {
		return yamlRenderValue{}, err
	}
	return cloned, nil
}

// mergeYAMLDesired performs an order-independent three-way fold. A single
// changed branch wins, equal changes coalesce, and unequal changes to the same
// leaf are rejected rather than silently choosing the later call.

func mergeYAMLDesiredContext(ctx context.Context, base, accumulated, whole yamlRenderValue, route string) (yamlRenderValue, error) {
	if err := ctx.Err(); err != nil {
		return yamlRenderValue{}, err
	}
	accumulatedBaseEqual, err := equalYAMLRenderStateContext(ctx, accumulated, base)
	if err != nil {
		return yamlRenderValue{}, err
	}
	wholeBaseEqual, err := equalYAMLRenderStateContext(ctx, whole, base)
	if err != nil {
		return yamlRenderValue{}, err
	}
	branchesEqual, err := equalYAMLRenderStateContext(ctx, accumulated, whole)
	if err != nil {
		return yamlRenderValue{}, err
	}
	switch {
	case accumulatedBaseEqual:
		return unchangedYAMLDesiredContext(ctx, whole, route)
	case wholeBaseEqual:
		return unchangedYAMLDesiredContext(ctx, accumulated, route)
	case branchesEqual:
		return unchangedYAMLDesiredContext(ctx, whole, route)
	case base.kind != accumulated.kind || base.kind != whole.kind:
		return yamlRenderValue{}, errYAMLDesiredConflict
	}
	switch base.kind {
	case yamlRenderMapping:
		return mergeYAMLDesiredMappingContext(ctx, base, accumulated, whole)
	case yamlRenderSequence:
		merged, _, err := mergeYAMLDesiredSequenceWithLineageContext(ctx, base, accumulated, whole, route, nil)
		return merged, err
	default:
		return yamlRenderValue{}, errYAMLDesiredConflict
	}
}

func yamlRenderMappingEntryContext(ctx context.Context, value yamlRenderValue, key string) (yamlRenderValue, bool, error) {
	for _, entry := range value.mapping {
		equal, err := stringsEqualContext(ctx, entry.Key, key)
		if err != nil {
			return yamlRenderValue{}, false, err
		}
		if equal {
			return entry.Value, true, nil
		}
	}
	return yamlRenderValue{}, false, ctx.Err()
}

func equalYAMLRenderOptionalContext(
	ctx context.Context,
	left yamlRenderValue,
	leftPresent bool,
	right yamlRenderValue,
	rightPresent bool,
) (bool, error) {
	if leftPresent != rightPresent || !leftPresent {
		return leftPresent == rightPresent, ctx.Err()
	}
	return equalYAMLRenderStateContext(ctx, left, right)
}

func mergeYAMLDesiredOptionalContext(
	ctx context.Context,
	base yamlRenderValue,
	basePresent bool,
	accumulated yamlRenderValue,
	accumulatedPresent bool,
	whole yamlRenderValue,
	wholePresent bool,
	route string,
) (yamlRenderValue, bool, error) {
	if err := ctx.Err(); err != nil {
		return yamlRenderValue{}, false, err
	}
	accumulatedBaseEqual, err := equalYAMLRenderOptionalContext(ctx, accumulated, accumulatedPresent, base, basePresent)
	if err != nil {
		return yamlRenderValue{}, false, err
	}
	wholeBaseEqual, err := equalYAMLRenderOptionalContext(ctx, whole, wholePresent, base, basePresent)
	if err != nil {
		return yamlRenderValue{}, false, err
	}
	branchesEqual, err := equalYAMLRenderOptionalContext(ctx, accumulated, accumulatedPresent, whole, wholePresent)
	if err != nil {
		return yamlRenderValue{}, false, err
	}
	switch {
	case accumulatedBaseEqual:
		cloned, err := cloneYAMLRenderValueContext(ctx, whole)
		return cloned, wholePresent, err
	case wholeBaseEqual:
		cloned, err := cloneYAMLRenderValueContext(ctx, accumulated)
		return cloned, accumulatedPresent, err
	case branchesEqual:
		cloned, err := cloneYAMLRenderValueContext(ctx, whole)
		return cloned, wholePresent, err
	case basePresent && accumulatedPresent && wholePresent:
		merged, err := mergeYAMLDesiredContext(ctx, base, accumulated, whole, route)
		return merged, err == nil, err
	default:
		return yamlRenderValue{}, false, errYAMLDesiredConflict
	}
}

func mergeYAMLDesiredMappingContext(ctx context.Context, base, accumulated, whole yamlRenderValue) (yamlRenderValue, error) {
	keys := make([]string, 0, len(base.mapping)+len(accumulated.mapping)+len(whole.mapping))
	seen := make(map[string]bool, cap(keys))
	indexes := make([]map[string]yamlRenderValue, 3)
	for branch, entries := range [][]yamlRenderEntry{base.mapping, accumulated.mapping, whole.mapping} {
		indexes[branch] = make(map[string]yamlRenderValue, len(entries))
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return yamlRenderValue{}, err
			}
			indexes[branch][entry.Key] = entry.Value
			if !seen[entry.Key] {
				seen[entry.Key] = true
				keys = append(keys, entry.Key)
			}
		}
	}
	mergedValues := make(map[string]yamlRenderValue, len(keys))
	presentKeys := make(map[string]bool, len(keys))
	for _, key := range keys {
		baseValue, basePresent := indexes[0][key]
		accumulatedValue, accumulatedPresent := indexes[1][key]
		wholeValue, wholePresent := indexes[2][key]
		value, present, err := mergeYAMLDesiredOptionalContext(
			ctx,
			baseValue, basePresent,
			accumulatedValue, accumulatedPresent,
			wholeValue, wholePresent,
			key,
		)
		if err != nil {
			return yamlRenderValue{}, err
		}
		if present {
			mergedValues[key] = value
			presentKeys[key] = true
		}
	}
	order, err := mergeYAMLMappingKeyOrderContext(ctx, base, accumulated, whole, presentKeys)
	if err != nil {
		return yamlRenderValue{}, err
	}
	merged := yamlMapping()
	for _, key := range order {
		merged.mapping = append(merged.mapping, yamlEntry(key, mergedValues[key]))
	}
	return merged, ctx.Err()
}

func mergeYAMLMappingKeyOrderContext(
	ctx context.Context,
	base, accumulated, whole yamlRenderValue,
	present map[string]bool,
) ([]string, error) {
	baseKeys := make([]string, 0, len(base.mapping))
	baseSet := make(map[string]bool, len(base.mapping))
	for _, entry := range base.mapping {
		if present[entry.Key] {
			baseKeys = append(baseKeys, entry.Key)
		}
		baseSet[entry.Key] = true
	}

	branchKeys := func(branch yamlRenderValue) []string {
		keys := make([]string, 0, len(branch.mapping))
		for _, entry := range branch.mapping {
			if present[entry.Key] {
				keys = append(keys, entry.Key)
			}
		}
		return keys
	}
	branchBaseKeys := func(keys []string) []string {
		filtered := make([]string, 0, len(keys))
		for _, key := range keys {
			if baseSet[key] {
				filtered = append(filtered, key)
			}
		}
		return filtered
	}
	isReordered := func(keys []string) bool {
		filtered := branchBaseKeys(keys)
		expected := make([]string, 0, len(filtered))
		inBranch := make(map[string]bool, len(filtered))
		for _, key := range filtered {
			inBranch[key] = true
		}
		for _, key := range baseKeys {
			if inBranch[key] {
				expected = append(expected, key)
			}
		}
		return !slices.Equal(filtered, expected)
	}

	accumulatedKeys := branchKeys(accumulated)
	wholeKeys := branchKeys(whole)
	accumulatedReordered := isReordered(accumulatedKeys)
	wholeReordered := isReordered(wholeKeys)

	edges := make(map[string]map[string]bool, len(present))
	indegree := make(map[string]int, len(present))
	for key, keep := range present {
		if keep {
			edges[key] = make(map[string]bool)
			indegree[key] = 0
		}
	}
	addEdge := func(from, to string) {
		if from == to || !present[from] || !present[to] || edges[from][to] {
			return
		}
		edges[from][to] = true
		indegree[to]++
	}
	addAdjacent := func(keys []string, includeBaseOrder bool) {
		for i := 0; i+1 < len(keys); i++ {
			from, to := keys[i], keys[i+1]
			if includeBaseOrder || !baseSet[from] || !baseSet[to] {
				addEdge(from, to)
			}
		}
	}

	switch {
	case accumulatedReordered && wholeReordered:
		addAdjacent(accumulatedKeys, true)
		addAdjacent(wholeKeys, true)
	case accumulatedReordered:
		addAdjacent(accumulatedKeys, true)
	case wholeReordered:
		addAdjacent(wholeKeys, true)
	default:
		addAdjacent(baseKeys, true)
	}
	addAdjacent(accumulatedKeys, false)
	addAdjacent(wholeKeys, false)

	ready := make([]string, 0, len(indegree))
	for key, degree := range indegree {
		if degree == 0 {
			ready = pushStringMinHeap(ready, key)
		}
	}
	order := make([]string, 0, len(indegree))
	for len(ready) != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var key string
		ready, key = popStringMinHeap(ready)
		order = append(order, key)
		for candidate := range edges[key] {
			indegree[candidate]--
			if indegree[candidate] == 0 {
				ready = pushStringMinHeap(ready, candidate)
			}
		}
	}
	if len(order) != len(indegree) {
		return nil, errYAMLDesiredConflict
	}
	return order, ctx.Err()
}

func pushStringMinHeap(values []string, value string) []string {
	values = append(values, value)
	for index := len(values) - 1; index > 0; {
		parent := (index - 1) / 2
		if values[parent] <= values[index] {
			break
		}
		values[parent], values[index] = values[index], values[parent]
		index = parent
	}
	return values
}

func popStringMinHeap(values []string) ([]string, string) {
	minimum := values[0]
	last := values[len(values)-1]
	values = values[:len(values)-1]
	if len(values) == 0 {
		return values, minimum
	}
	values[0] = last
	for index := 0; ; {
		left := index*2 + 1
		if left >= len(values) {
			break
		}
		smallest := left
		right := left + 1
		if right < len(values) && values[right] < values[left] {
			smallest = right
		}
		if values[index] <= values[smallest] {
			break
		}
		values[index], values[smallest] = values[smallest], values[index]
		index = smallest
	}
	return values, minimum
}

type yamlSequenceIdentity struct {
	kind            string
	fields          []yamlSelectorField
	exact           yamlRenderValue
	canonical       string
	canonicalDigest [sha256.Size]byte
}

func sequenceIdentityContext(ctx context.Context, value yamlRenderValue, route string) (yamlSequenceIdentity, error) {
	var identity yamlSequenceIdentity
	if value.kind == yamlRenderMapping {
		var keys []string
		switch route {
		case "sources":
			if _, present := yamlRenderMappingValue(value, "id"); present {
				keys = []string{"id"}
			}
		case "verified":
			keys = []string{"by", "at"}
		case "parameters":
			keys = []string{"name"}
		}
		if len(keys) != 0 {
			identity = yamlSequenceIdentity{kind: route}
			for _, key := range keys {
				field, present := yamlRenderMappingValue(value, key)
				if !present {
					cloned, err := cloneYAMLRenderValueContext(ctx, value)
					if err != nil {
						return yamlSequenceIdentity{}, err
					}
					identity = yamlSequenceIdentity{kind: "exact", exact: cloned}
					break
				}
				cloned, err := cloneYAMLRenderValueContext(ctx, field)
				if err != nil {
					return yamlSequenceIdentity{}, err
				}
				identity.fields = append(identity.fields, yamlSelector(key, cloned))
			}
			if identity.kind == route {
				canonical, err := sequenceIdentityCanonicalContext(ctx, identity)
				identity.canonical = canonical
				identity.canonicalDigest, err = stringFingerprintContext(ctx, canonical)
				return identity, err
			}
		}
	}
	if identity.kind == "" {
		cloned, err := cloneYAMLRenderValueContext(ctx, value)
		if err != nil {
			return yamlSequenceIdentity{}, err
		}
		identity = yamlSequenceIdentity{kind: "exact", exact: cloned}
	}
	canonical, err := sequenceIdentityCanonicalContext(ctx, identity)
	identity.canonical = canonical
	if err == nil {
		identity.canonicalDigest, err = stringFingerprintContext(ctx, canonical)
	}
	return identity, err
}

func equalSequenceIdentityContext(ctx context.Context, left, right yamlSequenceIdentity) (bool, error) {
	if left.kind != right.kind || len(left.fields) != len(right.fields) {
		return false, nil
	}
	leftCanonical := left.canonical
	if leftCanonical == "" {
		var err error
		leftCanonical, err = sequenceIdentityCanonicalContext(ctx, left)
		if err != nil {
			return false, err
		}
	}
	rightCanonical := right.canonical
	if rightCanonical == "" {
		var err error
		rightCanonical, err = sequenceIdentityCanonicalContext(ctx, right)
		if err != nil {
			return false, err
		}
	}
	return stringsEqualContext(ctx, leftCanonical, rightCanonical)
}

func sequenceIdentityCanonicalContext(ctx context.Context, identity yamlSequenceIdentity) (string, error) {
	var builder strings.Builder
	if err := appendCanonicalFrameContext(ctx, &builder, identity.kind); err != nil {
		return "", err
	}
	if identity.kind == "exact" {
		value, err := yamlRenderCanonicalKeyContext(ctx, identity.exact, "")
		if err != nil {
			return "", err
		}
		if err := appendCanonicalFrameContext(ctx, &builder, value); err != nil {
			return "", err
		}
		return builder.String(), ctx.Err()
	}
	for _, field := range identity.fields {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := appendCanonicalFrameContext(ctx, &builder, field.Key); err != nil {
			return "", err
		}
		value, err := yamlRenderCanonicalKeyContext(ctx, field.Value, field.Key)
		if err != nil {
			return "", err
		}
		if err := appendCanonicalFrameContext(ctx, &builder, value); err != nil {
			return "", err
		}
	}
	return builder.String(), ctx.Err()
}

func sequenceLineageCanonicalContext(ctx context.Context, lineage yamlSequenceLineage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if lineage.baseIndex >= 0 {
		return "b" + strconv.Itoa(lineage.baseIndex), nil
	}
	canonical := lineage.added.canonical
	if canonical == "" {
		var err error
		canonical, err = sequenceIdentityCanonicalContext(ctx, lineage.added)
		if err != nil {
			return "", err
		}
	}
	return "a" + canonical, ctx.Err()
}

func yamlRenderCanonicalKeyContext(ctx context.Context, value yamlRenderValue, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if temporal, ok := yamlRenderTemporalCanonical(value, key); ok {
		return temporal, nil
	}
	var builder strings.Builder
	switch value.kind {
	case yamlRenderString:
		if err := appendCanonicalFramesContext(ctx, &builder, "s", value.text); err != nil {
			return "", err
		}
	case yamlRenderBool:
		if err := appendCanonicalFramesContext(ctx, &builder, "b", strconv.FormatBool(value.boolean)); err != nil {
			return "", err
		}
	case yamlRenderInt:
		if err := appendCanonicalFramesContext(ctx, &builder, "n", strconv.FormatInt(value.integer, 10)); err != nil {
			return "", err
		}
	case yamlRenderUint:
		if err := appendCanonicalFramesContext(ctx, &builder, "n", strconv.FormatUint(value.unsigned, 10)); err != nil {
			return "", err
		}
	case yamlRenderDate, yamlRenderDateTime:
		if err := appendCanonicalFramesContext(ctx, &builder, "t", value.text); err != nil {
			return "", err
		}
	case yamlRenderMapping:
		type canonicalEntry struct {
			text   string
			digest [sha256.Size]byte
		}
		entries := make([]canonicalEntry, len(value.mapping))
		for index, entry := range value.mapping {
			child, err := yamlRenderCanonicalKeyContext(ctx, entry.Value, entry.Key)
			if err != nil {
				return "", err
			}
			var encoded strings.Builder
			if err := appendCanonicalFramesContext(ctx, &encoded, entry.Key, child); err != nil {
				return "", err
			}
			entries[index].text = encoded.String()
			entries[index].digest, err = stringFingerprintContext(ctx, entries[index].text)
			if err != nil {
				return "", err
			}
		}
		var compareErr error
		if err := sortSliceContext(ctx, entries, func(left, right canonicalEntry) bool {
			if compared := bytes.Compare(left.digest[:], right.digest[:]); compared != 0 {
				return compared < 0
			}
			compared, err := stringsCompareContext(ctx, left.text, right.text)
			if err != nil {
				compareErr = err
				return false
			}
			return compared < 0
		}); err != nil {
			return "", err
		}
		if compareErr != nil {
			return "", compareErr
		}
		if err := appendCanonicalFrameContext(ctx, &builder, "m"); err != nil {
			return "", err
		}
		for _, entry := range entries {
			if err := appendCanonicalFrameContext(ctx, &builder, entry.text); err != nil {
				return "", err
			}
		}
	case yamlRenderSequence:
		if err := appendCanonicalFrameContext(ctx, &builder, "q"); err != nil {
			return "", err
		}
		for _, item := range value.sequence {
			child, err := yamlRenderCanonicalKeyContext(ctx, item, key)
			if err != nil {
				return "", err
			}
			if err := appendCanonicalFrameContext(ctx, &builder, child); err != nil {
				return "", err
			}
		}
	default:
		if err := appendCanonicalFrameContext(ctx, &builder, "invalid"); err != nil {
			return "", err
		}
	}
	return builder.String(), ctx.Err()
}

func yamlRenderTemporalCanonical(value yamlRenderValue, key string) (string, bool) {
	if !isTemporalSelectorKey(key) && value.kind != yamlRenderDate && value.kind != yamlRenderDateTime {
		return "", false
	}
	if value.kind != yamlRenderString && value.kind != yamlRenderDate && value.kind != yamlRenderDateTime {
		return "", false
	}
	text := value.text
	if len(text) == len("2006-01-02") {
		if parsed, err := time.Parse("2006-01-02", text); err == nil {
			return "d" + parsed.Format("2006-01-02"), true
		}
	}
	if len(text) >= len("2006-01-02T00:00:00Z") && len(text) <= len("2006-01-02T00:00:00.999999999+00:00") {
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return "t" + parsed.UTC().Format(time.RFC3339Nano), true
		}
	}
	return "", false
}

func appendCanonicalFrameContext(ctx context.Context, builder *strings.Builder, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	builder.WriteString(strconv.Itoa(len(value)))
	builder.WriteByte(':')
	const chunkSize = 64 << 10
	for offset := 0; offset < len(value); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		builder.WriteString(value[offset:min(offset+chunkSize, len(value))])
	}
	return ctx.Err()
}

func appendCanonicalFramesContext(ctx context.Context, builder *strings.Builder, values ...string) error {
	for _, value := range values {
		if err := appendCanonicalFrameContext(ctx, builder, value); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func stringsCompareContext(ctx context.Context, left, right string) (int, error) {
	const chunkSize = 64 << 10
	limit := min(len(left), len(right))
	for offset := 0; offset < limit; offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		end := min(offset+chunkSize, limit)
		if compared := strings.Compare(left[offset:end], right[offset:end]); compared != 0 {
			return compared, nil
		}
	}
	return len(left) - len(right), ctx.Err()
}
