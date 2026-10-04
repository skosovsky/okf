---
layout: default
title: "Task 013: frozen live writer → consumer rerun"
lang: en
permalink: /readings/benchmark/agent/upkeeplive/LIVE/
documentation_id: benchmark-agent-upkeeplive-live
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical: true
---
{% include nav.html %}

**Historical reading edition.** Source: [benchmark/agent/upkeeplive/LIVE.md](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/upkeeplive/LIVE.md), repository revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70` (2026-10-04). This page preserves the methods, dates, measurements and status recorded in that revision. Historical results describe those runs, not the current product or a recommendation to run agents today.

<a id="task-013-frozen-live-writer--consumer-rerun"></a>

# Task 013: frozen live writer → consumer rerun
{: #section-1}

This is the preregistration for `upkeep-live-20260926-v1`. It is **not a result**.
Run only after separate authorization for sending the case data to OpenAI Codex.
The source checkout must be the clean commit
`260a7cb701f08e21efd94e0b04db9b491f7e1db7`. Do not rebuild or edit any
of the pinned inputs between approval and execution. A changed input requires
a new run ID and preregistration.

<a id="frozen-inputs-and-provenance"></a>

## Frozen inputs and provenance
{: #section-2}

| Input | Path | SHA-256 |
| --- | --- | --- |
| Source corpus | `benchmark/agent/corpus/backfill_cases.json` | `4c23a1ce55f666c737dde8eea0baf44ba82d5e0365585b98a699a72a2a03f85f` |
| Two-case derived corpus | `benchmark/agent/corpus/upkeep_cases.json` | `60701847761a868ad0ab8de31b5d7884d023fa42e82d13851c5ec4942b2dfe3d` |
| Frozen plan | `/private/tmp/okf-upkeep-live-v1-plan.json` | `27cc232a283d0b9993108684a942421a1906fb30e3215c2e974f63388db41327` |
| Runner, with the Go checker linked in | `/private/tmp/okf-upkeep-live` | `a0565afc973e6c1ef3d0a14ca732069d9a09416b6503e0d7f595688143a44b45` |
| Codex adapter | `/private/tmp/okf-upkeep-codex-adapter` | `3470b13c8e8d31fee44e2de6241a1af917a75bc02b5cb0f4c9f87c8cdbc8e9ea` |
| Codex CLI | `/Applications/ChatGPT.app/Contents/Resources/codex` | `c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a` |

The CLI version is `codex-cli 0.155.0-alpha.16.3`. Both writer and consumer
use `gpt-6-luna` with `low` reasoning. The adapter's writer prompt SHA-256 is
`f755ee7995f9f39e4383fc751ef55fc7fca1edb000f5036ab33fb57b38fa139e`;
its consumer prompt SHA-256 is
`86a8d806fceaddea4b93cb28e09122115c4fe234e2410e0e20c1c4067e5e6f59`.
The plan pins these hashes, the two models, one repeat, two cases and the
runner hash as `checker_sha256`. Its `source_corpus_sha256` refers to
`backfill_cases.json`, not the derived `upkeep_cases.json`.

<a id="comparison-and-scoring"></a>

## Comparison and scoring
{: #section-3}

Each case starts from the same committed code and OKF bundle in each arm. The
cases are `upkeep-reversal` (mode A becomes B) and `upkeep-truncated` (complete
rollout evidence becomes a partial capture). The runner freezes arm order from
plan ID, case ID and repeat. The writer must return the exact target code and
a complete final bundle. Each arm uses a fresh temporary Git repository.

In **checker off**, the writer sees the code task and initial bundle, then a
fresh consumer sees only that writer's final bundle and question. In **checker
on**, the writer also sees an OKF review reminder; after its first draft, the
real Go checker must return `needs_review`; a new model session sees that
result and gets one revision. A separate, fresh consumer then sees only the
final bundle and question. The consumer never sees the arm, source code,
checker feedback, writer transcript or gold answer. Every adapter invocation
starts a fresh ephemeral Codex session; duplicate session IDs fail validation.
The runner verifies exact target-code bytes and validates each final bundle
with our Go CLI using `instant-0b87c52` before calling its consumer.

The primary outcomes are documentation update rate and independent consumer
answer quality in each arm, on the same two cases, with all expected pairs in
the denominator. Also report concept updates, stale/wrong/refusal/ungradable
answers, operational failures, checker initial/final statuses, CLI validation,
token usage and missing usage. Apply the committed `upkeepstudy analyze`
grader without hand-editing rows. Report counts alongside percentages; with
only two cases, treat differences as descriptive. The checker-on intervention
includes a reminder, checker feedback **and** an extra revision opportunity,
so it cannot isolate the checker alone. `reviewed_updated` proves that concept
files changed; it does not prove their claims true.

<a id="authorized-execution-when-granted"></a>

## Authorized execution, when granted
{: #section-4}

Run from the repo root on the pinned clean commit. Verify each hash above,
`git status --porcelain`, the CLI version and the two printed prompt hashes
before making any model call. Preserve the plan in the run directory before
execution because `/private/tmp` is ephemeral. Create a **new** run directory;
the runner opens rows and audit files exclusively and must fail if they exist.
Use a new ID if any pin differs. The command below assumes approval for this
exact data transfer and budget; the documentation itself grants no approval.

```sh
export OKF_UPKEEP_CODEX_RUNTIME=/Applications/ChatGPT.app/Contents/Resources/codex
export OKF_UPKEEP_CODEX_VERSION=codex-cli\ 0.155.0-alpha.16.3
export OKF_UPKEEP_CODEX_SHA256=c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a
export OKF_UPKEEP_WRITER_MODEL=gpt-6-luna
export OKF_UPKEEP_CONSUMER_MODEL=gpt-6-luna
export OKF_UPKEEP_REASONING_EFFORT=low

mkdir benchmark/agent/runs/20260926-upkeep-live-v1
cp /private/tmp/okf-upkeep-live-v1-plan.json benchmark/agent/runs/20260926-upkeep-live-v1/plan.json

/private/tmp/okf-upkeep-live \
  -plan benchmark/agent/runs/20260926-upkeep-live-v1/plan.json \
  -rows benchmark/agent/runs/20260926-upkeep-live-v1/rows.json \
  -audit benchmark/agent/runs/20260926-upkeep-live-v1/audit.json \
  -adapter /private/tmp/okf-upkeep-codex-adapter \
  -max-calls 12 -max-seconds 600 -max-tokens 200000 -call-seconds 45

go run ./benchmark/agent/upkeepstudy analyze \
  -plan benchmark/agent/runs/20260926-upkeep-live-v1/plan.json \
  -rows benchmark/agent/runs/20260926-upkeep-live-v1/rows.json \
  > benchmark/agent/runs/20260926-upkeep-live-v1/report.json
```

The design needs ten calls if all stages succeed: two cases × (off writer +
consumer, on writer + revision + consumer). `12` is a hard maximum; it does not
authorize extra repeats or new cases. Each call has a 45-second outer timeout,
the whole run has a 600-second timeout, and the adapter has an internal
38-second deadline. The 200,000-token stop is checked **after** each call and
can be exceeded by that call. Usage is counted from Codex events. If a call
fails before usage is known, preserve its failure and stop; do not treat it as
zero tokens. No USD price or dollar cap is available. Codex receives case
source snippets, initial and revised OKF documents, and checker feedback in a
read-only temporary working directory; its service state may live outside the
workspace. The writer and consumer responses and usage become local rows and
audit artifacts.

Before interpreting the report, require `audit.json.complete` and verify its
`rows_sha256` and `audit_sha256` against the saved files; check audit plan,
runner and adapter hashes, `calls`, `tokens`, `unknown_usage_calls`, `stopped`,
per-stage events and unique session IDs. A missing completion marker or
`valid=false` must be reported as such, with operational failures and missing
pairs retained in the denominator. Preserve raw rows, audit, marker and report
even if the experiment fails; do not silently rerun or substitute offline
fake-adapter tests for live evidence. Two independent subagents should review
the final result for completeness and correctness before task 013 acceptance.
