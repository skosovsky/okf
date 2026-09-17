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
	"strconv"
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
	cache  map[string][]byte
}

func newTrackedSource(source bundle.Source) *trackedSource {
	return &trackedSource{source: source, paths: make(map[string]struct{}), cache: make(map[string][]byte)}
}

func (s *trackedSource) Paths(ctx context.Context) ([]string, error) { return s.source.Paths(ctx) }
func (s *trackedSource) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.paths[name] = struct{}{}
	if cached, ok := s.cache[name]; ok {
		return append([]byte(nil), cached...), nil
	}
	data, err := s.source.ReadFile(ctx, name)
	if err != nil {
		return nil, err
	}
	s.cache[name] = append([]byte(nil), data...)
	return append([]byte(nil), data...), nil
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
	paths := make([]string, 0, boundedProjectionCapacity(len(s.paths)))
	for name := range s.paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		paths = append(paths, name)
	}
	if err := sortSliceContext(ctx, paths, func(left, right string) bool { return left < right }); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]store.Read, 0, boundedProjectionCapacity(len(paths)))
	for _, name := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, store.Read{Path: name})
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
	if validation.ExitCode() != 0 || len(blockingRelationDiagnostics) != 0 {
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
	return availableSemanticRefsFromBundle(ctx, b)
}

// availableSemanticRefsFromBundle projects only the semantic index. In
// particular, it must not call Bundle.Concepts or Bundle.Get: those APIs clone
// complete Markdown documents and YAML trees although this projection needs
// only identifiers, fragments, and relations.
func availableSemanticRefsFromBundle(ctx context.Context, b *bundle.Bundle) ([]bundle.RelationRef, error) {
	conceptIDs, err := b.ConceptIDsContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[relationRefKey]bundle.RelationRef, boundedProjectionCapacity(len(conceptIDs)))
	add := func(ref bundle.RelationRef) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := relationRefIdentity(ref)
		if key.id != "" {
			seen[key] = ref
		}
		return nil
	}
	for _, conceptID := range conceptIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := add(bundle.RelationRef{ID: conceptID}); err != nil {
			return nil, err
		}
		fragments, err := b.SubresourcesContext(ctx, conceptID)
		if err != nil {
			return nil, err
		}
		for _, fragment := range fragments {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := add(bundle.RelationRef{ID: conceptID, Fragment: fragment}); err != nil {
				return nil, err
			}
		}
		relations, err := b.SemanticLinksFromContext(ctx, conceptID)
		if err != nil {
			return nil, err
		}
		for _, relation := range relations {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := add(relation.Source); err != nil {
				return nil, err
			}
			if err := add(relation.Target); err != nil {
				return nil, err
			}
		}
	}
	return materializeSortedRelationRefsContext(ctx, seen)
}

func materializeSortedRelationRefsContext(ctx context.Context, seen map[relationRefKey]bundle.RelationRef) ([]bundle.RelationRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, ref := range seen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	if err := sortRelationRefsContext(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func blockingRelationDiagnosticsContext(ctx context.Context, in []bundle.RelationDiagnostic) ([]bundle.RelationDiagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]bundle.RelationDiagnostic, 0, boundedProjectionCapacity(len(in)))
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
	cloned.affected, err = copyRefsContext(ctx, s.affected)
	if err != nil {
		return nil, err
	}
	cloned.reverse, err = copyRefsContext(ctx, s.reverse)
	if err != nil {
		return nil, err
	}
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
	if handled, err := applyV02OperationDescriptor(s, operation); handled {
		return err
	}
	switch op := operation.(type) {
	case store.EnsureRelation:
		return s.ensure(op)
	case store.MoveConcept:
		return s.move(op)
	case store.RenameFragment:
		return s.renameFragment(op)
	case store.MigrateV01ToV02:
		return s.migrateV01ToV02(op)
	default:
		return invalid("unknown_operation", nil, fmt.Errorf("unsupported operation %T", operation))
	}
}

func (s *planState) ensure(op store.EnsureRelation) error {
	if !s.bundle.Contains(op.Source.ID) || !s.bundle.Contains(op.Target.ID) {
		return invalid("missing_relation_endpoint", []store.Diagnostic{{Code: "missing_relation_endpoint", Refs: []bundle.RelationRef{op.Source, op.Target}}}, errors.New("relation endpoint does not exist"))
	}
	sourcePath, ok, err := s.bundle.ConceptPathContext(s.ctx, op.Source.ID)
	if err != nil {
		return err
	}
	if !ok {
		return invalid("missing_source", nil, errors.New("source missing"))
	}
	data, ok, err := readBundleFileContext(s.ctx, s.bundle, sourcePath)
	if err != nil {
		return err
	}
	if !ok {
		return invalid("missing_source", nil, errors.New("source content missing"))
	}
	p, err := parsePresentationContext(s.ctx, data)
	if err != nil {
		code := relationPresentationCode(err)
		return s.presentationInvalid(code, []store.Diagnostic{{Code: code, Refs: []bundle.RelationRef{op.Source, op.Target}}}, err, sourcePath, "ensure_relation", data)
	}
	sourceMapping, err := mappingForRef(s.ctx, p, p.root, op.Source)
	if err != nil {
		code := relationPresentationCode(err)
		return s.presentationInvalid(code, []store.Diagnostic{{Code: code, Refs: []bundle.RelationRef{op.Source, op.Target}}}, err, sourcePath, "ensure_relation", data)
	}
	if sourceMapping == nil {
		return invalid("missing_source_fragment", []store.Diagnostic{{Code: "missing_source_fragment", Refs: []bundle.RelationRef{op.Source}}}, errors.New("fragment missing"))
	}
	if op.Target.Fragment != "" {
		targetPath, targetOK, targetPathErr := s.bundle.ConceptPathContext(s.ctx, op.Target.ID)
		if targetPathErr != nil {
			return targetPathErr
		}
		if !targetOK {
			return invalid("missing_relation_endpoint", []store.Diagnostic{{Code: "missing_relation_endpoint", Refs: []bundle.RelationRef{op.Source, op.Target}}}, errors.New("relation endpoint does not exist"))
		}
		targetData, targetDataOK, targetDataErr := readBundleFileContext(s.ctx, s.bundle, targetPath)
		if targetDataErr != nil {
			return targetDataErr
		}
		if !targetDataOK {
			return invalid("missing_relation_endpoint", []store.Diagnostic{{Code: "missing_relation_endpoint", Refs: []bundle.RelationRef{op.Source, op.Target}}}, errors.New("relation endpoint content missing"))
		}
		targetPresentation, err := parsePresentationContext(s.ctx, targetData)
		if err != nil {
			code := relationPresentationCode(err)
			return s.presentationInvalid(code, []store.Diagnostic{{Code: code, Refs: []bundle.RelationRef{op.Source, op.Target}}}, err, targetPath, "ensure_relation", targetData)
		}
		targetMapping, err := mappingForRef(s.ctx, targetPresentation, targetPresentation.root, op.Target)
		if err != nil {
			code := relationPresentationCode(err)
			return s.presentationInvalid(code, []store.Diagnostic{{Code: code, Refs: []bundle.RelationRef{op.Source, op.Target}}}, err, targetPath, "ensure_relation", targetData)
		}
		if targetMapping == nil {
			return invalid("missing_relation_endpoint", []store.Diagnostic{{Code: "missing_relation_endpoint", Refs: []bundle.RelationRef{op.Source, op.Target}}}, errors.New("relation endpoint does not exist"))
		}
	}
	updated, err := ensureRelationPresentationContext(s.ctx, data, op.Source, op.Type, op.Target.String())
	if err != nil {
		code := relationPresentationCode(err)
		return s.presentationInvalid(code, []store.Diagnostic{{Code: code, Refs: []bundle.RelationRef{op.Source, op.Target}}}, err, sourcePath, "ensure_relation", data)
	}
	equal, err := bytesEqualContext(s.ctx, updated, data)
	if err != nil {
		return err
	}
	if !equal {
		if err := s.overlay.PutContext(s.ctx, sourcePath, updated); err != nil {
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
	fromPath, ok, err := s.bundle.ConceptPathContext(s.ctx, op.From)
	if err != nil {
		return err
	}
	if !ok {
		return invalid("missing_concept", []store.Diagnostic{{Code: "missing_concept", Refs: []bundle.RelationRef{{ID: op.From}}}}, errors.New("concept missing"))
	}
	if s.bundle.Contains(op.To) {
		return invalid("target_concept_exists", nil, errors.New("move target exists"))
	}
	toPath := conceptPath(op.To)
	if err := s.overlay.Rename(s.ctx, fromPath, toPath); err != nil {
		return err
	}
	s.renames = append(s.renames, store.Rename{From: fromPath, To: toPath})
	s.add(bundle.RelationRef{ID: op.From}, bundle.RelationRef{ID: op.To})
	if err := s.ctx.Err(); err != nil {
		return err
	}
	reverseImpact, err := s.bundle.ReverseImpactConceptContext(s.ctx, op.From)
	if err != nil {
		return err
	}
	for _, rel := range reverseImpact {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		s.reverse = append(s.reverse, rel.Source)
	}
	yamlImpact, err := s.yamlImpactPathsContext(s.ctx, bundle.RelationRef{ID: op.From})
	if err != nil {
		return err
	}
	// Rewrite canonical semantic target refs in frontmatter, then rewrite every
	// Markdown destination which resolves to the moved document.
	markdownFiles, err := s.bundle.MarkdownFilesContext(s.ctx)
	if err != nil {
		return err
	}
	for _, file := range markdownFiles {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		data, ok, err := readBundleFileContext(s.ctx, s.bundle, file)
		if err != nil {
			return err
		}
		if !ok {
			return invalid("missing_captured_file", nil, fmt.Errorf("captured Markdown file %q is missing", file))
		}
		pathImpact, err := moveDocumentPathImpactContext(s.ctx, s.bundle, file, fromPath)
		if err != nil {
			return err
		}
		var documentPresentationErr error
		if documentlayout.HasOpeningDelimiter(data) {
			p, err := parsePresentationContext(s.ctx, data)
			if err != nil {
				documentPresentationErr = err
				_, semanticImpact := yamlImpact[file]
				if semanticImpact || pathImpact {
					return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "move_concept", data)
				}
			} else {
				patches, err := relationTargetPatchesContext(s.ctx, p, op.From, op.To, "", "")
				if err != nil {
					return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "move_concept", data)
				}
				updated, err := p.patchYAMLContext(s.ctx, patches)
				if err != nil {
					return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "move_concept", data)
				}
				out := file
				if out == fromPath {
					out = toPath
				}
				updated, err = rewriteV02PathValuesContext(s.ctx, s.bundle, file, out, fromPath, toPath, updated)
				if err != nil {
					return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "move_concept", data)
				}
				if !bytes.Equal(updated, data) {
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
			current = staged
		}
		rewritten, err := rewriteMarkdownDestinationsContext(s.ctx, current, func(destination string) (string, bool) {
			value := rewriteDestination(destination, file, out, op.From, op.To, file == fromPath)
			return value, value != destination
		})
		if err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, out, "move_concept", current)
		}
		equal, err := bytesEqualContext(s.ctx, rewritten, current)
		if err != nil {
			return err
		}
		if !equal {
			if documentPresentationErr != nil {
				return s.presentationInvalid(
					presentationChangeCode(documentPresentationErr),
					nil,
					documentPresentationErr,
					file,
					"move_concept",
					data,
				)
			}
			if err := s.overlay.PutContext(s.ctx, out, rewritten); err != nil {
				return err
			}
		}
	}
	s.plans = append(s.plans, store.OperationPlan{Operation: op, AffectedRefs: []bundle.RelationRef{{ID: op.From}, {ID: op.To}}, Details: []string{"moved concept and rewrote canonical references"}})
	return nil
}

func (s *planState) renameFragment(op store.RenameFragment) error {
	conceptFile, ok, err := s.bundle.ConceptPathContext(s.ctx, op.Concept)
	if err != nil {
		return err
	}
	if !ok {
		file := conceptPath(op.Concept)
		if data, readErr := s.overlay.ReadFile(s.ctx, file); readErr == nil {
			if _, err := parsePresentationContext(s.ctx, data); err != nil {
				return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "rename_fragment", data)
			}
		}
		return invalid("missing_concept", []store.Diagnostic{{Code: "missing_concept", Refs: []bundle.RelationRef{{ID: op.Concept}}}}, errors.New("concept missing"))
	}
	data, ok, err := readBundleFileContext(s.ctx, s.bundle, conceptFile)
	if err != nil {
		return err
	}
	if !ok {
		return invalid("missing_concept", []store.Diagnostic{{Code: "missing_concept", Refs: []bundle.RelationRef{{ID: op.Concept}}}}, errors.New("concept content missing"))
	}
	p, err := parsePresentationContext(s.ctx, data)
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, conceptFile, "rename_fragment", data)
	}
	targetAliases, err := canonicalAliasProvenanceContext(s.ctx, p.root, op.To)
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, conceptFile, "rename_fragment", data)
	}
	if len(targetAliases) != 0 {
		err := p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, targetAliases...)
		return s.presentationInvalid(presentationChangeCode(err), nil, err, conceptFile, "rename_fragment", data)
	}
	targetCandidates, err := canonicalFragmentCandidatesContext(s.ctx, p.root, op.To)
	if err != nil {
		return err
	}
	if len(targetCandidates) > 1 {
		err := p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, targetCandidates...)
		return s.presentationInvalid(presentationChangeCode(err), nil, err, conceptFile, "rename_fragment", data)
	}
	if len(targetCandidates) == 1 {
		return invalid("target_fragment_exists", nil, errors.New("target fragment exists"))
	}
	fragment, found, err := fragmentPatchContext(s.ctx, p, op.From, op.To)
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, conceptFile, "rename_fragment", data)
	}
	if !found {
		if !s.bundle.FragmentExists(op.Concept, op.From) {
			return invalid("missing_or_ambiguous_fragment", []store.Diagnostic{{Code: "missing_or_ambiguous_fragment", Refs: []bundle.RelationRef{{ID: op.Concept, Fragment: op.From}}}}, errors.New("fragment missing or ambiguous"))
		}
		return invalid("missing_fragment", nil, errors.New("fragment not found"))
	}
	patches, err := relationTargetPatchesContext(s.ctx, p, op.Concept, op.Concept, op.From, op.To)
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, conceptFile, "rename_fragment", data)
	}
	patches = append(patches, fragment)
	updated, err := p.patchYAMLContext(s.ctx, patches)
	if err != nil {
		return s.presentationInvalid(presentationChangeCode(err), nil, err, conceptFile, "rename_fragment", []byte(data))
	}
	if err := s.overlay.PutContext(s.ctx, conceptFile, updated); err != nil {
		return err
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	reverseImpact, err := s.bundle.ReverseImpactFragmentContext(s.ctx, op.Concept, op.From)
	if err != nil {
		return err
	}
	for _, rel := range reverseImpact {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		s.reverse = append(s.reverse, rel.Source)
	}
	yamlImpact, err := s.yamlImpactPathsContext(s.ctx, bundle.RelationRef{ID: op.Concept, Fragment: op.From})
	if err != nil {
		return err
	}
	markdownFiles, err := s.bundle.MarkdownFilesContext(s.ctx)
	if err != nil {
		return err
	}
	for _, file := range markdownFiles {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if file == conceptFile {
			continue
		}
		data, ok, err := readBundleFileContext(s.ctx, s.bundle, file)
		if err != nil {
			return err
		}
		if !ok {
			return invalid("missing_captured_file", nil, fmt.Errorf("captured Markdown file %q is missing", file))
		}
		if !documentlayout.HasOpeningDelimiter(data) {
			continue
		}
		p, err := parsePresentationContext(s.ctx, data)
		if err != nil {
			if _, need := yamlImpact[file]; need {
				return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "rename_fragment", data)
			}
			continue
		}
		patches, err := relationTargetPatchesContext(s.ctx, p, op.Concept, op.Concept, op.From, op.To)
		if err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "rename_fragment", data)
		}
		if len(patches) > 0 {
			updated, err := p.patchYAMLContext(s.ctx, patches)
			if err != nil {
				return s.presentationInvalid(presentationChangeCode(err), nil, err, file, "rename_fragment", data)
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]struct{})
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var rels []bundle.Relation
		var err error
		if ref.Fragment == "" {
			rels, err = s.bundle.ReverseImpactConceptContext(ctx, ref.ID)
		} else {
			rels, err = s.bundle.ReverseImpactFragmentContext(ctx, ref.ID, ref.Fragment)
		}
		if err != nil {
			return nil, err
		}
		for _, rel := range rels {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if s.bundle.Contains(rel.Source.ID) {
				out[conceptPath(rel.Source.ID)] = struct{}{}
			}
		}
	}
	return out, ctx.Err()
}

func moveDocumentPathImpactContext(ctx context.Context, loaded *bundle.Bundle, documentPath, movedFrom string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if documentPath == movedFrom {
		return true, nil
	}
	conceptID, err := bundle.ConceptIDFromPath("", documentPath)
	if err != nil {
		return false, nil
	}
	observations, found, err := loaded.ConceptPathValueObservationsContext(ctx, conceptID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	targetsMovedPath := func(raw string, field bundle.PathValueField) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if pathValueHasSurroundingWhitespace(raw) {
			return false, nil
		}
		resolved, ok := loaded.ResolvePathValueFor(documentPath, raw, field)
		if !ok || !resolved.Exists {
			return false, nil
		}
		if resolved.Kind != bundle.PathValueRelative && resolved.Kind != bundle.PathValueBundleRelative {
			return false, nil
		}
		return resolved.Path == movedFrom, nil
	}
	if observations.SourcesPresent && !observations.SourcesSequence {
		return true, nil
	}
	for _, observation := range observations.Values {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if observation.ParentPresent && !observation.ParentMapping {
			return true, nil
		}
		if observation.Scalar.Present && !observation.Scalar.Valid {
			return true, nil
		}
		if !observation.Scalar.Valid {
			continue
		}
		impacted, err := targetsMovedPath(observation.Scalar.Value, observation.Field)
		if err != nil || impacted {
			return impacted, err
		}
	}
	return false, ctx.Err()
}

func (s *planState) add(refs ...bundle.RelationRef) {
	s.affected = append(s.affected, refs...)
}
func (s *planState) previewContext(ctx context.Context, base, result store.Revision) (store.Preview, error) {
	if err := ctx.Err(); err != nil {
		return store.Preview{}, err
	}
	writes := make([]store.Write, 0, boundedProjectionCapacity(len(s.overlay.changed)))
	deletes := make([]string, 0, boundedProjectionCapacity(len(s.overlay.deleted)))
	renames := make([]store.Rename, 0, boundedProjectionCapacity(len(s.renames)))
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
	if err := sortSliceContext(ctx, writes, func(left, right store.Write) bool {
		return left.Path < right.Path
	}); err != nil {
		return store.Preview{}, err
	}
	if err := sortSliceContext(ctx, deletes, func(left, right string) bool {
		return left < right
	}); err != nil {
		return store.Preview{}, err
	}
	if err := sortSliceContext(ctx, renames, func(left, right store.Rename) bool {
		if left.From == right.From {
			return left.To < right.To
		}
		return left.From < right.From
	}); err != nil {
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
	if err := ctx.Err(); err != nil {
		return store.Preview{}, err
	}
	plans := make([]store.OperationPlan, 0, boundedProjectionCapacity(len(s.plans)))
	for _, plan := range s.plans {
		if err := ctx.Err(); err != nil {
			return store.Preview{}, err
		}
		plan.AffectedRefs, err = copyRefsContext(ctx, plan.AffectedRefs)
		if err != nil {
			return store.Preview{}, err
		}
		plan.Details, err = copyStringsContext(ctx, plan.Details)
		if err != nil {
			return store.Preview{}, err
		}
		plans = append(plans, plan)
	}
	return store.Preview{BaseRevision: base, ResultRevision: result, Reads: reads, Writes: writes, Deletes: deletes, Renames: renames, AffectedRefs: affected, ReverseImpact: reverse, Plan: plans}, ctx.Err()
}

func copyRefsContext(ctx context.Context, refs []bundle.RelationRef) ([]bundle.RelationRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]bundle.RelationRef, 0, boundedProjectionCapacity(len(refs)))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, ctx.Err()
}

func copyStringsContext(ctx context.Context, values []string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, boundedProjectionCapacity(len(values)))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, value)
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
	if err := ctx.Err(); err != nil {
		return "", err
	}
	entries := make([]store.ManifestEntry, 0, boundedProjectionCapacity(len(paths)))
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
		location, err := presentationRangeContext(s.ctx, data, format)
		if err != nil {
			return err
		}
		return invalid(code, diagnostics, &PresentationError{
			Code:      "parse_presentation",
			Format:    format,
			Path:      path,
			Operation: operation,
			Location:  location,
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
		offset, err := yamlFrontmatterStartContext(s.ctx, data)
		if err != nil {
			return err
		}
		copy.Location.Start += offset
		copy.Location.End += offset
	}
	if len(data) > 0 && (copy.Location == (SourceSpan{}) || copy.Location.Start == copy.Location.End) {
		if copy.Location == (SourceSpan{}) {
			location, err := presentationRangeContext(s.ctx, data, copy.Format)
			if err != nil {
				return err
			}
			copy.Location = location
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

func yamlFrontmatterStartContext(ctx context.Context, data []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	const chunkSize = 64 << 10
	for offset := 0; offset < len(data); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		end := min(offset+chunkSize, len(data))
		if relative := bytes.IndexByte(data[offset:end], '\n'); relative >= 0 {
			return offset + relative + 1, nil
		}
	}
	return len(data), ctx.Err()
}

func presentationRangeContext(ctx context.Context, data []byte, format string) (SourceSpan, error) {
	if err := ctx.Err(); err != nil {
		return SourceSpan{}, err
	}
	if len(data) == 0 {
		return SourceSpan{}, nil
	}
	if format == "markdown" {
		start, err := markdownBodyStartContext(ctx, data)
		if err == nil && start < len(data) {
			return SourceSpan{Start: start, End: len(data)}, ctx.Err()
		}
		if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return SourceSpan{}, err
		}
		return SourceSpan{Start: 0, End: len(data)}, ctx.Err()
	}
	if layout, ok, err := documentlayout.SplitContext(ctx, data); err == nil && ok && layout.YAMLStart < layout.YAMLEnd {
		return SourceSpan{Start: layout.YAMLStart, End: layout.YAMLEnd}, ctx.Err()
	} else if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return SourceSpan{}, err
	}
	return SourceSpan{Start: 0, End: len(data)}, ctx.Err()
}
func refsForContext(ctx context.Context, in []bundle.RelationRef) ([]bundle.RelationRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[relationRefKey]bundle.RelationRef, boundedProjectionCapacity(len(in)))
	for _, r := range in {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := relationRefIdentity(r)
		if key.id != "" {
			seen[key] = r
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, r := range seen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := sortRelationRefsContext(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// sortRelationRefsContext performs the deterministic semantic-ref ordering with
// cancellation checks throughout comparison and materialization. sort.Slice
// cannot stop once its comparator observes cancellation, which makes it an
// unsuitable boundary for a large conflict namespace.
func sortRelationRefsContext(ctx context.Context, refs []bundle.RelationRef) error {
	return sortSliceContext(ctx, refs, func(left, right bundle.RelationRef) bool {
		return relationRefKeyLess(relationRefIdentity(left), relationRefIdentity(right))
	})
}

type relationRefKey struct {
	id       string
	fragment string
}

func relationRefIdentity(ref bundle.RelationRef) relationRefKey {
	return relationRefKey{id: ref.ID.String(), fragment: ref.Fragment}
}

func relationRefKeyLess(left, right relationRefKey) bool {
	if left.id != right.id {
		return left.id < right.id
	}
	return left.fragment < right.fragment
}

func relationRefEqual(left, right bundle.RelationRef) bool {
	return relationRefIdentity(left) == relationRefIdentity(right)
}

func sortSliceContext[T any](ctx context.Context, values []T, less func(left, right T) bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) < 2 {
		return nil
	}
	for root := len(values)/2 - 1; root >= 0; root-- {
		if err := siftSliceContext(ctx, values, less, root, len(values)-1); err != nil {
			return err
		}
	}
	for end := len(values) - 1; end > 0; end-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		values[0], values[end] = values[end], values[0]
		if err := siftSliceContext(ctx, values, less, 0, end-1); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// sortSliceCompareContext is a deterministic in-place heapsort whose
// comparator may stop expensive comparisons. Callers sort only private
// projections and must discard them when an error is returned.
func sortSliceCompareContext[T any](
	ctx context.Context,
	values []T,
	compare func(context.Context, T, T) (int, error),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) < 2 {
		return nil
	}
	for root := len(values)/2 - 1; root >= 0; root-- {
		if err := siftSliceCompareContext(ctx, values, compare, root, len(values)-1); err != nil {
			return err
		}
	}
	for end := len(values) - 1; end > 0; end-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		values[0], values[end] = values[end], values[0]
		if err := siftSliceCompareContext(ctx, values, compare, 0, end-1); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func siftSliceCompareContext[T any](
	ctx context.Context,
	values []T,
	compare func(context.Context, T, T) (int, error),
	root, end int,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := root*2 + 1
		if child > end {
			return nil
		}
		if child+1 <= end {
			order, err := compare(ctx, values[child], values[child+1])
			if err != nil {
				return err
			}
			if order < 0 {
				child++
			}
		}
		order, err := compare(ctx, values[root], values[child])
		if err != nil {
			return err
		}
		if order >= 0 {
			return nil
		}
		values[root], values[child] = values[child], values[root]
		root = child
	}
}

func compareStringsContext(ctx context.Context, left, right string) (int, error) {
	const chunkSize = 64 << 10
	limit := min(len(left), len(right))
	for offset := 0; offset < limit; offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		end := min(offset+chunkSize, limit)
		if order := strings.Compare(left[offset:end], right[offset:end]); order != 0 {
			return order, nil
		}
	}
	if len(left) < len(right) {
		return -1, ctx.Err()
	}
	if len(left) > len(right) {
		return 1, ctx.Err()
	}
	return 0, ctx.Err()
}

func sortStringsContext(ctx context.Context, values []string) error {
	return sortSliceCompareContext(ctx, values, compareStringsContext)
}

func searchStringsContext(ctx context.Context, values []string, target string) (int, error) {
	low, high := 0, len(values)
	for low < high {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		middle := low + (high-low)/2
		order, err := compareStringsContext(ctx, values[middle], target)
		if err != nil {
			return 0, err
		}
		if order < 0 {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low, ctx.Err()
}

func siftSliceContext[T any](ctx context.Context, values []T, less func(left, right T) bool, root, end int) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := root*2 + 1
		if child > end {
			return nil
		}
		if child+1 <= end {
			if err := ctx.Err(); err != nil {
				return err
			}
			if less(values[child], values[child+1]) {
				child++
			}
		}
		if !less(values[root], values[child]) {
			return nil
		}
		values[root], values[child] = values[child], values[root]
		root = child
	}
}

func boundedProjectionCapacity(length int) int {
	const initialCapacity = 64
	return min(length, initialCapacity)
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
			relationExists, relationErr := hasRelationContext(ctx, b, p.Source, p.Type, p.Target)
			if relationErr != nil {
				return relationErr
			}
			ok = relationExists
			refs = []bundle.RelationRef{p.Source, p.Target}
		case store.RelationAbsent:
			relationExists, relationErr := hasRelationContext(ctx, b, p.Source, p.Type, p.Target)
			if relationErr != nil {
				return relationErr
			}
			ok = !relationExists
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
func hasRelationContext(ctx context.Context, b *bundle.Bundle, source bundle.RelationRef, typ string, target bundle.RelationRef) (bool, error) {
	relations, err := b.SemanticLinksFromContext(ctx, source.ID)
	if err != nil {
		return false, err
	}
	for _, r := range relations {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if relationRefEqual(r.Source, source) && r.Type == typ && relationRefEqual(r.Target, target) {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func readBundleFileContext(ctx context.Context, loaded *bundle.Bundle, file string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return loaded.ReadFileContext(ctx, file)
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
	err = walkMappingsContext(ctx, root, excludeRootMapping, func(n *yaml.Node) error {
		if err := p.rejectMergedCanonicalFragmentContext(ctx, n, ref.Fragment); err != nil {
			return err
		}
		identity := bundle.ResolveMappingIdentity(n)
		if identity.State == bundle.MappingIdentityInvalid {
			mentions, err := identityMentionsFragmentContext(ctx, n, ref.Fragment)
			if err != nil {
				return err
			}
			if mentions {
				duplicate, err := duplicateCanonicalIdentityCandidateContext(ctx, n, ref.Fragment)
				if err != nil {
					return err
				}
				if duplicate {
					return p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, identityKeyNodes(n)...)
				} else {
					return p.unsupportedIdentityPresentationForFragmentContext(ctx, n, ref.Fragment)
				}
			}
			return nil
		}
		if identity.State == bundle.MappingIdentityValid && identity.Fragment == ref.Fragment {
			if err := p.requireTouchedProvenance(identity.Node); err != nil {
				return err
			}
			if _, err := p.resolver.scalar(identity.Node); err != nil {
				return err
			}
			found = append(found, n)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(found) > 1 {
		return nil, p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, found...)
	}
	if len(found) == 0 {
		return nil, nil
	}
	return found[0], nil
}

func duplicateCanonicalIdentityCandidateContext(ctx context.Context, n *yaml.Node, fragment string) (bool, error) {
	valid := func(key string) ([]string, error) {
		values := make([]string, 0)
		mappingValues, err := mappingValuesForKeyContext(ctx, n, key)
		if err != nil {
			return nil, err
		}
		for _, value := range mappingValues {
			if value != nil && value.Kind == yaml.ScalarNode && value.Tag == "!!str" && bundle.ValidateRelationFragment(value.Value) == nil {
				values = append(values, value.Value)
			}
		}
		return values, ctx.Err()
	}
	ids, err := valid("id")
	if err != nil {
		return false, err
	}
	if len(ids) > 0 {
		return len(ids) > 1 && stringSliceContains(ids, fragment), ctx.Err()
	}
	anchors, err := valid("anchor")
	if err != nil {
		return false, err
	}
	return len(anchors) > 1 && stringSliceContains(anchors, fragment), ctx.Err()
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

type mappingRootPolicy bool

const (
	excludeRootMapping mappingRootPolicy = false
	includeRootMapping mappingRootPolicy = true
)

// walkMappingsContext visits mappings in source pre-order. Excluding the root
// keeps document id/anchor metadata out of fragment identity traversal.
func walkMappingsContext(ctx context.Context, root *yaml.Node, policy mappingRootPolicy, visitor func(*yaml.Node) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if root == nil {
		return nil
	}
	if policy == includeRootMapping && root.Kind == yaml.MappingNode {
		if err := visitor(root); err != nil {
			return err
		}
	}
	switch root.Kind {
	case yaml.MappingNode:
		for index := 1; index < len(root.Content); index += 2 {
			if err := walkMappingsContext(ctx, root.Content[index], includeRootMapping, visitor); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range root.Content {
			if err := walkMappingsContext(ctx, child, includeRootMapping, visitor); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
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
func (p *presentation) unsupportedIdentityPresentationForFragmentContext(ctx context.Context, n *yaml.Node, from string) error {
	if from != "" {
		mentions, err := identityMentionsFragmentContext(ctx, n, from)
		if err != nil || !mentions {
			return err
		}
	}
	for _, key := range []string{"id", "anchor"} {
		values, err := mappingValuesForKeyContext(ctx, n, key)
		if err != nil {
			return err
		}
		for _, value := range values {
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
	out := make([]store.Diagnostic, 0, boundedProjectionCapacity(len(validation.Diagnostics)+len(relations)))
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keyed := make([]keyedDiagnostic, 0, boundedProjectionCapacity(len(out)))
	for _, diagnostic := range out {
		key, refs, canonicalRefs, err := diagnosticKeyContext(ctx, diagnostic)
		if err != nil {
			return nil, err
		}
		if len(canonicalRefs) != 0 {
			diagnostic.Refs = canonicalRefs
		}
		keyed = append(keyed, keyedDiagnostic{diagnostic: diagnostic, key: key, refs: refs})
	}
	if err := sortSliceContext(ctx, keyed, func(left, right keyedDiagnostic) bool {
		return diagnosticKeyLess(left, right)
	}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	unique := make([]store.Diagnostic, 0, boundedProjectionCapacity(len(keyed)))
	var lastKey diagnosticIdentity
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

type diagnosticIdentity struct {
	kind         store.DiagnosticKind
	severity     store.DiagnosticSeverity
	code         string
	file         string
	relationType string
	rawTarget    string
	message      string
	refsFrame    string
}

type keyedDiagnostic struct {
	diagnostic store.Diagnostic
	key        diagnosticIdentity
	refs       []relationRefKey
}

func diagnosticKeyContext(ctx context.Context, diagnostic store.Diagnostic) (diagnosticIdentity, []relationRefKey, []bundle.RelationRef, error) {
	refs, err := refsForContext(ctx, diagnostic.Refs)
	if err != nil {
		return diagnosticIdentity{}, nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return diagnosticIdentity{}, nil, nil, err
	}
	parts := make([]relationRefKey, 0, boundedProjectionCapacity(len(refs)))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return diagnosticIdentity{}, nil, nil, err
		}
		parts = append(parts, relationRefIdentity(ref))
	}
	refsFrame, err := lengthFrameRelationRefKeysContext(ctx, parts)
	if err != nil {
		return diagnosticIdentity{}, nil, nil, err
	}
	return diagnosticIdentity{
		kind:         diagnostic.Kind,
		severity:     diagnostic.Severity,
		code:         diagnostic.Code,
		file:         diagnostic.File,
		relationType: diagnostic.RelationType,
		rawTarget:    diagnostic.RawTarget,
		message:      diagnostic.Message,
		refsFrame:    refsFrame,
	}, parts, refs, nil
}

func diagnosticKeyLess(left, right keyedDiagnostic) bool {
	switch {
	case left.key.kind != right.key.kind:
		return left.key.kind < right.key.kind
	case left.key.severity != right.key.severity:
		return left.key.severity < right.key.severity
	case left.key.code != right.key.code:
		return left.key.code < right.key.code
	case left.key.file != right.key.file:
		return left.key.file < right.key.file
	case left.key.relationType != right.key.relationType:
		return left.key.relationType < right.key.relationType
	case left.key.rawTarget != right.key.rawTarget:
		return left.key.rawTarget < right.key.rawTarget
	case left.key.message != right.key.message:
		return left.key.message < right.key.message
	default:
		return relationRefKeySliceLess(left.refs, right.refs)
	}
}

func relationRefKeySliceLess(left, right []relationRefKey) bool {
	limit := min(len(left), len(right))
	for index := 0; index < limit; index++ {
		if left[index] != right[index] {
			return relationRefKeyLess(left[index], right[index])
		}
	}
	return len(left) < len(right)
}

func lengthFrameRelationRefKeysContext(ctx context.Context, refs []relationRefKey) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	values := make([]string, 0, boundedProjectionCapacity(2*len(refs)))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		values = append(values, ref.id, ref.fragment)
	}
	return lengthFrameStringsContext(ctx, values)
}

func lengthFrameStringsContext(ctx context.Context, values []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var framed []byte
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		framed = strconv.AppendInt(framed, int64(len(value)), 10)
		framed = append(framed, ':')
		framed = append(framed, value...)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(framed), nil
}
