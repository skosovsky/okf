---
lang: en
title: "Generalized lossless YAML engine for v0.2"
permalink: /development/v0.2/tasks/task13/
---

{% include nav.html %}

# Technical specification: Generalized lossless YAML engine for v0.2 {#section001}

> Historical document from source revision `61e75e9`. This plan records the work proposed at that revision; it is not current implementation guidance. [Original source](https://github.com/skosovsky/okf/blob/61e75e9/docs/development/v0.2/tasks/task13.md).

## 1. Goal {#section002}

Extend the private mutation engine with structural mapping and sequence primitives without weakening the fail-closed and byte-preservation contract.

Dependencies: [`task09.md`]({{ '/development/v0.2/tasks/task09/' | relative_url }}), [`task10.md`]({{ '/development/v0.2/tasks/task10/' | relative_url }}).

## 2. Gap {#section003}

The current engine focuses on `relations`, `id`, `anchor`, and string scalars. OKF v0.2 uses nested mappings and sequences, bool/int/date/datetime values, and flow forms. Touched flow nodes are currently rejected outright.

Do not patch flow style internally: atomic replacement of an entire collection value is allowed when its boundaries and ownership have been proven.

## 3. Private primitives {#section004}

Add:

- replacement, insertion, and deletion of root mapping entries;
- replacement, insertion, and deletion of nested mapping entries;
- whole-value replacement of mappings and sequences;
- ensuring, updating, and removing sequence items by a unique selector;
- conversion of a bare mapping into a one-element sequence;
- exact span ownership for mapping entries and collection values;
- rendering of string/bool/int/date/datetime/mapping/sequence values;
- semantic reparsing and verification that bytes outside patch spans remain unchanged.

Selector routes:

- `sources[]` by `id`; entries without an ID support exact-value selection only;
- `verified[]` by `(by, at)`;
- `parameters[]` by `name`;
- nested mappings by a unique scalar key.

A generic raw YAML path must not become a public API.

## 4. Fail-closed contract {#section005}

- Duplicate touched keys or selectors → `Ambiguous`.
- Alias or merge provenance → `Ambiguous`.
- Anchors, tags, complex keys, or unowned trivia → `Unsupported`.
- Unprovable comment ownership → `Unsupported`.
- Nested flow patches are allowed only as whole-value replacements.
- Unrelated YAML and Markdown remain byte-identical.
- Any error produces no staging, writes, renames, or plan.

Add stable error codes and source locations within bounds.

## 5. Files {#section006}

- `mutation/presentation.go`;
- `mutation/yaml_resolver.go`;
- `mutation/presentation_errors.go`;
- focused tests, a parser-backed corpus, and a fuzz corpus.

## 6. Tests {#section007}

All tests follow AAA.

- Insert, update, and delete mapping entries.
- Block forms and whole-value flow forms.
- Bare/list sequence normalization.
- String/bool/int/date/datetime values.
- LF/CRLF, comments, and quotes.
- Duplicate keys/selectors, aliases, merges, anchors, tags, and complex keys.
- Invalid UTF-8, nested flow forms, and mixed sequences.
- Caller-owned and source bytes are not mutated.
- Bytes outside spans remain identical.
- The reparsed semantic projection matches exactly.
- Rejection includes a typed error and location, with no public plan.
- A second desired-state application produces no diff.
- Fuzz: no panics, spans within bounds, and a parseable result.
- Race: concurrent planning and caller mutation.

## 7. Acceptance criteria {#section008}

- Upstream flow examples can be modified by whole-family replacement.
- No existing relation/move/rename digest or semantic contract changes.
- Property and fuzz tests prove the lossless guarantees.
- `go test -race ./mutation ./store` passes.

## 8. Out of scope {#section009}

Public v0.2 operations, semantic migration, executor runtime, and generic caller-controlled `SetYAML`.
