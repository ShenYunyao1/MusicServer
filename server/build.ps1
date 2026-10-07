param([string]$GoRoot = 'D:\Develop\Runtimes\Go1.26.8Full')
$ErrorActionPreference = 'Stop'
$go = Join-Path $GoRoot 'bin\go.exe'
if (!(Test-Path -LiteralPath $go)) { throw "Go executable not found: $go" }
$original = @{}
foreach ($name in @('GOROOT','GOTOOLCHAIN','GOCACHE','GOPATH','GOENV')) { $original[$name] = [Environment]::GetEnvironmentVariable($name,'Process') }
Push-Location $PSScriptRoot
try {
    $env:GOROOT = $GoRoot
    $env:GOTOOLCHAIN = 'local'
    $env:GOENV = 'off'
    $env:GOCACHE = 'D:\Develop\Runtimes\GoBuildCache26'
    $env:GOPATH = 'D:\Develop\Runtimes\GoPath26'
    & $go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    & $go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
    & $go build -o musicserver.exe ./cmd/musicserver
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
} finally {
    Pop-Location
    foreach ($name in $original.Keys) { [Environment]::SetEnvironmentVariable($name,$original[$name],'Process') }
}
