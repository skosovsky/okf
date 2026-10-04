---
lang: en
title: "Version-aware OKF v0.2 validator"
permalink: /development/v0.2/tasks/task11/
---

{% include nav.html %}

# Version-aware OKF v0.2 validator {#section001}

> Historical document. This plan is preserved from source revision `61e75e9`; it is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task11.md).

## 1. Objective {#section002}

Add version-aware base/strict policy for OKF v0.1 and v0.2 without expanding
hard conformance beyond §11.

Dependencies: [`task09.md`]({{ '/development/v0.2/tasks/task09/' | relative_url }}), [`task10.md`]({{ '/development/v0.2/tasks/task10/' | relative_url }}).

## 2. Version policy {#section003}

| Target | Base | Strict |
| --- | --- | --- |
| `0.1` | existing contract | legacy `timestamp`/`# Citations` |
| `0.2` | §11 | v0.2 field families/computation guidance |
| absent | v0.2 + §13 fallbacks | validate present field families |
| future | best-effort | only the safe, known subset |

Version resolution must be a shared domain API, not a validator-local heuristic.

## 3. Severity contract {#section004}

`ERROR`:

- invalid/missing concept frontmatter;
- missing/empty string `type`;
- invalid reserved file structure.

`WARNING`, strict/policy only:

- malformed present v0.2 field family;
- source-footnote integrity;
- malformed lifecycle/freshness;
- unusable Attested Computation contract;
- staleness boundary.

Missing optional families, unknown types/keys/runtimes, and broken resources/paths
are not conformance errors.

Each new diagnostic receives a stable `Code`, a file, and an exact field path
such as `sources[2].resource`.

## 4. Strict field families {#section005}

### Sources {#section006}

- `sources` is a sequence of mappings.
- `resource` is required and is a non-empty scalar in every entry.
- Check `id`, `title`, and `author` when present.
- IDs are unique as a toolkit attribution policy.
- `usage_count` is a non-negative integer under the toolkit profile.
- The effective `usage_window` has `from/to`, valid dates, and `from <= to`.
- `usage_count` requires an effective window.
- Source footnotes join by ID; ignore code fences.
- `sources[].resource` may be a scope descriptor.
- Check `sources[].author` only as a non-empty string because of the upstream
  `team:*` ambiguity.

### Generated/verified {#section007}

- `generated` is a mapping; `by` is required inside a present mapping;
  `at`, when present, is RFC3339.
- Apply the actor convention to `generated.by`/`verified.by`.
- `verified` accepts a mapping or a sequence.
- Each event contains `by/at`; normalize a bare mapping.
- The trust tier is derived, never stored.
- Fall back to legacy timestamp only when `generated` is entirely absent.

### Lifecycle {#section008}

- status: `draft|stable|deprecated`, absent → stable.
- `stale_after`: valid `YYYY-MM-DD`.
- Inject the reference date; `today == stale_after` means stale.

### Path-valued fields {#section009}

Support absolute URLs, bundle-relative paths, and relative paths for:

- `resource`;
- `computation`;
- `executor.resource`;
- `attester.resource`.

Do not check the network/target existence as a conformance requirement.

## 5. Attested Computation {#section010}

For the exact type:

- `runtime` is non-empty; unknown values are allowed;
- parameters are mappings `{name,type,required}`, with unique names and boolean
  `required`;
- computation is supplied inline or by path, but not both;
- inline mode has one fence under top-level `# Computation`;
- check executor/attester shapes when present;
- receipt consists of unique, non-empty field names.

Missing runtime/computation/executor/attester remain strict warnings, not base
errors. Do not validate the deferred ABI.

## 6. Legacy fallback {#section011}

- Explicit v0.1 preserves the existing strict rules.
- A v0.2 consumer reads timestamp/Citations only when the replacement is absent.
- Numeric `[1]` in explicit v0.2 does not automatically enable legacy policy.
- Fallback does not rewrite the document or produce a warning merely for a legacy
  form.

## 7. Fixtures/tests {#section012}

Add groups `v02/positive`, `v02/strict`, `v02/compat`,
`v02/adversarial`.

Cover:

- a minimal type-only concept;
- Appendix A;
- mapping/list forms of verified;
- local/relative/scope resources;
- malformed shapes for each family;
- duplicate IDs/orphan footnotes/code fences;
- actor cases;
- invalid timestamps/dates/windows;
- staleness before/equal/after the boundary;
- parameter/computation variants;
- declared 0.1, absent, 0.2, future;
- YAML implicit timestamps/dates;
- malformed optional family → warning, `ErrorCount == 0`.

All tests use AAA; output is deterministic.

## 8. Acceptance criteria {#section013}

- Official v0.2 examples conform to base rules.
- The canonical Appendix fixture has no strict diagnostics.
- v0.1 regression fixtures pass.
- A type-only document remains conformant.
- Bare verified equals a one-item list.
- No false warning for a missing `timestamp` in v0.2.
- Codes/field paths are stable.
- `go test ./validator ./bundle` and `go test ./...` pass.

## 9. Out of scope {#section014}

Execution, receipt/verdict ABI, attester runtime/sandbox/cache, runtime-specific
binding, network checks, and migration.
