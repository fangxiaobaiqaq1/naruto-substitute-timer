@echo off
setlocal
cd /d "%~dp0"

set "TARGET_VERSION=v0.2.7"
set "APP_EXE=%~dp0bin\timer-app.exe"
set "APP_VERSION="
set "VERSION_FILE=%TEMP%\timer-app-version-%RANDOM%-%RANDOM%.txt"

rem Probe the embedded version so stale avatar/recognition binaries are never launched.
if exist "%APP_EXE%" (
  "%APP_EXE%" --version > "%VERSION_FILE%" 2>nul
  if not errorlevel 1 set /p "APP_VERSION=" < "%VERSION_FILE%"
  del /q "%VERSION_FILE%" >nul 2>&1
  if /i "%APP_VERSION%"=="%TARGET_VERSION%" goto :launch
)

echo timer-app.exe is missing, stale, or could not be verified; rebuilding %TARGET_VERSION% to include current avatars and recognition data.
set "TIMER_VERSION=%TARGET_VERSION%"
set "CGO_ENABLED=1"
call "%~dp0build.bat"
if errorlevel 1 (
  echo Build failed; timer-app.exe was not launched.
  pause
  exit /b 1
)

set "APP_VERSION="
if exist "%APP_EXE%" (
  "%APP_EXE%" --version > "%VERSION_FILE%" 2>nul
  if not errorlevel 1 set /p "APP_VERSION=" < "%VERSION_FILE%"
)
del /q "%VERSION_FILE%" >nul 2>&1
if /i not "%APP_VERSION%"=="%TARGET_VERSION%" (
  echo Build did not produce the expected %TARGET_VERSION% timer-app.exe; it was not launched.
  pause
  exit /b 1
)

:launch
start "" "%APP_EXE%" %*
