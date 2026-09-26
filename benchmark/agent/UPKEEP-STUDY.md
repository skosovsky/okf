# Task 013: checker-on/off writer → consumer study

This is a separate experiment from the task-012 control/treatment pilot. The
Go analyzer in `upkeep_study.go` is offline and makes no model calls. Its report
is **not** evidence of a checker benefit until actual writer and independent
consumer rows have been collected. A checker result is process evidence, not
proof that the edited documentation is true.

## Frozen protocol

1. Freeze a plan JSON before inference. The two concrete starting cases in
   `corpus/upkeep_cases.json` are generated deterministically from the
   committed `corpus/backfill_cases.json`: A→B decision reversal and a handler
   capture that becomes truncated. Their initial bundles pass the Go OKF
   validator. To bind those cases to an actual experiment, run:

   ```sh
   go run ./benchmark/agent/upkeepstudy prepare \
     -corpus benchmark/agent/corpus/backfill_cases.json \
     -out /path/to/new-plan.json -id RUN_ID -repeats 1 \
     -writer-model PINNED_WRITER -consumer-model PINNED_CONSUMER \
     -writer-prompt-sha256 HASH -consumer-prompt-sha256 HASH \
     -checker-sha256 HASH
   ```

   The output is exclusive and includes the source corpus SHA-256. Record the
   source revision, checker
   binary SHA-256, writer and consumer model versions/settings, prompt hashes,
   run clock, corpus, repeat count, tool access, trial/time/token/cost caps and
   stop rule in the run notes. `UpkeepStudy` captures the fields checked by the
   offline analyzer; the run notes carry the rest. Do not edit the plan after
   seeing responses.
2. For each case and repeat, make two clean, isolated worktrees from identical
   `InitialCode` and `InitialBundle` bytes. The SHA-256 returned by
   `upkeepStartingHash` is recorded as `starting_sha256` in both writer rows.
   Use the same writer model, settings and task prompt. Randomize arm order;
   use a fresh session for every writer. Send only the Go
   `UpkeepCase.WriterRequest()` projection; never send the full plan, gold
   `Expected` object or consumer question to a writer. No ambient AGENTS or
   project instruction may mention upkeep in either arm. `checker_off` has no
   upkeep reminder.
   `checker_on` adds the pre-registered opt-in reminder and invokes the pinned
   `okf-upkeep` binary from a baseline captured before the writer edit. Supply
   the first `needs_review` result to the writer; allow one revision; record the
   final raw JSON result. Keep the checker advisory so both arms may finish.
   The checker config must explicitly include both `decision.go` and
   `handler-capture.txt` in `relevant_paths`, with the bundle under its
   `bundle_root`. Verify both paths yield `needs_review` in a dry-run before
   the model run; otherwise the truncated-evidence treatment is a no-op.
3. The writer must complete `TargetCode` exactly in both arms. Persist actual
   `FinalCode` and all `FinalBundle` artifacts, not a self-reported update flag.
   Validate each final bundle through the pinned Go OKF CLI and retain its
   exit/status in the run notes. A failed writer still gets a row with a
   `failure`; do not silently replace or discard the trial.
4. Start a new consumer session with no writer messages, source diff, checker
   result or arm label. Give it only `ConsumerQuestion` and the writer's final
   bundle via `UpkeepCase.ConsumerRequest(finalBundle)`. Never send the full
   plan, code task/target or gold `Expected` object. Record the SHA-256 of those visible artifacts and its raw
   `Observation`, including answer, evidence IDs, tokens and errors. The
   consumer session ID must differ from every writer session ID.
5. Analyze the frozen plan and saved rows:

   ```sh
   go run ./benchmark/agent/upkeepstudy \
     -plan /path/to/plan.json -rows /path/to/rows.json
   ```

The analyzer checks matching starting hashes, identical target code,
checker-on/off status, independent session IDs, exact consumer-visible bundle
hashes, and citations that exist in that bundle. `doc_update_rate` counts a
new or modified Markdown file. `concept_update_rate` excludes `index.md` and
`log.md` so a log-only edit cannot masquerade as a knowledge update. Deletions
are separate `doc_deleted` and `concept_deleted` counts; deleting a stale
concept does not count as an update. Both rates use
all preregistered cases × repeats as denominator. `answer_quality_rate` is
the share with the exact preregistered answer or alias *and* an expected
evidence ID. It is also divided by all cases × repeats; stale, wrong,
refusal, ungradable and failed trials remain visible. The report is `valid`
only when both writer and consumer rows exist for every pair and none failed.
Use descriptive counts for small samples; no upstream percentage is a product
claim.

The two frozen cases use the reversal and incomplete-evidence questions from
the task-010 corpus. The committed backfill treatment bundle is already
corrected, so the generator constructs a pre-change bundle for each case.
Do not add the current-code question until it has a distinct writer change and
consumer-visible evidence; otherwise it would duplicate the reversal trial.

`valid` means the saved rows satisfy this offline contract. It does not attest
to isolation of a model runtime or that the runner actually invoked the
checker/Go CLI. Preserve command logs, binary hashes and raw adapter metadata
alongside the rows; review those separately. Never infer freshness from
`reviewed_updated` alone.
