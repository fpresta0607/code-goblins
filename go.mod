module github.com/fpresta0607/code-goblins

go 1.26.5

require gopkg.in/yaml.v3 v3.0.1

require golang.org/x/sys v0.48.0

require github.com/coder/websocket v1.8.15

require (
	git.sr.ht/~jackmordaunt/go-toast/v2 v2.0.3 // indirect
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/wailsapp/wails/v3 v3.0.0-beta.26
)

// npm installs the frontend's packages here. One of them ships Go code, which
// ./... would make a package of this module, and walking them all slowed every
// go list ./... on a loaded machine.
ignore ./frontend/node_modules
