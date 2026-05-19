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

# Tier 1 smoke fuzz: run each fuzz target briefly to catch panics
# introduced by recent code. Corpus regression for known crashes
# already happens automatically when `just test` replays the files
# in testdata/fuzz/. Cost: ~30s total (10s per target).
fuzz-smoke:
    go test -run='^$' -fuzz=FuzzHydrate -fuzztime=10s -parallel=1 .
    go test -run='^$' -fuzz=FuzzBuilder -fuzztime=10s -parallel=1 .
    go test -run='^$' -fuzz=FuzzEventSequence -fuzztime=10s -parallel=1 .

# Tier 2 deep fuzz: run a single target for an extended window. The
# scheduled fuzz workflow invokes this once per target via a matrix
# (TARGET is one of FuzzHydrate, FuzzBuilder, FuzzEventSequence, etc).
fuzz-deep TARGET:
    go test -run='^$' -fuzz={{TARGET}} -fuzztime=10m -parallel=1 .

# Truth-tier verification for Mermaid output: re-render every golden .mmd
# through the real mermaid-js parser/renderer (via npx) and update the
# checked-in SVGs. Requires Node on PATH. Pinned to a specific mermaid-cli
# version so SVG output is reproducible — upgrades are explicit PRs that
# regenerate the SVGs and surface the visual diff for human review.
#
# Run locally before opening a PR that changes Mermaid emission.
mermaid-cli-version := "11.15.0"
mermaid-verify:
    UPDATE_GOLDEN=1 go test -count=1 -run TestMermaidGoldens .
    @mkdir -p testdata/golden/mermaid/svg
    @for mmd in testdata/golden/mermaid/*.mmd; do \
        name=$(basename $mmd .mmd); \
        echo "rendering $name"; \
        npx -y @mermaid-js/mermaid-cli@{{mermaid-cli-version}} \
            --quiet \
            -i $mmd \
            -o testdata/golden/mermaid/svg/$name.svg; \
    done
    @echo "done — review testdata/golden/mermaid/svg/ before commit"

# Everything CI runs end-to-end, in order.
ci: build lint vuln test test-race bench-ci fuzz-smoke
