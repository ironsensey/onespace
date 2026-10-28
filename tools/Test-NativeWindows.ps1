param([Parameter(Mandatory=$true)][string]$Executable)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public class Win32 {
    [DllImport("user32.dll", CharSet = CharSet.Auto)]
    public static extern IntPtr SendMessage(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);
    public const uint BM_CLICK = 0x00F5;
    public const uint WM_SETTEXT = 0x000C;
}
"@
$profileRoot = Join-Path ([IO.Path]::GetTempPath()) ('onespace-native-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $profileRoot | Out-Null
$process = $null
try {
    $process = Start-Process -FilePath (Resolve-Path $Executable) -ArgumentList @('-data', ('"' + $profileRoot + '"')) -PassThru
    $deadline = [DateTime]::UtcNow.AddSeconds(25)
    $window = $null
    while ([DateTime]::UtcNow -lt $deadline) {
        $process.Refresh()
        if ($process.HasExited) { throw 'Native engine exited before showing its window' }
        if ($process.MainWindowHandle -ne [IntPtr]::Zero) {
            $window = [System.Windows.Automation.AutomationElement]::FromHandle($process.MainWindowHandle)
            break
        }
        Start-Sleep -Milliseconds 200
    }
    if ($null -eq $window -or $window.Current.Name -ne 'OneSpace') { throw 'Native OneSpace window not found' }
    $notes = $window.FindFirst([System.Windows.Automation.TreeScope]::Descendants, (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty, 'Notes')))
    if ($null -eq $notes) { throw 'Native Notes navigation missing' }
    [Win32]::SendMessage($notes.Current.NativeWindowHandle, [Win32]::BM_CLICK, [IntPtr]::Zero, [IntPtr]::Zero)
    Start-Sleep -Milliseconds 800
    foreach ($item in @(@('201', 'Native test note'), @('202', 'Native test body'))) {
        $field = $window.FindFirst([System.Windows.Automation.TreeScope]::Descendants, (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::AutomationIdProperty, $item[0])))
        if ($null -eq $field) { throw ('Missing native field ' + $item[0]) }
        $textPtr = [System.Runtime.InteropServices.Marshal]::StringToHGlobalUni($item[1])
        [Win32]::SendMessage($field.Current.NativeWindowHandle, [Win32]::WM_SETTEXT, [IntPtr]::Zero, $textPtr)
        [System.Runtime.InteropServices.Marshal]::FreeHGlobal($textPtr)
    }
    $save = $window.FindFirst([System.Windows.Automation.TreeScope]::Descendants, (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty, 'Save note')))
    [Win32]::SendMessage($save.Current.NativeWindowHandle, [Win32]::BM_CLICK, [IntPtr]::Zero, [IntPtr]::Zero)
    Start-Sleep -Milliseconds 800
    $saved = Get-Content (Join-Path $profileRoot 'state.json') -Raw | ConvertFrom-Json
    if (-not ($saved.notes | Where-Object { $_.title -eq 'Native test note' -and $_.body -eq 'Native test body' })) { throw 'Native note did not persist' }
    $process.CloseMainWindow() | Out-Null
    if (-not $process.WaitForExit(10000)) { throw 'Closing native window did not stop engine' }
    Write-Output 'Native window, navigation, note persistence and shutdown passed.'
} finally {
    if ($null -ne $process -and -not $process.HasExited) { $process.Kill(); $process.WaitForExit() }
    Remove-Item $profileRoot -Recurse -Force -ErrorAction SilentlyContinue
}
