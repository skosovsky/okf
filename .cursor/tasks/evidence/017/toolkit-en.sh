set -eux
work_dir=$(mktemp -d)
go build -o "$work_dir/okf" ./cmd/okf
"$work_dir/okf" init "$work_dir/new-knowledge"

cp -R examples/project-knowledge/en "$work_dir/knowledge"

"$work_dir/okf" validate --path "$work_dir/knowledge" --spec auto --strict --check-links --check-orphans --max-warnings=0
"$work_dir/okf" search "$work_dir/knowledge" --query "delivery operator" --limit 5
"$work_dir/okf" view "$work_dir/knowledge" --output "$work_dir/knowledge.html" --lang en
"$work_dir/okf" info "$work_dir/knowledge" --spec auto
"$work_dir/okf" parse "$work_dir/knowledge/retry-policy.md" --format json

mkdir "$work_dir/team-notes"
cat > "$work_dir/team-notes/retries.md" <<'EOF'
# Retry policy
Retry failed delivery for 24 hours, then involve an operator.
[Operator steps](operator.md)
EOF
cat > "$work_dir/team-notes/operator.md" <<'EOF'
# Operator steps
Check the cause before another attempt.
EOF
"$work_dir/okf" setup --source "$work_dir/team-notes" --target "$work_dir/imported-knowledge" --type Guide

printf 'Paste the reviewed plan digest: '
read -r plan_digest
"$work_dir/okf" setup --source "$work_dir/team-notes" --target "$work_dir/imported-knowledge" --type Guide --apply --plan-digest "$plan_digest"
"$work_dir/okf" validate --path "$work_dir/imported-knowledge" --spec 0.2 --strict --check-links --check-orphans --max-warnings=0
"$work_dir/okf" view "$work_dir/imported-knowledge" --output "$work_dir/imported-knowledge.html" --lang en

"$work_dir/okf" graph "$work_dir/knowledge" --format mermaid
"$work_dir/okf" fmt "$work_dir/knowledge/retry-policy.md"
"$work_dir/okf" index "$work_dir/knowledge" --spec auto

printf "VALIDATION_WORK=%s\n" "$work_dir"
