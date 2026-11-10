# The names a build from before the supervisor's role was renamed, on
# 2026-11-09, knows itself by: its command, which is also the folder under cmd
# it builds from, and the prefix of every setting it reads. A test that drives
# such a build, a pinned baseline or a published release, dot-sources this
# file, so no other file spells them.
$PreRenameCommand = 'cfo'
$PreRenameSettingPrefix = 'CFO_'
