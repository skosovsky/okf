---
type: Reference
title: Multiple sources
description: A synthetic claim with two keyed source footnotes.
tags: [synthetic, provenance]
sources:
  - id: policy
    resource: https://example.invalid/policy
    title: Synthetic policy
    author: "human:fixture-author"
    usage_count: 12
    last_modified: 2026-06-20
  - id: runbook
    resource: references/runbook.txt
    title: Synthetic runbook
usage_window: { from: 2026-06-01, to: 2026-06-30 }
generated: { by: "process:fixture-export", at: 2026-07-01T09:05:00Z }
verified: { by: "human:fixture-reviewer", at: 2026-07-02T10:00:00Z }
status: stable
stale_after: 2099-01-01
---

# Claim

The fixture policy defines the synthetic rule.[^policy] The fixture runbook
describes the synthetic response.[^runbook]

[^policy]: Synthetic policy.
[^runbook]: Synthetic runbook.
