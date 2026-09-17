# ADR 0001: toolkit graph projection for OKF v0.2

Status: accepted.

## Context

The upstream OKF v0.2 specification does not define an RDF ontology, JSON-LD
context or graph ABI. The existing `https://okf.io/ontology/v0.1#` namespace is
a toolkit contract and cannot be presented as an upstream standard.

## Decision

The graph package keeps the legacy profile byte-compatible and introduces an
explicit `skosovsky/okf` v0.2 projection profile. OKF declared/effective
versions and the graph profile version are separate values. Existing predicates
are not silently renamed.

The v0.2 profile projects only typed bundle data: provenance, attribution,
verification, derived trust/lifecycle state and Attested Computation contract
fields. It may mark YAML `relations` as a toolkit extension. Unknown
frontmatter is not translated into invented predicates. Local assets may
become resource nodes; external URLs and scope descriptors do not become bundle
nodes. Missing local resources remain visible with `exists=false`.

Legacy `Render*` wrappers retain legacy output. Metadata projection requires an
explicit profile/options API. No renderer performs network access or executes
bundle content.

## Consequences

Consumers can opt into richer deterministic JSON-LD or N-Triples without
mistaking the vocabulary for upstream OKF. Legacy snapshots remain stable.
