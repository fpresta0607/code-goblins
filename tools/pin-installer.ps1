# pin-installer.ps1 - write the install.ps1 a release publishes.
#
# release.yml runs it once the release's programs are signed:
#
#   ./tools/pin-installer.ps1 -Repository owner/name -Tag v1.2.3 -Publisher "Name" -Destination release/install.ps1
#
# The copy it writes downloads from that repository's release of that tag,
# never the latest release or another repository's, and installs only
# programs validly signed by that publisher. It writes nothing when a value
# cannot be written into the script as it is, or when install.ps1 does not
# name each of the three exactly once.
param(
    [Parameter(Mandatory)][string]$Repository,
    [Parameter(Mandatory)][string]$Tag,
    [Parameter(Mandatory)][string]$Publisher,
    [Parameter(Mandatory)][string]$Destination
)
$ErrorActionPreference = "Stop"

if ($Repository -notmatch '^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$') {
    throw "'$Repository' is not a GitHub repository's owner/name"
}
if ($Tag -notmatch '^v[0-9A-Za-z._+-]+$') {
    throw "'$Tag' is not a release tag"
}
# A release download carries no charset, which Windows PowerShell reads as
# Latin-1, so the script stays plain ASCII; and the publisher goes between
# double quotes, where " ` and $ would mean something else.
if ($Publisher -notmatch '^[\x20-\x7E]+$' -or $Publisher -match '["`$]') {
    throw "The publisher name '$Publisher' cannot be written into install.ps1 as it is"
}

$script = [IO.File]::ReadAllText((Join-Path (Split-Path -Parent $PSScriptRoot) "install.ps1"))
if ($script -match '[^\x00-\x7F]') {
    throw "install.ps1 has a character outside ASCII"
}
$pins = @(
    @{ Pattern = '\$repo = "[^"\r\n]*"'; Value = "`$repo = `"$Repository`"" },
    @{ Pattern = [regex]::Escape('releases/latest/download"'); Value = "releases/download/$Tag`"" },
    @{ Pattern = [regex]::Escape('$releasePublisher = ""'); Value = "`$releasePublisher = `"$Publisher`"" }
)
foreach ($pin in $pins) {
    $found = @([regex]::Matches($script, $pin.Pattern))
    if ($found.Count -ne 1) {
        throw "install.ps1 matches $($pin.Pattern) $($found.Count) times, want once"
    }
    $script = $script.Replace($found[0].Value, $pin.Value)
}
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Destination) | Out-Null
Set-Content -LiteralPath $Destination -Value $script -NoNewline -Encoding ascii
