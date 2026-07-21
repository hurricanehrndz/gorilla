<#
.SYNOPSIS
Self-serve end-to-end smoke test for Gorilla (Workstream C).

.DESCRIPTION
Drives the running Gorilla service over the named pipe (via `gorilla.exe -S ...`)
against the selfserve fixture set and asserts the reconciling-run behavior on
disk: default_installs assertion, authorized install, write-time authorization,
deselect -> uninstall with prune, and the once-only default state machine.

Run AFTER bootstrap-vm.ps1 has installed and started the service against
-Manifest selfserve_manifest -Catalogs selfserve_catalog.

Exits non-zero on the first failed assertion; prints each step.
PowerShell 5.1 compatible.
#>

[CmdletBinding()]
param(
    [string]$Gorilla   = "$env:ProgramData\gorilla\bin\gorilla.exe",
    [string]$Config    = "$env:ProgramData\gorilla\config.yaml",
    [string]$SelfServe = "$env:ProgramData\gorilla\service-manifest.yaml",
    [string]$MarkerDir = "C:\ProgramData\gorilla-c-smoke",
    [int]$TimeoutSec   = 120
)

$ErrorActionPreference = "Stop"
$stepNum = 0

function Write-Step {
    param([string]$Message)
    $script:stepNum++
    Write-Host ("[{0}] {1}" -f $script:stepNum, $Message) -ForegroundColor Cyan
}

function Fail {
    param([string]$Message)
    Write-Host "ASSERTION FAILED: $Message" -ForegroundColor Red
    exit 1
}

# Invoke-Gorilla runs a service command. Returns @{Code=<int>; Out=<string[]>}.
# By default a non-zero exit is a hard failure; pass -AllowFail to inspect it.
function Invoke-Gorilla {
    param([string]$Spec, [switch]$AllowFail)
    # 2>&1 turns native stderr lines into ErrorRecords; with EAP=Stop that
    # becomes a terminating NativeCommandError. Relax EAP around the call so
    # stderr flows into $out as strings and we still check $LASTEXITCODE.
    $savedEAP = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $out = & $Gorilla -c $Config -S $Spec 2>&1
    } finally {
        $ErrorActionPreference = $savedEAP
    }
    $code = $LASTEXITCODE
    $lines = @($out | ForEach-Object { "$_" })
    Write-Host ("    gorilla -S {0} (exit {1})" -f $Spec, $code) -ForegroundColor DarkGray
    $lines | ForEach-Object { Write-Host "      $_" -ForegroundColor DarkGray }
    if (-not $AllowFail -and $code -ne 0) {
        Fail "gorilla -S $Spec exited $code"
    }
    return @{ Code = $code; Out = $lines }
}

# Get-YamlList parses a top-level YAML sequence value ("key:\n  - a\n  - b").
# Good enough for the flat self-serve manifest; returns @() when absent.
function Get-YamlList {
    param([string]$Path, [string]$Key)
    if (-not (Test-Path $Path)) { return @() }
    $items = @()
    $inKey = $false
    foreach ($raw in Get-Content -LiteralPath $Path) {
        if ($raw -match '^\s*#') { continue }
        if ($raw -match "^${Key}:\s*(.*)$") {
            $inKey = $true
            $inline = $Matches[1].Trim()
            if ($inline -and $inline -ne '[]') {
                # inline flow list e.g. key: [a, b]
                $inline.Trim('[',']').Split(',') | ForEach-Object {
                    $v = $_.Trim(); if ($v) { $items += $v }
                }
                $inKey = $false
            }
            continue
        }
        if ($inKey) {
            if ($raw -match '^\s*-\s*(.+?)\s*$') {
                $items += $Matches[1].Trim()
            } elseif ($raw -match '^\S') {
                # next top-level key ends the sequence
                $inKey = $false
            }
        }
    }
    return @($items)
}

function Wait-For {
    param([ScriptBlock]$Condition, [string]$Description)
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) { return }
        Start-Sleep -Seconds 3
    }
    Fail "timed out after ${TimeoutSec}s waiting for: $Description"
}

$optionalTxt = Join-Path $MarkerDir "optional.txt"
$defaultTxt  = Join-Path $MarkerDir "default.txt"

# --- Step 1: the service startup run asserts the default -> default.txt appears
Write-Step "Waiting for the default install (default.txt) from the startup run"
Wait-For { Test-Path $defaultTxt } "default.txt to exist"
Write-Host "    default.txt present" -ForegroundColor Green

# --- Step 2: DemoDefault recorded in both managed_installs and default_installs
Write-Step "Self-serve manifest records DemoDefault in managed_installs and default_installs"
if ((Get-YamlList $SelfServe "managed_installs") -notcontains "DemoDefault") {
    Fail "DemoDefault missing from managed_installs"
}
if ((Get-YamlList $SelfServe "default_installs") -notcontains "DemoDefault") {
    Fail "DemoDefault missing from default_installs"
}
Write-Host "    DemoDefault recorded in both lists" -ForegroundColor Green

# --- Step 3: ListOptionalInstalls offers DemoOptional
Write-Step "ListOptionalInstalls lists DemoOptional"
$list = Invoke-Gorilla "ListOptionalInstalls"
if (-not ($list.Out -match "DemoOptional")) {
    Fail "DemoOptional not listed by ListOptionalInstalls"
}
Write-Host "    DemoOptional offered" -ForegroundColor Green

# --- Step 4: InstallItem:DemoOptional -> optional.txt appears
Write-Step "InstallItem:DemoOptional installs the marker (optional.txt)"
Invoke-Gorilla "InstallItem:DemoOptional" | Out-Null
Wait-For { Test-Path $optionalTxt } "optional.txt to exist after InstallItem"
Write-Host "    optional.txt present" -ForegroundColor Green

# --- Step 5: InstallItem for an unavailable name is rejected, file unchanged
Write-Step "InstallItem:NotARealItem is rejected and leaves the manifest unchanged"
$before = (Get-Content -LiteralPath $SelfServe -Raw)
$bad = Invoke-Gorilla "InstallItem:NotARealItem" -AllowFail
if ($bad.Code -eq 0) {
    Fail "InstallItem:NotARealItem unexpectedly succeeded"
}
$after = (Get-Content -LiteralPath $SelfServe -Raw)
if ($before -ne $after) {
    Fail "self-serve manifest changed after a rejected InstallItem"
}
Write-Host "    rejected and manifest unchanged" -ForegroundColor Green

# --- Step 6: RemoveItem:DemoOptional -> optional.txt gone, uninstalls pruned empty
Write-Step "RemoveItem:DemoOptional uninstalls the marker and prunes managed_uninstalls"
Invoke-Gorilla "RemoveItem:DemoOptional" | Out-Null
Wait-For { -not (Test-Path $optionalTxt) } "optional.txt to be removed"
Wait-For { (Get-YamlList $SelfServe "managed_uninstalls").Count -eq 0 } "managed_uninstalls to be pruned empty"
Write-Host "    optional.txt gone and managed_uninstalls pruned" -ForegroundColor Green

# --- Step 7: once-only default -- a removed default does not re-assert
Write-Step "RemoveItem:DemoDefault, then a later run must NOT re-assert the default"
Invoke-Gorilla "RemoveItem:DemoDefault" | Out-Null
Wait-For { -not (Test-Path $defaultTxt) } "default.txt to be removed"
# InstallItem:DemoOptional triggers another full run.
Invoke-Gorilla "InstallItem:DemoOptional" | Out-Null
Wait-For { Test-Path $optionalTxt } "optional.txt to reappear (run happened)"
if (Test-Path $defaultTxt) {
    Fail "default.txt reappeared -- a user-removed default was re-asserted"
}
if ((Get-YamlList $SelfServe "default_installs") -notcontains "DemoDefault") {
    Fail "DemoDefault dropped from the default_installs record"
}
Write-Host "    default stayed removed and is still recorded (once-only)" -ForegroundColor Green

$blockedTxt = Join-Path $MarkerDir "blocked.txt"
$reportPath = "$env:ProgramData\gorilla\GorillaReport.json"

# Report-Defers returns $true when GorillaReport.json's DeferredItems names $Item.
# The report is rewritten at the end of every run.
function Report-Defers {
    param([string]$Item)
    if (-not (Test-Path $reportPath)) { return $false }
    try {
        $report = Get-Content -LiteralPath $reportPath -Raw | ConvertFrom-Json
    } catch {
        return $false
    }
    $deferred = $report.DeferredItems
    if (-not $deferred) { return $false }
    return @($deferred | Where-Object { $_.Name -eq $Item }).Count -gt 0
}

# --- Step 8: a running blocking app defers the install (never killed)
Write-Step "InstallItem:DemoBlocked with notepad running is deferred, not installed"
if (Test-Path $blockedTxt) { Remove-Item -LiteralPath $blockedTxt -Force }
Start-Process notepad | Out-Null
Invoke-Gorilla "InstallItem:DemoBlocked" | Out-Null
Wait-For { Report-Defers "DemoBlocked" } "GorillaReport.json DeferredItems to name DemoBlocked"
if (Test-Path $blockedTxt) {
    Fail "blocked.txt was created while notepad was running (item not deferred)"
}
Write-Host "    DemoBlocked deferred and blocked.txt absent" -ForegroundColor Green

# --- Step 9: with the blocker gone, the next run installs (retry works)
Write-Step "Stop notepad, InstallItem:DemoBlocked now installs (blocked.txt appears)"
Stop-Process -Name notepad -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1
Invoke-Gorilla "InstallItem:DemoBlocked" | Out-Null
Wait-For { Test-Path $blockedTxt } "blocked.txt to appear after the blocker stopped"
Write-Host "    blocked.txt present after retry" -ForegroundColor Green

$optionalUpdateTxt = Join-Path $MarkerDir "optional-update.txt"

# --- Step 10: update_for -- installing a referent rides its updater along
# Earlier runs predate this feature, so the updater may never have been pulled
# in. InstallItem:DemoOptional is idempotent -- it triggers a run that installs
# both the referent (optional.txt) and its updater (optional-update.txt).
Write-Step "InstallItem:DemoOptional rides DemoUpdater along (optional-update.txt appears)"
Invoke-Gorilla "InstallItem:DemoOptional" | Out-Null
Wait-For { Test-Path $optionalTxt } "optional.txt to exist"
Wait-For { Test-Path $optionalUpdateTxt } "optional-update.txt to exist (updater rode along)"
Write-Host "    optional.txt and optional-update.txt both present" -ForegroundColor Green

# --- Step 11: removal coupling -- removing the referent removes its updater
Write-Step "RemoveItem:DemoOptional removes both markers (removal coupling)"
Invoke-Gorilla "RemoveItem:DemoOptional" | Out-Null
Wait-For { -not (Test-Path $optionalTxt) } "optional.txt to be removed"
Wait-For { -not (Test-Path $optionalUpdateTxt) } "optional-update.txt to be removed (coupled)"
Wait-For { (Get-YamlList $SelfServe "managed_uninstalls").Count -eq 0 } "managed_uninstalls to be pruned empty"
Write-Host "    both markers gone and managed_uninstalls pruned" -ForegroundColor Green

# --- Honest pipe surface helpers (P6) -----------------------------------------

# Get-OptionalItem returns the parsed JSON object for one item from
# ListOptionalInstalls (the client now prints one compact JSON object per item).
function Get-OptionalItem {
    param([string]$Name)
    $res = Invoke-Gorilla "ListOptionalInstalls"
    foreach ($l in $res.Out) {
        $t = "$l".Trim()
        if (-not $t.StartsWith('{')) { continue }
        try { $obj = $t | ConvertFrom-Json } catch { continue }
        if ($obj.itemName -eq $Name) { return $obj }
    }
    return $null
}

# Parse-OperationId pulls the operationId the service returned for an accepted
# InstallItem/RemoveItem command ("operationId: <id>").
function Parse-OperationId {
    param([string[]]$Lines)
    foreach ($l in $Lines) {
        if ("$l" -match 'operationId:\s*(\S+)') { return $Matches[1] }
    }
    return $null
}

# Stream-TerminalEvent streams an operation to completion and returns the parsed
# terminal event object (the client prints each event payload as JSON and returns
# when a terminal state arrives).
function Stream-TerminalEvent {
    param([string]$OpId)
    $res = Invoke-Gorilla "StreamOperationStatus:$OpId"
    $terminal = $null
    foreach ($l in $res.Out) {
        $t = "$l".Trim()
        if (-not $t.StartsWith('{')) { continue }
        try { $ev = $t | ConvertFrom-Json } catch { continue }
        if (@('Succeeded','Failed','Deferred','Canceled') -contains $ev.state) { $terminal = $ev }
    }
    return $terminal
}

# --- Step 12: honest ListOptionalInstalls -- metadata + real status
Write-Step "ListOptionalInstalls carries DemoOptional R8 metadata and an honest status"
# Step 11 removed optional.txt, so the honest status must be NotInstalled.
$demo = Get-OptionalItem "DemoOptional"
if (-not $demo) { Fail "DemoOptional not present in ListOptionalInstalls payload" }
if ($demo.description -ne "A self-service optional install used by the Workstream C smoke test") {
    Fail "DemoOptional description mismatch: '$($demo.description)'"
}
if ($demo.category -ne "Smoke")   { Fail "DemoOptional category mismatch: '$($demo.category)'" }
if ($demo.developer -ne "Gorilla") { Fail "DemoOptional developer mismatch: '$($demo.developer)'" }
$expectStatus = if (Test-Path $optionalTxt) { "Installed" } else { "NotInstalled" }
if ($demo.status -ne $expectStatus) {
    Fail "DemoOptional status '$($demo.status)' does not match marker state (expected $expectStatus)"
}
Write-Host "    DemoOptional metadata present and status=$($demo.status) matches disk" -ForegroundColor Green

# --- Step 13: honest terminal event for a failing install
Write-Step "InstallItem:DemoFailing then StreamOperationStatus reports terminal Failed/item_failed"
$installOut = Invoke-Gorilla "InstallItem:DemoFailing"
$opId = Parse-OperationId $installOut.Out
if (-not $opId) { Fail "no operationId returned for InstallItem:DemoFailing" }
$terminal = Stream-TerminalEvent $opId
if (-not $terminal)                        { Fail "no terminal event received for DemoFailing" }
if ($terminal.state -ne "Failed")          { Fail "expected terminal Failed, got '$($terminal.state)'" }
if ($terminal.errorCode -ne "item_failed") { Fail "expected errorCode item_failed, got '$($terminal.errorCode)'" }
Write-Host "    DemoFailing terminal event Failed/item_failed" -ForegroundColor Green

# --- Step 14: honest terminal event for a deferred install
Write-Step "notepad running + InstallItem:DemoBlocked then stream reports terminal Deferred"
# blocked.txt exists from step 9; delete it so the install is actually needed and
# the blocking gate fires (an already-satisfied item is never deferred).
if (Test-Path $blockedTxt) { Remove-Item -LiteralPath $blockedTxt -Force }
Start-Process notepad | Out-Null
try {
    $installOut = Invoke-Gorilla "InstallItem:DemoBlocked"
    $opId = Parse-OperationId $installOut.Out
    if (-not $opId) { Fail "no operationId returned for InstallItem:DemoBlocked" }
    $terminal = Stream-TerminalEvent $opId
    if (-not $terminal)                 { Fail "no terminal event received for DemoBlocked" }
    if ($terminal.state -ne "Deferred") { Fail "expected terminal Deferred, got '$($terminal.state)'" }
} finally {
    Stop-Process -Name notepad -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
}
Write-Host "    DemoBlocked terminal event Deferred" -ForegroundColor Green

# --- Step 15: full regression -- re-verify the still-standing invariants on the
# final code. Defaults are once-only (cannot be reset in-script), so this replays
# the round-trips that remain repeatable rather than resetting markers.
Write-Step "Regression: self-serve manifest shape is Munki's three sorted lists"
$installs = Get-YamlList $SelfServe "managed_installs"
$sortedInstalls = @($installs | Sort-Object)
if (-not ($installs -join ',').Equals(($sortedInstalls -join ','))) {
    Fail "managed_installs is not sorted: $($installs -join ', ')"
}
if ((Get-YamlList $SelfServe "default_installs") -notcontains "DemoDefault") {
    Fail "default_installs record lost DemoDefault"
}
Write-Host "    manifest shape intact (sorted installs, defaults recorded)" -ForegroundColor Green

Write-Step "Regression: ListOptionalInstalls still lists DemoOptional with metadata"
$demo = Get-OptionalItem "DemoOptional"
if (-not $demo -or $demo.developer -ne "Gorilla") { Fail "DemoOptional metadata regression" }
Write-Host "    DemoOptional still listed with metadata" -ForegroundColor Green

Write-Step "Regression: DemoOptional install/remove round-trip incl updater coupling"
Invoke-Gorilla "InstallItem:DemoOptional" | Out-Null
Wait-For { Test-Path $optionalTxt } "optional.txt to exist"
Wait-For { Test-Path $optionalUpdateTxt } "optional-update.txt to exist (updater rode along)"
Invoke-Gorilla "RemoveItem:DemoOptional" | Out-Null
Wait-For { -not (Test-Path $optionalTxt) } "optional.txt to be removed"
Wait-For { -not (Test-Path $optionalUpdateTxt) } "optional-update.txt to be removed (coupled)"
Wait-For { (Get-YamlList $SelfServe "managed_uninstalls").Count -eq 0 } "managed_uninstalls to be pruned empty"
Write-Host "    DemoOptional round-trip + updater coupling + prune OK" -ForegroundColor Green

Write-Step "Regression: DemoBlocked defer-then-retry round-trip"
if (Test-Path $blockedTxt) { Remove-Item -LiteralPath $blockedTxt -Force }
Start-Process notepad | Out-Null
Invoke-Gorilla "InstallItem:DemoBlocked" | Out-Null
Wait-For { Report-Defers "DemoBlocked" } "GorillaReport.json DeferredItems to name DemoBlocked"
if (Test-Path $blockedTxt) { Fail "blocked.txt created while notepad running" }
Stop-Process -Name notepad -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1
Invoke-Gorilla "InstallItem:DemoBlocked" | Out-Null
Wait-For { Test-Path $blockedTxt } "blocked.txt to appear after the blocker stopped"
Write-Host "    DemoBlocked defer-then-retry OK" -ForegroundColor Green

Write-Host ""
Write-Host "SELF-SERVE SMOKE PASSED" -ForegroundColor Green
exit 0
