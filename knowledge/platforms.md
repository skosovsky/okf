---
type: Architecture Note
title: Supported platforms
description: Where the durable filesystem backend can run.
status: draft
sources:
  - id: release
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/release-engineering.md
    title: Release engineering contract
  - id: fs
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/store/fs/fs.go
    title: Filesystem store platform contract
---

# Can this host use the durable store?

The `store/fs` durable backend targets Darwin and Linux, subject to filesystem capability checks. Windows and other targets are compile-safe, but `Open` and `OpenContext` return `*fs.UnsupportedPlatformError` rather than claiming durability.[^fs][^release] Callers can inspect it with `errors.Is(err, fs.ErrUnsupportedPlatform)` and `errors.As`.[^release]

This concerns the [Durable store](store.md), not whether the read-only Go packages can compile.

[^fs]: `store/fs/fs.go`, package comment and unsupported-platform error.
[^release]: Release engineering contract, platform matrix.
