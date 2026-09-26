---
type: Architecture Note
title: Package boundaries
description: Which Go package owns each part of the toolkit.
status: draft
sources:
  - id: toolkit
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/toolkit.md
    title: Toolkit guide
  - id: module
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/go.mod
    title: Go module declaration
---

# Where should an implementation change go?

This is a Go module with separate surfaces for bundle parsing, validation, graph projection, mutation, durable storage, CLI, and MCP. Start from the contract owned by the relevant package; a CLI or MCP adapter should expose it rather than inventing different OKF semantics.[^toolkit] The module path is `github.com/skosovsky/okf`.[^module]

For a version-selection change, see [Version resolution](versions.md). For a write, follow the [Mutation boundary](mutation.md) into the [Durable store](store.md).

[^toolkit]: Toolkit guide, package and surface map.
[^module]: Go module declaration.
