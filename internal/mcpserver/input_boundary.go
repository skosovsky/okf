package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

type validatedToolInputContextKey struct{}

type validatedToolInputSeal struct {
	private byte
}

type validatedToolInputMarker struct {
	tool string
	seal *validatedToolInputSeal
}

var canonicalToolInputSeal = new(validatedToolInputSeal)

func withValidatedToolInput(ctx context.Context, tool string) context.Context {
	return context.WithValue(ctx, validatedToolInputContextKey{}, validatedToolInputMarker{
		tool: tool,
		seal: canonicalToolInputSeal,
	})
}

func hasValidatedToolInput(ctx context.Context, tool string) bool {
	marker, ok := ctx.Value(validatedToolInputContextKey{}).(validatedToolInputMarker)
	return ok && marker.tool == tool && marker.seal == canonicalToolInputSeal
}

func requireCanonicalToolInput(
	ctx context.Context,
	tool string,
	request mcp.CallToolRequest,
) *mcp.CallToolResult {
	if result := requestContextCheckpoint(ctx, tool+" request"); result != nil {
		return result
	}
	if hasValidatedToolInput(ctx, tool) {
		return nil
	}
	if result := validateRawMCPArgumentsContext(ctx, request.GetRawArguments()); result != nil {
		return result
	}
	if err := validateContract(tool+".input", request.GetArguments()); err != nil {
		if contractLimitViolation(err) {
			return stableToolError(
				"resource_limit",
				"tool input exceeds the advertised schema limits",
				false,
			)
		}
		return stableToolError(
			"invalid_request",
			"tool input does not match the advertised schema",
			false,
		)
	}
	return nil
}
