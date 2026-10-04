package okfcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/skosovsky/okf/setup"
)

func cmdSetup(args []string, stdout io.Writer) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{{Name: "--source", Kind: stringFlag}, {Name: "--target", Kind: stringFlag}, {Name: "--files", Kind: stringFlag}, {Name: "--type", Kind: stringFlag}, {Name: "--apply", Kind: boolFlag}, {Name: "--plan-digest", Kind: stringFlag}, {Name: "--json", Kind: boolFlag}})
	if err != nil {
		return 0, err
	}
	if len(parsed.positionals) != 0 {
		return 0, fmt.Errorf("setup uses explicit --source and --target")
	}
	opts := setup.Options{Source: parsed.value("--source", ""), Target: parsed.value("--target", ""), Type: parsed.value("--type", "")}
	if raw := parsed.value("--files", ""); raw != "" {
		opts.Files = strings.Split(raw, ",")
	}
	if !parsed.boolValue("--apply") && parsed.has("--plan-digest") {
		return 0, fmt.Errorf("--plan-digest requires --apply")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var result setup.Plan
	if parsed.boolValue("--apply") {
		result, err = setup.Apply(ctx, opts, parsed.value("--plan-digest", ""))
	} else {
		result, err = setup.Preview(ctx, opts)
	}
	if parsed.boolValue("--json") {
		if encodeErr := json.NewEncoder(stdout).Encode(result); encodeErr != nil {
			return 0, encodeErr
		}
	} else {
		if _, writeErr := fmt.Fprintf(stdout, "Setup: %d documents; applicable=%t; published=%t\nPlan digest: %s\n", len(result.Files), result.Applicable, result.Published, result.Digest); writeErr != nil {
			return 0, writeErr
		}
		for _, d := range result.Diagnostics {
			if _, writeErr := fmt.Fprintf(stdout, "%s %s: %s\n", d.Code, d.Path, d.Message); writeErr != nil {
				return 0, writeErr
			}
		}
	}
	if err != nil {
		return 0, err
	}
	if !result.Applicable {
		return 1, nil
	}
	return 0, nil
}
