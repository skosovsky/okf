package validator

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/markdownowner"
	"gopkg.in/yaml.v3"
)

// ValidatorConfig controls optional OKF validation layers.
type ValidatorConfig struct {
	Strict       bool
	CheckLinks   bool
	CheckOrphans bool
	// Spec selects the OKF contract assertion. Empty and "auto" use the
	// bundle's shared version resolution policy. Supported explicit values are
	// "0.1" and "0.2".
	Spec string
	// ReferenceDate injects the civil date used by strict stale_after policy.
	// A zero value disables time-dependent diagnostics.
	ReferenceDate time.Time
	// CheckRelations projects semantic relation diagnostics into the validation
	// report. It is deliberately opt-in: OKF base conformance does not
	// require semantic relation policy. Mutation paths enforce blocking relation
	// diagnostics independently of this reporting policy.
	CheckRelations bool
	// checkpoint is a package-private deterministic test seam. Production
	// callers cannot configure it; validation still checks ctx at every pass.
	checkpoint   func()
	stringDigest func(context.Context, string) ([sha256.Size]byte, error)
}

func (c ValidatorConfig) stringDigestOrDefault() func(context.Context, string) ([sha256.Size]byte, error) {
	if c.stringDigest != nil {
		return c.stringDigest
	}
	return validatorStringKeyContext
}

// FileRole classifies bundle files for validation.
type FileRole int

const (
	// RoleConcept is a non-reserved markdown concept document.
	RoleConcept FileRole = iota
	// RoleIndex is a reserved index.md directory listing.
	RoleIndex
	// RoleLog is a reserved log.md update history.
	RoleLog
	// RoleAsset is ignored by OKF validation.
	RoleAsset
)

// DetermineFileRole returns the OKF validation role for a path.
func DetermineFileRole(path string) FileRole {
	name := filepath.Base(path)
	switch name {
	case "index.md":
		return RoleIndex
	case "log.md":
		return RoleLog
	default:
		if filepath.Ext(name) == ".md" {
			return RoleConcept
		}
		return RoleAsset
	}
}

// Severity describes diagnostic impact on OKF conformance.
type Severity int

const (
	// SeverityError is a hard OKF conformance violation.
	SeverityError Severity = iota
	// SeverityWarning is a soft-guidance issue that does not break conformance.
	SeverityWarning
	// SeverityInfo is informational.
	SeverityInfo
)

// String returns a stable severity label.
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "ERROR"
	case SeverityWarning:
		return "WARN"
	case SeverityInfo:
		return "INFO"
	default:
		return "UNKNOWN"
	}
}

// Diagnostic is one validation finding.
type Diagnostic struct {
	// Code is the stable machine-readable identifier for this finding.
	Code string
	// SpecRef identifies the normative clause or explicit toolkit policy behind
	// the finding.
	SpecRef   string
	File      string
	FieldPath string
	Severity  Severity
	Message   string
	// PolicyFailure marks an opt-in toolkit assertion failure. It can affect
	// ExitCode without changing OKF base conformance.
	PolicyFailure bool
	// Source, RelationType and RawTarget preserve the semantic relation that
	// produced this finding. Refs contains Source for relation diagnostics and
	// leaves room for validators which can identify further affected resources.
	// They are empty for legacy document validation diagnostics.
	Source       bundle.RelationRef
	RelationType string
	RawTarget    string
	Refs         []bundle.RelationRef
}

// String returns a human-readable diagnostic.
func (d Diagnostic) String() string {
	prefix := fmt.Sprintf("[%s] ", d.Severity)
	message := d.Message
	if d.Code != "" || d.Source.String() != "" || d.RelationType != "" || d.RawTarget != "" || len(d.Refs) != 0 {
		parts := make([]string, 0, 5)
		if d.Code != "" {
			parts = append(parts, "code="+d.Code)
		}
		if d.SpecRef != "" {
			parts = append(parts, "spec_ref="+d.SpecRef)
		}
		if d.FieldPath != "" {
			parts = append(parts, "field_path="+d.FieldPath)
		}
		if d.PolicyFailure {
			parts = append(parts, "policy_failure=true")
		}
		if d.Source.String() != "" {
			parts = append(parts, "source="+d.Source.String())
		}
		if d.RelationType != "" {
			parts = append(parts, "relation_type="+d.RelationType)
		}
		if d.RawTarget != "" {
			parts = append(parts, "raw_target="+d.RawTarget)
		}
		if len(d.Refs) != 0 {
			refs := make([]string, len(d.Refs))
			for i, ref := range d.Refs {
				refs[i] = ref.String()
			}
			parts = append(parts, "refs="+strings.Join(refs, ","))
		}
		message += " (" + strings.Join(parts, ", ") + ")"
	}
	if d.File != "" {
		return prefix + d.File + ": " + message
	}
	return prefix + message
}

// Report is the result of validating a bundle.
type Report struct {
	Diagnostics        []Diagnostic
	ScannedFiles       int
	VersionDeclaration bundle.VersionDeclarationState
	// Version is the shared bundle contract resolution used for validation.
	// When VersionDeclaration is present but invalid, Version remains the
	// traversal contract used to continue collecting structural diagnostics;
	// consumers must not mistake it for an absent declaration.
	Version bundle.VersionResolution
}

// IsConformant reports whether the bundle has no normative OKF conformance
// errors. Opt-in toolkit policy failures are reported separately.
func (r Report) IsConformant() bool {
	return r.Count(SeverityError) == 0
}

// ExitCode returns the CLI exit code implied by the report.
func (r Report) ExitCode() int {
	if r.IsConformant() && !r.HasPolicyFailures() {
		return 0
	}
	return 1
}

// PolicyFailureCount returns the number of failed explicit toolkit policies.
func (r Report) PolicyFailureCount() int {
	count := 0
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.PolicyFailure {
			count++
		}
	}
	return count
}

// HasPolicyFailures reports whether an explicit selector or opt-in toolkit
// policy failed independently of OKF base conformance.
func (r Report) HasPolicyFailures() bool {
	return r.PolicyFailureCount() != 0
}

// Of returns diagnostics with the requested severity.
func (r Report) Of(severity Severity) []Diagnostic {
	var diagnostics []Diagnostic
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Severity == severity {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	return diagnostics
}

// Count returns the number of diagnostics with the requested severity.
func (r Report) Count(severity Severity) int {
	count := 0
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Severity == severity {
			count++
		}
	}
	return count
}

// ErrorCount returns the number of hard conformance errors.
func (r Report) ErrorCount() int {
	return r.Count(SeverityError)
}

// WarningCount returns the number of warnings.
func (r Report) WarningCount() int {
	return r.Count(SeverityWarning)
}

// InfoCount returns the number of informational diagnostics.
func (r Report) InfoCount() int {
	return r.Count(SeverityInfo)
}

type validator struct {
	ctx            context.Context
	root           string
	cfg            ValidatorConfig
	bundle         *bundle.Bundle
	version        bundle.VersionResolution
	report         Report
	files          []string
	capturedPaths  *validatorStringBuckets
	readErrors     *validatorStringBuckets
	publicationErr error
}

// ValidateBundle validates a loaded bundle against its resolved OKF contract.
func ValidateBundle(b *bundle.Bundle, cfg *ValidatorConfig) Report {
	report, _ := ValidateBundleContext(context.Background(), b, cfg)
	return report
}

// ValidateBundleContext validates a loaded bundle and stops promptly when ctx
// is cancelled. A cancelled validation has no partial-report contract.
func ValidateBundleContext(ctx context.Context, b *bundle.Bundle, cfg *ValidatorConfig) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	config := ValidatorConfig{}
	if cfg != nil {
		config = *cfg
	}

	if b == nil {
		return Report{Diagnostics: []Diagnostic{{
			Code:      "bundle_required",
			SpecRef:   "toolkit#bundle-input",
			File:      ".",
			FieldPath: "bundle",
			Severity:  SeverityError,
			Message:   "nil bundle",
		}}}, nil
	}

	selector := config.Spec
	if selector == "auto" {
		selector = ""
	}
	declarationState, err := b.VersionDeclarationStateContext(ctx)
	if err != nil {
		return Report{}, err
	}
	version, versionErr := b.VersionResolutionContext(ctx, selector)
	declarationMalformed := declarationState.Present && !declarationState.Valid
	policyErr := versionErr
	if declarationMalformed {
		version, _ = bundle.ResolveVersion("", "")
		policyErr = nil
		if selector != "" {
			_, policyErr = bundle.ResolveVersion("", selector)
		}
	} else if versionErr != nil {
		version, _ = b.VersionResolutionContext(ctx, "")
	}
	files, err := b.MarkdownFilesContext(ctx)
	if err != nil {
		return Report{}, err
	}
	capturedFiles, err := b.FilesContext(ctx)
	if err != nil {
		return Report{}, err
	}
	capturedPaths := newValidatorStringBuckets(config.stringDigestOrDefault())
	for _, path := range capturedFiles {
		if _, err := capturedPaths.InsertContext(ctx, path); err != nil {
			return Report{}, err
		}
	}

	v := &validator{
		ctx:           ctx,
		root:          b.Root(),
		cfg:           config,
		bundle:        b,
		version:       version,
		files:         files,
		capturedPaths: capturedPaths,
		readErrors:    newValidatorStringBuckets(config.stringDigestOrDefault()),
	}
	v.report.ScannedFiles = len(v.files)
	v.report.VersionDeclaration = declarationState
	v.report.Version = version
	if policyErr != nil {
		v.report.Diagnostics = append(v.report.Diagnostics, Diagnostic{
			Code:          "version_assertion_failed",
			SpecRef:       "toolkit#version-selector-assertion",
			File:          "index.md",
			FieldPath:     "okf_version",
			Severity:      SeverityWarning,
			Message:       policyErr.Error(),
			PolicyFailure: true,
		})
	}
	if err := v.validate(); err != nil {
		return Report{}, err
	}
	if config.CheckRelations {
		diagnostics, err := b.RelationDiagnosticsContext(ctx)
		if err != nil {
			return Report{}, err
		}
		if err := v.addRelationDiagnostics(diagnostics); err != nil {
			return Report{}, err
		}
	}
	if err := v.sortDiagnostics(); err != nil {
		return Report{}, err
	}
	if err := v.check(); err != nil {
		return Report{}, err
	}
	return v.report, nil
}

// ValidatePath loads and validates a bundle path against its resolved OKF contract.
func ValidatePath(bundlePath string, cfg *ValidatorConfig) Report {
	b, err := bundle.LoadBundle(bundlePath)
	if err != nil {
		return Report{Diagnostics: []Diagnostic{{
			Code:      "bundle_load_failed",
			SpecRef:   "toolkit#bundle-loading",
			File:      bundlePath,
			FieldPath: "bundle",
			Severity:  SeverityError,
			Message:   err.Error(),
		}}}
	}

	return ValidateBundle(b, cfg)
}

// ValidateSource loads and validates source using ctx for every validation pass.
// If ctx is cancelled, it returns ctx.Err and no partial-report contract exists.
func ValidateSource(ctx context.Context, source bundle.Source, cfg *ValidatorConfig) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	b, err := bundle.Load(ctx, source)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Report{}, ctxErr
		}
		detail := err.Error()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Report{}, ctxErr
		}
		detail, copyErr := validatorOwnedStringContext(ctx, detail)
		if copyErr != nil {
			return Report{}, copyErr
		}
		return Report{Diagnostics: []Diagnostic{{
			Code:      "bundle_load_failed",
			SpecRef:   "toolkit#bundle-loading",
			File:      ".",
			FieldPath: "bundle",
			Severity:  SeverityError,
			Message:   detail,
		}}}, nil
	}
	return ValidateBundleContext(ctx, b, cfg)
}

func (v *validator) check() error {
	if v.publicationErr != nil {
		return v.publicationErr
	}
	if v.cfg.checkpoint != nil {
		v.cfg.checkpoint()
	}
	return v.ctx.Err()
}

func (v *validator) validate() error {
	if err := v.validateConceptParseErrors(); err != nil {
		return err
	}
	if err := v.validateConcepts(); err != nil {
		return err
	}
	if err := v.validateReservedFiles(); err != nil {
		return err
	}
	if v.cfg.Strict {
		switch v.version.Compatibility {
		case bundle.VersionCompatibilityLegacy:
			if err := v.validateStrictLegacy(); err != nil {
				return err
			}
		case bundle.VersionCompatibilityBestEffort:
			// Future contracts retain only the §11 base boundary. Optional
			// families may be redefined and are not safe strict guidance.
		default:
			if err := v.validateStrictV02(); err != nil {
				return err
			}
		}
	}
	if v.cfg.CheckLinks {
		if err := v.validateLinks(); err != nil {
			return err
		}
	}
	if v.cfg.CheckOrphans {
		if err := v.validateOrphans(); err != nil {
			return err
		}
	}
	return v.check()
}

func (v *validator) validateConceptParseErrors() error {
	for _, parseError := range v.bundle.ParseErrors() {
		if err := v.check(); err != nil {
			return err
		}
		if err := v.ctx.Err(); err != nil {
			return err
		}
		detail := parseError.Err.Error()
		if err := v.ctx.Err(); err != nil {
			return err
		}
		message := v.diagnosticMessage("unparseable concept document: %s", detail)
		v.addDocumentReadError(parseError.Path, message)
	}
	return nil
}

func (v *validator) validateConcepts() error {
	ids, err := v.bundle.ConceptIDsContext(v.ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := v.check(); err != nil {
			return err
		}
		path, ok, err := v.bundle.ConceptPathContext(v.ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		document, ok, err := v.readDocumentContext(path, true)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		concept := bundle.NewConcept(id, path, document)
		if !concept.Document.HasFrontmatter {
			v.addSpecAt(SeverityError, "concept_frontmatter_missing", v.specRef("11.1"), concept.Path, "frontmatter", "missing YAML frontmatter block")
			continue
		}
		_, validType, err := concept.Document.Frontmatter.TypeContext(v.ctx)
		if err != nil {
			return err
		}
		if !validType {
			message := "missing or empty 'type' field"
			state, err := concept.Document.Frontmatter.SemanticValueStateContext(v.ctx, "type")
			if err != nil {
				return err
			}
			if state.Ambiguous {
				message = "'type' field should be declared exactly once"
			}
			v.addSpecAt(SeverityError, "concept_type_invalid", v.specRef("11.2"), concept.Path, "type", message)
		}
	}
	return nil
}

func (v *validator) validateReservedFiles() error {
	rootIndex := filepath.Join(v.root, "index.md")

	for _, path := range v.bundle.IndexFiles() {
		if err := v.check(); err != nil {
			return err
		}
		document, ok, err := v.readDocumentContext(path, true)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if document.HasFrontmatter {
			if !samePath(path, rootIndex) {
				v.addIndexError(path, "frontmatter", "index.md should not contain frontmatter")
				continue
			}
			keys, err := document.Frontmatter.KeysContext(v.ctx)
			if err != nil {
				return err
			}
			for _, key := range keys {
				if err := v.check(); err != nil {
					return err
				}
				if key != "okf_version" {
					v.addIndexError(path, key, "root index.md frontmatter should declare only 'okf_version'")
					break
				}
			}
			if len(keys) == 0 {
				v.addVersionDeclarationTypeError(path)
			} else if state, err := document.Frontmatter.SemanticValueStateContext(v.ctx, "okf_version"); err != nil {
				return err
			} else if state.Ambiguous {
				v.addVersionDeclarationAmbiguousError(path)
			} else if len(keys) == 1 && keys[0] == "okf_version" {
				state, err := document.Frontmatter.VersionDeclarationStateContext(v.ctx)
				if err != nil {
					return err
				}
				if !state.Valid {
					node, present, err := document.Frontmatter.SemanticGetContext(v.ctx, "okf_version")
					if err != nil {
						return err
					}
					if present && node != nil && node.Kind == yaml.ScalarNode &&
						node.Tag == "!!str" && strings.TrimSpace(node.Value) != "" {
						v.addVersionDeclarationSyntaxError(path)
					} else {
						v.addVersionDeclarationTypeError(path)
					}
				}
			}
		}
		// O1 needs empty nested indexes to remain usable as orphan coverage
		// surfaces; the base E4 empty-index fixture still rejects this outside
		// orphan-check mode.
		nonBlankBody, err := nonBlankValidatorStringContext(v.ctx, document.Body)
		if err != nil {
			return err
		}
		if v.cfg.CheckOrphans && !samePath(path, rootIndex) && !nonBlankBody {
			continue
		}
		if err := v.validateIndexBody(path, document.Body); err != nil {
			return err
		}
	}

	for _, path := range v.bundle.LogFiles() {
		if err := v.check(); err != nil {
			return err
		}
		document, ok, err := v.readDocumentContext(path, true)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if document.HasFrontmatter {
			v.addLogError(path, "frontmatter", "log.md should not contain frontmatter")
			continue
		}
		if err := v.validateLog(path, document.Body); err != nil {
			return err
		}
	}
	return nil
}

func (v *validator) validateIndexBody(path, body string) error {
	seenHeading := false
	seenEntry := false
	sectionHeading := ""
	sectionHasEntry := false

	blocks, err := markdownowner.TopLevelStructureContext(v.ctx, body)
	if err != nil {
		return err
	}
	for _, block := range blocks {
		if err := v.check(); err != nil {
			return err
		}
		if block.Kind == markdownowner.TopLevelHeading {
			if seenHeading && !sectionHasEntry {
				v.addIndexError(path, "body", v.diagnosticMessage("index.md section has no entries: %q", sectionHeading))
			}
			seenHeading = true
			sectionHeading = block.Text
			sectionHasEntry = false
			continue
		}
		if block.Kind != markdownowner.TopLevelList || block.Indent != 0 {
			continue
		}
		for _, item := range block.Items {
			if !seenHeading {
				v.addIndexError(path, "body", "index.md list entry appears before any heading")
				continue
			}
			if !item.HasLink {
				v.addIndexError(path, "body", "index.md list entry should contain a Markdown link")
				continue
			}
			seenEntry = true
			sectionHasEntry = true
		}
	}

	if !seenHeading {
		v.addIndexError(path, "body", "index.md should contain at least one heading")
		return nil
	}
	if !sectionHasEntry {
		v.addIndexError(path, "body", v.diagnosticMessage("index.md section has no entries: %q", sectionHeading))
	}
	if !seenEntry {
		v.addIndexError(path, "body", "index.md should contain at least one linked list entry")
	}
	return nil
}

func (v *validator) validateLog(path, body string) error {
	nonBlankBody, err := nonBlankValidatorStringContext(v.ctx, body)
	if err != nil {
		return err
	}
	if !nonBlankBody {
		return nil
	}

	type logDay struct {
		date    string
		entries int
	}
	var (
		days          []logDay
		current       *logDay
		badDateLevels []string
		nonListLines  []string
	)
	blocks, err := markdownowner.TopLevelStructureContext(v.ctx, body)
	if err != nil {
		return err
	}
	for _, block := range blocks {
		if err := v.check(); err != nil {
			return err
		}
		switch block.Kind {
		case markdownowner.TopLevelHeading:
			if block.HeadingLevel == 2 {
				if current != nil {
					days = append(days, *current)
				}
				current = &logDay{date: block.Text}
				continue
			}
			if IsISODate(block.Text) {
				badDateLevels = append(badDateLevels, block.Raw)
			} else if current != nil {
				nonListLines = append(nonListLines, block.Raw)
			}
		case markdownowner.TopLevelList:
			if current == nil {
				continue
			}
			for _, item := range block.Items {
				if block.Indent != 0 || item.Indent != 0 || item.HasNestedList {
					nonListLines = append(nonListLines, strings.TrimSpace(item.Raw))
					continue
				}
				current.entries++
				nonListLines = append(nonListLines, item.ContinuationTexts...)
			}
		default:
			if current != nil && strings.TrimSpace(block.Raw) != "" {
				nonListLines = append(nonListLines, block.Raw)
			}
		}
	}
	if current != nil {
		days = append(days, *current)
	}

	if len(days) == 0 && len(badDateLevels) == 0 {
		v.addLogError(path, "body", "log.md should contain ISO-8601 date headings")
	}
	for _, day := range days {
		if err := v.check(); err != nil {
			return err
		}
		if !IsISODate(day.date) {
			v.addLogError(path, "body", v.diagnosticMessage("log date heading is not ISO-8601 YYYY-MM-DD: %q", day.date))
		}
	}
	for _, badLevel := range badDateLevels {
		if err := v.check(); err != nil {
			return err
		}
		v.addLogError(path, "body", v.diagnosticMessage("log date heading should use level 2: %q", badLevel))
	}
	for _, day := range days {
		if err := v.check(); err != nil {
			return err
		}
		if day.entries == 0 {
			v.addLogError(path, "body", v.diagnosticMessage("log date heading has no entries: %q", day.date))
		}
	}
	previousDate := ""
	for _, day := range days {
		if err := v.check(); err != nil {
			return err
		}
		if !IsISODate(day.date) {
			continue
		}
		if previousDate != "" && day.date > previousDate {
			v.addLogError(path, "body", v.diagnosticMessage("log date headings should be newest first: %q", day.date))
		}
		previousDate = day.date
	}
	for _, line := range nonListLines {
		if err := v.check(); err != nil {
			return err
		}
		v.addLogError(path, "body", v.diagnosticMessage("log date group contains non-list entry: %q", line))
	}
	return nil
}

func (v *validator) validateStrictLegacy() error {
	ids, err := v.bundle.ConceptIDsContext(v.ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := v.check(); err != nil {
			return err
		}
		path, ok, err := v.bundle.ConceptPathContext(v.ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		document, ok, err := v.readDocumentContext(path, false)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		concept := bundle.NewConcept(id, path, document)
		if err := v.validateStrictFrontmatter(concept); err != nil {
			return err
		}
		if err := v.validateConventionalSections(concept); err != nil {
			return err
		}
	}
	for _, path := range v.bundle.IndexFiles() {
		if err := v.check(); err != nil {
			return err
		}
		document, ok, err := v.readDocumentContext(path, true)
		if err != nil {
			return err
		}
		if ok {
			if err := v.validateIndexDescriptions(path, document.Body); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *validator) validateStrictFrontmatter(concept bundle.Concept) error {
	frontmatter := concept.Document.Frontmatter
	for _, field := range []string{"title", "description", "tags", "timestamp"} {
		if err := v.check(); err != nil {
			return err
		}
		value, ok, err := frontmatter.SemanticGetContext(v.ctx, field)
		if err != nil {
			return err
		}
		if !ok || isEmptyYAMLValue(value) {
			v.legacyWarn("legacy_recommended_field_missing", "4.1", concept.Path, field, fmt.Sprintf("missing recommended frontmatter field '%s'", field))
		}
	}

	if state, err := frontmatter.TagsStateContext(v.ctx); err != nil {
		return err
	} else if state.Present {
		if !state.Valid {
			v.legacyWarn("tags_invalid", "4.1", concept.Path, "tags", "'tags' should be a YAML list of strings")
		}
		for range state.Items {
			if err := v.check(); err != nil {
				return err
			}
		}
	}

	if state, err := frontmatter.TimestampStateContext(v.ctx); err != nil {
		return err
	} else if state.Present {
		if !state.Valid {
			message := "'timestamp' should be an RFC3339 string"
			if state.Raw != "" {
				message = v.diagnosticMessage("'timestamp' is not RFC3339: %q", state.Raw)
			}
			v.legacyWarn("timestamp_invalid", "4.1", concept.Path, "timestamp", message)
		}
	}

	if state, err := frontmatter.ResourceStateContext(v.ctx); err != nil {
		return err
	} else if state.Present {
		if state.Valid {
			valid, err := isValidURIContext(v.ctx, state.Value)
			if err != nil {
				return err
			}
			if !valid {
				v.legacyWarn("path_value_invalid", "4.1", concept.Path, "resource", v.diagnosticMessage("'resource' is not a valid URI: %q", state.Value))
			}
		} else if node, ok, err := frontmatter.SemanticGetContext(v.ctx, "resource"); err != nil {
			return err
		} else if ok {
			if resource, displayable := displayString(node); displayable {
				v.legacyWarn("path_value_invalid", "4.1", concept.Path, "resource", v.diagnosticMessage("'resource' is not a valid URI: %q", resource))
			} else {
				v.legacyWarn("path_value_invalid", "4.1", concept.Path, "resource", "'resource' should be a URI string")
			}
		} else {
			v.legacyWarn("path_value_invalid", "4.1", concept.Path, "resource", "'resource' should be a URI string")
		}
	}
	return nil
}

func (v *validator) validateConventionalSections(concept bundle.Concept) error {
	body := concept.Document.Body
	observation, err := concept.Document.LegacyFallbackObservationContext(v.ctx)
	if err != nil {
		return err
	}
	if observation.CitationsActive {
		if err := v.validateLegacyCitations(concept); err != nil {
			return err
		}
	}

	section, ok, err := topLevelSectionBlocksContext(v.ctx, body, "Examples")
	if err != nil {
		return err
	}
	concrete, err := hasConcreteExampleContext(v.ctx, section)
	if err != nil {
		return err
	}
	if ok && !concrete {
		v.legacyWarn("conventional_section_invalid", "4.2", concept.Path, "body.# Examples", "'# Examples' should contain a concrete example")
	}

	typ, typed, err := concept.Document.Frontmatter.TypeContext(v.ctx)
	if err != nil {
		return err
	}
	if typed && strings.EqualFold(typ, "BigQuery Table") {
		hasSchema, err := hasTopLevelHeadingContext(v.ctx, body, "Schema")
		if err != nil {
			return err
		}
		if !hasSchema {
			v.legacyWarn("conventional_section_invalid", "4.2", concept.Path, "body.# Schema", "BigQuery Table concepts should include a '# Schema' section")
		}
	}
	return nil
}

func (v *validator) validateLegacyCitations(concept bundle.Concept) error {
	ownership, err := markdownowner.CollectMarkdownMigrationOwnership(v.ctx, []byte(concept.Document.Body))
	if err != nil {
		if ctxErr := v.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if ctxErr := v.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		detail := err.Error()
		if ctxErr := v.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		v.citationWarn(concept.Path, v.diagnosticMessage("invalid '# Citations' ownership: %s", detail))
		return nil
	}
	if len(ownership.NumericMarkers) == 0 {
		return nil
	}
	if ownership.CitationsSectionIndex < 0 {
		v.citationWarn(concept.Path, "citation markers require a bottom '# Citations' section")
		return nil
	}
	lastTopLevel := -1
	for index, section := range ownership.Sections {
		if section.Level == 1 {
			lastTopLevel = index
		}
	}
	if ownership.CitationsSectionIndex != lastTopLevel {
		v.citationWarn(concept.Path, "citation markers require a bottom '# Citations' section")
		return nil
	}
	if len(ownership.CitationEntries) == 0 {
		v.citationWarn(concept.Path, "'# Citations' should contain numbered citation entries")
		return nil
	}
	for i, citation := range ownership.CitationEntries {
		if err := v.check(); err != nil {
			return err
		}
		number := citation.Number
		if number == 0 {
			number = uint64(citation.Ordinal)
		}
		want := uint64(i + 1)
		if number != want {
			v.citationWarn(concept.Path, fmt.Sprintf("citation numbering should be contiguous starting at 1: got %d, want %d", number, want))
		}
		if citation.Resource == "" || !isValidCitationTarget(citation.Resource) {
			v.citationWarn(concept.Path, v.diagnosticMessage("citation entry has invalid target: %q", citation.Raw))
		}
	}
	return nil
}

func (v *validator) citationWarn(path, message string) {
	specRef := v.specRef("4.2")
	if v.version.Effective == bundle.OKFVersion {
		specRef = "okf-v0.2#13.1"
	}
	v.addSpecAt(SeverityWarning, "citation_integrity_invalid", specRef, path, "body.# Citations", message)
}

func (v *validator) validateIndexDescriptions(path, body string) error {
	blocks, err := markdownowner.TopLevelStructureContext(v.ctx, body)
	if err != nil {
		return err
	}
	for _, block := range blocks {
		if err := v.check(); err != nil {
			return err
		}
		if block.Kind != markdownowner.TopLevelList || block.Indent != 0 {
			continue
		}
		for _, item := range block.Items {
			if len(item.Links) == 0 {
				continue
			}
			link := item.Links[0]
			targetPath, ok, err := v.resolveLinkPath(path, link.Target)
			if err != nil {
				return err
			}
			if !ok || DetermineFileRole(targetPath) != RoleConcept {
				continue
			}
			document, ok, err := v.readDocumentContext(targetPath, false)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			want, ok, err := document.Frontmatter.DescriptionContext(v.ctx)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if item.Description != want {
				v.legacyWarn("index_description_mismatch", "8", path, "body", v.diagnosticMessage("index.md description for %q does not match target frontmatter", link.Target))
			}
		}
	}
	return nil
}

func (v *validator) validateLinks() error {
	for _, path := range v.files {
		if err := v.check(); err != nil {
			return err
		}
		document, ok, err := v.readDocumentContext(path, true)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		body := document.Body
		if !document.HasFrontmatter && DetermineFileRole(path) != RoleConcept {
			body = document.Body
		}
		links, err := bundle.ExtractLinksContext(v.ctx, body)
		if err != nil {
			return err
		}
		for i, link := range links {
			if err := v.check(); err != nil {
				return err
			}
			fieldPath := fmt.Sprintf("body.links[%d].target", i)
			if err := v.validateLink(path, fieldPath, link); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *validator) validateLink(sourcePath, fieldPath string, link bundle.Link) error {
	if err := v.check(); err != nil {
		return err
	}
	if link.Kind == bundle.LinkExternal || link.Kind == bundle.LinkOther {
		return nil
	}

	targetPath, targetKind, fragment, ok, err := v.resolveLinkTargetContext(sourcePath, link.Target)
	if err != nil {
		return err
	}
	if !ok {
		v.addSpecAt(SeverityInfo, "link_target_missing", "toolkit#link-resolution", sourcePath, fieldPath, v.diagnosticMessage("broken link to %q (target not found)", link.Target))
		return nil
	}
	if targetKind == RoleAsset {
		return nil
	}
	if fragment == "" {
		return nil
	}

	headingPath := targetPath
	if targetKind == RoleIndex && isDirectoryLink(link.Target) {
		headingPath = filepath.Join(targetPath, "index.md")
	}
	if err := v.check(); err != nil {
		return err
	}
	exists, err := v.headingExists(headingPath, fragment)
	if err != nil {
		return err
	}
	if !exists {
		v.addSpecAt(SeverityWarning, "link_anchor_missing", "toolkit#anchor-resolution", sourcePath, fieldPath, v.diagnosticMessage("anchor not found in target file: %s", link.Target))
	}
	return nil
}

func (v *validator) validateOrphans() error {
	type directoryConcepts struct {
		dir   string
		paths []string
	}
	var byDir []directoryConcepts
	for _, path := range v.files {
		if err := v.check(); err != nil {
			return err
		}
		if DetermineFileRole(path) == RoleConcept {
			dir := filepath.Dir(path)
			found := false
			for index := range byDir {
				result, err := compareValidatorStringsContext(v.ctx, byDir[index].dir, dir)
				if err != nil {
					return err
				}
				if result == 0 {
					byDir[index].paths = append(byDir[index].paths, path)
					found = true
					break
				}
			}
			if !found {
				owned, err := validatorOwnedStringContext(v.ctx, dir)
				if err != nil {
					return err
				}
				byDir = append(byDir, directoryConcepts{dir: owned, paths: []string{path}})
			}
		}
	}

	dirs := append([]directoryConcepts(nil), byDir...)
	var compareErr error
	if err := stableSortWithCheck(v.check, dirs, func(left, right directoryConcepts) bool {
		if compareErr != nil {
			return false
		}
		var result int
		result, compareErr = compareValidatorStringsContext(v.ctx, left.dir, right.dir)
		return result < 0
	}); err != nil {
		return err
	}
	if compareErr != nil {
		return compareErr
	}

	for _, entry := range dirs {
		dir := entry.dir
		if err := v.check(); err != nil {
			return err
		}
		indexPath := filepath.Join(dir, "index.md")
		if v.root == "" {
			if _, ok, err := v.bundle.ReadFileContext(v.ctx, indexPath); err != nil {
				return err
			} else if !ok {
				v.addSpecAt(SeverityInfo, "orphan_index_missing", "toolkit#orphan-missing-index", dir, "index.md", "missing index.md, skipping orphan check for this directory")
				continue
			}
			covered, err := v.indexCoveredConcepts(indexPath)
			if err != nil {
				return err
			}
			for _, conceptPath := range entry.paths {
				if err := v.check(); err != nil {
					return err
				}
				cleanConcept := filepath.Clean(conceptPath)
				if _, err := validatorStringKeyContext(v.ctx, cleanConcept); err != nil {
					return err
				}
				isCovered, err := covered.ContainsContext(v.ctx, cleanConcept)
				if err != nil {
					return err
				}
				if !isCovered {
					relIndex, err := v.relContext(indexPath)
					if err != nil {
						return err
					}
					v.addSpecAt(SeverityWarning, "orphan_unlisted", "toolkit#orphan-coverage", conceptPath, "index.md.body", v.diagnosticMessage("orphan file (not linked in %s)", relIndex))
				}
			}
			continue
		}
		if _, ok, err := v.bundle.ReadFileContext(v.ctx, indexPath); err != nil {
			return err
		} else if !ok {
			v.addSpecAt(SeverityInfo, "orphan_index_missing", "toolkit#orphan-missing-index", dir, "index.md", "missing index.md, skipping orphan check for this directory")
			continue
		}
		covered, err := v.indexCoveredConcepts(indexPath)
		if err != nil {
			return err
		}
		for _, conceptPath := range entry.paths {
			if err := v.check(); err != nil {
				return err
			}
			cleanConcept := filepath.Clean(conceptPath)
			if _, err := validatorStringKeyContext(v.ctx, cleanConcept); err != nil {
				return err
			}
			isCovered, err := covered.ContainsContext(v.ctx, cleanConcept)
			if err != nil {
				return err
			}
			if !isCovered {
				relIndex, err := v.relContext(indexPath)
				if err != nil {
					return err
				}
				v.addSpecAt(SeverityWarning, "orphan_unlisted", "toolkit#orphan-coverage", conceptPath, "index.md.body", v.diagnosticMessage("orphan file (not linked in %s)", relIndex))
			}
		}
	}
	return nil
}

func (v *validator) indexCoveredConcepts(indexPath string) (*validatorStringBuckets, error) {
	covered := v.newStringBuckets()
	document, ok, err := v.readDocumentContext(indexPath, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		return covered, nil
	}
	links, err := bundle.ExtractLinksContext(v.ctx, document.Body)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if v.check() != nil {
			return nil, v.ctx.Err()
		}
		targetPath, ok, err := v.resolveLinkPath(indexPath, link.Target)
		if err != nil {
			return nil, err
		}
		if !ok || DetermineFileRole(targetPath) != RoleConcept {
			continue
		}
		if filepath.Dir(targetPath) == filepath.Dir(indexPath) {
			cleanTarget := filepath.Clean(targetPath)
			if _, err := validatorStringKeyContext(v.ctx, cleanTarget); err != nil {
				return nil, err
			}
			if _, err := covered.InsertContext(v.ctx, cleanTarget); err != nil {
				return nil, err
			}
		}
	}
	return covered, v.check()
}

func (v *validator) readDocumentContext(path string, allowReservedNoFrontmatter bool) (bundle.Document, bool, error) {
	if err := v.check(); err != nil {
		return bundle.Document{}, false, err
	}
	data, ok, err := v.bundle.ReadFileContext(v.ctx, path)
	if err != nil {
		return bundle.Document{}, false, err
	}
	if !ok {
		return bundle.Document{}, false, nil
	}
	if err := v.check(); err != nil {
		return bundle.Document{}, false, err
	}
	document, err := bundle.ParseDocumentContext(v.ctx, string(data))
	if err != nil {
		if ctxErr := v.ctx.Err(); ctxErr != nil {
			return bundle.Document{}, false, ctxErr
		}
		v.addDocumentReadError(path, err.Error())
		return bundle.Document{}, false, v.check()
	}
	if err := v.check(); err != nil {
		return bundle.Document{}, false, err
	}
	if !allowReservedNoFrontmatter && !document.HasFrontmatter {
		return bundle.Document{}, false, nil
	}
	return document, true, v.check()
}

func (v *validator) resolveLinkPath(sourcePath, target string) (string, bool, error) {
	targetPath, _, _, ok, err := v.resolveLinkTargetContext(sourcePath, target)
	return targetPath, ok, err
}

func (v *validator) resolveLinkTargetContext(sourcePath, target string) (string, FileRole, string, bool, error) {
	if err := v.ctx.Err(); err != nil {
		return "", RoleAsset, "", false, err
	}
	for offset := 0; offset < len(target); offset += 64 << 10 {
		if err := v.ctx.Err(); err != nil {
			return "", RoleAsset, "", false, err
		}
	}
	rawPath, fragment := splitFragment(target)
	if err := v.ctx.Err(); err != nil {
		return "", RoleAsset, "", false, err
	}
	nonBlank, err := nonBlankValidatorStringContext(v.ctx, rawPath)
	if err != nil {
		return "", RoleAsset, "", false, err
	}
	if !nonBlank {
		if err := resolveLinkTargetFinalContext(v.ctx); err != nil {
			return "", RoleAsset, "", false, err
		}
		return sourcePath, DetermineFileRole(sourcePath), fragment, true, nil
	}

	var candidate string
	if strings.HasPrefix(rawPath, "/") {
		candidate = filepath.Join(v.root, strings.TrimLeft(rawPath, "/"))
	} else {
		candidate = filepath.Join(filepath.Dir(sourcePath), rawPath)
	}
	if err := v.ctx.Err(); err != nil {
		return "", RoleAsset, "", false, err
	}
	candidate = filepath.Clean(candidate)
	if err := v.ctx.Err(); err != nil {
		return "", RoleAsset, "", false, err
	}
	if v.root == "" && (candidate == ".." || strings.HasPrefix(candidate, ".."+string(filepath.Separator))) {
		if err := resolveLinkTargetFinalContext(v.ctx); err != nil {
			return "", RoleAsset, "", false, err
		}
		return "", RoleAsset, fragment, false, nil
	}
	if v.root != "" && !isInside(v.root, candidate) {
		if err := resolveLinkTargetFinalContext(v.ctx); err != nil {
			return "", RoleAsset, "", false, err
		}
		return "", RoleAsset, fragment, false, nil
	}
	ok, err := v.capturedPaths.ContainsContext(v.ctx, candidate)
	if err != nil {
		return "", RoleAsset, "", false, err
	}
	role := DetermineFileRole(candidate)
	if !ok {
		indexPath := filepath.Join(candidate, "index.md")
		if err := v.ctx.Err(); err != nil {
			return "", RoleAsset, "", false, err
		}
		ok, err = v.capturedPaths.ContainsContext(v.ctx, indexPath)
		if err != nil {
			return "", RoleAsset, "", false, err
		}
		if ok {
			role = RoleIndex
		}
	}
	if err := resolveLinkTargetFinalContext(v.ctx); err != nil {
		return "", RoleAsset, "", false, err
	}
	return candidate, role, fragment, ok, nil
}

func resolveLinkTargetFinalContext(ctx context.Context) error {
	return ctx.Err()
}

func (v *validator) headingExists(path, fragment string) (bool, error) {
	if err := v.check(); err != nil {
		return false, err
	}
	if _, err := validatorStringKeyContext(v.ctx, path); err != nil {
		return false, err
	}
	document, ok, err := v.readDocumentContext(path, true)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if err := v.check(); err != nil {
		return false, err
	}
	headings, err := markdownHeadingIDsContext(v.ctx, document.Body, v.check, v.cfg.stringDigestOrDefault())
	if err != nil {
		return false, err
	}
	fragment = strings.TrimPrefix(fragment, "#")
	if _, err := validatorStringKeyContext(v.ctx, fragment); err != nil {
		return false, err
	}
	ok, err = headings.ContainsContext(v.ctx, fragment)
	return ok, err
}

func (v *validator) addSpecAt(severity Severity, code, specRef, path, fieldPath, message string) {
	if v.publicationErr != nil {
		return
	}
	values := []*string{&code, &specRef, &path, &fieldPath, &message}
	for _, value := range values {
		owned, err := validatorOwnedStringContext(v.ctx, *value)
		if err != nil {
			v.publicationErr = err
			return
		}
		*value = owned
	}
	file, err := v.relContext(path)
	if err != nil {
		v.publicationErr = err
		return
	}
	if err := v.ctx.Err(); err != nil {
		v.publicationErr = err
		return
	}
	v.report.Diagnostics = append(v.report.Diagnostics, Diagnostic{
		Code:      code,
		SpecRef:   specRef,
		File:      file,
		FieldPath: fieldPath,
		Severity:  severity,
		Message:   message,
	})
}

func (v *validator) diagnosticMessage(format string, values ...any) string {
	if v.publicationErr != nil {
		return ""
	}
	message, err := validatorDiagnosticMessageContext(v.ctx, format, values...)
	if err != nil {
		v.publicationErr = err
		return ""
	}
	return message
}

func (v *validator) addIndexError(path, fieldPath, message string) {
	v.addSpecAt(SeverityError, "index_structure_invalid", v.specRef("11.3"), path, fieldPath, message)
}

func (v *validator) addLogError(path, fieldPath, message string) {
	v.addSpecAt(SeverityError, "log_structure_invalid", v.specRef("11.3"), path, fieldPath, message)
}

func (v *validator) addVersionDeclarationTypeError(path string) {
	v.addSpecAt(SeverityError, "index_structure_invalid", "okf-v0.2#12", path, "okf_version", "root index.md okf_version should be a non-empty string")
}

func (v *validator) addVersionDeclarationSyntaxError(path string) {
	v.addSpecAt(SeverityError, "index_structure_invalid", "okf-v0.2#12", path, "okf_version", "root index.md okf_version should use canonical <major>.<minor> syntax")
}

func (v *validator) addVersionDeclarationAmbiguousError(path string) {
	v.addSpecAt(SeverityError, "index_structure_invalid", "okf-v0.2#12", path, "okf_version", "root index.md okf_version should be declared exactly once")
}

func (v *validator) addDocumentReadError(path, message string) {
	key := filepath.Clean(path)
	if _, err := validatorStringKeyContext(v.ctx, key); err != nil {
		return
	}
	reported, err := v.readErrors.ContainsContext(v.ctx, key)
	if err != nil {
		v.publicationErr = err
		return
	}
	if reported {
		return
	}
	if _, err := v.readErrors.InsertContext(v.ctx, key); err != nil {
		v.publicationErr = err
		return
	}
	switch DetermineFileRole(path) {
	case RoleIndex:
		v.addIndexError(path, "frontmatter", message)
	case RoleLog:
		v.addLogError(path, "frontmatter", message)
	default:
		v.addSpecAt(SeverityError, "concept_unparseable", v.specRef("11.1"), path, "frontmatter", message)
	}
}

func (v *validator) legacyWarn(code, clause, path, fieldPath, message string) {
	v.addSpecAt(SeverityWarning, code, v.specRef(clause), path, fieldPath, message)
}

func (v *validator) specRef(clause string) string {
	version := v.version.Effective
	if version == "" {
		version = bundle.OKFVersion
	}
	return "okf-v" + version + "#" + clause
}

// addRelationDiagnostics projects semantic bundle findings onto the opt-in
// toolkit policy contract without expanding normative OKF conformance.
// Recognized aliases remain informational. Projection is sorted and
// de-duplicated here rather than relying on bundle construction order.
func (v *validator) addRelationDiagnostics(relations []bundle.RelationDiagnostic) error {
	var records []diagnosticRecord
	for _, relation := range relations {
		if err := v.check(); err != nil {
			return err
		}
		severity := SeverityWarning
		policyFailure := relation.BlocksMutation()
		if !policyFailure {
			severity = SeverityInfo
		}
		source := bundle.RelationRef{ID: relation.Source, Fragment: relation.SourceFragment}
		code := relation.Code
		if code == "" {
			code = "semantic_relation_invalid"
		}
		file, err := v.relContext(relation.File)
		if err != nil {
			return err
		}
		diagnostic := Diagnostic{
			Code:          code,
			SpecRef:       "toolkit#semantic-relations",
			File:          file,
			FieldPath:     "",
			Severity:      severity,
			Message:       relation.Message,
			Source:        source,
			RelationType:  relation.RelationType,
			RawTarget:     relation.RawTarget,
			Refs:          []bundle.RelationRef{source},
			PolicyFailure: policyFailure,
		}
		fieldPath, err := semanticRelationFieldPathContext(v.ctx, relation)
		if err != nil {
			return err
		}
		diagnostic.FieldPath = fieldPath
		record, err := v.newDiagnosticRecord(diagnostic)
		if err != nil {
			return err
		}
		records = append(records, record)
	}
	if err := v.sortDiagnosticRecordsContext(records); err != nil {
		return err
	}
	for index, record := range records {
		if err := v.check(); err != nil {
			return err
		}
		equal := false
		if index != 0 {
			result, err := compareDiagnosticRecordContext(v.ctx, record, records[index-1])
			if err != nil {
				return err
			}
			equal = result == 0
		}
		if index == 0 || !equal {
			v.report.Diagnostics = append(v.report.Diagnostics, record.diagnostic)
		}
	}
	return v.check()
}

func semanticRelationFieldPath(relation bundle.RelationDiagnostic) string {
	path, _ := semanticRelationFieldPathContext(context.Background(), relation)
	return path
}

func semanticRelationFieldPathContext(ctx context.Context, relation bundle.RelationDiagnostic) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fragmentPath := "frontmatter.fragments"
	if relation.SourceFragment != "" {
		owned, err := validatorOwnedStringContext(ctx, relation.SourceFragment)
		if err != nil {
			return "", err
		}
		fragmentPath += "[" + owned + "]"
	}
	switch relation.Code {
	case "anchor_alias":
		return fragmentPath + ".anchor", ctx.Err()
	case "invalid_fragment", "ambiguous_fragment":
		if relation.RelationType != "" {
			owned, err := validatorOwnedStringContext(ctx, relation.RelationType)
			if err != nil {
				return "", err
			}
			return fragmentPath + "." + owned, ctx.Err()
		}
		return fragmentPath, ctx.Err()
	case "duplicate_fragment":
		return fragmentPath, ctx.Err()
	}
	if relation.RelationType != "" {
		owned, err := validatorOwnedStringContext(ctx, relation.RelationType)
		if err != nil {
			return "", err
		}
		return "frontmatter.relations." + owned, ctx.Err()
	}
	if relation.SourceFragment != "" {
		return fragmentPath, ctx.Err()
	}
	return "frontmatter.semantic_relations", ctx.Err()
}

func (v *validator) rel(path string) string {
	rel, _ := v.relContext(path)
	return rel
}

func (v *validator) relContext(path string) (string, error) {
	if err := v.ctx.Err(); err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	owned, err := validatorOwnedStringContext(v.ctx, path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(v.root, owned)
	if ctxErr := v.ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(owned), v.ctx.Err()
	}
	return filepath.ToSlash(rel), v.ctx.Err()
}

func (v *validator) sortDiagnostics() error {
	var records []diagnosticRecord
	for _, diagnostic := range v.report.Diagnostics {
		if err := v.check(); err != nil {
			return err
		}
		record, err := v.newDiagnosticRecord(diagnostic)
		if err != nil {
			return err
		}
		records = append(records, record)
	}
	if err := v.sortDiagnosticRecordsContext(records); err != nil {
		return err
	}
	var diagnostics []Diagnostic
	for _, record := range records {
		if err := v.check(); err != nil {
			return err
		}
		diagnostics = append(diagnostics, record.diagnostic)
	}
	if err := v.check(); err != nil {
		return err
	}
	v.report.Diagnostics = diagnostics
	return v.check()
}

type diagnosticRecord struct {
	diagnostic Diagnostic
	refs       []bundle.RelationRef
}

func (v *validator) newDiagnosticRecord(diagnostic Diagnostic) (diagnosticRecord, error) {
	for _, field := range []*string{
		&diagnostic.Code, &diagnostic.SpecRef, &diagnostic.File, &diagnostic.FieldPath,
		&diagnostic.Message, &diagnostic.RelationType, &diagnostic.RawTarget,
	} {
		owned, err := validatorOwnedStringContext(v.ctx, *field)
		if err != nil {
			return diagnosticRecord{}, err
		}
		*field = owned
	}
	source, err := cloneValidatorRelationRefContext(v.ctx, diagnostic.Source)
	if err != nil {
		return diagnosticRecord{}, err
	}
	diagnostic.Source = source
	var refs []bundle.RelationRef
	for _, ref := range diagnostic.Refs {
		if err := v.check(); err != nil {
			return diagnosticRecord{}, err
		}
		owned, err := cloneValidatorRelationRefContext(v.ctx, ref)
		if err != nil {
			return diagnosticRecord{}, err
		}
		refs = append(refs, owned)
	}
	var compareErr error
	if err := stableSortWithCheck(v.check, refs, func(left, right bundle.RelationRef) bool {
		if compareErr != nil {
			return false
		}
		var result int
		result, compareErr = compareRelationRefContext(v.ctx, left, right)
		return result < 0
	}); err != nil {
		return diagnosticRecord{}, err
	}
	if compareErr != nil {
		return diagnosticRecord{}, compareErr
	}
	return diagnosticRecord{diagnostic: diagnostic, refs: refs}, v.check()
}

func cloneValidatorRelationRefContext(ctx context.Context, ref bundle.RelationRef) (bundle.RelationRef, error) {
	ownedSegments, err := ref.ID.SegmentsContext(ctx)
	if err != nil {
		return bundle.RelationRef{}, err
	}
	var id bundle.ConceptID
	if len(ownedSegments) != 0 {
		id, err = bundle.NewConceptIDContext(ctx, ownedSegments)
		if err != nil {
			return bundle.RelationRef{}, err
		}
	}
	fragment, err := validatorOwnedStringContext(ctx, ref.Fragment)
	if err != nil {
		return bundle.RelationRef{}, err
	}
	return bundle.RelationRef{ID: id, Fragment: fragment}, ctx.Err()
}

func (v *validator) sortDiagnosticRecordsContext(records []diagnosticRecord) error {
	var compareErr error
	if err := stableSortWithCheck(v.check, records, func(left, right diagnosticRecord) bool {
		if compareErr != nil {
			return false
		}
		var result int
		result, compareErr = compareDiagnosticRecordContext(v.ctx, left, right)
		return result < 0
	}); err != nil {
		return err
	}
	return compareErr
}

func lessDiagnosticRecord(left, right diagnosticRecord) bool {
	return compareDiagnosticRecord(left, right) < 0
}

func equalDiagnosticRecord(left, right diagnosticRecord) bool {
	return compareDiagnosticRecord(left, right) == 0
}

func compareDiagnosticRecord(left, right diagnosticRecord) int {
	result, _ := compareDiagnosticRecordContext(context.Background(), left, right)
	return result
}

func compareDiagnosticRecordContext(ctx context.Context, left, right diagnosticRecord) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if result := compareOrdered(left.diagnostic.Severity, right.diagnostic.Severity); result != 0 {
		return result, nil
	}
	for _, pair := range [][2]string{
		{left.diagnostic.File, right.diagnostic.File},
		{left.diagnostic.Code, right.diagnostic.Code},
		{left.diagnostic.SpecRef, right.diagnostic.SpecRef},
		{left.diagnostic.FieldPath, right.diagnostic.FieldPath},
	} {
		if result, err := compareValidatorStringsContext(ctx, pair[0], pair[1]); err != nil || result != 0 {
			return result, err
		}
	}
	if left.diagnostic.PolicyFailure != right.diagnostic.PolicyFailure {
		if !left.diagnostic.PolicyFailure {
			return -1, nil
		}
		return 1, nil
	}
	if result, err := compareRelationRefContext(ctx, left.diagnostic.Source, right.diagnostic.Source); err != nil || result != 0 {
		return result, err
	}
	for _, pair := range [][2]string{
		{left.diagnostic.RelationType, right.diagnostic.RelationType},
		{left.diagnostic.RawTarget, right.diagnostic.RawTarget},
		{left.diagnostic.Message, right.diagnostic.Message},
	} {
		if result, err := compareValidatorStringsContext(ctx, pair[0], pair[1]); err != nil || result != 0 {
			return result, err
		}
	}
	for index := 0; index < min(len(left.refs), len(right.refs)); index++ {
		if result, err := compareRelationRefContext(ctx, left.refs[index], right.refs[index]); err != nil || result != 0 {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return compareOrdered(len(left.refs), len(right.refs)), nil
}

func compareRelationRef(left, right bundle.RelationRef) int {
	result, _ := compareRelationRefContext(context.Background(), left, right)
	return result
}

func compareRelationRefContext(ctx context.Context, left, right bundle.RelationRef) (int, error) {
	leftSegments, err := left.ID.SegmentsContext(ctx)
	if err != nil {
		return 0, err
	}
	rightSegments, err := right.ID.SegmentsContext(ctx)
	if err != nil {
		return 0, err
	}
	for index := 0; index < min(len(leftSegments), len(rightSegments)); index++ {
		if result, err := compareValidatorStringsContext(ctx, leftSegments[index], rightSegments[index]); err != nil || result != 0 {
			return result, err
		}
	}
	if result := compareOrdered(len(leftSegments), len(rightSegments)); result != 0 {
		return result, nil
	}
	return compareValidatorStringsContext(ctx, left.Fragment, right.Fragment)
}

func compareValidatorStringsContext(ctx context.Context, left, right string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	const chunkSize = 64 << 10
	limit := min(len(left), len(right))
	for offset := 0; offset < limit; offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		end := min(offset+chunkSize, limit)
		if result := strings.Compare(left[offset:end], right[offset:end]); result != 0 {
			return result, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return compareOrdered(len(left), len(right)), nil
}

func nonBlankValidatorStringContext(ctx context.Context, value string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for offset := 0; offset < len(value); {
		if offset%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		current, size := utf8.DecodeRuneInString(value[offset:])
		if current == utf8.RuneError && size == 1 {
			return true, nil
		}
		if !unicode.IsSpace(current) {
			return true, ctx.Err()
		}
		offset += size
	}
	return false, ctx.Err()
}

func validatorStringKeyContext(ctx context.Context, value string) ([sha256.Size]byte, error) {
	hash := sha256.New()
	for offset := 0; offset < len(value); offset += 64 << 10 {
		if err := ctx.Err(); err != nil {
			return [sha256.Size]byte{}, err
		}
		_, _ = hash.Write([]byte(value[offset:min(offset+(64<<10), len(value))]))
	}
	if err := ctx.Err(); err != nil {
		return [sha256.Size]byte{}, err
	}
	var out [sha256.Size]byte
	copy(out[:], hash.Sum(nil))
	return out, nil
}

type validatorStringBuckets struct {
	digest func(context.Context, string) ([sha256.Size]byte, error)
	values map[[sha256.Size]byte][]string
}

type validatorStringValueEntry struct{ key, value string }
type validatorStringValueBuckets struct {
	digest func(context.Context, string) ([sha256.Size]byte, error)
	values map[[sha256.Size]byte][]validatorStringValueEntry
}

func newValidatorStringValueBuckets(digest func(context.Context, string) ([sha256.Size]byte, error)) *validatorStringValueBuckets {
	return &validatorStringValueBuckets{digest: digest, values: make(map[[sha256.Size]byte][]validatorStringValueEntry)}
}

func (b *validatorStringValueBuckets) LookupContext(ctx context.Context, key string) (string, bool, error) {
	digest, err := b.digest(ctx, key)
	if err != nil {
		return "", false, err
	}
	for _, entry := range b.values[digest] {
		result, err := compareValidatorStringsContext(ctx, entry.key, key)
		if err != nil {
			return "", false, err
		}
		if result == 0 {
			return entry.value, true, nil
		}
	}
	return "", false, ctx.Err()
}

func (b *validatorStringValueBuckets) PutContext(ctx context.Context, key, value string) error {
	digest, err := b.digest(ctx, key)
	if err != nil {
		return err
	}
	ownedKey, err := validatorOwnedStringContext(ctx, key)
	if err != nil {
		return err
	}
	ownedValue, err := validatorOwnedStringContext(ctx, value)
	if err != nil {
		return err
	}
	b.values[digest] = append(b.values[digest], validatorStringValueEntry{ownedKey, ownedValue})
	return ctx.Err()
}

func newValidatorStringBuckets(digest func(context.Context, string) ([sha256.Size]byte, error)) *validatorStringBuckets {
	return &validatorStringBuckets{digest: digest, values: make(map[[sha256.Size]byte][]string)}
}

func (v *validator) newStringBuckets() *validatorStringBuckets {
	digest := v.cfg.stringDigest
	if digest == nil {
		digest = validatorStringKeyContext
	}
	return newValidatorStringBuckets(digest)
}

func (v *validator) newStringValueBuckets() *validatorStringValueBuckets {
	digest := v.cfg.stringDigest
	if digest == nil {
		digest = validatorStringKeyContext
	}
	return newValidatorStringValueBuckets(digest)
}

func (b *validatorStringBuckets) InsertContext(ctx context.Context, value string) (bool, error) {
	key, err := b.digest(ctx, value)
	if err != nil {
		return false, err
	}
	for _, existing := range b.values[key] {
		result, err := compareValidatorStringsContext(ctx, existing, value)
		if err != nil {
			return false, err
		}
		if result == 0 {
			return true, nil
		}
	}
	owned, err := validatorOwnedStringContext(ctx, value)
	if err != nil {
		return false, err
	}
	b.values[key] = append(b.values[key], owned)
	return false, ctx.Err()
}

func (b *validatorStringBuckets) ContainsContext(ctx context.Context, value string) (bool, error) {
	key, err := b.digest(ctx, value)
	if err != nil {
		return false, err
	}
	for _, existing := range b.values[key] {
		result, err := compareValidatorStringsContext(ctx, existing, value)
		if err != nil {
			return false, err
		}
		if result == 0 {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func validatorOwnedStringContext(ctx context.Context, value string) (string, error) {
	var out strings.Builder
	out.Grow(len(value))
	for offset := 0; offset < len(value); offset += 64 << 10 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		out.WriteString(value[offset:min(offset+(64<<10), len(value))])
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func validatorDiagnosticMessageContext(ctx context.Context, format string, values ...any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for _, value := range values {
		if text, ok := value.(string); ok {
			if _, err := validatorOwnedStringContext(ctx, text); err != nil {
				return "", err
			}
		}
	}
	message := fmt.Sprintf(format, values...)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return message, nil
}

type ordered interface {
	~int
}

func compareOrdered[T ordered](left, right T) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func stableSortWithCheck[T any](check func() error, values []T, less func(left, right T) bool) error {
	if err := check(); err != nil {
		return err
	}
	if len(values) < 2 {
		return check()
	}
	var scratch []T
	var zero T
	for range values {
		if err := check(); err != nil {
			return err
		}
		scratch = append(scratch, zero)
	}
	source, destination := values, scratch
	for width := 1; width < len(values); width *= 2 {
		for start := 0; start < len(values); start += 2 * width {
			middle := min(start+width, len(values))
			end := min(start+2*width, len(values))
			left, right := start, middle
			for output := start; output < end; output++ {
				if err := check(); err != nil {
					return err
				}
				if left >= middle {
					destination[output] = source[right]
					right++
				} else if right >= end {
					destination[output] = source[left]
					left++
				} else if less(source[right], source[left]) {
					destination[output] = source[right]
					right++
				} else {
					destination[output] = source[left]
					left++
				}
			}
		}
		source, destination = destination, source
		if width > len(values)/2 {
			break
		}
	}
	if len(source) > 0 && &source[0] != &values[0] {
		for index := range source {
			if err := check(); err != nil {
				return err
			}
			values[index] = source[index]
		}
	}
	return check()
}

func samePath(a, b string) bool {
	rel, err := filepath.Rel(a, b)
	return err == nil && rel == "."
}

func displayString(node *yaml.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	if node.Kind == yaml.ScalarNode {
		switch node.Tag {
		case "!!str", "!!int", "!!float", "!!bool", "!!timestamp":
			return node.Value, node.Value != ""
		default:
			return "", false
		}
	}
	return "", false
}

func isEmptyYAMLValue(node *yaml.Node) bool {
	if node == nil {
		return true
	}
	if node.Kind == yaml.ScalarNode {
		return strings.TrimSpace(node.Value) == ""
	}
	if node.Kind == yaml.SequenceNode || node.Kind == yaml.MappingNode {
		return len(node.Content) == 0
	}
	return false
}

// IsISODate checks strict YYYY-MM-DD calendar-date syntax.
func IsISODate(value string) bool {
	if len(value) != 10 || value[4] != '-' || value[7] != '-' {
		return false
	}
	for _, index := range []int{0, 1, 2, 3, 5, 6, 8, 9} {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func isValidURI(value string) bool {
	valid, _ := isValidURIContext(context.Background(), value)
	return valid
}

func isValidURIContext(ctx context.Context, value string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for offset := 0; offset < len(value); offset += 64 << 10 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	parsed, err := url.Parse(value)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	return err == nil && parsed.Scheme != "", nil
}

func isValidCitationTarget(target string) bool {
	if isValidURI(target) {
		return true
	}
	if strings.HasPrefix(target, "/") {
		return true
	}
	clean := strings.TrimPrefix(target, "./")
	return strings.HasPrefix(clean, "references/")
}

func hasTopLevelHeadingContext(ctx context.Context, body, name string) (bool, error) {
	blocks, err := markdownowner.TopLevelStructureContext(ctx, body)
	if err != nil {
		return false, err
	}
	for _, block := range blocks {
		if block.Kind == markdownowner.TopLevelHeading &&
			block.HeadingLevel == 1 &&
			strings.EqualFold(block.Text, name) {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func topLevelSectionBlocksContext(ctx context.Context, body, name string) ([]markdownowner.TopLevelBlock, bool, error) {
	blocks, err := markdownowner.TopLevelStructureContext(ctx, body)
	if err != nil {
		return nil, false, err
	}
	var out []markdownowner.TopLevelBlock
	inSection := false
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if block.Kind == markdownowner.TopLevelHeading && block.HeadingLevel == 1 {
			if inSection {
				break
			}
			inSection = strings.EqualFold(block.Text, name)
			continue
		}
		if inSection {
			out = append(out, block)
		}
	}
	if !inSection {
		return nil, false, nil
	}
	return out, true, ctx.Err()
}

func hasConcreteExampleContext(ctx context.Context, blocks []markdownowner.TopLevelBlock) (bool, error) {
	var text strings.Builder
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		switch block.Kind {
		case markdownowner.TopLevelCode, markdownowner.TopLevelList:
			return true, nil
		}
		if block.HasLink || strings.Contains(block.Text, "|") {
			return true, nil
		}
		text.WriteByte(' ')
		for offset := 0; offset < len(block.Text); offset += 64 << 10 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			text.WriteString(block.Text[offset:min(offset+(64<<10), len(block.Text))])
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return len(strings.Fields(text.String())) >= 8, nil
}

func markdownHeadingIDsContext(ctx context.Context, body string, check func() error, digest func(context.Context, string) ([sha256.Size]byte, error)) (*validatorStringBuckets, error) {
	if err := check(); err != nil {
		return nil, err
	}
	ids, err := markdownowner.HeadingIDsContext(ctx, body)
	if err != nil {
		return nil, err
	}
	headings := newValidatorStringBuckets(digest)
	for _, id := range ids {
		if err := check(); err != nil {
			return nil, err
		}
		if _, err := headings.InsertContext(ctx, id); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := check(); err != nil {
		return nil, err
	}
	return headings, nil
}

func splitFragment(target string) (string, string) {
	before, after, found := strings.Cut(target, "#")
	if !found {
		return target, ""
	}
	return before, after
}

func isDirectoryLink(target string) bool {
	path, _ := splitFragment(target)
	return strings.HasSuffix(path, "/")
}

func isInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}
