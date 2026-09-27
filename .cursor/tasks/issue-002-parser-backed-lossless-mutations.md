# Техническое задание: parser-backed lossless mutations

Источник: продолжение [issue #1](issue-001-transactional-mutation-api.md). Эта
задача устраняет presentation-risk в уже добавленном mutation path; это не
исследовательская заметка и не разрешение на переписывание формата OKF.

## 1. Результат и мотивация

Mutation planner должен менять только доказанно принадлежащие операции байты
Markdown destination и YAML scalar, сохраняя **каждый** байт вне выбранных
span: комментарии, отступы, переносы строк, quoting, порядок и неизвестные
extension-поля. Никакого AST re-render или auto-canonicalization.

Исходный risk baseline (до этой задачи; в текущем дереве уже заменён — см. §10):

- [`mutation/planner.go`](../../mutation/planner.go) содержал handwritten
  построчный глобальный scanner `rewriteMarkdownLinks`; он вручную исключал
  fences/code spans и распознавал inline links/reference definitions. Такой
  scanner не является CommonMark semantic authority и **удалён** в пользу
  Goldmark-oracle path.
- [`mutation/presentation.go`](../../mutation/presentation.go) использовал
  `yaml.v3.Node.Line`/`Column` как старт token и эвристически искал конец
  scalar. Это был temporary baseline; сейчас span строит fail-closed
  `yamlResolver` (rune-aware `coordinate` + lexical `scalarEnd`).
- [`mutation/overlay.go`](../../mutation/overlay.go) при `clone` и `Rename`
  копировал payload, а `Paths` каждый раз перечислял base; оптимизировано в
  текущей плоской модели (immutable payload share + delta manifest).
- В `go.mod` уже зафиксированы `github.com/yuin/goldmark v1.8.2` и
  `gopkg.in/yaml.v3 v3.0.1`. Goldmark уже применён validator-ом.
- `bundle.Source` — context-aware immutable source с defensive-copy contract;
  `FileSystemSource` — adapter directory; добавлен `SourceFromFS`. MCP/CLI
  schemas уже существуют.

## 2. Цели и границы

Цели:

1. Заменить Markdown scanner semantic разбором Goldmark и точным внутренним
   collector-ом source byte spans.
2. Построить поверх `yaml.v3` fail-closed lossless resolver для строго
   названного YAML subset.
3. Уменьшить allocation/read overhead текущего overlay без изменения внешней
   модели `bundle.Source`; добавить `SourceFromFS(fs.FS)`.
4. Сохранить transaction/CAS/preview contract issue #1: ни один невалидный или
   unsupported mutation не оставляет staged либо filesystem partial write.

Не входит в задачу:

- поддержка Markdown autolinks (`<https:...>`), raw HTML links, URL rewriting
  в text/HTML и произвольных extensions, пока это отдельно не контрактировано;
- рендеринг/пересериализация Markdown или YAML, смена formatting всего файла;
- полная миграция YAML на `goccy/go-yaml`, tree-sitter либо новая зависимость;
- default parent-linked overlay, persistent chain, HAMT и крупная смена store
  model (допустимы только после benchmark gate ниже);
- compatibility shims/двойные parser paths. После миграции legacy scanner и
  его tests удаляются, а не живут как fallback.

## 3. Нормативные общие контракты

- Все span — half-open byte offsets `[Start, End)` в оригинальном `[]byte`, не
  rune offsets и не позиции нормализованной строки. Вход обязан быть valid
  UTF-8 до любого coordinate mapping; иначе `bundle.ErrInvalidEncoding`.
- Collector возвращает deterministic сортировку по `(Start, End, Kind)`;
  spans строго non-overlap. Пересечение, выход за buffer, semantic/span
  mismatch или невозможность однозначно найти token — typed fail-closed error,
  а не best-effort patch.
- Patch сначала валидируется весь, затем применяется справа налево к owned
  copy. До этого не мутируются `Overlay`, manifest, staged snapshot и disk.
  После patch semantic parser повторно разбирает весь документ и подтверждает
  intended semantic replacement; вне union spans применяется byte equality.
- `errors.Is`/`errors.As` должны различать как минимум `Unsupported` и
  `Ambiguous`, содержать stable code, format, path, operation и location.
  Например: `mutation.ErrUnsupportedPresentation`,
  `mutation.ErrAmbiguousPresentation`; exact exported names фиксируются до
  кода в GoDoc/contract tests, без string matching.
- Context проверяется перед costly parse/hash/read и на границах обходов;
  cancellation возвращается без частичного результата. Returned slices/maps
  owned by caller; source, overlay base и cached manifest остаются immutable и
  безопасны для concurrent reads.
- Existing MCP tool inputs/outputs и CLI flags/JSON wire schemas не меняются.
  Новые internal types не становятся обязательными публичными transport
  полями. Public Go contract меняется только с contract test и GoDoc.

## 4. Markdown: Goldmark как semantic oracle, source spans отдельно

### 4.1. Scope и API sketch

Goldmark v1.8.2 становится единственным semantic authority для mutation
destinations. Internal API (имена могут быть уточнены до implementation, но
обязанности/результаты — нет):

```go
type MarkdownDestinationKind uint8
const (
    MarkdownLinkDestination MarkdownDestinationKind = iota
    MarkdownImageDestination
    MarkdownReferenceDefinitionDestination
)

type SourceSpan struct { Start, End int }
type MarkdownDestination struct {
    Kind MarkdownDestinationKind
    Span SourceSpan        // destination token, включая <...> только если он был в source
    Value string           // semantic destination без presentation wrapper по контракту
}

func collectMarkdownDestinations(source []byte) ([]MarkdownDestination, error)
func rewriteMarkdownDestinations(source []byte, rewrite func(value string) (string, bool)) ([]byte, error)
```

Collector обязан связать каждый rewriteable candidate с соответствующим
Goldmark AST node и его semantic destination. Сам Goldmark AST **не** даёт
destination byte offsets: internal exact-source collector вычисляет их по
оригинальным байтам и доказывает совпадение с AST. Нельзя подменять это
`ast.Node` position, line scanner-ом либо AST rendering.

Поддержать только semantic nodes Goldmark:

- `ast.Link` (inline и full/collapsed/shortcut reference после resolution);
- `ast.Image` в тех же формах;
- `ast.LinkReferenceDefinition`, patch definition destination ровно один раз,
  даже если на неё ссылаются много use-sites.

Inline/reference use-sites не должны порождать дубликат span definition.
Неразрешённые reference links не переписываются. Autolinks и raw HTML должны
быть явно классифицированы как out-of-scope и остаться byte-identical.

### 4.2. Обязательная логика

1. Parse ровно оригинальные bytes Goldmark-ом с согласованными parser options.
2. Собрать eligible AST semantics, including resolved reference relationship.
3. Exact collector на исходных bytes находит только кандидаты, которые могут
   быть доказанно сопоставлены AST semantic item; любой extra/missing/ambiguous
   candidate — ошибка, file не меняется.
4. Отсортировать/deduplicate spans, проверить non-overlap, построить patches.
5. Reparse результата Goldmark-ом: intended eligible destination должен быть
   заменён, незатронутые eligible semantic destinations — эквивалентны;
   outside-span equality обязательна.

Миграция: удалить `rewriteMarkdownLinks`, `rewriteMarkdownLine`, fence/code
эвристики и связанные legacy-only tests из `mutation/planner.go`; planner
вызывает один новый path. Не оставлять scanner как compatibility fallback.

## 5. YAML: yaml.v3 semantic authority + lossless raw-byte resolver

### 5.1. Contract и subset

`yaml.v3` остаётся semantic authority этой задачи: `yaml.Node` определяет
какая relation target/`id`/`anchor` подлежит операции, но **не** определяет
конец исходного token. Resolver получает original frontmatter bytes и node,
строит byte-span и подтверждает, что raw token декодируется в тот же semantic
scalar. `goccy` `Node.Offset` нельзя считать byte offset; goccy/tree-sitter
не добавляются в implementation phase.

Поддерживаемый lossless subset (остальное всегда typed `Unsupported`):

- UTF-8 YAML document mapping в уже распознанном frontmatter, LF или CRLF;
- block-style mappings/sequences; scalar keys/values только для touched
  `relations`, `target`, canonical `id`/`anchor`;
- plain, single-quoted и double-quoted scalar, если resolver доказывает полный
  token range и encoder может представить replacement с сохранением style либо
  безопасной quoted scalar;
- insertion только в current documented block forms `relations` mapping и
  relation-type sequence, после полного resolver proof окружающего блока.

Fail closed `Unsupported` для flow mappings/sequences, block scalar styles,
explicit tags, anchors, aliases, merge keys, directives/document markers
внутри frontmatter, multi-document YAML, complex keys, duplicate touched keys,
комментариев/позиций, не позволяющих доказать range, и любой parser/raw
semantic disagreement. Anchors/aliases не редактируются: если touched node
имеет anchor либо value проходит через alias/merge, reject; unrelated bytes
могут оставаться только при доказанном непересечении. Политика directives:
`%YAML`/`%TAG` и explicit `---`/`...` вне recognized outer frontmatter layout
не поддерживаются.

`Ambiguous` обязателен (не Unsupported) для нескольких semantic candidates:
duplicate `relations`, relation type, `target`, canonical fragment, alias
provenance либо несколько raw ranges для одного node. Никакого first/last wins.
Verification/invariant failures после patch (`semantic_edit_mismatch`,
`outside_span_changed`, `missing_semantic_edit`, `raw_semantic_mismatch`,
overlap/`invalid_patch`) — это `Unsupported`, не Ambiguous.

### 5.2. Coordinates, verification, API

Coordinate mapper обязан один раз построить таблицу `yaml.v3` `(Line, Column)`
→ original byte offset: lines 1-based, columns 1-based **character (rune)
columns** within the physical line (yaml.v3 `skip()` advances `column` by one
per Unicode character while `buffer_pos` advances by UTF-8 width — never treat
Column as a raw byte index). CRLF учитывается как два source bytes при
переходе строки, но CR не входит в column count токена (line break
потребляется целиком). Он проверяет границы/UTF-8 и никогда не вычисляет
offset по normalized YAML output. После `offset` resolver лексически находит
exact scalar token до grammar boundary,
учитывая YAML escapes/удвоенные single quotes, затем семантически сверяет его
с node `Value`, `Tag`, kind и style.

```go
type YAMLScalarSpan struct { Span SourceSpan; Style yaml.Style }
type YAMLResolver interface {
    Scalar(*yaml.Node) (YAMLScalarSpan, error)
    BlockEnd(*yaml.Node) (int, error) // only supported insertion subset
}
func parseLosslessPresentation(data []byte) (*presentation, error)
func (p *presentation) VerifyPatched(data []byte, expected SemanticEdit) error
```

Имена могут отличаться, но resolver не должен быть exported unless needed by
tests; public mutation errors должны быть inspectable. `scalarPatch`,
`offset`, `blockEnd` и insertion logic мигрируют на resolver либо удаляются.
Нельзя сохранять old heuristic path.

После любого YAML patch: reparse `yaml.v3`, проверить targeted semantic edit,
сохранить semantic equality всех untouched nodes required by operation и exact
outside-span equality. Если повторный parse/error verification не проходит,
return error и не stage file.

### 5.3. Decision gate, не реализация

Полная goccy AST/CST migration допускается только отдельной issue после:

1. опубликованного corpus результата current resolver;
2. измеренного числа valid production documents, отвергнутых subset-ом;
3. benchmark/profile, показывающего, что resolver является material bottleneck;
4. проверки API stability, security/CVE policy и совместимой лицензии;
5. отдельного contract-first design с byte-offset validation.

До прохождения gate не добавлять goccy dependency и не менять authority.

## 6. Overlay и SourceFromFS

Оптимизировать существующую плоскую overlay прежде новой структуры:

- changed payload immutable после staging; `clone` shallow-copies maps и
  переиспользует immutable payload slices, а public `ReadFile` всё ещё отдаёт
  defensive copy. Вход `Put` копируется ровно один раз; ownership документирован.
- manifest — delta над immutable base manifest: changed/deleted/renamed paths
  не требуют full content rehash; deterministic lexical visible Paths.
- убрать redundant full `bundle.Load`/`bundle.LoadBundle` в planner/store там,
  где already-loaded staged `bundle.Source`/bundle и validation result
  достаточны; не ослабить snapshot/CAS/diagnostics semantics.
- `SourceFromFS(fsys fs.FS) bundle.Source` (или семантически эквивалентный
  adapter) добавляется поверх `io/fs`: slash paths only, no traversal,
  deterministic sorted `Paths`, regular files only, defensive reads, context
  checked before/through traversal. `bundle.Source` остаётся core contract;
  `FileSystemSource` и existing load APIs сохраняются как adapters.

Persistent parent chain/HAMT, structural sharing beyond immutable payload и
невзаимозаменяемая source migration вне scope. Их можно предложить лишь если
профиль этой реализации показывает benchmark gate: representative transaction
превышает согласованный budget по allocations/latency, и простой delta/shallow
copy вариант не удовлетворяет target. До gate не создавать такую архитектуру.

## 7. План работ

1. **Contracts и fixtures.** Зафиксировать errors, source spans, semantic
   verification, ownership/context GoDoc и contract tests; добавить adversarial
   Markdown/YAML corpus до replacement кода.
2. **Markdown.** Реализовать Goldmark-oracle collector, patch/reparse proof,
   перевести planner, удалить legacy scanner.
3. **YAML.** Реализовать coordinate mapper/resolver subset, typed failures,
   patch/reparse proof; заменить old scalar/end heuristics и удалить их.
4. **Overlay.** Ввести immutable payload/delta-manifest оптимизации и FS
   adapter; устранить доказанно лишние loads; снять benchmarks/profiles.
5. **Verification/documentation.** Run full test/fuzz/benchmark matrix,
   обновить документацию и приложить evidence к PR/issue.

Каждая фаза компилируется и проходит tests отдельно. No partial staged writes:
при error result должен быть пустым/не commit-ready и prior snapshot untouched;
filesystem commit выполняется только уже существующим transactional protocol.

## 8. Acceptance criteria и матрица проверки

Задача не может быть заявлена выполненной, пока **каждый** пункт ниже не имеет
direct test, benchmark/profile artifact или ссылку на конкретный command/output.

### Markdown (AAA + corpus + fuzz)

- Arrange/Act/Assert tests доказывают exact rewrite и outside-span byte equality
  для inline Link, Image, reference definition, full/collapsed/shortcut refs,
  repeated uses одной definition, titles, angle destinations, nested brackets,
  escapes, CRLF, multiline and adjacent links.
- CommonMark corpus включает fenced/indented code, code spans, blockquotes,
  lists, HTML, autolinks, unresolved refs, malformed syntax; только
  contract-supported semantic nodes меняются.
- Goldmark AST count/value и collected spans совпадают; output reparse
  подтверждает edit. Overlap/ambiguity/mismatch не изменяют source.
- Fuzz: arbitrary valid UTF-8 Markdown, random eligible rewrites; invariant —
  no panic, sorted nonoverlap spans, parse verification or typed error, and
  bytes outside spans identical. Seed corpus committed.

### YAML (AAA + corpus + fuzz)

- Tests для plain/single/double scalar, LF/CRLF, escaped quotes, nested
  mappings, `id`/`anchor`, relation target insertion/update/dedup prove
  resolver span and semantic reparse/outside-span equality.
- Corpus explicitly covers comments, quoted keys, flow/block styles, folded /
  literal scalars, tags, anchors, aliases, merge keys, directives, documents,
  duplicates, invalid UTF-8/YAML and ambiguous positions. Unsupported/Ambiguous
  is asserted by `errors.Is`/`As`; no fallback rewrite/canonicalization.
- Fuzz valid UTF-8 frontmatter and mutations: no panic, no out-of-bound/overlap,
  accepted output reparses and validates intended semantics; rejected input
  leaves original bytes/stage unchanged.

### Overlay/source (AAA + race + allocation)

- Tests prove input/output defensive copies, shallow sharing only for immutable
  internal payload, concurrent `Paths`/`ReadFile` safety (`go test -race`),
  context cancellation and deterministic ordering.
- `SourceFromFS` contract tests run against `fstest.MapFS`: sorted paths,
  nested files, invalid paths, directories/nonregular entries and copies.
- Benchmarks include empty overlay, N changed files, rename, 1/100/10k paths,
  repeated preview and validation. Record `-benchmem` baseline vs result and
  CPU/memory profile for representative large bundle; assert allocation test
  limits derived from recorded baseline (not arbitrary vanity numbers).
- Functional tests prove removed redundant full loads via instrumented source
  read/path counts without losing validation, revision or diagnostics.

### Repository-wide

- `go test ./...`, focused `go test -race ./...`, fuzz smoke/time-bounded CI
  command, `go vet ./...`, and `git diff --check` pass.
- Update [`README.md`](../../README.md), [`README.ru.md`](../../README.ru.md),
  EN/RU site pages under [`docs/`](../../docs/) and
  [`skills/open-knowledge-format/SKILL.md`](../../skills/open-knowledge-format/SKILL.md):
  describe lossless supported subset, fail-closed errors, Source adapter and
  explicitly excluded Markdown/YAML forms. EN/RU claims must agree.
- Dependency policy: implementation introduces **no** goccy/tree-sitter/new
  parser dependency. If a future dependency is proposed, pin version, review
  license compatibility, SBOM/vulnerability status, maintenance ownership and
  run `go mod tidy`/license audit in that separate decision.

## 9. Rollback и delivery evidence

Changes are ordinary source/test/doc commits; rollback is a revert of the
whole implementation commit, never a runtime dual parser flag or compatibility
shim. Existing transactional journal/CAS retains authority for durable writes.
No error path may publish a partial document, partial overlay, manifest or
filesystem state.

PR/issue description must enumerate every acceptance criterion with direct
test names/commands and benchmark/profile paths. Missing evidence blocks
completion even if manual examples look correct — presentation corruption is
exactly the kind of bug that “works on my fixture” likes to smuggle in.

## 10. Implementation evidence (2026-07-20)

This record supplements, and does not weaken, the normative criteria above.
Run from the repository root after all in-flight implementation files are
present:

```sh
go test ./bundle ./mutation
go test ./...
go test -race ./...
go vet ./...
go mod verify
go test -run '^$' -bench 'BenchmarkOverlay(Clone|Rename|Paths|ManifestDeltaRevision)$' -benchmem ./mutation
git diff --check
```

Behavioral gates: the Markdown corpus/fuzz tests must prove Goldmark semantic
counts equal exact non-overlapping source spans and outside-span byte equality;
the YAML corpus/fuzz tests must prove `errors.Is`/`errors.As` for Unsupported
and Ambiguous, invalid UTF-8 rejection, semantic reparse, and no staged write;
the overlay/source tests must prove defensive public reads, shallow private
payload sharing, manifest delta reuse, and only-successful shared `Paths`
caching. `bundle.SourceFromFS` tests exercise sorted regular slash paths and
the caller-owned stable-snapshot contract.

Benchmark output is a hardware-specific observation, not a hard nanosecond
target. Record the Go version, CPU, and `-benchmem` output with the PR; use it
to detect material regressions and to justify any future proposal for a HAMT or
parent chain, neither of which is part of this implementation.
