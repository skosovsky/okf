# Site navigation implementation

## Changes

- Both navigation includes match `page.url` against `site.data.documentation.documents` and link the alternate locale to the registered paired URL. Missing pairs do not silently link to the homepage.
- Main navigation presents overview, first run, existing documents, agent, reference and history. Additional materials include the documentation index, MCP and migration. The releases link targets GitHub Releases.
- The default layout inserts navigation only for pages without an explicit navigation include. Russian pages receive a Russian footer and Russian site metadata defaults; page metadata takes precedence.
- Language-switch JavaScript preserves the current fragment and updates links on `hashchange`; it does not fetch pages or infer translated section IDs.
- Documentation and history indexes list registered localized documents separately. Titles come from the matching Jekyll page; source filenames are a fallback.
- Long document names wrap and code blocks scroll horizontally within the page.

## Checks

- Jekyll 3.9.5 build to `/tmp/okf017-site-navigation`: exit 0. The initial run emitted landing-page YAML warnings (unquoted title/description colons), reported to the responsible owner for correction. A final clean build is still required after all documents are written.
- Compiled EN/RU quickstart pages each contain exactly one navigation bar and link to the corresponding localized quickstart.
- Node VM check (`/tmp/okf017-locale-nav-test.cjs`): PASS for EN/RU paired path, encoded Unicode fragment, fragment updates and cleared fragments.
- Scoped `git diff --check`: PASS.

## Integration requirements

The root registry owner must add `documentation-index` and `history-index` entries and correct landing URLs to `/` and `/ru/`. The toolkit guide must expose a shared `setup` anchor. The final whole-site audit must verify registered URLs, paired anchors, page titles, and layouts after parallel editing completes.

## Integration pass (all reading editions present)

Final site build command: `GEM_SPEC_CACHE=/tmp/okf016-gem-specs GEM_HOME=/tmp/okf016-gems GEM_PATH=/tmp/okf016-gems /tmp/okf016-gems/bin/jekyll build --source docs --destination /tmp/okf017-site --trace` (exit 0; no YAML warnings).

Comprehensive checker `site-link-validation.py` parses every compiled HTML page and checks local href/src destinations, fragments, registry routes, language-link targets and EN/RU fragment parity. Viewer concept routes are validated against the exported JSON concept IDs rather than being silently skipped. Initial integration found 18 Russian reading editions using the English navigation include; the shared English include now safely delegates to the Russian include when `page.lang` is `ru`.

After that fix: 193 HTML pages, 3092 local link checks, 95 site pairs, all language targets correct, no missing files or link fragments. Thirty-three page pairs still had differing translated heading IDs; reported to document owners for shared-anchor correction. The script/report are retained and must be rerun after corrections.

## Final validation

After document owners corrected paired headings, the final Jekyll build exited 0 without warnings. The compiled checker exited 0: **193 HTML pages, 3092 local href/src checks, 95 EN/RU site pairs, zero errors**. All registered site routes, language-switch targets and compiled fragment sets match. Four demo fragments were checked against the actual exported concept IDs. The two registry documents that link directly to repository files are not Jekyll pages and are explicitly outside compiled-page pairing.

The Node fragment-transfer check remains PASS. Compiled RU/EN quickstart accessibility labels are localized to their current page language. Final full checker report: `site-link-validation.json`; reproducible checker: `site-link-validation.py`.
