package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
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

// Unwrap preserves typed YAML and document parse failures for errors.Is and
// errors.As without dropping the path carried by ParseError.
func (e ParseError) Unwrap() error { return e.Err }

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
	allFiles     []string
	assetFiles   []string
	concepts     []Concept
	byID         map[string]int
	indexFiles   []string
	logFiles     []string
	parseErrors  []ParseError
	outbound     map[string][]ResolvedLink
	backlinks    map[string][]ConceptID
	semantic     map[string][]Relation
	observations map[string][]RelationObservation
	incoming     map[relationRefKey][]Relation
	subresources map[string]map[string]fragmentState
	diagnostics  []RelationDiagnostic
	contents     map[string][]byte
	captured     map[string]capturedFile
}

type capturedFile struct {
	size   uint64
	sha256 [sha256.Size]byte
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
		for i, p := range b.allFiles {
			b.allFiles[i] = filepath.Join(root, filepath.FromSlash(p))
		}
		for i, p := range b.assetFiles {
			b.assetFiles[i] = filepath.Join(root, filepath.FromSlash(p))
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, err := source.Paths(ctx)
	if err != nil {
		return nil, err
	}
	files, err = preflightSourcePathsContext(ctx, files)
	if err != nil {
		return nil, err
	}
	clean := make([]string, 0, len(files))
	markdown := make([]string, 0, len(files))
	assets := make([]string, 0, len(files))
	for _, name := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		clean = append(clean, name)
		if filepath.Ext(name) == ".md" {
			markdown = append(markdown, name)
		} else {
			assets = append(assets, name)
		}
	}
	if err := sortCompareContext(ctx, clean, compareStringsContext); err != nil {
		return nil, err
	}
	if err := sortCompareContext(ctx, markdown, compareStringsContext); err != nil {
		return nil, err
	}
	if err := sortCompareContext(ctx, assets, compareStringsContext); err != nil {
		return nil, err
	}

	bundle := &Bundle{
		files:        markdown,
		allFiles:     clean,
		assetFiles:   assets,
		byID:         make(map[string]int),
		outbound:     make(map[string][]ResolvedLink),
		backlinks:    make(map[string][]ConceptID),
		semantic:     make(map[string][]Relation),
		observations: make(map[string][]RelationObservation),
		incoming:     make(map[relationRefKey][]Relation),
		subresources: make(map[string]map[string]fragmentState),
		contents:     make(map[string][]byte),
		captured:     make(map[string]capturedFile),
	}

	for _, path := range clean {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, readErr := source.ReadFile(ctx, path)
		if readErr != nil {
			return nil, fmt.Errorf("read bundle file %q: %w", path, readErr)
		}
		owned, err := copySourceBytesContext(ctx, data)
		if err != nil {
			return nil, err
		}
		bundle.contents[path] = owned
		metadata, err := capturedFileMetadataContext(ctx, owned)
		if err != nil {
			return nil, err
		}
		bundle.captured[path] = metadata
		if filepath.Ext(path) != ".md" {
			continue
		}
		switch filepath.Base(path) {
		case "index.md":
			bundle.indexFiles = append(bundle.indexFiles, path)
		case "log.md":
			bundle.logFiles = append(bundle.logFiles, path)
		default:
			if err := bundle.loadConceptBytesContext(ctx, path, owned); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	for i, concept := range bundle.concepts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, err := conceptIDStringContext(ctx, concept.ID)
		if err != nil {
			return nil, err
		}
		bundle.byID[id] = i
	}
	if err := bundle.buildGraphContext(ctx); err != nil {
		return nil, err
	}
	if err := bundle.finalizeRelationDiagnosticsContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return bundle, nil
}

// preflightSourcePathsContext canonicalizes the validation order before Load
// performs any reads. Source is a BYOT boundary, so provider order must not
// decide which malformed or duplicate path is reported.
func preflightSourcePathsContext(ctx context.Context, paths []string) ([]string, error) {
	clean := make([]string, 0, len(paths))
	for _, raw := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		owned, err := stringFromStringContext(ctx, raw)
		if err != nil {
			return nil, err
		}
		clean = append(clean, owned)
	}
	if err := sortCompareContext(ctx, clean, compareStringsContext); err != nil {
		return nil, err
	}
	for index, raw := range clean {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		normalized, err := normalizeSourcePathContext(ctx, raw)
		if err != nil {
			return nil, err
		}
		clean[index] = normalized
		if index > 0 && clean[index-1] == normalized {
			return nil, fmt.Errorf("duplicate bundle source path %q", raw)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return clean, nil
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func capturedFileMetadataContext(ctx context.Context, data []byte) (capturedFile, error) {
	if err := ctx.Err(); err != nil {
		return capturedFile{}, err
	}
	digest := sha256.New()
	const chunkSize = 64 << 10
	for offset := 0; offset < len(data); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return capturedFile{}, err
		}
		end := min(offset+chunkSize, len(data))
		if _, err := digest.Write(data[offset:end]); err != nil {
			return capturedFile{}, fmt.Errorf("hash captured bundle file: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return capturedFile{}, err
	}
	var sum [sha256.Size]byte
	digest.Sum(sum[:0])
	return capturedFile{size: uint64(len(data)), sha256: sum}, nil
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
	out := make([]Concept, len(b.concepts))
	for index := range b.concepts {
		out[index] = cloneConcept(b.concepts[index])
	}
	return out
}

// ConceptIDsContext returns defensive concept identifiers in path order with
// cancellation checks during projection.
func (b *Bundle) ConceptIDsContext(ctx context.Context) ([]ConceptID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	var out []ConceptID
	for _, concept := range b.concepts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		owned, err := cloneConceptIDContext(ctx, concept.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, owned)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
	concept, ok, _ := b.GetContext(context.Background(), id)
	return concept, ok
}

// GetContext returns a cancellation-aware defensive concept copy. Unknown ids
// and nil bundles return the zero concept with ok=false.
func (b *Bundle) GetContext(ctx context.Context, id ConceptID) (Concept, bool, error) {
	if err := ctx.Err(); err != nil {
		return Concept{}, false, err
	}
	if b == nil {
		return Concept{}, false, nil
	}
	idString, err := conceptIDStringContext(ctx, id)
	if err != nil {
		return Concept{}, false, err
	}
	index, ok, err := lookupStringMapContext(ctx, b.byID, idString)
	if err != nil {
		return Concept{}, false, err
	}
	if !ok {
		return Concept{}, false, nil
	}
	concept, err := cloneConceptContext(ctx, b.concepts[index])
	if err != nil {
		return Concept{}, false, err
	}
	return concept, true, ctx.Err()
}

// ConceptPathContext returns the retained path for id without cloning its
// document. Unknown ids and nil bundles return ("", false, nil).
func (b *Bundle) ConceptPathContext(ctx context.Context, id ConceptID) (path string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if b == nil {
		return "", false, nil
	}
	idString, err := conceptIDStringContext(ctx, id)
	if err != nil {
		return "", false, err
	}
	index, ok, err := lookupStringMapContext(ctx, b.byID, idString)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return b.concepts[index].Path, true, nil
}

func cloneConceptID(id ConceptID) ConceptID {
	owned, _ := cloneConceptIDContext(context.Background(), id)
	return owned
}

func cloneConceptIDContext(ctx context.Context, id ConceptID) (ConceptID, error) {
	if err := ctx.Err(); err != nil {
		return ConceptID{}, err
	}
	var segments []string
	for _, segment := range id.segments {
		if err := ctx.Err(); err != nil {
			return ConceptID{}, err
		}
		owned, err := stringFromStringContext(ctx, segment)
		if err != nil {
			return ConceptID{}, err
		}
		segments = append(segments, owned)
	}
	if err := ctx.Err(); err != nil {
		return ConceptID{}, err
	}
	return ConceptID{segments: segments}, nil
}

func cloneConcept(concept Concept) Concept {
	owned, _ := cloneConceptContext(context.Background(), concept)
	return owned
}

func cloneConceptContext(ctx context.Context, concept Concept) (Concept, error) {
	var out Concept
	var err error
	if out.ID, err = cloneConceptIDContext(ctx, concept.ID); err != nil {
		return Concept{}, err
	}
	if out.Path, err = stringFromStringContext(ctx, concept.Path); err != nil {
		return Concept{}, err
	}
	out.Document.HasFrontmatter = concept.Document.HasFrontmatter
	if out.Document.Body, err = stringFromStringContext(ctx, concept.Document.Body); err != nil {
		return Concept{}, err
	}
	if out.Document.Frontmatter.node, err = cloneYAMLNodeContext(ctx, &concept.Document.Frontmatter.node); err != nil {
		return Concept{}, err
	}
	if out.Document.Frontmatter.raw, err = copySourceBytesContext(ctx, concept.Document.Frontmatter.raw); err != nil {
		return Concept{}, err
	}
	if out.Document.frontmatterYAML, err = copySourceBytesContext(ctx, concept.Document.frontmatterYAML); err != nil {
		return Concept{}, err
	}
	return out, ctx.Err()
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
	out, _ := b.MarkdownFilesContext(context.Background())
	return out
}

// MarkdownFilesContext returns a cancellation-aware owned copy of Markdown
// paths in loader order.
func (b *Bundle) MarkdownFilesContext(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	var out []string
	for _, path := range b.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Files returns every revision-visible regular file captured while loading.
func (b *Bundle) Files() []string {
	out, _ := b.FilesContext(context.Background())
	return out
}

// FilesContext returns a cancellation-aware owned copy of every
// revision-visible regular file captured while loading.
func (b *Bundle) FilesContext(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	var out []string
	for _, path := range b.allFiles {
		owned, err := stringFromStringContext(ctx, path)
		if err != nil {
			return nil, err
		}
		out = append(out, owned)
	}
	if err := bundleFilesFinalContext(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func bundleFilesFinalContext(ctx context.Context) error {
	return ctx.Err()
}

// AssetFiles returns captured non-Markdown revision-visible regular files.
func (b *Bundle) AssetFiles() []string {
	if b == nil {
		return nil
	}
	return append([]string(nil), b.assetFiles...)
}

// ReadFile returns an owned copy of a file captured while loading the bundle.
// It never rereads the source.
func (b *Bundle) ReadFile(path string) ([]byte, bool) {
	out, ok, _ := b.ReadFileContext(context.Background(), path)
	return out, ok
}

// ReadFileContext returns a cancellation-aware owned copy of a captured file.
// Unknown paths and nil bundles return (nil, false, nil).
func (b *Bundle) ReadFileContext(ctx context.Context, path string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if b == nil {
		return nil, false, nil
	}
	data, ok := b.contents[b.capturedPathKey(path)]
	if !ok {
		return nil, false, nil
	}
	owned, err := copySourceBytesContext(ctx, data)
	if err != nil {
		return nil, false, err
	}
	return owned, true, nil
}

// CapturedFileMetadata returns the immutable size and SHA-256 digest captured
// while loading path. It never rereads the source or exposes retained bytes.
func (b *Bundle) CapturedFileMetadata(path string) (uint64, [sha256.Size]byte, bool) {
	if b == nil {
		return 0, [sha256.Size]byte{}, false
	}
	metadata, ok := b.captured[b.capturedPathKey(path)]
	if !ok {
		return 0, [sha256.Size]byte{}, false
	}
	return metadata.size, metadata.sha256, true
}

// CapturedFilesEqualContext compares two files retained by immutable bundles.
// Metadata is used as a fast rejection, but matching metadata is always
// followed by a cancellable, chunked exact comparison of the retained bytes.
func CapturedFilesEqualContext(
	ctx context.Context,
	left *Bundle,
	leftPath string,
	right *Bundle,
	rightPath string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if left == nil || right == nil {
		return false, nil
	}
	leftKey := left.capturedPathKey(leftPath)
	rightKey := right.capturedPathKey(rightPath)
	leftMetadata, leftOK := left.captured[leftKey]
	rightMetadata, rightOK := right.captured[rightKey]
	if !leftOK || !rightOK {
		return false, nil
	}
	if leftMetadata != rightMetadata {
		return false, nil
	}
	leftData, leftOK := left.contents[leftKey]
	rightData, rightOK := right.contents[rightKey]
	if !leftOK || !rightOK {
		return false, nil
	}
	if len(leftData) != len(rightData) {
		return false, nil
	}
	const chunkSize = 64 << 10
	for offset := 0; offset < len(leftData); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		end := min(offset+chunkSize, len(leftData))
		if !bytes.Equal(leftData[offset:end], rightData[offset:end]) {
			return false, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}

// ParseErrors returns concept parse failures collected during loading.
func (b *Bundle) ParseErrors() []ParseError {
	if b == nil {
		return nil
	}
	return append([]ParseError(nil), b.parseErrors...)
}

// LinksFrom returns resolved outbound internal links for a concept id.
func (b *Bundle) LinksFrom(id ConceptID) []ResolvedLink {
	out, _ := b.LinksFromContext(context.Background(), id)
	return out
}

// LinksFromContext returns a cancellation-aware defensive copy of resolved
// Markdown links for id.
func (b *Bundle) LinksFromContext(ctx context.Context, id ConceptID) ([]ResolvedLink, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	idString, err := conceptIDStringContext(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []ResolvedLink
	links, _, err := lookupStringMapContext(ctx, b.outbound, idString)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		var owned ResolvedLink
		if owned.Target, err = cloneConceptIDContext(ctx, link.Target); err != nil {
			return nil, err
		}
		if owned.Text, err = stringFromStringContext(ctx, link.Text); err != nil {
			return nil, err
		}
		if owned.Raw, err = stringFromStringContext(ctx, link.Raw); err != nil {
			return nil, err
		}
		owned.Exists = link.Exists
		out = append(out, owned)
	}
	return out, ctx.Err()
}

// SemanticLinksFrom returns outbound YAML semantic relations for a concept id.
func (b *Bundle) SemanticLinksFrom(id ConceptID) []Relation {
	out, _ := b.SemanticLinksFromContext(context.Background(), id)
	return out
}

// SemanticLinksFromContext returns a cancellation-aware defensive copy of
// outbound YAML semantic relations for a concept id.
func (b *Bundle) SemanticLinksFromContext(ctx context.Context, id ConceptID) ([]Relation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	idString, err := conceptIDStringContext(ctx, id)
	if err != nil {
		return nil, err
	}
	relations, _, err := lookupStringMapContext(ctx, b.semantic, idString)
	if err != nil {
		return nil, err
	}
	if len(relations) == 0 {
		return nil, nil
	}
	var out []Relation
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		owned, err := cloneRelationContext(ctx, relation)
		if err != nil {
			return nil, err
		}
		out = append(out, owned)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// DeclaredSemanticLinksFrom returns syntactically valid declared relations,
// including unresolved concept and fragment targets.
func (b *Bundle) DeclaredSemanticLinksFrom(id ConceptID) []RelationObservation {
	out, _ := b.DeclaredSemanticLinksFromContext(context.Background(), id)
	return out
}

// DeclaredSemanticLinksFromContext returns a cancellation-aware defensive copy
// of syntactically valid declared relation observations.
func (b *Bundle) DeclaredSemanticLinksFromContext(ctx context.Context, id ConceptID) ([]RelationObservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	idString, err := conceptIDStringContext(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []RelationObservation
	observations, _, err := lookupStringMapContext(ctx, b.observations, idString)
	if err != nil {
		return nil, err
	}
	for _, observation := range observations {
		owned, err := cloneRelationContext(ctx, Relation(observation))
		if err != nil {
			return nil, err
		}
		out = append(out, RelationObservation(owned))
	}
	return out, ctx.Err()
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

// VersionDeclarationState returns raw/present/valid root declaration state.
func (b *Bundle) VersionDeclarationState() VersionDeclarationState {
	state, _ := b.VersionDeclarationStateContext(context.Background())
	return state
}

// VersionDeclarationStateContext returns the root version declaration with
// cancellation checks around retained-byte projection and document parsing.
func (b *Bundle) VersionDeclarationStateContext(ctx context.Context) (VersionDeclarationState, error) {
	if err := ctx.Err(); err != nil {
		return VersionDeclarationState{}, err
	}
	document, present, err := b.rootIndexDocumentContext(ctx)
	if !present {
		return VersionDeclarationState{Valid: true}, err
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return VersionDeclarationState{}, ctxErr
		}
		if errors.Is(err, ErrYAMLResourceLimit) || errors.Is(err, ErrInvalidYAMLGraph) {
			return VersionDeclarationState{}, err
		}
		return VersionDeclarationState{Present: true}, nil
	}
	return document.Frontmatter.VersionDeclarationStateContext(ctx)
}

func (b *Bundle) rootIndexDocument() (Document, bool, error) {
	return b.rootIndexDocumentContext(context.Background())
}

func (b *Bundle) rootIndexDocumentContext(ctx context.Context) (Document, bool, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, false, err
	}
	if b == nil {
		return Document{}, false, nil
	}
	text, ok, err := b.ReadFileContext(ctx, filepath.Join(b.root, "index.md"))
	if err != nil {
		return Document{}, false, err
	}
	if !ok {
		return Document{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return Document{}, false, err
	}
	document, err := ParseDocumentContext(ctx, string(text))
	if err != nil {
		return Document{}, true, fmt.Errorf("parse root index document: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Document{}, false, err
	}
	return document, true, nil
}

func (b *Bundle) loadConceptBytes(path string, text []byte) {
	_ = b.loadConceptBytesContext(context.Background(), path, text)
}

func (b *Bundle) loadConceptBytesContext(ctx context.Context, path string, text []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !utf8.Valid(text) {
		b.parseErrors = append(b.parseErrors, ParseError{Path: path, Err: fmt.Errorf("%w: invalid UTF-8", ErrInvalidEncoding)})
		return nil
	}
	document, err := ParseDocumentContext(ctx, string(text))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		b.parseErrors = append(b.parseErrors, ParseError{Path: path, Err: err})
		return nil
	}
	id, err := ConceptIDFromPath("", path)
	if err != nil {
		b.parseErrors = append(b.parseErrors, ParseError{Path: path, Err: err})
		return nil
	}
	b.concepts = append(b.concepts, NewConcept(id, path, document))
	return ctx.Err()
}

func (b *Bundle) capturedPathKey(filename string) string {
	if _, ok := b.contents[filename]; ok {
		return filename
	}
	key := filename
	if b.root != "" {
		if rel, err := filepath.Rel(b.root, filename); err == nil {
			key = filepath.ToSlash(rel)
		}
	}
	return key
}

func (b *Bundle) buildGraphContext(ctx context.Context) error {
	for _, concept := range b.concepts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.indexSubresourcesContext(ctx, concept); err != nil {
			return err
		}
	}
	for _, concept := range b.concepts {
		if err := ctx.Err(); err != nil {
			return err
		}
		var resolved []ResolvedLink
		links, err := concept.Document.LinksContext(ctx)
		if err != nil {
			return err
		}
		for _, link := range links {
			if err := ctx.Err(); err != nil {
				return err
			}
			target, ok, err := link.ResolveContext(ctx, concept.ID)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}

			targetKey, err := conceptIDStringContext(ctx, target)
			if err != nil {
				return err
			}
			_, exists := b.byID[targetKey]
			if exists {
				contains, err := containsConceptIDContext(ctx, b.backlinks[targetKey], concept.ID)
				if err != nil {
					return err
				}
				if !contains {
					b.backlinks[targetKey] = append(b.backlinks[targetKey], concept.ID)
				}
			}

			resolved = append(resolved, ResolvedLink{
				Target: target,
				Exists: exists,
				Text:   link.Text,
				Raw:    link.Target,
			})
		}
		conceptKey, err := conceptIDStringContext(ctx, concept.ID)
		if err != nil {
			return err
		}
		b.outbound[conceptKey] = resolved
		semanticResolved, observed, err := extractSemanticRelationsContext(ctx, concept, b)
		if err != nil {
			return err
		}
		b.semantic[conceptKey] = semanticResolved
		b.observations[conceptKey] = observed
		if err := sortRelationsContext(ctx, b.semantic[conceptKey]); err != nil {
			return err
		}
		if err := sortRelationObservationsContext(ctx, b.observations[conceptKey]); err != nil {
			return err
		}
		for _, relation := range b.semantic[conceptKey] {
			if err := ctx.Err(); err != nil {
				return err
			}
			if relation.TargetExists {
				key, err := relationRefIdentityContext(ctx, relation.Target)
				if err != nil {
					return err
				}
				b.incoming[key] = append(b.incoming[key], relation)
			}
		}
	}
	for target := range b.incoming {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := sortRelationsContext(ctx, b.incoming[target]); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func containsConceptID(ids []ConceptID, target ConceptID) bool {
	contains, _ := containsConceptIDContext(context.Background(), ids, target)
	return contains
}

func containsConceptIDContext(ctx context.Context, ids []ConceptID, target ConceptID) (bool, error) {
	targetString, err := conceptIDStringContext(ctx, target)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		idString, err := conceptIDStringContext(ctx, id)
		if err != nil {
			return false, err
		}
		equal, err := equalStringContext(ctx, idString, targetString)
		if err != nil {
			return false, err
		}
		if equal {
			return true, ctx.Err()
		}
	}
	return false, ctx.Err()
}
