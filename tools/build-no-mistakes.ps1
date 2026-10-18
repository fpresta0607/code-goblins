# build-no-mistakes.ps1 - build the no-mistakes the fleet's gate needs.
#
#   tools/build-no-mistakes.ps1 -Destination <path of no-mistakes.exe> [-Notices <path>]
#
# install.ps1 pins the build: the repository it comes from, the commit, and
# the version that build reports. This script reads those pins from the
# install.ps1 beside it, fetches exactly that commit, and builds it with the
# version stamped and no telemetry host, so the program reports the version
# install.ps1 checks and sends nothing anywhere. A release builds it so and
# signs it with its own programs, the install workflow serves it as the
# release's, and install.cmd -Dev builds it in a clone, where no release has
# it. -Notices writes the program's licence and those of everything compiled
# into it, as a release publishes them beside it. It needs git and Go, and
# runs in Windows PowerShell 5.1 as in 7.
param(
    [Parameter(Mandatory)] [string]$Destination,
    [string]$Notices
)
$ErrorActionPreference = "Stop"

$installScript = [IO.File]::ReadAllText((Join-Path $PSScriptRoot "..\install.ps1"))
function Read-Pin([string]$Name) {
    $pin = [regex]::Match($installScript, '\$' + $Name + ' = "([^"]+)"')
    if (-not $pin.Success) { throw "install.ps1 pins no `$$Name." }
    return $pin.Groups[1].Value
}
$version = Read-Pin "noMistakesVersion"
$repository = Read-Pin "noMistakesRepository"
$commit = Read-Pin "noMistakesCommit"
if ($commit -notmatch '^[0-9a-f]{40}$') { throw "install.ps1 pins no-mistakes commit '$commit', which is not a full commit id." }
# The version names its commit as git describe does, so the two pins cannot
# drift apart.
if ($version -notmatch "-g$($commit.Substring(0, 7))$") { throw "install.ps1 pins no-mistakes v$version, which does not name the pinned commit $commit." }

$Destination = [IO.Path]::GetFullPath($Destination)
$source = Join-Path ([IO.Path]::GetTempPath()) ("no-mistakes-source-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $source | Out-Null
$cgo = $env:CGO_ENABLED
try {
    & git -C $source init -q
    if ($LASTEXITCODE -ne 0) { throw "git init exited $LASTEXITCODE." }
    & git -C $source fetch -q --depth 1 "https://github.com/$repository.git" $commit
    if ($LASTEXITCODE -ne 0) { throw "git could not fetch $commit from $repository (exit $LASTEXITCODE)." }
    & git -C $source -c advice.detachedHead=false checkout -q FETCH_HEAD
    if ($LASTEXITCODE -ne 0) { throw "git checkout exited $LASTEXITCODE." }
    $fetched = (& git -C $source rev-parse HEAD).Trim()
    if ($fetched -ne $commit) { throw "$repository gave commit $fetched for $commit." }
    # The build is dated by its commit, so one commit builds to one program.
    $date = ([DateTimeOffset]::Parse((& git -C $source show -s --format=%cI HEAD).Trim())).UtcDateTime.ToString("yyyy-MM-ddTHH:mm:ssZ")
    $module = [regex]::Match([IO.File]::ReadAllText((Join-Path $source "go.mod")), '(?m)^module\s+(\S+)').Groups[1].Value
    if (-not $module) { throw "$repository at $commit has no module line in go.mod." }
    $flags = "-X $module/internal/buildinfo.Version=v$version -X $module/internal/buildinfo.Commit=$($commit.Substring(0, 7)) -X $module/internal/buildinfo.Date=$date"
    $env:CGO_ENABLED = "0"
    Push-Location -LiteralPath $source
    try {
        & go build -trimpath -ldflags $flags -o $Destination ./cmd/no-mistakes
        if ($LASTEXITCODE -ne 0) { throw "go build of no-mistakes exited $LASTEXITCODE." }
    }
    finally {
        Pop-Location
    }
    if ($Notices) {
        $Notices = [IO.Path]::GetFullPath($Notices)
        Push-Location -LiteralPath (Join-Path $PSScriptRoot "..")
        try {
            & go run ./tools/notices -program $source -package ./cmd/no-mistakes -out $Notices
            if ($LASTEXITCODE -ne 0) { throw "tools/notices could not write the notices of no-mistakes (exit $LASTEXITCODE)." }
        }
        finally {
            Pop-Location
        }
    }
}
finally {
    $env:CGO_ENABLED = $cgo
    Remove-Item -LiteralPath $source -Recurse -Force -ErrorAction SilentlyContinue
}

# The build reports the pinned version, and takes a gate's intent from a
# file and a launch selection, as the install checks it.
$said = & { $ErrorActionPreference = "Continue"; & $Destination --version 2>$null } | Out-String
if ($said -notmatch "(?m)^no-mistakes version v$([regex]::Escape($version))(\s|$)") { throw "the built no-mistakes says '$($said.Trim())', want v$version." }
$help = & { $ErrorActionPreference = "Continue"; & $Destination help axi run 2>$null } | Out-String
foreach ($flag in "--intent-file", "--launch-assertion") {
    if (-not $help.Contains($flag)) { throw "the built no-mistakes' axi run has no $flag, which the fleet's gate needs." }
}
Write-Host "Built no-mistakes v$version from $repository at $commit into $Destination"
