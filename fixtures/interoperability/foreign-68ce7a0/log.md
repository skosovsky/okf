# Update Log

## 2026-09-21
* **Repo hygiene**: README rewritten around one thread (what OKF is, install per channel, use, what's inside, format, measured results as bullets with links, contributing, credits); the layout tree and duplicate install block are gone, counts and CI rules match the code, and the backfill's dependency on the plugin-only `agents/` is stated. Drift fixed alongside: `make test` runs the gate tests, CI's shipped and `.okf` regexes cover `agents/`, the inert `benchmark/trust/runs/` ignore rule is dropped, the root index links the backfill skill and a new concept for `okf_backfill_events.py`, and the backfill concept documents the two agents.
* **Release 0.9.6**: the vendored spec was resynced with upstream, which moved to
  `GoogleCloudPlatform/open-knowledge-format` (the old `knowledge-catalog/okf`
  location is a frozen snapshot) and made three date fields — `stale_after`,
  `sources[].last_modified`, `usage_window.{from,to}` — ISO 8601 datetimes with
  an explicit offset. The validator now accepts a datetime on all three (a bare
  date is still tolerated, no new warnings on existing bundles); the visualizer
  compares staleness as instants (`Date.parse(stale_after) <= Date.now()`)
  instead of a string compare, so a datetime value reads stale on the right
  instant instead of never matching; the backfill weaver's frontmatter contract
  now asks for the event's full ISO 8601 timestamp in `sources[].last_modified`
  instead of a truncated `YYYY-MM-DD`, retiring the workaround from the map-phase
