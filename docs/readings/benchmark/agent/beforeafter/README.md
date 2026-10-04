---
layout: default
title: "004/005 Go CLI before/after runner"
lang: en
permalink: /readings/benchmark/agent/beforeafter/README/
documentation_id: benchmark-agent-beforeafter-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical: true
---
{% include nav.html %}

**Historical reading edition.** Source: [benchmark/agent/beforeafter/README.md](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/beforeafter/README.md), repository revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70` (2026-10-04). This page preserves the methods, dates, measurements and status recorded in that revision. Historical results describe those runs, not the current product or a recommendation to run agents today.

<a id="004005-go-cli-beforeafter-runner"></a>

# 004/005 Go CLI before/after runner
{: #section-1}

This is a separate agent experiment from the main repository-vs-OKF pilot. No
model calls run in `go test ./...`. The command is
`go run ./benchmark/agent/beforeafter/cmd/okf-before-after run`; it refuses a
dirty corrected checkout and writes its plan **before** the first model call.
`analyze` recomputes denominator accounting from that plan and the saved rows.
The current corpus has four date-spec 005 questions and one instant-spec 004
capability question. Two repeats mean **20 expected rows, 18 possible model
calls**: each 004 old row is `revision_unsupported` and is never sent to the
model. The 005 comparisons therefore contain eight paired old/new answer rows.

The five cases use the same source bytes and question in both arms. The
`foreign-68ce7a0` fixture covers a wrapped log item, root title/intro, root
`upkeep: enforced`, and an inline footnote marker. An independent source quote
anchors every gold answer. The corrected arm uses the explicit
`date-3fcbb9f` selector; the historical binary has no temporal-profile flag
and uses its default date contract. The instant case uses
`instant-0b87c52` only in the new arm. Its old outcome is a **new revision
capability gap**, not a date-spec defect or a factual model error.

Both CLI binaries are external to the Go harness. Build the old binary from
`ed7ddc28cd127682023bd150ee377906b817e099` with `git archive` and the new
binary from a clean corrected commit, then record both complete commit IDs and
binary SHA-256 values. Also pin the adapter and model-runtime SHA-256 values.
The run command rejects any hash mismatch. The SHA-to-Git-build relationship
still depends on the documented build procedure; the runner does not derive a
source commit from opaque binary bytes. The model-runtime SHA/version pin a
local executable, while a backend model snapshot cannot be independently
verified by this adapter. The full `go version`, model ID,
runtime version, settings, source corpus/fixture digests, prompt digest, fixed
date and instant clocks, arm order and budgets are saved in `plan.json`. The
adapter starts a fresh model process for every trial. Arm order alternates by
case and repetition; source text is evidence, never harness policy.

For every row the runner copies committed fixture files into an isolated
temporary bundle, calls `validate --json` and `parse --json` through the selected
binary, records exit status, command/stdout/stderr hashes and duration, and
passes the original sources plus CLI JSON projections to the model. It
normalizes temporary paths in model-visible projections. A **well-formed
validation JSON report at exit 1 is retained**. The pinned old 005 fixture
has 12 such diagnostics: one root intro, one unknown root field and ten wrapped
log-item findings. Each is classified against the unchanged date SPEC; a new
or unclassified diagnostic stops that trial before model inference. Unexpected
process failures, non-JSON output, and corrected validation errors are
`operational_failure`, separate from factual answers. The model must cite the
original source artifact ID; CLI diagnostics alone cannot earn a correct
verdict. Answers are graded by the existing exact-answer Go grader.

Before any paid run, explicitly approve the destination, payload, model,
maximum **18** calls, time/token/cost bounds, and any state outside the
workspace. This runner uses a hard call and wall-clock cap; observed tokens or
USD stop after each call and may be exceeded by one call. Use `-unpriced` only
when per-call cost is unavailable. Do not treat the `analyze` result as a
measured effect until every planned row exists and a real adapter has run.

Offline preflight, after building both binaries:

```sh
OKF_BAF_OLD_BIN=/absolute/old-okf \
OKF_BAF_NEW_BIN=/absolute/new-okf \
go test ./benchmark/agent/beforeafter -run TestPinnedCLIIntegration -v
```

`run` requires `-old-bin`, `-new-bin`, `-old-bin-sha256`,
`-new-bin-sha256`, `-adapter`, `-adapter-sha256`, `-model-runtime`,
`-model-runtime-version`, `-model-runtime-sha256`, `-new-commit`, `-run-id`,
`-model`, `-model-version`, `-settings`, `-plan`, `-rows`, and bounded run
flags. Its `-max-model-calls` must be at least 18 for the default corpus
with two repeats. No CLI argument implicitly authorizes a model call.
