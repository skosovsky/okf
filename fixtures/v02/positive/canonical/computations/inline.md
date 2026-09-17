---
type: Attested Computation
title: Inline computation
description: An inert Attested Computation contract with an inline computation.
tags: [synthetic, computation]
status: stable
runtime: postgres
parameters:
  - { name: account_id, type: string, required: true }
executor:
  resource: ../references/executor.py
  receipt: [query_id, executed_sql, result]
attester:
  resource: ../references/attester.py
generated: { by: "human:fixture-author", at: 2026-07-01T09:10:00Z }
verified: { by: "process:fixture-check", at: 2026-07-02T10:10:00Z }
stale_after: 2099-01-01
sources:
  - id: inline-policy
    resource: https://example.invalid/inline-policy
    title: Synthetic inline policy
---

# Computation

```sql
SELECT balance
FROM synthetic_accounts
WHERE account_id = :account_id
```

The declared computation follows the synthetic policy.[^inline-policy]

[^inline-policy]: Synthetic inline policy.
