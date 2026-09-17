package mcpserver

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/graph"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

const (
	maxConceptReadBytes            = 16 << 20
	maxFrontmatterBytes            = 1 << 20
	maxConceptItems                = 10_000
	maxReadConceptProjectionItems  = maxConceptItems
	maxReadConceptComputationItems = 256
	maxBundleTotalBytes            = 64 << 20
	maxBundlePathDepth             = 64
	maxMCPArgumentItems            = 100_000
)

var errMCPResourceLimit = errors.New("MCP bundle resource limit exceeded")
var errNilOwnedBundleSource = errors.New("nil owned bundle source")
var errNilRequestContext = errors.New("nil request context")
var errMCPOutputLimit = errors.New("MCP output limit exceeded")

var (
	errMCPArgumentCycle    = errors.New("MCP arguments contain a reference cycle")
	errMCPArgumentMapKey   = errors.New("MCP argument maps must use string keys")
	errMCPArgumentUTF8     = errors.New("MCP arguments contain invalid UTF-8")
	errMCPArgumentKeyUTF8  = errors.New("MCP argument key contains invalid UTF-8")
	errMCPArgumentCoercion = errors.New(
		"MCP arguments contain unsupported JSON coercions",
	)
	errMCPArgumentValue = errors.New("MCP arguments contain values unsupported by JSON")
)

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	jsonNumberType    = reflect.TypeFor[json.Number]()
)

type legacyToolIOObserver struct {
	sourceOpen func()
	sourceRead func()
	storeOpen  func()
}

type legacyToolIOObserverContextKey struct{}

func withLegacyToolIOObserver(
	ctx context.Context,
	observer legacyToolIOObserver,
) context.Context {
	return context.WithValue(ctx, legacyToolIOObserverContextKey{}, observer)
}

func observeLegacyToolIO(
	ctx context.Context,
	selectCallback func(legacyToolIOObserver) func(),
) {
	observer, ok := ctx.Value(legacyToolIOObserverContextKey{}).(legacyToolIOObserver)
	if !ok {
		return
	}
	if callback := selectCallback(observer); callback != nil {
		callback()
	}
}

const redactedSensitiveMessage = "operation failed; sensitive path redacted"

type conceptSummary struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

type listConceptsResponse struct {
	Concepts []conceptSummary `json:"concepts"`
}

type versionDTO struct {
	Declared      *string `json:"declared"`
	Effective     *string `json:"effective"`
	Resolution    *string `json:"resolution"`
	Compatibility *string `json:"compatibility"`
}

type versionDeclarationDTO struct {
	Present bool    `json:"present"`
	Valid   bool    `json:"valid"`
	Raw     *string `json:"raw"`
	Value   *string `json:"value"`
}

type computationMarkerDTO struct {
	Present  bool    `json:"present"`
	Runtime  *string `json:"runtime"`
	Executor *string `json:"executor"`
	Attester *string `json:"attester"`
}

type legacyDerivedDTO struct {
	Generated bool `json:"generated"`
	Citations bool `json:"citations"`
}

type statusStateDTO struct {
	Present   bool    `json:"present"`
	Valid     bool    `json:"valid"`
	Raw       *string `json:"raw"`
	Effective *string `json:"effective"`
}

type conceptSummaryV02 struct {
	ID            string               `json:"id"`
	Type          string               `json:"type"`
	Title         string               `json:"title"`
	Path          string               `json:"path"`
	Status        *string              `json:"status"`
	StatusState   statusStateDTO       `json:"status_state"`
	Trust         string               `json:"trust"`
	Stale         *bool                `json:"stale"`
	StaleAfter    *string              `json:"stale_after"`
	GeneratedAt   *string              `json:"generated_at"`
	SourceCount   int                  `json:"source_count"`
	Computation   computationMarkerDTO `json:"computation"`
	LegacyDerived legacyDerivedDTO     `json:"legacy_derived"`
}

type listConceptsStructured struct {
	Version            versionDTO            `json:"version"`
	VersionDeclaration versionDeclarationDTO `json:"version_declaration"`
	AsOf               *string               `json:"as_of"`
	Concepts           []conceptSummaryV02   `json:"concepts"`
}

type diagnosticDTO struct {
	Code     string `json:"code,omitempty"`
	Severity string `json:"severity"`
	File     string `json:"file,omitempty"`
	Message  string `json:"message"`
}

type parseErrorResponse struct {
	Status      string          `json:"status"`
	Diagnostics []diagnosticDTO `json:"diagnostics"`
}

type usageWindowDTO struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type sourceDTO struct {
	ID           string          `json:"id"`
	Resource     string          `json:"resource"`
	Title        string          `json:"title"`
	Author       string          `json:"author"`
	UsageCount   *string         `json:"usage_count"`
	LastModified *string         `json:"last_modified"`
	UsageWindow  *usageWindowDTO `json:"usage_window"`
}

type generationDTO struct {
	By *string `json:"by"`
	At *string `json:"at"`
}

type verificationDTO struct {
	By string `json:"by"`
	At string `json:"at"`
}

type computationParameterDTO struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type executorDTO struct {
	Resource string   `json:"resource"`
	Receipt  []string `json:"receipt"`
}

type attesterDTO struct {
	Resource string `json:"resource"`
}

type attestedComputationDTO struct {
	Runtime     string                    `json:"runtime"`
	Parameters  []computationParameterDTO `json:"parameters"`
	Computation string                    `json:"computation"`
	Executor    *executorDTO              `json:"executor"`
	Attester    *attesterDTO              `json:"attester"`
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

type attributionDTO struct {
	ID           string                  `json:"id"`
	NormalizedID string                  `json:"normalized_id"`
	Sources      []sourceDTO             `json:"sources"`
	References   []footnoteReferenceDTO  `json:"references"`
	Definitions  []footnoteDefinitionDTO `json:"definitions"`
}

type conceptProjectionDTO struct {
	Type         *string                 `json:"type"`
	Title        *string                 `json:"title"`
	Description  *string                 `json:"description"`
	Resource     *string                 `json:"resource"`
	Tags         []string                `json:"tags"`
	Sources      []sourceDTO             `json:"sources"`
	Attributions []attributionDTO        `json:"attributions"`
	UsageWindow  *usageWindowDTO         `json:"usage_window"`
	Generated    *generationDTO          `json:"generated"`
	Verified     []verificationDTO       `json:"verified"`
	Trust        string                  `json:"trust"`
	Status       *string                 `json:"status"`
	StatusState  statusStateDTO          `json:"status_state"`
	StaleAfter   *string                 `json:"stale_after"`
	Computation  *attestedComputationDTO `json:"computation"`
}

type legacyFallbackDTO struct {
	GeneratedFromTimestamp bool `json:"generated_from_timestamp"`
	SourcesFromCitations   bool `json:"sources_from_citations"`
}

type readConceptStructured struct {
	ID                 string                `json:"id"`
	Path               string                `json:"path"`
	Version            versionDTO            `json:"version"`
	VersionDeclaration versionDeclarationDTO `json:"version_declaration"`
	FrontmatterYAML    string                `json:"frontmatter_yaml"`
	Body               string                `json:"body"`
	Projection         conceptProjectionDTO  `json:"projection"`
	LegacyFallback     legacyFallbackDTO     `json:"legacy_fallback"`
}

type readConceptProjectionCollections struct {
	Tags         []string
	Sources      []bundle.ProvenanceSource
	Attributions []bundle.Attribution
	Verified     []bundle.Verification
	Computation  *bundle.AttestedComputationContract
}

func versionDTOFromBundle(resolution bundle.VersionResolution) versionDTO {
	var declared *string
	if resolution.Declared != "" {
		value := resolution.Declared
		declared = &value
	}
	effective := resolution.Effective
	source := string(resolution.Source)
	compatibility := string(resolution.Compatibility)
	return versionDTO{
		Declared: declared, Effective: &effective,
		Resolution: &source, Compatibility: &compatibility,
	}
}

func versionDeclarationDTOFromBundle(state bundle.VersionDeclarationState) versionDeclarationDTO {
	dto := versionDeclarationDTO{Present: state.Present, Valid: state.Valid}
	if state.Raw != "" {
		raw := state.Raw
		dto.Raw = &raw
	}
	if state.Value != "" {
		value := state.Value
		dto.Value = &value
	}
	return dto
}

func computationMarkerContext(
	ctx context.Context,
	document bundle.Document,
) (computationMarkerDTO, error) {
	state, err := document.AttestedComputationStateContext(ctx)
	if err != nil {
		return computationMarkerDTO{}, err
	}
	ok := state.TypeMatches && state.Valid && state.ContractPresent && state.ContractState.Valid
	marker := computationMarkerDTO{Present: ok}
	if !ok {
		return marker, nil
	}
	contract := state.Contract
	if contract.Runtime != "" {
		value := contract.Runtime
		marker.Runtime = &value
	}
	if contract.Executor != nil && contract.Executor.Resource != "" {
		value := contract.Executor.Resource
		marker.Executor = &value
	}
	if contract.Attester != nil && contract.Attester.Resource != "" {
		value := contract.Attester.Resource
		marker.Attester = &value
	}
	return marker, ctx.Err()
}

func statusStateFromBundle(state bundle.StatusState) statusStateDTO {
	projected := statusStateDTO{Present: state.Present, Valid: state.Valid}
	if state.Present {
		raw := state.Raw
		projected.Raw = &raw
	}
	if state.Valid {
		effective := state.Effective
		projected.Effective = &effective
	}
	return projected
}

func validDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validTimestamp(value string) bool {
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}

func collectReadConceptProjectionContext(
	ctx context.Context,
	document bundle.Document,
) (readConceptProjectionCollections, error) {
	if err := ctx.Err(); err != nil {
		return readConceptProjectionCollections{}, err
	}
	attributions, err := document.AttributionsContext(ctx)
	if err != nil {
		return readConceptProjectionCollections{}, err
	}
	sources, err := document.SourcesContext(ctx)
	if err != nil {
		return readConceptProjectionCollections{}, err
	}
	verifications, err := document.VerificationsContext(ctx)
	if err != nil {
		return readConceptProjectionCollections{}, err
	}
	collections := readConceptProjectionCollections{
		Tags:         document.Frontmatter.Tags(),
		Sources:      sources,
		Attributions: attributions,
		Verified:     verifications,
	}
	if err := ctx.Err(); err != nil {
		return readConceptProjectionCollections{}, err
	}
	computation, err := document.AttestedComputationStateContext(ctx)
	if err != nil {
		return readConceptProjectionCollections{}, err
	}
	if computation.TypeMatches && computation.Valid && computation.ContractPresent &&
		computation.ContractState.Valid {
		contract := computation.Contract
		collections.Computation = &contract
	}
	return collections, ctx.Err()
}

func validateReadConceptProjectionCapsContext(
	ctx context.Context,
	collections readConceptProjectionCollections,
) *mcp.CallToolResult {
	if result := requestContextCheckpoint(ctx, "validate read concept projection"); result != nil {
		return result
	}
	limits := []struct {
		name  string
		count int
		max   int
	}{
		{name: "tags", count: len(collections.Tags), max: maxReadConceptProjectionItems},
		{name: "sources", count: len(collections.Sources), max: maxReadConceptProjectionItems},
		{name: "attributions", count: len(collections.Attributions), max: maxReadConceptProjectionItems},
		{name: "verified", count: len(collections.Verified), max: maxReadConceptProjectionItems},
	}
	for _, limit := range limits {
		if result := requestContextCheckpoint(ctx, "validate read concept projection"); result != nil {
			return result
		}
		if limit.count > limit.max {
			return stableToolError(
				"resource_limit",
				fmt.Sprintf("read_concept projection %s exceeds the MCP item limit", limit.name),
				false,
			)
		}
	}
	for _, attribution := range collections.Attributions {
		if result := requestContextCheckpoint(ctx, "validate read concept projection"); result != nil {
			return result
		}
		nested := []struct {
			name  string
			count int
		}{
			{name: "attribution sources", count: len(attribution.Sources)},
			{name: "attribution references", count: len(attribution.References)},
			{name: "attribution definitions", count: len(attribution.Definitions)},
		}
		for _, limit := range nested {
			if result := requestContextCheckpoint(ctx, "validate read concept projection"); result != nil {
				return result
			}
			if limit.count > maxReadConceptProjectionItems {
				return stableToolError(
					"resource_limit",
					fmt.Sprintf("read_concept projection %s exceeds the MCP item limit", limit.name),
					false,
				)
			}
		}
	}
	if collections.Computation == nil {
		return nil
	}
	if len(collections.Computation.Parameters) > maxReadConceptComputationItems {
		return stableToolError(
			"resource_limit",
			"read_concept projection computation parameters exceed the MCP item limit",
			false,
		)
	}
	if collections.Computation.Executor != nil &&
		len(collections.Computation.Executor.Receipt) > maxReadConceptComputationItems {
		return stableToolError(
			"resource_limit",
			"read_concept projection executor receipt exceeds the MCP item limit",
			false,
		)
	}
	return nil
}

func projectDocumentContext(
	ctx context.Context,
	document bundle.Document,
) (conceptProjectionDTO, *mcp.CallToolResult) {
	collections, err := collectReadConceptProjectionContext(ctx, document)
	if err != nil {
		return conceptProjectionDTO{}, bundleDomainError(
			"project captured concept",
			"concept projection exceeds MCP resource limits",
			err,
		)
	}
	if result := validateReadConceptProjectionCapsContext(ctx, collections); result != nil {
		return conceptProjectionDTO{}, result
	}
	projection, err := projectDocumentCollectionsContext(ctx, document, collections)
	if err != nil {
		return conceptProjectionDTO{}, bundleDomainError(
			"project captured concept",
			"concept projection exceeds MCP resource limits",
			err,
		)
	}
	return projection, nil
}

func projectDocumentCollectionsContext(
	ctx context.Context,
	document bundle.Document,
	collections readConceptProjectionCollections,
) (conceptProjectionDTO, error) {
	if err := ctx.Err(); err != nil {
		return conceptProjectionDTO{}, err
	}
	status := statusStateFromBundle(document.StatusState())
	trust, err := document.TrustTierContext(ctx)
	if err != nil {
		return conceptProjectionDTO{}, err
	}
	projection := conceptProjectionDTO{
		Tags:         make([]string, 0, len(collections.Tags)),
		Sources:      []sourceDTO{},
		Attributions: []attributionDTO{},
		Verified:     []verificationDTO{},
		Trust:        string(trust),
		Status:       status.Effective,
		StatusState:  status,
	}
	projection.Type = optionalScalar(document.Frontmatter.Type())
	projection.Title = optionalScalar(document.Frontmatter.Title())
	projection.Description = optionalScalar(document.Frontmatter.Description())
	projection.Resource = optionalScalar(document.Frontmatter.Resource())
	for _, tag := range collections.Tags {
		if err := ctx.Err(); err != nil {
			return conceptProjectionDTO{}, err
		}
		projection.Tags = append(projection.Tags, tag)
	}
	for _, source := range collections.Sources {
		if err := ctx.Err(); err != nil {
			return conceptProjectionDTO{}, err
		}
		projection.Sources = append(projection.Sources, projectSource(source))
	}
	for _, attribution := range collections.Attributions {
		if err := ctx.Err(); err != nil {
			return conceptProjectionDTO{}, err
		}
		item := attributionDTO{
			ID:           attribution.ID,
			NormalizedID: attribution.NormalizedID,
			Sources:      make([]sourceDTO, 0, len(attribution.Sources)),
			References:   make([]footnoteReferenceDTO, 0, len(attribution.References)),
			Definitions:  make([]footnoteDefinitionDTO, 0, len(attribution.Definitions)),
		}
		for _, source := range attribution.Sources {
			if err := ctx.Err(); err != nil {
				return conceptProjectionDTO{}, err
			}
			item.Sources = append(item.Sources, projectSource(source))
		}
		for _, reference := range attribution.References {
			if err := ctx.Err(); err != nil {
				return conceptProjectionDTO{}, err
			}
			item.References = append(item.References, footnoteReferenceDTO{
				ID: reference.ID, Raw: reference.Raw,
			})
		}
		for _, definition := range attribution.Definitions {
			if err := ctx.Err(); err != nil {
				return conceptProjectionDTO{}, err
			}
			item.Definitions = append(item.Definitions, footnoteDefinitionDTO{
				ID: definition.ID, Text: definition.Text, Raw: definition.Raw,
			})
		}
		projection.Attributions = append(projection.Attributions, item)
	}
	if window, ok := document.UsageWindow(); ok && validDate(window.From) && validDate(window.To) {
		projection.UsageWindow = &usageWindowDTO{From: window.From, To: window.To}
	}
	generated := document.GenerationState()
	if generated.HasValue {
		projection.Generated = &generationDTO{}
		if generated.By.Valid {
			value := generated.By.Value
			projection.Generated.By = &value
		}
		if generated.At.State == bundle.TemporalValid {
			value := generated.At.Raw
			projection.Generated.At = &value
		}
	}
	for _, verification := range collections.Verified {
		if err := ctx.Err(); err != nil {
			return conceptProjectionDTO{}, err
		}
		if verification.By != "" && validTimestamp(verification.At) {
			projection.Verified = append(projection.Verified, verificationDTO{By: verification.By, At: verification.At})
		}
	}
	if value, ok := document.StaleAfter(); ok && validDate(value) {
		projection.StaleAfter = &value
	}
	if computation := collections.Computation; computation != nil {
		parameters := make([]computationParameterDTO, 0, len(computation.Parameters))
		for _, parameter := range computation.Parameters {
			if err := ctx.Err(); err != nil {
				return conceptProjectionDTO{}, err
			}
			parameters = append(parameters, computationParameterDTO{
				Name: parameter.Name, Type: parameter.Type, Required: parameter.Required,
			})
		}
		dto := &attestedComputationDTO{
			Runtime:     computation.Runtime,
			Parameters:  parameters,
			Computation: computation.Computation,
		}
		if computation.Executor != nil {
			receipts := make([]string, 0, len(computation.Executor.Receipt))
			for _, receipt := range computation.Executor.Receipt {
				if err := ctx.Err(); err != nil {
					return conceptProjectionDTO{}, err
				}
				receipts = append(receipts, receipt)
			}
			dto.Executor = &executorDTO{
				Resource: computation.Executor.Resource,
				Receipt:  receipts,
			}
		}
		if computation.Attester != nil {
			dto.Attester = &attesterDTO{Resource: computation.Attester.Resource}
		}
		projection.Computation = dto
	}
	if err := ctx.Err(); err != nil {
		return conceptProjectionDTO{}, err
	}
	return projection, nil
}

func projectSource(source bundle.ProvenanceSource) sourceDTO {
	item := sourceDTO{
		ID:       source.ID,
		Resource: source.Resource,
		Title:    source.Title,
		Author:   source.Author,
	}
	if source.UsageCount != nil {
		value := strconv.FormatUint(*source.UsageCount, 10)
		item.UsageCount = &value
	}
	if validDate(source.LastModified) {
		value := source.LastModified
		item.LastModified = &value
	}
	if source.UsageWindow != nil && validDate(source.UsageWindow.From) && validDate(source.UsageWindow.To) {
		item.UsageWindow = &usageWindowDTO{From: source.UsageWindow.From, To: source.UsageWindow.To}
	}
	return item
}

func optionalScalar(value string, ok bool) *string {
	if !ok {
		return nil
	}
	return &value
}

// diagnosticProjection retains the full internal diagnostic identity while
// projecting the fixed MCP wire contract. The transaction/store boundary owns
// structured relation context; MCP write_concept deliberately does not extend
// its long-standing diagnostic payload with it.
type diagnosticProjection struct {
	wire diagnosticDTO
	key  string
}

func diagnosticDTOFromStore(root string, diagnostic store.Diagnostic) diagnosticProjection {
	refs := make([]string, len(diagnostic.Refs))
	for i, ref := range diagnostic.Refs {
		refs[i] = ref.String()
	}
	source := ""
	if len(refs) > 0 {
		source = refs[0]
	}
	return diagnosticProjection{
		wire: diagnosticDTO{
			Severity: string(diagnostic.Severity),
			File:     relativeSlashPath(root, diagnostic.File),
			Message:  redactSensitivePaths(diagnostic.Message),
		},
		key: diagnosticProjectionKey(
			string(diagnostic.Severity), relativeSlashPath(root, diagnostic.File),
			diagnostic.RelationType, diagnostic.RawTarget, diagnostic.Code,
			diagnostic.Message, source, refs,
		),
	}
}

func diagnosticDTOFromValidator(root string, diagnostic validator.Diagnostic) diagnosticProjection {
	refs := make([]string, len(diagnostic.Refs))
	for i, ref := range diagnostic.Refs {
		refs[i] = ref.String()
	}
	return diagnosticProjection{
		wire: diagnosticDTO{
			Severity: diagnostic.Severity.String(),
			File:     relativeSlashPath(root, diagnostic.File),
			Message:  redactSensitivePaths(diagnostic.Message),
		},
		key: diagnosticProjectionKey(
			diagnostic.Severity.String(), relativeSlashPath(root, diagnostic.File),
			diagnostic.RelationType, diagnostic.RawTarget, diagnostic.Code,
			diagnostic.Message, diagnostic.Source.String(), refs,
		),
	}
}

// diagnosticProjectionKey identifies one logical diagnostic without leaking
// its structured context onto the MCP wire. Matching store and validator
// relation diagnostics share source/ref identity and therefore deduplicate.
func diagnosticProjectionKey(severity, file, relationType, rawTarget, code, message, source string, refs []string) string {
	var key strings.Builder
	write := func(value string) {
		key.WriteString(strconv.Itoa(len(value)))
		key.WriteByte(':')
		key.WriteString(value)
	}
	// Store uses lower-case severity values while validator emits upper-case
	// labels. They are one public severity domain for this projection.
	write(strings.ToLower(severity))
	write(file)
	write(relationType)
	write(rawTarget)
	write(code)
	write(message)
	write(source)
	for _, ref := range refs {
		write(ref)
	}
	return key.String()
}

// uniqueDiagnosticDTOs retains the first wire projection of each logical
// diagnostic. Callers pass store projections before validator projections.
func uniqueDiagnosticDTOsContext(
	ctx context.Context,
	groups ...[]diagnosticProjection,
) ([]diagnosticDTO, error) {
	if ctx == nil {
		return nil, errNilRequestContext
	}
	seen := make(map[string]struct{})
	var diagnostics []diagnosticDTO
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, diagnostic := range group {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if _, ok := seen[diagnostic.key]; ok {
				continue
			}
			seen[diagnostic.key] = struct{}{}
			diagnostics = append(diagnostics, diagnostic.wire)
		}
	}
	return diagnostics, ctx.Err()
}

type validateBundleResponse struct {
	ScannedFiles int             `json:"scanned_files"`
	Conformant   bool            `json:"conformant"`
	Errors       int             `json:"errors"`
	Warnings     int             `json:"warnings"`
	Info         int             `json:"info"`
	Diagnostics  []diagnosticDTO `json:"diagnostics"`
}

type conformanceDTO struct {
	Conformant bool `json:"conformant"`
	Errors     int  `json:"errors"`
}

type guidanceDTO struct {
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
}

type validateBundleStructured struct {
	Version            versionDTO            `json:"version"`
	VersionDeclaration versionDeclarationDTO `json:"version_declaration"`
	AsOf               *string               `json:"as_of,omitempty"`
	ScannedFiles       int                   `json:"scanned_files"`
	Conformance        conformanceDTO        `json:"conformance"`
	Guidance           guidanceDTO           `json:"guidance"`
	Diagnostics        []wireDiagnostic      `json:"diagnostics"`
}

type writeConceptResponse struct {
	Status      string          `json:"status"`
	Path        string          `json:"path"`
	Diagnostics []diagnosticDTO `json:"diagnostics"`
}

func handleListConcepts(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "list_concepts", request); result != nil {
		return result, nil
	}
	asOf, result := optionalDate(request, "as_of")
	if result != nil {
		return result, nil
	}
	root, result := requireBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}

	loaded, err := loadBundleContext(ctx, root)
	if err != nil {
		return bundleLoadError("load bundle", err), nil
	}
	if result := parseErrorsResult(root, loaded.ParseErrors()); result != nil {
		return result, nil
	}

	ids, err := loaded.ConceptIDsContext(ctx)
	if err != nil {
		return bundleLoadError("list captured concepts", err), nil
	}
	if len(ids) > maxConceptItems {
		return stableToolError("resource_limit", "bundle contains too many concepts", false), nil
	}
	concepts := make([]bundle.Concept, 0, len(ids))
	for _, id := range ids {
		if result := requestContextCheckpoint(ctx, "list captured concepts"); result != nil {
			return result, nil
		}
		concept, ok := loaded.Get(id)
		if !ok {
			return stableToolError("operation_rejected", "captured concept index is inconsistent", false), nil
		}
		concepts = append(concepts, concept)
	}
	if err := sortSliceContext(ctx, concepts, func(left, right bundle.Concept) bool {
		return left.ID.String() < right.ID.String()
	}); err != nil {
		return bundleLoadError("sort captured concepts", err), nil
	}

	resolution, err := loaded.VersionResolutionContext(ctx, "")
	if err != nil {
		return bundleLoadError("resolve bundle version", err), nil
	}
	declaration, err := loaded.VersionDeclarationStateContext(ctx)
	if err != nil {
		return bundleLoadError("read bundle version declaration", err), nil
	}
	response := listConceptsResponse{Concepts: make([]conceptSummary, 0, len(concepts))}
	structured := listConceptsStructured{
		Version:            versionDTOFromBundle(resolution),
		VersionDeclaration: versionDeclarationDTOFromBundle(declaration),
		Concepts:           make([]conceptSummaryV02, 0, len(concepts)),
	}
	if asOf != nil {
		value := asOf.Format("2006-01-02")
		structured.AsOf = &value
	}
	for _, concept := range concepts {
		if result := requestContextCheckpoint(ctx, "project concept list"); result != nil {
			return result, nil
		}
		fallback, result := legacyFallbackObservation(ctx, concept.Document)
		if result != nil {
			return result, nil
		}
		typ, _ := concept.Document.Frontmatter.Type()
		title, _ := concept.Document.Frontmatter.Title()
		response.Concepts = append(response.Concepts, conceptSummary{
			ID:    concept.ID.String(),
			Type:  typ,
			Title: title,
			Path:  relativeSlashPath(root, concept.Path),
		})
		status := statusStateFromBundle(concept.Document.StatusState())
		sources, sourcesErr := concept.Document.SourcesContext(ctx)
		if sourcesErr != nil {
			return bundleDomainError(
				"project concept list",
				"concept list exceeds MCP resource limits",
				sourcesErr,
			), nil
		}
		trust, trustErr := concept.Document.TrustTierContext(ctx)
		if trustErr != nil {
			return bundleDomainError(
				"project concept list",
				"concept list exceeds MCP resource limits",
				trustErr,
			), nil
		}
		computation, computationErr := computationMarkerContext(ctx, concept.Document)
		if computationErr != nil {
			return bundleDomainError(
				"project concept list",
				"concept list exceeds MCP resource limits",
				computationErr,
			), nil
		}
		if result := requestContextCheckpoint(ctx, "project concept list"); result != nil {
			return result, nil
		}
		summary := conceptSummaryV02{
			ID:          concept.ID.String(),
			Type:        typ,
			Title:       title,
			Path:        relativeSlashPath(root, concept.Path),
			Status:      status.Effective,
			StatusState: status,
			Trust:       string(trust),
			SourceCount: len(sources),
			Computation: computation,
			LegacyDerived: legacyDerivedDTO{
				Generated: fallback.TimestampActive,
				Citations: fallback.CitationsActive,
			},
		}
		if asOf != nil {
			stale := concept.Document.IsStale(*asOf)
			summary.Stale = &stale
		}
		if value, ok := concept.Document.StaleAfter(); ok && validDate(value) {
			summary.StaleAfter = &value
		}
		if value, ok := concept.Document.EffectiveContentChangeTime(); ok && validTimestamp(value) {
			summary.GeneratedAt = &value
		}
		structured.Concepts = append(structured.Concepts, summary)
	}
	if result := requestContextCheckpoint(ctx, "project concept list"); result != nil {
		return result, nil
	}
	return jsonStructuredResultContext(ctx, "project concept list", response, structured), nil
}

func handleReadConcept(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "read_concept", request); result != nil {
		return result, nil
	}
	id, result := requireConceptID(request)
	if result != nil {
		return result, nil
	}
	root, result := requireBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	loaded, err := loadBundleContext(ctx, root)
	if err != nil {
		return bundleLoadError("load bundle", err), nil
	}
	if result := parseErrorsResult(root, loaded.ParseErrors()); result != nil {
		return result, nil
	}
	return readConceptFromBundleContext(ctx, root, loaded, id), nil
}

// readConceptFromBundleContext projects one concept exclusively from the
// immutable files and typed documents retained by loaded. It deliberately
// accepts no pathname or Source: once Load has returned, no later filesystem
// mutation can mix another revision into the response.
func readConceptFromBundleContext(
	ctx context.Context,
	root string,
	loaded *bundle.Bundle,
	id bundle.ConceptID,
) *mcp.CallToolResult {
	path, ok, err := loaded.ConceptPathContext(ctx, id)
	if err != nil {
		return bundleLoadError("resolve captured concept path", err)
	}
	if !ok {
		return conceptNotFoundResult(id)
	}
	concept, ok := loaded.Get(id)
	if !ok {
		return conceptNotFoundResult(id)
	}
	data, present, err := loaded.ReadFileContext(ctx, path)
	if err != nil {
		return bundleLoadError("read captured concept", err)
	}
	if !present {
		return conceptNotFoundResult(id)
	}
	document := concept.Document
	if len(document.FrontmatterYAML()) > maxFrontmatterBytes {
		return stableToolError("resource_limit", "concept frontmatter exceeds the MCP limit", false)
	}
	fallback, result := legacyFallbackObservation(ctx, document)
	if result != nil {
		return result
	}
	projection, result := projectDocumentContext(ctx, document)
	if result != nil {
		return result
	}
	declaration, err := loaded.VersionDeclarationStateContext(ctx)
	if err != nil {
		return bundleLoadError("read captured version declaration", err)
	}
	var resolution versionDTO
	resolved, resolveErr := loaded.VersionResolutionContext(ctx, "")
	if resolveErr == nil {
		resolution = versionDTOFromBundle(resolved)
	} else if !(declaration.Present && !declaration.Valid &&
		errors.Is(resolveErr, bundle.ErrInvalidVersionDeclaration)) {
		if errors.Is(resolveErr, context.Canceled) || errors.Is(resolveErr, context.DeadlineExceeded) ||
			errors.Is(resolveErr, bundle.ErrYAMLResourceLimit) ||
			errors.Is(resolveErr, bundle.ErrInvalidYAMLGraph) {
			return bundleLoadError("resolve captured bundle version", resolveErr)
		}
		return bundleParseErrorResult(root, "index.md", resolveErr)
	}
	structured := readConceptStructured{
		ID:                 id.String(),
		Path:               path,
		Version:            resolution,
		VersionDeclaration: versionDeclarationDTOFromBundle(declaration),
		FrontmatterYAML:    string(document.FrontmatterYAML()),
		Body:               document.Body,
		Projection:         projection,
		LegacyFallback: legacyFallbackDTO{
			GeneratedFromTimestamp: fallback.TimestampActive,
			SourcesFromCitations:   fallback.CitationsActive,
		},
	}
	if result := requestContextCheckpoint(ctx, "project captured concept"); result != nil {
		return result
	}
	if _, err := estimateMCPJSONOutputSizeContext(ctx, structured, maxBundleTotalBytes); err != nil {
		return bundleDomainError("project captured concept", "read concept output exceeds MCP limits", err)
	}
	return mcp.NewToolResultStructured(structured, string(data))
}

func conceptNotFoundResult(id bundle.ConceptID) *mcp.CallToolResult {
	return stableToolError("concept_not_found", "concept not found: "+id.String(), false)
}

func legacyFallbackObservation(
	ctx context.Context,
	document bundle.Document,
) (bundle.LegacyFallbackObservation, *mcp.CallToolResult) {
	observation, err := document.LegacyFallbackObservationContext(ctx)
	if err == nil {
		return observation, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return bundle.LegacyFallbackObservation{}, stableToolError(
			"operation_cancelled", "legacy fallback observation was cancelled", true,
		)
	}
	return bundle.LegacyFallbackObservation{}, stableToolError(
		"operation_rejected", "legacy fallback observation failed", false,
	)
}

func handleValidateBundle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "validate_bundle", request); result != nil {
		return result, nil
	}
	strict, result := optionalBool(request, "strict")
	if result != nil {
		return result, nil
	}
	checkLinks, result := optionalBool(request, "check_links")
	if result != nil {
		return result, nil
	}
	checkOrphans, result := optionalBool(request, "check_orphans")
	if result != nil {
		return result, nil
	}
	targetVersion, result := optionalStringDefault(request, "target_version", "auto")
	if result != nil {
		return result, nil
	}
	switch targetVersion {
	case "auto", bundle.LegacyOKFVersion, bundle.OKFVersion:
	default:
		return toolErrorf("argument %q must be auto, 0.1, or 0.2", "target_version"), nil
	}
	asOf, result := optionalDate(request, "as_of")
	if result != nil {
		return result, nil
	}
	root, result := requireBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	loaded, err := loadBundleContext(ctx, root)
	if err != nil {
		return bundleLoadError("load bundle", err), nil
	}
	cfg := validator.ValidatorConfig{
		Strict:       strict,
		CheckLinks:   checkLinks,
		CheckOrphans: checkOrphans,
		Spec:         targetVersion,
	}
	if asOf != nil {
		cfg.ReferenceDate = *asOf
	}
	// Validate the same no-follow snapshot already loaded for this request.
	// Reopening by pathname here would turn the earlier path validation into a
	// TOCTOU preflight rather than a boundary for the validator's input.
	report, err := validator.ValidateBundleContext(ctx, loaded, &cfg)
	if err != nil {
		return bundleDomainError(
			"validate bundle",
			"bundle validation exceeds MCP resource limits",
			err,
		), nil
	}
	legacy, err := reportResponseContext(ctx, root, report)
	if err != nil {
		return bundleDomainError(
			"project bundle validation",
			"bundle validation output exceeds MCP resource limits",
			err,
		), nil
	}
	version := versionDTO{}
	if report.VersionDeclaration.Valid {
		version = versionDTOFromBundle(report.Version)
	}
	structured := validateBundleStructured{
		Version:            version,
		VersionDeclaration: versionDeclarationDTOFromBundle(report.VersionDeclaration),
		ScannedFiles:       report.ScannedFiles,
		Conformance:        conformanceDTO{Conformant: report.IsConformant(), Errors: report.ErrorCount()},
		Guidance:           guidanceDTO{Warnings: report.WarningCount(), Info: report.InfoCount()},
		Diagnostics:        make([]wireDiagnostic, 0, len(report.Diagnostics)),
	}
	if asOf != nil {
		value := asOf.Format("2006-01-02")
		structured.AsOf = &value
	}
	for _, diagnostic := range report.Diagnostics {
		if result := requestContextCheckpoint(ctx, "project bundle validation"); result != nil {
			return result, nil
		}
		structured.Diagnostics = append(structured.Diagnostics, wireDiagnosticFromValidator(root, diagnostic))
	}
	if result := requestContextCheckpoint(ctx, "project bundle validation"); result != nil {
		return result, nil
	}
	return jsonStructuredResultContext(ctx, "project validation report", legacy, structured), nil
}

func wireDiagnosticFromValidator(root string, diagnostic validator.Diagnostic) wireDiagnostic {
	return wireDiagnostic{
		Code:          stableDiagnosticCode(diagnostic.Code),
		Severity:      diagnostic.Severity.String(),
		File:          relativeSlashPath(root, diagnostic.File),
		Field:         diagnostic.FieldPath,
		Message:       redactSensitivePaths(diagnostic.Message),
		SpecRef:       diagnostic.SpecRef,
		PolicyFailure: diagnostic.PolicyFailure,
	}
}

func handleSemanticGraph(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "get_semantic_graph", request); result != nil {
		return result, nil
	}
	asOf, result := optionalDate(request, "as_of")
	if result != nil {
		return result, nil
	}
	root, result := requireBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	loaded, err := loadBundleContext(ctx, root)
	if err != nil {
		return bundleLoadError("load bundle", err), nil
	}
	if result := parseErrorsResult(root, loaded.ParseErrors()); result != nil {
		return result, nil
	}

	// Keep the historical renderer byte-for-byte as text fallback while
	// applying the same explicit transport ceiling as the structured profile.
	legacy := newBoundedOutputBuffer(maxConceptReadBytes)
	if err := graph.RenderJSONLDContext(ctx, legacy, loaded); err != nil {
		return bundleDomainError("render JSON-LD", "semantic graph exceeds the MCP byte limit", err), nil
	}

	out := newBoundedOutputBuffer(maxConceptReadBytes)
	if err := graph.RenderJSONLDWithOptionsContext(ctx, out, loaded, graph.Options{
		Profile:            graph.ProjectionProfileToolkitV02,
		AsOf:               asOf,
		ExtensionRelations: graph.ExtensionRelationsInclude,
		VersionSelector:    "",
	}); err != nil {
		return bundleDomainError("render v0.2 JSON-LD", "semantic graph exceeds the MCP byte limit", err), nil
	}
	var structured map[string]any
	if err := json.Unmarshal(out.Bytes(), &structured); err != nil {
		return toolErrorf("parse rendered JSON-LD: %v", err), nil
	}
	if result := requestContextCheckpoint(ctx, "project semantic graph"); result != nil {
		return result, nil
	}
	if result := graphProjectionBudgetContext(ctx, structured); result != nil {
		return result, nil
	}
	return mcp.NewToolResultStructured(structured, legacy.String()), nil
}

type boundedOutputBuffer struct {
	bytes.Buffer
	limit int
}

func newBoundedOutputBuffer(limit int) *boundedOutputBuffer {
	return &boundedOutputBuffer{limit: limit}
}

func (buffer *boundedOutputBuffer) Write(data []byte) (int, error) {
	if buffer == nil {
		return 0, errors.New("nil bounded output buffer")
	}
	if buffer.limit < 0 || len(data) > buffer.limit-buffer.Len() {
		return 0, errMCPOutputLimit
	}
	return buffer.Buffer.Write(data)
}

func graphProjectionBudgetContext(
	ctx context.Context,
	document map[string]any,
) *mcp.CallToolResult {
	if result := requestContextCheckpoint(ctx, "validate semantic graph projection"); result != nil {
		return result
	}
	nodes, ok := document["@graph"].([]any)
	if !ok {
		return stableToolError("invalid_projection", "semantic graph renderer returned an invalid node collection", false)
	}
	if len(nodes) > 100_000 {
		return stableToolError("resource_limit", "semantic graph contains too many nodes", false)
	}
	for _, raw := range nodes {
		if result := requestContextCheckpoint(ctx, "validate semantic graph projection"); result != nil {
			return result
		}
		node, ok := raw.(map[string]any)
		if !ok {
			return stableToolError("invalid_projection", "semantic graph renderer returned an invalid node", false)
		}
		if value, ok := node["@id"].(string); ok {
			count, err := runeCountContext(ctx, value, 8192)
			if err != nil {
				return bundleDomainError("validate semantic graph projection", "semantic graph exceeds MCP limits", err)
			}
			if count > 8192 {
				return stableToolError("resource_limit", "semantic graph identifier exceeds the MCP limit", false)
			}
		}
		if value, ok := node["projectionVersion"].(string); ok {
			count, err := runeCountContext(ctx, value, 64)
			if err != nil {
				return bundleDomainError("validate semantic graph projection", "semantic graph exceeds MCP limits", err)
			}
			if count > 64 {
				return stableToolError("resource_limit", "semantic graph projection version exceeds the MCP limit", false)
			}
		}
	}
	return requestContextCheckpoint(ctx, "validate semantic graph projection")
}

func runeCountContext(ctx context.Context, value string, limit int) (int, error) {
	if ctx == nil {
		return 0, errNilRequestContext
	}
	count := 0
	for range value {
		if count%256 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		count++
		if count > limit {
			return count, nil
		}
	}
	return count, ctx.Err()
}

func handleWriteConcept(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "write_concept", request); result != nil {
		return result, nil
	}
	id, result := requireConceptID(request)
	if result != nil {
		return result, nil
	}
	frontmatterText, err := request.RequireString("frontmatter")
	if err != nil {
		return toolError(err.Error()), nil
	}
	body, err := request.RequireString("body")
	if err != nil {
		return toolError(err.Error()), nil
	}
	if len(frontmatterText) > maxFrontmatterBytes || len(body) > maxConceptReadBytes {
		return stableToolError("resource_limit", "concept content exceeds the MCP transport limit", false), nil
	}
	if !utf8.ValidString(frontmatterText) || !utf8.ValidString(body) {
		return toolError(bundle.ErrInvalidEncoding.Error()), nil
	}
	frontmatter, err := bundle.ParseFrontmatterContext(ctx, frontmatterText)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, bundle.ErrYAMLResourceLimit) ||
			errors.Is(err, bundle.ErrInvalidYAMLGraph) {
			return bundleDomainError(
				"parse concept frontmatter",
				"concept frontmatter exceeds YAML resource limits",
				err,
			), nil
		}
		return toolError(err.Error()), nil
	}
	if _, err := bundle.NewDocument(frontmatter, body).Serialize(); err != nil {
		return toolError(err.Error()), nil
	}
	root, result := requireBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	if result := requireLoadableBundle(ctx, root); result != nil {
		return result, nil
	}
	if result := requestContextCheckpoint(ctx, "write concept"); result != nil {
		return result, nil
	}
	observeLegacyToolIO(ctx, func(observer legacyToolIOObserver) func() {
		return observer.storeOpen
	})
	response, writeErr := writeConcept(ctx, root, id, frontmatterText, body)
	if writeErr != nil {
		return writeConceptError(writeErr), nil
	}
	if response.Status == "rejected" {
		if result := requestContextCheckpoint(ctx, "project rejected concept write"); result != nil {
			return result, nil
		}
		return jsonErrorResult(response), nil
	}
	return jsonStructuredResult(response, response), nil
}

func writeConceptError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, errMCPWriteReceiptInvalid):
		return stableToolError("operation_rejected", "write concept receipt failed integrity validation", false)
	case errors.Is(err, mutation.ErrMigrationPlanMismatch):
		return stableToolError("plan_mismatch", "write concept receipt does not match the authorized request", false)
	case errors.Is(err, store.ErrStorageCorrupt):
		return stableToolError("operation_rejected", fmt.Sprintf("write concept: %v", err), false)
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return stableToolError("operation_cancelled", "write concept was cancelled", true)
	}
	var conflict *store.Conflict
	if errors.As(err, &conflict) {
		return stableToolError("revision_conflict", "bundle revision changed while writing concept", conflict.Retryable)
	}
	return bundleDomainError("write concept", "write concept exceeds MCP resource limits", err)
}

func requireBundlePath(ctx context.Context, request mcp.CallToolRequest) (string, *mcp.CallToolResult) {
	if result := validateRawMCPArgumentsContext(ctx, request.GetArguments()); result != nil {
		return "", result
	}
	return requireValidatedBundlePath(ctx, request)
}

func requireValidatedBundlePath(
	ctx context.Context,
	request mcp.CallToolRequest,
) (string, *mcp.CallToolResult) {
	raw, err := request.RequireString("bundle_path")
	if err != nil {
		return "", toolError(err.Error())
	}
	root, err := normalizeBundleRootContext(ctx, raw)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", bundleDomainError("validate bundle path", "bundle path exceeds MCP resource limits", err)
		}
		return "", toolError(err.Error())
	}
	return root, nil
}

func validateRawMCPArgumentsContext(ctx context.Context, arguments any) *mcp.CallToolResult {
	if ctx == nil {
		return bundleDomainError("validate tool input", "tool input exceeds MCP resource limits", errNilRequestContext)
	}
	if err := validateMCPInputContext(ctx, arguments, 0, new(int)); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return bundleDomainError("validate tool input", "tool input exceeds MCP resource limits", err)
		}
		if errors.Is(err, errMCPResourceLimit) {
			return stableToolError("resource_limit", err.Error(), false)
		}
		return stableToolError("invalid_request", err.Error(), false)
	}
	return nil
}

func validateMCPInputContext(ctx context.Context, value any, depth int, items *int) error {
	if ctx == nil {
		return errNilRequestContext
	}
	if err := validateMCPValue(
		ctx,
		reflect.ValueOf(value),
		depth,
		items,
		make(map[mcpInputVisit]struct{}),
	); err != nil {
		return err
	}
	_, err := estimateMCPJSONOutputSizeContext(ctx, value, maxBundleTotalBytes)
	return err
}

type mcpInputVisit struct {
	kind     reflect.Kind
	typ      reflect.Type
	pointer  uintptr
	length   int
	capacity int
}

type mcpInputStructField struct {
	index int
	name  string
}

func validateMCPValue(
	ctx context.Context,
	value reflect.Value,
	depth int,
	items *int,
	active map[mcpInputVisit]struct{},
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxBundlePathDepth {
		return errMCPResourceLimit
	}
	if err := consumeMCPInputItems(items, 1); err != nil {
		return errMCPResourceLimit
	}
	if !value.IsValid() {
		return nil
	}
	if mcpInputRequiresJSONCoercion(value.Type()) {
		return errMCPArgumentCoercion
	}

	switch value.Kind() {
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			return errMCPArgumentUTF8
		}
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
	case reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return validateMCPValue(ctx, value.Elem(), depth+1, items, active)
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		release, err := enterMCPInputContainer(value, active)
		if err != nil {
			return err
		}
		defer release()
		return validateMCPValue(ctx, value.Elem(), depth+1, items, active)
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return errMCPArgumentMapKey
		}
		if mcpInputRequiresJSONCoercion(value.Type().Key()) {
			return errMCPArgumentCoercion
		}
		if value.IsNil() {
			return nil
		}
		if value.Len() > maxMCPArgumentItems-*items {
			return errMCPResourceLimit
		}
		release, err := enterMCPInputContainer(value, active)
		if err != nil {
			return err
		}
		defer release()

		keys := value.MapKeys()
		if err := sortSliceContext(ctx, keys, func(left, right reflect.Value) bool {
			return left.String() < right.String()
		}); err != nil {
			return err
		}
		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !utf8.ValidString(key.String()) {
				return errMCPArgumentKeyUTF8
			}
			if err := validateMCPValue(ctx, value.MapIndex(key), depth+1, items, active); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if value.IsNil() {
			return nil
		}
		if value.Len() > maxMCPArgumentItems-*items {
			return errMCPResourceLimit
		}
		release, err := enterMCPInputContainer(value, active)
		if err != nil {
			return err
		}
		defer release()
		for index := 0; index < value.Len(); index++ {
			if err := validateMCPValue(ctx, value.Index(index), depth+1, items, active); err != nil {
				return err
			}
		}
	case reflect.Array:
		if value.Len() > maxMCPArgumentItems-*items {
			return errMCPResourceLimit
		}
		for index := 0; index < value.Len(); index++ {
			if err := validateMCPValue(ctx, value.Index(index), depth+1, items, active); err != nil {
				return err
			}
		}
	case reflect.Struct:
		fields, err := mcpInputStructFieldsContext(ctx, value.Type())
		if err != nil {
			return err
		}
		for _, field := range fields {
			if err := ctx.Err(); err != nil {
				return err
			}
			fieldValue := value.Field(field.index)
			if err := validateMCPValue(ctx, fieldValue, depth+1, items, active); err != nil {
				return err
			}
		}
	default:
		return errMCPArgumentCoercion
	}
	return nil
}

func mcpInputJSONEmptyValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer:
		return value.IsZero()
	default:
		return false
	}
}

func mcpInputRequiresJSONCoercion(typ reflect.Type) bool {
	if typ.Kind() == reflect.Slice && typ.Elem().Kind() == reflect.Uint8 {
		return true
	}
	if typ == jsonNumberType {
		return true
	}
	if typ.Implements(jsonMarshalerType) || typ.Implements(textMarshalerType) {
		return true
	}
	return typ.Kind() != reflect.Pointer &&
		(reflect.PointerTo(typ).Implements(jsonMarshalerType) ||
			reflect.PointerTo(typ).Implements(textMarshalerType))
}

func consumeMCPInputItems(items *int, amount int) error {
	if amount < 0 || *items < 0 || *items > maxMCPArgumentItems ||
		amount > maxMCPArgumentItems-*items {
		return errMCPResourceLimit
	}
	*items += amount
	return nil
}

type mcpJSONSizeEstimator struct {
	size   uint64
	limit  uint64
	active map[mcpInputVisit]struct{}
	ctx    context.Context
}

type mcpJSONStructField struct {
	name      string
	tagged    bool
	index     []int
	typ       reflect.Type
	omitEmpty bool
}

func estimateMCPJSONOutputSizeContext(
	ctx context.Context,
	value any,
	limit uint64,
) (uint64, error) {
	if ctx == nil {
		return 0, errNilRequestContext
	}
	estimator := mcpJSONSizeEstimator{
		limit:  limit,
		active: make(map[mcpInputVisit]struct{}),
		ctx:    ctx,
	}
	if err := estimator.value(reflect.ValueOf(value), 0); err != nil {
		return 0, err
	}
	return estimator.size, nil
}

func (estimator *mcpJSONSizeEstimator) consume(amount uint64) error {
	if estimator.ctx != nil {
		if err := estimator.ctx.Err(); err != nil {
			return err
		}
	}
	if estimator.size > estimator.limit || amount > estimator.limit-estimator.size {
		return errMCPResourceLimit
	}
	estimator.size += amount
	return nil
}

func (estimator *mcpJSONSizeEstimator) value(value reflect.Value, depth int) error {
	if estimator.ctx != nil {
		if err := estimator.ctx.Err(); err != nil {
			return err
		}
	}
	if depth > maxBundlePathDepth {
		return errMCPResourceLimit
	}
	if !value.IsValid() {
		return estimator.consume(4) // null
	}
	if mcpInputRequiresJSONCoercion(value.Type()) {
		return errMCPArgumentCoercion
	}

	switch value.Kind() {
	case reflect.String:
		return estimator.string(value.String())
	case reflect.Bool:
		if value.Bool() {
			return estimator.consume(4)
		}
		return estimator.consume(5)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return estimator.consume(uint64(len(strconv.FormatInt(value.Int(), 10))))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return estimator.consume(uint64(len(strconv.FormatUint(value.Uint(), 10))))
	case reflect.Float32, reflect.Float64:
		size, err := mcpJSONFloatSize(value.Float(), value.Type().Bits())
		if err != nil {
			return err
		}
		return estimator.consume(size)
	case reflect.Interface:
		if value.IsNil() {
			return estimator.consume(4)
		}
		return estimator.value(value.Elem(), depth+1)
	case reflect.Pointer:
		if value.IsNil() {
			return estimator.consume(4)
		}
		release, err := enterMCPInputContainer(value, estimator.active)
		if err != nil {
			return err
		}
		defer release()
		return estimator.value(value.Elem(), depth+1)
	case reflect.Map:
		return estimator.mapValue(value, depth)
	case reflect.Slice:
		return estimator.sliceValue(value, depth)
	case reflect.Array:
		return estimator.arrayValue(value, depth)
	case reflect.Struct:
		return estimator.structValue(value, depth)
	default:
		return errMCPArgumentCoercion
	}
}

func (estimator *mcpJSONSizeEstimator) mapValue(value reflect.Value, depth int) error {
	if value.Type().Key().Kind() != reflect.String {
		return errMCPArgumentMapKey
	}
	if mcpInputRequiresJSONCoercion(value.Type().Key()) {
		return errMCPArgumentCoercion
	}
	if value.IsNil() {
		return estimator.consume(4)
	}
	release, err := enterMCPInputContainer(value, estimator.active)
	if err != nil {
		return err
	}
	defer release()

	if err := estimator.consume(1); err != nil {
		return err
	}
	keys := value.MapKeys()
	if err := sortSliceContext(estimator.ctx, keys, func(left, right reflect.Value) bool {
		return left.String() < right.String()
	}); err != nil {
		return err
	}
	for index, key := range keys {
		if index != 0 {
			if err := estimator.consume(1); err != nil {
				return err
			}
		}
		if err := estimator.string(key.String()); err != nil {
			return err
		}
		if err := estimator.consume(1); err != nil {
			return err
		}
		if err := estimator.value(value.MapIndex(key), depth+1); err != nil {
			return err
		}
	}
	return estimator.consume(1)
}

func (estimator *mcpJSONSizeEstimator) sliceValue(value reflect.Value, depth int) error {
	if value.Type().Elem().Kind() == reflect.Uint8 {
		return errMCPArgumentCoercion
	}
	if value.IsNil() {
		return estimator.consume(4)
	}
	release, err := enterMCPInputContainer(value, estimator.active)
	if err != nil {
		return err
	}
	defer release()
	return estimator.sequence(value, depth)
}

func (estimator *mcpJSONSizeEstimator) arrayValue(value reflect.Value, depth int) error {
	return estimator.sequence(value, depth)
}

func (estimator *mcpJSONSizeEstimator) sequence(value reflect.Value, depth int) error {
	if err := estimator.consume(1); err != nil {
		return err
	}
	for index := 0; index < value.Len(); index++ {
		if index != 0 {
			if err := estimator.consume(1); err != nil {
				return err
			}
		}
		if err := estimator.value(value.Index(index), depth+1); err != nil {
			return err
		}
	}
	return estimator.consume(1)
}

func (estimator *mcpJSONSizeEstimator) structValue(value reflect.Value, depth int) error {
	fields, err := mcpJSONStructFieldsContext(estimator.ctx, value.Type())
	if err != nil {
		return err
	}
	if err := estimator.consume(1); err != nil {
		return err
	}
	written := 0
	for _, field := range fields {
		fieldValue, ok := mcpJSONFieldByIndex(value, field.index)
		if !ok || field.omitEmpty && mcpInputJSONEmptyValue(fieldValue) {
			continue
		}
		if written != 0 {
			if err := estimator.consume(1); err != nil {
				return err
			}
		}
		written++
		if err := estimator.string(field.name); err != nil {
			return err
		}
		if err := estimator.consume(1); err != nil {
			return err
		}
		if err := estimator.value(fieldValue, depth+1); err != nil {
			return err
		}
	}
	return estimator.consume(1)
}

func (estimator *mcpJSONSizeEstimator) string(value string) error {
	if err := estimator.consume(1); err != nil {
		return err
	}
	for index := 0; index < len(value); {
		current := value[index]
		if current < utf8.RuneSelf {
			var size uint64
			switch current {
			case '\\', '"', '\b', '\f', '\n', '\r', '\t':
				size = 2
			case '<', '>', '&':
				size = 6
			default:
				if current < 0x20 {
					size = 6
				} else {
					size = 1
				}
			}
			if err := estimator.consume(size); err != nil {
				return err
			}
			index++
			continue
		}

		runeValue, size := utf8.DecodeRuneInString(value[index:])
		if runeValue == utf8.RuneError && size == 1 {
			return errMCPArgumentUTF8
		}
		if runeValue == '\u2028' || runeValue == '\u2029' {
			if err := estimator.consume(6); err != nil {
				return err
			}
		} else if err := estimator.consume(uint64(size)); err != nil {
			return err
		}
		index += size
	}
	return estimator.consume(1)
}

func mcpJSONFloatSize(value float64, bits int) (uint64, error) {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, errMCPArgumentValue
	}
	absolute := math.Abs(value)
	format := byte('f')
	if absolute != 0 &&
		(bits == 64 && (absolute < 1e-6 || absolute >= 1e21) ||
			bits == 32 && (float32(absolute) < 1e-6 || float32(absolute) >= 1e21)) {
		format = 'e'
	}
	encoded := strconv.FormatFloat(value, format, -1, bits)
	size := len(encoded)
	if format == 'e' &&
		size >= 4 &&
		encoded[size-4] == 'e' &&
		encoded[size-3] == '-' &&
		encoded[size-2] == '0' {
		size--
	}
	return uint64(size), nil
}

func mcpJSONStructFieldsContext(
	ctx context.Context,
	typ reflect.Type,
) ([]mcpJSONStructField, error) {
	if ctx == nil {
		return nil, errNilRequestContext
	}
	current := []mcpJSONStructField{}
	next := []mcpJSONStructField{{typ: typ}}
	var count, nextCount map[reflect.Type]int
	visited := make(map[reflect.Type]bool)
	var fields []mcpJSONStructField

	for len(next) != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current, next = next, current[:0]
		count, nextCount = nextCount, make(map[reflect.Type]int)
		for _, parent := range current {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if visited[parent.typ] {
				continue
			}
			visited[parent.typ] = true
			for fieldIndex := 0; fieldIndex < parent.typ.NumField(); fieldIndex++ {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				field := parent.typ.Field(fieldIndex)
				if field.Anonymous {
					fieldType := field.Type
					if fieldType.Kind() == reflect.Pointer {
						fieldType = fieldType.Elem()
					}
					if !field.IsExported() && fieldType.Kind() != reflect.Struct {
						continue
					}
				} else if !field.IsExported() {
					continue
				}

				tag, rawTag, hasTag := mcpInputStructJSONTag(field.Tag)
				if hasTag && (!utf8.ValidString(rawTag) || !utf8.ValidString(tag)) {
					return nil, errMCPArgumentKeyUTF8
				}
				if !hasTag {
					tag = ""
				}
				name, options, _ := strings.Cut(tag, ",")
				if name == "-" {
					continue
				}
				if mcpInputJSONTagHasOption(options, "string") ||
					mcpInputJSONTagHasOption(options, "omitzero") {
					return nil, errMCPArgumentCoercion
				}
				if !mcpJSONValidTagName(name) {
					name = ""
				}

				index := make([]int, len(parent.index)+1)
				copy(index, parent.index)
				index[len(parent.index)] = fieldIndex

				fieldType := field.Type
				if fieldType.Name() == "" && fieldType.Kind() == reflect.Pointer {
					fieldType = fieldType.Elem()
				}
				if name != "" || !field.Anonymous || fieldType.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = field.Name
					}
					candidate := mcpJSONStructField{
						name:      name,
						tagged:    tagged,
						index:     index,
						typ:       fieldType,
						omitEmpty: mcpInputJSONTagHasOption(options, "omitempty"),
					}
					fields = append(fields, candidate)
					if count[parent.typ] > 1 {
						fields = append(fields, candidate)
					}
					continue
				}

				nextCount[fieldType]++
				if nextCount[fieldType] == 1 {
					next = append(next, mcpJSONStructField{
						name:  fieldType.Name(),
						index: index,
						typ:   fieldType,
					})
				}
			}
		}
	}

	if err := sortSliceContext(ctx, fields, func(leftField, rightField mcpJSONStructField) bool {
		if leftField.name != rightField.name {
			return leftField.name < rightField.name
		}
		if len(leftField.index) != len(rightField.index) {
			return len(leftField.index) < len(rightField.index)
		}
		if leftField.tagged != rightField.tagged {
			return leftField.tagged
		}
		return mcpJSONCompareFieldIndex(leftField.index, rightField.index) < 0
	}); err != nil {
		return nil, err
	}

	output := fields[:0]
	for start := 0; start < len(fields); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := start + 1
		for end < len(fields) && fields[end].name == fields[start].name {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			end++
		}
		dominant, ok := mcpJSONDominantField(fields[start:end])
		if ok {
			output = append(output, dominant)
		}
		start = end
	}
	fields = output
	if err := sortSliceContext(ctx, fields, func(left, right mcpJSONStructField) bool {
		return mcpJSONCompareFieldIndex(left.index, right.index) < 0
	}); err != nil {
		return nil, err
	}
	return fields, ctx.Err()
}

func mcpJSONDominantField(fields []mcpJSONStructField) (mcpJSONStructField, bool) {
	if len(fields) > 1 &&
		len(fields[0].index) == len(fields[1].index) &&
		fields[0].tagged == fields[1].tagged {
		return mcpJSONStructField{}, false
	}
	return fields[0], true
}

func mcpJSONCompareFieldIndex(left, right []int) int {
	limit := min(len(left), len(right))
	for index := 0; index < limit; index++ {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
}

func mcpJSONFieldByIndex(value reflect.Value, index []int) (reflect.Value, bool) {
	for _, fieldIndex := range index {
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return reflect.Value{}, false
			}
			value = value.Elem()
		}
		value = value.Field(fieldIndex)
	}
	return value, true
}

func mcpJSONValidTagName(name string) bool {
	if name == "" {
		return false
	}
	for _, current := range name {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", current):
		case !unicode.IsLetter(current) && !unicode.IsDigit(current):
			return false
		}
	}
	return true
}

func mcpInputStructFieldsContext(
	ctx context.Context,
	typ reflect.Type,
) ([]mcpInputStructField, error) {
	if ctx == nil {
		return nil, errNilRequestContext
	}
	fields := make([]mcpInputStructField, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		field := typ.Field(index)
		if !utf8.ValidString(field.Name) {
			return nil, errMCPArgumentKeyUTF8
		}

		name, skip, _, _, err := mcpInputStructFieldName(field)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		if field.PkgPath != "" {
			fieldType := field.Type
			if fieldType.Kind() == reflect.Pointer {
				fieldType = fieldType.Elem()
			}
			if !field.Anonymous || fieldType.Kind() != reflect.Struct {
				continue
			}
		}
		if !utf8.ValidString(name) {
			return nil, errMCPArgumentKeyUTF8
		}
		fields = append(fields, mcpInputStructField{
			index: index,
			name:  name,
		})
	}
	if err := sortSliceContext(ctx, fields, func(left, right mcpInputStructField) bool {
		if left.name == right.name {
			return left.index < right.index
		}
		return left.name < right.name
	}); err != nil {
		return nil, err
	}
	return fields, ctx.Err()
}

func mcpInputStructFieldName(field reflect.StructField) (string, bool, bool, bool, error) {
	tag, rawTag, ok := mcpInputStructJSONTag(field.Tag)
	if !ok {
		return field.Name, false, false, false, nil
	}
	if !utf8.ValidString(rawTag) || !utf8.ValidString(tag) {
		return "", false, false, false, errMCPArgumentKeyUTF8
	}
	name, options, _ := strings.Cut(tag, ",")
	if name == "-" {
		return "", true, false, false, nil
	}
	if mcpInputJSONTagHasOption(options, "string") ||
		mcpInputJSONTagHasOption(options, "omitzero") {
		return "", false, false, false, errMCPArgumentCoercion
	}
	if name == "" {
		return field.Name, false, false, mcpInputJSONTagHasOption(options, "omitempty"), nil
	}
	return name, false, true, mcpInputJSONTagHasOption(options, "omitempty"), nil
}

func mcpInputJSONTagHasOption(options, target string) bool {
	for options != "" {
		var option string
		option, options, _ = strings.Cut(options, ",")
		if option == target {
			return true
		}
	}
	return false
}

func mcpInputStructJSONTag(tag reflect.StructTag) (string, string, bool) {
	for tag != "" {
		index := 0
		for index < len(tag) && tag[index] == ' ' {
			index++
		}
		tag = tag[index:]
		if tag == "" {
			break
		}

		index = 0
		for index < len(tag) &&
			tag[index] > ' ' &&
			tag[index] != ':' &&
			tag[index] != '"' &&
			tag[index] != 0x7f {
			index++
		}
		if index == 0 || index+1 >= len(tag) || tag[index] != ':' || tag[index+1] != '"' {
			break
		}
		name := string(tag[:index])
		tag = tag[index+1:]

		index = 1
		for index < len(tag) && tag[index] != '"' {
			if tag[index] == '\\' {
				index++
			}
			index++
		}
		if index >= len(tag) {
			break
		}
		quoted := string(tag[:index+1])
		tag = tag[index+1:]
		if name != "json" {
			continue
		}
		value, err := strconv.Unquote(quoted)
		if err != nil {
			break
		}
		return value, quoted, true
	}
	return "", "", false
}

func enterMCPInputContainer(
	value reflect.Value,
	active map[mcpInputVisit]struct{},
) (func(), error) {
	visit := mcpInputVisit{
		kind:    value.Kind(),
		typ:     value.Type(),
		pointer: value.Pointer(),
	}
	if value.Kind() == reflect.Slice {
		visit.length = value.Len()
		visit.capacity = value.Cap()
	}
	if _, exists := active[visit]; exists {
		return nil, errMCPArgumentCycle
	}
	active[visit] = struct{}{}
	return func() {
		delete(active, visit)
	}, nil
}

func requireLoadableBundle(ctx context.Context, root string) *mcp.CallToolResult {
	if _, err := loadBundleContext(ctx, root); err != nil {
		return bundleLoadError("load bundle", err)
	}
	return nil
}

func requestContextCheckpoint(ctx context.Context, operation string) *mcp.CallToolResult {
	if ctx == nil {
		return bundleDomainError(operation, "request exceeds MCP resource limits", errNilRequestContext)
	}
	if err := ctx.Err(); err != nil {
		return bundleDomainError(operation, "request exceeds MCP resource limits", err)
	}
	return nil
}

func sortSliceContext[T any](
	ctx context.Context,
	values []T,
	less func(left, right T) bool,
) error {
	if ctx == nil {
		return errNilRequestContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	source := make([]T, len(values))
	for index := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		source[index] = values[index]
	}
	destination := make([]T, len(values))
	for width := 1; width < len(values); width *= 2 {
		for left := 0; left < len(values); left += 2 * width {
			if err := ctx.Err(); err != nil {
				return err
			}
			middle := min(left+width, len(values))
			right := min(left+2*width, len(values))
			first, second := left, middle
			for output := left; output < right; output++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				switch {
				case first >= middle:
					destination[output] = source[second]
					second++
				case second >= right:
					destination[output] = source[first]
					first++
				case less(source[second], source[first]):
					destination[output] = source[second]
					second++
				default:
					destination[output] = source[first]
					first++
				}
			}
		}
		source, destination = destination, source
		if width > len(values)/2 {
			break
		}
	}
	for index := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		values[index] = source[index]
	}
	return ctx.Err()
}

func sortStringsContext(ctx context.Context, values []string) error {
	return sortSliceContext(ctx, values, func(left, right string) bool { return left < right })
}

func bundleLoadError(operation string, err error) *mcp.CallToolResult {
	return bundleDomainError(operation, "bundle exceeds MCP resource limits", err)
}

// bundleDomainError is the single MCP projection for errors produced by a
// bounded Bundle capture. Sentinel precedence is contract-significant:
// cancellation wins over resource exhaustion, which wins over integrity;
// operational parsing/path failures are the final generic case.
func bundleDomainError(operation, resourceMessage string, err error) *mcp.CallToolResult {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return stableToolError("operation_cancelled", operation+" was cancelled", true)
	}
	if errors.Is(err, errMCPResourceLimit) ||
		errors.Is(err, errMCPPreviewDiffLimit) ||
		errors.Is(err, errMCPOutputLimit) ||
		errors.Is(err, bundle.ErrYAMLResourceLimit) {
		return stableToolError("resource_limit", resourceMessage, false)
	}
	if errors.Is(err, bundle.ErrInvalidYAMLGraph) {
		return stableToolError("invalid_yaml_graph", "bundle contains an invalid YAML graph", false)
	}
	return toolErrorf("%s: %v", operation, err)
}

func loadBundleContext(ctx context.Context, root string) (*bundle.Bundle, error) {
	observeLegacyToolIO(ctx, func(observer legacyToolIOObserver) func() {
		return observer.sourceOpen
	})
	source := &bundle.FileSystemSource{Root: root}
	return loadOwnedBundleSourceContext(ctx, source)
}

type ownedBundleSource interface {
	bundle.Source
	Close() error
}

// loadOwnedBundleSourceContext closes its source exactly once and joins a
// close failure with any load failure. A close failure clears the otherwise
// usable Bundle so callers cannot accidentally publish output before learning
// that the owned snapshot lifecycle failed.
func loadOwnedBundleSourceContext(
	ctx context.Context,
	source ownedBundleSource,
) (loaded *bundle.Bundle, err error) {
	if source == nil {
		return nil, errNilOwnedBundleSource
	}
	defer func() {
		closeErr := source.Close()
		if closeErr != nil {
			loaded = nil
		}
		err = errors.Join(err, closeErr)
	}()
	return loadBoundedSource(ctx, source)
}

func loadBoundedSource(ctx context.Context, source bundle.Source) (*bundle.Bundle, error) {
	loaded, err := bundle.Load(ctx, &boundedBundleSource{source: source})
	if err != nil {
		return nil, err
	}
	for _, parseError := range loaded.ParseErrors() {
		if errors.Is(parseError.Err, errMCPResourceLimit) {
			return nil, errMCPResourceLimit
		}
	}
	return loaded, nil
}

type boundedBundleSource struct {
	source bundle.Source
	mu     sync.Mutex
	total  int64
}

func (s *boundedBundleSource) Paths(ctx context.Context) ([]string, error) {
	observeLegacyToolIO(ctx, func(observer legacyToolIOObserver) func() {
		return observer.sourceRead
	})
	paths, err := s.source.Paths(ctx)
	if err != nil {
		return nil, err
	}
	if len(paths) > maxConceptItems {
		return nil, errMCPResourceLimit
	}
	for _, path := range paths {
		if len(path) > 4096 || strings.Count(path, "/")+1 > maxBundlePathDepth {
			return nil, errMCPResourceLimit
		}
	}
	return paths, nil
}

func (s *boundedBundleSource) ReadFile(ctx context.Context, path string) ([]byte, error) {
	observeLegacyToolIO(ctx, func(observer legacyToolIOObserver) func() {
		return observer.sourceRead
	})
	data, err := s.source.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxConceptReadBytes {
		return nil, errMCPResourceLimit
	}
	if filepath.Ext(path) == ".md" {
		if err := validateMCPDocumentYAMLContext(ctx, data); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total += int64(len(data))
	if s.total > maxBundleTotalBytes {
		return nil, errMCPResourceLimit
	}
	return data, nil
}

// validateMCPDocumentYAMLContext delegates graph integrity and all five public
// YAML resource dimensions to the Bundle domain boundary. Ordinary document
// syntax, frontmatter, and encoding failures intentionally remain per-file
// Bundle.ParseErrors; only cancellation and typed graph failures abort capture.
func validateMCPDocumentYAMLContext(ctx context.Context, data []byte) error {
	_, err := bundle.ParseDocumentContext(ctx, string(data))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, bundle.ErrYAMLResourceLimit):
		return err
	case errors.Is(err, bundle.ErrInvalidYAMLGraph):
		return err
	default:
		return nil
	}
}

func normalizeBundleRootContext(ctx context.Context, raw string) (string, error) {
	if ctx == nil {
		return "", errNilRequestContext
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if raw == "" {
		return "", fmt.Errorf("bundle_path is required")
	}
	if raw != strings.TrimSpace(raw) {
		return "", fmt.Errorf("bundle_path must not contain surrounding whitespace")
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("bundle_path must be absolute")
	}
	clean := filepath.Clean(raw)
	if raw != clean {
		return "", fmt.Errorf("bundle_path must be clean")
	}
	clean = canonicalMCPBundlePath(clean)
	if err := validateBundleRootPathContext(ctx, clean); err != nil {
		return "", err
	}
	return clean, nil
}

// canonicalMCPBundlePath recognizes only Darwin's kernel-owned aliases. It
// intentionally does not resolve arbitrary links: callers must receive their
// requested path (or this fixed canonical spelling), never an attacker-chosen
// resolved target.
func canonicalMCPBundlePath(path string) string {
	if runtime.GOOS != "darwin" {
		return path
	}
	for alias, physical := range map[string]string{
		"/etc": "/private/etc",
		"/tmp": "/private/tmp",
		"/var": "/private/var",
	} {
		if path == alias {
			return physical
		}
		if strings.HasPrefix(path, alias+string(filepath.Separator)) {
			return physical + strings.TrimPrefix(path, alias)
		}
	}
	return path
}

// validateBundleRootPath checks every requested component while descending
// through directory descriptors. The returned bundle pathname is never a
// symlink target; later source/store opens must independently pin that path.
type bundleRootHandle interface {
	Lstat(string) (os.FileInfo, error)
	Close() error
}

type bundleRootOpener func(string) (bundleRootHandle, error)
type bundleChildRootOpener func(bundleRootHandle, string) (bundleRootHandle, error)

var errNilBundleRoot = errors.New("bundle root opener returned nil root")

func validateBundleRootPathContext(ctx context.Context, path string) error {
	return validateBundleRootPathWithOpeners(
		ctx,
		path,
		func(path string) (bundleRootHandle, error) { return os.OpenRoot(path) },
		func(parent bundleRootHandle, name string) (bundleRootHandle, error) {
			root, ok := parent.(*os.Root)
			if !ok || root == nil {
				return nil, errNilBundleRoot
			}
			return root.OpenRoot(name)
		},
	)
}

func validateBundleRootPathWithOpeners(
	ctx context.Context,
	path string,
	openRoot bundleRootOpener,
	openChild bundleChildRootOpener,
) (err error) {
	if ctx == nil {
		return errNilRequestContext
	}
	if openRoot == nil || openChild == nil {
		return errors.New("nil bundle root opener")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	volumeRoot := filepath.VolumeName(path) + string(filepath.Separator)
	relative, err := filepath.Rel(volumeRoot, path)
	if err != nil {
		return fmt.Errorf("bundle_path is not accessible: %w", err)
	}
	root, err := openRoot(volumeRoot)
	if err != nil {
		return fmt.Errorf("bundle_path is not accessible: %w", err)
	}
	if root == nil {
		return errNilBundleRoot
	}
	opened := []bundleRootHandle{root}
	defer func() {
		for index := len(opened) - 1; index >= 0; index-- {
			err = errors.Join(err, opened[index].Close())
		}
	}()
	if relative == "." {
		return ctx.Err()
	}

	current := root
	components := strings.Split(relative, string(filepath.Separator))
	for index, component := range components {
		if err := ctx.Err(); err != nil {
			return err
		}
		before, err := current.Lstat(component)
		if err != nil {
			return fmt.Errorf("bundle_path is not accessible: %w", err)
		}
		if before.Mode()&os.ModeSymlink != 0 {
			if index == len(components)-1 {
				return fmt.Errorf("bundle_path must not be a symlink")
			}
			return fmt.Errorf("bundle_path must not contain a symlink")
		}
		if !before.IsDir() {
			return fmt.Errorf("bundle_path must be a directory")
		}
		next, err := openChild(current, component)
		if err != nil {
			return fmt.Errorf("bundle_path is not accessible: %w", err)
		}
		if next == nil {
			return errNilBundleRoot
		}
		opened = append(opened, next)
		after, err := next.Lstat(".")
		if err != nil || !os.SameFile(before, after) {
			if err != nil {
				return fmt.Errorf("bundle_path is not accessible: %w", err)
			}
			return fmt.Errorf("bundle_path changed while opening")
		}
		current = next
	}
	return ctx.Err()
}

func requireConceptID(request mcp.CallToolRequest) (bundle.ConceptID, *mcp.CallToolResult) {
	raw, err := request.RequireString("concept_id")
	if err != nil {
		return bundle.ConceptID{}, toolError(err.Error())
	}
	id, err := parseCanonicalConceptID(raw)
	if err != nil {
		return bundle.ConceptID{}, toolError(err.Error())
	}
	return id, nil
}

func parseCanonicalConceptID(raw string) (bundle.ConceptID, error) {
	if raw == "" {
		return bundle.ConceptID{}, fmt.Errorf("concept_id is required")
	}
	if raw != strings.TrimSpace(raw) {
		return bundle.ConceptID{}, fmt.Errorf("concept_id must not contain surrounding whitespace")
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") {
		return bundle.ConceptID{}, fmt.Errorf("concept_id must be bundle-relative")
	}
	if strings.Contains(raw, "\\") || strings.Contains(raw, "//") ||
		strings.HasPrefix(raw, "file:") || strings.Contains(raw, "://") ||
		strings.Contains(raw, ":") ||
		strings.HasPrefix(raw, "/") || strings.HasSuffix(raw, "/") ||
		strings.HasSuffix(raw, ".md") {
		return bundle.ConceptID{}, fmt.Errorf("concept_id is not canonical: %q", raw)
	}
	id, err := bundle.ParseConceptID(raw)
	if err != nil {
		return bundle.ConceptID{}, err
	}
	if id.String() != raw {
		return bundle.ConceptID{}, fmt.Errorf("concept_id is not canonical: %q", raw)
	}
	for _, segment := range id.Segments() {
		if segment == "index" || segment == "log" || segment == "." || segment == ".." ||
			strings.HasPrefix(segment, ".") || strings.Contains(segment, string(filepath.Separator)) {
			return bundle.ConceptID{}, fmt.Errorf("concept_id contains reserved segment %q", segment)
		}
	}
	return id, nil
}

func optionalBool(request mcp.CallToolRequest, key string) (bool, *mcp.CallToolResult) {
	args := request.GetArguments()
	value, ok := args[key]
	if !ok {
		return false, nil
	}
	typed, ok := value.(bool)
	if !ok {
		return false, toolErrorf("argument %q is not a boolean", key)
	}
	return typed, nil
}

func optionalStringDefault(request mcp.CallToolRequest, key, fallback string) (string, *mcp.CallToolResult) {
	args := request.GetArguments()
	value, ok := args[key]
	if !ok {
		return fallback, nil
	}
	typed, ok := value.(string)
	if !ok {
		return "", toolErrorf("argument %q is not a string", key)
	}
	return typed, nil
}

func optionalDate(request mcp.CallToolRequest, key string) (*time.Time, *mcp.CallToolResult) {
	args := request.GetArguments()
	value, ok := args[key]
	if !ok {
		return nil, nil
	}
	raw, ok := value.(string)
	if !ok {
		return nil, toolErrorf("argument %q is not a string", key)
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil || parsed.Format("2006-01-02") != raw {
		return nil, toolErrorf("argument %q must be YYYY-MM-DD", key)
	}
	return &parsed, nil
}

func parseErrorsResult(root string, errors []bundle.ParseError) *mcp.CallToolResult {
	if len(errors) == 0 {
		return nil
	}
	diagnostics := make([]diagnosticDTO, 0, len(errors))
	for _, parseError := range errors {
		diagnostics = append(diagnostics, diagnosticDTO{
			Code:     bundleParseErrorCode(parseError.Err),
			Severity: validator.SeverityError.String(),
			File:     relativeSlashPath(root, parseError.Path),
			Message:  redactSensitivePaths("unparseable concept document: " + parseError.Err.Error()),
		})
	}
	return jsonErrorResult(parseErrorResponse{
		Status:      "error",
		Diagnostics: diagnostics,
	})
}

func bundleParseErrorResult(root, path string, err error) *mcp.CallToolResult {
	return jsonErrorResult(parseErrorResponse{
		Status: "error",
		Diagnostics: []diagnosticDTO{{
			Code:     bundleParseErrorCode(err),
			Severity: validator.SeverityError.String(),
			File:     relativeSlashPath(root, filepath.Join(root, filepath.FromSlash(path))),
			Message:  redactSensitivePaths("unparseable root index document: " + err.Error()),
		}},
	})
}

func bundleParseErrorCode(err error) string {
	switch {
	case errors.Is(err, bundle.ErrInvalidEncoding):
		return "invalid_encoding"
	case errors.Is(err, bundle.ErrUnterminatedFrontmatter):
		return "unterminated_frontmatter"
	case errors.Is(err, bundle.ErrInvalidFrontmatter):
		return "invalid_frontmatter"
	default:
		return "operation_rejected"
	}
}

func reportResponseContext(
	ctx context.Context,
	root string,
	report validator.Report,
) (validateBundleResponse, error) {
	if ctx == nil {
		return validateBundleResponse{}, errNilRequestContext
	}
	diagnostics := make([]diagnosticDTO, 0, len(report.Diagnostics))
	errorsCount, warningsCount, infoCount := 0, 0, 0
	for _, diagnostic := range report.Diagnostics {
		if err := ctx.Err(); err != nil {
			return validateBundleResponse{}, err
		}
		diagnostics = append(diagnostics, diagnosticDTOFromValidator(root, diagnostic).wire)
		switch diagnostic.Severity {
		case validator.SeverityError:
			errorsCount++
		case validator.SeverityWarning:
			warningsCount++
		case validator.SeverityInfo:
			infoCount++
		}
	}
	if err := ctx.Err(); err != nil {
		return validateBundleResponse{}, err
	}
	return validateBundleResponse{
		ScannedFiles: report.ScannedFiles,
		Conformant:   errorsCount == 0,
		Errors:       errorsCount,
		Warnings:     warningsCount,
		Info:         infoCount,
		Diagnostics:  diagnostics,
	}, nil
}

func relativeSlashPath(root, path string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		projected := filepath.ToSlash(path)
		if containsSensitivePath(projected) || bundle.ValidateRevisionPath(projected) != nil {
			return ""
		}
		return projected
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return ""
	}
	projected := filepath.ToSlash(rel)
	if containsSensitivePath(projected) {
		return ""
	}
	return projected
}

func isInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func jsonTextResult(value any) *mcp.CallToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return toolErrorf("marshal JSON: %v", err)
	}
	return mcp.NewToolResultText(string(data))
}

func jsonStructuredResult(fallback, structured any) *mcp.CallToolResult {
	data, err := json.Marshal(fallback)
	if err != nil {
		return toolErrorf("marshal JSON: %v", err)
	}
	return mcp.NewToolResultStructured(structured, string(data))
}

func jsonStructuredResultContext(
	ctx context.Context,
	operation string,
	fallback any,
	structured any,
) *mcp.CallToolResult {
	if result := requestContextCheckpoint(ctx, operation); result != nil {
		return result
	}
	if _, err := estimateMCPJSONOutputSizeContext(ctx, structured, maxBundleTotalBytes); err != nil {
		return bundleDomainError(operation, "tool output exceeds MCP resource limits", err)
	}
	data, err := json.Marshal(fallback)
	if err != nil {
		return toolErrorf("marshal JSON: %v", err)
	}
	if len(data) > maxBundleTotalBytes {
		return stableToolError("resource_limit", "tool output exceeds MCP resource limits", false)
	}
	result := mcp.NewToolResultStructured(structured, string(data))
	if cancelled := requestContextCheckpoint(ctx, operation); cancelled != nil {
		return cancelled
	}
	return result
}

func jsonErrorResult(value any) *mcp.CallToolResult {
	switch typed := value.(type) {
	case writeConceptResponse:
		typed.Diagnostics = sanitizeDiagnosticDTOs(typed.Diagnostics)
		value = typed
	case parseErrorResponse:
		typed.Diagnostics = sanitizeDiagnosticDTOs(typed.Diagnostics)
		value = typed
	}
	result := jsonTextResult(value)
	result.IsError = true
	diagnostics := []wireDiagnostic{}
	appendDiagnostics := func(items []diagnosticDTO) {
		for _, diagnostic := range items {
			code := diagnostic.Code
			if code == "" {
				code = "operation_rejected"
			}
			diagnostics = append(diagnostics, wireDiagnostic{
				Code:     code,
				Severity: normalizeWireSeverity(diagnostic.Severity),
				File:     filepathSafe(diagnostic.File),
				Message:  redactSensitivePaths(diagnostic.Message),
			})
		}
	}
	switch typed := value.(type) {
	case writeConceptResponse:
		appendDiagnostics(typed.Diagnostics)
	case parseErrorResponse:
		appendDiagnostics(typed.Diagnostics)
	}
	result.StructuredContent = errorEnvelope{
		Code:        "operation_rejected",
		Message:     "tool operation was rejected",
		Retryable:   false,
		Diagnostics: diagnostics,
	}
	return result
}

func sanitizeDiagnosticDTOs(diagnostics []diagnosticDTO) []diagnosticDTO {
	sanitized := append([]diagnosticDTO(nil), diagnostics...)
	for index := range sanitized {
		sanitized[index].File = filepathSafe(sanitized[index].File)
		sanitized[index].Message = redactSensitivePaths(sanitized[index].Message)
	}
	return sanitized
}

func normalizeWireSeverity(severity string) string {
	switch strings.ToLower(severity) {
	case "error":
		return "ERROR"
	case "warning", "warn":
		return "WARN"
	default:
		return "INFO"
	}
}

func toolError(message string) *mcp.CallToolResult {
	return stableToolError("invalid_request", redactSensitivePaths(message), false)
}

func toolErrorf(format string, args ...any) *mcp.CallToolResult {
	return toolError(fmt.Sprintf(format, args...))
}

type errorEnvelope struct {
	Code        string           `json:"code"`
	Message     string           `json:"message"`
	Retryable   bool             `json:"retryable"`
	Diagnostics []wireDiagnostic `json:"diagnostics"`
}

type wireDiagnostic struct {
	Code          string `json:"code"`
	Severity      string `json:"severity"`
	File          string `json:"file"`
	Field         string `json:"field"`
	Message       string `json:"message"`
	SpecRef       string `json:"spec_ref"`
	PolicyFailure bool   `json:"policy_failure"`
}

func stableToolError(code, message string, retryable bool) *mcp.CallToolResult {
	code = stableDiagnosticCode(code)
	message = redactSensitivePaths(message)
	result := mcp.NewToolResultError(message)
	result.StructuredContent = errorEnvelope{
		Code:        code,
		Message:     message,
		Retryable:   retryable,
		Diagnostics: []wireDiagnostic{},
	}
	return result
}

func stableDiagnosticCode(code string) string {
	return stableDiagnosticCodeOr(code, "operation_rejected")
}

func stableDiagnosticCodeOr(code, fallback string) string {
	if code == "" {
		return fallback
	}
	for _, character := range code {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' {
			continue
		}
		return fallback
	}
	return code
}

func redactSensitivePaths(message string) string {
	if containsSensitivePath(message) {
		return redactedSensitiveMessage
	}
	return message
}

func containsSensitivePath(message string) bool {
	decoded := message
	for {
		if containsSensitivePathDecoded(decoded) {
			return true
		}
		if !strings.Contains(decoded, "%") {
			return false
		}
		next, err := url.PathUnescape(decoded)
		if err != nil {
			// A malformed escape can hide the boundary between a URI scheme
			// and a local path. Ambiguous error text is never projected.
			return true
		}
		if next == decoded {
			return false
		}
		decoded = next
	}
}

func containsSensitivePathDecoded(message string) bool {
	if strings.Contains(message, "..") || containsFileURIScheme(message) {
		return true
	}
	runes := []rune(message)
	for index := 0; index < len(runes); index++ {
		if end, safe := safeRemoteNetworkURI(runes, index); safe {
			index = end - 1
			continue
		}
		character := runes[index]
		previousIsBoundary := index == 0 || sensitivePathBoundary(runes[index-1])
		switch {
		case character == '/' && previousIsBoundary:
			return true
		case character == '\\' && previousIsBoundary &&
			index+1 < len(runes) && runes[index+1] == '\\':
			return true
		case unicode.IsLetter(character) && previousIsBoundary &&
			index+2 < len(runes) && runes[index+1] == ':' &&
			(runes[index+2] == '/' || runes[index+2] == '\\'):
			return true
		}
	}
	return false
}

func containsFileURIScheme(message string) bool {
	lower := strings.ToLower(message)
	for offset := 0; offset < len(lower); {
		index := strings.Index(lower[offset:], "file:")
		if index < 0 {
			return false
		}
		index += offset
		if index == 0 || !uriSchemeCharacter(rune(lower[index-1])) {
			return true
		}
		offset = index + len("file:")
	}
	return false
}

func safeRemoteNetworkURI(runes []rune, start int) (int, bool) {
	if start > 0 && !sensitivePathBoundary(runes[start-1]) {
		return 0, false
	}
	remaining := strings.ToLower(string(runes[start:]))
	if !strings.HasPrefix(remaining, "http://") && !strings.HasPrefix(remaining, "https://") {
		return 0, false
	}
	end := start
	for end < len(runes) && !remoteURITerminator(runes[end]) {
		end++
	}
	parsed, err := url.Parse(string(runes[start:end]))
	if err != nil ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) ||
		parsed.Host == "" ||
		parsed.Hostname() == "" ||
		parsed.Opaque != "" ||
		remoteURIHasLocalPathPayload(parsed) {
		return 0, false
	}
	return end, true
}

func remoteURIHasLocalPathPayload(parsed *url.URL) bool {
	path := parsed.Path
	if strings.HasPrefix(path, "//") ||
		strings.HasPrefix(path, `\\`) ||
		strings.Contains(path, `\`) ||
		windowsAbsoluteURIPath(path) {
		return true
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return parsed.RawQuery != ""
	}
	for _, entries := range values {
		for _, entry := range entries {
			if containsSensitivePath(entry) {
				return true
			}
		}
	}
	return parsed.Fragment != "" && containsSensitivePath(parsed.Fragment)
}

func windowsAbsoluteURIPath(value string) bool {
	value = strings.TrimPrefix(value, "/")
	runes := []rune(value)
	return len(runes) >= 3 &&
		unicode.IsLetter(runes[0]) &&
		runes[1] == ':' &&
		(runes[2] == '/' || runes[2] == '\\')
}

func remoteURITerminator(character rune) bool {
	return unicode.IsSpace(character) || strings.ContainsRune("\"'`<>", character)
}

func uriSchemeCharacter(character rune) bool {
	return unicode.IsLetter(character) ||
		unicode.IsDigit(character) ||
		character == '+' ||
		character == '-' ||
		character == '.'
}

func sensitivePathBoundary(character rune) bool {
	return unicode.IsSpace(character) || strings.ContainsRune("\"'`()[]{}<>=:,;|", character)
}
