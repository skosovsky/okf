---
layout: default
lang: ru
title: "Набор OKF, подготовленный вручную → независимый читатель — 2026-09-26"
permalink: /ru/readings/benchmark/agent/runs/20260926-manual-writer-consumer-v3/README/
documentation_id: benchmark-agent-runs-20260926-manual-writer-consumer-v3-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav_ru.html %}


> Архивный отчёт от 26 сентября 2026 года. Эта версия для чтения сохраняет результаты и ограничения исходного запуска; она не является актуальным руководством по продукту. [Исходный отчёт](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-manual-writer-consumer-v3/README.md), проверенная ревизия `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="manually-prepared-okf-bundle--independent-consumer--2026-09-26"></a>

# Набор OKF, подготовленный вручную → независимый читатель — 2026-09-26
{: #section-1}

Зафиксированный [протокол]({{ '/ru/readings/benchmark/agent/WRITER-CONSUMER-BASELINE/' | relative_url }}) выполнен на чистом коммите `dd8989ba36b8e27ac5762e2e0984cac7f88e8738` с моделью `gpt-6-luna` и низким уровнем рассуждений. Единственный синтетический случай смены режима очереди в [`writer_consumer_baseline.json`](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/writer_consumer_baseline.json) (SHA-256 `3fd56d975fa6570df6db81a939eea72d51178034a559882a52b1bb753baf522a`) сравнивает исходные записи с набором OKF, **подготовленным человеком** и содержащим тот же ответ и идентификатор свидетельства. Go-тест проверки этого набора без вызова модели прошёл успешно. Каждую группу получил отдельный новый запуск читателя; AI-автор, восстановление знаний средствами Go и проверка поддержания актуальности не участвовали. Адаптер со скрытыми обозначениями групп (SHA-256 `8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9`) передавал модели вопрос и артефакты группы без служебных меток среды оценки или эталонного ответа.

Сохранённые [план](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-manual-writer-consumer-v3/plan.json), [исходные записи](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-manual-writer-consumer-v3/rows.jsonl) и [отчёт](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-manual-writer-consumer-v3/report.json) байт в байт совпадают с исходными файлами запуска в `/private/tmp`. Повторный анализ воспроизводит отчёт байт в байт:

```sh
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go run \
  ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/writer_consumer_baseline.json \
  -plan benchmark/agent/runs/20260926-manual-writer-consumer-v3/plan.json \
  -rows benchmark/agent/runs/20260926-manual-writer-consumer-v3/rows.jsonl
```

Оба запланированных вызова завершились, отчёт содержит `valid=true`: контрольная группа дала **1/1** правильный ответ, экспериментальная — **1/1**. Оба читателя ответили ровно `B` и сослались на `decision`. Не было устаревших, неправильных, не поддающихся оценке ответов или отказов, операционных сбоев или вызовов инструментов моделью. Следовательно, этот случай показывает возможность ответа читателя по вручную подготовленному набору, при **отсутствии измеренной разницы качества** относительно контрольной группы с исходными записями.

Наблюдаемое потребление за два вызова — **30,291 входной + 52 выходных = 30,343 токена**. Из входных 27,136 отмечены как кэшированные и уже включены в потребление входных токенов. Суммарное время модели — 11.326 секунды. Стоимость в USD недоступна, поэтому нулевой `cost_usd` при нулевом `cost_known_trials` в отчёте не означает бесплатных вызовов.

Этот синтетический базовый эксперимент с одним случаем и одним повтором проверяет чтение независимым читателем корректного набора, подготовленного человеком. Он не измеряет качество AI-автора, восстановление из задачи 010, проверку из задачи 013 или общую пользу OKF. Более ранние черновики протоколов v1/v2 не содержали наблюдений модели и не входят в эти знаменатели.
