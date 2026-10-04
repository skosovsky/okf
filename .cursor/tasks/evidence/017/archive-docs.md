# Historical documentation translations

Source baseline: `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. These are reading editions of historical records, not renewed acceptance claims.

## Completed direct translations

| Source document | RU section coverage | Verification |
| --- | --- | --- |
| `docs/releases/v0.2.0.md` | All 3 sections: API additions, platform/isolation, distribution/evidence | 7 additions preserved; commits43f7214/bb9c169, source compatibility, advisory lease/recovery scope, source-only distribution and immovable tag retained |
| `docs/releases/v0.2.2.md` | All 3 sections: included work, migration hardening, compatibility | Date2026-09-17, tasks09–21, 401tests, nine historical MCPtools, typed-nil/Darwin paths/noop distinction, versions and8e5ef56 retained |
| `docs/issue-2-release-engineering-evidence.md` | All3sections and all12requirement rows | All15shell commands byte-identical; Go-template literal protected from Liquid by raw/endraw; retrospective warning about prior unproven100% claim retained |
| `docs/viewer-browser-review.md` | All narrative paragraphs, prepared files table, five manual steps and four screenshots | Both tables/hash values/byte counts preserved; all shell commands byte-identical; manual/user evidence and automated checks still distinguished |

All four EN pages retain substantive historical bodies and received EN language metadata, navigation, explicit historical revision note. RU pages provide full translated bodies. English UI strings in viewer acceptance are literal historical test conditions, explicitly labelled. Public source links retain original destinations; local cross-document links use registry permalinks where possible.

## Delegated completion and independent checks

All21archive document pairs are complete. Across the full subset, baseline/EN/RU fenced blocks are byte-identical and paired explicit anchors match. All14development pairs retain original heading counts. The seven originally English substantive bodies are unchanged after removing metadata, anchors, raw wrappers and link routing. The historical viewer report necessarily retains literal Cyrillic search/link labels in its English test conditions. Task09–21 originals were mixed Russian/English; their full EN editions are new translations and full RU editions are editorial cleanups. No machine JSON data, fixtures, captured logs or benchmark results were changed. The site-wide build and final independent product review remain root acceptance work.
# Parser-backed historical translations

Source revision: `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. All three pages translated in full; commands, captured logs and numeric measurements preserved.

- `docs/parser-backed-lossless-mutations-evidence.md`: 100% paragraphs/sections and factual content translated; 4 fenced blocks preserved exactly; paired path `docs/ru/parser-backed-lossless-mutations-evidence.md`.
- `docs/parser-backed-lossless-mutations-profiles/overlay-baseline-vs-result-2026-07-20.md`: 100% paragraphs/sections and factual content translated; 1 fenced blocks preserved exactly; paired path `docs/ru/parser-backed-lossless-mutations-profiles/overlay-baseline-vs-result-2026-07-20.md`.
- `docs/parser-backed-lossless-mutations-profiles/planner-10000-2026-07-20.md`: 100% paragraphs/sections and factual content translated; 4 fenced blocks preserved exactly; paired path `docs/ru/parser-backed-lossless-mutations-profiles/planner-10000-2026-07-20.md`.

The overlay table retains all 18 numeric measurement values and all three fixture meanings. Planner CPU/heap logs preserve all 40 nodes each verbatim. No registry, tests, fixtures, normative source or unrelated files changed.

All corresponding headings have matching explicit stable anchors. Internal report links use locale-specific registry permalinks. English substantive body is preserved exactly apart from those anchors and link-target routing. All code/log blocks are byte-identical to original.

Final language polish: GitHub issue link labeled «задачи GitHub #1 (на английском)»; revision links already identify English originals. Captured code unchanged.
# Historical development-task translation evidence

Source revision: `61e75e9`. Original task09–21 were Russian with substantial English prose; README was English. The English pages are complete translations, Russian pages full editorial translations rather than summaries. All original requirements, section order, API names, numeric values and test conditions were retained. Original source is linked at the pinned revision; historical status does not claim current implementation requirements.

28 pages (14 pairs): README and task09–21. Scope: only docs/development/v0.2/tasks/*.md and docs/ru/development/v0.2/tasks/*.md. Registry and tests unchanged.

Every page has lang/title/permalink frontmatter and appropriate navigation include. Paired section identifiers are identical (`section001` etc). Sibling links use locale-correct registry URLs through `relative_url`. Machine JSON evidence links to the pinned original on GitHub. English pages have no Cyrillic prose. Russian ordinary prose has been edited; technical literal names are in code formatting.

Structural checks do not prove semantic equivalence; the translations were also read against their source requirements. Original sections and all requirement lists were fully translated. No excerpts omitted.

| Document | Original headings | EN headings | RU headings | Anchors |
| --- | ---: | ---: | ---: | --- |
| README.md | 1 | 1 | 1 | matched |
| task09.md | 10 | 10 | 10 | matched |
| task10.md | 13 | 13 | 13 | matched |
| task11.md | 14 | 14 | 14 | matched |
| task12.md | 11 | 11 | 11 | matched |
| task13.md | 9 | 9 | 9 | matched |
| task14.md | 11 | 11 | 11 | matched |
| task15.md | 10 | 10 | 10 | matched |
| task16.md | 7 | 7 | 7 | matched |
| task17.md | 6 | 6 | 6 | matched |
| task18.md | 6 | 6 | 6 | matched |
| task19.md | 17 | 17 | 17 | matched |
| task20.md | 16 | 16 | 16 | matched |
| task21.md | 12 | 12 | 12 | matched |

`git diff --check` for owned paths: PASS. Full-site build and independent acceptance remain parent responsibilities.

## Final editorial corrections

- RU task21 lowercases both YAML field references to literal `timestamp`.
- RU v0.2.0 translates bounded idempotency receipts as bounded receipt storage rather than inventing a per-receipt size limit.
- All21pairs now have explicit matching identifiers for every real Markdown heading, including H1. Fixed the two actual site parity findings (issue2 and viewer historical H1) and added explicit release H1 anchors.
- Whitespace check of touched source/translation files passed. Rebuilt site validation remains root responsibility.

- Independent historical review: corrected two nominative subject forms in RU task09 (`исходная спецификация` at baseline lines21and107); technical claims unchanged. Targeted whitespace check passed.
