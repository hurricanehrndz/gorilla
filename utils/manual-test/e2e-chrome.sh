#!/usr/bin/env bash
# Host-side driver for the Chrome end-to-end loop on a windows-test-rig VM.
#
#   utils/manual-test/e2e-chrome.sh [-v VM] [--no-reset] [--keep] [OUT_DIR]
#
# Builds the local repo (build-e2e-repo.sh), ships it to the VM as
# C:\gorilla-repo, bootstraps the service against url: file://C:/gorilla-repo/,
# brands the UI through a config.yaml `branding:` block, then runs the two
# machine-assertable gates (run-selfserve-smoke.ps1 and
# run-chrome-e2e.ps1) and finally launches gorilla-ui.exe on the desktop and
# captures Home, search, the strip with Cancel enabled, a canceled install,
# installing (bottom strip), installed, details and Activity screenshots into
# OUT_DIR (default build/e2e-shots). Logs go next to the screenshots.
#
# Needs the devenv shell (go, node) and the windows-test-rig skill's `rig`.
# Exit status is non-zero if either gate fails; the UI capture is visual-only
# and is judged by reading the PNGs.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
rig=${RIG:-$HOME/.claude/skills/windows-test-rig/scripts/rig}
vm=() reset=1 keep=0 out=build/e2e-shots
while (($#)); do
	case $1 in
	-v) vm=(-v "$2"); shift ;;
	--no-reset) reset=0 ;;
	--keep) keep=1 ;;
	-h | --help) sed -n 2,17p "$0"; exit 0 ;;
	*) out=$1 ;;
	esac
	shift
done
mkdir -p "$out"
mt=utils/manual-test
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

step() { printf '\n==> %s\n' "$*"; }

if ((reset)); then
	step "Resetting VM to the clean snapshot"
	"$rig" "${vm[@]}" reset
fi

step "Building the local repo"
"$mt/build-e2e-repo.sh"

step "Shipping the repo and scripts"
"$rig" "${vm[@]}" put build/e2e-repo.tar C:/rig/
cat >"$tmp/extract.ps1" <<'PS'
$ErrorActionPreference = "Stop"
if (Test-Path C:\gorilla-repo) { Remove-Item -Recurse -Force C:\gorilla-repo }
New-Item -ItemType Directory -Force C:\gorilla-repo | Out-Null
tar.exe -xf C:\rig\e2e-repo.tar -C C:\gorilla-repo --strip-components=1
if ($LASTEXITCODE -ne 0) { throw "tar failed: $LASTEXITCODE" }
# A prior loop may have left self-serve state behind; the gates assume none.
sc.exe stop gorilla 2>$null | Out-Null
foreach ($p in @("$env:ProgramData\gorilla\service-manifest.yaml", "$env:ProgramData\gorilla\inventory.json",
                 "$env:ProgramData\gorilla\cache", "C:\ProgramData\gorilla-c-smoke", "C:\gorilla-test")) {
    if (Test-Path $p) { Remove-Item -Recurse -Force $p }
}
# A OneDrive sign-in prompt appears after a VM reset and steals keystrokes.
Stop-Process -Name OneDrive -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force C:\gorilla-test | Out-Null
PS
"$rig" "${vm[@]}" ps "$tmp/extract.ps1"

step "Bootstrapping the service against file://C:/gorilla-repo/"
"$rig" "${vm[@]}" ps "$mt/bootstrap-vm.ps1" -BaseUrl file://C:/gorilla-repo/ -Manifest e2e_manifest -Catalogs e2e_catalog -InstallService -StartService -NoPause

step "Branding the UI through config.yaml"
cat >"$tmp/branding.ps1" <<'PS'
$ErrorActionPreference = "Stop"
$dir = "$env:ProgramData\gorilla\branding"
New-Item -ItemType Directory -Force $dir | Out-Null
Copy-Item C:\gorilla-repo\branding\logo.png "$dir\logo.png" -Force
# A prior --no-reset loop must not leave a policy value that outranks the config.
Remove-Item HKLM:\SOFTWARE\Policies\Gorilla\Branding -Recurse -Force -ErrorAction SilentlyContinue
Add-Content -Path "$env:ProgramData\gorilla\config.yaml" -Encoding ASCII -Value @"

branding:
  title: Acme Software Center
  tagline: Need help? Call the service desk at ext. 1234.
  logo: $dir\logo.png
  help_url: https://example.invalid/help
  help_label: Get help
  accent: "#0b6e4f"
"@
# Builds before the stop fix never finished a stop on their own (the listener
# blocked it), so stop it and, if it is still running after 30 s, end the process.
sc.exe stop gorilla | Out-Null
$deadline = (Get-Date).AddSeconds(30)
while ((Get-Service gorilla).Status -ne "Stopped" -and (Get-Date) -lt $deadline) { Start-Sleep -Seconds 1 }
if ((Get-Service gorilla).Status -ne "Stopped") {
    $svcPid = (Get-CimInstance Win32_Service -Filter "Name='gorilla'").ProcessId
    if ($svcPid -gt 0) { Stop-Process -Id $svcPid -Force }
    (Get-Service gorilla).WaitForStatus("Stopped", [TimeSpan]::FromSeconds(30))
}
Start-Service gorilla
$deadline = (Get-Date).AddSeconds(60)
do {
    $out = & "$env:ProgramData\gorilla\bin\gorilla.exe" -S GetBranding 2>&1 | Out-String
    if ($LASTEXITCODE -eq 0) { Write-Host "branding: $($out.Trim())"; exit 0 }
    Start-Sleep -Seconds 2
} while ((Get-Date) -lt $deadline)
Write-Host "GetBranding did not answer after the restart: $out"
exit 1
PS
"$rig" "${vm[@]}" ps "$tmp/branding.ps1"

status=0
step "Gate 1: self-serve smoke"
"$rig" "${vm[@]}" ps "$mt/run-selfserve-smoke.ps1" | tee "$out/smoke.log" | grep -E '^\[|PASSED|FAILED' || status=1
grep -q 'SELF-SERVE SMOKE PASSED' "$out/smoke.log" || status=1

step "Gate 2: Chrome install/remove"
"$rig" "${vm[@]}" ps "$mt/run-chrome-e2e.ps1" | tee "$out/chrome-e2e.log" | grep -E '^\[|^    [a-zA-Z]|PASSED|FAILED' || status=1
grep -q 'CHROME E2E PASSED' "$out/chrome-e2e.log" || status=1

step "Visual: Gorilla UI on the desktop"
"$rig" "${vm[@]}" run-it "$mt/launch-wails-ui.ps1"
"$rig" "${vm[@]}" shot --settle 10 "$out/01-home.png"
# Keys go to the focused window. `rig focus` also restores the window size, so
# maximize right after it and before any Tab sequence; it keeps the page's
# focus. From the top of the page the tab order is the banner's Get help,
# Software, My items, Activity, search, category, then each card's action and
# Details buttons.
ui_focus() { "$rig" "${vm[@]}" focus gorilla-ui; "$rig" "${vm[@]}" key KEY_LEFTMETA KEY_UP; sleep 2; }
tab() { local n=${1:-1}; while ((n-- > 0)); do "$rig" "${vm[@]}" key KEY_TAB; done; }
ui_focus
tab 5; "$rig" "${vm[@]}" type "Chrome"
"$rig" "${vm[@]}" shot --settle 3 "$out/02-search-chrome.png"
# Hold the service's queue with a DemoOptional run (its installer sleeps 3 s)
# so the Chrome install the UI starts next waits Queued and the strip offers
# Cancel. Dropping the markers makes DemoOptional need installing again.
cat >"$tmp/busy.ps1" <<'PS'
Remove-Item C:\ProgramData\gorilla-c-smoke\optional.txt, C:\ProgramData\gorilla-c-smoke\optional-update.txt -Force -ErrorAction SilentlyContinue
& "$env:ProgramData\gorilla\bin\gorilla.exe" -S InstallItem:DemoOptional
PS
"$rig" "${vm[@]}" ps "$tmp/busy.ps1"
tab 2; "$rig" "${vm[@]}" key KEY_ENTER
"$rig" "${vm[@]}" shot --settle 1 "$out/03a-strip-cancel.png"
# The click re-rendered the grid, so Tab restarts there: Chrome's Details (its
# Install is disabled), then the strip's Cancel.
tab 2; "$rig" "${vm[@]}" key KEY_ENTER
"$rig" "${vm[@]}" shot --settle 3 "$out/03b-canceled.png"
# Install for real. The Cancel button that had focus is gone with the strip,
# so Tab restarts at the top of the page: Get help, Software, My items,
# Activity, search, category, then Chrome's Install.
ui_focus
tab 7; "$rig" "${vm[@]}" key KEY_ENTER
"$rig" "${vm[@]}" shot --settle 4 "$out/03-installing.png"
"$rig" "${vm[@]}" shot --settle 45 "$out/04-installed.png"
"$rig" "${vm[@]}" key KEY_END
"$rig" "${vm[@]}" shot --settle 3 "$out/05-card-installed.png"
ui_focus
# The install re-rendered the grid under the focused button, so Tab restarts
# at the grid: Remove, then Details.
tab 2; "$rig" "${vm[@]}" key KEY_ENTER
"$rig" "${vm[@]}" shot --settle 3 "$out/06-details.png"
"$rig" "${vm[@]}" key KEY_ESC
ui_focus
# Ctrl+L opens Activity from anywhere; no Tab counting needed.
"$rig" "${vm[@]}" key KEY_LEFTCTRL KEY_L; "$rig" "${vm[@]}" key KEY_END
"$rig" "${vm[@]}" shot --settle 3 "$out/07-activity.png"
cat >"$tmp/verify.ps1" <<'PS'
$e = Get-ChildItem 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall','HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall' -ErrorAction SilentlyContinue |
    ForEach-Object { Get-ItemProperty $_.PSPath } | Where-Object { $_.DisplayName -eq 'Google Chrome' }
if (-not $e) { Write-Host "UI INSTALL NOT VERIFIED: no registry entry"; exit 1 }
Write-Host "UI INSTALL VERIFIED: Google Chrome $($e.DisplayVersion)"
PS
"$rig" "${vm[@]}" ps "$tmp/verify.ps1" | tee -a "$out/chrome-e2e.log" || status=1

if ((keep == 0)); then
	step "Cleanup"
	"$rig" "${vm[@]}" cleanup
fi
printf '\nScreenshots and logs: %s\n' "$out"
exit $status
