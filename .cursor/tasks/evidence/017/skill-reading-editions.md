# Skill reading editions — task 017 evidence

Source revision: `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

Implemented 13 complete EN/RU pairs (26 pages). Runtime skills and snippets remain byte-identical to their baseline SHA-256; the two pinned format specs belong to another agent. Reading wrappers identify the canonical source and verified revision and distinguish documentation from installation.

| Source | Paired sections | Preserved key requirements |
| --- | ---: | --- |
| `docs/snippets/AGENTS-okf.md` | 1 | Relevant-only reading; upkeep baseline before edits; update or fingerprint-bound unaffected decision; log/validation are not verification; explicit migration. |
| `docs/snippets/CLAUDE-okf.md` | 1 | Same repository knowledge instructions as AGENTS, including baseline, affected notes, truthful final checker result and explicit migration. |
| `skills/okf-backfill/SKILL.md` | 5 | Extract/analyze/preview/write phases; analyst separation; draft publication; resume/failure evidence; no invented verification or source claims. |
| `skills/okf-backfill/references/analyst.md` | 3 | Evidence-only analyst JSON output; supported evidence selectors; actual source references; draft proposal; no publisher authority. |
| `skills/okf-maintain/SKILL.md` | 5 | Baseline before edits; actual JSON status/fingerprint; reviewed_updated/explicit_unaffected final state; advisory/blocking exceptions; manual missed-baseline gap. |
| `skills/open-knowledge-format/SKILL.md` | 8 | Author/read/validate/view/migrate/MCP routing; pinned spec hashes; actor separation; preservation; host permissions; inert assets and trust limits. |
| `skills/open-knowledge-format/references/adversarial-v02.md` | 2 | 17 data rows preserved; uint64 decimal-string transport, footnote ownership, exact warnings, target-noop/replay, distinct RelationRef identities, lifecycle, inert executor, agent-only vs deterministic evidence, symlink/recovery boundaries. |
| `skills/open-knowledge-format/references/authoring-and-reading.md` | 7 | Version/profiles; type-only base conformance; source/producer/verifier separation; structured trust/status/staleness; field-presence fallback suppression; computation inertness. |
| `skills/open-knowledge-format/references/conversion.md` | 7 | Lossless preservation; demonstrable source mapping; explicit type; per-source conversion safeguards; no hidden migration or fabricated lifecycle/trust. |
| `skills/open-knowledge-format/references/examples.md` | 13 | All 8 fenced examples retained; ordinary teaching prose localized; minimal source/verification/computation/compatibility patterns; selector conflicts; wire identity, usage counts, patch/remove selectors. |
| `skills/open-knowledge-format/references/mcp-operations.md` | 6 | Live schema/14-tool routing; preview/apply authority; frozen input/digest; exact selector forms and limits; projection differences; conflict and recovery handling. |
| `skills/open-knowledge-format/references/migration-v01-v02.md` | 11 | Full transaction contract; old timestamp/Citations conversion; actors/digests/exact bytes; selector overlaps; missing documents/blockers; RelationRef and computation boundary; no trust/freshness inference; all 3 code blocks retained; ordinary teaching prose localized. |
| `skills/open-knowledge-format/references/operational-workflows.md` | 4 | Validation inputs/strict budget/fallback/done; offline viewer collision authority/browser inspection; absent MCP export; upkeep baseline/check and manual gap. |

## Checks performed

- SHA-256 of every assigned original matches `baseline_sha256`; no runtime package/lock edit.
- All 26 registry paths exist with locale, permalink and document ID frontmatter.
- EN/RU explicit Kramdown `section-N` anchors match, including H1; headings inside code examples are not treated as page sections.
- All locale URLs emitted by these pages exist in the documentation registry; all referenced reading fragments exist in the target page.
- English pages contain no Cyrillic. Russian body prose was translated independently per group; command names, field names and literal test data remain literal.
- The adversarial table has the same 19 table lines as the source; both operational tables retain the same 10 lines. Every backtick literal in those two sources is present in the Russian edition.
- Examples and migration retain all fenced blocks (8 and 3 respectively). Following independent editorial review, Russian editions localize ordinary teaching comments, content placeholders, report/policy titles and matching citation text; API keys, IDs, URLs and timestamps remain unchanged. Repeated exact selectors retain the same localized bytes wherever repeated. Runtime originals remain byte-identical.
- Scoped `git diff --check -- docs/readings/skills docs/ru/readings/skills docs/readings/docs/snippets docs/ru/readings/docs/snippets` passes. A simultaneous global check reported EOF whitespace in another agent’s `docs/migration.md` and `docs/ru/migration.md`; those are outside this assignment.

These static checks do not prove semantic equivalence or final browser behavior. Final independent reviewer must compare paired prose and the assembled site; no claim of final acceptance is made here.

## Editorial correction

Independent review identified untranslated ordinary prose in migration code examples. Fixed `before`/`after` comments, caller actor comment, the sample claim, source title and footnote. Audited all 13 Russian reading pages and also localized example placeholders, report/policy display titles, upkeep fingerprint placeholders and the extension/tooling policy label. Adversarial quoted prompts remain literal, explicitly labelled as test evidence. No installed instructions or normative source files were modified.

Technical review correction: Russian descriptions of `write_concept` in the SKILL and MCP reading editions now call it a fallback interface for compatibility requiring result validation; they no longer imply bypassing validation or store restrictions. Source originals are unchanged.
