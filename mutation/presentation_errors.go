package mutation

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
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
