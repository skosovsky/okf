---
layout: default
title: "ADR 0003: Explicit temporal revisions within OKF 0.2"
lang: en
document_id: docs-adr-0003-okf-v02-temporal-revisions
permalink: /adr/0003-okf-v02-temporal-revisions/
---

<a id="adr-0003-explicit-temporal-revisions-within-okf-02"></a>

# ADR 0003: Explicit temporal revisions within OKF 0.2 {#doc-section-001}

- Status: Accepted
- Date: 2026-09-26

<a id="context"></a>

## Context {#doc-section-002}

Two pinned normative documents both declare OKF `0.2`. The original
`knowledge-catalog` revision `3fcbb9f` uses calendar dates for
`stale_after`, `sources[].last_modified`, and both forms of `usage_window`.
The `open-knowledge-format` revision `0b87c52` uses offset-bearing ISO 8601
datetimes for those fields, with `now >= stale_after`. The contents and hashes
are recorded in `fixtures/v02/spec-lock.json` and
`fixtures/v02/spec-lock-instant.json`.

`okf_version` alone cannot select the temporal rules. Guessing a revision
from one value also fails for bundles that omit these optional fields or mix
date and datetime values.

<a id="decision"></a>

## Decision {#doc-section-003}

The toolkit names two temporal profiles: `date-3fcbb9f` and
`instant-0b87c52`. The former remains the default for existing Go, CLI,
MCP, and graph contracts. The latter is selected explicitly by an additive
profile field or argument. Reports and graph metadata expose the selected
profile. A profile applies to the whole bundle, even when some optional
fields are absent.

The old `DateValue` accessors and `IsStale` semantics remain calendar based.
New profile-aware observations retain the original scalar, YAML family shape,
and precise `time.Time` instant. The instant profile accepts RFC 3339's
offset-bearing spelling (`Z` or `±HH:MM`) with optional fractional seconds.
This is a documented interoperable subset of ISO 8601. Go's parser rejects
timezone-less values, invalid offsets, `-00:00` as an unknown-offset marker,
and leap seconds. Fractional digits are retained in `Raw`; Go's `time.Time`
provides nanosecond comparison precision. We make no claim to support every
ISO 8601 spelling.

For the instant profile, `stale_after` is stale at and after its exact instant.
`usage_window.from <= usage_window.to` compares instants; equal endpoints
are valid. The interval is an observed range, with no inferred end-of-day
expansion. The validator keeps these optional-family shape problems advisory
under strict mode, matching the established 0.2 severity boundary.

A legacy date contains no timezone or time of day. The toolkit never infers
an instant from one. Without an explicit conversion mapping, revision upgrade
preview reports it unresolved and apply cannot publish an incomplete upgrade.
The mapping is per temporal family, including both usage endpoints, and is
recorded as user policy rather than recovered fact. Existing v0.1-to-v0.2
migration remains unchanged.

The existing `skosovsky/okf-v0.2` graph projection keeps `xsd:date` and exact
legacy bytes. The instant projection is a separate contract with
`xsd:dateTime`. Existing canonical change-set tags and old operation values
keep their identity and digest; any new temporal operation must use an
additive tag or an explicitly versioned envelope.

<a id="compatibility-and-rollout"></a>

## Compatibility and rollout {#doc-section-004}

The default continues to interpret a date-only bundle as before. Consumers
must select the instant profile explicitly for the revised spec. Producers
targeting that profile emit only offset-bearing datetimes. A mixed bundle
receives profile-specific warnings; it is not silently normalized. Missing
`as_of` leaves staleness unknown and does not consult the wall clock. A
date-only `as_of` remains valid only for the date profile; the instant profile
requires a datetime. JSON output reports the profile's date or offset
datetime form, retaining instant precision through nanoseconds.

<a id="consequences"></a>

## Consequences {#doc-section-005}

The duplicated `0.2` identifier makes the explicit profile necessary until
upstream publishes an unambiguous revision marker. The profile is toolkit
metadata, not an invented OKF version or frontmatter key.
