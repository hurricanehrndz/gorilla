# Gorilla UI

Gorilla UI is a Wails v3 desktop application in the repository's root Go module.
It runs as the logged-in standard user and calls the existing SYSTEM Gorilla
service through the shared typed client in `pkg/service`. It never receives
Gorilla configuration, credentials, package-server settings, or internal catalog
objects.

## Layout

- `main.go`: Wails application, `--pipe-name` flag (default `gorilla-service`),
  bundled WebView window titled `Gorilla UI`
- `service.go`: the bound service — `ListOptionalInstalls`, `InstallItem`,
  `RemoveItem`, `WatchOperation`
- `log.go`: opt-in diagnostics
- `assets_production.go` / `assets_development.go`: embedded `frontend/dist` under
  the `production` tag, compile-safe source filesystem otherwise
- `frontend/`: vanilla TypeScript, CSS, and Vite assets (no framework, router, or
  component library)
- `frontend/bindings/`: Wails-generated TypeScript — committed, never hand-edited
- `frontend/src/wails-api.ts` and `mock-api.ts`: the two implementations of the
  single `GorillaApi` adapter declared in `frontend/src/api.ts`

## Backend surface

Four bound methods and exactly one event channel, `gorilla:operation-status`.
Each status record carries `operationId`, `itemName`, `displayName`, `state`,
item-scoped `progressPercent`, `message`, timestamp, and terminal
error/cancellation fields. The frontend subscribes once and routes records by
`operationId`.

## Progress semantics

- `progressPercent` is **per item**. It may reset when the event's item changes
  (for example when a dependency or updater runs). It is never aggregate
  operation progress. The item's card shows the current phase and a bar; a phase
  without a percentage gets an indeterminate bar, and there is no overall
  operation indicator. The record timeline is only in Activity.
- `ItemCompleted` and `ItemFailed` are **non-terminal**. A dependency failure does
  not end the operation.
- Only `Succeeded`, `Failed`, `Deferred`, and `Canceled` end an operation.
- Installed/managed state is never inferred from progress. After every terminal
  record the UI calls `ListOptionalInstalls` and replaces the list and cache from
  that authoritative response. If that refresh fails, the previous data is kept
  and marked stale.
- `Failed`, `Deferred`, `Canceled`, pipe unavailability, request timeout, and
  premature stream end (`stream_ended`) are shown as-is and never converted into
  success.

## Cache and Activity limitations

`localStorage` keys `gorilla.optional-items.v1` and `gorilla.activity.v1` hold the
last successful list (with timestamp) and locally initiated activity. A valid cache
renders immediately, then a live refresh replaces it; a failed refresh keeps the
cached data behind a non-blocking stale/service-unavailable banner with Retry.
Corrupt or unavailable storage is ignored and never blocks a live request.

Activity is **local display history for this UI profile only** — up to the 100 most
recent records. It is not inventory, an audit log, or cross-user history, and
restarting the app does not resume an old stream; the next authoritative list
refresh provides convergence.

## Development mock

`npm run dev` serves a browser-only mock (`frontend/src/mock-api.ts`) selected by
Vite's development-mode module alias. Every other mode aliases the Wails adapter,
so the production bundle contains no mock fixture data and no
`GORILLA_VITE_MOCK_ONLY` marker.

## Commands

```sh
make ui-lint    # tsc --noEmit plus the generated-binding check
make ui-test    # node --test frontend state/cache tests
make build      # frontend assets, then build/gorilla.exe and build/gorilla-ui.exe
```

The Windows executable is a pure-Go cross-build:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -tags production -ldflags "-H windowsgui" -o build/gorilla-ui.exe ./gorilla-ui
```

Regenerate committed bindings from `gorilla-ui/` with the pinned command:

```sh
go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.117 generate bindings -clean -ts -noevents -d frontend/bindings .
```

## Diagnostics

Diagnostics are disabled by default and create no directory or file. Set
`GORILLA_UI_DEBUG=1` or `GORILLA_DEBUG=1` before launch to write structured debug
records to `%LOCALAPPDATA%\gorilla\ui-client.log`. The log rotates at 10 MiB and
keeps one backup; setup or rotation failures never block startup or fail a UI
operation.

The only runtime argument is `--pipe-name`, defaulting to `gorilla-service`.

## Windows VM procedure

The real validation loop lives in the repository `AGENTS.md`: build, bootstrap the
`dialog-win11` VM with the self-serve fixtures and the real SYSTEM service, run
`run-selfserve-smoke.ps1` (must exit 0 with `SELF-SERVE SMOKE PASSED`), then launch
`launch-wails-ui.ps1` through an interactive scheduled task and screenshot the
desktop to judge Home, item progress, terminal `Failed`/`Deferred`, Activity, and
the offline cached/stale state.
