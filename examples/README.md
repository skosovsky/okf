# OKF v0.2 examples

Executable examples are stored in the shared
[`fixtures/v02/corpus.yaml`](../fixtures/v02/corpus.yaml) instead of being
copied here.

Start with:

- `positive/minimal` for the type-only base contract;
- `positive/canonical` for human authoring, keyed sources, bare/list
  verification, lifecycle states, and inline/file computation;
- `positive/appendix-a` for the pinned end-to-end example;
- `compat/*` for intentional v0.1, mixed, and future consumption;
- `adversarial/optional-shapes` for malformed optional-family guidance.

All repository-authored synthetic examples use reserved domains such as
`example.invalid`. Computation, executor, and attester files are inert fixture
data; they are not an executable runtime contract.

For migration, use [the migration guide](../docs/migration.md). Do not copy
legacy fields into new v0.2 authoring.
