---
layout: default
lang: en
title: "Go CLI before/after 004/005 — 2026-09-26"
permalink: /readings/benchmark/agent/runs/20260926-beforeafter-v1/README/
documentation_id: benchmark-agent-runs-20260926-beforeafter-v1-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav.html %}


> Historical report, 26 September 2026. This reading edition preserves the findings and limits of the original run; it is not current product guidance. [Original report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-beforeafter-v1/README.md), revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="go-cli-beforeafter-004005--2026-09-26"></a>

# Go CLI before/after 004/005 — 2026-09-26
{: #section-1}

The preregistered [protocol]({{ '/readings/benchmark/agent/beforeafter/LIVE/' | relative_url }}) ran against the old Go
CLI at `ed7ddc28cd127682023bd150ee377906b817e099` and the corrected CLI at
`260a7cb701f08e21efd94e0b04db9b491f7e1db7`. The [plan](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-beforeafter-v1/plan.json),
[raw rows](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-beforeafter-v1/rows.jsonl), and [report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-beforeafter-v1/report.json) preserve the completed run.
The five-case [corpus](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/beforeafter/corpus.json) has SHA-256
`05d45be91e4ab15f49b8f71c4085eca91e85dddad3af6792755d824f536a6db4`;
the referenced fixture set has SHA-256
`3e7b800654e1a6cfa53680815eb968ab348b36419444f66b3b0dfdf5631db156`.
The plan pins the two CLI binaries, adapter, local Codex runtime, prompt, fixed
clock, model settings, and alternating arm order. The local runtime pin does
not independently identify the remote model snapshot.

All **20/20** planned rows are present. The analyzer reports `valid=true`, zero
missing or malformed rows, and zero operational failures. Eighteen rows made
model calls; the two old-CLI 004 rows were recorded as `revision_unsupported`
without model calls. The model was `gpt-6-luna` at low reasoning effort. Each
answer was checked by the frozen exact-answer Go grader: the normalized answer
must match the preregistered answer or alias **and** cite the original source
artifact ID. CLI diagnostic text alone cannot satisfy the evidence condition.

For the **eight paired 005 date-spec comparisons**, the old CLI arm scored
correct in 4/8 and the corrected arm in 6/8 under this strict grader. Four
pairs scored correct in both arms, two changed from wrong to correct, and two
remained wrong. Both score changes were on the root-intro question: one old
answer was `Insufficient evidence`, while the other gave the full repository
path `scaccogatto/okf-skills` instead of the preregistered short name
`okf-skills`. The inline-footnote answers in both arms identified
`sources[].id` but included an article, so the exact grader
marked them wrong. These scores should not be read as semantic factual-error
rates. The wrapped-log and root-upkeep questions scored correct in both arms.
The old CLI's well-formed exit-1 validation reports yielded 96 classified
diagnostics across its eight 005 rows: eight root-intro, eight unknown-root-field,
and 80 wrapped-log-item findings. They were retained as model-visible evidence,
not counted as process failures. The corrected 005 validation reports accepted
the fixtures.

The **004 instant-spec case** is a separate capability result: the old CLI
could not select that SPEC revision in either repeat, while the corrected CLI
validated it and the model answered correctly in both repeats. These two rows
do not form factual-answer pairs and are excluded from the 005 comparison.

The runner made 36 Go CLI projection calls and the model made zero tool calls.
The 18 model calls used **347,499 input-plus-output tokens**; the adapter
reported no per-call USD cost, so the dollar cost is unknown, not zero. The
four questions in the 005 stratum, each repeated twice, are a small descriptive
sample. This run shows two better paired **exact-grader scores** under the
corrected CLI projections; one is a normalization miss on a substantively
correct old answer. It does not establish a general factual-accuracy or product
benefit, or isolate which part of the changed CLI evidence caused the scores.

The saved report is reproducible byte-for-byte from the saved plan and rows:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/beforeafter/cmd/okf-before-after analyze \
  -repo-root . \
  -corpus benchmark/agent/beforeafter/corpus.json \
  -plan benchmark/agent/runs/20260926-beforeafter-v1/plan.json \
  -rows benchmark/agent/runs/20260926-beforeafter-v1/rows.jsonl
```
