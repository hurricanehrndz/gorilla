# Marker uninstaller for demoupdater (Workstream C self-serve smoke)
$ErrorActionPreference = "Stop"
Remove-Item -Path "C:\ProgramData\gorilla-c-smoke\optional-update.txt" -Force -ErrorAction SilentlyContinue
