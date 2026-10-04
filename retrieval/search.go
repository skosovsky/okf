// Package retrieval provides source-pinned lexical section search over immutable OKF bundles.
package retrieval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/documentlayout"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	DefaultLimit           = 20
	MaxLimit               = 100
	MaxFiles               = 10000
	MaxBytes               = 64 << 20
	MaxFileBytes           = 16 << 20
	MaxSections            = 100000
	MaxTokens              = 1000000
	MaxOutputBytes         = 1 << 20
	MaxNormalizedTermRunes = 4096
)

var (
	ErrLimit = errors.New("section search resource limit")
	ErrQuery = errors.New("invalid section search query")
	ErrParse = errors.New("section search bundle parse failure")
)

type Hit struct {
	ConceptID string   `json:"concept_id"`
	Path      string   `json:"path"`
	Heading   string   `json:"heading"`
	Locator   string   `json:"locator"`
	LineStart int      `json:"line_start"`
	LineEnd   int      `json:"line_end"`
	Digest    string   `json:"content_sha256"`
	Snippet   string   `json:"snippet"`
	Score     float64  `json:"score"`
	Terms     []string `json:"matched_terms"`
}
type Result struct {
	Query     string   `json:"query"`
	Terms     []string `json:"terms"`
	Revision  string   `json:"snapshot_revision"`
	Total     int      `json:"total"`
	Truncated bool     `json:"truncated"`
	Hits      []Hit    `json:"hits"`
}
type section struct {
	hit       Hit
	raw       string
	frequency map[string]int
	length    int
}

// QueryTerms validates and normalizes the query independently of bundle I/O.
func QueryTerms(query string) ([]string, error) {
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > 512 {
		return nil, ErrQuery
	}
	hasLetterNumber := false
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			hasLetterNumber = true
			break
		}
	}
	if !hasLetterNumber {
		return nil, ErrQuery
	}
	words := tokens(query)
	seen := map[string]bool{}
	result := []string{}
	for _, word := range words {
		if utf8.RuneCountInString(word) > MaxNormalizedTermRunes {
			return nil, ErrLimit
		}
		if !seen[word] {
			result = append(result, word)
			seen[word] = true
		}
	}
	if len(result) == 0 {
		return nil, ErrQuery
	}
	if len(result) > 32 {
		return nil, ErrLimit
	}
	return result, nil
}
func tokens(s string) []string {
	s = norm.NFC.String(cases.Fold().String(s))
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) })
}

// Load loads a no-follow bounded filesystem snapshot; it never writes data.
func Load(ctx context.Context, root string) (b *bundle.Bundle, err error) {
	source := &bundle.FileSystemSource{Root: root}
	defer func() {
		err = errors.Join(err, source.Close())
		if err != nil {
			b = nil
		}
	}()
	return bundle.Load(ctx, &boundedSource{source: source})
}

type boundedSource struct {
	source bundle.Source
	total  int
}

func (s *boundedSource) Paths(ctx context.Context) ([]string, error) {
	p, e := s.source.Paths(ctx)
	if e != nil {
		return nil, e
	}
	if len(p) > MaxFiles {
		return nil, ErrLimit
	}
	for _, v := range p {
		if len(v) > 4096 || strings.Count(v, "/")+1 > 64 {
			return nil, ErrLimit
		}
	}
	return p, nil
}
func (s *boundedSource) ReadFile(ctx context.Context, p string) ([]byte, error) {
	d, e := s.source.ReadFile(ctx, p)
	if e != nil {
		return nil, e
	}
	s.total += len(d)
	if len(d) > MaxFileBytes || s.total > MaxBytes {
		return nil, ErrLimit
	}
	return d, nil
}

// Search uses only captured bytes. A changed filesystem cannot rebind a result.
func Search(ctx context.Context, b *bundle.Bundle, query string, limit int) (Result, error) {
	result := Result{Query: query, Hits: []Hit{}}
	terms, err := QueryTerms(query)
	if err != nil {
		return Result{}, err
	}
	result.Terms = terms
	if limit < 1 || limit > MaxLimit {
		return Result{}, ErrQuery
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	if b == nil {
		return Result{}, ErrParse
	}
	if len(b.ParseErrors()) > 0 {
		for _, p := range b.ParseErrors() {
			if errors.Is(p.Err, ErrLimit) {
				return Result{}, ErrLimit
			}
		}
		return Result{}, ErrParse
	}
	files, err := b.FilesContext(ctx)
	if err != nil {
		return Result{}, err
	}
	if len(files) > MaxFiles {
		return Result{}, ErrLimit
	}
	type record struct {
		Path string `json:"path"`
		Size uint64 `json:"size"`
		SHA  string `json:"sha256"`
	}
	records := make([]record, 0, len(files))
	totalBytes := uint64(0)
	for _, p := range files {
		if err = ctx.Err(); err != nil {
			return Result{}, err
		}
		size, digest, ok := b.CapturedFileMetadata(p)
		if !ok {
			return Result{}, ErrParse
		}
		if size > MaxFileBytes {
			return Result{}, ErrLimit
		}
		totalBytes += size
		relative := relativePath(b, p)
		if len(relative) > 4096 || strings.Count(relative, "/")+1 > 64 {
			return Result{}, ErrLimit
		}
		records = append(records, record{relative, size, hex.EncodeToString(digest[:])})
	}
	if totalBytes > MaxBytes {
		return Result{}, ErrLimit
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	data, err := json.Marshal(records)
	if err != nil {
		return Result{}, err
	}
	revision := sha256.Sum256(data)
	result.Revision = hex.EncodeToString(revision[:])
	ids, err := b.ConceptIDsContext(ctx)
	if err != nil {
		return Result{}, err
	}
	sections := []section{}
	tokenCount := 0
	for _, id := range ids {
		concept, found, getErr := b.GetContext(ctx, id)
		if getErr != nil {
			return Result{}, getErr
		}
		if !found {
			return Result{}, ErrParse
		}
		if err = ctx.Err(); err != nil {
			return Result{}, err
		}
		raw, ok, e := b.ReadFileContext(ctx, concept.Path)
		if e != nil {
			return Result{}, e
		}
		if !ok {
			return Result{}, ErrParse
		}
		parts, e := splitSections(ctx, b, concept, raw)
		if e != nil {
			return Result{}, e
		}
		title, _ := concept.Document.Frontmatter.Title()
		if len(title) > 1024 {
			return Result{}, ErrLimit
		}
		for i := range parts {
			parts[i].frequency = map[string]int{}
			for _, field := range []struct {
				text   string
				weight int
			}{{parts[i].raw, 1}, {parts[i].hit.Heading, 3}, {title, 2}} {
				if e = ctx.Err(); e != nil {
					return Result{}, e
				}
				words := tokens(field.text)
				tokenCount += len(words)
				if tokenCount > MaxTokens {
					return Result{}, ErrLimit
				}
				for n, word := range words {
					if n%1024 == 0 {
						if e = ctx.Err(); e != nil {
							return Result{}, e
						}
					}
					parts[i].frequency[word] += field.weight
					parts[i].length += field.weight
				}
			}
		}
		sections = append(sections, parts...)
		if len(sections) > MaxSections {
			return Result{}, ErrLimit
		}
	}
	frequency := map[string]int{}
	avg := float64(0)
	for _, s := range sections {
		if err = ctx.Err(); err != nil {
			return Result{}, err
		}
		avg += float64(s.length)
		for _, term := range terms {
			if s.frequency[term] > 0 {
				frequency[term]++
			}
		}
	}
	if len(sections) > 0 {
		avg /= float64(len(sections))
	}
	if avg == 0 {
		avg = 1
	}
	for _, s := range sections {
		if err = ctx.Err(); err != nil {
			return Result{}, err
		}
		match := true
		score := float64(0)
		for _, term := range terms {
			tf := float64(s.frequency[term])
			if tf == 0 {
				match = false
				break
			}
			df := float64(frequency[term])
			idf := math.Log(1 + (float64(len(sections))-df+0.5)/(df+0.5))
			score += idf * (tf * 2.2) / (tf + 1.2*(0.25+0.75*float64(s.length)/avg))
		}
		if match {
			h := s.hit
			h.Score = score
			h.Terms = append([]string{}, terms...)
			h.Snippet, err = snippet(ctx, s.raw, terms)
			if err != nil {
				return Result{}, err
			}
			result.Hits = append(result.Hits, h)
		}
	}
	sort.Slice(result.Hits, func(i, j int) bool {
		a, c := result.Hits[i], result.Hits[j]
		if a.Score != c.Score {
			return a.Score > c.Score
		}
		if a.ConceptID != c.ConceptID {
			return a.ConceptID < c.ConceptID
		}
		return a.LineStart < c.LineStart
	})
	result.Total = len(result.Hits)
	result.Truncated = result.Total > limit
	if result.Truncated {
		result.Hits = result.Hits[:limit]
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return Result{}, err
	}
	if len(encoded) > MaxOutputBytes {
		return Result{}, ErrLimit
	}
	return result, nil
}
func relativePath(b *bundle.Bundle, p string) string {
	if b.Root() == "" {
		return filepath.ToSlash(p)
	}
	if rel, e := filepath.Rel(b.Root(), p); e == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}
func splitSections(ctx context.Context, b *bundle.Bundle, c bundle.Concept, raw []byte) ([]section, error) {
	layout, has, err := documentlayout.SplitContext(ctx, raw)
	if err != nil {
		return nil, err
	}
	offset := 0
	if has {
		offset = layout.BodyStart
	}
	body := raw[offset:]
	lineStarts := []int{0}
	for i, v := range raw {
		if i%65536 == 0 {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
		}
		if v == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	lineAt := func(pos int) int {
		return sort.Search(len(lineStarts), func(i int) bool { return lineStarts[i] > pos })
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	doc := goldmark.New().Parser().Parse(text.NewReader(body))
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	type marker struct {
		start   int
		heading string
	}
	markers := []marker{{offset, ""}}
	err = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if e := ctx.Err(); e != nil {
			return ast.WalkStop, e
		}
		h, ok := n.(*ast.Heading)
		if !entering || !ok || h.Lines().Len() == 0 {
			return ast.WalkContinue, nil
		}
		pos := offset + h.Lines().At(0).Start
		lineStart := lineStarts[lineAt(pos)-1]
		heading := string(h.Text(body))
		if len(heading) > 1024 {
			return ast.WalkStop, ErrLimit
		}
		if lineStart == markers[len(markers)-1].start {
			markers[len(markers)-1].heading = heading
		} else {
			markers = append(markers, marker{lineStart, heading})
		}
		if len(markers) > MaxSections {
			return ast.WalkStop, ErrLimit
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	_, digest, ok := b.CapturedFileMetadata(c.Path)
	if !ok {
		return nil, ErrParse
	}
	path := relativePath(b, c.Path)
	result := []section{}
	// Every boundary is a physical line start. A final newline is excluded from the inclusive range.
	for i, m := range markers {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		end := len(raw)
		if i+1 < len(markers) {
			end = markers[i+1].start
		}
		if end > m.start && raw[end-1] == '\n' {
			end--
			if end > m.start && raw[end-1] == '\r' {
				end--
			}
		}
		if end <= m.start || len(bytes.TrimSpace(raw[m.start:end])) == 0 {
			continue
		}
		startLine := lineAt(m.start)
		endLine := lineAt(end)
		hit := Hit{ConceptID: c.ID.String(), Path: path, Heading: m.heading, LineStart: startLine, LineEnd: endLine, Digest: hex.EncodeToString(digest[:]), Locator: (&url.URL{Path: path}).EscapedPath() + fmt.Sprintf("#L%d-L%d", startLine, endLine)}
		result = append(result, section{hit: hit, raw: string(raw[m.start:end])})
	}
	return result, nil
}
func snippet(ctx context.Context, raw string, terms []string) (string, error) {
	// Keep exact original bytes; find a visible token by normalized comparison without offset guessing.
	runes := []rune(raw)
	start := 0
	wordStart := -1
	for i := 0; i <= len(runes); i++ {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		var r rune
		if i < len(runes) {
			r = runes[i]
		}
		word := unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r)
		if word && wordStart < 0 {
			wordStart = i
		}
		if !word && wordStart >= 0 {
			candidate := tokens(string(runes[wordStart:i]))
			if len(candidate) > 0 {
				for _, term := range terms {
					if candidate[0] == term {
						start = max(0, wordStart-80)
						return string(runes[start:min(len(runes), start+512)]), nil
					}
				}
			}
			wordStart = -1
		}
	}
	return string(runes[:min(len(runes), 512)]), nil
}
