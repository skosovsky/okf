package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/documentlayout"
	"github.com/skosovsky/okf/internal/markdownowner"
	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

func (s *planState) setGenerated(op store.SetGenerated, operationName string) error {
	return s.applyV02ConceptOperation(op.Concept, operationName, op, func(p *presentation) ([]byte, error) {
		entries := []desiredMappingEntry{{key: "by", value: yamlString(op.Generated.By), present: true}}
		if op.Generated.At != "" {
			at, err := yamlDateTime(op.Generated.At)
			if err != nil {
				return nil, err
			}
			entries = append(entries, desiredMappingEntry{key: "at", value: at, present: true})
		} else {
			entries = append(entries, desiredMappingEntry{key: "at"})
		}
		return upsertKnownMappingFamily(p, p.root, "generated", entries)
	})
}

func (s *planState) ensureVerification(op store.EnsureVerification, operationName string) error {
	return s.applyV02ConceptOperation(op.Concept, operationName, op, func(p *presentation) ([]byte, error) {
		desired, selector, err := verificationRender(op.Verification)
		if err != nil {
			return nil, err
		}
		verified, found, err := p.structuralMappingEntry(p.root, "verified")
		if err != nil {
			return nil, err
		}
		mutation := p.newYAMLMutationContext(p.ctx)
		if !found {
			if err := mutation.insertMappingValue(p.root, "verified", desired); err != nil {
				return nil, err
			}
			return mutation.apply()
		}
		if verified.value.Kind != yaml.MappingNode && verified.value.Kind != yaml.SequenceNode {
			return nil, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, verified.value)
		}
		if _, present, err := p.selectSequenceItem(verified.value, selector); err != nil {
			return nil, err
		} else if present {
			return appendBytesContext(p.ctx, nil, p.data)
		}
		if err := mutation.ensureSequenceItem(verified.value, selector, desired); err != nil {
			return nil, err
		}
		return mutation.apply()
	})
}

func (s *planState) removeVerification(op store.RemoveVerification, operationName string) error {
	return s.applyV02ConceptOperation(op.Concept, operationName, op, func(p *presentation) ([]byte, error) {
		_, selector, err := verificationRender(op.Verification)
		if err != nil {
			return nil, err
		}
		verified, found, err := p.structuralMappingEntry(p.root, "verified")
		if err != nil {
			return nil, err
		}
		if !found {
			return appendBytesContext(p.ctx, nil, p.data)
		}
		if verified.value.Kind != yaml.MappingNode && verified.value.Kind != yaml.SequenceNode {
			return nil, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, verified.value)
		}
		_, present, err := p.selectSequenceItem(verified.value, selector)
		if err != nil {
			return nil, err
		}
		if !present {
			return appendBytesContext(p.ctx, nil, p.data)
		}
		mutation := p.newYAMLMutationContext(p.ctx)
		if verified.value.Kind == yaml.MappingNode || len(verified.value.Content) == 1 {
			if err := mutation.deleteMappingValue(p.root, "verified"); err != nil {
				return nil, err
			}
		} else if err := mutation.removeSequenceItem(verified.value, selector); err != nil {
			return nil, err
		}
		return mutation.apply()
	})
}

func (s *planState) putSource(op store.PutSource, operationName string) error {
	source := op.Source()
	return s.applyV02ConceptOperation(op.Concept, operationName, op, func(p *presentation) ([]byte, error) {
		desired, err := sourceRender(source)
		if err != nil {
			return nil, err
		}
		sources, found, err := p.structuralMappingEntry(p.root, "sources")
		if err != nil {
			return nil, err
		}
		mutation := p.newYAMLMutationContext(p.ctx)
		if !found {
			if err := mutation.insertMappingValue(p.root, "sources", yamlSequence(desired)); err != nil {
				return nil, err
			}
			return mutation.apply()
		}
		if sources.value.Kind != yaml.SequenceNode {
			return nil, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, sources.value)
		}
		selector := yamlExactSelector(desired)
		if source.ID != "" {
			selector = yamlMappingSelector(yamlSelector("id", yamlString(source.ID)))
		}
		var item *yaml.Node
		var matched bool
		if source.ID == "" {
			item, _, matched, err = selectAnonymousSourceItem(p, sources.value, desired)
		} else {
			item, matched, err = p.selectSequenceItem(sources.value, selector)
		}
		if err != nil {
			return nil, err
		}
		if !matched {
			if err := mutation.ensureSequenceItem(sources.value, selector, desired); err != nil {
				return nil, err
			}
			return mutation.apply()
		}
		if source.ID == "" {
			return appendBytesContext(p.ctx, nil, p.data)
		}
		if err := mutateKnownSource(mutation, item, source); err != nil {
			return nil, err
		}
		return mutation.apply()
	})
}

func (s *planState) removeSource(op store.RemoveSource, operationName string) error {
	selector := op.Selector()
	return s.applyV02ConceptOperation(op.Concept, operationName, op, func(p *presentation) ([]byte, error) {
		sources, found, err := p.structuralMappingEntry(p.root, "sources")
		if err != nil {
			return nil, err
		}
		if !found {
			return appendBytesContext(p.ctx, nil, p.data)
		}
		if sources.value.Kind != yaml.SequenceNode {
			return nil, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, sources.value)
		}
		yamlSelector, err := sourceSelectorRender(selector)
		if err != nil {
			return nil, err
		}
		var matched bool
		if _, idSelector := selector.ID(); idSelector {
			_, matched, err = p.selectSequenceItem(sources.value, yamlSelector)
		} else if exact, exactSelector := selector.Exact(); exactSelector {
			var desired yamlRenderValue
			desired, err = sourceRender(exact)
			if err == nil {
				_, yamlSelector, matched, err = selectAnonymousSourceItem(p, sources.value, desired)
			}
		} else {
			return nil, p.yamlNodeError(yamlCodeUnsupportedSelector, ErrUnsupportedPresentation, sources.value)
		}
		if err != nil {
			return nil, err
		}
		if !matched {
			return appendBytesContext(p.ctx, nil, p.data)
		}
		mutation := p.newYAMLMutationContext(p.ctx)
		if len(sources.value.Content) == 1 {
			if err := mutation.deleteMappingValue(p.root, "sources"); err != nil {
				return nil, err
			}
		} else if err := mutation.removeSequenceItem(sources.value, yamlSelector); err != nil {
			return nil, err
		}
		return mutation.apply()
	})
}

func (s *planState) setUsageWindow(op store.SetUsageWindow, operationName string) error {
	window, setWindow := op.Window()
	selector, perSource := op.Selector()
	return s.applyV02ConceptOperation(op.Concept, operationName, op, func(p *presentation) ([]byte, error) {
		var desired yamlRenderValue
		if setWindow {
			var err error
			desired, err = usageWindowRender(window)
			if err != nil {
				return nil, err
			}
		}
		if !perSource {
			return setUsageWindowValue(p, p.root, desired, setWindow)
		}
		sources, found, err := p.structuralMappingEntry(p.root, "sources")
		if err != nil {
			return nil, err
		}
		if !found || sources.value.Kind != yaml.SequenceNode {
			return nil, invalid("missing_source_selector", nil, errors.New("source selector does not exist"))
		}
		var item *yaml.Node
		var matched bool
		if _, idSelector := selector.ID(); idSelector {
			var yamlSelector yamlSequenceSelector
			yamlSelector, err = sourceSelectorRender(selector)
			if err != nil {
				return nil, err
			}
			item, matched, err = p.selectSequenceItem(sources.value, yamlSelector)
		} else if exact, exactSelector := selector.Exact(); exactSelector {
			before, renderErr := sourceRender(exact)
			if renderErr != nil {
				return nil, renderErr
			}
			post := exact
			if setWindow {
				post.UsageWindow = &window
			} else {
				post.UsageWindow = nil
			}
			after, renderErr := sourceRender(post)
			if renderErr != nil {
				return nil, renderErr
			}
			var alreadyDesired bool
			item, alreadyDesired, matched, err = selectAnonymousSourceTransitionItem(p, sources.value, before, after)
			if err == nil && matched && alreadyDesired {
				return appendBytesContext(p.ctx, nil, p.data)
			}
		} else {
			return nil, p.yamlNodeError(yamlCodeUnsupportedSelector, ErrUnsupportedPresentation, sources.value)
		}
		if err != nil {
			return nil, err
		}
		if !matched {
			return nil, invalid("missing_source_selector", nil, errors.New("source selector does not exist"))
		}
		return setUsageWindowValue(p, item, desired, setWindow)
	})
}

func (s *planState) setLifecycle(op store.SetLifecycle, operationName string) error {
	lifecycle := op.Lifecycle()
	return s.applyV02ConceptOperation(op.Concept, operationName, op, func(p *presentation) ([]byte, error) {
		mutation := p.newYAMLMutationContext(p.ctx)
		if err := mutateOptionalRootString(mutation, p.root, "status", lifecycle.Status); err != nil {
			return nil, err
		}
		var staleAfter *yamlRenderValue
		if lifecycle.StaleAfter != nil {
			value, err := yamlDate(*lifecycle.StaleAfter)
			if err != nil {
				return nil, err
			}
			staleAfter = &value
		}
		if err := mutateOptionalRootValue(mutation, p.root, "stale_after", staleAfter); err != nil {
			return nil, err
		}
		return mutation.apply()
	})
}

func (s *planState) putAttestedComputation(op store.PutAttestedComputation, operationName string) error {
	contract := op.Contract()
	return s.applyV02ConceptOperation(op.Concept, operationName, op, func(p *presentation) ([]byte, error) {
		updated, err := reconcileAttestedComputationYAMLContext(s.ctx, p, contract)
		if err != nil {
			return nil, err
		}
		return rewriteInlineComputation(s.ctx, updated, contract)
	})
}

func (s *planState) setBundleVersion(op store.SetBundleVersion, operationName string) error {
	data, err := s.overlay.ReadFile(s.ctx, "index.md")
	if err != nil {
		return invalid("missing_root_index", nil, errors.New("root index.md does not exist"))
	}
	updated, err := appendBytesContext(s.ctx, nil, data)
	if err != nil {
		return err
	}
	if documentlayout.HasOpeningDelimiter(data) {
		p, parseErr := parsePresentationContext(s.ctx, data)
		if parseErr != nil {
			return s.presentationInvalid(presentationChangeCode(parseErr), nil, parseErr, "index.md", operationName, data)
		}
		updated, err = setOrDeleteMappingValue(p, p.root, "okf_version", yamlString(op.Version), true)
		if err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, "index.md", operationName, data)
		}
	} else {
		newline := "\n"
		if bytes.Contains(data, []byte("\r\n")) {
			newline = "\r\n"
		}
		versionToken, tokenErr := yamlScalarTokenContext(s.ctx, op.Version, 0)
		if tokenErr != nil {
			return tokenErr
		}
		header := []byte("---" + newline + "okf_version: " + versionToken + newline + "---" + newline)
		updated, err = appendBytesContext(s.ctx, header, data)
		if err != nil {
			return err
		}
	}
	if !bytes.Equal(updated, data) {
		if err := s.overlay.PutContext(s.ctx, "index.md", updated); err != nil {
			return err
		}
	}
	s.plans = append(s.plans, store.OperationPlan{Operation: op, Details: []string{"root bundle version set"}})
	return nil
}

func (s *planState) applyV02ConceptOperation(conceptID bundle.ConceptID, operationName string, operation store.Operation, mutate func(*presentation) ([]byte, error)) error {
	conceptPath, ok, err := s.bundle.ConceptPathContext(s.ctx, conceptID)
	if err != nil {
		return err
	}
	if !ok {
		return invalid("missing_concept", []store.Diagnostic{{Code: "missing_concept", Refs: []bundle.RelationRef{{ID: conceptID}}}}, errors.New("concept missing"))
	}
	data, fileFound, err := readBundleFileContext(s.ctx, s.bundle, conceptPath)
	if err != nil {
		return err
	}
	if !fileFound {
		return invalid("missing_concept", []store.Diagnostic{{Code: "missing_concept", Refs: []bundle.RelationRef{{ID: conceptID}}}}, errors.New("concept file missing"))
	}
	p, err := parsePresentationContext(s.ctx, data)
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, conceptPath, operationName, data)
	}
	updated, err := mutate(p)
	if err != nil {
		var invalidChange *store.InvalidChangeSet
		if errors.As(err, &invalidChange) {
			return err
		}
		return s.presentationInvalid(presentationChangeCode(err), nil, err, conceptPath, operationName, data)
	}
	equal, err := bytesEqualContext(s.ctx, updated, data)
	if err != nil {
		return err
	}
	if !equal {
		if err := s.overlay.PutContext(s.ctx, conceptPath, updated); err != nil {
			return err
		}
	}
	ref := bundle.RelationRef{ID: conceptID}
	s.add(ref)
	s.plans = append(s.plans, store.OperationPlan{Operation: operation, AffectedRefs: []bundle.RelationRef{ref}, Details: []string{operationName + " desired state applied"}})
	return nil
}

type desiredMappingEntry struct {
	key     string
	value   yamlRenderValue
	present bool
}

func verificationRender(verification store.Verification) (yamlRenderValue, yamlSequenceSelector, error) {
	at, err := yamlDateTime(verification.At)
	if err != nil {
		return yamlRenderValue{}, yamlSequenceSelector{}, err
	}
	desired := yamlMapping(yamlEntry("by", yamlString(verification.By)), yamlEntry("at", at))
	selector := yamlMappingSelector(yamlSelector("by", yamlString(verification.By)), yamlSelector("at", at))
	return desired, selector, nil
}

func sourceRender(source store.ProvenanceSource) (yamlRenderValue, error) {
	entries := make([]yamlRenderEntry, 0, 7)
	if source.ID != "" {
		entries = append(entries, yamlEntry("id", yamlString(source.ID)))
	}
	entries = append(entries, yamlEntry("resource", yamlString(source.Resource)))
	if source.Title != "" {
		entries = append(entries, yamlEntry("title", yamlString(source.Title)))
	}
	if source.Author != "" {
		entries = append(entries, yamlEntry("author", yamlString(source.Author)))
	}
	if source.UsageCount != nil {
		entries = append(entries, yamlEntry("usage_count", yamlUint(*source.UsageCount)))
	}
	if source.LastModified != "" {
		value, err := yamlDate(source.LastModified)
		if err != nil {
			return yamlRenderValue{}, err
		}
		entries = append(entries, yamlEntry("last_modified", value))
	}
	if source.UsageWindow != nil {
		value, err := usageWindowRender(*source.UsageWindow)
		if err != nil {
			return yamlRenderValue{}, err
		}
		entries = append(entries, yamlEntry("usage_window", value))
	}
	return yamlMapping(entries...), nil
}

func sourceSelectorRender(selector store.SourceSelector) (yamlSequenceSelector, error) {
	if id, ok := selector.ID(); ok {
		return yamlMappingSelector(yamlSelector("id", yamlString(id))), nil
	}
	exact, ok := selector.Exact()
	if !ok {
		return yamlSequenceSelector{}, fmt.Errorf("%w: invalid source selector", ErrUnsupportedPresentation)
	}
	value, err := sourceRender(exact)
	if err != nil {
		return yamlSequenceSelector{}, err
	}
	return yamlExactSelector(value), nil
}

func selectAnonymousSourceItem(p *presentation, collection *yaml.Node, desired yamlRenderValue) (*yaml.Node, yamlSequenceSelector, bool, error) {
	items, err := p.sequenceSelectorItems(collection)
	if err != nil {
		return nil, yamlSequenceSelector{}, false, err
	}
	var matched *yaml.Node
	var matchedSelector yamlSequenceSelector
	for _, item := range items {
		if _, identified, identityErr := p.structuralMappingEntry(item, "id"); identityErr != nil {
			return nil, yamlSequenceSelector{}, false, identityErr
		} else if identified {
			// Anonymous exact selection does not own identified siblings. Their
			// extension fields may remain opaque and byte-preserved.
			continue
		}
		rendered, err := yamlRenderFromNodeContext(p.ctx, item)
		if err != nil {
			return nil, yamlSequenceSelector{}, false, p.renderNodeError(err, item)
		}
		projection, identified, extensions, err := anonymousSourceProjection(rendered)
		if err != nil {
			return nil, yamlSequenceSelector{}, false, p.yamlNodeError(yamlCodeUnsupportedSelector, ErrUnsupportedPresentation, item)
		}
		equal, err := equalYAMLRenderCanonicalAtKeyContext(p.ctx, projection, desired, "")
		if err != nil {
			return nil, yamlSequenceSelector{}, false, err
		}
		if identified || !equal {
			continue
		}
		if extensions {
			return nil, yamlSequenceSelector{}, false, p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, item)
		}
		if matched != nil {
			return nil, yamlSequenceSelector{}, false, p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, matched, item)
		}
		matched = item
		matchedSelector = yamlExactSelector(rendered)
	}
	return matched, matchedSelector, matched != nil, nil
}

func selectAnonymousSourceTransitionItem(
	p *presentation,
	collection *yaml.Node,
	before yamlRenderValue,
	after yamlRenderValue,
) (*yaml.Node, bool, bool, error) {
	items, err := p.sequenceSelectorItems(collection)
	if err != nil {
		return nil, false, false, err
	}
	var matched *yaml.Node
	matchedAfter := false
	for _, item := range items {
		if _, identified, identityErr := p.structuralMappingEntry(item, "id"); identityErr != nil {
			return nil, false, false, identityErr
		} else if identified {
			continue
		}
		rendered, err := yamlRenderFromNodeIgnoringCommentsContext(p.ctx, item)
		if err != nil {
			return nil, false, false, p.renderNodeError(err, item)
		}
		projection, identified, _, err := anonymousSourceProjection(rendered)
		if err != nil {
			return nil, false, false, p.yamlNodeError(yamlCodeUnsupportedSelector, ErrUnsupportedPresentation, item)
		}
		if identified {
			continue
		}
		matchesBefore, err := equalYAMLRenderCanonicalAtKeyContext(p.ctx, projection, before, "")
		if err != nil {
			return nil, false, false, err
		}
		matchesAfter, err := equalYAMLRenderCanonicalAtKeyContext(p.ctx, projection, after, "")
		if err != nil {
			return nil, false, false, err
		}
		if !matchesBefore && !matchesAfter {
			continue
		}
		if matched != nil {
			return nil, false, false, p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, matched, item)
		}
		matched = item
		matchedAfter = matchesAfter
	}
	return matched, matchedAfter, matched != nil, nil
}

func anonymousSourceProjection(source yamlRenderValue) (yamlRenderValue, bool, bool, error) {
	if source.kind != yamlRenderMapping {
		return yamlRenderValue{}, false, false, fmt.Errorf("anonymous source must be a mapping")
	}
	known := make(map[string]yamlRenderValue, len(source.mapping))
	extensions := false
	for _, entry := range source.mapping {
		switch entry.Key {
		case "id":
			return yamlRenderValue{}, true, false, nil
		case "resource", "title", "author", "usage_count", "last_modified":
			known[entry.Key] = entry.Value
		case "usage_window":
			window, unknown, err := normalizeUsageWindow(entry.Value)
			if err != nil {
				return yamlRenderValue{}, false, false, err
			}
			known[entry.Key] = window
			extensions = extensions || unknown
		default:
			extensions = true
		}
	}
	projected := yamlMapping()
	for _, key := range []string{"resource", "title", "author", "usage_count", "last_modified", "usage_window"} {
		if value, present := known[key]; present {
			projected.mapping = append(projected.mapping, yamlEntry(key, value))
		}
	}
	return projected, false, extensions, nil
}

func normalizeUsageWindow(window yamlRenderValue) (yamlRenderValue, bool, error) {
	if window.kind != yamlRenderMapping {
		return window, false, nil
	}
	known := make(map[string]yamlRenderValue, len(window.mapping))
	extensions := false
	for _, entry := range window.mapping {
		switch entry.Key {
		case "from", "to":
			known[entry.Key] = entry.Value
		default:
			extensions = true
		}
	}
	normalized := yamlMapping()
	for _, key := range []string{"from", "to"} {
		if value, present := known[key]; present {
			normalized.mapping = append(normalized.mapping, yamlEntry(key, value))
		}
	}
	return normalized, extensions, nil
}

func usageWindowRender(window store.UsageWindow) (yamlRenderValue, error) {
	from, err := yamlDate(window.From)
	if err != nil {
		return yamlRenderValue{}, err
	}
	to, err := yamlDate(window.To)
	if err != nil {
		return yamlRenderValue{}, err
	}
	return yamlMapping(yamlEntry("from", from), yamlEntry("to", to)), nil
}

func mutateKnownSource(mutation *yamlMutation, item *yaml.Node, source store.ProvenanceSource) error {
	if err := rejectInvalidUsageCount(mutation.p, item); err != nil {
		return err
	}
	entries := []desiredMappingEntry{
		{key: "id", value: yamlString(source.ID), present: source.ID != ""},
		{key: "resource", value: yamlString(source.Resource), present: true},
		{key: "title", value: yamlString(source.Title), present: source.Title != ""},
		{key: "author", value: yamlString(source.Author), present: source.Author != ""},
		{key: "usage_count", present: source.UsageCount != nil},
		{key: "last_modified", present: source.LastModified != ""},
		{key: "usage_window", present: source.UsageWindow != nil},
	}
	if source.UsageCount != nil {
		entries[4].value = yamlUint(*source.UsageCount)
	}
	if source.LastModified != "" {
		value, err := yamlDate(source.LastModified)
		if err != nil {
			return err
		}
		entries[5].value = value
	}
	if source.UsageWindow != nil {
		value, err := usageWindowRender(*source.UsageWindow)
		if err != nil {
			return err
		}
		entries[6].value = value
	}
	if item.Style&yaml.FlowStyle != 0 {
		current, err := yamlRenderFromNodeIgnoringCommentsContext(mutation.ctx, item)
		if err != nil {
			return mutation.p.renderNodeError(err, item)
		}
		outer, err := cloneYAMLRenderValueContext(mutation.ctx, current)
		if err != nil {
			return err
		}
		for _, entry := range entries[:6] {
			setRenderMappingEntry(&outer, entry)
		}
		equal, err := equalYAMLRenderCanonicalAtKeyContext(mutation.ctx, current, outer, "")
		if err != nil {
			return err
		}
		if !equal {
			if err := mutation.replaceCollectionValue(item, outer); err != nil {
				return err
			}
		}
		if source.UsageWindow != nil {
			return mutateUsageWindowValue(mutation, item, entries[6].value, true)
		}
		return mutateUsageWindowValue(mutation, item, yamlRenderValue{}, false)
	}
	if source.UsageWindow != nil {
		if err := mutateUsageWindowValue(mutation, item, entries[6].value, true); err != nil {
			return err
		}
	} else if err := mutateUsageWindowValue(mutation, item, yamlRenderValue{}, false); err != nil {
		return err
	}
	return mutateKnownMapping(mutation, item, entries[:6])
}

func rejectInvalidUsageCount(p *presentation, source *yaml.Node) error {
	match, found, err := p.structuralMappingEntry(source, "usage_count")
	if err != nil || !found {
		return err
	}
	if match.value.Kind != yaml.ScalarNode || match.value.Tag != "!!int" {
		return p.yamlNodeError("usage_count_invalid", ErrUnsupportedPresentation, match.value)
	}
	if _, err := bundle.ParseNonNegativeYAMLUint64(strings.TrimSpace(match.value.Value)); err != nil {
		return p.yamlNodeError("usage_count_invalid", ErrUnsupportedPresentation, match.value)
	}
	return nil
}

func mutateKnownMapping(mutation *yamlMutation, mapping *yaml.Node, entries []desiredMappingEntry) error {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return mutation.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, mapping)
	}
	if mapping.Style&yaml.FlowStyle != 0 {
		current, err := yamlRenderFromNodeContext(mutation.ctx, mapping)
		if err != nil {
			return mutation.p.renderNodeError(err, mapping)
		}
		for _, entry := range entries {
			setRenderMappingEntry(&current, entry)
		}
		return mutation.replaceCollectionValue(mapping, current)
	}
	for _, entry := range entries {
		if err := mutateDesiredMappingEntry(mutation, mapping, entry); err != nil {
			return err
		}
	}
	return nil
}

func setRenderMappingEntry(mapping *yamlRenderValue, desired desiredMappingEntry) {
	for index := range mapping.mapping {
		if mapping.mapping[index].Key != desired.key {
			continue
		}
		if desired.present {
			mapping.mapping[index].Value = desired.value
		} else {
			mapping.mapping = append(mapping.mapping[:index], mapping.mapping[index+1:]...)
		}
		return
	}
	if desired.present {
		mapping.mapping = append(mapping.mapping, yamlEntry(desired.key, desired.value))
	}
}

func mutateDesiredMappingEntry(mutation *yamlMutation, mapping *yaml.Node, desired desiredMappingEntry) error {
	_, found, err := mutation.p.structuralMappingEntry(mapping, desired.key)
	if err != nil {
		return err
	}
	if desired.present {
		if found {
			return mutation.replaceMappingValue(mapping, desired.key, desired.value)
		}
		return mutation.insertMappingValue(mapping, desired.key, desired.value)
	}
	if found {
		return mutation.deleteMappingValue(mapping, desired.key)
	}
	return nil
}

func upsertKnownMappingFamily(p *presentation, root *yaml.Node, key string, entries []desiredMappingEntry) ([]byte, error) {
	mutation := p.newYAMLMutationContext(p.ctx)
	if err := mutateKnownMappingFamily(mutation, root, key, entries); err != nil {
		return nil, err
	}
	return mutation.apply()
}

func mutateKnownMappingFamily(mutation *yamlMutation, root *yaml.Node, key string, entries []desiredMappingEntry) error {
	family, found, err := mutation.p.structuralMappingEntry(root, key)
	if err != nil {
		return err
	}
	if found {
		return mutateKnownMapping(mutation, family.value, entries)
	}
	rendered := yamlMapping()
	for _, entry := range entries {
		setRenderMappingEntry(&rendered, entry)
	}
	return mutation.insertMappingValue(root, key, rendered)
}

func setOrDeleteMappingValue(p *presentation, mapping *yaml.Node, key string, desired yamlRenderValue, present bool) ([]byte, error) {
	mutation := p.newYAMLMutationContext(p.ctx)
	if err := mutateDesiredMappingEntry(mutation, mapping, desiredMappingEntry{key: key, value: desired, present: present}); err != nil {
		return nil, err
	}
	return mutation.apply()
}

func setUsageWindowValue(p *presentation, mapping *yaml.Node, desired yamlRenderValue, present bool) ([]byte, error) {
	mutation := p.newYAMLMutationContext(p.ctx)
	if err := mutateUsageWindowValue(mutation, mapping, desired, present); err != nil {
		return nil, err
	}
	return mutation.apply()
}

func mutateUsageWindowValue(mutation *yamlMutation, mapping *yaml.Node, desired yamlRenderValue, present bool) error {
	match, found, err := mutation.p.structuralMappingEntry(mapping, "usage_window")
	if err != nil {
		return err
	}
	if !present {
		if !found {
			return nil
		}
		return mutation.deleteMappingValue(mapping, "usage_window")
	}
	if desired.kind != yamlRenderMapping {
		return mutation.p.yamlNodeError("invalid_render_value", ErrUnsupportedPresentation, mapping)
	}
	if !found {
		return mutation.insertMappingValue(mapping, "usage_window", desired)
	}
	if match.value.Kind != yaml.MappingNode {
		return mutation.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, match.value)
	}
	from, hasFrom := yamlRenderMappingValue(desired, "from")
	to, hasTo := yamlRenderMappingValue(desired, "to")
	if !hasFrom || !hasTo {
		return mutation.p.yamlNodeError("invalid_render_value", ErrUnsupportedPresentation, match.value)
	}
	return mutateKnownMapping(mutation, match.value, []desiredMappingEntry{
		{key: "from", value: from, present: true},
		{key: "to", value: to, present: true},
	})
}

func mutateOptionalRootString(mutation *yamlMutation, root *yaml.Node, key string, value *string) error {
	if value == nil {
		return mutateDesiredMappingEntry(mutation, root, desiredMappingEntry{key: key})
	}
	return mutateDesiredMappingEntry(mutation, root, desiredMappingEntry{key: key, value: yamlString(*value), present: true})
}

func mutateOptionalRootValue(mutation *yamlMutation, root *yaml.Node, key string, value *yamlRenderValue) error {
	if value == nil {
		return mutateDesiredMappingEntry(mutation, root, desiredMappingEntry{key: key})
	}
	return mutateDesiredMappingEntry(mutation, root, desiredMappingEntry{key: key, value: *value, present: true})
}

func stringSequenceRender(values []string) yamlRenderValue {
	rendered := make([]yamlRenderValue, len(values))
	for index, value := range values {
		rendered[index] = yamlString(value)
	}
	return yamlSequence(rendered...)
}

func reconcileAttestedComputationYAMLContext(
	ctx context.Context,
	p *presentation,
	contract store.AttestedComputationContract,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mutation := p.newYAMLMutationContext(p.ctx)
	if err := stageAttestedComputationContract(mutation, contract); err != nil {
		return nil, err
	}
	updated, err := mutation.apply()
	if err != nil {
		return nil, err
	}
	return reorderComputationParametersContext(ctx, updated, contract.Parameters)
}

func reconcileAttestedComputationYAMLBytesContext(
	ctx context.Context,
	source []byte,
	contract store.AttestedComputationContract,
) ([]byte, error) {
	p, err := parsePresentationContext(ctx, source)
	if err != nil {
		return nil, err
	}
	return reconcileAttestedComputationYAMLContext(ctx, p, contract)
}

func stageAttestedComputationContract(
	mutation *yamlMutation,
	contract store.AttestedComputationContract,
) error {
	p := mutation.p
	for _, entry := range []desiredMappingEntry{
		{key: "type", value: yamlString("Attested Computation"), present: true},
		{key: "runtime", value: yamlString(contract.Runtime), present: true},
	} {
		if err := mutateDesiredMappingEntry(mutation, p.root, entry); err != nil {
			return err
		}
	}
	if err := mergeComputationParameters(mutation, p, contract.Parameters); err != nil {
		return err
	}
	if err := mutateKnownMappingFamily(mutation, p.root, "executor", []desiredMappingEntry{
		{key: "resource", value: yamlString(contract.Executor.Resource), present: true},
		{key: "receipt", value: stringSequenceRender(contract.Executor.Receipt), present: true},
	}); err != nil {
		return err
	}
	if err := mutateKnownMappingFamily(mutation, p.root, "attester", []desiredMappingEntry{
		{key: "resource", value: yamlString(contract.Attester.Resource), present: true},
	}); err != nil {
		return err
	}
	return mutateDesiredMappingEntry(mutation, p.root, desiredMappingEntry{
		key:     "computation",
		value:   yamlString(contract.ComputationPath),
		present: contract.Mode == store.AttestedComputationModeFile,
	})
}

func mergeComputationParameters(
	mutation *yamlMutation,
	p *presentation,
	desired []store.ComputationParameter,
) error {
	match, found, err := p.structuralMappingEntry(p.root, "parameters")
	if err != nil {
		return err
	}
	if !found {
		return mutateDesiredMappingEntry(mutation, p.root, desiredMappingEntry{
			key:     "parameters",
			value:   computationParametersRender(desired),
			present: true,
		})
	}
	if match.value.Kind != yaml.SequenceNode {
		return p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, match.value)
	}
	if match.value.Style&yaml.FlowStyle != 0 {
		rendered, err := mergedFlowComputationParameters(p, match.value, desired)
		if err != nil {
			return err
		}
		return mutateDesiredMappingEntry(mutation, p.root, desiredMappingEntry{
			key: "parameters", value: rendered, present: true,
		})
	}

	existing := make(map[string]*yaml.Node, len(match.value.Content))
	existingOrder := make([]string, 0, len(match.value.Content))
	for _, item := range match.value.Content {
		name, err := computationParameterName(p, item)
		if err != nil {
			return err
		}
		if previous := existing[name]; previous != nil {
			return p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, previous, item)
		}
		existing[name] = item
		existingOrder = append(existingOrder, name)
	}
	desiredByName := make(map[string]store.ComputationParameter, len(desired))
	for _, parameter := range desired {
		desiredByName[parameter.Name] = parameter
	}
	for _, name := range existingOrder {
		if _, retained := desiredByName[name]; retained {
			continue
		}
		if err := mutation.removeSequenceItem(
			match.value,
			yamlMappingSelector(yamlSelector("name", yamlString(name))),
		); err != nil {
			return err
		}
	}
	for _, parameter := range desired {
		item := existing[parameter.Name]
		rendered := computationParameterRender(parameter)
		if item == nil {
			if err := mutation.ensureSequenceItem(
				match.value,
				yamlMappingSelector(yamlSelector("name", yamlString(parameter.Name))),
				rendered,
			); err != nil {
				return err
			}
			continue
		}
		if err := mutateKnownMapping(mutation, item, []desiredMappingEntry{
			{key: "name", value: yamlString(parameter.Name), present: true},
			{key: "type", value: yamlString(parameter.Type), present: true},
			{key: "required", value: yamlBool(parameter.Required), present: true},
		}); err != nil {
			return err
		}
	}
	return nil
}

func mergedFlowComputationParameters(
	p *presentation,
	sequence *yaml.Node,
	desired []store.ComputationParameter,
) (yamlRenderValue, error) {
	existing := make(map[string]yamlRenderValue)
	for _, item := range sequence.Content {
		rendered, err := yamlRenderFromNodeContext(p.ctx, item)
		if err != nil {
			return yamlRenderValue{}, p.renderNodeError(err, item)
		}
		if rendered.kind != yamlRenderMapping {
			return yamlRenderValue{}, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, item)
		}
		name, err := computationParameterName(p, item)
		if err != nil {
			return yamlRenderValue{}, err
		}
		if _, duplicate := existing[name]; duplicate {
			return yamlRenderValue{}, p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, item)
		}
		existing[name] = rendered
	}
	out := make([]yamlRenderValue, 0, len(desired))
	for _, parameter := range desired {
		rendered, ok := existing[parameter.Name]
		if !ok {
			rendered = yamlMapping()
		}
		for _, entry := range []desiredMappingEntry{
			{key: "name", value: yamlString(parameter.Name), present: true},
			{key: "type", value: yamlString(parameter.Type), present: true},
			{key: "required", value: yamlBool(parameter.Required), present: true},
		} {
			setRenderMappingEntry(&rendered, entry)
		}
		out = append(out, rendered)
	}
	return yamlSequence(out...), nil
}

func computationParameterName(p *presentation, item *yaml.Node) (string, error) {
	if item == nil || item.Kind != yaml.MappingNode {
		return "", p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, item)
	}
	name, found, err := p.structuralMappingEntry(item, "name")
	if err != nil {
		return "", err
	}
	if !found ||
		name.value.Kind != yaml.ScalarNode ||
		name.value.Tag != "!!str" ||
		name.value.Value == "" {
		return "", p.yamlNodeError(yamlCodeUnsupportedSelector, ErrUnsupportedPresentation, item)
	}
	return name.value.Value, nil
}

func computationParameterRender(parameter store.ComputationParameter) yamlRenderValue {
	return yamlMapping(
		yamlEntry("name", yamlString(parameter.Name)),
		yamlEntry("type", yamlString(parameter.Type)),
		yamlEntry("required", yamlBool(parameter.Required)),
	)
}

func computationParametersRender(parameters []store.ComputationParameter) yamlRenderValue {
	rendered := make([]yamlRenderValue, len(parameters))
	for index, parameter := range parameters {
		rendered[index] = computationParameterRender(parameter)
	}
	return yamlSequence(rendered...)
}

func reorderComputationParametersContext(
	ctx context.Context,
	source []byte,
	desired []store.ComputationParameter,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := parsePresentationContext(ctx, source)
	if err != nil {
		return nil, err
	}
	match, found, err := p.structuralMappingEntry(p.root, "parameters")
	if err != nil {
		return nil, err
	}
	if !found || match.value.Kind != yaml.SequenceNode {
		return nil, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, p.root)
	}
	if match.value.Style&yaml.FlowStyle != 0 || len(desired) < 2 {
		return appendBytesContext(ctx, nil, source)
	}

	itemsByName := make(map[string]*yaml.Node, len(match.value.Content))
	currentOrder := make([]string, 0, len(match.value.Content))
	for _, item := range match.value.Content {
		name, err := computationParameterName(p, item)
		if err != nil {
			return nil, err
		}
		if previous := itemsByName[name]; previous != nil {
			return nil, p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, previous, item)
		}
		itemsByName[name] = item
		currentOrder = append(currentOrder, name)
	}
	if len(currentOrder) != len(desired) {
		return nil, p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, match.value)
	}
	sameOrder := true
	for index, parameter := range desired {
		if currentOrder[index] != parameter.Name {
			sameOrder = false
		}
		if itemsByName[parameter.Name] == nil {
			return nil, p.yamlNodeError(yamlCodeMissingSequenceItem, ErrUnsupportedPresentation, match.value)
		}
	}
	if sameOrder {
		return appendBytesContext(ctx, nil, source)
	}

	type ownedItem struct {
		span SourceSpan
		raw  []byte
	}
	owned := make(map[string]ownedItem, len(currentOrder))
	for _, name := range currentOrder {
		item := itemsByName[name]
		span, err := p.sequenceItemSpan(match.value, item)
		if err != nil {
			return nil, err
		}
		owned[name] = ownedItem{
			span: span,
			raw: append(
				[]byte(nil),
				p.data[p.yamlStart+span.Start:p.yamlStart+span.End]...,
			),
		}
	}
	first := owned[currentOrder[0]].span.Start
	last := owned[currentOrder[len(currentOrder)-1]].span.End
	var reordered []byte
	for _, parameter := range desired {
		reordered = append(reordered, owned[parameter.Name].raw...)
	}

	expected, err := semanticYAMLContext(ctx, p.root)
	if err != nil {
		return nil, err
	}
	expectedSequence := expected
	for _, index := range p.paths[match.value] {
		if expectedSequence == nil || index < 0 || index >= len(expectedSequence.Content) {
			return nil, p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, match.value)
		}
		expectedSequence = expectedSequence.Content[index]
	}
	if expectedSequence == nil ||
		expectedSequence.Kind != yaml.SequenceNode ||
		len(expectedSequence.Content) != len(currentOrder) {
		return nil, p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, match.value)
	}
	original := append([]*yamlSemanticNode(nil), expectedSequence.Content...)
	indexByName := make(map[string]int, len(currentOrder))
	for index, name := range currentOrder {
		indexByName[name] = index
	}
	for index, parameter := range desired {
		expectedSequence.Content[index] = original[indexByName[parameter.Name]]
	}
	return p.patchYAMLContext(ctx, []bytePatch{{
		Start: p.yamlStart + first,
		End:   p.yamlStart + last,
		Text:  reordered,
		edit:  &semanticEdit{expected: expected},
	}})
}

func rewriteInlineComputation(ctx context.Context, source []byte, contract store.AttestedComputationContract) ([]byte, error) {
	syntax, err := inspectComputationSyntax(ctx, source)
	if err != nil {
		return nil, err
	}
	if syntax.inspection.State == markdownowner.ComputationMalformed {
		return nil, syntax.malformedError(len(source))
	}
	switch contract.Mode {
	case store.AttestedComputationModeFile:
		if syntax.inspection.State != markdownowner.ComputationInline {
			return verifyComputationRewrite(ctx, source, contract)
		}
		fence := syntax.inspection.DirectFences[0]
		span := syntax.fenceSpan(fence)
		patch := bytePatch{Start: span.Start, End: span.End}
		updated, err := applyBytePatchesContext(ctx, source, []bytePatch{patch})
		if err != nil {
			return nil, err
		}
		return verifyComputationRewrite(ctx, updated, contract)
	case store.AttestedComputationModeInline:
	default:
		return nil, markdownPresentationErrorAt(
			"computation_mode_mismatch",
			ErrUnsupportedPresentation,
			syntax.span(len(source)),
		)
	}
	rendered := renderComputationFence(source, contract.InlineLanguage, contract.InlineComputation)
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	var updated []byte
	if syntax.inspection.State == markdownowner.ComputationNoSection {
		prefix := []byte{}
		if len(source) > 0 && source[len(source)-1] != '\n' {
			prefix = []byte(newline)
		}
		text := append(prefix, []byte("# Computation"+newline+newline)...)
		text = append(text, rendered...)
		patch := bytePatch{Start: len(source), End: len(source), Text: text}
		updated, err = applyBytePatchesContext(ctx, source, []bytePatch{patch})
	} else if syntax.inspection.State == markdownowner.ComputationEmptySection {
		at := syntax.bodyStart + syntax.inspection.Sections[0].Heading.End
		text := append([]byte(newline), rendered...)
		updated, err = applyBytePatchesContext(ctx, source, []bytePatch{{Start: at, End: at, Text: text}})
	} else {
		span := syntax.fenceSpan(syntax.inspection.DirectFences[0])
		updated, err = applyBytePatchesContext(ctx, source, []bytePatch{{Start: span.Start, End: span.End, Text: rendered}})
	}
	if err != nil {
		return nil, err
	}
	return verifyComputationRewrite(ctx, updated, contract)
}

func verifyComputationRewrite(ctx context.Context, source []byte, contract store.AttestedComputationContract) ([]byte, error) {
	syntax, err := inspectComputationSyntax(ctx, source)
	if err != nil {
		return nil, err
	}
	switch contract.Mode {
	case store.AttestedComputationModeFile:
		if syntax.inspection.State != markdownowner.ComputationNoSection &&
			syntax.inspection.State != markdownowner.ComputationEmptySection {
			return nil, markdownPresentationErrorAt("computation_mode_mismatch", ErrUnsupportedPresentation, syntax.span(len(source)))
		}
		return appendBytesContext(ctx, nil, source)
	case store.AttestedComputationModeInline:
	default:
		return nil, markdownPresentationErrorAt(
			"computation_mode_mismatch",
			ErrUnsupportedPresentation,
			syntax.span(len(source)),
		)
	}
	if syntax.inspection.State != markdownowner.ComputationInline {
		return nil, markdownPresentationErrorAt("computation_mode_mismatch", ErrUnsupportedPresentation, syntax.span(len(source)))
	}
	fence := syntax.inspection.DirectFences[0]
	span := syntax.fenceSpan(fence)
	expectedContent := computationFenceContent(source, contract.InlineComputation)
	if !span.valid(len(source)) || fence.Info != contract.InlineLanguage || fence.Content != expectedContent ||
		!bytes.Equal(source[span.Start:span.End], renderComputationFence(source, contract.InlineLanguage, contract.InlineComputation)) {
		return nil, markdownPresentationErrorAt("computation_semantic_mismatch", ErrUnsupportedPresentation, span)
	}
	return appendBytesContext(ctx, nil, source)
}

type computationSyntax struct {
	bodyStart  int
	inspection markdownowner.ComputationInspection
}

func inspectComputationSyntax(ctx context.Context, source []byte) (computationSyntax, error) {
	if err := ctx.Err(); err != nil {
		return computationSyntax{}, err
	}
	bodyStart := 0
	if layout, ok, err := documentlayout.SplitContext(ctx, source); err != nil {
		return computationSyntax{}, err
	} else if ok {
		bodyStart = layout.BodyStart
	}
	inspection, err := markdownowner.InspectComputationContext(ctx, string(source[bodyStart:]))
	if err != nil {
		return computationSyntax{}, err
	}
	syntax := computationSyntax{bodyStart: bodyStart, inspection: inspection}
	if err := ctx.Err(); err != nil {
		return computationSyntax{}, err
	}
	return syntax, nil
}

func (syntax computationSyntax) fenceSpan(fence markdownowner.Fence) SourceSpan {
	return SourceSpan{Start: syntax.bodyStart + fence.Start, End: syntax.bodyStart + fence.End}
}

func (syntax computationSyntax) span(length int) SourceSpan {
	span := SourceSpan{}
	for _, section := range syntax.inspection.Sections {
		span = unionSourceSpans(span, SourceSpan{
			Start: syntax.bodyStart + section.Span.Start,
			End:   syntax.bodyStart + section.Span.End,
		})
	}
	for _, fence := range syntax.inspection.DirectFences {
		span = unionSourceSpans(span, syntax.fenceSpan(fence))
	}
	for _, fence := range syntax.inspection.ConflictingFences {
		span = unionSourceSpans(span, syntax.fenceSpan(fence))
	}
	if span == (SourceSpan{}) && length > 0 {
		return SourceSpan{Start: 0, End: length}
	}
	return span
}

func (syntax computationSyntax) malformedError(length int) error {
	if len(syntax.inspection.Sections) == 1 &&
		len(syntax.inspection.DirectFences) == 1 &&
		!syntax.inspection.DirectFences[0].Closed &&
		len(syntax.inspection.ConflictingFences) == 0 {
		return markdownPresentationErrorAt(
			"unclosed_computation_fence",
			ErrUnsupportedPresentation,
			syntax.fenceSpan(syntax.inspection.DirectFences[0]),
		)
	}
	return markdownPresentationErrorAt(
		"ambiguous_computation_section",
		ErrAmbiguousPresentation,
		syntax.span(length),
	)
}

func renderComputationFence(source []byte, language, computation string) []byte {
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	delimiter := byte('`')
	if strings.ContainsRune(language, '`') {
		delimiter = '~'
	}
	fenceLength := max(3, longestComputationFenceRun(computation, delimiter)+1)
	fence := strings.Repeat(string(delimiter), fenceLength)
	infoSeparator := ""
	if delimiter == '~' {
		infoSeparator = " "
	}
	return []byte(fence + infoSeparator + language + newline + computationFenceContent(source, computation) + fence + newline)
}

func longestComputationFenceRun(computation string, delimiter byte) int {
	longest, current := 0, 0
	for index := 0; index < len(computation); index++ {
		if computation[index] == delimiter {
			current++
			longest = max(longest, current)
			continue
		}
		current = 0
	}
	return longest
}

func computationFenceContent(source []byte, computation string) string {
	if computation == "" {
		return ""
	}
	if strings.HasSuffix(computation, "\n") {
		return computation
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	return computation + newline
}

func rewriteV02PathValuesContext(ctx context.Context, loaded *bundle.Bundle, sourcePath, outputPath, movedFrom, movedTo string, data []byte) ([]byte, error) {
	p, err := parsePresentationContext(ctx, data)
	if err != nil {
		return nil, err
	}
	mutation := p.newYAMLMutationContext(p.ctx)
	probe := v02PathRewriteProbe{
		loaded:     loaded,
		sourcePath: sourcePath,
		outputPath: outputPath,
		movedFrom:  movedFrom,
		movedTo:    movedTo,
	}
	stageBlockPathScalar := func(node *yaml.Node, value string) error {
		if err := mutation.requireActive(); err != nil {
			return err
		}
		if err := p.requireMutationPathProvenance(node); err != nil {
			return err
		}
		if err := mutation.rejectReplacedAncestor(node); err != nil {
			return err
		}
		expected := mutation.expectedNodes[node]
		if expected == nil {
			return p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, node)
		}
		desired := yamlString(value)
		desiredSemantic, semanticErr := desired.semanticContext(ctx)
		if semanticErr != nil {
			return semanticErr
		}
		equal, equalErr := equalSemanticYAMLContext(ctx, expected, desiredSemantic)
		if equalErr != nil {
			return equalErr
		}
		if equal {
			return nil
		}
		if err := mutation.stageValuePatch(node, desired); err != nil {
			return err
		}
		*expected = *desiredSemantic
		return nil
	}
	rewrite := func(mapping *yaml.Node, key string, field bundle.PathValueField) error {
		relevant, err := probe.mappingPathRelevantContext(ctx, mapping, key, field)
		if err != nil || !relevant {
			return err
		}
		match, found, err := p.structuralMappingEntry(mapping, key)
		if err != nil || !found {
			return err
		}
		if match.value.Kind == yaml.AliasNode || match.value.Alias != nil {
			return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, match.value)
		}
		if match.value.Kind != yaml.ScalarNode || match.value.Tag != "!!str" {
			return nil
		}
		value := rewritePathValue(loaded, sourcePath, outputPath, movedFrom, movedTo, match.value.Value, field)
		if value == match.value.Value {
			return nil
		}
		if mapping.Style&yaml.FlowStyle != 0 {
			current, renderErr := yamlRenderFromNodeContext(ctx, mapping)
			if renderErr != nil {
				return p.renderNodeError(renderErr, mapping)
			}
			setRenderMappingEntry(&current, desiredMappingEntry{key: key, value: yamlString(value), present: true})
			return mutation.replaceCollectionValue(mapping, current)
		}
		return stageBlockPathScalar(match.value, value)
	}
	sourcesRelevant, err := probe.sourcesRelevantContext(ctx, p.root)
	if err != nil {
		return nil, err
	}
	if sourcesRelevant {
		sources, found, err := p.structuralMappingEntry(p.root, "sources")
		if err != nil {
			return nil, err
		}
		if found && (sources.value.Kind == yaml.AliasNode || sources.value.Alias != nil) {
			return nil, p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, sources.value)
		}
		if found && sources.value.Kind == yaml.SequenceNode {
			for _, item := range sources.value.Content {
				itemRelevant, relevanceErr := probe.sourceItemRelevantContext(ctx, item)
				if relevanceErr != nil {
					return nil, relevanceErr
				}
				if !itemRelevant {
					continue
				}
				if item.Kind == yaml.AliasNode || item.Alias != nil {
					return nil, p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, item)
				}
				if item.Kind == yaml.MappingNode {
					if err := rewrite(item, "resource", bundle.PathFieldSourceResource); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if err := rewrite(p.root, "computation", bundle.PathFieldComputation); err != nil {
		return nil, err
	}
	for _, nested := range []struct {
		key   string
		field bundle.PathValueField
	}{
		{key: "executor", field: bundle.PathFieldExecutorResource},
		{key: "attester", field: bundle.PathFieldAttesterResource},
	} {
		relevant, relevanceErr := probe.nestedRouteRelevantContext(ctx, p.root, nested.key, nested.field)
		if relevanceErr != nil {
			return nil, relevanceErr
		}
		if !relevant {
			continue
		}
		match, found, err := p.structuralMappingEntry(p.root, nested.key)
		if err != nil {
			return nil, err
		}
		if found && (match.value.Kind == yaml.AliasNode || match.value.Alias != nil) {
			return nil, p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, match.value)
		}
		if found && match.value.Kind == yaml.MappingNode {
			if err := rewrite(match.value, "resource", nested.field); err != nil {
				return nil, err
			}
		}
	}
	return mutation.apply()
}

type v02PathRewriteProbe struct {
	loaded                                     *bundle.Bundle
	sourcePath, outputPath, movedFrom, movedTo string
}

func (probe v02PathRewriteProbe) mappingPathRelevantContext(
	ctx context.Context,
	mapping *yaml.Node,
	key string,
	field bundle.PathValueField,
) (bool, error) {
	return rawMappingKeyRelevantContext(
		ctx,
		mapping,
		key,
		func(value *yaml.Node) (bool, error) {
			return probe.pathValueRelevantContext(ctx, value, field)
		},
		make(map[rawMappingKeyVisit]bool),
	)
}

func (probe v02PathRewriteProbe) nestedRouteRelevantContext(
	ctx context.Context,
	root *yaml.Node,
	route string,
	field bundle.PathValueField,
) (bool, error) {
	return rawMappingKeyRelevantContext(
		ctx,
		root,
		route,
		func(value *yaml.Node) (bool, error) {
			value = rawAliasTarget(value, make(map[*yaml.Node]bool))
			if value == nil || value.Kind != yaml.MappingNode {
				return false, ctx.Err()
			}
			return probe.mappingPathRelevantContext(ctx, value, "resource", field)
		},
		make(map[rawMappingKeyVisit]bool),
	)
}

func (probe v02PathRewriteProbe) sourcesRelevantContext(
	ctx context.Context,
	root *yaml.Node,
) (bool, error) {
	return rawMappingKeyRelevantContext(
		ctx,
		root,
		"sources",
		func(value *yaml.Node) (bool, error) {
			value = rawAliasTarget(value, make(map[*yaml.Node]bool))
			if value == nil || value.Kind != yaml.SequenceNode {
				return false, ctx.Err()
			}
			for _, item := range value.Content {
				relevant, err := probe.sourceItemRelevantContext(ctx, item)
				if err != nil || relevant {
					return relevant, err
				}
			}
			return false, ctx.Err()
		},
		make(map[rawMappingKeyVisit]bool),
	)
}

func (probe v02PathRewriteProbe) sourceItemRelevantContext(
	ctx context.Context,
	item *yaml.Node,
) (bool, error) {
	item = rawAliasTarget(item, make(map[*yaml.Node]bool))
	if item == nil || item.Kind != yaml.MappingNode {
		return false, ctx.Err()
	}
	return probe.mappingPathRelevantContext(ctx, item, "resource", bundle.PathFieldSourceResource)
}

func (probe v02PathRewriteProbe) pathValueRelevantContext(
	ctx context.Context,
	value *yaml.Node,
	field bundle.PathValueField,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	value = rawAliasTarget(value, make(map[*yaml.Node]bool))
	if value == nil || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
		return false, ctx.Err()
	}
	rewritten := rewritePathValue(
		probe.loaded,
		probe.sourcePath,
		probe.outputPath,
		probe.movedFrom,
		probe.movedTo,
		value.Value,
		field,
	)
	return rewritten != value.Value, ctx.Err()
}

type rawMappingKeyVisit struct {
	node *yaml.Node
	key  string
}

type rawYAMLValuePredicate func(*yaml.Node) (bool, error)

func rawMappingKeyRelevantContext(
	ctx context.Context,
	mapping *yaml.Node,
	key string,
	relevant rawYAMLValuePredicate,
	visited map[rawMappingKeyVisit]bool,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	mapping = rawAliasTarget(mapping, make(map[*yaml.Node]bool))
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return false, ctx.Err()
	}
	visit := rawMappingKeyVisit{node: mapping, key: key}
	if visited[visit] {
		return false, ctx.Err()
	}
	visited[visit] = true
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		candidate, value := mapping.Content[index], mapping.Content[index+1]
		if candidate != nil && candidate.Kind == yaml.ScalarNode && candidate.Value == key {
			found, err := relevant(value)
			if err != nil || found {
				return found, err
			}
		}
		if candidate != nil && candidate.Tag == "!!merge" {
			found, err := rawMergedKeyRelevantContext(ctx, value, key, relevant, visited)
			if err != nil || found {
				return found, err
			}
		}
	}
	return false, ctx.Err()
}

func rawMergedKeyRelevantContext(
	ctx context.Context,
	node *yaml.Node,
	key string,
	relevant rawYAMLValuePredicate,
	visited map[rawMappingKeyVisit]bool,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	node = rawAliasTarget(node, make(map[*yaml.Node]bool))
	if node == nil {
		return false, ctx.Err()
	}
	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			found, err := rawMergedKeyRelevantContext(ctx, item, key, relevant, visited)
			if err != nil || found {
				return found, err
			}
		}
		return false, ctx.Err()
	}
	if node.Kind != yaml.MappingNode {
		return false, ctx.Err()
	}
	return rawMappingKeyRelevantContext(ctx, node, key, relevant, visited)
}

func rawAliasTarget(node *yaml.Node, visited map[*yaml.Node]bool) *yaml.Node {
	for node != nil && (node.Kind == yaml.AliasNode || node.Alias != nil) {
		if visited[node] {
			return nil
		}
		visited[node] = true
		node = node.Alias
	}
	return node
}

func rewritePathValue(loaded *bundle.Bundle, sourcePath, outputPath, movedFrom, movedTo, raw string, field bundle.PathValueField) string {
	if pathValueHasSurroundingWhitespace(raw) {
		return raw
	}
	resolved, ok := loaded.ResolvePathValueFor(sourcePath, raw, field)
	if !ok || !resolved.Exists ||
		resolved.Kind != bundle.PathValueRelative && resolved.Kind != bundle.PathValueBundleRelative {
		return raw
	}
	target := resolved.Path
	if target == movedFrom {
		target = movedTo
	}
	if target == resolved.Path {
		if resolved.Kind == bundle.PathValueBundleRelative || sourcePath == outputPath {
			return raw
		}
	}
	if resolved.Kind == bundle.PathValueBundleRelative {
		return "/" + target + resolved.Suffix
	}
	relative, err := filepath.Rel(path.Dir(outputPath), target)
	if err != nil {
		return raw
	}
	return filepath.ToSlash(relative) + resolved.Suffix
}

func pathValueHasSurroundingWhitespace(value string) bool {
	if value == "" {
		return false
	}
	first, firstSize := utf8.DecodeRuneInString(value)
	last, lastSize := utf8.DecodeLastRuneInString(value)
	return first == utf8.RuneError && firstSize == 1 ||
		last == utf8.RuneError && lastSize == 1 ||
		unicode.IsSpace(first) ||
		unicode.IsSpace(last)
}
