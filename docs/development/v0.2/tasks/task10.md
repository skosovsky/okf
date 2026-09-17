# Техническое задание: OKF v0.2 domain model в `bundle`

## 1. Цель

Сделать `bundle` доменным read layer для provenance, trust, lifecycle,
Attested Computation, keyed attribution и referenced assets. Сохранить
permissive conformance, BYOT и lossless unknown-key round-trip.

Зависимость: [`task09.md`](task09.md).

## 2. Текущие gaps

- `bundle.OKFVersion == "0.1"`.
- Typed accessors покрывают legacy `timestamp`, но не v0.2 families.
- Новые standard keys считаются extensions.
- `Document.Citations()` понимает только positional `# Citations`.
- `Bundle.Load` не сохраняет non-Markdown `.sql`/`.py` assets.
- generic semantic subresource index считает `sources[].id` fragment'ом.
- relations extension не исключает standard v0.2 families.

## 3. Public model

Добавить небольшие открытые read models:

- `Generation`;
- `Verification`;
- `UsageWindow`;
- `ProvenanceSource` (`Source` уже занят storage interface);
- `TrustTier`;
- lifecycle constants;
- `ComputationParameter`;
- `ExecutorContract`;
- `AttesterContract`;
- `AttestedComputationContract`.

Actor, runtime, parameter type и receipt field names остаются открытыми строками.
Не вводить закрытые registry/enums.

Добавить accessors:

- `Sources`, shared/effective `UsageWindow`;
- `Generated`, `Verifications`;
- derived `TrustTier`;
- raw/effective `Status`;
- `StaleAfter` и helper с explicit reference date;
- `AttestedComputation`;
- effective content-change time.

Bare `verified` mapping нормализуется в one-element slice без изменения raw YAML.

## 4. BYOT и lossless contract

- `Get`, `Set`, `YAMLNode` остаются authoritative.
- Добавить generic decode/encode helper для caller-owned structs.
- Не вводить монолитную `FrontmatterSchema`.
- Accessors возвращают copies и не разделяют mutable slices/maps с caller.
- Typed read не меняет node style, comments, key order или nested extensions.
- Standard keys v0.2 плюс legacy `timestamp` исключаются из `ExtensionKeys`;
  `relations` остаётся project extension.

## 5. Attribution

Добавить footnote references/definitions и `Document.Attributions()`:

- join key — `sources[].id`;
- source order не имеет значения;
- markers/definitions в fenced и inline code игнорируются;
- unknown/duplicate IDs остаются видимыми, не фильтруются молча;
- prose footnote definition не заменяет structured source;
- legacy `Citation` API сохраняется.

## 6. Bundle assets и path values

- `Load` сохраняет все revision-visible regular files.
- `MarkdownFiles()` сохраняет прежнюю семантику.
- Добавить `Files()`/`AssetFiles()` с defensive copies.
- `ReadFile()` читает captured `.sql`, `.py`, `.json` без повторного I/O.
- Сохранить no-follow/special-file/`.okf` security contract.
- Добавить resolver для URL, bundle-relative и relative path values.
- Scope descriptor не считать локальным path.

## 7. Semantic extension collision

- `sources[].id` не является `concept#fragment`.
- Standard family mappings не становятся implicit relation sources.
- Duplicate source IDs не создают `duplicate_fragment`.
- Настоящие producer extension subresources продолжают индексироваться.

## 8. Conformance и compatibility

`Document.ValidateConformance()` не расширять: hard requirement — только
parseable frontmatter и непустой string `type`.

- Optional family может отсутствовать.
- Malformed optional family не ломает `Bundle.Load`.
- `Timestamp()` сохранить как deprecated legacy accessor.
- `generated.at` authoritative; malformed присутствующий `generated` не должен
  тихо fallback'иться в `timestamp`.
- Никакой миграции при чтении/serialization.

## 9. Файлы

- `bundle/doc.go`;
- `bundle/frontmatter.go`, новый `frontmatter_v02.go`;
- `bundle/document.go`;
- `bundle/links.go` или новый `attribution.go`;
- `bundle/bundle.go`;
- `bundle/semantic_index.go`;
- `bundle/relations.go`;
- package tests и canonical fixtures.

## 10. Tests

Все tests — AAA.

- Полный Appendix A.
- Bare/list `verified`.
- Все trust tiers.
- Missing status → stable.
- Stale boundary `today == stale_after`.
- Generated present without `at` не fallback'ится.
- Shared/per-source usage window.
- Unknown/malformed nested shapes не panic'ят и не исчезают.
- Footnote reorder/repeat/unknown/definition/code cases.
- Parse → typed read → serialize сохраняет unknown bytes/semantics.
- `sources[].id` не появляется в subresource/relation indexes.
- `.sql/.py` captured и revision-visible.
- Existing v0.1 tests не регрессируют.

## 11. Acceptance criteria

- `bundle.OKFVersion == "0.2"`.
- SDK читает все поля §§5 и 10 без ручного YAML traversal.
- BYOT и unknown extension round-trip доказаны.
- Assets доступны из immutable loaded bundle.
- Legacy API остаётся рабочим.
- `go test ./bundle` и `go test ./...` проходят.

## 12. Out of scope

Execution, parameter binding, runtime receipts/verdicts, attester ABI/sandbox,
caching и автоматическая миграция.
