# 014 — Упростить подготовку миграции без ослабления preview/apply

Статус: выполнено. Приоритет: P2. Зависимости: 004/005; текущий migration contract остаётся authoritative до явно принятого изменения.

## Проблема и смысл чужого подхода

Чужой `validate --migrate` удобен: одна команда переводит старые поля в новые. Пользователь небольшого git-tracked bundle получает понятный быстрый путь. У нас безопасный API требует explicit actor/time/citation mappings, но подготовку этих inputs можно сделать удобнее без второго mutation engine.

Reference: [migration implementation](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/skills/validate/scripts/okf_validate.py#L402).

## Что не переносить как default

- Молчаливое `generated.by=process:okf-migrate` вместе с историческим timestamp: процесс действительно сделал преобразование, но не обязательно создал историческое содержание в эту дату.
- Regex extraction части Citations с удалением всей секции. Нераспознанная prose и claim attribution не должны исчезать.
- Последовательные in-place writes до проверки всего результата; нормализацию CRLF/BOM и публикацию index до остальных файлов.

Пояснение: для маленького bundle в Git одношаговая команда бывает удобна, а `process:okf-migrate` честно называет исполнителя преобразования. Проблема в привязке этого нового actor к старому времени создания содержания. Regex создаёт entries без восстанавливаемых source IDs и может удалить непонятый текст. Последовательная запись допускает смешанную версию при сбое; Git помогает откатить её позднее, но не защищает читателя в момент сбоя. Наша задача — дать столь же короткий пользовательский путь через существующий preview/apply, где неоднозначность видна до записи.

## Предлагаемый безопасный UX

1. Обычный preview выявляет blockers/manual actions существующим planner.
2. Go helper экспортирует заполняемый manifest actor/time и citation mappings, с точными parser-owned selectors и evidence spans.
3. Однозначные title/resource candidates допускаются как явно помеченные предложения. Не присваивать source IDs или авторов как установленный факт; согласовать deterministic suggestion policy отдельно.
4. Пользователь/агент подтверждает выбранные inputs, видит preview и вызывает существующий apply с proof/digest/source freeze. Отдельное подтверждение не требуется, если конкретные inputs и запись уже явно авторизованы задачей.
5. Helper использует существующий closed DTO; не создаёт параллельного несовместимого формата mapping.

## Contract-first решения

- Выбрать CLI surface для экспорта шаблона и outcome unresolved suggestions. Никакой implicit migration в validate/read/fmt.
- Разделить provenance исходного содержания и actor операции. Возможность явно записать process actor — отдельная документированная producer policy, не способ обойти обязательные inputs.
- Preview/helper по умолчанию zero-write для bundle. Если запрошен template file, запись только по явному output path вне source documents, с collision policy.
- Определить invalidation экспортированного шаблона при изменении входного bundle; apply всё равно проверяет существующие revision/proof guards.
- Не менять существующие manual action/error codes без необходимости; additive proposal data не выдавать за авторизованный migration proof.

## Приёмка

- [x] Пользователь может подготовить mapping без ручного восстановления legacy numbering/raw selectors из ошибок.
- [x] AAA Go fixtures: простой numbered list, unnumbered source, mixed sources/Citations, duplicate labels, code-owned псевдомаркеры, URL без title и нераспознанная prose.
- [x] Helper сохраняет ambiguity; неизвестные source/actor/time не заполняются выдуманными фактами.
- [x] Никакие непредложенные bytes не теряются, CRLF/BOM/unknown YAML сохраняются действующим mutation layer.
- [x] Apply с неподтверждённым/устаревшим inputs по-прежнему отвергается существующим контрактом; no-op/failed preview не создаёт private store artifacts.
- [x] Docs демонстрируют простой happy path и один ambiguous case без обещания однокнопочной корректности.
- [x] AAA Go test для template output path: существующий файл не перезаписывается без явно согласованной collision policy; отказ не меняет bundle и ранее существовавший template.

Доказательства: [`migrate-prepare` и закрытые шаблоны](../../docs/contracts/migration-preparation.md), [тест parser-owned selectors и неоднозначности](../../internal/okfcli/migration_prepare_test.go), [CLI-тесты collision, stale/unconfirmed inputs, CRLF и BOM](../../internal/okfcli/migrate_prepare_command_test.go). Leading BOM перед frontmatter распознаётся как неподдерживаемый случай и миграция завершается без записи; сохранение bytes здесь означает fail-closed, а не обещание автоматически мигрировать такой файл. Два независимых ревью реализации и проверок: PASS.

## Не входит

Новый migrator, замена transactional store, автоматическая верификация, перенос regex алгоритма, безусловный режим `--force`.
