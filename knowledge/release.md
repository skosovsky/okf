---
type: Architecture Note
title: Release evidence
description: What CI and a release record must prove.
status: draft
sources:
  - id: release
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/docs/release-engineering.md
    title: Release engineering contract
  - id: workflow
    resource: https://github.com/skosovsky/okf/blob/ed7ddc28cd127682023bd150ee377906b817e099/.github/workflows/ci.yml
    title: CI workflow
---

# What evidence is needed before release?

The CI workflow supplies executable quality gates across supported Go targets and checks the release tag's relationship to `main` on version-tag pushes.[^release][^workflow] A release record must link the issue, reviewable diff, commits, CI run, existing annotated tag, verification evidence, and remaining follow-ups.[^release] A draft release page does not establish that its future tag exists.[^release]

The platform matrix belongs to [Supported platforms](platforms.md); the OKF format version is covered by [Version resolution](versions.md).

[^release]: Release engineering contract, CI and release-record requirements.
[^workflow]: CI workflow implementation.
