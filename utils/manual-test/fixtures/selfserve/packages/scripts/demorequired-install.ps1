# Marker installer for demorequired (an admin managed_installs item in the e2e manifest)
$ErrorActionPreference = "Stop"
$dir = "C:\ProgramData\gorilla-c-smoke"
New-Item -ItemType Directory -Path $dir -Force | Out-Null
Set-Content -Path (Join-Path $dir "required.txt") -Value "demorequired" -Encoding ASCII
