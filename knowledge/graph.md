---
type: Architecture Note
title: Graph projections
description: Why a graph profile is separate from an OKF version.
status: draft
sources:
  - id: adr
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/adr/0001-okf-v02-graph-projection.md
    title: Graph projection decision
  - id: projection
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/graph/projection.go
    title: Toolkit graph projection
---

# Is the JSON-LD vocabulary part of OKF?

The upstream OKF v0.2 document contract does not define an RDF ontology or JSON-LD context. This toolkit exposes an explicit `skosovsky/okf` graph profile and retains a legacy profile; declared/effective OKF version and graph profile version are separate.[^adr][^projection] The projection draws from typed bundle data. Unknown YAML does not turn into invented predicates, and rendering does not run code or fetch network resources.[^adr]

For computation nodes, see [Inert computations](computations.md).

[^adr]: ADR 0001, scope and compatibility decision.
[^projection]: `graph/projection.go`, explicit projection metadata.
