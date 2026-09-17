package mutation

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/markdownowner"
	"github.com/skosovsky/okf/internal/migrationpath"
)

const MigrationSelectorAuto = "auto"

type MigrationResolutionSource string

const (
	MigrationResolutionExplicit    MigrationResolutionSource = "explicit"
	MigrationResolutionDeclared    MigrationResolutionSource = "declared"
	MigrationResolutionLegacyProbe MigrationResolutionSource = "legacy-probe"
	MigrationResolutionDefault     MigrationResolutionSource = "default"
	MigrationResolutionFuture      MigrationResolutionSource = "future"
)

type MigrationTransition string

const (
	MigrationTransitionV01ToV02   MigrationTransition = "v0.1-to-v0.2"
	MigrationTransitionTargetNoop MigrationTransition = "target-noop"
	MigrationTransitionBlocked    MigrationTransition = "blocked"
)

type MigrationSourceOptions struct {
	RequestedSelector string
	AllowFutureNoop   bool
}

type MigrationLegacyCandidate struct {
	Kind     string
	Path     string
	Location SourceSpan
}

// MigrationSourceResolution is the single domain projection used by CLI and
// MCP surfaces. FromVersion/ToVersion describe the actual transition: a
// target-noop is 0.2 -> 0.2 and never silently executes a 0.1 migration.
type MigrationSourceResolution struct {
	RequestedSelector  string
	DeclarationPresent bool
	DeclarationValid   bool
	DeclarationRaw     string
	DeclaredVersion    string
	ResolvedSource     string
	ResolutionSource   MigrationResolutionSource
	FromVersion        string
	ToVersion          string
	Transition         MigrationTransition
	Candidates         []MigrationLegacyCandidate
	Blockers           []MigrationBlocker
}

// ResolveSource performs parser-backed source resolution without mutating or
// previewing the bundle.
func (p MigrationPlanner) ResolveSource(ctx context.Context, source bundle.Source, options MigrationSourceOptions) (MigrationSourceResolution, error) {
	_ = p
	return ResolveMigrationSource(ctx, source, options)
}

// ResolveMigrationSource resolves auto/explicit migration source semantics.
// Only the root frontmatter declaration, root-level legacy timestamp keys, and
// neutral Markdown ownership evidence participate in detection.
func ResolveMigrationSource(ctx context.Context, source bundle.Source, options MigrationSourceOptions) (MigrationSourceResolution, error) {
	if source == nil {
		return MigrationSourceResolution{}, errors.New("mutation: nil migration source")
	}
	selector := options.RequestedSelector
	if selector == "" {
		selector = MigrationSelectorAuto
	}
	out := MigrationSourceResolution{RequestedSelector: selector, ToVersion: MigrationVersionV02}
	if selector != MigrationSelectorAuto && selector != MigrationVersionV01 && selector != MigrationVersionV02 {
		return out, fmt.Errorf("%w: selector %q", ErrUnsupportedMigrationVersion, selector)
	}

	loaded, err := bundle.Load(ctx, source)
	if err != nil {
		return migrationSourceResolutionError(out, err)
	}
	parseErrors := loaded.ParseErrors()
	if err := sortSliceCompareContext(ctx, parseErrors, func(ctx context.Context, left, right bundle.ParseError) (int, error) {
		return compareStringsContext(ctx, left.Path, right.Path)
	}); err != nil {
		return MigrationSourceResolution{}, err
	}
	rootInvalidUTF8 := false
	for _, parseErr := range parseErrors {
		if parseErr.Path != "index.md" {
			continue
		}
		if selector == MigrationSelectorAuto && errors.Is(parseErr.Err, bundle.ErrInvalidEncoding) {
			rootInvalidUTF8 = true
			break
		}
		return out, parseErr
	}
	declaration := bundle.VersionDeclarationState{Valid: true}
	if !rootInvalidUTF8 {
		declaration, err = migrationDeclaredVersionState(ctx, loaded)
		out.DeclarationPresent = declaration.Present
		out.DeclarationValid = declaration.Valid
		out.DeclarationRaw = declaration.Raw
		out.DeclaredVersion = declaration.Value
		if err != nil {
			if selector != MigrationSelectorAuto ||
				!errors.Is(err, bundle.ErrInvalidEncoding) {
				return migrationSourceResolutionError(out, err)
			}
			rootInvalidUTF8 = true
		}
	}
	if rootInvalidUTF8 {
		out.DeclarationValid = false
	}
	declared := declaration.Value

	if selector != MigrationSelectorAuto {
		resolution, err := bundle.ResolveVersion(declared, selector)
		if err != nil {
			return out, err
		}
		out.ResolvedSource = resolution.Effective
		out.ResolutionSource = MigrationResolutionExplicit
		return validatedMigrationSourceResolutionContext(ctx, finishMigrationResolution(out))
	}

	switch declared {
	case MigrationVersionV01:
		out.ResolvedSource = MigrationVersionV01
		out.ResolutionSource = MigrationResolutionDeclared
		return validatedMigrationSourceResolutionContext(ctx, finishMigrationResolution(out))
	case MigrationVersionV02:
		out.ResolvedSource = MigrationVersionV02
		out.ResolutionSource = MigrationResolutionDeclared
		return validatedMigrationSourceResolutionContext(ctx, finishMigrationResolution(out))
	case "":
		candidates, err := probeLegacyMigrationCandidates(ctx, loaded)
		out.Candidates = candidates
		if err != nil {
			return migrationSourceResolutionError(out, err)
		}
		if len(candidates) != 0 {
			out.ResolvedSource = MigrationVersionV01
			out.ResolutionSource = MigrationResolutionLegacyProbe
		} else {
			out.ResolvedSource = MigrationVersionV02
			out.ResolutionSource = MigrationResolutionDefault
		}
		return validatedMigrationSourceResolutionContext(ctx, finishMigrationResolution(out))
	default:
		out.ResolvedSource = declared
		out.ResolutionSource = MigrationResolutionFuture
		if options.AllowFutureNoop {
			out.FromVersion = declared
			out.ToVersion = declared
			out.Transition = MigrationTransitionTargetNoop
			return validatedMigrationSourceResolutionContext(ctx, out)
		}
		out.FromVersion = declared
		out.ToVersion = ""
		out.Transition = MigrationTransitionBlocked
		blocker := MigrationBlocker{
			Code:    "unsupported_migration_source",
			Path:    "index.md",
			Message: fmt.Sprintf("future declaration %q cannot be rewritten as v0.2", declared),
		}
		out.Blockers = []MigrationBlocker{blocker}
		validated, err := validatedMigrationSourceResolutionContext(ctx, out)
		if err != nil {
			return validated, err
		}
		return validated, fmt.Errorf("%w: %s", ErrUnsupportedMigrationVersion, blocker.Message)
	}
}

func migrationSourceResolutionError(
	resolution MigrationSourceResolution,
	err error,
) (MigrationSourceResolution, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return MigrationSourceResolution{}, err
	}
	return resolution, err
}

func validatedMigrationSourceResolutionContext(
	ctx context.Context,
	resolution MigrationSourceResolution,
) (MigrationSourceResolution, error) {
	if err := validateMigrationSourceResolutionContext(ctx, resolution); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return MigrationSourceResolution{}, err
		}
		return resolution, err
	}
	return resolution, nil
}

func finishMigrationResolution(out MigrationSourceResolution) MigrationSourceResolution {
	out.FromVersion = out.ResolvedSource
	if out.ResolvedSource == MigrationVersionV01 {
		out.Transition = MigrationTransitionV01ToV02
		return out
	}
	out.ToVersion = out.ResolvedSource
	out.Transition = MigrationTransitionTargetNoop
	return out
}

func migrationDeclaredVersionState(ctx context.Context, loaded *bundle.Bundle) (bundle.VersionDeclarationState, error) {
	data, ok, readErr := loaded.ReadFileContext(ctx, "index.md")
	if readErr != nil {
		return bundle.VersionDeclarationState{}, readErr
	}
	if !ok {
		if err := ctx.Err(); err != nil {
			return bundle.VersionDeclarationState{}, err
		}
		return bundle.VersionDeclarationState{Valid: true}, nil
	}
	if err := ctx.Err(); err != nil {
		return bundle.VersionDeclarationState{}, err
	}
	text, err := bytesStringContext(ctx, data)
	if err != nil {
		return bundle.VersionDeclarationState{}, err
	}
	document, err := bundle.ParseDocumentContext(ctx, text)
	if err != nil {
		return bundle.VersionDeclarationState{}, fmt.Errorf("parse root version declaration: %w", err)
	}
	state := document.Frontmatter.VersionDeclarationState()
	if state.Present && !state.Valid {
		return state, fmt.Errorf("%w: %q", bundle.ErrInvalidVersionDeclaration, state.Raw)
	}
	return state, nil
}

// Clone returns an independent source resolution. Nil and empty finding
// slices are semantically equivalent and are both encoded as a zero count.
func (r MigrationSourceResolution) Clone() MigrationSourceResolution {
	cloned, _ := r.cloneContext(context.Background())
	return cloned
}

func (r MigrationSourceResolution) cloneContext(ctx context.Context) (MigrationSourceResolution, error) {
	var err error
	if r.Candidates, err = cloneMigrationProofSliceContext(ctx, r.Candidates); err != nil {
		return MigrationSourceResolution{}, err
	}
	if r.Blockers, err = cloneMigrationProofSliceContext(ctx, r.Blockers); err != nil {
		return MigrationSourceResolution{}, err
	}
	return r, ctx.Err()
}

// ValidateMigrationSourceResolution validates the complete, caller-owned
// source-resolution projection without reading or mutating a bundle. It is the
// canonical trust boundary for planner, runtime, and transport surfaces.
func ValidateMigrationSourceResolution(resolution MigrationSourceResolution) error {
	return validateMigrationSourceResolutionContext(context.Background(), resolution)
}

func validateMigrationSourceResolutionContext(ctx context.Context, resolution MigrationSourceResolution) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch resolution.RequestedSelector {
	case MigrationSelectorAuto, MigrationVersionV01, MigrationVersionV02:
	default:
		return invalidMigrationSourceResolution(
			fmt.Sprintf("invalid requested selector %q", resolution.RequestedSelector),
		)
	}
	diagnostics := resolution.ResolutionSource == "" &&
		resolution.ResolvedSource == "" &&
		resolution.FromVersion == "" &&
		resolution.Transition == ""
	if err := validateMigrationResolutionDeclaration(resolution, diagnostics); err != nil {
		return err
	}
	if err := validateMigrationResolutionCandidatesContext(ctx, resolution.Candidates); err != nil {
		return err
	}
	if diagnostics {
		if resolution.ToVersion != MigrationVersionV02 ||
			len(resolution.Blockers) != 0 ||
			resolution.RequestedSelector != MigrationSelectorAuto && len(resolution.Candidates) != 0 ||
			len(resolution.Candidates) != 0 && resolution.DeclarationPresent ||
			resolution.DeclarationPresent && resolution.DeclarationValid &&
				(resolution.RequestedSelector == MigrationSelectorAuto ||
					resolution.DeclaredVersion == resolution.RequestedSelector) ||
			!resolution.DeclarationPresent && resolution.DeclarationValid &&
				resolution.RequestedSelector != MigrationSelectorAuto {
			return invalidMigrationSourceResolution("incoherent diagnostic source resolution")
		}
		return nil
	}
	if resolution.ResolutionSource == "" ||
		resolution.ResolvedSource == "" ||
		resolution.FromVersion == "" ||
		resolution.Transition == "" {
		return invalidMigrationSourceResolution("incomplete resolved source resolution")
	}
	if !resolution.DeclarationValid {
		return invalidMigrationSourceResolution("resolved source has invalid declaration state")
	}

	switch resolution.ResolutionSource {
	case MigrationResolutionExplicit:
		if (resolution.RequestedSelector != MigrationVersionV01 &&
			resolution.RequestedSelector != MigrationVersionV02) ||
			resolution.ResolvedSource != resolution.RequestedSelector ||
			resolution.DeclarationPresent &&
				resolution.DeclaredVersion != resolution.RequestedSelector ||
			len(resolution.Candidates) != 0 {
			return invalidMigrationSourceResolution("incoherent explicit source resolution")
		}
	case MigrationResolutionDeclared:
		if resolution.RequestedSelector != MigrationSelectorAuto ||
			!resolution.DeclarationPresent ||
			(resolution.DeclaredVersion != MigrationVersionV01 &&
				resolution.DeclaredVersion != MigrationVersionV02) ||
			resolution.ResolvedSource != resolution.DeclaredVersion ||
			len(resolution.Candidates) != 0 {
			return invalidMigrationSourceResolution("incoherent declared source resolution")
		}
	case MigrationResolutionLegacyProbe:
		if resolution.RequestedSelector != MigrationSelectorAuto ||
			resolution.DeclarationPresent ||
			resolution.ResolvedSource != MigrationVersionV01 ||
			len(resolution.Candidates) == 0 {
			return invalidMigrationSourceResolution("incoherent legacy-probe source resolution")
		}
	case MigrationResolutionDefault:
		if resolution.RequestedSelector != MigrationSelectorAuto ||
			resolution.DeclarationPresent ||
			resolution.ResolvedSource != MigrationVersionV02 ||
			len(resolution.Candidates) != 0 {
			return invalidMigrationSourceResolution("incoherent default source resolution")
		}
	case MigrationResolutionFuture:
		if resolution.RequestedSelector != MigrationSelectorAuto ||
			!resolution.DeclarationPresent ||
			resolution.ResolvedSource != resolution.DeclaredVersion ||
			resolution.ResolvedSource == MigrationVersionV01 ||
			resolution.ResolvedSource == MigrationVersionV02 ||
			len(resolution.Candidates) != 0 {
			return invalidMigrationSourceResolution("incoherent future source resolution")
		}
	default:
		return invalidMigrationSourceResolution("unknown source resolution provenance")
	}

	switch resolution.Transition {
	case MigrationTransitionV01ToV02:
		if resolution.ResolvedSource != MigrationVersionV01 ||
			resolution.FromVersion != MigrationVersionV01 ||
			resolution.ToVersion != MigrationVersionV02 ||
			len(resolution.Blockers) != 0 {
			return invalidMigrationSourceResolution("incoherent v0.1 transition resolution")
		}
	case MigrationTransitionTargetNoop:
		if resolution.ResolvedSource == MigrationVersionV01 ||
			resolution.FromVersion != resolution.ResolvedSource ||
			resolution.ToVersion != resolution.ResolvedSource ||
			len(resolution.Blockers) != 0 {
			return invalidMigrationSourceResolution("incoherent target-noop resolution")
		}
	case MigrationTransitionBlocked:
		if resolution.ResolutionSource != MigrationResolutionFuture ||
			resolution.FromVersion != resolution.ResolvedSource ||
			resolution.ToVersion != "" ||
			len(resolution.Blockers) != 1 {
			return invalidMigrationSourceResolution("incoherent blocked source resolution")
		}
		blocker := resolution.Blockers[0]
		wantMessage := fmt.Sprintf(
			"future declaration %q cannot be rewritten as v0.2",
			resolution.ResolvedSource,
		)
		if blocker.Code != "unsupported_migration_source" ||
			blocker.Path != "index.md" ||
			blocker.Location != (SourceSpan{}) ||
			blocker.Message != wantMessage {
			return invalidMigrationSourceResolution("invalid blocked source finding")
		}
	default:
		return invalidMigrationSourceResolution(
			fmt.Sprintf("unknown migration transition %q", resolution.Transition),
		)
	}
	return ctx.Err()
}

func validateMigrationResolutionDeclaration(
	resolution MigrationSourceResolution,
	diagnostics bool,
) error {
	if !resolution.DeclarationPresent {
		if resolution.DeclarationRaw != "" || resolution.DeclaredVersion != "" ||
			!resolution.DeclarationValid && !diagnostics {
			return invalidMigrationSourceResolution("incoherent absent declaration state")
		}
		return nil
	}
	if resolution.DeclarationRaw == "" {
		return invalidMigrationSourceResolution("present declaration is missing raw value")
	}
	if !resolution.DeclarationValid {
		if resolution.DeclaredVersion != "" || !diagnostics {
			return invalidMigrationSourceResolution("incoherent invalid declaration state")
		}
		return nil
	}
	if resolution.DeclaredVersion == "" ||
		resolution.DeclarationRaw != resolution.DeclaredVersion {
		return invalidMigrationSourceResolution("incoherent valid declaration state")
	}
	if _, err := bundle.ParseOKFVersion(resolution.DeclaredVersion); err != nil {
		return invalidMigrationSourceResolution("invalid declared version")
	}
	return nil
}

func validateMigrationResolutionCandidatesContext(ctx context.Context, candidates []MigrationLegacyCandidate) error {
	for index, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if candidate.Kind != "timestamp" && candidate.Kind != "citations" {
			return invalidMigrationSourceResolution("invalid legacy candidate kind")
		}
		if bundle.ValidateRevisionPath(candidate.Path) != nil ||
			!migrationpath.IsMarkdownDocument(candidate.Path) {
			return invalidMigrationSourceResolution("invalid legacy candidate path")
		}
		if candidate.Location.Start < 0 ||
			candidate.Location.End < candidate.Location.Start {
			return invalidMigrationSourceResolution("invalid legacy candidate span")
		}
		if candidate.Kind == "timestamp" && candidate.Location != (SourceSpan{}) ||
			candidate.Kind == "citations" &&
				candidate.Location.End == candidate.Location.Start {
			return invalidMigrationSourceResolution("incoherent legacy candidate span")
		}
		if index > 0 &&
			compareMigrationLegacyCandidate(candidates[index-1], candidate) >= 0 {
			return invalidMigrationSourceResolution("noncanonical legacy candidates")
		}
	}
	return nil
}

func compareMigrationLegacyCandidate(left, right MigrationLegacyCandidate) int {
	switch {
	case left.Path < right.Path:
		return -1
	case left.Path > right.Path:
		return 1
	case left.Kind < right.Kind:
		return -1
	case left.Kind > right.Kind:
		return 1
	case left.Location.Start < right.Location.Start:
		return -1
	case left.Location.Start > right.Location.Start:
		return 1
	case left.Location.End < right.Location.End:
		return -1
	case left.Location.End > right.Location.End:
		return 1
	default:
		return 0
	}
}

func invalidMigrationSourceResolution(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedMigrationVersion, reason)
}

func migrationSourceResolutionDigestContext(ctx context.Context, resolution MigrationSourceResolution) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateMigrationSourceResolutionContext(ctx, resolution); err != nil {
		return "", err
	}
	e := newContextPlanDigestEncoder(ctx)
	writeString := func(value string) error { return e.string(value) }
	writeCount := func(value int) error { return e.count(value) }
	if err := writeString("okf:migration-source-resolution:v1"); err != nil {
		return "", err
	}
	if err := writeString(resolution.RequestedSelector); err != nil {
		return "", err
	}
	if resolution.DeclarationPresent {
		if err := writeString("present"); err != nil {
			return "", err
		}
	} else {
		if err := writeString("absent"); err != nil {
			return "", err
		}
	}
	if resolution.DeclarationValid {
		if err := writeString("valid"); err != nil {
			return "", err
		}
	} else {
		if err := writeString("invalid"); err != nil {
			return "", err
		}
	}
	for _, value := range []string{
		resolution.DeclarationRaw,
		resolution.DeclaredVersion,
		resolution.ResolvedSource,
		string(resolution.ResolutionSource),
		resolution.FromVersion,
		resolution.ToVersion,
		string(resolution.Transition),
		"ordered_candidates",
	} {
		if err := writeString(value); err != nil {
			return "", err
		}
	}
	if err := writeCount(len(resolution.Candidates)); err != nil {
		return "", err
	}
	for _, candidate := range resolution.Candidates {
		if err := writeString(candidate.Kind); err != nil {
			return "", err
		}
		if err := writeString(candidate.Path); err != nil {
			return "", err
		}
		if err := writeCount(candidate.Location.Start); err != nil {
			return "", err
		}
		if err := writeCount(candidate.Location.End); err != nil {
			return "", err
		}
	}
	if err := writeString("ordered_blockers"); err != nil {
		return "", err
	}
	if err := writeCount(len(resolution.Blockers)); err != nil {
		return "", err
	}
	for _, blocker := range resolution.Blockers {
		if err := writeString(blocker.Code); err != nil {
			return "", err
		}
		if err := writeString(blocker.Path); err != nil {
			return "", err
		}
		if err := writeCount(blocker.Location.Start); err != nil {
			return "", err
		}
		if err := writeCount(blocker.Location.End); err != nil {
			return "", err
		}
		if err := writeString(blocker.Message); err != nil {
			return "", err
		}
	}
	return e.digest()
}

func constantTimeDigestEqual(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func validateMigrationResolutionAgainstBundle(
	ctx context.Context,
	loaded *bundle.Bundle,
	resolution MigrationSourceResolution,
) error {
	if err := validateMigrationSourceResolutionContext(ctx, resolution); err != nil {
		return err
	}
	declaration, err := migrationDeclaredVersionState(ctx, loaded)
	if err != nil {
		if resolution.ResolutionSource == "" && resolution.Transition == "" {
			return nil
		}
		return err
	}
	if declaration.Value != resolution.DeclaredVersion ||
		declaration.Present != resolution.DeclarationPresent ||
		declaration.Valid != resolution.DeclarationValid ||
		declaration.Raw != resolution.DeclarationRaw {
		return fmt.Errorf("%w: source declaration changed after resolution", ErrUnsupportedMigrationVersion)
	}
	if resolution.ResolutionSource != MigrationResolutionLegacyProbe &&
		resolution.ResolutionSource != MigrationResolutionDefault {
		return nil
	}
	candidates, err := probeLegacyMigrationCandidates(ctx, loaded)
	if err != nil {
		return err
	}
	if len(candidates) != len(resolution.Candidates) {
		return fmt.Errorf("%w: legacy evidence changed after resolution", ErrUnsupportedMigrationVersion)
	}
	for index := range candidates {
		if candidates[index] != resolution.Candidates[index] {
			return fmt.Errorf("%w: legacy evidence changed after resolution", ErrUnsupportedMigrationVersion)
		}
	}
	return nil
}

func probeLegacyMigrationCandidates(ctx context.Context, loaded *bundle.Bundle) ([]MigrationLegacyCandidate, error) {
	paths, err := migrationMarkdownPathsContext(ctx, loaded)
	if err != nil {
		return nil, err
	}

	var candidates []MigrationLegacyCandidate
	var causes []error
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source, ok, readErr := loaded.ReadFileContext(ctx, path)
		if readErr != nil {
			return nil, readErr
		}
		if !ok {
			continue
		}
		text, textErr := bytesStringContext(ctx, source)
		if textErr != nil {
			return nil, textErr
		}
		document, err := bundle.ParseDocumentContext(ctx, text)
		if err != nil {
			if errors.Is(err, bundle.ErrInvalidEncoding) {
				_, ownershipErr := markdownowner.CollectMarkdownMigrationOwnership(ctx, source)
				if ownershipErr != nil {
					causes = append(causes, fmt.Errorf("probe migration candidate %q: %w", path, ownershipErr))
					continue
				}
			}
			causes = append(causes, fmt.Errorf("parse migration candidate %q: %w", path, err))
			continue
		}
		fallback := document.LegacyFallbackObservation()
		if _, ok := document.Frontmatter.SemanticGet("timestamp"); ok && fallback.TimestampAllowed {
			candidates = append(candidates, MigrationLegacyCandidate{Kind: "timestamp", Path: path})
		}
		if fallback.SourcesPresent {
			continue
		}
		ownership, err := markdownowner.CollectMarkdownMigrationOwnership(ctx, source)
		if err != nil {
			var ownershipErr *markdownowner.OwnershipError
			if errors.As(err, &ownershipErr) {
				candidates = append(candidates, MigrationLegacyCandidate{
					Kind:     "citations",
					Path:     path,
					Location: mutationSourceSpan(ownershipErr.Span),
				})
				continue
			}
			causes = append(causes, fmt.Errorf("probe migration candidate %q: %w", path, err))
			continue
		}
		if legacyCitationsActive(ownership.CitationsSectionIndex) {
			section := ownership.Sections[ownership.CitationsSectionIndex]
			candidates = append(candidates, MigrationLegacyCandidate{
				Kind:     "citations",
				Path:     path,
				Location: mutationSourceSpan(section.Span),
			})
		}
	}
	if err := sortSliceContext(ctx, candidates, func(left, right MigrationLegacyCandidate) bool {
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Location.Start != right.Location.Start {
			return left.Location.Start < right.Location.Start
		}
		return left.Location.End < right.Location.End
	}); err != nil {
		return nil, err
	}
	return candidates, errors.Join(causes...)
}

func migrationMarkdownPathsContext(ctx context.Context, loaded *bundle.Bundle) ([]string, error) {
	if loaded == nil {
		return nil, nil
	}
	files, err := loaded.MarkdownFilesContext(ctx)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if migrationpath.IsMarkdownDocument(path) {
			paths = append(paths, path)
		}
	}
	if err := sortSliceContext(ctx, paths, func(left, right string) bool { return left < right }); err != nil {
		return nil, err
	}
	return paths, ctx.Err()
}
