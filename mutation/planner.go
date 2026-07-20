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
	"github.com/skosovsky/okf/internal/documentlayout"
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
	return &trackedSource{source: source, paths: make(map[string]struct{})}
}

func (s *trackedSource) Paths(ctx context.Context) ([]string, error) { return s.source.Paths(ctx) }
func (s *trackedSource) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.paths[name] = struct{}{}
	return s.source.ReadFile(ctx, name)
}
func (s *trackedSource) Manifest() store.Manifest {
	if source, ok := s.source.(store.ManifestSource); ok {
		return source.Manifest()
	}
	return store.Manifest{}
}
func (s *trackedSource) ManifestContext(ctx context.Context) (store.Manifest, error) {
	manifest, ok, err := sourceManifestContext(ctx, s.source)
	if err != nil {
		return store.Manifest{}, err
	}
	if !ok {
		return store.Manifest{}, nil
	}
	return manifest, nil
}
func (s *trackedSource) verifiedManifestContext(ctx context.Context) (store.Manifest, []string, bool, error) {
	manifest, paths, ok, err := verifiedSourceManifestContext(ctx, s.source)
	if err != nil || !ok {
		return store.Manifest{}, nil, ok, err
	}
	for _, name := range paths {
		if err := ctx.Err(); err != nil {
			return store.Manifest{}, nil, false, err
		}
		s.paths[name] = struct{}{}
	}
	return manifest, paths, true, nil
}
func (s *trackedSource) readsContext(ctx context.Context) ([]store.Read, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(s.paths))
	for name := range s.paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		paths = append(paths, name)
	}
	sort.Strings(paths)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]store.Read, len(paths))
	for i, name := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[i] = store.Read{Path: name}
	}
	return out, ctx.Err()
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
		changedRefs, err := availableSemanticRefs(ctx, tracked)
		if err != nil {
			return Result{}, err
		}
		return Result{}, &store.Conflict{Expected: change.BaseRevision, Actual: base, ChangedRefs: changedRefs, Retryable: true}
	}
	b, err := bundle.Load(ctx, tracked)
	if err != nil {
		return Result{}, err
	}
	if err := checkPreconditions(ctx, tracked, b, base, change.Preconditions); err != nil {
		return Result{}, err
	}
	o, err := NewOverlayContext(ctx, tracked)
	if err != nil {
		return Result{}, err
	}
	state := &planState{ctx: ctx, overlay: o, bundle: b, readPaths: tracked}
	for _, op := range change.Operations {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		candidate, err := state.cloneContext(ctx)
		if err != nil {
			return Result{}, err
		}
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
	// Every successful operation load above has already established the final
	// immutable bundle. Validate that exact snapshot: reloading the overlay here
	// is redundant and weakens the planner's read evidence.
	validation, err := validator.ValidateBundleContext(ctx, state.bundle, p.ValidatorConfig)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	relationDiagnostics, err := state.bundle.RelationDiagnosticsContext(ctx)
	if err != nil {
		return Result{}, err
	}
	preview, err := state.previewContext(ctx, base, resultRevision)
	if err != nil {
		return Result{}, err
	}
	preview.Diagnostics, err = previewDiagnosticsContext(ctx, validation, relationDiagnostics)
	if err != nil {
		return Result{}, err
	}
	result := Result{Preview: preview, Validation: validation, RelationDiagnostics: relationDiagnostics, Staged: state.overlay}
	blockingRelationDiagnostics, err := blockingRelationDiagnosticsContext(ctx, relationDiagnostics)
	if err != nil {
		return Result{}, err
	}
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
func availableSemanticRefs(ctx context.Context, source bundle.Source) ([]bundle.RelationRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, err := bundle.Load(ctx, source)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bundle.RelationRef)
	add := func(ref bundle.RelationRef) {
		if ref.String() != "" {
			seen[ref.String()] = ref
		}
	}
	for _, concept := range b.Concepts() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		add(bundle.RelationRef{ID: concept.ID})
		for _, fragment := range b.Subresources(concept.ID) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			add(bundle.RelationRef{ID: concept.ID, Fragment: fragment})
		}
		for _, relation := range b.SemanticLinksFrom(concept.ID) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			add(relation.Source)
			add(relation.Target)
		}
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, ref := range seen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func blockingRelationDiagnosticsContext(ctx context.Context, in []bundle.RelationDiagnostic) ([]bundle.RelationDiagnostic, error) {
	out := make([]bundle.RelationDiagnostic, 0, len(in))
	for _, diagnostic := range in {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if diagnostic.BlocksMutation() {
			out = append(out, diagnostic)
		}
	}
	return out, ctx.Err()
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

func (s *planState) cloneContext(ctx context.Context) (*planState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cloned := *s
	var err error
	cloned.overlay, err = s.overlay.cloneContext(ctx)
	if err != nil {
		return nil, err
	}
	cloned.affected = append([]bundle.RelationRef(nil), s.affected...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cloned.reverse = append([]bundle.RelationRef(nil), s.reverse...)
	cloned.plans = append([]store.OperationPlan(nil), s.plans...)
	cloned.renames = append([]store.Rename(nil), s.renames...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &cloned, nil
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
	if !s.bundle.Contains(op.Source.ID) || !s.bundle.Contains(op.Target.ID) {
		return invalid("missing_relation_endpoint", []store.Diagnostic{{Code: "missing_relation_endpoint", Refs: []bundle.RelationRef{op.Source, op.Target}}}, errors.New("relation endpoint does not exist"))
	}
	c, ok := s.bundle.Get(op.Source.ID)
	if !ok {
		return invalid("missing_source", nil, errors.New("source missing"))
	}
	data := []byte(readBundleFile(s.bundle, c.Path))
	p, err := parsePresentationContext(s.ctx, data)
	if err != nil {
		code := relationPresentationCode(err)
		return s.presentationInvalid(code, []store.Diagnostic{{Code: code, Refs: []bundle.RelationRef{op.Source, op.Target}}}, err, c.Path, "ensure_relation", data)
	}
	sourceMapping, err := mappingForRef(s.ctx, p, p.root, op.Source)
	if err != nil {
		code := relationPresentationCode(err)
		return s.presentationInvalid(code, []store.Diagnostic{{Code: code, Refs: []bundle.RelationRef{op.Source, op.Target}}}, err, c.Path, "ensure_relation", data)
	}
	if sourceMapping == nil {
		return invalid("missing_source_fragment", []store.Diagnostic{{Code: "missing_source_fragment", Refs: []bundle.RelationRef{op.Source}}}, errors.New("fragment missing"))
	}
	if op.Target.Fragment != "" {
		targetConcept, _ := s.bundle.Get(op.Target.ID)
		targetData := []byte(readBundleFile(s.bundle, targetConcept.Path))
		targetPresentation, err := parsePresentationContext(s.ctx, targetData)
		if err != nil {
			code := relationPresentationCode(err)
			return s.presentationInvalid(code, []store.Diagnostic{{Code: code, Refs: []bundle.RelationRef{op.Source, op.Target}}}, err, targetConcept.Path, "ensure_relation", targetData)
		}
		targetMapping, err := mappingForRef(s.ctx, targetPresentation, targetPresentation.root, op.Target)
		if err != nil {
			code := relationPresentationCode(err)
			return s.presentationInvalid(code, []store.Diagnostic{{Code: code, Refs: []bundle.RelationRef{op.Source, op.Target}}}, err, targetConcept.Path, "ensure_relation", targetData)
		}
		if targetMapping == nil {
			return invalid("missing_relation_endpoint", []store.Diagnostic{{Code: "missing_relation_endpoint", Refs: []bundle.RelationRef{op.Source, op.Target}}}, errors.New("relation endpoint does not exist"))
		}
	}
	updated, err := ensureRelationPresentationContext(s.ctx, data, op.Source, op.Type, op.Target.String())
	if err != nil {
		code := relationPresentationCode(err)
		return s.presentationInvalid(code, []store.Diagnostic{{Code: code, Refs: []bundle.RelationRef{op.Source, op.Target}}}, err, c.Path, "ensure_relation", data)
	}
	equal, err := bytesEqualContext(s.ctx, updated, data)
	if err != nil {
		return err
	}
	if !equal {
		if err := s.overlay.PutContext(s.ctx, c.Path, updated); err != nil {
			return err
		}
	}
	s.add(op.Source, op.Target)
	s.plans = append(s.plans, store.OperationPlan{Operation: op, AffectedRefs: []bundle.RelationRef{op.Source, op.Target}, Details: []string{"relation present exactly once"}})
	return nil
}

func relationPresentationCode(err error) string {
	if errors.Is(err, ErrAmbiguousPresentation) {
		return "ambiguous_relation_presentation"
	}
	return "invalid_relation_presentation"
}

func presentationChangeCode(err error) string {
	if errors.Is(err, ErrAmbiguousPresentation) {
		return "ambiguous_presentation"
	}
	return "invalid_presentation"
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
	yamlImpact, err := s.yamlImpactPathsContext(s.ctx, bundle.RelationRef{ID: op.From})
	if err != nil {
		return err
	}
	// Rewrite canonical semantic target refs in frontmatter, then rewrite every
	// Markdown destination which resolves to the moved document.
	for _, file := range s.bundle.MarkdownFiles() {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		data := readBundleFile(s.bundle, file)
		if documentlayout.HasOpeningDelimiter([]byte(data)) {
			p, err := parsePresentationContext(s.ctx, []byte(data))
			if err != nil {
				if _, need := yamlImpact[file]; need {
					return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "move_concept", []byte(data))
				}
			} else {
				patches, err := relationTargetPatchesContext(s.ctx, p, op.From, op.To, "", "")
				if err != nil {
					return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "move_concept", []byte(data))
				}
				updated, err := p.patchYAMLContext(s.ctx, patches)
				if err != nil {
					return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "move_concept", []byte(data))
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
		}
		out := file
		if out == fromPath {
			out = toPath
		}
		current := data
		if staged, readErr := s.overlay.ReadFile(s.ctx, out); readErr == nil {
			current = string(staged)
		}
		rewritten, err := rewriteMarkdownDestinationsContext(s.ctx, []byte(current), func(destination string) (string, bool) {
			value := rewriteDestination(destination, file, out, op.From, op.To, file == fromPath)
			return value, value != destination
		})
		if err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, out, "move_concept", []byte(current))
		}
		equal, err := bytesEqualContext(s.ctx, rewritten, []byte(current))
		if err != nil {
			return err
		}
		if !equal {
			if err := s.overlay.PutContext(s.ctx, out, rewritten); err != nil {
				return err
			}
		}
	}
	s.plans = append(s.plans, store.OperationPlan{Operation: op, AffectedRefs: []bundle.RelationRef{{ID: op.From}, {ID: op.To}}, Details: []string{"moved concept and rewrote canonical references"}})
	return nil
}

func (s *planState) renameFragment(op store.RenameFragment) error {
	c, ok := s.bundle.Get(op.Concept)
	if !ok {
		file := conceptPath(op.Concept)
		if data, readErr := s.overlay.ReadFile(s.ctx, file); readErr == nil {
			if _, err := parsePresentationContext(s.ctx, data); err != nil {
				return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "rename_fragment", data)
			}
		}
		return invalid("missing_concept", []store.Diagnostic{{Code: "missing_concept", Refs: []bundle.RelationRef{{ID: op.Concept}}}}, errors.New("concept missing"))
	}
	data := readBundleFile(s.bundle, c.Path)
	p, err := parsePresentationContext(s.ctx, []byte(data))
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, c.Path, "rename_fragment", []byte(data))
	}
	targetAliases, err := canonicalAliasProvenanceContext(s.ctx, p.root, op.To)
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, c.Path, "rename_fragment", []byte(data))
	}
	if len(targetAliases) != 0 {
		err := p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, targetAliases...)
		return s.presentationInvalid(presentationChangeCode(err), nil, err, c.Path, "rename_fragment", []byte(data))
	}
	targetCandidates, err := canonicalFragmentCandidatesContext(s.ctx, p.root, op.To)
	if err != nil {
		return err
	}
	if len(targetCandidates) > 1 {
		err := p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, targetCandidates...)
		return s.presentationInvalid(presentationChangeCode(err), nil, err, c.Path, "rename_fragment", []byte(data))
	}
	if len(targetCandidates) == 1 {
		return invalid("target_fragment_exists", nil, errors.New("target fragment exists"))
	}
	fragment, found, err := fragmentPatchContext(s.ctx, p, op.From, op.To)
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, c.Path, "rename_fragment", []byte(data))
	}
	if !found {
		if !s.bundle.FragmentExists(op.Concept, op.From) {
			return invalid("missing_or_ambiguous_fragment", []store.Diagnostic{{Code: "missing_or_ambiguous_fragment", Refs: []bundle.RelationRef{{ID: op.Concept, Fragment: op.From}}}}, errors.New("fragment missing or ambiguous"))
		}
		return invalid("missing_fragment", nil, errors.New("fragment not found"))
	}
	patches, err := relationTargetPatchesContext(s.ctx, p, op.Concept, op.Concept, op.From, op.To)
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, c.Path, "rename_fragment", []byte(data))
	}
	patches = append(patches, fragment)
	updated, err := p.patchYAMLContext(s.ctx, patches)
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, c.Path, "rename_fragment", []byte(data))
	}
	if err := s.overlay.PutContext(s.ctx, c.Path, updated); err != nil {
		return err
	}
	for _, rel := range s.bundle.ReverseImpactFragment(op.Concept, op.From) {
		s.reverse = append(s.reverse, rel.Source)
	}
	yamlImpact, err := s.yamlImpactPathsContext(s.ctx, bundle.RelationRef{ID: op.Concept, Fragment: op.From})
	if err != nil {
		return err
	}
	for _, file := range s.bundle.MarkdownFiles() {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if file == c.Path {
			continue
		}
		data := readBundleFile(s.bundle, file)
		if !documentlayout.HasOpeningDelimiter([]byte(data)) {
			continue
		}
		p, err := parsePresentationContext(s.ctx, []byte(data))
		if err != nil {
			if _, need := yamlImpact[file]; need {
				return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "rename_fragment", []byte(data))
			}
			continue
		}
		patches, err := relationTargetPatchesContext(s.ctx, p, op.Concept, op.Concept, op.From, op.To)
		if err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "rename_fragment", []byte(data))
		}
		if len(patches) > 0 {
			updated, err := p.patchYAMLContext(s.ctx, patches)
			if err != nil {
				return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "rename_fragment", []byte(data))
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

// yamlImpactPaths lists Markdown paths whose semantic relations target refs and
// therefore require a lossless YAML parse before Move/Rename can skip them.
func (s *planState) yamlImpactPathsContext(ctx context.Context, refs ...bundle.RelationRef) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var rels []bundle.Relation
		if ref.Fragment == "" {
			rels = s.bundle.ReverseImpactConcept(ref.ID)
		} else {
			rels = s.bundle.ReverseImpactFragment(ref.ID, ref.Fragment)
		}
		for _, rel := range rels {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if concept, ok := s.bundle.Get(rel.Source.ID); ok {
				out[concept.Path] = struct{}{}
			}
		}
	}
	return out, ctx.Err()
}

func (s *planState) add(refs ...bundle.RelationRef) {
	s.affected = append(s.affected, refs...)
}
func (s *planState) previewContext(ctx context.Context, base, result store.Revision) (store.Preview, error) {
	if err := ctx.Err(); err != nil {
		return store.Preview{}, err
	}
	writes := make([]store.Write, 0)
	deletes := make([]string, 0)
	renames := make([]store.Rename, 0)
	for p, data := range s.overlay.changed {
		if err := ctx.Err(); err != nil {
			return store.Preview{}, err
		}
		digest, err := sha256DigestContext(ctx, data.data)
		if err != nil {
			return store.Preview{}, err
		}
		content, err := appendBytesContext(ctx, nil, data.data)
		if err != nil {
			return store.Preview{}, err
		}
		writes = append(writes, store.Write{Path: p, Digest: digest, Content: content})
	}
	for p := range s.overlay.deleted {
		if err := ctx.Err(); err != nil {
			return store.Preview{}, err
		}
		deletes = append(deletes, p)
	}
	for _, rename := range s.renames {
		if err := ctx.Err(); err != nil {
			return store.Preview{}, err
		}
		renames = append(renames, rename)
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].Path < writes[j].Path })
	sort.Strings(deletes)
	sort.Slice(renames, func(i, j int) bool {
		if renames[i].From == renames[j].From {
			return renames[i].To < renames[j].To
		}
		return renames[i].From < renames[j].From
	})
	if err := ctx.Err(); err != nil {
		return store.Preview{}, err
	}
	reads, err := s.readPaths.readsContext(ctx)
	if err != nil {
		return store.Preview{}, err
	}
	affected, err := refsForContext(ctx, s.affected)
	if err != nil {
		return store.Preview{}, err
	}
	reverse, err := refsForContext(ctx, s.reverse)
	if err != nil {
		return store.Preview{}, err
	}
	plans := make([]store.OperationPlan, 0, len(s.plans))
	for _, plan := range s.plans {
		if err := ctx.Err(); err != nil {
			return store.Preview{}, err
		}
		plan.AffectedRefs, err = copyRefsContext(ctx, plan.AffectedRefs)
		if err != nil {
			return store.Preview{}, err
		}
		plan.Details = append([]string(nil), plan.Details...)
		plans = append(plans, plan)
	}
	return store.Preview{BaseRevision: base, ResultRevision: result, Reads: reads, Writes: writes, Deletes: deletes, Renames: renames, AffectedRefs: affected, ReverseImpact: reverse, Plan: plans}, ctx.Err()
}

func copyRefsContext(ctx context.Context, refs []bundle.RelationRef) ([]bundle.RelationRef, error) {
	out := make([]bundle.RelationRef, 0, len(refs))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, ctx.Err()
}

func sha256DigestContext(ctx context.Context, data []byte) (string, error) {
	hash := sha256.New()
	const chunkSize = 64 << 10
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := min(len(data), chunkSize)
		_, _ = hash.Write(data[:n])
		data = data[n:]
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func revision(ctx context.Context, source bundle.Source) (store.Revision, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if snapshot, ok := source.(store.Snapshot); ok {
		revision := snapshot.Revision()
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return revision, nil
	}
	if manifest, _, ok, manifestErr := verifiedSourceManifestContext(ctx, source); manifestErr != nil {
		return "", manifestErr
	} else if ok {
		manifestRevision, paths, manifestErr := manifest.RevisionPathsContext(ctx)
		if manifestErr == nil {
			if tracked, trackedOK := source.(*trackedSource); trackedOK {
				for _, name := range paths {
					if err := ctx.Err(); err != nil {
						return "", err
					}
					tracked.paths[name] = struct{}{}
				}
			}
			return manifestRevision, nil
		}
		if errors.Is(manifestErr, context.Canceled) || errors.Is(manifestErr, context.DeadlineExceeded) {
			return "", manifestErr
		}
	}
	paths, err := source.Paths(ctx)
	if err != nil {
		return "", err
	}
	entries := make([]store.ManifestEntry, 0, len(paths))
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
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

// presentationInvalid enriches a resolver failure at the planner boundary.
// It copies the typed error instead of mutating it: parser errors can be
// shared by callers and remain valid in their body/frontmatter-local domain.
func (s *planState) presentationInvalid(code string, diagnostics []store.Diagnostic, cause error, path, operation string, data []byte) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	var presentation *PresentationError
	if !errors.As(cause, &presentation) {
		format := "markdown"
		if documentlayout.HasOpeningDelimiter(data) {
			format = "yaml"
		}
		return invalid(code, diagnostics, &PresentationError{
			Code:      "parse_presentation",
			Format:    format,
			Path:      path,
			Operation: operation,
			Location:  presentationRange(data, format),
			Err:       fmt.Errorf("%w: %w", ErrUnsupportedPresentation, cause),
		})
	}
	copy := *presentation
	copy.Path = path
	copy.Operation = operation
	// Keep Err as the inner sentinel/cause. Wrapping the whole PresentationError
	// would nest identical metadata and break errors.Is against copy.Err.
	copy.Err = presentation.Err
	if copy.Format == "yaml" && copy.yamlRelative {
		copy.Location.Start += yamlFrontmatterStart(data)
		copy.Location.End += yamlFrontmatterStart(data)
	}
	if len(data) > 0 && (copy.Location == (SourceSpan{}) || copy.Location.Start == copy.Location.End) {
		if copy.Location == (SourceSpan{}) {
			copy.Location = presentationRange(data, copy.Format)
		} else if copy.Location.End < len(data) {
			copy.Location.End++
		} else if copy.Location.Start > 0 {
			copy.Location.Start--
		}
	}
	return invalid(code, diagnostics, &copy)
}

func yamlFrontmatterStart(data []byte) int {
	if end := bytes.IndexByte(data, '\n'); end >= 0 {
		return end + 1
	}
	return len(data)
}

func presentationRange(data []byte, format string) SourceSpan {
	if len(data) == 0 {
		return SourceSpan{}
	}
	if format == "markdown" {
		start, err := markdownBodyStart(data)
		if err == nil && start < len(data) {
			return SourceSpan{Start: start, End: len(data)}
		}
		return SourceSpan{Start: 0, End: len(data)}
	}
	if layout, ok, err := documentlayout.Split(data); err == nil && ok && layout.YAMLStart < layout.YAMLEnd {
		return SourceSpan{Start: layout.YAMLStart, End: layout.YAMLEnd}
	}
	return SourceSpan{Start: 0, End: len(data)}
}
func refsForContext(ctx context.Context, in []bundle.RelationRef) ([]bundle.RelationRef, error) {
	seen := map[string]bundle.RelationRef{}
	for _, r := range in {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seen[r.String()] = r
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, r := range seen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func checkPreconditions(ctx context.Context, source bundle.Source, b *bundle.Bundle, revision store.Revision, ps []store.Precondition) error {
	for _, pre := range ps {
		if err := ctx.Err(); err != nil {
			return err
		}
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
				digest, digestErr := sha256DigestContext(ctx, data)
				if digestErr != nil {
					return digestErr
				}
				ok = digest == p.Digest
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
			affected, err := refsForContext(ctx, refs)
			if err != nil {
				return err
			}
			return &store.PreconditionFailure{Precondition: pre, AffectedRefs: affected}
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

func mappingForRef(ctx context.Context, p *presentation, root *yaml.Node, ref bundle.RelationRef) (*yaml.Node, error) {
	if ref.Fragment == "" {
		return root, nil
	}
	alias, err := canonicalAliasProvenanceContext(ctx, root, ref.Fragment)
	if err != nil {
		return nil, err
	}
	if len(alias) != 0 {
		return nil, p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, alias...)
	}
	var found []*yaml.Node
	var first error
	walkNestedMappings(root, func(n *yaml.Node) {
		if first == nil {
			first = ctx.Err()
		}
		if first != nil {
			return
		}
		if err := p.rejectMergedCanonicalFragment(n, ref.Fragment); err != nil {
			first = err
			return
		}
		identity := bundle.ResolveMappingIdentity(n)
		if identity.State == bundle.MappingIdentityInvalid {
			if identityMentionsFragment(n, ref.Fragment) {
				if duplicateCanonicalIdentityCandidate(n, ref.Fragment) {
					first = p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, identityKeyNodes(n)...)
				} else {
					first = p.unsupportedIdentityPresentationForFragment(n, ref.Fragment)
				}
			}
			return
		}
		if identity.State == bundle.MappingIdentityValid && identity.Fragment == ref.Fragment {
			if err := p.requireTouchedProvenance(identity.Node); err != nil {
				first = err
				return
			}
			if _, err := p.resolver.scalar(identity.Node); err != nil {
				first = err
				return
			}
			found = append(found, n)
		}
	})
	if first != nil {
		return nil, first
	}
	if len(found) > 1 {
		return nil, p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, found...)
	}
	if len(found) == 0 {
		return nil, nil
	}
	return found[0], nil
}

func duplicateCanonicalIdentityCandidate(n *yaml.Node, fragment string) bool {
	valid := func(key string) []string {
		values := make([]string, 0)
		for _, value := range mappingValues(n, key) {
			if value != nil && value.Kind == yaml.ScalarNode && value.Tag == "!!str" && bundle.ValidateRelationFragment(value.Value) == nil {
				values = append(values, value.Value)
			}
		}
		return values
	}
	ids := valid("id")
	if len(ids) > 0 {
		return len(ids) > 1 && stringSliceContains(ids, fragment)
	}
	anchors := valid("anchor")
	return len(anchors) > 1 && stringSliceContains(anchors, fragment)
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func identityKeyNodes(n *yaml.Node) []*yaml.Node {
	nodes := append([]*yaml.Node(nil), mappingKeyNodes(n, "id")...)
	return append(nodes, mappingKeyNodes(n, "anchor")...)
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

// unsupportedIdentityPresentationForFragment rejects identity presentation
// outside the lossless subset. When from is empty every identity field is
// checked. When from is set, only mappings that mention from are checked, and
// then every identity field on that mapping must be a supported scalar: an
// invalid or non-scalar sibling next to a valid matching anchor is Unsupported
// (not a late staged_validation_failed).
func (p *presentation) unsupportedIdentityPresentationForFragment(n *yaml.Node, from string) error {
	if from != "" && !identityMentionsFragment(n, from) {
		return nil
	}
	for _, key := range []string{"id", "anchor"} {
		for _, value := range mappingValues(n, key) {
			if value == nil {
				continue
			}
			if value.Kind == yaml.AliasNode || value.Alias != nil {
				return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, value)
			}
			if value.Kind != yaml.ScalarNode {
				return p.yamlNodeError("unsupported_identity_scalar", ErrUnsupportedPresentation, value)
			}
			if value.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
				return p.yamlNodeError("unsupported_scalar", ErrUnsupportedPresentation, value)
			}
			if value.Tag != "!!str" || value.Anchor != "" || value.Style&yaml.TaggedStyle != 0 {
				code := "explicit_tag"
				if value.Anchor != "" {
					code = "touched_anchor"
				}
				return p.yamlNodeError(code, ErrUnsupportedPresentation, value)
			}
			if bundle.ValidateRelationFragment(value.Value) != nil {
				return p.yamlNodeError("invalid_identity_fragment", ErrUnsupportedPresentation, value)
			}
		}
	}
	return nil
}

func rewriteDestination(destination, resolveSource, emitSource string, oldID, newID bundle.ConceptID, preserveAllTargets bool) string {
	// Goldmark collectors already strip presentation wrappers; destination is the
	// semantic path (+ optional query/fragment), never angle-bracketed source text.
	cut := len(destination)
	if at := strings.IndexAny(destination, "?#"); at >= 0 {
		cut = at
	}
	local, suffix := destination[:cut], destination[cut:]
	if local == "" {
		return destination
	}
	sourceID, err := bundle.ConceptIDFromPath("", resolveSource)
	if err != nil {
		return destination
	}
	kind := bundle.ClassifyLink(destination)
	resolved, ok := (bundle.Link{Target: destination, Kind: kind}).Resolve(sourceID)
	if !ok || (!preserveAllTargets && resolved.String() != oldID.String()) {
		return destination
	}
	target := resolved
	if resolved.String() == oldID.String() {
		target = newID
	}
	if kind == bundle.LinkAbsolute {
		if target.String() == resolved.String() {
			return destination
		}
		return "/" + conceptPath(target) + suffix
	}
	rel, err := filepath.Rel(path.Dir(emitSource), conceptPath(target))
	if err != nil {
		return destination
	}
	return filepath.ToSlash(rel) + suffix
}

// previewDiagnostics is the sole public projection of staged validation and
// semantic findings. Keep the source kind and severity intact: callers need
// both informational aliases and blocking semantic errors to explain a plan.
func previewDiagnosticsContext(ctx context.Context, validation validator.Report, relations []bundle.RelationDiagnostic) ([]store.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]store.Diagnostic, 0, len(validation.Diagnostics)+len(relations))
	for _, diagnostic := range validation.Diagnostics {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		severity := store.DiagnosticInfo
		if relation.BlocksMutation() {
			severity = store.DiagnosticError
		}
		out = append(out, store.Diagnostic{
			Kind:         store.DiagnosticRelation,
			Severity:     severity,
			Code:         relation.Code,
			File:         relation.File,
			Message:      relation.Message,
			RelationType: relation.RelationType,
			RawTarget:    relation.RawTarget,
			Refs:         []bundle.RelationRef{{ID: relation.Source, Fragment: relation.SourceFragment}},
		})
	}
	type keyedDiagnostic struct {
		diagnostic store.Diagnostic
		key        string
	}
	keyed := make([]keyedDiagnostic, 0, len(out))
	for _, diagnostic := range out {
		key, err := diagnosticKeyContext(ctx, diagnostic)
		if err != nil {
			return nil, err
		}
		keyed = append(keyed, keyedDiagnostic{diagnostic: diagnostic, key: key})
	}
	sort.Slice(keyed, func(i, j int) bool { return keyed[i].key < keyed[j].key })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	unique := make([]store.Diagnostic, 0, len(keyed))
	lastKey := ""
	for index, item := range keyed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if index == 0 || item.key != lastKey {
			unique = append(unique, item.diagnostic)
			lastKey = item.key
		}
	}
	return unique, ctx.Err()
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

func diagnosticKeyContext(ctx context.Context, diagnostic store.Diagnostic) (string, error) {
	refs, err := refsForContext(ctx, diagnostic.Refs)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(refs))
	for i, ref := range refs {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		parts[i] = ref.String()
	}
	return string(diagnostic.Kind) + "\x00" + string(diagnostic.Severity) + "\x00" + diagnostic.Code + "\x00" + diagnostic.File + "\x00" + diagnostic.RelationType + "\x00" + diagnostic.RawTarget + "\x00" + diagnostic.Message + "\x00" + strings.Join(parts, "\x00"), ctx.Err()
}
