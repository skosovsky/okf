package okfcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
)

func cmdMigrateV03(args []string, stdout io.Writer) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--id", Kind: stringFlag},
		{Name: "--actor", Kind: stringFlag},
		{Name: "--assume-v02", Kind: boolFlag},
		{Name: "--path-choices", Kind: stringFlag},
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
	request := mutation.MigrationV03Request{
		ID:        store.ChangeSetID(parsed.value("--id", "migrate-v03")),
		Actor:     store.Actor(parsed.value("--actor", "okf-cli")),
		AssumeV02: parsed.boolValue("--assume-v02"),
	}
	if choices := parsed.value("--path-choices", ""); choices != "" {
		if err := readMigrationV03Choices(choices, &request.Choices); err != nil {
			return 0, err
		}
	}

	ctx := context.Background()
	if !parsed.boolValue("--write") {
		openSource := productionMigrateDependencies().openSource
		source, err := openSource(ctx, root)
		if err != nil {
			return 0, err
		}
		preview, previewErr := mutation.PreviewV03Migration(ctx, source, request)
		if closeErr := source.Close(); closeErr != nil {
			return 0, errors.Join(previewErr, closeErr)
		}
		if previewErr != nil {
			var blocked *store.InvalidChangeSet
			if errors.As(previewErr, &blocked) {
				return 1, json.NewEncoder(stdout).Encode(struct {
					Status      string             `json:"status"`
					Code        string             `json:"code"`
					Message     string             `json:"message"`
					Diagnostics []store.Diagnostic `json:"diagnostics"`
				}{"blocked", blocked.Code, blocked.Error(), blocked.Diagnostics})
			}
			return 0, previewErr
		}
		return 0, json.NewEncoder(stdout).Encode(preview)
	}

	base, err := store.ParseRevision(parsed.value("--base-revision", ""))
	if err != nil {
		return 0, fmt.Errorf("--base-revision: %w", err)
	}
	digest := parsed.value("--plan-digest", "")
	if err := mutation.ValidatePlanDigest(digest); err != nil {
		return 0, fmt.Errorf("--plan-digest: %w", err)
	}
	openStore := productionMigrateDependencies().openStore
	backend, err := openStore(ctx, root)
	if err != nil {
		return 0, err
	}
	receipt, applyErr := mutation.ApplyV03Migration(ctx, backend, mutation.MigrationV03ApplyRequest{
		Request: request, BaseRevision: base, PlanDigest: digest,
	})
	closeErr := backend.Close()
	if receipt.FormatVersion != 0 {
		encodeErr := json.NewEncoder(stdout).Encode(receipt)
		return 0, errors.Join(applyErr, closeErr, encodeErr)
	}
	return 0, errors.Join(applyErr, closeErr)
}

func readMigrationV03Choices(path string, choices *[]store.MigrationV03PathChoice) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, choices); err != nil {
		return fmt.Errorf("read migration path choices: %w", err)
	}
	return nil
}
