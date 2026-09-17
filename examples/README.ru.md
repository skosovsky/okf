# Примеры OKF v0.2

Executable examples хранятся в общем
[`fixtures/v02/corpus.yaml`](../fixtures/v02/corpus.yaml), а не копируются
сюда.

Начинай с:

- `positive/minimal` для type-only base contract;
- `positive/canonical` для human authoring, keyed sources, bare/list
  verification, lifecycle states и inline/file computation;
- `positive/appendix-a` для pinned end-to-end example;
- `compat/*` для intentional v0.1, mixed и future consumption;
- `adversarial/optional-shapes` для malformed optional-family guidance.

Все синтетические примеры repo используют reserved domains вроде
`example.invalid`. Computation, executor и attester files — inert fixture data,
а не executable runtime contract.

Для migration используй [migration guide](../docs/ru/migration.md). Не копируй
legacy fields в новый v0.2 authoring.
