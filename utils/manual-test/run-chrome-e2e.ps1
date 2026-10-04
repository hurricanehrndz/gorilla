<#
.SYNOPSIS
Real-package end-to-end test for Gorilla: install and remove Google Chrome
through the service pipe and assert every observable side effect.

.DESCRIPTION
Drives the running Gorilla service (via `gorilla.exe -S ...`) against the e2e
fixture set built by build-e2e-repo.sh and asserts, in order:
  - GetBranding returns the config.yaml branding block e2e-chrome.sh appended, a
    policy Title under HKLM\SOFTWARE\Policies\Gorilla\Branding wins over it, and
    removing the policy brings the config title back
  - the item is offered with its catalog metadata and an honest NotInstalled status
  - DemoRequired, an admin managed_installs item, is listed isRequired and RemoveItem
    of it is refused with item_not_removable
  - CancelOperation withdraws an InstallItem queued behind a busy run: the operation
    ends Canceled by the user, nothing is installed, the selection is reverted, and a
    second cancel is refused with operation_not_cancelable
  - Restart-Service completes within 30 s while a managed run is busy
  - InstallItem answers with a 32-hex operationId and requestedBy naming the calling
    user, streams Downloading/Installing/ItemCompleted (each record carrying
    requestedBy) and ends Succeeded
  - the MSI really installed: Uninstall registry entry at the catalog version, chrome.exe on disk
  - inventory.json records the item as installed with requested_by, with the documented ACL
  - the self-serve manifest records the selection
  - a running chrome.exe defers RemoveItem (blocking_apps) and the inventory says why
  - with Chrome closed, RemoveItem streams Removing/ItemCompleted, ends Succeeded, and the
    registry entry, chrome.exe and the self-serve selection are gone

Run AFTER bootstrap-vm.ps1 has installed and started the service against
-Manifest e2e_manifest. Chrome must not be installed when it starts; it leaves
Chrome uninstalled.

Exits non-zero on the first failed assertion; prints each step.
PowerShell 5.1 compatible.
#>

[CmdletBinding()]
param(
    [string]$Gorilla   = "$env:ProgramData\gorilla\bin\gorilla.exe",
    [string]$Config    = "$env:ProgramData\gorilla\config.yaml",
    [string]$SelfServe = "$env:ProgramData\gorilla\service-manifest.yaml",
    [string]$Inventory = "$env:ProgramData\gorilla\inventory.json",
    [string]$ItemName  = "GoogleChrome",
    [string]$RegistryName = "Google Chrome",
    [string]$ChromeExe = "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
    [int]$TimeoutSec   = 600,
    [string]$BrandingTitle = "Acme Software Center"
)

$ErrorActionPreference = "Stop"
$stepNum = 0

function Write-Step {
    param([string]$Message)
    $script:stepNum++
    Write-Host ("[{0}] {1}" -f $script:stepNum, $Message) -ForegroundColor Cyan
}

function Pass { param([string]$Message) Write-Host "    $Message" -ForegroundColor Green }

function Fail {
    param([string]$Message)
    Write-Host "ASSERTION FAILED: $Message" -ForegroundColor Red
    exit 1
}

function Invoke-Gorilla {
    param([string]$Spec, [switch]$AllowFail)
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
    if (-not $AllowFail -and $code -ne 0) {
        $lines | ForEach-Object { Write-Host "      $_" -ForegroundColor DarkGray }
        Fail "gorilla -S $Spec exited $code"
    }
    return @{ Code = $code; Out = $lines }
}

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
                $inline.Trim('[',']').Split(',') | ForEach-Object { $v = $_.Trim(); if ($v) { $items += $v } }
                $inKey = $false
            }
            continue
        }
        if ($inKey) {
            if ($raw -match '^\s*-\s*(.+?)\s*$') { $items += $Matches[1].Trim() }
            elseif ($raw -match '^\S') { $inKey = $false }
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

function Parse-OperationId {
    param([string[]]$Lines)
    foreach ($l in $Lines) { if ("$l" -match 'operationId:\s*(\S+)') { return $Matches[1] } }
    return $null
}

function Parse-RequestedBy {
    param([string[]]$Lines)
    foreach ($l in $Lines) { if ("$l" -match 'requestedBy:\s*(\S+)') { return $Matches[1] } }
    return $null
}

# The service resolves the caller from the pipe; the gate calls as this user.
$Caller = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name

function Stream-OperationEvents {
    param([string]$OpId)
    $res = Invoke-Gorilla "StreamOperationStatus:$OpId"
    $events = @()
    foreach ($l in $res.Out) {
        $t = "$l".Trim()
        if (-not $t.StartsWith('{')) { continue }
        try { $ev = $t | ConvertFrom-Json } catch { continue }
        if ($ev.operationId -eq $OpId -and $ev.state) { $events += $ev }
    }
    $summary = ($events | ForEach-Object { "{0}:{1}@{2}" -f $_.itemName, $_.state, $_.progressPercent }) -join " "
    Write-Host "    events: $summary" -ForegroundColor DarkGray
    return $events
}

function Get-TerminalEvent {
    param([object[]]$Events)
    $terminal = $null
    foreach ($ev in $Events) {
        if (@('Succeeded','Failed','Deferred','Canceled') -contains $ev.state) { $terminal = $ev }
    }
    return $terminal
}

function Assert-ItemProgress {
    param([object[]]$Events, [string]$Name, [string]$ActionState)
    $itemEvents = @($Events | Where-Object { $_.itemName -eq $Name })
    if ($itemEvents.Count -eq 0) { Fail "$Name was not distinguishable in the stream" }
    foreach ($state in @('Downloading', $ActionState, 'ItemCompleted')) {
        if (@($itemEvents | Where-Object { $_.state -eq $state }).Count -eq 0) {
            Fail "$Name stream missing mapped state $state"
        }
    }
    $previous = $null
    foreach ($ev in $itemEvents) {
        if ($null -ne $previous -and [int]$ev.progressPercent -lt [int]$previous.progressPercent) {
            Fail "progress reset within $Name : $($previous.progressPercent) -> $($ev.progressPercent)"
        }
        $previous = $ev
    }
}

# Get-UninstallEntry finds the Add/Remove Programs entry by DisplayName in both
# the native and WOW6432 views of HKLM, like Gorilla's registry check does.
function Get-UninstallEntry {
    param([string]$DisplayName)
    $roots = @(
        'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall',
        'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall'
    )
    foreach ($root in $roots) {
        foreach ($key in (Get-ChildItem $root -ErrorAction SilentlyContinue)) {
            $p = Get-ItemProperty $key.PSPath -ErrorAction SilentlyContinue
            if ($p.DisplayName -eq $DisplayName) { return $p }
        }
    }
    return $null
}

function Read-Inventory {
    if (-not (Test-Path $Inventory)) { return $null }
    try { return (Get-Content -LiteralPath $Inventory -Raw | ConvertFrom-Json) } catch { return $null }
}

function Get-InventoryItem {
    param([string]$Name)
    $inv = Read-Inventory
    if (-not $inv -or -not $inv.ManagedInstalls) { return $null }
    return @($inv.ManagedInstalls | Where-Object { $_.name -eq $Name }) | Select-Object -First 1
}

function Stop-Chrome {
    Wait-For {
        Stop-Process -Name chrome -Force -ErrorAction SilentlyContinue
        -not (Get-Process -Name chrome -ErrorAction SilentlyContinue)
    } "chrome to stop"
}

function Get-Branding {
    $res = Invoke-Gorilla "GetBranding"
    foreach ($l in $res.Out) {
        $t = "$l".Trim()
        if ($t.StartsWith('{')) { return ($t | ConvertFrom-Json) }
    }
    Fail "GetBranding printed no JSON: $($res.Out -join ' | ')"
}

# --- Branding: config block, then a policy value that wins over it
Write-Step "GetBranding returns the config branding; a policy Title wins, and removing it restores the config"
$PolicyKey = 'HKLM:\SOFTWARE\Policies\Gorilla\Branding'
$b = Get-Branding
if ($b.title -ne $BrandingTitle)   { Fail "branding title '$($b.title)', expected '$BrandingTitle' from config.yaml" }
if ($b.logoMime -ne "image/png")   { Fail "branding logoMime '$($b.logoMime)', expected image/png" }
if ([int]$b.logoBytes -le 0)       { Fail "branding logo is empty" }
if ($b.accent -ne "#0b6e4f")       { Fail "branding accent '$($b.accent)'" }
if ($b.helpUrl -notmatch '^https://') { Fail "branding helpUrl '$($b.helpUrl)'" }
try {
    & reg.exe add 'HKLM\SOFTWARE\Policies\Gorilla\Branding' /v Title /t REG_SZ /d 'Policy Title' /f | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "reg add exited $LASTEXITCODE" }
    $p = Get-Branding
    if ($p.title -ne "Policy Title") { Fail "policy Title did not win: '$($p.title)'" }
    if ([int]$p.logoBytes -ne [int]$b.logoBytes) { Fail "policy Title changed an unrelated field (logo)" }
} finally {
    Remove-Item -Path $PolicyKey -Recurse -Force -ErrorAction SilentlyContinue
}
$b = Get-Branding
if ($b.title -ne $BrandingTitle) { Fail "config title not restored after removing the policy: '$($b.title)'" }
Pass "config title '$BrandingTitle' ($($b.logoMime), $($b.logoBytes) bytes); policy 'Policy Title' won; config restored"

# --- Step 1: precondition and honest NotInstalled status with metadata
Write-Step "ListOptionalInstalls offers $ItemName as NotInstalled with catalog metadata"
if (Get-UninstallEntry $RegistryName) { Fail "$RegistryName is already installed; start from a clean VM" }
$item = Get-OptionalItem $ItemName
if (-not $item) { Fail "$ItemName not present in ListOptionalInstalls payload" }
if ($item.status -ne "NotInstalled") { Fail "$ItemName status '$($item.status)', expected NotInstalled" }
if ($item.developer -ne "Google")    { Fail "developer mismatch: '$($item.developer)'" }
if ($item.category -ne "Browsers")   { Fail "category mismatch: '$($item.category)'" }
if (-not $item.version)              { Fail "version missing from payload" }
$expectedVersion = "$($item.version)"
Pass "offered: $($item.displayName) $expectedVersion by $($item.developer) [$($item.category)] status=$($item.status)"

# --- Authorization: an admin-required item cannot be removed through the pipe
Write-Step "DemoRequired (managed_installs) is listed as required and RemoveItem:DemoRequired is refused"
Wait-For { Test-Path "C:\ProgramData\gorilla-c-smoke\required.txt" } "the managed run to install DemoRequired"
$req = Get-OptionalItem "DemoRequired"
if ($req -and $req.isRequired -ne $true) { Fail "DemoRequired is offered without isRequired: $($req | ConvertTo-Json -Compress)" }
$refused = Invoke-Gorilla "RemoveItem:DemoRequired" -AllowFail
if ($refused.Code -eq 0) { Fail "RemoveItem:DemoRequired was accepted" }
if (($refused.Out -join ' ') -notmatch 'item_not_removable') { Fail "refusal lacks item_not_removable: $($refused.Out -join ' | ')" }
if ((Get-YamlList $SelfServe "managed_uninstalls") -contains "DemoRequired") { Fail "the refused removal queued DemoRequired in managed_uninstalls" }
if (-not (Test-Path "C:\ProgramData\gorilla-c-smoke\required.txt")) { Fail "DemoRequired's marker is gone" }
Pass ("listed={0} isRequired={1}; RemoveItem refused (item_not_removable); still installed" -f [bool]$req, $req.isRequired)

# --- Cancel: a queued install is withdrawn before its installer runs
Write-Step "CancelOperation withdraws a queued InstallItem:$ItemName; a second cancel is refused"
# DemoOptional's installer sleeps 3 s, so its run holds the command queue.
# InstallItem:$ItemName answers at once with its operation Queued behind that
# run, so the cancel always lands before anything has run for Chrome.
$demoOut = Invoke-Gorilla "InstallItem:DemoOptional"
$demoOp = Parse-OperationId $demoOut.Out
if (-not $demoOp) { Fail "no operationId returned for InstallItem:DemoOptional" }
$chromeOut = Invoke-Gorilla "InstallItem:$ItemName"
$chromeOp = Parse-OperationId $chromeOut.Out
if (-not $chromeOp) { Fail "no operationId returned for InstallItem:$ItemName" }
Invoke-Gorilla "CancelOperation:$chromeOp" | Out-Null
$events = @(Stream-OperationEvents $chromeOp)
$terminal = Get-TerminalEvent $events
if (-not $terminal) { Fail "no terminal event for the canceled install" }
if ($terminal.state -ne "Canceled") { Fail "canceled install ended '$($terminal.state)', expected Canceled" }
if ($terminal.canceledBy -ne "user") { Fail "canceledBy '$($terminal.canceledBy)', expected user" }
if ($terminal.itemName -ne $ItemName) { Fail "terminal itemName '$($terminal.itemName)', expected $ItemName" }
$before = @($events | Where-Object { $_.state -ne 'Canceled' } | ForEach-Object { "$($_.itemName):$($_.state)" }) -join ','
if ($before -ne "GoogleChrome:Queued") { Fail "the cancel should land while only Queued; records before it: [$before]" }
# ListOptionalInstalls waits in the queue behind the busy run and the canceled
# operation's run, so when it answers neither has installed Chrome.
$item = Get-OptionalItem $ItemName
if ($item.status -ne "NotInstalled") { Fail "$ItemName status '$($item.status)' after the cancel, expected NotInstalled" }
if (Get-UninstallEntry $RegistryName) { Fail "'$RegistryName' was installed despite the cancel" }
if ((Get-YamlList $SelfServe "managed_installs") -contains $ItemName) { Fail "the cancel left $ItemName in managed_installs" }
$demoTerminal = Get-TerminalEvent @(Stream-OperationEvents $demoOp)
if ($demoTerminal.state -ne "Succeeded") { Fail "DemoOptional, queued ahead, ended '$($demoTerminal.state)'" }
$again = Invoke-Gorilla "CancelOperation:$chromeOp" -AllowFail
if ($again.Code -eq 0) { Fail "a second cancel of a finished operation was accepted" }
if (($again.Out -join ' ') -notmatch 'operation_not_cancelable') { Fail "refusal lacks operation_not_cancelable: $($again.Out -join ' | ')" }
Pass "accepted after [$before]; Canceled by user; not installed; not selected; second cancel refused (operation_not_cancelable)"

# --- Stop: the service restarts promptly while a managed run is busy
Write-Step "Restart-Service gorilla completes within 30 s while a run is busy"
$removeOut = Invoke-Gorilla "RemoveItem:DemoOptional"
$removeTerminal = Get-TerminalEvent @(Stream-OperationEvents (Parse-OperationId $removeOut.Out))
if ($removeTerminal.state -ne "Succeeded") { Fail "RemoveItem:DemoOptional ended '$($removeTerminal.state)'" }
Invoke-Gorilla "InstallItem:DemoOptional" | Out-Null
$sw = [Diagnostics.Stopwatch]::StartNew()
Restart-Service gorilla
$restartSec = $sw.Elapsed.TotalSeconds
if ($restartSec -gt 30) { Fail ("Restart-Service took {0:n0}s while a run was busy" -f $restartSec) }
Wait-For { (Invoke-Gorilla "GetBranding" -AllowFail).Code -eq 0 } "the restarted service to answer"
Pass ("Restart-Service took {0:n1}s; the service answers again" -f $restartSec)

# --- Step 2: install through the pipe and stream it to completion
Write-Step "InstallItem:$ItemName streams Downloading/Installing/ItemCompleted and ends Succeeded"
$sw = [Diagnostics.Stopwatch]::StartNew()
$installOut = Invoke-Gorilla "InstallItem:$ItemName"
$opId = Parse-OperationId $installOut.Out
if (-not $opId) { Fail "no operationId returned for InstallItem:$ItemName" }
if ($opId -notmatch '^[0-9a-f]{32}$') { Fail "operationId '$opId' is not 32 hex characters" }
$requestedBy = Parse-RequestedBy $installOut.Out
if ($requestedBy -ne $Caller) { Fail "requestedBy '$requestedBy', expected the calling user '$Caller'" }
$events = @(Stream-OperationEvents $opId)
$terminal = Get-TerminalEvent $events
if (-not $terminal) { Fail "no terminal event for the install" }
if ($terminal.state -ne "Succeeded") { Fail "install terminal state '$($terminal.state)' (errorCode=$($terminal.errorCode))" }
Assert-ItemProgress -Events $events -Name $ItemName -ActionState "Installing"
$unattributed = @($events | Where-Object { $_.requestedBy -ne $Caller })
if ($unattributed.Count -gt 0) { Fail "$($unattributed.Count) records lack requestedBy '$Caller'" }
Pass ("install Succeeded in {0:n0}s with {1} events; operationId {2}; requestedBy {3} on every record" -f $sw.Elapsed.TotalSeconds, $events.Count, $opId, $requestedBy)

# --- Step 3: the MSI really installed
Write-Step "Registry has '$RegistryName' at $expectedVersion and chrome.exe exists"
$entry = Get-UninstallEntry $RegistryName
if (-not $entry) { Fail "no Uninstall registry entry named '$RegistryName'" }
if ("$($entry.DisplayVersion)" -ne $expectedVersion) { Fail "DisplayVersion '$($entry.DisplayVersion)' != catalog version $expectedVersion" }
if (-not (Test-Path $ChromeExe)) { Fail "$ChromeExe missing" }
$fileVersion = (Get-Item $ChromeExe).VersionInfo.ProductVersion
Pass "DisplayVersion=$($entry.DisplayVersion) chrome.exe ProductVersion=$fileVersion"

# --- Step 4: inventory.json records it, with the documented ACL
Write-Step "inventory.json lists $ItemName installed (optional_install, self_service, requested_by) with SYSTEM/Administrators-only ACL"
Wait-For { $null -ne (Get-InventoryItem $ItemName) } "inventory.json to mention $ItemName"
$inv = Read-Inventory
$rec = Get-InventoryItem $ItemName
if ($inv.schema_version -ne 1)              { Fail "schema_version $($inv.schema_version), expected 1" }
if ($inv.ManifestName -ne "e2e_manifest")   { Fail "ManifestName '$($inv.ManifestName)'" }
if (-not $rec.installed)                    { Fail "inventory installed=false" }
if ($rec.status -ne "installed")            { Fail "inventory status '$($rec.status)'" }
if ($rec.kind -ne "optional_install")       { Fail "inventory kind '$($rec.kind)'" }
if (-not $rec.self_service)                 { Fail "inventory self_service=false" }
if ($rec.requested_by -ne $Caller)          { Fail "inventory requested_by '$($rec.requested_by)', expected '$Caller'" }
if ("$($rec.installed_version)" -ne $expectedVersion) { Fail "inventory installed_version '$($rec.installed_version)'" }
if (@($inv.InstalledItems) -notcontains $ItemName)   { Fail "InstalledItems lacks $ItemName" }
if (Test-Path (Join-Path (Split-Path $Inventory) "GorillaReport.json")) { Fail "legacy GorillaReport.json still present" }
$acl = (icacls $Inventory | Out-String)
if ($acl -notmatch 'NT AUTHORITY\\SYSTEM:\(F\)')     { Fail "inventory ACL lacks SYSTEM:(F): $acl" }
if ($acl -notmatch 'BUILTIN\\Administrators:\(R\)')  { Fail "inventory ACL lacks Administrators:(R): $acl" }
$principals = @([regex]::Matches($acl, '([A-Z ]+\\[A-Za-z ]+):\(') | ForEach-Object { $_.Groups[1].Value.Trim() } | Sort-Object -Unique)
if ($principals.Count -ne 2) { Fail "inventory ACL has extra principals: $($principals -join ', ')" }
Pass "installed=$($rec.installed) status=$($rec.status) kind=$($rec.kind) version=$($rec.installed_version) requested_by=$($rec.requested_by); ACL=$($principals -join ', ')"

# --- Step 5: self-serve manifest and honest Installed status
Write-Step "Self-serve manifest records $ItemName and ListOptionalInstalls now says Installed"
if ((Get-YamlList $SelfServe "managed_installs") -notcontains $ItemName) { Fail "$ItemName missing from managed_installs" }
$item = Get-OptionalItem $ItemName
if ($item.status -ne "Installed") { Fail "$ItemName status '$($item.status)', expected Installed" }
Pass "managed_installs has $ItemName; status=Installed"

# --- Step 6: a running Chrome defers removal (blocking_apps), never killed
Write-Step "RemoveItem:$ItemName with chrome.exe running ends Deferred and the inventory says why"
Start-Process -FilePath $ChromeExe -ArgumentList @('--headless=new', '--no-first-run', '--disable-gpu', 'about:blank') | Out-Null
Wait-For { $null -ne (Get-Process -Name chrome -ErrorAction SilentlyContinue) } "chrome.exe to be running"
try {
    $removeOut = Invoke-Gorilla "RemoveItem:$ItemName"
    $opId = Parse-OperationId $removeOut.Out
    if (-not $opId) { Fail "no operationId returned for RemoveItem:$ItemName" }
    $terminal = Get-TerminalEvent @(Stream-OperationEvents $opId)
    if (-not $terminal) { Fail "no terminal event for the deferred removal" }
    if ($terminal.state -ne "Deferred") { Fail "expected Deferred, got '$($terminal.state)'" }
    if (-not (Get-Process -Name chrome -ErrorAction SilentlyContinue)) { Fail "chrome.exe was killed by the deferred removal" }
    if (-not (Get-UninstallEntry $RegistryName)) { Fail "Chrome was removed despite the blocking app" }
    Wait-For { ($r = Get-InventoryItem $ItemName) -and $r.status -eq "deferred" } "inventory.json to list $ItemName as deferred"
    $rec = Get-InventoryItem $ItemName
    if ("$($rec.deferred_reason)" -notmatch 'chrome') { Fail "deferred_reason does not name chrome: '$($rec.deferred_reason)'" }
    if ($rec.kind -ne "managed_uninstall") { Fail "deferred removal kind '$($rec.kind)', expected managed_uninstall" }
    Pass "Deferred; chrome still running; inventory: status=$($rec.status) reason='$($rec.deferred_reason)'"
} finally {
    Stop-Chrome
}

# --- Step 7: with Chrome closed, removal succeeds and every trace is gone
Write-Step "RemoveItem:$ItemName with Chrome closed streams Removing/ItemCompleted, ends Succeeded, and uninstalls"
$sw.Restart()
$removeOut = Invoke-Gorilla "RemoveItem:$ItemName"
$opId = Parse-OperationId $removeOut.Out
if (-not $opId) { Fail "no operationId returned for RemoveItem:$ItemName" }
$events = @(Stream-OperationEvents $opId)
$terminal = Get-TerminalEvent $events
if (-not $terminal) { Fail "no terminal event for the removal" }
if ($terminal.state -ne "Succeeded") { Fail "removal terminal state '$($terminal.state)' (errorCode=$($terminal.errorCode))" }
Assert-ItemProgress -Events $events -Name $ItemName -ActionState "Removing"
if (Get-UninstallEntry $RegistryName) { Fail "'$RegistryName' still in the Uninstall registry" }
if (Test-Path $ChromeExe) { Fail "$ChromeExe still present" }
Wait-For { (Get-YamlList $SelfServe "managed_installs") -notcontains $ItemName } "managed_installs to drop $ItemName"
Wait-For { (Get-YamlList $SelfServe "managed_uninstalls").Count -eq 0 } "managed_uninstalls to be pruned empty"
$inv = Read-Inventory
$rec = Get-InventoryItem $ItemName
if (-not (@($inv.RemovedItems) -contains $ItemName) -and -not ($rec -and $rec.status -eq "removed")) {
    Fail "inventory does not record the removal (RemovedItems=$($inv.RemovedItems -join ','))"
}
$item = Get-OptionalItem $ItemName
if ($item.status -ne "NotInstalled") { Fail "$ItemName status '$($item.status)' after removal, expected NotInstalled" }
Pass ("removal Succeeded in {0:n0}s; registry, chrome.exe and selection gone; status=NotInstalled" -f $sw.Elapsed.TotalSeconds)

Write-Host ""
Write-Host "CHROME E2E PASSED" -ForegroundColor Green
exit 0
