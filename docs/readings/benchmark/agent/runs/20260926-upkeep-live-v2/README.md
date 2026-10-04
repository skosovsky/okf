---
layout: default
lang: en
title: "Task 013: checker on/off writer → consumer v2"
permalink: /readings/benchmark/agent/runs/20260926-upkeep-live-v2/README/
documentation_id: benchmark-agent-runs-20260926-upkeep-live-v2-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav.html %}


> Historical report, 26 September 2026. This reading edition preserves the findings and limits of the original run; it is not current product guidance. [Original report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v2/README.md), revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="task-013-checker-onoff-writer--consumer-v2"></a>

# Task 013: checker on/off writer → consumer v2
{: #section-1}

This preregistered rerun completed with `valid: true`. The [plan](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v2/plan.json), [raw rows](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v2/rows.json), [audit](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v2/audit.json), [completion marker](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v2/audit.json.complete), and [report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v2/report.json) are retained. The same committed analyzer reproduces `report.json` byte for byte. The completion marker's row and audit hashes match the saved files. V2 was scored under the [frozen protocol]({{ '/readings/benchmark/agent/upkeeplive/LIVE-V2/' | relative_url }}); the earlier [v1 run]({{ '/readings/benchmark/agent/runs/20260926-upkeep-live-v1/README/' | relative_url }}) remains invalid under its original scoring contract.

| Preregistered outcome | Checker off | Checker on |
| --- | ---: | ---: |
| Concept document updated | 2/2 | 1/2 |
| Independent consumer correct by strict grader | 0/2 | 1/2 |
| Wrong | 1/2 | 1/2 |
| Ungradable due to unsupported citation | 1/2 | 0/2 |
| Operational failure | 0/2 | 0/2 |

These strict scores need case-level context. In `upkeep-reversal`, checker off updated `decision.md` to Mode B and its consumer answered “Mode B”, whereas the registered exact answer was `B`; the grader therefore marked it **wrong**, despite the matching meaning. Checker on left the document saying Mode A after the code changed to B, and its consumer answered Mode A. The checker still returned `needs_review` after the revision. In `upkeep-truncated`, both documents explain that the partial capture cannot establish rollout status, and both consumers answered “Insufficient evidence.” Checker on cited the valid artifact ID `handler` and scored correct. Checker off cited a prose description absent from the bundle's artifact IDs, so its otherwise apt answer scored **ungradable** under the preregistered citation policy. The checker-on final status `reviewed_updated` records a changed concept file, not independent proof that its content is right.

The audit records all four expected writer/consumer pairs, 10 model calls, 155,387 observed input-plus-output tokens, zero unknown-usage calls, 10 distinct model session IDs, and successful Go CLI validation of every final bundle. The plan SHA-256 is `98f16ab46d4241bf6a8cfb8dcd7f3ff45189021dee362d5c0fc65073b38adc7f`; row SHA-256 is `086005a2e123b126c2804d9d9b28b26241c97c9abea60b15bf24bf1d6f938707`; audit SHA-256 is `23ec5bcb8fc29f4a987da1b09958f66be0aa8dfc9350483da56e559ed3485eb9`. USD cost is unavailable.

This is a two-case descriptive result. The checker-on arm combines a review reminder, actual checker feedback, and an extra writer revision, so the experiment cannot isolate a checker effect. The strict 0/2 versus 1/2 consumer scores also understate the semantic quality of the checker-off answers because of exact-answer and citation formatting. These observations support regression cases and a clearer evaluation contract, not a claim that checker-on improves consumer quality.
