# Developer notes

## Environment

The toolchain enters via [devenv](https://devenv.sh) + [direnv](https://direnv.net):
`direnv allow` (or `devenv shell`) drops you into a shell with Go 1.26, `just`,
`golangci-lint`, and `treefmt`. Formatting and linting are enforced on commit by
git-hooks (treefmt + golangci-lint on changed Go files).

Run tasks with `just`: `just build`, `just test`, `just lint`, `just fmt`,
`just check-xplat`, `just clean` (see the `justfile`).

## Build artifacts

Every build recipe writes only under `build/` (gitignored). Nothing is emitted
at the repo root.

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
