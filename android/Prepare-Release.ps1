param([int]$PublishedVersionCode = -1)
$ErrorActionPreference = 'Stop'
# Read the current PC publisher without printing or persisting its local key.
if ($PublishedVersionCode -lt 0) {
    try {
        $page = (Invoke-WebRequest -UseBasicParsing 'http://127.0.0.1:47600/').Content
        $match = [regex]::Match($page, "const KEY='([a-f0-9]{48})'")
        if (-not $match.Success) { throw 'Could not identify the running OneSpace dashboard' }
        $manifest = Invoke-RestMethod 'http://127.0.0.1:47600/local/v1/update/manifest' -Headers @{'X-Local-Key' = $match.Groups[1].Value}
        $PublishedVersionCode = [int]$manifest.versionCode
    } catch {
        throw 'Start Windows OneSpace with its existing data, or supply -PublishedVersionCode N using the code shown in Windows Updates. Use 9 only when code 9 is your highest existing release.'
    }
}
if ($PublishedVersionCode -lt 0 -or $PublishedVersionCode -ge 2100000000) { throw 'Invalid published code' }
$path = Join-Path $PSScriptRoot 'version.properties'
$text = [IO.File]::ReadAllText($path)
$old = [regex]::Match($text, '(?m)^publishedVersionCode=(\d+)').Groups[1].Value
$floor = [Math]::Max([int]$old, $PublishedVersionCode)
$text = [regex]::Replace($text, '(?m)^publishedVersionCode=\d+', "publishedVersionCode=$floor")
[IO.File]::WriteAllText($path, $text, [Text.Encoding]::ASCII)
Write-Host "Known published version floor: $floor. The next APK build reserves a strictly higher local code automatically."
