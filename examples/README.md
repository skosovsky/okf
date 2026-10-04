# Examples

Start with [the repository knowledge demo](https://skosovsky.github.io/okf/demo/knowledge.html#architecture). **Package boundaries** answers “Where should an implementation change go?” and links to sources and related notes. The source files are in [knowledge/](../knowledge/index.md).

The [quickstart](../docs/quickstart.md) reproduces the viewer and creates a two-note fictional service example from supplied requirements. That exercise is explicitly synthetic; the repository knowledge instead cites actual repository files.

For implementation testing, [fixtures/v02/corpus.yaml](../fixtures/v02/corpus.yaml) covers:

- `positive/minimal`: required fields.
- `positive/canonical` and `positive/appendix-a`: sources, verification, lifecycle, and computation metadata.
- `compat/*`: v0.1, mixed provenance, and future version reading.
- `adversarial/optional-shapes`: malformed optional fields.

Synthetic fixture URLs use reserved domains such as `example.invalid`. Computation, executor, and attester files are inert test data. See the [migration guide](../docs/migration.md) before converting legacy documents.
