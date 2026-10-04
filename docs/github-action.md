---
title: "Validation in GitHub Actions"
description: "Validation in GitHub Actions"
permalink: /github-action/
---

{% include nav.html %}

<span id="github-action-validate-an-okf-bundle"></span>

# Validate knowledge in GitHub Actions {#page-top}

If a bundle lives in your repository, check it alongside code. You need a GitHub repository with `knowledge/` and permission to edit its workflow. First confirm local validation passes, then add these steps to a CI job.

## Configure the steps {#configure}

This example pins OKF to published revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. The Action builds the Go program from that revision and runs the same validator as locally; it does not download a different release.

{% raw %}
```yaml
permissions:
  contents: read

steps:
  - uses: actions/checkout@fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09 # v5
  - id: okf
    uses: skosovsky/okf@61e75e9aa9a8719dfb480bf1f5226553b3a21d70
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
{% endraw %}

Expected result for a valid bundle: a successful step and a JSON `okf-validation` artifact. Structural failures or excess warnings fail the step while leaving a report available to upload. `path` resolves against `GITHUB_WORKSPACE`; absolute paths are accepted. Spaces, quotes, and shell metacharacters remain path data.

## Choose checks {#inputs}

| Input | Default | Meaning |
| --- | --- | --- |
| `path` | `.` | Bundle folder |
| `spec` | `auto` | Version selector: `auto`, `0.1`, `0.2` |
| `temporal-profile` | empty | Temporal revision; default `date-3fcbb9f` |
| `strict` | `false` | Additional metadata guidance |
| `check-links` | `false` | Markdown link checks |
| `check-orphans` | `false` | Index coverage checks |
| `as-of` | empty | Date or offset timestamp for the selected revision |
| `max-warnings` | empty | Warning budget; `0` rejects every warning |

`max-warnings=N` permits at most N warnings. This is a separate CI policy; `conformant` still reports base OKF conformance. `strict` enables additional checks. Structural errors take priority in diagnosis when multiple issues occur.

## Read the result {#outputs}

- `outcome`: `pass`, `validation_failure`, or `operational_failure`.
- `report`: complete JSON if no larger than 60,000 bytes; otherwise empty. Also empty on operational failure.
- `report-path`: temporary path to the full report on the same runner, including validation failures. Upload it with `if: always()`; the path is empty after operational failure.

Invalid inputs, a missing folder, I/O failure, or an incomplete report produce `operational_failure`. CLI exits 0 on success and 1 for either failure class; the Action distinguishes them by the presence of a complete JSON report.

## Compare with a local run {#troubleshooting}

```sh
go run ./cmd/okf validate --path knowledge --spec auto --strict --check-links --check-orphans --as-of 2026-09-26 --max-warnings 0 --format json
```

Use the same files, program revision, and flags. Validation failures still print JSON to stdout; operational failures write stderr without JSON. A workflow in this repository tests `uses: ./` on seven fixtures. The full input contract is in [action.yml](https://github.com/skosovsky/okf/blob/main/action.yml).
