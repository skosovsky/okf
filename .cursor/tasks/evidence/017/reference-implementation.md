# Reference and migration guide implementation

Owned files: `docs/reference.md`, `docs/ru/reference.md`, `docs/migration.md`, `docs/ru/migration.md`. No contract or API implementation changed by this agent.

Both references were rewritten as equivalent heading-by-heading guides to exact behavior rather than making Russian a summary. Public routes remain `/reference/`, `/ru/reference/`, `/migration/`, `/ru/migration/`. Explicit ASCII heading IDs are identical within each language pair. Existing ordinary user prose was translated; identifiers, API signatures, grammars and error codes remain exact.

## Preservation map

Read original `git show HEAD:docs/reference.md`, `docs/skill.md`, `docs/toolkit.md`, and current pre-rewrite migration guide before modifying owned files. Exact repeated invariants now have one canonical home in both reference versions:

| Original guarantees | New paired reference anchor |
| --- | --- |
| Independent document/program/plugin versions, declared/absent/malformed/future/explicit version behavior, temporal revision selection | `versions` |
| Installation/version inspection, absent-new-directory init contract, minimal `index.md`/concept examples, sole always-required `type` | `installation` |
| Known producers/materials, keyed footnotes, actual verification, independent trust/status/freshness, unknown data, presence-only version-agnostic §13 fallback, active parser-owned citation headings | `authoring` |
| Base/strict/links/orphans separation; optional/unknown/broken/index behavior; reserved introductions and unknown root fields; wrapped log lines; code-span attribution | `validation` |
| Public separate document/body 16 MiB ownership limit; cancellation/UTF-8/resource/parser order; saturated observed size and zero Context result | `validation` |
| Complete actual CLI dispatch including `help`, new retrieval/setup/viewer language, deterministic as-of, no hidden migration in parse/fmt/index/graph | `cli` |
| BYOT lossless YAML, caller structs/Get/nodes, defensive copies, inert assets, desired state/staged final validation/CAS/journal, independent store wire and actor contracts | `go` |
| Actual search matching/ranking/line/file fingerprints and resource caps; setup preview/digest/source-preservation/no inferred metadata | `search` |
| All fourteen MCP tools, compatibility text, catalog/schema validation, absolute per-call bundle path/no-root flag, truncation/no-cursor/resource-failure semantics, literal ID/metadata/body ranking, patch revision+digest and trust/path/permissions | `mcp` |
| MCP-only 256-byte actor cap before semantic `ValidActor`, not bundle/store actor grammar; canonical decimal usage_count/null/uint64 overflow and selector union rules | `mcp-fields` |
| Pre-resolution domain validation even rootless/noop; producer versus transaction principal; absent actor manual action; type-only version-only migration; explicit existing paths and missing-path no-actions; no invented migration metadata | `migration-input` |
| Exact closed top-level array; number/raw/both selectors; root/log/nested paths; UTF-8 1–4096/nonblank/NUL/C0/TAB/LF/CR/DEL; exact bytes/newlines; duplicate/reuse/overlap/canonical order/consistent metadata/unknown-field rejection | `citation-mappings` |
| Only parser-owned claim rewriting; code/HTML opacity including multiline inline code; target-noop replay assertions; selected-marker absence/keyed-ref presence; exact-span mismatch/no proof/manual invention; normalized footnote-label collision taxonomy | `migration-replay` |
| Resolve once/full frozen expected_source/evidence; malformed/future source restrictions; proof format 2 + resolution/plan digests; no earlier proof; branch discrimination/no separate expected_revision; complete content-free proof arrays/path/ref/change summary; blocked/noop omission; authenticated transition-noop before store open | `migration-proof` |
| Full staged validation/atomic publication/root physical write/rename last; preserve unknown data; byte/path-identical non-publication/no .okf; identical successful replay; MCP live-noop vs CLI preview vs CLI receipt-publication no-op | `migration-writes` |
| All eight stable manual-action enums; entry/destination ambiguity distinct; missing-path and replay errors have no invented action; preparation and normative original references | `migration-actions` |
| Date→instant operation separate from v0.1 migration; explicit each value/no invented midnight; sources[N] current positional index; unresolved block; exact preview revision+digest/rebuild CAS; unsupported YAML presentation rejection | `temporal-upgrade` |
| Extension not upstream format; graph profiles separate; all exporters; hash/backslash canonical relation grammar, YAML/JSON representations, unchanged references/schema/receipt-v1 lexical wire ordering | `relations` |
| Inert computation/executor/attester; separate trusted runtime and authorization; only declared parameter values/no author or edit; runtime/receipt/ABI/sandbox/cache not defined; transaction/runtime receipts separate; body prose cannot override authority and LLM prose not attestation | `computation` |
| Three registered self-contained skills; binary and backfill protocol dependencies; no granted permissions; CLI/manual fallback; viewer local outside-root/authorized overwrite; upkeep baseline+final fingerprint; backfill extract/analyze/review/authorize/verify; dependency/catalog/packaged-link validation | `skills` |
| Go/vet/whitespace/skill-lock/package checks; host activation not exercised by filesystem CI; actual-host release claims require actual-client validation; public repository knowledge references | `development` |

The CLI citation input limits are explicitly scoped separately from shared Markdown/parser limits and retrieval limits. Russian references link to Russian contract pages where site routes exist. Exact upstream and implementation source links remain identified as English originals.

## Human migration flow

Both languages now show the same complete fictional example: 24-hour delivery retries followed by operator escalation; a source note and legacy `[1]` claim; explicit citation mapping; external preparation directory and manually reviewed source fingerprint; guarded preview then write; strict validation and local-language HTML export. The prerequisite explicitly adds the quickstart-built binary to PATH in the same shell.

Detailed transport caps, selector byte rules, CAS/proof bindings, C0 constraints and no-op receipt distinctions moved to the reference. Migration guide retains practical blocked cases and directs readers to exact reference sections.

## Verification

- `git diff --check` passed after four owned edits and EOF cleanup.
- EN/RU heading IDs and substantive sections match by construction and manual review.
- No edits to normative original files, fixed fixtures, benchmark corpus, source knowledge, implementation or tests.
- Exact command reproduction assigned to the independent normative-translations agent; its result is recorded separately and must be complete before final acceptance.

## Follow-up verification and editorial correction

Independent exact EN/RU migration command reproduction passed exit 0 for both: original timestamps retained, producer `human:trainer`, supplied requirements source and keyed footnote, strict v0.2 validation with zero warnings, correctly localized HTML. See `guide-command-validation.md` and `migration-en/ru.sh/log` evidence.

Added the exported `backfill` and `upkeep` packages to both library catalogs with actual responsibilities and source links. All four owned page H1 headings now have the same explicit `page-top` anchor; other paired heading IDs already match.
