# release-on-main.ps1 - refuse a release whose commit is not on main.
#
# release.yml runs it before it builds anything, with the tag's commit:
#
#   ./tools/release-on-main.ps1 -Commit $env:GITHUB_SHA -Main origin/main
#
# A tag can name any commit, such as one on a branch no review has passed, and
# the release signs what it builds with the publisher's own certificate, so a
# commit main does not hold is refused, and a check git cannot answer is
# refused too, never taken for a pass.
param(
    [Parameter(Mandatory)][string]$Commit,
    [string]$Main = "origin/main"
)
$ErrorActionPreference = "Stop"

& git merge-base --is-ancestor $Commit $Main
switch ($LASTEXITCODE) {
    0 { "The release's commit $Commit is on $Main." }
    1 { throw "The release's commit $Commit is not on $Main, so nothing is built: tag a commit that $Main holds." }
    default { throw "git merge-base could not say whether $Commit is on $Main (exit $LASTEXITCODE), so nothing is built." }
}
