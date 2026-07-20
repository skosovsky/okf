package validator

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"github.com/yuin/goldmark"
	goldmarkast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

// ValidatorConfig controls optional OKF validation layers.
type ValidatorConfig struct {
	Strict       bool
	CheckLinks   bool
	CheckOrphans bool
	// CheckRelations projects semantic relation diagnostics into the validation
	// report. It is deliberately opt-in: OKF v0.1 base conformance does not
	// require semantic relation policy. Mutation paths enforce blocking relation
	// diagnostics independently of this reporting policy.
	CheckRelations bool
	// checkpoint is a package-private deterministic test seam. Production
	// callers cannot configure it; validation still checks ctx at every pass.
	checkpoint func()
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
	// Code is an optional stable machine-readable identifier. Most validator
	// findings predate semantic relation diagnostics and intentionally leave it
	// empty; callers must continue to use Message for their human-facing text.
	Code     string
	File     string
	Severity Severity
	Message  string
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
	Diagnostics  []Diagnostic
	ScannedFiles int
}

// IsConformant reports whether the bundle has no error diagnostics.
func (r Report) IsConformant() bool {
	return r.Count(SeverityError) == 0
}

// ExitCode returns the CLI exit code implied by the report.
func (r Report) ExitCode() int {
	if r.IsConformant() {
		return 0
	}
	return 1
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
	ctx          context.Context
	root         string
	cfg          ValidatorConfig
	bundle       *bundle.Bundle
	report       Report
	files        []string
	headingCache map[string]map[string]struct{}
}

// ValidateBundle validates a loaded bundle against OKF v0.1.
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
			Severity: SeverityError,
			Message:  "nil bundle",
		}}}, nil
	}

	v := &validator{
		ctx:          ctx,
		root:         b.Root(),
		cfg:          config,
		bundle:       b,
		files:        b.MarkdownFiles(),
		headingCache: make(map[string]map[string]struct{}),
	}
	v.report.ScannedFiles = len(v.files)
	if err := v.validate(); err != nil {
		return Report{}, err
	}
	if config.CheckRelations {
		v.addRelationDiagnostics(b.RelationDiagnostics())
	}
	v.sortDiagnostics()
	return v.report, nil
}

// ValidatePath loads and validates a bundle path against OKF v0.1.
func ValidatePath(bundlePath string, cfg *ValidatorConfig) Report {
	b, err := bundle.LoadBundle(bundlePath)
	if err != nil {
		return Report{Diagnostics: []Diagnostic{{
			Severity: SeverityError,
			File:     bundlePath,
			Message:  err.Error(),
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
		return Report{Diagnostics: []Diagnostic{{Severity: SeverityError, Message: err.Error()}}}, nil
	}
	return ValidateBundleContext(ctx, b, cfg)
}

// ValidateSourceBackground is the report-only compatibility wrapper.
func ValidateSourceBackground(source bundle.Source, cfg *ValidatorConfig) Report {
	report, _ := ValidateSource(context.Background(), source, cfg)
	return report
}

func (v *validator) check() error {
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
		if err := v.validateStrict(); err != nil {
			return err
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
		v.add(SeverityError, parseError.Path, "unparseable concept document: "+parseError.Err.Error())
	}
	return nil
}

func (v *validator) validateConcepts() error {
	for _, concept := range v.bundle.Concepts() {
		if err := v.check(); err != nil {
			return err
		}
		if !concept.Document.HasFrontmatter {
			v.add(SeverityError, concept.Path, "missing YAML frontmatter block")
			continue
		}
		if err := concept.Document.ValidateConformance(); err != nil {
			v.add(SeverityError, concept.Path, "missing or empty 'type' field")
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
		document, ok := v.readDocument(path, true)
		if !ok {
			continue
		}
		if document.HasFrontmatter {
			if !samePath(path, rootIndex) {
				v.add(SeverityError, path, "index.md should not contain frontmatter")
				continue
			}
			keys := document.Frontmatter.Keys()
			for _, key := range keys {
				if err := v.check(); err != nil {
					return err
				}
				if key != "okf_version" {
					v.add(SeverityError, path, "root index.md frontmatter should declare only 'okf_version'")
					break
				}
			}
			if len(keys) == 0 {
				v.add(SeverityError, path, "root index.md okf_version should be a non-empty string")
			} else if len(keys) == 1 && keys[0] == "okf_version" {
				if _, ok := document.Frontmatter.OKFVersion(); !ok {
					v.add(SeverityError, path, "root index.md okf_version should be a non-empty string")
				}
			}
		}
		// O1 needs empty nested indexes to remain usable as orphan coverage
		// surfaces; the base E4 empty-index fixture still rejects this outside
		// orphan-check mode.
		if v.cfg.CheckOrphans && !samePath(path, rootIndex) && strings.TrimSpace(document.Body) == "" {
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
		document, ok := v.readDocument(path, true)
		if !ok {
			continue
		}
		if document.HasFrontmatter {
			v.add(SeverityError, path, "log.md should not contain frontmatter")
			continue
		}
		if err := v.validateLog(path, document.Body); err != nil {
			return err
		}
	}
	return nil
}

func (v *validator) validateIndexBody(path, body string) error {
	lines := strings.Split(body, "\n")
	seenHeading := false
	seenEntry := false
	sectionHeading := ""
	sectionHasEntry := false

	for _, line := range lines {
		if err := v.check(); err != nil {
			return err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isMarkdownHeading(trimmed) {
			if seenHeading && !sectionHasEntry {
				v.add(SeverityError, path, fmt.Sprintf("index.md section has no entries: %q", sectionHeading))
			}
			seenHeading = true
			sectionHeading = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			sectionHasEntry = false
			continue
		}
		body, ok := bulletBody(trimmed)
		if !ok {
			continue
		}
		if !seenHeading {
			v.add(SeverityError, path, "index.md list entry appears before any heading")
			continue
		}
		if len(bundle.ExtractLinks(body)) == 0 {
			v.add(SeverityError, path, "index.md list entry should contain a Markdown link")
			continue
		}
		seenEntry = true
		sectionHasEntry = true
	}

	if !seenHeading {
		v.add(SeverityError, path, "index.md should contain at least one heading")
		return nil
	}
	if !sectionHasEntry {
		v.add(SeverityError, path, fmt.Sprintf("index.md section has no entries: %q", sectionHeading))
	}
	if !seenEntry {
		v.add(SeverityError, path, "index.md should contain at least one linked list entry")
	}
	return nil
}

func (v *validator) validateLog(path, body string) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}

	log := bundle.ParseLog(body)
	badDateLevels := invalidLogDateHeadingLevels(body)
	if len(log.Days) == 0 && len(badDateLevels) == 0 {
		v.add(SeverityError, path, "log.md should contain ISO-8601 date headings")
	}
	for _, badDate := range log.InvalidDates() {
		if err := v.check(); err != nil {
			return err
		}
		v.add(SeverityError, path, fmt.Sprintf("log date heading is not ISO-8601 YYYY-MM-DD: %q", badDate))
	}
	for _, badLevel := range badDateLevels {
		if err := v.check(); err != nil {
			return err
		}
		v.add(SeverityError, path, fmt.Sprintf("log date heading should use level 2: %q", badLevel))
	}
	for _, emptyDay := range log.EmptyDates() {
		if err := v.check(); err != nil {
			return err
		}
		v.add(SeverityError, path, fmt.Sprintf("log date heading has no entries: %q", emptyDay))
	}
	for _, outOfOrder := range log.OutOfOrderDates() {
		if err := v.check(); err != nil {
			return err
		}
		v.add(SeverityError, path, fmt.Sprintf("log date headings should be newest first: %q", outOfOrder))
	}
	for _, line := range nonListLogLines(body) {
		if err := v.check(); err != nil {
			return err
		}
		v.add(SeverityError, path, fmt.Sprintf("log date group contains non-list entry: %q", line))
	}
	return nil
}

func (v *validator) validateStrict() error {
	for _, concept := range v.bundle.Concepts() {
		if err := v.check(); err != nil {
			return err
		}
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
		document, ok := v.readDocument(path, true)
		if ok {
			if err := v.validateIndexDescriptions(path, document.Body); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *validator) validateStrictFrontmatter(concept bundle.Concept) error {
	for _, field := range []string{"title", "description", "tags", "timestamp"} {
		if err := v.check(); err != nil {
			return err
		}
		value, ok := concept.Document.Frontmatter.Get(field)
		if !ok || isEmptyYAMLValue(value) {
			v.add(SeverityWarning, concept.Path, fmt.Sprintf("missing recommended frontmatter field '%s'", field))
		}
	}

	if node, ok := concept.Document.Frontmatter.Get("tags"); ok {
		if node.Kind != yaml.SequenceNode {
			v.add(SeverityWarning, concept.Path, "'tags' should be a YAML list of strings")
		} else {
			for _, item := range node.Content {
				if err := v.check(); err != nil {
					return err
				}
				if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
					v.add(SeverityWarning, concept.Path, "'tags' should be a YAML list of strings")
					break
				}
			}
		}
	}

	if node, ok := concept.Document.Frontmatter.Get("timestamp"); ok {
		if timestamp, ok := displayString(node); ok {
			if _, err := time.Parse(time.RFC3339, timestamp); err != nil {
				v.add(SeverityWarning, concept.Path, fmt.Sprintf("'timestamp' is not RFC3339: %q", timestamp))
			}
		} else {
			v.add(SeverityWarning, concept.Path, "'timestamp' should be an RFC3339 string")
		}
	}

	if node, ok := concept.Document.Frontmatter.Get("resource"); ok {
		if resource, ok := displayString(node); ok {
			if !isValidURI(resource) {
				v.add(SeverityWarning, concept.Path, fmt.Sprintf("'resource' is not a valid URI: %q", resource))
			}
		} else {
			v.add(SeverityWarning, concept.Path, "'resource' should be a URI string")
		}
	}
	return nil
}

func (v *validator) validateConventionalSections(concept bundle.Concept) error {
	body := concept.Document.Body
	codeFreeBody := strings.Join(codeFreeLines(body), "\n")
	if citationMarkerRE.MatchString(codeFreeBody) {
		if !hasBottomTopLevelHeading(body, "Citations") {
			v.add(SeverityWarning, concept.Path, "citation markers require a bottom '# Citations' section")
		} else {
			citations := concept.Document.Citations()
			if len(citations) == 0 {
				v.add(SeverityWarning, concept.Path, "'# Citations' should contain numbered citation entries")
			}
			for i, citation := range citations {
				if err := v.check(); err != nil {
					return err
				}
				want := i + 1
				if citation.Number != want {
					v.add(SeverityWarning, concept.Path, fmt.Sprintf("citation numbering should be contiguous starting at 1: got %d, want %d", citation.Number, want))
				}
				if citation.Target == "" || !isValidCitationTarget(citation.Target) {
					v.add(SeverityWarning, concept.Path, fmt.Sprintf("citation entry has invalid target: %q", citation.Raw))
				}
			}
		}
	}

	if section, ok := topLevelSectionContent(body, "Examples"); ok && !hasConcreteExample(section) {
		v.add(SeverityWarning, concept.Path, "'# Examples' should contain a concrete example")
	}

	if typ, ok := concept.Document.Frontmatter.Type(); ok && strings.EqualFold(typ, "BigQuery Table") && !hasTopLevelHeading(body, "Schema") {
		v.add(SeverityWarning, concept.Path, "BigQuery Table concepts should include a '# Schema' section")
	}
	return nil
}

func (v *validator) validateIndexDescriptions(path, body string) error {
	for _, line := range strings.Split(body, "\n") {
		if err := v.check(); err != nil {
			return err
		}
		trimmed := strings.TrimSpace(line)
		bullet, ok := bulletBody(trimmed)
		if !ok {
			continue
		}
		links := bundle.ExtractLinks(bullet)
		if len(links) == 0 {
			continue
		}
		link := links[0]
		targetPath, ok := v.resolveLinkPath(path, link.Target)
		if !ok || DetermineFileRole(targetPath) != RoleConcept {
			continue
		}
		document, ok := v.readDocument(targetPath, false)
		if !ok {
			continue
		}
		want, ok := document.Frontmatter.Description()
		if !ok {
			continue
		}
		got := indexEntryDescription(bullet)
		if got != want {
			v.add(SeverityWarning, path, fmt.Sprintf("index.md description for %q does not match target frontmatter", link.Target))
		}
	}
	return nil
}

func (v *validator) validateLinks() error {
	for _, path := range v.files {
		if err := v.check(); err != nil {
			return err
		}
		document, ok := v.readDocument(path, true)
		if !ok {
			continue
		}
		body := document.Body
		if !document.HasFrontmatter && DetermineFileRole(path) != RoleConcept {
			body = document.Body
		}
		for _, link := range bundle.ExtractLinks(body) {
			if err := v.check(); err != nil {
				return err
			}
			if err := v.validateLink(path, link); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *validator) validateLink(sourcePath string, link bundle.Link) error {
	if err := v.check(); err != nil {
		return err
	}
	if link.Kind == bundle.LinkExternal || link.Kind == bundle.LinkOther {
		return nil
	}

	targetPath, targetKind, fragment, ok := v.resolveLinkTarget(sourcePath, link.Target)
	if !ok {
		v.add(SeverityInfo, sourcePath, fmt.Sprintf("broken link to %q (target not found)", link.Target))
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
		v.add(SeverityWarning, sourcePath, fmt.Sprintf("anchor not found in target file: %s", link.Target))
	}
	return nil
}

func (v *validator) validateOrphans() error {
	byDir := make(map[string][]string)
	for _, path := range v.files {
		if err := v.check(); err != nil {
			return err
		}
		if DetermineFileRole(path) == RoleConcept {
			byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], path)
		}
	}

	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		if err := v.check(); err != nil {
			return err
		}
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		if err := v.check(); err != nil {
			return err
		}
		indexPath := filepath.Join(dir, "index.md")
		if v.root == "" {
			if _, ok := v.bundle.ReadFile(indexPath); !ok {
				v.add(SeverityInfo, dir, "missing index.md, skipping orphan check for this directory")
				continue
			}
			covered := v.indexCoveredConcepts(indexPath)
			for _, conceptPath := range byDir[dir] {
				if err := v.check(); err != nil {
					return err
				}
				if !covered[filepath.Clean(conceptPath)] {
					v.add(SeverityWarning, conceptPath, fmt.Sprintf("orphan file (not linked in %s)", v.rel(indexPath)))
				}
			}
			continue
		}
		if _, err := os.Stat(indexPath); err != nil {
			if os.IsNotExist(err) {
				v.add(SeverityInfo, dir, "missing index.md, skipping orphan check for this directory")
			}
			continue
		}
		covered := v.indexCoveredConcepts(indexPath)
		for _, conceptPath := range byDir[dir] {
			if err := v.check(); err != nil {
				return err
			}
			if !covered[filepath.Clean(conceptPath)] {
				v.add(SeverityWarning, conceptPath, fmt.Sprintf("orphan file (not linked in %s)", v.rel(indexPath)))
			}
		}
	}
	return nil
}

func (v *validator) indexCoveredConcepts(indexPath string) map[string]bool {
	covered := make(map[string]bool)
	document, ok := v.readDocument(indexPath, true)
	if !ok {
		return covered
	}
	for _, link := range bundle.ExtractLinks(document.Body) {
		if v.check() != nil {
			return covered
		}
		targetPath, ok := v.resolveLinkPath(indexPath, link.Target)
		if !ok || DetermineFileRole(targetPath) != RoleConcept {
			continue
		}
		if filepath.Dir(targetPath) == filepath.Dir(indexPath) {
			covered[filepath.Clean(targetPath)] = true
		}
	}
	return covered
}

func (v *validator) readDocument(path string, allowReservedNoFrontmatter bool) (bundle.Document, bool) {
	data, ok := v.bundle.ReadFile(path)
	if !ok {
		return bundle.Document{}, false
	}
	if !utf8.Valid(data) {
		v.add(SeverityError, path, "markdown file is not valid UTF-8")
		return bundle.Document{}, false
	}
	document, err := bundle.ParseDocument(string(data))
	if err != nil {
		v.add(SeverityError, path, err.Error())
		return bundle.Document{}, false
	}
	if !allowReservedNoFrontmatter && !document.HasFrontmatter {
		return bundle.Document{}, false
	}
	return document, true
}

func (v *validator) resolveLinkPath(sourcePath, target string) (string, bool) {
	targetPath, _, _, ok := v.resolveLinkTarget(sourcePath, target)
	return targetPath, ok
}

func (v *validator) resolveLinkTarget(sourcePath, target string) (string, FileRole, string, bool) {
	rawPath, fragment := splitFragment(target)
	if strings.TrimSpace(rawPath) == "" {
		return sourcePath, DetermineFileRole(sourcePath), fragment, true
	}

	var candidate string
	if strings.HasPrefix(rawPath, "/") {
		candidate = filepath.Join(v.root, strings.TrimLeft(rawPath, "/"))
	} else {
		candidate = filepath.Join(filepath.Dir(sourcePath), rawPath)
	}
	candidate = filepath.Clean(candidate)
	if v.root == "" && (candidate == ".." || strings.HasPrefix(candidate, ".."+string(filepath.Separator))) {
		return "", RoleAsset, fragment, false
	}
	if v.root != "" && !isInside(v.root, candidate) {
		return "", RoleAsset, fragment, false
	}
	if _, ok := v.bundle.ReadFile(candidate); ok {
		return candidate, DetermineFileRole(candidate), fragment, true
	}
	if _, ok := v.bundle.ReadFile(filepath.Join(candidate, "index.md")); ok {
		return candidate, RoleIndex, fragment, true
	}
	if v.root == "" {
		return candidate, DetermineFileRole(candidate), fragment, false
	}

	info, err := os.Lstat(candidate)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return candidate, RoleAsset, fragment, true
		}
		if info.IsDir() {
			return candidate, RoleIndex, fragment, true
		}
		return candidate, DetermineFileRole(candidate), fragment, true
	}
	if os.IsNotExist(err) {
		return candidate, DetermineFileRole(candidate), fragment, false
	}
	return candidate, DetermineFileRole(candidate), fragment, false
}

func (v *validator) headingExists(path, fragment string) (bool, error) {
	if err := v.check(); err != nil {
		return false, err
	}
	headings, ok := v.headingCache[path]
	if !ok {
		headings = make(map[string]struct{})
		document, ok := v.readDocument(path, true)
		if !ok {
			v.headingCache[path] = headings
			return false, nil
		}
		if err := v.check(); err != nil {
			return false, err
		}
		headings = markdownHeadingIDs(document.Body)
		v.headingCache[path] = headings
	}
	_, ok = headings[strings.TrimPrefix(fragment, "#")]
	return ok, v.check()
}

func (v *validator) add(severity Severity, path string, message string) {
	v.report.Diagnostics = append(v.report.Diagnostics, Diagnostic{
		Severity: severity,
		File:     v.rel(path),
		Message:  message,
	})
}

// addRelationDiagnostics projects every semantic bundle finding onto the
// opt-in validator contract. Semantic errors are conformance errors;
// recognized aliases remain informational. Projection is sorted and
// de-duplicated here rather than relying on bundle construction order.
func (v *validator) addRelationDiagnostics(relations []bundle.RelationDiagnostic) {
	diagnostics := make([]Diagnostic, 0, len(relations))
	for _, relation := range relations {
		severity := SeverityError
		if !relation.BlocksMutation() {
			severity = SeverityInfo
		}
		source := bundle.RelationRef{ID: relation.Source, Fragment: relation.SourceFragment}
		diagnostics = append(diagnostics, Diagnostic{
			Code:         relation.Code,
			File:         v.rel(relation.File),
			Severity:     severity,
			Message:      relation.Message,
			Source:       source,
			RelationType: relation.RelationType,
			RawTarget:    relation.RawTarget,
			Refs:         []bundle.RelationRef{source},
		})
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnosticKey(diagnostics[i]) < diagnosticKey(diagnostics[j]) })
	for i, diagnostic := range diagnostics {
		if i == 0 || diagnosticKey(diagnostic) != diagnosticKey(diagnostics[i-1]) {
			v.report.Diagnostics = append(v.report.Diagnostics, diagnostic)
		}
	}
}

func (v *validator) rel(path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(v.root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func (v *validator) sortDiagnostics() {
	sort.SliceStable(v.report.Diagnostics, func(i, j int) bool {
		a := v.report.Diagnostics[i]
		b := v.report.Diagnostics[j]
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return diagnosticKey(a) < diagnosticKey(b)
	})
}

func diagnosticKey(d Diagnostic) string {
	refs := append([]bundle.RelationRef(nil), d.Refs...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
	parts := make([]string, len(refs))
	for i, ref := range refs {
		parts[i] = ref.String()
	}
	return string(rune(d.Severity)) + "\x00" + d.File + "\x00" + d.Code + "\x00" + d.Source.String() + "\x00" + d.RelationType + "\x00" + d.RawTarget + "\x00" + d.Message + "\x00" + strings.Join(parts, "\x00")
}

func samePath(a, b string) bool {
	rel, err := filepath.Rel(a, b)
	return err == nil && rel == "."
}

func codeFreeLines(body string) []string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	var fence rune
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if fence != 0 {
			if isFenceClose(trimmed, fence) {
				fence = 0
			}
			out = append(out, "")
			continue
		}
		if marker, ok := fenceStart(trimmed); ok {
			fence = marker
			out = append(out, "")
			continue
		}
		out = append(out, blankInlineCode(line))
	}
	return out
}

func blankInlineCode(line string) string {
	var builder strings.Builder
	builder.Grow(len(line))
	inCode := false
	for i := 0; i < len(line); i++ {
		if line[i] == '`' {
			inCode = !inCode
			builder.WriteByte(' ')
			continue
		}
		if inCode {
			builder.WriteByte(' ')
			continue
		}
		builder.WriteByte(line[i])
	}
	return builder.String()
}

func bulletBody(line string) (string, bool) {
	if rest, ok := strings.CutPrefix(line, "* "); ok {
		return rest, true
	}
	if rest, ok := strings.CutPrefix(line, "- "); ok {
		return rest, true
	}
	return "", false
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

func isMarkdownHeading(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	count := 0
	for count < len(line) && line[count] == '#' {
		count++
	}
	return count < len(line) && line[count] == ' '
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

var citationMarkerRE = regexp.MustCompile(`\[[0-9]+\]`)

func isValidURI(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme != ""
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

func hasTopLevelHeading(body, name string) bool {
	for _, heading := range markdownHeadingInfos(body) {
		if heading.level == 1 && strings.EqualFold(heading.title, name) {
			return true
		}
	}
	return false
}

func hasBottomTopLevelHeading(body, name string) bool {
	headings := markdownHeadingInfos(body)
	if len(headings) == 0 {
		return false
	}
	last := headings[len(headings)-1]
	return last.level == 1 && strings.EqualFold(last.title, name)
}

func topLevelSectionContent(body, name string) (string, bool) {
	lines := strings.Split(body, "\n")
	var out []string
	inSection := false
	var fence rune

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if fence != 0 {
			if inSection {
				out = append(out, line)
			}
			if isFenceClose(trimmed, fence) {
				fence = 0
			}
			continue
		}
		if marker, ok := fenceStart(trimmed); ok {
			if inSection {
				out = append(out, line)
			}
			fence = marker
			continue
		}
		if heading, ok := markdownHeadingInfo(trimmed); ok && heading.level == 1 {
			if inSection {
				break
			}
			inSection = strings.EqualFold(heading.title, name)
			continue
		}
		if inSection {
			out = append(out, line)
		}
	}
	if !inSection {
		return "", false
	}
	return strings.Join(out, "\n"), true
}

func hasFencedCodeBlock(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			return true
		}
	}
	return false
}

func hasConcreteExample(body string) bool {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return false
	}
	if hasFencedCodeBlock(body) {
		return true
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if _, ok := bulletBody(line); ok {
			return true
		}
		if strings.Contains(line, "|") || len(bundle.ExtractLinks(line)) > 0 {
			return true
		}
	}
	return len(strings.Fields(trimmed)) >= 8
}

type markdownHeading struct {
	level int
	title string
}

func markdownHeadingInfos(body string) []markdownHeading {
	var headings []markdownHeading
	var fence rune

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence != 0 {
			if isFenceClose(trimmed, fence) {
				fence = 0
			}
			continue
		}
		if marker, ok := fenceStart(trimmed); ok {
			fence = marker
			continue
		}
		if heading, ok := markdownHeadingInfo(trimmed); ok {
			headings = append(headings, heading)
		}
	}
	return headings
}

func fenceStart(line string) (rune, bool) {
	switch {
	case strings.HasPrefix(line, "```"):
		return '`', true
	case strings.HasPrefix(line, "~~~"):
		return '~', true
	default:
		return 0, false
	}
}

func isFenceClose(line string, fence rune) bool {
	return strings.HasPrefix(line, strings.Repeat(string(fence), 3))
}

func markdownHeadingInfo(line string) (markdownHeading, bool) {
	if !strings.HasPrefix(line, "#") {
		return markdownHeading{}, false
	}
	count := 0
	for count < len(line) && line[count] == '#' {
		count++
	}
	if count >= len(line) || line[count] != ' ' {
		return markdownHeading{}, false
	}
	return markdownHeading{
		level: count,
		title: strings.TrimSpace(strings.TrimLeft(line, "#")),
	}, true
}

func markdownHeadingIDs(body string) map[string]struct{} {
	source := []byte(body)
	markdown := goldmark.New(goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	document := markdown.Parser().Parse(text.NewReader(source))

	headings := make(map[string]struct{})
	_ = goldmarkast.Walk(document, func(node goldmarkast.Node, entering bool) (goldmarkast.WalkStatus, error) {
		if !entering {
			return goldmarkast.WalkContinue, nil
		}
		if _, ok := node.(*goldmarkast.Heading); !ok {
			return goldmarkast.WalkContinue, nil
		}
		value, ok := node.AttributeString("id")
		if !ok {
			return goldmarkast.WalkContinue, nil
		}
		switch id := value.(type) {
		case []byte:
			headings[string(id)] = struct{}{}
		case string:
			headings[id] = struct{}{}
		}
		return goldmarkast.WalkContinue, nil
	})
	return headings
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

func indexEntryDescription(body string) string {
	close := strings.LastIndex(body, ")")
	if close < 0 || close+1 >= len(body) {
		return ""
	}
	rest := strings.TrimSpace(body[close+1:])
	if !strings.HasPrefix(rest, "-") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(rest, "-"))
}

func nonListLogLines(body string) []string {
	var bad []string
	inDate := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			inDate = true
			continue
		}
		if isMarkdownHeading(trimmed) && inDate {
			bad = append(bad, trimmed)
			continue
		}
		if !inDate {
			continue
		}
		unindented := strings.TrimRight(line, " \t\r")
		if _, ok := bulletBody(unindented); !ok {
			bad = append(bad, trimmed)
		}
	}
	return bad
}

func invalidLogDateHeadingLevels(body string) []string {
	var bad []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !isMarkdownHeading(trimmed) || strings.HasPrefix(trimmed, "## ") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if IsISODate(heading) {
			bad = append(bad, trimmed)
		}
	}
	return bad
}
