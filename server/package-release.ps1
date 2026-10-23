param([string]$GoRoot = '')
$ErrorActionPreference = 'Stop'
$packagingEnvironment=@{}
foreach($name in @('GOROOT','GOTOOLCHAIN','GOENV','CGO_ENABLED','GOOS')) { $packagingEnvironment[$name]=[Environment]::GetEnvironmentVariable($name,'Process') }
Push-Location $PSScriptRoot
try {
    # Both platforms use the same validated build entry and stable binary names.
    & .\build.ps1 -GoRoot $GoRoot -Target windows
    if ($LASTEXITCODE -ne 0) { throw 'Windows build failed' }
    & .\build.ps1 -GoRoot $GoRoot -Target linux
    if ($LASTEXITCODE -ne 0) { throw 'Linux build failed' }

    # Use the same portable packager as Actions; unsigned local packages cannot be auto-installed.
    if (!$GoRoot) {
        if (Test-Path 'D:\Develop\Runtimes\Go1.26.8Full\bin\go.exe') { $GoRoot='D:\Develop\Runtimes\Go1.26.8Full' }
        else { $GoRoot=Split-Path (Split-Path (Get-Command go.exe).Source -Parent) -Parent }
    }
    $env:GOROOT=$GoRoot
    $env:GOTOOLCHAIN='local'
    $env:GOENV='off'
    $env:CGO_ENABLED='0'
    $go=Join-Path $GoRoot 'bin/go.exe'
    $env:GOOS='linux'
    & $go build -trimpath -buildvcs=true -o build/musicserver-updater ./cmd/musicserver-updater
    if ($LASTEXITCODE -ne 0) { throw 'Linux updater build failed' }
    $env:GOOS='windows'
    & $go run -buildvcs=true ./cmd/release
    if ($LASTEXITCODE -ne 0) { throw 'Release packaging failed' }
    Write-Host 'PACKAGING SUCCEEDED: build\packages\' -ForegroundColor Green
} catch {
    Write-Host "PACKAGING FAILED: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
} finally {
    Pop-Location
    foreach($name in $packagingEnvironment.Keys) { [Environment]::SetEnvironmentVariable($name,$packagingEnvironment[$name],'Process') }
}
