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
	"html"
	"net/http"
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
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

//go:embed assets/*
var assets embed.FS

const ExporterVersion = "1"
const DefaultMaxNodes = 10000

type Options struct {
	// Language selects en or ru; the empty value defaults to en.
	Language        string
	AsOf            *time.Time
	MaxNodes        int
	Overwrite       bool
	TemporalProfile bundle.TemporalProfile
	VersionSelector string
}

// RenderOptions selects presentation without changing the semantic projection.
type RenderOptions struct{ Language string }

func normalizeLanguage(language string) (string, error) {
	if language == "" {
		return "en", nil
	}
	if language != "en" && language != "ru" {
		return "", fmt.Errorf("unsupported viewer language %q (want en or ru)", language)
	}
	return language, nil
}

type Projection struct {
	BundleMetadata  *bundle.BundleMetadata `json:"bundle_metadata,omitempty"`
	ExporterVersion string                 `json:"exporter_version"`
	SpecVersion     string                 `json:"spec_version"`
	SpecRevision    string                 `json:"spec_revision"`
	ReferenceTime   string                 `json:"reference_time,omitempty"`
	ReferenceBasis  string                 `json:"reference_basis"`
	Concepts        []Concept              `json:"concepts"`
	Edges           []Edge                 `json:"edges"`
}
type Concept struct {
	ID                           string         `json:"id"`
	Title                        string         `json:"title"`
	Type                         string         `json:"type"`
	HTML                         string         `json:"html"`
	SearchText                   string         `json:"search_text"`
	Trust                        string         `json:"trust"`
	Status                       string         `json:"status"`
	StatusRaw                    string         `json:"status_raw,omitempty"`
	Staleness                    string         `json:"staleness"`
	StaleAfter                   string         `json:"stale_after,omitempty"`
	Sources                      []Source       `json:"sources"`
	Verifications                []Verification `json:"verifications,omitempty"`
	Replacement                  string         `json:"replacement,omitempty"`
	Attestable                   *bool          `json:"attestable,omitempty"`
	ComputationUnavailableReason string         `json:"computation_unavailable_reason,omitempty"`
}
type Verification struct {
	By      string `json:"by"`
	At      string `json:"at"`
	Current bool   `json:"current"`
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
	b, err = b.WithVersionResolutionContext(ctx, resolution)
	if err != nil {
		return Projection{}, err
	}
	profile := options.TemporalProfile
	if profile == "" {
		profile = bundle.TemporalProfileDate
	}
	profile, err = bundle.NormalizeTemporalProfile(bundle.TemporalProfileForVersion(resolution, profile))
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
	metadata, err := b.BundleMetadataContext(ctx, resolution)
	if err != nil {
		return Projection{}, err
	}
	p := Projection{BundleMetadata: metadata, ExporterVersion: ExporterVersion, SpecVersion: resolution.Effective, SpecRevision: string(profile), ReferenceBasis: "unevaluated", Concepts: make([]Concept, 0, len(concepts)), Edges: make([]Edge, 0)}
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
	markdown := safeMarkdown
	images := offlineImages{ctx: ctx, bundle: b}
	if resolution.Effective == bundle.OKFVersion03 {
		markdown = goldmark.New(
			goldmark.WithExtensions(extension.Footnote, extension.Table),
			// The source fallback runs after Goldmark's authored footnotes.
			goldmark.WithParserOptions(parser.WithInlineParsers(util.Prioritized(sourceFootnoteParser{}, 102))),
			goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(safeImageRenderer{dataURI: images.dataURI}, 500))),
		)
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
		state, err := c.Document.StatusStateForVersionContext(ctx, resolution)
		if err != nil {
			return Projection{}, err
		}
		status := state.Effective
		if !state.Valid {
			status = "unresolved"
		}
		concept := Concept{ID: id, Title: title, Type: typ, SearchText: c.Document.Body, Status: status, Staleness: "unevaluated", Sources: []Source{}}
		if resolution.Effective == bundle.OKFVersion03 {
			switch state.Effective {
			case bundle.StatusStable, bundle.StatusDraft, bundle.StatusDeprecated:
				// The viewer localizes the standard vocabulary.
			default:
				concept.StatusRaw = state.Raw
			}
			trust, err := c.Document.Frontmatter.TrustStateV03Context(ctx)
			if err != nil {
				return Projection{}, err
			}
			concept.Trust = string(trust.Tier)
			for _, event := range trust.Verifications {
				if event.Verification.Valid {
					value := event.Verification.Value
					concept.Verifications = append(concept.Verifications, Verification{By: value.By, At: value.At, Current: event.Current})
				}
			}
		} else {
			trust, err := c.Document.TrustTierContext(ctx)
			if err != nil {
				return Projection{}, err
			}
			concept.Trust = string(trust)
		}
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
		var rendered bytes.Buffer
		images.documentPath = c.Path
		body := []byte(c.Document.Body)
		if resolution.Effective == bundle.OKFVersion03 && len(concept.Sources) > 0 && bytes.Contains(body, []byte("[^")) {
			err = renderSourceFootnotes(ctx, markdown, body, concept.Sources, &rendered)
		} else {
			err = markdown.Convert(body, &rendered)
		}
		if err != nil {
			return Projection{}, fmt.Errorf("render %s: %w", id, err)
		}
		concept.HTML = rendered.String()
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
		if resolution.Effective == bundle.OKFVersion03 && status == bundle.StatusDeprecated {
			replacement, state, err := b.ResolveCurrentConceptContext(ctx, c.ID)
			if err != nil {
				return Projection{}, err
			}
			if state == bundle.ReplacementCurrent {
				concept.Replacement = replacement.String()
			}
		}
		if resolution.Effective == bundle.OKFVersion03 && typ == bundle.AttestedComputationType {
			computation, err := b.ComputationStateContext(ctx, c)
			if err != nil {
				return Projection{}, err
			}
			concept.Attestable = &computation.Attestable
			concept.ComputationUnavailableReason = computation.UnavailableReason
		}
		p.Concepts = append(p.Concepts, concept)
		links, err := b.LinksFromContext(ctx, c.ID)
		if err != nil {
			return Projection{}, err
		}
		for _, link := range links {
			p.Edges = append(p.Edges, Edge{From: id, To: link.Target.String(), FromConcept: id, ToConcept: link.Target.String(), Kind: "markdown", Exists: link.Exists, Raw: link.Raw})
		}
		pathEdges, err := b.PathConceptEdgesContext(ctx, c.ID)
		if err != nil {
			return Projection{}, err
		}
		for _, edge := range pathEdges {
			target := edge.Target.String()
			p.Edges = append(p.Edges, Edge{From: id, To: target, FromConcept: id, ToConcept: target, Kind: edge.FieldPath, Exists: true, Raw: edge.Raw})
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

type safeImageRenderer struct {
	dataURI func(destination string) (string, error)
}

func (images safeImageRenderer) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindImage, func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if images.dataURI != nil {
			image := n.(*ast.Image)
			uri, err := images.dataURI(string(image.Destination))
			if err != nil {
				return ast.WalkStop, err
			}
			if uri != "" {
				_, err = fmt.Fprintf(w, `<img src="%s" alt="%s">`, uri, html.EscapeString(string(image.Text(source))))
				return ast.WalkSkipChildren, err
			}
		}
		_, err := w.WriteString("[image omitted]")
		return ast.WalkSkipChildren, err
	})
}

var safeMarkdown = goldmark.New(goldmark.WithExtensions(extension.Footnote), goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(safeImageRenderer{}, 500))))

type offlineImages struct {
	ctx          context.Context
	bundle       *bundle.Bundle
	documentPath string
	bytes        uint64
}

func (images *offlineImages) dataURI(destination string) (string, error) {
	target, _, err := images.bundle.ResolveMarkdownTargetV03Context(images.ctx, images.documentPath, destination)
	if err != nil || !target.Exists || strings.HasSuffix(target.Path, ".md") {
		return "", err
	}
	size, _, ok := images.bundle.CapturedFileMetadata(target.Path)
	if !ok || size > 1<<20 || images.bytes+size > 8<<20 {
		return "", nil
	}
	data, found, err := images.bundle.ReadFileContext(images.ctx, target.Path)
	if err != nil || !found {
		return "", err
	}
	mediaType := http.DetectContentType(data)
	switch mediaType {
	case "image/gif", "image/jpeg", "image/png", "image/webp":
		images.bytes += size
		return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
	default:
		return "", nil
	}
}

// Render encodes JSON so input cannot close its script element.
func Render(ctx context.Context, p Projection) ([]byte, error) {
	return RenderWithOptions(ctx, p, RenderOptions{})
}

// RenderWithOptions renders an offline viewer in the selected interface language.
func RenderWithOptions(ctx context.Context, p Projection, options RenderOptions) ([]byte, error) {
	language, err := normalizeLanguage(options.Language)
	if err != nil {
		return nil, err
	}
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
	ui := map[string]string{"title": "OKF knowledge viewer", "search": "Search", "type": "Type", "all": "All types", "graph": "Show graph", "language": "Language"}
	if language == "ru" {
		ui = map[string]string{"title": "Просмотр знаний OKF", "search": "Поиск", "type": "Тип", "all": "Все типы", "graph": "Показать граф", "language": "Язык"}
	}
	enSelected, ruSelected := " selected", ""
	if language == "ru" {
		enSelected, ruSelected = "", " selected"
	}
	var out bytes.Buffer
	out.WriteString("<!doctype html><html lang=\"" + language + "\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><meta name=\"referrer\" content=\"no-referrer\"><meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; style-src 'unsafe-inline'; script-src ")
	out.WriteString(jsPolicy)
	imagePolicy := "'none'"
	if p.SpecVersion == bundle.OKFVersion03 {
		imagePolicy = "data:"
	}
	out.WriteString("; img-src " + imagePolicy + "; connect-src 'none'; font-src 'none'; base-uri 'none'; form-action 'none'\"><title>" + ui["title"] + "</title><style>")
	out.Write(css)
	out.WriteString("</style></head><body><header><h1 id=\"viewer-title\">" + ui["title"] + "</h1><span id=\"summary\"></span></header><main><aside><label><span id=\"search-label\">" + ui["search"] + "</span> <input id=\"search\" type=\"search\" autocomplete=\"off\"></label><label><span id=\"type-label\">" + ui["type"] + "</span> <select id=\"type\"><option id=\"all-types\" value=\"\">" + ui["all"] + "</option></select></label><label><input id=\"show-graph\" type=\"checkbox\"> <span id=\"graph-label\">" + ui["graph"] + "</span></label><label><span id=\"language-label\">" + ui["language"] + "</span><select id=\"language\"><option value=\"en\"" + enSelected + ">English</option><option value=\"ru\"" + ruSelected + ">Русский</option></select></label><div id=\"list\" role=\"list\"></div></aside><section id=\"detail\" aria-live=\"polite\"></section></main><script id=\"okf-data\" type=\"application/json\">")
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
	if _, err := normalizeLanguage(options.Language); err != nil {
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
	htmlBytes, err := RenderWithOptions(ctx, p, RenderOptions{Language: options.Language})
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
