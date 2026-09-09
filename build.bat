@echo off
setlocal
cd /d "%~dp0"
if not exist bin mkdir bin
if not defined GOCACHE set "GOCACHE=%CD%\bin\go-cache"
if not defined TIMER_VERSION set "TIMER_VERSION=development"
set "VERSION_FLAGS=-X narutotimer/internal/buildinfo.Version=%TIMER_VERSION%"
go mod download || goto :fail
echo Preparing native draw diagnostics
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\fyne-diagnostics\prepare.ps1 || goto :fail
echo [1/5] timer CLI
go build -ldflags "%VERSION_FLAGS%" -o bin\timer.exe ./cmd/timer || goto :fail
echo [2/5] calibration UI
go build -ldflags "%VERSION_FLAGS% -H windowsgui" -o bin\timer-gui.exe ./cmd/timer-gui || goto :fail
echo [3/5] timer application
go build -modfile bin\fyne-diagnostics\diagnostics.mod -overlay bin\fyne-diagnostics\overlay.json -ldflags "%VERSION_FLAGS% -H windowsgui" -o bin\timer-app.exe ./cmd/timer-app || goto :fail
echo [4/5] debug application
go build -modfile bin\fyne-diagnostics\diagnostics.mod -overlay bin\fyne-diagnostics\overlay.json -ldflags "%VERSION_FLAGS%" -o bin\timer-app-debug.exe ./cmd/timer-app || goto :fail
echo [5/5] capture and replay diagnostics
go build -ldflags "%VERSION_FLAGS%" -o bin\timer-lab.exe ./cmd/timer-lab || goto :fail
echo Build complete.
exit /b 0
:fail
echo Build failed. See the error above.
exit /b 1
