@echo off
setlocal
pushd "%~dp0" || exit /b 1

if not exist "musicserver.exe" (
    echo musicserver.exe is missing. Run build.bat first.
    popd
    exit /b 1
)

"%~dp0musicserver.exe" %*
set "server_exit=%errorlevel%"

popd
exit /b %server_exit%
