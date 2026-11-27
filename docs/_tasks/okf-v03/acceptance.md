# Приёмка кандидата OKF 0.3

Кандидат `84ead0387861add577f1e4326d16e23f2385d240` собран в отдельном локальном checkout от `0d01adcae4023f5c2c24a928028d7f5fcd9e0c3b`. [Нормативная матрица](normative-evidence.md) связывает требования §§3–12 с проверками. [Журнал](progress.md) хранит результаты промежуточных проверок.

Последующий прогон на 12 публичных наборах и два исправления валидатора описаны в [отчёте о корпусе](public-corpus.md).

## Якоря доказательств

- Копии лицензии и спецификации сверены побайтово с [upstream `527e81a`](https://github.com/open-knowledge-format/open-knowledge-format/tree/527e81aff55992be21507e97004286789126f88c). `TestPinnedOKF03Specification` проверяет SHA-256 нормативного текста. `git bundle verify /private/tmp/okf-v03-candidate-84ead03.bundle` подтверждает переносимый коммит для рабочей копии с базой `0d01adc`.
- Артефакты первого кандидата `ca0e6a8` находятся в `/private/tmp/okf-v03-candidate-checks.n5JdAM`. `88ad3cb` обновляет справочники EN/RU и метки архивных инструкций. `6b8238a` добавляет причину участия проверки в доверии в Go, CLI и MCP. `84ead03` исправляет валидацию `as_of` на транспортной границе и возвращает точное совпадение архивного протокола backfill. Проверки текущего кандидата пишутся в `/private/tmp/okf-v03-release-candidate.U7l9RB`.
- Внешние наборы закреплены на [Acme Retail `527e81a`](https://github.com/open-knowledge-format/open-knowledge-format/tree/527e81aff55992be21507e97004286789126f88c/bundles/acme_retail), [Crossplane `e72a5db`](https://github.com/jkroepke/okf-crossplane-v2/tree/e72a5db05542469d3a0f138744f35a933170d1ec/catalog) и тексте Appendix A. В Appendix A отсутствующие файлы созданы как инертные заглушки. Дата сравнения во внешнем корпусе `2026-11-27T00:00:00Z`. Вычисления и attesters остаются данными при чтении, проверке и экспорте.

## Межинструментальные сценарии

| Задачи | Действие и проверка | Результат |
|---|---|---|
| T01–T04, T08 | `validate --spec 0.3 --strict --check-links`, stdio `validate_bundle`, `info`, JSON-LD и offline viewer на внешних наборах | На `84ead03` CLI и MCP совпали по кодам, severity и путям. Acme Retail 9 концептов и ноль диагностик. Crossplane 108 концептов и одно предупреждение о сноске. Appendix A 3 концепта, два предупреждения о сопутствующем Markdown и один истёкший срок. Ноль ошибок на всех трёх |
| T05 | `read_concept` и viewer на вычислениях Acme Retail и Appendix A | Оба вычисления Appendix A доступны для проверки, revenue свежий, profit просрочен. Результаты приложений записаны в `interoperability-final` |
| T06 | `init`, `fmt -w`, `index`, setup preview/apply, MCP replace/patch и store round-trip | На `84ead03` init сохранил строковый title. Setup скопировал чужой ключ и CRLF побайтово. Форматтер сохранил значение. Crossplane не потерял root frontmatter и повтор индекса дал те же байты |
| T07 | `migrate-v03` и stdio preview/apply/replay на двух одинаковых копиях 0.2 | На `84ead03` одинаковый plan digest, одинаковые ревизии и байты после записи. Неоднозначный YAML остановился на предпросмотре. Независимый ekar-is `3a0f679` прочитал опубликованный концепт. Доказательства `migration.PEQMxO` |
| T08 | Запросы `search_concepts` и `get_neighbors` с `as_of` через производственную конфигурацию MCP | `84ead03` прошёл `TestProductionMCPAcceptsVersionedQueryComparisonTimes` для даты 0.2, timestamp 0.2 и обеих форм 0.3. Реальный stdio прошёл все 16 инструментов. Ответы и квитанции в `stdio-all-tools` |
| T09 | Пять бинарников, 9 сценариев `action-integration.yml`, backfill `extract → plan → apply → replay`, upkeep `baseline → check → decision` | На `84ead03` отчёты Action совпали с CLI, backfill сохранил чужой ключ индекса и ссылку на Git-доказательство, повтор вернул ту же квитанцию. Upkeep зафиксировал `explicit_unaffected` и сохранил заметку побайтово. Доказательства `interoperability-final`, `workflows-complete` |
| T10–T11 | Проверка двух локалей и offline demo, `node viewer/testdata/regression-016/routes.cjs`, Jekyll из чистого checkout | На `84ead03` GitHub Pages 232 собрал 199 страниц и 2 592 внутренних ссылки и якоря без ошибок. Все три демо совпали побайтово. Результаты `pages-native/links.json` |
| T12 | Darwin Go 1.27.1, Linux arm64 Go 1.25.5, race, Android и Windows cross-build, выбор iOS backend, Windows тест-бинарь | На `84ead03` сборки и выбор backend подтверждены в `darwin-cross-builds.log`. Четыре Windows-теста этого коммита прошли в CrossOver 26.3.0. Проверки архивной документации и исторических фикстур прошли в `targeted.log`. Полные журналы в каталоге кандидата |
| T12 | `BenchmarkToolkit` на 100 концептах 0.2, `-benchtime=180ms -count=5 -cpu=1` | Медиана памяти загрузки 2 359 568 → 2 383 217 B/op. Для JSON-LD 10 949 194 → 10 957 901 B/op, медиана выделений совпала. Замер времени шёл параллельно с длительными тестами. Сырые результаты в `benchmark/` |

На Darwin `go test -p 4 -json -timeout 45m ./...` завершил все пакеты на `84ead03`. Прошли 36 пакетов, ещё 8 без тестов. Длительный финальный шаг `cmd/go` сохранял результаты в кэш после PASS последнего пакета. Детали в `darwin-test.jsonl` и `darwin-go-after-pass.sample.txt`.

На Linux подтверждены те же 36 пакетов в обычном режиме и под race detector. В первой попытке обе команды завершились с кодом 1 после OOM в Go-процессах, сохранявших результаты в кэш. В обычном журнале к этому моменту уже были итоговые `ok` всех 36 пакетов. В race-журнале их было 35. Оставшийся `internal/mcpserver` прошёл командой `go test -race -count=1 -p 1 -parallel 2 -json -timeout 25m ./internal/mcpserver` с кодом 0. `acceptance-summary.json` сверяет множества пакетов. Исходные журналы и сообщения ядра сохранены отдельно. Linux `go vet`, `go mod tidy -diff`, `go build`, проверка whitespace, тест viewer и проверка трёх пакетов навыков завершились успешно.

Репозиторный манифест и marketplace Claude Code проходят `claude plugin validate --strict`. В отчёте профиля каталога отмечено отсутствие лицензии самого плагина и иконки. Установка в пользовательские хосты и публикация в каталоге не входят в локальную проверку.

Сайт собран на Ruby 3.3.4. Дополнительная запись `strscan` понадобилась локальному lock-файлу Bundler для метаданных `rexml` на Darwin. Файлы репозитория не менялись при сборке. Linux-прогоны выполнены в отдельной Debian 12 arm64 VM от обычного пользователя с Node 18.20.4. Контейнеры Docker Desktop задерживались до старта процесса и были удалены после переноса проверок.

## Способы записи

| Путь | Проверка сохранения данных на `84ead03` |
|---|---|
| Go document session, replace и move | `TestOKF03LogRenderingAndPublicationShareDateRules`, `TestOKF03ReplaceQuotesCoreStringBeforePublishingAndReplaysReceipt`, `TestOKF03MoveRewritesEncodedMarkdownAndPathFields`, `TestMoveConcept_PreservesDuplicateUnrelatedRelationExtensions` |
| `init` | `TestOKF03InitPublishesBundleMetadata` и бинарный сценарий `workflows-complete`. Команда создаёт новый набор, поэтому проверены поля, заданные на входе, и строковая семантика заголовка |
| `fmt -w` | `TestOKF03CLIFormatRoundTripsCoreYAMLValues`, `TestOKF03FmtKeepsPlainLogAndCompanionBytes` и повторное чтение чужого вложенного поля в `workflows-complete` |
| `index` | `TestRegenerateIndexesKeepsRootFrontmatterBytes` и два прогона индексации копии Crossplane. SHA-256 неизменного frontmatter в `interoperability-summary.json` |
| Setup и backfill | `TestOKF03SetupCopiesCoreTypedDocumentsWithoutRewriting`, `TestOKF03BackfillPreservesRootAndRejectsLosingExtensions` и CLI apply/replay в `workflows-complete` |
| MCP write и patch | `TestOKF03WriteConceptPreservesExtensionOwnership`, `TestOKF03WriteReordersSourcesWithoutMovingClaimAttribution`, `TestOKF03ComputationPatchPreservesSingletonParameterExtensions` и `stdio-all-tools` |
| Миграция | `TestOKF03MigrationPreservesLegacyScalarValues`, `TestOKF03MigrationAndMoveOnlyRewriteTheLogLink` и одинаковые CLI/MCP preview, apply и replay в `migration.PEQMxO` |

Независимая проверка воспроизводится файлами `/private/tmp/okf-v03-release-interoperability.py`, `/private/tmp/okf-v03-stdio-all-tools.py`, `/private/tmp/okf-v03-candidate-workflows.py`, `/private/tmp/okf-v03-check-migration-scalar.py` и `/private/tmp/okf-v03-check-built-links.py`. Выпуск тега и публикация релиза остаются отдельными действиями владельца.
