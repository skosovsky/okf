package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/documentlayout"
	"github.com/skosovsky/okf/internal/markdownowner"
	"github.com/skosovsky/okf/internal/receiptprojection"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
	"gopkg.in/yaml.v3"
)

const (
	MigrationVersionV01 = "0.1"
	MigrationVersionV02 = "0.2"
)

func bytesStringContext(ctx context.Context, source []byte) (string, error) {
	const chunkSize = 64 << 10
	var out bytes.Buffer
	out.Grow(len(source))
	for len(source) > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := min(len(source), chunkSize)
		_, _ = out.Write(source[:n])
		source = source[n:]
	}
	return out.String(), ctx.Err()
}

var (
	// ErrMigrationPlanMismatch means Apply did not reproduce the exact preview
	// authorized by the caller.
	ErrMigrationPlanMismatch = ErrPlanDigestMismatch
	// ErrUnsupportedMigrationVersion means the closed migration only supports
	// the v0.1 to v0.2 transition.
	ErrUnsupportedMigrationVersion = errors.New("unsupported migration version")
)

// MigrationRequest is the complete caller-owned v0.1 to v0.2 request. Actor is
// the transaction principal; Migration.GeneratedBy is the document producer
// and is never inferred from Actor.
type MigrationRequest struct {
	ID          store.ChangeSetID
	Actor       store.Actor
	FromVersion string
	ToVersion   string
	Migration   store.V01ToV02Migration
}

// MigrationBlocker is a stable fail-closed preview finding.
type MigrationBlocker struct {
	Code     string
	Path     string
	Location SourceSpan
	Message  string
}

// MigrationManualAction describes explicit caller input needed to unblock a
// migration. It is advisory and never authorizes a mutation.
type MigrationManualAction struct {
	Code    string
	Path    string
	Message string
}

// MigrationInputPreflight is the read-only, document-aware projection for
// caller-supplied migration input. It carries no authorization proof and
// cannot be used to apply a transition.
type MigrationInputPreflight struct {
	Blockers      []MigrationBlocker
	ManualActions []MigrationManualAction
}

// MigrationInputPreflightMode is the closed document interpretation selected
// by a previously computed MigrationSourceResolution.
type MigrationInputPreflightMode string

const (
	// MigrationInputPreflightTransition validates input as a prospective v0.1
	// to v0.2 rewrite.
	MigrationInputPreflightTransition MigrationInputPreflightMode = "transition"
	// MigrationInputPreflightTargetReplay validates input as an assertion about
	// documents that are already at the resolved target.
	MigrationInputPreflightTargetReplay MigrationInputPreflightMode = "target-replay"
	// MigrationInputPreflightSourceDiagnostics aggregates parser/ownership
	// findings when source resolution itself failed. It never interprets valid
	// sibling documents as either transition input or target replay.
	MigrationInputPreflightSourceDiagnostics MigrationInputPreflightMode = "source-diagnostics"
)

// MigrationPreview is a read-only, deterministic migration plan. PlanDigest
// binds the request digest, exact base/result revisions, dependency reads, and
// every staged file transition.
type MigrationPreview struct {
	Preview       store.Preview
	Proof         MigrationPlanProof
	PlanDigest    string
	Resolution    MigrationSourceResolution
	Blockers      []MigrationBlocker
	ManualActions []MigrationManualAction
	Staged        bundle.Source
}

func cloneStorePreviewContext(ctx context.Context, preview store.Preview) (store.Preview, error) {
	var err error
	if preview.Reads, err = cloneMigrationProofSliceContext(ctx, preview.Reads); err != nil {
		return store.Preview{}, err
	}
	if preview.Writes, err = cloneMigrationProofSliceContext(ctx, preview.Writes); err != nil {
		return store.Preview{}, err
	}
	for index := range preview.Writes {
		if preview.Writes[index].Content, err = cloneMigrationProofSliceContext(ctx, preview.Writes[index].Content); err != nil {
			return store.Preview{}, err
		}
	}
	if preview.Deletes, err = cloneMigrationProofSliceContext(ctx, preview.Deletes); err != nil {
		return store.Preview{}, err
	}
	if preview.Renames, err = cloneMigrationProofSliceContext(ctx, preview.Renames); err != nil {
		return store.Preview{}, err
	}
	if preview.AffectedRefs, err = cloneMigrationProofSliceContext(ctx, preview.AffectedRefs); err != nil {
		return store.Preview{}, err
	}
	if preview.ReverseImpact, err = cloneMigrationProofSliceContext(ctx, preview.ReverseImpact); err != nil {
		return store.Preview{}, err
	}
	if preview.Plan, err = cloneMigrationProofSliceContext(ctx, preview.Plan); err != nil {
		return store.Preview{}, err
	}
	for index := range preview.Plan {
		if preview.Plan[index].AffectedRefs, err = cloneMigrationProofSliceContext(ctx, preview.Plan[index].AffectedRefs); err != nil {
			return store.Preview{}, err
		}
		if preview.Plan[index].Details, err = cloneMigrationProofSliceContext(ctx, preview.Plan[index].Details); err != nil {
			return store.Preview{}, err
		}
	}
	if preview.Diagnostics, err = cloneMigrationProofSliceContext(ctx, preview.Diagnostics); err != nil {
		return store.Preview{}, err
	}
	for index := range preview.Diagnostics {
		if preview.Diagnostics[index].Refs, err = cloneMigrationProofSliceContext(ctx, preview.Diagnostics[index].Refs); err != nil {
			return store.Preview{}, err
		}
	}
	return preview, ctx.Err()
}

func cloneMigrationPreviewContext(ctx context.Context, preview MigrationPreview) (MigrationPreview, error) {
	var err error
	if preview.Preview, err = cloneStorePreviewContext(ctx, preview.Preview); err != nil {
		return MigrationPreview{}, err
	}
	if preview.Proof, err = preview.Proof.cloneContext(ctx); err != nil {
		return MigrationPreview{}, err
	}
	if preview.Resolution, err = preview.Resolution.cloneContext(ctx); err != nil {
		return MigrationPreview{}, err
	}
	if preview.Blockers, err = cloneMigrationProofSliceContext(ctx, preview.Blockers); err != nil {
		return MigrationPreview{}, err
	}
	if preview.ManualActions, err = cloneMigrationProofSliceContext(ctx, preview.ManualActions); err != nil {
		return MigrationPreview{}, err
	}
	return preview, ctx.Err()
}

// MigrationApplyRequest authorizes exactly one previously previewed plan.
type MigrationApplyRequest struct {
	Request    MigrationRequest
	Resolution MigrationSourceResolution
	Proof      MigrationPlanProof
	PlanDigest string
	Options    store.CommitOptions
}

// MigrationPlanner owns the migration-specific preview/apply contract.
type MigrationPlanner struct {
	ValidatorConfig *validator.ValidatorConfig
}

func NewMigrationPlanner() MigrationPlanner { return MigrationPlanner{} }

// ValidateV01ToV02MigrationInput validates the complete caller-supplied
// migration domain input without requiring a transition-only producer.
// Validation is canonical and does not mutate input.
func ValidateV01ToV02MigrationInput(input store.V01ToV02Migration) error {
	return validateV01ToV02MigrationInputContext(context.Background(), input)
}

func validateV01ToV02MigrationInputContext(ctx context.Context, input store.V01ToV02Migration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	operation, err := canonicalV01ToV02MigrationInput(input)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return validateV01ToV02CitationPresentationContext(ctx, operation.Migration().Citations)
}

// PreflightV01ToV02MigrationInput validates supplied input against the exact
// live documents in the explicit mode selected by resolution. Callers must
// pass the same MigrationSourceResolution used to choose transition/noop
// behavior. The preflight never stages, proves, digests, or writes.
func PreflightV01ToV02MigrationInput(
	ctx context.Context,
	source bundle.Source,
	resolution MigrationSourceResolution,
	input store.V01ToV02Migration,
) (MigrationInputPreflight, error) {
	if err := validateV01ToV02MigrationInputContext(ctx, input); err != nil {
		return MigrationInputPreflight{}, err
	}
	operation, err := canonicalV01ToV02MigrationInput(input)
	if err != nil {
		return MigrationInputPreflight{}, err
	}
	migration := operation.Migration()
	if source == nil {
		return MigrationInputPreflight{}, errors.New("mutation: nil migration source")
	}
	mode, err := migrationInputPreflightModeContext(ctx, resolution)
	if err != nil {
		return MigrationInputPreflight{}, err
	}
	loaded, err := bundle.Load(ctx, source)
	if err != nil {
		return MigrationInputPreflight{}, err
	}
	if err := validateMigrationResolutionAgainstBundle(ctx, loaded, resolution); err != nil {
		return MigrationInputPreflight{}, err
	}
	blockers, actions, err := migrationExplicitInputBlockers(ctx, loaded, migration, mode)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return MigrationInputPreflight{}, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return MigrationInputPreflight{}, ctxErr
	}
	return MigrationInputPreflight{
		Blockers:      blockers,
		ManualActions: actions,
	}, err
}

func migrationInputPreflightModeContext(ctx context.Context, resolution MigrationSourceResolution) (MigrationInputPreflightMode, error) {
	if err := validateMigrationSourceResolutionContext(ctx, resolution); err != nil {
		return "", err
	}
	switch resolution.Transition {
	case MigrationTransitionV01ToV02:
		return MigrationInputPreflightTransition, nil
	case MigrationTransitionTargetNoop:
		return MigrationInputPreflightTargetReplay, nil
	case MigrationTransitionBlocked:
		return "", fmt.Errorf("%w: source resolution is blocked", ErrUnsupportedMigrationVersion)
	default:
		return "", fmt.Errorf(
			"%w: preflight requires a resolved transition, got %q",
			ErrUnsupportedMigrationVersion,
			resolution.Transition,
		)
	}
}

func canonicalV01ToV02MigrationInput(input store.V01ToV02Migration) (store.MigrateV01ToV02, error) {
	return store.NewMigrateV01ToV02(input)
}

func validateV01ToV02CitationPresentationContext(
	ctx context.Context,
	documents []store.DocumentCitationMigration,
) error {
	for _, document := range documents {
		if err := ctx.Err(); err != nil {
			return err
		}
		mappings := make([]legacyCitationMapping, len(document.Entries))
		for index, entry := range document.Entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			mapping := legacyCitationMapping{
				LegacyNumber: entry.LegacyNumber,
				LegacyEntry:  entry.LegacyEntry,
				SourceID:     entry.SourceID,
				Title:        entry.Title,
				Resource:     entry.Resource,
			}
			validSourceID, err := validCitationSourceIDContext(ctx, mapping.SourceID)
			if err != nil {
				return err
			}
			if !validSourceID {
				return migrationCitationInputPresentationError(
					document.Path,
					markdownPresentationErrorAt("invalid_source_id", ErrUnsupportedPresentation, SourceSpan{}),
				)
			}
			if _, err := renderFootnoteDefinitionContext(ctx, mapping, MarkdownCitationEntry{}, "\n"); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return err
				}
				return migrationCitationInputPresentationError(
					document.Path,
					markdownPresentationErrorAt("unsupported_footnote_definition", ErrUnsupportedPresentation, SourceSpan{}),
				)
			}
			mappings[index] = mapping
		}
		if err := validateFootnoteIdentitiesContext(ctx, MarkdownMigrationOwnership{}, nil, mappings); err != nil {
			return migrationCitationInputPresentationError(document.Path, err)
		}
	}
	return ctx.Err()
}

func migrationCitationInputPresentationError(path string, err error) error {
	var presentation *PresentationError
	if !errors.As(err, &presentation) {
		return err
	}
	owned := *presentation
	owned.Path = path
	owned.Operation = "validate_v01_to_v02_migration_input"
	return &owned
}

// legacyCitationsActive is the single migration-domain authority for legacy
// citation presence. Numeric markers are operands of an already-owned
// citations section; they are never independent legacy evidence.
func legacyCitationsActive(sectionIndex int) bool {
	return sectionIndex >= 0
}

// Preview is read-only. The caller must pass the exact source resolution
// computed before preview; Preview never resolves or reinterprets it.
func (p MigrationPlanner) Preview(
	ctx context.Context,
	source bundle.Source,
	resolution MigrationSourceResolution,
	request MigrationRequest,
) (MigrationPreview, error) {
	if source == nil {
		return MigrationPreview{}, errors.New("mutation: nil migration source")
	}
	if err := validateMigrationSourceResolutionContext(ctx, resolution); err != nil {
		return MigrationPreview{}, err
	}
	if err := validateV01ToV02MigrationInputContext(ctx, request.Migration); err != nil {
		if errors.Is(err, ErrAmbiguousPresentation) {
			blocked, cloneErr := cloneMigrationPreviewContext(ctx, MigrationPreview{
				Blockers:      migrationBlockers(err),
				ManualActions: migrationManualActions(err),
			})
			if cloneErr != nil {
				return MigrationPreview{}, cloneErr
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return MigrationPreview{}, ctxErr
			}
			return blocked, err
		}
		return MigrationPreview{}, err
	}
	base, err := revision(ctx, source)
	if err != nil {
		return MigrationPreview{}, err
	}
	change, err := migrationChangeSet(request, base)
	if err != nil {
		return MigrationPreview{}, err
	}
	baseBundle, err := bundle.Load(ctx, source)
	if err != nil {
		return MigrationPreview{}, err
	}
	if err := validateMigrationResolutionAgainstBundle(ctx, baseBundle, resolution); err != nil {
		return MigrationPreview{}, err
	}
	mode := MigrationInputPreflightSourceDiagnostics
	if len(resolution.Blockers) == 0 &&
		resolution.Transition != MigrationTransitionBlocked &&
		resolution.ResolutionSource != "" {
		mode, err = migrationInputPreflightModeContext(ctx, resolution)
		if err != nil {
			return MigrationPreview{}, err
		}
	}
	inputBlockers, inputActions, inputErr := migrationExplicitInputBlockers(
		ctx,
		baseBundle,
		request.Migration,
		mode,
	)
	if errors.Is(inputErr, context.Canceled) || errors.Is(inputErr, context.DeadlineExceeded) {
		return MigrationPreview{}, inputErr
	}
	if inputErr != nil && mode == MigrationInputPreflightTransition {
		replayBlockers, replayActions, replayErr := migrationExplicitInputBlockers(
			ctx,
			baseBundle,
			request.Migration,
			MigrationInputPreflightTargetReplay,
		)
		if errors.Is(replayErr, context.Canceled) || errors.Is(replayErr, context.DeadlineExceeded) {
			return MigrationPreview{}, replayErr
		}
		if replayErr == nil {
			inputBlockers, inputActions, inputErr = replayBlockers, replayActions, nil
		}
	}
	if len(resolution.Blockers) != 0 {
		combined := make([]MigrationBlocker, 0, len(resolution.Blockers)+len(inputBlockers))
		combined = append(combined, resolution.Blockers...)
		combined = append(combined, inputBlockers...)
		inputBlockers = combined
		inputErr = fmt.Errorf("%w: frozen source resolution is blocked", ErrUnsupportedMigrationVersion)
	}
	if inputErr != nil {
		paths := baseBundle.Files()
		reads := make([]store.Read, len(paths))
		for index, path := range paths {
			if err := ctx.Err(); err != nil {
				return MigrationPreview{}, err
			}
			reads[index] = store.Read{Path: path}
		}
		preview, cloneErr := cloneMigrationPreviewContext(ctx, MigrationPreview{
			Preview:       store.Preview{BaseRevision: base, ResultRevision: base, Reads: reads},
			Resolution:    resolution,
			Blockers:      inputBlockers,
			ManualActions: inputActions,
		})
		if cloneErr != nil {
			return MigrationPreview{}, cloneErr
		}
		if err := ctx.Err(); err != nil {
			return MigrationPreview{}, err
		}
		return preview, inputErr
	}
	resolutionCopy, err := resolution.cloneContext(ctx)
	if err != nil {
		return MigrationPreview{}, err
	}
	ctx = context.WithValue(ctx, migrationResolutionContextKey{}, resolutionCopy)
	result, err := NewPlanner(p.ValidatorConfig).Plan(ctx, source, change)
	if err != nil {
		preview, cloneErr := cloneMigrationPreviewContext(ctx, MigrationPreview{
			Preview:       result.Preview,
			Resolution:    resolution,
			Blockers:      migrationBlockers(err),
			ManualActions: migrationManualActions(err),
		})
		if cloneErr != nil {
			return MigrationPreview{}, cloneErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return MigrationPreview{}, ctxErr
		}
		return preview, err
	}
	targetBundle, err := bundle.Load(ctx, result.Staged)
	if err != nil {
		return MigrationPreview{}, err
	}
	projection, err := receiptprojection.Derive(ctx, baseBundle, targetBundle)
	if err != nil {
		return MigrationPreview{}, err
	}
	proof, err := newMigrationPlanProofContext(ctx, change, result.Preview, projection, resolution)
	if err != nil {
		return MigrationPreview{}, err
	}
	digest, err := migrationPlanProofDigestContext(ctx, change, proof, resolution)
	if err != nil {
		return MigrationPreview{}, err
	}
	assembled, err := cloneMigrationPreviewContext(ctx, MigrationPreview{
		Preview:    result.Preview,
		Proof:      proof,
		PlanDigest: digest,
		Resolution: resolution,
		Staged:     result.Staged,
	})
	if err != nil {
		return MigrationPreview{}, err
	}
	if err := ctx.Err(); err != nil {
		return MigrationPreview{}, err
	}
	return assembled, nil
}

func migrationExplicitInputBlockers(
	ctx context.Context,
	loaded *bundle.Bundle,
	request store.V01ToV02Migration,
	mode MigrationInputPreflightMode,
) ([]MigrationBlocker, []MigrationManualAction, error) {
	citationMappings, err := migrationCitationMappingsContext(ctx, request.Citations)
	if err != nil {
		return nil, nil, err
	}
	generatedAt, err := migrationGeneratedAtContext(ctx, request.GeneratedAt)
	if err != nil {
		return nil, nil, err
	}
	computations, err := migrationComputationsContext(ctx, request.Computations)
	if err != nil {
		return nil, nil, err
	}
	var blockers []MigrationBlocker
	var actions []MigrationManualAction
	var causes []error
	paths, pathsErr := migrationPreflightPathsContext(ctx, loaded, request)
	if pathsErr != nil {
		return nil, nil, pathsErr
	}
	targetReplay := mode == MigrationInputPreflightTargetReplay
	existingPaths := make(map[string]bool, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		source, ok, readErr := loaded.ReadFileContext(ctx, path)
		if readErr != nil {
			return nil, nil, readErr
		}
		if !ok {
			continue
		}
		existingPaths[path] = true
		text, textErr := bytesStringContext(ctx, source)
		if textErr != nil {
			return nil, nil, textErr
		}
		document, parseErr := bundle.ParseDocumentContext(ctx, text)
		if parseErr != nil {
			if errors.Is(parseErr, bundle.ErrInvalidEncoding) {
				_, ownershipErr := CollectMarkdownMigrationOwnership(ctx, source)
				blocker, action, cause, classified := migrationCitationOwnershipFinding(path, ownershipErr)
				if classified {
					blockers = append(blockers, blocker)
					actions = append(actions, action)
					causes = append(causes, cause)
					continue
				}
			}
			blocker, action, cause := migrationDocumentPreflightFinding(ctx, path, source, parseErr)
			blockers = append(blockers, blocker)
			if action.Code != "" {
				actions = append(actions, action)
			}
			causes = append(causes, cause)
			continue
		}
		fallback := document.LegacyFallbackObservation()
		ownership, ownershipErr := CollectMarkdownMigrationOwnership(ctx, source)
		if ownershipErr != nil {
			blocker, action, cause, classified := migrationCitationOwnershipFinding(path, ownershipErr)
			if classified {
				blockers = append(blockers, blocker)
				actions = append(actions, action)
				causes = append(causes, cause)
				continue
			}
			blocker, action, cause = migrationDocumentPreflightFinding(ctx, path, source, ownershipErr)
			blockers = append(blockers, blocker)
			if action.Code != "" {
				actions = append(actions, action)
			}
			causes = append(causes, cause)
			continue
		}
		var location SourceSpan
		hasLegacy := legacyCitationsActive(ownership.CitationsSectionIndex)
		if hasLegacy {
			location = ownership.Sections[ownership.CitationsSectionIndex].Span
		}
		if hasLegacy && fallback.SourcesPresent {
			const message = "document contains both structured sources and legacy # Citations; normalize to one explicit representation before migration"
			presentation := &PresentationError{
				Code:      "sources_citations_conflict",
				Format:    "markdown",
				Path:      path,
				Operation: "migrate_v01_to_v02",
				Location:  location,
				Err:       ErrAmbiguousPresentation,
			}
			blockers = append(blockers, MigrationBlocker{
				Code: presentation.Code, Path: path, Location: location, Message: message,
			})
			actions = append(actions, MigrationManualAction{
				Code: "reconcile_sources_and_citations", Path: path, Message: message,
			})
			causes = append(causes, presentation)
			continue
		}
		if mapped, ok := citationMappings[path]; ok {
			if hasLegacy {
				_, resolutionErr := resolvedCitationEntries(ctx, source, mapped.Entries)
				if resolutionErr == nil {
					continue
				}
				blocker, action, cause, classified := migrationCitationMappingFinding(path, resolutionErr)
				if classified {
					blockers = append(blockers, blocker)
					actions = append(actions, action)
					causes = append(causes, cause)
					continue
				}
				blocker, action, cause = migrationDocumentPreflightFinding(ctx, path, source, resolutionErr)
				blockers = append(blockers, blocker)
				if action.Code != "" {
					actions = append(actions, action)
				}
				causes = append(causes, cause)
				continue
			}
			continue
		}
		if hasLegacy {
			presentation := &PresentationError{
				Code:      "missing_explicit_citation_mapping",
				Format:    "markdown",
				Path:      path,
				Operation: "migrate_v01_to_v02",
				Location:  location,
				Err:       ErrUnsupportedPresentation,
			}
			blockers = append(blockers, MigrationBlocker{
				Code: presentation.Code, Path: path, Location: location, Message: presentation.Error(),
			})
			actions = append(actions, MigrationManualAction{
				Code: "provide_citation_mapping", Path: path,
				Message: "provide exact legacy citation mappings",
			})
			causes = append(causes, presentation)
		}
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !existingPaths[path] {
			hasCitation := citationMappings[path].Path != ""
			hasComputation := computations[path].Path != ""
			hasGeneratedAt := generatedAt[path] != ""
			if hasCitation || hasComputation || hasGeneratedAt {
				presentation := migrationPathPresentationError(
					path,
					"migration_document_missing",
					fmt.Sprintf("migration document %q does not exist", path),
				)
				blocker, _, cause := migrationDocumentPreflightFinding(ctx, path, nil, presentation)
				blockers = append(blockers, blocker)
				causes = append(causes, cause)
			}
			continue
		}
		citation := citationMappings[path]
		source, ok, readErr := loaded.ReadFileContext(ctx, path)
		if readErr != nil {
			return nil, nil, readErr
		}
		if !ok {
			continue
		}
		if mode == MigrationInputPreflightSourceDiagnostics {
			continue
		}
		if computation := computations[path]; computation.Path != "" {
			if bindingErr := migrationComputationAssetPathErrorContext(ctx, loaded, computation); bindingErr != nil {
				if errors.Is(bindingErr, context.Canceled) || errors.Is(bindingErr, context.DeadlineExceeded) {
					return nil, nil, bindingErr
				}
				blocker, action, cause := migrationDocumentPreflightFinding(ctx, path, source, bindingErr)
				blockers = append(blockers, blocker)
				if action.Code != "" {
					actions = append(actions, action)
				}
				causes = append(causes, cause)
				continue
			}
		}
		if targetReplay {
			for _, preflightErr := range migrationTargetReplayInputPreflight(
				ctx,
				source,
				path,
				request,
				generatedAt[path],
				citationMappings[path],
				computations[path],
			) {
				blocker, action, cause := migrationDocumentPreflightFinding(ctx, path, source, preflightErr)
				blockers = append(blockers, blocker)
				if action.Code != "" {
					actions = append(actions, action)
				}
				causes = append(causes, cause)
			}
			continue
		}
		for _, preflightErr := range migrationDocumentIndependentPreflight(
			ctx,
			source,
			path,
			request,
			generatedAt[path],
			citation,
			computations[path],
		) {
			blocker, action, cause := migrationDocumentPreflightFinding(ctx, path, source, preflightErr)
			blockers = append(blockers, blocker)
			if action.Code != "" {
				actions = append(actions, action)
			}
			causes = append(causes, cause)
		}
	}
	if targetReplay {
		for _, computation := range request.Computations {
			presentation, replayErr := migrationTargetReplayComputationContext(ctx, loaded, computation)
			if replayErr != nil {
				return nil, nil, replayErr
			}
			if presentation != nil {
				blockers = append(blockers, MigrationBlocker{
					Code:     presentation.Code,
					Path:     presentation.Path,
					Location: presentation.Location,
					Message:  presentation.Error(),
				})
				causes = append(causes, presentation)
			}
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := sortMigrationInputFindingsContext(ctx, blockers, actions); err != nil {
		return nil, nil, err
	}
	blockers, err = dedupeMigrationBlockersContext(ctx, blockers)
	if err != nil {
		return nil, nil, err
	}
	actions, err = dedupeMigrationManualActionsContext(ctx, actions)
	if err != nil {
		return nil, nil, err
	}
	causes, err = dedupeMigrationCausesContext(ctx, causes)
	if err != nil {
		return nil, nil, err
	}
	joined, err := joinMigrationCausesContext(ctx, causes)
	if err != nil {
		return nil, nil, err
	}
	if joined == nil {
		return nil, nil, nil
	}
	return blockers, actions, joined
}

func migrationCitationMappingsContext(
	ctx context.Context,
	values []store.DocumentCitationMigration,
) (map[string]store.DocumentCitationMigration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]store.DocumentCitationMigration, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[value.Path] = value
	}
	return out, ctx.Err()
}

func migrationGeneratedAtContext(
	ctx context.Context,
	values []store.MigrationGeneratedAt,
) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[value.Path] = value.At
	}
	return out, ctx.Err()
}

func migrationComputationsContext(
	ctx context.Context,
	values []store.ComputationMigration,
) (map[string]store.ComputationMigration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]store.ComputationMigration, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[value.Path] = value
	}
	return out, ctx.Err()
}

func migrationTargetReplayInputPreflight(
	ctx context.Context,
	source []byte,
	path string,
	request store.V01ToV02Migration,
	explicitAt string,
	citation store.DocumentCitationMigration,
	computation store.ComputationMigration,
) []error {
	ownership, err := CollectMarkdownMigrationOwnership(ctx, source)
	if err != nil {
		return []error{err}
	}
	if legacyCitationsActive(ownership.CitationsSectionIndex) {
		return []error{markdownPresentationErrorAt(
			"migration_replay_mismatch",
			ErrUnsupportedPresentation,
			ownership.Sections[ownership.CitationsSectionIndex].Span,
		)}
	}
	requiresYAML := path == "index.md" || validator.DetermineFileRole(path) == validator.RoleConcept
	if !requiresYAML {
		if explicitAt != "" || computation.Path != "" {
			return []error{fmt.Errorf(
				"%w: reserved migration document cannot own generated or computation metadata",
				ErrUnsupportedPresentation,
			)}
		}
		if citation.Path != "" {
			if err := verifyMigratedCitations(ctx, source, nil, ownership, citation, false); err != nil {
				return []error{err}
			}
		}
		return nil
	}
	p, err := parsePresentationContext(ctx, source)
	if err != nil {
		return []error{err}
	}
	var causes []error
	if err := verifyTargetGeneratedReplay(p, request, explicitAt); err != nil {
		causes = append(causes, err)
	}
	replayed := source
	if computation.Path != "" {
		reconciled, err := reconcileAttestedComputationYAMLBytesContext(ctx, source, computation.Contract)
		if err != nil {
			causes = append(causes, err)
		} else {
			replayed = reconciled
		}
	}
	if len(causes) == 0 && !bytes.Equal(source, replayed) {
		causes = append(causes, markdownPresentationErrorAt(
			"migration_replay_mismatch",
			ErrUnsupportedPresentation,
			SourceSpan{Start: 0, End: len(source)},
		))
	}
	if citation.Path != "" {
		if err := verifyMigratedCitations(
			ctx,
			source,
			p,
			ownership,
			citation,
			validator.DetermineFileRole(path) == validator.RoleConcept,
		); err != nil {
			causes = append(causes, err)
		}
	}
	if computation.Path != "" {
		replayed, err := applyComputationBoundary(ctx, source, computation)
		if err != nil {
			causes = append(causes, err)
		} else if !bytes.Equal(source, replayed) {
			causes = append(causes, markdownPresentationErrorAt(
				"migration_replay_mismatch",
				ErrUnsupportedPresentation,
				SourceSpan{Start: 0, End: len(source)},
			))
		}
	}
	return causes
}

func migrationTargetReplayComputationContext(
	ctx context.Context,
	loaded *bundle.Bundle,
	computation store.ComputationMigration,
) (*PresentationError, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if computation.Asset == nil {
		return nil, ctx.Err()
	}
	content, ok, err := loaded.ReadFileContext(ctx, computation.Asset.Path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ok && bytes.Equal(content, computation.Asset.Content) {
		return nil, ctx.Err()
	}
	presentation := &PresentationError{
		Code:      "migration_replay_mismatch",
		Format:    "bundle",
		Path:      computation.Asset.Path,
		Operation: "migrate_v01_to_v02",
		Location:  SourceSpan{Start: 0, End: len(content)},
		Err:       ErrUnsupportedPresentation,
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return presentation, nil
}

func migrationPreflightPathsContext(ctx context.Context, loaded *bundle.Bundle, request store.V01ToV02Migration) ([]string, error) {
	paths := make(map[string]struct{})
	markdownPaths, err := migrationMarkdownPathsContext(ctx, loaded)
	if err != nil {
		return nil, err
	}
	for _, path := range markdownPaths {
		paths[path] = struct{}{}
	}
	for _, citation := range request.Citations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		paths[citation.Path] = struct{}{}
	}
	for _, generated := range request.GeneratedAt {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		paths[generated.Path] = struct{}{}
	}
	for _, computation := range request.Computations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		paths[computation.Path] = struct{}{}
	}
	out := make([]string, 0, len(paths))
	for path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	if err := sortSliceContext(ctx, out, func(left, right string) bool { return left < right }); err != nil {
		return nil, err
	}
	return out, ctx.Err()
}

func migrationPathPresentationError(path, code, message string) *PresentationError {
	return &PresentationError{
		Code:      code,
		Format:    "bundle",
		Path:      path,
		Operation: "migrate_v01_to_v02",
		Err:       fmt.Errorf("%w: %s", ErrUnsupportedPresentation, message),
	}
}

func migrationComputationAssetPathErrorContext(
	ctx context.Context,
	loaded *bundle.Bundle,
	computation store.ComputationMigration,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if computation.Asset == nil {
		return ctx.Err()
	}
	resolved, ok := loaded.ResolvePathValueFor(
		computation.Path,
		computation.Contract.ComputationPath,
		bundle.PathFieldComputation,
	)
	if err := ctx.Err(); err != nil {
		return err
	}
	local := resolved.Kind == bundle.PathValueRelative ||
		resolved.Kind == bundle.PathValueBundleRelative
	equalPath, err := compareStringsContext(ctx, resolved.Path, computation.Asset.Path)
	if err != nil {
		return err
	}
	if ok && local && resolved.Path != "" && equalPath == 0 {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return migrationPathPresentationError(
		computation.Path,
		"migration_computation_asset_path_mismatch",
		fmt.Sprintf(
			"computation path %q resolves as %q to %q, want migration asset %q",
			computation.Contract.ComputationPath,
			resolved.Kind,
			resolved.Path,
			computation.Asset.Path,
		),
	)
}

func validateMigrationComputationAssetPathsContext(
	ctx context.Context,
	loaded *bundle.Bundle,
	computations []store.ComputationMigration,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, computation := range computations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := migrationComputationAssetPathErrorContext(ctx, loaded, computation); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func migrationDocumentIndependentPreflight(
	ctx context.Context,
	source []byte,
	path string,
	request store.V01ToV02Migration,
	explicitAt string,
	citation store.DocumentCitationMigration,
	computation store.ComputationMigration,
) []error {
	if !utf8.Valid(source) {
		return nil
	}
	var causes []error
	var resolved []legacyCitationMapping
	role := validator.DetermineFileRole(path)
	if citation.Path != "" {
		var err error
		resolved, err = resolvedCitationEntries(ctx, source, citation.Entries)
		if err != nil {
			causes = append(causes, err)
		} else if err := validateResolvedCitationReplayEvidence(
			resolved,
			role == validator.RoleConcept,
		); err != nil {
			causes = append(causes, err)
		} else if _, err := rewriteLegacyCitationsContext(ctx, source, resolved); err != nil {
			causes = append(causes, err)
		}
	}

	requiresYAML := role == validator.RoleConcept ||
		(path == "index.md" && documentlayout.HasOpeningDelimiter(source))
	if !requiresYAML {
		if explicitAt != "" || computation.Path != "" {
			causes = append(causes, fmt.Errorf(
				"%w: reserved migration document cannot own generated or computation metadata",
				ErrUnsupportedPresentation,
			))
		}
		return causes
	}

	p, err := parsePresentationContext(ctx, source)
	if err != nil {
		return append(causes, err)
	}
	timestampMutation := p.newYAMLMutationContext(p.ctx)
	if err := stageTimestampMigration(timestampMutation, request, explicitAt); err != nil {
		causes = append(causes, err)
	} else if _, err := timestampMutation.apply(); err != nil {
		causes = append(causes, err)
	}

	structuredSources := false
	text, textErr := bytesStringContext(ctx, source)
	if textErr != nil {
		return append(causes, textErr)
	}
	if document, documentErr := bundle.ParseDocumentContext(ctx, text); documentErr == nil {
		structuredSources = document.LegacyFallbackObservation().SourcesPresent
	}
	if citation.Path != "" &&
		resolved != nil &&
		!structuredSources &&
		role == validator.RoleConcept {
		citationMutation := p.newYAMLMutationContext(p.ctx)
		if err := stageCitationSources(citationMutation, resolved); err != nil {
			causes = append(causes, err)
		} else if _, err := citationMutation.apply(); err != nil {
			causes = append(causes, err)
		}
	}

	if computation.Path != "" {
		updated, err := reconcileAttestedComputationYAMLBytesContext(ctx, source, computation.Contract)
		if err != nil {
			causes = append(causes, err)
		} else if _, err := applyComputationBoundary(ctx, updated, computation); err != nil {
			causes = append(causes, err)
		}
	}
	return causes
}

func dedupeMigrationBlockersContext(ctx context.Context, values []MigrationBlocker) ([]MigrationBlocker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := values[:0]
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(out) != 0 {
			equal, err := equalMigrationBlockerContext(ctx, out[len(out)-1], value)
			if err != nil {
				return nil, err
			}
			if equal {
				continue
			}
		}
		out = append(out, value)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return out, ctx.Err()
}

func equalMigrationBlockerContext(ctx context.Context, left, right MigrationBlocker) (bool, error) {
	order, err := compareMigrationBlockerContext(ctx, left, right)
	return order == 0, err
}

func dedupeMigrationManualActionsContext(ctx context.Context, values []MigrationManualAction) ([]MigrationManualAction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := values[:0]
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(out) != 0 {
			equal, err := equalMigrationManualActionContext(ctx, out[len(out)-1], value)
			if err != nil {
				return nil, err
			}
			if equal {
				continue
			}
		}
		out = append(out, value)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return out, ctx.Err()
}

func equalMigrationManualActionContext(ctx context.Context, left, right MigrationManualAction) (bool, error) {
	order, err := compareMigrationManualActionContext(ctx, left, right)
	return order == 0, err
}

func dedupeMigrationCausesContext(ctx context.Context, values []error) ([]error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type causeIdentity struct {
		Type    string
		Message string
	}
	seen := make([]causeIdentity, 0, len(values))
	out := values[:0]
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := causeIdentity{
			Type:    fmt.Sprintf("%T", value),
			Message: value.Error(),
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		exists := false
		for _, previous := range seen {
			equal, err := equalMigrationCauseIdentityContext(ctx, previous, key)
			if err != nil {
				return nil, err
			}
			if equal {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		seen = append(seen, key)
		out = append(out, value)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return out, ctx.Err()
}

func equalMigrationCauseIdentityContext(ctx context.Context, left, right struct {
	Type    string
	Message string
}) (bool, error) {
	if order, err := compareStringsContext(ctx, left.Type, right.Type); err != nil || order != 0 {
		return false, err
	}
	order, err := compareStringsContext(ctx, left.Message, right.Message)
	return order == 0, err
}

func joinMigrationCausesContext(ctx context.Context, causes []error) (error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var joined error
	for _, cause := range causes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if cause != nil {
			if joined == nil {
				joined = cause
			} else {
				joined = errors.Join(joined, cause)
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return joined, ctx.Err()
}

func migrationCitationOwnershipFinding(
	path string,
	err error,
) (MigrationBlocker, MigrationManualAction, error, bool) {
	var presentation *PresentationError
	if !errors.As(err, &presentation) {
		return MigrationBlocker{}, MigrationManualAction{}, nil, false
	}
	owned := *presentation
	owned.Path = path
	owned.Operation = "migrate_v01_to_v02"
	action := MigrationManualAction{Path: path}
	switch owned.Code {
	case "invalid_utf8", "invalid_encoding":
		action.Code = "repair_invalid_utf8"
		action.Message = "repair the document to valid UTF-8 before migration"
	case "ambiguous_citation_destination":
		action.Code = "disambiguate_citation_destination"
		action.Message = "reduce each legacy citation entry to one explicit destination before migration"
	case "duplicate_citation_number":
		action.Code = "disambiguate_citation_entry"
		action.Message = "make each legacy citation selector identify exactly one entry before migration"
	default:
		action.Code = "normalize_citations_section"
		action.Message = "normalize unsupported legacy citation syntax before migration"
	}
	return MigrationBlocker{
		Code: owned.Code, Path: path, Location: owned.Location, Message: owned.Error(),
	}, action, &owned, true
}

func migrationCitationMappingFinding(
	path string,
	err error,
) (MigrationBlocker, MigrationManualAction, error, bool) {
	var presentation *PresentationError
	if !errors.As(err, &presentation) {
		return MigrationBlocker{}, MigrationManualAction{}, nil, false
	}
	owned := *presentation
	owned.Path = path
	owned.Operation = "migrate_v01_to_v02"
	action := MigrationManualAction{
		Code:    "provide_citation_mapping",
		Path:    path,
		Message: "provide an exact legacy citation entry to stable source ID mapping",
	}
	if owned.Code == "ambiguous_citation_mapping" || owned.Code == "normalized_footnote_label_collision" {
		action.Code = "disambiguate_citation_entry"
		action.Message = "make each legacy citation selector identify exactly one entry before migration"
	}
	return MigrationBlocker{
		Code: owned.Code, Path: path, Location: owned.Location, Message: owned.Error(),
	}, action, &owned, true
}

func migrationDocumentPreflightFinding(
	ctx context.Context,
	path string,
	source []byte,
	err error,
) (MigrationBlocker, MigrationManualAction, error) {
	var presentation *PresentationError
	if !errors.As(err, &presentation) {
		if _, parseErr := parsePresentationContext(ctx, source); parseErr != nil {
			errors.As(parseErr, &presentation)
		}
	}
	if presentation == nil {
		presentation = &PresentationError{
			Code:      "migration_document_invalid",
			Format:    "markdown",
			Path:      path,
			Operation: "migrate_v01_to_v02",
			Location:  SourceSpan{Start: 0, End: len(source)},
			Err:       err,
		}
	} else {
		owned := *presentation
		owned.Path = path
		owned.Operation = "migrate_v01_to_v02"
		if owned.Format == "yaml" && owned.yamlRelative {
			offset := yamlFrontmatterStart(source)
			owned.Location.Start += offset
			owned.Location.End += offset
			owned.yamlRelative = false
		}
		presentation = &owned
	}
	var action MigrationManualAction
	if projected := migrationManualActions(presentation); len(projected) != 0 {
		action = projected[0]
	}
	return MigrationBlocker{
		Code: presentation.Code, Path: path, Location: presentation.Location, Message: presentation.Error(),
	}, action, presentation
}

func sortMigrationInputFindingsContext(
	ctx context.Context,
	blockers []MigrationBlocker,
	actions []MigrationManualAction,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sortSliceCompareContext(ctx, blockers, compareMigrationBlockerContext); err != nil {
		return err
	}
	return sortSliceCompareContext(ctx, actions, compareMigrationManualActionContext)
}

func compareMigrationBlockerContext(ctx context.Context, left, right MigrationBlocker) (int, error) {
	if order, err := compareStringsContext(ctx, left.Path, right.Path); err != nil || order != 0 {
		return order, err
	}
	if order, err := compareStringsContext(ctx, left.Code, right.Code); err != nil || order != 0 {
		return order, err
	}
	if left.Location.Start < right.Location.Start {
		return -1, ctx.Err()
	}
	if left.Location.Start > right.Location.Start {
		return 1, ctx.Err()
	}
	if left.Location.End < right.Location.End {
		return -1, ctx.Err()
	}
	if left.Location.End > right.Location.End {
		return 1, ctx.Err()
	}
	return compareStringsContext(ctx, left.Message, right.Message)
}

func compareMigrationManualActionContext(ctx context.Context, left, right MigrationManualAction) (int, error) {
	if order, err := compareStringsContext(ctx, left.Path, right.Path); err != nil || order != 0 {
		return order, err
	}
	if order, err := compareStringsContext(ctx, left.Code, right.Code); err != nil || order != 0 {
		return order, err
	}
	return compareStringsContext(ctx, left.Message, right.Message)
}

// Apply re-previews a fresh snapshot, verifies both exact base revision and
// plan digest, then delegates one closed ChangeSet to Store.Commit.
func (p MigrationPlanner) Apply(ctx context.Context, destination store.Store, request MigrationApplyRequest) (store.CommitReceipt, error) {
	if err := ctx.Err(); err != nil {
		return store.CommitReceipt{}, err
	}
	if destination == nil {
		return store.CommitReceipt{}, errors.New("mutation: nil migration store")
	}
	if err := validateMigrationSourceResolutionContext(ctx, request.Resolution); err != nil {
		return store.CommitReceipt{}, err
	}
	if err := validateMigrationPlanDigest(request.PlanDigest); err != nil {
		return store.CommitReceipt{}, err
	}
	change, err := migrationChangeSet(request.Request, request.Proof.BaseRevision)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if err := validateMigrationPlanProofContext(ctx, change, request.Proof, request.Resolution); err != nil {
		return store.CommitReceipt{}, err
	}
	proofDigest, err := migrationPlanProofDigestContext(ctx, change, request.Proof, request.Resolution)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if proofDigest != request.PlanDigest {
		return store.CommitReceipt{}, fmt.Errorf("%w: expected %s, actual %s", ErrMigrationPlanMismatch, request.PlanDigest, proofDigest)
	}
	resolutionCopy, err := request.Resolution.cloneContext(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	ctx = context.WithValue(ctx, migrationResolutionContextKey{}, resolutionCopy)
	options, err := migrationApplyCommitOptionsContext(ctx, request)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	snapshot, err := destination.Snapshot(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return store.CommitReceipt{}, err
	}
	if actual := snapshot.Revision(); actual != request.Proof.BaseRevision {
		// Store.Commit owns durable idempotency replay. Delegating the exact
		// original request here lets a completed retry return its receipt before
		// the backend's revision CAS; a new/mismatched request still conflicts.
		if err := migrationApplyPreCommitContext(ctx); err != nil {
			return store.CommitReceipt{}, err
		}
		receipt, err := destination.Commit(ctx, change, options)
		return migrationCommitOutcome(ctx, change, options, request.Proof, receipt, err)
	}
	fresh, err := p.Preview(ctx, snapshot, request.Resolution, request.Request)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if fresh.PlanDigest != request.PlanDigest {
		return store.CommitReceipt{}, fmt.Errorf("%w: fresh preview does not match authorized proof", ErrMigrationPlanMismatch)
	}
	proofMatches, err := equalMigrationPlanProofContext(ctx, fresh.Proof, request.Proof)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if !proofMatches {
		return store.CommitReceipt{}, fmt.Errorf("%w: fresh preview does not match authorized proof", ErrMigrationPlanMismatch)
	}
	if err := migrationApplyPreCommitContext(ctx); err != nil {
		return store.CommitReceipt{}, err
	}
	receipt, err := destination.Commit(ctx, change, options)
	return migrationCommitOutcome(ctx, change, options, request.Proof, receipt, err)
}

func migrationApplyCommitOptionsContext(
	ctx context.Context,
	request MigrationApplyRequest,
) (store.CommitOptions, error) {
	if err := ctx.Err(); err != nil {
		return store.CommitOptions{}, err
	}
	options := request.Options
	derivedKey, err := PlanDigestIdempotencyKey(
		"migration:v0.1-to-v0.2",
		request.PlanDigest,
		request.Options.IdempotencyKey,
	)
	if err != nil {
		return store.CommitOptions{}, err
	}
	options.IdempotencyKey = derivedKey
	if err := ctx.Err(); err != nil {
		return store.CommitOptions{}, err
	}
	return options, nil
}

func migrationApplyPreCommitContext(ctx context.Context) error {
	return ctx.Err()
}

func migrationCommitOutcome(
	ctx context.Context,
	change store.ChangeSet,
	options store.CommitOptions,
	proof MigrationPlanProof,
	receipt store.CommitReceipt,
	commitErr error,
) (store.CommitReceipt, error) {
	ctx = migrationPostCommitContext(ctx)
	if commitErr == nil {
		if err := validateMigrationReceiptContext(ctx, change, options, proof, receipt); err != nil {
			return store.CommitReceipt{}, err
		}
		return receipt.Clone(), nil
	}
	var committed *store.CommittedError
	if !errors.As(commitErr, &committed) {
		return store.CommitReceipt{}, commitErr
	}
	committedReceipt := committed.Receipt()
	returnedErr := validateMigrationReceiptContext(ctx, change, options, proof, receipt)
	committedErr := validateMigrationReceiptContext(ctx, change, options, proof, committedReceipt)
	receiptsEqual := false
	var equalityErr error
	if returnedErr == nil && committedErr == nil {
		receiptsEqual, equalityErr = equalMigrationCommitReceiptsContext(ctx, receipt, committedReceipt)
	}
	if returnedErr != nil || committedErr != nil || equalityErr != nil || !receiptsEqual {
		integrityErr := errors.Join(returnedErr, committedErr, equalityErr)
		if returnedErr == nil && committedErr == nil && equalityErr == nil && !receiptsEqual {
			integrityErr = errors.Join(integrityErr, fmt.Errorf("%w: returned receipt does not match committed error receipt", ErrMigrationPlanMismatch))
		}
		return store.CommitReceipt{}, errors.Join(commitErr, integrityErr)
	}
	return receipt.Clone(), commitErr
}

func migrationPostCommitContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

func equalMigrationCommitReceipts(left, right store.CommitReceipt) bool {
	equal, _ := equalMigrationCommitReceiptsContext(context.Background(), left, right)
	return equal
}

func equalMigrationCommitReceiptsContext(ctx context.Context, left, right store.CommitReceipt) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if left.FormatVersion != right.FormatVersion || !left.CommitTime.Equal(right.CommitTime) {
		return false, nil
	}
	for _, values := range [][2]string{
		{string(left.ChangeSetID), string(right.ChangeSetID)},
		{string(left.IdempotencyKey), string(right.IdempotencyKey)},
		{left.RequestDigest, right.RequestDigest},
		{left.BaseRevision.String(), right.BaseRevision.String()},
		{left.ResultRevision.String(), right.ResultRevision.String()},
	} {
		equal, err := equalMigrationReceiptStringContext(ctx, values[0], values[1])
		if err != nil || !equal {
			return false, err
		}
	}
	filesEqual, err := equalMigrationFileChangesContext(ctx, left.ChangedFiles, right.ChangedFiles)
	if err != nil || !filesEqual {
		return false, err
	}
	return equalMigrationRefsContext(ctx, left.ChangedRefs, right.ChangedRefs)
}

type migrationResolutionContextKey struct{}

func frozenMigrationResolution(ctx context.Context) (MigrationSourceResolution, error) {
	resolution, ok := ctx.Value(migrationResolutionContextKey{}).(MigrationSourceResolution)
	if !ok {
		return MigrationSourceResolution{}, fmt.Errorf(
			"%w: migration execution requires frozen source resolution",
			ErrUnsupportedMigrationVersion,
		)
	}
	if err := validateMigrationSourceResolutionContext(ctx, resolution); err != nil {
		return MigrationSourceResolution{}, err
	}
	return resolution, nil
}

func validateMigrationReceipt(
	change store.ChangeSet,
	options store.CommitOptions,
	proof MigrationPlanProof,
	receipt store.CommitReceipt,
) error {
	requestDigest, err := change.RequestDigest()
	if err != nil {
		return err
	}
	proof.RequestDigest = requestDigest
	return validateMigrationReceiptContext(context.Background(), change, options, proof, receipt)
}

func validateMigrationReceiptContext(
	ctx context.Context,
	change store.ChangeSet,
	options store.CommitOptions,
	proof MigrationPlanProof,
	receipt store.CommitReceipt,
) error {
	bound, err := migrationReceiptMatchesProofContext(ctx, change, options, proof, receipt)
	if err != nil {
		return err
	}
	if !bound {
		return fmt.Errorf("%w: receipt does not match authorized preview result", ErrMigrationPlanMismatch)
	}
	if err := store.ValidateCommitReceipt(receipt); err != nil {
		return fmt.Errorf("%w: invalid commit receipt: %w", ErrMigrationPlanMismatch, err)
	}
	return nil
}

func migrationReceiptMatchesProofContext(
	ctx context.Context,
	change store.ChangeSet,
	options store.CommitOptions,
	proof MigrationPlanProof,
	receipt store.CommitReceipt,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(receipt.ChangedFiles) != len(proof.ChangedFiles) || len(receipt.ChangedRefs) != len(proof.ChangedRefs) {
		return false, nil
	}
	for _, values := range [][2]string{
		{string(receipt.ChangeSetID), string(change.ID)},
		{string(receipt.IdempotencyKey), string(options.IdempotencyKey)},
		{receipt.RequestDigest, proof.RequestDigest},
		{receipt.BaseRevision.String(), change.BaseRevision.String()},
		{receipt.ResultRevision.String(), proof.ResultRevision.String()},
	} {
		equal, err := equalMigrationReceiptStringContext(ctx, values[0], values[1])
		if err != nil || !equal {
			return false, err
		}
	}
	filesEqual, err := equalMigrationFileChangesContext(ctx, receipt.ChangedFiles, proof.ChangedFiles)
	if err != nil || !filesEqual {
		return false, err
	}
	return equalMigrationRefsContext(ctx, receipt.ChangedRefs, proof.ChangedRefs)
}

func equalMigrationReceiptStringContext(ctx context.Context, left, right string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(left) != len(right) {
		return false, nil
	}
	order, err := compareStringsContext(ctx, left, right)
	return order == 0, err
}

func equalMigrationFileChanges(left, right []store.FileChange) bool {
	equal, _ := equalMigrationFileChangesContext(context.Background(), left, right)
	return equal
}

func equalMigrationFileChangesContext(ctx context.Context, left, right []store.FileChange) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(left) != len(right) {
		return false, nil
	}
	for index := range left {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if left[index].Kind != right[index].Kind {
			return false, nil
		}
		for _, values := range [][2]string{{left[index].Path, right[index].Path}, {left[index].From, right[index].From}} {
			equal, err := equalMigrationReceiptStringContext(ctx, values[0], values[1])
			if err != nil || !equal {
				return false, err
			}
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	return true, ctx.Err()
}

func equalMigrationRefsContext(ctx context.Context, left, right []bundle.RelationRef) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(left) != len(right) {
		return false, nil
	}
	leftBytes, err := migrationRefsByteSizeContext(ctx, left)
	if err != nil {
		return false, err
	}
	rightBytes, err := migrationRefsByteSizeContext(ctx, right)
	if err != nil {
		return false, err
	}
	if leftBytes != rightBytes {
		return false, nil
	}
	leftOwned, err := cloneMigrationProofSliceContext(ctx, left)
	if err != nil {
		return false, err
	}
	rightOwned, err := cloneMigrationProofSliceContext(ctx, right)
	if err != nil {
		return false, err
	}
	if err := sortSliceCompareContext(ctx, leftOwned, compareMigrationRelationRefContext); err != nil {
		return false, err
	}
	if err := sortSliceCompareContext(ctx, rightOwned, compareMigrationRelationRefContext); err != nil {
		return false, err
	}
	for index := range leftOwned {
		equal, err := equalMigrationRelationRefContext(ctx, leftOwned[index], rightOwned[index])
		if err != nil || !equal {
			return false, err
		}
	}
	return true, ctx.Err()
}

func migrationRefsByteSizeContext(ctx context.Context, values []bundle.RelationRef) (int, error) {
	const maxInt = int(^uint(0) >> 1)
	total := 0
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		id := value.ID.String()
		if len(id) > maxInt-total {
			return 0, fmt.Errorf("%w: migration receipt relation identity exceeds bounds", ErrMigrationPlanMismatch)
		}
		total += len(id)
		if len(value.Fragment) > maxInt-total {
			return 0, fmt.Errorf("%w: migration receipt relation identity exceeds bounds", ErrMigrationPlanMismatch)
		}
		total += len(value.Fragment)
	}
	return total, ctx.Err()
}

func equalMigrationRefs(left, right []bundle.RelationRef) bool {
	if len(left) != len(right) {
		return false
	}
	leftRefs := make(map[relationRefKey]struct{}, len(left))
	for _, ref := range left {
		key := relationRefIdentity(ref)
		if _, duplicate := leftRefs[key]; duplicate {
			return false
		}
		leftRefs[key] = struct{}{}
	}
	rightRefs := make(map[relationRefKey]struct{}, len(right))
	for _, ref := range right {
		key := relationRefIdentity(ref)
		if _, duplicate := rightRefs[key]; duplicate {
			return false
		}
		if _, exists := leftRefs[key]; !exists {
			return false
		}
		rightRefs[key] = struct{}{}
	}
	return len(leftRefs) == len(rightRefs)
}

func migrationChangeSet(request MigrationRequest, base store.Revision) (store.ChangeSet, error) {
	if request.FromVersion != MigrationVersionV01 || request.ToVersion != MigrationVersionV02 {
		return store.ChangeSet{}, fmt.Errorf("%w: %q to %q", ErrUnsupportedMigrationVersion, request.FromVersion, request.ToVersion)
	}
	operation, err := canonicalV01ToV02MigrationInput(request.Migration)
	if err != nil {
		return store.ChangeSet{}, err
	}
	change := store.ChangeSet{
		Version:       store.ChangeSetFormatVersion,
		ID:            request.ID,
		Actor:         request.Actor,
		BaseRevision:  base,
		Operations:    []store.Operation{operation},
		Preconditions: []store.Precondition{store.RevisionEquals{Revision: base}},
	}
	if err := change.Validate(); err != nil {
		return store.ChangeSet{}, err
	}
	return change, nil
}

func migrationBlockers(err error) []MigrationBlocker {
	if err == nil {
		return nil
	}
	blocker := MigrationBlocker{Code: "migration_blocked", Message: err.Error()}
	var presentation *PresentationError
	if errors.As(err, &presentation) {
		blocker.Code = presentation.Code
		blocker.Path = presentation.Path
		blocker.Location = presentation.Location
	}
	return []MigrationBlocker{blocker}
}

func migrationManualActions(err error) []MigrationManualAction {
	var presentation *PresentationError
	if !errors.As(err, &presentation) {
		return nil
	}
	switch presentation.Code {
	case "missing_explicit_citation_mapping",
		"incomplete_citation_mapping",
		"unresolved_citation_entry",
		"unresolved_claim_marker",
		"migration_citation_replay_evidence_missing":
		return []MigrationManualAction{{
			Code:    "provide_citation_mapping",
			Path:    presentation.Path,
			Message: "provide an exact legacy citation entry to stable source ID mapping",
		}}
	case "unowned_citations_content", "opaque_citations_content":
		return []MigrationManualAction{{
			Code:    "normalize_citations_section",
			Path:    presentation.Path,
			Message: "move unsupported or opaque content out of the Citations section before migration",
		}}
	case "sources_citations_conflict":
		return []MigrationManualAction{{
			Code:    "reconcile_sources_and_citations",
			Path:    presentation.Path,
			Message: "document contains both structured sources and legacy # Citations; normalize to one explicit representation before migration",
		}}
	case "invalid_utf8", "invalid_encoding":
		return []MigrationManualAction{{
			Code:    "repair_invalid_utf8",
			Path:    presentation.Path,
			Message: "repair the document to valid UTF-8 before migration",
		}}
	case "ambiguous_citation_destination":
		return []MigrationManualAction{{
			Code:    "disambiguate_citation_destination",
			Path:    presentation.Path,
			Message: "reduce each legacy citation entry to one explicit destination before migration",
		}}
	case "ambiguous_citation_mapping", "duplicate_citation_number", "normalized_footnote_label_collision":
		return []MigrationManualAction{{
			Code:    "disambiguate_citation_entry",
			Path:    presentation.Path,
			Message: "make each legacy citation selector identify exactly one entry before migration",
		}}
	case "missing_explicit_generated_at":
		return []MigrationManualAction{{
			Code:    "provide_generated_at",
			Path:    presentation.Path,
			Message: "provide an explicit RFC3339 generated.at value",
		}}
	case "missing_explicit_generated_by":
		return []MigrationManualAction{{
			Code:    "provide_generated_by",
			Path:    presentation.Path,
			Message: "provide an explicit generated.by document actor",
		}}
	default:
		return nil
	}
}

func validateMigrationPlanDigest(value string) error {
	return ValidatePlanDigest(value)
}

func (s *planState) migrateV01ToV02(operation store.MigrateV01ToV02) error {
	request := operation.Migration()
	resolution, err := frozenMigrationResolution(s.ctx)
	if err != nil {
		return err
	}
	if err := validateMigrationComputationAssetPathsContext(s.ctx, s.bundle, request.Computations); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return invalid("migration_computation_asset_path_mismatch", nil, err)
	}
	hasIndex, err := s.overlay.visiblePath(s.ctx, "index.md")
	if err != nil {
		return err
	}
	var indexData []byte
	if hasIndex {
		indexData, err = s.overlay.ReadFile(s.ctx, "index.md")
		if err != nil {
			return invalid("migration_missing_index", nil, err)
		}
	}
	if resolution.Transition == MigrationTransitionTargetNoop {
		if err := s.verifyMigrationTargetReplay(request, resolution.ResolvedSource); err != nil {
			return err
		}
		s.plans = append(s.plans, store.OperationPlan{Operation: operation, Details: []string{"v0.2 migration already applied"}})
		return nil
	}
	if resolution.Transition != MigrationTransitionV01ToV02 {
		return invalid("unsupported_migration_version", nil, fmt.Errorf(
			"%w: frozen transition is %q",
			ErrUnsupportedMigrationVersion,
			resolution.Transition,
		))
	}
	if err := s.verifyMigrationTargetReplay(request, MigrationVersionV02); err == nil {
		detail := "v0.2 migration already applied"
		if !hasIndex {
			detail += " without root declaration"
		}
		s.plans = append(s.plans, store.OperationPlan{Operation: operation, Details: []string{detail}})
		return nil
	}

	generatedAt := make(map[string]string, len(request.GeneratedAt))
	for _, value := range request.GeneratedAt {
		generatedAt[value.Path] = value.At
	}
	citations := make(map[string]store.DocumentCitationMigration, len(request.Citations))
	for _, value := range request.Citations {
		citations[value.Path] = value
	}
	computations := make(map[string]store.ComputationMigration, len(request.Computations))
	for _, value := range request.Computations {
		computations[value.Path] = value
	}

	paths, err := migrationMarkdownPathsContext(s.ctx, s.bundle)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(paths))
	for _, name := range paths {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		seen[name] = struct{}{}
		if name == "index.md" {
			continue
		}
		current, err := s.overlay.ReadFile(s.ctx, name)
		if err != nil {
			return err
		}
		updated, err := migrateV01Document(s.ctx, current, name, request, generatedAt[name], citations[name], computations[name])
		if err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, name, "migrate_v01_to_v02", current)
		}
		if !bytes.Equal(current, updated) {
			if err := s.overlay.PutContext(s.ctx, name, updated); err != nil {
				return err
			}
		}
	}
	for _, citation := range request.Citations {
		name := citation.Path
		if _, ok := seen[name]; !ok {
			return invalid("migration_document_missing", nil, fmt.Errorf("migration document %q does not exist", name))
		}
	}
	for _, computation := range request.Computations {
		name := computation.Path
		if _, ok := seen[name]; ok {
			continue
		}
		return invalid("migration_document_missing", nil, fmt.Errorf("migration document %q does not exist", name))
	}
	for _, generated := range request.GeneratedAt {
		name := generated.Path
		if _, ok := seen[name]; !ok {
			return invalid("migration_document_missing", nil, fmt.Errorf("migration document %q does not exist", name))
		}
	}
	for _, computation := range request.Computations {
		if computation.Asset == nil {
			continue
		}
		existing, readErr := s.overlay.ReadFile(s.ctx, computation.Asset.Path)
		switch {
		case readErr == nil && !bytes.Equal(existing, computation.Asset.Content):
			return invalid("migration_asset_conflict", nil, fmt.Errorf("migration asset %q already exists with different bytes", computation.Asset.Path))
		case readErr == nil:
			continue
		case !errors.Is(readErr, fs.ErrNotExist):
			// Overlay providers do not consistently wrap fs.ErrNotExist. Prove
			// absence through the visible path set before creating.
			exists, err := s.overlay.visiblePath(s.ctx, computation.Asset.Path)
			if err != nil {
				return err
			}
			if exists {
				return readErr
			}
		}
		if err := s.overlay.Create(s.ctx, computation.Asset.Path, computation.Asset.Content); err != nil {
			return err
		}
	}

	if hasIndex {
		indexUpdated, err := migrateV01Document(
			s.ctx,
			indexData,
			"index.md",
			request,
			generatedAt["index.md"],
			citations["index.md"],
			computations["index.md"],
		)
		if err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, "index.md", "migrate_v01_to_v02", indexData)
		}
		indexUpdated, err = setMigrationBundleVersion(s.ctx, indexUpdated, MigrationVersionV02)
		if err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, "index.md", "migrate_v01_to_v02", indexUpdated)
		}
		if !bytes.Equal(indexData, indexUpdated) {
			candidate, err := s.cloneContext(s.ctx)
			if err != nil {
				return err
			}
			if err := candidate.overlay.PutContext(s.ctx, "index.md", indexUpdated); err != nil {
				return err
			}
			if err := validateMigrationTargetProjection(s.ctx, candidate.overlay); err != nil {
				return err
			}
			// The real root version write is deliberately the last semantic
			// write, after the explicit target-v0.2 projection has passed.
			if err := s.overlay.PutContext(s.ctx, "index.md", indexUpdated); err != nil {
				return err
			}
		}
	}
	target, err := bundle.Load(s.ctx, s.overlay)
	if err != nil {
		return err
	}
	projection, err := receiptprojection.Derive(s.ctx, s.bundle, target)
	if err != nil {
		return err
	}
	s.add(projection.ChangedRefs...)
	s.plans = append(s.plans, store.OperationPlan{Operation: operation, Details: []string{"migrated complete bundle from v0.1 to v0.2"}})
	return nil
}

func validateMigrationTargetProjection(ctx context.Context, source bundle.Source) error {
	target, err := bundle.Load(ctx, source)
	if err != nil {
		return err
	}
	validation, err := validator.ValidateBundleContext(ctx, target, nil)
	if err != nil {
		return err
	}
	if validation.ExitCode() != 0 {
		return invalid("migration_target_preflight_failed", nil, errors.New("target v0.2 projection is not conformant"))
	}
	diagnostics, err := target.RelationDiagnosticsContext(ctx)
	if err != nil {
		return err
	}
	blocking, err := blockingRelationDiagnosticsContext(ctx, diagnostics)
	if err != nil {
		return err
	}
	if len(blocking) != 0 {
		return invalid("migration_target_preflight_failed", nil, errors.New("target v0.2 projection has blocking relations"))
	}
	return nil
}

func (s *planState) verifyMigrationTargetReplay(
	request store.V01ToV02Migration,
	expectedVersion string,
) error {
	generatedAt := make(map[string]string, len(request.GeneratedAt))
	for _, value := range request.GeneratedAt {
		generatedAt[value.Path] = value.At
	}
	citations := make(map[string]store.DocumentCitationMigration, len(request.Citations))
	for _, value := range request.Citations {
		citations[value.Path] = value
	}
	computations := make(map[string]store.ComputationMigration, len(request.Computations))
	for _, value := range request.Computations {
		computations[value.Path] = value
	}
	paths, err := migrationMarkdownPathsContext(s.ctx, s.bundle)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(paths))
	for _, name := range paths {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		seen[name] = struct{}{}
		source, err := s.overlay.ReadFile(s.ctx, name)
		if err != nil {
			return err
		}
		ownership, err := CollectMarkdownMigrationOwnership(s.ctx, source)
		if err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, name, "migrate_v01_to_v02", source)
		}
		if legacyCitationsActive(ownership.CitationsSectionIndex) {
			err := markdownPresentationErrorAt("migration_replay_mismatch", ErrUnsupportedPresentation, SourceSpan{Start: 0, End: len(source)})
			return s.presentationInvalid(presentationChangeCode(err), nil, err, name, "migrate_v01_to_v02", source)
		}
		requiresYAML := name == "index.md" || validator.DetermineFileRole(name) == validator.RoleConcept
		if !requiresYAML {
			if document, ok := citations[name]; ok {
				if err := verifyMigratedCitations(s.ctx, source, nil, ownership, document, false); err != nil {
					return s.migrationReplayPresentationInvalidExact(err, name, source)
				}
			}
			continue
		}
		p, err := parsePresentationContext(s.ctx, source)
		if err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, name, "migrate_v01_to_v02", source)
		}
		if name == "index.md" {
			version, declared, err := migrationVersion(p)
			if err != nil {
				return s.presentationInvalid(presentationChangeCode(err), nil, err, name, "migrate_v01_to_v02", source)
			}
			if !declared || version != expectedVersion {
				return invalid("migration_replay_mismatch", nil, fmt.Errorf("root version is %q, want %q", version, expectedVersion))
			}
		}
		if err := verifyTargetGeneratedReplay(p, request, generatedAt[name]); err != nil {
			return s.presentationInvalid(presentationChangeCode(err), nil, err, name, "migrate_v01_to_v02", source)
		}
		replayed := source
		if computation, ok := computations[name]; ok {
			reconciled, err := reconcileAttestedComputationYAMLBytesContext(s.ctx, source, computation.Contract)
			if err != nil {
				return s.presentationInvalid(presentationChangeCode(err), nil, err, name, "migrate_v01_to_v02", source)
			}
			replayed = reconciled
		}
		if !bytes.Equal(source, replayed) {
			return invalid("migration_replay_mismatch", nil, fmt.Errorf("v0.2 document %q does not match requested migration", name))
		}
		if document, ok := citations[name]; ok {
			if err := verifyMigratedCitations(
				s.ctx,
				source,
				p,
				ownership,
				document,
				validator.DetermineFileRole(name) == validator.RoleConcept,
			); err != nil {
				return s.migrationReplayPresentationInvalidExact(err, name, source)
			}
		}
		if computation, ok := computations[name]; ok {
			replayed, err := applyComputationBoundary(s.ctx, source, computation)
			if err != nil || !bytes.Equal(source, replayed) {
				if err == nil {
					err = markdownPresentationErrorAt("migration_replay_mismatch", ErrUnsupportedPresentation, SourceSpan{Start: 0, End: len(source)})
				}
				return s.presentationInvalid(presentationChangeCode(err), nil, err, name, "migrate_v01_to_v02", source)
			}
		}
	}
	for _, citation := range request.Citations {
		name := citation.Path
		if _, ok := seen[name]; !ok {
			return invalid("migration_replay_mismatch", nil, fmt.Errorf("v0.2 citation document %q is missing", name))
		}
	}
	for _, generated := range request.GeneratedAt {
		name := generated.Path
		if _, ok := seen[name]; !ok {
			return invalid("migration_replay_mismatch", nil, fmt.Errorf("v0.2 generated.at document %q is missing", name))
		}
	}
	for _, computation := range request.Computations {
		name := computation.Path
		if _, ok := seen[name]; !ok {
			return invalid("migration_replay_mismatch", nil, fmt.Errorf("v0.2 computation document %q is missing", name))
		}
		if computation.Asset != nil {
			content, err := s.overlay.ReadFile(s.ctx, computation.Asset.Path)
			if err != nil || !bytes.Equal(content, computation.Asset.Content) {
				return invalid("migration_replay_mismatch", nil, fmt.Errorf("v0.2 computation asset %q differs", computation.Asset.Path))
			}
		}
	}
	return nil
}

func (s *planState) migrationReplayPresentationInvalidExact(cause error, path string, source []byte) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	var presentation *PresentationError
	if !errors.As(cause, &presentation) {
		return s.presentationInvalid(
			presentationChangeCode(cause),
			nil,
			cause,
			path,
			"migrate_v01_to_v02",
			source,
		)
	}
	if presentation.Format != "markdown" {
		return s.presentationInvalid(
			presentationChangeCode(cause),
			nil,
			cause,
			path,
			"migrate_v01_to_v02",
			source,
		)
	}
	owned := *presentation
	owned.Path = path
	owned.Operation = "migrate_v01_to_v02"
	return invalid(presentationChangeCode(cause), nil, &owned)
}

type migratedCitationReplayExpectation struct {
	mapping    legacyCitationMapping
	sourceItem *yaml.Node
	definition MarkdownFootnoteDefinition
}

func verifyMigratedCitations(
	ctx context.Context,
	source []byte,
	p *presentation,
	ownership MarkdownMigrationOwnership,
	document store.DocumentCitationMigration,
	requireSources bool,
) error {
	if err := validateFootnoteIdentitiesContext(ctx, ownership, nil, nil); err != nil {
		return err
	}
	definitions := make(map[string]MarkdownFootnoteDefinition, len(ownership.FootnoteDefinitions))
	for _, definition := range ownership.FootnoteDefinitions {
		definitions[definition.NormalizedLabel] = definition
	}
	expectations := make([]migratedCitationReplayExpectation, 0, len(document.Entries))
	expectedIDs := make(map[string]string, len(document.Entries))
	for _, entry := range document.Entries {
		expected := legacyCitationMapping{
			LegacyNumber: entry.LegacyNumber,
			LegacyEntry:  entry.LegacyEntry,
			SourceID:     entry.SourceID,
			Title:        entry.Title,
			Resource:     entry.Resource,

			callerLegacyEntry: entry.LegacyEntry != "",
			callerTitle:       entry.Title != "",
			callerResource:    entry.Resource != "",
		}
		if !requireSources {
			if err := validateResolvedCitationReplayEvidence(
				[]legacyCitationMapping{expected},
				false,
			); err != nil {
				return err
			}
		}
		wantDefinition := ""
		var sourceItem *yaml.Node
		if entry.LegacyEntry != "" {
			var err error
			expected, wantDefinition, err = migratedCitationExpectation(ctx, entry)
			if err != nil {
				return err
			}
		}
		if requireSources {
			var err error
			expected, sourceItem, err = migratedCitationSourceExpectation(p, expected)
			if err != nil {
				return err
			}
		}
		if wantDefinition == "" {
			var err error
			wantDefinition, err = migratedRenderedCitationContentContext(ctx, expected)
			if err != nil {
				return err
			}
		}
		normalizedID := markdownowner.NormalizeFootnoteLabel(expected.SourceID)
		if previous, exists := expectedIDs[normalizedID]; exists && previous != expected.SourceID {
			span := SourceSpan{}
			if definition, ok := definitions[normalizedID]; ok {
				span = definition.Span
			}
			return markdownPresentationErrorAt(
				"normalized_footnote_label_collision",
				ErrAmbiguousPresentation,
				span,
			)
		}
		expectedIDs[normalizedID] = expected.SourceID
		definition, ok := definitions[normalizedID]
		if ok && definition.Label != expected.SourceID {
			return markdownPresentationErrorAt(
				"normalized_footnote_label_collision",
				ErrAmbiguousPresentation,
				definition.Span,
			)
		}
		if !ok {
			return markdownPresentationErrorAt(
				"migration_replay_mismatch",
				ErrUnsupportedPresentation,
				SourceSpan{Start: len(source), End: len(source)},
			)
		}
		if definition.Content == "" ||
			(wantDefinition != "" && definition.Content != wantDefinition) {
			return markdownPresentationErrorAt(
				"migration_replay_mismatch",
				ErrUnsupportedPresentation,
				definition.ContentSpan,
			)
		}
		expectations = append(expectations, migratedCitationReplayExpectation{
			mapping: expected, sourceItem: sourceItem, definition: definition,
		})
	}
	if err := verifyMigratedCitationClaimReplayContext(ctx, ownership, expectations); err != nil {
		return err
	}
	for _, expectation := range expectations {
		if !requireSources {
			continue
		}
		expected := expectation.mapping
		sourceItem := expectation.sourceItem
		fields := []struct{ key, want string }{{"title", expected.Title}, {"resource", expected.Resource}}
		for _, field := range fields {
			if field.want == "" {
				continue
			}
			match, ok, err := p.structuralMappingEntry(sourceItem, field.key)
			if err != nil {
				return err
			}
			if !ok || match.value.Kind != yaml.ScalarNode || match.value.Value != field.want {
				return p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, sourceItem)
			}
		}
	}
	return nil
}

func verifyMigratedCitationClaimReplayContext(
	ctx context.Context,
	ownership MarkdownMigrationOwnership,
	expectations []migratedCitationReplayExpectation,
) error {
	references := make(map[string]struct{}, len(ownership.FootnoteReferences))
	for _, reference := range ownership.FootnoteReferences {
		if err := ctx.Err(); err != nil {
			return err
		}
		references[reference.NormalizedLabel] = struct{}{}
	}
	for _, expectation := range expectations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if expectation.mapping.LegacyNumber == 0 {
			continue
		}
		for _, marker := range ownership.NumericMarkers {
			if err := ctx.Err(); err != nil {
				return err
			}
			if marker.Number == expectation.mapping.LegacyNumber {
				return markdownPresentationErrorAt(
					"migration_replay_mismatch",
					ErrUnsupportedPresentation,
					marker.Span,
				)
			}
		}
		normalizedID := markdownowner.NormalizeFootnoteLabel(expectation.mapping.SourceID)
		if _, ok := references[normalizedID]; !ok {
			return markdownPresentationErrorAt(
				"migration_replay_mismatch",
				ErrUnsupportedPresentation,
				expectation.definition.ContentSpan,
			)
		}
	}
	return ctx.Err()
}

func migratedCitationSourceExpectation(
	p *presentation,
	expected legacyCitationMapping,
) (legacyCitationMapping, *yaml.Node, error) {
	sources, ok, err := p.structuralMappingEntry(p.root, "sources")
	if err != nil {
		return legacyCitationMapping{}, nil, err
	}
	if !ok {
		return legacyCitationMapping{}, nil, p.yamlNodeError(
			"migration_replay_mismatch",
			ErrUnsupportedPresentation,
			p.root,
		)
	}
	item, found, err := p.selectSequenceItem(
		sources.value,
		yamlMappingSelector(yamlSelector("id", yamlString(expected.SourceID))),
	)
	if err != nil {
		return legacyCitationMapping{}, nil, err
	}
	if !found {
		return legacyCitationMapping{}, nil, p.yamlNodeError(
			"migration_replay_mismatch",
			ErrUnsupportedPresentation,
			sources.value,
		)
	}
	title, err := migratedCitationSourceField(p, item, "title", false)
	if err != nil {
		return legacyCitationMapping{}, nil, err
	}
	resource, err := migratedCitationSourceField(p, item, "resource", true)
	if err != nil {
		return legacyCitationMapping{}, nil, err
	}
	projected, err := buildExpectedCitationProjectionContext(
		p.ctx,
		expected,
		MarkdownCitationEntry{Title: title, Resource: resource},
		"\n",
	)
	if err != nil {
		return legacyCitationMapping{}, nil, p.yamlNodeError(
			"migration_replay_mismatch",
			ErrUnsupportedPresentation,
			item,
		)
	}
	return projected.Mapping, item, nil
}

func migratedCitationSourceField(
	p *presentation,
	item *yaml.Node,
	key string,
	required bool,
) (string, error) {
	match, ok, err := p.structuralMappingEntry(item, key)
	if err != nil {
		return "", err
	}
	if !ok {
		if !required {
			return "", nil
		}
		return "", p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, item)
	}
	if match.value.Kind != yaml.ScalarNode || match.value.Tag != "!!str" || match.value.Value == "" {
		return "", p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, match.value)
	}
	return match.value.Value, nil
}

func migratedRenderedCitationContentContext(ctx context.Context, expected legacyCitationMapping) (string, error) {
	projected, err := buildExpectedCitationProjectionContext(
		ctx,
		expected,
		MarkdownCitationEntry{},
		"\n",
	)
	if err != nil {
		return "", err
	}
	return projected.DefinitionContent, nil
}

func migratedCitationExpectation(
	ctx context.Context,
	entry store.LegacyCitationMapping,
) (legacyCitationMapping, string, error) {
	prefix := "- "
	if entry.LegacyNumber != 0 {
		prefix = fmt.Sprintf("[%d] ", entry.LegacyNumber)
	}
	source := []byte("# Citations\n\n" + prefix + entry.LegacyEntry + "\n")
	ownership, err := CollectMarkdownMigrationOwnership(ctx, source)
	if err != nil {
		return legacyCitationMapping{}, "", err
	}
	if len(ownership.CitationEntries) != 1 {
		return legacyCitationMapping{}, "", markdownPresentationErrorAt(
			"migration_replay_mismatch",
			ErrUnsupportedPresentation,
			SourceSpan{Start: 0, End: len(source)},
		)
	}
	mapping := legacyCitationMapping{
		LegacyNumber: entry.LegacyNumber,
		LegacyEntry:  entry.LegacyEntry,
		SourceID:     entry.SourceID,
		Title:        entry.Title,
		Resource:     entry.Resource,
	}
	projected, err := buildExpectedCitationProjectionContext(ctx, mapping, ownership.CitationEntries[0], "\n")
	if err != nil {
		return legacyCitationMapping{}, "", err
	}
	return projected.Mapping, projected.DefinitionContent, nil
}

func migrationVersion(p *presentation) (string, bool, error) {
	match, ok, err := p.structuralMappingEntry(p.root, "okf_version")
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	if match.value.Kind != yaml.ScalarNode || match.value.Tag != "!!str" {
		return "", true, fmt.Errorf("%w: invalid string okf_version", ErrUnsupportedPresentation)
	}
	return match.value.Value, true, nil
}

func migrateV01Document(
	ctx context.Context,
	source []byte,
	name string,
	request store.V01ToV02Migration,
	explicitAt string,
	citations store.DocumentCitationMigration,
	computation store.ComputationMigration,
) ([]byte, error) {
	var err error
	var resolvedCitations []legacyCitationMapping
	if citations.Path != "" {
		resolvedCitations, err = resolvedCitationEntries(ctx, source, citations.Entries)
		if err != nil {
			return nil, err
		}
	} else {
		ownership, err := CollectMarkdownMigrationOwnership(ctx, source)
		if err != nil {
			return nil, err
		}
		if legacyCitationsActive(ownership.CitationsSectionIndex) {
			span := ownership.Sections[ownership.CitationsSectionIndex].Span
			return nil, markdownPresentationErrorAt("missing_explicit_citation_mapping", ErrUnsupportedPresentation, span)
		}
	}
	requiresYAML := validator.DetermineFileRole(name) == validator.RoleConcept ||
		(name == "index.md" && documentlayout.HasOpeningDelimiter(source))
	if citations.Path != "" {
		if err := validateResolvedCitationReplayEvidence(
			resolvedCitations,
			validator.DetermineFileRole(name) == validator.RoleConcept,
		); err != nil {
			return nil, err
		}
	}
	if !requiresYAML {
		if explicitAt != "" || computation.Path != "" {
			return nil, fmt.Errorf("%w: reserved migration document cannot own generated or computation metadata", ErrUnsupportedPresentation)
		}
		if citations.Path == "" {
			return appendBytesContext(ctx, nil, source)
		}
		return rewriteLegacyCitationsContext(ctx, source, resolvedCitations)
	}
	p, err := parsePresentationContext(ctx, source)
	if err != nil {
		return nil, err
	}
	mutation := p.newYAMLMutationContext(p.ctx)
	if err := stageTimestampMigration(mutation, request, explicitAt); err != nil {
		return nil, err
	}
	if citations.Path != "" {
		if citations.Path != name {
			return nil, fmt.Errorf("%w: citation path mismatch", ErrUnsupportedPresentation)
		}
		if validator.DetermineFileRole(name) == validator.RoleConcept {
			if err := stageCitationSources(mutation, resolvedCitations); err != nil {
				return nil, err
			}
		}
	}
	if computation.Path != "" {
		if computation.Path != name {
			return nil, fmt.Errorf("%w: computation path mismatch", ErrUnsupportedPresentation)
		}
	}
	updated, err := mutation.apply()
	if err != nil {
		return nil, err
	}
	if computation.Path != "" {
		updated, err = reconcileAttestedComputationYAMLBytesContext(ctx, updated, computation.Contract)
		if err != nil {
			return nil, err
		}
	}
	if citations.Path != "" {
		updated, err = rewriteLegacyCitationsContext(ctx, updated, resolvedCitations)
		if err != nil {
			return nil, err
		}
	}
	if computation.Path != "" {
		updated, err = applyComputationBoundary(ctx, updated, computation)
		if err != nil {
			return nil, err
		}
	}
	return updated, nil
}

func resolvedCitationEntries(ctx context.Context, source []byte, entries []store.LegacyCitationMapping) ([]legacyCitationMapping, error) {
	ownership, err := CollectMarkdownMigrationOwnership(ctx, source)
	if err != nil {
		return nil, err
	}
	internal := make([]legacyCitationMapping, len(entries))
	for index, mapping := range entries {
		internal[index] = legacyCitationMapping{
			LegacyNumber: mapping.LegacyNumber,
			LegacyEntry:  mapping.LegacyEntry,
			SourceID:     mapping.SourceID,
			Title:        mapping.Title,
			Resource:     mapping.Resource,

			callerLegacyEntry: mapping.LegacyEntry != "",
			callerTitle:       mapping.Title != "",
			callerResource:    mapping.Resource != "",
		}
	}
	return resolveCitationMappingsContext(ctx, ownership.CitationEntries, internal)
}

func validateResolvedCitationReplayEvidence(
	resolved []legacyCitationMapping,
	requireSources bool,
) error {
	for _, mapping := range resolved {
		if mapping.callerLegacyEntry {
			continue
		}
		if mapping.linkTitle != "" {
			return markdownPresentationErrorAt(
				"migration_citation_replay_evidence_missing",
				ErrUnsupportedPresentation,
				mapping.sourceSpan,
			)
		}
		if requireSources {
			continue
		}
		if mapping.callerResource &&
			(mapping.Title == "" || mapping.callerTitle) {
			continue
		}
		return markdownPresentationErrorAt(
			"migration_citation_replay_evidence_missing",
			ErrUnsupportedPresentation,
			mapping.sourceSpan,
		)
	}
	return nil
}

// verifyTargetGeneratedReplay is a read-only assertion over an already
// migrated document. Transition staging may fill or replace metadata; target
// replay must instead compare caller input with existing structural owners.
func verifyTargetGeneratedReplay(
	p *presentation,
	request store.V01ToV02Migration,
	explicitAt string,
) error {
	timestamp, hasTimestamp, err := p.structuralMappingEntry(p.root, "timestamp")
	if err != nil {
		return err
	}
	generated, hasGenerated, err := p.structuralMappingEntry(p.root, "generated")
	if err != nil {
		return err
	}
	if !hasGenerated {
		if hasTimestamp {
			return p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, timestamp.value)
		}
		if explicitAt != "" {
			return yamlPresentationError(
				"migration_replay_mismatch",
				ErrUnsupportedPresentation,
				SourceSpan{Start: p.yamlEnd, End: p.yamlEnd},
			)
		}
		return nil
	}
	if generated.value.Kind != yaml.MappingNode {
		return p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, generated.value)
	}
	by, hasBy, err := p.structuralMappingEntry(generated.value, "by")
	if err != nil {
		return err
	}
	at, hasAt, err := p.structuralMappingEntry(generated.value, "at")
	if err != nil {
		return err
	}
	if !hasBy || by.value.Kind != yaml.ScalarNode || by.value.Tag != "!!str" || by.value.Value == "" {
		return p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, generated.value)
	}
	if !hasAt || at.value.Kind != yaml.ScalarNode ||
		(at.value.Tag != "!!str" && at.value.Tag != "!!timestamp") ||
		at.value.Value == "" {
		return p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, generated.value)
	}
	generatedInstant, err := time.Parse(time.RFC3339Nano, at.value.Value)
	if err != nil {
		return p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, at.value)
	}
	// GeneratedBy is creation input, not an assertion over pre-existing
	// generated metadata. The transition proof authenticates values created by
	// the planner; a target replay only proves that an existing producer is
	// structurally valid and must preserve its identity.
	if explicitAt != "" {
		explicitInstant, parseErr := time.Parse(time.RFC3339Nano, explicitAt)
		if parseErr != nil || !generatedInstant.Equal(explicitInstant) {
			return p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, at.value)
		}
	}
	if !hasTimestamp {
		return nil
	}
	if request.TimestampPolicy == store.LegacyTimestampRemoveAfterCopy {
		return p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, timestamp.value)
	}
	if timestamp.value.Kind != yaml.ScalarNode ||
		(timestamp.value.Tag != "!!str" && timestamp.value.Tag != "!!timestamp") ||
		timestamp.value.Value == "" {
		return p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, timestamp.value)
	}
	timestampInstant, err := time.Parse(time.RFC3339Nano, timestamp.value.Value)
	if err != nil {
		return p.yamlNodeError("migration_replay_mismatch", ErrUnsupportedPresentation, timestamp.value)
	}
	// use_generated explicitly permits preserving a conflicting legacy value.
	// Otherwise a preserved timestamp is redundant and must denote the same
	// instant as generated.at, including offset/fraction equivalence.
	if request.TimestampConflict != store.TimestampConflictUseGenerated &&
		!timestampInstant.Equal(generatedInstant) {
		return p.yamlNodeError(
			"migration_replay_mismatch",
			ErrUnsupportedPresentation,
			timestamp.value,
			at.value,
		)
	}
	return nil
}

func stageTimestampMigration(mutation *yamlMutation, request store.V01ToV02Migration, explicitAt string) error {
	p := mutation.p
	timestamp, hasTimestamp, err := p.structuralMappingEntry(p.root, "timestamp")
	if err != nil {
		return err
	}
	generated, hasGenerated, err := p.structuralMappingEntry(p.root, "generated")
	if err != nil {
		return err
	}
	if !hasTimestamp && !hasGenerated && explicitAt == "" {
		return nil
	}
	timestampValue := ""
	var timestampInstant time.Time
	if hasTimestamp {
		if timestamp.value.Kind == yaml.AliasNode {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, timestamp.value)
		}
		if timestamp.value.Kind != yaml.ScalarNode ||
			(timestamp.value.Tag != "!!str" && timestamp.value.Tag != "!!timestamp") ||
			timestamp.value.Value == "" {
			return p.yamlNodeError("invalid_legacy_timestamp", ErrUnsupportedPresentation, timestamp.value)
		}
		timestampValue = timestamp.value.Value
		timestampInstant, err = time.Parse(time.RFC3339Nano, timestampValue)
		if err != nil {
			return p.yamlNodeError("invalid_legacy_timestamp", ErrUnsupportedPresentation, timestamp.value)
		}
	}
	if !hasGenerated {
		if request.GeneratedBy == "" {
			return p.yamlNodeError("missing_explicit_generated_by", ErrUnsupportedPresentation, p.root)
		}
		at := timestampValue
		effectiveNode := p.root
		if at == "" {
			at = explicitAt
		} else {
			effectiveNode = timestamp.value
		}
		if at == "" {
			return p.yamlNodeError("missing_explicit_generated_at", ErrUnsupportedPresentation, p.root)
		}
		renderedAt, effectiveInstant, err := migrationDateTime(at)
		if err != nil {
			return p.yamlNodeError("invalid_generated_at", ErrUnsupportedPresentation, p.root)
		}
		if err := requireExplicitGeneratedAt(
			p,
			explicitAt,
			effectiveInstant,
			effectiveNode,
		); err != nil {
			return err
		}
		if err := mutation.insertMappingValue(p.root, "generated", yamlMapping(
			yamlEntry("by", yamlString(request.GeneratedBy)),
			yamlEntry("at", renderedAt),
		)); err != nil {
			return err
		}
		if hasTimestamp && request.TimestampPolicy == store.LegacyTimestampRemoveAfterCopy {
			return mutation.deleteMappingValue(p.root, "timestamp")
		}
		return nil
	}

	if generated.value.Kind != yaml.MappingNode {
		return p.yamlNodeError("generated_not_mapping", ErrUnsupportedPresentation, generated.value)
	}
	by, hasBy, err := p.structuralMappingEntry(generated.value, "by")
	if err != nil {
		return err
	}
	at, hasAt, err := p.structuralMappingEntry(generated.value, "at")
	if err != nil {
		return err
	}
	if !hasBy || by.value.Kind != yaml.ScalarNode || by.value.Tag != "!!str" || by.value.Value == "" {
		return p.yamlNodeError("missing_generated_by", ErrUnsupportedPresentation, generated.value)
	}
	if !hasAt || at.value.Kind != yaml.ScalarNode ||
		(at.value.Tag != "!!str" && at.value.Tag != "!!timestamp") ||
		at.value.Value == "" {
		return p.yamlNodeError("missing_generated_at", ErrUnsupportedPresentation, generated.value)
	}
	generatedInstant, err := time.Parse(time.RFC3339Nano, at.value.Value)
	if err != nil {
		return p.yamlNodeError("invalid_generated_at", ErrUnsupportedPresentation, at.value)
	}
	effectiveInstant := generatedInstant
	effectiveNode := at.value
	useTimestamp := false
	if hasTimestamp && !timestampInstant.Equal(generatedInstant) {
		switch request.TimestampConflict {
		case store.TimestampConflictReject:
			return p.yamlNodeError("timestamp_generated_conflict", ErrAmbiguousPresentation, timestamp.value, at.value)
		case store.TimestampConflictUseGenerated:
		case store.TimestampConflictUseTimestamp:
			effectiveInstant = timestampInstant
			effectiveNode = timestamp.value
			useTimestamp = true
		default:
			return p.yamlNodeError("timestamp_conflict_policy", ErrUnsupportedPresentation, timestamp.value, at.value)
		}
	}
	if err := requireExplicitGeneratedAt(
		p,
		explicitAt,
		effectiveInstant,
		effectiveNode,
	); err != nil {
		return err
	}
	if useTimestamp {
		renderedAt, _, err := migrationDateTime(timestampValue)
		if err != nil {
			return err
		}
		if err := mutation.replaceMappingValue(generated.value, "at", renderedAt); err != nil {
			return err
		}
	}
	if hasTimestamp && request.TimestampPolicy == store.LegacyTimestampRemoveAfterCopy {
		return mutation.deleteMappingValue(p.root, "timestamp")
	}
	return nil
}

func requireExplicitGeneratedAt(
	p *presentation,
	explicitAt string,
	effectiveInstant time.Time,
	effectiveNode *yaml.Node,
) error {
	if explicitAt == "" {
		return nil
	}
	explicitInstant, err := time.Parse(time.RFC3339Nano, explicitAt)
	if err != nil {
		return p.yamlNodeError("invalid_generated_at", ErrUnsupportedPresentation, effectiveNode)
	}
	if !explicitInstant.Equal(effectiveInstant) {
		return p.yamlNodeError(
			"explicit_generated_at_mismatch",
			ErrAmbiguousPresentation,
			effectiveNode,
		)
	}
	return nil
}

func migrationDateTime(value string) (yamlRenderValue, time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return yamlRenderValue{}, time.Time{}, err
	}
	return yamlRenderValue{kind: yamlRenderDateTime, text: value}, parsed, nil
}

func stageCitationSources(mutation *yamlMutation, entries []legacyCitationMapping) error {
	p := mutation.p
	sources, hasSources, err := p.structuralMappingEntry(p.root, "sources")
	if err != nil {
		return err
	}
	if hasSources {
		return p.yamlNodeError("sources_citations_conflict", ErrAmbiguousPresentation, sources.value)
	}
	unique := make([]legacyCitationMapping, 0, len(entries))
	seenSourceIDs := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		normalizedID, err := normalizeMarkdownLabelContext(mutation.ctx, entry.SourceID)
		if err != nil {
			return err
		}
		if _, seen := seenSourceIDs[normalizedID]; seen {
			continue
		}
		seenSourceIDs[normalizedID] = struct{}{}
		unique = append(unique, entry)
	}
	rendered := make([]yamlRenderValue, len(unique))
	for index, entry := range unique {
		fields := []yamlRenderEntry{yamlEntry("id", yamlString(entry.SourceID))}
		if entry.Title != "" {
			fields = append(fields, yamlEntry("title", yamlString(entry.Title)))
		}
		if entry.Resource != "" {
			fields = append(fields, yamlEntry("resource", yamlString(entry.Resource)))
		}
		rendered[index] = yamlMapping(fields...)
	}
	return mutation.insertMappingValue(p.root, "sources", yamlSequence(rendered...))
}

func applyComputationBoundary(ctx context.Context, source []byte, migration store.ComputationMigration) ([]byte, error) {
	bodyStart, err := markdownBodyStartContext(ctx, source)
	if err != nil {
		return nil, err
	}
	inspection, err := markdownowner.InspectComputationContext(ctx, string(source[bodyStart:]))
	if err != nil {
		return nil, err
	}
	span := computationInspectionSpan(inspection, bodyStart, len(source))
	if inspection.State == markdownowner.ComputationMalformed {
		return nil, markdownPresentationErrorAt("ambiguous_computation_boundary", ErrAmbiguousPresentation, span)
	}
	switch migration.Contract.Mode {
	case store.AttestedComputationModeInline:
		if inspection.State == markdownowner.ComputationNoSection {
			newline := markdownNewline(source)
			prefix := ""
			if len(source) != 0 && source[len(source)-1] != '\n' {
				prefix = newline
			}
			created := []byte(prefix + "# Computation" + newline + newline)
			created = append(created, renderComputationFence(
				source,
				migration.Contract.InlineLanguage,
				migration.Contract.InlineComputation,
			)...)
			updated, err := applyBytePatchesContext(ctx, source, []bytePatch{{
				Start: len(source),
				End:   len(source),
				Text:  created,
			}})
			if err != nil {
				return nil, err
			}
			return verifyComputationRewrite(ctx, updated, migration.Contract)
		}
		if inspection.State != markdownowner.ComputationInline {
			return nil, markdownPresentationErrorAt("missing_computation_boundary", ErrUnsupportedPresentation, span)
		}
		fence := inspection.DirectFences[0]
		if fence.Content != computationFenceContent(source, migration.Contract.InlineComputation) ||
			fence.Info != migration.Contract.InlineLanguage {
			return nil, markdownPresentationErrorAt("sanctioned_computation_mismatch", ErrUnsupportedPresentation, shiftedFenceSpan(fence, bodyStart))
		}
		return appendBytesContext(ctx, nil, source)
	case store.AttestedComputationModeFile:
	default:
		return nil, markdownPresentationErrorAt("computation_mode_mismatch", ErrUnsupportedPresentation, span)
	}
	switch inspection.State {
	case markdownowner.ComputationNoSection:
		return appendBytesContext(ctx, nil, source)
	case markdownowner.ComputationEmptySection:
		return nil, markdownPresentationErrorAt("missing_computation_boundary", ErrUnsupportedPresentation, span)
	case markdownowner.ComputationInline:
	default:
		return nil, markdownPresentationErrorAt("ambiguous_computation_boundary", ErrAmbiguousPresentation, span)
	}
	fence := inspection.DirectFences[0]
	if migration.Asset == nil || fence.Content != string(migration.Asset.Content) {
		return nil, markdownPresentationErrorAt("sanctioned_computation_mismatch", ErrUnsupportedPresentation, shiftedFenceSpan(fence, bodyStart))
	}
	// File-backed migration removes only the parser-proven heading and direct
	// fence. Prose, comments, raw HTML, and future Markdown siblings in the
	// former section remain byte-for-byte untouched.
	heading := inspection.Sections[0].Heading
	return applyBytePatchesContext(ctx, source, []bytePatch{
		{Start: bodyStart + heading.Start, End: bodyStart + heading.End},
		{Start: bodyStart + fence.Start, End: bodyStart + fence.End},
	})
}

func computationInspectionSpan(inspection markdownowner.ComputationInspection, bodyStart, length int) SourceSpan {
	span := SourceSpan{}
	for _, section := range inspection.Sections {
		span = unionSourceSpans(span, SourceSpan{Start: bodyStart + section.Span.Start, End: bodyStart + section.Span.End})
	}
	for _, fence := range inspection.DirectFences {
		span = unionSourceSpans(span, shiftedFenceSpan(fence, bodyStart))
	}
	for _, fence := range inspection.ConflictingFences {
		span = unionSourceSpans(span, shiftedFenceSpan(fence, bodyStart))
	}
	if span == (SourceSpan{}) && length != 0 {
		return SourceSpan{Start: 0, End: length}
	}
	return span
}

func shiftedFenceSpan(fence markdownowner.Fence, bodyStart int) SourceSpan {
	return SourceSpan{Start: bodyStart + fence.Start, End: bodyStart + fence.End}
}

func setMigrationBundleVersion(ctx context.Context, source []byte, version string) ([]byte, error) {
	if !documentlayout.HasOpeningDelimiter(source) {
		newline := "\n"
		if bytes.Contains(source, []byte("\r\n")) {
			newline = "\r\n"
		}
		versionToken, err := yamlScalarTokenContext(ctx, version, 0)
		if err != nil {
			return nil, err
		}
		header := []byte("---" + newline + "okf_version: " + versionToken + newline + "---" + newline)
		return appendBytesContext(ctx, header, source)
	}
	p, err := parsePresentationContext(ctx, source)
	if err != nil {
		return nil, err
	}
	match, ok, err := p.structuralMappingEntry(p.root, "okf_version")
	if err != nil {
		return nil, err
	}
	if !ok {
		return p.insertMappingValue(p.root, "okf_version", yamlString(version))
	}
	_ = match
	return p.replaceMappingValue(p.root, "okf_version", yamlString(version))
}
