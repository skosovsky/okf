package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

var errMCPConceptCountLimit = errors.New("MCP concept count limit exceeded")

func handleSearchConcepts(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "search_concepts", request); result != nil {
		return result, nil
	}
	query, err := request.RequireString("query")
	if err != nil || strings.TrimSpace(query) == "" {
		return stableToolError("schema_validation", "query must contain non-whitespace text", false), nil
	}
	limit, result := conceptQueryLimit(request)
	if result != nil {
		return result, nil
	}
	profile, result := optionalTemporalProfile(request)
	if result != nil {
		return result, nil
	}
	asOf, result := optionalTemporalReference(request, "as_of", profile)
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
	response, err := searchConceptSnapshot(ctx, loaded, query, limit, asOf, profile)
	if err != nil {
		return conceptQueryError("search concepts", err), nil
	}
	for i := range response.Hits {
		response.Hits[i].Concept.Path = relativeSlashPath(root, response.Hits[i].Concept.Path)
	}
	return jsonStructuredResultContext(ctx, "search concepts", response, response), nil
}

func handleGetNeighbors(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "get_neighbors", request); result != nil {
		return result, nil
	}
	id, result := requireConceptID(request)
	if result != nil {
		return result, nil
	}
	direction, result := optionalStringDefault(request, "direction", "both")
	if result != nil {
		return result, nil
	}
	if direction != "in" && direction != "out" && direction != "both" {
		return stableToolError("schema_validation", "invalid neighbor direction", false), nil
	}
	kinds, result := conceptNeighborKinds(request)
	if result != nil {
		return result, nil
	}
	limit, result := conceptQueryLimit(request)
	if result != nil {
		return result, nil
	}
	profile, result := optionalTemporalProfile(request)
	if result != nil {
		return result, nil
	}
	asOf, result := optionalTemporalReference(request, "as_of", profile)
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
	response, err := neighborConceptSnapshot(ctx, loaded, id, direction, kinds, limit, asOf, profile)
	if err != nil {
		if errors.Is(err, errConceptQueryNotFound) {
			return conceptNotFoundResult(id), nil
		}
		return conceptQueryError("get neighbors", err), nil
	}
	response.Concept.Path = relativeSlashPath(root, response.Concept.Path)
	for i := range response.Edges {
		response.Edges[i].Source.Path = relativeSlashPath(root, response.Edges[i].Source.Path)
		response.Edges[i].Target.Path = relativeSlashPath(root, response.Edges[i].Target.Path)
	}
	return jsonStructuredResultContext(ctx, "get neighbors", response, response), nil
}

func conceptQueryLimit(request mcp.CallToolRequest) (int, *mcp.CallToolResult) {
	value, ok := request.GetArguments()["limit"]
	if !ok {
		return defaultConceptQueryLimit, nil
	}
	var limit int
	switch typed := value.(type) {
	case float64:
		limit = int(typed)
		if typed != float64(limit) {
			return 0, stableToolError("schema_validation", "limit must be an integer", false)
		}
	case int:
		limit = typed
	case int64:
		if typed < 1 || typed > maxConceptQueryLimit {
			return 0, stableToolError("resource_limit", "limit exceeds the advertised bounds", false)
		}
		limit = int(typed)
	case json.Number:
		parsed, err := strconv.Atoi(string(typed))
		if err != nil {
			return 0, stableToolError("schema_validation", "limit must be an integer", false)
		}
		limit = parsed
	default:
		return 0, stableToolError("schema_validation", "limit must be an integer", false)
	}
	if limit < 1 || limit > maxConceptQueryLimit {
		return 0, stableToolError("resource_limit", "limit exceeds the advertised bounds", false)
	}
	return limit, nil
}

func conceptNeighborKinds(request mcp.CallToolRequest) ([]string, *mcp.CallToolResult) {
	value, ok := request.GetArguments()["kinds"]
	if !ok {
		return []string{"navigation", "relation", "source"}, nil
	}
	array, ok := value.([]any)
	if !ok {
		if strings, stringSlice := value.([]string); stringSlice {
			array = make([]any, len(strings))
			for i, item := range strings {
				array[i] = item
			}
			ok = true
		}
	}
	if !ok {
		return nil, stableToolError("schema_validation", "kinds must be an array", false)
	}
	if len(array) == 0 || len(array) > 3 {
		return nil, stableToolError("schema_validation", "invalid kinds count", false)
	}
	seen := map[string]bool{}
	for _, raw := range array {
		kind, ok := raw.(string)
		if !ok || seen[kind] || (kind != "navigation" && kind != "relation" && kind != "source") {
			return nil, stableToolError("schema_validation", "invalid neighbor kinds", false)
		}
		seen[kind] = true
	}
	out := []string{}
	for _, kind := range []string{"navigation", "relation", "source"} {
		if seen[kind] {
			out = append(out, kind)
		}
	}
	return out, nil
}

func conceptQueryError(operation string, err error) *mcp.CallToolResult {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return stableToolError("operation_cancelled", operation+" was cancelled", true)
	}
	if errors.Is(err, errMCPConceptCountLimit) {
		return withRecoveryStep(
			stableToolError("resource_limit", operation+" exceeds MCP resource limits", false),
			"bundle_path", "The selected bundle has too many concepts; use a smaller bundle. Narrowing query will not change this limit.",
		)
	}
	if errors.Is(err, errMCPResourceLimit) {
		if operation == "search concepts" {
			return stableToolErrorWithRecovery("resource_limit", operation+" exceeds MCP resource limits", false)
		}
		return stableToolError("resource_limit", operation+" exceeds MCP resource limits", false)
	}
	return bundleDomainError(operation, operation+" failed", err)
}
