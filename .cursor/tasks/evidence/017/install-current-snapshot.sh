set -eux
go install ./cmd/okf ./cmd/okf-mcp
"$GOBIN/okf" help
