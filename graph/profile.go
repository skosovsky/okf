package graph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/skosovsky/okf/bundle"
)

// ProjectionProfile identifies a graph projection contract. It is separate
// from the OKF document version carried by a bundle.
type ProjectionProfile string

const (
	// ProjectionProfileLegacyV01 preserves the historical graph output.
	ProjectionProfileLegacyV01 ProjectionProfile = "legacy-v0.1"
	// ProjectionProfileToolkitV02 is the skosovsky/okf toolkit projection for
	// OKF v0.2. It is not an upstream OKF ontology.
	ProjectionProfileToolkitV02 ProjectionProfile = "skosovsky/okf-v0.2"
)

// ExtensionRelationPolicy controls projection of the toolkit-specific YAML
// relations extension. Markdown references are unaffected.
type ExtensionRelationPolicy string

const (
	// ExtensionRelationsDefault preserves the selected profile's default.
	ExtensionRelationsDefault ExtensionRelationPolicy = ""
	// ExtensionRelationsExclude omits YAML relations from the projection.
	ExtensionRelationsExclude ExtensionRelationPolicy = "exclude"
	// ExtensionRelationsInclude includes YAML relations. The v0.2 profile
	// marks them as toolkit extensions.
	ExtensionRelationsInclude ExtensionRelationPolicy = "include"
)

// Options controls graph rendering.
//
// VersionSelector is an optional assertion supplied by the caller. An empty
// value uses the bundle's typed version resolution. AsOf is only used for
// derived staleness and never defaults to the wall clock. Text, DOT, and
// Mermaid remain topology-only unless AnnotateTopology is true.
type Options struct {
	Profile            ProjectionProfile
	VersionSelector    string
	AsOf               *time.Time
	ExtensionRelations ExtensionRelationPolicy
	AnnotateTopology   bool
}

func normalizeOptions(options Options) (Options, error) {
	if options.Profile == "" {
		options.Profile = ProjectionProfileLegacyV01
	}
	switch options.Profile {
	case ProjectionProfileLegacyV01, ProjectionProfileToolkitV02:
	default:
		return Options{}, fmt.Errorf("graph: unknown projection profile %q", options.Profile)
	}
	switch options.ExtensionRelations {
	case ExtensionRelationsDefault:
		options.ExtensionRelations = ExtensionRelationsInclude
	case ExtensionRelationsExclude, ExtensionRelationsInclude:
	default:
		return Options{}, fmt.Errorf("graph: unknown extension relation policy %q", options.ExtensionRelations)
	}
	return options, nil
}

func assertVersionSelector(b *bundle.Bundle, options Options) error {
	return assertVersionSelectorContext(context.Background(), b, options)
}

func assertVersionSelectorContext(ctx context.Context, b *bundle.Bundle, options Options) error {
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	if _, err := b.VersionResolutionContext(ctx, options.VersionSelector); err != nil {
		return err
	}
	return checkGraphContext(ctx)
}

func topologyAnnotation(document bundle.Document, options Options) string {
	annotation, _ := topologyAnnotationContext(context.Background(), document, options)
	return annotation
}

func topologyAnnotationContext(ctx context.Context, document bundle.Document, options Options) (string, error) {
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	status, err := document.StatusStateContext(ctx)
	if err != nil {
		return "", err
	}
	effectiveStatus := status.Effective
	if !status.Valid {
		effectiveStatus = "unresolved"
	}
	trustTier, err := document.TrustTierContext(ctx)
	if err != nil {
		return "", err
	}
	statusPart, err := concatGraphStringsContext(ctx, "status=", effectiveStatus)
	if err != nil {
		return "", err
	}
	trustPart, err := concatGraphStringsContext(ctx, "trust=", string(trustTier))
	if err != nil {
		return "", err
	}
	parts := []string{statusPart, trustPart}
	if options.AsOf != nil {
		observation, err := document.Frontmatter.StaleAfterObservationContext(ctx)
		if err != nil {
			return "", err
		}
		if observation.Value.State == bundle.TemporalValid {
			stale, err := document.IsStaleContext(ctx, *options.AsOf)
			if err != nil {
				return "", err
			}
			parts = append(parts, fmt.Sprintf("stale=%t", stale))
		}
	}
	var annotation strings.Builder
	for index, part := range parts {
		if index > 0 {
			annotation.WriteByte(' ')
		}
		for offset := 0; offset < len(part); {
			if err := checkGraphContext(ctx); err != nil {
				return "", err
			}
			end := min(offset+graphContextCheckInterval, len(part))
			for end < len(part) && end > offset && part[end]&0xc0 == 0x80 {
				end--
			}
			if end == offset {
				end = offset + 1
			}
			for _, character := range []byte(part[offset:end]) {
				switch character {
				case '\r', '\n', '\t':
					annotation.WriteByte(' ')
				default:
					annotation.WriteByte(character)
				}
			}
			offset = end
		}
	}
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	return annotation.String(), nil
}
