---
title: Issue 2 release-engineering evidence
description: Requirement-to-evidence map for CI, platform support, v0.2.0, and review traceability.
permalink: /issue-2-release-engineering-evidence/
---

# Issue 2 release-engineering evidence

This record maps the public requirements in
[GitHub issue #2](https://github.com/skosovsky/okf/issues/2) to reviewable
artifacts. It distinguishes verification of the existing `v0.2.0` tag from the
post-tag release-engineering hardening in
[pull request #3](https://github.com/skosovsky/okf/pull/3).

## Requirement-to-evidence

| Requirement | Executable or reviewable evidence |
| --- | --- |
| PR, `main`, and tag CI triggers | [CI workflow](https://github.com/skosovsky/okf/blob/main/.github/workflows/ci.yml) and [PR #3 checks](https://github.com/skosovsky/okf/pull/3/checks); version tags must resolve to `main`, and zero-base events scan the complete committed tree for whitespace errors |
| Test, race, vet, modules, tidy, and diff gates | `quality` and `race` jobs in the CI workflow |
| Linux durable backend | Linux test/build jobs plus the full local test matrix below |
| Darwin durable backend | Native `macos-latest` `store/fs` test and repository build |
| Windows unsupported runtime contract | Native `windows-latest` build and targeted `Open`/`OpenContext` tests |
| No Android/iOS tag expansion | Android build plus Android/iOS `go list` assertions selecting `fd_unsupported.go` |
| Stable typed unsupported error | `fs.ErrUnsupportedPlatform`, `fs.UnsupportedPlatformError`, and AAA contract tests |
| Public platform policy | EN/RU README, toolkit, site overview, skill, and [release-engineering contract](release-engineering.md) |
| Existing tag integrity | annotated tag `v0.2.0`; peeled commit `bb9c169`; the workflow does not move or recreate it |
| v0.2.0 contents and limitations | [tracked release notes](releases/v0.2.0.md) and [GitHub Release](https://github.com/skosovsky/okf/releases/tag/v0.2.0) |
| Mutation evidence provenance | renamed [parser-backed evidence](parser-backed-lossless-mutations-evidence.md), linked to issue #1 and commit `bb9c169` |
| Review and completion traceability | issue #2 → PR #3 → hosted checks → commit → existing tag → GitHub Release; exact run and commit links are recorded in the issue and Release |

## Local verification

Run from the repository root:

```sh
go test ./...
go test -race ./...
go vet ./...
go mod verify
go mod tidy -diff
git diff --check
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/okf-store-fs-windows.test.exe ./store/fs
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./...
GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go list -f '{{join .GoFiles " "}}' ./store/fs
GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go list -f '{{join .GoFiles " "}}' ./store/fs
actionlint .github/workflows/ci.yml
zizmor .github/workflows
```

The Android and iOS `go list` results must include `fd_unsupported.go` and must
not include `fd_unix.go`. `actionlint` must report no errors and `zizmor` must
report zero findings.

Long fuzz campaigns and 10,000-concept performance profiles remain separate
manual evidence in the parser-backed mutation record; they are not normal PR
CI gates.

## Legacy completion claim

The closing comment on issue #1 used "100%" and "zero findings" without
preserving public reviewer artifacts or hosted check runs. That statement is
not treated as evidence here and cannot be reconstructed retroactively. This
issue replaces that practice with the requirement table, reviewable PR, hosted
checks, exact commit and run links, existing tag, and GitHub Release record.
