---
title: Release engineering
description: Supported platforms, deterministic CI gates, and release traceability for OKF.
permalink: /release-engineering/
---

# Release engineering

OKF releases use the repository's `CI` workflow as the executable quality
contract. It runs for pull requests, pushes to `main`, version tags, and manual
dispatches. The workflow grants read-only repository permissions and pins
third-party actions to immutable commit SHAs.

## Deterministic gates

The normal workflow runs:

- `go test ./...` and `go test -race ./...` on Linux;
- `go vet ./...`, `go mod verify`, `go mod tidy -diff`, and
  `git diff --check`;
- `go build ./...` on Linux;
- `go test ./store/fs` and `go build ./...` on Darwin;
- native Windows `go build ./...` plus targeted runtime tests proving the typed
  unsupported-platform result from `Open` and `OpenContext`;
- Android cross-build plus Android/iOS source-selection assertions proving that
  derived Go tags do not accidentally select the Darwin/Linux backend.

For a version-tag push, CI additionally requires the tagged commit to be
reachable from `main`. Because GitHub supplies a zero `before` SHA when a tag is
created, the whitespace gate compares the complete tagged tree with Git's empty
tree instead of silently reducing the range check to a clean-worktree no-op.

Long fuzz campaigns and 10,000-concept profiles remain reproducible manual
gates documented in the parser-backed mutation evidence. They are deliberately
not placed on the latency-sensitive pull-request path.

## Platform contract

| Target | `store/fs` runtime contract | Other packages |
| --- | --- | --- |
| Darwin | Durable single-filesystem backend, subject to capability checks | Supported |
| Linux | Durable single-filesystem backend, subject to capability checks | Supported |
| Windows and other targets | Compile-safe; `Open` and `OpenContext` return `*fs.UnsupportedPlatformError` | Buildable |

Callers can recognize an unsupported durable backend with
`errors.Is(err, fs.ErrUnsupportedPlatform)` and inspect `GOOS`/`GOARCH` with
`errors.As`. An unsupported target never falls through to partial filesystem
initialization.

The durability boundary remains one filesystem. Leases are advisory: raw
editors do not participate, and raw readers can observe a multi-file rename
during publication. Recovery completes a recorded transaction; it is not
distributed isolation.

## Traceability

Material changes must use an issue-linked pull request and green required
checks. If repository rules cannot enforce a required check, the release
evidence must record that limitation and link the successful check explicitly.
A release record must link its issue, reviewable diff, commits, CI run, existing
annotated tag, verification evidence, and any remaining follow-ups. Closing
comments must use those artifacts rather than an unlinked completion claim.

The `v0.2.0` release notes are tracked in
[`releases/v0.2.0.md`](releases/v0.2.0.md). Parser-backed mutation verification
is tracked separately in
[`parser-backed-lossless-mutations-evidence.md`](parser-backed-lossless-mutations-evidence.md).
The issue #2 requirement map is
[`issue-2-release-engineering-evidence.md`](issue-2-release-engineering-evidence.md).
