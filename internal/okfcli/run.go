package okfcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/graph"
	"github.com/skosovsky/okf/validator"
)

const usage = `okf - Open Knowledge Format toolkit

okf is a command-line toolkit for building, validating, analyzing, and exporting Open Knowledge Format (OKF) bundles.

USAGE:
    okf <command> [args]

COMMANDS:
    validate             Check a bundle against version-aware OKF conformance
    info     <bundle>    Summarize version, trust, lifecycle, provenance, and topology
    index    <bundle>    (Re)generate every index.md in the bundle
    graph    <bundle>    Export a selected graph projection (--format text|dot|mermaid|json-ld|ntriples)
    parse    <file>      Parse one concept document and print its typed projection
    fmt      <file>      Normalize a document by parse + re-serialize (-w writes)
    migrate  <bundle>    Preview/apply transactional OKF v0.1 to v0.2 migration
    version              Show CLI and supported/default OKF versions

OPTIONS:
    --spec auto|0.1|0.2 Select/assert the OKF contract (default auto)
    --profile PROFILE    Graph profile: legacy-v0.1 or skosovsky/okf-v0.2
                         (default follows the effective OKF version)
    --as-of YYYY-MM-DD   Reference date for staleness-aware commands
    --citation-mappings FILE
                         Strict JSON mappings for v0.1 -> v0.2 migration.
                         Reserved index/log replay requires exact legacy_entry
                         or complete title/resource evidence
    -h, --help           Show this help
    -V, --version        Show version`

// Run dispatches one okf CLI invocation and returns its process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, productionRunDependencies())
}

type indexRegenerator func(bundleRoot, selector string) ([]string, error)

type bundleLoader func(root string) (*bundle.Bundle, error)

type bundleValidator func(context.Context, *bundle.Bundle, *validator.ValidatorConfig) (validator.Report, error)

type documentSession interface {
	Document() bundle.Document
	VersionResolution() bundle.VersionResolution
	RewriteContext(context.Context, bundle.Document) error
	Close() error
}

type documentSessionOpener func(context.Context, string, string) (documentSession, error)

type migrateCommand func([]string, io.Writer) (int, error)

type runDependencies struct {
	regenerateIndexes indexRegenerator
	loadBundle        bundleLoader
	validateBundle    bundleValidator
	openDocument      documentSessionOpener
	migrate           migrateCommand
}

func productionRunDependencies() runDependencies {
	return runDependencies{
		regenerateIndexes: bundle.RegenerateIndexesWithSelector,
		loadBundle:        bundle.LoadBundle,
		validateBundle:    validator.ValidateBundleContext,
		openDocument: func(ctx context.Context, path, selector string) (documentSession, error) {
			return bundle.OpenDocumentSessionContext(ctx, path, selector)
		},
		migrate: cmdMigrate,
	}
}

func runWithIndexRegenerator(
	args []string,
	stdout, stderr io.Writer,
	regenerateIndexes indexRegenerator,
) int {
	dependencies := productionRunDependencies()
	dependencies.regenerateIndexes = regenerateIndexes
	return runWithDependencies(args, stdout, stderr, dependencies)
}

func runWithDependencies(
	args []string,
	stdout, stderr io.Writer,
	dependencies runDependencies,
) int {
	if len(args) == 0 {
		writeCLIError(stderr, "missing command")
		return 1
	}
	checkedStdout := &checkedWriter{writer: stdout}
	stdout = checkedStdout

	cmd := args[0]
	rest := args[1:]

	var (
		code int
		err  error
	)
	switch cmd {
	case "validate":
		code, err = cmdValidate(
			rest,
			stdout,
			dependencies.loadBundle,
			dependencies.validateBundle,
		)
	case "info":
		code, err = cmdInfo(rest, stdout)
	case "index":
		code, err = cmdIndex(rest, stdout, dependencies.regenerateIndexes)
	case "graph":
		code, err = cmdGraph(rest, stdout)
	case "parse":
		code, err = cmdParseWithDocumentSession(rest, stdout, dependencies.openDocument)
	case "fmt":
		code, err = cmdFmtWithDocumentSession(rest, stdout, dependencies.openDocument)
	case "migrate":
		code, err = dependencies.migrate(rest, stdout)
	case "-h", "--help", "help":
		code, err = cmdHelp(rest, stdout)
	case "-V", "--version", "version":
		code, err = cmdVersion(rest, stdout)
	default:
		writeCLIError(stderr, fmt.Sprintf("unknown subcommand: %s", cmd))
		return 1
	}

	if err != nil {
		writeCLIError(stderr, err.Error())
		return 1
	}
	if err := checkedStdout.Err(); err != nil {
		writeCLIError(stderr, err.Error())
		return 1
	}
	return code
}

func writeCLIError(stderr io.Writer, message string) {
	fmt.Fprintf(stderr, "error: %s\n", renderTextString(message))
}

func cmdHelp(args []string, stdout io.Writer) (int, error) {
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return 0, err
	}
	if len(parsed.positionals) != 0 {
		return 0, fmt.Errorf("unexpected help argument: %s", parsed.positionals[0])
	}
	fmt.Fprintln(stdout, usage)
	return 0, nil
}

// VersionInfo is the stable machine-readable CLI version contract.
type VersionInfo struct {
	CLIVersion       string   `json:"cli_version"`
	OKFSpecDefault   string   `json:"okf_spec_default"`
	OKFSpecSupported []string `json:"okf_spec_supported"`
}

func cmdVersion(args []string, stdout io.Writer) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--json", Kind: boolFlag},
	})
	if err != nil {
		return 0, err
	}
	if len(parsed.positionals) != 0 {
		return 0, fmt.Errorf("unexpected version argument: %s", parsed.positionals[0])
	}
	info := VersionInfo{
		CLIVersion:       cliVersion(),
		OKFSpecDefault:   bundle.OKFVersion,
		OKFSpecSupported: []string{bundle.LegacyOKFVersion, bundle.OKFVersion},
	}
	if parsed.boolValue("--json") {
		return 0, json.NewEncoder(stdout).Encode(info)
	}
	fmt.Fprintf(stdout, "okf %s\n", info.CLIVersion)
	fmt.Fprintf(stdout, "OKF spec default: v%s\n", info.OKFSpecDefault)
	fmt.Fprintf(stdout, "OKF specs supported: v%s\n", strings.Join(info.OKFSpecSupported, ", v"))
	fmt.Fprintf(stdout, "OKF spec v%s remains supported for legacy reads and migration.\n", bundle.LegacyOKFVersion)
	return 0, nil
}

func cmdValidate(
	args []string,
	stdout io.Writer,
	loadBundle bundleLoader,
	validateBundle bundleValidator,
) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--path", Kind: stringFlag},
		{Name: "--strict", Kind: boolFlag},
		{Name: "--check-links", Kind: boolFlag},
		{Name: "--check-orphans", Kind: boolFlag},
		{Name: "--format", Kind: stringFlag},
		{Name: "--json", Kind: boolFlag},
		{Name: "--spec", Kind: stringFlag},
		{Name: "--as-of", Kind: stringFlag},
	})
	if err != nil {
		return 0, err
	}
	path := parsed.value("--path", ".")
	if len(parsed.positionals) != 0 {
		return 0, fmt.Errorf("unexpected validate argument: %s", parsed.positionals[0])
	}
	format := parsed.value("--format", "text")
	if parsed.boolValue("--json") {
		if parsed.has("--format") && format != "json" {
			return 0, fmt.Errorf("cannot use --json with --format=%s", format)
		}
		format = "json"
	}
	if format != "text" && format != "json" {
		return 0, fmt.Errorf("unsupported validate format: %s", format)
	}
	spec, err := parseSpecSelector(parsed.value("--spec", "auto"))
	if err != nil {
		return 0, err
	}
	asOf, err := parseReferenceDate(parsed.value("--as-of", ""))
	if err != nil {
		return 0, err
	}

	cfg := validator.ValidatorConfig{
		Strict:       parsed.boolValue("--strict"),
		CheckLinks:   parsed.boolValue("--check-links"),
		CheckOrphans: parsed.boolValue("--check-orphans"),
		Spec:         spec,
	}
	if asOf != nil {
		cfg.ReferenceDate = *asOf
	}
	loaded, err := loadBundle(path)
	if err != nil {
		return 0, err
	}
	report, err := validateBundle(context.Background(), loaded, &cfg)
	if err != nil {
		return 0, err
	}
	if format == "json" {
		return report.ExitCode(), writeValidationJSON(stdout, report)
	}

	fmt.Fprintf(stdout, "Validating bundle: %s\n", renderTextContent(path))
	fmt.Fprintf(stdout, "OKF version: declared=%s effective=%s resolution=%s compatibility=%s\n\n",
		displayAbsent(report.Version.Declared), report.Version.Effective, report.Version.Source, report.Version.Compatibility)
	for _, diagnostic := range report.Diagnostics {
		fmt.Fprintln(stdout, renderTextContent(diagnostic.String()))
	}
	errors := report.ErrorCount()
	warnings := report.WarningCount()
	infos := report.InfoCount()
	policyFailures := report.PolicyFailureCount()
	result := "PASS"
	if report.ExitCode() != 0 {
		result = "FAIL"
	}
	fmt.Fprintf(stdout, "\n---\n")
	fmt.Fprintf(stdout, "Scanned %d files.\n", report.ScannedFiles)
	fmt.Fprintf(stdout, "Result: %s (%d errors, %d warnings, %d info)\n", result, errors, warnings, infos)
	fmt.Fprintf(stdout, "Policy failures: %d\n", policyFailures)
	return report.ExitCode(), nil
}

// ValidationJSONDiagnostic is the stable JSON projection of a validator diagnostic.
type ValidationJSONDiagnostic struct {
	Code          string   `json:"code,omitempty"`
	SpecRef       string   `json:"spec_ref,omitempty"`
	Severity      string   `json:"severity"`
	File          string   `json:"file,omitempty"`
	FieldPath     string   `json:"field_path,omitempty"`
	Message       string   `json:"message"`
	Source        string   `json:"source,omitempty"`
	RelationType  string   `json:"relation_type,omitempty"`
	RawTarget     string   `json:"raw_target,omitempty"`
	Refs          []string `json:"refs,omitempty"`
	PolicyFailure bool     `json:"policy_failure,omitempty"`
}

func writeValidationJSON(stdout io.Writer, report validator.Report) error {
	diagnostics := make([]ValidationJSONDiagnostic, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		refs := make([]string, len(diagnostic.Refs))
		for i, ref := range diagnostic.Refs {
			refs[i] = ref.String()
		}
		diagnostics = append(diagnostics, ValidationJSONDiagnostic{
			Code: diagnostic.Code, SpecRef: diagnostic.SpecRef, Severity: diagnostic.Severity.String(), File: diagnostic.File,
			FieldPath: diagnostic.FieldPath,
			Message:   diagnostic.Message, Source: diagnostic.Source.String(), RelationType: diagnostic.RelationType,
			RawTarget: diagnostic.RawTarget, Refs: refs, PolicyFailure: diagnostic.PolicyFailure,
		})
	}
	return json.NewEncoder(stdout).Encode(struct {
		VersionDTO
		ScannedFiles   int                        `json:"scanned_files"`
		Conformant     bool                       `json:"conformant"`
		PolicyFailures int                        `json:"policy_failures"`
		Errors         int                        `json:"errors"`
		Warnings       int                        `json:"warnings"`
		Info           int                        `json:"info"`
		Diagnostics    []ValidationJSONDiagnostic `json:"diagnostics"`
	}{
		VersionDTO:     projectVersion(report.Version),
		ScannedFiles:   report.ScannedFiles,
		Conformant:     report.IsConformant(),
		PolicyFailures: report.PolicyFailureCount(),
		Errors:         report.ErrorCount(),
		Warnings:       report.WarningCount(),
		Info:           report.InfoCount(),
		Diagnostics:    diagnostics,
	})
}

func cmdInfo(args []string, stdout io.Writer) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--spec", Kind: stringFlag},
		{Name: "--as-of", Kind: stringFlag},
		{Name: "--format", Kind: stringFlag},
		{Name: "--json", Kind: boolFlag},
	})
	if err != nil {
		return 0, err
	}
	path, err := parsed.onePositional("<bundle>")
	if err != nil {
		return 0, infoArgumentError(err)
	}
	spec, err := parseSpecSelector(parsed.value("--spec", "auto"))
	if err != nil {
		return 0, err
	}
	asOf, err := parseReferenceDate(parsed.value("--as-of", ""))
	if err != nil {
		return 0, err
	}
	format := parsed.value("--format", "text")
	if parsed.boolValue("--json") {
		if parsed.has("--format") && format != "json" {
			return 0, fmt.Errorf("cannot use --json with --format=%s", format)
		}
		format = "json"
	}
	if format != "text" && format != "json" {
		return 0, fmt.Errorf("unsupported info format: %s", format)
	}
	b, err := bundle.LoadBundle(path)
	if err != nil {
		return 0, err
	}
	resolution, err := resolveBundleVersion(b, spec)
	if err != nil {
		return 0, err
	}

	byType := make(map[string]int)
	trust := trustCounts{}
	lifecycle := lifecycleCounts{}
	stale := 0
	sources := 0
	computations := 0
	for _, concept := range b.Concepts() {
		typ, ok := concept.Document.Frontmatter.Type()
		if !ok {
			typ = "(none)"
		}
		byType[typ]++
		trust.add(concept.Document.TrustTier())
		lifecycle.add(concept.Document.StatusState())
		sources += len(concept.Document.Sources())
		if typ == "Attested Computation" {
			computations++
		}
		if asOf != nil && concept.Document.IsStale(*asOf) {
			stale++
		}
	}

	totalLinks := 0
	for _, concept := range b.Concepts() {
		totalLinks += len(b.LinksFrom(concept.ID))
	}
	parseErrors := b.ParseErrors()

	response := infoResponse{
		VersionDTO:           projectVersion(resolution),
		Bundle:               b.Root(),
		Concepts:             b.Len(),
		IndexFiles:           len(b.IndexFiles()),
		LogFiles:             len(b.LogFiles()),
		Types:                byType,
		InternalLinks:        totalLinks,
		BrokenLinks:          len(b.BrokenLinks()),
		TrustTiers:           trust,
		Lifecycle:            lifecycle,
		Stale:                stale,
		Sources:              sources,
		AttestedComputations: computations,
		ParseErrors:          len(parseErrors),
	}
	if asOf != nil {
		response.AsOf = asOf.Format(time.DateOnly)
	}
	if format == "json" {
		return 0, json.NewEncoder(stdout).Encode(response)
	}
	writeInfoText(stdout, response, parseErrors)
	return 0, nil
}

type infoResponse struct {
	VersionDTO
	Bundle               string          `json:"bundle"`
	Concepts             int             `json:"concepts"`
	IndexFiles           int             `json:"index_files"`
	LogFiles             int             `json:"log_files"`
	Types                map[string]int  `json:"types"`
	InternalLinks        int             `json:"internal_links"`
	BrokenLinks          int             `json:"broken_links"`
	TrustTiers           trustCounts     `json:"trust_tiers"`
	Lifecycle            lifecycleCounts `json:"lifecycle"`
	AsOf                 string          `json:"as_of,omitempty"`
	Stale                int             `json:"stale"`
	Sources              int             `json:"sources"`
	AttestedComputations int             `json:"attested_computations"`
	ParseErrors          int             `json:"parse_errors"`
}

func infoArgumentError(err error) error {
	if strings.HasPrefix(err.Error(), "unexpected argument:") {
		return fmt.Errorf("%s", strings.Replace(err.Error(), "unexpected argument:", "unexpected info argument:", 1))
	}
	return err
}

func writeInfoText(stdout io.Writer, response infoResponse, parseErrors []bundle.ParseError) {
	fmt.Fprintf(stdout, "bundle:     %s\n", renderTextString(response.Bundle))
	if response.VersionDTO.Declared != "" {
		fmt.Fprintf(stdout, "okf_version: %s\n", renderTextString(response.VersionDTO.Declared))
	} else {
		fmt.Fprintln(stdout, "okf_version: (absent)")
	}
	fmt.Fprintf(stdout, "effective:  %s (%s, %s)\n",
		renderTextString(response.VersionDTO.Effective),
		renderTextString(response.VersionDTO.Source),
		renderTextString(response.VersionDTO.Compatibility))
	fmt.Fprintf(stdout, "concepts:   %d\n", response.Concepts)
	fmt.Fprintf(stdout, "index.md:   %d\n", response.IndexFiles)
	fmt.Fprintf(stdout, "log.md:     %d\n", response.LogFiles)

	if len(response.Types) > 0 {
		fmt.Fprintln(stdout, "\ntypes:")
		for _, typ := range sortedKeys(response.Types) {
			fmt.Fprintf(stdout, "  %4d  %s\n", response.Types[typ], renderTextString(typ))
		}
	}
	fmt.Fprintf(stdout, "\nlinks:      %d internal (%d broken)\n", response.InternalLinks, response.BrokenLinks)
	fmt.Fprintln(stdout, "trust tiers:")
	fmt.Fprintf(stdout, "  unverified:        %d\n", response.TrustTiers.Unverified)
	fmt.Fprintf(stdout, "  machine-confirmed: %d\n", response.TrustTiers.MachineConfirmed)
	fmt.Fprintf(stdout, "  human-reviewed:    %d\n", response.TrustTiers.HumanReviewed)
	fmt.Fprintln(stdout, "lifecycle:")
	fmt.Fprintf(stdout, "  draft:      %d\n", response.Lifecycle.Draft)
	fmt.Fprintf(stdout, "  stable:     %d\n", response.Lifecycle.Stable)
	fmt.Fprintf(stdout, "  deprecated: %d\n", response.Lifecycle.Deprecated)
	fmt.Fprintf(stdout, "  invalid:    %d\n", response.Lifecycle.Invalid)
	for _, status := range sortedKeys(response.Lifecycle.Other) {
		fmt.Fprintf(stdout, "  %s: %d\n", renderTextString(status), response.Lifecycle.Other[status])
	}
	if response.AsOf != "" {
		fmt.Fprintf(stdout, "stale (%s):  %d\n", renderTextString(response.AsOf), response.Stale)
	} else {
		fmt.Fprintln(stdout, "stale:       not evaluated (use --as-of)")
	}
	fmt.Fprintf(stdout, "sources:     %d\n", response.Sources)
	fmt.Fprintf(stdout, "attested computations: %d\n", response.AttestedComputations)
	fmt.Fprintf(stdout, "parse errors: %d\n", response.ParseErrors)

	if len(parseErrors) > 0 {
		fmt.Fprintln(stdout, "\nunparseable files:")
		for _, parseError := range parseErrors {
			errorText := ""
			if parseError.Err != nil {
				errorText = parseError.Err.Error()
			}
			fmt.Fprintf(stdout, "  %s: %s\n",
				renderTextString(parseError.Path),
				renderTextString(errorText))
		}
	}
}

func cmdIndex(
	args []string,
	stdout io.Writer,
	regenerateIndexes indexRegenerator,
) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--spec", Kind: stringFlag},
	})
	if err != nil {
		return 0, err
	}
	path, err := parsed.onePositional("<bundle>")
	if err != nil {
		return 0, namedArgumentError("index", err)
	}
	spec, err := parseSpecSelector(parsed.value("--spec", "auto"))
	if err != nil {
		return 0, err
	}
	written, err := regenerateIndexes(path, selectorAssertion(spec))
	if err != nil {
		return 0, err
	}
	if len(written) == 0 {
		fmt.Fprintln(stdout, "no index files written (empty bundle?)")
		return 0, nil
	}
	for _, path := range written {
		fmt.Fprintf(stdout, "wrote %s\n", renderTextString(path))
	}
	fmt.Fprintf(stdout, "\n%d index file(s) regenerated.\n", len(written))
	return 0, nil
}

func cmdGraph(args []string, stdout io.Writer) (int, error) {
	opts, err := parseGraphArgs(args)
	if err != nil {
		return 0, err
	}
	b, err := bundle.LoadBundle(opts.path)
	if err != nil {
		return 0, err
	}
	resolution, err := resolveBundleVersion(b, opts.spec)
	if err != nil {
		return 0, err
	}
	profile := opts.profile
	if profile == "" {
		if resolution.Effective == bundle.LegacyOKFVersion {
			profile = graph.ProjectionProfileLegacyV01
		} else {
			profile = graph.ProjectionProfileToolkitV02
		}
	}
	graphOptions := graph.Options{
		Profile:            profile,
		VersionSelector:    selectorAssertion(opts.spec),
		AsOf:               opts.asOf,
		ExtensionRelations: opts.extensionRelations,
		AnnotateTopology:   opts.annotateTopology,
	}

	switch opts.format {
	case graphFormatText:
		if err := graph.RenderTextWithOptions(stdout, b, graphOptions); err != nil {
			return 0, err
		}
	case graphFormatDOT:
		if err := graph.RenderDOTWithOptions(stdout, b, graphOptions); err != nil {
			return 0, err
		}
	case graphFormatMermaid:
		if err := graph.RenderMermaidWithOptions(stdout, b, graphOptions); err != nil {
			return 0, err
		}
	case graphFormatJSONLD:
		if err := graph.RenderJSONLDWithOptions(stdout, b, graphOptions); err != nil {
			return 0, err
		}
	case graphFormatNTriples:
		if err := graph.RenderNTriplesWithOptions(stdout, b, graphOptions); err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("unsupported graph format: %s", opts.format)
	}
	return 0, nil
}

type graphFormat string

const (
	graphFormatText     graphFormat = "text"
	graphFormatDOT      graphFormat = "dot"
	graphFormatMermaid  graphFormat = "mermaid"
	graphFormatJSONLD   graphFormat = "json-ld"
	graphFormatNTriples graphFormat = "ntriples"
)

type graphOptions struct {
	path               string
	format             graphFormat
	profile            graph.ProjectionProfile
	spec               string
	asOf               *time.Time
	extensionRelations graph.ExtensionRelationPolicy
	annotateTopology   bool
}

func parseGraphArgs(args []string) (graphOptions, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--format", Kind: stringFlag},
		{Name: "--dot", Kind: boolFlag},
		{Name: "--profile", Kind: stringFlag},
		{Name: "--spec", Kind: stringFlag},
		{Name: "--as-of", Kind: stringFlag},
		{Name: "--extension-relations", Kind: stringFlag},
		{Name: "--annotate-topology", Kind: boolFlag},
	})
	if err != nil {
		return graphOptions{}, fmt.Errorf("%s", strings.Replace(err.Error(), "unknown flag:", "unknown graph flag:", 1))
	}
	path, err := parsed.onePositional("<bundle>")
	if err != nil {
		if strings.HasPrefix(err.Error(), "unexpected argument:") {
			return graphOptions{}, fmt.Errorf("%s", strings.Replace(err.Error(), "unexpected argument:", "unexpected graph argument:", 1))
		}
		return graphOptions{}, err
	}
	opts := graphOptions{
		path:               path,
		format:             graphFormat(parsed.value("--format", string(graphFormatText))),
		extensionRelations: graph.ExtensionRelationsDefault,
		annotateTopology:   parsed.boolValue("--annotate-topology"),
	}
	spec, err := parseSpecSelector(parsed.value("--spec", "auto"))
	if err != nil {
		return graphOptions{}, err
	}
	opts.spec = spec

	if parsed.boolValue("--dot") {
		if parsed.has("--format") && opts.format != graphFormatDOT {
			return graphOptions{}, fmt.Errorf("cannot use --dot with --format=%s", opts.format)
		}
		opts.format = graphFormatDOT
	}

	switch opts.format {
	case graphFormatText, graphFormatDOT, graphFormatMermaid, graphFormatJSONLD, graphFormatNTriples:
	default:
		return graphOptions{}, fmt.Errorf("unsupported graph format: %s", opts.format)
	}

	if raw := parsed.value("--profile", ""); raw != "" {
		switch raw {
		case string(graph.ProjectionProfileLegacyV01):
			opts.profile = graph.ProjectionProfileLegacyV01
		case string(graph.ProjectionProfileToolkitV02):
			opts.profile = graph.ProjectionProfileToolkitV02
		default:
			return graphOptions{}, fmt.Errorf("unsupported graph profile: %s", raw)
		}
	}
	if raw := parsed.value("--extension-relations", ""); raw != "" {
		switch raw {
		case "include":
			opts.extensionRelations = graph.ExtensionRelationsInclude
		case "exclude":
			opts.extensionRelations = graph.ExtensionRelationsExclude
		default:
			return graphOptions{}, fmt.Errorf("unsupported extension relation policy: %s", raw)
		}
	}
	if raw := parsed.value("--as-of", ""); raw != "" {
		date, err := time.Parse(time.DateOnly, raw)
		if err != nil {
			return graphOptions{}, fmt.Errorf("invalid --as-of date %q (want YYYY-MM-DD)", raw)
		}
		opts.asOf = &date
	}
	return opts, nil
}

func cmdParse(args []string, stdout io.Writer) (int, error) {
	return cmdParseWithDocumentSession(args, stdout, productionRunDependencies().openDocument)
}

type documentProjector func(
	context.Context,
	string,
	bundle.Document,
	bundle.VersionResolution,
	*time.Time,
) (parseResponse, error)

func cmdParseWithDocumentSession(
	args []string,
	stdout io.Writer,
	openDocument documentSessionOpener,
) (int, error) {
	return cmdParseWithDocumentSessionAndProjector(args, stdout, openDocument, projectDocument)
}

func cmdParseWithDocumentSessionAndProjector(
	args []string,
	stdout io.Writer,
	openDocument documentSessionOpener,
	project documentProjector,
) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--spec", Kind: stringFlag},
		{Name: "--as-of", Kind: stringFlag},
		{Name: "--format", Kind: stringFlag},
		{Name: "--json", Kind: boolFlag},
	})
	if err != nil {
		return 0, err
	}
	path, err := parsed.onePositional("<file>")
	if err != nil {
		return 0, namedArgumentError("parse", err)
	}
	spec, err := parseSpecSelector(parsed.value("--spec", "auto"))
	if err != nil {
		return 0, err
	}
	asOf, err := parseReferenceDate(parsed.value("--as-of", ""))
	if err != nil {
		return 0, err
	}
	format := parsed.value("--format", "text")
	if parsed.boolValue("--json") {
		if parsed.has("--format") && format != "json" {
			return 0, fmt.Errorf("cannot use --json with --format=%s", format)
		}
		format = "json"
	}
	if format != "text" && format != "json" {
		return 0, fmt.Errorf("unsupported parse format: %s", format)
	}
	ctx := context.Background()
	session, err := openDocument(ctx, path, selectorAssertion(spec))
	if err != nil {
		return 0, err
	}
	projection, err := project(ctx, path, session.Document(), session.VersionResolution(), asOf)
	if err != nil {
		return 0, errors.Join(err, session.Close())
	}

	var output bytes.Buffer
	if format == "json" {
		if err := json.NewEncoder(&output).Encode(projection); err != nil {
			return 0, errors.Join(err, session.Close())
		}
	} else {
		writeParseText(&output, projection)
	}
	if err := session.Close(); err != nil {
		return 0, err
	}
	if _, err := io.Copy(stdout, &output); err != nil {
		return 0, err
	}

	if projection.Conformant {
		return 0, nil
	}
	return 1, nil
}

func writeParseText(stdout io.Writer, projection parseResponse) {
	fmt.Fprintf(stdout, "file: %s\n", renderTextString(projection.File))
	fmt.Fprintf(stdout, "declared version: %s\n", renderOptionalTextString(projection.VersionDTO.Declared))
	fmt.Fprintf(stdout, "effective version: %s (%s, %s)\n",
		renderTextString(projection.VersionDTO.Effective),
		renderTextString(projection.VersionDTO.Source),
		renderTextString(projection.VersionDTO.Compatibility))
	fmt.Fprintf(stdout, "type: %s\n", renderOptionalTextString(projection.Type))
	if projection.Title != "" {
		fmt.Fprintf(stdout, "title: %s\n", renderTextString(projection.Title))
	}
	if projection.Description != "" {
		fmt.Fprintf(stdout, "description: %s\n", renderTextString(projection.Description))
	}
	fmt.Fprintf(stdout, "has non-empty string `type`: %t\n", projection.Conformant)
	fmt.Fprintf(stdout, "body: %d byte(s)\n", projection.BodyBytes)
	if projection.Generated != nil {
		fmt.Fprintf(stdout, "generated: by=%s at=%s\n",
			renderTextString(projection.Generated.By),
			renderTextString(projection.Generated.At))
	} else if projection.legacyTimestampFallback {
		fmt.Fprintf(stdout, "generated: legacy timestamp fallback at=%s\n",
			renderTextString(projection.EffectiveContentChangeTime))
	}
	if projection.EffectiveContentChangeTime != "" {
		fmt.Fprintf(stdout, "effective content change time: %s\n",
			renderTextString(projection.EffectiveContentChangeTime))
	}
	writeGenerationAuditText(stdout, projection.GeneratedAudit)
	fmt.Fprintf(stdout, "legacy fallback: %t\n", projection.LegacyFallback)
	fmt.Fprintf(stdout, "trust: %s (%d verification(s))\n",
		renderTextString(projection.TrustTier), len(projection.Verified))
	fmt.Fprintf(stdout, "verified (%d):\n", len(projection.Verified))
	for index, verification := range projection.Verified {
		fmt.Fprintf(stdout, "  [%d] by=%s at=%s\n", index,
			renderTextString(verification.By), renderTextString(verification.At))
	}
	writeVerificationsAuditText(stdout, projection.VerifiedAudit)
	if projection.StatusValid {
		fmt.Fprintf(stdout, "status: %s\n", renderTextString(projection.Status))
	} else {
		fmt.Fprintf(stdout, "status: invalid raw=%s\n", renderTextString(projection.StatusRaw))
	}
	fmt.Fprintf(stdout, "status metadata: present=%t valid=%t raw=%s\n",
		projection.StatusPresent, projection.StatusValid, renderTextString(projection.StatusRaw))
	if projection.StaleAfter != "" {
		fmt.Fprintf(stdout, "stale_after: %s\n", renderTextString(projection.StaleAfter))
	}
	if projection.Stale != nil {
		fmt.Fprintf(stdout, "stale at %s: %t\n", renderTextString(projection.AsOf), *projection.Stale)
	}
	writeLifecycleAuditText(stdout, projection.LifecycleAudit)
	if projection.UsageWindow != nil {
		fmt.Fprintf(stdout, "usage window: from=%s to=%s\n",
			renderTextString(projection.UsageWindow.From),
			renderTextString(projection.UsageWindow.To))
	}
	writeUsageWindowAuditText(stdout, "usage window audit", "", projection.UsageWindowAudit)
	fmt.Fprintf(stdout, "sources: %d\n", len(projection.Sources))
	for index, source := range projection.Sources {
		writeParseSourceText(stdout, "  ", index, source)
	}
	writeSourcesAuditText(stdout, projection.SourcesAudit)
	fmt.Fprintf(stdout, "attributions: %d\n", len(projection.Attributions))
	for index, attribution := range projection.Attributions {
		if attribution.NormalizedID == "" {
			fmt.Fprintf(stdout, "  [%d] id=%s\n", index, renderTextString(attribution.ID))
		} else {
			fmt.Fprintf(stdout, "  [%d] id=%s normalized_id=%s\n",
				index,
				renderTextString(attribution.ID),
				renderTextString(attribution.NormalizedID))
		}
		fmt.Fprintf(stdout, "    sources: %d\n", len(attribution.Sources))
		for sourceIndex, source := range attribution.Sources {
			writeParseSourceText(stdout, "      ", sourceIndex, source)
		}
		fmt.Fprintf(stdout, "    references: %d\n", len(attribution.References))
		for referenceIndex, reference := range attribution.References {
			fmt.Fprintf(stdout, "      [%d] id=%s raw=%s\n",
				referenceIndex, renderTextString(reference.ID), renderTextString(reference.Raw))
		}
		fmt.Fprintf(stdout, "    definitions: %d\n", len(attribution.Definitions))
		for definitionIndex, definition := range attribution.Definitions {
			fmt.Fprintf(stdout, "      [%d] id=%s text=%s raw=%s\n",
				definitionIndex,
				renderTextString(definition.ID),
				renderTextString(definition.Text),
				renderTextString(definition.Raw))
		}
	}
	writeAttributionAuditText(stdout, projection.AttributionsAudit)
	if projection.Computation != nil {
		fmt.Fprintf(stdout, "attested computation: runtime=%s parameters=%d computation=%s\n",
			renderTextString(projection.Computation.Runtime),
			len(projection.Computation.Parameters),
			renderTextString(projection.Computation.Computation))
		fmt.Fprintf(stdout, "  parameters (%d):\n", len(projection.Computation.Parameters))
		for index, parameter := range projection.Computation.Parameters {
			fmt.Fprintf(stdout, "    [%d] name=%s type=%s required=%t\n",
				index,
				renderTextString(parameter.Name),
				renderTextString(parameter.Type),
				parameter.Required)
		}
		if projection.Computation.Executor != nil {
			fmt.Fprintf(stdout, "  executor: resource=%s\n",
				renderTextString(projection.Computation.Executor.Resource))
			fmt.Fprintf(stdout, "    receipt (%d):\n", len(projection.Computation.Executor.Receipt))
			for index, field := range projection.Computation.Executor.Receipt {
				fmt.Fprintf(stdout, "      [%d] %s\n", index, renderTextString(field))
			}
		}
		if projection.Computation.Attester != nil {
			fmt.Fprintf(stdout, "  attester: resource=%s\n",
				renderTextString(projection.Computation.Attester.Resource))
		}
	}
	writeComputationAuditText(stdout, projection.ComputationAudit)

	if len(projection.Links) > 0 {
		fmt.Fprintf(stdout, "\nlinks (%d):\n", len(projection.Links))
		for _, link := range projection.Links {
			fmt.Fprintf(stdout, "  kind=%s text=%s target=%s\n",
				renderTextString(titleCaseASCII(link.Kind)),
				renderTextString(link.Text),
				renderTextString(link.Target))
		}
	}

	if len(projection.LegacyCitations) > 0 {
		fmt.Fprintf(stdout, "\ncitations (%d):\n", len(projection.LegacyCitations))
		for _, citation := range projection.LegacyCitations {
			fmt.Fprintf(stdout, "  [%d] text=%s target=%s raw=%s\n",
				citation.Number,
				renderTextString(citation.Text),
				renderTextString(citation.Target),
				renderTextString(citation.Raw))
		}
	}
}

func writeParseSourceText(stdout io.Writer, indent string, index int, source sourceDTO) {
	fmt.Fprintf(stdout, "%s[%d] id=%s resource=%s title=%s author=%s\n",
		indent,
		index,
		renderTextString(source.ID),
		renderTextString(source.Resource),
		renderTextString(source.Title),
		renderTextString(source.Author))
	if source.UsageCount != nil {
		fmt.Fprintf(stdout, "%s  usage_count: %d\n", indent, *source.UsageCount)
	}
	if source.LastModified != "" {
		fmt.Fprintf(stdout, "%s  last_modified: %s\n", indent, renderTextString(source.LastModified))
	}
	if source.UsageWindow != nil {
		fmt.Fprintf(stdout, "%s  usage_window: from=%s to=%s\n",
			indent,
			renderTextString(source.UsageWindow.From),
			renderTextString(source.UsageWindow.To))
	}
	if source.EffectiveUsageWindow != nil {
		fmt.Fprintf(stdout, "%s  effective_usage_window: from=%s to=%s\n",
			indent,
			renderTextString(source.EffectiveUsageWindow.From),
			renderTextString(source.EffectiveUsageWindow.To))
	}
}

type parseResponse struct {
	File string `json:"file"`
	VersionDTO
	Type                       string                `json:"type,omitempty"`
	Title                      string                `json:"title,omitempty"`
	Description                string                `json:"description,omitempty"`
	Generated                  *generationDTO        `json:"generated,omitempty"`
	GeneratedAudit             generationAuditDTO    `json:"generated_audit"`
	EffectiveContentChangeTime string                `json:"effective_content_change_time,omitempty"`
	LegacyFallback             bool                  `json:"legacy_fallback"`
	Verified                   []verificationDTO     `json:"verified"`
	VerifiedAudit              verificationsAuditDTO `json:"verified_audit"`
	TrustTier                  string                `json:"trust_tier"`
	Status                     string                `json:"status"`
	StatusRaw                  string                `json:"status_raw,omitempty"`
	StatusPresent              bool                  `json:"status_present"`
	StatusValid                bool                  `json:"status_valid"`
	StaleAfter                 string                `json:"stale_after,omitempty"`
	AsOf                       string                `json:"as_of,omitempty"`
	Stale                      *bool                 `json:"stale,omitempty"`
	Sources                    []sourceDTO           `json:"sources"`
	SourcesAudit               sourcesAuditDTO       `json:"sources_audit"`
	UsageWindow                *usageWindowDTO       `json:"usage_window,omitempty"`
	UsageWindowAudit           usageWindowAuditDTO   `json:"usage_window_audit"`
	Attributions               []attributionDTO      `json:"attributions"`
	AttributionsAudit          attributionAuditDTO   `json:"attributions_audit"`
	Computation                *computationDTO       `json:"attested_computation,omitempty"`
	ComputationAudit           computationAuditDTO   `json:"attested_computation_audit"`
	LifecycleAudit             lifecycleAuditDTO     `json:"lifecycle_audit"`
	Conformant                 bool                  `json:"conformant"`
	BodyBytes                  int                   `json:"body_bytes"`
	Links                      []linkDTO             `json:"links"`
	LegacyCitations            []citationDTO         `json:"legacy_citations"`
	legacyTimestampFallback    bool
}

type linkDTO struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Target string `json:"target"`
}

type citationDTO struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
	Target string `json:"target,omitempty"`
	Raw    string `json:"raw"`
}

type attributionDTO struct {
	ID           string                  `json:"id"`
	NormalizedID string                  `json:"normalized_id,omitempty"`
	Sources      []sourceDTO             `json:"sources"`
	References   []footnoteReferenceDTO  `json:"references"`
	Definitions  []footnoteDefinitionDTO `json:"definitions"`
}

type footnoteReferenceDTO struct {
	ID  string `json:"id"`
	Raw string `json:"raw"`
}

type footnoteDefinitionDTO struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Raw  string `json:"raw"`
}

type computationDTO struct {
	Runtime     string         `json:"runtime,omitempty"`
	Parameters  []parameterDTO `json:"parameters"`
	Computation string         `json:"computation,omitempty"`
	Executor    *executorDTO   `json:"executor,omitempty"`
	Attester    *attesterDTO   `json:"attester,omitempty"`
}

type generationDTO struct {
	By string `json:"by,omitempty"`
	At string `json:"at,omitempty"`
}

type verificationDTO struct {
	By string `json:"by,omitempty"`
	At string `json:"at,omitempty"`
}

type usageWindowDTO struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type sourceDTO struct {
	ID                   string          `json:"id,omitempty"`
	Resource             string          `json:"resource,omitempty"`
	Title                string          `json:"title,omitempty"`
	Author               string          `json:"author,omitempty"`
	UsageCount           *uint64         `json:"usage_count,omitempty"`
	LastModified         string          `json:"last_modified,omitempty"`
	UsageWindow          *usageWindowDTO `json:"usage_window,omitempty"`
	EffectiveUsageWindow *usageWindowDTO `json:"effective_usage_window,omitempty"`
}

type parameterDTO struct {
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	Required bool   `json:"required"`
}

type executorDTO struct {
	Resource string   `json:"resource,omitempty"`
	Receipt  []string `json:"receipt"`
}

type attesterDTO struct {
	Resource string `json:"resource,omitempty"`
}

func projectDocument(
	ctx context.Context,
	path string,
	document bundle.Document,
	resolution bundle.VersionResolution,
	asOf *time.Time,
) (parseResponse, error) {
	generatedAudit, err := projectGenerationAudit(ctx, document)
	if err != nil {
		return parseResponse{}, err
	}
	verifiedAudit, err := projectVerificationsAudit(ctx, document)
	if err != nil {
		return parseResponse{}, err
	}
	sourcesAudit, err := projectSourcesAudit(ctx, document)
	if err != nil {
		return parseResponse{}, err
	}
	usageWindowAudit, err := projectDocumentUsageWindowAudit(ctx, document)
	if err != nil {
		return parseResponse{}, err
	}
	lifecycleAudit, err := projectLifecycleAudit(ctx, document)
	if err != nil {
		return parseResponse{}, err
	}
	computationAudit, err := projectComputationAudit(ctx, document)
	if err != nil {
		return parseResponse{}, err
	}
	attributions, err := document.AttributionsContext(ctx)
	if err != nil {
		return parseResponse{}, err
	}
	response := parseResponse{
		File:             path,
		VersionDTO:       projectVersion(resolution),
		TrustTier:        string(document.TrustTier()),
		Conformant:       document.ValidateConformance() == nil,
		BodyBytes:        len([]byte(document.Body)),
		Verified:         projectVerifications(document.Verifications()),
		Sources:          projectDocumentSources(document),
		Attributions:     projectAttributions(attributions),
		GeneratedAudit:   generatedAudit,
		VerifiedAudit:    verifiedAudit,
		SourcesAudit:     sourcesAudit,
		UsageWindowAudit: usageWindowAudit,
		LifecycleAudit:   lifecycleAudit,
		ComputationAudit: computationAudit,
	}
	response.AttributionsAudit, err = projectAttributionAudit(ctx, document, response.Attributions)
	if err != nil {
		return parseResponse{}, err
	}
	response.Type, _ = document.Frontmatter.Type()
	response.Title, _ = document.Frontmatter.Title()
	response.Description, _ = document.Frontmatter.Description()
	status := document.StatusState()
	response.Status = status.Effective
	response.StatusRaw = status.Raw
	response.StatusPresent = status.Present
	response.StatusValid = status.Valid
	generation := document.GenerationState()
	if generation.HasValue {
		response.Generated = &generationDTO{}
		if generation.By.Valid {
			response.Generated.By = generation.By.Value
		}
		if generation.At.State == bundle.TemporalValid {
			response.Generated.At = generation.At.Raw
		}
	}
	fallback := document.LegacyFallbackObservation()
	response.EffectiveContentChangeTime, _ = document.EffectiveContentChangeTime()
	response.LegacyFallback = fallback.TimestampActive || fallback.CitationsActive
	response.legacyTimestampFallback = fallback.TimestampActive
	response.StaleAfter, _ = document.StaleAfter()
	if window, ok := document.UsageWindow(); ok {
		response.UsageWindow = &usageWindowDTO{From: window.From, To: window.To}
	}
	if asOf != nil {
		stale := document.IsStale(*asOf)
		response.Stale = &stale
		response.AsOf = asOf.Format(time.DateOnly)
	}
	if contract, ok := document.AttestedComputation(); ok {
		response.Computation = &computationDTO{
			Runtime:     contract.Runtime,
			Parameters:  projectParameters(contract.Parameters),
			Computation: contract.Computation,
		}
		if contract.Executor != nil {
			response.Computation.Executor = &executorDTO{
				Resource: contract.Executor.Resource,
				Receipt:  nonNilSlice(contract.Executor.Receipt),
			}
		}
		if contract.Attester != nil {
			response.Computation.Attester = &attesterDTO{Resource: contract.Attester.Resource}
		}
	}
	for _, link := range document.Links() {
		response.Links = append(response.Links, linkDTO{Kind: link.Kind.String(), Text: link.Text, Target: link.Target})
	}
	response.Links = nonNilSlice(response.Links)
	if fallback.CitationsActive {
		for _, citation := range document.Citations() {
			response.LegacyCitations = append(response.LegacyCitations, citationDTO{
				Number: citation.Number,
				Text:   citation.Text,
				Target: citation.Target,
				Raw:    citation.Raw,
			})
		}
	}
	response.LegacyCitations = nonNilSlice(response.LegacyCitations)
	if err := ctx.Err(); err != nil {
		return parseResponse{}, err
	}
	return response, nil
}

func projectAttributions(values []bundle.Attribution) []attributionDTO {
	out := make([]attributionDTO, 0, len(values))
	for _, value := range values {
		projected := attributionDTO{
			ID:           value.ID,
			NormalizedID: value.NormalizedID,
			Sources:      projectSources(value.Sources),
		}
		for _, reference := range value.References {
			projected.References = append(projected.References, footnoteReferenceDTO{ID: reference.ID, Raw: reference.Raw})
		}
		projected.References = nonNilSlice(projected.References)
		for _, definition := range value.Definitions {
			projected.Definitions = append(projected.Definitions, footnoteDefinitionDTO{
				ID:   definition.ID,
				Text: definition.Text,
				Raw:  definition.Raw,
			})
		}
		projected.Definitions = nonNilSlice(projected.Definitions)
		out = append(out, projected)
	}
	return nonNilSlice(out)
}

func projectVerifications(values []bundle.Verification) []verificationDTO {
	out := make([]verificationDTO, 0, len(values))
	for _, value := range values {
		out = append(out, verificationDTO{By: value.By, At: value.At})
	}
	return nonNilSlice(out)
}

func projectSources(values []bundle.ProvenanceSource) []sourceDTO {
	out := make([]sourceDTO, 0, len(values))
	for _, value := range values {
		projected := sourceDTO{
			ID:           value.ID,
			Resource:     value.Resource,
			Title:        value.Title,
			Author:       value.Author,
			UsageCount:   value.UsageCount,
			LastModified: value.LastModified,
		}
		if value.UsageWindow != nil {
			projected.UsageWindow = &usageWindowDTO{
				From: value.UsageWindow.From,
				To:   value.UsageWindow.To,
			}
		}
		out = append(out, projected)
	}
	return nonNilSlice(out)
}

func projectDocumentSources(document bundle.Document) []sourceDTO {
	values := document.Sources()
	out := projectSources(values)
	for index, value := range values {
		if window, ok := document.EffectiveUsageWindow(value); ok {
			out[index].EffectiveUsageWindow = &usageWindowDTO{From: window.From, To: window.To}
		}
	}
	return out
}

func projectParameters(values []bundle.ComputationParameter) []parameterDTO {
	out := make([]parameterDTO, 0, len(values))
	for _, value := range values {
		out = append(out, parameterDTO{Name: value.Name, Type: value.Type, Required: value.Required})
	}
	return nonNilSlice(out)
}

func nonNilSlice[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func displayAbsent(value string) string {
	if value == "" {
		return "(absent)"
	}
	return value
}

func namedArgumentError(command string, err error) error {
	if strings.HasPrefix(err.Error(), "unexpected argument:") {
		return fmt.Errorf("%s", strings.Replace(err.Error(), "unexpected argument:", "unexpected "+command+" argument:", 1))
	}
	return err
}

func cmdFmt(args []string, stdout io.Writer) (int, error) {
	return cmdFmtWithDocumentSession(args, stdout, productionRunDependencies().openDocument)
}

func cmdFmtWithDocumentSession(
	args []string,
	stdout io.Writer,
	openDocument documentSessionOpener,
) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--write", Short: "-w", Kind: boolFlag},
		{Name: "--spec", Kind: stringFlag},
	})
	if err != nil {
		return 0, err
	}
	path, err := parsed.onePositional("<file>")
	if err != nil {
		return 0, namedArgumentError("fmt", err)
	}
	spec, err := parseSpecSelector(parsed.value("--spec", "auto"))
	if err != nil {
		return 0, err
	}
	write := parsed.boolValue("--write")
	ctx := context.Background()
	session, err := openDocument(ctx, path, selectorAssertion(spec))
	if err != nil {
		return 0, err
	}
	document := session.Document()
	out, err := document.Serialize()
	if err != nil {
		return 0, errors.Join(err, session.Close())
	}

	if write {
		if err := session.RewriteContext(ctx, document); err != nil {
			return 0, errors.Join(err, session.Close())
		}
		if err := session.Close(); err != nil {
			return 0, err
		}
		fmt.Fprintf(stdout, "formatted %s\n", renderTextString(path))
		return 0, nil
	}
	if err := session.Close(); err != nil {
		return 0, err
	}
	fmt.Fprint(stdout, out)
	return 0, nil
}

func sortedKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cliVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

func titleCaseASCII(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
