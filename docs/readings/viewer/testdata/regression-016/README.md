---
layout: default
title: "Viewer regression fixture on Linux and macOS"
lang: en
permalink: /readings/viewer/testdata/regression-016/README/
document_id: viewer-testdata-regression-016-readme
source: viewer/testdata/regression-016/README.txt
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
preserve_source: true
---

{% include nav.html %}

# Viewer regression fixture on Linux and macOS {#page-top}

This full reading edition preserves the instructions in [viewer/testdata/regression-016/README.txt](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/viewer/testdata/regression-016/README.txt), checked at revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. The original fixture files remain unchanged. This guide materializes a separate bundle for browser regression checks; it does not tell you to export the template directory directly.

## Materialize and inspect the fixture {#materialize}

The .md.fixture templates keep Git checkout valid on Windows. They preserve
exact source bytes for fn:example.md, fnref:example.md and fnref1:example.md.

TestRouteFixturePreservesFootnotesAndUnusualIDs materializes these names in a
fresh directory using the existing fixture helper, then loads the real bundle
and checks the same routes, edges, provenance and Goldmark footnote markup.
It skips on Windows because those filenames cannot exist there. The production
JavaScript routing test still runs independently when Node is available.

For a manual browser regression export, materialize first on Linux or macOS.
Run from the repository root (Python is only needed for this manual recipe):

```sh
fixture_dir=$(mktemp -d)
python3 - "$fixture_dir" <<'PYTHON'
from pathlib import Path
import sys
source = Path('viewer/testdata/regression-016')
target = Path(sys.argv[1])
for path in source.iterdir():
    name = path.name
    if name.endswith('.md.fixture'):
        name = name.removesuffix('.fixture').replace('-example', ':example', 1)
    elif not name.endswith('.md'):
        continue
    (target / name).write_bytes(path.read_bytes())
PYTHON
go run ./cmd/okf view "$fixture_dir" --output "$fixture_dir/viewer.html"
```

Open the generated viewer.html. Running view directly on this template storage
directory does not produce the colon-ID concepts.
