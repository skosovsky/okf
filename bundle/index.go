package bundle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

const indexFilename = "index.md"

// IndexEntry is one generated index row.
type IndexEntry struct {
	Type        string
	Title       string
	Link        string
	Description string
}

// SynthesizeDescription derives a one-line description for a subdirectory.
type SynthesizeDescription func(rel string, children []IndexChild) string

// IndexChild is a child title/description pair supplied to a synthesizer.
type IndexChild struct {
	Title       string
	Description string
}

// BuildIndexText renders index entries as grouped Markdown.
func BuildIndexText(entries []IndexEntry) string {
	grouped := make(map[string][]IndexEntry)
	for _, entry := range entries {
		key := entry.Type
		if key == "" {
			key = "Other"
		}
		grouped[key] = append(grouped[key], entry)
	}

	types := make([]string, 0, len(grouped))
	for typ := range grouped {
		types = append(types, typ)
	}
	sort.Strings(types)

	var sections []string
	for _, typ := range types {
		items := append([]IndexEntry(nil), grouped[typ]...)
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
		})

		lines := []string{fmt.Sprintf("# %s", typ), ""}
		for _, item := range items {
			suffix := ""
			if item.Description != "" {
				suffix = " - " + item.Description
			}
			lines = append(lines, fmt.Sprintf("* [%s](%s)%s", item.Title, item.Link, suffix))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}

	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n\n") + "\n"
}

// DefaultSynthesizeDescription deterministically describes a subdirectory by listing child titles.
func DefaultSynthesizeDescription(_ string, children []IndexChild) string {
	if len(children) == 0 {
		return ""
	}
	titles := make([]string, 0, len(children))
	for _, child := range children {
		titles = append(titles, child.Title)
	}
	return fmt.Sprintf("Contains %d: %s.", len(children), strings.Join(titles, ", "))
}

// RegenerateIndexes regenerates every index.md in the bundle.
func RegenerateIndexes(bundleRoot string) ([]string, error) {
	return RegenerateIndexesWithSelector(bundleRoot, "")
}

// RegenerateIndexesWith regenerates index.md files using a custom synthesizer.
func RegenerateIndexesWith(bundleRoot string, synthesize SynthesizeDescription) ([]string, error) {
	return regenerateIndexesWithHooks(bundleRoot, synthesize, "", indexPublishHooks{})
}

// RegenerateIndexesWithSelector regenerates index.md files after resolving the
// root declaration and selector against the same pinned source that is later
// published. selector is an assertion: pass "" for automatic resolution.
func RegenerateIndexesWithSelector(bundleRoot, selector string) ([]string, error) {
	return regenerateIndexesWithHooks(bundleRoot, DefaultSynthesizeDescription, selector, indexPublishHooks{})
}

func regenerateIndexesWithHooks(
	bundleRoot string,
	synthesize SynthesizeDescription,
	selector string,
	hooks indexPublishHooks,
) ([]string, error) {
	if synthesize == nil {
		synthesize = DefaultSynthesizeDescription
	}

	source := &FileSystemSource{Root: bundleRoot}
	root, err := source.openRoot()
	if err != nil {
		return nil, err
	}
	releaseIndexLock, err := acquirePublicationLock(root)
	if err != nil {
		_ = source.Close()
		return nil, err
	}
	defer finalizeIndexRegeneration(source, releaseIndexLock, hooks.beforeSourceClose)
	if hooks.afterIndexLock != nil {
		if err := hooks.afterIndexLock(); err != nil {
			return nil, err
		}
	}
	if err := inspectGlobalIndexRecoveryNamespace(root, hooks); err != nil {
		return nil, err
	}
	files, err := source.Paths(context.Background())
	if err != nil {
		return nil, err
	}
	rootIndex, err := resolveRootIndexForPublication(source, selector)
	if err != nil {
		return nil, err
	}
	if hooks.afterResolve != nil {
		if err := hooks.afterResolve(); err != nil {
			return nil, err
		}
	}
	documents, err := preloadIndexDocuments(source, files)
	if err != nil {
		return nil, err
	}
	directories, err := directoriesToIndex(bundleRoot, files)
	if err != nil {
		return nil, err
	}
	directoryDepths := make(map[string]int, len(directories))
	for _, directory := range directories {
		relative, err := filepath.Rel(bundleRoot, directory)
		if err != nil {
			return nil, fmt.Errorf("resolve index directory %q: %w", directory, err)
		}
		depth := 0
		if relative != "." {
			depth = len(strings.Split(filepath.ToSlash(relative), "/"))
		}
		directoryDepths[directory] = depth
	}
	sort.SliceStable(directories, func(i, j int) bool {
		depthI := directoryDepths[directories[i]]
		depthJ := directoryDepths[directories[j]]
		if depthI != depthJ {
			return depthI > depthJ
		}
		return directories[i] < directories[j]
	})

	outputs := make([]indexOutput, 0, len(directories))
	dirDescriptions := make(map[string]string)
	for _, directory := range directories {
		directoryRel, err := filepath.Rel(bundleRoot, directory)
		if err != nil {
			return nil, err
		}
		isRoot := directoryRel == "."
		entries, err := indexEntriesForDirectoryFromDocuments(
			filepath.ToSlash(directoryRel),
			files,
			dirDescriptions,
			documents,
		)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			continue
		}

		indexPath := filepath.Join(directory, indexFilename)
		text := BuildIndexText(entries)
		if isRoot {
			text, err = preserveRootIndexVersion(rootIndex, text)
			if err != nil {
				return nil, err
			}
		}
		outputs = append(outputs, indexOutput{
			relative: filepath.ToSlash(filepath.Join(directoryRel, indexFilename)),
			absolute: indexPath,
			data:     []byte(text),
		})
		if isRoot {
			outputs[len(outputs)-1].expectedOriginal = &indexExpectedOriginal{
				present: rootIndex.present,
				data:    append([]byte(nil), rootIndex.raw...),
			}
		}

		if isRoot {
			continue
		}

		children := make([]IndexChild, 0, len(entries))
		for _, entry := range entries {
			children = append(children, IndexChild{Title: entry.Title, Description: entry.Description})
		}
		description := ""
		if len(children) == 1 && children[0].Description != "" {
			description = children[0].Description
		} else {
			description = synthesize(filepath.ToSlash(directoryRel), children)
		}
		dirDescriptions[pathJoin("", strings.TrimPrefix(filepath.ToSlash(directoryRel), "./"))] = description
	}

	return publishIndexOutputs(root, outputs, hooks)
}

func finalizeIndexRegeneration(
	source *FileSystemSource,
	releaseIndexLock func(),
	beforeSourceClose func(),
) {
	// Release is registered first so the pinned source root is always closed
	// while both the process-local and cross-process ownership guards remain
	// held. The nested defers preserve that order even if a test barrier or
	// Close panics.
	defer releaseIndexLock()
	defer func() {
		_ = source.Close()
	}()
	if beforeSourceClose != nil {
		beforeSourceClose()
	}
}

type rootIndexPublicationState struct {
	present  bool
	raw      []byte
	document Document
}

func resolveRootIndexForPublication(source Source, selector string) (rootIndexPublicationState, error) {
	raw, err := source.ReadFile(context.Background(), indexFilename)
	if errors.Is(err, fs.ErrNotExist) {
		_, resolveErr := ResolveVersion("", selector)
		return rootIndexPublicationState{}, resolveErr
	}
	if err != nil {
		return rootIndexPublicationState{}, indexDocumentReadError{path: indexFilename, err: err}
	}
	document, err := ParseDocumentContext(context.Background(), string(raw))
	if err != nil {
		return rootIndexPublicationState{}, fmt.Errorf("parse index document %q: %w", indexFilename, err)
	}
	state := document.Frontmatter.VersionDeclarationState()
	if state.Present && !state.Valid {
		return rootIndexPublicationState{}, invalidVersionDeclarationError(state)
	}
	if _, err := ResolveVersion(state.Value, selector); err != nil {
		return rootIndexPublicationState{}, err
	}
	return rootIndexPublicationState{
		present:  true,
		raw:      append([]byte(nil), raw...),
		document: document,
	}, nil
}

func preserveRootIndexVersion(rootIndex rootIndexPublicationState, body string) (string, error) {
	if !rootIndex.present {
		return body, nil
	}

	state := rootIndex.document.Frontmatter.VersionDeclarationState()
	if !state.Present {
		return body, nil
	}
	if !state.Valid {
		return "", invalidVersionDeclarationError(state)
	}

	frontmatter := NewFrontmatter()
	if err := frontmatter.SetString("okf_version", state.Value); err != nil {
		return "", err
	}
	updated := NewDocument(frontmatter, body)
	return updated.Serialize()
}

func invalidVersionDeclarationError(state VersionDeclarationState) error {
	return fmt.Errorf("%w: %q", ErrInvalidVersionDeclaration, state.Raw)
}

func indexEntriesForDirectory(source Source, directory string, files []string, dirDescriptions map[string]string) ([]IndexEntry, error) {
	documents, err := preloadIndexDocuments(source, files)
	if err != nil {
		return nil, err
	}
	return indexEntriesForDirectoryFromDocuments(directory, files, dirDescriptions, documents)
}

func preloadIndexDocuments(source Source, files []string) (map[string]Document, error) {
	ordered := append([]string(nil), files...)
	sort.Strings(ordered)
	documents := make(map[string]Document)
	for _, file := range ordered {
		if err := ValidateRevisionPath(file); err != nil {
			return nil, fmt.Errorf("invalid index planning path %q: %w", file, err)
		}
		if filepath.Ext(file) != ".md" || isReservedFilename(filepath.Base(filepath.FromSlash(file))) {
			continue
		}
		document, err := loadIndexDocument(source, file)
		if err != nil {
			return nil, err
		}
		documents[file] = document
	}
	return documents, nil
}

func indexEntriesForDirectoryFromDocuments(
	directory string,
	files []string,
	dirDescriptions map[string]string,
	documents map[string]Document,
) ([]IndexEntry, error) {
	directory = strings.TrimPrefix(directory, "./")
	if directory == "." {
		directory = ""
	}
	children := make(map[string]bool)
	prefix := directory
	if prefix != "" {
		prefix += "/"
	}
	for _, file := range files {
		if !strings.HasPrefix(file, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(file, prefix)
		part := strings.SplitN(remainder, "/", 2)[0]
		if part != "" {
			children[part] = strings.Contains(remainder, "/")
		}
	}
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	sort.Strings(names)
	var entries []IndexEntry
	for _, name := range names {
		if isReservedFilename(name) {
			continue
		}
		childRel := pathJoin(directory, name)
		if children[name] {
			entries = append(entries, IndexEntry{
				Type:        "Subdirectories",
				Title:       name,
				Link:        filepath.ToSlash(filepath.Join(name, indexFilename)),
				Description: dirDescriptions[childRel],
			})
			continue
		}
		if filepath.Ext(name) != ".md" {
			continue
		}

		document, ok := documents[childRel]
		if !ok {
			return nil, fmt.Errorf("missing preloaded index document %q", childRel)
		}
		title, ok := document.Frontmatter.Title()
		if !ok || title == "" {
			title = strings.TrimSuffix(name, filepath.Ext(name))
		}
		description, _ := document.Frontmatter.Description()
		typ, _ := document.Frontmatter.Type()
		entries = append(entries, IndexEntry{
			Type:        typ,
			Title:       title,
			Link:        name,
			Description: description,
		})
	}

	return entries, nil
}

func loadIndexDocument(source Source, path string) (Document, error) {
	text, err := source.ReadFile(context.Background(), path)
	if err != nil {
		return Document{}, indexDocumentReadError{path: path, err: err}
	}
	document, err := ParseDocumentContext(context.Background(), string(text))
	if err != nil {
		return Document{}, fmt.Errorf("parse index document %q: %w", path, err)
	}
	return document, nil
}

type indexDocumentReadError struct {
	path string
	err  error
}

func (e indexDocumentReadError) Error() string {
	return fmt.Sprintf("read index document %q", e.path)
}

func (e indexDocumentReadError) Unwrap() error {
	return e.err
}

func directoriesToIndex(bundleRoot string, files []string) ([]string, error) {
	seen := make(map[string]struct{})
	cleanRoot := filepath.Clean(bundleRoot)
	for _, file := range files {
		if filepath.Ext(file) != ".md" {
			continue
		}
		dir := filepath.Join(bundleRoot, filepath.FromSlash(filepath.Dir(file)))
		for {
			seen[dir] = struct{}{}
			if filepath.Clean(dir) == cleanRoot {
				break
			}
			next := filepath.Dir(dir)
			if next == dir {
				return nil, fmt.Errorf("index directory %q is outside bundle root %q", dir, bundleRoot)
			}
			dir = next
		}
	}

	directories := make([]string, 0, len(seen))
	for dir := range seen {
		directories = append(directories, dir)
	}
	sort.Strings(directories)
	return directories, nil
}

func pathJoin(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}
