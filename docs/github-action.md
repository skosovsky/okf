# GitHub Action: validate an OKF bundle

Use the repository Action at an immutable commit SHA. The Action builds the Go toolkit from that exact revision and runs the same `okf validate` command used locally. It does not download another validator release.

```yaml
permissions:
  contents: read

steps:
  - uses: actions/checkout@fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09 # v5
  - id: okf
    uses: skosovsky/okf@0f15ab4091203f11caa7fb0b9e02da47d719a051
    with:
      path: knowledge
      spec: auto
      strict: 'true'
      check-links: 'true'
      check-orphans: 'true'
      as-of: '2026-09-26'
      max-warnings: '0'
  - if: always() && steps.okf.outputs['report-path'] != ''
    uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
    with:
      name: okf-validation
      path: ${{ steps.okf.outputs['report-path'] }}
```

The example pins the implementation commit in this branch. It becomes usable from consuming repositories after that commit is published to GitHub; update its SHA when adopting later changes. A workflow in this repository exercises `uses: ./` against seven fixtures. In a consuming repository, `path` is resolved relative to `GITHUB_WORKSPACE`; absolute paths are accepted. Inputs are passed as arguments to Go, so spaces, quotes, and shell metacharacters remain path data.

| Input | Default | Meaning |
| --- | --- | --- |
| `path` | `.` | Bundle directory |
| `spec` | `auto` | Version selector: `auto`, `0.1`, or `0.2` |
| `temporal-profile` | empty | Temporal revision; empty uses the CLI default (`date-3fcbb9f`) |
| `strict` | `false` | Additional advisory checks; it does not make every warning an error |
| `check-links` | `false` | Additional Markdown link checks |
| `check-orphans` | `false` | Additional index coverage checks |
| `as-of` | empty | Reference date or offset datetime according to the selected temporal revision |
| `max-warnings` | empty | Optional warning budget; `0` rejects any warning |

`max-warnings=N` passes when the report has at most N warnings. Exceeding N fails the CI step through a separate toolkit policy. The bundle's `conformant` value still reports base OKF conformance. `strict` merely enables more checks. Base errors take precedence in diagnosis: a report can contain both base errors and a warning-budget policy failure, but the Action produces one `validation_failure` outcome. Operational errors (bad inputs, missing directory, I/O failure, incomplete report) produce `operational_failure` with no JSON report. Existing CLI exit codes remain 0 for passing validation and 1 for validation or operational failure; the Action distinguishes the latter two by whether the CLI produced a complete JSON report.

The Action invokes the validator once and exposes:

- `outcome`: `pass`, `validation_failure`, or `operational_failure`.
- `report`: complete JSON when at most 60,000 bytes; otherwise empty. It is also empty after operational failure.
- `report-path`: full JSON report in a temporary file on the same runner, including validation failures. Upload it in a later step with `if: always()` when durable CI evidence is needed. The path is empty after operational failure.

For local parity, run `go run ./cmd/okf validate --path knowledge --spec auto --strict --check-links --check-orphans --as-of 2026-09-26 --max-warnings 0 --format json`. Compare this output with the Action's `report-path` file for the same bundle snapshot and flags. A failed validation still writes JSON to stdout. An operational error writes to stderr and has no JSON report.
