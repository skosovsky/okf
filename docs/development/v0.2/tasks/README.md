# OKF v0.2 implementation program

This directory is the tracked source of truth for the task09–21 program. The
specifications were moved from the locally ignored `.cursor` directory after
implementation so that requirements and acceptance evidence remain reviewable.

| Task | Contract | Primary implementation | Acceptance evidence |
| --- | --- | --- | --- |
| [09](task09.md) | v0.2 baseline and roadmap | `docs/contracts`, `fixtures/v02` | root contract and corpus tests |
| [10](task10.md) | typed bundle model | `bundle` v0.2/version/observation APIs | bundle v0.2 and context suites |
| [11](task11.md) | version-aware validation | `validator/strict_v02.go` | strict v0.2 and cancellation-integrity suites |
| [12](task12.md) | graph projection profile | `graph/profile.go`, `graph/projection.go` | graph contract/profile tests |
| [13](task13.md) | lossless YAML engine | structural mutation planner/renderers | YAML ownership, shadow-state, and fuzz suites |
| [14](task14.md) | semantic mutations | v0.2 operation descriptors and handlers | constructor, operation, and preview matrices |
| [15](task15.md) | v0.1 → v0.2 migration | shared migration planner/proof | migration resolution, replay, computation, and durability suites |
| [16](task16.md) | store compatibility | additive ChangeSet v1 operations | compatibility guardrail golden tests |
| [17](task17.md) | durable filesystem protocol | `store/fs` claim/recovery protocol | durability boundary and recovery matrices |
| [18](task18.md) | bounded journal decoding | immutable format ceilings plus reopen policy | staged payload limit suite |
| [19](task19.md) | version-aware CLI | `internal/okfcli` | CLI package and binary E2E tests |
| [20](task20.md) | schema-first MCP | checked-in contracts and safe write adapters | MCP contract/protocol/security suites |
| [21](task21.md) | skill, docs, examples, packaging | EN/RU docs, pinned skill references, fixtures | docs, corpus, link, and skill-lock tests |

The historical test-reorganization evidence is stored next to this program in
[`../test-reorganization-v1.json`](../test-reorganization-v1.json). A passing
repository test validates the manifest against the current Go test inventory.
