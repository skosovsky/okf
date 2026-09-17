package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
	"github.com/skosovsky/okf/validator"
)

const migrationTransactionActor = store.Actor("tool:okf-mcp")

var errMigrationAuthorizationPreflightSnapshot = errors.New(
	"mcpserver: migration authorization preflight reached snapshot boundary",
)

var (
	errExpectedMigrationSourceBlocked = errors.New(
		"mcpserver: blocked migration source cannot be applied",
	)
	errExpectedMigrationSourceInvalid = errors.New(
		"mcpserver: expected migration source is invalid",
	)
	errExpectedMigrationSourcePlanMismatch = errors.New(
		"mcpserver: expected migration source does not describe an applicable transition",
	)
)

type migrationIOObserver struct {
	sourceOpen func()
	sourceRead func()
	storeOpen  func()
}

type migrationIOObserverContextKey struct{}

func withMigrationIOObserver(
	ctx context.Context,
	observer migrationIOObserver,
) context.Context {
	return context.WithValue(ctx, migrationIOObserverContextKey{}, observer)
}

func observeMigrationIO(ctx context.Context, selectCallback func(migrationIOObserver) func()) {
	observer, ok := ctx.Value(migrationIOObserverContextKey{}).(migrationIOObserver)
	if !ok {
		return
	}
	if callback := selectCallback(observer); callback != nil {
		callback()
	}
}

type migrationGeneratedAtInput struct {
	ConceptID string `json:"concept_id"`
	At        string `json:"at"`
}

type migrationCitationEntryInput struct {
	LegacyNumber uint64 `json:"legacy_number,omitempty"`
	LegacyEntry  string `json:"legacy_entry,omitempty"`
	SourceID     string `json:"source_id"`
	Title        string `json:"title,omitempty"`
	Resource     string `json:"resource,omitempty"`
}

type migrationCitationInput struct {
	Path    string                        `json:"path"`
	Entries []migrationCitationEntryInput `json:"entries"`
}

type migrationAssetInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type migrationComputationInput struct {
	ConceptID string                        `json:"concept_id"`
	Contract  patchAttestedComputationInput `json:"contract"`
	Asset     *migrationAssetInput          `json:"asset,omitempty"`
}

type migrationArguments struct {
	BundlePath              string                      `json:"bundle_path"`
	From                    string                      `json:"from,omitempty"`
	GeneratedBy             string                      `json:"generated_by"`
	GeneratedAt             []migrationGeneratedAtInput `json:"generated_at,omitempty"`
	TimestampConflictPolicy string                      `json:"timestamp_conflict_policy,omitempty"`
	TimestampPolicy         string                      `json:"timestamp_policy"`
	CitationMappings        []migrationCitationInput    `json:"citation_mappings"`
	Computations            []migrationComputationInput `json:"computations,omitempty"`
	ExpectedPlanDigest      string                      `json:"expected_plan_digest,omitempty"`
	ExpectedSource          *migrationSourceDTO         `json:"expected_source,omitempty"`
	Proof                   *migrationPlanProofDTO      `json:"proof,omitempty"`
}

type migrationPlanWriteDTO struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type migrationRenameDTO struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type migrationFileChangeDTO struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
	From string `json:"from"`
}

type migrationPlanProofDTO struct {
	FormatVersion    uint16                   `json:"format_version"`
	RequestDigest    string                   `json:"request_digest"`
	ResolutionDigest string                   `json:"resolution_digest"`
	BaseRevision     string                   `json:"base_revision"`
	ResultRevision   string                   `json:"result_revision"`
	Reads            []string                 `json:"reads"`
	Writes           []migrationPlanWriteDTO  `json:"writes"`
	Deletes          []string                 `json:"deletes"`
	Renames          []migrationRenameDTO     `json:"renames"`
	AffectedRefs     []string                 `json:"affected_refs"`
	ReverseImpact    []string                 `json:"reverse_impact"`
	ChangedFiles     []migrationFileChangeDTO `json:"changed_files"`
	ChangedRefs      []string                 `json:"changed_refs"`
}

type migrationBlockerDTO struct {
	Code     string        `json:"code"`
	Path     string        `json:"path"`
	Location sourceSpanDTO `json:"location"`
	Message  string        `json:"message"`
}

type sourceSpanDTO struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type migrationManualActionDTO struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type migrationPreviewResponse struct {
	Status         string                     `json:"status"`
	Source         migrationSourceDTO         `json:"source"`
	Proof          *migrationPlanProofDTO     `json:"proof,omitempty"`
	BaseRevision   string                     `json:"base_revision"`
	ResultRevision *string                    `json:"result_revision"`
	PlanDigest     string                     `json:"plan_digest"`
	AffectedPaths  []string                   `json:"affected_paths"`
	Diff           string                     `json:"diff"`
	DiffTruncated  bool                       `json:"diff_truncated"`
	Blockers       []migrationBlockerDTO      `json:"blockers"`
	ManualActions  []migrationManualActionDTO `json:"manual_actions"`
	Diagnostics    []wireDiagnostic           `json:"diagnostics"`
}

type migrationApplyResponse struct {
	Status         string             `json:"status"`
	Source         migrationSourceDTO `json:"source"`
	BaseRevision   string             `json:"base_revision"`
	ResultRevision string             `json:"result_revision"`
	PlanDigest     string             `json:"plan_digest"`
	ChangedPaths   []string           `json:"changed_paths"`
	Diagnostics    []wireDiagnostic   `json:"diagnostics"`
}

type migrationSourceDTO struct {
	RequestedSelector  string                        `json:"requested_selector"`
	DeclarationPresent bool                          `json:"declaration_present"`
	DeclarationValid   bool                          `json:"declaration_valid"`
	DeclarationRaw     string                        `json:"declaration_raw"`
	DeclaredVersion    *string                       `json:"declared_version"`
	ResolvedSource     string                        `json:"resolved_source"`
	ResolutionSource   string                        `json:"resolution_source"`
	FromVersion        string                        `json:"from_version"`
	ToVersion          string                        `json:"to_version"`
	Transition         string                        `json:"transition"`
	Candidates         []migrationLegacyCandidateDTO `json:"candidates"`
	Blockers           []migrationBlockerDTO         `json:"blockers"`
}

type migrationLegacyCandidateDTO struct {
	Kind     string        `json:"kind"`
	Path     string        `json:"path"`
	Location sourceSpanDTO `json:"location"`
}

func handlePreviewV02Migration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "preview_v02_migration", request); result != nil {
		return result, nil
	}
	root, result := requireValidatedBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	arguments, result := decodeMigrationArguments(request)
	if result != nil {
		return result, nil
	}
	if result := requestContextCheckpoint(ctx, "decode migration arguments"); result != nil {
		return result, nil
	}
	migrationInput, validationErr, result := canonicalMigrationInput(arguments)
	if result != nil {
		return result, nil
	}
	if result := requestContextCheckpoint(ctx, "build migration input"); result != nil {
		return result, nil
	}
	if validationErr != nil {
		if blocked, matched := migrationInputPresentationPreview(ctx, root, arguments, validationErr); matched {
			return blocked, nil
		}
		return migrationInputValidationError(validationErr), nil
	}
	source, revision, err := captureMigrationSource(ctx, root)
	if err != nil {
		return migrationSourceError("capture migration source", err), nil
	}
	planner := mutation.NewMigrationPlanner()
	resolution, resolutionErr := mutation.ResolveMigrationSource(ctx, source, mutation.MigrationSourceOptions{
		RequestedSelector: arguments.From,
	})
	if resolution.Transition == mutation.MigrationTransitionBlocked {
		response, projectionErr := migrationResolutionPreviewContext(ctx, revision, resolution, resolutionErr)
		if projectionErr != nil {
			return bundleDomainError("project migration source", "migration preview exceeds MCP output limits", projectionErr), nil
		}
		return jsonStructuredResultContext(ctx, "project migration source", response, response), nil
	}
	if resolutionErr != nil {
		return bundleDomainError("resolve migration source", "migration source exceeds MCP resource limits", resolutionErr), nil
	}
	if resolution.Transition == mutation.MigrationTransitionTargetNoop {
		preflight, preflightErr := mutation.PreflightV01ToV02MigrationInput(
			ctx,
			source,
			resolution,
			migrationInput,
		)
		if len(preflight.Blockers) > maxChangedPaths || len(preflight.ManualActions) > maxChangedPaths {
			return stableToolError("resource_limit", "migration preflight exceeds MCP output limits", false), nil
		}
		if preflightErr != nil || len(preflight.Blockers) != 0 || len(preflight.ManualActions) != 0 {
			response, projectionErr := migrationInputPreflightPreviewContext(ctx, revision, resolution, preflight, preflightErr)
			if projectionErr != nil {
				return bundleDomainError("project migration preflight", "migration preflight exceeds MCP output limits", projectionErr), nil
			}
			return jsonStructuredResultContext(ctx, "project migration preflight", response, response), nil
		}
		response, projectionErr := migrationResolutionPreviewContext(ctx, revision, resolution, nil)
		if projectionErr != nil {
			return bundleDomainError("project migration source", "migration preview exceeds MCP output limits", projectionErr), nil
		}
		return jsonStructuredResultContext(ctx, "project migration source", response, response), nil
	}
	if resolution.Transition != mutation.MigrationTransitionV01ToV02 {
		return stableToolError("unsupported_migration_source", "migration source transition is unsupported", false), nil
	}
	domain, err := migrationRequestFromInput(resolution, migrationInput)
	if err != nil {
		return bundleDomainError("identify migration request", "migration request exceeds MCP resource limits", err), nil
	}
	preview, previewErr := planner.Preview(ctx, source, resolution, domain)
	paths, pathsErr := previewPathsContext(ctx, preview.Preview)
	if pathsErr != nil {
		return bundleDomainError("project migration paths", "migration preview exceeds MCP output limits", pathsErr), nil
	}
	if len(paths) > maxChangedPaths ||
		len(preview.Blockers) > maxChangedPaths ||
		len(preview.ManualActions) > maxChangedPaths ||
		len(preview.Preview.Diagnostics) > maxChangedPaths {
		return stableToolError("resource_limit", "migration preview exceeds MCP output limits", false), nil
	}
	response, projectionErr := migrationPreviewProjection(ctx, source, revision, preview.Resolution, preview, previewErr)
	if projectionErr != nil {
		return bundleDomainError("project migration preview", "migration preview exceeds MCP output limits", projectionErr), nil
	}
	return jsonStructuredResultContext(ctx, "project migration preview", response, response), nil
}

func handleApplyV02Migration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "apply_v02_migration", request); result != nil {
		return result, nil
	}
	root, result := requireValidatedBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	arguments, result := decodeMigrationArguments(request)
	if result != nil {
		return result, nil
	}
	if result := requestContextCheckpoint(ctx, "decode migration arguments"); result != nil {
		return result, nil
	}
	migrationInput, validationErr, result := canonicalMigrationInput(arguments)
	if result != nil {
		return result, nil
	}
	if result := requestContextCheckpoint(ctx, "build migration input"); result != nil {
		return result, nil
	}
	if validationErr != nil {
		return migrationInputValidationError(validationErr), nil
	}
	expectedSource, planner, proof, domain, result := prepareMigrationApplyAuthorization(
		ctx,
		arguments,
		migrationInput,
	)
	if result != nil {
		return result, nil
	}
	source, revision, err := captureMigrationSource(ctx, root)
	if err != nil {
		return migrationSourceError("capture migration source", err), nil
	}
	resolution, resolutionErr := mutation.ResolveMigrationSource(ctx, source, mutation.MigrationSourceOptions{
		RequestedSelector: arguments.From,
	})
	if expectedSource.Transition == mutation.MigrationTransitionTargetNoop {
		if !equalMigrationSource(resolution, expectedSource) {
			return stableToolError("source_conflict", "migration source resolution changed after preview", true), nil
		}
		if resolutionErr != nil || resolution.Transition != mutation.MigrationTransitionTargetNoop {
			return stableToolError("unsupported_migration_source", "migration source is no longer a target noop", false), nil
		}
		preflight, preflightErr := mutation.PreflightV01ToV02MigrationInput(
			ctx,
			source,
			resolution,
			migrationInput,
		)
		if len(preflight.Blockers) > maxChangedPaths || len(preflight.ManualActions) > maxChangedPaths {
			return stableToolError("resource_limit", "migration preflight exceeds MCP output limits", false), nil
		}
		if preflightErr != nil || len(preflight.Blockers) != 0 || len(preflight.ManualActions) != 0 {
			if result := requestContextCheckpoint(ctx, "project migration preflight"); result != nil {
				return result, nil
			}
			return migrationInputPreflightError(preflight, preflightErr), nil
		}
		response := migrationApplyResponse{
			Status: "noop", Source: migrationSourceDTOFromDomain(resolution),
			BaseRevision: string(revision), ResultRevision: string(revision), PlanDigest: "",
			ChangedPaths: []string{}, Diagnostics: []wireDiagnostic{},
		}
		return jsonStructuredResultContext(ctx, "project migration noop", response, response), nil
	}
	expected := proof.BaseRevision
	if revision != expected {
		if expectedSource.Transition != mutation.MigrationTransitionV01ToV02 {
			return stableToolError("plan_mismatch", "non-empty plan digest requires a frozen v0.1-to-v0.2 source transition", false), nil
		}
		if resolution.Transition == mutation.MigrationTransitionTargetNoop {
			preflight, preflightErr := mutation.PreflightV01ToV02MigrationInput(
				ctx,
				source,
				resolution,
				migrationInput,
			)
			if len(preflight.Blockers) > maxChangedPaths || len(preflight.ManualActions) > maxChangedPaths {
				return stableToolError("resource_limit", "migration preflight exceeds MCP output limits", false), nil
			}
			if preflightErr != nil || len(preflight.Blockers) != 0 || len(preflight.ManualActions) != 0 {
				if result := requestContextCheckpoint(ctx, "project migration preflight"); result != nil {
					return result, nil
				}
				return migrationInputPreflightError(preflight, preflightErr), nil
			}
		}
		metadataExists, metadataErr := migrationStoreMetadataExists(root)
		if metadataErr != nil {
			return bundleDomainError("inspect transactional store metadata", "migration metadata exceeds MCP resource limits", metadataErr), nil
		}
		if !metadataExists {
			return stableToolError("revision_conflict", "bundle revision changed after migration preview", true), nil
		}
		return applyMigrationPlan(ctx, root, planner, domain, expectedSource, proof, arguments.ExpectedPlanDigest)
	}
	if !equalMigrationSource(resolution, expectedSource) {
		return stableToolError("source_conflict", "migration source resolution changed after preview", true), nil
	}
	if resolution.Transition == mutation.MigrationTransitionBlocked {
		return stableToolError("unsupported_migration_source", "migration source is blocked", false), nil
	}
	if resolutionErr != nil {
		return bundleDomainError("resolve migration source", "migration source exceeds MCP resource limits", resolutionErr), nil
	}
	if resolution.Transition != mutation.MigrationTransitionV01ToV02 {
		return stableToolError("unsupported_migration_source", "migration source transition is unsupported", false), nil
	}
	preview, previewErr := planner.Preview(ctx, source, resolution, domain)
	paths, pathsErr := previewPathsContext(ctx, preview.Preview)
	if pathsErr != nil {
		return bundleDomainError("project migration paths", "migration apply exceeds MCP output limits", pathsErr), nil
	}
	if len(paths) > maxChangedPaths ||
		len(preview.Blockers) > maxChangedPaths ||
		len(preview.ManualActions) > maxChangedPaths ||
		len(preview.Preview.Diagnostics) > maxChangedPaths {
		return stableToolError("resource_limit", "migration apply exceeds MCP output limits", false), nil
	}
	if previewErr != nil {
		if len(preview.Blockers) == 0 {
			return bundleDomainError("rebuild migration preview", "migration preview exceeds MCP resource limits", previewErr), nil
		}
		response, projectionErr := rejectedMigrationApplyContext(
			ctx,
			revision,
			resolution,
			arguments.ExpectedPlanDigest,
			preview,
		)
		if projectionErr != nil {
			return bundleDomainError("project rejected migration", "migration apply exceeds MCP output limits", projectionErr), nil
		}
		return jsonStructuredResultContext(ctx, "project rejected migration", response, response), nil
	}
	if !equalMigrationSource(preview.Resolution, expectedSource) ||
		preview.PlanDigest != arguments.ExpectedPlanDigest ||
		!reflect.DeepEqual(preview.Proof, proof) {
		return stableToolError("plan_mismatch", "migration proof or digest does not match the rebuilt plan", false), nil
	}
	if preview.Preview.BaseRevision == preview.Preview.ResultRevision ||
		len(preview.Preview.Writes)+len(preview.Preview.Deletes)+len(preview.Preview.Renames) == 0 {
		diagnostics, projectionErr := wireDiagnosticsFromStoreContext(ctx, preview.Preview.Diagnostics)
		if projectionErr != nil {
			return bundleDomainError("project migration noop", "migration apply exceeds MCP output limits", projectionErr), nil
		}
		response := migrationApplyResponse{
			Status: "noop", Source: migrationSourceDTOFromDomain(preview.Resolution),
			BaseRevision: string(preview.Preview.BaseRevision), ResultRevision: string(preview.Preview.ResultRevision),
			PlanDigest: arguments.ExpectedPlanDigest, ChangedPaths: []string{},
			Diagnostics: diagnostics,
		}
		return jsonStructuredResultContext(ctx, "project migration noop", response, response), nil
	}
	return applyMigrationPlan(ctx, root, planner, domain, expectedSource, proof, arguments.ExpectedPlanDigest)
}

func rejectedMigrationApplyContext(
	ctx context.Context,
	revision store.Revision,
	resolution mutation.MigrationSourceResolution,
	planDigest string,
	preview mutation.MigrationPreview,
) (migrationApplyResponse, error) {
	diagnostics, err := wireDiagnosticsFromStoreContext(ctx, preview.Preview.Diagnostics)
	if err != nil {
		return migrationApplyResponse{}, err
	}
	for _, blocker := range preview.Blockers {
		if err := ctx.Err(); err != nil {
			return migrationApplyResponse{}, err
		}
		diagnostics = append(diagnostics, wireDiagnostic{
			Code:     stableDiagnosticCode(blocker.Code),
			Severity: "ERROR",
			File:     filepathSafe(blocker.Path),
			Message:  redactSensitivePaths(blocker.Message),
		})
	}
	return migrationApplyResponse{
		Status: "rejected", Source: migrationSourceDTOFromDomain(resolution),
		BaseRevision: string(revision), ResultRevision: string(revision),
		PlanDigest: planDigest, ChangedPaths: []string{}, Diagnostics: diagnostics,
	}, ctx.Err()
}

func migrationStoreMetadataExists(root string) (bool, error) {
	_, err := os.Lstat(filepath.Join(root, ".okf"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func migrationStoreConfig() storefs.Config {
	return withMCPStoreLimits(storefs.Config{
		ValidatorConfig: &validator.ValidatorConfig{Strict: true, Spec: bundle.OKFVersion},
	})
}

func expectedMigrationSource(
	arguments migrationArguments,
) (mutation.MigrationSourceResolution, *mcp.CallToolResult) {
	if arguments.ExpectedSource == nil {
		return mutation.MigrationSourceResolution{}, toolError("expected_source is required")
	}
	source := *arguments.ExpectedSource
	if source.RequestedSelector != arguments.From {
		return mutation.MigrationSourceResolution{}, stableToolError(
			"plan_mismatch", "expected_source.requested_selector must match from", false,
		)
	}
	resolution := migrationSourceFromDTO(source)
	if err := validateApplicableMigrationSourceTuple(resolution); err != nil {
		switch {
		case errors.Is(err, errExpectedMigrationSourceBlocked):
			return mutation.MigrationSourceResolution{}, stableToolError(
				"unsupported_migration_source",
				"expected_source describes a blocked migration source",
				false,
			)
		case errors.Is(err, errExpectedMigrationSourceInvalid):
			return mutation.MigrationSourceResolution{}, toolError(
				"expected_source is not a canonical applicable source resolution",
			)
		default:
			return mutation.MigrationSourceResolution{}, stableToolError(
				"plan_mismatch",
				"expected_source does not describe an applicable migration transition",
				false,
			)
		}
	}
	return resolution, nil
}

func migrationSourceFromDTO(
	source migrationSourceDTO,
) mutation.MigrationSourceResolution {
	var declared string
	if source.DeclaredVersion != nil {
		declared = *source.DeclaredVersion
	}
	resolution := mutation.MigrationSourceResolution{
		RequestedSelector:  source.RequestedSelector,
		DeclarationPresent: source.DeclarationPresent,
		DeclarationValid:   source.DeclarationValid,
		DeclarationRaw:     source.DeclarationRaw,
		DeclaredVersion:    declared,
		ResolvedSource:     source.ResolvedSource,
		ResolutionSource:   mutation.MigrationResolutionSource(source.ResolutionSource),
		FromVersion:        source.FromVersion,
		ToVersion:          source.ToVersion,
		Transition:         mutation.MigrationTransition(source.Transition),
		Candidates:         make([]mutation.MigrationLegacyCandidate, len(source.Candidates)),
		Blockers:           make([]mutation.MigrationBlocker, len(source.Blockers)),
	}
	for index, candidate := range source.Candidates {
		resolution.Candidates[index] = mutation.MigrationLegacyCandidate{
			Kind: candidate.Kind, Path: candidate.Path,
			Location: mutation.SourceSpan{
				Start: candidate.Location.Start,
				End:   candidate.Location.End,
			},
		}
	}
	for index, blocker := range source.Blockers {
		resolution.Blockers[index] = mutation.MigrationBlocker{
			Code: blocker.Code, Path: blocker.Path,
			Location: mutation.SourceSpan{
				Start: blocker.Location.Start,
				End:   blocker.Location.End,
			},
			Message: blocker.Message,
		}
	}
	return resolution
}

func validateApplicableMigrationSourceTuple(
	resolution mutation.MigrationSourceResolution,
) error {
	if err := mutation.ValidateMigrationSourceResolution(resolution); err != nil {
		return fmt.Errorf("%w: %v", errExpectedMigrationSourceInvalid, err)
	}
	if resolution.Transition == mutation.MigrationTransitionBlocked {
		return errExpectedMigrationSourceBlocked
	}
	switch resolution.Transition {
	case mutation.MigrationTransitionV01ToV02:
		return nil
	case mutation.MigrationTransitionTargetNoop:
		if resolution.ResolvedSource != mutation.MigrationVersionV02 {
			return errExpectedMigrationSourcePlanMismatch
		}
		return nil
	default:
		return errExpectedMigrationSourceInvalid
	}
}

func equalMigrationSource(
	actual mutation.MigrationSourceResolution,
	expected mutation.MigrationSourceResolution,
) bool {
	return reflect.DeepEqual(
		migrationSourceDTOFromDomain(actual),
		migrationSourceDTOFromDomain(expected),
	)
}

func prepareMigrationApplyAuthorization(
	ctx context.Context,
	arguments migrationArguments,
	migrationInput store.V01ToV02Migration,
) (
	mutation.MigrationSourceResolution,
	mutation.MigrationPlanner,
	mutation.MigrationPlanProof,
	mutation.MigrationRequest,
	*mcp.CallToolResult,
) {
	expectedSource, result := expectedMigrationSource(arguments)
	if result != nil {
		return mutation.MigrationSourceResolution{},
			mutation.MigrationPlanner{},
			mutation.MigrationPlanProof{},
			mutation.MigrationRequest{},
			result
	}
	if expectedSource.Transition == mutation.MigrationTransitionTargetNoop &&
		(arguments.Proof != nil || arguments.ExpectedPlanDigest != "") {
		return mutation.MigrationSourceResolution{},
			mutation.MigrationPlanner{},
			mutation.MigrationPlanProof{},
			mutation.MigrationRequest{},
			stableToolError(
				"plan_mismatch",
				"proof and expected_plan_digest must be omitted for target-noop",
				false,
			)
	}
	planner := mutation.NewMigrationPlanner()
	var proof mutation.MigrationPlanProof
	var domain mutation.MigrationRequest
	if expectedSource.Transition != mutation.MigrationTransitionTargetNoop {
		proof, result = migrationPlanProofFromDTO(arguments.Proof)
		if result != nil {
			return mutation.MigrationSourceResolution{},
				mutation.MigrationPlanner{},
				mutation.MigrationPlanProof{},
				mutation.MigrationRequest{},
				result
		}
		if arguments.ExpectedPlanDigest == "" {
			return mutation.MigrationSourceResolution{},
				mutation.MigrationPlanner{},
				mutation.MigrationPlanProof{},
				mutation.MigrationRequest{},
				stableToolError(
					"plan_mismatch",
					"expected_plan_digest is required",
					false,
				)
		}
		if err := mutation.ValidatePlanDigest(arguments.ExpectedPlanDigest); err != nil {
			return mutation.MigrationSourceResolution{},
				mutation.MigrationPlanner{},
				mutation.MigrationPlanProof{},
				mutation.MigrationRequest{},
				stableToolError(
					"plan_mismatch",
					"expected_plan_digest is not canonical",
					false,
				)
		}
	}
	if expectedSource.Transition == mutation.MigrationTransitionV01ToV02 {
		frozenDomain, err := migrationRequestFromInput(expectedSource, migrationInput)
		if err != nil {
			return mutation.MigrationSourceResolution{},
				mutation.MigrationPlanner{},
				mutation.MigrationPlanProof{},
				mutation.MigrationRequest{},
				toolErrorf("identify migration request: %v", err)
		}
		domain = frozenDomain
		if result := preflightMigrationApplyAuthorization(
			ctx,
			planner,
			domain,
			expectedSource,
			proof,
			arguments.ExpectedPlanDigest,
		); result != nil {
			return mutation.MigrationSourceResolution{},
				mutation.MigrationPlanner{},
				mutation.MigrationPlanProof{},
				mutation.MigrationRequest{},
				result
		}
	}
	return expectedSource, planner, proof, domain, nil
}

type migrationAuthorizationPreflightStore struct{}

func (migrationAuthorizationPreflightStore) Snapshot(context.Context) (store.Snapshot, error) {
	return nil, errMigrationAuthorizationPreflightSnapshot
}

func (migrationAuthorizationPreflightStore) Preview(
	context.Context,
	store.ChangeSet,
) (store.Preview, error) {
	return store.Preview{}, errors.New(
		"mcpserver: migration authorization preflight unexpectedly reached store preview",
	)
}

func (migrationAuthorizationPreflightStore) Commit(
	context.Context,
	store.ChangeSet,
	store.CommitOptions,
) (store.CommitReceipt, error) {
	return store.CommitReceipt{}, errors.New(
		"mcpserver: migration authorization preflight unexpectedly reached store commit",
	)
}

func preflightMigrationApplyAuthorization(
	ctx context.Context,
	planner mutation.MigrationPlanner,
	domain mutation.MigrationRequest,
	source mutation.MigrationSourceResolution,
	proof mutation.MigrationPlanProof,
	planDigest string,
) *mcp.CallToolResult {
	_, err := planner.Apply(ctx, migrationAuthorizationPreflightStore{}, mutation.MigrationApplyRequest{
		Request: domain, Resolution: source, Proof: proof, PlanDigest: planDigest,
		Options: store.CommitOptions{},
	})
	switch {
	case errors.Is(err, errMigrationAuthorizationPreflightSnapshot):
		return nil
	case errors.Is(err, mutation.ErrMigrationPlanMismatch):
		return stableToolError(
			"plan_mismatch",
			"migration proof or digest does not match the frozen migration request",
			false,
		)
	default:
		return bundleDomainError("validate migration authorization", "migration authorization exceeds MCP resource limits", err)
	}
}

func applyMigrationPlan(
	ctx context.Context,
	root string,
	planner mutation.MigrationPlanner,
	domain mutation.MigrationRequest,
	source mutation.MigrationSourceResolution,
	proof mutation.MigrationPlanProof,
	planDigest string,
) (*mcp.CallToolResult, error) {
	if result := requestContextCheckpoint(ctx, "open migration store"); result != nil {
		return result, nil
	}
	observeMigrationIO(ctx, func(observer migrationIOObserver) func() {
		return observer.storeOpen
	})
	opened, err := storefs.OpenContext(ctx, root, migrationStoreConfig())
	if err != nil {
		return bundleDomainError("open transactional store", "migration store exceeds MCP resource limits", err), nil
	}
	defer opened.Close()
	snapshot, err := opened.Snapshot(ctx)
	if err != nil {
		return bundleDomainError("snapshot migration bundle", "migration snapshot exceeds MCP resource limits", err), nil
	}
	if _, err := loadBoundedSource(ctx, snapshot); err != nil {
		return bundleDomainError("bound migration snapshot", "migration snapshot exceeds MCP resource limits", err), nil
	}
	if result := requestContextCheckpoint(ctx, "commit migration plan"); result != nil {
		return result, nil
	}
	return applyMigrationPlanWithStore(ctx, planner, opened, domain, source, proof, planDigest)
}

type migrationApplyPlanner interface {
	Apply(
		context.Context,
		store.Store,
		mutation.MigrationApplyRequest,
	) (store.CommitReceipt, error)
}

func applyMigrationPlanWithStore(
	ctx context.Context,
	planner migrationApplyPlanner,
	opened store.Store,
	domain mutation.MigrationRequest,
	source mutation.MigrationSourceResolution,
	proof mutation.MigrationPlanProof,
	planDigest string,
) (*mcp.CallToolResult, error) {
	request := mutation.MigrationApplyRequest{
		Request: domain, Resolution: source, Proof: proof, PlanDigest: planDigest,
		Options: store.CommitOptions{},
	}
	receipt, applyErr := planner.Apply(ctx, opened, request)
	receipt, err := classifyMCPMigrationCommitOutcome(request, receipt, applyErr)
	if err != nil {
		return migrationApplyError(err), nil
	}
	status := "applied"
	if receipt.BaseRevision == receipt.ResultRevision {
		status = "noop"
	}
	response := migrationApplyResponse{
		Status: status, Source: migrationSourceDTOFromDomain(source),
		BaseRevision: string(receipt.BaseRevision), ResultRevision: string(receipt.ResultRevision),
		PlanDigest: planDigest, ChangedPaths: changedReceiptPaths(receipt),
		Diagnostics: []wireDiagnostic{},
	}
	return jsonStructuredResult(response, response), nil
}

func migrationApplyError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, store.ErrStorageCorrupt):
		return stableToolError("operation_rejected", "migration commit receipt failed integrity validation", false)
	case errors.Is(err, mutation.ErrMigrationPlanMismatch):
		return stableToolError("plan_mismatch", "expected_plan_digest does not match the rebuilt migration plan", false)
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return stableToolError("operation_cancelled", "apply v0.2 migration was cancelled", true)
	}
	var conflict *store.Conflict
	if errors.As(err, &conflict) {
		return stableToolError("revision_conflict", "bundle revision changed after migration preview", conflict.Retryable)
	}
	return toolErrorf("apply v0.2 migration: %v", err)
}

func classifyMCPMigrationCommitOutcome(
	request mutation.MigrationApplyRequest,
	receipt store.CommitReceipt,
	applyErr error,
) (store.CommitReceipt, error) {
	var committed *store.CommittedError
	if applyErr != nil && !errors.As(applyErr, &committed) {
		return store.CommitReceipt{}, applyErr
	}
	if err := store.ValidateCommitReceipt(receipt); err != nil {
		invalid := fmt.Errorf("apply migration returned invalid commit receipt: %w", err)
		if applyErr != nil {
			return store.CommitReceipt{}, errors.Join(applyErr, invalid)
		}
		return store.CommitReceipt{}, invalid
	}
	if applyErr != nil {
		committedReceipt := committed.Receipt()
		if err := store.ValidateCommitReceipt(committedReceipt); err != nil {
			return store.CommitReceipt{}, errors.Join(
				applyErr,
				fmt.Errorf("apply migration committed error contains invalid receipt: %w", err),
			)
		}
		if !reflect.DeepEqual(receipt, committedReceipt) {
			return store.CommitReceipt{}, errors.Join(
				applyErr,
				fmt.Errorf("%w: returned receipt does not match committed error receipt", mutation.ErrMigrationPlanMismatch),
			)
		}
	}
	if err := validateMCPMigrationReceipt(request, receipt); err != nil {
		if applyErr != nil {
			return store.CommitReceipt{}, errors.Join(applyErr, err)
		}
		return store.CommitReceipt{}, err
	}
	return receipt.Clone(), nil
}

func validateMCPMigrationReceipt(
	request mutation.MigrationApplyRequest,
	receipt store.CommitReceipt,
) error {
	expectedKey, err := mutation.PlanDigestIdempotencyKey(
		"migration:v0.1-to-v0.2",
		request.PlanDigest,
		request.Options.IdempotencyKey,
	)
	if err != nil {
		return fmt.Errorf("%w: derive migration receipt identity: %v", mutation.ErrMigrationPlanMismatch, err)
	}
	if receipt.ChangeSetID != request.Request.ID ||
		receipt.IdempotencyKey != expectedKey ||
		receipt.RequestDigest != request.Proof.RequestDigest ||
		receipt.BaseRevision != request.Proof.BaseRevision ||
		receipt.ResultRevision != request.Proof.ResultRevision ||
		!reflect.DeepEqual(receipt.ChangedFiles, request.Proof.ChangedFiles) ||
		!equalMCPMigrationReceiptRefs(receipt.ChangedRefs, request.Proof.ChangedRefs) {
		return fmt.Errorf("%w: commit receipt does not match the authorized migration plan", mutation.ErrMigrationPlanMismatch)
	}
	return nil
}

func equalMCPMigrationReceiptRefs(left, right []bundle.RelationRef) bool {
	if len(left) != len(right) {
		return false
	}
	identities := make(map[string]struct{}, len(left))
	for _, ref := range left {
		identity := ref.String()
		if _, duplicate := identities[identity]; duplicate {
			return false
		}
		identities[identity] = struct{}{}
	}
	for _, ref := range right {
		identity := ref.String()
		if _, exists := identities[identity]; !exists {
			return false
		}
		delete(identities, identity)
	}
	return len(identities) == 0
}

func migrationPlanProofDTOFromDomainContext(
	ctx context.Context,
	proof mutation.MigrationPlanProof,
) (migrationPlanProofDTO, error) {
	if ctx == nil {
		return migrationPlanProofDTO{}, errNilRequestContext
	}
	dto := migrationPlanProofDTO{
		FormatVersion: proof.FormatVersion, RequestDigest: proof.RequestDigest,
		ResolutionDigest: proof.ResolutionDigest,
		BaseRevision:     string(proof.BaseRevision), ResultRevision: string(proof.ResultRevision),
		Reads: make([]string, len(proof.Reads)), Writes: make([]migrationPlanWriteDTO, len(proof.Writes)),
		Deletes: append([]string{}, proof.Deletes...), Renames: make([]migrationRenameDTO, len(proof.Renames)),
		AffectedRefs:  make([]string, len(proof.AffectedRefs)),
		ReverseImpact: make([]string, len(proof.ReverseImpact)),
		ChangedFiles:  make([]migrationFileChangeDTO, len(proof.ChangedFiles)),
		ChangedRefs:   make([]string, len(proof.ChangedRefs)),
	}
	for index, read := range proof.Reads {
		if err := ctx.Err(); err != nil {
			return migrationPlanProofDTO{}, err
		}
		dto.Reads[index] = read.Path
	}
	for index, write := range proof.Writes {
		if err := ctx.Err(); err != nil {
			return migrationPlanProofDTO{}, err
		}
		dto.Writes[index] = migrationPlanWriteDTO{Path: write.Path, Digest: write.Digest}
	}
	for index, rename := range proof.Renames {
		if err := ctx.Err(); err != nil {
			return migrationPlanProofDTO{}, err
		}
		dto.Renames[index] = migrationRenameDTO{From: rename.From, To: rename.To}
	}
	for index, ref := range proof.AffectedRefs {
		if err := ctx.Err(); err != nil {
			return migrationPlanProofDTO{}, err
		}
		dto.AffectedRefs[index] = ref.String()
	}
	for index, ref := range proof.ReverseImpact {
		if err := ctx.Err(); err != nil {
			return migrationPlanProofDTO{}, err
		}
		dto.ReverseImpact[index] = ref.String()
	}
	for index, change := range proof.ChangedFiles {
		if err := ctx.Err(); err != nil {
			return migrationPlanProofDTO{}, err
		}
		dto.ChangedFiles[index] = migrationFileChangeDTO{
			Kind: string(change.Kind), Path: change.Path, From: change.From,
		}
	}
	for index, ref := range proof.ChangedRefs {
		if err := ctx.Err(); err != nil {
			return migrationPlanProofDTO{}, err
		}
		dto.ChangedRefs[index] = ref.String()
	}
	if err := ctx.Err(); err != nil {
		return migrationPlanProofDTO{}, err
	}
	return dto, nil
}

func migrationPlanProofFromDTO(
	dto *migrationPlanProofDTO,
) (mutation.MigrationPlanProof, *mcp.CallToolResult) {
	if dto == nil {
		return mutation.MigrationPlanProof{}, toolError("proof is required")
	}
	if dto.FormatVersion != mutation.MigrationPlanProofFormatVersion {
		return mutation.MigrationPlanProof{}, stableToolError(
			"plan_mismatch", "proof.format_version is unsupported", false,
		)
	}
	if err := mutation.ValidatePlanDigest(dto.ResolutionDigest); err != nil {
		return mutation.MigrationPlanProof{}, stableToolError(
			"plan_mismatch", "proof.resolution_digest is not canonical", false,
		)
	}
	base, err := store.ParseRevision(dto.BaseRevision)
	if err != nil {
		return mutation.MigrationPlanProof{}, stableToolError(
			"invalid_revision", "proof.base_revision is not a canonical revision", false,
		)
	}
	result, err := store.ParseRevision(dto.ResultRevision)
	if err != nil {
		return mutation.MigrationPlanProof{}, stableToolError(
			"invalid_revision", "proof.result_revision is not a canonical revision", false,
		)
	}
	proof := mutation.MigrationPlanProof{
		FormatVersion: dto.FormatVersion, RequestDigest: dto.RequestDigest,
		ResolutionDigest: dto.ResolutionDigest,
		BaseRevision:     base, ResultRevision: result,
		Reads: make([]store.Read, len(dto.Reads)), Writes: make([]mutation.MigrationPlanWrite, len(dto.Writes)),
		Deletes: append([]string{}, dto.Deletes...), Renames: make([]store.Rename, len(dto.Renames)),
		AffectedRefs:  make([]bundle.RelationRef, len(dto.AffectedRefs)),
		ReverseImpact: make([]bundle.RelationRef, len(dto.ReverseImpact)),
		ChangedFiles:  make([]store.FileChange, len(dto.ChangedFiles)),
		ChangedRefs:   make([]bundle.RelationRef, len(dto.ChangedRefs)),
	}
	for index, path := range dto.Reads {
		proof.Reads[index] = store.Read{Path: path}
	}
	for index, write := range dto.Writes {
		proof.Writes[index] = mutation.MigrationPlanWrite{Path: write.Path, Digest: write.Digest}
	}
	for index, rename := range dto.Renames {
		proof.Renames[index] = store.Rename{From: rename.From, To: rename.To}
	}
	for index, raw := range dto.AffectedRefs {
		ref, parseErr := bundle.ParseRelationRef(raw)
		if parseErr != nil {
			return mutation.MigrationPlanProof{}, toolErrorf("proof.affected_refs[%d]: %v", index, parseErr)
		}
		proof.AffectedRefs[index] = ref
	}
	for index, raw := range dto.ReverseImpact {
		ref, parseErr := bundle.ParseRelationRef(raw)
		if parseErr != nil {
			return mutation.MigrationPlanProof{}, toolErrorf("proof.reverse_impact[%d]: %v", index, parseErr)
		}
		proof.ReverseImpact[index] = ref
	}
	for index, change := range dto.ChangedFiles {
		proof.ChangedFiles[index] = store.FileChange{
			Kind: store.FileChangeKind(change.Kind), Path: change.Path, From: change.From,
		}
	}
	for index, raw := range dto.ChangedRefs {
		ref, parseErr := bundle.ParseRelationRef(raw)
		if parseErr != nil {
			return mutation.MigrationPlanProof{}, toolErrorf("proof.changed_refs[%d]: %v", index, parseErr)
		}
		proof.ChangedRefs[index] = ref
	}
	return proof, nil
}

func decodeMigrationArguments(request mcp.CallToolRequest) (migrationArguments, *mcp.CallToolResult) {
	data, err := json.Marshal(request.GetArguments())
	if err != nil {
		return migrationArguments{}, toolErrorf("decode migration arguments: %v", err)
	}
	if len(data) > maxConceptReadBytes {
		return migrationArguments{}, stableToolError("resource_limit", "migration input exceeds the MCP limit", false)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var arguments migrationArguments
	if err := decoder.Decode(&arguments); err != nil {
		return migrationArguments{}, toolErrorf("decode migration arguments: %v", err)
	}
	if arguments.From == "" {
		arguments.From = mutation.MigrationSelectorAuto
	}
	if len(arguments.GeneratedBy) > 256 {
		return migrationArguments{}, stableToolError("resource_limit", "generated_by exceeds the MCP transport limit", false)
	}
	if arguments.GeneratedBy != "" && !bundle.ValidActor(arguments.GeneratedBy) {
		return migrationArguments{}, toolError("generated_by is not a valid document actor")
	}
	if arguments.TimestampConflictPolicy == "" {
		arguments.TimestampConflictPolicy = string(store.TimestampConflictReject)
	}
	if len(arguments.CitationMappings) > 10_000 || len(arguments.GeneratedAt) > 10_000 || len(arguments.Computations) > 256 {
		return migrationArguments{}, stableToolError("resource_limit", "migration item limit exceeded", false)
	}
	citationEntries := 0
	for _, document := range arguments.CitationMappings {
		citationEntries += len(document.Entries)
		if citationEntries > 10_000 {
			return migrationArguments{}, stableToolError("resource_limit", "migration citation item limit exceeded", false)
		}
	}
	return arguments, nil
}

func migrationInputPresentationPreview(
	ctx context.Context,
	root string,
	arguments migrationArguments,
	validationErr error,
) (*mcp.CallToolResult, bool) {
	presentation, matched := normalizedCitationCollision(validationErr)
	if !matched {
		return nil, false
	}
	source, revision, err := captureMigrationSource(ctx, root)
	if err != nil {
		return migrationSourceError("capture migration source", err), true
	}
	resolution, resolutionErr := mutation.NewMigrationPlanner().ResolveSource(
		ctx,
		source,
		mutation.MigrationSourceOptions{RequestedSelector: arguments.From},
	)
	if resolutionErr != nil && resolution.Transition != mutation.MigrationTransitionBlocked {
		return bundleDomainError("resolve migration source", "migration source exceeds MCP resource limits", resolutionErr), true
	}
	response, projectionErr := migrationResolutionPreviewContext(ctx, revision, resolution, resolutionErr)
	if projectionErr != nil {
		return bundleDomainError("project migration source", "migration preview exceeds MCP output limits", projectionErr), true
	}
	response.Status = "blocked"
	response.ResultRevision = nil
	response.Proof = nil
	response.PlanDigest = ""
	response.AffectedPaths = []string{}
	response.Diff = ""
	response.DiffTruncated = false
	response.Blockers = append(response.Blockers, migrationBlockerDTO{
		Code: stableDiagnosticCode(presentation.Code),
		Path: filepathSafe(presentation.Path),
		Location: sourceSpanDTO{
			Start: presentation.Location.Start,
			End:   presentation.Location.End,
		},
		Message: redactSensitivePaths(presentation.Error()),
	})
	response.ManualActions = append(response.ManualActions, migrationManualActionDTO{
		Code:    "disambiguate_citation_entry",
		Path:    filepathSafe(presentation.Path),
		Message: "make each legacy citation selector identify exactly one entry before migration",
	})
	return jsonStructuredResultContext(ctx, "project migration input presentation", response, response), true
}

func migrationInputPreflightPreviewContext(
	ctx context.Context,
	revision store.Revision,
	resolution mutation.MigrationSourceResolution,
	preflight mutation.MigrationInputPreflight,
	preflightErr error,
) (migrationPreviewResponse, error) {
	response, err := migrationResolutionPreviewContext(ctx, revision, resolution, nil)
	if err != nil {
		return migrationPreviewResponse{}, err
	}
	response.Status = "blocked"
	response.ResultRevision = nil
	response.Proof = nil
	response.PlanDigest = ""
	response.AffectedPaths = []string{}
	response.Diff = ""
	response.DiffTruncated = false
	response.Blockers = make([]migrationBlockerDTO, 0, len(preflight.Blockers))
	response.ManualActions = make([]migrationManualActionDTO, 0, len(preflight.ManualActions))
	for _, blocker := range preflight.Blockers {
		if err := ctx.Err(); err != nil {
			return migrationPreviewResponse{}, err
		}
		response.Blockers = append(response.Blockers, migrationBlockerDTO{
			Code: stableDiagnosticCode(blocker.Code),
			Path: filepathSafe(blocker.Path),
			Location: sourceSpanDTO{
				Start: blocker.Location.Start,
				End:   blocker.Location.End,
			},
			Message: redactSensitivePaths(blocker.Message),
		})
	}
	for _, action := range preflight.ManualActions {
		if err := ctx.Err(); err != nil {
			return migrationPreviewResponse{}, err
		}
		response.ManualActions = append(response.ManualActions, migrationManualActionDTO{
			Code:    action.Code,
			Path:    filepathSafe(action.Path),
			Message: redactSensitivePaths(action.Message),
		})
	}
	if preflightErr != nil && len(response.Blockers) == 0 {
		response.Blockers = append(response.Blockers, migrationBlockerDTO{
			Code:    "migration_blocked",
			Message: redactSensitivePaths(preflightErr.Error()),
		})
	}
	if err := ctx.Err(); err != nil {
		return migrationPreviewResponse{}, err
	}
	return response, nil
}

func migrationInputPreflightError(
	preflight mutation.MigrationInputPreflight,
	preflightErr error,
) *mcp.CallToolResult {
	if len(preflight.Blockers) != 0 {
		blocker := preflight.Blockers[0]
		return stableToolError(stableDiagnosticCode(blocker.Code), redactSensitivePaths(blocker.Message), false)
	}
	if preflightErr != nil {
		return toolErrorf("preflight migration input: %v", preflightErr)
	}
	return stableToolError("migration_blocked", "migration input preflight requires manual action", false)
}

func migrationInputValidationError(err error) *mcp.CallToolResult {
	if presentation, matched := normalizedCitationCollision(err); matched {
		return stableToolError(
			presentation.Code,
			"migration input is blocked; disambiguate the colliding citation entries",
			false,
		)
	}
	return toolErrorf("migration input: %v", err)
}

func normalizedCitationCollision(err error) (*mutation.PresentationError, bool) {
	var presentation *mutation.PresentationError
	if !errors.Is(err, mutation.ErrAmbiguousPresentation) ||
		!errors.As(err, &presentation) ||
		presentation.Code != "normalized_footnote_label_collision" {
		return nil, false
	}
	return presentation, true
}

func canonicalMigrationInput(
	arguments migrationArguments,
) (store.V01ToV02Migration, error, *mcp.CallToolResult) {
	input, result := buildMigrationInput(arguments)
	if result != nil {
		return store.V01ToV02Migration{}, nil, result
	}
	if err := mutation.ValidateV01ToV02MigrationInput(input); err != nil {
		return store.V01ToV02Migration{}, err, nil
	}
	operation, err := store.NewMigrateV01ToV02(input)
	if err != nil {
		return store.V01ToV02Migration{}, err, nil
	}
	return operation.Migration(), nil, nil
}

func buildMigrationInput(
	arguments migrationArguments,
) (store.V01ToV02Migration, *mcp.CallToolResult) {
	if result := validateMCPMigrationMappingMetadata(arguments); result != nil {
		return store.V01ToV02Migration{}, result
	}
	migrationRequest := store.V01ToV02Migration{
		GeneratedBy:       arguments.GeneratedBy,
		TimestampPolicy:   store.LegacyTimestampPolicy(arguments.TimestampPolicy),
		TimestampConflict: store.TimestampConflictPolicy(arguments.TimestampConflictPolicy),
		GeneratedAt:       make([]store.MigrationGeneratedAt, 0, len(arguments.GeneratedAt)),
		Computations:      make([]store.ComputationMigration, 0, len(arguments.Computations)),
	}
	for index, generated := range arguments.GeneratedAt {
		id, err := parseCanonicalConceptID(generated.ConceptID)
		if err != nil {
			return store.V01ToV02Migration{}, toolErrorf("generated_at[%d].concept_id: %v", index, err)
		}
		migrationRequest.GeneratedAt = append(migrationRequest.GeneratedAt, store.MigrationGeneratedAt{
			Path: id.String() + ".md", At: generated.At,
		})
	}
	citations := append([]migrationCitationInput{}, arguments.CitationMappings...)
	for _, document := range citations {
		entries := append([]migrationCitationEntryInput{}, document.Entries...)
		mapped := make([]store.LegacyCitationMapping, 0, len(entries))
		for _, entry := range entries {
			mapped = append(mapped, store.LegacyCitationMapping{
				LegacyNumber: entry.LegacyNumber,
				LegacyEntry:  entry.LegacyEntry,
				SourceID:     entry.SourceID,
				Title:        entry.Title,
				Resource:     entry.Resource,
			})
		}
		migrationRequest.Citations = append(migrationRequest.Citations, store.DocumentCitationMigration{
			Path: document.Path, Entries: mapped,
		})
	}
	for index, computation := range arguments.Computations {
		id, err := parseCanonicalConceptID(computation.ConceptID)
		if err != nil {
			return store.V01ToV02Migration{}, toolErrorf("computations[%d].concept_id: %v", index, err)
		}
		item := store.ComputationMigration{
			Path: id.String() + ".md", Contract: storeComputation(computation.Contract),
		}
		if computation.Asset != nil {
			item.Asset = &store.MigrationAsset{
				Path: computation.Asset.Path, Content: []byte(computation.Asset.Content),
			}
		}
		migrationRequest.Computations = append(migrationRequest.Computations, item)
	}
	return migrationRequest, nil
}

func validateMCPMigrationMappingMetadata(
	arguments migrationArguments,
) *mcp.CallToolResult {
	check := func(path, kind, value string) *mcp.CallToolResult {
		if len(value) > 4096 {
			return stableToolError(
				"resource_limit",
				path+" exceeds the MCP transport limit",
				false,
			)
		}
		if err := validateMCPPatchText(kind, value); err != nil {
			return toolErrorf("%s: %v", path, err)
		}
		return nil
	}
	for documentIndex, document := range arguments.CitationMappings {
		for entryIndex, entry := range document.Entries {
			prefix := fmt.Sprintf(
				"citation_mappings[%d].entries[%d]",
				documentIndex,
				entryIndex,
			)
			fields := [...]mcpTextField{
				{kind: "migration source id", value: entry.SourceID},
				{kind: "migration source title", value: entry.Title},
				{kind: "migration source resource", value: entry.Resource},
			}
			names := [...]string{"source_id", "title", "resource"}
			for index, field := range fields {
				if result := check(
					prefix+"."+names[index],
					field.kind,
					field.value,
				); result != nil {
					return result
				}
			}
		}
	}
	for index, computation := range arguments.Computations {
		prefix := fmt.Sprintf("computations[%d]", index)
		if err := validateMCPComputationMetadata(computation.Contract); err != nil {
			return toolErrorf("%s.contract: %v", prefix, err)
		}
		for _, field := range mcpComputationMetadataFields(computation.Contract) {
			if result := check(
				prefix+".contract",
				field.kind,
				field.value,
			); result != nil {
				return result
			}
		}
	}
	return nil
}

func migrationRequestFromInput(
	resolution mutation.MigrationSourceResolution,
	input store.V01ToV02Migration,
) (mutation.MigrationRequest, error) {
	if err := validateApplicableMigrationSourceTuple(resolution); err != nil {
		return mutation.MigrationRequest{}, fmt.Errorf(
			"mcpserver: invalid migration source tuple: %w",
			err,
		)
	}
	id, err := migrationChangeSetID(input, resolution)
	if err != nil {
		return mutation.MigrationRequest{}, err
	}
	return mutation.MigrationRequest{
		ID: id, Actor: migrationTransactionActor,
		FromVersion: resolution.FromVersion, ToVersion: resolution.ToVersion,
		Migration: input,
	}, nil
}

func migrationChangeSetID(
	input store.V01ToV02Migration,
	resolution mutation.MigrationSourceResolution,
) (store.ChangeSetID, error) {
	operation, err := store.NewMigrateV01ToV02(input)
	if err != nil {
		return "", err
	}
	revision, err := store.ParseRevision("sha256:" + strings.Repeat("0", 64))
	if err != nil {
		return "", err
	}
	sentinel := store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           "mcp-migration-identity-sentinel-v1",
		Actor:        migrationTransactionActor,
		BaseRevision: revision,
		Operations:   []store.Operation{operation},
	}
	requestDigest, err := sentinel.RequestDigest()
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(struct {
		Domain        string             `json:"domain"`
		RequestDigest string             `json:"request_digest"`
		Resolution    migrationSourceDTO `json:"resolution"`
	}{
		Domain: "okf-mcp:v01-to-v02-migration-identity:v3", RequestDigest: requestDigest,
		Resolution: migrationSourceDTOFromDomain(resolution),
	})
	sum := sha256.Sum256(data)
	return store.ChangeSetID("mcp-migration-" + hex.EncodeToString(sum[:])), nil
}

func migrationPreviewProjection(
	ctx context.Context,
	source bundle.Source,
	revision store.Revision,
	resolution mutation.MigrationSourceResolution,
	preview mutation.MigrationPreview,
	previewErr error,
) (migrationPreviewResponse, error) {
	if ctx == nil {
		return migrationPreviewResponse{}, errNilRequestContext
	}
	status := "applicable"
	var resultRevision *string
	if previewErr != nil {
		status = "blocked"
	} else {
		value := string(preview.Preview.ResultRevision)
		resultRevision = &value
		if preview.Preview.BaseRevision == preview.Preview.ResultRevision ||
			len(preview.Preview.Writes)+len(preview.Preview.Deletes)+len(preview.Preview.Renames) == 0 {
			status = "noop"
		}
	}
	base := preview.Preview.BaseRevision
	if base.IsZero() {
		base = revision
	}
	diff, err := boundedPreviewDiff(ctx, source, preview.Preview)
	if err != nil {
		return migrationPreviewResponse{}, err
	}
	paths, err := previewPathsContext(ctx, preview.Preview)
	if err != nil {
		return migrationPreviewResponse{}, err
	}
	diagnostics, err := wireDiagnosticsFromStoreContext(ctx, preview.Preview.Diagnostics)
	if err != nil {
		return migrationPreviewResponse{}, err
	}
	response := migrationPreviewResponse{
		Status: status, Source: migrationSourceDTOFromDomain(resolution),
		BaseRevision: string(base), ResultRevision: resultRevision,
		PlanDigest: preview.PlanDigest, AffectedPaths: paths,
		Diff: diff, DiffTruncated: false,
		Blockers:      make([]migrationBlockerDTO, 0, len(preview.Blockers)),
		ManualActions: make([]migrationManualActionDTO, 0, len(preview.ManualActions)),
		Diagnostics:   diagnostics,
	}
	if previewErr == nil && preview.Proof.FormatVersion == mutation.MigrationPlanProofFormatVersion {
		proof, err := migrationPlanProofDTOFromDomainContext(ctx, preview.Proof)
		if err != nil {
			return migrationPreviewResponse{}, err
		}
		response.Proof = &proof
	}
	for _, blocker := range preview.Blockers {
		if err := ctx.Err(); err != nil {
			return migrationPreviewResponse{}, err
		}
		response.Blockers = append(response.Blockers, migrationBlockerDTO{
			Code: stableDiagnosticCode(blocker.Code), Path: filepathSafe(blocker.Path),
			Location: sourceSpanDTO{Start: blocker.Location.Start, End: blocker.Location.End},
			Message:  redactSensitivePaths(blocker.Message),
		})
	}
	for _, action := range preview.ManualActions {
		if err := ctx.Err(); err != nil {
			return migrationPreviewResponse{}, err
		}
		response.ManualActions = append(response.ManualActions, migrationManualActionDTO{
			Code: action.Code, Path: filepathSafe(action.Path), Message: redactSensitivePaths(action.Message),
		})
	}
	if previewErr != nil && len(response.Blockers) == 0 {
		response.Blockers = append(response.Blockers, migrationBlockerDTO{
			Code: "migration_blocked", Message: redactSensitivePaths(previewErr.Error()),
		})
	}
	if err := ctx.Err(); err != nil {
		return migrationPreviewResponse{}, err
	}
	return response, nil
}

func migrationResolutionPreviewContext(
	ctx context.Context,
	revision store.Revision,
	resolution mutation.MigrationSourceResolution,
	resolutionErr error,
) (migrationPreviewResponse, error) {
	if ctx == nil {
		return migrationPreviewResponse{}, errNilRequestContext
	}
	if err := ctx.Err(); err != nil {
		return migrationPreviewResponse{}, err
	}
	status := "noop"
	result := string(revision)
	var resultRevision *string = &result
	if resolution.Transition == mutation.MigrationTransitionBlocked {
		status = "blocked"
		resultRevision = nil
	}
	response := migrationPreviewResponse{
		Status: status, Source: migrationSourceDTOFromDomain(resolution),
		BaseRevision: string(revision), ResultRevision: resultRevision,
		PlanDigest: "", AffectedPaths: []string{}, Diff: "", DiffTruncated: false,
		Blockers:      make([]migrationBlockerDTO, 0, len(resolution.Blockers)),
		ManualActions: []migrationManualActionDTO{}, Diagnostics: []wireDiagnostic{},
	}
	for _, blocker := range resolution.Blockers {
		if err := ctx.Err(); err != nil {
			return migrationPreviewResponse{}, err
		}
		response.Blockers = append(response.Blockers, migrationBlockerDTO{
			Code: stableDiagnosticCode(blocker.Code), Path: filepathSafe(blocker.Path),
			Location: sourceSpanDTO{Start: blocker.Location.Start, End: blocker.Location.End},
			Message:  redactSensitivePaths(blocker.Message),
		})
	}
	if resolutionErr != nil && len(response.Blockers) == 0 {
		response.Blockers = append(response.Blockers, migrationBlockerDTO{
			Code:    "unsupported_migration_source",
			Message: redactSensitivePaths(resolutionErr.Error()),
		})
	}
	if err := ctx.Err(); err != nil {
		return migrationPreviewResponse{}, err
	}
	return response, nil
}

type capturedMigrationSource struct {
	paths []string
	files map[string][]byte
}

func (s *capturedMigrationSource) Paths(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]string(nil), s.paths...), nil
}

func (s *capturedMigrationSource) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := s.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func captureMigrationSource(ctx context.Context, root string) (*capturedMigrationSource, store.Revision, error) {
	observeMigrationIO(ctx, func(observer migrationIOObserver) func() {
		return observer.sourceOpen
	})
	filesystem := &bundle.FileSystemSource{Root: root}
	return captureOwnedMigrationSourceContext(ctx, filesystem)
}

func captureOwnedMigrationSourceContext(
	ctx context.Context,
	source ownedBundleSource,
) (captured *capturedMigrationSource, revision store.Revision, err error) {
	if ctx == nil {
		return nil, "", errNilRequestContext
	}
	if source == nil {
		return nil, "", errNilOwnedBundleSource
	}
	defer func() {
		closeErr := source.Close()
		if closeErr != nil {
			captured = nil
			revision = ""
		}
		err = errors.Join(err, closeErr)
	}()
	bounded := &boundedBundleSource{source: source}
	paths, err := bounded.Paths(ctx)
	if err != nil {
		return nil, "", err
	}
	captured = &capturedMigrationSource{
		paths: append([]string(nil), paths...),
		files: make(map[string][]byte, len(paths)),
	}
	manifest := make([]store.ManifestEntry, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		observeMigrationIO(ctx, func(observer migrationIOObserver) func() {
			return observer.sourceRead
		})
		data, readErr := bounded.ReadFile(ctx, path)
		if readErr != nil {
			return nil, "", readErr
		}
		captured.files[path] = append([]byte(nil), data...)
		manifest = append(manifest, store.ManifestEntry{Path: path, Content: data})
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	revision, err = store.RevisionFromManifest(manifest)
	if err != nil {
		return nil, "", err
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return captured, revision, nil
}

func migrationSourceError(operation string, err error) *mcp.CallToolResult {
	return bundleDomainError(operation, "migration source exceeds MCP resource limits", err)
}

func migrationSourceDTOFromDomain(resolution mutation.MigrationSourceResolution) migrationSourceDTO {
	var declared *string
	if resolution.DeclaredVersion != "" {
		value := resolution.DeclaredVersion
		declared = &value
	}
	dto := migrationSourceDTO{
		RequestedSelector:  resolution.RequestedSelector,
		DeclarationPresent: resolution.DeclarationPresent,
		DeclarationValid:   resolution.DeclarationValid,
		DeclarationRaw:     resolution.DeclarationRaw,
		DeclaredVersion:    declared,
		ResolvedSource:     resolution.ResolvedSource,
		ResolutionSource:   string(resolution.ResolutionSource),
		FromVersion:        resolution.FromVersion,
		ToVersion:          resolution.ToVersion,
		Transition:         string(resolution.Transition),
		Candidates:         make([]migrationLegacyCandidateDTO, len(resolution.Candidates)),
		Blockers:           make([]migrationBlockerDTO, len(resolution.Blockers)),
	}
	for index, candidate := range resolution.Candidates {
		dto.Candidates[index] = migrationLegacyCandidateDTO{
			Kind: candidate.Kind, Path: filepathSafe(candidate.Path),
			Location: sourceSpanDTO{
				Start: candidate.Location.Start,
				End:   candidate.Location.End,
			},
		}
	}
	for index, blocker := range resolution.Blockers {
		dto.Blockers[index] = migrationBlockerDTO{
			Code: stableDiagnosticCode(blocker.Code), Path: filepathSafe(blocker.Path),
			Location: sourceSpanDTO{
				Start: blocker.Location.Start,
				End:   blocker.Location.End,
			},
			Message: redactSensitivePaths(blocker.Message),
		}
	}
	return dto
}
