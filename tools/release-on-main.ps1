# release-on-main.ps1 - refuse a release whose tag is no version, or whose
# commit main does not hold or is not newer than the last release's.
#
# release.yml runs it before it builds anything, with the tag and its commit:
#
#   ./tools/release-on-main.ps1 -Tag $env:RELEASE_TAG -Commit $env:GITHUB_SHA -Main origin/main
#
# The release signs what it builds with the publisher's own certificate, and
# pushing a tag needs only write access to the repository. A tag can name any
# commit, such as one on a branch no review has passed, or an old one that
# would go out again as new, and its name reaches the build. So the name must
# be a version, v and three numbers, the commit must be one main holds, and
# it must be newer than the last release's commit, under a higher version.
# A check git cannot answer is refused too, never taken for a pass.
param(
    [Parameter(Mandatory)][string]$Tag,
    [Parameter(Mandatory)][string]$Commit,
    [string]$Main = "origin/main"
)
$ErrorActionPreference = "Stop"

$versionShape = '^v[0-9]+\.[0-9]+\.[0-9]+$'
if ($Tag -cnotmatch $versionShape) {
    throw "The tag '$Tag' is not a version, v and three numbers, so nothing is built."
}

& git merge-base --is-ancestor $Commit $Main
switch ($LASTEXITCODE) {
    0 { }
    1 { throw "The release's commit $Commit is not on $Main, so nothing is built: tag a commit that $Main holds." }
    default { throw "git merge-base could not say whether $Commit is on $Main (exit $LASTEXITCODE), so nothing is built." }
}

$released = @(& git tag --list 'v*' --merged $Main)
if ($LASTEXITCODE -ne 0) {
    throw "git could not list the releases on $Main (exit $LASTEXITCODE), so nothing is built."
}
$last = $released |
    Where-Object { $_ -ne $Tag -and $_ -cmatch $versionShape } |
    Sort-Object { [version]$_.Substring(1) } -Descending |
    Select-Object -First 1
if ($last) {
    if ([version]$Tag.Substring(1) -le [version]$last.Substring(1)) {
        throw "The tag $Tag is no higher than the last release, $last, so nothing is built."
    }
    $lastCommit = & git rev-list -n 1 $last
    if ($LASTEXITCODE -ne 0 -or -not $lastCommit) {
        throw "git could not find the commit of the last release, $last, so nothing is built."
    }
    if ($lastCommit -eq $Commit) {
        throw "The release's commit $Commit is the last release's, $last, so nothing is built."
    }
    & git merge-base --is-ancestor $lastCommit $Commit
    switch ($LASTEXITCODE) {
        0 { }
        1 { throw "The release's commit $Commit is older than the last release, $last, so nothing is built: tag a commit newer than it." }
        default { throw "git merge-base could not say whether $Commit is newer than $last (exit $LASTEXITCODE), so nothing is built." }
    }
}
"The release $Tag is commit $Commit, on $Main and newer than the last release$(if ($last) { ", $last" })."
