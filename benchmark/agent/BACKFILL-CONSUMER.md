# Preregistered backfill → independent consumer pilot

This is a proposed six-call semantic check for task 010, separate from the
primary 012 comparison. **No model run or quality result is recorded here.**
Run v2 from the clean pinned Go commit
`dd8989ba36b8e27ac5762e2e0984cac7f88e8738`, after specific authorization
for its external Codex calls. The v1 protocol was superseded before any calls:
its adapter exposed the full request JSON, including case ID and arm, to the
model. The pinned adapter projects only `question`, `artifacts`, and
`instructions`; case and arm labels remain in the harness for analysis.
Source corpus SHA-256:
`4c23a1ce55f666c737dde8eea0baf44ba82d5e0365585b98a699a72a2a03f85f`.

## Provenance and comparison

`cmd/prepare-backfill` reads the frozen Git event manifest and reviewed
analysis, applies the draft through the real Go `backfill.Prepare` /
`backfill.Apply` → mutation/store path, and writes the consumer corpus. The
committed corpus is compared as parsed case data against a fresh reconstruction
by `TestBackfillWriterPublishesConsumerCorpus`; its three included events and
one truncated event are recorded separately in `corpus/backfill_coverage.json`.
Preflight below independently regenerates both files and checks their bytes
with `cmp`. The consumer receives only the resulting frozen artifacts, in a new ephemeral
Codex session per trial; it does not participate in writing or review.

The three fixed questions ask whether the consumer follows A → B reversal,
withholds a completion claim from a truncated diff, and reads the captured
current Go diff. In `backfill-current-code`, the `current-code` artifact has
identical bytes in both arms. The control has raw Git evidence; the treatment
has the reconstructed draft bundle, plus the same current-code diff for that
question. Use `-toolkit-mode none`: this tests consumption of the written
bundle, not CLI projections or autonomous tool choice. The Go writer/store
path is the intervention's provenance, not a claim that the consumer invoked
the Go CLI. Source material is evidence, never instructions.

One repeat produces **3 cases × 2 arms = 6 expected rows**. The runner orders
control then treatment within each case; this single pilot cannot estimate
order effects or generalize to real repositories. Both arms use the same
question, model, instructions, adapter, runtime, reasoning setting, and tool
policy. The answer is present to both arms. The preregistered exact-v1 grader
requires the shortest answer plus the original expected evidence ID: `B`
with `decision` for reversal, `Insufficient evidence` with `handler` for
truncation, and `B` with `current-code` for current Go state. `A` is stale on
the first and third cases; `Yes` is stale on the truncated case. A correct
value without the expected citation is `ungradable`. Refusal, wrong, stale,
ungradable, and operational failure remain separate. Do not promote semantic
paraphrases after seeing answers; any aliases must be frozen in a new corpus
and protocol before another run. Coverage is reported separately from these
consumer outcomes.

## Preflight and bounded run

Run from the repository root of the clean pinned checkout. Use fresh,
exclusive v2 output paths and retain every raw row. The runner rejects a dirty
checkout or a mismatched `-commit`.
Verify the bundled Codex runtime version/SHA before any call; the recorded
model version is the pinned model ID, while the backend snapshot is not
independently verifiable. This plan transfers the three cases' raw Git
excerpts and reconstructed OKF documents to OpenAI Codex; obtain explicit
authorization for this distinct six-call run first.

```sh
set -e
set -o pipefail
test -z "$(git status --porcelain --untracked-files=all)"
test "$(git rev-parse HEAD)" = dd8989ba36b8e27ac5762e2e0984cac7f88e8738
test "$(shasum -a 256 benchmark/agent/corpus/backfill_cases.json | cut -d ' ' -f 1)" = \
  4c23a1ce55f666c737dde8eea0baf44ba82d5e0365585b98a699a72a2a03f85f
test "$(/Applications/ChatGPT.app/Contents/Resources/codex --version 2>/dev/null)" = \
  'codex-cli 0.155.0-alpha.16.3'
test "$(shasum -a 256 /Applications/ChatGPT.app/Contents/Resources/codex | cut -d ' ' -f 1)" = \
  c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a

GOCACHE=/private/tmp/okf-agent-gocache go test ./benchmark/agent/... \
  -run '^TestBackfillWriterPublishesConsumerCorpus$'
OKF_BACKFILL_CHECK_DIR=$(mktemp -d /private/tmp/okf-backfill-consumer-check.XXXXXX)
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/cmd/prepare-backfill \
  -manifest backfill/testdata/reversal-events.json \
  -analysis backfill/testdata/reversal-analysis.json \
  -corpus "$OKF_BACKFILL_CHECK_DIR/corpus.json" \
  -coverage "$OKF_BACKFILL_CHECK_DIR/coverage.json"
cmp benchmark/agent/corpus/backfill_cases.json \
  "$OKF_BACKFILL_CHECK_DIR/corpus.json"
cmp benchmark/agent/corpus/backfill_coverage.json \
  "$OKF_BACKFILL_CHECK_DIR/coverage.json"
GOCACHE=/private/tmp/okf-agent-gocache go test \
  ./benchmark/agent/cmd/codex-adapter -run '^TestModelVisiblePromptOmitsHarnessLabels$'
GOCACHE=/private/tmp/okf-agent-gocache go build -buildvcs=false \
  -o /private/tmp/okf-eval-codex-adapter-dd8989b \
  ./benchmark/agent/cmd/codex-adapter
test "$(shasum -a 256 /private/tmp/okf-eval-codex-adapter-dd8989b | cut -d ' ' -f 1)" = \
  8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9
test ! -e /private/tmp/okf-backfill-consumer-v2-plan.json
test ! -e /private/tmp/okf-backfill-consumer-v2-rows.jsonl
test ! -e /private/tmp/okf-backfill-consumer-v2-report.json

OKF_BACKFILL_COMMIT=$(git rev-parse HEAD)
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/backfill_cases.json \
  -plan /private/tmp/okf-backfill-consumer-v2-plan.json \
  -rows /private/tmp/okf-backfill-consumer-v2-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter-dd8989b \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id backfill-consumer-20260926-v2 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit "$OKF_BACKFILL_COMMIT" \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 6 -max-seconds 360 -max-tokens 150000 -unpriced \
  -toolkit-mode none \
  -model-tool-access 'Codex read-only ephemeral temp; projected question/artifacts/instructions only; no CLI projections'
```

The hard cap is six adapter invocations, 45 seconds per trial, and a
360-second runner context. The adapter itself uses a 40-second model timeout.
The 150,000 input-plus-output token threshold is observed **after** each call
and may be exceeded by that call. `-unpriced` records unavailable USD cost; it
does not mean zero cost or impose a dollar ceiling. Stop on exceeded observed
budget or context deadline and retain the partial rows. The plan and rows use
exclusive creation; never reuse these paths after an attempted run.

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/backfill_cases.json \
  -plan /private/tmp/okf-backfill-consumer-v2-plan.json \
  -rows /private/tmp/okf-backfill-consumer-v2-rows.jsonl \
  > /private/tmp/okf-backfill-consumer-v2-report.json
```

Publish the plan, raw rows, analyzer report, corpus/adapter/runtime hashes,
usage and unavailable price, plus a manual check of unexpected citations.
Report all six rows in the completion denominator even if the run is invalid;
show factual errors and refusals separately from event coverage. A complete
`valid=true` run proves only that the protocol collected analyzable rows.
Do not claim a quality benefit from this small synthetic fixture, even if the
treatment answers are all correct.
