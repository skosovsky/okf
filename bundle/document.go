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

// ParseDocumentForVersionContext applies the selected document contract.
func ParseDocumentForVersionContext(ctx context.Context, text string, version VersionResolution) (Document, error) {
	if version.Effective != OKFVersion03 {
		return ParseDocumentContext(ctx, text)
	}
	return parseDocumentContext(ctx, text, version)
}

// ParseDocumentContext is the cancellation-aware form of ParseDocument.
func ParseDocumentContext(ctx context.Context, text string) (Document, error) {
	return parseDocumentContext(ctx, text, VersionResolution{})
}

// The declaration is in the document being read. Probe the 0.3 decoder only
// when its YAML directive is needed and the declaration or selector accepts it.
func parseRootIndexVersionProbeContext(ctx context.Context, text, selector string) (Document, error) {
	doc, err := ParseDocumentContext(ctx, text)
	if err == nil || !strings.Contains(text, "%") || ctx.Err() != nil {
		return doc, err
	}
	current, currentErr := ParseDocumentForVersionContext(ctx, text, VersionResolution{Effective: OKFVersion03})
	if currentErr != nil {
		if ctx.Err() != nil || errors.Is(currentErr, ErrYAMLResourceLimit) || errors.Is(currentErr, ErrInvalidYAMLGraph) {
			return Document{}, currentErr
		}
		return doc, err
	}
	declaration, stateErr := current.Frontmatter.VersionDeclarationStateContext(ctx)
	if stateErr != nil {
		return Document{}, stateErr
	}
	if !declaration.Valid {
		return doc, err
	}
	if declaration.Present {
		selector = ""
	}
	version, resolveErr := resolveVersionContext(ctx, declaration.Value, selector)
	if resolveErr == nil && version.Effective == OKFVersion03 {
		return current, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Document{}, ctxErr
	}
	return doc, err
}

// ParseBundleIndexContext resolves the declaration in the bundle's root index
// and reads it using the resulting version contract.
func ParseBundleIndexContext(ctx context.Context, text, selector string) (Document, VersionResolution, error) {
	doc, err := parseRootIndexVersionProbeContext(ctx, text, selector)
	if err != nil {
		return Document{}, VersionResolution{}, err
	}
	declaration, err := doc.Frontmatter.rootVersionDeclarationStateContext(ctx, selector)
	if err != nil {
		return Document{}, VersionResolution{}, err
	}
	if declaration.Present && !declaration.Valid {
		return Document{}, VersionResolution{}, invalidVersionDeclarationError(declaration)
	}
	version, err := resolveVersionContext(ctx, declaration.Value, selector)
	if err != nil {
		return Document{}, VersionResolution{}, err
	}
	if version.Effective == OKFVersion03 && !doc.Frontmatter.coreV03 {
		doc, err = ParseDocumentForVersionContext(ctx, text, version)
	}
	return doc, version, err
}

func parseDocumentContext(ctx context.Context, text string, version VersionResolution) (Document, error) {
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
	frontmatter, err := ParseFrontmatterForVersionContext(ctx, frontmatterText, version)
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
	return d.serialize(d.Frontmatter.coreV03)
}

func (d Document) serialize(coreV03 bool) (string, error) {
	if !utf8.ValidString(d.Body) {
		return "", fmt.Errorf("%w: invalid UTF-8", ErrInvalidEncoding)
	}

	frontmatter, err := d.Frontmatter.yamlStringContext(context.Background(), coreV03)
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

// SerializeForVersion preserves the body of a plain OKF 0.3 Markdown file
// without introducing an empty YAML frontmatter block.
func (d Document) SerializeForVersion(version VersionResolution) (string, error) {
	if version.Effective != OKFVersion03 {
		return d.Serialize()
	}
	if d.HasFrontmatter {
		return d.serialize(true)
	}
	if !utf8.ValidString(d.Body) {
		return "", fmt.Errorf("%w: invalid UTF-8", ErrInvalidEncoding)
	}
	return d.Body, nil
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
