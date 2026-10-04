# Gorilla UI Architecture

## Boundary

The Wails process runs as the interactive standard user. The SYSTEM Gorilla service remains the authorization and filesystem boundary. UI code receives no Gorilla configuration, credentials, arbitrary paths, package-server settings, or internal catalog objects.

`UIService` binds exactly six calls backed by the shared `pkg/service.Client`:

1. `ListOptionalInstalls`
2. `InstallItem`
3. `RemoveItem`
4. `WatchOperation`
5. `GetBranding`
6. `CancelOperation`

`WatchOperation` uses Wails' application-lifetime context and emits typed records only on `gorilla:operation-status`. The frontend imports generated calls through `frontend/src/wails-api.ts`.

## Progress state machine

`pkg/service` attaches an operation-scoped `installer.ProgressFn` to the run's
`installer.Runner.Emit`, so every record names a real item. Runner states map to
pipe states `Downloading`, `Installing`, `Removing`, `ItemCompleted`, and
`ItemFailed`; all five are non-terminal. Only `Succeeded`, `Failed`, `Deferred`,
and `Canceled` — from the requested item's real run report, a service
cancellation or error, or a user cancel — end an operation.

`progressPercent` is scoped to the record's `itemName` and may reset at an item
boundary. Each item's card (and its detail page) shows one status line with a
spinner while its operation runs, naming the record's item when a dependency or
updater takes over. The only bar is in the bottom `#operation-strip`, which
reports one running operation — the earliest the service has started work on —
as "n of N" with a `<progress max="100">` that is determinate only when the
latest record carries a percentage; other running items read "Waiting…" on
their cards. The percentage is still the record's item's, not an aggregate. The
record timeline is shown only in Activity, never on the Home view. No
percentage is fabricated for status checks, no-action items, blocking-app checks,
or pre/post scripts.

The app bar carries the connection state (a dot and the cached/stale message
with Retry). Above it, the `#banner` section stays hidden until branding
fills it (see Branding). "My items" is the same list filtered on the client to installed or
managed items; it is not a separate service call.

## Cancel

The strip has a Cancel button for the operation it shows. It is enabled only
while that operation's latest record is `Requested`, `Queued` or `Downloading`,
and otherwise is a real disabled button whose title says why. `CancelOperation`
skips the command queue, since the run it would stop holds that queue. The
service accepts it only while the operation is open and no run has started the
item's install or uninstall command; a running installer is never interrupted.
Anything else (item acted on, operation finished, unknown ID) is refused with
`operation_not_cancelable`, and the card shows the service's message without
changing the item.

An accepted cancel does three things. It puts back the self-serve selection the
`InstallItem` or `RemoveItem` replaced, so a later scheduled run does not
perform the request anyway. It withdraws the item from the run under way
(`installer.Cancels`): the run skips it before its download and again before
its command, and an in-flight download is aborted; other items carry on. And it
ends the operation with `Canceled` and `canceledBy: "user"`. The card then
reads "Canceled" as plain text until the next action, and Activity reads
"Canceled by you".

`InstallItem` and `RemoveItem` do not wait for the command queue either: they
write the selection, register the operation as `Queued` and answer at once, and
only the run they schedule is queued. A mutation that lands mid-run leaves that
run on the plan it loaded; the queued run picks up the newer selection. All
self-serve manifest writes, the run's included, go through
`manifest.UpdateSelfServe`, an in-process lock with a fresh load.

Installed and managed state come only from an authoritative `ListOptionalInstalls`
refresh performed after a terminal record, never from progress records. A failed
refresh keeps the prior data and marks it stale.

## Branding

Branding is admin configuration, not catalog data. The SYSTEM service resolves
it on each `GetBranding` call, field by field, first match wins:

1. REG_SZ values under `HKLM\SOFTWARE\Policies\Gorilla\Branding` (`Title`,
   `Tagline`, `LogoPath`, `HelpUrl`, `HelpLabel`, `Accent`), for MDM and GPO.
2. The `branding:` block in `config.yaml`.
3. Nothing, and the UI keeps its default look.

`pkg/branding` validates after the merge, so a bad policy value is dropped rather
than replaced by the config value. Text is trimmed, stripped of control
characters and capped (title 120, tagline 240, help label 60 runes). The help
URL must be absolute http or https. The accent must match `#rrggbb`. The logo is
read from its local path on every call, capped at 512 KiB, and kept only if its
bytes sniff as PNG, JPEG or SVG. A dropped field is logged at debug level and
never fails the call.

`GetBranding` skips the service command queue, because the UI calls it once
before creating its window to pick the window title, icon and caption colour
and must not wait behind a managed run. That startup call gives up after two
seconds and falls back to "Gorilla UI", the embedded gorilla icon and the system
caption. The caption colour goes through the Windows 11 DWM caption attributes
(`CustomTheme`), so the native title bar stays and only its colours follow the
accent; the window is not frameless. The frontend then applies the cached payload from
`gorilla.branding.v1`, fetches a fresh one and reapplies it. The banner shows
when a title, tagline, logo or help URL is set. The logo is an `<img>` from a
`data:` URL, so an SVG cannot run script. The help button opens its URL in the
system browser through the Wails Browser API after the frontend checks the
scheme again. The UI never reads `config.yaml` or the registry itself.

## Frontend state

The frontend is vanilla TypeScript with one adapter (`src/api.ts`) whose
implementation Vite selects by mode: `wails-api.ts` for production, `mock-api.ts`
for `npm run dev`. The production bundle therefore contains no mock fixture data
or `GORILLA_VITE_MOCK_ONLY` marker.

`localStorage` keys `gorilla.optional-items.v1` and `gorilla.activity.v1` provide
cache-first startup and up to 100 locally initiated activity records. Activity is
display history for this UI profile, not inventory, audit, or cross-user history;
a restart does not resume a stream. Catalog strings are written with `textContent`
only, and no catalog-provided URL is opened in the WebView.

## Assets and build

Production builds embed `frontend/dist` and expose `index.html` at the bundled asset root. Development Go builds use a compile-safe source filesystem, so Go tests do not require committed Vite output.

The build order is `npm ci`, Vite production assets, then a pure-Go Windows build with the `production` tag. The root module pins Wails v3; no nested Go module or alternate pipe client exists.

## Diagnostics

Structured `slog` diagnostics are opt-in through `GORILLA_UI_DEBUG=1` or `GORILLA_DEBUG=1`. Enabled logs use the existing lumberjack dependency at `%LOCALAPPDATA%\gorilla\ui-client.log`, 10 MiB with one backup. Lifecycle and status-event records are debug-level and include available operation, operation ID, state, result, and duration fields. Disabled diagnostics create no directory or file, and logging failures do not affect UI operations.

## Deliberate ceilings

- Progress is per item; there is no weighted or aggregate operation model. Upgrade
  only when the engine exposes a real work graph.
- Activity keeps 100 local display records and is not an audit trail. Upgrade only
  when an authoritative history operation exists.
- A closed app does not resume a stream; the next list refresh converges state.
- Icons are generated locally (monogram or category glyph); no remote icon
  transport exists.
- `OperationStatus` is hand-typed in `frontend/src/api.ts` because bindings are
  generated with `-noevents`, so the binding-diff gate cannot catch drift in it.
  Upgrade only when bindings are generated with events.
