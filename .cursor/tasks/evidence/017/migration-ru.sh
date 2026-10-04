set -eux
demo_dir='/var/folders/46/5ywmz5gj26n7mnky51gd60g00000gn/T/tmp.G0A9tN2sUI'
export PATH="$demo_dir:$PATH"
okf version

migration_demo=$(mktemp -d)
cd "$migration_demo"
mkdir old-knowledge
cat > old-knowledge/index.md <<'EOF'
---
okf_version: "0.1"
---

# Заметки

* [Повторы доставки](retries.md)
* [Учебные требования](requirements.md)
EOF
cat > old-knowledge/requirements.md <<'EOF'
---
type: Note
title: Учебные требования
timestamp: 2026-06-01T10:00:00Z
---

# Учебные требования

Повторять доставку не дольше 24 часов; затем передать задачу оператору.
EOF
cat > old-knowledge/retries.md <<'EOF'
---
type: Note
title: Повторы доставки
timestamp: 2026-06-01T10:00:00Z
---

# Повторы доставки

Повторять доставку не дольше 24 часов; затем передать задачу оператору.[1]

# Citations

[1] [Учебные требования](requirements.md)
EOF
okf validate --path old-knowledge --spec 0.1

cat > citation-mappings.json <<'EOF'
[
  {"path":"retries.md","entries":[
    {"legacy_number":1,"source_id":"requirements","title":"Учебные требования","resource":"requirements.md"}
  ]}
]
EOF

okf migrate-prepare old-knowledge --output-dir migration-inputs --format json
cat migration-inputs/preparation-report.json
printf 'Вставьте source_sha256 из отчёта: '
read -r source_sha256
okf migrate old-knowledge --to 0.2 --actor human:trainer \
  --citation-mappings citation-mappings.json \
  --prepared-source-sha256 "$source_sha256" --format json

okf migrate old-knowledge --to 0.2 --actor human:trainer \
  --citation-mappings citation-mappings.json \
  --prepared-source-sha256 "$source_sha256" --write --format json
okf validate --path old-knowledge --spec 0.2 --strict
cat old-knowledge/retries.md
okf view old-knowledge --output migrated.html --lang ru

printf "VALIDATION_MIGRATION=%s\n" "$migration_demo"
