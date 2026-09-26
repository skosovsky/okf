package okfcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

type temporalUpgradeMappingInput struct {
	Concept string `json:"concept"`
	Path    string `json:"path"`
	From    string `json:"from"`
	To      string `json:"to"`
}

type temporalUpgradeInput struct {
	Mappings []temporalUpgradeMappingInput `json:"mappings"`
}

func cmdTemporalUpgrade(args []string, stdout io.Writer) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--mappings", Kind: stringFlag},
		{Name: "--id", Kind: stringFlag},
		{Name: "--actor", Kind: stringFlag},
		{Name: "--base-revision", Kind: stringFlag},
		{Name: "--plan-digest", Kind: stringFlag},
		{Name: "--write", Kind: boolFlag},
	})
	if err != nil {
		return 0, err
	}
	root, err := parsed.onePositional("<bundle>")
	if err != nil {
		return 0, err
	}
	request := mutation.TemporalUpgradeRequest{ID: store.ChangeSetID(parsed.value("--id", "")), Actor: store.Actor(parsed.value("--actor", ""))}
	if path := parsed.value("--mappings", ""); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		var input temporalUpgradeInput
		if err := json.Unmarshal(data, &input); err != nil {
			return 0, fmt.Errorf("decode temporal mappings: %w", err)
		}
		for _, item := range input.Mappings {
			id, err := bundle.ParseConceptID(item.Concept)
			if err != nil {
				return 0, err
			}
			request.Mappings = append(request.Mappings, mutation.TemporalUpgradeMapping{Concept: id, Path: item.Path, From: item.From, To: item.To})
		}
	}
	ctx := context.Background()
	planner := mutation.NewTemporalUpgradePlanner()
	if !parsed.boolValue("--write") {
		preview, err := planner.Preview(ctx, &bundle.FileSystemSource{Root: root}, request)
		if err != nil && err != mutation.ErrTemporalUpgradeIncomplete && err != mutation.ErrTemporalUpgradeNoop {
			return 0, err
		}
		status := "ready"
		if err == mutation.ErrTemporalUpgradeIncomplete {
			status = "incomplete"
		}
		if err == mutation.ErrTemporalUpgradeNoop {
			status = "noop"
		}
		return 0, json.NewEncoder(stdout).Encode(struct {
			Status     string                            `json:"status"`
			Preview    store.Preview                     `json:"preview"`
			Unresolved []mutation.TemporalUpgradeMapping `json:"unresolved"`
			PlanDigest string                            `json:"plan_digest,omitempty"`
		}{status, preview.Preview, preview.Unresolved, preview.PlanDigest})
	}
	base, err := store.ParseRevision(parsed.value("--base-revision", ""))
	if err != nil {
		return 0, fmt.Errorf("--base-revision: %w", err)
	}
	digest := parsed.value("--plan-digest", "")
	if err := mutation.ValidatePlanDigest(digest); err != nil {
		return 0, fmt.Errorf("--plan-digest: %w", err)
	}
	backend, err := openCLITemporalUpgradeStore(ctx, root)
	if err != nil {
		return 0, err
	}
	receipt, applyErr := planner.Apply(ctx, backend, mutation.TemporalUpgradeApplyRequest{Request: request, BaseRevision: base, PlanDigest: digest})
	closeErr := backend.Close()
	if applyErr != nil {
		return 0, applyErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	return 0, json.NewEncoder(stdout).Encode(struct {
		Status  string              `json:"status"`
		Receipt store.CommitReceipt `json:"receipt"`
	}{"applied", receipt})
}

func openCLITemporalUpgradeStore(ctx context.Context, root string) (*storefs.Store, error) {
	return storefs.OpenContext(ctx, root, storefs.Config{})
}
