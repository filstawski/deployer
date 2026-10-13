# Tests installing and upgrading the CLI on Windows the way a user does:
# install.ps1 through irm | iex in Windows PowerShell, deployer upgrade
# replacing the running deployer.exe, and install.ps1 again in PowerShell 7.
#
# Site is a folder laid out like GitHub's release downloads, with releases
# v0.0.1 (built by scripts/release.sh) and v0.0.2, the latest. CI builds it;
# see .github/workflows/ci.yml. This changes the current user's installation.
param([Parameter(Mandatory = $true)] [string]$Site)
$ErrorActionPreference = 'Stop'

function Check([bool]$ok, [string]$what) {
    if (-not $ok) { throw "FAILED: $what" }
    Write-Host "ok: $what"
}

$url = 'http://127.0.0.1:8765'
$server = Start-Process python -ArgumentList '-m', 'http.server', '8765', '--bind', '127.0.0.1', '--directory', $Site -PassThru -NoNewWindow
try {
    for ($i = 0; ; $i++) {
        try {
            Invoke-WebRequest "$url/latest/download/VERSION" -UseBasicParsing | Out-Null
            break
        } catch {
            if ($i -ge 50) { throw }
            Start-Sleep -Milliseconds 200
        }
    }
    $env:DEPLOYER_RELEASE_URL = $url
    $dir = Join-Path $env:LOCALAPPDATA 'Programs\deployer'
    $exe = Join-Path $dir 'deployer.exe'
    $install = "irm $url/download/v0.0.1/install.ps1 | iex"

    powershell -NoProfile -Command $install
    Check ($LASTEXITCODE -eq 0) 'install.ps1 runs in Windows PowerShell'
    Check ((& $exe version) -eq 'deployer v0.0.1') 'it installs the release it belongs to'
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User') -split ';'
    Check ($userPath -contains $dir) 'it adds the folder to the user PATH'

    & $exe upgrade
    Check ($LASTEXITCODE -eq 0) 'deployer upgrade replaces the running deployer.exe'
    Check ((& $exe version) -eq 'deployer v0.0.2') 'deployer upgrade installs the latest release'
    Check (-not (Test-Path "$exe.old")) 'the next run removes the replaced program'

    pwsh -NoProfile -Command $install
    Check ($LASTEXITCODE -eq 0) 'install.ps1 runs in PowerShell 7'
    Check ((& $exe version) -eq 'deployer v0.0.1') 'running install.ps1 again replaces deployer.exe'
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User') -split ';'
    Check (@($userPath | Where-Object { $_ -eq $dir }).Count -eq 1) 'it adds the folder to the PATH only once'
} finally {
    Stop-Process $server -ErrorAction SilentlyContinue
}
