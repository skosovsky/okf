// Package mcpserver exposes OKF bundles through MCP tools.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const serverVersion = "0.2.0"
const ServerName = "okf-mcp"

// NewServer builds the stdio-capable OKF MCP server.
func NewServer() (*server.MCPServer, error) {
	return newServerWithSchemaLoader(contractSchema)
}

func newServerWithSchemaLoader(load contractSchemaLoader) (*server.MCPServer, error) {
	tools, err := serverToolsWithSchemaLoader(load)
	if err != nil {
		return nil, err
	}
	s := server.NewMCPServer(
		ServerName,
		serverVersion,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
		server.WithOutputSchemaValidation(),
	)
	s.AddTools(tools...)
	return s, nil
}

// ServerTools returns all OKF MCP tools and handlers.
func ServerTools() ([]server.ServerTool, error) {
	return serverToolsWithSchemaLoader(contractSchema)
}

func serverToolsWithSchemaLoader(load contractSchemaLoader) ([]server.ServerTool, error) {
	specs := []struct {
		name        string
		description string
		readOnly    bool
		handler     server.ToolHandlerFunc
	}{
		{name: "list_concepts", description: "Use to browse concept summaries when no search term is known; use search_concepts for a literal query or read_concept for full text. Returns the whole list (up to bundle/output limits), which can be large; then read a selected concept.", readOnly: true, handler: handleListConcepts},
		{name: "search_concepts", description: "Use to find concepts by one literal Unicode case-folded substring in ID, metadata or body; use list_concepts to browse without a query. Returns ranked matches, total and truncated (limit 1–100, default 20; no cursor). If truncated, raise limit or refine the literal query using a distinctive ID/title/phrase, then read_concept.", readOnly: true, handler: handleSearchConcepts},
		{name: "read_concept", description: "Use when a concept ID is known and its full Markdown, frontmatter or source projection is needed; use list_concepts or search_concepts to find IDs. Returns one parsed concept, subject to read bounds; use get_neighbors for related concepts.", readOnly: true, handler: handleReadConcept},
		{name: "get_neighbors", description: "Use for incoming/outgoing Markdown navigation and typed relations around one concept; use read_concept for full text or source details. Returns bounded edges with provenance sources separate, total and truncated (limit 1–100, default 20; no cursor). If truncated, narrow direction/kinds or use get_semantic_graph for complete traversal subject to its byte limit.", readOnly: true, handler: handleGetNeighbors},
		{name: "validate_bundle", description: "Use to check OKF conformance and optional link/orphan guidance; use read_concept to inspect an individual file. Returns diagnostics and guidance, not factual verification; inspect errors before editing.", readOnly: true, handler: handleValidateBundle},
		{name: "get_semantic_graph", description: "Use when a whole-bundle JSON-LD graph is needed; use get_neighbors for one concept's bounded edges. Returns the graph subject to bundle/output limits; inspect nodes and edges before following them.", readOnly: true, handler: handleSemanticGraph},
		{name: "write_concept", description: "Compatibility whole-concept create/update from frontmatter and body; use preview_concept_patch for bounded lossless metadata edits. Stages validation and returns a write receipt or diagnostics; read_concept and validate_bundle afterward.", handler: handleWriteConcept},
		{name: "preview_concept_patch", description: "Use before apply_concept_patch to inspect bounded lossless v0.2 domain edits without publishing; use write_concept only for whole-concept replacement. Returns proposed diff, base revision and plan digest or blockers; review them before apply.", readOnly: true, handler: handlePreviewConceptPatch},
		{name: "apply_concept_patch", description: "Use after reviewing preview_concept_patch; submits the same operations with expected revision and plan digest. Rebuilds and transactionally publishes or reports conflict/diagnostics; validate_bundle afterward. Preview alone is not authorization.", handler: handleApplyConceptPatch},
		{
			name:        "preview_v02_migration",
			description: "Use before apply_v02_migration to inspect an explicit v0.1→v0.2 migration without publishing; use preview_temporal_upgrade for date→instant conversion. Returns source resolution, blockers, proof and plan digest. Reserved index/log citations need exact legacy_entry or resource projection; resolve blockers before apply.",
			readOnly:    true,
			handler:     handlePreviewV02Migration,
		},
		{
			name:        "apply_v02_migration",
			description: "Use after reviewing preview_v02_migration with the same citation evidence and expected source; transition plans also require proof and plan digest. Rebuilds and transactionally publishes or reports conflict/noop; validate_bundle afterward. Preview alone is not authorization.",
			handler:     handleApplyV02Migration,
		},
		{name: "preview_temporal_upgrade", description: "Use to plan explicit date→instant mappings for a v0.2 bundle; use preview_v02_migration for v0.1→v0.2. Returns ready/incomplete/noop, unresolved dates, base revision and plan digest when ready; resolve mappings before apply.", readOnly: true, handler: handlePreviewTemporalUpgrade},
		{name: "apply_temporal_upgrade", description: "Use after reviewing preview_temporal_upgrade with the same mappings, base revision and plan digest. Transactionally upgrades temporal values or returns an operation_rejected error; validate_bundle with the instant profile afterward. Preview alone is not authorization.", handler: handleApplyTemporalUpgrade},
	}
	tools := make([]server.ServerTool, 0, len(specs))
	for _, spec := range specs {
		tool, err := schemaTool(spec.name, spec.description, spec.readOnly, load)
		if err != nil {
			return nil, fmt.Errorf("build MCP tool %s: %w", spec.name, err)
		}
		tools = append(tools, server.ServerTool{
			Tool:    tool,
			Handler: contractToolHandler(spec.name, spec.handler),
		})
	}
	return tools, nil
}

func contractToolHandler(name string, next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if result := requestContextCheckpoint(ctx, name+" request"); result != nil {
			return result, nil
		}
		if result := validateRawMCPArgumentsContext(ctx, request.GetRawArguments()); result != nil {
			return result, nil
		}
		if err := validateContract(name+".input", request.GetArguments()); err != nil {
			if contractLimitViolation(err) {
				return inputContractError(name+".input", "resource_limit", "tool input exceeds the advertised schema limits", err), nil
			}
			return inputContractError(name+".input", "schema_validation", "tool input does not match the advertised schema", err), nil
		}
		result, err := next(withValidatedToolInput(ctx, name), request)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return stableToolError("schema_validation", "tool handler returned no result", false), nil
		}
		return enforceContractResult(name, result), nil
	}
}

func enforceContractResult(name string, result *mcp.CallToolResult) *mcp.CallToolResult {
	if result == nil {
		return stableToolError("schema_validation", "tool handler returned no result", false)
	}
	contract := name + ".output"
	if result.IsError {
		contract = "error"
	}
	if err := validateContract(contract, result.StructuredContent); err != nil {
		if contractLimitViolation(err) {
			return stableToolError("resource_limit", "tool output exceeds the advertised schema limits", false)
		}
		return stableToolError("schema_validation", "tool output does not match the advertised schema", false)
	}
	if !result.IsError {
		if err := validateMutationOutputState(name, result.StructuredContent); err != nil {
			return stableToolError("schema_validation", "tool output violates its advertised state invariants", false)
		}
		if err := validateMigrationOutputSourceState(name, result.StructuredContent); err != nil {
			return stableToolError("schema_validation", "tool output violates its advertised state invariants", false)
		}
	}
	return result
}
