@echo off
setlocal
cd /d "%~dp0"
if not exist "gradlew.bat" (
  echo The supplied Gradle wrapper is missing. Extract the complete v0.6.2 project again.
  exit /b 1
)
if not exist "gradle\wrapper\gradle-wrapper.jar" (
  echo The supplied Gradle wrapper JAR is missing. Extract the complete project again.
  exit /b 1
)
call gradlew.bat --version
exit /b %errorlevel%
