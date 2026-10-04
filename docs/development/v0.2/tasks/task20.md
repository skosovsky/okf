---
lang: en
title: "Technical specification: OKF v0.2 in okf-mcp"
permalink: /development/v0.2/tasks/task20/
---

{% include nav.html %}

> Historical document from source revision `61e75e9`. This implementation plan is archived for reference and is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task20.md).

# Technical specification: OKF v0.2 in `okf-mcp` {#section001}

## 1. Goal {#section002}

Make MCP a schema-first v0.2 adapter with rich structured outputs and safe
preview/apply edits while preserving the legacy five-tool text contract.

Dependencies: [`task09.md`]({{ '/development/v0.2/tasks/task09/' | relative_url }})–[`task18.md`]({{ '/development/v0.2/tasks/task18/' | relative_url }}).

## 2. Checked-in schemas {#section003}

Add `internal/mcpserver/contracts/*.schema.json` for:

- common/version/error envelopes;
- list/read/validate/graph;
- legacy write result;
- concept patch preview/apply;
- migration preview/apply.

JSON Schema is the source of truth:

- tools register raw input/output schemas;
- server output validation is enabled;
- objects are closed with `additionalProperties: false`, except explicit extensions;
- dates/timestamps/enums/limits are fixed;
- arrays are not serialized as `null`;
- success returns structuredContent and a legacy text fallback.

The stable error envelope contains `code`, `message`, `retryable`, and diagnostics.

## 3. Existing tools compatibility {#section004}

### `list_concepts` {#section005}

Add input `as_of`. The structured result includes:

- declared/effective version and compatibility;
- status/trust/staleness/generated time/source count;
- Attested Computation/runtime marker;
- explicit legacy-derived marker.

### `read_concept` {#section006}

Preserve raw Markdown text. The structured result contains:

- lossless `frontmatter_yaml`;
- body;
- typed projection of known v0.2 fields;
- legacy fallback flags.

Do not convert unknown YAML into a lossy closed JSON model.

### `validate_bundle` {#section007}

Inputs:

- `target_version: auto|0.1|0.2`;
- explicit `as_of`.

Output:

- declared/effective/resolution/compatibility;
- conformance separate from guidance;
- counts and stable diagnostic code/field/specification reference.

### `get_semantic_graph` {#section008}

The text fallback remains graph package output; structured content is a parsed
document according to the task12 profile. MCP does not build its own graph model.

### `write_concept` {#section009}

Preserve the whole-document escape hatch, but make validation version-aware.
Canonical request identity includes domain/version policy. Do not add a caller-controlled
idempotency key.

## 4. Safe edit tools {#section010}

Add:

- `preview_concept_patch`;
- `apply_concept_patch`.

Operations map only to task14 domain operations. Preview returns:

- applicable/noop/rejected;
- base/result revision;
- plan digest;
- bounded diff;
- affected paths;
- diagnostics;
- confirmation of unknown content preservation.

Apply requires `expected_revision` and `expected_plan_digest`, rebuilds the plan, and
publishes through store CAS/journal. Raw filesystem writes are prohibited.

## 5. Migration tools {#section011}

Add:

- `preview_v02_migration`;
- `apply_v02_migration`.

They are adapters for the task15 planner:

- explicit actor when creating generated;
- safe legacy timestamp policy;
- citation extraction only with explicit mapping;
- manual actions/blockers for ambiguity;
- one transactional multi-file apply;
- idempotent replay.

## 6. Security {#section012}

- resource/computation/executor/attester values are inert data.
- MCP does not fetch from the network or execute code.
- Actor metadata is not authentication.
- Preserve absolute-root/no-follow/path containment/CAS boundaries.
- Add bounds on input bytes/items/nesting/diff.
- Errors do not reveal paths outside the root.
- Cancellation between parse/validate/preview/commit leaves zero changes.

## 7. Files {#section013}

- `internal/mcpserver/server.go`;
- `tools.go`, `write.go`;
- new `patch.go`, `migration.go`;
- checked-in schemas;
- protocol/tools/contracts/migration/adversarial tests;
- docs after the contract stabilizes.

## 8. Tests {#section014}

All tests follow AAA.

- Advertised schemas/output validation/additionalProperties.
- Old arguments/text payloads remain byte-compatible.
- Structured outputs are schema-valid.
- All trust tiers, bare/list verified, stable/stale boundary.
- v0.1/v0.2/absent/future/override.
- Deterministic arrays/diagnostics.
- Unknown YAML/comments/body preserved.
- Flow/alias/duplicate/ambiguous edits fail closed.
- Revision conflict/plan mismatch/replay/concurrent writers.
- Migration all-or-nothing/recovery.
- Symlink/path traversal/special/UTF-8/resource limits.
- Malicious executor/attester values remain inert.
- Cancellation leaves the bundle unchanged.

## 9. Acceptance criteria {#section015}

- Existing five tools and text contract remain compatible.
- Every success has validated structuredContent.
- v0.2 signals are available in list/read/graph.
- Validation reports version/compatibility/as_of.
- Patch/migration are available only as preview → digest/revision-bound apply.
- No tool executes/fetches computation resources.
- The MCP package tests and full suite pass.

## 10. Out of scope {#section016}

Execute/attest tools, receipt/verdict protocol, attester ABI/sandbox/cache.
