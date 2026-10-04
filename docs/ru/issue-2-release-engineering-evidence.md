---
title: Свидетельства подготовки выпуска по задаче 2
description: Соответствие требований CI, поддержки платформ, v0.2.0 и прослеживаемости проверки её свидетельствам.
lang: ru
historical: true
permalink: /ru/issue-2-release-engineering-evidence/
status: historical
documentation_id: docs-issue-2-release-engineering-evidence
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
---

{% include nav_ru.html %}

> Исторический документ. Перевод сверяется с [оригиналом в ревизии `61e75e9`](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/docs/issue-2-release-engineering-evidence.md) (английский). Описаны результаты и требования на тот момент; это не отчёт о проверках текущей версии.

# Свидетельства подготовки выпуска по задаче 2 {#issue-2-release-engineering-evidence}

Эта запись сопоставляет публичные требования [задачи GitHub #2](https://github.com/skosovsky/okf/issues/2) (английский) с доступными для проверки материалами. Она различает проверку существующего тега `v0.2.0` и усиление подготовки выпусков после создания тега в [PR #3](https://github.com/skosovsky/okf/pull/3) (английский).

## Соответствие требований и свидетельств {#requirement-evidence}

| Требование | Исполняемое или доступное для проверки свидетельство |
| --- | --- |
| Запуск CI для PR, `main` и тегов | [Конфигурация CI](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/.github/workflows/ci.yml) и [проверки PR #3](https://github.com/skosovsky/okf/pull/3/checks); версионные теги должны указывать на коммит `main`, события с нулевой базой проверяют всё зафиксированное дерево на ошибки пробелов |
| Обязательные проверки тестов, гонок, vet, модулей, tidy и diff | Задания `quality` и `race` в конфигурации CI |
| Надёжное хранилище Linux | Задания тестирования и сборки Linux плюс полная локальная матрица ниже |
| Надёжное хранилище Darwin | Тест `store/fs` и сборка репозитория на родной платформе `macos-latest` |
| Контракт неподдерживаемой среды Windows | Сборка на родной платформе `windows-latest` и целевые тесты `Open`/`OpenContext` |
| Отсутствие расширения тегов на Android/iOS | Сборка Android и проверки `go list` для Android/iOS, выбирающие `fd_unsupported.go` |
| Стабильная типизированная ошибка неподдерживаемой платформы | `fs.ErrUnsupportedPlatform`, `fs.UnsupportedPlatformError` и контрактные тесты по AAA |
| Публичная политика платформ | EN/RU README, практическое руководство, обзор сайта, инструкции для агента и [контракт подготовки выпусков]({{ '/ru/release-engineering/' | relative_url }}) |
| Целостность существующего тега | Аннотированный тег `v0.2.0`; коммит после раскрытия тега `bb9c169`; процесс CI не перемещает и не пересоздаёт тег |
| Содержание и ограничения v0.2.0 | [Отслеживаемые примечания к выпуску]({{ '/ru/releases/v0.2.0/' | relative_url }}) и [выпуск GitHub](https://github.com/skosovsky/okf/releases/tag/v0.2.0) (английский) |
| Происхождение свидетельств изменений | Переименованный [отчёт о проверке изменений на основе парсера]({{ '/ru/parser-backed-lossless-mutations-evidence/' | relative_url }}), связанный с задачей #1 и коммитом `bb9c169` |
| Прослеживаемость проверки и завершения | Задача #2 → PR #3 → проверки на GitHub → коммит → существующий тег → выпуск GitHub; точные ссылки на запуски и коммиты записаны в задаче и выпуске |

## Локальная проверка {#local-verification}

Запуск из корня репозитория. Команды ниже сохранены дословно:

{% raw %}
```sh
go test ./...
go test -race ./...
go vet ./...
go mod verify
go mod tidy -diff
git diff --check
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/okf-store-fs-windows.test.exe ./store/fs
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./...
GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go list -f '{{join .GoFiles " "}}' ./store/fs
GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go list -f '{{join .GoFiles " "}}' ./store/fs
actionlint .github/workflows/ci.yml
zizmor .github/workflows
```
{% endraw %}

Результаты `go list` для Android и iOS должны включать `fd_unsupported.go` и не должны включать `fd_unix.go`. `actionlint` должен завершаться без ошибок, `zizmor` — без замечаний.

Длительные запуски фаззинга и профили производительности для 10,000 концептов остаются отдельными вручную собранными свидетельствами в отчёте об изменениях на основе парсера; они не являются обычными обязательными проверками CI для PR.

## Прежнее заявление о завершении {#legacy-completion-claim}

В завершающем комментарии к задаче #1 использованы «100%» и «zero findings» без сохранения публичных материалов проверяющих или запусков проверок на GitHub. Здесь это заявление не считается свидетельством; его нельзя восстановить задним числом. Эта задача заменяет такой подход таблицей требований, доступным для проверки PR, проверками на GitHub, точными ссылками на коммиты и запуски, существующим тегом и записью выпуска GitHub.
