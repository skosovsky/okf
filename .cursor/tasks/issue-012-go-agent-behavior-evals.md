# 012 — Go harness для оценки качества знаний и поведения агента

Статус: backlog. Приоритет: P2. Зависимости: 004/005 до итогового прогона на исправленной реализации; 010/013 подключаются как отдельные экспериментальные сценарии.

## Зачем

Parser tests доказывают разбор документа, но не то, что агент выберет верное утверждение. Создать компактный evaluation corpus и Go harness для stale/deprecated conflicts, backfill reversals и поддержания документации.

Уточнение пользователя: задача включает адаптацию и **фактический запуск benchmarks под нашу реализацию после исправлений**, а не только описание методики. Performance Go-кода измеряется отдельно в задаче 015; здесь измеряется качество ответов/действий агента при работе с нашим toolkit.

References: [trust results](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/benchmark/trust/RESULTS.md), [gate protocol](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/benchmark/gate/PROTOCOL.md), [map-tier results](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/benchmark/map-tier/RESULTS.md).

## Что именно переносим

Идеи controlled comparisons, committed corpus, исходных trials и прозрачного разбора ошибок. Не переносим чужие численные эффекты как доказательство нашего продукта. Основной trust contrast у авторов invalid по собственному protocol; gate experiment проверяет инструкцию AGENTS.md, а не надёжность shell Stop hook.

Чужой trust corpus убирал другие признаки актуальности и отбирал случаи, где baseline ошибался. Это демонстрирует механизм, но плохо оценивает реальную величину пользы. При этом опубликованные протоколы, сырые прогоны и разбор загрязнения окружения полезны для нашего дизайна. В более позднем map-tier повторе уточнённые инструкции исправили часть ошибок дешёвой модели; нельзя переносить первоначальный отрицательный вывод как неизменный рейтинг моделей.

## Контракт и минимальный scope

- Go DTO + JSON Schema для case, input artifacts, expected factual answer/evidence, run metadata, observations, metrics и result validity.
- Первые 12–20 случаев: changed default/limit, отменённое решение, stale vs current code, prompt injection в body, ложная verification, intent vs execution, truncated diff, недостаточность evidence.
- В реалистичном control arm правильный ответ доступен в коде/документах. Не удалять все recency clues только для победы metadata. Отдельно маркировать mechanism-only synthetic cases.
- Детерминированные fixture/grader tests на Go идут в обычный CI. Live LLM runs только явно запущенные, с budget/concurrency caps; external calls не обязательны для `go test ./...`.
- BYOT runner interface для host/provider adapters, без жёсткой зависимости от Claude и локальной истории чатов. Сохранять model/version/settings, prompt/corpus digests, clock, usage, failures и retries.
- Изолировать system/project instructions/hooks между arms, фиксировать доступные tools и evidence. Dataset/tool text не может изменять harness policy.
- Разделять wrong/fresh/stale/refusal/ungradable/operational failure. Refusal не считать правильным ответом автоматически; threshold/grader менять только с зафиксированной revision.
- Предварительно фиксировать metric, denominator, invalidation rules и минимальный эффект. При малом n давать descriptive result без заявлений о статистической доказанности.

## Приёмка

- [ ] Корпус воспроизводится без сетевых вызовов; положительные и adversarial grader cases оформлены AAA.
- [ ] Go analyzer пересчитывает отчёт из сохранённых rows; отсутствующие/ошибочные trials видимы в denominator accounting.
- [ ] Сравнение metadata/no-metadata не подменяется отсутствием правильного ответа у control.
- [ ] Базовый writer → independent consumer проверяется на вручную подготовленном или изменённом через существующий Go mutation API bundle; реализация 010/013 для этого не требуется. Оценивается ответ consumer, а не только факт изменения документа.
- [ ] Backfill coverage и syntactic validity не выдаются за semantic accuracy.
- [ ] После 004/005 выполнен pilot через реальные Go CLI/MCP adapters, закреплён corrected commit; опубликованы rows, анализ, denominator/failure accounting и ограничения. Без фактического запуска задача не считается выполненной. Если external model недоступна, явно оставить live-run acceptance незавершённым.

## Прогоны на нашей реализации

1. Основное парное сравнение: одинаковые исходные материалы и вопрос; control читает обычные repo docs/code, treatment использует наш OKF bundle через Go MCP/CLI. Правильный ответ доступен обеим сторонам. Для оценки эффекта самих metadata добавить отдельный controlled contrast, не смешивать его с эффектом инструмента.
2. Отдельный before/after исправлений 004/005: один corpus, одинаковый model/settings/prompt, pinned исходный и исправленный Go commit. Зафиксировать clock и ревизию SPEC; случаи изменившейся спецификации пометить отдельно от устранения bug. Если baseline не может прочитать bundle, это tool failure, а не LLM factual error.
3. Метрики: factual/stale error rate, task completion, отказ/неоцениваемый ответ, tool failures, tool calls, input/output/cache tokens, elapsed time и стоимость только при известных ценах. Добавить повторения и оценку разброса; на малом pilot не заявлять универсальный процент выигрыша.
4. Базовый writer→consumer строится из уже существующего Go mutation API или фиксированного вручную подготовленного состояния. Для будущих 010/013 повторить сценарии после их реализации: 010 проверяет реконструкцию/reversal, 013 — влияние upkeep на документацию и ответ consumer. Основной pilot не зависит от ещё несуществующих функций.
5. Зафиксировать Go run/analyze entrypoints, model adapter, corpus hashes, commands и budget до запуска. Обычный `go test ./...` остаётся offline; платные прогоны имеют явный предел расходов и не запускаются автоматически каждой PR.

## Не входит

Копирование Python benchmark stack, автоматические платные runs в PR CI, обещание экономии токенов или выбор самой дешёвой модели по чужой истории.
