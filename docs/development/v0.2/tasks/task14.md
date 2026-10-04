---
lang: en
title: "v0.2 semantic mutation operations"
permalink: /development/v0.2/tasks/task14/
---

{% include nav.html %}

# Technical specification: v0.2 semantic mutation operations {#section001}

> Historical document from source revision `61e75e9`. This plan records the work proposed at that revision; it is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task14.md).

## 1. Goal {#section002}

Add idempotent domain operations for standard OKF v0.2 families on top of the lossless engine. Do not provide callers with a raw YAML mutation escape hatch.

Dependencies: [`task10.md`]({{ '/development/v0.2/tasks/task10/' | relative_url }}), [`task11.md`]({{ '/development/v0.2/tasks/task11/' | relative_url }}), [`task13.md`]({{ '/development/v0.2/tasks/task13/' | relative_url }}).

## 2. Operations {#section003}

Add narrowly scoped canonical operations:

- `SetGenerated`;
- `EnsureVerification`;
- `RemoveVerification`;
- `PutSource`;
- `RemoveSource`;
- `SetUsageWindow`;
- `SetLifecycle`;
- `PutAttestedComputation`;
- `SetBundleVersion` for root `index.md`.

`PutAttestedComputation` atomically sets a consistent contract:

- `type: Attested Computation`;
- runtime;
- parameters;
- inline/path computation mode;
- executor resource/receipt;
- attester resource.

Do not expose generic `SetYAML`, `yaml.Node`, or raw key paths.

## 3. Bare/list verified {#section004}

- A single verification may retain a bare mapping.
- Adding a second event atomically converts the mapping into a sequence.
- The ensure operation is idempotent.
- Removal preserves a valid form and retains unknown nested keys.

## 4. Store canonical contract {#section005}

Each operation:

- has its own canonical tag;
- serializes every field with a length prefix;
- deep-copies caller-owned slices and maps;
- has a deterministic digest;
- preserves the digests of existing operations.

An ADR is required: additive tags in ChangeSet format v1 or a justified version bump. Do not increase the format version “just in case”.

Do not restrict `store.ChangeSet.Actor` to the v0.2 actor convention: it is the transaction principal. Document actors are validated within the corresponding operation.

## 5. Move/path integration {#section006}

Extend `MoveConcept` to rewrite statically resolvable bundle paths losslessly in:

- `sources[].resource`;
- `computation`;
- `executor.resource`;
- `attester.resource`.

Leave external URLs and scope descriptors unchanged. Preserve broken or unresolved values.

## 6. Validation gating {#section007}

Preserve the pipeline:

1. Validate the ChangeSet.
2. Apply operations to a clone.
3. Load the final staged bundle.
4. Run the version-aware validator.
5. Return a diagnostic-only preview on a blocking finding.

Operation preflight checks actors, dates, source selectors, and parameter uniqueness. Strict warnings do not block an ordinary commit unless they are explicit postconditions of the operation.

## 7. Files {#section008}

- `store/change.go`, `types.go`, `result.go`;
- `mutation/planner.go`;
- new operation handlers;
- canonicalization, digest, and deep-copy tests;
- preview contract tests.

## 8. Tests {#section009}

All tests follow AAA.

- Insert, update, and remove each family.
- Bare/list verified forms.
- Shared and per-source usage windows.
- Typed usage_count/required values.
- Inline and file computation.
- Path rewriting for local paths, external URLs, and scopes.
- Unknown nested extensions are preserved.
- A second application is idempotent.
- Caller slice mutation does not affect the request or digest.
- Duplicate selectors and keys fail closed.
- Rejected plans have no staging, writes, or renames.
- Existing operations retain their exact canonical digests.
- `go test -race ./mutation ./store`.

## 9. Acceptance criteria {#section010}

- Every v0.2 family has public desired-state operations.
- Unknown fields and comments outside touched spans are preserved.
- The attested contract cannot be left partially updated.
- Existing APIs and digests remain compatible.
- CLI/MCP can construct operations without raw YAML.

## 10. Out of scope {#section011}

Legacy migration, computation execution, binding, receipts/verdicts, attester runtime, and arbitrary asset execution.
