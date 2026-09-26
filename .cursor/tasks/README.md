# План работ после исследования okf-skills

Исследованы наш `ed7ddc28cd127682023bd150ee377906b817e099` и [okf-skills@68ce7a0](https://github.com/scaccogatto/okf-skills/tree/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5). Это план задач, а не отчёт о выполненной реализации. Контракт каждого нового API сначала фиксируется в самой задаче и документации, затем реализуется на Go и проверяется тестами AAA.

## Что полезного осталось от исследования

У них хорошо проработан путь от пустого каталога до используемого knowledge bundle: starter templates, правила чтения и обновления, backfill истории, интерактивный просмотр и CI gate. Мы берём эти сценарии поверх существующих Go packages, CLI и MCP. Их read-only MCP подсказал `search_concepts` и `get_neighbors`; отдельный Python server не требуется. Для копируемых примеров нужны pinned source и соответствующие notices: toolkit MIT, vendored SPEC Apache-2.0.

Два внешних сигнала оказались особенно полезны. Новый upstream pin изменил временной контракт при том же `okf_version: "0.2"`. Прогон их bundle показал ошибки нашего кода: **310 из 312 errors** вызваны корректными переносами Markdown list items; typed `ParseLog` тоже теряет продолжения. Ещё **2 из 18 warnings** ложные: footnote внутри inline code. Два index errors разбираются по §8/§11 в [005](issue-005-okf-interoperability-corpus.md): вводный title допустим перед корректным listing, unknown root key рядом с корректной version declaration не должен вызывать base rejection. Остальные warnings: 14 чужих actor forms, один orphan по нашей дополнительной policy и один случай temporal SPEC drift.

Чужая миграция даёт удобный быстрый сценарий, но может потерять нераспознанный текст Citations, смешать время создания содержания с actor преобразования и оставить bundle частично переписанным при сбое. Поэтому [014](issue-014-go-migration-assisted-inputs.md) сокращает ручную подготовку inputs для нашего preview/apply. Их Stop hook полезен как opt-in напоминание, но одна правка log не доказывает актуальность concept; [013](issue-013-go-knowledge-upkeep.md) делает проверку точнее. Их benchmarks дают протоколы и случаи ошибок; опубликованные проценты не являются результатами нашего toolkit. [012](issue-012-go-agent-behavior-evals.md) запускает проверку поведения на нашей реализации, [015](issue-015-go-toolkit-benchmarks-after-fixes.md) отдельно измеряет производительность Go-кода.

## Порядок реализации и приёмка

Первый этап: 004 и подтверждённые bug fixes из 005 идут параллельно. Общие решения о revision, reserved index и diagnostics должны быть закреплены до выпуска новых producers. После них независимые работы можно вести параллельно; номера ниже задают рекомендуемую последовательность интеграции, а зависимости в задачах имеют приоритет над номером.

| Шаг | Задача | Условие приёмки |
| --- | --- | --- |
| 1 | [004 — временной контракт двух редакций SPEC](issue-004-align-upstream-v02-temporal-contract.md) | Пин и diff обеих редакций проверены; Go types, validator, store/mutation, CLI/MCP schemas и graph согласованы с выбранной совместимостью. Offset instants сравниваются корректно; старые dates не получают выдуманный часовой пояс; public compatibility и tests AAA проходят. |
| 2 | [005 — interoperability corpus и исправление Markdown](issue-005-okf-interoperability-corpus.md) | Offline Go fixtures воспроизводят все группы исходных 312/18; wrapped log и inline-code footnote больше не дают ложных diagnostics, `ParseLog` сохраняет текст. Title/intro принимается перед заполненными группами; пустая группа и листинг без ссылок остаются ошибками. Unknown root keys рядом с корректной version declaration не дают base rejection; настоящие ошибки, actor warnings и optional orphan policy различимы. |
| 3 | [015 — производительность Go toolkit](issue-015-go-toolkit-benchmarks-after-fixes.md) | После 004/005 реально выполнены повторные Go benchmark runs на pinned baseline и corrected commit; опубликованы corpus dimensions, raw results, ns/op, B/op, allocs/op и разброс. Быстрый отказ старого validator от корректного input не выдается за выигрыш. |
| 4 | [006 — `okf init` и шаблоны](issue-006-go-bundle-init.md) | Новая команда создаёт conformant bundle из пустого пути одним Go binary; conflict/retry/failure не перезаписывают знания и не оставляют частичную публикацию. Пример README повторён, Go AAA tests и проверка обновлённого skill lock проходят. |
| 5 | [007 — собственный knowledge bundle](issue-007-repository-knowledge-bundle.md) | 8–12 concepts отвечают на реальные инженерные вопросы, имеют проверяемые sources, не объявлены verified без проверки. Bundle находится вне служебной `.okf`, проходит Go validation и три ручных сценария навигации. |
| 6 | [008 — MCP search и neighbors](issue-008-mcp-search-and-neighbors.md) | Schema-first read-only tools дают bounded deterministic results, различают kinds связей и не теряют root/fragment/cancellation limits. Контрактные Go tests проходят; старые tools остаются совместимыми. |
| 7 | [011 — GitHub Action поверх Go validator](issue-011-go-validator-github-action.md) | Action и локальный CLI дают один JSON report и одинаковый outcome для одинаковых inputs; warning budget отделён от conformance/strict checks. Проверены path injection, missing bundle и ошибка исполнения; Action реально проходит integration fixtures. |
| 8 | [014 — удобная подготовка migration inputs](issue-014-go-migration-assisted-inputs.md) | Go helper выдаёт заполняемый mapping с точными selectors/evidence; ambiguity остаётся явной. Существующий preview/apply проверяет proof/revision; ни один неоднозначный или failed run не переписывает bundle. Существующий template output не перезаписывается без явно принятой collision policy. |
| 9 | [009 — автономный HTML viewer](issue-009-go-html-knowledge-viewer.md) | Go exporter создаёт offline HTML без runtime CDN; поиск, deep links, incoming/outgoing, provenance и раздельные trust/status/staleness проверены. Unsafe Markdown/URL не исполняются; large bundle и лимиты проверены. |
| 10 | [012 — поведенческие бенчи на нашем toolkit](issue-012-go-agent-behavior-evals.md) | После 004/005 и появления test bundle выполнен реальный pilot через наш Go CLI/MCP: pinned corpus, commands, raw rows, ошибки инструментов, метрики ответов, токены и время. Базовый writer→consumer использует существующий mutation API или подготовленное состояние, не требует 010/013. Control имеет доступ к правильному ответу; результат можно пересчитать Go analyzer. До фактического запуска задача открыта. |
| 11 | [010 — восстановление знаний из Git](issue-010-go-history-backfill.md) | Детерминированный Go extractor, resumable analysis и единственный writer сохраняют evidence и `draft`, обнаруживают отменённые решения; coverage не выдаётся за истинность. Preview/apply выдерживает cancel, конфликт revision и replay; качество проверено сценариями из 012. |
| 12 | [013 — поддержание знаний](issue-013-go-knowledge-upkeep.md) | Переносимый workflow и opt-in Go checker различают relevant changes, старую правку log, новые файлы и explicit unaffected. Не присваивают verification; после реализации выполнен отдельный writer→consumer rerun по harness 012 для влияния на актуальность и ответы. |

Шаги 3–9 после 004/005 могут идти параллельно с учётом локальных зависимостей. Шаг 10 проверяет реально реализованный toolkit; дополнительные прогоны для 010 и 013 выполняются после появления этих функций. Существующие задачи 001–003 сохраняются; 011 учитывает release engineering из 003.

## Общие правила приёмки

- Все новые runtime components, CLI/API, extractor, checker и evaluation harness — Go. Markdown, YAML, JSON Schema и встроенные browser assets — данные, а не Python/Node/uv runtime.
- Spec-First и Contract-First: новые команды, flags и DTO в задачах пока предложены, они не считаются существующими API. Сначала совместимый контракт, затем код, docs и AAA tests.
- BYOT сохраняется: не вводить обязательный registry типов или LLM provider. Tooling policies не становятся base conformance. Trust, status, staleness и actual verification остаются отдельными.
- Не обходить lossless, limits/cancellation, preview/apply, CAS и store guarantees. Bundle content, включая computation и инструкции в body, инертен без отдельно доверенного runtime.
- Foreign materials фиксируются commit/digest и проверяются offline; чужой Python checker не является нормативным oracle или нашей CI зависимостью.
- Приёмка каждой задачи подтверждается evidence из её собственного раздела «Приёмка» и строки таблицы; зелёные tests без проверки заявленного поведения недостаточны. Отдельные поведенческие и performance задачи требуют выполненных измерений, а не только harness.

## Независимая проверка плана

2026-09-26 два субагента независимо прочитали текущий README и критерии всех задач 004–015. Первый сверил полноту каждой строки таблицы с разделами «Приёмка» и зависимостями; второй проверил корректность, текст SPEC §8/§11, сохранённое распределение diagnostics 312/18 и pins спецификаций. После исправления замечаний оба повторно подтвердили все 12 наборов критериев и порядок без оставшихся ошибок. Это проверка **плана и критериев**; выполнение самих задач и их будущая приёмка ещё не подтверждены.

## Доступность файлов

На этой машине `.cursor/` исключён глобальным `/Users/skosovsky/.gitignore_global`. Задачи доступны локально, но не попадут в обычный `git status` до явного добавления в Git или изменения ignore policy.
