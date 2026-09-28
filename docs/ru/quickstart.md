---
title: Быстрый старт OKF и offline demo
description: Создание, чтение, поддержка и восстановление OKF bundle.
permalink: /ru/quickstart/
---

{% include nav_ru.html %}

# Быстрый старт

Команды запускаются из checkout репозитория с Go из `go.mod`. Установленный `okf` может заменить `go run ./cmd/okf`. `okf-mcp` необязателен: host должен подключить его, прежде чем появятся инструменты. Для `okf-upkeep` нужен Git, для `okf-backfill` — история Git и проверенное предложение аналитика. Скилы не устанавливают бинарники и не выдают разрешения host.

## Создать → проверить → открыть

```sh
demo_dir=$(mktemp -d)
go run ./cmd/okf init "$demo_dir/my-knowledge"
go run ./cmd/okf validate --path "$demo_dir/my-knowledge" --spec 0.2 --as-of 2026-09-26
go run ./cmd/okf view "$demo_dir/my-knowledge" --output "$demo_dir/my-knowledge.html" --spec 0.2 --temporal-profile date-3fcbb9f --as-of 2026-09-26
```

Открой `my-knowledge.html` с диска. Созданный concept — черновой шаблон; перед использованием как evidence замени его сведениями из источников. `init` требует отсутствующий каталог с существующим родителем; `view` — новый выходной файл вне bundle. Validation проверяет формат, а не фактическую истинность.

## Прочитать существующий bundle

Собственный [`knowledge/`](https://github.com/skosovsky/okf/tree/main/knowledge) bundle связывает claims с файлами репозитория:

```sh
go run ./cmd/okf info knowledge --spec auto --as-of 2026-09-26
go run ./cmd/okf validate --path knowledge --spec auto --strict --check-links --check-orphans --as-of 2026-09-26 --max-warnings=0
```

При подключённом MCP вызови **`validate_bundle` сервера OKF** с `{"bundle_path":"<absolute path to knowledge>","target_version":"auto","strict":true,"check_links":true,"check_orphans":true,"as_of":"2026-09-26"}`. Вызывающий выбирает доступный абсолютный root bundle; сервер проверяет его и применяет containment/no-follow правила внутри него. Какие корни разрешено выбирать, определяют права host. Универсального клиентского префикса инструмента нет. При отсутствии MCP используй CLI; если нет и CLI, называй чтение ручным и не объявляй bundle проверенным.

## Чтение и изменение через MCP

Сервер предоставляет тринадцать инструментов. `search_concepts` ищет
буквальное совпадение, `read_concept` читает выбранный concept целиком, а
`get_neighbors` возвращает ограниченный набор связей. Поля `total` и
`truncated` показывают, что часть результатов не вошла в ответ. Курсора в
этом контракте нет: уточни запрос или фильтры direction/kinds либо запроси
весь граф, если он помещается в лимит. Ошибка `resource_limit` означает
неудачный вызов, а не частичную страницу. При неверном вводе сервер возвращает
стабильный код и ограниченную подсказку по полю; исправь поле перед повтором.
Подробности — в [контракте запросов]({{ '/contracts/concept-queries/' | relative_url }}).

Перед записью изучи результат `preview_concept_patch`,
`preview_v02_migration` или `preview_temporal_upgrade`, затем применяй тот же
план с точной ревизией и digest. Preview само по себе не даёт разрешения на
публикацию. После timeout результат apply может быть неизвестен: проверь
bundle и повтори тот же запрос до построения нового плана. См.
[границы доверия mutation]({{ '/contracts/mcp-mutation-trust-boundaries/' | relative_url }}).

## Публичный viewer и локальный экспорт

[Открыть публичный viewer]({{ '/demo/knowledge.html' | relative_url }}).
Это статический снимок открытого bundle `knowledge/` этого репозитория с
явной датой проверки `2026-09-26`. CSS, JavaScript и данные встроены в HTML;
поиск и переходы между concepts не загружают дополнительные данные. Внешние
ссылки на источники открываются только по нажатию. Тот же HTML можно собрать
локально:

```sh
demo_dir=$(mktemp -d)
./scripts/build-viewer-demo.sh "$demo_dir/knowledge-demo.html"
```

Скрипт проверяет и экспортирует bundle с зафиксированным date profile и
`as-of 2026-09-26`. Открой результат offline, найди `Package boundaries`,
перейди по ссылке concept и открой `#architecture` напрямую. Проверь sources и
отдельные trust, status, staleness. Отсутствие `verified` означает
`unverified`; `draft` — lifecycle state; без `stale_after` viewer показывает
`unevaluated` даже при явной reference date. Подробности — в
[viewer guide](https://github.com/skosovsky/okf/blob/main/viewer/README.md).
CI повторно генерирует HTML и побайтово сравнивает его с опубликованным файлом.

## Поддержать после изменения

Перед правками следуй [`okf-maintain`](https://github.com/skosovsky/okf/blob/main/skills/okf-maintain/SKILL.md): сделай `okf-upkeep baseline` с JSON config, изучи код и затронутые concepts, затем вызови `okf-upkeep check` с baseline и session ID. При `needs_review` обнови concepts либо запиши конкретное решение `unaffected`, привязанное к fingerprint, и повтори проверку после последней правки. Config и команды — в [upkeep guide](https://github.com/skosovsky/okf/blob/main/docs/knowledge-upkeep.md). Успешная validation не заменяет содержательное ревью.

## Восстановить из Git

Применяй [`okf-backfill`](https://github.com/skosovsky/okf/blob/main/skills/okf-backfill/SKILL.md) при наличии реального репозитория и существующего bundle. Извлеки ограниченный manifest между явно указанными base/head commit, сверь каждый claim с recorded diff, подготовь план, затем явно примени и независимо проверь результат. JSON artifacts и границы разрешения описаны в [backfill protocol](https://github.com/skosovsky/okf/blob/main/backfill/PROTOCOL.md). Один `extract` не доказывает claim; отсутствие истории или analyst proposal — причина остановиться, а не выдумать evidence.

Демо показывает работу CLI/viewer. Оно не подтверждает model routing,
активацию скилов, совместимость любого host или качество benchmark. Validation
проверяет соответствие формату, а не фактическую истинность показанных claims.
