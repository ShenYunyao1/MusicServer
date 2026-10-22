param([string]$GoRoot = '')
$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    # Both platforms use the same validated build entry and stable binary names.
    & .\build.ps1 -GoRoot $GoRoot -Target windows
    if ($LASTEXITCODE -ne 0) { throw 'Windows build failed' }
    & .\build.ps1 -GoRoot $GoRoot -Target linux
    if ($LASTEXITCODE -ne 0) { throw 'Linux build failed' }

    $buildDirectory = Join-Path $PSScriptRoot 'build'
    $packages = Join-Path $buildDirectory 'packages'
    New-Item -ItemType Directory -Path $packages -Force | Out-Null
    $windowsArchive = Join-Path $packages 'musicserver-windows-amd64.zip'
    $linuxArchive = Join-Path $packages 'musicserver-linux-amd64.tar.gz'
    # Explicit inputs keep databases, music, credentials and staging out of packages.
    Compress-Archive -LiteralPath (Join-Path $buildDirectory 'musicserver.exe'),(Join-Path $PSScriptRoot 'README.md') -DestinationPath $windowsArchive -Force
    & tar -czf $linuxArchive -C $buildDirectory musicserver -C $PSScriptRoot README.md deploy/musicserver.service deploy/musicserver.env.example
    if ($LASTEXITCODE -ne 0) { throw 'Linux release archive failed' }
    Get-FileHash -Algorithm SHA256 -LiteralPath $windowsArchive,$linuxArchive | Select-Object Path,Hash
    Write-Host 'PACKAGING SUCCEEDED: build\packages\' -ForegroundColor Green
} catch {
    Write-Host "PACKAGING FAILED: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
} finally {
    Pop-Location
}
