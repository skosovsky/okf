---
type: Architecture Note
title: Validation policy
description: Which findings are conformance errors and which checks are optional policy.
status: draft
sources:
  - id: matrix
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/contracts/okf-v0.2-conformance.md
    title: OKF v0.2 conformance matrix
  - id: toolkit
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/toolkit.md
    title: Toolkit guide
---

# Does a warning make a bundle nonconformant?

Base errors concern concept frontmatter and `type`, plus structure of present reserved `index.md` and `log.md`. Optional provenance, lifecycle, computation and unknown extension fields do not become base errors solely because they are absent or unfamiliar.[^matrix] Strict validation checks present v0.2 families as guidance; links and orphan checks are opt-in. Staleness needs an explicit reference time, making CI results reproducible.[^toolkit]

The source of truth for a diagnostic code is the [conformance matrix](https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/contracts/okf-v0.2-conformance.md). See [Version resolution](versions.md) before interpreting a bundle.

[^matrix]: Conformance matrix, base and strict diagnostic boundaries.
[^toolkit]: Toolkit guide, validation commands and flags.
