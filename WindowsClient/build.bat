@echo off
setlocal
cd /d "%~dp0"

echo Building LYWP Windows server...
set CGO_ENABLED=0

echo [1/2] lywp-server.exe  (console: install / serve / hash)
go build -trimpath -ldflags="-s -w" -o bin\lywp-server.exe .
if errorlevel 1 goto :fail

echo [2/2] lywp-tray.exe    (GUI subsystem: no console window)
go build -trimpath -tags traygui -ldflags="-s -w -H windowsgui" -o bin\lywp-tray.exe .
if errorlevel 1 goto :fail

echo Done. Output:
dir bin\lywp-server.exe
dir bin\lywp-tray.exe
exit /b 0

:fail
echo Build failed.
exit /b 1
