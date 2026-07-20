package bundle

import (
	"context"
	"fmt"
	"os"
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
	return RegenerateIndexesWith(bundleRoot, DefaultSynthesizeDescription)
}

// RegenerateIndexesWith regenerates index.md files using a custom synthesizer.
func RegenerateIndexesWith(bundleRoot string, synthesize SynthesizeDescription) ([]string, error) {
	if synthesize == nil {
		synthesize = DefaultSynthesizeDescription
	}

	source := &FileSystemSource{Root: bundleRoot}
	defer source.Close()
	files, err := source.Paths(context.Background())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	directories, err := directoriesToIndex(bundleRoot, files)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(directories, func(i, j int) bool {
		depthI := pathDepth(bundleRoot, directories[i])
		depthJ := pathDepth(bundleRoot, directories[j])
		if depthI != depthJ {
			return depthI > depthJ
		}
		return directories[i] < directories[j]
	})

	var written []string
	dirDescriptions := make(map[string]string)
	for _, directory := range directories {
		directoryRel, err := filepath.Rel(bundleRoot, directory)
		if err != nil {
			return nil, err
		}
		entries, err := indexEntriesForDirectory(source, filepath.ToSlash(directoryRel), files, dirDescriptions)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			continue
		}

		indexPath := filepath.Join(directory, indexFilename)
		text := BuildIndexText(entries)
		if samePath(directory, bundleRoot) {
			var err error
			text, err = preserveRootIndexVersion(source, text)
			if err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(indexPath, []byte(text), 0o644); err != nil {
			return nil, err
		}
		written = append(written, indexPath)

		if samePath(directory, bundleRoot) {
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
			rel, _ := filepath.Rel(bundleRoot, directory)
			description = synthesize(filepath.ToSlash(rel), children)
		}
		dirDescriptions[pathJoin("", strings.TrimPrefix(filepath.ToSlash(directoryRel), "./"))] = description
	}

	return written, nil
}

func preserveRootIndexVersion(source Source, body string) (string, error) {
	document, ok := loadIndexDocument(source, indexFilename)
	if !ok {
		return body, nil
	}

	version, ok := document.Frontmatter.OKFVersion()
	if !ok {
		return body, nil
	}

	frontmatter := NewFrontmatter()
	if err := frontmatter.SetString("okf_version", version); err != nil {
		return "", err
	}
	updated := NewDocument(frontmatter, body)
	return updated.Serialize()
}

func indexEntriesForDirectory(source Source, directory string, files []string, dirDescriptions map[string]string) ([]IndexEntry, error) {
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

		document, ok := loadIndexDocument(source, childRel)
		if !ok {
			continue
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

func loadIndexDocument(source Source, path string) (Document, bool) {
	text, err := source.ReadFile(context.Background(), path)
	if err != nil {
		return Document{}, false
	}
	document, err := ParseDocument(string(text))
	if err != nil {
		return Document{}, false
	}
	return document, true
}

func directoriesToIndex(bundleRoot string, files []string) ([]string, error) {
	seen := make(map[string]struct{})
	for _, file := range files {
		if filepath.Ext(file) != ".md" {
			continue
		}
		dir := filepath.Join(bundleRoot, filepath.FromSlash(filepath.Dir(file)))
		for {
			seen[dir] = struct{}{}
			if samePath(dir, bundleRoot) {
				break
			}
			next := filepath.Dir(dir)
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

func pathDepth(root, dir string) int {
	rel, _ := filepath.Rel(root, dir)
	if rel == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

func samePath(a, b string) bool {
	cleanA := filepath.Clean(a)
	cleanB := filepath.Clean(b)
	rel, err := filepath.Rel(cleanA, cleanB)
	return err == nil && rel == "."
}
