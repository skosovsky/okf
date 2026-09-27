# Git history backfill protocol

`okf-backfill extract -repo PATH -base REF -head REF -out events.json` freezes
the two refs to commit IDs and emits events in causal order. The default merge
policy follows first parents and compares each merge commit to its first parent.
Set `-first-parent=false` to include side-branch commits; a merge event still
compares against its first parent, so the analysis must deduplicate repeated
changes. Git is the sole external executable. Commit text and paths are passed
as data, never interpolated into a shell.

Each event records all changed-file stats, an explicit skip reason for generated
or lock files, the byte count and SHA-256 of the complete included-file diff,
and a bounded diff prefix. Skipped file contents never enter the diff prefix.
Renames retain both paths and status; deletions retain the removed path; binary
files keep the Git binary marker and null line counts. Ranges over 100,000
events, commits over 10,000 changed files, and Git metadata over 32 MiB fail
closed with an explicit limit error and no partial manifest. A truncated
prefix is evidence only for visible lines; the
analyst must set `evidence_incomplete` when citing that event. The extractor
does not infer decisions, verify implementation, or read local chat histories.

The analyst reads `events.json` and writes `analysis.json` according to
`schemas/analysis.schema.json`. Each included event needs one disposition:
`considered` with candidate IDs or `rejected` with a reason. A candidate names
a persistent entity in `concept_id`, states its current claim in `body`, and
cites exact event and diff digests. The same entity may have several candidate
states. A successor names predecessor candidate IDs in `supersedes` and carries
a dated history note. Only the final current state is rendered. If causality
or current state is unclear, set `unresolved_conflict`; the writer omits all
states of that concept and reports the conflict for human review. Commit intent
is not evidence of completed work. Analysts must inspect code or other evidence
before asserting runtime behavior.

Go integrations implement `backfill.Analyzer` with their own model or review
provider. `RunAnalyzer` supplies the frozen manifest and validates the returned
analysis. The interface has no bundle write method; publication stays with the
single writer.

`okf-backfill plan -events events.json -analysis analysis.json -bundle DIR
-out plan.json` verifies the manifest and proposal, then returns proposed draft
documents without touching the bundle. `okf-backfill apply` uses the same
inputs plus `-plan plan.json -checkpoint checkpoint.json -out report.json`.
Keep the frozen plan for retries. The sole writer
uses store revision checks, idempotency keys, receipts, and durable recovery.
An interrupted run can be retried with the same inputs and checkpoint; changed
refs, event content, schema, prompt version, or analysis invalidate it.

Coverage reports that every extracted event has a disposition. It does not
measure whether claims are true. Acceptance requires an independent consumer
run from task 012 against the reconstructed bundle, with separate scores for
superseded decisions, incomplete evidence, and current code. Token usage and
cost are recorded only when an analyst supplies measured values.

The store publishes all active concepts and the root index in one journaled
transaction. A plan with only unresolved conflicts has no publishable concepts;
review its coverage report and resolve the conflicts before applying.
The CLI selects the upstream instant temporal profile because Git commit times
are RFC3339 datetimes. Nested concept IDs create and update their directory
indexes in the same transaction. Raw filesystem readers outside the store's
advisory root lock may observe its in-progress file replacements; cooperating
store readers see the recovered pre or post snapshot.
