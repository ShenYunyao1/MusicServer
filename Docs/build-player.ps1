param([ValidateSet('Debug','Release')][string]$Configuration='Debug')
$ErrorActionPreference='Stop'
$mfcRoot='C:\Program Files\Microsoft Visual Studio\2022\Professional\VC\Tools\MSVC\14.44.35207\atlmfc'
$msbuild='D:\Program\Microsoft Visual Studio\2022\BuildTools\MSBuild\Current\Bin\MSBuild.exe'
Push-Location (Join-Path $PSScriptRoot '..\MusicPlayer2')
try {
    # Rebuild cleans the previous project outputs before compiling and linking again.
    & $msbuild MusicPlayer2.sln /t:Rebuild "/p:Configuration=$Configuration" /p:Platform=x64 "/p:VC_ATLMFC_IncludePath=$mfcRoot\include" "/p:VC_LibraryPath_ATL_x64=$mfcRoot\lib\x64" "/p:MFC_KeyFile=$mfcRoot\lib\x64\mfcs140.lib" /m:2 /nologo /v:minimal
    if ($LASTEXITCODE -ne 0) { throw 'MusicPlayer2 build failed' }
} finally { Pop-Location }
