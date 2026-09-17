package okfcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

type migrateOptions struct {
	path                 string
	from                 string
	to                   string
	actor                string
	write                bool
	format               string
	citationMappingsFile string
	source               mutation.MigrationSourceResolution
}

func parseMigrateArgs(args []string) (migrateOptions, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--from", Kind: stringFlag},
		{Name: "--to", Kind: stringFlag},
		{Name: "--actor", Kind: stringFlag},
		{Name: "--citation-mappings", Kind: stringFlag},
		{Name: "--write", Kind: boolFlag},
		{Name: "--format", Kind: stringFlag},
	})
	if err != nil {
		return migrateOptions{}, err
	}
	path, err := parsed.onePositional("<bundle>")
	if err != nil {
		return migrateOptions{}, namedArgumentError("migrate", err)
	}
	from := parsed.value("--from", "auto")
	if from != "auto" && from != "0.1" {
		return migrateOptions{}, fmt.Errorf("unsupported migration source %q (want auto or 0.1)", from)
	}
	to := parsed.value("--to", "")
	if to == "" {
		return migrateOptions{}, fmt.Errorf("missing required --to 0.2")
	}
	if to != "0.2" {
		return migrateOptions{}, fmt.Errorf("unsupported migration target %q (want 0.2)", to)
	}
	format := parsed.value("--format", "text")
	if format != "text" && format != "json" {
		return migrateOptions{}, fmt.Errorf("unsupported migrate format: %s", format)
	}
	return migrateOptions{
		path:                 path,
		from:                 from,
		to:                   to,
		actor:                parsed.value("--actor", ""),
		write:                parsed.boolValue("--write"),
		format:               format,
		citationMappingsFile: parsed.value("--citation-mappings", ""),
	}, nil
}

type migrationSourceResolver func(
	context.Context,
	bundle.Source,
	mutation.MigrationSourceOptions,
) (mutation.MigrationSourceResolution, error)

type migrationPlanner interface {
	Preview(
		context.Context,
		bundle.Source,
		mutation.MigrationSourceResolution,
		mutation.MigrationRequest,
	) (mutation.MigrationPreview, error)
	Apply(
		context.Context,
		store.Store,
		mutation.MigrationApplyRequest,
	) (store.CommitReceipt, error)
}

type migrationStore interface {
	store.Store
	Close() error
}

type migrationStoreOpener func(context.Context, string) (migrationStore, error)

type migrationSource interface {
	bundle.Source
	Close() error
}

type migrationSourceOpener func(context.Context, string) (migrationSource, error)

type citationMappingsOpener func(string) (io.ReadCloser, error)

type migrateDependencies struct {
	resolve              migrationSourceResolver
	planner              migrationPlanner
	openStore            migrationStoreOpener
	openSource           migrationSourceOpener
	openCitationMappings citationMappingsOpener
}

func productionMigrateDependencies() migrateDependencies {
	return migrateDependencies{
		resolve: mutation.ResolveMigrationSource,
		planner: mutation.NewMigrationPlanner(),
		openStore: func(ctx context.Context, root string) (migrationStore, error) {
			return storefs.OpenContext(ctx, root, storefs.Config{})
		},
		openSource: func(_ context.Context, root string) (migrationSource, error) {
			return &bundle.FileSystemSource{Root: root}, nil
		},
		openCitationMappings: openCitationMappingsFile,
	}
}

func cmdMigrate(args []string, stdout io.Writer) (int, error) {
	return cmdMigrateWithDependencies(args, stdout, productionMigrateDependencies())
}

func cmdMigrateWithResolver(
	args []string,
	stdout io.Writer,
	resolve migrationSourceResolver,
) (int, error) {
	dependencies := productionMigrateDependencies()
	dependencies.resolve = resolve
	return cmdMigrateWithDependencies(args, stdout, dependencies)
}

func cmdMigrateWithDependencies(
	args []string,
	stdout io.Writer,
	dependencies migrateDependencies,
) (int, error) {
	opts, err := parseMigrateArgs(args)
	if err != nil {
		return 0, err
	}
	citationMappings, err := loadCitationMappingsWithOpener(
		opts.citationMappingsFile,
		dependencies.openCitationMappings,
	)
	if err != nil {
		return 0, err
	}
	migrationInput := store.V01ToV02Migration{
		GeneratedBy:       opts.actor,
		TimestampPolicy:   store.LegacyTimestampPreserve,
		TimestampConflict: store.TimestampConflictReject,
		Citations:         citationMappings,
	}
	inputValidationErr := validateMigrateInput(migrationInput)
	if inputValidationErr != nil && !errors.Is(inputValidationErr, mutation.ErrAmbiguousPresentation) {
		return 0, inputValidationErr
	}
	report, code, render, err := buildMigrationReport(
		context.Background(),
		opts,
		migrationInput,
		inputValidationErr,
		dependencies,
	)
	if err != nil {
		return 0, err
	}
	if !render {
		return code, nil
	}
	return renderMigrationReport(stdout, opts.format, report, code)
}

// buildMigrationReport owns every migration resource and never publishes
// output. Callers may render its report only after this function has closed
// the store and source exactly once.
func buildMigrationReport(
	ctx context.Context,
	opts migrateOptions,
	migrationInput store.V01ToV02Migration,
	inputValidationErr error,
	dependencies migrateDependencies,
) (
	report migrationReport,
	code int,
	render bool,
	resultErr error,
) {
	planner := dependencies.planner
	source, err := dependencies.openSource(ctx, opts.path)
	if err != nil {
		return migrationReport{}, 0, false, err
	}
	if source == nil {
		return migrationReport{}, 0, false, errors.New("migration source opener returned nil source")
	}
	var backend migrationStore
	committed := false
	var cleanupJoinErr error
	defer func() {
		var closeErrors []error
		if backend != nil {
			if closeErr := backend.Close(); closeErr != nil {
				if committed {
					report.CleanupDiagnostics = append(report.CleanupDiagnostics, migrationCleanupDiagnosticDTO{
						Code:      "migration_store_close_failed",
						Phase:     "store_close",
						Message:   "migration store cleanup failed after durable commit",
						Retryable: false,
					})
				} else {
					closeErrors = append(closeErrors, fmt.Errorf("close migration store: %w", closeErr))
				}
			}
		}
		if closeErr := source.Close(); closeErr != nil {
			if committed {
				report.CleanupDiagnostics = append(report.CleanupDiagnostics, migrationCleanupDiagnosticDTO{
					Code:      "migration_source_close_failed",
					Phase:     "source_close",
					Message:   "migration source cleanup failed after durable commit",
					Retryable: false,
				})
			} else {
				closeErrors = append(closeErrors, fmt.Errorf("close migration source: %w", closeErr))
			}
		}
		if committed {
			// A valid durable receipt is authoritative. Cleanup failures are
			// non-retryable report diagnostics, never an operational failure.
			code = 0
			render = true
			resultErr = nil
			return
		}
		if len(closeErrors) != 0 {
			resultErr = errors.Join(append([]error{resultErr, cleanupJoinErr}, closeErrors...)...)
			code = 0
			render = false
		}
	}()

	resolution, resolutionErr := dependencies.resolve(ctx, source, mutation.MigrationSourceOptions{
		RequestedSelector: opts.from,
	})
	opts.source = resolution
	report = newMigrationReport(opts)
	report.Blockers = projectMigrationBlockers(resolution.Blockers)
	if resolution.Transition == mutation.MigrationTransitionBlocked || len(report.Blockers) != 0 {
		cleanupJoinErr = resolutionErr
		if len(report.Blockers) == 0 && resolutionErr != nil {
			report.Blockers = []migrationBlockerDTO{{Code: "unsupported_migration_source", Message: resolutionErr.Error()}}
		}
		return report, 1, true, nil
	}
	if resolutionErr != nil {
		return migrationReport{}, 0, false, fmt.Errorf("resolve migration source version: %w", resolutionErr)
	}

	request := mutation.MigrationRequest{
		ID:          store.ChangeSetID("okf-migrate-v01-v02"),
		Actor:       store.Actor("tool:okf-cli"),
		FromVersion: mutation.MigrationVersionV01,
		ToVersion:   mutation.MigrationVersionV02,
		Migration:   migrationInput,
	}
	preflight, preflightErr := mutation.PreflightV01ToV02MigrationInput(
		ctx,
		source,
		resolution,
		migrationInput,
	)
	report.Blockers = projectMigrationBlockers(preflight.Blockers)
	report.ManualActions = projectMigrationManualActions(preflight.ManualActions)
	if preflightErr != nil {
		cleanupJoinErr = preflightErr
		// Canonical presentation failures are source-free, so Preflight
		// intentionally has no document findings to project. Preserve their
		// stable typed CLI blocker/action projection without manufacturing
		// a transition proof.
		if len(report.Blockers) == 0 && inputValidationErr != nil {
			preview, previewErr := planner.Preview(ctx, source, resolution, request)
			report = projectMigrationPreview(opts, preview)
			if previewErr == nil || len(report.Blockers) == 0 {
				return migrationReport{}, 0, false, inputValidationErr
			}
			return report, 1, true, nil
		}
		if len(report.Blockers) == 0 {
			return migrationReport{}, 0, false, fmt.Errorf("preflight migration input: %w", preflightErr)
		}
		return report, 1, true, nil
	}
	if len(report.Blockers) != 0 || len(report.ManualActions) != 0 {
		return report, 1, true, nil
	}
	if resolution.Transition != mutation.MigrationTransitionV01ToV02 &&
		resolution.Transition != mutation.MigrationTransitionTargetNoop {
		return migrationReport{}, 0, false, fmt.Errorf("unsupported migration transition: %s", resolution.Transition)
	}

	// Opening the durable store creates metadata. Keep preview and rejection
	// paths read-only; Apply revalidates this exact proof after store open,
	// including target and planned no-op transitions.
	preview, err := planner.Preview(ctx, source, resolution, request)
	report = projectMigrationPreview(opts, preview)
	if err != nil {
		cleanupJoinErr = err
		if len(report.Blockers) == 0 {
			report.Blockers = []migrationBlockerDTO{{
				Code:    "migration_request_invalid",
				Message: err.Error(),
			}}
		}
		return report, 1, true, nil
	}
	if len(report.Blockers) != 0 {
		return report, 1, true, nil
	}
	if !opts.write {
		return report, 0, true, nil
	}
	openedBackend, openErr := dependencies.openStore(ctx, opts.path)
	if openErr != nil {
		return migrationReport{}, 0, false, openErr
	}
	backend = openedBackend
	if backend == nil {
		return migrationReport{}, 0, false, errors.New("migration store opener returned nil store")
	}
	receipt, applyErr := planner.Apply(ctx, backend, mutation.MigrationApplyRequest{
		Request:    request,
		Resolution: preview.Resolution,
		Proof:      preview.Proof,
		PlanDigest: preview.PlanDigest,
		Options: store.CommitOptions{
			IdempotencyKey: store.IdempotencyKey("okf-migrate-" + preview.PlanDigest),
		},
	})
	receipt, err = classifyMigrationCommitOutcome(receipt, applyErr)
	if err == nil {
		committed = true
		report.Applied = true
		report.Receipt = projectMigrationReceipt(receipt)
		return report, 0, true, nil
	}
	if blocker, ok := migrationApplyBlocker(err); ok {
		cleanupJoinErr = err
		report.Blockers = []migrationBlockerDTO{blocker}
		return report, 1, true, nil
	}
	return migrationReport{}, 0, false, err
}

func classifyMigrationCommitOutcome(
	receipt store.CommitReceipt,
	applyErr error,
) (store.CommitReceipt, error) {
	if err := store.ValidateCommitReceipt(receipt); err != nil {
		invalid := fmt.Errorf("apply migration returned invalid commit receipt: %w", err)
		if applyErr != nil {
			return store.CommitReceipt{}, errors.Join(applyErr, invalid)
		}
		return store.CommitReceipt{}, invalid
	}
	if applyErr == nil {
		return receipt.Clone(), nil
	}
	var committed *store.CommittedError
	if !errors.As(applyErr, &committed) {
		return store.CommitReceipt{}, applyErr
	}
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
	return receipt.Clone(), nil
}

func migrationApplyBlocker(err error) (migrationBlockerDTO, bool) {
	var conflict *store.Conflict
	if !errors.As(err, &conflict) && !errors.Is(err, mutation.ErrMigrationPlanMismatch) {
		return migrationBlockerDTO{}, false
	}
	return migrationBlockerDTO{
		Code:    "migration_apply_conflict",
		Message: err.Error(),
	}, true
}

func validateMigrateInput(input store.V01ToV02Migration) error {
	if err := mutation.ValidateV01ToV02MigrationInput(input); err != nil {
		return fmt.Errorf("validate migration input: %w", err)
	}
	return nil
}

type migrationReport struct {
	From               string                          `json:"from"`
	RequestedSelector  string                          `json:"requested_selector"`
	DeclarationPresent bool                            `json:"declaration_present"`
	DeclarationValid   bool                            `json:"declaration_valid"`
	DeclarationRaw     string                          `json:"declaration_raw"`
	Declared           string                          `json:"declared_version,omitempty"`
	Effective          string                          `json:"effective_version"`
	ResolvedSource     string                          `json:"resolved_source"`
	ResolutionSource   string                          `json:"resolution_source"`
	Compatibility      string                          `json:"compatibility"`
	Transition         string                          `json:"transition"`
	Future             bool                            `json:"future"`
	SourceFindings     []migrationSourceFindingDTO     `json:"source_findings"`
	To                 string                          `json:"to"`
	Actor              string                          `json:"actor,omitempty"`
	Mode               string                          `json:"mode"`
	Base               string                          `json:"base_revision,omitempty"`
	Result             string                          `json:"result_revision,omitempty"`
	PlanDigest         string                          `json:"plan_digest,omitempty"`
	ProofFormatVersion uint16                          `json:"proof_format_version,omitempty"`
	ResolutionDigest   string                          `json:"resolution_digest,omitempty"`
	Outcome            string                          `json:"outcome"`
	Noop               bool                            `json:"noop"`
	Applied            bool                            `json:"applied"`
	Reads              []string                        `json:"reads"`
	Changes            []migrationChangeDTO            `json:"changes"`
	Blockers           []migrationBlockerDTO           `json:"blockers"`
	ManualActions      []migrationManualActionDTO      `json:"manual_actions"`
	Receipt            *migrationReceiptDTO            `json:"receipt,omitempty"`
	CleanupDiagnostics []migrationCleanupDiagnosticDTO `json:"cleanup_diagnostics"`
}

type migrationCleanupDiagnosticDTO struct {
	Code      string `json:"code"`
	Phase     string `json:"phase"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type migrationChangeDTO struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	From   string `json:"from,omitempty"`
	Digest string `json:"digest,omitempty"`
}

type migrationBlockerDTO struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Start   int    `json:"start,omitempty"`
	End     int    `json:"end,omitempty"`
	Message string `json:"message"`
}

type migrationManualActionDTO struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type migrationSourceFindingDTO struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Start int    `json:"start,omitempty"`
	End   int    `json:"end,omitempty"`
}

type migrationReceiptDTO struct {
	FormatVersion  uint16                    `json:"format_version"`
	ChangeSetID    string                    `json:"change_set_id"`
	IdempotencyKey string                    `json:"idempotency_key"`
	RequestDigest  string                    `json:"request_digest"`
	BaseRevision   string                    `json:"base_revision"`
	ResultRevision string                    `json:"result_revision"`
	CommitTime     string                    `json:"commit_time"`
	ChangedRefs    []string                  `json:"changed_refs"`
	ChangedFiles   []migrationChangedFileDTO `json:"changed_files"`
}

type migrationChangedFileDTO struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
	From string `json:"from,omitempty"`
}

func newMigrationReport(opts migrateOptions) migrationReport {
	mode := "dry-run"
	if opts.write {
		mode = "apply"
	}
	return migrationReport{
		From:               opts.source.FromVersion,
		RequestedSelector:  opts.source.RequestedSelector,
		DeclarationPresent: opts.source.DeclarationPresent,
		DeclarationValid:   opts.source.DeclarationValid,
		DeclarationRaw:     opts.source.DeclarationRaw,
		Declared:           opts.source.DeclaredVersion,
		Effective:          opts.source.ResolvedSource,
		ResolvedSource:     opts.source.ResolvedSource,
		ResolutionSource:   string(opts.source.ResolutionSource),
		Compatibility:      migrationResolutionCompatibility(opts.source),
		Transition:         string(opts.source.Transition),
		Future:             opts.source.ResolutionSource == mutation.MigrationResolutionFuture,
		To:                 opts.to,
		Actor:              opts.actor,
		Mode:               mode,
		Reads:              []string{},
		Changes:            []migrationChangeDTO{},
		Blockers:           []migrationBlockerDTO{},
		ManualActions:      []migrationManualActionDTO{},
		SourceFindings:     projectMigrationSourceFindings(opts.source.Candidates),
		CleanupDiagnostics: []migrationCleanupDiagnosticDTO{},
	}
}

func migrationResolutionCompatibility(resolution mutation.MigrationSourceResolution) string {
	switch {
	case resolution.ResolutionSource == mutation.MigrationResolutionFuture:
		return string(bundle.VersionCompatibilityBestEffort)
	case resolution.ResolvedSource == mutation.MigrationVersionV01:
		return string(bundle.VersionCompatibilityLegacy)
	default:
		return string(bundle.VersionCompatibilityNative)
	}
}

func projectMigrationSourceFindings(findings []mutation.MigrationLegacyCandidate) []migrationSourceFindingDTO {
	out := make([]migrationSourceFindingDTO, 0, len(findings))
	for _, finding := range findings {
		out = append(out, migrationSourceFindingDTO{
			Kind: finding.Kind, Path: finding.Path,
			Start: finding.Location.Start, End: finding.Location.End,
		})
	}
	return out
}

func projectMigrationBlockers(blockers []mutation.MigrationBlocker) []migrationBlockerDTO {
	out := make([]migrationBlockerDTO, 0, len(blockers))
	for _, blocker := range blockers {
		out = append(out, migrationBlockerDTO{
			Code:    blocker.Code,
			Path:    blocker.Path,
			Start:   blocker.Location.Start,
			End:     blocker.Location.End,
			Message: blocker.Message,
		})
	}
	return out
}

func projectMigrationManualActions(actions []mutation.MigrationManualAction) []migrationManualActionDTO {
	out := make([]migrationManualActionDTO, 0, len(actions))
	for _, action := range actions {
		out = append(out, migrationManualActionDTO{
			Code:    action.Code,
			Path:    action.Path,
			Message: action.Message,
		})
	}
	return out
}

func projectMigrationPreview(opts migrateOptions, preview mutation.MigrationPreview) migrationReport {
	if preview.Resolution.RequestedSelector != "" {
		opts.source = preview.Resolution
	}
	report := newMigrationReport(opts)
	report.Base = preview.Preview.BaseRevision.String()
	report.Result = preview.Preview.ResultRevision.String()
	report.PlanDigest = preview.PlanDigest
	report.ProofFormatVersion = preview.Proof.FormatVersion
	report.ResolutionDigest = preview.Proof.ResolutionDigest
	for _, read := range preview.Preview.Reads {
		report.Reads = append(report.Reads, read.Path)
	}
	for _, write := range preview.Preview.Writes {
		report.Changes = append(report.Changes, migrationChangeDTO{
			Kind: "write", Path: write.Path, Digest: write.Digest,
		})
	}
	for _, path := range preview.Preview.Deletes {
		report.Changes = append(report.Changes, migrationChangeDTO{Kind: "delete", Path: path})
	}
	for _, rename := range preview.Preview.Renames {
		report.Changes = append(report.Changes, migrationChangeDTO{
			Kind: "rename", Path: rename.To, From: rename.From,
		})
	}
	for _, blocker := range preview.Blockers {
		report.Blockers = append(report.Blockers, migrationBlockerDTO{
			Code:    blocker.Code,
			Path:    blocker.Path,
			Start:   blocker.Location.Start,
			End:     blocker.Location.End,
			Message: blocker.Message,
		})
	}
	for _, diagnostic := range preview.Preview.Diagnostics {
		if diagnostic.Severity != store.DiagnosticError {
			continue
		}
		report.Blockers = append(report.Blockers, migrationBlockerDTO{
			Code:    diagnostic.Code,
			Path:    diagnostic.File,
			Message: diagnostic.Message,
		})
	}
	report.ManualActions = append(report.ManualActions, projectMigrationManualActions(preview.ManualActions)...)
	report.Noop = len(report.Changes) == 0 && len(report.Blockers) == 0 && len(report.ManualActions) == 0
	return report
}

func projectMigrationReceipt(receipt store.CommitReceipt) *migrationReceiptDTO {
	out := &migrationReceiptDTO{
		FormatVersion:  receipt.FormatVersion,
		ChangeSetID:    string(receipt.ChangeSetID),
		IdempotencyKey: string(receipt.IdempotencyKey),
		RequestDigest:  receipt.RequestDigest,
		BaseRevision:   receipt.BaseRevision.String(),
		ResultRevision: receipt.ResultRevision.String(),
		CommitTime:     receipt.CommitTime.UTC().Format(time.RFC3339Nano),
		ChangedRefs:    make([]string, 0, len(receipt.ChangedRefs)),
		ChangedFiles:   make([]migrationChangedFileDTO, 0, len(receipt.ChangedFiles)),
	}
	for _, ref := range receipt.ChangedRefs {
		out.ChangedRefs = append(out.ChangedRefs, ref.String())
	}
	for _, file := range receipt.ChangedFiles {
		out.ChangedFiles = append(out.ChangedFiles, migrationChangedFileDTO{
			Kind: string(file.Kind),
			Path: file.Path,
			From: file.From,
		})
	}
	return out
}

func renderMigrationReport(stdout io.Writer, format string, report migrationReport, code int) (int, error) {
	if report.CleanupDiagnostics == nil {
		report.CleanupDiagnostics = []migrationCleanupDiagnosticDTO{}
	}
	switch {
	case len(report.Blockers) != 0:
		report.Outcome = "blocked"
	case report.Applied && len(report.CleanupDiagnostics) != 0:
		report.Outcome = "applied_with_cleanup_diagnostics"
	case report.Applied:
		report.Outcome = "applied"
	case report.Noop:
		report.Outcome = "noop"
	default:
		report.Outcome = "preview"
	}
	if format == "json" {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			return 0, err
		}
		return code, nil
	}
	fmt.Fprintf(stdout, "Migration: %s -> %s\n",
		renderTextString(report.From), renderTextString(report.To))
	fmt.Fprintln(stdout, "Source:")
	fmt.Fprintf(stdout, "  requested selector: %s\n", renderTextString(report.RequestedSelector))
	fmt.Fprintf(stdout, "  declaration: present=%t valid=%t raw=%s declared=%s\n",
		report.DeclarationPresent,
		report.DeclarationValid,
		renderTextString(report.DeclarationRaw),
		renderTextString(report.Declared))
	fmt.Fprintf(stdout, "  resolution: effective=%s resolved=%s source=%s compatibility=%s transition=%s future=%t\n",
		renderTextString(report.Effective),
		renderTextString(report.ResolvedSource),
		renderTextString(report.ResolutionSource),
		renderTextString(report.Compatibility),
		renderTextString(report.Transition),
		report.Future)
	fmt.Fprintf(stdout, "Source findings (%d):\n", len(report.SourceFindings))
	for _, finding := range report.SourceFindings {
		fmt.Fprintf(stdout, "  kind=%s path=%s start=%d end=%d\n",
			renderTextString(finding.Kind),
			renderTextString(finding.Path),
			finding.Start,
			finding.End)
	}
	fmt.Fprintf(stdout, "Actor: %s\n", renderTextString(report.Actor))
	fmt.Fprintf(stdout, "Mode: %s\n", renderTextString(report.Mode))
	fmt.Fprintf(stdout, "Base revision: %s\n", renderTextString(report.Base))
	fmt.Fprintf(stdout, "Result revision: %s\n", renderTextString(report.Result))
	fmt.Fprintf(stdout, "Plan digest: %s\n", renderTextString(report.PlanDigest))
	fmt.Fprintf(stdout, "Proof: format_version=%d resolution_digest=%s\n",
		report.ProofFormatVersion, renderTextString(report.ResolutionDigest))
	fmt.Fprintf(stdout, "Reads (%d):\n", len(report.Reads))
	for index, path := range report.Reads {
		fmt.Fprintf(stdout, "  [%d] %s\n", index, renderTextString(path))
	}
	fmt.Fprintf(stdout, "Changes (%d):\n", len(report.Changes))
	for _, change := range report.Changes {
		fmt.Fprintf(stdout, "  kind=%s path=%s from=%s digest=%s\n",
			renderTextString(change.Kind),
			renderTextString(change.Path),
			renderTextString(change.From),
			renderTextString(change.Digest))
	}
	fmt.Fprintf(stdout, "Blockers (%d):\n", len(report.Blockers))
	for _, blocker := range report.Blockers {
		fmt.Fprintf(stdout, "  code=%s path=%s start=%d end=%d message=%s\n",
			renderTextString(blocker.Code),
			renderTextString(blocker.Path),
			blocker.Start,
			blocker.End,
			renderTextString(blocker.Message))
	}
	fmt.Fprintf(stdout, "Manual actions (%d):\n", len(report.ManualActions))
	for _, action := range report.ManualActions {
		fmt.Fprintf(stdout, "  code=%s path=%s message=%s\n",
			renderTextString(action.Code),
			renderTextString(action.Path),
			renderTextString(action.Message))
	}
	fmt.Fprintf(stdout, "Cleanup diagnostics (%d):\n", len(report.CleanupDiagnostics))
	for _, diagnostic := range report.CleanupDiagnostics {
		fmt.Fprintf(stdout, "  code=%s phase=%s retryable=%t message=%s\n",
			renderTextString(diagnostic.Code),
			renderTextString(diagnostic.Phase),
			diagnostic.Retryable,
			renderTextString(diagnostic.Message))
	}
	fmt.Fprintf(stdout, "State: outcome=%s noop=%t applied=%t\n",
		renderTextString(report.Outcome), report.Noop, report.Applied)
	if report.Receipt == nil {
		fmt.Fprintln(stdout, "Receipt: (absent)")
	} else {
		fmt.Fprintln(stdout, "Receipt:")
		fmt.Fprintf(stdout, "  format_version: %d\n", report.Receipt.FormatVersion)
		fmt.Fprintf(stdout, "  change_set_id: %s\n", renderTextString(report.Receipt.ChangeSetID))
		fmt.Fprintf(stdout, "  idempotency_key: %s\n", renderTextString(report.Receipt.IdempotencyKey))
		fmt.Fprintf(stdout, "  request_digest: %s\n", renderTextString(report.Receipt.RequestDigest))
		fmt.Fprintf(stdout, "  base_revision: %s\n", renderTextString(report.Receipt.BaseRevision))
		fmt.Fprintf(stdout, "  result_revision: %s\n", renderTextString(report.Receipt.ResultRevision))
		fmt.Fprintf(stdout, "  commit_time: %s\n", renderTextString(report.Receipt.CommitTime))
		fmt.Fprintf(stdout, "  changed_refs (%d):\n", len(report.Receipt.ChangedRefs))
		for index, ref := range report.Receipt.ChangedRefs {
			fmt.Fprintf(stdout, "    [%d] %s\n", index, renderTextString(ref))
		}
		fmt.Fprintf(stdout, "  changed_files (%d):\n", len(report.Receipt.ChangedFiles))
		for _, file := range report.Receipt.ChangedFiles {
			fmt.Fprintf(stdout, "    kind=%s path=%s from=%s\n",
				renderTextString(file.Kind),
				renderTextString(file.Path),
				renderTextString(file.From))
		}
	}
	fmt.Fprintf(stdout, "Result: %s\n", renderTextString(strings.ToUpper(report.Outcome)))
	return code, nil
}
