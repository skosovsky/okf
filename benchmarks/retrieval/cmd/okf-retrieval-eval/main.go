// Command okf-retrieval-eval compares actual stdio MCP retrieval responses on a frozen corpus.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Gold struct {
	ID       string `json:"concept_id"`
	Heading  string `json:"heading"`
	Line     int    `json:"line_start"`
	Evidence string `json:"evidence"`
}
type Case struct {
	ID       string `json:"id"`
	Query    string `json:"query"`
	Relevant []Gold `json:"relevant"`
}
type Corpus struct {
	Cases []Case `json:"cases"`
}
type Row struct {
	ID         string          `json:"id"`
	Query      string          `json:"query"`
	Answerable bool            `json:"answerable"`
	IDs        []string        `json:"document_ids"`
	Hit        bool            `json:"hit_at_5"`
	RR         float64         `json:"reciprocal_rank_at_5"`
	Latency    int64           `json:"latency_ns"`
	Bytes      int             `json:"response_bytes"`
	Raw        json.RawMessage `json:"raw"`
}
type Report struct {
	Tool               string  `json:"tool"`
	ServerDigest       string  `json:"server_sha256"`
	FrozenCorpusDigest string  `json:"frozen_corpus_sha256"`
	SnapshotRevision   string  `json:"expected_snapshot_revision"`
	CorpusDigest       string  `json:"queries_sha256"`
	Mapping            string  `json:"comparison_mapping"`
	Answerable         int     `json:"answerable"`
	NoAnswer           int     `json:"no_answer"`
	Hit                float64 `json:"hit_at_5"`
	MRR                float64 `json:"mrr_at_5"`
	NoAnswerHits       int     `json:"no_answer_nonempty"`
	Rows               []Row   `json:"rows"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	server := flag.String("server", "", "absolute MCP server binary")
	tool := flag.String("tool", "search_concepts", "search_concepts or search_sections")
	corpus := flag.String("corpus", "benchmarks/retrieval/testdata/queries.json", "frozen queries")
	root := flag.String("bundle", "benchmarks/retrieval/testdata/corpus", "bundle root")
	freeze := flag.String("freeze", "benchmarks/retrieval/testdata/freeze.json", "pre-implementation freeze manifest")
	out := flag.String("output", "", "new JSON report path")
	flag.Parse()
	if !filepath.IsAbs(*server) || *out == "" {
		return fmt.Errorf("absolute --server and --output required")
	}
	if *tool != "search_concepts" && *tool != "search_sections" {
		return fmt.Errorf("unknown tool")
	}
	source, err := os.ReadFile(*corpus)
	if err != nil {
		return err
	}
	var cases Corpus
	if err = json.Unmarshal(source, &cases); err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	snapshot, frozenDigest, err := verifyFreeze(*freeze, *corpus, rootAbs)
	if err != nil {
		return err
	}
	if err = validateGold(cases, snapshot); err != nil {
		return err
	}
	revision := snapshotRevision(snapshot)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, *server)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	defer func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() }()
	encoder := json.NewEncoder(stdin)
	decoder := json.NewDecoder(stdout)
	seq := 0
	call := func(method string, params any) (json.RawMessage, error) {
		seq++
		if e := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": seq, "method": method, "params": params}); e != nil {
			return nil, e
		}
		for {
			var resp struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if e := decoder.Decode(&resp); e != nil {
				return nil, fmt.Errorf("decode: %w (%s)", e, stderr.String())
			}
			if resp.ID != seq {
				continue
			}
			if len(resp.Error) > 0 {
				return nil, fmt.Errorf("RPC: %s", resp.Error)
			}
			return resp.Result, nil
		}
	}
	if _, err = call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "okf-retrieval-eval", "version": "1"}}); err != nil {
		return err
	}
	if err = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return err
	}
	bin, err := os.ReadFile(*server)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(bin)
	cs := sha256.Sum256(source)
	report := Report{Tool: *tool, ServerDigest: hex.EncodeToString(sum[:]), CorpusDigest: hex.EncodeToString(cs[:]), FrozenCorpusDigest: frozenDigest, SnapshotRevision: revision, Mapping: "Document-level: each arm returns at most five hits. Stable first occurrence deduplication projects these hits to a ranked list of at most five documents; reciprocal rank uses this projected document rank. This equal five-hit budget may return fewer distinct documents in the section arm. Section identity/ranges/locators/snippets and snapshot digest are verified against the frozen source. No-answer queries are excluded from Hit/MRR and reported separately.", Rows: []Row{}}
	for _, c := range cases.Cases {
		start := time.Now()
		raw, e := call("tools/call", map[string]any{"name": *tool, "arguments": map[string]any{"bundle_path": rootAbs, "query": c.Query, "limit": 5}})
		if e != nil {
			return e
		}
		row := Row{ID: c.ID, Query: c.Query, Answerable: len(c.Relevant) > 0, Latency: time.Since(start).Nanoseconds(), Bytes: len(raw), Raw: raw, IDs: []string{}}
		var response toolResponse
		if e = json.Unmarshal(raw, &response); e != nil {
			return e
		}
		if response.Structured == nil {
			return fmt.Errorf("%s missing structured response", c.ID)
		}
		if response.IsError {
			return fmt.Errorf("%s tool error: %s", c.ID, raw)
		}
		if *tool == "search_sections" && response.Structured.Revision != revision {
			return fmt.Errorf("%s invalid snapshot revision", c.ID)
		}
		for _, h := range response.Structured.Hits {
			if *tool == "search_sections" {
				if e := validateHit(h, snapshot); e != nil {
					return fmt.Errorf("%s: %w", c.ID, e)
				}
			}
		}
		row.IDs, e = documentIDs(response.Structured.Hits, *tool)
		if e != nil {
			return fmt.Errorf("%s: %w", c.ID, e)
		}
		if row.Answerable {
			report.Answerable++
			row.Hit, row.RR = score(row.IDs, c.Relevant)
			if row.Hit {
				report.Hit++
			}
			report.MRR += row.RR
		} else {
			report.NoAnswer++
			if len(row.IDs) > 0 {
				report.NoAnswerHits++
			}
		}
		report.Rows = append(report.Rows, row)
	}
	if report.Answerable > 0 {
		report.Hit /= float64(report.Answerable)
		report.MRR /= float64(report.Answerable)
	}
	file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	err = enc.Encode(report)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
