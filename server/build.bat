@echo off
setlocal
set "build_exit=1"
pushd "%~dp0"
if errorlevel 1 (
    echo Unable to open the server source directory.
    goto finish
)

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1" %*
set "build_exit=%errorlevel%"

popd
:finish
echo.
if not "%build_exit%"=="0" echo BUILD FAILED: exit code %build_exit%. Review the error above.
if /i not "%MUSIC_SERVER_NO_PAUSE%"=="1" (
    echo Press any key to close this window...
    pause >nul
)
exit /b %build_exit%
