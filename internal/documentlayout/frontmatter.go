// Package documentlayout owns the byte-level outer Markdown/frontmatter
// boundary shared by bundle parsing and lossless mutations.
package documentlayout

import (
	"bytes"
	"errors"
)

// ErrUnterminatedFrontmatter reports an exact opening delimiter without an
// exact closing delimiter.
var ErrUnterminatedFrontmatter = errors.New("unterminated frontmatter")

// Frontmatter identifies half-open byte boundaries in an OKF document.
type Frontmatter struct {
	YAMLStart int
	YAMLEnd   int
	BodyStart int
}

// Split recognizes only an exact "---" physical line, with either LF, CRLF,
// or EOF termination. Leading/trailing whitespace is content, not a delimiter.
// This keeps the loader, YAML mutation path, and Markdown body parser on one
// byte boundary contract.
func Split(data []byte) (Frontmatter, bool, error) {
	first := lineEnd(data, 0)
	if !isDelimiterLine(data[:first]) {
		return Frontmatter{}, false, nil
	}
	for at := first; at < len(data); {
		next := lineEnd(data, at)
		if isDelimiterLine(data[at:next]) {
			return Frontmatter{YAMLStart: first, YAMLEnd: at, BodyStart: next}, true, nil
		}
		at = next
	}
	return Frontmatter{YAMLStart: first, YAMLEnd: len(data), BodyStart: len(data)}, true, ErrUnterminatedFrontmatter
}

// HasOpeningDelimiter reports whether data begins with the exact delimiter
// recognized by Split. It does not require a closing delimiter.
func HasOpeningDelimiter(data []byte) bool {
	return isDelimiterLine(data[:lineEnd(data, 0)])
}

func lineEnd(data []byte, at int) int {
	if at < 0 || at >= len(data) {
		return len(data)
	}
	if n := bytes.IndexByte(data[at:], '\n'); n >= 0 {
		return at + n + 1
	}
	return len(data)
}

func isDelimiterLine(line []byte) bool {
	if bytes.HasSuffix(line, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\n"))
		line = bytes.TrimSuffix(line, []byte("\r"))
	}
	return bytes.Equal(line, []byte("---"))
}
