// Package boardweb embeds Vite's reproducibly built assets. Node is needed
// only to rebuild frontend sources, never to run cfo serve.
package boardweb

import (
	"embed"
	"io/fs"
)

//go:generate powershell.exe -NoProfile -NonInteractive -Command "Set-Location ../../frontend; npm ci; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }; npm run build; exit $LASTEXITCODE"
//go:embed all:dist
var assets embed.FS

func Assets() (fs.FS, error) { return fs.Sub(assets, "dist") }
