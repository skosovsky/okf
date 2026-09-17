---
title: OKF Toolkit
description: Version-aware CLI, Go, graph, mutation и MCP surfaces для OKF v0.2.
permalink: /ru/toolkit/
---

{% include nav_ru.html %}

# Toolkit

Toolkit по умолчанию читает v0.2, сохраняет explicit v0.1 compatibility и
best-effort потребляет unknown future declarations без потери version.

Version axes независимы: plugin package `0.2.0`, release tag repo/Go module
`v0.2.1` и document spec `okf_version: "0.2"`. Ни одна ось не выбирает другую.
Document contract — [pinned spec](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/spec-v02.md).

## Version resolution

Bundle commands используют `--spec auto|0.1|0.2`, где этот selector доступен:

- `auto`: root declaration, иначе v0.2 default с §13 fallbacks;
- `0.1` или `0.2`: explicit assertion;
- present malformed declaration: hard reserved-index error, а не отсутствие
  или default conformance;
- mismatch declaration/selector: failure, а не silent override;
- future declaration: best-effort с сохранением declared version.

## Validation

```sh
okf validate --path <bundle> --spec auto
okf validate --path <bundle> --spec auto --strict --as-of 2026-07-29
okf validate --path <bundle> --check-links --check-orphans
```

Base errors ограничены parsing/`type` concepts и reserved-file structure.
Strict mode проверяет присутствующие v0.2 families как guidance. Для
deterministic staleness используется explicit `--as-of`.

Missing optional fields, unknown types/keys/runtimes, broken links и missing
indexes не становятся base errors.

## Inspect и parse

```sh
okf info <bundle> --spec auto --as-of 2026-07-29
okf parse <concept.md>
```

Version-aware projections не смешивают:

- declared/effective version и compatibility;
- generated time и legacy-derived marker;
- normalized verification events и derived trust;
- raw/effective lifecycle status;
- staleness на выбранную дату;
- sources/attributions;
- inert Attested Computation summary.

Bare и one-item-list verification дают одинаковую typed semantics без
перезаписи исходной YAML shape. Trust выводится из `verified`, а не хранится как
score.

## Format и index

```sh
okf fmt <concept.md>
okf fmt <concept.md> -w
okf index <bundle> --spec auto
```

Эти commands не мигрируют provenance, не stamp'ят actors/times, не добавляют
verification и не bump'ят `okf_version`. Unknown content сохраняется losslessly
в поддерживаемом parser-backed subset.

## Graph

```sh
okf graph <bundle>
okf graph <bundle> --format dot
okf graph <bundle> --format mermaid
okf graph <bundle> --format json-ld
okf graph <bundle> --format ntriples
okf graph <bundle> --profile skosovsky/okf-v0.2 --extension-relations include
okf graph <bundle> --profile legacy-v0.1
```

Current v0.2 projection и legacy graph profile — explicit compatibility
choices. Graph profile version не связан с declared/effective OKF version.

YAML `relations` — `skosovsky/okf` extension/tooling policy, а не upstream v0.2
family. Markdown links остаются стандартными OKF edges.

Grammar relation reference в этом extension:
`<escaped-concept-id>[#<fragment>]`. Только `#`, принадлежащий ConceptID,
экранируется logical spelling `\#`; первый unescaped `#` является fragment
delimiter. Поэтому `source#part` означает concept `source` с fragment `part`, а
`source\#part` — root concept с ID `source#part`. Backslashes после delimiter
остаются fragment bytes и не интерпретируются как escapes. Stray и
non-canonical concept escapes, например `source\part`, `source\` и
`source\\#part`, отклоняются. Existing refs без escaped concept hash сохраняют
byte-identical strings.

В YAML logical spelling можно записать как `'source\#part'`. JSON transport
экранирует backslash и передаёт его как `"source\\#part"`. Это additive
canonical string encoding: shapes graph/MCP schemas не меняются, а store
receipt format v1 по-прежнему представляет relation refs как `[]string`.
Поддержка escaped hash — canonicalization bugfix, а не format migration:
persisted v1 arrays сохраняют lexical canonical wire ordering.

## Migration

```sh
okf migrate <bundle> --to 0.2
okf migrate <bundle> --from auto --to 0.2 --actor human:reviewer
```

Default — read-only preview. Safe conversion требует:

- explicit producer actor для `timestamp` → `generated.at`;
- explicit legacy retention/conflict policies.

Unresolved legacy Citations остаются blockers, пока explicit mapping legacy
citation → stable source ID не передан. CLI принимает bounded JSON file
`--citation-mappings <json-file>`; top-level value — exact closed array
`[{path,entries:[{legacy_number?,legacy_entry?,source_id,title?,resource?}]}]`,
MCP использует то же field value. Bundle-relative Markdown paths охватывают
root `index.md`, logs и nested files. Каждая entry требует nonzero
`legacy_number`, exact nonblank `legacy_entry` или оба selector; если оба
заданы, они AND-match одну actual entry. Implementation canonical-sort по path,
number и exact raw entry. Duplicate nonzero numbers всегда запрещены. Один raw
selector допустим только в distinct full number+entry pairs; entry-only
пересекается с любым reuse того же raw text. Implementation также отклоняет
inconsistent metadata reused source IDs и unknown fields. `legacy_entry` —
valid UTF-8, 1..4096 bytes, `TrimSpace`-nonblank и без NUL. TAB/LF/CR разрешены;
остальные C0 controls и DEL запрещены. Exact bytes bind authorization/digest,
поэтому LF и CRLF различаются и не нормализуются. Existing `sources` вместе с
legacy Citations всегда блокирует migration с
`reconcile_sources_and_citations`, merge не выполняется.
Writable plan должен быть all-or-nothing, сначала валидировать target v0.2 и
публиковать physical write/rename root `index.md` последним. CLI `--actor`
задаёт document
`generated.by`; transaction principal отделён и остаётся internal для adapter.
Dry preview может не передавать `--actor` и получить manual action; перед apply
generated change через `--write` actor обязателен. Version-only type-only
migration не требует actor/time и не создаёт `generated`.
Explicit citation/generated-at/computation path должен указывать на existing
bundle document. Missing path блокируется с `migration_document_missing`,
возвращает empty manual actions и не может быть создан mappings; новый action
code не добавляется.

В MCP source resolution вычисляется ровно один раз до Preview; Preview и Apply
используют один и тот же полный frozen `expected_source`. Он содержит
`requested_selector`, declaration state (`declaration_present`,
`declaration_valid`, `declaration_raw`, `declared_version`),
resolved/provenance и transition fields, а также ordered legacy candidates и
blockers. Apply отклоняет изменённое resolution evidence. Preview
`v0.1-to-v0.2` возвращает content-free proof `format_version: 2`, обязательный
non-empty `resolution_digest` и non-empty plan digest. Apply требует этот proof,
`expected_plan_digest` и тот же frozen `expected_source`; более ранний proof
format не принимается. Live-ветка `target-noop` требует frozen `expected_source`,
остаётся proofless, не передаёт plan digest и всё равно валидирует переданное
migration target state. Blocked preview применить нельзя. Отдельного migration
`expected_revision` нет; если proof присутствует, `proof.base_revision`
authoritative. Proof фиксирует request и source resolution, revisions, paths,
refs и changed-file/ref summaries с non-null arrays, но не file
bytes/frontmatter/body.
Каждая MCP non-publication ветка preview, noop, rejected, blocked, invalid или
cancelled оставляет всё filesystem tree идентичным path-for-path и byte-for-byte
и не создаёт `.okf` или staging artifacts. Filesystem changes может публиковать
только authorized actual commit; identical successful replay возвращает
записанный result без второй publication.
Migration input validation выполняется до source resolution. Любое переданное
structurally/domain-invalid individual actor, timestamp, citation, generated-at,
computation или asset field отклоняется даже для `target-noop` или rootless
bundle с той же zero-write гарантией.
§13 fallback является presence-only и не зависит от version source: default,
declared или explicit v0.1/v0.2 и future resolution используют один predicate.
`GeneratedPresent`/`SourcesPresent` подавляют fallback даже при malformed value;
`TimestampAllowed`/`CitationsAllowed` фиксируют отсутствие replacement, а
`TimestampActive`/`CitationsActive` дополнительно требуют actual legacy form.
`CitationsActive` требует parser-owned exact heading `# Citations`; одного
numeric marker недостаточно. В частности, prose `[1]` без этой exact active
section не является legacy evidence. Undeclared bundle только с таким prose
разрешается как native v0.2 `target-noop`, а не inferred v0.1 migration.
Один document-aware input preflight выполняется до transition planning, в том
числе до `target-noop`. Успешный live `target-noop` proofless, не открывает
store, ничего не записывает и не создаёт `.okf`.

CLI использует тот же planner, но другой durable adapter contract. Dry-run
строит proof без открытия store. С `--write` live `target-noop` выполняет empty
CAS и создаёт либо replay'ит durable receipt в `.okf`, сохраняя все
revision-visible bundle files path-and-byte identical.

В `v0.1-to-v0.2` number-only mapping выбирает numbered entry внутри active
legacy section. В `target-noop` тот же explicit mapping является replay
assertion: он должен уже совпадать с keyed footnote, а для concept — со
structured source ID и переданной metadata. Bare prose `[1]` он не преобразует.
Для каждого mapping с nonzero `legacy_number` выбранный parser-owned `[n]`
должен исчезнуть, а reference на normalized keyed `[^SourceID]` — существовать.
Entry-only mapping не требует claim reference. Leftover selected, missing или
wrong reference блокируется с `migration_replay_mismatch` на exact
parser-owned span без invented manual action. Inline/fenced code и raw HTML
opaque: marker-like bytes внутри них не подтверждают и не нарушают claim
replay. Normalized collision с
existing footnote label блокируется с
`normalized_footnote_label_collision` и
`disambiguate_citation_entry`; active legacy section в v0.2 target также
блокирует replay. Все эти preflight outcomes proofless и zero-write.
Unrenderable individual migration field возвращает `invalid_request`.
Normalized per-document SourceID collision вместо этого блокируется с
`normalized_footnote_label_collision` и `disambiguate_citation_entry`, включая
`target-noop`; existing-document collision возвращает тот же exact span. Ни
одна ветка не публикуется, обе zero-write.
Proof-bound apply `v0.1-to-v0.2` может вернуть transition-noop только после
authentication и rebuild exact proof и plan digest; noop возвращается до
открытия store и не создаёт `.okf`. Live MCP `target-noop` остаётся proofless;
поведение CLI описано выше. Все варианты оставляют revision-visible bundle
files path-and-byte identical.
См.
[Migration]({{ '/ru/migration/' | relative_url }}).

## Go read model

Package `bundle` даёт permissive typed views для v0.2 fields, сохраняя `Get`,
YAML-node access и caller-owned structs authoritative для unknown data.
Accessors возвращают defensive copies.

Non-Markdown computation/source assets сохраняются как inert revision-visible
bundle files. Чтение asset не исполняет и не fetch'ит его.

## Mutation и durability

Semantic operations — узкие desired-state operations. Они preview'ят staged
output, валидируют итоговый bundle, сохраняют untouched presentation и commit'ят
через CAS/journal durability.

Existing store ChangeSet/Receipt formats и transaction actors отделены от OKF
document actors. `store.CommitReceipt` — transaction durability evidence,
который создаётся после успешного store commit. `executor.receipt` — runtime
artifact Attested Computation; он не хранится в canonical transaction receipt и
не принимается вместо него.

Legacy wire/profile versions остаются stable, пока не меняется их собственный
contract; OKF v0.2 сам по себе не причина их bump'ать.

## MCP

Compatibility tools сохраняют text fallbacks:

- `list_concepts`
- `read_concept`
- `validate_bundle`
- `get_semantic_graph`
- `write_concept`

Server предоставляет девять tools. Четыре safe v0.2 tools работают парами
preview/apply:

- `preview_concept_patch` / `apply_concept_patch`
- `preview_v02_migration` / `apply_v02_migration`

Structured outputs schema-validated. Patch apply требует `expected_revision` и
preview plan digest. Migration apply discriminated по transition:
`v0.1-to-v0.2` требует preview proof `format_version: 2` с non-empty
`resolution_digest`, non-empty `expected_plan_digest` и тот же полный
`expected_source`; более ранний proof format не принимается. Live `target-noop` требует только
`expected_source`, остаётся proofless и валидирует target document. Если proof
присутствует, `proof.base_revision` authoritative. Raw frontmatter/body
остаются доступны, чтобы unknown YAML не проходил через lossy closed model.
Identical migration apply replay-safe.
Во всём MCP structured JSON present `usage_count` — canonical decimal string по
`^(0|[1-9][0-9]*)$` или `null` для nullable outputs. Patch input отклоняет
semantic uint64 overflow. Это исключает `float64` precision loss в decoded MCP
Arguments, включая `MaxUint64`; legacy text fallbacks не меняются.
Selector `set_usage_window` — closed union: shared (нет `source_id` и `source`),
identified (`source_id` non-empty) или exact anonymous (`source` присутствует,
а его `id` отсутствует). Empty `source_id`, mixed/unknown selector forms и
ambiguous exact anonymous matches отклоняются.
Selector `remove_source` — closed union из identified (`source_id` non-empty)
или exact anonymous (`source` присутствует, а его `id` отсутствует); shared
form отсутствует. Empty `source_id`, mixed/unknown selector forms и ambiguous
exact anonymous matches отклоняются.

## Security boundary

- Bundle paths используют containment/no-follow rules.
- Resource fields не авторизуют filesystem, network, shell или secret use.
- Actor metadata не является authentication.
- MCP actor-bearing fields имеют independent 256-byte transport/resource cap.
  257+ bytes возвращает `resource_limit`, а не invalid actor; внутри cap
  semantics определяет shared `ValidActor`. Bundle/store actor grammar этим не
  ограничивается.
- Executor/attester/computation content inert без отдельной trusted runtime и
  authorization.
- По pinned spec agent MAY передавать только значения declared parameters и
  MUST NOT создавать или редактировать sanctioned computation.
- Toolkit не определяет и не выдумывает parameter binding, receipt/verdict
  protocol, attester ABI, sandbox или cache.
