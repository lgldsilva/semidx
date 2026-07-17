# semidx installer (Windows PowerShell) — downloads the right release archive
# for your OS/arch from GitHub releases, verifies its SHA-256, and installs
# the binary.
#
# One-liner (latest):
#   irm https://raw.githubusercontent.com/lgldsilva/semidx/main/install.ps1 | iex
#
# With parameters (wrap so args survive iex):
#   iex "& { $(irm https://raw.githubusercontent.com/lgldsilva/semidx/main/install.ps1) } -Version v0.2.0"
#   .\install.ps1                              # install latest for this machine
#   .\install.ps1 -Version v0.2.0              # a specific release
#   .\install.ps1 -NoInstall -Destination .\dl # just fetch the archive
#   .\install.ps1 -All -Destination .\dist     # download EVERY artifact + checksums
#
# Env overrides: SEMIDX_API, SEMIDX_DOWNLOAD_BASE, SEMIDX_BIN_DIR
#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$Destination = "",
    [switch]$All,
    [switch]$NoInstall
)

$ErrorActionPreference = "Stop"

$Api = if ($env:SEMIDX_API) { $env:SEMIDX_API.TrimEnd("/") } else { "https://api.github.com/repos/lgldsilva/semidx" }
$DlBase = if ($env:SEMIDX_DOWNLOAD_BASE) { $env:SEMIDX_DOWNLOAD_BASE.TrimEnd("/") } else { "https://github.com/lgldsilva/semidx/releases/download" }
$BinDir = if ($env:SEMIDX_BIN_DIR) { $env:SEMIDX_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "semidx\bin" }

function Write-InstallError {
    param([string]$Message)
    $host.UI.WriteErrorLine("install: $Message")
    exit 1
}

function Get-SemidxOs {
    # PowerShell Core on non-Windows sets $IsWindows; Windows PowerShell always runs on Windows.
    if ($PSVersionTable.PSEdition -eq "Core" -and -not $IsWindows) {
        Write-InstallError "this installer targets Windows; use install.sh on Unix"
    }
    return "windows"
}

function Get-SemidxArch {
    $arch = $env:PROCESSOR_ARCHITECTURE
    if (-not $arch -and [Environment]::Is64BitOperatingSystem) { $arch = "AMD64" }
    switch -Regex ($arch.ToUpperInvariant()) {
        "^(AMD64|X64)$" { return "amd64" }
        "^(ARM64)$" { return "arm64" }
        "^(X86|I386)$" { Write-InstallError "32-bit Windows is not supported" }
        default { Write-InstallError "unsupported arch: $arch" }
    }
}

function Resolve-LatestVersion {
    try {
        $headers = @{
            "User-Agent" = "semidx-install.ps1"
            "Accept"     = "application/vnd.github+json"
        }
        $release = Invoke-RestMethod -Uri "$Api/releases/latest" -Headers $headers
        if (-not $release.tag_name) {
            Write-InstallError "could not resolve the latest release from $Api (empty tag_name)"
        }
        return [string]$release.tag_name
    }
    catch {
        Write-InstallError "could not resolve the latest release from $Api : $_"
    }
}

function Get-ReleaseAssetUrls {
    param([string]$Tag)
    $headers = @{
        "User-Agent" = "semidx-install.ps1"
        "Accept"     = "application/vnd.github+json"
    }
    try {
        $release = Invoke-RestMethod -Uri "$Api/releases/tags/$Tag" -Headers $headers
    }
    catch {
        Write-InstallError "could not list assets for $Tag from $Api : $_"
    }
    $urls = @()
    foreach ($asset in $release.assets) {
        if ($asset.browser_download_url) {
            $urls += [string]$asset.browser_download_url
        }
    }
    if ($urls.Count -eq 0) {
        Write-InstallError "no downloadable assets found for release $Tag"
    }
    return $urls
}

function Test-Checksum {
    param(
        [string]$ArchivePath,
        [string]$ArchiveName,
        [string]$ChecksumsPath
    )
    if (-not (Test-Path -LiteralPath $ChecksumsPath)) {
        Write-Warning "checksums.txt not found — skipping verification"
        return
    }
    $want = $null
    foreach ($line in Get-Content -LiteralPath $ChecksumsPath) {
        # goreleaser: "<sha256>  <filename>" or "<sha256> *<filename>"
        if ($line -match "^\s*([0-9a-fA-F]{64})\s+\*?(\S+)\s*$") {
            if ($Matches[2] -eq $ArchiveName) {
                $want = $Matches[1].ToLowerInvariant()
                break
            }
        }
    }
    if (-not $want) {
        Write-Warning "no checksum entry for $ArchiveName — skipping verification"
        return
    }
    $got = (Get-FileHash -LiteralPath $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($want -ne $got) {
        Write-InstallError "checksum mismatch for $ArchiveName (want $want, got $got)"
    }
    Write-Host "Checksum OK."
}

function Add-BinDirToUserPath {
    param([string]$Dir)
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not $userPath) { $userPath = "" }
    $parts = $userPath -split ";" | Where-Object { $_ -ne "" }
    $already = $parts | Where-Object { $_.TrimEnd("\") -ieq $Dir.TrimEnd("\") }
    if ($already) {
        return $false
    }
    $newPath = if ($userPath.TrimEnd(";")) { "$($userPath.TrimEnd(";"));$Dir" } else { $Dir }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    # Also update current session so semidx is usable immediately.
    if ($env:Path -notmatch [regex]::Escape($Dir)) {
        $env:Path = "$env:Path;$Dir"
    }
    return $true
}

# --- resolve version ---
if (-not $Version) {
    $Version = Resolve-LatestVersion
}
$VerNoV = $Version -replace "^v", ""

# --- -All: every asset + stop ---
if ($All) {
    $dest = if ($Destination) { $Destination } else { "." }
    if (-not (Test-Path -LiteralPath $dest)) {
        New-Item -ItemType Directory -Path $dest -Force | Out-Null
    }
    Write-Host "Downloading all artifacts for $Version into $dest ..."
    $urls = Get-ReleaseAssetUrls -Tag $Version
    foreach ($url in $urls) {
        $name = Split-Path -Leaf ([Uri]$url).AbsolutePath
        $out = Join-Path $dest $name
        Write-Host "  $url"
        try {
            Invoke-WebRequest -Uri $url -OutFile $out -UseBasicParsing
        }
        catch {
            Write-InstallError "download failed: $url — $_"
        }
    }
    Write-Host "Done."
    exit 0
}

$Os = Get-SemidxOs
$Arch = Get-SemidxArch
$Archive = "semidx_${VerNoV}_${Os}_${Arch}.zip"
$Base = "$DlBase/$Version"
$ArchiveUrl = "$Base/$Archive"
$ChecksumsUrl = "$Base/checksums.txt"

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("semidx-install-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $work -Force | Out-Null
try {
    $archivePath = Join-Path $work $Archive
    Write-Host "Fetching $Archive ($Version) ..."
    try {
        Invoke-WebRequest -Uri $ArchiveUrl -OutFile $archivePath -UseBasicParsing
    }
    catch {
        Write-InstallError "download failed: $ArchiveUrl — $_"
    }

    $checksumsPath = Join-Path $work "checksums.txt"
    $haveChecksums = $false
    try {
        Invoke-WebRequest -Uri $ChecksumsUrl -OutFile $checksumsPath -UseBasicParsing
        $haveChecksums = $true
    }
    catch {
        Write-Warning "checksums.txt not found — skipping verification"
    }
    if ($haveChecksums) {
        Test-Checksum -ArchivePath $archivePath -ArchiveName $Archive -ChecksumsPath $checksumsPath
    }

    # -Destination / -NoInstall: save archive only (mirrors install.sh --dest).
    # Default (neither flag): install semidx.exe into BIN_DIR.
    if ($NoInstall -or $Destination) {
        $dest = if ($Destination) { $Destination } else { "." }
        if (-not (Test-Path -LiteralPath $dest)) {
            New-Item -ItemType Directory -Path $dest -Force | Out-Null
        }
        $out = Join-Path $dest $Archive
        Copy-Item -LiteralPath $archivePath -Destination $out -Force
        Write-Host "Saved $out"
        exit 0
    }

    $extractDir = Join-Path $work "extract"
    New-Item -ItemType Directory -Path $extractDir -Force | Out-Null
    Expand-Archive -Path $archivePath -DestinationPath $extractDir -Force

    $bin = Get-ChildItem -Path $extractDir -Filter "semidx.exe" -Recurse -File -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if (-not $bin) {
        Write-InstallError "binary not found inside $Archive"
    }

    if (-not (Test-Path -LiteralPath $BinDir)) {
        New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
    }
    $target = Join-Path $BinDir "semidx.exe"
    Copy-Item -LiteralPath $bin.FullName -Destination $target -Force

    $pathUpdated = Add-BinDirToUserPath -Dir $BinDir
    Write-Host "Installed semidx $Version to $target"
    if ($pathUpdated) {
        Write-Host "Added $BinDir to your user PATH (new shells will pick it up)."
    }
    else {
        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        if ($userPath -and ($userPath -split ";" | Where-Object { $_.TrimEnd("\") -ieq $BinDir.TrimEnd("\") })) {
            # already on PATH
        }
        else {
            Write-Host "note: add $BinDir to your PATH"
        }
    }
}
finally {
    if (Test-Path -LiteralPath $work) {
        Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
    }
}
