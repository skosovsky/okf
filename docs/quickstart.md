---
title: OKF quickstart and offline demo
description: Create, inspect, maintain, and reconstruct an OKF bundle.
permalink: /quickstart/
---

{% include nav.html %}

# Quickstart

Run from a repository checkout with Go from `go.mod`, or replace `go run ./cmd/okf` with an installed `okf`. `okf-mcp` is optional and must be connected by the host before its tools appear. `okf-upkeep` needs Git; `okf-backfill` needs a Git history and a reviewed analyst proposal. Skills do not install binaries or grant host permissions.

## Create → check → open

```sh
demo_dir=$(mktemp -d)
go run ./cmd/okf init "$demo_dir/my-knowledge"
go run ./cmd/okf validate --path "$demo_dir/my-knowledge" --spec 0.2 --as-of 2026-09-26
go run ./cmd/okf view "$demo_dir/my-knowledge" --output "$demo_dir/my-knowledge.html" --spec 0.2 --temporal-profile date-3fcbb9f --as-of 2026-09-26
```

Open `my-knowledge.html` from disk. The generated concept is a draft placeholder: replace it with sourced knowledge before using it as evidence. `init` needs an absent target under an existing parent; `view` needs a new output path outside the bundle. Validation establishes conformance, not factual verification.

## Read an existing bundle

The repository's [`knowledge/`](https://github.com/skosovsky/okf/tree/main/knowledge) bundle attributes its claims to repository files. From this checkout:

```sh
go run ./cmd/okf info knowledge --spec auto --as-of 2026-09-26
go run ./cmd/okf validate --path knowledge --spec auto --strict --check-links --check-orphans --as-of 2026-09-26 --max-warnings=0
```

In an MCP-connected host, call **`validate_bundle` on the OKF server** with `{"bundle_path":"<absolute path to knowledge>","target_version":"auto","strict":true,"check_links":true,"check_orphans":true,"as_of":"2026-09-26"}`. The caller selects an accessible absolute bundle root; the server validates that root and uses containment/no-follow access inside it. Host permissions determine which root the caller may select. No universal client-side tool prefix is assumed. If the tool is unavailable, use the CLI commands above; if neither is available, label manual reading as unvalidated.

## Read and change through MCP

The server exposes thirteen tools. Use `search_concepts` for a literal query,
`read_concept` for the selected full concept, and `get_neighbors` for bounded
links; `total` and `truncated` disclose omitted search hits or edges. There is
no cursor in this contract. Narrow the query or direction/kinds, or use the
whole graph if it fits the output limit. A `resource_limit` error is a failed
call, not a partial page. Invalid inputs return a stable error code with
bounded field guidance; correct the named field instead of retrying unchanged.
See [query limits]({{ '/contracts/concept-queries/' | relative_url }}).

For edits, review the preview from `preview_concept_patch`,
`preview_v02_migration`, or `preview_temporal_upgrade`, then apply the matching
plan with its exact revision and digest. Preview does not grant authorization
to publish. A timeout after apply can have an uncertain outcome: inspect the
bundle and retry the same request before building a new plan. See the
[mutation trust boundary]({{ '/contracts/mcp-mutation-trust-boundaries/' | relative_url }}).

## Published viewer and offline export

[Open the published knowledge viewer]({{ '/demo/knowledge.html' | relative_url }}).
It is a static snapshot of this repository's public `knowledge/` bundle with
the explicit reference date `2026-09-26`. It has embedded CSS, JS, and data;
search and concept navigation do not fetch additional data. External source
links open only when clicked. The same HTML can be produced locally:

```sh
demo_dir=$(mktemp -d)
./scripts/build-viewer-demo.sh "$demo_dir/knowledge-demo.html"
```

The script validates and exports the bundle with the pinned date profile and
`as-of 2026-09-26`. Open its output offline; search for `Package boundaries`,
follow a concept link, and open `#architecture` directly. Inspect the source
list and the separate trust, status, and staleness fields. Missing `verified`
means `unverified`; `draft` is a lifecycle value; without `stale_after` the
viewer shows `unevaluated` even with an explicit reference date. See
[viewer behavior](https://github.com/skosovsky/okf/blob/main/viewer/README.md).
CI regenerates the HTML and compares it byte-for-byte with the published file.

## Maintain after a repository change

Before editing, follow the [`okf-maintain` procedure](https://github.com/skosovsky/okf/blob/main/skills/okf-maintain/SKILL.md): capture an `okf-upkeep baseline` with a JSON config, inspect code and relevant concepts, then run `okf-upkeep check` with that baseline and session ID. If it reports `needs_review`, update affected concepts or record a concrete `unaffected` decision bound to its fingerprint, then check again after the final edit. The [upkeep guide](https://github.com/skosovsky/okf/blob/main/docs/knowledge-upkeep.md) gives the config and exact commands. A green validator does not replace this review.

## Reconstruct from Git

Use [`okf-backfill`](https://github.com/skosovsky/okf/blob/main/skills/okf-backfill/SKILL.md) only with a real repository and an existing bundle. Extract a bounded manifest between explicit base/head commits, review each proposed claim against its recorded diff, prepare a plan, then explicitly apply and independently verify it. The [backfill protocol](https://github.com/skosovsky/okf/blob/main/backfill/PROTOCOL.md) defines the JSON artifacts and approval boundaries. `extract` alone proves no claim; a missing history or analyst proposal is a stop condition, not a reason to invent evidence.

This demo shows CLI/viewer behavior. It makes no model-routing, skill activation,
host compatibility, or benchmark-quality claim. Validation checks conformance,
not whether the displayed claims are factually true.
