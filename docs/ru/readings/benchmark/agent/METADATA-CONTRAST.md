---
layout: default
lang: ru
permalink: /ru/readings/benchmark/agent/METADATA-CONTRAST/
documentation_id: benchmark-agent-metadata-contrast
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
documentation_status: historical
historical: true
---
{% include nav_ru.html %}

> Историческая редакция · 2026-10-04 · Сверено с ревизией `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. [Английский оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/METADATA-CONTRAST.md). Архивный протокол сохранён как историческое свидетельство; публикация не запускает его команды.

<a id="preregistered-metadata-only-contrast"></a>

# Предзарегистрированное сравнение только метаданных
{: #section-1}

Это протокол будущего запуска, а не завершённый результат или разрешение на вызовы модели. Завершённый основной пилот с заранее вычисленными данными содержит восемь строк и отчёт анализатора с `valid=true`; его экспериментальная группа включает проекции Go CLI. Не объединяйте эти строки или их знаменатель с данным сравнением. Более ранний протокол `metadata-only-20260926-v1` был заменён до первого вызова модели: его адаптер передавал бы Codex метки экспериментальной программы `case_id` и `arm`, раскрывая назначение экспериментальной группы. Этот протокол v2 использует проекцию запроса со скрытыми метками; у v1 нет наблюдений для анализа.

[`corpus/metadata_cases.json` — оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/metadata_cases.json) ([оригинал](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/corpus/metadata_cases.json)) (SHA-256 `4fd2222f07b91cacda4e8f15b0b34792f41d962a151e2ddc95760b8329b1745a`) содержит четыре случая: три `mechanism_only` и один `realistic`. В каждой паре одинаковы вопрос, ID артефактов, пути и тело Markdown после удаления вводных метаданных YAML экспериментальной группы и пробельных символов по краям. Экспериментальная группа добавляет только вводные метаданные OKF, включая `status` документа; обе группы сохраняют одинаковый текст с ответом и обычные признаки актуальности. Это проверяет добавочный эффект метаданных в данном небольшом корпусе. Проекции CLI, самостоятельный выбор инструментов и эффективность на произвольных репозиториях не проверяются.

Предзарегистрированная конечная точка — полный парный запуск из восьми строк, пригодный для анализа: четыре случая, контрольная группа перед экспериментальной, один повтор. Для каждой группы сообщайте результаты точного оценщика v1: `correct`, `stale`, `wrong`, `refusal`, `ungradable` и операционные сбои, используя знаменатели фактических ошибок и завершённости из [README.md]({{ '/ru/readings/benchmark/agent/README/' | relative_url }}). Также сообщайте вызовы инструментов, входные/выходные/кешированные токены, длительность и неизвестную стоимость в долларах. Порога эффективности нет; четыре парных наблюдения дают описательные данные и не позволяют установить общий процент улучшения. Проверяйте неожиданные исходные ответы и ссылки, не меняя замороженный оценщик и не повышая оценку строки вручную.

До любого запуска получите явное разрешение на восемь вызовов OpenAI Codex и передачу исходных фрагментов и документов OKF для этих четырёх случаев. Запускайте из чистого закреплённого рабочего дерева `dd8989ba36b8e27ac5762e2e0984cac7f88e8738`. Запишите полный HEAD в плане вместе с закреплённой ревизией SPEC профиля моментов времени `0b87c52c6ef999286c745e19998fdfcd03d5dbee`, указанным выше корпусом и адаптером, собранным из этого рабочего дерева с `-buildvcs=false`. Его SHA-256: `8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9`; путь: `/private/tmp/okf-eval-codex-adapter-dd8989b`. Если он недоступен или отличается, зафиксируйте новый хеш адаптера и ID запуска до первого вызова модели. Управляющая программа проверяет чистый коммит, закрепление SPEC и среды исполнения до записи плана. Встроенная среда Codex — `codex-cli 0.155.0-alpha.16.3` с SHA-256 `c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a`. Адаптер закрепляет `gpt-6-luna` с низким уровнем рассуждений; он не может проверить снимок модели серверной части. Настройки модели и политика инструментов только для чтения должны быть одинаковыми в обеих группах.

Из корня этого чистого репозитория проверьте закреплённые значения и используйте новые, ещё не существующие пути вывода:

```sh
set -e
set -o pipefail
test -z "$(git status --porcelain --untracked-files=all)"
test "$(git rev-parse HEAD)" = dd8989ba36b8e27ac5762e2e0984cac7f88e8738
test "$(shasum -a 256 benchmark/agent/corpus/metadata_cases.json | cut -d ' ' -f 1)" = \
  4fd2222f07b91cacda4e8f15b0b34792f41d962a151e2ddc95760b8329b1745a
test "$(/Applications/ChatGPT.app/Contents/Resources/codex --version 2>/dev/null)" = \
  'codex-cli 0.155.0-alpha.16.3'
test "$(shasum -a 256 /Applications/ChatGPT.app/Contents/Resources/codex | cut -d ' ' -f 1)" = \
  c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a
test "$(shasum -a 256 /private/tmp/okf-eval-codex-adapter-dd8989b | cut -d ' ' -f 1)" = \
  8f132bc3d12aeb1ea78d74642156427c1921a35b28d60f12b1296354a0b44ea9
test ! -e /private/tmp/okf-metadata-only-v2-plan.json
test ! -e /private/tmp/okf-metadata-only-v2-rows.jsonl

OKF_METADATA_COMMIT=$(git rev-parse HEAD)
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go run \
  ./benchmark/agent/cmd/okf-agent-eval run \
  -corpus benchmark/agent/corpus/metadata_cases.json \
  -plan /private/tmp/okf-metadata-only-v2-plan.json \
  -rows /private/tmp/okf-metadata-only-v2-rows.jsonl \
  -adapter /private/tmp/okf-eval-codex-adapter-dd8989b \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -run-id metadata-only-20260926-v2 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -commit "$OKF_METADATA_COMMIT" \
  -spec 0b87c52c6ef999286c745e19998fdfcd03d5dbee \
  -repeats 1 -max-trials 8 -max-seconds 360 -max-tokens 150000 -unpriced \
  -toolkit-mode none \
  -model-tool-access 'Codex read-only ephemeral temp; projected question/artifacts/instructions JSON; no CLI projections'
```

`-toolkit-mode none` предотвращает проекции Go CLI управляющей программы в обеих группах. Адаптер отправляет Codex только `question`, `artifacts` и общие `instructions` в новой временной сессии только для чтения для каждого испытания. Метки экспериментальной программы `case_id`, `arm` и эталонный ответ остаются вне видимого модели промпта. Этот протокол не доказывает, что модель прочитала каждый переданный артефакт. Управляющая программа ограничивает каждый вызов адаптера 45 секундами и устанавливает общий крайний срок контекста 360 секунд. Порог 150,000 токенов проверяется **после** каждого ответа и может быть превышен одним вызовом. `-unpriced` означает, что стоимость отдельного вызова в USD недоступна; это не означает нулевую стоимость. План и строки создаются эксклюзивно, поэтому прерванная попытка требует нового ID запуска и путей вывода. Сохраняйте неполные строки и объявляйте запуск недействительным, если любая проверка вызова, закреплённых значений или бюджета завершается ошибкой.

Проанализируйте сохранённые строки с тем же корпусом:

```sh
GOCACHE=/private/tmp/okf-agent-gocache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go run \
  ./benchmark/agent/cmd/okf-agent-eval analyze \
  -corpus benchmark/agent/corpus/metadata_cases.json \
  -plan /private/tmp/okf-metadata-only-v2-plan.json \
  -rows /private/tmp/okf-metadata-only-v2-rows.jsonl
```

Общий промпт запрашивает кратчайшее фактическое значение и исходный ID артефакта. Точный оценщик v1 засчитывает правильный ответ, только если он совпадает с зарегистрированным ответом или допустимым вариантом и ссылается на `current`. Правильный ответ без ссылки `current` получает `ungradable`; ответ с пояснением, например `No, not yet`, считается `wrong` по этому замороженному точному оценщику. Устаревшие значения сопоставляются точно. Опубликуйте план, исходные строки, отчёт анализатора, хеш корпуса и любые заметки ручной проверки. `valid=true` означает, что сравнение было полным и пригодным для анализа, а не что метаданные улучшили ответы.
