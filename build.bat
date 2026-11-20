@echo off
setlocal

set "ROOT=%~dp0"
set "VERSION=%~1"
if "%VERSION%"=="" set "VERSION=dev"

powershell -NoProfile -ExecutionPolicy Bypass -File "%ROOT%build\package\build.ps1" -Version "%VERSION%"
if errorlevel 1 (
    echo.
    echo Build failed.
    pause
    exit /b 1
)

echo.
echo Build succeeded: %ROOT%bin\clip-translator.exe
