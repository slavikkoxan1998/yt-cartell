@echo off
rem Builds YT-Cartell-Studio.exe: no cgo, no console window, own icon.
rem
rem   build.bat            the version written in internal\version\version.go
rem   build.bat 2.0.1      another version (shown in Settings, compared with
rem                        the GitHub releases by the updater)
rem
rem Optional environment: UPDATE_OWNER / UPDATE_REPO point the updater at
rem another GitHub repository (default gitkalenyuk/yt-cartell-studio).
rem Releases are published with release.ps1 (it builds into a temp folder).
rem
rem The icon + version info live in rsrc_windows_<arch>.syso (linked by
rem "go build" automatically). Delete that file to regenerate it from
rem internal\assets\icon256.png via go-winres (needs internet once).
setlocal
cd /d "%~dp0"
set CGO_ENABLED=0

where go >nul 2>nul
if errorlevel 1 (
  echo Go not found. Install Go 1.27+ from https://go.dev/dl/ and try again.
  exit /b 1
)

for /f %%a in ('go env GOARCH') do set ARCH=%%a

set VERSION=%~1
if "%VERSION%"=="" for /f "usebackq delims=" %%v in (`powershell -NoProfile -ExecutionPolicy Bypass -Command "$m = Select-String -LiteralPath 'internal\version\version.go' -Pattern '^\s*Version\s*=\s*\x22([^\x22]+)\x22' | Select-Object -First 1; if ($m) { $m.Matches[0].Groups[1].Value }"`) do set VERSION=%%v
if "%VERSION%"=="" set VERSION=0.0.0
for /f "usebackq delims=" %%d in (`powershell -NoProfile -Command "Get-Date -Format yyyy-MM-dd"`) do set BUILDDATE=%%d
set COMMIT=
where git >nul 2>nul
if not errorlevel 1 for /f "delims=" %%c in ('git rev-parse --short HEAD 2^>nul') do set COMMIT=%%c

if not exist rsrc_windows_%ARCH%.syso (
  echo Generating the exe icon...
  go run github.com/tc-hib/go-winres@v0.3.3 simply --icon internal\assets\icon256.png --arch %ARCH% --manifest none --product-name "YT Cartell Studio" --file-description "YT Cartell Studio" --original-filename YT-Cartell-Studio.exe --product-version %VERSION% --file-version %VERSION%
  if errorlevel 1 echo Icon skipped: go-winres unavailable. The exe will use the default icon.
)

set LDFLAGS=-s -w -H windowsgui -X ytcartell/internal/version.Version=%VERSION%
if defined BUILDDATE set LDFLAGS=%LDFLAGS% -X ytcartell/internal/version.BuildDate=%BUILDDATE%
if defined COMMIT set LDFLAGS=%LDFLAGS% -X ytcartell/internal/version.Commit=%COMMIT%
if defined UPDATE_OWNER set LDFLAGS=%LDFLAGS% -X ytcartell/internal/update.Owner=%UPDATE_OWNER%
if defined UPDATE_REPO set LDFLAGS=%LDFLAGS% -X ytcartell/internal/update.Repo=%UPDATE_REPO%

echo Building YT Cartell Studio %VERSION%...
go build -trimpath -ldflags "%LDFLAGS%" -o YT-Cartell-Studio.exe .
if errorlevel 1 (
  echo.
  echo Build failed. If the studio is running, close it and try again.
  exit /b 1
)
for %%f in (YT-Cartell-Studio.exe) do echo Built: %%~ff (%%~zf bytes, version %VERSION%)
