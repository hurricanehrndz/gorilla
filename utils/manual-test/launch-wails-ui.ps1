<#
.SYNOPSIS
Launch the installed Gorilla UI for the interactive Windows smoke test.
#>

[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$ReadyMarker = "C:\gorilla-test\ui-smoke-ready.txt"
$UIPath = Join-Path $env:ProgramData "gorilla\bin\gorilla-ui.exe"

try {
    Remove-Item -Path $ReadyMarker -Force -ErrorAction SilentlyContinue
    Get-Process -Name "gorilla-ui" -ErrorAction SilentlyContinue | Stop-Process -Force

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
            Write-Host "Gorilla UI started with PID $($process.Id)"
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
