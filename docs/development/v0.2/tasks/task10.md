---
lang: en
title: "OKF v0.2 domain model in bundle"
permalink: /development/v0.2/tasks/task10/
---

{% include nav.html %}

# OKF v0.2 domain model in bundle {#section001}

> Historical document. This plan is preserved from source revision `61e75e9`; it is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task10.md).

## 1. Objective {#section002}

Make `bundle` the domain reading layer for provenance, trust, lifecycle,
Attested Computation, keyed attribution, and referenced assets. Preserve
permissive conformance, BYOT, and lossless round-tripping of unknown keys.

Dependency: [`task09.md`]({{ '/development/v0.2/tasks/task09/' | relative_url }}).

## 2. Current gaps {#section003}

- `bundle.OKFVersion == "0.1"`.
- Typed accessors cover legacy `timestamp`, but not v0.2 field families.
- New standard keys are treated as extensions.
- `Document.Citations()` understands only positional `# Citations`.
- `Bundle.Load` does not retain non-Markdown `.sql`/`.py` assets.
- The generic semantic subresource index treats `sources[].id` as a fragment.
- The relations extension does not exclude standard v0.2 field families.

## 3. Public model {#section004}

Add small, open reading models:

- `Generation`;
- `Verification`;
- `UsageWindow`;
- `ProvenanceSource` (`Source` is already used by a storage interface);
- `TrustTier`;
- lifecycle constants;
- `ComputationParameter`;
- `ExecutorContract`;
- `AttesterContract`;
- `AttestedComputationContract`.

Actor, runtime, parameter type, and receipt field names remain open strings.
Do not introduce closed registries/enums.

Add accessors:

- `Sources`, shared/effective `UsageWindow`;
- `Generated`, `Verifications`;
- derived `TrustTier`;
- raw/effective `Status`;
- `StaleAfter` and a helper with an explicit reference date;
- `AttestedComputation`;
- effective content-change time.

Normalize a bare `verified` mapping to a one-element slice without changing the
raw YAML.

## 4. BYOT and lossless contract {#section005}

- `Get`, `Set`, and `YAMLNode` remain authoritative.
- Add a generic decode/encode helper for caller-owned structs.
- Do not introduce a monolithic `FrontmatterSchema`.
- Accessors return copies and do not share mutable slices/maps with callers.
- Typed reads do not change node style, comments, key order, or nested extensions.
- Exclude standard v0.2 keys and legacy `timestamp` from `ExtensionKeys`;
  `relations` remains a project extension.

## 5. Attribution {#section006}

Add footnote references/definitions and `Document.Attributions()`:

- the join key is `sources[].id`;
- source order does not matter;
- ignore markers/definitions in fenced and inline code;
- unknown/duplicate IDs remain visible and are not silently filtered;
- a prose footnote definition does not replace a structured source;
- preserve the legacy `Citation` API.

## 6. Bundle assets and path values {#section007}

- `Load` retains all revision-visible regular files.
- `MarkdownFiles()` retains its existing semantics.
- Add `Files()`/`AssetFiles()` with defensive copies.
- `ReadFile()` reads captured `.sql`, `.py`, and `.json` files without further I/O.
- Preserve the no-follow/special-file/`.okf` security contract.
- Add a resolver for URLs, bundle-relative paths, and relative path values.
- Do not treat a scope descriptor as a local path.

## 7. Semantic extension collision {#section008}

- `sources[].id` is not `concept#fragment`.
- Standard family mappings do not become implicit relation sources.
- Duplicate source IDs do not produce `duplicate_fragment`.
- Continue indexing actual producer extension subresources.

## 8. Conformance and compatibility {#section009}

Do not expand `Document.ValidateConformance()`: its hard requirement remains
parseable frontmatter and a non-empty string `type`.

- An optional field family may be absent.
- A malformed optional family does not break `Bundle.Load`.
- Keep `Timestamp()` as a deprecated legacy accessor.
- `generated.at` is authoritative; a malformed present `generated` must not
  silently fall back to `timestamp`.
- No migration during reading/serialization.

## 9. Files {#section010}

- `bundle/doc.go`;
- `bundle/frontmatter.go`, new `frontmatter_v02.go`;
- `bundle/document.go`;
- `bundle/links.go` or new `attribution.go`;
- `bundle/bundle.go`;
- `bundle/semantic_index.go`;
- `bundle/relations.go`;
- package tests and canonical fixtures.

## 10. Tests {#section011}

All tests use AAA.

- Complete Appendix A.
- Mapping/list forms of `verified`.
- All trust tiers.
- Missing status → stable.
- Staleness boundary `today == stale_after`.
- Present `generated` without `at` does not fall back.
- Shared/per-source usage windows.
- Unknown/malformed nested shapes neither panic nor disappear.
- Footnote reorder/repeat/unknown/definition/code cases.
- Parse → typed read → serialize preserves unknown bytes/semantics.
- `sources[].id` does not appear in subresource/relation indexes.
- `.sql/.py` files are captured and revision-visible.
- Existing v0.1 tests do not regress.

## 11. Acceptance criteria {#section012}

- `bundle.OKFVersion == "0.2"`.
- The SDK reads all fields from §§5 and 10 without manual YAML traversal.
- BYOT and round-tripping of unknown extensions are demonstrated.
- Assets are available from the immutable loaded bundle.
- The legacy API remains operational.
- `go test ./bundle` and `go test ./...` pass.

## 12. Out of scope {#section013}

Execution, parameter binding, runtime receipts/verdicts, attester ABI/sandbox,
caching, and automatic migration.
