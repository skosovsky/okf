# Viewer localization implementation

Contract was updated in `viewer/README.md` before implementation. Added `Options.Language`, `RenderOptions`, `RenderWithOptions(ctx, Projection, RenderOptions)`, and `okf view --lang en|ru`. Empty API language and existing `Render` default to English. Unknown languages fail before output publication, replacement or temporary file creation. Presentation language never enters semantic projection JSON.

The initial shell and production embedded JavaScript support English and Russian. Runtime language selection changes page language, title, labels, counts, provenance/relationship panels, graph labels, truncation messages, empty states and known status/trust/staleness/reference-basis values. Labels are category-specific. Authored content, identifiers, source titles and custom values remain unchanged. No network, storage or added permissions; CSP remains the SHA-256 of the actual embedded production script. Locale changes preserve the concept route (including genuine footnote anchor routes), search, type filter and graph toggle. Reload starts with the exported locale. Header wraps for narrow screens.

Verification completed:

- `node viewer/testdata/regression-016/routes.cjs`: PASS. Executes production asset; encoded Unicode and fn/fnref/fnref1 concept precedence, footnotes and repeated backlinks, simulated history; EN/RU switching/state retention, empty states, category-specific known/custom values and hostile source URL handling. This simulated DOM is not browser acceptance.
- `GOCACHE=/tmp/okf017-cache GOMODCACHE=/tmp/okf016-mod go test ./viewer -count=1`: PASS (0.915s).
- `GOCACHE=/tmp/okf017-cache GOMODCACHE=/tmp/okf016-mod go test ./viewer ./internal/okfcli -run 'Test.*(View|Render|Export|EmptyBundle|Language)' -count=1`: PASS (0.521s / 0.549s). CLI Russian output, unsupported locale no create/replace, API default equivalence, unchanged JSON, escaping and actual CSP digest.
- `GOCACHE=/tmp/okf017-cache GOMODCACHE=/tmp/okf016-mod go test -race ./viewer ./internal/okfcli -run 'Test.*(View|Render|Export|EmptyBundle|Language|Production)' -count=1`: PASS; final rerun after static shell/selected-option and responsive changes: 1.559s / 1.575s.
- `GOCACHE=/tmp/okf017-cache GOMODCACHE=/tmp/okf016-mod go vet ./viewer ./internal/okfcli`: PASS.
- Scoped `git diff --check`: PASS.

Native browser acceptance, generated site demos and final repository-wide tests are coordinated by the parent agent. No commit or push performed by this implementation agent.

## Teaching demo exports and CI

Added `scripts/build-project-demo.sh en|ru OUTPUT.html`. It validates `examples/project-knowledge/<lang>` against explicit OKF 0.2 with strict links/orphans, zero allowed warnings and fixed 2026-09-26 reference date; exports the matching interface language. Existing output, symlink output and missing parent are rejected before exporting. Existing `build-viewer-demo.sh` is unchanged.

CI now explicitly runs `node viewer/testdata/regression-016/routes.cjs` and rebuilds each EN/RU teaching export twice, compares the two generated files, then compares the tracked site snapshot. Existing knowledge snapshot gate remains intact. This is local validation of the CI commands; no GitHub execution is claimed.

Locally exported all three snapshots twice: strict validators PASS with zero errors/warnings; all three `cmp` checks PASS. Invalid language/arguments, output collision, symlink and missing-parent cases PASS; `sh -n` and scoped whitespace check PASS. Final snapshot SHA-256:

- `docs/demo/knowledge.html`: `9126d4d07d7bfa0e4b564dd201def3de38e8de24e8267bf600f43d27d93499fd`
- `docs/demo/project-en.html`: `84afd37b1adec4f1fd0b4f76ecd1b434aaa4d3bd8dab77fd0c9426d54ab01ccc`
- `docs/demo/project-ru.html`: `5562dd142765f29dccb8e50c5e8024034f29419715ca924282f81ee384add88c`

Parent coordinates native browser testing of these exact snapshot bytes.

## Browser count finding corrected

Russian note, edge and graph connection counts now use singular/paucal/plural forms with the 11–14 exception. Production JS tests cover 0,1,2,4,5,11,12,14,21,22,25,111. All three demos were exported twice again; strict validation and deterministic comparisons PASS. Updated snapshots:

- `knowledge.html`: `8981fa2200cbdc2d1b469e4c8ebc7aad4ce9b25c10d16c839e2d8d354996ae35`
- `project-en.html`: `4052f0b88baf16779c125eeac0584773398754b32664d42cc40669d7d750bc3d`
- `project-ru.html`: `ced05cedfc42d6d6cbd4fa6b06501d89800e089d2a0d77aa515b41e1bea006a1`

## Complete viewer reading editions

Created paired `docs/readings/viewer/README.md` and `docs/ru/readings/viewer/README.md` covering every current canonical README option, API, security, projection, provenance and routing guarantee. Both explicitly distinguish the published baseline `source_revision` from task 017 working-contract additions; neither claims the updated language contract existed at baseline. All five stable section anchors and interface/time/link identifiers match. Navigation uses `document_id: viewer-readme`; operational recipe points to its actual paired `section-3` anchor. Registry unchanged.

## Windows-compatible regression guide reading editions

Added complete EN/RU reading editions of `viewer/testdata/regression-016/README.txt` at both registry paths. Original .txt matches pinned 61e75e9 bytes exactly (SHA-256 `1c3ca97cf6def1a0d4a12807ea023ffa877f8cd6610370a559adb88fd9157068`) and remains unchanged. Both explain Windows colon filename restrictions, test skip, independent JavaScript tests and materialization before native browser export. Shared page-top/materialize anchors, localized navigation and pinned provenance supplied. Both command blocks are byte-identical, preserve the original recipe, and the extracted recipe executed successfully in a fresh temporary directory.
