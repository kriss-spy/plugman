[CmdletBinding()]
param(
    [Parameter()]
    [string] $Version = "@PLUGMAN_VERSION@",

    [Parameter()]
    [string] $InstallDir = "",

    [Parameter()]
    [switch] $NoModifyPath
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

$repository = "kriss-spy/plugman"
$userAgent = "plugman-installer"
$originalProgressPreference = $ProgressPreference

function Get-PlugmanArchitecture {
    $architecture = ""

    try {
        $architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    }
    catch {
        # RuntimeInformation is not available on every Windows PowerShell 5.1 host.
    }

    if ([string]::IsNullOrWhiteSpace($architecture)) {
        $architecture = $env:PROCESSOR_ARCHITEW6432
    }
    if ([string]::IsNullOrWhiteSpace($architecture)) {
        $architecture = $env:PROCESSOR_ARCHITECTURE
    }

    switch ($architecture.ToUpperInvariant()) {
        { $_ -in @("AMD64", "X64") } { return "amd64" }
        { $_ -in @("ARM64", "ARM64EC") } { return "arm64" }
        default { throw "Unsupported Windows architecture '$architecture'. Plugman supports amd64 and arm64." }
    }
}

function Resolve-PlugmanVersion {
    param([string] $RequestedVersion)

    if ($RequestedVersion.StartsWith("@")) {
        $RequestedVersion = "latest"
    }
    if ($RequestedVersion -ne "latest") {
        $normalized = $RequestedVersion
        if (-not $normalized.StartsWith("v")) {
            $normalized = "v$normalized"
        }
        if ($normalized -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$') {
            throw "Invalid version '$RequestedVersion'. Use 'latest', 'v1.2.3', or '1.2.3'."
        }
        return $normalized
    }

    $latestUrl = "https://github.com/$repository/releases/latest"
    try {
        $response = Invoke-WebRequest -Uri $latestUrl -Method Head -MaximumRedirection 10 -Headers @{ "User-Agent" = $userAgent } -UseBasicParsing
    }
    catch {
        throw "Could not resolve the latest Plugman release: $($_.Exception.Message)"
    }

    $resolvedUrl = ""
    $responseUriProperty = $response.BaseResponse.PSObject.Properties["ResponseUri"]
    $requestMessageProperty = $response.BaseResponse.PSObject.Properties["RequestMessage"]
    if ($null -ne $responseUriProperty -and $null -ne $responseUriProperty.Value) {
        # Windows PowerShell 5.1 exposes the final URI as ResponseUri.
        $resolvedUrl = $responseUriProperty.Value.AbsoluteUri
    }
    elseif ($null -ne $requestMessageProperty -and $null -ne $requestMessageProperty.Value) {
        # PowerShell 7 exposes it through HttpResponseMessage.RequestMessage.
        $resolvedUrl = $requestMessageProperty.Value.RequestUri.AbsoluteUri
    }

    if ($resolvedUrl -notmatch '/tag/(?<version>v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)') {
        throw "GitHub returned an unexpected latest-release URL: '$resolvedUrl'."
    }
    return $Matches.version
}

function Get-ExpectedChecksum {
    param(
        [string] $ChecksumPath,
        [string] $ArtifactName
    )

    $matchingHashes = @()
    foreach ($line in Get-Content -LiteralPath $ChecksumPath) {
        if ($line -match '^(?<hash>[0-9A-Fa-f]{64})\s+\*?(?<name>.+)$' -and $Matches.name -eq $ArtifactName) {
            $matchingHashes += $Matches.hash.ToLowerInvariant()
        }
    }
    if ($matchingHashes.Count -ne 1) {
        throw "checksums.txt does not contain exactly one checksum for '$ArtifactName'."
    }
    return $matchingHashes[0]
}

function Save-ReleaseFile {
    param(
        [string] $Uri,
        [string] $Destination
    )

    for ($attempt = 1; $attempt -le 3; $attempt++) {
        try {
            Invoke-WebRequest -Uri $Uri -OutFile $Destination -Headers @{ "User-Agent" = $userAgent } -UseBasicParsing
            return
        }
        catch {
            if ($attempt -eq 3) {
                throw
            }
            Start-Sleep -Seconds $attempt
        }
    }
}

function Add-UserPathEntry {
    param([string] $Directory)

    $fullDirectory = [System.IO.Path]::GetFullPath($Directory).TrimEnd('\')
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $entries = @()
    if (-not [string]::IsNullOrWhiteSpace($userPath)) {
        $entries = $userPath.Split(';') | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
    }

    $alreadyPresent = $false
    foreach ($entry in $entries) {
        $candidate = $entry.Trim().Trim('"')
        try {
            $candidate = [System.IO.Path]::GetFullPath($candidate).TrimEnd('\')
        }
        catch {
            # Preserve unusual PATH entries, but do not treat them as this directory.
            continue
        }
        if ([string]::Equals($candidate, $fullDirectory, [System.StringComparison]::OrdinalIgnoreCase)) {
            $alreadyPresent = $true
            break
        }
    }

    if (-not $alreadyPresent) {
        $newUserPath = if ([string]::IsNullOrWhiteSpace($userPath)) {
            $fullDirectory
        }
        else {
            "$($userPath.TrimEnd(';'));$fullDirectory"
        }
        [Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
    }

    $processEntries = $env:Path.Split(';')
    if (-not ($processEntries | Where-Object { $_.Trim().TrimEnd('\') -ieq $fullDirectory })) {
        $env:Path = "$fullDirectory;$env:Path"
    }

    return -not $alreadyPresent
}

function Assert-NotInsideVault {
    param([string] $Directory)

    $current = [System.IO.DirectoryInfo]::new($Directory)
    while ($null -ne $current) {
        if ($current.Exists -and ($current.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
            throw "Refusing installer path containing link or junction '$($current.FullName)'."
        }
        if ([System.IO.Directory]::Exists((Join-Path $current.FullName ".obsidian"))) {
            throw "Refusing to install inside Obsidian Vault '$($current.FullName)'."
        }
        $current = $current.Parent
    }
}

try {
    if ($env:OS -ne "Windows_NT") {
        throw "This installer supports Windows only."
    }

    # Invoke-WebRequest progress rendering is disproportionately slow in Windows PowerShell 5.1.
    $ProgressPreference = "SilentlyContinue"
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

    if ([string]::IsNullOrWhiteSpace($InstallDir)) {
        $localAppData = [Environment]::GetFolderPath("LocalApplicationData")
        if ([string]::IsNullOrWhiteSpace($localAppData)) {
            throw "Windows did not provide a LocalAppData directory. Pass -InstallDir explicitly."
        }
        $InstallDir = Join-Path $localAppData "Programs\plugman\bin"
    }
    $InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
    Assert-NotInsideVault $InstallDir

    $resolvedVersion = Resolve-PlugmanVersion $Version
    $architecture = Get-PlugmanArchitecture
    $artifactName = "plugman_${resolvedVersion}_windows_${architecture}.exe"
    $releaseBaseUrl = "https://github.com/$repository/releases/download/$resolvedVersion"
    $temporaryDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("plugman-install-" + [Guid]::NewGuid().ToString("N"))

    New-Item -ItemType Directory -Path $temporaryDirectory | Out-Null
    try {
        $artifactPath = Join-Path $temporaryDirectory $artifactName
        $checksumPath = Join-Path $temporaryDirectory "checksums.txt"

        Write-Host "Downloading Plugman $resolvedVersion for Windows $architecture..."
        try {
            Save-ReleaseFile "$releaseBaseUrl/$artifactName" $artifactPath
            Save-ReleaseFile "$releaseBaseUrl/checksums.txt" $checksumPath
        }
        catch {
            throw "Could not download Plugman ${resolvedVersion}: $($_.Exception.Message)"
        }

        $expectedHash = Get-ExpectedChecksum $checksumPath $artifactName
        $actualHash = (Get-FileHash -LiteralPath $artifactPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash) {
            throw "Checksum verification failed for '$artifactName' (expected $expectedHash, got $actualHash)."
        }

        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
        Assert-NotInsideVault $InstallDir
        $destination = Join-Path $InstallDir "plugman.exe"
        $stagedDestination = Join-Path $InstallDir (".plugman-" + [Guid]::NewGuid().ToString("N") + ".exe")
        $backupDestination = Join-Path $InstallDir (".plugman-backup-" + [Guid]::NewGuid().ToString("N") + ".exe")

        try {
            Copy-Item -LiteralPath $artifactPath -Destination $stagedDestination
            if (Test-Path -LiteralPath $destination) {
                [System.IO.File]::Replace($stagedDestination, $destination, $backupDestination, $true)
                Remove-Item -LiteralPath $backupDestination -Force -ErrorAction SilentlyContinue
            }
            else {
                [System.IO.File]::Move($stagedDestination, $destination)
            }
        }
        catch {
            throw "Could not install '$destination'. Close any running Plugman process and try again: $($_.Exception.Message)"
        }
        finally {
            Remove-Item -LiteralPath $stagedDestination -Force -ErrorAction SilentlyContinue
        }

        $skipPathUpdate = $NoModifyPath -or $env:PLUGMAN_NO_MODIFY_PATH -eq "1"
        $pathChanged = $false
        if (-not $skipPathUpdate) {
            try {
                $pathChanged = Add-UserPathEntry $InstallDir
            }
            catch {
                $skipPathUpdate = $true
                Write-Warning "Plugman was installed, but PATH could not be updated: $($_.Exception.Message)"
            }
        }
        Write-Host "Installed Plugman $resolvedVersion to $destination"
        if ($skipPathUpdate) {
            Write-Host "PATH was not changed. Run $destination directly or add $InstallDir to PATH."
        }
        elseif ($pathChanged) {
            Write-Host "Added $InstallDir to your user PATH. Open a new terminal, then run: plugman --version"
        }
        else {
            Write-Host "Run: plugman --version"
        }
    }
    finally {
        Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force -ErrorAction SilentlyContinue
    }
}
catch {
    Write-Error "Plugman installation failed: $($_.Exception.Message)"
    exit 1
}
finally {
    $ProgressPreference = $originalProgressPreference
}
