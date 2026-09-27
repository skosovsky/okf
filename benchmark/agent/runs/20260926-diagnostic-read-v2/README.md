# File-read diagnostic v2: control made no shell call

The separately authorized `diagnostic-read-20260926-v2` ran on clean HEAD
`29604a133ba6656de7c75b738c2ca221a469bdf4` with the one-case corpus
`15a2a6b0646acdbd18114b54f49f971fee512ad8dfec04aad9d34641820b0d95`.
The bundled Codex runtime was `codex-cli 0.155.0-alpha.16.3`, SHA-256
`c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`.
The Go CLI binary SHA-256 was
`e3b9f1c344f5a169d08332e14b96306b54ab7d61820afafe9ef784ffa5f6e525`;
the adapter SHA-256 was
`954f5e31d3c014b883bae4bfbe43c7214c38c818c68fecd3094e05f5bbd0fe34`.

One control model call ran; treatment was not attempted. The complete saved
event metadata consists of `thread.started`, `turn.started`, one completed
`agent_message`, and `turn.completed`. There were **zero** command or MCP tool
events, so this is not a shell-command parser mismatch. Codex did not visibly
read either file, despite the shared `cat` instruction. The model answered
`Insufficient evidence` and cited `current`; the frozen factual answer is
`0.2`, supported by the `current` artifact. Since no file read was observed,
the runner marked the control row an operational failure and stopped before
treatment. The analyzer reports `valid=false`: one operational failure and one
missing treatment row. There is no control/treatment quality comparison and
no Go CLI invocation.

The prior v1 control did emit two successful shell events through `/bin/zsh`.
These two runs show that a prompt instruction alone has not made shell use
reliable; they do not establish why the model chose differently. The v2 parser
supports safe zsh wrappers, but it had no command event to parse here.

Observed usage was 14,997 input plus 28 output tokens (15,025 total), with
zero cached tokens and no per-call USD price. The 2-call/120-second bounds and
150,000 observed soft token threshold were respected. Cost remains unknown;
there is no USD cap. No second call was made after the control read-proof
failure. `plan.json`, `rows.jsonl`, and `report.json` preserve the registered
plan, raw row, sanitized event metadata, and analyzer output.

Exact command:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/diagnostic_primary.json \
  -plan /private/tmp/okf-diagnostic-read-v2-plan.json \
  -rows /private/tmp/okf-diagnostic-read-v2-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -go-cli /private/tmp/okf-eval-cli \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id diagnostic-read-20260926-v2 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit 29604a133ba6656de7c75b738c2ca221a469bdf4 \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 2 -max-seconds 120 -max-tokens 150000 -unpriced \
  -toolkit-mode direct -diagnostic-read \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```
