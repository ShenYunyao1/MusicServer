param(
    [string]$GoRoot = '',
    [ValidateSet('windows', 'linux')][string]$Target = 'windows'
)
$ErrorActionPreference = 'Stop'
trap { Write-Host "BUILD FAILED: $($_.Exception.Message)" -ForegroundColor Red; exit 1 }
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
foreach ($name in @('GOROOT','GOTOOLCHAIN','GOCACHE','GOPATH','GOENV','GOOS','GOARCH','CGO_ENABLED')) { $original[$name] = [Environment]::GetEnvironmentVariable($name,'Process') }
Push-Location $PSScriptRoot
try {
	$buildDirectory = Join-Path $PSScriptRoot 'build'
	$binaryName = if ($Target -eq 'windows') { 'musicserver.exe' } else { 'musicserver' }
	$outputPath = Join-Path $buildDirectory $binaryName
	if ($Target -eq 'windows') {
		$running = Get-Process -Name musicserver -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $outputPath }
		if ($running) { throw 'Stop build\musicserver.exe before building. Runtime data will be retained.' }
	}
    $env:GOROOT = $GoRoot
    $env:GOTOOLCHAIN = 'local'
    $env:GOENV = 'off'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    $runtimeParent = Split-Path $GoRoot -Parent
    if (!$env:GOCACHE -and (Test-Path (Join-Path $runtimeParent 'GoBuildCache26'))) { $env:GOCACHE = Join-Path $runtimeParent 'GoBuildCache26' }
    if (!$env:GOPATH -and (Test-Path (Join-Path $runtimeParent 'GoPath26'))) { $env:GOPATH = Join-Path $runtimeParent 'GoPath26' }
    Write-Host '[1/3] Running tests...' -ForegroundColor Cyan
    & $go test ./... -count=1 -timeout 120s
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    Write-Host '[2/3] Checking code...' -ForegroundColor Cyan
    & $go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
    New-Item -ItemType Directory -Path $buildDirectory -Force | Out-Null
    $env:GOOS = $Target
    Write-Host "[3/3] Building $Target/amd64 -> build\$binaryName..." -ForegroundColor Cyan
    & $go build -trimpath -buildvcs=auto -o $outputPath ./cmd/musicserver
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
    Write-Host ''
    Write-Host 'BUILD SUCCEEDED' -ForegroundColor Green
    Write-Host "Executable: $outputPath"
    Write-Host 'Runtime data in build\ is retained. First startup asks you to set the admin password.'
    if ($Target -eq 'windows') { Write-Host 'Next: run start.bat.' }
} catch {
    Write-Host "BUILD FAILED: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
} finally {
    Pop-Location
    foreach ($name in $original.Keys) { [Environment]::SetEnvironmentVariable($name,$original[$name],'Process') }
}
