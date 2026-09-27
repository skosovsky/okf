# Metadata-only contrast — 2026-09-26

The preregistered [protocol](../../METADATA-CONTRAST.md) ran on clean commit
`dd8989ba36b8e27ac5762e2e0984cac7f88e8738`, with the pinned SPEC revision
`0b87c52c6ef999286c745e19998fdfcd03d5dbee`, `gpt-6-luna` at low reasoning
effort, and `corpus/metadata_cases.json` (SHA-256
`4fd2222f07b91cacda4e8f15b0b34792f41d962a151e2ddc95760b8329b1745a`).
The treatment adds OKF YAML frontmatter to the same answer-bearing bodies shown
to control. Both arms used the blinded adapter (SHA-256
`8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9`)
with no Go CLI projections. The [plan](plan.json), [raw rows](rows.jsonl), and
[report](report.json) preserve the completed run. The analyzer reproduces the
saved report byte-for-byte:

```sh
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go run \
  ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/metadata_cases.json \
  -plan benchmark/agent/runs/20260926-metadata-only-v2/plan.json \
  -rows benchmark/agent/runs/20260926-metadata-only-v2/rows.jsonl
```

All eight planned calls completed: four cases in each arm, one repeat, no
missing or invalid rows and no operational failures. The report has
`valid=true`. Control and treatment each answered **4/4 correctly**, with zero
stale, wrong, refusal or ungradable answers. The paired answers and `current`
citations are identical across arms: `PostgreSQL`, `No`, `Insufficient evidence`,
and `0.2`. Thus the measured difference in factual-error and completion rates
is zero in this corpus. Both arms made zero model tool calls.

Observed input plus output usage was **121,498 + 220 = 121,718 tokens** across
eight calls: 60,736 in control and 60,982 in treatment. Cached input tokens
were 26,112 and 53,248 respectively; these are reported separately and are
not extra calls. Summed model elapsed time was 46.001 seconds. The adapter did
not provide a USD price, so cost is unknown rather than zero.

This is a four-pair, single-repeat descriptive pilot. The unchanged outcomes
do not establish that OKF metadata lacks value elsewhere: ordinary text and
freshness clues already supported the correct answers here. The run does not
test autonomous CLI use, whether every supplied artifact was read, or a
general repository-level quality effect. The earlier v1 protocol had no model
observations and is not part of these denominators.
