---
layout: default
lang: ru
permalink: /ru/readings/benchmark/agent/PILOT-DIAGNOSTIC/
documentation_id: benchmark-agent-pilot-diagnostic
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
documentation_status: historical
historical: true
title: "Предложение: диагностика чтения файлов v2 (требует отдельного разрешения)"
---
{% include nav_ru.html %}

> Историческое издание для чтения. Сверено 2026-10-04 с ревизией `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. Протокол и результаты ниже сохраняют исходный исторический статус; это не новый запуск и не разрешение на выполнение. [Английский оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/PILOT-DIAGNOSTIC.md).

<a id="proposed-file-read-diagnostic-v2-separate-approval-required"></a>

# Предложение: диагностика чтения файлов v2 (требует отдельного разрешения)
{: #section-1}

Запуск v1 сохранён в [`runs/20260926-diagnostic-read-v1/` — оригинал](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-diagnostic-read-v1). Он остановился после одного вызова в контрольной группе: безопасный анализатор команд не распознал оболочку `/bin/zsh -lc`, которую использовал Codex. Теперь анализатор поддерживает именно эту безопасную оболочку и требует полного совпадения вывода. Строка v1 остаётся неподтверждённой, поскольку необработанный вывод не был сохранён. Разрешение на v1 для этого плана уже исчерпано.

Предложение проверяет только первый реалистичный случай `repo-default-okf-version`, с теми же материалами контрольной и экспериментальной групп, что и в [`corpus/primary_realistic.json` — оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/primary_realistic.json). Набор с одним случаем — [`corpus/diagnostic_primary.json` — оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/diagnostic_primary.json), SHA-256 `15a2a6b0646acdbd18114b54f49f971fee512ad8dfec04aad9d34641820b0d95`. SHA-256 исходного набора: `3e00dd9796e507555c184d4c2b68cd37bc53b0d8672c6e1b7e307eecb289544b`. Проверяемое утверждение привязано к [`bundle/doc.go` — оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/bundle/doc.go) (`OKFVersion = "0.2"`), а ревизия SPEC — `0b87c52c6ef999286c745e19998fdfcd03d5dbee`. Полный SHA итогового Go-коммита нужно получить из чистого рабочего дерева после фиксации кода диагностики.

`metadata.prompt_sha256` охватывает только общие `Request.Instructions`; новое поле плана и отчёта `prompt_hash_scope` явно указывает это. Хеш набора и хеш двоичного файла адаптера фиксируют остальные входные данные и код построения запроса, но контекст, добавляемый Codex, отдельно не хешируется.

Обе группы получают одинаковые вопрос, формат ответа, настройки модели, доступ к оболочке только для чтения и явное требование выполнить простой `cat` для каждого предоставленного материала. Экспериментальная группа дополнительно получает инструкцию по Go CLI; это и есть проверяемое воздействие. Адаптер сохраняет не более 64 строк метаданных событий на испытание: тип события, тип и статус элемента, код завершения, категорию команды и SHA-256 команды. В этих метаданных не сохраняются вывод команды, текст модели или исходный материал. Чтение подтверждается только тогда, когда успешный `cat` одного предоставленного файла возвращает точно всё его содержимое. Распознаются безопасно экранированные оболочки bash/sh/zsh; составные команды отклоняются. Если контрольная группа не прочитала все файлы, программа запуска сохраняет строку как операционный сбой и останавливается до экспериментальной группы. Неизвестные форматы событий и команд отклоняются; метаданные должны показывать, появились ли события команд, которые не удалось распознать.

Новый идентификатор запуска — `diagnostic-read-20260926-v2`. Жёсткие пределы: два вызова, 120 секунд суммарно и 45 секунд на вызов. `150,000` — наблюдаемый порог токенов, проверяемый после вызова; один вызов может его превысить. Контрольный вызов v1 использовал 45,464 входных и выходных токена, включая два обращения к оболочке; экспериментальная группа может использовать существенно больше. Цена запуска неизвестна: **лимита в USD нет**, максимальная денежная стоимость не заявляется. Два завершённых испытания дадут диагностику доступа к инструментам, а не статистически значимый результат поведения.

После явного разрешения пересоберите оба двоичных файла из чистого коммита и выполните:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go build -o /private/tmp/okf-eval-cli ./cmd/okf
GOCACHE=/private/tmp/okf-agent-gocache go build -o /private/tmp/okf-eval-codex-adapter ./benchmark/agent/cmd/codex-adapter
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
  -commit FULL_NEW_CLEAN_GIT_SHA \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 2 -max-seconds 120 -max-tokens 150000 -unpriced \
  -toolkit-mode direct -diagnostic-read \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```

Не используйте повторно план, строки или разрешение предыдущего пилотного запуска. Если версия или хеш любого двоичного файла отличаются при предварительной проверке, остановитесь и проверьте новую привязку до обращения к модели. Анализируйте необработанные строки с тем же набором данных и сохранённым планом.
