# Task 012: manually prepared writer → independent consumer baseline

This is a frozen, two-call semantic pilot, **not a result**. The earlier v1/v2
protocol drafts were never run. The human writer
prepared the final OKF bundle in `corpus/writer_consumer_baseline.json`; it does
not use the task-010 backfill writer or task-013 upkeep checker. The consumer
will receive that bundle in a fresh model invocation. The raw-record control
and bundle treatment both say that the old queue mode A was superseded by B.
The control preserves the same answer and evidence ID, so a treatment win
cannot be attributed to withholding the right answer from control.

The corpus SHA-256 is
`3fd56d975fa6570df6db81a939eea72d51178034a559882a52b1bb753baf522a`.
The single case is synthetic (`mechanism_only`). The shortest correct answer is
`B`, citing artifact `decision`; `A` is stale. The exact-v1 grader treats a
correct value without that citation as ungradable. The CLI validation test is
a publication precondition, not evidence that a consumer answered correctly.
The writer is human and fixed: this run measures the independent consumer,
not AI writing quality or the effect of 010/013.

## Frozen protocol

Use the clean Go checkout at
`dd8989ba36b8e27ac5762e2e0984cac7f88e8738` and record that full commit
and pinned SPEC revision `0b87c52c6ef999286c745e19998fdfcd03d5dbee`
in the plan.
Run the included offline Go test before any model call. Use the bundled
`codex-cli 0.155.0-alpha.16.3` binary with SHA-256
`c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`,
model ID `gpt-6-luna` and `low` reasoning. Its backend snapshot is not
independently verifiable. The baseline adapter at
`/private/tmp/okf-eval-codex-adapter-dd8989b` has SHA-256
`8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9`.
It is built with `-buildvcs=false` from this code; the exact source commit is
still recorded separately by the runner.
If this binary is unavailable or differs, freeze and preregister a new adapter
hash and run ID before calling the model. The adapter makes one fresh,
ephemeral, read-only Codex invocation per arm; the consumer receives only the
projected request (`question`, `artifacts`, common answer instructions), never
the harness `case_id` or `arm`, gold `expected` object, writer history or other
arm. The harness runs control
then treatment. This one-repeat pilot cannot estimate order or variance.

Run ID: `manual-writer-consumer-20260926-v3`. Expected rows and model calls:
**one case × two arms × one repeat = two**. Hard bounds: two calls, 120 seconds
of runner context, 40 seconds per adapter model call. Observed input+output
token threshold: 50,000, checked after each call and therefore able to be
exceeded by the final call. USD cost is unavailable; `-unpriced` is not a
dollar cap. Source material transferred to OpenAI Codex is the two raw record
excerpts and the three manually authored OKF Markdown files. Obtain separate,
specific user authorization before that transfer and run. No model call is
part of ordinary Go tests or this protocol document.

From the clean repository root, use exclusive `/private/tmp` output paths:

```sh
set -e
set -o pipefail
test -z "$(git status --porcelain --untracked-files=all)"
test "$(git rev-parse HEAD)" = dd8989ba36b8e27ac5762e2e0984cac7f88e8738
test "$(shasum -a 256 benchmark/agent/corpus/writer_consumer_baseline.json | cut -d ' ' -f 1)" = \
  3fd56d975fa6570df6db81a939eea72d51178034a559882a52b1bb753baf522a
test "$(/Applications/ChatGPT.app/Contents/Resources/codex --version 2>/dev/null)" = \
  'codex-cli 0.155.0-alpha.16.3'
test "$(shasum -a 256 /Applications/ChatGPT.app/Contents/Resources/codex | cut -d ' ' -f 1)" = \
  c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
  go build -buildvcs=false -o /private/tmp/okf-eval-codex-adapter-dd8989b \
  ./benchmark/agent/cmd/codex-adapter
test "$(shasum -a 256 /private/tmp/okf-eval-codex-adapter-dd8989b | cut -d ' ' -f 1)" = \
  8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9

GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
  go test ./benchmark/agent -run '^TestManualWriterConsumerBaseline$' -count=1

OKF_BASELINE_COMMIT=$(git rev-parse HEAD)
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
  go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/writer_consumer_baseline.json \
  -plan /private/tmp/okf-manual-writer-consumer-v3-plan.json \
  -rows /private/tmp/okf-manual-writer-consumer-v3-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter-dd8989b \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id manual-writer-consumer-20260926-v3 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit "$OKF_BASELINE_COMMIT" \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 2 -max-seconds 120 -max-tokens 50000 -unpriced \
  -toolkit-mode none \
  -model-tool-access 'Codex read-only ephemeral temp; projected question/artifacts/instructions JSON; no CLI projections'

GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
  go run ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/writer_consumer_baseline.json \
  -plan /private/tmp/okf-manual-writer-consumer-v3-plan.json \
  -rows /private/tmp/okf-manual-writer-consumer-v3-rows.jsonl \
  > /private/tmp/okf-manual-writer-consumer-v3-report.json
```

Preserve the exact plan, raw rows and analyzer report under `runs/` after the
authorized call. Show both rows in denominator accounting even on failure;
report correctness, stale, ungradable, refusal and operational failure
separately, with token usage and unknown price. `valid=true` means analyzable
rows, not a demonstrated product benefit. Any changed corpus, grader,
adapter or protocol requires a new run ID before observing answers.
