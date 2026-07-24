<#
.SYNOPSIS
Launch the installed Gorilla UI for the interactive Windows smoke test.

.DESCRIPTION
Every run launches as the same interactive user, so WebView2 reuses the same
profile and localStorage (the cached item list and local Activity history)
survives between runs. WebView2 stores that profile beside the executable name
under the launching user's roaming AppData; this script never deletes it.
#>

[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$ReadyMarker = "C:\gorilla-test\ui-smoke-ready.txt"
$UIPath = Join-Path $env:ProgramData "gorilla\bin\gorilla-ui.exe"
# Wails leaves WebviewUserDataPath empty, so go-webview2 derives this path from
# %AppData% and the executable name. Only reported, never removed.
$ProfilePath = Join-Path $env:AppData "gorilla-ui.exe"

try {
    Remove-Item -Path $ReadyMarker -Force -ErrorAction SilentlyContinue

    # Close gracefully first: WebView2 flushes localStorage on shutdown, and a
    # forced kill can lose the cache the offline check depends on.
    foreach ($existing in Get-Process -Name "gorilla-ui" -ErrorAction SilentlyContinue) {
        $existing.CloseMainWindow() | Out-Null
        if (-not $existing.WaitForExit(5000)) {
            $existing | Stop-Process -Force
        }
    }

    if (-not (Test-Path $UIPath -PathType Leaf)) {
        throw "Gorilla UI is not installed at $UIPath"
    }

    $process = Start-Process -FilePath $UIPath -ArgumentList @("--pipe-name", "gorilla-service") -PassThru
    if ($process.HasExited) {
        throw "Gorilla UI exited immediately with code $($process.ExitCode)"
    }

    $deadline = (Get-Date).AddSeconds(29)
    do {
        $running = Get-Process -Id $process.Id -ErrorAction SilentlyContinue
        if ($running) {
            Start-Sleep -Seconds 1
            $running = Get-Process -Id $process.Id -ErrorAction SilentlyContinue
            if (-not $running) {
                throw "Gorilla UI exited during startup"
            }
            New-Item -ItemType Directory -Path (Split-Path -Parent $ReadyMarker) -Force | Out-Null
            Set-Content -Path $ReadyMarker -Value $process.Id -Encoding ASCII
            $reused = if (Test-Path $ProfilePath -PathType Container) { "reused" } else { "created on first run" }
            Write-Host "Gorilla UI started with PID $($process.Id) as $env:USERNAME"
            Write-Host "WebView2 profile $ProfilePath ($reused)"
            exit 0
        }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $deadline)

    throw "Gorilla UI did not remain running within 30 seconds"
}
catch {
    # Write-Error under $ErrorActionPreference = "Stop" would terminate before exit 1.
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
