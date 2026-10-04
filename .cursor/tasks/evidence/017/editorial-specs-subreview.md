# Independent editorial subreview: reading specifications

Scope: full manual reading of both English and Russian reading editions of `spec-v02.md` and `spec-v02-instant.md`. No product files edited.

## Coverage and result

Read all introduction/authority notices, §§1–13 (all subsections), and Appendix A with v0.1 and three v0.2 documents. Compared purpose, definitions, schema/key requirements, MUST/MUST NOT/SHOULD/MAY strength, links/path forms, optionality, trust tiers, lifecycle, source/usage signals, computation executor/receipt/attester boundary, verification versus attestation, compatibility fallbacks, timestamps and illustrative SQL/YAML. No confirmed material EN/RU fact, action, example or restriction omissions. Date and instant profiles retain their separate pins and comparison rules (`today >= stale_after` versus `now >= stale_after`); both translations identify canonical original authority and repository revision. English date body equals canonical original after navigation/anchors removed; instant body only removes four trailing spaces.

The Russian documents are substantially readable and their technical density belongs to a normative reference. English technical identifiers, conventional schema headings and enum/type values are intentionally retained and are not language errors. The initial minor ordinary-label/prose findings below were corrected and independently rechecked; no confirmed open editorial finding remains in these two pairs.

## Initial editorial findings — now closed

- **SPECS-RU-01 (P3, AC02):** `docs/ru/readings/skills/open-knowledge-format/references/spec-v02-instant.md:205` and `:211` retain `[customers]` in otherwise translated prose and `:435` retains `[Customer Metrics]`. These are human display labels in examples, not API fields or immutable concept IDs; paths can remain unchanged while labels become «клиентам» / «клиентами» and «метрик клиентов». The date translation already localizes equivalent labels. `docs/ru/readings/skills/open-knowledge-format/references/spec-v02.md:9` also retains ordinary prose «редакцией upstream»; «исходной редакцией» expresses the same fact.
- **SPECS-RU-02 (P3, AC02):** `docs/ru/readings/skills/open-knowledge-format/references/spec-v02-instant.md:265` says «Ему СЛЕДУЕТ присутствовать» rather than normal Russian «Его СЛЕДУЕТ указывать». `docs/ru/readings/skills/open-knowledge-format/references/spec-v02.md:459` defines parameters as «именованных типизированных мест»; «именованных параметров с типами» preserves the fact and reads clearly (already used in the instant translation). These are wording edits, not changes to requirements.

## Initial reviewed file SHA-256

- `docs/readings/skills/open-knowledge-format/references/spec-v02.md`: `9b1b332d3d8349c99bb232c101b27d6bf6b6b5fd38a48ec9c10071f867acb8c1`
- `docs/readings/skills/open-knowledge-format/references/spec-v02-instant.md`: `08b910b62ec45e87ebb36dec762ae7741b680d54834e47475de9e221331aee6c`
- `docs/ru/readings/skills/open-knowledge-format/references/spec-v02.md`: `48e8de97b0290fbd29c8d4782f87ba4449172f389bb5742735c576af5fdb1c81`
- `docs/ru/readings/skills/open-knowledge-format/references/spec-v02-instant.md`: `312e8d9ab56d277b94b59de085b1c94c9648452b18666624f992ef051bfad35a`

## Independent correction recheck

Re-read changed authority notices, source-ID recommendation, parameter definition, Schema examples, customer-reference table/join prose, and log example in both pairs. SPECS-RU-01 is closed: customers and Customer Metrics display labels are localized, including grammatical «в таблицу клиентов» and «с таблицей клиентов»; the date authority notice now says «исходной редакцией». SPECS-RU-02 is closed: source-ID advice is «Его СЛЕДУЕТ указывать», and the date parameter definition uses «именованных параметров с типами». The human Schema example heading is localized as «Схема»; conventional original Schema/Examples/Computation identifiers in the section table are accurately explained and API/path/enum values stay unchanged. No changed fact, normative strength, or example executable code was found.

The latest source anchor sets match within each pair (including added stable instant-profile section anchors). Source originals remain preserved. This recheck is before the final freeze and only applies to the hashes below. No product edits performed.

## Current reviewed file SHA-256

- `docs/readings/skills/open-knowledge-format/references/spec-v02.md`: `fbb1c364915e5ab4a2be9c6c3fb3b99ca97f3cd176d0248ce0b748313a7f93e7`
- `docs/ru/readings/skills/open-knowledge-format/references/spec-v02.md`: `8a20fa47fee0ca291d3d1b33c584cfccbc9fcf2f9ce2bdf804aca2f19664a7ac`
- `spec-v02.md` anchor set match: True, 41 anchors.
- `docs/readings/skills/open-knowledge-format/references/spec-v02-instant.md`: `f3a3a0b891d934ebbbfd0fdcb6b3f112dec34f361a9fe81ea32b1657cec383c4`
- `docs/ru/readings/skills/open-knowledge-format/references/spec-v02-instant.md`: `5357ac9252d704ecc5a120bf7876aa563c87c0a153d05605b274b8482a6a8261`
- `spec-v02-instant.md` anchor set match: True, 75 anchors.
