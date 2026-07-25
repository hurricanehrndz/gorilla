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

Releases publish exactly those two executables; there is no installer or code
signing in this path.

UI-specific targets: `make ui-lint` (TypeScript and generated-binding check),
`make ui-test` (frontend tests). See [gorilla-ui/README.md](gorilla-ui/README.md).

## Contributing
Pull Requests are always welcome. Before submitting, lint and test:
```
go fmt ./...
go test ./...
```

## Repo Admin Mode
Gorilla also supports local repo admin workflows:
- `-b` / `-build`: compile `packages-info/*.yaml` files into catalog files under `catalogs/`
- `-i` / `-import`: scaffold package-info data from an installer (currently stubbed as not yet implemented)

For these modes, set `repo_path` in config (or run from your repo root so the current working directory is used).
See `examples/example_package-info.yaml` for a package-info example.
