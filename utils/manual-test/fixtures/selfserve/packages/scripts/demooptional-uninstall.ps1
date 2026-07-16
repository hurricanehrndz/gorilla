# Marker uninstaller for demooptional (Workstream C self-serve smoke)
$ErrorActionPreference = "Stop"
Remove-Item -Path "C:\ProgramData\gorilla-c-smoke\optional.txt" -Force -ErrorAction SilentlyContinue
