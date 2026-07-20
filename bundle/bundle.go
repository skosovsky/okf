package bundle

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"unicode/utf8"
)

// ReservedFilenames are OKF filenames with reserved meaning at every bundle level.
var ReservedFilenames = []string{"index.md", "log.md"}

func isReservedFilename(name string) bool {
	for _, reserved := range ReservedFilenames {
		if name == reserved {
			return true
		}
	}
	return false
}

// ParseError records a concept file that could not be parsed while loading a bundle.
type ParseError struct {
	Path string
	Err  error
}

// Error returns a human-readable parse error.
func (e ParseError) Error() string {
	if e.Err == nil {
		return e.Path
	}
	return fmt.Sprintf("%s: %v", e.Path, e.Err)
}

// ResolvedLink is an internal link resolved to a target concept id.
type ResolvedLink struct {
	Target ConceptID
	Exists bool
	Text   string
	Raw    string
}

// BrokenLink is a resolved internal link whose target concept is absent.
type BrokenLink struct {
	Source ConceptID
	Raw    string
}

// Bundle is a loaded OKF directory tree.
type Bundle struct {
	root         string
	files        []string
	concepts     []Concept
	byID         map[string]int
	indexFiles   []string
	logFiles     []string
	parseErrors  []ParseError
	outbound     map[string][]ResolvedLink
	backlinks    map[string][]ConceptID
	semantic     map[string][]Relation
	incoming     map[string][]Relation
	subresources map[string]map[string]fragmentState
	diagnostics  []RelationDiagnostic
	contents     map[string][]byte
}

// LoadBundle loads an OKF bundle from a directory tree.
//
// I/O failures and a non-directory root are returned as errors. Per-file parse
// failures are collected in Bundle.ParseErrors and do not abort loading.
func LoadBundle(root string) (*Bundle, error) {
	source := &FileSystemSource{Root: root}
	defer source.Close()
	b, err := Load(context.Background(), source)
	if b != nil {
		b.root = root
		for i, p := range b.files {
			b.files[i] = filepath.Join(root, filepath.FromSlash(p))
		}
		for i := range b.indexFiles {
			b.indexFiles[i] = filepath.Join(root, filepath.FromSlash(b.indexFiles[i]))
		}
		for i := range b.logFiles {
			b.logFiles[i] = filepath.Join(root, filepath.FromSlash(b.logFiles[i]))
		}
		for i := range b.concepts {
			b.concepts[i].Path = filepath.Join(root, filepath.FromSlash(b.concepts[i].Path))
		}
		for i := range b.parseErrors {
			b.parseErrors[i].Path = filepath.Join(root, filepath.FromSlash(b.parseErrors[i].Path))
		}
	}
	return b, err
}

// Load builds an immutable bundle from source without filesystem access.
//
// The caller retains ownership of source. In particular, Load never closes a
// supplied io.Closer, so a Source may be reused for subsequent loads.
func Load(ctx context.Context, source Source) (*Bundle, error) {
	if source == nil {
		return nil, fmt.Errorf("nil bundle source")
	}
	files, err := source.Paths(ctx)
	if err != nil {
		return nil, err
	}
	clean := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, name := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name, err = normalizeSourcePath(name)
		if err != nil {
			return nil, err
		}
		if filepath.Ext(name) != ".md" {
			continue
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate bundle source path %q", name)
		}
		seen[name] = struct{}{}
		clean = append(clean, name)
	}
	sort.Strings(clean)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	bundle := &Bundle{
		files:        clean,
		byID:         make(map[string]int),
		outbound:     make(map[string][]ResolvedLink),
		backlinks:    make(map[string][]ConceptID),
		semantic:     make(map[string][]Relation),
		incoming:     make(map[string][]Relation),
		subresources: make(map[string]map[string]fragmentState),
		contents:     make(map[string][]byte),
	}

	for _, path := range clean {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, readErr := source.ReadFile(ctx, path)
		if readErr != nil {
			bundle.parseErrors = append(bundle.parseErrors, ParseError{Path: path, Err: readErr})
			continue
		}
		owned, err := copySourceBytesContext(ctx, data)
		if err != nil {
			return nil, err
		}
		bundle.contents[path] = owned
		switch filepath.Base(path) {
		case "index.md":
			bundle.indexFiles = append(bundle.indexFiles, path)
		case "log.md":
			bundle.logFiles = append(bundle.logFiles, path)
		default:
			bundle.loadConceptBytes(path, data)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	for i, concept := range bundle.concepts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bundle.byID[concept.ID.String()] = i
	}
	bundle.buildGraph()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle.finalizeRelationDiagnostics()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return bundle, nil
}

func copySourceBytesContext(ctx context.Context, source []byte) ([]byte, error) {
	out := make([]byte, 0, len(source))
	const chunkSize = 64 << 10
	for len(source) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := min(len(source), chunkSize)
		out = append(out, source[:n]...)
		source = source[n:]
	}
	return out, ctx.Err()
}

// Root returns the bundle root path.
func (b *Bundle) Root() string {
	if b == nil {
		return ""
	}
	return b.root
}

// Concepts returns the successfully parsed concepts in path order.
func (b *Bundle) Concepts() []Concept {
	if b == nil {
		return nil
	}
	return append([]Concept(nil), b.concepts...)
}

// Len returns the number of successfully parsed concepts.
func (b *Bundle) Len() int {
	if b == nil {
		return 0
	}
	return len(b.concepts)
}

// IsEmpty reports whether the bundle has no parsed concepts.
func (b *Bundle) IsEmpty() bool {
	return b.Len() == 0
}

// Get returns a concept by id.
func (b *Bundle) Get(id ConceptID) (Concept, bool) {
	if b == nil {
		return Concept{}, false
	}
	index, ok := b.byID[id.String()]
	if !ok {
		return Concept{}, false
	}
	return b.concepts[index], true
}

// Contains reports whether the bundle contains a concept id.
func (b *Bundle) Contains(id ConceptID) bool {
	if b == nil {
		return false
	}
	_, ok := b.byID[id.String()]
	return ok
}

// IndexFiles returns discovered index.md files.
func (b *Bundle) IndexFiles() []string {
	if b == nil {
		return nil
	}
	return append([]string(nil), b.indexFiles...)
}

// LogFiles returns discovered log.md files.
func (b *Bundle) LogFiles() []string {
	if b == nil {
		return nil
	}
	return append([]string(nil), b.logFiles...)
}

// MarkdownFiles returns all Markdown files discovered while loading the bundle.
func (b *Bundle) MarkdownFiles() []string {
	if b == nil {
		return nil
	}
	return append([]string(nil), b.files...)
}

// ReadFile returns an owned copy of a file captured while loading the bundle.
// It never rereads the source.
func (b *Bundle) ReadFile(path string) ([]byte, bool) { return b.contentForPath(path) }

// ParseErrors returns concept parse failures collected during loading.
func (b *Bundle) ParseErrors() []ParseError {
	if b == nil {
		return nil
	}
	return append([]ParseError(nil), b.parseErrors...)
}

// LinksFrom returns resolved outbound internal links for a concept id.
func (b *Bundle) LinksFrom(id ConceptID) []ResolvedLink {
	if b == nil {
		return nil
	}
	return append([]ResolvedLink(nil), b.outbound[id.String()]...)
}

// SemanticLinksFrom returns outbound YAML semantic relations for a concept id.
func (b *Bundle) SemanticLinksFrom(id ConceptID) []Relation {
	if b == nil {
		return nil
	}
	return append([]Relation(nil), b.semantic[id.String()]...)
}

// Backlinks returns concept ids that link to the given concept.
func (b *Bundle) Backlinks(id ConceptID) []ConceptID {
	if b == nil {
		return nil
	}
	return append([]ConceptID(nil), b.backlinks[id.String()]...)
}

// BrokenLinks returns all broken internal links.
func (b *Bundle) BrokenLinks() []BrokenLink {
	if b == nil {
		return nil
	}

	var broken []BrokenLink
	for _, concept := range b.concepts {
		for _, link := range b.LinksFrom(concept.ID) {
			if !link.Exists {
				broken = append(broken, BrokenLink{
					Source: concept.ID,
					Raw:    link.Raw,
				})
			}
		}
	}
	return broken
}

// OKFVersion returns the okf_version declared in the root index.md frontmatter.
func (b *Bundle) OKFVersion() (string, bool) {
	if b == nil {
		return "", false
	}
	text, ok := b.contentForPath(filepath.Join(b.root, "index.md"))
	if !ok {
		return "", false
	}
	document, err := ParseDocument(string(text))
	if err != nil {
		return "", false
	}
	return document.Frontmatter.OKFVersion()
}

func (b *Bundle) loadConceptBytes(path string, text []byte) {
	if !utf8.Valid(text) {
		b.parseErrors = append(b.parseErrors, ParseError{Path: path, Err: fmt.Errorf("%w: invalid UTF-8", ErrInvalidEncoding)})
		return
	}
	document, err := ParseDocument(string(text))
	if err != nil {
		b.parseErrors = append(b.parseErrors, ParseError{Path: path, Err: err})
		return
	}
	id, err := ConceptIDFromPath("", path)
	if err != nil {
		b.parseErrors = append(b.parseErrors, ParseError{Path: path, Err: err})
		return
	}
	b.concepts = append(b.concepts, NewConcept(id, path, document))
}

func (b *Bundle) contentForPath(filename string) ([]byte, bool) {
	if b == nil {
		return nil, false
	}
	key := filename
	if b.root != "" {
		if rel, err := filepath.Rel(b.root, filename); err == nil {
			key = filepath.ToSlash(rel)
		}
	}
	v, ok := b.contents[key]
	return append([]byte(nil), v...), ok
}

func (b *Bundle) buildGraph() {
	for _, concept := range b.concepts {
		b.indexSubresources(concept)
	}
	for _, concept := range b.concepts {
		var resolved []ResolvedLink
		for _, link := range concept.Document.Links() {
			target, ok := link.Resolve(concept.ID)
			if !ok {
				continue
			}

			exists := b.Contains(target)
			if exists {
				key := target.String()
				if !containsConceptID(b.backlinks[key], concept.ID) {
					b.backlinks[key] = append(b.backlinks[key], concept.ID)
				}
			}

			resolved = append(resolved, ResolvedLink{
				Target: target,
				Exists: exists,
				Text:   link.Text,
				Raw:    link.Target,
			})
		}
		b.outbound[concept.ID.String()] = resolved
		b.semantic[concept.ID.String()] = extractSemanticRelations(concept, b)
		sortRelations(b.semantic[concept.ID.String()])
		for _, relation := range b.semantic[concept.ID.String()] {
			if relation.TargetExists {
				key := relation.Target.String()
				b.incoming[key] = append(b.incoming[key], relation)
			}
		}
	}
	for target := range b.incoming {
		sortRelations(b.incoming[target])
	}
}

func containsConceptID(ids []ConceptID, target ConceptID) bool {
	for _, id := range ids {
		if id.String() == target.String() {
			return true
		}
	}
	return false
}
