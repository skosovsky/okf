# OKF examples

## First sourced rule

[English files](project-knowledge/en/index.md) contain fictional requirements: retry delivery for 24 hours, then involve an operator. `retry-policy.md` cites `source-material.md`. The English and Russian folders use the same IDs and filenames.

[Open the HTML example](https://skosovsky.github.io/okf/demo/project-en.html#retry-policy) · [Run the walkthrough locally](../docs/quickstart.md)

First copy the folder to a temporary directory, then find the rule, inspect its source, and manually change the deadline to 48 hours in both documents. Keep the repository template unchanged while following the walkthrough.

## Repository architecture

[knowledge/](../knowledge/index.md) describes this repository and cites real sources. Notes remain in English. Start with [Package boundaries](https://skosovsky.github.io/okf/demo/knowledge.html#architecture). [Reading and upkeep guide](../docs/knowledge.md).

## Implementation test data

[fixtures/v02/corpus.yaml](../fixtures/v02/corpus.yaml) lists:

- `positive/minimal`: minimum required fields.
- `positive/canonical`, `positive/appendix-a`: sources, verification, lifecycle, and computation.
- `compat/*`: 0.1, mixed provenance, and future-version reading.
- `adversarial/optional-shapes`: malformed optional fields.

These fixtures test the implementation rather than replace the user guide. URLs such as `example.invalid` are synthetic test addresses. Computation, executor, and attester resources are inert test data. For old documents use the [migration guide](../docs/migration.md).
