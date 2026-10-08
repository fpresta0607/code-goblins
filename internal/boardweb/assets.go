// Package boardweb embeds the board Vite builds from frontend into
// dist/board, which git ignores. Node is needed only to build the board,
// never to run cfo serve. A checkout that has not built it embeds the one
// file under dist that is committed, the placeholder page dist/index.html,
// which says so and names the command that builds it. A build carrying only
// that page is never installed: cfo update and cfo install refuse it.
package boardweb

import (
	"embed"
	"io/fs"
)

//go:generate powershell.exe -NoProfile -NonInteractive -Command "Set-Location ../../frontend; npm ci; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }; npm run build; exit $LASTEXITCODE"
//go:embed all:dist
var assets embed.FS

// Built reports whether this build embeds the board rather than the
// placeholder page.
func Built() bool {
	_, err := fs.Stat(assets, "dist/board/index.html")
	return err == nil
}

// Assets is the board, or the placeholder page where it was not built.
func Assets() (fs.FS, error) {
	if !Built() {
		return fs.Sub(assets, "dist")
	}
	return fs.Sub(assets, "dist/board")
}
