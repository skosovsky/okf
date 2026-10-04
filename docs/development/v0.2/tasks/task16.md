---
lang: en
title: "Store compatibility guardrails for OKF v0.2"
permalink: /development/v0.2/tasks/task16/
---

{% include nav.html %}

# Technical specification: Store compatibility guardrails for OKF v0.2 {#section001}

> Historical document from source revision `61e75e9`. This plan records the work proposed at that revision; it is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task16.md).

## 1. Verdict {#section002}

The backend-neutral `store` is already specification-agnostic. No production API migration is needed. This task is a verification/no-op guardrail and is closed after integration.

## 2. Preserve unchanged {#section003}

- `Store`, `Snapshot`, `ChangeSet`;
- `ChangeSetFormatVersion`;
- `Preview`, `CommitReceipt`, `CommitReceiptFormatVersion`;
- the diagnostic wire shape;
- `store.Actor` validation.

New frontmatter families belong to `bundle`/`validator`/`mutation`.

## 3. Receipt boundary {#section004}

`store.CommitReceipt` is evidence of transaction durability. `executor.receipt` is an Attested Computation runtime artifact.

Do not:

- add executor receipts or verdicts to CommitReceipt;
- bump the format version because of OKF v0.2;
- turn store into an execution or attestation cache;
- restrict store.Actor to the actor convention for document metadata.

## 4. Guardrail tests {#section005}

All tests follow AAA.

- New validator codes pass through `Preview.Diagnostics` without losing code/file/severity/message.
- v0.1 and v0.2 bundles use the same transaction protocol.
- Document actors and legacy store actors are accepted; the whitespace/control/UTF-8 policy remains unchanged.
- Executor-like JSON is not accepted as a canonical transaction receipt.
- Existing ChangeSet/Receipt canonical bytes remain unchanged.

## 5. Acceptance criteria {#section006}

- Public interfaces and format versions remain unchanged.
- Integration `go test ./store ./mutation ./store/fs` passes.
- Documentation explicitly distinguishes the two receipts.

## 6. Out of scope {#section007}

Execution, attestation storage/cache, and a generic raw asset operation unless a separate migration contract requires one.
