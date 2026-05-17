# gstate task runner — see https://just.systems
#
# `just` is the entry point for repo-local commands. CI runs the same
# recipes, so anything green locally via `just ci` should be green in CI.
#
# Get `just` itself: https://just.systems/man/en/chapter_4.html
# (e.g. `brew install just`, `cargo install just`, or asdf).

# Show available recipes.
default:
    @just --list

# Install dev tools (latest) into $GOBIN (defaults to ~/go/bin).
install-tools:
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
    go install golang.org/x/vuln/cmd/govulncheck@latest

# Run all lint checks. CI gate.
lint:
    go vet ./...
    golangci-lint run ./...

# Scan dependencies for known vulnerabilities (govulncheck). CI gate.
vuln:
    govulncheck ./...

# Apply safe auto-fixes (gofmt simplifications, golangci-lint --fix).
# Run before `git commit` if `just lint` flagged anything fixable.
fix:
    gofmt -s -w .
    golangci-lint run --fix ./...

# Build all packages.
build:
    go build ./...

# Run tests.
test:
    go test -count=1 ./...

# Run tests with the race detector. CI gate.
test-race:
    go test -race -count=1 ./...

# Run benchmarks with memory stats. Use locally for real measurements.
bench:
    go test -bench=. -benchmem -count=3 -benchtime=1s -run='^$' ./...

# Compile-gate benchmarks: run each benchmark exactly once so CI catches
# benchmark code rotting without paying for real timing. Cost: ~2s.
bench-ci:
    go test -run='^$' -bench=. -benchtime=1x ./...

# Trend benchmarks: same args as `bench`, but writes output to bench.txt
# so CI's Tier 2 workflow can upload it as an artifact for offline
# benchstat comparison.
bench-trend:
    go test -bench=. -benchmem -count=3 -benchtime=1s -run='^$' ./... | tee bench.txt

# Everything CI runs end-to-end, in order.
ci: build lint vuln test test-race bench-ci
