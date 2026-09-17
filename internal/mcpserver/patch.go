package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
	"github.com/skosovsky/okf/validator"
)

const maxPatchDiffBytes = 1 << 20
const maxChangedPaths = 10_000

var (
	errMCPPreviewDiffLimit = errors.New("MCP preview diff limit exceeded")
	errNilPreviewSource    = errors.New("nil preview source")
)

type patchOperationInput struct {
	Kind                string                         `json:"kind"`
	ConceptID           string                         `json:"concept_id,omitempty"`
	Generated           *patchGenerationInput          `json:"generated,omitempty"`
	Verification        *patchVerificationInput        `json:"verification,omitempty"`
	Source              *patchSourceInput              `json:"source,omitempty"`
	SourceID            *string                        `json:"source_id,omitempty"`
	UsageWindow         *patchUsageWindowInput         `json:"usage_window,omitempty"`
	Lifecycle           *patchLifecycleInput           `json:"lifecycle,omitempty"`
	AttestedComputation *patchAttestedComputationInput `json:"attested_computation,omitempty"`
	Version             string                         `json:"version,omitempty"`
}

type patchCommitter interface {
	Commit(context.Context, store.ChangeSet, store.CommitOptions) (store.CommitReceipt, error)
}

type patchGenerationInput struct {
	By string `json:"by"`
	At string `json:"at,omitempty"`
}

type patchVerificationInput struct {
	By string `json:"by"`
	At string `json:"at"`
}

type patchUsageWindowInput struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type patchSourceInput struct {
	ID           string                 `json:"id,omitempty"`
	Resource     string                 `json:"resource"`
	Title        string                 `json:"title,omitempty"`
	Author       string                 `json:"author,omitempty"`
	UsageCount   *string                `json:"usage_count,omitempty"`
	LastModified string                 `json:"last_modified,omitempty"`
	UsageWindow  *patchUsageWindowInput `json:"usage_window,omitempty"`
}

type patchLifecycleInput struct {
	Status     *string `json:"status"`
	StaleAfter *string `json:"stale_after"`
}

type patchParameterInput struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type patchExecutorInput struct {
	Resource string   `json:"resource"`
	Receipt  []string `json:"receipt"`
}

type patchAttesterInput struct {
	Resource string `json:"resource"`
}

type patchAttestedComputationInput struct {
	Mode              store.AttestedComputationMode `json:"mode"`
	Runtime           string                        `json:"runtime"`
	Parameters        []patchParameterInput         `json:"parameters"`
	ComputationPath   string                        `json:"computation_path,omitempty"`
	InlineComputation *string                       `json:"inline_computation,omitempty"`
	InlineLanguage    string                        `json:"inline_language,omitempty"`
	Executor          patchExecutorInput            `json:"executor"`
	Attester          patchAttesterInput            `json:"attester"`
}

type conceptPatchArguments struct {
	BundlePath         string                `json:"bundle_path"`
	Actor              string                `json:"actor"`
	Operations         []patchOperationInput `json:"operations"`
	ExpectedRevision   string                `json:"expected_revision,omitempty"`
	ExpectedPlanDigest string                `json:"expected_plan_digest,omitempty"`
}

type patchPreviewResponse struct {
	Status           string           `json:"status"`
	BaseRevision     string           `json:"base_revision"`
	ResultRevision   *string          `json:"result_revision"`
	PlanDigest       string           `json:"plan_digest"`
	Diff             string           `json:"diff"`
	DiffTruncated    bool             `json:"diff_truncated"`
	AffectedPaths    []string         `json:"affected_paths"`
	Diagnostics      []wireDiagnostic `json:"diagnostics"`
	UnknownPreserved bool             `json:"unknown_preserved"`
}

type patchApplyResponse struct {
	Status         string           `json:"status"`
	BaseRevision   string           `json:"base_revision"`
	ResultRevision string           `json:"result_revision"`
	PlanDigest     string           `json:"plan_digest"`
	ChangedPaths   []string         `json:"changed_paths"`
	Diagnostics    []wireDiagnostic `json:"diagnostics"`
}

func handlePreviewConceptPatch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "preview_concept_patch", request); result != nil {
		return result, nil
	}
	root, result := requireValidatedBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	arguments, result := decodePatchArguments(request)
	if result != nil {
		return result, nil
	}
	if result := requestContextCheckpoint(ctx, "decode patch arguments"); result != nil {
		return result, nil
	}
	operations, result := buildPatchOperations(arguments.Operations)
	if result != nil {
		return result, nil
	}
	if result := requestContextCheckpoint(ctx, "build patch operations"); result != nil {
		return result, nil
	}
	source, revision, err := captureMigrationSource(ctx, root)
	if err != nil {
		return migrationSourceError("capture patch source", err), nil
	}
	change, err := newPatchChangeSet(arguments.Actor, revision, operations)
	if err != nil {
		return bundleDomainError("identify concept patch", "patch exceeds MCP resource limits", err), nil
	}
	planned, err := mutation.NewPlanner(&validator.ValidatorConfig{
		Strict: true, Spec: bundle.OKFVersion,
	}).Plan(ctx, source, change)
	preview := planned.Preview
	if err != nil {
		requestDigest, digestErr := change.RequestDigest()
		if digestErr != nil {
			return bundleDomainError("digest concept patch", "patch exceeds MCP resource limits", digestErr), nil
		}
		digest := requestDigest
		var invalid *store.InvalidChangeSet
		if !errors.As(err, &invalid) {
			return bundleDomainError("preview concept patch", "patch preview exceeds MCP resource limits", err), nil
		}
		response, projectionErr := rejectedPatchPreviewContext(
			ctx,
			revision,
			digest,
			preview.Diagnostics,
			invalid.Diagnostics,
		)
		if projectionErr != nil {
			return bundleDomainError("project rejected patch preview", "patch preview exceeds MCP resource limits", projectionErr), nil
		}
		response.Diagnostics, projectionErr = ensurePatchRejectionErrorContext(ctx, response.Diagnostics, invalid)
		if projectionErr != nil {
			return bundleDomainError("project rejected patch preview", "patch preview exceeds MCP resource limits", projectionErr), nil
		}
		return jsonStructuredResultContext(ctx, "project rejected patch preview", response, response), nil
	}
	paths, err := previewPathsContext(ctx, preview)
	if err != nil {
		return bundleDomainError("project patch paths", "patch preview exceeds MCP output limits", err), nil
	}
	if len(paths) > maxChangedPaths {
		return stableToolError("resource_limit", "patch affects too many paths", false), nil
	}
	digest, err := mutation.PlanDigest(change, preview)
	if err != nil {
		return bundleDomainError("digest concept patch plan", "patch preview exceeds MCP resource limits", err), nil
	}
	response, err := successfulPatchPreview(ctx, source, digest, preview)
	if err != nil {
		return bundleDomainError("project patch preview", "patch preview exceeds MCP output limits", err), nil
	}
	return jsonStructuredResultContext(ctx, "project patch preview", response, response), nil
}

func handleApplyConceptPatch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "apply_concept_patch", request); result != nil {
		return result, nil
	}
	root, result := requireValidatedBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	arguments, result := decodePatchArguments(request)
	if result != nil {
		return result, nil
	}
	if result := requestContextCheckpoint(ctx, "decode patch arguments"); result != nil {
		return result, nil
	}
	expected, err := store.ParseRevision(arguments.ExpectedRevision)
	if err != nil {
		return stableToolError("invalid_revision", "expected_revision is not a canonical revision", false), nil
	}
	operations, result := buildPatchOperations(arguments.Operations)
	if result != nil {
		return result, nil
	}
	if result := requestContextCheckpoint(ctx, "build patch operations"); result != nil {
		return result, nil
	}
	change, err := newPatchChangeSet(arguments.Actor, expected, operations)
	if err != nil {
		return bundleDomainError("identify concept patch", "patch exceeds MCP resource limits", err), nil
	}
	if err := mutation.ValidatePlanDigest(arguments.ExpectedPlanDigest); err != nil {
		return stableToolError("plan_mismatch", "expected_plan_digest is not canonical", false), nil
	}
	idempotencyKey, err := mutation.PlanDigestIdempotencyKey(
		"mcp:concept-patch",
		arguments.ExpectedPlanDigest,
		"",
	)
	if err != nil {
		return stableToolError("plan_mismatch", "expected_plan_digest cannot derive transaction identity", false), nil
	}
	source, revision, err := captureMigrationSource(ctx, root)
	if err != nil {
		return migrationSourceError("capture patch source", err), nil
	}
	if revision == expected {
		planned, planErr := mutation.NewPlanner(&validator.ValidatorConfig{
			Strict: true, Spec: bundle.OKFVersion,
		}).Plan(ctx, source, change)
		if planErr != nil {
			var invalid *store.InvalidChangeSet
			if errors.As(planErr, &invalid) {
				response, projectionErr := rejectedPatchApplyContext(
					ctx,
					revision,
					arguments.ExpectedPlanDigest,
					planned.Preview.Diagnostics,
					invalid.Diagnostics,
				)
				if projectionErr != nil {
					return bundleDomainError("project rejected patch apply", "patch apply exceeds MCP resource limits", projectionErr), nil
				}
				response.Diagnostics, projectionErr = ensurePatchRejectionErrorContext(ctx, response.Diagnostics, invalid)
				if projectionErr != nil {
					return bundleDomainError("project rejected patch apply", "patch apply exceeds MCP resource limits", projectionErr), nil
				}
				return jsonStructuredResultContext(ctx, "project rejected patch apply", response, response), nil
			}
			return bundleDomainError("rebuild concept patch preview", "patch preview exceeds MCP resource limits", planErr), nil
		}
		preview := planned.Preview
		paths, pathsErr := previewPathsContext(ctx, preview)
		if pathsErr != nil {
			return bundleDomainError("project patch paths", "patch preview exceeds MCP output limits", pathsErr), nil
		}
		if len(paths) > maxChangedPaths {
			return stableToolError("resource_limit", "patch affects too many paths", false), nil
		}
		digest, digestErr := mutation.PlanDigest(change, preview)
		if digestErr != nil {
			return bundleDomainError("digest concept patch plan", "patch preview exceeds MCP resource limits", digestErr), nil
		}
		if digest != arguments.ExpectedPlanDigest {
			return stableToolError("plan_mismatch", "expected_plan_digest does not match the rebuilt plan", false), nil
		}
		if preview.BaseRevision == preview.ResultRevision ||
			len(preview.Writes)+len(preview.Deletes)+len(preview.Renames) == 0 {
			response := patchApplyResponse{
				Status: "noop", BaseRevision: string(revision), ResultRevision: string(revision),
				PlanDigest: arguments.ExpectedPlanDigest, ChangedPaths: []string{}, Diagnostics: []wireDiagnostic{},
			}
			return jsonStructuredResultContext(ctx, "project patch noop", response, response), nil
		}
	} else {
		metadataExists, metadataErr := migrationStoreMetadataExists(root)
		if metadataErr != nil {
			return bundleDomainError("inspect transactional store metadata", "patch metadata exceeds MCP resource limits", metadataErr), nil
		}
		if !metadataExists {
			return stableToolError("revision_conflict", "bundle revision changed after preview", true), nil
		}
	}
	observeMigrationIO(ctx, func(observer migrationIOObserver) func() {
		return observer.storeOpen
	})
	if result := requestContextCheckpoint(ctx, "open patch store"); result != nil {
		return result, nil
	}
	opened, err := storefs.OpenContext(ctx, root, withMCPStoreLimits(storefs.Config{
		ValidatorConfig: &validator.ValidatorConfig{Strict: true, Spec: bundle.OKFVersion},
	}))
	if err != nil {
		return bundleDomainError("open transactional store", "patch store exceeds MCP resource limits", err), nil
	}
	defer opened.Close()

	snapshot, err := opened.Snapshot(ctx)
	if err != nil {
		return bundleDomainError("snapshot concept patch", "patch snapshot exceeds MCP resource limits", err), nil
	}
	if _, err := loadBoundedSource(ctx, snapshot); err != nil {
		return bundleDomainError("bound patch snapshot", "patch snapshot exceeds MCP resource limits", err), nil
	}
	if result := requestContextCheckpoint(ctx, "commit concept patch"); result != nil {
		return result, nil
	}
	return applyPatchCommitWithStore(ctx, opened, change, store.CommitOptions{
		IdempotencyKey: idempotencyKey,
	}, arguments.ExpectedPlanDigest)
}

func applyPatchCommitWithStore(
	ctx context.Context,
	committer patchCommitter,
	change store.ChangeSet,
	options store.CommitOptions,
	planDigest string,
) (*mcp.CallToolResult, error) {
	receipt, err := committer.Commit(ctx, change, options)
	receipt, err = classifyMCPPatchCommitOutcome(change, options, receipt, err)
	if err != nil {
		if errors.Is(err, store.ErrStorageCorrupt) {
			return stableToolError("operation_rejected", "patch commit receipt failed integrity validation", false), nil
		}
		if errors.Is(err, mutation.ErrMigrationPlanMismatch) {
			return stableToolError("plan_mismatch", "committed patch receipt does not match expected_plan_digest", false), nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return stableToolError("operation_cancelled", "apply concept patch was cancelled", true), nil
		}
		var conflict *store.Conflict
		if errors.As(err, &conflict) {
			return stableToolError("revision_conflict", "bundle revision changed after preview", conflict.Retryable), nil
		}
		return toolErrorf("apply concept patch: %v", err), nil
	}
	status := "applied"
	if receipt.BaseRevision == receipt.ResultRevision {
		status = "noop"
	}
	response := patchApplyResponse{
		Status:         status,
		BaseRevision:   string(receipt.BaseRevision),
		ResultRevision: string(receipt.ResultRevision),
		PlanDigest:     planDigest,
		ChangedPaths:   changedReceiptPaths(receipt),
		Diagnostics:    []wireDiagnostic{},
	}
	return jsonStructuredResult(response, response), nil
}

func decodePatchArguments(request mcp.CallToolRequest) (conceptPatchArguments, *mcp.CallToolResult) {
	data, err := json.Marshal(request.GetArguments())
	if err != nil {
		return conceptPatchArguments{}, toolErrorf("decode patch arguments: %v", err)
	}
	if len(data) > maxConceptReadBytes {
		return conceptPatchArguments{}, stableToolError("resource_limit", "patch input exceeds the MCP limit", false)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var arguments conceptPatchArguments
	if err := decoder.Decode(&arguments); err != nil {
		return conceptPatchArguments{}, toolErrorf("decode patch arguments: %v", err)
	}
	if arguments.Actor == "" {
		return conceptPatchArguments{}, toolError("actor is required")
	}
	if len(arguments.Actor) > 256 {
		return conceptPatchArguments{}, stableToolError("resource_limit", "actor exceeds the MCP transport limit", false)
	}
	if len(arguments.Operations) == 0 || len(arguments.Operations) > 256 {
		return conceptPatchArguments{}, stableToolError("resource_limit", "operations must contain 1..256 items", false)
	}
	for _, operation := range arguments.Operations {
		if operation.Generated != nil && len(operation.Generated.By) > 256 ||
			operation.Verification != nil && len(operation.Verification.By) > 256 {
			return conceptPatchArguments{}, stableToolError(
				"resource_limit", "document actor exceeds the MCP transport limit", false,
			)
		}
	}
	return arguments, nil
}

func buildPatchOperations(inputs []patchOperationInput) ([]store.Operation, *mcp.CallToolResult) {
	operations := make([]store.Operation, 0, len(inputs))
	for index, input := range inputs {
		var concept bundle.ConceptID
		var err error
		if input.Kind != "set_bundle_version" {
			concept, err = parseCanonicalConceptID(input.ConceptID)
			if err != nil {
				return nil, toolErrorf("operations[%d].concept_id: %v", index, err)
			}
		}
		var operation store.Operation
		switch input.Kind {
		case "set_generated":
			if input.Generated == nil {
				return nil, toolErrorf("operations[%d].generated is required", index)
			}
			operation, err = store.NewSetGenerated(
				concept,
				store.Generation{By: input.Generated.By, At: input.Generated.At},
			)
		case "ensure_verification", "remove_verification":
			if input.Verification == nil {
				return nil, toolErrorf("operations[%d].verification is required", index)
			}
			verification := store.Verification{By: input.Verification.By, At: input.Verification.At}
			if input.Kind == "ensure_verification" {
				operation, err = store.NewEnsureVerification(concept, verification)
			} else {
				operation, err = store.NewRemoveVerification(concept, verification)
			}
		case "put_source":
			if input.Source == nil {
				return nil, toolErrorf("operations[%d].source is required", index)
			}
			var source store.ProvenanceSource
			if err = validateMCPPatchSource(*input.Source); err == nil {
				source, err = storeSource(*input.Source)
			}
			if err == nil {
				operation, err = store.NewPutSource(concept, source)
			}
		case "remove_source":
			var selector store.SourceSelector
			switch {
			case input.SourceID != nil && input.Source != nil:
				return nil, toolErrorf("operations[%d] source_id and source are mutually exclusive", index)
			case input.SourceID != nil:
				if *input.SourceID == "" {
					return nil, toolErrorf("operations[%d].source_id must not be empty", index)
				}
				if err = validateMCPPatchText("source id", *input.SourceID); err == nil {
					selector, err = store.SourceByID(*input.SourceID)
				}
			case input.Source != nil:
				var source store.ProvenanceSource
				if err = validateMCPPatchSource(*input.Source); err == nil {
					source, err = storeSource(*input.Source)
				}
				if err == nil {
					selector, err = store.SourceByExact(source)
				}
			default:
				return nil, toolErrorf("operations[%d] requires source_id or source", index)
			}
			if err == nil {
				operation, err = store.NewRemoveSource(concept, selector)
			}
		case "set_usage_window":
			var window *store.UsageWindow
			if input.UsageWindow != nil {
				window = &store.UsageWindow{From: input.UsageWindow.From, To: input.UsageWindow.To}
			}
			switch {
			case input.SourceID != nil && input.Source != nil:
				return nil, toolErrorf("operations[%d] source_id and source are mutually exclusive", index)
			case input.SourceID != nil:
				if *input.SourceID == "" {
					return nil, toolErrorf("operations[%d].source_id must not be empty", index)
				}
				if selectorErr := validateMCPPatchText("source id", *input.SourceID); selectorErr != nil {
					return nil, toolErrorf("operations[%d]: %v", index, selectorErr)
				}
				selector, selectorErr := store.SourceByID(*input.SourceID)
				if selectorErr != nil {
					return nil, toolErrorf("operations[%d]: %v", index, selectorErr)
				}
				operation, err = store.NewSetUsageWindow(concept, &selector, window)
			case input.Source != nil:
				if sourceErr := validateMCPPatchSource(*input.Source); sourceErr != nil {
					return nil, toolErrorf("operations[%d]: %v", index, sourceErr)
				}
				source, sourceErr := storeSource(*input.Source)
				if sourceErr != nil {
					return nil, toolErrorf("operations[%d]: %v", index, sourceErr)
				}
				selector, selectorErr := store.SourceByExact(source)
				if selectorErr != nil {
					return nil, toolErrorf("operations[%d]: %v", index, selectorErr)
				}
				operation, err = store.NewSetUsageWindow(concept, &selector, window)
			default:
				operation, err = store.NewSetUsageWindow(concept, nil, window)
			}
		case "set_lifecycle":
			if input.Lifecycle == nil {
				return nil, toolErrorf("operations[%d].lifecycle is required", index)
			}
			operation, err = store.NewSetLifecycle(concept, store.Lifecycle{
				Status: input.Lifecycle.Status, StaleAfter: input.Lifecycle.StaleAfter,
			})
		case "put_attested_computation":
			if input.AttestedComputation == nil {
				return nil, toolErrorf("operations[%d].attested_computation is required", index)
			}
			if err = validateMCPComputationMetadata(*input.AttestedComputation); err == nil {
				operation, err = store.NewPutAttestedComputation(
					concept,
					storeComputation(*input.AttestedComputation),
				)
			}
		case "set_bundle_version":
			operation, err = store.NewSetBundleVersion(input.Version)
		default:
			return nil, toolErrorf("operations[%d].kind is unsupported", index)
		}
		if err != nil {
			return nil, toolErrorf("operations[%d]: %v", index, err)
		}
		operations = append(operations, operation)
	}
	return operations, nil
}

func validateMCPPatchSource(input patchSourceInput) error {
	fields := [...]struct {
		kind  string
		value string
	}{
		{kind: "source id", value: input.ID},
		{kind: "source resource", value: input.Resource},
		{kind: "source title", value: input.Title},
		{kind: "source author", value: input.Author},
	}
	for _, field := range fields {
		if field.value != "" {
			if err := validateMCPPatchText(field.kind, field.value); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMCPPatchText(kind, value string) error {
	if len(value) > 4096 || !utf8.ValidString(value) {
		return fmt.Errorf("%w: invalid %s", store.ErrInvalidChangeSet, kind)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: invalid %s", store.ErrInvalidChangeSet, kind)
		}
	}
	return nil
}

type mcpTextField struct {
	kind  string
	value string
}

func validateMCPComputationMetadata(input patchAttestedComputationInput) error {
	switch input.Mode {
	case store.AttestedComputationModeInline:
		if input.InlineComputation == nil {
			return fmt.Errorf(
				"%w: inline computation mode requires inline_computation",
				store.ErrInvalidChangeSet,
			)
		}
	case store.AttestedComputationModeFile:
		if input.InlineComputation != nil {
			return fmt.Errorf(
				"%w: file computation mode cannot declare inline_computation",
				store.ErrInvalidChangeSet,
			)
		}
	default:
		return fmt.Errorf("%w: invalid attested computation mode", store.ErrInvalidChangeSet)
	}
	for _, field := range mcpComputationMetadataFields(input) {
		if err := validateMCPPatchText(field.kind, field.value); err != nil {
			return err
		}
	}
	return nil
}

func mcpComputationMetadataFields(input patchAttestedComputationInput) []mcpTextField {
	fields := []mcpTextField{
		{kind: "computation runtime", value: input.Runtime},
		{kind: "computation path", value: input.ComputationPath},
		{kind: "inline computation language", value: input.InlineLanguage},
		{kind: "executor resource", value: input.Executor.Resource},
		{kind: "attester resource", value: input.Attester.Resource},
	}
	for index, parameter := range input.Parameters {
		fields = append(fields,
			mcpTextField{
				kind: fmt.Sprintf("parameter[%d] name", index), value: parameter.Name,
			},
			mcpTextField{
				kind: fmt.Sprintf("parameter[%d] type", index), value: parameter.Type,
			},
		)
	}
	for index, receipt := range input.Executor.Receipt {
		fields = append(fields, mcpTextField{
			kind: fmt.Sprintf("executor receipt[%d]", index), value: receipt,
		})
	}
	return fields
}

func storeSource(input patchSourceInput) (store.ProvenanceSource, error) {
	source := store.ProvenanceSource{
		ID: input.ID, Resource: input.Resource, Title: input.Title, Author: input.Author,
		LastModified: input.LastModified,
	}
	if input.UsageCount != nil {
		value, err := strconv.ParseUint(*input.UsageCount, 10, 64)
		if err != nil {
			return store.ProvenanceSource{}, fmt.Errorf("usage_count must be a canonical uint64 decimal string: %w", err)
		}
		if strconv.FormatUint(value, 10) != *input.UsageCount {
			return store.ProvenanceSource{}, errors.New("usage_count must be a canonical uint64 decimal string")
		}
		source.UsageCount = &value
	}
	if input.UsageWindow != nil {
		source.UsageWindow = &store.UsageWindow{From: input.UsageWindow.From, To: input.UsageWindow.To}
	}
	return source, nil
}

func storeComputation(input patchAttestedComputationInput) store.AttestedComputationContract {
	parameters := make([]store.ComputationParameter, 0, len(input.Parameters))
	for _, parameter := range input.Parameters {
		parameters = append(parameters, store.ComputationParameter{
			Name: parameter.Name, Type: parameter.Type, Required: parameter.Required,
		})
	}
	inlineComputation := ""
	if input.InlineComputation != nil {
		inlineComputation = *input.InlineComputation
	}
	return store.AttestedComputationContract{
		Mode: input.Mode, Runtime: input.Runtime, Parameters: parameters,
		ComputationPath: input.ComputationPath, InlineComputation: inlineComputation,
		InlineLanguage: input.InlineLanguage,
		Executor: store.ExecutorContract{
			Resource: input.Executor.Resource, Receipt: append([]string{}, input.Executor.Receipt...),
		},
		Attester: store.AttesterContract{Resource: input.Attester.Resource},
	}
}

func newPatchChangeSet(actor string, revision store.Revision, operations []store.Operation) (store.ChangeSet, error) {
	id, err := patchChangeSetID(actor, operations)
	if err != nil {
		return store.ChangeSet{}, err
	}
	return store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           id,
		Actor:        store.Actor(actor),
		BaseRevision: revision,
		Operations:   operations,
	}, nil
}

func patchChangeSetID(actor string, operations []store.Operation) (store.ChangeSetID, error) {
	revision, err := store.ParseRevision("sha256:" + strings.Repeat("0", 64))
	if err != nil {
		return "", err
	}
	sentinel := store.ChangeSet{
		Version: store.ChangeSetFormatVersion,
		ID:      "mcp-patch-identity-sentinel-v1",
		Actor:   store.Actor(actor),
		// Identity intentionally excludes the live revision. The final
		// ChangeSet rebuild below binds the actual preview base revision.
		BaseRevision: revision,
		Operations:   operations,
	}
	digest, err := sentinel.RequestDigest()
	if err != nil {
		return "", err
	}
	return store.ChangeSetID("mcp-patch-" + strings.TrimPrefix(digest, "sha256:")), nil
}

func rejectedPatchPreviewContext(
	ctx context.Context,
	base store.Revision,
	digest string,
	groups ...[]store.Diagnostic,
) (patchPreviewResponse, error) {
	diagnostics := []wireDiagnostic{}
	for _, group := range groups {
		projected, err := wireDiagnosticsFromStoreContext(ctx, group)
		if err != nil {
			return patchPreviewResponse{}, err
		}
		diagnostics = append(diagnostics, projected...)
	}
	return patchPreviewResponse{
		Status: "rejected", BaseRevision: string(base), ResultRevision: nil,
		PlanDigest: digest, Diff: "", DiffTruncated: false,
		AffectedPaths: []string{}, Diagnostics: diagnostics, UnknownPreserved: false,
	}, ctx.Err()
}

func rejectedPatchApplyContext(
	ctx context.Context,
	base store.Revision,
	digest string,
	groups ...[]store.Diagnostic,
) (patchApplyResponse, error) {
	diagnostics := []wireDiagnostic{}
	for _, group := range groups {
		projected, err := wireDiagnosticsFromStoreContext(ctx, group)
		if err != nil {
			return patchApplyResponse{}, err
		}
		diagnostics = append(diagnostics, projected...)
	}
	return patchApplyResponse{
		Status: "rejected", BaseRevision: string(base), ResultRevision: string(base),
		PlanDigest: digest, ChangedPaths: []string{}, Diagnostics: diagnostics,
	}, ctx.Err()
}

func ensurePatchRejectionErrorContext(
	ctx context.Context,
	diagnostics []wireDiagnostic,
	invalid *store.InvalidChangeSet,
) ([]wireDiagnostic, error) {
	for _, diagnostic := range diagnostics {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if diagnostic.Severity == "ERROR" {
			return diagnostics, nil
		}
	}
	code := "invalid_change_set"
	message := "concept patch was rejected"
	if invalid != nil {
		code = stableDiagnosticCodeOr(invalid.Code, code)
		message = redactSensitivePaths(invalid.Error())
	}
	return append(diagnostics, wireDiagnostic{
		Code: code, Severity: "ERROR", Message: message,
	}), ctx.Err()
}

func successfulPatchPreview(
	ctx context.Context,
	source bundle.Source,
	digest string,
	preview store.Preview,
) (patchPreviewResponse, error) {
	if ctx == nil {
		return patchPreviewResponse{}, errNilRequestContext
	}
	status := "applicable"
	if preview.BaseRevision == preview.ResultRevision ||
		len(preview.Writes)+len(preview.Deletes)+len(preview.Renames) == 0 {
		status = "noop"
	}
	result := string(preview.ResultRevision)
	diff, err := boundedPreviewDiff(ctx, source, preview)
	if err != nil {
		return patchPreviewResponse{}, err
	}
	paths, err := previewPathsContext(ctx, preview)
	if err != nil {
		return patchPreviewResponse{}, err
	}
	diagnostics, err := wireDiagnosticsFromStoreContext(ctx, preview.Diagnostics)
	if err != nil {
		return patchPreviewResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return patchPreviewResponse{}, err
	}
	return patchPreviewResponse{
		Status: status, BaseRevision: string(preview.BaseRevision), ResultRevision: &result,
		PlanDigest: digest, Diff: diff, DiffTruncated: false,
		AffectedPaths: paths, Diagnostics: diagnostics,
		UnknownPreserved: true,
	}, nil
}

func wireDiagnosticsFromStoreContext(
	ctx context.Context,
	diagnostics []store.Diagnostic,
) ([]wireDiagnostic, error) {
	if ctx == nil {
		return nil, errNilRequestContext
	}
	out := make([]wireDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, wireDiagnostic{
			Code: stableDiagnosticCode(diagnostic.Code), Severity: wireStoreSeverity(diagnostic.Severity),
			File: filepathSafe(diagnostic.File), Field: "", Message: redactSensitivePaths(diagnostic.Message), SpecRef: "",
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func wireStoreSeverity(severity store.DiagnosticSeverity) string {
	switch severity {
	case store.DiagnosticError:
		return "ERROR"
	case store.DiagnosticWarning:
		return "WARN"
	default:
		return "INFO"
	}
}

func withMCPStoreLimits(config storefs.Config) storefs.Config {
	config.MaxStagedPayloadBytes = maxConceptReadBytes
	config.MaxStagedTransactionBytes = maxBundleTotalBytes
	config.MaxStagedFiles = maxConceptItems
	return config
}

func filepathSafe(value string) string {
	if value == "" || containsSensitivePath(value) || bundle.ValidateRevisionPath(value) != nil {
		return ""
	}
	return value
}

func previewPathsContext(ctx context.Context, preview store.Preview) ([]string, error) {
	if ctx == nil {
		return nil, errNilRequestContext
	}
	seen := make(map[string]struct{})
	for _, write := range preview.Writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seen[write.Path] = struct{}{}
	}
	for _, path := range preview.Deletes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seen[path] = struct{}{}
	}
	for _, rename := range preview.Renames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seen[rename.From] = struct{}{}
		seen[rename.To] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	if err := sortStringsContext(ctx, out); err != nil {
		return nil, err
	}
	return out, ctx.Err()
}

func changedReceiptPaths(receipt store.CommitReceipt) []string {
	seen := make(map[string]struct{})
	for _, file := range receipt.ChangedFiles {
		if file.From != "" {
			seen[file.From] = struct{}{}
		}
		if file.Path != "" {
			seen[file.Path] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func boundedPreviewDiff(ctx context.Context, source bundle.Source, preview store.Preview) (string, error) {
	if ctx == nil {
		return "", errNilRequestContext
	}
	if source == nil {
		return "", errNilPreviewSource
	}
	var builder strings.Builder
	appendBounded := func(value []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(value) > maxPatchDiffBytes-builder.Len() {
			return errMCPPreviewDiffLimit
		}
		_, _ = builder.Write(value)
		return nil
	}
	appendText := func(value string) error { return appendBounded([]byte(value)) }
	appendPrefixedFile := func(prefix byte, content []byte) error {
		for len(content) != 0 {
			if err := appendBounded([]byte{prefix}); err != nil {
				return err
			}
			newline := bytes.IndexByte(content, '\n')
			if newline < 0 {
				if err := appendBounded(content); err != nil {
					return err
				}
				return appendBounded([]byte("\n\\ No newline at end of file\n"))
			}
			if err := appendBounded(content[:newline+1]); err != nil {
				return err
			}
			content = content[newline+1:]
		}
		return ctx.Err()
	}
	writes := append([]store.Write(nil), preview.Writes...)
	if err := sortSliceContext(ctx, writes, func(left, right store.Write) bool {
		return left.Path < right.Path
	}); err != nil {
		return "", err
	}
	for _, write := range writes {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		before, readErr := source.ReadFile(ctx, write.Path)
		beforePath := "a/" + write.Path
		if errors.Is(readErr, fs.ErrNotExist) {
			beforePath = "/dev/null"
			before = nil
		} else if readErr != nil {
			return "", readErr
		}
		if err := appendText(fmt.Sprintf("--- %s\n+++ b/%s\n", beforePath, write.Path)); err != nil {
			return "", err
		}
		if err := appendPrefixedFile('-', before); err != nil {
			return "", err
		}
		if err := appendPrefixedFile('+', write.Content); err != nil {
			return "", err
		}
	}
	deletes := append([]string(nil), preview.Deletes...)
	if err := sortStringsContext(ctx, deletes); err != nil {
		return "", err
	}
	for _, path := range deletes {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		before, readErr := source.ReadFile(ctx, path)
		if readErr != nil {
			return "", readErr
		}
		if err := appendText(fmt.Sprintf("--- a/%s\n+++ /dev/null\n", path)); err != nil {
			return "", err
		}
		if err := appendPrefixedFile('-', before); err != nil {
			return "", err
		}
	}
	renames := append([]store.Rename(nil), preview.Renames...)
	if err := sortSliceContext(ctx, renames, func(left, right store.Rename) bool {
		if left.From != right.From {
			return left.From < right.From
		}
		return left.To < right.To
	}); err != nil {
		return "", err
	}
	for _, rename := range renames {
		if err := appendText(fmt.Sprintf("rename from %s\nrename to %s\n", rename.From, rename.To)); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return builder.String(), nil
}
