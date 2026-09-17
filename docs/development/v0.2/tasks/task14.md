# Техническое задание: v0.2 semantic mutation operations

## 1. Цель

Добавить idempotent domain operations для стандартных families OKF v0.2 поверх
lossless engine. Не давать caller'у raw YAML mutation escape hatch.

Зависимости: [`task10.md`](task10.md), [`task11.md`](task11.md), [`task13.md`](task13.md).

## 2. Operations

Добавить узкие canonical operations:

- `SetGenerated`;
- `EnsureVerification`;
- `RemoveVerification`;
- `PutSource`;
- `RemoveSource`;
- `SetUsageWindow`;
- `SetLifecycle`;
- `PutAttestedComputation`;
- `SetBundleVersion` для root `index.md`.

`PutAttestedComputation` атомарно задаёт согласованный contract:

- `type: Attested Computation`;
- runtime;
- parameters;
- inline/path computation mode;
- executor resource/receipt;
- attester resource.

Не публиковать generic `SetYAML`, `yaml.Node` или raw key path.

## 3. Bare/list verified

- Одна verification может сохранять bare mapping.
- При добавлении второго события mapping атомарно становится sequence.
- Ensure operation идемпотентна.
- Removal сохраняет допустимую форму и не теряет unknown nested keys.

## 4. Store canonical contract

Каждая operation:

- имеет отдельный canonical tag;
- length-prefix serializes все fields;
- deep-copies caller-owned slices/maps;
- имеет deterministic digest;
- не меняет digest старых operations.

Нужно ADR: additive tags в ChangeSet format v1 либо обоснованный bump. Нельзя
повышать format version «на всякий случай».

`store.ChangeSet.Actor` не ограничивать actor convention v0.2: это transaction
principal. Document actors валидируются внутри соответствующей operation.

## 5. Move/path integration

Расширить `MoveConcept`, чтобы он losslessly переписывал statically resolvable
bundle paths в:

- `sources[].resource`;
- `computation`;
- `executor.resource`;
- `attester.resource`.

External URL и scope descriptors не менять. Broken/unresolved values сохранять.

## 6. Validation gating

Сохранить pipeline:

1. Validate ChangeSet.
2. Apply operations в clone.
3. Load final staged bundle.
4. Run version-aware validator.
5. На blocking finding вернуть diagnostic-only preview.

Operation preflight проверяет actor/date/source selector/parameter uniqueness.
Strict warnings не блокируют обычный commit, если не являются explicit
postcondition операции.

## 7. Файлы

- `store/change.go`, `types.go`, `result.go`;
- `mutation/planner.go`;
- new operation handlers;
- canonical/digest/deep-copy tests;
- preview contract tests.

## 8. Tests

Все tests — AAA.

- Insert/update/remove каждой family.
- Bare/list verified.
- Shared/per-source usage window.
- Typed usage_count/required.
- Inline/file computation.
- Path rewriting local/external/scope.
- Unknown nested extensions preserved.
- Idempotent second application.
- Caller slice mutation не влияет на request/digest.
- Duplicate selectors/keys fail closed.
- Rejected plan has no stage/write/rename.
- Existing operations retain exact canonical digest.
- `go test -race ./mutation ./store`.

## 9. Acceptance criteria

- Все v0.2 families имеют public desired-state operations.
- Unknown fields/comments outside touched spans preserved.
- Attested contract cannot be left half-updated.
- Existing API/digests remain compatible.
- CLI/MCP могут строить operations без raw YAML.

## 10. Out of scope

Legacy migration, computation execution, binding, receipts/verdicts, attester
runtime и arbitrary asset execution.
