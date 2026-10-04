set -eux
repo_root=$(pwd)
demo_dir=$(mktemp -d)
go build -o "$demo_dir/okf" ./cmd/okf
cp -R examples/project-knowledge/en "$demo_dir/my-knowledge"

"$demo_dir/okf" validate --path "$demo_dir/my-knowledge" --spec 0.2 --strict --check-links --check-orphans --max-warnings=0
"$demo_dir/okf" view "$demo_dir/my-knowledge" --output "$demo_dir/my-knowledge.html" --spec 0.2 --lang en

"$demo_dir/okf" search "$demo_dir/my-knowledge" --query "delivery operator" --limit 5
"$demo_dir/okf" search "$demo_dir/my-knowledge" --query "delivery operator" --limit 5 --json

export OKF_VALIDATION_DEMO="$demo_dir"
python3 - <<'UPDATE'
from pathlib import Path
import os
for name in ['source-material.md','retry-policy.md']:
 p=Path(os.environ['OKF_VALIDATION_DEMO'])/'my-knowledge'/name;s=p.read_text();assert '24' in s;p.write_text(s.replace('24','48'))
UPDATE
"$demo_dir/okf" validate --path "$demo_dir/my-knowledge" --spec 0.2 --strict --check-links --check-orphans --max-warnings=0
"$demo_dir/okf" search "$demo_dir/my-knowledge" --query "delivery operator" --limit 5
"$demo_dir/okf" view "$demo_dir/my-knowledge" --output "$demo_dir/my-knowledge.html" --spec 0.2 --lang en --overwrite

printf "VALIDATION_DEMO=%s\n" "$demo_dir"
