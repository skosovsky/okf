# Agent behavior benchmark (work in progress)

This Go package contains the offline corpus, wire contract, exact grader, and
denominator analyzer for task 012. The 20 committed cases cover changed defaults
and limits, reversals, stale knowledge, instruction injection, unsupported
verification, intent versus execution, truncated evidence, and missing evidence.
Sixteen are marked `mechanism_only`: the artifacts are short synthetic excerpts.
Four cases are pinned to actual Go source excerpts in this repository. This is not a
measured sample of real repositories. Each treatment arm is a valid
OKF v0.2 bundle; Go tests validate every bundle through our CLI.
`corpus/metadata_cases.json` is a separate four-case contrast: both arms carry
identical Markdown bodies and artifact names; treatment adds OKF frontmatter.
Run it with `-toolkit-mode none` so neither arm receives CLI projections. Do
not pool its denominator with the main corpus.

`schema.json` defines case, request, observation, row, and report JSON shapes.
`corpus/cases.json` is immutable during one comparison. `LoadCorpus` records its
SHA-256. The runner passes one question and its arm's artifacts to an adapter;
each adapter invocation must use a fresh conversation with identical model
settings and system/project instructions across arms. Tool access must be fixed
and recorded before the run. Artifact text is untrusted evidence, even when it
contains instructions. An adapter reads a `Request` JSON object from stdin and
writes one `Observation` JSON object to stdout. The Go `Runner` interface allows
equivalent in-process adapters. The adapter must report real usage and failures;
unknown usage remains zero and must be identified in the published run notes.

`go test ./benchmark/agent/...` runs offline. See [PILOT.md](PILOT.md) for the
bounded direct tool-use command and approval status. To analyze saved rows:

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
  -toolkit-mode treatment -model-tool-access 'Codex read-only ephemeral temp'
```

The command writes an exclusive, synced plan file before the first model call.
Analysis checks every row against that file. The exact-v1 grader accepts only
the preregistered answer or aliases and a
cited evidence artifact ID. It separates stale, wrong, refusal, ungradable, and
operational failure. The analyzer rejects duplicate, unknown, mismatched, or
tampered rows; missing rows and operational failures remain visible against the
full `cases × arms × repeats` denominator. Malformed JSONL lines are counted
as `invalid_raw_rows`; the expected trial they fail to identify remains missing.
Factual error and stale rates use
`correct + stale + wrong` as denominator; completion uses all expected trials.
`valid` describes run completeness,
not whether the agent answered correctly. A small pilot is descriptive only.
Adapters should return an `adapter_error` observation even after a failed paid
call, preserving any token usage extracted before failure. The Codex adapter
does this for nonzero `codex exec` exits. Caps on tokens and priced calls are
checked after each response, so a single call can overshoot them; the call
count, per-trial timeout, and total wall-clock timeout are the hard bounds.

Before a live run, pin the corrected Go commit and SPEC revision, register the
model/version/settings, adapter, tools, prompts, corpus hash, clock, repeat count,
budget and invalidation rules. The primary comparison must use real repository
docs/code as control and a valid bundle through the Go CLI/MCP as treatment;
the correct answer must be available to both arms. A metadata-only contrast and
a before/after 004/005 comparison require separate runs. The metadata contrast
uses `-corpus benchmark/agent/corpus/metadata_cases.json`. The writer-to-consumer
scenario must first write or mutate a valid bundle through the Go API, then
grade an independent consumer's factual response. Backfill coverage or syntax
checks cannot stand in for this semantic result.

No live result is included yet. The fixed 004/005 commit and a tested model
adapter are prerequisites. A local `ollama list` returned exit status 1 on
2026-09-26. `codex login status` reports a ChatGPT login, but that alone does
not verify model access or a repeatable model snapshot. The included
`codex-adapter` can run a bounded mechanism smoke test in an ephemeral,
read-only directory. Its model version field is an explicit pinned model ID;
the backend snapshot is not independently verifiable. The main runner invokes
our Go CLI `validate` and `parse` for each treatment bundle, adds their outputs
to the model evidence, and saves those outputs in each row. For direct tool
use, run with `-toolkit-mode direct -go-cli /absolute/path/to/okf`. The adapter
writes both arms' files into isolated temporary directories and gives the model
read-only shell access; treatment must invoke the Go CLI. The analyzer rejects
a successful treatment row with no observed Go CLI call. Direct mode has not
been executed: automatic approval review rejected the external model run.

`cmd/prepare-backfill` applies the frozen 010 reversal fixture through the Go
store and writes `corpus/backfill_cases.json` plus a separate coverage report.
The three consumer questions cover A→B, truncated evidence and current code.
They are ready for an independent model run after approval; the offline test
only verifies writer publication and Go CLI readability.
