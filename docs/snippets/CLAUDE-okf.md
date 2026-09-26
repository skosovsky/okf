# Repository knowledge workflow (copy into CLAUDE.md)

Before changing code, read `knowledge/index.md` and only the concepts relevant to the task. Check their claims against the linked source contract or current code; a concept is a navigation aid.

At the start of a work session, capture an upkeep baseline if the repository uses `okf-upkeep`. After changing code, review the affected concepts. Update the concepts and `knowledge/log.md` when their claims changed. Otherwise, record an explicit reason why the current code changes do not affect the bundle, bound to the current upkeep fingerprint. Do not present a changed log or a successful validator run as proof that a concept is true. Do not infer `verified` or freshness metadata from Git history.

Use the repository's Go validation command after editing the bundle. Migration is a separate explicit operation.
