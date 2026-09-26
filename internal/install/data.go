package install

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/layout"
)

// layOutData lays out the home's data folder, whichever kind of home it is,
// and leaves a folder that holds data from before the layout exactly as it
// is: laying that out means moving the operator's files, which an install
// never does.
func (s Service) layOutData(report *reporter) error {
	data := filepath.Join(s.Root, "data")
	created, legacy, err := layout.Ensure(data)
	if err != nil {
		return fmt.Errorf("install: lay out %s: %w", data, err)
	}
	switch {
	case legacy:
		report.same("data", "kept "+data+" as it is: it holds data from before the home layout, and laying it out would move your files")
	case len(created) > 0:
		report.change("data", "laid out "+data+": "+strings.Join(created, ", "))
	default:
		report.same("data", data+" is laid out")
	}
	return nil
}
