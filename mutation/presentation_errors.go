package mutation

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

// ErrUnsupportedPresentation means that the source uses presentation which is
// outside the lossless mutation subset. Callers must leave it untouched.
var ErrUnsupportedPresentation = errors.New("unsupported presentation")

// ErrAmbiguousPresentation means multiple semantic candidates share one edit
// target: duplicate relations/type/target/canonical fragment, or multiple raw
// ranges for one node. Parser/raw disagreement, subset rejection, and
// verification/invariant failures are ErrUnsupportedPresentation instead.
// Callers must leave the source untouched.
var ErrAmbiguousPresentation = errors.New("ambiguous presentation")

// Stable private structural-engine codes. They are intentionally strings on
// PresentationError rather than exported sentinel errors: callers branch on
// Unsupported/Ambiguous while tests and diagnostics retain exact reasons.
const (
	yamlCodeAliasProvenance     = "alias_provenance"
	yamlCodeAmbiguousSelector   = "ambiguous_selector"
	yamlCodeDuplicateTouchedKey = "duplicate_touched_key"
	yamlCodeExplicitTag         = "explicit_tag"
	// yamlCodeGraphIntegrity is the single mutation-layer classification for
	// malformed yaml.Node graphs. The underlying *bundle.YAMLIntegrityError is
	// retained so callers can inspect its precise integrity code with errors.As.
	yamlCodeGraphIntegrity      = "graph_integrity"
	yamlCodeMissingMappingEntry = "missing_mapping_entry"
	yamlCodeMissingSequenceItem = "missing_sequence_item"
	yamlCodeOverlappingPatch    = "overlapping_structural_patch"
	// yamlCodeResourceLimit is the stable mutation-layer classification for all
	// public YAML graph budgets. Kind, Observed, and Limit remain available on
	// the wrapped *bundle.YAMLResourceLimitError.
	yamlCodeResourceLimit         = "resource_limit"
	yamlCodeSelectorMismatch      = "selector_desired_mismatch"
	yamlCodeTouchedAnchor         = "touched_anchor"
	yamlCodeUnownedComment        = "unowned_comment"
	yamlCodeUnsupportedCollection = "unsupported_collection"
	yamlCodeUnsupportedKey        = "unsupported_key"
	yamlCodeUnsupportedSelector   = "unsupported_selector"
)

// PresentationError describes a fail-closed presentation mutation failure.
// Format is intentionally shared by Markdown and YAML resolvers. Errors
// returned by Planner always include Code, Format, Path, Operation, and a
// Location in full original-file byte offsets.
type PresentationError struct {
	// Code is a stable machine-readable failure classification.
	Code string
	// Format identifies the presentation parser, currently "markdown" or "yaml".
	Format string
	// Path is the affected bundle path.
	Path string
	// Operation identifies the semantic mutation being planned.
	Operation string
	// Location is the relevant source byte span in the full original file.
	Location SourceSpan
	// Err is ErrUnsupportedPresentation, ErrAmbiguousPresentation, or a cause.
	// Use errors.Is or errors.As rather than matching Error text.
	Err error

	// yamlRelative is internal resolver provenance. YAML resolver spans address
	// the frontmatter slice; presentation patch spans already address the full
	// file. The planner uses this bit to expose one public convention.
	yamlRelative bool
}

func (e *PresentationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s presentation %s: %v", e.Format, e.Code, e.Err)
}

func (e *PresentationError) Unwrap() error { return e.Err }

func markdownPresentationErrorAt(code string, err error, span SourceSpan) error {
	return &PresentationError{Code: code, Format: "markdown", Location: span, Err: err}
}

func invalidUTF8PresentationErrorAt(format string, span SourceSpan) error {
	err := fmt.Errorf("%w: %w", ErrUnsupportedPresentation, bundle.ErrInvalidEncoding)
	return &PresentationError{Code: "invalid_encoding", Format: format, Location: span, Err: err}
}

func validateUTF8Context(ctx context.Context, source []byte) (SourceSpan, bool, error) {
	for at := 0; at < len(source); {
		if at&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return SourceSpan{}, false, err
			}
		}
		_, size := utf8.DecodeRune(source[at:])
		if size == 1 && source[at] >= utf8.RuneSelf {
			return SourceSpan{Start: at, End: at + 1}, false, nil
		}
		at += size
	}
	return SourceSpan{}, true, ctx.Err()
}

// shiftMarkdownPresentationLocation remaps a body-relative Markdown Location to
// full-file offsets. Collectors parse Goldmark against source[bodyStart:];
// PresentationError.Location is documented as original-file byte offsets.
func shiftMarkdownPresentationLocation(err error, bodyStart int) error {
	if err == nil || bodyStart == 0 {
		return err
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Format != "markdown" || presentation.Location == (SourceSpan{}) {
		return err
	}
	copy := *presentation
	copy.Location.Start += bodyStart
	copy.Location.End += bodyStart
	return &copy
}

func yamlLocalPresentationError(code string, err error, span SourceSpan) error {
	return &PresentationError{Code: code, Format: "yaml", Location: span, Err: err, yamlRelative: true}
}

// concreteYAMLDiagnosticSpan turns a parser coordinate or ownership boundary
// into a concrete diagnostic byte. Zero-width spans remain valid for internal
// insertion patches, but must never escape as public rejection locations.
//
// An empty local slice has no owned byte. It stays absent here and is mapped by
// the public planner boundary to a concrete byte in the complete source file;
// inventing [0,1) would be outside the local slice.
func concreteYAMLDiagnosticSpan(source []byte, span SourceSpan) SourceSpan {
	if len(source) == 0 {
		return SourceSpan{}
	}
	start := min(max(span.Start, 0), len(source))
	end := min(max(span.End, 0), len(source))
	if start < end {
		return SourceSpan{Start: start, End: end}
	}
	if start < len(source) {
		return SourceSpan{Start: start, End: start + 1}
	}
	return SourceSpan{Start: len(source) - 1, End: len(source)}
}

func yamlLocalPresentationErrorForSource(code string, err error, source []byte, span SourceSpan) error {
	return yamlLocalPresentationError(code, err, concreteYAMLDiagnosticSpan(source, span))
}

// validateMutationYAMLGraphContext is the only mutation-owned yaml.Node graph
// boundary. Recursive consumers call it before walking a decoded or caller-
// supplied graph instead of growing a second, subtly different traversal.
func validateMutationYAMLGraphContext(ctx context.Context, node *yaml.Node) error {
	return bundle.ValidateYAMLNodeContext(ctx, node)
}

// mutationYAMLGraphValidationCause translates the bundle graph contract into
// the stable presentation contract without erasing the typed bundle cause.
func mutationYAMLGraphValidationCause(err error) (string, error, bool) {
	if err == nil {
		return "", nil, false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "", err, false
	}
	switch {
	case errors.Is(err, bundle.ErrYAMLResourceLimit):
		return yamlCodeResourceLimit, fmt.Errorf("%w: %w", ErrUnsupportedPresentation, err), true
	case errors.Is(err, bundle.ErrInvalidYAMLGraph):
		return yamlCodeGraphIntegrity, fmt.Errorf("%w: %w", ErrUnsupportedPresentation, err), true
	default:
		// ValidateYAMLNodeContext currently returns only cancellation, resource,
		// and integrity errors. Preserve fail-closed behavior if that contract is
		// ever extended, while retaining the new typed cause.
		return yamlCodeGraphIntegrity, fmt.Errorf("%w: %w", ErrUnsupportedPresentation, err), true
	}
}

func validateMutationYAMLGraphForSourceContext(
	ctx context.Context,
	node *yaml.Node,
	source []byte,
	span SourceSpan,
) error {
	err := validateMutationYAMLGraphContext(ctx, node)
	return mutationYAMLGraphErrorForSource(err, source, span)
}

func mutationYAMLGraphErrorForSource(err error, source []byte, span SourceSpan) error {
	code, cause, mapped := mutationYAMLGraphValidationCause(err)
	if !mapped {
		return cause
	}
	return yamlLocalPresentationErrorForSource(code, cause, source, span)
}

// preserveTraversalError is the single mutation boundary for errors emitted
// by recursive render/identity/merge traversals. Control-flow and already
// located errors retain identity; typed graph failures gain a stable mutation
// classification without hiding their bundle error; ordinary failures keep
// the caller's established fallback contract.
func (p *presentation) preserveTraversalError(
	err error,
	fallbackCode string,
	fallbackCause error,
	owners ...*yaml.Node,
) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var presentation *PresentationError
	if errors.As(err, &presentation) {
		return err
	}
	var resource *bundle.YAMLResourceLimitError
	if errors.As(err, &resource) {
		return p.yamlNodeError(yamlCodeResourceLimit, errors.Join(ErrUnsupportedPresentation, err), owners...)
	}
	var integrity *bundle.YAMLIntegrityError
	if errors.As(err, &integrity) {
		return p.yamlNodeError(yamlCodeGraphIntegrity, errors.Join(ErrUnsupportedPresentation, err), owners...)
	}
	return p.yamlNodeError(fallbackCode, fallbackCause, owners...)
}
