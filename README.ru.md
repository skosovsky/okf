# okf

`okf` — Go toolkit, CLI, MCP server и agent skill для
[Open Knowledge Format (OKF) v0.2](skills/open-knowledge-format/references/spec-v02.md):
переносимых knowledge bundles из Markdown concepts с YAML frontmatter.

Репозиторий поддерживает три version mode:

- `0.2` — текущий default contract для чтения и записи;
- `0.1` — intentional legacy для чтения, validation и migration source;
- неизвестная future declaration — best-effort lossless consumption.

Version axes независимы:

- plugin package: `0.2.0`;
- release tag repo/Go module: `v0.2.1`;
- OKF document spec: `okf_version: "0.2"`.

Совпадающие числа не означают, что одна ось выбирает другую.

English documentation: [README.md](README.md).

## Поверхности

1. `okf`: version-aware validation, inspection, formatting, indexing, graph
   projection и explicit migration.
2. Go packages: `bundle`, `validator`, `graph`, `store`, `store/fs` и
   `mutation`.
3. `okf-mcp`: schema-first stdio tools с legacy text fallbacks.
4. `open-knowledge-format`: agent skill, pinned на upstream v0.2 spec.

## Установка

```sh
go install github.com/skosovsky/okf/cmd/okf@latest
go install github.com/skosovsky/okf/cmd/okf-mcp@latest
```

Проверяй binary version отдельно от поддерживаемых OKF specs:

```sh
okf version
okf version --json
```

## Минимальный OKF v0.2 bundle

`index.md`:

```markdown
---
okf_version: "0.2"
---

# Concepts

* [Minimal](minimal.md) - Minimal conformant v0.2 concept.
```

`minimal.md`:

```markdown
---
type: Unknown Producer Type
---

Minimal conformant v0.2 concept.
```

`type` — единственное всегда обязательное поле concept. Optional provenance,
trust, lifecycle и computation families не являются base conformance
requirements.

## Правила authoring

- Записывай `okf_version` только в root `index.md`.
- Добавляй `generated` только при известном producer actor.
- Создавай `sources` только из actual materials.
- Привязывай claims footnotes с labels из `sources[].id`.
- Записывай `verified` только после реальной проверки.
- Не выводи `status`, `stale_after`, credibility или verification из git,
  timestamps, authorship, migration или successful validation.
- Выноси sanctioned computation в отдельный concept с
  `type: Attested Computation`.
- Сохраняй unknown keys/types/content для forward compatibility.

Canonical examples привязаны к fixtures через
[`fixtures/v02/corpus.yaml`](fixtures/v02/corpus.yaml): minimal, Appendix A,
bare/list verification, lifecycle, inline/file computation, legacy, mixed,
future и adversarial cases. Синтетические примеры repo используют reserved
domains вроде `example.invalid`.

## Правила consumption

До интерпретации fields определи declared и effective version:

1. Supported declaration в root `index.md` выбирает contract.
2. При отсутствии declaration default — v0.2 с §13 legacy fallbacks.
3. Present malformed `okf_version` не является отсутствием: это hard
   reserved-index error, и v0.2 traversal default не делает bundle conformant.
4. Unknown future declaration сохраняется и читается best-effort.
5. Explicit selector — assertion; конфликт с declaration завершается ошибкой.

Для v0.2 consumption:

- предпочитай `generated` и `sources`;
- используй `timestamp` только если `generated` полностью отсутствует;
- используй `# Citations` только если `sources` отсутствует;
- нормализуй bare `verified` в one-item typed list;
- выводи trust только из `verified`;
- показывай trust, status и staleness отдельно.

Когда legacy и v0.2 provenance присутствуют вместе, effective read использует
v0.2: §13 fallback действует только при отсутствии replacement. Raw legacy data
сохраняется. Migration всегда блокирует документ, содержащий одновременно
`sources` и legacy Citations, с manual action
`reconcile_sources_and_citations` и не выдумывает merge/dedup policy.

## CLI

Основные команды:

```sh
okf validate --path <bundle> --spec auto
okf validate --path <bundle> --spec auto --strict --as-of 2026-07-29
okf info <bundle> --spec auto --as-of 2026-07-29
okf parse <concept.md>
okf fmt <concept.md>
okf fmt <concept.md> -w
okf index <bundle>
okf graph <bundle>
okf migrate <bundle> --to 0.2
okf migrate <bundle> --to 0.2 --citation-mappings <json-file>
```

Migration по умолчанию dry-run. Apply требует explicit inputs для безопасного
timestamp conversion. Unresolved legacy Citations остаются blockers, пока
caller не передал bounded JSON file `--citation-mappings <json-file>`. Его
точный top-level value — closed array
`[{path,entries:[{legacy_number?,legacy_entry?,source_id,title?,resource?}]}]`,
keyed по bundle-relative Markdown `path`, включая root `index.md`, logs и nested
files. Каждая entry требует хотя бы один selector: nonzero `legacy_number` для
`[1]` или exact nonblank `legacy_entry` для unnumbered bullet/raw URL. Если
заданы оба, одна actual entry должна совпасть по обоим. CLI и MCP используют
тот же DTO с canonical sort по path, number и exact raw entry.
Duplicate nonzero numbers всегда запрещены. Один `legacy_entry` допустим только
в distinct full number+entry pairs; entry-only selector пересекается с любым
reuse того же raw text и отклоняется. Reuse source ID требует consistent
metadata. `legacy_entry` — valid UTF-8, 1..4096 bytes,
`TrimSpace`-nonblank и без NUL. TAB/LF/CR разрешены; остальные C0 controls и DEL
запрещены. Exact bytes входят в authorization/digest, поэтому LF и CRLF
различаются и не нормализуются. Root `index.md` migration публикует physical
write/rename последним. Explicit citation/generated-at/computation path должен
указывать на existing bundle document. Missing path блокируется с
`migration_document_missing`, возвращает empty manual actions и не может быть
создан mappings; новый action code не добавляется.
Каждая non-publication ветка preview, noop, rejected, blocked, invalid или
cancelled оставляет всё filesystem tree идентичным path-for-path и byte-for-byte
и не создаёт `.okf` или staging artifacts. Filesystem changes может публиковать
только authorized actual commit; identical successful replay возвращает
записанный result без второй publication.
Migration input validation выполняется до source resolution. Любое переданное
structurally/domain-invalid individual actor, timestamp, citation, generated-at,
computation или asset field отклоняется даже для `target-noop` или rootless
bundle с той же zero-write гарантией.
Source resolution вычисляется ровно один раз до Preview; Preview и Apply
используют один и тот же полный frozen `expected_source`. Он содержит
`requested_selector`, declaration state (`declaration_present`,
`declaration_valid`, `declaration_raw`, `declared_version`),
resolved/provenance и transition fields, а также ordered legacy candidates и
blockers. Apply отклоняет изменённый resolution или его evidence.
§13 fallback является presence-only и не зависит от version source: default,
declared или explicit v0.1/v0.2 и future resolution используют один predicate.
`GeneratedPresent`/`SourcesPresent` подавляют fallback даже при malformed value;
`TimestampAllowed`/`CitationsAllowed` фиксируют отсутствие replacement, а
`TimestampActive`/`CitationsActive` дополнительно требуют actual legacy form.
`CitationsActive` требует parser-owned exact heading `# Citations`; одного
numeric marker недостаточно.
Для каждого citation mapping с nonzero `legacy_number` replay требует, чтобы
выбранный parser-owned marker `[n]` исчез, а reference на normalized keyed
`[^SourceID]` существовал. Entry-only mapping не требует claim reference.
Leftover selected marker, missing keyed reference или wrong keyed reference
блокируется с `migration_replay_mismatch` на exact parser-owned span.
Inline/fenced code и raw HTML opaque: marker-like bytes внутри них не являются
ни claim evidence, ни replay failure. Любой mismatch zero-write и не возвращает
proof или plan authorization.
Unrenderable individual migration field возвращает `invalid_request`.
Normalized per-document SourceID collision вместо этого блокируется с
`normalized_footnote_label_collision` и `disambiguate_citation_entry`, включая
`target-noop`; existing-document collision возвращает тот же exact span. Ни
одна ветка не публикуется, обе zero-write.
Proof-bound apply `v0.1-to-v0.2` может вернуть transition-noop только после
authentication и rebuild exact proof и plan digest; noop возвращается до
открытия store и не создаёт `.okf`. Поведение live `target-noop` зависит от
поверхности: MCP остаётся proofless и не открывает store; CLI dry-run строит
proof без открытия store; CLI `--write` выполняет empty CAS и сохраняет либо
replay'ит durable receipt в `.okf`. Во всех трёх случаях revision-visible
bundle files остаются path-and-byte identical.
CLI
`--actor` — document producer, который записывается в `generated.by`, а не
store transaction principal. Для dry preview его можно не передавать: если
generation metadata нужна, preview вернёт manual action. Перед таким apply с
`--write` actor обязателен. Version-only migration type-only legacy concepts не
требует actor или time и не должна создавать `generated`.

Validation остаётся слоистой:

| Слой | Значение |
| --- | --- |
| Base | UTF-8/parseable concept frontmatter, непустой string `type`, структура reserved files. |
| Strict | Advisory validation присутствующих v0.2 families, attribution, lifecycle и computation shapes. |
| Links | Advisory diagnostics внутренних links. |
| Orphans | Advisory diagnostics покрытия indexes. |

Unknown types/keys/runtimes, отсутствующие optional fields, broken links и
missing indexes не становятся base errors.

## Go library

```go
b, err := bundle.LoadBundle("./knowledge")
if err != nil {
	return err
}

report := validator.ValidateBundle(b, &validator.ValidatorConfig{
	Strict:       true,
	CheckLinks:   true,
	CheckOrphans: true,
})
if !report.IsConformant() {
	return errors.New("bundle is not conformant")
}
```

Read model остаётся permissive и Bring Your Own Types friendly. Typed v0.2
accessors — projections поверх lossless YAML; unknown fields доступны
caller-owned models и сохраняются при round trip.

Mutation использует desired-state, preview-first и transactional contract.
Semantic migration отделена от parse, format, index и graph.

## MCP server

```json
{
  "mcpServers": {
    "okf": {
      "command": "okf-mcp",
      "args": ["-root", "/absolute/path/to/bundle"]
    }
  }
}
```

Compatibility tools:

- `list_concepts`
- `read_concept`
- `validate_bundle`
- `get_semantic_graph`
- `write_concept`

Server предоставляет четыре safe v0.2 tools; всего tools девять:

- `preview_concept_patch` / `apply_concept_patch`
- `preview_v02_migration` / `apply_v02_migration`

Patch apply использует `expected_revision` и preview plan digest. Migration
apply discriminated по transition: `v0.1-to-v0.2` требует preview proof
`format_version: 2` с обязательным non-empty `resolution_digest`, non-empty
`expected_plan_digest` и тем же frozen `expected_source`; более ранний proof
format не принимается. Live `target-noop` требует только этот frozen `expected_source`,
остаётся proofless и всё равно валидирует переданное migration target state.
Отдельного migration `expected_revision` нет; если proof присутствует,
`proof.base_revision` authoritative. Actor metadata не является
authentication. Identical migration apply replay-safe.
MCP actor-bearing fields имеют отдельный 256-byte transport/resource cap:
257+ bytes возвращает `resource_limit`, а не invalid-actor verdict. Внутри cap
semantic validity определяет shared `ValidActor`. Adapter cap не является actor
grammar и не задаёт bundle/store domain length ceiling.

Во всём MCP structured JSON present `usage_count` — canonical decimal string по
`^(0|[1-9][0-9]*)$` или `null` для nullable output field. Patch input отклоняет
semantic uint64 overflow. Decimal string исключает `float64` precision loss в
decoded MCP Arguments, включая `MaxUint64`. Legacy text fallbacks не меняются.
Selector `set_usage_window` — closed union: shared (нет `source_id` и `source`),
identified (`source_id` non-empty) или exact anonymous (`source` присутствует,
а его `id` отсутствует). Empty `source_id`, mixed/unknown selector forms и
ambiguous exact anonymous matches отклоняются.
Selector `remove_source` — closed union из identified (`source_id` non-empty)
или exact anonymous (`source` присутствует, а его `id` отсутствует); shared
form отсутствует. Empty `source_id`, mixed/unknown selector forms и ambiguous
exact anonymous matches отклоняются.

## Safety boundary Attested Computation

OKF записывает computation/attestation contracts; сам факт наличия field или
resource не запускает content.

`computation`, `executor.resource` и `attester.resource` — inert data.
По pinned spec agent MAY передавать только значения declared parameters и MUST
NOT создавать или редактировать sanctioned computation.
Execution требует отдельной trusted runtime и authorization. OKF v0.2 не
определяет runtime discovery, parameter binding implementation,
receipt/verdict wire format, attester ABI, portability, sandboxing или cache.
`store.CommitReceipt` не связан с `executor.receipt`.

## Extension `skosovsky/okf`: relations

YAML `relations` — extension/tooling policy этого repo, а не часть upstream OKF
v0.2. Она добавляет typed semantic edges и optional relation validation.
Markdown links остаются стандартным OKF navigation/relationship mechanism.
Legacy graph profiles и wire formats остаются compatibility surfaces; их
версия не выбирает OKF document contract.

## Migration

Смотри:

- [Migration guide](docs/ru/migration.md)
- [Pinned v0.2 spec](skills/open-knowledge-format/references/spec-v02.md)
- [Agent workflow](skills/open-knowledge-format/SKILL.md)

Migration не выдумывает producer actors, source mappings, verification,
lifecycle, freshness, credibility signals, receipt или attestation.

## Разработка

```sh
go test ./...
go vet ./...
git diff --check
```

Финальный lock-file test проверяет, что `skills-lock.json` совпадает с точным
содержимым `skills/open-knowledge-format`. Lock пересчитывается только после
стабилизации skill и references.
