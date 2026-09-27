package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skosovsky/okf/backfill"
	okfbundle "github.com/skosovsky/okf/bundle"
	storefs "github.com/skosovsky/okf/store/fs"
	"github.com/skosovsky/okf/validator"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: okf-backfill extract|plan|apply")
	}
	ctx := context.Background()
	switch args[0] {
	case "extract":
		f := flag.NewFlagSet("extract", flag.ContinueOnError)
		repo := f.String("repo", ".", "Git repository")
		base := f.String("base", "", "excluded ancestor commit/ref")
		head := f.String("head", "HEAD", "included head commit/ref")
		out := f.String("out", "", "manifest output JSON")
		limit := f.Int("max-diff-bytes", 64<<10, "captured diff bytes per event")
		first := f.Bool("first-parent", true, "follow first parent of merge commits")
		skip := f.String("skip-glob", "", "comma-separated file path globs to exclude from diff evidence")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *out == "" {
			return fmt.Errorf("-out required")
		}
		skipPaths := []string{}
		if *skip != "" {
			skipPaths = strings.Split(*skip, ",")
		}
		m, err := backfill.Extract(ctx, backfill.Config{Repository: *repo, Base: *base, Head: *head, MaxDiffBytes: *limit, FirstParent: *first, SkipPaths: skipPaths})
		if err != nil {
			return err
		}
		return writeJSON(*out, m)
	case "plan", "apply":
		f := flag.NewFlagSet(args[0], flag.ContinueOnError)
		manifest := f.String("events", "", "frozen event manifest JSON")
		analysis := f.String("analysis", "", "analyst proposal JSON")
		bundle := f.String("bundle", "", "existing OKF bundle")
		out := f.String("out", "", "plan or report output JSON")
		planPath := f.String("plan", "", "frozen plan JSON for apply")
		checkpoint := f.String("checkpoint", "", "durable checkpoint JSON")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *manifest == "" || *analysis == "" || *bundle == "" || *out == "" {
			return fmt.Errorf("-events, -analysis, -bundle and -out required")
		}
		if err := artifactOutsideBundle(*bundle, *out); err != nil {
			return err
		}
		if *checkpoint != "" {
			if err := artifactOutsideBundle(*bundle, *checkpoint); err != nil {
				return err
			}
		}
		var m backfill.Manifest
		var a backfill.Analysis
		if err := readJSON(*manifest, &m); err != nil {
			return err
		}
		if err := readJSON(*analysis, &a); err != nil {
			return err
		}
		s, err := storefs.Open(*bundle, storefs.Config{ValidatorConfig: &validator.ValidatorConfig{TemporalProfile: okfbundle.TemporalProfileInstant}})
		if err != nil {
			return err
		}
		defer s.Close()
		var plan backfill.Plan
		if args[0] == "plan" {
			plan, err = backfill.Prepare(ctx, s, m, a)
			if err != nil {
				return err
			}
			return writeJSON(*out, plan)
		}
		if *checkpoint == "" || *planPath == "" {
			return fmt.Errorf("-checkpoint and -plan required")
		}
		if err := readJSON(*planPath, &plan); err != nil {
			return err
		}
		cp, err := backfill.Apply(ctx, s, plan, m, a, *checkpoint)
		if err != nil {
			return err
		}
		return writeJSON(*out, struct {
			Coverage   backfill.Coverage   `json:"coverage"`
			Checkpoint backfill.Checkpoint `json:"checkpoint"`
		}{plan.Coverage, cp})
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func artifactOutsideBundle(bundleRoot, path string) error {
	root, err := filepath.Abs(bundleRoot)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	return fmt.Errorf("backfill artifact must be outside bundle: %s", path)
}

func readJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
