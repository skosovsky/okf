# Independent completeness review — task 016

First conclusion, 2026-10-04. Reviewer: final_completeness. No product edits, commits or pushes. The other independent review was not read before this conclusion. Implementation reports were read as supporting evidence, then checked against task requirements, contracts, source/tests and raw artifacts.

Reviewed baseline: `8e822ea631caaac058fb40aa65c5821352048278`.
Reviewed product state: `141326454ef6c42d7956c9f88424368d3825f824dd43919db587c44f29fb3b76`, 72 files. Independently recomputed canonical manifest SHA256; every listed file size and SHA256 matched. Product modifications require renewed review. Acceptance-only evidence is excluded by the documented manifest method.

Whole-criterion scoring only: completed with evidence = 5%, partial/unverified/pending = 0%. Current first-conclusion score: **80% (16/20)**. No scope or denominator reduction.

| ID | Status | Source / concrete evidence | Gap | Credit |
| --- | --- | --- | --- | --- |
| AC01 | Completed | `viewer/README.md`; effective fallback in `viewer/viewer.go`; `TestSourcesPresenceOnlyLegacyFallback`, `TestDeclaredV01LegacySourcesRegression` in `viewer/provenance_regression_test.go`; own viewer tests PASS | None. Absence, valid/empty/null/malformed/duplicate replacement and text-only source covered. | 5% |
| AC02 | Not completed / pending | Persistent `viewer/testdata/regression-016`, production JS route precedence; `TestRouteFixturePreservesFootnotesAndUnusualIDs`, `TestViewerRouteRegression` PASS | Real-browser final acceptance artifact for direct fn/fnref/encoded routes, native history and actual footnotes/backlinks not yet saved. Node harness cannot satisfy this portion. | 0% |
| AC03 | Completed | `README.md`, `README.ru.md`: human tasks, disk layout, format/toolkit distinction, architecture demo and full quickstart/MCP links; docs tests PASS | None. | 5% |
| AC04 | Completed | `docs/index.md`, `docs/ru/index.md`; nav includes; `docs/reference.md`, `docs/ru/reference.md`; preserved deep toolkit/migration/skill contracts; own docs tests PASS | None for content/contracts. Rendered browser acceptance belongs to AC08. | 5% |
| AC05 | Completed | `docs/quickstart.md`, `docs/ru/quickstart.md`; `quickstart-clean-script.sh`, `quickstart-clean.log`, `quickstart-clean-result.json` final run cyypib1j exit 0 | None. Fresh clone plus current product snapshot overlay, cwd/prerequisites/safe new output documented; demo, sourced own note, search and setup commands completed. Prior qy192i_z result is retained as initial evidence. | 5% |
| AC06 | Completed | `knowledge/architecture.md`; generated `docs/demo/knowledge.html`; quickstart known answer, two sources, related mutation/version notes; supplied fictional material and own retry note; clean validation/export PASS | None. Actual rendered demo operation additionally awaits AC08 artifact. | 5% |
| AC07 | Completed | `mcp-client-request.json`, `mcp-client-events.jsonl`, `mcp-client-summary.json`, `mcp-client-exit.json`; Codex CLI 0.160.0 | None. Actual client successfully used search_concepts, read_concept, get_neighbors and answered with note and pinned source. Session config isolated; skill/MCP distinction explicit. | 5% |
| AC08 | Not completed / pending | `site-build.log`, `site-reference-links.json`, `site-reference-http.json`; final demo generated | Final real-browser desktop/narrow EN/RU navigation, links, commands, demo and readability artifact not yet present. Build/HTTP checks alone insufficient. | 0% |
| AC09 | Completed | `docs/contracts/section-search.md`, MCP input/output schemas, baseline.md; additive CLI flags and shared result contract; old `concept_queries.go` literal contract/source retained; old search tests PASS | None. Contract-first sequence recorded; additive schemas and semantics preserved. | 5% |
| AC10 | Completed | `retrieval/search.go`; shared CLI/MCP adapters; tests ANDUnicodeTiesTruncationAndSnapshot, SectionsUseOriginalLinesAndIgnoreFencedHeadings, SectionSearchCLIAndMCPShareSnapshotAndSchema PASS | None. Unicode case folding/NFC, AND, weighted BM25, deterministic score/ID/line ties, EN/RU and multi-term evidence. | 5% |
| AC11 | Completed | Search contract; physical lines/parser spans and captured metadata in retrieval source; source-range/fence/Setext/CRLF/snapshot tests; own independent validation of every final raw hit's SHA256/snippet/locator | None. Drift changes revision; results bind captured bytes, consumer must repeat search or compare digest after edit. | 5% |
| AC12 | Completed | Retrieval query/load/section/token/output bounds; InvalidCancellationAndResourceCaps, AggregateTokenSectionAndOutputCaps, FilesystemNoFollowAndRelativeRoot; targeted MCP/old-search boundary tests PASS; documented total/truncated and no cursor | None. Shared no-follow loading, inert content, explicit cap failures, cancellation and unchanged literal tools remain covered. | 5% |
| AC13 | Completed | `docs/contracts/markdown-setup.md`, `setup/contracts/*.schema.json`, `setup/schema_test.go`; `internal/okfcli/setup.go`; existing init path; setup CLI JSON tests PASS | None. Source/target, selection, default preview, digest-bound apply, collision/replay, drift and cancellation explicit; init remains distinct. | 5% |
| AC14 | Completed | `setup/setup.go` shared parser/index/validator/publication reuse; TestManagedCopyPreservesBytes, TestSelectionAndMissingType; clean setup/strict validation/export | None. Explicit missing-type policy, no provenance/status/freshness inference, conformant complete bundle. | 5% |
| AC15 | Completed | Setup tests exact unknown YAML/body and source preservation, supported links; blockers malformed/unterminated/duplicate metadata, unsupported assets/HTML/autolinks; drift/collision/cancel cleanup; 501-doc/1003-output complete publication test; own entire setup package PASS | None. Existing no-replace publication guarantees reused; documented post-publication fsync/quiescent-source boundary honest. | 5% |
| AC16 | Completed | Original `corpus-freeze.json`, byte-identical tracked `benchmarks/retrieval/testdata/freeze.json`; `queries.json` 24 cases, 20 answerable +4 no-answer EN/RU; gold headings/lines/evidence; unchanged file hashes | None. Freeze-before-search provenance recorded; no corpus adjustment in final state. | 5% |
| AC17 | Completed | `retrieval-baseline-checked.json`, `retrieval-final-reviewed.json`; fixed baseline commit/binary digests; `benchmarks/retrieval/README.md` reproducible commands; harness validation source/tests; own raw metrics/source validation | None. Independent recalculation yields 0.10→0.95 for both document Hit@5/MRR@5, no-answer 0/4 in both. Same five-hit budget, document dedup mapping and separate attribution clearly stated. | 5% |
| AC18 | Completed | README/landing/quickstart/MCP docs and benchmark README; actual integration events versus offline raw rows; limitations and no LLM-quality claim explicit | No product claim gap. Evidence wording about response-size object should match harness precisely; root notified. Synthetic corpus and single smoke limitations remain clear. | 5% |
| AC19 | Not completed / pending | Own targeted tests all PASS (commands below); existing implementation checks/site build | Current complete `go test ./...` still running per root; final successful full suite, full vet, diff check and changed skill lock/package checks must be captured on current files in verification artifact. No timeout/uncompleted run credited. | 0% |
| AC20 | Pending | Current final-state manifest verified | Both final independent conclusions plus current acceptance/evidence statuses and no open confirmed findings must be verified after root finalizes evidence. Other review intentionally unread before first conclusion. | 0% |

## Independent commands and results

Environment: `GOCACHE=/tmp/okf016-review-complete-cache GOMODCACHE=/tmp/okf016-mod`.

- `go test ./retrieval ./setup ./viewer ./docs ./benchmarks/retrieval/cmd/okf-retrieval-eval -count=1 -timeout=20m` — exit 0: retrieval 0.921s, setup 33.468s, viewer 6.464s, docs 0.733s, harness 0.211s.
- `go test ./internal/mcpserver ./internal/okfcli -run 'Test(SectionSearch|SearchConcepts|ConceptQueriesRejectSymlinkEscape|SetupCLI|BundlePathRejectsAncestorSymlink)' -count=1 -timeout=20m` — exit 0: mcpserver 0.666s, CLI 0.334s.
- Independent Python manifest check: 72 files, no size/SHA mismatch, canonical digest `141326454ef6c42d7956c9f88424368d3825f824dd43919db587c44f29fb3b76`.
- Independent Python raw-row recalculation: each arm 24 rows; baseline Hit/MRR 0.1/0.1; final 0.95/0.95; no-answer nonempty 0 in each. Every final raw section hit checked against frozen bytes for content hash, snippet containment in reported physical lines and percent-encoded locator.

The full expensive suite was not duplicated concurrently. Browser records and final verification were still being assembled, so the pending rows remain 0%. Required next pass: check those artifacts and successful suite on unchanged product state, then read both independent final conclusions, current acceptance matrix/status and finding dispositions for AC20. Existing historical implementation reports describe earlier states and do not replace final verification.

## Second evaluation — browser evidence and independent correctness conclusion

2026-10-04, same product digest `141326454ef6c42d7956c9f88424368d3825f824dd43919db587c44f29fb3b76`. Independently rechecked all product file hashes: no drift. Read the independent correctness report only after the first conclusion above.

The table above is the historical first-conclusion state; the following decisions supersede its pending browser rows. **Current score: 90% (18/20), AC19 and AC20 remain 0%.**

| ID | Updated status | Concrete evidence checked | Remaining gap | Credit |
| --- | --- | --- | --- | --- |
| AC02 | Completed | `browser-review.md`: real Chrome 154.0.8037.97 direct/reload encoded fn route, fnref list/reload, ordinary Markdown links, native Back/Forward, Unicode, fnref1 concept and genuine repeated marker/backlink operations. `browser-artifact-digests.json`: every pinned file digest independently matched current production JS/demo and actual regression/site HTML. | None. Complements own passing production-JS and Go regressions. | 5% |
| AC08 | Completed | `browser-review.md`: actual Chrome desktop1729×907 and narrow390×844; EN/RU landing/navigation, quickstart code overflow, MCP page, useful demo/source/related links, own sourced note. `site-flow-links.json`: 10 pages/189 local links, 0 failures, all HTTP200 plus decoded fragment checks. Site and demo digests match actual built files. | None within required desktop/narrow coverage. Chrome coverage is explicitly bounded. | 5% |
| AC19 | Pending | Targeted tests and browser/site proof completed | Final successful full-suite/whole vet/diff/affected skills lock-package verification artifact not present yet. | 0% |
| AC20 | Pending | Both independent reports now reviewed on identical 72-file final state; correctness final conclusion independently closes F1–F6 with concrete regressions/repros, no open confirmed findings. | Final acceptance matrix/status/verification must be reconciled after successful AC19, then acceptance-only changes checked. | 0% |

No newly confirmed product completeness issue arose in this second evaluation. Evidence wording now identifies response bytes as the serialized MCP result object excluding the outer JSON-RPC envelope, matching the harness. The real-browser report states that screenshots were inspected in tool outputs but not retained as image files; it records concrete actions and observations plus artifact hashes, and does not claim cross-browser/device exhaustive testing.

## Product delta — CLI help compatibility regression

Current product state is now `4b41bb383a31c260213ab7cae10e69d972f89862ade3d7c9eb34470b8b080a11` (72 files). Independently re-read final-state manifest and checked current file SHA256 values: no mismatches. The reported delta from the prior reviewed state is only `internal/okfcli/run.go`: help lists additive search/setup commands after version, preserving the established source-pinned command excerpt. Dispatch and all other reviewed product bytes are unchanged. This is a compatibility correction discovered by the root full-suite `benchmark/agent.TestRealisticCasePinsActualGoSource` failure, not a completed successful suite.

AC03/05/09/10/13 were reconsidered for help order impact: documented invocation semantics, CLI dispatch, additive search/setup behavior and first-use commands are unaffected. Existing browser artifacts remain applicable to unchanged viewer/site files; their exact hashes were checked in the second evaluation. Their original aggregate product digest remains historical and must be explicitly linked to this narrow delta in final verification. No corpus/gold alteration is allowed or observed.

**Current score remains 90% (18/20). AC19 stays 0% until regression retest and successful complete final suite; AC20 stays 0% until both independent final reviews and acceptance evidence converge on the new current digest.** Prior reports/tests are supporting coverage, not grounds for claiming a green whole-suite result on this state.

## Final whole-suite verification — AC19

Final complete unfiltered command `GOCACHE=/tmp/okf016-cache GOMODCACHE=/tmp/okf016-mod go test ./... -timeout=30m` completed exit0/session15606 on current digest `4b41bb383a31c260213ab7cae10e69d972f89862ade3d7c9eb34470b8b080a11`. Independently read all42 output lines, including benchmark/agent7.019s, MCP190.781s, CLI99.549s, viewer7.891s and valid cached unchanged packages; no FAIL/panic/timeout. Independently matched full log SHA256 `96ecb87a39d3223ac90edfeed1186eca565bd42d77ee6d7e24c00c9839235542` and every log digest/exit in command-results.json. Full vet exit0; skills lock/package exits0; package tests2/2passed; own repeated current `git diff --check` exit0. Historical failed/interrupted runs are explicitly not acceptance evidence.

Independently checked current manifest file sizes,modes,SHA256, canonical aggregate digest and patch SHA256, plus exact changed tracked/nonignored untracked product inventory using NUL-separated Git commands:72files, no mismatch, no extra/missing product path. The correctness reviewer has now independently ACKed final synchronization on the same digest, F1–F7 CLOSED, no open confirmed findings. AC19 is **completed5%**, interim score95%; AC20 awaits final stale journal record reconciliation only, not further product work or testing.

## Authoritative final acceptance — 100%

2026-10-04. This final table supersedes every earlier interim score/pending row in this report. Reviewed baseline `8e822ea631caaac058fb40aa65c5821352048278`; final current product digest `4b41bb383a31c260213ab7cae10e69d972f89862ade3d7c9eb34470b8b080a11`,72files. Final recheck after journal correction: every product file byte/size/mode and canonical digest matches. Independent correctness final synchronization ACK on the same state was received and its saved report read: F1–F7 CLOSED, no open confirmed correctness findings in reviewed scope.

Final `findings-journal.md` now accurately records F7 CLOSED, successful full suite and AC19 verified. Browser/retrieval/client proofs transparently retain their historical aggregate state and are carried forward through the single help-order delta; all relevant runtime/site/fixture bytes are unchanged. Task/index/matrix final closure labels are the remaining mechanical acceptance-record update following this verdict. They introduce no unmet product requirement and must be checked separately by root; any subsequent product change requires renewed affected-scope review.

| ID | Final status | Concrete source / test / artifact | Gap | Credit |
| --- | --- | --- | --- | --- |
| AC01 | Completed | viewer provenance contract/code; absent/replacement/malformed legacy regression tests, own viewer PASS | None | 5% |
| AC02 | Completed | permanent route/footnote regressions plus actual Chrome direct/reload/click/Back/Forward/Unicode/repeated backlink observations in browser-review.md; pinned artifact hashes match | None | 5% |
| AC03 | Completed | EN/RU README user tasks, format/toolkit distinction, working example and continuation links; docs tests | None | 5% |
| AC04 | Completed | EN/RU landing and accessible preserved references; compiled links/navigation plus docs contract tests | None | 5% |
| AC05 | Completed | full documented quickstart final fresh-clone/current-overlay cyypib1j run; exact script/log/result exit0; prerequisites/cwd/new output specified | None | 5% |
| AC06 | Completed | meaningful architecture demo/sources/relations and supplied fictional own-note exercise; real browser opens both source and note | None | 5% |
| AC07 | Completed | Codex CLI0.160.0 request/config/events/summary/exit0: three actual tools, known answer with pinned source | None | 5% |
| AC08 | Completed | built Jekyll website/demo actual Chrome1729×907/390×844 EN/RU flow; 189 local links/10pages HTTP200 plus fragments; source/digest checks | None | 5% |
| AC09 | Completed | additive CLI/MCP schema/section contract recorded before implementation; existing literal code/schema/tests preserved | None | 5% |
| AC10 | Completed | common retrieval Go weighted BM25/Unicode/AND/deterministic ordering; EN/RU/multiterm and CLI/MCP parity tests PASS | None | 5% |
| AC11 | Completed | captured-byte spans/hash/locator/lines/revision contract/tests; own all-hit raw attribution validation, drift tests | None | 5% |
| AC12 | Completed | query/load/section/token/response limits, total/truncated, cancellation and no-follow containment regressions; existing tool tests/fullsuite | None | 5% |
| AC13 | Completed | setup source/target/selection/preview/apply/digest/collision/replay/cancel/drift contract+schemas; init compatible; CLI tests | None | 5% |
| AC14 | Completed | explicit-type managed conformant setup via shared parser/index/validator; clean quickstart and entire setup tests, no invented provenance | None | 5% |
| AC15 | Completed | exact unknown YAML/body/source/link preservation and blockers, atomic publication failure/drift/cancel tests; 501-doc/1003-output complete-copy regression | None | 5% |
| AC16 | Completed | unchanged preimplementation freeze/manifest,24EN/RUqueries,20answerable+4no-answer with manual gold evidence | None | 5% |
| AC17 | Completed | same-source baseline/final raw comparison; own0.10→0.95 Hit/MRR recalculation and all attribution checks,0/4no-answer; reproducible tracked harness | None | 5% |
| AC18 | Completed | documentation separates structural validation, integration smoke, offline retrieval and LLM quality; synthetic/performance/size limitations consistent | None | 5% |
| AC19 | Completed | command-results.json/log SHA verified; complete unfiltered go test ./... exit0, full vet0, current diffcheck0, skills lock/package0 and2/2tests, site/affected tests | None | 5% |
| AC20 | Completed | both independent final reports/ACK at identical4b41…72files; current verification, corrected findings-journal, final-state/patch manifest and unchanged corpus; F1–F7closed | None | 5% |

**Final independent completeness verdict:100% (20×5%). No partial credit, removed criteria, changed denominator or open confirmed finding.** Scope remains the fixed task A–E; no commit/push/release/deploy performed. This acceptance is evidence of the requested behavior and checks on the pinned product state, not a claim of universal retrieval gain, LLM quality improvement or absolute absence of possible bugs.

## Release follow-up — portable regression fixture checkout

Reviewed release follow-up to PR10/source commit `f629f39`, after user-authorized release. No production file is changed by this narrow delta: only viewer test fixture storage names and the fixture test loader. Independent `git show f629f39:<original-colon-path>` comparison confirms all three new portable `.md.fixture` payloads are byte-identical to the accepted original fixtures: fn:example SHA2562f031e9e86bc40e60247489ecf425e1d663debb8d312991bf80ed0bca1132808; fnref:example ade87eb55d03c521a06c5ed7dc248f3473833c8f01fa84c241e6bab32ade9620; fnref1:example1065dfc2ded2250b2bbc34a505d4adfc386181d2355a1aa10722c4f245ce4466.

`TestRouteFixturePreservesFootnotesAndUnusualIDs` materializes the same original colon filenames via the existing fixture helper on supported hosts; every concept, actual Goldmark footnote/backlink markup, legacy source and resolved-edge assertion remains unchanged. Explicit Windows skip applies only to this filesystem-dependent integration test, because NTFS cannot represent those names. Windows CI currently compiles packages and executes typed unsupported-platform/cancellation store tests rather than a viewer runtime suite; no existing Windows viewer execution coverage is removed. Production JS routing regression remains unchanged.

Independent affected command completed exit0: `GOCACHE=/tmp/okf016-review-complete-cache GOMODCACHE=/tmp/okf016-mod go test ./viewer -run 'TestRouteFixturePreservesFootnotesAndUnusualIDs|TestViewerRouteRegression' -count=1 -timeout=20m` —0.221s. AC02 regression coverage is preserved, and previous real-browser observations remain applicable to identical runtime/fixture bytes. No missing user-facing implementation scope found.

Reproduction detail: direct `okf view viewer/testdata/regression-016` no longer includes `.md.fixture` payloads. Browser regression export must first copy the `.md` files and materialize the three portable fixtures to their original colon `.md` names in a temporary directory on a supported host, then export that directory. Root was notified to record this in release-follow-up evidence; historical direct-export commands describe the original accepted layout.

The earlier100% task verdict applies to its pinned accepted product state. This follow-up does not claim that pending release CI has passed: affected local viewer coverage is verified; root owns full viewer result and subsequent CI rerun/release bookkeeping. No product edits made by this reviewer.
