package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type hit struct {
	Concept struct {
		ID string `json:"id"`
	} `json:"concept"`
	ID      string `json:"concept_id"`
	Path    string `json:"path"`
	Heading string `json:"heading"`
	Locator string `json:"locator"`
	Start   int    `json:"line_start"`
	End     int    `json:"line_end"`
	Snippet string `json:"snippet"`
	Digest  string `json:"content_sha256"`
}
type toolResponse struct {
	IsError    bool `json:"isError"`
	Structured *struct {
		Hits     []hit  `json:"hits"`
		Revision string `json:"snapshot_revision"`
	} `json:"structuredContent"`
}

func contentHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func verifyFreeze(manifestPath, queryPath, root string) (map[string][]byte, string, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, "", err
	}
	var freeze struct {
		Files  map[string]string `json:"files"`
		Digest string            `json:"corpus_digest"`
	}
	if err = json.Unmarshal(raw, &freeze); err != nil {
		return nil, "", err
	}
	keys := make([]string, 0, len(freeze.Files))
	for k := range freeze.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// The original freeze was generated with Python's sorted json.dumps default separators.
	var canonical bytes.Buffer
	canonical.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			canonical.WriteString(", ")
		}
		key, _ := json.Marshal(k)
		value, _ := json.Marshal(freeze.Files[k])
		canonical.Write(key)
		canonical.WriteString(": ")
		canonical.Write(value)
	}
	canonical.WriteByte('}')
	if len(keys) == 0 || contentHash(canonical.Bytes()) != freeze.Digest {
		return nil, "", fmt.Errorf("freeze manifest digest mismatch")
	}
	queryBytes, err := os.ReadFile(queryPath)
	if err != nil {
		return nil, "", err
	}
	queryKey := "benchmarks/retrieval/testdata/queries.json"
	if contentHash(queryBytes) != freeze.Files[queryKey] {
		return nil, "", fmt.Errorf("frozen queries changed")
	}
	expected := map[string]string{}
	prefix := "benchmarks/retrieval/testdata/corpus/"
	for k, d := range freeze.Files {
		if strings.HasPrefix(k, prefix) {
			expected[strings.TrimPrefix(k, prefix)] = d
		} else if k != queryKey {
			return nil, "", fmt.Errorf("unknown frozen artifact %s", k)
		}
	}
	files := map[string][]byte{}
	err = filepath.WalkDir(root, func(p string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("corpus symlink %s", p)
		}
		if e.IsDir() {
			return nil
		}
		if !e.Type().IsRegular() {
			return fmt.Errorf("corpus special file %s", p)
		}
		rel, e2 := filepath.Rel(root, p)
		if e2 != nil {
			return e2
		}
		rel = filepath.ToSlash(rel)
		want, ok := expected[rel]
		if !ok {
			return fmt.Errorf("unfrozen corpus file %s", rel)
		}
		b, e2 := os.ReadFile(p)
		if e2 != nil {
			return e2
		}
		if contentHash(b) != want {
			return fmt.Errorf("frozen corpus file changed %s", rel)
		}
		files[rel] = b
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if len(files) != len(expected) {
		return nil, "", fmt.Errorf("frozen corpus inventory changed")
	}
	return files, freeze.Digest, nil
}
func snapshotRevision(files map[string][]byte) string {
	type record struct {
		Path string `json:"path"`
		Size uint64 `json:"size"`
		SHA  string `json:"sha256"`
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	records := make([]record, 0, len(keys))
	for _, k := range keys {
		records = append(records, record{k, uint64(len(files[k])), contentHash(files[k])})
	}
	raw, _ := json.Marshal(records)
	return contentHash(raw)
}
func heading(line string) string {
	line = strings.TrimSpace(line)
	for i := 1; i <= 6; i++ {
		prefix := strings.Repeat("#", i) + " "
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
func validateGold(cases Corpus, files map[string][]byte) error {
	if len(cases.Cases) < 20 {
		return fmt.Errorf("corpus needs at least 20 cases")
	}
	seen := map[string]bool{}
	noAnswer := 0
	for _, c := range cases.Cases {
		if c.ID == "" || seen[c.ID] || strings.TrimSpace(c.Query) == "" {
			return fmt.Errorf("invalid/duplicate case %s", c.ID)
		}
		seen[c.ID] = true
		if len(c.Relevant) == 0 {
			noAnswer++
		}
		for _, g := range c.Relevant {
			data, ok := files[g.ID+".md"]
			if !ok {
				return fmt.Errorf("%s missing gold concept %s", c.ID, g.ID)
			}
			lines := strings.Split(string(data), "\n")
			if g.Line < 1 || g.Line > len(lines) || g.Heading == "" || heading(lines[g.Line-1]) != g.Heading {
				return fmt.Errorf("%s invalid gold heading/line", c.ID)
			}
			end := g.Line
			for end < len(lines) && heading(lines[end]) == "" {
				end++
			}
			if g.Evidence == "" || !strings.Contains(strings.Join(lines[g.Line-1:end], "\n"), g.Evidence) {
				return fmt.Errorf("%s gold evidence absent from section", c.ID)
			}
		}
	}
	if noAnswer < 4 {
		return fmt.Errorf("corpus needs at least four no-answer cases")
	}
	return nil
}
func validateHit(h hit, files map[string][]byte) error {
	if !fs.ValidPath(h.Path) || !strings.HasSuffix(h.Path, ".md") || strings.TrimSuffix(h.Path, ".md") != h.ID {
		return fmt.Errorf("invalid concept/path identity")
	}
	data, ok := files[h.Path]
	if !ok {
		return fmt.Errorf("unfrozen source path")
	}
	if contentHash(data) != h.Digest {
		return fmt.Errorf("stale source digest")
	}
	lines := strings.Split(string(data), "\n")
	if h.Start < 1 || h.End < h.Start || h.End > len(lines) {
		return fmt.Errorf("invalid range")
	}
	if h.Heading != "" && heading(lines[h.Start-1]) != h.Heading {
		return fmt.Errorf("heading not at returned start")
	}
	expectedEnd := len(lines)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		expectedEnd--
	}
	for i := h.Start; i < len(lines); i++ {
		if heading(lines[i]) != "" {
			expectedEnd = i
			break
		}
	}
	if h.End != expectedEnd {
		return fmt.Errorf("range does not end at section boundary")
	}
	want := (&url.URL{Path: h.Path}).EscapedPath() + fmt.Sprintf("#L%d-L%d", h.Start, h.End)
	if h.Locator != want {
		return fmt.Errorf("invalid locator")
	}
	text := strings.Join(lines[h.Start-1:h.End], "\n")
	if h.Snippet == "" || !strings.Contains(text, h.Snippet) {
		return fmt.Errorf("snippet not in returned range")
	}
	return nil
}
func documentIDs(hits []hit, tool string) ([]string, error) {
	if len(hits) > 5 {
		return nil, fmt.Errorf("tool exceeded five-hit evaluation budget")
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, h := range hits {
		id := h.Concept.ID
		if tool == "search_sections" {
			id = h.ID
		}
		if id == "" {
			return nil, fmt.Errorf("missing concept ID")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func score(ids []string, gold []Gold) (bool, float64) {
	for rank, id := range ids {
		if rank >= 5 {
			break
		}
		for _, g := range gold {
			if id == g.ID {
				return true, 1 / float64(rank+1)
			}
		}
	}
	return false, 0
}
