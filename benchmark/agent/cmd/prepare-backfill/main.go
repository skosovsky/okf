package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/skosovsky/okf/benchmark/agent"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	manifest := flag.String("manifest", "", "frozen backfill manifest JSON")
	analysis := flag.String("analysis", "", "reviewed backfill analysis JSON")
	corpus := flag.String("corpus", "", "exclusive output corpus JSON")
	coverage := flag.String("coverage", "", "exclusive output coverage JSON")
	flag.Parse()
	if *manifest == "" || *analysis == "" || *corpus == "" || *coverage == "" {
		return fmt.Errorf("manifest, analysis, corpus and coverage paths required")
	}
	m, err := os.ReadFile(*manifest)
	if err != nil {
		return err
	}
	a, err := os.ReadFile(*analysis)
	if err != nil {
		return err
	}
	cases, report, err := agent.BuildReversalFixtureCorpus(context.Background(), m, a)
	if err != nil {
		return err
	}
	cf, err := os.OpenFile(*corpus, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer cf.Close()
	if err := json.NewEncoder(cf).Encode(cases); err != nil {
		return err
	}
	if err := cf.Sync(); err != nil {
		return err
	}
	rf, err := os.OpenFile(*coverage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer rf.Close()
	if err := json.NewEncoder(rf).Encode(report); err != nil {
		return err
	}
	return rf.Sync()
}
