---
type: Attested Computation
title: File computation
description: An inert Attested Computation contract referencing a bundled SQL asset.
tags: [synthetic, computation]
status: stable
runtime: postgres
parameters:
  - { name: account_id, type: string, required: true }
computation: ../references/account-balance.sql
executor:
  resource: ../references/executor.py
  receipt: [query_id, executed_sql, result]
attester:
  resource: ../references/attester.py
generated: { by: "process:fixture-export", at: 2026-07-01T09:15:00Z }
verified:
  - { by: "process:fixture-check", at: 2026-07-02T10:15:00Z }
  - { by: "human:fixture-reviewer", at: 2026-07-03T11:00:00Z }
stale_after: 2099-01-01
---

# File computation

The computation is referenced as an inert bundle asset. This fixture does not
define parameter binding, execution, receipt, verdict, attester, or sandbox ABI.
