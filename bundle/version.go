package bundle

import (
	"context"
	"fmt"
	"strings"
)

// VersionSource describes how an effective OKF version was selected.
type VersionSource string

const (
	VersionSourceDeclared         VersionSource = "declared"
	VersionSourceDefault          VersionSource = "default"
	VersionSourceExplicit         VersionSource = "explicit"
	VersionSourceFutureBestEffort VersionSource = "future-best-effort"
)

// VersionCompatibility describes how the v0.2 reader consumes the effective
// contract.
type VersionCompatibility string

const (
	VersionCompatibilityNative     VersionCompatibility = "native"
	VersionCompatibilityLegacy     VersionCompatibility = "legacy"
	VersionCompatibilityBestEffort VersionCompatibility = "best-effort"
)

// VersionResolution keeps declaration, effective semantics, provenance of the
// decision, and compatibility separate.
type VersionResolution struct {
	Declared      string
	Effective     string
	Source        VersionSource
	Compatibility VersionCompatibility
}

// VersionNumber is a canonical numeric <major>.<minor> pair. Components stay
// decimal strings so the syntax contract does not invent an integer ceiling.
type VersionNumber struct {
	Major string
	Minor string
}

// VersionDeclarationState preserves malformed present declarations for
// validator and surface diagnostics.
type VersionDeclarationState struct {
	Present bool
	Valid   bool
	Raw     string
	Value   string
}

// ParseOKFVersion parses the exact canonical <major>.<minor> grammar. Each
// component is decimal with no leading zero unless the component is zero.
func ParseOKFVersion(value string) (VersionNumber, error) {
	return parseOKFVersionContext(context.Background(), value)
}

func parseOKFVersionContext(ctx context.Context, value string) (VersionNumber, error) {
	owned, err := stringFromStringContext(ctx, value)
	if err != nil {
		return VersionNumber{}, err
	}
	major, minor, found := strings.Cut(owned, ".")
	minorHasDot, err := containsStringContext(ctx, minor, '.')
	if err != nil {
		return VersionNumber{}, err
	}
	majorValid, err := canonicalVersionComponentContext(ctx, major)
	if err != nil {
		return VersionNumber{}, err
	}
	minorValid, err := canonicalVersionComponentContext(ctx, minor)
	if err != nil {
		return VersionNumber{}, err
	}
	if !found || minorHasDot || !majorValid || !minorValid {
		return VersionNumber{}, fmt.Errorf("%w: %q", ErrInvalidVersionDeclaration, value)
	}
	return VersionNumber{Major: major, Minor: minor}, nil
}

func containsStringContext(ctx context.Context, value string, target byte) (bool, error) {
	for index := 0; index < len(value); index++ {
		if index%contextStringChunkSize == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if value[index] == target {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func canonicalVersionComponent(value string) bool {
	valid, _ := canonicalVersionComponentContext(context.Background(), value)
	return valid
}

func canonicalVersionComponentContext(ctx context.Context, value string) (bool, error) {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return false, ctx.Err()
	}
	for index, character := range value {
		if index%contextStringChunkSize == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if character < '0' || character > '9' {
			return false, nil
		}
	}
	return true, ctx.Err()
}

// VersionDeclarationState returns raw/present/valid declaration state from a
// root-index frontmatter mapping.
func (f Frontmatter) VersionDeclarationState() VersionDeclarationState {
	state, _ := f.VersionDeclarationStateContext(context.Background())
	return state
}

// VersionDeclarationStateContext preserves declaration shape while allowing
// cancellation during semantic resolution, scalar ownership, and parsing.
func (f Frontmatter) VersionDeclarationStateContext(ctx context.Context) (VersionDeclarationState, error) {
	node, present, err := f.SemanticGetContext(ctx, "okf_version")
	if err != nil {
		return VersionDeclarationState{}, err
	}
	if !present {
		return VersionDeclarationState{Valid: true}, nil
	}
	state, err := strictStringNodeStateContext(ctx, node, true)
	if err != nil {
		return VersionDeclarationState{}, err
	}
	if !state.Valid {
		return VersionDeclarationState{Present: true, Raw: state.Raw}, nil
	}
	if _, err := parseOKFVersionContext(ctx, state.Value); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return VersionDeclarationState{}, ctxErr
		}
		return VersionDeclarationState{Present: true, Raw: state.Value}, nil
	}
	return VersionDeclarationState{Present: true, Valid: true, Raw: state.Value, Value: state.Value}, nil
}

// ResolveVersion resolves a declaration and an optional explicit selector.
//
// selector is an assertion, not an override. Only versions implemented by this
// read layer may be selected explicitly. Unknown declarations remain visible
// while being consumed with v0.2 best-effort semantics.
func ResolveVersion(declared, selector string) (VersionResolution, error) {
	return resolveVersionContext(context.Background(), declared, selector)
}

func resolveVersionContext(ctx context.Context, declared, selector string) (VersionResolution, error) {
	if declared != "" {
		if _, err := parseOKFVersionContext(ctx, declared); err != nil {
			return VersionResolution{}, err
		}
	}
	if selector != "" {
		if _, err := parseOKFVersionContext(ctx, selector); err != nil {
			return VersionResolution{}, err
		}
	}
	legacy, err := equalStringContext(ctx, selector, LegacyOKFVersion)
	if err != nil {
		return VersionResolution{}, err
	}
	native, err := equalStringContext(ctx, selector, OKFVersion)
	if err != nil {
		return VersionResolution{}, err
	}
	if selector != "" && !legacy && !native {
		return VersionResolution{}, fmt.Errorf("%w: %q", ErrUnsupportedVersionSelector, selector)
	}
	equal, err := equalStringContext(ctx, selector, declared)
	if err != nil {
		return VersionResolution{}, err
	}
	if selector != "" && declared != "" && !equal {
		return VersionResolution{}, fmt.Errorf("%w: declared %q, selected %q", ErrVersionConflict, declared, selector)
	}
	if selector != "" {
		return resolutionForSupported(declared, selector, VersionSourceExplicit), nil
	}
	if err := ctx.Err(); err != nil {
		return VersionResolution{}, err
	}
	switch declared {
	case "":
		return resolutionForSupported("", OKFVersion, VersionSourceDefault), nil
	case LegacyOKFVersion, OKFVersion:
		return resolutionForSupported(declared, declared, VersionSourceDeclared), nil
	default:
		return VersionResolution{
			Declared:      declared,
			Effective:     OKFVersion,
			Source:        VersionSourceFutureBestEffort,
			Compatibility: VersionCompatibilityBestEffort,
		}, nil
	}
}

func resolutionForSupported(declared, effective string, source VersionSource) VersionResolution {
	compatibility := VersionCompatibilityNative
	if effective == LegacyOKFVersion {
		compatibility = VersionCompatibilityLegacy
	}
	return VersionResolution{
		Declared:      declared,
		Effective:     effective,
		Source:        source,
		Compatibility: compatibility,
	}
}

// VersionResolution resolves the root declaration with selector as an
// optional assertion. Pass an empty selector for ordinary best-effort reading.
func (b *Bundle) VersionResolution(selector string) (VersionResolution, error) {
	return b.VersionResolutionContext(context.Background(), selector)
}

// VersionResolutionContext resolves the root declaration with cancellation
// checks around retained-byte projection and document parsing.
func (b *Bundle) VersionResolutionContext(ctx context.Context, selector string) (VersionResolution, error) {
	if err := ctx.Err(); err != nil {
		return VersionResolution{}, err
	}
	document, present, err := b.rootIndexDocumentContext(ctx)
	if err != nil {
		return VersionResolution{}, err
	}
	state := VersionDeclarationState{Valid: true}
	if present {
		state, err = document.Frontmatter.VersionDeclarationStateContext(ctx)
		if err != nil {
			return VersionResolution{}, err
		}
	}
	if state.Present && !state.Valid {
		return VersionResolution{}, fmt.Errorf("%w: %q", ErrInvalidVersionDeclaration, state.Raw)
	}
	resolution, err := resolveVersionContext(ctx, state.Value, selector)
	if err != nil {
		return VersionResolution{}, err
	}
	if err := ctx.Err(); err != nil {
		return VersionResolution{}, err
	}
	return resolution, nil
}
