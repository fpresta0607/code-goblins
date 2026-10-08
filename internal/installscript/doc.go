// Package installscript holds the tests of the install scripts at the
// repository's root: install.ps1, install.cmd and tools/pin-installer.ps1.
// Each runs a real PowerShell, so together they take minutes. They sat in the
// root package, which embeds AGENTS.md and the docs it links to, so every
// edit to those docs changed the root package and ran all of them in the
// gate's local test step, although no install test reads a doc. In a package
// of their own a docs edit runs only the root package's own tests, which take
// seconds, and these run when this package changes and, as every package
// does, in CI.
package installscript
