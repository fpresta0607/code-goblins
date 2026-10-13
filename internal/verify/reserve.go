package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// DefaultDesktopReserve is the memory a measured run leaves the desktop.
// Below it, in either dimension, the run is cancelled and ended. It sits
// under the admission floor, since a run admitted at the floor uses the
// memory between the two.
var DefaultDesktopReserve = Headroom{PhysicalAvailable: 2 << 30, CommitAvailable: 2 << 30}

// DesktopReserve reads the machine's desktop reserve from reserve.json in
// the admission store dir, {"physical_gib": 2, "commit_gib": 2}, each at
// least half a gigabyte and under the admission floor. A store without the file keeps the
// default. Any other content is refused, so a broken setting never runs a
// heavy command without its reserve.
func DesktopReserve(dir string) (Headroom, error) {
	data, err := fsx.ReadFile(filepath.Join(dir, "reserve.json"))
	if errors.Is(err, os.ErrNotExist) {
		return DefaultDesktopReserve, nil
	}
	if err != nil {
		return Headroom{}, err
	}
	var setting struct {
		PhysicalGiB *float64 `json:"physical_gib"`
		CommitGiB   *float64 `json:"commit_gib"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&setting); err != nil {
		return Headroom{}, fmt.Errorf("verify: reserve.json must hold only physical_gib and commit_gib: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Headroom{}, errors.New("verify: reserve.json has trailing data")
	}
	_, floor, err := AdmissionLimits(dir)
	if err != nil {
		return Headroom{}, err
	}
	limit := float64(floor) / (1 << 30)
	for _, value := range []*float64{setting.PhysicalGiB, setting.CommitGiB} {
		if value == nil || !(*value >= 0.5 && *value < limit) {
			return Headroom{}, fmt.Errorf("verify: reserve.json must set physical_gib and commit_gib from 0.5 to under the %.0f GiB admission floor", limit)
		}
	}
	return Headroom{PhysicalAvailable: uint64(*setting.PhysicalGiB * (1 << 30)), CommitAvailable: uint64(*setting.CommitGiB * (1 << 30))}, nil
}
