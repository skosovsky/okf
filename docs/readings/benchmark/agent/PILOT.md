---
layout: default
lang: en
permalink: /readings/benchmark/agent/PILOT/
documentation_id: benchmark-agent-pilot
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
documentation_status: historical
historical: true
title: "Historical primary pilot protocol"
---
{% include nav.html %}

> Historical reading edition. Checked on 2026-10-04 against revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. The protocol and results below retain their original historical status; this is not a new run or authorization. [Original English document](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/PILOT.md).

<a id="historical-primary-pilot-protocol"></a>

# Historical primary pilot protocol
{: #section-1}

The prior eight-attempt run is preserved in [`runs/20260926-primary-live/` — original](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-live).
Every attempt failed before model inference because `PATH` selected standalone
`codex-cli 0.137.0`, which cannot decode the current model catalog's `max`
reasoning level. The subsequent run selected the bundled
`/Applications/ChatGPT.app/Contents/Resources/codex` explicitly. Its observed
version was `codex-cli 0.155.0-alpha.16.3` and its SHA-256 was
`c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`.
The runner and adapter reject a different version or binary. That run stopped
after one control trial exceeded its observed-token budget; its plan, row and
report are in [`runs/20260926-primary-bundled-v2/` — original](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-bundled-v2). The later one-case file-read
diagnostic stopped after control on a shell-wrapper parser mismatch; see
[`runs/20260926-diagnostic-read-v1/` — original](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-diagnostic-read-v1). The next diagnostic proposal is in
[`PILOT-DIAGNOSTIC.md`]({{ '/readings/benchmark/agent/PILOT-DIAGNOSTIC/' | relative_url }}). None of these partial runs supports an arm comparison.

For any new execution, the corrected 004/005 code, SPEC, corpus and adapter must be
in one clean commit. `-commit` must equal `git rev-parse HEAD`. `-spec` must be
the full upstream commit in [`skills/open-knowledge-format/SKILL.md`]({{ '/readings/skills/open-knowledge-format/SKILL/' | relative_url }}); the
preflight checks that declaration against the actual SPEC SHA-256. Build both
Go binaries from that clean checkout:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go build -o /private/tmp/okf-eval-cli ./cmd/okf
GOCACHE=/private/tmp/okf-agent-gocache go build -o /private/tmp/okf-eval-codex-adapter ./benchmark/agent/cmd/codex-adapter
```

The executed run ID was `primary-bundled-20260926-v2`. The protocol below
fixes the model ID, low reasoning effort, eight calls,
360 seconds total, 45 seconds per call, and 12,000 observed input/output
tokens. The token threshold is checked after each call, so one call can exceed
it; it is an observed stop condition, not a strict spend limit. Likewise, a
priced adapter's monetary threshold can be exceeded by one call. `-unpriced`
states that the account does not provide a per-call price;
it does **not** claim zero resource use. The runner records binary SHA-256s,
corpus/prompt hashes, clock, tool-access mode, results, failures and usage.

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

The command above is a historical record, not permission to rerun an exhausted
trial. The runner hashed both Go binaries during preflight. The SPEC revision
is the repository's pinned instant profile. Analyze the immutable
plan and raw rows:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/primary_realistic.json \
  -plan benchmark/agent/runs/20260926-primary-bundled-v2/plan.json \
  -rows benchmark/agent/runs/20260926-primary-bundled-v2/rows.jsonl
```

`valid` requires all eight trials, no operational failures, pinned metadata,
budget compliance, and a model-observed Go CLI call in each treatment trial.
The adapter counts a CLI call only from a completed command event with exit
code zero and an exact direct invocation of the pinned Go binary: JSON
validation of the materialized bundle or JSON parsing of a supplied concept.
Help, version, unrelated paths and any event format change fail closed. The
evaluator must inspect the raw tool traces and factual answers before
publishing the report. With four cases, the result is descriptive; it cannot
support a universal effect size.

Follow-up runs use separate plans and rows: [`corpus/metadata_cases.json` — original](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/metadata_cases.json) with
`-toolkit-mode none` (8 trials), [`corpus/backfill_cases.json` — original](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/backfill_cases.json) with direct mode
(6 trials), and the 013 checker-on/off writer→consumer pair. A before/after
004/005 comparison additionally needs a clean old-commit worktree and its own
SPEC lock; tool failures in that baseline are counted as operational failures.
The Codex adapter requires the same explicit `-model-runtime`, version and
SHA-256 pins in every toolkit mode, including `none`; its BYOT runner permits
other adapters without a local runtime in non-direct modes. No follow-up run
is implied by success of the first pilot.
