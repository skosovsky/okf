---
title: "Knowledge upkeep"
description: "Knowledge upkeep"
permalink: /knowledge-upkeep/
---

{% include nav.html %}

<span id="optional-knowledge-upkeep-check"></span>

# Review knowledge after a change {#page-top}

When code or requirements change, review related notes. For example, changing delivery from 24 to 48 hours requires updating the source and rule, as in the quickstart. `okf-upkeep` helps record that this review happened.

## Prerequisites {#prerequisites}

You need a Git repository, Go, and a `knowledge/` bundle. An agent can use the separate `okf-maintain` skill; portable AGENTS.md and CLAUDE.md instructions are in the document library. Run the checker explicitly; it does not install client hooks.

## Set the policy {#policy}

Create `upkeep.json` at the repository root, outside the OKF root index:

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

This example fits this repository; replace the directories for your project. `repo_root` resolves relative to the configuration file and must be the Git root. Paths in both lists are relative; exclusions take precedence. Exclude generated outputs from relevant paths.

## Capture a baseline and do the work {#baseline}

Before editing:

```sh
session_id=$(go run ./cmd/okf-upkeep baseline --config upkeep.json --out /tmp/okf-upkeep-baseline.json)
```

After changing code or requirements:

```sh
go run ./cmd/okf-upkeep check --config upkeep.json --baseline /tmp/okf-upkeep-baseline.json --session "$session_id"
```

The checker examines working-copy changes and commits since the baseline, including new, deleted, and renamed files. Spaces and newlines in filenames are supported. Committing initially dirty files without changing bytes or executable mode is not itself a new edit; new commits are compared with the baseline even after a working-copy revert. The baseline is bound to the repository, HEAD, session, and capture time; `max_session_minutes` limits its lifetime.

## Record the decision {#decision}

With no relevant changes the result is `no_relevant_change`. When review is needed, `needs_review` includes a `fingerprint`. Read the current requirement, code, and affected notes, then update a note or explain why it is unaffected. Example `decision.json` for the latter:

```json
{"fingerprint":"<fingerprint from check>","kind":"unaffected","reason":"Only test fixture names changed; the documented APIs and behavior are unchanged."}
```

If a note was updated:

```json
{"fingerprint":"<fingerprint from check>","kind":"updated","updated_concepts":["knowledge/architecture.md"]}
```

The named note must actually change since the baseline; a `knowledge/log.md` edit alone is insufficient. Store the decision outside the repository or exclude its path. After the final edit run:

```sh
go run ./cmd/okf-upkeep check --config upkeep.json --baseline /tmp/okf-upkeep-baseline.json --session "$session_id" --decision /tmp/decision.json
```

Expect `reviewed_updated` or `explicit_unaffected`. If code changes after the decision, its `fingerprint` becomes stale and the result returns to `needs_review`. This records a review action; check the correctness of the decision separately.

## Modes and failures {#troubleshooting}

In `advisory`, `needs_review` exits 0; explicit `blocking` exits 2. Configuration and usage errors exit 1. A Git or input failure returns `unavailable`: `open` permits continuation; `closed` blocks only in `blocking` mode. `--override "reason"` and `--attempt 2` provide explicit per-call exceptions recorded in JSON. Git and content-reading timeouts are configured, up to 60 seconds.

For clients supporting Stop hooks, `--adapter claude-stop` reads JSON from stdin, uses boolean `stop_hook_active`, ignores other fields, and returns `{}` when allowed or `{"decision":"block","reason":"..."}` when blocked, exiting 0. An already active Stop hook is allowed. Codex and CI can call the ordinary JSON command directly.

Exact inputs and outputs: [config](https://github.com/skosovsky/okf/blob/main/upkeep/contracts/config.schema.json), [decision](https://github.com/skosovsky/okf/blob/main/upkeep/contracts/decision.schema.json), [result](https://github.com/skosovsky/okf/blob/main/upkeep/contracts/result.schema.json). [okf-maintain skill]({{ '/readings/skills/okf-maintain/SKILL/' | relative_url }}).
