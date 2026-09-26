# Viewer browser acceptance, 2026-09-26

Browser acceptance remains **open**. A Go CLI export succeeded, but the browser-control URL policy rejected opening its local `file://` URL. The rejection explicitly prohibited reaching the same page through another URL, browser surface, or indirect browser command. No page was rendered or operated in this review, so filters, deep links, wrapping, code blocks, and large-list responsiveness cannot be claimed as browser-verified.

Reproduce the generated artifact from the repository root:

```sh
go run ./cmd/okf view knowledge --output /private/tmp/okf-viewer-009-knowledge.html --overwrite
shasum -a 256 /private/tmp/okf-viewer-009-knowledge.html
```

On this checkout the CLI printed `wrote "/private/tmp/okf-viewer-009-knowledge.html"`. The artifact was 49,302 bytes, SHA-256 `4efd1697e0002d09cbcae1104778b991b38f2c4dd1855857c0bf059454dee6a6`.

The executable checks that could run without a browser passed:

```sh
go test ./viewer -run 'Test(LargeBundleUsesBoundedListLayout|UnicodeConceptIDAndMissingMetadata)' -count=1 -v
go test ./viewer ./internal/okfcli -run 'Test(RenderEscapesScriptAndMarkdownHTML|BuildLinksLimitsAndReferenceTime|View)' -count=1 -v
```

The first command covers a 1,001-concept projection, a 1,000-node rejection, Unicode IDs, and absent metadata. The second covers the small bundle, escaping, and CLI export. These are Go-level checks; the last two acceptance boxes in `issue-009` still need a human or permitted browser session to exercise search/type filtering, deep links, long titles, Russian text, code blocks, and a 1,000-concept page visually.
