---
type: Architecture Note
title: Inert computations
description: Why computation metadata does not authorize execution.
status: draft
sources:
  - id: matrix
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/contracts/okf-v0.2-conformance.md
    title: OKF v0.2 conformance matrix
  - id: toolkit
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/toolkit.md
    title: Toolkit security boundary
---

# Does reading an Attested Computation execute it?

No. The bundle and graph APIs expose a typed definition only for the exact `type: Attested Computation`; they do not execute its executor or attester, issue a runtime receipt, or infer a verdict.[^matrix] Resource fields and actor metadata grant no filesystem, network, shell, secret, or authentication authority. A separate trusted runtime and authorization would be needed to execute anything described by a bundle.[^toolkit]

The [Graph projections](graph.md) can describe the contract without crossing this boundary.

[^matrix]: Conformance matrix, computation and execution boundaries.
[^toolkit]: Toolkit guide, security boundary.
