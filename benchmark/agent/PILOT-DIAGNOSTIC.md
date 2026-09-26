# Proposed file-read diagnostic (separate approval required)

This proposal tests the first realistic case only:
`repo-default-okf-version`, with the same control and treatment artifacts as
`corpus/primary_realistic.json`. The single-case corpus is
`corpus/diagnostic_primary.json`, SHA-256
`15a2a6b0646acdbd18114b54f49f971fee512ad8dfec04aad9d34641820b0d95`.
Its source corpus SHA-256 is
`3e00dd9796e507555c184d4c2b68cd37bc53b0d8672c6e1b7e307eecb289544b`.
The current claim is pinned to `bundle/doc.go` (`OKFVersion = "0.2"`) and the
SPEC revision is `0b87c52c6ef999286c745e19998fdfcd03d5dbee`. The final
Go commit SHA must be filled from a clean checkout after this diagnostic code
is committed.
`metadata.prompt_sha256` covers the shared `Request.Instructions` only; the
new plan/report `prompt_hash_scope` records this explicitly. The corpus hash
and adapter binary hash pin the remaining prompt construction inputs/code,
but Codex-injected context is not separately hashed.

Both arms receive the same question, answer format, model settings, read-only
shell access, and explicit requirement to run a simple `cat` on every supplied
artifact. Treatment additionally receives the Go CLI instruction; that is the
intervention being tested. The adapter records at most 64 event metadata rows
per trial: event type, item type/status, exit code, command category and command
SHA-256. It does not retain command output, model text, or source in event
metadata. It confirms a read only when a successful single-file `cat` of a
supplied path returns exactly the complete artifact content. Safely quoted
bash/sh/zsh wrappers are recognized; compound commands are rejected. If
control fails to read all its files,
the runner records that row as an operational failure and stops before
treatment. Unknown event/command formats fail closed; the metadata should
show whether command events were emitted but not recognized.

The new run ID is `diagnostic-read-20260926-v1`. Hard limits are two calls,
120 seconds total and 45 seconds per call. `60,000` is an observed post-call
token stop condition: one call may exceed it. The prior first call alone used
15,016 tokens; shell round trips can cost more. The run is unpriced, so no USD
ceiling or worst-case monetary spend is asserted. Two complete trials would
be a tool-access diagnostic, not a statistically meaningful behavior result.

After explicit approval, rebuild both binaries from the clean commit and run:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go build -o /private/tmp/okf-eval-cli ./cmd/okf
GOCACHE=/private/tmp/okf-agent-gocache go build -o /private/tmp/okf-eval-codex-adapter ./benchmark/agent/cmd/codex-adapter
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
  -commit FULL_NEW_CLEAN_GIT_SHA \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 2 -max-seconds 120 -max-tokens 60000 -unpriced \
  -toolkit-mode direct -diagnostic-read \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```

Do not reuse the previous pilot's plan, rows, or authorization. If either
binary version/hash differs at preflight, stop and review the new pin before
any model call. Analyze raw rows against this same corpus and saved plan.
