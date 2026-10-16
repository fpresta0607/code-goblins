package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectLaneOverrideKeepsFleetStartFloor(t *testing.T) {
	directory := t.TempDir()
	projectDirectory := filepath.Join(directory, "projects", "project")
	if err := os.MkdirAll(projectDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(directory, "routing.json"):        `{"start_floor_percent":12,"default_lane":"build","lanes":{"build":{"harness":"codex"}}}`,
		filepath.Join(projectDirectory, "project.json"): `{"project":"project","routing":{"default_lane":"local","lanes":{"local":{"harness":"claude","model":"sonnet"}}}}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inputs, err := LoadRouteInputs(directory, filepath.Join(directory, "checkouts", "project"))
	if err != nil || inputs.Table.DefaultLane != "local" || inputs.Table.StartFloorPercent != 12 {
		t.Fatalf("inputs=%+v err=%v", inputs, err)
	}
}
