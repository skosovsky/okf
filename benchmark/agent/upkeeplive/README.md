# Task 013 live writer → consumer runner

`upkeep-live` is a Go runner for the frozen `UpkeepStudy` plan. It does not call a
model by itself. An executable adapter receives one JSON `Request` on stdin and
returns one JSON `Response` on stdout. The adapter must start a **new model
session for each invocation**, even for the checker-on revision. The runner
rejects repeated session IDs. Build and pin the adapter and runner binaries,
model IDs/settings, prompts, source commit, plan and corpus before a run.

```sh
go build -o /absolute/path/to/upkeep-live ./benchmark/agent/upkeeplive/cmd/upkeep-live
go build -o /absolute/path/to/codex-upkeep-adapter ./benchmark/agent/upkeeplive/cmd/codex-upkeep-adapter
/absolute/path/to/codex-upkeep-adapter --print-prompt-sha256
# Use those two hashes when preparing the frozen UpkeepStudy plan. Set its
# checker_sha256 to the SHA-256 of the built upkeep-live binary, because the
# checker is linked into that binary; the CLI verifies equality before calls.
# Pin OKF_UPKEEP_CODEX_RUNTIME, OKF_UPKEEP_CODEX_VERSION,
# OKF_UPKEEP_CODEX_SHA256, OKF_UPKEEP_WRITER_MODEL,
# OKF_UPKEEP_CONSUMER_MODEL and OKF_UPKEEP_REASONING_EFFORT in the environment.
/absolute/path/to/upkeep-live \
  -plan /absolute/path/to/frozen-plan.json \
  -rows /absolute/path/to/new-rows.json \
  -audit /absolute/path/to/new-audit.json \
  -adapter /absolute/path/to/codex-upkeep-adapter \
  -max-calls 12 -max-seconds 600 -max-tokens 150000 -call-seconds 45
go run ./benchmark/agent/upkeepstudy analyze \
  -plan /absolute/path/to/frozen-plan.json \
  -rows /absolute/path/to/new-rows.json
```

The plan is read before inference; rows and audit paths are created
exclusively. A run is complete only when `AUDIT.complete` exists and its
`rows_sha256` and `audit_sha256` match the two files; missing marker means a
crash or write failure left incomplete outputs. This marker detects incomplete
files; it is not a power-loss atomicity guarantee because directory metadata
is not fsynced. The audit file stores plan,
adapter and runner binary hashes, UTC start time,
Go version, call/token counts, checker statuses, final bundle hashes and Go
CLI exits. The runner has a hard call cap, per-call timeout and total timeout.
Its token cap is checked **after** each adapter response, so one call may
overshoot. If usage is missing or the adapter transport fails, the attempted
slot is retained as an operational failure and no further model call is made;
`unknown_usage_calls` flags that the token cap cannot be attested. It has no
dollar cap or model price information. An interrupted process may leave empty
output files; preserve such failures and do not report them as complete
observations.
The command requires `-call-seconds` of at least 45: the built-in Codex
adapter uses a 38-second internal deadline, leaving time for response transport
and cleanup before the outer timeout.

Before any adapter call, a dry-run applies each case's exact target code in a
temporary Git repository and requires an advisory `needs_review` from the
in-process Go upkeep checker. Every actual arm gets a new temporary Git
repository and an identical committed baseline. The runner records actual
target-code bytes, final OKF files and a Go CLI validation result. The
checker-off writer sees the code task and initial bundle. The checker-on
writer also sees the registered reminder, then a real `needs_review` result
after its first draft and gets one revision. If a concept changes, the runner
submits an `updated` decision with the changed concept paths; a
`reviewed_updated` result proves only that files changed, not that the new
claim is true. The independent consumer receives only the final bundle and
question: no arm, code task, checker result, writer transcript or gold answer.

The adapter request has `role` (`writer`, `writer_revision`, `consumer`),
`repeat`, pinned model and prompt hash, and exactly one of `writer` or
`consumer`. Only checker-on writer
requests have `reminder`; only `writer_revision` has `draft` and
`checker_initial`. The response has a unique `session_id`, usage counters,
optional `failure`, and either a `draft` (`final_code`, `final_bundle`) or an
`observation` (`answer`, `evidence`, etc.). All artifacts are full `{id,path,content}`
objects. The runner accepts only safe relative paths and requires final code
to equal the plan's `TargetCode` exactly. Adapter sessions and actual model
settings cannot be independently attested by this subprocess boundary; retain
the adapter's own invocation logs and verify the model/prompt pins during
review. An executable BYOT adapter is trusted local code with the process's
host permissions; its SHA pin alone does not confine it to read-only access.
The built-in `cmd/codex-upkeep-adapter` launches one ephemeral Codex CLI
session in a read-only sandbox per request and checks the model, prompt,
runtime version and SHA pins. It records the `thread_id` emitted by Codex as
the session ID. The model-visible consumer JSON contains only `question` and
`artifacts`; case ID and repeat stay in the harness. Its writer prompt SHA-256 is the hash of the
`writerPrompt` constant in that command; likewise for `consumerPrompt`.
The plan's `checker_sha256` must equal the runner executable's SHA-256: this
pins the in-process Go `upkeep` package compiled into that executable rather
than an external `okf-upkeep` binary. Also record the source commit. The
checker-on intervention
combines a reminder, checker feedback and an extra revision call; its result
cannot isolate the causal effect of the checker alone.

`go test ./benchmark/agent/upkeeplive/...` uses a deterministic fake adapter.
It verifies real Git snapshots, checker transitions, Go CLI bundle validation,
session separation, role projections, cap behavior and analyzer-compatible
rows. These tests are not a model result.
