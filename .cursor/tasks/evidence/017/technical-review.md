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

## Provisional acceptance

Each criterion is 10%; partial or unverified is 0. This first-pass technical review is not final approval. AC01 10; AC02 0 (editorial full-scope verification pending); AC03 0 (exhaustive pair semantic review pending); AC04 10; AC05 10; AC06 10; AC07 0 (built-site/native browser acceptance pending); AC08 0 (native browser acceptance pending); AC09 0 (full suite/vet/race/CI pending); AC10 0 (two reviews on same frozen state pending). Total: 40%.

Pending: inspect root native browser/full-suite/CI evidence, coordinate editorial evidence after the independent first pass, and recheck frozen product digest.
