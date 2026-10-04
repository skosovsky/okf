package mcpserver

import (
	"context"
	"errors"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/retrieval"
)

func handleSearchSections(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if r := requireCanonicalToolInput(ctx, "search_sections", request); r != nil {
		return r, nil
	}
	query, err := request.RequireString("query")
	if err != nil {
		return stableToolError("schema_validation", "query is required", false), nil
	}
	if _, err = retrieval.QueryTerms(query); err != nil {
		if errors.Is(err, retrieval.ErrLimit) {
			return stableToolError("resource_limit", "query exceeds term bounds", false), nil
		}
		return stableToolError("schema_validation", "query must contain Unicode letters or numbers", false), nil
	}
	limit, r := conceptQueryLimit(request)
	if r != nil {
		return r, nil
	}
	root, r := requireBundlePath(ctx, request)
	if r != nil {
		return r, nil
	}
	b, err := loadBundleContext(ctx, root)
	if err != nil {
		return bundleLoadError("load bundle", err), nil
	}
	if r := parseErrorsResult(root, b.ParseErrors()); r != nil {
		return r, nil
	}
	result, err := retrieval.Search(ctx, b, query, limit)
	if err != nil {
		if errors.Is(err, retrieval.ErrLimit) {
			return stableToolError("resource_limit", "section search exceeds bounds", false), nil
		}
		return conceptQueryError("search sections", err), nil
	}
	return jsonStructuredResultContext(ctx, "search sections", result, result), nil
}
