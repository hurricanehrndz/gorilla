# AGENTS.md

Guidance for coding agents working in this repository.

## Scope

- Applies to the entire repository unless a deeper `AGENTS.md` overrides it.

## Project Context

- Project: `gorilla` (Windows-focused application/package management tool in Go).
- Main entrypoint: `./cmd/gorilla`.
- Gorilla's deployed/runtime target is Windows, so Windows behavior is first-class.
- CI runs in GitHub Actions and primarily targets Windows (`windows-latest`) to match deployment expectations.
- Development often happens on macOS: keep macOS build/test/dev workflows working where practical, but do not add major complexity solely to preserve parity.
- Where appropriate, macOS/non-Windows stub or no-op behavior is acceptable if it keeps development workflows usable.

## Preferred Workflow

1. Read relevant package(s) before editing.
2. Make minimal, focused changes.
3. Run broad local validation with `make test` (tests are fast/lightweight in this repo).
4. Keep changes ready for PR review (clear commits, no unrelated edits).
5. For each task, create and use a new branch named `agent/<task-slug>` (do not work on `main`).
6. Before any commit, verify the current branch was created by this agent for this task; if not, stop and create a new `agent/<task-slug>` branch.
7. Do not commit to or push a branch you did not create unless explicitly asked to.

## Build & Test Commands

- Helpful make targets:
  - `make build`
  - `make test`
  - `make ui-lint`
  - `make ui-test`
  - `make clean`
  - `make bootstrap`
  - `make bootstrap-run`

Prefer `make test` as the default local validation step, even for small changes.
When changes include Gorilla UI code, run `make ui-lint` and `make ui-test`.
When changes span Go service/CLI and UI protocol layers, run `make test`, `make ui-lint`, and `make ui-test`.

`make build` produces both raw Windows executables — `build/gorilla.exe` and the
pure-Go, windows-GUI `build/gorilla-ui.exe`
(`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags production -ldflags "-H windowsgui"`).

## Code Style

- Use idiomatic Go and keep code gofmt-clean.
- Prefer small, explicit functions over broad refactors.
- Preserve existing package boundaries (`cmd/`, `pkg/`, `integration/`, `utils/`, `wix/`).
- Keep Gorilla UI code and related docs under `gorilla-ui/` unless there is a clear reason to place files elsewhere.
- Do not add new dependencies unless necessary, and explicitly call out/review any dependency additions in the PR.

## Windows & Integration Notes

- Be careful with path handling, newlines, and shell behavior differences.
- Changes that affect service behavior should include/adjust tests in `pkg/service` and `cmd/gorilla` when appropriate.
- When changing Windows named-pipe/service code paths, add or update Windows-only tests (`//go:build windows`) and validate on a Windows VM.
- Keep diagnostics pragmatic: prefer debug-level logging or explicit debug toggles over always-on high-volume tracing.
- Manual/integration helpers live under:
  - `integration/windows/`
  - `utils/manual-test/`

## Config & Examples

- If behavior/config changes, update examples and tests together:
  - `examples/example_config.yaml`
  - `examples/example_catalog.yaml`
  - `examples/example_manifest.yaml`
  - `examples/example_package-info.yaml`
- For UI protocol/data shape changes, keep the JSON contract aligned with the YAML model represented in:
  - `examples/example_manifest.yaml`
  - `examples/example_package-info.yaml`

## UI & Protocol Notes

- Gorilla UI is a Wails v3 application (`package main` in `gorilla-ui/`) inside
  this repository's root Go module. Its frontend is vanilla TypeScript + Vite.
- Local tooling prerequisites: the `devenv` shell (`devenv shell` / `direnv allow`)
  supplies Go, Node 22, `pkg-config`, GTK4, and WebKitGTK 6. No other SDK is needed;
  the shipped Windows binary is a pure-Go cross-build.
- Generated Wails TypeScript bindings under `gorilla-ui/frontend/bindings/` are
  committed and must never be hand-edited. Regenerate them from `gorilla-ui/` with
  the pinned command and commit the result:

      go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.117 generate bindings -clean -ts -noevents -d frontend/bindings .

  `make ui-lint` fails when the committed tree and a fresh generation differ.
- Keep `cmd/gorilla` service-message commands updated in lockstep with Gorilla UI protocol changes for testing/debugging.
- `ListOptionalInstalls` should return JSON-safe subset DTOs, not full internal item objects.
- The bound Wails surface is exactly six methods, `ListOptionalInstalls`, `InstallItem`,
  `RemoveItem`, `WatchOperation`, `GetBranding`, and `CancelOperation`, and one
  `gorilla:operation-status` event. Progress percentages are per item, not aggregate;
  only `Succeeded`, `Failed`, `Deferred`, and `Canceled` end an operation, and
  `Canceled` comes from the service (`canceledBy: "service"`) or the user (`"user"`).

## Real Windows Validation Loop

Automated checks do not cover the visible UI. For service/UI changes, validate on
a Windows VM against the real SYSTEM service.

**If `AGENTS.local.md` exists at the repository root, read it before any VM
validation.** It is gitignored and describes this machine's test rig: which VM
to use and the exact commands for each step below.

1. Start from a clean VM. `devenv shell -- make bootstrap MANUAL_TEST_BASE_URL=<url the VM can reach>`
   builds both binaries and the fixture assets. Serve them with
   `./build/manual-test-server -root build/manual-test/server-root -addr <addr>`.
2. Copy `build/manual-test/vm/bootstrap-vm.ps1`, `utils/manual-test/run-selfserve-smoke.ps1`
   and `utils/manual-test/launch-wails-ui.ps1` to the VM, then run
   `bootstrap-vm.ps1 -BaseUrl <url> -Manifest selfserve_manifest -Catalogs selfserve_catalog -InstallService -StartService -NoPause`
   as an administrator.
3. Machine-assertable gate: `run-selfserve-smoke.ps1` must exit 0 and print
   `SELF-SERVE SMOKE PASSED`.
4. Visible gate: run `launch-wails-ui.ps1` in the logged-on user's desktop session
   with a standard (non-elevated) token, so `gorilla-ui.exe` runs as the standard
   user. It writes a ready marker under `C:\gorilla-test\`, so create that
   directory first. Screenshot the desktop to judge the Home, progress, terminal,
   Activity and offline-cache states.
5. Clean up afterwards: stop the local test server and anything that exposes it
   to the VM, and remove temporary scheduled tasks.

## Diagnostics

- For diagnostics policy, behavior, and implementation guidance, follow:
  - `gorilla-ui/ARCHITECTURE.md`
  - `gorilla-ui/README.md`

## PR Expectations

- Keep PRs focused and explain user-visible behavior changes clearly.
- Call out risks and platform impact (especially Windows).
- Do not include unrelated formatting-only churn.
- After pushing a new branch, open a PR targeting `main`.
- Keep PR title and summary brief but descriptive, and ensure the summary covers all branch changes relative to `main`.
- Only push/open PR for the `agent/<task-slug>` branch created for the current task.

## Safety Rules

- Avoid destructive git commands unless explicitly requested.
- Never remove or overwrite user-authored changes outside the task scope.
- If you encounter unexpected repo state, stop and ask before proceeding.
