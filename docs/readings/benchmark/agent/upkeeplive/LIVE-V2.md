---
layout: default
title: "Task 013: preregistered checker on/off writer → consumer rerun (v2)"
lang: en
permalink: /readings/benchmark/agent/upkeeplive/LIVE-V2/
documentation_id: benchmark-agent-upkeeplive-live-v2
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical: true
---
{% include nav.html %}

**Historical reading edition.** Source: [benchmark/agent/upkeeplive/LIVE-V2.md](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/upkeeplive/LIVE-V2.md), repository revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70` (2026-10-04). This page preserves the methods, dates, measurements and status recorded in that revision. Historical results describe those runs, not the current product or a recommendation to run agents today.

<a id="task-013-preregistered-checker-onoff-writer--consumer-rerun-v2"></a>

# Task 013: preregistered checker on/off writer → consumer rerun (v2)
{: #section-1}

This freezes `upkeep-live-20260926-v2` **before any v2 model call**. It is a
new run on the same two synthetic cases, not a reinterpretation of v1. The v1
model run completed, but its registered analyzer rejected a prose citation in
the `upkeep-truncated` checker-on answer; its post-hoc sensitivity report does
not make v1 a valid primary result. V2 uses the committed grader on clean
source commit `dd8989ba36b8e27ac5762e2e0984cac7f88e8738`. It treats a
citation absent from the final writer bundle's artifact IDs as **ungradable**
(unless the answer is a refusal or operational failure). Exact answers,
accepted aliases, stale answers, case data, and prompt text are unchanged;
there is no post-hoc alias tuning. Neither v1 nor v2 can establish a product
benefit from two cases.

Run only after **fresh, explicit authorization** for the external transfer and
budget below. No earlier permission for v1 covers v2. The checkout, frozen
plan, runner, adapter, corpus, runtime, and prompt hashes must match before
calling a model. Any changed pin requires a new plan, run ID, and permission.

<a id="frozen-inputs"></a>

## Frozen inputs
{: #section-2}

| Input | Path | SHA-256 |
| --- | --- | --- |
| Clean source commit | repository | `dd8989ba36b8e27ac5762e2e0984cac7f88e8738` |
| Source corpus | `benchmark/agent/corpus/backfill_cases.json` | `4c23a1ce55f666c737dde8eea0baf44ba82d5e0365585b98a699a72a2a03f85f` |
| Two-case derived corpus | `benchmark/agent/corpus/upkeep_cases.json` | `60701847761a868ad0ab8de31b5d7884d023fa42e82d13851c5ec4942b2dfe3d` |
| Frozen plan | `/private/tmp/okf-upkeep-live-v2-plan.json` | `98f16ab46d4241bf6a8cfb8dcd7f3ff45189021dee362d5c0fc65073b38adc7f` |
| Runner with linked Go checker | `/private/tmp/okf-upkeep-live-v2` | `22447dcbe34ca77b08c61b3667adcac331bc6b00689fa32994f3df8dea452eed` |
| Codex adapter | `/private/tmp/okf-upkeep-codex-adapter-v2` | `1cfff326d7c74c7e14687b30ad3c55ce3707443a3a88c11ce5a016ed37ccaead` |
| Codex CLI | `/Applications/ChatGPT.app/Contents/Resources/codex` | `c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a` |

The CLI version is `codex-cli 0.155.0-alpha.16.3`. Writer and independent
consumer use `gpt-6-luna` with `low` reasoning, one repeat. The writer prompt
SHA-256 remains
`f755ee7995f9f39e4383fc751ef55fc7fca1edb000f5036ab33fb57b38fa139e`;
the consumer prompt SHA-256 remains
`86a8d806fceaddea4b93cb28e09122115c4fe234e2410e0e20c1c4067e5e6f59`.
The plan pins both hashes and models, two cases, one repeat, and the runner
hash as `checker_sha256`. Its `source_corpus_sha256` is the source backfill
corpus hash; the derived corpus has a separate hash above.

<a id="protocol-and-outcomes"></a>

## Protocol and outcomes
{: #section-3}

Both arms start from identical committed code and OKF documents in fresh
temporary Git repositories. `upkeep-reversal` changes mode A to B;
`upkeep-truncated` replaces complete rollout evidence with a partial capture.
The writer must apply the target code exactly and return the complete final
bundle. In checker off, a fresh consumer reads only that bundle and the
question. In checker on, the writer sees an OKF review reminder, receives the
actual Go checker result after its first draft, and gets one further model
revision. A separate fresh consumer reads only the final bundle and question.
The consumer does not see source code, arm, checker feedback, writer transcript,
or gold answer. The runner checks exact target-code bytes and validates the
final bundle with the Go CLI under `instant-0b87c52` before calling consumer.

The primary outcomes are document update rate and independent consumer answer
quality in each arm. Count all two expected pairs per arm in the denominator.
The committed `upkeepstudy analyze` grader requires cited evidence strings to
be artifact IDs present in that arm's final writer bundle. Unsupported IDs
make non-refusal/non-operational answers ungradable even when their answer text
matches the expected answer. Do not repair or reinterpret responses after the
run. Report correct, stale, wrong, refusal, ungradable, unsupported citations,
operational failures, concept changes, checker initial/final statuses, CLI
validation, tokens, and missing usage with counts as well as rates. A
`reviewed_updated` checker status only means a concept file changed. The
checker-on intervention combines reminder, checker feedback, and another
revision, so any descriptive difference cannot isolate the checker itself.

<a id="execution-after-authorization"></a>

## Execution after authorization
{: #section-4}

Run from a clean checkout at the pinned source commit. The output directory is
under `/private/tmp` so the pinned checkout stays clean for the other studies.
The run opens its rows and audit paths exclusively. This command presumes
authorization; this document does not grant it.

```sh
set -e
set -o pipefail
test -z "$(git status --porcelain --untracked-files=all)"
test "$(git rev-parse HEAD)" = dd8989ba36b8e27ac5762e2e0984cac7f88e8738
check_sha() { test "$(shasum -a 256 "$1" | cut -d ' ' -f 1)" = "$2"; }
check_sha benchmark/agent/corpus/backfill_cases.json 4c23a1ce55f666c737dde8eea0baf44ba82d5e0365585b98a699a72a2a03f85f
check_sha benchmark/agent/corpus/upkeep_cases.json 60701847761a868ad0ab8de31b5d7884d023fa42e82d13851c5ec4942b2dfe3d
check_sha /private/tmp/okf-upkeep-live-v2-plan.json 98f16ab46d4241bf6a8cfb8dcd7f3ff45189021dee362d5c0fc65073b38adc7f
check_sha /private/tmp/okf-upkeep-live-v2 22447dcbe34ca77b08c61b3667adcac331bc6b00689fa32994f3df8dea452eed
check_sha /private/tmp/okf-upkeep-codex-adapter-v2 1cfff326d7c74c7e14687b30ad3c55ce3707443a3a88c11ce5a016ed37ccaead
check_sha /Applications/ChatGPT.app/Contents/Resources/codex c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a
test "$(/Applications/ChatGPT.app/Contents/Resources/codex --version)" = 'codex-cli 0.155.0-alpha.16.3'
test "$(/private/tmp/okf-upkeep-codex-adapter-v2 --print-prompt-sha256)" = "$(printf 'writer=f755ee7995f9f39e4383fc751ef55fc7fca1edb000f5036ab33fb57b38fa139e\nconsumer=86a8d806fceaddea4b93cb28e09122115c4fe234e2410e0e20c1c4067e5e6f59')"
test ! -e /private/tmp/okf-upkeep-live-v2-output

export OKF_UPKEEP_CODEX_RUNTIME=/Applications/ChatGPT.app/Contents/Resources/codex
export OKF_UPKEEP_CODEX_VERSION=codex-cli\ 0.155.0-alpha.16.3
export OKF_UPKEEP_CODEX_SHA256=c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a
export OKF_UPKEEP_WRITER_MODEL=gpt-6-luna
export OKF_UPKEEP_CONSUMER_MODEL=gpt-6-luna
export OKF_UPKEEP_REASONING_EFFORT=low

mkdir /private/tmp/okf-upkeep-live-v2-output
cp /private/tmp/okf-upkeep-live-v2-plan.json /private/tmp/okf-upkeep-live-v2-output/plan.json

/private/tmp/okf-upkeep-live-v2 \
  -plan /private/tmp/okf-upkeep-live-v2-output/plan.json \
  -rows /private/tmp/okf-upkeep-live-v2-output/rows.json \
  -audit /private/tmp/okf-upkeep-live-v2-output/audit.json \
  -adapter /private/tmp/okf-upkeep-codex-adapter-v2 \
  -max-calls 10 -max-seconds 600 -max-tokens 200000 -call-seconds 45

GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/upkeepstudy analyze \
  -plan /private/tmp/okf-upkeep-live-v2-output/plan.json \
  -rows /private/tmp/okf-upkeep-live-v2-output/rows.json \
  > /private/tmp/okf-upkeep-live-v2-output/report.json
```

The design has ten planned calls: for each case, checker-off writer and
consumer plus checker-on first writer, revision, and consumer. Ten is the
hard maximum; it permits no extra cases, repeats, or retry. The whole run is
capped at 600 seconds, each call at 45 seconds (adapter internal deadline 38
seconds). The 200,000 input-plus-output-token stop is checked after each
call, so one call can exceed it. USD cost and a dollar cap are unavailable.
The external OpenAI Codex service receives synthetic case source snippets,
initial and revised OKF documents, checker feedback, and consumer questions.
Each call uses a read-only temporary shell; Codex service state may be outside
the workspace. Responses, usage, and stage audits are saved locally.

Keep the raw `rows.json`, `audit.json`, `audit.json.complete`, plan and report,
including failures. Verify the marker's `rows_sha256` and `audit_sha256`
against the saved files; inspect audit plan, runner and adapter hashes, call
count, observed tokens, unknown usage, stop reason, per-stage events and
distinct session IDs. If the marker is absent, scoring fails, or the report is
`valid=false`, describe the run as invalid, with missing pairs in the
denominator. Do not silently rerun. Have two independent subagents review
completeness and correctness before accepting task 013.
