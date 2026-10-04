---
layout: default
title: "Предварительно зарегистрированный запуск: Go CLI до и после исправления 004/005"
lang: ru
permalink: /ru/readings/benchmark/agent/beforeafter/LIVE/
documentation_id: benchmark-agent-beforeafter-live
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical: true
---
{% include nav_ru.html %}

**Исторический материал для чтения.** [Оригинал на английском: benchmark/agent/beforeafter/LIVE.md](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmark/agent/beforeafter/LIVE.md), ревизия репозитория `61e75e9aa9a8719dfb480bf1f5226553b3a21d70` (2026-10-04). Перевод сохраняет методику, даты, измерения и статус, зафиксированные в этой ревизии. Исторические результаты относятся к описанным запускам; они не описывают текущий продукт и не служат рекомендацией запускать агентов сегодня.

<a id="preregistered-live-run-go-cli-beforeafter-004005"></a>

# Предварительно зарегистрированный запуск: Go CLI до и после исправления 004/005
{: #section-1}

Это инструкция запуска, а не результат и не разрешение обращаться к внешней модели. Для выполнения зафиксирован чистый коммит `260a7cb701f08e21efd94e0b04db9b491f7e1db7`. Во время запуска храните эту инструкцию вне этой рабочей копии: неотслеживаемый `LIVE.md` нарушает требование чистоты, а его коммит меняет HEAD и делает фиксацию `-new-commit` недействительной. Запускайте из отдельной чистой копии ровно на этом коммите либо восстановите копию до этого коммита, предварительно сохранив текст в другом месте.

Пять зафиксированных вопросов в `corpus.json` — это четыре случая совместимости спецификации дат 005 и один случай поддержки спецификации временных меток 004. Обе группы используют одинаковые байты источников, вопрос, критерии фактической оценки, модель, адаптер, настройки, правила доступа к инструментам только для чтения и фиксированные часы. Каждый вызов модели получает исходные материалы и JSON-представления соответствующего Go CLI от `validate --json` и `parse --json`. Ожидается, что исторический файл 005 вернёт 12 классифицированных диагностик проверки; JSON с кодом завершения 1 — свидетельство, а не сбой выполнения. Исправленный файл 005 должен проходить проверку без ошибок. Исторический CLI не умеет выбирать более позднюю редакцию с временными метками, поэтому каждая старая строка 004 получает `revision_unsupported` без вызова модели. Это измеряет разрыв возможностей 004, а не парное улучшение фактических ответов. Описательное сравнение результатов модели опирается только на восемь пар 005 (16 строк ответов).

Зафиксированные входные данные, проверенные до написания документа:

| Входные данные | Зафиксированное значение |
| --- | --- |
| Исторический коммит исходников | `ed7ddc28cd127682023bd150ee377906b817e099` |
| Чистый коммит исправленных исходников | `260a7cb701f08e21efd94e0b04db9b491f7e1db7` |
| Исторический CLI `/private/tmp/okf-baf-old-okf` SHA-256 | `d74a1d309c72ae10efe794c3e5413186a71058fb2f465d9bf594293c2ad4ed47` |
| Исправленный CLI `/private/tmp/okf-baf-new-okf` SHA-256 | `5ff4dfaea4dc646ff51dcfe48dc94463f5233d16e113f1cde09825fca9cff2a0` |
| Адаптер `/private/tmp/okf-baf-codex-adapter` SHA-256 | `2e691b39499dd6da12d3edc8ee73cc6548cc31151537d47b171462eafb92a260` |
| Зафиксированный набор случаев `benchmark/agent/beforeafter/corpus.json` SHA-256 | `05d45be91e4ab15f49b8f71c4085eca91e85dddad3af6792755d824f536a6db4` |
| Среда запуска Codex `/Applications/ChatGPT.app/Contents/Resources/codex` | `codex-cli 0.155.0-alpha.16.3`, SHA-256 `c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a` |
| Модель и настройки | `gpt-6-luna`, название версии `gpt-6-luna`, `{"reasoning_effort":"low"}`; снимок модели на сервере нельзя независимо зафиксировать |

До выполнения получите явное разрешение на передачу **внешнему сервису OpenAI Codex** пяти вопросов и выдержек из `benchmark/agent/beforeafter/corpus.json` и указанных в нём тестовых материалов OKF, включая сторонний набор `okf-skills` и пример профиля временных меток. Каждый запрос модели также содержит JSON-представления старого или нового Go CLI и сведения об их происхождении. Адаптер может записывать служебное состояние Codex вне рабочего каталога; каждый вызов использует новый процесс и оболочку только для чтения в одноразовом временном каталоге. Разрешение должно охватывать максимум **18 вызовов**, **900 секунд** общего времени, мягкую остановку после **400 000 наблюдаемых входных и выходных токенов** и использование без доступной цены. Программа не может обеспечить лимит USD, поскольку адаптер не сообщает долларовую стоимость отдельного вызова. Проверка токенов выполняется после вызова и может превысить лимит на этот вызов; тайм-аут отдельного вызова — 45 секунд. Этот файл сам по себе не разрешает ни одного вызова.

Непосредственно перед согласованным запуском проверьте чистый HEAD и все зафиксированные значения. Пути плана и строк ниже не должны существовать: они создаются исключительно как новые и никогда не используются повторно после попытки запуска. Родительский каталог должен существовать. Программа сохраняет план до первого обращения к модели, чередует порядок групп по случаю и повтору и ожидает 20 строк за два повтора, включая две старые строки 004 без вызова модели.

```sh
git status --porcelain --untracked-files=all
git rev-parse HEAD
shasum -a 256 /private/tmp/okf-baf-old-okf /private/tmp/okf-baf-new-okf /private/tmp/okf-baf-codex-adapter benchmark/agent/beforeafter/corpus.json /Applications/ChatGPT.app/Contents/Resources/codex
test ! -e /private/tmp/okf-baf-live-20260926-v1-plan.json
test ! -e /private/tmp/okf-baf-live-20260926-v1-rows.jsonl

GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/beforeafter/cmd/okf-before-after run \
  -repo-root . \
  -corpus benchmark/agent/beforeafter/corpus.json \
  -plan /private/tmp/okf-baf-live-20260926-v1-plan.json \
  -rows /private/tmp/okf-baf-live-20260926-v1-rows.jsonl \
  -old-bin /private/tmp/okf-baf-old-okf \
  -old-bin-sha256 d74a1d309c72ae10efe794c3e5413186a71058fb2f465d9bf594293c2ad4ed47 \
  -new-bin /private/tmp/okf-baf-new-okf \
  -new-bin-sha256 5ff4dfaea4dc646ff51dcfe48dc94463f5233d16e113f1cde09825fca9cff2a0 \
  -adapter /private/tmp/okf-baf-codex-adapter \
  -adapter-sha256 2e691b39499dd6da12d3edc8ee73cc6548cc31151537d47b171462eafb92a260 \
  -model-runtime /Applications/ChatGPT.app/Contents/Resources/codex \
  -model-runtime-version 'codex-cli 0.155.0-alpha.16.3' \
  -model-runtime-sha256 c67698d0990aae05211d9c43ab343ad9517e406824dea77eca103a2806232b3a \
  -new-commit 260a7cb701f08e21efd94e0b04db9b491f7e1db7 \
  -run-id beforeafter-20260926-v1 \
  -model gpt-6-luna -model-version gpt-6-luna \
  -settings '{"reasoning_effort":"low"}' \
  -repeats 2 -max-model-calls 18 -max-seconds 900 \
  -max-tokens 400000 -unpriced
```

После запуска проанализируйте сохранённые строки без дополнительных вызовов модели:

```sh
GOCACHE=/private/tmp/okf-agent-gocache go run \
  ./benchmark/agent/beforeafter/cmd/okf-before-after analyze \
  -repo-root . \
  -corpus benchmark/agent/beforeafter/corpus.json \
  -plan /private/tmp/okf-baf-live-20260926-v1-plan.json \
  -rows /private/tmp/okf-baf-live-20260926-v1-rows.jsonl
```

Сохраните план, исходные строки, вывод анализа и свидетельства любых сбоев. Сообщите о правильности 005 по парным случаям и группам, отдельно о возможностях 004, всех сбоях выполнения, токенах, затраченном времени и неизвестной стоимости USD. Неполный запуск или `valid=false` нельзя использовать как результат сравнения до и после; восемь пар остаются небольшой описательной выборкой и не обосновывают общее утверждение о пользе продукта.
