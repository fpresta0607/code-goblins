package supervisor

import (
	"errors"
	"math"
	"testing"
)

// overlordsAdapters are the Overlord's two graphics adapters, as Windows
// names them and numbers them.
func overlordsAdapters() []gpuAdapter {
	return []gpuAdapter{
		{luid: 0x14d79, name: "Intel(R) Graphics"},
		{luid: 0x1517d, name: "NVIDIA GeForce RTX 5060 Laptop GPU"},
	}
}

func programNames(names map[uint32]string) func(uint32) string {
	return func(pid uint32) string { return names[pid] }
}

// On 2026-10-09 a hidden window of one of the Overlord's own apps held 33% to
// 78% of his Intel adapter's 3D engine all day while his NVIDIA adapter sat
// idle, and nothing in the fleet read either. An adapter is as busy as its
// busiest engine, which every program using it adds to, and names the program
// using most of that engine. A machine reads as the adapters it has: two as
// his, one, or none.
func TestReadGPUTakesEachAdaptersBusiestEngineAndItsBusiestProgram(t *testing.T) {
	for _, test := range []struct {
		name     string
		adapters []gpuAdapter
		uses     []gpuEngineUse
		want     []GPUAdapter
	}{
		{
			name:     "two adapters, two programs on one engine and a little on another",
			adapters: overlordsAdapters(),
			uses: []gpuEngineUse{
				{instance: "pid_7692_luid_0x00000000_0x00014D79_phys_0_eng_0_engtype_3D", percent: 63.6},
				{instance: "pid_29156_luid_0x00000000_0x00014D79_phys_0_eng_0_engtype_3D", percent: 6.9},
				{instance: "pid_29156_luid_0x00000000_0x00014D79_phys_0_eng_3_engtype_VideoDecode", percent: 20},
			},
			want: []GPUAdapter{
				{Name: "Intel(R) Graphics", Busy: 0.705, Busiest: "msedgewebview2.exe"},
				{Name: "NVIDIA GeForce RTX 5060 Laptop GPU"},
			},
		},
		{
			name:     "two adapters, an engine counted past full reads as full",
			adapters: overlordsAdapters(),
			uses: []gpuEngineUse{
				{instance: "pid_7692_luid_0x00000000_0x0001517D_phys_0_eng_0_engtype_3D", percent: 80},
				{instance: "pid_29156_luid_0x00000000_0x0001517D_phys_0_eng_0_engtype_3D", percent: 45},
			},
			want: []GPUAdapter{
				{Name: "Intel(R) Graphics"},
				{Name: "NVIDIA GeForce RTX 5060 Laptop GPU", Busy: 1, Busiest: "msedgewebview2.exe"},
			},
		},
		{
			name:     "two adapters, an adapter Windows does not name, a name that is no engine and a program that ended",
			adapters: overlordsAdapters(),
			uses: []gpuEngineUse{
				{instance: "pid_4_luid_0x00000000_0x00015148_phys_0_eng_0_engtype_3D", percent: 50},
				{instance: "_Total", percent: 90},
				{instance: "pid_555_luid_0x00000000_0x00014D79_phys_0_eng_0_engtype_3D", percent: 12},
			},
			want: []GPUAdapter{
				{Name: "Intel(R) Graphics", Busy: 0.12},
				{Name: "NVIDIA GeForce RTX 5060 Laptop GPU"},
			},
		},
		{
			name:     "two adapters, nothing in use",
			adapters: overlordsAdapters(),
			want: []GPUAdapter{
				{Name: "Intel(R) Graphics"},
				{Name: "NVIDIA GeForce RTX 5060 Laptop GPU"},
			},
		},
		{
			name:     "one adapter",
			adapters: overlordsAdapters()[:1],
			uses: []gpuEngineUse{
				{instance: "pid_29156_luid_0x00000000_0x00014D79_phys_0_eng_0_engtype_3D", percent: 31},
			},
			want: []GPUAdapter{{Name: "Intel(R) Graphics", Busy: 0.31, Busiest: "chrome.exe"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got, err := readGPU(test.adapters, test.uses, programNames(map[uint32]string{7692: "msedgewebview2.exe", 29156: "chrome.exe"}))

			// Assert
			if err != nil {
				t.Fatalf("readGPU: %v", err)
			}
			if len(got.Adapters) != len(test.want) {
				t.Fatalf("adapters = %+v, want %+v", got.Adapters, test.want)
			}
			for index, want := range test.want {
				adapter := got.Adapters[index]
				if adapter.Name != want.Name || math.Abs(adapter.Busy-want.Busy) > 1e-9 || adapter.Busiest != want.Busiest {
					t.Errorf("adapter %d = %+v, want %+v", index, adapter, want)
				}
			}
		})
	}
}

// On 2026-10-09 CI's machine, which counts graphics engines but has no
// adapter besides Windows' software renderer, read as a GPU with no adapters,
// which a board would have drawn as a meter of nothing. A machine with no
// adapter has no GPU reading at all, whatever its engines count.
func TestReadGPUOnAMachineWithNoAdapterIsNoReading(t *testing.T) {
	for _, test := range []struct {
		name string
		uses []gpuEngineUse
	}{
		{"its software renderer in use", []gpuEngineUse{{instance: "pid_4_luid_0x00000000_0x00015148_phys_0_eng_0_engtype_3D", percent: 50}}},
		{"nothing in use", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got, err := readGPU(nil, test.uses, programNames(nil))

			// Assert
			if !errors.Is(err, ErrNoGPU) {
				t.Fatalf("readGPU = %+v, %v, want ErrNoGPU", got, err)
			}
		})
	}
}

// The meter shows how free the busiest adapter is, since that is the one the
// Overlord's apps are drawing on.
func TestGPUFreeIsOfTheBusiestAdapter(t *testing.T) {
	for _, test := range []struct {
		name string
		gpu  GPU
		want float64
	}{
		{"one busy and one idle", GPU{Adapters: []GPUAdapter{{Name: "Intel(R) Graphics", Busy: 0.7}, {Name: "NVIDIA GeForce RTX 5060 Laptop GPU"}}}, 0.3},
		{"both idle", GPU{Adapters: []GPUAdapter{{Name: "Intel(R) Graphics"}, {Name: "NVIDIA GeForce RTX 5060 Laptop GPU"}}}, 1},
		{"one full", GPU{Adapters: []GPUAdapter{{Name: "Intel(R) Graphics", Busy: 1}}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := test.gpu.Free()

			// Assert
			if math.Abs(got-test.want) > 1e-9 {
				t.Errorf("Free = %v, want %v", got, test.want)
			}
		})
	}
}
