# Issue016 findings and closure

Product state: final-state.json digest4b41bb383a31c260213ab7cae10e69d972f89862ade3d7c9eb34470b8b080a11,72files. No commit/push/publish. Acceptance criteria unchanged.

| Finding | Confirmed behavior | Fix and regression | Independent status |
| --- | --- | --- | --- |
| F1 P1 setup publication loss |501 selected nested docs produced1003 outputs but Apply silently published1001 | Apply enumerates all derived outputs;501-doc regression checks all byte-exact copies,502indexes,links,sourceunchanged and1003 inventory | Correctness CLOSED |
| F2 P2 Unicode output schema | ß×512 accepted but folded1024-character term violated512outputbound | Explicit normalizedtermcap4096, schemas+implementation; ß/ﬃ×512 MCP real output and schema tests | Correctness CLOSED |
| F3 P2 autolink bypass | file/ftp/javascript angle autolinks skipped common URI checker | AutoLink+email handling, blockers and allowedHTTPS/mailto/email regression | Correctness CLOSED |
| F4 P2 reference404 | Relative .md hrefs under permalink resolved to wrong publicpaths | Liquid site routes/exact advanced-sourceURLs; compiled path/fragment and HTTP checks | Correctness CLOSED |
| F5 P2 copyable sample | Minimal localindexlink was rewritten to nonexistentremoteURL | Relative minimal.md restored; independent two-file strictexamplevalidation | Correctness CLOSED |
| F6 P2 repeatedfootnotes | Goldmark fnref1:3 secondbacklink lost href | Goldmark numbered namespace recognized; ConceptIDfnref1:example keepspriority; Go+productionJS negativecontrol and realChrome checks | Correctness CLOSED |

F7: the initial fullsuite also found benchmark/agent source-pin drift in the literal CLI help excerpt. Only new help rows were moved after the legacy command block; dispatch and all corpus bytes remained unchanged. F7 CLOSED: target source-pin test and the final full suite passed. go-test-all.log records exit 0, verified independently by both reviewers. Prior reviewed state141326… differed only in internal/okfcli/run.go help ordering; all browser and MCP/retrieval behavior is unchanged. Both reviewers verified the unchanged 4b41bb… product state.

Additional root checks repaired snippet selection for a final unterminated word (cancellation regression included) and compiled RU language metadata (page.lang plus ru defaults, html lang=ru observed). All are part of the same reviewed product state. No finding was moved outside scope or dismissed without verification.

Initial completeness80%, then90% after browser/evidence checks. AC19 is verified: the final full suite, vet and skills checks passed. AC20 verified: completeness reviewer issued final 100% on the corrected journal and both independent reports. Task, index, matrix and verification are synchronized; acceptance-only edits were checked separately. Initial correctness reports retained inside correctness-review.md; final closure explicitly identifies same-state digest and coverage/limitations.

## Release follow-up F8 — Windows checkout

PR CI found Windows checkout rejected colon characters in three regression fixture filenames. The three byte-identical templates now have portable `.md.fixture` filenames; the Go test materializes the original names in a temporary Unix directory using the existing helper. Assertions are unchanged; Windows skips only this unrepresentable-filename fixture test. Production JS routing coverage remains independent. README.txt documents manual materialization before browser export; historical browser proof used the same original bytes. Local full viewer tests passed (0.765s). Both independent reviewers inspect this narrow release delta; final CI links are recorded in GitHub Release.
