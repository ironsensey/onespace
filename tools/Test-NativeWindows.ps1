param([Parameter(Mandatory=$true)][string]$Executable)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
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
    ([System.Windows.Automation.SelectionItemPattern]$notes.GetCurrentPattern([System.Windows.Automation.SelectionItemPattern]::Pattern)).Select()
    Start-Sleep -Milliseconds 800
    foreach ($item in @(@('201', 'Native test note'), @('202', 'Native test body'))) {
        $field = $window.FindFirst([System.Windows.Automation.TreeScope]::Descendants, (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::AutomationIdProperty, $item[0])))
        if ($null -eq $field) { throw ('Missing native field ' + $item[0]) }
        ([System.Windows.Automation.ValuePattern]$field.GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern)).SetValue($item[1])
    }
    $save = $window.FindFirst([System.Windows.Automation.TreeScope]::Descendants, (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty, 'Save note')))
    ([System.Windows.Automation.InvokePattern]$save.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)).Invoke()
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
