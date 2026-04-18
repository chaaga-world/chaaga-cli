# install.ps1 — install the chaaga CLI on Windows
#
# Usage (run in PowerShell):
#   irm https://raw.githubusercontent.com/chaaga-world/chaaga-cli/main/install.ps1 | iex
#   # or pin a version:
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/chaaga-world/chaaga-cli/main/install.ps1))) -Version v1.2.3

param(
    [string]$Version = "latest",
    [string]$InstallDir = "$env:LOCALAPPDATA\chaaga\bin"
)

$ErrorActionPreference = "Stop"
$Repo = "chaaga-world/chaaga-cli"
$BinName = "chaaga.exe"

# Resolve 'latest' version
if ($Version -eq "latest") {
    Write-Host "Fetching latest release..."
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
    $Version = $release.tag_name
    if (-not $Version) {
        Write-Error "Could not determine latest version."
        exit 1
    }
}

$Archive = "chaaga_${Version}_windows_amd64.zip"
$Url = "https://github.com/$Repo/releases/download/$Version/$Archive"

# Download to temp directory
$TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $TmpDir | Out-Null

try {
    Write-Host "Downloading chaaga $Version (windows/amd64)..."
    $ArchivePath = Join-Path $TmpDir $Archive
    Invoke-WebRequest -Uri $Url -OutFile $ArchivePath -UseBasicParsing

    # Verify checksum
    $ChecksumUrl = "https://github.com/$Repo/releases/download/$Version/checksums.txt"
    try {
        $ChecksumFile = Join-Path $TmpDir "checksums.txt"
        Invoke-WebRequest -Uri $ChecksumUrl -OutFile $ChecksumFile -UseBasicParsing
        $expected = (Get-Content $ChecksumFile | Where-Object { $_ -match [regex]::Escape($Archive) }) -split '\s+' | Select-Object -First 1
        $actual = (Get-FileHash -Path $ArchivePath -Algorithm SHA256).Hash.ToLower()
        if ($expected -and ($actual -ne $expected)) {
            Write-Error "Checksum mismatch! Expected $expected, got $actual"
            exit 1
        }
        Write-Host "Checksum verified."
    } catch {
        Write-Warning "Could not verify checksum: $_"
    }

    # Extract archive
    Expand-Archive -Path $ArchivePath -DestinationPath $TmpDir -Force

    # Create install directory and copy binary
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir | Out-Null
    }
    $Dest = Join-Path $InstallDir $BinName
    Copy-Item -Path (Join-Path $TmpDir $BinName) -Destination $Dest -Force

    # Add to user PATH if not already present
    $CurrentPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($CurrentPath -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable("Path", "$CurrentPath;$InstallDir", "User")
        Write-Host ""
        Write-Host "Added $InstallDir to your PATH."
        Write-Host "Restart your terminal (or open a new one) before using 'chaaga'."
    }

    Write-Host ""
    Write-Host "chaaga $Version installed to $Dest"

} finally {
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}
