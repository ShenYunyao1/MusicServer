param(
    [ValidateSet('Inspect','Configure','Library','SelectServer','SelectTrack','Play','Refresh','Settings','CloseSettings','Screenshot','Pause')] [string]$Action='Inspect',
    [string]$ServerUrl='http://127.0.0.1:18080',
    [string]$ApiKey='dev-test-key',
    [string]$CacheDirectory='D:\Develop\Runtimes\MusicServerTest\cache'
)
Add-Type -TypeDefinition @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public class MusicDesktopTest {
 public delegate bool EnumCallback(IntPtr hwnd, IntPtr data);
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumCallback callback,IntPtr data);
 [DllImport("user32.dll")] public static extern bool EnumChildWindows(IntPtr hwnd,EnumCallback callback,IntPtr data);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hwnd,out uint pid);
 [DllImport("user32.dll")] public static extern int GetDlgCtrlID(IntPtr hwnd);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr hwnd,StringBuilder text,int max);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern IntPtr SendMessage(IntPtr hwnd,uint message,IntPtr wparam,string text);
 [DllImport("user32.dll",EntryPoint="SendMessageW")] public static extern IntPtr Query(IntPtr hwnd,uint message,IntPtr wparam,IntPtr lparam);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hwnd,uint message,IntPtr wparam,IntPtr lparam);
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hwnd,int command);
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hwnd);
}
'@
$player = Get-Process MusicPlayer2 | Where-Object MainWindowHandle -NE 0 | Select-Object -First 1
if (!$player) { throw 'MusicPlayer2 is not running' }
$script:windows = [System.Collections.Generic.List[object]]::new()
$callback = [MusicDesktopTest+EnumCallback]{param($handle,$data)
    $windowPid = 0
    [void][MusicDesktopTest]::GetWindowThreadProcessId($handle,[ref]$windowPid)
    if ($windowPid -eq $player.Id) {
        $text = [System.Text.StringBuilder]::new(2048)
        [void][MusicDesktopTest]::GetWindowText($handle,$text,2048)
        $script:windows.Add([pscustomobject]@{Handle=$handle;Id=[MusicDesktopTest]::GetDlgCtrlID($handle);Text=$text.ToString()})
    }
    return $true
}
[void][MusicDesktopTest]::EnumWindows($callback,[IntPtr]::Zero)
foreach ($window in @($script:windows.ToArray())) { [void][MusicDesktopTest]::EnumChildWindows($window.Handle,$callback,[IntPtr]::Zero) }
function Find-Control([int]$control) { ($script:windows | Where-Object Id -EQ $control | Select-Object -First 1).Handle }
function Click-Control([int]$control) { [void][MusicDesktopTest]::PostMessage((Find-Control $control),0xF5,[IntPtr]::Zero,[IntPtr]::Zero) }
switch ($Action) {
    Inspect { $script:windows | Where-Object { $_.Id -in @(0,8105,8112,65535) -and $_.Text } | Format-List; if(Find-Control 8107){ 'Items: '+[MusicDesktopTest]::Query((Find-Control 8107),4100,[IntPtr]0,[IntPtr]0); 'Selected: '+[MusicDesktopTest]::Query((Find-Control 8107),4146,[IntPtr]0,[IntPtr]0) } }
    Settings { [void][MusicDesktopTest]::ShowWindow($player.MainWindowHandle,5); [void][MusicDesktopTest]::PostMessage($player.MainWindowHandle,1152,[IntPtr]6,[IntPtr]::Zero) }
    Configure {
        [void][MusicDesktopTest]::SendMessage((Find-Control 8101),12,[IntPtr]::Zero,$ServerUrl)
        [void][MusicDesktopTest]::SendMessage((Find-Control 8102),12,[IntPtr]::Zero,$ApiKey)
        [void][MusicDesktopTest]::SendMessage((Find-Control 8103),12,[IntPtr]::Zero,$CacheDirectory)
        Click-Control 8104
    }
    CloseSettings { $settings=$script:windows | Where-Object Text -EQ '选项设置' | Select-Object -First 1; [void][MusicDesktopTest]::PostMessage($settings.Handle,273,[IntPtr]1,[IntPtr]::Zero) }
    Library { [void][MusicDesktopTest]::ShowWindow($player.MainWindowHandle,5); [void][MusicDesktopTest]::SetForegroundWindow($player.MainWindowHandle); [void][MusicDesktopTest]::PostMessage($player.MainWindowHandle,273,[IntPtr]32793,[IntPtr]::Zero) }
    SelectServer {
        $library=$script:windows | Where-Object Text -EQ '媒体库' | Select-Object -First 1
        [void][MusicDesktopTest]::ShowWindow($library.Handle,5)
        [void][MusicDesktopTest]::SetForegroundWindow($library.Handle)
        $tab=Find-Control 1083
        $count=[MusicDesktopTest]::Query($tab,4868,[IntPtr]0,[IntPtr]0).ToInt32()
        [void][MusicDesktopTest]::Query($tab,4876,[IntPtr]($count-1),[IntPtr]0)
        [void][MusicDesktopTest]::PostMessage($tab,256,[IntPtr]37,[IntPtr]0)
        [void][MusicDesktopTest]::PostMessage($tab,256,[IntPtr]39,[IntPtr]0)
    }
    Play { Click-Control 8110 }
    SelectTrack {
        $list=Find-Control 8107
        $point=[IntPtr](30 + (40 -shl 16))
        [void][MusicDesktopTest]::PostMessage($list,513,[IntPtr]1,$point)
        [void][MusicDesktopTest]::PostMessage($list,514,[IntPtr]0,$point)
        [void][MusicDesktopTest]::PostMessage($list,256,[IntPtr]36,[IntPtr]0)
        [void][MusicDesktopTest]::PostMessage($list,257,[IntPtr]36,[IntPtr]0)
    }
    Refresh { Click-Control 8108 }
    Pause { [void][MusicDesktopTest]::PostMessage($player.MainWindowHandle,273,[IntPtr]32778,[IntPtr]::Zero) }
    Screenshot {
        Add-Type -AssemblyName System.Windows.Forms
        Add-Type -AssemblyName System.Drawing
        $bounds=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds
        $bitmap=[System.Drawing.Bitmap]::new($bounds.Width,$bounds.Height)
        $graphics=[System.Drawing.Graphics]::FromImage($bitmap)
        $graphics.CopyFromScreen($bounds.Location,[System.Drawing.Point]::Empty,$bounds.Size)
        $bitmap.Save((Join-Path $PSScriptRoot 'integration-screen.png'))
        $graphics.Dispose(); $bitmap.Dispose()
    }
}
