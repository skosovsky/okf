---
title: Open Knowledge Format v0.2
description: OKF v0.2 format, toolkit, migration, and agent workflow.
---

{% include nav.html %}

# Open Knowledge Format v0.2

OKF stores portable knowledge as Markdown concepts with YAML frontmatter. This
repository implements the v0.2 read/write contract while retaining explicit
v0.1 consumption and migration.

![Example OKF bundle](images/01-hero.png?v=20260729){: .hero-image }

## Spec {#spec}

The normative contract is the
[pinned upstream specification](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/spec-v02.md)
at commit `3fcbb9f828c2f23d109c855ee403c3a4c81f3a96`.

Base conformance is deliberately small:

1. Every concept is UTF-8 Markdown with parseable YAML frontmatter.
2. Every concept has a non-empty string `type`.
3. Reserved `index.md` and `log.md` files follow their defined structure.

Provenance, trust, lifecycle, and Attested Computation fields are optional.
Missing optional data and unknown keys/types/runtimes are consumable.
An absent root version may use the v0.2 traversal default. A present malformed
`okf_version` instead fails reserved-index conformance; unsupported canonical
future versions remain declared and are read best-effort.

## Quickstart {#quickstart}

```text
knowledge/
├── index.md
└── minimal.md
```

`index.md`:

```markdown
---
okf_version: "0.2"
---

# Concepts

* [Minimal](minimal.md) - Minimal conformant v0.2 concept.
```

`minimal.md`:

```markdown
---
type: Unknown Producer Type
---

Minimal conformant v0.2 concept.
```

Validate:

```sh
okf validate --path ./knowledge --spec auto
```

Use strict guidance and a deterministic staleness date for review:

```sh
okf validate --path ./knowledge --spec auto --strict --as-of 2026-07-29
```

Validation is not verification. Add `verified` only after checking the concept
against its sources or resource.

## Provenance, trust, and lifecycle

`sources` records actual materials. Body claims use Markdown footnotes keyed by
`sources[].id`. `generated` identifies the known producer of current content;
`verified` records separate checks.

Consumers derive trust only from `verified`:

- absent: unverified;
- non-human verifiers only: machine-confirmed;
- any `human:<id>` verifier: human-reviewed.

Trust, `status`, and staleness are separate signals. A deprecated concept may be
human-reviewed; a stable concept may be unverified; a verified concept may be
stale.

## Attested Computation

A sanctioned computation is a standalone `type: Attested Computation` concept,
linked from narrative concepts. Its computation is either one inline fence
under `# Computation` or one file path.

The contract is inert. This toolkit does not execute a referenced resource
without a separately trusted runtime and authorization. v0.2 deliberately
defers binding, runtime packaging, receipt/verdict wire format, attester ABI,
sandboxing, and caching.

Per the pinned spec, an agent MAY supply only values for declared parameters
and MUST NOT author or edit the sanctioned computation.

## Examples {#examples}

Canonical examples are parsed from
[`fixtures/v02/corpus.yaml`](https://github.com/skosovsky/okf/blob/main/fixtures/v02/corpus.yaml). The corpus includes
minimal, Appendix A, verification shapes, lifecycle, inline/file computation,
narrative links, v0.1 compatibility, mixed provenance, future version, and
adversarial cases.

Repository-authored synthetic URLs use reserved domains such as
`example.invalid`.

## Tools {#tools}

- [Toolkit]({{ '/toolkit/' | relative_url }}): CLI and Go surfaces.
- [Agent skill]({{ '/skill/' | relative_url }}): authoring, consumption, and guardrails.
- [Migration]({{ '/migration/' | relative_url }}): explicit v0.1 → v0.2 workflow.

YAML `relations` is a `skosovsky/okf` extension/tooling policy. It is not part of
the upstream v0.2 spec.

## FAQ {#faq}

### Does every concept need provenance?

No. `type` is the only always-required field. Missing provenance means
provenance is unknown, not that the concept is invalid.

### Is a human-authored concept human-reviewed?

No. `generated.by: human:...` identifies authorship. Human-reviewed requires an
actual human event in `verified`.

### Can the bundle run executor or attester content?

Not by virtue of being OKF. Resource fields are inert data. Execution needs a
trusted runtime and authorization outside this format/toolkit.

### What happens to v0.1?

It remains an intentional read/validate/migration source. Legacy fallback never
performs hidden migration. See the [migration guide]({{ '/migration/' | relative_url }}).
