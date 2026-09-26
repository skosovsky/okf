# Preregistered metadata-only contrast

This is a protocol for a future run, not a completed result or authorization
for model calls. The completed primary precomputed pilot has eight rows and an
analyzer report with `valid=true`; its treatment includes Go CLI projections.
Do not pool those rows or their denominator with this contrast.
The earlier `metadata-only-20260926-v1` protocol was superseded before any
model call: its adapter would have exposed harness `case_id` and `arm` labels
to Codex, leaking the experimental assignment. This v2 protocol uses a
blinded request projection; v1 has no observations to analyze.

`corpus/metadata_cases.json` (SHA-256
`4fd2222f07b91cacda4e8f15b0b34792f41d962a151e2ddc95760b8329b1745a`)
contains four cases, three `mechanism_only` and one `realistic`. Each pair has
the same question, artifact IDs, paths and Markdown body after stripping the
treatment YAML frontmatter and boundary whitespace. The treatment adds only
OKF frontmatter, including document `status`; both arms retain the same
answer-bearing text and ordinary freshness clues. This tests the incremental
effect of metadata in this small corpus. It does not test CLI projections,
autonomous tool selection or general repository performance.

The preregistered endpoint is a complete, analyzable eight-row paired run:
four cases, control then treatment, one repeat. Report exact-v1 `correct`,
`stale`, `wrong`, `refusal`, `ungradable` and operational failures for each arm,
with factual-error and completion denominators from `README.md`. Also report
tool calls, input/output/cache tokens, elapsed time and unknown dollar cost.
There is no efficacy threshold; four paired observations are descriptive and
cannot establish a general percentage benefit. Inspect unexpected raw answers
and citations without changing the frozen grader or promoting a row by hand.

Before any run, obtain explicit authorization for eight OpenAI Codex calls and
transfer of these four cases' source excerpts and OKF documents. Run from the
clean pinned checkout at `dd8989ba36b8e27ac5762e2e0984cac7f88e8738`.
Record that full HEAD in the plan, alongside the pinned
instant-profile SPEC revision `0b87c52c6ef999286c745e19998fdfcd03d5dbee`,
the corpus above and the adapter built from that checkout with
`-buildvcs=false`. Its SHA-256 is
`8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9`
at `/private/tmp/okf-eval-codex-adapter-dd8989b`. If it is unavailable or
differs, freeze a new adapter hash and run ID before any model call. The runner
checks the clean commit, SPEC lock and runtime pins before writing its plan. The
bundled Codex runtime is `codex-cli 0.155.0-alpha.16.3` with SHA-256
`c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`.
The adapter pins `gpt-6-luna` with low reasoning effort; it cannot verify the
backend model snapshot. Keep the model settings and read-only tool policy the
same for both arms.

From that clean repository root, verify the pins and use new, nonexistent
output paths:

```sh
set -e
set -o pipefail
test -z "$(git status --porcelain --untracked-files=all)"
test "$(git rev-parse HEAD)" = dd8989ba36b8e27ac5762e2e0984cac7f88e8738
test "$(shasum -a 256 benchmark/agent/corpus/metadata_cases.json | cut -d ' ' -f 1)" = \
  4fd2222f07b91cacda4e8f15b0b34792f41d962a151e2ddc95760b8329b1745a
test "$(/Applications/ChatGPT.app/Contents/Resources/codex --version 2>/dev/null)" = \
  'codex-cli 0.155.0-alpha.16.3'
test "$(shasum -a 256 /Applications/ChatGPT.app/Contents/Resources/codex | cut -d ' ' -f 1)" = \
  c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a
test "$(shasum -a 256 /private/tmp/okf-eval-codex-adapter-dd8989b | cut -d ' ' -f 1)" = \
  8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9
test ! -e /private/tmp/okf-metadata-only-v2-plan.json
test ! -e /private/tmp/okf-metadata-only-v2-rows.jsonl

OKF_METADATA_COMMIT=$(git rev-parse HEAD)
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go run \
  ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/metadata_cases.json \
  -plan /private/tmp/okf-metadata-only-v2-plan.json \
  -rows /private/tmp/okf-metadata-only-v2-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter-dd8989b \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id metadata-only-20260926-v2 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit "$OKF_METADATA_COMMIT" \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 8 -max-seconds 360 -max-tokens 150000 -unpriced \
  -toolkit-mode none \
  -model-tool-access 'Codex read-only ephemeral temp; projected question/artifacts/instructions JSON; no CLI projections'
```

`-toolkit-mode none` prevents runner Go CLI projections in both arms. The
adapter sends only `question`, `artifacts` and common `instructions` to Codex
in a fresh, ephemeral, read-only session for each trial. Harness `case_id`,
`arm` and the gold answer remain outside the model-visible prompt. This
protocol does not prove that the model read every supplied artifact. The
runner limits each adapter call to 45 seconds and sets a 360-second overall
context deadline. Its 150,000-token
threshold is checked **after** each response and can be exceeded by one call.
`-unpriced` acknowledges unavailable per-call USD cost; it does not mean zero
cost. The plan and rows are created exclusively, so an interrupted attempt
needs a new run ID and output paths. Keep partial rows and report the run
invalid if any call, pin, or budget check fails.

Analyze the saved rows with the same corpus:

```sh
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go run \
  ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/metadata_cases.json \
  -plan /private/tmp/okf-metadata-only-v2-plan.json \
  -rows /private/tmp/okf-metadata-only-v2-rows.jsonl
```

The shared prompt asks for the shortest factual value and an original artifact
ID. Exact-v1 grades a correct answer only when it matches the registered
answer or alias and cites `current`. A correct answer with no `current`
citation is `ungradable`; an explanatory answer such as `No, not yet` is
`wrong` under this frozen exact grader. Stale values are matched exactly.
Publish the plan, raw rows, analyzer report, corpus hash and any manual
inspection notes. `valid=true` means the comparison was complete and
analyzable, not that metadata improved answers.
