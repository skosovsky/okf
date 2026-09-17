# ADR 0002: Additive semantic operation tags in ChangeSet format v1

- Status: Accepted
- Date: 2026-07-29

## Context

`store.ChangeSet` format v1 is the canonical request envelope used for request
digests, journal binding, receipt replay, and idempotency. OKF v0.2 adds narrow
semantic operations, but does not change the transaction protocol or the
meaning of existing operations and preconditions.

Changing the format version solely because the closed operation union gained
new members would invalidate existing request digests and force every store
backend to introduce a second envelope decoder without gaining a distinct wire
contract.

## Decision

ChangeSet format remains version `1`. New operation kinds receive unique,
lowercase canonical tags within the existing length-prefixed operation slot:

- `set_generated`
- `ensure_verification`
- `remove_verification`
- `put_source`
- `remove_source`
- `set_usage_window`
- `set_lifecycle`
- `put_attested_computation`
- `set_bundle_version`
- `migrate_v01_to_v02`

Tags are allocated once and are never reused. Every field following a tag is
encoded with the existing deterministic v1 primitives: fixed-width unsigned
lengths, length-prefixed strings or bytes, explicit presence bits, and
order-sensitive lists.

The v1 envelope is unchanged:

1. domain marker;
2. format version;
3. change-set ID;
4. transaction actor;
5. base revision;
6. operation count and operations;
7. precondition count and preconditions.

The public Go union accepts only the documented non-pointer concrete values.
Pointers, typed nils, and unknown variants are rejected before validation or
canonical dispatch. A decoder or adapter that encounters an unknown canonical
tag must reject the request as unsupported; it must not skip, reinterpret, or
execute the payload.

## Compatibility

Canonical bytes and request digests of every pre-v0.2 operation and
precondition remain byte-for-byte unchanged. Adding a new tag changes only
requests that explicitly contain that new operation. ChangeSet v1 therefore
continues to bind previews, commits, journals, and receipts through one
protocol.

Frozen legacy vectors and per-operation field-sensitivity tests are executable
compatibility guards. A tag rename, tag reuse, field reorder, implicit
optional-field encoding, or change to a legacy primitive is a breaking change.

## Future version bump criteria

Increment `ChangeSetFormatVersion` only when at least one of these changes is
required:

- the envelope fields, their order, or their encoding changes;
- an existing tag or field changes meaning;
- an existing operation needs an incompatible field layout;
- unknown variants become skippable or otherwise gain forward-compatible
  framing absent from v1;
- canonical normalization rules change in a way that alters existing digests;
- transaction, journal, receipt, or idempotency binding semantics diverge.

A future version must use new golden vectors and an explicit backend migration
plan. Merely allocating another unique closed-union tag is not sufficient
reason to bump the format.
