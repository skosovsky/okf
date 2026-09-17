// Package mcpserver exposes OKF bundles through MCP tools.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const serverVersion = "0.2.0"

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
		"okf-mcp",
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
		{name: "list_concepts", description: "List parsed OKF concepts for navigation.", readOnly: true, handler: handleListConcepts},
		{name: "read_concept", description: "Read one OKF concept Markdown file with frontmatter.", readOnly: true, handler: handleReadConcept},
		{name: "validate_bundle", description: "Validate an OKF bundle and return conformance and guidance separately.", readOnly: true, handler: handleValidateBundle},
		{name: "get_semantic_graph", description: "Return the OKF semantic graph as JSON-LD.", readOnly: true, handler: handleSemanticGraph},
		{name: "write_concept", description: "Create or update one whole OKF concept through validation staging.", handler: handleWriteConcept},
		{name: "preview_concept_patch", description: "Preview bounded lossless OKF v0.2 domain edits without publishing.", readOnly: true, handler: handlePreviewConceptPatch},
		{name: "apply_concept_patch", description: "Rebuild and publish a revision- and digest-bound concept patch.", handler: handleApplyConceptPatch},
		{
			name:        "preview_v02_migration",
			description: "Preview an explicit OKF v0.1 to v0.2 migration without publishing. Reserved index/log citation mappings require replay-sufficient legacy_entry or resource projection evidence.",
			readOnly:    true,
			handler:     handlePreviewV02Migration,
		},
		{
			name:        "apply_v02_migration",
			description: "Rebuild and transactionally publish a revision- and digest-bound OKF v0.2 migration using the same replay-sufficient citation evidence as preview.",
			handler:     handleApplyV02Migration,
		},
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
				return stableToolError("resource_limit", "tool input exceeds the advertised schema limits", false), nil
			}
			return stableToolError("schema_validation", "tool input does not match the advertised schema", false), nil
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
