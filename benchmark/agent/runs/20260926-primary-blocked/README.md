# Primary realistic pilot: blocked before execution

- Attempt time: 2026-09-26T10:10:03Z (UTC).
- Clean checkout: `f6ebfebac965e681fa43365d5075007767984e05`.
- Corpus: `benchmark/agent/corpus/primary_realistic.json` (four paired cases, eight intended model calls).
- Model: `gpt-6-luna`; version label: `gpt-6-luna`; settings: `{"reasoning_effort":"low"}`.
- Toolkit: direct, Codex read-only ephemeral shell with supplied Go CLI.
- Limits: eight trials, 360 seconds total, 45 seconds per trial, 12,000 observed tokens, unpriced.
- Go CLI SHA-256: `96c93890c25cba6e9d9ce75779eb4f9f52922f92598b6a55d29a63fcfd955e17`.
- Adapter SHA-256: `c405f6a976599db2fd7042660aab822074f64ea975f770e1a41a8f6267dc9f09`.
- Offline checks: `go test ./benchmark/agent/... -count=1` passed.

The requested escalated run was rejected by automatic approval review before
the process started. It said the pilot would send repository-derived benchmark
and source artifacts to an external model and might write Codex state outside
the workspace. Although the user had approved a bounded pilot, that approval
did not specifically authorize this sensitive payload to the external
destination. The review explicitly prohibited bypassing the rejection through
a workaround or indirect execution.

No plan file, raw rows, model responses, usage measurements or CLI traces were
created. The number of model calls in this attempt is zero. The pilot has no
performance result, and the exploratory failed smoke must not be treated as
one. A future run requires authorization that explicitly covers transmission
of these repository-derived source and benchmark artifacts to the external
Codex model and Codex's state writes outside the workspace.

Intended command (never executed):

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/primary_realistic.json \
  -plan /private/tmp/okf-primary-plan.json \
  -rows /private/tmp/okf-primary-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -go-cli /private/tmp/okf-eval-cli \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit f6ebfebac965e681fa43365d5075007767984e05 \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 8 -max-seconds 360 -max-tokens 12000 -unpriced \
  -toolkit-mode direct \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```
