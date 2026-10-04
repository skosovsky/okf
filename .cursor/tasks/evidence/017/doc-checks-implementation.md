# Documentation controls — task 017

The automated suite checks structural coverage, executable contract owners and navigation. It deliberately does not label matching tokens or anchors as proof of semantic parity. Independent editorial review owns completeness, language quality and every EN/RU pair's factual correspondence.

## Replaced controls

| Previous control | Replacement | Retained guarantee |
| --- | --- | --- |
| `TestPublishedEnglishRussianSemanticParity` demanded particular EN/RU explanatory sentences, including mixed-language Russian | Registry inventory, existing paired files, unique paths and routes; matching stable primary-guide section IDs | Every document has an identified edition and primary language links have corresponding section targets. Semantic comparison remains an independent review criterion. |
| Every onboarding page had to repeat technical migration guarantees | Reference catalogue requires documented migration ABI fields and manual-action codes in both EN/RU reference pages; normative edition owner/revision checks | Canonical details stay discoverable in the reference; guides may link rather than duplicate DTO rules. |
| Russian reference required exact words, while allowing missing whole catalogues | Both references checked against actual `mcpserver.ServerTools()` registration, input/output schema files and actual CLI dispatch AST | Missing public commands or MCP tools fail regardless of prose wording. |
| Whole-repository legacy occurrence SHA locked offsets in user-facing guides | Whole-file SHA256 for preserved normative/skill/history originals; legacy classification checks for fixed compatibility inputs; original lock metadata retained | Immutable originals remain exact; fixed v0.1 evidence remains explicitly classified; translation does not require changing fixture metadata. |
| Published links understood only the first demo and rejected a fragment outside Liquid | Links resolve real published permalinks or actual demo files; fragments outside the Liquid route accepted | New EN/RU demos and stable-section links use the same route validation. Compiled anchor and browser checks remain required acceptance evidence. |

## Unchanged controls

- Migration validation/source/store call ordering and owned boundary AST checks.
- Canonical embedded MCP JSON schemas and resolvable `$ref` inventory.
- Manual-action enum/runtime owners and migration DTO/wire-field owners.
- Runtime skill reference routes and ABI fields.
- Fixture corpus/provenance ownership, executable expected validator diagnostics.
- Adversarial inert executor bytes, absence of invented receipts, structured lifecycle precedence over body claims.
- Plugin/skill manifest identity and registration consistency.

## Implementation and initial verification

Only `docs/contract_test.go` and the new `docs/documentation_registry_test.go` changed. First test run compiles and exercises the new checks; expected integration failures identify pages/translations/demos still being implemented by other agents. The CLI AST extractor was corrected to inspect actual `runWithDependencies` dispatch, rather than the public `Run` wrapper. Final integration results are recorded by the root task after all documentation editions are present.

Cache isolation: `GOCACHE=/tmp/okf017-cache GOMODCACHE=/tmp/okf016-mod`.

Preserved behavioral/ownership checks passed independently: `go test ./docs -run 'TestMigrationHandlers|TestCorpusAndProvenance|TestAdversarialEvidence|TestPluginManifests|TestLegacyRepresentations' -timeout=5m` (exit 0, package 0.487s).

Baseline coverage now additionally freezes all 95 original source-path + SHA256 records, sorted and encoded as path + NUL + digest + LF: `643674be19acf8f31af0119980ee5de7515ff0754137171b0b0f09d77550b6c6`. New additive documents have empty baseline hashes/revisions; removing original and both editions cannot silently reduce scope. Canonical source links may resolve through the registered English permalink; both Liquid quote styles and existing static JSON/HTML assets are supported.

## Independent scope correction

Independent technical review found two human-readable instructions/report documents incorrectly excluded with test data: `fixtures/interoperability/README.md` and `viewer/testdata/regression-016/README.txt`. The agreed scope explicitly includes such documenting READMEs. Both originals remain byte-exact at revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`; their EN/RU reading editions are now registered. This expands baseline coverage from 95 to 97 rather than weakening any criterion. Original `baseline-registry.json` stays immutable; `registry-scope-correction.json` records the additive entries. Updated frozen sorted source-path + NUL + SHA + LF digest: `a96b4d3aced216425631a22ee6c5c70a26b9f83554cd05948aa27d49d19ce24b` (97 records).
