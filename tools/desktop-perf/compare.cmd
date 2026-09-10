@echo off
cd /d "%~dp0"
"%~dp0desktop-perf.exe" compare
if errorlevel 1 echo Collection or analysis failed. See the error above.
pause
