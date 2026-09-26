---
type: Architecture Note
title: MCP contracts
description: Which stdio tools expose reading and safe writes.
status: draft
sources:
  - id: toolkit
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/toolkit.md
    title: Toolkit MCP guide
  - id: contracts
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/internal/mcpserver/contracts.go
    title: MCP tool contracts
  - id: current-tools
    resource: ../internal/mcpserver/server.go
    title: Current Go MCP tool registration
  - id: query-contract
    resource: ../docs/contracts/concept-queries.md
    title: Current MCP concept query contract
  - id: upgrade-contract
    resource: ../internal/mcpserver/contracts/apply_temporal_upgrade.input.schema.json
    title: Current temporal upgrade apply contract
---

# Which MCP operation should an agent call?

The stdio server has thirteen tools.[^current-tools] The original five read/write operations (`list_concepts`, `read_concept`, `validate_bundle`, `get_semantic_graph`, `write_concept`) and the preview/apply pairs for concept patches and v0.2 migrations remain available.[^toolkit][^contracts] Two read-only operations add bounded concept search (`search_concepts`) and incoming/outgoing neighbors (`get_neighbors`). Search ranks literal matches in ID and metadata ahead of body matches. Neighbors keep Markdown navigation, typed relations, and provenance source links distinct. Both report truncation and use a declared temporal profile when freshness is requested.[^query-contract] A third preview/apply pair upgrades a pinned bundle from date to instant temporal semantics.[^current-tools][^upgrade-contract] Structured outputs follow checked-in schemas; apply operations require their respective preview evidence.[^contracts][^upgrade-contract]

Use [Version resolution](versions.md) when selecting a reader contract and [Mutation boundary](mutation.md) for write safety.

[^toolkit]: Toolkit guide, MCP tool list and proof rules.
[^contracts]: MCP tool schema definitions.
[^current-tools]: Current Go server registration, source path relative to this concept. Replace with an immutable commit link after publication.
[^query-contract]: Checked-in query contract, source path relative to this concept.
[^upgrade-contract]: Checked-in temporal upgrade apply schema, source path relative to this concept.
