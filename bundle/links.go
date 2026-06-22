package bundle

import (
	"strconv"
	"strings"
	"unicode"
)

// LinkKind describes how an OKF Markdown link target should be interpreted.
type LinkKind int

const (
	// LinkOther is an empty or otherwise unclassified target.
	LinkOther LinkKind = iota
	// LinkAbsolute is a bundle-root-relative internal target beginning with "/".
	LinkAbsolute
	// LinkRelative is a relative internal target.
	LinkRelative
	// LinkExternal is an external URI or protocol-relative URL.
	LinkExternal
	// LinkAnchor is an in-document anchor target beginning with "#".
	LinkAnchor
)

// String returns a stable display name for the link kind.
func (kind LinkKind) String() string {
	switch kind {
	case LinkAbsolute:
		return "absolute"
	case LinkRelative:
		return "relative"
	case LinkExternal:
		return "external"
	case LinkAnchor:
		return "anchor"
	default:
		return "other"
	}
}

// Link is one inline Markdown link found in a concept body.
type Link struct {
	Text   string
	Target string
	Kind   LinkKind
}

// ClassifyLink classifies a raw Markdown link target.
func ClassifyLink(target string) LinkKind {
	trimmed := strings.TrimSpace(target)
	switch {
	case trimmed == "":
		return LinkOther
	case strings.HasPrefix(trimmed, "#"):
		return LinkAnchor
	case isExternalLink(trimmed):
		return LinkExternal
	case strings.HasPrefix(trimmed, "/"):
		return LinkAbsolute
	default:
		return LinkRelative
	}
}

// Resolve resolves an internal link target from the source concept id.
//
// External links, anchors, directory links, and invalid concept ids return false.
func (link Link) Resolve(source ConceptID) (ConceptID, bool) {
	switch link.Kind {
	case LinkAbsolute:
		return resolveAbsoluteLink(link.Target)
	case LinkRelative:
		return resolveRelativeLink(link.Target, source)
	default:
		return ConceptID{}, false
	}
}

// Citation is a numbered entry under a "# Citations" heading.
type Citation struct {
	Number int
	Text   string
	Target string
	Raw    string
}

// ExtractLinks extracts inline Markdown links from body text.
//
// Links inside fenced code blocks and inline code spans are ignored.
func ExtractLinks(body string) []Link {
	var links []Link
	for _, line := range codeFreeLines(body) {
		scanLineLinks(line, &links)
	}
	return links
}

// ExtractCitations extracts numbered citation entries from the "# Citations" section.
func ExtractCitations(body string) []Citation {
	var citations []Citation
	inSection := false

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if inSection {
				break
			}
			inSection = strings.EqualFold(title, "citations")
			continue
		}

		if !inSection || trimmed == "" {
			continue
		}
		if citation, ok := parseCitationLine(trimmed); ok {
			citations = append(citations, citation)
		}
	}

	return citations
}

func isExternalLink(target string) bool {
	lower := strings.ToLower(target)
	return strings.HasPrefix(lower, "//") ||
		strings.Contains(lower, "://") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "tel:") ||
		strings.HasPrefix(lower, "data:")
}

func resolveAbsoluteLink(target string) (ConceptID, bool) {
	trimmed := stripAnchor(target)
	if strings.HasSuffix(trimmed, "/") {
		return ConceptID{}, false
	}
	segments := normalizeConceptPathSegments(strings.Split(strings.TrimLeft(trimmed, "/"), "/"), nil)
	return conceptIDFromNormalizedSegments(segments)
}

func resolveRelativeLink(target string, source ConceptID) (ConceptID, bool) {
	trimmed := stripAnchor(target)
	if trimmed == "" || strings.HasSuffix(trimmed, "/") {
		return ConceptID{}, false
	}
	var base []string
	if parent, ok := source.Parent(); ok {
		base = parent.Segments()
	}
	segments := normalizeConceptPathSegments(strings.Split(trimmed, "/"), base)
	return conceptIDFromNormalizedSegments(segments)
}

func stripAnchor(target string) string {
	if i := strings.Index(target, "#"); i >= 0 {
		return target[:i]
	}
	return target
}

func normalizeConceptPathSegments(parts []string, base []string) []string {
	segments := append([]string(nil), base...)
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, part)
		}
	}
	return segments
}

func conceptIDFromNormalizedSegments(segments []string) (ConceptID, bool) {
	if len(segments) == 0 {
		return ConceptID{}, false
	}
	segments = append([]string(nil), segments...)
	segments[len(segments)-1] = strings.TrimSuffix(segments[len(segments)-1], ".md")
	id, err := NewConceptID(segments)
	if err != nil {
		return ConceptID{}, false
	}
	return id, true
}

func codeFreeLines(body string) []string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	var fence rune

	for _, line := range lines {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		if fence != 0 {
			if strings.HasPrefix(trimmed, strings.Repeat(string(fence), 3)) {
				fence = 0
			}
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "```"):
			fence = '`'
			continue
		case strings.HasPrefix(trimmed, "~~~"):
			fence = '~'
			continue
		default:
			out = append(out, blankInlineCode(line))
		}
	}

	return out
}

func blankInlineCode(line string) string {
	var builder strings.Builder
	builder.Grow(len(line))

	inCode := false
	for _, r := range line {
		if r == '`' {
			inCode = !inCode
			builder.WriteRune(' ')
			continue
		}
		if inCode {
			builder.WriteRune(' ')
			continue
		}
		builder.WriteRune(r)
	}

	return builder.String()
}

func scanLineLinks(line string, out *[]Link) {
	runes := []rune(line)
	for i := 0; i < len(runes); {
		if runes[i] == '[' {
			text, dest, next, ok := parseInlineLink(runes, i)
			if ok {
				target := stripLinkTitle(dest)
				*out = append(*out, Link{
					Text:   text,
					Target: target,
					Kind:   ClassifyLink(target),
				})
				i = next
				continue
			}
		}
		i++
	}
}

func parseInlineLink(runes []rune, start int) (text string, dest string, next int, ok bool) {
	i := start + 1
	depth := 1
	textStart := i
	for i < len(runes) {
		switch runes[i] {
		case '\\':
			i++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				goto textDone
			}
		}
		i++
	}
textDone:
	if depth != 0 || i >= len(runes) {
		return "", "", 0, false
	}

	text = string(runes[textStart:i])
	j := i + 1
	if j >= len(runes) || runes[j] != '(' {
		return "", "", 0, false
	}

	j++
	destStart := j
	paren := 1
	for j < len(runes) {
		switch runes[j] {
		case '\\':
			j++
		case '(':
			paren++
		case ')':
			paren--
			if paren == 0 {
				goto destDone
			}
		}
		j++
	}
destDone:
	if paren != 0 || j >= len(runes) {
		return "", "", 0, false
	}

	return text, string(runes[destStart:j]), j + 1, true
}

func stripLinkTitle(dest string) string {
	trimmed := strings.TrimSpace(dest)
	for i, r := range trimmed {
		if r == ' ' || r == '\t' {
			rest := strings.TrimLeft(trimmed[i:], " \t")
			if strings.HasPrefix(rest, "\"") || strings.HasPrefix(rest, "'") {
				return trimmed[:i]
			}
			return trimmed
		}
	}
	return trimmed
}

func parseCitationLine(line string) (Citation, bool) {
	rest, ok := strings.CutPrefix(line, "[")
	if !ok {
		return Citation{}, false
	}

	close := strings.Index(rest, "]")
	if close < 0 {
		return Citation{}, false
	}

	number, err := strconv.Atoi(strings.TrimSpace(rest[:close]))
	if err != nil {
		return Citation{}, false
	}

	raw := strings.TrimSpace(rest[close+1:])
	citation := Citation{
		Number: number,
		Raw:    raw,
	}

	runes := []rune(raw)
	for i, r := range runes {
		if r == '[' {
			text, dest, _, ok := parseInlineLink(runes, i)
			if ok {
				citation.Text = text
				citation.Target = stripLinkTitle(dest)
			}
			break
		}
	}

	return citation, true
}
