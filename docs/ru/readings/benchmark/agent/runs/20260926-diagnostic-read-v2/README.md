---
layout: default
lang: ru
title: "Диагностика чтения файлов v2: контрольная группа не вызвала оболочку"
permalink: /ru/readings/benchmark/agent/runs/20260926-diagnostic-read-v2/README/
documentation_id: benchmark-agent-runs-20260926-diagnostic-read-v2-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav_ru.html %}


> Архивный отчёт от 26 сентября 2026 года. Эта версия для чтения сохраняет результаты и ограничения исходного запуска; она не является актуальным руководством по продукту. [Исходный отчёт](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-diagnostic-read-v2/README.md), проверенная ревизия `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="file-read-diagnostic-v2-control-made-no-shell-call"></a>

# Диагностика чтения файлов v2: контрольная группа не вызвала оболочку
{: #section-1}

Отдельно разрешённый запуск `diagnostic-read-20260926-v2` выполнен на чистом HEAD `29604a133ba6656de7c75b738c2ca221a469bdf4` с набором из одного случая `15a2a6b0646acdbd18114b54f49f971fee512ad8dfec04aad9d34641820b0d95`. Встроенная среда Codex — `codex-cli 0.155.0-alpha.16.3`, SHA-256 `c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`. SHA-256 бинарного файла Go CLI — `e3b9f1c344f5a169d08332e14b96306b54ab7d61820afafe9ef784ffa5f6e525`; адаптера — `954f5e31d3c014b883bae4bfbe43c7214c38c818c68fecd3094e05f5bbd0fe34`.

Выполнен один вызов модели в контрольной группе; экспериментальная группа не запускалась. Полные сохранённые метаданные событий состоят из `thread.started`, `turn.started`, одного завершённого `agent_message` и `turn.completed`. Событий команд или MCP-инструментов было **ноль**, поэтому проблема не в разборе команд оболочки. Наблюдаемых чтений обоих файлов Codex не выполнил, несмотря на общую инструкцию `cat`. Модель ответила `Insufficient evidence` («недостаточно данных») и сослалась на `current`; зафиксированный фактический ответ — `0.2`, подтверждённый артефактом `current`. Поскольку чтение файлов не наблюдалось, среда запуска отметила контрольную запись как операционный сбой и остановилась до экспериментальной группы. Анализатор сообщает `valid=false`: один операционный сбой и одна отсутствующая запись экспериментальной группы. Сравнения качества между группами нет, Go CLI не вызывался.

Предыдущая контрольная группа v1 создала два успешных события оболочки через `/bin/zsh`. Эти два запуска показывают, что одной инструкции в запросе недостаточно для надёжного использования оболочки; они не устанавливают, почему модель выбрала другое поведение. Обработчик v2 поддерживает безопасные обёртки zsh, но здесь не было события команды для разбора.

Наблюдаемое потребление — 14,997 входных и 28 выходных токенов (15,025 всего), без кэшированных токенов и цены отдельного вызова в USD. Ограничения в 2 вызова и 120 секунд, а также мягкий порог 150,000 наблюдаемых токенов соблюдены. Стоимость остаётся неизвестной; ограничения в USD нет. После неподтверждённого чтения контрольной группой второго вызова не было. `plan.json`, `rows.jsonl` и `report.json` сохраняют зарегистрированный план, исходную запись, очищенные метаданные событий и вывод анализатора.

Точная команда:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/diagnostic_primary.json \
  -plan /private/tmp/okf-diagnostic-read-v2-plan.json \
  -rows /private/tmp/okf-diagnostic-read-v2-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -go-cli /private/tmp/okf-eval-cli \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id diagnostic-read-20260926-v2 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit 29604a133ba6656de7c75b738c2ca221a469bdf4 \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 2 -max-seconds 120 -max-tokens 150000 -unpriced \
  -toolkit-mode direct -diagnostic-read \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```
