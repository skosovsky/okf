---
title: "Release engineering"
description: "Release engineering"
permalink: /release-engineering/
---

{% include nav.html %}

<span id="release-engineering"></span>

# Release a checked OKF version {#page-top}

This process is for repository maintainers. You need GitHub access, Git, and the required Go version. First prepare an issue and PR for changes, wait for required checks, then publish the tag and release with supporting evidence.

## Required checks {#checks}

The `CI` workflow runs for PRs, pushes to `main`, version tags, and manual dispatch. Repository permissions are read-only; third-party actions are pinned to immutable SHAs.

Linux runs:

```sh
go test ./...
go test -race ./...
go vet ./...
go mod verify
go mod tidy -diff
git diff --check
go build ./...
```

Skill dependencies, the lock file, and standalone packages are also checked, including negative cases for missing tools and reference files. Darwin runs `go test ./store/fs` and `go build ./...`. Windows runs builds and tests the typed unsupported-platform result from `Open` and `OpenContext`. Android is cross-built; Android/iOS source-selection assertions prevent derived Go tags from accidentally selecting the Darwin/Linux backend.

For tags, CI also checks that the commit is reachable from `main`. GitHub supplies a zero `before` SHA on tag creation, so whitespace checks compare the entire tag against Git’s empty tree. Long fuzz runs and 10,000-note profiles remain manual checks described in the [mutation evidence]({{ '/parser-backed-lossless-mutations-evidence/' | relative_url }}).

## Platforms and durability boundary {#platforms}

| Platform | `store/fs` contract | Other packages |
| --- | --- | --- |
| Darwin | Durable storage on one filesystem after capability checks | Supported |
| Linux | Durable storage on one filesystem after capability checks | Supported |
| Windows and others | Compile-safe; `Open` and `OpenContext` return `*fs.UnsupportedPlatformError` | Buildable |

Use `errors.Is(err, fs.ErrUnsupportedPlatform)` to recognize the error and `errors.As` to inspect `GOOS`/`GOARCH`. An unsupported platform does not start partial initialization.

The durability boundary is one filesystem. Leases are advisory: ordinary editors do not participate, and readers can observe multi-file renaming during publication. Recovery completes a recorded transaction; it does not provide distributed isolation.

## Release evidence {#release}

Record the issue, PR with a reviewable diff, commits, successful CI, existing annotated tag, verification, and open follow-ups. If GitHub rules cannot enforce a required status, explicitly record the limitation and link a successful check. Completion messages must link these artifacts.

Expected result: a published release whose changes can be traced to a PR and checks. If a check fails, investigate before claiming successful acceptance; record follow-ups and limitations in the release.

[Current releases](https://github.com/skosovsky/okf/releases). Historical [v0.2.0]({{ '/releases/v0.2.0/' | relative_url }}) and [v0.2.2]({{ '/releases/v0.2.2/' | relative_url }}) records describe their own versions. The [issue #2 requirement map]({{ '/issue-2-release-engineering-evidence/' | relative_url }}) preserves that verification history.
