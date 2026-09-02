cd "$(dirname "$0")"
go clean -modcache
go mod tidy
go run ./src
