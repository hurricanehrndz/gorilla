# Marker installer for demooptional (Workstream C self-serve smoke)
$ErrorActionPreference = "Stop"
$dir = "C:\ProgramData\gorilla-c-smoke"
New-Item -ItemType Directory -Path $dir -Force | Out-Null
Set-Content -Path (Join-Path $dir "optional.txt") -Value "demooptional" -Encoding ASCII
