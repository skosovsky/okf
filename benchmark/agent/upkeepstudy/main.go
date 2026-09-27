// Command upkeepstudy prepares a task-013 plan or analyzes saved observations.
// It never calls a model or mutates a repository.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/skosovsky/okf/benchmark/agent"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "prepare" {
		return prepare(args[1:])
	}
	if len(args) > 0 && args[0] == "analyze" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("upkeepstudy", flag.ContinueOnError)
	planPath := fs.String("plan", "", "pre-registered study plan JSON")
	rowsPath := fs.String("rows", "", "saved writer/consumer rows JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *planPath == "" || *rowsPath == "" || fs.NArg() != 0 {
		return errors.New("usage: upkeepstudy -plan PLAN.json -rows ROWS.json")
	}
	var plan agent.UpkeepStudy
	if err := read(*planPath, &plan); err != nil {
		return err
	}
	var rows agent.UpkeepStudyRows
	if err := read(*rowsPath, &rows); err != nil {
		return err
	}
	report, err := agent.AnalyzeUpkeepStudy(plan, rows)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func prepare(args []string) error {
	fs := flag.NewFlagSet("prepare", flag.ContinueOnError)
	corpusPath := fs.String("corpus", "", "committed backfill corpus JSON")
	outPath := fs.String("out", "", "exclusive output plan JSON")
	id := fs.String("id", "", "run ID")
	repeats := fs.Int("repeats", 1, "pre-registered repeats")
	writerModel := fs.String("writer-model", "", "pinned writer model")
	consumerModel := fs.String("consumer-model", "", "pinned consumer model")
	writerPrompt := fs.String("writer-prompt-sha256", "", "writer prompt SHA-256")
	consumerPrompt := fs.String("consumer-prompt-sha256", "", "consumer prompt SHA-256")
	checker := fs.String("checker-sha256", "", "checker executable SHA-256")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *corpusPath == "" || *outPath == "" || fs.NArg() != 0 {
		return errors.New("prepare requires -corpus and -out")
	}
	f, err := os.Open(*corpusPath)
	if err != nil {
		return err
	}
	cases, sourceHash, err := agent.LoadCorpus(f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	plan, err := agent.BuildUpkeepStudyFromBackfill(cases, sourceHash, *id, *repeats, *writerModel, *consumerModel, *writerPrompt, *consumerPrompt, *checker)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(*outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(plan); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func read(file string, dst any) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if err != nil {
		return err
	}
	if len(b) > 16<<20 {
		return fmt.Errorf("%s: JSON exceeds 16 MiB", file)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: trailing JSON", file)
	}
	return nil
}
