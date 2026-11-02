package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/project"
)

func TestTaskDeliveryPreservesItsSelectionAndRejectsMutation(t *testing.T) {
	selection, err := project.SelectDelivery(project.Manifest{}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	data, hash, err := selection.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, mode, content string
		isRejected          bool
	}{
		{"original", "no-mistakes", data, false},
		{"different mode", "direct-PR", data, true},
		{"changed receipt", "no-mistakes", "{}", true},
		{"missing receipt", "no-mistakes", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			meta := TaskMeta{ID: "task", Mode: test.mode, DeliveryHash: hash, TaskTmp: t.TempDir()}
			if test.content != "" {
				if err := os.WriteFile(filepath.Join(meta.TaskTmp, "delivery.json"), []byte(test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			got, err := ReadTaskDelivery(meta)

			// Assert
			if (err != nil) != test.isRejected || err == nil && (got.Mode != "no-mistakes" || got.Reason == "") {
				t.Fatalf("selection=%+v error=%v", got, err)
			}
		})
	}
}
