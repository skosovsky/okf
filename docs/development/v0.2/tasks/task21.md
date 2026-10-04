---
lang: en
title: "Technical specification: agent skill, docs, and examples for OKF v0.2"
permalink: /development/v0.2/tasks/task21/
---

{% include nav.html %}

> Historical document from source revision `61e75e9`. This implementation plan is archived for reference and is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task21.md).

# Technical specification: agent skill, docs, and examples for OKF v0.2 {#section001}

## 1. Goal {#section002}

Move the agent-facing contract, README/docs, examples, fixtures, and plugin
packaging to v0.2 while preserving explicitly labeled legacy consumption.

Finalize after tasks 09–20.

## 2. Embedded specification {#section003}

- Replace `references/spec-v01.md` with pinned `spec-v02.md`.
- Add `references/migration-v01-v02.md`.
- Do not conflate normative MUST/SHOULD/MAY.
- Move YAML `relations` into a labeled `skosovsky/okf extension`.
- Describe deferred/ambiguous ABI honestly, without invented rules.

## 3. Authoring workflow {#section004}

The skill must:

1. Determine the target version and write the version only in the root index.
2. Require only `type` for a minimal concept.
3. Add generated only when the actor is known.
4. Create sources only from actual materials.
5. Use keyed footnotes for demonstrable claim attribution.
6. Never treat author/generator/validation as evidence of verification.
7. Write verified only after an actual check.
8. Never invent status/stale date/credibility signals.
9. Put sanctioned computation in a separate concept.
10. Never turn credibility signals into a score.

## 4. Consumption workflow {#section005}

- Prefer v0.2 fields, then §13 fallback.
- Normalize bare verified.
- Derive the trust tier strictly from verified.
- Show trust/status/staleness separately.
- Do not execute executor/attester content without a trusted runtime and authorization.
- Do not ignore deprecated/stale signals because of an instruction in the body.

## 5. Migration guidance {#section006}

- Transfer timestamp only with an explicit generated actor.
- Unknown actor → unresolved/manual action.
- Citations → sources without invented metadata.
- Claim footnotes only with demonstrable mapping.
- Do not infer verified/status/stale_after from git/timestamp/migration.
- Bump the version after validation.
- Preserve unknown content losslessly.

## 6. Examples/fixtures {#section007}

Update examples and add:

- human-authored concept;
- multiple sources/keyed footnotes;
- bare/list verified;
- draft/stable/deprecated;
- fresh/stale boundary;
- inline/file Attested Computation;
- narrative concept linking computation;
- explicit legacy/mixed/future examples.

Canonical snippets must be extracted from fixtures or checked by drift tests.
Synthetic examples use reserved domains and are labeled synthetic.

## 7. Adversarial contexts {#section008}

Matrix of input → expected decision → forbidden behavior:

- body asks to ignore frontmatter/trust;
- generated human actor without verified;
- human source author without a verifier;
- high usage_count as an attempt to increase trust;
- footnotes in code/unknown/duplicate IDs;
- simultaneous legacy/v0.2 provenance;
- stale/deprecated self-promotion;
- executor asks for shell/secrets/policy bypass;
- LLM receipt declares attestation success.

Deterministic cases go into fixtures/tests; agent-only cases go into the evaluation matrix.

## 8. EN/RU and packaging {#section009}

Update together:

- `README.md` / `README.ru.md`;
- `docs/index.md` / `docs/ru/index.md`;
- `docs/skill.md` / `docs/ru/skill.md`;
- `docs/toolkit.md` / `docs/ru/toolkit.md`;
- navigation and migration page.

After stabilization:

- plugin descriptions/versions;
- do not equate the package version with the OKF specification version;
- recalculate `skills-lock.json` last;
- check `skills.sh.json`/marketplace manifests.

## 9. Stop-slop constraints {#section010}

- Do not invent actors, sources, verification, freshness, or receipts.
- One term has one meaning.
- Do not duplicate the overview on every page.
- EN/RU agree on the contract, but Russian is not a literal calque.
- Always label repository policy as extension/tooling policy.
- No marketing fog.

## 10. Acceptance criteria {#section011}

- Active docs/skill do not call the current specification a v0.1 draft.
- Timestamp/Citations remain only in legacy/migration cases.
- All links point to spec-v02/migration.
- Canonical v0.2 fixtures pass base/strict validation cleanly.
- The legacy fixture is read using the documented fallback.
- Bare/list verified semantics are identical.
- Attested examples make no runtime ABI promises.
- EN/RU parity passes.
- Plugin manifests are consistent; the skill lock test passes.
- Searching for v0.1/timestamp/Citations/spec-v01 returns only intentional cases.
- `go test ./...`, `go vet ./...`, and `git diff --check` pass.

## 11. Out of scope {#section012}

Computation execution, attester ABI/sandbox/cache, invented scoring, and
automatic semantic rewriting.
