# Gorilla UI

Gorilla UI is a Wails v3 desktop application in the repository's root Go module.
It runs as the logged-in standard user and calls the existing SYSTEM Gorilla
service through the shared typed client in `pkg/service`. It never receives
Gorilla configuration, credentials, package-server settings, or internal catalog
objects.

## Layout

- `main.go`: Wails application, `--pipe-name` flag (default `gorilla-service`),
  bundled WebView window titled with the branding title, or `Gorilla UI`
- `service.go`: the bound service — `ListOptionalInstalls`, `InstallItem`,
  `RemoveItem`, `WatchOperation`, `GetBranding`, `CancelOperation`
- `log.go`: opt-in diagnostics
- `assets_production.go` / `assets_development.go`: embedded `frontend/dist` under
  the `production` tag, compile-safe source filesystem otherwise
- `frontend/`: vanilla TypeScript, CSS, and Vite assets (no framework, router, or
  component library)
- `frontend/bindings/`: Wails-generated TypeScript — committed, never hand-edited
- `frontend/src/wails-api.ts` and `mock-api.ts`: the two implementations of the
  single `GorillaApi` adapter declared in `frontend/src/api.ts`

## Backend surface

Six bound methods and exactly one event channel, `gorilla:operation-status`.
Each status record carries `operationId`, `itemName`, `displayName`, `state`,
item-scoped `progressPercent`, `message`, timestamp, and terminal
error/cancellation fields. The frontend subscribes once and routes records by
`operationId`.

## Progress semantics

- `progressPercent` is **per item**. It may reset when the event's item changes
  (for example when a dependency or updater runs). It is never aggregate
  operation progress. An item's card shows its current phase beside a spinner;
  the one bar is in the bottom strip, for the operation being worked on, and is
  indeterminate for a phase without a percentage. The record timeline is only in
  Activity.
- `ItemCompleted` and `ItemFailed` are **non-terminal**. A dependency failure does
  not end the operation.
- Only `Succeeded`, `Failed`, `Deferred`, and `Canceled` end an operation.
- Installed/managed state is never inferred from progress. After every terminal
  record the UI calls `ListOptionalInstalls` and replaces the list and cache from
  that authoritative response. If that refresh fails, the previous data is kept
  and marked stale.
- Cancel, in the bottom strip, works only until the service starts the item's
  installer or uninstaller (`Requested`, `Queued`, `Downloading`); after that it
  is disabled, and the service refuses a late request with
  `operation_not_cancelable`. A running installer is never interrupted. An
  accepted cancel reverts the request's self-service selection, skips the item in
  the run under way (aborting its download), and ends the operation with
  `Canceled` from the user. Test it with `gorilla.exe -S CancelOperation:<id>`.
- `Failed`, `Deferred`, `Canceled`, pipe unavailability, request timeout, and
  premature stream end (`stream_ended`) are shown as-is and never converted into
  success.

## Branding

An organisation can put its name, a tagline, a logo, a help link and an accent
colour on the UI with no rebuild or re-signing, and it works offline. The SYSTEM
service resolves the branding and validates it; the UI only asks for it with
`GetBranding` and never reads `config.yaml` or the registry itself. Each field
comes from the first source that sets it:

1. Policy values under `HKLM\SOFTWARE\Policies\Gorilla\Branding`, all REG_SZ.
2. The `branding:` block in `config.yaml` (see `examples/example_config.yaml`).
3. Nothing. The UI then looks exactly as it does unbranded.

| Policy value | `config.yaml` key | Accepted |
|---|---|---|
| `Title` | `title` | Plain text, up to 120 characters. Also the window title and the app-bar name. |
| `Tagline` | `tagline` | Plain text, up to 240 characters. |
| `LogoPath` | `logo` | Local path to a PNG, JPEG or SVG of at most 512 KiB, checked by content. A PNG also becomes the window, taskbar and Alt+Tab icon. |
| `HelpUrl` | `help_url` | Absolute `http` or `https` URL, opened in the system browser. |
| `HelpLabel` | `help_label` | Plain text, up to 60 characters. Defaults to "Get help". |
| `Accent` | `accent` | `#rrggbb`. Recolours buttons, links and the banner, and on Windows 11 the native title bar (Windows 10 keeps its default caption). |

Set the policy values with Intune (a custom OMA-URI or a registry script), Group
Policy Preferences, or `reg add`:

```bat
reg add HKLM\SOFTWARE\Policies\Gorilla\Branding /v Title /t REG_SZ /d "Acme Software Center" /f
reg add HKLM\SOFTWARE\Policies\Gorilla\Branding /v Accent /t REG_SZ /d "#0b6e4f" /f
```

Policy values apply on the next `GetBranding` call. A `config.yaml` change needs
a service restart, like any other config change. A value that fails its check is
dropped (debug log only) and is not replaced by the config value. Check the
result with `gorilla.exe -S GetBranding`, which prints the payload with the logo
replaced by its size in bytes. The UI caches the last payload in
`localStorage` key `gorilla.branding.v1`, applies it at start, then fetches
again.

## Cache and Activity limitations

`localStorage` keys `gorilla.optional-items.v1` and `gorilla.activity.v1` hold the
last successful list (with timestamp) and locally initiated activity. A valid cache
renders immediately, then a live refresh replaces it; a failed refresh keeps the
cached data behind a non-blocking stale/service-unavailable notice with Retry in
the app bar.
Corrupt or unavailable storage is ignored and never blocks a live request.

Activity is **local display history for this UI profile only** — up to the 100 most
recent records. It is not inventory, an audit log, or cross-user history, and
restarting the app does not resume an old stream; the next authoritative list
refresh provides convergence.

## Keyboard

Everything is reachable with Tab and real buttons. `Ctrl+L` shows or hides
Activity (like Managed Software Center's ⌘L), and `Escape` leaves the detail page
or Activity. Both are frontend-only; nothing is added to the bound surface.

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
