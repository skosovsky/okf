package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/graph"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

type conceptSummary struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

type listConceptsResponse struct {
	Concepts []conceptSummary `json:"concepts"`
}

type diagnosticDTO struct {
	Severity string `json:"severity"`
	File     string `json:"file,omitempty"`
	Message  string `json:"message"`
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
			Message:  diagnostic.Message,
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
			Message:  diagnostic.Message,
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
func uniqueDiagnosticDTOs(groups ...[]diagnosticProjection) []diagnosticDTO {
	seen := make(map[string]struct{})
	var diagnostics []diagnosticDTO
	for _, group := range groups {
		for _, diagnostic := range group {
			if _, ok := seen[diagnostic.key]; ok {
				continue
			}
			seen[diagnostic.key] = struct{}{}
			diagnostics = append(diagnostics, diagnostic.wire)
		}
	}
	return diagnostics
}

type validateBundleResponse struct {
	ScannedFiles int             `json:"scanned_files"`
	Conformant   bool            `json:"conformant"`
	Errors       int             `json:"errors"`
	Warnings     int             `json:"warnings"`
	Info         int             `json:"info"`
	Diagnostics  []diagnosticDTO `json:"diagnostics"`
}

type writeConceptResponse struct {
	Status      string          `json:"status"`
	Path        string          `json:"path"`
	Diagnostics []diagnosticDTO `json:"diagnostics"`
}

func handleListConcepts(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	root, result := requireBundlePath(request)
	if result != nil {
		return result, nil
	}

	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		return toolErrorf("load bundle: %v", err), nil
	}
	if result := parseErrorsResult(root, loaded.ParseErrors()); result != nil {
		return result, nil
	}

	concepts := loaded.Concepts()
	sort.Slice(concepts, func(i, j int) bool {
		return concepts[i].ID.String() < concepts[j].ID.String()
	})

	response := listConceptsResponse{Concepts: make([]conceptSummary, 0, len(concepts))}
	for _, concept := range concepts {
		typ, _ := concept.Document.Frontmatter.Type()
		title, _ := concept.Document.Frontmatter.Title()
		response.Concepts = append(response.Concepts, conceptSummary{
			ID:    concept.ID.String(),
			Type:  typ,
			Title: title,
			Path:  relativeSlashPath(root, concept.Path),
		})
	}
	return jsonTextResult(response), nil
}

func handleReadConcept(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	root, result := requireBundlePath(request)
	if result != nil {
		return result, nil
	}
	if result := requireLoadableBundle(root); result != nil {
		return result, nil
	}
	id, result := requireConceptID(request)
	if result != nil {
		return result, nil
	}

	data, err := readPinnedConcept(ctx, root, id.String()+".md")
	if err != nil {
		if os.IsNotExist(err) {
			return toolErrorf("concept not found: %s", id.String()), nil
		}
		return toolErrorf("read concept: %v", err), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// readPinnedConcept opens each path component relative to a pinned directory
// descriptor. The Lstat/identity checks reject a symlink even if it is swapped
// in after a directory listing or prior validation.
// readPinnedConceptBeforeOpen is a test seam for the Lstat-to-open race.
// Production code leaves it nil.
var readPinnedConceptBeforeOpen func()

func readPinnedConcept(ctx context.Context, rootPath, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	beforeRoot, err := os.Lstat(rootPath)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	openedRoot, err := root.Lstat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(beforeRoot, openedRoot) {
		return nil, fmt.Errorf("bundle root changed while opening")
	}

	parts := strings.Split(name, "/")
	current := root
	var opened []*os.Root
	defer func() {
		for i := len(opened) - 1; i >= 0; i-- {
			_ = opened[i].Close()
		}
	}()
	for i, part := range parts {
		before, err := current.Lstat(part)
		if err != nil {
			return nil, err
		}
		rel := strings.Join(parts[:i+1], "/")
		if before.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("path contains symlink: %s", rel)
		}
		if i == len(parts)-1 {
			if before.IsDir() {
				return nil, fmt.Errorf("concept path is a directory: %s", strings.TrimSuffix(name, ".md"))
			}
			if !before.Mode().IsRegular() {
				return nil, fmt.Errorf("concept path is not a regular file: %s", strings.TrimSuffix(name, ".md"))
			}
			if readPinnedConceptBeforeOpen != nil {
				readPinnedConceptBeforeOpen()
			}
			file, err := openPinnedConceptFile(current, part)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			after, err := file.Stat()
			if err != nil {
				return nil, err
			}
			if !os.SameFile(before, after) || !after.Mode().IsRegular() {
				return nil, fmt.Errorf("concept path changed while opening")
			}
			return readPinnedConceptContents(ctx, file)
		}
		if !before.IsDir() {
			return nil, fmt.Errorf("concept path component is not a directory: %s", rel)
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			return nil, err
		}
		after, err := next.Lstat(".")
		if err != nil || !os.SameFile(before, after) {
			next.Close()
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("path contains symlink: %s", rel)
		}
		opened = append(opened, next)
		current = next
	}
	return nil, fmt.Errorf("invalid concept path")
}

func readPinnedConceptContents(ctx context.Context, file *os.File) ([]byte, error) {
	var out []byte
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := file.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func handleValidateBundle(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	root, result := requireBundlePath(request)
	if result != nil {
		return result, nil
	}
	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		return toolErrorf("load bundle: %v", err), nil
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
	cfg := validator.ValidatorConfig{
		Strict:       strict,
		CheckLinks:   checkLinks,
		CheckOrphans: checkOrphans,
	}
	// Validate the same no-follow snapshot already loaded for this request.
	// Reopening by pathname here would turn the earlier path validation into a
	// TOCTOU preflight rather than a boundary for the validator's input.
	report := validator.ValidateBundle(loaded, &cfg)
	return jsonTextResult(reportResponse(root, report)), nil
}

func handleSemanticGraph(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	root, result := requireBundlePath(request)
	if result != nil {
		return result, nil
	}

	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		return toolErrorf("load bundle: %v", err), nil
	}
	if result := parseErrorsResult(root, loaded.ParseErrors()); result != nil {
		return result, nil
	}

	var out bytes.Buffer
	if err := graph.RenderJSONLD(&out, loaded); err != nil {
		return toolErrorf("render JSON-LD: %v", err), nil
	}
	return mcp.NewToolResultText(out.String()), nil
}

func handleWriteConcept(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	root, result := requireBundlePath(request)
	if result != nil {
		return result, nil
	}
	if result := requireLoadableBundle(root); result != nil {
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
	response, writeErr := writeConcept(ctx, root, id, frontmatterText, body)
	if writeErr != nil {
		return toolError(writeErr.Error()), nil
	}
	if response.Status == "rejected" {
		return jsonErrorResult(response), nil
	}
	return jsonTextResult(response), nil
}

func requireBundlePath(request mcp.CallToolRequest) (string, *mcp.CallToolResult) {
	raw, err := request.RequireString("bundle_path")
	if err != nil {
		return "", toolError(err.Error())
	}
	root, err := normalizeBundleRoot(raw)
	if err != nil {
		return "", toolError(err.Error())
	}
	return root, nil
}

func requireLoadableBundle(root string) *mcp.CallToolResult {
	if _, err := bundle.LoadBundle(root); err != nil {
		return toolErrorf("load bundle: %v", err)
	}
	return nil
}

func normalizeBundleRoot(raw string) (string, error) {
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
	if err := validateBundleRootPath(clean); err != nil {
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
func validateBundleRootPath(path string) error {
	volumeRoot := filepath.VolumeName(path) + string(filepath.Separator)
	relative, err := filepath.Rel(volumeRoot, path)
	if err != nil {
		return fmt.Errorf("bundle_path is not accessible: %w", err)
	}
	root, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return fmt.Errorf("bundle_path is not accessible: %w", err)
	}
	defer root.Close()
	if relative == "." {
		return nil
	}

	current := root
	var opened []*os.Root
	defer func() {
		for i := len(opened) - 1; i >= 0; i-- {
			_ = opened[i].Close()
		}
	}()
	components := strings.Split(relative, string(filepath.Separator))
	for index, component := range components {
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
		next, err := current.OpenRoot(component)
		if err != nil {
			return fmt.Errorf("bundle_path is not accessible: %w", err)
		}
		after, err := next.Lstat(".")
		if err != nil || !os.SameFile(before, after) {
			_ = next.Close()
			if err != nil {
				return fmt.Errorf("bundle_path is not accessible: %w", err)
			}
			return fmt.Errorf("bundle_path changed while opening")
		}
		opened = append(opened, next)
		current = next
	}
	return nil
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

func parseErrorsResult(root string, errors []bundle.ParseError) *mcp.CallToolResult {
	if len(errors) == 0 {
		return nil
	}
	diagnostics := make([]diagnosticDTO, 0, len(errors))
	for _, parseError := range errors {
		diagnostics = append(diagnostics, diagnosticDTO{
			Severity: validator.SeverityError.String(),
			File:     relativeSlashPath(root, parseError.Path),
			Message:  "unparseable concept document: " + parseError.Err.Error(),
		})
	}
	return jsonErrorResult(struct {
		Status      string          `json:"status"`
		Diagnostics []diagnosticDTO `json:"diagnostics"`
	}{
		Status:      "error",
		Diagnostics: diagnostics,
	})
}

func reportResponse(root string, report validator.Report) validateBundleResponse {
	diagnostics := make([]diagnosticDTO, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		diagnostics = append(diagnostics, diagnosticDTOFromValidator(root, diagnostic).wire)
	}
	return validateBundleResponse{
		ScannedFiles: report.ScannedFiles,
		Conformant:   report.IsConformant(),
		Errors:       report.ErrorCount(),
		Warnings:     report.WarningCount(),
		Info:         report.InfoCount(),
		Diagnostics:  diagnostics,
	}
}

func relativeSlashPath(root, path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
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

func jsonErrorResult(value any) *mcp.CallToolResult {
	result := jsonTextResult(value)
	result.IsError = true
	return result
}

func toolError(message string) *mcp.CallToolResult {
	return mcp.NewToolResultError(message)
}

func toolErrorf(format string, args ...any) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(format, args...))
}
