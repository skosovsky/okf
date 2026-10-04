---
layout: default
lang: en
title: "Primary repo-source pilot: adapter startup failure"
permalink: /readings/benchmark/agent/runs/20260926-primary-live/README/
documentation_id: benchmark-agent-runs-20260926-primary-live-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav.html %}


> Historical report, 26 September 2026. This reading edition preserves the findings and limits of the original run; it is not current product guidance. [Original report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-live/README.md), revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="primary-repo-source-pilot-adapter-startup-failure"></a>

# Primary repo-source pilot: adapter startup failure
{: #section-1}

The authorized eight-trial primary pilot ran on clean Git HEAD
`f6ebfebac965e681fa43365d5075007767984e05` using
`benchmark/agent/corpus/primary_realistic.json`. The immutable plan, all eight
raw rows, and the analyzer output are saved beside this file. Four control and
four treatment trials were attempted. All eight failed before a model answer:
`codex-cli 0.137.0` could not decode its model catalog (`unknown variant max`,
expected `none|minimal|low|medium|high|xhigh`). Each raw row contains the
adapter error. There are no model tool traces or answers; no observed toolkit
calls or token usage. Billing cannot be inferred from the zero usage fields.

The analyzer reports `valid=false`, eight operational failures, no factual
attempts, and no quality estimate. The sum of trial durations is 43,828 ms,
within the 360-second wall budget. This consumed the registered eight-call
allowance as attempts; it does not justify an unapproved rerun. The treatment
could not demonstrate direct Go CLI use because Codex failed before inference.

Pinned settings: `gpt-6-luna`, model-version label `gpt-6-luna`, low reasoning
effort, direct toolkit mode, eight-trial cap, 360-second wall cap, 45-second
per-trial timeout, 12,000 observed-token stop condition, unpriced. The Go CLI
binary SHA-256 was
`96c93890c25cba6e9d9ce75779eb4f9f52922f92598b6a55d29a63fcfd955e17`;
the adapter SHA-256 was
`c405f6a976599db2fd7042660aab822074f64ea975f770e1a41a8f6267dc9f09`.
The SPEC revision was `0b87c52c6ef999286c745e19998fdfcd03d5dbee`.

Exact command:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/primary_realistic.json \
  -plan /private/tmp/okf-primary-plan.json \
  -rows /private/tmp/okf-primary-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -go-cli /private/tmp/okf-eval-cli \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit f6ebfebac965e681fa43365d5075007767984e05 \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 8 -max-seconds 360 -max-tokens 12000 -unpriced \
  -toolkit-mode direct \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```

The earlier auto-review refusal, before this more specific authorization, is
recorded separately in `../20260926-primary-blocked/README.md`.
