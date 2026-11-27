package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

type migrationV03Arguments struct {
	ID           store.ChangeSetID              `json:"id"`
	Actor        store.Actor                    `json:"actor"`
	AssumeV02    bool                           `json:"assume_v02"`
	Choices      []store.MigrationV03PathChoice `json:"path_choices"`
	BaseRevision string                         `json:"base_revision"`
	PlanDigest   string                         `json:"plan_digest"`
}

func (args migrationV03Arguments) request() mutation.MigrationV03Request {
	return mutation.MigrationV03Request{ID: args.ID, Actor: args.Actor, AssumeV02: args.AssumeV02, Choices: args.Choices}
}

func decodeMigrationV03Arguments(request mcp.CallToolRequest) (migrationV03Arguments, error) {
	payload, err := json.Marshal(request.GetArguments())
	if err != nil {
		return migrationV03Arguments{}, err
	}
	var args migrationV03Arguments
	err = json.Unmarshal(payload, &args)
	return args, err
}

type migrationV03PreviewResponse struct {
	Status         string                `json:"status"`
	BaseRevision   string                `json:"base_revision,omitempty"`
	ResultRevision string                `json:"result_revision,omitempty"`
	PlanDigest     string                `json:"plan_digest,omitempty"`
	Files          []string              `json:"files"`
	Details        []string              `json:"details"`
	Diagnostics    []migrationV03Finding `json:"diagnostics"`
}

type migrationV03Finding struct {
	Code    string `json:"code"`
	File    string `json:"file"`
	Message string `json:"message"`
}

func handlePreviewV03Migration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "preview_v03_migration", request); result != nil {
		return result, nil
	}
	root, result := requireValidatedBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	args, err := decodeMigrationV03Arguments(request)
	if err != nil {
		return stableToolError("invalid_argument", "invalid migration input", false), nil
	}
	source, _, err := captureMigrationSource(ctx, root)
	if err != nil {
		return bundleLoadError("capture migration source", err), nil
	}
	preview, err := mutation.PreviewV03Migration(ctx, source, args.request())
	response := migrationV03PreviewResponse{Files: []string{}, Details: []string{}, Diagnostics: []migrationV03Finding{}}
	var blocked *store.InvalidChangeSet
	switch {
	case errors.As(err, &blocked):
		response.Status = "blocked"
		if len(blocked.Diagnostics) == 0 {
			response.Diagnostics = append(response.Diagnostics, migrationV03Finding{Code: blocked.Code, Message: redactSensitivePaths(blocked.Error())})
		}
		for _, diagnostic := range blocked.Diagnostics {
			response.Diagnostics = append(response.Diagnostics, migrationV03Finding{diagnostic.Code, relativeSlashPath(root, diagnostic.File), redactSensitivePaths(diagnostic.Message)})
		}
	case err != nil:
		return bundleDomainError("preview migration", "cannot preview OKF 0.3 migration", err), nil
	default:
		response.Status = "ready"
		response.BaseRevision = preview.Preview.BaseRevision.String()
		response.ResultRevision = preview.Preview.ResultRevision.String()
		response.PlanDigest = preview.PlanDigest
		for _, write := range preview.Preview.Writes {
			response.Files = append(response.Files, write.Path)
		}
		for _, operation := range preview.Preview.Plan {
			response.Details = append(response.Details, operation.Details...)
		}
	}
	if _, err := estimateMCPJSONOutputSizeContext(ctx, response, maxBundleTotalBytes); err != nil {
		return bundleDomainError("preview migration", "migration preview output exceeds MCP limits", err), nil
	}
	return jsonStructuredResultContext(ctx, "preview OKF 0.3 migration", response, response), nil
}

func handleApplyV03Migration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "apply_v03_migration", request); result != nil {
		return result, nil
	}
	root, result := requireValidatedBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	args, err := decodeMigrationV03Arguments(request)
	if err != nil {
		return stableToolError("invalid_argument", "invalid migration input", false), nil
	}
	base, err := store.ParseRevision(args.BaseRevision)
	if err != nil || mutation.ValidatePlanDigest(args.PlanDigest) != nil {
		return stableToolError("invalid_argument", "invalid migration proof", false), nil
	}
	backend, err := openTransactionalStoreContext(ctx, root, withMCPStoreLimits(storefs.Config{}))
	if err != nil {
		return bundleDomainError("open migration store", "cannot open OKF 0.3 migration store", err), nil
	}
	receipt, applyErr := mutation.ApplyV03Migration(ctx, backend, mutation.MigrationV03ApplyRequest{
		Request: args.request(), BaseRevision: base, PlanDigest: args.PlanDigest,
	})
	_ = backend.Close()
	if receipt.FormatVersion != 0 {
		status := "applied"
		if applyErr != nil {
			status = "committed_with_error"
		}
		structured := map[string]any{"status": status, "request_digest": receipt.RequestDigest, "result_revision": receipt.ResultRevision.String()}
		return jsonStructuredResultContext(context.WithoutCancel(ctx), "apply OKF 0.3 migration", structured, structured), nil
	}
	return withRecoveryStep(bundleDomainError("apply migration", "cannot apply OKF 0.3 migration", applyErr),
		"plan_digest", "Run preview_v03_migration again if the bundle or path choices changed."), nil
}
