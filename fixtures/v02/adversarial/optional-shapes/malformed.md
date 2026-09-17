---
type: Attested Computation
sources:
  - id: duplicate
    resource: ""
    usage_count: -1
  - id: duplicate
    resource: { unexpected: mapping }
usage_window: { from: 2026-07-31, to: 2026-07-01, future: keep }
generated: broken
timestamp: 2020-01-01T00:00:00Z
verified:
  - { by: "", at: never }
  - unexpected
status: frozen
stale_after: tomorrow
runtime: 42
parameters:
  - { name: duplicate, type: string, required: yes, extension: keep }
  - { name: duplicate, type: custom, required: false }
computation: ./query.sql
executor:
  receipt: [result, result, ""]
attester: unexpected
x-unknown:
  nested: [must, survive]
---

# Computation

```sql
SELECT 1
```

Markers inside code are ignored:

```text
[^duplicate]
[^orphan]: not a real definition here
```

Outside marker.[^orphan]
