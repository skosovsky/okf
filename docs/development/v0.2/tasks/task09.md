---
lang: en
title: "OKF v0.2 contract baseline and migration roadmap"
permalink: /development/v0.2/tasks/task09/
---

{% include nav.html %}

# OKF v0.2 contract baseline and migration roadmap {#section001}

> Historical document. This plan is preserved from source revision `61e75e9`; it is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task09.md).

## 1. Objective {#section002}

Establish an executable contract for Open Knowledge Format v0.2 support before
changing public APIs. This task is the dependency root for the other migration
tasks.

Source of truth:

- upstream `okf/SPEC.md`, commit
  `3fcbb9f828c2f23d109c855ee403c3a4c81f3a96`;
- normative sections §§1–13;
- Appendix A as the canonical end-to-end example.

Do not rely solely on the current `main`: reproducible builds and tests must
reference the pinned specification revision.

## 2. Version contract {#section003}

Supported versions:

- `0.1` — legacy reading/validation and migration source;
- `0.2` — current default reading/writing contract;
- an unknown future version — best-effort consumption without rejection solely
  because of the `okf_version` value.

Resolution policy:

1. An explicit supported `okf_version` in the root `index.md` sets the contract.
2. Without a declaration, the effective version is `0.2`, with the legacy
   fallbacks from §13 permitted.
3. A v0.2 consumer reads an unknown declaration on a best-effort basis and
   preserves it as the declared version.
4. An explicit API/CLI selector is an assertion. A conflict with the declaration
   must not silently change semantics.

The resolution model must distinguish:

- declared version;
- effective version;
- source: `declared`, `default`, `explicit`, `future-best-effort`;
- native/legacy/best-effort compatibility.

The Go module, CLI binary, and plugin package versions are not `okf_version`.

## 3. Conformance matrix {#section004}

Create a versioned matrix: `spec clause → implementation → diagnostics → tests`.

Hard `[ERROR]` diagnostics remain limited to §11:

- parseable UTF-8 Markdown/YAML frontmatter for concepts;
- a non-empty string `type`;
- the structure of reserved `index.md` and `log.md` files.

Optional v0.2 field families do not become hard requirements:

- `sources`, `usage_window`;
- `generated`, `verified`;
- `status`, `stale_after`;
- computation fields.

The strict/policy layer checks malformed field families when present; they must
not break permissive loading. Unknown keys/types/runtimes remain consumable.

## 4. Legacy contract {#section005}

Support two fallbacks without implicit migration:

- use `timestamp` only when `generated` is entirely absent;
- read `# Citations` only when `sources` is absent.

A legacy representation alone is not a conformance failure.
Reading, `fmt`, `index`, and graph operations must not rewrite v0.1 documents.

Migration must be a separate preview/apply operation with an explicit actor and
source mapping.

## 5. Ambiguity ledger {#section006}

Record tooling policy decisions separately from the upstream specification:

- `sources[].author` references the actor convention, but upstream examples use
  the undocumented `team:*`; validate it as a non-empty string;
- the grammar and uniqueness of `sources[].id` are undefined; uniqueness is a
  toolkit strict policy for safe attribution joins;
- `sources[].resource` may be a scope descriptor rather than a path/URI;
- the timezone for `today >= stale_after` is undefined; tooling accepts an
  explicit reference date, and the CLI default is documented separately;
- `usage_count` is not a score;
- runtimes and parameter types have no registry;
- executor/attester requirements are weaker than the `runtime` requirement and
  remain strict guidance;
- simultaneous legacy/v0.2 provenance has no upstream precedence/deduplication
  rule;
- YAML `relations` is a `skosovsky/okf` extension, not part of upstream v0.2.

Reflect each decision consistently in bundle, validator, CLI, MCP, fixtures,
and documentation.

## 6. Canonical fixtures {#section007}

Add one shared corpus:

- a minimal v0.2 concept with only `type`;
- the complete Appendix A;
- inline and file-backed Attested Computation;
- mapping/list forms of `verified`;
- declared and undeclared v0.1;
- mixed legacy/v0.2;
- an unknown future version;
- adversarial shapes and unknown extensions.

Reuse fixtures in package tests, CLI/MCP E2E tests, and documentation snippets.

## 7. Deferred ABI {#section008}

Explicitly exclude:

- executor runtime;
- parameter binding implementation;
- receipt/verdict wire format;
- attester ABI, portability, and sandbox;
- attestation caching;
- execution of bundle content.

`store.CommitReceipt` is unrelated to `executor.receipt`.

## 8. Acceptance criteria {#section009}

- The contract and ambiguity ledger are recorded in versioned documentation.
- A clause-level conformance matrix and canonical fixture corpus exist.
- All downstream tasks reference the same version policy.
- Tests cover v0.1 fallbacks and future best-effort consumption.
- No task invents the deferred runtime ABI.
- `go test ./...` and `git diff --check` pass after adding the contract corpus.

## 9. Dependency graph {#section010}

Order:

1. This task.
2. `bundle`.
3. In parallel: `validator`, `graph`, mutation engine, store guardrails.
4. Mutation operations and migration.
5. CLI and MCP.
6. Skills/documentation and the final conformance gate across all interfaces.
