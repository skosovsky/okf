---
title: "Обновление знаний"
description: "Обновление знаний"
permalink: /ru/knowledge-upkeep/
---

{% include nav_ru.html %}

<span id="optional-knowledge-upkeep-check"></span>

# Пересмотреть знания после изменения {#page-top}

Когда меняется код или требование, проверьте связанные заметки. Например, изменение срока доставки с 24 до 48 часов требует обновить источник и правило, как в первом запуске. Проверка `okf-upkeep` помогает зафиксировать, что вы выполнили этот пересмотр.

## Подготовка {#prerequisites}

Нужны Git-репозиторий, Go и набор `knowledge/`. Для агента можно использовать отдельный навык `okf-maintain`; переносимые инструкции для AGENTS.md и CLAUDE.md находятся в библиотеке документов. Проверка запускается явно и не устанавливает обработчики событий клиента.

## Задать политику {#policy}

Создайте `upkeep.json` в корне репозитория, вне корневого индекса OKF:

```json
{
  "repo_root": ".",
  "bundle_root": "knowledge",
  "relevant_paths": ["bundle", "validator", "store", "graph", "mutation", "internal", "cmd", "backfill", "viewer", "upkeep", "go.mod", "go.sum", "action.yml"],
  "exclude_paths": ["internal/generated"],
  "mode": "advisory",
  "failure_policy": "open",
  "timeout_ms": 5000,
  "max_session_minutes": 1440
}
```

Пример соответствует этому репозиторию; для своего проекта замените каталоги. `repo_root` разрешается относительно файла настроек и должен совпасть с корнем Git. Пути внутри двух списков относительные; исключение имеет приоритет. Сгенерированные результаты исключайте из значимых путей.

## Сохранить исходное состояние и выполнить работу {#baseline}

До изменений:

```sh
session_id=$(go run ./cmd/okf-upkeep baseline --config upkeep.json --out /tmp/okf-upkeep-baseline.json)
```

После изменения кода или требований:

```sh
go run ./cmd/okf-upkeep check --config upkeep.json --baseline /tmp/okf-upkeep-baseline.json --session "$session_id"
```

Проверяются изменения рабочей копии и коммиты после исходного состояния, включая новые, удалённые и переименованные файлы. Поддерживаются пробелы и переводы строк в именах. Коммит исходно изменённых файлов без изменения байтов или исполняемого режима сам по себе новым изменением не считается; новые коммиты сравниваются с исходным состоянием даже после отката рабочей копии. Исходное состояние связано с репозиторием, HEAD, сеансом и временем; срок действия задаёт `max_session_minutes`.

## Зафиксировать решение {#decision}

Без значимых изменений результат — `no_relevant_change`. Если нужен пересмотр, результат `needs_review` содержит `fingerprint`. Прочитайте текущее требование, код и затронутые заметки, затем обновите заметку либо объясните, почему она не затронута. Пример `decision.json` для второго случая:

```json
{"fingerprint":"<fingerprint из check>","kind":"unaffected","reason":"Изменены только имена тестовых данных; описанные API и поведение сохранены."}
```

Если заметка обновлена:

```json
{"fingerprint":"<fingerprint из check>","kind":"updated","updated_concepts":["knowledge/architecture.md"]}
```

Указанная заметка должна действительно измениться после исходного состояния; одной записи в `knowledge/log.md` недостаточно. Решение храните вне репозитория или исключите его путь из значимых. После последней правки выполните:

```sh
go run ./cmd/okf-upkeep check --config upkeep.json --baseline /tmp/okf-upkeep-baseline.json --session "$session_id" --decision /tmp/decision.json
```

Ожидайте `reviewed_updated` или `explicit_unaffected`. Если после решения снова изменился код, прежний `fingerprint` устаревает и результат возвращается к `needs_review`. Эта запись подтверждает действие пересмотра; правильность решения проверяйте отдельно.

## Режимы и ошибки {#troubleshooting}

В `advisory` состояние `needs_review` возвращает код 0; в явном `blocking` — код 2. Ошибки настроек и использования — код 1. При ошибке Git или входных данных возвращается `unavailable`: политика `open` разрешает продолжение, `closed` блокирует только в режиме `blocking`. `--override "причина"` и `--attempt 2` дают явное исключение для одного вызова и отражаются в JSON. Ограничение времени для Git и чтения данных задаётся настройками, максимум 60 секунд.

Для клиента с поддержкой обработчиков Stop существует `--adapter claude-stop`: он читает JSON со stdin, учитывает логическое поле `stop_hook_active`, игнорирует прочие поля и возвращает `{}` при разрешении либо `{"decision":"block","reason":"..."}` при блокировке, с кодом 0. Повторный вызов активного обработчика Stop разрешён. Codex и CI могут вызывать обычную JSON-команду напрямую.

Точные входы и выходы: [config](https://github.com/skosovsky/okf/blob/main/upkeep/contracts/config.schema.json), [decision](https://github.com/skosovsky/okf/blob/main/upkeep/contracts/decision.schema.json), [result](https://github.com/skosovsky/okf/blob/main/upkeep/contracts/result.schema.json). [Навык okf-maintain]({{ '/ru/readings/skills/okf-maintain/SKILL/' | relative_url }}).
