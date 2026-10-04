---
lang: en
title: "OKF v0.2 graph projection profile"
permalink: /development/v0.2/tasks/task12/
---

{% include nav.html %}

# OKF v0.2 graph projection profile {#section001}

> Historical document. This plan is preserved from source revision `61e75e9`; it is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task12.md).

## 1. Objective {#section002}

Extend `graph` with projections of provenance, trust, lifecycle, and Attested
Computation without presenting the project-specific RDF/JSON-LD vocabulary as
part of upstream OKF.

Dependencies: [`task09.md`]({{ '/development/v0.2/tasks/task09/' | relative_url }}), [`task10.md`]({{ '/development/v0.2/tasks/task10/' | relative_url }}).

## 2. Contract boundary {#section003}

The upstream SPEC does not define a JSON-LD context, RDF ontology, or graph ABI.
The current `https://okf.io/ontology/v0.1#` is a toolkit contract, not a normative
part of OKF.

Before changing the namespace, create an ADR and a versioned projection contract:

- the legacy profile preserves byte-compatible v0.1 output;
- the new profile is explicitly labeled as `skosovsky/okf` projection v0.2;
- declared/effective OKF version and graph profile version are separate fields;
- existing predicates must not be silently renamed.

## 3. API {#section004}

Preserve existing `Render*` wrappers for compatibility and add an options/profile
API:

- effective specification version;
- projection profile;
- optional explicit `as_of` for derived staleness;
- extension relation policy.

`graph` receives typed data from `bundle`; it does not parse raw YAML again.

## 4. Projection {#section005}

JSON-LD and N-Triples must be able to express:

- generation actor/time and a legacy fallback provenance marker;
- normalized verification events;
- derived trust tier;
- effective status, stale date, and staleness evaluated at an explicit date;
- sources, credibility signals, and shared/per-source usage windows;
- keyed claim attribution;
- Attested Computation runtime, parameters, computation/executor/attester
  resources, and declared receipt fields;
- local referenced assets as separate resource nodes when the path is resolvable;
- declared/effective OKF version and compatibility mode.

Preserve unknown runtimes/parameter types as literals. Do not convert unknown
frontmatter into invented predicates; if needed, provide a separate lossless
raw/frontmatter extension payload only in a documented profile.

## 5. Edges and extensions {#section006}

- Markdown links remain untyped `references`.
- `sources[].resource` and computation/executor/attester paths receive only
  explicit project-profile predicates.
- Scope descriptors and external URLs are not bundle nodes.
- Broken local paths remain visible with `exists=false`.
- YAML `relations` remains a separately labeled toolkit extension.
- `sources[].id` does not become a subresource fragment.
- The graph renderer executes nothing and does not access the network.

Text/DOT/Mermaid may retain topology-only output; add metadata annotations only
through an explicit option to avoid breaking snapshots.

## 6. Determinism {#section007}

- Stable ordering of concepts, sources, verifications, parameters, receipts, and
  edges.
- Bare/list verified produce the same projection.
- Encode empty collections consistently according to the profile.
- Dates/timestamps receive correct RDF datatypes only after valid typed parsing.
- All renderers propagate writer/short-write errors.

## 7. Files {#section008}

- `graph/jsonld.go`;
- `graph/ntriples.go`;
- `graph/text.go`, `dot.go`, `mermaid.go` only for options wiring;
- new `profile.go`, `projection.go`;
- package tests and CLI/MCP contract fixtures;
- ADR/documentation for the projection profile.

## 8. Tests {#section009}

All tests use AAA.

- v0.1 legacy output is byte-compatible.
- Complete Appendix A v0.2 projection.
- All trust tiers and bare/list verified.
- Missing status/default stable and staleness boundary.
- Shared/per-source usage windows.
- Unknown source IDs/runtimes/types.
- Local/external/scope/broken resources.
- `sources[].id` does not create a fragment.
- Extension relations are explicitly labeled.
- Deterministic output for shuffled input.
- JSON-LD parses; N-Triples escaping/datatype/IRI tests.
- Writer failure for each renderer.

## 9. Acceptance criteria {#section010}

- v0.1 consumers can choose the legacy profile.
- v0.2 signal families are available in a documented JSON-LD/N-Triples profile.
- The namespace/profile is not claimed to be an upstream standard.
- CLI and MCP use one graph implementation.
- No execution/network side effects.
- `go test ./graph` and `go test ./...` pass.

## 10. Out of scope {#section011}

Official ontology standardization, runtime execution/attestation, semantic score,
network dereferencing, and inference of new relation types from prose.
