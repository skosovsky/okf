---
type: Architecture Note
title: Mutation boundary
description: How an edit moves from preview to an authorized apply.
status: draft
sources:
  - id: toolkit
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/toolkit.md
    title: Toolkit guide
  - id: change
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/store/change.go
    title: ChangeSet operation contract
  - id: adr
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/adr/0002-changeset-v1-additive-operation-tags.md
    title: ChangeSet v1 operation-tag decision
---

# Where is the write boundary?

A proposed edit becomes a typed `store.ChangeSet` operation, then a preview tied to an expected revision and plan digest, followed by an authorized apply through the store. The MCP patch path requires that preview evidence for apply.[^toolkit][^change] The Go operation union accepts documented concrete variants; unknown variants are rejected.[^adr]

ChangeSet format v1 remains the canonical envelope for request digests, journals, receipts and idempotency; adding distinct v0.2 operation tags did not change the envelope.[^adr] Follow the [Durable store](store.md) for commit and recovery behavior.

[^toolkit]: Toolkit guide, mutation and MCP apply contracts.
[^change]: `store/change.go`, operation types and validation.
[^adr]: ADR 0002, ChangeSet v1 compatibility decision.
