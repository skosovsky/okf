# Preregistered live run: Go CLI before/after 004/005

This is a runbook, not a result or permission to invoke an external model. The
frozen execution checkout is clean commit
`260a7cb701f08e21efd94e0b04db9b491f7e1db7`. Keep this runbook outside
that checkout during execution: adding an untracked `LIVE.md` makes the runner's
clean-checkout gate fail, while committing it changes HEAD and invalidates the
`-new-commit` pin. Run from a separate clean checkout at that exact commit or
restore that checkout to the commit after retaining this text elsewhere.

The five frozen questions in `corpus.json` comprise four 005 date-spec
interoperability cases and one 004 instant-spec capability case. Both arms use
the same source bytes, question, factual rubric, model, adapter, settings,
read-only tool policy and fixed clocks. Each model call receives original
source artifacts and the corresponding Go CLI `validate --json` and
`parse --json` projections. The historical 005 binary is expected to return
12 classified validation diagnostics; JSON exit 1 is evidence, not an
operational failure. The corrected 005 binary is expected to validate cleanly.
The historical CLI cannot select the later instant revision, so each 004 old
row is `revision_unsupported` without a model call. This measures an 004
capability gap, not a paired factual-answer improvement. Only the eight paired
005 comparisons (16 answer rows) support a descriptive model-outcome comparison.

Pinned inputs, verified before this document was written:

| Input | Pin |
| --- | --- |
| Historical source commit | `ed7ddc28cd127682023bd150ee377906b817e099` |
| Corrected clean source commit | `260a7cb701f08e21efd94e0b04db9b491f7e1db7` |
| Historical CLI `/private/tmp/okf-baf-old-okf` SHA-256 | `d74a1d309c72ae10efe794c3e5413186a71058fb2f465d9bf594293c2ad4ed47` |
| Corrected CLI `/private/tmp/okf-baf-new-okf` SHA-256 | `5ff4dfaea4dc646ff51dcfe48dc94463f5233d16e113f1cde09825fca9cff2a0` |
| Adapter `/private/tmp/okf-baf-codex-adapter` SHA-256 | `2e691b39499dd6da12d3edc8ee73cc6548cc31151537d47b171462eafb92a260` |
| Frozen corpus `benchmark/agent/beforeafter/corpus.json` SHA-256 | `05d45be91e4ab15f49b8f71c4085eca91e85dddad3af6792755d824f536a6db4` |
| Codex runtime `/Applications/ChatGPT.app/Contents/Resources/codex` | `codex-cli 0.155.0-alpha.16.3`, SHA-256 `c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a` |
| Model and settings | `gpt-6-luna`, version label `gpt-6-luna`, `{"reasoning_effort":"low"}`; backend snapshot cannot be independently pinned |

Before executing, obtain explicit approval for transfer to **external OpenAI
Codex** of the five questions and source excerpts from
`benchmark/agent/beforeafter/corpus.json` and its referenced OKF fixtures,
including the foreign `okf-skills` bundle and instant-profile example. Each
model request also contains the old or new Go CLI JSON projections and their
provenance. The adapter may write Codex service state outside the workspace;
each call uses a fresh process and read-only shell in an ephemeral temporary
directory. Approval must cover at most **18 calls**, a **900-second** wall-clock
cap, a **400,000 observed input-plus-output token** soft stop, and unpriced
usage. The runner cannot enforce a USD cap because the adapter does not report
per-call dollar cost. The token stop is checked after each call and can be
overshot by that one call; the per-call timeout is 45 seconds. No call is
authorized by this file alone.

Verify clean HEAD and the pins immediately before the approved run. The plan
and rows paths below must not exist; they are created exclusively and are
never reused after an attempted run. The parent directory must exist. The
runner saves a plan before the first model call, alternates arm order by case
and repeat, and expects 20 rows from two repeats, including two model-free
004 old rows.

```sh
git status --porcelain --untracked-files=all
git rev-parse HEAD
shasum -a 256 /private/tmp/okf-baf-old-okf /private/tmp/okf-baf-new-okf /private/tmp/okf-baf-codex-adapter benchmark/agent/beforeafter/corpus.json /Applications/ChatGPT.app/Contents/Resources/codex
test ! -e /private/tmp/okf-baf-live-20260926-v1-plan.json
test ! -e /private/tmp/okf-baf-live-20260926-v1-rows.jsonl

GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/beforeafter/cmd/okf-before-after run \
  -repo-root . \
  -corpus benchmark/agent/beforeafter/corpus.json \
  -plan /private/tmp/okf-baf-live-20260926-v1-plan.json \
  -rows /private/tmp/okf-baf-live-20260926-v1-rows.jsonl \
  -old-bin /private/tmp/okf-baf-old-okf \
  -old-bin-sha256 d74a1d309c72ae10efe794c3e5413186a71058fb2f465d9bf594293c2ad4ed47 \
  -new-bin /private/tmp/okf-baf-new-okf \
  -new-bin-sha256 5ff4dfaea4dc646ff51dcfe48dc94463f5233d16e113f1cde09825fca9cff2a0 \
  -adapter /private/tmp/okf-baf-codex-adapter \
  -adapter-sha256 2e691b39499dd6da12d3edc8ee73cc6548cc31151537d47b171462eafb92a260 \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -new-commit 260a7cb701f08e21efd94e0b04db9b491f7e1db7 \
  -run-id beforeafter-20260926-v1 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -repeats 2 -max-model-calls 18 -max-seconds 900 \
  -max-tokens 400000 -unpriced
```

After the run, analyze the saved rows without further model calls:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/beforeafter/cmd/okf-before-after analyze \
  -repo-root . \
  -corpus benchmark/agent/beforeafter/corpus.json \
  -plan /private/tmp/okf-baf-live-20260926-v1-plan.json \
  -rows /private/tmp/okf-baf-live-20260926-v1-rows.jsonl
```

Preserve plan, raw rows, analyzer output, and any failure evidence. Report 005
correctness by paired case and arm, 004 capability separately, all operational
failures, tokens, elapsed time, and unknown USD cost. A partial run or
`valid=false` cannot be used as a before/after result; 8 pairs are still a
small descriptive sample and must not support a general product claim.
