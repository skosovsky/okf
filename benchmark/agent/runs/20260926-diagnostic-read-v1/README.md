# File-read diagnostic: control stopped on parser mismatch

The separately authorized `diagnostic-read-20260926-v1` ran on clean HEAD
`2af06eb420e2fc5b74c39e95957822233761a904` with the pinned one-case
corpus (`15a2a6b0646acdbd18114b54f49f971fee512ad8dfec04aad9d34641820b0d95`).
The bundled Codex runtime pin was `codex-cli 0.155.0-alpha.16.3`, SHA-256
`c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`.
The Go CLI binary SHA-256 was
`67dd408da3a634fd0560b1a1c97f0c18d0cefa88e7c023afd902910bfd6b83cb`;
the adapter SHA-256 was
`ecb39f0fa8634bd1b2edfb67f149828557d6443ce710a4408029c19db848f045`.
The saved `metadata.prompt_sha256` covers only the shared request instruction,
not the full adapter-built prompt or Codex's injected context. That is why it
matches the previous primary run despite the new `cat` instruction. New plan
files label this scope explicitly as `shared_request_instructions_only`.

One control model call ran; treatment was not attempted. Control answered
`0.2` and cited `current`, matching the frozen factual answer. The event
metadata contains two started/completed `command_execution` pairs, both
completed with exit code zero. Both were classified `other`, so the strict
read proof failed and the runner correctly stopped before treatment. The
saved command SHA-256s match these safe local candidate strings exactly:

| Event command SHA-256 | Matching shell command |
| --- | --- |
| `d5aad6657cc20cb7ebbe0cee41e46748b79e42f0832638609260c1f9d10f970f` | `/bin/zsh -lc 'cat docs/legacy-support.md'` |
| `3472d33018eb9d0e6e3c660371a560de8199d827ed5ffee0f42c23d5f2805e3e` | `/bin/zsh -lc 'cat bundle/doc.go'` |

This identifies a parser mismatch: the diagnostic recognizer supports safely
quoted bash/sh wrappers, but Codex used `/bin/zsh`. The metadata does not
retain command output, so exact content delivery cannot be proven after the
fact. The factual answer is one observed answer, not a control/treatment
quality comparison. The frozen analyzer correctly reports `valid=false`:
one operational failure and one missing treatment trial.

An offline parser change after this run adds a narrowly accepted `/bin/zsh`
wrapper and requires the complete `cat` output to equal the supplied artifact
content. It does not retroactively confirm the old row: that output was not
saved. A future run needs its own plan and authorization.

Observed usage: 45,362 input tokens, of which 28,160 were reported cached,
and 102 output tokens. The total input+output count is 45,464, below the
60,000 observed soft stop. There is no per-call USD price in the adapter, so
cost remains unknown. The 2-call/120-second limits were respected. No second
call was made after the control read-proof failure. The raw plan, row, report
and redacted event metadata are saved here; no raw source or shell output is
stored in the event trace.

Exact command:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/diagnostic_primary.json \
  -plan /private/tmp/okf-diagnostic-read-v1-plan.json \
  -rows /private/tmp/okf-diagnostic-read-v1-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -go-cli /private/tmp/okf-eval-cli \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id diagnostic-read-20260926-v1 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit 2af06eb420e2fc5b74c39e95957822233761a904 \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 2 -max-seconds 120 -max-tokens 60000 -unpriced \
  -toolkit-mode direct -diagnostic-read \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```
