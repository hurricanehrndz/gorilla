# Marker uninstaller for demoblocked (Workstream C self-serve smoke)
$ErrorActionPreference = "Stop"
Remove-Item -Path "C:\ProgramData\gorilla-c-smoke\blocked.txt" -Force -ErrorAction SilentlyContinue
