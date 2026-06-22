# Конвертация источников в OKF

Гайды по преобразованию существующих знаний в conformant OKF bundles.

---

## Из экспорта Notion

Notion экспортирует Markdown с properties в YAML-like формате.

### Шаги

1. **Экспортировать** из Notion в Markdown & CSV.
2. **Очистить filenames** - удалить UUID suffixes (`Page Name abc123def.md` -> `page-name.md`).
3. **Сопоставить properties с frontmatter:**

| Notion property | OKF field |
|-----------------|-----------|
| Type (select) | `type` (required) |
| Name | `title` |
| Tags (multi-select) | `tags` |
| Last Edited | `timestamp` |
| URL | `resource` |

4. **Конвертировать links** - Notion использует `[Page Name](Page%20Name%20abc123def.md)`. Преобразовать в чистые relative paths: `[Page Name](./page-name.md)`.
5. **Удалить Notion artifacts** - empty toggle blocks, breadcrumb headers, cover image references.
6. **Добавить missing `type` field** - если в Notion не было property "Type", спросить пользователя, какой type назначить.

### Edge cases

- Notion databases: каждая row становится concept. Название database становится именем directory.
- Nested pages: сохранять hierarchy. Child pages помещать в subdirectories.
- Inline databases: разворачивать в список внутри body родительского concept.
- Notion formulas/rollups: удалять; они не переводятся в static Markdown.

---

## Из Obsidian vault

Obsidian vaults уже близки к OKF. Главные отличия: wikilinks и потенциально отсутствующее поле `type`.

### Шаги

1. **Конвертировать wikilinks в standard links:**
   - `[[Note Name]]` -> `[Note Name](./note-name.md)`
   - `[[Note Name|Display Text]]` -> `[Display Text](./note-name.md)`
   - `[[Note Name#Heading]]` -> `[Note Name](./note-name.md#heading)`

2. **Убедиться, что `type` field существует** в каждом frontmatter block. Типовые mappings:

| Obsidian pattern | Suggested OKF type |
|------------------|--------------------|
| Daily notes | `Log` |
| MOC / index note | Конвертировать в `index.md` (reserved file) |
| Permanent notes | `Reference` |
| Literature notes | `Reference` |
| Project notes | `Playbook` или domain-specific type |

3. **Конвертировать tags:**
   - Inline `#tag` -> перенести во frontmatter `tags: [tag]`.
   - Nested `#parent/child` -> развернуть в `tags: [parent, child]` или оставить как `parent/child`.

4. **Обработать embeds:**
   - `![[Note]]` - заменить обычной ссылкой или inline content.
   - `![[image.png]]` - оставить как стандартное Markdown image `![](./image.png)`.

5. **Удалить Obsidian-specific syntax:**
   - `%%comments%%` -> удалить.
   - `> [!callout]` -> преобразовать в blockquote или heading.
   - Dataview queries -> удалить: они dynamic, not portable.

### Что оставить как есть

- Standard Markdown formatting: headings, lists, tables, code blocks.
- Existing YAML frontmatter: только добавить `type`, если он отсутствует.
- Standard Markdown links: уже OKF-compatible.
- Mermaid diagrams: standard Markdown fenced blocks.

---

## Из CSV / spreadsheet

Каждая row становится одним concept document.

### Шаги

1. **Определить column mapping:**

| Роль column | Maps to |
|-------------|---------|
| Primary identifier / name | Filename |
| Category / kind | `type` field |
| Short description | `description` field |
| Tags / labels | `tags` field |
| URL / link | `resource` field |
| Last modified date | `timestamp` field |
| Остальные columns | Body content в виде table или sections |

2. **Сгенерировать один `.md` per row:**

```markdown
---
type: {category_column}
title: {name_column}
description: {description_column}
tags: [{tag1}, {tag2}]
timestamp: {date_column}T00:00:00Z
---

# {name_column}

| Field | Value |
|-------|-------|
| Column3 | {value} |
| Column4 | {value} |
```

3. **Сгенерировать `index.md` по полному списку:**

```markdown
# {Sheet Name}

- [{row1_name}](./{row1_slug}.md) - {row1_description}
- [{row2_name}](./{row2_slug}.md) - {row2_description}
```

4. **Сгенерировать `log.md` с creation entry:**

```markdown
# Update Log

## {today_iso8601}
- **Creation**: Generated {N} concepts from spreadsheet import.
```

### Edge cases

- Empty cells: полностью пропускать field, не писать empty strings.
- Multi-value cells через запятую: парсить как YAML list для `tags`.
- Очень длинные text cells: переносить в body section, не во frontmatter.
- Duplicate names: добавлять disambiguator, например `widget-v1.md`, `widget-v2.md`.
