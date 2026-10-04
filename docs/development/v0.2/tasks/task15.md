---
lang: en
title: "Transactional migration from OKF v0.1 to v0.2"
permalink: /development/v0.2/tasks/task15/
---

{% include nav.html %}

# Technical specification: Transactional migration from OKF v0.1 to v0.2 {#section001}

> Historical document from source revision `61e75e9`. This plan records the work proposed at that revision; it is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task15.md).

## 1. Goal {#section002}

Add a deterministic preview/apply migration plan for the two breaking changes in v0.2:

- `timestamp` → `generated`;
- `# Citations` → `sources` + keyed footnotes.

Dependencies: [`task10.md`]({{ '/development/v0.2/tasks/task10/' | relative_url }}), [`task11.md`]({{ '/development/v0.2/tasks/task11/' | relative_url }}), [`task13.md`]({{ '/development/v0.2/tasks/task13/' | relative_url }}), [`task14.md`]({{ '/development/v0.2/tasks/task14/' | relative_url }}), [`task17.md`]({{ '/development/v0.2/tasks/task17/' | relative_url }}).

## 2. General contract {#section003}

- Preview is read-only by default.
- Apply requires the exact base revision and plan digest.
- The entire bundle changes in one transaction; partial migration is prohibited.
- Unknown frontmatter, body content, and assets are preserved.
- Root `okf_version` changes last, after validation against target v0.2.
- An identical rerun is a no-op or idempotent replay.

## 3. Timestamp migration {#section004}

Create `generated` only with explicit `generated.by`. Do not use `ChangeSet.Actor` as an implicit producer.

Cases:

- generated absent + timestamp present → copy to `generated.at`;
- both absent → explicit values are required; no `time.Now()`;
- equivalent generated exists → no-op;
- conflicting `timestamp`/`generated.at` → blocker without an explicit policy;
- legacy removal policy: `preserve` or `remove_after_copy`;
- duplicate nodes, merge nodes, or nodes with ambiguous comment ownership → fail closed.

## 4. Citation migration {#section005}

Do not infer provenance or claim attribution from meaning.

Input must contain an explicit mapping from legacy entries to stable source IDs.

Support:

- `[1] [Title](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/URL)`;
- the upstream bulleted URL form;
- explicit ID/title/resource;
- replacement of proven numeric markers only;
- creation of keyed footnote definitions;
- removal of `# Citations` only after the entire mapping succeeds.

Fail closed on:

- multiple Citations sections;
- duplicate numbers or labels;
- unresolved entries or claim markers;
- conflicting footnote definitions;
- ambiguous Markdown ownership.

Do not generate title/author/usage/last_modified/verified/status/stale_after.

## 5. Markdown collector {#section006}

Add parser-backed ownership for:

- section spans;
- citation entries;
- footnote references and definitions;
- normalized label collisions;
- the `# Computation` heading and fence.

Fenced code, inline code, and raw HTML remain opaque. Do not change the existing MoveConcept parser configuration globally.

## 6. Attested Computation migration boundary {#section007}

Do not automatically split narrative documents or convert SQL in prose into Attested Computation.

Allow only an explicit creation/move plan:

- the caller specifies target concepts and sanctioned computation;
- inline mode owns one fence under one heading;
- file mode creates a referenced asset through transactional store support;
- multiple headings or fences → `Ambiguous`.

## 7. Tests {#section008}

All tests follow AAA.

- Canonical Appendix v0.1 → v0.2.
- Dry-run leaves bytes unchanged.
- Timestamp policies, conflicts, and actor validation.
- Citation URLs, paths, and explicit mapping.
- Multiple sections, duplicate IDs, and markers in code/raw HTML.
- CRLF/LF, comments, and flow YAML.
- One bad file results in no writes.
- Revision conflict, plan mismatch, and concurrent writer.
- Crash/recovery leaves only the pre-state or post-state.
- A second migration is a no-op.
- Byte preservation for sources, body content, and unknown extensions.
- Validation against target v0.2 passes.

## 8. Acceptance criteria {#section009}

- Safe cases migrate transactionally and losslessly.
- Ambiguous cases return blockers or manual actions.
- No inferred verification, trust, freshness, or actor.
- The root version never changes before full preflight.
- CLI/MCP use the same planner.
- Race tests, recovery tests, and the full suite pass.

## 9. Out of scope {#section010}

LLM rewriting, semantic claim matching, automatic SQL splitting, executor runtime, attestation ABI, and network fetching.
