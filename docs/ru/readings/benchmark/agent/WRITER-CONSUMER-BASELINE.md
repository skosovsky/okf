---
layout: default
lang: ru
permalink: /ru/readings/benchmark/agent/WRITER-CONSUMER-BASELINE/
documentation_id: benchmark-agent-writer-consumer-baseline
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
documentation_status: historical
historical: true
title: "Задача 012: вручную подготовленная исходная версия для независимого потребителя"
---
{% include nav_ru.html %}

> Историческое издание для чтения. Сверено 2026-10-04 с ревизией `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. Протокол и результаты ниже сохраняют исходный исторический статус; это не новый запуск и не разрешение на выполнение. [Английский оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/WRITER-CONSUMER-BASELINE.md).

<a id="task-012-manually-prepared-writer--independent-consumer-baseline"></a>

# Задача 012: вручную подготовленная исходная версия для независимого потребителя
{: #section-1}

Это зафиксированный семантический пилот с двумя вызовами, **не результат**. Ранние черновики v1/v2 не запускались. Человек-автор подготовил итоговый набор OKF в [`corpus/writer_consumer_baseline.json` — оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/writer_consumer_baseline.json); он не использует автора backfill задачи 010 или проверку поддержки знаний задачи 013. Потребитель получит набор в новом обращении к модели. И контроль с исходными записями, и экспериментальный набор сообщают, что старый режим очереди A заменён на B. Контроль сохраняет тот же ответ и идентификатор доказательства, поэтому преимущество экспериментальной группы нельзя объяснить отсутствием правильного ответа в контроле.

SHA-256 набора — `3fd56d975fa6570df6db81a939eea72d51178034a559882a52b1bb753baf522a`. Единственный случай синтетический (`mechanism_only`). Кратчайший правильный ответ — `B` со ссылкой на материал `decision`; `A` устарел. Оценщик exact-v1 считает правильное значение без этой ссылки не поддающимся оценке. Проверка CLI — условие публикации, а не доказательство правильного ответа потребителя. Автор — человек, его работа зафиксирована: запуск измеряет независимого потребителя, а не качество написания AI или влияние 010/013.

<a id="frozen-protocol"></a>

## Зафиксированный протокол
{: #section-2}

Используйте чистое рабочее дерево Go-коммита `dd8989ba36b8e27ac5762e2e0984cac7f88e8738`, сохраните полный коммит и ревизию SPEC `0b87c52c6ef999286c745e19998fdfcd03d5dbee` в плане. Перед обращением к модели выполните включённый автономный Go-тест. Используйте встроенный `codex-cli 0.155.0-alpha.16.3` с SHA-256 `c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`, модель `gpt-6-luna` и интенсивность рассуждений `low`. Серверную версию модели независимо проверить нельзя. Адаптер исходной версии `/private/tmp/okf-eval-codex-adapter-dd8989b` имеет SHA-256 `8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9`. Он собран с `-buildvcs=false` из этого кода; точный коммит исходников программа записывает отдельно.

Если двоичный файл недоступен или отличается, до обращения к модели зафиксируйте и зарегистрируйте новый хеш адаптера и идентификатор запуска. Адаптер создаёт по одному новому временному вызову Codex с доступом только для чтения на группу. Потребитель получает только проекцию запроса (`question`, `artifacts`, общие инструкции ответа), без служебных `case_id`/`arm`, эталонного `expected`, истории автора или другой группы. Программа сначала запускает контроль, затем экспериментальную группу. Пилот с одним повторением не оценивает влияние порядка или разброс.

Идентификатор запуска — `manual-writer-consumer-20260926-v3`. Ожидаемые строки и обращения к модели: **один случай × две группы × одно повторение = два**. Жёсткие пределы: два вызова, 120 секунд контекста программы запуска, 40 секунд на вызов модели адаптером. Наблюдаемый порог входных и выходных токенов — 50,000; он проверяется после вызова, поэтому последний вызов может его превысить. Стоимость в USD неизвестна; `-unpriced` не задаёт долларовый предел. В OpenAI Codex передаются две выдержки исходных записей и три Markdown-файла OKF, подготовленных человеком. Перед передачей и запуском получите отдельное конкретное разрешение пользователя. Обычные Go-тесты и этот документ протокола не включают вызовов модели.

Из корня чистого репозитория используйте новые пути вывода в `/private/tmp`:

```sh
set -e
set -o pipefail
test -z "$(git status --porcelain --untracked-files=all)"
test "$(git rev-parse HEAD)" = dd8989ba36b8e27ac5762e2e0984cac7f88e8738
test "$(shasum -a 256 benchmark/agent/corpus/writer_consumer_baseline.json | cut -d ' ' -f 1)" = \
  3fd56d975fa6570df6db81a939eea72d51178034a559882a52b1bb753baf522a
test "$(/Applications/ChatGPT.app/Contents/Resources/codex --version 2>/dev/null)" = \
  'codex-cli 0.155.0-alpha.16.3'
test "$(shasum -a 256 /Applications/ChatGPT.app/Contents/Resources/codex | cut -d ' ' -f 1)" = \
  c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
  go build -buildvcs=false -o /private/tmp/okf-eval-codex-adapter-dd8989b \
  ./benchmark/agent/cmd/codex-adapter
test "$(shasum -a 256 /private/tmp/okf-eval-codex-adapter-dd8989b | cut -d ' ' -f 1)" = \
  8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9

GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
  go test ./benchmark/agent -run '^TestManualWriterConsumerBaseline$' -count=1

OKF_BASELINE_COMMIT=$(git rev-parse HEAD)
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
  go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/writer_consumer_baseline.json \
  -plan /private/tmp/okf-manual-writer-consumer-v3-plan.json \
  -rows /private/tmp/okf-manual-writer-consumer-v3-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter-dd8989b \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id manual-writer-consumer-20260926-v3 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit "$OKF_BASELINE_COMMIT" \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 2 -max-seconds 120 -max-tokens 50000 -unpriced \
  -toolkit-mode none \
  -model-tool-access 'Codex read-only ephemeral temp; projected question/artifacts/instructions JSON; no CLI projections'

GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
  go run ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/writer_consumer_baseline.json \
  -plan /private/tmp/okf-manual-writer-consumer-v3-plan.json \
  -rows /private/tmp/okf-manual-writer-consumer-v3-rows.jsonl \
  > /private/tmp/okf-manual-writer-consumer-v3-report.json
```

После разрешённого обращения сохраните точные план, необработанные строки и отчёт анализатора в [`runs/` — оригинал](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs). Покажите обе строки при расчёте знаменателей даже при сбое; отдельно сообщите правильность, устаревшие и не поддающиеся оценке ответы, отказы и операционные сбои, использование токенов и неизвестную цену. `valid=true` означает пригодные для анализа строки, а не доказанную пользу продукта. Изменение набора, оценщика, адаптера или протокола требует нового идентификатора запуска до просмотра ответов.
