// Package mcpserver exposes OKF bundles through MCP tools.
package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const serverVersion = "0.1.0"

// NewServer builds the stdio-capable OKF MCP server.
func NewServer() *server.MCPServer {
	s := server.NewMCPServer(
		"okf-mcp",
		serverVersion,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)
	s.AddTools(ServerTools()...)
	return s
}

// ServerTools returns all OKF MCP tools and handlers.
func ServerTools() []server.ServerTool {
	tools := []server.ServerTool{
		{Tool: listConceptsTool(), Handler: handleListConcepts},
		{Tool: readConceptTool(), Handler: handleReadConcept},
		{Tool: validateBundleTool(), Handler: handleValidateBundle},
		{Tool: semanticGraphTool(), Handler: handleSemanticGraph},
		{Tool: writeConceptTool(), Handler: handleWriteConcept},
	}
	return tools
}

func baseBundlePathToolOptions(description string) []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithDescription(description),
		mcp.WithString("bundle_path",
			mcp.Required(),
			mcp.Description("Absolute path to the OKF bundle directory."),
		),
		mcp.WithSchemaAdditionalProperties(false),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	}
}

func listConceptsTool() mcp.Tool {
	opts := baseBundlePathToolOptions("List parsed OKF concepts for navigation.")
	return mcp.NewTool("list_concepts", opts...)
}

func readConceptTool() mcp.Tool {
	opts := baseBundlePathToolOptions("Read one OKF concept Markdown file with frontmatter.")
	opts = append(opts,
		mcp.WithString("concept_id",
			mcp.Required(),
			mcp.Description("Canonical slash-separated OKF concept id without .md suffix."),
		),
	)
	return mcp.NewTool("read_concept", opts...)
}

func validateBundleTool() mcp.Tool {
	opts := baseBundlePathToolOptions("Validate an OKF bundle and return a diagnostics summary.")
	opts = append(opts,
		mcp.WithBoolean("strict", mcp.Description("Enable strict OKF guidance checks."), mcp.DefaultBool(false)),
		mcp.WithBoolean("check_links", mcp.Description("Check internal links and anchors."), mcp.DefaultBool(false)),
		mcp.WithBoolean("check_orphans", mcp.Description("Check local index.md orphan coverage."), mcp.DefaultBool(false)),
	)
	return mcp.NewTool("validate_bundle", opts...)
}

func semanticGraphTool() mcp.Tool {
	opts := baseBundlePathToolOptions("Return the OKF semantic graph as JSON-LD.")
	return mcp.NewTool("get_semantic_graph", opts...)
}

func writeConceptTool() mcp.Tool {
	opts := baseBundlePathToolOptions("Create or update one OKF concept through validation staging.")
	opts = append(opts,
		mcp.WithString("concept_id",
			mcp.Required(),
			mcp.Description("Canonical slash-separated OKF concept id without .md suffix."),
		),
		mcp.WithString("frontmatter",
			mcp.Required(),
			mcp.Description("YAML mapping without frontmatter delimiters."),
		),
		mcp.WithString("body",
			mcp.Required(),
			mcp.Description("Markdown body."),
		),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
	)
	return mcp.NewTool("write_concept", opts...)
}
