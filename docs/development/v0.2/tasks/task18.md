---
layout: default
lang: en
title: "Task 18: journal decoding and payload limits"
permalink: /development/v0.2/tasks/task18/
---

{% include nav.html %}

Historical implementation specification, preserved from revision `61e75e9`. Requirements describe the work planned at that time, not current usage instructions. [Original document](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task18.md).

# Technical specification: align journal decoding with payload limits {#section001}

## 1. Problem {#section002}

`Config.Validate` permits enlarged limits up to absolute ceilings. The writer and staged reader use the effective Config, but the journal decoder hardcodes the default 256 MiB/1 GiB limits. A valid large-asset transaction can commit yet fail to recover after a crash with the same configuration.

The problem was found during the audit of v0.2 computation assets but is a general durability bug.

## 2. Requirements {#section003}

Choose and document one contract:

1. The decoder checks the manifest against immutable absolute ceilings without payload I/O, then `readStagedJournal` applies the effective Config; or
2. The decoder accepts explicitly supplied immutable effective limits.

Do not:

- weaken overflow, ordinal or tamper checks;
- read payload before manifest, provenance and limit validation;
- change the journal v5 wire format unnecessarily;
- allocate hundreds of MiB merely for a test fixture.

## 3. Files {#section004}

- `store/fs/fs.go`;
- `store/fs/staged_payload_limits_test.go`;
- a focused recovery integration test.

## 4. Tests {#section005}

All tests use AAA.

- A manifest above defaults but below the absolute ceiling passes the decoder stage.
- A smaller configuration on reopen rejects recovery before payload I/O.
- The same enlarged configuration recovers successfully.
- Aggregate and size overflow are rejected.
- Ordinal, tamper and missing-payload contracts do not regress.
- Use a bounded unit fixture instead of a real 256+ MiB allocation.

## 5. Acceptance criteria {#section006}

- The writer and recovery accept the same valid configured transaction.
- A smaller policy safely blocks recovery before payload reads.
- The journal wire version is unchanged.
- `go test ./store/fs` passes.
