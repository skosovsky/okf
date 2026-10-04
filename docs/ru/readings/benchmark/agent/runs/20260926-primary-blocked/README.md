---
layout: default
lang: ru
title: "Основной пилот на реалистичных данных: заблокирован до выполнения"
permalink: /ru/readings/benchmark/agent/runs/20260926-primary-blocked/README/
documentation_id: benchmark-agent-runs-20260926-primary-blocked-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav_ru.html %}


> Архивный отчёт от 26 сентября 2026 года. Эта версия для чтения сохраняет результаты и ограничения исходного запуска; она не является актуальным руководством по продукту. [Исходный отчёт](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-blocked/README.md), проверенная ревизия `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="primary-realistic-pilot-blocked-before-execution"></a>

# Основной пилот на реалистичных данных: заблокирован до выполнения
{: #section-1}

- Время попытки: 2026-09-26T10:10:03Z (UTC).
- Чистая рабочая копия: `f6ebfebac965e681fa43365d5075007767984e05`.
- Набор случаев: `benchmark/agent/corpus/primary_realistic.json` (четыре парных случая, восемь предполагаемых вызовов модели).
- Модель: `gpt-6-luna`; метка версии: `gpt-6-luna`; настройки: `{"reasoning_effort":"low"}`.
- Инструментарий: прямой доступ, временная оболочка Codex только для чтения с предоставленным Go CLI.
- Ограничения: восемь испытаний, всего 360 секунд, 45 секунд на испытание, 12,000 наблюдаемых токенов, цена не определена.
- SHA-256 Go CLI: `96c93890c25cba6e9d9ce75779eb4f9f52922f92598b6a55d29a63fcfd955e17`.
- SHA-256 адаптера: `c405f6a976599db2fd7042660aab822074f64ea975f770e1a41a8f6267dc9f09`.
- Проверки без вызова модели: `go test ./benchmark/agent/... -count=1` прошли.

Запрошенный запуск с повышенными разрешениями отклонён автоматической проверкой разрешений до начала процесса. В решении указано, что пилот передаст внешней модели наборы оценки и исходные артефакты из репозитория, а также может записать состояние Codex вне рабочего каталога. Хотя пользователь разрешил ограниченный пилот, это разрешение не охватывало явно передачу этих чувствительных данных внешнему адресату. Проверка прямо запретила обход отказа с помощью обходных решений или косвенного выполнения.

Не созданы план, исходные записи, ответы модели, измерения потребления или трассировки CLI. Число вызовов модели в этой попытке — ноль. У пилота нет результата оценки, а неудачный предварительный пробный запуск нельзя считать таким результатом. Будущий запуск требует разрешения, явно охватывающего передачу этих исходных артефактов и наборов оценки из репозитория внешней модели Codex, а также запись состояния Codex вне рабочего каталога.

Предполагавшаяся команда (не выполнялась):

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
