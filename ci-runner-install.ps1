# ci-runner-install.ps1 - install, or remove, the desktop CI runner from one
# Code Goblins release.
#
# The copy published with a release names that release and its publisher
# below, and installs only a cg-ci-runner.exe that matches the release's
# SHA256SUMS and carries a valid signature from that publisher. A copy that
# names no publisher, as this file in the repository does, installs nothing,
# since the service it installs holds a GitHub App's key.
#
# From an elevated Windows PowerShell, after downloading this script from the
# release:
#
#   powershell -ExecutionPolicy AllSigned -File ci-runner-install.ps1 -AppId <id> -InstallationId <id> -Repo <owner/name> -Key <file> -User <account>
#
# -DryRun downloads and checks the program, then shows each step of the
# install without elevation and changes nothing. -Uninstall removes the
# runner, its VM, its service, its key and its program.
param(
    [string]$AppId,
    [string]$InstallationId,
    [string]$Repo,
    [string]$Key,
    [string]$User,
    [switch]$DryRun,
    [switch]$Uninstall
)
$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
$ProgressPreference = "SilentlyContinue"

# The copy of this script published with a release names that release here
# in place of latest, and its publisher below. CODE_GOBLINS_RELEASE_BASE
# points the download at another copy of the release's files, as a test's,
# and the checks apply the same.
$repo = "fpresta0607/code-goblins"
$releaseBase = "https://github.com/$repo/releases/latest/download"
if ($env:CODE_GOBLINS_RELEASE_BASE) {
    $releaseBase = $env:CODE_GOBLINS_RELEASE_BASE.TrimEnd("/")
}
$releasePublisher = ""

$installed = Join-Path $env:ProgramFiles "Code Goblins\ci-runner"
$program = Join-Path $installed "cg-ci-runner.exe"
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
$isElevated = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $DryRun -and -not $isElevated) {
    throw "The runner's install makes a service and a Hyper-V VM, so run this from an elevated Windows PowerShell, or with -DryRun to see each step."
}

if ($Uninstall) {
    if (Test-Path -LiteralPath $program) {
        $arguments = @("uninstall")
        if ($DryRun) { $arguments += "--dry-run" }
        & $program @arguments
        if ($LASTEXITCODE -ne 0) { throw "cg-ci-runner uninstall failed with exit $LASTEXITCODE, so its program was left in $installed." }
    }
    if (-not $DryRun -and (Test-Path -LiteralPath $installed)) {
        Remove-Item -LiteralPath $installed -Recurse -Force
    }
    return
}

if (-not $releasePublisher) {
    throw "This copy of ci-runner-install.ps1 names no publisher, so it installs nothing: download it from a signed Code Goblins release."
}

$folder = Join-Path ([IO.Path]::GetTempPath()) ("code-goblins-ci-runner-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $folder | Out-Null
try {
    $sums = Join-Path $folder "SHA256SUMS"
    $download = Join-Path $folder "cg-ci-runner.exe"
    Invoke-WebRequest -Uri "$releaseBase/SHA256SUMS" -OutFile $sums -UseBasicParsing
    Invoke-WebRequest -Uri "$releaseBase/cg-ci-runner.exe" -OutFile $download -UseBasicParsing
    $expected = ""
    foreach ($line in Get-Content -LiteralPath $sums) {
        $fields = @($line.Trim() -split "\s+")
        if ($fields.Count -eq 2 -and $fields[1].TrimStart("*") -eq "cg-ci-runner.exe") {
            $expected = $fields[0]
        }
    }
    $actual = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash
    if (-not $expected -or $actual -ne $expected) {
        Write-Host "SHA256 of the download: $actual; the release's SHA256SUMS lists: '$expected'"
        throw "The downloaded cg-ci-runner.exe does not match the release's checksum, so nothing was installed."
    }
    $signature = Get-AuthenticodeSignature -LiteralPath $download
    $signer = ""
    if ($signature.SignerCertificate) {
        $signer = $signature.SignerCertificate.GetNameInfo("SimpleName", $false)
    }
    if ($signature.Status -ne "Valid" -or $signer -ne $releasePublisher) {
        Write-Host "Signature of the download: $($signature.Status), by '$signer'; the release is signed by '$releasePublisher'"
        throw "The downloaded cg-ci-runner.exe is not signed by $releasePublisher, so nothing was installed."
    }
    Write-Host "Verified cg-ci-runner.exe against the release's SHA256SUMS ($actual) and its signature by $releasePublisher."

    $arguments = @("install", "--app-id", $AppId, "--installation-id", $InstallationId, "--repo", $Repo, "--key", $Key, "--user", $User)
    if ($DryRun) {
        & $download @($arguments + "--dry-run")
        if ($LASTEXITCODE -ne 0) { throw "cg-ci-runner install --dry-run failed with exit $LASTEXITCODE." }
        return
    }
    New-Item -ItemType Directory -Force -Path $installed | Out-Null
    Copy-Item -LiteralPath $download -Destination $program -Force
    & $program @arguments
    if ($LASTEXITCODE -ne 0) { throw "cg-ci-runner install failed with exit $($LASTEXITCODE): its output above says at which step." }
}
finally {
    Remove-Item -LiteralPath $folder -Recurse -Force -ErrorAction SilentlyContinue
}
