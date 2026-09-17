# Техническое задание: OKF v0.2 в `okf-mcp`

## 1. Цель

Сделать MCP schema-first v0.2 adapter с rich structured outputs и безопасными
preview/apply edits, сохранив legacy 5-tool text contract.

Зависимости: [`task09.md`](task09.md)–[`task18.md`](task18.md).

## 2. Checked-in schemas

Добавить `internal/mcpserver/contracts/*.schema.json` для:

- common/version/error envelopes;
- list/read/validate/graph;
- legacy write result;
- concept patch preview/apply;
- migration preview/apply.

JSON Schema — source of truth:

- raw input/output schema регистрируется tool'ом;
- server output validation включена;
- objects closed через `additionalProperties: false`, кроме explicit extension;
- dates/timestamps/enums/limits зафиксированы;
- arrays не сериализуются как `null`;
- success возвращает structuredContent и legacy text fallback.

Stable error envelope содержит `code`, `message`, `retryable`, diagnostics.

## 3. Existing tools compatibility

### `list_concepts`

Additive input `as_of`. Structured result включает:

- declared/effective version и compatibility;
- status/trust/staleness/generated time/source count;
- Attested Computation/runtime marker;
- explicit legacy-derived marker.

### `read_concept`

Сохранить raw Markdown text. Structured result содержит:

- lossless `frontmatter_yaml`;
- body;
- typed known v0.2 projection;
- legacy fallback flags.

Не переводить unknown YAML в lossy closed JSON model.

### `validate_bundle`

Inputs:

- `target_version: auto|0.1|0.2`;
- explicit `as_of`.

Output:

- declared/effective/resolution/compatibility;
- conformance отдельно от guidance;
- counts и stable diagnostic code/field/spec ref.

### `get_semantic_graph`

Text fallback остаётся graph package output; structured content — parsed
document согласно task12 profile. MCP не строит собственную graph model.

### `write_concept`

Сохранить whole-document escape hatch, но сделать validation version-aware.
Canonical request identity включает domain/version policy. Caller-controlled
idempotency key не добавлять.

## 4. Safe edit tools

Добавить:

- `preview_concept_patch`;
- `apply_concept_patch`.

Операции мапятся только в task14 domain operations. Preview возвращает:

- applicable/noop/rejected;
- base/result revision;
- plan digest;
- bounded diff;
- affected paths;
- diagnostics;
- confirmation unknown preservation.

Apply требует `expected_revision` и `expected_plan_digest`, пересобирает plan и
publishes через store CAS/journal. Raw filesystem writes запрещены.

## 5. Migration tools

Добавить:

- `preview_v02_migration`;
- `apply_v02_migration`.

Они являются adapters task15 planner:

- explicit actor при generated creation;
- safe legacy timestamp policy;
- citation extraction только с explicit mapping;
- manual actions/blockers для ambiguity;
- one transactional multi-file apply;
- idempotent replay.

## 6. Security

- resource/computation/executor/attester values — inert data.
- MCP не fetch'ит сеть и не исполняет code.
- Actor metadata не является authentication.
- Сохранить absolute-root/no-follow/path containment/CAS boundaries.
- Добавить bounded input bytes/items/nesting/diff.
- Errors не раскрывают paths outside root.
- Cancellation между parse/validate/preview/commit leaves zero changes.

## 7. Файлы

- `internal/mcpserver/server.go`;
- `tools.go`, `write.go`;
- новые `patch.go`, `migration.go`;
- checked-in schemas;
- protocol/tools/contracts/migration/adversarial tests;
- docs после стабилизации contract.

## 8. Tests

Все tests — AAA.

- Advertised schemas/output validation/additionalProperties.
- Old args/text payload byte-compatible.
- Structured outputs schema-valid.
- All trust tiers, bare/list verified, stable/stale boundary.
- v0.1/v0.2/absent/future/override.
- Deterministic arrays/diagnostics.
- Unknown YAML/comments/body preserved.
- Flow/alias/duplicate/ambiguous edit fail closed.
- Revision conflict/plan mismatch/replay/concurrent writers.
- Migration all-or-nothing/recovery.
- Symlink/path traversal/special/UTF-8/resource limits.
- Malicious executor/attester values remain inert.
- Cancellation leaves bundle unchanged.

## 9. Acceptance criteria

- Existing 5 tools and text contract remain compatible.
- Каждый success имеет validated structuredContent.
- v0.2 signals доступны list/read/graph.
- Validation сообщает version/compatibility/as_of.
- Patch/migration доступны только preview → digest/revision-bound apply.
- No tool executes/fetches computation resources.
- MCP package/full suite проходят.

## 10. Out of scope

Execute/attest tools, receipt/verdict protocol, attester ABI/sandbox/cache.
