package supervisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type FleetCapacity struct {
	Live       int `json:"live"`
	Limit      int `json:"limit"`
	Configured int `json:"configured"`
	Slots      int `json:"slots"`
}

func ReadFleetCapacity(h home.Home, memory Memory) (FleetCapacity, error) {
	settings := struct {
		MaxLiveGoblins int `json:"max_live_goblins"`
	}{MaxLiveGoblins: 8}
	data, err := fsx.ReadFile(filepath.Join(h.Root, "config", "fleet.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return FleetCapacity{}, err
	}
	if err == nil {
		if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
			return FleetCapacity{}, errors.New("fleet cap setting must contain one JSON object")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&settings); err != nil {
			return FleetCapacity{}, fmt.Errorf("fleet cap setting: %w", err)
		}
		if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
			return FleetCapacity{}, errors.New("fleet cap setting must contain one JSON object")
		}
	}
	if settings.MaxLiveGoblins < 1 || settings.MaxLiveGoblins > 128 {
		return FleetCapacity{}, errors.New("max_live_goblins must be between 1 and 128")
	}
	capacity := FleetCapacity{Configured: settings.MaxLiveGoblins}
	scan, err := state.ScanIDs(h.State)
	if err != nil {
		return capacity, err
	}
	for _, id := range scan.MetaIDs {
		meta, err := state.ReadTaskMeta(h.State, id)
		if err != nil {
			return capacity, err
		}
		if meta.Backend != "native" {
			continue
		}
		record, err := host.ReadRecord(h.State, meta.ID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return capacity, err
		}
		if host.Running(record) {
			capacity.Live++
		}
	}
	free := min(memory.Available, memory.CommitAvailable)
	resourceSlots := 0
	if free >= memoryFloor {
		resourceSlots = int((free - memoryFloor) / (1 << 30))
	}
	capacity.Limit = min(capacity.Configured, capacity.Live+resourceSlots)
	capacity.Slots = max(0, capacity.Limit-capacity.Live)
	return capacity, nil
}

func CheckLaunch(h home.Home, memory Memory) error {
	if short := memory.shortfall(); short != "" {
		return errors.New(short + "; a start needs 5 GB of memory and commit to keep the 4 GB floor")
	}
	capacity, err := ReadFleetCapacity(h, memory)
	if err != nil {
		return err
	}
	if capacity.Slots == 0 {
		return fmt.Errorf("live goblin cap reached: %d live, cap %d (configured maximum %d); wait for a goblin to pause or finish", capacity.Live, capacity.Limit, capacity.Configured)
	}
	return nil
}
