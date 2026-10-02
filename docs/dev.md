# Developer notes

## Environment

The toolchain enters via [devenv](https://devenv.sh) + [direnv](https://direnv.net):
`direnv allow` (or `devenv shell`) drops you into a shell with Go 1.26, `just`,
`golangci-lint`, `treefmt`, and the Gorilla UI toolchain (Node 22, `pkg-config`,
GTK4, WebKitGTK 6). Formatting and linting are enforced on commit by git-hooks
(treefmt + golangci-lint on changed Go files).

Run tasks with `just`: `just build`, `just test`, `just lint`, `just fmt`,
`just check-xplat`, `just makecatalogs`, `just clean` (see the `justfile`).
UI-specific targets exist in both runners: `ui-lint` (TypeScript plus the
committed-binding check), `ui-test` (frontend tests), `ui-assets` (Vite
production build).

## Build artifacts

Every build recipe writes only under `build/` (gitignored), except the frontend
recipes: `ui-install` writes `gorilla-ui/frontend/node_modules/` and `ui-assets`
writes `gorilla-ui/frontend/dist/` (both gitignored). Nothing is emitted at the
repo root.

A normal `make build` / `just build` produces **both** raw Windows executables:
`build/gorilla.exe` and `build/gorilla-ui.exe`. The UI build runs the Vite
production build first, then embeds `gorilla-ui/frontend/dist`. No installer is
produced and nothing is signed.

## Cross-compilation: pure Go, no cgo

The agent is pure Go — it has **zero cgo** (all Windows syscalls go through
`golang.org/x/sys/windows`), so the Windows agent cross-compiles from Linux with
plain `CGO_ENABLED=0 GOOS=windows go build` (`just build`). `just check-xplat`
proves the tree also still builds for `GOOS=linux`. There is **no zig and no cgo
toolchain** in this environment, and none is needed.

**Standing convention:** should any future component ever require cgo for OS
interfacing (e.g. a native MSI/registry reader we cross-distribute), `zig cc` is
the designated cross C compiler — wire it *there*, in that component's build
recipe, not here.
