---
layout: default
lang: ru
permalink: /ru/readings/benchmark/agent/PILOT/
documentation_id: benchmark-agent-pilot
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
documentation_status: historical
historical: true
title: "Исторический протокол основного пилотного запуска"
---
{% include nav_ru.html %}

> Историческое издание для чтения. Сверено 2026-10-04 с ревизией `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. Протокол и результаты ниже сохраняют исходный исторический статус; это не новый запуск и не разрешение на выполнение. [Английский оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/PILOT.md).

<a id="historical-primary-pilot-protocol"></a>

# Исторический протокол основного пилотного запуска
{: #section-1}

Предыдущий запуск с восемью попытками сохранён в [`runs/20260926-primary-live/` — оригинал](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-live). Все попытки завершились до вычисления ответа модели: `PATH` выбрал отдельный `codex-cli 0.137.0`, который не умеет читать уровень рассуждений `max` из актуального каталога моделей. Следующий запуск явно выбрал встроенный `/Applications/ChatGPT.app/Contents/Resources/codex`. Наблюдаемая версия — `codex-cli 0.155.0-alpha.16.3`, SHA-256 — `c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`. Программа запуска и адаптер отклоняют другую версию или двоичный файл. Этот запуск остановился после одного контрольного испытания, превысившего наблюдаемый бюджет токенов; план, строка и отчёт находятся в [`runs/20260926-primary-bundled-v2/` — оригинал](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-bundled-v2). Позднее диагностика чтения файлов с одним случаем остановилась после контрольной группы из-за несоответствия анализатора оболочки; см. [`runs/20260926-diagnostic-read-v1/` — оригинал](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-diagnostic-read-v1). Следующее предложение диагностики находится в [`PILOT-DIAGNOSTIC.md`]({{ '/ru/readings/benchmark/agent/PILOT-DIAGNOSTIC/' | relative_url }}). Ни один из частичных запусков не позволяет сравнить группы.

Для нового выполнения исправленный код 004/005, SPEC, набор и адаптер должны быть в одном чистом коммите. `-commit` должен совпадать с `git rev-parse HEAD`, а `-spec` — с полным внешним коммитом в [`skills/open-knowledge-format/SKILL.md`]({{ '/ru/readings/skills/open-knowledge-format/SKILL/' | relative_url }}); предварительная проверка сверяет это объявление с фактическим SHA-256 SPEC. Соберите оба Go-файла из чистого рабочего дерева:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go build -o /private/tmp/okf-eval-cli ./cmd/okf
GOCACHE=/private/tmp/okf-agent-gocache go build -o /private/tmp/okf-eval-codex-adapter ./benchmark/agent/cmd/codex-adapter
```

Выполненный запуск имел идентификатор `primary-bundled-20260926-v2`. Протокол ниже фиксирует идентификатор модели, низкую интенсивность рассуждений, восемь вызовов, 360 секунд суммарно, 45 секунд на вызов и 12,000 наблюдаемых входных/выходных токенов. Порог токенов проверяется после вызова, поэтому один вызов может его превысить: это условие остановки по наблюдению, а не строгий предел расходов. Денежный порог адаптера с известной ценой также может быть превышен одним вызовом. `-unpriced` означает, что аккаунт не предоставляет цену отдельного вызова, и **не** означает отсутствие потребления ресурсов. Программа записывает SHA-256 двоичных файлов, хеши набора и инструкций, время, режим доступа к инструментам, результаты, сбои и использование ресурсов.

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/primary_realistic.json \
  -plan /private/tmp/okf-primary-bundled-v2-plan.json \
  -rows /private/tmp/okf-primary-bundled-v2-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -go-cli /private/tmp/okf-eval-cli \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id primary-bundled-20260926-v2 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit d6bd02824b089a4e9c2e1080a8ef608ba9e67276 \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 8 -max-seconds 360 -max-tokens 12000 -unpriced \
  -toolkit-mode direct \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```

Команда выше — историческая запись, а не разрешение повторить уже исчерпанное испытание. При предварительной проверке программа хешировала оба Go-файла. Ревизия SPEC — закреплённый в репозитории профиль временных моментов. Анализируйте неизменные план и необработанные строки:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/primary_realistic.json \
  -plan benchmark/agent/runs/20260926-primary-bundled-v2/plan.json \
  -rows benchmark/agent/runs/20260926-primary-bundled-v2/rows.jsonl
```

`valid` требует всех восьми испытаний, отсутствия операционных сбоев, закреплённых метаданных, соблюдения бюджета и наблюдаемого вызова Go CLI моделью в каждом экспериментальном испытании. Адаптер считает вызов CLI только по завершённому событию команды с нулевым кодом и точным прямым запуском закреплённого Go-файла: проверкой материализованного набора в JSON или разбором предоставленной заметки в JSON. Справка, версия, посторонние пути и изменение формата событий отклоняются. Перед публикацией отчёта оценщик должен проверить необработанные трассы инструментов и фактические ответы. При четырёх случаях результат описателен и не подтверждает универсальный размер эффекта.

Последующие запуски используют отдельные планы и строки: [`corpus/metadata_cases.json` — оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/metadata_cases.json) с `-toolkit-mode none` (8 испытаний), [`corpus/backfill_cases.json` — оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/backfill_cases.json) в прямом режиме (6 испытаний) и пару автора/потребителя 013 с включённой и выключенной проверкой. Сравнению 004/005 до и после дополнительно нужны чистое рабочее дерево старого коммита и собственная привязка SPEC; сбои инструментов исходной версии учитываются как операционные. Адаптер Codex требует одинаковые явные привязки `-model-runtime`, версии и SHA-256 во всех режимах, включая `none`; программа с интерфейсом Bring Your Own Types допускает другие адаптеры без локальной среды исполнения в непрямых режимах. Успех первого пилота сам по себе не предполагает последующих запусков.
