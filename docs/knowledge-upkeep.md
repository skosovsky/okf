# Optional knowledge upkeep check

The portable instructions for [AGENTS.md](snippets/AGENTS-okf.md) and [CLAUDE.md](snippets/CLAUDE-okf.md) ask an agent to read only relevant concepts and review them after a code change. They need no Python, Node, or host plugin. The separate [okf-maintain skill](https://github.com/skosovsky/okf/blob/main/skills/okf-maintain/SKILL.md) guides the baseline → concept review → decision → final fingerprint check workflow; this page describes the checker's exact options. The Go checker provides session evidence for that review. It does not decide whether a concept is factually correct or set `status`, `trust`, or `verified`.

The policy belongs in a separate JSON file outside the OKF root index. For this repository, an example at the repository root is:

```json
{
  "repo_root": ".",
  "bundle_root": "knowledge",
  "relevant_paths": ["bundle", "validator", "store", "graph", "mutation", "internal", "cmd", "backfill", "viewer", "upkeep", "go.mod", "go.sum", "action.yml"],
  "exclude_paths": ["internal/generated"],
  "mode": "advisory",
  "failure_policy": "open",
  "timeout_ms": 5000,
  "max_session_minutes": 1440
}
```

Paths in `relevant_paths` and `exclude_paths` are clean repository-relative paths. An exclusion wins for that path. `repo_root` is resolved relative to the config file and must be the Git worktree root. Git status and committed diffs use NUL-delimited records, so spaces and newlines in names are supported. The checker compares tracked and untracked working-copy entries with a baseline and also compares commits made since that baseline. Committing already dirty content without changing its bytes or tracked executable mode does not count as a new edit; newly committed content is checked against the baseline even if the working copy later reverts. Deleted and renamed files are included. The baseline is bound to the repository, starting HEAD, state, session ID, and capture time; it expires after `max_session_minutes`. Place tooling output paths in `exclude_paths` when a relevant directory also contains generated files.

Capture the baseline before the work, then check afterward:

```sh
session_id=$(go run ./cmd/okf-upkeep baseline --config upkeep.json --out /tmp/okf-upkeep-baseline.json)
go run ./cmd/okf-upkeep check --config upkeep.json --baseline /tmp/okf-upkeep-baseline.json --session "$session_id"
```

The check returns `no_relevant_change`, `needs_review`, `reviewed_updated`, or `explicit_unaffected` in JSON. For a relevant change without a decision it returns `needs_review` plus a `fingerprint`. After reviewing current code and concepts, either edit at least one affected concept and record the decision as `updated`, or record a concrete reason why the bundle is unaffected:

```json
{"fingerprint":"<fingerprint from check>","kind":"unaffected","reason":"Only test fixture names changed; the documented API and behavior are unchanged."}
```

For an update, use `{"fingerprint":"...","kind":"updated","updated_concepts":["knowledge/architecture.md"]}`. The named concept must have changed since the baseline. An old `knowledge/log.md` edit or a fresh log edit alone is insufficient. Re-run `check --session "$session_id" --decision /path/to/decision.json` after the final code edit: a stale fingerprint returns `needs_review`. Keep the decision file outside the repository or exclude it from relevant paths. The result records a review action; it does not prove that the action was correct.

The default `advisory` mode always exits zero for `needs_review`. Explicit `blocking` mode exits 2 for that state. Configuration and usage errors exit 1. A Git or input failure returns `unavailable`; `failure_policy: open` allows it, while `closed` blocks only in blocking mode. A per-invocation `--override "reason"` allows an otherwise blocked result. `--attempt 2` allows a second host invocation to avoid a stop loop. These controls are visible in result JSON. The Git command and content scan have the configured timeout, bounded to 60 seconds; choose a timeout appropriate to the repository size.

The optional `--adapter claude-stop` reads a JSON object from stdin with optional boolean `stop_hook_active`; other host fields are ignored. A blocking result emits `{"decision":"block","reason":"..."}` and exits zero, as expected by the Stop adapter. An allowed result emits `{}` and exits zero. A repeated Stop hook call (`stop_hook_active: true`) is allowed. The host-neutral adapter emits the [result schema](https://github.com/skosovsky/okf/blob/main/upkeep/contracts/result.schema.json) and uses exit code 2 for a blocked result. The [config](https://github.com/skosovsky/okf/blob/main/upkeep/contracts/config.schema.json) and [decision](https://github.com/skosovsky/okf/blob/main/upkeep/contracts/decision.schema.json) schemas define the other inputs. Configure the adapter only in a host that supports Stop hooks; Codex and CI can invoke the host-neutral JSON command directly. The checker does not install hooks or alter host settings.
