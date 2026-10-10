package supervisor

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// The Overlord asked on 2026-10-09 for CPU and GPU meters beside memory and
// disk in Tasks. The snapshot carries what they show: how free the
// performance cores are against the mark at which the supervisor starts a
// goblin by itself, the efficiency cores beside them, and each graphics
// adapter with the program using most of it.
func TestSnapshotShowsTheProcessorsAgainstTheNextStartAndTheGraphicsAdapters(t *testing.T) {
	// Arrange
	handler, _ := startBoard(t, 9*gigabyte, &spawnRecorder{})
	handler.Service.Options.Dispatch.Processors = func() (Processors, error) {
		return Processors{PerformanceCores: 6, EfficiencyCores: 4, Free: 0.55, EfficiencyFree: 0.125}, nil
	}
	adapters := GPU{Adapters: []GPUAdapter{{Name: "Intel(R) Graphics", Busy: 0.75, Busiest: "siqshift-desktop.exe"}, {Name: "NVIDIA GeForce RTX 5060 Laptop GPU"}}}
	handler.Service.Options.Dispatch.GPU = func() (GPU, error) { return adapters, nil }

	// Act
	snapshot, err := handler.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	wantProcessors := Processors{PerformanceCores: 6, EfficiencyCores: 4, Free: 0.55, EfficiencyFree: 0.125, Next: 0.25}
	if !reflect.DeepEqual(snapshot.Processors, &wantProcessors) {
		t.Fatalf("processors = %+v, want %+v", snapshot.Processors, wantProcessors)
	}
	if !reflect.DeepEqual(snapshot.GPU, &adapters) {
		t.Fatalf("gpu = %+v, want %+v", snapshot.GPU, adapters)
	}
	for name, test := range map[string]struct {
		value any
		want  string
	}{
		"processors": {snapshot.Processors, `{"performance_cores":6,"efficiency_cores":4,"free":0.55,"efficiency_free":0.125,"next":0.25}`},
		"gpu":        {snapshot.GPU, `{"adapters":[{"name":"Intel(R) Graphics","busy":0.75,"busiest":"siqshift-desktop.exe"},{"name":"NVIDIA GeForce RTX 5060 Laptop GPU","busy":0}]}`},
	} {
		if got, err := json.Marshal(test.value); err != nil || string(got) != test.want {
			t.Errorf("%s reaches the board as %s (%v), want %s", name, got, err, test.want)
		}
	}
}

// A board that cannot read the processors or the graphics adapters shows no
// meter for them, and still shows the rest.
func TestSnapshotLeavesOutTheProcessorsAndGraphicsAdaptersItCannotRead(t *testing.T) {
	for _, test := range []struct {
		name       string
		processors func() (Processors, error)
		gpu        func() (GPU, error)
	}{
		{"neither is read", nil, nil},
		{"both fail", func() (Processors, error) { return Processors{}, errors.New("no reading") }, func() (GPU, error) { return GPU{}, ErrNoGPU }},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			handler, _ := startBoard(t, 9*gigabyte, &spawnRecorder{})
			handler.Service.Options.Dispatch.Processors = test.processors
			handler.Service.Options.Dispatch.GPU = test.gpu

			// Act
			snapshot, err := handler.Service.Snapshot()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Processors != nil || snapshot.GPU != nil {
				t.Fatalf("processors = %+v and gpu = %+v, want neither", snapshot.Processors, snapshot.GPU)
			}
			if snapshot.Memory == nil || snapshot.Disk == nil {
				t.Fatalf("memory = %+v and disk = %+v, want both still shown", snapshot.Memory, snapshot.Disk)
			}
		})
	}
}
