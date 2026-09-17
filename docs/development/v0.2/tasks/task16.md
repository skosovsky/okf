# Техническое задание: `store` compatibility guardrails для OKF v0.2

## 1. Verdict

Backend-neutral `store` уже spec-agnostic. Production API migration не нужна.
Task является verification/no-op guardrail и закрывается после integration.

## 2. Не менять

- `Store`, `Snapshot`, `ChangeSet`;
- `ChangeSetFormatVersion`;
- `Preview`, `CommitReceipt`, `CommitReceiptFormatVersion`;
- diagnostic wire shape;
- `store.Actor` validation.

Новые frontmatter families принадлежат `bundle`/`validator`/`mutation`.

## 3. Receipt boundary

`store.CommitReceipt` — transaction durability evidence.
`executor.receipt` — runtime artifact Attested Computation.

Запрещено:

- добавлять executor receipt/verdict в CommitReceipt;
- bump'ать format version из-за OKF v0.2;
- делать store execution/attestation cache;
- ограничивать store.Actor actor convention'ом document metadata.

## 4. Guardrail tests

Все tests — AAA.

- New validator codes проходят через `Preview.Diagnostics` без потери
  code/file/severity/message.
- v0.1 и v0.2 bundles используют один transaction protocol.
- Document actors и legacy store actors принимаются; whitespace/control/UTF-8
  policy не меняется.
- Executor-like JSON не принимается как canonical transaction receipt.
- Existing ChangeSet/Receipt canonical bytes неизменны.

## 5. Acceptance criteria

- Public interfaces и format versions не изменились.
- Integration `go test ./store ./mutation ./store/fs` проходит.
- Docs явно различают два receipt.

## 6. Out of scope

Execution, attestation storage/cache и generic raw asset operation, если она не
потребуется отдельному migration contract.
