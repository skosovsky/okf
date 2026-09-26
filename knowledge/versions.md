---
type: Architecture Note
title: Version resolution
description: How OKF document versions differ from package releases and how readers select a contract.
status: draft
sources:
  - id: version-code
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/bundle/version.go
    title: Bundle version resolver
  - id: toolkit
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/toolkit.md
    title: Toolkit guide
---

# Which version controls this bundle?

The root `index.md` declares the OKF document contract with `okf_version`; a Go module release or plugin package version does not select that contract.[^toolkit] The resolver keeps the declared version, effective reader version, selection source, and compatibility mode separately. An absent declaration defaults to v0.2; a supported explicit selector is an assertion, and a conflict fails. A canonical unknown future declaration remains visible while the reader uses best-effort v0.2 semantics.[^version-code]

This bundle declares `0.2` in its root index. See [Validation policy](validation.md) for malformed declarations.

[^version-code]: `bundle/version.go`, `VersionResolution` and `ResolveVersion`.
[^toolkit]: Toolkit guide, version axes and resolution rules.
