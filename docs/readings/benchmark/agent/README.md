---
layout: default
lang: en
permalink: /readings/benchmark/agent/README/
documentation_id: benchmark-agent-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
documentation_status: historical
historical: true
title: "Agent behavior benchmark"
---
{% include nav.html %}

> Historical reading edition. Checked on 2026-10-04 against revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. The protocol and results below retain their original historical status; this is not a new run or authorization. [Original English document](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/README.md).

<a id="agent-behavior-benchmark"></a>

# Agent behavior benchmark
{: #section-1}

This Go package contains the offline corpus, wire contract, exact grader, and
denominator analyzer for task 012. The 20 committed cases cover changed defaults
and limits, reversals, stale knowledge, instruction injection, unsupported
verification, intent versus execution, truncated evidence, and missing evidence.
Sixteen are marked `mechanism_only`: the artifacts are short synthetic excerpts.
Four cases are pinned to actual Go source excerpts in this repository. This is not a
measured sample of real repositories. Each treatment arm is a valid
OKF v0.2 bundle; Go tests validate every bundle through our CLI.
[`corpus/metadata_cases.json` — original](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/metadata_cases.json) is a separate four-case contrast: both arms carry
identical Markdown bodies and artifact names; treatment adds OKF frontmatter.
Run it with `-toolkit-mode none` so neither arm receives CLI projections. Do
not pool its denominator with the main corpus.

[`schema.json` — original](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/schema.json) defines case, request, observation, row, and report JSON shapes.
[`corpus/cases.json` — original](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/cases.json) is immutable during one comparison. `LoadCorpus` records its
SHA-256. The runner passes one question and its arm's artifacts to an adapter;
`metadata.prompt_sha256` hashes only the shared `Request.Instructions`, not the
full model-visible Codex prompt. New plans/reports say this explicitly in
`prompt_hash_scope`. The corpus hash fixes question/artifacts and the adapter
binary hash fixes prompt-construction code, but Codex's injected context is not
independently hashed by this harness.
Each adapter invocation must use a fresh conversation with identical model
settings and system/project instructions across arms. Tool access must be fixed
and recorded before the run. Artifact text is untrusted evidence, even when it
contains instructions. An adapter reads a `Request` JSON object from stdin and
writes one `Observation` JSON object to stdout. The Go `Runner` interface allows
equivalent in-process adapters. The adapter must report real usage and failures;
unknown usage remains zero and must be identified in the published run notes.

`go test ./benchmark/agent/...` runs offline. [PILOT.md]({{ '/readings/benchmark/agent/PILOT/' | relative_url }}) records the
historical primary commands; [PILOT-DIAGNOSTIC.md]({{ '/readings/benchmark/agent/PILOT-DIAGNOSTIC/' | relative_url }}) defines
the one-case read-proof diagnostic. [PILOT-PRECOMPUTED.md]({{ '/readings/benchmark/agent/PILOT-PRECOMPUTED/' | relative_url }})
records the paired protocol using runner-produced Go CLI projections. To
analyze saved rows:

```sh
go run ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/cases.json \
  -plan /path/to/plan.json -rows /path/to/rows.jsonl
```

The live command has an explicit trial cap, wall-clock cap, 45-second per-trial
timeout, and exclusive output creation. It writes and syncs every raw JSONL row,
including operational failures. It never runs from `go test`:

```sh
go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/cases.json \
  -plan /path/to/new-plan.json -rows /path/to/new-rows.jsonl \
  -adapter /absolute/path/to/model-adapter -model MODEL -model-version VERSION \
  -settings '{"reasoning_effort":"low"}' -commit COMMIT -spec SPEC_REVISION \
  -repeats 1 -max-trials 40 -max-seconds 600 -max-tokens 50000 -unpriced \
  -toolkit-mode treatment -model-tool-access 'BYOT adapter with fixed tool policy'
```

The command writes an exclusive, synced plan file before the first model call.
Analysis checks every row against that file. The exact-v1 grader accepts only
the preregistered answer or aliases and a cited original evidence artifact ID.
A `cli-current` projection derives from `current`, so the shared instruction
tells both arms to cite `current`. A correct answer citing only `cli-current`
remains ungradable under `exact-v1`; the grader does not silently translate
IDs. It separates stale, wrong, refusal, ungradable, and
operational failure. The analyzer rejects duplicate, unknown, mismatched, or
tampered rows; missing rows and operational failures remain visible against the
full `cases × arms × repeats` denominator. Malformed JSONL lines are counted
as `invalid_raw_rows`; the expected trial they fail to identify remains missing.
Factual error and stale rates use
`correct + stale + wrong` as denominator; completion uses all expected trials.
`valid` describes run completeness,
not whether the agent answered correctly. A small pilot is descriptive only.
New plans set `tool_call_accounting=separate-v1`: `tool_calls` counts model
tool events, `model_toolkit_calls` counts model-initiated Go CLI events, and
`runner_toolkit_calls` counts Go CLI calls made by the runner before the model.
Legacy plans without this field retain their historical mixed `tool_calls`
accounting and are not relabeled after the fact.
Adapters should return an `adapter_error` observation even after a failed paid
call, preserving any token usage extracted before failure. The Codex adapter
does this for nonzero `codex exec` exits. Caps on tokens and priced calls are
checked after each response, so a single call can overshoot them; the call
count, per-trial timeout, and total wall-clock timeout are the hard bounds.

Before a live run, pin the corrected Go commit and SPEC revision, register the
model/version/settings, adapter, tools, prompts, corpus hash, clock, repeat count,
budget and invalidation rules. The primary comparison must use real repository
docs/code as control and a valid bundle through the Go CLI/MCP as treatment;
the correct answer must be available to both arms. The separate metadata-only,
before/after 004/005, backfill-to-consumer, and upkeep writer-to-consumer
protocols are linked with their results below. The metadata contrast uses
`-corpus benchmark/agent/corpus/metadata_cases.json`. A writer-to-consumer
scenario must grade an independent consumer's factual response after the writer
has produced a valid bundle; backfill coverage or syntax checks alone cannot
stand in for this result.

The [precomputed primary pilot]({{ '/readings/benchmark/agent/runs/20260926-primary-precomputed-v1/README/' | relative_url }})
completed 8/8 planned trials (four pairs) on corrected commit `ed0bd12`:
`valid=true`,
3/4 correct in each arm, one different wrong answer in each, nine
runner-side Go CLI calls, zero model tool calls, and 127,346 observed
input-plus-output tokens. The adapter did not provide a dollar cost. With
four cases and one repeat, this shows feasibility of the Go CLI projection
path and no measured answer-quality benefit; it does not test autonomous CLI
use. Its plan, raw rows, and analyzer report are committed together.

The completed follow-up runs each retain a plan, raw observations, report, and
their own protocol. Scores below use a **frozen strict grader**: an answer must
match the registered answer or alias and cite a supported artifact ID. Read the
case-level notes before treating an exact score as factual correctness.

| Run | Valid result | Observed outcome |
| --- | --- | --- |
| [Metadata-only v2]({{ '/readings/benchmark/agent/runs/20260926-metadata-only-v2/README/' | relative_url }}) | 8/8 calls, `valid=true` | Control 4/4, treatment 4/4 correct. Adding frontmatter to identical answer-bearing bodies made no measured difference here. |
| [Manual bundle → consumer v3]({{ '/readings/benchmark/agent/runs/20260926-manual-writer-consumer-v3/README/' | relative_url }}) | 2/2 calls, `valid=true` | Raw-record and human-prepared OKF consumers both 1/1 correct; this does not test an AI writer. |
| [Go backfill → consumer v2]({{ '/readings/benchmark/agent/runs/20260926-backfill-consumer-v2/README/' | relative_url }}) | 6/6 calls, `valid=true` | Control 3/3, treatment 2/3 by exact grading. The treatment's “Mode B” versus expected “B” is a format miss; its meaning and citation match the evidence. |
| [Go CLI before/after 004/005 v1]({{ '/readings/benchmark/agent/runs/20260926-beforeafter-v1/README/' | relative_url }}) | 20/20 rows, `valid=true`; 18 model calls | In eight paired 005 comparisons, old 4/8 and corrected 6/8 pass exact grading; one gain reflects answer normalization rather than a factual correction. In the separate 004 case, the old CLI cannot select the instant-spec revision; the corrected CLI validates it and both model answers pass. |
| [Checker off/on writer → consumer v2]({{ '/readings/benchmark/agent/runs/20260926-upkeep-live-v2/README/' | relative_url }}) | 10 calls, `valid=true` | Strict consumer scores are off 0/2 and on 1/2. In the reversal case, checker-on left a stale Mode A document and consumer answer; checker-off wrote Mode B, but “Mode B” missed the exact `B` answer. The checker intervention includes a reminder and another revision, so this is not an isolated checker effect. |

The earlier [checker live v1]({{ '/readings/benchmark/agent/runs/20260926-upkeep-live-v1/README/' | relative_url }})
completed model calls but failed its preregistered analyzer contract; its
post-hoc report is diagnostic only, and v1 has no valid primary score. All
these studies are small synthetic or targeted comparisons. None establishes a
universal OKF quality benefit or a factual-error rate for real repositories.
The adapters supplied no USD prices; a zero-valued report cost with zero
cost-known trials means **unknown**, not free.

The earlier authorized direct pilot attempts and diagnostics are preserved under
[`runs/` — original](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs). The first eight attempts failed before inference because standalone
`codex-cli 0.137.0` could not decode the model catalog. The pinned bundled CLI
then produced one control answer before exceeding the registered 12,000-token
stop condition. The first file-read diagnostic saw successful zsh commands but
could not prove their output under its old parser; the second saw no shell
events and stopped after control. Those direct-mode reports are all
`valid=false` and support no control/treatment quality comparison.

The included
`codex-adapter` can run a bounded mechanism smoke test in an ephemeral,
read-only directory. Its model version field is an explicit pinned model ID;
the backend snapshot is not independently verifiable. In `treatment` and
`both` modes, the runner invokes the Go CLI implementation in process
(`internal/okfcli.Run`) for `validate` and `parse` on each enabled bundle,
adds their outputs to the model evidence, and saves those
outputs in each row. For direct tool use, run with
`-toolkit-mode direct -go-cli /absolute/path/to/okf`; the model must invoke the
CLI itself. The adapter
writes both arms' files into isolated temporary directories and gives the model
read-only shell access. In direct mode, treatment must invoke the Go CLI and
the analyzer rejects a successful treatment row with no observed Go CLI call.
For direct runs, the runner and adapter require an absolute model runtime path,
exact version and
SHA-256, recorded in the plan and report. The previous auto-review rejection
is preserved separately from the later authorized failed run.

[`cmd/prepare-backfill` — original](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/cmd/prepare-backfill) applies the frozen 010 reversal fixture through the Go
store and writes [`corpus/backfill_cases.json` — original](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/backfill_cases.json) plus a separate coverage report.
The three consumer questions cover A→B, truncated evidence and current code.
The current-code question gives both arms the same frozen `git/current.diff`
artifact so a wrong answer is not caused by missing source evidence. The live
consumer run used `-toolkit-mode none`: the current-code treatment
also includes this raw diff, while Go CLI treatment projection accepts only
OKF files. The [completed consumer run]({{ '/readings/benchmark/agent/runs/20260926-backfill-consumer-v2/README/' | relative_url }})
reports the independent answers; the offline test separately verifies writer
publication, identical code evidence and Go CLI readability of the two
bundle-only cases.
