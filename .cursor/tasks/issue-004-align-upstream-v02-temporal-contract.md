# Issue 004 — Согласовать временной контракт двух редакций OKF 0.2

Статус: реализовано; полный test/vet/race-прогон пройден в CI 2026-09-26. Приоритет: P0 для решения контракта, до новых producer workflows.
Язык реализации: Go. JSON Schema, Markdown, YAML/JSON fixtures — контракты и данные; Python/Node runtime не добавлять.

## Проблема и проверенные источники

Версия `0.2` не идентифицирует единственную редакцию спецификации. Наш pinned документ и upstream, закреплённый okf-skills, задают разные типы нескольких полей времени. Это drift самого нормативного документа, а не просто чужая трактовка.

Проверка 2026-09-26: обе raw upstream версии скачаны и сравнены побайтно с локальными копиями. Наша копия совпадает полностью; чужая — после удаления десяти строк provenance header.

| Редакция | Upstream | SHA-256 документа без чужого header |
|---|---|---|
| Наша | [knowledge-catalog/okf/SPEC.md, 3fcbb9f](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/3fcbb9f828c2f23d109c855ee403c3a4c81f3a96/okf/SPEC.md) | `5a3311d270bebb16d558010e75064f5b75323f284992641732b1c8097511f948` |
| okf-skills | [open-knowledge-format/SPEC.md, 0b87c52](https://github.com/GoogleCloudPlatform/open-knowledge-format/blob/0b87c52c6ef999286c745e19998fdfcd03d5dbee/SPEC.md) | `26aa5da029278939f914e578107242d9607d4f2dc5fe153272b82f9ed1030101` |

Наш lock: `fixtures/v02/spec-lock.json:1`. Foreign repository: [okf-skills, 68ce7a0](https://github.com/scaccogatto/okf-skills/tree/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5). Ни `main`, ни содержимое временного каталога не являются достаточным pin для будущих тестов.

## Полный semantic diff двух документов

1. В §5 добавлено общее требование: каждый timestamp-valued key — ISO 8601 datetime с явным UTC offset.
2. `sources[].last_modified`: из `YYYY-MM-DD` в datetime.
3. Общий и per-source `usage_window.{from,to}`: date range заменён datetime range.
4. `stale_after`: абсолютная дата и `today >= stale_after` заменены абсолютным моментом и `now >= stale_after`.
5. §10.5 gate и примеры §5, §10 и Appendix A синхронизированы с этими изменениями.

Остальные нормативные положения между этими pins не менялись: root index, лог, upkeep, trust, sources и migration §13. Поэтому разногласия по multiline log/root index/upkeep нельзя объяснять этой сменой SPEC; они расследуются в issue-005.

`generated.at` и `verified.at` уже были datetime: нужна проверка общей политики offset/precision, а не массовая конверсия. Заголовки `log.md` остаются календарными датами по §9: общее предложение о timestamp-valued keys не превращает их в datetime.

## Почему недостаточно поменять regex

Сегодня `IsStale` отбрасывает время и строит midnight UTC из календарной даты входного `time.Time`. После обновления `2026-09-23T18:00:00+07:00` должен устаревать ровно в `2026-09-23T11:00:00Z`, а не с начала дня. Два разных offset могут представлять один момент; лексикографическое сравнение строк больше не определяет порядок времени.

У старого `last_modified: 2026-06-01` неизвестны ни время, ни timezone. Автоматическое добавление `T00:00:00Z` придумывает точность. Особенно опасен `usage_window.to`: конец календарного дня не равен его началу. SPEC называет диапазон, но не задаёт правила преобразования даты в границы instant interval.

## Карта затронутых контрактов и реализации

Все `file:line` ниже — существующие точки входа, а не предлагаемые API.

| Область | Текущее поведение и места проверки |
|---|---|
| Go public types | `bundle/frontmatter_v02.go:52`, `:95`, `:115`: поля source/window и `DateValue` публичны. `UsageWindow.FromValue/ToValue`, `ProvenanceSource.LastModifiedValue` возвращают DateValue (`:1813`). Нельзя тихо превратить тип «validated YYYY-MM-DD» в произвольный datetime. |
| Observation/parser | `bundle/frontmatter_v02.go:1604` парсит date, `:1627` имеет отдельный datetime parser; `bundle/observation_v02.go:262` сохраняет форму и состояния stale_after. Сохранить absent/malformed, raw, aliases/merges, ambiguity и cancellation. |
| Staleness | `bundle/frontmatter_v02.go:756`, `validator/strict_v02.go:511`: civil-date truncation. Для нового контракта сравнивать instant без усечения. |
| Validator | `validator/strict_v02.go:254`, `:290`, `:507`: date-only warnings и window ordering; severity менять только по отдельно описанному контракту. |
| Store | `store/change.go:100`, `:862`, `:875`, `:896`: date validation; `window.From > window.To` неверен для разных offset. Изменения канонического encoding/fingerprints требуют отдельной проверки replay/idempotency. |
| Mutation | `mutation/v02_operations.go:275`, `:415`, `:593`, `:621` вызывают yamlDate; `mutation/yaml_render_value.go:61` и `mutation/yaml_sequence_merge.go:673` различают date/datetime. Общая YAML поддержка datetime уже есть, но доменные операции date-only. |
| CLI | `internal/okfcli/projection.go:41`, `internal/okfcli/run.go:41`, `:695`: --as-of принимает дату; graph имеет отдельный путь parsing. Синхронизировать validate/info/graph и JSON. |
| MCP | `internal/mcpserver/tools.go:2721` optionalDate; `:869`, `:1150` форматируют date. В `contracts/*` list/validate/graph inputs, list/validate outputs, read output, preview/apply patch inputs используют `format: date`. Обновлять handler и schemas вместе. |
| Graph | `graph/projection.go:275`, `:493`, `:502` и `graph/contracts/v0.2.json:79`, `:111`: xsd:date для staleAfter, stalenessAsOf, lastModified, usageFrom/To. Простая подмена литерала при старом datatype недопустима; профиль является публичным контрактом. |
| Migration | `mutation/migration.go:2797` и соседние блоки уже сравнивают generated/timestamp как instants. Не превращать существующую v0.1→v0.2 migration в незаметную нормализацию v0.2 dates. Нужен отдельный, явный plan для revision upgrade. |
| Fixtures/docs | `fixtures/v02/spec-lock.json`, `validator/fixture_test.go:119`, `bundle/observation_v02_test.go:260`, `graph/profile_test.go:1941`, `store/change_v02_test.go`, `mutation/v02_operations_test.go`, CLI/MCP contract tests и README. Старые fixtures сохраняют смысл legacy regression corpus. |

## Путь совместимости — решение принято в [ADR 0003](../../docs/adr/0003-okf-v02-temporal-revisions.md)

1. Сначала ADR/contract matrix: различать format version `0.2` и spec revision/temporal profile в наших runtime/report contracts. Не придумывать `okf_version: 0.3` за upstream и не полагаться на один `0.2` для выбора семантики.
2. Старые public accessors, date-only constructors и graph profile сохраняют документированную семантику до явной версии/депрекации. Для нового поведения — additive revision-aware API или новый согласованный профиль. Решить имена и поверхность до реализации.
3. Новый consumer может читать обе формы, сохраняя precision/raw/revision, но должен отличать legacy date от datetime нового временного контракта. Это различие optional family само по себе не превращает bundle в base non-conformant: severity остаётся в согласованной strict/advisory границе. При запросе instant comparison для legacy date без политики — явный unsupported/unknown outcome, а не false «свежее».
4. Новые producers по новому профилю пишут только offset datetime. Producer старого профиля остаётся воспроизводимым. Автоопределение редакции по одному встреченному значению не применять к смешанным bundles.
5. --as-of и MCP as_of: сохранить старую date-only семантику для старого профиля. Для нового профиля принимать точный datetime; date-only ввод требует явной и документированной политики, а не локальной timezone машины. Конкретные flags/fields — предмет ADR.
6. Revision migration — только preview/apply через Go mutation/store. Пользователь задаёт mapping/policy: timezone и значение времени для каждого семейства; неизвлекаемые instants остаются unresolved. Сохранять комментарии/стиль/unknown keys; не менять unaffected bytes. Нельзя заявлять, что автоматически назначенный midnight — восстановленный факт.
7. Для старого graph profile оставить xsd:date; для нового согласовать versioned projection с xsd:dateTime и разрешением precision. Не менять datatype «внутри той же схемы» без compatibility strategy.
8. Pin нового SPEC и SHA обновлять вместе с runnable contracts и отдельным corpus, оставляя legacy revision доступной для regression. Новый pin не является поводом ослабить lossless/transactional guarantees.

## Принятые решения (Spec-First / Contract-First)

- [x] Согласовать способ выбора revision/profile, default и rollout, отражение в report/MCP/graph. Без этого нельзя менять текущие defaults.
- [x] Сравнить отдельные revision profiles с более простым additive precision-aware read API. Выбрать минимальный вариант, сохраняющий опубликованные гарантии; несколько постоянно поддерживаемых режимов не являются заранее заданным требованием.
- [x] Согласовать допустимый subset ISO 8601: RFC3339 с Z/±HH:MM, fractional precision, offset bounds, `-00:00`, leap seconds. Upstream говорит ISO 8601, а наши текущие APIs — RFC3339; не выдавать более узкую грамматику за полный upstream без описания.
- [x] Согласовать legacy date conversion по каждому полю и смысл usage_window endpoints. В частности, не расширять нормативный диапазон до inclusive interval только из нынешнего комментария store.
- [x] Согласовать additive Go API и версионирование JSON Schema/graph; решения зафиксировать до обновления fixtures.
- [x] Определить warnings/errors для legacy values в выбранной revision и точную семантику отсутствующего as_of. Не добавлять зависимость от текущего времени туда, где его раньше не было.

## Acceptance и тесты (AAA)

Во всех новых Go tests явно отделять Arrange / Act / Assert; таблицы допустимы.

- [x] Arrange: pinned old/new SPEC + hashes. Act: проверка corpus contracts. Assert: каждый fixture привязан к revision; датированные примеры нового Appendix A проходят новый профиль, старые — старый.
- [x] Arrange: stale_after с +07:00, эквивалентный Z, nanoseconds, моменты непосредственно до/ровно/после границы. Act: bundle/validator/CLI/MCP/graph. Assert: единый результат и inclusive `>=`, без calendar truncation.
- [x] Arrange: usage_window с offset, где лексический и временной порядки различаются. Act: parse/validate/mutate. Assert: ordering по instant, raw сохранён; эквивалентные instants допустимы согласно согласованному контракту.
- [x] Arrange: unquoted/quoted/explicit !!timestamp/!!str, timezone-less datetime, неверная дата, duplicate keys, merges/aliases, null, cancellation. Act: observation и validation. Assert: consistent absent/malformed/valid/ambiguous outcomes, без потери raw и fail-open.
- [x] Arrange: существующий Go consumer, date CLI/MCP requests и старый graph datatype. Act: compatibility suite. Assert: старое поведение воспроизводится или break выделен отдельной согласованной версией, а не спрятан в patch.
- [x] Arrange: legacy dates без mapping. Act: revision migration preview. Assert: unresolved, отсутствует выдуманный instant; apply не пишет неполный план.
- [x] Arrange: явный mapping + CRLF/comments/unknown keys + concurrent edit. Act: preview/apply/replay. Assert: только intended edits, прежние conflict/fingerprint/idempotency/transactional guarantees.
- [x] MCP schemas принимают именно то, что принимает runtime; output удовлетворяет соответствующим revision schemas. Существующие Graph JSON-LD/N-Triples используют корректный datatype; новый RDF-формат ради этой задачи не добавляется.
- [x] Запустить `go test ./...`, `go test -race ./...`, `go vet ./...`, contract/golden suites; обновить EN/RU docs и migration guidance.

## Зависимости и риски

- Issue-005 interoperability corpus может выполняться параллельно; multiline log/index/upkeep не блокируются временным ADR.
- Init/templates, backfill и новые producers должны использовать явно выбранный temporal profile; до решения не массово переписывать corpus.
- Search/viewer должны учитывать «staleness unknown», не превращать неподдержанную дату в fresh.
- Риск скрытого breaking change высок: не только validation, но и public return types, schema formats, RDF datatypes, CLI deterministic runs и operation identity.
- Эта задача не включает и не разрешает замену Go mutation/store чужой Python migration, автоматическое обновление всех знаний или изменение опубликованных release tags.

## Решение о переносе

Берём из okf-skills обновлённый upstream pin и проверяем его последствия для нашей Go-модели. Их validator не становится источником нормативных решений: его успех на bundle не доказывает совместимость, а смена date на datetime затрагивает больше, чем регулярное выражение. Итоговый контракт и его проверка принадлежат этой задаче.

Доказательства: [date SPEC lock](../../fixtures/v02/spec-lock.json), [instant lock](../../fixtures/v02/spec-lock-instant.json), [профильные тесты bundle](../../bundle/temporal_profile_test.go), [validator](../../validator/temporal_profile_test.go), [CLI](../../internal/okfcli/temporal_profile_test.go), [MCP](../../internal/mcpserver/temporal_profile_test.go), [graph](../../graph/temporal_profile_test.go) и [transactional upgrade](../../mutation/temporal_upgrade_external_test.go). На коммите `ec1299ab22731e85efa194695ee11609c9cf335e` [Linux test/vet/module integrity](https://github.com/skosovsky/okf/actions/runs/36244091051/job/108409919281) прошёл `go vet ./...`, `go test ./...`, проверку модулей, bundle validation и сборку viewer; [Linux race detector](https://github.com/skosovsky/okf/actions/runs/36244091051/job/108409919390) прошёл `go test -race ./...`.
