---
layout: default
lang: en
title: "Task 17: filesystem durability for OKF v0.2"
permalink: /development/v0.2/tasks/task17/
---

{% include nav.html %}

Historical implementation specification, preserved from revision `61e75e9`. Requirements describe the work planned at that time, not current usage instructions. [Original document](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task17.md).

# Technical specification: OKF v0.2 durability contract in `store/fs` {#section001}

## 1. Assessment {#section002}

`store/fs` stores opaque regular-file bytes and can already durably save v0.2 metadata and assets. Production changes specific to the specification are unnecessary; an explicit contract test corpus is required.

Dependencies: [`task10.md`]({{ '/development/v0.2/tasks/task10/' | relative_url }}), [`task11.md`]({{ '/development/v0.2/tasks/task11/' | relative_url }}), [`task16.md`]({{ '/development/v0.2/tasks/task16/' | relative_url }}).

## 2. Fixture {#section003}

Create a bundle containing:

- root `okf_version: "0.2"`;
- nested sources/generated/verified/lifecycle;
- Attested Computation;
- `references/computations/*.sql`;
- `references/attesters/*.py`;
- an executor `.md` file.

## 3. Tests {#section004}

All tests use AAA.

- Snapshot `Paths`/`ReadFile` preserve metadata and assets byte for byte.
- Any metadata or asset byte change changes the revision.
- An index migration that changes only the version changes the revision.
- Failure after durable journal synchronization → reopen/recovery → exact post-transaction state.
- Commit receipt replay is idempotent.
- Tampered, missing or symlinked staged payload → storage corruption, with no partially visible write.
- v0.1 fallback and the final v0.2 state pass staged validation.
- The existing Darwin/Linux and unsupported-platform matrix continues to pass.

Do not duplicate generic large-payload, provenance or no-follow tests without adding coverage for a new v0.2 contract.

## 4. Boundary {#section005}

- Keep journal and receipt schema versions unchanged.
- Do not store runtime attestation receipts.
- `.okf/**` remains private and invisible to revision computation.
- An asset-only change must at least appear in `ChangedFiles`; reverse semantic `ChangedRefs` for computation dependencies is a separate graph/store extension, excluded until a public asset mutation operation exists.

## 5. Acceptance criteria {#section006}

- Crash/recovery supports an Appendix-style v0.2 bundle byte for byte.
- The transaction protocol is identical for v0.1/v0.2.
- `go test ./store/fs` and the platform matrix pass.
