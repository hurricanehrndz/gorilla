# Gorilla UI

Gorilla UI is a Wails v3 desktop application in the repository's root Go module. It runs as the logged-in user and calls the existing SYSTEM Gorilla service through the shared typed client in `pkg/service`.

## Layout

- `main.go`: Wails application and bundled WebView window
- `service.go`: four bound methods (`ListOptionalInstalls`, `InstallItem`, `RemoveItem`, `WatchOperation`)
- `frontend/`: vanilla TypeScript and Vite assets
- `frontend/bindings/`: generated Wails TypeScript; do not edit

## Commands

```sh
make ui-lint
make ui-test
make build
```

Regenerate committed bindings from `gorilla-ui/` with:

```sh
go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.117 generate bindings -clean -ts -noevents -d frontend/bindings .
```

The Phase 2 frontend only proves the real list binding by rendering service status and optional-item names. Home/detail/cache/mock and mutation UX arrive in later phases.

## Diagnostics

Diagnostics are disabled by default and create no file. Set `GORILLA_UI_DEBUG=1` or `GORILLA_DEBUG=1` before launch to write structured debug records to `%LOCALAPPDATA%\\gorilla\\ui-client.log`. The log rotates at 10 MiB and keeps one backup; setup failures never block startup.

The only runtime argument is `--pipe-name`, defaulting to `gorilla-service`.
