# gorilla task runner. All build artifacts go to build/.
app := "gorilla"
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`

# Windows agent build (pure Go, no cgo) -> build/gorilla.exe
build arch="amd64":
    GOOS=windows GOARCH={{arch}} CGO_ENABLED=0 \
        go build -ldflags "-X github.com/1dustindavis/gorilla/pkg/version.version={{version}}" \
        -o build/{{app}}.exe ./cmd/gorilla

# Guard that the tree keeps cross-compiling on Linux (CI-without-Windows goal).
check-xplat:
    GOOS=linux GOARCH=amd64 go build -o /dev/null ./...

# Go tests.
test:
    go test -cover -race ./...

# Incremental lint gate (only issues new vs {{rev}}).
lint rev="main":
    golangci-lint run --new-from-merge-base={{rev}} ./...

# Whole-tree lint audit (non-gating; drives Workstream B cleanup).
lint-all:
    golangci-lint run ./...

# Format the tree.
fmt *args:
    treefmt {{args}}

# Verify formatting without writing.
fmt-check:
    treefmt --fail-on-change --no-cache

# Remove build artifacts.
clean:
    rm -rf build/
