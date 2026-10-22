@echo off
setlocal
set "server_exit=1"
if not exist "%~dp0build\musicserver.exe" (
    echo build\musicserver.exe is missing. Run build.bat first.
    goto finish
)

pushd "%~dp0build"
if errorlevel 1 (
    echo Unable to open the build directory.
    goto finish
)
"%~dp0build\musicserver.exe" %*
set "server_exit=%errorlevel%"

popd
:finish
echo.
if not "%server_exit%"=="0" echo ERROR: MusicServer exited with code %server_exit%. Review the error above.
if /i not "%MUSIC_SERVER_NO_PAUSE%"=="1" (
    echo Press any key to close this window...
    pause >nul
)
exit /b %server_exit%
