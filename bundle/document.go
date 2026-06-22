package bundle

import (
	"fmt"
	"strings"
)

const frontmatterDelimiter = "---"

// Document is one OKF concept document: YAML frontmatter plus Markdown body.
type Document struct {
	Frontmatter Frontmatter
	Body        string
	// HasFrontmatter reports whether the document text had a YAML frontmatter
	// block delimited by --- at the beginning of the file.
	HasFrontmatter bool
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
func ParseDocument(text string) (Document, error) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterDelimiter {
		return Document{Frontmatter: NewFrontmatter(), Body: text}, nil
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == frontmatterDelimiter {
			end = i
			break
		}
	}
	if end == -1 {
		return Document{}, ErrUnterminatedFrontmatter
	}

	frontmatter, err := ParseFrontmatter(strings.Join(lines[1:end], "\n"))
	if err != nil {
		return Document{}, err
	}

	body := strings.Join(lines[end+1:], "\n")
	body = strings.TrimPrefix(body, "\n")
	body = trimSplitTrailingNewline(body)

	return NewDocument(frontmatter, body), nil
}

// Serialize renders the document as frontmatter delimited by "---" followed by
// a blank line and a newline-terminated Markdown body.
func (d Document) Serialize() (string, error) {
	frontmatter, err := d.Frontmatter.YAMLString()
	if err != nil {
		return "", err
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

// Citations extracts numbered entries from the document body's Citations section.
func (d Document) Citations() []Citation {
	return ExtractCitations(d.Body)
}

// ValidateConformance checks the hard OKF v0.1 document requirement: non-empty string type.
func (d Document) ValidateConformance() error {
	if _, ok := d.Frontmatter.Type(); !ok {
		return fmt.Errorf("%w: type", ErrMissingFrontmatterKeys)
	}
	return nil
}

func trimSplitTrailingNewline(text string) string {
	return strings.TrimSuffix(text, "\n")
}
