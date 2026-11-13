package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The program's icon has to reach its resource objects byte for byte.
// go-winres copies an .ico as it is, but encodes a PNG again with the Go that
// runs it, and Go 1.27 writes other bytes than Go 1.26 does. With a PNG the
// committed objects match winres.json under one Go and not under another,
// which is what CI checks.
func TestTheProgramsIconIsAnIcoThatIsCopiedAsItIs(t *testing.T) {
	data, err := os.ReadFile("winres.json")
	if err != nil {
		t.Fatal(err)
	}
	var resources struct {
		Icons map[string]map[string]string `json:"RT_GROUP_ICON"`
	}
	if err := json.Unmarshal(data, &resources); err != nil {
		t.Fatal(err)
	}

	named := 0
	for id, languages := range resources.Icons {
		for language, file := range languages {
			named++
			if filepath.Ext(file) != ".ico" {
				t.Errorf("icon %s %s is %s: an .ico is wanted, since any other picture is encoded again by the Go that runs go-winres", id, language, file)
			}
			if _, err := os.Stat(file); err != nil {
				t.Errorf("icon %s %s: %v", id, language, err)
			}
		}
	}

	if named == 0 {
		t.Fatal("winres.json names no icon")
	}
}
