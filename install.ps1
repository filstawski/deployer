# Deployer CLI installer for Windows. https://github.com/filstawski/deployer
#
#   In PowerShell:
#     irm https://github.com/filstawski/deployer/releases/latest/download/install.ps1 | iex
#
# It installs deployer.exe into %LOCALAPPDATA%\Programs\deployer and adds that
# folder to your PATH. Running it again upgrades. Optional settings, as
# environment variables:
#
#   DEPLOYER_VERSION      release to install, such as v1.2.3 (default: this
#                         script's release, or the latest)
#   DEPLOYER_INSTALL_DIR  folder to install into
#
# The server runs on Linux; install it with install.sh.

# A script block keeps these settings from changing the caller's session.
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # The progress bar slows downloads down a lot.
    # Windows PowerShell 5.1 may not offer TLS 1.2, which GitHub requires, by default.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $defaultVersion = 'latest'
    $repository = if ($env:DEPLOYER_REPOSITORY) { $env:DEPLOYER_REPOSITORY } else { 'filstawski/deployer' }
    $releases = if ($env:DEPLOYER_RELEASE_URL) { $env:DEPLOYER_RELEASE_URL.TrimEnd('/') } else { "https://github.com/$repository/releases" }
    $version = if ($env:DEPLOYER_VERSION) { $env:DEPLOYER_VERSION } else { $defaultVersion }
    $dir = if ($env:DEPLOYER_INSTALL_DIR) { $env:DEPLOYER_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\deployer' }

    # 32-bit PowerShell on 64-bit Windows reports the real processor here.
    $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    $arch = switch ($cpu) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { throw "Deployer is not built for $cpu processors" }
    }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('deployer-install-' + [Guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        function Get-ReleaseFile([string]$url, [string]$path) {
            try {
                Invoke-WebRequest -Uri $url -OutFile $path -UseBasicParsing
            } catch {
                throw "Could not download ${url}: $($_.Exception.Message)"
            }
        }

        if ($version -eq 'latest') {
            Get-ReleaseFile "$releases/latest/download/VERSION" "$tmp\VERSION"
            $version = (Get-Content -Raw "$tmp\VERSION").Trim()
        }
        if ($version -notmatch '^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') {
            throw "Invalid version: $version (expected something like v1.2.3)"
        }
        $base = "$releases/download/$version"
        $asset = "deployer_${version}_windows_$arch.exe"
        Get-ReleaseFile "$base/SHA256SUMS" "$tmp\SHA256SUMS"
        Get-ReleaseFile "$base/$asset" "$tmp\deployer.exe"

        $expected = $null
        foreach ($line in Get-Content "$tmp\SHA256SUMS") {
            $fields = -split $line
            if ($fields.Count -eq 2 -and $fields[1].TrimStart('*') -eq $asset) {
                $expected = $fields[0]
            }
        }
        if (-not $expected) {
            throw "Release $version has no checksum for $asset"
        }
        if ((Get-FileHash -Algorithm SHA256 "$tmp\deployer.exe").Hash -ne $expected) {
            throw "Checksum mismatch for $asset; nothing was installed"
        }

        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        $dest = Join-Path $dir 'deployer.exe'
        # Windows can't replace a running program, but can rename it.
        Remove-Item "$dest.old" -Force -ErrorAction SilentlyContinue
        if (Test-Path $dest) {
            Move-Item $dest "$dest.old" -Force
        }
        try {
            Move-Item "$tmp\deployer.exe" $dest
        } catch {
            if (Test-Path "$dest.old") { Move-Item "$dest.old" $dest -Force }
            throw
        }
        Remove-Item "$dest.old" -Force -ErrorAction SilentlyContinue # deployer removes it later if it's in use
        Write-Host "Installed deployer $version to $dest"
    } finally {
        Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    # Add the folder to the user's PATH, keeping entries such as %USERPROFILE%
    # unexpanded.
    $environment = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    try {
        $userPath = $environment.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
        $entries = @($userPath -split ';' | Where-Object { $_ })
        if ($entries -notcontains $dir -and $entries -notcontains "$dir\") {
            $environment.SetValue('Path', (($entries + $dir) -join ';'), 'ExpandString')
            # Setting a variable through .NET tells Explorer that the PATH
            # changed, so new terminals see it.
            [Environment]::SetEnvironmentVariable('DEPLOYER_INSTALLING', '1', 'User')
            [Environment]::SetEnvironmentVariable('DEPLOYER_INSTALLING', $null, 'User')
            Write-Host "Added $dir to your PATH; terminals you open from now on find deployer."
        }
    } finally {
        $environment.Close()
    }
    if (($env:Path -split ';') -notcontains $dir) {
        $env:Path = "$env:Path;$dir"
    }

    Write-Host ''
    Write-Host 'Next: deployer login https://api.YOUR-DOMAIN'
}
