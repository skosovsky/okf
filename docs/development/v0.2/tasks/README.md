---
layout: default
lang: en
title: "OKF v0.2 implementation program"
permalink: /development/v0.2/tasks/README/
---

{% include nav.html %}

Historical implementation specification, preserved from revision `61e75e9`. Requirements describe the work planned at that time, not current usage instructions. [Original document](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/README.md).

# OKF v0.2 implementation program {#section001}

This directory is the tracked source of truth for the task09–21 program. The
specifications were moved from the locally ignored `.cursor` directory after
implementation so that requirements and acceptance evidence remain reviewable.

| Task | Contract | Primary implementation | Acceptance evidence |
| --- | --- | --- | --- |
| [09]({{ '/development/v0.2/tasks/task09/' | relative_url }}) | v0.2 baseline and roadmap | `docs/contracts`, `fixtures/v02` | root contract and corpus tests |
| [10]({{ '/development/v0.2/tasks/task10/' | relative_url }}) | typed bundle model | `bundle` v0.2/version/observation APIs | bundle v0.2 and context suites |
| [11]({{ '/development/v0.2/tasks/task11/' | relative_url }}) | version-aware validation | `validator/strict_v02.go` | strict v0.2 and cancellation-integrity suites |
| [12]({{ '/development/v0.2/tasks/task12/' | relative_url }}) | graph projection profile | `graph/profile.go`, `graph/projection.go` | graph contract/profile tests |
| [13]({{ '/development/v0.2/tasks/task13/' | relative_url }}) | lossless YAML engine | structural mutation planner/renderers | YAML ownership, shadow-state, and fuzz suites |
| [14]({{ '/development/v0.2/tasks/task14/' | relative_url }}) | semantic mutations | v0.2 operation descriptors and handlers | constructor, operation, and preview matrices |
| [15]({{ '/development/v0.2/tasks/task15/' | relative_url }}) | v0.1 → v0.2 migration | shared migration planner/proof | migration resolution, replay, computation, and durability suites |
| [16]({{ '/development/v0.2/tasks/task16/' | relative_url }}) | store compatibility | additive ChangeSet v1 operations | compatibility guardrail golden tests |
| [17]({{ '/development/v0.2/tasks/task17/' | relative_url }}) | durable filesystem protocol | `store/fs` claim/recovery protocol | durability boundary and recovery matrices |
| [18]({{ '/development/v0.2/tasks/task18/' | relative_url }}) | bounded journal decoding | immutable format ceilings plus reopen policy | staged payload limit suite |
| [19]({{ '/development/v0.2/tasks/task19/' | relative_url }}) | version-aware CLI | `internal/okfcli` | CLI package and binary E2E tests |
| [20]({{ '/development/v0.2/tasks/task20/' | relative_url }}) | schema-first MCP | checked-in contracts and safe write adapters | MCP contract/protocol/security suites |
| [21]({{ '/development/v0.2/tasks/task21/' | relative_url }}) | skill, docs, examples, packaging | EN/RU docs, pinned skill references, fixtures | docs, corpus, link, and skill-lock tests |

The historical test-reorganization evidence is stored next to this program in
[`../test-reorganization-v1.json`](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/test-reorganization-v1.json). A passing
repository test validates the manifest against the current Go test inventory.
