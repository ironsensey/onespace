@echo off
setlocal
cd /d "%~dp0"
if not exist "gradlew.bat" (
  echo Extract the complete project including its Gradle wrapper first.
  exit /b 1
)
if not exist "local.properties" (
  if not defined ANDROID_HOME if exist "%LOCALAPPDATA%\Android\Sdk\platforms" set "ANDROID_HOME=%LOCALAPPDATA%\Android\Sdk"
  if not defined ANDROID_HOME (
    echo Open this android folder in Android Studio and install SDK Platform 36 and Build Tools 35.0.0.
    exit /b 1
  )
  powershell -NoProfile -NonInteractive -Command "$sdk=$env:ANDROID_HOME.Replace('\','/'); [IO.File]::WriteAllText('local.properties', 'sdk.dir='+$sdk, [Text.Encoding]::ASCII)"
  if errorlevel 1 exit /b 1
)
call gradlew.bat :app:assembleDebug
if errorlevel 1 exit /b 1
echo APK: %CD%\app\build\outputs\apk\debug\app-debug.apk
echo The actual version code is shown in the Gradle output and increases automatically for each APK build.
echo Debug APKs update existing apps only with the SAME signing certificate. For a release installation use your original release key in Android Studio.
