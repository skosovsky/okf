// Package viewer exports self-contained read-only OKF HTML.
package viewer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/markdownowner"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

//go:embed assets/*
var assets embed.FS

const ExporterVersion = "1"
const DefaultMaxNodes = 10000

type Options struct {
	AsOf            *time.Time
	MaxNodes        int
	Overwrite       bool
	TemporalProfile bundle.TemporalProfile
	VersionSelector string
}
type Projection struct {
	ExporterVersion string    `json:"exporter_version"`
	SpecVersion     string    `json:"spec_version"`
	SpecRevision    string    `json:"spec_revision"`
	ReferenceTime   string    `json:"reference_time,omitempty"`
	ReferenceBasis  string    `json:"reference_basis"`
	Concepts        []Concept `json:"concepts"`
	Edges           []Edge    `json:"edges"`
}
type Concept struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	HTML       string   `json:"html"`
	SearchText string   `json:"search_text"`
	Trust      string   `json:"trust"`
	Status     string   `json:"status"`
	Staleness  string   `json:"staleness"`
	StaleAfter string   `json:"stale_after,omitempty"`
	Sources    []Source `json:"sources"`
}
type Source struct {
	ID       string `json:"id,omitempty"`
	Title    string `json:"title,omitempty"`
	Resource string `json:"resource"`
}
type Edge struct {
	From        string `json:"from"`
	To          string `json:"to"`
	FromConcept string `json:"from_concept"`
	ToConcept   string `json:"to_concept"`
	Kind        string `json:"kind"`
	Exists      bool   `json:"exists"`
	Raw         string `json:"raw,omitempty"`
}

// Build reuses bundle's typed relation and lifecycle APIs.
func Build(ctx context.Context, b *bundle.Bundle, options Options) (Projection, error) {
	if err := ctx.Err(); err != nil {
		return Projection{}, err
	}
	if b == nil {
		return Projection{}, errors.New("nil bundle")
	}
	resolution, err := b.VersionResolutionContext(ctx, options.VersionSelector)
	if err != nil {
		return Projection{}, err
	}
	profile := options.TemporalProfile
	if profile == "" {
		profile = bundle.TemporalProfileDate
	}
	profile, err = bundle.NormalizeTemporalProfile(profile)
	if err != nil {
		return Projection{}, err
	}
	concepts := b.Concepts()
	limit := options.MaxNodes
	if limit == 0 {
		limit = DefaultMaxNodes
	}
	if limit < 0 {
		return Projection{}, errors.New("max nodes must be positive")
	}
	if len(concepts) > limit {
		return Projection{}, fmt.Errorf("bundle has %d concepts; exceeds --max-nodes=%d", len(concepts), limit)
	}
	p := Projection{ExporterVersion: ExporterVersion, SpecVersion: resolution.Effective, SpecRevision: string(profile), ReferenceBasis: "unevaluated", Concepts: make([]Concept, 0, len(concepts)), Edges: make([]Edge, 0)}
	if resolution.Effective == bundle.LegacyOKFVersion {
		p.SpecRevision = "legacy-v0.1"
	}
	if options.AsOf != nil {
		p.ReferenceBasis = "explicit-instant"
		p.ReferenceTime = options.AsOf.Format(time.RFC3339)
		if profile == bundle.TemporalProfileDate {
			p.ReferenceBasis = "explicit-civil-date"
			p.ReferenceTime = options.AsOf.Format(time.DateOnly)
		}
	}
	for _, c := range concepts {
		if err := ctx.Err(); err != nil {
			return Projection{}, err
		}
		id := c.ID.String()
		title, _ := c.Document.Frontmatter.Title()
		if title == "" {
			title = id
		}
		typ, _ := c.Document.Frontmatter.Type()
		var rendered bytes.Buffer
		if err := safeMarkdown.Convert([]byte(c.Document.Body), &rendered); err != nil {
			return Projection{}, fmt.Errorf("render %s: %w", id, err)
		}
		trust, err := c.Document.TrustTierContext(ctx)
		if err != nil {
			return Projection{}, err
		}
		state, err := c.Document.StatusStateContext(ctx)
		if err != nil {
			return Projection{}, err
		}
		status := state.Effective
		if !state.Valid {
			status = "unresolved"
		}
		concept := Concept{ID: id, Title: title, Type: typ, HTML: rendered.String(), SearchText: c.Document.Body, Trust: string(trust), Status: status, Staleness: "unevaluated", Sources: []Source{}}
		stale, err := c.Document.Frontmatter.StaleAfterForProfileContext(ctx, profile)
		if err != nil {
			return Projection{}, err
		}
		if stale.Value.State == bundle.TemporalValid {
			concept.StaleAfter = stale.Value.Raw
			if options.AsOf != nil {
				yes, evaluated, err := c.Document.Frontmatter.IsStaleForProfileContext(ctx, *options.AsOf, profile)
				if err != nil {
					return Projection{}, err
				}
				if !evaluated {
					concept.Staleness = "unevaluated"
				} else if yes {
					concept.Staleness = "stale"
				} else {
					concept.Staleness = "fresh"
				}
			}
		} else if stale.Value.State == bundle.TemporalMalformed {
			concept.Staleness = "invalid"
		}
		sourceStates, err := c.Document.Frontmatter.SourceStatesForProfileContext(ctx, profile)
		if err != nil {
			return Projection{}, err
		}
		for _, s := range sourceStates {
			if s.Valid && s.Source.Resource.Valid {
				concept.Sources = append(concept.Sources, Source{ID: s.Value.ID, Title: s.Value.Title, Resource: s.Value.Resource})
			}
		}
		fallback, err := c.Document.LegacyFallbackObservationContext(ctx)
		if err != nil {
			return Projection{}, err
		}
		if fallback.CitationsActive {
			citations, err := markdownowner.CollectCitationSectionProjection(ctx, []byte(c.Document.Body))
			if err != nil {
				return Projection{}, err
			}
			for _, citation := range citations.Entries {
				if err := ctx.Err(); err != nil {
					return Projection{}, err
				}
				title := citation.Title
				if title == "" {
					title = citation.Raw
				}
				concept.Sources = append(concept.Sources, Source{Title: title, Resource: citation.Resource})
			}
		}
		p.Concepts = append(p.Concepts, concept)
		links, err := b.LinksFromContext(ctx, c.ID)
		if err != nil {
			return Projection{}, err
		}
		for _, link := range links {
			p.Edges = append(p.Edges, Edge{From: id, To: link.Target.String(), FromConcept: id, ToConcept: link.Target.String(), Kind: "markdown", Exists: link.Exists, Raw: link.Raw})
		}
		relations, err := b.DeclaredSemanticLinksFromContext(ctx, c.ID)
		if err != nil {
			return Projection{}, err
		}
		for _, rel := range relations {
			p.Edges = append(p.Edges, Edge{From: rel.Source.String(), To: rel.Target.String(), FromConcept: rel.Source.ID.String(), ToConcept: rel.Target.ID.String(), Kind: rel.Type, Exists: rel.TargetExists})
		}
	}
	sort.Slice(p.Concepts, func(i, j int) bool { return p.Concepts[i].ID < p.Concepts[j].ID })
	sort.SliceStable(p.Edges, func(i, j int) bool {
		a, b := p.Edges[i], p.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	return p, nil
}

type safeImageRenderer struct{}

func (safeImageRenderer) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindImage, func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, _ = w.WriteString("[image omitted]")
		}
		return ast.WalkSkipChildren, nil
	})
}

var safeMarkdown = goldmark.New(goldmark.WithExtensions(extension.Footnote), goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(safeImageRenderer{}, 1000))))

// Render encodes JSON so input cannot close its script element.
func Render(ctx context.Context, p Projection) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	css, err := assets.ReadFile("assets/viewer.css")
	if err != nil {
		return nil, err
	}
	js, err := assets.ReadFile("assets/viewer.js")
	if err != nil {
		return nil, err
	}
	jsDigest := sha256.Sum256(js)
	jsPolicy := "'sha256-" + base64.StdEncoding.EncodeToString(jsDigest[:]) + "'"
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><meta name=\"referrer\" content=\"no-referrer\"><meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; style-src 'unsafe-inline'; script-src ")
	out.WriteString(jsPolicy)
	out.WriteString("; img-src 'none'; connect-src 'none'; font-src 'none'; base-uri 'none'; form-action 'none'\"><title>OKF knowledge viewer</title><style>")
	out.Write(css)
	out.WriteString("</style></head><body><header><h1>OKF knowledge viewer</h1><span id=\"summary\"></span></header><main><aside><label>Search <input id=\"search\" type=\"search\" autocomplete=\"off\"></label><label>Type <select id=\"type\"><option value=\"\">All types</option></select></label><label><input id=\"show-graph\" type=\"checkbox\"> Show graph</label><div id=\"list\" role=\"list\"></div></aside><section id=\"detail\" aria-live=\"polite\"></section></main><script id=\"okf-data\" type=\"application/json\">")
	out.Write(data)
	out.WriteString("</script><script>")
	out.Write(js)
	out.WriteString("</script></body></html>")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Export publishes via a sibling temporary file, never truncating an old file.
func Export(ctx context.Context, b *bundle.Bundle, output string, options Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if output == "" {
		return errors.New("output path is required")
	}
	absolute, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := guardOutput(b, absolute, options.Overwrite); err != nil {
		return err
	}
	p, err := Build(ctx, b, options)
	if err != nil {
		return err
	}
	htmlBytes, err := Render(ctx, p)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(absolute), ".okf-view-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(htmlBytes); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if options.Overwrite {
		if err := guardOutput(b, absolute, true); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), absolute)
	}
	if err := guardOutput(b, absolute, false); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), absolute); err != nil {
		return err
	}
	return nil
}
func guardOutput(b *bundle.Bundle, output string, overwrite bool) error {
	if b == nil {
		return errors.New("nil bundle")
	}
	root, err := filepath.Abs(b.Root())
	if err != nil {
		return err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(output))
	if err != nil {
		return err
	}
	resolvedOutput := filepath.Join(resolvedParent, filepath.Base(output))
	rel, err := filepath.Rel(resolvedRoot, resolvedOutput)
	if err != nil {
		return err
	}
	if rel == "." {
		return errors.New("output path is bundle root")
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		slash := filepath.ToSlash(rel)
		if slash == ".okf" || strings.HasPrefix(slash, ".okf/") {
			return errors.New("output under .okf store is forbidden")
		}
		if strings.HasSuffix(strings.ToLower(slash), ".md") {
			return errors.New("output may not overwrite bundle markdown")
		}
	}
	// The store directory may itself be a symlink; protect its resolved target.
	store := filepath.Join(root, ".okf")
	if _, err := os.Lstat(store); err == nil {
		resolvedStore, err := filepath.EvalSymlinks(store)
		if err != nil {
			return err
		}
		storeRel, err := filepath.Rel(resolvedStore, resolvedOutput)
		if err != nil {
			return err
		}
		if storeRel == "." || storeRel != ".." && !strings.HasPrefix(storeRel, ".."+string(os.PathSeparator)) {
			return errors.New("output under .okf store is forbidden")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if st, err := os.Lstat(output); err == nil {
		if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			return errors.New("output is not a regular file")
		}
		if !overwrite {
			return errors.New("output exists; use --overwrite")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
