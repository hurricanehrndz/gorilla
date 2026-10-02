![Gorilla logo](gorilla.png)
# Gorilla [![Go Report Card](https://goreportcard.com/badge/github.com/1dustindavis/gorilla)](https://goreportcard.com/report/github.com/1dustindavis/gorilla) [![Build status](https://github.com/1dustindavis/gorilla/actions/workflows/go-test.yml/badge.svg?branch=main)](https://github.com/1dustindavis/gorilla/actions/workflows/go-test.yml)

Munki-like Application Management for Windows

Gorilla is intended to provide application management on Windows using [Munki](https://github.com/munki/munki) as inspiration.
Gorilla supports `.msi`, `.ps1`, `.exe`, or `.nupkg` [(via chocolatey)](https://github.com/chocolatey/choco).

## Getting Started
Information related to installing and configuring Gorilla can be found on the [Wiki](https://github.com/1dustindavis/gorilla/wiki).
For quick manual-test setup helpers on a fresh Windows VM, see [utils/manual-test/README.md](utils/manual-test/README.md).

## Building

If you just want the latest version, download it from the [releases page](https://github.com/1dustindavis/gorilla/releases).

Building from source requires the [Go tools](https://golang.org/doc/install) and
Node 22 for the Gorilla UI frontend. The [devenv](https://devenv.sh) shell
(`devenv shell`, or `direnv allow`) supplies both plus `just`, `golangci-lint`,
and `treefmt` — see [docs/dev.md](docs/dev.md).

`make build` (or `just build`) produces **both** raw Windows executables in
`build/`:

- `build/gorilla.exe` — the agent/CLI/service
- `build/gorilla-ui.exe` — the Wails self-service UI (pure Go, no cgo)

Releases publish those two executables plus the standalone `makecatalogs`
binaries (see below); there is no installer or code
signing in this path.

UI-specific targets: `make ui-lint` (TypeScript and generated-binding check),
`make ui-test` (frontend tests). See [gorilla-ui/README.md](gorilla-ui/README.md).

## Contributing
Pull Requests are always welcome. Before submitting, lint and test:
```
go fmt ./...
go test ./...
```

## Building Catalogs (makecatalogs)
`makecatalogs` compiles a repo's `packages-info/*.yaml` files into
`catalogs/<catalog>.yaml`, like Munki's `makecatalogs`. It is a standalone, pure-Go
binary for Linux, macOS and Windows, so a package repo's CI never needs Windows.
It reads no Gorilla config file.

```
makecatalogs [--check] <repo_path>
```

- Without `--check`, it replaces `<repo_path>/catalogs/`. A package-info file
  with no `catalog`, a catalog name containing a path, or one that differs from
  another only by case is skipped with a warning; a duplicate item name within
  a catalog warns and the later file wins. If nothing is left to write, it
  fails and leaves `catalogs/` alone. Dotfiles such as `._foo.yaml` are ignored.
- `--check` validates the repo, prints what it would write, and writes nothing.
  It fails on those warnings too, so use it as a pull-request gate in a package
  repo.
- Exit codes: `0` success, `1` any error (or any problem under `--check`),
  `2` usage error.

Releases publish `makecatalogs-<os>-<arch>[.exe]` for linux, darwin and windows
on amd64 and arm64. `just makecatalogs` builds the same set into `build/`.
See `examples/example_package-info.yaml` for a package-info example.

`gorilla -build`/`-b` and `-import`/`-i` were removed, along with the
`repo_path` config key; existing configs that still set it keep loading.
`-import` was never implemented.
