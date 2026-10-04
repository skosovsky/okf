---
title: "Проверка в GitHub Actions"
description: "Проверка в GitHub Actions"
permalink: /ru/github-action/
---

{% include nav_ru.html %}

<span id="github-action-validate-an-okf-bundle"></span>

# Проверить знания в GitHub Actions {#page-top}

Если набор хранится в репозитории, проверяйте его вместе с кодом. Нужны существующий GitHub-репозиторий с `knowledge/` и права на изменение процесса CI. Сначала убедитесь, что локальная проверка проходит, затем добавьте шаги в задание CI.

## Настроить шаги {#configure}

Пример закрепляет OKF за опубликованной ревизией `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. Action собирает Go-программу именно из этой ревизии и запускает тот же валидатор, что локально; другую версию не загружает.

{% raw %}
```yaml
permissions:
  contents: read

steps:
  - uses: actions/checkout@fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09 # v5
  - id: okf
    uses: skosovsky/okf@61e75e9aa9a8719dfb480bf1f5226553b3a21d70
    with:
      path: knowledge
      spec: auto
      strict: 'true'
      check-links: 'true'
      check-orphans: 'true'
      as-of: '2026-09-26'
      max-warnings: '0'
  - if: always() && steps.okf.outputs['report-path'] != ''
    uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
    with:
      name: okf-validation
      path: ${{ steps.okf.outputs['report-path'] }}
```
{% endraw %}

Ожидаемый результат для исправного набора: успешный шаг и JSON-артефакт `okf-validation`. При нарушении структуры или превышении бюджета предупреждений шаг завершится ошибкой, а отчёт останется доступен для загрузки. Путь разрешается относительно `GITHUB_WORKSPACE`; абсолютные пути допустимы. Пробелы, кавычки и символы оболочки передаются как данные пути.

## Выбрать проверки {#inputs}

| Параметр | По умолчанию | Значение |
| --- | --- | --- |
| `path` | `.` | Папка набора |
| `spec` | `auto` | Режим версии: `auto`, `0.1`, `0.2` |
| `temporal-profile` | пусто | Редакция времени; по умолчанию `date-3fcbb9f` |
| `strict` | `false` | Дополнительные рекомендации для метаданных |
| `check-links` | `false` | Проверка Markdown-ссылок |
| `check-orphans` | `false` | Проверка включения документов в индексы |
| `as-of` | пусто | Дата или время со смещением в выбранной редакции |
| `max-warnings` | пусто | Бюджет предупреждений; `0` запрещает все предупреждения |

`max-warnings=N` пропускает не более N предупреждений. Это отдельная политика CI; значение `conformant` продолжает отражать базовое соответствие OKF. `strict` включает дополнительные проверки. При нескольких проблемах ошибки структуры имеют приоритет в диагностике.

## Прочитать результат {#outputs}

- `outcome`: `pass`, `validation_failure` или `operational_failure`.
- `report`: полный JSON, если он не больше 60 000 байт; иначе пусто. При операционной ошибке тоже пусто.
- `report-path`: временный путь к полному отчёту на том же исполнителе GitHub, в том числе при неуспешной проверке. Загрузите его с `if: always()`; при операционной ошибке путь пустой.

Ошибки ввода, отсутствующая папка, ошибка чтения или неполный отчёт дают `operational_failure`. CLI возвращает 0 при успехе и 1 при обоих видах ошибки; Action различает их по наличию полного JSON-отчёта.

## Сравнить с локальным запуском {#troubleshooting}

```sh
go run ./cmd/okf validate --path knowledge --spec auto --strict --check-links --check-orphans --as-of 2026-09-26 --max-warnings 0 --format json
```

Используйте те же файлы, ревизию программы и флаги. Ошибка проверки всё равно выводит JSON в stdout; операционная ошибка пишет в stderr без JSON. В этом репозитории отдельный процесс CI проверяет `uses: ./` на семи наборах тестовых данных. Полный контракт параметров находится в [action.yml](https://github.com/skosovsky/okf/blob/main/action.yml).
