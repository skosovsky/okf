# Frozen offline retrieval result

Authoritative raw reports: `retrieval-baseline-checked.json` and `retrieval-final-reviewed.json`. Earlier `retrieval-baseline.json`/`retrieval-final.json` are retained historical first passes. Gold corpus and query bytes have not changed. Final harness independently checks gold heading/line/evidence, inventory and every section attribution.

| Tool | Document Hit@5 | Document MRR@5 | No-answer nonempty | Median latency ms | Mean response bytes | Max response bytes |
| --- | --- | --- | --- | --- | --- | --- |
| search_concepts | 0.1 | 0.1 | 0/4 | 2.179 | 385.8 | 966 |
| search_sections | 0.95 | 0.95 | 0/4 | 3.174 | 1499.9 | 2261 |

Gate passed: both quality metrics improve from 2/20 to 19/20 on these fixed queries. q13 remains a miss: the RU query and source use different inflections; this literal lexical tokenizer does not stem Russian. Four no-answer queries return no hits in both arms.

Each arm gets five wire hits. Stable first-occurrence deduplication projects them to unique documents; MRR uses compact projected document rank. This is document-level evaluation, not section-level gold scoring. New section ranges/locators/snippets/digests are verified separately. The section arm can return fewer than five unique documents under the same wire-hit budget.

One sequential pass per arm, fresh server process; initialization excluded, each measured tool call includes load, parse and serialize. Latency is a descriptive observation on a warm developer machine, not a throughput or statistically significant performance claim. Size is the serialized MCP result object in bytes, excluding the outer JSON-RPC envelope and including text and structuredContent duplication; token counts are not reported. This toy synthetic corpus does not establish broad relevance or LLM answer quality.

Reproduction, baseline git archive commit and metric re-computation: `benchmarks/retrieval/README.md`. Original freeze record: `corpus-freeze.json`, byte-exact tracked manifest: `benchmarks/retrieval/testdata/freeze.json`. Baseline source commit 8e822ea631caaac058fb40aa65c5821352048278; final source is uncommitted, identified by final-state product digest. Binary SHA values are included in each raw report.

Commands executed from repository root with GOCACHE=/tmp/okf016-cache GOMODCACHE=/tmp/okf016-mod:

```sh
go build -o /tmp/okf016-mcp ./cmd/okf-mcp
go run ./benchmarks/retrieval/cmd/okf-retrieval-eval --server /tmp/okf016-mcp-baseline --tool search_concepts --output .cursor/tasks/evidence/016/retrieval-baseline-checked.json
go run ./benchmarks/retrieval/cmd/okf-retrieval-eval --server /tmp/okf016-mcp --tool search_sections --output .cursor/tasks/evidence/016/retrieval-final-reviewed.json
```

Environment: go version go1.26.5 darwin/arm64; macOS-27.0.1-arm64-arm-64bit-Mach-O
