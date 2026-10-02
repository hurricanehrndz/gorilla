# osquery inventory contract

Every managed run writes `inventory.json`, a JSON file that a JSON-capable
osquery extension (fleetd, or macadmins `osquery-extension`) can turn into
`gorilla_*` tables. Its shape is Munki's `ManagedInstallReport`, with JSON in
place of plist plus a few Gorilla extras, so a table can copy macadmins'
[`tables/munki/munki.go`](https://github.com/macadmins/osquery-extension/blob/main/tables/munki/munki.go)
almost line for line.

The extension table itself is out of scope here. osquery's ATC (automatic
table construction) can't be used, because it reads SQLite databases only.

## File

- **Path:** `%ProgramData%\gorilla\inventory.json` (`AppDataPath` in
  `config.yaml`, default `C:\ProgramData\gorilla`).
- **Writer:** the managed run (`gorilla.exe` as SYSTEM, from the service or a
  scheduled run). It is written at the end of every run, including a run that
  fails part way, so the failure is visible. Check-only (`-C`) prints the same
  JSON to stdout and does not write the file.
- **Write:** the run creates a temp file in the same directory, applies the
  ACL below, writes, then renames it over `inventory.json`. A file that a user
  plants at the path is replaced, so its owner and ACL do not survive.
- **ACL:** SDDL `D:P(A;;FA;;;SY)(A;;FR;;;BA)`. The DACL is protected, so
  nothing is inherited. `icacls` shows only these two entries:

  ```text
  C:\ProgramData\gorilla\inventory.json NT AUTHORITY\SYSTEM:(F)
                                        BUILTIN\Administrators:(R)
  ```

  Standard users cannot read it. Elevated admins and SYSTEM (osquery) can.

## Fields

`schema_version` is an integer, currently `1`. It increases when a field is
renamed or removed, or its meaning changes. New fields can be added without
changing it, so readers should ignore keys they don't know.

### Top level

| Key | Type | Meaning |
|---|---|---|
| `schema_version` | int | See above. Gorilla extra. |
| `ConsoleUser` | string | Short name of the user at the console when the run ended; `""` if none. |
| `StartTime`, `EndTime` | string | Local time, `2006-01-02 15:04:05 -0700` (Munki 6 format). |
| `ManagedInstallVersion` | string | Gorilla version. |
| `ManifestName` | string | The client manifest (`manifest` in config). |
| `Errors` | []string | Run-level error (e.g. manifest fetch failed), then one `"<action> of <name> failed: <error>"` per failed item. |
| `Warnings` | []string | Reserved; currently always empty. |
| `ProblemInstalls` | []item | Install-side items that failed and are not installed. |
| `ManagedInstalls` | []item | Every item the run considered (see `kind`). |
| `InstalledItems` | []string | Names of install-side items that are installed. |
| `RemovedItems` | []string | Names of `managed_uninstall` items that are absent. |
| `ItemsToInstall` | []item | Install-side items still `pending` or `deferred`. |
| `ItemsToRemove` | []item | `managed_uninstall` items still `pending` or `deferred`. |

Every list is present, and empty rather than `null`.

### Item

The first five keys are Munki's. The rest are Gorilla extras.

| Key | Type | Meaning |
|---|---|---|
| `name` | string | Catalog item name. |
| `display_name` | string | Catalog `display_name`, or `name`. |
| `installed` | bool | Present on disk after the run. |
| `installed_version` | string | Catalog version when `installed` (Gorilla's checks report present/absent, not a version), else `""`. |
| `version_to_install` | string | Catalog version. |
| `kind` | string | Where the item came from; see below. |
| `status` | string | What the run did with it; see below. |
| `self_service` | bool | Chosen by the user (or a default) through self-service. |
| `deferred_reason` | string | Set when `status` is `deferred`. Omitted otherwise. |
| `error` | string | Set when `status` is `failed`. Omitted otherwise. |

`kind`: `managed_install`, `managed_uninstall`, `managed_update`,
`optional_install`, `default_install`, `update_for`. A name in several lists
keeps the first kind the run assigns, in this order: the admin manifests'
`managed_installs`, `managed_uninstalls`, `managed_updates`, then self-service
removals and selections, then `update_for` updaters, then offered optional
installs nobody selected. Dependencies that
the run installs but no manifest lists are reported as `managed_install`, the
way Munki handles `requires`.

`status`:

| Value | Meaning |
|---|---|
| `installed` | Installed this run, or already present. |
| `removed` | Uninstalled this run, or already absent (`managed_uninstall`). |
| `pending` | Needs action and the run didn't do it: check-only, or the run stopped first. |
| `failed` | The action failed (`error` says why). If a post-install script fails after a successful install, the item is `failed` with `installed: true` and is not in `ProblemInstalls`. |
| `deferred` | Skipped because a `blocking_apps` process was running, or a dependency was deferred. Retried next run. |
| `available` | Optional install offered but not selected. `installed` comes from a real check; items with only a script check report `false`. |

## Sample

From a real run (trimmed to two items):

```json
{
  "schema_version": 1,
  "ConsoleUser": "tester",
  "StartTime": "2026-10-01 23:29:03 -0700",
  "EndTime": "2026-10-01 23:29:05 -0700",
  "ManagedInstallVersion": "v1.1.0-3-gf72c7f9-dirty",
  "ManifestName": "selfserve_manifest",
  "Errors": ["install of DemoFailing failed: exit status 1"],
  "Warnings": [],
  "ProblemInstalls": [
    {"name": "DemoFailing", "display_name": "Demo Failing", "installed": false, "installed_version": "", "version_to_install": "1.0", "kind": "optional_install", "status": "failed", "self_service": true, "error": "exit status 1"}
  ],
  "ManagedInstalls": [
    {"name": "DemoBlocked", "display_name": "Demo Blocked", "installed": false, "installed_version": "", "version_to_install": "1.0", "kind": "optional_install", "status": "deferred", "self_service": true, "deferred_reason": "blocking application(s) running: notepad"},
    {"name": "DemoFailing", "display_name": "Demo Failing", "installed": false, "installed_version": "", "version_to_install": "1.0", "kind": "optional_install", "status": "failed", "self_service": true, "error": "exit status 1"}
  ],
  "InstalledItems": [],
  "RemovedItems": [],
  "ItemsToInstall": [
    {"name": "DemoBlocked", "display_name": "Demo Blocked", "installed": false, "installed_version": "", "version_to_install": "1.0", "kind": "optional_install", "status": "deferred", "self_service": true, "deferred_reason": "blocking application(s) running: notepad"}
  ],
  "ItemsToRemove": []
}
```

## Proposed tables

These mirror `munki_info` and `munki_installs`. All columns are text, as in
macadmins.

`gorilla_info` (one row):

| Column | Source |
|---|---|
| `version` | `ManagedInstallVersion` |
| `start_time`, `end_time` | `StartTime`, `EndTime` |
| `success` | `len(Errors) == 0` |
| `errors`, `warnings` | `Errors`, `Warnings` joined with `;` |
| `console_user` | `ConsoleUser` |
| `problem_installs` | `ProblemInstalls[].name` joined with `;` |
| `manifest_name` | `ManifestName` |
| `schema_version` | `schema_version` (extra) |

`gorilla_installs` (one row per `ManagedInstalls` entry):

| Column | Source |
|---|---|
| `name`, `display_name` | `name`, `display_name` |
| `installed` | `installed` |
| `installed_version`, `version_to_install` | same keys |
| `end_time` | top-level `EndTime` |
| `kind`, `status`, `self_service` | same keys (extras) |
| `deferred_reason`, `error` | same keys (extras) |

To keep `munki_installs` semantics, which cover only items meant to be
installed, a query can filter with `WHERE kind != 'managed_uninstall' AND status
!= 'available'`.
