---
layout: default
lang: ru
title: "Основной пилот со встроенным Codex: остановлен после одного испытания"
permalink: /ru/readings/benchmark/agent/runs/20260926-primary-bundled-v2/README/
documentation_id: benchmark-agent-runs-20260926-primary-bundled-v2-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical_date: 2026-09-26
status: historical
historical: true
---
{% include nav_ru.html %}


> Архивный отчёт от 26 сентября 2026 года. Эта версия для чтения сохраняет результаты и ограничения исходного запуска; она не является актуальным руководством по продукту. [Исходный отчёт](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/runs/20260926-primary-bundled-v2/README.md), проверенная ревизия `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

<a id="primary-bundled-codex-pilot-stopped-after-one-trial"></a>

# Основной пилот со встроенным Codex: остановлен после одного испытания
{: #section-1}

Отдельно разрешённый запуск `primary-bundled-20260926-v2` использовал чистый HEAD `d6bd02824b089a4e9c2e1080a8ef608ba9e67276`, набор `primary_realistic.json` из четырёх случаев, модель `gpt-6-luna` с низким уровнем рассуждений и явно указанную встроенную среду Codex `codex-cli 0.155.0-alpha.16.3` (SHA-256 `c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`). SHA-256 бинарного файла Go CLI — `53bebd9623685a240a056255aec0a4bd8885fb39fe2f3ffd18b80cff7c8198fd`; адаптера — `168d725ad20225f93f1516c811289f5732f0a3647c9aac1a3d2000e6d8cfd17f`.

Первое контрольное испытание завершилось с 14,985 входными и 31 выходным токеном. Один этот вызов превысил зарегистрированный предел 12,000 наблюдаемых токенов; среда запуска остановилась до остальных семи испытаний. Пределы в 8 вызовов и 360 секунд соблюдены. Цена недоступна, поэтому стоимость неизвестна. Условие остановки по токенам проверяется после вызова и является мягким наблюдаемым пределом, как указано в заранее зарегистрированном плане и `PILOT.md`.

На первый вопрос модель ответила `Insufficient evidence` («недостаточно данных»), сославшись на `historical` и `current`. Ожидаемый ответ — `0.2`, подтверждённый артефактом `current`. Запись содержит ноль вызовов оболочки/инструментов и не содержит трассировки, поэтому свидетельств чтения моделью обоих файлов нет. Зафиксированный оценщик точного совпадения отметил ответ как `wrong`, но один контрольный ответ при семи отсутствующих испытаниях не является результатом исследования поведения или сравнением контрольной и экспериментальной групп. Испытания экспериментальной группы и вызовы Go CLI не выполнялись.

`report.json` фиксирует `valid=false`: семь отсутствующих испытаний и превышенный бюджет токенов. `plan.json` и `rows.jsonl` — исходные зарегистрированный план и единственная запись. Трассировок инструментов нет, кроме пустой трассировки этой записи. После остановки по бюджету оставшиеся разрешённые испытания не запускались.

Точная команда:

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
