---
layout: default
lang: en
title: "Backfill → independent consumer — 2026-09-26"
permalink: /readings/benchmark/agent/runs/20260926-backfill-consumer-v2/README/
documentation_id: benchmark-agent-runs-20260926-backfill-consumer-v2-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav.html %}


> Historical report, 26 September 2026. This reading edition preserves the findings and limits of the original run; it is not current product guidance. [Original report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-backfill-consumer-v2/README.md), revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="backfill--independent-consumer--2026-09-26"></a>

# Backfill → independent consumer — 2026-09-26
{: #section-1}

The preregistered [protocol]({{ '/readings/benchmark/agent/BACKFILL-CONSUMER/' | relative_url }}) ran from clean commit
`dd8989ba36b8e27ac5762e2e0984cac7f88e8738`, with SPEC revision
`0b87c52c6ef999286c745e19998fdfcd03d5dbee`, `gpt-6-luna` at low
reasoning effort, and the Go-backfilled
[`backfill_cases.json`](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/backfill_cases.json) corpus (SHA-256
`4c23a1ce55f666c737dde8eea0baf44ba82d5e0365585b98a699a72a2a03f85f`).
The treatment consumer saw the resulting OKF bundle; the control saw raw Git
evidence. Both received the same current-code diff for that question. The
blinded adapter (SHA-256
`8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9`)
exposed no case ID, arm, gold answer, or Go CLI projection to the model. The
[plan](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-backfill-consumer-v2/plan.json), [raw rows](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-backfill-consumer-v2/rows.jsonl), and [report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-backfill-consumer-v2/report.json) preserve
the run. Reanalysis reproduces the saved report byte-for-byte:

```sh
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go run \
  ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/backfill_cases.json \
  -plan benchmark/agent/runs/20260926-backfill-consumer-v2/plan.json \
  -rows benchmark/agent/runs/20260926-backfill-consumer-v2/rows.jsonl
```

All **6/6** planned calls completed, with no missing or invalid rows, refusal,
stale answer, ungradable answer, operational failure, or model tool call. The
report is `valid=true`. By the preregistered exact-answer grader, control was
**3/3 correct** and treatment **2/3 correct**. The single treatment `wrong`
verdict is the reversal question: the model answered `Mode B`, citing
`decision`, while the frozen expected answer was exactly `B`. Its selected
mode and citation agree with the evidence; this is an **exact-format miss**,
not an observed factual reversal. On the truncated handler, both arms answered
`Insufficient evidence`; on the captured current Go diff, both answered `B`.
Do not rewrite the frozen grader after inspecting this answer.

The separate [backfill coverage](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/backfill_coverage.json) records
3 included of 3 considered Git events, zero rejected or missing events, and
one explicitly truncated event. That is coverage of event disposition, not a
claim that every generated fact is true or that the handler rollout completed.

Observed usage across six calls was **92,316 input + 163 output = 92,479
tokens**; 81,408 input tokens were reported as cached and are not additional
calls. Summed model elapsed time was 33.255 seconds. USD cost was unavailable,
so the report's zero `cost_usd` with zero `cost_known_trials` must not be read
as a free run.

This three-pair, single-repeat synthetic pilot supports only the narrow
finding above. It does not show a consumer-quality gain from the bundle, test
autonomous CLI use, or estimate behavior across real repositories. The v1
protocol was superseded before any model observations because its adapter
would have exposed harness labels; those unrun cases are outside these
denominators.
