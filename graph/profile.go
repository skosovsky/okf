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
	// ProjectionProfileToolkitV02Instant uses the later OKF 0.2 temporal
	// revision and xsd:dateTime for its temporal concept/source fields.
	ProjectionProfileToolkitV02Instant ProjectionProfile = "skosovsky/okf-v0.2-instant"
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
	case ProjectionProfileLegacyV01, ProjectionProfileToolkitV02, ProjectionProfileToolkitV02Instant:
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

func selectGraphBundleContext(ctx context.Context, b *bundle.Bundle, options Options) (*bundle.Bundle, bundle.VersionResolution, error) {
	if err := checkGraphContext(ctx); err != nil {
		return nil, bundle.VersionResolution{}, err
	}
	version, err := b.VersionResolutionContext(ctx, options.VersionSelector)
	if err != nil {
		return nil, bundle.VersionResolution{}, err
	}
	selected, err := b.WithVersionResolutionContext(ctx, version)
	return selected, version, err
}

func topologyAnnotation(document bundle.Document, options Options) string {
	annotation, _ := topologyAnnotationContext(context.Background(), document, options)
	return annotation
}

func topologyAnnotationContext(ctx context.Context, document bundle.Document, options Options, versions ...bundle.VersionResolution) (string, error) {
	var version bundle.VersionResolution
	if len(versions) > 0 {
		version = versions[0]
	}
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	status, err := document.StatusStateForVersionContext(ctx, version)
	if err != nil {
		return "", err
	}
	effectiveStatus := status.Effective
	if !status.Valid {
		effectiveStatus = "unresolved"
	}
	trustTier, err := document.TrustTierForVersionContext(ctx, version)
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
	if version.Effective == bundle.OKFVersion03 && !status.Valid && status.Raw != "" {
		rawStatusPart, err := concatGraphStringsContext(ctx, "status_raw=", status.Raw)
		if err != nil {
			return "", err
		}
		parts = append(parts, rawStatusPart)
	}
	if options.AsOf != nil || version.Effective == bundle.OKFVersion03 {
		temporalProfile := bundle.TemporalProfileDate
		if options.Profile == ProjectionProfileToolkitV02Instant {
			temporalProfile = bundle.TemporalProfileInstant
		}
		temporalProfile = bundle.TemporalProfileForVersion(version, temporalProfile)
		observation, err := document.Frontmatter.StaleAfterForProfileContext(ctx, temporalProfile)
		if err != nil {
			return "", err
		}
		if temporalProfile == bundle.TemporalProfileV03 && observation.Value.State == bundle.TemporalMalformed {
			parts = append(parts, "stale_after=malformed")
		}
		if options.AsOf != nil && observation.Value.State == bundle.TemporalValid {
			stale, _, err := document.Frontmatter.IsStaleForProfileContext(ctx, *options.AsOf, temporalProfile)
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
