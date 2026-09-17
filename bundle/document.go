package bundle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/documentlayout"
)

const frontmatterDelimiter = "---"

// Document is one OKF concept document: YAML frontmatter plus Markdown body.
type Document struct {
	Frontmatter Frontmatter
	Body        string
	// HasFrontmatter reports whether the document text had a YAML frontmatter
	// block delimited by --- at the beginning of the file.
	HasFrontmatter  bool
	frontmatterYAML []byte
}

// NewDocument creates a document from frontmatter and Markdown body.
func NewDocument(frontmatter Frontmatter, body string) Document {
	return Document{
		Frontmatter:    frontmatter,
		Body:           body,
		HasFrontmatter: true,
	}
}

// ParseDocument parses an OKF Markdown document with optional YAML frontmatter.
//
// If the text does not start with a frontmatter delimiter, the entire input is
// treated as the body and the frontmatter is empty.
//
// Frontmatter bytes between the opening and closing --- lines are passed to
// yaml.v3 unchanged, including the newline that precedes the closing delimiter.
// Split/Join must not reconstruct that slice: literal and folded scalars treat
// a missing final newline as a different semantic Value.
func ParseDocument(text string) (Document, error) {
	return ParseDocumentContext(context.Background(), text)
}

// ParseDocumentContext is the cancellation-aware form of ParseDocument.
func ParseDocumentContext(ctx context.Context, text string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	// strings and yaml.v3 can otherwise accept malformed input after replacing
	// invalid byte sequences with U+FFFD. The source bytes are part of the OKF
	// document contract, so reject them before inspecting delimiters or YAML.
	valid, err := validUTF8StringContext(ctx, text)
	if err != nil {
		return Document{}, err
	}
	if !valid {
		return Document{}, fmt.Errorf("%w: invalid UTF-8", ErrInvalidEncoding)
	}

	data, err := bytesFromStringContext(ctx, text)
	if err != nil {
		return Document{}, err
	}
	layout, ok, err := documentlayout.SplitContext(ctx, data)
	if errors.Is(err, documentlayout.ErrUnterminatedFrontmatter) {
		return Document{}, ErrUnterminatedFrontmatter
	}
	if err != nil {
		return Document{}, err
	}
	if !ok {
		if err := ctx.Err(); err != nil {
			return Document{}, err
		}
		return Document{Frontmatter: NewFrontmatter(), Body: text}, nil
	}

	frontmatterText, err := stringFromBytesContext(ctx, data[layout.YAMLStart:layout.YAMLEnd])
	if err != nil {
		return Document{}, err
	}
	frontmatter, err := ParseFrontmatterContext(ctx, frontmatterText)
	if err != nil {
		return Document{}, err
	}

	body, err := stringFromBytesContext(ctx, data[layout.BodyStart:])
	if err != nil {
		return Document{}, err
	}
	switch {
	case strings.HasPrefix(body, "\r\n"):
		body = body[len("\r\n"):]
	case strings.HasPrefix(body, "\n"):
		body = body[len("\n"):]
	}
	switch {
	case strings.HasSuffix(body, "\r\n"):
		body = strings.TrimSuffix(body, "\r\n")
	case strings.HasSuffix(body, "\n"):
		body = strings.TrimSuffix(body, "\n")
	}

	document := NewDocument(frontmatter, body)
	document.frontmatterYAML, err = copySourceBytesContext(ctx, data[layout.YAMLStart:layout.YAMLEnd])
	if err != nil {
		return Document{}, err
	}
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	return document, nil
}

// Serialize renders the document as frontmatter delimited by "---" followed by
// a blank line and a newline-terminated Markdown body.
func (d Document) Serialize() (string, error) {
	if !utf8.ValidString(d.Body) {
		return "", fmt.Errorf("%w: invalid UTF-8", ErrInvalidEncoding)
	}

	frontmatter, err := d.Frontmatter.YAMLString()
	if err != nil {
		return "", err
	}
	if frontmatter != "" && !strings.HasSuffix(frontmatter, "\n") && !strings.HasSuffix(frontmatter, "\r") {
		frontmatter += "\n"
	}

	body := d.Body
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}

	return fmt.Sprintf("%s\n%s%s\n\n%s", frontmatterDelimiter, frontmatter, frontmatterDelimiter, body), nil
}

// Links extracts Markdown links from the document body.
func (d Document) Links() []Link {
	return ExtractLinks(d.Body)
}

// LinksContext is the cancellation-aware form of Links.
func (d Document) LinksContext(ctx context.Context) ([]Link, error) {
	return ExtractLinksContext(ctx, d.Body)
}

// Citations extracts numbered entries from the document body's Citations section.
func (d Document) Citations() []Citation {
	if !d.LegacyFallbackObservation().CitationsActive {
		return nil
	}
	return ExtractCitations(d.Body)
}

// FrontmatterYAML returns an owned, byte-exact copy of the YAML bytes captured
// between the delimiters by ParseDocument. It preserves comments, style, key
// order, and line endings. Documents built with NewDocument return nil.
func (d Document) FrontmatterYAML() []byte {
	return append([]byte(nil), d.frontmatterYAML...)
}

// ValidateConformance checks the hard OKF document requirement: non-empty string type.
func (d Document) ValidateConformance() error {
	if _, ok := d.Frontmatter.Type(); !ok {
		return fmt.Errorf("%w: type", ErrMissingFrontmatterKeys)
	}
	return nil
}
