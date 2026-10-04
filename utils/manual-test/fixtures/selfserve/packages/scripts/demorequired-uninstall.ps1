# Marker uninstaller for demorequired (an admin managed_installs item in the e2e manifest)
$ErrorActionPreference = "Stop"
Remove-Item -Path "C:\ProgramData\gorilla-c-smoke\required.txt" -Force -ErrorAction SilentlyContinue
