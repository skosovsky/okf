# Independent technical review — task 017

Reviewer: acceptance_technical_017. First pass over an unfrozen workspace; no product edits or commits. Other final review verdicts were not read.

## Findings and recheck

T017-01 (P2): EN/RU quickstart tells the reader to choose the training source under Sources. The viewer Sources panel deliberately keeps local source resources inert (`viewer/assets/viewer.js`, source-resource branch), so that item is not clickable. Describe selecting the source note from the left list or following the body citation link. Both locales must agree. Closed: both quickstarts now describe the body citation link and source selection from the left list; directly reread.

T017-02 (P2): New RU skill reading editions call `write_concept` a way to bypass limits (`docs/ru/readings/skills/open-knowledge-format/SKILL.md:80` and `references/mcp-operations.md:29`). The tool retains schema, validation and store boundaries. Describe it as a compatibility fallback rather than a means of bypassing constraints. Closed: both RU readings now describe a compatibility fallback requiring result verification; directly reread.

T017-03 (P2): Manual knowledge review was marked current in the registry despite its historical report status. Closed: purpose/status now historical, directly reread.

T017-04 (P2): `fixtures/interoperability/README.md` and `viewer/testdata/regression-016/README.txt` are wrongly excluded as fixed test data. Both document methods, provenance or executable instructions, so user scope requires EN/RU reading editions while preserving original fixtures. Closed: both preserved-source full EN/RU pairs now registered (99 total), directly read and reviewed; the viewer materialization recipe independently executed with exit 0. Registry normative_source metadata corrected to null and directly reread.

## Independently verified

- `go test ./viewer ./docs ./internal/okfcli -run 'Test.*(Language|Render|View|Registry|Documentation)' -count=1`: all three packages PASS. Caches: /tmp/okf017-cache, /tmp/okf016-mod.
- `node viewer/testdata/regression-016/routes.cjs`: PASS against the production asset.
- Direct code inspection: default EN compatibility, Options.Language and RenderWithOptions, CLI strict en/ru rejection before load/publication, API rejection before output creation, no semantic JSON locale field, script SHA-256 CSP, no network/storage, no translation of arbitrary authored values, category-specific known labels, unchanged link/safety policy.
- Full command/tool table compared with current dispatch/registrations and schemas: 14 MCP tools, including search_sections; CLI setup/search/view locale and Go retrieval/setup/viewer API listed.
- Every registered pair examined for existence and technical identifier/numeric invariants. Direct technical semantic review completed for all 99 registered pairs: contracts, ADR, pinned specifications and skill readings, historical protocols/run reports/profiles, package/user guides and API catalog. Numeric/identifier scans supplemented reading; they do not independently establish semantic equivalence.
- Both complete pinned spec EN readings contain the original after removing presentation anchors; originals and runtime skill sources remain byte-identical. Normative contract EN body changes are presentation/navigation only. Historical numbers and raw logs remain data rather than newly asserted current results.
- Reviewed clean-directory command scripts/results and actual four Codex skill/MCP event streams/evidence summaries; source-copy overlay is explicitly disclosed, not a claim of unreleased GitHub availability.

## Final frozen-state acceptance

Product digest: `7883e48018b83027860e2eec99a590ebb8c2ef11ea6d6ac744e8da4c1b551115` (1439 files). `freeze-product.py --check` independently returned unchanged. Coverage has all 99 IDs with current EN/RU hashes. All T017-01..04 are closed after direct rereading. No open confirmed technical findings.

Reviewed actual native browser evidence (desktop and 390px EN/RU, localized navigation, same-section switches, source opening, filtering, graph state, Unicode/colliding fn namespaces, repeated footnotes, Back/Forward, reload and empty states), built-site report (197 HTML pages, 3148 local checks, 97 site pairs, zero errors), four real Codex skill/MCP clients and clean-directory guide evidence. After finishing the independent first pass, coordinated the editorial 99-pair semantic review and closed-finding evidence; structural checks are not cited as semantic equivalence proof.

| Criterion | Score | Evidence or remaining requirement |
|---|---:|---|
| AC01 | 10% | Complete expanded 99-pair registry; two fixture READMEs now included; originals preserved. |
| AC02 | 10% | Full editorial corpus review, corrected Russian readings, new teaching prose and localized UI. |
| AC03 | 10% | Technical pair review plus separately completed 99-pair editorial fact/action/example/constraint comparison. |
| AC04 | 10% | Guides follow fictional supplied rule through source, search and 24→48-hour revision. |
| AC05 | 10% | Clean-directory EN/RU command evidence and four actual skill/MCP client runs; new fixture recipe also independently executed. |
| AC06 | 10% | Current CLI/Go/14-MCP catalog, preserved normative/skill originals, contract guarantees and public package catalog. |
| AC07 | 10% | Built site and actual desktop/narrow language/anchor/navigation evidence pass. |
| AC08 | 10% | Own Go and production-JS tests plus actual native locale/state/route/browser evidence pass. |
| AC09 | 10% | Independently fetched both completed GitHub runs at e7062ef; all 5 CI jobs, all 7 action cases and every step SUCCESS, including full Go suite/race/vet/modules/JS/deterministic exports. |
| AC10 | 10% | Both independent final reviewers confirm this exact frozen digest, 99 pairs and zero open confirmed findings. |

Final strict score: **100%** (10 criteria × 10%). Both reviewers confirmed the identical product digest and zero open findings. Independently retrieved CI metadata is preserved in `technical-ci-verified.json` and `technical-action-ci-verified.json`: runs 37218005570 and 37218005295, exact tested SHA `e7062ef88efc2f0deaa99b3295df1c7a63b841cd`, all jobs and steps successful. The 99 reviewed EN/RU pair hashes and product digest were rechecked unchanged after CI. Any subsequent evidence-only commit should retain this product digest; latest-head readiness checks are handled by root. This review does not publish a release or move a tag.

Local root full-suite runner was stopped after diagnosing a giant pre-existing cache read; it is not recorded as PASS. Clean GitHub full-suite/race for PR #12 completed successfully; their independently fetched results are the acceptance evidence. No additional heavy tests were run during this coordination update.
