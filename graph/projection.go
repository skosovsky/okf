package graph

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/markdownowner"
)

const (
	toolkitProjectionNamespace = "https://github.com/skosovsky/okf/ns/projection/v0.2#"
	toolkitProjectionVersion   = "0.2"
	toolkitBundlePrefix        = "local:bundle:"
	toolkitSyntheticPrefix     = "urn:skosovsky:okf:graph:v0.2:"
)

type projectionValueKind uint8

const (
	projectionLiteral projectionValueKind = iota
	projectionIRI
	projectionBoolean
	projectionInteger
	projectionDate
	projectionDateTime
)

type projectionValue struct {
	Kind  projectionValueKind
	Value string
}

type projectionNode struct {
	ID         string
	Types      []string
	Properties map[string][]projectionValue
}

type toolkitProjection struct {
	Profile           string
	ProjectionVersion string
	DeclaredVersion   string
	EffectiveVersion  string
	VersionSource     string
	Compatibility     string
	Nodes             []projectionNode
}

type normalizedSourceLookupEntry struct {
	key    string
	values []string
}

type normalizedSourceLookup []normalizedSourceLookupEntry

func (lookup *normalizedSourceLookup) appendContext(ctx context.Context, key, value string) error {
	for index := range *lookup {
		equal, err := equalGraphStringsContext(ctx, (*lookup)[index].key, key)
		if err != nil {
			return err
		}
		if equal {
			(*lookup)[index].values = append((*lookup)[index].values, value)
			return checkGraphContext(ctx)
		}
	}
	*lookup = append(*lookup, normalizedSourceLookupEntry{key: key, values: []string{value}})
	return checkGraphContext(ctx)
}

func (lookup normalizedSourceLookup) getContext(ctx context.Context, key string) ([]string, error) {
	for _, entry := range lookup {
		equal, err := equalGraphStringsContext(ctx, entry.key, key)
		if err != nil {
			return nil, err
		}
		if equal {
			return append([]string(nil), entry.values...), checkGraphContext(ctx)
		}
	}
	return nil, checkGraphContext(ctx)
}

func buildToolkitProjection(b *bundle.Bundle, options Options) (toolkitProjection, error) {
	return buildToolkitProjectionContext(context.Background(), b, options)
}

func buildToolkitProjectionContext(ctx context.Context, b *bundle.Bundle, options Options) (toolkitProjection, error) {
	if err := checkGraphContext(ctx); err != nil {
		return toolkitProjection{}, err
	}
	resolution, err := b.VersionResolutionContext(ctx, options.VersionSelector)
	if err != nil {
		return toolkitProjection{}, err
	}
	projection := toolkitProjection{
		Profile:           string(ProjectionProfileToolkitV02),
		ProjectionVersion: toolkitProjectionVersion,
		DeclaredVersion:   resolution.Declared,
		EffectiveVersion:  resolution.Effective,
		VersionSource:     string(resolution.Source),
		Compatibility:     string(resolution.Compatibility),
	}
	metadataID, err := toolkitSyntheticIRIContext(ctx, "projection")
	if err != nil {
		return toolkitProjection{}, err
	}
	metadataNode := newProjectionNode(metadataID, "Projection")
	metadataNode.addLiteral("profile", projection.Profile)
	metadataNode.addLiteral("projectionVersion", projection.ProjectionVersion)
	if projection.DeclaredVersion != "" {
		metadataNode.addLiteral("declaredOKFVersion", projection.DeclaredVersion)
	}
	metadataNode.addLiteral("effectiveOKFVersion", projection.EffectiveVersion)
	metadataNode.addLiteral("versionSource", projection.VersionSource)
	metadataNode.addLiteral("compatibilityMode", projection.Compatibility)
	projection.Nodes = append(projection.Nodes, metadataNode)
	if err := checkGraphContext(ctx); err != nil {
		return toolkitProjection{}, err
	}
	concepts, err := graphConceptsContext(ctx, b)
	if err != nil {
		return toolkitProjection{}, err
	}
	for conceptIndex, concept := range concepts {
		if conceptIndex%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return toolkitProjection{}, err
			}
		}
		conceptIRI, err := toolkitConceptIRIContext(ctx, concept.ID)
		if err != nil {
			return toolkitProjection{}, err
		}
		node := newProjectionNode(conceptIRI, "Concept")
		var conceptAssets []projectionNode
		document := concept.Document
		frontmatter := document.Frontmatter
		if value, ok, err := frontmatter.TypeContext(ctx); err != nil {
			return toolkitProjection{}, err
		} else if ok {
			node.addLiteral("conceptType", value)
		}
		if value, ok, err := frontmatter.TitleContext(ctx); err != nil {
			return toolkitProjection{}, err
		} else if ok {
			node.addLiteral("title", value)
		}
		if value, ok, err := frontmatter.DescriptionContext(ctx); err != nil {
			return toolkitProjection{}, err
		} else if ok {
			node.addLiteral("description", value)
		}
		if value, ok, err := frontmatter.ResourceContext(ctx); err != nil {
			return toolkitProjection{}, err
		} else if ok {
			node.addLiteral("resource", value)
			if resolved, ok, err := b.ResolvePathValueForContext(ctx, concept.Path, value, bundle.PathFieldConceptResource); err != nil {
				return toolkitProjection{}, err
			} else if ok {
				node.addLiteral("resourceKind", string(resolved.Kind))
				if asset, ok, err := projectPathValueContext(ctx, concept.ID, resolved, "concept"); err != nil {
					return toolkitProjection{}, err
				} else if ok {
					node.addIRI("resourceNode", asset.ID)
					conceptAssets = append(conceptAssets, asset)
				}
			}
		}
		if tags, err := frontmatter.TagsStateContext(ctx); err != nil {
			return toolkitProjection{}, err
		} else if tags.Valid {
			for tagIndex, tag := range tags.Items {
				if tagIndex%graphContextCheckInterval == 0 {
					if err := checkGraphContext(ctx); err != nil {
						return toolkitProjection{}, err
					}
				}
				node.addLiteral("tag", tag.Value)
			}
		}
		links, err := b.LinksFromContext(ctx, concept.ID)
		if err != nil {
			return toolkitProjection{}, err
		}
		for linkIndex, link := range links {
			if linkIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return toolkitProjection{}, err
				}
			}
			targetIRI, err := toolkitConceptIRIContext(ctx, link.Target)
			if err != nil {
				return toolkitProjection{}, err
			}
			node.addIRI("references", targetIRI)
		}
		if err := projectLifecycleContext(ctx, &node, document, options); err != nil {
			return toolkitProjection{}, err
		}
		projected := []projectionNode{node}
		projected = append(projected, conceptAssets...)
		generationNodes, err := projectGenerationContext(ctx, concept.ID, document)
		if err != nil {
			return toolkitProjection{}, err
		}
		projected = append(projected, generationNodes...)
		verificationNodes, err := projectVerificationsContext(ctx, concept.ID, document)
		if err != nil {
			return toolkitProjection{}, err
		}
		projected = append(projected, verificationNodes...)
		sourceNodes, sourceAssets, sourcesByNormalizedID, err := projectSourcesContext(ctx, b, concept)
		if err != nil {
			return toolkitProjection{}, err
		}
		projected = append(projected, sourceNodes...)
		projected = append(projected, sourceAssets...)
		attributionNodes, err := projectAttributionsContext(ctx, concept.ID, document, sourcesByNormalizedID)
		if err != nil {
			return toolkitProjection{}, err
		}
		projected = append(projected, attributionNodes...)
		computationNodes, err := projectAttestedComputationContext(ctx, b, concept)
		if err != nil {
			return toolkitProjection{}, err
		}
		projected = append(projected, computationNodes...)
		projection.Nodes = append(projection.Nodes, projected...)

		if options.ExtensionRelations == ExtensionRelationsInclude {
			relationNodes, err := projectExtensionRelationsContext(ctx, b, concept.ID)
			if err != nil {
				return toolkitProjection{}, err
			}
			projection.Nodes = append(projection.Nodes, relationNodes...)
		}
	}
	if err := sortProjectionContext(ctx, &projection); err != nil {
		return toolkitProjection{}, err
	}
	return projection, nil
}

func projectLifecycle(node *projectionNode, document bundle.Document, options Options) {
	_ = projectLifecycleContext(context.Background(), node, document, options)
}

func projectLifecycleContext(ctx context.Context, node *projectionNode, document bundle.Document, options Options) error {
	trustTier, err := document.TrustTierContext(ctx)
	if err != nil {
		return err
	}
	node.addLiteral("trustTier", string(trustTier))
	status, err := document.StatusStateContext(ctx)
	if err != nil {
		return err
	}
	if status.Valid {
		node.addLiteral("effectiveStatus", status.Effective)
		if status.Present {
			node.addLiteral("declaredStatus", status.Raw)
		}
	} else if status.Present {
		if status.Raw != "" {
			node.addLiteral("declaredStatus", status.Raw)
		}
		node.addLiteral("effectiveStatus", "unresolved")
	}
	staleObservation, err := document.Frontmatter.StaleAfterObservationContext(ctx)
	if err != nil {
		return err
	}
	if staleAfter := staleObservation.Value; staleAfter.State == bundle.TemporalValid {
		node.add("staleAfter", projectionValue{Kind: projectionDate, Value: staleAfter.Raw})
		if options.AsOf != nil {
			year, month, day := options.AsOf.Date()
			asOf := time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
			node.add("stalenessAsOf", projectionValue{Kind: projectionDate, Value: asOf})
			stale, err := document.IsStaleContext(ctx, *options.AsOf)
			if err != nil {
				return err
			}
			node.addBoolean("stale", stale)
		}
	}
	return checkGraphContext(ctx)
}

func projectGeneration(id bundle.ConceptID, document bundle.Document) []projectionNode {
	nodes, _ := projectGenerationContext(context.Background(), id, document)
	return nodes
}

func projectGenerationContext(ctx context.Context, id bundle.ConceptID, document bundle.Document) ([]projectionNode, error) {
	if err := checkGraphContext(ctx); err != nil {
		return nil, err
	}
	generation, err := document.GenerationStateContext(ctx)
	if err != nil {
		return nil, err
	}
	effectiveAt, err := document.EffectiveContentChangeTimeValueContext(ctx)
	if err != nil {
		return nil, err
	}
	fallback, err := document.LegacyFallbackObservationContext(ctx)
	if err != nil {
		return nil, err
	}
	legacyFallback := fallback.TimestampAllowed &&
		effectiveAt.State == bundle.TemporalValid
	if !generation.HasValue && !legacyFallback {
		return nil, nil
	}
	nodeID, err := toolkitChildIRIContext(ctx, id, "generation", "000000")
	if err != nil {
		return nil, err
	}
	node := newProjectionNode(nodeID, "Generation")
	if generation.By.Valid {
		node.addLiteral("actor", generation.By.Value)
	}
	if generation.At.State == bundle.TemporalValid {
		node.add("at", projectionValue{Kind: projectionDateTime, Value: generation.At.Raw})
	}
	if legacyFallback {
		node.add("at", projectionValue{Kind: projectionDateTime, Value: effectiveAt.Raw})
		node.addBoolean("legacyFallback", true)
	}
	conceptIRI, err := toolkitConceptIRIContext(ctx, id)
	if err != nil {
		return nil, err
	}
	node.addIRI("generationOf", conceptIRI)
	return []projectionNode{node}, checkGraphContext(ctx)
}

func projectVerifications(id bundle.ConceptID, document bundle.Document) []projectionNode {
	nodes, _ := projectVerificationsContext(context.Background(), id, document)
	return nodes
}

func projectVerificationsContext(ctx context.Context, id bundle.ConceptID, document bundle.Document) ([]projectionNode, error) {
	if err := checkGraphContext(ctx); err != nil {
		return nil, err
	}
	verifications, err := document.VerificationsContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := stableSortContext(ctx, verifications, func(ctx context.Context, left, right bundle.Verification) (bool, error) {
		comparison, err := compareGraphStringsContext(ctx, left.At, right.At)
		if err != nil || comparison != 0 {
			return comparison < 0, err
		}
		comparison, err = compareGraphStringsContext(ctx, left.By, right.By)
		return comparison < 0, err
	}); err != nil {
		return nil, err
	}
	nodes := make([]projectionNode, 0, len(verifications))
	conceptIRI, err := toolkitConceptIRIContext(ctx, id)
	if err != nil {
		return nil, err
	}
	for ordinal, verification := range verifications {
		if ordinal%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, err
			}
		}
		nodeID, err := toolkitChildIRIContext(ctx, id, "verification", ordinalString(ordinal))
		if err != nil {
			return nil, err
		}
		node := newProjectionNode(nodeID, "Verification")
		if verification.By != "" {
			node.addLiteral("actor", verification.By)
		}
		if at := verification.AtValue(); at.State == bundle.TemporalValid {
			node.add("at", projectionValue{Kind: projectionDateTime, Value: at.Raw})
		}
		node.addIRI("verificationOf", conceptIRI)
		nodes = append(nodes, node)
	}
	return nodes, checkGraphContext(ctx)
}

func projectSources(
	b *bundle.Bundle,
	concept bundle.Concept,
) ([]projectionNode, []projectionNode, normalizedSourceLookup) {
	nodes, assets, byID, _ := projectSourcesContext(context.Background(), b, concept)
	return nodes, assets, byID
}

func projectSourcesContext(
	ctx context.Context,
	b *bundle.Bundle,
	concept bundle.Concept,
) ([]projectionNode, []projectionNode, normalizedSourceLookup, error) {
	if err := checkGraphContext(ctx); err != nil {
		return nil, nil, nil, err
	}
	id := concept.ID
	document := concept.Document
	sourceStates, err := document.SourceStatesContext(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	fallback, err := document.LegacyFallbackObservationContext(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if fallback.CitationsAllowed {
		return projectLegacyCitationsContext(ctx, b, concept)
	}
	validSources := make([]bundle.ProvenanceSourceState, 0, len(sourceStates))
	for index, state := range sourceStates {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, nil, nil, err
			}
		}
		if state.Valid && state.Resource.Valid {
			validSources = append(validSources, state)
		}
	}
	if err := stableSortContext(ctx, validSources, provenanceSourceStateLessContext); err != nil {
		return nil, nil, nil, err
	}
	nodes := make([]projectionNode, 0, len(validSources))
	var assets []projectionNode
	var byID normalizedSourceLookup
	sharedWindow, err := document.Frontmatter.UsageWindowStateContext(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	for ordinal, sourceState := range validSources {
		if ordinal%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, nil, nil, err
			}
		}
		source := sourceState.Value
		nodeIRI, err := toolkitChildIRIContext(ctx, id, "source", ordinalString(ordinal))
		if err != nil {
			return nil, nil, nil, err
		}
		node := newProjectionNode(nodeIRI, "ProvenanceSource")
		if source.ID != "" {
			node.addLiteral("sourceID", source.ID)
			normalizedID, err := markdownowner.NormalizeFootnoteLabelContext(ctx, source.ID)
			if err != nil {
				return nil, nil, nil, err
			}
			if normalizedID != "" {
				if err := byID.appendContext(ctx, normalizedID, nodeIRI); err != nil {
					return nil, nil, nil, err
				}
			}
		}
		if source.Resource != "" {
			node.addLiteral("resource", source.Resource)
			if resolved, ok, err := b.ResolvePathValueForContext(ctx, concept.Path, source.Resource, bundle.PathFieldSourceResource); err != nil {
				return nil, nil, nil, err
			} else if ok {
				node.addLiteral("resourceKind", string(resolved.Kind))
				if asset, ok, err := projectPathValueContext(ctx, concept.ID, resolved, "source"); err != nil {
					return nil, nil, nil, err
				} else if ok {
					node.addIRI("resourceNode", asset.ID)
					assets = append(assets, asset)
				}
			}
		}
		if source.Title != "" {
			node.addLiteral("title", source.Title)
		}
		if source.Author != "" {
			node.addLiteral("author", source.Author)
		}
		if sourceState.UsageCount.Valid {
			node.addUnsignedInteger("usageCount", sourceState.UsageCount.Value)
		}
		if sourceState.LastModified.State == bundle.TemporalValid {
			node.add("lastModified", projectionValue{Kind: projectionDate, Value: sourceState.LastModified.Raw})
		}
		window := sharedWindow
		override := sourceState.UsageWindow.Present
		if override {
			window = sourceState.UsageWindow
		}
		if window.Valid {
			if window.From.State == bundle.TemporalValid {
				node.add("usageFrom", projectionValue{Kind: projectionDate, Value: window.From.Raw})
			}
			if window.To.State == bundle.TemporalValid {
				node.add("usageTo", projectionValue{Kind: projectionDate, Value: window.To.Raw})
			}
			node.addBoolean("sourceUsageWindowOverride", override)
		}
		conceptIRI, err := toolkitConceptIRIContext(ctx, id)
		if err != nil {
			return nil, nil, nil, err
		}
		node.addIRI("sourceOf", conceptIRI)
		nodes = append(nodes, node)
	}
	return nodes, assets, byID, checkGraphContext(ctx)
}

func projectLegacyCitations(
	b *bundle.Bundle,
	concept bundle.Concept,
) ([]projectionNode, []projectionNode, normalizedSourceLookup) {
	nodes, assets, byID, _ := projectLegacyCitationsContext(context.Background(), b, concept)
	return nodes, assets, byID
}

func projectLegacyCitationsContext(
	ctx context.Context,
	b *bundle.Bundle,
	concept bundle.Concept,
) ([]projectionNode, []projectionNode, normalizedSourceLookup, error) {
	if err := checkGraphContext(ctx); err != nil {
		return nil, nil, nil, err
	}
	projection, err := markdownowner.CollectCitationSectionProjection(ctx, []byte(concept.Document.Body))
	if err != nil {
		return nil, nil, nil, err
	}
	citations := make([]bundle.Citation, 0, len(projection.Entries))
	for index, entry := range projection.Entries {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, nil, nil, err
			}
		}
		number := entry.Number
		if number == 0 {
			number = uint64(entry.Ordinal)
		}
		if number > uint64(^uint(0)>>1) {
			continue
		}
		citations = append(citations, bundle.Citation{
			Number: int(number),
			Text:   entry.Title,
			Target: entry.Resource,
			Raw:    entry.Raw,
		})
	}
	nodes := make([]projectionNode, 0, len(citations))
	var assets []projectionNode
	for ordinal, citation := range citations {
		if ordinal%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, nil, nil, err
			}
		}
		nodeID, err := toolkitChildIRIContext(ctx, concept.ID, "source", "legacy-"+ordinalString(ordinal))
		if err != nil {
			return nil, nil, nil, err
		}
		node := newProjectionNode(nodeID, "ProvenanceSource")
		resource := citation.Target
		if resource == "" {
			resource = citation.Raw
		}
		node.addLiteral("resource", resource)
		if citation.Text != "" {
			node.addLiteral("title", citation.Text)
		}
		node.addBoolean("legacyFallback", true)
		node.addInteger("legacyCitationNumber", int64(citation.Number))
		conceptIRI, err := toolkitConceptIRIContext(ctx, concept.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		node.addIRI("sourceOf", conceptIRI)
		if resolved, ok, err := b.ResolvePathValueForContext(ctx, concept.Path, resource, bundle.PathFieldSourceResource); err != nil {
			return nil, nil, nil, err
		} else if ok {
			node.addLiteral("resourceKind", string(resolved.Kind))
			if asset, ok, err := projectPathValueContext(ctx, concept.ID, resolved, "source"); err != nil {
				return nil, nil, nil, err
			} else if ok {
				node.addIRI("resourceNode", asset.ID)
				assets = append(assets, asset)
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, assets, nil, checkGraphContext(ctx)
}

func projectAttributions(
	id bundle.ConceptID,
	document bundle.Document,
	sourcesByNormalizedID normalizedSourceLookup,
) []projectionNode {
	nodes, _ := projectAttributionsContext(context.Background(), id, document, sourcesByNormalizedID)
	return nodes
}

func projectAttributionsContext(
	ctx context.Context,
	id bundle.ConceptID,
	document bundle.Document,
	sourcesByNormalizedID normalizedSourceLookup,
) ([]projectionNode, error) {
	if err := checkGraphContext(ctx); err != nil {
		return nil, err
	}
	attributions, err := document.AttributionsContext(ctx)
	if err != nil {
		return nil, err
	}
	nodes := make([]projectionNode, 0, len(attributions))
	for index, attribution := range attributions {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, err
			}
		}
		normalizedID, err := markdownowner.NormalizeFootnoteLabelContext(ctx, attribution.ID)
		if err != nil {
			return nil, err
		}
		if normalizedID == "" {
			continue
		}
		nodeID, err := toolkitChildIRIContext(ctx, id, "attribution", normalizedID)
		if err != nil {
			return nil, err
		}
		node := newProjectionNode(nodeID, "ClaimAttribution")
		node.addLiteral("sourceID", normalizedID)
		conceptIRI, err := toolkitConceptIRIContext(ctx, id)
		if err != nil {
			return nil, err
		}
		node.addIRI("attributionOf", conceptIRI)
		node.addInteger("referenceCount", int64(len(attribution.References)))
		node.addInteger("definitionCount", int64(len(attribution.Definitions)))
		matches, err := sourcesByNormalizedID.getContext(ctx, normalizedID)
		if err != nil {
			return nil, err
		}
		if err := sortGraphStringsContext(ctx, matches); err != nil {
			return nil, err
		}
		node.addBoolean("resolved", len(matches) > 0)
		for matchIndex, sourceIRI := range matches {
			if matchIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return nil, err
				}
			}
			node.addIRI("attributedSource", sourceIRI)
		}
		nodes = append(nodes, node)
	}
	return nodes, checkGraphContext(ctx)
}

func projectAttestedComputation(b *bundle.Bundle, concept bundle.Concept) []projectionNode {
	nodes, _ := projectAttestedComputationContext(context.Background(), b, concept)
	return nodes
}

func projectAttestedComputationContext(ctx context.Context, b *bundle.Bundle, concept bundle.Concept) ([]projectionNode, error) {
	if err := checkGraphContext(ctx); err != nil {
		return nil, err
	}
	id := concept.ID
	state, err := concept.Document.AttestedComputationStateContext(ctx)
	if err != nil {
		return nil, err
	}
	if !state.TypeMatches {
		return nil, nil
	}
	contractState := state.ContractState
	var nodes []projectionNode
	contractID, err := toolkitChildIRIContext(ctx, id, "computation-contract", "000000")
	if err != nil {
		return nil, err
	}
	node := newProjectionNode(contractID, "AttestedComputationContract")
	conceptIRI, err := toolkitConceptIRIContext(ctx, id)
	if err != nil {
		return nil, err
	}
	node.addIRI("contractOf", conceptIRI)
	node.addLiteral("computationMode", string(state.Mode))
	if contractState.Runtime.Valid {
		node.addLiteral("runtime", contractState.Runtime.Value)
	}
	if state.Valid && state.Mode == bundle.ComputationModeFile && contractState.Computation.Valid {
		node.addLiteral("computationResource", contractState.Computation.Value)
		if resolved, ok, err := b.ResolvePathValueForContext(
			ctx,
			concept.Path,
			contractState.Computation.Value,
			bundle.PathFieldComputation,
		); err != nil {
			return nil, err
		} else if ok {
			node.addLiteral("computationResourceKind", string(resolved.Kind))
			if asset, ok, err := projectPathValueContext(ctx, id, resolved, "computation"); err != nil {
				return nil, err
			} else if ok {
				node.addIRI("computationNode", asset.ID)
				nodes = append(nodes, asset)
			}
		}
	}
	if contractState.Executor.Valid && contractState.Executor.HasValue {
		executor := contractState.Executor
		if executor.Resource.Valid {
			node.addLiteral("executorResource", executor.Resource.Value)
			if resolved, ok, err := b.ResolvePathValueForContext(
				ctx,
				concept.Path,
				executor.Resource.Value,
				bundle.PathFieldExecutorResource,
			); err != nil {
				return nil, err
			} else if ok {
				node.addLiteral("executorResourceKind", string(resolved.Kind))
				if asset, ok, err := projectPathValueContext(ctx, id, resolved, "executor"); err != nil {
					return nil, err
				} else if ok {
					node.addIRI("executorNode", asset.ID)
					nodes = append(nodes, asset)
				}
			}
		}
		receipts := append([]string(nil), executor.Receipt.Values...)
		if err := sortGraphStringsContext(ctx, receipts); err != nil {
			return nil, err
		}
		for index, receipt := range receipts {
			if index%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return nil, err
				}
			}
			node.addLiteral("receiptField", receipt)
		}
	}
	if contractState.Attester.Valid && contractState.Attester.HasValue &&
		contractState.Attester.Resource.Valid {
		attesterResource := contractState.Attester.Resource.Value
		node.addLiteral("attesterResource", attesterResource)
		if resolved, ok, err := b.ResolvePathValueForContext(
			ctx,
			concept.Path,
			attesterResource,
			bundle.PathFieldAttesterResource,
		); err != nil {
			return nil, err
		} else if ok {
			node.addLiteral("attesterResourceKind", string(resolved.Kind))
			if asset, ok, err := projectPathValueContext(ctx, id, resolved, "attester"); err != nil {
				return nil, err
			} else if ok {
				node.addIRI("attesterNode", asset.ID)
				nodes = append(nodes, asset)
			}
		}
	}
	nodes = append([]projectionNode{node}, nodes...)
	var parameters []bundle.ComputationParameter
	if contractState.ParametersValid {
		parameters = make([]bundle.ComputationParameter, 0, len(contractState.Parameters))
		for index, parameter := range contractState.Parameters {
			if index%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return nil, err
				}
			}
			if parameter.Valid {
				parameters = append(parameters, parameter.Value)
			}
		}
	}
	if err := stableSortContext(ctx, parameters, func(ctx context.Context, left, right bundle.ComputationParameter) (bool, error) {
		comparison, err := compareGraphStringsContext(ctx, left.Name, right.Name)
		if err != nil || comparison != 0 {
			return comparison < 0, err
		}
		comparison, err = compareGraphStringsContext(ctx, left.Type, right.Type)
		if err != nil || comparison != 0 {
			return comparison < 0, err
		}
		return !left.Required && right.Required, nil
	}); err != nil {
		return nil, err
	}
	for ordinal, parameter := range parameters {
		if ordinal%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, err
			}
		}
		parameterID, err := toolkitChildIRIContext(ctx, id, "parameter", ordinalString(ordinal))
		if err != nil {
			return nil, err
		}
		parameterNode := newProjectionNode(parameterID, "ComputationParameter")
		parameterNode.addIRI("parameterOf", contractID)
		parameterNode.addLiteral("name", parameter.Name)
		parameterNode.addLiteral("parameterType", parameter.Type)
		parameterNode.addBoolean("required", parameter.Required)
		nodes = append(nodes, parameterNode)
	}
	return nodes, checkGraphContext(ctx)
}

func projectPathValue(id bundle.ConceptID, resolved bundle.ResolvedPathValue, role string) (projectionNode, bool) {
	node, ok, _ := projectPathValueContext(context.Background(), id, resolved, role)
	return node, ok
}

func projectPathValueContext(ctx context.Context, id bundle.ConceptID, resolved bundle.ResolvedPathValue, role string) (projectionNode, bool, error) {
	if err := checkGraphContext(ctx); err != nil {
		return projectionNode{}, false, err
	}
	if resolved.Kind != bundle.PathValueRelative && resolved.Kind != bundle.PathValueBundleRelative {
		return projectionNode{}, false, nil
	}
	assetID := ""
	var err error
	if resolved.Path != "" {
		assetID, err = toolkitSyntheticIRIContext(ctx, "asset", resolved.Path)
	} else {
		identity, identityErr := graphConceptIDContext(ctx, id)
		if identityErr != nil {
			return projectionNode{}, false, identityErr
		}
		assetID, err = toolkitSyntheticIRIContext(ctx, "unresolved-asset", identity, role, resolved.Raw)
	}
	if err != nil {
		return projectionNode{}, false, err
	}
	node := newProjectionNode(assetID, "ReferencedAsset")
	node.addLiteral("resource", resolved.Raw)
	node.addLiteral("resourceKind", string(resolved.Kind))
	if resolved.Path != "" {
		node.addLiteral("bundlePath", resolved.Path)
	}
	node.addBoolean("exists", resolved.Exists)
	node.addLiteral("resourceRole", role)
	conceptIRI, err := toolkitConceptIRIContext(ctx, id)
	if err != nil {
		return projectionNode{}, false, err
	}
	node.addIRI("referencedBy", conceptIRI)
	return node, true, checkGraphContext(ctx)
}

func projectExtensionRelations(b *bundle.Bundle, conceptID bundle.ConceptID) []projectionNode {
	nodes, _ := projectExtensionRelationsContext(context.Background(), b, conceptID)
	return nodes
}

func projectExtensionRelationsContext(ctx context.Context, b *bundle.Bundle, conceptID bundle.ConceptID) ([]projectionNode, error) {
	if err := checkGraphContext(ctx); err != nil {
		return nil, err
	}
	observations, err := b.DeclaredSemanticLinksFromContext(ctx, conceptID)
	if err != nil {
		return nil, err
	}
	if err := stableSortContext(ctx, observations, func(ctx context.Context, left, right bundle.RelationObservation) (bool, error) {
		comparison, err := compareRelationRefsContext(ctx, left.Source, right.Source)
		if err != nil || comparison != 0 {
			return comparison < 0, err
		}
		comparison, err = compareGraphStringsContext(ctx, left.Type, right.Type)
		if err != nil || comparison != 0 {
			return comparison < 0, err
		}
		comparison, err = compareRelationRefsContext(ctx, left.Target, right.Target)
		if err != nil || comparison != 0 {
			return comparison < 0, err
		}
		comparison, err = compareGraphStringsContext(ctx, left.RawTarget, right.RawTarget)
		if err != nil || comparison != 0 {
			return comparison < 0, err
		}
		return !left.TargetExists && right.TargetExists, nil
	}); err != nil {
		return nil, err
	}
	nodes := make([]projectionNode, 0, len(observations))
	conceptIdentity, err := graphConceptIDContext(ctx, conceptID)
	if err != nil {
		return nil, err
	}
	for ordinal, observation := range observations {
		if ordinal%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, err
			}
		}
		nodeID, err := toolkitSyntheticIRIContext(ctx, "extension-relation", conceptIdentity, ordinalString(ordinal))
		if err != nil {
			return nil, err
		}
		node := newProjectionNode(nodeID, "ToolkitExtensionRelation")
		node.addLiteral("extensionNamespace", "skosovsky/okf")
		node.addLiteral("relationType", observation.Type)
		sourceIRI, err := toolkitRelationRefIRIContext(ctx, observation.Source)
		if err != nil {
			return nil, err
		}
		targetIRI, err := toolkitRelationRefIRIContext(ctx, observation.Target)
		if err != nil {
			return nil, err
		}
		node.addIRI("relationSource", sourceIRI)
		node.addIRI("relationTarget", targetIRI)
		node.addBoolean("targetExists", observation.TargetExists)
		nodes = append(nodes, node)
	}
	return nodes, checkGraphContext(ctx)
}

func compareRelationRefs(left, right bundle.RelationRef) int {
	comparison, _ := compareRelationRefsContext(context.Background(), left, right)
	return comparison
}

func compareRelationRefsContext(ctx context.Context, left, right bundle.RelationRef) (int, error) {
	leftIdentity, err := graphConceptIDContext(ctx, left.ID)
	if err != nil {
		return 0, err
	}
	rightIdentity, err := graphConceptIDContext(ctx, right.ID)
	if err != nil {
		return 0, err
	}
	comparison, err := compareGraphStringsContext(ctx, leftIdentity, rightIdentity)
	if err != nil || comparison != 0 {
		return comparison, err
	}
	return compareGraphStringsContext(ctx, left.Fragment, right.Fragment)
}

func newProjectionNode(id string, types ...string) projectionNode {
	return projectionNode{
		ID:         id,
		Types:      append([]string(nil), types...),
		Properties: make(map[string][]projectionValue),
	}
}

func (n *projectionNode) addLiteral(property, value string) {
	n.add(property, projectionValue{Kind: projectionLiteral, Value: value})
}

func (n *projectionNode) addIRI(property, value string) {
	n.add(property, projectionValue{Kind: projectionIRI, Value: value})
}

func (n *projectionNode) addBoolean(property string, value bool) {
	n.add(property, projectionValue{Kind: projectionBoolean, Value: fmt.Sprintf("%t", value)})
}

func (n *projectionNode) addInteger(property string, value int64) {
	n.add(property, projectionValue{Kind: projectionInteger, Value: strconv.FormatInt(value, 10)})
}

func (n *projectionNode) addUnsignedInteger(property string, value uint64) {
	n.add(property, projectionValue{Kind: projectionInteger, Value: strconv.FormatUint(value, 10)})
}

func (n *projectionNode) add(property string, value projectionValue) {
	n.Properties[property] = append(n.Properties[property], value)
}

func toolkitConceptIRI(id bundle.ConceptID) string {
	value, _ := toolkitConceptIRIContext(context.Background(), id)
	return value
}

func toolkitConceptIRIContext(ctx context.Context, id bundle.ConceptID) (string, error) {
	identity, err := graphConceptIDContext(ctx, id)
	if err != nil {
		return "", err
	}
	escaped, err := graphPathEscapeContext(ctx, identity)
	if err != nil {
		return "", err
	}
	return concatGraphStringsContext(ctx, toolkitBundlePrefix, escaped)
}

func toolkitRelationRefIRI(ref bundle.RelationRef) string {
	value, _ := toolkitRelationRefIRIContext(context.Background(), ref)
	return value
}

func toolkitRelationRefIRIContext(ctx context.Context, ref bundle.RelationRef) (string, error) {
	value, err := toolkitConceptIRIContext(ctx, ref.ID)
	if err != nil {
		return "", err
	}
	if ref.Fragment != "" {
		escaped, err := graphPathEscapeContext(ctx, ref.Fragment)
		if err != nil {
			return "", err
		}
		value, err = concatGraphStringsContext(ctx, value, "#", escaped)
		if err != nil {
			return "", err
		}
	}
	return value, checkGraphContext(ctx)
}

func toolkitChildIRI(id bundle.ConceptID, kind, key string) string {
	value, _ := toolkitChildIRIContext(context.Background(), id, kind, key)
	return value
}

func toolkitChildIRIContext(ctx context.Context, id bundle.ConceptID, kind, key string) (string, error) {
	identity, err := graphConceptIDContext(ctx, id)
	if err != nil {
		return "", err
	}
	return toolkitSyntheticIRIContext(ctx, kind, identity, key)
}

func ordinalString(ordinal int) string {
	return fmt.Sprintf("%06d", ordinal)
}

func toolkitSyntheticIRI(kind string, components ...string) string {
	value, _ := toolkitSyntheticIRIContext(context.Background(), kind, components...)
	return value
}

func toolkitSyntheticIRIContext(ctx context.Context, kind string, components ...string) (string, error) {
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	var value strings.Builder
	if err := appendGraphStringContext(ctx, &value, toolkitSyntheticPrefix); err != nil {
		return "", err
	}
	if err := appendGraphStringContext(ctx, &value, kind); err != nil {
		return "", err
	}
	for _, component := range components {
		value.WriteByte(':')
		const chunkSize = 3 * 4096
		for offset := 0; offset < len(component); offset += chunkSize {
			if err := checkGraphContext(ctx); err != nil {
				return "", err
			}
			end := min(offset+chunkSize, len(component))
			value.WriteString(base64.RawURLEncoding.EncodeToString([]byte(component[offset:end])))
		}
	}
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	return value.String(), nil
}

func provenanceSourceStateLess(left, right bundle.ProvenanceSourceState) bool {
	less, _ := provenanceSourceStateLessContext(context.Background(), left, right)
	return less
}

func provenanceSourceStateLessContext(
	ctx context.Context,
	left, right bundle.ProvenanceSourceState,
) (bool, error) {
	leftValue, rightValue := left.Value, right.Value
	for _, pair := range [][2]string{
		{leftValue.ID, rightValue.ID},
		{leftValue.Resource, rightValue.Resource},
		{leftValue.Title, rightValue.Title},
		{leftValue.Author, rightValue.Author},
		{leftValue.LastModified, rightValue.LastModified},
	} {
		comparison, err := compareGraphStringsContext(ctx, pair[0], pair[1])
		if err != nil || comparison != 0 {
			return comparison < 0, err
		}
	}
	if (leftValue.UsageCount == nil) != (rightValue.UsageCount == nil) {
		return leftValue.UsageCount == nil, nil
	}
	if leftValue.UsageCount != nil && *leftValue.UsageCount != *rightValue.UsageCount {
		return *leftValue.UsageCount < *rightValue.UsageCount, nil
	}
	if (leftValue.UsageWindow == nil) != (rightValue.UsageWindow == nil) {
		return leftValue.UsageWindow == nil, nil
	}
	if leftValue.UsageWindow != nil {
		comparison, err := compareGraphStringsContext(ctx, leftValue.UsageWindow.From, rightValue.UsageWindow.From)
		if err != nil || comparison != 0 {
			return comparison < 0, err
		}
		comparison, err = compareGraphStringsContext(ctx, leftValue.UsageWindow.To, rightValue.UsageWindow.To)
		if err != nil || comparison != 0 {
			return comparison < 0, err
		}
	}

	for _, pair := range [][2]bool{
		{left.ID.Present, right.ID.Present},
		{left.Resource.Present, right.Resource.Present},
		{left.Title.Present, right.Title.Present},
		{left.Author.Present, right.Author.Present},
		{left.UsageCount.Present, right.UsageCount.Present},
		{left.UsageWindow.Present, right.UsageWindow.Present},
	} {
		if pair[0] != pair[1] {
			return !pair[0] && pair[1], nil
		}
	}
	return left.LastModified.State < right.LastModified.State, checkGraphContext(ctx)
}

func sortProjection(projection *toolkitProjection) {
	_ = sortProjectionContext(context.Background(), projection)
}

func sortProjectionContext(ctx context.Context, projection *toolkitProjection) error {
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	working, err := cloneProjectionNodesContext(ctx, projection.Nodes)
	if err != nil {
		return err
	}
	coalesced := make([]projectionNode, 0, len(working))
	for index, node := range working {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		existingIndex := -1
		for candidate := range coalesced {
			equal, err := equalGraphStringsContext(ctx, coalesced[candidate].ID, node.ID)
			if err != nil {
				return err
			}
			if equal {
				existingIndex = candidate
				break
			}
		}
		if existingIndex < 0 {
			coalesced = append(coalesced, node)
			continue
		}
		existing := coalesced[existingIndex]
		existing.Types = append(existing.Types, node.Types...)
		propertyIndex := 0
		for property, values := range node.Properties {
			if propertyIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			propertyIndex++
			existing.Properties[property] = append(existing.Properties[property], values...)
		}
		coalesced[existingIndex] = existing
	}
	for i := range coalesced {
		if i%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		node := &coalesced[i]
		if err := sortGraphStringsContext(ctx, node.Types); err != nil {
			return err
		}
		var err error
		node.Types, err = compactSortedStringsContext(ctx, node.Types)
		if err != nil {
			return err
		}
		propertyIndex := 0
		for property := range node.Properties {
			if propertyIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			propertyIndex++
			values := node.Properties[property]
			if err := stableSortContext(ctx, values, func(ctx context.Context, left, right projectionValue) (bool, error) {
				if left.Kind != right.Kind {
					return left.Kind < right.Kind, nil
				}
				comparison, err := compareGraphStringsContext(ctx, left.Value, right.Value)
				return comparison < 0, err
			}); err != nil {
				return err
			}
			values, err = compactProjectionValuesContext(ctx, values)
			if err != nil {
				return err
			}
			node.Properties[property] = values
		}
	}
	if err := stableSortContext(ctx, coalesced, func(ctx context.Context, left, right projectionNode) (bool, error) {
		comparison, err := compareGraphStringsContext(ctx, left.ID, right.ID)
		return comparison < 0, err
	}); err != nil {
		return err
	}
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	projection.Nodes = coalesced
	return nil
}

func cloneProjectionNodesContext(ctx context.Context, nodes []projectionNode) ([]projectionNode, error) {
	if err := checkGraphContext(ctx); err != nil {
		return nil, err
	}
	cloned := make([]projectionNode, len(nodes))
	for index, node := range nodes {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, err
			}
		}
		cloned[index] = node
		cloned[index].Types = append([]string(nil), node.Types...)
		if node.Properties != nil {
			cloned[index].Properties = make(map[string][]projectionValue, len(node.Properties))
			propertyIndex := 0
			for property, values := range node.Properties {
				if propertyIndex%graphContextCheckInterval == 0 {
					if err := checkGraphContext(ctx); err != nil {
						return nil, err
					}
				}
				propertyIndex++
				cloned[index].Properties[property] = append([]projectionValue(nil), values...)
			}
		}
	}
	if err := checkGraphContext(ctx); err != nil {
		return nil, err
	}
	return cloned, nil
}

func compactSortedStrings(values []string) []string {
	out, _ := compactSortedStringsContext(context.Background(), values)
	return out
}

func compactSortedStringsContext(ctx context.Context, values []string) ([]string, error) {
	out := values[:0]
	for index, value := range values {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, err
			}
		}
		equal := false
		if len(out) > 0 {
			var err error
			equal, err = equalGraphStringsContext(ctx, out[len(out)-1], value)
			if err != nil {
				return nil, err
			}
		}
		if !equal {
			out = append(out, value)
		}
	}
	return out, checkGraphContext(ctx)
}

func compactProjectionValues(values []projectionValue) []projectionValue {
	out, _ := compactProjectionValuesContext(context.Background(), values)
	return out
}

func compactProjectionValuesContext(ctx context.Context, values []projectionValue) ([]projectionValue, error) {
	out := values[:0]
	for index, value := range values {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, err
			}
		}
		equal := false
		if len(out) > 0 && out[len(out)-1].Kind == value.Kind {
			var err error
			equal, err = equalGraphStringsContext(ctx, out[len(out)-1].Value, value.Value)
			if err != nil {
				return nil, err
			}
		}
		if !equal {
			out = append(out, value)
		}
	}
	return out, checkGraphContext(ctx)
}
