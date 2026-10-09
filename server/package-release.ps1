param([string]$GoRoot = 'D:\Develop\Runtimes\Go1.26.8Full')
$ErrorActionPreference = 'Stop'
$go = Join-Path $GoRoot 'bin\go.exe'
if (!(Test-Path -LiteralPath $go)) { throw "Go executable not found: $go" }
$original = @{}
foreach ($name in @('GOROOT','GOTOOLCHAIN','GOCACHE','GOPATH','GOENV','GOOS','GOARCH')) {
    $original[$name] = [Environment]::GetEnvironmentVariable($name,'Process')
}
Push-Location $PSScriptRoot
try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    & .\build.ps1 -GoRoot $GoRoot
    $env:GOROOT = $GoRoot
    $env:GOTOOLCHAIN = 'local'
    $env:GOENV = 'off'
    $env:GOCACHE = 'D:\Develop\Runtimes\GoBuildCache26'
    $env:GOPATH = 'D:\Develop\Runtimes\GoPath26'
    $dist = Join-Path $PSScriptRoot 'dist'
    New-Item -ItemType Directory -Path $dist -Force | Out-Null

    $windowsArchive = Join-Path $dist 'musicserver-windows-amd64.zip'
    $linuxArchive = Join-Path $dist 'musicserver-linux-amd64.tar.gz'
    # Replace prior release archives so no stale entries survive a rebuild.
    Remove-Item -LiteralPath $windowsArchive,$linuxArchive -ErrorAction SilentlyContinue
    Compress-Archive -LiteralPath (Join-Path $PSScriptRoot 'musicserver.exe'),(Join-Path $PSScriptRoot 'README.md') -DestinationPath $windowsArchive

    $env:GOOS = 'linux'
    $env:GOARCH = 'amd64'
    $linuxBinary = Join-Path $dist 'musicserver'
    & $go build -o $linuxBinary ./cmd/musicserver
    if ($LASTEXITCODE -ne 0) { throw 'Linux release build failed' }
    & tar -czf $linuxArchive -C $dist musicserver -C $PSScriptRoot README.md deploy/musicserver.service deploy/musicserver.env.example
    if ($LASTEXITCODE -ne 0) { throw 'Linux release archive failed' }

    Get-FileHash -Algorithm SHA256 -LiteralPath $windowsArchive,$linuxArchive | Select-Object Path,Hash
} finally {
    Pop-Location
    foreach ($name in $original.Keys) { [Environment]::SetEnvironmentVariable($name,$original[$name],'Process') }
}
