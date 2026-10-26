# Build the Windows desktop executable

Uses Go 1.23+ and **no third-party Go dependencies**.

```powershell
cd windows
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go test ./...
go build -buildvcs=false -trimpath -ldflags "-s -w" -o ..\bin\OneSpace-Windows-Hub-Preview.exe .\cmd\onespace
```

The executable embeds its own HTML/CSS/JavaScript interface and opens it in your default Windows browser. The app is not a Windows tray icon/widget yet. Do not launch multiple OneSpace instances simultaneously — they use local ports 47600 and 47601.
