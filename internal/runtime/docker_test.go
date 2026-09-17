package runtime

import (
	"encoding/json"
	"strings"
	"testing"
)

// inspectFixture is one real `docker inspect` row from this machine, kept
// verbatim in the shape Docker emits. The label values hold Windows paths,
// which is the whole reason this report reads inspect rather than the
// comma-joined labels `docker ps --format` would give it.
const inspectFixture = `[{
  "Id": "b878bab94631aaaabbbbccccddddeeeeffff00001111222233334444555566667777",
  "Name": "/supabase_vector_peakcraftsman-demo",
  "State": {"Status":"restarting","Running":true,"Restarting":true,"StartedAt":"2026-09-17T19:09:22.527044482Z","FinishedAt":"2026-09-17T19:09:22.819739601Z"},
  "RestartCount": 1033,
  "Config": {
    "Image": "public.ecr.aws/supabase/vector:0.53.0-alpine",
    "Labels": {
      "com.supabase.cli.project": "peakcraftsman-demo",
      "com.supabase.cli.workdir": "C:\\dev\\peakCraftsman\\.worktrees\\gb-peak-demo-local"
    }
  },
  "HostConfig": {"Memory": 0},
  "Mounts": [
    {"Type":"volume","Name":"supabase_db_peakcraftsman-demo"},
    {"Type":"volume","Name":"957c4c62a68dcb13948afb14ab5cfb7195c33da3446da406612c6b558806f5d7"},
    {"Type":"bind","Name":""}
  ],
  "NetworkSettings": {"Ports": {"5432/tcp": [
    {"HostIp":"0.0.0.0","HostPort":"54422"},
    {"HostIp":"::","HostPort":"54422"}
  ]}}
}]`

func TestInspectedContainerReadsLabelsPathsAndState(t *testing.T) {
	var rows []inspected
	if err := json.Unmarshal([]byte(inspectFixture), &rows); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	container := rows[0].container()

	if container.Name != "supabase_vector_peakcraftsman-demo" {
		t.Errorf("name = %q, want the leading slash stripped", container.Name)
	}
	if container.Stack != "peakcraftsman-demo" {
		t.Errorf("stack = %q, want the Supabase CLI project label", container.Stack)
	}
	// The path carries no comma here, but it is the field every attribution
	// turns on, so it must survive intact.
	if container.WorkDir != `C:\dev\peakCraftsman\.worktrees\gb-peak-demo-local` {
		t.Errorf("work dir = %q, want the full Windows path", container.WorkDir)
	}
	if !container.RestartLooping() || container.RestartCount != 1033 {
		t.Errorf("restarts = %d state = %q, want a flagged loop", container.RestartCount, container.State)
	}
	if len(container.ID) != 12 {
		t.Errorf("id = %q, want it shortened to 12 characters", container.ID)
	}
	// Docker reports one binding per address family; the same published port
	// must not be printed twice.
	if len(container.Ports) != 1 || container.Ports[0] != "54422->5432/tcp" {
		t.Errorf("ports = %v, want one deduplicated binding", container.Ports)
	}
	// An anonymous volume's name is its own id and carries no ownership at
	// all; only named volumes belong in the report.
	if len(container.Volumes) != 1 || container.Volumes[0] != "supabase_db_peakcraftsman-demo" {
		t.Errorf("volumes = %v, want only the named one", container.Volumes)
	}
	// A restarting container is timed from when it last started, not from
	// the exit a moment later.
	if got := container.Since.Format("15:04:05"); got != "19:09:22" {
		t.Errorf("since = %v, want the start time", container.Since)
	}
}

func TestVolumeStackReadsComposeAndSupabaseNaming(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		// compose: <stack>_<declared name>
		{"gb-siqsermon-draft-flow_qdrant_data", "gb-siqsermon-draft-flow"},
		{"precisiondocs-dev_dev-qdrant-data", "precisiondocs-dev"},
		// supabase: supabase_<service>_<stack>, and its service names hold
		// underscores while its stack names cannot.
		{"supabase_db_peakcraftsman-demo", "peakcraftsman-demo"},
		{"supabase_edge_runtime_peakCraftsman", "peakCraftsman"},
		{"supabase_storage_feat-support-notification-pipeline", "feat-support-notification-pipeline"},
		{"supabase_edge_runtime_", ""},
		// a volume made by hand carries no stack at all
		{"qm-dev-postgres-data", ""},
		{"vbmeasure-hf", ""},
	}
	for _, testCase := range cases {
		if got := (Volume{Name: testCase.name}).Stack(); got != testCase.want {
			t.Errorf("Volume(%q).Stack() = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

// Docker prints human sizes in decimal units, with the reclaimable figure
// carrying a percentage the parse must ignore.
func TestParseSizeReadsDockerSizes(t *testing.T) {
	cases := []struct {
		value string
		want  int64
	}{
		{"72.29GB", 72_290_000_000},
		{"366.1MB", 366_100_000},
		{"783B", 783},
		{"1.5kB", 1_500},
		{"49.02GB (67%)", 49_020_000_000},
		{"0B", 0},
		{"N/A", 0},
		{"", 0},
	}
	for _, testCase := range cases {
		if got := parseSize(testCase.value); got != testCase.want {
			t.Errorf("parseSize(%q) = %d, want %d", testCase.value, got, testCase.want)
		}
	}
}

func TestAnonymousVolumeOnlyMatchesDockersOwnIDs(t *testing.T) {
	id := strings.Repeat("a1b2", 16)
	if !anonymousVolume(id) {
		t.Errorf("anonymousVolume(%q) = false, want true", id)
	}
	for _, name := range []string{"siqsermon_qdrant_data", strings.Repeat("z", 64), id[:63]} {
		if anonymousVolume(name) {
			t.Errorf("anonymousVolume(%q) = true, want false", name)
		}
	}
}
