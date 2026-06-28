---
name: open-knowledge-format
description: >
  Создавать, проверять, анализировать и обогащать Open Knowledge Format (OKF) bundles:
  открытый формат для представления организационных знаний как Markdown-файлов
  с YAML frontmatter. Использовать, когда пользователь упоминает OKF,
  Open Knowledge Format, knowledge bundle, OKF bundle, базу знаний для агентов,
  консультирование по OKF, validate OKF, convert to OKF, enrich knowledge docs,
  agent-readable knowledge, LLM wiki, knowledge catalog или хочет
  структурировать знания как Markdown-файлы для потребления AI-агентами. Также
  использовать, когда у пользователя есть директория Markdown-файлов и он хочет
  сделать ее интероперабельной или совместимой со стандартом OKF.
---

# Open Knowledge Format (OKF)

OKF - открытая, нейтральная к поставщикам спецификация v0.1 draft для представления знаний как директории Markdown-файлов с YAML frontmatter. Обязательный SDK не нужен: если файл можно прочитать через `cat`, значит OKF можно потреблять.

OKF фиксирует минимальный интероперабельный формат: знания, написанные разными производителями, могут потребляться разными агентами без трансляции в платформенный формат.

Полная русская справка по спецификации: [references/spec-v01.md](references/spec-v01.md). Источник истины для спорных деталей - upstream `SPEC.md` в `GoogleCloudPlatform/knowledge-catalog`.

## Что должен уметь skill

Использовать skill в восьми режимах:

1. **Консультировать по OKF** - объяснять модель bundle/concept/frontmatter/body/link/citation, границу hard conformance и soft guidance, применимость OKF и отличия от Obsidian/Notion/metadata-as-code.
2. **Проектировать bundle** - выбрать структуру директорий без навязывания taxonomy, определить concept types, reserved files, cross-links, политику `index.md`/`log.md` и объявление версии.
3. **Создавать OKF documents** - писать `.md` concepts с YAML frontmatter, структурным Markdown body, `# Schema`, `# Examples`, `# Citations` при наличии оснований.
4. **Конвертировать sources в OKF** - переносить Notion, Obsidian, CSV/spreadsheets и произвольные Markdown-директории в conformant bundle; подробности в [references/conversion.md](references/conversion.md).
5. **Обогащать existing bundles** - добавлять обоснованные recommended fields, schemas, examples, citations, links, indexes и logs без выдумывания данных.
6. **Выполнять deterministic CLI pipeline** - использовать OKF CLI из Go module для quality gate, summary, maintenance и graph extraction; разделять errors/warnings/info и не отклонять bundles по soft guidance.
7. **Работать через MCP, если host это поддерживает** - использовать `okf-mcp` как отдельный stdio MCP server для tool-based чтения, проверки, graph extraction и безопасного редактирования local OKF bundles.
8. **Поддерживать consumption workflow** - помогать агенту читать bundle через `index.md`, затем нужные concepts, links/citations, YAML `relations` и только потом deep files.

Не привязывать workflow к конкретному provider, IDE или cloud. OKF - формат, не платформа.

## OKF CLI

Для детерминированных операций использовать OKF CLI toolkit из Go module `github.com/skosovsky/okf/cmd/okf`. Skill должен оставаться универсальным: инструкции не завязаны на provider/runtime и не требуют локального checkout репозитория.

OKF bundle - это executable architecture surface: Markdown documents с YAML frontmatter могут включать Markdown links для навигации и YAML `relations` для строгих semantic edges. Агент сначала использует deterministic CLI output, а semantic/editorial judgment делает только после этого.

## OKF CLI pipeline

Детерминированный quality gate OKF - команда `okf validate`, реализованная в Go module `github.com/skosovsky/okf/cmd/okf` поверх доменных packages `bundle` и `validator`.

Использовать `okf validate`, когда пользователь просит:

- проверить OKF bundle;
- подтвердить conformance OKF v0.1;
- проверить результат после создания, конвертации или enrichment;
- отличить hard errors от warnings/info.

### Quality Gate

Основная self-contained команда для агента:

```sh
go run github.com/skosovsky/okf/cmd/okf@latest validate -path <bundle>
go run github.com/skosovsky/okf/cmd/okf@latest validate -path <bundle> --strict --check-links --check-orphans
```

Если binary `okf` уже доступен в `PATH`, можно использовать короткую форму:

```sh
okf validate -path <bundle>
okf validate -path <bundle> --strict --check-links --check-orphans
```

Интерпретировать результат так:

- exit code `0` + `PASS` - bundle conformant;
- exit code non-zero + `FAIL` - есть hard conformance errors;
- CLI usage/flag error возвращает `1` и печатает `error:` без validation summary;
- `[ERROR]` - нарушение conformance, нужно исправить;
- `[WARN]` - soft guidance, bundle может оставаться conformant;
- `[INFO]` - информационная находка, например broken link как knowledge gap.

Режимы `validate`:

| Режим | Флаг | Что значит для агента |
|-------|------|------------------------|
| Base conformance | default | Выполняется всегда. `[ERROR]` означает, что bundle не является OKF v0.1. |
| Strict guidance | `--strict` | Проверяет recommended metadata/body conventions. Дает `[WARN]`, но не ломает conformance. |
| Link graph | `--check-links` | Проверяет target files и anchors. Missing files дают `[INFO]`, missing anchors - `[WARN]`. |
| Orphan coverage | `--check-orphans` | Проверяет покрытие concept files локальными `index.md`. Orphans дают `[WARN]`, missing index - `[INFO]`. |

Base conformance проверяет:

1. Все Markdown-файлы валидны как UTF-8.
2. Non-reserved concept `.md` files имеют YAML frontmatter block строго в начале файла, отделенный строками `---`.
3. `type` в concept frontmatter - непустая YAML string.
4. Reserved `log.md` не имеет frontmatter, использует `## YYYY-MM-DD`, newest first, и list entries.
5. Reserved `index.md` не имеет frontmatter, кроме root `okf_version`; body содержит headings и Markdown list entries со ссылками.
6. Unknown frontmatter keys, unknown `type` values и future `okf_version` values молча принимаются ради forward compatibility.

Исключение: с `--check-orphans` пустой non-root local `index.md` допускается как orphan coverage surface и дает orphan warnings вместо base empty-index error.

`--strict` проверяет:

1. Наличие `title`, `description`, `tags`, `timestamp`.
2. `tags` как YAML list of strings.
3. `timestamp` как `time.RFC3339`.
4. `resource`, только если он присутствует: значение должно быть URI string. Отсутствующий `resource` не является warning.
5. Citation markers `[1]` -> нижняя секция `# Citations` с валидной непрерывной нумерацией; citation targets должны быть valid URIs, bundle-absolute paths или paths under `references/`.
6. `# Examples` -> concrete example content: code block, list, table, link или substantive prose.
7. `type: BigQuery Table` -> секция `# Schema`.
8. Descriptions в `index.md` entries совпадают с `description` target concept, если target description есть.

Semantic checks остаются out of scope для `okf validate` и делегируются агенту или policy: поиск claims без citation markers, оценка осмысленности `type`, стиль "structural markdown over prose", генерация контента и исправление ссылок.

Warnings/info не должны автоматически блокировать bundle. Они требуют явного решения по задаче: исправить, оставить как known gap или передать на custom policy.

### Bundle Summary и Maintenance

Как использовать CLI:

1. По умолчанию использовать Go module command:

```sh
go run github.com/skosovsky/okf/cmd/okf@latest validate -path <bundle>
go run github.com/skosovsky/okf/cmd/okf@latest validate -path <bundle> --strict --check-links --check-orphans
go run github.com/skosovsky/okf/cmd/okf@latest info <bundle>
go run github.com/skosovsky/okf/cmd/okf@latest index <bundle>
go run github.com/skosovsky/okf/cmd/okf@latest graph <bundle>
go run github.com/skosovsky/okf/cmd/okf@latest graph <bundle> -format mermaid
go run github.com/skosovsky/okf/cmd/okf@latest graph <bundle> -format json-ld
go run github.com/skosovsky/okf/cmd/okf@latest graph <bundle> -format ntriples
```

2. Если `okf` уже доступен в `PATH`, можно использовать короткие команды `okf validate`, `okf info`, `okf index`, `okf graph`.

Использовать:

- `okf info <bundle>` - получить summary по concepts, types, links и version.
- `okf index <bundle>` - регенерировать `index.md` surfaces.
- `okf fmt <file>` - нормализовать один concept document.

## OKF MCP server

Skill entrypoint остается `skills/open-knowledge-format/SKILL.md`. Для MCP-capable hosts настраивать отдельную stdio command `okf-mcp`.

Установка:

```sh
go install github.com/skosovsky/okf/cmd/okf-mcp@latest
```

Из checkout репозитория:

```sh
go run ./cmd/okf-mcp
```

Пример client configuration:

```json
{
  "mcpServers": {
    "okf": {
      "command": "okf-mcp"
    }
  }
}
```

`stdout` зарезервирован под MCP JSON-RPC protocol. Diagnostics и startup errors писать только в `stderr`.

Tools:

- `list_concepts` - загрузить bundle и вернуть deterministic JSON с `concepts[]`: `id`, `type`, `title`, `path`.
- `read_concept` - прочитать raw Markdown одного concept по canonical concept id.
- `validate_bundle` - вернуть JSON validation report; conformance errors остаются normal report, а preflight/load failures возвращаются как tool error.
- `get_semantic_graph` - вернуть JSON-LD graph, byte-compatible с `okf graph -format json-ld`.
- `write_concept` - создать или обновить один concept через staged copy, strict/link/orphan validation и atomic rename.

Rules for MCP usage:

1. Всегда передавать absolute `bundle_path`.
2. `concept_id` должен быть canonical OKF concept id: `tables/orders`, без leading slash, `./`, `../`, `.md`, URI scheme, empty segment или surrounding whitespace.
3. `read_concept` и `write_concept` отклоняют symlink target/ancestor внутри bundle.
4. `write_concept` принимает YAML frontmatter без delimiters `---` и Markdown body; serialized document добавляет frontmatter delimiters сам.
5. Если staged validation нашла hard errors, считать write rejected: не пытаться повторять запись напрямую в файловую систему, сначала исправить diagnostics.
6. `write_concept` не регенерирует indexes. Если задача требует updated `index.md`, явно запускать `okf index <bundle>` после успешного write.
7. Перед изменениями вызвать `get_semantic_graph` и при необходимости `read_concept`, чтобы понять impact и текущий source text.
8. Для проверки использовать `validate_bundle`.
9. Если MCP server доступен в IDE-сценарии, concept edits делать через `write_concept`, не обходить MCP обычными filesystem writes, если только пользователь явно не попросил low-level repair.

### Knowledge Extraction

`okf graph` поддерживает форматы:

- default `text` - компактный adjacency list для терминала и быстрых agent checks;
- `-format dot` - Graphviz DOT для Graphviz-based пайплайнов; `--dot` остается legacy alias;
- `-format mermaid` - Mermaid flowchart syntax (`graph LR`) для вставки в Markdown/README/документацию. Битые internal links отображаются пунктирными ребрами с меткой `404`.
- `-format json-ld` - JSON-LD документ с `@context` и `@graph` для graph tooling и agent harnesses. Concepts выводятся как узлы `bundle:<id>` с `@type: "okf:Concept"`, internal links - как `okf:Reference` объекты с `target` и `exists`; dangling internal links сохраняются как `"exists": false`.
- `-format ntriples` - line-oriented RDF/N-Triples с full IRIs и одним фактом на строку для RDF tooling, bulk-load jobs, streaming graph pipelines и shell processing.

Когда пользователь просит визуализацию graph внутри Markdown, предпочитать `-format mermaid`. Когда пользователь просит совместимость с Graphviz или DOT, использовать `-format dot`.
Когда нужен JSON consumer, graph tooling или agent harness с `@context`/`@graph`, использовать `-format json-ld`. Когда нужен RDF bulk load, streaming facts или CLI pipeline, использовать `-format ntriples`.

### Level 2: Semantic Tracing & Field Granularity

Для impact analysis и архитектурной трассировки не полагаться только на generic Markdown links. Markdown links - слой human navigation; YAML `relations` - строгий semantic graph.

Использовать такой формат:

```yaml
schema:
  fields:
    - id: payload-user_id
      name: user_id
      relations:
        writes_to:
          - target: tables/orders#col-customer_id
relations:
  depends_on:
    - target: tables/orders#col-status
```

Rules:

- `target` в `relations` - OKF concept ref, не Markdown path: `tables/orders#col-status`, не `tables/orders.md#col-status`.
- Для field-level traceability всегда использовать URL fragments/subresources: API field, database column, metric input field.
- Nested source должен иметь явный `id` или `anchor`; не выводить anchor из display `name`.
- Для impact analysis сначала анализировать `relations`, затем Markdown links как навигационный/контекстный слой.
- `okf validate --check-links` проверяет только Markdown links; graph export сохраняет semantic edges даже при missing target concept.

Детерминированный CLI toolkit - `okf`.

### Принципы дизайна

1. **Минимум предписаний** - обязателен только `type`. Спецификация задает поверхность интероперабельности, а не модель контента.
2. **Независимость producer/consumer** - автор и читатель развязаны. Human-authored bundles кормят агентов; LLM-generated bundles читают люди.
3. **Формат, не платформа** - нет зависимости от cloud, SDK или vendor. Ценность появляется из того, что многие стороны говорят на одном формате.

---

## Ключевая терминология

- **Bundle** - дерево директорий с `.md` файлами. Единица распространения: git repo, tarball или поддиректория.
- **Concept** - один Markdown-файл как одна единица знания: таблица, метрика, playbook, API и т.д.
- **Concept ID** - путь к файлу внутри bundle без суффикса `.md`. Пример: `tables/users.md` -> ID `tables/users`.
- **Frontmatter** - YAML-блок между разделителями `---` в начале файла.
- **Body** - все после frontmatter. Обычный Markdown.
- **Link** - стандартная Markdown-ссылка, выражающая связь между concept.
- **Citation** - ссылка на внешний источник, подтверждающий утверждение в body.

---

## Краткая справка: поля frontmatter

| Поле | Обязательность | Описание |
|------|----------------|----------|
| `type` | **ДА** | Непустая YAML string: free-form вид concept, например `BigQuery Table`, `Metric`, `Playbook`, `API Endpoint` |
| `title` | Опционально; CLI warning при отсутствии | Человекочитаемое display name |
| `description` | Опционально; CLI warning при отсутствии | Краткое summary в одно предложение |
| `resource` | Опционально; warning только если присутствует и не является валидным URI | URI базового asset; не нужен для абстрактных concept |
| `tags` | Опционально; CLI warning при отсутствии | YAML list для сквозной категоризации |
| `timestamp` | Опционально; CLI warning при отсутствии | RFC3339-профиль ISO 8601 для последнего содержательного изменения |

Дополнительные producer-defined keys разрешены. Никогда не отклонять неизвестные поля.

## Зарезервированные имена файлов

| Файл | Назначение | Есть frontmatter? |
|------|------------|-------------------|
| `index.md` | Directory listing для progressive disclosure | НЕТ* |
| `log.md` | История изменений, новые записи сверху | НЕТ |

*Исключение: bundle-root `index.md` МОЖЕТ иметь frontmatter с `okf_version: "0.1"` для объявления версии спецификации.

Все остальные `.md` файлы являются concept documents. `tags` остаются полем frontmatter; SPEC не задает отдельный reserved file для tag aggregation.

## Конвенциональные headings в body

| Heading | Когда использовать |
|---------|--------------------|
| `# Schema` | Data assets - описать columns/fields |
| `# Examples` | Показать конкретное использование: code blocks, queries |
| `# Citations` | Перечислить external sources, подтверждающие claims |

---

## Создание bundle

Когда пользователь хочет создать OKF bundle с нуля:

### 1. Определить scope и структуру

Спросить: какие знания фиксируются? Таблицы, метрики, API, playbooks и т.д.
Организовать дерево директорий так, как естественно для домена.

### 2. Создать concept documents

Один concept = один `.md` файл. Минимальный conformant example:

```markdown
---
type: Metric
title: Monthly Recurring Revenue
description: Ежемесячная повторяющаяся subscription revenue.
tags: [revenue, saas]
timestamp: 2026-06-13T10:00:00Z
---

# Monthly Recurring Revenue (MRR)

## Definition

Сумма active subscriptions, нормализованная к месячному периоду.
One-time fees и overages не учитываются.

## Formula

`MRR = Σ(active_subscription_monthly_value)`

## Related

- [Churn Rate](./churn.md) использует MRR как denominator
- [ARR](./arr.md) = MRR × 12
```

Больше примеров по доменам: [references/examples.md](references/examples.md).

### 3. Связать concept cross-links

Использовать стандартные Markdown-ссылки. Есть две формы:

- **Absolute** (bundle-relative, начинается с `/`): `[customers](/tables/customers.md)` - **предпочтительно**, потому что стабильно при переносе файлов.
- **Relative**: `[churn](./churn.md)`.

Ссылки утверждают наличие отношений. Тип отношения передается окружающим текстом, а не синтаксисом ссылки. Broken links явно разрешены: они представляют знание, которое еще не написано.

### 4. Сгенерировать index.md

Размещать в любой директории для progressive disclosure. Frontmatter нет. Формат:

```markdown
# Metrics

- [MRR](./mrr.md) - ежемесячная повторяющаяся revenue
- [Churn](./churn.md) - monthly churn rate
- [NPS](./nps.md) - Net Promoter Score
```

Entries SHOULD включать `description` из frontmatter связанного concept. Отсутствие description не ломает conformance.

### 5. Сгенерировать log.md (опционально)

Хронологическая история изменений, новые записи сверху, headings с ISO 8601 dates:

```markdown
# Update Log

## 2026-06-13
- **Creation**: Добавлены metrics MRR, Churn и NPS.
- **Creation**: Создана directory structure.

## 2026-06-10
- **Initialization**: Bundle создан.
```

Первое жирное слово (`**Update**`, `**Creation**`, `**Deprecation**`) - конвенция, а не требование.

### 6. Объявить версию (опционально)

Bundle-root `index.md` может содержать frontmatter с версией спецификации:

```markdown
---
okf_version: "0.1"
---

# My Knowledge Bundle

- [Tables](./tables/) - database tables
- [Metrics](./metrics/) - business KPIs
```

Это единственное место, где frontmatter разрешен в `index.md`.

### 7. Распространение

Bundle можно распространять как:

- **Git repository** - recommended, потому что есть history, attribution и diffs.
- Tarball или zip archive.
- Поддиректорию внутри larger repository.

### 8. Проверить conformance

Минимальный gate - базовый `okf validate`. Для review-pass включать advisory
режимы `--strict`, `--check-links` и `--check-orphans`.

Base conformance должен пройти полностью: UTF-8, frontmatter block у concepts,
непустой string `type`, reserved `index.md`/`log.md`, forward compatibility.
Warnings/info из advisory-режимов не делают bundle non-conformant. Исключение:
`--check-orphans` допускает пустой non-root local `index.md` как orphan coverage
surface и сообщает orphan warnings.

---

## Проверка bundle

Когда просят validate, проверить conformance rules через CLI. Типичный успешный output:

```text
Validating bundle: <bundle>

---
Scanned 12 files.
Result: PASS (0 errors, 1 warnings, 2 info)
```

Для machine check использовать OKF CLI:

```sh
go run github.com/skosovsky/okf/cmd/okf@latest validate -path <bundle>
go run github.com/skosovsky/okf/cmd/okf@latest validate -path <bundle> --strict --check-links --check-orphans
```

### Severity levels

- `[ERROR]` - hard conformance failure. CLI returns non-zero exit status.
- `[WARN]` - soft guidance issue. Bundle can still be conformant.
- `[INFO]` - informational finding, including broken links that represent knowledge gaps.

Hard error examples:

- Markdown file is not valid UTF-8;
- non-reserved `.md` file has no parseable YAML frontmatter;
- frontmatter has no non-empty string `type`;
- reserved `index.md` or `log.md` violates the structure from SPEC;
- nested `index.md` declares `okf_version`, or root `index.md` has extra frontmatter keys.

Warning/info examples:

- missing recommended `title`, `description`, `tags` or `timestamp`;
- present but invalid `resource`;
- `tags` is not a YAML list of strings;
- `timestamp` is not RFC3339;
- citation markers without a bottom `# Citations` section;
- citation entry target is not a valid URI, bundle-absolute path, or `references/` path;
- `# Examples` without concrete example content;
- BigQuery Table concept without `# Schema`;
- `index.md` entry description differs from target concept `description`;
- broken cross-link target file when `--check-links` is enabled;
- missing anchor when `--check-links` is enabled;
- orphan concept file or missing local `index.md` when `--check-orphans` is enabled.

Consumers MUST NOT reject a bundle because of missing optional fields, unknown type values, unknown frontmatter keys, broken links, orphan warnings, or missing index files.

Hard conformance проверяет только:

1. Markdown files имеют valid UTF-8.
2. Non-reserved `.md` files имеют parseable YAML frontmatter block at file start.
3. Frontmatter содержит непустой string `type`.
4. Reserved files `index.md` и `log.md` соответствуют структуре из SPEC, когда присутствуют.
5. Forward compatibility: unknown frontmatter keys, unknown `type` values и будущие `okf_version` values не считаются ошибками.

Exception: with `--check-orphans`, an empty non-root local `index.md` is tolerated as an orphan coverage surface and reports orphan warnings instead of a base empty-index error.

Optional modes:

1. `--strict` - warnings for recommended fields, metadata types, RFC3339 timestamps, citation structure/targets, examples, BigQuery schema, and `index.md` description mismatch.
2. `--check-links` - info/warnings for internal link targets and anchors.
3. `--check-orphans` - warnings for concept files not listed in local `index.md`; info when local `index.md` is missing.

Soft guidance может давать warnings/info, но не делает bundle non-conformant:

- отсутствуют `title`, `description`, `tags`, `timestamp`;
- `resource` присутствует, но не является валидным URI;
- `tags` или `timestamp` имеют неподходящий machine-readable формат;
- conventional body sections выглядят неполно;
- broken cross-links;
- отсутствует локальный `index.md`;
- concept file не включен в локальный `index.md`.

При редактировании сохранять unknown frontmatter keys и не нормализовать type values в закрытый enum.

Не добавлять `resource` только ради прохождения `okf validate`: поле полезно только когда у concept есть настоящий physical URI. Для abstract concepts, playbooks, policies и mental models отсутствие `resource` нормально.

Semantic/editorial checks не пытаться выдавать за результат `okf validate`:

1. Claims без citation markers.
2. Репрезентативность и осмысленность `type`.
3. Стиль writing/structural markdown.
4. Генерация недостающего контента.
5. Автоматическое исправление links.

---

## Обогащение concepts

Когда у пользователя уже есть OKF concepts, которые нужно enriched:

### Добавить schema section

Для data assets добавить `# Schema` с таблицей columns:

```markdown
# Schema

| Column | Type | Description |
|--------|------|-------------|
| `order_id` | STRING | Уникальный identifier |
| `customer_id` | STRING | FK to [customers](/tables/customers.md) |
```

### Добавить examples section

Для APIs, queries или tools добавить `# Examples` с fenced code blocks, показывающими usage.

### Добавить citations

Когда claims ссылаются на external sources, добавить внизу `# Citations`, numbered:

```markdown
# Citations

[1] [Official docs](https://example.com/docs)
[2] [Internal runbook](https://wiki.internal/quality)
```

Citations могут быть absolute URLs, bundle-relative paths или путями в поддиректорию `references/`.

### Добавить cross-links

Вплетать links в естественный текст. Не создавать отдельный раздел "links": выражать отношения там, где они имеют смысл.

### Заполнить recommended fields

Если отсутствуют `title`, `description`, `tags` или `timestamp`, добавлять их только при наличии источника или надежного вывода из body/source system. `resource` добавлять только когда у concept действительно есть физический URI. Не добавлять пустые или угаданные значения.

### Reference workflow для enrichment

Официальный enrichment agent следует такому паттерну - применять ту же логику вручную:

1. Начать с metadata-only docs: только frontmatter + minimal body.
2. Добавить schema/structure из source system.
3. Добавить citations из authoritative documentation.
4. Вплести Markdown cross-links для навигации и YAML `relations` для строгих dependencies: FKs, shared fields, join paths, impact-analysis edges.
5. Сгенерировать `index.md` files для progressive disclosure.

---

## Конвертация sources в OKF

Подробные conversion guides: [references/conversion.md](references/conversion.md).

### Quick rules

**Notion export:** Properties -> frontmatter. Удалить UUID suffixes из filenames. Конвертировать Notion links -> relative Markdown links.

**Obsidian vault:** Конвертировать `[[wikilinks]]` -> `[title](./file.md)`. Убедиться, что поле `type` существует. Перенести inline `#tags` в frontmatter.

**CSV/spreadsheet:** Каждая row = один concept. Columns мапятся в frontmatter fields. First column = filename.

---

## Чтение OKF bundle агентом

Когда пользователь просит разобраться в existing bundle:

1. Начать с root `index.md`, если он есть; если нет - перечислить верхний уровень директории.
2. Читать directory `index.md` перед отдельными concepts, чтобы сохранить progressive disclosure.
3. Открывать concepts по задаче, а не сканировать весь bundle без необходимости.
4. Для impact analysis сначала читать YAML `relations`; они задают typed contract edges и field-level traceability.
5. Markdown links использовать как untyped navigation/context layer; surrounding prose может пояснять ссылку, но не заменяет `relations`.
6. Проверять citations только когда пользователь спрашивает об evidence или claim correctness.
7. Broken links и missing semantic targets трактовать как knowledge gaps, а не как fatal parse errors.
8. Если `okf_version` неизвестен, делать best-effort consumption и явно назвать риск версии.

---

## Версионирование

Текущая спецификация - OKF `0.1` draft. Bundle MAY объявлять версию через `okf_version: "0.1"` во frontmatter только root `index.md`.

Будущие версии ожидаются в формате `<major>.<minor>`:

- minor - backward-compatible additions, например optional fields или conventional headings;
- major - breaking changes, например переименование required fields или reserved filenames.

Если agent видит неподдерживаемую версию, не отказывать автоматически: попытаться прочитать bundle best-effort, затем явно перечислить неизвестные/рискованные части.

---

## Связь с другими форматами

Позиционировать OKF как specified, portable Markdown+frontmatter convention:

- ближе всего к LLM wiki repositories, personal knowledge bases и metadata-as-code;
- не заменяет Avro, Protobuf, OpenAPI и domain schemas, а ссылается на них через `resource`, body и citations;
- не задает storage/query/serving infrastructure;
- не задает fixed taxonomy concept types.

---

## Guardrails

1. **НИКОГДА не выдумывать data.** Если неизвестен корректный `type`, спросить. Если нет schema info, не добавлять ее. Без fabricated URLs или column names.
2. **Сохранять unknown fields.** OKF явно разрешает extension. Не удалять поля, которые не распознаны.
3. **Не навязывать taxonomy.** Type values - free-form strings. Можно предложить descriptive values, но нельзя reject bundle из-за unexpected types.
4. **Broken links are OK.** Спецификация явно разрешает их: они обозначают еще не написанные знания.
5. **Minimal by default.** Генерировать только `type` (required) + warranted recommended fields. Не набивать пустыми values.
6. **Ask before assuming.** Если домен неясен, спросить, какие types и structure имеют смысл.
7. **Не путать SPEC и ecosystem tooling.** Интеграции конкретных vendors или catalogs могут быть полезны, но не являются частью OKF conformance, пока это не сказано в SPEC.

---

## Формат ответа

Когда создается или существенно меняется bundle, показывать результат так:

1. **Directory tree** с полной структурой.
2. **Content каждого файла** в fenced code blocks.
3. **Conformance check** через `go run github.com/skosovsky/okf/cmd/okf@latest validate`, подтверждающий hard conformance и отдельно перечисляющий warnings/info.

```text
saas-metrics/
├── index.md
├── log.md
├── mrr.md
├── churn.md
└── nps.md
```

Затем показать каждый файл, затем подтвердить: "Bundle is OKF v0.1 conformant" только если CLI или эквивалентная проверка hard rules это доказали.

Когда пользователь просит консультацию, отвечать короче:

1. Прямой ответ.
2. Relevant SPEC rule.
3. Практический пример или command, если применимо.
4. Риски/gaps, если данных недостаточно.
