---
layout: default
lang: en
permalink: /readings/benchmark/agent/PILOT-PRECOMPUTED/
documentation_id: benchmark-agent-pilot-precomputed
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
documentation_status: historical
historical: true
title: "Preregistered primary pilot: precomputed Go CLI projections"
---
{% include nav.html %}

> Historical reading edition. Checked on 2026-10-04 against revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. The protocol and results below retain their original historical status; this is not a new run or authorization. [Original English document](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/PILOT-PRECOMPUTED.md).

<a id="preregistered-primary-pilot-precomputed-go-cli-projections"></a>

# Preregistered primary pilot: precomputed Go CLI projections
{: #section-1}

This is a proposal for a **new** eight-call run, not a record of completed
results or authorization for external model calls. The four realistic cases in
[`corpus/primary_precomputed.json` — original](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/primary_precomputed.json) ask about facts present in this repository's
Go code. This new corpus makes the CLI delegation question unambiguous;
`primary_realistic.json` and `cases.json` remain byte-identical to the inputs
of historical plans so their recorded corpus hashes stay reproducible. In
each control trial the model receives ordinary source excerpts.
In each treatment trial it receives the same answer-bearing excerpts in a
valid OKF bundle, plus JSON produced by the Go CLI implementation's
`validate` and `parse` commands. The runner calls `internal/okfcli.Run` in
process before the model call, saves the exact `cli-*` projections in the raw
row, and counts those calls. The Codex adapter sends the complete request JSON
to the model. The model does **not** have to discover or invoke a shell command.

This contrast estimates the combined effect of OKF packaging and the CLI
projections. It does not isolate metadata, test autonomous tool choice, or
measure general repository performance. The existing direct-mode attempts in
[`PILOT.md`]({{ '/readings/benchmark/agent/PILOT/' | relative_url }}) remain a separate protocol and retain their `valid=false` reports.
The exact-v1 grader requires an original evidence ID such as `current`.
`cli-current` is a derived projection, not a substitute citation. The shared
instruction tells both arms to cite original IDs; a correct answer citing
only `cli-current` is ungradable and is not manually promoted afterward.
This is a feasibility pilot: the preregistered primary endpoint is whether
all eight paired rows are collected and analyzable without operational
failure or budget violation. There is no minimum efficacy threshold and no
claim that any observed arm difference proves a benefit. Factual error rate
and completion rate are secondary descriptive metrics with the denominators
defined in [`README.md`]({{ '/readings/benchmark/agent/README/' | relative_url }}).

Before starting, obtain explicit authorization for these eight OpenAI Codex
calls and the transfer of the four cases' source excerpts and OKF documents.
Freeze this protocol, the corpus, and the corrected 004/005 implementation in
one clean commit. The runner requires `-commit` to match clean HEAD and checks
the pinned upstream SPEC revision and content. Build the adapter from that
checkout and verify the runtime binary's version and SHA-256. Save both raw
files outside the repository until their content has been reviewed:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go build \
  -o /private/tmp/okf-eval-codex-adapter \
  ./benchmark/agent/cmd/codex-adapter

OKF_EVAL_COMMIT=$(git rev-parse HEAD)
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/primary_precomputed.json \
  -plan /private/tmp/okf-primary-precomputed-v1-plan.json \
  -rows /private/tmp/okf-primary-precomputed-v1-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id primary-precomputed-20260926-v1 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit "$OKF_EVAL_COMMIT" \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 8 -max-seconds 360 -max-tokens 150000 -unpriced \
  -toolkit-mode treatment \
  -model-tool-access 'Codex read-only ephemeral temp; full request JSON; runner embeds in-process Go CLI projections in treatment'
```

The hard limits are eight adapter invocations and 45 seconds per model
invocation. The runner sets a 360-second context deadline, checked by model
calls and between rows; in-process CLI precomputation does not currently
observe that context and can overrun the deadline. The 150,000
input-plus-output token threshold is
checked **after** each call and can be exceeded by one call. `-unpriced` means
the adapter cannot provide a dollar price; it does not mean zero cost. Stop
on the first detected exceeded observed-token threshold or context deadline; retain
partial rows and mark the result invalid. Plan and rows use exclusive file
creation, so never reuse these paths after an attempted run.

Run the saved-row analyzer with the same pinned corpus:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/primary_precomputed.json \
  -plan /private/tmp/okf-primary-precomputed-v1-plan.json \
  -rows /private/tmp/okf-primary-precomputed-v1-rows.jsonl
```

Publish the plan, raw rows, analyzer report, and manual inspection of any
unexpected citations or tool activity. A valid plan requires all eight rows,
no operational failures, unchanged pinned inputs, and budget compliance;
`valid=true` says nothing about factual correctness. Report correct, stale,
wrong, refusal, and ungradable separately for each arm, plus toolkit calls,
model tool calls, tokens, elapsed time, and unavailable dollar costs. With
four cases and one repeat, results are descriptive and offer no reliable
effect-size estimate. Metadata-only, before/after 004/005, and
writer-to-independent-consumer checks require separate protocols and runs.
