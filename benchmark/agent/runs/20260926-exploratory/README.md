# Exploratory smoke: no model result

This two-trial smoke used one repo-derived case (`repo-default-okf-version`),
`gpt-6-luna` with low reasoning effort, a 120-second wall limit, a 20,000
observed-token limit, and at most two model calls. It ran against a dirty
worktree before the corrected implementation was committed. It is explicitly
invalid for task 012 acceptance.

The treatment arm invoked the real Go CLI three times (`validate`, then `parse`
for both concepts). The original treatment row records three tool calls.
Both model attempts failed before inference: Codex CLI could not write its
state database under the filesystem sandbox and then could not initialize its
app-server client. Therefore there are no factual observations, token usage,
or accuracy estimates.

`plan.original.json` and `rows.original.jsonl` are the exact files written by
the harness version used for this smoke. `plan.json` and `rows.jsonl` add fields
introduced after the smoke (`spec_sha256`, `adapter_sha256`, mode, exploratory
marker, zero/unknown trial elapsed, zero observed direct CLI calls, and empty
tool-output copies) so the current
analyzer can recount it. Those added values are **post hoc** and cannot be
treated as preregistered evidence. `report.json` is the resulting invalid
report. `corpus.json` is the frozen one-case input.

An escalated retry was rejected by automatic approval review because the
external model could access repository content and shell and write state
outside the workspace. Do not use a different home directory or another
provider to bypass that rejection. A live pilot needs explicit user approval
for the external model run, then a clean corrected commit and a new plan.
