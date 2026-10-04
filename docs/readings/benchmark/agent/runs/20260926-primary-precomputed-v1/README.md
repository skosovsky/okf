---
layout: default
lang: en
title: "Primary precomputed Go CLI pilot — 2026-09-26"
permalink: /readings/benchmark/agent/runs/20260926-primary-precomputed-v1/README/
documentation_id: benchmark-agent-runs-20260926-primary-precomputed-v1-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav.html %}


> Historical report, 26 September 2026. This reading edition preserves the findings and limits of the original run; it is not current product guidance. [Original report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-precomputed-v1/README.md), revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="primary-precomputed-go-cli-pilot--2026-09-26"></a>

# Primary precomputed Go CLI pilot — 2026-09-26
{: #section-1}

This is the first complete paired pilot of task 012. The preregistered
[protocol]({{ '/readings/benchmark/agent/PILOT-PRECOMPUTED/' | relative_url }}) ran on corrected commit
`ed0bd12c15b03445070bfc3c2775ab1d980dc822` with the pinned instant-profile
SPEC revision `0b87c52c6ef999286c745e19998fdfcd03d5dbee`,
`gpt-6-luna` at low reasoning effort, and
`corpus/primary_precomputed.json` (SHA-256
`5f9964c1b63871ed8a14f2d420848e62ffa09e9d85cff71890a585e0c8fad639`).
The [plan](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-precomputed-v1/plan.json), [raw rows](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-precomputed-v1/rows.jsonl), and [report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-precomputed-v1/report.json)
are copied byte-for-byte from the run. The analyzer recomputes the saved
report byte-for-byte:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/primary_precomputed.json \
  -plan benchmark/agent/runs/20260926-primary-precomputed-v1/plan.json \
  -rows benchmark/agent/runs/20260926-primary-precomputed-v1/rows.jsonl
```

All eight planned trials completed: four control and four treatment, one
repeat each, zero missing rows, zero operational failures, and zero invalid
raw rows. The analyzer reports `valid=true`. Both arms answered 3/4 correctly
and 1/4 incorrectly; factual error rate was 1/4 in each. On
`repo-parse-output`, control returned `Insufficient evidence` despite the
supplied CLI help excerpt saying that `parse` prints a typed projection.
Treatment answered “Its typed projection as JSON.” It added an unsupported
default-JSON claim: the CLI defaults to text and uses JSON only with `--json`
or `--format json`. The treatment's runner-generated parse projection was
explicitly requested with `--json`; whether that caused the extra claim cannot
be determined from one observation.

The runner made nine Go CLI calls across treatment trials: four validations
and five parses. The model made zero tool calls in either arm. Thus this run
tests model answers from precomputed Go CLI projections; it does not test
whether the model can choose or invoke the CLI itself. Input plus output usage
was 127,346 tokens across the eight calls (control 61,305; treatment 66,041).
The adapter supplied no dollar cost, so cost is unavailable, not zero.
No model shell output or command trace was saved in the rows; the `cli-*`
evidence contains only Go CLI JSON derived from the committed corpus.

This four-case, one-repeat feasibility pilot measured no answer-quality
benefit: both arms scored 3/4. It cannot estimate a general effect or
separate OKF packaging from CLI projections. Metadata-only, before/after
004/005, and writer-to-independent-consumer evaluations remain separate
acceptance work.
