package toolkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Corpus is a deterministic, immutable-in-use snapshot. The seed is fixed by
// the numeric ID sequence; no random generator or wall clock participates.
type Corpus map[string][]byte

type Manifest struct {
	Concepts int               `json:"concepts"`
	Indexes  int               `json:"indexes"`
	Logs     int               `json:"logs"`
	Files    int               `json:"files"`
	Bytes    int               `json:"bytes"`
	Edges    int               `json:"edges"`
	Sources  int               `json:"sources"`
	Wraps    int               `json:"line_wraps"`
	SHA256   string            `json:"sha256"`
	FileSHA  map[string]string `json:"file_sha256"`
}

// Generate produces 100-concept directory shards. Links form a ring within
// each shard, and one source plus one body footnote belong to each concept.
// The standard corpus uses single-line log items so both revisions can run
// the same conformant work. Wrapped-log behavior has a separate case.
func Generate(n int) (Corpus, Manifest) {
	if n <= 0 || n > 10000 {
		panic("toolkit benchmark concept count must be 1..10000")
	}
	files := make(Corpus)
	groups := (n + 99) / 100
	var root strings.Builder
	root.WriteString("---\nokf_version: \"0.2\"\n---\n# Knowledge\n\n")
	for g := 0; g < groups; g++ {
		fmt.Fprintf(&root, "* [Group %03d](g%03d/index.md)\n", g, g)
		var index strings.Builder
		fmt.Fprintf(&index, "# Group %03d\n\n", g)
		start := g * 100
		end := min(start+100, n)
		for i := start; i < end; i++ {
			fmt.Fprintf(&index, "* [Concept %05d](c%05d.md)\n", i, i)
			next := start + (i-start+1)%(end-start)
			files[fmt.Sprintf("g%03d/c%05d.md", g, i)] = []byte(fmt.Sprintf("---\ntype: Note\nsources:\n  - id: s%d\n    resource: https://example.org/source/%d\n---\n# Concept %05d\n\nSee [next](c%05d.md). Claim [^s%d].\n\n[^s%d]: Example source.\n", i, i, i, next, i, i))
		}
		files[fmt.Sprintf("g%03d/index.md", g)] = []byte(index.String())
	}
	files["index.md"] = []byte(root.String())
	files["log.md"] = []byte("# Log\n\n## 2026-09-21\n* Corpus generated from a fixed numeric sequence.\n")
	return files, ManifestFor(files, n, 0)
}

// InteropCorpus has one wrapped log item and an inline-code footnote decoy.
// The text is authored for this benchmark; no foreign repository bytes are copied.
func InteropCorpus() (Corpus, Manifest) {
	files, _ := Generate(10)
	files["log.md"] = []byte("# Log\n\n## 2026-09-21\n* **Decision**: Entry starts\n  and continues.\n")
	files["g000/c00000.md"] = []byte(strings.Replace(string(files["g000/c00000.md"]),
		"\n\n[^s0]:", "\n\n`[^fake]`\n\n[^s0]:", 1))
	return files, ManifestFor(files, 10, 1)
}

// TemporalCorpus changes only the first concept. Both inputs remain parseable
// Markdown on the baseline. Instant-profile validation is a corrected-only
// operation and has no baseline timing because its semantics did not exist.
func TemporalCorpus(instant bool) (Corpus, Manifest) {
	files, _ := Generate(10)
	value, from, to := "2026-09-26", "2026-09-25", "2026-09-26"
	if instant {
		value = "2026-09-26T12:00:00+07:00"
		from = "2026-09-26T12:00:00+07:00"
		to = "2026-09-26T05:00:00Z" // Same instant, different offset.
	}
	files["g000/c00000.md"] = []byte(fmt.Sprintf("---\ntype: Note\nstale_after: %s\nsources:\n  - id: s0\n    resource: https://example.org/source/0\n    last_modified: %s\n    usage_count: 1\n    usage_window: {from: %s, to: %s}\n---\n# Concept 00000\n\nSee [next](c00001.md). Claim [^s0].\n\n[^s0]: Example source.\n", value, value, from, to))
	return files, ManifestFor(files, 10, 0)
}

func ManifestFor(files Corpus, n, wraps int) Manifest {
	groups := (n + 99) / 100
	manifest := Manifest{Concepts: n, Indexes: groups + 1, Logs: 1, Edges: n, Sources: n, Wraps: wraps, FileSHA: make(map[string]string)}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		data := files[k]
		manifest.Files++
		manifest.Bytes += len(data)
		sum := sha256.Sum256(data)
		manifest.FileSHA[k] = hex.EncodeToString(sum[:])
		fmt.Fprintf(h, "%d:%s:%d:", len(k), k, len(data))
		h.Write(data)
	}
	manifest.SHA256 = hex.EncodeToString(h.Sum(nil))
	return manifest
}

func (c Corpus) Paths(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(c))
	for p := range c {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

func (c Corpus) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := c[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (c Corpus) WriteDir(root string) error {
	for path, data := range c {
		name := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(name, data, 0644); err != nil {
			return err
		}
	}
	return nil
}
