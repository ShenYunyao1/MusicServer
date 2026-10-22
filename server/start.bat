@echo off
setlocal
if not exist "%~dp0build\musicserver.exe" (
    echo build\musicserver.exe is missing. Run build.bat first.
    exit /b 1
)

pushd "%~dp0build" || exit /b 1
"%~dp0build\musicserver.exe" %*
set "server_exit=%errorlevel%"

popd
exit /b %server_exit%
