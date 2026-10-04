---
layout: default
lang: ru
title: "Основной пилот на исходниках репозитория: сбой запуска адаптера"
permalink: /ru/readings/benchmark/agent/runs/20260926-primary-live/README/
documentation_id: benchmark-agent-runs-20260926-primary-live-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav_ru.html %}


> Архивный отчёт от 26 сентября 2026 года. Эта версия для чтения сохраняет результаты и ограничения исходного запуска; она не является актуальным руководством по продукту. [Исходный отчёт](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-live/README.md), проверенная ревизия `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="primary-repo-source-pilot-adapter-startup-failure"></a>

# Основной пилот на исходниках репозитория: сбой запуска адаптера
{: #section-1}

Разрешённый основной пилот из восьми испытаний выполнен на чистом Git HEAD `f6ebfebac965e681fa43365d5075007767984e05` с `benchmark/agent/corpus/primary_realistic.json`. Неизменяемый план, все восемь исходных записей и вывод анализатора сохранены рядом с исходным файлом этого отчёта. Предприняты четыре контрольных и четыре экспериментальных испытания. Все восемь завершились сбоем до ответа модели: `codex-cli 0.137.0` не смог разобрать свой каталог моделей (`unknown variant max`, ожидались `none|minimal|low|medium|high|xhigh`). Каждая исходная запись содержит ошибку адаптера. Ответов и трассировок инструментов модели нет; наблюдаемых вызовов инструментария и потребления токенов нет. По нулевым полям потребления нельзя судить о начислениях.

Анализатор сообщает `valid=false`, восемь операционных сбоев, отсутствие попыток фактического ответа и оценки качества. Суммарная длительность испытаний — 43,828 мс, в пределах общего бюджета 360 секунд. Эти попытки исчерпали зарегистрированное разрешение на восемь вызовов; это не даёт основания для повторного запуска без разрешения. Экспериментальная группа не смогла продемонстрировать прямое использование Go CLI, поскольку Codex завершился сбоем до генерации ответа.

Зафиксированные настройки: `gpt-6-luna`, метка версии модели `gpt-6-luna`, низкий уровень рассуждений, прямое использование инструментария, предел восьми испытаний, общий предел 360 секунд, 45 секунд на испытание, условие остановки на 12,000 наблюдаемых токенов, цена не определена. SHA-256 бинарного файла Go CLI — `96c93890c25cba6e9d9ce75779eb4f9f52922f92598b6a55d29a63fcfd955e17`; адаптера — `c405f6a976599db2fd7042660aab822074f64ea975f770e1a41a8f6267dc9f09`. Редакция SPEC — `0b87c52c6ef999286c745e19998fdfcd03d5dbee`.

Точная команда:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/primary_realistic.json \
  -plan /private/tmp/okf-primary-plan.json \
  -rows /private/tmp/okf-primary-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter \
  -go-cli /private/tmp/okf-eval-cli \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit f6ebfebac965e681fa43365d5075007767984e05 \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 8 -max-seconds 360 -max-tokens 12000 -unpriced \
  -toolkit-mode direct \
  -model-tool-access 'Codex read-only ephemeral temp with shell; Go CLI binary supplied'
```

Предыдущий отказ автоматической проверки, до этого более конкретного разрешения, зафиксирован отдельно в [отчёте о заблокированном запуске]({{ '/ru/readings/benchmark/agent/runs/20260926-primary-blocked/README/' | relative_url }}).
