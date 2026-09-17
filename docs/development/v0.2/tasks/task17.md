# Техническое задание: OKF v0.2 durability contract в `store/fs`

## 1. Verdict

`store/fs` хранит opaque regular-file bytes и уже способен durable сохранять
v0.2 metadata/assets. Production spec-specific changes не нужны; требуется
явный contract corpus.

Зависимости: [`task10.md`](task10.md), [`task11.md`](task11.md), [`task16.md`](task16.md).

## 2. Fixture

Создать bundle:

- root `okf_version: "0.2"`;
- nested sources/generated/verified/lifecycle;
- Attested Computation;
- `references/computations/*.sql`;
- `references/attesters/*.py`;
- executor `.md`.

## 3. Tests

Все tests — AAA.

- Snapshot `Paths`/`ReadFile` byte-exact для metadata/assets.
- Любое metadata/asset byte change меняет revision.
- Version-only index migration меняет revision.
- Fault after durable journal sync → reopen/recovery → exact post-state.
- Commit receipt replay идемпотентен.
- Tampered/missing/symlink staged payload → storage corruption и zero partial
  visible write.
- v0.1 fallback и v0.2 final state проходят staged validation.
- Existing Darwin/Linux and unsupported-platform matrix остаётся зелёной.

Не дублировать generic large-payload/provenance/no-follow tests без нового
v0.2 contract value.

## 4. Boundary

- Journal/receipt schema versions не менять.
- Runtime attestation receipt не хранить.
- `.okf/**` остаётся private/non-revision-visible.
- Asset-only change минимум отражается в `ChangedFiles`; reverse semantic
  `ChangedRefs` для computation dependencies является отдельным graph/store
  extension и не входит до появления public asset mutation.

## 5. Acceptance criteria

- Crash/recovery поддерживает Appendix-style v0.2 bundle byte-exact.
- Transaction protocol одинаков для v0.1/v0.2.
- `go test ./store/fs` и platform matrix проходят.
