# Gorilla UI Architecture

## Boundary

The Wails process runs as the interactive standard user. The SYSTEM Gorilla service remains the authorization and filesystem boundary. UI code receives no Gorilla configuration, credentials, arbitrary paths, package-server settings, or internal catalog objects.

`UIService` binds exactly four calls backed by the shared `pkg/service.Client`:

1. `ListOptionalInstalls`
2. `InstallItem`
3. `RemoveItem`
4. `WatchOperation`

`WatchOperation` uses Wails' application-lifetime context and emits typed records only on `gorilla:operation-status`. The frontend imports generated calls through `frontend/src/wails-api.ts`.

## Assets and build

Production builds embed `frontend/dist` and expose `index.html` at the bundled asset root. Development Go builds use a compile-safe source filesystem, so Go tests do not require committed Vite output.

The build order is `npm ci`, Vite production assets, then a pure-Go Windows build with the `production` tag. The root module pins Wails v3; no nested Go module or alternate pipe client exists.

## Diagnostics

Structured `slog` diagnostics are opt-in through `GORILLA_UI_DEBUG=1` or `GORILLA_DEBUG=1`. Enabled logs use the existing lumberjack dependency at `%LOCALAPPDATA%\\gorilla\\ui-client.log`, 10 MiB with one backup. Lifecycle and status-event records are debug-level and include available operation, operation ID, state, result, and duration fields. Disabled diagnostics create no directory or file, and logging failures do not affect UI operations.

## Current phase limit

Phase 2 renders only a semantic heading, service status, and names returned by the real list binding. Presentation state, cache, browser mock, detail views, mutation controls, and Activity are intentionally deferred.
