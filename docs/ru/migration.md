---
title: Migration OKF v0.1 в v0.2
description: Lossless explicit transaction-bound migration из OKF v0.1.
permalink: /ru/migration/
---

{% include nav_ru.html %}

# Migration v0.1 → v0.2

v0.2 меняет две legacy формы:

- `timestamp` superseded by `generated.at`;
- body `# Citations` superseded by frontmatter `sources` и keyed footnotes.

Legacy read не является migration. Parse, format, index, graph и обычные read
operations сохраняют legacy bytes.
Absent declaration разрешается по documented source rules. Present malformed
root `okf_version` — hard reserved-index error, а не absent/default или
conformant migration source. Unsupported canonical future declaration
сохраняется и блокируется для v0.1 migration.

## Safe workflow

1. Preview без writes.
2. Передать actor, time или citation mappings только для преобразований,
   которым они действительно нужны.
3. Разрешить conflicts/manual actions.
4. Сохранить unknown YAML, Markdown и assets.
5. Провалидировать весь staged bundle как v0.2.
6. Stage и publish physical write/rename root `index.md` последним; его
   `okf_version` change — финальная visible publication transaction.
7. Apply atomic относительно preview-frozen `expected_source`; для transition
   `v0.1-to-v0.2` также повторить proof v2 и non-empty
   `expected_plan_digest`.

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
открытия store и не создаёт `.okf`. Для live `target-noop` MCP остаётся
proofless и не открывает store, CLI dry-run строит proof без открытия store, а
CLI `--write` выполняет empty CAS с durable receipt в `.okf`. Каждая поверхность
оставляет revision-visible bundle files path-and-byte identical.

Identical successful apply replay-safe: adapter возвращает записанный result,
а не публикует migration повторно.

Preview разрешает `from: auto|0.1` в frozen source identity. MCP preview ветки
`v0.1-to-v0.2` с non-empty plan digest также возвращает content-free proof с
`format_version: 2` и обязательным non-empty `resolution_digest`; apply этой
ветки требует proof, non-empty `expected_plan_digest` и тот же frozen
`expected_source`. Более ранний proof format не принимается. Live-ветка `target-noop` требует
frozen `expected_source`, но не передаёт ни proof, ни plan digest и всё равно
валидирует переданное migration target state. Blocked preview применить нельзя.
Отдельного migration `expected_revision` нет: если proof присутствует,
`proof.base_revision` authoritative. Proof фиксирует request и полный source
resolution, revision digests, read paths, write path+digest pairs, deletes,
renames, affected/reverse-impact refs и changed file/ref summaries. Его arrays
non-null; file bytes, frontmatter и body в proof отсутствуют. Noop/blocked
preview proof не возвращает.

## Timestamp rule

Legacy timestamp не содержит actor. Он переносится в `generated.at` только если
caller передал `generated.by`. Transaction actor, git author, file owner или
current user не становятся implicit producer.

В `okf migrate` flag `--actor` задаёт этого explicit document producer и
записывается в `generated.by`. Это не `store.ChangeSet.Actor`; CLI adapter
использует отдельный internal transaction principal. Dry preview может
пропустить `--actor`; если plan требует generation metadata, preview вернёт
manual action. Для apply такого change с `--write` actor обязателен.

Если type-only legacy bundle не содержит timestamp для conversion, migration
меняет только root version declaration, не требует actor/time и не создаёт
`generated`.

Для MCP actor-bearing fields отдельный 256-byte transport/resource cap
выполняется до semantic validation: 257+ bytes возвращает `resource_limit`, а
не invalid actor. Внутри cap semantics определяет shared `ValidActor`. Adapter
cap не является actor grammar и не ограничивает bundle/store domain.

Unknown actor — manual action, а не guessed value. Конфликт
`timestamp`/`generated.at` требует explicit policy.

## Citation rule

Каждой legacy entry нужен explicit mapping в stable source ID. CLI принимает
его только через bounded JSON file `--citation-mappings <json-file>`; MCP
использует тот же exact closed array
`[{path,entries:[{legacy_number?,legacy_entry?,source_id,title?,resource?}]}]`.
Каждая group keyed по bundle-relative Markdown `path`, а не concept ID, поэтому
адресуются root `index.md`, log files и nested Markdown без угадывания identity.
Каждая mapping entry требует один или оба selector:

- nonzero `legacy_number` выбирает numbered `[1]`;
- exact nonblank `legacy_entry` выбирает raw entry text, включая unnumbered
  bullet или raw URL;
- если заданы оба, они AND-match одну actual entry.

`legacy_entry` сравнивается exactly, без trim, case folding, URL canonicalization
или newline normalization. Значение должно быть valid UTF-8, 1..4096 bytes,
nonblank по `strings.TrimSpace` и без NUL. TAB (`0x09`), LF (`0x0a`) и CR
(`0x0d`) разрешены; остальные C0 controls и DEL (`0x7f`) запрещены. Validation
не trim'ит value: exact bytes сохраняются и bind authorization/digest. Поэтому
LF и CRLF authorize/digest как distinct values. Entry-only selector, совпавший
с duplicate identical raw entries, ambiguous и отклоняется. Duplicate nonzero
`legacy_number` всегда запрещён. Один `legacy_entry` допустим, только если
каждый reuse также содержит distinct nonzero number и образует disjoint full
AND-selector pair. Entry-only плюс любой reuse того же raw text пересекаются и
отклоняются; два entry-only тоже.
Distinct full pairs по number допустимы. Implementations canonical-sort по
path, затем `legacy_number`, затем exact `legacy_entry`; отклоняют overlapping
selectors и требуют consistent metadata при reuse source ID. Unknown fields,
unsafe paths, oversized files/arrays/values и incomplete mappings fail closed.
В keyed footnotes переписываются только claim markers с доказуемым ownership.

CLI file не имеет wrapper object:

```json
[
  {
    "path": "index.md",
    "entries": [
      {
        "legacy_number": 1,
        "source_id": "policy",
        "title": "Synthetic policy",
        "resource": "https://example.invalid/policy"
      }
    ]
  },
  {
    "path": "logs/2026-07.md",
    "entries": [
      {
        "legacy_entry": "https://example.invalid/runbook",
        "source_id": "runbook"
      }
    ]
  },
  {
    "path": "nested/report.md",
    "entries": [
      {
        "legacy_number": 2,
        "legacy_entry": "[Incident](https://example.invalid/incident)",
        "source_id": "incident-report"
      },
      {
        "legacy_number": 3,
        "legacy_entry": "[Incident](https://example.invalid/incident)",
        "source_id": "incident-report-copy"
      }
    ]
  }
]
```

Migration не выдумывает source titles, authors, usage, last-modified dates или
claim mappings. Ambiguous/duplicate/unresolved Markdown блокирует migration.

Когда `sources` и legacy Citations сосуществуют, effective read всё ещё
использует `sources`, потому что fallback действует только при отсутствии
`sources`. Migration всегда блокирует такой документ: она не merge'ит, не
deduplicate'ит и не удаляет ни одну форму; manual action —
`reconcile_sources_and_citations`.

Explicit citation/generated-at/computation path должен указывать на existing
bundle document. Missing path блокируется с `migration_document_missing`,
возвращает empty manual actions и не может быть создан mappings; новый action
code не добавляется.

Stable migration manual-action taxonomy точна:

- `provide_citation_mapping`: mapping отсутствует/unresolved или
  `legacy_number` + `legacy_entry` противоречат actual entry;
- `disambiguate_citation_entry`: duplicate number/raw selector matches;
- `disambiguate_citation_destination`: parser ownership не может выбрать один
  link destination;
- `normalize_citations_section`: opaque или unowned extra section content;
- `reconcile_sources_and_citations`: structured `sources` сосуществуют с legacy
  Citations;
- `repair_invalid_utf8`: invalid UTF-8;
- `provide_generated_at`: generation time required, но отсутствует;
- `provide_generated_by`: producer actor required, но отсутствует.

Duplicate raw selector ambiguity не является destination ambiguity.

## Что migration не выводит

- `verified`, verification или trust;
- lifecycle status или stale date;
- credibility score/signals;
- receipt/verdict или successful attestation;
- Attested Computation из narrative prose.

См. полную
[migration policy skill](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/migration-v01-v02.md)
и [pinned §13 contract](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/spec-v02.md).
