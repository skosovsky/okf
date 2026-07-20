# okf

`okf` - CLI-toolkit для создания, проверки, анализа и экспорта
[Open Knowledge Format (OKF) v0.1](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md),
открытого формата Google для представления знаний в виде дерева Markdown-файлов
с YAML frontmatter, удобного и людям, и агентам. Репозиторий также содержит
Go-библиотеку с минимальным числом зависимостей и portable agent skill для OKF
workflows.

Проект предоставляет четыре публичных способа использования:

1. CLI-toolkit `okf` для работы с OKF-bundle.
2. Go library packages `github.com/skosovsky/okf/bundle`, `github.com/skosovsky/okf/validator`, `github.com/skosovsky/okf/graph`, `github.com/skosovsky/okf/store` и `github.com/skosovsky/okf/store/fs` для встраивания OKF и транзакционных мутаций в Go-программы.
3. Stdio MCP server `okf-mcp` для agent clients, которым нужно через tools
   читать, проверять, строить graph и безопасно редактировать локальные
   OKF-bundle.
4. Agent skill `open-knowledge-format` для консультаций, создания, конвертации,
   обогащения, проверки и экспорта OKF-bundle.

English documentation: [README.md](README.md).

## Способ 1: Toolkit

Установка команды `okf`:

```sh
go install github.com/skosovsky/okf/cmd/okf@latest
```

Проверка:

```sh
okf help
okf version
```

Основные команды:

```sh
okf validate -path <bundle>       # Проверить базовый OKF v0.1 conformance
okf validate -path <bundle> --strict --check-links --check-orphans
okf info     <bundle>       # Показать сводку по bundle
okf index    <bundle>       # Регенерировать index.md files
okf graph    <bundle>       # Экспортировать Markdown links и YAML relations
okf graph    <bundle> -format mermaid
okf graph    <bundle> -format json-ld
okf graph    <bundle> -format ntriples
okf graph    <bundle> --dot # Напечатать Graphviz DOT
okf parse    <file>         # Показать parsed structure одного документа
okf fmt      <file>         # Нормализовать документ в stdout
okf fmt      <file> -w      # Перезаписать файл на месте
```

Форматы вывода graph:

- `okf graph <bundle>` печатает default text adjacency list.
- `okf graph <bundle> -format dot` печатает Graphviz DOT. `--dot` остается legacy alias для этого формата.
- `okf graph <bundle> -format mermaid` печатает Mermaid flowchart syntax (`graph LR`), который можно вставить в Markdown code fence на платформах с Mermaid-rendering. Битые internal links выводятся пунктирными ребрами с меткой `404`.
- `okf graph <bundle> -format json-ld` печатает JSON-LD документ с `@context` и `@graph` для graph tooling и agent harnesses. Каждый concept выводится как узел `bundle:<id>` с `@type: "okf:Concept"`. Internal links выводятся как объекты `okf:Reference` с `target` и `exists`, поэтому dangling internal links остаются видимыми как `"exists": false`.
- `okf graph <bundle> -format ntriples` печатает line-oriented RDF/N-Triples: один full-IRI факт на строку для bulk load, shell processing, RDF tooling и streaming graph pipelines.

Semantic relations добавляют второй слой graph. Markdown links остаются human
navigation и экспортируются как `okf:references`; YAML `relations` задают
строгие semantic dependencies для impact analysis:

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

Targets в `relations` - это OKF concept refs, а не Markdown paths: используй
`tables/orders#col-status`, а не `tables/orders.md#col-status`. Для nested
semantic sources нужен явный `id` или `anchor`; display `name` не
интерпретируется как anchor. Для target с fragment `exists` равно true, только
если существуют и concept, и сам fragment, причем fragment уникален.
Некорректные или неразрешенные semantic relations остаются structured
diagnostics с контекстом source, target и refs. CLI и MCP validation по
умолчанию покрывают только base conformance v0.1; Go-клиент может включить
relation policy через `ValidatorConfig.CheckRelations`. Mutation и write paths
всё равно отклоняют blocking relation diagnostics. Они исключаются из resolved
outgoing, incoming и reverse indexes, а также из всех semantic graph exporters.
Non-canonical anchor aliases остаются только informational. Это отдельный слой
от dangling Markdown links: они остаются навигационными данными и могут
отображаться как missing.

Grammar relation ref: `<concept-id>[#<fragment>]`. Concept id должен точно
совпадать с bundle concept id: без leading `/`, `./`, `../`, `.md` suffix,
external URI scheme, empty path segment и пробелов по краям. Fragment - literal
subresource id: непустой, без пробелов по краям, без `#` и без ASCII control
characters. Invalid examples: `/tables/orders.md`, `tables/orders.md`,
`#local-section`, `https://example.com/orders`, `urn:orders`, `tables/orders#`,
`tables/orders#col#status`, `tables/orders# col-status`.

```mermaid
graph LR
  n0["api/checkout"] -->|"depends_on"| n1["tables/orders#col-status"]
  n2["api/checkout#payload-user_id"] -->|"writes_to"| n3["tables/orders#col-customer_id"]
```

```json
{"@id":"bundle:api/checkout#payload-user_id","@type":"okf:SubResource","is_part_of":{"@id":"bundle:api/checkout"},"writes_to":[{"@id":"bundle:tables/orders#col-customer_id","exists":true}]}
```

```text
<local:bundle:api%2Fcheckout#payload-user_id> <https://okf.io/ontology/v0.1#writes_to> <local:bundle:tables%2Forders#col-customer_id> .
```

Для успешно разобранного validation invocation `okf validate` возвращает
non-zero exit status, если в bundle есть conformance errors. Поэтому команду
можно использовать напрямую в CI. CLI usage или flag errors тоже возвращают
`1`, но не печатают validation summary:

```sh
okf validate -path ./knowledge
```

Успешная проверка печатает детерминированные diagnostics и summary:

```text
Validating bundle: ./knowledge

---
Scanned 12 files.
Result: PASS (0 errors, 0 warnings, 0 info)
```

Findings перед summary печатаются с severity labels `[ERROR]`, `[WARN]` или
`[INFO]`.

### Режимы валидации

`okf validate` устроен слоями. Базовый слой conformance выполняется по
умолчанию; дополнительные флаги включают advisory-проверки для review workflows.

| Режим | Как включить | Diagnostics | Код выхода |
| --- | --- | --- | --- |
| Base conformance | по умолчанию | `[ERROR]` для hard OKF v0.1 violations | `1`, если есть хотя бы одна error |
| Strict guidance | `--strict` | `[WARN]` для recommended metadata и body conventions | остается `0`, если нет base errors |
| Link graph | `--check-links` | `[INFO]` для missing files, `[WARN]` для missing anchors | остается `0`, если нет base errors |
| Orphan coverage | `--check-orphans` | `[WARN]` для unlisted concepts, `[INFO]` для missing local indexes | остается `0`, если нет base errors |

Исключение: с `--check-orphans` пустой non-root local `index.md` трактуется как
поверхность orphan coverage и дает orphan warnings вместо empty-index structure
error.

Базовый слой проверяет UTF-8, frontmatter blocks у concepts, непустой string
`type`, структуру reserved `index.md` и `log.md`, а также
forward-compatible поведение для unknown frontmatter keys, unknown `type`
values и будущих `okf_version`.

`--strict` проверяет recommended fields `title`, `description`, `tags` и
`timestamp`; `tags` должен быть YAML list of strings, а `timestamp` должен
парситься как RFC3339. Также проверяются conventional `# Citations`,
`# Examples`, BigQuery `# Schema` и descriptions в `index.md`. Отсутствующий
`resource` намеренно не считается warning; если `resource` присутствует, он
должен быть валидным URI.

## Способ 2: Library

Добавление пакета в Go-модуль:

```sh
go get github.com/skosovsky/okf
```

Импорт:

```go
import (
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/validator"
)
```

Валидация bundle:

```go
b, err := bundle.LoadBundle("./knowledge")
if err != nil {
	return err
}

report := validator.ValidateBundle(b, &validator.ValidatorConfig{
	Strict:       true,
	CheckLinks:   true,
	CheckOrphans: true,
})
if !report.IsConformant() {
	for _, diagnostic := range report.Of(validator.SeverityError) {
		fmt.Println(diagnostic)
	}
}
```

Парсинг одного документа:

```go
doc, err := bundle.ParseDocument(input)
if err != nil {
	return err
}

title, _ := doc.Frontmatter.Title()
links := doc.Links()
citations := doc.Citations()
```

Регенерация индексов из Go:

```go
written, err := bundle.RegenerateIndexes("./knowledge")
if err != nil {
	return err
}

fmt.Println(written)
```

### Транзакционные мутации

`store` дает immutable snapshots, preview декларативных изменений и CAS commit.
`store/fs` — durable backend для одного filesystem.

```go
package example

import (
	"context"
	"errors"
	"fmt"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/store/fs"
)

func update(ctx context.Context) (err error) {
	s, err := fs.Open("./knowledge", fs.DefaultConfig())
	if err != nil { return err }
	defer func() { if closeErr := s.Close(); closeErr != nil && err == nil { err = closeErr } }()
	base, err := s.Snapshot(ctx)
	if err != nil { return err }
	source, err := bundle.ParseRelationRef("api/orders")
	if err != nil { return err }
	target, err := bundle.ParseRelationRef("tables/orders")
	if err != nil { return err }
	change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "add-order-dependency", Actor: "agent", BaseRevision: base.Revision(), Operations: []store.Operation{store.EnsureRelation{Source: source, Type: "depends_on", Target: target}}}
	preview, err := s.Preview(ctx, change)
	if err != nil { return err }
	_ = preview
	_, err = s.Commit(ctx, change, store.CommitOptions{IdempotencyKey: "request-42"})
	var conflict *store.Conflict
	if errors.As(err, &conflict) { return fmt.Errorf("refresh and retry from %s", conflict.Actual) }
	return err
}
```

Операции описывают desired state: `EnsureRelation` оставляет ровно одно
semantic edge; `MoveConcept` переносит concept и переписывает statically
resolvable canonical references; `RenameFragment` переименовывает один явный
уникальный fragment и входящие canonical references. Revision —
algorithm-qualified digest от canonical sorted manifest. По умолчанию это
`sha256:<lowercase-hex>`; `fs.Config.HashAlgorithm` может заменить алгоритм.
`fs.Config` также ограничивает durable staged payloads: по умолчанию 256 MiB
на payload и 1 GiB на transaction; recovery отклоняет превышающий лимиты
persisted manifest до чтения payload bytes.
Visible set ревизии — каждый regular file под bundle root, включая non-Markdown
files и reserved index/log files, кроме `.okf/**`. Symlinks никогда не читаются
и не хешируются; internal journal, receipts и lease исключены. Journal v5
фиксирует алгоритм и canonical request/result/replay data в compact bounded
manifest; durable staged payloads хранят post-state bytes. Перед
apply/recovery проверяются safe no-follow path, declared size и SHA-256 digest
каждого payload, затем очищаются journal и stage. Recovery требует ту же
configured algorithm. Persisted receipt envelope использует v2.
`Commit` сверяет `BaseRevision` под lease cooperating writers и при CAS mismatch
возвращает structured `*store.Conflict`. `Commit` возвращает
`store.CommitReceipt`, а `ReplaceConcept` возвращает его в result; оба replay
идентичный receipt для того же canonical request и idempotency key. По умолчанию
receipts хранятся 24 часа и всегда сохраняются
как минимум 1000 самых новых.

В fragment namespace входят только nested mapping: top-level frontmatter `id`
и `anchor` остаются metadata concept. Для nested mapping `id` — canonical
identity fragment. Отличающийся валидный
`anchor` — только noncanonical alias для информации и навигации; semantic
mutations и relation refs адресуют canonical `id`.

Filesystem backend намеренно ограничен: lease advisory, поэтому raw editors не
участвуют; raw readers могут увидеть multi-rename commit во время публикации.
Journal дает recovery, а не distributed isolation. Backend покрывает один
filesystem; для distributed deployment нужен другой `store.Store` backend.

### Lossless presentation contract

Mutation planner один раз загружает и валидирует staged source до записи:
invalid или unsupported edit никогда не stage'ится и не записывается. Для
Markdown Goldmark — semantic oracle, а отдельный collector вычисляет точные
byte spans. Парсится только Markdown body (body-only offsets отображаются в
полный файл); поддержаны inline links, images и каждое reference definition
ровно один раз. Autolinks, raw-HTML `href`/URLs, code spans, fenced или
indented code и unresolved или malformed references не переписываются. Semantic
Markdown link или image внутри inline-HTML container остаётся eligible, если
Goldmark создаёт AST `Link` или `Image`. Reference uses не дублируют span
definition; duplicate normalized definitions дают Ambiguous. Escaped/entity
source tokens сравниваются как semantic destinations, а replacement сохраняет
angle style либо безопасно переключается на escaped angle destination. Markdown
и YAML не re-render'ятся.

YAML mutations используют `yaml.v3` как semantic authority и поддерживают
только доказанный block-style subset touched scalar keys и values: plain,
single-quoted и double-quoted. Unsupported или ambiguous touched presentation
включает flow mapping/sequence; literal/folded block scalar; explicit/custom
tag; direct anchors или complex keys (Unsupported); alias/merge provenance и
duplicate semantic relations/type/target/id/anchor (Ambiguous); остальные
duplicate touched mapping keys (Unsupported); directives `%YAML`/`%TAG`; inner
document/end markers или multidoc внутри frontmatter; а также comments или
source ranges, которые нельзя доказать. Unrelated nonintersecting extension
bytes могут остаться lossless. Invalid UTF-8 и эти случаи возвращают typed
error, не создают stage и различимы через
`errors.Is(err, mutation.ErrUnsupportedPresentation)` или
`mutation.ErrAmbiguousPresentation`; операция fail-closed. Зависимостей goccy
и tree-sitter нет.

YAML document задают только recognized outer frontmatter delimiters. Thematic
`---` или setext underline в Markdown body — это Markdown, а не YAML multi-doc.

`mutation.Overlay` намеренно flat: clones shallow-share immutable staged
payloads, manifest — delta над base, public reads остаются defensive, а общий
cache `Paths` заполняется только после успешного enumeration. Parent chain и
HAMT отсутствуют. `bundle.SourceFromFS(fsys fs.FS)` — non-owning adapter;
передавай stable filesystem snapshot на весь срок `Paths`/`ReadFile`.
`bundle.Source` остается core loading contract. Эти детали не меняют CLI flags
или MCP wire schemas.

## Способ 3: MCP Server

Установка команды `okf-mcp`:

```sh
go install github.com/skosovsky/okf/cmd/okf-mcp@latest
```

Настрой MCP client на запуск `okf-mcp` через stdio. Сервер предоставляет tools:

```json
{
  "mcpServers": {
    "okf": {
      "command": "okf-mcp"
    }
  }
}
```

`stdout` зарезервирован под MCP JSON-RPC protocol. Diagnostics и startup errors
пишутся в `stderr`.

- Перед изменением bundle получить контекст через `get_semantic_graph` и, если
  нужно, `read_concept`.
- `list_concepts` - загрузить bundle и вернуть детерминированный список concepts.
- `read_concept` - прочитать Markdown одного concept по canonical concept id.
- `validate_bundle` - вернуть JSON validation report.
- `get_semantic_graph` - вернуть тот же JSON-LD graph, что и `okf graph -format json-ld`.
- `write_concept` - создать или обновить один concept через staged strict validation и durable cooperating-writer commit path.

Все tools требуют absolute `bundle_path`. Concept tools используют canonical OKF
concept ids вроде `tables/orders`, без leading slash и без suffix `.md`.
Read/write paths отклоняют symlinks внутри bundle path. `write_concept`
проверяет staged content через strict, link и orphan checks, затем commit'ит
через тот же lease, CAS, Journal v5, receipt, recovery и cleanup path, что и
`store/fs`. Он использует server-generated idempotency identity из canonical
write request, поэтому идентичный MCP retry не публикует повторно. MCP сохраняет
fixed success schema (`status`, `path`, `diagnostics`) и не раскрывает receipt
DTO или commit evidence.
Rejected writes не меняют файлы. Это координация только cooperating writers, без
distributed isolation от raw filesystem editors.

## Способ 4: Agent Skill

В репозитории есть универсальный русскоязычный skill:
`skills/open-knowledge-format`. Используй его, когда агенту нужно:

- объяснить OKF concepts и правила conformance;
- спроектировать новый OKF bundle;
- конвертировать Markdown, Notion, Obsidian, CSV или spreadsheet материалы в OKF;
- обогатить existing OKF concepts metadata, sections `# Schema` и `# Examples`,
  citations, cross-links, indexes и logs;
- проверить OKF bundle через OKF CLI из Go module;
- работать с локальным OKF bundle через `okf-mcp`, если host поддерживает MCP
  tools;
- извлечь graph output для impact analysis и agent harnesses.

Для runtime, который поддерживает local skills, зарегистрируй или скопируй
директорию `skills/open-knowledge-format` с именем skill
`open-knowledge-format`. Skill не привязан к конкретному provider/runtime:
внутри только portable Markdown-инструкции и references.

Для установки как Codex plugin из этого репозитория используй включенный plugin
manifest `.codex-plugin/plugin.json` и repo-local marketplace manifest
`.agents/plugins/marketplace.json`:

```sh
codex plugin marketplace add .
codex plugin add okf@okf-local
```

После установки открой новую Codex-сессию и попроси использовать
`$open-knowledge-format`.

Для установки как Claude Code plugin из GitHub используй включенный Claude
plugin manifest `.claude-plugin/plugin.json` и marketplace manifest
`.claude-plugin/marketplace.json`:

```text
/plugin marketplace add skosovsky/okf
/plugin install okf@okf
/reload-plugins
```

После установки вызови `/okf:open-knowledge-format` или дай Claude Code
использовать skill автоматически, когда задача связана с OKF.

Для quality gate используй команду из Go module:

```sh
go run github.com/skosovsky/okf/cmd/okf@latest validate -path <bundle>
```

Этот же CLI умеет печатать summary, генерировать index, экспортировать graph,
парсить и форматировать documents:

```sh
go run github.com/skosovsky/okf/cmd/okf@latest validate -path <bundle>
go run github.com/skosovsky/okf/cmd/okf@latest info <bundle>
go run github.com/skosovsky/okf/cmd/okf@latest index <bundle>
go run github.com/skosovsky/okf/cmd/okf@latest graph <bundle>
go run github.com/skosovsky/okf/cmd/okf@latest graph <bundle> -format mermaid
go run github.com/skosovsky/okf/cmd/okf@latest graph <bundle> -format json-ld
go run github.com/skosovsky/okf/cmd/okf@latest graph <bundle> -format ntriples
```

Если `okf` уже установлен, `okf validate -path <bundle>` эквивалентен.

## OKF-документ

```markdown
---
type: BigQuery Table
title: Orders
description: One row per completed customer order.
tags: [sales, orders]
timestamp: 2026-05-28T00:00:00Z
---

# Schema

Part of the [sales dataset](/datasets/sales.md).

# Citations

[1] [Runbook](https://example.com/runbook)
```

## Что поддерживается

- Markdown-документы с YAML frontmatter.
- Валидация concept ID и сопоставление с путями.
- Загрузка bundle из дерева директорий.
- Извлечение Markdown-ссылок и citations.
- Graph output для Markdown links и YAML semantic relations.
- Backlinks и отчет о broken links.
- Conformance validation для OKF v0.1.
- Детерминированная генерация `index.md`.
- Парсинг и рендеринг `log.md`.

## Валидация

Conformance validation следует правилам OKF v0.1 и сознательно не переносит
семантическую экспертизу в Go validation layer. Поиск claims, оценка
репрезентативности `type`, стиль текста, генерация контента и исправление
ссылок остаются задачей агента или кастомной политики, а не базового
conformance.

Для CI-gate используй default mode. Для review workflows добавляй `--strict`,
`--check-links` и `--check-orphans`: warnings и informational diagnostics
становятся видимыми, но сами по себе не отклоняют bundle.

## Разработка

Запуск тестов:

```sh
go test ./...
```

Проверка покрытия:

```sh
go test -coverprofile=/tmp/okf-cover.out ./...
go tool cover -func=/tmp/okf-cover.out
```

### Границы filesystem durability

`fs.Config.MaxStagedFiles` по умолчанию и максимум 100 000 (`payload-00000`…`payload-99999`). Case-folding и Unicode-normalization aliases определяются независимо. `.okf` directories no-follow 0700, private files и lease 0600 либо Open fail-closed.
