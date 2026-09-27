# Техническое задание: snapshot-based transactional mutation API

Источник: [GitHub issue #1 — Add snapshot-based transactional mutation API for safe concurrent bundle writes](https://github.com/skosovsky/okf/issues/1).

## 1. Результат задачи

Добавить в `okf` methodology-neutral фундамент для безопасных семантических
изменений нескольких файлов:

- корректный concept/fragment graph с детерминированными diagnostics и reverse
  impact;
- immutable snapshots и content-addressed revision;
- intent-level `ChangeSet` с preconditions и preview;
- staged validation целиком в памяти;
- optimistic concurrency через compare-and-swap;
- durable filesystem commit с журналом и crash recovery;
- bounded idempotency для повторных agent calls;
- актуальную документацию в обоих README и на EN/RU сайте.

`okf` остается инфраструктурой хранения и графовых мутаций. В ядро нельзя
добавлять domain types, methodology profiles, workflow states, approval gates
или правила конкретного бизнеса. Публичные операции работают с типами OKF и
позволяют вызывающему коду приносить собственную доменную модель (Bring Your
Own Types).

## 2. Проверенный baseline репозитория

На момент постановки задачи `go test ./...` проходит. Текущая архитектура:

- `bundle.LoadBundle` напрямую обходит filesystem и загружает Markdown;
- `bundle.RelationRef` уже парсит каноническую форму
  `<concept-id>[#<fragment>]`, но parser приватный;
- `bundle.extractSemanticRelations` молча пропускает malformed `relations`,
  relation type, item и target ref;
- nested `id`/`anchor` используются только как source для найденного relation
  block; полноценного `SubresourceIndex` и duplicate detection нет;
- `Relation.TargetExists` проверяет только concept. Target fragment не
  резолвится, поэтому `b#missing` считается существующим, если существует `b`;
- `Bundle.SemanticLinksFrom` дает только outgoing semantic relations;
  incoming semantic index и reverse impact API отсутствуют;
- `validator.ValidateBundle` принимает concrete `*bundle.Bundle`, а
  `ValidatePath` всегда повторно читает directory;
- `internal/mcpserver/write.go` валидирует изменение через полную temporary
  filesystem copy и затем атомарно заменяет один файл;
- текущий write path не имеет bundle revision, CAS, lease, directory/file
  `fsync`, transaction journal, recovery и idempotency receipts;
- пакетов `mutation`, `store` и `store/fs` пока нет;
- `Frontmatter` хранит YAML AST и сохраняет неизвестные поля; это поведение
  нельзя потерять при мутациях.

Не создавать второй независимый parser relations или параллельную модель refs.
Нужно развить существующие `bundle.RelationRef`, `bundle.Relation` и
`Bundle.SemanticLinksFrom`, сохранив source compatibility публичного Go API там,
где это возможно.

## 3. Contract-first gate

До реализации filesystem commit зафиксировать публичные контракты в GoDoc и
contract tests. Минимальная граница:

```go
type Snapshot interface {
    Revision() Revision
    OpenConcept(bundle.ConceptID) (bundle.Concept, error)
    ListConcepts() ([]bundle.ConceptID, error)
}

type ChangeSet struct {
    ID            ChangeSetID
    Actor         Actor
    BaseRevision  Revision
    Operations    []Operation
    Preconditions []Precondition
}

type Store interface {
    Snapshot(context.Context) (Snapshot, error)
    Preview(context.Context, ChangeSet) (Preview, error)
    Commit(context.Context, ChangeSet, CommitOptions) (CommitReceipt, error)
}
```

Exact signatures можно уточнить до начала implementation, но итоговый контракт
обязан явно определить:

- zero-value и validation rules для `Revision`, `ChangeSetID`, `Actor` и
  idempotency key;
- versioning сериализуемых `ChangeSet`, journal и receipt formats;
- closed или extensible набор `Operation`/`Precondition` и правила их
  сериализации;
- error taxonomy и возможность надежно распознавать ошибки через
  `errors.Is`/`errors.As`;
- deterministic ordering всех возвращаемых slices и diagnostics;
- ownership returned values: вызывающий код не должен мутировать snapshot
  через возвращенные slices, maps или YAML nodes;
- cancellation semantics для preview, hashing, validation, lease acquisition
  и commit;
- отсутствие domain-specific enums и зависимостей от MCP/CLI.

Обязательные structured errors/results:

- `Conflict`: expected revision, actual revision, changed refs при наличии и
  `Retryable`;
- `PreconditionFailure`: failed precondition и affected refs;
- `InvalidChangeSet`: stable machine-readable code и diagnostics;
- `IdempotencyConflict`: key и digests предыдущего/нового request;
- `CommitReceipt`: change set/idempotency identity, base/result revisions,
  commit time и фактически измененные refs/files.

## 4. Phase 1 — graph correctness

### 4.1. Relation diagnostics

Заменить silent skip на structured diagnostics для:

- `relations` не mapping;
- relation key невалидного типа или зарезервированного имени;
- relation value не sequence;
- item не mapping;
- отсутствующего, пустого или non-string `target`;
- malformed/canonical-invalid relation ref;
- nested relation source без валидного `id` или `anchor`;
- отсутствующего target concept;
- отсутствующего или неоднозначного target fragment.

Diagnostic содержит stable code, source concept, source fragment при наличии,
relation type при наличии, raw target при наличии, source file и понятное
message. Порядок diagnostics стабилен по source path, source ref, relation type,
target и code. Невалидная relation не попадает в resolved edge index, но
остается видимой в diagnostics.

### 4.2. Canonical refs и subresources

- Сделать единственный проверяемый public/internal contract для parsing
  `<concept-id>[#<fragment>]`; graph renderers, mutation planner и validator
  используют его, а не собственные string operations.
- Построить immutable `SubresourceIndex` по всем explicit nested string-полям
  `id` и `anchor`, а не только рядом с `relations`.
- `id` имеет приоритет как canonical fragment текущего mapping; валидный
  `anchor` используется, если `id` отсутствует или невалиден. Если оба поля
  валидны и различаются, правила aliasing должны быть явно выбраны,
  задокументированы и протестированы; не считать оба canonical молча.
- Диагностировать duplicate fragment IDs внутри одного concept. Duplicate не
  должен резолвиться произвольным first/last wins.
- Для relation target с fragment считать target существующим только при наличии
  и concept, и однозначного fragment. Это намеренная коррекция текущего
  поведения `exists`; обновить graph golden tests и документацию.

### 4.3. Indexes и reverse impact

Добавить детерминированные:

- outgoing relation index;
- incoming relation index;
- lookup concept/fragment ref;
- reverse impact query для rename/delete/move concept;
- reverse impact query для rename/delete fragment.

Не ломать существующие Markdown backlinks: semantic incoming relations и
Markdown backlinks — разные слои. `graph` exporters должны использовать общий
resolved graph и одинаково трактовать `exists` во всех форматах.

## 5. Phase 2 — immutable snapshot и staged mutation

### 5.1. Revision

`BundleRevision` вычисляется из canonical sorted manifest относительных slash
paths и content digests:

- начальный алгоритм: SHA-256;
- serialized value: `sha256:<lowercase-hex-digest>`;
- manifest encoding использует однозначный length-prefix для path и digest, а
  не разделители строк;
- symlinks не читаются и не хэшируются;
- internal lease/journal/receipt files не входят в revision;
- набор остальных revision-visible файлов и internal metadata location нужно
  явно задокументировать и покрыть tests;
- кэшировать per-file digest и поддержать incremental manifest recomputation;
- алгоритм hashing должен быть заменяемым без изменения модели `Revision`.

Проверить одинаковый digest при разном порядке directory enumeration и разные
digests для path/content комбинаций, которые были бы неоднозначны при простом
concatenation.

### 5.2. Snapshot-compatible source

Отделить read model от `os.*` так, чтобы bundle loading, graph construction и
validator работали поверх immutable snapshot-compatible source. Сохранить
существующие `LoadBundle(path)` и `ValidatePath(path, cfg)` как filesystem
adapters.

Source contract обязан сохранять:

- deterministic slash-path listing;
- UTF-8 и parse-error behavior;
- reserved `index.md`/`log.md` behavior;
- запрет path traversal и symlink following;
- копирующую семантику возвращаемых значений;
- backward-compatible conformance diagnostics.

### 5.3. In-memory overlay

Реализовать overlay над base snapshot:

- changed/new files;
- deletion tombstones;
- rename/move без fallback к старому path;
- чтение неизмененных файлов из base snapshot;
- deterministic directory listing;
- те же path normalization и symlink protections, что у filesystem source.

Preview и validation overlay не создают temporary directory и не меняют
реальный bundle.

### 5.4. Operations и preconditions

Минимальный intent-level набор:

- `EnsureRelation`: desired-state операция, повторное применение не создает
  duplicate edge;
- `MoveConcept`: переносит concept и обновляет все statically resolvable refs,
  затронутые indexes и local `index.md`, не теряя unknown frontmatter;
- `RenameFragment`: переименовывает explicit `id`/`anchor` и все входящие
  canonical refs; missing/duplicate source fragment отклоняется до commit.

Не добавлять non-idempotent `AppendRelation`. Serialization Markdown/YAML
должна минимизировать unrelated diffs и сохранять unknown frontmatter fields.

Минимальные preconditions:

- ref существует / не существует;
- file/content digest равен ожидаемому;
- relation существует / отсутствует;
- fragment однозначен;
- base revision равна ожидаемой.

### 5.5. Preview

`Store.Preview` строит и валидирует staged snapshot без записи и возвращает:

- base/result revision;
- read set;
- write/delete/rename set;
- direct affected refs;
- reverse impact set;
- validation и relation diagnostics;
- normalized operation plan в детерминированном порядке.

Preview invalid change не должен возвращать commit-ready plan. Нельзя
использовать preview result как authorization token после изменения base
revision: commit все равно выполняет CAS.

## 6. Phase 3 — concurrent durable filesystem commit

Commit protocol:

1. Прочитать immutable snapshot `R`.
2. Проверить `ChangeSet`, preconditions и idempotency request digest.
3. Спланировать операции и impact set.
4. Построить и провалидировать staged snapshot без lock.
5. Взять короткую bundle-wide write lease для cooperating writers.
6. Повторно вычислить current revision и сравнить с `R` через CAS.
7. При mismatch вернуть structured `Conflict`; last-write-wins запрещен.
8. Записать и синхронизировать transaction journal.
9. Применить staged create/update/rename/delete.
10. Опубликовать result revision и durable commit receipt.
11. Завершить/удалить journal по выбранному recovery protocol и отпустить lease.

### 6.1. Lease и conflict handling

- Lease bundle-wide и advisory; platform details скрыть в `store/fs/internal`.
- Lock wait учитывает `context.Context` и имеет bounded timeout/config.
- Structured conflict содержит expected/actual revisions, известные changed refs
  и retryability.
- Автоматических retries не больше двух; каждый retry начинает новый snapshot,
  заново планирует impact и валидирует staged state.
- Store не делает semantic merge конфликтующих targets.

### 6.2. Durability и recovery

- Для каждого staged file: создать temp в целевой filesystem, записать,
  `file.Sync()`, проверить `Close()`, затем rename.
- Синхронизировать каждую directory, затронутую create, rename или delete.
- Синхронизировать journal file и transaction directory.
- Проверять ошибки write, short write, chmod при необходимости, sync, close,
  rename, remove и directory sync; не проглатывать secondary errors.
- Platform-specific sync/rename/lease behavior изолировать внутренним
  интерфейсом и fake implementation для fault injection.
- Startup/open store всегда выполняет deterministic recovery незавершенных
  transactions до выдачи snapshot.
- Recovery приводит bundle либо к валидному pre-state, либо к валидному
  committed post-state согласно зафиксированному journal protocol; смешанное
  состояние не допускается после recovery.

Документировать ограничение: несколько filesystem renames не становятся
атомарно видимыми raw readers. Journal гарантирует recovery, а CAS/lease —
защиту только cooperating writers.

### 6.3. Idempotency

- Receipt хранит key, canonical request digest, base revision, result revision,
  commit time и commit identity.
- Повтор того же key + request digest внутри retention window возвращает
  предыдущий receipt и ничего не применяет повторно.
- Тот же key с другим digest возвращает `IdempotencyConflict`.
- Default retention: 24 часа и минимум последние 1000 receipts, даже если они
  старше time window; оба параметра configurable и bounded.
- Очистка receipts детерминирована и сама не должна ломать commit/recovery.
- Request digest считается из versioned canonical ChangeSet representation, а
  не из недетерминированного Go map/JSON output.

## 7. Package boundaries

Целевая структура:

```text
bundle/       parsing, serialization, existing relation value types
graph/        subresource/relation indexes, diagnostics, reverse impact
validator/    conformance over snapshot-compatible source
mutation/     operations, ChangeSet, planning, preconditions, preview
store/        Snapshot, Revision, Store, receipt and conflict contracts
store/fs/     filesystem adapter, overlay, lease, journal, durability, recovery
```

Допустима другая декомпозиция только если она не создает import cycles и
сохраняет единственную модель relation refs. Domain logic не помещать в
`cmd/okf`, `cmd/okf-mcp` или transport handlers.

Существующий `internal/mcpserver/write.go` должен перестать владеть отдельной
реализацией staging-copy/atomic-write. `write_concept` нужно адаптировать к тем
же snapshot, overlay, validation и durable commit primitives, чтобы MCP был
cooperating writer. При этом не расширять MCP tool schema без отдельного
contract decision и сохранить текущие success/rejected response contracts.

## 8. Backward compatibility

- Не менять module path и существующие CLI command names/flags.
- Сохранить public entrypoints `bundle.LoadBundle`, `validator.ValidateBundle`,
  `validator.ValidatePath` и существующие graph renderers.
- Не смешивать semantic relation diagnostics с base OKF v0.1 conformance без
  явного opt-in/config contract: существующий bundle не становится
  non-conformant только из-за нового relation lint слоя.
- Исправление fragment-aware `exists` — осознанное graph behavior change; оно
  должно быть отражено в tests, README и сайте.
- Unknown frontmatter keys, неизвестные `type` и future `okf_version` должны
  сохраниться byte/semantic-equivalent после затронутой mutation.
- Existing path traversal и symlink protections нельзя ослаблять.

## 9. Tests

Все тесты писать в AAA (Arrange–Act–Assert). Кроме unit tests добавить
contract, integration, concurrent и recovery suites.

Обязательные adversarial cases:

- все malformed relation shapes и refs возвращают deterministic diagnostics;
- duplicate fragments, missing concept и missing fragment;
- ambiguous `id`/`anchor` и nested fragments;
- deterministic outgoing/incoming/impact ordering;
- manifest collision attempts, mixed path separators и enumeration order;
- overlay create/update/delete/rename и tombstone fallback;
- invalid staged mutation оставляет filesystem byte-identical;
- stale `BaseRevision` никогда не перезаписывает новый commit;
- два concurrent `EnsureRelation` не теряют edge и не создают duplicate;
- concurrent `EnsureRelation` + `MoveConcept` не теряют relations;
- same idempotency key/same payload возвращает тот же receipt;
- same key/different payload отклоняется;
- retention cleanup сохраняет минимум 1000 последних receipts;
- symlink root, ancestor, target и swap attempts;
- cancellation до lease, во время lease wait и до journal publish;
- fault injection после каждого write/sync/close/rename/delete/directory-sync/
  journal step с последующим reopen/recovery;
- unknown YAML fields и ordering не теряются;
- deterministic outputs при повторном запуске.

Не использовать timing-only assertions для concurrency. Координировать goroutine
через barriers/channels и запускать race suite.

## 10. README и сайт — обязательный documentation gate

Задача не считается завершенной без синхронного обновления:

- `README.md`;
- `README.ru.md`;
- `docs/index.md`;
- `docs/ru/index.md`;
- `docs/toolkit.md`;
- `docs/ru/toolkit.md`.

Документация должна:

- добавить transactional mutation/store как публичную Go-library capability;
- показать короткий compilable пример `Snapshot` → `ChangeSet` → `Preview` →
  `Commit` с conflict handling;
- описать `EnsureRelation`, `MoveConcept`, `RenameFragment` и desired-state
  semantics;
- объяснить algorithm-qualified revision, CAS и idempotency window;
- честно перечислить guarantees и limitations: advisory locks, raw editors,
  non-atomic visibility нескольких renames, single-filesystem scope и
  необходимость другого backend для distributed deployments;
- обновить описание MCP `write_concept`: staged validation и durable cooperating
  commit, без обещания distributed isolation;
- исправить прежнее описание fragment `exists`: target существует только при
  успешном разрешении concept и fragment;
- сохранять смысловой паритет EN/RU, не копируя длинный API reference на landing
  page.

Если меняется contract agent skill, дополнительно обновить
`skills/open-knowledge-format/SKILL.md`, зеркала `docs/skill.md` /
`docs/ru/skill.md` и `skills-lock.json`. Не менять skill только ради marketing
copy.

## 11. Verification commands

```sh
gofmt -w <changed-go-files>
go vet ./...
go test ./...
go test -race ./...
```

Отдельно запустить recovery/fault-injection suite многократно без test cache.
Если тесты требуют platform-specific исключений, они должны использовать
явные build tags или capability detection, а не silently skip основной
durability contract.

## 12. Acceptance criteria

- Malformed relation дает deterministic structured diagnostic с source
  concept/fragment, relation type и target, когда поля доступны.
- Missing/duplicate target fragment обнаруживается до commit.
- Outgoing, incoming и reverse impact queries deterministic и fragment-aware.
- Revision воспроизводима и меняется при любом revision-visible content/path
  change.
- Preview полностью выполняется без filesystem copy и без реальных записей.
- Stale `BaseRevision` не может перезаписать новый commit.
- Invalid staged change оставляет bundle без изменений.
- Повтор одного ChangeSet/idempotency payload внутри window не применяет его
  дважды; reuse key с другим payload отклоняется.
- Fault injection в каждой точке multi-file commit после recovery дает
  детерминированный валидный state.
- Concurrent semantic mutations не теряют relations.
- Unknown frontmatter сохраняется, существующий OKF conformance остается
  backward compatible.
- Existing CLI, library, graph и MCP tests проходят, новые concurrency tests
  проходят с `-race`.
- Оба README и EN/RU сайт описывают фактически реализованные контракты,
  guarantees и limitations.

## 13. Non-goals

- Methodology profiles, workflow/business gates и semantic approval.
- Fine-grained locks или MVCC в первой версии.
- Автоматический semantic merge конфликтующих targets.
- Distributed lock service или distributed filesystem transaction.
- Git/object-store/database backend в этой задаче; public `Store` contract не
  должен мешать добавить их позже.
- Замена native validators вроде OpenAPI/Protobuf/Avro validators.
- Release, publish, tag, push или commit.
