# Manual Test Utils

This directory supports a fast macOS -> Windows VM manual test loop.

## Loop
1. Make code changes on macOS.
2. Run `make bootstrap-run` on macOS.
3. Start the local test server on macOS (included in `bootstrap-run`).
4. Copy generated VM scripts from `build/manual-test/vm/` to the VM.
5. Run one VM bootstrap script to pull the latest binary/config.
6. Run Gorilla manually on the VM.

## 1) Prepare assets on macOS
From repo root:

```bash
make bootstrap-run
```

Or if you want separate steps:

```bash
make bootstrap
./build/manual-test-server -root build/manual-test/server-root -addr :8080
```

This creates:
- `build/manual-test/server-root/gorilla.exe`
- `build/manual-test/server-root/gorilla-ui.exe`
- `build/manual-test/server-root/manifests/example_manifest.yaml`
- `build/manual-test/server-root/catalogs/example_catalog.yaml`
- `build/manual-test/server-root/packages/` (empty)
- `build/manual-test-server` (Go static file server)
- `build/manual-test/vm/bootstrap-vm.ps1`
- `build/manual-test/vm/bootstrap-vm.bat` (URL stamped automatically)
- `build/manual-test/vm/bootstrap-vm-full.ps1`
- `build/manual-test/vm/bootstrap-vm-full.bat` (URL stamped automatically)
- `build/manual-test/vm/run-gorilla-check.bat`
- `build/manual-test/vm/run-release-integration.bat`
- `build/manual-test/vm/base-url.txt` (resolved URL used for stamping)

`make bootstrap` auto-detects a URL like `http://<your-mac-ip>:8080/`.
To override:

```bash
make bootstrap MANUAL_TEST_BASE_URL=http://192.168.1.50:8080/
```

Server source lives in `utils/manual-test/server` (separate Go module).

Two VM scripts are not generated and must be copied straight from this
directory: `run-selfserve-smoke.ps1` (machine-assertable self-serve smoke test,
exits 0 and prints `SELF-SERVE SMOKE PASSED`) and `launch-wails-ui.ps1` (starts
`gorilla-ui.exe` as the interactive user).

## 2) Serve assets from macOS

```bash
./build/manual-test-server -root build/manual-test/server-root -addr :8080
```

Use your Mac's reachable IP in the VM, for example:
- `http://192.168.1.50:8080/`

## 3) Bootstrap from the Windows VM
From the copied `build/manual-test/vm/` folder on the VM:

```bat
.\bootstrap-vm.bat
```

Optional switches:
- `.\bootstrap-vm.bat -InstallService -StartService`
- One-off URL override: `.\bootstrap-vm.bat http://192.168.1.99:8080/`

For full integration-test prerequisites (go/chocolatey/WiX):

```bat
.\bootstrap-vm-full.bat
```

Optional switches:
- `.\bootstrap-vm-full.bat -InstallService -StartService`
- One-off URL override: `.\bootstrap-vm-full.bat http://192.168.1.99:8080/`

## 4) Manual run on VM

```bat
.\run-gorilla-check.bat
```

`-C` runs check-only mode so you can quickly validate config/flow without installing packages.

## 5) Run release integration script from VM

From repo root on the VM:

```bat
.\build\manual-test\vm\run-release-integration.bat
```

This helper now runs both phases in order:
- `integration/windows/prepare-release-integration.ps1`
- `integration/windows/run-release-integration.ps1`

Recommended flow first:

```bat
.\build\manual-test\vm\bootstrap-vm-full.bat
```

Optional args:
- `.\build\manual-test\vm\run-release-integration.bat C:\path\to\gorilla.exe`
- `.\build\manual-test\vm\run-release-integration.bat C:\path\to\gorilla.exe C:\temp\gorilla-release-integration`

## Chrome end-to-end loop (local file:// repo)

A real-package loop that installs and removes Google Chrome through the
service, with the repository on the VM's own disk instead of an HTTP server.

```bash
utils/manual-test/e2e-chrome.sh -v win11-test      # needs devenv shell + windows-test-rig
```

It runs `build-e2e-repo.sh` (downloads the Chrome enterprise MSI once into
`build/cache/`, renders `fixtures/e2e/packages-info/GoogleChrome.yaml.in` with
the MSI's version and SHA-256, compiles the catalog with `makecatalogs`, and
copies the selfserve fixtures and both binaries into `build/e2e-repo/`), ships
the tar to `C:\gorilla-repo`, bootstraps the service with
`-BaseUrl file://C:/gorilla-repo/ -Manifest e2e_manifest`, brands the UI by
appending a `branding:` block (title, tagline, `fixtures/e2e/branding/logo.png`,
help link, accent) to `config.yaml` and restarting the service, then runs two
gates and a visual pass:

- `run-selfserve-smoke.ps1` (prints `SELF-SERVE SMOKE PASSED`), reached here
  through `included_manifests`.
- `run-chrome-e2e.ps1` (prints `CHROME E2E PASSED`): `GetBranding` returns
  the config branding, a policy `Title` under
  `HKLM\SOFTWARE\Policies\Gorilla\Branding` wins over it and removing it
  restores the config; then metadata and NotInstalled
  status, a `CancelOperation` of a Chrome install queued behind a busy run
  (ends `Canceled` by the user, nothing installed, selection reverted, a second
  cancel refused), `Restart-Service` within 30 s while a run is busy,
  streamed install to `Succeeded`, registry entry and `chrome.exe`,
  `inventory.json` contents and ACL, self-serve manifest, deferred removal while
  `chrome.exe` runs, then a real uninstall with every trace gone.
- `gorilla-ui.exe` on the desktop, driven with the keyboard, with screenshots
  under `build/e2e-shots/`, showing the branded banner and window title.

Repository URL spellings (verified on Windows): `file://C:/gorilla-repo/`
works; `file:///C:/gorilla-repo/` returns 404 from the file transport, and a
bare `C:/gorilla-repo/` or `C:\gorilla-repo\` fails with "unsupported protocol
scheme".
