package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/services"
)

// costs is the measured start costs of one harness, in gigabytes, oldest
// first.
func costs(harness string, gigabytes ...float64) map[string][]StartCost {
	samples := make([]StartCost, len(gigabytes))
	for index, size := range gigabytes {
		samples[index] = StartCost{Bytes: gb(size), Task: "task"}
	}
	return map[string][]StartCost{harness: samples}
}

func TestRoomCostIsTheLargestOfTheLastFiveSamplesOrOneGigabyteWhenNoneIsMeasured(t *testing.T) {
	for _, test := range []struct {
		name    string
		room    Room
		harness string
		want    uint64
	}{
		{name: "no samples", room: Room{}, harness: "claude", want: gigabyte},
		{name: "a harness with none beside one with samples", room: Room{Costs: costs("claude", 3)}, harness: "codex", want: gigabyte},
		{name: "one sample", room: Room{Costs: costs("claude", 2.5)}, harness: "claude", want: gb(2.5)},
		{name: "the largest of several", room: Room{Costs: costs("claude", 1.5, 2.5, 2)}, harness: "claude", want: gb(2.5)},
		{name: "a larger sample older than the last five", room: Room{Costs: costs("claude", 6, 1, 1, 1, 1, 1.5)}, harness: "claude", want: gb(1.5)},
		{name: "a sample smaller than the unmeasured cost", room: Room{Costs: costs("pi", 0.4)}, harness: "pi", want: gb(0.4)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := test.room.Cost(test.harness)

			// Assert
			if got != test.want {
				t.Fatalf("Cost(%s) = %d, want %d", test.harness, got, test.want)
			}
		})
	}
}

func TestRoomNeedIsTheFloorWhatIsHeldAndTheStartsOwnCost(t *testing.T) {
	for _, test := range []struct {
		name string
		room Room
		want uint64
	}{
		{name: "nothing measured or held", room: Room{Floor: 4 * gigabyte}, want: 5 * gigabyte},
		{name: "a measured cost", room: Room{Floor: 4 * gigabyte, Costs: costs("claude", 2.5)}, want: gb(6.5)},
		{name: "a start still settling", room: Room{Floor: 4 * gigabyte, Costs: costs("claude", 2.5), Pending: []PendingStart{{Task: "a", Harness: "claude", Remaining: gigabyte}, {Task: "b", Harness: "codex", Remaining: gigabyte / 2}}}, want: 8 * gigabyte},
		{name: "a services start", room: Room{Floor: 4 * gigabyte, Reserved: []ReservedStart{{Task: "a", Project: "p", Cost: gb(3.5)}}}, want: gb(8.5)},
		{name: "a floor from settings", room: Room{Floor: 6 * gigabyte}, want: 7 * gigabyte},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := test.room.Need("claude")

			// Assert
			if got != test.want {
				t.Fatalf("Need(claude) = %d, want %d", got, test.want)
			}
		})
	}
}

func TestRoomShortfallSaysWhyTheFreeMemoryIsUnderWhatAStartNeeds(t *testing.T) {
	room := Room{
		Floor:    4 * gigabyte,
		Costs:    costs("claude", 2.5),
		Pending:  []PendingStart{{Task: "a", Harness: "claude", Remaining: gigabyte}},
		Reserved: []ReservedStart{{Task: "b", Project: "p", Cost: gb(3.5)}},
	}
	for _, test := range []struct {
		name   string
		room   Room
		memory Memory
		want   []string
	}{
		{name: "it fits", room: Room{Floor: 4 * gigabyte}, memory: Memory{Available: gb(5), CommitAvailable: gb(5)}},
		{name: "memory is short", room: Room{Floor: 4 * gigabyte}, memory: Memory{Available: gb(4.95), CommitAvailable: gb(9)}, want: []string{"Only 4.9 GB of memory is free", "a claude start needs 5.0 GB", "floor 4.0", "start 1.0"}},
		{name: "commit is short", room: Room{Floor: 4 * gigabyte}, memory: Memory{Available: gb(9), CommitAvailable: gb(4.95)}, want: []string{"Only 4.9 GB of commit (RAM plus page file) is free"}},
		{name: "both are short", room: Room{Floor: 4 * gigabyte}, memory: Memory{Available: gb(3), CommitAvailable: gb(2)}, want: []string{"Only 3.0 GB of memory and 2.0 GB of commit (RAM plus page file) are free"}},
		{name: "what starts hold is named", room: room, memory: Memory{Available: gb(9), CommitAvailable: gb(9)}, want: []string{"Only 9.0 GB of memory and 9.0 GB of commit (RAM plus page file) are free", "a claude start needs 11.0 GB", "floor 4.0", "start 2.5", "goblins starting 1.0", "services starting 3.5"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := test.room.Shortfall(test.memory, "claude")

			// Assert
			if len(test.want) == 0 && got != "" {
				t.Fatalf("Shortfall = %q, want it to fit", got)
			}
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Errorf("Shortfall = %q, lacks %q", got, want)
				}
			}
			if strings.ContainsAny(got, ";") || strings.ContainsRune(got, 0x2014) {
				t.Errorf("Shortfall = %q, which the Overlord reads, has a semicolon or an em dash", got)
			}
		})
	}
}

func TestWriteStartCostKeepsTheLastFivePerHarness(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	at := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)

	// Act
	for index := range 7 {
		if err := writeStartCost(stateDir, "claude", StartCost{Bytes: uint64(index+1) * gigabyte, At: at.Add(time.Duration(index) * time.Hour), Task: "task"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeStartCost(stateDir, "codex", StartCost{Bytes: gigabyte / 2, At: at, Task: "other"}); err != nil {
		t.Fatal(err)
	}
	got, err := readStartCosts(stateDir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(got["claude"]) != 5 || got["claude"][0].Bytes != 3*gigabyte || got["claude"][4].Bytes != 7*gigabyte {
		t.Errorf("claude samples = %+v, want the last five, 3 GB through 7 GB", got["claude"])
	}
	if len(got["codex"]) != 1 || got["codex"][0].Task != "other" || !got["codex"][0].At.Equal(at) {
		t.Errorf("codex samples = %+v, want its one", got["codex"])
	}
	if room := (Room{Costs: got}); room.Cost("claude") != 7*gigabyte {
		t.Errorf("claude cost = %d, want the largest kept", room.Cost("claude"))
	}
}

func TestReadStartCostsRefusesAFileItCannotRead(t *testing.T) {
	for _, test := range []struct {
		name, content string
	}{
		{name: "not JSON", content: "{"},
		{name: "another schema", content: `{"schema":"other","harnesses":{"claude":[{"bytes":1}]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			writeFile(t, startCostsPath(stateDir), test.content)

			// Act
			_, err := readStartCosts(stateDir)

			// Assert
			if err == nil {
				t.Fatal("an unreadable start costs file was read as none")
			}
		})
	}
	if got, err := readStartCosts(t.TempDir()); err != nil || got != nil {
		t.Fatalf("a home that measured none gave %v, %v, want none", got, err)
	}
}

// roomHome is a home whose settings file holds settings, or none when empty.
func roomHome(t *testing.T, settings string) home.Home {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		writeFile(t, filepath.Join(root, "config", "fleet.json"), settings)
	}
	return h
}

func TestReadRoomTakesTheFloorFromTheHomesSettingsAndTheCostsFromItsState(t *testing.T) {
	for _, test := range []struct {
		name, settings string
		wantFloor      uint64
	}{
		{name: "no settings file", wantFloor: 4 * gigabyte},
		{name: "a floor the file sets", settings: `{"memory_floor_gb":6.5}`, wantFloor: gb(6.5)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := roomHome(t, test.settings)
			if err := writeStartCost(h.State, "claude", StartCost{Bytes: 2 * gigabyte, Task: "task"}); err != nil {
				t.Fatal(err)
			}

			// Act
			room, err := ReadRoom(h, nil, time.Now())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if room.Floor != test.wantFloor || room.Cost("claude") != 2*gigabyte || room.Cost("codex") != gigabyte || len(room.Pending) != 0 || len(room.Reserved) != 0 {
				t.Fatalf("room = %+v, want floor %d, claude at 2 GB, codex unmeasured and nothing held", room, test.wantFloor)
			}
		})
	}
}

func TestReadRoomRefusesWhenTheSettingsCannotBeReadAndNeverUsesADefault(t *testing.T) {
	for _, settings := range []string{`{"memory_floor_gb":0}`, `{"memory_floor_gb":-4}`, `{"memory":4}`, `[`} {
		t.Run(settings, func(t *testing.T) {
			// Arrange
			h := roomHome(t, settings)

			// Act
			_, err := ReadRoom(h, nil, time.Now())

			// Assert
			if err == nil {
				t.Fatalf("settings %s were read as a room", settings)
			}
		})
	}
}

// A services start that waits for memory or is starting holds the memory it
// was admitted with against every goblin that would start or resume (#2695),
// for as long as its process runs, and not once that process is gone.
func TestReadRoomHoldsTheCostOfALiveServicesStartAndIgnoresADeadOne(t *testing.T) {
	own, isKnown := proc.StartTime(os.Getpid())
	if !isKnown {
		t.Skip("this system cannot read a process's start")
	}
	for _, test := range []struct {
		name      string
		admitting services.Admitting
		want      []ReservedStart
	}{
		{name: "its process runs", admitting: services.Admitting{Task: "task-a", Cost: gb(3.5), PID: os.Getpid(), Started: own}, want: []ReservedStart{{Task: "task-a", Project: "Example", Cost: gb(3.5)}}},
		{name: "its pid runs another process now", admitting: services.Admitting{Task: "task-a", Cost: 3 * gigabyte, PID: os.Getpid(), Started: own.Add(-time.Hour)}},
		{name: "its process is gone", admitting: services.Admitting{Task: "task-a", Cost: 3 * gigabyte, PID: 1<<31 - 2, Started: own}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := roomHome(t, "")
			if err := services.WriteRecord(h.State, services.Record{Admitting: map[string]services.Admitting{"Example": test.admitting}}); err != nil {
				t.Fatal(err)
			}

			// Act
			room, err := ReadRoom(h, nil, time.Now())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(room.Reserved) != len(test.want) || len(test.want) == 1 && room.Reserved[0] != test.want[0] {
				t.Fatalf("reserved = %+v, want %+v", room.Reserved, test.want)
			}
			wantNeed := 5*gigabyte + room.reserved()
			if got := room.Need("claude"); got != wantNeed {
				t.Fatalf("Need = %d, want %d", got, wantNeed)
			}
		})
	}
}

// gb is size gigabytes as a byte count.
func gb(size float64) uint64 { return uint64(size * gigabyte) }
