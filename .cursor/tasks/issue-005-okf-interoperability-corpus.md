# OKF interoperability corpus: Markdown structure, extensions и ложные diagnostics

Статус: реализовано; профильный frozen corpus и regression tests закреплены. Приоритет: P0 для подтверждённых parser/validator bugs.
Реализация и тестовый harness — Go. Python validator другого проекта используется
только как исследуемый объект, не как зависимость продукта или CI.

## Результат

Добавить воспроизводимый interoperability corpus и устранить подтверждённые
ложные diagnostics. Разделить ошибки нашего consumer, отклонения foreign producer,
неоднозначности спецификации и дополнительные toolkit policies. Не добиваться
«нулевого числа предупреждений» ослаблением всех проверок.

Связанная задача: [004 — temporal contract](issue-004-align-upstream-v02-temporal-contract.md) — изменения datetime
и закрепление редакции SPEC.

## Проверенный baseline, 2026-09-26

- Наш commit: `ed7ddc28cd127682023bd150ee377906b817e099`.
- Foreign: `scaccogatto/okf-skills@68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5`.
- Наша SPEC: `fixtures/v02/spec-lock.json:1`, upstream
  `GoogleCloudPlatform/knowledge-catalog@3fcbb9f828c2f23d109c855ee403c3a4c81f3a96`,
  `okf/SPEC.md`, sha256 `5a3311d270bebb16d558010e75064f5b75323f284992641732b1c8097511f948`.
- Foreign SPEC: `skills/okf/reference/SPEC.md`; `.okf/reference/okf-spec.md:19`
  указывает upstream `GoogleCloudPlatform/open-knowledge-format@0b87c52`.
- Исследовательский checkout: `/tmp/okf-skills-research-20260926`.
- Отчёт повторного прогона: `/tmp/okf-interop-current.json`.
- Команда: `GOCACHE=/tmp/okf-research-gocache go run ./cmd/okf validate --path /tmp/okf-skills-research-20260926/.okf --strict --check-links --check-orphans --json`.
- Результат: 30 files, 312 errors, 18 warnings, policy_failures=0.
- Foreign validator на том же bundle: 24 concepts, 5 indexes, 1 log,
  conformant=true, 0 errors, 0 warnings. Это наблюдение, не oracle соответствия SPEC.

| Diagnostics | Количество | Классификация |
| --- | ---: | --- |
| `log_structure_invalid` | 310 errors | Наш false positive: физические переносы строк внутри Markdown list item |
| `index_structure_invalid`, body | 1 error | Наш строгий профиль index: заголовок-введение без непосредственного списка; требуется нормативное решение |
| `index_structure_invalid`, upkeep | 1 error | Foreign extension и конфликт трактовок §8/§11; требуется нормативное решение |
| `actor_invalid` | 14 warnings | Foreign actor shape вне трёх форм §7; предупреждения оправданы |
| `source_footnote_unknown` / `source_footnote_definition_missing` | 2 warnings | Наш false positive на `[^label]` внутри inline code |
| `orphan_unlisted` | 1 warning | Наша opt-in policy ближайшего directory index, не нарушение upstream |
| `source_field_invalid` | 1 warning | SPEC revision drift: datetime в sources[].last_modified |

## Evidence и выводы

### 1. Wrapped log entries — подтверждённый наш bug

`validator/validate.go:731` добавляет **все** `item.ContinuationTexts` в список
non-list errors. `internal/markdownowner/reserved.go:156` извлекает туда строки
одного AST paragraph/text block. То есть parser уже установил принадлежность
текста к list item, а validator отвергает его только за перенос строки.

Foreign `.okf/log.md:5` содержит обычную запись `* **Release 0.9.6**`, продолженную
строками с двумя пробелами. Все 310 errors этой группы — сообщения
`log date group contains non-list entry` для таких продолжений.

Обе редакции SPEC §9 требуют flat list, date groups и newest first; запрета на
переносы prose внутри одного item нет. Flat list означает отсутствие вложенных
списков, а не «ровно одна физическая строка на запись».

Минимальный repro (`/tmp/okf-interop-mini/wrapped-log/log.md`) даёт 1 error:

```markdown
# Log

## 2026-09-21
* Entry starts
  and continues.
```

`fixtures/negative/log-plain-paragraph/log.md:5` сейчас содержит lazy continuation
без пустой строки, а не самостоятельный paragraph. Наличие negative fixture
не превращает корректный Markdown в нарушение спецификации. Сменить fixture на
реальный внешний paragraph (отделённый blank line), а lazy continuation проверить
положительно. См. также `internal/markdownowner/reserved_test.go:45`.

Смежный риск: `bundle/log.go:27` читает только bullet lines и теряет continuation
при typed чтении. Исправление validator должно сопровождаться согласованием
ParseLog/Markdown и проверкой сохранности текста, а не только зелёным CLI output.

### 2. Index title / introduction — убрать дополнительный запрет

Foreign `.okf/index.md:6` — H1 с названием bundle, затем prose intro и H1 Skills
со ссылками. `validator/validate.go:642` требует linked entries под каждым
heading, поэтому отвергает вводную секцию.

SPEC §8 (`skills/open-knowledge-format/references/spec-v02.md:509`) описывает
секции группировки concepts под headings и показывает пример, но не формулирует
отдельный MUST «каждый heading обязан непосредственно содержать list entry».
Foreign `check_index` (`skills/validate/scripts/okf_validate.py:319`) вообще не
проверяет body, поэтому его принятие bundle само по себе ничего не доказывает.

Направление исправления: принимать документный title и introductory prose при
наличии дальше корректного directory listing. Не требовать entries непосредственно
под каждым heading и не считать пример SPEC исчерпывающей грамматикой Markdown.
Отличать вводный title от grouping heading: секция, заявленная как группа
concepts/subdirectories, должна содержать хотя бы одну linked entry. Пустая
grouping section остаётся `index_structure_invalid`; title/intro перед другими
заполненными секциями допустимы. Для root index без единой linked group сохранять
существующую проверку empty listing по §8/§11.3.
Зафиксировать это прочтение §8 в conformance matrix и fixtures до кода; отдельное
пользовательское решение о разрешении заголовка не требуется. Реальные grouping
sections и malformed directory listings по-прежнему проверять по §8, не отключать
всю структурную проверку. Не обосновывать вывод только отсутствием слова MUST:
§11.3 отсылает к структуре §8, но запрет title/introduction из неё не следует.

### 3. upkeep: enforced — extension, а не стандартное поле OKF

Foreign `.okf/index.md:3` использует поле для локального Stop hook.
`skills/validate/scripts/okf_validate.py:333` специально исключает `upkeep` из
extra keys и ссылается на запрет §11 отвергать unknown keys. Наш
`validator/validate.go:560` разрешает только `okf_version` и выдаёт base ERROR.

В §8 описана допустимая форма стандартного producer output: исключение для
root okf_version. В §11 отдельно и без явного исключения для root index задан
consumer MUST NOT reject bundle because of unknown additional frontmatter keys.
Текст не объясняет их взаимодействие отдельным примером; это следует честно
отметить, но не превращать в блокер или произвольный выбор нового стандарта.

Рабочее прочтение для реализации: при корректной root declaration неизвестные
соседние keys сохраняются и не вызывают base ERROR; потребитель не интерпретирует
их как стандартные поля. Наш producer по-прежнему пишет только нормативный
okf_version; upkeep config держим вне index. При необходимости отдельная opt-in
producer-policy диагностика, не скрытое расширение base conformance. Зафиксировать
основание (§11 consumer prohibition) в contract note и generic fixtures, без
special-case whitelist upkeep. Malformed/duplicate okf_version и frontmatter
в nested index остаются отдельными проверками. Root frontmatter без declaration
проверить отдельным case по reserved-file contract, не распространять это решение
автоматически на любые запрещённые frontmatter blocks.

### 4. Inline-code footnote — подтверждённый наш bug

Foreign `.okf/components/validator.md:33` объясняет синтаксис буквальным
`[^label]` внутри backticks. Наш CLI выдаёт две citation warnings.
Минимальный repro `/tmp/okf-interop-mini/code-footnote/a.md`:

```markdown
---
type: Note
---
A literal `[^label]` example.
```

`docs/contracts/okf-v0.2-conformance.md:18` уже требует игнорировать code-owned
markers. `internal/markdownowner/footnotes.go:71` также обещает исключить code.
Причина в текущей реализации: `internal/markdownowner/migration.go:1162`
добавляет inner text CodeSpan в excluded, а `:1188` снимает исключение с **любого**
span, равного `[^label]`, ради shortcut-reference exception. Ownership kind
теряется. Требуется ограничить исключение shortcut links, не code spans.
Проверить влияния общего owner на read APIs и migration; не чинить regex только
в validator и не удалять поддержку настоящих footnotes.

### 5. Остальные предупреждения

- 14 actors используют `agent:...` (например foreign `.okf/components/validator.md:8`).
  Обе SPEC §7 перечисляют producer/version, human:id и process:id. Наш
  `bundle/frontmatter_v02.go:1738` соответствует этим формам; foreign
  `skills/validate/scripts/okf_validate.py:59` принимает произвольный namespace:id.
  Сохранить эти warnings и raw values; не нормализовать actor выдуманными данными.
- `.okf/components/backfill-events.md` указан в root index (`.okf/index.md:22`),
  но отсутствует в `.okf/components/index.md`. Наша политика
  `validator/validate.go:1243` проверяет directory coverage. Это полезное
  предупреждение навигации, но не основание объявить bundle неконформным.
- `.okf/reference/okf-spec.md:14` содержит `2026-08-21T00:00:00Z`. Warning по
  старой date-only SPEC ожидаем и закрывается задачей revision alignment.

## Scope реализации

1. Закрепить foreign SHA, file selection, digest, source URLs и лицензионные
   атрибуты в manifest для corpus. Предпочесть небольшие самостоятельные fixtures
   с точной derivation; полный foreign snapshot допустим только при обосновании.
2. Corpus запускать Go tests без сети, Python, live checkout и временных путей.
   Разделить `base`, `strict`, `check-orphans`; expected codes/severity/field paths
   фиксировать раздельно от человеческих messages.
3. Обновить contract/ADR для log ownership, index title и root extension policy.
   Для подтверждённых bugs восстановить уже заявленные гарантии.
4. Исправить shared Markdown owner и Go validator, проверить bundle typed log
   parsing/serialization и consumers. Не вводить второй Markdown parser.
5. Обновить EN/RU описание conformance и migration notes, если публично меняются
   diagnostics/policy. Старый negative corpus пересмотреть по основаниям,
   сохранить независимые проверки реально malformed structures.

## Acceptance: AAA

- Arrange: wrapped LF/CRLF list entries, indented и CommonMark lazy continuation.
  Act: parse и base/strict validate. Assert: текст принадлежит тому же entry,
  отсутствует log_structure_invalid; typed reading не отбрасывает продолжение.
- Arrange: отдельный paragraph после blank line, nested list, неверная дата,
  неправильный порядок дат. Act: validate. Assert: реальные нарушения остаются
  диагностируемыми согласно принятому contract, без ошибки на каждый soft wrap.
- Arrange: inline-code `[^label]`, включая multiline code spans, рядом с настоящим
  `[^actual]` и его definition/source. Act: общий ownership/read/migration и
  validator. Assert: code bytes не считаются attribution и не переписываются;
  настоящие references остаются доступны, включая shortcut-reference case.
- Arrange: title + intro + linked groups, title + H2 groups, пустая grouping
  section и malformed list. Act: validate. Assert: title/intro перед заполненными
  группами принимаются; пустая grouping section, полностью пустой listing и
  malformed list дают `index_structure_invalid` по §8/§11.3, независимо от
  выбранного H1/H2 оформления.
- Arrange: root `upkeep` и другое неизвестное поле, nested index frontmatter,
  duplicate/malformed `okf_version`. Act: read/validate/round-trip.
  Assert: unknown fields сохранены, применена выбранная generic policy,
  standard declaration errors остаются отдельно видимыми.
- Arrange: agent:..., producer/version, human:..., process:... и directory orphan.
  Act: разные validation profiles. Assert: actor warning остаётся soft,
  orphan возникает только при запрошенной policy и не меняет base conformance.
- Arrange: frozen foreign corpus. Act: reproducible Go validation.
  Assert: отчёт объясняет каждый оставшийся класс; нет 310 wrapped-line false
  positives и двух inline-code false warnings. Не фиксировать требование
  «весь foreign corpus = 0 warnings».

## Non-goals

- Перенос Python validator, его неполной log/index проверки или special-case upkeep.
- Изменение foreign bundle, автоисправление provenance, публикация upstream issues.
- Реализация datetime contracts в этой задаче (зависимость от отдельной задачи).
- Подмена conformance внешним validator output, выравнивание всех warning counts.
- Снятие resource limits/cancellation, ослабление lossless mutation guarantees.

## Решение о чужом checker

Его принятие собственного bundle — полезный внешний пример, но не oracle: body index практически не проверяется, log проверяется главным образом по date headings. Исправляем наши доказанные ложные diagnostics по тексту SPEC и минимальным fixtures; не добавляем второй Python validator и не добиваемся искусственного равенства warning counts. При адаптации чужих fixtures фиксируем commit, derivation и нужные лицензионные атрибуты.

## Проверка выполненной задачи

Запустить профильные Go tests для markdownowner, bundle, validator и затронутых
mutation consumers, затем `go test ./...`. Проверить fixtures/contract drift
tests. В отчёте указать before/after по диагностическим классам и нормативное
основание каждого изменённого результата.

## Состояние приёмки

Все семь групп Acceptance выше покрыты [offline frozen corpus](../../fixtures/interoperability/README.md), [точными expected diagnostics](../../validator/interoperability_corpus_test.go) и [reserved-file regression tests](../../validator/interoperability_reserved_test.go). Полный pinned foreign bundle после исправления: 30 файлов, 0 errors, 16 warnings; причины оставшихся предупреждений и исходные SHA записаны в README corpus. Два независимых acceptance review выполнены в ходе реализации.
