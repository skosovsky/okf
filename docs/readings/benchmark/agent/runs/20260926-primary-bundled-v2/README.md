---
layout: default
lang: en
title: "Primary bundled Codex pilot: stopped after one trial"
permalink: /readings/benchmark/agent/runs/20260926-primary-bundled-v2/README/
documentation_id: benchmark-agent-runs-20260926-primary-bundled-v2-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav.html %}


> Historical report, 26 September 2026. This reading edition preserves the findings and limits of the original run; it is not current product guidance. [Original report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-bundled-v2/README.md), revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="primary-bundled-codex-pilot-stopped-after-one-trial"></a>

# Primary bundled Codex pilot: stopped after one trial
{: #section-1}

The separately authorized run `primary-bundled-20260926-v2` used clean HEAD
`d6bd02824b089a4e9c2e1080a8ef608ba9e67276`, the four-case
`primary_realistic.json` corpus, `gpt-6-luna` at low reasoning effort, and the
explicit bundled Codex runtime `codex-cli 0.155.0-alpha.16.3` (SHA-256
`c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`).
The Go CLI binary SHA-256 was
`53bebd9623685a240a056255aec0a4bd8885fb39fe2f3ffd18b80cff7c8198fd`;
the adapter SHA-256 was
`168d725ad20225f93f1516c811289f5732f0a3647c9aac1a3d2000e6d8cfd17f`.

The first control trial completed with 14,985 input and 31 output tokens.
The registered 12,000 observed-token cap was therefore exceeded by that one
call; the runner stopped before the other seven trials. The 8-call and
360-second ceilings were respected. No price was available, so cost is
unknown. The post-call token stop condition is a soft observed cap, as the
preregistered plan and `PILOT.md` disclose.

The model answered `Insufficient evidence` to the first question, citing
`historical` and `current`. The expected answer was `0.2`, supported by the
`current` artifact. The row has zero shell/tool calls and no trace, so there
is no evidence that the model read either file. That response was graded
`wrong` under the frozen exact grader, but one control response with seven
missing trials is not a behavioral result or a control/treatment comparison.
No treatment trial or Go CLI invocation occurred.

`report.json` records `valid=false`: seven missing trials and token budget
exceeded. `plan.json` and `rows.jsonl` are the raw registered plan and single
row. There are no tool traces beyond the empty trace in that row. No remaining
authorized trials were attempted after the budget stop.

Exact command:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/primary_realistic.json \
  -plan /private/tmp/okf-primary-bundled-v2-plan.json \
  -rows /private/tmp/okf-primary-bundled-v2-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -go-cli /private/tmp/okf-eval-cli \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id primary-bundled-20260926-v2 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit d6bd02824b089a4e9c2e1080a8ef608ba9e67276 \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 8 -max-seconds 360 -max-tokens 12000 -unpriced \
  -toolkit-mode direct \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```
