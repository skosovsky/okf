# Independent correctness review — task 016

Reviewer: final_correctness. Baseline commit: `8e822ea631caaac058fb40aa65c5821352048278`.
Initial product snapshot: 68 changed/untracked files excluding `.cursor/`, aggregate SHA256 of sorted path→file-SHA256 JSON: `0c0849e05c897ddf4e5d6b68bcb119cba9f3a516ec3d0ed57bf66aa2039175c8`. Per-file record: `/tmp/okf016-correctness-digests.json`. Product files were read only by this reviewer; only this evidence report was written.

Status: final affected-scope re-review completed. F1–F7 are CLOSED with independent verification below; no open confirmed correctness findings remain in the reviewed scope. Historical initial findings are retained for the audit trail. No other final reviewer's findings were consulted.

## Confirmed findings

### F1 — P1: silently truncated publication loses selected documents

Original `setup/setup.go:375–378`: `Apply` stops collecting `out` names after 1001 entries, although the cap concerns 1000 source documents and generated indexes also occupy entries. Selection happens while ranging a map, making the omitted paths nondeterministic.

Reproduction: `/tmp/okf016-setup-repro.go`, `GOCACHE=/tmp/okf016-review-error-cache GOMODCACHE=/tmp/okf016-mod go run /tmp/okf016-setup-repro.go`. It creates 501 directories with one `note.md` each. Preview: applicable=true, docs=501, no diagnostics. Apply: published=true, err=nil. Actual published files:1001; expected:1003 (501 notes,501 nested indexes,root index). Observed missing original `d033/note.md` and generated `d259/index.md`. Target: `/var/folders/46/5ywmz5gj26n7mnky51gd60g00000gn/T/okf016-truncate-2815889006/target`.

Consequence: successful managed copy silently drops source data and may publish broken navigation. Shared validator's base ErrorCount alone does not detect this loss (observed zero).

Fix verification: retain complete output inventory, including generated indexes; add 501 nested documents regression asserting every source file and index exists after apply and byte/digest correctness, not just conformance.

### F2 — P2: Unicode normalization makes valid CLI/MCP queries diverge

Original `retrieval/search.go:QueryTerms`, `internal/mcpserver/contracts/search_sections.output.schema.json` terms/matched_terms maxLength=512. Input permits 512 Unicode characters, but case folding expands some characters.

Reproduction: `/tmp/okf016-unicode-repro.go`: `ß` repeated512 produces one normalized term of1024 runes, QueryTerms=nil error; Search succeeds with no hits and term1024. Built actual stdio server `/tmp/okf016-review-mcp`; `/tmp/okf016-mcp-unicode.py` sends same512-rune query on frozen corpus and obtains `isError:true`, code=`resource_limit`, message=`tool output exceeds the advertised schema limits`.

Expected: CLI and MCP have the same accepted query/result semantics. Actual: Go/CLI success vs MCP output contract rejection. Consequence: ordinary valid bounded Unicode inputs can fail only at wire serialization.

Fix verification: publish an explicit bound for normalized terms independently of input length, enforce it in the shared layer, align schema, and test expanding casefold `ß` and `ﬃ` at input cap through real output contract (including hits/matched_terms).

### F3 — P2: unsupported URI autolinks bypass setup diagnostics

Original `setup/setup.go` link inspection switch handles Link/Image/HTML but omits `ast.AutoLink`.

Reproduction: `/tmp/okf016-links-repro.go`: `<file:///etc/passwd>`, `<ftp://example.test/asset.png>`, `<javascript:alert(1)>` each yields applicable=true, no diagnostics. `[file](file:///etc/passwd)` yields applicable=false, unsupported_link. All previews contain otherwise valid ordinary Markdown with explicit type.

Expected: unsupported schemes block publication irrespective of Markdown link syntax. Actual: autolinks bypass check. Consequence: false successful setup with links forbidden by documented contract. This is a link-preservation failure, not evidence of executing content.

Fix verification: include AutoLink URLs/email handling in the same URI policy; test rejection for file/ftp/javascript autolinks and retention of supported http/https/mailto.

### F4 — P2: new reference pages contain broken published-site links

`docs/reference.md:31,36,88,191,336–337,398,432`; `docs/ru/reference.md:69,107`.

Independent HTTP reproduction against root's actual Jekyll build `/tmp/okf`, served at `http://127.0.0.1:8766/okf/`: parse anchor hrefs in `/okf/reference/` and `/okf/ru/reference/`, resolve relative links, request each local URL. Twelve links return404. EN URLs resolve under `/okf/reference/`, including `quickstart.md`, `knowledge.md`, `contracts/cli-init.md`, `adr/0003-okf-v02-temporal-revisions.md`, `contracts/concept-queries.md`, `contracts/section-search.md`, `migration.md`, `contracts/markdown-setup.md`. RU resolves `/okf/ru/contracts/section-search.md` and `markdown-setup.md`.

Expected: moved detailed contracts remain accessible through published reference. Actual: navigation fails. Fix verification: rebuilt HTML hrefs resolve to actual routes or deliberate valid repository URLs; HTTP-check every affected link after rebuild.

### F5 — P2: minimal bundle sample no longer indexes its local note

`docs/reference.md:101` shows `* [Minimal](https://github.com/skosovsky/okf/blob/main/minimal.md)` inside the copyable Markdown index example. Baseline README sample uses `(minimal.md)`; repo has no root `minimal.md`.

Expected: two copyable minimal files form a bundle whose index navigates to the local `minimal.md`. Actual: generated sample index points to an unrelated nonexistent remote path, leaving local note unindexed. Consequence: broken copyable example and misleading navigation/coverage.

Fix verification: restore relative `(minimal.md)` in fenced sample and repeat the two-file example with link/orphan checks.

## Coverage and limits

Read implementation/contracts/tests for common Go retrieval, CLI/MCP adapters, query/output schemas, source pinning, Unicode, physical line/locator handling, ranking/ties, cancellation and caps; setup plan/digest, raw YAML preservation, no invented metadata, selection/reserved paths, collision/drift/cancellation and atomic no-replace publication; viewer provenance fallback and route asset/regressions; EN/RU README, quickstart, MCP/reference and retrieval benchmark freeze/scorer/mapping.

Adversarial standalone repros above were executed independently. Package test command started: `GOCACHE=/tmp/okf016-review-error-cache GOMODCACHE=/tmp/okf016-mod go test ./retrieval ./setup ./viewer ./internal/mcpserver ./internal/okfcli ./benchmarks/retrieval/cmd/okf-retrieval-eval -timeout 20m`; initial retrieval/setup/viewer output passed; remainder pending at report creation. Root is responsible for separately recorded full suite/vet, live client and real-browser acceptance; this reviewer does not substitute DOM simulation for that evidence. No absolute absence of other errors is asserted.

## Product changes during review

Expected fixes changed these originally captured files during review: docs/contracts/section-search.md; internal/mcpserver/contracts/search_sections.output.schema.json; internal/mcpserver/section_search_test.go; retrieval/search.go; setup/setup.go; setup/setup_test.go. Root was informed. Initial findings describe initial snapshot; a fresh digest and affected-scope re-review are required after fixes settle.

## Final re-review — fixed product state

Independently verified `.cursor/tasks/evidence/016/final-state.json`: baseline remains `8e822ea631caaac058fb40aa65c5821352048278`; patch SHA256 `1ab8cf228d8da85079177b36ff9241608bba1c5369bac023e52ab2116845ffae`; final product digest `141326454ef6c42d7956c9f88424368d3825f824dd43919db587c44f29fb3b76`, 72 files. Checked every file's current bytes,size,mode against manifest, checked current changed tracked+nonignored untracked inventory using NUL-separated Git output (including Unicode filename), recomputed compact UTF8 JSON SHA256 with manifest field order. All matched. No later product changes observed by this review.

| Finding | Final status | Independently executed fix verification |
|---|---|---|
| F1 silent setup truncation | CLOSED | Complete `out` collection now has no1001 cap. `TestApplyPublishesAllNestedDocumentsAndIndexes` passed on501 source docs, checking every source/copy byte,501 nested indexes,root entries and1003 published files. |
| F2 expanding Unicode query output | CLOSED | Shared layer publishes/enforces4096 normalized-term rune cap; schema bounds match. `TestSectionSearchCaseFoldExpansionConformsToOutput` passed (`ß`/`ﬃ`×512 including actual hit/matched_terms and output contract). Rebuilt actual stdio MCP independently and repeated original `ß`×512 request: successful structured result,1024-rune normalized term,no error. |
| F3 unsupported autolink URI | CLOSED | Inspected AutoLink URL/email switch into shared URI policy. `TestAutolinksFollowExplicitURIPolicy` passed preview/apply rejection and supported byte retention. Repeated original standalone repro: file/ftp/javascript autolinks all applicable=false with unsupported_link; ordinary supported local links still applicable=true. |
| F4 reference-site broken links | CLOSED | Independently requested all55 local anchor hrefs from actual rebuilt EN/RU reference HTML on `http://127.0.0.1:8766/okf/`, checked HTTP200 and presence of decoded fragments in destination HTML; all passed. Compiled RU reference html lang=ru, EN=en-US. |
| F5 minimal bundle sample link | CLOSED | Parsed the actual two Markdown fenced blocks from fixed docs/reference.md into a fresh temp bundle. `/tmp/okf016 validate --spec0.2 --check-links --check-orphans --max-warnings=0` returned exit0,0 errors,0 warnings; index points to local minimal.md. |
| F6 repeated footnote backlinks | CLOSED | See independent verification below. |

F6 was raised by root after its browser acceptance: repeated Goldmark references produce `fnref1:3`, while original `/^#fn(?:ref)?:/` rejected this backlink and removed href. Independently confirmed the issue with a negative control: copied production asset/routes harness to temp, restored only old regex, and ran Node; repeated-reference click assertion failed (backlink absent). Current production `fn(?:ref\d*)?` regex passes the same permanent routes regression. Independently parsed current compiled demo's embedded mutation projection: both `href="#fnref1:3"` and `id="fnref1:3"` exist. `TestRouteFixturePreservesFootnotesAndUnusualIDs` and production Node route harness pass, including genuine repeated anchors and actual `fnref1:example` concept route priority. This reviewer attempted an independent browser tab: IAB unavailable; Chrome create timed out/reset. Root's separately recorded real-browser click/history/footnote acceptance remains required and is not replaced by Node/static HTML verification here.

Completed prior reviewer package command (session68938) successfully: retrieval0.961s,setup1.069s,viewer4.638s,MCP253.987s,CLI126.938s,benchmark0.229s. It began before final fixes, so it is supporting broad regression coverage, not the final whole-suite acceptance.

Final targeted command (exit0):

```sh
GOCACHE=/tmp/okf016-review-error-cache GOMODCACHE=/tmp/okf016-mod go test ./setup ./retrieval ./viewer ./docs ./internal/mcpserver ./benchmarks/retrieval/cmd/okf-retrieval-eval -run 'TestApplyPublishesAllNestedDocumentsAndIndexes|TestAutolinksFollowExplicitURIPolicy|TestSectionSearch|TestSourcesPresenceOnlyLegacyFallback|TestDeclaredV01LegacySourcesRegression|TestViewerRouteRegression|TestRouteFixture|Test.*Reference|Test.*Freeze|Test.*Score' -count=1 -timeout 20m
```

Actual matched tests passed: setup26.238s,viewer0.646s,docs0.412s,MCP0.558s. Retrieval/benchmark had no test matching this selective pattern; consequently they were run in full separately on final state:

```sh
GOCACHE=/tmp/okf016-review-error-cache GOMODCACHE=/tmp/okf016-mod go test ./retrieval ./benchmarks/retrieval/cmd/okf-retrieval-eval -count=1 -timeout 20m
node viewer/testdata/regression-016/routes.cjs
```

Both packages and Node passed (retrieval0.576s,benchmark0.193s). Reviewed fixes remain additive: literal search contracts unchanged; source digest/section bounds still checked; unsupported-link fix uses same URI policy and preserves supported bytes; complete setup inventory retains atomic publication/collision/source-drift/cancellation behavior; footnote namespace widening preserves concept priority and legacy fallback remains presence-only.

Final correctness conclusion: all independently confirmed F1–F6 are closed, and no additional confirmed defect remains open within this review's scope at the pinned final product digest. This does not claim absence of every possible error. Root's complete final `go test ./...`, vet, package/lock, built-site browser and actual-client evidence are separate acceptance requirements; no pending overall acceptance claim is made by this correctness report.

## Final narrow follow-up — F7 legacy source-pinned benchmark

Root's full suite subsequently surfaced a source-pinned fixture regression in `benchmark/agent:TestRealisticCasePinsActualGoSource`: `repo-parse-output` controls contain the original contiguous CLI help `COMMANDS` excerpt through `version`. Inserting `search`/`setup` immediately after `COMMANDS` broke that fixed excerpt, causing the existing package test to fail. This is a regression of existing frozen benchmark infrastructure (P2), not evidence of retrieval quality failure. Frozen fixtures/tests must not be changed merely to make the implementation pass.

F7 status: CLOSED. The only follow-up product change moves the two additive help rows after `version` in `internal/okfcli/run.go:41–42`, preserving the original contiguous block. Runtime dispatch and command semantics are unchanged. `benchmark/agent` corpus/tests have no product diff. Independently executed:

```sh
GOCACHE=/tmp/okf016-review-error-cache GOMODCACHE=/tmp/okf016-mod go test ./benchmark/agent -run TestRealisticCasePinsActualGoSource -count=1 -timeout 20m
```

Result: PASS,0.204s,exit0. Independently inspected the test's actual source/quote/control-substring checks and current986-byte `repo-parse-output` artifact.

Current final product digest is `4b41bb383a31c260213ab7cae10e69d972f89862ade3d7c9eb34470b8b080a11` (72 files) from the refreshed final-state manifest. Every current file hash and aggregate digest matches. To verify unchanged F1–F6 reviewed bytes rather than assume scope: reconstructed the prior help ordering in memory, substituted only its SHA256 in the current manifest, and recomputed aggregate; it exactly equals previous reviewed `141326454ef6c42d7956c9f88424368d3825f824dd43919db587c44f29fb3b76`. Thus every other reviewed product file and inventory is byte-identical, and only help row ordering changed; F1–F6 closures carry forward without broad retesting.

No open confirmed correctness finding remains in this review's scope at current digest `4b41…0a11`. Final whole-repository suite and acceptance evidence remain root's separate checks. Earlier final digest references above are historical reviewed states, not the latest state.

Additional affected CLI check independently passed (0.471s,exit0): `GOCACHE=/tmp/okf016-review-error-cache GOMODCACHE=/tmp/okf016-mod go test ./internal/okfcli -run 'TestHelpDocumentsReservedCitationReplayEvidenceContract|TestSetupCLIJSONPreviewAndApply' -count=1 -timeout 20m`.

## Final synchronization after complete repository checks

Current product digest remains `4b41bb383a31c260213ab7cae10e69d972f89862ade3d7c9eb34470b8b080a11`,72 files. Independently rechecked every manifest SHA256,size,mode; exact changed/untracked inventory; recomputed aggregate. No product difference since F7 follow-up.

Read final `go-test-all.log`, `command-results.json`, and `verification.md`. Final unfiltered `go test ./... -timeout=30m` is recorded exit0/session15606; complete log includes benchmark/agent PASS7.019s, MCP PASS190.781s, CLI PASS99.549s, viewer PASS7.891s and valid cached unchanged packages, with no FAIL/panic/timeout. Independently verified all referenced logSHA256 and successful recorded exits against command-results.json, including fullsuite,vets,skills lock/package and package-tests. Fullsuite SHA256 `96ecb87a39d3223ac90edfeed1186eca565bd42d77ee6d7e24c00c9839235542` matches current bytes. Historical failed/interrupted runs are explicitly retained as non-acceptance evidence and are not confused with this successful final run.

Synchronization acknowledgement: F1–F7 remain CLOSED; no open confirmed correctness finding remains within this independently reviewed scope at `4b41…0a11`. Final correctness report is ready for AC20 synchronization with the independent completeness reviewer. No new fullsuite was run by this reviewer; no product edits or other reviewer's conclusions were used to reach this conclusion. Absolute absence of possible bugs is not asserted; scope/limitations above still apply.

## Release follow-up — portable regression fixture checkout

Base released-work commit reviewed: `f629f39`. Narrow uncommitted follow-up inspected independently: only viewer test code and three regression fixture storage paths change. Renamed `fn:example.md`, `fnref:example.md`, `fnref1:example.md` to `fn-example.md.fixture`, `fnref-example.md.fixture`, `fnref1-example.md.fixture`. Compared each new file directly against `git show f629f39:<originalpath>`; all bodies are byte-identical. Their SHA256 remain respectively `2f031e9e86bc40e60247489ecf425e1d663debb8d312991bf80ed0bca1132808`, `ade87eb55d03c521a06c5ed7dc248f3473833c8f01fa84c241e6bab32ade9620`, `1065dfc2ded2250b2bbc34a505d4adfc386181d2355a1aa10722c4f245ce4466`.

`TestRouteFixturePreservesFootnotesAndUnusualIDs` now materializes the original colon-bearing filenames in the existing temp-directory fixture helper, then uses the real bundle loader/parser. Its assertions still require all unusual ConceptIDs, Goldmark repeated footnote markup, Sources fallback and resolved edges. Windows explicitly skips only this filesystem fixture test because colon filenames are unrepresentable there; portable production-JavaScript route regression remains independent. No production implementation or shipped demo changed.

Independently executed `GOCACHE=/tmp/okf016-review-error-cache GOMODCACHE=/tmp/okf016-mod go test ./viewer -count=1 -timeout 20m`: PASS,0.567s,exit0. No new confirmed correctness finding from this narrow follow-up. The earlier persistent bundle directory is now a template store rather than directly loadable with all unusual IDs; historical browser export evidence describes the earlier byte-equivalent bundle, while Go regression materializes it explicitly. New cross-platform CI completion is root's separate release verification; this reviewer did not claim Windows CI ran locally.
