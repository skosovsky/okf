package okfcli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/skosovsky/okf/retrieval"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

func cmdSearch(args []string, stdout io.Writer) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{{Name: "--query", Kind: stringFlag}, {Name: "--limit", Kind: stringFlag}, {Name: "--json", Kind: boolFlag}})
	if err != nil {
		return 0, err
	}
	root, err := parsed.onePositional("<bundle>")
	if err != nil {
		return 0, err
	}
	query := parsed.value("--query", "")
	if _, err = retrieval.QueryTerms(query); err != nil {
		return 0, err
	}
	limit := retrieval.DefaultLimit
	if raw := parsed.value("--limit", ""); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > retrieval.MaxLimit {
			return 0, fmt.Errorf("limit must be 1..100")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	b, err := retrieval.Load(ctx, root)
	if err != nil {
		return 0, err
	}
	result, err := retrieval.Search(ctx, b, query, limit)
	if err != nil {
		return 0, err
	}
	if parsed.boolValue("--json") {
		return 0, json.NewEncoder(stdout).Encode(result)
	}
	for _, h := range result.Hits {
		if _, err = fmt.Fprintf(stdout, "%s lines %d–%d · %s\n%s\n", renderTextString(h.Path), h.LineStart, h.LineEnd, renderTextString(h.Heading), renderTextString(h.Snippet)); err != nil {
			return 0, err
		}
	}
	_, err = fmt.Fprintf(stdout, "%d sections matched; truncated=%t\n", result.Total, result.Truncated)
	return 0, err
}
