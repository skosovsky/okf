---
layout: default
lang: en
title: "Task 013 live writer → consumer run (`upkeep-live-20260926-v1`)"
permalink: /readings/benchmark/agent/runs/20260926-upkeep-live-v1/README/
documentation_id: benchmark-agent-runs-20260926-upkeep-live-v1-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav.html %}


> Historical report, 26 September 2026. This reading edition preserves the findings and limits of the original run; it is not current product guidance. [Original report](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v1/README.md), revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="task-013-live-writer--consumer-run-upkeep-live-20260926-v1"></a>

# Task 013 live writer → consumer run (`upkeep-live-20260926-v1`)
{: #section-1}

The preregistered analysis **failed**: the committed `upkeepstudy analyze` command exited 1 because the checker-on consumer for `upkeep-truncated` cited `handler: The captured diff is partial, so rollout status cannot be established.` instead of the artifact ID `handler`. Its verbatim error is in [original-analysis-error.txt](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v1/original-analysis-error.txt). Therefore this run has **no valid primary result** under the frozen scoring contract. [report-posthoc.json](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v1/report-posthoc.json) is a corrected, post-hoc sensitivity analysis; its `valid: true` describes that corrected analysis, not the preregistered v1 result. Do not relabel v1 valid or use it to claim a checker benefit. Task 013 still needs a freshly preregistered live rerun with an explicitly specified citation policy and grader.

The model run itself completed: [plan.json](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v1/plan.json) SHA-256 `27cc232a283d0b9993108684a942421a1906fb30e3215c2e974f63388db41327`; [rows.json](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v1/rows.json) SHA-256 `5be692ba2f2a5e54a703586b55208d7bae9643fb3edeace09e764144f16f6dec`; [audit.json](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v1/audit.json) SHA-256 `403a447e386271b0a69b1ddc3832626be0e6e1c40fbebcd07522af80ecf7c6e4`. The [completion marker](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-upkeep-live-v1/audit.json.complete) contains the latter two hashes, which match the saved files. The audit pins runner `a0565afc973e6c1ef3d0a14ca732069d9a09416b6503e0d7f595688143a44b45` and adapter `3470b13c8e8d31fee44e2de6241a1af917a75bc02b5cb0f4c9f87c8cdbc8e9ea`. It records 10 model calls, 155,378 input plus output tokens, zero unknown-usage calls, 10 distinct session IDs, both expected cases and arms, and a Go CLI validation event for every final bundle. These operational checks establish complete artifacts, but cannot repair the original scoring failure. USD cost is unavailable.

For diagnosis only, the post-hoc grader reports:

| Outcome | Checker off | Checker on |
| --- | ---: | ---: |
| Concept document updated | 2/2 | 1/2 |
| Independent consumer correct by the strict grader | 1/2 | 0/2 |
| Wrong answer | 1/2 | 1/2 |
| Ungradable answer with unsupported citation | 0/2 | 1/2 |

The `upkeep-reversal` checker-on writer left `decision.md` saying Mode A after changing the code to Mode B. The final checker status remained `needs_review`; the independent consumer answered Mode A. Checker off updated that document to Mode B and its consumer answered correctly. For `upkeep-truncated`, both consumers answered “Insufficient evidence.” The checker-on answer nevertheless failed strict citation grading because its evidence field contained a prose citation unsupported by the registered artifact-ID contract. Its final checker status was `reviewed_updated`, which records a changed concept file, not factual correctness. The post-hoc summary therefore keeps it ungradable rather than silently counting it correct.

This is a two-case descriptive observation, and the checker-on arm combines a reminder, checker feedback, and an extra revision opportunity. It cannot isolate a checker effect. The observed pattern supplies a regression case for the grader and for writer behavior; it does not satisfy task 013's live acceptance criterion.
