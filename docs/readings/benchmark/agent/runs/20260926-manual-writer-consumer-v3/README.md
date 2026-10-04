---
layout: default
lang: en
title: "Manually prepared OKF bundle → independent consumer — 2026-09-26"
permalink: /readings/benchmark/agent/runs/20260926-manual-writer-consumer-v3/README/
documentation_id: benchmark-agent-runs-20260926-manual-writer-consumer-v3-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav.html %}


> Historical report, 26 September 2026. This reading edition preserves the findings and limits of the original run; it is not current product guidance. [Original report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-manual-writer-consumer-v3/README.md), revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="manually-prepared-okf-bundle--independent-consumer--2026-09-26"></a>

# Manually prepared OKF bundle → independent consumer — 2026-09-26
{: #section-1}

The frozen [protocol]({{ '/readings/benchmark/agent/WRITER-CONSUMER-BASELINE/' | relative_url }}) ran from clean commit
`dd8989ba36b8e27ac5762e2e0984cac7f88e8738` with `gpt-6-luna` at low
reasoning effort. The one synthetic queue-mode reversal in
[`writer_consumer_baseline.json`](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/writer_consumer_baseline.json)
(SHA-256 `3fd56d975fa6570df6db81a939eea72d51178034a559882a52b1bb753baf522a`)
compares raw records against a **human-prepared** OKF bundle containing the
same answer and evidence ID. The offline Go validation test for that bundle
passed. A fresh consumer invocation received each arm; no AI writer, Go
backfill, or upkeep checker participated. The blinded adapter (SHA-256
`8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9`)
sent the model the question and arm artifacts without the harness labels or
gold answer.

The saved [plan](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-manual-writer-consumer-v3/plan.json), [raw rows](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-manual-writer-consumer-v3/rows.jsonl), and [report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-manual-writer-consumer-v3/report.json)
are byte-identical to the original run files in `/private/tmp`. Reanalysis
reproduces the report byte-for-byte:

```sh
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go run \
  ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/writer_consumer_baseline.json \
  -plan benchmark/agent/runs/20260926-manual-writer-consumer-v3/plan.json \
  -rows benchmark/agent/runs/20260926-manual-writer-consumer-v3/rows.jsonl
```

Both planned calls completed and the report is `valid=true`: control **1/1**
correct and treatment **1/1** correct. Both consumers answered exactly `B` and
cited `decision`. Neither was stale, wrong, ungradable, or a refusal; there
were no operational failures or model tool calls. Thus this case shows a
consumer can answer from the manually prepared bundle, with **no measured
quality difference** against the raw-record control.

Observed usage was **30,291 input + 52 output = 30,343 tokens** across two
calls; 27,136 input tokens were reported as cached and are already included
in input usage. Summed model elapsed time was 11.326 seconds. USD cost was
unavailable, so the report's zero `cost_usd` with zero `cost_known_trials`
does not mean the calls were free.

This one-case, one-repeat synthetic baseline tests independent consumer
reading of a valid, human-prepared bundle. It does not measure AI writer
quality, the task-010 backfill or task-013 checker, or a general benefit from
OKF. Earlier v1/v2 protocol drafts had no model observations and are outside
these denominators.
