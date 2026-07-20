package bundle

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ConceptID identifies a concept by its bundle-relative path without ".md".
type ConceptID struct {
	segments []string
}

// NewConceptID creates a concept identifier from bundle-relative path segments.
func NewConceptID(segments []string) (ConceptID, error) {
	if len(segments) == 0 {
		return ConceptID{}, fmt.Errorf("%w: concept id must have at least one segment", ErrInvalidConceptID)
	}

	copied := make([]string, len(segments))
	for i, segment := range segments {
		if err := ValidateConceptSegment(segment); err != nil {
			return ConceptID{}, err
		}
		copied[i] = segment
	}

	return ConceptID{segments: copied}, nil
}

// ParseConceptID parses a slash-separated concept identifier.
//
// Empty path segments are ignored so leading, trailing, and duplicate slashes
// are tolerated.
func ParseConceptID(raw string) (ConceptID, error) {
	parts := strings.Split(raw, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			segments = append(segments, part)
		}
	}
	if len(segments) == 0 {
		return ConceptID{}, fmt.Errorf("%w: empty concept id %q", ErrInvalidConceptID, raw)
	}
	return NewConceptID(segments)
}

// Segments returns a copy of the concept id segments.
func (id ConceptID) Segments() []string {
	return append([]string(nil), id.segments...)
}

// Name returns the final concept id segment.
func (id ConceptID) Name() string {
	if len(id.segments) == 0 {
		return ""
	}
	return id.segments[len(id.segments)-1]
}

// Parent returns the concept id of the containing directory.
func (id ConceptID) Parent() (ConceptID, bool) {
	if len(id.segments) <= 1 {
		return ConceptID{}, false
	}
	parent := append([]string(nil), id.segments[:len(id.segments)-1]...)
	return ConceptID{segments: parent}, true
}

// String returns the slash-separated concept id.
func (id ConceptID) String() string {
	return strings.Join(id.segments, "/")
}

// ValidateConceptID validates a constructed concept identifier for use in a
// public semantic contract. It is deliberately separate from ParseConceptID:
// the latter accepts cosmetic extra slashes for loader compatibility.
func ValidateConceptID(id ConceptID) error {
	if len(id.segments) == 0 {
		return fmt.Errorf("%w: empty concept id", ErrInvalidConceptID)
	}
	for _, segment := range id.segments {
		if err := ValidateConceptSegment(segment); err != nil {
			return err
		}
	}
	return nil
}

// ToPath resolves the concept id to a Markdown file path under bundleRoot.
func (id ConceptID) ToPath(bundleRoot string) string {
	if len(id.segments) == 0 {
		return bundleRoot
	}
	parts := make([]string, 0, len(id.segments)+1)
	if bundleRoot != "" {
		parts = append(parts, bundleRoot)
	}
	parts = append(parts, id.segments[:len(id.segments)-1]...)
	parts = append(parts, id.Name()+".md")
	return filepath.Join(parts...)
}

// ConceptIDFromPath derives a concept id from a Markdown file path under bundleRoot.
func ConceptIDFromPath(bundleRoot, filePath string) (ConceptID, error) {
	rel, _ := filepath.Rel(bundleRoot, filePath)
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return ConceptID{}, fmt.Errorf("%w: %s is not under bundle root %s", ErrInvalidConceptID, filePath, bundleRoot)
	}

	rel = filepath.ToSlash(rel)
	segments := strings.Split(rel, "/")
	last := segments[len(segments)-1]
	segments[len(segments)-1] = strings.TrimSuffix(last, ".md")
	return NewConceptID(segments)
}

// ValidateConceptSegment validates one concept id path segment.
//
// OKF v0.1 does not impose a filename character grammar. Validation rejects
// path semantics and control characters that cannot safely identify a source
// path. Ordinary punctuation and Unicode remain valid.
func ValidateConceptSegment(segment string) error {
	if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "\\/") {
		return fmt.Errorf("%w: invalid segment %q", ErrInvalidConceptID, segment)
	}
	if !utf8.ValidString(segment) {
		return fmt.Errorf("%w: invalid segment %q", ErrInvalidConceptID, segment)
	}
	for _, r := range segment {
		if r <= 0x1F || r == 0x7F {
			return fmt.Errorf("%w: invalid segment %q", ErrInvalidConceptID, segment)
		}
	}
	return nil
}
