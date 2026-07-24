# gorilla task runner. All build artifacts go to build/.
app := "gorilla"
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`

# Frontend dependencies.
ui-install:
    npm ci --prefix gorilla-ui/frontend

# Frontend type check.
ui-type: ui-install
    npm run check --prefix gorilla-ui/frontend

# Frontend tests.
ui-test: ui-install
    npm test --prefix gorilla-ui/frontend

# Production frontend assets.
ui-assets: ui-install
    npm run build --prefix gorilla-ui/frontend

# Verify committed Wails bindings.
ui-bindings-check:
    rm -rf build/ui-bindings-check
    mkdir -p build
    cd gorilla-ui && go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.117 generate bindings -clean -ts -noevents -d ../build/ui-bindings-check .
    diff -ru gorilla-ui/frontend/bindings build/ui-bindings-check

# Type check plus committed-binding verification.
ui-lint: ui-type ui-bindings-check

# Windows binaries (pure Go, no cgo) -> build/gorilla.exe and build/gorilla-ui.exe
build arch="amd64": ui-assets
    mkdir -p build
    GOOS=windows GOARCH={{arch}} CGO_ENABLED=0 \
        go build -ldflags "-X github.com/1dustindavis/gorilla/pkg/version.version={{version}}" \
        -o build/{{app}}.exe ./cmd/gorilla
    GOOS=windows GOARCH={{arch}} CGO_ENABLED=0 \
        go build -tags production -ldflags "-H windowsgui" \
        -o build/gorilla-ui.exe ./gorilla-ui

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
