package mutation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

func sequenceIdentitiesContext(ctx context.Context, sequence yamlRenderValue, route string) ([]yamlSequenceIdentity, error) {
	identities := make([]yamlSequenceIdentity, len(sequence.sequence))
	buckets := make(map[[sha256.Size]byte][]int, len(sequence.sequence))
	for i, item := range sequence.sequence {
		identity, err := sequenceIdentityContext(ctx, item, route)
		if err != nil {
			return nil, err
		}
		identities[i] = identity
		if !isSequenceIdentityRoute(route) {
			continue
		}
		for _, j := range buckets[identity.canonicalDigest] {
			equal, err := equalSequenceIdentityContext(ctx, identities[i], identities[j])
			if err != nil {
				return nil, err
			}
			if equal {
				return nil, errYAMLDuplicateSequenceIdentity
			}
		}
		buckets[identity.canonicalDigest] = append(buckets[identity.canonicalDigest], i)
	}
	return identities, ctx.Err()
}

type sequenceIdentityLookup struct {
	identities []yamlSequenceIdentity
	buckets    map[[sha256.Size]byte][]int
}

func newSequenceIdentityLookupContext(ctx context.Context, identities []yamlSequenceIdentity) (sequenceIdentityLookup, error) {
	lookup := sequenceIdentityLookup{identities: identities, buckets: make(map[[sha256.Size]byte][]int, len(identities))}
	for index := range identities {
		if err := ctx.Err(); err != nil {
			return sequenceIdentityLookup{}, err
		}
		identity := identities[index]
		if identity.canonical == "" {
			var err error
			identity.canonical, err = sequenceIdentityCanonicalContext(ctx, identity)
			if err != nil {
				return sequenceIdentityLookup{}, err
			}
		}
		if identity.canonicalDigest == ([sha256.Size]byte{}) {
			var err error
			identity.canonicalDigest, err = stringFingerprintContext(ctx, identity.canonical)
			if err != nil {
				return sequenceIdentityLookup{}, err
			}
		}
		lookup.identities[index] = identity
		lookup.buckets[identity.canonicalDigest] = append(lookup.buckets[identity.canonicalDigest], index)
	}
	return lookup, ctx.Err()
}

func (lookup sequenceIdentityLookup) indexContext(ctx context.Context, identity yamlSequenceIdentity) (int, bool, error) {
	if identity.canonical == "" {
		var err error
		identity.canonical, err = sequenceIdentityCanonicalContext(ctx, identity)
		if err != nil {
			return 0, false, err
		}
	}
	if identity.canonicalDigest == ([sha256.Size]byte{}) {
		var err error
		identity.canonicalDigest, err = stringFingerprintContext(ctx, identity.canonical)
		if err != nil {
			return 0, false, err
		}
	}
	for _, index := range lookup.buckets[identity.canonicalDigest] {
		equal, err := equalSequenceIdentityContext(ctx, lookup.identities[index], identity)
		if err != nil {
			return 0, false, err
		}
		if equal {
			return index, true, nil
		}
	}
	return 0, false, ctx.Err()
}

func (lookup sequenceIdentityLookup) uniqueIndexContext(ctx context.Context, identity yamlSequenceIdentity) (int, bool, error) {
	if identity.canonical == "" {
		var err error
		identity.canonical, err = sequenceIdentityCanonicalContext(ctx, identity)
		if err != nil {
			return 0, false, err
		}
	}
	if identity.canonicalDigest == ([sha256.Size]byte{}) {
		var err error
		identity.canonicalDigest, err = stringFingerprintContext(ctx, identity.canonical)
		if err != nil {
			return 0, false, err
		}
	}
	match := -1
	for _, index := range lookup.buckets[identity.canonicalDigest] {
		equal, err := equalSequenceIdentityContext(ctx, lookup.identities[index], identity)
		if err != nil {
			return 0, false, err
		}
		if !equal {
			continue
		}
		if match >= 0 {
			return 0, false, nil
		}
		match = index
	}
	return match, match >= 0, ctx.Err()
}

type yamlSequenceLineage struct {
	baseIndex int
	added     yamlSequenceIdentity
	appended  bool
}

func equalSequenceLineageContext(ctx context.Context, left, right yamlSequenceLineage) (bool, error) {
	if left.baseIndex >= 0 || right.baseIndex >= 0 {
		return left.baseIndex == right.baseIndex, ctx.Err()
	}
	return equalSequenceIdentityContext(ctx, left.added, right.added)
}

type sequenceLineageLookup struct {
	lineages []yamlSequenceLineage
	buckets  map[[sha256.Size]byte][]int
}

func newSequenceLineageLookupContext(ctx context.Context, lineages []yamlSequenceLineage) (sequenceLineageLookup, error) {
	lookup := sequenceLineageLookup{lineages: lineages, buckets: make(map[[sha256.Size]byte][]int, len(lineages))}
	for index, lineage := range lineages {
		key, err := sequenceLineageCanonicalContext(ctx, lineage)
		if err != nil {
			return sequenceLineageLookup{}, err
		}
		digest, err := stringFingerprintContext(ctx, key)
		if err != nil {
			return sequenceLineageLookup{}, err
		}
		lookup.buckets[digest] = append(lookup.buckets[digest], index)
	}
	return lookup, ctx.Err()
}

func (lookup sequenceLineageLookup) indexContext(ctx context.Context, lineage yamlSequenceLineage) (int, bool, error) {
	key, err := sequenceLineageCanonicalContext(ctx, lineage)
	if err != nil {
		return 0, false, err
	}
	digest, err := stringFingerprintContext(ctx, key)
	if err != nil {
		return 0, false, err
	}
	for _, index := range lookup.buckets[digest] {
		equal, err := equalSequenceLineageContext(ctx, lookup.lineages[index], lineage)
		if err != nil {
			return 0, false, err
		}
		if equal {
			return index, true, nil
		}
	}
	return 0, false, ctx.Err()
}

func alignSequenceLineageContext(
	ctx context.Context,
	baseIDs, branchIDs []yamlSequenceIdentity,
	allowIdentityChange bool,
) ([]yamlSequenceLineage, error) {
	lineages := make([]yamlSequenceLineage, len(branchIDs))
	for i := range lineages {
		lineages[i].baseIndex = -1
	}
	baseLookup, err := newSequenceIdentityLookupContext(ctx, baseIDs)
	if err != nil {
		return nil, err
	}
	usedBase := make([]bool, len(baseIDs))
	usedBranch := make([]bool, len(branchIDs))
	for branchIndex, identity := range branchIDs {
		if identity.canonical == "" {
			identity.canonical, err = sequenceIdentityCanonicalContext(ctx, identity)
			if err != nil {
				return nil, err
			}
		}
		if identity.canonicalDigest == ([sha256.Size]byte{}) {
			identity.canonicalDigest, err = stringFingerprintContext(ctx, identity.canonical)
			if err != nil {
				return nil, err
			}
		}
		for _, baseIndex := range baseLookup.buckets[identity.canonicalDigest] {
			equal, equalErr := equalSequenceIdentityContext(ctx, identity, baseIDs[baseIndex])
			if equalErr != nil {
				return nil, equalErr
			}
			if !usedBase[baseIndex] && equal {
				lineages[branchIndex] = yamlSequenceLineage{baseIndex: baseIndex}
				usedBase[baseIndex] = true
				usedBranch[branchIndex] = true
				break
			}
		}
	}
	if allowIdentityChange {
		var unmatchedBase, unmatchedBranch []int
		for index, used := range usedBase {
			if !used {
				unmatchedBase = append(unmatchedBase, index)
			}
		}
		for index, used := range usedBranch {
			if !used {
				unmatchedBranch = append(unmatchedBranch, index)
			}
		}
		if len(unmatchedBase) == len(unmatchedBranch) {
			for i := range unmatchedBase {
				lineages[unmatchedBranch[i]] = yamlSequenceLineage{baseIndex: unmatchedBase[i]}
				usedBranch[unmatchedBranch[i]] = true
			}
		}
	}
	for index, used := range usedBranch {
		if !used {
			lineages[index] = yamlSequenceLineage{baseIndex: -1, added: branchIDs[index]}
		}
	}
	return lineages, ctx.Err()
}

func equalSequenceOrderContext(ctx context.Context, left, right []yamlSequenceLineage) (bool, error) {
	if len(left) != len(right) {
		return false, nil
	}
	for i := range left {
		equal, err := equalSequenceLineageContext(ctx, left[i], right[i])
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, ctx.Err()
}

func sequenceOrderReordersBase(lineages []yamlSequenceLineage) bool {
	previous := -1
	for _, lineage := range lineages {
		if lineage.baseIndex < 0 {
			continue
		}
		if lineage.baseIndex < previous {
			return true
		}
		previous = lineage.baseIndex
	}
	return false
}

func filterSequenceLineagesContext(
	ctx context.Context,
	lineages, present []yamlSequenceLineage,
	baseOnly bool,
) ([]yamlSequenceLineage, error) {
	lookup, err := newSequenceLineageLookupContext(ctx, present)
	if err != nil {
		return nil, err
	}
	filtered := make([]yamlSequenceLineage, 0, len(lineages))
	for _, lineage := range lineages {
		if baseOnly && lineage.baseIndex < 0 {
			continue
		}
		if _, exists, lookupErr := lookup.indexContext(ctx, lineage); lookupErr != nil {
			return nil, lookupErr
		} else if exists {
			filtered = append(filtered, lineage)
		}
	}
	return filtered, ctx.Err()
}

func mergeSequenceLineageOrderContext(
	ctx context.Context,
	baseOrder, accumulatedOrder, wholeOrder, present []yamlSequenceLineage,
) ([]yamlSequenceLineage, error) {
	accumulatedReordered := sequenceOrderReordersBase(accumulatedOrder)
	wholeReordered := sequenceOrderReordersBase(wholeOrder)
	accumulatedBase, err := filterSequenceLineagesContext(ctx, accumulatedOrder, present, true)
	if err != nil {
		return nil, err
	}
	wholeBase, err := filterSequenceLineagesContext(ctx, wholeOrder, present, true)
	if err != nil {
		return nil, err
	}
	var orderedBase []yamlSequenceLineage
	switch {
	case accumulatedReordered && wholeReordered:
		equal, err := equalSequenceOrderContext(ctx, accumulatedBase, wholeBase)
		if err != nil {
			return nil, err
		}
		if !equal {
			return nil, errYAMLDesiredConflict
		}
		orderedBase = accumulatedBase
	case accumulatedReordered:
		orderedBase = accumulatedBase
	case wholeReordered:
		orderedBase = wholeBase
	default:
		orderedBase, err = filterSequenceLineagesContext(ctx, baseOrder, present, true)
		if err != nil {
			return nil, err
		}
	}

	presentLookup, err := newSequenceLineageLookupContext(ctx, present)
	if err != nil {
		return nil, err
	}
	edges := make([]map[int]struct{}, len(present))
	indegree := make([]int, len(present))
	addEdge := func(left, right yamlSequenceLineage) error {
		from, fromPresent, err := presentLookup.indexContext(ctx, left)
		if err != nil {
			return err
		}
		to, toPresent, err := presentLookup.indexContext(ctx, right)
		if err != nil {
			return err
		}
		if !fromPresent || !toPresent || from == to {
			return nil
		}
		if edges[from] == nil {
			edges[from] = make(map[int]struct{})
		}
		if _, exists := edges[from][to]; exists {
			return nil
		}
		edges[from][to] = struct{}{}
		indegree[to]++
		return ctx.Err()
	}
	for i := 0; i+1 < len(orderedBase); i++ {
		if err := addEdge(orderedBase[i], orderedBase[i+1]); err != nil {
			return nil, err
		}
	}
	for _, branch := range [][]yamlSequenceLineage{accumulatedOrder, wholeOrder} {
		filtered, err := filterSequenceLineagesContext(ctx, branch, present, false)
		if err != nil {
			return nil, err
		}
		for i := 0; i+1 < len(filtered); i++ {
			if filtered[i].baseIndex < 0 || filtered[i+1].baseIndex < 0 {
				if err := addEdge(filtered[i], filtered[i+1]); err != nil {
					return nil, err
				}
			}
		}
	}

	order := make([]yamlSequenceLineage, 0, len(present))
	ready := make(map[int]struct{}, len(present))
	for index, degree := range indegree {
		if degree == 0 {
			ready[index] = struct{}{}
		}
	}
	for len(order) < len(present) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(ready) == 0 {
			return nil, errYAMLDesiredConflict
		}
		candidate := -1
		if len(ready) == 1 {
			for index := range ready {
				candidate = index
			}
		} else {
			preferred := -1
			for index := range ready {
				if present[index].baseIndex < 0 && !present[index].appended {
					if preferred >= 0 {
						return nil, errYAMLDesiredConflict
					}
					preferred = index
					continue
				}
				if present[index].baseIndex >= 0 || !present[index].appended {
					return nil, errYAMLDesiredConflict
				}
			}
			if preferred < 0 {
				// Concurrent sequence additions without a shared base-relative
				// order are semantically unordered. Reject instead of sorting
				// identities or choosing the invocation order.
				return nil, errYAMLDesiredConflict
			}
			candidate = preferred
		}
		delete(ready, candidate)
		order = append(order, present[candidate])
		for to := range edges[candidate] {
			indegree[to]--
			if indegree[to] == 0 {
				ready[to] = struct{}{}
			}
		}
	}
	return order, ctx.Err()
}

func mergeGenericYAMLDesiredSequenceContext(ctx context.Context, base, accumulated, whole yamlRenderValue) (yamlRenderValue, error) {
	if len(base.sequence) != len(accumulated.sequence) || len(base.sequence) != len(whole.sequence) {
		return yamlRenderValue{}, errYAMLDesiredConflict
	}
	merged := yamlSequence()
	for i := range base.sequence {
		value, err := mergeYAMLDesiredContext(ctx, base.sequence[i], accumulated.sequence[i], whole.sequence[i], "")
		if err != nil {
			return yamlRenderValue{}, err
		}
		merged.sequence = append(merged.sequence, value)
	}
	return merged, ctx.Err()
}

func mergeYAMLDesiredSequenceWithLineageContext(
	ctx context.Context,
	base, accumulated, whole yamlRenderValue,
	route string,
	accumulatedLineage []yamlSequenceLineage,
) (yamlRenderValue, []yamlSequenceLineage, error) {
	if !isSequenceIdentityRoute(route) {
		merged, err := mergeGenericYAMLDesiredSequenceContext(ctx, base, accumulated, whole)
		return merged, nil, err
	}
	baseIDs, err := sequenceIdentitiesContext(ctx, base, route)
	if err != nil {
		return yamlRenderValue{}, nil, err
	}
	accumulatedIDs, err := sequenceIdentitiesContext(ctx, accumulated, route)
	if err != nil {
		return yamlRenderValue{}, nil, err
	}
	wholeIDs, err := sequenceIdentitiesContext(ctx, whole, route)
	if err != nil {
		return yamlRenderValue{}, nil, err
	}
	baseOrder := make([]yamlSequenceLineage, len(baseIDs))
	for i := range baseOrder {
		baseOrder[i] = yamlSequenceLineage{baseIndex: i}
	}
	accumulatedOrder := append([]yamlSequenceLineage(nil), accumulatedLineage...)
	if len(accumulatedOrder) == 0 {
		accumulatedOrder, err = alignSequenceLineageContext(ctx, baseIDs, accumulatedIDs, false)
		if err != nil {
			return yamlRenderValue{}, nil, err
		}
	}
	if len(accumulatedOrder) != len(accumulatedIDs) {
		return yamlRenderValue{}, nil, errYAMLDesiredConflict
	}
	accumulatedSeen := make(map[[sha256.Size]byte][]int, len(accumulatedOrder))
	for i, lineage := range accumulatedOrder {
		if lineage.baseIndex >= len(baseIDs) {
			return yamlRenderValue{}, nil, errYAMLDesiredConflict
		}
		key, keyErr := sequenceLineageCanonicalContext(ctx, lineage)
		if keyErr != nil {
			return yamlRenderValue{}, nil, keyErr
		}
		digest, digestErr := stringFingerprintContext(ctx, key)
		if digestErr != nil {
			return yamlRenderValue{}, nil, digestErr
		}
		for _, j := range accumulatedSeen[digest] {
			equal, equalErr := equalSequenceLineageContext(ctx, lineage, accumulatedOrder[j])
			if equalErr != nil {
				return yamlRenderValue{}, nil, equalErr
			}
			if equal {
				return yamlRenderValue{}, nil, errYAMLDesiredConflict
			}
		}
		accumulatedSeen[digest] = append(accumulatedSeen[digest], i)
	}
	wholeOrder, err := alignSequenceLineageContext(ctx, baseIDs, wholeIDs, false)
	if err != nil {
		return yamlRenderValue{}, nil, err
	}
	allLineages := append([]yamlSequenceLineage(nil), baseOrder...)
	allLookup, err := newSequenceLineageLookupContext(ctx, allLineages)
	if err != nil {
		return yamlRenderValue{}, nil, err
	}
	for _, branchOrder := range [][]yamlSequenceLineage{accumulatedOrder, wholeOrder} {
		for _, lineage := range branchOrder {
			if _, present, lookupErr := allLookup.indexContext(ctx, lineage); lookupErr != nil {
				return yamlRenderValue{}, nil, lookupErr
			} else if !present {
				allLineages = append(allLineages, lineage)
				key, keyErr := sequenceLineageCanonicalContext(ctx, lineage)
				if keyErr != nil {
					return yamlRenderValue{}, nil, keyErr
				}
				allLookup.lineages = allLineages
				digest, digestErr := stringFingerprintContext(ctx, key)
				if digestErr != nil {
					return yamlRenderValue{}, nil, digestErr
				}
				allLookup.buckets[digest] = append(allLookup.buckets[digest], len(allLineages)-1)
			}
		}
	}
	accumulatedLookup, err := newSequenceLineageLookupContext(ctx, accumulatedOrder)
	if err != nil {
		return yamlRenderValue{}, nil, err
	}
	wholeLookup, err := newSequenceLineageLookupContext(ctx, wholeOrder)
	if err != nil {
		return yamlRenderValue{}, nil, err
	}
	type mergedLineageValue struct {
		lineage yamlSequenceLineage
		value   yamlRenderValue
		present bool
	}
	mergedValues := make([]mergedLineageValue, 0, len(allLineages))
	for _, lineage := range allLineages {
		accumulatedIndex, accumulatedPresent, lookupErr := accumulatedLookup.indexContext(ctx, lineage)
		if lookupErr != nil {
			return yamlRenderValue{}, nil, lookupErr
		}
		wholeIndex, wholePresent, lookupErr := wholeLookup.indexContext(ctx, lineage)
		if lookupErr != nil {
			return yamlRenderValue{}, nil, lookupErr
		}
		baseIndex, basePresent := lineage.baseIndex, lineage.baseIndex >= 0
		var baseValue, accumulatedValue, wholeValue yamlRenderValue
		if basePresent {
			baseValue = base.sequence[baseIndex]
		}
		if accumulatedPresent {
			accumulatedValue = accumulated.sequence[accumulatedIndex]
		}
		if wholePresent {
			wholeValue = whole.sequence[wholeIndex]
		}
		value, present, mergeErr := mergeYAMLDesiredOptionalContext(
			ctx,
			baseValue, basePresent,
			accumulatedValue, accumulatedPresent,
			wholeValue, wholePresent,
			"",
		)
		if mergeErr != nil {
			return yamlRenderValue{}, nil, mergeErr
		}
		mergedValues = append(mergedValues, mergedLineageValue{lineage: lineage, value: value, present: present})
	}
	presentLineages := make([]yamlSequenceLineage, 0, len(mergedValues))
	for _, candidate := range mergedValues {
		if candidate.present {
			presentLineages = append(presentLineages, candidate.lineage)
		}
	}
	order, orderErr := mergeSequenceLineageOrderContext(ctx, baseOrder, accumulatedOrder, wholeOrder, presentLineages)
	if orderErr != nil {
		return yamlRenderValue{}, nil, orderErr
	}
	merged := yamlSequence()
	mergedOrder := make([]yamlSequenceLineage, 0, len(order))
	mergedLookup, err := newSequenceLineageLookupContext(ctx, allLineages)
	if err != nil {
		return yamlRenderValue{}, nil, err
	}
	for _, lineage := range order {
		index, present, lookupErr := mergedLookup.indexContext(ctx, lineage)
		if lookupErr != nil {
			return yamlRenderValue{}, nil, lookupErr
		}
		if !present || !mergedValues[index].present {
			return yamlRenderValue{}, nil, errYAMLDesiredConflict
		}
		merged.sequence = append(merged.sequence, mergedValues[index].value)
		mergedOrder = append(mergedOrder, lineage)
	}
	orderLookup, err := newSequenceLineageLookupContext(ctx, order)
	if err != nil {
		return yamlRenderValue{}, nil, err
	}
	for _, candidate := range mergedValues {
		if !candidate.present {
			continue
		}
		if _, ordered, lookupErr := orderLookup.indexContext(ctx, candidate.lineage); lookupErr != nil {
			return yamlRenderValue{}, nil, lookupErr
		} else if !ordered {
			return yamlRenderValue{}, nil, errYAMLDesiredConflict
		}
	}
	if _, err := sequenceIdentitiesContext(ctx, merged, route); err != nil {
		return yamlRenderValue{}, nil, err
	}
	return merged, mergedOrder, ctx.Err()
}

// yamlRenderFromNode creates a detached typed value. Alias, anchor, explicit
// tag, comment, complex-key, and unsupported scalar provenance is rejected
// rather than guessed.

func yamlRenderFromNodeContext(ctx context.Context, n *yaml.Node) (yamlRenderValue, error) {
	if err := ctx.Err(); err != nil {
		return yamlRenderValue{}, err
	}
	if n == nil {
		return yamlRenderValue{}, fmt.Errorf("%w: nil YAML node", ErrUnsupportedPresentation)
	}
	if n.Kind == yaml.AliasNode || n.Alias != nil {
		return yamlRenderValue{}, fmt.Errorf("%w: YAML alias", ErrAmbiguousPresentation)
	}
	if n.Anchor != "" || n.Style&yaml.TaggedStyle != 0 || n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" {
		return yamlRenderValue{}, fmt.Errorf("%w: YAML provenance", ErrUnsupportedPresentation)
	}
	switch n.Kind {
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str":
			return yamlString(n.Value), nil
		case "!!bool":
			value, err := strconv.ParseBool(n.Value)
			if err != nil {
				return yamlRenderValue{}, fmt.Errorf("%w: invalid bool", ErrUnsupportedPresentation)
			}
			return yamlBool(value), nil
		case "!!int":
			value, err := strconv.ParseInt(n.Value, 0, 64)
			if err == nil {
				return yamlInt(value), nil
			}
			unsigned, unsignedErr := strconv.ParseUint(n.Value, 0, 64)
			if unsignedErr != nil {
				return yamlRenderValue{}, fmt.Errorf("%w: invalid int", ErrUnsupportedPresentation)
			}
			return yamlUint(unsigned), nil
		case "!!timestamp":
			if len(n.Value) == len("2006-01-02") {
				value, err := yamlDate(n.Value)
				if err == nil {
					value.lexical = n.Value
				}
				return value, err
			}
			value, err := yamlDateTime(n.Value)
			if err == nil {
				value.lexical = n.Value
			}
			return value, err
		default:
			return yamlRenderValue{}, fmt.Errorf("%w: scalar tag %q", ErrUnsupportedPresentation, n.Tag)
		}
	case yaml.MappingNode:
		if n.Tag != "!!map" || len(n.Content)%2 != 0 {
			return yamlRenderValue{}, fmt.Errorf("%w: mapping", ErrUnsupportedPresentation)
		}
		entries := make([]yamlRenderEntry, 0, len(n.Content)/2)
		seen := make(map[string]bool, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			if err := ctx.Err(); err != nil {
				return yamlRenderValue{}, err
			}
			key := n.Content[i]
			if key == nil || n.Content[i+1] == nil {
				return yamlRenderValue{}, fmt.Errorf("%w: invalid mapping shape", ErrUnsupportedPresentation)
			}
			if key.Kind == yaml.AliasNode || key.Alias != nil || key.Tag == "!!merge" {
				return yamlRenderValue{}, fmt.Errorf("%w: alias or merge mapping key", ErrAmbiguousPresentation)
			}
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" ||
				key.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.FlowStyle|yaml.TaggedStyle) != 0 ||
				key.HeadComment != "" || key.LineComment != "" || key.FootComment != "" || seen[key.Value] {
				if key.Kind == yaml.ScalarNode && seen[key.Value] {
					return yamlRenderValue{}, fmt.Errorf("%w: duplicate mapping key %q", ErrAmbiguousPresentation, key.Value)
				}
				return yamlRenderValue{}, fmt.Errorf("%w: complex mapping key", ErrUnsupportedPresentation)
			}
			seen[key.Value] = true
			value, err := yamlRenderFromNodeContext(ctx, n.Content[i+1])
			if err != nil {
				return yamlRenderValue{}, err
			}
			entries = append(entries, yamlEntry(key.Value, value))
		}
		return yamlMapping(entries...), nil
	case yaml.SequenceNode:
		if n.Tag != "!!seq" {
			return yamlRenderValue{}, fmt.Errorf("%w: sequence", ErrUnsupportedPresentation)
		}
		values := make([]yamlRenderValue, 0, len(n.Content))
		for _, child := range n.Content {
			if err := ctx.Err(); err != nil {
				return yamlRenderValue{}, err
			}
			value, err := yamlRenderFromNodeContext(ctx, child)
			if err != nil {
				return yamlRenderValue{}, err
			}
			values = append(values, value)
		}
		return yamlSequence(values...), nil
	default:
		return yamlRenderValue{}, fmt.Errorf("%w: YAML node kind %d", ErrUnsupportedPresentation, n.Kind)
	}
}
