package bundle

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/markdownowner"
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
	kind, _ := ClassifyLinkContext(context.Background(), target)
	return kind
}

// ClassifyLinkContext is the cancellation-aware form of ClassifyLink.
func ClassifyLinkContext(ctx context.Context, target string) (LinkKind, error) {
	if err := ctx.Err(); err != nil {
		return LinkOther, err
	}
	owned, err := stringFromStringContext(ctx, target)
	if err != nil {
		return LinkOther, err
	}
	target = owned
	trimmed, err := trimSpaceStringContext(ctx, target)
	if err != nil {
		return LinkOther, err
	}
	switch {
	case trimmed == "":
		return LinkOther, nil
	case strings.HasPrefix(trimmed, "#"):
		return LinkAnchor, nil
	case isExternalLinkContext(ctx, trimmed):
		if err := ctx.Err(); err != nil {
			return LinkOther, err
		}
		return LinkExternal, ctx.Err()
	case strings.HasPrefix(trimmed, "/"):
		return LinkAbsolute, nil
	default:
		return LinkRelative, nil
	}
}

// Resolve resolves an internal link target from the source concept id.
//
// External links, anchors, directory links, and invalid concept ids return false.
func (link Link) Resolve(source ConceptID) (ConceptID, bool) {
	id, ok, _ := link.ResolveContext(context.Background(), source)
	return id, ok
}

// ResolveContext is the cancellation-aware form of Resolve.
func (link Link) ResolveContext(ctx context.Context, source ConceptID) (ConceptID, bool, error) {
	if err := ctx.Err(); err != nil {
		return ConceptID{}, false, err
	}
	target, err := stringFromStringContext(ctx, link.Target)
	if err != nil {
		return ConceptID{}, false, err
	}
	switch link.Kind {
	case LinkAbsolute:
		return resolveLinkContext(ctx, target, nil, true)
	case LinkRelative:
		base, err := cloneConceptIDContext(ctx, source)
		if err != nil {
			return ConceptID{}, false, err
		}
		if len(base.segments) > 0 {
			base.segments = base.segments[:len(base.segments)-1]
		}
		return resolveLinkContext(ctx, target, base.segments, false)
	default:
		return ConceptID{}, false, ctx.Err()
	}
}

func resolveLinkContext(ctx context.Context, target string, base []string, absolute bool) (ConceptID, bool, error) {
	end := len(target)
	for index := 0; index < len(target); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return ConceptID{}, false, err
			}
		}
		if target[index] == '?' || target[index] == '#' {
			end = index
			break
		}
	}
	target = target[:end]
	if target == "" || strings.HasSuffix(target, "/") {
		return ConceptID{}, false, ctx.Err()
	}
	start := 0
	if absolute {
		for start < len(target) && target[start] == '/' {
			start++
		}
	}
	segments := make([]string, 0, len(base)+4)
	for _, segment := range base {
		owned, err := stringFromStringContext(ctx, segment)
		if err != nil {
			return ConceptID{}, false, err
		}
		segments = append(segments, owned)
	}
	partStart := start
	for index := start; index <= len(target); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return ConceptID{}, false, err
			}
		}
		if index != len(target) && target[index] != '/' {
			continue
		}
		part := target[partStart:index]
		switch part {
		case "", ".":
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			owned, err := stringFromStringContext(ctx, part)
			if err != nil {
				return ConceptID{}, false, err
			}
			segments = append(segments, owned)
		}
		partStart = index + 1
	}
	if len(segments) == 0 {
		return ConceptID{}, false, ctx.Err()
	}
	last := segments[len(segments)-1]
	if strings.HasSuffix(last, ".md") {
		last = last[:len(last)-3]
	}
	segments[len(segments)-1] = last
	for _, segment := range segments {
		if err := validateConceptSegmentContext(ctx, segment); err != nil {
			if ctx.Err() != nil {
				return ConceptID{}, false, ctx.Err()
			}
			return ConceptID{}, false, nil
		}
	}
	return ConceptID{segments: segments}, true, ctx.Err()
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
	links, _ := ExtractLinksContext(context.Background(), body)
	return links
}

// ExtractLinksContext is the cancellation-aware form of ExtractLinks.
func ExtractLinksContext(ctx context.Context, body string) ([]Link, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owned, err := stringFromStringContext(ctx, body)
	if err != nil {
		return nil, err
	}
	var links []Link
	lines, err := markdownowner.ProseLinesContext(ctx, owned)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := scanLineLinksContext(ctx, line, &links); err != nil {
			return nil, err
		}
	}
	return links, ctx.Err()
}

// ExtractCitations extracts numbered citation entries from the "# Citations" section.
func ExtractCitations(body string) []Citation {
	projection, err := markdownowner.CollectCitationSectionProjection(context.Background(), []byte(body))
	if err != nil || !projection.Found {
		return nil
	}
	out := make([]Citation, 0, len(projection.Entries))
	for _, entry := range projection.Entries {
		number := entry.Number
		if number == 0 {
			number = uint64(entry.Ordinal)
		}
		if number > uint64(^uint(0)>>1) {
			continue
		}
		out = append(out, Citation{
			Number: int(number),
			Text:   entry.Title,
			Target: entry.Resource,
			Raw:    entry.Raw,
		})
	}
	return out
}

func isExternalLink(target string) bool {
	lower := strings.ToLower(target)
	return strings.HasPrefix(lower, "//") ||
		strings.Contains(lower, "://") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "tel:") ||
		strings.HasPrefix(lower, "data:")
}

func isExternalLinkContext(ctx context.Context, target string) bool {
	if ctx.Err() != nil {
		return false
	}
	const chunk = 64 << 10
	for offset := 0; offset < len(target); offset += chunk {
		if ctx.Err() != nil {
			return false
		}
		end := min(offset+chunk, len(target))
		for index := offset; index+2 < end; index++ {
			if target[index:index+3] == "://" {
				return true
			}
		}
	}
	return asciiFoldPrefix(target, "//") || asciiFoldPrefix(target, "mailto:") || asciiFoldPrefix(target, "tel:") || asciiFoldPrefix(target, "data:")
}

func asciiFoldPrefix(value, prefix string) bool {
	if len(value) < len(prefix) {
		return false
	}
	for index := range prefix {
		left, right := value[index], prefix[index]
		if left >= 'A' && left <= 'Z' {
			left += 'a' - 'A'
		}
		if right >= 'A' && right <= 'Z' {
			right += 'a' - 'A'
		}
		if left != right {
			return false
		}
	}
	return true
}

func trimSpaceStringContext(ctx context.Context, value string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	start := 0
	for start < len(value) {
		if start%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		r, size := utf8.DecodeRuneInString(value[start:])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}
	end := len(value)
	for end > start {
		if (len(value)-end)%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		r, size := utf8.DecodeLastRuneInString(value[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	return value[start:end], ctx.Err()
}

func resolveAbsoluteLink(target string) (ConceptID, bool) {
	trimmed := stripLinkSuffix(target)
	if strings.HasSuffix(trimmed, "/") {
		return ConceptID{}, false
	}
	segments := normalizeConceptPathSegments(strings.Split(strings.TrimLeft(trimmed, "/"), "/"), nil)
	return conceptIDFromNormalizedSegments(segments)
}

func resolveRelativeLink(target string, source ConceptID) (ConceptID, bool) {
	trimmed := stripLinkSuffix(target)
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

func stripLinkSuffix(target string) string {
	if i := strings.IndexAny(target, "?#"); i >= 0 {
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

func scanLineLinks(line string, out *[]Link) {
	runes := []rune(line)
	for i := 0; i < len(runes); {
		if runes[i] == '[' && !escapedRune(runes, i) {
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

func scanLineLinksContext(ctx context.Context, line string, out *[]Link) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var runes []rune
	for index, value := range line {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		runes = append(runes, value)
	}
	var local []Link
	for index := 0; index < len(runes); {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if runes[index] == '[' && !escapedRune(runes, index) {
			text, destination, next, ok, err := parseInlineLinkContext(ctx, runes, index)
			if err != nil {
				return err
			}
			if ok {
				target, err := stripLinkTitleContext(ctx, destination)
				if err != nil {
					return err
				}
				kind, err := ClassifyLinkContext(ctx, target)
				if err != nil {
					return err
				}
				local = append(local, Link{Text: text, Target: target, Kind: kind})
				index = next
				continue
			}
		}
		index++
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	*out = append(*out, local...)
	return ctx.Err()
}

func parseInlineLinkContext(ctx context.Context, runes []rune, start int) (string, string, int, bool, error) {
	index, depth, textStart := start+1, 1, start+1
	for index < len(runes) {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return "", "", 0, false, err
			}
		}
		switch runes[index] {
		case '\\':
			index++
		case '[':
			depth++
		case ']':
			depth--
		}
		if depth == 0 {
			break
		}
		index++
	}
	if depth != 0 || index >= len(runes) {
		return "", "", 0, false, ctx.Err()
	}
	text := string(runes[textStart:index])
	destinationStart := index + 2
	if index+1 >= len(runes) || runes[index+1] != '(' {
		return "", "", 0, false, ctx.Err()
	}
	index = destinationStart
	depth = 1
	for index < len(runes) {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return "", "", 0, false, err
			}
		}
		switch runes[index] {
		case '\\':
			index++
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth == 0 {
			break
		}
		index++
	}
	if depth != 0 || index >= len(runes) {
		return "", "", 0, false, ctx.Err()
	}
	return text, string(runes[destinationStart:index]), index + 1, true, ctx.Err()
}

func stripLinkTitleContext(ctx context.Context, destination string) (string, error) {
	trimmed, err := trimSpaceStringContext(ctx, destination)
	if err != nil {
		return "", err
	}
	for index, value := range trimmed {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if value == ' ' || value == '\t' {
			rest, err := trimLeftHorizontalSpaceContext(ctx, trimmed[index:])
			if err != nil {
				return "", err
			}
			if strings.HasPrefix(rest, "\"") || strings.HasPrefix(rest, "'") {
				return trimmed[:index], ctx.Err()
			}
			return trimmed, ctx.Err()
		}
	}
	return trimmed, ctx.Err()
}

func trimLeftHorizontalSpaceContext(ctx context.Context, value string) (string, error) {
	for index := 0; index < len(value); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if value[index] != ' ' && value[index] != '\t' {
			return value[index:], ctx.Err()
		}
	}
	return "", ctx.Err()
}

func escapedRune(runes []rune, at int) bool {
	backslashes := 0
	for at > 0 && runes[at-1] == '\\' {
		backslashes++
		at--
	}
	return backslashes%2 == 1
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
