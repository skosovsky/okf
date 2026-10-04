# Benchmark reading editions — issue 017

Source pin: `61e75e9aa9a8719dfb480bf1f5226553b3a21d70` (commit date 2026-10-04). Original sources, raw benchmark outputs, frozen corpus, queries and manifests are unchanged. Registry determines the reading paths and current/historical status. No new performance/answer-quality claim is introduced.

## Directly translated documents

| Source | Coverage | Retained checks |
| --- | --- | --- |
| `benchmarks/retrieval/README.md` | Full EN reading copy and RU translation: introductory purpose, freeze provenance, reproduction command block, relocation/output constraints, attribution checker, document ranking, no-answer denominator, latency/byte methodology and raw evidence requirements | Code block exact; every source numeric value retained; explicitly document-level ranking after stable dedup, five-hit wire budget, outer JSON-RPC excluded, no LLM quality measurement |
| `benchmarks/toolkit/README.md` | Full EN reading copy and RU translation: pinned revisions, paired results, all corpus rows, interoperability/temporal correctness caveats, baseline reproduction, paired reproduction and superseded historical profiles | Both code blocks exact; all 15 table lines retained; every source numeric value retained; every hash/revision retained; no current performance promise |
| `benchmarks/toolkit/results/paired-ed7ddc28-0f15ab4-summary.md` | Full 34-workload comparison, both variants, N, ns/op, B/op, allocs/op and all three deltas | All 36 table lines retained; workload IDs, N/A cells and measurements untouched; headings localized |

Current benchmark methodology pages retain registry `current` status and distinguish past measurements from guarantees. Historical result tables carry an explicit archived-source notice. Original raw artifacts use immutable GitHub links; reading crosslinks use localized registry paths.

## Delegated coverage

Top-level agent benchmark guides: `archive-bench-guides.md` (9 sources).
Archived run READMEs: `archive-bench-runs.md` (13 sources).
Live before/after and upkeep guides: `archive-bench-live.md` (5 sources).

All delegated reading editions are complete. Aggregate verification below supplements those reports. This report is implementation evidence, not the independent final acceptance report.

## Final aggregate verification

All 30 sources / 60 reading pages checked: source SHA exactly matches registry; every fenced command body remains byte-exact; every table line count matches; explicit `section-N` IDs match in each EN/RU pair. Historical English answers remain exact and are marked as raw quotes, with Russian explanation. English heading aliases retained in both locales so old section links remain usable.

Structured per-document evidence: `archive-benchmark-checks.json`.

| Source | EN/RU | Exact source + code | Table lines | Paired heading anchors |
| --- | --- | --- | ---: | ---: |
| `benchmark/agent/BACKFILL-CONSUMER.md` | Complete | PASS | 0 | 3 |
| `benchmark/agent/BEFORE-AFTER.md` | Complete | PASS | 4 | 4 |
| `benchmark/agent/METADATA-CONTRAST.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/PILOT-DIAGNOSTIC.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/PILOT-PRECOMPUTED.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/PILOT.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/README.md` | Complete | PASS | 7 | 1 |
| `benchmark/agent/UPKEEP-STUDY.md` | Complete | PASS | 0 | 2 |
| `benchmark/agent/WRITER-CONSUMER-BASELINE.md` | Complete | PASS | 0 | 2 |
| `benchmark/agent/beforeafter/LIVE.md` | Complete | PASS | 10 | 1 |
| `benchmark/agent/beforeafter/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-backfill-consumer-v2/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-beforeafter-v1/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-diagnostic-read-v1/README.md` | Complete | PASS | 4 | 1 |
| `benchmark/agent/runs/20260926-diagnostic-read-v2/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-exploratory/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-manual-writer-consumer-v3/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-metadata-only-v2/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-primary-blocked/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-primary-bundled-v2/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-primary-live/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-primary-precomputed-v1/README.md` | Complete | PASS | 0 | 1 |
| `benchmark/agent/runs/20260926-upkeep-live-v1/README.md` | Complete | PASS | 6 | 1 |
| `benchmark/agent/runs/20260926-upkeep-live-v2/README.md` | Complete | PASS | 7 | 1 |
| `benchmark/agent/upkeeplive/LIVE-V2.md` | Complete | PASS | 9 | 4 |
| `benchmark/agent/upkeeplive/LIVE.md` | Complete | PASS | 8 | 4 |
| `benchmark/agent/upkeeplive/README.md` | Complete | PASS | 0 | 1 |
| `benchmarks/retrieval/README.md` | Complete | PASS | 0 | 1 |
| `benchmarks/toolkit/README.md` | Complete | PASS | 15 | 4 |
| `benchmarks/toolkit/results/paired-ed7ddc28-0f15ab4-summary.md` | Complete | PASS | 36 | 0 |

Whitespace check reported unrelated concurrent migration.md EOF warnings only; no owned reading file warning. Functional site build/link acceptance remains root responsibility.

## Navigation correction after editorial review

Corrected remaining Russian benchmark editions using English navigation to `nav_ru.html`. Also found the 13 archived-run pairs had no navigation include at all; added the appropriate include to both locales. All 30 EN editions now include `nav.html`; all 30 RU editions include `nav_ru.html`. No prose, source, command, result or table was changed.

## Editorial grammar and rendered heading check

Corrected the case agreement in the RU metadata-only protocol: «Для каждой группы сообщайте результаты точного оценщика v1: …». Historical result labels remain exact. Parsed actual H1–H6 elements from the compiled site for all 30 pairs: EN/RU heading tag/ID sequences match and every actual heading uses a stable `section-N` ID. The site-link validation record contains no errors for owned benchmark pages.

## Final historical-reading editorial corrections

Added missing sentence-ending periods after Russian parenthetical explanations of exact English answers in primary-precomputed-v1 and upkeep-live-v1/v2. Changed «постоянные часы» to «фиксированное время» in beforeafter-v1. Only RU reading prose/punctuation changed; original sources and historical answer bytes are untouched.
