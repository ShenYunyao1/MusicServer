@echo off
setlocal
pushd "%~dp0" || exit /b 1

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1" %*
set "build_exit=%errorlevel%"

popd
exit /b %build_exit%
