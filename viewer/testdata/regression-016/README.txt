The .md.fixture templates keep Git checkout valid on Windows. They preserve
exact source bytes for fn:example.md, fnref:example.md and fnref1:example.md.

TestRouteFixturePreservesFootnotesAndUnusualIDs materializes these names in a
fresh directory using the existing fixture helper, then loads the real bundle
and checks the same routes, edges, provenance and Goldmark footnote markup.
It skips on Windows because those filenames cannot exist there. The production
JavaScript routing test still runs independently when Node is available.

For a manual browser regression export, materialize first on Linux or macOS.
Run from the repository root (Python is only needed for this manual recipe):

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

Open the generated viewer.html. Running view directly on this template storage
directory does not produce the colon-ID concepts.
