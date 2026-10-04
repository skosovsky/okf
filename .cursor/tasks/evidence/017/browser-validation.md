# Actual browser acceptance — 2026-10-04

Root used the CUA browser interface against the built site served on localhost (base URL /okf). Chrome desktop and an explicit 390×844 viewport were used; the temporary viewport was reset. After Chrome extension timeouts, remaining empty-state and desktop checks were completed in the in-app Chromium browser (1280×720). These are observed UI checks, separate from the production JavaScript harness.

- EN/RU quickstart pages: desktop and narrow screenshots inspected; navigation wraps and prose remains readable. Horizontal scrolling is confined to command blocks; document scrollWidth equals viewport width at 390px. Native language transition opens the same page; `#search` is preserved and exists in the destination. Russian navigation/footer/accessibility language is localized.
- EN/RU project viewer exports: narrow screenshots and desktop screenshot inspected. At 390px and 1280px the document has no horizontal overflow. Russian summary uses `2 заметки · 1 связь`.
- Runtime language switch preserves the selected retry-policy, search text and checked graph; user content retains its authored language. Empty search changes from `No matching concepts.` to `Подходящих заметок нет.`.
- Reload restores the language encoded in the export (RU on project-ru, EN on project-en), clearing ephemeral filters as the existing behavior specifies.
- The body link opens source-material. Browser Back restores retry-policy; Forward returns to the source. Source and note show the fictional 24-hour rule.
- Materialized regression templates were exported in both languages. Native RU viewer tested `fn:example`, `fnref:example`, `fnref1:example`, and Unicode `тест` via real Markdown links; the corresponding card appears. Unicode reload preserves the card.
- Repeated footnotes and both backlinks are present; clicking the second marker and repeated backlink keeps the ordinary card rather than opening a colliding concept. The footnote moves within the document; its hash remains the concept route by existing design.
- An actual empty bundle export displays both localized empty-list and empty-bundle messages. Switching RU→EN translates both.

The Go tests additionally verify invalid-language no-publication, unchanged semantic projection, CSP digest, safe escaping and deterministic bytes. The production JS suite covers every footnote collision namespace, direct hashes, Back/Forward behavior, unknown custom values, preserved filter state and Russian plural rules. Native UI evidence does not replace these safety/regression checks.
