# Migration OKF v0.1 → v0.2

Эта инструкция описывает безопасную migration policy toolkit. Нормативные
изменения перечислены в §13 [pinned OKF v0.2 spec](spec-v02.md). Toolkit policy
не расширяет upstream conformance.

## Что меняется

Два breaking changes:

- `timestamp` superseded by `generated.at`;
- body `# Citations` superseded by frontmatter `sources` и keyed footnotes.

Остальные v0.2 families optional. Migration не обязана добавлять `verified`,
`status`, `stale_after`, credibility signals или Attested Computation.

## Transaction contract

1. Сделать read-only preview.
2. Зафиксировать frozen source identity; для `v0.1-to-v0.2` также deterministic
   non-empty plan digest и content-free proof v2 с обязательным
   `resolution_digest`.
3. Получить explicit inputs только для semantic преобразований, которые
   действительно будут выполнены.
4. Сохранить unknown YAML, Markdown и assets losslessly.
5. Провалидировать весь staged bundle как target v0.2.
6. Опубликовать physical write/rename root `index.md` последним; изменение
   `okf_version` является финальной visible publication transaction.
7. Apply выполнить одной transaction. Patch apply использует
   `expected_revision` + preview digest. Migration `v0.1-to-v0.2` использует
   proof v2 + non-empty `expected_plan_digest` + тот же полный
   `expected_source`; live `target-noop` использует только `expected_source`,
   остаётся proofless и валидирует target document. Более ранний proof format не принимается.

Partial migration запрещена. Любая ambiguity оставляет bundle без изменений и
возвращает blocker/manual action.

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
используют один и тот же полный frozen `expected_source`. Он включает
`requested_selector`, declaration state (`declaration_present`,
`declaration_valid`, `declaration_raw`, `declared_version`),
resolved/provenance/transition fields и ordered candidates/blockers. Apply
отклоняет изменённый resolution или его evidence до store.
§13 fallback является presence-only и не зависит от version source: default,
declared или explicit v0.1/v0.2 и future resolution используют один predicate.
`GeneratedPresent`/`SourcesPresent` подавляют fallback даже при malformed value;
`TimestampAllowed`/`CitationsAllowed` фиксируют отсутствие replacement, а
`TimestampActive`/`CitationsActive` дополнительно требуют actual legacy form.
`CitationsActive` требует parser-owned exact heading `# Citations`; одного
numeric marker недостаточно. Prose `[1]` без exact active section не является
legacy evidence. Undeclared bundle только с таким marker разрешается как native
v0.2 `target-noop`, а не inferred v0.1 transition.
Общий document-aware input preflight выполняется до transition planning,
включая `target-noop`. В MCP успешный live target-noop proofless, не открывает
store, ничего не записывает и не создаёт `.okf`. CLI dry-run строит proof без
открытия store; CLI `--write` выполняет empty CAS и сохраняет либо replay'ит
durable receipt в `.okf`, не меняя revision-visible bundle files.

В `v0.1-to-v0.2` number-only citation mapping выбирает numbered entry внутри
active legacy section. На `target-noop` это replay assertion уже
мигрированного состояния: matching keyed footnote и, для concept, structured
source ID и переданная metadata должны существовать. Bare prose `[1]` mapping
не переписывает. Missing/different migrated content блокируется с
`migration_replay_mismatch` без invented manual action. Normalized collision с
existing footnote label блокируется с
`normalized_footnote_label_collision` и `disambiguate_citation_entry`; active
legacy section в v0.2 target также является replay mismatch. Все ветки
proofless и zero-write.
Для каждого mapping с nonzero `legacy_number` выбранный parser-owned `[n]`
должен исчезнуть, а reference на normalized keyed `[^SourceID]` — существовать.
Entry-only mapping не требует claim reference. Leftover selected, missing или
wrong reference блокируется с `migration_replay_mismatch` на exact
parser-owned span, без writes, proof или plan authorization. Marker-like bytes
в inline/fenced code и raw HTML opaque: они не являются evidence и не создают
replay mismatch.
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

Migration proof содержит только frozen metadata: `format_version: 2`,
обязательный non-empty `resolution_digest`, request, полный source resolution,
base/result revisions, read paths, write path+digest pairs, deletes, renames,
canonical affected/reverse refs и changed-file/ref summaries. Arrays non-null;
file bytes, frontmatter и body отсутствуют. Более ранний proof format не принимается. Отдельного
migration `expected_revision` нет: при наличии proof `proof.base_revision`
authoritative. Noop/blocked preview proof не возвращает; blocked preview
применить нельзя.

## `timestamp` → `generated`

`timestamp` содержит time, но не producer identity. Поэтому migration требует
explicit `generated.by`.

В CLI `okf migrate --actor` является explicit document producer для
`generated.by`. Это не `store.ChangeSet.Actor`: transaction principal
CLI-adapter'а отдельный и внутренний. Dry preview может не иметь `--actor` и
вернуть manual action; `--write` требует actor перед созданием `generated`.
Для MCP actor-bearing fields отдельный 256-byte transport/resource cap
проверяется до shared `ValidActor`: 257+ bytes возвращает `resource_limit`, а не
invalid actor. Cap не является actor grammar и не ограничивает bundle/store
domain.

Safe case:

```yaml
# before
timestamp: 2026-05-28T22:53:05Z

# after, actor supplied by caller
generated:
  by: "process:catalog-export"
  at: 2026-05-28T22:53:05Z
```

Rules:

- не использовать transaction actor как implicit producer;
- не использовать git author, file owner или current user как producer;
- если actor неизвестен, вернуть unresolved/manual action;
- если `generated` отсутствует и `timestamp` есть, copy time только с actor;
- если `generated` и `timestamp` отсутствуют, не создавать `generated`;
- version-only migration type-only concept меняет только root declaration и не
  требует actor/time;
- никогда не использовать `now`;
- equivalent `generated` — noop;
- conflicting `timestamp`/`generated.at` — blocker без explicit conflict policy;
- legacy key удалять только по выбранной policy, после успешного copy;
- comments, duplicate keys, aliases/merges или ambiguous ownership — fail closed.

## `# Citations` → `sources`

Migration не делает semantic matching. Caller задаёт explicit mapping из каждой
legacy entry в stable source ID и structured resource.

CLI принимает только bounded JSON file
`--citation-mappings <json-file>` с exact top-level closed array
`[{path,entries:[{legacy_number?,legacy_entry?,source_id,title?,resource?}]}]`;
MCP field содержит тот же array. `path` — bundle-relative Markdown path,
включая root `index.md`, logs и nested files. Каждая entry требует один или оба
selector: nonzero `legacy_number` для `[1]`, exact nonblank `legacy_entry` для
unnumbered bullet/raw URL. Если заданы оба, они AND-match одну actual entry.
`legacy_entry` должен быть valid UTF-8, 1..4096 bytes, `TrimSpace`-nonblank, без
NUL. TAB/LF/CR разрешены; остальные C0 controls и DEL запрещены. Exact bytes не
trim/case/URL/newline-normalize и bind authorization/digest, поэтому LF и CRLF
различаются. Entry-only selector, совпавший с duplicate identical raw entries,
ambiguous и отклоняется. Duplicate nonzero `legacy_number` всегда запрещён.
Один `legacy_entry` допустим только в distinct full number+entry pairs;
entry-only пересекается с любым reuse того же raw text, два entry-only тоже
запрещены. Distinct full pairs по number допустимы.
Canonical-sort по path, затем `legacy_number`, затем exact `legacy_entry`;
overlapping selectors, unknown fields и inconsistent metadata reused source ID
запрещены.

```markdown
# before
The claim is documented.[1]

# Citations

[1] [Synthetic policy](https://example.invalid/policy)
```

```markdown
# after
---
type: Reference
sources:
  - id: policy
    resource: https://example.invalid/policy
    title: Synthetic policy
---

The claim is documented.[^policy]

[^policy]: Synthetic policy.
```

Rules:

- создать `sources` только из actual legacy entries и explicit mapping;
- не выдумывать title, author, usage, `last_modified` или `usage_window`;
- заменить только доказанные numeric claim markers;
- keyed footnote label должен совпадать с `sources[].id`;
- удалить `# Citations` только после полного успешного mapping;
- multiple sections, duplicate numbers/IDs, unresolved markers, conflicting
  definitions и ambiguous Markdown ownership — blocker;
- inline/fenced code и raw HTML остаются opaque.

Если `sources` уже присутствует одновременно с legacy Citations, effective
read использует `sources`: fallback включается только при отсутствии
replacement. Migration сохраняет обе raw формы и всегда блокируется; merge,
dedup и delete не выполняются. Manual action —
`reconcile_sources_and_citations`; caller должен нормализовать документ заранее.

Manual-action vocabulary:

- missing/unresolved mapping или number+entry mismatch →
  `provide_citation_mapping`;
- duplicate number/raw selector ambiguity → `disambiguate_citation_entry`;
- parser link-destination ownership ambiguity →
  `disambiguate_citation_destination`;
- opaque/unowned extra section → `normalize_citations_section`;
- mixed structured `sources` + legacy Citations →
  `reconcile_sources_and_citations`;
- invalid UTF-8 → `repair_invalid_utf8`.
- missing generation time → `provide_generated_at`;
- missing producer actor → `provide_generated_by`.

Explicit citation/generated-at/computation path должен указывать на existing
bundle document. Missing path блокируется с `migration_document_missing`,
возвращает empty manual actions и не может быть создан mappings; новый action
code не добавляется.

Duplicate raw selector ambiguity не является destination ambiguity.

## `skosovsky/okf` RelationRef wire extension

Эта grammar — toolkit extension, не upstream OKF v0.2 migration rule. Relation
reference имеет вид `<escaped-concept-id>[#<fragment>]`. Только `#` внутри
ConceptID кодируется logical spelling `\#`; первый unescaped `#` отделяет
fragment. Поэтому `source#part` и `source\#part` — разные identities:
concept+fragment и root concept с ID `source#part`. Backslashes после delimiter
остаются literal fragment bytes. Stray/non-canonical concept escapes
отклоняются, ordinary refs сохраняются byte-identical.

YAML single-quoted spelling: `'source\#part'`. В JSON тот же logical ref:
`"source\\#part"`. Graph/MCP schema shapes остаются string-based, а store
receipt format v1 продолжает хранить refs как `[]string`.

## Attested Computation boundary

Migration не превращает narrative prose, SQL snippets или shell examples в
Attested Computation автоматически. Это отдельный authoring decision:

- caller указывает target computation concepts;
- caller задаёт sanctioned computation;
- inline mode владеет ровно одним fence под `# Computation`;
- file mode создаёт asset transactionally;
- unknown executor/attester/runtime ABI не изобретается.

## Запрещённые выводы

Не выводить из migration, timestamp, git history или validation:

- `verified`;
- trust tier;
- `status`;
- `stale_after`;
- source credibility;
- receipt/verdict;
- успешную attestation.

## Legacy consumption после migration tooling

До apply и для intentional legacy bundles v0.2 consumer:

- использует legacy `timestamp` только когда `generated` полностью отсутствует;
- читает legacy `# Citations` только когда `sources` отсутствует;
- не переписывает input через parse/fmt/index/read;
- показывает fallback как legacy-derived;
- сохраняет declared `0.1` и unknown future version.

## Review checklist

- [ ] Preview не изменил bytes.
- [ ] Producer actor предоставлен явно, если plan создаёт `generated`.
- [ ] Каждая citation имеет explicit source mapping.
- [ ] Unknown content сохранён.
- [ ] Нет invented verification/lifecycle/credibility.
- [ ] Target v0.2 validation прошла.
- [ ] Root version меняется последней.
- [ ] Migration apply повторяет frozen `expected_source`; `v0.1-to-v0.2`
      дополнительно передаёт proof v2 с `resolution_digest` и non-empty
      `expected_plan_digest`, а live `target-noop` не передаёт ни proof, ни
      digest.
- [ ] Identical apply replay-safe: возвращается записанный result без второй
      publication.
