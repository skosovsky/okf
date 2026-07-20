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
- Работать с локальными bundles через `okf-mcp`, если host поддерживает MCP tools.
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

## Процесс работы с MCP server

Skill остается одним skill в `skills/open-knowledge-format/SKILL.md`. Если
agent host поддерживает MCP, настрой отдельную stdio server command:

```sh
go run ./cmd/okf-mcp
```

или установи binary:

```sh
go install github.com/skosovsky/okf/cmd/okf-mcp@latest
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

`stdout` зарезервирован под MCP JSON-RPC protocol; diagnostics идут в `stderr`.

Сервер предоставляет `list_concepts`, `read_concept`, `validate_bundle`,
`get_semantic_graph` и `write_concept`. Все tools требуют absolute
`bundle_path`; concept tools требуют canonical concept id без `.md`.
`write_concept` проверяет in-memory staged bundle со strict/link/orphan
validation, затем публикует через bundle-wide advisory lease, fresh-revision
CAS, Journal v5/recovery, cleanup и receipt. Journal v5 использует compact
bounded manifest, который фиксирует algorithm и canonical request/result/replay
data, с отдельно durable staged payloads; перед apply recovery проверяет safe
no-follow path, declared size и SHA-256 digest. Persisted receipt envelope
использует v2. При conflict он максимум два раза
заново планирует и валидирует изменения; rejected writes возвращают diagnostics
и не меняют bundle. Lease advisory: raw editors не координируются, а raw readers
могут увидеть non-atomic multi-file rename во время публикации.
Durable backend `store/fs` имеет runtime contract только для Darwin/Linux. На
Windows, Android, iOS и других targets он compile-safe, а
`Open`/`OpenContext` возвращают `fs.ErrUnsupportedPlatform`; не заявляй там
durable filesystem support.
Revisions по умолчанию используют `sha256:<lowercase-hex>`, но
`fs.Config.HashAlgorithm` может заменить алгоритм; Journal v5 фиксирует его,
поэтому recovery требует ту же configured algorithm.
Durable staged payloads ограничены `fs.Config`: по умолчанию 256 MiB на payload
и 1 GiB на transaction; recovery отклоняет oversized manifest до allocation.
Revision-visible set —
каждый regular file под bundle root, включая non-Markdown и reserved index/log
files, кроме `.okf/**`; symlinks никогда не читаются и не хешируются, а internal
journal, receipts и lease исключены. Перед изменениями получать контекст через
`get_semantic_graph` и `read_concept`; проверять через `validate_bundle`;
редактировать concepts через `write_concept`, не обходя MCP прямыми filesystem
writes, когда MCP доступен. Идентичный retry использует тот же server-side
idempotent pipeline без повторной публикации; не добавлять transport-only поля
для повтора. MCP success содержит только `status`, `path` и `diagnostics`, без
receipt DTO или commit evidence.

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

Не выводи anchors из display `name`. `okf validate` и MCP `validate_bundle`
проверяют base conformance v0.1; `--check-links` добавляет только проверку
Markdown links. Go-клиент может включить semantic relation reporting через
`ValidatorConfig.CheckRelations`; mutation и write paths всегда отклоняют
blocking relation diagnostics. Noncanonical anchor aliases остаются
informational. Semantic failures
исключаются из resolved outgoing, incoming и reverse indexes, а также из всех
semantic graph exporters. Dangling Markdown links — отдельный навигационный
слой и могут по-прежнему отображаться как missing.

Fragments определяют только nested mapping; top-level frontmatter `id` и
`anchor` — metadata concept. Для nested mapping frontmatter `id` — canonical. Валидный отличающийся
`anchor` — noncanonical alias для информации и навигации; semantic mutations и
relation refs должны адресовать canonical `id`.

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

## Безопасные semantic edits

Skill обязан считать rejected semantic mutation результатом без записи. Go
mutation path один раз валидирует staged source, использует Goldmark и exact
source spans для eligible Markdown body destinations и `yaml.v3` только для
поддерживаемого lossless frontmatter subset. Autolinks и raw HTML не
переписываются; invalid UTF-8, unsupported и ambiguous presentation fail-closed.
Это не добавляет MCP fields или CLI flags. `bundle.SourceFromFS` — non-owning
adapter, которому нужен stable snapshot `fs.FS`; `bundle.Source` остается
контрактом.

Граница строгая: parsing идёт только по body, offsets отображаются в полный
файл. Изменяться могут лишь Goldmark AST inline links/images и reference
definitions; semantic link/image внутри inline-HTML container остаётся eligible,
если Goldmark создаёт `Link`/`Image`. Autolinks, raw-HTML `href`/URLs, code
spans, fenced/indented code и unresolved/malformed references не
переписываются. Touched YAML принимает доказанные plain/single/double-quoted
scalar keys и values и fail-closed для flow mapping/sequence, literal/folded
block scalar, explicit/custom tag, direct anchor или complex key (Unsupported);
alias/merge provenance и duplicate semantic relations/type/target/id/anchor
(Ambiguous); остальные duplicate touched mapping keys (Unsupported);
`%YAML`/`%TAG`, inner
document/end markers или multidoc frontmatter и
unprovable comment/range; unrelated nonintersecting extension bytes могут
остаться. Invalid UTF-8 и все unsupported/ambiguous случаи — typed error
(`errors.Is(..., mutation.ErrUnsupportedPresentation)` /
`mutation.ErrAmbiguousPresentation`) без
stage. YAML задают только recognized outer frontmatter delimiters; body
thematic `---` и setext underline — Markdown, не YAML multi-doc.

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

Filesystem store: `MaxStagedFiles` по умолчанию и максимум 100 000 (`payload-00000`…`payload-99999`). Case-folding и Unicode-normalization aliases определяются независимо. `.okf` directories no-follow 0700, private files и lease 0600 либо fail-closed.
