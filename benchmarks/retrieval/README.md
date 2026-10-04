# Frozen offline retrieval comparison

This developer harness compares the old literal `search_concepts` MCP tool with
additive lexical `search_sections`, on exactly the same synthetic Markdown and
24 predeclared queries: 20 answerable and four no-answer, in English and Russian.
It measures retrieval and source attribution. It does not measure LLM answer
quality, hallucinations or whether an agent will choose to call a tool.

The corpus and gold evidence were frozen before section-search implementation.
Do not edit `testdata/corpus`, `testdata/queries.json` or
`testdata/freeze.json` to improve the results. Changes to a
future corpus require a new freeze and a new baseline; retain these original rows.
The tracked `testdata/freeze.json` is a byte-identical copy of the original
`.cursor/tasks/evidence/016/corpus-freeze.json` provenance record, so a fresh
checkout can reproduce the run without the locally ignored task directory.

Run from the repository root with Go 1.25.5 or newer, Git and Python 3. The harness
needs no model provider or external service. The commands below build the actual
baseline server from commit `8e822ea631caaac058fb40aa65c5821352048278`, and the final
server from the current working tree, including uncommitted changes. No commit is
required. Temporary artifacts and Go cache stay outside the repository.

```sh
work=$(mktemp -d /tmp/okf-retrieval.XXXXXX)
mkdir "$work/baseline-source" "$work/go-cache"
git archive 8e822ea631caaac058fb40aa65c5821352048278 | tar -x -C "$work/baseline-source"
GOCACHE="$work/go-cache" go -C "$work/baseline-source" build -o "$work/baseline-mcp" ./cmd/okf-mcp
GOCACHE="$work/go-cache" go build -o "$work/final-mcp" ./cmd/okf-mcp
GOCACHE="$work/go-cache" go build -o "$work/eval" ./benchmarks/retrieval/cmd/okf-retrieval-eval
GOCACHE="$work/go-cache" go test ./benchmarks/retrieval/cmd/okf-retrieval-eval -count=1
go version
uname -a
"$work/eval" --server "$work/baseline-mcp" --tool search_concepts --output "$work/baseline.json"
"$work/eval" --server "$work/final-mcp" --tool search_sections --output "$work/final.json"
python3 - "$work/baseline.json" "$work/final.json" <<'PY'
import json, statistics, sys
reports = [json.load(open(p)) for p in sys.argv[1:]]
for path, report in zip(sys.argv[1:], reports):
    rows = report['rows']
    print(path)
    print('Hit@5:', report['hit_at_5'], 'MRR@5:', report['mrr_at_5'])
    print('No-answer nonempty:', report['no_answer_nonempty'], '/', report['no_answer'])
    print('mean/median latency ms:', statistics.mean(r['latency_ns'] for r in rows)/1e6,
          statistics.median(r['latency_ns'] for r in rows)/1e6)
    print('mean/max response bytes:', statistics.mean(r['response_bytes'] for r in rows),
          max(r['response_bytes'] for r in rows))
baseline, final = reports
assert baseline['queries_sha256'] == final['queries_sha256']
assert baseline['frozen_corpus_sha256'] == final['frozen_corpus_sha256']
assert baseline['expected_snapshot_revision'] == final['expected_snapshot_revision']
assert final['hit_at_5'] >= baseline['hit_at_5']
assert final['mrr_at_5'] >= baseline['mrr_at_5']
assert final['hit_at_5'] > baseline['hit_at_5'] or final['mrr_at_5'] > baseline['mrr_at_5']
print('Retrieval acceptance gate passed on this frozen corpus.')
PY
```

`--output` must be absent: the harness refuses to overwrite a report. Both servers
receive their absolute bundle path in the MCP tool arguments. The stdio server
itself has no `-root` argument. Defaults refer to the frozen corpus, query and
manifest paths above; optional `--bundle`, `--corpus`, `--freeze` permit relocation
only when the manifest's exact bytes and inventory still match.

Before starting either server the harness checks all frozen SHA256 values, the
combined manifest digest, exact file inventory and every manually declared gold
heading, physical line and evidence within its section. It aborts on drift. For
the new arm it also verifies every returned concept/path identity, inclusive
physical line range and section end, heading, percent-encoded locator, source
SHA256, nonempty snippet and whole-bundle snapshot revision. The checker targets
the simple ATX headings in this fixed corpus; it is not a substitute for product
parser regression tests involving fences or other Markdown heading forms.

Each arm runs a fresh stdio server, initializes MCP, then executes every query once
in the same order with `limit: 5`. Stable first-occurrence deduplication projects
these returned hits into a ranked list of at most five **documents**. Hit@5 and
MRR@5 use this projected document rank and the declared gold concept IDs. Repeated
sections of one document consume the equal five-hit response budget, so the new
arm may return fewer distinct documents. This is not section-level recall; the
gold sections are source evidence and attribution checks, not a second metric.
No-answer cases contribute only to `no_answer_nonempty`, not to either denominator.

Latency is one wall-clock observation per tool call, from sending `tools/call` to
receiving its MCP result. It includes tool execution and IPC/JSON decoding but
excludes server startup, initialization and the harness's attribution/scoring
checks. There are no retries or warmups. Means and medians describe this single
ordered run; they do not establish a statistically significant speed difference.
Response bytes are the serialized MCP result object, including its content and
structured content, excluding the outer JSON-RPC envelope. They are measured
identically in both arms. No token count is claimed.

Each JSON report contains all raw MCP responses, rows, aggregate scores, query
and corpus hashes, expected source revision and actual server binary SHA256.
Save the reports together with the commands, machine/Go version, baseline commit
and final working-tree patch digest in task evidence. Retain the raw rows so
metrics can be recalculated without another retrieval run.
