---
title: Skill
description: Repo-local OKF agent skill.
permalink: /ru/skill/
---

{% include nav_ru.html %}

# Skill

В репозитории есть `skills/open-knowledge-format`: portable agent skill для консультаций, проектирования, создания, конвертации, обогащения и проверки OKF bundles.

## Когда использовать

- Объяснить OKF concepts и правила conformance.
- Спроектировать структуру нового OKF bundle.
- Конвертировать Markdown, Notion, Obsidian, CSV или spreadsheet materials в OKF.
- Обогатить existing concepts metadata, `# Schema`, `# Examples`, citations, cross-links, indexes и logs.
- Проверить bundle через CLI этого репозитория.
- Просмотреть, визуализировать или экспортировать graph output для Markdown links и YAML semantic relations.

## Процесс работы с CLI toolkit

Для conformance gate skill запускает quality gate:

```sh
go run ./cmd/okf validate -path <bundle>
```

Для review workflows можно включить все advisory-режимы:

```sh
go run ./cmd/okf validate -path <bundle> --strict --check-links --check-orphans
```

Skill трактует `[ERROR]` как hard OKF v0.1 failure. `[WARN]` и `[INFO]`
остаются review-сигналами для recommended fields, conventional body sections,
links, anchors и local index coverage. Отсутствующий `resource` намеренно
разрешен для abstract concepts.

С `--check-orphans` пустой non-root local `index.md` трактуется как orphan
coverage surface и дает orphan warnings вместо empty-index structure error.

Для summary и maintenance skill может использовать:

```sh
go run ./cmd/okf info <bundle>
go run ./cmd/okf index <bundle>
go run ./cmd/okf fmt <file>
```

## Процесс вывода graph

Для быстрого просмотра в терминале skill может использовать default graph output:

```sh
go run ./cmd/okf graph <bundle>
```

Для non-default graph output выбирать формат по месту использования:

```sh
go run ./cmd/okf graph <bundle> -format dot
go run ./cmd/okf graph <bundle> -format mermaid
go run ./cmd/okf graph <bundle> -format json-ld
go run ./cmd/okf graph <bundle> -format ntriples
```

Используй `-format mermaid`, когда graph нужно вставить в Markdown или README.
Mermaid output начинается с `graph LR`; битые internal links отображаются
пунктирными ребрами с меткой `404`. Используй `-format dot` для Graphviz
tooling; `--dot` остается legacy alias для `-format dot`. Используй
`-format json-ld`, когда graph tooling или agent harness нужен машиночитаемый
вывод с `@context` и `@graph`. В JSON-LD concepts - это узлы `bundle:<id>` с
`@type: "okf:Concept"`, а internal links - объекты `okf:Reference` с `target` и
`exists`; dangling links сохраняются как `"exists": false`. Используй
`-format ntriples`, когда RDF tooling, bulk-load jobs, streaming graph pipelines
или shell processing нужен один full-IRI факт на строку.

Для impact analysis предпочитай semantic YAML `relations`, а не generic
Markdown links. Markdown links - навигация; `relations` - contract edges.
Targets используют OKF concept refs вроде `tables/orders#col-status`, а не
Markdown paths вроде `tables/orders.md#col-status`. Для field-level tracing
нужен явный `id` или `anchor` на nested YAML mapping:

```yaml
schema:
  fields:
    - id: payload-user_id
      name: user_id
      relations:
        writes_to:
          - target: tables/orders#col-customer_id
```

Не выводи anchors из display `name`. `okf validate --check-links` проверяет
только Markdown links; graph export все равно сохраняет semantic edges, даже
если target concept отсутствует.

Grammar relation ref: `<concept-id>[#<fragment>]`. Invalid refs:
`/tables/orders.md`, `tables/orders.md`, `#local-section`,
`https://example.com/orders`, `urn:orders`, `tables/orders#`,
`tables/orders#col#status`, `tables/orders# col-status`.

## Установка как local Codex plugin

Из корня репозитория:

```sh
codex plugin marketplace add .
codex plugin add okf@okf-local
```

После установки открой новую Codex-сессию и попроси использовать `$open-knowledge-format`.

## Установка как Claude Code plugin

Используй Claude plugin manifest из репозитория:

```text
/plugin marketplace add skosovsky/okf
/plugin install okf@okf
/reload-plugins
```

После установки вызови `/okf:open-knowledge-format` или дай Claude Code
использовать skill автоматически, когда задача связана с OKF.

## Portable skill path

```text
skills/open-knowledge-format/SKILL.md
```

Для runtime, который поддерживает local skills, зарегистрируй или скопируй `skills/open-knowledge-format` с именем `open-knowledge-format`.

## References внутри skill

| Файл | Назначение |
| --- | --- |
| `references/spec-v01.md` | OKF v0.1 reference. |
| `references/examples.md` | Example bundles. |
| `references/conversion.md` | Notion, Obsidian, CSV и spreadsheet conversion guidance. |

У skill нет bundled validation script. Детерминированные checks и graph extraction идут через `cmd/okf`.
