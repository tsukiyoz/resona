@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0desktop\scripts\build-windows.ps1" %*
exit /b %errorlevel%
