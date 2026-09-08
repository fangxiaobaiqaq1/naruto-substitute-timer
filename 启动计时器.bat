@echo off
cd /d "%~dp0"
if not exist "bin\timer-app.exe" (
  call build.bat
  if errorlevel 1 (
    pause
    exit /b 1
  )
)
start "" "%~dp0bin\timer-app.exe"
