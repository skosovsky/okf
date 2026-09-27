# 004/005 before-and-after protocol (not yet run with a model)

This is a separate experiment from the primary control-vs-OKF comparison in
`PILOT.md`. It compares **two Go toolkit revisions on the same frozen input**,
using the same model, adapter binary, settings, prompt, clock, tool policy and
trial order. No model result exists yet. In particular, the successful paired
Go performance benchmark in `benchmarks/toolkit/` is not an agent-quality result.

## Revisions and strata

| Stratum | Old Go | Corrected Go | Governing contract | Interpretation |
| --- | --- | --- | --- | --- |
| 005 Markdown/extension bug fixes | `ed7ddc28cd127682023bd150ee377906b817e099` | Pin a clean descendant of `0f15ab4` | Date profile, upstream `3fcbb9f828c2f23d109c855ee403c3a4c81f3a96` | Same-spec regression/fix comparison. |
| 004 datetime revision support | same old commit | same corrected commit, explicit `instant-0b87c52` | New upstream `0b87c52c6ef999286c745e19998fdfcd03d5dbee` | New normative contract support. Old rejection of an instant is **revision unsupported**, not a defect under its pinned date contract. |

Keep these denominators separate. The old CLI has no `--temporal-profile` flag;
the corrected CLI defaults to the old date contract. Do not quietly validate
the old arm against the new SPEC or call a rejected datetime an old-spec bug.
Record an expected old-arm 004 inability as `revision_unsupported`, a separate
capability outcome outside both `operational_failure` and the LLM factual-answer
denominator; there is no old-arm model answer to pair for that case. Still show
the capability count in the full experiment accounting. Unexpected CLI failures
remain operational failures.
Freeze `as_of` (date or offset datetime as appropriate); neither arm may use the
machine clock to decide staleness. The foreign 005 snapshot is
`fixtures/interoperability/foreign-68ce7a0/`, with provenance and expected
diagnostics in its manifest. Its selected files are a conformance probe, not
agent questions. Build factual questions whose answer is independently present
in both arms' source material. Some `primary_realistic.json` questions quote
post-fix CLI source, so that file must **not** be reused unchanged for this
comparison.

## Offline preflight

The commands below extract the historical tree without changing the checkout
or contacting the network. Run from the repository root. Freeze the corrected
commit before the model run, then save all SHA-256 values and Go version beside
the plan. The corrected checkout should be clean when it is pinned.

```sh
set -e
set -o pipefail
export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
OLD=ed7ddc28cd127682023bd150ee377906b817e099
test -z "$(git status --porcelain --untracked-files=all)" || { echo 'commit all experiment inputs first' >&2; exit 1; }
NEW=$(git rev-parse HEAD)
RUN_DIR=$(mktemp -d /private/tmp/okf-agent-before-after.XXXXXX)
mkdir -p "$RUN_DIR/old"
git archive "$OLD" | tar -x -C "$RUN_DIR/old"
GOCACHE="$RUN_DIR/gocache" go -C "$RUN_DIR/old" build -o "$RUN_DIR/old-okf" ./cmd/okf
GOCACHE="$RUN_DIR/gocache" go build -o "$RUN_DIR/new-okf" ./cmd/okf
if "$RUN_DIR/old-okf" validate --path fixtures/interoperability/foreign-68ce7a0 --json --as-of 2026-09-26 > "$RUN_DIR/old-005.json"; then OLD_EXIT=0; else OLD_EXIT=$?; fi
if "$RUN_DIR/new-okf" validate --path fixtures/interoperability/foreign-68ce7a0 --json --temporal-profile date-3fcbb9f --as-of 2026-09-26 > "$RUN_DIR/new-005.json"; then NEW_EXIT=0; else NEW_EXIT=$?; fi
test "$OLD_EXIT" -eq 1 && test "$NEW_EXIT" -eq 0
test -s "$RUN_DIR/old-005.json" && test -s "$RUN_DIR/new-005.json"
printf 'old_exit=%s new_exit=%s\n' "$OLD_EXIT" "$NEW_EXIT"
"$RUN_DIR/new-okf" validate --path fixtures/v02/temporal-profiles/instant --json --temporal-profile instant-0b87c52 --as-of '2026-09-26T12:00:00+07:00' > "$RUN_DIR/new-004.json"
shasum -a 256 "$RUN_DIR/old-okf" "$RUN_DIR/new-okf" "$RUN_DIR/old-005.json" "$RUN_DIR/new-005.json" "$RUN_DIR/new-004.json"
go version
```

The final experiment should invoke each **pinned binary**, not build the old
runner in the old checkout. Record `NEW` as the full commit ID, the fixture
digest, both binary digests, exit status, diagnostics and elapsed time. The
expected `OLD_EXIT=1` is caused by the old 005 diagnostics; the preflight fails
on any other exit pair. The final runner must also retain nonzero exits and
their JSON reports. Verify date-profile 005 cases
against the unchanged `3fcbb9f` document, and instant cases against `0b87c52`.
The old source contains only `spec-v02.md`; it cannot pass the current
`pinnedInputs` check for the instant document from inside its own tree.
On the five-file selected snapshot, this offline command observed 12 errors
with the old CLI and 0 with the corrected date-profile CLI (both 0 warnings
without `--strict`). Those counts test validator behavior, not agent answers.

## Live protocol to implement before requesting a bounded run

1. Commit a small, revision-stable question corpus. For every case, store
   identical bytes and question across old/new arms, a factual gold answer,
   source citation, contract stratum, and expected capability. Include at
   least a wrapped log item, root title/intro, unknown root `upkeep`, an inline
   code footnote marker, and a date-vs-datetime temporal case. Test the
   corpus offline with AAA tests and hash it before the first model call.
2. Give the **same** model-visible prompt and ordinary source artifacts to
   both arms. Add only the corresponding CLI projection/result, labelled with
   binary SHA and contract revision. Use one adapter binary, fixed model ID,
   version, reasoning settings and tool permissions. Alternate or randomize
   paired order with a saved seed; run at least two repeats to expose variance.
   The adapter must start a fresh conversation per trial. Source text remains
   untrusted evidence and cannot change harness policy.
3. For each trial, materialize an isolated bundle, invoke its selected binary
   with `validate --json --as-of 2026-09-26` and, where applicable,
   `parse <file> --json --as-of 2026-09-26`; both old and new `parse` commands
   accept `--as-of`. Use the date selector only on the new binary for the 005
   stratum. For 004 use the corrected instant selector and frozen
   `--as-of 2026-09-26T12:00:00+07:00` for both `validate` and `parse`;
   register the old CLI's unsupported
   revision as a capability outcome without invoking the model. Capture process exit,
   stdout/stderr digest, parsed diagnostics, command/argument digest and
   duration before invoking the model. Preserve a row even when the CLI fails.
   Process launch failure, crash, unsupported invocation or unparseable output
   is `operational_failure`; do not ask the model to guess or score that as a
   factual error. A well-formed validation report can have nonzero exit due to
   diagnostics: show that report to the model and separately classify each
   diagnostic against the pinned SPEC. Thus the old CLI's 005 false positives
   remain the very mechanism being tested, not missing model trials.
4. Preregister case count, repeats, all expected rows, grading aliases,
   denominator, invalidation rules, soft observed-token/cost stops and hard
   call/time caps. Save plan before the first paid call and sync every row.
   Analyze `correct`, `stale`, `wrong`, `refusal`, `ungradable` and
   `operational_failure` separately; missing rows remain in the completion
   denominator. Publish raw rows, per-stratum paired table, failures and
   limits. At small `n`, report descriptive differences only.

The current `okf-agent-eval` **cannot execute this live protocol unchanged**:
`exposeGoCLI` calls the compiled-in `internal/okfcli.Run`, hardcodes the instant
profile, and discards a well-formed validation report whenever the command
returns nonzero. The external-binary path must preserve such reports, including
the old false-positive diagnostics. `pinnedInputs` also requires `-commit` to
equal this checkout's HEAD.
The historical commit predates `benchmark/agent` entirely. Merely passing the
old SHA in `-commit`, or using `-exploratory`, would falsely label current code
as the baseline. Add a pinned external-CLI input to the Go runner, record its
SHA per arm, and make the temporal selector explicit; alternatively write a
small Go orchestration layer using the existing `Runner`, `Row` and analyzer
contracts. Keep the adapter and grader frozen across both arms. Until that is
implemented and an authorized live run completes, issue 012's before/after
measurement is open.
