package harness

import (
	"path/filepath"
	"testing"
)

func TestCatalogEffortsBuildInTheNativeAdapters(t *testing.T) {
	root := t.TempDir()
	for _, kind := range []Kind{Claude, Codex} {
		t.Run(string(kind), func(t *testing.T) {
			efforts := Efforts(kind)
			if len(efforts) == 0 {
				t.Fatal("native adapter exposes no reasoning efforts")
			}
			adapter, err := DefaultRegistry().Get(kind)
			if err != nil {
				t.Fatal(err)
			}
			for _, effort := range efforts {
				if _, err := adapter.Build(LaunchSpec{BriefPath: filepath.Join(root, "brief.md"), TaskTmp: root, Scratch: root, Effort: effort}); err != nil {
					t.Fatalf("catalog effort %s refused: %v", effort, err)
				}
			}
		})
	}
	if len(Efforts(Kind("kimi"))) != 0 || len(Efforts(Kind("unknown"))) != 0 {
		t.Fatal("efforts advertised for an adapter with no effort flag")
	}
}
