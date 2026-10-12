<#
.SYNOPSIS
  Builds the Windows release of YT Cartell Studio and publishes it as the
  GitHub release v<version> (the updater in the app reads it from there).

.DESCRIPTION
  Run from the GO folder in PowerShell (Windows PowerShell 5.1 or 7):

    .\release.ps1 -Notes notes-2.0.0.md
    .\release.ps1 -Notes notes-2.0.1.md -Version 2.0.1 -Title "швидший монтаж"
    .\release.ps1 -Notes notes-2.0.0.md -Draft     # publish later: gh release edit v2.0.0 --draft=false
    .\release.ps1 -Notes notes-2.0.0.md -DryRun    # build + SHA256SUMS.txt only, no GitHub

  Steps: the exe's version resource (rsrc_windows_amd64.syso) is regenerated
  with the version (go-winres; -KeepResource skips it), the exe is built with
  CGO_ENABLED=0 into -OutDir (default %TEMP%\yt-cartell-release-<version>; the
  GO\YT-Cartell-Studio*.exe files are never touched) as
  YT-Cartell-Studio-<version>-windows-x64.exe, SHA256SUMS.txt gets its line
  (the lines of the macOS zips already on the release are kept), then the
  release v<version> in -Repo is created (gh release create) or updated
  (gh release upload --clobber + gh release edit) with the notes file.

  Needs: Go 1.27+, GitHub CLI (gh) signed in with access to the repository
  (gh auth login). The macOS zips are added from a Mac with
  bash build-mac.sh --upload; the app installs nothing without a line in SHA256SUMS.txt.

.PARAMETER Notes
  Release notes (Markdown, UTF-8) — shown on GitHub and in the app's «Оновлення» sheet.
.PARAMETER Version
  X.Y.Z (default: the Version in internal\version\version.go).
.PARAMETER Title
  Text after "YT Cartell Studio vX.Y.Z — " (optional).
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Notes,
    [string]$Version = "",
    [string]$Title = "",
    [string]$Repo = "gitkalenyuk/yt-cartell-studio",
    [string]$OutDir = "",
    [switch]$Draft,
    [switch]$DryRun,
    [switch]$KeepResource
)

$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

function Fail([string]$msg) {
    Write-Host "release: $msg" -ForegroundColor Red
    exit 1
}

function Need([string]$tool, [string]$hint) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { Fail "$tool not found. $hint" }
}

# ---- inputs -------------------------------------------------------------------------------------

Need 'go' 'Install Go 1.27+ from https://go.dev/dl/'
if (-not $DryRun) {
    Need 'gh' 'Install the GitHub CLI (winget install GitHub.cli) and run: gh auth login'
    & gh auth status --hostname github.com *> $null
    if ($LASTEXITCODE -ne 0) { Fail 'gh is not signed in. Run: gh auth login' }
}

if (-not (Test-Path -LiteralPath $Notes -PathType Leaf)) { Fail "notes file not found: $Notes" }
$Notes = (Resolve-Path -LiteralPath $Notes).Path

if ($Version -eq "") {
    $m = Select-String -LiteralPath 'internal\version\version.go' -Pattern '^\s*Version\s*=\s*"([^"]+)"' | Select-Object -First 1
    if (-not $m) { Fail 'cannot read Version from internal\version\version.go; pass -Version' }
    $Version = $m.Matches[0].Groups[1].Value
}
$Version = $Version.TrimStart('v', 'V')
if ($Version -notmatch '^\d+\.\d+\.\d+$') { Fail "version '$Version' is not X.Y.Z (the app ignores pre-releases)" }
$tag = "v$Version"
$fullTitle = "YT Cartell Studio $tag"
if ($Title -ne "") { $fullTitle += " $([char]0x2014) $Title" }

if ($OutDir -eq "") { $OutDir = Join-Path $env:TEMP "yt-cartell-release-$Version" }
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$OutDir = (Resolve-Path -LiteralPath $OutDir).Path

$exeName = "YT-Cartell-Studio-$Version-windows-x64.exe"
$exe = Join-Path $OutDir $exeName
$sums = Join-Path $OutDir 'SHA256SUMS.txt'
$date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-dd')
$commit = ""
if (Get-Command git -ErrorAction SilentlyContinue) {
    $commit = (& git rev-parse --short HEAD 2>$null)
    if ($LASTEXITCODE -ne 0) { $commit = "" }
}

Write-Host "YT Cartell Studio $Version -> $Repo ($(if ($DryRun) { 'dry run' } else { 'publish' }))"
Write-Host "  out: $OutDir"

# ---- build --------------------------------------------------------------------------------------

if (-not $KeepResource) {
    Write-Host "Version resource (go-winres)..."
    & go run github.com/tc-hib/go-winres@v0.3.3 simply --icon internal\assets\icon256.png --arch amd64 --manifest none `
        --product-name "YT Cartell Studio" --file-description "YT Cartell Studio" --original-filename YT-Cartell-Studio.exe `
        --product-version $Version --file-version $Version
    if ($LASTEXITCODE -ne 0) { Write-Warning 'go-winres failed: the exe keeps the existing rsrc_windows_amd64.syso (icon, older version info).' }
}

$ldflags = "-s -w -H windowsgui -X ytcartell/internal/version.Version=$Version -X ytcartell/internal/version.BuildDate=$date"
if ($commit) { $ldflags += " -X ytcartell/internal/version.Commit=$commit" }
# The built-in updater reads the releases of the repository it is published to (forks: -Repo me/studio).
$owner, $name = $Repo.Split('/', 2)
$ldflags += " -X ytcartell/internal/update.Owner=$owner -X ytcartell/internal/update.Repo=$name"

$saved = @{ CGO_ENABLED = $env:CGO_ENABLED; GOOS = $env:GOOS; GOARCH = $env:GOARCH }
try {
    $env:CGO_ENABLED = '0'; $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
    Write-Host "Building $exeName..."
    & go build -trimpath -ldflags $ldflags -o $exe .
    if ($LASTEXITCODE -ne 0) { Fail 'go build failed' }
}
finally {
    $env:CGO_ENABLED = $saved.CGO_ENABLED; $env:GOOS = $saved.GOOS; $env:GOARCH = $saved.GOARCH
}
$size = (Get-Item -LiteralPath $exe).Length
if ($size -lt 5MB) { Fail "the exe is suspiciously small ($size bytes)" }

# ---- SHA256SUMS.txt -----------------------------------------------------------------------------

# Native tools (gh, go) report progress and "not found" on stderr; with
# ErrorActionPreference=Stop Windows PowerShell 5.1 would turn that into a
# terminating error, so exit codes are checked explicitly from here on.
$ErrorActionPreference = 'Continue'

$exists = $false
if (-not $DryRun) {
    & gh release view $tag -R $Repo --json tagName *> $null
    $exists = ($LASTEXITCODE -eq 0)
    if ($exists) {
        # Keep the lines of the assets already published (the macOS zips).
        & gh release download $tag -R $Repo -p 'SHA256SUMS.txt' -D $OutDir --clobber *> $null
    }
}
$lines = @()
if (Test-Path -LiteralPath $sums) {
    $lines = @(Get-Content -LiteralPath $sums | Where-Object { $_ -match '^[0-9a-fA-F]{64} [ *]?(\S.*)$' -and $Matches[1] -ne $exeName })
}
$hash = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash.ToLowerInvariant()
$lines += "$hash  $exeName"
[IO.File]::WriteAllText($sums, (($lines | Sort-Object { ($_ -split '\s+', 2)[1] }) -join "`n") + "`n", (New-Object Text.UTF8Encoding $false))
Write-Host "  $exeName  $([math]::Round($size / 1MB, 1)) MB"
Write-Host "  sha256 $hash"

if ($DryRun) {
    Write-Host "Dry run: nothing published. Files are in $OutDir"
    exit 0
}

# ---- publish ------------------------------------------------------------------------------------

if (-not $exists) {
    $ghArgs = @('release', 'create', $tag, $exe, $sums, '-R', $Repo, '--title', $fullTitle, '--notes-file', $Notes)
    if ($Draft) { $ghArgs += '--draft' } else { $ghArgs += '--latest' }
    Write-Host "Creating release $tag..."
    & gh @ghArgs
    if ($LASTEXITCODE -ne 0) { Fail 'gh release create failed' }
}
else {
    Write-Host "Updating release $tag (assets replaced)..."
    & gh release upload $tag $exe $sums -R $Repo --clobber
    if ($LASTEXITCODE -ne 0) { Fail 'gh release upload failed' }
    & gh release edit $tag -R $Repo --title $fullTitle --notes-file $Notes
    if ($LASTEXITCODE -ne 0) { Fail 'gh release edit failed' }
}

Write-Host ""
Write-Host "Published: https://github.com/$Repo/releases/tag/$tag" -ForegroundColor Green
if ($Draft) { Write-Host "It is a draft: the app sees it after  gh release edit $tag -R $Repo --draft=false" }
Write-Host "macOS assets (on a Mac, from the repo root):  bash build-mac.sh --version $Version --upload"
