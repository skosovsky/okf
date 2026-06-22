# Open Knowledge Format (OKF)

**Version 0.1 - Draft**

OKF - открытый, удобный для людей и агентов формат представления *знаний*: metadata, context и curated insight вокруг данных и систем. Он рассчитан на то, что его пишут люди, генерируют агенты, обменивают между организациями и потребляют обе стороны.

Формат намеренно минимален: директория Markdown-файлов с YAML frontmatter. Нет schema registry, центрального authority и обязательного tooling. Если файл можно прочитать через `cat`, OKF можно прочитать. Если repo можно получить через `git clone`, OKF можно доставить.

---

## 1. Motivation

Пространство knowledge representation для AI agents быстро развивается, и появляется много несовместимых conventions. OKF исходит из позиции, что знания лучше всего представлять в широко доступных, устоявшихся форматах, которые:

- **Readable** людьми без tooling.
- **Parseable** агентами без bespoke SDK.
- **Diffable** в version control.
- **Portable** между tools, organizations и временем.

Формат минимально opinionated. Он стандартизирует только небольшой набор structural conventions, нужных для того, чтобы knowledge corpus был *self-describing*. Все остальное остается на усмотрение producer.

### Goals

1. Определить universal format, в который могут писать **enrichment agents**.
2. Описать, как **consumption agents** должны читать и обходить этот формат.
3. Облегчить **exchange** знаний между systems и organizations.
4. Стандартизировать небольшой набор **required** fields, которые должны присутствовать, чтобы content можно было meaningful consume.

### Non-goals

- Определять fixed taxonomy concept types.
- Предписывать storage, serving или query infrastructure.
- Заменять domain-specific schemas (Avro, Protobuf, OpenAPI и т.д.) - OKF *ссылается* на них, но не поглощает их.

---

## 2. Terminology

- **Knowledge Bundle** - self-contained hierarchical collection of knowledge documents. Единица распространения.
- **Concept** - single unit of knowledge within a bundle. Представлен одним Markdown document. Может описывать tangible asset (table, API), abstract idea (metric, business process) или что-то между ними.
- **Concept ID** - путь к concept file внутри bundle без суффикса `.md`. Например, `tables/users.md` имеет concept ID `tables/users`.
- **Frontmatter** - YAML metadata block, ограниченный `---` в начале Markdown-файла.
- **Body** - все в файле после frontmatter.
- **Link** - стандартная Markdown-ссылка от одного concept к другому, используемая для выражения relationships за пределами implicit parent/child hierarchy.
- **Citation** - ссылка от concept к external source, который подтверждает claim в body.

---

## 3. Bundle Structure

Bundle - это дерево директорий из Markdown-файлов. Directory structure независима от домена: producers организуют concepts так, как имеет смысл для фиксируемых знаний.

```text
path/to/bundle/
├── index.md                      # Optional. Directory listing for progressive disclosure.
├── log.md                        # Optional. Chronological history of updates.
├── <concept>.md                  # A concept at the bundle root.
└── <subdirectory>/               # Subdirectories organize concepts into groups.
    ├── index.md
    ├── <concept>.md
    └── <subdirectory>/
        └── ...
```

Bundle MAY be distributed as:

- Git repository (recommended: history, attribution, diffs).
- Tarball или zip archive директории.
- Subdirectory within a larger repository.

### 3.1 Reserved filenames

Следующие filenames имеют defined meaning на любом уровне hierarchy и MUST NOT использоваться для concept documents:

| Filename | Purpose |
|----------|---------|
| `index.md` | Directory listing. См. §6. |
| `log.md` | Update history. См. §7. |

Все остальные `.md` files являются concept documents.

Tags themselves remain a first-class concept - см. поле `tags` frontmatter в §4.1. OKF не задает отдельный file format для агрегации documents по tag; producers, которым нужен tag-browsing view, могут синтезировать его на стороне consumption, просканировав frontmatter.

---

## 4. Concept Documents

Каждый concept - UTF-8 Markdown file. У него две части:

1. **YAML frontmatter block**, ограниченный `---` на отдельной строке в начале файла и закрывающим `---` на отдельной строке.
2. **Markdown body** со свободным content.

### 4.1 Frontmatter

```yaml
---
type: <Type name>                  # REQUIRED
title: <Optional display name>
description: <Optional one-line summary>
resource: <Optional canonical URI for the underlying asset>
tags: [<tag>, <tag>, ...]          # Optional
timestamp: <ISO 8601 datetime>     # Optional last-modified time
# ... other producer-defined key/value pairs
---
```

**Required:**

- `type` - short string, определяющая kind of concept. Consumers используют ее для routing, filtering и presentation. Example values: `BigQuery Table`, `BigQuery Dataset`, `API Endpoint`, `Metric`, `Playbook`, `Reference`.

  Type values **are not** registered centrally. Producers SHOULD pick values that are descriptive and self-explanatory; consumers MUST tolerate unknown types gracefully, usually by treating them as generic concepts.

**Recommended (в порядке priority):**

- `title` - human-readable display name. Если отсутствует, consumers MAY derive title from filename.
- `description` - one sentence summarizing the concept. Используется генераторами `index.md`, search snippets и previews.
- `resource` - URI, uniquely identifying the underlying asset described by concept. Отсутствует для concepts, описывающих abstract ideas rather than physical resources.
- `tags` - YAML list of short strings for cross-cutting categorization.
- `timestamp` - ISO 8601 datetime последнего meaningful change.

**Extensions:** Producers MAY include any additional keys. Consumers SHOULD preserve unknown keys when round-tripping and SHOULD NOT reject documents with unrecognized fields.

### 4.2 Body

Body - standard Markdown. Producers SHOULD favor structural Markdown: headings, lists, tables, fenced code blocks, а не freeform prose, потому что структура помогает и human reading, и agent retrieval.

Required body sections отсутствуют. Следующие section headings имеют **conventional** meaning и SHOULD использоваться, когда применимы:

| Heading | Purpose |
|---------|---------|
| `# Schema` | Structured description of an asset's columns/fields. |
| `# Examples` | Concrete usage examples, often as fenced code blocks. |
| `# Citations` | External sources backing claims in the body. См. §8. |

### 4.3 Example: concept bound to a resource

```markdown
---
type: BigQuery Table
title: Customer Orders
description: One row per completed customer order across all channels.
resource: https://console.cloud.google.com/bigquery?p=acme&d=sales&t=orders
tags: [sales, orders, revenue]
timestamp: 2026-05-28T14:30:00Z
---

# Schema

| Column | Type | Description |
|--------|------|-------------|
| `order_id` | STRING | Globally unique order identifier. |
| `customer_id` | STRING | Foreign key into [customers](/tables/customers.md). |
| `total_usd` | NUMERIC | Order total in US dollars. |
| `placed_at` | TIMESTAMP | When the customer submitted the order. |

# Joins

Joined with [customers](/tables/customers.md) on `customer_id`.

# Citations

[1] [BigQuery table schema](https://console.cloud.google.com/bigquery?p=acme&d=sales&t=orders)
```

### 4.4 Example: concept not bound to a resource

```markdown
---
type: Playbook
title: Incident response - data freshness alert
description: Steps to triage a freshness alert on the orders pipeline.
tags: [oncall, incident]
timestamp: 2026-04-12T09:00:00Z
---

# Trigger

A freshness alert fires when `orders` lags more than 30 minutes behind
its expected SLA. See the [orders table](/tables/orders.md).

# Steps

1. Check the [ingestion job dashboard](https://example.com/dash).
2. ...
```

---

## 5. Cross-linking

Concepts MAY link to other concepts using standard Markdown links. Поддерживаются две формы:

### 5.1 Absolute (bundle-relative) links

Начинаются с `/`, интерпретируются относительно bundle root.

```markdown
See the [customers table](/tables/customers.md) for the join key.
```

Это **recommended** form, потому что она стабильна при переносе documents внутри subdirectory.

### 5.2 Relative links

Обычные Markdown relative paths.

```markdown
See the [neighboring concept](./other.md).
```

### 5.3 Link semantics

Markdown link from concept A to concept B is a human navigation/context edge.
Consumers, строящие graph view, SHOULD treat Markdown links as directed
`okf:references` edges. Do not infer typed dependencies such as `depends_on`,
`writes_to` или `joins_to` from surrounding prose; use YAML `relations` for
typed semantic edges.

Consumers MUST tolerate broken links. Link, target которого отсутствует в bundle, не malformed; он может просто представлять not-yet-written knowledge.

### 5.4 Semantic relations

Markdown links are the human navigation layer. For strict semantic dependencies,
producers MAY add a YAML `relations` block in frontmatter. Relation targets use
OKF concept refs, not Markdown paths:

```yaml
type: API Endpoint
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

- `target` is `<concept-id>[#<fragment>]`, for example `tables/orders#col-status`.
- The concept id must match the bundle concept id exactly: no leading `/`, `./`, `../`, `.md` suffix, external URI scheme, empty path segment, or surrounding whitespace.
- Fragment is a literal subresource id: non-empty, no surrounding whitespace, no `#`, and no ASCII control characters.
- Invalid refs include `/tables/orders.md`, `tables/orders.md`, `#local-section`, `https://example.com/orders`, `urn:orders`, `tables/orders#`, `tables/orders#col#status`, and `tables/orders# col-status`.
- Nested field-level sources require explicit `id` or `anchor`. Display `name` is not a stable anchor.
- Missing semantic targets are preserved in graph export and do not make a bundle non-conformant.
- `validate --check-links` checks Markdown links only; semantic relation validation is policy/tooling-specific.

---

## 6. Index Files

`index.md` MAY appear in any directory, including bundle root. Он enumerates directory contents для **progressive disclosure**: позволяет человеку или агенту увидеть, что доступно, до открытия отдельных documents.

Index files contain no frontmatter. Body uses one or more sections, each grouping concepts under a heading:

```markdown
# Section / Group Heading

* [Title 1](relative-url-1) - short description of item 1
* [Title 2](relative-url-2) - short description of item 2

# Another Section

* [Subdirectory](subdir/) - short description of the subdirectory
```

Entries SHOULD include the description from the linked concept's frontmatter. Producers MAY generate `index.md` automatically; consumers MAY synthesize one on the fly when none is present.

---

## 7. Log Files (optional)

`log.md` MAY appear at any level of hierarchy to record history of changes to that scope. Формат - flat list of date-grouped entries, newest first:

```markdown
# Directory Update Log

## 2026-05-22
* **Update**: Added new BigQuery table reference for [Customer Metrics](/tables/customer-metrics.md).
* **Creation**: Established the [Dataplex Playbook](/playbooks/dataplex.md).

## 2026-05-15
* **Initialization**: Created foundational directory structure.
* **Update**: Added progressive-disclosure guidelines to the root [index](/index.md).
```

Date headings MUST use ISO 8601 `YYYY-MM-DD` form. Log entries are prose; leading bold word (`**Update**`, `**Creation**`, `**Deprecation**` и т.д.) is a convention, not a requirement.

---

## 8. Citations

When a concept's body makes claims sourced from external material, those sources SHOULD be listed under a `# Citations` heading at the bottom of the document, numbered:

```markdown
# Citations

[1] [BigQuery public dataset announcement](https://cloud.google.com/blog/products/data-analytics/...)
[2] [Internal data quality runbook](https://wiki.acme.internal/data/quality)
```

Citation links MAY be absolute URLs, bundle-relative paths, or paths into a `references/` subdirectory that mirrors external material as first-class OKF concepts.

---

## 9. Conformance

Bundle is **conformant** with OKF v0.1 if:

1. Every non-reserved `.md` file in the tree contains a parseable YAML frontmatter block.
2. Every frontmatter block contains a non-empty `type` field.
3. Every reserved filename (`index.md`, `log.md`) follows the structure described in §6 and §7 respectively when present.

Consumers SHOULD treat all other constraints as soft guidance. In particular, consumers MUST NOT reject a bundle because of:

- Missing optional frontmatter fields.
- Unknown `type` values.
- Unknown additional frontmatter keys.
- Broken cross-links.
- Missing `index.md` files.

This permissive consumption model is intentional: OKF is meant to remain useful as bundles grow, get refactored, and are partially generated by agents.

---

## 10. Relationship to other formats

OKF is intentionally close to several established patterns:

- **LLM "wiki" repositories**, which use Markdown + frontmatter as agent-readable knowledge bases.
- **Personal knowledge tools** like Obsidian and Notion, which use hierarchical Markdown with cross-links.
- **"Metadata as code"** approaches, which store catalog metadata alongside source code rather than in a separate registry.

OKF differs primarily in being **specified**: it pins down the small set of rules needed for interoperability without dictating tooling.

---

## 11. Versioning

This document specifies OKF version **0.1**. Future revisions will be versioned in the form `<major>.<minor>`:

- **minor** version bump introduces backward-compatible additions: new optional fields, new conventional section headings.
- **major** version bump may make breaking changes: renaming required fields, changing reserved filenames.

Bundles MAY declare the OKF version they target by including `okf_version: "0.1"` in a bundle-root `index.md` frontmatter block, the only place frontmatter is permitted in an `index.md`. Consumers that do not understand the declared version SHOULD attempt best-effort consumption rather than refusing the bundle.

---

## Appendix A - Minimal example bundle

```text
my_bundle/
├── index.md
├── datasets/
│   ├── index.md
│   └── sales.md
└── tables/
    ├── index.md
    ├── orders.md
    └── customers.md
```

`datasets/sales.md`:

```markdown
---
type: BigQuery Dataset
title: Sales
description: All sales-related tables for the retail business.
resource: https://console.cloud.google.com/bigquery?p=acme&d=sales
tags: [sales]
timestamp: 2026-05-28T00:00:00Z
---

The sales dataset contains transactional tables, including
[orders](/tables/orders.md) and [customers](/tables/customers.md).
```

`tables/orders.md`:

```markdown
---
type: BigQuery Table
title: Orders
description: One row per completed customer order.
resource: https://console.cloud.google.com/bigquery?p=acme&d=sales&t=orders
tags: [sales, orders]
timestamp: 2026-05-28T00:00:00Z
---

# Schema

| Column | Type | Description |
|--------|------|-------------|
| `order_id` | STRING | Unique order identifier. |
| `customer_id` | STRING | FK to [customers](/tables/customers.md). |
| `total_usd` | NUMERIC | Order total in USD. |

Part of the [sales dataset](/datasets/sales.md).
```
