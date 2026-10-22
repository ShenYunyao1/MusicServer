param([string]$GoRoot = '')
$ErrorActionPreference = 'Stop'
if (!$GoRoot) {
    if ($env:GOROOT -and (Test-Path -LiteralPath (Join-Path $env:GOROOT 'bin\go.exe'))) {
        $GoRoot = $env:GOROOT
    } elseif (Test-Path -LiteralPath 'D:\Develop\Runtimes\Go1.26.8Full\bin\go.exe') {
        $GoRoot = 'D:\Develop\Runtimes\Go1.26.8Full'
    } else {
        $installed = Get-Command go.exe -ErrorAction SilentlyContinue
        if (!$installed) { throw 'Go 1.26+ not found. Install Go or run build.bat -GoRoot <Go directory>.' }
        $GoRoot = Split-Path (Split-Path $installed.Source -Parent) -Parent
    }
}
$go = Join-Path $GoRoot 'bin\go.exe'
if (!(Test-Path -LiteralPath $go)) { throw "Go executable not found: $go" }
$original = @{}
foreach ($name in @('GOROOT','GOTOOLCHAIN','GOCACHE','GOPATH','GOENV')) { $original[$name] = [Environment]::GetEnvironmentVariable($name,'Process') }
Push-Location $PSScriptRoot
try {
    $env:GOROOT = $GoRoot
    $env:GOTOOLCHAIN = 'local'
    $env:GOENV = 'off'
    $runtimeParent = Split-Path $GoRoot -Parent
    if (!$env:GOCACHE -and (Test-Path (Join-Path $runtimeParent 'GoBuildCache26'))) { $env:GOCACHE = Join-Path $runtimeParent 'GoBuildCache26' }
    if (!$env:GOPATH -and (Test-Path (Join-Path $runtimeParent 'GoPath26'))) { $env:GOPATH = Join-Path $runtimeParent 'GoPath26' }
    Write-Host '[1/3] Running tests...' -ForegroundColor Cyan
    & $go test ./... -count=1 -timeout 120s
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    Write-Host '[2/3] Checking code...' -ForegroundColor Cyan
    & $go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
    Write-Host '[3/3] Building musicserver.exe...' -ForegroundColor Cyan
    & $go build -o musicserver.exe ./cmd/musicserver
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
    Write-Host ''
    Write-Host 'BUILD SUCCEEDED' -ForegroundColor Green
    Write-Host "Executable: $(Join-Path $PSScriptRoot 'musicserver.exe')"
    Write-Host 'Next: run start.bat. First startup asks you to set the admin password.'
} catch {
    Write-Host "BUILD FAILED: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
} finally {
    Pop-Location
    foreach ($name in $original.Keys) { [Environment]::SetEnvironmentVariable($name,$original[$name],'Process') }
}
