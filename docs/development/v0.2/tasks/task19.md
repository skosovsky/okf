---
lang: en
title: "Technical specification: the okf CLI for OKF v0.2"
permalink: /development/v0.2/tasks/task19/
---

{% include nav.html %}

> Historical document from source revision `61e75e9`. This implementation plan is archived for reference and is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task19.md).

# Technical specification: CLI `okf` for OKF v0.2 {#section001}

## 1. Goal {#section002}

Make every CLI read and write interface version-aware, expose v0.2 signals, and
integrate a single migration planner without domain logic in `main.go`.

Dependencies: [`task09.md`]({{ '/development/v0.2/tasks/task09/' | relative_url }})–[`task15.md`]({{ '/development/v0.2/tasks/task15/' | relative_url }}).

## 2. Architecture {#section003}

Keep `cmd/okf/main.go` as the bootstrap. Move command orchestration into
`internal/okfcli`:

- dispatch/args/help/version;
- validate/info/parse/fmt/index/graph/migrate;
- stable text/JSON DTOs and renderers.

Version resolution, trust, staleness, provenance, graph, and migration come
from domain packages. The CLI does not traverse raw YAML.

## 3. Shared selector {#section004}

For bundle commands:

```text
--spec auto|0.1|0.2
```

The default is `auto`; resolution follows task09. An explicit selector that
conflicts with the declaration is a failure/assertion mismatch, not a silent override.

## 4. Commands {#section005}

### `version` {#section006}

Text reports the CLI version separately from the supported/default OKF specifications.
Add `--json`:

```json
{
  "cli_version": "vX.Y.Z",
  "okf_spec_default": "0.2",
  "okf_spec_supported": ["0.1", "0.2"]
}
```

### `validate` {#section007}

Preserve existing flags and output fields; add:

- declared/effective version;
- resolution/compatibility;
- explicit `--as-of YYYY-MM-DD` for strict staleness checks;
- stable diagnostic codes and field paths.

v0.2 must not produce false positives for timestamp/Citations.

### `info` {#section008}

Add deterministic counts:

- trust tiers;
- lifecycle status;
- stale at `--as-of`;
- sources;
- Attested Computation;
- parse errors;
- declared/effective version.

### `parse` {#section009}

Typed text/JSON projection:

- generated/verified/trust;
- status/staleness;
- sources/attributions;
- legacy fallback marker;
- computation contract summary.

Bare and list forms of verified produce the same result.

### `fmt` {#section010}

Remain parsing plus canonical serialization:

- do not migrate timestamp/Citations;
- preserve unknown keys;
- do not add generated/version;
- do not perform hidden writes without `-w`.

### `index` {#section011}

- preserve the 0.1/0.2/future root version;
- do not stamp or bump the version;
- nested indexes have no frontmatter.

### `graph` {#section012}

Pass an explicit projection profile and options to package `graph`; stdout contains
only the selected graph payload. Preserve legacy `--dot`.

### `migrate` {#section013}

```text
okf migrate <bundle> --to 0.2
  [--from auto|0.1]
  [--actor <actor>]
  [--write]
  [--format text|json]
```

Default to a dry run. Delegate to the task15 planner; do not perform independent YAML/file
edits. Apply uses a revision/digest-bound atomic store commit.

## 5. stdout/stderr/exit {#section014}

- `0`: successful command, conformant validation, valid dry run/apply.
- `1`: conformance failure, migration blocker, usage/operational error.
- Reports/payload → stdout.
- Usage/I/O errors → stderr with `error:`.
- JSON mode → one JSON document without banners/ANSI.
- Successful commands write nothing to stderr.
- A migration dry run with a diff returns `0`.

## 6. Tests {#section015}

All tests follow AAA.

- Version text/JSON.
- Declared 0.1/0.2, absent, future, explicit conflict.
- Strict v0.2 without a timestamp warning.
- Legacy fallback.
- Trust/status/staleness boundary.
- Deterministic text/JSON.
- JSON stdout/stderr purity.
- v0.2 fmt semantic round-trip + unknown keys.
- Index preserves versions.
- Graph profile selection.
- Flag order, `--`, unknown/duplicate/extra args.
- Migration dry run/noop/conflict/one bad file/atomic recovery.
- Binary E2E over canonical fixtures.

## 7. Acceptance criteria {#section016}

- The CLI accurately reports supported/default specifications.
- All bundle commands use one resolution contract.
- v0.1 remains consumable.
- info/parse expose v0.2 signals.
- fmt/index perform no hidden migration.
- migrate is transactional/idempotent.
- `main.go` contains no domain logic.
- `go test ./...` and binary E2E pass.

## 8. Out of scope {#section017}

Execution/attestation runtime, LLM conversion of arbitrary SQL/prose, and changes
to custom `relations`.
