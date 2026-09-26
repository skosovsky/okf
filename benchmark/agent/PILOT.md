# Preregistered live pilot command

The first accepted run is the four-case repository-source subset, eight paired
trials. Run only after explicit approval for Codex CLI to send the benchmark
artifacts to an external model, use its read-only shell tool, and write its
own state outside the workspace. The earlier escalated two-trial attempt was
rejected by automatic approval review; do not work around that rejection.

Before execution, the corrected 004/005 code, SPEC, corpus and adapter must be
in one clean commit. `-commit` must equal `git rev-parse HEAD`. `-spec` must be
the full upstream commit in `skills/open-knowledge-format/SKILL.md`; the
preflight checks that declaration against the actual SPEC SHA-256. Build both
Go binaries from that clean checkout:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go build -o /private/tmp/okf-eval-cli ./cmd/okf
GOCACHE=/private/tmp/okf-agent-gocache go build -o /private/tmp/okf-eval-codex-adapter ./benchmark/agent/cmd/codex-adapter
```

The command below fixes the model ID, low reasoning effort, eight calls,
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
  -plan /private/tmp/okf-primary-plan.json \
  -rows /private/tmp/okf-primary-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -go-cli /private/tmp/okf-eval-cli \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit FULL_CORRECTED_GIT_SHA \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 8 -max-seconds 360 -max-tokens 12000 -unpriced \
  -toolkit-mode direct \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```

Replace the full corrected Git SHA argument from the clean checkout before
approval. The SPEC revision is the repository's pinned instant profile. Keep
the exact command in the report. Analyze the immutable
plan and raw rows:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/primary_realistic.json \
  -plan /private/tmp/okf-primary-plan.json \
  -rows /private/tmp/okf-primary-rows.jsonl
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

Follow-up runs use separate plans and rows: `corpus/metadata_cases.json` with
`-toolkit-mode none` (8 trials), `corpus/backfill_cases.json` with direct mode
(6 trials), and the 013 checker-on/off writer→consumer pair. A before/after
004/005 comparison additionally needs a clean old-commit worktree and its own
SPEC lock; tool failures in that baseline are counted as operational failures.
No follow-up run is implied by success of the first pilot.
