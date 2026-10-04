---
layout: default
lang: ru
permalink: /ru/readings/benchmark/agent/BACKFILL-CONSUMER/
documentation_id: benchmark-agent-backfill-consumer
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
documentation_status: historical
historical: true
---
{% include nav_ru.html %}

> Историческая редакция · 2026-10-04 · Сверено с ревизией `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. [Английский оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/BACKFILL-CONSUMER.md). Архивный протокол сохранён как историческое свидетельство; публикация не запускает его команды.

<a id="preregistered-backfill--independent-consumer-pilot"></a>

# Предзарегистрированный пилот: backfill → независимый потребитель
{: #section-1}

Это предлагаемый семантический тест из шести вызовов для задачи 010, отдельный от основного сравнения 012. **Здесь не зафиксированы ни запуск модели, ни результат оценки качества.** Запускайте v2 из чистого рабочего дерева закреплённого Go-коммита `dd8989ba36b8e27ac5762e2e0984cac7f88e8738`, после отдельного разрешения на внешние вызовы Codex. Протокол v1 был заменён до первого вызова: его адаптер передавал модели полный JSON запроса, включая ID случая и группу эксперимента. Закреплённый адаптер проецирует только `question`, `artifacts` и `instructions`; метки случая и группы остаются в экспериментальной программе для анализа. SHA-256 исходного корпуса: `4c23a1ce55f666c737dde8eea0baf44ba82d5e0365585b98a699a72a2a03f85f`.

<a id="provenance-and-comparison"></a>

## Происхождение данных и сравнение
{: #section-2}

[`cmd/prepare-backfill` — оригинал](https://github.com/skosovsky/okf/tree/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/cmd/prepare-backfill) читает замороженный манифест событий Git и проверенный анализ, применяет черновик через реальный Go-путь `backfill.Prepare` / `backfill.Apply` → мутация/хранилище и записывает корпус для потребителя. В `TestBackfillWriterPublishesConsumerCorpus` закоммиченный корпус сравнивается как разобранные данные случаев со свежей реконструкцией; три включённых события и одно обрезанное событие учитываются отдельно в [`corpus/backfill_coverage.json` — оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/backfill_coverage.json) ([оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/backfill_coverage.json)). Предварительная проверка ниже независимо пересоздаёт оба файла и проверяет их байты через `cmp`. Потребитель получает только полученные замороженные артефакты, в новой временной сессии Codex для каждого испытания; он не участвует в записи или проверке.

Три фиксированных вопроса проверяют, учитывает ли потребитель изменение решения A → B, воздерживается ли от утверждения о завершении при обрезанной разнице и читает ли сохранённую текущую разницу кода Go. В `backfill-current-code` артефакт `current-code` имеет одинаковые байты в обеих группах. Контрольная группа получает исходные свидетельства Git; экспериментальная — восстановленный пакет черновика и та же разница текущего кода для этого вопроса. Используйте `-toolkit-mode none`: это проверка чтения записанного пакета, а не проекций CLI или самостоятельного выбора инструментов. Путь Go-модуля записи/хранилища определяет происхождение воздействия; это не утверждение, что потребитель вызывал Go CLI. Исходные материалы — свидетельства, никогда не инструкции.

Один повтор даёт **3 случая × 2 группы = 6 ожидаемых строк**. Управляющая программа располагает контрольную группу перед экспериментальной внутри каждого случая; этот единственный пилот не позволяет оценить эффект порядка или обобщить результат на реальные репозитории. Обе группы используют одинаковые вопрос, модель, инструкции, адаптер, среду исполнения, настройку рассуждений и политику инструментов. Ответ доступен обеим группам. Предзарегистрированный точный оценщик v1 требует кратчайший ответ и исходный ожидаемый ID свидетельства: `B` с `decision` для изменения решения, `Insufficient evidence` с `handler` для обрезанной разницы и `B` с `current-code` для текущего состояния Go. `A` устарело в первом и третьем случаях; `Yes` устарело в случае с обрезанной разницей. Правильное значение без ожидаемой ссылки получает `ungradable`. Отказ, неверный ответ, устаревший ответ, невозможность оценки и операционный сбой учитываются отдельно. Не добавляйте семантические перефразирования после просмотра ответов; любые допустимые варианты необходимо зафиксировать в новом корпусе и протоколе до следующего запуска. Покрытие учитывается отдельно от этих результатов потребителя.

<a id="preflight-and-bounded-run"></a>

## Предварительная проверка и ограниченный запуск
{: #section-3}

Запускайте из корня репозитория в чистом закреплённом рабочем дереве. Используйте новые, эксклюзивные пути вывода v2 и сохраняйте каждую исходную строку. Управляющая программа отклоняет грязное рабочее дерево или несовпадающий `-commit`. До любого вызова проверьте версию/SHA встроенной среды Codex; записанная версия модели — закреплённый ID модели, а снимок серверной части независимо проверить невозможно. Этот план передаёт OpenAI Codex исходные фрагменты Git и восстановленные документы OKF для трёх случаев; сначала получите явное разрешение на этот отдельный запуск из шести вызовов.

```sh
set -e
set -o pipefail
test -z "$(git status --porcelain --untracked-files=all)"
test "$(git rev-parse HEAD)" = dd8989ba36b8e27ac5762e2e0984cac7f88e8738
test "$(shasum -a 256 benchmark/agent/corpus/backfill_cases.json | cut -d ' ' -f 1)" = \
  4c23a1ce55f666c737dde8eea0baf44ba82d5e0365585b98a699a72a2a03f85f
test "$(/Applications/ChatGPT.app/Contents/Resources/codex --version 2>/dev/null)" = \
  'codex-cli 0.155.0-alpha.16.3'
test "$(shasum -a 256 /Applications/ChatGPT.app/Contents/Resources/codex | cut -d ' ' -f 1)" = \
  c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a

GOCACHE=/private/tmp/okf-agent-gocache go test ./benchmark/agent/... \
  -run '^TestBackfillWriterPublishesConsumerCorpus$'
OKF_BACKFILL_CHECK_DIR=$(mktemp -d /private/tmp/okf-backfill-consumer-check.XXXXXX)
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/cmd/prepare-backfill \
  -manifest backfill/testdata/reversal-events.json \
  -analysis backfill/testdata/reversal-analysis.json \
  -corpus "$OKF_BACKFILL_CHECK_DIR/corpus.json" \
  -coverage "$OKF_BACKFILL_CHECK_DIR/coverage.json"
cmp benchmark/agent/corpus/backfill_cases.json \
  "$OKF_BACKFILL_CHECK_DIR/corpus.json"
cmp benchmark/agent/corpus/backfill_coverage.json \
  "$OKF_BACKFILL_CHECK_DIR/coverage.json"
GOCACHE=/private/tmp/okf-agent-gocache go test \
  ./benchmark/agent/cmd/codex-adapter -run '^TestModelVisiblePromptOmitsHarnessLabels$'
GOCACHE=/private/tmp/okf-agent-gocache go build -buildvcs=false \
  -o /private/tmp/okf-eval-codex-adapter-dd8989b \
  ./benchmark/agent/cmd/codex-adapter
test "$(shasum -a 256 /private/tmp/okf-eval-codex-adapter-dd8989b | cut -d ' ' -f 1)" = \
  8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9
test ! -e /private/tmp/okf-backfill-consumer-v2-plan.json
test ! -e /private/tmp/okf-backfill-consumer-v2-rows.jsonl
test ! -e /private/tmp/okf-backfill-consumer-v2-report.json

OKF_BACKFILL_COMMIT=$(git rev-parse HEAD)
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/backfill_cases.json \
  -plan /private/tmp/okf-backfill-consumer-v2-plan.json \
  -rows /private/tmp/okf-backfill-consumer-v2-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter-dd8989b \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id backfill-consumer-20260926-v2 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit "$OKF_BACKFILL_COMMIT" \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 6 -max-seconds 360 -max-tokens 150000 -unpriced \
  -toolkit-mode none \
  -model-tool-access 'Codex read-only ephemeral temp; projected question/artifacts/instructions only; no CLI projections'
```

Жёсткие пределы: шесть вызовов адаптера, 45 секунд на испытание и 360-секундный контекст управляющей программы. Сам адаптер использует 40-секундный таймаут модели. Порог 150,000 входных и выходных токенов проверяется **после** каждого вызова и может быть превышен этим вызовом. `-unpriced` фиксирует недоступную стоимость в USD; это не означает нулевую стоимость и не устанавливает денежный предел. Остановитесь при превышении наблюдаемого бюджета или крайнего срока контекста и сохраните неполные строки. План и строки создаются эксклюзивно; никогда не используйте эти пути повторно после попытки запуска.

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/backfill_cases.json \
  -plan /private/tmp/okf-backfill-consumer-v2-plan.json \
  -rows /private/tmp/okf-backfill-consumer-v2-rows.jsonl \
  > /private/tmp/okf-backfill-consumer-v2-report.json
```

Опубликуйте план, исходные строки, отчёт анализатора, хеши корпуса/адаптера/среды исполнения, расход ресурсов и недоступную цену, а также ручную проверку неожиданных ссылок. Учитывайте все шесть строк в знаменателе завершённости, даже если запуск недействителен; показывайте фактические ошибки и отказы отдельно от покрытия событий. Полный запуск с `valid=true` доказывает лишь, что протокол собрал строки, пригодные для анализа. Не утверждайте, что качество улучшилось на основании этого небольшого синтетического примера, даже если все ответы экспериментальной группы верны.
