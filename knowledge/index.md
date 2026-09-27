---
okf_version: "0.2"
---

# Reading and conformance

* [Package boundaries](architecture.md) - Which Go package owns each part of the toolkit.
* [Version resolution](versions.md) - How OKF document versions differ from package releases and how readers select a contract.
* [Validation policy](validation.md) - Which findings are conformance errors and which checks are optional policy.

# Changes and durability

* [Mutation boundary](mutation.md) - How an edit moves from preview to an authorized apply.
* [Durable store](store.md) - What the filesystem store promises and where its private state lives.
* [Supported platforms](platforms.md) - Where the durable filesystem backend can run.

# Interfaces and publication

* [MCP contracts](mcp.md) - Which stdio tools expose reading and safe writes.
* [Graph projections](graph.md) - Why a graph profile is separate from an OKF version.
* [Inert computations](computations.md) - Why computation metadata does not authorize execution.
* [Release evidence](release.md) - What CI and a release record must prove.
