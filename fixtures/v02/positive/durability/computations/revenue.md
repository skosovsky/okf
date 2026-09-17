---
type: Attested Computation
title: Revenue for fiscal year
description: Recognized revenue for a fiscal year, per Finance policy.
tags: [finance, revenue]
status: stable
runtime: postgres
parameters:
  - { name: year, type: integer, required: true }
computation: ../references/computations/revenue.sql
executor:
  resource: ../references/executors/run-postgres.md
  receipt: [query_id, executed_sql, result]
attester:
  resource: ../references/attesters/sql-equality.py
generated: { by: reference_agent/gemini-2.5-pro, at: 2026-06-28T14:00:00Z }
verified:
  - { by: process:finance-nightly, at: 2026-06-29T02:00:00Z }
  - { by: human:finance-lead, at: 2026-06-29T09:00:00Z }
stale_after: 2026-12-31
sources:
  - id: rev-policy
    resource: https://wiki.example/finance/revenue-recognition
    title: Revenue recognition policy
    author: team:finance-fpa
    last_modified: 2026-04-02
  - id: sanctioned-sql
    resource: ../references/computations/revenue.sql
    title: Reviewed revenue query
    author: process:finance-build
    usage_count: 5000
    last_modified: 2026-06-18
    usage_window: { from: 2026-06-15, to: 2026-06-30 }
usage_window: { from: 2026-06-01, to: 2026-06-30 }
---

# Computation

The sanctioned computation is stored byte-exact in
`../references/computations/revenue.sql`.[^sanctioned-sql] It implements
the revenue policy.[^rev-policy]

[^rev-policy]: Revenue recognition policy
[^sanctioned-sql]: Reviewed revenue query
