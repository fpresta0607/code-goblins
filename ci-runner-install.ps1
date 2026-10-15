# ci-runner-install.ps1 - install, or remove, the desktop CI runner from one
# Code Goblins release.
#
# The copy published with a release names that release, its publisher and
# its cg-ci-runner.exe's SHA-256 below, and installs only that program, with
# a valid signature from that publisher. The hash is pinned in this script,
# which the release signs, so no file beside the program, and no server it
# comes from, can vouch for another. A copy that names no publisher, as this
# file in the repository does, installs nothing, since the service it
# installs holds a GitHub App's key.
#
# From an elevated Windows PowerShell, after downloading this script from the
# release:
#
#   powershell -NoProfile -ExecutionPolicy AllSigned -File ci-runner-install.ps1 -AppId <id> -InstallationId <id> -Repo <owner/name> -Key <file> -User <account> -RemoveKey
#
# -DryRun, from a PowerShell that is not elevated, downloads and checks the
# program, then shows each step of the install and changes nothing. -RemoveKey deletes the
# App's key file once the service holds its own copy. -Uninstall removes the
# runner, its VM, its service, its key and its program.
param(
    [string]$AppId,
    [string]$InstallationId,
    [string]$Repo,
    [string]$Key,
    [string]$User,
    [switch]$DryRun,
    [switch]$RemoveKey,
    [switch]$Uninstall
)
$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
$ProgressPreference = "SilentlyContinue"

# The copy of this script published with a release names that release's
# files here, in place of latest, its publisher, and its program's SHA-256.
$releaseRepository = "fpresta0607/code-goblins"
$releaseBase = "https://github.com/$releaseRepository/releases/latest/download"
$releasePublisher = ""
$releaseProgramHash = ""

# Program Files as Windows gives it, which only Administrators may change,
# never as a variable a user can set says.
$installed = Join-Path ([Environment]::GetFolderPath("ProgramFiles")) "Code Goblins\ci-runner"
$program = Join-Path $installed "cg-ci-runner.exe"
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
$isElevated = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $DryRun -and -not $isElevated) {
    throw "The runner's install makes a service and a Hyper-V VM, so run this from an elevated Windows PowerShell, or with -DryRun to see each step."
}
if ($DryRun -and $isElevated) {
    throw "A dry run changes nothing and needs no elevation, so run it from a Windows PowerShell that is not elevated: elevated, the program it checks in your own temp folder would run as an administrator."
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

if (-not $releasePublisher -or -not $releaseProgramHash) {
    throw "This copy of ci-runner-install.ps1 names no publisher, so it installs nothing: download it from a signed Code Goblins release."
}

# Confirm-Program refuses a program that is not the release's: its SHA-256
# must be the one this script pins, and its signature valid and by the
# release's publisher.
function Confirm-Program([string]$Path) {
    $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
    if ($actual -ne $releaseProgramHash) {
        Write-Host "SHA256 of the download: $actual. The release pins $releaseProgramHash."
        throw "The downloaded cg-ci-runner.exe is not the release's program, so nothing was installed."
    }
    $signature = Get-AuthenticodeSignature -LiteralPath $Path
    $signer = ""
    if ($signature.SignerCertificate) {
        $signer = $signature.SignerCertificate.GetNameInfo("SimpleName", $false)
    }
    if ($signature.Status -ne "Valid" -or $signer -ne $releasePublisher) {
        Write-Host "Signature of the download: $($signature.Status), by '$signer'. The release is signed by '$releasePublisher'."
        throw "The downloaded cg-ci-runner.exe is not signed by $releasePublisher, so nothing was installed."
    }
    Write-Host "Verified cg-ci-runner.exe as the release's program ($actual), signed by $releasePublisher."
}

$arguments = @("install", "--app-id", $AppId, "--installation-id", $InstallationId, "--repo", $Repo, "--key", $Key, "--user", $User)
if ($RemoveKey) { $arguments += "--remove-key" }

if ($DryRun) {
    # A dry run is never elevated, as the script refuses one that is, so it
    # may check and run the program from the user's own temp folder.
    $folder = Join-Path ([IO.Path]::GetTempPath()) ("code-goblins-ci-runner-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $folder | Out-Null
    try {
        $download = Join-Path $folder "cg-ci-runner.exe"
        Invoke-WebRequest -Uri "$releaseBase/cg-ci-runner.exe" -OutFile $download -UseBasicParsing
        Confirm-Program $download
        & $download @($arguments + "--dry-run")
        if ($LASTEXITCODE -ne 0) { throw "cg-ci-runner install --dry-run failed with exit $LASTEXITCODE." }
    }
    finally {
        Remove-Item -LiteralPath $folder -Recurse -Force -ErrorAction SilentlyContinue
    }
    return
}

# Elevated, the program is downloaded into Program Files, checked there and
# run from there. A user's own temp folder is open to every program the
# user runs, which could change the program between its check and its run.
New-Item -ItemType Directory -Force -Path $installed | Out-Null
$download = Join-Path $installed "cg-ci-runner.exe.download"
try {
    Invoke-WebRequest -Uri "$releaseBase/cg-ci-runner.exe" -OutFile $download -UseBasicParsing
    Confirm-Program $download
    Move-Item -LiteralPath $download -Destination $program -Force
}
finally {
    Remove-Item -LiteralPath $download -Force -ErrorAction SilentlyContinue
}
& $program @arguments
if ($LASTEXITCODE -ne 0) { throw "cg-ci-runner install failed with exit $($LASTEXITCODE): its output above says at which step." }
