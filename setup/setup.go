// Package setup prepares an explicit managed copy of ordinary Markdown.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/validator"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

type Options struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Files  []string `json:"selection"`
	Type   string   `json:"type"`
}
type Diagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}
type File struct {
	Path         string `json:"path"`
	SourceDigest string `json:"source_digest"`
	OutputDigest string `json:"output_digest"`
}
type Plan struct {
	Version     int          `json:"version"`
	Options     Options      `json:"options"`
	Files       []File       `json:"files"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Applicable  bool         `json:"applicable"`
	Digest      string       `json:"digest"`
	Published   bool         `json:"published"`
}

func hash(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func (p *Plan) diagnostic(code, file, message string) {
	p.Diagnostics = append(p.Diagnostics, Diagnostic{code, file, message})
}

func Preview(ctx context.Context, opts Options) (Plan, error) {
	p, _, err := prepare(ctx, opts)
	return p, err
}
func prepare(ctx context.Context, opts Options) (Plan, map[string][]byte, error) {
	p := Plan{Version: 1, Options: opts, Files: []File{}, Diagnostics: []Diagnostic{}}
	out := map[string][]byte{}
	if err := ctx.Err(); err != nil {
		return p, nil, err
	}
	var err error
	if opts.Source == "" || opts.Target == "" {
		return p, nil, fmt.Errorf("source and target are required")
	}
	opts.Source, err = filepath.Abs(opts.Source)
	if err != nil {
		return p, nil, err
	}
	opts.Target, err = filepath.Abs(opts.Target)
	if err != nil {
		return p, nil, err
	}
	// Resolve directory aliases to prevent target nesting through a symlinked parent.
	opts.Source, err = filepath.EvalSymlinks(opts.Source)
	if err != nil {
		return p, nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(opts.Target))
	if err != nil {
		return p, nil, err
	}
	opts.Target = filepath.Join(parent, filepath.Base(opts.Target))
	opts.Files = append([]string(nil), opts.Files...)
	sort.Strings(opts.Files)
	p.Options = opts
	rel, _ := filepath.Rel(opts.Source, opts.Target)
	back, _ := filepath.Rel(opts.Target, opts.Source)
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") || (!strings.HasPrefix(back, ".."+string(filepath.Separator)) && back != "..") {
		return p, nil, fmt.Errorf("source and target must be disjoint")
	}
	if _, err := os.Lstat(opts.Target); err == nil {
		p.diagnostic("target_exists", "", "target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return p, nil, err
	}
	root, err := os.OpenRoot(opts.Source)
	if err != nil {
		return p, nil, err
	}
	defer root.Close()
	selected := map[string]bool{}
	for _, f := range opts.Files {
		if !fs.ValidPath(f) || !strings.HasSuffix(f, ".md") || selected[f] {
			p.diagnostic("invalid_selection", f, "selection must contain unique relative .md paths")
		}
		selected[f] = true
	}
	var names []string
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if strings.HasPrefix(strings.ToLower(path.Base(name)), ".okf") {
			p.diagnostic("reserved_path", name, "reserved OKF path")
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			p.diagnostic("symlink", name, "symlinks are unsupported")
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			p.diagnostic("special_file", name, "special files are unsupported")
			return nil
		}
		if strings.HasSuffix(name, ".md") && (len(opts.Files) == 0 || selected[name]) {
			if len(names) < 1001 {
				names = append(names, name)
			}
		}
		return nil
	})
	if err != nil {
		return p, nil, err
	}
	sort.Strings(names)
	for f := range selected {
		i := sort.SearchStrings(names, f)
		if i == len(names) || names[i] != f {
			p.diagnostic("missing_selection", f, "selected file unavailable")
		}
	}
	if len(names) == 0 {
		p.diagnostic("empty_selection", "", "no Markdown selected")
	}
	if len(names) > 1000 {
		p.diagnostic("limit", "", "at most 1000 documents")
		names = nil
	}
	total := 0
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return p, nil, err
		}
		base := path.Base(name)
		if base == "index.md" || base == "log.md" || strings.HasPrefix(strings.ToLower(base), ".okf") {
			p.diagnostic("reserved_path", name, "reserved OKF path")
			continue
		}
		id, err := bundle.ParseConceptID(strings.TrimSuffix(name, ".md"))
		if err != nil {
			p.diagnostic("invalid_path", name, err.Error())
			continue
		}
		_ = id
		f, err := root.Open(name)
		if err != nil {
			return p, nil, err
		}
		b, readErr := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		err = errors.Join(readErr, f.Close())
		if err != nil {
			return p, nil, err
		}
		total += len(b)
		if len(b) > 2<<20 || total > 16<<20 {
			p.diagnostic("limit", name, "source byte limit exceeded")
			continue
		}
		doc, err := bundle.ParseDocumentContext(ctx, string(b))
		if err != nil {
			p.diagnostic("frontmatter", name, err.Error())
			continue
		}
		if duplicateYAMLKeys(doc.Frontmatter.YAMLNode(), map[*yaml.Node]bool{}) {
			p.diagnostic("frontmatter", name, "duplicate YAML mapping keys")
			continue
		}
		rendered := b
		if _, ok := doc.Frontmatter.Type(); !ok {
			if _, present := doc.Frontmatter.Get("type"); present {
				p.diagnostic("invalid_type", name, "existing type must be nonempty string")
				continue
			}
			if strings.TrimSpace(opts.Type) == "" {
				p.diagnostic("type_required", name, "choose explicit --type for missing type")
				continue
			}
			scalar, _ := yaml.Marshal(opts.Type)
			// Insertion preserves all original YAML/body bytes, including comments and CRLF.
			line := "type: " + string(scalar)
			if doc.HasFrontmatter {
				i := strings.IndexByte(string(b), '\n')
				rendered = []byte(string(b[:i+1]) + line + string(b[i+1:]))
			} else {
				rendered = []byte("---\n" + line + "---\n" + string(b))
			}
		}
		p.Files = append(p.Files, File{name, hash(b), hash(rendered)})
		out[name] = rendered
	}
	for _, name := range names {
		b, ok := out[name]
		if !ok {
			continue
		}
		doc, _ := bundle.ParseDocumentContext(ctx, string(b))
		body := []byte(doc.Body)
		tree := goldmark.New().Parser().Parse(text.NewReader(body))
		err := ast.Walk(tree, func(node ast.Node, enter bool) (ast.WalkStatus, error) {
			if !enter {
				return ast.WalkContinue, nil
			}
			if err := ctx.Err(); err != nil {
				return ast.WalkStop, err
			}
			var dest []byte
			switch n := node.(type) {
			case *ast.Link:
				dest = n.Destination
			case *ast.Image:
				dest = n.Destination
			case *ast.AutoLink:
				dest = n.URL(body)
				if n.AutoLinkType == ast.AutoLinkEmail {
					dest = append([]byte("mailto:"), dest...)
				}
			case *ast.HTMLBlock, *ast.RawHTML:
				p.diagnostic("unsupported_html", name, "HTML may contain unmanaged links/assets")
			}
			if dest == nil {
				return ast.WalkContinue, nil
			}
			target := string(dest)
			u, e := url.Parse(target)
			if e != nil {
				p.diagnostic("unsupported_link", name, target)
				return ast.WalkContinue, nil
			}
			if u.Scheme != "" {
				if u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "mailto" {
					p.diagnostic("unsupported_link", name, target)
				}
				return ast.WalkContinue, nil
			}
			if u.Host != "" || u.Path == "" && u.Fragment != "" {
				return ast.WalkContinue, nil
			}
			resolved := path.Clean(path.Join(path.Dir(name), u.Path))
			if strings.HasPrefix(u.Path, "/") {
				resolved = path.Clean(strings.TrimPrefix(u.Path, "/"))
			}
			if u.RawQuery != "" || strings.HasSuffix(u.Path, "/") || out[resolved] == nil {
				p.diagnostic("unsupported_link", name, target)
			}
			return ast.WalkContinue, nil
		})
		if err != nil {
			return p, nil, err
		}
	}
	if len(p.Diagnostics) == 0 {
		dirs := map[string][]bundle.IndexEntry{}
		for _, f := range p.Files {
			doc, _ := bundle.ParseDocumentContext(ctx, string(out[f.Path]))
			typ, _ := doc.Frontmatter.Type()
			dir := path.Dir(f.Path)
			title := path.Base(f.Path)
			dirs[dir] = append(dirs[dir], bundle.IndexEntry{Type: typ, Title: title, Link: url.PathEscape(path.Base(f.Path))})
			for dir != "." {
				child := path.Base(dir)
				par := path.Dir(dir)
				found := false
				for _, e := range dirs[par] {
					if e.Link == url.PathEscape(child)+"/index.md" {
						found = true
					}
				}
				if !found {
					dirs[par] = append(dirs[par], bundle.IndexEntry{Type: "Directories", Title: child, Link: url.PathEscape(child) + "/index.md"})
				}
				dir = par
			}
		}
		for dir, entries := range dirs {
			prefix := ""
			if dir == "." {
				prefix = "---\nokf_version: \"0.2\"\n---\n\n"
			}
			out[path.Join(dir, "index.md")] = []byte(prefix + bundle.BuildIndexText(entries))
		}
		report, err := validator.ValidateSource(ctx, memorySource(out), &validator.ValidatorConfig{CheckLinks: true})
		if err != nil {
			return p, nil, err
		}
		if report.ErrorCount() > 0 {
			p.diagnostic("validation", "", fmt.Sprintf("bundle has %d conformance errors", report.ErrorCount()))
		}
	}
	sort.Slice(p.Diagnostics, func(i, j int) bool {
		a, b := p.Diagnostics[i], p.Diagnostics[j]
		return a.Path+"\x00"+a.Code+"\x00"+a.Message < b.Path+"\x00"+b.Code+"\x00"+b.Message
	})
	p.Applicable = len(p.Diagnostics) == 0
	encoded, _ := json.Marshal(p)
	p.Digest = hash(encoded)
	return p, out, ctx.Err()
}

func Apply(ctx context.Context, opts Options, digest string) (p Plan, resultErr error) {
	return apply(ctx, opts, digest, nil)
}

func apply(ctx context.Context, opts Options, digest string, beforePublish func() error) (p Plan, resultErr error) {
	p, out, err := prepare(ctx, opts)
	if err != nil {
		return p, err
	}
	if !p.Applicable {
		return p, fmt.Errorf("setup has blockers")
	}
	if digest == "" || digest != p.Digest {
		return p, fmt.Errorf("plan digest mismatch: preview again")
	}
	parent, err := os.OpenRoot(filepath.Dir(p.Options.Target))
	if err != nil {
		return p, err
	}
	defer parent.Close()
	random := make([]byte, 12)
	if _, err = rand.Read(random); err != nil {
		return p, err
	}
	stageName := ".okf-setup-" + hex.EncodeToString(random)
	if err = parent.Mkdir(stageName, 0755); err != nil {
		return p, err
	}
	published := false
	defer func() {
		if !published {
			resultErr = errors.Join(resultErr, parent.RemoveAll(stageName))
		}
	}()
	stage, err := parent.OpenRoot(stageName)
	if err != nil {
		return p, err
	}
	defer stage.Close()
	names := make([]string, 0, len(out))
	for name := range out {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err = ctx.Err(); err != nil {
			return p, err
		}
		if dir := path.Dir(name); dir != "." {
			if err = stage.MkdirAll(dir, 0755); err != nil {
				return p, err
			}
		}
		file, e := stage.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			return p, e
		}
		_, e = file.Write(out[name])
		if e == nil {
			e = file.Sync()
		}
		if e = errors.Join(e, file.Close()); e != nil {
			return p, e
		}
	}
	// Sync every nested directory before exposing the complete tree.
	dirs := map[string]bool{".": true}
	for _, name := range names {
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	for dir := range dirs {
		d, e := stage.OpenRoot(dir)
		if e != nil {
			return p, e
		}
		e = errors.Join(bundle.SyncPublicationDirectory(d), d.Close())
		if e != nil {
			return p, e
		}
	}
	if beforePublish != nil {
		if err = beforePublish(); err != nil {
			return p, err
		}
	}
	current, err := Preview(ctx, opts)
	if err != nil {
		return p, err
	}
	if current.Digest != digest {
		return p, fmt.Errorf("source drift: preview again")
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	if err = bundle.PublishNewDirectory(parent, stageName, filepath.Base(p.Options.Target)); err != nil {
		return p, err
	}
	published = true
	p.Published = true
	return p, bundle.SyncPublicationDirectory(parent)
}

func duplicateYAMLKeys(n *yaml.Node, seen map[*yaml.Node]bool) bool {
	if n == nil || seen[n] {
		return false
	}
	seen[n] = true
	if n.Kind == yaml.MappingNode {
		keys := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			id := k.Tag + "\x00" + k.Value
			if keys[id] {
				return true
			}
			keys[id] = true
		}
	}
	for _, c := range n.Content {
		if duplicateYAMLKeys(c, seen) {
			return true
		}
	}
	return duplicateYAMLKeys(n.Alias, seen)
}

type memorySource map[string][]byte

func (m memorySource) Paths(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}
func (m memorySource) ReadFile(ctx context.Context, n string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, ok := m[n]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}
