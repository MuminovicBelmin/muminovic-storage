@echo off
setlocal

if not exist "%~dp0bin\muminovic-cloud-update.exe" (
  echo Build the updater first: go run ./cmd/build
  exit /b 1
)

if "%MUMINOVIC_CLOUD_HOST%"=="" (
  echo Set MUMINOVIC_CLOUD_HOST to the server DNS name or IP address.
  exit /b 1
)

if "%MUMINOVIC_CLOUD_COMPOSE_DIR%"=="" (
  echo Set MUMINOVIC_CLOUD_COMPOSE_DIR to the remote deployment directory.
  exit /b 1
)

set "REMOTE_USER=root"
if not "%MUMINOVIC_CLOUD_USER%"=="" set "REMOTE_USER=%MUMINOVIC_CLOUD_USER%"
set "REMOTE_PORT=22"
if not "%MUMINOVIC_CLOUD_PORT%"=="" set "REMOTE_PORT=%MUMINOVIC_CLOUD_PORT%"

"%~dp0bin\muminovic-cloud-update.exe" --config-dir "%~dp0." --helper-dir "%~dp0bin" --remote-user "%REMOTE_USER%" --remote-host "%MUMINOVIC_CLOUD_HOST%" --remote-port "%REMOTE_PORT%" --compose-dir "%MUMINOVIC_CLOUD_COMPOSE_DIR%" %*
exit /b %ERRORLEVEL%
