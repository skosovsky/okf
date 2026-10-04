package okfcli

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/viewer"
)

// cmdView exports an offline HTML viewer using the loaded bundle snapshot.
func cmdView(args []string, stdout io.Writer) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--output", Kind: stringFlag},
		{Name: "--lang", Kind: stringFlag},
		{Name: "--overwrite", Kind: boolFlag},
		{Name: "--max-nodes", Kind: stringFlag},
		{Name: "--as-of", Kind: stringFlag},
		{Name: "--spec", Kind: stringFlag},
		{Name: "--temporal-profile", Kind: stringFlag},
	})
	if err != nil {
		return 0, err
	}
	path, err := parsed.onePositional("<bundle>")
	if err != nil {
		return 0, err
	}
	output := parsed.value("--output", "")
	if output == "" {
		return 0, fmt.Errorf("--output is required")
	}
	spec, err := parseSpecSelector(parsed.value("--spec", "auto"))
	if err != nil {
		return 0, err
	}
	maxNodes := viewer.DefaultMaxNodes
	if raw := parsed.value("--max-nodes", ""); raw != "" {
		maxNodes, err = strconv.Atoi(raw)
		if err != nil || maxNodes <= 0 {
			return 0, fmt.Errorf("invalid --max-nodes %q (want positive integer)", raw)
		}
	}
	language := parsed.value("--lang", "en")
	if language != "en" && language != "ru" {
		return 0, fmt.Errorf("invalid --lang %q (want en or ru)", language)
	}
	b, err := bundle.LoadBundle(path)
	if err != nil {
		return 0, err
	}
	if _, err := resolveBundleVersion(b, spec); err != nil {
		return 0, err
	}
	profile := bundle.TemporalProfileDate
	if raw := parsed.value("--temporal-profile", ""); raw != "" {
		profile = bundle.TemporalProfile(raw)
	}
	if _, err := bundle.NormalizeTemporalProfile(profile); err != nil {
		return 0, err
	}
	asOf, err := parseTemporalReference(parsed.value("--as-of", ""), profile)
	if err != nil {
		return 0, err
	}
	if err := viewer.Export(context.Background(), b, output, viewer.Options{Language: language, AsOf: asOf, MaxNodes: maxNodes, Overwrite: parsed.boolValue("--overwrite"), TemporalProfile: profile, VersionSelector: selectorAssertion(spec)}); err != nil {
		return 0, err
	}
	_, err = fmt.Fprintf(stdout, "wrote %s\n", renderTextString(output))
	return 0, err
}
