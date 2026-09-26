---
type: Architecture Note
title: Durable store
description: What the filesystem store promises and where its private state lives.
status: draft
sources:
  - id: fs
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/store/fs/fs.go
    title: Filesystem store implementation
  - id: toolkit
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/toolkit.md
    title: Toolkit guide
---

# What survives a failed or repeated write?

The filesystem backend coordinates writes with an advisory root-inode lock and uses a journal to complete an unfinished transaction to its recorded post-state before the next store observation or mutation.[^fs] The `.okf` directory inside an opened bundle holds private staging and receipt metadata; it is not a place to author concepts.[^fs] Preview and apply are bound to revision and plan evidence, and committed results carry store receipts distinct from computation receipts.[^toolkit]

Read the [Mutation boundary](mutation.md) before calling commit and [Supported platforms](platforms.md) before choosing a backend.

[^fs]: `store/fs/fs.go`, package contract and private directory.
[^toolkit]: Toolkit guide, mutation durability and receipt distinction.
