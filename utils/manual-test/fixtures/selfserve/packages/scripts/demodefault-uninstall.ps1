# Marker uninstaller for demodefault (Workstream C self-serve smoke)
$ErrorActionPreference = "Stop"
Remove-Item -Path "C:\ProgramData\gorilla-c-smoke\default.txt" -Force -ErrorAction SilentlyContinue
