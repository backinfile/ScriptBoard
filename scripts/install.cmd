@echo off
setlocal

fltmc >nul 2>&1
if errorlevel 1 (
  echo Run install.cmd from an elevated Command Prompt or PowerShell window. 1>&2
  exit /b 1
)

if not exist "%~dp0scriptboard.exe" (
  echo This installer must remain beside scriptboard.exe in a complete release package. 1>&2
  exit /b 1
)

"%~dp0scriptboard.exe" service install --start %*
exit /b %errorlevel%
