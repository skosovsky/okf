package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/graph"
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

func handleReadConcept(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	path := id.ToPath(root)
	if !isInside(root, path) {
		return toolError("concept path escapes bundle root"), nil
	}
	if err := rejectReadSymlinks(root, path); err != nil {
		return toolError(err.Error()), nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return toolErrorf("concept not found: %s", id.String()), nil
		}
		return toolErrorf("read concept metadata: %v", err), nil
	}
	if info.IsDir() {
		return toolErrorf("concept path is a directory: %s", id.String()), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return toolErrorf("read concept: %v", err), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func handleValidateBundle(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	root, result := requireBundlePath(request)
	if result != nil {
		return result, nil
	}
	if _, err := bundle.LoadBundle(root); err != nil {
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
	report := validator.ValidatePath(root, &cfg)
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

func handleWriteConcept(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	response, writeErr := writeConcept(root, id, frontmatterText, body)
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
	info, err := os.Lstat(clean)
	if err != nil {
		return "", fmt.Errorf("bundle_path is not accessible: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("bundle_path must not be a symlink")
	}
	if !info.IsDir() {
		return "", fmt.Errorf("bundle_path must be a directory")
	}
	evaluated, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("bundle_path symlink evaluation failed: %w", err)
	}
	return filepath.Clean(evaluated), nil
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
		diagnostics = append(diagnostics, diagnosticDTO{
			Severity: diagnostic.Severity.String(),
			File:     relativeSlashPath(root, diagnostic.File),
			Message:  diagnostic.Message,
		})
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
