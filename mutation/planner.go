package mutation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
	"gopkg.in/yaml.v3"
)

// Result is a complete, non-authorizing in-memory preview. Staged is safe to
// load as a bundle.Source; it must not be mutated after Plan returns.
type Result struct {
	Preview             store.Preview
	Validation          validator.Report
	RelationDiagnostics []bundle.RelationDiagnostic
	Staged              bundle.Source
}

// Planner controls staged validation. Nil config uses validator defaults.
type Planner struct{ ValidatorConfig *validator.ValidatorConfig }

func NewPlanner(cfg *validator.ValidatorConfig) Planner { return Planner{ValidatorConfig: cfg} }

// trackedSource records the base files a planner actually consumes. It keeps
// the optional manifest contract so tracking does not turn a captured snapshot
// into an expensive re-hash.
type trackedSource struct {
	source bundle.Source
	paths  map[string]struct{}
}

func newTrackedSource(source bundle.Source) *trackedSource {
	tracked := &trackedSource{source: source, paths: make(map[string]struct{})}
	// A valid manifest is the complete dependency set of the revision fast
	// path. Keep it even when computing the revision needs no ReadFile calls.
	if source, ok := source.(store.ManifestSource); ok {
		manifest := source.Manifest()
		if manifest.Valid() {
			for name := range manifest.Digests() {
				tracked.paths[name] = struct{}{}
			}
		}
	}
	return tracked
}

func (s *trackedSource) Paths(ctx context.Context) ([]string, error) { return s.source.Paths(ctx) }
func (s *trackedSource) ReadFile(ctx context.Context, name string) ([]byte, error) {
	s.paths[name] = struct{}{}
	return s.source.ReadFile(ctx, name)
}
func (s *trackedSource) Manifest() store.Manifest {
	if source, ok := s.source.(store.ManifestSource); ok {
		return source.Manifest()
	}
	return store.Manifest{}
}
func (s *trackedSource) reads() []store.Read {
	paths := make([]string, 0, len(s.paths))
	for name := range s.paths {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	out := make([]store.Read, len(paths))
	for i, name := range paths {
		out[i] = store.Read{Path: name}
	}
	return out
}

// Plan applies change declaratively to source, without filesystem I/O.
func (p Planner) Plan(ctx context.Context, source bundle.Source, change store.ChangeSet) (Result, error) {
	if source == nil {
		return Result{}, invalid("nil_source", nil, errors.New("nil source"))
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := change.Validate(); err != nil {
		return Result{}, invalid("invalid_change_set", nil, err)
	}
	tracked := newTrackedSource(source)
	base, err := revision(ctx, tracked)
	if err != nil {
		return Result{}, err
	}
	if change.BaseRevision != base {
		return Result{}, &store.Conflict{Expected: change.BaseRevision, Actual: base, ChangedRefs: availableSemanticRefs(ctx, tracked), Retryable: true}
	}
	b, err := bundle.Load(ctx, tracked)
	if err != nil {
		return Result{}, err
	}
	if err := checkPreconditions(ctx, tracked, b, base, change.Preconditions); err != nil {
		return Result{}, err
	}
	o := NewOverlay(tracked)
	state := &planState{ctx: ctx, overlay: o, bundle: b, readPaths: tracked}
	for _, op := range change.Operations {
		candidate := state.clone()
		if err := candidate.apply(op); err != nil {
			return Result{}, err
		}
		b, err = bundle.Load(ctx, candidate.overlay)
		if err != nil {
			return Result{}, err
		}
		candidate.bundle = b
		state = candidate
	}
	resultRevision, err := revision(ctx, state.overlay)
	if err != nil {
		return Result{}, err
	}
	validation, err := validator.ValidateSource(ctx, state.overlay, p.ValidatorConfig)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	relationDiagnostics := b.RelationDiagnostics()
	preview := state.preview(base, resultRevision)
	preview.Diagnostics = previewDiagnostics(validation, relationDiagnostics)
	result := Result{Preview: preview, Validation: validation, RelationDiagnostics: append([]bundle.RelationDiagnostic(nil), relationDiagnostics...), Staged: state.overlay}
	blockingRelationDiagnostics := blockingRelationDiagnostics(relationDiagnostics)
	if !validation.IsConformant() || len(blockingRelationDiagnostics) != 0 {
		// The structured error is the error-side representation of the public
		// diagnostic preview. Do not project semantic diagnostics again: that
		// loses their file, severity, relation type, and raw target.
		d := preview.Diagnostics
		// An invalid plan intentionally exposes only dependency evidence and
		// diagnostics: no writes, renames, or staged source can be mistaken for
		// a commit-ready plan.
		diagnosticPreview := store.Preview{BaseRevision: base, ResultRevision: resultRevision, Reads: preview.Reads, Diagnostics: preview.Diagnostics}
		return Result{Preview: diagnosticPreview, Validation: validation, RelationDiagnostics: relationDiagnostics}, invalid("staged_validation_failed", d, errors.New("staged bundle is invalid"))
	}
	return result, nil
}

// availableSemanticRefs returns the complete current semantic namespace when
// Plan can observe only the actual source, not the caller's expected snapshot.
// Reporting that conservative set prevents a revision-only CAS conflict from
// hiding a changed relation, fragment, move, or deleted concept.
func availableSemanticRefs(ctx context.Context, source bundle.Source) []bundle.RelationRef {
	b, err := bundle.Load(ctx, source)
	if err != nil {
		return nil
	}
	seen := make(map[string]bundle.RelationRef)
	add := func(ref bundle.RelationRef) {
		if ref.String() != "" {
			seen[ref.String()] = ref
		}
	}
	for _, concept := range b.Concepts() {
		add(bundle.RelationRef{ID: concept.ID})
		for _, fragment := range b.Subresources(concept.ID) {
			add(bundle.RelationRef{ID: concept.ID, Fragment: fragment})
		}
		for _, relation := range b.SemanticLinksFrom(concept.ID) {
			add(relation.Source)
			add(relation.Target)
		}
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func blockingRelationDiagnostics(in []bundle.RelationDiagnostic) []bundle.RelationDiagnostic {
	out := make([]bundle.RelationDiagnostic, 0, len(in))
	for _, diagnostic := range in {
		if diagnostic.BlocksMutation() {
			out = append(out, diagnostic)
		}
	}
	return out
}

// Preview is an alias for Plan.
func (p Planner) Preview(ctx context.Context, source bundle.Source, change store.ChangeSet) (Result, error) {
	return p.Plan(ctx, source, change)
}
func Plan(ctx context.Context, source bundle.Source, change store.ChangeSet) (Result, error) {
	return Planner{}.Plan(ctx, source, change)
}

type planState struct {
	ctx               context.Context
	overlay           *Overlay
	bundle            *bundle.Bundle
	affected, reverse []bundle.RelationRef
	readPaths         *trackedSource
	plans             []store.OperationPlan
	renames           []store.Rename
}

func (s *planState) clone() *planState {
	cloned := *s
	cloned.overlay = s.overlay.clone()
	cloned.affected = append([]bundle.RelationRef(nil), s.affected...)
	cloned.reverse = append([]bundle.RelationRef(nil), s.reverse...)
	cloned.plans = append([]store.OperationPlan(nil), s.plans...)
	cloned.renames = append([]store.Rename(nil), s.renames...)
	return &cloned
}

func (s *planState) apply(operation store.Operation) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	switch op := operation.(type) {
	case store.EnsureRelation:
		return s.ensure(op)
	case store.MoveConcept:
		return s.move(op)
	case store.RenameFragment:
		return s.renameFragment(op)
	default:
		return invalid("unknown_operation", nil, fmt.Errorf("unsupported operation %T", operation))
	}
}

func (s *planState) ensure(op store.EnsureRelation) error {
	if !s.bundle.TargetExists(op.Source) || !s.bundle.TargetExists(op.Target) {
		return invalid("missing_relation_endpoint", []store.Diagnostic{{Code: "missing_relation_endpoint", Refs: []bundle.RelationRef{op.Source, op.Target}}}, errors.New("relation endpoint does not exist"))
	}
	c, ok := s.bundle.Get(op.Source.ID)
	if !ok {
		return invalid("missing_source", nil, errors.New("source missing"))
	}
	data := []byte(readBundleFile(s.bundle, c.Path))
	p, err := parsePresentation(data)
	if err != nil {
		return invalid("ambiguous_relation_presentation", []store.Diagnostic{{Code: "ambiguous_relation_presentation", Refs: []bundle.RelationRef{op.Source, op.Target}}}, err)
	}
	if mappingForRef(p.root, op.Source) == nil {
		return invalid("missing_source_fragment", []store.Diagnostic{{Code: "missing_source_fragment", Refs: []bundle.RelationRef{op.Source}}}, errors.New("fragment missing"))
	}
	updated, err := ensureRelationPresentation(data, op.Source, op.Type, op.Target.String())
	if err != nil {
		if errors.Is(err, errLossyRelationDeduplication) {
			return invalid("lossy_relation_deduplication", []store.Diagnostic{{Code: "lossy_relation_deduplication", Refs: []bundle.RelationRef{op.Source, op.Target}}}, err)
		}
		return invalid("ambiguous_relation_presentation", []store.Diagnostic{{Code: "ambiguous_relation_presentation", Refs: []bundle.RelationRef{op.Source, op.Target}}}, err)
	}
	if !bytes.Equal(updated, data) {
		if err := s.overlay.PutContext(s.ctx, c.Path, updated); err != nil {
			return err
		}
	}
	s.add(op.Source, op.Target)
	s.plans = append(s.plans, store.OperationPlan{Operation: op, AffectedRefs: []bundle.RelationRef{op.Source, op.Target}, Details: []string{"relation present exactly once"}})
	return nil
}

func (s *planState) move(op store.MoveConcept) error {
	c, ok := s.bundle.Get(op.From)
	if !ok {
		return invalid("missing_concept", []store.Diagnostic{{Code: "missing_concept", Refs: []bundle.RelationRef{{ID: op.From}}}}, errors.New("concept missing"))
	}
	if s.bundle.Contains(op.To) {
		return invalid("target_concept_exists", nil, errors.New("move target exists"))
	}
	fromPath, toPath := c.Path, conceptPath(op.To)
	if err := s.overlay.Rename(s.ctx, fromPath, toPath); err != nil {
		return err
	}
	s.renames = append(s.renames, store.Rename{From: fromPath, To: toPath})
	s.add(bundle.RelationRef{ID: op.From}, bundle.RelationRef{ID: op.To})
	for _, rel := range s.bundle.ReverseImpactConcept(op.From) {
		s.reverse = append(s.reverse, rel.Source)
	}
	// Rewrite canonical semantic target refs in frontmatter, then rewrite every
	// Markdown destination which resolves to the moved document.
	for _, file := range s.bundle.MarkdownFiles() {
		data := readBundleFile(s.bundle, file)
		if bytes.HasPrefix([]byte(data), []byte("---")) {
			p, err := parsePresentation([]byte(data))
			if err != nil {
				return presentationInvalid(err)
			}
			patches, err := relationTargetPatches(p, op.From, op.To, "", "")
			if err != nil {
				return presentationInvalid(err)
			}
			updated, err := p.patchYAML(patches)
			if err != nil {
				return presentationInvalid(err)
			}
			if len(patches) > 0 {
				out := file
				if out == fromPath {
					out = toPath
				}
				if err := s.overlay.PutContext(s.ctx, out, updated); err != nil {
					return err
				}
			}
		}
		out := file
		if out == fromPath {
			out = toPath
		}
		current := data
		if staged, readErr := s.overlay.ReadFile(s.ctx, out); readErr == nil {
			current = string(staged)
		}
		rewritten := rewriteMarkdownLinks(current, out, fromPath, toPath)
		if rewritten != current {
			if err := s.overlay.PutContext(s.ctx, out, []byte(rewritten)); err != nil {
				return err
			}
		}
	}
	s.plans = append(s.plans, store.OperationPlan{Operation: op, AffectedRefs: []bundle.RelationRef{{ID: op.From}, {ID: op.To}}, Details: []string{"moved concept and rewrote canonical references"}})
	return nil
}

func (s *planState) renameFragment(op store.RenameFragment) error {
	if !s.bundle.FragmentExists(op.Concept, op.From) {
		return invalid("missing_or_ambiguous_fragment", []store.Diagnostic{{Code: "missing_or_ambiguous_fragment", Refs: []bundle.RelationRef{{ID: op.Concept, Fragment: op.From}}}}, errors.New("fragment missing or ambiguous"))
	}
	if s.bundle.FragmentExists(op.Concept, op.To) {
		return invalid("target_fragment_exists", nil, errors.New("target fragment exists"))
	}
	c, _ := s.bundle.Get(op.Concept)
	data := readBundleFile(s.bundle, c.Path)
	p, err := parsePresentation([]byte(data))
	if err != nil {
		return presentationInvalid(err)
	}
	fragment, found, err := fragmentPatch(p, op.From, op.To)
	if err != nil {
		return presentationInvalid(err)
	}
	if !found {
		return invalid("missing_fragment", nil, errors.New("fragment not found"))
	}
	patches, err := relationTargetPatches(p, op.Concept, op.Concept, op.From, op.To)
	if err != nil {
		return presentationInvalid(err)
	}
	patches = append(patches, fragment)
	updated, err := p.patchYAML(patches)
	if err != nil {
		return presentationInvalid(err)
	}
	if err := s.overlay.PutContext(s.ctx, c.Path, updated); err != nil {
		return err
	}
	for _, rel := range s.bundle.ReverseImpactFragment(op.Concept, op.From) {
		s.reverse = append(s.reverse, rel.Source)
	}
	for _, file := range s.bundle.MarkdownFiles() {
		if file == c.Path {
			continue
		}
		data := readBundleFile(s.bundle, file)
		if !bytes.HasPrefix([]byte(data), []byte("---")) {
			continue
		}
		p, err := parsePresentation([]byte(data))
		if err != nil {
			return presentationInvalid(err)
		}
		patches, err := relationTargetPatches(p, op.Concept, op.Concept, op.From, op.To)
		if err != nil {
			return presentationInvalid(err)
		}
		if len(patches) > 0 {
			updated, err := p.patchYAML(patches)
			if err != nil {
				return presentationInvalid(err)
			}
			if err := s.overlay.PutContext(s.ctx, file, updated); err != nil {
				return err
			}
		}
	}
	s.add(bundle.RelationRef{ID: op.Concept, Fragment: op.From}, bundle.RelationRef{ID: op.Concept, Fragment: op.To})
	s.plans = append(s.plans, store.OperationPlan{Operation: op, AffectedRefs: []bundle.RelationRef{{ID: op.Concept, Fragment: op.From}, {ID: op.Concept, Fragment: op.To}}, Details: []string{"renamed canonical fragment and incoming references"}})
	return nil
}

func (s *planState) add(refs ...bundle.RelationRef) {
	s.affected = append(s.affected, refs...)
}
func (s *planState) preview(base, result store.Revision) store.Preview {
	writes := make([]store.Write, 0)
	deletes := make([]string, 0)
	renames := make([]store.Rename, 0)
	for p, data := range s.overlay.changed {
		sum := sha256.Sum256(data)
		writes = append(writes, store.Write{Path: p, Digest: hex.EncodeToString(sum[:]), Content: append([]byte(nil), data...)})
	}
	for p := range s.overlay.deleted {
		deletes = append(deletes, p)
	}
	renames = append(renames, s.renames...)
	sort.Slice(writes, func(i, j int) bool { return writes[i].Path < writes[j].Path })
	sort.Strings(deletes)
	sort.Slice(renames, func(i, j int) bool {
		if renames[i].From == renames[j].From {
			return renames[i].To < renames[j].To
		}
		return renames[i].From < renames[j].From
	})
	return store.Preview{BaseRevision: base, ResultRevision: result, Reads: s.readPaths.reads(), Writes: writes, Deletes: deletes, Renames: renames, AffectedRefs: refsFor(s.affected), ReverseImpact: refsFor(s.reverse), Plan: append([]store.OperationPlan(nil), s.plans...)}
}

func revision(ctx context.Context, source bundle.Source) (store.Revision, error) {
	if snapshot, ok := source.(store.Snapshot); ok {
		return snapshot.Revision(), nil
	}
	if source, ok := source.(store.ManifestSource); ok {
		manifest := source.Manifest()
		if manifest.Valid() {
			return manifest.RevisionContext(ctx)
		}
	}
	paths, err := source.Paths(ctx)
	if err != nil {
		return "", err
	}
	entries := make([]store.ManifestEntry, 0, len(paths))
	for _, p := range paths {
		p, err = sourcePath(p)
		if err != nil {
			return "", err
		}
		data, err := source.ReadFile(ctx, p)
		if err != nil {
			return "", err
		}
		entries = append(entries, store.ManifestEntry{Path: p, Content: data})
	}
	manifest, err := store.NewManifestContext(ctx, entries, nil)
	if err != nil {
		return "", err
	}
	return manifest.RevisionContext(ctx)
}
func invalid(code string, ds []store.Diagnostic, cause error) error {
	return &store.InvalidChangeSet{Code: code, Diagnostics: ds, Cause: cause}
}

func presentationInvalid(cause error) error {
	return invalid("invalid_presentation", nil, cause)
}
func refsFor(in []bundle.RelationRef) []bundle.RelationRef {
	seen := map[string]bundle.RelationRef{}
	for _, r := range in {
		seen[r.String()] = r
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
func checkPreconditions(ctx context.Context, source bundle.Source, b *bundle.Bundle, revision store.Revision, ps []store.Precondition) error {
	for _, pre := range ps {
		ok := false
		var refs []bundle.RelationRef
		switch p := pre.(type) {
		case store.RefExists:
			ok = b.TargetExists(p.Ref)
			refs = []bundle.RelationRef{p.Ref}
		case store.RefAbsent:
			ok = !b.TargetExists(p.Ref)
			refs = []bundle.RelationRef{p.Ref}
		case store.FragmentUnique:
			ok = b.FragmentExists(p.Ref.ID, p.Ref.Fragment)
			refs = []bundle.RelationRef{p.Ref}
		case store.RevisionEquals:
			ok = p.Revision == revision
		case store.FileDigestEquals:
			data, err := source.ReadFile(ctx, p.Path)
			if err == nil {
				sum := sha256.Sum256(data)
				ok = hex.EncodeToString(sum[:]) == p.Digest
			}
		case store.RelationExists:
			ok = hasRelation(b, p.Source, p.Type, p.Target)
			refs = []bundle.RelationRef{p.Source, p.Target}
		case store.RelationAbsent:
			ok = !hasRelation(b, p.Source, p.Type, p.Target)
			refs = []bundle.RelationRef{p.Source, p.Target}
		default:
			return invalid("unknown_precondition", nil, fmt.Errorf("unsupported precondition %T", pre))
		}
		if !ok {
			return &store.PreconditionFailure{Precondition: pre, AffectedRefs: refsFor(refs)}
		}
	}
	return nil
}
func hasRelation(b *bundle.Bundle, source bundle.RelationRef, typ string, target bundle.RelationRef) bool {
	for _, r := range b.SemanticLinksFrom(source.ID) {
		if r.Source.String() == source.String() && r.Type == typ && r.Target.String() == target.String() {
			return true
		}
	}
	return false
}
func readBundleFile(b *bundle.Bundle, file string) string {
	data, _ := b.ReadFile(file)
	return string(data)
}
func conceptPath(id bundle.ConceptID) string { return strings.Join(id.Segments(), "/") + ".md" }

func mappingForRef(root *yaml.Node, ref bundle.RelationRef) *yaml.Node {
	if ref.Fragment == "" {
		return root
	}
	var found *yaml.Node
	walkNestedMappings(root, func(n *yaml.Node) {
		if found != nil {
			return
		}
		if canonicalFragment(n) == ref.Fragment {
			found = n
		}
	})
	return found
}

// walkNestedMappings applies fn to mappings below the document frontmatter
// root. Root id/anchor are concept metadata and never establish a fragment.
func walkNestedMappings(root *yaml.Node, fn func(*yaml.Node)) {
	if root == nil {
		return
	}
	switch root.Kind {
	case yaml.MappingNode:
		for i := 1; i < len(root.Content); i += 2 {
			walkMappings(root.Content[i], fn)
		}
	case yaml.SequenceNode:
		for _, child := range root.Content {
			walkMappings(child, fn)
		}
	}
}
func walkMappings(n *yaml.Node, fn func(*yaml.Node)) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.MappingNode:
		fn(n)
		for i := 1; i < len(n.Content); i += 2 {
			walkMappings(n.Content[i], fn)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			walkMappings(c, fn)
		}
	}
}
func value(n *yaml.Node, key string) (*yaml.Node, bool) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Kind == yaml.ScalarNode && n.Content[i].Value == key {
			return n.Content[i+1], true
		}
	}
	return nil, false
}
func canonicalFragment(n *yaml.Node) string {
	fragment, _, ambiguous := canonicalFragmentNode(n)
	if ambiguous {
		return ""
	}
	return fragment
}

// canonicalFragmentNode implements the same identity rule as the semantic
// subresource index: one valid id wins; otherwise one valid anchor wins.
// Invalid occurrences never mask a later valid value, while duplicate valid
// canonical keys are deliberately ambiguous.
func canonicalFragmentNode(n *yaml.Node) (string, *yaml.Node, bool) {
	identity := bundle.ResolveMappingIdentity(n)
	if identity.State == bundle.MappingIdentityInvalid {
		return "", nil, true
	}
	return identity.Fragment, identity.Node, false
}
func rewriteTargets(root *yaml.Node, from, to bundle.ConceptID, oldFragment, newFragment string) bool {
	changed := false
	// Only relation item targets are semantic references. Extension fields named
	// "target" are opaque user data and must survive a planner operation.
	walkMappings(root, func(n *yaml.Node) {
		relations, ok := value(n, "relations")
		if !ok || relations.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(relations.Content); i += 2 {
			items := relations.Content[i+1]
			if items.Kind != yaml.SequenceNode {
				continue
			}
			for _, item := range items.Content {
				v, ok := value(item, "target")
				if !ok || v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
					continue
				}
				r, err := bundle.ParseRelationRef(v.Value)
				if err != nil || r.ID.String() != from.String() || (oldFragment != "" && r.Fragment != oldFragment) {
					continue
				}
				r.ID = to
				if oldFragment != "" {
					r.Fragment = newFragment
				}
				v.Value = r.String()
				changed = true
			}
		}
	})
	return changed
}
func renameCanonicalFragment(root *yaml.Node, from, to string) bool {
	changed := false
	walkNestedMappings(root, func(n *yaml.Node) {
		fragment, node, ambiguous := canonicalFragmentNode(n)
		if ambiguous || fragment != from {
			return
		}
		node.Value = to
		changed = true
	})
	return changed
}

// rewriteMarkdownLinks changes only the destination bytes of inline links and
// reference definitions. The scanner deliberately treats Markdown code as
// opaque: a link-looking string in a fence, indented block, or code span is
// never a navigation target.
func rewriteMarkdownLinks(text, source, oldPath, newPath string) string {
	var out strings.Builder
	out.Grow(len(text))
	fence := byte(0)
	fenceWidth := 0
	for i := 0; i < len(text); {
		lineEnd := strings.IndexByte(text[i:], '\n')
		if lineEnd < 0 {
			lineEnd = len(text)
		} else {
			lineEnd += i
		}
		line := text[i:lineEnd]
		if marker, width, ok := markdownFence(line); ok {
			if fence == 0 {
				fence, fenceWidth = marker, width
			} else if marker == fence && width >= fenceWidth {
				fence, fenceWidth = 0, 0
			}
			out.WriteString(text[i : lineEnd+newlineWidth(text, lineEnd)])
			i = lineEnd + newlineWidth(text, lineEnd)
			continue
		}
		if fence != 0 || indentedCode(line) {
			out.WriteString(text[i : lineEnd+newlineWidth(text, lineEnd)])
			i = lineEnd + newlineWidth(text, lineEnd)
			continue
		}
		out.WriteString(rewriteMarkdownLine(line, source, oldPath, newPath))
		if lineEnd < len(text) {
			out.WriteByte('\n')
		}
		i = lineEnd + newlineWidth(text, lineEnd)
	}
	return out.String()
}

func newlineWidth(text string, lineEnd int) int {
	if lineEnd < len(text) && text[lineEnd] == '\n' {
		return 1
	}
	return 0
}

func markdownFence(line string) (byte, int, bool) {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0, false
	}
	marker, start := line[i], i
	for i < len(line) && line[i] == marker {
		i++
	}
	return marker, i - start, i-start >= 3
}

func indentedCode(line string) bool {
	return strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ")
}

func rewriteMarkdownLine(line, source, oldPath, newPath string) string {
	var out strings.Builder
	out.Grow(len(line))
	for i := 0; i < len(line); {
		if line[i] == '`' {
			start := i
			for i < len(line) && line[i] == '`' {
				i++
			}
			ticks := line[start:i]
			if end := strings.Index(line[i:], ticks); end >= 0 {
				end += i + len(ticks)
				out.WriteString(line[start:end])
				i = end
				continue
			}
			out.WriteString(line[start:i])
			continue
		}
		if i == 0 {
			if start, end, destination, ok := markdownReferenceDefinition(line); ok {
				out.WriteString(line[:start])
				out.WriteString(rewriteDestination(destination, source, oldPath, newPath))
				out.WriteString(line[end:])
				return out.String()
			}
		}
		if line[i] == ']' && i+1 < len(line) && line[i+1] == '(' {
			out.WriteString("](")
			i += 2
			start := i
			for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
				i++
			}
			out.WriteString(line[start:i])
			end, destination, ok := markdownInlineDestination(line, i)
			if ok {
				out.WriteString(rewriteDestination(destination, source, oldPath, newPath))
				i = end
				continue
			}
			continue
		}
		out.WriteByte(line[i])
		i++
	}
	return out.String()
}

// markdownInlineDestination returns the bytes occupied by the first
// destination in an inline link, retaining its angle brackets when present.
func markdownInlineDestination(line string, start int) (int, string, bool) {
	if start >= len(line) {
		return start, "", false
	}
	if line[start] == '<' {
		end := strings.IndexByte(line[start:], '>')
		if end <= 0 {
			return start, "", false
		}
		end += start
		return end + 1, line[start : end+1], true
	}
	end := start
	for end < len(line) && line[end] != ')' && line[end] != ' ' && line[end] != '\t' {
		if line[end] == '\\' && end+1 < len(line) {
			end += 2
			continue
		}
		end++
	}
	return end, line[start:end], end > start
}

func markdownReferenceDefinition(line string) (int, int, string, bool) {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || line[i] != '[' {
		return 0, 0, "", false
	}
	close := strings.IndexByte(line[i+1:], ']')
	if close < 0 {
		return 0, 0, "", false
	}
	close += i + 1
	if close+1 >= len(line) || line[close+1] != ':' {
		return 0, 0, "", false
	}
	i = close + 2
	if i >= len(line) || (line[i] != ' ' && line[i] != '\t') {
		return 0, 0, "", false
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	end, destination, ok := markdownInlineDestination(line, i)
	return i, end, destination, ok
}

func rewriteDestination(destination, source, oldPath, newPath string) string {
	angle := strings.HasPrefix(destination, "<") && strings.HasSuffix(destination, ">")
	raw := destination
	if angle {
		raw = raw[1 : len(raw)-1]
	}
	cut := len(raw)
	if at := strings.IndexAny(raw, "?#"); at >= 0 {
		cut = at
	}
	local, suffix := raw[:cut], raw[cut:]
	if local == "" || strings.HasPrefix(local, "/") || strings.Contains(local, ":") {
		return destination
	}
	resolved := path.Clean(path.Join(path.Dir(source), local))
	if resolved != oldPath {
		return destination
	}
	rel, err := filepath.Rel(path.Dir(source), newPath)
	if err != nil {
		return destination
	}
	rewritten := filepath.ToSlash(rel) + suffix
	if angle {
		return "<" + rewritten + ">"
	}
	return rewritten
}

// previewDiagnostics is the sole public projection of staged validation and
// semantic findings. Keep the source kind and severity intact: callers need
// both informational aliases and blocking semantic errors to explain a plan.
func previewDiagnostics(validation validator.Report, relations []bundle.RelationDiagnostic) []store.Diagnostic {
	out := make([]store.Diagnostic, 0, len(validation.Diagnostics)+len(relations))
	for _, diagnostic := range validation.Diagnostics {
		code := diagnostic.Code
		if code == "" {
			code = "validator"
		}
		out = append(out, store.Diagnostic{
			Kind:     store.DiagnosticValidation,
			Severity: validationSeverity(diagnostic.Severity),
			Code:     code,
			File:     diagnostic.File,
			Message:  diagnostic.Message,
		})
	}
	out = append(out, store.ProjectRelationDiagnostics(relations)...)
	sort.Slice(out, func(i, j int) bool {
		return diagnosticKey(out[i]) < diagnosticKey(out[j])
	})
	unique := out[:0]
	for _, diagnostic := range out {
		if len(unique) == 0 || diagnosticKey(unique[len(unique)-1]) != diagnosticKey(diagnostic) {
			unique = append(unique, diagnostic)
		}
	}
	return unique
}

func validationSeverity(severity validator.Severity) store.DiagnosticSeverity {
	switch severity {
	case validator.SeverityInfo:
		return store.DiagnosticInfo
	case validator.SeverityWarning:
		return store.DiagnosticWarning
	default:
		return store.DiagnosticError
	}
}

func diagnosticKey(diagnostic store.Diagnostic) string {
	refs := refsFor(diagnostic.Refs)
	parts := make([]string, len(refs))
	for i, ref := range refs {
		parts[i] = ref.String()
	}
	return string(diagnostic.Kind) + "\x00" + string(diagnostic.Severity) + "\x00" + diagnostic.Code + "\x00" + diagnostic.File + "\x00" + diagnostic.RelationType + "\x00" + diagnostic.RawTarget + "\x00" + diagnostic.Message + "\x00" + strings.Join(parts, "\x00")
}
