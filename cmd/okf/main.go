package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/graph"
	"github.com/skosovsky/okf/validator"
	"gopkg.in/yaml.v3"
)

const usage = `okf - Open Knowledge Format toolkit

okf is a command-line toolkit for building, validating, analyzing, and exporting Open Knowledge Format (OKF) bundles.

USAGE:
    okf <command> [args]

COMMANDS:
    validate             Check a bundle against OKF v0.1 conformance
    info     <bundle>    Summarize a bundle (concepts, types, links, version)
    index    <bundle>    (Re)generate every index.md in the bundle
    graph    <bundle>    Export Markdown links and YAML semantic relations (-format text|dot|mermaid|json-ld|ntriples; --dot for legacy DOT)
    parse    <file>      Parse one concept document and print its structure
    fmt      <file>      Normalize a document by parse + re-serialize (-w writes)

OPTIONS:
    -h, --help           Show this help
    -V, --version        Show version`

func main() {
	exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

var exit = os.Exit

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 1
	}

	cmd := args[0]
	rest := args[1:]

	var (
		code int
		err  error
	)
	switch cmd {
	case "validate":
		code, err = cmdValidate(rest, stdout)
	case "info":
		code, err = cmdInfo(rest, stdout)
	case "index":
		code, err = cmdIndex(rest, stdout)
	case "graph":
		code, err = cmdGraph(rest, stdout)
	case "parse":
		code, err = cmdParse(rest, stdout)
	case "fmt":
		code, err = cmdFmt(rest, stdout)
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, usage)
		return 0
	case "-V", "--version", "version":
		fmt.Fprintf(stdout, "okf %s (OKF spec v%s)\n", cliVersion(), bundle.OKFVersion)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand: %s\n\n%s\n", cmd, usage)
		return 1
	}

	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return code
}

func cmdValidate(args []string, stdout io.Writer) (int, error) {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("path", ".", "path to bundle root")
	strict := flags.Bool("strict", false, "enable strict OKF guidance checks")
	checkLinks := flags.Bool("check-links", false, "check internal links")
	checkOrphans := flags.Bool("check-orphans", false, "check local index.md orphan coverage")
	if err := flags.Parse(args); err != nil {
		return 0, err
	}
	if extra := flags.Args(); len(extra) > 0 {
		return 0, fmt.Errorf("unexpected validate argument: %s", extra[0])
	}

	cfg := validator.ValidatorConfig{
		Strict:       *strict,
		CheckLinks:   *checkLinks,
		CheckOrphans: *checkOrphans,
	}
	report := validator.ValidatePath(*path, &cfg)

	fmt.Fprintf(stdout, "Validating bundle: %s\n\n", *path)
	for _, diagnostic := range report.Diagnostics {
		fmt.Fprintln(stdout, diagnostic)
	}
	errors := report.ErrorCount()
	warnings := report.WarningCount()
	infos := report.InfoCount()
	result := "PASS"
	if !report.IsConformant() {
		result = "FAIL"
	}
	fmt.Fprintf(stdout, "\n---\n")
	fmt.Fprintf(stdout, "Scanned %d files.\n", report.ScannedFiles)
	fmt.Fprintf(stdout, "Result: %s (%d errors, %d warnings, %d info)\n", result, errors, warnings, infos)
	return report.ExitCode(), nil
}

func cmdInfo(args []string, stdout io.Writer) (int, error) {
	path, err := positional(args, "<bundle>")
	if err != nil {
		return 0, err
	}
	b, err := bundle.LoadBundle(path)
	if err != nil {
		return 0, err
	}

	fmt.Fprintf(stdout, "bundle:     %s\n", b.Root())
	if version, ok := b.OKFVersion(); ok {
		fmt.Fprintf(stdout, "okf_version: %s\n", version)
	}
	fmt.Fprintf(stdout, "concepts:   %d\n", b.Len())
	fmt.Fprintf(stdout, "index.md:   %d\n", len(b.IndexFiles()))
	fmt.Fprintf(stdout, "log.md:     %d\n", len(b.LogFiles()))

	byType := make(map[string]int)
	for _, concept := range b.Concepts() {
		typ, ok := concept.Document.Frontmatter.Type()
		if !ok {
			typ = "(none)"
		}
		byType[typ]++
	}
	if len(byType) > 0 {
		fmt.Fprintln(stdout, "\ntypes:")
		for _, typ := range sortedKeys(byType) {
			fmt.Fprintf(stdout, "  %4d  %s\n", byType[typ], typ)
		}
	}

	totalLinks := 0
	for _, concept := range b.Concepts() {
		totalLinks += len(b.LinksFrom(concept.ID))
	}
	fmt.Fprintf(stdout, "\nlinks:      %d internal (%d broken)\n", totalLinks, len(b.BrokenLinks()))

	parseErrors := b.ParseErrors()
	if len(parseErrors) > 0 {
		fmt.Fprintln(stdout, "\nunparseable files:")
		for _, parseError := range parseErrors {
			fmt.Fprintf(stdout, "  %s: %v\n", parseError.Path, parseError.Err)
		}
	}
	return 0, nil
}

func cmdIndex(args []string, stdout io.Writer) (int, error) {
	path, err := positional(args, "<bundle>")
	if err != nil {
		return 0, err
	}
	written, err := bundle.RegenerateIndexes(path)
	if err != nil {
		return 0, err
	}
	if len(written) == 0 {
		fmt.Fprintln(stdout, "no index files written (empty bundle?)")
		return 0, nil
	}
	for _, path := range written {
		fmt.Fprintf(stdout, "wrote %s\n", path)
	}
	fmt.Fprintf(stdout, "\n%d index file(s) regenerated.\n", len(written))
	return 0, nil
}

func cmdGraph(args []string, stdout io.Writer) (int, error) {
	opts, err := parseGraphArgs(args)
	if err != nil {
		return 0, err
	}
	b, err := bundle.LoadBundle(opts.path)
	if err != nil {
		return 0, err
	}

	switch opts.format {
	case graphFormatText:
		if err := graph.RenderText(stdout, b); err != nil {
			return 0, err
		}
	case graphFormatDOT:
		if err := graph.RenderDOT(stdout, b); err != nil {
			return 0, err
		}
	case graphFormatMermaid:
		if err := graph.RenderMermaid(stdout, b); err != nil {
			return 0, err
		}
	case graphFormatJSONLD:
		if err := graph.RenderJSONLD(stdout, b); err != nil {
			return 0, err
		}
	case graphFormatNTriples:
		if err := graph.RenderNTriples(stdout, b); err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("unsupported graph format: %s", opts.format)
	}
	return 0, nil
}

type graphFormat string

const (
	graphFormatText     graphFormat = "text"
	graphFormatDOT      graphFormat = "dot"
	graphFormatMermaid  graphFormat = "mermaid"
	graphFormatJSONLD   graphFormat = "json-ld"
	graphFormatNTriples graphFormat = "ntriples"
)

type graphOptions struct {
	path   string
	format graphFormat
}

func parseGraphArgs(args []string) (graphOptions, error) {
	opts := graphOptions{format: graphFormatText}
	formatSet := false
	dotSet := false
	dotValue := false
	var paths []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			paths = append(paths, args[i+1:]...)
			i = len(args)
		case arg == "-format" || arg == "--format":
			if i+1 >= len(args) {
				return graphOptions{}, fmt.Errorf("flag needs an argument: %s", arg)
			}
			i++
			opts.format = graphFormat(args[i])
			formatSet = true
		case strings.HasPrefix(arg, "-format="):
			opts.format = graphFormat(strings.TrimPrefix(arg, "-format="))
			formatSet = true
		case strings.HasPrefix(arg, "--format="):
			opts.format = graphFormat(strings.TrimPrefix(arg, "--format="))
			formatSet = true
		case arg == "-dot" || arg == "--dot":
			dotSet = true
			dotValue = true
		case strings.HasPrefix(arg, "-dot="):
			value, err := parseGraphBoolFlag("-dot", strings.TrimPrefix(arg, "-dot="))
			if err != nil {
				return graphOptions{}, err
			}
			dotSet = true
			dotValue = value
		case strings.HasPrefix(arg, "--dot="):
			value, err := parseGraphBoolFlag("--dot", strings.TrimPrefix(arg, "--dot="))
			if err != nil {
				return graphOptions{}, err
			}
			dotSet = true
			dotValue = value
		case strings.HasPrefix(arg, "-"):
			return graphOptions{}, fmt.Errorf("unknown graph flag: %s", arg)
		default:
			paths = append(paths, arg)
		}
	}

	if dotSet && dotValue {
		if formatSet && opts.format != graphFormatDOT {
			return graphOptions{}, fmt.Errorf("cannot use --dot with -format=%s", opts.format)
		}
		opts.format = graphFormatDOT
	}

	switch opts.format {
	case graphFormatText, graphFormatDOT, graphFormatMermaid, graphFormatJSONLD, graphFormatNTriples:
	default:
		return graphOptions{}, fmt.Errorf("unsupported graph format: %s", opts.format)
	}

	if len(paths) == 0 {
		return graphOptions{}, fmt.Errorf("missing <bundle>")
	}
	if len(paths) > 1 {
		return graphOptions{}, fmt.Errorf("unexpected graph argument: %s", paths[1])
	}
	opts.path = paths[0]
	return opts, nil
}

func parseGraphBoolFlag(name, value string) (bool, error) {
	switch strings.ToLower(value) {
	case "1", "t", "true":
		return true, nil
	case "0", "f", "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q for %s", value, name)
	}
}

func cmdParse(args []string, stdout io.Writer) (int, error) {
	path, err := positional(args, "<file>")
	if err != nil {
		return 0, err
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	document, err := bundle.ParseDocument(string(text))
	if err != nil {
		return 0, err
	}

	keys := document.Frontmatter.Keys()
	fmt.Fprintf(stdout, "frontmatter (%d key(s)):\n", len(keys))
	for _, key := range keys {
		value, _ := document.Frontmatter.Get(key)
		fmt.Fprintf(stdout, "  %s: %s\n", key, formatYAMLValue(value))
	}

	conformant := document.ValidateConformance() == nil
	fmt.Fprintf(stdout, "\nhas non-empty string `type`: %t\n", conformant)
	fmt.Fprintf(stdout, "body: %d byte(s)\n", len([]byte(document.Body)))

	links := document.Links()
	if len(links) > 0 {
		fmt.Fprintf(stdout, "\nlinks (%d):\n", len(links))
		for _, link := range links {
			fmt.Fprintf(stdout, "  [%s] %s -> %s\n", cliLinkKind(link.Kind), link.Text, link.Target)
		}
	}

	citations := document.Citations()
	if len(citations) > 0 {
		fmt.Fprintf(stdout, "\ncitations (%d):\n", len(citations))
		for _, citation := range citations {
			fmt.Fprintf(stdout, "  [%d] %s\n", citation.Number, citation.Raw)
		}
	}

	if conformant {
		return 0, nil
	}
	return 1, nil
}

func cmdFmt(args []string, stdout io.Writer) (int, error) {
	path, err := positional(args, "<file>")
	if err != nil {
		return 0, err
	}
	write := hasFlag(args, "-w") || hasFlag(args, "--write")
	text, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	document, err := bundle.ParseDocument(string(text))
	if err != nil {
		return 0, err
	}
	out, err := document.Serialize()
	if err != nil {
		return 0, err
	}

	if write {
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			return 0, err
		}
		fmt.Fprintf(stdout, "formatted %s\n", path)
		return 0, nil
	}
	fmt.Fprint(stdout, out)
	return 0, nil
}

func positional(args []string, what string) (string, error) {
	for i, arg := range args {
		if arg == "--" && i+1 < len(args) {
			return args[i+1], nil
		}
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg, nil
		}
	}
	return "", fmt.Errorf("missing %s", what)
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func sortedKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cliVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

func cliLinkKind(kind bundle.LinkKind) string {
	switch kind {
	case bundle.LinkAbsolute:
		return "Absolute"
	case bundle.LinkRelative:
		return "Relative"
	case bundle.LinkExternal:
		return "External"
	case bundle.LinkAnchor:
		return "Anchor"
	default:
		return "Other"
	}
}

func formatYAMLValue(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Value
	case yaml.SequenceNode:
		parts := make([]string, 0, len(node.Content))
		for _, item := range node.Content {
			parts = append(parts, formatYAMLValue(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case yaml.MappingNode:
		parts := make([]string, 0, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			parts = append(parts, formatYAMLValue(node.Content[i])+": "+formatYAMLValue(node.Content[i+1]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return ""
	}
}
