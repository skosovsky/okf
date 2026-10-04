---
layout: default
lang: ru
title: "Диагностика чтения файлов: контрольная группа остановлена из-за несовпадения разбора команд"
permalink: /ru/readings/benchmark/agent/runs/20260926-diagnostic-read-v1/README/
documentation_id: benchmark-agent-runs-20260926-diagnostic-read-v1-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav_ru.html %}


> Архивный отчёт от 26 сентября 2026 года. Эта версия для чтения сохраняет результаты и ограничения исходного запуска; она не является актуальным руководством по продукту. [Исходный отчёт](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-diagnostic-read-v1/README.md), проверенная ревизия `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="file-read-diagnostic-control-stopped-on-parser-mismatch"></a>

# Диагностика чтения файлов: контрольная группа остановлена из-за несовпадения разбора команд
{: #section-1}

Отдельно разрешённый запуск `diagnostic-read-20260926-v1` выполнен на чистом HEAD `2af06eb420e2fc5b74c39e95957822233761a904` с зафиксированным набором из одного случая (`15a2a6b0646acdbd18114b54f49f971fee512ad8dfec04aad9d34641820b0d95`). Встроенная среда Codex зафиксирована как `codex-cli 0.155.0-alpha.16.3`, SHA-256 `c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`. SHA-256 бинарного файла Go CLI — `67dd408da3a634fd0560b1a1c97f0c18d0cefa88e7c023afd902910bfd6b83cb`; адаптера — `ecb39f0fa8634bd1b2edfb67f149828557d6443ce710a4408029c19db848f045`. Сохранённое поле `metadata.prompt_sha256` охватывает только общую инструкцию запроса, а не весь подготовленный адаптером запрос или добавленный Codex контекст. Поэтому оно совпадает с предыдущим основным запуском, несмотря на новую инструкцию `cat`. Новые планы явно обозначают этот охват как `shared_request_instructions_only`.

Выполнен один вызов модели в контрольной группе; экспериментальная группа не запускалась. Контрольный ответ — `0.2` со ссылкой на `current`, что совпадает с зафиксированным фактическим ответом. Метаданные событий содержат две пары начала/завершения `command_execution`, обе завершились с кодом выхода ноль. Обе классифицированы как `other`, поэтому строгая проверка подтверждения чтения не прошла и среда запуска корректно остановилась до экспериментальной группы. Сохранённые SHA-256 команд точно совпадают со следующими безопасными локальными вариантами:

| SHA-256 команды события | Соответствующая команда оболочки |
| --- | --- |
| `d5aad6657cc20cb7ebbe0cee41e46748b79e42f0832638609260c1f9d10f970f` | `/bin/zsh -lc 'cat docs/legacy-support.md'` |
| `3472d33018eb9d0e6e3c660371a560de8199d827ed5ffee0f42c23d5f2805e3e` | `/bin/zsh -lc 'cat bundle/doc.go'` |

Это выявляет несовпадение разбора команд: диагностический распознаватель поддерживает безопасно заключённые в кавычки обёртки bash/sh, но Codex использовал `/bin/zsh`. Метаданные не сохраняют вывод команд, поэтому точную передачу содержимого нельзя подтвердить задним числом. Фактический ответ — одно наблюдение, а не сравнение качества контрольной и экспериментальной групп. Зафиксированный анализатор корректно сообщает `valid=false`: один операционный сбой и одно отсутствующее испытание экспериментальной группы.

Изменение обработчика после этого запуска добавляет узко ограниченную поддержку обёртки `/bin/zsh` и требует точного совпадения полного вывода `cat` с предоставленным содержимым артефакта. Оно не подтверждает старую запись задним числом: этот вывод не был сохранён. Следующему запуску нужны собственный план и разрешение.

Наблюдаемое потребление: 45,362 входных токена, из которых 28,160 отмечены как кэшированные, и 102 выходных. Всего 45,464 входных и выходных токена — ниже мягкого порога остановки 60,000 наблюдаемых токенов. Адаптер не сообщает цену вызова в USD, поэтому стоимость остаётся неизвестной. Ограничения в 2 вызова и 120 секунд соблюдены. После неподтверждённого чтения контрольной группой второго вызова не было. Здесь сохранены исходные план, запись, отчёт и метаданные событий с удалёнными чувствительными данными; в трассировке событий нет исходного текста файлов или вывода оболочки.

Точная команда:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/diagnostic_primary.json \
  -plan /private/tmp/okf-diagnostic-read-v1-plan.json \
  -rows /private/tmp/okf-diagnostic-read-v1-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -go-cli /private/tmp/okf-eval-cli \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id diagnostic-read-20260926-v1 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit 2af06eb420e2fc5b74c39e95957822233761a904 \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 2 -max-seconds 120 -max-tokens 60000 -unpriced \
  -toolkit-mode direct -diagnostic-read \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```
