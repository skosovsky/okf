# Human guides and paired teaching examples

Authored EN/RU pairs: root README; examples README; landing; quickstart; MCP; skill; toolkit; repository knowledge; upkeep; dated manual knowledge review; GitHub Action; release engineering. Existing public routes retained (landing / and /ru/). No registry/layout/test/spec/skill original/knowledge source changes made by this worker.

## Content decisions

- One fictional teaching story throughout: failed delivery retries for 24 hours, then operator review. Paired bundles use the same index/source-material/retry-policy paths, source ID requirements, and type Guide, with fully translated titles, bodies, root descriptions, and footnotes.
- The quickstart builds from a source clone, copies the appropriate locale into a temporary folder, validates/exports/searches, opens source evidence, manually changes both source and derived rule to 48 hours, reruns validation/search, and explicitly overwrites the derived HTML. No producer, verification or staleness metadata is fabricated.
- The source teaching files are explicitly fictional. Original knowledge/ and frozen fixtures/benchmarks remain untouched; English repository-note titles in the historical review are exact identifiers with an explanation.
- The former long toolkit/skill transport, migration, selectors, proof, fallback, and transaction details are linked through the reference and contract pages. Reference worker notified to retain every invariant from the original toolkit/skill pages. Beginner guide structure is task/prerequisites/command/result/common failure.
- Every website pair has identical stable ASCII section anchors. The source wording is independently authored into aligned pairs, not merely mechanically transliterated. RU shell prompts, note text, explanatory headings and regular prose are Russian; literal API fields and named commands remain unchanged.
- Codex installation is whole-folder project copy into .agents/skills/open-knowledge-format, explicit $open-knowledge-format invocation, /skills discovery, PATH inherited from launch shell. Root verified current official docs at https://learn.chatgpt.com/docs/build-skills and installed codex-cli 0.160.0; both guide editions link that source (labeled English original in RU). Root performs actual client verification.
- MCP guide uses isolated project and session-only configuration, search_sections, read_concept for retry-policy, then source-material. Read/no-edit prompt, absolute bundle_path, expected actual tool-call evidence, diagnostics and absent-connection handling are explicit. Instructions never infer success merely from configured settings.
- GitHub Action example pins published 61e75e9 rather than the prior branch-specific claim. Full input/output semantics retained. Its ${{ ... }} expression is protected by Liquid raw tags and must remain exact after build.
- Historical manual review retains date 2026-09-26, three question/route/answer/source rows, original caveat and illustrated version numbers. It describes the toolkit guide at the original review date rather than pretending its rewritten structure is identical.

## Verification performed

- Source-inspected internal/okfcli/init.go: init supports explicit new directory and --json only. Removed unsupported --spec flag before acceptance.
- Both teaching bundles executed through actual CLI strict validation with check-links/check-orphans/max-warnings=0: exit 0, scanned_files 3, errors 0, warnings 0, conformant true.
- Pair structural check: identical stable anchor lists for all ten website pairs.
- Normative translations agent independently executed EN/RU quickstart: validation/search/view and 24→48 update passed. It identified init flag; fixed and asked it to rerun toolkit.
- Fixed Jekyll frontmatter colon quoting, accidental JSON double braces in upkeep explanatory text, and Liquid expansion of Github Action expression.

Full website/browser/client/technical and independent semantic final acceptance remain root's integration checks. This document does not claim those checks completed.

## Independent review corrections

- Quickstart EN/RU now explicitly opens the source via the body link or left note list. Sources displays local resource details without a clickable link; instructions no longer claim it navigates. No viewer runtime or contract changes made.
- Independent guide command validation completed EN/RU quickstart 24→48 and ALL toolkit shell blocks including setup with actual reviewed digest, validation, view, info/parse, graph, fmt/index; both locales exited zero. Evidence: guide-command-validation.md.
- RU quickstart search explanation now explicitly states case/Unicode normalization, matching EN and retrieval behavior; ambiguous "учитывает регистр" removed.
- Added explicit H1 `page-top` IDs to all ten EN/RU guide pairs so compiled heading IDs match independently of localized title slug. Existing section anchors remain unchanged.
