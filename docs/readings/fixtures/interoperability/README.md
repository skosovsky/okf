---
layout: default
title: "OKF interoperability corpus"
lang: en
permalink: /readings/fixtures/interoperability/README/
documentation_id: fixtures-interoperability-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical: true
---
{% include nav.html %}

**Historical reading edition.** Source: [fixtures/interoperability/README.md](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/fixtures/interoperability/README.md), repository revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70` (2026-10-04). This page preserves the methods, dates, measurements and status recorded in that revision. Historical results describe those runs, not the current product or a recommendation to run agents today.

<a id="page-top"></a>
<a id="okf-interoperability-corpus"></a>

# OKF interoperability corpus
{: #section-1}

`foreign-68ce7a0/` selects five files from
[`scaccogatto/okf-skills@68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5`](https://github.com/scaccogatto/okf-skills/tree/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/.okf).
The selection retains a root title and introduction, `upkeep` extension,
wrapped log entry, literal `[^label]` in code, foreign actor spelling and a
concept omitted from its nearest directory index. `manifest.json` records the
exact source URL, source/fixture SHA-256 and transform for each file. The
upstream MIT license and copyright are reproduced in `LICENSE`.

`TestFrozenForeignInteroperabilityCorpus` validates this snapshot in Go,
offline, under base, strict and strict-plus-orphans profiles. Its expected
diagnostics pin code, severity, file and field path; wording may improve.
Links to files outside the selected snapshot are deliberately not checked.

The full foreign bundle was separately validated at the pinned commit with
`okf validate --strict --check-links --check-orphans`. Before the fixes it
produced 30 files, 312 errors and 18 warnings. After the fixes it produces
30 files, 0 errors and 16 warnings:

| Class | Before | After | Contract reading |
| --- | ---: | ---: | --- |
| `log_structure_invalid` | 310 errors | 0 | §9 flat list prohibits nested entries, not a physical wrap within one item. |
| `index_structure_invalid` | 2 errors | 0 | §8 permits an introductory title; §11 forbids rejecting unknown adjacent root frontmatter keys. |
| `source_footnote_unknown` / `source_footnote_definition_missing` | 2 warnings | 0 | §5.1 claim attribution uses prose-owned references; code spans are opaque. |
| `actor_invalid` | 14 warnings | 14 | §7 does not include `agent:...` in the actor forms. |
| `orphan_unlisted` | 1 warning | 1 | Optional toolkit index coverage policy, not base conformance. |
| `source_field_invalid` | 1 warning | 1 | Date-only temporal profile sees the foreign datetime; the upstream datetime profile is handled in issue 004. |

The foreign Python checker is useful for comparison but is not a conformance
oracle: its index body and log structure checks do not cover these cases.
