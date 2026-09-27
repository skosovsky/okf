package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

type temporalUpgradeArguments struct {
	BundlePath string `json:"bundle_path"`
	ID         string `json:"id"`
	Actor      string `json:"actor"`
	Mappings   []struct {
		Concept string `json:"concept"`
		Path    string `json:"path"`
		From    string `json:"from"`
		To      string `json:"to"`
	} `json:"mappings"`
	BaseRevision string `json:"base_revision"`
	PlanDigest   string `json:"plan_digest"`
}

func temporalUpgradeRequest(args temporalUpgradeArguments) (mutation.TemporalUpgradeRequest, error) {
	request := mutation.TemporalUpgradeRequest{ID: store.ChangeSetID(args.ID), Actor: store.Actor(args.Actor)}
	for _, mapping := range args.Mappings {
		id, err := bundle.ParseConceptID(mapping.Concept)
		if err != nil {
			return mutation.TemporalUpgradeRequest{}, err
		}
		request.Mappings = append(request.Mappings, mutation.TemporalUpgradeMapping{Concept: id, Path: mapping.Path, From: mapping.From, To: mapping.To})
	}
	return request, nil
}

func decodeTemporalUpgradeArguments(request mcp.CallToolRequest) (temporalUpgradeArguments, error) {
	data, err := json.Marshal(request.GetArguments())
	if err != nil {
		return temporalUpgradeArguments{}, err
	}
	var args temporalUpgradeArguments
	err = json.Unmarshal(data, &args)
	return args, err
}

func handlePreviewTemporalUpgrade(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "preview_temporal_upgrade", request); result != nil {
		return result, nil
	}
	root, result := requireValidatedBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	args, err := decodeTemporalUpgradeArguments(request)
	if err != nil {
		return stableToolError("invalid_argument", "cannot decode temporal upgrade input", false), nil
	}
	planRequest, err := temporalUpgradeRequest(args)
	if err != nil {
		return stableToolError("invalid_argument", "invalid temporal mapping", false), nil
	}
	source, _, err := captureMigrationSource(ctx, root)
	if err != nil {
		return stableToolError("operation_rejected", "cannot capture temporal upgrade source", false), nil
	}
	preview, err := mutation.NewTemporalUpgradePlanner().Preview(ctx, source, planRequest)
	status := "ready"
	if errors.Is(err, mutation.ErrTemporalUpgradeIncomplete) {
		status = "incomplete"
	} else if errors.Is(err, mutation.ErrTemporalUpgradeNoop) {
		status = "noop"
	} else if err != nil {
		return stableToolError("operation_rejected", "temporal upgrade preview failed", false), nil
	}
	unresolved := make([]map[string]string, 0, len(preview.Unresolved))
	for _, field := range preview.Unresolved {
		unresolved = append(unresolved, map[string]string{"concept": field.Concept.String(), "path": field.Path, "from": field.From})
	}
	structured := map[string]any{"status": status, "base_revision": preview.Preview.BaseRevision.String(), "result_revision": preview.Preview.ResultRevision.String(), "unresolved": unresolved}
	if preview.PlanDigest != "" {
		structured["plan_digest"] = preview.PlanDigest
	}
	return mcp.NewToolResultStructured(structured, ""), nil
}

func handleApplyTemporalUpgrade(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := requireCanonicalToolInput(ctx, "apply_temporal_upgrade", request); result != nil {
		return result, nil
	}
	root, result := requireValidatedBundlePath(ctx, request)
	if result != nil {
		return result, nil
	}
	args, err := decodeTemporalUpgradeArguments(request)
	if err != nil {
		return stableToolError("invalid_argument", "cannot decode temporal upgrade input", false), nil
	}
	planRequest, err := temporalUpgradeRequest(args)
	if err != nil {
		return stableToolError("invalid_argument", "invalid temporal mapping", false), nil
	}
	base, err := store.ParseRevision(args.BaseRevision)
	if err != nil {
		return stableToolError("invalid_argument", "invalid base revision", false), nil
	}
	if err := mutation.ValidatePlanDigest(args.PlanDigest); err != nil {
		return stableToolError("invalid_argument", "invalid plan digest", false), nil
	}
	backend, err := openTemporalUpgradeStoreContext(ctx, root)
	if err != nil {
		return stableToolError("operation_rejected", "cannot open temporal upgrade store", false), nil
	}
	receipt, applyErr := mutation.NewTemporalUpgradePlanner().Apply(ctx, backend, mutation.TemporalUpgradeApplyRequest{Request: planRequest, BaseRevision: base, PlanDigest: args.PlanDigest})
	closeErr := backend.Close()
	if applyErr != nil {
		return stableToolError("operation_rejected", "temporal upgrade apply failed", false), nil
	}
	if closeErr != nil {
		return stableToolError("operation_rejected", "temporal upgrade store cleanup failed", false), nil
	}
	return mcp.NewToolResultStructured(map[string]any{"status": "applied", "request_digest": receipt.RequestDigest, "result_revision": receipt.ResultRevision.String()}, ""), nil
}

// The upgrade uses the same transactional filesystem backend as semantic
// patches; its separate Store interface needs Commit as well as Close.
func openTemporalUpgradeStoreContext(ctx context.Context, root string) (*storefs.Store, error) {
	return storefs.OpenContext(ctx, root, storefs.Config{})
}
