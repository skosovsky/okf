# Harden v0.2.x release engineering: CI, platform contract, and traceability

Источник: [GitHub issue #2](https://github.com/skosovsky/okf/issues/2).

## Context

The transactional mutation implementation from #1 is available under the annotated `v0.2.0` tag and passes the documented local verification matrix. This follow-up is not a request to reopen or redesign that implementation.

The release surface around it still has several objective gaps:

- the repository has no `.github/workflows` CI configuration;
- GitHub Releases currently contains `v0.1.0`, but no Release object for the existing `v0.2.0` tag;
- `store/fs` has a Darwin/Linux implementation (`fd_unix.go` uses `//go:build darwin || linux`), while the supported-platform policy is not stated in the public documentation;
- `GOOS=windows GOARCH=amd64 go build ./...` currently fails because the filesystem capability backend has no Windows or unsupported-platform implementation;
- #1 was closed manually without a linked PR, completed checklist, or GitHub-hosted check run;
- `docs/issue-002-evidence.md` refers to "Issue 002", although there is no corresponding public GitHub issue, which makes the evidence provenance unclear.

## Goal

Make `v0.2.x` and future OKF releases reproducible and auditable without changing OKF conformance semantics or the transactional API.

## 1. Add continuous integration

Add GitHub Actions workflows for pull requests, pushes to `main`, and version tags.

Required deterministic gates:

- [ ] `go test ./...`
- [ ] `go test -race ./...` on at least one supported platform
- [ ] `go vet ./...`
- [ ] `go mod verify`
- [ ] `go mod tidy -diff`
- [ ] `git diff --check`
- [ ] Linux build
- [ ] Darwin build/test for `store/fs` platform-specific behavior
- [ ] unsupported-platform compile check, including Windows

The long fuzz campaigns and 10,000-concept benchmarks may remain scheduled or manually triggered jobs. Normal PR CI should still run the deterministic regression corpus and ordinary tests.

## 2. Declare and enforce the platform contract

Choose and document one explicit policy for `store/fs`:

### Option A: Darwin/Linux backend only

- document that `store/fs` provides durable commits only on Darwin and Linux;
- add a compile-safe unsupported-platform implementation which returns a stable typed error from `Open`/`OpenContext`;
- keep backend-neutral packages such as `bundle`, `graph`, `validator`, `store`, and `mutation` buildable on Windows;
- ensure `go build ./...` does not fail merely because an unsupported runtime platform was selected.

### Option B: implement a Windows backend

- provide equivalent no-follow path handling, lease, rename, file sync, directory durability, and recovery guarantees;
- add Windows-specific adversarial and fault-injection tests.

Windows backend support is not required by this issue. An explicit Darwin/Linux contract plus a compile-safe stub is sufficient.

Acceptance checks:

- [ ] README and toolkit documentation state supported platforms and guarantees.
- [ ] Unsupported platforms fail explicitly rather than through missing symbols.
- [ ] CI prevents accidental expansion or regression of the platform matrix.

## 3. Publish the `v0.2.0` GitHub Release

- [ ] Create a GitHub Release for the existing annotated `v0.2.0` tag.
- [ ] Include the two implementation commits (`43f7214` and `bb9c169`).
- [ ] Summarize public API additions and backward-compatibility notes.
- [ ] State the supported `store/fs` platform scope and advisory-lock/raw-reader limitations.
- [ ] Link the verification evidence and CI run when available.
- [ ] Document whether binaries are published or the release is Go-module/source only.

Do not move or recreate the existing tag unless its integrity is known to be wrong.

## 4. Improve review and completion traceability

- [ ] For future material changes, use a PR or another reviewable diff surface linked from the issue.
- [ ] Require green checks before merge/tag through repository rules where practical.
- [ ] Closing comments link commits/PRs, check runs, release tag, and remaining follow-ups.
- [ ] Mark the original issue checklist or add a final requirement-to-evidence table instead of relying only on a prose completion claim.
- [ ] Clarify or rename `docs/issue-002-evidence.md` so a reader can identify the task/review source behind "Issue 002".
- [ ] Avoid claims such as "100% complete" or "zero findings" unless the corresponding review artifacts are publicly linked or stored in the repository.

## Acceptance criteria

- [ ] A new pull request receives automatic test, race, vet, module, diff, and platform results.
- [ ] `v0.2.0` appears on the GitHub Releases page with reproducible verification notes.
- [ ] The supported platform policy for `store/fs` is unambiguous.
- [ ] Linux and Darwin remain supported.
- [ ] `GOOS=windows GOARCH=amd64 go build ./...` either succeeds with a runtime unsupported-platform error path or Windows is excluded through a deliberate documented module/package boundary without undefined symbols.
- [ ] Release and review evidence can be followed from issue to code, checks, tag, and release without relying on an author's unlinked assertion.

## Non-goals

- Reworking the snapshot, CAS, journal, idempotency, or mutation design completed in #1.
- Making Windows a supported durable filesystem backend unless explicitly chosen.
- Moving methodology profiles, workflow gates, or AI Drive policy into `okf`.
- Treating CI success as semantic or business-quality validation.
